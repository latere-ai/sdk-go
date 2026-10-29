// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package messaging

import (
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"
)

// A recovery key is 128 random bits the person keeps, made on their
// device. It is written as 28 characters in seven groups of four, from
// the 32 symbols of Crockford's base32, which leaves out I, L, O and U
// so no two look alike. The 140 bits are a 2-bit version, the 128-bit
// key and a 10-bit check, the first bits of a SHA-256 of a label, the
// version and the key, which catches a mistyped character before anything is asked
// of the service.

// RecoveryKeyGroups is how many groups of four characters a recovery
// key is written in.
const RecoveryKeyGroups = 7

const (
	recoveryAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	recoveryVersion  = 0
	recoveryChars    = RecoveryKeyGroups * 4
	recoveryKeyBytes = 16
	recoveryCheck    = "latere messaging recovery key check v1"
)

// ErrRecoveryKey is the error for text that is not a recovery key.
var ErrRecoveryKey = errors.New("messaging: not a recovery key")

// RecoveryKey is a person's recovery key.
type RecoveryKey struct {
	key [recoveryKeyBytes]byte
}

// NewRecoveryKey returns a fresh recovery key.
func NewRecoveryKey() (*RecoveryKey, error) {
	var k RecoveryKey
	if _, err := rand.Read(k.key[:]); err != nil {
		return nil, err
	}
	return &k, nil
}

func (k *RecoveryKey) check() uint16 {
	h := sha256.New()
	h.Write([]byte(recoveryCheck))
	h.Write([]byte{recoveryVersion})
	h.Write(k.key[:])
	sum := h.Sum(nil)
	return uint16(sum[0])<<2 | uint16(sum[1]>>6)
}

// symbols returns the 140 bits as 28 five-bit values.
func (k *RecoveryKey) symbols() [recoveryChars]byte {
	// 2 version bits, 128 key bits, 10 check bits, most significant first.
	var bits [140]byte
	bits[0], bits[1] = recoveryVersion>>1&1, recoveryVersion&1
	for i := range 128 {
		bits[2+i] = k.key[i/8] >> (7 - i%8) & 1
	}
	c := k.check()
	for i := range 10 {
		bits[130+i] = byte(c>>(9-i)) & 1
	}
	var out [recoveryChars]byte
	for i := range out {
		for j := range 5 {
			out[i] = out[i]<<1 | bits[i*5+j]
		}
	}
	return out
}

// String writes the key as the person sees it: seven groups of four
// characters joined by hyphens.
func (k *RecoveryKey) String() string {
	var sb strings.Builder
	for i, s := range k.symbols() {
		if i > 0 && i%4 == 0 {
			sb.WriteByte('-')
		}
		sb.WriteByte(recoveryAlphabet[s])
	}
	return sb.String()
}

// Group returns the i-th group of four characters, from 0, for the
// person to type back.
func (k *RecoveryKey) Group(i int) (string, error) {
	if i < 0 || i >= RecoveryKeyGroups {
		return "", fmt.Errorf("messaging: a recovery key has groups 0 to %d", RecoveryKeyGroups-1)
	}
	return strings.Split(k.String(), "-")[i], nil
}

// CheckGroup reports whether typed, read as ParseRecoveryKey reads a
// key, is the i-th group, in constant time.
func (k *RecoveryKey) CheckGroup(i int, typed string) bool {
	want, err := k.Group(i)
	if err != nil {
		return false
	}
	got, ok := normalize(typed)
	return ok && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// normalize uppercases s, drops spaces and hyphens, and reads the
// characters people confuse with the alphabet's as the ones it has: O
// as 0, I and L as 1.
func normalize(s string) (string, bool) {
	var sb strings.Builder
	for _, c := range strings.ToUpper(s) {
		switch c {
		case ' ', '-', '\t':
			continue
		case 'O':
			c = '0'
		case 'I', 'L':
			c = '1'
		}
		if !strings.ContainsRune(recoveryAlphabet, c) {
			return "", false
		}
		sb.WriteRune(c)
	}
	return sb.String(), true
}

// ParseRecoveryKey reads a recovery key as a person types it, ignoring
// case, spaces and hyphens. It returns ErrRecoveryKey for text of the
// wrong length or a failing check.
func ParseRecoveryKey(s string) (*RecoveryKey, error) {
	n, ok := normalize(s)
	if !ok || len(n) != recoveryChars {
		return nil, ErrRecoveryKey
	}
	var bits [140]byte
	for i := range recoveryChars {
		v := byte(strings.IndexByte(recoveryAlphabet, n[i]))
		for j := range 5 {
			bits[i*5+j] = v >> (4 - j) & 1
		}
	}
	if bits[0]<<1|bits[1] != recoveryVersion {
		return nil, fmt.Errorf("%w: unknown version", ErrRecoveryKey)
	}
	var k RecoveryKey
	for i := range 128 {
		k.key[i/8] |= bits[2+i] << (7 - i%8)
	}
	var c uint16
	for i := range 10 {
		c = c<<1 | uint16(bits[130+i])
	}
	if c != k.check() {
		return nil, fmt.Errorf("%w: the check does not match, so a character is mistyped", ErrRecoveryKey)
	}
	return &k, nil
}

// The labels the recovery key's derivations use.
const (
	wrapLabel    = "latere messaging recovery wrap v1"
	signingLabel = "latere messaging recovery signing v1"
)

func (k *RecoveryKey) derive(label string) ([]byte, error) {
	return hkdf.Key(sha256.New, k.key[:], nil, label, 32)
}

// SigningKey returns the recovery signing key, whose public half the
// person's recovery record holds and which vouches for a new device.
func (k *RecoveryKey) SigningKey() (ed25519.PrivateKey, error) {
	seed, err := k.derive(signingLabel)
	if err != nil {
		return nil, err
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// wrappingKey returns the key the backup key is wrapped under.
func (k *RecoveryKey) wrappingKey() ([]byte, error) { return k.derive(wrapLabel) }
