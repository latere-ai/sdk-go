// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package account_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"latere.ai/x/sdk-go/account"
	"latere.ai/x/sdk-go/latere"
)

const preferences = `{"network":{"mode":"listed","domains":[],"managed_by":"you","editable":true,"modes":["listed","ask"],"package_managers":[],"max_domains":200},
"sharing":{"mode":"anyone","managed_by":"you","editable":true,"modes":["off","anyone"]},
"profile":{"call_me":"Mara","work":"","works":["research"],"max_call_me":60},"instructions":{"text":"","max_chars":4000}}`

func TestUpdatePreferences(t *testing.T) {
	c, calls := capturingAPI(t, 1, func(w http.ResponseWriter, r *http.Request, n int) {
		if n == 0 {
			answerUnavailable(w)
			return
		}
		answerJSON(w, http.StatusOK, preferences)
	})
	listed, mara, none, on := "listed", "Mara", "", true
	p, err := c.UpdatePreferences(t.Context(), &account.UpdatePreferencesParams{
		NetworkMode: &listed, CallMe: &mara, Work: &none, Instructions: &none, ZeroRetention: &on,
	})
	if err != nil || p.Profile.CallMe != "Mara" || p.Network.Mode != "listed" {
		t.Fatalf("UpdatePreferences: %+v, %v", p, err)
	}
	sent := calls()
	if len(sent) != 2 {
		t.Fatalf("sent %d requests; a merge patch is retried", len(sent))
	}
	got := sent[1]
	want := `{"data":{"zero_retention":true},"instructions":{"text":null},"network":{"mode":"listed"},"profile":{"call_me":"Mara","work":null}}`
	if got.Method != http.MethodPatch || got.Path != "/v1/account/preferences" || got.RawBody != want {
		t.Errorf("sent %s %s %s; want PATCH with %s", got.Method, got.Path, got.RawBody, want)
	}

	off := false
	if _, err := c.UpdatePreferences(t.Context(), &account.UpdatePreferencesParams{SharingMode: &none, ZeroRetention: &off}); err != nil {
		t.Fatal(err)
	}
	if got := calls()[2].RawBody; got != `{"data":{"zero_retention":false},"sharing":{"mode":null}}` {
		t.Errorf("a return to the defaults sent %s", got)
	}
}

func TestAPreferencesChangeOfNothingIsRefused(t *testing.T) {
	c, calls := capturingAPI(t, 0, func(w http.ResponseWriter, r *http.Request, n int) { answerJSON(w, http.StatusOK, preferences) })
	for _, p := range []*account.UpdatePreferencesParams{nil, {}} {
		if _, err := c.UpdatePreferences(t.Context(), p); err == nil {
			t.Errorf("UpdatePreferences(%+v) succeeded", p)
		}
	}
	if n := len(calls()); n != 0 {
		t.Errorf("sent %d requests, want none", n)
	}
}

func TestSites(t *testing.T) {
	network := `{"mode":"ask","domains":["*.wikipedia.org"],"managed_by":"you","editable":true,"modes":["ask"],"package_managers":[],"max_domains":200,"added_later":1}`
	c, calls := capturingAPI(t, 0, func(w http.ResponseWriter, r *http.Request, n int) {
		switch r.Method {
		case http.MethodPut:
			answerJSON(w, http.StatusCreated, network)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	n, err := c.AddSite(t.Context(), "*.wikipedia.org")
	if err != nil || n.Domains[0] != "*.wikipedia.org" || n.MaxDomains != 200 || string(n.RawJSON()) != network {
		t.Fatalf("AddSite: %+v, %v", n, err)
	}
	if err := c.RemoveSite(t.Context(), "*.wikipedia.org"); err != nil {
		t.Fatal(err)
	}
	sent := calls()
	if sent[0].Method != http.MethodPut || sent[0].Path != "/v1/account/preferences/network/domains/%2A.wikipedia.org" || sent[0].RawBody != "" ||
		sent[1].Method != http.MethodDelete || sent[1].Path != sent[0].Path {
		t.Errorf("requests: %+v", sent)
	}
	if _, err := c.AddSite(t.Context(), ""); err == nil {
		t.Error("AddSite with no site succeeded")
	}
	if err := c.RemoveSite(t.Context(), ""); err == nil {
		t.Error("RemoveSite with no site succeeded")
	}
	if len(calls()) != 2 {
		t.Errorf("an empty site was sent")
	}
}

func TestBookmarks(t *testing.T) {
	bookmarks := `{"sessions":{"ses_1":[12,87]},"max_per_session":50,"max_sessions":100}`
	c, calls := capturingAPI(t, 0, func(w http.ResponseWriter, r *http.Request, n int) {
		switch {
		case r.URL.Path == "/v1/account/preferences/bookmarks/ses_full/9":
			answerJSON(w, http.StatusBadRequest, `{"error":{"code":"ceiling_exceeded","message":"Full."}}`)
		case r.Method == http.MethodPut:
			answerJSON(w, http.StatusOK, bookmarks)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	b, err := c.BookmarkTurn(t.Context(), "ses_1", 87)
	if err != nil || b.Sessions["ses_1"][1] != 87 || b.MaxPerSession != 50 || string(b.RawJSON()) != bookmarks {
		t.Fatalf("BookmarkTurn: %+v, %v", b, err)
	}
	if err := c.UnbookmarkTurn(t.Context(), "ses_1", 87); err != nil {
		t.Fatal(err)
	}
	var apiErr *latere.Error
	if _, err := c.BookmarkTurn(t.Context(), "ses_full", 9); !errors.As(err, &apiErr) || apiErr.Code != "ceiling_exceeded" {
		t.Errorf("a bookmark past the ceiling: %v", err)
	}
	sent := calls()
	if sent[0].Method != http.MethodPut || sent[0].Path != "/v1/account/preferences/bookmarks/ses_1/87" ||
		sent[1].Method != http.MethodDelete || sent[1].Path != sent[0].Path {
		t.Errorf("requests: %+v", sent)
	}
	for name, err := range map[string]error{
		"no session": second(c.BookmarkTurn(t.Context(), "", 1)),
		"seq 0":      second(c.BookmarkTurn(t.Context(), "ses_1", 0)),
		"seq -1":     c.UnbookmarkTurn(t.Context(), "ses_1", -1),
		"unmark":     c.UnbookmarkTurn(t.Context(), "", 1),
	} {
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if len(calls()) != 3 {
		t.Errorf("sent %d requests; an invalid bookmark was sent", len(calls()))
	}
}

func TestPinsOfAppsAndProjects(t *testing.T) {
	pins := `{"sessions":[],"apps":["app_1"],"projects":["prj_1"],"max_sessions":20,"max_apps":20,"max_projects":20}`
	c, calls := capturingAPI(t, 0, func(w http.ResponseWriter, r *http.Request, n int) {
		if r.Method == http.MethodPut {
			answerJSON(w, http.StatusCreated, pins)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	app, err := c.PinApp(t.Context(), "app_1")
	if err != nil || app.Apps[0] != "app_1" {
		t.Errorf("PinApp: %+v, %v", app, err)
	}
	project, err := c.PinProject(t.Context(), "prj_1")
	if err != nil || project.Projects[0] != "prj_1" || project.MaxProjects != 20 {
		t.Errorf("PinProject: %+v, %v", project, err)
	}
	if err := c.UnpinApp(t.Context(), "app_1"); err != nil {
		t.Error(err)
	}
	if err := c.UnpinProject(t.Context(), "prj_1"); err != nil {
		t.Error(err)
	}
	want := []string{
		"PUT /v1/account/preferences/pins/apps/app_1",
		"PUT /v1/account/preferences/pins/projects/prj_1",
		"DELETE /v1/account/preferences/pins/apps/app_1",
		"DELETE /v1/account/preferences/pins/projects/prj_1",
	}
	sent := calls()
	for i, w := range want {
		if got := sent[i].Method + " " + sent[i].Path; got != w {
			t.Errorf("request %d is %s, want %s", i, got, w)
		}
	}
	for name, err := range map[string]error{
		"PinApp":       second(c.PinApp(t.Context(), "")),
		"PinProject":   second(c.PinProject(t.Context(), "")),
		"UnpinApp":     c.UnpinApp(t.Context(), ""),
		"UnpinProject": c.UnpinProject(t.Context(), ""),
	} {
		if err == nil {
			t.Errorf("%s with no id: no error", name)
		}
	}
	if len(calls()) != len(want) {
		t.Errorf("an empty id was sent")
	}
}

// A preferences read keeps the JSON of the sections a write of one of them
// answers, so a program relays a section as the API answered it.
func TestPreferencesSectionsKeepTheirJSON(t *testing.T) {
	var p account.Preferences
	raw := `{"network":{"mode":"ask","added_later":true},"sharing":{"mode":"off"},"profile":{},"instructions":{},"bookmarks":{"sessions":{},"later":1}}`
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	if string(p.Network.RawJSON()) != `{"mode":"ask","added_later":true}` || string(p.Bookmarks.RawJSON()) != `{"sessions":{},"later":1}` {
		t.Errorf("network %s, bookmarks %s", p.Network.RawJSON(), p.Bookmarks.RawJSON())
	}
}
