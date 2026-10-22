// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package signin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// keysService is the sign-in service's /me/keys as auth serves it: for the
// bearer of the sign-in's access token alone, refusing in the
// {"error", "message"} envelope.
type keysService struct {
	srv *httptest.Server

	mu sync.Mutex
	// bodies are the create requests' bodies, decoded; revoked the ids a
	// revoke named, as the path carried them.
	bodies  []map[string]any
	revoked []string
	// lists are the list requests' queries, as they were sent.
	lists []string
	// asked counts the requests that reached the service; offsite those
	// that reached the site a redirect names.
	asked, offsite atomic.Int32
}

func newKeys(t *testing.T) *keysService {
	k := &keysService{}
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		k.offsite.Add(1)
		writeJSON(w, map[string]string{"id": "key_stolen", "key": "pat_stolen"})
	}))
	t.Cleanup(elsewhere.Close)
	refuse := func(w http.ResponseWriter, status int, code, message string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprintf(w, `{"error":%q,"message":%q,"detail":"for a developer"}`, code, message)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /me/keys", func(w http.ResponseWriter, r *http.Request) {
		k.asked.Add(1)
		if r.Header.Get("Authorization") != "Bearer login_1" {
			refuse(w, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		if r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Accept") != "application/json" {
			refuse(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
			return
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			refuse(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
			return
		}
		k.mu.Lock()
		k.bodies = append(k.bodies, body)
		k.mu.Unlock()
		switch body["name"] {
		case "the twenty-first":
			refuse(w, http.StatusConflict, "key_limit", "You already hold the maximum number of personal access tokens.")
		case "no value":
			writeJSON(w, map[string]any{"id": "key_9", "prefix": "pat_9"})
		case "garbled":
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"id":5,"key":"pat_x"}`)
		case "moved":
			http.Redirect(w, r, elsewhere.URL+"/me/keys", http.StatusTemporaryRedirect)
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			answer := map[string]any{
				"id": "key_1", "name": body["name"], "prefix": "pat_ab12", "key": "pat_ab12secret",
				"created_at": "2026-10-22T13:00:00Z", "expires_at": body["expires_at"],
				"grants": body["grants"], "org_id": body["org_id"], "status": "active",
				"client_id": "pat",
			}
			if body["org_id"] != nil {
				answer["status"] = "pending_approval"
			}
			if err := json.NewEncoder(w).Encode(answer); err != nil {
				t.Error(err)
			}
		}
	})
	mux.HandleFunc("GET /me/keys", func(w http.ResponseWriter, r *http.Request) {
		k.asked.Add(1)
		k.mu.Lock()
		k.lists = append(k.lists, r.URL.RawQuery)
		k.mu.Unlock()
		switch r.Header.Get("Authorization") {
		case "Bearer login_1":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"keys":[{"id":"key_2","name":"new","prefix":"pat_cd34","created_at":"2026-10-22T13:00:00Z","last_used_at":"2026-10-22T14:00:00Z",`+
				`"expires_at":null,"revoked_at":null,"grants":[],"org_id":"org_1","approved_at":null,"revoked_reason":null,"status":"pending_approval"},`+
				`{"id":"key_1","name":"old","prefix":"pat_ab12","created_at":"2026-01-02T03:04:05Z","last_used_at":null,`+
				`"expires_at":"2026-02-02T03:04:05Z","revoked_at":"2026-01-03T03:04:05Z","grants":[],"org_id":null,"revoked_reason":"revoked","status":"revoked"}]}`)
		case "Bearer alone":
			fmt.Fprint(w, `{"keys":null}`)
		default:
			refuse(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		}
	})
	mux.HandleFunc("DELETE /me/keys/{kid}", func(w http.ResponseWriter, r *http.Request) {
		k.asked.Add(1)
		if r.Header.Get("Authorization") != "Bearer login_1" {
			refuse(w, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		k.mu.Lock()
		k.revoked = append(k.revoked, r.PathValue("kid"))
		k.mu.Unlock()
		switch r.PathValue("kid") {
		case "key_gone":
			refuse(w, http.StatusNotFound, "not_found", "key not found")
		case "key_moved":
			http.Redirect(w, r, elsewhere.URL+"/me/keys/key_moved", http.StatusTemporaryRedirect)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	k.srv = httptest.NewServer(mux)
	t.Cleanup(k.srv.Close)
	return k
}

// config is a Config of the service with no ClientID, which the key calls
// do not need.
func (k *keysService) config() *Config { return &Config{Issuer: k.srv.URL + "/"} }

var testGrants = json.RawMessage(`[{"type":"latere-authz","actions":["lux:model.use"],"datatypes":["Model"],"locations":["https://api.latere.ai"]}]`)

func TestCreateKey(t *testing.T) {
	k := newKeys(t)
	expires := time.Date(2027, 1, 20, 13, 0, 0, 500_000_000, time.FixedZone("CET", 3600))
	key, err := k.config().CreateKey(t.Context(), "login_1", &CreateKeyParams{Name: "cli on laptop", Grants: testGrants, ExpiresAt: expires})
	if err != nil {
		t.Fatal(err)
	}
	if key.ID != "key_1" || key.Name != "cli on laptop" || key.Prefix != "pat_ab12" || key.Value != "pat_ab12secret" ||
		key.Status != KeyActive || key.OrgID != "" {
		t.Errorf("the created key is %+v", key)
	}
	if !key.ExpiresAt.Equal(expires) || !key.CreatedAt.Equal(time.Date(2026, 10, 22, 13, 0, 0, 0, time.UTC)) {
		t.Errorf("the key's times are %v and %v", key.CreatedAt, key.ExpiresAt)
	}
	var grants, sent any
	if err := json.Unmarshal(key.Grants, &grants); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(testGrants, &sent); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(grants) != fmt.Sprint(sent) {
		t.Errorf("the key's grants are %s", key.Grants)
	}
	if !strings.Contains(string(key.RawJSON()), `"client_id":"pat"`) {
		t.Errorf("RawJSON lost a field: %s", key.RawJSON())
	}
	body := k.bodies[0]
	if body["name"] != "cli on laptop" || body["expires_at"] != "2027-01-20T12:00:00.5Z" {
		t.Errorf("the request was %v", body)
	}
	if _, ok := body["org_id"]; ok {
		t.Errorf("a personal key sent an org_id: %v", body)
	}

	member, err := k.config().CreateKey(t.Context(), "login_1", &CreateKeyParams{Name: "member", Grants: testGrants, OrgID: "org_1"})
	if err != nil {
		t.Fatal(err)
	}
	if member.OrgID != "org_1" || member.Status != KeyPendingApproval || !member.ExpiresAt.IsZero() {
		t.Errorf("the member key is %+v", member)
	}
	if body := k.bodies[1]; body["org_id"] != "org_1" || body["expires_at"] != nil {
		t.Errorf("a member key without an expiry sent %v", body)
	}
}

func TestCreateKeyRefusals(t *testing.T) {
	k := newKeys(t)
	c := k.config()
	var oe *Error
	_, err := c.CreateKey(t.Context(), "login_1", &CreateKeyParams{Name: "the twenty-first", Grants: testGrants})
	if !errors.As(err, &oe) || oe.StatusCode != http.StatusConflict || oe.Code != "key_limit" || !strings.HasPrefix(oe.Description, "You already hold") || oe.Detail != "for a developer" {
		t.Errorf("a key over the limit: %v", err)
	}
	if _, err := c.CreateKey(t.Context(), "api_token", &CreateKeyParams{Name: "x", Grants: testGrants}); !errors.As(err, &oe) || oe.StatusCode != http.StatusUnauthorized {
		t.Errorf("a refused token: %v", err)
	}
	if _, err := c.CreateKey(t.Context(), "login_1", &CreateKeyParams{Name: "no value", Grants: testGrants}); err == nil || errors.As(err, &oe) {
		t.Errorf("an answer without the key's value: %v", err)
	}
	if _, err := c.CreateKey(t.Context(), "login_1", &CreateKeyParams{Name: "garbled", Grants: testGrants}); err == nil || errors.As(err, &oe) {
		t.Errorf("a garbled answer: %v", err)
	}
	// A redirect is answered, and the token never reaches the site it
	// names.
	if _, err := c.CreateKey(t.Context(), "login_1", &CreateKeyParams{Name: "moved", Grants: testGrants}); !errors.As(err, &oe) || oe.StatusCode != http.StatusTemporaryRedirect {
		t.Errorf("a redirect: %v", err)
	}
	if k.offsite.Load() != 0 {
		t.Error("the create followed a redirect")
	}
	before := k.asked.Load()
	for name, params := range map[string]*CreateKeyParams{
		"no params": nil,
		"no name":   {Name: " ", Grants: testGrants},
		"no grants": {Name: "x"},
	} {
		if _, err := c.CreateKey(t.Context(), "login_1", params); err == nil {
			t.Errorf("%s: the key was created", name)
		}
	}
	if _, err := c.CreateKey(t.Context(), "", &CreateKeyParams{Name: "x", Grants: testGrants}); err == nil {
		t.Error("an empty token was accepted")
	}
	if k.asked.Load() != before {
		t.Error("a create that could not succeed was sent")
	}
}

func TestRevokeKey(t *testing.T) {
	k := newKeys(t)
	c := k.config()
	if err := c.RevokeKey(t.Context(), "login_1", "key_1"); err != nil {
		t.Errorf("RevokeKey: %v", err)
	}
	if err := c.RevokeKey(t.Context(), "login_1", "a/b c"); err != nil {
		t.Errorf("RevokeKey of an id that needs escaping: %v", err)
	}
	if strings.Join(k.revoked, ",") != "key_1,a/b c" {
		t.Errorf("the revokes named %q", k.revoked)
	}
	var oe *Error
	err := c.RevokeKey(t.Context(), "login_1", "key_gone")
	if !errors.Is(err, ErrNoKey) || !errors.As(err, &oe) || oe.StatusCode != http.StatusNotFound || oe.Code != "not_found" {
		t.Errorf("a key the person does not hold: %v", err)
	}
	if err := c.RevokeKey(t.Context(), "api_token", "key_1"); errors.Is(err, ErrNoKey) || !errors.As(err, &oe) || oe.StatusCode != http.StatusUnauthorized {
		t.Errorf("a refused token: %v", err)
	}
	if err := c.RevokeKey(t.Context(), "login_1", "key_moved"); !errors.As(err, &oe) || oe.StatusCode != http.StatusTemporaryRedirect || k.offsite.Load() != 0 {
		t.Errorf("a redirect: %v, followed %d times", err, k.offsite.Load())
	}
	before := k.asked.Load()
	if err := c.RevokeKey(t.Context(), "login_1", ""); err == nil {
		t.Error("an empty id was accepted")
	}
	if err := c.RevokeKey(t.Context(), "", "key_1"); err == nil {
		t.Error("an empty token was accepted")
	}
	if k.asked.Load() != before {
		t.Error("a revoke that could not succeed was sent")
	}
}

func TestKeysUnreachable(t *testing.T) {
	c := &Config{Issuer: "http://127.0.0.1:1"}
	if _, err := c.CreateKey(t.Context(), "login_1", &CreateKeyParams{Name: "x", Grants: testGrants}); err == nil || !strings.HasPrefix(err.Error(), "signin: POST /me/keys: ") {
		t.Errorf("an unreachable service: %v", err)
	}
	if err := c.RevokeKey(t.Context(), "login_1", "key_1"); err == nil || !strings.HasPrefix(err.Error(), "signin: DELETE /me/keys/key_1: ") {
		t.Errorf("an unreachable service: %v", err)
	}
	if _, err := (&Config{Issuer: "http://bad host"}).CreateKey(t.Context(), "login_1", &CreateKeyParams{Name: "x", Grants: testGrants}); err == nil {
		t.Error("an issuer that is no address answered")
	}
	if _, err := c.CreateKey(t.Context(), "login_1", &CreateKeyParams{Name: "x", Grants: json.RawMessage(`[`)}); err == nil || !strings.HasPrefix(err.Error(), "signin: POST /me/keys: ") {
		t.Errorf("grants that are not JSON: %v", err)
	}
}

func TestListKeys(t *testing.T) {
	k := newKeys(t)
	c := k.config()
	keys, err := c.ListKeys(t.Context(), "login_1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("ListKeys returned %d keys, want 2", len(keys))
	}
	member, old := keys[0], keys[1]
	if member.ID != "key_2" || member.OrgID != "org_1" || member.Status != KeyPendingApproval || member.Value != "" ||
		!member.ExpiresAt.IsZero() || !member.RevokedAt.IsZero() || !member.LastUsedAt.Equal(time.Date(2026, 10, 22, 14, 0, 0, 0, time.UTC)) {
		t.Errorf("the member key is %+v", member)
	}
	if !strings.Contains(string(member.RawJSON()), `"approved_at":null`) {
		t.Errorf("RawJSON lost a field: %s", member.RawJSON())
	}
	if old.Status != KeyRevoked || old.OrgID != "" || !old.LastUsedAt.IsZero() ||
		!old.RevokedAt.Equal(time.Date(2026, 1, 3, 3, 4, 5, 0, time.UTC)) || !old.ExpiresAt.Equal(time.Date(2026, 2, 2, 3, 4, 5, 0, time.UTC)) {
		t.Errorf("the revoked key is %+v", old)
	}
	if _, err := c.ListKeys(t.Context(), "login_1", &ListKeysParams{Personal: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListKeys(t.Context(), "login_1", &ListKeysParams{OrgID: "org_1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListKeys(t.Context(), "login_1", &ListKeysParams{}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(k.lists, ","); got != ",org_id=,org_id=org_1," {
		t.Errorf("the lists asked %q", got)
	}
	if keys, err := c.ListKeys(t.Context(), "alone", nil); err != nil || keys == nil || len(keys) != 0 {
		t.Errorf("a person with no keys: %v, %v; want an empty slice", keys, err)
	}
	var oe *Error
	if _, err := c.ListKeys(t.Context(), "api_token", nil); !errors.As(err, &oe) || oe.StatusCode != http.StatusUnauthorized {
		t.Errorf("a refused token: %v", err)
	}
	before := k.asked.Load()
	if _, err := c.ListKeys(t.Context(), "login_1", &ListKeysParams{Personal: true, OrgID: "org_1"}); err == nil {
		t.Error("a list of personal and member keys at once was accepted")
	}
	if k.asked.Load() != before {
		t.Error("a list that could not succeed was sent")
	}
}
