// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package apps_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"latere.ai/x/sdk-go/apps"
	"latere.ai/x/sdk-go/latere"
)

// call is one request the fake API answered.
type call struct {
	Method, Path, Query, Body string
	Header                    http.Header
}

// fakeAPI is an API that answers each "METHOD /path" with its handler, and
// records every request.
type fakeAPI struct {
	mu     sync.Mutex
	calls  []call
	routes map[string]http.HandlerFunc
}

func (f *fakeAPI) record() []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]call(nil), f.calls...)
}

// newFake starts a fake API and returns a client of it with retries as
// given, and the fake.
func newFake(t *testing.T, retries int, routes map[string]http.HandlerFunc) (*apps.Client, *fakeAPI) {
	t.Helper()
	f := &fakeAPI{routes: routes}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading the request body: %v", err)
		}
		f.mu.Lock()
		f.calls = append(f.calls, call{Method: r.Method, Path: r.URL.EscapedPath(), Query: r.URL.RawQuery, Body: string(body), Header: r.Header.Clone()})
		f.mu.Unlock()
		h, ok := f.routes[r.Method+" "+r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"error":{"code":"not_found","message":"Not found.","details":{}}}`)
			return
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	cfg, err := latere.NewConfig(latere.WithBaseURL(srv.URL), latere.WithTokenSource(latere.StaticToken("tok")),
		latere.WithMaxRetries(retries), latere.WithBackoff(time.Millisecond, time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	return apps.New(cfg), f
}

// answer replies with status and body as JSON.
func answer(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}
}

const appJSON = `{"id":"5c1e2a3b-4d5e-4f60-8a1b-2c3d4e5f6a7b","slug":"tide-tables","name":"Tide tables",
	"owner":{"kind":"personal","id":"usr_1"},"visibility":"unlisted","state":"active","health":"ready",
	"url":"https://tide-tables.latere.site","preview_url":"https://tide-tables--p.latere.site",
	"current_deploy":{"id":"d1","seq":3,"kind":"static","status":"live","ready_at":"2026-10-07T10:00:00Z","live_at":"2026-10-07T10:01:00Z","commit_sha":"abc","thumbnail":null},
	"latest_preview":null,"session":{"id":"ses_1"},"published_by_session":null,"published_at":"2026-10-07T10:01:00Z",
	"actions":["open","copy_link","unpublish","delete"],"thumbnail":{"url":"/v1/apps/x","width":1280,"height":800},
	"settings":{"sleep_after_seconds":1800,"always_on":false,"previews":{"visibility":"private"},"paused_by":"owner"},
	"repository":{"clone_url":"https://code.latere.ai/r/5c1e","push_url":"https://code.latere.ai/al/tide-tables"},
	"cluster":{"dockerfile_builds":false},"env_pending":false,"pending_deploy":false,"slug_generated":false,
	"created_at":"2026-10-07T09:00:00Z","updated_at":"2026-10-07T10:01:00Z"}`

func TestAppCalls(t *testing.T) {
	c, f := newFake(t, 0, map[string]http.HandlerFunc{
		"POST /v1/apps/apps":               answer(http.StatusCreated, appJSON),
		"GET /v1/apps/apps/tide-tables":    answer(http.StatusOK, appJSON),
		"PATCH /v1/apps/apps/tide-tables":  answer(http.StatusOK, appJSON),
		"DELETE /v1/apps/apps/tide-tables": answer(http.StatusAccepted, `{"state":"deleting"}`),
	})
	ctx := t.Context()

	created, err := c.CreateApp(ctx, &apps.CreateAppParams{Name: "Tide tables", Visibility: apps.VisibilityUnlisted})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	if created.Slug != "tide-tables" || created.State != apps.AppActive || created.Health != apps.HealthReady ||
		created.Owner.Kind != apps.OwnerPersonal || created.CurrentDeploy.Status != apps.DeployLive ||
		created.Session.ID != "ses_1" || created.Settings.Previews.Visibility != apps.PreviewsPrivate ||
		!created.PublishedAt.Equal(time.Date(2026, 10, 7, 10, 1, 0, 0, time.UTC)) || len(created.Actions) != 4 {
		t.Errorf("CreateApp decoded %+v", created)
	}
	// RawJSON keeps what the models leave out, such as a setting the
	// platform added.
	if !strings.Contains(string(created.RawJSON()), `"paused_by":"owner"`) {
		t.Errorf("RawJSON = %s, want the answer as sent", created.RawJSON())
	}

	if _, err := c.GetApp(ctx, "tide-tables"); err != nil {
		t.Fatalf("GetApp: %v", err)
	}
	alwaysOn := false
	if _, err := c.UpdateApp(ctx, "tide-tables", &apps.UpdateAppParams{Slug: "tides", Settings: &apps.SettingsUpdate{AlwaysOn: &alwaysOn}}); err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	if err := c.DeleteApp(ctx, "tide-tables"); err != nil {
		t.Fatalf("DeleteApp: %v", err)
	}

	calls := f.record()
	want := []struct{ method, path, body string }{
		{"POST", "/v1/apps/apps", `{"name":"Tide tables","visibility":"unlisted"}`},
		{"GET", "/v1/apps/apps/tide-tables", ""},
		{"PATCH", "/v1/apps/apps/tide-tables", `{"slug":"tides","settings":{"always_on":false}}`},
		{"DELETE", "/v1/apps/apps/tide-tables", ""},
	}
	if len(calls) != len(want) {
		t.Fatalf("made %d requests, want %d: %+v", len(calls), len(want), calls)
	}
	for i, w := range want {
		if calls[i].Method != w.method || calls[i].Path != w.path || strings.TrimSpace(calls[i].Body) != w.body {
			t.Errorf("request %d = %s %s %s, want %s %s %s", i, calls[i].Method, calls[i].Path, calls[i].Body, w.method, w.path, w.body)
		}
	}
}

func TestListAppsPagesAndStopsEarly(t *testing.T) {
	page := func(cursor string, ids ...string) string {
		items := make([]string, len(ids))
		for i, id := range ids {
			items[i] = `{"id":"` + id + `","slug":"s-` + id + `"}`
		}
		return `{"items":[` + strings.Join(items, ",") + `],"has_more":` + fmt.Sprint(cursor != "") + `,"next_cursor":"` + cursor + `"}`
	}
	c, f := newFake(t, 0, map[string]http.HandlerFunc{
		"GET /v1/apps/apps": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("cursor") == "" {
				answer(http.StatusOK, page("c2", "a", "b"))(w, r)
				return
			}
			answer(http.StatusOK, page("", "c"))(w, r)
		},
	})
	var got []string
	for a, err := range c.ListApps(t.Context(), &apps.ListAppsParams{Order: apps.OrderPublished, Limit: 2}) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, a.ID)
	}
	if strings.Join(got, ",") != "a,b,c" {
		t.Errorf("listed %v, want a, b, c", got)
	}
	calls := f.record()
	if len(calls) != 2 || calls[0].Query != "limit=2&sort=published" || calls[1].Query != "cursor=c2&limit=2&sort=published" {
		t.Errorf("queries = %+v", calls)
	}

	// A loop that stops in the first page asks for no other.
	for range c.ListApps(t.Context(), nil) {
		break
	}
	if n := len(f.record()); n != 3 {
		t.Errorf("an early break made %d requests in all, want 3", n)
	}

	p, err := c.ListAppsPage(t.Context(), &apps.ListAppsParams{IDs: []string{"a", "b"}, Cursor: "c2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 1 || p.NextCursor != "" {
		t.Errorf("ListAppsPage = %+v", p)
	}
	if q := f.record()[3].Query; q != "cursor=c2&id=a&id=b" {
		t.Errorf("ListAppsPage query = %q", q)
	}
}

// A create is sent once, whatever the retry policy: the apps API takes no
// idempotency key, so a repeat could be a second app. A read is retried.
func TestCreatesAreNotRetriedAndReadsAre(t *testing.T) {
	c, f := newFake(t, 2, map[string]http.HandlerFunc{
		"POST /v1/apps/apps":            answer(http.StatusServiceUnavailable, `{"error":{"code":"unavailable","message":"Try again.","details":{}}}`),
		"GET /v1/apps/apps/tide-tables": answer(http.StatusServiceUnavailable, `{"error":{"code":"unavailable","message":"Try again.","details":{}}}`),
	})
	if _, err := c.CreateApp(t.Context(), nil); err == nil {
		t.Fatal("CreateApp succeeded against a failing API")
	}
	if n := len(f.record()); n != 1 {
		t.Errorf("CreateApp sent %d requests, want 1", n)
	}
	if calls := f.record(); calls[0].Header.Get("Idempotency-Key") != "" {
		t.Error("CreateApp sent an idempotency key the API does not take")
	}
	if _, err := c.GetApp(t.Context(), "tide-tables"); err == nil {
		t.Fatal("GetApp succeeded against a failing API")
	}
	if n := len(f.record()); n != 4 {
		t.Errorf("GetApp sent %d requests, want 3", n-1)
	}
}

func TestRefusalsAreErrors(t *testing.T) {
	c, _ := newFake(t, 0, map[string]http.HandlerFunc{
		"POST /v1/apps/apps": answer(http.StatusConflict, `{"error":{"code":"slug_taken","message":"The slug is taken.","details":{"hint":"Choose another slug."}}}`),
	})
	_, err := c.CreateApp(t.Context(), &apps.CreateAppParams{Slug: "taken"})
	if !errors.Is(err, latere.ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	var e *latere.Error
	if !errors.As(err, &e) || e.Code != "slug_taken" || e.Details["hint"] != "Choose another slug." {
		t.Errorf("err = %#v", err)
	}
	if _, err := c.GetApp(t.Context(), "gone"); !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("GetApp of an unknown app: %v, want ErrNotFound", err)
	}
}

// A call missing what names its resource fails before any request.
func TestMissingNamesFailBeforeAnyRequest(t *testing.T) {
	c, f := newFake(t, 0, nil)
	ctx := t.Context()
	checks := map[string]error{}
	_, checks["GetApp"] = c.GetApp(ctx, "")
	_, checks["UpdateApp"] = c.UpdateApp(ctx, "", &apps.UpdateAppParams{Name: "x"})
	_, checks["UpdateApp without a change"] = c.UpdateApp(ctx, "x", &apps.UpdateAppParams{})
	checks["DeleteApp"] = c.DeleteApp(ctx, "")
	_, checks["ListDeploys"] = c.ListDeploys(ctx, "")
	_, checks["GetDeploy"] = c.GetDeploy(ctx, "x", "")
	_, checks["ListReleases"] = c.ListReleases(ctx, "")
	_, checks["GetRelease"] = c.GetRelease(ctx, "x", "")
	_, checks["GetAccess"] = c.GetAccess(ctx, "")
	_, checks["Invite"] = c.Invite(ctx, "id", nil)
	checks["RevokeAccess"] = c.RevokeAccess(ctx, "id", "")
	checks["SetPassword"] = c.SetPassword(ctx, "id", "")
	checks["RemovePassword"] = c.RemovePassword(ctx, "")
	for _, err := range c.ReadDeployLog(ctx, "", "d", nil) {
		checks["ReadDeployLog"] = err
	}
	for _, err := range c.FollowDeployLog(ctx, "x", "", nil) {
		checks["FollowDeployLog"] = err
	}
	for name, err := range checks {
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if n := len(f.record()); n != 0 {
		t.Errorf("made %d requests", n)
	}
}

func TestReleases(t *testing.T) {
	release := `{"id":"r1","tag":"v1.2.0","commit_sha":"abc","status":"released","reason":"","source_deploy":"d0","deploy":"d1",
		"via":"push","created_by":"usr_1","created_at":"2026-10-07T10:00:00Z","released_at":"2026-10-07T10:01:00Z","superseded_at":null}`
	c, f := newFake(t, 0, map[string]http.HandlerFunc{
		"GET /v1/apps/apps/tide-tables/releases":        answer(http.StatusOK, `{"releases":[`+release+`]}`),
		"GET /v1/apps/apps/tide-tables/releases/v1.2.0": answer(http.StatusOK, `{"release":`+release+`,"deploy":{"id":"d1","status":"live"}}`),
	})
	list, err := c.ListReleases(t.Context(), "tide-tables")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Status != apps.ReleaseReleased || list[0].SupersededAt.IsZero() != true || len(list[0].RawJSON()) == 0 {
		t.Errorf("ListReleases = %+v", list)
	}
	detail, err := c.GetRelease(t.Context(), "tide-tables", "v1.2.0")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Release.Tag != "v1.2.0" || detail.Deploy == nil || detail.Deploy.Status != apps.DeployLive || detail.SourceDeploy != nil {
		t.Errorf("GetRelease = %+v", detail)
	}
	if p := f.record()[1].Path; p != "/v1/apps/apps/tide-tables/releases/v1.2.0" {
		t.Errorf("GetRelease path = %s", p)
	}
}

func TestAccess(t *testing.T) {
	const appID = "5c1e2a3b-4d5e-4f60-8a1b-2c3d4e5f6a7b"
	base := "/v1/account/apps/" + appID + "/access"
	c, f := newFake(t, 2, map[string]http.HandlerFunc{
		"GET " + base: answer(http.StatusOK, `{"app_id":"`+appID+`","people":[{"id":"inv_1","role":"viewer","email":"pilot@harbor.example",
			"state":"active","invited_at":"2026-10-07T10:10:00Z","claimed_at":"2026-10-07T11:00:00Z","person":{"display_name":"Pia","handle":"pia"}}],
			"organization":null,"password":{"set_at":"2026-10-07T12:00:00Z"},"actions":["invite","revoke","set_password"],
			"limits":{"max_people":50,"max_emails_per_invite":20,"min_password":8,"max_password":128}}`),
		"POST " + base + "/invites": answer(http.StatusCreated, `{"people":[{"id":"inv_2","role":"viewer","email":"a@b.example","state":"pending","invited_at":"2026-10-07T10:10:00Z"}],
			"skipped":[{"email":"me@b.example","reason":"self"}]}`),
		"DELETE " + base + "/inv_1":    answer(http.StatusNoContent, ""),
		"PUT " + base + "/password":    answer(http.StatusNoContent, ""),
		"DELETE " + base + "/password": answer(http.StatusNoContent, ""),
	})
	ctx := t.Context()
	a, err := c.GetAccess(ctx, appID)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.People) != 1 || a.People[0].State != apps.AccessActive || a.People[0].Account.Handle != "pia" ||
		a.Password == nil || a.Organization != nil || a.Limits.MaxPeople != 50 || a.Actions[2] != apps.AccessSetPassword {
		t.Errorf("GetAccess = %+v", a)
	}
	inv, err := c.Invite(ctx, appID, &apps.InviteParams{Emails: []string{"a@b.example", "me@b.example"}, Role: apps.RoleViewer})
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.People) != 1 || inv.People[0].State != apps.AccessPending || inv.People[0].Role != apps.RoleViewer || inv.Skipped[0].Reason != "self" {
		t.Errorf("Invite = %+v", inv)
	}
	if err := c.RevokeAccess(ctx, appID, "inv_1"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetPassword(ctx, appID, "harbor-lights"); err != nil {
		t.Fatal(err)
	}
	if err := c.RemovePassword(ctx, appID); err != nil {
		t.Fatal(err)
	}
	calls := f.record()
	var invite struct {
		Emails []string `json:"emails"`
		Role   string   `json:"role"`
	}
	if err := json.Unmarshal([]byte(calls[1].Body), &invite); err != nil || len(invite.Emails) != 2 || invite.Role != "viewer" {
		t.Errorf("Invite body = %s", calls[1].Body)
	}
	if strings.TrimSpace(calls[3].Body) != `{"password":"harbor-lights"}` || calls[3].Method != http.MethodPut {
		t.Errorf("SetPassword sent %s %s", calls[3].Method, calls[3].Body)
	}
}

// An invite is sent once, whatever the retry policy: a repeat would answer
// the people as already invited and count them twice against the day's
// invitations.
func TestInvitesAreNotRetried(t *testing.T) {
	c, f := newFake(t, 2, map[string]http.HandlerFunc{
		"POST /v1/account/apps/a1/access/invites": answer(http.StatusBadGateway, `{"error":{"code":"unavailable","message":"Try again.","details":{}}}`),
	})
	if _, err := c.Invite(t.Context(), "a1", &apps.InviteParams{Emails: []string{"a@b.example"}}); err == nil {
		t.Fatal("Invite succeeded against a failing API")
	}
	if n := len(f.record()); n != 1 {
		t.Errorf("Invite sent %d requests, want 1", n)
	}
}

// Every call returns the API's refusal as a *latere.Error.
func TestEveryCallReturnsTheRefusal(t *testing.T) {
	refuse := answer(http.StatusForbidden, `{"error":{"code":"forbidden","message":"You may not.","details":{}}}`)
	c, f := newFake(t, 0, map[string]http.HandlerFunc{})
	for _, route := range []string{
		"GET /v1/apps/apps", "GET /v1/apps/apps/x", "PATCH /v1/apps/apps/x", "GET /v1/apps/apps/x/deploys",
		"GET /v1/apps/apps/x/deploys/d", "GET /v1/apps/apps/x/deploys/d/logs", "GET /v1/apps/apps/x/releases",
		"GET /v1/apps/apps/x/releases/v1", "GET /v1/account/apps/a/access", "POST /v1/account/apps/a/access/invites",
		"DELETE /v1/account/apps/a/access/p", "PUT /v1/account/apps/a/access/password",
	} {
		f.routes[route] = refuse
	}
	ctx := t.Context()
	errs := map[string]error{}
	_, errs["ListAppsPage"] = c.ListAppsPage(ctx, nil)
	_, errs["GetApp"] = c.GetApp(ctx, "x")
	_, errs["UpdateApp"] = c.UpdateApp(ctx, "x", &apps.UpdateAppParams{Name: "y"})
	_, errs["ListDeploys"] = c.ListDeploys(ctx, "x")
	_, errs["GetDeploy"] = c.GetDeploy(ctx, "x", "d")
	_, errs["ListReleases"] = c.ListReleases(ctx, "x")
	_, errs["GetRelease"] = c.GetRelease(ctx, "x", "v1")
	_, errs["GetAccess"] = c.GetAccess(ctx, "a")
	_, errs["Invite"] = c.Invite(ctx, "a", &apps.InviteParams{Emails: []string{"e@x.example"}})
	errs["RevokeAccess"] = c.RevokeAccess(ctx, "a", "p")
	errs["SetPassword"] = c.SetPassword(ctx, "a", "long-enough")
	for _, err := range c.ReadDeployLog(ctx, "x", "d", nil) {
		errs["ReadDeployLog"] = err
	}
	for _, err := range c.FollowDeployLog(ctx, "x", "d", nil) {
		errs["FollowDeployLog"] = err
	}
	for name, err := range errs {
		if !errors.Is(err, latere.ErrForbidden) {
			t.Errorf("%s: %v, want the refusal", name, err)
		}
	}
}

// An answer whose fields have the wrong types is an error, not a model
// silently left at zero.
func TestMalformedAnswersAreErrors(t *testing.T) {
	c, _ := newFake(t, 0, map[string]http.HandlerFunc{
		"GET /v1/apps/apps/x":                    answer(http.StatusOK, `{"slug":7}`),
		"GET /v1/apps/apps/x/deploys":            answer(http.StatusOK, `{"deploys":[{"seq":"four"}]}`),
		"GET /v1/apps/apps/x/releases":           answer(http.StatusOK, `{"releases":[{"tag":1}]}`),
		"GET /v1/apps/apps/x/releases/v1":        answer(http.StatusOK, `{"release":{"tag":1}}`),
		"GET /v1/account/apps/a/access":          answer(http.StatusOK, `{"people":"none"}`),
		"POST /v1/account/apps/a/access/invites": answer(http.StatusCreated, `{"skipped":"none"}`),
	})
	ctx := t.Context()
	errs := map[string]error{}
	_, errs["GetApp"] = c.GetApp(ctx, "x")
	_, errs["ListDeploys"] = c.ListDeploys(ctx, "x")
	_, errs["ListReleases"] = c.ListReleases(ctx, "x")
	_, errs["GetRelease"] = c.GetRelease(ctx, "x", "v1")
	_, errs["GetAccess"] = c.GetAccess(ctx, "a")
	_, errs["Invite"] = c.Invite(ctx, "a", &apps.InviteParams{Emails: []string{"e@x.example"}})
	for name, err := range errs {
		if err == nil {
			t.Errorf("%s decoded a malformed answer without an error", name)
		}
	}
}

// Every model keeps the JSON it was decoded from, and a model a program
// builds has none.
func TestRawJSON(t *testing.T) {
	for name, m := range map[string]interface{ RawJSON() json.RawMessage }{
		"App": &apps.App{}, "Deploy": &apps.Deploy{}, "LogLine": &apps.LogLine{}, "LogEnd": &apps.LogEnd{},
		"Release": &apps.Release{}, "ReleaseDetail": &apps.ReleaseDetail{}, "Access": &apps.Access{}, "Invites": &apps.Invites{},
	} {
		if m.RawJSON() != nil {
			t.Errorf("a %s built by the program has RawJSON %s", name, m.RawJSON())
		}
		in := `{"unmodeled":true}`
		if err := json.Unmarshal([]byte(in), m); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(m.RawJSON()) != in {
			t.Errorf("%s.RawJSON() = %s, want %s", name, m.RawJSON(), in)
		}
	}
}
