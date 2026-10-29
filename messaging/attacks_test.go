// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package messaging

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
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
	_, _, err = w.policy().JoinExternal(info, tablet.signer, tablet.id)
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
