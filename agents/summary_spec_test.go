// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"latere.ai/x/sdk-go/latere"
)

// The summary's models are held to the schemas of its answer, and the
// query it sends to the parameters of its route.

func TestSummaryMatchesTheSchemas(t *testing.T) {
	d, _ := loadDocument(t)
	s := d.Components.Schemas
	d.checkModel(t, "SessionSummary", reflect.TypeFor[SessionSummary](), s["Summary"], true)
	d.checkModel(t, "SessionCounts", reflect.TypeFor[SessionCounts](), s["Counts"], true)
}

// TestTheSummaryQueryFitsItsParameters sends the fullest summary and
// checks that each query parameter is one the route takes, a metadata
// entry by the list's metadata.<key> form, with a value its enum lists
// where it has one, and that the route requires nothing it leaves out.
func TestTheSummaryQueryFitsItsParameters(t *testing.T) {
	d, _ := loadDocument(t)
	type parameter struct {
		Name     string  `json:"name"`
		In       string  `json:"in"`
		Required bool    `json:"required"`
		Schema   *schema `json:"schema"`
	}
	var op struct {
		Description string      `json:"description"`
		Parameters  []parameter `json:"parameters"`
	}
	if err := json.Unmarshal(d.Paths["/sessions/summary"]["get"], &op); err != nil {
		t.Fatal(err)
	}
	var sent map[string][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"sessions":{"running":0,"waiting_for_approval":0,"idle":0,"ended":0},"agents":0}`)
	}))
	t.Cleanup(srv.Close)
	cfg, err := latere.NewConfig(latere.WithBaseURL(srv.URL), latere.WithTokenSource(latere.StaticToken("key_test")), latere.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(cfg).GetSessionSummary(t.Context(), &SessionSummaryParams{
		Agent: "a", Runner: RunnerExternal, Archived: ArchivedOnly, Trigger: "trg_1", Rollout: "any", MetadataKey: "project", MetadataValue: "p1",
	}); err != nil {
		t.Fatal(err)
	}
	for name, values := range sent {
		if strings.HasPrefix(name, "metadata.") {
			if !strings.Contains(op.Description, "metadata.<key>") {
				t.Errorf("the summary sends %s, and the route's description names no metadata.<key> filter", name)
			}
			continue
		}
		i := slices.IndexFunc(op.Parameters, func(p parameter) bool { return p.Name == name && p.In == "query" })
		if i < 0 {
			t.Errorf("the summary sends %s, which the route does not take", name)
			continue
		}
		if s := op.Parameters[i].Schema; s != nil && len(s.Enum) > 0 && !slices.Contains(s.Enum, values[0]) {
			t.Errorf("the summary sends %s=%s, which the route's enum does not list", name, values[0])
		}
	}
	for _, p := range op.Parameters {
		if _, ok := sent[p.Name]; p.Required && !ok {
			t.Errorf("the route requires %s, which the summary does not send", p.Name)
		}
	}
}
