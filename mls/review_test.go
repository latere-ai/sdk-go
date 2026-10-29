// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package mls

import (
	"bytes"
	"errors"
	"slices"
	"testing"
	"time"
)

// The tests here hold what an adversarial review of the package found:
// each fails without the change it covers.

// trio is a group of alice, bob and carol, made by alice.
func trio(t testing.TB, cfg Config) (alice, bob, carol *client) {
	t.Helper()
	alice, bob, carol = newClient(t, testSuite, "alice"), newClient(t, testSuite, "bob"), newClient(t, testSuite, "carol")
	var err error
	alice.group, err = CreateGroup(randomID(t), alice.signer, alice.cred, CreateOptions{}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	sc, err := alice.group.Commit(CommitOptions{Add: []*KeyPackage{bob.kp, carol.kp}})
	if err != nil {
		t.Fatal(err)
	}
	if err := alice.group.Merge(sc); err != nil {
		t.Fatal(err)
	}
	for _, c := range []*client{bob, carol} {
		c.group, err = JoinGroup(sc.Welcome(), c.kp, c.secrets, c.signer, JoinOptions{}, cfg)
		if err != nil {
			t.Fatal(err)
		}
	}
	return alice, bob, carol
}

// proposalFrom signs a proposal as a sender outside the group: a client
// proposing its own add, or an external sender.
func proposalFrom(t testing.TB, signer *SignatureKey, sd sender, groupID []byte, epoch uint64, pr *proposal) []byte {
	t.Helper()
	s, err := suiteFor(signer.CipherSuite())
	if err != nil {
		t.Fatal(err)
	}
	a := &authenticatedContent{
		wireFormat: WireFormatPublicMessage,
		content:    framedContent{groupID: groupID, epoch: epoch, sender: sd, contentType: contentProposal, proposal: pr},
	}
	if err := s.signContent(signer, a, nil); err != nil {
		t.Fatal(err)
	}
	return encode((&mlsMessage{wireFormat: WireFormatPublicMessage, public: &publicMessage{content: a.content, auth: a.auth}}).marshal)
}

// The epoch's encryption secret is gone once it seeds the secret tree, so
// a state taken after a message was read cannot open it again.
func TestEncryptionSecretDeleted(t *testing.T) {
	alice, bob := pair(t, Config{})
	msg, err := alice.group.Encrypt([]byte("read and deleted"), nil)
	if err != nil {
		t.Fatal(err)
	}
	deliver(t, msg, bob)
	for _, g := range []*Group{alice.group, bob.group} {
		if g.es.encryption != nil {
			t.Fatal("the encryption secret outlived the secret tree's seeding")
		}
	}
	state, err := bob.group.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	stolen, err := UnmarshalGroup(state, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if stolen.es.encryption != nil {
		t.Fatal("the state carries the encryption secret")
	}
	if _, err := stolen.Process(msg); err == nil {
		t.Fatal("a state taken after the read opens the message")
	}
	// A state of the earlier version, which carried it, is refused.
	old := bytes.Clone(state)
	old[0] = 1
	if _, err := UnmarshalGroup(old, Config{}); err == nil {
		t.Fatal("a state of version 1 restored")
	}
}

// A client outside the group cannot get a pending proposal into it: a
// proposal of its own add and an external sender's add or extensions are
// refused unless the application accepts them, so no commit carries one
// with IncludePending.
func TestProposalsFromOutside(t *testing.T) {
	signer, err := GenerateSignatureKey(testSuite)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{PublicHandshake: true}
	alice := newClient(t, testSuite, "alice")
	alice.group, err = CreateGroup(randomID(t), alice.signer, alice.cred, CreateOptions{Extensions: []Extension{
		ExternalSendersExtension([]ExternalSender{{SignatureKey: signer.PublicKey(), Credential: BasicCredential([]byte("ds"))}}),
	}}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	g := alice.group
	mallory := newClient(t, testSuite, "mallory")
	kp := mallory.kp.kp
	selfAdd := proposalFrom(t, mallory.signer, sender{typ: senderNewMemberProposal}, g.GroupID(), g.Epoch(), &proposal{typ: proposalAdd, add: &kp})
	dsAdd := proposalFrom(t, signer, sender{typ: senderExternal}, g.GroupID(), g.Epoch(), &proposal{typ: proposalAdd, add: &kp})
	dsExt := proposalFrom(t, signer, sender{typ: senderExternal}, g.GroupID(), g.Epoch(),
		&proposal{typ: proposalGroupContextExtensions, extensions: nil})
	for name, msg := range map[string][]byte{"own add": selfAdd, "service's add": dsAdd, "service's extensions": dsExt} {
		if _, err := g.Process(msg); err == nil {
			t.Errorf("%s became pending", name)
		}
	}
	if len(g.PendingProposals()) != 0 {
		t.Fatal("a proposal from outside is pending")
	}
	if _, err := g.Encrypt([]byte("hi"), nil); err != nil {
		t.Fatalf("encrypt after refusals: %v", err)
	}

	// The application may accept what it decides to.
	var asked []Proposal
	g.cfg.AcceptProposal = func(p Proposal) error {
		asked = append(asked, p)
		return nil
	}
	if _, err := g.Process(selfAdd); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 || asked[0].Sender.Kind != SenderNewMember || asked[0].Kind != ProposalAdd {
		t.Fatalf("asked %+v", asked)
	}
	if _, err := g.Process(selfAdd); err == nil {
		t.Fatal("a proposal pending twice")
	}

	// An add whose key package has expired is refused on arrival, so it
	// never blocks Encrypt or a commit.
	g.DiscardProposal(asked[0].Ref)
	past := time.Now().Add(-48 * time.Hour)
	stale, _, err := NewKeyPackage(mallory.signer, mallory.cred, KeyPackageOptions{
		Lifetime: Lifetime{NotBefore: uint64(past.Unix()), NotAfter: uint64(past.Add(time.Hour).Unix())},
	})
	if err != nil {
		t.Fatal(err)
	}
	skp := stale.kp
	if _, err := g.Process(proposalFrom(t, mallory.signer, sender{typ: senderNewMemberProposal}, g.GroupID(), g.Epoch(),
		&proposal{typ: proposalAdd, add: &skp})); err == nil {
		t.Fatal("an expired add became pending")
	}
}

// A member's own public proposal, echoed back, is its own message, not a
// second pending entry.
func TestOwnProposalEcho(t *testing.T) {
	alice, bob, carol := trio(t, Config{PublicHandshake: true})
	prop, err := alice.group.ProposeRemove(carol.group.Leaf(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := alice.group.Process(prop); !errors.Is(err, ErrOwnMessage) {
		t.Fatalf("echo: %v", err)
	}
	if n := len(alice.group.PendingProposals()); n != 1 {
		t.Fatalf("%d pending", n)
	}
	deliver(t, prop, bob)
}

// Changes says who proposed each change: carrying Carol's own removal
// reads apart from removing her inline.
func TestChangesProposers(t *testing.T) {
	describe := func(byRef bool) Changes {
		alice, bob, carol := trio(t, Config{PublicHandshake: true})
		opts := CommitOptions{Remove: []uint32{carol.group.Leaf()}}
		if byRef {
			prop, err := carol.group.ProposeRemove(carol.group.Leaf(), nil)
			if err != nil {
				t.Fatal(err)
			}
			deliver(t, prop, alice, bob)
			opts = CommitOptions{IncludePending: true}
		}
		sc, err := bob.group.Commit(opts)
		if err != nil {
			t.Fatal(err)
		}
		if mine := sc.Changes().Proposals; len(mine) != 1 {
			t.Fatalf("the committer's own Changes list %d proposals", len(mine))
		}
		m, err := alice.group.Process(sc.Message())
		if err != nil {
			t.Fatal(err)
		}
		return m.Commit.Changes()
	}
	leave, kicked := describe(true), describe(false)
	if len(leave.Proposals) != 1 || len(kicked.Proposals) != 1 {
		t.Fatalf("proposals %d and %d", len(leave.Proposals), len(kicked.Proposals))
	}
	lp, kp := leave.Proposals[0], kicked.Proposals[0]
	if lp.Kind != ProposalRemove || string(lp.Sender.Credential.Identity) != "carol" || lp.Ref == nil {
		t.Fatalf("carried leave %+v", lp)
	}
	if kp.Kind != ProposalRemove || string(kp.Sender.Credential.Identity) != "bob" || kp.Ref != nil {
		t.Fatalf("inline removal %+v", kp)
	}

	// An add's proposal names the leaf it took.
	alice, _ := pair(t, Config{})
	dave := newClient(t, testSuite, "dave")
	sc, err := alice.group.Commit(CommitOptions{Add: []*KeyPackage{dave.kp}})
	if err != nil {
		t.Fatal(err)
	}
	ch := sc.Changes()
	if len(ch.Proposals) != 1 || ch.Proposals[0].Member.Leaf != ch.Added[0].Leaf {
		t.Fatalf("add proposal %+v, added %+v", ch.Proposals, ch.Added)
	}
}

// A leaf keeps its credential through a path unless the application
// accepts the new one as its successor, and Changes shows what it was.
func TestCredentialSuccessor(t *testing.T) {
	rename := func(cfg Config) (*Message, error) {
		alice, bob := pair(t, cfg)
		x := leafNodeIndex(bob.group.Leaf())
		renamed := *bob.group.tree.nodes[x].leaf
		renamed.credential = BasicCredential([]byte("carol"))
		bob.group.tree.nodes[x].leaf = &renamed
		sc, err := bob.group.Commit(CommitOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return alice.group.Process(sc.Message())
	}
	if _, err := rename(Config{PublicHandshake: true}); err == nil {
		t.Fatal("a path renamed its leaf")
	}
	var seen [][2]string
	m, err := rename(Config{PublicHandshake: true, CheckSuccessor: func(old, new Credential) error {
		seen = append(seen, [2]string{string(old.Identity), string(new.Identity)})
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	ch := m.Commit.Changes()
	if len(seen) != 1 || seen[0] != [2]string{"bob", "carol"} {
		t.Fatalf("asked %v", seen)
	}
	if len(ch.Replaced) != 1 || string(ch.Replaced[0].Credential.Identity) != "bob" || string(ch.Updated[0].Credential.Identity) != "carol" {
		t.Fatalf("changes %+v", ch)
	}
}

// A group whose handshakes are public refuses a commit sent encrypted.
func TestPrivateHandshakeRefused(t *testing.T) {
	alice, bob := pair(t, Config{PublicHandshake: true})
	bob.group.cfg.PublicHandshake = false
	sc, err := bob.group.Commit(CommitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if info, err := PeekMessage(sc.Message()); err != nil || info.WireFormat != WireFormatPrivateMessage {
		t.Fatalf("%+v, %v", info, err)
	}
	if _, err := alice.group.Process(sc.Message()); err == nil {
		t.Fatal("an encrypted commit was staged")
	}
}

// A tree of more than MaxLeaves leaves is refused while it is decoded.
func TestTreeBound(t *testing.T) {
	huge := encode(func(w *writer) {
		w.nested(func(w *writer) {
			for range 2 * MaxLeaves {
				w.u8(0) // a blank node
			}
		})
	})
	if _, err := parseTree(huge); err == nil {
		t.Fatal("a tree past MaxLeaves parsed")
	}
}

// What the package returns shares no memory with the group's tree.
func TestReturnedCopies(t *testing.T) {
	alice, _ := pair(t, Config{})
	before := alice.group.tree.treeHash(alice.group.s)
	for _, m := range alice.group.Members() {
		m.Credential.Identity[0] ^= 1
		m.Capabilities.CipherSuites[0] = 0
	}
	if !bytes.Equal(before, alice.group.tree.treeHash(alice.group.s)) {
		t.Fatal("changing a returned member changed the tree")
	}
	kp := newClient(t, testSuite, "dave").kp
	c := kp.Credential()
	c.Identity[0] ^= 1
	if string(kp.Credential().Identity) != "dave" {
		t.Fatal("changing a returned credential changed the key package")
	}
}

// An external joiner's leaf claims the extensions it was told it
// supports, not the group's.
func TestExternalJoinerCapabilities(t *testing.T) {
	const ext ExtensionType = 0xF0F0
	cfg := Config{PublicHandshake: true}
	alice := newClient(t, testSuite, "alice")
	var err error
	alice.group, err = CreateGroup(randomID(t), alice.signer, alice.cred,
		CreateOptions{Extensions: []Extension{RequiredCapabilitiesExtension([]ExtensionType{ext}, nil, nil)},
			SupportedExtensions: []ExtensionType{ext}}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	gi, err := alice.group.GroupInfo(GroupInfoOptions{RatchetTree: true, ExternalPub: true})
	if err != nil {
		t.Fatal(err)
	}
	bob := newClient(t, testSuite, "bob")
	if _, _, err := JoinExternal(gi, bob.signer, bob.cred, ExternalJoinOptions{}, cfg); err == nil {
		t.Fatal("a joiner that supports no required extension joined")
	}
	g, msg, err := JoinExternal(gi, bob.signer, bob.cred, ExternalJoinOptions{SupportedExtensions: []ExtensionType{ext}}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	m, err := alice.group.Process(msg)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(m.Commit.Changes().Added[0].Capabilities.Extensions, ext) || g == nil {
		t.Fatal("the joiner's leaf does not list the extension")
	}
}

// A key package's init key takes an application's sealed bytes, which
// only its secrets open, under the same label and context.
func TestKeyPackageSeal(t *testing.T) {
	c := newClient(t, testSuite, "carol")
	sealed, err := c.kp.Seal("conversation history", []byte("ctx"), []byte("the key"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.secrets.Open(c.kp, "conversation history", []byte("ctx"), sealed)
	if err != nil || string(got) != "the key" {
		t.Fatalf("%q, %v", got, err)
	}
	if _, err := c.secrets.Open(c.kp, "other label", []byte("ctx"), sealed); err == nil {
		t.Fatal("opened under another label")
	}
	if _, err := c.secrets.Open(c.kp, "conversation history", []byte("other"), sealed); err == nil {
		t.Fatal("opened under another context")
	}
	other := newClient(t, testSuite, "dave")
	if _, err := other.secrets.Open(c.kp, "conversation history", []byte("ctx"), sealed); err == nil {
		t.Fatal("opened with another key package's secrets")
	}
	for _, label := range []string{"Welcome", "UpdatePathNode", ""} {
		if _, err := c.kp.Seal(label, nil, nil); err == nil {
			t.Errorf("sealed under %q", label)
		}
	}
}

// Merging a removal deletes every secret; nothing works after it.
func TestRemovalDeletesSecrets(t *testing.T) {
	alice, bob := pair(t, Config{})
	sc, err := alice.group.Commit(CommitOptions{Remove: []uint32{bob.group.Leaf()}})
	if err != nil {
		t.Fatal(err)
	}
	m, err := bob.group.Process(sc.Message())
	if err != nil {
		t.Fatal(err)
	}
	if err := bob.group.Merge(m.Commit); err != nil {
		t.Fatal(err)
	}
	g := bob.group
	if g.Active() || g.st != nil || g.es.init != nil || g.es.exporter != nil || len(g.priv.keys) != 0 || g.resumption != nil {
		t.Fatal("a removed member keeps secrets")
	}
	if _, err := g.Encrypt([]byte("x"), nil); !errors.Is(err, ErrInactive) {
		t.Fatalf("encrypt: %v", err)
	}
	if _, err := g.Export("x", nil, 32); !errors.Is(err, ErrInactive) {
		t.Fatalf("export: %v", err)
	}
}
