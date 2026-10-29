// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package memory

import (
	"errors"

	"latere.ai/x/sdk-go/latere"
)

// Errors of the memory calls to test with errors.Is, one for each of the
// API's codes a caller branches on. The error that matches one wraps the
// *latere.Error the API answered, which errors.As still finds with its
// status, its message, its details and its request id, and which the
// sentinel of its status, such as latere.ErrNotFound, matches too.
var (
	// ErrNotFound matches a 404 with the code "memory_not_found": the scope
	// holds no memory of that id, or nothing forgotten of that id is left
	// to restore. A Ref that names a project the person does not hold is
	// refused with a 404 of the code "not_found", which latere.ErrNotFound
	// matches and ErrNotFound does not.
	ErrNotFound = errors.New("memory: no memory of that id in that scope")
	// ErrChanged matches a 412 with the code "memory_changed": the memory's
	// checksum is not the one IfMatch named, so it changed since it was
	// read. Read it again before changing it. latere.ErrPreconditionFailed
	// matches it too.
	ErrChanged = errors.New("memory: the memory changed since it was read")
	// ErrTaken matches a 409 with the code "memory_taken": a memory of the
	// same id was saved in the scope after the one being restored was
	// forgotten. Rename or delete that one first. latere.ErrConflict
	// matches it too.
	ErrTaken = errors.New("memory: a memory of the same id is in the scope again")
	// ErrNameTaken matches a 409 with the code "memory_name_taken": so many
	// memories of the scope share the name an id is made from that no id
	// is free. Give the memory another name. latere.ErrConflict matches it
	// too.
	ErrNameTaken = errors.New("memory: too many memories of the scope share the name")
	// ErrTooLarge matches a 413 with the code "memory_too_large": the
	// memory's file would hold more than Limits.MaxBytes. The
	// *latere.Error's Details hold "max_bytes".
	ErrTooLarge = errors.New("memory: the memory is too large")
	// ErrInvalidField matches a 400 with the code "invalid_field": a member
	// breaks its rule, such as a name or a description of more than one
	// line or past its bound in Limits, a type that is none of
	// Limits.Types, or an import with no text. The *latere.Error's Details
	// name the member in "field".
	ErrInvalidField = errors.New("memory: a member breaks its rule")
	// ErrPersonalOnly matches a 403 with the code "memory_personal_only":
	// memory is kept for a person in their personal context, and the token
	// names an organization's context or is a service account's.
	// latere.ErrForbidden matches it too.
	ErrPersonalOnly = errors.New("memory: memory is kept for a person in their personal context")
	// ErrAgentToken matches a 403 with the code "memory_agent_token": the
	// token is a conversation's agent's, which changes memory with its file
	// tools rather than through these calls. latere.ErrForbidden matches it
	// too.
	ErrAgentToken = errors.New("memory: an agent's token changes memory with its file tools")
	// ErrUnavailable matches a 503 with the code "memory_unavailable": the
	// installation keeps no memory, which no retry changes.
	ErrUnavailable = errors.New("memory: this installation keeps no memory")
	// ErrStorageUnavailable matches a 503 with the code
	// "storage_unavailable": Storage, where memory is kept, could not be
	// reached. The SDK has already retried the calls that are safe to
	// repeat; try again shortly.
	ErrStorageUnavailable = errors.New("memory: Storage, where memory is kept, could not be reached")
	// ErrRunNotFound matches a 404 with the code "run_not_found": no run
	// of that id is the person's.
	ErrRunNotFound = errors.New("memory: no run of that id is the person's")
	// ErrRunRunning matches a 409 with the code "run_running": the run has
	// not ended, so its changes are not ready to decide.
	// latere.ErrConflict matches it too.
	ErrRunRunning = errors.New("memory: the run has not ended")
	// ErrRunNotWaiting matches a 409 with the code "run_not_waiting": none
	// of the changes an apply or a decline named waits for the person.
	// GetRun tells what each is now. latere.ErrConflict matches it too.
	ErrRunNotWaiting = errors.New("memory: none of the changes named waits")
	// ErrRunBusy matches a 409 with the code "run_busy": another request
	// is deciding the run. Try again shortly. latere.ErrConflict matches
	// it too.
	ErrRunBusy = errors.New("memory: another request is deciding the run")
	// ErrRunChangeUnknown matches a 400 with the code
	// "run_change_unknown": RunDecisionParams names a change the run did
	// not make.
	ErrRunChangeUnknown = errors.New("memory: the run made no such change")
	// ErrRunsUnavailable matches a 503 with the code "runs_unavailable":
	// the installation keeps no record of runs, which no retry changes.
	ErrRunsUnavailable = errors.New("memory: this installation keeps no record of runs")
)

// codes are the API's codes of the memory calls' refusals that have a
// sentinel.
var codes = map[string]error{
	"memory_not_found":     ErrNotFound,
	"memory_changed":       ErrChanged,
	"memory_taken":         ErrTaken,
	"memory_name_taken":    ErrNameTaken,
	"memory_too_large":     ErrTooLarge,
	"invalid_field":        ErrInvalidField,
	"memory_personal_only": ErrPersonalOnly,
	"memory_agent_token":   ErrAgentToken,
	"memory_unavailable":   ErrUnavailable,
	"storage_unavailable":  ErrStorageUnavailable,
	"run_not_found":        ErrRunNotFound,
	"run_running":          ErrRunRunning,
	"run_not_waiting":      ErrRunNotWaiting,
	"run_busy":             ErrRunBusy,
	"run_change_unknown":   ErrRunChangeUnknown,
	"runs_unavailable":     ErrRunsUnavailable,
}

// codeError is an *latere.Error that also matches the sentinel of its
// API code, for a refusal a caller branches on whose status alone does
// not tell it apart, such as the two kinds of 403.
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
