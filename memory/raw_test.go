// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package memory_test

import (
	"encoding/json"
	"testing"

	"latere.ai/x/sdk-go/memory"
)

// rawResources are the resources the package answers, each new.
func rawResources() map[string]interface{ RawJSON() json.RawMessage } {
	return map[string]interface{ RawJSON() json.RawMessage }{
		"memory":           &memory.Memory{},
		"list":             &memory.List{},
		"settings changed": &memory.SettingsChanged{},
		"forgotten list":   &memory.ForgottenList{},
		"imported":         &memory.Imported{},
		"run list":         &memory.RunList{},
		"run":              &memory.Run{},
		"undone":           &memory.Undone{},
	}
}

// Each resource the package answers keeps the JSON it was decoded from, a
// field the package does not model included.
func TestEveryResourceKeepsItsJSON(t *testing.T) {
	for name, v := range rawResources() {
		if v.RawJSON() != nil {
			t.Errorf("%s: one the program built has JSON", name)
		}
		raw := `{"added_later":"kept"}`
		if err := json.Unmarshal([]byte(raw), v); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(v.RawJSON()) != raw {
			t.Errorf("%s: RawJSON = %s, want %s", name, v.RawJSON(), raw)
		}
	}
}

// JSON of the wrong shape is refused, and nothing is kept.
func TestAResourceRefusesJSONOfTheWrongShape(t *testing.T) {
	for name, v := range rawResources() {
		if err := json.Unmarshal([]byte(`[]`), v); err == nil {
			t.Errorf("%s decoded an array", name)
		}
		if v.RawJSON() != nil {
			t.Errorf("%s kept JSON it refused", name)
		}
	}
}
