// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package apps_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"latere.ai/x/sdk-go/apps"
	"latere.ai/x/sdk-go/latere"
)

func TestListAppSessionsPages(t *testing.T) {
	c, f := newFake(t, 0, map[string]http.HandlerFunc{
		"GET /v1/apps/apps/tide-tables/sessions": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("cursor") == "" {
				answer(http.StatusOK, `{"items":[{"id":"ses_2","first_published_at":"2026-10-07T08:14:02Z","last_published_at":"2026-10-07T09:40:51Z","deploys":4,"released":true,"commits":7}],"has_more":true,"next_cursor":"c2"}`)(w, r)
				return
			}
			// The session the app was created in, which no deploy names.
			answer(http.StatusOK, `{"items":[{"id":"ses_1","first_published_at":null,"last_published_at":null,"deploys":0,"released":false}],"has_more":false,"next_cursor":null}`)(w, r)
		},
	})
	var got []*apps.AppSession
	for s, err := range c.ListAppSessions(t.Context(), "tide-tables", &apps.ListAppSessionsParams{Limit: 1}) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, s)
	}
	if len(got) != 2 || got[0].ID != "ses_2" || got[0].Deploys != 4 || !got[0].Released ||
		!got[0].FirstPublishedAt.Equal(time.Date(2026, 10, 7, 8, 14, 2, 0, time.UTC)) ||
		got[1].ID != "ses_1" || !got[1].FirstPublishedAt.IsZero() || !got[1].LastPublishedAt.IsZero() {
		t.Fatalf("listed %+v", got)
	}
	if !strings.Contains(string(got[0].RawJSON()), `"commits":7`) {
		t.Errorf("RawJSON = %s, want the row as sent", got[0].RawJSON())
	}
	calls := f.record()
	if len(calls) != 2 || calls[0].Query != "limit=1" || calls[1].Query != "cursor=c2&limit=1" {
		t.Errorf("queries = %+v", calls)
	}

	page, err := c.ListAppSessionsPage(t.Context(), "tide-tables", &apps.ListAppSessionsParams{Cursor: "c2"})
	if err != nil || len(page.Items) != 1 || page.NextCursor != "" {
		t.Errorf("ListAppSessionsPage = %+v, %v", page, err)
	}
	if _, err := c.ListAppSessionsPage(t.Context(), "", nil); err == nil {
		t.Error("an empty slug was sent")
	}
	if n := len(f.record()); n != 3 {
		t.Errorf("made %d requests, want 3", n)
	}
}

const visitsJSON = `{"counted_since":"2026-10-13","days":[{"day":"2026-11-11","final":true,"visitors":14,"visits":31},
	{"day":"2026-11-12","final":false,"visitors":5,"visits":12}],"id":"314317e6","slug":"kornhaus","time_zone":"UTC",
	"today":"2026-11-12","totals":{"last_30_days":{"visitors":230,"visits":512},"last_7_days":{"visitors":61,"visits":140},
	"today":{"visitors":5,"visits":12}}}`

func TestGetAppVisits(t *testing.T) {
	c, f := newFake(t, 0, map[string]http.HandlerFunc{
		"GET /v1/apps/apps/kornhaus/visits": answer(http.StatusOK, visitsJSON),
	})
	v, err := c.GetAppVisits(t.Context(), "kornhaus", 7)
	if err != nil {
		t.Fatal(err)
	}
	if v.Slug != "kornhaus" || v.Today != "2026-11-12" || v.TimeZone != "UTC" || v.CountedSince != "2026-10-13" ||
		v.Totals.Last30Days.Visits != 512 || v.Totals.Last7Days.Visitors != 61 || v.Totals.Today.Visits != 12 ||
		len(v.Days) != 2 || !v.Days[0].Final || v.Days[1].Final || v.Days[1].Visitors != 5 || len(v.RawJSON()) == 0 {
		t.Errorf("GetAppVisits = %+v", v)
	}
	if _, err := c.GetAppVisits(t.Context(), "kornhaus", 0); err != nil {
		t.Fatal(err)
	}
	for _, days := range []int{-1, apps.MaxVisitDays + 1} {
		if _, err := c.GetAppVisits(t.Context(), "kornhaus", days); err == nil {
			t.Errorf("days %d was sent", days)
		}
	}
	calls := f.record()
	if len(calls) != 2 || calls[0].Query != "days=7" || calls[1].Query != "" {
		t.Errorf("requests = %+v, want days=7 and then no query", calls)
	}
}

func TestDeploysByID(t *testing.T) {
	const id = "9f2c6a1e-2b7d-4c4f-9e0a-5d1b7f3c8a21"
	c, f := newFake(t, 0, map[string]http.HandlerFunc{
		"GET /v1/apps/deploys/" + id:          answer(http.StatusOK, `{"id":"`+id+`","seq":3,"kind":"static","status":"live"}`),
		"GET /v1/apps/deploys/" + id + "/app": answer(http.StatusOK, `{"id":"5c1e","slug":"tides","name":"Tide tables","url":"https://tides.latere.site","icon":"t"}`),
	})
	d, err := c.GetDeployByID(t.Context(), id)
	if err != nil || d.ID != id || d.Status != apps.DeployLive {
		t.Fatalf("GetDeployByID = %+v, %v", d, err)
	}
	a, err := c.GetDeployApp(t.Context(), id)
	if err != nil || a.Slug != "tides" || a.Name != "Tide tables" || a.URL != "https://tides.latere.site" ||
		!strings.Contains(string(a.RawJSON()), `"icon":"t"`) {
		t.Fatalf("GetDeployApp = %+v, %v", a, err)
	}
	if _, err := c.GetDeployApp(t.Context(), "nope"); !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("an unknown deploy: %v, want ErrNotFound", err)
	}
	if _, err := c.GetDeployByID(t.Context(), ""); err == nil {
		t.Error("an empty id was sent")
	}
	if _, err := c.GetDeployApp(t.Context(), ""); err == nil {
		t.Error("an empty id was sent")
	}
	// An id is one path segment, escaped.
	if _, err := c.GetDeployByID(t.Context(), "a/b"); !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("a/b: %v", err)
	}
	calls := f.record()
	if len(calls) != 4 || calls[3].Path != "/v1/apps/deploys/a%2Fb" {
		t.Errorf("requests = %+v", calls)
	}
}
