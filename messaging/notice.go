// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package messaging

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"latere.ai/x/sdk-go/mls"
)

// Whoever lets someone in chooses how much of the earlier conversation
// they get, and the commit that adds them says so in its authenticated
// data, which its signature covers, as a CommitNotice. The notice has a
// commitment to the range and two parts. For the members, the range
// chosen, sealed under the exporter of the epoch the commit leaves, so
// every member draws the line and the service reads nothing. For each
// joining leaf, the range, where the history bundle is, its digest and
// size and the key it is sealed under, sealed to the init key of the key
// package the commit adds, so only that device opens it. Both parts carry
// the commitment's salt, and each side checks its range against the
// commitment, so the members and the joiners read one range. A joiner
// never sees the commit itself, so the commit's Welcome carries the
// notice's digest in its group info (WelcomeExtension), which the
// committer signs, and the joiner takes no notice of another digest.

// RangeKind is how much earlier history a joiner gets.
type RangeKind uint8

// The kinds of range.
const (
	// RangeNone shares no earlier message.
	RangeNone RangeKind = 0
	// RangeAll shares every earlier message.
	RangeAll RangeKind = 1
	// RangeSinceTime shares the messages sent since HistoryRange.Since.
	RangeSinceTime RangeKind = 2
	// RangeSinceMessage shares HistoryRange.SinceMessage and every
	// message after it.
	RangeSinceMessage RangeKind = 3
)

// HistoryRange is the earlier history a joiner gets.
type HistoryRange struct {
	Kind RangeKind
	// Since is the time a RangeSinceTime starts at, to the millisecond.
	Since time.Time
	// SinceMessage is the id of the message a RangeSinceMessage starts
	// with.
	SinceMessage string
}

func (h HistoryRange) validate() error {
	switch h.Kind {
	case RangeNone, RangeAll:
		if !h.Since.IsZero() || h.SinceMessage != "" {
			return errors.New("messaging: a range of none or all has no start")
		}
	case RangeSinceTime:
		if h.Since.IsZero() || h.SinceMessage != "" {
			return errors.New("messaging: a range since a time has its time alone")
		}
	case RangeSinceMessage:
		if !h.Since.IsZero() {
			return errors.New("messaging: a range since a message has no time")
		}
		return validID("message", h.SinceMessage)
	default:
		return fmt.Errorf("messaging: unknown range %d", uint8(h.Kind))
	}
	return nil
}

// equal compares two ranges as they are encoded, to the millisecond.
func (h HistoryRange) equal(o HistoryRange) bool {
	return h.Kind == o.Kind && h.Since.UnixMilli() == o.Since.UnixMilli() && h.Since.IsZero() == o.Since.IsZero() &&
		h.SinceMessage == o.SinceMessage
}

func (h HistoryRange) marshal(w *writer) {
	w.u8(uint8(h.Kind))
	var ms int64
	if !h.Since.IsZero() {
		ms = h.Since.UnixMilli()
	}
	w.u64(uint64(ms))
	w.vec8([]byte(h.SinceMessage))
}

func (h *HistoryRange) unmarshal(r *reader) {
	h.Kind = RangeKind(r.u8())
	if ms := int64(r.u64()); ms != 0 {
		h.Since = time.UnixMilli(ms).UTC()
		if h.Since.IsZero() {
			r.fail("a start of the zero time, which is written as 0")
		}
	}
	h.SinceMessage = string(r.vec8())
}

// Filter keeps the messages of a history share that fall in the range,
// in the order given, and counts those it drops. Under RangeSinceMessage
// the share starts at the named message, so everything before it is
// dropped.
func (h HistoryRange) Filter(msgs []*SharedMessage) (kept []*SharedMessage, dropped int) {
	started := false
	for _, m := range msgs {
		in := false
		switch h.Kind {
		case RangeAll:
			in = true
		case RangeSinceTime:
			in = !m.Payload.SentAt.Before(h.Since)
		case RangeSinceMessage:
			started = started || m.Payload.ID == h.SinceMessage
			in = started
		}
		if in {
			kept = append(kept, m)
		} else {
			dropped++
		}
	}
	return kept, dropped
}

// JoinerNotice is what a joining leaf learns of its history share.
type JoinerNotice struct {
	Range HistoryRange
	// Owner and Ref are where the sealed bundle is: whose Storage space,
	// as the storage package names it, and its path there. Both are
	// empty when the range shares nothing.
	Owner string
	Ref   string
	// Digest and Size are the bundle's SHA-256 and length.
	Digest []byte
	Size   int64
	// Key is the key the bundle is sealed under.
	Key []byte
}

func (j *JoinerNotice) validate() error {
	if err := j.Range.validate(); err != nil {
		return err
	}
	if j.Range.Kind == RangeNone {
		if j.Owner != "" || j.Ref != "" || j.Digest != nil || j.Size != 0 || j.Key != nil {
			return errors.New("messaging: a notice that shares nothing has no bundle")
		}
		return nil
	}
	if err := validPlace(j.Owner, j.Ref); err != nil {
		return err
	}
	if len(j.Digest) != sha256.Size || len(j.Key) != BundleKeySize || j.Size <= 0 || j.Size > MaxBundleBytes {
		return errors.New("messaging: a notice's bundle has a digest, a size and a key")
	}
	return nil
}

// The labels and versions of the notice.
const (
	noticeVersion = 1
	// noticeExporterLabel is the exporter label of the members' key.
	noticeExporterLabel = "latere messaging history notice v1"
	// HistoryLabel is the EncryptWithLabel label a joiner's part of a
	// notice is sealed under.
	HistoryLabel = "conversation history"
	// commitmentLabel and noticeDigestLabel separate the range's
	// commitment and the notice's digest from every other hash.
	commitmentLabel   = "latere messaging history range commitment v1"
	noticeDigestLabel = "latere messaging history notice digest v1"
	saltSize          = 32
)

// joinerContext is the context a joiner's part is sealed under: the
// group and the epoch the commit starts.
func joinerContext(groupID []byte, epoch uint64) []byte {
	return encode(func(w *writer) {
		w.vec32(groupID)
		w.u64(epoch)
	})
}

func membersAAD(groupID []byte, epoch uint64) []byte {
	return encode(func(w *writer) {
		w.vec32([]byte(noticeExporterLabel))
		w.vec32(groupID)
		w.u64(epoch)
	})
}

func commitment(rng HistoryRange, salt []byte) []byte {
	h := sha256.New()
	h.Write(encode(func(w *writer) {
		w.vec8([]byte(commitmentLabel))
		rng.marshal(w)
		w.vec8(salt)
	}))
	return h.Sum(nil)
}

// WelcomeExtension is the extension the commit that carries a notice
// puts in its Welcome, mls.CommitOptions.WelcomeExtensions, for the
// joiners to check the notice they are handed against.
func WelcomeExtension(notice []byte) mls.Extension {
	h := sha256.New()
	h.Write([]byte(noticeDigestLabel))
	h.Write(notice)
	return mls.Extension{Type: NoticeExtension, Data: h.Sum(nil)}
}

// Joiner is one leaf a commit adds, with what its notice says.
type Joiner struct {
	KeyPackage *mls.KeyPackage
	Notice     JoinerNotice
}

// NewCommitNotice makes the notice of a commit g is about to make, to
// pass as mls.CommitOptions.AuthenticatedData, with WelcomeExtension of
// it in mls.CommitOptions.WelcomeExtensions. The members' part says rng,
// and so does each joiner's part, sealed to its key package: one commit
// lets everyone it adds in with one range, so the line every member
// draws is true of each of them.
func NewCommitNotice(g *mls.Group, rng HistoryRange, joiners []Joiner) ([]byte, error) {
	if err := rng.validate(); err != nil {
		return nil, err
	}
	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	key, err := g.Export(noticeExporterLabel, nil, 32)
	if err != nil {
		return nil, err
	}
	members, err := seal(key, encode(func(w *writer) {
		rng.marshal(w)
		w.vec8(salt)
	}), membersAAD(g.GroupID(), g.Epoch()))
	if err != nil {
		return nil, err
	}
	ctx := joinerContext(g.GroupID(), g.Epoch()+1)
	type part struct{ signatureKey, sealed []byte }
	parts := make([]part, 0, len(joiners))
	seen := make(map[string]bool)
	for _, j := range joiners {
		if err := j.Notice.validate(); err != nil {
			return nil, err
		}
		if !j.Notice.Range.equal(rng) {
			return nil, errors.New("messaging: every joiner of a commit gets the commit's range")
		}
		sk := j.KeyPackage.SignatureKey()
		if seen[string(sk)] {
			return nil, errors.New("messaging: two joiners of one commit share a key")
		}
		seen[string(sk)] = true
		plain := encode(func(w *writer) {
			j.Notice.Range.marshal(w)
			w.vec8(salt)
			w.vec8([]byte(j.Notice.Owner))
			w.vec32([]byte(j.Notice.Ref))
			w.vec8(j.Notice.Digest)
			w.u64(uint64(j.Notice.Size))
			w.vec8(j.Notice.Key)
		})
		sealed, err := j.KeyPackage.Seal(HistoryLabel, ctx, plain)
		if err != nil {
			return nil, err
		}
		parts = append(parts, part{sk, sealed})
	}
	return encode(func(w *writer) {
		w.u8(noticeVersion)
		w.vec8(commitment(rng, salt))
		w.vec32(members)
		w.u16(uint16(len(parts)))
		for _, p := range parts {
			w.vec32(p.signatureKey)
			w.vec32(p.sealed)
		}
	}), nil
}

// CommitNotice is a commit's notice, decoded but not opened.
type CommitNotice struct {
	raw        []byte
	commitment []byte
	members    []byte
	joiners    map[string][]byte
}

// ParseCommitNotice decodes a commit's authenticated data as a notice.
// It does not open either part; the service checks with it that every
// joining leaf has one.
func ParseCommitNotice(ad []byte) (*CommitNotice, error) {
	r := &reader{b: ad}
	if v := r.u8(); r.err == nil && v != noticeVersion {
		return nil, fmt.Errorf("%w: notice version %d", errFormat, v)
	}
	n := &CommitNotice{raw: bytes.Clone(ad), commitment: r.vec8(), members: r.vec32(), joiners: make(map[string][]byte)}
	if r.err == nil && len(n.commitment) != sha256.Size {
		r.fail("a commitment of %d bytes", len(n.commitment))
	}
	count := int(r.u16())
	for i := 0; i < count && r.err == nil; i++ {
		sk, sealed := r.vec32(), r.vec32()
		if r.err != nil {
			break
		}
		if _, dup := n.joiners[string(sk)]; dup {
			r.fail("two parts for one leaf")
			break
		}
		n.joiners[string(sk)] = sealed
	}
	if err := r.done(); err != nil {
		return nil, err
	}
	return n, nil
}

// Covers reports whether the notice has a part for the leaf with the
// signature key.
func (n *CommitNotice) Covers(signatureKey []byte) bool {
	_, ok := n.joiners[string(signatureKey)]
	return ok
}

// readRange reads a range and its salt and checks them against the
// commitment.
func (n *CommitNotice) readRange(r *reader) (HistoryRange, error) {
	var h HistoryRange
	h.unmarshal(r)
	salt := r.vec8()
	if r.err != nil {
		return HistoryRange{}, r.err
	}
	if err := h.validate(); err != nil {
		return HistoryRange{}, err
	}
	if len(salt) != saltSize || !hmac.Equal(commitment(h, salt), n.commitment) {
		return HistoryRange{}, errors.New("messaging: the notice's range is not the one its commit committed to")
	}
	return h, nil
}

// Range opens the members' part with the group the commit came to, while
// it is still at the epoch the commit leaves: before mls.Group.Merge.
func (n *CommitNotice) Range(g *mls.Group) (HistoryRange, error) {
	key, err := g.Export(noticeExporterLabel, nil, 32)
	if err != nil {
		return HistoryRange{}, err
	}
	plain, err := open(key, n.members, membersAAD(g.GroupID(), g.Epoch()))
	if err != nil {
		return HistoryRange{}, err
	}
	r := &reader{b: plain}
	h, err := n.readRange(r)
	if err != nil {
		return HistoryRange{}, err
	}
	if err := r.done(); err != nil {
		return HistoryRange{}, err
	}
	return h, nil
}

// Open opens the joiner's part for the device that joined with kp, with
// the group it joined: after mls.JoinGroup, before the group is saved and
// the key package's secrets are deleted. The notice must be the one
// whose digest the Welcome carries.
func (n *CommitNotice) Open(g *mls.Group, kp *mls.KeyPackage, secrets *mls.KeyPackageSecrets) (*JoinerNotice, error) {
	want := WelcomeExtension(n.raw)
	carried := false
	for _, e := range g.WelcomeExtensions() {
		if e.Type == NoticeExtension {
			carried = hmac.Equal(e.Data, want.Data)
		}
	}
	if !carried {
		return nil, errors.New("messaging: the notice is not the one the Welcome names")
	}
	sealed, ok := n.joiners[string(kp.SignatureKey())]
	if !ok {
		return nil, errors.New("messaging: the notice has no part for this device")
	}
	plain, err := secrets.Open(kp, HistoryLabel, joinerContext(g.GroupID(), g.Epoch()), sealed)
	if err != nil {
		return nil, err
	}
	r := &reader{b: plain}
	var j JoinerNotice
	if j.Range, err = n.readRange(r); err != nil {
		return nil, err
	}
	j.Owner = string(r.vec8())
	j.Ref = string(r.vec32())
	if d := r.vec8(); len(d) > 0 {
		j.Digest = bytes.Clone(d)
	}
	size := r.u64()
	if k := r.vec8(); len(k) > 0 {
		j.Key = bytes.Clone(k)
	}
	if err := r.done(); err != nil {
		return nil, err
	}
	if size > MaxBundleBytes {
		return nil, errors.New("messaging: a notice's bundle is over the limit")
	}
	j.Size = int64(size)
	if err := j.validate(); err != nil {
		return nil, err
	}
	return &j, nil
}

// MaxRefBytes is the longest path of a file in Storage that a message
// or notice names.
const MaxRefBytes = 1024

// Folder is the folder of a person's Storage space that holds the
// ciphertext of what their conversations carry: attachments, history
// bundles and the backup. A message or notice names a file under it and
// nowhere else.
const Folder = "files/messaging"

// validPlace checks where a file is said to be: a space's owner, an id
// and never "me", which each reader would read as their own space, and
// a path under Folder of at most MaxRefBytes with no empty, "." or ".."
// segment and no control character.
func validPlace(owner, ref string) error {
	if err := validID("space owner", owner); err != nil {
		return err
	}
	if owner == "me" {
		return errors.New(`messaging: a file's space is named by its owner, never "me"`)
	}
	if len(ref) > MaxRefBytes || !strings.HasPrefix(ref, Folder+"/") || !utf8.ValidString(ref) {
		return fmt.Errorf("messaging: a file is named by its path under %s", Folder)
	}
	for seg := range strings.SplitSeq(ref, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return errors.New("messaging: a file's path has no empty, . or .. segment")
		}
	}
	if strings.ContainsFunc(ref, func(r rune) bool { return unicode.In(r, unicode.Cc, unicode.Cf, unicode.Zl, unicode.Zp) }) {
		return errors.New("messaging: a file's path holds no control or format character")
	}
	return nil
}
