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
	"testing"

	"latere.ai/x/sdk-go/latere"
)

// The search's models are held to the schemas of its answer, and the
// query it sends to the parameters of its route.

func TestSearchMatchesTheSchemas(t *testing.T) {
	d, _ := loadDocument(t)
	s := d.Components.Schemas
	d.checkModel(t, "SessionMatch", reflect.TypeFor[SessionMatch](), s["SearchResult"], true)
	d.checkModel(t, "EventMatch", reflect.TypeFor[EventMatch](), s["Match"], true)
	d.checkModel(t, "ExcerptPart", reflect.TypeFor[ExcerptPart](), s["Fragment"], true)
}

// TestTheSearchQueryFitsItsParameters sends the fullest search and checks
// that each query parameter is one the route takes, with a value its enum
// lists where it has one, and that the route requires nothing the search
// leaves out.
func TestTheSearchQueryFitsItsParameters(t *testing.T) {
	d, _ := loadDocument(t)
	type parameter struct {
		Name     string  `json:"name"`
		In       string  `json:"in"`
		Required bool    `json:"required"`
		Schema   *schema `json:"schema"`
	}
	var op struct {
		Parameters []parameter `json:"parameters"`
	}
	if err := json.Unmarshal(d.Paths["/sessions/search"]["get"], &op); err != nil {
		t.Fatal(err)
	}
	var sent map[string][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"items":[]}`)
	}))
	t.Cleanup(srv.Close)
	cfg, err := latere.NewConfig(latere.WithBaseURL(srv.URL), latere.WithTokenSource(latere.StaticToken("key_test")), latere.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(cfg).SearchSessionsPage(t.Context(), &SearchSessionsParams{
		Query: "notes", Agent: "a", Status: StatusEnded, Runner: RunnerExternal, Archived: ArchivedOnly, Rollout: "any", Limit: MaxSearchLimit, Cursor: "c",
	}); err != nil {
		t.Fatal(err)
	}
	for name, values := range sent {
		i := slices.IndexFunc(op.Parameters, func(p parameter) bool { return p.Name == name && p.In == "query" })
		if i < 0 {
			t.Errorf("the search sends %s, which the route does not take", name)
			continue
		}
		if s := op.Parameters[i].Schema; s != nil && len(s.Enum) > 0 && !slices.Contains(s.Enum, values[0]) {
			t.Errorf("the search sends %s=%s, which the route's enum does not list", name, values[0])
		}
	}
	for _, p := range op.Parameters {
		if _, ok := sent[p.Name]; p.Required && !ok {
			t.Errorf("the route requires %s, which the search does not send", p.Name)
		}
	}
}
