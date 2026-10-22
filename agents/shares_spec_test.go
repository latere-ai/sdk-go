// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// routesOutsideTheDocuments are the routes the package calls that no
// published document describes, each with what its models follow
// instead. The read of a share is served beside the account part, on
// the API's own prefix, so the account document is checked too.
var routesOutsideTheDocuments = map[string]string{
	"get /v1/shares/{token}":                "the platform's read of a share, whose page SharedPage and Share follow",
	"get /v1/shares/{token}/blobs/{digest}": "the platform's read of a file a share names, whose bytes FileReader reads",
}

// TestRoutesOutsideTheDocumentsAreStillOutside fails once a vendored
// document describes one of routesOutsideTheDocuments, so its models are
// then held to the document like the rest of the package's.
func TestRoutesOutsideTheDocumentsAreStillOutside(t *testing.T) {
	for _, name := range []string{"agents", "account"} {
		raw, err := os.ReadFile("../openapi/" + name + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var d struct {
			Paths map[string]map[string]json.RawMessage `json:"paths"`
		}
		if err := json.Unmarshal(raw, &d); err != nil {
			t.Fatal(err)
		}
		for route := range routesOutsideTheDocuments {
			method, path, _ := strings.Cut(route, " ")
			if _, ok := d.Paths[path][method]; ok {
				t.Errorf("%s is now in the %s document: hold its models to it and take it off routesOutsideTheDocuments", route, name)
			}
		}
	}
}
