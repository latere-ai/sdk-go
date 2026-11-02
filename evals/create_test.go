// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package evals_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"latere.ai/x/sdk-go/evals"
	"latere.ai/x/sdk-go/latere"
)

const createdJSON = `{"id":"exp_9","owner":"o","suite":"sha256:s","candidates":["sha256:c1"],"reference":{"candidate":"sha256:c1"},"trials":3,"parallelism":2,
"mode":"execute","scheduling":"interleaved","state":"pending","budget":{"cap_usd_micro":1000000},"progress":{},"metadata":{"pr":"7"},
"created_at":"2026-11-01T09:00:00Z","updated_at":"2026-11-01T09:00:00Z"}`

// A created experiment is sent with the body the document names, nothing
// of the call's own parameters in it, and a key as the header.
func TestAnExperimentIsCreated(t *testing.T) {
	c, rec := newRecorder(t, map[string]reply{"POST /v1/evals/experiments": {status: http.StatusCreated, body: createdJSON}})
	e, err := c.CreateExperiment(t.Context(), &evals.CreateExperimentParams{
		Suite: "sha256:s", Candidates: []string{"sha256:c1"}, Budget: evals.BudgetRequest{CapMicroUSD: 1000000}, Trials: 3, Mode: evals.ModeExecute,
		Parallelism: 2, Reference: &evals.Reference{Candidate: "sha256:c1"}, Scheduling: evals.SchedulingInterleaved,
		Comparisons:  []evals.DeclaredComparison{{Candidate: "sha256:c2", ReferenceCandidate: "sha256:c1", Variable: "model"}},
		Independence: &evals.Independence{Metric: "sha256:m"}, Metadata: map[string]string{"pr": "7"}, SignIn: "handle",
		Owner: "https://auth.latere.ai#org", IdempotencyKey: "key-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if e.ID != "exp_9" || e.State != evals.ExperimentPending || e.Metadata["pr"] != "7" {
		t.Errorf("experiment %+v", e)
	}
	got := rec.last(t)
	if got.key != "key-1" || got.query != "owner=https%3A%2F%2Fauth.latere.ai%23org" {
		t.Errorf("sent key %q query %q", got.key, got.query)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatal(err)
	}
	want := `{"budget":{"cap_usd_micro":1000000},"candidates":["sha256:c1"],"comparisons":[{"candidate":"sha256:c2","reference_candidate":"sha256:c1","variable":"model"}],` +
		`"independence":{"metric":"sha256:m"},"metadata":{"pr":"7"},"mode":"execute","parallelism":2,"reference":{"candidate":"sha256:c1"},"scheduling":"interleaved",` +
		`"sign_in":"handle","suite":"sha256:s","trials":3}`
	if gotJSON, _ := json.Marshal(body); string(gotJSON) != want {
		t.Errorf("body %s\nwant %s", gotJSON, want)
	}
	// Without a key none is sent.
	if _, err := c.CreateExperiment(t.Context(), &evals.CreateExperimentParams{Suite: "s", Candidates: []string{"c"}}); err != nil {
		t.Fatal(err)
	}
	if got := rec.last(t); got.key != "" || got.query != "" {
		t.Errorf("sent key %q query %q", got.key, got.query)
	}
	if _, err := c.CreateExperiment(t.Context(), nil); err == nil {
		t.Error("nil parameters were sent")
	}
}

// The refusal of a key's experiment that needs the sign-in is found as
// ErrSignInNotKept beside the API's own error and its status.
func TestSignInNotKeptIsMatched(t *testing.T) {
	c, _ := newRecorder(t, map[string]reply{"POST /v1/evals/experiments": {status: http.StatusForbidden,
		body: `{"error":{"code":"sign_in_not_kept","message":"The sign-in is not kept.","details":{"retryable":false}}}`}})
	_, err := c.CreateExperiment(t.Context(), &evals.CreateExperimentParams{Suite: "s", Candidates: []string{"c"}, IdempotencyKey: "k"})
	var apiErr *latere.Error
	if !errors.Is(err, evals.ErrSignInNotKept) || !errors.Is(err, latere.ErrForbidden) || !errors.As(err, &apiErr) || apiErr.Code != "sign_in_not_kept" {
		t.Errorf("error %v", err)
	}
	// Another 403 is not that refusal.
	c, _ = newRecorder(t, map[string]reply{"POST /v1/evals/experiments": {status: http.StatusForbidden, body: `{"error":{"code":"forbidden","message":"No."}}`}})
	if _, err := c.CreateExperiment(t.Context(), &evals.CreateExperimentParams{Suite: "s", Candidates: []string{"c"}}); errors.Is(err, evals.ErrSignInNotKept) || !errors.Is(err, latere.ErrForbidden) {
		t.Errorf("another refusal: %v", err)
	}
}

const leaseJSON = `{"lease":"lease_1","expires_at":"2026-11-01T10:00:00Z","model_key":"key_secret","trials":[{"id":"trial_1","cell":"cell_1","index":0,
"candidate":{"name":"h","digest":"sha256:c1","external":{"digest":"sha256:e","harness":"codex","version":"1","model":"haiku-5.5","reasoning":"low","flags":{"a":"b"}}},
"task":{"name":"t","digest":"sha256:t"},"instruction":"Do it.","inputs":[{"path":"a"}],"outputs":["out.txt"],"max_turns":9,"timeout":"10m","lux_labels":"examen.trial=trial_1"}]}`

// Leasing takes the pending trials with what a harness needs, and an
// answer of nothing to lease is no lease and no error.
func TestTrialsAreLeased(t *testing.T) {
	c, rec := newRecorder(t, map[string]reply{
		"POST /v1/evals/experiments/exp_1/work/lease":     {body: leaseJSON},
		"POST /v1/evals/experiments/exp_empty/work/lease": {status: http.StatusNoContent},
	})
	l, err := c.LeaseTrials(t.Context(), "exp_1", &evals.LeaseTrialsParams{Candidate: "sha256:c1", Max: 2, IdempotencyKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if got := rec.last(t); got.key != "k" || got.body != `{"candidate":"sha256:c1","max":2}` {
		t.Errorf("sent %+v", got)
	}
	tr := l.Trials[0]
	if l.Lease != "lease_1" || l.ModelKey != "key_secret" || l.ExpiresAt.Sub(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) != 10*time.Hour || tr.ID != "trial_1" ||
		tr.Candidate.External.Harness != "codex" || tr.Candidate.External.Flags["a"] != "b" || tr.Task.Name != "t" || tr.Outputs[0] != "out.txt" || tr.MaxTurns != 9 ||
		tr.LuxLabels != "examen.trial=trial_1" || len(tr.Inputs) != 1 || !strings.Contains(string(l.RawJSON()), "lease_1") {
		t.Errorf("lease %+v", l)
	}
	if none, err := c.LeaseTrials(t.Context(), "exp_empty", nil); none != nil || err != nil {
		t.Errorf("nothing to lease: %+v, %v", none, err)
	}
	if got := rec.last(t); got.key != "" || got.body != `{}` {
		t.Errorf("sent %+v", got)
	}
	if _, err := c.LeaseTrials(t.Context(), "", nil); err == nil {
		t.Error("an empty id was sent")
	}
}

// A submitted outcome carries the lease, the times as RFC 3339 and the
// declared provenance, and answers the trial.
func TestATrialIsSubmitted(t *testing.T) {
	c, rec := newRecorder(t, map[string]reply{"POST /v1/evals/trials/trial_1/submit": {body: trialJSON}})
	start := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	tr, err := c.SubmitTrial(t.Context(), "trial_1", &evals.SubmitTrialParams{
		Lease: "lease_1", Outcome: evals.OutcomeCompleted, StartedAt: start, AgentEndedAt: start.Add(time.Minute),
		Declared: &evals.TrialDeclared{Image: "img"}, Outputs: []evals.SubmitOutput{{Path: "out.txt", Arca: evals.ArcaFile{Path: "p", Digest: "sha256:d"}}},
		LuxRequests: []string{"r1"}, Measurements: []evals.SubmitMeasurement{{Metric: "m", Value: true}},
		ServiceUsage: map[string]evals.ServiceUsage{"search": {CostMicroUSD: 5}}, ServiceVersions: map[string]string{"search": "1"}, Ref: "run-1",
	})
	if err != nil || tr.State != evals.TrialDone {
		t.Fatalf("trial %+v, %v", tr, err)
	}
	want := `{"agent_ended_at":"2026-11-01T09:01:00Z","declared":{"image":"img"},"lease":"lease_1","lux_requests":["r1"],"measurements":[{"metric":"m","value":true}],` +
		`"outcome":"completed","outputs":[{"arca":{"digest":"sha256:d","path":"p"},"path":"out.txt"}],"ref":"run-1","service_usage":{"search":{"cost_usd_micro":5}},` +
		`"service_versions":{"search":"1"},"started_at":"2026-11-01T09:00:00Z"}`
	var body map[string]any
	if err := json.Unmarshal([]byte(rec.last(t).body), &body); err != nil {
		t.Fatal(err)
	}
	if got, _ := json.Marshal(body); string(got) != want {
		t.Errorf("body %s\nwant %s", got, want)
	}
	if _, err := c.SubmitTrial(t.Context(), "", &evals.SubmitTrialParams{}); err == nil {
		t.Error("an empty id was sent")
	}
	if _, err := c.SubmitTrial(t.Context(), "trial_1", nil); err == nil {
		t.Error("nil parameters were sent")
	}
}
