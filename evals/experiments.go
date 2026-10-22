// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package evals

import (
	"context"
	"encoding/json"
	"iter"
	"net/url"
	"time"

	"latere.ai/x/sdk-go/latere"
)

// experimentsPath is the collection of experiments.
const experimentsPath = basePath + "/experiments"

// ExperimentState is where an experiment is in its life.
type ExperimentState string

// The states of an experiment.
const (
	// ExperimentPending is an experiment no cell of which has started.
	ExperimentPending ExperimentState = "pending"
	// ExperimentRunning is an experiment whose cells run their trials.
	ExperimentRunning ExperimentState = "running"
	// ExperimentGrading is an experiment whose trials have run and are
	// being graded.
	ExperimentGrading ExperimentState = "grading"
	// ExperimentComplete is an experiment every cell of which finished,
	// complete or parked; its comparisons are computed.
	ExperimentComplete ExperimentState = "complete"
	// ExperimentCanceled is an experiment stopped before its end; every
	// trial it finished is kept.
	ExperimentCanceled ExperimentState = "canceled"
)

// Finished reports whether an experiment in state s has ended, complete
// or canceled, and will not change state again.
func (s ExperimentState) Finished() bool {
	return s == ExperimentComplete || s == ExperimentCanceled
}

// Mode is how an experiment's trials come to be.
type Mode string

// The modes of an experiment.
const (
	// ModeExecute, the default, has the platform run every trial.
	ModeExecute Mode = "execute"
	// ModeImport has the platform run none: each trial is one a harness
	// ran elsewhere and imported.
	ModeImport Mode = "import"
)

// Scheduling is how an experiment's cells take turns at its parallelism.
type Scheduling string

// The schedulings of an experiment.
const (
	// SchedulingTaskByTask, the default, runs one task's cells before the
	// next task's.
	SchedulingTaskByTask Scheduling = "task_by_task"
	// SchedulingInterleaved runs every cell of every task in turn, so the
	// same trial of every cell runs in one window, whatever its task.
	SchedulingInterleaved Scheduling = "interleaved"
)

// CellState is where a cell is in its life. A cell blocked, over budget
// or canceled is parked: it does not finish on its own.
type CellState string

// The states of a cell.
const (
	// CellPending is a cell no trial of which has started.
	CellPending CellState = "pending"
	// CellSmoking is a cell running its smoke trials, the first few that
	// show the candidate can run the task before the rest run.
	CellSmoking CellState = "smoking"
	// CellRunning is a cell running its trials.
	CellRunning CellState = "running"
	// CellGrading is a cell whose trials have run and are being graded.
	CellGrading CellState = "grading"
	// CellComplete is a cell every trial of which ran and was graded.
	CellComplete CellState = "complete"
	// CellBlocked is a cell parked before its trials could run, such as
	// when its smoke trials failed or its run was denied; its
	// ParkedReason says why. A retry of the cell runs it again.
	CellBlocked CellState = "blocked"
	// CellOverBudget is a cell parked because the experiment's budget ran
	// out. A retry runs it again once there is budget.
	CellOverBudget CellState = "over_budget"
	// CellCanceled is a cell parked because its experiment was canceled.
	CellCanceled CellState = "canceled"
)

// Parked reports whether a cell in state s is parked: blocked, over budget
// or canceled, waiting for a retry or for nothing.
func (s CellState) Parked() bool {
	return s == CellBlocked || s == CellOverBudget || s == CellCanceled
}

// TrialState is where a trial is in its life.
type TrialState string

// The states of a trial.
const (
	// TrialPending is a trial that has not started.
	TrialPending TrialState = "pending"
	// TrialRunning is a trial whose system under test runs.
	TrialRunning TrialState = "running"
	// TrialChecking is a trial whose system under test ended and whose
	// checks run.
	TrialChecking TrialState = "checking"
	// TrialDone is a finished trial, which has an Outcome.
	TrialDone TrialState = "done"
)

// TrialOutcome is how a finished trial ended.
type TrialOutcome string

// The outcomes of a trial. The first four are a result of the system under
// test and count in its results; the others leave the trial out of them.
const (
	// OutcomeCompleted is a system under test that ended its work itself.
	// Ending is not success: a task's success is a check's metric.
	OutcomeCompleted TrialOutcome = "completed"
	// OutcomeDeclined is a system under test that refused the task, said
	// it would not or could not do it, or stopped without trying.
	OutcomeDeclined TrialOutcome = "declined"
	// OutcomeTimedOut is a trial whose task's timeout passed while the
	// system under test ran.
	OutcomeTimedOut TrialOutcome = "timed_out"
	// OutcomeTurnLimit is a trial whose system under test reached the
	// task's MaxTurns.
	OutcomeTurnLimit TrialOutcome = "turn_limit"
	// OutcomeOverBudget is a trial stopped because the experiment's budget
	// ran out.
	OutcomeOverBudget TrialOutcome = "over_budget"
	// OutcomeInfraFailed is a trial the platform failed to run.
	OutcomeInfraFailed TrialOutcome = "infra_failed"
	// OutcomeCanceled is a trial stopped because its experiment was
	// canceled.
	OutcomeCanceled TrialOutcome = "canceled"
)

// An Experiment runs a suite's tasks for each of its candidates. Each
// definition it names is named by its digest.
type Experiment struct {
	// ID identifies the experiment, such as "exp_01jb2c3d4e5f6g7h8j9k0m1n2p".
	ID string `json:"id"`
	// Owner is the context the experiment belongs to, a person or an
	// organization.
	Owner string `json:"owner"`
	// CreatedBy is who created it, which differs from Owner for an
	// experiment of an organization's, created by one of its members.
	CreatedBy string `json:"created_by,omitempty"`
	// Suite is the suite whose tasks it runs.
	Suite string `json:"suite"`
	// Candidates are the systems under test.
	Candidates []string `json:"candidates"`
	// Reference is what every other candidate is compared with.
	Reference Reference `json:"reference"`
	// Trials is how many trials each cell runs.
	Trials int `json:"trials"`
	// Parallelism is how many trials run at once.
	Parallelism int `json:"parallelism"`
	// Mode is how its trials come to be.
	Mode Mode `json:"mode"`
	// Scheduling is how its cells take turns at Parallelism.
	Scheduling Scheduling `json:"scheduling,omitempty"`
	// State is where it is in its life.
	State ExperimentState `json:"state"`
	// Budget is what its model calls are metered against.
	Budget Budget `json:"budget"`
	// Progress counts its cells and trials by state, and its finished
	// trials by outcome.
	Progress Progress `json:"progress"`
	// Usage is what the calls made with the experiment's key recorded, on
	// a read of one experiment whose trials ran with its key; nil
	// otherwise.
	Usage *Usage `json:"usage,omitempty"`
	// Comparisons are the comparisons it declared when it was created.
	Comparisons []DeclaredComparison `json:"comparisons,omitempty"`
	// Independence is the test of independence it declared, nil when it
	// declared none.
	Independence *Independence `json:"independence,omitempty"`
	// Metadata are its labels.
	Metadata map[string]string `json:"metadata,omitempty"`
	// CreatedAt is when it was created, and UpdatedAt when it last
	// changed.
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the experiment, including
// any field Experiment does not model.
func (e *Experiment) RawJSON() json.RawMessage { return e.raw }

// UnmarshalJSON decodes the experiment and keeps the JSON for RawJSON.
func (e *Experiment) UnmarshalJSON(b []byte) error {
	type plain Experiment
	if err := json.Unmarshal(b, (*plain)(e)); err != nil {
		return err
	}
	e.raw = keep(b)
	return nil
}

// A Reference is what an experiment compares every other candidate with:
// a baseline, or one of its candidates. One field is set.
type Reference struct {
	// Baseline is the id of a baseline record.
	Baseline string `json:"baseline,omitempty"`
	// Candidate is one of the experiment's candidates, by digest.
	Candidate string `json:"candidate,omitempty"`
}

// A Budget is what an experiment's model calls are metered against, with
// its figures as they were read when the answer was built. The figures
// are nil when they could not be read, and for a budget in another
// currency than US dollars, which Currency names instead.
type Budget struct {
	// Name names the budget.
	Name string `json:"name,omitempty"`
	// CapMicroUSD is the budget's amount, in millionths of a US dollar.
	CapMicroUSD *int64 `json:"cap_usd_micro,omitempty"`
	// SpentMicroUSD is what it spent.
	SpentMicroUSD *int64 `json:"spent_usd_micro,omitempty"`
	// RemainingMicroUSD is the smallest remainder across the budget and
	// every budget that pays for it.
	RemainingMicroUSD *int64 `json:"remaining_usd_micro,omitempty"`
	// Currency is the budget's currency when it is not US dollars.
	Currency string `json:"currency,omitempty"`
}

// Progress counts an experiment's cells and trials by state, and its
// finished trials by outcome. A state or an outcome with no count is
// absent.
type Progress struct {
	// Cells counts the cells by state.
	Cells map[CellState]int `json:"cells,omitempty"`
	// Trials counts the trials by state.
	Trials map[TrialState]int `json:"trials,omitempty"`
	// Outcomes counts the finished trials by outcome.
	Outcomes map[TrialOutcome]int `json:"outcomes,omitempty"`
}

// Usage is what the calls made with an experiment's key recorded, as far
// as ReadTo.
type Usage struct {
	// Requests counts the calls.
	Requests int64 `json:"requests"`
	// CostMicroUSD is the priced calls' cost, in millionths of a US
	// dollar.
	CostMicroUSD int64 `json:"cost_usd_micro"`
	// UnpricedRequests counts the calls that could not be priced, whose
	// cost is unknown.
	UnpricedRequests int64 `json:"unpriced_requests"`
	// Unattributed are the calls no trial claims, which count toward the
	// experiment and no trial.
	Unattributed *UsageCount `json:"unattributed,omitempty"`
	// ReadTo is how far the records were read.
	ReadTo time.Time `json:"read_to"`
}

// A UsageCount is a count of calls and their cost.
type UsageCount struct {
	// Requests counts the calls.
	Requests int64 `json:"requests"`
	// CostMicroUSD is the priced calls' cost, in millionths of a US
	// dollar.
	CostMicroUSD int64 `json:"cost_usd_micro"`
	// UnpricedRequests counts the calls that could not be priced.
	UnpricedRequests int64 `json:"unpriced_requests"`
}

// A DeclaredComparison is a comparison an experiment declared it will
// make: two of its candidates, one candidate's two tasks, or in its
// general form two levels of one cell variable. Each candidate and task is
// named by its digest.
type DeclaredComparison struct {
	// Candidate and ReferenceCandidate are the two candidates compared.
	Candidate          string `json:"candidate,omitempty"`
	ReferenceCandidate string `json:"reference_candidate,omitempty"`
	// Task and ReferenceTask are the two tasks of Candidate compared.
	Task          string `json:"task,omitempty"`
	ReferenceTask string `json:"reference_task,omitempty"`
	// Variable is, on a pair of candidates, the one thing they differ in,
	// such as "model"; in the general form, the cell variable compared.
	Variable string `json:"variable,omitempty"`
	// Levels are, in the general form, the variable's two levels, the
	// candidate side's first.
	Levels []string `json:"levels,omitempty"`
	// Where holds, in the general form, cell variables each to one level.
	Where map[string]string `json:"where,omitempty"`
	// By are, in the general form, the cell variables the comparison is
	// stratified by.
	By []string `json:"by,omitempty"`
	// Metric is the metric of its primary result, by digest; empty for the
	// suite's primary metric.
	Metric string `json:"metric,omitempty"`
}

// Independence is an experiment's declared test of whether a label
// metric depends on one cell variable.
type Independence struct {
	// Metric is the label metric tested, by digest.
	Metric string `json:"metric"`
	// Variable is the cell variable it may depend on; "candidate" when
	// empty.
	Variable string `json:"variable,omitempty"`
	// By are the cell variables the test is stratified by.
	By []string `json:"by,omitempty"`
	// Where holds cell variables each to one level, selecting the cells
	// tested; every cell when empty.
	Where map[string]string `json:"where,omitempty"`
}

// ListExperimentsParams filter and page the experiments of the caller's
// context. The zero value lists every one, newest first, in pages of
// DefaultLimit.
type ListExperimentsParams struct {
	// Suite keeps the experiments of a version of a suite, by digest, or
	// of every version of a suite, by name.
	Suite string
	// Candidate keeps the experiments of this candidate, by digest.
	Candidate string
	// State keeps the experiments in this state.
	State ExperimentState
	// Mode keeps the experiments of this mode.
	Mode Mode
	// Baseline keeps the experiments whose Reference is this baseline
	// record, by id.
	Baseline string
	// MetadataKey and MetadataValue keep the experiments whose metadata
	// holds the key with exactly the value. The API takes one such pair.
	MetadataKey, MetadataValue string
	// Limit is the page size, at most MaxLimit;
	// DefaultLimit when zero.
	Limit int
	// Cursor asks ListExperimentsPage for the page after the one that
	// returned it. ListExperiments ignores it.
	Cursor string
}

func (p *ListExperimentsParams) query(cursor string) url.Values {
	if p == nil {
		return listQuery(0, cursor)
	}
	q := listQuery(p.Limit, cursor)
	setNonEmpty(q, "suite", p.Suite)
	setNonEmpty(q, "candidate", p.Candidate)
	setNonEmpty(q, "state", string(p.State))
	setNonEmpty(q, "mode", string(p.Mode))
	setNonEmpty(q, "baseline", p.Baseline)
	setMetadata(q, p.MetadataKey, p.MetadataValue)
	return q
}

func (p *ListExperimentsParams) check() error {
	if p != nil && p.MetadataKey == "" && p.MetadataValue != "" {
		return errMetadata
	}
	return nil
}

// ListExperiments returns an iterator over the experiments params keeps,
// newest first, fetching pages as the loop asks for more. params may be
// nil.
func (c *Client) ListExperiments(ctx context.Context, params *ListExperimentsParams) iter.Seq2[*Experiment, error] {
	if err := params.check(); err != nil {
		return func(yield func(*Experiment, error) bool) { yield(nil, err) }
	}
	return latere.Items(ctx, pageFunc[Experiment](c, experimentsPath, params.query))
}

// ListExperimentsPage returns one page of the experiments params keeps,
// starting at params.Cursor, and the cursor of the next page in
// NextCursor.
func (c *Client) ListExperimentsPage(ctx context.Context, params *ListExperimentsParams) (*latere.Page[*Experiment], error) {
	if err := params.check(); err != nil {
		return nil, err
	}
	cursor := ""
	if params != nil {
		cursor = params.Cursor
	}
	return page[Experiment](ctx, c, experimentsPath, params.query(cursor))
}

// experimentPath is the path of the experiment id, followed by suffix.
func experimentPath(id, suffix string) (string, error) {
	seg, err := segment(id, errNoID)
	if err != nil {
		return "", err
	}
	return experimentsPath + "/" + seg + suffix, nil
}

// GetExperiment returns the experiment id, with its progress as it is now.
// An id the caller's context holds no experiment by returns an error
// matching latere.ErrNotFound.
func (c *Client) GetExperiment(ctx context.Context, id string) (*Experiment, error) {
	p, err := experimentPath(id, "")
	if err != nil {
		return nil, err
	}
	return get[Experiment](ctx, c, p)
}

// ---- cells ----

// A Cell is one task of one candidate in an experiment, which runs the
// experiment's Trials trials.
type Cell struct {
	// ID identifies the cell.
	ID string `json:"id"`
	// Experiment is the experiment the cell belongs to.
	Experiment string `json:"experiment"`
	// Owner is the context the cell belongs to, its experiment's.
	Owner string `json:"owner,omitempty"`
	// Task and Candidate are the cell's task and candidate, by digest.
	Task      string `json:"task"`
	Candidate string `json:"candidate"`
	// State is where the cell is in its life.
	State CellState `json:"state"`
	// ParkedReason says why a parked cell is parked, as a stable code such
	// as "run_denied" or "model_unpriced", with the figures or the trial
	// behind it.
	ParkedReason string `json:"parked_reason,omitempty"`
	// Adopted are trials of an identical earlier cell that this cell
	// reads as its own, by id.
	Adopted []string `json:"adopted,omitempty"`
	// CreatedAt is when the cell was made, and UpdatedAt when it last
	// changed.
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the cell, including any
// field Cell does not model.
func (c *Cell) RawJSON() json.RawMessage { return c.raw }

// UnmarshalJSON decodes the cell and keeps the JSON for RawJSON.
func (c *Cell) UnmarshalJSON(b []byte) error {
	type plain Cell
	if err := json.Unmarshal(b, (*plain)(c)); err != nil {
		return err
	}
	c.raw = keep(b)
	return nil
}

// ListCellsParams filter and page the cells of an experiment. The zero
// value lists every cell, newest first, in pages of DefaultLimit.
type ListCellsParams struct {
	// State keeps the cells in this state.
	State CellState
	// Task keeps the cells of this task, by digest or name.
	Task string
	// Candidate keeps the cells of this candidate, by digest.
	Candidate string
	// Limit is the page size, at most MaxLimit;
	// DefaultLimit when zero.
	Limit int
	// Cursor asks ListCellsPage for the page after the one that returned
	// it. ListCells ignores it.
	Cursor string
}

func (p *ListCellsParams) query(cursor string) url.Values {
	if p == nil {
		return listQuery(0, cursor)
	}
	q := listQuery(p.Limit, cursor)
	setNonEmpty(q, "state", string(p.State))
	setNonEmpty(q, "task", p.Task)
	setNonEmpty(q, "candidate", p.Candidate)
	return q
}

// ListCells returns an iterator over the cells of the experiment id that
// params keeps, newest first, fetching pages as the loop asks for more.
// An id the caller's context holds no experiment by yields an error
// matching latere.ErrNotFound. params may be nil.
func (c *Client) ListCells(ctx context.Context, id string, params *ListCellsParams) iter.Seq2[*Cell, error] {
	p, err := experimentPath(id, "/cells")
	if err != nil {
		return func(yield func(*Cell, error) bool) { yield(nil, err) }
	}
	return latere.Items(ctx, pageFunc[Cell](c, p, params.query))
}

// ListCellsPage returns one page of the cells of the experiment id that
// params keeps, starting at params.Cursor, and the cursor of the next page
// in NextCursor.
func (c *Client) ListCellsPage(ctx context.Context, id string, params *ListCellsParams) (*latere.Page[*Cell], error) {
	p, err := experimentPath(id, "/cells")
	if err != nil {
		return nil, err
	}
	cursor := ""
	if params != nil {
		cursor = params.Cursor
	}
	return page[Cell](ctx, c, p, params.query(cursor))
}

// ---- comparisons ----

// A Verdict is what a comparison found of a metric on one task.
type Verdict string

// The verdicts of a comparison's task-level results.
const (
	// VerdictImproved is a difference past the noise, in the metric's
	// better direction.
	VerdictImproved Verdict = "improved"
	// VerdictRegressed is a difference past the noise, in its worse
	// direction.
	VerdictRegressed Verdict = "regressed"
	// VerdictChanged is a difference past the noise of a metric that has
	// no better direction, or of a label metric's labels.
	VerdictChanged Verdict = "changed"
	// VerdictWithinNoise is no difference the test tells apart from the
	// noise.
	VerdictWithinNoise Verdict = "within_noise"
	// VerdictInsufficient is too little to test, such as too few trials or
	// tasks on a side; the result's reason says what.
	VerdictInsufficient Verdict = "insufficient"
)

// ComparisonState is where a comparison's computation is.
type ComparisonState string

// The states of a comparison.
const (
	// ComparisonComputing is a comparison being computed.
	ComparisonComputing ComparisonState = "computing"
	// ComparisonComplete is a computed comparison, whose figures may still
	// change until it is Final.
	ComparisonComplete ComparisonState = "complete"
	// ComparisonFailed is a comparison that could not be computed.
	ComparisonFailed ComparisonState = "failed"
)

// A Comparison compares a candidate with a reference: the experiment's
// reference candidate or baseline, or, across tasks, one task of the
// candidate with another. Each candidate and task is named by its digest.
//
// A comparison's figures, its results and summaries with their intervals,
// corrections and windows, are not modeled; read them from RawJSON.
type Comparison struct {
	// ID identifies the comparison.
	ID string `json:"id"`
	// Experiment is the experiment the comparison belongs to.
	Experiment string `json:"experiment"`
	// Owner is the context the comparison belongs to.
	Owner string `json:"owner,omitempty"`
	// Candidate is the candidate compared.
	Candidate string `json:"candidate"`
	// ReferenceCandidate is the reference side's candidate, when it is
	// one.
	ReferenceCandidate string `json:"reference_candidate,omitempty"`
	// Baseline is the baseline record the reference side reads, when it
	// is one.
	Baseline string `json:"baseline,omitempty"`
	// Task and ReferenceTask are, for a comparison across tasks, the
	// candidate's task and the reference task.
	Task          string `json:"task,omitempty"`
	ReferenceTask string `json:"reference_task,omitempty"`
	// Automatic reports the comparison the platform keeps of a candidate
	// against its experiment's reference.
	Automatic bool `json:"automatic"`
	// Exploratory reports a comparison computed on request, or one against
	// a reference set after a cell started, which a report never leads
	// with.
	Exploratory bool `json:"exploratory"`
	// Declared is, for a comparison its experiment declared, its place in
	// the experiment's Comparisons; nil otherwise.
	Declared *int `json:"declared,omitempty"`
	// Declaration is, for a declared comparison of the general form, what
	// it compares.
	Declaration *Declaration `json:"declaration,omitempty"`
	// Variable are the fields of the candidates' setups the comparison
	// means to vary.
	Variable []string `json:"variable,omitempty"`
	// SingleVariable reports that no other field of the setups differs on
	// any task both sides ran; nil until the comparison is computed.
	SingleVariable *bool `json:"single_variable,omitempty"`
	// Confounds name each confound of its results, a difference besides
	// Variable that could explain them.
	Confounds []string `json:"confounds,omitempty"`
	// DeclaredProvenance are the sides, "candidate" or "reference", whose
	// candidate is a CandidateExternal, so whose setup its harness
	// declared rather than the platform observed.
	DeclaredProvenance []string `json:"declared_provenance,omitempty"`
	// State is where its computation is.
	State ComparisonState `json:"state"`
	// Final reports that both sides finished, so no figure changes again.
	Final bool `json:"final"`
	// VerdictCounts count, for each metric, the task-level results of each
	// verdict.
	VerdictCounts map[string]map[Verdict]int `json:"verdict_counts,omitempty"`
	// Taxonomies name, for each metric of VocabularyOpen it reads, the
	// taxonomy record its canonical labels were read at.
	Taxonomies map[string]string `json:"taxonomies,omitempty"`
	// AnalysisVersion names the analysis that computed it, such as
	// "examen.analysis/v1".
	AnalysisVersion string `json:"analysis_version,omitempty"`
	// InputsDigest is the digest of the inputs it was computed from.
	InputsDigest string `json:"inputs_digest,omitempty"`
	// Seed is the root seed of its resampling, in hex.
	Seed string `json:"seed,omitempty"`
	// CreatedAt is when it was made, and ComputedAt when it was last
	// computed.
	CreatedAt  time.Time `json:"created_at"`
	ComputedAt time.Time `json:"computed_at"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the comparison, with its
// figures and any other field Comparison does not model.
func (c *Comparison) RawJSON() json.RawMessage { return c.raw }

// UnmarshalJSON decodes the comparison and keeps the JSON for RawJSON.
func (c *Comparison) UnmarshalJSON(b []byte) error {
	type plain Comparison
	if err := json.Unmarshal(b, (*plain)(c)); err != nil {
		return err
	}
	c.raw = keep(b)
	return nil
}

// A Declaration is what a declared comparison of the general form
// compares: two levels of one cell variable.
type Declaration struct {
	// Variable is the cell variable compared.
	Variable string `json:"variable,omitempty"`
	// Levels are its two levels, the candidate side's first.
	Levels []string `json:"levels,omitempty"`
	// Where holds cell variables each to one level.
	Where map[string]string `json:"where,omitempty"`
	// By are the cell variables the comparison is stratified by.
	By []string `json:"by,omitempty"`
	// Metric is the metric of its primary result, by digest.
	Metric string `json:"metric,omitempty"`
}

// ListExperimentComparisonsParams filter and page the comparisons of an
// experiment. The zero value lists every one, in pages of DefaultLimit; a nil
// boolean filter keeps both values.
type ListExperimentComparisonsParams struct {
	// Candidate keeps the comparisons of this candidate, by digest.
	Candidate string
	// ReferenceCandidate keeps the comparisons against this candidate, by
	// digest.
	ReferenceCandidate string
	// Baseline keeps the comparisons against this baseline record, by id.
	Baseline string
	// Automatic, Declared, Exploratory and Final keep the comparisons
	// whose field of that name, or for Declared whether it was declared,
	// is the value given.
	Automatic, Declared, Exploratory, Final *bool
	// Limit is the page size, at most MaxLimit;
	// DefaultLimit when zero.
	Limit int
	// Cursor asks ListExperimentComparisonsPage for the page after the one
	// that returned it. ListExperimentComparisons ignores it.
	Cursor string
}

func (p *ListExperimentComparisonsParams) query(cursor string) url.Values {
	if p == nil {
		return listQuery(0, cursor)
	}
	q := listQuery(p.Limit, cursor)
	setNonEmpty(q, "candidate", p.Candidate)
	setNonEmpty(q, "reference_candidate", p.ReferenceCandidate)
	setNonEmpty(q, "baseline", p.Baseline)
	setBool(q, "automatic", p.Automatic)
	setBool(q, "declared", p.Declared)
	setBool(q, "exploratory", p.Exploratory)
	setBool(q, "final", p.Final)
	return q
}

// ListExperimentComparisons returns an iterator over the comparisons of
// the experiment id that params keeps, fetching pages as the loop asks for
// more. An id the caller's context holds no experiment by yields an error
// matching latere.ErrNotFound. params may be nil.
func (c *Client) ListExperimentComparisons(ctx context.Context, id string, params *ListExperimentComparisonsParams) iter.Seq2[*Comparison, error] {
	p, err := experimentPath(id, "/comparisons")
	if err != nil {
		return func(yield func(*Comparison, error) bool) { yield(nil, err) }
	}
	return latere.Items(ctx, pageFunc[Comparison](c, p, params.query))
}

// ListExperimentComparisonsPage returns one page of the comparisons of
// the experiment id that params keeps, starting at params.Cursor, and the
// cursor of the next page in NextCursor.
func (c *Client) ListExperimentComparisonsPage(ctx context.Context, id string, params *ListExperimentComparisonsParams) (*latere.Page[*Comparison], error) {
	p, err := experimentPath(id, "/comparisons")
	if err != nil {
		return nil, err
	}
	cursor := ""
	if params != nil {
		cursor = params.Cursor
	}
	return page[Comparison](ctx, c, p, params.query(cursor))
}
