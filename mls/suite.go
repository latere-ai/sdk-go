// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package mls

import (
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/hpke"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"errors"
	"fmt"
	"hash"
)

// CipherSuite names an MLS cipher suite by its code point in the IANA
// registry of RFC 9420, Section 17.1. The package implements the four
// suites the Go standard library has every primitive for.
type CipherSuite uint16

// The cipher suites this package implements, named as the IANA registry
// names them. MLS_128_DHKEMX25519_AES128GCM_SHA256_Ed25519 is the suite
// every MLS implementation must support.
const (
	MLS_128_DHKEMX25519_AES128GCM_SHA256_Ed25519 CipherSuite = 0x0001
	MLS_128_DHKEMP256_AES128GCM_SHA256_P256      CipherSuite = 0x0002
	MLS_256_DHKEMP521_AES256GCM_SHA512_P521      CipherSuite = 0x0005
	MLS_256_DHKEMP384_AES256GCM_SHA384_P384      CipherSuite = 0x0007
)

// Supported reports whether the package implements the suite.
func (cs CipherSuite) Supported() bool {
	_, err := suiteFor(cs)
	return err == nil
}

// String returns the suite's IANA name, or its code point for a suite
// the package does not implement.
func (cs CipherSuite) String() string {
	switch cs {
	case MLS_128_DHKEMX25519_AES128GCM_SHA256_Ed25519:
		return "MLS_128_DHKEMX25519_AES128GCM_SHA256_Ed25519"
	case MLS_128_DHKEMP256_AES128GCM_SHA256_P256:
		return "MLS_128_DHKEMP256_AES128GCM_SHA256_P256"
	case MLS_256_DHKEMP521_AES256GCM_SHA512_P521:
		return "MLS_256_DHKEMP521_AES256GCM_SHA512_P521"
	case MLS_256_DHKEMP384_AES256GCM_SHA384_P384:
		return "MLS_256_DHKEMP384_AES256GCM_SHA384_P384"
	}
	return fmt.Sprintf("CipherSuite(0x%04x)", uint16(cs))
}

// ErrUnsupportedSuite is returned for a cipher suite the package does not
// implement.
var ErrUnsupportedSuite = errors.New("mls: unsupported cipher suite")

// suite holds the primitives of one cipher suite.
type suite struct {
	id      CipherSuite
	newHash func() hash.Hash
	nh      int // the size of a hash and of a KDF output
	kem     hpke.KEM
	kdf     hpke.KDF
	aead    hpke.AEAD
	nk      int // the AEAD key size
	nn      int // the AEAD nonce size
	// sigCurve is the ECDSA curve, nil for Ed25519.
	sigCurve elliptic.Curve
	sigHash  crypto.Hash
}

var suites = map[CipherSuite]*suite{
	MLS_128_DHKEMX25519_AES128GCM_SHA256_Ed25519: {
		id: MLS_128_DHKEMX25519_AES128GCM_SHA256_Ed25519, newHash: sha256.New, nh: 32,
		kem: hpke.DHKEM(ecdh.X25519()), kdf: hpke.HKDFSHA256(), aead: hpke.AES128GCM(),
		nk: 16, nn: 12,
	},
	MLS_128_DHKEMP256_AES128GCM_SHA256_P256: {
		id: MLS_128_DHKEMP256_AES128GCM_SHA256_P256, newHash: sha256.New, nh: 32,
		kem: hpke.DHKEM(ecdh.P256()), kdf: hpke.HKDFSHA256(), aead: hpke.AES128GCM(),
		nk: 16, nn: 12, sigCurve: elliptic.P256(), sigHash: crypto.SHA256,
	},
	MLS_256_DHKEMP521_AES256GCM_SHA512_P521: {
		id: MLS_256_DHKEMP521_AES256GCM_SHA512_P521, newHash: sha512.New, nh: 64,
		kem: hpke.DHKEM(ecdh.P521()), kdf: hpke.HKDFSHA512(), aead: hpke.AES256GCM(),
		nk: 32, nn: 12, sigCurve: elliptic.P521(), sigHash: crypto.SHA512,
	},
	MLS_256_DHKEMP384_AES256GCM_SHA384_P384: {
		id: MLS_256_DHKEMP384_AES256GCM_SHA384_P384, newHash: sha512.New384, nh: 48,
		kem: hpke.DHKEM(ecdh.P384()), kdf: hpke.HKDFSHA384(), aead: hpke.AES256GCM(),
		nk: 32, nn: 12, sigCurve: elliptic.P384(), sigHash: crypto.SHA384,
	},
}

func suiteFor(cs CipherSuite) (*suite, error) {
	s, ok := suites[cs]
	if !ok {
		return nil, fmt.Errorf("%w: %v", ErrUnsupportedSuite, cs)
	}
	return s, nil
}

// labelPrefix starts every label MLS 1.0 signs, encrypts or derives with.
const labelPrefix = "MLS 1.0 "

func (s *suite) hash(b []byte) []byte {
	h := s.newHash()
	h.Write(b)
	return h.Sum(nil)
}

func (s *suite) mac(key, data []byte) []byte {
	m := hmac.New(s.newHash, key)
	m.Write(data)
	return m.Sum(nil)
}

// extract is HKDF-Extract (RFC 5869) with the suite's hash. An empty salt
// is the same HMAC key as a salt of nh zero bytes.
func (s *suite) extract(salt, ikm []byte) []byte {
	return s.mac(salt, ikm)
}

// expand is HKDF-Expand (RFC 5869). n is at most 255 times nh; every
// caller passes a suite size, and the exporter checks a caller's length.
func (s *suite) expand(prk, info []byte, n int) []byte {
	out := make([]byte, 0, n+s.nh)
	var t []byte
	for i := byte(1); len(out) < n; i++ {
		m := hmac.New(s.newHash, prk)
		m.Write(t)
		m.Write(info)
		m.Write([]byte{i})
		t = m.Sum(nil)
		out = append(out, t...)
	}
	return out[:n]
}

// expandWithLabel is ExpandWithLabel of RFC 9420, Section 8.
func (s *suite) expandWithLabel(secret []byte, label string, context []byte, n int) []byte {
	info := encode(func(w *writer) {
		w.u16(uint16(n))
		w.vec([]byte(labelPrefix + label))
		w.vec(context)
	})
	return s.expand(secret, info, n)
}

func (s *suite) deriveSecret(secret []byte, label string) []byte {
	return s.expandWithLabel(secret, label, nil, s.nh)
}

// deriveTreeSecret is DeriveTreeSecret of RFC 9420, Section 9.1.
func (s *suite) deriveTreeSecret(secret []byte, label string, generation uint32, n int) []byte {
	return s.expandWithLabel(secret, label, encode(func(w *writer) { w.u32(generation) }), n)
}

// refHash is RefHash of RFC 9420, Section 5.2. The label carries its own
// "MLS 1.0 " prefix where the RFC gives one.
func (s *suite) refHash(label string, value []byte) []byte {
	return s.hash(encode(func(w *writer) {
		w.vec([]byte(label))
		w.vec(value)
	}))
}

func (s *suite) gcm(key []byte) (cipher.AEAD, error) {
	if len(key) != s.nk {
		return nil, fmt.Errorf("mls: AEAD key of %d bytes, want %d", len(key), s.nk)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (s *suite) seal(key, nonce, aad, plaintext []byte) ([]byte, error) {
	a, err := s.gcm(key)
	if err != nil {
		return nil, err
	}
	if len(nonce) != a.NonceSize() {
		return nil, fmt.Errorf("mls: AEAD nonce of %d bytes, want %d", len(nonce), a.NonceSize())
	}
	return a.Seal(nil, nonce, plaintext, aad), nil
}

// errDecrypt is returned when an AEAD or HPKE ciphertext does not open.
var errDecrypt = errors.New("mls: message authentication failed")

func (s *suite) open(key, nonce, aad, ciphertext []byte) ([]byte, error) {
	a, err := s.gcm(key)
	if err != nil {
		return nil, err
	}
	if len(nonce) != a.NonceSize() {
		return nil, fmt.Errorf("mls: AEAD nonce of %d bytes, want %d", len(nonce), a.NonceSize())
	}
	pt, err := a.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, errDecrypt
	}
	return pt, nil
}

// signContent is the SignContent of RFC 9420, Section 5.1.2.
func signContent(label string, content []byte) []byte {
	return encode(func(w *writer) {
		w.vec([]byte(labelPrefix + label))
		w.vec(content)
	})
}

func (s *suite) signWithLabel(key *SignatureKey, label string, content []byte) ([]byte, error) {
	if key == nil || key.suite != s.id {
		return nil, errors.New("mls: signature key is for another cipher suite")
	}
	msg := signContent(label, content)
	if s.sigCurve == nil {
		return ed25519.Sign(key.ed, msg), nil
	}
	h := s.sigHash.New()
	h.Write(msg)
	return ecdsa.SignASN1(rand.Reader, key.ec, h.Sum(nil))
}

// errSignature is returned when a signature does not verify.
var errSignature = errors.New("mls: signature does not verify")

func (s *suite) verifyWithLabel(pub []byte, label string, content, sig []byte) error {
	msg := signContent(label, content)
	if s.sigCurve == nil {
		if len(pub) != ed25519.PublicKeySize {
			return fmt.Errorf("%w: Ed25519 key of %d bytes", errSignature, len(pub))
		}
		if !ed25519.Verify(ed25519.PublicKey(pub), msg, sig) {
			return errSignature
		}
		return nil
	}
	pk, err := ecdsa.ParseUncompressedPublicKey(s.sigCurve, pub)
	if err != nil {
		return fmt.Errorf("%w: %v", errSignature, err)
	}
	h := s.sigHash.New()
	h.Write(msg)
	if !ecdsa.VerifyASN1(pk, h.Sum(nil), sig) {
		return errSignature
	}
	return nil
}

// checkSignaturePublicKey reports whether pub is a valid signature
// public key for the suite.
func (s *suite) checkSignaturePublicKey(pub []byte) error {
	if s.sigCurve == nil {
		if len(pub) != ed25519.PublicKeySize {
			return fmt.Errorf("mls: Ed25519 public key of %d bytes", len(pub))
		}
		return nil
	}
	if _, err := ecdsa.ParseUncompressedPublicKey(s.sigCurve, pub); err != nil {
		return fmt.Errorf("mls: signature public key: %w", err)
	}
	return nil
}

// encryptContext is the HPKE info of EncryptWithLabel, RFC 9420,
// Section 5.1.3.
func encryptContext(label string, context []byte) []byte {
	return encode(func(w *writer) {
		w.vec([]byte(labelPrefix + label))
		w.vec(context)
	})
}

// encryptWithLabel seals plaintext to an HPKE public key in base mode
// and returns the KEM output and the ciphertext.
func (s *suite) encryptWithLabel(pub []byte, label string, context, plaintext []byte) (kemOutput, ciphertext []byte, err error) {
	pk, err := s.kem.NewPublicKey(pub)
	if err != nil {
		return nil, nil, fmt.Errorf("mls: HPKE public key: %w", err)
	}
	enc, sender, err := hpke.NewSender(pk, s.kdf, s.aead, encryptContext(label, context))
	if err != nil {
		return nil, nil, err
	}
	ct, err := sender.Seal(nil, plaintext)
	if err != nil {
		return nil, nil, err
	}
	return enc, ct, nil
}

// scalarSize is the size of a private scalar of the suite's NIST curve,
// zero for the Edwards and Montgomery curves.
func (s *suite) scalarSize() int {
	if s.sigCurve == nil {
		return 0
	}
	return (s.sigCurve.Params().BitSize + 7) / 8
}

// padScalar left-pads a NIST curve private key some encoders write
// without its leading zero bytes.
func (s *suite) padScalar(priv []byte) []byte {
	if n := s.scalarSize(); n > 0 && len(priv) < n {
		return append(make([]byte, n-len(priv)), priv...)
	}
	return priv
}

func (s *suite) hpkePrivateKey(priv []byte) (hpke.PrivateKey, error) {
	sk, err := s.kem.NewPrivateKey(s.padScalar(priv))
	if err != nil {
		return nil, fmt.Errorf("mls: HPKE private key: %w", err)
	}
	return sk, nil
}

func (s *suite) decryptWithLabel(priv []byte, label string, context, kemOutput, ciphertext []byte) ([]byte, error) {
	sk, err := s.hpkePrivateKey(priv)
	if err != nil {
		return nil, err
	}
	r, err := hpke.NewRecipient(kemOutput, sk, s.kdf, s.aead, encryptContext(label, context))
	if err != nil {
		return nil, errDecrypt
	}
	pt, err := r.Open(nil, ciphertext)
	if err != nil {
		return nil, errDecrypt
	}
	return pt, nil
}

// deriveKeyPair is KEM.DeriveKeyPair: the HPKE key pair, private key
// first as SerializePrivateKey encodes it, derived from a secret.
func (s *suite) deriveKeyPair(secret []byte) (priv, pub []byte, err error) {
	sk, err := s.kem.DeriveKeyPair(secret)
	if err != nil {
		return nil, nil, err
	}
	priv, err = sk.Bytes()
	if err != nil {
		return nil, nil, err
	}
	return priv, sk.PublicKey().Bytes(), nil
}

func (s *suite) generateKeyPair() (priv, pub []byte, err error) {
	sk, err := s.kem.GenerateKey()
	if err != nil {
		return nil, nil, err
	}
	priv, err = sk.Bytes()
	if err != nil {
		return nil, nil, err
	}
	return priv, sk.PublicKey().Bytes(), nil
}

// hpkePublic returns the public key of an HPKE private key.
func (s *suite) hpkePublic(priv []byte) ([]byte, error) {
	sk, err := s.hpkePrivateKey(priv)
	if err != nil {
		return nil, err
	}
	return sk.PublicKey().Bytes(), nil
}

// checkHPKEPublicKey reports whether pub is a valid HPKE public key for
// the suite.
func (s *suite) checkHPKEPublicKey(pub []byte) error {
	if _, err := s.kem.NewPublicKey(pub); err != nil {
		return fmt.Errorf("mls: HPKE public key: %w", err)
	}
	return nil
}

// externalInitLabel is the exporter context of RFC 9420, Section 8.3.
const externalInitLabel = labelPrefix + "external init secret"

// externalInitSend returns the KEM output of an ExternalInit proposal
// and the init secret it carries.
func (s *suite) externalInitSend(externalPub []byte) (kemOutput, initSecret []byte, err error) {
	pk, err := s.kem.NewPublicKey(externalPub)
	if err != nil {
		return nil, nil, fmt.Errorf("mls: external public key: %w", err)
	}
	enc, sender, err := hpke.NewSender(pk, s.kdf, s.aead, nil)
	if err != nil {
		return nil, nil, err
	}
	secret, err := sender.Export(externalInitLabel, s.nh)
	if err != nil {
		return nil, nil, err
	}
	return enc, secret, nil
}

func (s *suite) externalInitReceive(externalPriv, kemOutput []byte) ([]byte, error) {
	sk, err := s.hpkePrivateKey(externalPriv)
	if err != nil {
		return nil, err
	}
	r, err := hpke.NewRecipient(kemOutput, sk, s.kdf, s.aead, nil)
	if err != nil {
		return nil, fmt.Errorf("mls: external init: %w", err)
	}
	return r.Export(externalInitLabel, s.nh)
}

// equal compares secrets and tags in constant time.
func equal(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}

// SignatureKey is a member's private signing key for one cipher suite.
// It signs the member's key packages, leaf nodes and messages.
type SignatureKey struct {
	suite CipherSuite
	ed    ed25519.PrivateKey
	ec    *ecdsa.PrivateKey
	pub   []byte
}

// GenerateSignatureKey returns a fresh signing key for the suite.
func GenerateSignatureKey(cs CipherSuite) (*SignatureKey, error) {
	s, err := suiteFor(cs)
	if err != nil {
		return nil, err
	}
	if s.sigCurve == nil {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		return &SignatureKey{suite: cs, ed: priv, pub: pub}, nil
	}
	k, err := ecdsa.GenerateKey(s.sigCurve, rand.Reader)
	if err != nil {
		return nil, err
	}
	return ecSignatureKey(cs, k)
}

// NewSignatureKey parses a private signing key as Bytes encodes it: the
// 32-byte seed for Ed25519, the big-endian scalar for ECDSA, which may
// come without its leading zero bytes.
func NewSignatureKey(cs CipherSuite, private []byte) (*SignatureKey, error) {
	s, err := suiteFor(cs)
	if err != nil {
		return nil, err
	}
	if s.sigCurve == nil {
		if len(private) != ed25519.SeedSize {
			return nil, fmt.Errorf("mls: Ed25519 private key of %d bytes, want %d", len(private), ed25519.SeedSize)
		}
		priv := ed25519.NewKeyFromSeed(private)
		return &SignatureKey{suite: cs, ed: priv, pub: priv.Public().(ed25519.PublicKey)}, nil
	}
	k, err := ecdsa.ParseRawPrivateKey(s.sigCurve, s.padScalar(private))
	if err != nil {
		return nil, fmt.Errorf("mls: ECDSA private key: %w", err)
	}
	return ecSignatureKey(cs, k)
}

func ecSignatureKey(cs CipherSuite, k *ecdsa.PrivateKey) (*SignatureKey, error) {
	pub, err := k.PublicKey.Bytes()
	if err != nil {
		return nil, err
	}
	return &SignatureKey{suite: cs, ec: k, pub: pub}, nil
}

// CipherSuite returns the suite the key signs for.
func (k *SignatureKey) CipherSuite() CipherSuite { return k.suite }

// PublicKey returns the public key as MLS encodes it: 32 bytes for
// Ed25519, an uncompressed point for ECDSA.
func (k *SignatureKey) PublicKey() []byte { return append([]byte{}, k.pub...) }

// Bytes returns the private key in the encoding NewSignatureKey parses.
// It is secret: store it only where the member's other keys are kept.
func (k *SignatureKey) Bytes() ([]byte, error) {
	if k.ed != nil {
		return append([]byte{}, k.ed.Seed()...), nil
	}
	return k.ec.Bytes()
}
