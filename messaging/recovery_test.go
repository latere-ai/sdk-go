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
	phone, k := newKeys(t), newKeys(t)
	approver := record(t, "aiko", "phone", phone, "phone", phone.sig)
	rec := record(t, "aiko", "tablet", k, "phone", phone.sig)
	dir, _ := NewDirectory("aiko", []DeviceRecord{approver, rec}, nil)
	sealed, err := bk.SealTo(rec, "phone", phone.sig)
	if err != nil {
		t.Fatal(err)
	}
	got, err := OpenBackupKey(k.enc, dir, "tablet", sealed)
	if err != nil || !bytes.Equal(got.Bytes(), bk.Bytes()) {
		t.Fatalf("open: %v", err)
	}
	if _, err := OpenBackupKey(k.enc, dir, "phone", sealed); err == nil {
		t.Fatal("another device opened the transfer")
	}
	stranger, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenBackupKey(stranger, dir, "tablet", sealed); err == nil {
		t.Fatal("another key opened the transfer")
	}
	if _, err := bk.SealTo(DeviceRecord{Kind: RecordRecovery}, "phone", phone.sig); err == nil {
		t.Fatal("sealed to a recovery record")
	}
	if _, err := bk.SealTo(rec, "tablet", k.sig); err == nil {
		t.Fatal("a device approved itself")
	}

	// Whoever reads the directory, the service first, seals a key of its
	// own choosing: no device of the person signed it, so it is refused.
	_, forger, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	chosen, err := NewBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	forged, err := chosen.SealTo(rec, "phone", forger)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenBackupKey(k.enc, dir, "tablet", forged); err == nil {
		t.Fatal("the device took a backup key no device of its person signed")
	}
	// A transfer from a device the person revoked is refused.
	gone := Revocation{Person: "aiko", Key: approver.SignatureKey, Keep: [][]byte{rec.SignatureKey}}
	if err := gone.Sign("tablet", k.sig); err != nil {
		t.Fatal(err)
	}
	revoked, _ := NewDirectory("aiko", []DeviceRecord{approver, rec}, []Revocation{gone})
	if _, err := OpenBackupKey(k.enc, revoked, "tablet", sealed); err == nil {
		t.Fatal("a revoked device's transfer was taken")
	}
}
