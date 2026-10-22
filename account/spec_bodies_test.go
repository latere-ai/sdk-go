// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package account_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"latere.ai/x/sdk-go/account"
)

// requestSchema returns the schema of the JSON body a route's method
// takes, with its reference followed.
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
		t.Fatalf("%s %s: %v", strings.ToUpper(method), path, err)
	}
	for _, ct := range []string{"application/json", "application/merge-patch+json"} {
		if c, ok := op.RequestBody.Content[ct]; ok {
			return resolve(d, c.Schema)
		}
	}
	t.Fatalf("%s %s takes no JSON body", strings.ToUpper(method), path)
	return nil
}

// resolve follows a schema's reference to the components.
func resolve(d *document, s *schema) *schema {
	if s != nil && s.Ref != "" {
		return d.Components.Schemas[strings.TrimPrefix(s.Ref, "#/components/schemas/")]
	}
	return s
}

// fits reports, as test errors, each member of body its schema does not
// define, at any depth, and each member the schema requires that body
// leaves out.
func fits(t *testing.T, d *document, where string, body map[string]any, s *schema) {
	t.Helper()
	for name, v := range body {
		prop, ok := s.Properties[name]
		if !ok {
			t.Errorf("%s sends %s, which its schema does not define", where, name)
			continue
		}
		if nested, isObject := v.(map[string]any); isObject {
			fits(t, d, where+"."+name, nested, resolve(d, prop))
		}
	}
	for _, r := range s.Required {
		if _, ok := body[r]; !ok {
			t.Errorf("%s leaves out %s, which its schema requires", where, r)
		}
	}
}

// Each body the client sends, with every member it can set, is the one
// its route takes.
func TestTheBodiesFitTheirSchemas(t *testing.T) {
	d := loadDocument(t)
	c, calls := capturingAPI(t, 0, func(w http.ResponseWriter, r *http.Request, n int) {
		switch {
		case r.URL.Path == "/v1/account/arrival":
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/v1/account/wallet/checkout":
			answerJSON(w, http.StatusOK, `{"url":"https://pay.example/c"}`)
		case r.URL.Path == "/v1/account/preferences":
			answerJSON(w, http.StatusOK, preferences)
		default:
			answerJSON(w, http.StatusOK, share)
		}
	})
	mode, text, on := "ask", "Be brief.", true
	sends := []struct {
		method, path string
		send         func() error
	}{
		{"post", "/v1/account/shares", func() error { return second(c.CreateShare(t.Context(), "ses_1")) }},
		{"patch", "/v1/account/shares/{share_id}", func() error { return second(c.UpdateShare(t.Context(), "shr_1")) }},
		{"post", "/v1/account/wallet/checkout", func() error {
			return second(c.StartCheckout(t.Context(), &account.StartCheckoutParams{AmountMicroUSD: 1, SuccessURL: "https://a/", CancelURL: "https://a/"}))
		}},
		{"put", "/v1/account/arrival", func() error {
			return c.RecordArrival(t.Context(), &account.RecordArrivalParams{SourceHost: "example.com", LandingPath: "/"})
		}},
		{"patch", "/v1/account/preferences", func() error {
			return second(c.UpdatePreferences(t.Context(), &account.UpdatePreferencesParams{
				NetworkMode: &mode, SharingMode: &mode, CallMe: &text, Work: &text, Instructions: &text, ZeroRetention: &on,
			}))
		}},
	}
	for i, s := range sends {
		if err := s.send(); err != nil {
			t.Fatalf("%s %s: %v", s.method, s.path, err)
		}
		fits(t, d, strings.ToUpper(s.method)+" "+s.path, calls()[i].Body, requestSchema(t, d, s.method, s.path))
	}
}

// The sections UpdatePreferences writes are those the patch's schema
// defines, so a member the API adds to the patch is decided on here.
func TestUpdatePreferencesSetsEveryMemberOfThePatch(t *testing.T) {
	d := loadDocument(t)
	patch := d.Components.Schemas["PreferencesPatch"]
	var members []string
	for section, s := range patch.Properties {
		for member := range s.Properties {
			members = append(members, section+"."+member)
		}
	}
	slices.Sort(members)
	want := []string{"data.zero_retention", "instructions.text", "network.mode", "profile.call_me", "profile.work", "sharing.mode"}
	if !slices.Equal(members, want) {
		t.Errorf("the patch's members are %v; UpdatePreferencesParams sets %v", members, want)
	}
}
