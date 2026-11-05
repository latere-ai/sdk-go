// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package mls

import (
	"strings"
	"testing"
)

// A key package says what it is for, and a copy that lies about it is
// refused before its signature is looked at.
func TestKeyPackageSelfChecks(t *testing.T) {
	c := newClient(t, testSuite, "alice")
	s, err := suiteFor(testSuite)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.kp.kp.verify(s); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*keyPackage){
		"another protocol version": func(k *keyPackage) { k.version = 7 },
		"another suite":            func(k *keyPackage) { k.suite = MLS_128_DHKEMP256_AES128GCM_SHA256_P256 },
		"a leaf from an update":    func(k *keyPackage) { k.leaf.source = leafSourceUpdate },
		"an init key that is the leaf's": func(k *keyPackage) {
			k.initKey = k.leaf.encryptionKey
		},
		"an init key that is no point": func(k *keyPackage) { k.initKey = []byte{1, 2, 3} },
		"a signature that is not the leaf's": func(k *keyPackage) {
			k.signature = append([]byte{}, k.signature...)
			k.signature[0] ^= 0xff
		},
		"a repeated extension": func(k *keyPackage) { k.extensions = []Extension{{Type: 0xf020}, {Type: 0xf020}} },
	} {
		kp := c.kp.kp
		mutate(&kp)
		if err := kp.verify(s); err == nil {
			t.Errorf("%s: verified", name)
		}
	}
}

// A leaf's own claims are checked: its credential type and MLS 1.0 are in
// its capabilities, and its extensions are listed there.
func TestLeafSelfChecks(t *testing.T) {
	c := newClient(t, testSuite, "alice")
	s, err := suiteFor(testSuite)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*leafNode){
		"a credential type it does not list": func(l *leafNode) { l.capabilities.Credentials = nil },
		"no MLS 1.0":                         func(l *leafNode) { l.capabilities.Versions = nil },
		"an extension it does not list":      func(l *leafNode) { l.extensions = []Extension{{Type: 0xf021}} },
		"a signature that does not verify":   func(l *leafNode) { l.signature = append([]byte{0xff}, l.signature...) },
	} {
		l := c.kp.kp.leaf
		l.capabilities = l.capabilities.clone()
		mutate(&l)
		if err := checkLeafSelf(s, &l, nil, 0); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestJoinGroupRefusals(t *testing.T) {
	alice, bob := newClient(t, testSuite, "alice"), newClient(t, testSuite, "bob")
	carol := newClient(t, testSuite, "carol")
	var err error
	alice.group, err = CreateGroup(randomID(t), alice.signer, alice.cred, CreateOptions{}, Config{})
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
	p256 := newClient(t, MLS_128_DHKEMP256_AES128GCM_SHA256_P256, "dave")
	for name, join := range map[string]func() (*Group, error){
		"a message that is not a Welcome": func() (*Group, error) {
			return JoinGroup(bob.kp.Bytes(), bob.kp, bob.secrets, bob.signer, JoinOptions{}, Config{})
		},
		"a key package of another suite": func() (*Group, error) {
			return JoinGroup(sc.Welcome(), p256.kp, p256.secrets, p256.signer, JoinOptions{}, Config{})
		},
		"a signature key that is not the key package's": func() (*Group, error) {
			return JoinGroup(sc.Welcome(), bob.kp, bob.secrets, carol.signer, JoinOptions{}, Config{})
		},
		"a Welcome made for another key package": func() (*Group, error) {
			return JoinGroup(sc.Welcome(), carol.kp, carol.secrets, carol.signer, JoinOptions{}, Config{})
		},
		"secrets that are not the key package's": func() (*Group, error) {
			return JoinGroup(sc.Welcome(), bob.kp, carol.secrets, bob.signer, JoinOptions{}, Config{})
		},
		"a cut Welcome": func() (*Group, error) {
			w := sc.Welcome()
			return JoinGroup(w[:len(w)-4], bob.kp, bob.secrets, bob.signer, JoinOptions{}, Config{})
		},
	} {
		if g, err := join(); err == nil {
			t.Errorf("%s: joined %v", name, g)
		}
	}
	if g, err := JoinGroup(sc.Welcome(), bob.kp, bob.secrets, bob.signer, JoinOptions{}, Config{}); err != nil || g == nil {
		t.Fatalf("the honest join: %v", err)
	}
	// A group info with no ratchet tree needs the tree from the caller.
	gi, err := alice.group.GroupInfo(GroupInfoOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewPublicGroup(gi, JoinOptions{}, Config{}); err == nil || !strings.Contains(err.Error(), "ratchet tree") {
		t.Errorf("a group info with no tree: %v", err)
	}
	if _, err := NewPublicGroup(gi, JoinOptions{RatchetTree: []byte{0}}, Config{}); err == nil {
		t.Error("a tree that does not parse was taken")
	}
}

func TestJoinExternalRefusals(t *testing.T) {
	alice, bob := pair(t, Config{})
	gi, err := alice.group.GroupInfo(GroupInfoOptions{RatchetTree: true})
	if err != nil {
		t.Fatal(err)
	}
	carol := newClient(t, testSuite, "carol")
	p256 := newClient(t, MLS_128_DHKEMP256_AES128GCM_SHA256_P256, "dave")
	cases := map[string]func() error{
		"a message that is not a group info": func() error {
			_, _, err := JoinExternal(bob.kp.Bytes(), carol.signer, carol.cred, ExternalJoinOptions{}, Config{})
			return err
		},
		"a signature key of another suite": func() error {
			_, _, err := JoinExternal(gi, p256.signer, p256.cred, ExternalJoinOptions{}, Config{})
			return err
		},
		"data over the limit": func() error {
			_, _, err := JoinExternal(gi, carol.signer, carol.cred, ExternalJoinOptions{AuthenticatedData: make([]byte, MaxContentBytes+1)}, Config{})
			return err
		},
		"a group info with no external public key": func() error {
			_, _, err := JoinExternal(gi, carol.signer, carol.cred, ExternalJoinOptions{}, Config{})
			return err
		},
	}
	for name, f := range cases {
		if err := f(); err == nil {
			t.Errorf("%s: joined", name)
		}
	}
	if _, err := ProposeExternalRemove(carol.signer, nil, 0, 0, 0); err == nil {
		t.Error("a proposal for a group with no ID was made")
	}
	if _, err := ProposeExternalRemove(carol.signer, make([]byte, MaxContentBytes+1), 0, 0, 0); err == nil {
		t.Error("a proposal for a group ID over the limit was made")
	}
}
