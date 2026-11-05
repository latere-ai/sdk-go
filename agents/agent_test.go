// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"latere.ai/x/sdk-go/agents"
	"latere.ai/x/sdk-go/latere"
)

// agent is an agent as the API answers it, with a spec the package keeps
// whole in RawJSON.
const agent = `{"apiVersion":"topos.latere.ai/v1","kind":"Agent",
"metadata":{"name":"release-notes","displayName":"Release notes","labels":{"team":"docs"}},
"spec":{"model":"claude","instructions":"Write the release notes."},
"status":{"id":"agent_01M34FSDG0Q60424XT3D96DRAJ","version":3,"digest":"sha256:dac7","createdAt":"2026-09-22T12:02:00Z"}}`

// An agent is read by its name in the caller's context, or by its id, and
// its id is the one a session and the decision service name it by.
func TestGetAgent(t *testing.T) {
	c, calls := fakeAPI(t, map[string]string{
		"GET /v1/agents/agents/release-notes":                    agent,
		"GET /v1/agents/agents/agent_01M34FSDG0Q60424XT3D96DRAJ": agent,
	})
	got, err := c.GetAgent(t.Context(), "release-notes")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "Agent" || got.Metadata.Name != "release-notes" || got.Metadata.DisplayName != "Release notes" || got.Metadata.Labels["team"] != "docs" {
		t.Errorf("decoded the metadata as %+v", got.Metadata)
	}
	st := got.Status
	if st.ID != "agent_01M34FSDG0Q60424XT3D96DRAJ" || st.Version != 3 || st.Digest != "sha256:dac7" || st.CreatedAt.IsZero() || st.ArchivedAt != nil {
		t.Errorf("decoded the status as %+v", st)
	}
	if !strings.Contains(string(got.RawJSON()), `"instructions":"Write the release notes."`) {
		t.Errorf("RawJSON lost the spec: %s", got.RawJSON())
	}
	if byID, err := c.GetAgent(t.Context(), st.ID); err != nil || byID.Metadata.Name != "release-notes" {
		t.Errorf("by its id: %+v, %v", byID, err)
	}
	if (*calls)[0].Method != http.MethodGet || (*calls)[0].Path != "/v1/agents/agents/release-notes" {
		t.Errorf("sent %+v", (*calls)[0])
	}
	// A name is one segment of the path, escaped.
	if _, err := c.GetAgent(t.Context(), "a/b"); !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("a name with a slash: %v", err)
	}
	if last := (*calls)[len(*calls)-1]; last.Path != "/v1/agents/agents/a%2Fb" {
		t.Errorf("sent %+v", last)
	}
	if _, err := c.GetAgent(t.Context(), "missing"); !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("a missing agent: %v", err)
	}
	n := len(*calls)
	if _, err := c.GetAgent(t.Context(), ""); err == nil || len(*calls) != n {
		t.Errorf("an empty ref: %v, %d calls", err, len(*calls)-n)
	}
}

const agentVersions = `{"items":[
{"version":3,"digest":"sha256:dac7","created_by":"https://login.example|alice","created_at":"2026-09-22T12:02:00Z"},
{"version":2,"digest":"sha256:beef","created_by":"https://login.example|alice","created_at":"2026-09-21T12:02:00Z"}],"next_cursor":"Mg"}`

// An agent's versions are read by its name or id, a page at a time, each
// with the digest a program names it by.
func TestListAgentVersions(t *testing.T) {
	c, calls := fakeAPI(t, map[string]string{
		"GET /v1/agents/agents/release-notes/versions": agentVersions,
	})
	page, err := c.ListAgentVersionsPage(t.Context(), "release-notes", &agents.ListAgentVersionsParams{Limit: 2, Cursor: "MQ"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.NextCursor != "Mg" {
		t.Fatalf("page = %+v", page)
	}
	v := page.Items[0]
	if v.Version != 3 || v.Digest != "sha256:dac7" || v.CreatedBy != "https://login.example|alice" || v.CreatedAt.IsZero() {
		t.Errorf("decoded %+v", v)
	}
	if !strings.Contains(string(v.RawJSON()), `"created_by"`) {
		t.Errorf("RawJSON = %s", v.RawJSON())
	}
	if got := (*calls)[0].Query; got != "cursor=MQ&limit=2" {
		t.Errorf("query = %q", got)
	}
	// The iterator follows the cursor until a page names none; the fake
	// answers every page with one, so stop after the second page.
	n := 0
	for v, err := range c.ListAgentVersions(t.Context(), "release-notes", nil) {
		if err != nil || v == nil {
			t.Fatalf("%v, %v", v, err)
		}
		if n++; n == 3 {
			break
		}
	}
	if n != 3 {
		t.Errorf("read %d versions", n)
	}
	if _, err := c.ListAgentVersionsPage(t.Context(), "a/b", nil); !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("a missing agent: %v", err)
	}
	if last := (*calls)[len(*calls)-1]; last.Path != "/v1/agents/agents/a%2Fb/versions" {
		t.Errorf("sent %+v", last)
	}
}

// An empty ref is refused before any request, by both methods.
func TestListAgentVersionsNeedsARef(t *testing.T) {
	c, calls := fakeAPI(t, nil)
	if _, err := c.ListAgentVersionsPage(t.Context(), "", nil); err == nil {
		t.Error("the page method accepted an empty ref")
	}
	for _, err := range c.ListAgentVersions(t.Context(), "", nil) {
		if err == nil {
			t.Error("the iterator accepted an empty ref")
		}
	}
	if len(*calls) != 0 {
		t.Errorf("sent %d requests", len(*calls))
	}
}
