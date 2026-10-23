// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents_test

import (
	"encoding/json"
	"errors"
	"fmt"
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
	"latere.ai/x/sdk-go/agents/routine"
	"latere.ai/x/sdk-go/latere"
)

// watchAPI stands in for the trigger list, the sessions and their logs:
// it answers the triggers it holds in pages of pageSize, each session it
// holds, and each log in pages of the limit asked, and records every
// request.
type watchAPI struct {
	t        *testing.T
	mu       sync.Mutex
	pageSize int
	triggers []map[string]any
	sessions map[string]string
	logs     map[string][]string
	failing  map[string]bool
	slow     time.Duration
	calls    []string
}

func newWatchAPI(t *testing.T) (*watchAPI, *agents.Client) {
	t.Helper()
	api := &watchAPI{t: t, pageSize: 2, sessions: map[string]string{}, logs: map[string][]string{}, failing: map[string]bool{}}
	srv := httptest.NewServer(http.HandlerFunc(api.serve))
	t.Cleanup(srv.Close)
	cfg, err := latere.NewConfig(latere.WithBaseURL(srv.URL), latere.WithTokenSource(latere.StaticToken("key_test")), latere.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	return api, agents.New(cfg)
}

// change changes what the stand-in holds while a request a failed reading
// canceled may still be answered.
func (api *watchAPI) change(f func()) {
	api.mu.Lock()
	defer api.mu.Unlock()
	f()
}

// received returns the requests since it was last called, sorted, since
// a page's reads run at once.
func (api *watchAPI) received() []string {
	api.mu.Lock()
	defer api.mu.Unlock()
	out := slices.Clone(api.calls)
	slices.Sort(out)
	api.calls = nil
	return out
}

func (api *watchAPI) serve(w http.ResponseWriter, r *http.Request) {
	api.mu.Lock()
	slow := api.slow
	if api.failing[r.URL.Path] {
		slow = 0
	}
	api.mu.Unlock()
	time.Sleep(slow)
	api.mu.Lock()
	defer api.mu.Unlock()
	line := r.Method + " " + r.URL.Path
	if r.URL.RawQuery != "" {
		line += "?" + r.URL.RawQuery
	}
	api.calls = append(api.calls, line)
	w.Header().Set("Content-Type", "application/json")
	if api.failing[r.URL.Path] {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":{"code":"internal","message":"Something went wrong."}}`)
		return
	}
	q := r.URL.Query()
	from, _ := strconv.Atoi(q.Get("cursor"))
	rest, isSession := strings.CutPrefix(r.URL.Path, "/v1/agents/sessions/")
	switch {
	case r.URL.Path == "/v1/agents/triggers":
		end := min(from+api.pageSize, len(api.triggers))
		api.page(w, api.triggers[from:end], end, len(api.triggers))
		return
	case isSession && strings.HasSuffix(rest, "/events"):
		events, ok := api.logs[strings.TrimSuffix(rest, "/events")]
		if ok {
			limit, err := strconv.Atoi(q.Get("limit"))
			if err != nil {
				api.t.Errorf("a log read without a limit: %s", line)
				limit = 50
			}
			end := min(from+limit, len(events))
			items := []json.RawMessage{}
			for _, e := range events[from:end] {
				items = append(items, json.RawMessage(e))
			}
			api.page(w, items, end, len(events))
			return
		}
	case isSession:
		if s, ok := api.sessions[rest]; ok {
			fmt.Fprint(w, s)
			return
		}
	case strings.HasPrefix(r.URL.Path, "/v1/agents/triggers/"):
		ref := strings.TrimPrefix(r.URL.Path, "/v1/agents/triggers/")
		for _, t := range api.triggers {
			if status, _ := t["status"].(map[string]any); status != nil && status["id"] == ref {
				if err := json.NewEncoder(w).Encode(t); err != nil {
					api.t.Error(err)
				}
				return
			}
		}
	}
	w.WriteHeader(http.StatusNotFound)
	fmt.Fprint(w, `{"error":{"code":"not_found","message":"Not found."}}`)
}

// page writes items as a page whose next cursor is end, while end is
// short of total.
func (api *watchAPI) page(w http.ResponseWriter, items any, end, total int) {
	next := ""
	if end < total {
		next = strconv.Itoa(end)
	}
	if err := json.NewEncoder(w).Encode(map[string]any{"items": items, "next_cursor": next}); err != nil {
		api.t.Error(err)
	}
}

// routineTrigger is a routine of agent named by key, as Latere Chat
// writes it, with spec changed by change and the status the API answers.
func routineTrigger(agent, key, title, message string, status map[string]any, change func(spec map[string]any)) map[string]any {
	spec := map[string]any{"agent": "agent_1", "schedule": "0 9 * * 1", "timeZone": "Europe/Berlin", "skipIfActive": false,
		"session": map[string]any{"message": message, "title": title}}
	if change != nil {
		change(spec)
	}
	return map[string]any{"apiVersion": "topos.latere.ai/v1", "kind": "Trigger", "spec": spec, "status": status,
		"metadata": map[string]any{"name": agents.RoutineName(key), "displayName": title, "labels": map[string]any{agents.RoutineLabel: agent}}}
}

// eventName is the name of the trigger of an offer on events no routine
// starts from, made with key.
func eventName(key string) string { return routine.EventName(key) }

// eventTrigger is a trigger an agent offered on a repository's pushes,
// which no routine starts from, as the API writes it: a routine's label,
// title and ask under a name of its own, with the event after the ask.
func eventTrigger(agent, key string, status map[string]any) map[string]any {
	return map[string]any{"apiVersion": "topos.latere.ai/v1", "kind": "Trigger", "status": status,
		"metadata": map[string]any{"name": eventName(key), "displayName": "Retry test on main", "labels": map[string]any{agents.RoutineLabel: agent}},
		"spec": map[string]any{"agent": "agent_1", "skipIfActive": false,
			"on":      map[string]any{"product": "github", "verbs": []any{"push"}, "resources": []any{"acme/field-notes"}},
			"session": map[string]any{"message": routine.EventMessage("Run the retry test."), "title": "Retry test on main"}}}
}

// wakeTrigger is a wake an agent set in session.
func wakeTrigger(id, session, toolUse, title string) map[string]any {
	return map[string]any{"apiVersion": "topos.latere.ai/v1", "kind": "Trigger",
		"metadata": map[string]any{"name": "wake-" + strings.ToLower(strings.TrimPrefix(session, "ses_")), "displayName": title},
		"spec": map[string]any{"agent": "agent_1", "at": "2026-10-24T07:00:00Z", "maxAge": "24h",
			"session": map[string]any{"message": "Look at the inbox.", "policy": "continue", "target": session}},
		"status": map[string]any{"id": id, "createdAt": "2026-10-23T09:00:00Z", "origin": map[string]any{"kind": "wake", "sessionId": session, "toolUseId": toolUse}}}
}

// offered is the status of a trigger an agent offered in session with the
// call toolUse, which the person accepted at created, or at a time the
// status does not say when created is empty.
func offered(id, session, toolUse, created string) map[string]any {
	status := map[string]any{"id": id, "origin": map[string]any{"kind": "proposal", "sessionId": session, "toolUseId": toolUse, "confirmedBy": "alice"}}
	if created != "" {
		status["createdAt"] = created
	}
	return status
}

// sessionOf is a session of agent with a title and, when wake is set, its
// pending wake.
func sessionOf(id, agent, title, wake string) string {
	s := fmt.Sprintf(`{"id":%q,"agent":{"id":"agent_1","name":%q,"version":3},"title":%q,"status":"idle","turn":2,"last_seq":9,
"created_at":"2026-10-22T09:00:00Z","updated_at":"2026-10-23T09:00:00Z"`, id, agent, title)
	if wake != "" {
		s += `,"wake":` + wake
	}
	return s + "}"
}

// logEvent is an event of session at seq, appended at minute past 9:00 on
// 2026-10-22, of type typ with payload.
func logEvent(session string, seq, minute int, typ, payload string) string {
	at := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC).Add(time.Duration(minute) * time.Minute).Format(time.RFC3339)
	return fmt.Sprintf(`{"id":"evt_%d","seq":%d,"session_id":%q,"type":%q,"time":%q,"payload":%s}`, seq, seq, session, typ, at, payload)
}

// heldCall is the payload of a held call of the trigger tool.
func heldCall(toolUse, input string) string {
	return fmt.Sprintf(`{"tool_use_id":%q,"name":"trigger","input":%s,"verdict":"ask"}`, toolUse, input)
}

// TestWatchesAreReadInOnePass: one page of triggers reads as the watches
// of one agent, routines, event triggers in an offer's shape and wakes,
// with each session they came from read once and each offer's why read
// from one pass over its session's log; triggers of another agent, of
// another shape, and wakes of another agent's sessions are left out. A
// second reading that knows the whys reads no log.
func TestWatchesAreReadInOnePass(t *testing.T) {
	api, c := newWatchAPI(t)
	api.pageSize = 10
	link := "Tag each issue.\n\nWhat the other app sent:\n{{event.payload.body}}"
	api.triggers = []map[string]any{
		routineTrigger(chat, "mine", "Weekly sign-ups", "Count the sign-ups.",
			map[string]any{"id": "trg_1", "createdAt": "2026-10-01T09:00:00Z", "nextFireAt": "2026-10-26T08:00:00Z"}, nil),
		routineTrigger(chat, "offered", "Monday plan", "Plan the week.", offered("trg_2", "ses_a", "toolu_1", "2026-10-22T09:02:00Z"),
			func(spec map[string]any) { spec["suspend"] = true }),
		eventTrigger(chat, "event", offered("trg_3", "ses_a", "toolu_2", "2026-10-22T09:06:00Z")),
		eventTrigger("release-notes", "theirs", offered("trg_9", "ses_a", "toolu_9", "2026-10-22T09:06:00Z")),
		eventTrigger(chat, "unoffered", map[string]any{"id": "trg_10"}),
		wakeTrigger("trg_4", "ses_b", "toolu_3", "Whether the refund arrived"),
		wakeTrigger("trg_5", "ses_c", "toolu_4", "Whether the build passed"),
		{"apiVersion": "topos.latere.ai/v1", "kind": "Trigger", "metadata": map[string]any{"name": "weekly-digest"},
			"spec": map[string]any{"agent": "agent_1", "schedule": "0 9 * * 1", "session": map[string]any{"message": "Digest."}}, "status": map[string]any{"id": "trg_6"}},
		routineTrigger("release-notes", "theirs", "Notes", "Write notes.", map[string]any{"id": "trg_7"}, nil),
		routineTrigger(chat, "link", "Tag issues", link, map[string]any{"id": "trg_8"}, func(spec map[string]any) {
			delete(spec, "schedule")
			delete(spec, "timeZone")
			spec["on"] = map[string]any{"product": "webhook", "verbs": []any{"received"}}
		}),
	}
	api.sessions["ses_a"] = sessionOf("ses_a", chat, "Plan my week", "")
	api.sessions["ses_b"] = sessionOf("ses_b", chat, "A refund", `{"trigger_id":"trg_4","at":"2026-10-24T07:00:00Z","title":"Whether the refund arrived","count":3}`)
	api.sessions["ses_c"] = sessionOf("ses_c", "release-notes", "A build", `{"trigger_id":"trg_5","at":"2026-10-24T07:00:00Z","title":"Whether the build passed","count":1}`)
	api.logs["ses_a"] = []string{
		logEvent("ses_a", 1, 0, "user.message", `{"sender":{"subject":"alice","kind":"person"},"content":[{"type":"text","text":"Every Monday, plan my week."}]}`),
		logEvent("ses_a", 2, 1, "agent.tool_use", heldCall("toolu_1", `{"action":"propose","name":"Monday plan","ask":"Plan the week.","why":"Every Monday at 9:00 I plan your week.","schedule":"0 9 * * 1","timeZone":"Europe/Berlin"}`)),
		logEvent("ses_a", 3, 2, "user.tool_confirmation", `{"tool_use_id":"toolu_1","decision":"allow"}`),
		strings.Replace(logEvent("ses_a", 4, 3, "agent.tool_use", heldCall("toolu_2", `{"action":"propose","why":"A helper's call."}`)), `"seq":4,`, `"seq":4,"thread":"thr_1",`, 1),
		logEvent("ses_a", 5, 5, "agent.tool_use", heldCall("toolu_2", `{"action":"propose","name":"Retry test on main","ask":"Run the retry test.","why":"After each push to main I run it and tell you if it fails.","on":{"product":"github","verbs":["push"]}}`)),
		logEvent("ses_a", 6, 6, "user.tool_confirmation", `{"tool_use_id":"toolu_2","decision":"allow"}`),
	}

	var got []*agents.Watch
	for w, err := range c.ListWatches(t.Context(), &agents.ListWatchesParams{Agent: chat, Limit: 500}) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, w)
	}
	at := func(s string) *time.Time {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return &v
	}
	want := []agents.Watch{
		{Kind: agents.WatchRoutine, TriggerID: "trg_1", Name: agents.RoutineName("mine"), Title: "Weekly sign-ups", Why: "Count the sign-ups.", SetBy: agents.SetByPerson,
			Starts: agents.StartsOnSchedule, Schedule: &agents.RoutineSchedule{Days: []time.Weekday{time.Monday}, Hour: 9, Zone: "Europe/Berlin"},
			NextAt: at("2026-10-26T08:00:00Z"), Notify: agents.NotifyAlways, CreatedAt: *at("2026-10-01T09:00:00Z")},
		{Kind: agents.WatchRoutine, TriggerID: "trg_2", Name: agents.RoutineName("offered"), Title: "Monday plan", Why: "Every Monday at 9:00 I plan your week.", SetBy: agents.SetByOffer,
			Origin:  &agents.TriggerOrigin{Kind: agents.TriggerFromProposal, SessionID: "ses_a", ToolUseID: "toolu_1", ConfirmedBy: "alice"},
			Session: &agents.WatchSession{ID: "ses_a", Title: "Plan my week"}, Starts: agents.StartsOnSchedule,
			Schedule: &agents.RoutineSchedule{Days: []time.Weekday{time.Monday}, Hour: 9, Zone: "Europe/Berlin"}, Notify: agents.NotifyAlways, Paused: true,
			CreatedAt: *at("2026-10-22T09:02:00Z")},
		{Kind: agents.WatchEvent, TriggerID: "trg_3", Name: eventName("event"), Title: "Retry test on main", Why: "After each push to main I run it and tell you if it fails.",
			SetBy:   agents.SetByOffer,
			Origin:  &agents.TriggerOrigin{Kind: agents.TriggerFromProposal, SessionID: "ses_a", ToolUseID: "toolu_2", ConfirmedBy: "alice"},
			Session: &agents.WatchSession{ID: "ses_a", Title: "Plan my week"}, On: &agents.EventFilter{Product: "github", Verbs: []string{"push"}, Resources: []string{"acme/field-notes"}},
			Notify: agents.NotifyAlways, CreatedAt: *at("2026-10-22T09:06:00Z")},
		{Kind: agents.WatchEvent, TriggerID: "trg_10", Name: eventName("unoffered"), Title: "Retry test on main", Why: "Run the retry test.", SetBy: agents.SetByPerson,
			On: &agents.EventFilter{Product: "github", Verbs: []string{"push"}, Resources: []string{"acme/field-notes"}}, Notify: agents.NotifyAlways},
		{Kind: agents.WatchWake, TriggerID: "trg_4", Name: "wake-b", Title: "Whether the refund arrived", Why: "Whether the refund arrived", SetBy: agents.SetByAgent,
			Origin:  &agents.TriggerOrigin{Kind: agents.TriggerFromWake, SessionID: "ses_b", ToolUseID: "toolu_3"},
			Session: &agents.WatchSession{ID: "ses_b", Title: "A refund"}, NextAt: at("2026-10-24T07:00:00Z"), Notify: agents.NotifyJudged, Count: 3,
			CreatedAt: *at("2026-10-23T09:00:00Z")},
		{Kind: agents.WatchRoutine, TriggerID: "trg_8", Name: agents.RoutineName("link"), Title: "Tag issues", Why: "Tag each issue.", SetBy: agents.SetByPerson,
			Starts: agents.StartsFromLink, Notify: agents.NotifyAlways},
	}
	if len(got) != len(want) {
		t.Fatalf("read %d watches, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		g := *got[i]
		if len(g.RawJSON()) == 0 {
			t.Errorf("%s keeps no JSON", g.TriggerID)
		}
		g = agents.Watch{Kind: g.Kind, TriggerID: g.TriggerID, Name: g.Name, Title: g.Title, Why: g.Why, SetBy: g.SetBy, Origin: g.Origin, Session: g.Session,
			Starts: g.Starts, Schedule: g.Schedule, On: g.On, NextAt: g.NextAt, Notify: g.Notify, Paused: g.Paused, Count: g.Count,
			LastRunAt: g.LastRunAt, LastSessionID: g.LastSessionID, CreatedAt: g.CreatedAt}
		if !reflect.DeepEqual(g, want[i]) {
			t.Errorf("watch %d reads\n%+v\nwant\n%+v", i, g, want[i])
		}
	}
	wantCalls := []string{
		"GET /v1/agents/sessions/ses_a",
		"GET /v1/agents/sessions/ses_a/events?limit=500",
		"GET /v1/agents/sessions/ses_b",
		"GET /v1/agents/sessions/ses_c",
		"GET /v1/agents/triggers?limit=500",
	}
	if calls := api.received(); !slices.Equal(calls, wantCalls) {
		t.Errorf("read\n%s\nwant\n%s", strings.Join(calls, "\n"), strings.Join(wantCalls, "\n"))
	}

	whys := map[string]string{}
	for _, w := range got {
		whys[w.TriggerID] = w.Why
	}
	page, err := c.ListWatchesPage(t.Context(), &agents.ListWatchesParams{Agent: chat, Limit: 500, KnownWhys: whys})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != len(want) || page.Items[2].Why != want[2].Why || page.NextCursor != "" {
		t.Errorf("the reading that knows the whys is %+v", page)
	}
	for _, call := range api.received() {
		if strings.HasSuffix(call, "/events?limit=500") {
			t.Errorf("the reading that knows the whys read the log: %s", call)
		}
	}
}

// TestALongLogIsReadUpToTheOffer: an offer's call past the first page of
// its log is found on the next; a call the log no longer holds stops the
// read at the first event after its trigger was made, leaves the
// routine's instruction as its why, and reads no further page.
func TestALongLogIsReadUpToTheOffer(t *testing.T) {
	api, c := newWatchAPI(t)
	api.triggers = []map[string]any{routineTrigger(chat, "far", "Weekly sign-ups", "Count the sign-ups.", offered("trg_1", "ses_a", "toolu_far", "2026-10-22T19:00:00Z"), nil)}
	api.sessions["ses_a"] = sessionOf("ses_a", chat, "Sign-ups", "")
	for seq := 1; seq <= 1200; seq++ {
		payload := `{"sender":{"subject":"alice","kind":"person"},"content":[{"type":"text","text":"More."}]}`
		typ := "user.message"
		if seq == 550 {
			typ, payload = "agent.tool_use", heldCall("toolu_far", `{"action":"propose","name":"Weekly sign-ups","ask":"Count the sign-ups.","why":"Every Monday I count them."}`)
		}
		api.logs["ses_a"] = append(api.logs["ses_a"], logEvent("ses_a", seq, seq/2, typ, payload))
	}
	page, err := c.ListWatchesPage(t.Context(), &agents.ListWatchesParams{Agent: chat})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Why != "Every Monday I count them." {
		t.Fatalf("read %+v", page.Items)
	}
	var reads []string
	for _, call := range api.received() {
		if strings.Contains(call, "/events") {
			reads = append(reads, call)
		}
	}
	if want := []string{"GET /v1/agents/sessions/ses_a/events?cursor=500&limit=500", "GET /v1/agents/sessions/ses_a/events?limit=500"}; !slices.Equal(reads, want) {
		t.Errorf("read the log as %v, want %v", reads, want)
	}

	// The trigger was made at minute 60, the event of seq 120; the call is
	// not in the log, which is read no further than minute 61.
	api.triggers = []map[string]any{routineTrigger(chat, "gone", "Weekly sign-ups", "Count the sign-ups.", offered("trg_2", "ses_a", "toolu_gone", "2026-10-22T10:00:00Z"), nil)}
	page, err = c.ListWatchesPage(t.Context(), &agents.ListWatchesParams{Agent: chat})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Why != "Count the sign-ups." {
		t.Fatalf("read %+v", page.Items)
	}
	reads = nil
	for _, call := range api.received() {
		if strings.Contains(call, "/events") {
			reads = append(reads, call)
		}
	}
	if want := []string{"GET /v1/agents/sessions/ses_a/events?limit=500"}; !slices.Equal(reads, want) {
		t.Errorf("read the log as %v, want %v", reads, want)
	}
}

// TestASessionGoneLeavesItsTitleEmpty: an offer whose session was deleted
// reads with no title, and with what each run asks as its why, the
// routine's instruction or the event trigger's ask; a wake whose session
// is gone is left out.
func TestASessionGoneLeavesItsTitleEmpty(t *testing.T) {
	api, c := newWatchAPI(t)
	api.triggers = []map[string]any{
		routineTrigger(chat, "offered", "Monday plan", "Plan the week.", offered("trg_1", "ses_gone", "toolu_1", "2026-10-22T09:02:00Z"), nil),
		wakeTrigger("trg_2", "ses_gone2", "toolu_2", "Whether the refund arrived"),
	}
	api.pageSize = 3
	api.triggers = append(api.triggers, eventTrigger(chat, "event", offered("trg_3", "ses_gone", "toolu_3", "2026-10-22T09:02:00Z")))
	page, err := c.ListWatchesPage(t.Context(), &agents.ListWatchesParams{Agent: chat})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("read %d watches: %+v", len(page.Items), page.Items)
	}
	if w := page.Items[0]; w.TriggerID != "trg_1" || w.Why != "Plan the week." || w.Session == nil || *w.Session != (agents.WatchSession{ID: "ses_gone"}) {
		t.Errorf("read %+v, its session %+v", w, w.Session)
	}
	if w := page.Items[1]; w.TriggerID != "trg_3" || w.Kind != agents.WatchEvent || w.Why != "Run the retry test." || w.Session == nil || w.Session.Title != "" {
		t.Errorf("read %+v, its session %+v", w, w.Session)
	}
}

// TestTheWatchesPageThroughTheTriggers: the iterator reads every page of
// triggers, and a page answers the next cursor.
func TestTheWatchesPageThroughTheTriggers(t *testing.T) {
	api, c := newWatchAPI(t)
	for i := range 3 {
		api.triggers = append(api.triggers, routineTrigger(chat, strconv.Itoa(i), "Routine "+strconv.Itoa(i), "Do it.", map[string]any{"id": "trg_" + strconv.Itoa(i)}, nil))
	}
	n := 0
	for _, err := range c.ListWatches(t.Context(), &agents.ListWatchesParams{Agent: chat, Cursor: "ignored"}) {
		if err != nil {
			t.Fatal(err)
		}
		n++
	}
	if n != 3 {
		t.Errorf("listed %d watches", n)
	}
	page, err := c.ListWatchesPage(t.Context(), &agents.ListWatchesParams{Agent: chat})
	if err != nil || len(page.Items) != 2 || page.NextCursor != "2" {
		t.Fatalf("the first page is %+v, %v", page, err)
	}
	page, err = c.ListWatchesPage(t.Context(), &agents.ListWatchesParams{Agent: chat, Cursor: page.NextCursor})
	if err != nil || len(page.Items) != 1 || page.NextCursor != "" {
		t.Errorf("the second page is %+v, %v", page, err)
	}
}

// TestAFailedReadFailsTheWatches: an agent left out is refused before any
// request, and a read of the triggers, a session or a log that fails
// fails the page.
func TestAFailedReadFailsTheWatches(t *testing.T) {
	api, c := newWatchAPI(t)
	for _, params := range []*agents.ListWatchesParams{nil, {}} {
		if _, err := c.ListWatchesPage(t.Context(), params); err == nil {
			t.Errorf("%+v listed watches", params)
		}
	}
	if calls := api.received(); len(calls) != 0 {
		t.Errorf("sent %v", calls)
	}
	api.triggers = []map[string]any{routineTrigger(chat, "offered", "Monday plan", "Plan the week.", offered("trg_1", "ses_a", "toolu_1", "2026-10-22T09:02:00Z"), nil)}
	api.sessions["ses_a"] = sessionOf("ses_a", chat, "Plan my week", "")
	api.logs["ses_a"] = []string{logEvent("ses_a", 1, 1, "agent.tool_use", `{"tool_use_id":7}`)}
	if _, err := c.ListWatchesPage(t.Context(), &agents.ListWatchesParams{Agent: chat}); err == nil {
		t.Error("a call whose payload does not decode read as no error")
	}
	api.change(func() {
		api.logs["ses_a"] = []string{logEvent("ses_a", 1, 1, "agent.tool_use", heldCall("toolu_1", `{"action":"propose","why":"Weekly."}`))}
	})
	for _, path := range []string{"/v1/agents/triggers", "/v1/agents/sessions/ses_a", "/v1/agents/sessions/ses_a/events"} {
		api.change(func() { api.failing = map[string]bool{path: true} })
		_, err := c.ListWatchesPage(t.Context(), &agents.ListWatchesParams{Agent: chat})
		if apiErr := (*latere.Error)(nil); !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusInternalServerError {
			t.Errorf("a failed read of %s: %v", path, err)
		}
	}
	api.change(func() { api.failing = nil })
	if page, err := c.ListWatchesPage(t.Context(), &agents.ListWatchesParams{Agent: chat}); err != nil || page.Items[0].Why != "Weekly." {
		t.Errorf("the reading after the failures is %+v, %v", page, err)
	}
}

// TestAnOffersLogIsReadAsFarAsItMustBe: with no time its trigger was made,
// a log is read past other calls to its offer's, and to its end when the
// call is not there, which leaves the routine's instruction as the why; a
// call of the offer's id that offers nothing fails the page.
func TestAnOffersLogIsReadAsFarAsItMustBe(t *testing.T) {
	api, c := newWatchAPI(t)
	api.triggers = []map[string]any{routineTrigger(chat, "undated", "Monday plan", "Plan the week.", offered("trg_1", "ses_a", "toolu_1", ""), nil)}
	api.sessions["ses_a"] = sessionOf("ses_a", chat, "Plan my week", "")
	api.logs["ses_a"] = []string{
		logEvent("ses_a", 1, 0, "agent.tool_use", `{"tool_use_id":"toolu_0","name":"bash","input":{"command":"date"}}`),
		logEvent("ses_a", 2, 900, "agent.tool_use", heldCall("toolu_1", `{"action":"propose","name":"Monday plan","why":"Every Monday I plan your week."}`)),
	}
	page, err := c.ListWatchesPage(t.Context(), &agents.ListWatchesParams{Agent: chat})
	if err != nil || len(page.Items) != 1 || page.Items[0].Why != "Every Monday I plan your week." {
		t.Fatalf("read %+v, %v", page, err)
	}
	api.logs["ses_a"] = api.logs["ses_a"][:1]
	page, err = c.ListWatchesPage(t.Context(), &agents.ListWatchesParams{Agent: chat})
	if err != nil || len(page.Items) != 1 || page.Items[0].Why != "Plan the week." {
		t.Fatalf("a log without the call read %+v, %v", page, err)
	}
	api.logs["ses_a"] = []string{logEvent("ses_a", 1, 0, "agent.tool_use", heldCall("toolu_1", `{"action":"list"}`))}
	if _, err := c.ListWatchesPage(t.Context(), &agents.ListWatchesParams{Agent: chat}); err == nil {
		t.Error("a call of the offer's id that offers nothing read as no error")
	}
}

// TestAFailureStartsNoMoreReads: once a read of a page fails, the reads
// waiting for their turn are not sent.
func TestAFailureStartsNoMoreReads(t *testing.T) {
	api, c := newWatchAPI(t)
	api.pageSize, api.slow = 10, 200*time.Millisecond
	for i := range 6 {
		id := "ses_" + strconv.Itoa(i)
		api.triggers = append(api.triggers, wakeTrigger("trg_"+strconv.Itoa(i), id, "toolu_"+strconv.Itoa(i), "Look again"))
		api.sessions[id] = sessionOf(id, chat, "A watch", "")
	}
	api.failing["/v1/agents/sessions/ses_0"] = true
	if _, err := c.ListWatchesPage(t.Context(), &agents.ListWatchesParams{Agent: chat}); err == nil {
		t.Fatal("a failed read of a session read as no error")
	}
	for _, call := range api.received() {
		if strings.HasSuffix(call, "/ses_4") || strings.HasSuffix(call, "/ses_5") {
			t.Errorf("read %s after a read failed", call)
		}
	}
}

// startedBy is a session of agent, with its pending wake, that the
// trigger triggerID started; none when triggerID is empty.
func startedBy(id, agent, wake, triggerID string) string {
	s := sessionOf(id, agent, "Started", wake)
	if triggerID == "" {
		return s
	}
	return strings.Replace(s, `"status":"idle"`, fmt.Sprintf(`"trigger_id":%q,"status":"idle"`, triggerID), 1)
}

// TestAWatchCarriesTheWeekItCountsToward: a routine and an event watch
// carry their own allowance, week and skipped firings; a wake carries the
// allowance and week of the trigger that started its session, read from
// the page when it is there and else once per trigger, none in a session a
// person started, and none when that trigger is gone; its own skipped
// firings are its own.
func TestAWatchCarriesTheWeekItCountsToward(t *testing.T) {
	api, c := newWatchAPI(t)
	api.pageSize = 6
	week := map[string]any{"windowDays": 7, "spentUsdMicro": 1_500_000, "perWeekUsdMicro": 2_000_000, "perLookUsdMicro": 300_000,
		"expectedLooks": 3, "projectedUsdMicro": 2_400_000}
	other := map[string]any{"windowDays": 7, "spentUsdMicro": 900_000, "expectedLooks": 1}
	budget := func(perWeek int) func(spec map[string]any) {
		return func(spec map[string]any) { spec["budget"] = map[string]any{"perWeek": perWeek} }
	}
	skipped := func(status map[string]any, n int) map[string]any {
		status["counts"] = map[string]any{"started": 1, "continued": 0, "held": 0, "filtered": 0, "skippedActive": 0, "skippedBusy": 0,
			"skippedLate": 0, "refused": 0, "failed": 0, "skippedBudget": n}
		return status
	}
	event := eventTrigger(chat, "event", map[string]any{"id": "trg_2", "spend": other})
	event["spec"].(map[string]any)["budget"] = map[string]any{"perWeek": 1_000_000}
	api.triggers = []map[string]any{
		routineTrigger(chat, "inbox", "Inbox", "Look at the inbox.", skipped(map[string]any{"id": "trg_1", "spend": week}, 2), budget(2_000_000)),
		event,
		wakeTrigger("trg_4", "ses_b", "toolu_3", "Whether the refund arrived"),
		wakeTrigger("trg_5", "ses_c", "toolu_4", "Whether the build passed"),
		wakeTrigger("trg_6", "ses_d", "toolu_5", "Whether the parcel came"),
		wakeTrigger("trg_8", "ses_e", "toolu_6", "Whether they answered"),
		// On the next page.
		routineTrigger(chat, "build", "Build", "Check the build.", map[string]any{"id": "trg_7", "spend": other}, budget(4_000_000)),
	}
	skipped(api.triggers[3]["status"].(map[string]any), 1)
	api.sessions["ses_b"] = startedBy("ses_b", chat, `{"trigger_id":"trg_4","at":"2026-10-24T07:00:00Z","title":"Whether the refund arrived","count":1}`, "trg_1")
	api.sessions["ses_c"] = startedBy("ses_c", chat, `{"trigger_id":"trg_5","at":"2026-10-24T07:00:00Z","title":"Whether the build passed","count":1}`, "trg_7")
	api.sessions["ses_d"] = startedBy("ses_d", chat, "", "trg_7")
	api.sessions["ses_e"] = startedBy("ses_e", chat, "", "")
	page, err := c.ListWatchesPage(t.Context(), &agents.ListWatchesParams{Agent: chat})
	if err != nil {
		t.Fatal(err)
	}
	type read struct {
		countsToward     string
		perWeek, skipped int64
		spent            int64
	}
	got := map[string]read{}
	for _, w := range page.Items {
		r := read{countsToward: w.CountsToward, perWeek: w.PerWeekMicroUSD, skipped: w.SkippedBudget, spent: -1}
		if w.Spend != nil {
			r.spent = w.Spend.SpentMicroUSD
		}
		got[w.TriggerID] = r
	}
	want := map[string]read{
		"trg_1": {"trg_1", 2_000_000, 2, 1_500_000},
		"trg_2": {"trg_2", 1_000_000, 0, 900_000},
		"trg_4": {"trg_1", 2_000_000, 0, 1_500_000},
		"trg_5": {"trg_7", 4_000_000, 1, 900_000},
		"trg_6": {"trg_7", 4_000_000, 0, 900_000},
		"trg_8": {"", 0, 0, -1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the watches read\n%+v\nwant\n%+v", got, want)
	}
	for _, w := range page.Items {
		if w.TriggerID == "trg_4" && !w.Spend.AheadOfPace() {
			t.Errorf("the wake's week is not ahead of pace: %+v", w.Spend)
		}
	}
	reads := 0
	for _, call := range api.received() {
		if strings.HasPrefix(call, "GET /v1/agents/triggers/") {
			reads++
			if call != "GET /v1/agents/triggers/trg_7" {
				t.Errorf("read %s", call)
			}
		}
	}
	if reads != 1 {
		t.Errorf("read the trigger on the next page %d times", reads)
	}

	// The trigger that started a wake's session is gone.
	api.change(func() { api.triggers = api.triggers[:6] })
	page, err = c.ListWatchesPage(t.Context(), &agents.ListWatchesParams{Agent: chat})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range page.Items {
		if w.TriggerID == "trg_5" && (w.CountsToward != "trg_7" || w.Spend != nil || w.PerWeekMicroUSD != 0) {
			t.Errorf("a wake whose trigger is gone reads %+v", w)
		}
	}

	// A failed read of that trigger fails the page.
	api.change(func() { api.failing["/v1/agents/triggers/trg_7"] = true })
	if _, err := c.ListWatchesPage(t.Context(), &agents.ListWatchesParams{Agent: chat}); err == nil {
		t.Error("a failed read of the trigger a wake counts toward read as a page")
	}
}

// TestAPausedWatchSaysWhoPausedIt: a watch an agent paused reads the
// sentence it gave, the person it paused for and the call; one a person
// paused through the API reads them alone; one that runs reads none; and
// a trigger reads the firings skipped for an ended target.
func TestAPausedWatchSaysWhoPausedIt(t *testing.T) {
	api, c := newWatchAPI(t)
	api.pageSize = 10
	suspend := func(spec map[string]any) { spec["suspend"] = true }
	byAgent := map[string]any{"id": "trg_1", "paused": map[string]any{"why": "It reached 79 EUR.", "by": "alice", "sessionId": "ses_a", "toolUseId": "toolu_9",
		"at": "2026-10-23T09:00:00Z"}}
	byPerson := map[string]any{"id": "trg_2", "paused": map[string]any{"by": "alice", "at": "2026-10-23T10:00:00Z"},
		"counts": map[string]any{"started": 1, "continued": 0, "held": 0, "filtered": 0, "skippedActive": 0, "skippedBusy": 0, "skippedLate": 0,
			"skippedEnded": 4, "refused": 0, "failed": 0}}
	api.triggers = []map[string]any{
		routineTrigger(chat, "price", "Flight price", "Check the price.", byAgent, suspend),
		routineTrigger(chat, "news", "News", "Read the news.", byPerson, suspend),
		routineTrigger(chat, "inbox", "Inbox", "Look at the inbox.", map[string]any{"id": "trg_3"}, nil),
	}
	page, err := c.ListWatchesPage(t.Context(), &agents.ListWatchesParams{Agent: chat})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 {
		t.Fatalf("read %d watches", len(page.Items))
	}
	agent, person, running := page.Items[0].Pause, page.Items[1].Pause, page.Items[2].Pause
	want := &agents.TriggerPause{Why: "It reached 79 EUR.", By: "alice", SessionID: "ses_a", ToolUseID: "toolu_9", At: time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)}
	if !reflect.DeepEqual(agent, want) || !agent.ByAgent() || !page.Items[0].Paused {
		t.Errorf("a watch an agent paused reads %+v", agent)
	}
	if person == nil || person.By != "alice" || person.Why != "" || person.ByAgent() {
		t.Errorf("a watch a person paused reads %+v", person)
	}
	if running != nil || running.ByAgent() {
		t.Errorf("a running watch reads the pause %+v", running)
	}
	var tr agents.Trigger
	raw, err := json.Marshal(api.triggers[1])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &tr); err != nil {
		t.Fatal(err)
	}
	if tr.Status.Counts == nil || tr.Status.Counts.SkippedEnded != 4 || tr.Status.Paused == nil {
		t.Errorf("the trigger reads %+v", tr.Status)
	}
}
