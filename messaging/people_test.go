// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package messaging_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"

	"latere.ai/x/sdk-go/latere"
	"latere.ai/x/sdk-go/messaging"
)

// FindPerson asks for the handle as given, as the person's own token and
// for no device, and reads the person the platform answers.
func TestFindPerson(t *testing.T) {
	c, got := fakeService(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) {
		answer(w, http.StatusOK, `{"id":"7c0e9b1a","handle":"ada","display_name":"Ada Lovelace"}`)
	})
	p, err := c.FindPerson(t.Context(), "Ada")
	if err != nil {
		t.Fatal(err)
	}
	if *p != (messaging.Person{ID: "7c0e9b1a", Handle: "ada", DisplayName: "Ada Lovelace"}) {
		t.Errorf("found %+v", p)
	}
	reqs := got()
	if len(reqs) != 1 {
		t.Fatalf("%d requests", len(reqs))
	}
	r := reqs[0]
	if r.method != http.MethodGet || r.path != "/v1/messaging/people" || r.query != "handle=Ada" || r.device != "" || r.auth != "Bearer person-token" || r.body != nil {
		t.Errorf("sent %+v", r)
	}
}

// A handle no one holds is ErrPersonNotFound and a 404; any other refusal
// is the platform's answer as it came.
func TestFindPersonRefused(t *testing.T) {
	for _, c := range []struct {
		status        int
		code          string
		notFound, any error
	}{
		{http.StatusNotFound, "person_not_found", messaging.ErrPersonNotFound, latere.ErrNotFound},
		{http.StatusNotFound, "not_found", nil, latere.ErrNotFound},
		{http.StatusForbidden, "personal_only", nil, latere.ErrForbidden},
		{http.StatusBadRequest, "handle_required", nil, nil},
	} {
		client, _ := fakeService(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) { refuse(w, c.status, c.code) })
		p, err := client.FindPerson(t.Context(), "ada")
		if p != nil || err == nil {
			t.Errorf("%s: found %+v, %v", c.code, p, err)
			continue
		}
		if got := errors.Is(err, messaging.ErrPersonNotFound); got != (c.notFound != nil) {
			t.Errorf("%s: matches ErrPersonNotFound: %v", c.code, got)
		}
		if c.any != nil && !errors.Is(err, c.any) {
			t.Errorf("%s: %v does not match %v", c.code, err, c.any)
		}
		var apiErr *latere.Error
		if !errors.As(err, &apiErr) || apiErr.Code != c.code {
			t.Errorf("%s: the platform's answer is not in %v", c.code, err)
		}
	}
}

// Person asks for the id as given, as the person's own token and for no
// device, and reads the person the platform answers, with an empty handle
// for one who has not chosen it.
func TestPerson(t *testing.T) {
	for _, c := range []struct {
		body string
		want messaging.Person
	}{
		{`{"id":"7c0e9b1a-4b7e-4b8e-9d55-0a1f6c2b3d4e","handle":"ada","display_name":"Ada Lovelace"}`, messaging.Person{ID: "7c0e9b1a-4b7e-4b8e-9d55-0a1f6c2b3d4e", Handle: "ada", DisplayName: "Ada Lovelace"}},
		{`{"id":"2b3c4d5e-6f70-4182-9394-a5b6c7d8e9f0","handle":"","display_name":"Alan Turing"}`, messaging.Person{ID: "2b3c4d5e-6f70-4182-9394-a5b6c7d8e9f0", DisplayName: "Alan Turing"}},
	} {
		client, got := fakeService(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) {
			answer(w, http.StatusOK, c.body)
		})
		p, err := client.Person(t.Context(), c.want.ID)
		if err != nil {
			t.Fatal(err)
		}
		if *p != c.want {
			t.Errorf("answered %+v, want %+v", p, c.want)
		}
		reqs := got()
		if len(reqs) != 1 {
			t.Fatalf("%d requests", len(reqs))
		}
		r := reqs[0]
		if r.method != http.MethodGet || r.path != "/v1/messaging/people" || r.query != "id="+c.want.ID || r.device != "" || r.auth != "Bearer person-token" || r.body != nil {
			t.Errorf("sent %+v", r)
		}
	}
}

// An id that is not a person's is ErrPersonNotFound and a 404; any other
// refusal is the platform's answer as it came.
func TestPersonRefused(t *testing.T) {
	for _, c := range []struct {
		status        int
		code          string
		notFound, any error
	}{
		{http.StatusNotFound, "person_not_found", messaging.ErrPersonNotFound, latere.ErrNotFound},
		{http.StatusBadRequest, "person_query_invalid", nil, nil},
		{http.StatusForbidden, "person_required", nil, latere.ErrForbidden},
	} {
		client, _ := fakeService(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) { refuse(w, c.status, c.code) })
		p, err := client.Person(t.Context(), "7c0e9b1a-4b7e-4b8e-9d55-0a1f6c2b3d4e")
		if p != nil || err == nil {
			t.Errorf("%s: answered %+v, %v", c.code, p, err)
			continue
		}
		if got := errors.Is(err, messaging.ErrPersonNotFound); got != (c.notFound != nil) {
			t.Errorf("%s: matches ErrPersonNotFound: %v", c.code, got)
		}
		if c.any != nil && !errors.Is(err, c.any) {
			t.Errorf("%s: %v does not match %v", c.code, err, c.any)
		}
		var apiErr *latere.Error
		if !errors.As(err, &apiErr) || apiErr.Code != c.code {
			t.Errorf("%s: the platform's answer is not in %v", c.code, err)
		}
	}
}

// The lookup takes the two query parameters FindPerson and Person send,
// handle and id, neither required since a request sends one, and an id
// is a UUID.
func TestPersonQueriesAreTheDocuments(t *testing.T) {
	raw, err := os.ReadFile("../openapi/messaging.json")
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Paths map[string]map[string]struct {
			Parameters []struct {
				Name     string `json:"name"`
				In       string `json:"in"`
				Required bool   `json:"required"`
				Schema   struct {
					Format string `json:"format"`
				} `json:"schema"`
			} `json:"parameters"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range d.Paths["/v1/messaging/people"]["get"].Parameters {
		got = append(got, p.In+" "+p.Name)
		if p.Required {
			t.Errorf("the document requires %s, and FindPerson and Person send one of handle and id", p.Name)
		}
		if p.Name == "id" && p.Schema.Format != "uuid" {
			t.Errorf("the document's id is of format %q", p.Schema.Format)
		}
	}
	if want := []string{"query handle", "query id"}; !slices.Equal(got, want) {
		t.Errorf("the document lists %v; FindPerson and Person send %v", got, want)
	}
}

// The lookup's refusal of a handle no one holds is one the document
// describes under its 404.
func TestPersonNotFoundIsTheDocuments(t *testing.T) {
	raw, err := os.ReadFile("../openapi/messaging.json")
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Paths map[string]map[string]struct {
			Responses map[string]struct {
				Ref string `json:"$ref"`
			} `json:"responses"`
		} `json:"paths"`
		Components struct {
			Responses map[string]struct {
				Description string `json:"description"`
			} `json:"responses"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	ref := d.Paths["/v1/messaging/people"]["get"].Responses["404"].Ref
	if ref == "" {
		t.Fatal("GET /v1/messaging/people answers no 404 the document names")
	}
	if described := d.Components.Responses[strings.TrimPrefix(ref, "#/components/responses/")].Description; !strings.Contains(described, "person_not_found") {
		t.Errorf("the 404 the document describes is %q, not person_not_found", described)
	}
}
