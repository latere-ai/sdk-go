// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package messaging

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/hpke"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

// A person's history backup is every message their devices can read,
// each sealed as one record under a key derived from the backup key. The
// backup key is random; every approved device of the person holds it,
// and the service stores it only wrapped under a key the recovery key
// derives. A record's id is a keyed hash of the message as signed, so
// two devices of the person write a message's record once and the
// service cannot tell which message a record is.

// BackupKeySize is the size of a backup key.
const BackupKeySize = 32

// BackupKey is the key a person's history backup is sealed under.
type BackupKey struct {
	key [BackupKeySize]byte
}

// NewBackupKey returns a fresh backup key.
func NewBackupKey() (*BackupKey, error) {
	var k BackupKey
	if _, err := rand.Read(k.key[:]); err != nil {
		return nil, err
	}
	return &k, nil
}

// ErrSealed is the error for sealed bytes that do not open: the wrong
// key, the wrong context, or bytes that were changed.
var ErrSealed = errors.New("messaging: sealed bytes do not open")

// The labels of what is sealed, so nothing sealed for one use opens as
// another.
const (
	wrapAAD      = "latere messaging backup key wrap v1"
	recordIDKey  = "latere messaging backup record id v1"
	recordKey    = "latere messaging backup record v1"
	indexKey     = "latere messaging backup index v1"
	transferInfo = "latere messaging backup key transfer v1"
)

// sealVersion is the first byte of every value seal writes.
const sealVersion = 1

// seal encrypts with AES-256-GCM under key, with a random nonce, and
// binds aad.
func seal(key, plaintext, aad []byte) ([]byte, error) {
	aead, err := gcm(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 1+aead.NonceSize(), 1+aead.NonceSize()+len(plaintext)+aead.Overhead())
	out[0] = sealVersion
	if _, err := rand.Read(out[1:]); err != nil {
		return nil, err
	}
	return aead.Seal(out, out[1:], plaintext, aad), nil
}

func open(key, sealed, aad []byte) ([]byte, error) {
	aead, err := gcm(key)
	if err != nil {
		return nil, err
	}
	if len(sealed) < 1+aead.NonceSize()+aead.Overhead() || sealed[0] != sealVersion {
		return nil, ErrSealed
	}
	nonce := sealed[1 : 1+aead.NonceSize()]
	out, err := aead.Open(nil, nonce, sealed[1+aead.NonceSize():], aad)
	if err != nil {
		return nil, ErrSealed
	}
	return out, nil
}

func gcm(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Wrap seals the backup key under the wrapping key the recovery key
// derives, bound to the person, for the service to store.
func (b *BackupKey) Wrap(recovery *RecoveryKey, person string) ([]byte, error) {
	w, err := recovery.wrappingKey()
	if err != nil {
		return nil, err
	}
	return seal(w, b.key[:], append([]byte(wrapAAD+"\x00"), person...))
}

// UnwrapBackupKey opens a backup key Wrap sealed, with the person's
// recovery key. It returns ErrSealed for the wrong key or person.
func UnwrapBackupKey(recovery *RecoveryKey, person string, wrapped []byte) (*BackupKey, error) {
	w, err := recovery.wrappingKey()
	if err != nil {
		return nil, err
	}
	raw, err := open(w, wrapped, append([]byte(wrapAAD+"\x00"), person...))
	if err != nil {
		return nil, err
	}
	if len(raw) != BackupKeySize {
		return nil, ErrSealed
	}
	var k BackupKey
	copy(k.key[:], raw)
	return &k, nil
}

func (b *BackupKey) derive(label string, context []byte) ([]byte, error) {
	return hkdf.Key(sha256.New, b.key[:], nil, label+"\x00"+string(context), 32)
}

// RecordID is the id of the backup record of a message, from the
// message as signed (mls.Message.Signed): the same for every device of
// the person, and nothing the service can link to the message.
func (b *BackupKey) RecordID(signed []byte) (string, error) {
	k, err := b.derive(recordIDKey, nil)
	if err != nil {
		return "", err
	}
	m := hmac.New(sha256.New, k)
	m.Write(signed)
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)), nil
}

// SealRecord seals a backup record's content, such as an encoded Entry,
// under the key of its id.
func (b *BackupKey) SealRecord(id string, content []byte) ([]byte, error) {
	k, err := b.derive(recordKey, []byte(id))
	if err != nil {
		return nil, err
	}
	return seal(k, content, []byte(id))
}

// OpenRecord opens a backup record SealRecord sealed under the same id.
func (b *BackupKey) OpenRecord(id string, sealed []byte) ([]byte, error) {
	k, err := b.derive(recordKey, []byte(id))
	if err != nil {
		return nil, err
	}
	return open(k, sealed, []byte(id))
}

// SealIndex seals the backup's index, the list of the person's
// conversations.
func (b *BackupKey) SealIndex(index []byte) ([]byte, error) {
	k, err := b.derive(indexKey, nil)
	if err != nil {
		return nil, err
	}
	return seal(k, index, []byte(indexKey))
}

// OpenIndex opens what SealIndex sealed.
func (b *BackupKey) OpenIndex(sealed []byte) ([]byte, error) {
	k, err := b.derive(indexKey, nil)
	if err != nil {
		return nil, err
	}
	return open(k, sealed, []byte(indexKey))
}

// transferSuite is HPKE with DHKEM(X25519, HKDF-SHA256), HKDF-SHA256
// and AES-256-GCM.
func transferSuite() (hpke.KDF, hpke.AEAD) { return hpke.HKDFSHA256(), hpke.AES256GCM() }

func transferContext(person, device string) []byte {
	return encode(func(w *writer) {
		w.vec32([]byte(transferInfo))
		w.vec32([]byte(person))
		w.vec32([]byte(device))
	})
}

// SealTo seals the backup key to a new device's X25519 key from its
// directory record, which an approving device does once it has vouched
// for the device.
func (b *BackupKey) SealTo(device DeviceRecord) ([]byte, error) {
	if device.Kind != RecordDevice {
		return nil, errors.New("messaging: a backup key is sealed to a device")
	}
	pub, err := ecdh.X25519().NewPublicKey(device.EncryptionKey)
	if err != nil {
		return nil, err
	}
	pk, err := hpke.NewDHKEMPublicKey(pub)
	if err != nil {
		return nil, err
	}
	kdf, aead := transferSuite()
	return hpke.Seal(pk, kdf, aead, transferContext(device.Person, device.ID), b.key[:])
}

// OpenBackupKey opens a backup key SealTo sealed to this device, with
// its X25519 private key.
func OpenBackupKey(key *ecdh.PrivateKey, person, device string, sealed []byte) (*BackupKey, error) {
	sk, err := hpke.NewDHKEMPrivateKey(key)
	if err != nil {
		return nil, err
	}
	kdf, aead := transferSuite()
	raw, err := hpke.Open(sk, kdf, aead, transferContext(person, device), sealed)
	if err != nil || len(raw) != BackupKeySize {
		return nil, ErrSealed
	}
	var k BackupKey
	copy(k.key[:], raw)
	return &k, nil
}

// Bytes returns the backup key, to keep where the device keeps its
// other keys.
func (b *BackupKey) Bytes() []byte { return append([]byte{}, b.key[:]...) }

// ParseBackupKey reads a backup key Bytes returned.
func ParseBackupKey(raw []byte) (*BackupKey, error) {
	if len(raw) != BackupKeySize {
		return nil, errors.New("messaging: a backup key has 32 bytes")
	}
	var k BackupKey
	copy(k.key[:], raw)
	return &k, nil
}
