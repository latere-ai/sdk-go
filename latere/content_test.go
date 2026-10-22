// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package latere

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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

// A signed link's query is its credential: no error names it, whether the
// network failed on the way there or the link is not an address at all.
func TestASignedURLsQueryNeverReachesAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	c, _ := testConfig(t, srv, WithMaxRetries(0))
	err := c.Do(context.Background(), &Request{Method: http.MethodPut, SignedURL: "http://127.0.0.1:1/bucket/key?partNumber=1&X-Amz-Signature=SECRETSIG", Content: strings.NewReader("abcd"), ContentLength: 4}, nil)
	if err == nil || strings.Contains(err.Error(), "SECRETSIG") || strings.Contains(err.Error(), "partNumber") {
		t.Fatalf("err = %v, want one without the link's query", err)
	}
	if !strings.Contains(err.Error(), "PUT http://127.0.0.1:1/bucket/key") {
		t.Errorf("err = %v, want it to name the request by the link's host and path", err)
	}
	err = c.Do(context.Background(), &Request{Method: http.MethodPut, SignedURL: "ftp://x/y?X-Amz-Signature=SECRET2", Content: strings.NewReader("a"), ContentLength: 1}, nil)
	if err == nil || strings.Contains(err.Error(), "SECRET2") {
		t.Fatalf("err = %v, want one without the link", err)
	}
}

// A redirect to another host that then fails is named without its query:
// the API's redirects lead to signed links.
func TestARedirectElsewhereIsNamedWithoutItsQuery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:1/obj?X-Amz-Signature=READSIG", http.StatusFound)
	}))
	defer srv.Close()
	c, _ := testConfig(t, srv, WithMaxRetries(0))
	_, err := c.Open(context.Background(), &Request{Method: http.MethodGet, Path: "/v1/storage/files/me/files/big.bin", Query: url.Values{"cursor": {"c1"}}})
	if err == nil || strings.Contains(err.Error(), "READSIG") {
		t.Fatalf("err = %v, want one without the link's query", err)
	}
}

// A request to a signed URL takes no credential, so it cannot send the
// key either.
func TestASignedURLRefusesSendKey(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer srv.Close()
	c, err := NewConfig(WithKey("lk_live_THEKEY"), WithBaseURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Open(context.Background(), &Request{Method: http.MethodPut, SignedURL: srv.URL + "/p?sig=x", SendKey: true, Content: strings.NewReader("a"), ContentLength: 1})
	if err == nil || calls.Load() != 0 {
		t.Fatalf("err = %v after %d requests; want it refused before sending", err, calls.Load())
	}
}

// Sending bytes may take longer than the timeout meant for an answer:
// each attempt gets the timeout and TransferTime for its length.
func TestContentIsNotCutOffByTheAnswersTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A slow uplink: the body arrives after the timeout has passed.
		time.Sleep(300 * time.Millisecond)
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Error(err)
		}
		w.Header().Set("ETag", `"e"`)
	}))
	defer srv.Close()
	c, _ := testConfig(t, srv, WithTimeout(100*time.Millisecond))
	resp, err := c.Open(context.Background(), &Request{Method: http.MethodPut, SignedURL: srv.URL + "/p", Content: strings.NewReader("x"), ContentLength: 1})
	if err != nil {
		t.Fatalf("a slow part was cut off: %v", err)
	}
	closeBody(resp)
	if err := c.Do(context.Background(), &Request{Method: http.MethodPut, Path: "/v1/x", Content: strings.NewReader("x"), ContentLength: 1}, nil); err != nil {
		t.Fatalf("a slow write was cut off: %v", err)
	}
}

// An attempt still cut off by its bound is sent again, as a failure on
// the network is.
func TestAContentAttemptCutOffIsSentAgain(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			select {
			case <-r.Context().Done():
			case <-time.After(2 * time.Second):
			}
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c, _ := testConfig(t, srv, WithTimeout(100*time.Millisecond))
	// An empty body has no transfer time, so the bound is the timeout.
	if err := c.Do(context.Background(), &Request{Method: http.MethodPut, Path: "/v1/x", Content: strings.NewReader(""), ContentLength: 0}, nil); err != nil {
		t.Fatalf("err = %v, want the second attempt's answer", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("sent %d times, want 2", calls.Load())
	}
}

func TestTransferTime(t *testing.T) {
	for n, want := range map[int64]time.Duration{0: 0, -1: 0, 1: 2 * time.Minute, 16 << 20: 2 * time.Minute, 16<<20 + 1: 4 * time.Minute, 5 << 30: 640 * time.Minute} {
		if got := TransferTime(n); got != want {
			t.Errorf("TransferTime(%d) = %v, want %v", n, got, want)
		}
	}
}

// RetryAnswers sends a request again after an answer that says it failed,
// and never after a failure on the network.
func TestRetryAnswers(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			writeError(w, http.StatusServiceUnavailable, "unavailable", "try again")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c, _ := testConfig(t, srv)
	if err := c.Do(context.Background(), &Request{Method: http.MethodPost, Path: "/v1/x", Retry: RetryAnswers}, nil); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("sent %d times, want 2", calls.Load())
	}
	gone := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	}))
	defer gone.Close()
	c, _ = testConfig(t, gone)
	calls.Store(0)
	if err := c.Do(context.Background(), &Request{Method: http.MethodPost, Path: "/v1/x", Retry: RetryAnswers}, nil); err == nil {
		t.Fatal("a broken connection: nil error")
	}
	if calls.Load() != 1 {
		t.Fatalf("sent %d times after a failure on the network, want once", calls.Load())
	}
}

// An anonymous request goes to the API without the credential: no token
// is fetched for it, so it is sent even while the credentials fail, and a
// 401 is answered as it is rather than by renewing a token.
func TestAnAnonymousRequestCarriesNoCredential(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want none", got)
		}
		if r.URL.Path == "/v1/refused" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "No.")
			return
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()
	src := &resetSource{}
	c, _ := testConfig(t, srv, WithTokenSource(src))
	if err := c.Do(context.Background(), &Request{Method: http.MethodGet, Path: "/v1/web/icons/example.org", Anonymous: true}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Do(context.Background(), &Request{Method: http.MethodGet, Path: "/v1/refused", Anonymous: true}, nil); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("a refused anonymous request: %v, want ErrUnauthorized", err)
	}
	if src.n.Load() != 0 || src.resets.Load() != 0 {
		t.Errorf("the token source gave %d tokens and reset %d times, want neither", src.n.Load(), src.resets.Load())
	}
	if calls.Load() != 2 {
		t.Errorf("sent %d requests, want 2: a 401 is not sent again", calls.Load())
	}

	failing, _ := testConfig(t, srv, WithTokenSource(TokenSourceFunc(func(context.Context) (Token, error) {
		return Token{}, errors.New("the sign-in ended")
	})))
	if err := failing.Do(context.Background(), &Request{Method: http.MethodGet, Path: "/v1/web/icons/example.org", Anonymous: true}, nil); err != nil {
		t.Errorf("an anonymous request with failing credentials: %v", err)
	}
}

// A request sent without a credential cannot send the key either.
func TestAnAnonymousRequestRefusesSendKey(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer srv.Close()
	c, err := NewConfig(WithKey("lk_live_THEKEY"), WithBaseURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	err = c.Do(context.Background(), &Request{Method: http.MethodGet, Path: "/v1/models", Anonymous: true, SendKey: true}, nil)
	if err == nil || calls.Load() != 0 {
		t.Fatalf("err = %v after %d requests; want it refused before sending", err, calls.Load())
	}
}
