// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package memory_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"latere.ai/x/sdk-go/latere"
	"latere.ai/x/sdk-go/memory"
)

const (
	projectID = "prj_01JA4Q0Z8X3K5V7M9N1P2R4T6W"
	checksum  = "9f2c4a1b7e3d5f60a8b9c0d1e2f30415263748596a7b8c9d0e1f2a3b4c5d6e7f"
	// one is a memory of You as a read of it answers, with a field the
	// package does not model.
	one = `{"scope":"you","id":"short-lists","name":"Short lists","description":"Keep lists under five things.","type":"preference",
"source":"ses_01JA3BQ7Z4M8N2P5R6S7T8V9WX","sensitive":true,"body":"Applies to every list, not only to steps.","checksum":"` + checksum + `","size":141,"added_later":1}`
	// inProject is a project's memory as a list answers it.
	inProject = `{"scope":"project","project":"` + projectID + `","id":"tide-sources","name":"Tide sources","description":"Uses the harbor's own tables.",
"type":"topic","source":"you","checksum":"41aa","size":120,"updated":"2026-10-29T10:41:07Z"}`
	list = `{"settings":{"remember":true,"chats":true,"sensitive":false,"review":"ask"},"memories":[` + one + `,` + inProject + `],
"projects":[{"id":"` + projectID + `","name":"Tide tables"}],"more":true,
"limits":{"max_name":100,"max_description":300,"max_bytes":8192,"max_import_lines":200,"types":["preference","profile","topic","area","person"]},
"import_prompt":"List everything you remember about me.","added_later":true}`
)

// A call is one request the fake API received.
type call struct {
	method, path, ifMatch, accept string
	// body is the request's JSON body, nil when it sent none.
	body map[string]any
}

// fakeAPI is a Client of a server that records each request and answers
// it with answer, which learns how many requests came before it. It
// allows retries, with no wait between them.
func fakeAPI(t *testing.T, retries int, answer func(w http.ResponseWriter, r *http.Request, n int)) (*memory.Client, func() []call) {
	t.Helper()
	var mu sync.Mutex
	var calls []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		c := call{method: r.Method, path: r.URL.EscapedPath(), ifMatch: r.Header.Get("If-Match"), accept: r.Header.Get("Accept")}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &c.body); err != nil {
				t.Errorf("%s %s sent a body that is no JSON object: %s", r.Method, r.URL.Path, raw)
			}
		}
		mu.Lock()
		n := len(calls)
		calls = append(calls, c)
		mu.Unlock()
		answer(w, r, n)
	}))
	t.Cleanup(srv.Close)
	cfg, err := latere.NewConfig(latere.WithBaseURL(srv.URL), latere.WithTokenSource(latere.StaticToken("tok")),
		latere.WithMaxRetries(retries), latere.WithBackoff(time.Millisecond, time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	return memory.New(cfg), func() []call {
		mu.Lock()
		defer mu.Unlock()
		return append([]call(nil), calls...)
	}
}

// answer writes body with status.
func answer(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

// refuse answers status with the API's envelope of code.
func refuse(w http.ResponseWriter, status int, code string) {
	answer(w, status, `{"error":{"code":"`+code+`","message":"No.","details":{"field":"description"}}}`)
}

// refusedWith returns the error of a list the API refuses with status and
// code, sent once.
func refusedWith(t *testing.T, status int, code string) error {
	t.Helper()
	c, _ := fakeAPI(t, 0, func(w http.ResponseWriter, r *http.Request, _ int) { refuse(w, status, code) })
	_, err := c.ListMemories(t.Context())
	return err
}

func TestListMemories(t *testing.T) {
	c, calls := fakeAPI(t, 0, func(w http.ResponseWriter, r *http.Request, _ int) { answer(w, http.StatusOK, list) })
	l, err := c.ListMemories(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got := calls(); len(got) != 1 || got[0].method != http.MethodGet || got[0].path != "/v1/account/memory" {
		t.Errorf("sent %+v", got)
	}
	if !l.Settings.Remember || !l.Settings.Chats || l.Settings.Sensitive || l.Settings.Review != memory.ReviewAsk || !l.More || len(l.Memories) != 2 || len(l.Projects) != 1 ||
		l.Projects[0].ID != projectID || l.Projects[0].Name != "Tide tables" || l.ImportPrompt == "" {
		t.Errorf("list %+v", l)
	}
	if lim := l.Limits; lim.MaxName != 100 || lim.MaxDescription != 300 || lim.MaxBytes != 8192 || lim.MaxImportLines != 200 ||
		len(lim.Types) != 5 || lim.Types[0] != memory.TypePreference || lim.Types[4] != memory.TypePerson {
		t.Errorf("limits %+v", lim)
	}
	you, p := l.Memories[0], l.Memories[1]
	if you.Scope != memory.ScopeYou || you.ID != "short-lists" || you.Type != memory.TypePreference || !you.Sensitive ||
		you.Checksum != checksum || you.Size != 141 || !you.Updated.IsZero() || string(you.RawJSON()) != one {
		t.Errorf("a memory of You: %+v with %.40s", you, you.RawJSON())
	}
	if p.Scope != memory.ScopeProject || p.Project != projectID || p.Type != memory.TypeTopic || p.Source != memory.SourceYou ||
		p.Body != "" || !p.Updated.Equal(time.Date(2026, 10, 29, 10, 41, 7, 0, time.UTC)) {
		t.Errorf("a project's memory: %+v", p)
	}
	if p.Ref() != (memory.Ref{Scope: memory.ScopeProject, Project: projectID, ID: "tide-sources"}) {
		t.Errorf("the project's memory's ref is %+v", p.Ref())
	}
	if !strings.Contains(string(l.RawJSON()), `"added_later":true`) {
		t.Errorf("the list kept %.60s", l.RawJSON())
	}
}

// A Ref addresses a memory of You or a project's, and one that names no
// id, or a scope its project does not fit, is refused before any request.
func TestARefAddressesItsScope(t *testing.T) {
	c, calls := fakeAPI(t, 0, func(w http.ResponseWriter, r *http.Request, _ int) { answer(w, http.StatusOK, one) })
	for ref, path := range map[memory.Ref]string{
		{ID: "short-lists"}:                                                  "/v1/account/memory/you/short-lists",
		{Scope: memory.ScopeYou, ID: "short-lists"}:                          "/v1/account/memory/you/short-lists",
		{Project: projectID, ID: "tide-sources"}:                             "/v1/account/memory/projects/" + projectID + "/tide-sources",
		{Scope: memory.ScopeProject, Project: projectID, ID: "a/b"}:          "/v1/account/memory/projects/" + projectID + "/a%2Fb",
		{Scope: memory.ScopeProject, Project: "prj_1/..", ID: "tide-s"}:      "/v1/account/memory/projects/prj_1%2F..",
		{Scope: memory.ScopeProject, Project: projectID, ID: "tide-sources"}: "/v1/account/memory/projects/" + projectID + "/tide-sources",
	} {
		before := len(calls())
		m, err := c.GetMemory(t.Context(), ref)
		if err != nil {
			t.Fatalf("%+v: %v", ref, err)
		}
		got := calls()[before]
		if got.method != http.MethodGet || !strings.HasPrefix(got.path, path) || m.Body == "" {
			t.Errorf("%+v: sent %s %s, want %s", ref, got.method, got.path, path)
		}
	}
	before := len(calls())
	for _, ref := range []memory.Ref{
		{},
		{Scope: memory.ScopeProject, ID: "tide-sources"},
		{Scope: memory.ScopeYou, Project: projectID, ID: "short-lists"},
		{Scope: "team", ID: "short-lists"},
	} {
		if _, err := c.GetMemory(t.Context(), ref); err == nil {
			t.Errorf("%+v was sent", ref)
		}
		if _, err := c.UpdateMemory(t.Context(), ref, nil); err == nil {
			t.Errorf("a change of %+v was sent", ref)
		}
		if err := c.DeleteMemory(t.Context(), ref, nil); err == nil {
			t.Errorf("a delete of %+v was sent", ref)
		}
		if _, err := c.RestoreMemory(t.Context(), ref); err == nil {
			t.Errorf("a restore of %+v was sent", ref)
		}
	}
	if n := len(calls()) - before; n != 0 {
		t.Errorf("%d refs that address nothing were sent", n)
	}
}

// A memory the scope does not hold is ErrNotFound; a project the person
// does not hold is latere.ErrNotFound alone.
func TestGetMemoryTellsAMemoryFromAProject(t *testing.T) {
	c, _ := fakeAPI(t, 0, func(w http.ResponseWriter, r *http.Request, _ int) {
		if strings.Contains(r.URL.Path, "/projects/") {
			refuse(w, http.StatusNotFound, "not_found")
			return
		}
		refuse(w, http.StatusNotFound, "memory_not_found")
	})
	_, err := c.GetMemory(t.Context(), memory.Ref{ID: "gone"})
	if !errors.Is(err, memory.ErrNotFound) || !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("a memory the scope does not hold: %v", err)
	}
	var apiErr *latere.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "memory_not_found" {
		t.Errorf("the error does not carry the API's answer: %v", err)
	}
	_, err = c.GetMemory(t.Context(), memory.Ref{Project: "prj_other", ID: "x"})
	if errors.Is(err, memory.ErrNotFound) || !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("a project the person does not hold: %v", err)
	}
}

func TestAddMemory(t *testing.T) {
	c, calls := fakeAPI(t, 3, func(w http.ResponseWriter, r *http.Request, n int) {
		switch n {
		case 0:
			answer(w, http.StatusCreated, one)
		case 1:
			refuse(w, http.StatusConflict, "memory_name_taken")
		default:
			refuse(w, http.StatusBadGateway, "unavailable")
		}
	})
	m, err := c.AddMemory(t.Context(), &memory.AddMemoryParams{Scope: memory.ScopeProject, Project: projectID, Type: memory.TypeTopic,
		Name: "Tide sources", Description: "Uses the harbor's own tables.", Body: "Not the national ones."})
	if err != nil {
		t.Fatal(err)
	}
	got := calls()[0]
	if got.method != http.MethodPost || got.path != "/v1/account/memory" || got.body["scope"] != "project" || got.body["project"] != projectID ||
		got.body["type"] != "topic" || got.body["name"] != "Tide sources" || got.body["description"] != "Uses the harbor's own tables." ||
		got.body["body"] != "Not the national ones." || m.ID != "short-lists" || string(m.RawJSON()) != one {
		t.Errorf("sent %+v, answered %+v", got, m)
	}
	if _, err := c.AddMemory(t.Context(), &memory.AddMemoryParams{Description: "Keep lists short."}); !errors.Is(err, memory.ErrNameTaken) || !errors.Is(err, latere.ErrConflict) {
		t.Errorf("a name no id is free for: %v", err)
	}
	if b := calls()[1].body; len(b) != 1 || b["description"] != "Keep lists short." {
		t.Errorf("a memory with a description alone sent %v", b)
	}
	before := len(calls())
	if _, err := c.AddMemory(t.Context(), &memory.AddMemoryParams{Description: "Keep lists short."}); err == nil {
		t.Error("a 502 succeeded")
	}
	if n := len(calls()) - before; n != 1 {
		t.Errorf("an add was sent %d times; it is sent once", n)
	}
	for _, p := range []*memory.AddMemoryParams{nil, {Description: " \n"}} {
		if _, err := c.AddMemory(t.Context(), p); err == nil {
			t.Errorf("%+v was added", p)
		}
	}
	if n := len(calls()) - before; n != 1 {
		t.Errorf("an add with no description was sent")
	}
}

func TestUpdateMemory(t *testing.T) {
	c, calls := fakeAPI(t, 2, func(w http.ResponseWriter, r *http.Request, n int) {
		switch {
		case n == 0:
			answer(w, http.StatusOK, one)
		case n == 1:
			refuse(w, http.StatusPreconditionFailed, "memory_changed")
		case n == 2:
			refuse(w, http.StatusRequestEntityTooLarge, "memory_too_large")
		default:
			refuse(w, http.StatusServiceUnavailable, "storage_unavailable")
		}
	})
	desc, body := "Keep lists under four things.", ""
	ref := memory.Ref{ID: "short-lists"}
	m, err := c.UpdateMemory(t.Context(), ref, &memory.UpdateMemoryParams{Description: &desc, Body: &body, Type: memory.TypeProfile, IfMatch: checksum})
	if err != nil || m.Checksum != checksum {
		t.Fatalf("%+v, %v", m, err)
	}
	got := calls()[0]
	if got.method != http.MethodPatch || got.path != "/v1/account/memory/you/short-lists" || got.ifMatch != `"`+checksum+`"` ||
		len(got.body) != 3 || got.body["description"] != desc || got.body["body"] != "" || got.body["type"] != "profile" {
		t.Errorf("sent %+v", got)
	}
	_, err = c.UpdateMemory(t.Context(), ref, &memory.UpdateMemoryParams{Description: &desc, IfMatch: `"stale"`})
	if !errors.Is(err, memory.ErrChanged) || !errors.Is(err, latere.ErrPreconditionFailed) || calls()[1].ifMatch != `"stale"` {
		t.Errorf("a change of a memory changed since: %v, If-Match %s", err, calls()[1].ifMatch)
	}
	if _, err := c.UpdateMemory(t.Context(), ref, &memory.UpdateMemoryParams{Body: &desc}); !errors.Is(err, memory.ErrTooLarge) {
		t.Errorf("a memory past its bound: %v", err)
	}
	if got := calls()[2]; got.ifMatch != "" || len(got.body) != 1 {
		t.Errorf("a change without IfMatch sent %+v", got)
	}

	// A change in place is retried; a move is sent once.
	before := len(calls())
	_, err = c.UpdateMemory(t.Context(), ref, &memory.UpdateMemoryParams{Description: &desc})
	if !errors.Is(err, memory.ErrStorageUnavailable) {
		t.Errorf("a change Storage could not take: %v", err)
	}
	if n := len(calls()) - before; n != 3 {
		t.Errorf("a change in place was sent %d times, want 3", n)
	}
	for _, move := range []*memory.UpdateMemoryParams{
		{Scope: memory.ScopeProject, Project: projectID},
		{Project: projectID},
		{Scope: memory.ScopeYou},
	} {
		before := len(calls())
		if _, err := c.UpdateMemory(t.Context(), ref, move); err == nil {
			t.Error("a 503 succeeded")
		}
		got := calls()
		if n := len(got) - before; n != 1 {
			t.Errorf("a move %+v was sent %d times; it is sent once", move, n)
		}
		if b := got[before].body; b["scope"] != nilIfEmpty(string(move.Scope)) || b["project"] != nilIfEmpty(move.Project) {
			t.Errorf("a move %+v sent %v", move, b)
		}
	}
	before = len(calls())
	if _, err := c.UpdateMemory(t.Context(), ref, nil); err == nil {
		t.Error("a 503 succeeded")
	}
	if got := calls()[before]; got.body == nil || len(got.body) != 0 {
		t.Errorf("a nil change sent %v", got.body)
	}
}

// nilIfEmpty is s as a decoded body holds it: absent when it is empty.
func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func TestDeleteMemory(t *testing.T) {
	c, calls := fakeAPI(t, 2, func(w http.ResponseWriter, r *http.Request, n int) {
		switch n {
		case 0, 1:
			w.WriteHeader(http.StatusNoContent)
		case 2:
			refuse(w, http.StatusServiceUnavailable, "storage_unavailable")
		default:
			refuse(w, http.StatusNotFound, "memory_not_found")
		}
	})
	ref := memory.Ref{Project: projectID, ID: "tide-sources"}
	if err := c.DeleteMemory(t.Context(), ref, &memory.DeleteOptions{IfMatch: "41aa"}); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteMemory(t.Context(), ref, nil); err != nil {
		t.Fatal(err)
	}
	// A retried delete whose first attempt landed meets ErrNotFound.
	if err := c.DeleteMemory(t.Context(), ref, &memory.DeleteOptions{}); !errors.Is(err, memory.ErrNotFound) {
		t.Errorf("a retried delete: %v", err)
	}
	got := calls()
	if len(got) != 4 || got[0].method != http.MethodDelete || got[0].path != "/v1/account/memory/projects/"+projectID+"/tide-sources" ||
		got[0].ifMatch != `"41aa"` || got[1].ifMatch != "" || got[2].ifMatch != "" || got[0].body != nil {
		t.Errorf("sent %+v", got)
	}
}

func TestForgetEverything(t *testing.T) {
	c, calls := fakeAPI(t, 1, func(w http.ResponseWriter, r *http.Request, n int) {
		if n == 0 {
			refuse(w, http.StatusServiceUnavailable, "storage_unavailable")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	if err := c.ForgetEverything(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := calls(); len(got) != 2 || got[1].method != http.MethodDelete || got[1].path != "/v1/account/memory" {
		t.Errorf("sent %+v; forgetting everything is retried", got)
	}
}

func TestUpdateSettings(t *testing.T) {
	c, calls := fakeAPI(t, 1, func(w http.ResponseWriter, r *http.Request, n int) {
		if n == 0 {
			refuse(w, http.StatusServiceUnavailable, "storage_unavailable")
			return
		}
		answer(w, http.StatusOK, `{"remember":true,"sensitive":false,"review":"ask","deleted_sensitive":3,"added_later":1}`)
	})
	off := false
	s, err := c.UpdateSettings(t.Context(), &memory.UpdateSettingsParams{Sensitive: &off, Review: memory.ReviewAsk})
	if err != nil {
		t.Fatal(err)
	}
	got := calls()
	if len(got) != 2 || got[1].method != http.MethodPatch || got[1].path != "/v1/account/memory/settings" ||
		len(got[1].body) != 2 || got[1].body["sensitive"] != false || got[1].body["review"] != "ask" {
		t.Errorf("sent %+v; a change of the settings is retried", got)
	}
	if !s.Remember || s.Sensitive || s.Review != memory.ReviewAsk || s.DeletedSensitive != 3 || !strings.Contains(string(s.RawJSON()), "added_later") {
		t.Errorf("settings %+v with %s", s, s.RawJSON())
	}
	if _, err := c.UpdateSettings(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if b := calls()[2].body; b == nil || len(b) != 0 {
		t.Errorf("a nil change sent %v", b)
	}
}

// TestSearchPastChats: a change of Search past chats sends that switch
// alone and reads it back from the answer.
func TestSearchPastChats(t *testing.T) {
	c, calls := fakeAPI(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) {
		answer(w, http.StatusOK, `{"remember":true,"chats":false,"sensitive":false,"deleted_sensitive":0}`)
	})
	off := false
	s, err := c.UpdateSettings(t.Context(), &memory.UpdateSettingsParams{Chats: &off})
	if err != nil {
		t.Fatal(err)
	}
	got := calls()
	if len(got) != 1 || got[0].method != http.MethodPatch || got[0].path != "/v1/account/memory/settings" ||
		len(got[0].body) != 1 || got[0].body["chats"] != false {
		t.Errorf("sent %+v", got)
	}
	if !s.Remember || s.Chats || s.Sensitive || s.DeletedSensitive != 0 {
		t.Errorf("settings %+v", s)
	}
}

func TestListForgottenAndRestore(t *testing.T) {
	c, calls := fakeAPI(t, 2, func(w http.ResponseWriter, r *http.Request, n int) {
		switch {
		case r.Method == http.MethodGet:
			answer(w, http.StatusOK, `{"forgotten":[{"scope":"project","project":"`+projectID+`","id":"trains","size":120,
"forgotten_at":"2026-10-29T11:02:00Z","purges_at":"2026-11-28T11:02:00Z"}]}`)
		case n == 1:
			answer(w, http.StatusOK, one)
		case n == 2:
			refuse(w, http.StatusConflict, "memory_taken")
		default:
			refuse(w, http.StatusBadGateway, "unavailable")
		}
	})
	fl, err := c.ListForgotten(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(fl.Forgotten) != 1 || string(fl.RawJSON()) == "" {
		t.Fatalf("forgotten %+v", fl)
	}
	f := fl.Forgotten[0]
	if f.Scope != memory.ScopeProject || f.ID != "trains" || f.Size != 120 || f.ForgottenAt.IsZero() || !f.PurgesAt.After(f.ForgottenAt) {
		t.Errorf("forgotten %+v", f)
	}
	if got := calls()[0]; got.method != http.MethodGet || got.path != "/v1/account/memory/forgotten" {
		t.Errorf("sent %+v", got)
	}
	m, err := c.RestoreMemory(t.Context(), f.Ref())
	if err != nil || m.ID != "short-lists" {
		t.Fatalf("%+v, %v", m, err)
	}
	if got := calls()[1]; got.method != http.MethodPost || got.path != "/v1/account/memory/forgotten/restore" ||
		len(got.body) != 3 || got.body["scope"] != "project" || got.body["project"] != projectID || got.body["id"] != "trains" {
		t.Errorf("sent %+v", got)
	}
	if _, err := c.RestoreMemory(t.Context(), memory.Ref{ID: "trains"}); !errors.Is(err, memory.ErrTaken) || !errors.Is(err, latere.ErrConflict) {
		t.Errorf("a memory of the same id saved since: %v", err)
	}
	if b := calls()[2].body; len(b) != 1 || b["id"] != "trains" {
		t.Errorf("a restore to You sent %v", b)
	}
	before := len(calls())
	if _, err := c.RestoreMemory(t.Context(), memory.Ref{ID: "trains"}); err == nil {
		t.Error("a 502 succeeded")
	}
	if n := len(calls()) - before; n != 1 {
		t.Errorf("a restore was sent %d times; it is sent once", n)
	}
}

func TestImportMemory(t *testing.T) {
	added := `{"added":[{"scope":"you","id":"mia-is-my-sister","name":"Mia is my sister","description":"Mia is my sister.","type":"person",
"source":"import","checksum":"5d6e","size":118}],"skipped":1}`
	c, calls := fakeAPI(t, 2, func(w http.ResponseWriter, r *http.Request, n int) {
		if n == 0 {
			refuse(w, http.StatusServiceUnavailable, "storage_unavailable")
			return
		}
		answer(w, http.StatusCreated, added)
	})
	text := "Preference: Keep lists under five things.\nPerson: Mia is my sister."
	params := &memory.ImportMemoryParams{Text: text, Scope: memory.ScopeProject, Project: projectID}
	if _, err := c.ImportMemory(t.Context(), params); !errors.Is(err, memory.ErrStorageUnavailable) {
		t.Errorf("an import Storage could not take: %v", err)
	}
	if n := len(calls()); n != 1 {
		t.Errorf("an import was sent %d times; it is sent once", n)
	}
	im, err := c.ImportMemory(t.Context(), params)
	if err != nil {
		t.Fatal(err)
	}
	got := calls()
	if len(got) != 2 || got[1].method != http.MethodPost || got[1].path != "/v1/account/memory/import" ||
		got[1].body["text"] != text || got[1].body["scope"] != "project" || got[1].body["project"] != projectID {
		t.Errorf("sent %+v", got)
	}
	if im.Skipped != 1 || len(im.Added) != 1 || im.Added[0].Source != memory.SourceImport || im.Added[0].Type != memory.TypePerson ||
		string(im.RawJSON()) != added || string(im.Added[0].RawJSON()) == "" {
		t.Errorf("imported %+v", im)
	}
	for _, p := range []*memory.ImportMemoryParams{nil, {Text: "\n \t"}} {
		if _, err := c.ImportMemory(t.Context(), p); err == nil {
			t.Errorf("%+v was imported", p)
		}
	}
	if n := len(calls()); n != 2 {
		t.Errorf("an import with no text was sent")
	}
}

// A token of an organization's context, or an agent's, is refused with
// the sentinel of its code, which also matches latere.ErrForbidden, on
// every call.
func TestTheTokensMemoryRefuses(t *testing.T) {
	text, ref := "Keep lists short.", memory.Ref{ID: "short-lists"}
	for code, sentinel := range map[string]error{"memory_personal_only": memory.ErrPersonalOnly, "memory_agent_token": memory.ErrAgentToken} {
		c, _ := fakeAPI(t, 0, func(w http.ResponseWriter, r *http.Request, _ int) { refuse(w, http.StatusForbidden, code) })
		ctx := t.Context()
		for name, call := range map[string]func() error{
			"ListMemories": func() error { _, err := c.ListMemories(ctx); return err },
			"AddMemory":    func() error { _, err := c.AddMemory(ctx, &memory.AddMemoryParams{Description: text}); return err },
			"GetMemory":    func() error { _, err := c.GetMemory(ctx, ref); return err },
			"UpdateMemory": func() error {
				_, err := c.UpdateMemory(ctx, ref, &memory.UpdateMemoryParams{Description: &text})
				return err
			},
			"DeleteMemory":     func() error { return c.DeleteMemory(ctx, ref, nil) },
			"ForgetEverything": func() error { return c.ForgetEverything(ctx) },
			"UpdateSettings":   func() error { _, err := c.UpdateSettings(ctx, nil); return err },
			"ListForgotten":    func() error { _, err := c.ListForgotten(ctx); return err },
			"RestoreMemory":    func() error { _, err := c.RestoreMemory(ctx, ref); return err },
			"ExportMemory":     func() error { _, err := c.ExportMemory(ctx); return err },
			"ImportMemory":     func() error { _, err := c.ImportMemory(ctx, &memory.ImportMemoryParams{Text: text}); return err },
		} {
			err := call()
			if !errors.Is(err, sentinel) || !errors.Is(err, latere.ErrForbidden) {
				t.Errorf("%s, %s: %v", name, code, err)
			}
			var apiErr *latere.Error
			if !errors.As(err, &apiErr) || apiErr.Code != code || err.Error() != apiErr.Error() {
				t.Errorf("%s, %s: the error does not read as the API's answer: %v", name, code, err)
			}
		}
	}
	// A code with no sentinel is the API's error as it is.
	err := refusedWith(t, http.StatusBadRequest, "bad_request")
	if _, ok := err.(*latere.Error); !ok || errors.Is(err, memory.ErrInvalidField) {
		t.Errorf("bad_request: %T %v", err, err)
	}
}

// A call with a Config not built with latere.NewConfig fails, as every
// package's does.
func TestAClientOfNoConfigFails(t *testing.T) {
	c := memory.New(nil)
	if _, err := c.ListMemories(t.Context()); err == nil {
		t.Error("a list with no Config succeeded")
	}
	if _, err := c.ExportMemory(t.Context()); err == nil {
		t.Error("an export with no Config succeeded")
	}
}
