// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package connections_test

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"latere.ai/x/sdk-go/connections"
)

// The models of this package are written by hand from the connections
// API's OpenAPI document, vendored in openapi/connections.json by `make
// openapi`. These tests hold the two together: every field a model reads
// is in the schema, every field the schema requires is in the model, every
// field the schema defines is read or listed in leftOut with its reason,
// and every endpoint the client calls is in the document.

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
	raw, err := os.ReadFile("../openapi/connections.json")
	if err != nil {
		t.Fatal(err)
	}
	var d document
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	return &d
}

var timeType = reflect.TypeFor[time.Time]()

func fieldType(t reflect.Type, name string) reflect.Type {
	for i := range t.NumField() {
		if n, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ","); n == name {
			ft := t.Field(i).Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			return ft
		}
	}
	return nil
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

// models pairs each model with the schema it is written from.
func models(d *document) map[string]struct {
	model  any
	schema *schema
} {
	s := d.Components.Schemas
	add := s["AddConnector"]
	return map[string]struct {
		model  any
		schema *schema
	}{
		"Connection":       {connections.Connection{}, s["Connection"]},
		"ConnectionLinks":  {connections.ConnectionLinks{}, s["Connection"].Properties["links"]},
		"ToolMarks":        {connections.ToolMarks{}, s["Marks"]},
		"Connector":        {connections.Connector{}, s["Connector"]},
		"ConnectorAuth":    {connections.ConnectorAuth{}, s["Connector"].Properties["auth"]},
		"Inject":           {connections.Inject{}, s["Inject"]},
		"Credential":       {connections.Credential{}, s["Credential"]},
		"NewConnectorAuth": {connections.NewConnectorAuth{}, add.Properties["auth"]},
		"ConnectorClient":  {connections.ConnectorClient{}, add.Properties["auth"].Properties["client"]},
		"Tool":             {connections.Tool{}, s["ToolList"].Properties["items"].Items},
		"Call":             {connections.Call{}, s["Call"]},
	}
}

func TestModelsMatchTheSchemas(t *testing.T) {
	for name, c := range models(loadDocument(t)) {
		if c.schema == nil {
			t.Errorf("%s: the document has no schema for it", name)
			continue
		}
		typ := reflect.TypeOf(c.model)
		fields := jsonFields(typ)
		for _, f := range fields {
			prop, ok := c.schema.Properties[f]
			if !ok {
				t.Errorf("%s.%s is not in the schema", name, f)
				continue
			}
			if prop.Format == "date-time" && fieldType(typ, f) != timeType {
				t.Errorf("%s.%s is a date-time, read as %v", name, f, fieldType(typ, f))
			}
		}
		for _, r := range c.schema.Required {
			if !slices.Contains(fields, r) {
				t.Errorf("%s does not read %s, which the schema requires", name, r)
			}
		}
	}
}

// leftOut lists fields a schema defines that a model does not read, each
// with its reason. None so far: every field of the answers is modeled.
var leftOut = map[string]string{}

func TestModelsReadEveryField(t *testing.T) {
	for name, c := range models(loadDocument(t)) {
		fields := jsonFields(reflect.TypeOf(c.model))
		for prop := range c.schema.Properties {
			if !slices.Contains(fields, prop) && leftOut[name+"."+prop] == "" {
				t.Errorf("%s does not read %s; model it or add it to leftOut with a reason", name, prop)
			}
		}
	}
}

// The request bodies the client builds as maps must name only fields their
// schemas define.
func TestRequestBodiesUseTheSchemasFields(t *testing.T) {
	s := loadDocument(t).Components.Schemas
	for name, fields := range map[string][]string{
		"Connect":           {"connector", "name", "credential", "return_to", "scopes"},
		"Rename":            {"name"},
		"Confirm":           {"code"},
		"Reconnect":         {"return_to", "scopes"},
		"ReplaceCredential": {"credential"},
		"AddConnector":      {"name", "title", "kind", "url", "description", "icon", "auth", "tools"},
		"EditConnector":     {"title", "description", "icon", "disabled", "auth", "tools"},
	} {
		for _, f := range fields {
			if _, ok := s[name].Properties[f]; !ok {
				t.Errorf("the client sends %s.%s, which the schema does not define", name, f)
			}
		}
	}
}

func TestEndpointsAreInTheDocument(t *testing.T) {
	d := loadDocument(t)
	for _, e := range []struct{ method, path string }{
		{"get", "/v1/connections/connections"},
		{"post", "/v1/connections/connections"},
		{"get", "/v1/connections/connections/{id}"},
		{"patch", "/v1/connections/connections/{id}"},
		{"delete", "/v1/connections/connections/{id}"},
		{"post", "/v1/connections/connections/{id}/confirm"},
		{"post", "/v1/connections/connections/{id}/reconnect"},
		{"put", "/v1/connections/connections/{id}/credential"},
		{"get", "/v1/connections/connections/{id}/tools"},
		{"get", "/v1/connections/connections/{id}/calls"},
		{"get", "/v1/connections/connectors"},
		{"post", "/v1/connections/connectors"},
		{"get", "/v1/connections/connectors/{ref}"},
		{"patch", "/v1/connections/connectors/{ref}"},
		{"delete", "/v1/connections/connectors/{ref}"},
	} {
		if _, ok := d.Paths[e.path][e.method]; !ok {
			t.Errorf("%s %s is not in the document", strings.ToUpper(e.method), e.path)
		}
	}
}
