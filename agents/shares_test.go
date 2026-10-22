// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents_test

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"

	"latere.ai/x/sdk-go/agents"
	"latere.ai/x/sdk-go/latere"
)

const sharedEvent = `{"id":"evt_2","seq":2,"type":"agent.message","time":"2026-10-20T09:00:05Z","turn":1,"payload":{"message":{"role":"assistant","blocks":[{"type":"text","text":"Hello."}]},"stop_reason":"end_turn"}}`

const sharedPage = `{"share":{"title":"Release notes","session_created_at":"2026-10-20T09:00:00Z","shared_at":"2026-10-20T10:00:00Z",
"updated_at":"2026-10-20T11:00:00Z","up_to_seq":9},"events":[` + sharedEvent + `],"next_cursor":"c2"}`

// A shared page names its share and the events a reader sees, with the
// answer's JSON kept.
func TestGetSharedPage(t *testing.T) {
	c, calls := fakeAPI(t, map[string]string{"GET /v1/shares/tok_1": sharedPage})
	page, err := c.GetSharedPage(t.Context(), "tok_1", &agents.SharedPageParams{Cursor: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	s := page.Share
	if s.Title != "Release notes" || s.UpToSeq != 9 || !s.SharedAt.Equal(time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)) ||
		s.SessionCreatedAt.IsZero() || s.UpdatedAt.IsZero() {
		t.Errorf("the share reads %+v", s)
	}
	if len(page.Events) != 1 || page.Events[0].Type != agents.EventAgentMessage || page.Events[0].Turn != 1 ||
		string(page.Events[0].RawJSON()) != sharedEvent || page.NextCursor != "c2" {
		t.Errorf("the page reads %+v", page)
	}
	var msg agents.AgentMessage
	if err := page.Events[0].Decode(&msg); err != nil || msg.Text() != "Hello." {
		t.Errorf("the event decodes to %+v, %v", msg, err)
	}
	if string(page.RawJSON()) != sharedPage {
		t.Errorf("RawJSON = %s; want the answer", page.RawJSON())
	}
	if (*calls)[0].Query != "cursor=c1" {
		t.Errorf("sent the query %q", (*calls)[0].Query)
	}
	if _, err := c.GetSharedPage(t.Context(), "tok_1", nil); err != nil || (*calls)[1].Query != "" {
		t.Errorf("a first page: %v, the query %q", err, (*calls)[1].Query)
	}
}

// The events of a share are read page after page, and a stopped link is
// not found.
func TestListSharedEvents(t *testing.T) {
	var cursors []string
	c := fileClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/shares/tok_1" {
			refuse(w, http.StatusNotFound, "share_not_found", `{}`)
			return
		}
		cursors = append(cursors, r.URL.Query().Get("cursor"))
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") == "" {
			_, _ = io.WriteString(w, sharedPage)
			return
		}
		_, _ = io.WriteString(w, `{"share":{"title":"Release notes","up_to_seq":9},"events":[]}`)
	}, 0)
	n := 0
	for e, err := range c.ListSharedEvents(t.Context(), "tok_1") {
		if err != nil {
			t.Fatal(err)
		}
		if e.Seq != 2 {
			t.Errorf("read the event %+v", e)
		}
		n++
	}
	if n != 1 || len(cursors) != 2 || cursors[1] != "c2" {
		t.Errorf("read %d events over the cursors %q", n, cursors)
	}
	for _, err := range c.ListSharedEvents(t.Context(), "tok_gone") {
		if !errors.Is(err, latere.ErrNotFound) {
			t.Errorf("a stopped link: %v, want ErrNotFound", err)
		}
	}
	if _, err := c.GetSharedPage(t.Context(), "", nil); err == nil {
		t.Error("a read of no token was sent")
	}
}

// A file the share names is read as its bytes, with its type and length,
// by an escaped digest.
func TestReadSharedBlob(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n")
	c := fileClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/v1/shares/tok_1/blobs/sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08" {
			refuse(w, http.StatusNotFound, "share_not_found", `{}`)
			return
		}
		if r.Header.Get("Accept") != "*/*" {
			t.Errorf("Accept = %q", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", strconv.Itoa(len(png)))
		_, _ = w.Write(png)
	}, 0)
	f, err := c.ReadSharedBlob(t.Context(), "tok_1", digest)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(f)
	if err := errors.Join(err, f.Close()); err != nil {
		t.Fatal(err)
	}
	if string(b) != string(png) || f.MediaType() != "image/png" || f.Size() != int64(len(png)) {
		t.Errorf("read %q as %s of %d bytes", b, f.MediaType(), f.Size())
	}
	if _, err := c.ReadSharedBlob(t.Context(), "tok_1", "sha256:0000"); !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("a digest the share does not name: %v, want ErrNotFound", err)
	}
	for _, bad := range []struct{ token, digest string }{{"", digest}, {"tok_1", ".."}, {"tok_1", "sha256:"}} {
		if _, err := c.ReadSharedBlob(t.Context(), bad.token, bad.digest); err == nil || errors.Is(err, latere.ErrNotFound) {
			t.Errorf("ReadSharedBlob(%q, %q) = %v; want a refusal before any request", bad.token, bad.digest, err)
		}
	}
}
