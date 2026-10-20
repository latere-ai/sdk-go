// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package latere

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStaticToken(t *testing.T) {
	tok, err := StaticToken("key_1").Token(t.Context())
	if err != nil || tok.Value != "key_1" || !tok.Expiry.IsZero() {
		t.Errorf("StaticToken = %+v, %v", tok, err)
	}
}

func TestReuseTokenAsksTheSourceOnceAtATime(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	src := TokenSourceFunc(func(context.Context) (Token, error) {
		n := calls.Add(1)
		<-release
		return Token{Value: fmt.Sprintf("tok_%d", n), Expiry: time.Now().Add(time.Hour)}, nil
	})
	r := ReuseToken(src, time.Minute)
	var wg sync.WaitGroup
	got := make([]string, 20)
	for i := range got {
		wg.Go(func() {
			tok, err := r.Token(t.Context())
			if err != nil {
				t.Error(err)
			}
			got[i] = tok.Value
		})
	}
	// Let every caller reach the wait before the one request answers.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Errorf("20 concurrent callers asked the source %d times, want 1", calls.Load())
	}
	for i, v := range got {
		if v != "tok_1" {
			t.Errorf("caller %d got %q", i, v)
		}
	}
	if tok, _ := r.Token(t.Context()); tok.Value != "tok_1" || calls.Load() != 1 {
		t.Errorf("a valid token was not reused: %q after %d calls", tok.Value, calls.Load())
	}
}

func TestReuseTokenReplacesATokenAboutToExpire(t *testing.T) {
	var calls atomic.Int32
	src := TokenSourceFunc(func(context.Context) (Token, error) {
		n := calls.Add(1)
		return Token{Value: fmt.Sprintf("tok_%d", n), Expiry: time.Now().Add(30 * time.Second)}, nil
	})
	r := ReuseToken(src, time.Minute)
	a, _ := r.Token(t.Context())
	b, _ := r.Token(t.Context())
	if a.Value == b.Value || calls.Load() != 2 {
		t.Errorf("a token inside the margin was reused: %q then %q", a.Value, b.Value)
	}
}

func TestReuseTokenKeepsNoFailure(t *testing.T) {
	boom := errors.New("down")
	var fail atomic.Bool
	fail.Store(true)
	src := TokenSourceFunc(func(context.Context) (Token, error) {
		if fail.Load() {
			return Token{}, boom
		}
		return Token{Value: "tok", Expiry: time.Now().Add(time.Hour)}, nil
	})
	r := ReuseToken(src, time.Minute)
	if _, err := r.Token(t.Context()); !errors.Is(err, boom) {
		t.Fatalf("the source's error: %v", err)
	}
	fail.Store(false)
	if tok, err := r.Token(t.Context()); err != nil || tok.Value != "tok" {
		t.Errorf("after a failure: %+v, %v", tok, err)
	}
}

func TestReuseTokenHonorsTheCallersContext(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	r := ReuseToken(TokenSourceFunc(func(context.Context) (Token, error) {
		<-block
		return Token{Value: "late"}, nil
	}), 0)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := r.Token(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a caller whose context ended: %v", err)
	}
}

func TestReuseTokenResetPassesOn(t *testing.T) {
	src := &resetSource{}
	r := ReuseToken(src, time.Minute)
	a, _ := r.Token(t.Context())
	r.Reset()
	b, _ := r.Token(t.Context())
	if a.Value == b.Value || src.resets.Load() != 1 {
		t.Errorf("after Reset: %q then %q, %d resets passed on", a.Value, b.Value, src.resets.Load())
	}
}

func TestReuseTokenLeavesAHungSourceBehind(t *testing.T) {
	var calls atomic.Int32
	block := make(chan struct{})
	defer close(block)
	// The first request to the source ignores its context and never
	// answers; the next caller must not wait on it forever.
	r := ReuseToken(TokenSourceFunc(func(context.Context) (Token, error) {
		if calls.Add(1) == 1 {
			<-block
		}
		return Token{Value: "tok", Expiry: time.Now().Add(time.Hour)}, nil
	}), time.Second)
	r.timeout = 50 * time.Millisecond
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	if _, err := r.Token(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a caller whose context ended: %v", err)
	}
	cancel()
	time.Sleep(100 * time.Millisecond)
	tok, err := r.Token(t.Context())
	if err != nil || tok.Value != "tok" || calls.Load() != 2 {
		t.Errorf("after the hung request's bound: %+v, %v, source asked %d times", tok, err, calls.Load())
	}
}

func TestReuseTokenBoundsTheSharedRequest(t *testing.T) {
	r := ReuseToken(TokenSourceFunc(func(ctx context.Context) (Token, error) {
		<-ctx.Done()
		return Token{}, ctx.Err()
	}), 0)
	r.timeout = 20 * time.Millisecond
	if _, err := r.Token(t.Context()); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a source that answers no one: %v", err)
	}
}

func newActorTokens(t *testing.T, session TokenSource, opts ...TokenOption) *ActorTokens {
	t.Helper()
	a, err := NewActorTokens(session, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func newKeyTokens(t *testing.T, key string, opts ...TokenOption) *KeyTokens {
	t.Helper()
	k, err := NewKeyTokens(key, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestActorTokensMintsAndReuses(t *testing.T) {
	var mints atomic.Int32
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/actor-tokens" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer session_tok" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "No session.")
			return
		}
		var in struct {
			Audience string `json:"audience"`
			TTL      int    `json:"ttl_seconds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Audience != "api.example.test" || in.TTL != 300 {
			t.Errorf("exchange body %+v, %v", in, err)
		}
		n := mints.Add(1)
		fmt.Fprintf(w, `{"actor_token":"actor_%d","expires_in":300}`, n)
	}))
	defer issuer.Close()
	a := newActorTokens(t, StaticToken("session_tok"), WithIssuer(issuer.URL+"/"), WithAudience("api.example.test"))
	tok, err := a.Token(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if tok.Value != "actor_1" || time.Until(tok.Expiry) < 4*time.Minute {
		t.Errorf("minted %+v", tok)
	}
	if again, _ := a.Token(t.Context()); again.Value != "actor_1" || mints.Load() != 1 {
		t.Errorf("a fresh actor token was minted again: %q", again.Value)
	}
	a.Reset()
	if after, _ := a.Token(t.Context()); after.Value != "actor_2" {
		t.Errorf("after Reset: %q, want a new mint", after.Value)
	}
}

func TestActorTokensFailures(t *testing.T) {
	refuse := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusBadRequest, "invalid_target", "Not an audience of this client.")
	}))
	defer refuse.Close()
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"actor_token":"","expires_in":0}`)
	}))
	defer empty.Close()
	garbled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{`)
	}))
	defer garbled.Close()
	boom := errors.New("signed out")

	var apiErr *Error
	if _, err := newActorTokens(t, StaticToken("s"), WithIssuer(refuse.URL)).Token(t.Context()); !errors.As(err, &apiErr) || apiErr.Code != "invalid_target" {
		t.Errorf("a refused exchange: %v", err)
	}
	if _, err := newActorTokens(t, StaticToken("s"), WithIssuer(empty.URL)).Token(t.Context()); err == nil {
		t.Error("an empty answer: no error")
	}
	if _, err := newActorTokens(t, StaticToken("s"), WithIssuer(garbled.URL)).Token(t.Context()); err == nil {
		t.Error("a garbled answer: no error")
	}
	session := TokenSourceFunc(func(context.Context) (Token, error) { return Token{}, boom })
	if _, err := newActorTokens(t, session, WithIssuer(refuse.URL)).Token(t.Context()); !errors.Is(err, boom) {
		t.Errorf("a failing session: %v", err)
	}
	if _, err := newActorTokens(t, StaticToken("s"), WithIssuer("http://127.0.0.1:1")).Token(t.Context()); err == nil {
		t.Error("an unreachable issuer: no error")
	}
}

func TestTokenSourcesRefuseBadInput(t *testing.T) {
	for name, err := range map[string]error{
		"no session":          second(NewActorTokens(nil)),
		"a bad issuer":        second(NewActorTokens(StaticToken("s"), WithIssuer("%zz"))),
		"a relative issuer":   second(NewActorTokens(StaticToken("s"), WithIssuer("/auth"))),
		"an empty audience":   second(NewActorTokens(StaticToken("s"), WithAudience(""))),
		"an empty key":        second(NewKeyTokens("")),
		"a key with audience": second(NewKeyTokens("pat_x", WithAudience("api.example.test"))),
		"a key's bad issuer":  second(NewKeyTokens("pat_x", WithIssuer("ftp://example.test"))),
	} {
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func second[T any](_ T, err error) error { return err }

func TestActorTokensResetResetsTheSession(t *testing.T) {
	src := &resetSource{}
	a := newActorTokens(t, src)
	a.Reset()
	if src.resets.Load() != 1 {
		t.Errorf("the session was reset %d times, want 1", src.resets.Load())
	}
}

func TestKeyTokensExchangesTheKey(t *testing.T) {
	var grants atomic.Int32
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if r.URL.Path != "/token" || r.FormValue("grant_type") != "client_credentials" || !ok || user != "pat" {
			http.NotFound(w, r)
			return
		}
		if pass != "pat_good" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":"invalid_client","error_description":"The key is revoked."}`)
			return
		}
		fmt.Fprintf(w, `{"access_token":"tok_%d","expires_in":900,"token_type":"Bearer"}`, grants.Add(1))
	}))
	defer issuer.Close()
	k := newKeyTokens(t, "pat_good", WithIssuer(issuer.URL))
	tok, err := k.Token(t.Context())
	if err != nil || tok.Value != "tok_1" || time.Until(tok.Expiry) < 14*time.Minute {
		t.Fatalf("Token = %+v, %v", tok, err)
	}
	if again, _ := k.Token(t.Context()); again.Value != "tok_1" || grants.Load() != 1 {
		t.Errorf("a fresh token was exchanged again: %q", again.Value)
	}
	k.Reset()
	if after, _ := k.Token(t.Context()); after.Value != "tok_2" {
		t.Errorf("after Reset: %q", after.Value)
	}
	var apiErr *Error
	if _, err := newKeyTokens(t, "pat_revoked", WithIssuer(issuer.URL)).Token(t.Context()); !errors.As(err, &apiErr) || apiErr.Code != "invalid_client" || apiErr.Message != "The key is revoked." || !errors.Is(err, ErrUnauthorized) {
		t.Errorf("a revoked key: %v", err)
	}
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{}`) }))
	defer empty.Close()
	if _, err := newKeyTokens(t, "pat_x", WithIssuer(empty.URL)).Token(t.Context()); err == nil {
		t.Error("an answer without a token was accepted")
	}
}

func TestTokenExchangesUseTheConfigsTransport(t *testing.T) {
	var used atomic.Int32
	hc := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		used.Add(1)
		return http.DefaultTransport.RoundTrip(r)
	})}
	var agents []string
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agents = append(agents, r.Header.Get("User-Agent"))
		switch r.URL.Path {
		case "/token":
			fmt.Fprint(w, `{"access_token":"tok","expires_in":900}`)
		case "/actor-tokens":
			fmt.Fprint(w, `{"actor_token":"actor","expires_in":300}`)
		}
	}))
	defer issuer.Close()
	var hooked []string
	hooks := WithHooks(Hooks{BeforeRequest: func(r *http.Request) { hooked = append(hooked, r.URL.Path) }})
	ua := WithUserAgent("probe/1.0")
	for _, src := range []TokenSource{
		newKeyTokens(t, "pat_x", WithIssuer(issuer.URL)),
		newActorTokens(t, StaticToken("s"), WithIssuer(issuer.URL)),
	} {
		used.Store(0)
		agents, hooked = nil, nil
		// The source is given before the client, which must not matter.
		c, err := NewConfig(WithTokenSource(src), WithHTTPClient(hc), ua, hooks)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.tokens.Token(t.Context()); err != nil {
			t.Fatal(err)
		}
		if used.Load() != 1 || len(agents) != 1 || !strings.HasPrefix(agents[0], "probe/1.0 latere-sdk-go/") || len(hooked) != 1 {
			t.Errorf("%T: client used %d times, User-Agent %q, hooks saw %v", src, used.Load(), agents, hooked)
		}
	}
	// A source given to a second Config keeps the first one's transport.
	k := newKeyTokens(t, "pat_x", WithIssuer(issuer.URL))
	if _, err := NewConfig(WithTokenSource(k), WithHTTPClient(hc)); err != nil {
		t.Fatal(err)
	}
	if _, err := NewConfig(WithTokenSource(k)); err != nil {
		t.Fatal(err)
	}
	used.Store(0)
	if _, err := k.Token(t.Context()); err != nil || used.Load() != 1 {
		t.Errorf("after a second Config: %v, the first one's client used %d times", err, used.Load())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
