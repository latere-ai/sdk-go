// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package account

import (
	"errors"

	"latere.ai/x/sdk-go/latere"
)

// codeError is an *latere.Error that also matches the sentinel of its
// API code, for a refusal a caller branches on whose status alone does
// not tell it apart, such as two kinds of 409.
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
