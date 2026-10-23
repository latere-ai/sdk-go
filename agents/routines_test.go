// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"latere.ai/x/sdk-go/agents"
	"latere.ai/x/sdk-go/latere"
)

// chat is the agent whose routines these tests make, Latere Chat's.
const chat = "latere-chat"

// toposCall is a request the stand-in for the trigger API received.
type toposCall struct {
	Method, Path, Query, Key string
	Body                     []byte
}

// refusal is an answer the stand-in gives in place of its own.
type refusal struct {
	status int
	body   string
}

// topos stands in for the trigger API: it keeps each trigger as it was
// applied, answers it with a status of its own, lists them newest first,
// and fires them once per key.
type topos struct {
	t        *testing.T
	mu       sync.Mutex
	triggers map[string]map[string]any
	created  []string
	firings  map[string]string
	refuse   map[string]refusal
	calls    []toposCall
	n        int
}

const triggersPath = "/v1/agents/triggers"

func newTopos(t *testing.T) (*topos, *agents.Client) {
	t.Helper()
	tp := &topos{t: t, triggers: map[string]map[string]any{}, firings: map[string]string{}, refuse: map[string]refusal{}}
	srv := httptest.NewServer(http.HandlerFunc(tp.serve))
	t.Cleanup(srv.Close)
	cfg, err := latere.NewConfig(latere.WithBaseURL(srv.URL), latere.WithTokenSource(latere.StaticToken("key_test")), latere.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	return tp, agents.New(cfg)
}

// lines returns the requests received since it was last called, as
// "METHOD path".
func (tp *topos) lines() []string {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	out := []string{}
	for _, c := range tp.calls {
		out = append(out, c.Method+" "+c.Path)
	}
	tp.calls = nil
	return out
}

// received returns the requests received since lines was last called.
func (tp *topos) received() []toposCall {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	return slices.Clone(tp.calls)
}

// manifest is the body of the last apply of the trigger name, as sent.
func (tp *topos) manifest(name string) map[string]any {
	tp.t.Helper()
	tp.mu.Lock()
	defer tp.mu.Unlock()
	for i := len(tp.calls) - 1; i >= 0; i-- {
		if c := tp.calls[i]; c.Method == http.MethodPut && c.Path == triggersPath+"/"+name {
			var m map[string]any
			if err := json.Unmarshal(c.Body, &m); err != nil {
				tp.t.Fatal(err)
			}
			return m
		}
	}
	tp.t.Fatalf("no apply of %s was received", name)
	return nil
}

// put keeps a trigger as if it had been applied another way.
func (tp *topos) put(name string, manifest string) {
	tp.t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(manifest), &m); err != nil {
		tp.t.Fatal(err)
	}
	tp.mu.Lock()
	defer tp.mu.Unlock()
	tp.store(name, m)
}

// edit changes a kept trigger as a change made another way would.
func (tp *topos) edit(name string, change func(spec, meta map[string]any)) {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	m := tp.triggers[name]
	change(m["spec"].(map[string]any), m["metadata"].(map[string]any))
}

// store keeps m under name with the status the API writes. The caller
// holds the lock.
func (tp *topos) store(name string, m map[string]any) {
	status := map[string]any{}
	if old, ok := tp.triggers[name]; ok {
		status = old["status"].(map[string]any)
	} else {
		tp.n++
		status["id"] = fmt.Sprintf("trg_%d", tp.n)
		status["createdAt"] = time.Date(2026, 10, 1, 0, 0, tp.n, 0, time.UTC).Format(time.RFC3339)
		tp.created = append(tp.created, name)
	}
	status["version"] = 1
	spec, _ := m["spec"].(map[string]any)
	delete(status, "nextFireAt")
	if spec != nil && spec["schedule"] != nil && spec["suspend"] != true {
		status["nextFireAt"] = "2026-10-26T07:00:00Z"
	}
	m["status"] = status
	tp.triggers[name] = m
}

func (tp *topos) write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		tp.t.Error(err)
	}
}

func (tp *topos) serve(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		tp.t.Error(err)
	}
	tp.mu.Lock()
	defer tp.mu.Unlock()
	tp.calls = append(tp.calls, toposCall{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Key: r.Header.Get("Idempotency-Key"), Body: body})
	if f, ok := tp.refuse[r.Method+" "+r.URL.Path]; ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.status)
		_, _ = io.WriteString(w, f.body)
		return
	}
	notFound := func() {
		tp.write(w, http.StatusNotFound, map[string]any{"error": map[string]any{"code": "not_found", "message": "No such trigger."}})
	}
	rest, isTrigger := strings.CutPrefix(r.URL.Path, triggersPath+"/")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == triggersPath:
		limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
		if err != nil {
			limit = 20
		}
		from := 0
		if c := r.URL.Query().Get("cursor"); c != "" {
			if from, err = strconv.Atoi(c); err != nil {
				tp.t.Errorf("a cursor of another page: %q", c)
			}
		}
		newest := slices.Clone(tp.created)
		slices.Reverse(newest)
		page := map[string]any{"items": []any{}}
		items := []any{}
		for i := from; i < len(newest) && i < from+limit; i++ {
			items = append(items, tp.triggers[newest[i]])
		}
		page["items"] = items
		if from+limit < len(newest) {
			page["next_cursor"] = strconv.Itoa(from + limit)
		}
		tp.write(w, http.StatusOK, page)
	case r.Method == http.MethodPut && isTrigger:
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			tp.t.Errorf("an apply that is no JSON object: %s", body)
		}
		tp.store(rest, m)
		tp.write(w, http.StatusOK, tp.triggers[rest])
	case r.Method == http.MethodGet && isTrigger:
		m, ok := tp.triggers[rest]
		if !ok {
			notFound()
			return
		}
		tp.write(w, http.StatusOK, m)
	case r.Method == http.MethodDelete && isTrigger:
		if _, ok := tp.triggers[rest]; !ok {
			notFound()
			return
		}
		delete(tp.triggers, rest)
		tp.created = slices.DeleteFunc(tp.created, func(n string) bool { return n == rest })
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && strings.HasSuffix(rest, "/fire"):
		name := strings.TrimSuffix(rest, "/fire")
		m, ok := tp.triggers[name]
		if !ok {
			notFound()
			return
		}
		if m["spec"].(map[string]any)["suspend"] == true {
			tp.write(w, http.StatusConflict, map[string]any{"error": map[string]any{"code": "conflict", "message": "The trigger is suspended."}})
			return
		}
		key := r.Header.Get("Idempotency-Key")
		if f, ok := tp.firings[key]; ok {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, f)
			return
		}
		tp.n++
		status := m["status"].(map[string]any)
		session := fmt.Sprintf("ses_%d", tp.n)
		status["lastFiredAt"], status["lastSessionId"] = "2026-10-21T09:00:00Z", session
		f := fmt.Sprintf(`{"id":"frg_%d","trigger_id":%q,"origin":"fire","key":%q,"outcome":"started","session_id":%q,"received_at":"2026-10-21T09:00:00Z"}`, tp.n, status["id"], key, session)
		tp.firings[key] = f
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, f)
	default:
		notFound()
	}
}

// weekly is the routine of Latere Chat's own example: every Monday and
// Thursday at 9:00, Berlin time.
func weekly() *agents.CreateRoutineParams {
	return &agents.CreateRoutineParams{
		Agent:       chat,
		Title:       "Weekly news",
		Instruction: "Summarize this week's news on climate policy and city cycling.",
		Schedule:    &agents.RoutineSchedule{Days: []time.Weekday{time.Thursday, time.Monday}, Hour: 9, Zone: "Europe/Berlin"},
	}
}

// create makes a routine with key and fails the test on an error.
func create(t *testing.T, c *agents.Client, p *agents.CreateRoutineParams, key string) *agents.Routine {
	t.Helper()
	p.IdempotencyKey = key
	r, err := c.CreateRoutine(t.Context(), p)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return r
}

// sameJSON reports whether got is the JSON want holds.
func sameJSON(t *testing.T, got map[string]any, want string) bool {
	t.Helper()
	var w map[string]any
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(got, w)
}

// The manifest of each kind of routine is the one Latere Chat writes for
// it, field for field: its cases are these.
func TestTheManifestIsTheOneLatereChatWrites(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params func() *agents.CreateRoutineParams
		want   string
	}{
		{"a routine on two days", func() *agents.CreateRoutineParams { return weekly() },
			`{"apiVersion":"topos.latere.ai/v1","kind":"Trigger","metadata":{"name":"NAME","displayName":"Weekly news","labels":{"applied-by":"latere-chat"}},
			"spec":{"agent":"latere-chat","schedule":"0 9 * * 1,4","timeZone":"Europe/Berlin","session":{"message":"Summarize this week's news on climate policy and city cycling.","title":"Weekly news"},"skipIfActive":false}}`},
		{"literal text", func() *agents.CreateRoutineParams {
			p := weekly()
			p.Title, p.Instruction = "Notes {{weekly}}", `Fill {{event.id}} and {{{ and a written \{{ into the notes.`
			return p
		}, `{"apiVersion":"topos.latere.ai/v1","kind":"Trigger","metadata":{"name":"NAME","displayName":"Notes {{weekly}}","labels":{"applied-by":"latere-chat"}},
			"spec":{"agent":"latere-chat","schedule":"0 9 * * 1,4","timeZone":"Europe/Berlin","session":{"message":"Fill \\{{event.id}} and \\{{{ and a written \\\\{{ into the notes.","title":"Notes \\{{weekly}}"},"skipIfActive":false}}`},
		{"a routine from another app", func() *agents.CreateRoutineParams {
			return &agents.CreateRoutineParams{Agent: chat, Title: "Contact form", Instruction: "Draft a reply to it. {{name}} is theirs.", Starts: agents.StartsFromLink}
		}, `{"apiVersion":"topos.latere.ai/v1","kind":"Trigger","metadata":{"name":"NAME","displayName":"Contact form","labels":{"applied-by":"latere-chat"}},
			"spec":{"agent":"latere-chat","on":{"product":"webhook","verbs":["received"]},"session":{"message":"Draft a reply to it. \\{{name}} is theirs.\n\nWhat the other app sent:\n{{event.payload.body}}","title":"Contact form"},"skipIfActive":false}}`},
		{"a routine from an email", func() *agents.CreateRoutineParams {
			return &agents.CreateRoutineParams{Agent: chat, Title: "Receipts", Instruction: "Add it to my list.", Starts: agents.StartsFromEmail}
		}, `{"apiVersion":"topos.latere.ai/v1","kind":"Trigger","metadata":{"name":"NAME","displayName":"Receipts","labels":{"applied-by":"latere-chat"}},
			"spec":{"agent":"latere-chat","on":{"product":"mail","verbs":["received"]},"session":{"message":"Add it to my list.\n\nThe email that started this run:\nFrom: {{event.actor}}\nSubject: {{event.payload.subject}}\n\n{{event.payload.text}}\n\nAttachments: {{event.payload.attachments}}","title":"Receipts"},"skipIfActive":false}}`},
		{"a paused routine, trimmed", func() *agents.CreateRoutineParams {
			p := weekly()
			p.Title, p.Instruction, p.Paused = "  Weekly news ", "\n Summarize this week's news on climate policy and city cycling. \n", true
			return p
		}, `{"apiVersion":"topos.latere.ai/v1","kind":"Trigger","metadata":{"name":"NAME","displayName":"Weekly news","labels":{"applied-by":"latere-chat"}},
			"spec":{"agent":"latere-chat","schedule":"0 9 * * 1,4","timeZone":"Europe/Berlin","session":{"message":"Summarize this week's news on climate policy and city cycling.","title":"Weekly news"},"skipIfActive":false,"suspend":true}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tp, c := newTopos(t)
			r := create(t, c, tc.params(), "key-1")
			if got := tp.manifest(r.Name); !sameJSON(t, got, strings.ReplaceAll(tc.want, "NAME", r.Name)) {
				b, _ := json.Marshal(got)
				t.Errorf("the manifest is\n%s\nwant\n%s", b, tc.want)
			}
		})
	}
}

// A routine is one apply of one trigger, named by its key: the same key
// applies the same name again, and another key another.
func TestARoutineIsOneTriggerNamedByItsKey(t *testing.T) {
	tp, c := newTopos(t)
	got := create(t, c, weekly(), "key-1")
	if lines := tp.lines(); len(lines) != 1 || lines[0] != "PUT "+triggersPath+"/"+got.Name {
		t.Fatalf("the API saw %v, want one apply", lines)
	}
	if got.Name != agents.RoutineName("key-1") || len(got.Name) != len(agents.RoutinePrefix)+20 {
		t.Errorf("the routine is named %q", got.Name)
	}
	if got.Title != "Weekly news" || got.Instruction != "Summarize this week's news on climate policy and city cycling." || got.Paused ||
		got.Agent != chat || got.Starts != agents.StartsOnSchedule || got.Schedule == nil ||
		!reflect.DeepEqual(got.Schedule.Days, []time.Weekday{time.Monday, time.Thursday}) || got.Schedule.Hour != 9 || got.Schedule.Minute != 0 || got.Schedule.Zone != "Europe/Berlin" {
		t.Errorf("the routine reads %+v", got)
	}
	if !strings.HasPrefix(got.TriggerID, "trg_") || got.NextRunAt == nil || got.CreatedAt.IsZero() || got.LastRunAt != nil {
		t.Errorf("the routine's record reads %+v", got)
	}
	var raw map[string]any
	if err := json.Unmarshal(got.RawJSON(), &raw); err != nil || raw["status"] == nil {
		t.Errorf("RawJSON = %s; want the trigger as the API answered it", got.RawJSON())
	}
	if (&agents.Routine{}).RawJSON() != nil {
		t.Error("a routine the program built has JSON")
	}

	again := create(t, c, weekly(), "key-1")
	if again.Name != got.Name || again.TriggerID != got.TriggerID || len(tp.triggers) != 1 {
		t.Errorf("a retry made %s (%s), want %s; %d triggers", again.Name, again.TriggerID, got.Name, len(tp.triggers))
	}
	if other := create(t, c, weekly(), "key-2"); other.Name == got.Name {
		t.Errorf("another key made the same routine %s", other.Name)
	}
	// With no key, one is made, and each create makes its own routine.
	a, err := c.CreateRoutine(t.Context(), weekly())
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.CreateRoutine(t.Context(), weekly())
	if err != nil || a.Name == b.Name {
		t.Errorf("two creates without a key made %s and %s, %v", a.Name, b.Name, err)
	}
}

// Every day writes the hour and minute of every day; chosen days write
// their numbers in ascending order, Sunday 0; each reads back as the days
// in week order and the time it was written at.
func TestTheScheduleIsWrittenAndReadBack(t *testing.T) {
	all := []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday}
	for _, tc := range []struct {
		days         []time.Weekday
		hour, minute int
		cron         string
		read         []time.Weekday
	}{
		{[]time.Weekday{time.Sunday, time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday}, 7, 0, "0 7 * * *", all},
		{[]time.Weekday{time.Sunday, time.Saturday}, 10, 30, "30 10 * * 0,6", []time.Weekday{time.Saturday, time.Sunday}},
		{[]time.Weekday{time.Friday, time.Monday, time.Wednesday, time.Tuesday, time.Thursday}, 8, 5, "5 8 * * 1,2,3,4,5", []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday}},
		{[]time.Weekday{time.Friday}, 17, 0, "0 17 * * 5", []time.Weekday{time.Friday}},
		{[]time.Weekday{time.Monday}, 0, 0, "0 0 * * 1", []time.Weekday{time.Monday}},
		{[]time.Weekday{time.Sunday, time.Sunday}, 23, 59, "59 23 * * 0", []time.Weekday{time.Sunday}},
	} {
		t.Run(tc.cron, func(t *testing.T) {
			tp, c := newTopos(t)
			p := weekly()
			p.Schedule = &agents.RoutineSchedule{Days: tc.days, Hour: tc.hour, Minute: tc.minute, Zone: "America/New_York"}
			got := create(t, c, p, "k")
			spec := tp.manifest(got.Name)["spec"].(map[string]any)
			if spec["schedule"] != tc.cron || spec["timeZone"] != "America/New_York" {
				t.Errorf("wrote %v in %v, want %q", spec["schedule"], spec["timeZone"], tc.cron)
			}
			if s := got.Schedule; s == nil || !reflect.DeepEqual(s.Days, tc.read) || s.Hour != tc.hour || s.Minute != tc.minute {
				t.Errorf("reads back as %+v, want %v at %d:%02d", s, tc.read, tc.hour, tc.minute)
			}
		})
	}
}

// A schedule of a shape a routine does not write, set another way, reads
// as none, and a change that names no schedule keeps it; a schedule whose
// minute or hour is past the clock is none either, and a trigger with no
// zone reads in UTC.
func TestAScheduleSetElsewhereIsKept(t *testing.T) {
	tp, c := newTopos(t)
	r := create(t, c, weekly(), "k")
	tp.edit(r.Name, func(spec, _ map[string]any) { spec["schedule"] = "*/15 9-17 * * 1-5" })
	got, err := c.GetRoutine(t.Context(), chat, r.Name)
	if err != nil || got.Schedule != nil {
		t.Fatalf("the routine reads %+v, %v; want no schedule", got, err)
	}
	title := "Work hours"
	if _, err := c.ChangeRoutine(t.Context(), chat, r.Name, &agents.ChangeRoutineParams{Title: &title}); err != nil {
		t.Fatal(err)
	}
	if spec := tp.manifest(r.Name)["spec"].(map[string]any); spec["schedule"] != "*/15 9-17 * * 1-5" || spec["timeZone"] != "Europe/Berlin" {
		t.Errorf("the change wrote the schedule %v in %v, want the stored one kept", spec["schedule"], spec["timeZone"])
	}
	changed, err := c.ChangeRoutine(t.Context(), chat, r.Name, &agents.ChangeRoutineParams{Schedule: &agents.RoutineSchedule{Days: []time.Weekday{time.Saturday}, Hour: 11, Zone: "Europe/Berlin"}})
	if err != nil || changed.Schedule == nil || changed.Schedule.Hour != 11 {
		t.Errorf("a new schedule reads %+v, %v", changed, err)
	}

	for _, cron := range []string{"75 9 * * 1", "0 24 * * 1"} {
		tp.edit(r.Name, func(spec, _ map[string]any) { spec["schedule"] = cron })
		if got, err := c.GetRoutine(t.Context(), chat, r.Name); err != nil || got.Schedule != nil {
			t.Errorf("%s reads %+v, %v", cron, got, err)
		}
	}
	tp.edit(r.Name, func(spec, _ map[string]any) {
		spec["schedule"] = "0 6 * * 7"
		delete(spec, "timeZone")
	})
	if got, err := c.GetRoutine(t.Context(), chat, r.Name); err != nil || got.Schedule == nil || got.Schedule.Zone != "UTC" ||
		!reflect.DeepEqual(got.Schedule.Days, []time.Weekday{time.Sunday}) {
		t.Errorf("Sunday as 7 with no zone reads %+v, %v", got.Schedule, err)
	}
}

// What a person writes is never read as a placeholder: it reads back as
// written, and a title read from a trigger with no display name is its
// session's title, unescaped.
func TestARoutineReadsBackAsWritten(t *testing.T) {
	tp, c := newTopos(t)
	p := weekly()
	p.Title, p.Instruction = "Notes {{weekly}}", `Fill {{event.id}} and {{{ and a written \{{ into the notes.`
	got := create(t, c, p, "k")
	if got.Instruction != p.Instruction || got.Title != p.Title {
		t.Errorf("reads back as %q, %q", got.Title, got.Instruction)
	}
	tp.edit(got.Name, func(_, meta map[string]any) { delete(meta, "displayName") })
	if r, err := c.GetRoutine(t.Context(), chat, got.Name); err != nil || r.Title != "Notes {{weekly}}" {
		t.Errorf("without a display name it reads %+v, %v", r, err)
	}
}

// The list is the agent's routines and nothing else: a trigger made
// another way, one under a routine's name without the label, and another
// agent's routine are left out; every page is read, 100 triggers a page,
// newest first.
func TestTheListHoldsTheRoutinesOfEveryPage(t *testing.T) {
	tp, c := newTopos(t)
	tp.put("nightly-build", `{"metadata":{"name":"nightly-build","labels":{"team":"web"}},"spec":{"agent":"agent_x","schedule":"0 2 * * *","session":{"message":"Build"}}}`)
	tp.put("routine-ffffffffffffffffffff", `{"metadata":{"name":"routine-ffffffffffffffffffff"},"spec":{"agent":"agent_x","schedule":"0 2 * * *","session":{"message":"Not a routine"}}}`)
	tp.put("routine-eeeeeeeeeeeeeeeeeeee", `{"metadata":{"name":"routine-eeeeeeeeeeeeeeeeeeee","labels":{"applied-by":"another"}},"spec":{"agent":"another","schedule":"0 2 * * *","session":{"message":"Another's"}}}`)
	for i := range 130 {
		p := weekly()
		p.Title = fmt.Sprintf("Routine %d", i)
		create(t, c, p, fmt.Sprintf("k%d", i))
	}
	tp.lines()
	var list []*agents.Routine
	for r, err := range c.ListRoutines(t.Context(), &agents.ListRoutinesParams{Agent: chat}) {
		if err != nil {
			t.Fatal(err)
		}
		list = append(list, r)
	}
	if len(list) != 130 {
		t.Fatalf("the list holds %d routines, want 130", len(list))
	}
	if list[0].Title != "Routine 129" || list[129].Title != "Routine 0" {
		t.Errorf("the list runs from %q to %q, want the newest first", list[0].Title, list[129].Title)
	}
	calls := tp.received()
	if lines := tp.lines(); !reflect.DeepEqual(lines, []string{"GET " + triggersPath, "GET " + triggersPath}) {
		t.Errorf("the API saw %v, want two pages read", lines)
	}
	for _, call := range calls {
		if !strings.Contains(call.Query, "limit=100") {
			t.Errorf("a page asked %q", call.Query)
		}
	}

	page, err := c.ListRoutinesPage(t.Context(), &agents.ListRoutinesParams{Agent: chat, Limit: 120})
	if err != nil || len(page.Items) != 120 || page.NextCursor == "" {
		t.Fatalf("the first page holds %d, next %q, %v", len(page.Items), page.NextCursor, err)
	}
	last, err := c.ListRoutinesPage(t.Context(), &agents.ListRoutinesParams{Agent: chat, Limit: 120, Cursor: page.NextCursor})
	if err != nil || len(last.Items) != 10 || last.NextCursor != "" {
		t.Errorf("the last page holds %d of the 13 triggers left, next %q, %v", len(last.Items), last.NextCursor, err)
	}
	for _, bad := range []*agents.ListRoutinesParams{nil, {}} {
		if _, err := c.ListRoutinesPage(t.Context(), bad); err == nil {
			t.Errorf("listed the routines of no agent with %+v", bad)
		}
	}
	if lines := tp.lines(); len(lines) != 2 {
		t.Errorf("the API saw %v, want two pages and nothing for a list of no agent", lines)
	}
}

// A change writes only what it names over the stored trigger, keeping
// every other field as the API answered it, those this package does not
// model and numbers past a float's precision among them; pausing writes
// suspend and resuming takes it away.
func TestAChangeKeepsWhatTheRoutineDoesNotOwn(t *testing.T) {
	tp, c := newTopos(t)
	r := create(t, c, weekly(), "k")
	tp.edit(r.Name, func(spec, meta map[string]any) {
		meta["labels"].(map[string]any)["team"] = "home"
		meta["annotations"] = map[string]any{"note": "kept"}
		meta["addedLater"] = "kept"
		spec["maxActive"] = 2
		spec["addedLater"] = map[string]any{"deep": true}
		spec["bigNumber"] = json.Number("9007199254740993")
		spec["session"].(map[string]any)["budget"] = map[string]any{"maxCost": "0.50"}
		spec["session"].(map[string]any)["addedLater"] = "kept"
	})
	title := "Monday news"
	got, err := c.ChangeRoutine(t.Context(), chat, r.Name, &agents.ChangeRoutineParams{Title: &title})
	if err != nil || got.Title != "Monday news" || got.Instruction != r.Instruction {
		t.Fatalf("the change answered %+v, %v", got, err)
	}
	put := tp.manifest(r.Name)
	meta, spec := put["metadata"].(map[string]any), put["spec"].(map[string]any)
	if labels := meta["labels"].(map[string]any); labels["team"] != "home" || labels[agents.RoutineLabel] != chat ||
		meta["annotations"].(map[string]any)["note"] != "kept" || meta["addedLater"] != "kept" || meta["displayName"] != "Monday news" {
		t.Errorf("the metadata written is %v", meta)
	}
	session := spec["session"].(map[string]any)
	if spec["maxActive"] != float64(2) || session["budget"] == nil || session["addedLater"] != "kept" || session["title"] != "Monday news" ||
		spec["addedLater"] == nil || spec["schedule"] != "0 9 * * 1,4" || spec["agent"] != chat {
		t.Errorf("the spec written is %v", spec)
	}
	if body := tp.received(); !strings.Contains(string(body[len(body)-1].Body), `"bigNumber":9007199254740993`) {
		t.Errorf("a large number was not sent as it was: %s", body[len(body)-1].Body)
	}
	if _, has := put["status"]; has {
		t.Errorf("the change sent the trigger's status back: %v", put)
	}

	paused := true
	if got, err := c.ChangeRoutine(t.Context(), chat, r.Name, &agents.ChangeRoutineParams{Paused: &paused}); err != nil || !got.Paused || got.NextRunAt != nil {
		t.Errorf("a paused routine reads %+v, %v", got, err)
	}
	if spec := tp.manifest(r.Name)["spec"].(map[string]any); spec["suspend"] != true || spec["addedLater"] == nil {
		t.Errorf("pausing wrote %v", spec)
	}
	paused = false
	if got, err := c.ChangeRoutine(t.Context(), chat, r.Name, &agents.ChangeRoutineParams{Paused: &paused}); err != nil || got.Paused || got.NextRunAt == nil {
		t.Errorf("a resumed routine reads %+v, %v", got, err)
	}
	if spec := tp.manifest(r.Name)["spec"].(map[string]any); spec["suspend"] != nil {
		t.Errorf("resuming wrote suspend %v", spec["suspend"])
	}
	// A change that names nothing writes the routine as it is.
	if got, err := c.ChangeRoutine(t.Context(), chat, r.Name, nil); err != nil || got.Title != "Monday news" {
		t.Errorf("an empty change answered %+v, %v", got, err)
	}
}

// A trigger that is not a routine of the agent is not changed, deleted or
// run, even under a routine's name, and a name of another shape never
// reaches the API.
func TestATriggerThatIsNoRoutineIsNotTouched(t *testing.T) {
	tp, c := newTopos(t)
	const other = "routine-0123456789abcdef0123"
	tp.put(other, `{"metadata":{"name":"`+other+`","labels":{"applied-by":"console"}},"spec":{"agent":"agent_x","schedule":"0 2 * * *","session":{"message":"Build"}}}`)
	tp.lines()
	paused := true
	for _, name := range []string{other, "nightly-build", "ROUTINE-0123456789ABCDEF0123", "routine-0123", "routine-0123456789abcdef0123/../x"} {
		_, getErr := c.GetRoutine(t.Context(), chat, name)
		_, changeErr := c.ChangeRoutine(t.Context(), chat, name, &agents.ChangeRoutineParams{Paused: &paused})
		deleteErr := c.DeleteRoutine(t.Context(), chat, name)
		_, runErr := c.RunRoutine(t.Context(), chat, name, nil)
		for what, err := range map[string]error{"read": getErr, "change": changeErr, "delete": deleteErr, "run": runErr} {
			if !errors.Is(err, agents.ErrNotRoutine) || !errors.Is(err, latere.ErrNotFound) {
				t.Errorf("a %s of %s: %v", what, name, err)
			}
		}
	}
	for _, line := range tp.lines() {
		if line != "GET "+triggersPath+"/"+other {
			t.Errorf("the API saw %s; only a read of the trigger may reach it", line)
		}
	}
	if tp.triggers[other] == nil {
		t.Error("the trigger is gone")
	}
	if _, err := c.GetRoutine(t.Context(), "", other); err == nil || errors.Is(err, agents.ErrNotRoutine) {
		t.Errorf("a read for no agent: %v", err)
	}
	if lines := tp.lines(); len(lines) != 0 {
		t.Errorf("a read for no agent reached the API: %v", lines)
	}
}

// Delete removes the routine's trigger once it read that it is one; a
// routine already gone is not found.
func TestDeleteRemovesTheTrigger(t *testing.T) {
	tp, c := newTopos(t)
	r := create(t, c, weekly(), "k")
	tp.lines()
	if err := c.DeleteRoutine(t.Context(), chat, r.Name); err != nil {
		t.Fatal(err)
	}
	if lines := tp.lines(); !reflect.DeepEqual(lines, []string{"GET " + triggersPath + "/" + r.Name, "DELETE " + triggersPath + "/" + r.Name}) || len(tp.triggers) != 0 {
		t.Errorf("the API saw %v and keeps %d triggers", lines, len(tp.triggers))
	}
	if err := c.DeleteRoutine(t.Context(), chat, r.Name); !errors.Is(err, latere.ErrNotFound) || errors.Is(err, agents.ErrNotRoutine) {
		t.Errorf("a second delete: %v", err)
	}
	if _, err := c.GetRoutine(t.Context(), chat, r.Name); !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("a read of the deleted routine: %v", err)
	}
}

// Run now fires the trigger with no body and the caller's key, after
// reading that it is a routine on a schedule that is not paused; a second
// run with the same key runs once.
func TestRunNowFiresTheTriggerOnce(t *testing.T) {
	tp, c := newTopos(t)
	r := create(t, c, weekly(), "k")
	tp.lines()
	fired, err := c.RunRoutine(t.Context(), chat, r.Name, &agents.RunRoutineParams{IdempotencyKey: "press-1"})
	if err != nil || fired.Outcome != agents.FiringStarted || fired.SessionID == "" || fired.TriggerID != r.TriggerID {
		t.Fatalf("the run answered %+v, %v", fired, err)
	}
	calls := tp.received()
	if lines := tp.lines(); !reflect.DeepEqual(lines, []string{"GET " + triggersPath + "/" + r.Name, "POST " + triggersPath + "/" + r.Name + "/fire"}) {
		t.Errorf("the API saw %v", lines)
	}
	if fire := calls[1]; len(fire.Body) != 0 || fire.Key != "press-1" {
		t.Errorf("the fire carried the body %q and the key %q", fire.Body, fire.Key)
	}
	again, err := c.RunRoutine(t.Context(), chat, r.Name, &agents.RunRoutineParams{IdempotencyKey: "press-1"})
	if err != nil || again.SessionID != fired.SessionID {
		t.Errorf("a second press answered %+v, %v", again, err)
	}
	if got, err := c.GetRoutine(t.Context(), chat, r.Name); err != nil || got.LastSessionID != fired.SessionID || got.LastRunAt == nil {
		t.Errorf("the routine's last run reads %+v, %v", got, err)
	}
	// Without a key, one is made.
	tp.lines()
	if _, err := c.RunRoutine(t.Context(), chat, r.Name, nil); err != nil {
		t.Fatal(err)
	}
	if calls := tp.received(); calls[len(calls)-1].Key == "" {
		t.Error("a run without a key sent none")
	}
}

// A firing the API records as failed is answered with the firing beside
// the error, as FireTrigger answers it.
func TestAFailedRunIsSaidAsTheFiring(t *testing.T) {
	tp, c := newTopos(t)
	r := create(t, c, weekly(), "k")
	tp.refuse["POST "+triggersPath+"/"+r.Name+"/fire"] = refusal{http.StatusServiceUnavailable,
		`{"id":"frg_9","trigger_id":"` + r.TriggerID + `","origin":"fire","key":"r","outcome":"failed","reason":"the agent is gone","received_at":"2026-10-21T09:00:00Z"}`}
	fired, err := c.RunRoutine(t.Context(), chat, r.Name, &agents.RunRoutineParams{IdempotencyKey: "r"})
	var apiErr *latere.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusServiceUnavailable || fired == nil || fired.Outcome != agents.FiringFailed || fired.Reason == "" {
		t.Errorf("a failed firing answered %+v, %v", fired, err)
	}
}

// A paused routine is not fired, and one paused since it was read is
// refused by the API as paused, an error that still carries the API's.
func TestAPausedRoutineDoesNotRun(t *testing.T) {
	tp, c := newTopos(t)
	p := weekly()
	p.Paused = true
	r := create(t, c, p, "k")
	tp.lines()
	if _, err := c.RunRoutine(t.Context(), chat, r.Name, nil); !errors.Is(err, agents.ErrRoutinePaused) {
		t.Errorf("a run of a paused routine: %v", err)
	}
	for _, line := range tp.lines() {
		if strings.HasSuffix(line, "/fire") {
			t.Errorf("a paused routine was fired: %s", line)
		}
	}
	paused := false
	if _, err := c.ChangeRoutine(t.Context(), chat, r.Name, &agents.ChangeRoutineParams{Paused: &paused}); err != nil {
		t.Fatal(err)
	}
	tp.refuse["POST "+triggersPath+"/"+r.Name+"/fire"] = refusal{http.StatusConflict, `{"error":{"code":"conflict","message":"The trigger is suspended."}}`}
	fired, err := c.RunRoutine(t.Context(), chat, r.Name, nil)
	var apiErr *latere.Error
	if fired != nil || !errors.Is(err, agents.ErrRoutinePaused) || !errors.Is(err, latere.ErrConflict) || !errors.As(err, &apiErr) || apiErr.Code != "conflict" {
		t.Errorf("a run of a routine paused elsewhere: %+v, %v", fired, err)
	}
	if err.Error() != apiErr.Error() {
		t.Errorf("the error says %q, want the API's %q", err, apiErr)
	}
	// Another refusal of the fire stays the API's.
	tp.refuse["POST "+triggersPath+"/"+r.Name+"/fire"] = refusal{http.StatusForbidden, `{"error":{"code":"forbidden","message":"No."}}`}
	if _, err := c.RunRoutine(t.Context(), chat, r.Name, nil); !errors.Is(err, latere.ErrForbidden) || errors.Is(err, agents.ErrRoutinePaused) {
		t.Errorf("a forbidden run: %v", err)
	}
}

// What a routine cannot be is refused before the API hears of any of it.
func TestARoutineIsCheckedBeforeTheAPIHearsOfIt(t *testing.T) {
	tp, c := newTopos(t)
	r := create(t, c, weekly(), "k")
	with := func(change func(*agents.CreateRoutineParams)) *agents.CreateRoutineParams {
		p := weekly()
		change(p)
		return p
	}
	schedule := func(s agents.RoutineSchedule) func(*agents.CreateRoutineParams) {
		return func(p *agents.CreateRoutineParams) { p.Schedule = &s }
	}
	for _, tc := range []struct {
		name   string
		params *agents.CreateRoutineParams
		want   error
	}{
		{"no name", with(func(p *agents.CreateRoutineParams) { p.Title = "" }), agents.ErrRoutineTitleMissing},
		{"a blank name", with(func(p *agents.CreateRoutineParams) { p.Title = "   " }), agents.ErrRoutineTitleMissing},
		{"a long name", with(func(p *agents.CreateRoutineParams) { p.Title = strings.Repeat("ä", agents.MaxRoutineTitle+1) }), agents.ErrRoutineTitleInvalid},
		{"a name of two lines", with(func(p *agents.CreateRoutineParams) { p.Title = "one\ntwo" }), agents.ErrRoutineTitleInvalid},
		{"a name with a bidirectional control", with(func(p *agents.CreateRoutineParams) { p.Title = "one\u202etwo" }), agents.ErrRoutineTitleInvalid},
		{"a name with a line separator", with(func(p *agents.CreateRoutineParams) { p.Title = "one\u2028two" }), agents.ErrRoutineTitleInvalid},
		{"no instruction", with(func(p *agents.CreateRoutineParams) { p.Instruction = "" }), agents.ErrRoutineInstructionMissing},
		{"a blank instruction", with(func(p *agents.CreateRoutineParams) { p.Instruction = "\n " }), agents.ErrRoutineInstructionMissing},
		{"a long instruction", with(func(p *agents.CreateRoutineParams) {
			p.Instruction = strings.Repeat("a", agents.MaxRoutineInstruction+1)
		}), agents.ErrRoutineInstructionTooLong},
		{"no schedule", with(func(p *agents.CreateRoutineParams) { p.Schedule = nil }), agents.ErrRoutineScheduleInvalid},
		{"no day", with(schedule(agents.RoutineSchedule{Hour: 9, Zone: "UTC"})), agents.ErrRoutineScheduleInvalid},
		{"no day of the week", with(schedule(agents.RoutineSchedule{Days: []time.Weekday{7}, Hour: 9, Zone: "UTC"})), agents.ErrRoutineScheduleInvalid},
		{"an hour past the day", with(schedule(agents.RoutineSchedule{Days: []time.Weekday{time.Monday}, Hour: 24, Zone: "UTC"})), agents.ErrRoutineScheduleInvalid},
		{"a minute past the hour", with(schedule(agents.RoutineSchedule{Days: []time.Weekday{time.Monday}, Hour: 9, Minute: 60, Zone: "UTC"})), agents.ErrRoutineScheduleInvalid},
		{"a negative hour", with(schedule(agents.RoutineSchedule{Days: []time.Weekday{time.Monday}, Hour: -1, Zone: "UTC"})), agents.ErrRoutineScheduleInvalid},
		{"a zone of another shape", with(schedule(agents.RoutineSchedule{Days: []time.Weekday{time.Monday}, Hour: 9, Zone: "../etc"})), agents.ErrRoutineScheduleInvalid},
		{"no zone", with(schedule(agents.RoutineSchedule{Days: []time.Weekday{time.Monday}, Hour: 9})), agents.ErrRoutineScheduleInvalid},
		{"a way to start there is none of", with(func(p *agents.CreateRoutineParams) { p.Starts = "webhook" }), agents.ErrRoutineStartsInvalid},
		{"a long instruction from another app", with(func(p *agents.CreateRoutineParams) {
			p.Starts, p.Instruction = agents.StartsFromLink, strings.Repeat("a", agents.MaxEventRoutineInstruction+1)
		}), agents.ErrRoutineInstructionTooLong},
	} {
		tp.lines()
		if _, err := c.CreateRoutine(t.Context(), tc.params); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, err, tc.want)
		}
		if lines := tp.lines(); len(lines) != 0 {
			t.Errorf("%s: the API saw %v", tc.name, lines)
		}
	}
	for _, bad := range []*agents.CreateRoutineParams{nil, {Title: "x", Instruction: "y", Schedule: weekly().Schedule}} {
		if _, err := c.CreateRoutine(t.Context(), bad); err == nil {
			t.Errorf("a create of no agent: %+v", bad)
		}
	}

	empty, long, webhook := "", strings.Repeat("a", agents.MaxRoutineInstruction+1), agents.RoutineStart("webhook")
	for _, tc := range []struct {
		name   string
		change *agents.ChangeRoutineParams
		want   error
	}{
		{"a change of name to none", &agents.ChangeRoutineParams{Title: &empty}, agents.ErrRoutineTitleMissing},
		{"a change to a long instruction", &agents.ChangeRoutineParams{Instruction: &long}, agents.ErrRoutineInstructionTooLong},
		{"a change to no day", &agents.ChangeRoutineParams{Schedule: &agents.RoutineSchedule{Hour: 9, Zone: "UTC"}}, agents.ErrRoutineScheduleInvalid},
		{"a change to a way to start there is none of", &agents.ChangeRoutineParams{Starts: &webhook}, agents.ErrRoutineStartsInvalid},
	} {
		tp.lines()
		if _, err := c.ChangeRoutine(t.Context(), chat, r.Name, tc.change); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, err, tc.want)
		}
		if lines := tp.lines(); !reflect.DeepEqual(lines, []string{"GET " + triggersPath + "/" + r.Name}) {
			t.Errorf("%s: the API saw %v, want the read alone", tc.name, lines)
		}
	}
	if len(tp.triggers) != 1 {
		t.Errorf("the API keeps %d triggers", len(tp.triggers))
	}
}

// The API's refusals of a manifest reach the caller as its errors.
func TestTheAPIsRefusalsPassThrough(t *testing.T) {
	tp, c := newTopos(t)
	p := weekly()
	p.Schedule.Zone = "Mars/Olympus_Mons"
	tp.refuse["PUT "+triggersPath+"/"+agents.RoutineName("k")] = refusal{http.StatusBadRequest,
		`{"error":{"code":"invalid_request","message":"The manifest is not valid.","details":{"detail":"spec.timeZone: unknown time zone Mars/Olympus_Mons"}}}`}
	_, err := c.CreateRoutine(t.Context(), &agents.CreateRoutineParams{Agent: chat, Title: p.Title, Instruction: p.Instruction, Schedule: p.Schedule, IdempotencyKey: "k"})
	var apiErr *latere.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "invalid_request" || !strings.HasPrefix(fmt.Sprint(apiErr.Details["detail"]), "spec.timeZone") {
		t.Errorf("an unknown zone: %v", err)
	}
	if len(tp.triggers) != 0 {
		t.Errorf("a refused manifest left %d triggers", len(tp.triggers))
	}
}

// An answer of the API that is no routine's trigger is no routine: a
// stored trigger without the label, one that is not a trigger at all, and
// a refused delete are each an error, never taken as done.
func TestAnAnswerThatIsNoRoutineIsNotTakenAsOne(t *testing.T) {
	tp, c := newTopos(t)
	r := create(t, c, weekly(), "k")
	title := "Other"
	change := &agents.ChangeRoutineParams{Title: &title}
	put := "PUT " + triggersPath + "/" + r.Name
	tp.refuse[put] = refusal{http.StatusOK, `{"metadata":{"name":"` + r.Name + `"},"spec":{"schedule":"0 9 * * 1"}}`}
	if _, err := c.ChangeRoutine(t.Context(), chat, r.Name, change); err == nil || errors.Is(err, agents.ErrNotRoutine) {
		t.Errorf("an apply answered without the label: %v", err)
	}
	tp.refuse[put] = refusal{http.StatusOK, `{"metadata":{"name":"` + r.Name + `","labels":{"applied-by":"latere-chat"}},"spec":"no object"}`}
	if _, err := c.ChangeRoutine(t.Context(), chat, r.Name, change); err == nil {
		t.Error("an apply answered with what is no trigger was taken as a routine")
	}
	delete(tp.refuse, put)
	tp.refuse["GET "+triggersPath] = refusal{http.StatusOK, `{"items":[{"metadata":"no object","spec":{}}]}`}
	for _, err := range c.ListRoutines(t.Context(), &agents.ListRoutinesParams{Agent: chat}) {
		if err == nil {
			t.Error("a list of what is no trigger was read")
		}
	}
	get := "GET " + triggersPath + "/" + r.Name
	tp.refuse[get] = refusal{http.StatusOK, `{"metadata":{"name":"` + r.Name + `","labels":{"applied-by":"latere-chat"}},"spec":"no object"}`}
	if err := c.DeleteRoutine(t.Context(), chat, r.Name); err == nil {
		t.Error("a read of what is no trigger let the delete through")
	}
	delete(tp.refuse, get)
	tp.refuse["DELETE "+triggersPath+"/"+r.Name] = refusal{http.StatusForbidden, `{"error":{"code":"forbidden","message":"Not yours."}}`}
	if err := c.DeleteRoutine(t.Context(), chat, r.Name); !errors.Is(err, latere.ErrForbidden) {
		t.Errorf("a refused delete: %v", err)
	}
	if tp.triggers[r.Name] == nil {
		t.Error("the routine is gone after its delete was refused")
	}
	tp.refuse[get] = refusal{http.StatusServiceUnavailable, `{"error":{"code":"unavailable","message":"Try again."}}`}
	if _, err := c.RunRoutine(t.Context(), chat, r.Name, nil); err == nil {
		t.Error("a run whose read failed answered no error")
	}
	for _, line := range tp.lines() {
		if strings.HasSuffix(line, "/fire") {
			t.Errorf("a routine that could not be read was fired: %s", line)
		}
	}
}

// A routine that starts from another app is an event trigger of the
// platform's webhook product: it reads back as its instruction alone, with
// no schedule or next run, and runs only when something arrives.
func TestARoutineFromAnotherAppIsAnEventTrigger(t *testing.T) {
	_, c := newTopos(t)
	r := create(t, c, &agents.CreateRoutineParams{Agent: chat, Title: "Contact form", Instruction: "Draft a reply to it. {{name}} is theirs.", Starts: agents.StartsFromLink}, "k")
	if r.Starts != agents.StartsFromLink || r.Instruction != "Draft a reply to it. {{name}} is theirs." || r.Schedule != nil || r.NextRunAt != nil {
		t.Errorf("the routine reads %+v", r)
	}
	var list []*agents.Routine
	for got, err := range c.ListRoutines(t.Context(), &agents.ListRoutinesParams{Agent: chat}) {
		if err != nil {
			t.Fatal(err)
		}
		list = append(list, got)
	}
	if len(list) != 1 || list[0].Starts != agents.StartsFromLink || list[0].Instruction != r.Instruction {
		t.Errorf("the list reads %+v", list)
	}
	if _, err := c.RunRoutine(t.Context(), chat, r.Name, nil); !errors.Is(err, agents.ErrRoutineStartsOnArrival) {
		t.Errorf("Run now of a routine from another app: %v", err)
	}
}

// A routine on a schedule becomes one from an email by a change, which
// takes its schedule away; it comes back to a schedule only with one
// named, keeping its instruction, and the instruction is held to the
// bound of how it starts once the change is made.
func TestAChangeOfHowARoutineStarts(t *testing.T) {
	tp, c := newTopos(t)
	w := create(t, c, weekly(), "w")
	tp.edit(w.Name, func(spec, _ map[string]any) { spec["session"].(map[string]any)["key"] = "routine" })
	email := agents.StartsFromEmail
	got, err := c.ChangeRoutine(t.Context(), chat, w.Name, &agents.ChangeRoutineParams{Starts: &email})
	if err != nil || got.Starts != agents.StartsFromEmail || got.Schedule != nil || got.Instruction != w.Instruction {
		t.Fatalf("the change answered %+v, %v", got, err)
	}
	spec := tp.manifest(w.Name)["spec"].(map[string]any)
	on, _ := spec["on"].(map[string]any)
	msg, _ := spec["session"].(map[string]any)["message"].(string)
	if spec["schedule"] != nil || spec["timeZone"] != nil || spec["session"].(map[string]any)["key"] != nil || on["product"] != "mail" ||
		!strings.HasPrefix(msg, w.Instruction+"\n\nThe email that started this run:\nFrom: {{event.actor}}") {
		t.Errorf("the changed spec is %v", spec)
	}
	schedule := agents.StartsOnSchedule
	if _, err := c.ChangeRoutine(t.Context(), chat, w.Name, &agents.ChangeRoutineParams{Starts: &schedule}); !errors.Is(err, agents.ErrRoutineScheduleInvalid) {
		t.Errorf("back to a schedule without one: %v", err)
	}
	back, err := c.ChangeRoutine(t.Context(), chat, w.Name, &agents.ChangeRoutineParams{Starts: &schedule,
		Schedule: &agents.RoutineSchedule{Days: []time.Weekday{time.Monday}, Hour: 8, Zone: "Europe/Berlin"}})
	if err != nil || back.Starts != agents.StartsOnSchedule || back.Schedule == nil || back.Instruction != w.Instruction {
		t.Errorf("back to a schedule answered %+v, %v", back, err)
	}
	if spec := tp.manifest(w.Name)["spec"].(map[string]any); spec["on"] != nil || spec["schedule"] != "0 8 * * 1" {
		t.Errorf("back to a schedule wrote %v", spec)
	}

	long := strings.Repeat("a", agents.MaxEventRoutineInstruction+1)
	p := weekly()
	p.Instruction = long
	l := create(t, c, p, "long")
	link := agents.StartsFromLink
	if _, err := c.ChangeRoutine(t.Context(), chat, l.Name, &agents.ChangeRoutineParams{Starts: &link}); !errors.Is(err, agents.ErrRoutineInstructionTooLong) {
		t.Errorf("a long instruction moved to a link: %v", err)
	}
	// A schedule named for a routine that starts from a link is not
	// written.
	if got, err := c.ChangeRoutine(t.Context(), chat, w.Name, &agents.ChangeRoutineParams{Starts: &link, Schedule: weekly().Schedule}); err != nil || got.Schedule != nil {
		t.Errorf("a link with a schedule reads %+v, %v", got, err)
	}
}

// An event of another product, set another way, reads as a routine on a
// schedule set elsewhere, which a change that names a schedule replaces.
func TestAnEventOfAnotherProductReadsAsAScheduleSetElsewhere(t *testing.T) {
	tp, c := newTopos(t)
	r := create(t, c, weekly(), "k")
	tp.edit(r.Name, func(spec, _ map[string]any) {
		delete(spec, "schedule")
		spec["on"] = map[string]any{"product": "github", "verbs": []any{"release.published"}}
	})
	got, err := c.GetRoutine(t.Context(), chat, r.Name)
	if err != nil || got.Starts != agents.StartsOnSchedule || got.Schedule != nil {
		t.Fatalf("the routine reads %+v, %v", got, err)
	}
	if _, err := c.ChangeRoutine(t.Context(), chat, r.Name, &agents.ChangeRoutineParams{Schedule: weekly().Schedule}); err != nil {
		t.Fatal(err)
	}
	if spec := tp.manifest(r.Name)["spec"].(map[string]any); spec["on"] != nil || spec["schedule"] != "0 9 * * 1,4" {
		t.Errorf("the change wrote %v", spec)
	}
}

// RoutineOf reads a trigger as a routine of one agent only.
func TestRoutineOf(t *testing.T) {
	var tr agents.Trigger
	if err := json.Unmarshal([]byte(trigger), &tr); err != nil {
		t.Fatal(err)
	}
	if _, ok := agents.RoutineOf(&tr, chat); ok {
		t.Error("a trigger without the label read as a routine")
	}
	tr.Metadata.Name, tr.Metadata.Labels = agents.RoutineName("k"), map[string]string{agents.RoutineLabel: chat}
	r, ok := agents.RoutineOf(&tr, chat)
	if !ok || r.Title != "Weekly digest" || r.TriggerID != "trg_1" || r.LastSessionID != "ses_9" || r.NextRunAt == nil || string(r.RawJSON()) != trigger {
		t.Errorf("the routine reads %+v, %v", r, ok)
	}
	if r.Origin != nil {
		t.Errorf("a routine the person made reads the origin %+v", r.Origin)
	}
	offered := &agents.TriggerOrigin{Kind: agents.TriggerFromProposal, SessionID: "ses_4", ToolUseID: "toolu_1", ConfirmedBy: "alice"}
	tr.Status.Origin = offered
	if r, ok := agents.RoutineOf(&tr, chat); !ok || r.Origin == nil || *r.Origin != *offered {
		t.Errorf("a routine an agent offered reads the origin %+v, %v", r.Origin, ok)
	}
	for _, agent := range []string{"", "another"} {
		if _, ok := agents.RoutineOf(&tr, agent); ok {
			t.Errorf("the routine read as one of %q", agent)
		}
	}
	if _, ok := agents.RoutineOf(nil, chat); ok {
		t.Error("nil read as a routine")
	}
}
