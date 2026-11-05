// SPDX-FileCopyrightText: 2026 Latere AI
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

// A reply is what a recorder answers one route with.
type reply struct {
	status int
	body   string
}

// A request is one the recorder was sent, with its body and the headers
// the tests read.
type request struct {
	method, path, query string
	key                 string
	body                string
	// ifMatch and ifNoneMatch are the preconditions the request sent.
	ifMatch, ifNoneMatch string
}

// recorder answers the routes of replies, keyed "METHOD /path" (a body
// alone is a 200), and records every request.
type recorder struct {
	mu   sync.Mutex
	sent []request
}

func (r *recorder) all() []request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]request(nil), r.sent...)
}

func (r *recorder) last(t *testing.T) request {
	t.Helper()
	all := r.all()
	if len(all) == 0 {
		t.Fatal("no request was sent")
	}
	return all[len(all)-1]
}

func newRecorder(t *testing.T, replies map[string]reply) (*evals.Client, *recorder) {
	t.Helper()
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Error(err)
		}
		rec.mu.Lock()
		rec.sent = append(rec.sent, request{req.Method, req.URL.EscapedPath(), req.URL.RawQuery, req.Header.Get("Idempotency-Key"), string(body), req.Header.Get("If-Match"), req.Header.Get("If-None-Match")})
		rec.mu.Unlock()
		rp, ok := replies[req.Method+" "+req.URL.EscapedPath()]
		w.Header().Set("Content-Type", "application/json")
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":{"code":"not_found","message":"Not found.","details":{"retryable":false}}}`)
			return
		}
		if rp.status != 0 {
			w.WriteHeader(rp.status)
		}
		_, _ = io.WriteString(w, rp.body)
	}))
	t.Cleanup(srv.Close)
	cfg, err := latere.NewConfig(latere.WithBaseURL(srv.URL), latere.WithTokenSource(latere.StaticToken("tok_test")),
		latere.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	return evals.New(cfg), rec
}

const trialJSON = `{"id":"trial_2","experiment":"exp_1","owner":"o","cell":"cell_1","index":3,"state":"done","outcome":"completed",
"imported":false,"retry_of":"trial_1","executor_ref":"ses_9","declared":{"harness_version":"1.2","image":"img"},
"failure":{"code":"x","detail":"y"},"lease":{"id":"l1","subject":"s","expires_at":"2026-11-01T10:00:00Z"},
"sent":{"requests":4,"by_setting":[{"model":"haiku-5.5","reasoning":"low","requests":4}]},
"checks":[{"grader":"g","state":"done"}],"judgments":[{"grader":"j"}],"evidence":[{"kind":"check_output"}],
"observed":{"model":"haiku-5.5"},"outputs":{"a.txt":{}},"values":{"sha256:m1":true},
"created_at":"2026-11-01T09:00:00Z","started_at":"2026-11-01T09:01:00Z","agent_ended_at":"2026-11-01T09:05:00Z","ended_at":"2026-11-01T09:06:00Z","future":1}`

// A trial is read whole, the one it runs again named, and listed with every
// filter the document names.
func TestTrialsAreListedAndRead(t *testing.T) {
	c, rec := newRecorder(t, map[string]reply{
		"GET /v1/evals/experiments/exp_1/trials": {body: `{"items":[` + trialJSON + `],"next_cursor":"n2"}`},
		"GET /v1/evals/trials/trial_2":           {body: trialJSON},
	})
	ctx := t.Context()
	yes := true
	page, err := c.ListTrialsPage(ctx, "exp_1", &evals.ListTrialsParams{Cell: "cell_1", Task: "t", Candidate: "sha256:c", State: evals.TrialDone,
		Outcome: evals.OutcomeCompleted, Imported: &yes, Retried: &yes, Metric: "pass", Value: "true", Limit: 5, Cursor: "n1"})
	if err != nil || page.NextCursor != "n2" || len(page.Items) != 1 {
		t.Fatalf("page %+v, %v", page, err)
	}
	if q := rec.last(t).query; q != "candidate=sha256%3Ac&cell=cell_1&cursor=n1&imported=true&limit=5&metric=pass&outcome=completed&retried=true&state=done&task=t&value=true" {
		t.Errorf("query %q", q)
	}
	tr, err := c.GetTrial(ctx, "trial_2")
	if err != nil {
		t.Fatal(err)
	}
	if tr.ID != "trial_2" || tr.Experiment != "exp_1" || tr.Cell != "cell_1" || tr.Index != 3 || tr.State != evals.TrialDone ||
		tr.Outcome != evals.OutcomeCompleted || tr.RetryOf != "trial_1" || !tr.IsRetry() || tr.ExecutorRef != "ses_9" ||
		tr.Declared.Image != "img" || tr.Failure.Code != "x" || tr.Lease.ID != "l1" || tr.Lease.ExpiresAt.IsZero() ||
		tr.Sent.Requests != 4 || tr.Sent.BySetting[0].Model != "haiku-5.5" || len(tr.Checks) != 1 || len(tr.Judgments) != 1 || len(tr.Evidence) != 1 ||
		string(tr.Values) != `{"sha256:m1":true}` || tr.EndedAt.Sub(tr.StartedAt) != 5*time.Minute || tr.AgentEndedAt.IsZero() || tr.CreatedAt.IsZero() {
		t.Errorf("trial %+v", tr)
	}
	if !strings.Contains(string(tr.RawJSON()), `"future":1`) {
		t.Errorf("RawJSON %s", tr.RawJSON())
	}
	n := 0
	for got, err := range c.ListTrials(ctx, "exp_1", &evals.ListTrialsParams{Limit: 1}) {
		if err != nil {
			t.Fatal(err)
		}
		if got.ID != "trial_2" {
			t.Errorf("listed %+v", got)
		}
		if n++; n == 2 {
			break
		}
	}
	if n != 2 {
		t.Errorf("the iterator followed %d pages", n)
	}
}

func TestTrialCallsRefuseWhatNeedsNoRequest(t *testing.T) {
	c, rec := newRecorder(t, nil)
	ctx := t.Context()
	if _, err := c.GetTrial(ctx, ""); err == nil {
		t.Error("GetTrial accepted an empty id")
	}
	if _, err := c.GetTrial(ctx, ".."); err == nil {
		t.Error("GetTrial accepted ..")
	}
	for _, err := range c.ListTrials(ctx, "", nil) {
		if err == nil {
			t.Error("ListTrials accepted an empty experiment id")
		}
	}
	if _, err := c.ListTrialsPage(ctx, "exp_1", &evals.ListTrialsParams{Value: "true"}); err == nil {
		t.Error("a Value without a Metric was sent")
	}
	if got := rec.all(); len(got) != 0 {
		t.Errorf("sent %v", got)
	}
	if _, err := c.GetTrial(ctx, "gone"); !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("a missing trial: %v", err)
	}
	var tr evals.Trial
	if err := json.Unmarshal([]byte(`{"id":"t"}`), &tr); err != nil || tr.IsRetry() {
		t.Errorf("a first attempt: %+v, %v", tr, err)
	}
}
