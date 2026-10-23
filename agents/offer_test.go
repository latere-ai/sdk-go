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
	want := &agents.Offer{ToolUseID: "toolu_1", Action: agents.OfferPropose, Name: "Weekly sign-ups", Ask: "Count the sign-ups.", Why: "Every Monday at 9:00, Berlin time, I count them.",
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
		"a pause":        {ToolUseID: "toolu_5", Name: agents.ToolTrigger, Verdict: agents.VerdictAllow, Input: json.RawMessage(`{"action":"pause","id":"trg_1","why":"It reached 79 EUR."}`)},
		"another tool":   {ToolUseID: "toolu_3", Name: "question", Input: json.RawMessage(`{}`)},
		"no JSON object": {ToolUseID: "toolu_4", Name: agents.ToolTrigger, Input: json.RawMessage(`[]`)},
	} {
		if o, err := u.Offer(); err == nil {
			t.Errorf("%s read as the offer %+v", name, o)
		}
	}
}

// TestEveryHeldCallReadsAsItsOffer: a proposal of an event trigger reads
// its filter, and its notify only when it names one; a change reads the
// trigger it changes and what it sets; a resume and a delete read their
// trigger alone.
func TestEveryHeldCallReadsAsItsOffer(t *testing.T) {
	for name, c := range map[string]struct {
		input string
		want  agents.Offer
	}{
		"an event": {
			`{"action":"propose","name":"Retry test on main","ask":"Run the retry test.","why":"After each push to main I run it and tell you only if it fails.",
"on":{"product":"github","verbs":["push"],"resources":["acme/field-notes"],"match":[{"path":"payload.ref","in":["refs/heads/main"]}]}}`,
			agents.Offer{Action: agents.OfferPropose, Name: "Retry test on main", Ask: "Run the retry test.", Why: "After each push to main I run it and tell you only if it fails.",
				On: &agents.EventFilter{Product: "github", Verbs: []string{"push"}, Resources: []string{"acme/field-notes"}, Match: []agents.PayloadMatch{{Path: "payload.ref", In: []string{"refs/heads/main"}}}}},
		},
		"an event that always tells": {
			`{"action":"propose","name":"Releases","ask":"Summarize it.","why":"Each release.","on":{"product":"github","verbs":["release.published"]},"notify":"always"}`,
			agents.Offer{Action: agents.OfferPropose, Name: "Releases", Ask: "Summarize it.", Why: "Each release.", On: &agents.EventFilter{Product: "github", Verbs: []string{"release.published"}}, Notify: agents.NotifyAlways},
		},
		"a change": {
			`{"action":"change","id":"trg_1","why":"Every Monday at 8:00 instead.","schedule":"0 8 * * 1","timeZone":"Europe/Berlin"}`,
			agents.Offer{Action: agents.OfferChange, TriggerID: "trg_1", Why: "Every Monday at 8:00 instead.", Schedule: &agents.RoutineSchedule{Days: []time.Weekday{time.Monday}, Hour: 8, Zone: "Europe/Berlin"}},
		},
		"a change of name and notify": {
			`{"action":"change","id":"trg_1","name":"Sign-ups","why":"A shorter name, and only news.","notify":"judged"}`,
			agents.Offer{Action: agents.OfferChange, TriggerID: "trg_1", Name: "Sign-ups", Why: "A shorter name, and only news.", Notify: agents.NotifyJudged},
		},
		"a resume": {
			`{"action":"resume","id":"trg_1","why":"ignored"}`,
			agents.Offer{Action: agents.OfferResume, TriggerID: "trg_1"},
		},
		"a delete": {
			`{"action":"delete","id":"trg_1"}`,
			agents.Offer{Action: agents.OfferDelete, TriggerID: "trg_1"},
		},
	} {
		use := &agents.ToolUse{ToolUseID: "toolu_1", Name: agents.ToolTrigger, Verdict: agents.VerdictAsk, Input: json.RawMessage(c.input)}
		got, err := use.Offer()
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		c.want.ToolUseID = "toolu_1"
		if !reflect.DeepEqual(*got, c.want) {
			t.Errorf("%s read as %+v, want %+v", name, *got, c.want)
		}
	}
}

// TestAHeldWakeIsAnOffer: a wake set that waits for the person reads as an
// offer, at its time or after its while, with its title and note; one that
// does not wait, a cancel, and an input whose time does not parse read as
// none.
func TestAHeldWakeIsAnOffer(t *testing.T) {
	use := &agents.ToolUse{ToolUseID: "toolu_9", Name: agents.ToolWake, Verdict: agents.VerdictAsk, Input: json.RawMessage(
		`{"action":"set","at":"2026-10-24T09:00:00+02:00","title":"Whether the refund arrived","note":"Look for the refund in the inbox."}`)}
	o, err := use.Offer()
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 24, 7, 0, 0, 0, time.UTC)
	if o.Action != agents.OfferWake || o.ToolUseID != "toolu_9" || o.At == nil || !o.At.Equal(at) || o.After != 0 ||
		o.Name != "Whether the refund arrived" || o.Note != "Look for the refund in the inbox." || o.TriggerID != "" {
		t.Errorf("read the wake as %+v", o)
	}
	use.Input = json.RawMessage(`{"action":"set","after":"36h","title":"Whether the refund arrived","note":"Look again."}`)
	if o, err := use.Offer(); err != nil || o.At != nil || o.After != 36*time.Hour {
		t.Errorf("a wake after a while read as %+v, %v", o, err)
	}
	for name, u := range map[string]*agents.ToolUse{
		"a wake that runs at once": {ToolUseID: "toolu_2", Name: agents.ToolWake, Verdict: agents.VerdictAllow, Input: json.RawMessage(`{"action":"set","after":"2h","title":"t","note":"n"}`)},
		"a cancel":                 {ToolUseID: "toolu_3", Name: agents.ToolWake, Verdict: agents.VerdictAsk, Input: json.RawMessage(`{"action":"cancel"}`)},
		"a time that is none":      {ToolUseID: "toolu_4", Name: agents.ToolWake, Verdict: agents.VerdictAsk, Input: json.RawMessage(`{"action":"set","at":"tomorrow","title":"t","note":"n"}`)},
		"a while that is none":     {ToolUseID: "toolu_5", Name: agents.ToolWake, Verdict: agents.VerdictAsk, Input: json.RawMessage(`{"action":"set","after":"a day","title":"t","note":"n"}`)},
		"no JSON object":           {ToolUseID: "toolu_6", Name: agents.ToolWake, Verdict: agents.VerdictAsk, Input: json.RawMessage(`"set"`)},
		"the notify tool":          {ToolUseID: "toolu_7", Name: agents.ToolNotify, Verdict: agents.VerdictAllow, Input: json.RawMessage(`{"title":"The refund arrived."}`)},
	} {
		if o, err := u.Offer(); err == nil {
			t.Errorf("%s read as the offer %+v", name, o)
		}
	}
}
