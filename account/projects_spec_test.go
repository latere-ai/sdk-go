// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package account_test

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"testing"

	"latere.ai/x/sdk-go/account"
)

// operation is the part of an operation of the document these tests read:
// its query parameters' bounds, its request body's schema and the schema
// of each answer.
type operation struct {
	Parameters []struct {
		Name   string `json:"name"`
		Schema struct {
			Maximum int `json:"maximum"`
		} `json:"schema"`
	} `json:"parameters"`
	RequestBody struct {
		Content map[string]struct {
			Schema *schema `json:"schema"`
		} `json:"content"`
	} `json:"requestBody"`
	Responses map[string]struct {
		Content map[string]struct {
			Schema *schema `json:"schema"`
		} `json:"content"`
	} `json:"responses"`
}

func readOperation(t *testing.T, d *document, method, path string) *operation {
	t.Helper()
	raw, ok := d.Paths[path][method]
	if !ok {
		t.Fatalf("%s %s is not in the document", method, path)
	}
	var op operation
	if err := json.Unmarshal(raw, &op); err != nil {
		t.Fatal(err)
	}
	return &op
}

// holdToSchema checks that every field model reads is in sch, with a
// date-time read as a time.Time, and that model reads every field sch
// has.
func holdToSchema(t *testing.T, name string, model any, sch *schema) {
	t.Helper()
	if sch == nil {
		t.Errorf("%s: the document has no schema for it", name)
		return
	}
	typ := reflect.TypeOf(model)
	fields := jsonFields(typ)
	for _, f := range fields {
		prop, ok := sch.Properties[f]
		if !ok {
			t.Errorf("%s.%s is not in the schema", name, f)
			continue
		}
		if prop.Format == "date-time" && fieldType(typ, f) != timeType {
			t.Errorf("%s.%s is a date-time, read as %v", name, f, fieldType(typ, f))
		}
	}
	for prop := range sch.Properties {
		if !slices.Contains(fields, prop) {
			t.Errorf("%s does not read %s", name, prop)
		}
	}
}

// The answers and the bodies of the project and trigger routes that the
// document gives no schema of their own.
func TestTheInlineSchemasMatch(t *testing.T) {
	d := loadDocument(t)
	inbound := readOperation(t, d, "get", "/v1/account/triggers/inbound").Responses["200"].Content["application/json"].Schema
	holdToSchema(t, "TriggerInbound", account.TriggerInbound{}, inbound)
	if inbound != nil {
		holdToSchema(t, "InboundMail", account.InboundMail{}, inbound.Properties["mail"])
	}
	hook := readOperation(t, d, "post", "/v1/account/triggers/{trigger_id}/hook").Responses["201"].Content["application/json"].Schema
	holdToSchema(t, "TriggerHook", account.TriggerHook{}, hook)
	address := readOperation(t, d, "post", "/v1/account/triggers/{trigger_id}/address").Responses["201"].Content["application/json"].Schema
	holdToSchema(t, "TriggerAddress", account.TriggerAddress{}, address)
	update := readOperation(t, d, "patch", "/v1/account/projects/{id}").RequestBody.Content["application/json"].Schema
	holdToSchema(t, "UpdateProjectParams", account.UpdateProjectParams{}, update)
}

// The values the package names are the ones the document lists, and its
// bounds the document's.
func TestProjectAndInletValuesAreTheDocuments(t *testing.T) {
	raw, err := os.ReadFile("../openapi/account.json")
	if err != nil {
		t.Fatal(err)
	}
	type property struct {
		Enum      []string `json:"enum"`
		MaxLength int      `json:"maxLength"`
		MaxItems  int      `json:"maxItems"`
		Items     struct {
			Enum []string `json:"enum"`
		} `json:"items"`
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	var d struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]property `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	s := d.Components.Schemas
	for name, c := range map[string]struct {
		declared []string
		listed   []string
	}{
		"ProjectAction": {[]string{string(account.ProjectRename), string(account.ProjectEditInstructions), string(account.ProjectAddApp), string(account.ProjectDelete)},
			s["Project"].Properties["actions"].Items.Enum},
		"OwnerKind": {[]string{string(account.OwnerPersonal), string(account.OwnerOrganization)}, s["Project"].Properties["owner"].Properties["kind"].Enum},
		"InletKind": {[]string{string(account.InletWebhook), string(account.InletMail)}, s["TriggerInlet"].Properties["kind"].Enum},
	} {
		if len(c.listed) == 0 || !slices.Equal(slices.Sorted(slices.Values(c.declared)), slices.Sorted(slices.Values(c.listed))) {
			t.Errorf("%s declares %v; the document lists %v", name, c.declared, c.listed)
		}
	}
	create := s["ProjectCreate"].Properties
	if create["name"].MaxLength != account.MaxProjectName || create["apps"].MaxItems != account.MaxProjectApps {
		t.Errorf("a project's name holds %d characters and %d apps; the package says %d and %d",
			create["name"].MaxLength, create["apps"].MaxItems, account.MaxProjectName, account.MaxProjectApps)
	}
	list := readOperation(t, loadDocument(t), "get", "/v1/account/projects")
	for _, p := range list.Parameters {
		if p.Name == "limit" && p.Schema.Maximum != account.MaxProjectsPage {
			t.Errorf("a page holds %d projects; MaxProjectsPage is %d", p.Schema.Maximum, account.MaxProjectsPage)
		}
	}
	page := list.Responses["200"].Content["application/json"].Schema
	if page == nil || page.Properties["items"] == nil || page.Properties["items"].Items.Ref != "#/components/schemas/Project" || page.Properties["next_cursor"] == nil {
		t.Errorf("the list's page is %+v; ListProjectsPage reads items of Project and next_cursor", page)
	}
}
