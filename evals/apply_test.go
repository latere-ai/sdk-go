// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package evals_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"latere.ai/x/sdk-go/evals"
	"latere.ai/x/sdk-go/latere"
)

const preconditionFailed = `{"error":{"code":"precondition_failed","message":"This changed since you last read it.","details":{"retryable":false}}}`

// Each kind of definition is applied by PUT to its name, with its own
// members as the body and none of the record the API writes, and a 201 is
// a new version and a 200 one made current again.
func TestDefinitionsAreApplied(t *testing.T) {
	rec := `"id":"ver_1","owner":"o","digest":"sha256:aa","current":true,"created_at":"2026-10-01T09:00:00Z"`
	// The definitions as a read leaves them, with a record to leave out.
	ver := evals.Version{ID: "ver_0", Owner: "o", Name: "n", Digest: "sha256:old", Current: true}
	cases := []struct {
		kind, body string
		apply      func(c *evals.Client, p *evals.ApplyParams) (digest string, created bool, err error)
		want       string
	}{
		{"metrics", `{"name":"n",` + rec + `,"type":"binary","direction":"higher_is_better"}`,
			func(c *evals.Client, p *evals.ApplyParams) (string, bool, error) {
				a, err := c.ApplyMetric(t.Context(), &evals.Metric{Version: ver, Type: evals.MetricBinary, Direction: evals.DirectionHigherIsBetter, Range: []float64{0, 1}}, p)
				if err != nil {
					return "", false, err
				}
				return a.Version.Digest, a.Created, nil
			}, `{"direction":"higher_is_better","range":[0,1],"type":"binary"}`},
		{"graders", `{"name":"n",` + rec + `,"kind":"check"}`,
			func(c *evals.Client, p *evals.ApplyParams) (string, bool, error) {
				a, err := c.ApplyGrader(t.Context(), &evals.Grader{Version: ver, Kind: evals.GraderCheck, Produces: []string{"sha256:m"}}, p)
				if err != nil {
					return "", false, err
				}
				return a.Version.Digest, a.Created, nil
			}, `{"kind":"check","produces":["sha256:m"]}`},
		{"tasks", `{"name":"n",` + rec + `,"instruction":"x"}`,
			func(c *evals.Client, p *evals.ApplyParams) (string, bool, error) {
				a, err := c.ApplyTask(t.Context(), &evals.Task{Version: ver, Instruction: "x", Checks: []string{"sha256:g"}}, p)
				if err != nil {
					return "", false, err
				}
				return a.Version.Digest, a.Created, nil
			}, `{"checks":["sha256:g"],"instruction":"x"}`},
		{"suites", `{"name":"n",` + rec + `,"tasks":["sha256:t"],"metrics":["sha256:m"],"primary_metric":"sha256:m"}`,
			func(c *evals.Client, p *evals.ApplyParams) (string, bool, error) {
				a, err := c.ApplySuite(t.Context(), &evals.Suite{Version: ver, Tasks: []string{"sha256:t"}, Metrics: []string{"sha256:m"}, PrimaryMetric: "sha256:m"}, p)
				if err != nil {
					return "", false, err
				}
				return a.Version.Digest, a.Created, nil
			}, `{"metrics":["sha256:m"],"primary_metric":"sha256:m","tasks":["sha256:t"]}`},
		{"candidates", `{"name":"n",` + rec + `,"kind":"external"}`,
			func(c *evals.Client, p *evals.ApplyParams) (string, bool, error) {
				a, err := c.ApplyCandidate(t.Context(), &evals.Candidate{Version: ver, Kind: evals.CandidateExternal, Metadata: map[string]string{"a": "b"}}, p)
				if err != nil {
					return "", false, err
				}
				return a.Version.Digest, a.Created, nil
			}, `{"kind":"external","metadata":{"a":"b"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			path := "/v1/evals/" + tc.kind + "/n"
			c, r := newRecorder(t, map[string]reply{"PUT " + path: {status: http.StatusCreated, body: tc.body}})
			digest, created, err := tc.apply(c, nil)
			if err != nil {
				t.Fatal(err)
			}
			got := r.last(t)
			if got.method != http.MethodPut || got.path != path || got.query != "" || got.ifMatch != "" || got.ifNoneMatch != "" || got.body != tc.want {
				t.Errorf("sent %+v\\nwant body %s", got, tc.want)
			}
			if digest != "sha256:aa" || !created {
				t.Errorf("answer digest %q created %v", digest, created)
			}

			// A 200 is a version the content already was.
			c, _ = newRecorder(t, map[string]reply{"PUT " + path: {body: tc.body}})
			if _, created, err := tc.apply(c, nil); err != nil || created {
				t.Errorf("an existing version: created %v, error %v", created, err)
			}

			// The owner and each precondition are sent as the contract names them.
			c, r = newRecorder(t, map[string]reply{"PUT " + path: {body: tc.body}})
			if _, _, err := tc.apply(c, &evals.ApplyParams{Owner: "https://auth.latere.ai#org", IfMatch: "sha256:old"}); err != nil {
				t.Fatal(err)
			}
			if got := r.last(t); got.query != "owner=https%3A%2F%2Fauth.latere.ai%23org" || got.ifMatch != `"sha256:old"` || got.ifNoneMatch != "" || got.body != tc.want {
				t.Errorf("sent %+v", got)
			}
			if _, _, err := tc.apply(c, &evals.ApplyParams{OnlyNew: true}); err != nil {
				t.Fatal(err)
			}
			if got := r.last(t); got.ifMatch != "" || got.ifNoneMatch != "*" {
				t.Errorf("sent %+v", got)
			}

			// A precondition that does not hold matches the 412.
			c, _ = newRecorder(t, map[string]reply{"PUT " + path: {status: http.StatusPreconditionFailed, body: preconditionFailed}})
			_, _, err = tc.apply(c, &evals.ApplyParams{IfMatch: "sha256:old"})
			var apiErr *latere.Error
			if !errors.Is(err, latere.ErrPreconditionFailed) || !errors.As(err, &apiErr) || apiErr.Code != "precondition_failed" {
				t.Errorf("error %v", err)
			}
		})
	}
}

// An apply that no request can answer is refused before it is sent, and a
// quoted digest is sent once quoted.
func TestApplyRefusesWhatNeedsNoRequest(t *testing.T) {
	c, r := newRecorder(t, map[string]reply{"PUT /v1/evals/metrics/n": {body: `{"name":"n","digest":"sha256:aa"}`}})
	ctx := context.Background()
	m := &evals.Metric{Version: evals.Version{Name: "n"}, Type: evals.MetricCount, Direction: evals.DirectionNone}
	if _, err := c.ApplyMetric(ctx, nil, nil); err == nil {
		t.Error("a nil metric was sent")
	}
	if _, err := c.ApplyMetric(ctx, &evals.Metric{}, nil); err == nil {
		t.Error("a metric with no name was sent")
	}
	if _, err := c.ApplyMetric(ctx, &evals.Metric{Version: evals.Version{Name: ".."}}, nil); err == nil {
		t.Error("the name .. was sent")
	}
	if _, err := c.ApplyMetric(ctx, m, &evals.ApplyParams{IfMatch: "sha256:a", OnlyNew: true}); err == nil {
		t.Error("both preconditions were sent")
	}
	if n := len(r.all()); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
	if _, err := c.ApplyMetric(ctx, m, &evals.ApplyParams{IfMatch: `"sha256:a"`}); err != nil {
		t.Fatal(err)
	}
	if got := r.last(t); got.ifMatch != `"sha256:a"` {
		t.Errorf("If-Match %q", got.ifMatch)
	}
}

const canceledJSON = `{"id":"exp_9","owner":"o","suite":"sha256:s","candidates":["sha256:c1"],"trials":3,"mode":"import","state":"canceled",
"created_at":"2026-11-01T09:00:00Z","updated_at":"2026-11-01T09:00:00Z"}`

// Canceling and closing an experiment post to its id and answer the
// experiment, and each refusal matches its sentinel.
func TestAnExperimentIsCanceledAndClosed(t *testing.T) {
	calls := []struct {
		name, suffix string
		call         func(c *evals.Client, id string) (*evals.Experiment, error)
		code         string
	}{
		{"cancel", "/cancel", func(c *evals.Client, id string) (*evals.Experiment, error) {
			return c.CancelExperiment(t.Context(), id)
		}, "experiment_finished"},
		{"close", "/close", func(c *evals.Client, id string) (*evals.Experiment, error) {
			return c.CloseExperiment(t.Context(), id)
		}, "wrong_mode"},
	}
	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			path := "/v1/evals/experiments/exp_9" + tc.suffix
			c, r := newRecorder(t, map[string]reply{
				"POST " + path: {body: canceledJSON},
				"POST /v1/evals/experiments/exp_x" + tc.suffix: {status: http.StatusConflict,
					body: `{"error":{"code":"` + tc.code + `","message":"No.","details":{"retryable":false}}}`},
			})
			e, err := tc.call(c, "exp_9")
			if err != nil {
				t.Fatal(err)
			}
			if e.ID != "exp_9" || e.State != evals.ExperimentCanceled {
				t.Errorf("experiment %+v", e)
			}
			if got := r.last(t); got.method != http.MethodPost || got.path != path || got.query != "" || got.body != "" || got.key != "" {
				t.Errorf("sent %+v", got)
			}
			if _, err := tc.call(c, "exp_none"); !errors.Is(err, latere.ErrNotFound) {
				t.Errorf("a missing experiment: %v", err)
			}
			_, err = tc.call(c, "exp_x")
			var apiErr *latere.Error
			if !errors.Is(err, latere.ErrConflict) || !errors.As(err, &apiErr) || apiErr.Code != tc.code {
				t.Errorf("a conflict: %v", err)
			}
			if _, err := tc.call(c, ""); err == nil {
				t.Error("an empty id was sent")
			}
		})
	}
}
