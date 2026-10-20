// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"latere.ai/x/sdk-go/internal/sse"
	"latere.ai/x/sdk-go/latere"
)

// The agents API's OpenAPI document, vendored in openapi/agents.json by
// `make openapi`, describes its answers by examples rather than schemas.
// These tests decode every example into the models, check that each field
// a model reads appears in some example, and check that every endpoint the
// client calls is in the document.

type document struct {
	Paths map[string]map[string]json.RawMessage `json:"paths"`
}

func loadDocument(t *testing.T) (*document, any) {
	t.Helper()
	raw, err := os.ReadFile("../openapi/agents.json")
	if err != nil {
		t.Fatal(err)
	}
	var d document
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		t.Fatal(err)
	}
	return &d, tree
}

// examples returns every object in the document that keep reports as an
// example of a model.
func examples(tree any, keep func(map[string]any) bool) []map[string]any {
	var out []map[string]any
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if keep(v) {
				out = append(out, v)
			}
			for _, child := range v {
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	walk(tree)
	return out
}

func jsonFields(t reflect.Type) []string {
	var out []string
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			out = append(out, name)
		}
	}
	return out
}

// checkExamples decodes each example into a new value of model's type and
// reports the model's fields that no example holds, less those absent.
func checkExamples(t *testing.T, name string, found []map[string]any, model any, absent ...string) {
	t.Helper()
	if len(found) == 0 {
		t.Fatalf("the document holds no example of a %s", name)
	}
	seen := map[string]bool{}
	for _, ex := range found {
		raw, err := json.Marshal(ex)
		if err != nil {
			t.Fatal(err)
		}
		v := reflect.New(reflect.TypeOf(model)).Interface()
		if err := json.Unmarshal(raw, v); err != nil {
			t.Errorf("an example %s does not decode: %v\n%s", name, err, raw)
		}
		for k := range ex {
			seen[k] = true
		}
	}
	for _, f := range jsonFields(reflect.TypeOf(model)) {
		if !seen[f] && !slices.Contains(absent, f) {
			t.Errorf("%s.%s appears in no example of the document", name, f)
		}
	}
}

func TestSessionsMatchTheExamples(t *testing.T) {
	_, tree := loadDocument(t)
	found := examples(tree, func(m map[string]any) bool {
		id, _ := m["id"].(string)
		_, hasStatus := m["status"]
		return strings.HasPrefix(id, "ses_") && hasStatus
	})
	// metadata and trigger_id are described in the list operation's text
	// and shown in no example.
	checkExamples(t, "Session", found, Session{}, "metadata", "trigger_id")
}

func TestEventsMatchTheExamples(t *testing.T) {
	_, tree := loadDocument(t)
	found := examples(tree, func(m map[string]any) bool {
		id, _ := m["id"].(string)
		_, hasSeq := m["seq"]
		return strings.HasPrefix(id, "evt_") && hasSeq
	})
	// thread is set only on a helper agent's events and step on events
	// within a turn's steps, which no example shows; turn is shown only in
	// the stream's example, whose frames are text, checked below.
	checkExamples(t, "Event", found, Event{}, "thread", "step", "turn")
}

func TestTheStreamExampleDecodes(t *testing.T) {
	d, _ := loadDocument(t)
	var op struct {
		Responses map[string]struct {
			Content map[string]struct {
				Example string `json:"example"`
			} `json:"content"`
		} `json:"responses"`
	}
	if err := json.Unmarshal(d.Paths["/sessions/{id}/stream"]["get"], &op); err != nil {
		t.Fatal(err)
	}
	example := op.Responses["200"].Content["text/event-stream"].Example
	if example == "" {
		t.Fatal("the stream has no example")
	}
	r := sse.NewReader(strings.NewReader(example))
	n, messages := 0, 0
	for {
		f, err := r.Next()
		if err != nil {
			break
		}
		item, err := decodeFrame(latere.Frame{ID: f.ID, Event: f.Event, Data: f.Data})
		if err != nil {
			t.Errorf("frame %s: %v", f.ID, err)
		}
		if item.Event != nil && item.Event.Seq == 0 {
			t.Errorf("frame %s decoded without a sequence", f.ID)
		}
		if item.Event != nil && item.Event.Type == EventAgentMessage {
			var m AgentMessage
			if err := item.Event.Decode(&m); err != nil || m.Text() == "" || m.StopReason == "" {
				t.Errorf("the example's agent message read as %+v, %v", m, err)
			}
			messages++
		}
		n++
	}
	if n == 0 || messages == 0 {
		t.Errorf("the stream example held %d frames and %d agent messages", n, messages)
	}
}

func TestEndpointsAreInTheDocument(t *testing.T) {
	d, _ := loadDocument(t)
	// The document's paths are written under the server root /v1; the API
	// serves them under /v1/agents.
	for _, e := range []struct{ method, path string }{
		{"get", "/sessions"},
		{"post", "/sessions"},
		{"get", "/sessions/{id}"},
		{"delete", "/sessions/{id}"},
		{"post", "/sessions/{id}/archive"},
		{"post", "/sessions/{id}/unarchive"},
		{"post", "/sessions/{id}/end"},
		{"post", "/sessions/{id}/fork"},
		{"get", "/sessions/{id}/events"},
		{"post", "/sessions/{id}/events"},
		{"get", "/sessions/{id}/stream"},
	} {
		if _, ok := d.Paths[e.path][e.method]; !ok {
			t.Errorf("%s %s is not in the document", strings.ToUpper(e.method), e.path)
		}
	}
}
