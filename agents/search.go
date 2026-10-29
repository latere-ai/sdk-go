// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"latere.ai/x/sdk-go/latere"
)

// The bounds of a search of sessions.
const (
	// MaxSearchQuery is the most characters a search's words hold; the API
	// refuses a longer query as invalid.
	MaxSearchQuery = 200
	// MaxSearchLimit is the most sessions a page of a search holds.
	MaxSearchLimit = 20
)

// SearchSessionsParams is what a search of sessions looks for and the
// sessions it searches. The narrowing fields keep the sessions
// ListSessionsParams would list with the same values.
type SearchSessionsParams struct {
	// Query is the words to look for, at most MaxSearchQuery characters.
	// Required. A message matches when it holds every word, each found as
	// the start of a word whatever its case.
	Query string
	// Agent keeps the sessions of one agent, by its id or its name.
	Agent string
	// Status keeps the sessions in one state.
	Status Status
	// Runner keeps the sessions that run where it says.
	Runner Runner
	// Archived says whether archived sessions are searched.
	Archived ArchiveFilter
	// Rollout keeps the runs of one rollout, by its id, or every session
	// with "any"; empty leaves the runs of every rollout out.
	Rollout string
	// Limit is how many sessions a page holds at most, up to
	// MaxSearchLimit; the API's default, MaxSearchLimit, when zero.
	Limit int
	// Cursor starts SearchSessionsPage at the page a previous one's
	// NextCursor named; SearchSessions ignores it.
	Cursor string
}

// query encodes the parameters for a search request.
func (p *SearchSessionsParams) query(cursor string) url.Values {
	q := url.Values{}
	set := func(k, v string) {
		if v != "" {
			q.Set(k, v)
		}
	}
	set("q", p.Query)
	set("agent", p.Agent)
	set("status", string(p.Status))
	set("runner", string(p.Runner))
	set("archived", string(p.Archived))
	set("rollout", p.Rollout)
	if p.Limit > 0 {
		q.Set("limit", strconv.Itoa(p.Limit))
	}
	set("cursor", cursor)
	return q
}

// A SessionMatch is a session a search found, with where in its log what
// was said matched.
type SessionMatch struct {
	// Session is the session, as a read of it answers it.
	Session *Session `json:"session"`
	// Matches are the session's newest matching events, at most three,
	// newest first.
	Matches []EventMatch `json:"matches"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the match, with every
// field, including those this package does not model; nil for a
// SessionMatch the program built itself.
func (m *SessionMatch) RawJSON() json.RawMessage { return m.raw }

// UnmarshalJSON decodes the match and keeps the JSON for RawJSON.
func (m *SessionMatch) UnmarshalJSON(b []byte) error {
	type plain SessionMatch
	if err := json.Unmarshal(b, (*plain)(m)); err != nil {
		return err
	}
	m.raw = keep(b)
	return nil
}

// An EventMatch is an event of a session's log whose text matched a
// search: a message a person sent, an answer the agent wrote in the
// session's own conversation, or a person's answer to a question.
type EventMatch struct {
	// Seq is the event's place in the session's log, by which a program
	// shows it in the conversation.
	Seq int64 `json:"seq"`
	// EventID is the event's id.
	EventID string `json:"event_id"`
	// Type is the event's type.
	Type EventType `json:"type"`
	// Time is when the event was appended.
	Time time.Time `json:"time"`
	// Excerpt is up to 160 characters of the text around its first match,
	// in parts, each part the words matched marked Match; an ellipsis
	// stands where the text is cut.
	Excerpt []ExcerptPart `json:"excerpt"`
}

// An ExcerptPart is a run of an excerpt's text, Match when the search's
// words matched it.
type ExcerptPart struct {
	// Text is the run's text.
	Text string `json:"text"`
	// Match says the search's words matched the run.
	Match bool `json:"match,omitempty"`
}

// errNoQuery is returned before any request when a search has no words.
var errNoQuery = errors.New("agents: SearchSessionsParams.Query is required")

// SearchSessions returns an iterator over the sessions whose messages,
// answers and answers to questions hold every word of params.Query,
// newest first as ListSessions orders them, fetching pages as the loop
// needs them. A tool's output, a subagent's conversation, a file and a
// redacted event are never searched. A page may hold fewer sessions than
// its limit while more follow, since a session the caller may not read
// is left out. An empty Query yields an error before any request.
func (c *Client) SearchSessions(ctx context.Context, params *SearchSessionsParams) iter.Seq2[*SessionMatch, error] {
	return latere.Items(ctx, func(ctx context.Context, cursor string) (*latere.Page[*SessionMatch], error) {
		return c.searchPage(ctx, params, cursor)
	})
}

// SearchSessionsPage returns one page of the sessions a search finds,
// starting at params.Cursor, and the cursor of the next page in
// NextCursor.
func (c *Client) SearchSessionsPage(ctx context.Context, params *SearchSessionsParams) (*latere.Page[*SessionMatch], error) {
	cursor := ""
	if params != nil {
		cursor = params.Cursor
	}
	return c.searchPage(ctx, params, cursor)
}

func (c *Client) searchPage(ctx context.Context, params *SearchSessionsParams, cursor string) (*latere.Page[*SessionMatch], error) {
	if params == nil || params.Query == "" {
		return nil, errNoQuery
	}
	var page latere.Page[*SessionMatch]
	err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodGet, Path: basePath + "/sessions/search", Query: params.query(cursor)}, &page)
	if err != nil {
		return nil, err
	}
	return &page, nil
}
