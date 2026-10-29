// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package messaging

import (
	"crypto/ed25519"
	"slices"
	"time"

	"latere.ai/x/sdk-go/mls"
)

// MaxKeyPackageLifetime bounds how long a key package a conversation's
// group takes in may be valid for.
const MaxKeyPackageLifetime = 90 * 24 * time.Hour

// extensionTypes are the extension types every leaf of a conversation's
// group supports, which its required capabilities name.
var extensionTypes = []mls.ExtensionType{StateExtension, AttestationExtension}

// Config is the configuration of a conversation's group: handshake
// messages in the clear, signed, so the service checks every commit as
// the devices do; every credential a device's or Latere's member's; no
// proposal pending that the rules refuse from its sender, as far as the
// conversation's state decides it; no key package valid for longer than
// MaxKeyPackageLifetime. Policy.Config also checks a proposal's leaves
// against the directory.
func Config() mls.Config {
	return mls.Config{
		PublicHandshake: true,
		CheckCredential: func(c mls.Credential, _ []byte) error {
			_, err := ParseCredential(c)
			return err
		},
		AcceptProposal: func(g mls.GroupState, pr mls.Proposal) error { return checkProposal(g, pr, nil) },
		MaxLifetime:    MaxKeyPackageLifetime,
	}
}

// Config is the configuration of a conversation's group for a device or
// the service that checks with the policy: Config, with every proposal
// that arrives checked by CheckProposal, so none is pending that a
// commit carrying it would break a rule with.
func (p *Policy) Config() mls.Config {
	c := Config()
	c.AcceptProposal = func(g mls.GroupState, pr mls.Proposal) error { return checkProposal(g, pr, p) }
	return c
}

// CreateOptions are the options a conversation's group is created with:
// its state, the required capabilities that keep out a device that does
// not understand it, and the service as the external sender that
// proposes removals.
func CreateOptions(s State, service mls.ExternalSender) (mls.CreateOptions, error) {
	st, err := s.Extension()
	if err != nil {
		return mls.CreateOptions{}, err
	}
	return mls.CreateOptions{
		Extensions: []mls.Extension{
			mls.RequiredCapabilitiesExtension(extensionTypes, nil, nil),
			mls.ExternalSendersExtension([]mls.ExternalSender{service}),
			st,
		},
		SupportedExtensions: slices.Clone(extensionTypes),
	}, nil
}

// KeyPackageOptions are the options a device's key packages are made
// with, so that they meet every conversation's required capabilities.
func KeyPackageOptions() mls.KeyPackageOptions {
	return mls.KeyPackageOptions{Extensions: slices.Clone(extensionTypes)}
}

// LatereKeyPackageOptions are the options of the key package of
// Latere's member of a conversation, with the attestation the
// installation signs for the leaf's signature key.
func LatereKeyPackageOptions(installation ed25519.PrivateKey, conversation string, signatureKey []byte) mls.KeyPackageOptions {
	o := KeyPackageOptions()
	o.LeafExtensions = []mls.Extension{Attest(installation, GroupID(conversation), signatureKey)}
	return o
}

// CheckKeyPackage checks a key package the service handed out before it
// is added, the suite of conversations and a credential of one of the two
// forms, and returns who it is, for the caller to compare with whom it
// was claimed for. Policy.CheckCommit checks the leaf against the
// directory.
func CheckKeyPackage(kp *mls.KeyPackage) (Identity, error) {
	if err := CheckSuite(kp.CipherSuite()); err != nil {
		return Identity{}, err
	}
	return ParseCredential(kp.Credential())
}
