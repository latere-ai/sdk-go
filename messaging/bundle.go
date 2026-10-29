// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package messaging

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"

	"latere.ai/x/sdk-go/mls"
)

// A message leaves its group, into a history share for a new member,
// into its reader's backup or into a report, as an Entry: the message as
// its sender signed it, with the group context of its epoch, and the
// sender's credential and key. Whoever holds an entry checks it without
// the group: the signature over the content and that context, and that
// the key is the sender's device in the directory.

// Entry is one message in the form that verifies outside its group.
type Entry struct {
	// Signed is the message as mls.Message.Signed returns it: the framed
	// content, the group context of its epoch and the signature.
	Signed []byte
	// Sender is who the sender's leaf named.
	Sender Identity
	// SignatureKey is the key of the sender's leaf.
	SignatureKey []byte
	// Attestation is, for a message from Latere's member, its leaf's
	// attestation.
	Attestation []byte
}

// NewEntry is the entry of a message Process returned, from the member
// who sent it.
func NewEntry(m *mls.Message) (Entry, error) {
	if m.Kind != mls.MessageApplication || m.Sender.Kind != mls.SenderMember {
		return Entry{}, errors.New("messaging: only a member's application message is shared")
	}
	id, err := ParseCredential(m.Sender.Credential)
	if err != nil {
		return Entry{}, err
	}
	return Entry{Signed: bytes.Clone(m.Signed), Sender: id, SignatureKey: bytes.Clone(m.Sender.SignatureKey)}, nil
}

const entryVersion = 1

// Bytes encodes the entry.
func (e Entry) Bytes() []byte {
	return encode(func(w *writer) {
		w.u8(entryVersion)
		w.vec32(e.Signed)
		w.vec32([]byte(e.Sender.String()))
		w.vec32(e.SignatureKey)
		w.vec32(e.Attestation)
	})
}

// ParseEntry decodes an entry Bytes encoded.
func ParseEntry(b []byte) (Entry, error) {
	r := &reader{b: b}
	if v := r.u8(); r.err == nil && v != entryVersion {
		return Entry{}, fmt.Errorf("%w: entry version %d", errFormat, v)
	}
	e := Entry{Signed: bytes.Clone(r.vec32())}
	sender := r.vec32()
	e.SignatureKey = bytes.Clone(r.vec32())
	if a := r.vec32(); len(a) > 0 {
		e.Attestation = bytes.Clone(a)
	}
	if err := r.done(); err != nil {
		return Entry{}, err
	}
	id, err := ParseCredential(mls.BasicCredential(sender))
	if err != nil {
		return Entry{}, err
	}
	e.Sender = id
	return e, nil
}

// SharedMessage is an entry that verified.
type SharedMessage struct {
	Sender  Identity
	Epoch   uint64
	Payload Payload
	// Entry is the entry it came from, to keep or pass on.
	Entry Entry
}

// KeyCheck decides whether a key is the key of a sender: for a device,
// that its person's directory lists the device with that key; for
// Latere's member, that its attestation verifies. Directory.CheckMember
// and CheckLatere do each.
type KeyCheck func(sender Identity, signatureKey, attestation []byte) error

// VerifyEntry checks an entry of the group's conversation: that its
// signature verifies over its content and its epoch's group context for
// this group in the suite of conversations, that check accepts the key for
// the sender, and that the payload parses.
func VerifyEntry(e Entry, groupID []byte, check KeyCheck) (*SharedMessage, error) {
	if len(e.SignatureKey) != ed25519.PublicKeySize {
		return nil, errors.New("messaging: an entry's key is an Ed25519 key")
	}
	sc, err := mls.VerifySignedContent(e.Signed, e.SignatureKey)
	if err != nil {
		return nil, err
	}
	if err := CheckSuite(sc.CipherSuite); err != nil {
		return nil, err
	}
	if !bytes.Equal(sc.GroupID, groupID) {
		return nil, errors.New("messaging: entry is from another conversation")
	}
	if err := check(e.Sender, e.SignatureKey, e.Attestation); err != nil {
		return nil, err
	}
	p, err := ParsePayload(sc.ApplicationData)
	if err != nil {
		return nil, err
	}
	return &SharedMessage{Sender: e.Sender, Epoch: sc.Epoch, Payload: p, Entry: e}, nil
}

// The sizes of a history bundle.
const (
	// BundleChunkBytes is how much of a bundle one AES-GCM chunk seals.
	BundleChunkBytes = 64 << 10
	// MaxBundleBytes is the most a sealed bundle takes.
	MaxBundleBytes = 64 << 20
)

const (
	bundleVersion = 1
	bundleAAD     = "latere messaging history bundle v1"
	gcmTag        = 16
)

// BundleKeySize is the size of the key a bundle is sealed under.
const BundleKeySize = 32

// SealedBundle is a history bundle sealed under a fresh key, for the
// service to store and the new member to fetch.
type SealedBundle struct {
	// Ciphertext is what is uploaded.
	Ciphertext []byte
	// Key is the key it is sealed under, which only the history notice
	// carries, sealed to each new member.
	Key []byte
	// Digest is the SHA-256 of Ciphertext.
	Digest []byte
}

// SealBundle seals entries as a history bundle under a fresh key, in
// chunks of BundleChunkBytes, each chunk's nonce its position and
// whether it is the last, so a bundle cut short or reordered does not
// open.
func SealBundle(entries []Entry) (*SealedBundle, error) {
	plain := encode(func(w *writer) {
		w.u8(bundleVersion)
		for _, e := range entries {
			w.vec32(e.Bytes())
		}
	})
	key := make([]byte, BundleKeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	aead, err := gcm(key)
	if err != nil {
		return nil, err
	}
	chunks := (len(plain) + BundleChunkBytes - 1) / BundleChunkBytes
	if int64(len(plain))+int64(chunks)*gcmTag > MaxBundleBytes {
		return nil, fmt.Errorf("messaging: a history bundle is at most %d bytes", MaxBundleBytes)
	}
	var out []byte
	for i := range chunks {
		end := min((i+1)*BundleChunkBytes, len(plain))
		out = aead.Seal(out, chunkNonce(i, i == chunks-1), plain[i*BundleChunkBytes:end], []byte(bundleAAD))
	}
	sum := sha256.Sum256(out)
	return &SealedBundle{Ciphertext: out, Key: key, Digest: sum[:]}, nil
}

func chunkNonce(i int, last bool) []byte {
	nonce := make([]byte, 12)
	binary.BigEndian.PutUint64(nonce[3:11], uint64(i))
	if last {
		nonce[11] = 1
	}
	return nonce
}

// OpenBundle checks a fetched bundle against the digest its notice gave
// and opens it with the notice's key, returning its entries unverified;
// VerifyEntry checks each.
func OpenBundle(ciphertext, key, digest []byte) ([]Entry, error) {
	if len(ciphertext) > MaxBundleBytes {
		return nil, fmt.Errorf("messaging: a history bundle is at most %d bytes", MaxBundleBytes)
	}
	sum := sha256.Sum256(ciphertext)
	if !bytes.Equal(sum[:], digest) {
		return nil, errors.New("messaging: history bundle does not match its digest")
	}
	if len(key) != BundleKeySize {
		return nil, ErrSealed
	}
	aead, err := gcm(key)
	if err != nil {
		return nil, err
	}
	var plain []byte
	for i := 0; ; i++ {
		n := BundleChunkBytes + gcmTag
		last := len(ciphertext) <= n
		if last {
			n = len(ciphertext)
		}
		plain, err = aead.Open(plain, chunkNonce(i, last), ciphertext[:n], []byte(bundleAAD))
		if err != nil {
			return nil, ErrSealed
		}
		ciphertext = ciphertext[n:]
		if last {
			break
		}
	}
	r := &reader{b: plain}
	if v := r.u8(); r.err == nil && v != bundleVersion {
		return nil, fmt.Errorf("%w: bundle version %d", errFormat, v)
	}
	var entries []Entry
	for r.err == nil && len(r.b) > 0 {
		e, err := ParseEntry(r.vec32())
		if r.err != nil {
			break
		}
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	if err := r.done(); err != nil {
		return nil, err
	}
	return entries, nil
}
