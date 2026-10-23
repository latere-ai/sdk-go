// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents_test

import (
	"slices"
	"strings"
	"testing"

	"latere.ai/x/sdk-go/agents"
)

// The sandbox lets a page run its scripts and nothing else; above all it
// never gives the page an origin it could share with the program's.
func TestPageSandboxIsScriptsAlone(t *testing.T) {
	if got := strings.Fields(agents.PageSandbox); !slices.Equal(got, []string{"allow-scripts"}) {
		t.Errorf("PageSandbox = %q, want allow-scripts alone", agents.PageSandbox)
	}
}

// pageDirectives parses PagePolicy into its directives' sources, failing
// the test on a policy a browser would read differently than it reads.
func pageDirectives(t *testing.T) map[string][]string {
	t.Helper()
	policy := map[string][]string{}
	for d := range strings.SplitSeq(agents.PagePolicy, "; ") {
		fields := strings.Fields(d)
		if len(fields) < 2 || strings.Contains(d, ";") || strings.Join(fields, " ") != d {
			t.Fatalf("PagePolicy holds %q, not one directive and its sources separated by single spaces", d)
		}
		name := fields[0]
		if _, again := policy[name]; again {
			// A browser reads the first of two directives of one name and
			// ignores the second.
			t.Fatalf("PagePolicy names %s twice", name)
		}
		policy[name] = fields[1:]
	}
	return policy
}

// The policy sends nothing, frames nothing, starts no worker, moves no
// address and loads only from public addresses and the page itself; no
// directive names the program's origin or any host.
func TestPagePolicyReachesNothing(t *testing.T) {
	policy := pageDirectives(t)
	for _, name := range []string{"default-src", "connect-src", "frame-src", "worker-src", "form-action", "base-uri"} {
		if got := policy[name]; !slices.Equal(got, []string{"'none'"}) {
			t.Errorf("%s is %q, want 'none'", name, got)
		}
	}
	known := []string{"default-src", "script-src", "style-src", "font-src", "img-src", "media-src", "connect-src", "frame-src", "worker-src", "form-action", "base-uri"}
	sources := []string{"'none'", "'unsafe-inline'", "'unsafe-eval'", "https:", "data:", "blob:"}
	for name, got := range policy {
		// A directive the list does not know may report, sandbox or
		// navigate somewhere; adding one is a decision for this test too.
		if !slices.Contains(known, name) {
			t.Errorf("PagePolicy names %s, which this test does not know", name)
		}
		for _, s := range got {
			if !slices.Contains(sources, s) {
				t.Errorf("%s allows %s; a page loads from public https addresses and from itself alone", name, s)
			}
		}
	}
	for _, name := range []string{"script-src", "style-src", "font-src", "img-src", "media-src"} {
		if !slices.Contains(policy[name], "https:") {
			t.Errorf("%s is %q, want a page's public https addresses allowed", name, policy[name])
		}
	}
}
