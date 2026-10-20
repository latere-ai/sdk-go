// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package apps_test

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"latere.ai/x/sdk-go/apps"
)

// The models of this package are written by hand from the apps API's
// OpenAPI document, vendored in openapi/apps.json, and, for who may open
// an app, from the account API's, openapi/account.json, both by
// `make openapi`. These tests hold the two together: every field a model
// reads is in the schema, every field the schema defines is modeled or
// left out with a reason, date-times are read as time.Time, and every
// endpoint the client calls is in the document.

type schema struct {
	Format     string             `json:"format"`
	Properties map[string]*schema `json:"properties"`
	Required   []string           `json:"required"`
	Items      *schema            `json:"items"`
	OneOf      []*schema          `json:"oneOf"`
}

type document struct {
	Paths      map[string]map[string]json.RawMessage `json:"paths"`
	Components struct {
		Schemas map[string]*schema `json:"schemas"`
	} `json:"components"`
}

func loadDocument(t *testing.T, name string) *document {
	t.Helper()
	raw, err := os.ReadFile("../openapi/" + name + ".json")
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
			return t.Field(i).Type
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

// requestSchema is the schema of the JSON body an operation takes, which
// a document may write in place rather than under components.
func requestSchema(t *testing.T, d *document, method, path string) *schema {
	t.Helper()
	var op struct {
		RequestBody struct {
			Content map[string]struct {
				Schema *schema `json:"schema"`
			} `json:"content"`
		} `json:"requestBody"`
	}
	if err := json.Unmarshal(d.Paths[path][method], &op); err != nil {
		t.Fatal(err)
	}
	return op.RequestBody.Content["application/json"].Schema
}

// object is the object a nullable schema allows besides null.
func object(s *schema) *schema {
	for _, alt := range s.OneOf {
		if alt.Properties != nil {
			return alt
		}
	}
	return s
}

// leftOut lists the fields a schema defines that a model leaves out on
// purpose, each with its reason.
var leftOut = map[string]string{
	"App.cluster":        "reserved; the API answers it with a constant",
	"App.env_pending":    "reserved; the API answers it false",
	"App.pending_deploy": "reserved; the API answers it false",
}

func TestModelsMatchTheSchemas(t *testing.T) {
	a := loadDocument(t, "apps").Components.Schemas
	accountDoc := loadDocument(t, "account")
	acc := accountDoc.Components.Schemas
	access := acc["AppAccess"]
	cases := map[string]struct {
		model  any
		schema *schema
	}{
		"App":                {apps.App{}, a["App"]},
		"Owner":              {apps.Owner{}, a["Owner"]},
		"SessionRef":         {apps.SessionRef{}, a["SessionRef"]},
		"Thumbnail":          {apps.Thumbnail{}, a["Thumbnail"]},
		"Settings":           {apps.Settings{}, a["Settings"]},
		"PreviewSettings":    {apps.PreviewSettings{}, a["PreviewSettings"]},
		"Repository":         {apps.Repository{}, a["Repository"]},
		"CreateAppParams":    {apps.CreateAppParams{}, a["AppCreate"]},
		"UpdateAppParams":    {apps.UpdateAppParams{}, a["AppUpdate"]},
		"SettingsUpdate":     {apps.SettingsUpdate{}, a["SettingsUpdate"]},
		"Deploy":             {apps.Deploy{}, a["Deploy"]},
		"DeploySummary":      {apps.DeploySummary{}, a["DeploySummary"]},
		"Component":          {apps.Component{}, a["Component"]},
		"DeployError":        {apps.DeployError{}, a["DeployError"]},
		"LogLine":            {apps.LogLine{}, a["LogLine"]},
		"LogEnd":             {apps.LogEnd{}, a["LogEnd"]},
		"BuildError":         {apps.BuildError{}, a["LogEnd"].Properties["error"]},
		"Release":            {apps.Release{}, a["Release"]},
		"ReleaseDetail":      {apps.ReleaseDetail{}, a["ReleaseDetail"]},
		"Access":             {apps.Access{}, access},
		"AccessOrganization": {apps.AccessOrganization{}, object(access.Properties["organization"])},
		"Password":           {apps.Password{}, object(access.Properties["password"])},
		"AccessLimits":       {apps.AccessLimits{}, access.Properties["limits"]},
		"Person":             {apps.Person{}, acc["AppAccessPerson"]},
		"PersonAccount":      {apps.PersonAccount{}, acc["AppAccessPerson"].Properties["person"]},
		"Invites":            {apps.Invites{}, acc["AppInvites"]},
		"InviteParams":       {apps.InviteParams{}, requestSchema(t, accountDoc, "post", "/v1/account/apps/{app_id}/access/invites")},
		"SkippedInvite":      {apps.SkippedInvite{}, acc["AppInvites"].Properties["skipped"].Items},
	}
	for name, c := range cases {
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
		for prop := range c.schema.Properties {
			if !slices.Contains(fields, prop) && leftOut[name+"."+prop] == "" {
				t.Errorf("%s does not read %s; model it or add it to leftOut with a reason", name, prop)
			}
		}
	}
}

// The API's origin serves the apps document's routes under /v1/apps: its
// /v1/apps/{slug} is /v1/apps/apps/{slug} at api.latere.ai.
func TestEndpointsAreInTheDocuments(t *testing.T) {
	appsDoc, accountDoc := loadDocument(t, "apps"), loadDocument(t, "account")
	for _, e := range []struct{ method, path string }{
		{"post", "/v1/apps/apps"},
		{"get", "/v1/apps/apps"},
		{"get", "/v1/apps/apps/{slug}"},
		{"patch", "/v1/apps/apps/{slug}"},
		{"delete", "/v1/apps/apps/{slug}"},
		{"get", "/v1/apps/apps/{slug}/deploys"},
		{"get", "/v1/apps/apps/{slug}/deploys/{id}"},
		{"get", "/v1/apps/apps/{slug}/deploys/{id}/logs"},
		{"get", "/v1/apps/apps/{slug}/releases"},
		{"get", "/v1/apps/apps/{slug}/releases/{tag}"},
	} {
		docPath := "/v1" + strings.TrimPrefix(e.path, "/v1/apps")
		if _, ok := appsDoc.Paths[docPath][e.method]; !ok {
			t.Errorf("%s %s (%s in the document) is not in the apps document", strings.ToUpper(e.method), e.path, docPath)
		}
	}
	for _, e := range []struct{ method, path string }{
		{"get", "/v1/account/apps/{app_id}/access"},
		{"post", "/v1/account/apps/{app_id}/access/invites"},
		{"delete", "/v1/account/apps/{app_id}/access/{access_id}"},
		{"put", "/v1/account/apps/{app_id}/access/password"},
		{"delete", "/v1/account/apps/{app_id}/access/password"},
	} {
		if _, ok := accountDoc.Paths[e.path][e.method]; !ok {
			t.Errorf("%s %s is not in the account document", strings.ToUpper(e.method), e.path)
		}
	}
}
