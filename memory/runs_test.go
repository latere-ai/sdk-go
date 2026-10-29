// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package memory_test

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"latere.ai/x/sdk-go/latere"
	"latere.ai/x/sdk-go/memory"
)

const (
	runID     = "ses_01JA5C2M8Q4R6T8V0W2X4Y6Z8A"
	triggerID = "trg_01JA5B9K3M5P7R9T1V3W5X7Y9Z"
	// listed is a run of a project's memory as the list answers it, with a
	// field the package does not model.
	listed = `{"id":"` + runID + `","trigger_id":"` + triggerID + `","kind":"memory","scope":"project","project":"` + projectID + `",
"state":"partly_applied","review":"apply","counts":{"added":1,"updated":1,"removed":1,"waiting":1},
"created_at":"2026-10-30T03:00:04Z","ended_at":"2026-10-30T03:02:41Z","expires_at":"2026-11-06T03:02:41Z","added_later":1}`
	// withChanges is a run of You as a read of it answers.
	withChanges = `{"id":"` + runID + `","trigger_id":"` + triggerID + `","kind":"memory","scope":"you","state":"partly_applied","review":"apply",
"counts":{"added":0,"updated":1,"removed":1,"waiting":1},"created_at":"2026-10-30T03:00:04Z","ended_at":"2026-10-30T03:02:41Z",
"expires_at":"2026-11-06T03:02:41Z","changes":[
{"id":"short-lists","kind":"changed","state":"applied","name":"Short lists","description":"Keep lists under five things, and lead with the answer.",
"before":"---\nname: Short lists\n---\n","after":"---\nname: Short lists\ndescription: Keep lists under five things, and lead with the answer.\n---\n"},
{"id":"trains","kind":"removed","state":"waiting","held":"yours","name":"Trains","description":"Likes trains.","before":"---\nname: Trains\n---\n"}]}`
	// decided is a run after a decision.
	decided = `{"id":"` + runID + `","trigger_id":"` + triggerID + `","kind":"memory","scope":"you","state":"applied","review":"apply",
"counts":{"added":0,"updated":1,"removed":1,"waiting":0},"created_at":"2026-10-30T03:00:04Z","ended_at":"2026-10-30T03:02:41Z",
"decided_at":"2026-10-30T08:15:00Z","changes":[{"id":"trains","kind":"removed","state":"applied","name":"Trains","description":"Likes trains.","before":"---\n"}]}`
	// undone is the answer of an undo, a change of which was left as it was.
	undone = `{"id":"` + runID + `","trigger_id":"` + triggerID + `","kind":"memory","scope":"you","state":"partly_applied","review":"apply",
"counts":{"added":0,"updated":0,"removed":1,"waiting":0},"created_at":"2026-10-30T03:00:04Z","ended_at":"2026-10-30T03:02:41Z",
"decided_at":"2026-10-30T03:02:41Z","changes":[
{"id":"short-lists","kind":"changed","state":"undone","name":"Short lists","description":"Keep lists under five things.","before":"---\n","after":"---\n"},
{"id":"tea","kind":"created","state":"applied","name":"Tea","description":"Drinks green tea.","after":"---\n"}],"moved_on":["tea"]}`
)

func TestListRuns(t *testing.T) {
	c, calls := fakeAPI(t, 0, func(w http.ResponseWriter, r *http.Request, _ int) {
		answer(w, http.StatusOK, `{"runs":[`+listed+`],"added_later":true}`)
	})
	l, err := c.ListRuns(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got := calls(); len(got) != 1 || got[0].method != http.MethodGet || got[0].path != "/v1/account/runs" {
		t.Errorf("sent %+v", got)
	}
	if len(l.Runs) != 1 || !strings.Contains(string(l.RawJSON()), `"added_later":true`) {
		t.Fatalf("list %+v with %s", l, l.RawJSON())
	}
	r := l.Runs[0]
	if r.ID != runID || r.TriggerID != triggerID || r.Kind != memory.RunMemory || r.Scope != memory.ScopeProject || r.Project != projectID ||
		r.State != memory.RunPartlyApplied || r.Review != memory.ReviewApply || r.Changes != nil ||
		r.Counts != (memory.RunCounts{Added: 1, Updated: 1, Removed: 1, Waiting: 1}) {
		t.Errorf("run %+v", r)
	}
	if !r.CreatedAt.Equal(time.Date(2026, 10, 30, 3, 0, 4, 0, time.UTC)) || !r.EndedAt.Equal(time.Date(2026, 10, 30, 3, 2, 41, 0, time.UTC)) ||
		!r.DecidedAt.IsZero() || !r.ExpiresAt.Equal(time.Date(2026, 11, 6, 3, 2, 41, 0, time.UTC)) {
		t.Errorf("the run's times: %+v", r)
	}
	if string(r.RawJSON()) != listed {
		t.Errorf("the run kept %s", r.RawJSON())
	}
}

func TestGetRun(t *testing.T) {
	c, calls := fakeAPI(t, 0, func(w http.ResponseWriter, r *http.Request, _ int) { answer(w, http.StatusOK, withChanges) })
	r, err := c.GetRun(t.Context(), "ses_1/x")
	if err != nil {
		t.Fatal(err)
	}
	if got := calls(); len(got) != 1 || got[0].method != http.MethodGet || got[0].path != "/v1/account/runs/ses_1%2Fx" {
		t.Errorf("sent %+v; the id is escaped", got)
	}
	if r.Scope != memory.ScopeYou || r.Project != "" || len(r.Changes) != 2 || string(r.RawJSON()) != withChanges {
		t.Fatalf("run %+v", r)
	}
	changed, removed := r.Changes[0], r.Changes[1]
	if changed.ID != "short-lists" || changed.Kind != memory.ChangeChanged || changed.State != memory.ChangeApplied || changed.Held != "" ||
		changed.Name != "Short lists" || !strings.HasPrefix(changed.Before, "---\n") || !strings.Contains(changed.After, "lead with the answer") {
		t.Errorf("the change %+v", changed)
	}
	if removed.Kind != memory.ChangeRemoved || removed.State != memory.ChangeWaiting || removed.Held != memory.HeldYours ||
		removed.Description != "Likes trains." || removed.Before == "" || removed.After != "" {
		t.Errorf("the removal %+v", removed)
	}
}

// An apply and a decline send the changes they name, an empty object when
// they name none, and are sent once: a repeat after a server error could
// be refused for what the first did.
func TestApplyAndDeclineRun(t *testing.T) {
	for action, decide := range map[string]func(*memory.Client, *memory.RunDecisionParams) (*memory.Run, error){
		"apply": func(c *memory.Client, p *memory.RunDecisionParams) (*memory.Run, error) {
			return c.ApplyRun(t.Context(), runID, p)
		},
		"decline": func(c *memory.Client, p *memory.RunDecisionParams) (*memory.Run, error) {
			return c.DeclineRun(t.Context(), runID, p)
		},
	} {
		c, calls := fakeAPI(t, 2, func(w http.ResponseWriter, r *http.Request, n int) {
			if n == 0 {
				refuse(w, http.StatusServiceUnavailable, "storage_unavailable")
				return
			}
			answer(w, http.StatusOK, decided)
		})
		if _, err := decide(c, nil); !errors.Is(err, memory.ErrStorageUnavailable) {
			t.Errorf("%s: a 503 answered %v", action, err)
		}
		if n := len(calls()); n != 1 {
			t.Errorf("%s was sent %d times; it is sent once", action, n)
		}
		r, err := decide(c, &memory.RunDecisionParams{Changes: []string{"trains"}})
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		if _, err := decide(c, &memory.RunDecisionParams{}); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		got := calls()
		if len(got) != 3 || got[1].method != http.MethodPost || got[1].path != "/v1/account/runs/"+runID+"/"+action {
			t.Fatalf("%s sent %+v", action, got)
		}
		if names, ok := got[1].body["changes"].([]any); !ok || len(names) != 1 || names[0] != "trains" || len(got[1].body) != 1 {
			t.Errorf("%s of one change sent %v", action, got[1].body)
		}
		for _, i := range []int{0, 2} {
			if b := got[i].body; b == nil || len(b) != 0 {
				t.Errorf("%s of every change sent %v; want {}", action, b)
			}
		}
		if r.State != memory.RunApplied || !r.DecidedAt.Equal(time.Date(2026, 10, 30, 8, 15, 0, 0, time.UTC)) || len(r.Changes) != 1 ||
			r.Changes[0].State != memory.ChangeApplied || string(r.RawJSON()) != decided {
			t.Errorf("%s answered %+v", action, r)
		}
	}
}

// An undo is retried, since its repeat undoes nothing more, and its answer
// is the run with what the undo left as it was.
func TestUndoRun(t *testing.T) {
	c, calls := fakeAPI(t, 1, func(w http.ResponseWriter, r *http.Request, n int) {
		if n == 0 {
			refuse(w, http.StatusServiceUnavailable, "storage_unavailable")
			return
		}
		answer(w, http.StatusOK, undone)
	})
	u, err := c.UndoRun(t.Context(), runID, &memory.RunDecisionParams{Changes: []string{"short-lists", "tea"}})
	if err != nil {
		t.Fatal(err)
	}
	got := calls()
	if len(got) != 2 || got[1].method != http.MethodPost || got[1].path != "/v1/account/runs/"+runID+"/undo" {
		t.Fatalf("sent %+v; an undo is retried", got)
	}
	if names, ok := got[1].body["changes"].([]any); !ok || len(names) != 2 || names[1] != "tea" {
		t.Errorf("sent %v", got[1].body)
	}
	if u.ID != runID || u.State != memory.RunPartlyApplied || len(u.Changes) != 2 || u.Changes[0].State != memory.ChangeUndone ||
		u.Changes[1].Kind != memory.ChangeCreated || u.Changes[1].Before != "" || !slices.Equal(u.MovedOn, []string{"tea"}) {
		t.Errorf("undone %+v", u)
	}
	if string(u.RawJSON()) != undone || string(u.Run.RawJSON()) != undone {
		t.Errorf("the undo kept %s", u.RawJSON())
	}
	if _, err := c.UndoRun(t.Context(), runID, nil); err != nil {
		t.Fatal(err)
	}
	if b := calls()[2].body; b == nil || len(b) != 0 {
		t.Errorf("an undo of every change sent %v; want {}", b)
	}
}

// A call that names no run is refused before any request.
func TestARunWithNoID(t *testing.T) {
	c, calls := fakeAPI(t, 0, func(w http.ResponseWriter, r *http.Request, _ int) { answer(w, http.StatusOK, decided) })
	ctx := t.Context()
	for name, call := range map[string]func() error{
		"GetRun":     func() error { _, err := c.GetRun(ctx, ""); return err },
		"ApplyRun":   func() error { _, err := c.ApplyRun(ctx, "", nil); return err },
		"DeclineRun": func() error { _, err := c.DeclineRun(ctx, "", nil); return err },
		"UndoRun":    func() error { _, err := c.UndoRun(ctx, "", nil); return err },
	} {
		if err := call(); err == nil {
			t.Errorf("%s of no run succeeded", name)
		}
	}
	if got := calls(); len(got) != 0 {
		t.Errorf("sent %+v", got)
	}
}

// A decision's refusals each match their sentinel and the sentinel of
// their status.
func TestTheRefusalsOfADecision(t *testing.T) {
	for _, c := range []struct {
		status   int
		code     string
		sentinel error
		status2  error
	}{
		{http.StatusNotFound, "run_not_found", memory.ErrRunNotFound, latere.ErrNotFound},
		{http.StatusConflict, "run_running", memory.ErrRunRunning, latere.ErrConflict},
		{http.StatusConflict, "run_not_waiting", memory.ErrRunNotWaiting, latere.ErrConflict},
		{http.StatusConflict, "run_busy", memory.ErrRunBusy, latere.ErrConflict},
		{http.StatusBadRequest, "run_change_unknown", memory.ErrRunChangeUnknown, nil},
		{http.StatusServiceUnavailable, "runs_unavailable", memory.ErrRunsUnavailable, nil},
	} {
		cl, calls := fakeAPI(t, 0, func(w http.ResponseWriter, r *http.Request, _ int) { refuse(w, c.status, c.code) })
		_, err := cl.ApplyRun(t.Context(), runID, &memory.RunDecisionParams{Changes: []string{"trains"}})
		if !errors.Is(err, c.sentinel) || (c.status2 != nil && !errors.Is(err, c.status2)) || errors.Is(err, memory.ErrNotFound) {
			t.Errorf("%d %s: %v", c.status, c.code, err)
		}
		if n := len(calls()); n != 1 {
			t.Errorf("%s: sent %d times", c.code, n)
		}
	}
}
