// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package evals_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"latere.ai/x/sdk-go/evals"
	"latere.ai/x/sdk-go/latere"
)

// fake answers each route of answers, keyed by "METHOD /path", and records
// every request it was sent as its path and query.
type fake struct {
	mu   sync.Mutex
	sent []string
}

func (f *fake) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sent...)
}

func newClient(t *testing.T, answers map[string]string) (*evals.Client, *fake) {
	t.Helper()
	f := &fake{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.URL.EscapedPath()
		if r.URL.RawQuery != "" {
			got += "?" + r.URL.RawQuery
		}
		f.mu.Lock()
		f.sent = append(f.sent, got)
		f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer tok_test" {
			t.Errorf("%s carries %q", got, r.Header.Get("Authorization"))
		}
		body, ok := answers[r.Method+" "+got]
		if !ok {
			body, ok = answers[r.Method+" "+r.URL.EscapedPath()]
		}
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":{"code":"not_found","message":"Not found.","details":{"retryable":false}}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	cfg, err := latere.NewConfig(latere.WithBaseURL(srv.URL), latere.WithTokenSource(latere.StaticToken("tok_test")),
		latere.WithBackoff(time.Millisecond, time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	return evals.New(cfg), f
}

const record = `"id":"ver_1","owner":"https://auth.latere.ai#u_1","digest":"sha256:aa","current":true,"created_at":"2026-10-01T09:00:00Z"`

func TestMetricsAreReadWithTheirRecord(t *testing.T) {
	metric := `{` + record + `,"name":"pass_rate","type":"binary","direction":"higher_is_better","level":"trial","applies_to":["completed","declined"],"future":1}`
	c, f := newClient(t, map[string]string{
		"GET /v1/evals/metrics?limit=2":             `{"items":[` + metric + `],"next_cursor":"c2"}`,
		"GET /v1/evals/metrics?cursor=c2&limit=2":   `{"items":[` + metric + `]}`,
		"GET /v1/evals/metrics/pass_rate@sha256:aa": metric,
		"GET /v1/evals/metrics/pass_rate/versions":  `{"items":[` + metric + `]}`,
	})
	ctx := t.Context()
	var names []string
	for m, err := range c.ListMetrics(ctx, &evals.ListMetricsParams{Limit: 2, Cursor: "ignored"}) {
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, m.Ref())
	}
	if strings.Join(names, ",") != "pass_rate@sha256:aa,pass_rate@sha256:aa" {
		t.Errorf("listed %v", names)
	}
	m, err := c.GetMetric(ctx, "pass_rate@sha256:aa")
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "pass_rate" || m.Digest != "sha256:aa" || !m.Current || m.CreatedAt.IsZero() || m.Type != evals.MetricBinary ||
		m.Direction != evals.DirectionHigherIsBetter || m.Level != evals.LevelTrial || len(m.AppliesTo) != 2 || m.AppliesTo[1] != evals.OutcomeDeclined {
		t.Errorf("metric %+v", m)
	}
	if !strings.Contains(string(m.RawJSON()), `"future":1`) {
		t.Errorf("RawJSON %s", m.RawJSON())
	}
	page, err := c.ListMetricVersionsPage(ctx, "pass_rate", nil)
	if err != nil || len(page.Items) != 1 || page.NextCursor != "" {
		t.Errorf("versions %+v, %v", page, err)
	}
	if got := f.requests(); len(got) != 4 {
		t.Errorf("sent %v", got)
	}
}

func TestEachKindReadsItsFields(t *testing.T) {
	grader := `{` + record + `,"name":"tests_pass","kind":"check","produces":["sha256:m1"],"check":{"command":["go","test","./..."],"env":{"CI":"1"},"files":{"path":"files/fixtures","digest":"sha256:f1"}}}`
	judge := `{` + record + `,"name":"tone","kind":"model_judge","level":"candidate","model_judge":{"model":"anthropic/claude-haiku-5-5","temperature":0,"role":"grade","inputs":{"transcript":true,"outputs":["report/"]}}}`
	task := `{` + record + `,"name":"fix_flaky","instruction":"Fix the flaky test.","inputs":[{"name":"notes.md","text":"hi"},{"name":"seed","origo":{"repository":"acme/app","commit":"abc","path":"seed"}}],` +
		`"workspace":{"kind":"origo","repository":"acme/app","commit":"abc"},"environment":{"image":"img@sha256:i1","resources":{"cpu":"2"},"network":"none"},"checks":["sha256:g1"],"timeout":"30m","max_turns":40,"factors":{"object":"build"}}`
	suite := `{` + record + `,"name":"flaky","tasks":["sha256:t1","sha256:t2"],"metrics":["sha256:m1","builtin:cost_usd"],"primary_metric":"sha256:m1","weights":{"sha256:t2":0.5}}`
	agent := `{` + record + `,"name":"harness_1_4","kind":"topos","topos":{"agent":"sha256:a1","model":"anthropic/claude-sonnet-5-5","tools":["bash"]},"metadata":{"team":"core"}}`
	program := `{` + record + `,"name":"runner","kind":"program","program":{"image":"run@sha256:r1","config":{"url":"https://example.com"},"lux":{"surface":"openai_responses"},"credentials":[{"audience":"api.latere.ai","host":"api.latere.ai","env":"TOKEN"}]}}`
	external := `{` + record + `,"name":"theirs","kind":"external","external":{"harness":"bench","version":"2.1","flags":{"fast":"1"}}}`
	c, _ := newClient(t, map[string]string{
		"GET /v1/evals/graders/tests_pass":     grader,
		"GET /v1/evals/graders/tone":           judge,
		"GET /v1/evals/tasks/fix_flaky":        task,
		"GET /v1/evals/suites/flaky":           suite,
		"GET /v1/evals/candidates/harness_1_4": agent,
		"GET /v1/evals/candidates/runner":      program,
		"GET /v1/evals/candidates/theirs":      external,
	})
	ctx := t.Context()
	g, err := c.GetGrader(ctx, "tests_pass")
	if err != nil || g.Kind != evals.GraderCheck || g.Check == nil || g.Check.Command[1] != "test" || g.Check.Env["CI"] != "1" || g.Check.Files.Digest != "sha256:f1" || g.ModelJudge != nil {
		t.Errorf("grader %+v, %v", g, err)
	}
	j, err := c.GetGrader(ctx, "tone")
	if err != nil || j.Level != evals.LevelCandidate || j.ModelJudge == nil || j.ModelJudge.Temperature == nil || *j.ModelJudge.Temperature != 0 ||
		j.ModelJudge.Role != evals.JudgeGrade || !j.ModelJudge.Inputs.Transcript || j.ModelJudge.Inputs.Outputs[0] != "report/" {
		t.Errorf("judge %+v, %v", j, err)
	}
	tk, err := c.GetTask(ctx, "fix_flaky")
	if err != nil || tk.Instruction != "Fix the flaky test." || tk.Inputs[0].Text != "hi" || tk.Inputs[1].Repository.Commit != "abc" ||
		tk.Workspace.Kind != evals.WorkspaceRepository || tk.Environment.Image != "img@sha256:i1" || string(tk.Environment.Network) != `"none"` ||
		tk.Timeout != "30m" || tk.MaxTurns != 40 || tk.Factors["object"] != "build" || tk.Checks[0] != "sha256:g1" {
		t.Errorf("task %+v, %v", tk, err)
	}
	s, err := c.GetSuite(ctx, "flaky")
	if err != nil || len(s.Tasks) != 2 || s.PrimaryMetric != "sha256:m1" || s.Weights["sha256:t2"] != 0.5 || s.Metrics[1] != "builtin:cost_usd" {
		t.Errorf("suite %+v, %v", s, err)
	}
	a, err := c.GetCandidate(ctx, "harness_1_4")
	if err != nil || a.Kind != evals.CandidateAgent || a.Agent == nil || a.Agent.AgentVersion != "sha256:a1" || a.Agent.Tools[0] != "bash" || a.Metadata["team"] != "core" {
		t.Errorf("agent candidate %+v, %v", a, err)
	}
	p, err := c.GetCandidate(ctx, "runner")
	if err != nil || p.Kind != evals.CandidateProgram || p.Program.Image != "run@sha256:r1" || !strings.Contains(string(p.Program.Config), "example.com") ||
		p.Program.Models.Surface != "openai_responses" || p.Program.Credentials[0].Env != "TOKEN" {
		t.Errorf("program candidate %+v, %v", p, err)
	}
	e, err := c.GetCandidate(ctx, "theirs")
	if err != nil || e.External == nil || e.External.Harness != "bench" || e.External.Version != "2.1" || e.External.Flags["fast"] != "1" || e.Version.Name != "theirs" {
		t.Errorf("external candidate %+v, %v", e, err)
	}
}

func TestListsSendTheirFilters(t *testing.T) {
	c, f := newClient(t, map[string]string{
		"GET /v1/evals/metrics":                       `{"items":[]}`,
		"GET /v1/evals/graders":                       `{"items":[]}`,
		"GET /v1/evals/tasks":                         `{"items":[]}`,
		"GET /v1/evals/suites":                        `{"items":[]}`,
		"GET /v1/evals/candidates":                    `{"items":[]}`,
		"GET /v1/evals/experiments":                   `{"items":[]}`,
		"GET /v1/evals/experiments/exp_1/cells":       `{"items":[]}`,
		"GET /v1/evals/experiments/exp_1/comparisons": `{"items":[]}`,
		"GET /v1/evals/suites/flaky/versions":         `{"items":[]}`,
	})
	ctx := t.Context()
	yes, no := true, false
	calls := []func() error{
		func() error {
			_, err := c.ListMetricsPage(ctx, &evals.ListMetricsParams{Name: "pass_rate", Type: evals.MetricScore, AllVersions: true, Limit: 10, Cursor: "c1"})
			return err
		},
		func() error {
			_, err := c.ListGradersPage(ctx, &evals.ListGradersParams{Kind: evals.GraderModelJudge, Produces: "sha256:m1"})
			return err
		},
		func() error {
			_, err := c.ListTasksPage(ctx, &evals.ListTasksParams{Suite: "sha256:s1", MetadataKey: "area", MetadataValue: "ci"})
			return err
		},
		func() error { _, err := c.ListSuitesPage(ctx, nil); return err },
		func() error {
			_, err := c.ListCandidatesPage(ctx, &evals.ListCandidatesParams{Kind: evals.CandidateAgent, AgentVersion: "sha256:a1", Model: "m"})
			return err
		},
		func() error {
			_, err := c.ListExperimentsPage(ctx, &evals.ListExperimentsParams{Suite: "flaky", State: evals.ExperimentRunning, Mode: evals.ModeImport, Candidate: "sha256:c1", Baseline: "bas_1", MetadataKey: "pr", MetadataValue: "42"})
			return err
		},
		func() error {
			_, err := c.ListCellsPage(ctx, "exp_1", &evals.ListCellsParams{State: evals.CellBlocked, Task: "fix_flaky", Candidate: "sha256:c1", Limit: 200})
			return err
		},
		func() error {
			_, err := c.ListExperimentComparisonsPage(ctx, "exp_1", &evals.ListExperimentComparisonsParams{Candidate: "sha256:c1", ReferenceCandidate: "sha256:c2", Baseline: "bas_1", Automatic: &yes, Final: &no})
			return err
		},
		func() error {
			_, err := c.ListSuiteVersionsPage(ctx, "flaky", &evals.ListVersionsParams{Limit: 5, Cursor: "c9"})
			return err
		},
	}
	for i, call := range calls {
		if err := call(); err != nil {
			t.Errorf("call %d: %v", i, err)
		}
	}
	want := []string{
		"/v1/evals/metrics?cursor=c1&limit=10&name=pass_rate&type=score&versions=all",
		"/v1/evals/graders?grader_kind=model_judge&produces=sha256%3Am1",
		"/v1/evals/tasks?metadata.area=ci&suite=sha256%3As1",
		"/v1/evals/suites",
		"/v1/evals/candidates?agent=sha256%3Aa1&candidate_kind=topos&model=m",
		"/v1/evals/experiments?baseline=bas_1&candidate=sha256%3Ac1&metadata.pr=42&mode=import&state=running&suite=flaky",
		"/v1/evals/experiments/exp_1/cells?candidate=sha256%3Ac1&limit=200&state=blocked&task=fix_flaky",
		"/v1/evals/experiments/exp_1/comparisons?automatic=true&baseline=bas_1&candidate=sha256%3Ac1&final=false&reference_candidate=sha256%3Ac2",
		"/v1/evals/suites/flaky/versions?cursor=c9&limit=5",
	}
	got := f.requests()
	if len(got) != len(want) {
		t.Fatalf("sent %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("request %d: %s, want %s", i, got[i], want[i])
		}
	}
}

func TestExperimentsAreRead(t *testing.T) {
	experiment := `{"id":"exp_1","owner":"org","created_by":"u_1","suite":"sha256:s1","candidates":["sha256:c1","sha256:c2"],"trials":5,"parallelism":4,` +
		`"budget":{"name":"evals","cap_usd_micro":5000000,"spent_usd_micro":1250000},"reference":{"candidate":"sha256:c1"},"mode":"execute","scheduling":"interleaved","state":"running",` +
		`"progress":{"cells":{"running":3,"blocked":1},"trials":{"done":7,"running":2},"outcomes":{"completed":6,"infra_failed":1}},` +
		`"usage":{"requests":12,"cost_usd_micro":900,"unpriced_requests":0,"read_to":"2026-10-01T10:00:00Z","unattributed":{"requests":1,"cost_usd_micro":10,"unpriced_requests":0}},` +
		`"comparisons":[{"candidate":"sha256:c2","reference_candidate":"sha256:c1","variable":"model"}],"independence":{"metric":"sha256:l1","by":["task"]},` +
		`"metadata":{"pr":"42"},"created_at":"2026-10-01T09:00:00Z","updated_at":"2026-10-01T09:30:00Z"}`
	cell := `{"id":"cel_1","experiment":"exp_1","task":"sha256:t1","candidate":"sha256:c1","state":"blocked","parked_reason":"run_denied","created_at":"2026-10-01T09:00:00Z","tuple":{"model":"m"}}`
	comparison := `{"id":"cmp_1","experiment":"exp_1","candidate":"sha256:c2","reference_candidate":"sha256:c1","automatic":true,"exploratory":false,"state":"complete","final":false,` +
		`"declared":0,"variable":["model"],"single_variable":true,"confounds":["tools"],"verdict_counts":{"sha256:m1":{"better":2}},"results":[{"metric":"sha256:m1"}],"computed_at":"2026-10-01T09:40:00Z"}`
	c, _ := newClient(t, map[string]string{
		"GET /v1/evals/experiments":                   `{"items":[` + experiment + `]}`,
		"GET /v1/evals/experiments/exp_1":             experiment,
		"GET /v1/evals/experiments/exp_1/cells":       `{"items":[` + cell + `]}`,
		"GET /v1/evals/experiments/exp_1/comparisons": `{"items":[` + comparison + `]}`,
	})
	ctx := t.Context()
	for e, err := range c.ListExperiments(ctx, nil) {
		if err != nil || e.ID != "exp_1" {
			t.Errorf("listed %+v, %v", e, err)
		}
	}
	e, err := c.GetExperiment(ctx, "exp_1")
	if err != nil {
		t.Fatal(err)
	}
	if e.State != evals.ExperimentRunning || e.State.Finished() || e.Mode != evals.ModeExecute || e.Scheduling != evals.SchedulingInterleaved ||
		e.Reference.Candidate != "sha256:c1" || *e.Budget.CapMicroUSD != 5000000 || e.Budget.RemainingMicroUSD != nil ||
		e.Progress.Cells[evals.CellBlocked] != 1 || e.Progress.Trials[evals.TrialDone] != 7 || e.Progress.Outcomes[evals.OutcomeInfraFailed] != 1 ||
		e.Usage.Unattributed.Requests != 1 || e.Usage.ReadTo.IsZero() || e.Comparisons[0].Variable != "model" || e.Independence.By[0] != "task" ||
		e.UpdatedAt.Sub(e.CreatedAt) != 30*time.Minute || e.Metadata["pr"] != "42" {
		t.Errorf("experiment %+v", e)
	}
	for cl, err := range c.ListCells(ctx, "exp_1", &evals.ListCellsParams{State: evals.CellBlocked}) {
		if err != nil || !cl.State.Parked() || cl.ParkedReason != "run_denied" || !strings.Contains(string(cl.RawJSON()), "tuple") {
			t.Errorf("cell %+v, %v", cl, err)
		}
	}
	for cm, err := range c.ListExperimentComparisons(ctx, "exp_1", nil) {
		if err != nil || cm.State != evals.ComparisonComplete || cm.Final || cm.Declared == nil || *cm.Declared != 0 || !*cm.SingleVariable ||
			cm.VerdictCounts["sha256:m1"]["better"] != 2 || !strings.Contains(string(cm.RawJSON()), `"results"`) || cm.ComputedAt.IsZero() {
			t.Errorf("comparison %+v, %v", cm, err)
		}
	}
}

func TestAMissingObjectIsNotFound(t *testing.T) {
	c, _ := newClient(t, nil)
	ctx := t.Context()
	if _, err := c.GetSuite(ctx, "gone"); !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("GetSuite: %v", err)
	}
	if _, err := c.GetExperiment(ctx, "exp_gone"); !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("GetExperiment: %v", err)
	}
	for _, err := range c.ListTaskVersions(ctx, "gone", nil) {
		if !errors.Is(err, latere.ErrNotFound) {
			t.Errorf("ListTaskVersions: %v", err)
		}
	}
}

func TestEmptyArgumentsAreRefusedBeforeSending(t *testing.T) {
	c, f := newClient(t, nil)
	ctx := t.Context()
	var errs []error
	_, err := c.GetMetric(ctx, "")
	errs = append(errs, err)
	_, err = c.GetCandidate(ctx, "..")
	errs = append(errs, err)
	_, err = c.GetExperiment(ctx, "")
	errs = append(errs, err)
	_, err = c.ListGraderVersionsPage(ctx, "", nil)
	errs = append(errs, err)
	_, err = c.ListCellsPage(ctx, ".", nil)
	errs = append(errs, err)
	_, err = c.ListExperimentComparisonsPage(ctx, "", nil)
	errs = append(errs, err)
	_, err = c.ListTasksPage(ctx, &evals.ListTasksParams{MetadataValue: "orphan"})
	errs = append(errs, err)
	_, err = c.ListExperimentsPage(ctx, &evals.ListExperimentsParams{MetadataValue: "orphan"})
	errs = append(errs, err)
	_, err = c.ListCandidatesPage(ctx, &evals.ListCandidatesParams{MetadataValue: "orphan"})
	errs = append(errs, err)
	for _, err := range c.ListSuiteVersions(ctx, "", nil) {
		errs = append(errs, err)
	}
	for _, err := range c.ListCells(ctx, "", nil) {
		errs = append(errs, err)
	}
	for _, err := range c.ListExperimentComparisons(ctx, "", nil) {
		errs = append(errs, err)
	}
	for _, err := range c.ListTasks(ctx, &evals.ListTasksParams{MetadataValue: "orphan"}) {
		errs = append(errs, err)
	}
	for _, err := range c.ListExperiments(ctx, &evals.ListExperimentsParams{MetadataValue: "orphan"}) {
		errs = append(errs, err)
	}
	for _, err := range c.ListCandidates(ctx, &evals.ListCandidatesParams{MetadataValue: "orphan"}) {
		errs = append(errs, err)
	}
	for _, err := range c.StreamEvents(ctx, "", nil) {
		errs = append(errs, err)
	}
	for i, err := range errs {
		if err == nil {
			t.Errorf("call %d: no error", i)
		}
	}
	if len(errs) != 16 {
		t.Errorf("%d errors", len(errs))
	}
	if got := f.requests(); len(got) != 0 {
		t.Errorf("sent %v", got)
	}
}

func TestPathsAreEscaped(t *testing.T) {
	c, f := newClient(t, map[string]string{"GET /v1/evals/tasks/a%2Fb": `{` + record + `,"name":"a/b"}`})
	if _, err := c.GetTask(t.Context(), "a/b"); err != nil {
		t.Fatal(err)
	}
	if got := f.requests(); got[0] != "/v1/evals/tasks/a%2Fb" {
		t.Errorf("sent %v", got)
	}
}

func TestStateHelpers(t *testing.T) {
	for s, want := range map[evals.ExperimentState]bool{
		evals.ExperimentComplete: true, evals.ExperimentCanceled: true, evals.ExperimentGrading: false, evals.ExperimentPending: false,
	} {
		if s.Finished() != want {
			t.Errorf("%s.Finished() = %v", s, !want)
		}
	}
	for s, want := range map[evals.CellState]bool{
		evals.CellBlocked: true, evals.CellOverBudget: true, evals.CellCanceled: true, evals.CellComplete: false, evals.CellSmoking: false,
	} {
		if s.Parked() != want {
			t.Errorf("%s.Parked() = %v", s, !want)
		}
	}
	if v := (evals.Version{Name: "n", Digest: "sha256:d"}); v.Ref() != "n@sha256:d" {
		t.Errorf("Ref = %s", v.Ref())
	}
}

// A model decoded twice keeps the second answer's JSON: RawJSON is the
// bytes it was decoded from, not a buffer a decoder reuses.
func TestRawJSONIsACopy(t *testing.T) {
	b := []byte(`{` + record + `,"name":"x","tasks":[],"metrics":[],"primary_metric":"m"}`)
	var s evals.Suite
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	b[2] = 'X'
	if strings.Contains(string(s.RawJSON()), "X") {
		t.Errorf("RawJSON shares the decoder's buffer: %s", s.RawJSON())
	}
}

// Every iterating list reads its route to the end, and each kind keeps
// the JSON it was decoded from.
func TestEveryListIterates(t *testing.T) {
	item := `{` + record + `,"name":"n","kind":"check","tasks":[],"metrics":[],"primary_metric":"m","type":"count","direction":"none","later":true}`
	page := `{"items":[` + item + `]}`
	routes := []string{"metrics", "graders", "tasks", "suites", "candidates"}
	answers := map[string]string{}
	for _, r := range routes {
		answers["GET /v1/evals/"+r] = page
		answers["GET /v1/evals/"+r+"/n/versions"] = page
	}
	c, f := newClient(t, answers)
	ctx := t.Context()
	raws := map[string]json.RawMessage{}
	count := func(name string, raw json.RawMessage, err error) {
		if err != nil {
			t.Errorf("%s: %v", name, err)
		}
		raws[name] = raw
	}
	for m, err := range c.ListMetricVersions(ctx, "n", nil) {
		count("metric versions", m.RawJSON(), err)
	}
	for g, err := range c.ListGraders(ctx, nil) {
		count("graders", g.RawJSON(), err)
	}
	for g, err := range c.ListGraderVersions(ctx, "n", nil) {
		count("grader versions", g.RawJSON(), err)
	}
	for task, err := range c.ListTasks(ctx, nil) {
		count("tasks", task.RawJSON(), err)
	}
	for task, err := range c.ListTaskVersions(ctx, "n", nil) {
		count("task versions", task.RawJSON(), err)
	}
	for s, err := range c.ListSuites(ctx, nil) {
		count("suites", s.RawJSON(), err)
	}
	for cd, err := range c.ListCandidates(ctx, nil) {
		count("candidates", cd.RawJSON(), err)
	}
	for cd, err := range c.ListCandidateVersions(ctx, "n", nil) {
		count("candidate versions", cd.RawJSON(), err)
	}
	if len(raws) != 8 {
		t.Errorf("read %d lists", len(raws))
	}
	for name, raw := range raws {
		if !strings.Contains(string(raw), `"later":true`) {
			t.Errorf("%s: RawJSON %s", name, raw)
		}
	}
	if got := f.requests(); len(got) != 8 {
		t.Errorf("sent %v", got)
	}
}

// A model whose answer does not decode returns the decoder's error.
func TestAnAnswerThatDoesNotDecodeFails(t *testing.T) {
	for name, v := range map[string]any{
		"Metric": &evals.Metric{}, "Grader": &evals.Grader{}, "Task": &evals.Task{}, "Suite": &evals.Suite{},
		"Candidate": &evals.Candidate{}, "Experiment": &evals.Experiment{}, "Cell": &evals.Cell{}, "Comparison": &evals.Comparison{},
	} {
		if err := json.Unmarshal([]byte(`{"id":7}`), v); err == nil {
			t.Errorf("%s decoded an id that is a number", name)
		}
	}
}
