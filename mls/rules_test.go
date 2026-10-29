// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package mls

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

const testSuite = MLS_128_DHKEMX25519_AES128GCM_SHA256_Ed25519

// pair returns Alice's group with Bob in it, both merged.
func pair(t testing.TB, cfg Config) (alice, bob *client) {
	t.Helper()
	alice, bob = newClient(t, testSuite, "alice"), newClient(t, testSuite, "bob")
	var err error
	alice.group, err = CreateGroup(randomID(t), alice.signer, alice.cred, CreateOptions{}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	sc, err := alice.group.Commit(CommitOptions{Add: []*KeyPackage{bob.kp}})
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
	return alice, bob
}

func TestStateRoundTrip(t *testing.T) {
	alice, bob := pair(t, Config{})
	var msgs [][]byte
	for i := range 3 {
		m, err := bob.group.Encrypt([]byte{byte(i)}, nil)
		if err != nil {
			t.Fatal(err)
		}
		msgs = append(msgs, m)
	}
	// The second arrives first; the first's key is kept for later.
	deliver(t, msgs[1], alice)
	state, err := alice.group.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	alice.group, err = UnmarshalGroup(state, Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{0, 2} {
		m := deliver(t, msgs[i], alice)[0]
		if !bytes.Equal(m.ApplicationData, []byte{byte(i)}) {
			t.Fatalf("message %d reads %x", i, m.ApplicationData)
		}
	}
	if _, err := alice.group.Process(msgs[1]); err == nil {
		t.Fatal("a message opens twice across a restart")
	}
	sc, err := alice.group.Commit(CommitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := alice.group.Merge(sc); err != nil {
		t.Fatal(err)
	}
	deliver(t, sc.Message(), bob)
	sameEpoch(t, alice, bob)

	if _, err := UnmarshalGroup(state[:len(state)-1], Config{}); err == nil {
		t.Fatal("a truncated state restores")
	}
}

func TestPublicGroupState(t *testing.T) {
	alice, bob := pair(t, Config{PublicHandshake: true})
	gi, err := alice.group.GroupInfo(GroupInfoOptions{RatchetTree: true})
	if err != nil {
		t.Fatal(err)
	}
	ds, err := NewPublicGroup(gi, JoinOptions{}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	state, err := ds.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if ds, err = UnmarshalPublicGroup(state, Config{}); err != nil {
		t.Fatal(err)
	}
	sc, err := bob.group.Commit(CommitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	m, err := ds.Process(sc.Message())
	if err != nil {
		t.Fatal(err)
	}
	if err := ds.Merge(m.Commit); err != nil {
		t.Fatal(err)
	}
	if err := bob.group.Merge(sc); err != nil {
		t.Fatal(err)
	}
	if err := ds.VerifyGroupInfo(sc.GroupInfo()); err != nil {
		t.Fatal(err)
	}
	if err := ds.VerifyGroupInfo(gi); err == nil {
		t.Fatal("a group info of the previous epoch verifies")
	}
	enc, err := bob.group.Encrypt([]byte("x"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ds.Process(enc); err == nil {
		t.Fatal("the delivery service reads a private message")
	}
}

func TestTamperedMessages(t *testing.T) {
	alice, bob := pair(t, Config{PublicHandshake: true})
	msg, err := bob.group.Encrypt([]byte("hello"), []byte("ad"))
	if err != nil {
		t.Fatal(err)
	}
	for i := range msg {
		bad := bytes.Clone(msg)
		bad[i] ^= 1
		if _, err := alice.group.Process(bad); err == nil {
			t.Fatalf("a message with byte %d flipped opens", i)
		}
	}
	sc, err := bob.group.Commit(CommitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	commitMsg := sc.Message()
	for _, i := range []int{len(commitMsg) - 1, len(commitMsg) / 2, 40} {
		bad := bytes.Clone(commitMsg)
		bad[i] ^= 1
		m, err := alice.group.Process(bad)
		if err == nil && m.Commit != nil {
			t.Fatalf("a commit with byte %d flipped stages", i)
		}
	}
	deliver(t, msg, alice)
	if err := bob.group.Merge(sc); err != nil {
		t.Fatal(err)
	}
	deliver(t, commitMsg, alice)
	if _, err := alice.group.Process(msg); !errors.Is(err, ErrWrongEpoch) {
		t.Fatalf("a message of the last epoch: %v", err)
	}
}

func TestPublicApplicationMessageRejected(t *testing.T) {
	alice, bob := pair(t, Config{})
	a := &authenticatedContent{
		wireFormat: WireFormatPublicMessage,
		content: framedContent{
			groupID: bob.group.ctx.groupID, epoch: bob.group.ctx.epoch,
			sender:      sender{typ: senderMember, index: bob.group.Leaf()},
			contentType: contentApplication, applicationData: []byte("in the clear"),
		},
	}
	msg, err := bob.group.protect(a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := alice.group.Process(msg); err == nil {
		t.Fatal("an unencrypted application message is accepted")
	}
}

func TestMergeRules(t *testing.T) {
	alice, bob := pair(t, Config{})
	sc, err := alice.group.Commit(CommitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := bob.group.Merge(sc); !errors.Is(err, ErrStaleCommit) {
		t.Fatalf("merging another member's staged commit: %v", err)
	}
	if err := alice.group.Merge(sc); err != nil {
		t.Fatal(err)
	}
	if err := alice.group.Merge(sc); !errors.Is(err, ErrStaleCommit) {
		t.Fatalf("merging twice: %v", err)
	}
	// Bob commits in the same epoch; his commit loses, since Alice's won.
	lost, err := bob.group.Commit(CommitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	deliver(t, sc.Message(), bob)
	if err := bob.group.Merge(lost); !errors.Is(err, ErrStaleCommit) {
		t.Fatalf("merging a commit that lost the epoch: %v", err)
	}
	if _, err := alice.group.Process(lost.Message()); !errors.Is(err, ErrWrongEpoch) {
		t.Fatalf("a commit of the last epoch: %v", err)
	}
	sameEpoch(t, alice, bob)
}

func TestCommitRules(t *testing.T) {
	alice, bob := pair(t, Config{})
	carol := newClient(t, testSuite, "carol")
	cases := map[string]CommitOptions{
		"remove the committer":  {Remove: []uint32{alice.group.Leaf()}},
		"remove an empty leaf":  {Remove: []uint32{7}},
		"remove twice":          {Remove: []uint32{bob.group.Leaf(), bob.group.Leaf()}},
		"add twice":             {Add: []*KeyPackage{carol.kp, carol.kp}},
		"add a member":          {Add: []*KeyPackage{bob.kp}},
		"duplicate extensions":  {ReplaceExtensions: true, Extensions: []Extension{{Type: 0xf000}, {Type: 0xf000}}},
		"unsupported extension": {ReplaceExtensions: true, Extensions: []Extension{{Type: 0xf000}}},
	}
	for name, opts := range cases {
		if _, err := alice.group.Commit(opts); err == nil {
			t.Errorf("%s: the commit is made", name)
		}
	}
}

func TestCredentialAndLifetimeChecks(t *testing.T) {
	cfg := Config{CheckCredential: func(c Credential, _ []byte) error {
		if string(c.Identity) == "mallory" {
			return errors.New("not allowed")
		}
		return nil
	}}
	alice, _ := pair(t, cfg)
	mallory := newClient(t, testSuite, "mallory")
	if _, err := alice.group.Commit(CommitOptions{Add: []*KeyPackage{mallory.kp}}); err == nil {
		t.Fatal("a rejected credential is added")
	}

	carol := newClient(t, testSuite, "carol")
	expired := alice.group.cfg
	expired.Now = func() time.Time { return time.Now().Add(2 * DefaultKeyPackageLifetime) }
	alice.group.cfg = expired
	if _, err := alice.group.Commit(CommitOptions{Add: []*KeyPackage{carol.kp}}); err == nil {
		t.Fatal("an expired key package is added")
	}
	alice.group.cfg = Config{MaxLifetime: time.Hour}
	if _, err := alice.group.Commit(CommitOptions{Add: []*KeyPackage{carol.kp}}); err == nil {
		t.Fatal("a key package living longer than allowed is added")
	}
}

func TestExternalSenderRemove(t *testing.T) {
	server, err := GenerateSignatureKey(testSuite)
	if err != nil {
		t.Fatal(err)
	}
	alice, bob := newClient(t, testSuite, "alice"), newClient(t, testSuite, "bob")
	ext := ExternalSendersExtension([]ExternalSender{{SignatureKey: server.PublicKey(), Credential: BasicCredential([]byte("server"))}})
	alice.group, err = CreateGroup(randomID(t), alice.signer, alice.cred, CreateOptions{Extensions: []Extension{ext}}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	sc, err := alice.group.Commit(CommitOptions{Add: []*KeyPackage{bob.kp}})
	if err != nil {
		t.Fatal(err)
	}
	if err := alice.group.Merge(sc); err != nil {
		t.Fatal(err)
	}
	prop, err := ProposeExternalRemove(server, alice.group.GroupID(), alice.group.Epoch(), 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	m := deliver(t, prop, alice)[0]
	if m.Sender.Kind != SenderExternal || string(m.Sender.Credential.Identity) != "server" {
		t.Fatalf("sender %+v", m.Sender)
	}
	forged, err := GenerateSignatureKey(testSuite)
	if err != nil {
		t.Fatal(err)
	}
	bad, err := ProposeExternalRemove(forged, alice.group.GroupID(), alice.group.Epoch(), 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := alice.group.Process(bad); err == nil {
		t.Fatal("a proposal signed by another key is accepted")
	}
	sc, err = alice.group.Commit(CommitOptions{IncludePending: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.Changes().Removed) != 1 {
		t.Fatal("the external remove is not committed")
	}
}

func TestRatchetLimits(t *testing.T) {
	s := mustSuite(t, uint16(testSuite))
	st := newSecretTree(s, make([]byte, s.nh), 2)
	if _, _, err := st.get(1, ratchetApplication, MaxForwardGenerations+1); !errors.Is(err, errGeneration) {
		t.Fatalf("a generation too far ahead: %v", err)
	}
	_, advance, err := st.get(1, ratchetApplication, 5)
	if err != nil {
		t.Fatal(err)
	}
	advance()
	_, advance, err = st.get(1, ratchetApplication, 2)
	if err != nil {
		t.Fatalf("a skipped generation: %v", err)
	}
	advance()
	if _, _, err := st.get(1, ratchetApplication, 2); !errors.Is(err, errGeneration) {
		t.Fatalf("a used generation: %v", err)
	}
	if _, _, err := st.get(1, ratchetApplication, 5); !errors.Is(err, errGeneration) {
		t.Fatalf("a used generation: %v", err)
	}
	// An unconfirmed read moves nothing.
	if _, _, err := st.get(0, ratchetHandshake, 3); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.get(0, ratchetHandshake, 0); err != nil {
		t.Fatalf("generation 0 after an abandoned read: %v", err)
	}
}

func TestMemberViews(t *testing.T) {
	alice, bob := pair(t, Config{})
	m, ok := alice.group.Member(bob.group.Leaf())
	if !ok || string(m.Credential.Identity) != "bob" || !bytes.Equal(m.SignatureKey, bob.signer.PublicKey()) {
		t.Fatalf("member %+v", m)
	}
	if _, ok := alice.group.Member(9); ok {
		t.Fatal("a member at an empty leaf")
	}
	if !bytes.Equal(alice.group.GroupID(), bob.group.GroupID()) || alice.group.CipherSuite() != testSuite {
		t.Fatal("views differ")
	}
	if _, err := alice.group.Export("x", nil, MaxExportBytes+1); err == nil {
		t.Fatal("an export longer than the bound")
	}
	ratchet := alice.group.RatchetTree()
	tree, err := parseTree(ratchet)
	if err != nil || tree.numLeaves() != 2 {
		t.Fatalf("ratchet tree: %v", err)
	}
}
