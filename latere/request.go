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
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// A Request is one call to the API. The capability packages build them;
// build one yourself with Config.Do to reach an endpoint those packages do
// not cover yet.
type Request struct {
	// Method is the HTTP method, such as http.MethodGet.
	Method string
	// Path is the endpoint's path from the base URL, such as
	// "/v1/agents/sessions". Escape path parameters with url.PathEscape.
	Path string
	// Query holds the query parameters.
	Query url.Values
	// Body is encoded as JSON and sent when it is not nil.
	Body any
	// Header holds extra headers. The SDK sets Authorization, User-Agent,
	// Accept, Content-Type and Idempotency-Key itself.
	Header http.Header
	// IdempotencyKey is sent as the Idempotency-Key header. The API answers
	// a repeated POST with the same key with its first answer, so a POST
	// that carries one is retried under RetryByMethod; the API reads the
	// key on POST alone. A repeat that arrives while the first request is
	// still being answered is refused with 409 and ErrConflict, even though
	// the first succeeds: read the resource to learn the outcome.
	IdempotencyKey string
	// Retry says whether the request may be sent again after a failure;
	// RetryByMethod, the zero value, decides by its method and key.
	Retry RetryRule
	// SendKey sends the platform key itself as the bearer credential,
	// instead of the short-lived token it is exchanged for. The models
	// capability authenticates this way; package llm sets it, and a
	// program calling a models endpoint llm does not cover yet sets it too,
	// which is why it is exported. A Config whose credentials are not a
	// key, one built with WithTokenSource, sends its token as for any
	// request.
	SendKey bool
	// Content, when not nil, is the request's body as raw bytes, sent in
	// place of Body: ContentLength bytes from its start, of the media type
	// ContentType. Each attempt reads it from the start, so a request with
	// Content is retried by its Retry rule like any other. An *os.File, a
	// *bytes.Reader and an *io.SectionReader are each an io.ReaderAt.
	//
	// Sending the bytes takes as long as the network allows, so the
	// Config's timeout does not bound such a call as a whole: each attempt
	// gets the timeout and TransferTime for its length, and an attempt cut
	// off by that bound is sent again by the Retry rule, as a failure on
	// the network is.
	Content io.ReaderAt
	// ContentLength is how many bytes of Content are sent, as the
	// Content-Length header; the API admits a write by it before reading
	// the bytes.
	ContentLength int64
	// ContentType is Content's media type; "application/octet-stream"
	// when it is empty.
	ContentType string
	// SignedURL, when set, is an address the API signed and handed out,
	// such as an upload part's link. The request goes there in place of
	// the base URL and Path, and without the Config's credential, which
	// the link stands in for; a request that also sets SendKey is refused.
	// The link's query is a credential: an error names the link without
	// it.
	SignedURL string
	// KeepRedirect makes Open return a 3xx answer as it is, rather than
	// following it: its Location header says where the API sent the
	// request, such as a short-lived link to a file's bytes. Do refuses a
	// request that sets it, since a redirect has no JSON to decode.
	KeepRedirect bool
	// Anonymous sends the request without the Config's credential, for a
	// route that reads none, such as a site's icon: no token is fetched
	// for it, so it is sent whether or not the credentials work, and a 401
	// is not answered by renewing one. A request that also sets SendKey is
	// refused.
	Anonymous bool
}

// Do sends r and decodes the JSON answer into out, which may be nil to
// discard it. A request the API refused returns an *Error; a request that
// never got an answer returns the transport's error, wrapped, or the
// context's when ctx ended.
func (c *Config) Do(ctx context.Context, r *Request, out any) error {
	if err := c.built(); err != nil {
		return err
	}
	if r.KeepRedirect {
		return errors.New("latere: a request that keeps its redirect is sent with Open, not Do")
	}
	// A request with Content bounds each attempt instead (send).
	if c.timeout > 0 && r.Content == nil {
		if _, has := ctx.Deadline(); !has {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, c.timeout)
			// The answer is read below, so the bound ends with Do and not
			// with send.
			defer cancel()
		}
	}
	resp, err := c.send(ctx, r)
	if err != nil {
		return err
	}
	defer closeBody(resp)
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("latere: decoding the answer to %s %s: %w", r.Method, c.where(r), causeOr(ctx, err))
	}
	return nil
}

// Open sends r and returns the response with its body unread, for an
// answer that is a stream. Retries and errors work as for Do while the
// request connects, and WithTimeout bounds the wait for the first answer;
// once a 2xx response arrives, reading it is the caller's, for as long as
// ctx allows, and the caller must close its body.
func (c *Config) Open(ctx context.Context, r *Request) (*http.Response, error) {
	if err := c.built(); err != nil {
		return nil, err
	}
	if c.timeout <= 0 || r.Content != nil {
		// A request with Content bounds each attempt instead (send).
		return c.send(ctx, r)
	}
	if _, has := ctx.Deadline(); has {
		return c.send(ctx, r)
	}
	// A deadline would also end the body, so a timer cancels the request
	// instead, and stops once the answer arrives.
	ctx, cancel := context.WithCancelCause(ctx)
	timer := time.AfterFunc(c.timeout, func() { cancel(context.DeadlineExceeded) })
	resp, err := c.send(ctx, r)
	if !timer.Stop() {
		// The timer fired, even if an answer arrived at the same moment.
		if err == nil {
			closeStream(resp)
			err = fmt.Errorf("latere: %s %s: %w", r.Method, c.where(r), context.DeadlineExceeded)
		}
		cancel(nil)
		return nil, err
	}
	if err != nil {
		cancel(nil)
		return nil, err
	}
	resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: func() { cancel(nil) }}
	return resp, nil
}

// cancelOnClose releases the context of a stream's request when the stream
// is closed.
type cancelOnClose struct {
	io.ReadCloser
	cancel func()
}

func (b *cancelOnClose) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

// causeOr returns why ctx ended, when it has, and err otherwise, so a call
// cut off by its deadline returns an error matching
// context.DeadlineExceeded rather than the transport's description of it.
func causeOr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	return err
}

// retryable reports whether a status is worth sending the request again
// for: the API timed out, limited the caller's rate, or failed.
func retryable(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooManyRequests,
		http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// A RetryRule says whether a request may be sent again after a failure
// on the network or an answer of 408, 429, 500, 502, 503 or 504.
type RetryRule int

const (
	// RetryByMethod, the zero value, retries a GET, HEAD, PUT, DELETE or
	// OPTIONS request, and a POST that carries an IdempotencyKey.
	RetryByMethod RetryRule = iota
	// RetryAlways retries the request whatever its method: it has the same
	// effect sent twice, as a PATCH that only sets values has.
	RetryAlways
	// RetryNever sends the request once: a second send could do the work
	// twice, such as creating a second app or running a model again.
	RetryNever
	// RetryAnswers sends the request again after an answer of 408, 429,
	// 500, 502, 503 or 504, which says it failed, but never after a
	// failure on the network, after which it may have been done: for a
	// request the API can resume but not repeat, such as completing an
	// upload in parts.
	RetryAnswers
)

// safeToRepeat reports whether sending r twice has the effect of sending
// it once, as r's Retry rule says.
func safeToRepeat(r *Request) bool {
	switch r.Retry {
	case RetryAlways:
		return true
	case RetryNever, RetryAnswers:
		return false
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete, http.MethodOptions:
		return true
	case http.MethodPost:
		return r.IdempotencyKey != ""
	}
	return false
}

// TransferTime is how long a request's Content may take to send, beyond
// the Config's timeout: two minutes for every 16 MiB begun, the pace
// Latere Chat gives an upload's part, a little over 1 Mbit/s.
func TransferTime(n int64) time.Duration {
	const chunk, pace = 16 << 20, 2 * time.Minute
	if n <= 0 {
		return 0
	}
	return time.Duration((n+chunk-1)/chunk) * pace
}

// send performs r with retries and returns a 2xx response.
func (c *Config) send(ctx context.Context, r *Request) (*http.Response, error) {
	target, err := c.url(r)
	if err != nil {
		return nil, err
	}
	where := c.where(r)
	var body []byte
	switch {
	case r.SignedURL != "" && r.SendKey:
		return nil, fmt.Errorf("latere: %s %s goes to a signed URL, which takes no credential, and also sets SendKey", r.Method, where)
	case r.Anonymous && r.SendKey:
		return nil, fmt.Errorf("latere: %s %s is sent without a credential and also sets SendKey", r.Method, where)
	case r.Content != nil && r.Body != nil:
		return nil, fmt.Errorf("latere: %s %s sets both Body and Content; it sends one body", r.Method, where)
	case r.Content != nil && r.ContentLength < 0:
		return nil, fmt.Errorf("latere: %s %s has a ContentLength of %d", r.Method, where, r.ContentLength)
	case r.Body != nil:
		if body, err = json.Marshal(r.Body); err != nil {
			return nil, fmt.Errorf("latere: encoding the body of %s %s: %w", r.Method, where, err)
		}
	}
	repeatable := safeToRepeat(r)
	answers := repeatable || r.Retry == RetryAnswers
	resetDone := false
	for attempt := 0; ; attempt++ {
		actx, done := c.attemptContext(ctx, r)
		resp, err := c.attempt(actx, r, target, body)
		if err != nil {
			done()
			if ctx.Err() != nil {
				return nil, fmt.Errorf("latere: %s %s: %w", r.Method, where, context.Cause(ctx))
			}
			if !repeatable || attempt >= c.maxRetries || !temporary(err) {
				return nil, fmt.Errorf("latere: %s %s: %w", r.Method, where, err)
			}
			if err := c.sleep(ctx, c.backoff(attempt)); err != nil {
				return nil, fmt.Errorf("latere: %s %s: %w", r.Method, where, context.Cause(ctx))
			}
			continue
		}
		if resp.StatusCode/100 == 2 || (r.KeepRedirect && resp.StatusCode/100 == 3) {
			resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: done}
			return resp, nil
		}
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		closeBody(resp)
		done()
		if readErr != nil {
			return nil, fmt.Errorf("latere: %s %s: reading the %d answer: %w", r.Method, where, resp.StatusCode, causeOr(ctx, readErr))
		}
		apiErr := newError(resp, raw)
		// A 401 means the request was not acted on, so it is safe to send
		// once more with a fresh token whatever its method. A request that
		// sent the key itself has no token to renew.
		_, sentKey := c.key(r)
		if resp.StatusCode == http.StatusUnauthorized && !resetDone && !sentKey && r.SignedURL == "" && !r.Anonymous {
			if rs, ok := c.tokens.(Resetter); ok {
				rs.Reset()
				resetDone = true
				attempt--
				continue
			}
		}
		if !answers || !retryable(resp.StatusCode) || attempt >= c.maxRetries {
			return nil, apiErr
		}
		wait := c.backoff(attempt)
		if apiErr.RetryAfter > 0 {
			if apiErr.RetryAfter > c.maxBackoff {
				return nil, apiErr
			}
			wait = apiErr.RetryAfter
		}
		if err := c.sleep(ctx, wait); err != nil {
			return nil, fmt.Errorf("latere: %s %s: %w while waiting to retry after: %w", r.Method, where, context.Cause(ctx), apiErr)
		}
	}
}

// attemptContext bounds one attempt of a request with Content by the
// Config's timeout and TransferTime for its length, so a slow upload is
// not cut off by a bound meant for an answer, and an attempt that is
// still cut off can be sent again. Other requests are bounded by the call
// (Do, Open). The returned function releases the bound.
func (c *Config) attemptContext(ctx context.Context, r *Request) (context.Context, context.CancelFunc) {
	if r.Content == nil || c.timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, c.timeout+TransferTime(r.ContentLength))
}

// where names r's target in an error: its path, or a signed URL without
// its query, which is a credential.
func (c *Config) where(r *Request) string {
	if r.SignedURL != "" {
		return redact(r.SignedURL)
	}
	return r.Path
}

// redact is an address as an error may name it: its scheme, host and
// path, without the user, the query and the fragment, which a signed link
// carries its signature in.
func redact(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "an address that is not one"
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}).String()
}

// redactError drops the credential from the address a transport's error
// names, when the address is a signed URL or another host's than the
// API's, such as a link a redirect led to.
func (c *Config) redactError(r *Request, err error) {
	var ue *url.Error
	if !errors.As(err, &ue) {
		return
	}
	if u, perr := url.Parse(ue.URL); r.SignedURL != "" || perr != nil || u.Host != c.baseURL.Host {
		ue.URL = redact(ue.URL)
	}
	// net/http quotes a redirect's Location it cannot parse, which may be
	// a signed link, before any rule of the client's can stop it.
	if ue.Err != nil && strings.Contains(ue.Err.Error(), "Location header") {
		ue.Err = errors.New("the answer's Location header is not an address")
	}
}

// A TokenError is a call that failed before any request was sent, because
// the Config's credentials could not be obtained: the token source failed,
// or the issuer refused the key or the sign-in. Err is the cause; when the
// issuer refused, it is an *Error, so errors.Is(err, ErrUnauthorized) holds
// for a revoked key. A call returns it after any retries.
type TokenError struct {
	// Err is why the credentials could not be obtained.
	Err error
}

// Error returns the cause, prefixed to say it happened while getting a
// token; the call that returns it names the request in front.
func (e *TokenError) Error() string { return "getting a token: " + e.Err.Error() }

// Unwrap returns the cause.
func (e *TokenError) Unwrap() error { return e.Err }

// temporary reports whether an attempt's failure may pass if the request
// is sent again: a failure on the network does, and so does a token source
// that failed on the network or met an issuer that is down. A refusal from
// the issuer, such as a revoked key's, does not.
func temporary(err error) bool {
	var te *TokenError
	if !errors.As(err, &te) {
		return true
	}
	var apiErr *Error
	if errors.As(te.Err, &apiErr) {
		return retryable(apiErr.StatusCode)
	}
	var netErr *url.Error
	return errors.As(te.Err, &netErr)
}

// key returns the platform key to send as r's credential, when r asks for
// the key and the Config holds one.
func (c *Config) key(r *Request) (string, bool) {
	if !r.SendKey {
		return "", false
	}
	kh, ok := c.tokens.(keyHolder)
	if !ok {
		return "", false
	}
	return kh.platformKey(), true
}

// attempt sends r once.
func (c *Config) attempt(ctx context.Context, r *Request, target string, body []byte) (*http.Response, error) {
	bearer, sentKey := c.key(r)
	if !sentKey && r.SignedURL == "" && !r.Anonymous {
		token, err := c.tokens.Token(ctx)
		if err != nil {
			return nil, &TokenError{Err: err}
		}
		bearer = token.Value
	}
	var reader io.Reader
	switch {
	case r.Content != nil:
		reader = io.NewSectionReader(r.Content, 0, r.ContentLength)
	case body != nil:
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, target, reader)
	if err != nil {
		return nil, err
	}
	if r.Content != nil {
		// A section reader is not one of the readers a request takes its
		// length from, and the API admits a write by its length.
		req.ContentLength = r.ContentLength
		if r.ContentLength == 0 {
			req.Body = http.NoBody
		}
	}
	for k, vs := range r.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	req.Header.Set("User-Agent", c.userAgent)
	if req.Header.Get("Accept") == "" && r.SignedURL == "" {
		req.Header.Set("Accept", "application/json")
	}
	switch {
	case r.Content != nil && r.ContentType != "":
		req.Header.Set("Content-Type", r.ContentType)
	case r.Content != nil:
		req.Header.Set("Content-Type", "application/octet-stream")
	case body != nil:
		req.Header.Set("Content-Type", "application/json")
	}
	if r.IdempotencyKey != "" {
		req.Header.Set("Idempotency-Key", r.IdempotencyKey)
	}
	if c.hooks.BeforeRequest != nil {
		c.hooks.BeforeRequest(req)
	}
	client := c.httpClient
	if r.KeepRedirect {
		// A copy of the caller's client that stops at a redirect; the copy
		// shares its transport, so its connections are reused.
		kept := *client
		kept.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		client = &kept
	}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		c.redactError(r, err)
	}
	if c.hooks.AfterResponse != nil {
		c.hooks.AfterResponse(req, resp, err, time.Since(start))
	}
	return resp, err
}

// url joins the base URL, r's path and its query. The path is already
// escaped, so it is appended as written.
func (c *Config) url(r *Request) (string, error) {
	if r.Method == "" {
		return "", errors.New("latere: the request has no method")
	}
	if r.SignedURL != "" {
		u, err := url.Parse(r.SignedURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			// The link is a credential, so the error does not quote it.
			return "", fmt.Errorf("latere: the signed URL of %s is not an http or https address", r.Method)
		}
		return r.SignedURL, nil
	}
	if !strings.HasPrefix(r.Path, "/") || strings.ContainsAny(r.Path, "?#") {
		return "", fmt.Errorf("latere: the path %q does not start with / or holds a query or a fragment", r.Path)
	}
	s := c.baseURL.String() + r.Path
	if q := r.Query.Encode(); q != "" {
		s += "?" + q
	}
	return s, nil
}

// backoff is the wait before retry attempt+1: minBackoff doubled per
// attempt up to maxBackoff, jittered to between half and all of it so
// clients that failed together do not retry together.
func (c *Config) backoff(attempt int) time.Duration {
	d := c.minBackoff << min(attempt, 30)
	if d <= 0 || d > c.maxBackoff {
		d = c.maxBackoff
	}
	half := d / 2
	return half + rand.N(d-half+1)
}
