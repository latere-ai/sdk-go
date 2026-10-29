// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package mls

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"testing"
)

var testSuites = []CipherSuite{
	MLS_128_DHKEMX25519_AES128GCM_SHA256_Ed25519,
	MLS_128_DHKEMP256_AES128GCM_SHA256_P256,
	MLS_256_DHKEMP521_AES256GCM_SHA512_P521,
	MLS_256_DHKEMP384_AES256GCM_SHA384_P384,
}

type client struct {
	name    string
	signer  *SignatureKey
	cred    Credential
	kp      *KeyPackage
	secrets *KeyPackageSecrets
	group   *Group
}

func newClient(t testing.TB, cs CipherSuite, name string) *client {
	t.Helper()
	signer, err := GenerateSignatureKey(cs)
	if err != nil {
		t.Fatal(err)
	}
	c := &client{name: name, signer: signer, cred: BasicCredential([]byte(name))}
	c.kp, c.secrets, err = NewKeyPackage(signer, c.cred, KeyPackageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func randomID(t testing.TB) []byte {
	t.Helper()
	id := make([]byte, 32)
	if _, err := rand.Read(id); err != nil {
		t.Fatal(err)
	}
	return id
}

// deliver hands a message to every group in to, which merges a commit
// at once, and fails the test when one cannot process it.
func deliver(t *testing.T, msg []byte, to ...*client) []*Message {
	t.Helper()
	var out []*Message
	for _, c := range to {
		m, err := c.group.Process(msg)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if m.Kind == MessageCommit {
			if err := c.group.Merge(m.Commit); err != nil {
				t.Fatalf("%s: merge: %v", c.name, err)
			}
		}
		out = append(out, m)
	}
	return out
}

// sameEpoch checks every group derives the same epoch.
func sameEpoch(t *testing.T, cs ...*client) {
	t.Helper()
	want := cs[0].group.EpochAuthenticator()
	exp, err := cs[0].group.Export("test", []byte("ctx"), 32)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cs[1:] {
		if c.group.Epoch() != cs[0].group.Epoch() {
			t.Fatalf("%s is in epoch %d, %s in %d", c.name, c.group.Epoch(), cs[0].name, cs[0].group.Epoch())
		}
		if !bytes.Equal(c.group.EpochAuthenticator(), want) {
			t.Fatalf("%s derives another epoch authenticator than %s", c.name, cs[0].name)
		}
		got, err := c.group.Export("test", []byte("ctx"), 32)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, exp) {
			t.Fatalf("%s exports another secret", c.name)
		}
	}
}

func TestGroupLifecycle(t *testing.T) {
	for _, cs := range testSuites {
		t.Run(cs.String(), func(t *testing.T) {
			for _, public := range []bool{false, true} {
				t.Run(fmt.Sprintf("public handshake %v", public), func(t *testing.T) {
					testLifecycle(t, cs, Config{PublicHandshake: public})
				})
			}
		})
	}
}

func testLifecycle(t *testing.T, cs CipherSuite, cfg Config) {
	alice, bob, carol, dave := newClient(t, cs, "alice"), newClient(t, cs, "bob"), newClient(t, cs, "carol"), newClient(t, cs, "dave")
	var err error
	alice.group, err = CreateGroup(randomID(t), alice.signer, alice.cred, CreateOptions{}, cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Alice adds Bob and Carol in one commit.
	sc, err := alice.group.Commit(CommitOptions{Add: []*KeyPackage{bob.kp, carol.kp}, AuthenticatedData: []byte("add")})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(sc.Changes().Added); got != 2 {
		t.Fatalf("commit adds %d members, want 2", got)
	}
	if err := alice.group.Merge(sc); err != nil {
		t.Fatal(err)
	}
	for _, c := range []*client{bob, carol} {
		refs, err := WelcomeKeyPackageRefs(sc.Welcome())
		if err != nil {
			t.Fatal(err)
		}
		if !containsBytes(refs, c.kp.Ref()) {
			t.Fatalf("the Welcome is not for %s", c.name)
		}
		c.group, err = JoinGroup(sc.Welcome(), c.kp, c.secrets, c.signer, JoinOptions{}, cfg)
		if err != nil {
			t.Fatalf("%s joins: %v", c.name, err)
		}
	}
	sameEpoch(t, alice, bob, carol)

	// A delivery service follows from the group info the commit made.
	ds, err := NewPublicGroup(sc.GroupInfo(), JoinOptions{}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := ds.VerifyGroupInfo(sc.GroupInfo()); err != nil {
		t.Fatal(err)
	}

	// Messages go both ways.
	msg, err := bob.group.Encrypt([]byte("hello"), []byte("ad"))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range deliver(t, msg, alice, carol) {
		if m.Kind != MessageApplication || string(m.ApplicationData) != "hello" || string(m.AuthenticatedData) != "ad" {
			t.Fatalf("got %+v", m)
		}
		if m.Sender.Kind != SenderMember || m.Sender.Leaf != bob.group.Leaf() || string(m.Sender.Credential.Identity) != "bob" {
			t.Fatalf("sender %+v", m.Sender)
		}
		sc, err := VerifySignedContent(m.Signed, bob.signer.PublicKey())
		if err != nil {
			t.Fatal(err)
		}
		if string(sc.ApplicationData) != "hello" {
			t.Fatalf("signed content %q", sc.ApplicationData)
		}
		if _, err := VerifySignedContent(m.Signed, carol.signer.PublicKey()); err == nil {
			t.Fatal("a record verifies with another member's key")
		}
	}
	if _, err := alice.group.Process(msg); err == nil {
		t.Fatal("a replayed message opens again")
	}
	if _, err := bob.group.Process(msg); !errors.Is(err, ErrOwnMessage) {
		t.Fatalf("own message: %v", err)
	}
	if info, err := PeekMessage(msg); err != nil || info.WireFormat != WireFormatPrivateMessage || info.Kind != MessageApplication || info.Epoch != bob.group.Epoch() {
		t.Fatalf("peek: %+v, %v", info, err)
	}

	// Carol leaves: she proposes her removal and Bob commits it.
	prop, err := carol.group.ProposeRemove(carol.group.Leaf(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range deliver(t, prop, alice, bob) {
		if m.Kind != MessageProposal || m.Proposal.Kind != ProposalRemove || m.Proposal.Leaf != carol.group.Leaf() {
			t.Fatalf("proposal %+v", m.Proposal)
		}
	}
	if cfg.PublicHandshake {
		if _, err := ds.Process(prop); err != nil {
			t.Fatal(err)
		}
		if pp := ds.PendingProposals(); len(pp) != 1 || pp[0].Kind != ProposalRemove || pp[0].Leaf != carol.group.Leaf() {
			t.Fatalf("the view keeps %+v", pp)
		}
	}
	if _, err := bob.group.Encrypt([]byte("too soon"), nil); !errors.Is(err, ErrPendingProposals) {
		t.Fatalf("encrypt with a pending proposal: %v", err)
	}
	sc, err = bob.group.Commit(CommitOptions{IncludePending: true})
	if err != nil {
		t.Fatal(err)
	}
	if rm := sc.Changes().Removed; len(rm) != 1 || string(rm[0].Credential.Identity) != "carol" {
		t.Fatalf("removed %+v", rm)
	}
	if err := bob.group.Merge(sc); err != nil {
		t.Fatal(err)
	}
	deliver(t, sc.Message(), alice)
	m, err := carol.group.Process(sc.Message())
	if err != nil {
		t.Fatal(err)
	}
	if !m.Commit.Changes().RemovesSelf {
		t.Fatal("carol is not told she is removed")
	}
	if err := carol.group.Merge(m.Commit); err != nil {
		t.Fatal(err)
	}
	if carol.group.Active() {
		t.Fatal("carol's group is still active")
	}
	if _, err := carol.group.Encrypt([]byte("x"), nil); !errors.Is(err, ErrInactive) {
		t.Fatalf("removed member encrypts: %v", err)
	}
	if cfg.PublicHandshake {
		pm, err := ds.Process(sc.Message())
		if err != nil {
			t.Fatal(err)
		}
		if err := ds.Merge(pm.Commit); err != nil {
			t.Fatal(err)
		}
	}
	sameEpoch(t, alice, bob)

	// Dave joins on his own with an external commit from Bob's group info.
	gi, err := bob.group.GroupInfo(GroupInfoOptions{RatchetTree: true, ExternalPub: true})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PublicHandshake {
		if err := ds.VerifyGroupInfo(gi); err != nil {
			t.Fatal(err)
		}
	}
	dave.group, msg, err = JoinExternal(gi, dave.signer, dave.cred, ExternalJoinOptions{AuthenticatedData: []byte("join")}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range deliver(t, msg, alice, bob) {
		ch := m.Commit.Changes()
		if !ch.ExternalJoin || len(ch.Added) != 1 || string(ch.Added[0].Credential.Identity) != "dave" {
			t.Fatalf("external join changes %+v", ch)
		}
	}
	if cfg.PublicHandshake {
		pm, err := ds.Process(msg)
		if err != nil {
			t.Fatal(err)
		}
		if err := ds.Merge(pm.Commit); err != nil {
			t.Fatal(err)
		}
	}
	sameEpoch(t, alice, bob, dave)

	// Everyone talks in the new epoch, after Dave commits for new keys.
	sc, err = dave.group.Commit(CommitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := dave.group.Merge(sc); err != nil {
		t.Fatal(err)
	}
	deliver(t, sc.Message(), alice, bob)
	if cfg.PublicHandshake {
		pm, err := ds.Process(sc.Message())
		if err != nil {
			t.Fatal(err)
		}
		if err := ds.Merge(pm.Commit); err != nil {
			t.Fatal(err)
		}
		if ds.Epoch() != alice.group.Epoch() || !bytes.Equal(ds.RatchetTree(), alice.group.RatchetTree()) {
			t.Fatal("the delivery service's view differs from the members'")
		}
	}
	sameEpoch(t, alice, bob, dave)
	for _, from := range []*client{alice, bob, dave} {
		msg, err := from.group.Encrypt([]byte("from "+from.name), nil)
		if err != nil {
			t.Fatal(err)
		}
		var to []*client
		for _, c := range []*client{alice, bob, dave} {
			if c != from {
				to = append(to, c)
			}
		}
		for _, m := range deliver(t, msg, to...) {
			if string(m.ApplicationData) != "from "+from.name {
				t.Fatalf("got %q", m.ApplicationData)
			}
		}
	}
	if got := len(alice.group.Members()); got != 3 {
		t.Fatalf("%d members, want 3", got)
	}
}

func containsBytes(list [][]byte, b []byte) bool {
	for _, x := range list {
		if bytes.Equal(x, b) {
			return true
		}
	}
	return false
}

// A committer's Welcome extensions reach the joiners under its
// signature, and a group info's envelope says which extensions it has.
func TestWelcomeExtensions(t *testing.T) {
	cfg := Config{PublicHandshake: true}
	alice, bob := newClient(t, testSuite, "alice"), newClient(t, testSuite, "bob")
	var err error
	alice.group, err = CreateGroup(randomID(t), alice.signer, alice.cred, CreateOptions{SupportedExtensions: []ExtensionType{0xff01}}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for name, exts := range map[string][]Extension{
		"tree":         {{Type: ExtensionRatchetTree, Data: []byte{1}}},
		"external key": {{Type: ExtensionExternalPub, Data: []byte{1}}},
		"twice":        {{Type: 0xff01}, {Type: 0xff01}},
	} {
		if _, err := alice.group.Commit(CommitOptions{Add: []*KeyPackage{bob.kp}, WelcomeExtensions: exts}); err == nil {
			t.Errorf("%s: a Welcome extension the group info sets itself or repeats was taken", name)
		}
	}
	if _, err := alice.group.Commit(CommitOptions{WelcomeExtensions: []Extension{{Type: 0xff01}}}); err == nil {
		t.Error("a commit that adds no one carried Welcome extensions")
	}
	want := Extension{Type: 0xff01, Data: []byte("notice digest")}
	sc, err := alice.group.Commit(CommitOptions{Add: []*KeyPackage{bob.kp}, WelcomeExtensions: []Extension{want}})
	if err != nil {
		t.Fatal(err)
	}
	if err := alice.group.Merge(sc); err != nil {
		t.Fatal(err)
	}
	bob.group, err = JoinGroup(sc.Welcome(), bob.kp, bob.secrets, bob.signer, JoinOptions{}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	got := bob.group.WelcomeExtensions()
	if len(got) != 1 || got[0].Type != want.Type || !bytes.Equal(got[0].Data, want.Data) {
		t.Fatalf("bob read %+v", got)
	}
	if alice.group.WelcomeExtensions() != nil {
		t.Error("the creator has Welcome extensions")
	}

	info, err := PeekMessage(sc.GroupInfo())
	if err != nil {
		t.Fatal(err)
	}
	if info.WireFormat != WireFormatGroupInfo || !bytes.Equal(info.GroupID, alice.group.GroupID()) || info.Epoch != 1 ||
		len(info.Extensions) != 2 || info.Extensions[0] != ExtensionRatchetTree || info.Extensions[1] != ExtensionExternalPub {
		t.Fatalf("peeked %+v", info)
	}
	bare, err := alice.group.GroupInfo(GroupInfoOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if info, err := PeekMessage(bare); err != nil || len(info.Extensions) != 0 {
		t.Fatalf("peeked %+v, %v", info, err)
	}
}
