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
	"strings"
	"testing"
	"time"

	"latere.ai/x/sdk-go/agents"
	"latere.ai/x/sdk-go/latere"
)

// call is one request the test server received.
type call struct {
	Method, Path, Query, Key string
	Body                     map[string]any
}

// fakeAPI answers each request with the body routes holds for its method
// and path, and records the requests.
func fakeAPI(t *testing.T, routes map[string]string) (*agents.Client, *[]call) {
	t.Helper()
	var calls []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := call{Method: r.Method, Path: r.URL.EscapedPath(), Query: r.URL.RawQuery, Key: r.Header.Get("Idempotency-Key")}
		if raw, err := io.ReadAll(r.Body); err != nil {
			t.Error(err)
		} else if len(raw) > 0 {
			if err := json.Unmarshal(raw, &c.Body); err != nil {
				t.Errorf("a body that is not JSON: %s", raw)
			}
		}
		calls = append(calls, c)
		body, ok := routes[r.Method+" "+r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"error":{"code":"not_found","message":"No such session."}}`)
			return
		}
		if body == "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	cfg, err := latere.NewConfig(latere.WithBaseURL(srv.URL), latere.WithTokenSource(latere.StaticToken("key_test")), latere.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	return agents.New(cfg), &calls
}

const session = `{"id":"ses_1","agent":{"id":"agent_1","name":"release-notes","version":3},"title":"Notes","initiator":{"subject":"alice","kind":"person"},
"runner":"hosted","status":"idle","stop_reason":"end_turn","turn":1,"last_seq":5,"model":{"name":"tier/quick"},"machine":{"kind":"cella","image":"base"},
"attended":true,"metadata":{"project":"p1"},"parent":{"session_id":"ses_0","seq":4},"root":"ses_0","budget":{"spent_cost_usd_micro":1200},
"created_at":"2026-09-22T12:05:00Z","updated_at":"2026-09-22T12:06:00Z","expires_at":"2026-09-29T12:05:00Z"}`

const event = `{"id":"evt_8","seq":8,"session_id":"ses_1","type":"user.message","time":"2026-09-22T12:12:00Z","payload":{"sender":{"subject":"alice","kind":"person"},"content":[{"type":"text","text":"Hi"}]}}`

func TestGet(t *testing.T) {
	s, calls := fakeAPI(t, map[string]string{"GET /v1/agents/sessions/ses_1": session})
	got, err := s.GetSession(t.Context(), "ses_1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "ses_1" || got.Agent.Name != "release-notes" || got.StopReason != agents.StopEndTurn || got.LastSeq != 5 ||
		got.Model.Name != "tier/quick" || got.Parent.Seq != 4 || got.Budget.SpentMicroUSD != 1200 || got.ExpiresAt.IsZero() {
		t.Errorf("decoded %+v", got)
	}
	if (*calls)[0].Method != http.MethodGet {
		t.Errorf("sent %+v", (*calls)[0])
	}
	if _, err := s.GetSession(t.Context(), "ses_missing"); !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("a missing session: %v", err)
	}
	if _, err := s.GetSession(t.Context(), ""); err == nil {
		t.Error("an empty id was sent")
	}
}

func TestListPagesThroughSessions(t *testing.T) {
	s, calls := fakeAPI(t, map[string]string{"GET /v1/agents/sessions": `{"items":[` + session + `],"next_cursor":""}`})
	var n int
	for got, err := range s.ListSessions(t.Context(), &agents.ListSessionsParams{
		Agent: "latere-chat", Status: agents.StatusIdle, Runner: agents.RunnerHosted, Archived: agents.ArchivedIncluded,
		Root: "ses_0", Parent: "ses_0", Trigger: "trg_1", GroupByTree: true, Limit: 20,
		MetadataKey: "project", MetadataValue: "p1", Cursor: "ignored",
	}) {
		if err != nil {
			t.Fatal(err)
		}
		if got.ID != "ses_1" {
			t.Errorf("listed %+v", got)
		}
		n++
	}
	if n != 1 {
		t.Errorf("listed %d sessions", n)
	}
	q := (*calls)[0].Query
	for _, want := range []string{"agent=latere-chat", "status=idle", "runner=hosted", "archived=any", "root=ses_0", "parent=ses_0", "trigger=trg_1", "group=tree", "limit=20", "metadata.project=p1"} {
		if !strings.Contains(q, want) {
			t.Errorf("query %q lacks %q", q, want)
		}
	}
	if strings.Contains(q, "cursor") {
		t.Errorf("List sent the cursor: %q", q)
	}
	page, err := s.ListSessionsPage(t.Context(), &agents.ListSessionsParams{Cursor: "c2"})
	if err != nil || len(page.Items) != 1 || !strings.Contains((*calls)[1].Query, "cursor=c2") {
		t.Errorf("ListPage: %+v, %v, query %q", page, err, (*calls)[1].Query)
	}
	if _, err := s.ListSessionsPage(t.Context(), nil); err != nil {
		t.Errorf("ListPage(nil): %v", err)
	}
	if (*calls)[2].Query != "" {
		t.Errorf("a list with no parameters sent %q", (*calls)[2].Query)
	}
}

func TestListYieldsAnError(t *testing.T) {
	s, _ := fakeAPI(t, map[string]string{})
	for _, err := range s.ListSessions(t.Context(), nil) {
		if !errors.Is(err, latere.ErrNotFound) {
			t.Errorf("yielded %v", err)
		}
	}
	if _, err := s.ListSessionsPage(t.Context(), nil); err == nil {
		t.Error("ListPage succeeded against nothing")
	}
}

func TestCreate(t *testing.T) {
	s, calls := fakeAPI(t, map[string]string{"POST /v1/agents/sessions": session})
	got, err := s.CreateSession(t.Context(), &agents.CreateSessionParams{
		Agent: "release-notes", Message: "Write the notes.", Title: "Notes", Attended: true,
		Metadata: map[string]string{"project": "p1"}, Machine: &agents.Machine{Kind: agents.MachineDevice, Environment: "dev_1", Workdir: "/work"},
		IdempotencyKey: "create_1",
	})
	if err != nil || got.ID != "ses_1" {
		t.Fatalf("Create: %+v, %v", got, err)
	}
	c := (*calls)[0]
	if c.Key != "create_1" || c.Body["agent"] != "release-notes" || c.Body["message"] != "Write the notes." || c.Body["attended"] != true {
		t.Errorf("sent %+v", c)
	}
	if m, ok := c.Body["machine"].(map[string]any); !ok || m["kind"] != "device" || m["environment"] != "dev_1" {
		t.Errorf("machine %v", c.Body["machine"])
	}
	if _, err := s.CreateSession(t.Context(), &agents.CreateSessionParams{Agent: "a"}); err != nil {
		t.Fatal(err)
	}
	if c := (*calls)[1]; c.Key == "" || len(c.Body) != 1 {
		t.Errorf("a bare create sent key %q body %v, want a generated key and the agent alone", c.Key, c.Body)
	}
	if _, err := s.CreateSession(t.Context(), nil); err == nil {
		t.Error("a create without an agent was sent")
	}
}

func TestArchiveAndFork(t *testing.T) {
	s, calls := fakeAPI(t, map[string]string{
		"POST /v1/agents/sessions/ses_1/archive": session,
		"POST /v1/agents/sessions/ses_1/fork":    session,
	})
	if _, err := s.ArchiveSession(t.Context(), "ses_1"); err != nil {
		t.Fatal(err)
	}
	if (*calls)[0].Key == "" {
		t.Error("archive sent no idempotency key")
	}
	if _, err := s.ForkSession(t.Context(), "ses_1", &agents.ForkSessionParams{BeforeSeq: 8, Title: "Again", Message: []agents.ContentBlock{agents.Text("Retry")}, Attended: true, IdempotencyKey: "fork_1"}); err != nil {
		t.Fatal(err)
	}
	c := (*calls)[1]
	if c.Key != "fork_1" || c.Body["before_seq"] != float64(8) || c.Body["title"] != "Again" || c.Body["attended"] != true {
		t.Errorf("fork sent %+v", c)
	}
	if msg, ok := c.Body["message"].(map[string]any); !ok || fmt.Sprint(msg["content"]) != "[map[text:Retry type:text]]" {
		t.Errorf("fork message %v", c.Body["message"])
	}
	if _, err := s.ForkSession(t.Context(), "ses_1", nil); err != nil {
		t.Errorf("a bare fork: %v", err)
	}
	if _, err := s.ArchiveSession(t.Context(), ""); err == nil {
		t.Error("archive of no id was sent")
	}
	if _, err := s.ForkSession(t.Context(), "", nil); err == nil {
		t.Error("fork of no id was sent")
	}
	if _, err := s.ArchiveSession(t.Context(), "ses_missing"); err == nil {
		t.Error("archive of a missing session succeeded")
	}
	if _, err := s.ForkSession(t.Context(), "ses_missing", nil); err == nil {
		t.Error("fork of a missing session succeeded")
	}
	if _, err := s.CreateSession(t.Context(), &agents.CreateSessionParams{Agent: "missing-route"}); err != nil {
		var apiErr *latere.Error
		if !errors.As(err, &apiErr) {
			t.Errorf("create's error is %T", err)
		}
	}
}

func TestEvents(t *testing.T) {
	s, calls := fakeAPI(t, map[string]string{"GET /v1/agents/sessions/ses_1/events": `{"items":[` + event + `]}`})
	var got []*agents.Event
	for e, err := range s.ListEvents(t.Context(), "ses_1", &agents.ListEventsParams{FromSeq: 8, Limit: 10}) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, e)
	}
	if len(got) != 1 || got[0].Seq != 8 || got[0].Type != agents.EventUserMessage {
		t.Fatalf("events %+v", got)
	}
	var msg agents.UserMessage
	if err := got[0].Decode(&msg); err != nil || msg.Content[0].Text != "Hi" || msg.Sender.Kind != agents.SenderPerson {
		t.Errorf("payload %+v, %v", msg, err)
	}
	if q := (*calls)[0].Query; q != "from_seq=8&limit=10" {
		t.Errorf("query %q", q)
	}
	// The first sequence is sent as given, as StreamEvents sends it.
	if _, err := s.ListEventsPage(t.Context(), "ses_1", &agents.ListEventsParams{FromSeq: 1}); err != nil || (*calls)[1].Query != "from_seq=1" {
		t.Errorf("FromSeq 1: %v, query %q", err, (*calls)[1].Query)
	}
	*calls = (*calls)[:1]
	if _, err := s.ListEventsPage(t.Context(), "ses_1", &agents.ListEventsParams{Cursor: "c2"}); err != nil || (*calls)[1].Query != "cursor=c2" {
		t.Errorf("EventsPage: %v, query %q", err, (*calls)[1].Query)
	}
	if _, err := s.ListEventsPage(t.Context(), "", nil); err == nil {
		t.Error("events of no id were read")
	}
	if _, err := s.ListEventsPage(t.Context(), "ses_missing", nil); err == nil {
		t.Error("events of a missing session succeeded")
	}
	var bad agents.Event
	bad.Payload = json.RawMessage(`[`)
	if err := bad.Decode(&msg); err == nil {
		t.Error("a broken payload decoded")
	}
}

func TestSendingEvents(t *testing.T) {
	s, calls := fakeAPI(t, map[string]string{"POST /v1/agents/sessions/ses_1/events": event})
	if _, err := s.SendMessage(t.Context(), "ses_1", &agents.SendMessageParams{Content: []agents.ContentBlock{agents.Text("Hi")}, IdempotencyKey: "m1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Interrupt(t.Context(), "ses_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AnswerQuestion(t.Context(), "ses_1", &agents.AnswerQuestionParams{ToolUseID: "call_1", Answers: []agents.Answer{{Selected: []string{"Yes"}}, {Text: "Mine"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AnswerQuestion(t.Context(), "ses_1", &agents.AnswerQuestionParams{ToolUseID: "call_2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConfirmTool(t.Context(), "ses_1", &agents.ConfirmToolParams{ToolUseID: "call_3", Decision: agents.Allow, Note: "fine", Remember: "web_fetch(domain:example.com)"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConfirmTool(t.Context(), "ses_1", &agents.ConfirmToolParams{ApprovalID: "apr_1", Decision: agents.Deny}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`{"payload":{"content":[{"text":"Hi","type":"text"}]},"type":"user.message"}`,
		`{"payload":{},"type":"user.interrupt"}`,
		`{"payload":{"answers":[{"selected":["Yes"]},{"text":"Mine"}],"tool_use_id":"call_1"},"type":"user.answer"}`,
		`{"payload":{"answers":[],"tool_use_id":"call_2"},"type":"user.answer"}`,
		`{"payload":{"decision":"allow","note":"fine","remember":"web_fetch(domain:example.com)","tool_use_id":"call_3"},"type":"user.tool_confirmation"}`,
		`{"payload":{"approval_id":"apr_1","decision":"deny"},"type":"user.tool_confirmation"}`,
	}
	for i, w := range want {
		got, err := json.Marshal((*calls)[i].Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != w {
			t.Errorf("event %d sent %s, want %s", i, got, w)
		}
		if (*calls)[i].Key == "" {
			t.Errorf("event %d sent no idempotency key", i)
		}
	}
	if (*calls)[0].Key != "m1" {
		t.Errorf("the message's key is %q", (*calls)[0].Key)
	}
	for name, err := range map[string]error{
		"no content":    second(s.SendMessage(t.Context(), "ses_1", &agents.SendMessageParams{})),
		"no params":     second(s.SendMessage(t.Context(), "ses_1", nil)),
		"no call":       second(s.AnswerQuestion(t.Context(), "ses_1", &agents.AnswerQuestionParams{})),
		"both ids":      second(s.ConfirmTool(t.Context(), "ses_1", &agents.ConfirmToolParams{ToolUseID: "a", ApprovalID: "b", Decision: agents.Allow})),
		"no id":         second(s.ConfirmTool(t.Context(), "ses_1", &agents.ConfirmToolParams{Decision: agents.Allow})),
		"bad decision":  second(s.ConfirmTool(t.Context(), "ses_1", &agents.ConfirmToolParams{ToolUseID: "a", Decision: "maybe"})),
		"empty session": second(s.Interrupt(t.Context(), "")),
		"missing":       second(s.Interrupt(t.Context(), "ses_missing")),
	} {
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func second[T any](_ T, err error) error { return err }

func TestQuestions(t *testing.T) {
	q := agents.ToolUse{ToolUseID: "call_1", Name: "question", Input: json.RawMessage(`{"questions":[{"question":"Which?","header":"Pick","options":[{"label":"A","description":"a","recommended":true},{"label":"B","description":"b"}]}]}`)}
	got, err := q.Questions()
	if err != nil || len(got) != 1 || got[0].Options[0].Label != "A" || !got[0].Options[0].Recommended {
		t.Errorf("questions %+v, %v", got, err)
	}
	if _, err := (&agents.ToolUse{Name: "bash"}).Questions(); err == nil {
		t.Error("a bash call yielded questions")
	}
	if _, err := (&agents.ToolUse{Name: "question", Input: json.RawMessage(`[`)}).Questions(); err == nil {
		t.Error("a broken input yielded questions")
	}
}

func TestEndUnarchiveAndDelete(t *testing.T) {
	s, calls := fakeAPI(t, map[string]string{
		"POST /v1/agents/sessions/ses_1/end":       session,
		"POST /v1/agents/sessions/ses_1/unarchive": session,
		"DELETE /v1/agents/sessions/ses_1":         "",
	})
	if _, err := s.EndSession(t.Context(), "ses_1", agents.StopCompleted); err != nil {
		t.Fatal(err)
	}
	if c := (*calls)[0]; c.Body["reason"] != "completed" || c.Key == "" {
		t.Errorf("end sent %+v", c)
	}
	if _, err := s.UnarchiveSession(t.Context(), "ses_1"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSession(t.Context(), "ses_1"); err != nil {
		t.Fatal(err)
	}
	if c := (*calls)[2]; c.Method != http.MethodDelete || c.Path != "/v1/agents/sessions/ses_1" {
		t.Errorf("delete sent %+v", c)
	}
	n := len(*calls)
	for name, err := range map[string]error{
		"end with a running reason": second(s.EndSession(t.Context(), "ses_1", agents.StopEndTurn)),
		"end of no id":              second(s.EndSession(t.Context(), "", agents.StopCanceled)),
		"delete of no id":           s.DeleteSession(t.Context(), ""),
	} {
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if len(*calls) != n {
		t.Errorf("a refused call was sent: %+v", (*calls)[n:])
	}
	if err := s.DeleteSession(t.Context(), "ses_missing"); !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("delete of a missing session: %v", err)
	}
}

func TestSendToolResult(t *testing.T) {
	s, calls := fakeAPI(t, map[string]string{"POST /v1/agents/sessions/ses_1/events": event})
	if _, err := s.SendToolResult(t.Context(), "ses_1", &agents.SendToolResultParams{ToolUseID: "call_9", Content: []agents.ContentBlock{agents.Text("no such file")}, IsError: true, IdempotencyKey: "r1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SendToolResult(t.Context(), "ses_1", &agents.SendToolResultParams{ToolUseID: "call_10"}); err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{
		`{"payload":{"content":[{"text":"no such file","type":"text"}],"is_error":true,"tool_use_id":"call_9"},"type":"user.tool_result"}`,
		`{"payload":{"content":[],"tool_use_id":"call_10"},"type":"user.tool_result"}`,
	} {
		got, err := json.Marshal((*calls)[i].Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("result %d sent %s, want %s", i, got, want)
		}
	}
	if (*calls)[0].Key != "r1" || (*calls)[1].Key == "" {
		t.Errorf("keys %q and %q", (*calls)[0].Key, (*calls)[1].Key)
	}
	if _, err := s.SendToolResult(t.Context(), "ses_1", &agents.SendToolResultParams{}); err == nil {
		t.Error("a result without a call was sent")
	}
}

func TestPayloads(t *testing.T) {
	decode := func(typ agents.EventType, payload string, v any) {
		t.Helper()
		e := agents.Event{ID: "evt_1", Type: typ, Payload: json.RawMessage(payload)}
		if err := e.Decode(v); err != nil {
			t.Fatal(err)
		}
	}
	var m agents.AgentMessage
	decode(agents.EventAgentMessage, `{"message":{"role":"assistant","blocks":[{"type":"thinking","text":"Hmm."},{"type":"text","text":"The notes "},{"type":"tool_use","tool_use":{"id":"call_1","name":"bash"}},{"type":"text","text":"are in NOTES.md."}]},"stop_reason":"end_turn"}`, &m)
	if m.Text() != "The notes are in NOTES.md." || m.StopReason != agents.StopEndTurn || m.Message.Role != "assistant" || m.Message.Blocks[0].Type != agents.BlockThinking {
		t.Errorf("agent message %+v, text %q", m, m.Text())
	}
	var r agents.ToolResult
	decode(agents.EventToolResult, `{"tool_use_id":"call_1","content":[{"type":"text","text":"exit 1"}],"is_error":true,"outcome":"error"}`, &r)
	if r.ToolUseID != "call_1" || !r.IsError || r.Outcome != "error" || r.Text() != "exit 1" || r.Sender != nil {
		t.Errorf("tool result %+v", r)
	}
	var e agents.SessionError
	decode(agents.EventSessionError, `{"code":"model_busy","message":"The model is busy right now.","retryable":true}`, &e)
	if e.Code != "model_busy" || !e.Retryable {
		t.Errorf("session error %+v", e)
	}
	var u agents.ToolUse
	decode(agents.EventAgentToolUse, `{"tool_use_id":"call_2","name":"lookup","input":{},"client":true}`, &u)
	if !u.Client {
		t.Errorf("a client tool call decoded as %+v", u)
	}
}

func TestAClientWithoutAConfig(t *testing.T) {
	for _, cfg := range []*latere.Config{nil, {}} {
		if _, err := agents.New(cfg).GetSession(t.Context(), "ses_1"); err == nil || !strings.Contains(err.Error(), "NewConfig") {
			t.Errorf("a call without a built Config: %v", err)
		}
	}
}

func TestUpdateAndResume(t *testing.T) {
	s, calls := fakeAPI(t, map[string]string{
		"PATCH /v1/agents/sessions/ses_1":       session,
		"POST /v1/agents/sessions/ses_1/resume": session,
	})
	got, err := s.UpdateSession(t.Context(), "ses_1", &agents.UpdateSessionParams{Model: &agents.ModelChange{Name: "tier/thorough"}, Policy: agents.PolicyConfirm, Title: "Notes", Metadata: map[string]string{"p": "1"}})
	if err != nil || got.ID != "ses_1" {
		t.Fatalf("update: %+v, %v", got, err)
	}
	body, err := json.Marshal((*calls)[0].Body)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"metadata":{"p":"1"},"model":{"name":"tier/thorough"},"policy":{"mode":"confirm"},"title":"Notes"}`; string(body) != want {
		t.Errorf("update sent %s, want %s", body, want)
	}
	if (*calls)[0].Method != http.MethodPatch || (*calls)[0].Key != "" {
		t.Errorf("update was %s with key %q; want a PATCH with none, which the API does not read", (*calls)[0].Method, (*calls)[0].Key)
	}
	if _, err := s.ResumeSession(t.Context(), "ses_1", &agents.ResumeSessionParams{MaxCostUSDMicro: 5000000, Reason: "budget_raised"}); err != nil {
		t.Fatal(err)
	}
	if c := (*calls)[1]; c.Body["reason"] != "budget_raised" || c.Body["max_cost_usd_micro"] != float64(5000000) {
		t.Errorf("resume sent %+v", c.Body)
	}
	if _, err := s.ResumeSession(t.Context(), "ses_1", nil); err != nil {
		t.Fatal(err)
	}
	if c := (*calls)[2]; len(c.Body) != 0 {
		t.Errorf("a resume with no parameters sent %+v", c.Body)
	}
	n := len(*calls)
	for name, err := range map[string]error{
		"update of no id":         second(s.UpdateSession(t.Context(), "", &agents.UpdateSessionParams{Title: "x"})),
		"update with no params":   second(s.UpdateSession(t.Context(), "ses_1", nil)),
		"update changing nothing": second(s.UpdateSession(t.Context(), "ses_1", &agents.UpdateSessionParams{})),
		"resume of no id":         second(s.ResumeSession(t.Context(), "", nil)),
	} {
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if len(*calls) != n {
		t.Errorf("a refused call was sent: %+v", (*calls)[n:])
	}
	if got, err := s.UpdateSession(t.Context(), "ses_gone", &agents.UpdateSessionParams{Title: "x"}); got != nil || !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("an update the API refused: %+v, %v; want no session and the 404", got, err)
	}
}

// A model change sends only the members it sets: reasoning alone keeps the
// model, and AgentReasoning sends an empty level, which returns to the
// agent's own. Labels merge: a deleted one is sent as null.
func TestUpdateSessionSendsWhatItChanges(t *testing.T) {
	s, calls := fakeAPI(t, map[string]string{"PATCH /v1/agents/sessions/ses_1": session})
	for name, tc := range map[string]struct {
		params *agents.UpdateSessionParams
		want   string
	}{
		"reasoning alone":        {&agents.UpdateSessionParams{Model: &agents.ModelChange{Reasoning: agents.ReasoningHigh}}, `{"model":{"reasoning":"high"}}`},
		"the agent's own":        {&agents.UpdateSessionParams{Model: &agents.ModelChange{AgentReasoning: true}}, `{"model":{"reasoning":""}}`},
		"name and level":         {&agents.UpdateSessionParams{Model: &agents.ModelChange{Name: "tier/quick", Reasoning: agents.ReasoningLow}}, `{"model":{"name":"tier/quick","reasoning":"low"}}`},
		"labels set and removed": {&agents.UpdateSessionParams{Metadata: map[string]string{"p": "2"}, DeleteMetadata: []string{"old"}}, `{"metadata":{"old":null,"p":"2"}}`},
	} {
		*calls = nil
		if _, err := s.UpdateSession(t.Context(), "ses_1", tc.params); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		body, err := json.Marshal((*calls)[0].Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != tc.want {
			t.Errorf("%s: sent %s, want %s", name, body, tc.want)
		}
	}
	*calls = nil
	for name, params := range map[string]*agents.UpdateSessionParams{
		"a level and the agent's own": {Model: &agents.ModelChange{Reasoning: agents.ReasoningHigh, AgentReasoning: true}},
		"an empty model change":       {Model: &agents.ModelChange{}},
		"a label set and deleted":     {Metadata: map[string]string{"p": "1"}, DeleteMetadata: []string{"p"}},
	} {
		if _, err := s.UpdateSession(t.Context(), "ses_1", params); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("a refused change was sent: %+v", *calls)
	}
}

// An update that only sets values is retried after a server error.
func TestUpdateSessionIsRetried(t *testing.T) {
	var sends int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sends++
		if sends == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		fmt.Fprint(w, session)
	}))
	t.Cleanup(srv.Close)
	cfg, err := latere.NewConfig(latere.WithBaseURL(srv.URL), latere.WithTokenSource(latere.StaticToken("key_test")), latere.WithBackoff(time.Millisecond, time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agents.New(cfg).UpdateSession(t.Context(), "ses_1", &agents.UpdateSessionParams{Title: "x"}); err != nil || sends != 2 {
		t.Errorf("update: %v after %d sends; want success after 2", err, sends)
	}
}

func TestVerdictsAndApprovals(t *testing.T) {
	var u agents.ToolUse
	e := agents.Event{ID: "evt_1", Type: agents.EventAgentToolUse, Payload: json.RawMessage(`{"tool_use_id":"call_3","name":"bash","input":{"command":"rm -r build"},"verdict":"ask","reason":"Deletes files."}`)}
	if err := e.Decode(&u); err != nil {
		t.Fatal(err)
	}
	if u.Verdict != agents.VerdictAsk || u.Reason != "Deletes files." {
		t.Errorf("tool use %+v", u)
	}
	var a agents.ApprovalRequested
	e = agents.Event{ID: "evt_2", Type: agents.EventApprovalRequested, Payload: json.RawMessage(`{"approval_id":"apr_1","tool_use_id":"call_3","source":"egress","destination":{"host":"example.com","port":443},"verdict":"ask","more":2}`)}
	if err := e.Decode(&a); err != nil {
		t.Fatal(err)
	}
	if a.ApprovalID != "apr_1" || a.Destination.Host != "example.com" || a.Destination.Port != 443 || a.Verdict != agents.VerdictAsk || a.More != 2 {
		t.Errorf("approval requested %+v", a)
	}
	var d agents.ApprovalDecided
	e = agents.Event{ID: "evt_3", Type: agents.EventApprovalDecided, Payload: json.RawMessage(`{"approval_id":"apr_1","decision":"allow","by":{"subject":"alice","kind":"person"}}`)}
	if err := e.Decode(&d); err != nil {
		t.Fatal(err)
	}
	if d.ApprovalID != "apr_1" || d.Decision != agents.Allow || d.By == nil || d.By.Subject != "alice" {
		t.Errorf("approval decided %+v", d)
	}
}
