// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package signin

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// recordsService is the sign-in service's own records as auth serves them:
// GET /me/orgs and GET /handles, each for the bearer of the sign-in's
// access token alone, refusing in the {"error", "message"} envelope.
type recordsService struct {
	srv *httptest.Server
	// asked counts the requests that reached the service; offsite counts
	// those that reached the site a redirect names.
	asked, offsite atomic.Int32
}

func newRecords(t *testing.T) *recordsService {
	r := &recordsService{}
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.offsite.Add(1)
		writeJSON(w, map[string]string{"handle": "taken"})
	}))
	t.Cleanup(elsewhere.Close)
	refuse := func(w http.ResponseWriter, status int, code, message string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprintf(w, `{"error":%q,"message":%q}`, code, message)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /me/orgs", func(w http.ResponseWriter, req *http.Request) {
		r.asked.Add(1)
		switch req.Header.Get("Authorization") {
		case "Bearer login_1":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `[{"id":"org_1","name":"Acme","slug":"acme","owner_id":"u_1","owner":true,"joined_at":"2026-01-02 15:04:05.123456+00","managed_by":null,"seats":3},`+
				`{"id":"org_2","name":"Beta","slug":"beta","owner_id":"u_9","joined_at":"2026-03-04 05:06:07+05:30","managed_by":"okta"},`+
				`{"id":"org_3","name":"Gamma","slug":"gamma","owner_id":"u_9","joined_at":"yesterday","managed_by":null}]`)
		case "Bearer alone":
			fmt.Fprint(w, `[]`)
		case "Bearer null":
			fmt.Fprint(w, `null`)
		case "Bearer garbled":
			fmt.Fprint(w, `[{"id":`)
		default:
			refuse(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		}
	})
	mux.HandleFunc("GET /handles", func(w http.ResponseWriter, req *http.Request) {
		r.asked.Add(1)
		if req.Header.Get("Authorization") != "Bearer login_1" || req.Header.Get("Accept") != "application/json" {
			refuse(w, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		switch id := req.URL.Query().Get("principal_id"); id {
		case "0b6f1c1e-0000-4000-8000-000000000001":
			writeJSON(w, map[string]string{"principal_id": id, "handle": "ada"})
		case "0b6f1c1e-0000-4000-8000-000000000002":
			writeJSON(w, map[string]string{"principal_id": id, "handle": ""})
		case "0b6f1c1e-0000-4000-8000-000000000003":
			refuse(w, http.StatusNotFound, "not_found", "principal not found")
		case "moved":
			http.Redirect(w, req, elsewhere.URL+"/handles", http.StatusFound)
		default:
			refuse(w, http.StatusBadRequest, "bad_request", "principal_id must be a UUID")
		}
	})
	r.srv = httptest.NewServer(mux)
	t.Cleanup(r.srv.Close)
	return r
}

// config is a Config of the service with no ClientID, which these reads
// do not need.
func (r *recordsService) config() *Config { return &Config{Issuer: r.srv.URL + "/"} }

func TestListOrganizations(t *testing.T) {
	r := newRecords(t)
	orgs, err := r.config().ListOrganizations(t.Context(), "login_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(orgs) != 3 {
		t.Fatalf("ListOrganizations returned %d organizations, want 3", len(orgs))
	}
	acme, beta, gamma := orgs[0], orgs[1], orgs[2]
	if acme.ID != "org_1" || acme.Name != "Acme" || acme.Slug != "acme" || acme.OwnerID != "u_1" || !acme.Owner || acme.ManagedBy != "" {
		t.Errorf("the first organization is %+v", acme)
	}
	if want := time.Date(2026, 1, 2, 15, 4, 5, 123456000, time.UTC); !acme.JoinedAt.Equal(want) {
		t.Errorf("JoinedAt = %v, want %v", acme.JoinedAt, want)
	}
	if !strings.Contains(string(acme.RawJSON()), `"seats":3`) {
		t.Errorf("RawJSON lost a field: %s", acme.RawJSON())
	}
	if beta.Owner || beta.ManagedBy != "okta" {
		t.Errorf("the second organization is %+v", beta)
	}
	if want := time.Date(2026, 3, 4, 5, 6, 7, 0, time.FixedZone("", 5*3600+30*60)); !beta.JoinedAt.Equal(want) {
		t.Errorf("JoinedAt with an offset of minutes = %v, want %v", beta.JoinedAt, want)
	}
	if !gamma.JoinedAt.IsZero() || !strings.Contains(string(gamma.RawJSON()), `"joined_at":"yesterday"`) {
		t.Errorf("a time in no known form read as %v, with RawJSON %s", gamma.JoinedAt, gamma.RawJSON())
	}
	for _, token := range []string{"alone", "null"} {
		if orgs, err := r.config().ListOrganizations(t.Context(), token); err != nil || orgs == nil || len(orgs) != 0 {
			t.Errorf("%s: ListOrganizations = %v, %v; want an empty slice", token, orgs, err)
		}
	}
	if _, err := r.config().ListOrganizations(t.Context(), "garbled"); err == nil {
		t.Error("a garbled answer decoded")
	}
	var oe *Error
	if _, err := r.config().ListOrganizations(t.Context(), "api_token"); !errors.As(err, &oe) || oe.StatusCode != 401 || oe.Code != "unauthorized" || oe.Description != "authentication required" {
		t.Errorf("a refused token: %v", err)
	}
	before := r.asked.Load()
	if _, err := r.config().ListOrganizations(t.Context(), ""); err == nil {
		t.Error("an empty token was accepted")
	}
	if r.asked.Load() != before {
		t.Error("a read with an empty token was sent")
	}
}

func TestGetHandle(t *testing.T) {
	r := newRecords(t)
	c := r.config()
	if h, err := c.GetHandle(t.Context(), "login_1", "0b6f1c1e-0000-4000-8000-000000000001"); err != nil || h != "ada" {
		t.Errorf("GetHandle = %q, %v; want ada", h, err)
	}
	if h, err := c.GetHandle(t.Context(), "login_1", "0b6f1c1e-0000-4000-8000-000000000002"); err != nil || h != "" {
		t.Errorf("a person without a handle: %q, %v", h, err)
	}
	_, err := c.GetHandle(t.Context(), "login_1", "0b6f1c1e-0000-4000-8000-000000000003")
	var oe *Error
	if !errors.Is(err, ErrNoPerson) || !errors.As(err, &oe) || oe.StatusCode != 404 || oe.Code != "not_found" {
		t.Errorf("an id of no one: %v", err)
	}
	if _, err := c.GetHandle(t.Context(), "login_1", "not a uuid"); errors.Is(err, ErrNoPerson) || !errors.As(err, &oe) || oe.StatusCode != 400 || oe.Description != "principal_id must be a UUID" {
		t.Errorf("an id that is no UUID: %v", err)
	}
	if _, err := c.GetHandle(t.Context(), "nope", "0b6f1c1e-0000-4000-8000-000000000001"); !errors.As(err, &oe) || oe.StatusCode != 401 {
		t.Errorf("a refused token: %v", err)
	}
	// A redirect is answered, and the token never reaches the site it
	// names.
	if _, err := c.GetHandle(t.Context(), "login_1", "moved"); !errors.As(err, &oe) || oe.StatusCode != http.StatusFound {
		t.Errorf("a redirect: %v", err)
	}
	if r.offsite.Load() != 0 {
		t.Error("the read followed a redirect")
	}
	before := r.asked.Load()
	if _, err := c.GetHandle(t.Context(), "login_1", ""); err == nil {
		t.Error("an empty id was accepted")
	}
	if r.asked.Load() != before {
		t.Error("a read of an empty id was sent")
	}
	// The caller's client keeps following redirects for its own calls.
	hc := &http.Client{}
	c.HTTPClient = hc
	if _, err := c.GetHandle(t.Context(), "login_1", "moved"); !errors.As(err, &oe) || oe.StatusCode != http.StatusFound {
		t.Errorf("a redirect with the caller's client: %v", err)
	}
	if hc.CheckRedirect != nil {
		t.Error("the caller's client was changed")
	}
}

func TestRecordsUnreachable(t *testing.T) {
	c := &Config{Issuer: "http://127.0.0.1:1"}
	if _, err := c.ListOrganizations(t.Context(), "login_1"); err == nil || !strings.HasPrefix(err.Error(), "signin: GET /me/orgs: ") {
		t.Errorf("an unreachable service: %v", err)
	}
	if _, err := (&Config{Issuer: "http://bad host"}).GetHandle(t.Context(), "login_1", "x"); err == nil {
		t.Error("an issuer that is no address answered")
	}
}

// The OAuth envelope keeps its description over a message beside it.
func TestErrorEnvelopes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"invalid_grant","error_description":"spent","message":"other"}`)
	}))
	defer srv.Close()
	_, err := (&Config{Issuer: srv.URL}).ListOrganizations(t.Context(), "t")
	var oe *Error
	if !errors.As(err, &oe) || oe.Code != "invalid_grant" || oe.Description != "spent" || !errors.Is(err, ErrInvalidGrant) {
		t.Errorf("the OAuth envelope read as %v", err)
	}
}
