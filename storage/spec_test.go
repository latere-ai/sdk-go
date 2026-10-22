// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package storage_test

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"latere.ai/x/sdk-go/storage"
)

// The models of this package are written by hand from the Storage API,
// whose OpenAPI document `make openapi` vendors in openapi/storage.json.
// The document names every route but gives the answers of the file and
// upload routes no schema, so File and Upload follow the API's reference
// pages; these tests hold the routes the client calls, and the one shared
// list shape the document does define, to the document.

type schema struct {
	Properties map[string]*schema `json:"properties"`
	Required   []string           `json:"required"`
}

type document struct {
	Paths      map[string]map[string]json.RawMessage `json:"paths"`
	Components struct {
		Schemas map[string]*schema `json:"schemas"`
	} `json:"components"`
}

func loadDocument(t *testing.T) *document {
	t.Helper()
	raw, err := os.ReadFile("../openapi/storage.json")
	if err != nil {
		t.Fatal(err)
	}
	var d document
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	return &d
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

// A listing is the API's one list shape, with the folders and the space's
// usage added for a folder; a page of shares is the shape alone.
func TestFilesPageReadsTheListShape(t *testing.T) {
	page := loadDocument(t).Components.Schemas["Page"]
	if page == nil {
		t.Fatal("the document has no Page schema")
	}
	for _, typ := range []reflect.Type{reflect.TypeFor[storage.FilesPage](), reflect.TypeFor[storage.GrantsPage]()} {
		fields := jsonFields(typ)
		for prop := range page.Properties {
			if !slices.Contains(fields, prop) {
				t.Errorf("%s does not read %s", typ.Name(), prop)
			}
		}
		for _, r := range page.Required {
			if !slices.Contains(fields, r) {
				t.Errorf("%s does not read %s, which the schema requires", typ.Name(), r)
			}
		}
	}
}

func TestEndpointsAreInTheDocument(t *testing.T) {
	d := loadDocument(t)
	for _, e := range []struct{ method, path string }{
		{"get", "/v1/storage/files/{owner}/{path}"},
		{"head", "/v1/storage/files/{owner}/{path}"},
		{"put", "/v1/storage/files/{owner}/{path}"},
		{"post", "/v1/storage/files/{owner}/{path}"},
		{"delete", "/v1/storage/files/{owner}/{path}"},
		{"get", "/v1/storage/shares/with-me"},
		{"post", "/v1/storage/uploads"},
		{"post", "/v1/storage/uploads/{id}/complete"},
		{"delete", "/v1/storage/uploads/{id}"},
	} {
		if _, ok := d.Paths[e.path][e.method]; !ok {
			t.Errorf("%s %s is not in the document", strings.ToUpper(e.method), e.path)
		}
	}
}
