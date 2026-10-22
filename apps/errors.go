// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package apps

import (
	"errors"

	"latere.ai/x/sdk-go/latere"
)

// Errors to test with errors.Is, for refusals whose status alone does not
// tell them apart from a missing app. The error that matches one wraps the
// *latere.Error the API answered, which errors.As still finds, with its
// details and its request id; latere.ErrNotFound matches it too.
var (
	// ErrNoThumbnail matches a read of a deploy's picture when the app is
	// there but the deploy is not one of its deploys, or has no picture
	// yet: one is taken a moment after a deploy is ready. The
	// *latere.Error's Details hold "thumbnail_status" and "reason" when the
	// deploy exists.
	ErrNoThumbnail = errors.New("apps: the deploy has no picture")
	// ErrFrameUnavailable matches a frame pass asked of an installation
	// that names no page that may frame a private app, so it hands out
	// none.
	ErrFrameUnavailable = errors.New("apps: this installation hands out no frame pass")
)

// codeError is an *latere.Error that also matches the sentinel of its API
// code, for a refusal a caller branches on whose status alone does not
// tell it apart, such as one 404 among several.
type codeError struct {
	err      *latere.Error
	sentinel error
}

func (e *codeError) Error() string { return e.err.Error() }

// Is reports whether target is the sentinel of the error's code; the
// *latere.Error it unwraps to matches the sentinels of its status.
func (e *codeError) Is(target error) bool { return target == e.sentinel }

// Unwrap returns the API's answer.
func (e *codeError) Unwrap() error { return e.err }

// withSentinel returns err joined with the sentinel codes names for the
// API's code, when err is an *latere.Error of one of them, and err
// otherwise.
func withSentinel(err error, codes map[string]error) error {
	var apiErr *latere.Error
	if !errors.As(err, &apiErr) {
		return err
	}
	if sentinel, ok := codes[apiErr.Code]; ok {
		return &codeError{err: apiErr, sentinel: sentinel}
	}
	return err
}
