// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package latere

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// A request with Content sends those bytes, with their length and type,
// and sends them again from the start when it is retried.
func TestContentIsSentWithItsLengthAndAgainOnARetry(t *testing.T) {
	var calls atomic.Int32
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading the body: %v", err)
		}
		bodies = append(bodies, string(b))
		if r.ContentLength != 5 {
			t.Errorf("Content-Length = %d, want 5", r.ContentLength)
		}
		if got := r.Header.Get("Content-Type"); got != "text/plain" {
			t.Errorf("Content-Type = %q, want text/plain", got)
		}
		if calls.Add(1) == 1 {
			writeError(w, http.StatusServiceUnavailable, "unavailable", "try again")
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"path":"a.txt"}`) // the test reads the answer below
	}))
	defer srv.Close()
	c, _ := testConfig(t, srv)
	var out struct{ Path string }
	err := c.Do(context.Background(), &Request{Method: http.MethodPut, Path: "/v1/x", Content: strings.NewReader("hello"), ContentLength: 5, ContentType: "text/plain"}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || bodies[0] != "hello" || bodies[1] != "hello" {
		t.Fatalf("bodies = %q, want the bytes twice", bodies)
	}
	if out.Path != "a.txt" {
		t.Fatalf("answer = %+v", out)
	}
}

// Content without a type is sent as application/octet-stream, and an
// empty Content is sent with a length of zero.
func TestContentDefaultsItsTypeAndSendsAnEmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Content-Type"); got != "application/octet-stream" {
			t.Errorf("Content-Type = %q", got)
		}
		if r.ContentLength != 0 {
			t.Errorf("Content-Length = %d, want 0", r.ContentLength)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c, _ := testConfig(t, srv)
	if err := c.Do(context.Background(), &Request{Method: http.MethodPut, Path: "/v1/x", Content: strings.NewReader(""), ContentLength: 0}, nil); err != nil {
		t.Fatal(err)
	}
}

// A request may not carry both a JSON body and raw bytes, nor a negative
// length.
func TestContentRefusesABodyBesideItAndANegativeLength(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("nothing should be sent")
	}))
	defer srv.Close()
	c, _ := testConfig(t, srv)
	if err := c.Do(context.Background(), &Request{Method: http.MethodPut, Path: "/v1/x", Body: map[string]any{}, Content: strings.NewReader("x"), ContentLength: 1}, nil); err == nil {
		t.Error("Body and Content together: nil error")
	}
	if err := c.Do(context.Background(), &Request{Method: http.MethodPut, Path: "/v1/x", Content: strings.NewReader("x"), ContentLength: -1}, nil); err == nil {
		t.Error("a negative ContentLength: nil error")
	}
}

// A request to a signed URL goes there, not to the base URL, and carries
// no credential; a refusal there is not answered with a fresh token.
func TestSignedURLGoesThereWithoutTheCredential(t *testing.T) {
	var calls atomic.Int32
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want none", got)
		}
		if r.URL.Query().Get("sig") != "abc" {
			t.Errorf("query = %q, want the signature kept", r.URL.RawQuery)
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer store.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the base URL should not be called")
	}))
	defer api.Close()
	c, _ := testConfig(t, api)
	err := c.Do(context.Background(), &Request{Method: http.MethodPut, SignedURL: store.URL + "/part?sig=abc", Content: strings.NewReader("x"), ContentLength: 1}, nil)
	if err == nil {
		t.Fatal("a refused part: nil error")
	}
	if calls.Load() != 1 {
		t.Fatalf("sent %d times, want once: a signed link has no token to renew", calls.Load())
	}
}

// A signed URL that is not an http address is refused before sending.
func TestSignedURLMustBeAnHTTPAddress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	c, _ := testConfig(t, srv)
	for _, u := range []string{"ftp://x/y", "/relative", "https://"} {
		if err := c.Do(context.Background(), &Request{Method: http.MethodGet, SignedURL: u}, nil); err == nil {
			t.Errorf("SignedURL %q: nil error", u)
		}
	}
}

// KeepRedirect returns the redirect from Open rather than following it,
// and Do refuses it.
func TestKeepRedirectReturnsTheRedirect(t *testing.T) {
	followed := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/elsewhere" {
			followed = true
			return
		}
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	defer srv.Close()
	c, _ := testConfig(t, srv)
	resp, err := c.Open(context.Background(), &Request{Method: http.MethodGet, Path: "/v1/file", KeepRedirect: true})
	if err != nil {
		t.Fatal(err)
	}
	closeBody(resp)
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/elsewhere" || followed {
		t.Fatalf("status %d, location %q, followed %v; want the 302 kept", resp.StatusCode, resp.Header.Get("Location"), followed)
	}
	if err := c.Do(context.Background(), &Request{Method: http.MethodGet, Path: "/v1/file", KeepRedirect: true}, nil); err == nil {
		t.Fatal("Do with KeepRedirect: nil error")
	}
	// Without it, the redirect is followed.
	resp, err = c.Open(context.Background(), &Request{Method: http.MethodGet, Path: "/v1/file"})
	if err != nil {
		t.Fatal(err)
	}
	closeBody(resp)
	if !followed {
		t.Fatal("a plain Open did not follow the redirect")
	}
}
