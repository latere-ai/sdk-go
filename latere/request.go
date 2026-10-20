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
	// a repeated request with the same key with its first answer, so a
	// request that carries one is retried whatever its method. A repeat
	// that arrives while the first request is still being answered is
	// refused with 409 and ErrConflict, even though the first succeeds:
	// read the resource to learn the outcome.
	IdempotencyKey string
}

// Do sends r and decodes the JSON answer into out, which may be nil to
// discard it. A request the API refused returns an *Error; a request that
// never got an answer returns the transport's error, wrapped, or the
// context's when ctx ended.
func (c *Config) Do(ctx context.Context, r *Request, out any) error {
	if err := c.built(); err != nil {
		return err
	}
	if c.timeout > 0 {
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
		return fmt.Errorf("latere: decoding the answer to %s %s: %w", r.Method, r.Path, causeOr(ctx, err))
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
	if c.timeout <= 0 {
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
			err = fmt.Errorf("latere: %s %s: %w", r.Method, r.Path, context.DeadlineExceeded)
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

// safeToRepeat reports whether sending r twice has the effect of sending
// it once.
func safeToRepeat(r *Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete, http.MethodOptions:
		return true
	}
	return r.IdempotencyKey != ""
}

// send performs r with retries and returns a 2xx response.
func (c *Config) send(ctx context.Context, r *Request) (*http.Response, error) {
	target, err := c.url(r)
	if err != nil {
		return nil, err
	}
	var body []byte
	if r.Body != nil {
		if body, err = json.Marshal(r.Body); err != nil {
			return nil, fmt.Errorf("latere: encoding the body of %s %s: %w", r.Method, r.Path, err)
		}
	}
	repeatable := safeToRepeat(r)
	resetDone := false
	for attempt := 0; ; attempt++ {
		resp, err := c.attempt(ctx, r, target, body)
		if err != nil {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("latere: %s %s: %w", r.Method, r.Path, context.Cause(ctx))
			}
			if !repeatable || attempt >= c.maxRetries || !temporary(err) {
				return nil, fmt.Errorf("latere: %s %s: %w", r.Method, r.Path, err)
			}
			if err := c.sleep(ctx, c.backoff(attempt)); err != nil {
				return nil, fmt.Errorf("latere: %s %s: %w", r.Method, r.Path, context.Cause(ctx))
			}
			continue
		}
		if resp.StatusCode/100 == 2 {
			return resp, nil
		}
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		closeBody(resp)
		if readErr != nil {
			return nil, fmt.Errorf("latere: %s %s: reading the %d answer: %w", r.Method, r.Path, resp.StatusCode, causeOr(ctx, readErr))
		}
		apiErr := newError(resp, raw)
		// A 401 means the request was not acted on, so it is safe to send
		// once more with a fresh token whatever its method.
		if resp.StatusCode == http.StatusUnauthorized && !resetDone {
			if rs, ok := c.tokens.(Resetter); ok {
				rs.Reset()
				resetDone = true
				attempt--
				continue
			}
		}
		if !repeatable || !retryable(resp.StatusCode) || attempt >= c.maxRetries {
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
			return nil, fmt.Errorf("latere: %s %s: %w while waiting to retry after: %w", r.Method, r.Path, context.Cause(ctx), apiErr)
		}
	}
}

// tokenError is a failure of the token source, before any request was
// sent.
type tokenError struct{ err error }

func (e *tokenError) Error() string { return "the token: " + e.err.Error() }
func (e *tokenError) Unwrap() error { return e.err }

// temporary reports whether an attempt's failure may pass if the request
// is sent again: a failure on the network does, and so does a token source
// that failed on the network or met an issuer that is down. A refusal from
// the issuer, such as a revoked key's, does not.
func temporary(err error) bool {
	var te *tokenError
	if !errors.As(err, &te) {
		return true
	}
	var apiErr *Error
	if errors.As(te.err, &apiErr) {
		return retryable(apiErr.StatusCode)
	}
	var netErr *url.Error
	return errors.As(te.err, &netErr)
}

// attempt sends r once.
func (c *Config) attempt(ctx context.Context, r *Request, target string, body []byte) (*http.Response, error) {
	token, err := c.tokens.Token(ctx)
	if err != nil {
		return nil, &tokenError{err}
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, target, reader)
	if err != nil {
		return nil, err
	}
	for k, vs := range r.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	req.Header.Set("Authorization", "Bearer "+token.Value)
	req.Header.Set("User-Agent", c.userAgent)
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if r.IdempotencyKey != "" {
		req.Header.Set("Idempotency-Key", r.IdempotencyKey)
	}
	if c.hooks.BeforeRequest != nil {
		c.hooks.BeforeRequest(req)
	}
	start := time.Now()
	resp, err := c.httpClient.Do(req)
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
