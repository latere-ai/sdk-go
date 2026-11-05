// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package evals_test

import (
	"errors"
	"strings"
	"testing"

	"latere.ai/x/sdk-go/evals"
	"latere.ai/x/sdk-go/latere"
)

const gridJSON = `{"experiment":"exp_1","state":"running",
"metric":{"name":"pass_rate","digest":"sha256:m1","type":"binary","direction":"higher_is_better","aggregate":"rate"},
"reference":{"candidate":"sha256:c1"},
"candidates":[{"name":"goalless-agent","digest":"sha256:c1"},{"name":"goalless-agent","digest":"sha256:c2"}],
"items":[{"task":{"name":"t1","digest":"sha256:t1"},
 "reference_summary":{"aggregate":"rate","n":5,"denominator":5,"missing":0,"not_applicable":0,"final":true,"value":0.6,"numerator":3,"interval":[0.2,0.9]},
 "cells":[
  {"candidate":"sha256:c1","cell":"cell_1","state":"complete","trials":{"declared":5,"done":7,"retried":2,"pending":0,"running":0,"checking":0,"outcomes":{"completed":5}}},
  {"candidate":"sha256:c2","cell":"cell_2","state":"blocked","parked_reason":"run_denied","comparison":"cmp_1","verdict":"improved","reason":"Higher.",
   "summary":{"aggregate":"rate","n":5,"denominator":5,"missing":0,"not_applicable":0,"final":false,"value":0.8}}]}],
"next_cursor":"r2","future":1}`

// A grid reads its columns, its rows and each entry's cell, counts and
// analysis, and a cell's runs done leave out the trials a retry replaced.
func TestTheGridIsRead(t *testing.T) {
	c, rec := newRecorder(t, map[string]reply{"GET /v1/evals/experiments/exp_1/grid": {body: gridJSON}})
	g, err := c.GetGrid(t.Context(), "exp_1", &evals.GridParams{Metric: "pass_rate", Comparisons: []string{"cmp_1", "cmp_2"}, Candidates: []string{"sha256:c2"},
		Tasks: []string{"t1"}, State: evals.CellBlocked, Limit: 10, Cursor: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	if q := rec.last(t).query; q != "candidate=sha256%3Ac2&comparison=cmp_1&comparison=cmp_2&cursor=r1&limit=10&metric=pass_rate&state=blocked&task=t1" {
		t.Errorf("query %q", q)
	}
	if g.Experiment != "exp_1" || g.State != evals.ExperimentRunning || g.Metric.Aggregate != evals.AggregateRate || g.Metric.Direction != evals.DirectionHigherIsBetter ||
		g.Reference.Candidate != "sha256:c1" || len(g.Candidates) != 2 || g.Candidates[1].Digest != "sha256:c2" || g.NextCursor != "r2" || len(g.Items) != 1 {
		t.Fatalf("grid %+v", g)
	}
	row := g.Items[0]
	ref := row.ReferenceSummary
	if row.Task.Name != "t1" || ref == nil || *ref.Numerator != 3 || ref.Interval[1] != 0.9 {
		t.Errorf("row %+v", row)
	}
	if v, ok := ref.Number(); !ok || v != 0.6 {
		t.Errorf("reference value %v, %v", v, ok)
	}
	first, second := row.Cells[0], row.Cells[1]
	if first.State != evals.CellComplete || first.Trials.RunsDone() != 5 || first.Trials.Outcomes[evals.OutcomeCompleted] != 5 || first.Summary != nil || first.Verdict != "" {
		t.Errorf("first cell %+v", first)
	}
	if !second.State.Parked() || second.ParkedReason != "run_denied" || second.Comparison != "cmp_1" || second.Verdict != evals.VerdictImproved ||
		second.Summary == nil || second.Summary.Final || second.Trials != nil {
		t.Errorf("second cell %+v", second)
	}
	if !strings.Contains(string(g.RawJSON()), `"future":1`) {
		t.Errorf("RawJSON %s", g.RawJSON())
	}
	if _, err := c.GetGrid(t.Context(), "", nil); err == nil {
		t.Error("an empty id was sent")
	}
	if _, err := c.GetGrid(t.Context(), "gone", nil); !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("a missing experiment: %v", err)
	}
	if got := (evals.GridTrials{Done: 3, Retried: 1}).RunsDone(); got != 2 {
		t.Errorf("a cell that declares none: %d", got)
	}
}

const resultJSON = `{"metric":"sha256:m1","metric_name":"pass_rate","level":"task","task":"t1","task_digests":{"candidate":"sha256:t1","reference":"sha256:t1"},
"direction":"higher_is_better",
"candidate":{"aggregate":"rate","n":10,"denominator":10,"missing":0,"not_applicable":0,"final":true,"value":0.8,"numerator":8,"interval":[0.5,0.95]},
"reference":{"aggregate":"rate","n":10,"denominator":10,"missing":0,"not_applicable":0,"final":true,"value":0.5,"numerator":5},
"delta_kind":"difference","delta":0.3,"delta_interval":[0.05,0.55],"verdict":"improved","reason":"Past the noise.","p":0.01,"q":0.02,
"method":"boschloo_exact","statistic":"fisher_p","exact":true,"seed":"ab",
"confounds":[{"name":"window","candidate":{"from":"a","to":"b"}}],"left_out":[{"task":"t2","why":"unpaired","candidate":3,"reference":0}],"future":1}`

const labelResultJSON = `{"metric":"sha256:m2","metric_name":"category","level":"suite","direction":"none",
"candidate":{"aggregate":"distribution","n":4,"denominator":4,"missing":0,"not_applicable":0,"final":true,"value":{"a":0.75,"b":0.25},
 "distribution":[{"label":"a","count":3,"proportion":0.75,"interval":[0.3,0.95]},{"label":"b","count":1,"proportion":0.25,"unmapped":true}],"mode":"a"},
"reference":{"aggregate":"distribution","n":4,"denominator":4,"missing":0,"not_applicable":0,"final":true},
"delta_kind":"per_label","delta":{"a":0.25,"b":-0.25},"delta_interval":{"a":[0,0.5]},"verdict":"changed","reason":"Labels differ.","method":"chi_square_permutation","resamples":999}`

// A comparison is read with its suite-level results, its task-level ones
// are paged, and each result carries the figures the analysis computed.
func TestComparisonsAreReadWithTheirResults(t *testing.T) {
	cmp := `{"id":"cmp_1","experiment":"exp_1","candidate":"sha256:c2","automatic":true,"exploratory":false,"state":"complete","final":true,
"results":[` + labelResultJSON + `],"candidate_suite":{"sha256:m1":{"n":3,"denominator":3,"missing":0,"not_applicable":0,"final":true,"value":2}},
"created_at":"2026-11-01T09:00:00Z","computed_at":"2026-11-01T09:10:00Z","correction":{"method":"benjamini_hochberg","family":2}}`
	c, rec := newRecorder(t, map[string]reply{
		"GET /v1/evals/comparisons/cmp_1":      {body: cmp},
		"GET /v1/evals/comparisons/cmp_1/rows": {body: `{"items":[` + resultJSON + `],"next_cursor":"r2"}`},
	})
	ctx := t.Context()
	got, err := c.GetComparison(ctx, "cmp_1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "cmp_1" || got.State != evals.ComparisonComplete || len(got.Results) != 1 || got.CandidateSuite["sha256:m1"].N != 3 || !strings.Contains(string(got.RawJSON()), "benjamini_hochberg") {
		t.Fatalf("comparison %+v", got)
	}
	r := got.Results[0]
	labels, err := r.DeltaLabels()
	if err != nil || labels["b"] != -0.25 || r.DeltaKind != evals.DeltaPerLabel || r.Level != evals.ResultSuite || r.Verdict != evals.VerdictChanged ||
		r.Candidate.Distribution[0].Count != 3 || r.Candidate.Distribution[0].Interval[0] != 0.3 || !r.Candidate.Distribution[1].Unmapped || r.Candidate.Mode != "a" ||
		*r.Resamples != 999 || r.P != nil || r.Q != nil {
		t.Errorf("label result %+v, %v", r, err)
	}
	if _, ok := r.DeltaNumber(); ok {
		t.Error("a per-label delta read as a number")
	}
	page, err := c.ListComparisonRowsPage(ctx, "cmp_1", &evals.ListComparisonRowsParams{Metric: "m", Task: "t1", Verdict: evals.VerdictImproved, Limit: 5, Cursor: "r1"})
	if err != nil || page.NextCursor != "r2" || len(page.Items) != 1 {
		t.Fatalf("rows %+v, %v", page, err)
	}
	if q := rec.last(t).query; q != "cursor=r1&limit=5&metric=m&task=t1&verdict=improved" {
		t.Errorf("query %q", q)
	}
	row := page.Items[0]
	delta, ok := row.DeltaNumber()
	cand, _ := row.Candidate.Number()
	if !ok || delta != 0.3 || cand != 0.8 || row.Verdict != evals.VerdictImproved || *row.P != 0.01 || *row.Q != 0.02 || !row.Exact ||
		row.Method != "boschloo_exact" || row.TaskDigests.Candidate != "sha256:t1" || row.Direction != evals.DirectionHigherIsBetter ||
		row.Candidate.Interval[0] != 0.5 || row.Reference.Interval != nil || row.Confounds[0].Name != "window" || row.LeftOut[0].Why != "unpaired" {
		t.Errorf("row %+v", row)
	}
	if labels, err := row.DeltaLabels(); labels != nil || err != nil {
		t.Errorf("a numeric delta read as labels: %v, %v", labels, err)
	}
	if !strings.Contains(string(row.RawJSON()), `"future":1`) {
		t.Errorf("RawJSON %s", row.RawJSON())
	}
	n := 0
	for _, err := range c.ListComparisonRows(ctx, "cmp_1", nil) {
		if err != nil {
			t.Fatal(err)
		}
		if n++; n == 2 {
			break
		}
	}
	if n != 2 {
		t.Errorf("the iterator followed %d pages", n)
	}
	if _, err := c.GetComparison(ctx, ""); err == nil {
		t.Error("an empty id was sent")
	}
	for _, err := range c.ListComparisonRows(ctx, "", nil) {
		if err == nil {
			t.Error("an empty id was sent")
		}
	}
	if _, err := c.GetComparison(ctx, "gone"); !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("a missing comparison: %v", err)
	}
}
