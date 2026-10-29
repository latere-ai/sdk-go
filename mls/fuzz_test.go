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

// FuzzSignedCommit reads the input as a commit's body, signs it validly,
// as a member with its membership tag or as a new member with a
// re-signed path leaf, and has a member and a public view process and
// merge it. FuzzProcess stops at the signature; this reaches the
// proposal list, the path and the tree checks with hostile content.
func FuzzSignedCommit(f *testing.F) {
	alice, bob, _ := trio(f, Config{PublicHandshake: true})
	mallory := newClient(f, testSuite, "mallory")
	dave := newClient(f, testSuite, "dave")
	for _, opts := range []CommitOptions{{}, {Add: []*KeyPackage{dave.kp}}, {Remove: []uint32{2}}} {
		sc, err := bob.group.Commit(opts)
		if err != nil {
			f.Fatal(err)
		}
		m, err := parseMessage(sc.Message())
		if err != nil {
			f.Fatal(err)
		}
		body := encode(m.public.content.commit.marshal)
		f.Add(uint8(0), body)
		f.Add(uint8(1), body)
	}
	gi, err := bob.group.GroupInfo(GroupInfoOptions{RatchetTree: true, ExternalPub: true})
	if err != nil {
		f.Fatal(err)
	}
	_, ext, err := JoinExternal(gi, mallory.signer, mallory.cred, ExternalJoinOptions{}, Config{})
	if err != nil {
		f.Fatal(err)
	}
	m, err := parseMessage(ext)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(uint8(1), encode(m.public.content.commit.marshal))
	aliceState, err := alice.group.MarshalBinary()
	if err != nil {
		f.Fatal(err)
	}
	bobState, err := bob.group.MarshalBinary()
	if err != nil {
		f.Fatal(err)
	}
	aliceInfo, err := alice.group.GroupInfo(GroupInfoOptions{RatchetTree: true})
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, mode uint8, body []byte) {
		var c commit
		if decode(body, c.unmarshal) != nil {
			return
		}
		a, err := UnmarshalGroup(aliceState, Config{})
		if err != nil {
			t.Fatal(err)
		}
		b, err := UnmarshalGroup(bobState, Config{})
		if err != nil {
			t.Fatal(err)
		}
		ac := &authenticatedContent{
			wireFormat: WireFormatPublicMessage,
			content: framedContent{
				groupID: b.ctx.groupID, epoch: b.ctx.epoch,
				sender:      sender{typ: senderMember, index: b.Leaf()},
				contentType: contentCommit, commit: &c,
			},
			auth: framedContentAuthData{confirmationTag: make([]byte, 32)},
		}
		key := b.signer
		if mode&1 == 1 {
			if c.path == nil {
				return
			}
			ac.content.sender = sender{typ: senderNewMemberCommit}
			c.path.leaf.signatureKey = mallory.signer.PublicKey()
			if c.path.leaf.sign(b.s, mallory.signer, b.ctx.groupID, a.tree.freeLeaf()) != nil {
				return
			}
			key = mallory.signer
		}
		ctx := b.ctxBytes()
		if err := b.s.signContent(key, ac, ctx); err != nil {
			return
		}
		pm := &publicMessage{content: ac.content, auth: ac.auth}
		if ac.content.sender.typ == senderMember {
			pm.membershipTag = b.s.membershipTag(b.es.membership, ac, ctx)
		}
		msg := encode((&mlsMessage{wireFormat: WireFormatPublicMessage, public: pm}).marshal)
		if m, err := a.Process(msg); err == nil && m.Commit != nil {
			_ = a.Merge(m.Commit) // only a panic matters here
		}
		if pg, err := NewPublicGroup(aliceInfo, JoinOptions{}, Config{}); err == nil {
			if m, err := pg.Process(msg); err == nil && m.Commit != nil {
				_ = pg.Merge(m.Commit) // only a panic matters here
			}
		}
	})
}

// FuzzTreeValidators runs every check a joiner runs on a tree, and the
// path helpers, on an arbitrary parsed tree.
func FuzzTreeValidators(f *testing.F) {
	alice, _, _ := trio(f, Config{})
	f.Add(alice.group.tree.bytes())
	s := alice.group.s
	f.Fuzz(func(t *testing.T, b []byte) {
		tree, err := parseTree(b)
		if err != nil || tree.numLeaves() > 1<<12 {
			return
		}
		// Only a panic matters: each check may refuse the tree.
		_ = tree.treeHash(s)
		_ = tree.verifyParentHashes(s)
		_ = tree.verifyUnmergedLeaves()
		_ = tree.verifyUniqueKeys()
		p := &publicState{s: s, ctx: alice.group.ctx}
		_ = p.checkGroupExtensions(tree, nil)
		for _, i := range tree.members() {
			_ = tree.filteredDirectPath(i)
		}
	})
}
