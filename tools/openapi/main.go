// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Command openapi refreshes the OpenAPI documents the SDK's tests hold the
// models to. It fetches each capability's document from the public API
// reference, converts it to JSON with sorted keys so a refresh that changes
// nothing changes no byte, and writes it to openapi/ with a sources.json
// that records where each came from and the SHA-256 of what was fetched.
//
// Run it from the repository root with `make openapi`.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// documents are the capabilities whose documents are vendored, by the file
// name each is written to.
var documents = map[string]string{
	"agents":      "https://platform.latere.ai/docs/agents/openapi.yaml",
	"account":     "https://platform.latere.ai/docs/account/openapi.yaml",
	"apps":        "https://platform.latere.ai/docs/apps/openapi.yaml",
	"connections": "https://platform.latere.ai/docs/connections/openapi.yaml",
	"evals":       "https://platform.latere.ai/docs/evals/openapi.yaml",
	"storage":     "https://platform.latere.ai/docs/storage/openapi.yaml",
}

// source is one entry of sources.json.
type source struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

func main() {
	dir := flag.String("dir", "openapi", "the directory the documents are written to")
	local := map[string]string{}
	flag.Func("local", "name=path: read the document of name from a file, such as a core's branch, in place of the public reference, to check the models against a change before it is published; the next run without it fetches the published document again", func(v string) error {
		name, path, ok := strings.Cut(v, "=")
		if _, known := documents[name]; !ok || !known || path == "" {
			return fmt.Errorf("%q is not name=path for a document of %v", v, documentNames())
		}
		local[name] = path
		return nil
	})
	flag.Parse()
	if err := run(*dir, local); err != nil {
		fmt.Fprintln(os.Stderr, "openapi:", err)
		os.Exit(1)
	}
}

// documentNames is the names of the documents, sorted.
func documentNames() []string {
	names := make([]string, 0, len(documents))
	for name := range documents {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func run(dir string, local map[string]string) error {
	client := &http.Client{Timeout: time.Minute}
	sources := map[string]source{}
	for _, name := range documentNames() {
		url := documents[name]
		var raw []byte
		var err error
		if path, ok := local[name]; ok {
			raw, err = os.ReadFile(path)
		} else {
			raw, err = fetch(client, url)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		var doc any
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("%s: parsing the YAML: %w", name, err)
		}
		out, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return fmt.Errorf("%s: encoding the JSON: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".json"), append(out, '\n'), 0o644); err != nil {
			return err
		}
		sum := sha256.Sum256(raw)
		sources[name] = source{URL: url, SHA256: hex.EncodeToString(sum[:])}
	}
	out, err := json.MarshalIndent(sources, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "sources.json"), append(out, '\n'), 0o644)
}

// fetch returns the body of a 200 answer to a GET of url.
func fetch(client *http.Client, url string) ([]byte, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 32<<20))
}
