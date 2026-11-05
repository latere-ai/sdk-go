// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleYAML = "openapi: 3.1.0\ninfo:\n  title: sample\n  version: \"1\"\n"

// pointDocumentsAt swaps every vendored document's URL for base+"/"+name and
// restores them when the test ends.
func pointDocumentsAt(t *testing.T, base string) {
	t.Helper()
	saved := documents
	documents = map[string]string{}
	for name := range saved {
		documents[name] = base + "/" + name
	}
	t.Cleanup(func() { documents = saved })
}

func TestRunFetchesAndWritesEveryDocument(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(sampleYAML))
	}))
	defer srv.Close()
	pointDocumentsAt(t, srv.URL)

	dir := t.TempDir()
	if err := run(dir, nil); err != nil {
		t.Fatalf("run: %v", err)
	}
	for name := range documents {
		raw, err := os.ReadFile(filepath.Join(dir, name+".json"))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s is not JSON: %v", name, err)
		}
		if doc["openapi"] != "3.1.0" {
			t.Errorf("%s: openapi = %v, want 3.1.0", name, doc["openapi"])
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, "sources.json"))
	if err != nil {
		t.Fatalf("sources.json: %v", err)
	}
	var sources map[string]source
	if err := json.Unmarshal(raw, &sources); err != nil {
		t.Fatalf("sources.json is not JSON: %v", err)
	}
	sum := sha256.Sum256([]byte(sampleYAML))
	want := hex.EncodeToString(sum[:])
	for name, url := range documents {
		if sources[name].URL != url || sources[name].SHA256 != want {
			t.Errorf("sources[%s] = %+v, want url %s sha %s", name, sources[name], url, want)
		}
	}
}

func TestRunIsIdempotent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("b: 2\na: 1\n"))
	}))
	defer srv.Close()
	pointDocumentsAt(t, srv.URL)

	dir := t.TempDir()
	if err := run(dir, nil); err != nil {
		t.Fatalf("first run: %v", err)
	}
	first, err := os.ReadFile(filepath.Join(dir, "agents.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := run(dir, nil); err != nil {
		t.Fatalf("second run: %v", err)
	}
	second, err := os.ReadFile(filepath.Join(dir, "agents.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Error("a refresh that changes nothing changed the bytes")
	}
	if !strings.Contains(string(first), `"a": 1,`) {
		t.Errorf("keys are not sorted: %s", first)
	}
}

func TestRunReadsLocalFilesInPlaceOfTheNetwork(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/agents" {
			t.Error("agents was fetched although a local file was named")
		}
		_, _ = w.Write([]byte(sampleYAML))
	}))
	defer srv.Close()
	pointDocumentsAt(t, srv.URL)

	local := filepath.Join(t.TempDir(), "agents.yaml")
	if err := os.WriteFile(local, []byte("local: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := run(dir, map[string]string{"agents": local}); err != nil {
		t.Fatalf("run: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "agents.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"local": true`) {
		t.Errorf("agents.json = %s, want the local document", raw)
	}
}

func TestRunFailures(t *testing.T) {
	t.Run("status", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		defer srv.Close()
		pointDocumentsAt(t, srv.URL)
		err := run(t.TempDir(), nil)
		if err == nil || !strings.Contains(err.Error(), "404") {
			t.Errorf("err = %v, want one naming the 404", err)
		}
	})
	t.Run("unreachable", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		url := srv.URL
		srv.Close()
		pointDocumentsAt(t, url)
		if err := run(t.TempDir(), nil); err == nil {
			t.Error("run succeeded against a closed server")
		}
	})
	t.Run("bad url", func(t *testing.T) {
		pointDocumentsAt(t, "http://bad host")
		if err := run(t.TempDir(), nil); err == nil {
			t.Error("run succeeded with a URL that cannot be requested")
		}
	})
	t.Run("not yaml", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("a: [unclosed\n"))
		}))
		defer srv.Close()
		pointDocumentsAt(t, srv.URL)
		err := run(t.TempDir(), nil)
		if err == nil || !strings.Contains(err.Error(), "parsing the YAML") {
			t.Errorf("err = %v, want a YAML parse error", err)
		}
	})
	t.Run("missing local file", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(sampleYAML))
		}))
		defer srv.Close()
		pointDocumentsAt(t, srv.URL)
		err := run(t.TempDir(), map[string]string{"agents": filepath.Join(t.TempDir(), "absent.yaml")})
		if err == nil || !strings.HasPrefix(err.Error(), "agents: ") {
			t.Errorf("err = %v, want one naming the agents document", err)
		}
	})
	t.Run("unwritable directory", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(sampleYAML))
		}))
		defer srv.Close()
		pointDocumentsAt(t, srv.URL)
		if err := run(filepath.Join(t.TempDir(), "absent"), nil); err == nil {
			t.Error("run succeeded writing into a directory that does not exist")
		}
	})
}

func TestRunWritesSourcesLast(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(sampleYAML))
	}))
	defer srv.Close()
	pointDocumentsAt(t, srv.URL)

	// A directory in the place of sources.json makes only that last write fail.
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sources.json"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := run(dir, nil); err == nil {
		t.Error("run succeeded although sources.json cannot be written")
	}
}

func TestDocumentNamesAreSorted(t *testing.T) {
	names := documentNames()
	if len(names) != len(documents) {
		t.Fatalf("got %d names, want %d", len(names), len(documents))
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Errorf("names not sorted at %d: %v", i, names)
		}
	}
}

func TestExecute(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(sampleYAML))
	}))
	defer srv.Close()
	pointDocumentsAt(t, srv.URL)

	t.Run("ok", func(t *testing.T) {
		var stderr bytes.Buffer
		if code := execute([]string{"-dir", t.TempDir()}, &stderr); code != 0 {
			t.Errorf("code = %d, stderr = %s", code, stderr.String())
		}
	})
	t.Run("local flag", func(t *testing.T) {
		local := filepath.Join(t.TempDir(), "a.yaml")
		if err := os.WriteFile(local, []byte(sampleYAML), 0o600); err != nil {
			t.Fatal(err)
		}
		var stderr bytes.Buffer
		if code := execute([]string{"-dir", t.TempDir(), "-local", "agents=" + local}, &stderr); code != 0 {
			t.Errorf("code = %d, stderr = %s", code, stderr.String())
		}
	})
	for _, bad := range []string{"agents", "nope=file", "agents="} {
		t.Run("bad local "+bad, func(t *testing.T) {
			var stderr bytes.Buffer
			if code := execute([]string{"-local", bad}, &stderr); code != 2 {
				t.Errorf("code = %d, want 2", code)
			}
			if !strings.Contains(stderr.String(), "is not name=path") {
				t.Errorf("stderr = %q, want the flag's error", stderr.String())
			}
		})
	}
	t.Run("failure", func(t *testing.T) {
		var stderr bytes.Buffer
		if code := execute([]string{"-dir", filepath.Join(t.TempDir(), "absent")}, &stderr); code != 1 {
			t.Errorf("code = %d, want 1", code)
		}
		if !strings.HasPrefix(stderr.String(), "openapi: ") {
			t.Errorf("stderr = %q, want the openapi: prefix", stderr.String())
		}
	})
}
