// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package messaging

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"regexp"
	"testing"

	"latere.ai/x/sdk-go/mls"
)

type keys struct {
	sig ed25519.PrivateKey
	enc *ecdh.PrivateKey
}

func newKeys(t *testing.T) keys {
	t.Helper()
	_, sig, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return keys{sig, enc}
}

func record(t *testing.T, person, id string, k keys, voucher string, by ed25519.PrivateKey) DeviceRecord {
	t.Helper()
	r := NewDeviceRecord(person, id, k.sig.Public().(ed25519.PublicKey), k.enc.PublicKey())
	if err := r.Sign(voucher, by); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestDirectoryChains(t *testing.T) {
	phone, laptop, tablet := newKeys(t), newKeys(t), newKeys(t)
	rk, err := NewRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	rsig, err := rk.SigningKey()
	if err != nil {
		t.Fatal(err)
	}
	recovery := DeviceRecord{Person: "aiko", ID: "recovery-1", Kind: RecordRecovery, SignatureKey: rsig.Public().(ed25519.PublicKey)}
	if err := recovery.Sign("phone", phone.sig); err != nil {
		t.Fatal(err)
	}
	root := record(t, "aiko", "phone", phone, "phone", phone.sig)
	root.Revoked = true // lost, and its vouches still stand
	recs := []DeviceRecord{
		root,
		recovery,
		record(t, "aiko", "laptop", laptop, "phone", phone.sig),
		record(t, "aiko", "tablet", tablet, "recovery-1", rsig),
	}
	d, dropped := NewDirectory("aiko", recs)
	if dropped != 0 {
		t.Fatalf("dropped %d", dropped)
	}
	if got := d.Devices(); len(got) != 2 || got[0].ID != "laptop" || got[1].ID != "tablet" {
		t.Fatalf("devices %+v", got)
	}
	if roots := d.Roots(); len(roots) != 1 || roots[0].ID != "phone" {
		t.Fatalf("roots %+v", roots)
	}
	if r, ok := d.Recovery(); !ok || r.ID != "recovery-1" {
		t.Fatal("no recovery record")
	}
	if _, ok := d.Device("phone"); ok {
		t.Fatal("a revoked device is listed")
	}

	// A member's leaf checks against its record.
	id, _ := DeviceIdentity("aiko", "laptop")
	m := mls.Member{Credential: id.Credential(), SignatureKey: laptop.sig.Public().(ed25519.PublicKey)}
	if err := d.CheckMember(m); err != nil {
		t.Fatal(err)
	}
	m.SignatureKey = tablet.sig.Public().(ed25519.PublicKey)
	if err := d.CheckMember(m); err == nil {
		t.Fatal("a leaf with another device's key passed")
	}
	other, _ := DeviceIdentity("bob", "laptop")
	if err := d.CheckMember(mls.Member{Credential: other.Credential(), SignatureKey: laptop.sig.Public().(ed25519.PublicKey)}); err == nil {
		t.Fatal("another person's leaf passed")
	}
}

func TestDirectoryDrops(t *testing.T) {
	phone, laptop, evil := newKeys(t), newKeys(t), newKeys(t)
	root := record(t, "aiko", "phone", phone, "phone", phone.sig)
	forged := record(t, "aiko", "laptop", laptop, "phone", evil.sig)
	orphan := record(t, "aiko", "watch", evil, "nowhere", evil.sig)
	a := record(t, "aiko", "a", newKeys(t), "b", evil.sig)
	b := record(t, "aiko", "b", newKeys(t), "a", evil.sig)
	elsewhere := record(t, "bob", "phone", evil, "phone", evil.sig)
	edited := record(t, "aiko", "pad", newKeys(t), "phone", phone.sig)
	edited.EncryptionKey = evil.enc.PublicKey().Bytes()
	recoveryRoot := DeviceRecord{Person: "aiko", ID: "r", Kind: RecordRecovery, SignatureKey: evil.sig.Public().(ed25519.PublicKey), Voucher: "r"}
	recoveryRoot.Vouch = ed25519.Sign(evil.sig, recoveryRoot.vouchMessage())
	d, dropped := NewDirectory("aiko", []DeviceRecord{root, forged, orphan, a, b, elsewhere, edited, recoveryRoot})
	if dropped != 7 || len(d.Devices()) != 1 {
		t.Fatalf("dropped %d, devices %+v", dropped, d.Devices())
	}
	// Two records under one id are both dropped.
	twin := record(t, "aiko", "phone", newKeys(t), "phone", phone.sig)
	if _, dropped := NewDirectory("aiko", []DeviceRecord{root, twin}); dropped != 2 {
		t.Fatalf("dropped %d of two twins", dropped)
	}
}

func TestSecurityCode(t *testing.T) {
	a1, b1, b2 := newKeys(t), newKeys(t), newKeys(t)
	da, _ := NewDirectory("aiko", []DeviceRecord{record(t, "aiko", "phone", a1, "phone", a1.sig)})
	db, _ := NewDirectory("bob", []DeviceRecord{record(t, "bob", "phone", b1, "phone", b1.sig)})
	code := SecurityCode(da, db)
	if code != SecurityCode(db, da) {
		t.Fatal("the code depends on who computes it")
	}
	if !regexp.MustCompile(`^\d{5}( \d{5}){11}$`).MatchString(code) {
		t.Fatalf("code %q", code)
	}
	dbNew, _ := NewDirectory("bob", []DeviceRecord{record(t, "bob", "phone", b2, "phone", b2.sig)})
	if SecurityCode(da, dbNew) == code {
		t.Fatal("a new root keeps the code")
	}
}
