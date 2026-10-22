// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"latere.ai/x/sdk-go/agents"
)

// TestAnOfferIsReadFromItsCall: a trigger call that proposes a routine
// reads as the offer, its schedule read into days and a time in its zone;
// a schedule of another shape reads as none; a list, another tool and an
// input that does not decode read as no offer.
func TestAnOfferIsReadFromItsCall(t *testing.T) {
	use := &agents.ToolUse{ToolUseID: "toolu_1", Name: agents.ToolTrigger, Verdict: agents.VerdictAsk, Input: json.RawMessage(
		`{"action":"propose","name":"Weekly sign-ups","ask":"Count the sign-ups.","why":"Every Monday at 9:00, Berlin time, I count them.","schedule":"0 9 * * 1","timeZone":"Europe/Berlin"}`)}
	o, err := use.Offer()
	if err != nil {
		t.Fatal(err)
	}
	want := &agents.Offer{ToolUseID: "toolu_1", Name: "Weekly sign-ups", Ask: "Count the sign-ups.", Why: "Every Monday at 9:00, Berlin time, I count them.",
		Schedule: &agents.RoutineSchedule{Days: []time.Weekday{time.Monday}, Hour: 9, Zone: "Europe/Berlin"}}
	if !reflect.DeepEqual(o, want) {
		t.Fatalf("the offer is %+v, want %+v", o, want)
	}
	use.Input = json.RawMessage(`{"action":"propose","name":"Often","ask":"Look.","why":"Often.","schedule":"*/5 * * * *","timeZone":"UTC"}`)
	if o, err := use.Offer(); err != nil || o.Schedule != nil {
		t.Fatalf("a schedule no routine runs on read as %+v, %v", o, err)
	}
	for name, u := range map[string]*agents.ToolUse{
		"a list":         {ToolUseID: "toolu_2", Name: agents.ToolTrigger, Input: json.RawMessage(`{"action":"list"}`)},
		"another tool":   {ToolUseID: "toolu_3", Name: "question", Input: json.RawMessage(`{}`)},
		"no JSON object": {ToolUseID: "toolu_4", Name: agents.ToolTrigger, Input: json.RawMessage(`[]`)},
	} {
		if o, err := u.Offer(); err == nil {
			t.Errorf("%s read as the offer %+v", name, o)
		}
	}
}
