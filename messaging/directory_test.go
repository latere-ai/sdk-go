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

// record makes the record of a device with keys k, vouched for by
// voucher, whose key by signs the vouch: a nil voucher makes a root. The
// vouch is signed directly, so a test can sign it with a key that is not
// the voucher's.
func record(t *testing.T, person, id string, k keys, voucher *DeviceRecord, by ed25519.PrivateKey) DeviceRecord {
	t.Helper()
	r, err := NewDeviceRecord(person, id, k.sig, k.enc.PublicKey(), voucher)
	if err != nil {
		t.Fatal(err)
	}
	if voucher != nil {
		r.Vouch = ed25519.Sign(by, r.body(vouchLabel))
	}
	return r
}

// named is a record that stands for an id and key alone, for a record to
// name as its voucher.
func named(person, id string, k keys) *DeviceRecord {
	return &DeviceRecord{Person: person, ID: id, Kind: RecordDevice, SignatureKey: k.sig.Public().(ed25519.PublicKey)}
}

func revocation(t *testing.T, person string, key []byte, by string, with ed25519.PrivateKey, keep ...[]byte) Revocation {
	t.Helper()
	v := Revocation{Person: person, Key: key, Keep: keep}
	if err := v.Sign(by, with); err != nil {
		t.Fatal(err)
	}
	return v
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
	root := record(t, "aiko", "phone", phone, nil, nil)
	recovery, err := NewRecoveryRecord("aiko", "recovery-1", rk, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := recovery.Sign(phone.sig); err != nil {
		t.Fatal(err)
	}
	if err := recovery.Sign(laptop.sig); err == nil {
		t.Fatal("a key the record does not name vouched for it")
	}
	if _, err := NewRecoveryRecord("aiko", "recovery-2", rk, recovery); err == nil {
		t.Fatal("a recovery record vouched for a recovery record")
	}
	recs := []DeviceRecord{
		root,
		recovery,
		record(t, "aiko", "laptop", laptop, &root, phone.sig),
		record(t, "aiko", "tablet", tablet, &recovery, rsig),
	}
	// The phone is lost; the laptop revokes it and keeps what it vouched
	// for.
	lost := revocation(t, "aiko", recs[0].SignatureKey, "laptop", laptop.sig, recs[1].SignatureKey, recs[2].SignatureKey)
	d, dropped := NewDirectory("aiko", recs, []Revocation{lost}, nil)
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
	phone, laptop, evil, other, ka, kb := newKeys(t), newKeys(t), newKeys(t), newKeys(t), newKeys(t), newKeys(t)
	root := record(t, "aiko", "phone", phone, nil, nil)
	forged := record(t, "aiko", "laptop", laptop, &root, evil.sig)
	orphan := record(t, "aiko", "watch", evil, named("aiko", "nowhere", other), other.sig)
	a := record(t, "aiko", "a", ka, named("aiko", "b", kb), kb.sig)
	b := record(t, "aiko", "b", kb, named("aiko", "a", ka), ka.sig)
	elsewhere := record(t, "bob", "phone", evil, nil, nil)
	edited := record(t, "aiko", "pad", newKeys(t), &root, phone.sig)
	edited.EncryptionKey = evil.enc.PublicKey().Bytes()
	misnamed := record(t, "aiko", "pen", newKeys(t), named("aiko", "phone", evil), evil.sig)
	recoveryRoot := DeviceRecord{Person: "aiko", ID: "r", Kind: RecordRecovery, SignatureKey: other.sig.Public().(ed25519.PublicKey), Voucher: "r",
		VoucherKey: other.sig.Public().(ed25519.PublicKey)}
	recoveryRoot.Vouch = ed25519.Sign(other.sig, recoveryRoot.body(vouchLabel))
	recoveryRoot.Possession = ed25519.Sign(other.sig, recoveryRoot.body(possessionLabel))
	d, dropped := NewDirectory("aiko", []DeviceRecord{root, forged, orphan, a, b, elsewhere, edited, misnamed, recoveryRoot}, nil, nil)
	if dropped != 8 || len(d.Devices()) != 1 {
		t.Fatalf("dropped %d, devices %+v", dropped, d.Devices())
	}
	// Two records under one id, or with one key, are both dropped.
	twin := record(t, "aiko", "phone", newKeys(t), nil, nil)
	if _, dropped := NewDirectory("aiko", []DeviceRecord{root, twin}, nil, nil); dropped != 2 {
		t.Fatalf("dropped %d of two twins", dropped)
	}
	sameKey := record(t, "aiko", "laptop", phone, nil, nil)
	if _, dropped := NewDirectory("aiko", []DeviceRecord{root, sameKey}, nil, nil); dropped != 2 {
		t.Fatalf("dropped %d of two records with one key", dropped)
	}
}

// A record proves its own key: no one lists another's key as their
// device, so another's message never verifies as theirs.
func TestDirectoryPossession(t *testing.T) {
	mallory, bob := newKeys(t), newKeys(t)
	root := record(t, "mallory", "phone", mallory, nil, nil)
	stolen := record(t, "mallory", "bobs", bob, &root, mallory.sig)
	stolen.Possession = ed25519.Sign(mallory.sig, stolen.body(possessionLabel))
	if _, dropped := NewDirectory("mallory", []DeviceRecord{root, stolen}, nil, nil); dropped != 1 {
		t.Fatalf("dropped %d, want the record of a key mallory does not hold", dropped)
	}
	unproved := record(t, "mallory", "pad", newKeys(t), &root, mallory.sig)
	unproved.Possession = nil
	if _, dropped := NewDirectory("mallory", []DeviceRecord{root, unproved}, nil, nil); dropped != 1 {
		t.Fatalf("dropped %d, want the record with no proof", dropped)
	}
}

// Where a record hangs is its own word: the service, which holds no key
// of the person's, cannot list a device under a root of its own, so no
// root it makes up acts on the person's devices.
func TestDirectoryVoucherBound(t *testing.T) {
	phone, laptop, svc := newKeys(t), newKeys(t), newKeys(t)
	root := record(t, "aiko", "phone", phone, nil, nil)
	lap := record(t, "aiko", "laptop", laptop, &root, phone.sig)
	forgedRoot := record(t, "aiko", "svc", svc, nil, nil)
	for name, moved := range map[string]DeviceRecord{"root": root, "device": lap} {
		moved.Voucher, moved.VoucherKey = "svc", forgedRoot.SignatureKey
		moved.Vouch = ed25519.Sign(svc.sig, moved.body(vouchLabel))
		d, _ := NewDirectory("aiko", []DeviceRecord{forgedRoot, moved}, nil, nil) // the record it hung from withheld
		if _, ok := d.Device(moved.ID); ok {
			t.Errorf("the %s, moved under the service's root, stands", name)
		}
	}
	// A root the service lists revokes nothing under another root.
	v := revocation(t, "aiko", lap.SignatureKey, "svc", svc.sig)
	d, _ := NewDirectory("aiko", []DeviceRecord{root, lap, forgedRoot}, []Revocation{v}, nil)
	if d.Revoked(lap.SignatureKey) {
		t.Error("a root the service listed revoked the person's device")
	}
	if _, err := d.CheckRevocation(v); err == nil {
		t.Error("the service would keep a revocation by another root")
	}
}

// A revoked key vouches for nothing it signs after, the service cannot
// undo a revocation the device kept, and a dispute between the person's
// devices and a thief's fails closed.
func TestDirectoryRevocation(t *testing.T) {
	phone, laptop, spawn, stranger := newKeys(t), newKeys(t), newKeys(t), newKeys(t)
	root := record(t, "aiko", "phone", phone, nil, nil)
	lap := record(t, "aiko", "laptop", laptop, &root, phone.sig)
	lost := revocation(t, "aiko", root.SignatureKey, "laptop", laptop.sig, lap.SignatureKey)

	// The thief signs a new device with the stolen phone.
	minted := record(t, "aiko", "spawn", spawn, &root, phone.sig)
	d, _ := NewDirectory("aiko", []DeviceRecord{root, lap, minted}, []Revocation{lost}, nil)
	if _, ok := d.Device("spawn"); ok {
		t.Error("a device a revoked key vouched for and no revocation keeps stands")
	}
	if _, ok := d.Device("laptop"); !ok {
		t.Error("the device the revocation keeps is gone")
	}
	kept := d.Revocations()
	// The service leaves the revocation out, or the laptop that signed
	// it: the revocation the device kept still applies.
	for name, recs := range map[string][]DeviceRecord{"revocation": {root, lap, minted}, "signer": {root, minted}} {
		if d, _ := NewDirectory("aiko", recs, nil, kept); !d.Revoked(root.SignatureKey) {
			t.Errorf("leaving out the %s undid a revocation the device kept", name)
		} else if _, ok := d.Device("spawn"); ok {
			t.Errorf("leaving out the %s let the thief's device stand", name)
		}
	}
	// Without what the device kept, the revocation stops counting when
	// its signer is left out, so the apps keep it.
	if d, _ := NewDirectory("aiko", []DeviceRecord{root, minted}, []Revocation{lost}, nil); !slices.ContainsFunc(d.Devices(), func(r DeviceRecord) bool { return r.ID == "spawn" }) {
		t.Error("a revocation whose signer is left out counted")
	}

	// The thief answers with revocations of its own: the phone keeping
	// spawn, and the laptop. Both sides lose; nothing the thief holds
	// stands.
	counter := revocation(t, "aiko", root.SignatureKey, "spawn", spawn.sig, minted.SignatureKey)
	d, _ = NewDirectory("aiko", []DeviceRecord{root, lap, minted}, []Revocation{lost, counter}, nil)
	if len(d.Devices()) != 0 {
		t.Errorf("after a dispute %+v stand", d.Devices())
	}
	// A key keeps nothing it revokes itself.
	self := revocation(t, "aiko", root.SignatureKey, "phone", phone.sig, lap.SignatureKey)
	if d, _ := NewDirectory("aiko", []DeviceRecord{root, lap}, []Revocation{self}, nil); len(d.Devices()) != 0 {
		t.Errorf("a self-revocation kept %+v", d.Devices())
	}
	// A revocation counts only when its signer's chain verifies.
	outsider := revocation(t, "aiko", root.SignatureKey, "laptop", stranger.sig)
	other := revocation(t, "aiko", root.SignatureKey, "nobody", stranger.sig)
	if d, _ := NewDirectory("aiko", []DeviceRecord{root, lap}, []Revocation{outsider, other}, nil); d.Revoked(root.SignatureKey) {
		t.Error("a revocation no record of the person signed counts")
	}
	bad := lost
	bad.Keep = append(slices.Clone(bad.Keep), minted.SignatureKey)
	if d, _ := NewDirectory("aiko", []DeviceRecord{root, lap, minted}, []Revocation{bad}, nil); d.Revoked(root.SignatureKey) {
		t.Error("a revocation whose list was changed counts")
	}
}

// A record kept under a revoked voucher stands only when the voucher is
// kept the same way, every link up to the root.
func TestDirectoryKeptChain(t *testing.T) {
	phone, laptop, xk, yk := newKeys(t), newKeys(t), newKeys(t), newKeys(t)
	root := record(t, "aiko", "phone", phone, nil, nil)
	lap := record(t, "aiko", "laptop", laptop, &root, phone.sig)
	x := record(t, "aiko", "x", xk, &root, phone.sig)
	y := record(t, "aiko", "y", yk, &x, xk.sig)
	recs := []DeviceRecord{y, x, lap, root}
	keepY := revocation(t, "aiko", x.SignatureKey, "laptop", laptop.sig, y.SignatureKey)
	for name, c := range map[string]struct {
		phoneKeeps [][]byte
		stand      []string
	}{
		"x kept":     {[][]byte{lap.SignatureKey, x.SignatureKey}, []string{"laptop", "y"}},
		"x not kept": {[][]byte{lap.SignatureKey}, []string{"laptop"}},
	} {
		lost := revocation(t, "aiko", root.SignatureKey, "laptop", laptop.sig, c.phoneKeeps...)
		d, _ := NewDirectory("aiko", recs, []Revocation{lost, keepY}, nil)
		var ids []string
		for _, r := range d.Devices() {
			ids = append(ids, r.ID)
		}
		if !slices.Equal(ids, c.stand) {
			t.Errorf("%s: %v stand, want %v", name, ids, c.stand)
		}
	}
}

// The service keeps a revocation that counts and changes something, so
// what it keeps for a person stays within MaxRevocations.
func TestCheckRevocation(t *testing.T) {
	phone, laptop, tablet := newKeys(t), newKeys(t), newKeys(t)
	root := record(t, "aiko", "phone", phone, nil, nil)
	lap := record(t, "aiko", "laptop", laptop, &root, phone.sig)
	tab := record(t, "aiko", "tablet", tablet, &root, phone.sig)
	recs := []DeviceRecord{root, lap, tab}
	lost := revocation(t, "aiko", root.SignatureKey, "laptop", laptop.sig, lap.SignatureKey, tab.SignatureKey)
	d, _ := NewDirectory("aiko", recs, nil, nil)
	if changes, err := d.CheckRevocation(lost); err != nil || !changes {
		t.Fatalf("the first revocation: %v, %v", changes, err)
	}
	d, _ = NewDirectory("aiko", recs, []Revocation{lost}, nil)
	for name, c := range map[string]struct {
		v       Revocation
		changes bool
	}{
		"the same keep":     {revocation(t, "aiko", root.SignatureKey, "tablet", tablet.sig, tab.SignatureKey, lap.SignatureKey), false},
		"a keep it lacks":   {revocation(t, "aiko", root.SignatureKey, "tablet", tablet.sig, tab.SignatureKey), true},
		"its own key":       {revocation(t, "aiko", root.SignatureKey, "phone", phone.sig, tab.SignatureKey, lap.SignatureKey), true},
		"a key not revoked": {revocation(t, "aiko", tab.SignatureKey, "laptop", laptop.sig), true},
	} {
		if changes, err := d.CheckRevocation(c.v); err != nil || changes != c.changes {
			t.Errorf("%s: changes %v, %v", name, changes, err)
		}
	}
	for name, v := range map[string]Revocation{
		"another person": revocation(t, "bob", root.SignatureKey, "laptop", laptop.sig),
		"no listed key":  revocation(t, "aiko", newKeys(t).sig.Public().(ed25519.PublicKey), "laptop", laptop.sig),
		"no signer":      revocation(t, "aiko", root.SignatureKey, "pad", laptop.sig),
		"a bad signature": func() Revocation {
			v := revocation(t, "aiko", root.SignatureKey, "laptop", laptop.sig)
			v.Signature[0] ^= 1
			return v
		}(),
	} {
		if _, err := d.CheckRevocation(v); err == nil {
			t.Errorf("%s: kept", name)
		}
	}
}

// A chain as long as the records allowed, listed from its end, is walked
// without recursion; past MaxRecords records, or MaxRevocations
// revocations that may count, nothing stands, whatever order the service
// lists them in.
func TestDirectoryBounds(t *testing.T) {
	ks := []keys{newKeys(t)}
	recs := []DeviceRecord{record(t, "aiko", "d0", ks[0], nil, nil)}
	for i := 1; i < MaxRecords; i++ {
		ks = append(ks, newKeys(t))
		recs = append(recs, record(t, "aiko", fmt.Sprintf("d%d", i), ks[i], &recs[i-1], ks[i-1].sig))
	}
	root, lap := recs[0], recs[1]
	rootKey, lapKey := ks[0].sig, ks[1].sig
	slices.Reverse(recs)
	if d, dropped := NewDirectory("aiko", recs, nil, nil); len(d.Devices()) != MaxRecords || dropped != 0 {
		t.Fatalf("%d devices stand, %d dropped", len(d.Devices()), dropped)
	}
	extra := record(t, "aiko", "extra", newKeys(t), &recs[0], ks[len(ks)-1].sig)
	if d, dropped := NewDirectory("aiko", append(slices.Clone(recs), extra), nil, nil); len(d.Devices()) != 0 || dropped != MaxRecords+1 {
		t.Fatalf("past MaxRecords %d devices stand, %d dropped", len(d.Devices()), dropped)
	}

	// Revocations a thief signs with the stolen root, of keys no record
	// holds, do not count toward the bound, and the person's still does.
	var flood []Revocation
	for range MaxRevocations + 1 {
		flood = append(flood, revocation(t, "aiko", newKeys(t).sig.Public().(ed25519.PublicKey), root.ID, rootKey))
	}
	lost := revocation(t, "aiko", root.SignatureKey, lap.ID, lapKey, lap.SignatureKey)
	d, _ := NewDirectory("aiko", recs, append(flood, lost), nil)
	if !d.Revoked(root.SignatureKey) || len(d.Devices()) != MaxRecords-1 {
		t.Errorf("after a flood of revocations of no one's key the root is revoked %v, %d stand", d.Revoked(root.SignatureKey), len(d.Devices()))
	}
	// Revocations that may count past the bound: nothing stands.
	var many []Revocation
	for range MaxRevocations + 1 {
		many = append(many, revocation(t, "aiko", lap.SignatureKey, root.ID, rootKey, newKeys(t).sig.Public().(ed25519.PublicKey)))
	}
	if d, dropped := NewDirectory("aiko", recs, many, nil); len(d.Devices()) != 0 || dropped != len(recs) {
		t.Errorf("past MaxRevocations %d devices stand, %d dropped", len(d.Devices()), dropped)
	}
}

func TestSecurityCode(t *testing.T) {
	a1, b1, b2 := newKeys(t), newKeys(t), newKeys(t)
	da, _ := NewDirectory("aiko", []DeviceRecord{record(t, "aiko", "phone", a1, nil, nil)}, nil, nil)
	db, _ := NewDirectory("bob", []DeviceRecord{record(t, "bob", "phone", b1, nil, nil)}, nil, nil)
	code := SecurityCode(da, db)
	if code != SecurityCode(db, da) {
		t.Fatal("the code depends on who computes it")
	}
	if !regexp.MustCompile(`^\d{5}( \d{5}){11}$`).MatchString(code) {
		t.Fatalf("code %q", code)
	}
	dbNew, _ := NewDirectory("bob", []DeviceRecord{record(t, "bob", "phone", b2, nil, nil)}, nil, nil)
	if SecurityCode(da, dbNew) == code {
		t.Fatal("a new root keeps the code")
	}
}
