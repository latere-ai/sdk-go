// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package storage_test

import (
	"bytes"
	"context"
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

	"latere.ai/x/sdk-go/latere"
	"latere.ai/x/sdk-go/storage"
)

// partSize is the part size the fake Storage hands out: a file just over
// MaxWriteBytes goes up in three parts, the last one short.
const partSize = 8 << 20

// fakeStorage serves the upload routes and a signed link per part. fail,
// when set, answers the part it names with 403.
type fakeStorage struct {
	t        *testing.T
	srv      *httptest.Server
	fail     int
	mu       sync.Mutex
	parts    map[int][]byte
	started  []map[string]any
	complete []map[string]any
	aborted  []string
	writes   int
	inFlight atomic.Int32
	most     atomic.Int32
	// headers holds each API request's headers, by method and path.
	headers map[string]http.Header
	// completeFails refuses every completion.
	completeFails bool
}

func newFakeStorage(t *testing.T) *fakeStorage {
	s := &fakeStorage{t: t, parts: map[int][]byte{}, headers: map[string]http.Header{}}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *fakeStorage) serve(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/v1/") {
		s.mu.Lock()
		s.headers[r.Method+" "+r.URL.Path] = r.Header.Clone()
		s.mu.Unlock()
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/storage/uploads":
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			s.t.Error(err)
		}
		s.mu.Lock()
		s.started = append(s.started, body)
		s.mu.Unlock()
		size := int64(body["size"].(float64))
		count := int((size + partSize - 1) / partSize)
		var urls []string
		for n := 1; n <= count; n++ {
			urls = append(urls, fmt.Sprintf("%s/part/%d?sig=s%d", s.srv.URL, n, n))
		}
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(map[string]any{"id": "up_1", "owner": "https://auth.latere.ai|alice", "path": body["path"],
			"part_size": partSize, "part_count": count, "part_urls": urls, "expires_at": "2026-10-21T09:00:00Z"}); err != nil {
			s.t.Error(err)
		}
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/part/"):
		if r.Header.Get("Authorization") != "" {
			s.t.Error("a part carried the credential")
		}
		now := s.inFlight.Add(1)
		defer s.inFlight.Add(-1)
		for {
			most := s.most.Load()
			if now <= most || s.most.CompareAndSwap(most, now) {
				break
			}
		}
		var n int
		if _, err := fmt.Sscanf(r.URL.Path, "/part/%d", &n); err != nil {
			s.t.Error(err)
		}
		b, err := io.ReadAll(r.Body)
		if err != nil {
			// The client broke the part off, as an upload whose context
			// ends does.
			return
		}
		if n == s.fail {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		s.mu.Lock()
		s.parts[n] = b
		s.mu.Unlock()
		w.Header().Set("ETag", fmt.Sprintf(`"etag-%d"`, n))
	case r.Method == http.MethodPost && r.URL.Path == "/v1/storage/uploads/up_1/complete" && s.completeFails:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"error":{"code":"forbidden","message":"No."}}`)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/storage/uploads/up_1/complete":
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			s.t.Error(err)
		}
		s.mu.Lock()
		s.complete = append(s.complete, body)
		s.mu.Unlock()
		fmt.Fprint(w, `{"path":"files/videos/talk.mp4","size":1,"checksum":"e1-3","checksum_kind":"etag","content_type":"video/mp4"}`)
	case r.Method == http.MethodDelete && r.URL.Path == "/v1/storage/uploads/up_1":
		s.mu.Lock()
		s.aborted = append(s.aborted, "up_1")
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/v1/storage/files/"):
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			s.t.Error(err)
		}
		s.mu.Lock()
		s.writes++
		s.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"path":"files/a.txt","size":5}`)
	default:
		s.t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

// bigFile is just over MaxWriteBytes, each byte telling where it is.
func bigFile() []byte {
	b := make([]byte, storage.MaxWriteBytes+3)
	for i := range b {
		b[i] = byte(i % 251)
	}
	return b
}

// progressLog records what UploadFile reports.
type progressLog struct {
	mu   sync.Mutex
	last int64
	max  int64
	tot  int64
}

func (p *progressLog) report(sent, total int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.last, p.tot = sent, total
	p.max = max(p.max, sent)
}

func TestUploadFileWritesASmallFileInOneRequest(t *testing.T) {
	s := newFakeStorage(t)
	var p progressLog
	f, err := newClient(t, s.srv).UploadFile(context.Background(), storage.Me, "files/a.txt", strings.NewReader("hello"), 5, &storage.UploadOptions{Progress: p.report})
	if err != nil {
		t.Fatal(err)
	}
	if f.Path != "files/a.txt" || s.writes != 1 || len(s.started) != 0 {
		t.Fatalf("file %+v after %d writes and %d uploads", f, s.writes, len(s.started))
	}
	if p.last != 5 || p.tot != 5 {
		t.Fatalf("progress ended at %d of %d, want 5 of 5", p.last, p.tot)
	}
}

func TestUploadFilePutsALargeFileInParts(t *testing.T) {
	s := newFakeStorage(t)
	data := bigFile()
	var p progressLog
	f, err := newClient(t, s.srv).UploadFile(context.Background(), storage.Me, "files/videos/talk.mp4", bytes.NewReader(data), int64(len(data)),
		&storage.UploadOptions{ContentType: "video/mp4", Progress: p.report, Parallel: 2})
	if err != nil {
		t.Fatal(err)
	}
	if f.ChecksumKind != storage.ChecksumETag || f.ContentType != "video/mp4" {
		t.Fatalf("file = %+v", f)
	}
	if len(s.started) != 1 || s.started[0]["content_type"] != "video/mp4" || s.started[0]["size"] != float64(len(data)) || s.started[0]["owner"] != "me" {
		t.Fatalf("started = %v", s.started)
	}
	var joined []byte
	for n := 1; n <= 3; n++ {
		joined = append(joined, s.parts[n]...)
	}
	if !bytes.Equal(joined, data) || len(s.parts[3]) != 3+storage.MaxWriteBytes-2*partSize {
		t.Fatalf("the parts hold %d bytes, the last %d; want the file's %d in order", len(joined), len(s.parts[3]), len(data))
	}
	got, err := json.Marshal(s.complete)
	if err != nil {
		t.Fatal(err)
	}
	if want := `[{"parts":[{"etag":"\"etag-1\"","n":1},{"etag":"\"etag-2\"","n":2},{"etag":"\"etag-3\"","n":3}]}]`; string(got) != want {
		t.Fatalf("complete = %s, want %s", got, want)
	}
	if s.most.Load() > 2 {
		t.Fatalf("%d parts went at once, want at most 2", s.most.Load())
	}
	if p.last != int64(len(data)) || p.tot != int64(len(data)) || p.max != p.last {
		t.Fatalf("progress ended at %d of %d (most %d), want the whole file", p.last, p.tot, p.max)
	}
	if len(s.aborted) != 0 {
		t.Fatal("a finished upload was given up")
	}
}

// A part refused for good stops the upload, which is given up, and its
// error is what the caller hears.
func TestUploadFileGivesUpWhenAPartFails(t *testing.T) {
	s := newFakeStorage(t)
	s.fail = 2
	data := bigFile()
	_, err := newClient(t, s.srv).UploadFile(context.Background(), storage.Me, "files/videos/talk.mp4", bytes.NewReader(data), int64(len(data)), nil)
	if !errors.Is(err, latere.ErrForbidden) {
		t.Fatalf("err = %v, want the part's ErrForbidden", err)
	}
	if len(s.aborted) != 1 || len(s.complete) != 0 {
		t.Fatalf("aborted %v, completed %d times; want given up and never completed", s.aborted, len(s.complete))
	}
}

// An upload whose context ends is still given up.
func TestUploadFileGivesUpWhenItsContextEnds(t *testing.T) {
	s := newFakeStorage(t)
	data := bigFile()
	ctx, cancel := context.WithCancel(context.Background())
	_, err := newClient(t, s.srv).UploadFile(ctx, storage.Me, "files/videos/talk.mp4", bytes.NewReader(data), int64(len(data)), &storage.UploadOptions{
		Progress: func(sent, total int64) {
			if sent > 0 {
				cancel()
			}
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(s.aborted) != 1 || len(s.complete) != 0 {
		t.Fatalf("aborted %v, completed %d times", s.aborted, len(s.complete))
	}
}

// StartUpload is sent once even when retries are on, since the API takes
// no idempotency key for it. CompleteUpload is sent again after a server
// error, since the API keeps the upload until it completes.
func TestStartIsSentOnceAndCompleteAgainAfterAServerError(t *testing.T) {
	var starts, completes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/complete") {
			completes.Add(1)
		} else {
			starts.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"error":{"code":"unavailable","message":"Try again."}}`)
	}))
	defer srv.Close()
	cfg, err := latere.NewConfig(latere.WithBaseURL(srv.URL), latere.WithTokenSource(latere.StaticToken("key_test")),
		latere.WithMaxRetries(3), latere.WithBackoff(time.Millisecond, 2*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	c := storage.New(cfg)
	if _, err := c.StartUpload(context.Background(), &storage.StartUploadParams{Owner: storage.Me, Path: "files/a", Size: 1}); err == nil {
		t.Fatal("StartUpload: nil error")
	}
	if _, err := c.CompleteUpload(context.Background(), "up_1", &storage.CompleteUploadParams{Parts: []storage.Part{{N: 1, ETag: `"e"`}}}); err == nil {
		t.Fatal("CompleteUpload: nil error")
	}
	if starts.Load() != 1 || completes.Load() != 4 {
		t.Fatalf("sent %d starts and %d completions, want 1 and 4", starts.Load(), completes.Load())
	}
}

// A completion that fails for good gives the upload up, and the error
// names it.
func TestUploadFileGivesUpWhenTheCompletionFails(t *testing.T) {
	s := newFakeStorage(t)
	s.completeFails = true
	data := bigFile()
	_, err := newClient(t, s.srv).UploadFile(context.Background(), storage.Me, "files/videos/talk.mp4", bytes.NewReader(data), int64(len(data)), nil)
	var up *storage.UploadError
	if !errors.As(err, &up) || up.ID != "up_1" || !errors.Is(err, latere.ErrForbidden) {
		t.Fatalf("err = %v, want an *UploadError naming up_1 with the completion's refusal", err)
	}
	if len(s.aborted) != 1 {
		t.Fatalf("aborted %v, want the upload given up", s.aborted)
	}
}

// A write's conditions reach the API whichever way the file goes up: on
// the write, or on the completion of its parts.
func TestUploadFileHoldsTheWriteToItsConditions(t *testing.T) {
	s := newFakeStorage(t)
	c := newClient(t, s.srv)
	if _, err := c.UploadFile(context.Background(), storage.Me, "files/a.txt", strings.NewReader("hello"), 5, &storage.UploadOptions{IfMatch: "9f86d0"}); err != nil {
		t.Fatal(err)
	}
	data := bigFile()
	if _, err := c.UploadFile(context.Background(), storage.Me, "files/videos/talk.mp4", bytes.NewReader(data), int64(len(data)), &storage.UploadOptions{IfNoneMatch: true}); err != nil {
		t.Fatal(err)
	}
	if got := s.headers["PUT /v1/storage/files/me/files/a.txt"].Get("If-Match"); got != `"9f86d0"` {
		t.Errorf("the write's If-Match = %q", got)
	}
	if got := s.headers["POST /v1/storage/uploads/up_1/complete"].Get("If-None-Match"); got != "*" {
		t.Errorf("the completion's If-None-Match = %q", got)
	}
}

// An upload whose parts do not cover the file is refused before a part
// goes up, and given up.
func TestUploadFileRefusesPartsThatDoNotFitTheFile(t *testing.T) {
	var aborted, parts atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"id":"up_1","part_size":1024,"part_count":1,"part_urls":["%s/part/1"]}`, srv.URL)
		case r.Method == http.MethodDelete:
			aborted.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			parts.Add(1)
		}
	}))
	defer srv.Close()
	data := bigFile()
	if _, err := newClient(t, srv).UploadFile(context.Background(), storage.Me, "files/a", bytes.NewReader(data), int64(len(data)), nil); err == nil {
		t.Fatal("nil error")
	}
	if parts.Load() != 0 || aborted.Load() != 1 {
		t.Fatalf("%d parts sent, %d give-ups; want none sent and one give-up", parts.Load(), aborted.Load())
	}
}

// An answer StartUpload cannot use is given up, and the error names the
// upload.
func TestStartUploadRefusesAnAnswerWithoutLinks(t *testing.T) {
	var aborted atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			aborted.Add(1)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":"up_1","part_size":8,"part_count":2,"part_urls":["https://x/1"]}`)
	}))
	defer srv.Close()
	c := newClient(t, srv)
	_, err := c.StartUpload(context.Background(), &storage.StartUploadParams{Owner: storage.Me, Path: "files/a", Size: 16})
	var up *storage.UploadError
	if !errors.As(err, &up) || up.ID != "up_1" || aborted.Load() != 1 {
		t.Fatalf("two parts and one link: err = %v after %d give-ups; want the upload named and given up", err, aborted.Load())
	}
	if _, err := c.StartUpload(context.Background(), &storage.StartUploadParams{Owner: storage.Me, Path: "files/a"}); err == nil {
		t.Fatal("no size: nil error")
	}
	if _, err := c.StartUpload(context.Background(), nil); err == nil {
		t.Fatal("no params: nil error")
	}
}

func TestPutPartNeedsObjectStoragesLabel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	c := newClient(t, srv)
	u := &storage.Upload{ID: "up_1", PartSize: 1, PartCount: 1, PartURLs: []string{srv.URL + "/part/1"}}
	if _, err := c.PutPart(context.Background(), u, 1, strings.NewReader("x"), 1); err == nil {
		t.Fatal("a part with no ETag: nil error")
	}
	if _, err := c.PutPart(context.Background(), u, 2, strings.NewReader("x"), 1); err == nil {
		t.Fatal("part 2 of 1: nil error")
	}
	if _, err := c.PutPart(context.Background(), u, 1, nil, 1); err == nil {
		t.Fatal("no content: nil error")
	}
}

func TestUploadKeepsItsJSON(t *testing.T) {
	var u storage.Upload
	if err := json.Unmarshal([]byte(`{"id":"up_1","part_count":1,"part_urls":["https://x/1"],"later":true}`), &u); err != nil {
		t.Fatal(err)
	}
	if u.ID != "up_1" || !strings.Contains(string(u.RawJSON()), `"later":true`) {
		t.Fatalf("upload %+v, raw %s", u, u.RawJSON())
	}
}

func TestCompleteAndAbortNeedAnID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("sent %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()
	c := newClient(t, srv)
	if _, err := c.CompleteUpload(context.Background(), "", &storage.CompleteUploadParams{Parts: []storage.Part{{N: 1}}}); err == nil {
		t.Error("CompleteUpload with no id: nil error")
	}
	if _, err := c.CompleteUpload(context.Background(), "up_1", nil); err == nil {
		t.Error("CompleteUpload with no part: nil error")
	}
	if err := c.AbortUpload(context.Background(), ""); err == nil {
		t.Error("AbortUpload with no id: nil error")
	}
}
