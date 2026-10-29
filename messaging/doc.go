// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package messaging holds what every device of a conversation between
// people agrees on, over the end-to-end encryption of package mls: who
// a leaf is, the conversation's state in its group, the rules each
// commit is checked against, the form a message takes, and the keys
// that restore a person's history.
//
// A conversation is one MLS group in the cipher suite Suite, whose id is
// the conversation's id. Each device of each person is a leaf, with a
// credential naming the person and the device and the device's own
// signature key, and Latere's member is one more leaf while Latere reads
// along or takes part. The group context carries the conversation's
// State: its people in the order they joined, its owners and its level.
//
// # Devices and the directory
//
// A person's devices are in the directory as DeviceRecords, each
// vouched for by another of the same person, the first device for
// itself and later ones by an approved device or by the recovery key,
// and each signed by its own key over the voucher it names. A key the
// person no longer holds is revoked by a Revocation a record under the
// same root signs. NewDirectory keeps the records whose vouches chain to
// a root through keys no one revoked, and a root an app has not pinned
// for a person is a changed security code. The apps keep the
// revocations a directory counted (Directory.Revocations) and pass them
// back as known, which apply whatever the service lists later, so the
// service can neither undo a revocation nor keep it from a device that
// saw it. The service keeps a revocation only when
// Directory.CheckRevocation says it changes something.
//
// # Commits
//
// A device checks every commit before merging it, with
// Policy.CheckCommit, and the service checks the same before accepting
// one; Policy.CheckProposal and CheckMessage check what arrives between
// commits, and a group made with Policy.Config keeps no proposal pending
// that the rules refuse. The service proposes nothing but the removal of
// a device its person revoked. A device takes a group it joins, by a
// Welcome or on its own, only once Policy.CheckJoin passes on it, which
// Policy.JoinGroup and Policy.JoinExternal do, and the service checks a
// new conversation's group the same way. The application merges a
// commit only if the check passes:
//
//	msg, err := g.Process(b)
//	if err != nil {
//		return err
//	}
//	if msg.Kind == mls.MessageCommit {
//		change, err := policy.CheckCommit(g, msg.Commit.Changes())
//		if err != nil {
//			return err // refused: not merged
//		}
//		if err := g.Merge(msg.Commit); err != nil {
//			return err
//		}
//		draw(change)
//	}
//
// # Messages and history
//
// What a person writes is a Payload, padded to one of PaddedSizes and
// sent with mls.Group.Encrypt. A message leaves its group as an Entry,
// which verifies without the group: into a history share for someone
// let in, sealed by SealBundle and announced by the commit's notice
// (NewCommitNotice, with WelcomeExtension in the commit's Welcome), into
// the person's backup, sealed under their BackupKey, or into a report.
// A new device of the person takes the backup key from the device that
// approved it, sealed to it and signed (BackupKey.SealTo).
//
// # Files
//
// A message carries any file as an Attachment. SealAttachment encrypts
// the file under a key of its own as a stream of authenticated chunks,
// the ciphertext is uploaded to Storage, and the payload carries where it
// is, the key, the file's digest and size, its name, its media type and a
// small preview. OpenAttachment decrypts it and checks the digest, and
// OpenAttachmentChunk decrypts one chunk for a reader that seeks. History
// bundles are sealed as the same stream.
//
// # The recovery key
//
// A RecoveryKey is 128 random bits the person keeps, written as 28
// characters. It derives the key the backup key is wrapped under and the
// signing key that vouches for a new device; the service never holds it.
//
// # Limits
//
// What this package does not yet prove, so the apps say no more than it
// does:
//
//   - An Entry proves who signed a message, not that it was sent to the
//     conversation: VerifyEntry checks the signature against the group
//     context the entry carries, so a member can date their own entries
//     as they like, and a sharer can put in a bundle entries signed in
//     another group under the conversation's id. A shared message is
//     shown as its sender's, not as proof of the conversation's past.
//   - The messages a device sent before its key was revoked stop
//     verifying when the KeyCheck refuses a key no record stands for.
//   - The directory is as trustworthy as the roots a person's contacts
//     pinned. The service can list a new root for a person, whose
//     devices then join on their own; the only sign is a changed
//     security code. Such a root revokes nothing under the person's
//     roots and approves no backup key, and a new root of the person's
//     own reaches none of their old devices either: the recovery key,
//     which vouches under the old root while it stands, is the way back
//     into it.
//   - A revocation the service withholds from a device that never saw
//     it, a new contact's, does not apply there until it is listed.
//   - A group a person of the conversation makes under its id, with the
//     service handing it out, is joined like the conversation's own:
//     nothing in a group tells the two apart. Latere's member makes no
//     group a device takes, since a device refuses a group info it
//     signed; a device joining on its own after Latere's member updated
//     its leaf waits for a person's commit, or is added by another of
//     its person's devices.
//   - A member removed since an epoch, Latere's among them, may keep
//     that epoch's secrets, and the service may keep its group info. A
//     device that joins on its own from it is read by that member, so
//     Policy.JoinExternal takes the conversation and its epoch the
//     approving device hands over the pairing, and refuses a group info
//     of another conversation or an earlier epoch; with no epoch given,
//     the conversation's group info of any epoch is taken. A Welcome of an
//     old epoch reaches a device only through a key package it uses
//     twice, its last resort.
//   - The revocations an app keeps grow with what it was shown: one for
//     each key revoked and one for each key a later revocation stops
//     keeping, past MaxRevocations when a service shows a device more
//     records over time than it lists at once.
//   - The service checks a commit's signature and rules, not its
//     membership or confirmation tag, which only members can compute. A
//     member that sends a commit with a wrong tag has it taken as the
//     epoch's and refused by every other member, which stops the
//     conversation; the commit names its sender, and the owners start a
//     new conversation without them.
//   - A chunk opened alone is checked against the file's key, not its
//     digest, so a sender can seal for a seeking reader other bytes than
//     the whole file opens to; OpenAttachment checks the digest.
//   - A revoked device keeps the backup key it was given. The backup key
//     does not change on a revocation, so a revoked device that can still
//     read the person's Storage reads their backup; removing a device
//     also ends its sign-in, and Storage decides every read.
package messaging
