// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents_test

import (
	"encoding/json"
	"strings"
	"testing"

	"latere.ai/x/sdk-go/agents"
)

// A session answered by the API keeps its JSON, a field the package does
// not model included, for a program that shows or stores the answer.
func TestASessionKeepsTheJSONTheAPIAnswered(t *testing.T) {
	answer := strings.Replace(session, `"title":"Notes",`, `"title":"Notes","added_later":"kept",`, 1)
	c, _ := fakeAPI(t, map[string]string{"GET /v1/agents/sessions/ses_1": answer})
	got, err := c.GetSession(t.Context(), "ses_1")
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(got.RawJSON(), &fields); err != nil || fields["added_later"] != "kept" {
		t.Errorf("RawJSON = %s; want the answer with the field the package does not model", got.RawJSON())
	}
	if got.Title != "Notes" {
		t.Errorf("Title = %q; keeping the JSON must not change the decoding", got.Title)
	}
	if (&agents.Session{}).RawJSON() != nil {
		t.Error("a session the program built has JSON")
	}
}

// Each resource the package answers keeps the JSON it was decoded from.
func TestEveryResourceKeepsItsJSON(t *testing.T) {
	for name, tc := range map[string]struct {
		raw string
		v   interface {
			RawJSON() json.RawMessage
		}
	}{
		"event":   {event, &agents.Event{}},
		"trigger": {trigger, &agents.Trigger{}},
		"firing":  {firing, &agents.Firing{}},
		"match":   {match, &agents.SessionMatch{}},
	} {
		if err := json.Unmarshal([]byte(tc.raw), tc.v); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(tc.v.RawJSON()) != tc.raw {
			t.Errorf("%s: RawJSON differs from the answer", name)
		}
	}
}

// JSON of the wrong shape is refused, and nothing is kept.
func TestAResourceRefusesJSONOfTheWrongShape(t *testing.T) {
	for _, v := range []interface {
		RawJSON() json.RawMessage
	}{
		&agents.Session{},
		&agents.Event{},
		&agents.Trigger{},
		&agents.Firing{},
		&agents.SessionMatch{},
	} {
		if err := json.Unmarshal([]byte(`[]`), v); err == nil {
			t.Errorf("%T decoded an array", v)
		}
		if v.RawJSON() != nil {
			t.Errorf("%T kept JSON it refused", v)
		}
	}
}
