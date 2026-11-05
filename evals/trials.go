// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package evals

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"net/url"
	"time"

	"latere.ai/x/sdk-go/latere"
)

// trialsPath is the collection of trials, which an experiment's trials
// are listed under and one is read from.
const trialsPath = BasePath + "/trials"

var errNoTrialID = errors.New("evals: the trial's id is empty")

// A Trial is one run of one cell: one index of the cell's trials, which
// the system under test runs and the cell's graders grade.
type Trial struct {
	// ID identifies the trial.
	ID string `json:"id"`
	// Experiment and Cell are the experiment and the cell the trial
	// belongs to, by id.
	Experiment string `json:"experiment"`
	Cell       string `json:"cell"`
	// Owner is the context the trial belongs to, its experiment's.
	Owner string `json:"owner,omitempty"`
	// Index is which of the cell's trials this is, from 0.
	Index int `json:"index"`
	// State is where the trial is in its life.
	State TrialState `json:"state"`
	// Outcome is how a finished trial ended; empty until it is done.
	Outcome TrialOutcome `json:"outcome,omitempty"`
	// Imported reports a trial a harness ran elsewhere and imported,
	// rather than one the platform ran or leased.
	Imported bool `json:"imported,omitempty"`
	// RetryOf is the id of the earlier trial of the same cell and index
	// that this trial runs again: one that ended OutcomeInfraFailed and
	// was retried automatically, or one a cell's retry runs again. That
	// trial is marked retried and leaves every figure. Empty on a first
	// attempt.
	RetryOf string `json:"retry_of,omitempty"`
	// ExecutorRef names where the trial ran, such as a session id.
	ExecutorRef string `json:"executor_ref,omitempty"`
	// Failure says why a trial the platform failed to run failed; nil
	// otherwise.
	Failure *TrialFailure `json:"failure,omitempty"`
	// Declared is the provenance a harness declared for an external
	// trial; nil otherwise.
	Declared *TrialDeclared `json:"declared,omitempty"`
	// Lease is the lease an external trial is held under; nil when it is
	// not leased.
	Lease *TrialLease `json:"lease,omitempty"`
	// Sent is what the trial's model calls sent, as the platform counted
	// them; nil when it counted none.
	Sent *TrialSent `json:"sent,omitempty"`
	// Checks holds one run record per check of the task, in order, each
	// with its state, exit code, reason and evidence; the records' shape
	// is the document's description of the field.
	Checks []json.RawMessage `json:"checks,omitempty"`
	// Judgments holds one record per model judge of the trial, each with
	// its state, served model, tokens and cost.
	Judgments []json.RawMessage `json:"judgments,omitempty"`
	// Evidence holds the evidence references the trial kept.
	Evidence []json.RawMessage `json:"evidence,omitempty"`
	// Observed, Outputs and Values are the trial's observed setup, the
	// files its run produced and the current value of each metric it was
	// graded on, as JSON objects whose keys the suite and the metrics
	// decide.
	Observed json.RawMessage `json:"observed,omitempty"`
	Outputs  json.RawMessage `json:"outputs,omitempty"`
	Values   json.RawMessage `json:"values,omitempty"`
	// CreatedAt is when the trial was made, StartedAt when its run
	// started, AgentEndedAt when the system under test ended, and EndedAt
	// when the trial finished, its grading included. A time the trial has
	// not reached is zero.
	CreatedAt    time.Time `json:"created_at"`
	StartedAt    time.Time `json:"started_at"`
	AgentEndedAt time.Time `json:"agent_ended_at"`
	EndedAt      time.Time `json:"ended_at"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the trial, including any
// field Trial does not model.
func (t *Trial) RawJSON() json.RawMessage { return t.raw }

// UnmarshalJSON decodes the trial and keeps the JSON for RawJSON.
func (t *Trial) UnmarshalJSON(b []byte) error {
	type plain Trial
	if err := json.Unmarshal(b, (*plain)(t)); err != nil {
		return err
	}
	t.raw = keep(b)
	return nil
}

// IsRetry reports whether the trial runs an earlier trial again, which
// then leaves every figure. A trial read alone does not say that another
// replaced it; list the experiment's trials with ListTrialsParams.Retried
// to find those.
func (t *Trial) IsRetry() bool { return t.RetryOf != "" }

// TrialFailure says why a trial failed.
type TrialFailure struct {
	// Code is a stable code naming the failure.
	Code string `json:"code,omitempty"`
	// Detail is a sentence about it for a person.
	Detail string `json:"detail,omitempty"`
}

// TrialDeclared is the provenance a harness declared for an external
// trial.
type TrialDeclared struct {
	// HarnessVersion is the version of the harness that ran it.
	HarnessVersion string `json:"harness_version,omitempty"`
	// Image is the image it ran in.
	Image string `json:"image,omitempty"`
}

// TrialLease is the lease an external trial is held under.
type TrialLease struct {
	// ID identifies the lease.
	ID string `json:"id,omitempty"`
	// Subject is who holds it.
	Subject string `json:"subject,omitempty"`
	// ExpiresAt is when the lease ends unless it is renewed.
	ExpiresAt time.Time `json:"expires_at"`
}

// TrialSent counts the model calls a trial sent.
type TrialSent struct {
	// Requests is how many calls it sent.
	Requests int `json:"requests,omitempty"`
	// BySetting counts them by the model and reasoning they asked for.
	BySetting []TrialSentSetting `json:"by_setting,omitempty"`
}

// TrialSentSetting is the calls a trial sent with one model and
// reasoning.
type TrialSentSetting struct {
	// Model and Reasoning are the setting the calls asked for.
	Model     string `json:"model,omitempty"`
	Reasoning string `json:"reasoning,omitempty"`
	// Requests is how many calls asked for it.
	Requests int `json:"requests,omitempty"`
}

// ListTrialsParams filter and page the trials of an experiment. The zero
// value lists every trial, newest first, in pages of DefaultLimit; a nil
// boolean filter keeps both values.
type ListTrialsParams struct {
	// Cell keeps the trials of this cell, by id.
	Cell string
	// Task keeps the trials of this task, by digest or name.
	Task string
	// Candidate keeps the trials of this candidate, by digest.
	Candidate string
	// State keeps the trials in this state.
	State TrialState
	// Outcome keeps the trials that ended this way.
	Outcome TrialOutcome
	// Imported keeps the trials that were imported, or those that were
	// not.
	Imported *bool
	// Retried keeps the trials a retry replaced, or, false, those it did
	// not.
	Retried *bool
	// Metric is a trial-level metric, by name or digest, and Value keeps
	// the trials whose current value of it equals Value: true, false, a
	// number or a label, or "missing" for the trials with none. Value
	// needs Metric.
	Metric, Value string
	// Limit is the page size, at most MaxLimit; DefaultLimit when zero.
	Limit int
	// Cursor asks ListTrialsPage for the page after the one that returned
	// it. ListTrials ignores it.
	Cursor string
}

func (p *ListTrialsParams) query(cursor string) url.Values {
	if p == nil {
		return listQuery(0, cursor)
	}
	q := listQuery(p.Limit, cursor)
	setNonEmpty(q, "cell", p.Cell)
	setNonEmpty(q, "task", p.Task)
	setNonEmpty(q, "candidate", p.Candidate)
	setNonEmpty(q, "state", string(p.State))
	setNonEmpty(q, "outcome", string(p.Outcome))
	setBool(q, "imported", p.Imported)
	setBool(q, "retried", p.Retried)
	setNonEmpty(q, "metric", p.Metric)
	setNonEmpty(q, "value", p.Value)
	return q
}

var errValueNeedsMetric = errors.New("evals: ListTrialsParams.Value needs a Metric")

func (p *ListTrialsParams) check() error {
	if p != nil && p.Metric == "" && p.Value != "" {
		return errValueNeedsMetric
	}
	return nil
}

// ListTrials returns an iterator over the trials of the experiment id
// that params keeps, newest first, fetching pages as the loop asks for
// more. An id the caller's context holds no experiment by yields an error
// matching latere.ErrNotFound. params may be nil.
func (c *Client) ListTrials(ctx context.Context, id string, params *ListTrialsParams) iter.Seq2[*Trial, error] {
	p, err := experimentPath(id, "/trials")
	if err == nil {
		err = params.check()
	}
	if err != nil {
		return func(yield func(*Trial, error) bool) { yield(nil, err) }
	}
	return latere.Items(ctx, pageFunc[Trial](c, p, params.query))
}

// ListTrialsPage returns one page of the trials of the experiment id that
// params keeps, starting at params.Cursor, and the cursor of the next page
// in NextCursor.
func (c *Client) ListTrialsPage(ctx context.Context, id string, params *ListTrialsParams) (*latere.Page[*Trial], error) {
	p, err := experimentPath(id, "/trials")
	if err != nil {
		return nil, err
	}
	if err := params.check(); err != nil {
		return nil, err
	}
	cursor := ""
	if params != nil {
		cursor = params.Cursor
	}
	return page[Trial](ctx, c, p, params.query(cursor))
}

// GetTrial returns the trial id. An id the caller's context holds no trial
// by returns an error matching latere.ErrNotFound.
func (c *Client) GetTrial(ctx context.Context, id string) (*Trial, error) {
	seg, err := segment(id, errNoTrialID)
	if err != nil {
		return nil, err
	}
	return get[Trial](ctx, c, trialsPath+"/"+seg)
}
