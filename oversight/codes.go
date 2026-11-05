// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package oversight

import (
	"errors"

	"latere.ai/x/sdk-go/latere"
)

// Errors of the oversight calls to test with errors.Is, one for each of the
// API's codes a caller branches on. The error that matches one wraps the
// *latere.Error the API answered, which errors.As still finds with its
// status, its message, its details and its request id, and which the
// sentinel of its status matches too.
var (
	// ErrAskTooCostly matches a 422 with the code "ask_too_costly": under
	// the costs, the decision service would run or stop a step of a person
	// it knows nothing about without asking. Make each kind of error cost
	// more than two asks.
	ErrAskTooCostly = errors.New("oversight: the costs would decide a step without asking")
	// ErrNoDecision matches a 409 with the code "no_decision": the step has
	// no recorded decision to answer. latere.ErrConflict matches it too.
	ErrNoDecision = errors.New("oversight: the step has no recorded decision")
	// ErrUnavailable matches a 503 with the code "unavailable": the
	// decision service could not answer now. The SDK has already retried
	// the calls that are safe to repeat; try again shortly.
	ErrUnavailable = errors.New("oversight: the decision service could not answer")
)

// codes are the API's codes of the oversight calls' refusals that have a
// sentinel.
var codes = map[string]error{
	"ask_too_costly": ErrAskTooCostly,
	"no_decision":    ErrNoDecision,
	"unavailable":    ErrUnavailable,
}

// codeError is an *latere.Error that also matches the sentinel of its API
// code.
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

// withSentinel returns err joined with the sentinel of the API's code,
// when err is an *latere.Error of one of codes, and err otherwise.
func withSentinel(err error) error {
	var apiErr *latere.Error
	if !errors.As(err, &apiErr) {
		return err
	}
	if sentinel, ok := codes[apiErr.Code]; ok {
		return &codeError{err: apiErr, sentinel: sentinel}
	}
	return err
}
