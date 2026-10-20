// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents

import (
	"strings"
	"testing"
)

// The trigger examples of the agents document show one event trigger
// with a minimal session, so the models' other fields appear in none.
// Each field listed as absent below is a field of the trigger manifest
// the API accepts and answers, left out of the examples.

func triggerExamples(tree any) []map[string]any {
	return examples(tree, func(m map[string]any) bool { return m["kind"] == "Trigger" })
}

func TestTriggersMatchTheExamples(t *testing.T) {
	_, tree := loadDocument(t)
	found := triggerExamples(tree)
	checkExamples(t, "Trigger", found, Trigger{})

	var metadata, specs, sessions, filters, statuses, counts []map[string]any
	for _, ex := range found {
		if m, ok := ex["metadata"].(map[string]any); ok {
			metadata = append(metadata, m)
		}
		spec, _ := ex["spec"].(map[string]any)
		if spec == nil {
			continue
		}
		specs = append(specs, spec)
		if s, ok := spec["session"].(map[string]any); ok {
			sessions = append(sessions, s)
		}
		if on, ok := spec["on"].(map[string]any); ok {
			filters = append(filters, on)
		}
		if st, ok := ex["status"].(map[string]any); ok {
			statuses = append(statuses, st)
			if c, ok := st["counts"].(map[string]any); ok {
				counts = append(counts, c)
			}
		}
	}
	checkExamples(t, "TriggerMetadata", metadata, TriggerMetadata{}, "displayName", "labels", "annotations")
	// schedule is the other kind of trigger than the example's event one.
	checkExamples(t, "TriggerSpec", specs, TriggerSpec{}, "schedule", "maxActive")
	checkExamples(t, "TriggerSession", sessions, TriggerSession{}, "title", "policy", "key", "machine", "resources", "budget", "limits")
	checkExamples(t, "EventFilter", filters, EventFilter{}, "resources", "match")
	// archivedAt is answered only for an archived trigger, nextFireAt only
	// for a schedule.
	checkExamples(t, "TriggerStatus", statuses, TriggerStatus{}, "archivedAt", "nextFireAt")
	checkExamples(t, "FiringCounts", counts, FiringCounts{})
}

func TestFiringsMatchTheExamples(t *testing.T) {
	_, tree := loadDocument(t)
	found := examples(tree, func(m map[string]any) bool {
		id, _ := m["id"].(string)
		return strings.HasPrefix(id, "frg_")
	})
	// reason is answered only for a refused or failed firing.
	checkExamples(t, "Firing", found, Firing{}, "reason")
	var events []map[string]any
	for _, f := range found {
		if e, ok := f["event"].(map[string]any); ok {
			events = append(events, e)
		}
	}
	checkExamples(t, "FiringEvent", events, FiringEvent{})
}

func TestTheFireExampleIsATriggerEvent(t *testing.T) {
	_, tree := loadDocument(t)
	found := examples(tree, func(m map[string]any) bool {
		_, hasVerb := m["verb"]
		_, hasPayload := m["payload"]
		return hasVerb && hasPayload
	})
	// The vendored document's example names no subject; the field is the
	// event envelope's own, which the trigger's filter reads as
	// event.subject. The schemas topos v0.50.0 publishes list it.
	checkExamples(t, "TriggerEvent", found, TriggerEvent{}, "subject")
}

func TestTriggerEndpointsAreInTheDocument(t *testing.T) {
	d, _ := loadDocument(t)
	for _, e := range []struct{ method, path string }{
		{"get", "/triggers"},
		{"put", "/triggers/{name}"},
		{"get", "/triggers/{ref}"},
		{"delete", "/triggers/{ref}"},
		{"post", "/triggers/{ref}/fire"},
		{"get", "/triggers/{ref}/firings"},
	} {
		if _, ok := d.Paths[e.path][e.method]; !ok {
			t.Errorf("%s %s is not in the document", strings.ToUpper(e.method), e.path)
		}
	}
}
