// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package messaging

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"

	"latere.ai/x/sdk-go/mls"
)

// Suite is the one cipher suite of conversations. Every group, key
// package and leaf of another suite is refused, by the checks here and
// by the service.
const Suite = mls.MLS_128_DHKEMX25519_AES128GCM_SHA256_Ed25519

// The extension types conversations add to MLS, from the range RFC 9420
// keeps for private use.
const (
	// StateExtension is the group context extension that carries a
	// conversation's State.
	StateExtension mls.ExtensionType = 0xF5E0
	// AttestationExtension is the leaf extension of Latere's member: the
	// installation's signature over the group and the leaf's key.
	AttestationExtension mls.ExtensionType = 0xF5E1
	// NoticeExtension is the Welcome extension that carries the digest of
	// the commit's history notice, so a joiner takes the notice the
	// committer made and no other.
	NoticeExtension mls.ExtensionType = 0xF5E2
)

// MaxIDBytes bounds a person's, a device's and a conversation's id.
const MaxIDBytes = 128

// ErrSuite is the error for a group, key package or leaf whose suite is
// not Suite.
var ErrSuite = errors.New("messaging: cipher suite is not the suite of conversations")

// CheckSuite returns ErrSuite unless cs is Suite.
func CheckSuite(cs mls.CipherSuite) error {
	if cs != Suite {
		return fmt.Errorf("%w: %v", ErrSuite, cs)
	}
	return nil
}

// Identity is who a leaf of a conversation's group is: a device of a
// person, or Latere's member of one conversation.
type Identity struct {
	// Person is the person's auth subject; empty for Latere's member.
	Person string
	// Device is the device's id among the person's devices.
	Device string
	// Conversation is the conversation Latere's member belongs to; empty
	// for a device.
	Conversation string
}

// Latere reports whether the identity is Latere's member.
func (id Identity) Latere() bool { return id.Conversation != "" }

// String is the identity as a basic credential names it:
// user:<person>/device:<device> or latere/conversation:<conversation>.
func (id Identity) String() string {
	if id.Latere() {
		return "latere/conversation:" + id.Conversation
	}
	return "user:" + id.Person + "/device:" + id.Device
}

// Credential is the basic credential the identity's leaves carry.
func (id Identity) Credential() mls.Credential { return mls.BasicCredential([]byte(id.String())) }

// Validate reports whether the identity is one a leaf may carry.
func (id Identity) Validate() error {
	if id.Latere() {
		if id.Person != "" || id.Device != "" {
			return errors.New("messaging: Latere's member names no person or device")
		}
		return validID("conversation", id.Conversation)
	}
	if err := validID("person", id.Person); err != nil {
		return err
	}
	return validID("device", id.Device)
}

// DeviceIdentity is the identity of a person's device.
func DeviceIdentity(person, device string) (Identity, error) {
	id := Identity{Person: person, Device: device}
	return id, id.Validate()
}

// LatereIdentity is the identity of Latere's member of a conversation.
func LatereIdentity(conversation string) (Identity, error) {
	id := Identity{Conversation: conversation}
	return id, id.Validate()
}

// ParseCredential returns the identity a leaf's credential names. It
// refuses any credential but a basic one in one of the two forms.
func ParseCredential(c mls.Credential) (Identity, error) {
	if c.Type != mls.CredentialBasic {
		return Identity{}, errors.New("messaging: credential is not a basic credential")
	}
	s := string(c.Identity)
	var id Identity
	if conv, ok := strings.CutPrefix(s, "latere/conversation:"); ok {
		id.Conversation = conv
	} else if rest, ok := strings.CutPrefix(s, "user:"); ok {
		person, device, found := strings.Cut(rest, "/device:")
		if !found {
			return Identity{}, fmt.Errorf("messaging: credential %q names no device", s)
		}
		id.Person, id.Device = person, device
	} else {
		return Identity{}, fmt.Errorf("messaging: credential %q is neither a device nor Latere's member", s)
	}
	if err := id.Validate(); err != nil {
		return Identity{}, err
	}
	// The forms are unambiguous only if parsing gives back the bytes.
	if id.String() != s {
		return Identity{}, fmt.Errorf("messaging: credential %q is not in its one form", s)
	}
	return id, nil
}

// validID accepts 1 to MaxIDBytes of letters, digits and "-._~", the
// characters a URL path segment carries unescaped, which keeps ':' and
// '/' out so an identity parses one way.
func validID(what, s string) error {
	if s == "" || len(s) > MaxIDBytes {
		return fmt.Errorf("messaging: a %s id has 1 to %d bytes", what, MaxIDBytes)
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~') {
			return fmt.Errorf("messaging: a %s id has a character other than letters, digits and -._~", what)
		}
	}
	return nil
}

// NewConversationID returns a fresh random conversation id, which is also
// the group's id: GroupID([]byte(id)).
func NewConversationID() string { return rand.Text() }

// GroupID is the MLS group id of a conversation.
func GroupID(conversation string) []byte { return []byte(conversation) }

// attestationLabel separates the attestation's signature from every other
// use of the installation's key.
const attestationLabel = "latere messaging attestation v1"

// attestationMessage is what the installation signs for Latere's member:
// the label, the group's id and the leaf's signature key.
func attestationMessage(groupID, signatureKey []byte) []byte {
	return encode(func(w *writer) {
		w.vec8([]byte(attestationLabel))
		w.vec32(groupID)
		w.vec32(signatureKey)
	})
}

// Attest signs, with the installation's Latere key, that the leaf with
// signatureKey is Latere's member of the group, and returns the leaf
// extension that carries the signature.
func Attest(installation ed25519.PrivateKey, groupID, signatureKey []byte) mls.Extension {
	return mls.Extension{
		Type: AttestationExtension,
		Data: ed25519.Sign(installation, attestationMessage(groupID, signatureKey)),
	}
}

// CheckLatere checks that a leaf is Latere's member of the group: its
// credential names the conversation the group is, and its attestation
// verifies under the installation's key, which the apps pin.
func CheckLatere(installation ed25519.PublicKey, groupID []byte, m mls.Member) error {
	id, err := ParseCredential(m.Credential)
	if err != nil {
		return err
	}
	if !id.Latere() || !bytes.Equal(GroupID(id.Conversation), groupID) {
		return errors.New("messaging: leaf is not Latere's member of this conversation")
	}
	if len(installation) != ed25519.PublicKeySize {
		return errors.New("messaging: no installation key to check Latere's member against")
	}
	for _, e := range m.Extensions {
		if e.Type == AttestationExtension {
			if !ed25519.Verify(installation, attestationMessage(groupID, m.SignatureKey), e.Data) {
				return errors.New("messaging: Latere's member's attestation does not verify")
			}
			return nil
		}
	}
	return errors.New("messaging: Latere's member carries no attestation")
}
