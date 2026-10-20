// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package latere

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Errors to test an *Error against with errors.Is. Each matches by the
// response's HTTP status, so it holds whatever code the API chose.
var (
	// ErrUnauthorized matches a 401: the token is missing, expired or not
	// accepted.
	ErrUnauthorized = errors.New("latere: unauthorized")
	// ErrForbidden matches a 403: the token is valid and may not do this.
	ErrForbidden = errors.New("latere: forbidden")
	// ErrNotFound matches a 404: the resource does not exist, or the caller
	// may not know that it does.
	ErrNotFound = errors.New("latere: not found")
	// ErrConflict matches a 409: the request conflicts with the resource's
	// current state, or an idempotency key was reused with another body.
	ErrConflict = errors.New("latere: conflict")
	// ErrRateLimited matches a 429: too many requests; Error.RetryAfter says
	// how long to wait when the API said.
	ErrRateLimited = errors.New("latere: rate limited")
)

// Error is a request the API answered with an error status.
type Error struct {
	// StatusCode is the response's HTTP status, such as 404.
	StatusCode int
	// Code is the API's stable error code, such as "not_found"; programs
	// branch on it. Empty when the response carried no error body.
	Code string
	// Message is one sentence about the error, written for a person.
	Message string
	// Details holds developer detail when the API sent any, such as the
	// field a request got wrong.
	Details map[string]any
	// RequestID identifies the request in the API's logs; include it when
	// you report a problem.
	RequestID string
	// RetryAfter is how long the API asked the caller to wait before
	// trying again, from the Retry-After header; zero when it did not say.
	RetryAfter time.Duration
}

// Error returns the status, code, message and request id on one line.
func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "latere: %d", e.StatusCode)
	if e.Code != "" {
		fmt.Fprintf(&b, " %s", e.Code)
	}
	if e.Message != "" {
		fmt.Fprintf(&b, ": %s", e.Message)
	}
	if e.RequestID != "" {
		fmt.Fprintf(&b, " (request %s)", e.RequestID)
	}
	return b.String()
}

// Is reports whether target is the sentinel error for e's status, so
// errors.Is(err, ErrNotFound) holds for any 404.
func (e *Error) Is(target error) bool {
	switch target {
	case ErrUnauthorized:
		return e.StatusCode == http.StatusUnauthorized
	case ErrForbidden:
		return e.StatusCode == http.StatusForbidden
	case ErrNotFound:
		return e.StatusCode == http.StatusNotFound
	case ErrConflict:
		return e.StatusCode == http.StatusConflict
	case ErrRateLimited:
		return e.StatusCode == http.StatusTooManyRequests
	}
	return false
}

// maxErrorBody bounds how much of an error response is read.
const maxErrorBody = 64 << 10

// newError builds the Error for an error response from its body, which
// holds the API's envelope {"error": {"code", "message", "details"}}. A body
// that is not the envelope, from a proxy in front of the API for instance,
// still yields an Error: its message is the start of the body, or the
// status text when the body is empty.
func newError(resp *http.Response, body []byte) *Error {
	e := &Error{
		StatusCode: resp.StatusCode,
		RequestID:  resp.Header.Get("X-Request-Id"),
		RetryAfter: retryAfter(resp.Header.Get("Retry-After"), time.Now()),
	}
	var envelope struct {
		Error struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) == nil && envelope.Error.Code != "" {
		e.Code = envelope.Error.Code
		e.Message = envelope.Error.Message
		e.Details = envelope.Error.Details
		return e
	}
	text := strings.TrimSpace(string(body))
	if len(text) > 200 {
		text = text[:200] + "…"
	}
	if text == "" {
		text = http.StatusText(resp.StatusCode)
	}
	e.Message = text
	return e
}

// retryAfter reads a Retry-After header, in seconds or as an HTTP date, as
// a duration from now. It returns zero for an absent, malformed or past
// value.
func retryAfter(v string, now time.Time) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if at, err := http.ParseTime(v); err == nil {
		if d := at.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}
