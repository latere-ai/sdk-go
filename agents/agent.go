// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"iter"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"latere.ai/x/sdk-go/latere"
)

// Agent is an agent as the API holds it: its names and the API's record
// of its latest version. The spec it runs is not modeled; RawJSON holds
// it whole.
type Agent struct {
	// APIVersion is the manifest's version, such as "topos.latere.ai/v1".
	APIVersion string `json:"apiVersion"`
	// Kind is "Agent".
	Kind string `json:"kind"`
	// Metadata names the agent.
	Metadata AgentMetadata `json:"metadata"`
	// Status is the API's record of the agent.
	Status AgentStatus `json:"status,omitzero"`

	raw jsontext.Value
}

// RawJSON returns the JSON the API answered for the agent, with every
// field, its spec included.
func (a *Agent) RawJSON() jsontext.Value { return a.raw }

// UnmarshalJSON decodes the agent and keeps the JSON for RawJSON.
func (a *Agent) UnmarshalJSON(b []byte) error {
	type plain Agent
	if err := json.Unmarshal(b, (*plain)(a)); err != nil {
		return err
	}
	a.raw = keep(b)
	return nil
}

// AgentMetadata names an agent.
type AgentMetadata struct {
	// Name identifies the agent among its owner's agents.
	Name string `json:"name"`
	// DisplayName is the name a person reads; empty shows Name.
	DisplayName string `json:"displayName,omitempty"`
	// Labels are the owner's own key-value pairs.
	Labels map[string]string `json:"labels,omitempty"`
}

// AgentStatus is the API's record of an agent.
type AgentStatus struct {
	// ID identifies the agent whoever owns it. A session names it as
	// SessionAgent.ID, and the decision service keys an agent's steps by
	// it, as the Agent of an oversight.Scope.
	ID string `json:"id,omitempty"`
	// Version counts the distinct specs the agent has had; Digest
	// identifies the latest.
	Version int    `json:"version,omitzero"`
	Digest  string `json:"digest,omitempty"`
	// CreatedAt is when the agent was first applied.
	CreatedAt time.Time `json:"createdAt,omitzero"`
	// ArchivedAt is when the agent was archived; nil when it was not.
	ArchivedAt *time.Time `json:"archivedAt,omitempty"`
}

// errNoAgentRef is returned before any request when an agent's id or
// name is empty.
var errNoAgentRef = errors.New("agents: the agent's id or name is empty")

// GetAgent returns the latest version of the agent ref names: an agent's
// id, or a name among the agents of the context the caller acts in, its
// organization's or its own. It returns an error matching
// latere.ErrNotFound when the caller may read no such agent, a name only
// another owner holds included.
func (c *Client) GetAgent(ctx context.Context, ref string) (*Agent, error) {
	if ref == "" {
		return nil, errNoAgentRef
	}
	var out Agent
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodGet, Path: BasePath + "/agents/" + url.PathEscape(ref)}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AgentVersion is one version of an agent: a distinct spec the agent has
// had, in the order it was applied.
type AgentVersion struct {
	// Version is the version's number, from 1; AgentStatus.Version of the
	// agent counts the latest.
	Version int `json:"version"`
	// Digest identifies the version's spec, such as "sha256:" and its hex.
	Digest string `json:"digest"`
	// CreatedBy is the subject that applied the version.
	CreatedBy string `json:"created_by"`
	// CreatedAt is when the version was applied.
	CreatedAt time.Time `json:"created_at"`

	raw jsontext.Value
}

// RawJSON returns the JSON the API answered for the version, with every
// field.
func (v *AgentVersion) RawJSON() jsontext.Value { return v.raw }

// UnmarshalJSON decodes the version and keeps the JSON for RawJSON.
func (v *AgentVersion) UnmarshalJSON(b []byte) error {
	type plain AgentVersion
	if err := json.Unmarshal(b, (*plain)(v)); err != nil {
		return err
	}
	v.raw = keep(b)
	return nil
}

// ListAgentVersionsParams page through an agent's versions. The zero
// value lists them in pages of the API's default size, 50.
type ListAgentVersionsParams struct {
	// Limit is the page size, from 1 to 500; the API's default when zero.
	Limit int
	// Cursor asks ListAgentVersionsPage for the page after the one that
	// returned it. ListAgentVersions ignores it.
	Cursor string
}

func (p *ListAgentVersionsParams) query(cursor string) url.Values {
	q := url.Values{}
	if p != nil && p.Limit > 0 {
		q.Set("limit", strconv.Itoa(p.Limit))
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	return q
}

// ListAgentVersions returns an iterator over the versions of the agent ref
// names, an id or a name as GetAgent reads, in the order the API lists
// them, fetching pages as the loop asks for more. An empty ref yields an
// error before any request; an agent the caller may read none of yields an
// error matching latere.ErrNotFound. params may be nil.
func (c *Client) ListAgentVersions(ctx context.Context, ref string, params *ListAgentVersionsParams) iter.Seq2[*AgentVersion, error] {
	if ref == "" {
		return func(yield func(*AgentVersion, error) bool) { yield(nil, errNoAgentRef) }
	}
	return latere.Items(ctx, func(ctx context.Context, cursor string) (*latere.Page[*AgentVersion], error) {
		return c.agentVersionsPage(ctx, ref, params.query(cursor))
	})
}

// ListAgentVersionsPage returns one page of the versions of the agent ref
// names, starting at params.Cursor, and the cursor of the next page in
// NextCursor. An empty ref returns an error before any request.
func (c *Client) ListAgentVersionsPage(ctx context.Context, ref string, params *ListAgentVersionsParams) (*latere.Page[*AgentVersion], error) {
	if ref == "" {
		return nil, errNoAgentRef
	}
	cursor := ""
	if params != nil {
		cursor = params.Cursor
	}
	return c.agentVersionsPage(ctx, ref, params.query(cursor))
}

func (c *Client) agentVersionsPage(ctx context.Context, ref string, q url.Values) (*latere.Page[*AgentVersion], error) {
	var out latere.Page[*AgentVersion]
	req := &latere.Request{Method: http.MethodGet, Path: BasePath + "/agents/" + url.PathEscape(ref) + "/versions", Query: q}
	if err := c.cfg.Do(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
