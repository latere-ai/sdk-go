// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents_test

import (
	"encoding/json"
	"testing"

	"latere.ai/x/sdk-go/agents"
)

// spentEvent is an event of typ with payload at seq, as the log answers it.
func spentEvent(seq int64, typ agents.EventType, payload string) agents.Event {
	return agents.Event{ID: "evt_1", Seq: seq, Type: typ, Payload: json.RawMessage(payload)}
}

// A priced tool call's result carries its cost beside its meta, and a
// call that cost nothing carries none.
func TestAToolResultCarriesItsCost(t *testing.T) {
	r := toolResult(t, `{"tool_use_id":"call_1","content":[{"type":"text","text":"3 results"}],"outcome":"ok","meta":{"refusal":""},"cost_usd_micro":10000}`)
	if r.CostMicroUSD == nil || *r.CostMicroUSD != 10000 {
		t.Fatalf("the cost of a priced search is %v", r.CostMicroUSD)
	}
	if r := toolResult(t, `{"tool_use_id":"call_2","content":[]}`); r.CostMicroUSD != nil {
		t.Fatalf("a free call costs %d", *r.CostMicroUSD)
	}
}

// What each event adds to the session's spending: a model request's
// cost, the session's own or a subagent's, a priced tool result's, a
// redacted one's from its tombstone, and nothing for any other event,
// for an unpriced one, or for a payload that does not decode.
func TestAnEventsCostIsWhatItAddsToTheSpending(t *testing.T) {
	sub := spentEvent(5, agents.EventModelRequest, `{"model":"m","cost_usd_micro":700}`)
	sub.Subagent = "sub_1"
	for name, tc := range map[string]struct {
		e    agents.Event
		want int64
	}{
		"a model request":            {spentEvent(2, agents.EventModelRequest, `{"model":"m","usage":{"input_tokens":10,"output_tokens":5},"cost_usd_micro":4200}`), 4200},
		"a subagent's model request": {sub, 700},
		"an unpriced model request":  {spentEvent(3, agents.EventModelRequest, `{"model":"m"}`), 0},
		"a priced tool result":       {spentEvent(4, agents.EventToolResult, `{"tool_use_id":"call_1","content":[],"cost_usd_micro":10000}`), 10000},
		"a redacted tool result":     {spentEvent(6, agents.EventToolResult, `{"tombstone":true,"tool_use_id":"call_1","cost_usd_micro":10000}`), 10000},
		"a free tool result":         {spentEvent(7, agents.EventToolResult, `{"tool_use_id":"call_2","content":[]}`), 0},
		"a result your program sent": {spentEvent(8, agents.EventUserToolResult, `{"tool_use_id":"call_3","content":[],"cost_usd_micro":5}`), 0},
		"a subagent that ended":      {spentEvent(9, "subagent.ended", `{"reason":"done","cost_usd_micro":700}`), 0},
		"a message":                  {spentEvent(1, agents.EventAgentMessage, `{"message":{"role":"assistant","blocks":[]}}`), 0},
		"a payload that is no JSON":  {spentEvent(10, agents.EventModelRequest, `{`), 0},
		"a cost of the wrong type":   {spentEvent(11, agents.EventToolResult, `{"tool_use_id":"call_4","cost_usd_micro":"ten"}`), 0},
	} {
		if got := tc.e.CostMicroUSD(); got != tc.want {
			t.Errorf("%s adds %d, want %d", name, got, tc.want)
		}
	}
}

// A session read with its LastSeq, and each later event's cost added,
// is the spending the next read reports.
func TestTheSpendingKeptCurrentFromTheStream(t *testing.T) {
	var s agents.Session
	if err := json.Unmarshal([]byte(`{"id":"ses_1","last_seq":4,"budget":{"spent_cost_usd_micro":4200}}`), &s); err != nil {
		t.Fatal(err)
	}
	spent := s.Budget.SpentMicroUSD
	for _, e := range []agents.Event{
		spentEvent(3, agents.EventModelRequest, `{"model":"m","cost_usd_micro":4200}`),
		spentEvent(5, agents.EventToolResult, `{"tool_use_id":"call_1","content":[],"cost_usd_micro":10000}`),
		spentEvent(6, agents.EventModelRequest, `{"model":"m","cost_usd_micro":800}`),
	} {
		if e.Seq > s.LastSeq {
			spent += e.CostMicroUSD()
		}
	}
	if spent != 15000 {
		t.Fatalf("the spending kept current is %d, want 15000", spent)
	}
}

// A fork's budget holds apart what it copied from the session it was
// forked from, and its own spending is the rest; a session no fork made
// carries nothing.
func TestAForksOwnSpending(t *testing.T) {
	var fork agents.Session
	if err := json.Unmarshal([]byte(`{"id":"ses_2","budget":{"max_cost_usd_micro":5000000,"spent_cost_usd_micro":1500,"carried_cost_usd_micro":1200}}`), &fork); err != nil {
		t.Fatal(err)
	}
	if b := fork.Budget; b.SpentMicroUSD != 1500 || b.CarriedMicroUSD != 1200 || b.OwnMicroUSD() != 300 {
		t.Fatalf("the fork's budget %+v, its own %d", *b, b.OwnMicroUSD())
	}
	b := agents.Budget{SpentMicroUSD: 900}
	if b.OwnMicroUSD() != 900 {
		t.Fatalf("a session no fork made spent %d of its own", b.OwnMicroUSD())
	}
	var none *agents.Budget
	if none.OwnMicroUSD() != 0 {
		t.Fatalf("a session that reports no budget spent %d", none.OwnMicroUSD())
	}
}

// A todo call's result records the list it left, typed, and tells an
// emptied list from a result that holds none.
func TestATodoResultRecordsTheList(t *testing.T) {
	r := toolResult(t, `{"tool_use_id":"call_1","content":[{"type":"text","text":"1 of 3 done"}],"outcome":"ok","meta":{"todos":[`+
		`{"id":"a","content":"Read the listings","status":"completed"},`+
		`{"id":"b","content":"Compare the prices","status":"in_progress"},`+
		`{"id":"c","content":"Write the summary","status":"pending"}]}}`)
	want := []agents.Todo{
		{ID: "a", Content: "Read the listings", Status: agents.TodoCompleted},
		{ID: "b", Content: "Compare the prices", Status: agents.TodoInProgress},
		{ID: "c", Content: "Write the summary", Status: agents.TodoPending},
	}
	if r.Meta == nil || len(r.Meta.Todos) != len(want) {
		t.Fatalf("the list %+v", r.Meta)
	}
	for i, td := range r.Meta.Todos {
		if td != want[i] {
			t.Errorf("item %d is %+v, want %+v", i, td, want[i])
		}
	}

	emptied := toolResult(t, `{"tool_use_id":"call_2","content":[],"outcome":"ok","meta":{"todos":[]}}`)
	if emptied.Meta == nil || emptied.Meta.Todos == nil || len(emptied.Meta.Todos) != 0 {
		t.Fatalf("an emptied list reads as %+v", emptied.Meta)
	}
	other := toolResult(t, `{"tool_use_id":"call_3","content":[],"outcome":"ok","meta":{"path":"/workspace/a.md","sha256":"00"}}`)
	if other.Meta == nil || other.Meta.Todos != nil {
		t.Fatalf("a read's meta holds the list %+v", other.Meta.Todos)
	}

	// Built by a program and written back, an emptied list stays one.
	b, err := json.Marshal(agents.ToolMeta{Todos: []agents.Todo{}})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"todos":[]}` {
		t.Fatalf("an emptied list is written as %s", b)
	}
}
