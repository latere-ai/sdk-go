// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents_test

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"latere.ai/x/sdk-go/agents"
)

const match = `{"session":` + session + `,"matches":[{"seq":3,"event_id":"evt_3","type":"agent.message","time":"2026-09-22T12:05:40Z",
"excerpt":[{"text":"The "},{"text":"release","match":true},{"text":" notes are in NOTES.md."}]},
{"seq":1,"event_id":"evt_1","type":"user.message","time":"2026-09-22T12:05:00Z","excerpt":[{"text":"Write the "},{"text":"release","match":true},{"text":" notes."}]}]}`

// A search sends its words and the list's narrowing fields, and reads
// each session it found with its matching events, newest first.
func TestSearchSessions(t *testing.T) {
	c, calls := fakeAPI(t, map[string]string{"GET /v1/agents/sessions/search": `{"items":[` + match + `],"next_cursor":""}`})
	n := 0
	for got, err := range c.SearchSessions(t.Context(), &agents.SearchSessionsParams{
		Query: "release notes", Agent: "latere-chat", Status: agents.StatusIdle, Runner: agents.RunnerHosted,
		Archived: agents.ArchivedIncluded, Rollout: "any", Limit: 5, Cursor: "ignored",
	}) {
		if err != nil {
			t.Fatal(err)
		}
		n++
		if got.Session == nil || got.Session.ID != "ses_1" || len(got.Matches) != 2 {
			t.Fatalf("found %+v", got)
		}
		m := got.Matches[0]
		if m.Seq != 3 || m.EventID != "evt_3" || m.Type != agents.EventAgentMessage || m.Time.IsZero() ||
			len(m.Excerpt) != 3 || !m.Excerpt[1].Match || m.Excerpt[1].Text != "release" || m.Excerpt[0].Match {
			t.Errorf("the first match reads %+v", m)
		}
		if string(got.RawJSON()) != match {
			t.Errorf("RawJSON = %s; want the answer", got.RawJSON())
		}
	}
	if n != 1 {
		t.Errorf("found %d sessions", n)
	}
	q, err := url.ParseQuery((*calls)[0].Query)
	if err != nil {
		t.Fatal(err)
	}
	want := url.Values{"q": {"release notes"}, "agent": {"latere-chat"}, "status": {"idle"}, "runner": {"hosted"},
		"archived": {"any"}, "rollout": {"any"}, "limit": {"5"}}
	if q.Encode() != want.Encode() {
		t.Errorf("sent the query %q, want %q", q.Encode(), want.Encode())
	}

	page, err := c.SearchSessionsPage(t.Context(), &agents.SearchSessionsParams{Query: "notes", Cursor: "c2"})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("one page: %+v, %v", page, err)
	}
	if got := (*calls)[1].Query; got != "cursor=c2&q=notes" {
		t.Errorf("a page sent the query %q", got)
	}
	for _, bad := range []*agents.SearchSessionsParams{nil, {}, {Agent: "latere-chat"}} {
		if _, err := c.SearchSessionsPage(t.Context(), bad); err == nil {
			t.Errorf("searched for no words with %+v", bad)
		}
		for _, err := range c.SearchSessions(t.Context(), bad) {
			if err == nil {
				t.Errorf("the iterator searched for no words with %+v", bad)
			}
		}
	}
	if len(*calls) != 2 {
		t.Errorf("a search for no words reached the API: %+v", (*calls)[2:])
	}
}

// A search that finds nothing is an empty page, and one the API refuses
// is its error.
func TestASearchThatFindsNothingOrIsRefused(t *testing.T) {
	c, _ := fakeAPI(t, map[string]string{"GET /v1/agents/sessions/search": `{"items":[]}`})
	for _, err := range c.SearchSessions(t.Context(), &agents.SearchSessionsParams{Query: "nothing"}) {
		t.Errorf("found something, %v", err)
	}
	refused, _ := fakeAPI(t, map[string]string{})
	if _, err := refused.SearchSessionsPage(t.Context(), &agents.SearchSessionsParams{Query: strings.Repeat("a", agents.MaxSearchQuery+1)}); err == nil {
		t.Error("a refused search answered no error")
	}
	var m agents.SessionMatch
	if err := json.Unmarshal([]byte(`{"session":"no object"}`), &m); err == nil || m.RawJSON() != nil {
		t.Errorf("a match of the wrong shape decoded: %v, %s", err, m.RawJSON())
	}
	if (&agents.SessionMatch{}).RawJSON() != nil {
		t.Error("a match the program built has JSON")
	}
}
