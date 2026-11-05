// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package mls

import (
	"strings"
	"testing"
	"time"
)

// The proposal rules of RFC 9420, Section 12.2, are checked on a list a
// commit covers, whoever built it. These tests hand the check lists no
// honest client builds, so each rule's refusal is held to its reason.

func rp(sd sender, pr proposal) resolvedProposal { return resolvedProposal{p: &pr, sender: sd} }

func memberSender(i uint32) sender { return sender{typ: senderMember, index: i} }

func TestApplyProposalsRefusals(t *testing.T) {
	alice, bob, carol := trio(t, Config{})
	g := alice.group
	ps := &g.publicState
	stranger := newClient(t, testSuite, "stranger")
	strangerKP := stranger.kp.kp
	bobKP := bob.kp.kp
	bobLeaf := *ps.tree.leaf(bob.group.Leaf())
	nonce := make([]byte, ps.s.nh)
	psk := func(n []byte, typ pskType, usage resumptionUsage, id string) proposal {
		return proposal{typ: proposalPSK, psk: preSharedKeyID{typ: typ, id: []byte(id), usage: usage, nonce: n}}
	}
	badSource := bobLeaf
	badSource.source = leafSourceKeyPackage
	sameKey := bobLeaf
	sameKey.source = leafSourceUpdate
	external := sender{typ: senderNewMemberCommit}
	service := sender{typ: senderExternal}
	newcomer := sender{typ: senderNewMemberProposal}
	committer := memberSender(g.Leaf())
	remove := func(i uint32) proposal { return proposal{typ: proposalRemove, remove: i} }

	cases := []struct {
		name      string
		committer sender
		props     []resolvedProposal
		reason    string
	}{
		{"an external commit with an add", external, []resolvedProposal{
			rp(external, proposal{typ: proposalExternalInit}), rp(external, proposal{typ: proposalAdd, add: &strangerKP})}, "carries a proposal of type"},
		{"an external commit with no external init", external, []resolvedProposal{rp(external, psk(nonce, pskExternal, 0, "k"))}, "exactly one external init"},
		{"an external commit that removes a member", external, []resolvedProposal{
			rp(external, proposal{typ: proposalExternalInit}), rp(external, remove(bob.group.Leaf()))}, "removes a member"},
		{"a member's commit with an external init", committer, []resolvedProposal{rp(committer, proposal{typ: proposalExternalInit})}, "external init"},
		{"an update the service proposes", committer, []resolvedProposal{rp(service, proposal{typ: proposalUpdate, update: &bobLeaf})}, "external sender proposed type"},
		{"an add a new member proposes that is a remove", committer, []resolvedProposal{rp(newcomer, remove(1))}, "a new member proposed type"},
		{"an update from a sender that is not a member", committer, []resolvedProposal{rp(newcomer, proposal{typ: proposalUpdate, update: &bobLeaf})}, "a new member proposed type"},
		{"the committer's own update", committer, []resolvedProposal{rp(committer, proposal{typ: proposalUpdate, update: &bobLeaf})}, "committer's own update"},
		{"two updates of one leaf", committer, []resolvedProposal{
			rp(memberSender(bob.group.Leaf()), proposal{typ: proposalUpdate, update: &bobLeaf}),
			rp(memberSender(bob.group.Leaf()), proposal{typ: proposalUpdate, update: &bobLeaf})}, "two updates or removes"},
		{"an update and a remove of one leaf", committer, []resolvedProposal{
			rp(memberSender(bob.group.Leaf()), proposal{typ: proposalUpdate, update: &bobLeaf}), rp(committer, remove(bob.group.Leaf()))}, "two updates or removes"},
		{"a remove of a leaf that holds no member", committer, []resolvedProposal{rp(committer, remove(40))}, "holds no member"},
		{"a remove of the committer", committer, []resolvedProposal{rp(committer, remove(g.Leaf()))}, "removes the committer"},
		{"two removes of one leaf", committer, []resolvedProposal{rp(committer, remove(bob.group.Leaf())), rp(committer, remove(bob.group.Leaf()))}, "two updates or removes"},
		{"two adds of one client", committer, []resolvedProposal{
			rp(committer, proposal{typ: proposalAdd, add: &strangerKP}), rp(committer, proposal{typ: proposalAdd, add: &strangerKP})}, "two adds of the same client"},
		{"an add of a client in the group", committer, []resolvedProposal{rp(committer, proposal{typ: proposalAdd, add: &bobKP})}, "already in the group"},
		{"a PSK with a short nonce", committer, []resolvedProposal{rp(committer, psk(nonce[:3], pskExternal, 0, "k"))}, "PSK nonce"},
		{"a resumption PSK for a branch", committer, []resolvedProposal{rp(committer, psk(nonce, pskResumption, resumptionReInit, ""))}, "resumption PSK"},
		{"the same PSK twice", committer, []resolvedProposal{rp(committer, psk(nonce, pskExternal, 0, "k")), rp(committer, psk(nonce, pskExternal, 0, "k"))}, "same PSK twice"},
		{"two extensions proposals", committer, []resolvedProposal{
			rp(committer, proposal{typ: proposalGroupContextExtensions}), rp(committer, proposal{typ: proposalGroupContextExtensions})}, "two group context extensions"},
		{"an extensions proposal that repeats a type", committer, []resolvedProposal{
			rp(committer, proposal{typ: proposalGroupContextExtensions, extensions: []Extension{{Type: 0xf001}, {Type: 0xf001}}})}, ""},
		{"a reinit to an older version", committer, []resolvedProposal{rp(committer, proposal{typ: proposalReInit, reinit: reInit{version: 0, suite: testSuite}})}, "older version"},
		{"a reinit with another proposal", committer, []resolvedProposal{
			rp(committer, proposal{typ: proposalReInit, reinit: reInit{version: protocolVersionMLS10, suite: testSuite}}), rp(committer, remove(bob.group.Leaf()))}, "with other proposals"},
		{"an update with the wrong source", committer, []resolvedProposal{rp(memberSender(bob.group.Leaf()), proposal{typ: proposalUpdate, update: &badSource})}, "has source"},
		{"an update that keeps its encryption key", committer, []resolvedProposal{rp(memberSender(bob.group.Leaf()), proposal{typ: proposalUpdate, update: &sameKey})}, "keeps its encryption key"},
		{"an update of a leaf holding no member", committer, []resolvedProposal{rp(memberSender(40), proposal{typ: proposalUpdate, update: &sameKey})}, "keeps its encryption key"},
		{"an add of a client the group requires more of", committer, []resolvedProposal{
			rp(committer, proposal{typ: proposalGroupContextExtensions, extensions: []Extension{RequiredCapabilitiesExtension([]ExtensionType{0xf002}, nil, nil)}}),
			rp(committer, proposal{typ: proposalAdd, add: &strangerKP})}, "required extension"},
	}
	for _, c := range cases {
		_, err := ps.applyProposals(&g.cfg, c.props, c.committer)
		if err == nil {
			t.Errorf("%s: accepted", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.reason) {
			t.Errorf("%s: refused for %q, want %q", c.name, err, c.reason)
		}
	}
	_ = carol
}

// A key package outside its lifetime, or valid for longer than the group
// allows, is refused on an add.
func TestAddedKeyPackageLifetime(t *testing.T) {
	alice, _, _ := trio(t, Config{})
	g := alice.group
	ps := &g.publicState
	stranger := newClient(t, testSuite, "stranger")
	now := time.Now()
	for name, lt := range map[string]Lifetime{
		"expired":  {NotBefore: uint64(now.Add(-48 * time.Hour).Unix()), NotAfter: uint64(now.Add(-24 * time.Hour).Unix())},
		"not yet":  {NotBefore: uint64(now.Add(24 * time.Hour).Unix()), NotAfter: uint64(now.Add(48 * time.Hour).Unix())},
		"too long": {NotBefore: uint64(now.Add(-time.Hour).Unix()), NotAfter: uint64(now.Add(1000 * time.Hour).Unix())},
	} {
		kp, _, err := NewKeyPackage(stranger.signer, stranger.cred, KeyPackageOptions{Lifetime: lt})
		if err != nil {
			t.Fatal(err)
		}
		cfg := g.cfg
		cfg.MaxLifetime = 24 * time.Hour
		inner := kp.kp
		if err := ps.checkKeyPackage(&cfg, &inner, nil); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// A commit names a pending proposal by its reference, once, and an
// external commit names none.
func TestResolveProposalRefusals(t *testing.T) {
	committer := memberSender(0)
	pending := []pendingProposal{{ref: []byte("r1"), proposal: &proposal{typ: proposalRemove, remove: 1}, sender: committer}}
	byRef := func(refs ...string) *commit {
		c := &commit{}
		for _, r := range refs {
			c.proposals = append(c.proposals, proposalOrRef{reference: []byte(r)})
		}
		return c
	}
	if got, err := resolveProposals(pending, committer, byRef("r1")); err != nil || len(got) != 1 || got[0].ref == nil {
		t.Fatalf("a pending proposal: %v, %v", got, err)
	}
	if _, err := resolveProposals(pending, sender{typ: senderNewMemberCommit}, byRef("r1")); err == nil || !strings.Contains(err.Error(), "by reference") {
		t.Errorf("an external commit naming a proposal: %v", err)
	}
	if _, err := resolveProposals(pending, committer, byRef("nope")); err == nil || !strings.Contains(err.Error(), "does not have") {
		t.Errorf("an unknown proposal: %v", err)
	}
	if _, err := resolveProposals(pending, committer, byRef("r1", "r1")); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Errorf("a proposal named twice: %v", err)
	}
}

// Every member must support the group's extensions, the credential types
// in use and what the group requires.
func TestGroupExtensionSupport(t *testing.T) {
	alice, _, _ := trio(t, Config{})
	ps := &alice.group.publicState
	l := ps.tree.leaf(alice.group.Leaf())
	for name, exts := range map[string][]Extension{
		"an extension no member lists":     {{Type: 0xf003}},
		"a required extension":             {RequiredCapabilitiesExtension([]ExtensionType{0xf004}, nil, nil)},
		"a required proposal":              {RequiredCapabilitiesExtension(nil, []ProposalKind{0xf005}, nil)},
		"a required credential":            {RequiredCapabilitiesExtension(nil, nil, []CredentialType{0xf006})},
		"required capabilities that parse": {{Type: ExtensionRequiredCapabilities, Data: []byte{0xff}}},
	} {
		if err := checkLeafSupports(l, exts); err == nil {
			t.Errorf("%s: a leaf supported it", name)
		}
		if err := ps.checkGroupExtensions(ps.tree, exts); err == nil {
			t.Errorf("%s: the group took it", name)
		}
	}
	if err := ps.checkGroupExtensions(ps.tree, []Extension{{Type: 0xf007}, {Type: 0xf007}}); err == nil {
		t.Error("the group took a repeated extension")
	}
	if err := ps.checkGroupExtensions(ps.tree, []Extension{{Type: ExtensionExternalSenders, Data: []byte{0xff}}}); err == nil {
		t.Error("the group took external senders that do not parse")
	}
	// A member that cannot carry the credential type in use is refused.
	narrow := *ps.tree.leaf(alice.group.Leaf())
	narrow.capabilities = narrow.capabilities.clone()
	narrow.capabilities.Credentials = nil
	tree := ps.tree.clone()
	tree.nodes[leafNodeIndex(alice.group.Leaf())] = node{leaf: &narrow}
	if err := ps.checkGroupExtensions(tree, nil); err == nil || !strings.Contains(err.Error(), "credential type") {
		t.Errorf("a member without the credential type in use: %v", err)
	}
}
