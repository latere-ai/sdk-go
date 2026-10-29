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
// vouched for by another of the same person: the first device for
// itself, later ones by an approved device or by the recovery key.
// NewDirectory keeps the records whose vouches chain to a root, and a
// root an app has not pinned for a person is a changed security code.
//
// # Commits
//
// A device checks every commit before merging it, with
// Policy.CheckCommit, and the service checks the same before accepting
// one; CheckProposal and CheckMessage check what arrives between
// commits. The application merges a commit only if the check passes:
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
// (NewCommitNotice), into the person's backup, sealed under their
// BackupKey, or into a report.
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
package messaging
