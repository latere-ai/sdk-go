// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package messaging

import (
	"crypto/ed25519"
	"crypto/rand"
	"slices"
	"testing"

	"latere.ai/x/sdk-go/mls"
)

// letIn is the options of a commit by by that adds the devices with the
// state s and a notice sharing nothing.
func letIn(t testing.TB, by *device, s State, joiners ...*device) mls.CommitOptions {
	t.Helper()
	opts := mls.CommitOptions{ReplaceExtensions: true, Extensions: withState(t, by.group, s)}
	var js []Joiner
	for _, j := range joiners {
		opts.Add = append(opts.Add, j.kp)
		js = append(js, Joiner{KeyPackage: j.kp, Notice: JoinerNotice{Range: HistoryRange{Kind: RangeNone}}})
	}
	ad, err := NewCommitNotice(by.group, HistoryRange{Kind: RangeNone}, js)
	if err != nil {
		t.Fatal(err)
	}
	opts.AuthenticatedData = ad
	return opts
}

// propose hands a proposal message to the service and each device,
// which check it and keep it pending.
func (w *world) propose(msg []byte, to ...*device) {
	w.t.Helper()
	m, err := w.view.Process(msg)
	if err != nil {
		w.t.Fatalf("the service processing a proposal: %v", err)
	}
	if err := CheckProposal(w.view, *m.Proposal); err != nil {
		w.t.Fatalf("the service checking a proposal: %v", err)
	}
	for _, d := range to {
		m, err := d.group.Process(msg)
		if err != nil {
			w.t.Fatalf("%s processing a proposal: %v", d.id, err)
		}
		if err := CheckProposal(d.group, *m.Proposal); err != nil {
			w.t.Fatalf("%s checking a proposal: %v", d.id, err)
		}
	}
}

// trio is a group conversation of alice, the owner, with bob and carol.
func trio(t *testing.T) (w *world, alice, bob, carol *device) {
	w = newWorld(t)
	alice, bob, carol = w.device("alice", "phone"), w.device("bob", "phone"), w.device("carol", "phone")
	w.create(alice, KindGroup)
	s := state(t, alice.group)
	s.People = append(s.People, "bob", "carol")
	ch, err := w.commit(alice, letIn(t, alice, s, bob, carol), nil, bob, carol)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ch.Joined, []string{"bob", "carol"}) || ch.By != alice.id {
		t.Fatalf("change %+v", ch)
	}
	return w, alice, bob, carol
}

func TestOwnerAddsPeople(t *testing.T) {
	_, alice, bob, carol := trio(t)
	for _, d := range []*device{alice, bob, carol} {
		if s := state(t, d.group); !slices.Equal(s.People, []string{"alice", "bob", "carol"}) || !slices.Equal(s.Owners, []string{"alice"}) {
			t.Fatalf("%s's state %+v", d.id, s)
		}
	}
}

func TestOnlyOwnersAddPeople(t *testing.T) {
	w, alice, bob, carol := trio(t)
	dave := w.device("dave", "phone")
	s := state(t, bob.group)
	s.People = append(s.People, "dave")
	_, err := w.commit(bob, letIn(t, bob, s, dave), []*device{alice, carol})
	refused(t, err, "only an owner adds a person")
}

func TestAddNeedsStateAndNotice(t *testing.T) {
	w, alice, bob, carol := trio(t)
	dave := w.device("dave", "phone")

	// The state does not list dave.
	_, err := w.commit(alice, letIn(t, alice, state(t, alice.group), dave), []*device{bob, carol})
	refused(t, err, "lists the people it leaves")

	// No notice.
	s := state(t, alice.group)
	s.People = append(s.People, "dave")
	opts := letIn(t, alice, s, dave)
	opts.AuthenticatedData = nil
	_, err = w.commit(alice, opts, []*device{bob, carol})
	refused(t, err, "carries its history notice")
}

func TestAddChecksTheDirectory(t *testing.T) {
	w, alice, bob, carol := trio(t)
	dave := w.device("dave", "phone")
	// A key package for dave under a key his directory does not hold.
	other, err := mls.GenerateSignatureKey(Suite)
	if err != nil {
		t.Fatal(err)
	}
	dave.kp, dave.secrets, err = mls.NewKeyPackage(other, dave.id.Credential(), KeyPackageOptions())
	if err != nil {
		t.Fatal(err)
	}
	s := state(t, alice.group)
	s.People = append(s.People, "dave")
	_, err = w.commit(alice, letIn(t, alice, s, dave), []*device{bob, carol})
	refused(t, err, "directory record does not")
}

func TestLeave(t *testing.T) {
	w, alice, bob, carol := trio(t)
	msg, err := bob.group.ProposeRemove(leafOf(t, bob.group, bob), nil)
	if err != nil {
		t.Fatal(err)
	}
	w.propose(msg, alice, carol)
	// carol, not an owner, carries bob's leaving.
	s := state(t, carol.group)
	s.People = []string{"alice", "carol"}
	ch, err := w.commit(carol, mls.CommitOptions{IncludePending: true, ReplaceExtensions: true, Extensions: withState(t, carol.group, s)},
		[]*device{alice})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ch.Left, []string{"bob"}) || len(ch.Removed) != 0 {
		t.Fatalf("change %+v", ch)
	}
}

func TestRemovingAnother(t *testing.T) {
	w, alice, bob, carol := trio(t)

	// carol is not an owner.
	s := state(t, carol.group)
	s.People = []string{"alice", "carol"}
	_, err := w.commit(carol, mls.CommitOptions{Remove: []uint32{leafOf(t, carol.group, bob)},
		ReplaceExtensions: true, Extensions: withState(t, carol.group, s)}, []*device{alice, bob})
	refused(t, err, "only an owner removes another person")

	// alice is.
	ch, err := w.commit(alice, mls.CommitOptions{Remove: []uint32{leafOf(t, alice.group, bob)},
		ReplaceExtensions: true, Extensions: withState(t, alice.group, s)}, []*device{carol})
	if err != nil {
		t.Fatal(err)
	}
	if len(ch.Removed) != 1 || ch.Removed[0].Person != "bob" || ch.Removed[0].By != alice.id {
		t.Fatalf("change %+v", ch)
	}
}

func TestServiceRemoves(t *testing.T) {
	w, alice, bob, carol := trio(t)
	msg, err := mls.ProposeExternalRemove(w.service, bob.group.GroupID(), bob.group.Epoch(), 0, leafOf(t, bob.group, bob))
	if err != nil {
		t.Fatal(err)
	}
	w.propose(msg, alice, bob, carol)
	s := state(t, carol.group)
	s.People = []string{"alice", "carol"}
	ch, err := w.commit(carol, mls.CommitOptions{IncludePending: true, ReplaceExtensions: true, Extensions: withState(t, carol.group, s)},
		[]*device{alice})
	if err != nil {
		t.Fatal(err)
	}
	if len(ch.Removed) != 1 || !ch.Removed[0].ByService || ch.Removed[0].Person != "bob" {
		t.Fatalf("change %+v", ch)
	}
}

func TestCheckProposal(t *testing.T) {
	w, alice, _, _ := trio(t)
	l := w.latere("x")
	members := alice.group.Members()
	service := mls.Sender{Kind: mls.SenderExternal}
	for _, pr := range []mls.Proposal{
		{Kind: mls.ProposalAdd, Sender: service},
		{Kind: mls.ProposalGroupContextExtensions, Sender: service},
		{Kind: mls.ProposalRemove, Sender: service, Leaf: 99},
		{Kind: mls.ProposalPSK, Sender: mls.Sender{Kind: mls.SenderMember, Credential: alice.id.Credential()}},
		{Kind: mls.ProposalReInit, Sender: mls.Sender{Kind: mls.SenderMember, Credential: alice.id.Credential()}},
		{Kind: mls.ProposalAdd, Sender: mls.Sender{Kind: mls.SenderNewMember}},
		{Kind: mls.ProposalRemove, Sender: mls.Sender{Kind: mls.SenderMember, Credential: l.id.Credential()}},
	} {
		refused(t, CheckProposal(alice.group, pr))
	}
	ok := mls.Proposal{Kind: mls.ProposalRemove, Sender: service, Leaf: members[1].Leaf}
	if err := CheckProposal(alice.group, ok); err != nil {
		t.Fatal(err)
	}
}

func TestOwnDevices(t *testing.T) {
	w, alice, bob, carol := trio(t)
	tablet := w.device("bob", "tablet")

	// alice may not add bob's device for him.
	_, err := w.commit(alice, mls.CommitOptions{Add: []*mls.KeyPackage{tablet.kp}}, []*device{bob, carol})
	refused(t, err, "a person adds their own devices")

	tablet.fresh(t)
	ch, err := w.commit(bob, mls.CommitOptions{Add: []*mls.KeyPackage{tablet.kp}}, []*device{alice, carol}, tablet)
	if err != nil {
		t.Fatal(err)
	}
	if len(ch.DevicesAdded) != 1 || ch.DevicesAdded[0] != tablet.id || len(ch.Joined) != 0 {
		t.Fatalf("change %+v", ch)
	}

	// bob removes his phone, and stays by the tablet.
	ch, err = w.commit(tablet, mls.CommitOptions{Remove: []uint32{leafOf(t, tablet.group, bob)}}, []*device{alice, carol})
	if err != nil {
		t.Fatal(err)
	}
	if len(ch.DevicesRemoved) != 1 || ch.DevicesRemoved[0] != bob.id || len(ch.Left) != 0 {
		t.Fatalf("change %+v", ch)
	}
}

func TestExternalJoin(t *testing.T) {
	w, alice, _, _ := trio(t)
	info, err := alice.group.GroupInfo(mls.GroupInfoOptions{RatchetTree: true, ExternalPub: true})
	if err != nil {
		t.Fatal(err)
	}

	join := func(d *device) error {
		g, msg, err := mls.JoinExternal(info, d.signer, d.id.Credential(), mls.ExternalJoinOptions{SupportedExtensions: extensionTypes}, Config())
		if err != nil {
			t.Fatal(err)
		}
		m, err := alice.group.Process(msg)
		if err != nil {
			t.Fatal(err)
		}
		vm, err := w.view.Process(msg)
		if err != nil {
			t.Fatal(err)
		}
		_, verr := w.policy().CheckCommit(w.view, vm.Commit.Changes())
		ch, err := w.policy().CheckCommit(alice.group, m.Commit.Changes())
		if (verr == nil) != (err == nil) {
			t.Fatalf("alice's check %v, the service's check %v", err, verr)
		}
		if err != nil {
			return err
		}
		if !ch.ExternalJoin || ch.By != d.id || len(ch.DevicesAdded) != 1 {
			t.Fatalf("change %+v", ch)
		}
		d.group = g
		return nil
	}
	if err := join(w.device("bob", "laptop")); err != nil {
		t.Fatal(err)
	}
	// dave is not in the conversation.
	refused(t, join(w.device("dave", "phone")), "only for a person of the conversation")
}

func TestOneOnOne(t *testing.T) {
	w := newWorld(t)
	alice, bob, carol := w.device("alice", "phone"), w.device("bob", "phone"), w.device("carol", "phone")
	w.create(alice, KindOneOnOne)
	s := state(t, alice.group)
	s.People = append(s.People, "bob")
	if _, err := w.commit(alice, letIn(t, alice, s, bob), nil, bob); err != nil {
		t.Fatal(err)
	}
	// A third person: the state refuses three, and a second person is
	// taken only at the start.
	s3 := state(t, alice.group)
	s3.People = append(s3.People, "carol")
	_, err := w.commit(alice, letIn(t, alice, s3, carol), []*device{bob})
	refused(t, err, "two people at most")

	s1 := state(t, alice.group)
	s1.People = []string{"alice"}
	if _, err := w.commit(alice, mls.CommitOptions{Remove: []uint32{leafOf(t, alice.group, bob)},
		ReplaceExtensions: true, Extensions: withState(t, alice.group, s1)}, []*device{bob}); err != nil {
		t.Fatal(err)
	}
	carol.fresh(t)
	s2 := state(t, alice.group)
	s2.People = append(s2.People, "carol")
	_, err = w.commit(alice, letIn(t, alice, s2, carol), nil)
	refused(t, err, "when it starts, and never a third")
}

func TestOwners(t *testing.T) {
	w, alice, bob, carol := trio(t)
	s := state(t, bob.group)
	s.Owners = []string{"alice", "bob"}
	_, err := w.commit(bob, mls.CommitOptions{ReplaceExtensions: true, Extensions: withState(t, bob.group, s)}, []*device{alice, carol})
	refused(t, err, "only an owner changes the owners")
	if _, err := w.commit(alice, mls.CommitOptions{ReplaceExtensions: true, Extensions: withState(t, alice.group, s)}, []*device{bob, carol}); err != nil {
		t.Fatal(err)
	}
	if !state(t, carol.group).Owner("bob") {
		t.Fatal("bob is not an owner")
	}
}

func TestLastOwnerLeaves(t *testing.T) {
	w, alice, bob, carol := trio(t)
	msg, err := alice.group.ProposeRemove(leafOf(t, alice.group, alice), nil)
	if err != nil {
		t.Fatal(err)
	}
	w.propose(msg, bob, carol)
	commit := func(owners ...string) error {
		s := state(t, carol.group)
		s.People, s.Owners = []string{"bob", "carol"}, owners
		_, err := w.commit(carol, mls.CommitOptions{IncludePending: true, ReplaceExtensions: true, Extensions: withState(t, carol.group, s)},
			[]*device{bob})
		return err
	}
	// carol may not make herself the owner; bob joined first.
	refused(t, commit("carol"), "the person who joined first is the owner")
	if err := commit("bob"); err != nil {
		t.Fatal(err)
	}
}

func TestOtherExtensionsStay(t *testing.T) {
	w, alice, bob, carol := trio(t)
	var exts []mls.Extension
	for _, e := range alice.group.Extensions() {
		if e.Type != mls.ExtensionExternalSenders {
			exts = append(exts, e)
		}
	}
	_, err := w.commit(alice, mls.CommitOptions{ReplaceExtensions: true, Extensions: exts}, []*device{bob, carol})
	refused(t, err, "no extension but the conversation's state")
}

func TestLatere(t *testing.T) {
	w, alice, bob, carol := trio(t)
	conv := string(alice.group.GroupID())
	others := []*device{bob, carol}

	on := func(by *device, level Level, l *device) error {
		s := state(t, by.group)
		s.Level = level
		var rest []*device
		for _, d := range append([]*device{alice}, others...) {
			if d != by {
				rest = append(rest, d)
			}
		}
		_, err := w.commit(by, letIn(t, by, s, l), rest, l)
		return err
	}
	// Not an owner, the wrong level, an attestation of another
	// installation.
	refused(t, on(bob, LevelReadsAlong, w.latere(conv)), "only an owner lets Latere in")
	refused(t, on(alice, LevelOff, w.latere(conv)), "exactly when the level is not Off")
	_, stranger, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	forged := w.latere(conv)
	forged.kp, forged.secrets, err = mls.NewKeyPackage(forged.signer, forged.id.Credential(),
		LatereKeyPackageOptions(stranger, conv, forged.signer.PublicKey()))
	if err != nil {
		t.Fatal(err)
	}
	refused(t, on(alice, LevelReadsAlong, forged), "attestation does not verify")
	// Latere's member of another conversation.
	refused(t, on(alice, LevelReadsAlong, w.latere("another")), "not Latere's member of this conversation")

	l := w.latere(conv)
	if err := on(alice, LevelReadsAlong, l); err != nil {
		t.Fatal(err)
	}

	// The level without Latere's member changing, by an owner only.
	setLevel := func(by *device, level Level) error {
		s := state(t, by.group)
		s.Level = level
		var rest []*device
		for _, d := range []*device{alice, bob, carol, l} {
			if d != by {
				rest = append(rest, d)
			}
		}
		_, err := w.commit(by, mls.CommitOptions{ReplaceExtensions: true, Extensions: withState(t, by.group, s)}, rest)
		return err
	}
	refused(t, setLevel(carol, LevelTakesPart), "only an owner sets the level")
	refused(t, setLevel(alice, LevelOff), "exactly when the level is not Off") // Latere's member stays in
	if err := setLevel(alice, LevelTakesPart); err != nil {
		t.Fatal(err)
	}

	// Messages from Latere's member, by the level.
	latereSender := mls.Sender{Kind: mls.SenderMember, Credential: l.id.Credential()}
	if err := CheckMessage(state(t, bob.group), latereSender); err != nil {
		t.Fatal(err)
	}
	reads := state(t, bob.group)
	reads.Level = LevelReadsAlong
	refused(t, CheckMessage(reads, latereSender), "only when Latere takes part")

	// A proposal from Latere's member never becomes pending: Config has
	// the group refuse it on receipt.
	msg, err := l.group.ProposeRemove(leafOf(t, l.group, carol), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = alice.group.Process(msg)
	refused(t, err, "Latere's member proposes nothing")

	// Latere's member commits its own update, and nothing more.
	if _, err := w.commit(l, mls.CommitOptions{}, []*device{alice, bob, carol}); err != nil {
		t.Fatal(err)
	}
	dave := w.device("dave", "phone")
	s := state(t, l.group)
	s.People = append(s.People, "dave")
	_, err = w.commit(l, letIn(t, l, s, dave), []*device{alice, bob, carol})
	refused(t, err, "an update of its own leaf alone")

	// Off: an owner removes Latere's member and sets the level at once.
	off := state(t, alice.group)
	off.Level = LevelOff
	ch, err := w.commit(alice, mls.CommitOptions{Remove: []uint32{leafOf(t, alice.group, l)},
		ReplaceExtensions: true, Extensions: withState(t, alice.group, off)}, []*device{bob, carol})
	if err != nil {
		t.Fatal(err)
	}
	if !ch.LatereRemoved || ch.After.Level != LevelOff {
		t.Fatalf("change %+v", ch)
	}
}
