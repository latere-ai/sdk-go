// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"latere.ai/x/sdk-go/latere"
)

// SessionSummaryParams narrows the sessions GetSessionSummary counts. Its
// fields keep the sessions ListSessionsParams would list with the same
// values; a summary counts sessions, so it takes none of the list's Root,
// Parent and GroupByTree.
type SessionSummaryParams struct {
	// Agent counts the sessions of one agent, by its id or its name. A
	// name the caller holds no agent of counts zero everywhere.
	Agent string
	// Runner counts the sessions that run where it says.
	Runner Runner
	// Archived says whether archived sessions are counted; they are left
	// out when it is empty.
	Archived ArchiveFilter
	// Trigger counts the sessions this trigger started, by its id.
	Trigger string
	// Rollout counts the runs of one rollout, by its id, or every session
	// with "any"; empty leaves the runs of every rollout out.
	Rollout string
	// MetadataKey and MetadataValue count the sessions whose metadata
	// holds the key with exactly the value. The API takes one such entry.
	MetadataKey, MetadataValue string
}

// query encodes the parameters for a summary request.
func (p *SessionSummaryParams) query() url.Values {
	q := url.Values{}
	if p == nil {
		return q
	}
	set := func(k, v string) {
		if v != "" {
			q.Set(k, v)
		}
	}
	set("agent", p.Agent)
	set("runner", string(p.Runner))
	set("archived", string(p.Archived))
	set("trigger", p.Trigger)
	set("rollout", p.Rollout)
	if p.MetadataKey != "" {
		q.Set("metadata."+p.MetadataKey, p.MetadataValue)
	}
	return q
}

// A SessionSummary counts the sessions a list would page through, by
// what each is doing, as a client shows above its list.
type SessionSummary struct {
	// Sessions counts the sessions by state; the four counts are
	// disjoint.
	Sessions SessionCounts `json:"sessions"`
	// Agents is how many distinct agents the counted sessions belong to.
	Agents int `json:"agents"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the summary, with every
// field, including those this package does not model; nil for a
// SessionSummary the program built itself.
func (s *SessionSummary) RawJSON() json.RawMessage { return s.raw }

// UnmarshalJSON decodes the summary and keeps the JSON for RawJSON.
func (s *SessionSummary) UnmarshalJSON(b []byte) error {
	type plain SessionSummary
	if err := json.Unmarshal(b, (*plain)(s)); err != nil {
		return err
	}
	s.raw = keep(b)
	return nil
}

// SessionCounts are the counts of a SessionSummary, one per state a
// person acts on differently.
type SessionCounts struct {
	// Running is the sessions of StatusRunning.
	Running int `json:"running"`
	// WaitingForApproval is the idle sessions whose StopReason is
	// StopToolConfirmation: a call or an approval waits for a person.
	WaitingForApproval int `json:"waiting_for_approval"`
	// Idle is every other idle session, those waiting on a question's
	// answer among them.
	Idle int `json:"idle"`
	// Ended is the sessions of StatusEnded.
	Ended int `json:"ended"`
}

// GetSessionSummary counts the sessions ListSessions would list with the
// same narrowing, archived sessions left out unless params.Archived asks
// for them. A nil params counts every session the caller may list. The
// counts are of the sessions the caller may read, as the list's are.
func (c *Client) GetSessionSummary(ctx context.Context, params *SessionSummaryParams) (*SessionSummary, error) {
	var out SessionSummary
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodGet, Path: basePath + "/sessions/summary", Query: params.query()}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
