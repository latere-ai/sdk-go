// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package mls implements Messaging Layer Security, RFC 9420: end-to-end
// encryption for groups, in which every member's device holds the keys
// and a server delivers what it cannot read. It is written on the Go
// standard library alone and passes the interoperability test vectors of
// the IETF MLS working group for every suite it implements.
//
// # Suites
//
// The package implements the suites the standard library has every
// primitive for: MLS_128_DHKEMX25519_AES128GCM_SHA256_Ed25519, the suite
// every implementation supports, and the three NIST suites
// MLS_128_DHKEMP256_AES128GCM_SHA256_P256,
// MLS_256_DHKEMP384_AES256GCM_SHA384_P384 and
// MLS_256_DHKEMP521_AES256GCM_SHA512_P521. The ChaCha20-Poly1305 and
// X448 suites need primitives the standard library does not export.
//
// # A member's group
//
// Each device is one member, with its own SignatureKey. A device makes a
// KeyPackage for others to add it with, keeps its KeyPackageSecrets, and
// joins with the Welcome that comes back:
//
//	signer, err := mls.GenerateSignatureKey(mls.MLS_128_DHKEMX25519_AES128GCM_SHA256_Ed25519)
//	kp, secrets, err := mls.NewKeyPackage(signer, mls.BasicCredential(deviceID), mls.KeyPackageOptions{})
//	// ... publish kp.Bytes(); later, a Welcome arrives:
//	g, err := mls.JoinGroup(welcome, kp, secrets, signer, mls.JoinOptions{}, cfg)
//
// CreateGroup starts a group with its creator alone. Commit changes the
// group, adding and removing members, and stages the change: send
// StagedCommit.Message and StagedCommit.Welcome, and Merge once the
// delivery service accepts the commit. Another member's commit comes in
// through Process, staged the same way, so the application inspects its
// Changes, such as who it adds, and rejects it by not merging. Nothing
// changes before Merge, and the delivery service must hand every member
// one commit per epoch, in the same order.
//
// Encrypt protects an application message and Process opens one. A
// message carries its sender's signature; Message.Signed keeps it with
// the content, so the message can be shared with a member who joins
// later and checked by VerifySignedContent.
//
// JoinExternal joins from a group info with the group's external public
// key, without a member online. PublicGroup follows a group from outside
// it, as a delivery service does, checking the signatures and tree
// changes of public handshake messages while holding no key.
//
// MarshalBinary encodes a member's state, private keys included, to
// store between runs; UnmarshalGroup restores it. Store the newest copy
// only: a key the current state has deleted stays in an old copy.
//
// # What the application decides
//
// The package checks what RFC 9420 requires. Who may join, who may add
// whom and what a credential means are the application's: Config's
// CheckCredential vets every credential the group takes in,
// CheckSuccessor whether a leaf may change its credential, and
// AcceptProposal which proposals may wait for a commit, refusing by
// default those from outside the group but an external sender's
// removal. A staged commit's Changes say what it does, and who proposed
// each part, before it is merged; check a commit this member made the
// same way before sending it. A credential's identity is opaque bytes
// here; binding it to a person is the application's.
//
// Not implemented: sending Update proposals (a commit renews the
// committer's keys instead), reinitializing or branching a group (a
// committed ReInit closes it), external commits that replace a member's
// earlier leaf, and the validation of X.509 credentials, which the
// application does in CheckCredential.
package mls
