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
// console use NewKeyTokens, or WithKey.
func StaticToken(token string) TokenSource {
	return TokenSourceFunc(func(context.Context) (Token, error) {
		return Token{Value: token}, nil
	})
}

// A TokenOption changes where NewKeyTokens or NewActorTokens exchanges
// tokens.
type TokenOption func(*tokenOptions) error

// tokenOptions are what the TokenOptions set.
type tokenOptions struct {
	issuer   string
	audience string
}

// WithIssuer exchanges tokens at the sign-in service at rawURL instead of
// DefaultIssuer, for a self-hosted installation or a test server.
func WithIssuer(rawURL string) TokenOption {
	return func(o *tokenOptions) error {
		u, err := parseBaseURL(rawURL)
		if err != nil {
			return fmt.Errorf("latere: WithIssuer: %w", err)
		}
		o.issuer = u.String()
		return nil
	}
}

// WithAudience asks NewActorTokens for tokens addressed to audience instead
// of DefaultAudience. NewKeyTokens refuses it, since a key's tokens are
// always addressed to the API.
func WithAudience(audience string) TokenOption {
	return func(o *tokenOptions) error {
		if audience == "" {
			return errors.New("latere: WithAudience: the audience is empty")
		}
		o.audience = audience
		return nil
	}
}

// applyTokenOptions returns the options applied over the defaults.
func applyTokenOptions(opts []TokenOption) (tokenOptions, error) {
	o := tokenOptions{issuer: DefaultIssuer, audience: DefaultAudience}
	for _, opt := range opts {
		if err := opt(&o); err != nil {
			return o, err
		}
	}
	return o, nil
}

// transport is what a token exchange is sent with: the HTTP client,
// User-Agent and hooks of the Config the token source was given to.
type transport struct {
	client    *http.Client
	userAgent string
	hooks     Hooks
}

// exchanger is a token source of this package that sends exchanges of its
// own. NewConfig binds it to the Config's transport.
type exchanger interface {
	bind(t *transport)
}

// boundTransport holds the transport of an exchanger: the one of the first
// Config it was given to, or the defaults before that.
type boundTransport struct {
	mu sync.Mutex
	t  *transport
}

// bind keeps t unless a Config bound the source first.
func (b *boundTransport) bind(t *transport) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.t == nil {
		b.t = t
	}
}

// get returns the bound transport, or the defaults.
func (b *boundTransport) get() *transport {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.t == nil {
		return &transport{client: http.DefaultClient, userAgent: defaultUserAgent()}
	}
	return b.t
}

// KeyTokens returns API tokens for a key created under Keys in the console.
// A key is exchanged at the issuer's token endpoint (an OAuth 2.0 client
// credentials grant, with the key as the secret of the shared client id
// "pat") for a token of about 15 minutes that acts as the key's owner, and
// exchanged again shortly before that token expires. Requests carry that
// token, not the key, except a request with Request.SendKey set: the models
// capability authenticates a key as itself. Build one with NewKeyTokens, or
// let WithKey build it.
//
// Its exchanges go through the HTTP client, User-Agent and hooks of the
// Config it is given to, the first one when it is given to several.
type KeyTokens struct {
	key    string
	issuer string
	tr     boundTransport
	reuse  *ReuseTokenSource
}

// NewKeyTokens returns a KeyTokens for key, whose value starts with "pat_".
// It returns an error when key is empty or an option is invalid; the key
// itself is first checked by the issuer, on the first request.
func NewKeyTokens(key string, opts ...TokenOption) (*KeyTokens, error) {
	if key == "" {
		return nil, errors.New("latere: NewKeyTokens: the key is empty")
	}
	o, err := applyTokenOptions(opts)
	if err != nil {
		return nil, err
	}
	if o.audience != DefaultAudience {
		return nil, errors.New("latere: NewKeyTokens: a key's tokens are addressed to the API; WithAudience applies to NewActorTokens")
	}
	k := &KeyTokens{key: key, issuer: o.issuer}
	k.reuse = ReuseToken(TokenSourceFunc(k.exchange), actorEarly)
	return k, nil
}

// Token returns a token for the key, exchanging the key when the held
// token is about to expire. A refusal from the issuer, such as a revoked
// key, is an *Error.
func (k *KeyTokens) Token(ctx context.Context) (Token, error) { return k.reuse.Token(ctx) }

// Reset drops the held token, so the next call exchanges the key again.
func (k *KeyTokens) Reset() { k.reuse.Reset() }

// platformKey returns the key itself, for a request with Request.SendKey.
func (k *KeyTokens) platformKey() string { return k.key }

// keyHolder is a token source that holds a platform key, which a request
// with Request.SendKey sends as itself.
type keyHolder interface {
	platformKey() string
}

func (k *KeyTokens) bind(t *transport) { k.tr.bind(t) }

// exchange performs one client credentials grant with the key.
func (k *KeyTokens) exchange(ctx context.Context) (Token, error) {
	form := url.Values{"grant_type": {"client_credentials"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, k.issuer+"/token", strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, fmt.Errorf("latere: KeyTokens: %w", err)
	}
	req.SetBasicAuth("pat", k.key)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	start := time.Now()
	if err := exchange(k.tr.get(), req, &out); err != nil {
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
	source  TokenSource
	early   time.Duration
	timeout time.Duration
	now     func() time.Time

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

// TokenTimeout bounds one request of a ReuseTokenSource to its source.
// The request is shared by every caller waiting for a token, so no one
// caller's context bounds it; a source that has not answered by then
// fails the waiting callers, and the next call asks it again.
const TokenTimeout = 30 * time.Second

// ReuseToken returns a ReuseTokenSource over src that replaces a token
// early before it expires, so a request does not reach the API with a
// token that lapses on the way.
func ReuseToken(src TokenSource, early time.Duration) *ReuseTokenSource {
	return &ReuseTokenSource{source: src, early: early, timeout: TokenTimeout, now: time.Now}
}

// Token returns the held token while it has more than the early margin left,
// and otherwise a new one from the source. A caller whose context ends while
// the request to the source is in flight returns the context's error; the
// request itself goes on for the others, up to TokenTimeout.
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
		// cancellation and deadline, since other callers share the answer;
		// fetch bounds it instead.
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
// call. A source that ignores its context's deadline is left behind once
// the deadline passes, so it cannot hold every later caller.
func (r *ReuseTokenSource) fetch(ctx context.Context, call *tokenCall) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	type answer struct {
		token Token
		err   error
	}
	answers := make(chan answer, 1)
	go func() {
		t, err := r.source.Token(ctx)
		answers <- answer{t, err}
	}()
	var a answer
	select {
	case a = <-answers:
	case <-ctx.Done():
		a.err = fmt.Errorf("latere: the token source did not answer within %v: %w", r.timeout, ctx.Err())
	}
	r.mu.Lock()
	if a.err == nil {
		r.token = a.token
	}
	r.inflight = nil
	r.mu.Unlock()
	call.token, call.err = a.token, a.err
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
// in. It exchanges the person's sign-in token at the issuer's /actor-tokens
// endpoint for a token of a few minutes addressed to the API, and reuses
// that token until shortly before it expires. Use it with WithTokenSource
// in a program that acts for a person, such as a desktop app; package
// signin obtains and refreshes the sign-in token. Build one with
// NewActorTokens.
//
// Its exchanges go through the HTTP client, User-Agent and hooks of the
// Config it is given to, the first one when it is given to several.
type ActorTokens struct {
	session  TokenSource
	issuer   string
	audience string
	tr       boundTransport
	reuse    *ReuseTokenSource
}

// NewActorTokens returns an ActorTokens over session, the source of the
// person's sign-in token, such as signin.Config.TokenSource returns. It
// returns an error when session is nil or an option is invalid.
func NewActorTokens(session TokenSource, opts ...TokenOption) (*ActorTokens, error) {
	if session == nil {
		return nil, errors.New("latere: NewActorTokens: the session token source is nil")
	}
	o, err := applyTokenOptions(opts)
	if err != nil {
		return nil, err
	}
	a := &ActorTokens{session: session, issuer: o.issuer, audience: o.audience}
	a.reuse = ReuseToken(TokenSourceFunc(a.mint), actorEarly)
	return a, nil
}

// actorLifetime is the lifetime asked of the issuer. The issuer may grant
// less, and the expiry it answers with is the one kept.
const actorLifetime = 5 * time.Minute

// actorEarly is how long before expiry a minted token is replaced: enough
// for a request and its retries to reach the API before the token lapses.
const actorEarly = 30 * time.Second

// Token returns a token addressed to the audience, minting one when the
// held one is about to expire. It returns an error when the session source
// fails or the issuer refuses the exchange; a refusal from the issuer is an
// *Error.
func (a *ActorTokens) Token(ctx context.Context) (Token, error) { return a.reuse.Token(ctx) }

// Reset drops the held token, and the session's if it can drop it too.
func (a *ActorTokens) Reset() {
	a.reuse.Reset()
	if rs, ok := a.session.(Resetter); ok {
		rs.Reset()
	}
}

func (a *ActorTokens) bind(t *transport) { a.tr.bind(t) }

// mint performs one exchange at the issuer.
func (a *ActorTokens) mint(ctx context.Context) (Token, error) {
	session, err := a.session.Token(ctx)
	if err != nil {
		return Token{}, fmt.Errorf("latere: ActorTokens: the session token: %w", err)
	}
	body, err := json.Marshal(map[string]any{"audience": a.audience, "ttl_seconds": int(actorLifetime / time.Second)})
	if err != nil {
		return Token{}, fmt.Errorf("latere: ActorTokens: encoding the exchange: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.issuer+"/actor-tokens", bytes.NewReader(body))
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
	if err := exchange(a.tr.get(), req, &out); err != nil {
		return Token{}, fmt.Errorf("latere: ActorTokens: %w", err)
	}
	if out.ActorToken == "" || out.ExpiresIn <= 0 {
		return Token{}, errors.New("latere: ActorTokens: the issuer returned no token")
	}
	// The lifetime counts from when the exchange was sent, which errs early.
	return Token{Value: out.ActorToken, Expiry: start.Add(time.Duration(out.ExpiresIn) * time.Second)}, nil
}

// exchange sends a token request to the issuer with t and decodes its JSON
// answer into out. A refusal becomes an *Error, from the API's envelope or
// from the OAuth form {"error", "error_description"}.
func exchange(t *transport, req *http.Request, out any) error {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", t.userAgent)
	if t.hooks.BeforeRequest != nil {
		t.hooks.BeforeRequest(req)
	}
	start := time.Now()
	resp, err := t.client.Do(req)
	if t.hooks.AfterResponse != nil {
		t.hooks.AfterResponse(req, resp, err, time.Since(start))
	}
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
