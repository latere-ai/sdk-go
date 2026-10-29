// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package messaging

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"regexp"
	"slices"
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
	r, err := NewDeviceRecord(person, id, k.sig, k.enc.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
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
	recovery, err := NewRecoveryRecord("aiko", "recovery-1", rk)
	if err != nil {
		t.Fatal(err)
	}
	if err := recovery.Sign("phone", phone.sig); err != nil {
		t.Fatal(err)
	}
	recs := []DeviceRecord{
		record(t, "aiko", "phone", phone, "phone", phone.sig),
		recovery,
		record(t, "aiko", "laptop", laptop, "phone", phone.sig),
		record(t, "aiko", "tablet", tablet, "recovery-1", rsig),
	}
	// The phone is lost; the laptop revokes it and keeps what it vouched
	// for.
	lost := Revocation{Person: "aiko", Key: recs[0].SignatureKey, Keep: [][]byte{recs[1].SignatureKey, recs[2].SignatureKey}}
	if err := lost.Sign("laptop", laptop.sig); err != nil {
		t.Fatal(err)
	}
	d, dropped := NewDirectory("aiko", recs, []Revocation{lost})
	if dropped != 1 {
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
	if _, ok := d.Device("phone"); ok || !d.Revoked(recs[0].SignatureKey) || len(d.Revocations()) != 1 {
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
	phone, laptop, evil, other := newKeys(t), newKeys(t), newKeys(t), newKeys(t)
	root := record(t, "aiko", "phone", phone, "phone", phone.sig)
	forged := record(t, "aiko", "laptop", laptop, "phone", evil.sig)
	orphan := record(t, "aiko", "watch", evil, "nowhere", evil.sig)
	a := record(t, "aiko", "a", newKeys(t), "b", evil.sig)
	b := record(t, "aiko", "b", newKeys(t), "a", evil.sig)
	elsewhere := record(t, "bob", "phone", evil, "phone", evil.sig)
	edited := record(t, "aiko", "pad", newKeys(t), "phone", phone.sig)
	edited.EncryptionKey = evil.enc.PublicKey().Bytes()
	recoveryRoot := DeviceRecord{Person: "aiko", ID: "r", Kind: RecordRecovery, SignatureKey: other.sig.Public().(ed25519.PublicKey), Voucher: "r"}
	recoveryRoot.Vouch = ed25519.Sign(other.sig, recoveryRoot.vouchMessage())
	recoveryRoot.Possession = ed25519.Sign(other.sig, recoveryRoot.possessionMessage())
	d, dropped := NewDirectory("aiko", []DeviceRecord{root, forged, orphan, a, b, elsewhere, edited, recoveryRoot}, nil)
	if dropped != 7 || len(d.Devices()) != 1 {
		t.Fatalf("dropped %d, devices %+v", dropped, d.Devices())
	}
	// Two records under one id, or with one key, are both dropped.
	twin := record(t, "aiko", "phone", newKeys(t), "phone", phone.sig)
	if _, dropped := NewDirectory("aiko", []DeviceRecord{root, twin}, nil); dropped != 2 {
		t.Fatalf("dropped %d of two twins", dropped)
	}
	sameKey := record(t, "aiko", "laptop", phone, "phone", phone.sig)
	if _, dropped := NewDirectory("aiko", []DeviceRecord{root, sameKey}, nil); dropped != 2 {
		t.Fatalf("dropped %d of two records with one key", dropped)
	}
}

// A record proves its own key: no one lists another's key as their
// device, so another's message never verifies as theirs.
func TestDirectoryPossession(t *testing.T) {
	mallory, bob := newKeys(t), newKeys(t)
	root := record(t, "mallory", "phone", mallory, "phone", mallory.sig)
	stolen := record(t, "mallory", "bobs", bob, "phone", mallory.sig)
	stolen.Possession = ed25519.Sign(mallory.sig, stolen.possessionMessage())
	if _, dropped := NewDirectory("mallory", []DeviceRecord{root, stolen}, nil); dropped != 1 {
		t.Fatalf("dropped %d, want the record of a key mallory does not hold", dropped)
	}
	unproved := record(t, "mallory", "pad", newKeys(t), "phone", mallory.sig)
	unproved.Possession = nil
	if _, dropped := NewDirectory("mallory", []DeviceRecord{root, unproved}, nil); dropped != 1 {
		t.Fatalf("dropped %d, want the record with no proof", dropped)
	}
}

// A revoked key vouches for nothing it signs after, the service cannot
// undo a revocation the device passes, and a dispute between the
// person's devices and a thief's fails closed.
func TestDirectoryRevocation(t *testing.T) {
	phone, laptop, spawn, stranger := newKeys(t), newKeys(t), newKeys(t), newKeys(t)
	root := record(t, "aiko", "phone", phone, "phone", phone.sig)
	lap := record(t, "aiko", "laptop", laptop, "phone", phone.sig)
	revoke := func(key []byte, by string, with ed25519.PrivateKey, keep ...[]byte) Revocation {
		t.Helper()
		v := Revocation{Person: "aiko", Key: key, Keep: keep}
		if err := v.Sign(by, with); err != nil {
			t.Fatal(err)
		}
		return v
	}
	lost := revoke(root.SignatureKey, "laptop", laptop.sig, lap.SignatureKey)

	// The thief signs a new device with the stolen phone.
	minted := record(t, "aiko", "spawn", spawn, "phone", phone.sig)
	d, _ := NewDirectory("aiko", []DeviceRecord{root, lap, minted}, []Revocation{lost})
	if _, ok := d.Device("spawn"); ok {
		t.Error("a device a revoked key vouched for and no revocation keeps stands")
	}
	if _, ok := d.Device("laptop"); !ok {
		t.Error("the device the revocation keeps is gone")
	}
	// The service leaves the revocation out; the device keeps its own.
	if d, _ := NewDirectory("aiko", []DeviceRecord{root, lap}, d.Revocations()); !d.Revoked(root.SignatureKey) {
		t.Error("a revocation the device holds was undone")
	}

	// The thief answers with revocations of its own: the phone keeping
	// spawn, and the laptop. Both sides lose; nothing the thief holds
	// stands.
	counter := revoke(root.SignatureKey, "spawn", spawn.sig, minted.SignatureKey)
	d, _ = NewDirectory("aiko", []DeviceRecord{root, lap, minted}, []Revocation{lost, counter})
	if len(d.Devices()) != 0 {
		t.Errorf("after a dispute %+v stand", d.Devices())
	}
	// A key keeps nothing it revokes itself.
	self := revoke(root.SignatureKey, "phone", phone.sig, lap.SignatureKey)
	if d, _ := NewDirectory("aiko", []DeviceRecord{root, lap}, []Revocation{self}); len(d.Devices()) != 0 {
		t.Errorf("a self-revocation kept %+v", d.Devices())
	}
	// A revocation counts only when its signer's chain verifies.
	outsider := revoke(root.SignatureKey, "laptop", stranger.sig)
	other := revoke(root.SignatureKey, "nobody", stranger.sig)
	if d, _ := NewDirectory("aiko", []DeviceRecord{root, lap}, []Revocation{outsider, other}); d.Revoked(root.SignatureKey) {
		t.Error("a revocation no record of the person signed counts")
	}
	bad := lost
	bad.Keep = append(slices.Clone(bad.Keep), minted.SignatureKey)
	if d, _ := NewDirectory("aiko", []DeviceRecord{root, lap, minted}, []Revocation{bad}); d.Revoked(root.SignatureKey) {
		t.Error("a revocation whose list was changed counts")
	}
}

// A chain as long as the records allowed, listed from its end, is walked
// without recursion, and records past MaxRecords are dropped.
func TestDirectoryBounds(t *testing.T) {
	prev := newKeys(t)
	recs := []DeviceRecord{record(t, "aiko", "d0", prev, "d0", prev.sig)}
	for i := 1; i < MaxRecords+10; i++ {
		k := newKeys(t)
		recs = append(recs, record(t, "aiko", fmt.Sprintf("d%d", i), k, fmt.Sprintf("d%d", i-1), prev.sig))
		prev = k
	}
	slices.Reverse(recs)
	d, dropped := NewDirectory("aiko", recs, nil)
	if len(d.Devices()) != 0 || dropped != len(recs) {
		t.Fatalf("%d devices stand, %d dropped; the first MaxRecords listed hang from records past it", len(d.Devices()), dropped)
	}
	slices.Reverse(recs)
	if d, dropped := NewDirectory("aiko", recs, nil); len(d.Devices()) != MaxRecords || dropped != 10 {
		t.Fatalf("%d devices stand, %d dropped", len(d.Devices()), dropped)
	}
}

func TestSecurityCode(t *testing.T) {
	a1, b1, b2 := newKeys(t), newKeys(t), newKeys(t)
	da, _ := NewDirectory("aiko", []DeviceRecord{record(t, "aiko", "phone", a1, "phone", a1.sig)}, nil)
	db, _ := NewDirectory("bob", []DeviceRecord{record(t, "bob", "phone", b1, "phone", b1.sig)}, nil)
	code := SecurityCode(da, db)
	if code != SecurityCode(db, da) {
		t.Fatal("the code depends on who computes it")
	}
	if !regexp.MustCompile(`^\d{5}( \d{5}){11}$`).MatchString(code) {
		t.Fatalf("code %q", code)
	}
	dbNew, _ := NewDirectory("bob", []DeviceRecord{record(t, "bob", "phone", b2, "phone", b2.sig)}, nil)
	if SecurityCode(da, dbNew) == code {
		t.Fatal("a new root keeps the code")
	}
}
