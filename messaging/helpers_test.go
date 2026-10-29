// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package messaging

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"testing"

	"latere.ai/x/sdk-go/mls"
)

// world is an installation for tests: the service's external sender
// key and its view of the conversation's group, the installation's
// Latere key and everyone's directory records.
type world struct {
	t            testing.TB
	service      *mls.SignatureKey
	view         *mls.PublicGroup
	installation ed25519.PrivateKey
	records      map[string][]DeviceRecord
	revocations  map[string][]Revocation
	known        map[string][]Revocation
	keys         map[string]ed25519.PrivateKey
}

func newWorld(t testing.TB) *world {
	t.Helper()
	service, err := mls.GenerateSignatureKey(Suite)
	if err != nil {
		t.Fatal(err)
	}
	_, inst, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &world{t: t, service: service, installation: inst,
		records: make(map[string][]DeviceRecord), revocations: make(map[string][]Revocation), known: make(map[string][]Revocation),
		keys: make(map[string]ed25519.PrivateKey)}
}

func (w *world) externalSender() mls.ExternalSender {
	return mls.ExternalSender{SignatureKey: w.service.PublicKey(), Credential: mls.BasicCredential([]byte("the service"))}
}

func (w *world) policy() *Policy {
	return &Policy{
		Installation: w.installation.Public().(ed25519.PublicKey),
		Directory: func(person string) (*Directory, error) {
			d, _ := NewDirectory(person, w.records[person], w.revocations[person], w.known[person])
			return d, nil
		},
	}
}

// device is one device of a person, with its keys.
type device struct {
	id      Identity
	key     ed25519.PrivateKey
	enc     *ecdh.PrivateKey
	signer  *mls.SignatureKey
	record  DeviceRecord
	kp      *mls.KeyPackage
	secrets *mls.KeyPackageSecrets
	group   *mls.Group
}

// device makes a device of person and lists it in the directory: the
// person's first device as a root, later ones vouched for by the first.
func (w *world) device(person, name string) *device {
	w.t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		w.t.Fatal(err)
	}
	enc, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		w.t.Fatal(err)
	}
	signer, err := mls.NewSignatureKey(Suite, key.Seed())
	if err != nil {
		w.t.Fatal(err)
	}
	id, err := DeviceIdentity(person, name)
	if err != nil {
		w.t.Fatal(err)
	}
	d := &device{id: id, key: key, enc: enc, signer: signer}
	if first := w.records[person]; len(first) == 0 {
		d.record, err = NewDeviceRecord(person, name, key, enc.PublicKey(), nil)
	} else if d.record, err = NewDeviceRecord(person, name, key, enc.PublicKey(), &first[0]); err == nil {
		err = d.record.Sign(w.keys[first[0].Person+"/"+first[0].ID])
	}
	if err != nil {
		w.t.Fatal(err)
	}
	w.records[person] = append(w.records[person], d.record)
	w.keys[person+"/"+name] = key
	d.fresh(w.t)
	return d
}

// revoke has by, a device of the same person, revoke d's key, keeping
// the records of keep, and lists the revocation.
func (w *world) revoke(by, d *device, keep ...*device) Revocation {
	w.t.Helper()
	var keys [][]byte
	for _, k := range keep {
		keys = append(keys, k.record.SignatureKey)
	}
	v := Revocation{Person: d.id.Person, Key: d.record.SignatureKey, Keep: keys}
	if err := v.Sign(by.id.Device, by.key); err != nil {
		w.t.Fatal(err)
	}
	w.revocations[d.id.Person] = append(w.revocations[d.id.Person], v)
	return v
}

// fresh gives the device a new key package.
func (d *device) fresh(t testing.TB) {
	t.Helper()
	var err error
	d.kp, d.secrets, err = mls.NewKeyPackage(d.signer, d.id.Credential(), KeyPackageOptions())
	if err != nil {
		t.Fatal(err)
	}
}

// latere makes Latere's member of a conversation, attested by the
// installation.
func (w *world) latere(conversation string) *device {
	w.t.Helper()
	signer, err := mls.GenerateSignatureKey(Suite)
	if err != nil {
		w.t.Fatal(err)
	}
	id, err := LatereIdentity(conversation)
	if err != nil {
		w.t.Fatal(err)
	}
	d := &device{id: id, signer: signer}
	d.kp, d.secrets, err = mls.NewKeyPackage(signer, id.Credential(),
		LatereKeyPackageOptions(w.installation, conversation, signer.PublicKey()))
	if err != nil {
		w.t.Fatal(err)
	}
	return d
}

// create starts a conversation of kind with its creator alone, the owner.
func (w *world) create(owner *device, kind Kind) string {
	w.t.Helper()
	conv := NewConversationID()
	opts, err := CreateOptions(State{Kind: kind, People: []string{owner.id.Person}, Owners: []string{owner.id.Person}}, w.externalSender())
	if err != nil {
		w.t.Fatal(err)
	}
	owner.group, err = mls.CreateGroup(GroupID(conv), owner.signer, owner.id.Credential(), opts, Config())
	if err != nil {
		w.t.Fatal(err)
	}
	info, err := owner.group.GroupInfo(mls.GroupInfoOptions{RatchetTree: true})
	if err != nil {
		w.t.Fatal(err)
	}
	if w.view, err = mls.NewPublicGroup(info, mls.JoinOptions{}, Config()); err != nil {
		w.t.Fatal(err)
	}
	return conv
}

// state is the conversation's state as g holds it.
func state(t testing.TB, g *mls.Group) State {
	t.Helper()
	s, err := ParseState(g.Extensions())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// withState is the group's extension list with the state replaced, by
// one Validate need not accept, as a malicious device would send it.
func withState(t testing.TB, g *mls.Group, s State) []mls.Extension {
	t.Helper()
	ext := s.encode()
	var out []mls.Extension
	for _, e := range g.Extensions() {
		if e.Type == StateExtension {
			e = ext
		}
		out = append(out, e)
	}
	return out
}

// commit makes a commit from by, checks it with the policy from the
// committer's side, the service's and every other member's, and on
// success merges it everywhere and joins the added devices. It returns
// the change, or the error of the committer's check.
func (w *world) commit(by *device, opts mls.CommitOptions, others []*device, joiners ...*device) (*Change, error) {
	w.t.Helper()
	sc, err := by.group.Commit(opts)
	if err != nil {
		return nil, err
	}
	p := w.policy()
	ch, err := p.CheckCommit(by.group, sc.Changes())
	vm, verr := w.view.Process(sc.Message())
	if verr != nil {
		w.t.Fatalf("the service processing %s's commit: %v", by.id, verr)
	}
	vch, verr := p.CheckCommit(w.view, vm.Commit.Changes())
	if (verr == nil) != (err == nil) {
		w.t.Fatalf("committer's check %v, the service's check %v", err, verr)
	}
	if verr == nil {
		if fmt.Sprint(vch) != fmt.Sprint(ch) {
			w.t.Fatalf("committer and the service see different changes:\n%+v\n%+v", ch, vch)
		}
		if err := w.view.Merge(vm.Commit); err != nil {
			w.t.Fatal(err)
		}
	}
	for _, o := range others {
		msg, perr := o.group.Process(sc.Message())
		if perr != nil {
			w.t.Fatalf("%s processing %s's commit: %v", o.id, by.id, perr)
		}
		och, oerr := p.CheckCommit(o.group, msg.Commit.Changes())
		if (oerr == nil) != (err == nil) {
			w.t.Fatalf("committer's check %v, %s's check %v", err, o.id, oerr)
		}
		if oerr == nil {
			if fmt.Sprint(och) != fmt.Sprint(ch) {
				w.t.Fatalf("committer and %s see different changes:\n%+v\n%+v", o.id, ch, och)
			}
			if err := o.group.Merge(msg.Commit); err != nil {
				w.t.Fatal(err)
			}
		}
	}
	if err != nil {
		return nil, err
	}
	if err := by.group.Merge(sc); err != nil {
		w.t.Fatal(err)
	}
	for _, j := range joiners {
		j.group, err = mls.JoinGroup(sc.Welcome(), j.kp, j.secrets, j.signer, mls.JoinOptions{}, Config())
		if err != nil {
			w.t.Fatal(err)
		}
	}
	return ch, nil
}

// leafOf is the leaf of d in g.
func leafOf(t testing.TB, g *mls.Group, d *device) uint32 {
	t.Helper()
	for _, m := range g.Members() {
		if string(m.Credential.Identity) == d.id.String() {
			return m.Leaf
		}
	}
	t.Fatalf("%s is not in the group", d.id)
	return 0
}

// refused fails the test unless err is a refusal, for the reason given
// when there is one.
func refused(t testing.TB, err error, reason ...string) {
	t.Helper()
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("got %v, want a refusal", err)
	}
	for _, r := range reason {
		if !strings.Contains(err.Error(), r) {
			t.Fatalf("refused for %v, want %q", err, r)
		}
	}
}
