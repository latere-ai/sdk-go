// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"latere.ai/x/sdk-go/agents"
)

// report is the context of a request of a 200,000-token window that used
// 50,000, as the API writes it.
const report = `{"window":200000,"used":50000,"source":"provider","parts":[{"part":"instructions","tokens":6000},{"part":"tools","tokens":9000},
{"part":"messages","tokens":30000},{"part":"files","tokens":4000},{"part":"summary","tokens":1000},{"part":"free","tokens":150000}]}`

// TestASessionReadsItsContextReport: a session reads the context of its
// latest request, the share of the window it used and its parts in order.
func TestASessionReadsItsContextReport(t *testing.T) {
	var s agents.Session
	if err := json.Unmarshal([]byte(`{"id":"ses_1","status":"idle","created_at":"2026-10-23T09:00:00Z","updated_at":"2026-10-23T09:00:00Z","context_report":`+report+`}`), &s); err != nil {
		t.Fatal(err)
	}
	r := s.ContextReport
	if r == nil || r.Window != 200_000 || r.Used != 50_000 || r.Source != agents.ContextFromProvider || len(r.Parts) != 6 {
		t.Fatalf("the session reads %+v", r)
	}
	if f, ok := r.UsedFraction(); !ok || f != 0.25 {
		t.Errorf("the used share is %v, %v", f, ok)
	}
	want := []agents.ContextPart{
		{Part: agents.ContextInstructions, Tokens: 6000}, {Part: agents.ContextTools, Tokens: 9000}, {Part: agents.ContextMessages, Tokens: 30000},
		{Part: agents.ContextFiles, Tokens: 4000}, {Part: agents.ContextSummary, Tokens: 1000}, {Part: agents.ContextFree, Tokens: 150000},
	}
	if got := r.PartsInOrder(); !reflect.DeepEqual(got, want) {
		t.Errorf("the parts in order are %+v", got)
	}
}

// TestTheUsedShareAndThePartsInOrder: the share is 0 to 1 and unknown
// without a window; the parts are put in their order, a part of a kind
// not known after the others, without changing the report.
func TestTheUsedShareAndThePartsInOrder(t *testing.T) {
	for name, c := range map[string]struct {
		r    *agents.ContextReport
		want float64
		ok   bool
	}{
		"nil":             {nil, 0, false},
		"no window":       {&agents.ContextReport{Used: 10}, 0, false},
		"past the window": {&agents.ContextReport{Window: 100, Used: 120, Source: agents.ContextEstimated}, 1, true},
		"empty":           {&agents.ContextReport{Window: 100}, 0, true},
		"half":            {&agents.ContextReport{Window: 100, Used: 50}, 0.5, true},
	} {
		if got, ok := c.r.UsedFraction(); got != c.want || ok != c.ok {
			t.Errorf("%s: the used share is %v, %v", name, got, ok)
		}
	}
	r := &agents.ContextReport{Window: 100, Used: 60, Parts: []agents.ContextPart{
		{Part: agents.ContextFree, Tokens: 40}, {Part: "skills", Tokens: 5}, {Part: agents.ContextMessages, Tokens: 40},
		{Part: agents.ContextInstructions, Tokens: 15}, {Part: "memory", Tokens: 0},
	}}
	before := append([]agents.ContextPart(nil), r.Parts...)
	want := []agents.ContextPart{
		{Part: agents.ContextInstructions, Tokens: 15}, {Part: agents.ContextMessages, Tokens: 40}, {Part: agents.ContextFree, Tokens: 40},
		{Part: "skills", Tokens: 5}, {Part: "memory", Tokens: 0},
	}
	if got := r.PartsInOrder(); !reflect.DeepEqual(got, want) {
		t.Errorf("the parts in order are %+v", got)
	}
	if !reflect.DeepEqual(r.Parts, before) {
		t.Errorf("the report's own parts changed to %+v", r.Parts)
	}
	var none *agents.ContextReport
	if none.PartsInOrder() != nil {
		t.Error("a nil report has parts")
	}
}

// TestTheEventsOfAnUnattendedTurn: a woken turn's message reads the week
// its trigger was at, a call and an approval read whether a person began
// their turn, an event written before reads neither, and a model request
// reads its model, usage, cost and context.
func TestTheEventsOfAnUnattendedTurn(t *testing.T) {
	decode := func(typ agents.EventType, payload string, v any) {
		t.Helper()
		e := agents.Event{ID: "evt_1", Type: typ, Payload: json.RawMessage(payload)}
		if err := e.Decode(v); err != nil {
			t.Fatal(err)
		}
	}
	var m agents.UserMessage
	decode(agents.EventUserMessage, `{"sender":{"subject":"trg_1","kind":"trigger"},"content":[{"type":"text","text":"Look."}],"firing_id":"frg_1",
"spend":{"trigger_id":"trg_1","window_days":7,"spent_usd_micro":1500000,"per_week_usd_micro":2000000,"per_look_usd_micro":300000,
"expected_looks":3,"projected_usd_micro":2400000}}`, &m)
	perLook, projected := int64(300_000), int64(2_400_000)
	want := &agents.FiringSpend{TriggerID: "trg_1", WindowDays: 7, SpentMicroUSD: 1_500_000, PerWeekMicroUSD: 2_000_000, PerLookMicroUSD: &perLook,
		ExpectedLooks: 3, ProjectedMicroUSD: &projected}
	if !reflect.DeepEqual(m.Spend, want) || !m.Spend.AheadOfPace() {
		t.Errorf("the message reads the spend %+v", m.Spend)
	}
	var person agents.UserMessage
	decode(agents.EventUserMessage, `{"sender":{"subject":"alice","kind":"person"},"content":[]}`, &person)
	if person.Spend != nil {
		t.Errorf("a person's message reads the spend %+v", person.Spend)
	}

	var use agents.ToolUse
	decode(agents.EventAgentToolUse, `{"tool_use_id":"toolu_1","name":"bash","input":{},"verdict":"ask","attended":false}`, &use)
	if use.Attended == nil || *use.Attended {
		t.Errorf("an unattended call reads attended %v", use.Attended)
	}
	var old agents.ToolUse
	decode(agents.EventAgentToolUse, `{"tool_use_id":"toolu_1","name":"bash","input":{},"verdict":"allow"}`, &old)
	if old.Attended != nil {
		t.Errorf("a call recorded before reads attended %v", *old.Attended)
	}
	var approval agents.ApprovalRequested
	decode(agents.EventApprovalRequested, `{"approval_id":"apr_1","tool_use_id":"toolu_1","source":"egress","destination":{"host":"example.com"},
"reason":"","verdict":"ask","attended":true}`, &approval)
	if approval.Attended == nil || !*approval.Attended {
		t.Errorf("an attended approval reads attended %v", approval.Attended)
	}

	var req agents.ModelRequest
	decode(agents.EventModelRequest, `{"model":"anthropic/claude-haiku-5-5","outcome":"ok","usage":{"input_tokens":1000,"output_tokens":200,
"cache_read_input_tokens":49000},"cost_usd_micro":4200,"context":`+report+`}`, &req)
	if req.Model != "anthropic/claude-haiku-5-5" || req.Usage == nil || req.Usage.CacheReadInputTokens != 49_000 || req.CostMicroUSD == nil || *req.CostMicroUSD != 4200 ||
		req.Context == nil || req.Context.Used != 50_000 {
		t.Errorf("the request reads %+v", req)
	}
	var title agents.ModelRequest
	decode(agents.EventModelRequest, `{"model":"m","outcome":"ok","purpose":"title"}`, &title)
	if title.Context != nil {
		t.Errorf("a title's request reads the context %+v", title.Context)
	}
}
