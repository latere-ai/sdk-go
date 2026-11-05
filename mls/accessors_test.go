// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package mls

import (
	"bytes"
	"strings"
	"testing"
)

func TestProposalKindNames(t *testing.T) {
	for k, want := range map[ProposalKind]string{
		ProposalAdd: "add", ProposalUpdate: "update", ProposalRemove: "remove", ProposalPSK: "psk",
		ProposalReInit: "reinit", ProposalExternalInit: "external_init", ProposalGroupContextExtensions: "group_context_extensions",
		ProposalKind(0x7777): "proposal(30583)",
	} {
		if got := k.String(); got != want {
			t.Errorf("%d is %q, want %q", uint16(k), got, want)
		}
	}
}

func TestSuiteNames(t *testing.T) {
	if !testSuite.Supported() || CipherSuite(0x7777).Supported() {
		t.Error("Supported disagrees with the suites the package implements")
	}
	if got := CipherSuite(0x7777).String(); got != "CipherSuite(0x7777)" {
		t.Errorf("an unknown suite is %q", got)
	}
	for _, cs := range testSuites {
		if strings.HasPrefix(cs.String(), "CipherSuite(") {
			t.Errorf("suite %d has no name", uint16(cs))
		}
	}
}

// A signature key of each kind returns the bytes NewSignatureKey takes
// back, and refuses bytes of another kind.
func TestSignatureKeyBytes(t *testing.T) {
	for _, cs := range testSuites {
		k, err := GenerateSignatureKey(cs)
		if err != nil {
			t.Fatal(err)
		}
		b, err := k.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		back, err := NewSignatureKey(cs, b)
		if err != nil {
			t.Fatalf("%v: %v", cs, err)
		}
		if !bytes.Equal(back.PublicKey(), k.PublicKey()) {
			t.Errorf("%v: the key did not survive its bytes", cs)
		}
		// An ECDSA scalar is read zero-padded; Ed25519 takes a seed of its size.
		if _, err := NewSignatureKey(cs, []byte{1, 2, 3}); (cs == testSuite) != (err != nil) {
			t.Errorf("%v: a key of 3 bytes gave %v", cs, err)
		}
		if _, err := NewSignatureKey(cs, make([]byte, len(b))); cs != testSuite && err == nil {
			t.Errorf("%v: a zero scalar was accepted", cs)
		}
	}
	if _, err := NewSignatureKey(CipherSuite(0x7777), nil); err == nil {
		t.Error("a key for an unknown suite was accepted")
	}
}

func TestKeyPackageAccessors(t *testing.T) {
	c := newClient(t, testSuite, "alice")
	kp := c.kp
	if kp.CipherSuite() != testSuite || !bytes.Equal(kp.SignatureKey(), c.signer.PublicKey()) {
		t.Error("the key package does not report its suite and signature key")
	}
	parsed, err := ParseKeyPackage(kp.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(parsed.Ref(), kp.Ref()) || len(kp.LeafExtensions()) != len(parsed.LeafExtensions()) || len(kp.Extensions()) != len(parsed.Extensions()) {
		t.Error("a parsed key package differs from the one it was made from")
	}
	// The accessors hand out copies.
	kp.Bytes()[0] ^= 0xff
	kp.SignatureKey()[0] ^= 0xff
	if !bytes.Equal(kp.SignatureKey(), c.signer.PublicKey()) {
		t.Error("an accessor exposed the key package's own bytes")
	}
}

func TestKeyPackageSecretsRoundTrip(t *testing.T) {
	c := newClient(t, testSuite, "alice")
	b := c.secrets.Bytes()
	back, err := ParseKeyPackageSecrets(b)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(back.Bytes(), b) {
		t.Error("the secrets changed in a round trip")
	}
	if _, err := ParseKeyPackageSecrets(b[:len(b)-1]); err == nil {
		t.Error("cut secrets were accepted")
	}
	if _, err := ParseKeyPackageSecrets(append(bytes.Clone(b), 0)); err == nil {
		t.Error("secrets with trailing bytes were accepted")
	}
}

func TestGroupViews(t *testing.T) {
	alice, bob := pair(t, Config{PublicHandshake: true})
	if len(alice.group.Extensions()) != 0 {
		t.Error("a group made without extensions has some")
	}
	gi, err := alice.group.GroupInfo(GroupInfoOptions{RatchetTree: true})
	if err != nil {
		t.Fatal(err)
	}
	ds, err := NewPublicGroup(gi, JoinOptions{}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if ds.CipherSuite() != testSuite || !bytes.Equal(ds.GroupID(), alice.group.GroupID()) || len(ds.Members()) != 2 || len(ds.Extensions()) != 0 {
		t.Error("the delivery service's view differs from the group")
	}
	// A pending proposal is part of a saved group and of a saved view.
	prop, err := alice.group.ProposeRemove(bob.group.Leaf(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ds.Process(prop); err != nil {
		t.Fatal(err)
	}
	for name, save := range map[string]func() ([]byte, error){"group": alice.group.MarshalBinary, "view": ds.MarshalBinary} {
		b, err := save()
		if err != nil {
			t.Fatal(err)
		}
		if name == "group" {
			g, err := UnmarshalGroup(b, Config{PublicHandshake: true})
			if err != nil || len(g.PendingProposals()) != 1 {
				t.Errorf("group restored with %v, %v", g, err)
			}
			continue
		}
		v, err := UnmarshalPublicGroup(b, Config{})
		if err != nil || len(v.pending) != 1 {
			t.Errorf("view restored with %v, %v", v, err)
		}
		if _, err := UnmarshalPublicGroup(b[:len(b)-2], Config{}); err == nil {
			t.Error("a cut view was restored")
		}
		bad := bytes.Clone(b)
		bad[0] = 99
		if _, err := UnmarshalPublicGroup(bad, Config{}); err == nil {
			t.Error("a view of another version was restored")
		}
		bad = bytes.Clone(b)
		bad[1], bad[2] = 0x77, 0x77
		if _, err := UnmarshalPublicGroup(bad, Config{}); err == nil {
			t.Error("a view of an unknown suite was restored")
		}
	}
}

func TestReaderRefusals(t *testing.T) {
	for name, f := range map[string]func(*reader){
		"a u8 with no bytes":              func(r *reader) { r.u8() },
		"a u16 with one byte":             func(r *reader) { r.u16() },
		"a u32 with three bytes":          func(r *reader) { r.u32() },
		"a u64 with seven bytes":          func(r *reader) { r.u64() },
		"a length with no bytes":          func(r *reader) { r.varint() },
		"a length of the prefix 0b11":     func(r *reader) { r.varint() },
		"an optional that is neither":     func(r *reader) { r.optional() },
		"a vector longer than its bytes":  func(r *reader) { r.vec() },
		"a nested vector left with bytes": func(r *reader) { r.nested(func(*reader) {}) },
	} {
		inputs := map[string][]byte{
			"a u8 with no bytes": nil, "a u16 with one byte": {1}, "a u32 with three bytes": {1, 2, 3}, "a u64 with seven bytes": {1, 2, 3, 4, 5, 6, 7},
			"a length with no bytes": nil, "a length of the prefix 0b11": {0xc0, 0}, "an optional that is neither": {2},
			"a vector longer than its bytes": {5, 1}, "a nested vector left with bytes": {2, 1, 2},
		}
		if err := decode(inputs[name], f); err == nil {
			t.Errorf("%s: decoded", name)
		}
	}
	// Lengths are written in the fewest bytes.
	if err := decode([]byte{0x40, 0x05, 0, 0, 0, 0, 0}, func(r *reader) { r.vec() }); err == nil {
		t.Error("a two-byte length of 5 decoded")
	}
	if err := decode([]byte{0x80, 0, 0x01, 0x00, 0}, func(r *reader) { r.vec() }); err == nil {
		t.Error("a four-byte length of 256 decoded")
	}
	if err := decode([]byte{0, 9}, func(r *reader) { r.u8() }); err == nil {
		t.Error("a value with a trailing byte decoded")
	}
}
