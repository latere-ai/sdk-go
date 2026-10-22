// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package evals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/url"

	"latere.ai/x/sdk-go/latere"
)

// The collections the five kinds of definition are kept in.
const (
	metricsPath    = basePath + "/metrics"
	gradersPath    = basePath + "/graders"
	tasksPath      = basePath + "/tasks"
	suitesPath     = basePath + "/suites"
	candidatesPath = basePath + "/candidates"
)

// segment checks one path segment a caller gave, a ref, a name or an id,
// and returns it escaped. A segment of "." or ".." would name another
// route, and no ref, name or id is either.
func segment(s string, empty error) (string, error) {
	switch s {
	case "":
		return "", empty
	case ".", "..":
		return "", fmt.Errorf("evals: %q is not a ref, a name or an id", s)
	}
	return url.PathEscape(s), nil
}

// getVersion reads the version ref names in the collection at kind.
func getVersion[T any](ctx context.Context, c *Client, kind, ref string) (*T, error) {
	seg, err := segment(ref, errNoRef)
	if err != nil {
		return nil, err
	}
	return get[T](ctx, c, kind+"/"+seg)
}

// versions is the iterator over the versions of name in the collection at
// kind.
func versions[T any](ctx context.Context, c *Client, kind, name string, p *ListVersionsParams) iter.Seq2[*T, error] {
	seg, err := segment(name, errNoName)
	if err != nil {
		return func(yield func(*T, error) bool) { yield(nil, err) }
	}
	return latere.Items(ctx, pageFunc[T](c, kind+"/"+seg+"/versions", p.query))
}

// versionsPage is one page of the versions of name in the collection at
// kind.
func versionsPage[T any](ctx context.Context, c *Client, kind, name string, p *ListVersionsParams) (*latere.Page[*T], error) {
	seg, err := segment(name, errNoName)
	if err != nil {
		return nil, err
	}
	return page[T](ctx, c, kind+"/"+seg+"/versions", p.query(p.cursor()))
}

// query encodes the parameters of a list of versions.
func (p *ListVersionsParams) query(cursor string) url.Values {
	if p == nil {
		return listQuery(0, cursor)
	}
	return listQuery(p.Limit, cursor)
}

// cursor is the cursor a Page method starts at.
func (p *ListVersionsParams) cursor() string {
	if p == nil {
		return ""
	}
	return p.Cursor
}

// definitionQuery is the query every list of a kind of definition shares:
// a name, every version rather than each name's current one, and a page.
func definitionQuery(name string, all bool, limit int, cursor string) url.Values {
	q := listQuery(limit, cursor)
	setNonEmpty(q, "name", name)
	if all {
		q.Set("versions", "all")
	}
	return q
}

// errMetadata is a metadata filter with a value and no key.
var errMetadata = errors.New("evals: a metadata filter has a value and no key")

// setMetadata sets the one metadata pair a list filters by.
func setMetadata(q url.Values, key, value string) {
	if key != "" {
		q.Set("metadata."+key, value)
	}
}

// ---- metrics ----

// MetricType is what a value of a metric is.
type MetricType string

// The types of a metric.
const (
	// MetricBinary is true or false, summarized as a rate.
	MetricBinary MetricType = "binary"
	// MetricScore is a number within the metric's Range.
	MetricScore MetricType = "score"
	// MetricCount is a whole number of something, such as tool calls.
	MetricCount MetricType = "count"
	// MetricDuration is a length of time, in seconds.
	MetricDuration MetricType = "duration"
	// MetricCost is an amount spent, in US dollars.
	MetricCost MetricType = "cost"
	// MetricLabel is a string: one of the metric's Labels, or under
	// VocabularyOpen any label a judge proposes.
	MetricLabel MetricType = "label"
)

// Direction says which way a metric is better.
type Direction string

// The directions of a metric.
const (
	// DirectionHigherIsBetter is a metric whose higher values are better,
	// such as a pass rate.
	DirectionHigherIsBetter Direction = "higher_is_better"
	// DirectionLowerIsBetter is a metric whose lower values are better,
	// such as a cost.
	DirectionLowerIsBetter Direction = "lower_is_better"
	// DirectionNone is a metric with no better way, such as a label.
	DirectionNone Direction = "none"
)

// Level is what one value of a metric, or one grading of a grader, is of.
type Level string

// The levels of a metric and of a grader.
const (
	// LevelTrial is one value per finished trial, from a grader; an empty
	// Level reads as LevelTrial.
	LevelTrial Level = "trial"
	// LevelCell is one value per cell, which the platform computes from
	// its trials' values of another metric. A grader is never of this
	// level.
	LevelCell Level = "cell"
	// LevelCandidate is one value per candidate of an experiment, from a
	// grader of this level that reads all of the candidate's trials.
	LevelCandidate Level = "candidate"
)

// Vocabulary says where the values of a MetricLabel come from.
type Vocabulary string

// The vocabularies of a label metric.
const (
	// VocabularyClosed, the default, is a metric whose values are among
	// its Labels.
	VocabularyClosed Vocabulary = "closed"
	// VocabularyOpen is a metric whose values are any labels a judge
	// proposes, read as the canonical labels its suite maps them to.
	VocabularyOpen Vocabulary = "open"
)

// An Aggregate is how a cell summarizes the values of its trials for a
// metric.
type Aggregate string

// The aggregates of a metric.
const (
	// AggregateRate is the share of trials whose value is true.
	AggregateRate Aggregate = "rate"
	// AggregateMean is the mean of the trials' values.
	AggregateMean Aggregate = "mean"
	// AggregateMedian is their median.
	AggregateMedian Aggregate = "median"
	// AggregateP90 is their 90th percentile.
	AggregateP90 Aggregate = "p90"
	// AggregateDistribution is the share of trials of each label.
	AggregateDistribution Aggregate = "distribution"
	// AggregateRepeatRate is, for a LevelCell metric, the share of the
	// pairs of trials that hold one label.
	AggregateRepeatRate Aggregate = "repeat_rate"
	// AggregateModeShare is, for a LevelCell metric, the share of trials
	// that hold the most common label.
	AggregateModeShare Aggregate = "mode_share"
	// AggregateDistinctCount is, for a LevelCell metric, how many
	// distinct labels the trials hold.
	AggregateDistinctCount Aggregate = "distinct_count"
)

// A Metric is one version of a metric: a quantity a trial, a cell or a
// candidate is measured on, such as whether a task's check passed or what
// a trial cost.
type Metric struct {
	Version
	// Description says what the metric measures, for people.
	Description string `json:"description,omitempty"`
	// Type is what a value of the metric is.
	Type MetricType `json:"type"`
	// Direction says which way the metric is better.
	Direction Direction `json:"direction"`
	// Level is what one value is of; empty reads as LevelTrial.
	Level Level `json:"level,omitempty"`
	// Range is the lowest and the highest value of a MetricScore.
	Range []float64 `json:"range,omitempty"`
	// Unit is the unit a value is shown in, such as "s" or "USD".
	Unit string `json:"unit,omitempty"`
	// Aggregate is how a cell summarizes its trials' values. Empty for a
	// LevelCandidate metric, whose one value is its summary.
	Aggregate Aggregate `json:"aggregate,omitempty"`
	// AppliesTo are, for a LevelTrial metric, the outcomes of the trials
	// it has a value for; a trial of another outcome is not missing a
	// value. Empty means every outcome the system under test caused:
	// OutcomeCompleted, OutcomeDeclined, OutcomeTimedOut and
	// OutcomeTurnLimit.
	AppliesTo []TrialOutcome `json:"applies_to,omitempty"`
	// Vocabulary says, for a MetricLabel, where its values come from;
	// empty reads as VocabularyClosed.
	Vocabulary Vocabulary `json:"vocabulary,omitempty"`
	// Labels are the values a MetricLabel of VocabularyClosed takes.
	Labels []string `json:"labels,omitempty"`
	// Of is, for a LevelCell metric, the digest of the LevelTrial label
	// metric whose values it summarizes.
	Of string `json:"of,omitempty"`
	// ExcludeLabels are, for a LevelCell metric, the labels of Of whose
	// trials it leaves out. Nil leaves out "declined" and "none"; an empty
	// list leaves out none.
	ExcludeLabels []string `json:"exclude_labels,omitempty"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the metric, including any
// field Metric does not model.
func (m *Metric) RawJSON() json.RawMessage { return m.raw }

// UnmarshalJSON decodes the metric and keeps the JSON for RawJSON.
func (m *Metric) UnmarshalJSON(b []byte) error {
	type plain Metric
	if err := json.Unmarshal(b, (*plain)(m)); err != nil {
		return err
	}
	m.raw = keep(b)
	return nil
}

// ListMetricsParams filter and page the metrics of the caller's context.
// The zero value lists the current version of each metric, newest first,
// in pages of DefaultLimit.
type ListMetricsParams struct {
	// Name keeps the metric of this name.
	Name string
	// Type keeps the metrics of this type.
	Type MetricType
	// AllVersions lists every version rather than each name's current one.
	AllVersions bool
	// Limit is the page size, at most MaxLimit;
	// DefaultLimit when zero.
	Limit int
	// Cursor asks ListMetricsPage for the page after the one that returned
	// it. ListMetrics ignores it.
	Cursor string
}

func (p *ListMetricsParams) query(cursor string) url.Values {
	if p == nil {
		return definitionQuery("", false, 0, cursor)
	}
	q := definitionQuery(p.Name, p.AllVersions, p.Limit, cursor)
	setNonEmpty(q, "type", string(p.Type))
	return q
}

// ListMetrics returns an iterator over the metrics params keeps, newest
// first, fetching pages as the loop asks for more. params may be nil.
func (c *Client) ListMetrics(ctx context.Context, params *ListMetricsParams) iter.Seq2[*Metric, error] {
	return latere.Items(ctx, pageFunc[Metric](c, metricsPath, params.query))
}

// ListMetricsPage returns one page of the metrics params keeps, starting
// at params.Cursor, and the cursor of the next page in NextCursor.
func (c *Client) ListMetricsPage(ctx context.Context, params *ListMetricsParams) (*latere.Page[*Metric], error) {
	cursor := ""
	if params != nil {
		cursor = params.Cursor
	}
	return page[Metric](ctx, c, metricsPath, params.query(cursor))
}

// GetMetric returns the version of a metric ref names: a name, for its
// current version, name@digest, a digest or an id. A ref the caller's
// context holds no metric by returns an error matching latere.ErrNotFound.
func (c *Client) GetMetric(ctx context.Context, ref string) (*Metric, error) {
	return getVersion[Metric](ctx, c, metricsPath, ref)
}

// ListMetricVersions returns an iterator over every version of the metric
// name, newest first, the current one marked by Current. A name the
// caller's context holds no metric by yields an error matching
// latere.ErrNotFound. params may be nil.
func (c *Client) ListMetricVersions(ctx context.Context, name string, params *ListVersionsParams) iter.Seq2[*Metric, error] {
	return versions[Metric](ctx, c, metricsPath, name, params)
}

// ListMetricVersionsPage returns one page of the versions of the metric
// name, starting at params.Cursor, and the cursor of the next page in
// NextCursor.
func (c *Client) ListMetricVersionsPage(ctx context.Context, name string, params *ListVersionsParams) (*latere.Page[*Metric], error) {
	return versionsPage[Metric](ctx, c, metricsPath, name, params)
}

// ---- graders ----

// GraderKind is how a grader fills its metrics.
type GraderKind string

// The kinds of grader.
const (
	// GraderCheck runs a command after the system under test ends, whose
	// output records the values; its fields are a Grader's Check.
	GraderCheck GraderKind = "check"
	// GraderModelJudge asks a model to grade what the trial produced; its
	// fields are a Grader's ModelJudge.
	GraderModelJudge GraderKind = "model_judge"
	// GraderAgentJudge has an agent grade the trial; its fields are a
	// Grader's AgentJudge.
	GraderAgentJudge GraderKind = "agent_judge"
	// GraderAnnotation queues a sample of the trials for a person to
	// grade; its fields are a Grader's Annotation.
	GraderAnnotation GraderKind = "annotation"
)

// JudgeRole is what a model judge does.
type JudgeRole string

// The roles of a model judge.
const (
	// JudgeGrade grades trials, the role of a judge that names none.
	JudgeGrade JudgeRole = "grade"
	// JudgeCanonicalize maps the labels judges proposed for a metric of
	// VocabularyOpen to the suite's canonical labels.
	JudgeCanonicalize JudgeRole = "canonicalize"
)

// A Grader is one version of a grader: what fills a trial's or a
// candidate's values of some metrics. Of Check, ModelJudge, AgentJudge and
// Annotation, the one its Kind names is set.
type Grader struct {
	Version
	// Kind is how the grader fills its metrics.
	Kind GraderKind `json:"kind"`
	// Level is LevelTrial, grading one trial at a time, or for a
	// GraderModelJudge LevelCandidate, grading each candidate once over all
	// its trials; empty reads as LevelTrial.
	Level Level `json:"level,omitempty"`
	// Produces are the digests of the metrics the grader fills.
	Produces []string `json:"produces,omitempty"`
	// Check is a GraderCheck's command.
	Check *Check `json:"check,omitempty"`
	// ModelJudge is a GraderModelJudge's model and prompt.
	ModelJudge *ModelJudge `json:"model_judge,omitempty"`
	// AgentJudge is a GraderAgentJudge's agent and instructions.
	AgentJudge *AgentJudge `json:"agent_judge,omitempty"`
	// Annotation is a GraderAnnotation's instructions and sample.
	Annotation *Annotation `json:"annotation,omitempty"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the grader, including any
// field Grader does not model.
func (g *Grader) RawJSON() json.RawMessage { return g.raw }

// UnmarshalJSON decodes the grader and keeps the JSON for RawJSON.
func (g *Grader) UnmarshalJSON(b []byte) error {
	type plain Grader
	if err := json.Unmarshal(b, (*plain)(g)); err != nil {
		return err
	}
	g.raw = keep(b)
	return nil
}

// A Check is the command a GraderCheck runs once the system under test
// ended, in the trial's machine or, when Image is set, in a sandbox of its
// own.
type Check struct {
	// Command is the program and its arguments.
	Command []string `json:"command"`
	// Workdir is the directory the command runs in.
	Workdir string `json:"workdir,omitempty"`
	// Env are the variables the command runs with.
	Env map[string]string `json:"env,omitempty"`
	// Image is an image pinned by digest, mounted read-only while the
	// check runs, from which Command's program runs.
	Image string `json:"image,omitempty"`
	// Files is a file of the owner's Storage delivered read-only to the
	// check, by its path and the digest of its content.
	Files *StorageFile `json:"files,omitempty"`
}

// A ModelJudge is the model a GraderModelJudge asks, and what it asks.
type ModelJudge struct {
	// Model is the model, by its pinned id.
	Model string `json:"model"`
	// Reasoning is the reasoning level the model runs at.
	Reasoning string `json:"reasoning,omitempty"`
	// Temperature is the sampling temperature; nil for the model's own.
	Temperature *float64 `json:"temperature,omitempty"`
	// Role is what the judge does; empty reads as JudgeGrade.
	Role JudgeRole `json:"role,omitempty"`
	// Preamble is the platform's judge preamble, pinned on the grader when
	// it was applied and covered by its digest.
	Preamble string `json:"preamble,omitempty"`
	// Prompt is what the judge is asked.
	Prompt string `json:"prompt,omitempty"`
	// Rubric is what the judge grades against.
	Rubric string `json:"rubric,omitempty"`
	// Inputs are what the judge reads of a trial.
	Inputs *JudgeInputs `json:"inputs,omitempty"`
}

// An AgentJudge is the agent a GraderAgentJudge runs, and what it tells it.
type AgentJudge struct {
	// Agent is the agent version that judges, by digest.
	Agent string `json:"agent"`
	// Instructions are what the agent is told to grade.
	Instructions string `json:"instructions"`
	// Inputs are what the agent reads of a trial.
	Inputs *JudgeInputs `json:"inputs,omitempty"`
}

// An Annotation is the queue of a GraderAnnotation: what a person grading
// is told, and how many trials they are asked to grade.
type Annotation struct {
	// Instructions are what the person is told.
	Instructions string `json:"instructions"`
	// Sample is the fraction of the trials queued, from 0 to 1.
	Sample float64 `json:"sample"`
}

// JudgeInputs are what a judge reads of a trial.
type JudgeInputs struct {
	// Transcript reads the trial's whole transcript.
	Transcript bool `json:"transcript,omitempty"`
	// FinalMessage reads the last message of the system under test.
	FinalMessage bool `json:"final_message,omitempty"`
	// Checks reads, once every check of the trial ended, the values the
	// checks recorded, by metric name.
	Checks bool `json:"checks,omitempty"`
	// Files are files of the trial's workspace the judge reads.
	Files []string `json:"files,omitempty"`
	// Outputs are outputs of the trial the judge reads: an output by its
	// name, a file of a directory output as "<output>/<path>", or a whole
	// directory as "<output>/".
	Outputs []string `json:"outputs,omitempty"`
}

// ListGradersParams filter and page the graders of the caller's context.
// The zero value lists the current version of each grader, newest first,
// in pages of DefaultLimit.
type ListGradersParams struct {
	// Name keeps the grader of this name.
	Name string
	// Kind keeps the graders of this kind.
	Kind GraderKind
	// Produces keeps the graders that fill this metric, by digest.
	Produces string
	// AllVersions lists every version rather than each name's current one.
	AllVersions bool
	// Limit is the page size, at most MaxLimit;
	// DefaultLimit when zero.
	Limit int
	// Cursor asks ListGradersPage for the page after the one that returned
	// it. ListGraders ignores it.
	Cursor string
}

func (p *ListGradersParams) query(cursor string) url.Values {
	if p == nil {
		return definitionQuery("", false, 0, cursor)
	}
	q := definitionQuery(p.Name, p.AllVersions, p.Limit, cursor)
	setNonEmpty(q, "grader_kind", string(p.Kind))
	setNonEmpty(q, "produces", p.Produces)
	return q
}

// ListGraders returns an iterator over the graders params keeps, newest
// first, fetching pages as the loop asks for more. params may be nil.
func (c *Client) ListGraders(ctx context.Context, params *ListGradersParams) iter.Seq2[*Grader, error] {
	return latere.Items(ctx, pageFunc[Grader](c, gradersPath, params.query))
}

// ListGradersPage returns one page of the graders params keeps, starting
// at params.Cursor, and the cursor of the next page in NextCursor.
func (c *Client) ListGradersPage(ctx context.Context, params *ListGradersParams) (*latere.Page[*Grader], error) {
	cursor := ""
	if params != nil {
		cursor = params.Cursor
	}
	return page[Grader](ctx, c, gradersPath, params.query(cursor))
}

// GetGrader returns the version of a grader ref names: a name, for its
// current version, name@digest, a digest or an id. A ref the caller's
// context holds no grader by returns an error matching latere.ErrNotFound.
func (c *Client) GetGrader(ctx context.Context, ref string) (*Grader, error) {
	return getVersion[Grader](ctx, c, gradersPath, ref)
}

// ListGraderVersions returns an iterator over every version of the grader
// name, newest first, the current one marked by Current. A name the
// caller's context holds no grader by yields an error matching
// latere.ErrNotFound. params may be nil.
func (c *Client) ListGraderVersions(ctx context.Context, name string, params *ListVersionsParams) iter.Seq2[*Grader, error] {
	return versions[Grader](ctx, c, gradersPath, name, params)
}

// ListGraderVersionsPage returns one page of the versions of the grader
// name, starting at params.Cursor, and the cursor of the next page in
// NextCursor.
func (c *Client) ListGraderVersionsPage(ctx context.Context, name string, params *ListVersionsParams) (*latere.Page[*Grader], error) {
	return versionsPage[Grader](ctx, c, gradersPath, name, params)
}

// ---- tasks ----

// WorkspaceKind is where a task's trial starts.
type WorkspaceKind string

// The kinds of a task's workspace.
const (
	// WorkspaceNone starts the trial in an empty directory.
	WorkspaceNone WorkspaceKind = "none"
	// WorkspaceRepository starts it from a repository of Repos at a
	// commit, the API's "origo".
	WorkspaceRepository WorkspaceKind = "origo"
	// WorkspaceStorage starts it from a path of the owner's Storage with
	// the digest of its content, the API's "arca".
	WorkspaceStorage WorkspaceKind = "arca"
)

// A Task is one version of a task: one piece of work for the system under
// test, with the files it is given, where it starts, and the checks it is
// graded by.
type Task struct {
	Version
	// Instruction is the first message the system under test receives.
	Instruction string `json:"instruction,omitempty"`
	// Inputs are the named files the system under test is given.
	Inputs []FileSource `json:"inputs,omitempty"`
	// Reference are the named expected outputs, given to the task's checks
	// alone, never to the system under test.
	Reference []FileSource `json:"reference,omitempty"`
	// Outputs are what the system under test writes for the checks to
	// read and the trial to keep: a file, or with a trailing "/" a
	// directory, each relative to the outputs directory.
	Outputs []string `json:"outputs,omitempty"`
	// Workspace is where the trial starts.
	Workspace *Workspace `json:"workspace,omitempty"`
	// Environment is the sandbox the trial runs in.
	Environment *Environment `json:"environment,omitempty"`
	// Checks are the graders of kind GraderCheck run in order after the
	// system under test ends, by digest.
	Checks []string `json:"checks,omitempty"`
	// Graders are judges and annotation queues of LevelTrial applied to
	// this task's trials alone, beside the suite's graders, by digest.
	Graders []string `json:"graders,omitempty"`
	// Timeout is the trial's wall time, a duration such as "30m" as Go's
	// time.ParseDuration reads it; empty for the platform's default.
	Timeout string `json:"timeout,omitempty"`
	// MaxTurns bounds the model responses of the system under test's own
	// thread, which some kinds of candidate take as advice alone; zero for
	// no bound.
	MaxTurns int `json:"max_turns,omitempty"`
	// Evidence are files of the workspace kept as evidence besides the
	// transcript.
	Evidence []string `json:"evidence,omitempty"`
	// Factors place the task in a design, each factor's name to the task's
	// level of it, such as {"object": "build"}; comparisons and a test of
	// independence select and stratify cells by them.
	Factors map[string]string `json:"factors,omitempty"`
	// Metadata are the task's labels.
	Metadata map[string]string `json:"metadata,omitempty"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the task, including any
// field Task does not model.
func (t *Task) RawJSON() json.RawMessage { return t.raw }

// UnmarshalJSON decodes the task and keeps the JSON for RawJSON.
func (t *Task) UnmarshalJSON(b []byte) error {
	type plain Task
	if err := json.Unmarshal(b, (*plain)(t)); err != nil {
		return err
	}
	t.raw = keep(b)
	return nil
}

// A FileSource is a named file of a task: inline text, a file of Storage,
// or a file of a repository at a commit. Of Text, Storage and Repository,
// one is set.
type FileSource struct {
	// Name is the file's name where it is delivered.
	Name string `json:"name"`
	// Text is the file's content, given inline.
	Text string `json:"text,omitempty"`
	// Storage is a file of the owner's Storage, the API's "arca".
	Storage *StorageFile `json:"arca,omitempty"`
	// Repository is a file of a repository of Repos, the API's "origo".
	Repository *RepositoryFile `json:"origo,omitempty"`
}

// A StorageFile is a file of Storage pinned by the digest of its content.
type StorageFile struct {
	// Path is the file's path in its owner's space.
	Path string `json:"path"`
	// Digest is the digest of the content the definition was applied with.
	Digest string `json:"digest"`
}

// A RepositoryFile is a file of a repository of Repos at a commit.
type RepositoryFile struct {
	// Repository names the repository.
	Repository string `json:"repository"`
	// Commit is the commit, by its full id.
	Commit string `json:"commit"`
	// Path is the file's path in the repository.
	Path string `json:"path"`
}

// A Workspace is where a task's trial starts.
type Workspace struct {
	// Kind is where it starts from.
	Kind WorkspaceKind `json:"kind"`
	// Repository and Commit name the repository and the full commit id of
	// a WorkspaceRepository.
	Repository string `json:"repository,omitempty"`
	Commit     string `json:"commit,omitempty"`
	// Path is the path in the repository, or in the owner's Storage for a
	// WorkspaceStorage.
	Path string `json:"path,omitempty"`
	// Digest is the digest of a WorkspaceStorage's content.
	Digest string `json:"digest,omitempty"`
}

// An Environment is the sandbox a task's trial runs in.
type Environment struct {
	// Image is the sandbox's image, pinned by digest.
	Image string `json:"image"`
	// Resources are the sandbox's resources, in the shape Environments
	// takes them.
	Resources json.RawMessage `json:"resources,omitempty"`
	// Network is the sandbox's network policy, in the shape Environments
	// takes it.
	Network json.RawMessage `json:"network,omitempty"`
}

// ListTasksParams filter and page the tasks of the caller's context. The
// zero value lists the current version of each task, newest first, in
// pages of DefaultLimit.
type ListTasksParams struct {
	// Name keeps the task of this name.
	Name string
	// Suite keeps the tasks a version of a suite pins, by its digest or
	// its name@digest; a name alone, which names no one version, is
	// refused with 400.
	Suite string
	// MetadataKey and MetadataValue keep the tasks whose metadata holds
	// the key with exactly the value. The API takes one such pair.
	MetadataKey, MetadataValue string
	// AllVersions lists every version rather than each name's current one.
	AllVersions bool
	// Limit is the page size, at most MaxLimit;
	// DefaultLimit when zero.
	Limit int
	// Cursor asks ListTasksPage for the page after the one that returned
	// it. ListTasks ignores it.
	Cursor string
}

func (p *ListTasksParams) query(cursor string) url.Values {
	if p == nil {
		return definitionQuery("", false, 0, cursor)
	}
	q := definitionQuery(p.Name, p.AllVersions, p.Limit, cursor)
	setNonEmpty(q, "suite", p.Suite)
	setMetadata(q, p.MetadataKey, p.MetadataValue)
	return q
}

func (p *ListTasksParams) check() error {
	if p != nil && p.MetadataKey == "" && p.MetadataValue != "" {
		return errMetadata
	}
	return nil
}

// ListTasks returns an iterator over the tasks params keeps, newest first,
// fetching pages as the loop asks for more. params may be nil.
func (c *Client) ListTasks(ctx context.Context, params *ListTasksParams) iter.Seq2[*Task, error] {
	if err := params.check(); err != nil {
		return func(yield func(*Task, error) bool) { yield(nil, err) }
	}
	return latere.Items(ctx, pageFunc[Task](c, tasksPath, params.query))
}

// ListTasksPage returns one page of the tasks params keeps, starting at
// params.Cursor, and the cursor of the next page in NextCursor.
func (c *Client) ListTasksPage(ctx context.Context, params *ListTasksParams) (*latere.Page[*Task], error) {
	if err := params.check(); err != nil {
		return nil, err
	}
	cursor := ""
	if params != nil {
		cursor = params.Cursor
	}
	return page[Task](ctx, c, tasksPath, params.query(cursor))
}

// GetTask returns the version of a task ref names: a name, for its current
// version, name@digest, a digest or an id. A ref the caller's context
// holds no task by returns an error matching latere.ErrNotFound.
func (c *Client) GetTask(ctx context.Context, ref string) (*Task, error) {
	return getVersion[Task](ctx, c, tasksPath, ref)
}

// ListTaskVersions returns an iterator over every version of the task
// name, newest first, the current one marked by Current. A name the
// caller's context holds no task by yields an error matching
// latere.ErrNotFound. params may be nil.
func (c *Client) ListTaskVersions(ctx context.Context, name string, params *ListVersionsParams) iter.Seq2[*Task, error] {
	return versions[Task](ctx, c, tasksPath, name, params)
}

// ListTaskVersionsPage returns one page of the versions of the task name,
// starting at params.Cursor, and the cursor of the next page in
// NextCursor.
func (c *Client) ListTaskVersionsPage(ctx context.Context, name string, params *ListVersionsParams) (*latere.Page[*Task], error) {
	return versionsPage[Task](ctx, c, tasksPath, name, params)
}

// ---- suites ----

// A Suite is one version of a suite: a set of tasks, the metrics they are
// measured by, and the one a report leads with. Each definition it names
// is named by its digest; a built-in metric's digest is "builtin:" and its
// name, such as "builtin:cost_usd".
type Suite struct {
	Version
	// Tasks are the suite's tasks.
	Tasks []string `json:"tasks"`
	// Metrics are the metrics the tasks are measured by.
	Metrics []string `json:"metrics"`
	// PrimaryMetric is the metric a report leads with, of LevelTrial or
	// LevelCell; its suite-level result is the experiment's one test
	// declared in advance.
	PrimaryMetric string `json:"primary_metric"`
	// Graders are judges and annotation queues applied to every trial,
	// beside each task's checks, and judges of LevelCandidate applied to
	// each candidate.
	Graders []string `json:"graders,omitempty"`
	// Canonicalizers are model judges of JudgeCanonicalize, at most one
	// for each metric of VocabularyOpen.
	Canonicalizers []string `json:"canonicalizers,omitempty"`
	// DeclineJudge is the model judge that decides whether the system
	// under test declined a task; empty for the platform's own.
	DeclineJudge string `json:"decline_judge,omitempty"`
	// Weights are the tasks' weights in the suite's results, by digest; a
	// task not listed weighs 1.
	Weights map[string]float64 `json:"weights,omitempty"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the suite, including any
// field Suite does not model.
func (s *Suite) RawJSON() json.RawMessage { return s.raw }

// UnmarshalJSON decodes the suite and keeps the JSON for RawJSON.
func (s *Suite) UnmarshalJSON(b []byte) error {
	type plain Suite
	if err := json.Unmarshal(b, (*plain)(s)); err != nil {
		return err
	}
	s.raw = keep(b)
	return nil
}

// ListSuitesParams filter and page the suites of the caller's context. The
// zero value lists the current version of each suite, newest first, in
// pages of DefaultLimit.
type ListSuitesParams struct {
	// Name keeps the suite of this name.
	Name string
	// AllVersions lists every version rather than each name's current one.
	AllVersions bool
	// Limit is the page size, at most MaxLimit;
	// DefaultLimit when zero.
	Limit int
	// Cursor asks ListSuitesPage for the page after the one that returned
	// it. ListSuites ignores it.
	Cursor string
}

func (p *ListSuitesParams) query(cursor string) url.Values {
	if p == nil {
		return definitionQuery("", false, 0, cursor)
	}
	return definitionQuery(p.Name, p.AllVersions, p.Limit, cursor)
}

// ListSuites returns an iterator over the suites params keeps, newest
// first, fetching pages as the loop asks for more. params may be nil.
func (c *Client) ListSuites(ctx context.Context, params *ListSuitesParams) iter.Seq2[*Suite, error] {
	return latere.Items(ctx, pageFunc[Suite](c, suitesPath, params.query))
}

// ListSuitesPage returns one page of the suites params keeps, starting at
// params.Cursor, and the cursor of the next page in NextCursor.
func (c *Client) ListSuitesPage(ctx context.Context, params *ListSuitesParams) (*latere.Page[*Suite], error) {
	cursor := ""
	if params != nil {
		cursor = params.Cursor
	}
	return page[Suite](ctx, c, suitesPath, params.query(cursor))
}

// GetSuite returns the version of a suite ref names: a name, for its
// current version, name@digest, a digest or an id. A ref the caller's
// context holds no suite by returns an error matching latere.ErrNotFound.
func (c *Client) GetSuite(ctx context.Context, ref string) (*Suite, error) {
	return getVersion[Suite](ctx, c, suitesPath, ref)
}

// ListSuiteVersions returns an iterator over every version of the suite
// name, newest first, the current one marked by Current. A name the
// caller's context holds no suite by yields an error matching
// latere.ErrNotFound. params may be nil.
func (c *Client) ListSuiteVersions(ctx context.Context, name string, params *ListVersionsParams) iter.Seq2[*Suite, error] {
	return versions[Suite](ctx, c, suitesPath, name, params)
}

// ListSuiteVersionsPage returns one page of the versions of the suite
// name, starting at params.Cursor, and the cursor of the next page in
// NextCursor.
func (c *Client) ListSuiteVersionsPage(ctx context.Context, name string, params *ListVersionsParams) (*latere.Page[*Suite], error) {
	return versionsPage[Suite](ctx, c, suitesPath, name, params)
}

// ---- candidates ----

// CandidateKind is what a candidate runs.
type CandidateKind string

// The kinds of candidate.
const (
	// CandidateAgent runs an agent of the platform's Agents, the API's
	// "topos"; its fields are a Candidate's Agent.
	CandidateAgent CandidateKind = "topos"
	// CandidateProgram runs a program in an image; its fields are a
	// Candidate's Program.
	CandidateProgram CandidateKind = "program"
	// CandidateExternal is a harness that runs elsewhere and reports its
	// trials; its fields are a Candidate's External.
	CandidateExternal CandidateKind = "external"
)

// A Candidate is one version of a candidate: the system under test. Of
// Agent, Program and External, the one its Kind names is set.
type Candidate struct {
	Version
	// Kind is what the candidate runs.
	Kind CandidateKind `json:"kind"`
	// Agent is a CandidateAgent's agent and model.
	Agent *AgentCandidate `json:"topos,omitempty"`
	// Program is a CandidateProgram's image and command.
	Program *ProgramCandidate `json:"program,omitempty"`
	// External is a CandidateExternal's harness.
	External *ExternalCandidate `json:"external,omitempty"`
	// Metadata are the candidate's labels.
	Metadata map[string]string `json:"metadata,omitempty"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the candidate, including
// any field Candidate does not model.
func (c *Candidate) RawJSON() json.RawMessage { return c.raw }

// UnmarshalJSON decodes the candidate and keeps the JSON for RawJSON.
func (c *Candidate) UnmarshalJSON(b []byte) error {
	type plain Candidate
	if err := json.Unmarshal(b, (*plain)(c)); err != nil {
		return err
	}
	c.raw = keep(b)
	return nil
}

// An AgentCandidate is the agent a CandidateAgent runs, and the model it
// runs on.
type AgentCandidate struct {
	// AgentVersion is the agent version that runs, by digest.
	AgentVersion string `json:"agent"`
	// Model is the model it runs on, by its pinned id, in place of the
	// agent's own.
	Model string `json:"model"`
	// Reasoning is the reasoning level it runs at, in place of the
	// agent's own.
	Reasoning string `json:"reasoning,omitempty"`
	// Tools narrow the agent's tools to these; empty keeps them all.
	Tools []string `json:"tools,omitempty"`
}

// A ProgramCandidate is the program a CandidateProgram runs.
type ProgramCandidate struct {
	// Image is the program's image, pinned by digest.
	Image string `json:"image"`
	// Command is the program and its arguments; empty runs the image's
	// own.
	Command []string `json:"command,omitempty"`
	// Env are the variables the program runs with, none of them a secret.
	Env map[string]string `json:"env,omitempty"`
	// Config is an object the program defines and reads.
	Config json.RawMessage `json:"config,omitempty"`
	// Model and Reasoning are the model, by its pinned id, and the
	// reasoning level the program is told to use.
	Model     string `json:"model,omitempty"`
	Reasoning string `json:"reasoning,omitempty"`
	// Models says where the program finds its way to Models: the
	// variables it reads the address and the key from, and the API it
	// speaks.
	Models *ModelAccess `json:"lux,omitempty"`
	// Credentials are the tokens the program is given for other parts of
	// the API, each scoped to one host.
	Credentials []ProgramCredential `json:"credentials,omitempty"`
}

// ModelAccess is how a program reaches Models.
type ModelAccess struct {
	// Surface is the API the program speaks, such as
	// "anthropic_messages", "openai_responses",
	// "openai_chat_completions", "gemini" or "lux", the default.
	Surface string `json:"surface,omitempty"`
	// URLEnv is the variable the address standing in for the surface's
	// base URL is set in; "LUX_URL" when empty.
	URLEnv string `json:"url_env,omitempty"`
	// KeyEnv is the variable the key is set in; "LUX_KEY" when empty.
	KeyEnv string `json:"key_env,omitempty"`
}

// A ProgramCredential is a token a program is given for an audience of
// the API, sent only to Host.
type ProgramCredential struct {
	// Audience is the audience the token is minted for.
	Audience string `json:"audience"`
	// Host is the host name the token is sent to.
	Host string `json:"host"`
	// Env is the variable the token's placeholder is set in, or File the
	// file it is written to.
	Env  string `json:"env,omitempty"`
	File string `json:"file,omitempty"`
}

// An ExternalCandidate is the harness a CandidateExternal names, as the
// harness declares itself.
type ExternalCandidate struct {
	// Harness is the harness's name.
	Harness string `json:"harness"`
	// Version is the version the harness declares for itself.
	Version string `json:"version"`
	// Digest is a digest the harness declares for its build.
	Digest string `json:"digest,omitempty"`
	// Model and Reasoning are the model, by its pinned id, and the
	// reasoning level the harness runs its system with.
	Model     string `json:"model,omitempty"`
	Reasoning string `json:"reasoning,omitempty"`
	// Flags are the harness's own settings.
	Flags map[string]string `json:"flags,omitempty"`
}

// ListCandidatesParams filter and page the candidates of the caller's
// context. The zero value lists the current version of each candidate,
// newest first, in pages of DefaultLimit.
type ListCandidatesParams struct {
	// Name keeps the candidate of this name.
	Name string
	// Kind keeps the candidates of this kind.
	Kind CandidateKind
	// AgentVersion keeps the candidates that run this agent version, by
	// digest.
	AgentVersion string
	// Model keeps the candidates that run on this model.
	Model string
	// MetadataKey and MetadataValue keep the candidates whose metadata
	// holds the key with exactly the value. The API takes one such pair.
	MetadataKey, MetadataValue string
	// AllVersions lists every version rather than each name's current one.
	AllVersions bool
	// Limit is the page size, at most MaxLimit;
	// DefaultLimit when zero.
	Limit int
	// Cursor asks ListCandidatesPage for the page after the one that
	// returned it. ListCandidates ignores it.
	Cursor string
}

func (p *ListCandidatesParams) query(cursor string) url.Values {
	if p == nil {
		return definitionQuery("", false, 0, cursor)
	}
	q := definitionQuery(p.Name, p.AllVersions, p.Limit, cursor)
	setNonEmpty(q, "candidate_kind", string(p.Kind))
	setNonEmpty(q, "agent", p.AgentVersion)
	setNonEmpty(q, "model", p.Model)
	setMetadata(q, p.MetadataKey, p.MetadataValue)
	return q
}

func (p *ListCandidatesParams) check() error {
	if p != nil && p.MetadataKey == "" && p.MetadataValue != "" {
		return errMetadata
	}
	return nil
}

// ListCandidates returns an iterator over the candidates params keeps,
// newest first, fetching pages as the loop asks for more. params may be
// nil.
func (c *Client) ListCandidates(ctx context.Context, params *ListCandidatesParams) iter.Seq2[*Candidate, error] {
	if err := params.check(); err != nil {
		return func(yield func(*Candidate, error) bool) { yield(nil, err) }
	}
	return latere.Items(ctx, pageFunc[Candidate](c, candidatesPath, params.query))
}

// ListCandidatesPage returns one page of the candidates params keeps,
// starting at params.Cursor, and the cursor of the next page in
// NextCursor.
func (c *Client) ListCandidatesPage(ctx context.Context, params *ListCandidatesParams) (*latere.Page[*Candidate], error) {
	if err := params.check(); err != nil {
		return nil, err
	}
	cursor := ""
	if params != nil {
		cursor = params.Cursor
	}
	return page[Candidate](ctx, c, candidatesPath, params.query(cursor))
}

// GetCandidate returns the version of a candidate ref names: a name, for
// its current version, name@digest, a digest or an id. A ref the caller's
// context holds no candidate by returns an error matching
// latere.ErrNotFound.
func (c *Client) GetCandidate(ctx context.Context, ref string) (*Candidate, error) {
	return getVersion[Candidate](ctx, c, candidatesPath, ref)
}

// ListCandidateVersions returns an iterator over every version of the
// candidate name, newest first, the current one marked by Current. A name
// the caller's context holds no candidate by yields an error matching
// latere.ErrNotFound. params may be nil.
func (c *Client) ListCandidateVersions(ctx context.Context, name string, params *ListVersionsParams) iter.Seq2[*Candidate, error] {
	return versions[Candidate](ctx, c, candidatesPath, name, params)
}

// ListCandidateVersionsPage returns one page of the versions of the
// candidate name, starting at params.Cursor, and the cursor of the next
// page in NextCursor.
func (c *Client) ListCandidateVersionsPage(ctx context.Context, name string, params *ListVersionsParams) (*latere.Page[*Candidate], error) {
	return versionsPage[Candidate](ctx, c, candidatesPath, name, params)
}
