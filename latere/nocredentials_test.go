// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package latere

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// A Config built WithoutCredentials sends no Authorization header, reads
// no LATERE_KEY, and answers a 401 as it is, with no token to renew.
func TestWithoutCredentialsSendsNone(t *testing.T) {
	t.Setenv(EnvKey, "not a key")
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want none", got)
		}
		if r.URL.Path == "/v1/private" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "A valid bearer token is required.")
			return
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()
	c, err := NewConfig(WithBaseURL(srv.URL), WithoutCredentials())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Do(context.Background(), &Request{Method: http.MethodGet, Path: "/v1/shares/tok"}, nil); err != nil {
		t.Fatal(err)
	}
	resp, err := c.Open(context.Background(), &Request{Method: http.MethodGet, Path: "/v1/web/icons/example.org"})
	if err != nil {
		t.Fatal(err)
	}
	closeBody(resp)
	if err := c.Do(context.Background(), &Request{Method: http.MethodGet, Path: "/v1/private"}, nil); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("a route that needs a credential: %v, want ErrUnauthorized", err)
	}
	if n := calls.Load(); n != 3 {
		t.Errorf("sent %d requests, want 3: a 401 is not sent again", n)
	}
}

// A request that sends the key cannot be sent without one.
func TestWithoutCredentialsRefusesSendKey(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer srv.Close()
	c, err := NewConfig(WithBaseURL(srv.URL), WithoutCredentials())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Do(context.Background(), &Request{Method: http.MethodGet, Path: "/v1/models", SendKey: true}, nil); err == nil || calls.Load() != 0 {
		t.Fatalf("err = %v after %d requests; want it refused before sending", err, calls.Load())
	}
}

// Whichever credential option comes last decides.
func TestTheLastCredentialOptionWins(t *testing.T) {
	var auth atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth.Store(r.Header.Get("Authorization"))
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()
	for _, tc := range []struct {
		opts []Option
		want string
	}{
		{[]Option{WithoutCredentials(), WithTokenSource(StaticToken("tok"))}, "Bearer tok"},
		{[]Option{WithTokenSource(StaticToken("tok")), WithoutCredentials()}, ""},
	} {
		c, err := NewConfig(append([]Option{WithBaseURL(srv.URL)}, tc.opts...)...)
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Do(context.Background(), &Request{Method: http.MethodGet, Path: "/v1/x"}, nil); err != nil {
			t.Fatal(err)
		}
		if got := auth.Load(); got != tc.want {
			t.Errorf("Authorization = %q, want %q", got, tc.want)
		}
	}
}

// visitorKey carries a visitor's address in a call's context.
type visitorKey struct{}

// A hook sees the context the call was made with, so a server forwards
// each visitor's address on the reads it makes for them.
func TestAHookForwardsTheVisitorFromTheContext(t *testing.T) {
	var got atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Store(r.Header.Get("X-Real-IP"))
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()
	c, err := NewConfig(WithBaseURL(srv.URL), WithoutCredentials(), WithHooks(Hooks{
		BeforeRequest: func(req *http.Request) {
			if v, ok := req.Context().Value(visitorKey{}).(string); ok {
				req.Header.Set("X-Real-IP", v)
			}
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), visitorKey{}, "203.0.113.7")
	if err := c.Do(ctx, &Request{Method: http.MethodGet, Path: "/v1/shares/tok"}, nil); err != nil {
		t.Fatal(err)
	}
	if got.Load() != "203.0.113.7" {
		t.Errorf("X-Real-IP = %q, want the visitor's address", got.Load())
	}
}
