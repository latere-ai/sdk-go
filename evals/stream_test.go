// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package evals_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"latere.ai/x/sdk-go/evals"
	"latere.ai/x/sdk-go/latere"
)

func streamClient(t *testing.T, h http.HandlerFunc) *evals.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	cfg, err := latere.NewConfig(latere.WithBaseURL(srv.URL), latere.WithTokenSource(latere.StaticToken("tok_test")),
		latere.WithBackoff(time.Millisecond, time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	return evals.New(cfg)
}

// event writes one frame of an experiment's stream, as the API frames it:
// its sequence as the id when it has one, its type as the event, and its
// data.
func event(seq int, typ, data string) string {
	id := ""
	if seq > 0 {
		id = fmt.Sprintf("id: %d\n", seq)
	}
	return fmt.Sprintf("%sevent: %s\ndata: %s\n\n", id, typ, data)
}

func TestTheStreamFollowsAnExperimentToItsEnd(t *testing.T) {
	var mu sync.Mutex
	var resumes []string
	c := streamClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/evals/experiments/exp_1/stream" || r.Header.Get("Accept") != "text/event-stream" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		resumes = append(resumes, r.Header.Get("Last-Event-ID"))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		if r.Header.Get("Last-Event-ID") == "" {
			fmt.Fprint(w, event(4, "snapshot", `{"id":"exp_1","owner":"o","suite":"s","candidates":["c"],"trials":1,"budget":{},"parallelism":1,"reference":{},"mode":"execute","state":"running"}`))
			fmt.Fprint(w, event(5, "cell", `{"id":"cel_1","task":"t","candidate":"c","state":"running"}`))
			return // the connection drops, as it does when its token expires
		}
		fmt.Fprint(w, ": keepalive\n\n")
		fmt.Fprint(w, event(6, "trial", `{"id":"tri_1","cell":"cel_1","index":0,"state":"done","outcome":"completed"}`))
		fmt.Fprint(w, event(7, "graded", `{"trial":"tri_1","grader":"sha256:g","generation":1,"metrics":2}`))
		fmt.Fprint(w, event(8, "comparison", `{"id":"cmp_1","candidate":"c","reference_candidate":"r","automatic":true,"final":true,"declared":0}`))
		fmt.Fprint(w, event(9, "experiment", `{"id":"exp_1","state":"complete","progress":{"cells":{"complete":1}}}`))
		fmt.Fprint(w, event(10, "experiment.completed", `{"id":"exp_1","state":"complete","budget":"evals"}`))
		fmt.Fprint(w, event(11, "bar.future", `{"anything":true}`))
		fmt.Fprint(w, event(0, "end", `{"state":"complete"}`))
		fmt.Fprint(w, event(12, "never.read", `{}`))
	})
	var got []string
	for ev, err := range c.StreamEvents(t.Context(), "exp_1", nil) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, fmt.Sprintf("%d:%s", ev.Seq, ev.Type))
		switch ev.Type {
		case evals.EventSnapshot:
			var e evals.Experiment
			if err := ev.Decode(&e); err != nil || e.State != evals.ExperimentRunning || len(e.RawJSON()) == 0 {
				t.Errorf("snapshot %+v, %v", e, err)
			}
		case evals.EventCell:
			var cell evals.CellChange
			if err := ev.Decode(&cell); err != nil || cell.State != evals.CellRunning {
				t.Errorf("cell %+v, %v", cell, err)
			}
		case evals.EventTrial:
			var tr evals.TrialChange
			if err := ev.Decode(&tr); err != nil || tr.State != evals.TrialDone || tr.Outcome != evals.OutcomeCompleted || tr.Cell != "cel_1" {
				t.Errorf("trial %+v, %v", tr, err)
			}
		case evals.EventGraded:
			var g evals.Grading
			if err := ev.Decode(&g); err != nil || g.Generation != 1 || g.Metrics != 2 {
				t.Errorf("grading %+v, %v", g, err)
			}
		case evals.EventComparison:
			var cm evals.ComparisonChange
			if err := ev.Decode(&cm); err != nil || !cm.Final || cm.Declared == nil || *cm.Declared != 0 {
				t.Errorf("comparison %+v, %v", cm, err)
			}
		case evals.EventExperiment:
			var x evals.ExperimentChange
			if err := ev.Decode(&x); err != nil || x.State != evals.ExperimentComplete || x.Progress.Cells[evals.CellComplete] != 1 {
				t.Errorf("experiment %+v, %v", x, err)
			}
		case evals.EventExperimentCompleted:
			var end evals.ExperimentEnd
			if err := ev.Decode(&end); err != nil || end.State != evals.ExperimentComplete || end.Budget != "evals" {
				t.Errorf("end %+v, %v", end, err)
			}
		case evals.EventEnd:
			var end evals.StreamEnd
			if err := ev.Decode(&end); err != nil || end.State != evals.ExperimentComplete {
				t.Errorf("the stream's end %+v, %v", end, err)
			}
		}
	}
	want := "4:snapshot,5:cell,6:trial,7:graded,8:comparison,9:experiment,10:experiment.completed,11:bar.future,0:end"
	if strings.Join(got, ",") != want {
		t.Errorf("streamed %v, want %s", got, want)
	}
	if strings.Join(resumes, ",") != ",5" {
		t.Errorf("connected with Last-Event-ID %q, want none and then 5", resumes)
	}
}

func TestTheStreamResumesAfterASequence(t *testing.T) {
	c := streamClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Last-Event-ID") != "41" {
			t.Errorf("Last-Event-ID %q", r.Header.Get("Last-Event-ID"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, event(42, "cell", `{"id":"cel_1","task":"t","candidate":"c","state":"complete"}`))
		fmt.Fprint(w, event(0, "end", `{"state":"complete"}`))
	})
	n := 0
	for ev, err := range c.StreamEvents(t.Context(), "exp_1", &evals.StreamEventsParams{AfterSeq: 41}) {
		if err != nil || (ev.Seq != 42 && ev.Type != evals.EventEnd) {
			t.Fatalf("%+v, %v", ev, err)
		}
		if ev.Type != evals.EventEnd {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d events", n)
	}
}

func TestTheStreamEndsWithItsRefusal(t *testing.T) {
	c := streamClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, event(3, "snapshot", `{"id":"exp_1"}`))
		fmt.Fprint(w, event(0, "error", `{"error":{"code":"experiment_not_found","message":"There is no such experiment.","details":{"retryable":false,"detail":"experiment.read was denied while the stream was open"}}}`))
	})
	var last error
	n := 0
	for _, err := range c.StreamEvents(t.Context(), "exp_1", nil) {
		n++
		last = err
	}
	var se *evals.StreamError
	if n != 2 || !errors.As(last, &se) || se.Code != "experiment_not_found" || se.Retryable() || !errors.Is(last, latere.ErrNotFound) ||
		!strings.Contains(last.Error(), "There is no such experiment.") {
		t.Errorf("%d items, last %v", n, last)
	}

	retry := &evals.StreamError{Code: "unavailable", Details: map[string]any{"retryable": true}}
	if !retry.Retryable() || errors.Is(retry, latere.ErrNotFound) {
		t.Errorf("an unavailable stream: retryable %v", retry.Retryable())
	}
}

func TestTheStreamReportsWhatItCannotRead(t *testing.T) {
	for name, body := range map[string]string{
		"an id that is no sequence":    "id: x\nevent: cell\ndata: {}\n\n",
		"data that is no JSON":         event(2, "cell", `{broken`),
		"an error that is no envelope": event(0, "error", `oops`),
	} {
		c := streamClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, body)
		})
		var last error
		for _, err := range c.StreamEvents(t.Context(), "exp_1", nil) {
			last = err
		}
		if last == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestTheStreamOfAnUnknownExperimentIsNotFound(t *testing.T) {
	c := streamClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":{"code":"experiment_not_found","message":"There is no such experiment.","details":{"retryable":false}}}`)
	})
	for _, err := range c.StreamEvents(t.Context(), "exp_gone", nil) {
		if !errors.Is(err, latere.ErrNotFound) {
			t.Errorf("%v", err)
		}
	}
}

func TestDecodeNamesTheEvent(t *testing.T) {
	ev := &evals.StreamEvent{Seq: 9, Type: evals.EventCell, Data: []byte(`[]`)}
	var cell evals.CellChange
	if err := ev.Decode(&cell); err == nil || !strings.Contains(err.Error(), "cell event 9") {
		t.Errorf("Decode: %v", err)
	}
}
