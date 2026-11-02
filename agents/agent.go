// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
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

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the agent, with every
// field, its spec included.
func (a *Agent) RawJSON() json.RawMessage { return a.raw }

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
	Version int    `json:"version,omitempty"`
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
