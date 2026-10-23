// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package routine_test

import (
	"strings"
	"testing"

	"latere.ai/x/sdk-go/agents/routine"
)

// TestAnOffersNameIsItsCalls: an offer's call names one trigger, a
// routine's name for a routine and the same digits under EventPrefix for
// events no routine starts from, and neither name reads as the other.
func TestAnOffersNameIsItsCalls(t *testing.T) {
	key := routine.OfferKey("ses_01M34G2JF01B91B8399CD1G6YP", "toolu_01")
	if key != "ses_01M34G2JF01B91B8399CD1G6YP/toolu_01" {
		t.Fatalf("the key is %q", key)
	}
	name, event := routine.Name(key), routine.EventName(key)
	if event != routine.EventPrefix+strings.TrimPrefix(name, routine.Prefix) || event != routine.EventName(key) || event == routine.EventName(key+"x") {
		t.Fatalf("the names are %s and %s", name, event)
	}
	if !routine.IsEventName(event) || routine.IsEventName(name) || routine.IsName(event) {
		t.Errorf("%s and %s read as each other's shape", name, event)
	}
	for _, n := range []string{"event-", "event-" + strings.Repeat("A", 20), event + "0", "weekly"} {
		if routine.IsEventName(n) {
			t.Errorf("%q reads as an offer's name on events", n)
		}
	}
}

// TestARoutineStartsOnAWholeProductsArrivals: a routine starts on every
// call of the person's link or every email, the one verb received and
// nothing narrowed; any other events start no routine.
func TestARoutineStartsOnAWholeProductsArrivals(t *testing.T) {
	for _, c := range []struct {
		product  string
		verbs    []string
		narrowed bool
		want     routine.Start
	}{
		{"webhook", []string{"received"}, false, routine.FromLink},
		{"mail", []string{"received"}, false, routine.FromEmail},
		{"mail", []string{"received"}, true, ""},
		{"mail", []string{"received", "sent"}, false, ""},
		{"github", []string{"received"}, false, ""},
		{"github", []string{"push"}, false, ""},
		{"webhook", nil, false, ""},
	} {
		got, ok := routine.StartsOn(c.product, c.verbs, c.narrowed)
		if got != c.want || ok != (c.want != "") {
			t.Errorf("%s %v narrowed %v starts %q, %v; want %q", c.product, c.verbs, c.narrowed, got, ok, c.want)
		}
	}
}

// TestAnOfferOnEventsReadsBack: the message an offer on events is written
// with holds its ask literally and the event after it, and reads back as
// the ask the agent wrote, with its title and its pause; a trigger of
// another agent, of another name or on no events reads as none.
func TestAnOfferOnEventsReadsBack(t *testing.T) {
	message := routine.EventMessage("Run the {{retry}} test.")
	if !strings.HasPrefix(message, `Run the \{{retry}} test.`) || !strings.HasSuffix(message, routine.EventSuffix) {
		t.Fatalf("the message is %q", message)
	}
	name := routine.EventName(routine.OfferKey("ses_1", "toolu_1"))
	tr := routine.Trigger{Name: name, DisplayName: "Retry test on main", Labels: map[string]string{routine.Label: "latere-chat"},
		Product: "github", Message: message, Title: `Retry \{{x}}`, Suspend: true}
	got, ok := routine.ReadEvent(tr, "latere-chat")
	if want := (routine.EventFields{Title: "Retry test on main", Ask: "Run the {{retry}} test.", Paused: true}); !ok || got != want {
		t.Fatalf("read %+v, %v; want %+v", got, ok, want)
	}
	tr.DisplayName = ""
	if got, _ := routine.ReadEvent(tr, "latere-chat"); got.Title != "Retry {{x}}" {
		t.Errorf("a trigger without a display name reads the title %q", got.Title)
	}
	if _, ok := routine.Read(tr, "latere-chat"); ok {
		t.Error("an offer on events read as a routine")
	}
	for why, change := range map[string]func(*routine.Trigger){
		"another agent's":  func(t *routine.Trigger) { t.Labels = map[string]string{routine.Label: "release-notes"} },
		"a routine's name": func(t *routine.Trigger) { t.Name = routine.Name("k") },
		"no events":        func(t *routine.Trigger) { t.Product = "" },
	} {
		c := tr
		change(&c)
		if _, ok := routine.ReadEvent(c, "latere-chat"); ok {
			t.Errorf("a trigger with %s read as an offer on events", why)
		}
	}
	if _, ok := routine.ReadEvent(tr, ""); ok {
		t.Error("a trigger read as an offer of no agent")
	}
}
