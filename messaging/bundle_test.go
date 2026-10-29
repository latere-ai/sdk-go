// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package messaging

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	"latere.ai/x/sdk-go/mls"
)

func TestPayload(t *testing.T) {
	p := Payload{ID: "m1", SentAt: time.UnixMilli(1_700_000_000_000).UTC(), Type: PayloadText, Text: "hello"}
	b, err := EncodePayload(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != PaddedSizes[0] {
		t.Fatalf("padded to %d", len(b))
	}
	got, err := ParsePayload(b)
	if err != nil || got != p {
		t.Fatalf("%+v, %v", got, err)
	}
	p.Text = strings.Repeat("x", 5000)
	if b, err = EncodePayload(p); err != nil || len(b) != 16<<10 {
		t.Fatalf("%d, %v", len(b), err)
	}
	p.Text = strings.Repeat("x", MaxPayloadBytes)
	if _, err := EncodePayload(p); err == nil {
		t.Fatal("an oversized payload encoded")
	}

	b, err = EncodePayload(Payload{ID: "m1", SentAt: time.Now(), Type: PayloadText})
	if err != nil {
		t.Fatal(err)
	}
	padded := bytes.Clone(b)
	padded[len(padded)-1] = 1
	for _, bad := range [][]byte{nil, b[:len(b)-1], append(bytes.Clone(b), 0), padded, append([]byte{2}, b[1:]...)} {
		if _, err := ParsePayload(bad); err == nil {
			t.Errorf("%d bytes parsed", len(bad))
		}
	}
	for _, bad := range []Payload{
		{SentAt: time.Now(), Type: PayloadText},
		{ID: "m", Type: PayloadText},
		{ID: "m", SentAt: time.Now(), Type: "poll"},
		{ID: "m", SentAt: time.Now(), Type: PayloadEdit},
		{ID: "m", SentAt: time.Now(), Type: PayloadText, AskedBy: "a b"},
	} {
		if _, err := EncodePayload(bad); err == nil {
			t.Errorf("%+v encoded", bad)
		}
	}
}

// chat is alice and bob in a conversation, with messages from bob that
// alice received.
func chat(t *testing.T, n int, text string) (w *world, alice, bob *device, received []*mls.Message) {
	t.Helper()
	w = newWorld(t)
	alice, bob = w.device("alice", "phone"), w.device("bob", "phone")
	w.create(alice, KindGroup)
	s := state(t, alice.group)
	s.People = append(s.People, "bob")
	if _, err := w.commit(alice, letIn(t, alice, s, bob), nil, bob); err != nil {
		t.Fatal(err)
	}
	for i := range n {
		b, err := EncodePayload(Payload{ID: "m" + string(rune('a'+i%26)) + strings.Repeat("x", i/26), SentAt: time.UnixMilli(int64(1000 * (i + 1))).UTC(), Type: PayloadText, Text: text})
		if err != nil {
			t.Fatal(err)
		}
		msg, err := bob.group.Encrypt(b, nil)
		if err != nil {
			t.Fatal(err)
		}
		m, err := alice.group.Process(msg)
		if err != nil {
			t.Fatal(err)
		}
		received = append(received, m)
	}
	return w, alice, bob, received
}

func (w *world) keyCheck(groupID []byte) KeyCheck {
	return func(sender Identity, key, attestation []byte) error {
		if sender.Latere() {
			return CheckLatere(w.policy().Installation, groupID, mls.Member{Credential: sender.Credential(), SignatureKey: key,
				Extensions: []mls.Extension{{Type: AttestationExtension, Data: attestation}}})
		}
		d, err := w.policy().Directory(sender.Person)
		if err != nil {
			return err
		}
		return d.CheckMember(mls.Member{Credential: sender.Credential(), SignatureKey: key})
	}
}

func TestBundle(t *testing.T) {
	// Enough text that the bundle takes several chunks.
	w, alice, _, received := chat(t, 12, strings.Repeat("y", 20000))
	var entries []Entry
	for _, m := range received {
		e, err := NewEntry(m)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, e)
	}
	sb, err := SealBundle(entries)
	if err != nil {
		t.Fatal(err)
	}
	if len(sb.Ciphertext) < 3*BundleChunkBytes {
		t.Fatalf("bundle of %d bytes", len(sb.Ciphertext))
	}
	got, err := OpenBundle(sb.Ciphertext, sb.Key, sb.Digest)
	if err != nil || len(got) != len(entries) {
		t.Fatalf("%d entries, %v", len(got), err)
	}
	var shared []*SharedMessage
	for i, e := range got {
		m, err := VerifyEntry(e, alice.group.GroupID(), w.keyCheck(alice.group.GroupID()))
		if err != nil {
			t.Fatal(err)
		}
		if m.Sender.Person != "bob" || m.Payload.SentAt.UnixMilli() != int64(1000*(i+1)) {
			t.Fatalf("entry %d: %+v", i, m)
		}
		shared = append(shared, m)
	}

	// The range keeps what it says.
	kept, dropped := HistoryRange{Kind: RangeSinceTime, Since: time.UnixMilli(5000)}.Filter(shared)
	if len(kept) != 8 || dropped != 4 {
		t.Fatalf("since a time kept %d dropped %d", len(kept), dropped)
	}
	kept, dropped = HistoryRange{Kind: RangeSinceMessage, SinceMessage: shared[10].Payload.ID}.Filter(shared)
	if len(kept) != 2 || dropped != 10 {
		t.Fatalf("since a message kept %d dropped %d", len(kept), dropped)
	}

	// Cut short at a chunk, a chunk swapped, a changed byte, the wrong
	// key or digest: none opens.
	n := BundleChunkBytes + gcmTag
	swapped := append(append(bytes.Clone(sb.Ciphertext[n:2*n]), sb.Ciphertext[:n]...), sb.Ciphertext[2*n:]...)
	flipped := bytes.Clone(sb.Ciphertext)
	flipped[7] ^= 1
	for name, c := range map[string][]byte{"cut": sb.Ciphertext[:2*n], "swapped": swapped, "flipped": flipped} {
		sum := sha256Sum(c)
		if _, err := OpenBundle(c, sb.Key, sum); !errors.Is(err, ErrSealed) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := OpenBundle(sb.Ciphertext, sb.Key, make([]byte, 32)); err == nil {
		t.Error("a wrong digest opened")
	}
	if _, err := OpenBundle(sb.Ciphertext, make([]byte, 32), sb.Digest); !errors.Is(err, ErrSealed) {
		t.Errorf("a wrong key: %v", err)
	}
}

func TestVerifyEntryRefuses(t *testing.T) {
	w, alice, _, received := chat(t, 1, "hi")
	e, err := NewEntry(received[0])
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseEntry(e.Bytes())
	if err != nil || !bytes.Equal(back.Bytes(), e.Bytes()) {
		t.Fatalf("entry round trip: %v", err)
	}
	check := w.keyCheck(alice.group.GroupID())
	if _, err := VerifyEntry(e, []byte("other"), check); err == nil {
		t.Fatal("an entry of another group verified")
	}
	// Claimed for another device of the directory: its key is not bob's.
	mallory := w.device("mallory", "phone")
	claimed := e
	claimed.Sender = mallory.id
	if _, err := VerifyEntry(claimed, alice.group.GroupID(), check); err == nil {
		t.Fatal("an entry claimed for another person verified")
	}
	// Mallory's key over bob's content fails the signature.
	resigned := e
	resigned.SignatureKey = mallory.key.Public().(ed25519.PublicKey)
	resigned.Sender = mallory.id
	if _, err := VerifyEntry(resigned, alice.group.GroupID(), check); err == nil {
		t.Fatal("an entry under another key verified")
	}
	changed := e
	changed.Signed = bytes.Clone(e.Signed)
	changed.Signed[len(changed.Signed)-70] ^= 1
	if _, err := VerifyEntry(changed, alice.group.GroupID(), check); err == nil {
		t.Fatal("a changed entry verified")
	}
	if _, err := ParseEntry(append(e.Bytes(), 0)); err == nil {
		t.Fatal("trailing bytes parsed")
	}
}

func TestCommitNotice(t *testing.T) {
	w, alice, bob, received := chat(t, 3, "earlier")
	carol := w.device("carol", "phone")
	var entries []Entry
	for _, m := range received {
		e, err := NewEntry(m)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, e)
	}
	sb, err := SealBundle(entries)
	if err != nil {
		t.Fatal(err)
	}
	rng := HistoryRange{Kind: RangeSinceTime, Since: time.UnixMilli(2000).UTC()}
	jn := JoinerNotice{Range: rng, Bundle: "b1", Digest: sb.Digest, Size: int64(len(sb.Ciphertext)), Key: sb.Key}
	ad, err := NewCommitNotice(alice.group, rng, []Joiner{{KeyPackage: carol.kp, Notice: jn}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewCommitNotice(alice.group, HistoryRange{Kind: RangeAll}, []Joiner{{KeyPackage: carol.kp, Notice: jn}}); err == nil {
		t.Fatal("a joiner with another range than the commit's")
	}
	s := state(t, alice.group)
	s.People = append(s.People, "carol")
	opts := mls.CommitOptions{Add: []*mls.KeyPackage{carol.kp}, ReplaceExtensions: true, Extensions: withState(t, alice.group, s), AuthenticatedData: ad}
	sc, err := alice.group.Commit(opts)
	if err != nil {
		t.Fatal(err)
	}

	// bob reads the range before merging.
	m, err := bob.group.Process(sc.Message())
	if err != nil {
		t.Fatal(err)
	}
	n, err := ParseCommitNotice(m.Commit.Changes().AuthenticatedData)
	if err != nil {
		t.Fatal(err)
	}
	got, err := n.Range(bob.group)
	if err != nil || !got.equal(rng) {
		t.Fatalf("range %+v, %v", got, err)
	}
	if !n.Covers(carol.kp.SignatureKey()) || n.Covers(bob.kp.SignatureKey()) {
		t.Fatal("covers")
	}
	if _, err := w.policy().CheckCommit(bob.group, m.Commit.Changes()); err != nil {
		t.Fatal(err)
	}
	if err := bob.group.Merge(m.Commit); err != nil {
		t.Fatal(err)
	}
	// After the merge the members' part no longer opens.
	if _, err := n.Range(bob.group); err == nil {
		t.Fatal("the range opened in the next epoch")
	}
	if err := alice.group.Merge(sc); err != nil {
		t.Fatal(err)
	}

	// carol joins and opens her part, then the bundle.
	carol.group, err = mls.JoinGroup(sc.Welcome(), carol.kp, carol.secrets, carol.signer, mls.JoinOptions{}, Config())
	if err != nil {
		t.Fatal(err)
	}
	cn, err := n.Open(carol.group, carol.kp, carol.secrets)
	if err != nil {
		t.Fatal(err)
	}
	if !cn.Range.equal(rng) || cn.Bundle != "b1" || cn.Size != int64(len(sb.Ciphertext)) {
		t.Fatalf("joiner notice %+v", cn)
	}
	es, err := OpenBundle(sb.Ciphertext, cn.Key, cn.Digest)
	if err != nil {
		t.Fatal(err)
	}
	var shared []*SharedMessage
	for _, e := range es {
		sm, err := VerifyEntry(e, carol.group.GroupID(), w.keyCheck(carol.group.GroupID()))
		if err != nil {
			t.Fatal(err)
		}
		shared = append(shared, sm)
	}
	if kept, dropped := cn.Range.Filter(shared); len(kept) != 2 || dropped != 1 {
		t.Fatalf("kept %d dropped %d", len(kept), dropped)
	}

	// Another device cannot open carol's part.
	dave := w.device("dave", "phone")
	if _, err := n.Open(carol.group, dave.kp, dave.secrets); err == nil {
		t.Fatal("a part opened for a device it is not for")
	}
	if _, err := ParseCommitNotice(append(bytes.Clone(ad), 0)); err == nil {
		t.Fatal("trailing bytes parsed")
	}
}

func sha256Sum(b []byte) []byte {
	s := sha256.Sum256(b)
	return s[:]
}
