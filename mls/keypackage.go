// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package mls

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"time"
)

// DefaultKeyPackageLifetime is how long a key package NewKeyPackage makes
// is valid when the options set no lifetime.
const DefaultKeyPackageLifetime = 30 * 24 * time.Hour

// KeyPackage is a client's signed offer to be added to a group: the leaf
// it will hold and the key a Welcome to it is encrypted to. A key
// package is used once.
type KeyPackage struct {
	kp  keyPackage
	raw []byte
}

// KeyPackageSecrets are the private keys of a key package, kept by the
// client that made it until a Welcome uses it.
type KeyPackageSecrets struct {
	initPriv       []byte
	encryptionPriv []byte
}

// KeyPackageOptions sets what NewKeyPackage puts in a key package.
type KeyPackageOptions struct {
	// Lifetime bounds when the key package is valid. The zero value
	// means from an hour ago, for clocks that run behind, to
	// DefaultKeyPackageLifetime from now.
	Lifetime Lifetime
	// Extensions are the extension types beyond RFC 9420's that the
	// client supports, listed in its capabilities.
	Extensions []ExtensionType
	// LeafExtensions are carried in the client's leaf. Their types are
	// added to the capabilities.
	LeafExtensions []Extension
	// KeyPackageExtensions are carried in the key package itself.
	KeyPackageExtensions []Extension
	// Now is the clock for the default lifetime; nil uses time.Now.
	Now func() time.Time
}

// NewKeyPackage makes a key package for a client with the credential and
// signing key, and returns it with its private keys.
func NewKeyPackage(signer *SignatureKey, cred Credential, opts KeyPackageOptions) (*KeyPackage, *KeyPackageSecrets, error) {
	s, err := suiteFor(signer.CipherSuite())
	if err != nil {
		return nil, nil, err
	}
	if err := uniqueExtensions(opts.LeafExtensions); err != nil {
		return nil, nil, err
	}
	if err := uniqueExtensions(opts.KeyPackageExtensions); err != nil {
		return nil, nil, err
	}
	lt := opts.Lifetime
	if lt == (Lifetime{}) {
		now := time.Now
		if opts.Now != nil {
			now = opts.Now
		}
		t := now()
		lt = Lifetime{NotBefore: uint64(t.Add(-time.Hour).Unix()), NotAfter: uint64(t.Add(DefaultKeyPackageLifetime).Unix())}
	}
	initPriv, initPub, err := s.generateKeyPair()
	if err != nil {
		return nil, nil, err
	}
	encPriv, encPub, err := s.generateKeyPair()
	if err != nil {
		return nil, nil, err
	}
	caps := defaultCapabilities(s.id, cred.Type, opts.Extensions, opts.LeafExtensions)
	kp := keyPackage{
		version: protocolVersionMLS10,
		suite:   s.id,
		initKey: initPub,
		leaf: leafNode{
			encryptionKey: encPub,
			signatureKey:  signer.PublicKey(),
			credential:    cred,
			capabilities:  caps,
			source:        leafSourceKeyPackage,
			lifetime:      lt,
			extensions:    cloneExtensions(opts.LeafExtensions),
		},
		extensions: cloneExtensions(opts.KeyPackageExtensions),
	}
	if err := kp.leaf.sign(s, signer, nil, 0); err != nil {
		return nil, nil, err
	}
	sig, err := s.signWithLabel(signer, "KeyPackageTBS", encode(kp.marshalContent))
	if err != nil {
		return nil, nil, err
	}
	kp.signature = sig
	k := &KeyPackage{kp: kp}
	k.raw = encode((&mlsMessage{wireFormat: WireFormatKeyPackage, keyPackage: &k.kp}).marshal)
	return k, &KeyPackageSecrets{initPriv: initPriv, encryptionPriv: encPriv}, nil
}

// defaultCapabilities lists what this package supports for a leaf.
func defaultCapabilities(cs CipherSuite, ct CredentialType, exts []ExtensionType, leafExts []Extension) Capabilities {
	c := Capabilities{
		Versions:     []uint16{protocolVersionMLS10},
		CipherSuites: []CipherSuite{cs},
		Credentials:  []CredentialType{ct},
	}
	for _, t := range exts {
		if !t.isDefault() && !slices.Contains(c.Extensions, t) {
			c.Extensions = append(c.Extensions, t)
		}
	}
	for _, e := range leafExts {
		if !e.Type.isDefault() && !slices.Contains(c.Extensions, e.Type) {
			c.Extensions = append(c.Extensions, e.Type)
		}
	}
	return c
}

// ParseKeyPackage decodes a key package as an MLSMessage carries it and
// checks its signatures and its leaf. It does not check the lifetime or
// the credential, which a group does when the key package is added.
func ParseKeyPackage(b []byte) (*KeyPackage, error) {
	m, err := parseMessage(b)
	if err != nil {
		return nil, err
	}
	if m.wireFormat != WireFormatKeyPackage {
		return nil, fmt.Errorf("mls: message is not a key package")
	}
	k := &KeyPackage{kp: *m.keyPackage, raw: bytes.Clone(b)}
	s, err := suiteFor(k.kp.suite)
	if err != nil {
		return nil, err
	}
	if err := k.kp.verify(s); err != nil {
		return nil, err
	}
	return k, nil
}

// verify checks what a key package says of itself, RFC 9420, Section
// 10.1, leaving to the group what depends on the group.
func (k *keyPackage) verify(s *suite) error {
	if k.version != protocolVersionMLS10 {
		return fmt.Errorf("mls: key package for protocol version %d", k.version)
	}
	if k.suite != s.id {
		return fmt.Errorf("mls: key package for %v in a %v group", k.suite, s.id)
	}
	if k.leaf.source != leafSourceKeyPackage {
		return fmt.Errorf("mls: key package leaf has source %d", k.leaf.source)
	}
	if err := s.checkHPKEPublicKey(k.initKey); err != nil {
		return err
	}
	if bytes.Equal(k.initKey, k.leaf.encryptionKey) {
		return errors.New("mls: key package init key is its leaf's encryption key")
	}
	if err := uniqueExtensions(k.extensions); err != nil {
		return err
	}
	if err := s.verifyWithLabel(k.leaf.signatureKey, "KeyPackageTBS", encode(k.marshalContent), k.signature); err != nil {
		return fmt.Errorf("mls: key package: %w", err)
	}
	return checkLeafSelf(s, &k.leaf, nil, 0)
}

// Bytes returns the key package as an MLSMessage.
func (k *KeyPackage) Bytes() []byte { return bytes.Clone(k.raw) }

// Ref returns the reference a Welcome names the key package by.
func (k *KeyPackage) Ref() []byte {
	s, err := suiteFor(k.kp.suite)
	if err != nil {
		// A KeyPackage is only made or parsed for a supported suite.
		return nil
	}
	return k.kp.ref(s)
}

// CipherSuite returns the suite the key package is for.
func (k *KeyPackage) CipherSuite() CipherSuite { return k.kp.suite }

// Credential returns the client's credential.
func (k *KeyPackage) Credential() Credential { return k.kp.leaf.credential.clone() }

// SignatureKey returns the client's signature public key.
func (k *KeyPackage) SignatureKey() []byte { return bytes.Clone(k.kp.leaf.signatureKey) }

// Lifetime returns when the key package is valid.
func (k *KeyPackage) Lifetime() Lifetime { return k.kp.leaf.lifetime }

// LeafExtensions returns the extensions in the client's leaf.
func (k *KeyPackage) LeafExtensions() []Extension { return cloneExtensions(k.kp.leaf.extensions) }

// Extensions returns the key package's own extensions.
func (k *KeyPackage) Extensions() []Extension { return cloneExtensions(k.kp.extensions) }

// mlsLabels are the EncryptWithLabel labels MLS uses itself, which an
// application's sealing never takes.
var mlsLabels = []string{"Welcome", "UpdatePathNode"}

// Seal encrypts plaintext to the key package's init key with
// EncryptWithLabel, RFC 9420, Section 5.1.3, under an application's
// label and context, for the client that made it to open with
// KeyPackageSecrets.Open. The label may not be one MLS uses.
func (k *KeyPackage) Seal(label string, context, plaintext []byte) ([]byte, error) {
	if label == "" || slices.Contains(mlsLabels, label) {
		return nil, fmt.Errorf("mls: %q is not a label for an application", label)
	}
	s, err := suiteFor(k.kp.suite)
	if err != nil {
		return nil, err
	}
	kem, ct, err := s.encryptWithLabel(k.kp.initKey, label, context, plaintext)
	if err != nil {
		return nil, err
	}
	return encode((&hpkeCiphertext{kemOutput: kem, ciphertext: ct}).marshal), nil
}

// Open decrypts what KeyPackage.Seal sealed to kp, whose secrets these
// are, under the same label and context.
func (k *KeyPackageSecrets) Open(kp *KeyPackage, label string, context, sealed []byte) ([]byte, error) {
	if label == "" || slices.Contains(mlsLabels, label) {
		return nil, fmt.Errorf("mls: %q is not a label for an application", label)
	}
	s, err := suiteFor(kp.kp.suite)
	if err != nil {
		return nil, err
	}
	var c hpkeCiphertext
	if err := decode(bytes.Clone(sealed), c.unmarshal); err != nil {
		return nil, err
	}
	return s.decryptWithLabel(k.initPriv, label, context, c.kemOutput, c.ciphertext)
}

// Bytes encodes the secrets for storage beside the key package. They are
// private keys.
func (k *KeyPackageSecrets) Bytes() []byte {
	return encode(func(w *writer) {
		w.vec(k.initPriv)
		w.vec(k.encryptionPriv)
	})
}

// ParseKeyPackageSecrets decodes what KeyPackageSecrets.Bytes encodes.
func ParseKeyPackageSecrets(b []byte) (*KeyPackageSecrets, error) {
	k := new(KeyPackageSecrets)
	err := decode(bytes.Clone(b), func(r *reader) {
		k.initPriv = r.vec()
		k.encryptionPriv = r.vec()
	})
	if err != nil {
		return nil, err
	}
	return k, nil
}

// checkLeafSelf checks what a leaf says of itself: its keys are valid for
// the suite, its signature verifies, its credential and extensions are
// in its capabilities. Its capabilities need not list the group's suite:
// RFC 9420 puts that on the client making the leaf, and the suite is
// fixed by the group context and the key package's signed suite.
func checkLeafSelf(s *suite, l *leafNode, groupID []byte, leafIndex uint32) error {
	if err := s.checkHPKEPublicKey(l.encryptionKey); err != nil {
		return err
	}
	if err := s.checkSignaturePublicKey(l.signatureKey); err != nil {
		return err
	}
	if err := l.verify(s, groupID, leafIndex); err != nil {
		return fmt.Errorf("mls: leaf node: %w", err)
	}
	if !slices.Contains(l.capabilities.Credentials, l.credential.Type) {
		return fmt.Errorf("mls: leaf's credential type 0x%04x is not in its capabilities", uint16(l.credential.Type))
	}
	if !slices.Contains(l.capabilities.Versions, protocolVersionMLS10) {
		return errors.New("mls: leaf does not support MLS 1.0")
	}
	if err := uniqueExtensions(l.extensions); err != nil {
		return err
	}
	for _, e := range l.extensions {
		if !l.capabilities.supportsExtension(e.Type) {
			return fmt.Errorf("mls: leaf carries extension 0x%04x its capabilities do not list", uint16(e.Type))
		}
	}
	return nil
}
