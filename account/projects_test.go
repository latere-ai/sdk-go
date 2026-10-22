// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package account_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"latere.ai/x/sdk-go/account"
	"latere.ai/x/sdk-go/latere"
)

const project = `{"id":"prj_01JA4Q0Z8X3K5V7M9N1P2R4T6W","owner":{"id":"org_01J9ZK3M","kind":"organization"},"name":"Tide tables",
"instructions":"Keep the site static.","apps":[{"id":"5c1e7a2b-3d4f-4a6b-8c9d-0e1f2a3b4c5d","name":"Tide tables","slug":"tide-tables","url":"https://tide-tables.latere.site"}],
"actions":["rename","edit_instructions","add_app","delete"],"created_by":"https://auth.latere.ai|8f1c","created_at":"2026-10-07T09:12:40Z","updated_at":"2026-10-07T10:02:11Z","added_later":1}`

// sent is one request a test server received.
type sent struct {
	method, path string
	query        url.Values
	body         map[string]any
}

// recordingAPI is a Client of a server that records each request and
// answers it with answer, with the given retries.
func recordingAPI(t *testing.T, retries int, answer func(w http.ResponseWriter, r *http.Request, n int)) (*account.Client, *[]sent) {
	t.Helper()
	var got []sent
	var n atomic.Int32
	c := deviceAPI(t, retries, func(w http.ResponseWriter, r *http.Request) {
		s := sent{method: r.Method, path: r.URL.EscapedPath(), query: r.URL.Query()}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &s.body); err != nil {
				t.Errorf("the body %s is no JSON object: %v", raw, err)
			}
		}
		got = append(got, s)
		answer(w, r, int(n.Add(1)))
	})
	return c, &got
}

func TestListProjects(t *testing.T) {
	c, got := recordingAPI(t, 0, func(w http.ResponseWriter, r *http.Request, _ int) {
		if r.URL.Query().Get("cursor") == "" {
			_, _ = io.WriteString(w, `{"items":[`+project+`],"next_cursor":"c2"}`)
			return
		}
		_, _ = io.WriteString(w, `{"items":[`+project+`]}`)
	})
	var listed []*account.Project
	for p, err := range c.ListProjects(t.Context(), &account.ListProjectsParams{App: "5c1e7a2b-3d4f-4a6b-8c9d-0e1f2a3b4c5d", Limit: 10, Cursor: "ignored"}) {
		if err != nil {
			t.Fatal(err)
		}
		listed = append(listed, p)
	}
	if len(listed) != 2 || len(*got) != 2 {
		t.Fatalf("listed %d projects in %d requests, want 2 in 2", len(listed), len(*got))
	}
	first, second := (*got)[0], (*got)[1]
	if first.method != http.MethodGet || first.path != "/v1/account/projects" || first.query.Get("app") != "5c1e7a2b-3d4f-4a6b-8c9d-0e1f2a3b4c5d" ||
		first.query.Get("limit") != "10" || first.query.Has("cursor") || second.query.Get("cursor") != "c2" {
		t.Errorf("sent %+v then %+v", first, second)
	}
	p := listed[0]
	if p.ID != "prj_01JA4Q0Z8X3K5V7M9N1P2R4T6W" || p.Owner.Kind != account.OwnerOrganization || p.Owner.ID != "org_01J9ZK3M" ||
		len(p.Apps) != 1 || p.Apps[0].Slug != "tide-tables" || p.Apps[0].URL == "" || len(p.Actions) != 4 || p.Actions[2] != account.ProjectAddApp ||
		p.CreatedAt.IsZero() || !p.UpdatedAt.After(p.CreatedAt) || string(p.RawJSON()) == "" {
		t.Errorf("project %+v", p)
	}

	*got = nil
	page, err := c.ListProjectsPage(t.Context(), &account.ListProjectsParams{Cursor: "c2"})
	if err != nil || len(page.Items) != 1 || page.NextCursor != "" {
		t.Fatalf("page %+v, %v", page, err)
	}
	if q := (*got)[0].query; q.Get("cursor") != "c2" || q.Has("app") || q.Has("limit") {
		t.Errorf("the page asked for %v", q)
	}
	*got = nil
	if _, err := c.ListProjectsPage(t.Context(), nil); err != nil || len((*got)[0].query) != 0 {
		t.Errorf("a nil params' page: %v, sent %v", err, (*got)[0].query)
	}

	refused, _ := recordingAPI(t, 0, func(w http.ResponseWriter, r *http.Request, _ int) {
		writeError(w, http.StatusForbidden, "session_not_allowed")
	})
	for _, err := range refused.ListProjects(t.Context(), nil) {
		if !errors.Is(err, latere.ErrForbidden) {
			t.Errorf("a hosted agent's list: %v", err)
		}
	}
}

func TestCreateProject(t *testing.T) {
	c, got := recordingAPI(t, 3, func(w http.ResponseWriter, r *http.Request, n int) {
		switch {
		case n == 1:
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, project)
		case n == 2:
			writeError(w, http.StatusConflict, "project_limit")
		case n == 3:
			writeError(w, http.StatusConflict, "app_not_in_context")
		default:
			writeError(w, http.StatusServiceUnavailable, "unavailable")
		}
	})
	p, err := c.CreateProject(t.Context(), &account.CreateProjectParams{Name: "Tide tables", Instructions: "Keep the site static.", Apps: []string{"5c1e7a2b-3d4f-4a6b-8c9d-0e1f2a3b4c5d"}})
	if err != nil || p.Name != "Tide tables" {
		t.Fatalf("created %+v, %v", p, err)
	}
	s := (*got)[0]
	if s.method != http.MethodPost || s.path != "/v1/account/projects" || s.body["name"] != "Tide tables" ||
		s.body["instructions"] != "Keep the site static." || len(s.body["apps"].([]any)) != 1 || len(s.body) != 3 {
		t.Errorf("sent %+v", s)
	}
	if _, err := c.CreateProject(t.Context(), &account.CreateProjectParams{Name: "Second"}); !errors.Is(err, account.ErrProjectLimit) || !errors.Is(err, latere.ErrConflict) ||
		!strings.Contains(err.Error(), "No.") {
		t.Errorf("a context past its bound: %v", err)
	}
	if len((*got)[1].body) != 1 {
		t.Errorf("a create of a name alone sent %v", (*got)[1].body)
	}
	var apiErr *latere.Error
	if _, err := c.CreateProject(t.Context(), &account.CreateProjectParams{Name: "Third", Apps: []string{"x"}}); !errors.Is(err, account.ErrAppNotInContext) ||
		errors.Is(err, account.ErrProjectLimit) || !errors.As(err, &apiErr) || apiErr.Code != "app_not_in_context" {
		t.Errorf("an app of another context: %v", err)
	}
	// A create takes no key, so a server error is not sent again.
	if _, err := c.CreateProject(t.Context(), &account.CreateProjectParams{Name: "Fourth"}); !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusServiceUnavailable || len(*got) != 4 {
		t.Errorf("a create the API failed: %v after %d requests, want one", err, len(*got)-3)
	}
	for _, params := range []*account.CreateProjectParams{nil, {}, {Name: "  "}} {
		if _, err := c.CreateProject(t.Context(), params); err == nil || len(*got) != 4 {
			t.Errorf("a create of %+v: %v, sent %d", params, err, len(*got))
		}
	}
}

func TestGetAndUpdateProject(t *testing.T) {
	var failed atomic.Bool
	c, got := recordingAPI(t, 2, func(w http.ResponseWriter, r *http.Request, _ int) {
		switch {
		case r.URL.EscapedPath() == "/v1/account/projects/prj_gone":
			writeError(w, http.StatusNotFound, "project_not_found")
		case r.Method == http.MethodPatch && !failed.Swap(true):
			writeError(w, http.StatusBadGateway, "bad_gateway")
		default:
			_, _ = io.WriteString(w, project)
		}
	})
	p, err := c.GetProject(t.Context(), "prj_01JA4Q0Z8X3K5V7M9N1P2R4T6W")
	if err != nil || p.Instructions != "Keep the site static." || (*got)[0].method != http.MethodGet || (*got)[0].path != "/v1/account/projects/prj_01JA4Q0Z8X3K5V7M9N1P2R4T6W" {
		t.Fatalf("read %+v, %v, sent %+v", p, err, (*got)[0])
	}
	if _, err := c.GetProject(t.Context(), "prj_gone"); !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("a project of another context: %v", err)
	}

	*got = nil
	name, none := "Tide tables, 2027", ""
	if _, err := c.UpdateProject(t.Context(), "prj_01JA4Q0Z8X3K5V7M9N1P2R4T6W", &account.UpdateProjectParams{Name: &name, Instructions: &none}); err != nil {
		t.Fatal(err)
	}
	// The first attempt met a 502, and a change is sent again.
	if len(*got) != 2 {
		t.Fatalf("sent %d changes, want a retry after the 502", len(*got))
	}
	s := (*got)[1]
	if s.method != http.MethodPatch || s.path != "/v1/account/projects/prj_01JA4Q0Z8X3K5V7M9N1P2R4T6W" || s.body["name"] != name ||
		s.body["instructions"] != "" || len(s.body) != 2 {
		t.Errorf("sent %+v", s)
	}
	*got = nil
	if _, err := c.UpdateProject(t.Context(), "prj_01JA4Q0Z8X3K5V7M9N1P2R4T6W", &account.UpdateProjectParams{Name: &name}); err != nil || len((*got)[0].body) != 1 {
		t.Errorf("a rename alone: %v, sent %v", err, (*got)[0].body)
	}
	*got = nil
	if _, err := c.UpdateProject(t.Context(), "prj_01JA4Q0Z8X3K5V7M9N1P2R4T6W", nil); err != nil || len((*got)[0].body) != 0 {
		t.Errorf("an empty change: %v, sent %v", err, (*got)[0].body)
	}
	*got = nil
	if _, err := c.UpdateProject(t.Context(), "prj_gone", &account.UpdateProjectParams{Name: &name}); !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("a change of a project of another context: %v", err)
	}
	*got = nil
	if _, err := c.GetProject(t.Context(), ""); err == nil || len(*got) != 0 {
		t.Errorf("an empty id was read: %v", err)
	}
	if _, err := c.UpdateProject(t.Context(), "", &account.UpdateProjectParams{Name: &name}); err == nil || len(*got) != 0 {
		t.Errorf("an empty id was changed: %v", err)
	}
}

func TestDeleteProject(t *testing.T) {
	c, got := recordingAPI(t, 0, func(w http.ResponseWriter, r *http.Request, _ int) {
		switch r.URL.EscapedPath() {
		case "/v1/account/projects/prj_1":
			w.WriteHeader(http.StatusNoContent)
		case "/v1/account/projects/prj_gone":
			writeError(w, http.StatusNotFound, "project_not_found")
		case "/v1/account/projects/prj_proxy":
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, "<html>no</html>")
		default:
			writeError(w, http.StatusForbidden, "role_insufficient")
		}
	})
	if err := c.DeleteProject(t.Context(), "prj_1"); err != nil || (*got)[0].method != http.MethodDelete || (*got)[0].path != "/v1/account/projects/prj_1" {
		t.Errorf("delete: %v, sent %+v", err, (*got)[0])
	}
	if err := c.DeleteProject(t.Context(), "prj_gone"); err != nil {
		t.Errorf("a project the context no longer holds: %v, want done", err)
	}
	if err := c.DeleteProject(t.Context(), "prj_proxy"); !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("a 404 that is not the API's: %v, want an error", err)
	}
	var apiErr *latere.Error
	if err := c.DeleteProject(t.Context(), "prj_other"); !errors.As(err, &apiErr) || apiErr.Code != "role_insufficient" {
		t.Errorf("a member's delete of another's project: %v", err)
	}
	if err := c.DeleteProject(t.Context(), ""); err == nil || len(*got) != 4 {
		t.Errorf("an empty id was deleted: %v", err)
	}
}

func TestProjectApps(t *testing.T) {
	c, got := recordingAPI(t, 0, func(w http.ResponseWriter, r *http.Request, _ int) {
		switch {
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.URL.EscapedPath() == "/v1/account/projects/prj_1/apps/full":
			writeError(w, http.StatusConflict, "project_limit")
		default:
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, project)
		}
	})
	p, err := c.AddProjectApp(t.Context(), "prj_1", "5c1e7a2b-3d4f-4a6b-8c9d-0e1f2a3b4c5d")
	if err != nil || len(p.Apps) != 1 {
		t.Fatalf("added: %+v, %v", p, err)
	}
	if s := (*got)[0]; s.method != http.MethodPut || s.path != "/v1/account/projects/prj_1/apps/5c1e7a2b-3d4f-4a6b-8c9d-0e1f2a3b4c5d" || s.body != nil {
		t.Errorf("sent %+v", s)
	}
	if _, err := c.AddProjectApp(t.Context(), "prj_1", "full"); !errors.Is(err, account.ErrProjectLimit) {
		t.Errorf("a project that holds its bound of apps: %v", err)
	}
	if err := c.RemoveProjectApp(t.Context(), "prj_1", "5c1e7a2b-3d4f-4a6b-8c9d-0e1f2a3b4c5d"); err != nil {
		t.Fatal(err)
	}
	if s := (*got)[2]; s.method != http.MethodDelete || s.path != "/v1/account/projects/prj_1/apps/5c1e7a2b-3d4f-4a6b-8c9d-0e1f2a3b4c5d" {
		t.Errorf("sent %+v", s)
	}
	for _, ids := range [][2]string{{"", "app"}, {"prj_1", ""}} {
		if _, err := c.AddProjectApp(t.Context(), ids[0], ids[1]); err == nil {
			t.Errorf("added %q to %q", ids[1], ids[0])
		}
		if err := c.RemoveProjectApp(t.Context(), ids[0], ids[1]); err == nil {
			t.Errorf("removed %q from %q", ids[1], ids[0])
		}
	}
	if len(*got) != 3 {
		t.Errorf("sent %d requests, want an empty id refused before any", len(*got))
	}
}

// Every resource the project and trigger calls answer keeps the JSON it
// was decoded from.
func TestProjectsAndInletsKeepTheirJSON(t *testing.T) {
	for name, v := range map[string]interface {
		RawJSON() json.RawMessage
	}{
		"project":         &account.Project{},
		"trigger inbound": &account.TriggerInbound{},
		"trigger hook":    &account.TriggerHook{},
		"trigger address": &account.TriggerAddress{},
	} {
		if v.RawJSON() != nil {
			t.Errorf("%s: one the program built has JSON", name)
		}
		raw := `{"added_later":"kept"}`
		if err := json.Unmarshal([]byte(raw), v); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(v.RawJSON()) != raw {
			t.Errorf("%s: RawJSON = %s, want %s", name, v.RawJSON(), raw)
		}
		if err := json.Unmarshal([]byte(`{"items":7,"mail":7,"trigger_id":7,"id":7}`), v); err == nil {
			t.Errorf("%s: JSON of the wrong shape was decoded", name)
		}
	}
}
