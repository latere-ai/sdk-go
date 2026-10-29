// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package messaging

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"latere.ai/x/sdk-go/mls"
)

// Attacks on conversations by the service, a thief or a member, each
// asserting the rule that stops it.

// externalJoin has d join on its own from a group info member hands out
// and reports whether the service and every one of others take it.
func (w *world) externalJoin(t *testing.T, member *device, others []*device, d *device) error {
	t.Helper()
	info, err := member.group.GroupInfo(mls.GroupInfoOptions{RatchetTree: true, ExternalPub: true})
	if err != nil {
		t.Fatal(err)
	}
	g, msg, err := mls.JoinExternal(info, d.signer, d.id.Credential(), mls.ExternalJoinOptions{SupportedExtensions: extensionTypes}, Config())
	if err != nil {
		t.Fatal(err)
	}
	p := w.policy()
	vm, err := w.view.Process(msg)
	if err != nil {
		t.Fatal(err)
	}
	_, verr := p.CheckCommit(w.view, vm.Commit.Changes())
	for _, o := range others {
		m, err := o.group.Process(msg)
		if err != nil {
			t.Fatal(err)
		}
		if _, oerr := p.CheckCommit(o.group, m.Commit.Changes()); (oerr == nil) != (verr == nil) {
			t.Fatalf("the service %v, %s %v", verr, o.id, oerr)
		}
	}
	if verr == nil {
		d.group = g
	}
	return verr
}

// A thief holding alice's stolen phone, which she revoked from her
// laptop, signs a new device with it; the device joins none of her
// conversations.
func TestStolenKeyMintsNoDevice(t *testing.T) {
	w, phone, bob, carol := trio(t)
	laptop := w.device("alice", "laptop") // vouched for by the phone
	if _, err := w.commit(phone, mls.CommitOptions{Add: []*mls.KeyPackage{laptop.kp}}, []*device{bob, carol}, laptop); err != nil {
		t.Fatal(err)
	}
	w.revoke(laptop, phone, laptop)
	if _, err := w.commit(laptop, mls.CommitOptions{Remove: []uint32{leafOf(t, laptop.group, phone)}}, []*device{bob, carol}); err != nil {
		t.Fatal(err)
	}
	spawn := w.device("alice", "spawn") // the helper vouches with the first record, the phone
	refused(t, w.externalJoin(t, bob, []*device{bob, laptop, carol}, spawn))
	refused(t, w.externalJoin(t, bob, []*device{bob, laptop, carol}, phone))
}

// noticeWith encodes a notice in the layout NewCommitNotice writes, from
// parts a committer or the service built by hand.
func noticeWith(commit, members []byte, parts map[string][]byte) []byte {
	return encode(func(w *writer) {
		w.u8(noticeVersion)
		w.vec8(commit)
		w.vec32(members)
		w.u16(uint16(len(parts)))
		for sk, sealed := range parts {
			w.vec32([]byte(sk))
			w.vec32(sealed)
		}
	})
}

func joinerPlain(j JoinerNotice, salt []byte) []byte {
	return encode(func(w *writer) {
		j.Range.marshal(w)
		w.vec8(salt)
		w.vec8([]byte(j.Owner))
		w.vec32([]byte(j.Ref))
		w.vec8(j.Digest)
		w.u64(uint64(j.Size))
		w.vec8(j.Key)
	})
}

func newSalt(t *testing.T) []byte {
	t.Helper()
	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		t.Fatal(err)
	}
	return salt
}

// The service, which holds every key package and so every init key,
// hands a joiner a notice of its own making; the joiner takes only the
// notice whose digest the committer's Welcome carries.
func TestForgedJoinerNotice(t *testing.T) {
	w, alice, bob, _ := chat(t, 3, "earlier")
	carol := w.device("carol", "phone")
	s := state(t, alice.group)
	s.People = append(s.People, "carol")
	if _, err := w.commit(alice, letIn(t, alice, s, carol), []*device{bob}, carol); err != nil {
		t.Fatal(err)
	}
	sb, err := SealBundle(nil)
	if err != nil {
		t.Fatal(err)
	}
	all := HistoryRange{Kind: RangeAll}
	forged := JoinerNotice{Range: all, Owner: "service", Ref: Folder + "/made-up", Digest: sb.Digest, Size: int64(len(sb.Ciphertext)), Key: sb.Key}
	salt := newSalt(t)
	sealed, err := carol.kp.Seal(HistoryLabel, joinerContext(carol.group.GroupID(), carol.group.Epoch()), joinerPlain(forged, salt))
	if err != nil {
		t.Fatal(err)
	}
	n, err := ParseCommitNotice(noticeWith(commitment(all, salt), make([]byte, 64), map[string][]byte{string(carol.kp.SignatureKey()): sealed}))
	if err != nil {
		t.Fatal(err)
	}
	if jn, err := n.Open(carol.group, carol.kp, carol.secrets); err == nil || !strings.Contains(err.Error(), "Welcome") {
		t.Fatalf("carol took a notice the service made: %+v, %v", jn, err)
	}
}

// A committer seals "nothing" for the members and "everything" for the
// joiner under one commitment; the joiner refuses the part, so the line
// the members draw is true of what the joiner gets.
func TestNoticeRangesAgree(t *testing.T) {
	w, alice, bob, received := chat(t, 3, "before carol")
	carol := w.device("carol", "phone")
	var entries []Entry
	for _, m := range received {
		e, err := NewEntry(m)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, e)
	}
	sb, err := SealBundle(entries)
	if err != nil {
		t.Fatal(err)
	}
	none := HistoryRange{Kind: RangeNone}
	salt := newSalt(t)
	key, err := alice.group.Export(noticeExporterLabel, nil, 32)
	if err != nil {
		t.Fatal(err)
	}
	members, err := seal(key, encode(func(w *writer) {
		none.marshal(w)
		w.vec8(salt)
	}), membersAAD(alice.group.GroupID(), alice.group.Epoch()))
	if err != nil {
		t.Fatal(err)
	}
	all := JoinerNotice{Range: HistoryRange{Kind: RangeAll}, Owner: "alice", Ref: Folder + "/b1", Digest: sb.Digest, Size: int64(len(sb.Ciphertext)), Key: sb.Key}
	sealed, err := carol.kp.Seal(HistoryLabel, joinerContext(alice.group.GroupID(), alice.group.Epoch()+1), joinerPlain(all, salt))
	if err != nil {
		t.Fatal(err)
	}
	ad := noticeWith(commitment(none, salt), members, map[string][]byte{string(carol.kp.SignatureKey()): sealed})
	s := state(t, alice.group)
	s.People = append(s.People, "carol")
	sc, err := alice.group.Commit(mls.CommitOptions{Add: []*mls.KeyPackage{carol.kp}, ReplaceExtensions: true,
		Extensions: withState(t, alice.group, s), AuthenticatedData: ad, WelcomeExtensions: []mls.Extension{WelcomeExtension(ad)}})
	if err != nil {
		t.Fatal(err)
	}
	m, err := bob.group.Process(sc.Message())
	if err != nil {
		t.Fatal(err)
	}
	n, err := ParseCommitNotice(m.Commit.Changes().AuthenticatedData)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := n.Range(bob.group); err != nil || got.Kind != RangeNone {
		t.Fatalf("bob read %+v, %v", got, err)
	}
	if err := alice.group.Merge(sc); err != nil {
		t.Fatal(err)
	}
	carol.group, err = mls.JoinGroup(sc.Welcome(), carol.kp, carol.secrets, carol.signer, mls.JoinOptions{}, Config())
	if err != nil {
		t.Fatal(err)
	}
	if cn, err := n.Open(carol.group, carol.kp, carol.secrets); err == nil || !strings.Contains(err.Error(), "committed") {
		t.Fatalf("carol took range %+v while bob was told none: %v", cn, err)
	}
}

// payloadOf frames a JSON body as EncodePayload frames one.
func payloadOf(body string) []byte {
	b := make([]byte, 1024)
	b[0] = payloadVersion
	binary.BigEndian.PutUint32(b[1:5], uint32(len(body)))
	copy(b[5:], body)
	return b
}

// One signed payload reads one way: keys that differ in case, a repeated
// key or a member the format lacks would read differently in another
// decoder.
func TestPayloadOneEncoding(t *testing.T) {
	a, _ := sealFile(t, []byte("benign"))
	key2 := bytes.Repeat([]byte{7}, StreamKeySize)
	for name, body := range map[string]string{
		"case":     `{"id":"m1","sent_at":"2026-10-29T10:00:00Z","type":"text","text":"hello","TEXT":"the other text"}`,
		"repeated": `{"id":"m1","sent_at":"2026-10-29T10:00:00Z","type":"text","text":"hello","text":"the other text"}`,
		"unknown":  `{"id":"m1","sent_at":"2026-10-29T10:00:00Z","type":"text","text":"hello","x":1}`,
		"spaced":   `{"id":"m1", "sent_at":"2026-10-29T10:00:00Z","type":"text","text":"hello"}`,
		"file": fmt.Sprintf(`{"id":"m1","sent_at":"2026-10-29T10:00:00Z","type":"text","attachments":[{"name":"f.bin","media_type":"image/png","size":%d,"blob":%q,"owner":"alice","ref":%q,"key":%q,"REF":%q,"KEY":%q}]}`,
			a.Size, a.Blob, Folder+"/benign", base64.StdEncoding.EncodeToString(a.Key), Folder+"/other", base64.StdEncoding.EncodeToString(key2)),
	} {
		if p, err := ParsePayload(payloadOf(body)); err == nil {
			t.Errorf("%s: parsed as %+v", name, p)
		}
	}
	good, err := EncodePayload(Payload{ID: "m1", SentAt: time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC), Type: PayloadText, Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParsePayload(good); err != nil {
		t.Fatal(err)
	}
}

// payloadWith is a text carrying one sealed file, edited.
func payloadWith(t *testing.T, edit func(a *Attachment)) ([]byte, error) {
	t.Helper()
	a, _ := sealFile(t, []byte("a file"))
	edit(a)
	return EncodePayload(Payload{ID: "m1", SentAt: time.UnixMilli(1_700_000_000_000).UTC(), Type: PayloadText, Attachments: []Attachment{*a}})
}

// A file's name draws as it reads, and a file is named in its sender's
// messaging folder and nowhere else.
func TestAttachmentNamesAndPlaces(t *testing.T) {
	for _, name := range []string{
		"invoice\u202efdp.exe", // a right-to-left override: draws as invoiceexe.pdf
		"photo\u2066.jpg\u2069.app",
		"a\u0085b",      // NEL, a C1 control
		"x\u009b31mred", // CSI, a C1 control
		"report\u200b.pdf",
		"line\u2028break",
	} {
		if _, err := payloadWith(t, func(a *Attachment) { a.Name = name }); err == nil {
			t.Errorf("a file named %+q is carried", name)
		}
	}
	for name, edit := range map[string]func(a *Attachment){
		"me":         func(a *Attachment) { a.Owner = "me" },
		"dots":       func(a *Attachment) { a.Ref = Folder + "/../../../v1/account" },
		"dot":        func(a *Attachment) { a.Ref = Folder + "/./x" },
		"empty":      func(a *Attachment) { a.Ref = Folder + "//x" },
		"elsewhere":  func(a *Attachment) { a.Ref = "files/taxes.pdf" },
		"the folder": func(a *Attachment) { a.Ref = Folder },
	} {
		if _, err := payloadWith(t, edit); err == nil {
			t.Errorf("%s: a file at an owner or path outside the messaging folder is carried", name)
		}
	}
}

func dirOf(t *testing.T, w *world, person string) *Directory {
	t.Helper()
	d, err := w.policy().Directory(person)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// phoneRevoked is the trio with alice's laptop added and her stolen
// phone, her root, revoked from the laptop and removed from the group.
func phoneRevoked(t *testing.T) (w *world, phone, laptop, bob, carol *device) {
	t.Helper()
	w, phone, bob, carol = trio(t)
	laptop = w.device("alice", "laptop")
	if _, err := w.commit(phone, mls.CommitOptions{Add: []*mls.KeyPackage{laptop.kp}}, []*device{bob, carol}, laptop); err != nil {
		t.Fatal(err)
	}
	w.revoke(laptop, phone, laptop)
	if _, err := w.commit(laptop, mls.CommitOptions{Remove: []uint32{leafOf(t, laptop.group, phone)}}, []*device{bob, carol}); err != nil {
		t.Fatal(err)
	}
	if !dirOf(t, w, "alice").Revoked(phone.record.SignatureKey) {
		t.Fatal("the phone is not revoked")
	}
	return w, phone, laptop, bob, carol
}

// The thief signs more revocations than the bound with the stolen phone,
// of keys no record holds, and lists them first; they do not count, and
// alice's revocation of the phone still does.
func TestRevocationFlood(t *testing.T) {
	w, phone, laptop, bob, carol := phoneRevoked(t)
	var flood []Revocation
	for range MaxRevocations + 1 {
		junk := make([]byte, ed25519.PublicKeySize)
		if _, err := rand.Read(junk); err != nil {
			t.Fatal(err)
		}
		v := Revocation{Person: "alice", Key: junk}
		if err := v.Sign("phone", phone.key); err != nil {
			t.Fatal(err)
		}
		flood = append(flood, v)
	}
	w.revocations["alice"] = append(flood, w.revocations["alice"]...)
	if !dirOf(t, w, "alice").Revoked(phone.record.SignatureKey) {
		t.Error("the stolen phone is no longer revoked once revocations it signed come first")
	}
	spawn := w.device("alice", "spawn") // vouched for by the phone
	refused(t, w.externalJoin(t, bob, []*device{bob, laptop, carol}, spawn))
}

// The service leaves out the laptop, the record that signed alice's
// revocation of the phone; the revocation bob's app kept still applies,
// and the phone stays out.
func TestWithheldSigner(t *testing.T) {
	w, phone, _, bob, carol := phoneRevoked(t)
	w.known["alice"] = dirOf(t, w, "alice").Revocations() // what bob's app keeps
	w.revocations["alice"] = nil
	w.records["alice"] = slices.DeleteFunc(slices.Clone(w.records["alice"]), func(r DeviceRecord) bool { return r.ID == "laptop" })
	if !dirOf(t, w, "alice").Revoked(phone.record.SignatureKey) {
		t.Error("a revocation bob's app kept stops counting when the service leaves out the record that signed it")
	}
	refused(t, w.externalJoin(t, bob, []*device{bob, carol}, phone))
}

// forgedRoot lists a root of the service's own for person, and returns
// its record and key.
func forgedRoot(t *testing.T, w *world, person, id string) (DeviceRecord, ed25519.PrivateKey) {
	t.Helper()
	k := newKeys(t)
	r := record(t, person, id, k, nil, nil)
	w.records[person] = append(w.records[person], r)
	return r, k.sig
}

// A root the service lists approves no backup key for alice's new
// device: only the device that approved it does.
func TestForgedRootApprovesNoBackupKey(t *testing.T) {
	w := newWorld(t)
	w.device("alice", "phone")
	tablet := w.device("alice", "tablet") // vouched for by the phone
	svc, svcKey := forgedRoot(t, w, "alice", "svc")
	serviceKey, err := NewBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	moved := tablet.record
	moved.Voucher, moved.VoucherKey = svc.ID, svc.SignatureKey
	transfer, err := serviceKey.SealTo(moved, svcKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenBackupKey(tablet.enc, dirOf(t, w, "alice"), tablet.record, transfer); err == nil {
		t.Error("alice's tablet took a backup key the service chose, approved by a root the service listed")
	}
}

// A root the service lists cannot revoke alice's only device, so the
// service cannot remove her and make bob the owner.
func TestForgedRootRevokesNothing(t *testing.T) {
	w, alice, bob, carol := trio(t)
	_, svcKey := forgedRoot(t, w, "alice", "svc")
	v := Revocation{Person: "alice", Key: alice.record.SignatureKey}
	if err := v.Sign("svc", svcKey); err != nil {
		t.Fatal(err)
	}
	w.revocations["alice"] = append(w.revocations["alice"], v)
	msg, err := mls.ProposeExternalRemove(w.service, bob.group.GroupID(), bob.group.Epoch(), 0, leafOf(t, bob.group, alice))
	if err != nil {
		t.Fatal(err)
	}
	m, err := w.view.Process(msg)
	if err != nil {
		t.Fatal(err)
	}
	refused(t, w.policy().CheckProposal(w.view, *m.Proposal))
	for _, d := range []*device{bob, carol} {
		if _, err := d.group.Process(msg); err == nil {
			t.Fatalf("%s kept the removal pending", d.id)
		}
		d.group = loosen(t, d.group)
		if _, err := d.group.Process(msg); err != nil {
			t.Fatal(err)
		}
	}
	s := state(t, bob.group)
	s.People, s.Owners = []string{"bob", "carol"}, []string{"bob"}
	_, err = w.commit(bob, mls.CommitOptions{IncludePending: true, ReplaceExtensions: true, Extensions: withState(t, bob.group, s)}, []*device{carol})
	refused(t, err)
}

// forgedGroup is a group the service makes up whose "alice/phone" leaf is
// a key of its own, in a state that lists people.
func forgedGroup(t *testing.T, w *world, people ...string) *mls.Group {
	t.Helper()
	fakeSigner, err := mls.GenerateSignatureKey(Suite)
	if err != nil {
		t.Fatal(err)
	}
	aliceID, err := DeviceIdentity("alice", "phone")
	if err != nil {
		t.Fatal(err)
	}
	opts, err := CreateOptions(State{Kind: KindGroup, People: people, Owners: []string{"alice"}}, w.externalSender())
	if err != nil {
		t.Fatal(err)
	}
	fake, err := mls.CreateGroup(GroupID(NewConversationID()), fakeSigner, aliceID.Credential(), opts, Config())
	if err != nil {
		t.Fatal(err)
	}
	return fake
}

// The service makes a group of its own whose "alice" is its key, signs
// the group info with it and carries a notice digest that matches; bob
// refuses to take it, by a Welcome or joining on his own, so nothing he
// sends reaches the service.
func TestServiceForgesAWholeGroup(t *testing.T) {
	w := newWorld(t)
	w.device("alice", "phone") // alice's real directory
	bob := w.device("bob", "phone")
	fake := forgedGroup(t, w, "alice")
	s := state(t, fake)
	s.People = append(s.People, "bob")
	none := HistoryRange{Kind: RangeNone}
	ad, err := NewCommitNotice(fake, none, []Joiner{{KeyPackage: bob.kp, Notice: JoinerNotice{Range: none}}})
	if err != nil {
		t.Fatal(err)
	}
	sc, err := fake.Commit(mls.CommitOptions{Add: []*mls.KeyPackage{bob.kp}, ReplaceExtensions: true, Extensions: withState(t, fake, s),
		AuthenticatedData: ad, WelcomeExtensions: []mls.Extension{WelcomeExtension(ad)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := fake.Merge(sc); err != nil {
		t.Fatal(err)
	}
	_, err = w.policy().JoinGroup(sc.Welcome(), bob.kp, bob.secrets, bob.signer, mls.JoinOptions{})
	refused(t, err, "user:alice/device:phone")

	tablet := w.device("bob", "tablet")
	info, err := forgedGroup(t, w, "alice", "bob").GroupInfo(mls.GroupInfoOptions{RatchetTree: true, ExternalPub: true})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = w.policy().JoinExternal(info, tablet.signer, tablet.id, conversationOf(t, info), 0)
	refused(t, err, "user:alice/device:phone")
}

// A group whose state breaks a rule, or that lacks a person's device, is
// refused as well.
func TestCheckJoin(t *testing.T) {
	w, alice, bob, carol := trio(t)
	p := w.policy()
	for _, d := range []*device{alice, bob, carol} {
		if err := p.CheckJoin(d.group); err != nil {
			t.Fatalf("%s: %v", d.id, err)
		}
	}
	if err := p.CheckJoin(w.view); err != nil {
		t.Fatalf("the service's view: %v", err)
	}
	// A person listed with no device: alice's group before bob and
	// carol joined, with them listed.
	lonely := forgedGroup(t, w, "alice", "dave")
	refused(t, p.CheckJoin(lonely))
	// A revoked device's leaf.
	w.revoke(carol, carol)
	refused(t, p.CheckJoin(alice.group), "carol")
}

// A committer that leaves the members' part of a notice unreadable has
// its commit refused by every member, who would otherwise draw no line.
func TestMembersPartUnreadable(t *testing.T) {
	w, alice, bob, received := chat(t, 3, "before carol")
	carol := w.device("carol", "phone")
	var entries []Entry
	for _, m := range received {
		e, err := NewEntry(m)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, e)
	}
	sb, err := SealBundle(entries)
	if err != nil {
		t.Fatal(err)
	}
	salt := newSalt(t)
	all := HistoryRange{Kind: RangeAll}
	jn := JoinerNotice{Range: all, Owner: "alice", Ref: Folder + "/b1", Digest: sb.Digest, Size: int64(len(sb.Ciphertext)), Key: sb.Key}
	sealed, err := carol.kp.Seal(HistoryLabel, joinerContext(alice.group.GroupID(), alice.group.Epoch()+1), joinerPlain(jn, salt))
	if err != nil {
		t.Fatal(err)
	}
	ad := noticeWith(commitment(all, salt), make([]byte, 64), map[string][]byte{string(carol.kp.SignatureKey()): sealed})
	s := state(t, alice.group)
	s.People = append(s.People, "carol")
	sc, err := alice.group.Commit(mls.CommitOptions{Add: []*mls.KeyPackage{carol.kp}, ReplaceExtensions: true, Extensions: withState(t, alice.group, s),
		AuthenticatedData: ad, WelcomeExtensions: []mls.Extension{WelcomeExtension(ad)}})
	if err != nil {
		t.Fatal(err)
	}
	m, err := bob.group.Process(sc.Message())
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.policy().CheckCommit(bob.group, m.Commit.Changes())
	refused(t, err, "members")
}

// alice revoked her stolen phone keeping only her laptop. The thief holds
// x under the phone and y under x, and revokes x keeping y, signed with
// the phone or with y. y hangs from x, which the revocation of the phone
// does not keep, so y stands nowhere and joins none of alice's
// conversations.
func TestKeptUnderARevokedDevice(t *testing.T) {
	for _, signer := range []string{"phone", "y"} {
		t.Run("signed by "+signer, func(t *testing.T) {
			w, phone, laptop, bob, carol := phoneRevoked(t)
			x := w.deviceUnder("alice", "x", &phone.record)
			y := w.deviceUnder("alice", "y", &x.record)
			before := dirOf(t, w, "alice")
			key := phone.key
			if signer == "y" {
				key = y.key
			}
			v := revocation(t, "alice", x.record.SignatureKey, signer, key, y.record.SignatureKey)
			if changes, err := before.CheckRevocation(v); err != nil || !changes {
				t.Fatalf("CheckRevocation: %v, %v", changes, err)
			}
			w.revocations["alice"] = append(w.revocations["alice"], v)
			d := dirOf(t, w, "alice")
			if !d.Revoked(phone.record.SignatureKey) {
				t.Fatal("the phone is no longer revoked")
			}
			if _, ok := d.Device("y"); ok {
				t.Error("a device the thief made under the revoked phone stands")
			}
			if _, ok := d.Device("laptop"); !ok {
				t.Error("the laptop the revocation keeps is gone")
			}
			refused(t, w.externalJoin(t, bob, []*device{bob, laptop, carol}, y))
		})
	}
}

// latereFork lets Latere's member into alice's conversation, sets the
// level back to Off without it, and has it make a group of its own under
// the conversation's id at ReadsAlong, from alice's key package the
// service holds.
func latereFork(t *testing.T) (w *world, alice, bob, carol *device, fork *mls.Group) {
	t.Helper()
	w, alice, bob, carol = trio(t)
	conv := string(alice.group.GroupID())
	l := w.latere(conv)
	s := state(t, alice.group)
	s.Level = LevelReadsAlong
	if _, err := w.commit(alice, letIn(t, alice, s, l), []*device{bob, carol}, l); err != nil {
		t.Fatal(err)
	}
	s = state(t, alice.group)
	s.Level = LevelOff
	if _, err := w.commit(alice, mls.CommitOptions{Remove: []uint32{leafOf(t, alice.group, l)}, ReplaceExtensions: true,
		Extensions: withState(t, alice.group, s)}, []*device{bob, carol}); err != nil {
		t.Fatal(err)
	}
	opts, err := CreateOptions(State{Kind: KindGroup, People: []string{"alice", "bob"}, Owners: []string{"alice"}, Level: LevelReadsAlong}, w.externalSender())
	if err != nil {
		t.Fatal(err)
	}
	opts.LeafExtensions = l.kp.LeafExtensions()
	if fork, err = mls.CreateGroup(GroupID(conv), l.signer, l.id.Credential(), opts, Config()); err != nil {
		t.Fatal(err)
	}
	alice.fresh(t)
	return w, alice, bob, carol, fork
}

// Latere's member, removed from a conversation, still holds its leaf key
// and the installation's attestation for the conversation's id. A group
// it makes under that id passes CheckJoin, every leaf a device or the
// attested member at a level that matches; bob's new tablet refuses it
// all the same, by a Welcome or on its own, since Latere's member signed
// its group info.
func TestLatereMakesNoGroup(t *testing.T) {
	w, alice, _, _, fork := latereFork(t)
	tablet := w.device("bob", "tablet")
	sc, err := fork.Commit(mls.CommitOptions{Add: []*mls.KeyPackage{alice.kp, tablet.kp}})
	if err != nil {
		t.Fatal(err)
	}
	if err := fork.Merge(sc); err != nil {
		t.Fatal(err)
	}
	if err := w.policy().CheckJoin(fork); err != nil {
		t.Fatalf("the fork fails CheckJoin, so this test does not reach the signer: %v", err)
	}
	_, err = w.policy().JoinGroup(sc.Welcome(), tablet.kp, tablet.secrets, tablet.signer, mls.JoinOptions{})
	refused(t, err, "Latere's member signed the group info")

	info, err := fork.GroupInfo(mls.GroupInfoOptions{RatchetTree: true, ExternalPub: true})
	if err != nil {
		t.Fatal(err)
	}
	other := w.device("bob", "laptop")
	_, _, err = w.policy().JoinExternal(info, other.signer, other.id, conversationOf(t, info), 0)
	refused(t, err, "Latere's member signed the group info")
}

// After Latere's member commits an update of its own leaf, the group
// info of the epoch is its; a new device does not join on its own from
// it, and does once a person's device commits.
func TestJoinAfterLatereUpdates(t *testing.T) {
	w, alice, bob, carol := trio(t)
	l := w.latere(string(alice.group.GroupID()))
	s := state(t, alice.group)
	s.Level = LevelReadsAlong
	if _, err := w.commit(alice, letIn(t, alice, s, l), []*device{bob, carol}, l); err != nil {
		t.Fatal(err)
	}
	if _, err := w.commit(l, mls.CommitOptions{}, []*device{alice, bob, carol}); err != nil {
		t.Fatal(err)
	}
	tablet := w.device("bob", "tablet")
	info, err := l.group.GroupInfo(mls.GroupInfoOptions{RatchetTree: true, ExternalPub: true})
	if err != nil {
		t.Fatal(err)
	}
	conv := string(alice.group.GroupID())
	_, _, err = w.policy().JoinExternal(info, tablet.signer, tablet.id, conv, 0)
	refused(t, err, "Latere's member signed the group info")

	if _, err := w.commit(alice, mls.CommitOptions{}, []*device{bob, carol, l}); err != nil {
		t.Fatal(err)
	}
	if info, err = alice.group.GroupInfo(mls.GroupInfoOptions{RatchetTree: true, ExternalPub: true}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.policy().JoinExternal(info, tablet.signer, tablet.id, conv, 0); err != nil {
		t.Errorf("a join from alice's group info: %v", err)
	}
}

// A thief with a key under alice's root, and a service listing a fresh
// batch of revocations each time, add nothing past the first changes to
// what bob's app keeps: a revocation that keeps no less than those
// before it changes nothing.
func TestKnownRevocationsStay(t *testing.T) {
	w, phone, laptop, _, _ := phoneRevoked(t)
	w.known["alice"] = dirOf(t, w, "alice").Revocations() // bob's app saw alice revoke the phone
	var sizes []int
	for range 3 {
		var batch []Revocation
		for range MaxRevocations - 2 {
			junk := newKeys(t).sig.Public().(ed25519.PublicKey)
			batch = append(batch, revocation(t, "alice", laptop.record.SignatureKey, "phone", phone.key, junk))
		}
		w.revocations["alice"] = batch
		w.known["alice"] = dirOf(t, w, "alice").Revocations()
		sizes = append(sizes, len(w.known["alice"]))
	}
	// alice's revocation of the phone, the first of the laptop, and the
	// second, which keeps none of what the first kept.
	if !slices.Equal(sizes, []int{3, 3, 3}) {
		t.Errorf("bob's app keeps %v revocations of alice over three listings", sizes)
	}
	d := dirOf(t, w, "alice")
	if !d.Revoked(phone.record.SignatureKey) || !d.Revoked(laptop.record.SignatureKey) {
		t.Error("a revocation that counted no longer applies")
	}
}

// The service lists a root of its own with a recovery record under it
// whose id sorts first. Recovery returns that one; RecoveryFor returns
// the record of alice's own recovery key, and nothing for a key no
// record holds.
func TestRecoveryFor(t *testing.T) {
	w := newWorld(t)
	phone := w.device("alice", "phone")
	rk, err := NewRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	own, err := NewRecoveryRecord("alice", "recovery", rk, phone.record)
	if err != nil {
		t.Fatal(err)
	}
	if err := own.Sign(phone.key); err != nil {
		t.Fatal(err)
	}
	svc, svcKey := forgedRoot(t, w, "alice", "svc")
	other, err := NewRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	fake, err := NewRecoveryRecord("alice", "a-recovery", other, svc)
	if err != nil {
		t.Fatal(err)
	}
	if err := fake.Sign(svcKey); err != nil {
		t.Fatal(err)
	}
	w.records["alice"] = append(w.records["alice"], own, fake)
	d := dirOf(t, w, "alice")
	if got, ok := d.Recovery(); !ok || got.ID != fake.ID {
		t.Fatalf("Recovery() returned %q, %v; the test expects the service's record first", got.ID, ok)
	}
	if got, err := d.RecoveryFor(rk); err != nil || got.ID != own.ID {
		t.Errorf("RecoveryFor returned %q, %v", got.ID, err)
	}
	none, err := NewRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.RecoveryFor(none); err == nil {
		t.Error("RecoveryFor found a record for a key no record holds")
	}
}

// The thief, holding alice's stolen phone and x it made under it,
// revokes the phone from x first, keeping her laptop and a key it holds
// no record for yet. alice's revocation of the phone keeps the laptop
// alone, which changes nothing that is listed, so the service need not
// keep it. CheckRecord then refuses the record the thief makes for that
// key under the phone, so nothing the thief named stands.
func TestCheckRecord(t *testing.T) {
	w := newWorld(t)
	phone := w.device("alice", "phone")
	laptop := w.device("alice", "laptop")
	x := w.device("alice", "x")
	later := newKeys(t)
	w.revocations["alice"] = []Revocation{revocation(t, "alice", phone.record.SignatureKey, "x", x.key,
		laptop.record.SignatureKey, later.sig.Public().(ed25519.PublicKey))}
	d := dirOf(t, w, "alice")
	own := revocation(t, "alice", phone.record.SignatureKey, "laptop", laptop.key, laptop.record.SignatureKey)
	if changes, err := d.CheckRevocation(own); err != nil || changes {
		t.Fatalf("alice's revocation: changes %v, %v; the test expects it to change nothing listed", changes, err)
	}
	minted := record(t, "alice", "later", later, &phone.record, phone.key)
	if err := d.CheckRecord(minted); err == nil {
		t.Error("the service took a record the revoked phone vouched for")
	}
	w.records["alice"] = append(w.records["alice"], minted)
	if _, ok := dirOf(t, w, "alice").Device("later"); !ok {
		t.Fatal("the record stands once listed, so CheckRecord is what keeps it out")
	}

	fresh := newKeys(t)
	tablet := record(t, "alice", "tablet", fresh, &laptop.record, laptop.key)
	if err := d.CheckRecord(tablet); err != nil {
		t.Errorf("a record the laptop vouches for: %v", err)
	}
	for name, r := range map[string]DeviceRecord{
		"another person's": record(t, "bob", "tablet", fresh, nil, nil),
		"a revoked key":    phone.record,
		"a bad vouch": func() DeviceRecord {
			r := tablet
			r.Vouch = slices.Clone(r.Vouch)
			r.Vouch[0] ^= 1
			return r
		}(),
	} {
		if err := d.CheckRecord(r); err == nil {
			t.Errorf("%s: taken", name)
		}
	}
}

// A record under an id alice already holds, with another key, would
// take her laptop out of her directory, since two records under one id
// both stand nowhere, and with it every revocation the laptop signs.
// CheckRecord refuses it, before the revocation and after, as it does a
// record with a key another id holds.
func TestDuplicateRecordRefused(t *testing.T) {
	t.Run("after the revocation", func(t *testing.T) {
		w, _, _, _, _ := phoneRevoked(t)
		d := dirOf(t, w, "alice")
		if err := d.CheckRecord(record(t, "alice", "laptop", newKeys(t), nil, nil)); err == nil {
			t.Error("a root under the id of alice's laptop was taken")
		}
	})
	t.Run("before the revocation", func(t *testing.T) {
		w := newWorld(t)
		phone := w.device("alice", "phone")
		laptop := w.device("alice", "laptop")
		d := dirOf(t, w, "alice")
		if err := d.CheckRecord(record(t, "alice", "laptop", newKeys(t), &phone.record, phone.key)); err == nil {
			t.Error("a record under the id of alice's laptop, vouched for by the phone, was taken")
		}
		twin := keys{sig: laptop.key, enc: laptop.enc}
		if err := d.CheckRecord(record(t, "alice", "pad", twin, &phone.record, phone.key)); err == nil {
			t.Error("a record with the laptop's key under another id was taken")
		}
		// The laptop vouched for again under the same id and key is a
		// replacement the service may take.
		again := record(t, "alice", "laptop", twin, &phone.record, phone.key)
		if err := d.CheckRecord(again); err != nil {
			t.Errorf("the laptop vouched for again: %v", err)
		}
	})
	t.Run("an id listed twice", func(t *testing.T) {
		w := newWorld(t)
		phone := w.device("alice", "phone")
		w.records["alice"] = append(w.records["alice"], record(t, "alice", "pad", newKeys(t), &phone.record, phone.key),
			record(t, "alice", "pad", newKeys(t), &phone.record, phone.key))
		if err := dirOf(t, w, "alice").CheckRecord(record(t, "alice", "pad", newKeys(t), &phone.record, phone.key)); err == nil {
			t.Error("a third record under an id listed twice was taken")
		}
	})
}

// The service lists alice's phone key once under a record k2 the tablet
// vouches for, with the thief's revocation of the phone signed by k2
// ahead of alice's; later it lists the phone as itself with the thief's
// x under it, and no revocations. The thief's revocation is signed with
// the revoked key under either listing, so it keeps nothing in both, and
// x stays out at bob's app, which saw alice revoke the phone.
func TestKnownRevocationKeepsItsMeaning(t *testing.T) {
	lapK, phoneK, tabletK, xK := newKeys(t), newKeys(t), newKeys(t), newKeys(t)
	lap := record(t, "alice", "laptop", lapK, nil, nil)
	phone := record(t, "alice", "phone", phoneK, &lap, lapK.sig)
	tablet := record(t, "alice", "tablet", tabletK, &lap, lapK.sig)
	x := record(t, "alice", "x", xK, &phone, phoneK.sig)
	k2 := record(t, "alice", "k2", phoneK, &tablet, tabletK.sig)
	thief := revocation(t, "alice", phone.SignatureKey, "k2", phoneK.sig, x.SignatureKey)
	alice := revocation(t, "alice", phone.SignatureKey, "laptop", lapK.sig)
	aliceTablet := revocation(t, "alice", tablet.SignatureKey, "laptop", lapK.sig)

	first, _ := NewDirectory("alice", []DeviceRecord{lap, tablet, k2}, []Revocation{thief, alice, aliceTablet}, nil)
	later, _ := NewDirectory("alice", []DeviceRecord{lap, phone, tablet, x}, nil, first.Revocations())
	if !later.Revoked(phone.SignatureKey) {
		t.Fatal("the phone is not revoked")
	}
	if _, ok := later.Device("x"); ok {
		t.Error("the thief's device under the revoked phone stands at an app that saw alice revoke the phone")
	}
}

// Latere's member, removed and the level set back to Off, kept its state
// of an epoch it was in, and the service kept alice's group info of that
// epoch. bob's new tablet, given the conversation and its epoch over the
// pairing, refuses the stale group info; given no epoch, it takes it, the
// limit the package documents. A group info of another conversation of
// bob's is refused whatever its epoch, so the floor of a young
// conversation admits no stale group info of an older one, and so is a
// message that is not a group info.
func TestStaleGroupInfoRefused(t *testing.T) {
	w, alice, bob, carol := trio(t)
	conv := string(alice.group.GroupID())
	l := w.latere(conv)
	s := state(t, alice.group)
	s.Level = LevelReadsAlong
	if _, err := w.commit(alice, letIn(t, alice, s, l), []*device{bob, carol}, l); err != nil {
		t.Fatal(err)
	}
	stale, err := alice.group.GroupInfo(mls.GroupInfoOptions{RatchetTree: true, ExternalPub: true})
	if err != nil {
		t.Fatal(err)
	}
	s = state(t, alice.group)
	s.Level = LevelOff
	if _, err := w.commit(alice, mls.CommitOptions{Remove: []uint32{leafOf(t, alice.group, l)}, ReplaceExtensions: true,
		Extensions: withState(t, alice.group, s)}, []*device{bob, carol}); err != nil {
		t.Fatal(err)
	}
	tablet := w.device("bob", "tablet")
	_, _, err = w.policy().JoinExternal(stale, tablet.signer, tablet.id, conv, bob.group.Epoch())
	refused(t, err, "before the conversation's epoch")
	if _, _, err := w.policy().JoinExternal(stale, tablet.signer, tablet.id, conv, 0); err != nil {
		t.Errorf("with no epoch given, the stale group info: %v", err)
	}
	fresh, err := alice.group.GroupInfo(mls.GroupInfoOptions{RatchetTree: true, ExternalPub: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.policy().JoinExternal(fresh, tablet.signer, tablet.id, conv, bob.group.Epoch()); err != nil {
		t.Errorf("the current group info: %v", err)
	}
	young := NewConversationID()
	for name, info := range map[string][]byte{"stale": stale, "current": fresh} {
		_, _, err := w.policy().JoinExternal(info, tablet.signer, tablet.id, young, 1)
		if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "another conversation") {
			t.Errorf("the %s group info asked for as another conversation: got %v, want a refusal", name, err)
		}
	}
	sent, err := alice.group.Encrypt([]byte("hello"), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = w.policy().JoinExternal(sent, tablet.signer, tablet.id, conv, 0)
	refused(t, err, "not a group info")
	if _, _, err := w.policy().JoinExternal([]byte("not a group info"), tablet.signer, tablet.id, conv, 0); err == nil {
		t.Error("bytes that are no message were joined")
	}
}
