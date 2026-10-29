// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package memory_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"latere.ai/x/sdk-go/latere"
	"latere.ai/x/sdk-go/memory"
)

// The models of this package are written by hand from the account API's
// OpenAPI document, vendored in openapi/account.json by `make openapi`,
// whose memory operations they cover. These tests hold the two together:
// every field a model reads is in its schema and every field the schema
// has is in the model, the values the package names are the ones the
// document lists, each call's answer is the schema its model is held to,
// and each refusal the package names is one the document describes.

type schema struct {
	Format     string             `json:"format"`
	Properties map[string]*schema `json:"properties"`
	Required   []string           `json:"required"`
	Items      *schema            `json:"items"`
	Ref        string             `json:"$ref"`
	Enum       []string           `json:"enum"`
	AllOf      []*schema          `json:"allOf"`
}

// merged returns the schema the parts of sch's allOf make together, each
// part named by its $ref resolved in schemas, or sch itself when it has
// no allOf.
func merged(t *testing.T, schemas map[string]*schema, sch *schema) *schema {
	t.Helper()
	if sch == nil || len(sch.AllOf) == 0 {
		return sch
	}
	out := &schema{Properties: map[string]*schema{}}
	for _, part := range sch.AllOf {
		if name, ok := strings.CutPrefix(part.Ref, "#/components/schemas/"); ok {
			part = schemas[name]
		}
		if part == nil {
			t.Fatalf("an allOf names a schema the document does not have")
		}
		for name, prop := range part.Properties {
			out.Properties[name] = prop
		}
		out.Required = append(out.Required, part.Required...)
	}
	return out
}

// operation is the part of an operation these tests read.
type operation struct {
	Parameters []struct {
		Ref string `json:"$ref"`
	} `json:"parameters"`
	RequestBody struct {
		Content map[string]struct {
			Schema *schema `json:"schema"`
		} `json:"content"`
	} `json:"requestBody"`
	Responses map[string]struct {
		Ref     string `json:"$ref"`
		Content map[string]struct {
			Schema *schema `json:"schema"`
		} `json:"content"`
	} `json:"responses"`
}

type document struct {
	Paths      map[string]map[string]json.RawMessage `json:"paths"`
	Components struct {
		Schemas   map[string]*schema `json:"schemas"`
		Responses map[string]struct {
			Description string `json:"description"`
		} `json:"responses"`
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

func readOperation(t *testing.T, d *document, method, path string) *operation {
	t.Helper()
	raw, ok := d.Paths[path][method]
	if !ok {
		t.Fatalf("%s %s is not in the document", strings.ToUpper(method), path)
	}
	var op operation
	if err := json.Unmarshal(raw, &op); err != nil {
		t.Fatal(err)
	}
	return &op
}

// timeType is the type a date-time property decodes into.
var timeType = reflect.TypeFor[time.Time]()

// jsonFields returns the JSON names of a struct type's fields, by the
// type of each, those of an embedded struct among them, as encoding/json
// reads them.
func jsonFields(t reflect.Type) map[string]reflect.Type {
	out := map[string]reflect.Type{}
	for i := range t.NumField() {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if f.Anonymous && tag == "" && f.Type.Kind() == reflect.Struct {
			for name, typ := range jsonFields(f.Type) {
				out[name] = typ
			}
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if name != "" && name != "-" {
			out[name] = f.Type
		}
	}
	return out
}

// holdToSchema checks that every field model reads is in sch, with a
// date-time read as a time.Time, that model reads every field sch has,
// and that each field sch requires is one model reads.
func holdToSchema(t *testing.T, name string, model any, sch *schema) {
	t.Helper()
	if sch == nil {
		t.Errorf("%s: the document has no schema for it", name)
		return
	}
	fields := jsonFields(reflect.TypeOf(model))
	for f, typ := range fields {
		prop, ok := sch.Properties[f]
		if !ok {
			t.Errorf("%s.%s is not in the schema", name, f)
			continue
		}
		if prop.Format == "date-time" && typ != timeType {
			t.Errorf("%s.%s is a date-time, read as %v", name, f, typ)
		}
	}
	for prop := range sch.Properties {
		if _, ok := fields[prop]; !ok {
			t.Errorf("%s does not read %s", name, prop)
		}
	}
	for _, r := range sch.Required {
		if _, ok := fields[r]; !ok {
			t.Errorf("%s does not read %s, which the schema requires", name, r)
		}
	}
}

func TestModelsMatchTheSchemas(t *testing.T) {
	s := loadDocument(t).Components.Schemas
	list, forgotten, run := s["MemoryList"], s["MemoryForgottenList"], s["Run"]
	if list == nil || forgotten == nil || run == nil {
		t.Fatal("the document has no MemoryList, MemoryForgottenList or Run")
	}
	for name, c := range map[string]struct {
		model  any
		schema *schema
	}{
		"Memory":               {memory.Memory{}, s["Memory"]},
		"List":                 {memory.List{}, list},
		"Settings":             {memory.Settings{}, s["MemorySettings"]},
		"Project":              {memory.Project{}, list.Properties["projects"].Items},
		"Limits":               {memory.Limits{}, list.Properties["limits"]},
		"Ref":                  {memory.Ref{}, s["MemoryRef"]},
		"AddMemoryParams":      {memory.AddMemoryParams{}, s["MemoryAdd"]},
		"UpdateMemoryParams":   {memory.UpdateMemoryParams{}, s["MemoryChange"]},
		"UpdateSettingsParams": {memory.UpdateSettingsParams{}, s["MemorySettingsChange"]},
		"SettingsChanged":      {memory.SettingsChanged{}, s["MemorySettingsChanged"]},
		"ForgottenList":        {memory.ForgottenList{}, forgotten},
		"Forgotten":            {memory.Forgotten{}, forgotten.Properties["forgotten"].Items},
		"ImportMemoryParams":   {memory.ImportMemoryParams{}, s["MemoryImport"]},
		"Imported":             {memory.Imported{}, s["MemoryImported"]},
		"RunList":              {memory.RunList{}, s["RunList"]},
		"Run":                  {memory.Run{}, run},
		"RunCounts":            {memory.RunCounts{}, run.Properties["counts"]},
		"RunChange":            {memory.RunChange{}, s["RunChange"]},
		"RunDecisionParams":    {memory.RunDecisionParams{}, s["RunDecision"]},
		"Undone":               {memory.Undone{}, merged(t, s, s["RunUndone"])},
	} {
		holdToSchema(t, name, c.model, c.schema)
	}
}

// The scopes, the types, the review modes and the kinds and states of a
// run and its changes the package names are the ones the document lists,
// wherever it lists them.
func TestTheValuesAreTheDocuments(t *testing.T) {
	s := loadDocument(t).Components.Schemas
	scopes := []string{string(memory.ScopeYou), string(memory.ScopeProject)}
	types := []string{string(memory.TypePreference), string(memory.TypeProfile), string(memory.TypeTopic), string(memory.TypeArea), string(memory.TypePerson)}
	reviews := []string{string(memory.ReviewApply), string(memory.ReviewAsk)}
	runStates := []string{string(memory.RunRunning), string(memory.RunApplied), string(memory.RunWaiting),
		string(memory.RunPartlyApplied), string(memory.RunDeclined), string(memory.RunUndone)}
	changeStates := []string{string(memory.ChangeApplied), string(memory.ChangeWaiting), string(memory.ChangeDeclined),
		string(memory.ChangeChangedSince), string(memory.ChangeUndone)}
	changeKinds := []string{string(memory.ChangeCreated), string(memory.ChangeChanged), string(memory.ChangeRemoved)}
	for _, c := range []struct {
		name     string
		declared []string
		listed   *schema
	}{
		{"Memory.scope", scopes, s["Memory"].Properties["scope"]},
		{"MemoryAdd.scope", scopes, s["MemoryAdd"].Properties["scope"]},
		{"MemoryChange.scope", scopes, s["MemoryChange"].Properties["scope"]},
		{"MemoryRef.scope", scopes, s["MemoryRef"].Properties["scope"]},
		{"MemoryImport.scope", scopes, s["MemoryImport"].Properties["scope"]},
		{"MemoryForgottenList.forgotten.scope", scopes, s["MemoryForgottenList"].Properties["forgotten"].Items.Properties["scope"]},
		{"Memory.type", types, s["Memory"].Properties["type"]},
		{"MemoryAdd.type", types, s["MemoryAdd"].Properties["type"]},
		{"MemoryChange.type", types, s["MemoryChange"].Properties["type"]},
		{"MemorySettings.review", reviews, s["MemorySettings"].Properties["review"]},
		{"MemorySettingsChange.review", reviews, s["MemorySettingsChange"].Properties["review"]},
		{"MemorySettingsChanged.review", reviews, s["MemorySettingsChanged"].Properties["review"]},
		{"Run.review", reviews, s["Run"].Properties["review"]},
		{"Run.scope", scopes, s["Run"].Properties["scope"]},
		{"Run.kind", []string{string(memory.RunMemory)}, s["Run"].Properties["kind"]},
		{"Run.state", runStates, s["Run"].Properties["state"]},
		{"RunChange.kind", changeKinds, s["RunChange"].Properties["kind"]},
		{"RunChange.state", changeStates, s["RunChange"].Properties["state"]},
		{"RunChange.held", []string{string(memory.HeldYours)}, s["RunChange"].Properties["held"]},
	} {
		if c.listed == nil || !slices.Equal(slices.Sorted(slices.Values(c.declared)), slices.Sorted(slices.Values(c.listed.Enum))) {
			t.Errorf("%s: the package declares %v; the document lists %+v", c.name, c.declared, c.listed)
		}
	}
}

// Each call the package makes is in the document, answered with the
// status and the schema the package reads, and a change or a delete of
// one memory takes If-Match.
func TestEndpointsAreInTheDocument(t *testing.T) {
	d := loadDocument(t)
	const one, project = "/v1/account/memory/you/{id}", "/v1/account/memory/projects/{project}/{id}"
	for _, e := range []struct {
		method, path, status, answer string
	}{
		{"get", "/v1/account/memory", "200", "MemoryList"},
		{"post", "/v1/account/memory", "201", "Memory"},
		{"delete", "/v1/account/memory", "204", ""},
		{"patch", "/v1/account/memory/settings", "200", "MemorySettingsChanged"},
		{"get", "/v1/account/memory/forgotten", "200", "MemoryForgottenList"},
		{"post", "/v1/account/memory/forgotten/restore", "200", "Memory"},
		{"get", "/v1/account/memory/export", "200", ""},
		{"post", "/v1/account/memory/import", "201", "MemoryImported"},
		{"get", one, "200", "Memory"},
		{"patch", one, "200", "Memory"},
		{"delete", one, "204", ""},
		{"get", project, "200", "Memory"},
		{"patch", project, "200", "Memory"},
		{"delete", project, "204", ""},
		{"get", "/v1/account/runs", "200", "RunList"},
		{"get", "/v1/account/runs/{run}", "200", "Run"},
		{"post", "/v1/account/runs/{run}/apply", "200", "Run"},
		{"post", "/v1/account/runs/{run}/decline", "200", "Run"},
		{"post", "/v1/account/runs/{run}/undo", "200", "RunUndone"},
	} {
		op := readOperation(t, d, e.method, e.path)
		answer, ok := op.Responses[e.status]
		if !ok {
			t.Errorf("%s %s answers no %s", strings.ToUpper(e.method), e.path, e.status)
			continue
		}
		if e.answer != "" {
			sch := answer.Content["application/json"].Schema
			if sch == nil || sch.Ref != "#/components/schemas/"+e.answer {
				t.Errorf("%s %s answers %+v; the package reads %s", strings.ToUpper(e.method), e.path, sch, e.answer)
			}
		}
		if e.method == "patch" && e.path != "/v1/account/memory/settings" || e.method == "delete" && e.path != "/v1/account/memory" {
			if !slices.ContainsFunc(op.Parameters, func(p struct {
				Ref string `json:"$ref"`
			}) bool {
				return p.Ref == "#/components/parameters/if_match"
			}) {
				t.Errorf("%s %s takes no If-Match", strings.ToUpper(e.method), e.path)
			}
		}
	}
	if _, ok := readOperation(t, d, "get", "/v1/account/memory/export").Responses["200"].Content["application/zip"]; !ok {
		t.Error("the export is not answered as application/zip")
	}
}

// Each code the package has a sentinel for is one the document describes
// on a memory refusal of the status the sentinel says, and the SDK
// answers it with the sentinel and with the sentinel of its status.
func TestTheCodesAreTheDocuments(t *testing.T) {
	responses := loadDocument(t).Components.Responses
	for _, c := range []struct {
		code     string
		status   int
		response string
		sentinel error
		status2  error
	}{
		{"memory_not_found", http.StatusNotFound, "memory_not_found", memory.ErrNotFound, latere.ErrNotFound},
		{"memory_changed", http.StatusPreconditionFailed, "memory_changed", memory.ErrChanged, latere.ErrPreconditionFailed},
		{"memory_taken", http.StatusConflict, "memory_conflict", memory.ErrTaken, latere.ErrConflict},
		{"memory_name_taken", http.StatusConflict, "memory_conflict", memory.ErrNameTaken, latere.ErrConflict},
		{"memory_too_large", http.StatusRequestEntityTooLarge, "memory_too_large", memory.ErrTooLarge, nil},
		{"invalid_field", http.StatusBadRequest, "memory_invalid", memory.ErrInvalidField, nil},
		{"memory_personal_only", http.StatusForbidden, "memory_forbidden", memory.ErrPersonalOnly, latere.ErrForbidden},
		{"memory_agent_token", http.StatusForbidden, "memory_forbidden", memory.ErrAgentToken, latere.ErrForbidden},
		{"memory_unavailable", http.StatusServiceUnavailable, "memory_unavailable", memory.ErrUnavailable, nil},
		{"storage_unavailable", http.StatusServiceUnavailable, "memory_unavailable", memory.ErrStorageUnavailable, nil},
		{"storage_unavailable", http.StatusServiceUnavailable, "runs_unavailable", memory.ErrStorageUnavailable, nil},
		{"run_not_found", http.StatusNotFound, "run_not_found", memory.ErrRunNotFound, latere.ErrNotFound},
		{"run_running", http.StatusConflict, "run_conflict", memory.ErrRunRunning, latere.ErrConflict},
		{"run_not_waiting", http.StatusConflict, "run_conflict", memory.ErrRunNotWaiting, latere.ErrConflict},
		{"run_busy", http.StatusConflict, "run_conflict", memory.ErrRunBusy, latere.ErrConflict},
		{"run_change_unknown", http.StatusBadRequest, "run_invalid", memory.ErrRunChangeUnknown, nil},
		{"runs_unavailable", http.StatusServiceUnavailable, "runs_unavailable", memory.ErrRunsUnavailable, nil},
	} {
		if !strings.Contains(responses[c.response].Description, c.code+":") {
			t.Errorf("the document's %s response does not describe %s: %q", c.response, c.code, responses[c.response].Description)
		}
		err := refusedWith(t, c.status, c.code)
		if !errors.Is(err, c.sentinel) || (c.status2 != nil && !errors.Is(err, c.status2)) {
			t.Errorf("%d %s: %v matches neither its sentinel nor its status's", c.status, c.code, err)
		}
	}
}
