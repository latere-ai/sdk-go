// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package latere

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// DefaultIssuer is the address of Latere's sign-in service, which issues
// the tokens a person signs in with and exchanges them for API tokens.
const DefaultIssuer = "https://auth.latere.ai"

// DefaultAudience is the audience of a token the API accepts.
const DefaultAudience = "api.latere.ai"

// A Token is a credential sent as "Authorization: Bearer <Value>".
type Token struct {
	// Value is the token itself.
	Value string
	// Expiry is when the token stops working; zero when unknown, in which
	// case the token is used until the API refuses it.
	Expiry time.Time
}

// validFor reports whether t has a value and will not expire within margin
// of now.
func (t Token) validFor(now time.Time, margin time.Duration) bool {
	return t.Value != "" && (t.Expiry.IsZero() || now.Add(margin).Before(t.Expiry))
}

// A TokenSource returns the token a request is sent with. The SDK asks for
// a token before every attempt, so a source that mints tokens caches them.
// Implementations must be safe for concurrent use.
type TokenSource interface {
	Token(ctx context.Context) (Token, error)
}

// TokenSourceFunc adapts a function to a TokenSource.
type TokenSourceFunc func(ctx context.Context) (Token, error)

// Token calls f.
func (f TokenSourceFunc) Token(ctx context.Context) (Token, error) { return f(ctx) }

// A Resetter is a TokenSource that can drop the token it holds. When the
// API answers 401, the SDK calls Reset and sends the request once more with
// a fresh token, since a held token may have been revoked or may have
// expired early by the server's clock.
type Resetter interface {
	Reset()
}

// StaticToken returns a TokenSource that always returns token, a bearer
// token you obtained elsewhere and renew yourself. For a key from the
// console use KeyTokens, or WithKey.
func StaticToken(token string) TokenSource {
	return TokenSourceFunc(func(context.Context) (Token, error) {
		return Token{Value: token}, nil
	})
}

// KeyTokens returns API tokens for a key created under Keys in the console.
// A key is not sent with requests: it is exchanged at the issuer's token
// endpoint (an OAuth 2.0 client credentials grant, with the key as the
// secret of the shared client id "pat") for a token of about 15 minutes
// that acts as the key's owner, and exchanged again shortly before that
// token expires. WithKey builds one.
//
// The fields are read on first use and must not change after.
type KeyTokens struct {
	// Key is the key's value, which starts with "pat_".
	Key string
	// Issuer is the sign-in service; DefaultIssuer when empty.
	Issuer string
	// HTTPClient sends the exchange; http.DefaultClient when nil.
	HTTPClient *http.Client

	once  sync.Once
	reuse *ReuseTokenSource
}

// Token returns a token for the key, exchanging the key when the held
// token is about to expire. A refusal from the issuer, such as a revoked
// key, is an *Error.
func (k *KeyTokens) Token(ctx context.Context) (Token, error) {
	k.once.Do(func() { k.reuse = ReuseToken(TokenSourceFunc(k.exchange), actorEarly) })
	return k.reuse.Token(ctx)
}

// Reset drops the held token, so the next call exchanges the key again.
func (k *KeyTokens) Reset() {
	k.once.Do(func() { k.reuse = ReuseToken(TokenSourceFunc(k.exchange), actorEarly) })
	k.reuse.Reset()
}

// exchange performs one client credentials grant with the key.
func (k *KeyTokens) exchange(ctx context.Context) (Token, error) {
	if k.Key == "" {
		return Token{}, errors.New("latere: KeyTokens: Key is empty")
	}
	form := url.Values{"grant_type": {"client_credentials"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, issuerURL(k.Issuer)+"/token", strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, fmt.Errorf("latere: KeyTokens: %w", err)
	}
	req.SetBasicAuth("pat", k.Key)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	start := time.Now()
	if err := exchange(k.HTTPClient, req, &out); err != nil {
		return Token{}, fmt.Errorf("latere: KeyTokens: %w", err)
	}
	if out.AccessToken == "" || out.ExpiresIn <= 0 {
		return Token{}, errors.New("latere: KeyTokens: the issuer returned no token")
	}
	return Token{Value: out.AccessToken, Expiry: start.Add(time.Duration(out.ExpiresIn) * time.Second)}, nil
}

// ReuseTokenSource holds the token its source returned and returns it until
// it is about to expire. However many requests need a new token at once, it
// asks the source once and they all wait for that answer. Build one with
// ReuseToken.
type ReuseTokenSource struct {
	source TokenSource
	early  time.Duration
	now    func() time.Time

	mu       sync.Mutex
	token    Token
	inflight *tokenCall
}

// tokenCall is one request to the source that concurrent callers share.
type tokenCall struct {
	done  chan struct{}
	token Token
	err   error
}

// ReuseToken returns a ReuseTokenSource over src that replaces a token
// early before it expires, so a request does not reach the API with a
// token that lapses on the way.
func ReuseToken(src TokenSource, early time.Duration) *ReuseTokenSource {
	return &ReuseTokenSource{source: src, early: early, now: time.Now}
}

// Token returns the held token while it has more than the early margin left,
// and otherwise a new one from the source. A caller whose context ends while
// another caller's request to the source is in flight returns the
// context's error; the request itself goes on for the others.
func (r *ReuseTokenSource) Token(ctx context.Context) (Token, error) {
	r.mu.Lock()
	if r.token.validFor(r.now(), r.early) {
		t := r.token
		r.mu.Unlock()
		return t, nil
	}
	call := r.inflight
	if call == nil {
		call = &tokenCall{done: make(chan struct{})}
		r.inflight = call
		// The source is asked with a context detached from this caller's
		// cancellation, since other callers share the answer.
		go r.fetch(context.WithoutCancel(ctx), call)
	}
	r.mu.Unlock()
	select {
	case <-call.done:
		return call.token, call.err
	case <-ctx.Done():
		return Token{}, ctx.Err()
	}
}

// fetch asks the source for a token and hands it to every caller waiting on
// call.
func (r *ReuseTokenSource) fetch(ctx context.Context, call *tokenCall) {
	t, err := r.source.Token(ctx)
	r.mu.Lock()
	if err == nil {
		r.token = t
	}
	r.inflight = nil
	r.mu.Unlock()
	call.token, call.err = t, err
	close(call.done)
}

// Reset drops the held token, so the next call asks the source.
func (r *ReuseTokenSource) Reset() {
	r.mu.Lock()
	r.token = Token{}
	r.mu.Unlock()
	if rs, ok := r.source.(Resetter); ok {
		rs.Reset()
	}
}

// ActorTokens returns tokens addressed to the API for a person who signed
// in. It exchanges the person's sign-in token, from Session, at the
// issuer's /actor-tokens endpoint for a token of a few minutes addressed to
// Audience, and reuses that token until shortly before it expires. Use it
// with WithTokenSource in a program that acts for a person, such as a
// desktop app; package signin obtains and refreshes the Session token.
//
// The fields are read on first use and must not change after.
type ActorTokens struct {
	// Session returns the person's sign-in token, addressed to the issuer.
	Session TokenSource
	// Issuer is the sign-in service; DefaultIssuer when empty.
	Issuer string
	// Audience is who the minted token is addressed to; DefaultAudience
	// when empty.
	Audience string
	// HTTPClient sends the exchange; http.DefaultClient when nil.
	HTTPClient *http.Client

	once  sync.Once
	reuse *ReuseTokenSource
}

// actorLifetime is the lifetime asked of the issuer. The issuer may grant
// less, and the expiry it answers with is the one kept.
const actorLifetime = 5 * time.Minute

// actorEarly is how long before expiry a minted token is replaced: enough
// for a request and its retries to reach the API before the token lapses.
const actorEarly = 30 * time.Second

// Token returns a token addressed to the audience, minting one when the
// held one is about to expire. It returns an error when Session is nil,
// when Session fails, or when the issuer refuses the exchange; a refusal
// from the issuer is an *Error.
func (a *ActorTokens) Token(ctx context.Context) (Token, error) {
	a.once.Do(func() {
		a.reuse = ReuseToken(TokenSourceFunc(a.mint), actorEarly)
	})
	return a.reuse.Token(ctx)
}

// Reset drops the held token, and the session's if it can drop it too.
func (a *ActorTokens) Reset() {
	a.once.Do(func() {
		a.reuse = ReuseToken(TokenSourceFunc(a.mint), actorEarly)
	})
	a.reuse.Reset()
	if rs, ok := a.Session.(Resetter); ok {
		rs.Reset()
	}
}

// mint performs one exchange at the issuer.
func (a *ActorTokens) mint(ctx context.Context) (Token, error) {
	if a.Session == nil {
		return Token{}, errors.New("latere: ActorTokens: Session is nil")
	}
	session, err := a.Session.Token(ctx)
	if err != nil {
		return Token{}, fmt.Errorf("latere: ActorTokens: the session token: %w", err)
	}
	audience := a.Audience
	if audience == "" {
		audience = DefaultAudience
	}
	body, err := json.Marshal(map[string]any{"audience": audience, "ttl_seconds": int(actorLifetime / time.Second)})
	if err != nil {
		return Token{}, fmt.Errorf("latere: ActorTokens: encoding the exchange: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, issuerURL(a.Issuer)+"/actor-tokens", bytes.NewReader(body))
	if err != nil {
		return Token{}, fmt.Errorf("latere: ActorTokens: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+session.Value)
	req.Header.Set("Content-Type", "application/json")
	var out struct {
		ActorToken string `json:"actor_token"`
		ExpiresIn  int64  `json:"expires_in"`
	}
	start := time.Now()
	if err := exchange(a.HTTPClient, req, &out); err != nil {
		return Token{}, fmt.Errorf("latere: ActorTokens: %w", err)
	}
	if out.ActorToken == "" || out.ExpiresIn <= 0 {
		return Token{}, errors.New("latere: ActorTokens: the issuer returned no token")
	}
	// The lifetime counts from when the exchange was sent, which errs early.
	return Token{Value: out.ActorToken, Expiry: start.Add(time.Duration(out.ExpiresIn) * time.Second)}, nil
}

// issuerURL returns issuer without a trailing slash, or DefaultIssuer.
func issuerURL(issuer string) string {
	if issuer == "" {
		return DefaultIssuer
	}
	return strings.TrimSuffix(issuer, "/")
}

// exchange sends a token request to the issuer and decodes its JSON answer
// into out. A refusal becomes an *Error, from the API's envelope or from
// the OAuth form {"error", "error_description"}.
func exchange(hc *http.Client, req *http.Request, out any) error {
	if hc == nil {
		hc = http.DefaultClient
	}
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer closeBody(resp)
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	if err != nil {
		return fmt.Errorf("reading the answer: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		e := newError(resp, raw)
		var oauth struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		if e.Code == "" && json.Unmarshal(raw, &oauth) == nil && oauth.Error != "" {
			e.Code, e.Message = oauth.Error, oauth.Description
		}
		return e
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decoding the answer: %w", err)
	}
	return nil
}

// closeBody drains up to a bounded amount of a response body, so the
// connection can be reused, and closes it. Neither failure changes the
// outcome of a call whose answer was already read, so both are dropped.
func closeBody(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBody))
	_ = resp.Body.Close()
}
