// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package evals_test

import (
	"context"
	"errors"
	"fmt"
	"log"

	"latere.ai/x/sdk-go/evals"
	"latere.ai/x/sdk-go/latere"
)

func client() *evals.Client {
	cfg, err := latere.NewConfig() // the key in LATERE_KEY
	if err != nil {
		log.Fatal(err)
	}
	return evals.New(cfg)
}

// Listing the current version of each suite, by the reference that names
// exactly that version.
func ExampleClient_ListSuites() {
	for s, err := range client().ListSuites(context.Background(), nil) {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(s.Ref(), len(s.Tasks), "tasks")
	}
}

// Reading a suite's current version by its name, then its metrics by the
// digests it names them by.
func ExampleClient_GetSuite() {
	ctx := context.Background()
	ev := client()
	s, err := ev.GetSuite(ctx, "fix-flaky-tests")
	if err != nil {
		log.Fatal(err)
	}
	for _, digest := range s.Metrics {
		m, err := ev.GetMetric(ctx, digest)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(m.Name, m.Type, m.Direction, digest == s.PrimaryMetric)
	}
}

// Every version of a suite, newest first, with the current one marked.
func ExampleClient_ListSuiteVersions() {
	for s, err := range client().ListSuiteVersions(context.Background(), "fix-flaky-tests", nil) {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(s.Digest, s.CreatedAt.Format("2006-01-02"), s.Current)
	}
}

// Listing the score metrics of every version.
func ExampleClient_ListMetrics() {
	params := &evals.ListMetricsParams{Type: evals.MetricScore, AllVersions: true}
	for m, err := range client().ListMetrics(context.Background(), params) {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(m.Ref(), m.Range, m.Unit)
	}
}

// Reading one version of a metric exactly, by name@digest.
func ExampleClient_GetMetric() {
	m, err := client().GetMetric(context.Background(), "pass_rate@sha256:2c26b46b68ffc68ff99b453c1d30413413422d706483bfa0f98a5e886266e7ae")
	if errors.Is(err, latere.ErrNotFound) {
		fmt.Println("no such version")
		return
	}
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(m.Name, m.Description)
}

// Listing the model judges, with the model each asks.
func ExampleClient_ListGraders() {
	for g, err := range client().ListGraders(context.Background(), &evals.ListGradersParams{Kind: evals.GraderModelJudge}) {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(g.Name, g.ModelJudge.Model)
	}
}

// Listing the tasks one version of a suite pins.
func ExampleClient_ListTasks() {
	ctx := context.Background()
	ev := client()
	s, err := ev.GetSuite(ctx, "fix-flaky-tests")
	if err != nil {
		log.Fatal(err)
	}
	for task, err := range ev.ListTasks(ctx, &evals.ListTasksParams{Suite: s.Digest}) {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(task.Name, task.Timeout, len(task.Checks), "checks")
	}
}

// Reading a task, with what it starts from.
func ExampleClient_GetTask() {
	task, err := client().GetTask(context.Background(), "fix_flaky_login")
	if err != nil {
		log.Fatal(err)
	}
	if w := task.Workspace; w != nil && w.Kind == evals.WorkspaceRepository {
		fmt.Println("starts from", w.Repository, "at", w.Commit)
	}
	fmt.Println(task.Instruction)
}

// Listing the candidates that run an agent of the platform.
func ExampleClient_ListCandidates() {
	for c, err := range client().ListCandidates(context.Background(), &evals.ListCandidatesParams{Kind: evals.CandidateAgent}) {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(c.Name, c.Agent.Model)
	}
}

// Reading a candidate and saying what it runs, by its kind.
func ExampleClient_GetCandidate() {
	c, err := client().GetCandidate(context.Background(), "acme-harness-1-4")
	if err != nil {
		log.Fatal(err)
	}
	switch c.Kind {
	case evals.CandidateAgent:
		fmt.Println("the agent", c.Agent.AgentVersion, "on", c.Agent.Model)
	case evals.CandidateProgram:
		fmt.Println("the image", c.Program.Image)
	case evals.CandidateExternal:
		fmt.Println(c.External.Harness, c.External.Version)
	}
}

// The experiments running now, with their trials done of all.
func ExampleClient_ListExperiments() {
	params := &evals.ListExperimentsParams{State: evals.ExperimentRunning}
	for e, err := range client().ListExperiments(context.Background(), params) {
		if err != nil {
			log.Fatal(err)
		}
		all := 0
		for _, n := range e.Progress.Trials {
			all += n
		}
		fmt.Println(e.ID, e.Progress.Trials[evals.TrialDone], "of", all)
	}
}

// One page of a suite's experiments at a time, keeping the cursor between
// requests, as a web page that shows a page per view does.
func ExampleClient_ListExperimentsPage() {
	params := &evals.ListExperimentsParams{Suite: "fix-flaky-tests", Limit: 20}
	page, err := client().ListExperimentsPage(context.Background(), params)
	if err != nil {
		log.Fatal(err)
	}
	for _, e := range page.Items {
		fmt.Println(e.ID, e.State)
	}
	// The next view asks for the page after this one; the cursor is empty
	// on the last page.
	fmt.Println("next:", page.NextCursor)
}

// Reading an experiment, with what it spent of its budget.
func ExampleClient_GetExperiment() {
	e, err := client().GetExperiment(context.Background(), "exp_01jb2c3d4e5f6g7h8j9k0m1n2p")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(e.State, e.Progress.Cells)
	if b := e.Budget; b.SpentMicroUSD != nil && b.CapMicroUSD != nil {
		fmt.Printf("spent $%.2f of $%.2f\n", float64(*b.SpentMicroUSD)/1e6, float64(*b.CapMicroUSD)/1e6)
	}
}

// The cells of an experiment that wait for a retry, with why.
func ExampleClient_ListCells() {
	ctx := context.Background()
	for _, state := range []evals.CellState{evals.CellBlocked, evals.CellOverBudget} {
		for cell, err := range client().ListCells(ctx, "exp_01jb2c3d4e5f6g7h8j9k0m1n2p", &evals.ListCellsParams{State: state}) {
			if err != nil {
				log.Fatal(err)
			}
			fmt.Println(cell.Task, cell.Candidate, cell.State, cell.ParkedReason)
		}
	}
}

// The final comparisons of an experiment, with what could explain them.
func ExampleClient_ListExperimentComparisons() {
	final := true
	params := &evals.ListExperimentComparisonsParams{Final: &final}
	for c, err := range client().ListExperimentComparisons(context.Background(), "exp_01jb2c3d4e5f6g7h8j9k0m1n2p", params) {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(c.Candidate, "against", c.ReferenceCandidate+c.Baseline, "confounds:", c.Confounds)
	}
}

// Following an experiment to its end, printing each cell that changes
// state and each comparison whose figures are final.
func ExampleClient_StreamEvents() {
	for ev, err := range client().StreamEvents(context.Background(), "exp_01jb2c3d4e5f6g7h8j9k0m1n2p", nil) {
		var refused *evals.StreamError
		if errors.As(err, &refused) && refused.Retryable() {
			log.Fatal("the stream ended for now; open it again later: ", err)
		}
		if err != nil {
			log.Fatal(err)
		}
		switch ev.Type {
		case evals.EventCell:
			var cell evals.CellChange
			if err := ev.Decode(&cell); err != nil {
				log.Fatal(err)
			}
			fmt.Println("cell", cell.ID, cell.State, cell.ParkedReason)
		case evals.EventComparison:
			var c evals.ComparisonChange
			if err := ev.Decode(&c); err != nil {
				log.Fatal(err)
			}
			if c.Final {
				fmt.Println("comparison", c.ID, "is final")
			}
		}
	}
	fmt.Println("the experiment ended")
}

// Naming one version exactly, as an experiment's create names its suite.
func ExampleVersion_Ref() {
	v := evals.Version{Name: "fix-flaky-tests", Digest: "sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"}
	fmt.Println(v.Ref())
	// Output: fix-flaky-tests@sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08
}
