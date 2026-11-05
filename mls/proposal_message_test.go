// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package mls

import (
	"strings"
	"testing"
)

// A proposal on its own is checked against the epoch before it becomes
// pending, by the rule of its kind.
func TestProposalMessageRefusals(t *testing.T) {
	alice, bob, _ := trio(t, Config{})
	ps := &alice.group.publicState
	stranger := newClient(t, testSuite, "stranger")
	strangerKP := stranger.kp.kp
	bobKP := bob.kp.kp
	bobLeaf := *ps.tree.leaf(bob.group.Leaf())
	sameKey := bobLeaf
	sameKey.source = leafSourceUpdate
	badSource := bobLeaf
	badSource.source = leafSourceKeyPackage
	other := bobLeaf
	other.source = leafSourceUpdate
	other.encryptionKey = append([]byte{}, bobLeaf.encryptionKey...)
	other.encryptionKey[0] ^= 0xff
	other.credential = BasicCredential([]byte("someone else"))
	nonce := make([]byte, ps.s.nh)
	member := memberSender(bob.group.Leaf())

	cases := []struct {
		name   string
		sd     sender
		pr     proposal
		reason string
	}{
		{"an external init", member, proposal{typ: proposalExternalInit}, "only valid inline"},
		{"an update from the service", sender{typ: senderExternal}, proposal{typ: proposalUpdate, update: &bobLeaf}, "only a member proposes an update"},
		{"an update with the wrong source", member, proposal{typ: proposalUpdate, update: &badSource}, "new encryption key"},
		{"an update that keeps its key", member, proposal{typ: proposalUpdate, update: &sameKey}, "new encryption key"},
		{"an update that changes the credential", member, proposal{typ: proposalUpdate, update: &other}, "not its old one"},
		{"a remove of a leaf holding no member", member, proposal{typ: proposalRemove, remove: 40}, "holds no member"},
		{"an add of a client in the group", member, proposal{typ: proposalAdd, add: &bobKP}, "already in the group"},
		{"a PSK with a short nonce", member, proposal{typ: proposalPSK, psk: preSharedKeyID{typ: pskExternal, id: []byte("k"), nonce: nonce[:2]}}, "wrong nonce or usage"},
		{"a resumption PSK for a branch", member, proposal{typ: proposalPSK, psk: preSharedKeyID{typ: pskResumption, usage: resumptionBranch, nonce: nonce}}, "wrong nonce or usage"},
		{"extensions a member does not support", member, proposal{typ: proposalGroupContextExtensions, extensions: []Extension{{Type: 0xf010}}}, "does not support"},
		{"a reinit to an older version", member, proposal{typ: proposalReInit, reinit: reInit{version: 0, suite: testSuite}}, "older version"},
	}
	for _, c := range cases {
		pr := c.pr
		a := &authenticatedContent{wireFormat: WireFormatPublicMessage, content: framedContent{
			groupID: alice.group.GroupID(), epoch: alice.group.Epoch(), sender: c.sd, contentType: contentProposal, proposal: &pr,
		}}
		_, err := ps.checkProposalMessage(&alice.group.cfg, nil, a)
		if err == nil {
			t.Errorf("%s: accepted", c.name)
		} else if !strings.Contains(err.Error(), c.reason) {
			t.Errorf("%s: refused for %q, want %q", c.name, err, c.reason)
		}
	}
	// The same proposal twice is pending once.
	pr := proposal{typ: proposalRemove, remove: bob.group.Leaf()}
	a := &authenticatedContent{wireFormat: WireFormatPublicMessage, content: framedContent{
		groupID: alice.group.GroupID(), epoch: alice.group.Epoch(), sender: memberSender(0), contentType: contentProposal, proposal: &pr,
	}}
	pp, err := ps.checkProposalMessage(&alice.group.cfg, nil, a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ps.checkProposalMessage(&alice.group.cfg, []pendingProposal{pp}, a); err == nil || !strings.Contains(err.Error(), "already pending") {
		t.Errorf("a proposal pending twice: %v", err)
	}
	_ = strangerKP
}

// A public message is held to its sender's kind before its signature is
// looked at.
func TestPublicMessageSenders(t *testing.T) {
	service, err := GenerateSignatureKey(testSuite)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{PublicHandshake: true}
	alice := newClient(t, testSuite, "alice")
	alice.group, err = CreateGroup(randomID(t), alice.signer, alice.cred, CreateOptions{Extensions: []Extension{
		ExternalSendersExtension([]ExternalSender{{SignatureKey: service.PublicKey(), Credential: BasicCredential([]byte("ds"))}}),
	}}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	g := alice.group
	stranger := newClient(t, testSuite, "stranger")
	kp := stranger.kp.kp
	add := &proposal{typ: proposalAdd, add: &kp}
	update := &proposal{typ: proposalUpdate, update: &kp.leaf}
	cases := []struct {
		name    string
		signer  *SignatureKey
		sd      sender
		groupID []byte
		epoch   uint64
		pr      *proposal
		reason  string
	}{
		{"another group", stranger.signer, sender{typ: senderNewMemberProposal}, []byte("elsewhere"), g.Epoch(), add, "another group"},
		{"another epoch", stranger.signer, sender{typ: senderNewMemberProposal}, g.GroupID(), g.Epoch() + 5, add, "epoch"},
		{"a member who is not there", alice.signer, sender{typ: senderMember, index: 40}, g.GroupID(), g.Epoch(), add, "no member at sender leaf"},
		{"the service proposing an update", service, sender{typ: senderExternal}, g.GroupID(), g.Epoch(), update, "external sender may send only"},
		{"a service that is not listed", service, sender{typ: senderExternal, index: 3}, g.GroupID(), g.Epoch(), add, "no external sender"},
		{"a new member proposing an update", stranger.signer, sender{typ: senderNewMemberProposal}, g.GroupID(), g.Epoch(), update, "only its own add"},
	}
	for _, c := range cases {
		msg := proposalFrom(t, c.signer, c.sd, c.groupID, c.epoch, c.pr)
		_, err := g.Process(msg)
		if err == nil {
			t.Errorf("%s: accepted", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.reason) {
			t.Errorf("%s: refused for %q, want %q", c.name, err, c.reason)
		}
	}
}
