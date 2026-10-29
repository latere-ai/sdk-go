// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package messaging

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"latere.ai/x/sdk-go/mls"
)

func TestIdentity(t *testing.T) {
	d, err := DeviceIdentity("u-1", "phone.2")
	if err != nil {
		t.Fatal(err)
	}
	l, err := LatereIdentity("C0NV")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []Identity{d, l} {
		got, err := ParseCredential(id.Credential())
		if err != nil || got != id {
			t.Fatalf("%s: %+v, %v", id, got, err)
		}
	}
	if d.String() != "user:u-1/device:phone.2" || l.String() != "latere/conversation:C0NV" || d.Latere() || !l.Latere() {
		t.Fatal("identity forms")
	}
	for _, bad := range []string{
		"", "user:", "user:a", "user:a/device:", "user:/device:b", "user:a/device:b/device:c",
		"user:a:b/device:c", "latere/conversation:", "latere/conversation:a/b", "user:a/device:b ",
		"user:a/device:" + strings.Repeat("x", MaxIDBytes+1), "group:a",
	} {
		if _, err := ParseCredential(mls.BasicCredential([]byte(bad))); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
	if _, err := ParseCredential(mls.Credential{Type: mls.CredentialX509}); err == nil {
		t.Error("an X.509 credential parsed")
	}
}

func TestSuite(t *testing.T) {
	if err := CheckSuite(Suite); err != nil {
		t.Fatal(err)
	}
	if err := CheckSuite(mls.MLS_128_DHKEMP256_AES128GCM_SHA256_P256); !errors.Is(err, ErrSuite) {
		t.Fatalf("got %v", err)
	}
	// A key package of another suite is refused before it is added.
	k, err := mls.GenerateSignatureKey(mls.MLS_128_DHKEMP256_AES128GCM_SHA256_P256)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := DeviceIdentity("a", "b")
	kp, _, err := mls.NewKeyPackage(k, id.Credential(), KeyPackageOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CheckKeyPackage(kp); !errors.Is(err, ErrSuite) {
		t.Fatalf("got %v", err)
	}
}

func TestAttestation(t *testing.T) {
	w := newWorld(t)
	conv := NewConversationID()
	l := w.latere(conv)
	var m mls.Member
	m.Credential = l.kp.Credential()
	m.SignatureKey = l.kp.SignatureKey()
	m.Extensions = l.kp.LeafExtensions()
	pub := w.policy().Installation
	if err := CheckLatere(pub, GroupID(conv), m); err != nil {
		t.Fatal(err)
	}
	if err := CheckLatere(pub, GroupID("other"), m); err == nil {
		t.Fatal("attestation for another group accepted")
	}
	if err := CheckLatere(nil, GroupID(conv), m); err == nil {
		t.Fatal("no installation key accepted")
	}
	swapped := m
	swapped.SignatureKey = bytes.Repeat([]byte{1}, 32)
	if err := CheckLatere(pub, GroupID(conv), swapped); err == nil {
		t.Fatal("attestation over another key accepted")
	}
	bare := m
	bare.Extensions = nil
	if err := CheckLatere(pub, GroupID(conv), bare); err == nil {
		t.Fatal("no attestation accepted")
	}
	d := w.device("a", "b")
	dm := mls.Member{Credential: d.id.Credential(), SignatureKey: d.kp.SignatureKey(), Extensions: m.Extensions}
	if err := CheckLatere(pub, GroupID(conv), dm); err == nil {
		t.Fatal("a device passed for Latere's member")
	}
}
