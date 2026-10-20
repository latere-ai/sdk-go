// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package storage_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"latere.ai/x/sdk-go/latere"
	"latere.ai/x/sdk-go/storage"
)

const file = `{"path":"files/notes/today.md","size":5,"checksum":"9f86d0","checksum_kind":"sha256",
"content_type":"text/markdown","is_public":false,"modified":"2026-10-20T09:00:00Z","version_count":2}`

// newClient returns a client for srv that sends each request once.
func newClient(t *testing.T, srv *httptest.Server) *storage.Client {
	t.Helper()
	cfg, err := latere.NewConfig(latere.WithBaseURL(srv.URL), latere.WithTokenSource(latere.StaticToken("key_test")), latere.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	return storage.New(cfg)
}

func TestWriteFileSendsTheBytesAndReadsTheFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.EscapedPath() != "/v1/storage/files/me/files/notes/to%20day.md" {
			t.Errorf("request = %s %s", r.Method, r.URL.EscapedPath())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer key_test" {
			t.Errorf("Authorization = %q", got)
		}
		if r.ContentLength != 5 || r.Header.Get("Content-Type") != "text/markdown" {
			t.Errorf("length %d, type %q", r.ContentLength, r.Header.Get("Content-Type"))
		}
		if got := r.Header.Get("If-Match"); got != `"9f86d0"` {
			t.Errorf("If-Match = %q", got)
		}
		if b, _ := io.ReadAll(r.Body); string(b) != "hello" {
			t.Errorf("body = %q", b)
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, file)
	}))
	defer srv.Close()
	f, err := newClient(t, srv).WriteFile(context.Background(), storage.Me, "files/notes/to day.md", strings.NewReader("hello"), 5,
		&storage.WriteOptions{ContentType: "text/markdown", IfMatch: "9f86d0"})
	if err != nil {
		t.Fatal(err)
	}
	if f.Path != "files/notes/today.md" || f.Size != 5 || f.ChecksumKind != storage.ChecksumSHA256 || !f.Modified.Equal(time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("file = %+v", f)
	}
	if !strings.Contains(string(f.RawJSON()), `"version_count":2`) {
		t.Fatalf("RawJSON = %s, want every field", f.RawJSON())
	}
}

func TestWriteFileWithIfNoneMatchIsRefusedWhenAFileIsThere(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("If-None-Match"); got != "*" {
			t.Errorf("If-None-Match = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPreconditionFailed)
		fmt.Fprint(w, `{"error":{"code":"precondition_failed","message":"The file changed."}}`)
	}))
	defer srv.Close()
	_, err := newClient(t, srv).WriteFile(context.Background(), storage.Me, "files/a.txt", strings.NewReader("x"), 1, &storage.WriteOptions{IfNoneMatch: true})
	if !errors.Is(err, latere.ErrPreconditionFailed) {
		t.Fatalf("err = %v, want ErrPreconditionFailed", err)
	}
}

// Nothing is sent for a file with no owner, no path or no content.
func TestAFileNeedsAnOwnerAPathAndContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("sent %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()
	c := newClient(t, srv)
	ctx := context.Background()
	if _, err := c.WriteFile(ctx, "", "files/a", strings.NewReader("x"), 1, nil); err == nil {
		t.Error("WriteFile with no owner: nil error")
	}
	if _, err := c.WriteFile(ctx, storage.Me, "files/a", nil, 1, nil); err == nil {
		t.Error("WriteFile with no content: nil error")
	}
	if _, err := c.StatFile(ctx, storage.Me, ""); err == nil {
		t.Error("StatFile with no path: nil error")
	}
	if _, err := c.ReadFile(ctx, storage.Me, ""); err == nil {
		t.Error("ReadFile with no path: nil error")
	}
	if _, err := c.GetDownloadURL(ctx, "", "files/a", nil); err == nil {
		t.Error("GetDownloadURL with no owner: nil error")
	}
	if _, err := c.ListFilesPage(ctx, storage.Me, "", nil); err == nil {
		t.Error("ListFilesPage with no path: nil error")
	}
	if _, err := c.UploadFile(ctx, storage.Me, "", strings.NewReader("x"), 1, nil); err == nil {
		t.Error("UploadFile with no path: nil error")
	}
	if _, err := c.UploadFile(ctx, storage.Me, "files/a", nil, 1, nil); err == nil {
		t.Error("UploadFile with no content: nil error")
	}
}

func TestStatFileReadsTheHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("method = %s", r.Method)
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", "2048")
		w.Header().Set("ETag", `"abc"`)
		w.Header().Set("Last-Modified", "Tue, 20 Oct 2026 09:00:00 GMT")
	}))
	defer srv.Close()
	info, err := newClient(t, srv).StatFile(context.Background(), storage.Me, "files/a.png")
	if err != nil {
		t.Fatal(err)
	}
	if info.Size != 2048 || info.ContentType != "image/png" || info.Checksum != "abc" || !info.Modified.Equal(time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("info = %+v", info)
	}
}

func TestStatFileOfNothingIsNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	if _, err := newClient(t, srv).StatFile(context.Background(), storage.Me, "files/none"); !errors.Is(err, latere.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// A small file comes back in the answer; a large one is followed to
// object storage.
func TestReadFileFollowsALargeFileToObjectStorage(t *testing.T) {
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The API and the store share a host here, which Go's own
		// redirects would forward the credential to.
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("the store got Authorization %q", got)
		}
		w.Header().Set("Content-Type", "video/mp4")
		fmt.Fprint(w, "large bytes")
	}))
	defer store.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != "*/*" {
			t.Errorf("Accept = %q", got)
		}
		if strings.HasSuffix(r.URL.Path, "/large.mp4") {
			http.Redirect(w, r, store.URL+"/obj?sig=1", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "small")
	}))
	defer srv.Close()
	c := newClient(t, srv)
	for path, want := range map[string]string{"files/small.txt": "small", "files/large.mp4": "large bytes"} {
		r, err := c.ReadFile(context.Background(), storage.Me, path)
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Close(); err != nil {
			t.Fatal(err)
		}
		if string(b) != want || r.Size() != int64(len(want)) || r.ContentType() == "" {
			t.Errorf("%s: %q of %d bytes, type %q; want %q", path, b, r.Size(), r.ContentType(), want)
		}
	}
}

func TestGetDownloadURLAnswersTheLinkWithoutFollowingIt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/storage/files/me/files/a.pdf":
			if r.URL.Query().Get("inline") != "0" || r.URL.Query().Get("download") != "1" {
				t.Errorf("query = %q", r.URL.RawQuery)
			}
			http.Redirect(w, r, "https://objects.example.com/a.pdf?sig=1", http.StatusFound)
		case "/v1/storage/files/me/files/public.png":
			http.Redirect(w, r, "/public/me/public.png", http.StatusFound)
		default:
			t.Errorf("followed to %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	c := newClient(t, srv)
	got, err := c.GetDownloadURL(context.Background(), storage.Me, "files/a.pdf", &storage.DownloadURLOptions{Attachment: true})
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://objects.example.com/a.pdf?sig=1" {
		t.Errorf("link = %q", got)
	}
	got, err = c.GetDownloadURL(context.Background(), storage.Me, "files/public.png", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != srv.URL+"/public/me/public.png" {
		t.Errorf("a relative link = %q, want it resolved against the base URL", got)
	}
}

// An answer with the bytes rather than a link is an error, not an empty
// link.
func TestGetDownloadURLWithoutARedirectIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "bytes")
	}))
	defer srv.Close()
	if _, err := newClient(t, srv).GetDownloadURL(context.Background(), storage.Me, "files/a", nil); err == nil {
		t.Fatal("nil error")
	}
}

func TestListFilesWalksEveryPage(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Query().Encode())
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") == "" {
			fmt.Fprint(w, `{"entries":[{"path":"files/a/1.txt","size":1},{"path":"files/a/b/2.txt","size":2}],"prefixes":["files/a/b"],"next_cursor":"c2"}`)
			return
		}
		fmt.Fprint(w, `{"entries":[{"path":"files/a/3.txt","size":3}],"prefixes":[]}`)
	}))
	defer srv.Close()
	var paths []string
	for f, err := range newClient(t, srv).ListFiles(context.Background(), storage.Me, "files/a", &storage.ListFilesParams{Query: "txt", Limit: 2}) {
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, f.Path)
	}
	if strings.Join(paths, " ") != "files/a/1.txt files/a/b/2.txt files/a/3.txt" {
		t.Fatalf("paths = %q", paths)
	}
	want := []string{"limit=2&list=1&q=txt", "cursor=c2&limit=2&list=1&q=txt"}
	if strings.Join(queries, " ") != strings.Join(want, " ") {
		t.Fatalf("queries = %q, want %q", queries, want)
	}
}

func TestListFilesPageReadsTheFoldersAndTheUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("recent") != "1" || r.URL.Query().Get("cursor") != "c1" {
			t.Errorf("query = %q", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"entries":[`+file+`],"prefixes":["files/notes"],"space":{"bytes":5,"files":1}}`)
	}))
	defer srv.Close()
	page, err := newClient(t, srv).ListFilesPage(context.Background(), storage.Me, "files", &storage.ListFilesParams{Recent: true, Cursor: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Files) != 1 || page.Files[0].ContentType != "text/markdown" || len(page.Folders) != 1 || page.Usage == nil || page.Usage.Files != 1 || page.NextCursor != "" {
		t.Fatalf("page = %+v", page)
	}
}

// Every call reports a refusal as the API's error, and an answer that is
// not JSON as an error.
func TestRefusalsAndBrokenAnswersAreErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/broken") || r.URL.Path == "/v1/storage/uploads" {
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"path":`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":{"code":"not_found","message":"No such file."}}`)
	}))
	defer srv.Close()
	c := newClient(t, srv)
	ctx := context.Background()
	if _, err := c.ReadFile(ctx, storage.Me, "files/none"); !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("ReadFile: %v", err)
	}
	if _, err := c.GetDownloadURL(ctx, storage.Me, "files/none", nil); !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("GetDownloadURL: %v", err)
	}
	for _, err := range c.ListFiles(ctx, storage.Me, "files/none", nil) {
		if !errors.Is(err, latere.ErrNotFound) {
			t.Errorf("ListFiles: %v", err)
		}
	}
	if _, err := c.UploadFile(ctx, storage.Me, "files/none", strings.NewReader("x"), 1, nil); !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("UploadFile of a small file: %v", err)
	}
	if _, err := c.WriteFile(ctx, storage.Me, "files/broken", strings.NewReader("x"), 1, nil); err == nil {
		t.Error("WriteFile answered with broken JSON: nil error")
	}
	big := make([]byte, storage.MaxWriteBytes+1)
	if _, err := c.UploadFile(ctx, storage.Me, "files/big", bytes.NewReader(big), int64(len(big)), nil); err == nil {
		t.Error("UploadFile whose upload answered broken JSON: nil error")
	}
}

// ReadFile follows the API's link once, and no further: a second
// redirect, and a link that is not https, are refused.
func TestReadFileFollowsOneLinkAndNoMore(t *testing.T) {
	var store *httptest.Server
	store = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, store.URL+"/again", http.StatusFound)
	}))
	defer store.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/ftp.bin") {
			http.Redirect(w, r, "ftp://objects.example.com/k?sig=FTPSIG", http.StatusFound)
			return
		}
		http.Redirect(w, r, store.URL+"/obj?sig=1", http.StatusFound)
	}))
	defer srv.Close()
	c := newClient(t, srv)
	if _, err := c.ReadFile(context.Background(), storage.Me, "files/twice.bin"); err == nil {
		t.Error("a second redirect was followed")
	}
	_, err := c.ReadFile(context.Background(), storage.Me, "files/ftp.bin")
	if err == nil || strings.Contains(err.Error(), "FTPSIG") {
		t.Errorf("a link that is not https: %v", err)
	}
}

// A link the API answers that is not an address is never quoted, since
// its query is a credential.
func TestALinkThatIsNotAnAddressIsNotQuoted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "http://[::1/obj?X-Amz-Signature=BADSIG")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()
	_, err := newClient(t, srv).GetDownloadURL(context.Background(), storage.Me, "files/a.pdf", nil)
	if err == nil || strings.Contains(err.Error(), "BADSIG") {
		t.Fatalf("err = %v, want one without the link", err)
	}
}
