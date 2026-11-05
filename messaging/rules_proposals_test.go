// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package messaging

import (
	"errors"
	"testing"

	"latere.ai/x/sdk-go/mls"
)

func asMember(d *device) mls.Sender {
	return mls.Sender{Kind: mls.SenderMember, Credential: d.id.Credential()}
}

func leafMember(d *device) *mls.Member {
	return &mls.Member{Credential: d.id.Credential(), SignatureKey: d.record.SignatureKey, Capabilities: suiteCaps()}
}

// A proposal is refused for the reason the rule names, by a policy and by
// the state alone, which is what every group checks without a directory.
func TestProposalRefusals(t *testing.T) {
	w, alice, bob, _ := trio(t)
	p := w.policy()
	members := alice.group.Members()
	junk := mls.Sender{Kind: mls.SenderMember, Credential: mls.BasicCredential([]byte("nobody"))}
	x509 := mls.Sender{Kind: mls.SenderMember, Credential: mls.Credential{Type: mls.CredentialX509}}
	l := w.latere("x")
	dave := w.device("dave", "phone")
	aliceTwo := w.device("alice", "laptop")
	s := state(t, alice.group)
	s.Level = LevelReadsAlong

	cases := []struct {
		name   string
		pr     mls.Proposal
		reason string
	}{
		{"a sender that is neither a member nor the service", mls.Proposal{Kind: mls.ProposalAdd, Sender: mls.Sender{Kind: mls.SenderNewMember}}, "a member or the service"},
		{"a sender with a credential that is not a device", mls.Proposal{Kind: mls.ProposalAdd, Sender: junk}, ""},
		{"a sender with a credential of another type", mls.Proposal{Kind: mls.ProposalAdd, Sender: x509}, ""},
		{"Latere proposing", mls.Proposal{Kind: mls.ProposalAdd, Sender: asMember(l)}, "proposes nothing"},
		{"an add naming no device", mls.Proposal{Kind: mls.ProposalAdd, Sender: asMember(alice)}, "names the device"},
		{"an add of a member with a bad credential", mls.Proposal{Kind: mls.ProposalAdd, Sender: asMember(alice), Member: &mls.Member{Credential: mls.BasicCredential([]byte("nobody"))}}, ""},
		{"a non-owner letting Latere in", mls.Proposal{Kind: mls.ProposalAdd, Sender: asMember(bob), Member: leafMember(l)}, "only an owner lets Latere in"},
		{"a person adding another person's device", mls.Proposal{Kind: mls.ProposalAdd, Sender: asMember(bob), Member: leafMember(alice)}, "adds their own devices"},
		{"a non-owner adding a person", mls.Proposal{Kind: mls.ProposalAdd, Sender: asMember(bob), Member: leafMember(dave)}, "only an owner adds a person"},
		{"an add the directory does not vouch for", mls.Proposal{Kind: mls.ProposalAdd, Sender: asMember(alice), Member: &mls.Member{Credential: dave.id.Credential(), SignatureKey: aliceTwo.record.SignatureKey, Capabilities: suiteCaps()}}, ""},
		{"a leaf outside the suite", mls.Proposal{Kind: mls.ProposalAdd, Sender: asMember(alice), Member: &mls.Member{Credential: dave.id.Credential(), SignatureKey: dave.record.SignatureKey}}, "the suite of conversations"},
		{"a remove of a leaf not in the group", mls.Proposal{Kind: mls.ProposalRemove, Sender: asMember(alice), Leaf: 99}, "not in the group"},
		{"a non-owner removing Latere", mls.Proposal{Kind: mls.ProposalRemove, Sender: asMember(bob), Leaf: members[0].Leaf}, "only an owner removes"},
		{"an update that changes the identity", mls.Proposal{Kind: mls.ProposalUpdate, Sender: asMember(bob), Member: leafMember(alice)}, "keeps the leaf's identity"},
		{"an update with a bad credential", mls.Proposal{Kind: mls.ProposalUpdate, Sender: asMember(bob), Member: &mls.Member{Credential: mls.BasicCredential([]byte("nobody"))}}, ""},
		{"an update of a leaf the directory does not vouch for", mls.Proposal{Kind: mls.ProposalUpdate, Sender: asMember(bob), Member: &mls.Member{Credential: bob.id.Credential(), SignatureKey: dave.record.SignatureKey, Capabilities: suiteCaps()}}, ""},
		{"a state change by a non-owner", mls.Proposal{Kind: mls.ProposalGroupContextExtensions, Sender: asMember(bob), Extensions: withState(t, alice.group, s)}, "only an owner proposes"},
		{"a state change that does not parse", mls.Proposal{Kind: mls.ProposalGroupContextExtensions, Sender: asMember(alice), Extensions: []mls.Extension{{Type: StateExtension, Data: []byte{0xff}}}}, "the proposal's state"},
		{"a state change that edits another extension", mls.Proposal{Kind: mls.ProposalGroupContextExtensions, Sender: asMember(alice), Extensions: append(withState(t, alice.group, s), mls.Extension{Type: 0xff01, Data: []byte{1}})}, "no extension but the conversation's state"},
		{"a proposal of a kind a conversation does not take", mls.Proposal{Kind: mls.ProposalPSK, Sender: asMember(alice)}, "takes no"},
		{"the service removing a leaf not in the group", mls.Proposal{Kind: mls.ProposalRemove, Sender: mls.Sender{Kind: mls.SenderExternal}, Leaf: 99}, "not in the group"},
	}
	for _, c := range cases {
		err := p.CheckProposal(alice.group, c.pr)
		if err == nil {
			t.Errorf("%s: accepted", c.name)
			continue
		}
		if c.reason != "" {
			refused(t, err, c.reason)
		}
	}
}

// With no policy a group decides on the state alone: the service may
// remove a device of a person, never Latere's member, and an update or an
// add is not checked against a directory.
func TestProposalsWithoutAPolicy(t *testing.T) {
	w, alice, bob, _ := trio(t)
	dave := w.device("dave", "phone")
	service := mls.Sender{Kind: mls.SenderExternal}
	members := alice.group.Members()
	for _, m := range members {
		if err := checkProposal(alice.group, mls.Proposal{Kind: mls.ProposalRemove, Sender: service, Leaf: m.Leaf}, nil); err != nil {
			t.Errorf("the service removing leaf %d: %v", m.Leaf, err)
		}
	}
	if err := checkProposal(alice.group, mls.Proposal{Kind: mls.ProposalUpdate, Sender: asMember(bob), Member: leafMember(bob)}, nil); err != nil {
		t.Errorf("an update with no policy: %v", err)
	}
	if err := checkProposal(alice.group, mls.Proposal{Kind: mls.ProposalAdd, Sender: asMember(alice), Member: leafMember(dave)}, nil); err != nil {
		t.Errorf("an add of a person with no policy: %v", err)
	}
}

// A policy with no directory cannot vouch for a person's leaf or a
// removal, and says so rather than letting either through.
func TestPolicyWithoutADirectory(t *testing.T) {
	w, alice, bob, _ := trio(t)
	p := &Policy{Installation: w.policy().Installation}
	members := alice.group.Members()
	err := p.CheckProposal(alice.group, mls.Proposal{Kind: mls.ProposalRemove, Sender: mls.Sender{Kind: mls.SenderExternal}, Leaf: members[1].Leaf})
	if err == nil || errors.Is(err, ErrRefused) {
		t.Errorf("a removal with no directory: %v", err)
	}
	err = p.CheckProposal(alice.group, mls.Proposal{Kind: mls.ProposalUpdate, Sender: asMember(bob), Member: leafMember(bob)})
	if err == nil || errors.Is(err, ErrRefused) {
		t.Errorf("an update with no directory: %v", err)
	}
}

// A directory that fails stops the check with its error.
func TestPolicyDirectoryFailure(t *testing.T) {
	w, alice, bob, _ := trio(t)
	boom := errors.New("directory unavailable")
	p := w.policy()
	p.Directory = func(string) (*Directory, error) { return nil, boom }
	members := alice.group.Members()
	if err := p.CheckProposal(alice.group, mls.Proposal{Kind: mls.ProposalRemove, Sender: mls.Sender{Kind: mls.SenderExternal}, Leaf: members[1].Leaf}); !errors.Is(err, boom) {
		t.Errorf("a removal: %v", err)
	}
	if err := p.CheckProposal(alice.group, mls.Proposal{Kind: mls.ProposalUpdate, Sender: asMember(bob), Member: leafMember(bob)}); !errors.Is(err, boom) {
		t.Errorf("an update: %v", err)
	}
}

func TestCheckMessageRefusals(t *testing.T) {
	w := newWorld(t)
	l := w.latere("x")
	refused(t, CheckMessage(State{}, mls.Sender{Kind: mls.SenderExternal}), "from a member")
	if err := CheckMessage(State{}, mls.Sender{Kind: mls.SenderMember, Credential: mls.BasicCredential([]byte("nobody"))}); err == nil {
		t.Error("a sender with a credential that is not a device was accepted")
	}
	refused(t, CheckMessage(State{Level: LevelReadsAlong}, asMember(l)), "takes part")
	if err := CheckMessage(State{Level: LevelTakesPart}, asMember(l)); err != nil {
		t.Errorf("Latere taking part: %v", err)
	}
}

// suiteCaps are the capabilities of a leaf in the suite of conversations.
func suiteCaps() mls.Capabilities {
	return mls.Capabilities{CipherSuites: []mls.CipherSuite{Suite}}
}
