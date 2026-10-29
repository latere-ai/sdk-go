// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package mls

import (
	"bytes"
	"testing"
)

// Fuzz targets for everything that decodes bytes from the network or
// from storage. Each runs over its seeds in every `go test`; run one with
// `go test -fuzz=FuzzParseMessage ./mls` to search further.

func messageSeeds(f *testing.F) {
	var vs []map[string]hexBytes
	loadVectors(f, "messages", &vs)
	if len(vs) == 0 {
		f.Fatal("no message vectors")
	}
	for _, v := range vs[:4] {
		for _, k := range []string{"mls_welcome", "mls_group_info", "mls_key_package", "public_message_proposal",
			"public_message_commit", "private_message"} {
			f.Add([]byte(v[k]))
		}
	}
}

// FuzzParseMessage checks that decoding never panics and that whatever
// decodes is the one encoding of its value.
func FuzzParseMessage(f *testing.F) {
	messageSeeds(f)
	f.Fuzz(func(t *testing.T, b []byte) {
		m, err := parseMessage(b)
		if err != nil {
			return
		}
		if got := encode(m.marshal); !bytes.Equal(got, b) {
			t.Fatalf("re-encoding differs:\n in %x\nout %x", b, got)
		}
		// The public entry points over the same bytes must not panic.
		_, _ = ParseKeyPackage(b)
		_, _ = WelcomeKeyPackageRefs(b)
		_, _ = PeekMessage(b)
		_, _ = NewPublicGroup(b, JoinOptions{}, Config{})
	})
}

func FuzzParseTree(f *testing.F) {
	var vs []map[string]hexBytes
	loadVectors(f, "messages", &vs)
	for _, v := range vs[:4] {
		f.Add([]byte(v["ratchet_tree"]))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		tree, err := parseTree(b)
		if err != nil {
			return
		}
		if got := tree.bytes(); !bytes.Equal(got, b) {
			t.Fatalf("re-encoding differs:\n in %x\nout %x", b, got)
		}
		s, err := suiteFor(testSuite)
		if err != nil {
			t.Fatal(err)
		}
		// Checks over an arbitrary tree must not panic.
		_ = tree.treeHash(s)
		_ = tree.verifyParentHashes(s)
		_ = tree.verifyUnmergedLeaves()
		_ = tree.verifyUniqueKeys()
	})
}

// FuzzProcess feeds mutated messages to a member of a live group, which
// must reject what it cannot authenticate without panicking.
func FuzzProcess(f *testing.F) {
	alice, bob := pair(f, Config{PublicHandshake: true})
	app, err := bob.group.Encrypt([]byte("seed"), []byte("ad"))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(app)
	prop, err := bob.group.ProposeRemove(bob.group.Leaf(), nil)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(prop)
	bob.group.DiscardProposal(bob.group.PendingProposals()[0].Ref)
	sc, err := bob.group.Commit(CommitOptions{})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(sc.Message())
	f.Add(sc.GroupInfo())
	state, err := alice.group.MarshalBinary()
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		g, err := UnmarshalGroup(state, Config{})
		if err != nil {
			t.Fatal(err)
		}
		m, err := g.Process(b)
		if err == nil && m.Kind == MessageCommit {
			_ = g.Merge(m.Commit)
		}
		_, _, _ = JoinExternal(b, bob.signer, bob.cred, ExternalJoinOptions{}, Config{})
	})
}

func FuzzUnmarshalGroup(f *testing.F) {
	alice, _ := pair(f, Config{})
	state, err := alice.group.MarshalBinary()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(state)
	f.Fuzz(func(t *testing.T, b []byte) {
		g, err := UnmarshalGroup(b, Config{})
		if err != nil {
			return
		}
		// A restored group must survive its own operations.
		_, _ = g.Encrypt([]byte("x"), nil)
		_, _ = g.Commit(CommitOptions{})
	})
}

func FuzzVerifySignedContent(f *testing.F) {
	alice, bob := pair(f, Config{})
	msg, err := bob.group.Encrypt([]byte("seed"), nil)
	if err != nil {
		f.Fatal(err)
	}
	m, err := alice.group.Process(msg)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(m.Signed)
	key := bob.signer.PublicKey()
	f.Fuzz(func(t *testing.T, b []byte) {
		sc, err := VerifySignedContent(b, key)
		if err == nil && !bytes.Equal(b, m.Signed) && sc == nil {
			t.Fatal("a record verifies without content")
		}
	})
}
