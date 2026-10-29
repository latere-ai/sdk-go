// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package memory

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"latere.ai/x/sdk-go/latere"
)

// runsPath is where the routes of the runs live.
const runsPath = "/v1/account/runs"

// RunKind is what a run changes.
type RunKind string

// The kinds of a run.
const (
	// RunMemory is a run of Tidy memory, which changes one scope of the
	// person's memory.
	RunMemory RunKind = "memory"
)

// RunState is where a run stands.
type RunState string

// The states of a run.
const (
	// RunRunning is a run that has not ended; its changes are not ready.
	RunRunning RunState = "running"
	// RunApplied is a run whose changes were all applied, or that changed
	// nothing.
	RunApplied RunState = "applied"
	// RunWaiting is a run whose changes all wait for the person.
	RunWaiting RunState = "waiting"
	// RunPartlyApplied is a run of mixed outcomes: some changes applied
	// and others waiting, or some applied or waiting and others declined,
	// undone or left as they were.
	RunPartlyApplied RunState = "partly_applied"
	// RunDeclined is a run of which no change was applied or waits: each
	// was declined, by the person or past ExpiresAt, or left as it was
	// because the person changed its memory since.
	RunDeclined RunState = "declined"
	// RunUndone is a run of which every change it applied was undone and
	// none waits; the rest were declined or left as they were.
	RunUndone RunState = "undone"
)

// ChangeKind is what a run did to one memory.
type ChangeKind string

// The kinds of a change.
const (
	// ChangeCreated added the memory.
	ChangeCreated ChangeKind = "created"
	// ChangeChanged rewrote the memory.
	ChangeChanged ChangeKind = "changed"
	// ChangeRemoved removed the memory.
	ChangeRemoved ChangeKind = "removed"
)

// ChangeState is where one change of a run stands.
type ChangeState string

// The states of a change.
const (
	// ChangeApplied was written to the memory.
	ChangeApplied ChangeState = "applied"
	// ChangeWaiting waits for the person to apply or decline it.
	ChangeWaiting ChangeState = "waiting"
	// ChangeDeclined was declined, by the person or because it waited past
	// the run's ExpiresAt.
	ChangeDeclined ChangeState = "declined"
	// ChangeChangedSince was left as it is because the person changed the
	// memory after the run read it, so nothing they wrote was overwritten.
	ChangeChangedSince ChangeState = "changed_since"
	// ChangeUndone was applied and then undone.
	ChangeUndone ChangeState = "undone"
)

// Held is why a change waits for the person whatever the review mode.
type Held string

// The reasons a change is held.
const (
	// HeldYours is a change to a memory the person wrote, which waits for
	// their OK under ReviewApply too.
	HeldYours Held = "yours"
)

// A RunList is the person's runs, as ListRuns answers them.
type RunList struct {
	// Runs are the runs, the newest first, without their Changes.
	Runs []*Run `json:"runs"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the list, with every
// field, including those this package does not model; nil for a RunList
// the program built itself.
func (l *RunList) RawJSON() json.RawMessage { return l.raw }

// UnmarshalJSON decodes the list and keeps the JSON for RawJSON.
func (l *RunList) UnmarshalJSON(b []byte) error {
	type plain RunList
	if err := json.Unmarshal(b, (*plain)(l)); err != nil {
		return err
	}
	l.raw = keep(b)
	return nil
}

// A Run is one run of an Always on item that changes the person's memory,
// such as a night's run of Tidy memory on one scope, with what the person
// decided of its changes.
type Run struct {
	// ID is the run's id, the id of the session it ran in, which GetRun,
	// ApplyRun, DeclineRun and UndoRun take.
	ID string `json:"id"`
	// TriggerID is the id of the Always on item the run is one of, a
	// trigger of the agents capability.
	TriggerID string `json:"trigger_id"`
	// Kind is what the run changes.
	Kind RunKind `json:"kind"`
	// Scope is whose memory the run changed: the person's own, or a
	// project's.
	Scope Scope `json:"scope"`
	// Project is the project's id, when Scope is ScopeProject.
	Project string `json:"project,omitempty"`
	// State is where the run stands.
	State RunState `json:"state"`
	// Review is the review mode the run's changes follow: the person's
	// when the run started.
	Review Review `json:"review"`
	// Counts are how many memories the run added, updated and removed,
	// applied or waiting, and how many changes wait for the person.
	Counts RunCounts `json:"counts"`
	// CreatedAt is when the run started.
	CreatedAt time.Time `json:"created_at"`
	// EndedAt is when the run ended and its changes were ready; zero while
	// it runs.
	EndedAt time.Time `json:"ended_at,omitzero"`
	// DecidedAt is when nothing of the run waited any more; zero while
	// something does.
	DecidedAt time.Time `json:"decided_at,omitzero"`
	// ExpiresAt is when what still waits is declined, seven days after the
	// run ended; zero when nothing waits.
	ExpiresAt time.Time `json:"expires_at,omitzero"`
	// Changes are each memory the run changed, on the answer of GetRun,
	// ApplyRun, DeclineRun and UndoRun; a list answers none.
	Changes []RunChange `json:"changes,omitempty"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the run, with every
// field, including those this package does not model; nil for a Run the
// program built itself.
func (r *Run) RawJSON() json.RawMessage { return r.raw }

// UnmarshalJSON decodes the run and keeps the JSON for RawJSON.
func (r *Run) UnmarshalJSON(b []byte) error {
	type plain Run
	if err := json.Unmarshal(b, (*plain)(r)); err != nil {
		return err
	}
	r.raw = keep(b)
	return nil
}

// RunCounts are how many memories a run added, updated and removed,
// counting the changes applied and those waiting, and how many changes
// wait for the person.
type RunCounts struct {
	// Added is how many memories the run added.
	Added int `json:"added"`
	// Updated is how many memories the run rewrote.
	Updated int `json:"updated"`
	// Removed is how many memories the run removed.
	Removed int `json:"removed"`
	// Waiting is how many changes wait for the person.
	Waiting int `json:"waiting"`
}

// A RunChange is what a run did to one memory of its scope.
type RunChange struct {
	// ID is the memory's id in the run's scope, which RunDecisionParams
	// names.
	ID string `json:"id"`
	// Kind is what the run did to the memory.
	Kind ChangeKind `json:"kind"`
	// State is where the change stands.
	State ChangeState `json:"state"`
	// Held is why the change waits whatever the review mode, HeldYours;
	// empty for a change that follows the run's Review.
	Held Held `json:"held,omitempty"`
	// Name is the memory's name after the change, or before it for one
	// removed.
	Name string `json:"name"`
	// Description is the memory's description after the change, or
	// before it for one removed.
	Description string `json:"description"`
	// Before is the memory's file as it was before the change, for a
	// program to show the difference; empty for one the run added, since
	// a memory's file is never empty.
	Before string `json:"before,omitempty"`
	// After is the memory's file as the run left it; empty for one the
	// run removed.
	After string `json:"after,omitempty"`
}

// Undone is a run after an undo, with the changes the undo left as they
// were.
type Undone struct {
	// Run is the run as it is now, with its changes.
	Run
	// MovedOn are the ids of the changes not undone, because the person
	// changed their memory after the run.
	MovedOn []string `json:"moved_on"`
}

// RawJSON returns the JSON the API answered for the undo, the run with
// MovedOn, with every field, including those this package does not
// model; nil for an Undone the program built itself.
func (u *Undone) RawJSON() json.RawMessage { return u.raw }

// UnmarshalJSON decodes the run and what the undo left as it was, and
// keeps the JSON for RawJSON. It decodes in two steps because the Run it
// embeds decodes itself, which would leave MovedOn out.
func (u *Undone) UnmarshalJSON(b []byte) error {
	var rest struct {
		MovedOn []string `json:"moved_on"`
	}
	if err := json.Unmarshal(b, &rest); err != nil {
		return err
	}
	if err := json.Unmarshal(b, &u.Run); err != nil {
		return err
	}
	u.MovedOn = rest.MovedOn
	return nil
}

// RunDecisionParams name the changes of a run that ApplyRun, DeclineRun or
// UndoRun decides. A nil *RunDecisionParams, or one that names no change,
// decides every change the call applies to.
type RunDecisionParams struct {
	// Changes are the ids of the changes, as RunChange.ID names them.
	Changes []string `json:"changes,omitempty"`
}

// runPath returns the path of the run id, or of one of its actions, or an
// error, before any request, when id is empty.
func runPath(id, action string) (string, error) {
	if id == "" {
		return "", errors.New("memory: the run's id is empty")
	}
	p := runsPath + "/" + url.PathEscape(id)
	if action != "" {
		p += "/" + action
	}
	return p, nil
}

// decision returns the body of a decision, an empty object for nil.
func decision(params *RunDecisionParams) *RunDecisionParams {
	if params == nil {
		return &RunDecisionParams{}
	}
	return params
}

// ListRuns returns the runs of the person's Always on items that change
// their memory, the newest first, at most 50, each with its counts and
// without its changes. Tidy memory runs at night, once for the person's
// own memory and once for each project with something new. Read one
// run's changes with GetRun.
func (c *Client) ListRuns(ctx context.Context) (*RunList, error) {
	var out RunList
	if err := c.do(ctx, &latere.Request{Method: http.MethodGet, Path: runsPath}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetRun returns the run id with each memory it changed, the file before
// and after the change for a program to show the difference. A run that
// is not the person's returns an error matching ErrRunNotFound.
func (c *Client) GetRun(ctx context.Context, id string) (*Run, error) {
	p, err := runPath(id, "")
	if err != nil {
		return nil, err
	}
	var out Run
	if err := c.do(ctx, &latere.Request{Method: http.MethodGet, Path: p}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ApplyRun is the person's OK of the waiting changes params names, every
// waiting change when it names none, a change held because the memory is
// theirs included, and returns the run as it is now. A change to a memory
// the person changed after the run read it is left as it is, its State
// ChangeChangedSince. A run that has not ended is refused with an error
// matching ErrRunRunning, one with none of the changes named waiting with
// one matching ErrRunNotWaiting, an id the run does not hold with one
// matching ErrRunChangeUnknown, and a run another request is deciding
// with one matching ErrRunBusy: try again shortly.
//
// ApplyRun is sent once and never retried: the account part reads no
// idempotency key on it, and the answer reads each changed memory from
// Storage after the decision is saved, so even a server error can follow
// an apply that landed, and a repeat would be refused with
// ErrRunNotWaiting though the first succeeded. GetRun tells what each
// change is now. Sending it again with the same params applies only what
// still waits, so ErrRunNotWaiting on that repeat means the first applied
// it all.
func (c *Client) ApplyRun(ctx context.Context, id string, params *RunDecisionParams) (*Run, error) {
	return c.decide(ctx, id, "apply", params)
}

// DeclineRun is the person's no to the waiting changes params names, every
// waiting change when it names none, and returns the run as it is now.
// Nothing of the memory changes, and a declined change cannot be applied
// later. It is refused as ApplyRun is.
//
// DeclineRun is sent once and never retried, for ApplyRun's reason: a
// repeat after an answer that was lost, or that failed after the decline
// was saved, would be refused with ErrRunNotWaiting though the first
// succeeded. GetRun tells what each change is now.
func (c *Client) DeclineRun(ctx context.Context, id string, params *RunDecisionParams) (*Run, error) {
	return c.decide(ctx, id, "decline", params)
}

// decide sends the decision action on the run id, once.
func (c *Client) decide(ctx context.Context, id, action string, params *RunDecisionParams) (*Run, error) {
	p, err := runPath(id, action)
	if err != nil {
		return nil, err
	}
	var out Run
	req := &latere.Request{Method: http.MethodPost, Path: p, Body: decision(params), Retry: latere.RetryNever}
	if err := c.do(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UndoRun takes back the applied changes params names, every applied
// change when it names none, from Storage's versions and trash: a memory
// the run added is removed, one it rewrote gets its text before the run
// back, and one it removed is restored. It returns the run as it is now,
// with MovedOn naming each change left as it is because the person
// changed its memory after the run. A run that has not ended is refused
// with an error matching ErrRunRunning, an id the run does not hold with
// one matching ErrRunChangeUnknown, and a run another request is
// deciding with one matching ErrRunBusy: try again shortly.
//
// UndoRun is retried after a failure, though the account part reads no
// idempotency key on it: an undo passes over every change that is not
// applied, so a repeat after a lost answer undoes nothing more, refuses
// nothing, and answers the run as the first left it, with the same
// MovedOn.
func (c *Client) UndoRun(ctx context.Context, id string, params *RunDecisionParams) (*Undone, error) {
	p, err := runPath(id, "undo")
	if err != nil {
		return nil, err
	}
	var out Undone
	req := &latere.Request{Method: http.MethodPost, Path: p, Body: decision(params), Retry: latere.RetryAlways}
	if err := c.do(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
