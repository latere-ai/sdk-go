// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package memory_test

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"testing"

	"latere.ai/x/sdk-go/latere"
	"latere.ai/x/sdk-go/memory"
)

// zipOf is a zip of files, by name, with comment.
func zipOf(t *testing.T, comment string, files map[string][]byte) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, body := range files {
		w, err := z.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.SetComment(comment); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// exportAPI is a Client of a server that answers an export with body,
// flushed in pieces of chunk bytes so the zip arrives as a stream.
func exportAPI(t *testing.T, body []byte, chunk int) (*memory.Client, func() []call) {
	t.Helper()
	return fakeAPI(t, 0, func(w http.ResponseWriter, r *http.Request, _ int) {
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="latere-memory-2026-10-29.zip"`)
		w.WriteHeader(http.StatusOK)
		for len(body) > 0 {
			n := min(chunk, len(body))
			if _, err := w.Write(body[:n]); err != nil {
				t.Error(err)
				return
			}
			w.(http.Flusher).Flush()
			body = body[n:]
		}
	})
}

func TestExportMemoryStreamsTheZip(t *testing.T) {
	big := make([]byte, 200<<10)
	if _, err := rand.Read(big); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string][]byte{
		"two files":                 zipOf(t, "", map[string][]byte{"latere-memory/you/MEMORY.md": []byte("- [Short lists](short-lists.md)\n"), "latere-memory/you/short-lists.md": []byte("Keep lists short.\n")}),
		"no memory":                 zipOf(t, "", nil),
		"a comment":                 zipOf(t, "made by Latere", map[string][]byte{"latere-memory/you/a.md": []byte("a")}),
		"a file past the tail kept": zipOf(t, "", map[string][]byte{"latere-memory/you/big.md": big}),
	} {
		c, calls := exportAPI(t, body, 1000)
		e, err := c.ExportMemory(t.Context())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var got bytes.Buffer
		if _, err := io.Copy(&got, e); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if err := e.Close(); err != nil {
			t.Error(err)
		}
		if !bytes.Equal(got.Bytes(), body) || e.FileName() != "latere-memory-2026-10-29.zip" {
			t.Errorf("%s: read %d bytes of %d, named %q", name, got.Len(), len(body), e.FileName())
		}
		if _, err := zip.NewReader(bytes.NewReader(got.Bytes()), int64(got.Len())); err != nil {
			t.Errorf("%s: the zip read does not open: %v", name, err)
		}
		if c := calls()[0]; c.method != http.MethodGet || c.path != "/v1/account/memory/export" || c.accept != "application/zip" {
			t.Errorf("%s: sent %+v", name, c)
		}
	}
}

// An export the API stopped partway, or answered with nothing, ends with
// ErrExportIncomplete rather than io.EOF.
func TestAnExportCutShortIsIncomplete(t *testing.T) {
	whole := zipOf(t, "", map[string][]byte{"latere-memory/you/a.md": []byte("a memory\n"), "latere-memory/you/b.md": []byte("another\n")})
	for name, body := range map[string][]byte{
		"nothing":            nil,
		"no end record":      whole[:len(whole)-22],
		"part of the record": whole[:len(whole)-5],
		"a byte past it":     append(append([]byte(nil), whole...), 0),
		"the first files":    whole[:40],
	} {
		c, _ := exportAPI(t, body, 7)
		e, err := c.ExportMemory(t.Context())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		n, err := io.Copy(io.Discard, e)
		if !errors.Is(err, memory.ErrExportIncomplete) || n != int64(len(body)) {
			t.Errorf("%s: read %d of %d bytes, then %v", name, n, len(body), err)
		}
		if err := e.Close(); err != nil {
			t.Error(err)
		}
	}
}

// A refused export is the API's error with its sentinel, and a file name
// with a folder in it is given without the folder.
func TestExportMemoryRefusalsAndNames(t *testing.T) {
	c, _ := fakeAPI(t, 0, func(w http.ResponseWriter, r *http.Request, _ int) {
		refuse(w, http.StatusForbidden, "memory_personal_only")
	})
	if _, err := c.ExportMemory(t.Context()); !errors.Is(err, memory.ErrPersonalOnly) || !errors.Is(err, latere.ErrForbidden) {
		t.Errorf("an organization's export: %v", err)
	}
	for header, want := range map[string]string{
		`attachment; filename="../../etc/memory.zip"`: "memory.zip",
		`attachment; filename="C:\\memory.zip"`:       "memory.zip",
		`attachment; filename=".."`:                   "",
		`attachment`:                                  "",
		`attachment; filename="unterminated`:          "",
		``:                                            "",
	} {
		c, _ := fakeAPI(t, 0, func(w http.ResponseWriter, r *http.Request, _ int) {
			if header != "" {
				w.Header().Set("Content-Disposition", header)
			}
			_, _ = w.Write(zipOf(t, "", nil))
		})
		e, err := c.ExportMemory(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if e.FileName() != want {
			t.Errorf("%s: named %q, want %q", header, e.FileName(), want)
		}
		if err := e.Close(); err != nil {
			t.Error(err)
		}
	}
}
