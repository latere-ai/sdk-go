// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package evals

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
)

// An Interval is a range of a figure, [low, high], at the confidence level
// the analysis computed the comparison with, which a Comparison's
// RawJSON states under its parameters.
type Interval [2]float64

// A Summary is one metric over one set of trials: a cell's, or a
// candidate's tasks at suite level.
type Summary struct {
	// Aggregate is the metric's aggregate; empty for a candidate-level
	// metric.
	Aggregate Aggregate `json:"aggregate,omitempty"`
	// N is the finished trials of the set that the metric applies to,
	// Denominator the ones holding a current value of it less those
	// ExcludedLabels leaves out, and Missing is N less Denominator. Trials
	// outside the metric's applies_to are in NotApplicable, in neither.
	N             int `json:"n"`
	Denominator   int `json:"denominator"`
	Missing       int `json:"missing"`
	NotApplicable int `json:"not_applicable"`
	// Final reports that every declared trial of the set is done and
	// graded; false while the figure can still change.
	Final bool `json:"final"`
	// Value is the rate, mean, median or 90th percentile; for a
	// distribution an object from each label to its share; for a repeat
	// rate, a mode share and a distinct count, the statistic; for a
	// candidate-level metric, its one current value. It is empty when
	// nothing is valued, and left as JSON since its type is the
	// aggregate's: read a number with Number.
	Value jsontext.Value `json:"value,omitzero"`
	// Numerator is, for a rate, the finished trials whose value is true,
	// and for a mode share the trials holding the most frequent label.
	Numerator *int `json:"numerator,omitzero"`
	// Mean, SD, Min and Max are the figures of a numeric metric, nil
	// where the aggregate has none.
	Mean *float64 `json:"mean,omitzero"`
	SD   *float64 `json:"sd,omitzero"`
	Min  *float64 `json:"min,omitzero"`
	Max  *float64 `json:"max,omitzero"`
	// Interval is the value's interval; nil when there is none, and
	// NoInterval says why.
	Interval   *Interval `json:"interval,omitzero"`
	NoInterval string    `json:"no_interval,omitempty"`
	// Distribution is, for a label metric, each label's count and
	// proportion.
	Distribution []LabelShare `json:"distribution,omitempty"`
	// Mode is, for a label metric, its most frequent label.
	Mode string `json:"mode,omitempty"`
	// Coverage is, at suite level, the share of the suite's weight the
	// included tasks hold; Tasks is how many tasks that is.
	Coverage *float64 `json:"coverage,omitzero"`
	Tasks    *int     `json:"tasks,omitzero"`
	// Excluded counts the trials of the set not counted in N, by reason:
	// retried, infra_failed, over_budget, canceled or not_done.
	Excluded map[string]int `json:"excluded,omitempty"`
	// ExcludedLabels counts the trials left out for holding a label the
	// metric excludes, by label.
	ExcludedLabels map[string]int `json:"excluded_labels,omitempty"`
	// Unmapped is, for an open-vocabulary metric, the finished trials
	// whose raw label the taxonomy record read does not map.
	Unmapped *int `json:"unmapped,omitzero"`
}

// Number returns Value as a number, and false when the summary holds none:
// nothing is valued, or the value is a distribution.
func (s *Summary) Number() (float64, bool) {
	var v float64
	if len(s.Value) == 0 || json.Unmarshal(s.Value, &v) != nil {
		return 0, false
	}
	return v, true
}

// A LabelShare is one label's count and proportion in a distribution,
// with the proportion's interval.
type LabelShare struct {
	// Label is the label.
	Label string `json:"label"`
	// Count is how many trials hold it, and Proportion their share.
	Count      int     `json:"count"`
	Proportion float64 `json:"proportion"`
	// Interval is the proportion's interval, nil when there is none.
	Interval *Interval `json:"interval,omitzero"`
	// Unmapped reports a raw label of an open vocabulary that the taxonomy
	// record read does not map.
	Unmapped bool `json:"unmapped,omitzero"`
}

// ResultLevel is what a comparison's result is over.
type ResultLevel string

// The levels of a comparison's results.
const (
	// ResultTask is a result over one task's cells.
	ResultTask ResultLevel = "task"
	// ResultSuite is a result over the paired tasks it includes.
	ResultSuite ResultLevel = "suite"
	// ResultCandidate is a candidate-level metric's one value per side,
	// which is descriptive: it has no verdict.
	ResultCandidate ResultLevel = "candidate"
)

// DeltaKind is the shape of a result's Delta.
type DeltaKind string

// The kinds of delta.
const (
	// DeltaDifference is one number, the candidate's value less the
	// reference's.
	DeltaDifference DeltaKind = "difference"
	// DeltaPerLabel is an object from each label to the difference of its
	// proportions, for a label metric.
	DeltaPerLabel DeltaKind = "per_label"
)

// A Result is one result of a comparison, as the analysis computed it: one
// metric on one task, over the paired tasks at suite level, or of the
// candidates.
type Result struct {
	// Metric is the metric's digest, and MetricName its name.
	Metric     string `json:"metric"`
	MetricName string `json:"metric_name"`
	// Level is what the result is over.
	Level ResultLevel `json:"level"`
	// Task is, on a task-level result, the task's name; for a comparison
	// across tasks, both names joined by " vs "; for a declared
	// comparison of the general form, its stratum's name.
	Task string `json:"task,omitempty"`
	// TaskDigests are the digests of the candidate's and the reference's
	// task, on a task-level result.
	TaskDigests *TaskDigests `json:"task_digests,omitzero"`
	// Direction says which way the metric is better.
	Direction Direction `json:"direction"`
	// Candidate and Reference summarize the metric on each side.
	Candidate Summary `json:"candidate"`
	Reference Summary `json:"reference"`
	// DeltaKind says whether Delta is one number or one per label.
	DeltaKind DeltaKind `json:"delta_kind,omitempty"`
	// Delta is the change, the candidate less the reference: a number, or
	// for a label metric an object from each label to the difference of
	// its proportions. Absent on a descriptive result. Read it with
	// DeltaNumber or DeltaLabels.
	Delta jsontext.Value `json:"delta,omitzero"`
	// DeltaInterval is the delta's interval: one Interval, or for a
	// per-label delta an object from each label to its interval. Absent
	// where none is computed, and NoDeltaInterval says why when the result
	// is not insufficient.
	DeltaInterval   jsontext.Value `json:"delta_interval,omitzero"`
	NoDeltaInterval string         `json:"no_delta_interval,omitempty"`
	// Verdict is what the comparison found; empty on a descriptive
	// result. Reason is one plain sentence saying why.
	Verdict Verdict `json:"verdict,omitempty"`
	Reason  string  `json:"reason"`
	// P is the uncorrected p-value, and Q its value after the correction
	// for the comparison's results. Nil where the test computed none.
	P *float64 `json:"p,omitzero"`
	Q *float64 `json:"q,omitzero"`
	// H is, on a pair result of two benchmark entries, the Holm-adjusted
	// value in place of Q; nil on a comparison's result.
	H *float64 `json:"h,omitzero"`
	// Method is the test, such as "boschloo_exact" or "permutation";
	// empty on a descriptive result. Statistic is what the test orders
	// by, Exact reports that it enumerated every case, Resamples is how
	// many relabelings or tables it enumerated or drew, and Seed is the
	// result's derived seed, in hex.
	Method    string `json:"method,omitempty"`
	Statistic string `json:"statistic,omitempty"`
	Exact     bool   `json:"exact,omitzero"`
	Resamples *int   `json:"resamples,omitzero"`
	Seed      string `json:"seed,omitempty"`
	// Confounds are the result's confounds, besides the variable compared.
	Confounds []Confound `json:"confounds,omitempty"`
	// LeftOut is, on a suite-level result, each task of weight above zero
	// the result does not include.
	LeftOut []LeftOut `json:"left_out,omitempty"`

	raw jsontext.Value
}

// RawJSON returns the JSON the API answered for the result, including any
// field Result does not model.
func (r *Result) RawJSON() jsontext.Value { return r.raw }

// UnmarshalJSON decodes the result and keeps the JSON for RawJSON.
func (r *Result) UnmarshalJSON(b []byte) error {
	type plain Result
	if err := json.Unmarshal(b, (*plain)(r)); err != nil {
		return err
	}
	r.raw = keep(b)
	return nil
}

// DeltaNumber returns the delta as one number, and false when the result
// has none: it is descriptive or its delta is one per label.
func (r *Result) DeltaNumber() (float64, bool) {
	var v float64
	if len(r.Delta) == 0 || json.Unmarshal(r.Delta, &v) != nil {
		return 0, false
	}
	return v, true
}

// DeltaLabels returns the delta of a label metric, from each label to the
// difference of its proportions, and nil when the delta is one number or
// absent. A malformed delta is an error.
func (r *Result) DeltaLabels() (map[string]float64, error) {
	if len(r.Delta) == 0 || r.Delta[0] != '{' {
		return nil, nil
	}
	var out map[string]float64
	if err := json.Unmarshal(r.Delta, &out); err != nil {
		return nil, fmt.Errorf("evals: reading a result's per-label delta: %w", err)
	}
	return out, nil
}

// TaskDigests are the digests of the two tasks a task-level result
// compares.
type TaskDigests struct {
	// Candidate is the candidate side's task, and Reference the
	// reference side's.
	Candidate string `json:"candidate"`
	Reference string `json:"reference"`
}

// A Confound is one confound of a result: a field besides the variable
// compared in which the two sides differ, or a condition of the trials the
// result read.
type Confound struct {
	// Name names the confound.
	Name string `json:"name"`
	// Grader is, for an uncalibrated confound, the judge version whose
	// calibration the result read.
	Grader string `json:"grader,omitempty"`
	// Tasks are, on a suite-level result, the task names the confound
	// applies to.
	Tasks []string `json:"tasks,omitempty"`
	// Candidate and Reference are the figure per side: a count of
	// requests or trials, a window {from, to}, or a taxonomy record id,
	// by the confound's name. Absent for a field of the setup.
	Candidate jsontext.Value `json:"candidate,omitzero"`
	Reference jsontext.Value `json:"reference,omitzero"`
}

// A LeftOut is a task of weight above zero that a suite-level result does
// not include, with each side's valued trials.
type LeftOut struct {
	// Task is the task's name.
	Task string `json:"task"`
	// Why is "unpaired" or "too_few_trials".
	Why string `json:"why"`
	// Candidate and Reference are each side's valued trials.
	Candidate int `json:"candidate"`
	Reference int `json:"reference"`
}
