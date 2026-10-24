// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package account

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// TestTheForecastReadsItsSchemas holds the forecast's wire types, which
// Forecast's UnmarshalJSON reads its calendar dates through, to the
// account document: every member they read is in the schema, and every
// member the schema has, required or not, is read.
func TestTheForecastReadsItsSchemas(t *testing.T) {
	raw, err := os.ReadFile("../openapi/account.json")
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]json.RawMessage `json:"properties"`
				Required   []string                   `json:"required"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	for name, model := range map[string]any{
		"Forecast": forecastJSON{}, "ForecastDay": forecastDayJSON{}, "ForecastItem": forecastItemJSON{},
		"ForecastItemDay": forecastItemDayJSON{}, "ForecastSkipping": forecastSkippingJSON{},
	} {
		sch, ok := d.Components.Schemas[name]
		if !ok {
			t.Errorf("the document has no schema %s", name)
			continue
		}
		var fields []string
		ty := reflect.TypeOf(model)
		for i := range ty.NumField() {
			if n, _, _ := strings.Cut(ty.Field(i).Tag.Get("json"), ","); n != "" {
				fields = append(fields, n)
			}
		}
		for _, f := range fields {
			if _, ok := sch.Properties[f]; !ok {
				t.Errorf("%s.%s is not in the schema", name, f)
			}
		}
		for p := range sch.Properties {
			if !slices.Contains(fields, p) {
				t.Errorf("%s does not read %s", name, p)
			}
		}
		for _, r := range sch.Required {
			if !slices.Contains(fields, r) {
				t.Errorf("%s does not read %s, which the schema requires", name, r)
			}
		}
	}
}
