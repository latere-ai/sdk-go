// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Every decoder on hostile input, for panics and for encodings that
// parse but are not in their one form.

package messaging

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/sha256"
	"testing"
	"time"

	"latere.ai/x/sdk-go/attachment"
	"latere.ai/x/sdk-go/mls"
)

func FuzzParseState(f *testing.F) {
	ext, _ := State{Kind: KindGroup, Level: LevelReadsAlong, People: []string{"a", "b", "c"}, Owners: []string{"a", "c"}}.Extension()
	f.Add(ext.Data)
	f.Fuzz(func(t *testing.T, data []byte) {
		s, err := ParseState([]mls.Extension{{Type: StateExtension, Data: data}})
		if err != nil {
			return
		}
		again, err := s.Extension()
		if err != nil || !bytes.Equal(again.Data, data) {
			t.Fatalf("state parsed but does not re-encode to its bytes: %v", err)
		}
	})
}

func FuzzParseEntry(f *testing.F) {
	f.Add(Entry{Signed: []byte("x"), Sender: Identity{Person: "a", Device: "b"}, SignatureKey: make([]byte, 32)}.Bytes())
	f.Add(Entry{Signed: []byte("x"), Sender: Identity{Conversation: "c"}, SignatureKey: make([]byte, 32), Attestation: []byte("a")}.Bytes())
	f.Fuzz(func(t *testing.T, b []byte) {
		e, err := ParseEntry(b)
		if err != nil {
			return
		}
		if !bytes.Equal(e.Bytes(), b) {
			t.Fatal("entry parsed but does not re-encode to its bytes")
		}
		_, _ = VerifyEntry(e, []byte("g"), func(Identity, []byte, []byte) error { return nil })
	})
}

func FuzzParseCommitNotice(f *testing.F) {
	f.Add(noticeWith(make([]byte, sha256.Size), []byte("m"), map[string][]byte{"k": []byte("s")}))
	f.Fuzz(func(t *testing.T, ad []byte) {
		n, err := ParseCommitNotice(ad)
		if err != nil {
			return
		}
		_ = n.Covers([]byte("k"))
	})
}

func FuzzParsePayload(f *testing.F) {
	b, _ := EncodePayload(Payload{ID: "m", SentAt: time.UnixMilli(1), Type: PayloadText, Text: "hi"})
	f.Add(b)
	b2, _ := EncodePayload(Payload{ID: "m", SentAt: time.UnixMilli(1), Type: PayloadText, Attachments: []Attachment{{
		Attachment: attachment.Attachment{Name: "f", Size: 3, Blob: attachment.Digest([]byte("abc")), Ref: Folder + "/x",
			Preview: &attachment.Preview{Data: []byte{1}, Waveform: []byte{2}}},
		Owner: "alice", Key: bytes.Repeat([]byte{1}, StreamKeySize)}}})
	f.Add(b2)
	f.Fuzz(func(t *testing.T, b []byte) {
		p, err := ParsePayload(b)
		if err != nil {
			return
		}
		if _, err := EncodePayload(p); err != nil {
			t.Fatalf("payload parsed but does not encode: %v", err)
		}
	})
}

func FuzzParseRecoveryKey(f *testing.F) {
	k, _ := NewRecoveryKey()
	f.Add(k.String())
	f.Fuzz(func(t *testing.T, s string) {
		k, err := ParseRecoveryKey(s)
		if err != nil {
			return
		}
		back, err := ParseRecoveryKey(k.String())
		if err != nil || back.key != k.key {
			t.Fatal("recovery key does not round-trip")
		}
	})
}

func FuzzParseCredential(f *testing.F) {
	f.Add([]byte("user:a/device:b"))
	f.Add([]byte("latere/conversation:c"))
	f.Fuzz(func(t *testing.T, b []byte) {
		id, err := ParseCredential(mls.BasicCredential(b))
		if err != nil {
			return
		}
		if id.String() != string(b) || id.Validate() != nil {
			t.Fatal("credential parsed but is not in its one form")
		}
	})
}

func FuzzOpenBundle(f *testing.F) {
	sb, err := SealBundle([]Entry{{Signed: []byte("x"), Sender: Identity{Person: "a", Device: "b"}, SignatureKey: make([]byte, 32)}})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(sb.Ciphertext)
	f.Fuzz(func(t *testing.T, ct []byte) {
		sum := sha256.Sum256(ct)
		_, _ = OpenBundle(ct, sb.Key, sum[:])
	})
}

func FuzzNewDirectory(f *testing.F) {
	_, priv, _ := ed25519.GenerateKey(nil)
	_, priv2, _ := ed25519.GenerateKey(nil)
	enc, _ := ecdh.X25519().GenerateKey(nil)
	pub, pub2 := priv.Public().(ed25519.PublicKey), priv2.Public().(ed25519.PublicKey)
	f.Add("p", "d", uint8(1), []byte(pub), enc.PublicKey().Bytes(), "d", "q", "e", uint8(2), "d", "q", true)
	f.Fuzz(func(t *testing.T, person, id string, kind uint8, sig, encKey []byte, voucher, id2, voucher2 string, kind2 uint8, voucher3, by string, keep bool) {
		r1 := DeviceRecord{Person: person, ID: id, Kind: RecordKind(kind), SignatureKey: sig, EncryptionKey: encKey, Voucher: voucher, VoucherKey: pub}
		sign := func(r *DeviceRecord, k ed25519.PrivateKey) {
			if r.validate() == nil { // body is reached only after validate
				r.Vouch = ed25519.Sign(priv, r.body(vouchLabel))
				r.Possession = ed25519.Sign(k, r.body(possessionLabel))
			}
		}
		sign(&r1, priv)
		r2 := DeviceRecord{Person: person, ID: id2, Kind: RecordKind(kind2), SignatureKey: pub2, Voucher: voucher2, VoucherKey: pub}
		if r2.Kind == RecordDevice {
			r2.EncryptionKey = enc.PublicKey().Bytes()
		}
		sign(&r2, priv2)
		r3 := DeviceRecord{Person: person, ID: voucher3, Kind: RecordDevice, SignatureKey: pub, EncryptionKey: enc.PublicKey().Bytes(), Voucher: id, VoucherKey: sig}
		sign(&r3, priv)
		v := Revocation{Person: person, Key: pub}
		if keep {
			v.Keep = [][]byte{pub2}
		}
		if err := v.Sign(by, priv2); err != nil {
			v.By = by
		}
		d, dropped := NewDirectory(person, []DeviceRecord{r1, r2, r3}, []Revocation{v}, []Revocation{v})
		if dropped < 0 || dropped > 3 {
			t.Fatalf("dropped %d", dropped)
		}
		for _, r := range d.Devices() {
			if r.Person != person || r.Kind != RecordDevice || d.Revoked(r.SignatureKey) {
				t.Fatalf("listed %+v", r)
			}
		}
		_ = d.Roots()
		_, _ = d.Recovery()
		_ = SecurityCode(d, d)
	})
}

func FuzzOpenBackupKey(f *testing.F) {
	_, priv, _ := ed25519.GenerateKey(nil)
	enc, _ := ecdh.X25519().GenerateKey(nil)
	_, priv2, _ := ed25519.GenerateKey(nil)
	approver, _ := NewDeviceRecord("p", "a", priv, enc.PublicKey(), nil)
	dev, _ := NewDeviceRecord("p", "b", priv2, enc.PublicKey(), &approver)
	_ = dev.Sign(priv)
	d, _ := NewDirectory("p", []DeviceRecord{approver, dev}, nil, nil)
	bk, _ := NewBackupKey()
	if sealed, err := bk.SealTo(dev, priv); err == nil {
		f.Add(sealed)
	}
	f.Fuzz(func(t *testing.T, transfer []byte) {
		_, _ = OpenBackupKey(enc, d, dev, transfer)
	})
}

func FuzzHistoryRange(f *testing.F) {
	f.Add(encode(HistoryRange{Kind: RangeSinceTime, Since: time.UnixMilli(5)}.marshal))
	f.Fuzz(func(t *testing.T, b []byte) {
		r := &reader{b: b}
		var h HistoryRange
		h.unmarshal(r)
		if r.done() != nil || h.validate() != nil {
			return
		}
		if !bytes.Equal(encode(h.marshal), b) {
			t.Fatalf("range %+v parsed from %x re-encodes to %x", h, b, encode(h.marshal))
		}
	})
}

func FuzzOpenStream(f *testing.F) {
	key := bytes.Repeat([]byte{1}, StreamKeySize)
	for _, n := range []int{0, 5, ChunkBytes, ChunkBytes + 3} {
		s, err := sealBytes(key, attachmentInfo, make([]byte, n), MaxAttachmentBytes)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, sealed []byte) {
		plain, err := openBytes(key, attachmentInfo, sealed, MaxAttachmentBytes)
		if err != nil {
			return
		}
		again, err := sealBytes(key, attachmentInfo, plain, MaxAttachmentBytes)
		if err != nil || !bytes.Equal(again, sealed) {
			t.Fatal("a stream opened that the sealer would not write")
		}
	})
}

func FuzzOpenAttachmentChunk(f *testing.F) {
	a := &Attachment{Attachment: attachment.Attachment{Name: "f", Size: 3, Blob: attachment.Digest([]byte("abc"))}, Key: bytes.Repeat([]byte{1}, StreamKeySize)}
	s, _ := sealBytes(a.Key, attachmentInfo, []byte("abc"), MaxAttachmentBytes)
	f.Add(int64(0), int64(3), s[1:])
	f.Fuzz(func(t *testing.T, i, size int64, sealed []byte) {
		b := *a
		b.Size = size
		_, _ = OpenAttachmentChunk(&b, i, sealed)
	})
}
