// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents_test

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"latere.ai/x/sdk-go/agents"
	"latere.ai/x/sdk-go/latere"
)

// A call reads the reach it was decided on: each member a word of its
// list, a boundary left out as not known and one sent as false as not
// crossed, and the source the scope rests on.
func TestAToolUseReadsItsScope(t *testing.T) {
	raw := `{"tool_use_id":"toolu_1","name":"bash","input":{"command":"git push --force origin main"},"verdict":"ask","decided_by":"learned",
		"suggestion":{"source":"learned/1","verdict":"ask","approve":0.9,"uncertainty":0.2,"thresholds":{"allow_above":0.65,"block_below":0.35},"audit_rate":0.05,"ask_kind":"wider"},
		"scope":{"undo":"irreversible","crosses":{"remote":true,"public":false},"target_class":"primary","target_kind":"ref","contact":"repeat","source":"service"}}`
	var u agents.ToolUse
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		t.Fatal(err)
	}
	s := u.Scope
	if s == nil || s.Undo != agents.UndoIrreversible || s.TargetClass != agents.TargetPrimary || s.TargetKind != "ref" ||
		s.Contact != agents.ContactRepeat || s.Source != agents.ScopeByService || !s.Known() {
		t.Fatalf("scope %+v", s)
	}
	c := s.Crosses
	if c == nil || c.Remote == nil || !*c.Remote || c.Public == nil || *c.Public || c.Machine != nil || c.Network != nil || c.Organization != nil {
		t.Errorf("crosses %+v", c)
	}
	if u.Suggestion.AskKind != agents.AskWider {
		t.Errorf("ask kind %q", u.Suggestion.AskKind)
	}

	// A shell command without a decision service records its source and
	// no reach, which is not known, not none.
	var bash agents.ToolUse
	if err := json.Unmarshal([]byte(`{"tool_use_id":"toolu_2","name":"bash","input":{},"verdict":"ask","scope":{"source":"topos"}}`), &bash); err != nil {
		t.Fatal(err)
	}
	if bash.Scope == nil || bash.Scope.Source != agents.ScopeByPlatform || bash.Scope.Known() {
		t.Errorf("a command's scope without a service: %+v", bash.Scope)
	}
	// Crosses with no boundary known says nothing.
	if (&agents.CallScope{Source: agents.ScopeByPlatform, Crosses: &agents.Crosses{}}).Known() {
		t.Error("a scope whose crosses know no boundary is known")
	}
	var no *agents.CallScope
	if no.Known() {
		t.Error("a nil scope is known")
	}
	// A call recorded before the API wrote it has none.
	var old agents.ToolUse
	if err := json.Unmarshal([]byte(`{"tool_use_id":"toolu_0","name":"bash","input":{},"verdict":"allow"}`), &old); err != nil || old.Scope != nil {
		t.Errorf("an older call: %+v, %v", old.Scope, err)
	}
}

// The ask kinds of the consent contract read as their constants.
func TestTheAskKindsOfTheCover(t *testing.T) {
	for raw, want := range map[string]agents.AskKind{"short": agents.AskShort, "wider": agents.AskWider, "few": agents.AskFew, "split": agents.AskSplit} {
		var s agents.Suggestion
		if err := json.Unmarshal(fmt.Appendf(nil, `{"source":"learned/1","verdict":"ask","ask_kind":%q}`, raw), &s); err != nil || s.AskKind != want {
			t.Errorf("%s: %q, %v", raw, s.AskKind, err)
		}
	}
}

// A confirmation reads how the person answered, and one without the fields
// reads with none of them.
func TestAConfirmationReadsHowThePersonAnswered(t *testing.T) {
	var c agents.ToolConfirmation
	raw := `{"sender":{"subject":"usr_1","kind":"person"},"tool_use_id":"toolu_1","decision":"allow","answer_ms":4200,"details_opened":true,"suggestion_shown":false,"surface":"notification"}`
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatal(err)
	}
	if c.AnswerMS == nil || *c.AnswerMS != 4200 || c.DetailsOpened == nil || !*c.DetailsOpened || c.SuggestionShown == nil || *c.SuggestionShown || c.Surface != agents.SurfaceNotification {
		t.Errorf("confirmation %+v", c)
	}
	var old agents.ToolConfirmation
	if err := json.Unmarshal([]byte(`{"sender":{"subject":"usr_1","kind":"person"},"tool_use_id":"toolu_1","decision":"deny"}`), &old); err != nil ||
		old.AnswerMS != nil || old.DetailsOpened != nil || old.SuggestionShown != nil || old.Surface != "" {
		t.Errorf("an older confirmation: %+v, %v", old, err)
	}
}

// The event that hands a session's calls back to the person reads its
// counts, the limit it reached and what the session does now, and a call
// decided in the fallback and the turn it stopped read their names.
func TestTheHandBackAfterRefusalsIsRead(t *testing.T) {
	raw := `{"id":"evt_9","seq":9,"session_id":"ses_1","type":"session.denials_reached","time":"2026-11-02T12:00:00Z","payload":{"consecutive":3,"total":5,"limit":"consecutive","effect":"ask"}}`
	var e agents.Event
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		t.Fatal(err)
	}
	if e.Type != agents.EventDenialsReached {
		t.Errorf("type %q", e.Type)
	}
	var d agents.DenialsReached
	if err := e.Decode(&d); err != nil {
		t.Fatal(err)
	}
	if d.Consecutive != 3 || d.Total != 5 || d.Limit != agents.DenialsConsecutive || d.Effect != agents.DenialsAsk {
		t.Errorf("denials %+v", d)
	}
	var u agents.ToolUse
	if err := json.Unmarshal([]byte(`{"tool_use_id":"toolu_4","name":"write","input":{},"verdict":"ask","decided_by":"denials","review_probability":1}`), &u); err != nil || u.DecidedBy != agents.LayerDenials || u.DecidedBy.Learned() {
		t.Errorf("a call in the fallback: %q, %v", u.DecidedBy, err)
	}
	var st agents.SessionStatus
	if err := json.Unmarshal([]byte(`{"status":"idle","stop_reason":"denials"}`), &st); err != nil || st.StopReason != agents.StopDenials {
		t.Errorf("the stop: %+v, %v", st, err)
	}
}

// A confirmation sends how the person answered only where the program set
// it, and each value as it was set.
func TestConfirmToolSendsHowThePersonAnswered(t *testing.T) {
	routes := map[string]string{"POST /v1/agents/sessions/ses_1/events": event}
	s, calls := fakeAPI(t, routes)
	ms, opened, shown := int64(0), false, true
	if _, err := s.ConfirmTool(t.Context(), "ses_1", &agents.ConfirmToolParams{
		ToolUseID: "call_3", Decision: agents.DecisionDeny, AnswerMS: &ms, DetailsOpened: &opened, SuggestionShown: &shown, Surface: agents.SurfaceBulk,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConfirmTool(t.Context(), "ses_1", &agents.ConfirmToolParams{ToolUseID: "call_4", Decision: agents.DecisionAllow, Surface: agents.SurfacePrompt}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 {
		t.Fatalf("sent %d requests", len(*calls))
	}
	all := (*calls)[0].Body["payload"].(map[string]any)
	if all["answer_ms"] != float64(0) || all["details_opened"] != false || all["suggestion_shown"] != true || all["surface"] != "bulk" {
		t.Errorf("sent %v", all)
	}
	one := (*calls)[1].Body["payload"].(map[string]any)
	for _, name := range []string{"answer_ms", "details_opened", "suggestion_shown"} {
		if _, ok := one[name]; ok {
			t.Errorf("sent %s, which the program did not set: %v", name, one)
		}
	}
	if one["surface"] != "prompt" {
		t.Errorf("sent %v", one)
	}
}

// A value of how the person answered outside its range or its list is
// refused before anything is sent.
func TestConfirmToolRefusesWhatTheServerCannotRead(t *testing.T) {
	s, calls := fakeAPI(t, map[string]string{"POST /v1/agents/sessions/ses_1/events": event})
	for name, ms := range map[string]int64{"negative": -1, "over a day": agents.MaxAnswerMS + 1} {
		if _, err := s.ConfirmTool(t.Context(), "ses_1", &agents.ConfirmToolParams{ToolUseID: "a", Decision: agents.DecisionAllow, AnswerMS: &ms}); err == nil {
			t.Errorf("%s: an answer time of %d was sent", name, ms)
		}
	}
	if _, err := s.ConfirmTool(t.Context(), "ses_1", &agents.ConfirmToolParams{ToolUseID: "a", Decision: agents.DecisionAllow, Surface: "email"}); err == nil {
		t.Error("an unknown surface was sent")
	}
	if len(*calls) != 0 {
		t.Errorf("sent %d requests", len(*calls))
	}
	day := int64(agents.MaxAnswerMS)
	if _, err := s.ConfirmTool(t.Context(), "ses_1", &agents.ConfirmToolParams{ToolUseID: "a", Decision: agents.DecisionAllow, AnswerMS: &day}); err != nil {
		t.Errorf("a day: %v", err)
	}
}

// confirmServer answers a confirmation that says how the person answered
// with refusal, as a server from before the fields does, and any other
// with the recorded event. It records each request's key and payload.
func confirmServer(t *testing.T, refusal string, status int) (*agents.Client, *[]map[string]any, *[]string) {
	t.Helper()
	var payloads []map[string]any
	var keys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		var body struct {
			Payload map[string]any `json:"payload"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Error(err)
		}
		payloads, keys = append(payloads, body.Payload), append(keys, r.Header.Get("Idempotency-Key"))
		w.Header().Set("Content-Type", "application/json")
		if _, fields := body.Payload["surface"]; fields {
			w.WriteHeader(status)
			fmt.Fprint(w, refusal)
			return
		}
		fmt.Fprint(w, event)
	}))
	t.Cleanup(srv.Close)
	cfg, err := latere.NewConfig(latere.WithBaseURL(srv.URL), latere.WithTokenSource(latere.StaticToken("key_test")), latere.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	return agents.New(cfg), &payloads, &keys
}

// A server from before the fields refuses them as invalid_request, and the
// confirmation goes once more without them, under a key derived from the
// first, so the person's decision is recorded.
func TestConfirmToolSendsAgainWithoutTheFieldsAnOlderServerRefuses(t *testing.T) {
	s, payloads, keys := confirmServer(t, `{"error":{"code":"invalid_request","message":"This confirmation has a field the server cannot read."}}`, http.StatusBadRequest)
	ms := int64(1500)
	e, err := s.ConfirmTool(t.Context(), "ses_1", &agents.ConfirmToolParams{
		ToolUseID: "call_3", Decision: agents.DecisionAllow, Note: "fine", AnswerMS: &ms, Surface: agents.SurfacePrompt, IdempotencyKey: "k1",
	})
	if err != nil || e == nil || e.ID != "evt_8" {
		t.Fatalf("ConfirmTool = %v, %v", e, err)
	}
	if len(*payloads) != 2 {
		t.Fatalf("sent %d requests", len(*payloads))
	}
	second := (*payloads)[1]
	if second["decision"] != "allow" || second["tool_use_id"] != "call_3" || second["note"] != "fine" {
		t.Errorf("the second sent %v", second)
	}
	for _, name := range []string{"answer_ms", "surface"} {
		if _, ok := second[name]; ok {
			t.Errorf("the second sent %s again: %v", name, second)
		}
	}
	if (*keys)[0] != "k1" || (*keys)[1] != "k1.plain" {
		t.Errorf("keys %q", *keys)
	}

	// A generated key is the first's in both, so the second is never the
	// first's key with another body.
	if _, err := s.ConfirmTool(t.Context(), "ses_1", &agents.ConfirmToolParams{ToolUseID: "call_4", Decision: agents.DecisionDeny, Surface: agents.SurfaceQueue}); err != nil {
		t.Fatal(err)
	}
	if k := *keys; len(k) != 4 || k[2] == "" || k[3] != k[2]+".plain" {
		t.Errorf("keys %q", k)
	}
}

// Any other refusal is returned as it came, and nothing is sent again.
func TestConfirmToolDoesNotSendAgainOnAnotherRefusal(t *testing.T) {
	for name, tc := range map[string]struct {
		body   string
		status int
	}{
		"a conflict":          {`{"error":{"code":"conflict","message":"No call waits."}}`, http.StatusConflict},
		"another bad request": {`{"error":{"code":"invalid_query","message":"No."}}`, http.StatusBadRequest},
	} {
		s, payloads, _ := confirmServer(t, tc.body, tc.status)
		_, err := s.ConfirmTool(t.Context(), "ses_1", &agents.ConfirmToolParams{ToolUseID: "call_3", Decision: agents.DecisionAllow, Surface: agents.SurfacePrompt})
		var apiErr *latere.Error
		if !errors.As(err, &apiErr) || apiErr.StatusCode != tc.status {
			t.Errorf("%s: %v", name, err)
		}
		if len(*payloads) != 1 {
			t.Errorf("%s: sent %d requests", name, len(*payloads))
		}
	}
}
