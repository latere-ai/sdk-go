// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package messaging

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// fixedRecoveryKey is the key of the pinned vectors: the bytes 0 to 15.
func fixedRecoveryKey() *RecoveryKey {
	var k RecoveryKey
	for i := range k.key {
		k.key[i] = byte(i)
	}
	return &k
}

func TestRecoveryKeyVectors(t *testing.T) {
	k := fixedRecoveryKey()
	if got := k.String(); got != pinnedRecoveryText {
		t.Fatalf("text %s, want %s", got, pinnedRecoveryText)
	}
	sig, err := k.SigningKey()
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(sig.Seed()); got != pinnedRecoverySigningSeed {
		t.Fatalf("signing seed %s", got)
	}
	w, err := k.wrappingKey()
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(w); got != pinnedRecoveryWrap {
		t.Fatalf("wrapping key %s", got)
	}
}

// The pinned vectors of fixedRecoveryKey, which a change to the format or
// a derivation breaks. They were computed apart from this package, from
// the format's description, with Python's hashlib and hmac.
const (
	pinnedRecoveryText        = "0004-1061-050R-3GG2-8A1C-60T3-GF3G"
	pinnedRecoverySigningSeed = "bb2d9a90d6762d9cd02e7b08b71e48af96977e11ecfbcc29361c25c528f71a6a"
	pinnedRecoveryWrap        = "549a71f7ce266336c756e0ca740d92e49086760712bc25f24230cb10722dedf7"
)

func TestRecoveryKeyText(t *testing.T) {
	for range 50 {
		k, err := NewRecoveryKey()
		if err != nil {
			t.Fatal(err)
		}
		s := k.String()
		if !regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{4}(-[0-9A-HJKMNP-TV-Z]{4}){6}$`).MatchString(s) {
			t.Fatalf("text %q", s)
		}
		for _, typed := range []string{s, strings.ToLower(s), strings.ReplaceAll(s, "-", " "), strings.ReplaceAll(s, "-", "")} {
			got, err := ParseRecoveryKey(typed)
			if err != nil || got.key != k.key {
				t.Fatalf("%q: %v", typed, err)
			}
		}
		for i := range RecoveryKeyGroups {
			g, err := k.Group(i)
			if err != nil {
				t.Fatal(err)
			}
			if !k.CheckGroup(i, strings.ToLower(g)) || (g != "0000" && k.CheckGroup(i, "0000")) {
				t.Fatalf("group %d", i)
			}
		}
	}
	k := fixedRecoveryKey()
	if _, err := k.Group(RecoveryKeyGroups); err == nil {
		t.Fatal("group past the end")
	}
	// Look-alike letters read as the digits.
	s := strings.ReplaceAll(strings.ReplaceAll(k.String(), "0", "O"), "1", "l")
	if got, err := ParseRecoveryKey(s); err != nil || got.key != k.key {
		t.Fatalf("look-alikes: %v", err)
	}
}

func TestRecoveryKeyCheck(t *testing.T) {
	k := fixedRecoveryKey()
	s := strings.ReplaceAll(k.String(), "-", "")
	accepted := 0
	for i := range len(s) {
		for _, c := range recoveryAlphabet {
			if byte(c) == s[i] {
				continue
			}
			typo := s[:i] + string(c) + s[i+1:]
			_, err := ParseRecoveryKey(typo)
			if err == nil {
				accepted++
			} else if !errors.Is(err, ErrRecoveryKey) {
				t.Fatal(err)
			}
		}
	}
	// A 10-bit check lets about one typo in 1024 through.
	if accepted > 5 {
		t.Fatalf("%d of %d one-character typos accepted", accepted, len(s)*31)
	}
	for _, bad := range []string{"", "ABCD", s + "0", s[:27], strings.Replace(s, s[:1], "U", 1)} {
		if _, err := ParseRecoveryKey(bad); !errors.Is(err, ErrRecoveryKey) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestBackupKeyWrap(t *testing.T) {
	rk, err := NewRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	bk, err := NewBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := bk.Wrap(rk, "aiko")
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnwrapBackupKey(rk, "aiko", wrapped)
	if err != nil || got.key != bk.key {
		t.Fatalf("unwrap: %v", err)
	}
	other, err := NewRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UnwrapBackupKey(other, "aiko", wrapped); !errors.Is(err, ErrSealed) {
		t.Fatalf("another key: %v", err)
	}
	if _, err := UnwrapBackupKey(rk, "bob", wrapped); !errors.Is(err, ErrSealed) {
		t.Fatalf("another person: %v", err)
	}
	wrapped[len(wrapped)-1] ^= 1
	if _, err := UnwrapBackupKey(rk, "aiko", wrapped); !errors.Is(err, ErrSealed) {
		t.Fatalf("changed bytes: %v", err)
	}
}

func TestBackupRecords(t *testing.T) {
	bk, err := NewBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	same, err := ParseBackupKey(bk.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	id1, err := bk.RecordID([]byte("message one"))
	if err != nil {
		t.Fatal(err)
	}
	id2, err := same.RecordID([]byte("message one"))
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	id3, err := other.RecordID([]byte("message one"))
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 || id1 == id3 {
		t.Fatal("record ids are not one per message and key")
	}
	sealed, err := bk.SealRecord(id1, []byte("content"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := same.OpenRecord(id1, sealed); err != nil || string(got) != "content" {
		t.Fatalf("open: %v", err)
	}
	if _, err := bk.OpenRecord(id3, sealed); !errors.Is(err, ErrSealed) {
		t.Fatalf("another id: %v", err)
	}
	idx, err := bk.SealIndex([]byte("index"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := bk.OpenIndex(idx); err != nil || string(got) != "index" {
		t.Fatalf("index: %v", err)
	}
	if _, err := bk.OpenRecord(id1, idx); !errors.Is(err, ErrSealed) {
		t.Fatal("the index opened as a record")
	}
	if _, err := ParseBackupKey([]byte("short")); err == nil {
		t.Fatal("a short backup key parsed")
	}
}

func TestBackupKeyTransfer(t *testing.T) {
	bk, err := NewBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	phone, laptop, k, svc := newKeys(t), newKeys(t), newKeys(t), newKeys(t)
	approver := record(t, "aiko", "phone", phone, nil, nil)
	lap := record(t, "aiko", "laptop", laptop, &approver, phone.sig)
	rec := record(t, "aiko", "tablet", k, &approver, phone.sig)
	recs := []DeviceRecord{approver, lap, rec}
	dir, _ := NewDirectory("aiko", recs, nil, nil)
	sealed, err := bk.SealTo(rec, phone.sig)
	if err != nil {
		t.Fatal(err)
	}
	got, err := OpenBackupKey(k.enc, dir, rec, sealed)
	if err != nil || !bytes.Equal(got.Bytes(), bk.Bytes()) {
		t.Fatalf("open: %v", err)
	}
	if _, err := OpenBackupKey(k.enc, dir, lap, sealed); err == nil {
		t.Fatal("another device opened the transfer")
	}
	stranger, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenBackupKey(stranger, dir, rec, sealed); err == nil {
		t.Fatal("another key opened the transfer")
	}
	for name, seal := range map[string]func() ([]byte, error){
		"a recovery record": func() ([]byte, error) { return bk.SealTo(DeviceRecord{Kind: RecordRecovery}, phone.sig) },
		"a root":            func() ([]byte, error) { return bk.SealTo(approver, phone.sig) },
		"another device":    func() ([]byte, error) { return bk.SealTo(rec, laptop.sig) },
	} {
		if _, err := seal(); err == nil {
			t.Errorf("sealed to %s, or by a device that did not approve it", name)
		}
	}

	// as is a transfer signed by another record, as if it had approved
	// the tablet.
	as := func(by DeviceRecord, key ed25519.PrivateKey, chosen *BackupKey) []byte {
		t.Helper()
		moved := rec
		moved.Voucher, moved.VoucherKey = by.ID, by.SignatureKey
		b, err := chosen.SealTo(moved, key)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	chosen, err := NewBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	// Another device of the person's, or a root the service lists, seals
	// a key of its own choosing: neither approved the tablet, so it is
	// refused.
	svcRoot := record(t, "aiko", "svc", svc, nil, nil)
	withSvc, _ := NewDirectory("aiko", append(slices.Clone(recs), svcRoot), nil, nil)
	for name, transfer := range map[string][]byte{"another device": as(lap, laptop.sig, chosen), "the service's root": as(svcRoot, svc.sig, chosen)} {
		if _, err := OpenBackupKey(k.enc, withSvc, rec, transfer); err == nil {
			t.Errorf("the tablet took a backup key from %s", name)
		}
	}
	// A key signed by no one is refused.
	_, forger, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	forgedKey := rec
	forgedKey.VoucherKey = forger.Public().(ed25519.PublicKey)
	forged, err := chosen.SealTo(forgedKey, forger)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenBackupKey(k.enc, dir, rec, forged); err == nil {
		t.Fatal("the device took a backup key no device of its person signed")
	}
	// The device's record must stand as the device holds it.
	gone, _ := NewDirectory("aiko", []DeviceRecord{approver, lap}, nil, nil)
	if _, err := OpenBackupKey(k.enc, gone, rec, sealed); err == nil {
		t.Fatal("a device that does not stand took the backup key")
	}
	// A transfer from a device the person revoked is refused.
	lost := revocation(t, "aiko", approver.SignatureKey, "laptop", laptop.sig, lap.SignatureKey, rec.SignatureKey)
	revoked, _ := NewDirectory("aiko", recs, []Revocation{lost}, nil)
	if _, err := OpenBackupKey(k.enc, revoked, rec, sealed); err == nil {
		t.Fatal("a revoked device's transfer was taken")
	}
}
