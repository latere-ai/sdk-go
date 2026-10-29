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
// another of the same person. A device record holds a device's
// signature key, the one its leaves carry in every group, and the X25519
// key the person's other devices seal to it with. A recovery record
// holds the public half of the signing key the person's recovery key
// derives. The first device vouches for itself, which makes it a root,
// and for the recovery record; every later device is vouched for by an
// approved device or by the recovery key. The service keeps a record of
// a removed device, marked revoked, so the vouches it made still verify;
// a revoked device is no longer one of the person's devices.

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
	// Revoked marks a device the person removed. The service sets it; it
	// is not signed.
	Revoked bool `json:"revoked,omitempty"`
}

// vouchLabel separates a vouch from every other use of a device's key.
const vouchLabel = "latere messaging vouch v1"

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
// and encryption key, not yet vouched for.
func NewDeviceRecord(person, device string, signatureKey ed25519.PublicKey, encryptionKey *ecdh.PublicKey) DeviceRecord {
	return DeviceRecord{
		Person:        person,
		ID:            device,
		Kind:          RecordDevice,
		SignatureKey:  bytes.Clone(signatureKey),
		EncryptionKey: encryptionKey.Bytes(),
	}
}

// Directory is the part of a person's records whose vouches verify, each
// through a chain of vouches to a root.
type Directory struct {
	person  string
	records map[string]DeviceRecord
}

// NewDirectory verifies a person's records and keeps those whose chain
// of vouches reaches a root. A record that fails, or names another
// person, is left out and counted in dropped, so a record the service
// made up costs nothing but itself.
func NewDirectory(person string, records []DeviceRecord) (d *Directory, dropped int) {
	d = &Directory{person: person, records: make(map[string]DeviceRecord)}
	byID := make(map[string]DeviceRecord, len(records))
	dup := make(map[string]bool)
	for _, r := range records {
		if r.Person != person {
			continue
		}
		if _, ok := byID[r.ID]; ok {
			dup[r.ID] = true
		}
		byID[r.ID] = r
	}
	// state: 0 unvisited, 1 on the current path, 2 valid, 3 invalid.
	state := make(map[string]int, len(byID))
	var visit func(id string) bool
	visit = func(id string) bool {
		switch state[id] {
		case 1:
			return false // a cycle reaches no root
		case 2:
			return true
		case 3:
			return false
		}
		r, ok := byID[id]
		if !ok || dup[id] || r.Person != person || r.validate() != nil {
			state[id] = 3
			return false
		}
		state[id] = 1
		valid := false
		if r.Root() {
			valid = r.Kind == RecordDevice && ed25519.Verify(r.SignatureKey, r.vouchMessage(), r.Vouch)
		} else if v, ok := byID[r.Voucher]; ok && visit(r.Voucher) {
			valid = ed25519.Verify(v.SignatureKey, r.vouchMessage(), r.Vouch)
		}
		if valid {
			state[id] = 2
		} else {
			state[id] = 3
		}
		return valid
	}
	for _, r := range records {
		if r.Person == person && visit(r.ID) {
			d.records[r.ID] = r
		}
	}
	return d, len(records) - len(d.records)
}

// Person is whose directory it is.
func (d *Directory) Person() string { return d.person }

// Devices returns the person's devices that are not revoked, by id.
func (d *Directory) Devices() []DeviceRecord {
	var out []DeviceRecord
	for _, id := range d.ids() {
		if r := d.records[id]; r.Kind == RecordDevice && !r.Revoked {
			out = append(out, r)
		}
	}
	return out
}

// Roots returns the person's roots, revoked or not, by id. A root the app
// has not pinned for the person is a changed security code.
func (d *Directory) Roots() []DeviceRecord {
	var out []DeviceRecord
	for _, id := range d.ids() {
		if r := d.records[id]; r.Root() {
			out = append(out, r)
		}
	}
	return out
}

// Recovery returns the person's recovery record that is not revoked, if
// there is one.
func (d *Directory) Recovery() (DeviceRecord, bool) {
	for _, id := range d.ids() {
		if r := d.records[id]; r.Kind == RecordRecovery && !r.Revoked {
			return r, true
		}
	}
	return DeviceRecord{}, false
}

// Device returns the device with the id, if it is verified and not
// revoked.
func (d *Directory) Device(id string) (DeviceRecord, bool) {
	r, ok := d.records[id]
	if !ok || r.Kind != RecordDevice || r.Revoked {
		return DeviceRecord{}, false
	}
	return r, true
}

// CheckMember checks that a leaf is a device of the person: its
// credential names a device of the directory's person, verified and not
// revoked, whose signature key is the leaf's.
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

func (d *Directory) ids() []string {
	ids := make([]string, 0, len(d.records))
	for id := range d.records {
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
