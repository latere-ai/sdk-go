// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package evals

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"net/http"
	"net/url"
	"time"

	"latere.ai/x/sdk-go/latere"
)

// ErrSignInNotKept matches the refusal of an experiment an API key
// creates that would act as the person: its API code is
// "sign_in_not_kept", with status 403. Only the console keeps the sign-in
// such an experiment needs, so a program creates only experiments whose
// candidates are all CandidateExternal and whose suite names no check and
// no model judge. The *latere.Error the call returns is found with
// errors.As.
var ErrSignInNotKept = errors.New("evals: the experiment needs a sign-in the caller does not keep")

// codeError is an *latere.Error that also matches the sentinel of its API
// code, for a refusal a caller branches on whose status alone does not
// tell it apart: a 403 may refuse for any reason.
type codeError struct {
	err      *latere.Error
	sentinel error
}

func (e *codeError) Error() string { return e.err.Error() }

// Is reports whether target is the sentinel of the error's code.
func (e *codeError) Is(target error) bool { return target == e.sentinel }

// Unwrap returns the API's answer.
func (e *codeError) Unwrap() error { return e.err }

// withCodes returns err as a *codeError matching the sentinel of its API
// code when it has one that this package names, and err itself otherwise.
func withCodes(err error) error {
	var apiErr *latere.Error
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusForbidden && apiErr.Code == "sign_in_not_kept" {
		return &codeError{err: apiErr, sentinel: ErrSignInNotKept}
	}
	return err
}

// BudgetRequest is the budget an experiment is created with.
type BudgetRequest struct {
	// Name names the budget.
	Name string `json:"name,omitempty"`
	// CapMicroUSD is the cap, in millionths of a US dollar.
	CapMicroUSD int64 `json:"cap_usd_micro,omitzero"`
}

// CreateExperimentParams describe an experiment to create. Suite,
// Candidates and Budget are required.
type CreateExperimentParams struct {
	// Suite is the suite whose tasks the experiment runs, by name or
	// digest.
	Suite string `json:"suite"`
	// Candidates are the systems under test, each by name or digest.
	Candidates []string `json:"candidates"`
	// Budget is what its model calls are metered against.
	Budget BudgetRequest `json:"budget"`
	// Trials is how many trials each cell runs; the API's default when
	// zero.
	Trials int `json:"trials,omitzero"`
	// Mode is how its trials come to be; ModeExecute when empty.
	Mode Mode `json:"mode,omitempty"`
	// Parallelism is how many trials run at once; the API's default when
	// zero.
	Parallelism int `json:"parallelism,omitzero"`
	// Reference is what every other candidate is compared with; nil lets
	// the API choose.
	Reference *Reference `json:"reference,omitzero"`
	// Scheduling is how its cells take turns at Parallelism and how its
	// external trials are leased; SchedulingTaskByTask when empty. Fixed
	// at create.
	Scheduling Scheduling `json:"scheduling,omitempty"`
	// Comparisons are the comparisons it declares it will make, computed
	// without being asked and corrected together. Fixed at create.
	Comparisons []DeclaredComparison `json:"comparisons,omitempty"`
	// Independence is the test of independence it declares.
	Independence *Independence `json:"independence,omitzero"`
	// Metadata are the owner's own labels, such as a commit, a branch or
	// a CI run: at most 16 pairs, none with a key beginning "examen.".
	// Evals reads none of them, and ListExperimentsParams.MetadataKey
	// finds the experiments that carry one. Fixed at create.
	Metadata map[string]string `json:"metadata,omitempty"`
	// SignIn is an opaque value the program's installation gave for this
	// create, such as a handle to a sign-in it keeps for the experiment.
	// Evals passes it to the authorizer and keeps it nowhere.
	SignIn string `json:"sign_in,omitempty"`
	// Owner is the context to create the experiment in, a rendered
	// subject; the context the token acts in when empty.
	Owner string `json:"-"`
	// IdempotencyKey, when set, is sent as the Idempotency-Key header: a
	// repeat with the same key and body within 24 hours answers the first
	// experiment instead of making a second, and the call is then retried
	// after a failure. Without one the call is sent once, and is not
	// retried.
	IdempotencyKey string `json:"-"`
}

// CreateExperiment creates an experiment as params say and returns it,
// pending. A request the API refuses returns a *latere.Error, found with
// errors.As, whose Code says why.
//
// An API key creates only an experiment whose candidates are all
// CandidateExternal and whose suite names no check and no model judge.
// Any other acts as the person while it runs, and only the console keeps
// the sign-in such an experiment needs, so a key's create of one is
// refused 403 sign_in_not_kept, which matches ErrSignInNotKept.
func (c *Client) CreateExperiment(ctx context.Context, params *CreateExperimentParams) (*Experiment, error) {
	if params == nil {
		return nil, errNoParams
	}
	req := &latere.Request{Method: http.MethodPost, Path: experimentsPath, Body: params, IdempotencyKey: params.IdempotencyKey}
	if params.Owner != "" {
		req.Query = url.Values{"owner": {params.Owner}}
	}
	var out Experiment
	if err := c.cfg.Do(ctx, req, &out); err != nil {
		return nil, withCodes(err)
	}
	return &out, nil
}

var errNoParams = errors.New("evals: the parameters are nil")

// LeaseTrialsParams say which trials to lease, or which lease to renew.
type LeaseTrialsParams struct {
	// Candidate is an external candidate of the experiment, by digest;
	// empty takes trials of every external candidate.
	Candidate string `json:"candidate,omitempty"`
	// Max is the most trials to take; the API's default when zero. The
	// experiment's parallelism and each cell's share of it bound it too.
	Max int `json:"max,omitzero"`
	// Renew is the Lease of a lease to extend, in place of taking trials.
	Renew string `json:"renew,omitempty"`
	// IdempotencyKey, when set, is sent as the Idempotency-Key header, so
	// a repeat after a lost answer gets the first lease's answer and the
	// call is retried after a failure. Without one the call is sent once.
	IdempotencyKey string `json:"-"`
}

// A Lease is the pending trials of an external candidate handed to a
// harness, held until ExpiresAt unless renewed. SubmitTrial sends each
// trial's outcome with the lease's id.
type Lease struct {
	// Lease identifies the lease; SubmitTrialParams.Lease and
	// LeaseTrialsParams.Renew name it.
	Lease string `json:"lease"`
	// ExpiresAt is when the lease ends unless it is renewed.
	ExpiresAt time.Time `json:"expires_at"`
	// ModelKey is the experiment's Lux key, which each trial's model calls
	// go through so the experiment's budget binds them and the trial's
	// cost is recorded. It is a secret: do not log it. Empty when the
	// installation has no key source.
	ModelKey string `json:"model_key,omitempty"`
	// Trials are the trials leased, none on a renewal that names none.
	Trials []LeasedTrial `json:"trials"`

	raw jsontext.Value
}

// RawJSON returns the JSON the API answered for the lease, including any
// field Lease does not model. It holds the model key, when there is one.
func (l *Lease) RawJSON() jsontext.Value { return l.raw }

// UnmarshalJSON decodes the lease and keeps the JSON for RawJSON.
func (l *Lease) UnmarshalJSON(b []byte) error {
	type plain Lease
	if err := json.Unmarshal(b, (*plain)(l)); err != nil {
		return err
	}
	l.raw = keep(b)
	return nil
}

// A LeasedTrial is one trial of a lease, with what a harness needs to run
// it.
type LeasedTrial struct {
	// ID identifies the trial, Cell its cell and Index which of the
	// cell's trials it is.
	ID    string `json:"id,omitempty"`
	Cell  string `json:"cell,omitempty"`
	Index int    `json:"index,omitzero"`
	// Candidate is the candidate the trial runs.
	Candidate LeasedCandidate `json:"candidate"`
	// Task is the task it runs.
	Task LeasedTask `json:"task"`
	// Instruction is the task's instruction; Inputs its input files, as
	// the API describes them; Outputs the paths the harness reports.
	Instruction string           `json:"instruction,omitempty"`
	Inputs      []jsontext.Value `json:"inputs,omitempty"`
	Outputs     []string         `json:"outputs,omitempty"`
	// MaxTurns and Timeout are the task's limits, Timeout as a duration
	// such as "10m".
	MaxTurns int    `json:"max_turns,omitzero"`
	Timeout  string `json:"timeout,omitempty"`
	// LuxLabels is the value of the Lux-Labels header each of the trial's
	// model calls carries, attributing the call to the trial.
	LuxLabels string `json:"lux_labels,omitempty"`
}

// A LeasedCandidate is the candidate a leased trial runs.
type LeasedCandidate struct {
	// Name and Digest name the candidate.
	Name   string `json:"name,omitempty"`
	Digest string `json:"digest,omitempty"`
	// External is the setup the candidate declares.
	External *LeasedExternal `json:"external,omitzero"`
}

// A LeasedExternal is the setup an external candidate declares.
type LeasedExternal struct {
	// Digest names the setup.
	Digest string `json:"digest,omitempty"`
	// Harness, Version, Model and Reasoning are the setup's.
	Harness   string `json:"harness,omitempty"`
	Version   string `json:"version,omitempty"`
	Model     string `json:"model,omitempty"`
	Reasoning string `json:"reasoning,omitempty"`
	// Flags are the harness's flags.
	Flags map[string]string `json:"flags,omitempty"`
}

// A LeasedTask is the task a leased trial runs.
type LeasedTask struct {
	// Name and Digest name the task.
	Name   string `json:"name,omitempty"`
	Digest string `json:"digest,omitempty"`
}

// LeaseTrials takes pending trials of the external candidates of the
// experiment id, or renews the lease params.Renew names, and returns the
// lease. It returns a nil Lease and no error when nothing may be leased
// now. params may be nil, which takes trials of every external candidate.
//
// The answer of an experiment with a key source carries the experiment's
// model key; see Lease.ModelKey.
func (c *Client) LeaseTrials(ctx context.Context, id string, params *LeaseTrialsParams) (*Lease, error) {
	p, err := experimentPath(id, "/work/lease")
	if err != nil {
		return nil, err
	}
	if params == nil {
		params = &LeaseTrialsParams{}
	}
	var out Lease
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodPost, Path: p, Body: params, IdempotencyKey: params.IdempotencyKey}, &out); err != nil {
		return nil, err
	}
	if out.Lease == "" {
		return nil, nil
	}
	return &out, nil
}

// SubmitTrialParams are the outcome of a leased external trial. Lease,
// Outcome, StartedAt and AgentEndedAt are required.
type SubmitTrialParams struct {
	// Lease is the Lease.Lease the trial was leased under.
	Lease string `json:"lease"`
	// Outcome is how the run ended: any outcome but OutcomeCanceled,
	// which only the platform records.
	Outcome TrialOutcome `json:"outcome"`
	// StartedAt and AgentEndedAt are when the run started and when the
	// system under test ended.
	StartedAt    time.Time `json:"started_at"`
	AgentEndedAt time.Time `json:"agent_ended_at"`
	// Failure says why a run that failed did.
	Failure *SubmitFailure `json:"failure,omitzero"`
	// Declared is the provenance the harness declares.
	Declared *TrialDeclared `json:"declared,omitzero"`
	// Outputs are the files the run produced, each an Arca reference with
	// its digest.
	Outputs []SubmitOutput `json:"outputs,omitempty"`
	// LuxRequests are the ids of the Lux requests the run made, as given.
	LuxRequests []string `json:"lux_requests,omitempty"`
	// Measurements are the run's self-reported values, kept under
	// "self:" and the harness's name.
	Measurements []SubmitMeasurement `json:"measurements,omitempty"`
	// ServiceUsage is what each service the run used cost, by the
	// service's name, and ServiceVersions their versions by service.
	ServiceUsage    map[string]ServiceUsage `json:"service_usage,omitempty"`
	ServiceVersions map[string]string       `json:"service_versions,omitempty"`
	// Ref is a reference of the harness's own, such as its run's id.
	Ref string `json:"ref,omitempty"`
}

// SubmitFailure says why a submitted run failed.
type SubmitFailure struct {
	// Code is a stable code naming the failure.
	Code string `json:"code"`
	// Detail is a sentence about it.
	Detail string `json:"detail,omitempty"`
}

// SubmitOutput is a file a submitted run produced.
type SubmitOutput struct {
	// Path is the path the task names for it.
	Path string `json:"path"`
	// Arca is where the file is kept.
	Arca ArcaFile `json:"arca"`
}

// ArcaFile is a file in the owner's file store, named by its path and its
// content's digest.
type ArcaFile struct {
	// Path is the file's path, and Digest its content's digest.
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

// SubmitMeasurement is one self-reported value of a submitted run.
type SubmitMeasurement struct {
	// Metric is the metric, by name or digest.
	Metric string `json:"metric"`
	// Value is the value: true or false, a number or a label.
	Value any `json:"value"`
	// Rationale says how the harness reached it.
	Rationale string `json:"rationale,omitempty"`
}

// ServiceUsage is what one service a submitted run used cost.
type ServiceUsage struct {
	// CostMicroUSD is the cost, in millionths of a US dollar.
	CostMicroUSD int64 `json:"cost_usd_micro"`
	// Units counts what the services metered, by unit.
	Units map[string]float64 `json:"units,omitempty"`
}

// SubmitTrial records the outcome of the leased external trial id and
// returns the trial. A repeat of the same lease and body answers the same
// trial and changes nothing, so the call is retried after a failure. A
// lease that expired or is not held returns an *latere.Error of status
// 409.
func (c *Client) SubmitTrial(ctx context.Context, id string, params *SubmitTrialParams) (*Trial, error) {
	seg, err := segment(id, errNoTrialID)
	if err != nil {
		return nil, err
	}
	if params == nil {
		return nil, errNoParams
	}
	var out Trial
	req := &latere.Request{Method: http.MethodPost, Path: trialsPath + "/" + seg + "/submit", Body: params, Retry: latere.RetryAlways}
	if err := c.cfg.Do(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
