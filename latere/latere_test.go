// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package latere

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

// testConfig returns a Config for srv whose retries wait no time, and the
// waits it was asked for.
func testConfig(t *testing.T, srv *httptest.Server, opts ...Option) (*Config, *[]time.Duration) {
	t.Helper()
	opts = append([]Option{WithBaseURL(srv.URL), WithTokenSource(StaticToken("key_test"))}, opts...)
	c, err := NewConfig(opts...)
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	var mu sync.Mutex
	waits := &[]time.Duration{}
	c.sleep = func(ctx context.Context, d time.Duration) error {
		mu.Lock()
		*waits = append(*waits, d)
		mu.Unlock()
		return ctx.Err()
	}
	return c, waits
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Request-Id", "req_test")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": message, "details": map[string]any{"field": "agent"}}}); err != nil {
		panic(err)
	}
}

func TestNewConfigReadsTheEnvironment(t *testing.T) {
	t.Setenv(EnvKey, "")
	t.Setenv(EnvBaseURL, "")
	if _, err := NewConfig(); !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("no credentials: %v, want ErrNoCredentials", err)
	}
	t.Setenv(EnvKey, "pat_env")
	t.Setenv(EnvBaseURL, "https://example.test/base/")
	c, err := NewConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got := c.BaseURL(); got != "https://example.test/base" {
		t.Errorf("BaseURL = %q, want the environment's without its trailing slash", got)
	}
	if kt, ok := c.tokens.(*KeyTokens); !ok || kt.key != "pat_env" {
		t.Errorf("tokens = %#v, want KeyTokens with the environment's key", c.tokens)
	}
	t.Setenv(EnvBaseURL, "not a url")
	if _, err := NewConfig(); err == nil || !strings.Contains(err.Error(), EnvBaseURL) {
		t.Errorf("a bad LATERE_BASE_URL: %v, want an error naming it", err)
	}
}

func TestOptionsRefuseBadValues(t *testing.T) {
	t.Setenv(EnvKey, "pat_env")
	for name, opt := range map[string]Option{
		"WithKey":         WithKey(""),
		"WithTokenSource": WithTokenSource(nil),
		"WithBaseURL":     WithBaseURL("ftp://example.test"),
		"WithBaseURL2":    WithBaseURL("https://example.test/?q=1"),
		"WithHTTPClient":  WithHTTPClient(nil),
		"WithUserAgent":   WithUserAgent("bad\nagent"),
		"WithMaxRetries":  WithMaxRetries(-1),
		"WithBackoff":     WithBackoff(time.Second, time.Millisecond),
		"WithTimeout":     WithTimeout(-time.Second),
	} {
		if _, err := NewConfig(opt); err == nil {
			t.Errorf("%s accepted a bad value", name)
		}
	}
	if _, err := NewConfig(WithBaseURL("%zz")); err == nil {
		t.Error("WithBaseURL accepted an unparsable URL")
	}
}

func TestDoSendsTheRequest(t *testing.T) {
	var got *http.Request
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		var err error
		if body, err = io.ReadAll(r.Body); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"ses_1"}`)
	}))
	defer srv.Close()
	var hooked []string
	c, _ := testConfig(t, srv, WithUserAgent("my-app/1.2"), WithHooks(Hooks{
		BeforeRequest: func(r *http.Request) { hooked = append(hooked, "before "+r.Method) },
		AfterResponse: func(r *http.Request, resp *http.Response, err error, d time.Duration) {
			hooked = append(hooked, fmt.Sprintf("after %d", resp.StatusCode))
		},
	}))
	var out struct{ ID string }
	err := c.Do(t.Context(), &Request{
		Method: http.MethodPost, Path: "/v1/agents/sessions/ses%2F1/events",
		Query: map[string][]string{"a": {"b c"}}, Body: map[string]string{"k": "v"},
		Header: http.Header{"X-Extra": {"1"}}, IdempotencyKey: "idem_1",
	}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if out.ID != "ses_1" {
		t.Errorf("decoded %+v", out)
	}
	if got.URL.RawPath != "/v1/agents/sessions/ses%2F1/events" || got.URL.RawQuery != "a=b+c" {
		t.Errorf("path %q query %q: the escaped path parameter must survive", got.URL.EscapedPath(), got.URL.RawQuery)
	}
	for k, want := range map[string]string{
		"Authorization": "Bearer key_test", "Content-Type": "application/json", "Accept": "application/json",
		"Idempotency-Key": "idem_1", "X-Extra": "1",
	} {
		if v := got.Header.Get(k); v != want {
			t.Errorf("%s = %q, want %q", k, v, want)
		}
	}
	if ua := got.Header.Get("User-Agent"); !strings.HasPrefix(ua, "my-app/1.2 latere-sdk-go/"+Version) {
		t.Errorf("User-Agent = %q", ua)
	}
	if string(body) != `{"k":"v"}` {
		t.Errorf("body = %s", body)
	}
	if strings.Join(hooked, ",") != "before POST,after 200" {
		t.Errorf("hooks ran %v", hooked)
	}
}

func TestDoRefusesABadRequest(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	c, _ := testConfig(t, srv)
	for _, r := range []*Request{
		{Path: "/v1"},
		{Method: http.MethodGet, Path: "v1"},
		{Method: http.MethodGet, Path: "/v1?x=1"},
		{Method: http.MethodPost, Path: "/v1", Body: func() {}},
	} {
		if err := c.Do(t.Context(), r, nil); err == nil {
			t.Errorf("%+v was sent", r)
		}
	}
}

func TestDoReturnsTheAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "The session does not exist.")
	}))
	defer srv.Close()
	c, _ := testConfig(t, srv)
	err := c.Do(t.Context(), &Request{Method: http.MethodGet, Path: "/v1/x"}, nil)
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("%v is not an *Error", err)
	}
	if apiErr.StatusCode != 404 || apiErr.Code != "not_found" || apiErr.RequestID != "req_test" || apiErr.Details["field"] != "agent" {
		t.Errorf("error = %+v", apiErr)
	}
	if !errors.Is(err, ErrNotFound) || errors.Is(err, ErrConflict) {
		t.Error("errors.Is does not match the status")
	}
	if want := "latere: 404 not_found: The session does not exist. (request req_test)"; err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
}

// A body that is not the envelope yields the status text as the message
// and is kept, unread, in Body.
func TestErrorWithoutTheEnvelope(t *testing.T) {
	for _, body := range []string{"", "<html>proxy down</html>", strings.Repeat("x", 300)} {
		e := newError(&http.Response{StatusCode: http.StatusBadGateway, Header: http.Header{}}, []byte(body))
		if e.Message != "Bad Gateway" || e.Code != "" {
			t.Errorf("body %.20q: message %q code %q, want the status text and no code", body, e.Message, e.Code)
		}
		if string(e.Body) != body {
			t.Errorf("body %.20q: Body = %.20q, want the body as sent", body, e.Body)
		}
	}
	if s := (&Error{StatusCode: 500}).Error(); s != "latere: 500" {
		t.Errorf("a bare error reads %q", s)
	}
}

// A body cut at the read bound keeps the code and message its envelope
// names before the cut, and ends on a rune boundary.
func TestATruncatedErrorKeepsItsCode(t *testing.T) {
	start := `{"error":{"code":"too_large","message":"The request is too large.","details":{"echo":"`
	if (maxErrorBody-len(start))%2 == 0 {
		start += "x" // so the cut lands inside a two-byte é
	}
	full := start + strings.Repeat("é", maxErrorBody) + `"}}}`
	cut := []byte(full)[:maxErrorBody]
	if utf8.Valid(cut) {
		t.Fatal("the cut does not split a rune; the test needs one that does")
	}
	e := newError(&http.Response{StatusCode: http.StatusRequestEntityTooLarge, Header: http.Header{}}, cut)
	if e.Code != "too_large" || e.Message != "The request is too large." {
		t.Errorf("code %q message %q; want the envelope's, read before the cut", e.Code, e.Message)
	}
	if !utf8.Valid(e.Body) || len(e.Body) != len(cut)-1 {
		t.Errorf("Body is %d bytes, valid UTF-8 %v; want the cut less its half rune", len(e.Body), utf8.Valid(e.Body))
	}
	if code, _ := envelopeStart([]byte(`{"error":{"details":{"a":1},"code":"x"`)); code != "" {
		t.Errorf("details before the code yielded the code %q; the reader stops at details", code)
	}
	if code, _ := envelopeStart([]byte(`{"items":[]}`)); code != "" {
		t.Errorf("a body that is not the envelope yielded the code %q", code)
	}
	for body, want := range map[string][2]string{
		`{"error":{"code":"x"`:               {"x", ""},
		`{"error":{"code":"x","message":`:    {"x", ""},
		`{"error":{"message":"m","code":"x"`: {"x", "m"},
	} {
		if code, message := envelopeStart([]byte(body)); code != want[0] || message != want[1] {
			t.Errorf("envelopeStart(%s) = %q, %q; want %q, %q", body, code, message, want[0], want[1])
		}
	}
	if got := validUTF8Prefix([]byte("ab\xffcd")); string(got) != "ab\xffcd" {
		t.Errorf("a body with an invalid byte inside was cut to %q", got)
	}
	if got := validUTF8Prefix([]byte("ab€")[:4]); string(got) != "ab" {
		t.Errorf("a body cut two bytes into a three-byte rune was kept as %q; want %q", got, "ab")
	}
	if got := validUTF8Prefix([]byte("\x80\x80\x80\x80\x80")); len(got) != 5 {
		t.Errorf("a body ending in stray continuation bytes was cut to %q; it holds no rune to finish", got)
	}
}

func TestErrorIsMatchesEachSentinel(t *testing.T) {
	for status, sentinel := range map[int]error{401: ErrUnauthorized, 403: ErrForbidden, 404: ErrNotFound, 409: ErrConflict, 429: ErrRateLimited} {
		if !errors.Is(&Error{StatusCode: status}, sentinel) {
			t.Errorf("a %d does not match %v", status, sentinel)
		}
		if errors.Is(&Error{StatusCode: 500}, sentinel) {
			t.Errorf("a 500 matches %v", sentinel)
		}
	}
}

func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	for v, want := range map[string]time.Duration{
		"":                              0,
		"3":                             3 * time.Second,
		"-1":                            0,
		"soon":                          0,
		"Tue, 20 Oct 2026 12:00:07 GMT": 7 * time.Second,
		"Tue, 20 Oct 2026 11:00:00 GMT": 0,
	} {
		if got := retryAfter(v, now); got != want {
			t.Errorf("retryAfter(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestRetriesAnIdempotentRequest(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			writeError(w, http.StatusServiceUnavailable, "unavailable", "Try again.")
		case 2:
			w.Header().Set("Retry-After", "2")
			writeError(w, http.StatusTooManyRequests, "rate_limited", "Slow down.")
		default:
			fmt.Fprint(w, `{}`)
		}
	}))
	defer srv.Close()
	c, waits := testConfig(t, srv)
	if err := c.Do(t.Context(), &Request{Method: http.MethodGet, Path: "/v1/x"}, nil); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Errorf("sent %d times, want 3", calls.Load())
	}
	if len(*waits) != 2 || (*waits)[1] != 2*time.Second {
		t.Errorf("waited %v, want a backoff then the 2s Retry-After", *waits)
	}
	if w := (*waits)[0]; w < DefaultMinBackoff/2 || w > DefaultMinBackoff {
		t.Errorf("the first backoff %v is outside [min/2, min]", w)
	}
}

func TestDoesNotRetryAPostWithoutAKey(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeError(w, http.StatusBadGateway, "bad_gateway", "Down.")
	}))
	defer srv.Close()
	c, _ := testConfig(t, srv)
	if err := c.Do(t.Context(), &Request{Method: http.MethodPost, Path: "/v1/x"}, nil); err == nil {
		t.Fatal("a 502 succeeded")
	}
	if calls.Load() != 1 {
		t.Errorf("a POST without an idempotency key was sent %d times, want 1", calls.Load())
	}
	calls.Store(0)
	if err := c.Do(t.Context(), &Request{Method: http.MethodPost, Path: "/v1/x", IdempotencyKey: "k"}, nil); err == nil {
		t.Fatal("a 502 succeeded")
	}
	if calls.Load() != 1+DefaultMaxRetries {
		t.Errorf("a POST with a key was sent %d times, want %d", calls.Load(), 1+DefaultMaxRetries)
	}
}

// The retry rule decides by method and key unless the request names one:
// a key does not make a PATCH retryable, since the API reads it on POST
// alone; RetryAlways retries a PATCH that sets values; RetryNever sends a
// keyed POST once.
func TestTheRetryRule(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeError(w, http.StatusBadGateway, "bad_gateway", "Down.")
	}))
	defer srv.Close()
	c, _ := testConfig(t, srv)
	for name, tc := range map[string]struct {
		r     *Request
		sends int32
	}{
		"a PATCH":                {&Request{Method: http.MethodPatch, Path: "/v1/x"}, 1},
		"a PATCH with a key":     {&Request{Method: http.MethodPatch, Path: "/v1/x", IdempotencyKey: "k"}, 1},
		"a PATCH that sets":      {&Request{Method: http.MethodPatch, Path: "/v1/x", Retry: RetryAlways}, 1 + DefaultMaxRetries},
		"a keyed POST sent once": {&Request{Method: http.MethodPost, Path: "/v1/x", IdempotencyKey: "k", Retry: RetryNever}, 1},
		"a GET sent once":        {&Request{Method: http.MethodGet, Path: "/v1/x", Retry: RetryNever}, 1},
		"a POST without a key":   {&Request{Method: http.MethodPost, Path: "/v1/x"}, 1},
		"a POST that may repeat": {&Request{Method: http.MethodPost, Path: "/v1/x", Retry: RetryAlways}, 1 + DefaultMaxRetries},
	} {
		calls.Store(0)
		if err := c.Do(t.Context(), tc.r, nil); err == nil {
			t.Fatalf("%s: a 502 succeeded", name)
		}
		if calls.Load() != tc.sends {
			t.Errorf("%s: sent %d times, want %d", name, calls.Load(), tc.sends)
		}
	}
}

func TestDoesNotRetryAClientError(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeError(w, http.StatusBadRequest, "invalid_request", "No.")
	}))
	defer srv.Close()
	c, _ := testConfig(t, srv)
	if err := c.Do(t.Context(), &Request{Method: http.MethodGet, Path: "/v1/x"}, nil); err == nil || calls.Load() != 1 {
		t.Errorf("a 400: %v after %d sends, want an error after 1", err, calls.Load())
	}
}

func TestAnOverlongRetryAfterIsReturned(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "3600")
		writeError(w, http.StatusTooManyRequests, "rate_limited", "Later.")
	}))
	defer srv.Close()
	c, _ := testConfig(t, srv)
	err := c.Do(t.Context(), &Request{Method: http.MethodGet, Path: "/v1/x"}, nil)
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.RetryAfter != time.Hour || calls.Load() != 1 {
		t.Errorf("an hour's Retry-After: %v after %d sends, want it returned at once", err, calls.Load())
	}
}

func TestRetriesANetworkError(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("no hijacker")
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				t.Fatal(err)
			}
			if err := conn.Close(); err != nil {
				t.Error(err)
			}
			return
		}
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()
	c, waits := testConfig(t, srv)
	if err := c.Do(t.Context(), &Request{Method: http.MethodGet, Path: "/v1/x"}, nil); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || len(*waits) != 1 {
		t.Errorf("sent %d times with %d waits, want 2 and 1", calls.Load(), len(*waits))
	}
	// A fresh connection each time: net/http itself resends an idempotent
	// request once when a reused connection drops, which would hide this.
	c, _ = testConfig(t, srv, WithMaxRetries(0), WithHTTPClient(&http.Client{Transport: &http.Transport{DisableKeepAlives: true}}))
	calls.Store(0)
	if err := c.Do(t.Context(), &Request{Method: http.MethodGet, Path: "/v1/x"}, nil); err == nil {
		t.Error("with retries off, a dropped connection succeeded")
	}
}

func TestAStoppedRetryReturnsTheContextsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "Down.")
	}))
	defer srv.Close()
	c, _ := testConfig(t, srv)
	ctx, cancel := context.WithCancel(t.Context())
	c.sleep = func(context.Context, time.Duration) error { cancel(); return context.Canceled }
	err := c.Do(ctx, &Request{Method: http.MethodGet, Path: "/v1/x"}, nil)
	var apiErr *Error
	if !errors.Is(err, context.Canceled) || !errors.As(err, &apiErr) || apiErr.Code != "unavailable" {
		t.Errorf("a canceled wait returned %v, want the context's error and the last answer", err)
	}

	// A deadline that passes during the wait reads as one.
	c, err = NewConfig(WithBaseURL(srv.URL), WithTokenSource(StaticToken("t")), WithBackoff(2*time.Second, 2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if err := c.Do(ctx, &Request{Method: http.MethodGet, Path: "/v1/x"}, nil); !errors.Is(err, context.DeadlineExceeded) || !errors.As(err, &apiErr) {
		t.Errorf("a deadline during the wait returned %v, want context.DeadlineExceeded", err)
	}
}

// resetSource counts tokens and resets.
type resetSource struct {
	n, resets atomic.Int32
}

func (s *resetSource) Token(context.Context) (Token, error) {
	return Token{Value: fmt.Sprintf("tok_%d", s.n.Add(1))}, nil
}
func (s *resetSource) Reset() { s.resets.Add(1) }

func TestA401ResetsTheTokenOnce(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		if len(seen) == 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized", "Expired.")
			return
		}
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()
	src := &resetSource{}
	c, _ := testConfig(t, srv, WithTokenSource(src))
	if err := c.Do(t.Context(), &Request{Method: http.MethodPost, Path: "/v1/x"}, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Join(seen, ",") != "Bearer tok_1,Bearer tok_2" || src.resets.Load() != 1 {
		t.Errorf("sent with %v after %d resets, want a fresh token after one reset", seen, src.resets.Load())
	}
	seen = nil
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		writeError(w, http.StatusUnauthorized, "unauthorized", "Revoked.")
	})
	if err := c.Do(t.Context(), &Request{Method: http.MethodGet, Path: "/v1/x"}, nil); !errors.Is(err, ErrUnauthorized) || len(seen) != 2 {
		t.Errorf("a token refused twice: %v after %d sends, want ErrUnauthorized after 2", err, len(seen))
	}
}

func TestATokenErrorIsReturned(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	for name, tc := range map[string]struct {
		err   error
		tries int32
	}{
		"a source of its own":    {errors.New("keychain locked"), 1},
		"a revoked key":          {&Error{StatusCode: http.StatusUnauthorized, Code: "invalid_client"}, 1},
		"an issuer that is down": {&Error{StatusCode: http.StatusServiceUnavailable}, 1 + DefaultMaxRetries},
		"an unreachable issuer":  {&url.Error{Op: "Post", URL: "https://auth.example.test/token", Err: errors.New("connection refused")}, 1 + DefaultMaxRetries},
	} {
		var tries atomic.Int32
		c, _ := testConfig(t, srv, WithTokenSource(TokenSourceFunc(func(context.Context) (Token, error) {
			tries.Add(1)
			return Token{}, tc.err
		})))
		if err := c.Do(t.Context(), &Request{Method: http.MethodGet, Path: "/v1/x"}, nil); !errors.Is(err, tc.err) || tries.Load() != tc.tries {
			t.Errorf("%s: %v after %d tries, want it after %d", name, err, tries.Load(), tc.tries)
		}
	}
}

// A token source's failure is a *TokenError naming its cause, so a program
// can tell it from a refusal of the request itself.
func TestATokenErrorNamesItsCause(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	cause := &Error{StatusCode: http.StatusUnauthorized, Code: "invalid_client"}
	c, _ := testConfig(t, srv, WithTokenSource(TokenSourceFunc(func(context.Context) (Token, error) {
		return Token{}, cause
	})))
	err := c.Do(t.Context(), &Request{Method: http.MethodGet, Path: "/v1/x"}, nil)
	var te *TokenError
	if !errors.As(err, &te) || te.Err != cause {
		t.Fatalf("err = %v, want a *TokenError wrapping the issuer's refusal", err)
	}
	if !errors.Is(err, ErrUnauthorized) || !strings.Contains(err.Error(), "getting a token: ") {
		t.Errorf("err = %q; want ErrUnauthorized and the token prefix", err)
	}
}

// An error response's body is kept whole, up to the read bound, for an
// answer that carries more than the error.
func TestAnErrorKeepsItsBody(t *testing.T) {
	long := `{"trigger_id":"trg_1","reason":"` + strings.Repeat("x", 300) + `"}`
	e := newError(&http.Response{StatusCode: http.StatusServiceUnavailable, Header: http.Header{}}, []byte(long))
	if string(e.Body) != long {
		t.Errorf("Body = %.40q (%d bytes), want the whole body (%d bytes)", e.Body, len(e.Body), len(long))
	}
	if e := newError(&http.Response{StatusCode: http.StatusBadGateway, Header: http.Header{}}, nil); e.Body != nil {
		t.Errorf("an empty body kept %q", e.Body)
	}
}

func TestACallOnAConfigNotBuilt(t *testing.T) {
	r := &Request{Method: http.MethodGet, Path: "/v1/x"}
	for name, c := range map[string]*Config{"nil": nil, "zero": {}} {
		if err := c.Do(t.Context(), r, nil); !errors.Is(err, errNotBuilt) {
			t.Errorf("Do on a %s Config: %v", name, err)
		}
		if _, err := c.Open(t.Context(), r); !errors.Is(err, errNotBuilt) {
			t.Errorf("Open on a %s Config: %v", name, err)
		}
		for _, err := range c.Stream(t.Context(), r, nil) {
			if !errors.Is(err, errNotBuilt) {
				t.Errorf("Stream on a %s Config: %v", name, err)
			}
		}
		if c.BaseURL() != "" {
			t.Errorf("BaseURL of a %s Config: %q", name, c.BaseURL())
		}
	}
}

func TestTimeoutBoundsTheCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	c, _ := testConfig(t, srv, WithTimeout(50*time.Millisecond), WithMaxRetries(0))
	start := time.Now()
	err := c.Do(t.Context(), &Request{Method: http.MethodGet, Path: "/v1/x"}, nil)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 5*time.Second {
		t.Errorf("a call past its timeout: %v after %v", err, time.Since(start))
	}
	if c, _ := testConfig(t, srv); c.timeout != DefaultTimeout {
		t.Errorf("the default timeout is %v, want DefaultTimeout", c.timeout)
	}
	if c, _ := testConfig(t, srv, WithTimeout(0)); c.timeout != 0 {
		t.Errorf("WithTimeout(0) left %v", c.timeout)
	}
}

func TestTimeoutDoesNotCutTheAnswerShort(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The answer's body arrives after its headers, as a large one does.
		fmt.Fprint(w, `{"id":`)
		w.(http.Flusher).Flush()
		time.Sleep(50 * time.Millisecond)
		fmt.Fprint(w, `"ses_1"}`)
	}))
	defer srv.Close()
	c, _ := testConfig(t, srv, WithTimeout(10*time.Second))
	var out struct{ ID string }
	if err := c.Do(t.Context(), &Request{Method: http.MethodGet, Path: "/v1/x"}, &out); err != nil || out.ID != "ses_1" {
		t.Errorf("an answer whose body came after its headers: %+v, %v", out, err)
	}
}

func TestDoDecodingFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `not json`)
	}))
	defer srv.Close()
	c, _ := testConfig(t, srv)
	var out map[string]any
	if err := c.Do(t.Context(), &Request{Method: http.MethodGet, Path: "/v1/x"}, &out); err == nil || !strings.Contains(err.Error(), "decoding") {
		t.Errorf("a body that is not JSON: %v", err)
	}
	if err := c.Do(t.Context(), &Request{Method: http.MethodGet, Path: "/v1/x"}, nil); err != nil {
		t.Errorf("a discarded answer failed: %v", err)
	}
}

func TestBackoffGrowsAndCaps(t *testing.T) {
	c := &Config{minBackoff: time.Second, maxBackoff: 4 * time.Second}
	for attempt, ceiling := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 4 * time.Second, 4 * time.Second} {
		for range 50 {
			if d := c.backoff(attempt); d < ceiling/2 || d > ceiling {
				t.Fatalf("backoff(%d) = %v, want within [%v, %v]", attempt, d, ceiling/2, ceiling)
			}
		}
	}
	if d := c.backoff(1000); d > 4*time.Second {
		t.Errorf("a huge attempt overflowed to %v", d)
	}
}

func TestSleepContext(t *testing.T) {
	if err := sleepContext(t.Context(), time.Millisecond); err != nil {
		t.Errorf("a short sleep: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := sleepContext(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("a canceled sleep: %v", err)
	}
}

func TestNewIdempotencyKeyIsRandom(t *testing.T) {
	a, b := NewIdempotencyKey(), NewIdempotencyKey()
	if a == b || len(a) < 20 {
		t.Errorf("keys %q and %q", a, b)
	}
}

func TestVersionMatchesTheChangelog(t *testing.T) {
	raw, err := os.ReadFile("../CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^## \[(\d+\.\d+\.\d+)\]`).FindSubmatch(raw)
	switch {
	case m == nil && !strings.HasSuffix(Version, "-dev"):
		t.Errorf("CHANGELOG.md has no release, so Version %q should end in -dev", Version)
	case m != nil && Version != string(m[1]):
		t.Errorf("Version is %q, and the newest release in CHANGELOG.md is %s", Version, m[1])
	}
}

// A request with SendKey carries the platform key itself, and asks the
// issuer for nothing: the models capability authenticates a key as itself.
func TestSendKeyPresentsTheKeyItself(t *testing.T) {
	var issued atomic.Int32
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		issued.Add(1)
		http.Error(w, "not expected", http.StatusTeapot)
	}))
	defer issuer.Close()
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"code":"unauthenticated","message":"This request needs a valid credential."}}`)
	}))
	defer srv.Close()
	kt, err := NewKeyTokens("pat_test", WithIssuer(issuer.URL))
	if err != nil {
		t.Fatal(err)
	}
	c, _ := testConfig(t, srv, WithTokenSource(kt))
	err = c.Do(t.Context(), &Request{Method: http.MethodPost, Path: "/v1/models/lux/v1/generate", SendKey: true}, nil)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if len(got) != 1 || got[0] != "Bearer pat_test" {
		t.Errorf("Authorization = %q, want the key itself, sent once: a key has no token to renew after a 401", got)
	}
	if issued.Load() != 0 {
		t.Errorf("the issuer was asked %d times, want none", issued.Load())
	}
}

// A Config whose credentials are not a key sends its token, SendKey or not.
func TestSendKeyWithATokenSourceSendsItsToken(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()
	c, _ := testConfig(t, srv)
	if err := c.Do(t.Context(), &Request{Method: http.MethodGet, Path: "/v1/models/lux/v1/models", SendKey: true}, nil); err != nil {
		t.Fatal(err)
	}
	if got != "Bearer key_test" {
		t.Errorf("Authorization = %q, want the token source's token", got)
	}
}

// A service that names the request in the envelope's details, rather than
// in X-Request-Id, still gives the Error its request id.
func TestErrorRequestIDFromTheDetails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":{"code":"model_not_found","message":"There is no model of that name.","details":{"detail":"no Model named \"x\"","request_id":"req_1"}}}`)
	}))
	defer srv.Close()
	c, _ := testConfig(t, srv)
	err := c.Do(t.Context(), &Request{Method: http.MethodGet, Path: "/v1/x"}, nil)
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.RequestID != "req_1" || apiErr.Code != "model_not_found" {
		t.Fatalf("err = %#v, want model_not_found with request req_1", err)
	}
}
