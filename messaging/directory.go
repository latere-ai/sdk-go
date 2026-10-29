// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package messaging

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strings"

	"latere.ai/x/sdk-go/mls"
)

// A person's keys are in the directory as records, each vouched for by
// another of the same person and signed by its own key, which proves
// the person's device holds it. A device record holds a device's
// signature key, the one its leaves carry in every group, and the X25519
// key the person's other devices seal to it with. A recovery record
// holds the public half of the signing key the person's recovery key
// derives. The first device vouches for itself, which makes it a root,
// and for the recovery record; every later device is vouched for by an
// approved device or by the recovery key.
//
// A key the person no longer holds is revoked by a Revocation, signed
// by one of their records, which the service stores and lists but cannot
// make or undo. A revoked key is no record's key and vouches for
// nothing, and a record it vouched for stays only when every revocation
// of the key names it in Keep: a device of the person's that revokes a
// stolen device names the devices that stay, and a revocation the thief
// signs that names others makes both lose them, so a dispute fails
// closed. A revocation counts when its signer's chain of vouches reaches
// a root, revoked or not, so a stolen key can revoke the person's other
// devices too; the person then starts over from a new root or the
// recovery key. The apps keep every revocation they have seen and pass
// it to NewDirectory with the service's, so the service can withhold a
// revocation only from a device that never saw it.

// MaxRecords bounds the records, and the revocations, NewDirectory takes
// for one person; those past it are dropped.
const MaxRecords = 256

// RecordKind is what a directory record holds.
type RecordKind uint8

// The kinds of record.
const (
	// RecordDevice is a device's keys.
	RecordDevice RecordKind = 1
	// RecordRecovery is the public key of a recovery key's signing key.
	RecordRecovery RecordKind = 2
)

// String is the kind as the API names it.
func (k RecordKind) String() string {
	switch k {
	case RecordDevice:
		return "device"
	case RecordRecovery:
		return "recovery"
	}
	return fmt.Sprintf("record(%d)", uint8(k))
}

// MarshalText encodes the kind as the API names it.
func (k RecordKind) MarshalText() ([]byte, error) {
	if k != RecordDevice && k != RecordRecovery {
		return nil, fmt.Errorf("messaging: unknown record kind %d", uint8(k))
	}
	return []byte(k.String()), nil
}

// UnmarshalText decodes a kind the API names.
func (k *RecordKind) UnmarshalText(b []byte) error {
	for _, v := range []RecordKind{RecordDevice, RecordRecovery} {
		if string(b) == v.String() {
			*k = v
			return nil
		}
	}
	return fmt.Errorf("messaging: unknown record kind %q", b)
}

// DeviceRecord is one record of a person's keys in the directory.
type DeviceRecord struct {
	Person string `json:"person"`
	// ID is the device's id, or the recovery record's.
	ID   string     `json:"id"`
	Kind RecordKind `json:"kind"`
	// SignatureKey is the Ed25519 public key.
	SignatureKey []byte `json:"signature_key"`
	// EncryptionKey is a device's X25519 public key; empty for a
	// recovery record.
	EncryptionKey []byte `json:"encryption_key,omitempty"`
	// Voucher is the id of the record whose key signed Vouch: the
	// record's own for a root.
	Voucher string `json:"voucher"`
	Vouch   []byte `json:"vouch"`
	// Possession is the record's own key's signature of the record
	// without its voucher, made when the device made its keys.
	Possession []byte `json:"possession"`
}

// The labels that separate a vouch, a proof of possession and a
// revocation from every other use of a device's key.
const (
	vouchLabel      = "latere messaging vouch v1"
	possessionLabel = "latere messaging possession v1"
	revocationLabel = "latere messaging revocation v1"
)

func (r *DeviceRecord) possessionMessage() []byte {
	return encode(func(w *writer) {
		w.vec8([]byte(possessionLabel))
		w.vec8([]byte(r.Person))
		w.vec8([]byte(r.ID))
		w.u8(uint8(r.Kind))
		w.vec8(r.SignatureKey)
		w.vec8(r.EncryptionKey)
	})
}

func (r *DeviceRecord) vouchMessage() []byte {
	return encode(func(w *writer) {
		w.vec8([]byte(vouchLabel))
		w.vec8([]byte(r.Person))
		w.vec8([]byte(r.ID))
		w.u8(uint8(r.Kind))
		w.vec8(r.SignatureKey)
		w.vec8(r.EncryptionKey)
		w.vec8([]byte(r.Voucher))
	})
}

// Root reports whether the record vouches for itself.
func (r *DeviceRecord) Root() bool { return r.Voucher == r.ID }

// Fingerprint is the SHA-256 of a record's signature key, which the apps
// pin for each person's roots.
func (r *DeviceRecord) Fingerprint() []byte {
	h := sha256.Sum256(append([]byte(vouchLabel+" fingerprint\x00"), r.SignatureKey...))
	return h[:]
}

func (r *DeviceRecord) validate() error {
	if err := validID("person", r.Person); err != nil {
		return err
	}
	if err := validID("record", r.ID); err != nil {
		return err
	}
	if err := validID("voucher", r.Voucher); err != nil {
		return err
	}
	if len(r.SignatureKey) != ed25519.PublicKeySize {
		return errors.New("messaging: a record's signature key is an Ed25519 key")
	}
	switch r.Kind {
	case RecordDevice:
		if _, err := ecdh.X25519().NewPublicKey(r.EncryptionKey); err != nil {
			return fmt.Errorf("messaging: a device's encryption key: %w", err)
		}
	case RecordRecovery:
		if len(r.EncryptionKey) != 0 {
			return errors.New("messaging: a recovery record has no encryption key")
		}
		if r.Root() {
			return errors.New("messaging: a recovery record is vouched for by a device")
		}
	default:
		return fmt.Errorf("messaging: unknown record kind %d", uint8(r.Kind))
	}
	return nil
}

// Sign vouches for the record with the voucher's private key, setting
// Voucher and Vouch. A device signs as itself for its own root record.
func (r *DeviceRecord) Sign(voucher string, key ed25519.PrivateKey) error {
	r.Voucher = voucher
	if err := r.validate(); err != nil {
		return err
	}
	r.Vouch = ed25519.Sign(key, r.vouchMessage())
	return nil
}

// NewDeviceRecord returns the record of a device with its signature key
// and encryption key, its possession proved and not yet vouched for.
func NewDeviceRecord(person, device string, key ed25519.PrivateKey, encryptionKey *ecdh.PublicKey) (DeviceRecord, error) {
	return newRecord(DeviceRecord{Person: person, ID: device, Kind: RecordDevice, EncryptionKey: encryptionKey.Bytes()}, key)
}

// NewRecoveryRecord returns the record of a person's recovery key, its
// possession proved and not yet vouched for: the device that made the
// key vouches for it.
func NewRecoveryRecord(person, id string, recovery *RecoveryKey) (DeviceRecord, error) {
	key, err := recovery.SigningKey()
	if err != nil {
		return DeviceRecord{}, err
	}
	return newRecord(DeviceRecord{Person: person, ID: id, Kind: RecordRecovery}, key)
}

func newRecord(r DeviceRecord, key ed25519.PrivateKey) (DeviceRecord, error) {
	if len(key) != ed25519.PrivateKeySize {
		return DeviceRecord{}, errors.New("messaging: a record's key is an Ed25519 key")
	}
	r.SignatureKey = bytes.Clone(key.Public().(ed25519.PublicKey))
	if err := validID("person", r.Person); err != nil {
		return DeviceRecord{}, err
	}
	if err := validID("record", r.ID); err != nil {
		return DeviceRecord{}, err
	}
	r.Possession = ed25519.Sign(key, r.possessionMessage())
	return r, nil
}

// Revocation is a person's signed word that a key of theirs is no longer
// theirs: a device they removed or lost, or a recovery key they
// replaced.
type Revocation struct {
	Person string `json:"person"`
	// Key is the signature key revoked.
	Key []byte `json:"key"`
	// Keep are the signature keys of the records Key vouched for that
	// stay the person's; what those vouched for stays with them.
	Keep [][]byte `json:"keep,omitempty"`
	// By is the id of the record whose key signed it.
	By        string `json:"by"`
	Signature []byte `json:"signature"`
}

func (v *Revocation) message() []byte {
	return encode(func(w *writer) {
		w.vec8([]byte(revocationLabel))
		w.vec8([]byte(v.Person))
		w.vec8(v.Key)
		w.u16(uint16(len(v.Keep)))
		for _, k := range v.Keep {
			w.vec8(k)
		}
		w.vec8([]byte(v.By))
	})
}

func (v *Revocation) validate() error {
	if err := validID("person", v.Person); err != nil {
		return err
	}
	if err := validID("record", v.By); err != nil {
		return err
	}
	if len(v.Key) != ed25519.PublicKeySize {
		return errors.New("messaging: a revocation is of an Ed25519 key")
	}
	if len(v.Keep) > MaxRecords {
		return errors.New("messaging: a revocation keeps at most MaxRecords records")
	}
	seen := make(map[string]bool, len(v.Keep))
	for _, k := range v.Keep {
		if len(k) != ed25519.PublicKeySize || seen[string(k)] || bytes.Equal(k, v.Key) {
			return errors.New("messaging: a revocation keeps distinct Ed25519 keys other than the one it revokes")
		}
		seen[string(k)] = true
	}
	return nil
}

// Sign signs the revocation with the private key of the record by,
// setting By and Signature.
func (v *Revocation) Sign(by string, key ed25519.PrivateKey) error {
	v.By = by
	if err := v.validate(); err != nil {
		return err
	}
	v.Signature = ed25519.Sign(key, v.message())
	return nil
}

// Directory is the part of a person's records whose vouches verify,
// each through a chain of vouches to a root, with the person's
// revocations applied.
type Directory struct {
	person string
	// records are the records that stand: verified and not revoked.
	records map[string]DeviceRecord
	// verified are the records whose chain verifies, revoked or not.
	verified    map[string]DeviceRecord
	revoked     map[string]bool
	revocations []Revocation
}

// NewDirectory verifies a person's records against their revocations and
// keeps the records that stand. A record that fails, names another
// person, is revoked or is past MaxRecords is left out and counted in
// dropped, so a record the service made up costs nothing but itself. Two
// records under one id, or with one key, are both left out.
func NewDirectory(person string, records []DeviceRecord, revocations []Revocation) (d *Directory, dropped int) {
	d = &Directory{person: person, records: make(map[string]DeviceRecord),
		verified: make(map[string]DeviceRecord), revoked: make(map[string]bool)}
	var recs []DeviceRecord
	for _, r := range records {
		if r.Person == person && len(recs) < MaxRecords {
			recs = append(recs, r)
		}
	}
	n := len(recs)
	byID := make(map[string]int, n)
	ok := make([]bool, n)
	ids := make(map[string]int, n)
	keys := make(map[string]int, n)
	for _, r := range recs {
		ids[r.ID]++
		keys[string(r.SignatureKey)]++
	}
	for i, r := range recs {
		byID[r.ID] = i
		ok[i] = ids[r.ID] == 1 && keys[string(r.SignatureKey)] == 1 && r.validate() == nil &&
			ed25519.Verify(r.SignatureKey, r.possessionMessage(), r.Possession)
	}
	parent := func(i int) (int, bool) {
		p, found := byID[recs[i].Voucher]
		return p, found
	}
	vouched := func(i, p int) bool {
		return ed25519.Verify(recs[p].SignatureKey, recs[i].vouchMessage(), recs[i].Vouch)
	}

	// The chains that verify, with no revocation applied.
	verified := walk(n, func(i int) (bool, bool) {
		r := recs[i]
		switch {
		case !ok[i]:
			return false, true
		case r.Root():
			return r.Kind == RecordDevice && ed25519.Verify(r.SignatureKey, r.vouchMessage(), r.Vouch), true
		}
		if _, found := parent(i); !found {
			return false, true
		}
		return false, false
	}, parent, vouched)

	// The revocations that count, and what each revoked key keeps: what
	// every revocation of it names.
	keep := make(map[string]map[string]bool)
	counted := 0
	for _, v := range revocations {
		if v.Person != person {
			continue
		}
		if counted == MaxRecords {
			break
		}
		s, found := byID[v.By]
		if !found || !verified[s] || v.validate() != nil || !ed25519.Verify(recs[s].SignatureKey, v.message(), v.Signature) {
			continue
		}
		counted++
		names := make(map[string]bool, len(v.Keep))
		if !bytes.Equal(recs[s].SignatureKey, v.Key) { // a key keeps nothing it revokes itself
			for _, k := range v.Keep {
				names[string(k)] = true
			}
		}
		if prev, seen := keep[string(v.Key)]; seen {
			for k := range prev {
				if !names[k] {
					delete(prev, k)
				}
			}
		} else {
			keep[string(v.Key)] = names
		}
		d.revoked[string(v.Key)] = true
		d.revocations = append(d.revocations, cloneRevocation(v))
	}

	// The records that stand: not revoked, through vouchers that stand,
	// or through a revoked voucher whose revocations all keep them.
	stands := walk(n, func(i int) (bool, bool) {
		r := recs[i]
		switch {
		case !verified[i] || d.revoked[string(r.SignatureKey)]:
			return false, true
		case r.Root():
			return true, true
		}
		p, _ := parent(i) // verified, so the voucher is there
		if d.revoked[string(recs[p].SignatureKey)] {
			return keep[string(recs[p].SignatureKey)][string(r.SignatureKey)], true
		}
		return false, false
	}, parent, func(int, int) bool { return true })

	for i, r := range recs {
		if verified[i] {
			d.verified[r.ID] = r
		}
		if stands[i] {
			d.records[r.ID] = r
		}
	}
	return d, len(records) - len(d.records)
}

// walk decides a value for each of n records that hang from one another
// by their vouchers, without recursion, so a long chain costs no stack.
// leaf decides a record on its own when it can (done); otherwise the
// record's value is its voucher's and edge's. A cycle decides false.
func walk(n int, leaf func(i int) (v, done bool), parent func(i int) (int, bool), edge func(i, p int) bool) []bool {
	const (
		unknown = iota
		walking
		known
	)
	state := make([]uint8, n)
	value := make([]bool, n)
	var path []int
	for i := range n {
		path = path[:0]
		cur, v := i, false
		for {
			if state[cur] == known {
				v = value[cur]
				break
			}
			if state[cur] == walking {
				v = false // a cycle reaches no root
				break
			}
			state[cur] = walking
			if lv, done := leaf(cur); done {
				value[cur], state[cur], v = lv, known, lv
				break
			}
			path = append(path, cur)
			cur, _ = parent(cur)
		}
		for j := len(path) - 1; j >= 0; j-- {
			k := path[j]
			p, _ := parent(k)
			v = v && edge(k, p)
			value[k], state[k] = v, known
		}
	}
	return value
}

func cloneRevocation(v Revocation) Revocation {
	v.Key = bytes.Clone(v.Key)
	v.Signature = bytes.Clone(v.Signature)
	keep := make([][]byte, len(v.Keep))
	for i, k := range v.Keep {
		keep[i] = bytes.Clone(k)
	}
	v.Keep = keep
	return v
}

// Person is whose directory it is.
func (d *Directory) Person() string { return d.person }

// Devices returns the person's devices that stand, by id.
func (d *Directory) Devices() []DeviceRecord {
	var out []DeviceRecord
	for _, id := range sortedIDs(d.records) {
		if r := d.records[id]; r.Kind == RecordDevice {
			out = append(out, r)
		}
	}
	return out
}

// Roots returns the person's roots whose vouch verifies, revoked or not,
// by id. A root the app has not pinned for the person is a changed
// security code.
func (d *Directory) Roots() []DeviceRecord {
	var out []DeviceRecord
	for _, id := range sortedIDs(d.verified) {
		if r := d.verified[id]; r.Root() {
			out = append(out, r)
		}
	}
	return out
}

// Recovery returns the person's recovery record that stands, if there is
// one.
func (d *Directory) Recovery() (DeviceRecord, bool) {
	for _, id := range sortedIDs(d.records) {
		if r := d.records[id]; r.Kind == RecordRecovery {
			return r, true
		}
	}
	return DeviceRecord{}, false
}

// Device returns the device with the id, if it stands.
func (d *Directory) Device(id string) (DeviceRecord, bool) {
	r, ok := d.records[id]
	if !ok || r.Kind != RecordDevice {
		return DeviceRecord{}, false
	}
	return r, true
}

// Revoked reports whether a revocation that counts revokes the key.
func (d *Directory) Revoked(signatureKey []byte) bool { return d.revoked[string(signatureKey)] }

// Revocations returns the revocations that count, for the app to keep
// and pass to NewDirectory again.
func (d *Directory) Revocations() []Revocation {
	out := make([]Revocation, len(d.revocations))
	for i, v := range d.revocations {
		out[i] = cloneRevocation(v)
	}
	return out
}

// CheckMember checks that a leaf is a device of the person: its
// credential names a device of the directory's person that stands, whose
// signature key is the leaf's.
func (d *Directory) CheckMember(m mls.Member) error {
	id, err := ParseCredential(m.Credential)
	if err != nil {
		return err
	}
	if id.Latere() || id.Person != d.person {
		return fmt.Errorf("messaging: leaf %s is not a device of %s", id, d.person)
	}
	r, ok := d.Device(id.Device)
	if !ok {
		return fmt.Errorf("messaging: %s is not a device of %s", id.Device, d.person)
	}
	if !bytes.Equal(r.SignatureKey, m.SignatureKey) {
		return fmt.Errorf("messaging: leaf of %s has a key its directory record does not", id)
	}
	return nil
}

func sortedIDs(m map[string]DeviceRecord) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// SecurityCode is the code two people compare in person to confirm each
// other's roots: 60 digits in twelve groups of five, the same on both
// sides whichever of the two computes it.
func SecurityCode(a, b *Directory) string {
	fp := func(d *Directory) []byte {
		h := sha256.New()
		h.Write([]byte(d.person))
		for _, r := range d.Roots() {
			h.Write(r.Fingerprint())
		}
		return h.Sum(nil)
	}
	x, y := fp(a), fp(b)
	if bytes.Compare(x, y) > 0 {
		x, y = y, x
	}
	sum := sha256.Sum256(append(append([]byte(vouchLabel+" security code\x00"), x...), y...))
	// Each five bytes give ten digits, two groups, as Signal's safety
	// numbers take them: 30 bytes give the 60 digits.
	var groups []string
	for i := range 6 {
		var v uint64
		for _, c := range sum[i*5 : i*5+5] {
			v = v<<8 | uint64(c)
		}
		v %= 10_000_000_000
		groups = append(groups, fmt.Sprintf("%05d", v/100_000), fmt.Sprintf("%05d", v%100_000))
	}
	return strings.Join(groups, " ")
}
