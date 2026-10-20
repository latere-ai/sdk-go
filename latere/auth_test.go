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
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Audience != DefaultAudience || in.TTL != 300 {
			t.Errorf("exchange body %+v, %v", in, err)
		}
		n := mints.Add(1)
		fmt.Fprintf(w, `{"actor_token":"actor_%d","expires_in":300}`, n)
	}))
	defer issuer.Close()
	a := &ActorTokens{Session: StaticToken("session_tok"), Issuer: issuer.URL + "/"}
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

	if _, err := (&ActorTokens{}).Token(t.Context()); err == nil {
		t.Error("no Session: no error")
	}
	var apiErr *Error
	if _, err := (&ActorTokens{Session: StaticToken("s"), Issuer: refuse.URL}).Token(t.Context()); !errors.As(err, &apiErr) || apiErr.Code != "invalid_target" {
		t.Errorf("a refused exchange: %v", err)
	}
	if _, err := (&ActorTokens{Session: StaticToken("s"), Issuer: empty.URL}).Token(t.Context()); err == nil {
		t.Error("an empty answer: no error")
	}
	if _, err := (&ActorTokens{Session: StaticToken("s"), Issuer: garbled.URL}).Token(t.Context()); err == nil {
		t.Error("a garbled answer: no error")
	}
	session := TokenSourceFunc(func(context.Context) (Token, error) { return Token{}, boom })
	if _, err := (&ActorTokens{Session: session, Issuer: refuse.URL}).Token(t.Context()); !errors.Is(err, boom) {
		t.Errorf("a failing session: %v", err)
	}
	if _, err := (&ActorTokens{Session: StaticToken("s"), Issuer: "http://127.0.0.1:1"}).Token(t.Context()); err == nil {
		t.Error("an unreachable issuer: no error")
	}
	if _, err := (&ActorTokens{Session: StaticToken("s"), Issuer: "%zz"}).Token(t.Context()); err == nil {
		t.Error("a bad issuer: no error")
	}
}

func TestActorTokensResetResetsTheSession(t *testing.T) {
	src := &resetSource{}
	a := &ActorTokens{Session: src}
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
	k := &KeyTokens{Key: "pat_good", Issuer: issuer.URL}
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
	if _, err := (&KeyTokens{Key: "pat_revoked", Issuer: issuer.URL}).Token(t.Context()); !errors.As(err, &apiErr) || apiErr.Code != "invalid_client" || apiErr.Message != "The key is revoked." || !errors.Is(err, ErrUnauthorized) {
		t.Errorf("a revoked key: %v", err)
	}
	if _, err := (&KeyTokens{}).Token(t.Context()); err == nil {
		t.Error("an empty key was exchanged")
	}
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{}`) }))
	defer empty.Close()
	if _, err := (&KeyTokens{Key: "pat_x", Issuer: empty.URL}).Token(t.Context()); err == nil {
		t.Error("an answer without a token was accepted")
	}
	if _, err := (&KeyTokens{Key: "pat_x", Issuer: "%zz"}).Token(t.Context()); err == nil {
		t.Error("a bad issuer was accepted")
	}
}

func TestWithKeyUsesTheConfiguredClient(t *testing.T) {
	var used atomic.Bool
	hc := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		used.Store(true)
		return http.DefaultTransport.RoundTrip(r)
	})}
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"access_token":"tok","expires_in":900}`)
	}))
	defer issuer.Close()
	for _, order := range [][]Option{{WithKey("pat_x"), WithHTTPClient(hc)}, {WithHTTPClient(hc), WithKey("pat_x")}} {
		used.Store(false)
		c, err := NewConfig(order...)
		if err != nil {
			t.Fatal(err)
		}
		kt, ok := c.tokens.(*KeyTokens)
		if !ok || kt.HTTPClient != hc {
			t.Fatalf("WithKey's exchange does not use WithHTTPClient's client")
		}
		kt.Issuer = issuer.URL
		if _, err := kt.Token(t.Context()); err != nil || !used.Load() {
			t.Errorf("the exchange: %v, used the client: %v", err, used.Load())
		}
	}
	mine := &KeyTokens{Key: "pat_y"}
	c, err := NewConfig(WithKey("pat_x"), WithTokenSource(mine), WithHTTPClient(hc))
	if err != nil {
		t.Fatal(err)
	}
	if c.tokens != mine || mine.HTTPClient != nil {
		t.Error("a token source the caller built was changed")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
