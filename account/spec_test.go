// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package account_test

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"latere.ai/x/sdk-go/account"
)

// The models of this package are written by hand from the account API's
// OpenAPI document, vendored in openapi/account.json by `make openapi`.
// These tests hold the two together: every field a model reads is in the
// schema, every field the schema requires is in the model, and every
// endpoint the client calls is in the document.

type schema struct {
	Format     string             `json:"format"`
	Properties map[string]*schema `json:"properties"`
	Required   []string           `json:"required"`
	Items      *schema            `json:"items"`
	Ref        string             `json:"$ref"`
}

type document struct {
	Paths      map[string]map[string]json.RawMessage `json:"paths"`
	Components struct {
		Schemas map[string]*schema `json:"schemas"`
	} `json:"components"`
}

func loadDocument(t *testing.T) *document {
	t.Helper()
	raw, err := os.ReadFile("../openapi/account.json")
	if err != nil {
		t.Fatal(err)
	}
	var d document
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	return &d
}

// timeType is the type a date-time property decodes into.
var timeType = reflect.TypeFor[time.Time]()

// fieldType returns the type of the field of t named name in JSON.
func fieldType(t reflect.Type, name string) reflect.Type {
	for i := range t.NumField() {
		if n, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ","); n == name {
			return t.Field(i).Type
		}
	}
	return nil
}

// jsonFields returns the JSON names of a struct type's fields.
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

func TestModelsMatchTheSchemas(t *testing.T) {
	d := loadDocument(t)
	s := d.Components.Schemas
	device := s["Device"]
	cases := map[string]struct {
		model  any
		schema *schema
	}{
		"AppliedAgent":    {account.AppliedAgent{}, s["AppliedAgent"]},
		"EffortLevels":    {account.EffortLevels{}, s["EffortLevels"]},
		"EffortLevel":     {account.EffortLevel{}, s["EffortLevel"]},
		"EffortAuto":      {account.EffortAuto{}, s["EffortAuto"]},
		"EffortModel":     {account.EffortModel{}, s["EffortModel"]},
		"NextTurn":        {account.NextTurn{}, s["NextTurn"]},
		"Price":           {account.Price{}, s["Price"]},
		"FreeTurns":       {account.FreeTurns{}, s["FreeTurns"]},
		"Wallet":          {account.Wallet{}, s["Wallet"]},
		"NextGrant":       {account.NextGrant{}, s["NextGrant"]},
		"TopUp":           {account.TopUp{}, s["TopUp"]},
		"Preferences":     {account.Preferences{}, s["Preferences"]},
		"Network":         {account.Network{}, s["Network"]},
		"Sharing":         {account.Sharing{}, s["Sharing"]},
		"Profile":         {account.Profile{}, s["Profile"]},
		"Instructions":    {account.Instructions{}, s["Instructions"]},
		"Pins":            {account.Pins{}, s["Pins"]},
		"Bookmarks":       {account.Bookmarks{}, s["Bookmarks"]},
		"DataPreferences": {account.DataPreferences{}, s["DataPreferences"]},
		"Device":          {account.Device{}, device},
		"DeviceFolder":    {account.DeviceFolder{}, device.Properties["folders"].Items},
		"DeviceNetwork":   {account.DeviceNetwork{}, device.Properties["network"]},
	}
	for name, c := range cases {
		if c.schema == nil {
			t.Errorf("%s: the document has no schema for it", name)
			continue
		}
		fields := jsonFields(reflect.TypeOf(c.model))
		for _, f := range fields {
			prop, ok := c.schema.Properties[f]
			if !ok {
				t.Errorf("%s.%s is not in the schema", name, f)
				continue
			}
			if prop.Format == "date-time" && fieldType(reflect.TypeOf(c.model), f) != timeType {
				t.Errorf("%s.%s is a date-time, read as %v", name, f, fieldType(reflect.TypeOf(c.model), f))
			}
		}
		for _, r := range c.schema.Required {
			if !slices.Contains(fields, r) {
				t.Errorf("%s does not read %s, which the schema requires", name, r)
			}
		}
	}
}

// Fields a schema defines that a model leaves out on purpose, each with
// its reason. A field missing from a model and from this list fails
// TestModelsReadEveryField, so a field the API adds is decided on.
var leftOut = map[string]string{
	"EffortLevel.free_searches": "web search allowances; not read by any client yet",
}

func TestModelsReadEveryField(t *testing.T) {
	d := loadDocument(t)
	s := d.Components.Schemas
	for name, model := range map[string]any{
		"EffortLevel": account.EffortLevel{}, "Wallet": account.Wallet{}, "Preferences": account.Preferences{},
		"Device": account.Device{}, "Pins": account.Pins{}, "Network": account.Network{}, "Price": account.Price{},
	} {
		fields := jsonFields(reflect.TypeOf(model))
		for prop := range s[name].Properties {
			if !slices.Contains(fields, prop) && leftOut[name+"."+prop] == "" {
				t.Errorf("%s does not read %s; model it or add it to leftOut with a reason", name, prop)
			}
		}
	}
}

func TestEndpointsAreInTheDocument(t *testing.T) {
	d := loadDocument(t)
	for _, e := range []struct{ method, path string }{
		{"put", "/v1/account/agents/{id}"},
		{"get", "/v1/account/effort"},
		{"get", "/v1/account/wallet"},
		{"get", "/v1/account/preferences"},
		{"put", "/v1/account/preferences/pins/{session_id}"},
		{"delete", "/v1/account/preferences/pins/{session_id}"},
		{"get", "/v1/account/devices"},
	} {
		if _, ok := d.Paths[e.path][e.method]; !ok {
			t.Errorf("%s %s is not in the document", strings.ToUpper(e.method), e.path)
		}
	}
}
