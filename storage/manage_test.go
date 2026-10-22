// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package storage_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"latere.ai/x/sdk-go/latere"
	"latere.ai/x/sdk-go/storage"
)

// recorded is one request a test server saw.
type recorded struct {
	method, path, query string
	body                map[string]any
}

// recorder answers every request with status and answer, and records it.
func recorder(t *testing.T, status int, answer string) (*httptest.Server, *[]recorded) {
	t.Helper()
	var seen []recorded
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := recorded{method: r.Method, path: r.URL.EscapedPath(), query: r.URL.RawQuery}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &rec.body); err != nil {
				t.Errorf("a body that is not JSON: %s", raw)
			}
		}
		seen = append(seen, rec)
		if answer == "" {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, answer)
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func TestMoveFileSendsTheDestinationAndReadsTheFile(t *testing.T) {
	srv, seen := recorder(t, http.StatusOK, file)
	f, err := newClient(t, srv).MoveFile(context.Background(), storage.Me, "files/notes/old name.md", "files/notes/today.md")
	if err != nil {
		t.Fatal(err)
	}
	if f.Path != "files/notes/today.md" || string(f.RawJSON()) != file {
		t.Errorf("file = %+v, RawJSON = %s", f, f.RawJSON())
	}
	got := (*seen)[0]
	if got.method != http.MethodPost || got.path != "/v1/storage/files/me/files/notes/old%20name.md" ||
		len(got.body) != 1 || got.body["move_to"] != "files/notes/today.md" {
		t.Errorf("sent %+v", got)
	}
}

func TestMoveFolderMovesEverythingUnderIt(t *testing.T) {
	const moved = `{"path":"files/archive/2026","files":3,"bytes":1200}`
	srv, seen := recorder(t, http.StatusOK, moved)
	m, err := newClient(t, srv).MoveFolder(context.Background(), storage.Me, "files/2026", "files/archive/2026")
	if err != nil {
		t.Fatal(err)
	}
	if m.Path != "files/archive/2026" || m.Files != 3 || m.Bytes != 1200 || string(m.RawJSON()) != moved {
		t.Errorf("moved = %+v, RawJSON = %s", m, m.RawJSON())
	}
	got := (*seen)[0]
	if got.method != http.MethodPost || got.path != "/v1/storage/files/me/files/2026" ||
		got.body["move_to"] != "files/archive/2026" || got.body["recursive"] != true {
		t.Errorf("sent %+v", got)
	}
}

// A move is sent once: the API takes no key, and a repeat would find the
// file gone.
func TestAMoveIsNeverRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"error":{"code":"authorizer_unavailable","message":"Retry shortly."}}`)
	}))
	defer srv.Close()
	cfg, err := latere.NewConfig(latere.WithBaseURL(srv.URL), latere.WithTokenSource(latere.StaticToken("key_test")),
		latere.WithMaxRetries(3), latere.WithBackoff(time.Millisecond, time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	c := storage.New(cfg)
	if _, err := c.MoveFile(context.Background(), storage.Me, "files/a.txt", "files/b.txt"); err == nil {
		t.Error("a refused move returned no error")
	}
	if _, err := c.MoveFolder(context.Background(), storage.Me, "files/a", "files/b"); err == nil {
		t.Error("a refused folder move returned no error")
	}
	if n := calls.Load(); n != 2 {
		t.Errorf("sent %d requests for two moves, want each once", n)
	}
}

func TestAMoveToATakenPathIsAConflict(t *testing.T) {
	srv, _ := recorder(t, http.StatusConflict, `{"error":{"code":"path_taken","message":"Something already exists at that path."}}`)
	_, err := newClient(t, srv).MoveFile(context.Background(), storage.Me, "files/a.txt", "files/b.txt")
	var apiErr *latere.Error
	if !errors.Is(err, latere.ErrConflict) || !errors.As(err, &apiErr) || apiErr.Code != "path_taken" {
		t.Errorf("err = %v, want path_taken", err)
	}
}

func TestAMoveNeedsBothPaths(t *testing.T) {
	srv, seen := recorder(t, http.StatusOK, file)
	c := newClient(t, srv)
	ctx := context.Background()
	if _, err := c.MoveFile(ctx, storage.Me, "files/a.txt", ""); err == nil {
		t.Error("a move to nowhere was sent")
	}
	if _, err := c.MoveFolder(ctx, storage.Me, "", "files/b"); err == nil {
		t.Error("a move of no folder was sent")
	}
	if len(*seen) != 0 {
		t.Errorf("sent %d requests", len(*seen))
	}
}

func TestDeletesTrashOrRemoveAFileOrAFolder(t *testing.T) {
	srv, seen := recorder(t, http.StatusNoContent, "")
	c := newClient(t, srv)
	ctx := context.Background()
	for _, err := range []error{
		c.DeleteFile(ctx, storage.Me, "files/a b.txt", nil),
		c.DeleteFile(ctx, storage.Me, "files/a.txt", &storage.DeleteOptions{Permanent: true}),
		c.DeleteFolder(ctx, storage.Me, "files/notes", nil),
		c.DeleteFolder(ctx, storage.Me, "files/notes", &storage.DeleteOptions{Permanent: true}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	want := []recorded{
		{method: http.MethodDelete, path: "/v1/storage/files/me/files/a%20b.txt"},
		{method: http.MethodDelete, path: "/v1/storage/files/me/files/a.txt", query: "permanent=1"},
		{method: http.MethodDelete, path: "/v1/storage/files/me/files/notes", query: "recursive=1"},
		{method: http.MethodDelete, path: "/v1/storage/files/me/files/notes", query: "permanent=1&recursive=1"},
	}
	for i, w := range want {
		if got := (*seen)[i]; got.method != w.method || got.path != w.path || got.query != w.query || got.body != nil {
			t.Errorf("delete %d sent %+v, want %+v", i, got, w)
		}
	}
	if err := c.DeleteFile(ctx, storage.Me, "", nil); err == nil {
		t.Error("a delete of no path was sent")
	}
}

func TestADeleteOfNothingIsNotFound(t *testing.T) {
	srv, _ := recorder(t, http.StatusNotFound, `{"error":{"code":"not_found","message":"There is no such object."}}`)
	if err := newClient(t, srv).DeleteFolder(context.Background(), storage.Me, "files/none", nil); !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

const grant = `{"id":"shr_1","owner":"https://auth.example/u/ada","path_prefix":"files/talks","grantee_kind":"subject",
"grantee":"https://auth.example/u/bob","permission":"write","status":"active","created_by":"https://auth.example/u/ada",
"created_at":"2026-10-20T09:00:00Z","expires_at":"2026-11-20T09:00:00Z"}`

func TestListSharedWithMeWalksEveryPage(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/storage/shares/with-me" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		queries = append(queries, r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") == "" {
			fmt.Fprint(w, `{"entries":[`+grant+`],"next_cursor":"c2"}`)
			return
		}
		fmt.Fprint(w, `{"entries":[]}`)
	}))
	defer srv.Close()
	n := 0
	for g, err := range newClient(t, srv).ListSharedWithMe(context.Background(), &storage.ListSharedParams{Limit: 50, Cursor: "ignored"}) {
		if err != nil {
			t.Fatal(err)
		}
		n++
		if g.ID != "shr_1" || g.Owner != "https://auth.example/u/ada" || g.PathPrefix != "files/talks" ||
			g.GranteeKind != storage.GranteeSubject || g.Permission != storage.PermissionWrite || g.Status != storage.GrantActive ||
			!g.CreatedAt.Equal(time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)) || g.ExpiresAt.IsZero() {
			t.Errorf("grant = %+v", g)
		}
		if !strings.Contains(string(g.RawJSON()), `"path_prefix":"files/talks"`) {
			t.Errorf("RawJSON = %s", g.RawJSON())
		}
	}
	if n != 1 || len(queries) != 2 || queries[0] != "limit=50" || queries[1] != "cursor=c2&limit=50" {
		t.Errorf("read %d grants with the queries %q", n, queries)
	}
}

func TestListSharedWithMePageStartsAtTheCursor(t *testing.T) {
	srv, seen := recorder(t, http.StatusOK, `{"entries":[`+grant+`],"next_cursor":"c3"}`)
	page, err := newClient(t, srv).ListSharedWithMePage(context.Background(), &storage.ListSharedParams{Cursor: "c2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Grants) != 1 || page.NextCursor != "c3" || (*seen)[0].query != "cursor=c2" {
		t.Errorf("page = %+v after the query %q", page, (*seen)[0].query)
	}
	if _, err := newClient(t, srv).ListSharedWithMePage(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if (*seen)[1].query != "" {
		t.Errorf("a first page sent the query %q", (*seen)[1].query)
	}
}
