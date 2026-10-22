// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents_test

import (
	"net/url"
	"testing"

	"latere.ai/x/sdk-go/agents"
)

const summary = `{"sessions":{"running":1,"waiting_for_approval":2,"idle":3,"ended":4},"agents":1}`

// A summary sends the list's narrowing fields and reads the four counts.
func TestGetSessionSummary(t *testing.T) {
	c, calls := fakeAPI(t, map[string]string{"GET /v1/agents/sessions/summary": summary})
	got, err := c.GetSessionSummary(t.Context(), &agents.SessionSummaryParams{
		Agent: "latere-chat", Runner: agents.RunnerHosted, Archived: agents.ArchivedIncluded,
		Trigger: "trg_1", Rollout: "any", MetadataKey: "project", MetadataValue: "p1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Sessions != (agents.SessionCounts{Running: 1, WaitingForApproval: 2, Idle: 3, Ended: 4}) || got.Agents != 1 {
		t.Errorf("the summary reads %+v", got)
	}
	if string(got.RawJSON()) != summary {
		t.Errorf("RawJSON = %s; want the answer", got.RawJSON())
	}
	q, err := url.ParseQuery((*calls)[0].Query)
	if err != nil {
		t.Fatal(err)
	}
	want := url.Values{"agent": {"latere-chat"}, "runner": {"hosted"}, "archived": {"any"}, "trigger": {"trg_1"},
		"rollout": {"any"}, "metadata.project": {"p1"}}
	if q.Encode() != want.Encode() {
		t.Errorf("sent the query %q, want %q", q.Encode(), want.Encode())
	}

	if _, err := c.GetSessionSummary(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if (*calls)[1].Query != "" {
		t.Errorf("a summary of no narrowing sent the query %q", (*calls)[1].Query)
	}
}

// A refusal is the API's error.
func TestGetSessionSummaryRefused(t *testing.T) {
	c, _ := fakeAPI(t, map[string]string{})
	if _, err := c.GetSessionSummary(t.Context(), nil); err == nil {
		t.Fatal("a refused summary returned no error")
	}
}
