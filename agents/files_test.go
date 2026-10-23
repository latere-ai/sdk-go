// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"latere.ai/x/sdk-go/agents"
	"latere.ai/x/sdk-go/latere"
)

// The digest of a picture the session kept.
const digest = "sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"

// fileClient is a Client of a server h answers, with no retries and the
// given timeout.
func fileClient(t *testing.T, h http.HandlerFunc, timeout time.Duration) *agents.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	cfg, err := latere.NewConfig(latere.WithBaseURL(srv.URL), latere.WithTokenSource(latere.StaticToken("key_test")),
		latere.WithMaxRetries(0), latere.WithTimeout(timeout))
	if err != nil {
		t.Fatal(err)
	}
	return agents.New(cfg)
}

// refuse answers the API's error envelope.
func refuse(w http.ResponseWriter, status int, code, details string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Request-Id", "req_1")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, `{"error":{"code":"`+code+`","message":"No.","details":`+details+`}}`)
}

func TestReadBlob(t *testing.T) {
	png := "\x89PNG\r\n\x1a\nrest"
	var path, accept string
	c := fileClient(t, func(w http.ResponseWriter, r *http.Request) {
		path, accept = r.URL.EscapedPath(), r.Header.Get("Accept")
		switch r.URL.Path {
		case "/v1/agents/sessions/ses_1/blobs/" + digest:
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Content-Length", strconv.Itoa(len(png)))
			_, _ = io.WriteString(w, png)
		case "/v1/agents/sessions/ses_1/blobs/sha256:report":
			// A kept file that is no picture: its own type and length, as
			// a download.
			w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
			w.Header().Set("Content-Length", "8")
			w.Header().Set("Content-Disposition", "attachment")
			_, _ = io.WriteString(w, "# Report")
		case "/v1/agents/sessions/ses_1/blobs/sha256:streamed":
			// An answer that states no length.
			w.Header().Set("Content-Type", "application/octet-stream")
			w.(http.Flusher).Flush()
			_, _ = io.WriteString(w, "bytes")
		default:
			refuse(w, http.StatusNotFound, "not_found", "{}")
		}
	}, time.Minute)

	r, err := c.ReadBlob(t.Context(), "ses_1", digest)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	if err != nil || string(got) != png {
		t.Errorf("read %q, %v", got, err)
	}
	if err := r.Close(); err != nil {
		t.Error(err)
	}
	if r.MediaType() != "image/png" || r.Size() != int64(len(png)) {
		t.Errorf("a picture of %s and %d bytes", r.MediaType(), r.Size())
	}
	if path != "/v1/agents/sessions/ses_1/blobs/"+digest || accept != "*/*" {
		t.Errorf("asked %s with Accept %q", path, accept)
	}

	r, err = c.ReadBlob(t.Context(), "ses_1", "sha256:report")
	if err != nil {
		t.Fatal(err)
	}
	if r.MediaType() != "text/markdown; charset=utf-8" || r.Size() != 8 || r.ContentDisposition() != "attachment" {
		t.Errorf("a kept file of %s, %d bytes and %q, before a byte was read", r.MediaType(), r.Size(), r.ContentDisposition())
	}
	if got, err := io.ReadAll(r); err != nil || string(got) != "# Report" {
		t.Errorf("a kept file read %q, %v", got, err)
	}
	_ = r.Close() // the bytes are read; a failed close loses nothing

	r, err = c.ReadBlob(t.Context(), "ses_1", "sha256:streamed")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := io.ReadAll(r); err != nil || string(got) != "bytes" || r.Size() != -1 || r.MediaType() != "application/octet-stream" {
		t.Errorf("a streamed blob read %q, %v, of %d bytes and %s", got, err, r.Size(), r.MediaType())
	}
	_ = r.Close() // the bytes are read; a failed close loses nothing

	if _, err := c.ReadBlob(t.Context(), "ses_1", "sha256:gone"); !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("a blob the session does not hold: %v", err)
	}
	for _, d := range []string{"", "..", ".", "sha256:", ":abc", "9f86d081"} {
		if _, err := c.ReadBlob(t.Context(), "ses_1", d); err == nil || errors.Is(err, latere.ErrNotFound) {
			t.Errorf("the digest %q was sent: %v", d, err)
		}
	}
	if _, err := c.ReadBlob(t.Context(), "", digest); err == nil {
		t.Error("a blob of no session was asked")
	}
}

func TestReadSessionFile(t *testing.T) {
	var asked []string
	c := fileClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/agents/sessions/ses_1/files" {
			refuse(w, http.StatusNotFound, "not_found", "{}")
			return
		}
		p := r.URL.Query().Get("path")
		asked = append(asked, p)
		switch p {
		case "/workspace/report.md":
			w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
			w.Header().Set("Content-Length", "8")
			_, _ = io.WriteString(w, "# Report")
		case "stopped.md":
			refuse(w, http.StatusConflict, "file_unavailable", `{"detail":"the sandbox is Stopped"}`)
		case "huge.bin":
			refuse(w, http.StatusRequestEntityTooLarge, "file_too_large", `{"size":40000000,"limit":33554432}`)
		case "shrank.txt":
			w.Header().Set("Content-Length", "100")
			_, _ = io.WriteString(w, "short")
		case "busy.md":
			refuse(w, http.StatusConflict, "conflict", "{}")
		default:
			refuse(w, http.StatusNotFound, "not_found", "{}")
		}
	}, time.Minute)

	r, err := c.ReadSessionFile(t.Context(), "ses_1", "/workspace/report.md")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := io.ReadAll(r); err != nil || string(got) != "# Report" || r.Size() != 8 || r.MediaType() != "text/markdown; charset=utf-8" {
		t.Errorf("read %q, %v, of %d bytes and %s", got, err, r.Size(), r.MediaType())
	}
	if err := r.Close(); err != nil {
		t.Error(err)
	}
	if asked[0] != "/workspace/report.md" {
		t.Errorf("asked for %q", asked[0])
	}

	_, err = c.ReadSessionFile(t.Context(), "ses_1", "stopped.md")
	var apiErr *latere.Error
	if !errors.Is(err, agents.ErrFileUnavailable) || !errors.Is(err, latere.ErrConflict) || errors.Is(err, agents.ErrFileTooLarge) ||
		!errors.As(err, &apiErr) || apiErr.Code != "file_unavailable" || apiErr.RequestID != "req_1" {
		t.Errorf("a stopped machine: %v", err)
	}
	if err == nil || err.Error() != apiErr.Error() {
		t.Errorf("the error reads %q, not the API's %q", err, apiErr)
	}

	_, err = c.ReadSessionFile(t.Context(), "ses_1", "huge.bin")
	if !errors.Is(err, agents.ErrFileTooLarge) || errors.Is(err, agents.ErrFileUnavailable) || !errors.As(err, &apiErr) ||
		apiErr.Details["limit"] != float64(agents.MaxSessionFileBytes) {
		t.Errorf("a file too large: %v, details %v", err, apiErr.Details)
	}

	// A 409 that is not the machine's is a conflict alone.
	if _, err := c.ReadSessionFile(t.Context(), "ses_1", "busy.md"); errors.Is(err, agents.ErrFileUnavailable) || !errors.Is(err, latere.ErrConflict) {
		t.Errorf("another conflict: %v", err)
	}
	if _, err := c.ReadSessionFile(t.Context(), "ses_1", "missing.md"); !errors.Is(err, latere.ErrNotFound) || errors.Is(err, agents.ErrFileUnavailable) {
		t.Errorf("a missing file: %v", err)
	}

	r, err = c.ReadSessionFile(t.Context(), "ses_1", "shrank.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(r); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("a file that shrank read to the end with %v", err)
	}
	_ = r.Close() // the read failed already; the close has nothing to add

	if _, err := c.ReadSessionFile(t.Context(), "ses_1", ""); err == nil {
		t.Error("a file of no path was asked")
	}
	if _, err := c.ReadSessionFile(t.Context(), "", "a.md"); err == nil {
		t.Error("a file of no session was asked")
	}
}

// TestAFileIsReadPastTheTimeout: the Config's timeout bounds the wait for
// the answer, and the read of the bytes after it is bounded only by the
// caller's context.
func TestAFileIsReadPastTheTimeout(t *testing.T) {
	c := fileClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = io.WriteString(w, "first ")
		w.(http.Flusher).Flush()
		time.Sleep(time.Second)
		_, _ = io.WriteString(w, "last")
	}, 400*time.Millisecond)
	for name, open := range map[string]func() (*agents.FileReader, error){
		"ReadBlob":        func() (*agents.FileReader, error) { return c.ReadBlob(t.Context(), "ses_1", digest) },
		"ReadSessionFile": func() (*agents.FileReader, error) { return c.ReadSessionFile(t.Context(), "ses_1", "big.bin") },
	} {
		r, err := open()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got, err := io.ReadAll(r); err != nil || string(got) != "first last" {
			t.Errorf("%s read %q, %v", name, got, err)
		}
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	}
}

func TestTheFilesAnAnswerKept(t *testing.T) {
	e := agents.Event{Type: agents.EventFilesKept, Payload: []byte(`{"message":"evt_7","files":[
		{"path":"chart.png","resolved":"/workspace/chart.png","blob":"` + digest + `","media_type":"image/png","size":2048,"width":640,"height":480},
		{"path":"report.pdf","resolved":"/workspace/report.pdf","blob":"sha256:ab","media_type":"application/pdf","size":9000,"width":0,"height":0,"ref":"results/1/report.pdf"}],
		"skipped":[{"path":"big.mov","reason":"too_large"}]}`)}
	var kept agents.FilesKept
	if err := e.Decode(&kept); err != nil {
		t.Fatal(err)
	}
	if kept.MessageID != "evt_7" || len(kept.Files) != 2 || kept.Files[0].Blob != digest || kept.Files[0].Width != 640 ||
		kept.Files[1].Ref != "results/1/report.pdf" || kept.Files[1].MediaType != "application/pdf" {
		t.Errorf("kept %+v", kept)
	}
	if len(kept.Skipped) != 1 || kept.Skipped[0].Reason != agents.SkipTooLarge {
		t.Errorf("skipped %+v", kept.Skipped)
	}
}
