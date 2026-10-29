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
// another of the same person and signed by its own key. A device record
// holds a device's signature key, the one its leaves carry in every
// group, and the X25519 key the person's other devices seal to it with.
// A recovery record holds the public half of the signing key the
// person's recovery key derives. The first device vouches for itself,
// which makes it a root, and for the recovery record; every later device
// is vouched for by an approved device or by the recovery key. A record
// names its voucher's id and key under its own key's signature as well
// as the voucher's, so where a record hangs is its own word and the
// voucher's together, and no one who holds neither key moves it under
// another root.
//
// A key the person no longer holds is revoked by a Revocation, signed
// by a record under the same root as the key, which the service stores
// and lists but cannot make or undo. A revoked key is no record's key
// and vouches for nothing, and a record it vouched for stays only when
// every revocation of the key names it in Keep: a device of the person's
// that revokes a stolen device names the devices that stay, and a
// revocation the thief signs that names others makes both lose them, so
// a dispute fails closed. A revocation counts whether or not its signer
// is revoked itself, so a stolen key can revoke the person's other
// devices under its root too; the person then comes back through the
// recovery key or starts over from a new root. The apps keep every
// revocation that counted and pass it back to NewDirectory as known, so
// it applies whatever the service lists later; the service can withhold
// a revocation only from a device that never saw it.

// MaxRecords bounds the records NewDirectory takes for one person; past
// it nothing stands, so no record is dropped for where the service lists
// it.
const MaxRecords = 256

// MaxRevocations bounds the revocations that count for one person: one
// for each key and one for each record a later revocation stops keeping.
// Past it nothing stands.
const MaxRevocations = 2 * MaxRecords

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
	// Voucher is the id of the record whose key signed Vouch, and
	// VoucherKey that record's signature key: the record's own for a
	// root.
	Voucher    string `json:"voucher"`
	VoucherKey []byte `json:"voucher_key"`
	Vouch      []byte `json:"vouch"`
	// Possession is the record's own key's signature of the record,
	// voucher included, made when the device made its keys.
	Possession []byte `json:"possession"`
}

// The labels that separate a vouch, a proof of possession and a
// revocation from every other use of a device's key.
const (
	vouchLabel      = "latere messaging vouch v1"
	possessionLabel = "latere messaging possession v1"
	revocationLabel = "latere messaging revocation v1"
)

// body is the record as its possession and its vouch sign it, under
// one of the two labels.
func (r *DeviceRecord) body(label string) []byte {
	return encode(func(w *writer) {
		w.vec8([]byte(label))
		w.vec8([]byte(r.Person))
		w.vec8([]byte(r.ID))
		w.u8(uint8(r.Kind))
		w.vec8(r.SignatureKey)
		w.vec8(r.EncryptionKey)
		w.vec8([]byte(r.Voucher))
		w.vec8(r.VoucherKey)
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
	if len(r.SignatureKey) != ed25519.PublicKeySize || len(r.VoucherKey) != ed25519.PublicKeySize {
		return errors.New("messaging: a record's signature key and its voucher's are Ed25519 keys")
	}
	if r.Root() != bytes.Equal(r.VoucherKey, r.SignatureKey) {
		return errors.New("messaging: a root, and only a root, is vouched for by its own key")
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

// verify reports whether the record is well formed and both its
// signatures verify: its own key's and the key it names as its
// voucher's.
func (r *DeviceRecord) verify() bool {
	return r.validate() == nil && ed25519.Verify(r.SignatureKey, r.body(possessionLabel), r.Possession) &&
		ed25519.Verify(r.VoucherKey, r.body(vouchLabel), r.Vouch)
}

// Sign vouches for the record with the private key of the record it
// names as its voucher: the device that approves it, or the recovery
// key that recovers it.
func (r *DeviceRecord) Sign(key ed25519.PrivateKey) error {
	if len(key) != ed25519.PrivateKeySize || !bytes.Equal(key.Public().(ed25519.PublicKey), r.VoucherKey) {
		return errors.New("messaging: the key is not the one the record names as its voucher's")
	}
	if err := r.validate(); err != nil {
		return err
	}
	r.Vouch = ed25519.Sign(key, r.body(vouchLabel))
	return nil
}

// NewDeviceRecord returns the record of a device with its signature key
// and encryption key, naming the record that vouches for it, with the
// device's proof that it holds the key. The voucher then signs it with
// Sign. A nil voucher makes a root, the person's first device, which
// vouches for itself here.
func NewDeviceRecord(person, device string, key ed25519.PrivateKey, encryptionKey *ecdh.PublicKey, voucher *DeviceRecord) (DeviceRecord, error) {
	return newRecord(DeviceRecord{Person: person, ID: device, Kind: RecordDevice, EncryptionKey: encryptionKey.Bytes()}, key, voucher)
}

// NewRecoveryRecord returns the record of a person's recovery key,
// naming the device that made the key, which then vouches for it with
// Sign.
func NewRecoveryRecord(person, id string, recovery *RecoveryKey, voucher DeviceRecord) (DeviceRecord, error) {
	if voucher.Kind != RecordDevice {
		return DeviceRecord{}, errors.New("messaging: a device vouches for the recovery record")
	}
	key, err := recovery.SigningKey()
	if err != nil {
		return DeviceRecord{}, err
	}
	return newRecord(DeviceRecord{Person: person, ID: id, Kind: RecordRecovery}, key, &voucher)
}

func newRecord(r DeviceRecord, key ed25519.PrivateKey, voucher *DeviceRecord) (DeviceRecord, error) {
	if len(key) != ed25519.PrivateKeySize {
		return DeviceRecord{}, errors.New("messaging: a record's key is an Ed25519 key")
	}
	r.SignatureKey = bytes.Clone(key.Public().(ed25519.PublicKey))
	if voucher == nil {
		r.Voucher, r.VoucherKey = r.ID, bytes.Clone(r.SignatureKey)
	} else {
		if voucher.Person != r.Person {
			return DeviceRecord{}, errors.New("messaging: a record is vouched for by a record of the same person")
		}
		r.Voucher, r.VoucherKey = voucher.ID, bytes.Clone(voucher.SignatureKey)
	}
	if err := r.validate(); err != nil {
		return DeviceRecord{}, err
	}
	r.Possession = ed25519.Sign(key, r.body(possessionLabel))
	if voucher == nil {
		r.Vouch = ed25519.Sign(key, r.body(vouchLabel))
	}
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
	// stay the person's; what those vouched for stays with them. A
	// record that revokes its own key keeps nothing.
	Keep [][]byte `json:"keep,omitempty"`
	// By is the id of the record whose key signed it, under the same
	// root as Key.
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
	// verified are the records whose chain verifies, revoked or not,
	// with the id of the root each chain reaches, and their ids by key.
	verified map[string]DeviceRecord
	roots    map[string]string
	byKey    map[string]string
	// revoked are the keys revoked, and keep what each keeps: what
	// every revocation of it names.
	revoked     map[string]bool
	keep        map[string]map[string]bool
	revocations []Revocation
}

// NewDirectory verifies a person's records against their revocations and
// keeps the records that stand. known are the revocations the app kept
// from an earlier directory of the person, Revocations, which apply
// whatever is listed now; revocations are the service's, which count
// when they verify. A record that fails, names another person or is
// revoked is left out and counted in dropped, so a record the service
// made up costs nothing but itself. Two records under one id, or with
// one key, are both left out. Past MaxRecords records, or MaxRevocations
// revocations that may count, nothing stands.
func NewDirectory(person string, records []DeviceRecord, revocations, known []Revocation) (d *Directory, dropped int) {
	d = &Directory{person: person, records: make(map[string]DeviceRecord), verified: make(map[string]DeviceRecord),
		roots: make(map[string]string), byKey: make(map[string]string),
		revoked: make(map[string]bool), keep: make(map[string]map[string]bool)}
	seen := make(map[string]bool)
	var facts []Revocation
	for _, v := range known {
		if v.Person == person && v.validate() == nil && !seen[string(v.Signature)] {
			seen[string(v.Signature)] = true
			facts = append(facts, v)
		}
	}
	var recs []DeviceRecord
	for _, r := range records {
		if r.Person == person {
			recs = append(recs, r)
		}
	}
	d.revocations = cloneRevocations(facts)
	if len(recs) > MaxRecords {
		d.applyAll(facts)
		return d, len(records)
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
		ok[i] = ids[r.ID] == 1 && keys[string(r.SignatureKey)] == 1 && r.verify()
	}
	parent := func(i int) int { return byID[recs[i].Voucher] }

	// The root each chain reaches, with no revocation applied: a record
	// hangs from the listed record it names as its voucher, by id and
	// key.
	root := walk(n, func(i int) (int, bool) {
		r := recs[i]
		switch {
		case !ok[i]:
			return -1, true
		case r.Root():
			if r.Kind != RecordDevice {
				return -1, true
			}
			return i, true
		}
		p, found := byID[r.Voucher]
		if !found || !bytes.Equal(recs[p].SignatureKey, r.VoucherKey) {
			return -1, true
		}
		return 0, false
	}, parent, -1)
	for i, r := range recs {
		if root[i] >= 0 {
			d.verified[r.ID] = r
			d.roots[r.ID] = recs[root[i]].ID
			d.byKey[string(r.SignatureKey)] = r.ID
		}
	}

	// The revocations that may count: of a key a verified record holds,
	// by a verified record under the same root, each once.
	var counting []Revocation
	for _, v := range revocations {
		if v.Person != person || seen[string(v.Signature)] {
			continue
		}
		k, found := d.byKey[string(v.Key)]
		if !found || d.roots[k] != d.roots[v.By] || v.validate() != nil {
			continue
		}
		seen[string(v.Signature)] = true
		counting = append(counting, v)
	}
	if len(counting) > MaxRevocations {
		clear(d.verified)
		clear(d.roots)
		clear(d.byKey)
		d.applyAll(facts)
		return d, len(records)
	}
	for _, v := range counting {
		if ed25519.Verify(d.verified[v.By].SignatureKey, v.message(), v.Signature) {
			d.apply(v)
			d.revocations = append(d.revocations, cloneRevocation(v))
		}
	}
	d.applyAll(facts)

	// The records that stand: not revoked, through vouchers that stand,
	// or through a revoked voucher whose revocations all keep them.
	stands := walk(n, func(i int) (bool, bool) {
		r := recs[i]
		switch {
		case root[i] < 0 || d.revoked[string(r.SignatureKey)]:
			return false, true
		case r.Root():
			return true, true
		}
		if d.revoked[string(r.VoucherKey)] {
			return d.keep[string(r.VoucherKey)][string(r.SignatureKey)], true
		}
		return false, false
	}, parent, false)
	for i, r := range recs {
		if stands[i] {
			d.records[r.ID] = r
		}
	}
	return d, len(records) - len(d.records)
}

// apply records a revocation that counts: its key is revoked, and keeps
// only what this revocation and every earlier one of it name.
func (d *Directory) apply(v Revocation) {
	names := make(map[string]bool, len(v.Keep))
	if d.byKey[string(v.Key)] != v.By { // a record that revokes its own key keeps nothing
		for _, k := range v.Keep {
			names[string(k)] = true
		}
	}
	if prev, seen := d.keep[string(v.Key)]; seen {
		for k := range prev {
			if !names[k] {
				delete(prev, k)
			}
		}
	} else {
		d.keep[string(v.Key)] = names
	}
	d.revoked[string(v.Key)] = true
}

func (d *Directory) applyAll(vs []Revocation) {
	for _, v := range vs {
		d.apply(v)
	}
}

// CheckRevocation checks a revocation before the service keeps it: that
// it would count, as NewDirectory counts one, and whether it changes
// anything, revoking a key not yet revoked or keeping less of what the
// key vouched for. One that changes nothing need not be kept, which
// bounds what the service keeps by MaxRevocations.
func (d *Directory) CheckRevocation(v Revocation) (changes bool, err error) {
	if v.Person != d.person {
		return false, fmt.Errorf("messaging: a revocation of %s is not one of %s", v.Person, d.person)
	}
	if err := v.validate(); err != nil {
		return false, err
	}
	k, found := d.byKey[string(v.Key)]
	if !found {
		return false, errors.New("messaging: the revocation is of no key of the person's")
	}
	by, found := d.verified[v.By]
	if !found || d.roots[by.ID] != d.roots[k] {
		return false, errors.New("messaging: the revocation is signed by no record under the root of the key it revokes")
	}
	if !ed25519.Verify(by.SignatureKey, v.message(), v.Signature) {
		return false, errors.New("messaging: the revocation's signature does not verify")
	}
	if !d.revoked[string(v.Key)] {
		return true, nil
	}
	for _, r := range d.verified {
		if bytes.Equal(r.VoucherKey, v.Key) && r.ID != k && d.keep[string(v.Key)][string(r.SignatureKey)] &&
			(k == v.By || !slices.ContainsFunc(v.Keep, func(key []byte) bool { return bytes.Equal(key, r.SignatureKey) })) {
			return true, nil
		}
	}
	return false, nil
}

// walk decides a value for each of n records that hang from one another
// by their vouchers, without recursion, so a long chain costs no stack.
// leaf decides a record on its own when it can (done); otherwise the
// record takes its voucher's value. A cycle decides cycle.
func walk[T any](n int, leaf func(i int) (v T, done bool), parent func(i int) int, cycle T) []T {
	const (
		unknown = iota
		walking
		known
	)
	state := make([]uint8, n)
	value := make([]T, n)
	var path []int
	for i := range n {
		path = path[:0]
		cur := i
		var v T
		for {
			if state[cur] == known {
				v = value[cur]
				break
			}
			if state[cur] == walking {
				v = cycle
				break
			}
			state[cur] = walking
			if lv, done := leaf(cur); done {
				value[cur], state[cur], v = lv, known, lv
				break
			}
			path = append(path, cur)
			cur = parent(cur)
		}
		for _, k := range path {
			value[k], state[k] = v, known
		}
	}
	return value
}

func cloneRevocations(vs []Revocation) []Revocation {
	out := make([]Revocation, len(vs))
	for i, v := range vs {
		out[i] = cloneRevocation(v)
	}
	return out
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

// Revocations returns the revocations that counted, those known among
// them, for the app to keep and pass to NewDirectory again as known.
func (d *Directory) Revocations() []Revocation { return cloneRevocations(d.revocations) }

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
