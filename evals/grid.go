// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package evals

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"net/http"
	"net/url"

	"latere.ai/x/sdk-go/latere"
)

// A Grid is an experiment's candidate by task grid: one row per task of
// the suite, in the suite's order, and in each row one entry per candidate
// in the experiment's order, paged by rows.
type Grid struct {
	// Experiment is the experiment's id, and State its state.
	Experiment string          `json:"experiment"`
	State      ExperimentState `json:"state"`
	// Metric is the metric the entries' summaries read.
	Metric GridMetric `json:"metric"`
	// Reference is what the entries' verdicts compare with; nil when the
	// experiment names none.
	Reference *Reference `json:"reference,omitzero"`
	// Candidates are the columns, in the experiment's order.
	Candidates []GridCandidate `json:"candidates"`
	// Items are the page's rows, in the suite's order.
	Items []GridRow `json:"items"`
	// NextCursor asks GetGrid for the page of rows after this one through
	// GridParams.Cursor; empty on the last page.
	NextCursor string `json:"next_cursor,omitempty"`

	raw jsontext.Value
}

// RawJSON returns the JSON the API answered for the grid, including any
// field Grid does not model.
func (g *Grid) RawJSON() jsontext.Value { return g.raw }

// UnmarshalJSON decodes the grid and keeps the JSON for RawJSON.
func (g *Grid) UnmarshalJSON(b []byte) error {
	type plain Grid
	if err := json.Unmarshal(b, (*plain)(g)); err != nil {
		return err
	}
	g.raw = keep(b)
	return nil
}

// GridMetric is the metric a grid's summaries read.
type GridMetric struct {
	// Name and Digest name the metric.
	Name   string `json:"name,omitempty"`
	Digest string `json:"digest,omitempty"`
	// Type, Direction and Aggregate are the metric's.
	Type      MetricType `json:"type,omitempty"`
	Direction Direction  `json:"direction,omitempty"`
	Aggregate Aggregate  `json:"aggregate,omitempty"`
}

// GridCandidate is one column of a grid.
type GridCandidate struct {
	// Name and Digest name the candidate.
	Name   string `json:"name,omitempty"`
	Digest string `json:"digest,omitempty"`
}

// GridRow is one task of a grid with its cells.
type GridRow struct {
	// Task is the row's task.
	Task GridTask `json:"task"`
	// ReferenceSummary is the reference's summary of the metric on the
	// task; nil when there is none. The document leaves its shape open;
	// the analysis writes it as a Summary, and RawJSON holds anything
	// more.
	ReferenceSummary *Summary `json:"reference_summary,omitzero"`
	// Cells are the row's entries, one per candidate column kept, in the
	// columns' order.
	Cells []GridCell `json:"cells"`
}

// GridTask names a row's task.
type GridTask struct {
	// Name and Digest name the task.
	Name   string `json:"name,omitempty"`
	Digest string `json:"digest,omitempty"`
}

// GridCell is one candidate's entry on one task.
type GridCell struct {
	// Candidate is the candidate's digest, and Cell the cell's id.
	Candidate string `json:"candidate,omitempty"`
	Cell      string `json:"cell,omitempty"`
	// State is the cell's state, and ParkedReason says why a parked cell
	// is parked.
	State        CellState `json:"state,omitempty"`
	ParkedReason string    `json:"parked_reason,omitempty"`
	// Trials counts the cell's trials.
	Trials *GridTrials `json:"trials,omitzero"`
	// Comparison is the id of the comparison the entry's summary and
	// verdict read.
	Comparison string `json:"comparison,omitempty"`
	// Summary is the cell's summary of the metric; nil until the
	// analysis computed it.
	Summary *Summary `json:"summary,omitzero"`
	// Verdict and Reason are the analysis's; empty until it computed
	// them.
	Verdict Verdict `json:"verdict,omitempty"`
	Reason  string  `json:"reason,omitempty"`
}

// GridTrials counts a grid cell's trials.
type GridTrials struct {
	// Declared is the runs the cell declares.
	Declared int `json:"declared,omitzero"`
	// Pending, Running, Checking and Done count the cell's trials by
	// state.
	Pending  int `json:"pending,omitzero"`
	Running  int `json:"running,omitzero"`
	Checking int `json:"checking,omitzero"`
	Done     int `json:"done,omitzero"`
	// Retried counts the trials a retry replaced. Each is also counted
	// under its state, so Done less Retried counts each finished run of
	// the cell once.
	Retried int `json:"retried,omitzero"`
	// Outcomes counts the finished trials by outcome.
	Outcomes map[TrialOutcome]int `json:"outcomes,omitempty"`
}

// RunsDone returns how many of the cell's runs have finished: Done less
// Retried, held to Declared when the cell declares some.
func (t GridTrials) RunsDone() int {
	done := t.Done - t.Retried
	if t.Declared > 0 {
		return min(done, t.Declared)
	}
	return done
}

// GridParams choose what GetGrid reads. The zero value reads the primary
// metric for every candidate and task, a page of rows of DefaultLimit.
type GridParams struct {
	// Metric is a metric of the suite, by name or digest; the primary
	// metric when empty.
	Metric string
	// Comparisons are comparisons of this experiment a candidate's column
	// reads its summary and verdict from, one per candidate.
	Comparisons []string
	// Candidates keeps the columns of the candidates named, by digest.
	Candidates []string
	// Tasks keeps the rows of the tasks named.
	Tasks []string
	// State keeps the rows that hold a cell in this state.
	State CellState
	// Limit is the page size in rows, at most MaxLimit; DefaultLimit when
	// zero.
	Limit int
	// Cursor is the NextCursor of the page of rows before.
	Cursor string
}

func (p *GridParams) query() url.Values {
	if p == nil {
		return url.Values{}
	}
	q := listQuery(p.Limit, p.Cursor)
	setNonEmpty(q, "metric", p.Metric)
	setNonEmpty(q, "state", string(p.State))
	for _, v := range p.Comparisons {
		q.Add("comparison", v)
	}
	for _, v := range p.Candidates {
		q.Add("candidate", v)
	}
	for _, v := range p.Tasks {
		q.Add("task", v)
	}
	return q
}

// GetGrid returns a page of the candidate by task grid of the experiment
// id, as params says; follow NextCursor through params.Cursor for the rest
// of the rows. An id the caller's context holds no experiment by returns
// an error matching latere.ErrNotFound. params may be nil.
func (c *Client) GetGrid(ctx context.Context, id string, params *GridParams) (*Grid, error) {
	p, err := experimentPath(id, "/grid")
	if err != nil {
		return nil, err
	}
	var out Grid
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodGet, Path: p, Query: params.query()}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
