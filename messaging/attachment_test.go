// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package messaging

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"latere.ai/x/sdk-go/attachment"
)

func randomBytes(t testing.TB, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func sealFile(t testing.TB, plain []byte) (*Attachment, []byte) {
	t.Helper()
	var out bytes.Buffer
	a, err := SealAttachment(&out, bytes.NewReader(plain))
	if err != nil {
		t.Fatal(err)
	}
	a.Name, a.MediaType, a.Owner, a.Ref = "f.bin", "application/octet-stream", "alice", "files/messaging/conversations/c/o"
	return a, out.Bytes()
}

func TestAttachmentRoundTrip(t *testing.T) {
	for _, n := range []int{0, 1, ChunkBytes - 1, ChunkBytes, ChunkBytes + 1, 3 * ChunkBytes, 3*ChunkBytes + 17} {
		plain := randomBytes(t, n)
		a, sealed := sealFile(t, plain)
		sum := sha256.Sum256(plain)
		if a.Size != int64(n) || a.Blob != "sha256:"+hex.EncodeToString(sum[:]) || len(a.Key) != StreamKeySize {
			t.Fatalf("%d: %+v", n, a.Attachment)
		}
		if int64(len(sealed)) != SealedSize(int64(n)) {
			t.Fatalf("%d: sealed %d, SealedSize %d", n, len(sealed), SealedSize(int64(n)))
		}
		var out bytes.Buffer
		if err := OpenAttachment(&out, bytes.NewReader(sealed), a); err != nil || !bytes.Equal(out.Bytes(), plain) {
			t.Fatalf("%d: %v", n, err)
		}
		// Each chunk opens alone, at its offset.
		var joined []byte
		for i := int64(0); ChunkOffset(i) < int64(len(sealed)); i++ {
			end := min(ChunkOffset(i+1), int64(len(sealed)))
			p, err := OpenAttachmentChunk(a, i, sealed[ChunkOffset(i):end])
			if err != nil {
				t.Fatalf("%d: chunk %d: %v", n, i, err)
			}
			joined = append(joined, p...)
		}
		if !bytes.Equal(joined, plain) {
			t.Fatalf("%d: the chunks do not join to the file", n)
		}
	}
}

func TestAttachmentRefuses(t *testing.T) {
	plain := randomBytes(t, 3*ChunkBytes)
	a, sealed := sealFile(t, plain)
	c1, c2, c3 := ChunkOffset(1), ChunkOffset(2), ChunkOffset(3)
	flipped := bytes.Clone(sealed)
	flipped[c1+5] ^= 1
	version := bytes.Clone(sealed)
	version[0] = 2
	cases := map[string][]byte{
		// Cut at a chunk boundary: the new last chunk is not marked last.
		"cut":              sealed[:c2],
		"cut mid":          sealed[:c2+100],
		"swapped":          slices.Concat(sealed[:c1], sealed[c2:c3], sealed[c1:c2], sealed[c3:]),
		"extended":         slices.Concat(sealed, sealed[c2:c3]),
		"appended":         append(bytes.Clone(sealed), 0),
		"flipped":          flipped,
		"version":          version,
		"empty":            nil,
		"only the version": sealed[:1],
	}
	for name, c := range cases {
		var out bytes.Buffer
		if err := OpenAttachment(&out, bytes.NewReader(c), a); !errors.Is(err, ErrSealed) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// The wrong key, and the right key with another file's digest or size.
	other := *a
	other.Key = randomBytes(t, StreamKeySize)
	if err := OpenAttachment(io.Discard, bytes.NewReader(sealed), &other); !errors.Is(err, ErrSealed) {
		t.Errorf("another key: %v", err)
	}
	other = *a
	other.Blob = attachment.Digest([]byte("another file"))
	if err := OpenAttachment(io.Discard, bytes.NewReader(sealed), &other); err == nil || errors.Is(err, ErrSealed) {
		t.Errorf("another digest: %v", err)
	}
	other = *a
	other.Size--
	if err := OpenAttachment(io.Discard, bytes.NewReader(sealed), &other); err == nil {
		t.Error("another size opened")
	}
	// A chunk is bound to its position and to whether it is last.
	if _, err := OpenAttachmentChunk(a, 1, sealed[ChunkOffset(0):c1]); !errors.Is(err, ErrSealed) {
		t.Errorf("chunk 0 as chunk 1: %v", err)
	}
	last := *a
	last.Size = 2 * ChunkBytes
	if _, err := OpenAttachmentChunk(&last, 1, sealed[c1:c2]); !errors.Is(err, ErrSealed) {
		t.Errorf("a middle chunk as the last: %v", err)
	}
	// An attachment's key is used for its own stream alone: the same key
	// does not open a history bundle.
	if _, err := openBytes(a.Key, bundleInfo, sealed, MaxBundleBytes); !errors.Is(err, ErrSealed) {
		t.Errorf("as a bundle: %v", err)
	}
}

func TestSealAttachmentLimit(t *testing.T) {
	// A reader that never ends stops at the limit.
	if _, _, err := sealStream(io.Discard, infinite{}, randomBytes(t, StreamKeySize), attachmentInfo, 3*ChunkBytes); err == nil {
		t.Fatal("sealed past the limit")
	}
	if _, _, err := sealStream(io.Discard, bytes.NewReader(make([]byte, 3*ChunkBytes)), randomBytes(t, StreamKeySize), attachmentInfo, 3*ChunkBytes); err != nil {
		t.Fatalf("at the limit: %v", err)
	}
	a := Attachment{Attachment: attachment.Attachment{Name: "big", Size: MaxAttachmentBytes + 1, Blob: attachment.Digest(nil)}, Key: make([]byte, StreamKeySize)}
	if err := OpenAttachment(io.Discard, strings.NewReader(""), &a); err == nil {
		t.Fatal("an attachment over the limit opened")
	}
}

type infinite struct{}

func (infinite) Read(p []byte) (int, error) { return len(p), nil }

// TestStreamVector pins the construction: the key 00..1f, the plaintext
// "latere", one chunk.
func TestStreamVector(t *testing.T) {
	key := make([]byte, StreamKeySize)
	for i := range key {
		key[i] = byte(i)
	}
	sealed, err := sealBytes(key, attachmentInfo, []byte("latere"), MaxAttachmentBytes)
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(sealed); got != attachmentVector {
		t.Fatalf("sealed %s, want %s", got, attachmentVector)
	}
}

func TestPayloadAttachments(t *testing.T) {
	a, _ := sealFile(t, []byte("a picture"))
	a.MediaType = "image/png"
	a.Preview = &attachment.Preview{MediaType: "image/jpeg", Data: randomBytes(t, 2000), Width: 640, Height: 480}
	p := Payload{ID: "m1", SentAt: time.UnixMilli(1_700_000_000_000).UTC(), Type: PayloadText, Attachments: []Attachment{*a}}
	b, err := EncodePayload(p)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParsePayload(b)
	if err != nil || !reflect.DeepEqual(got, p) {
		t.Fatalf("%+v, %v", got, err)
	}
	// Every file of the most a message carries, each with the largest
	// preview, still fits a payload.
	a.Preview.Data = randomBytes(t, MaxPreviewBytes)
	a.Preview.Waveform = randomBytes(t, MaxWaveformBytes)
	full := p
	full.Text = strings.Repeat("x", 4000)
	full.Attachments = slices.Repeat([]Attachment{*a}, MaxAttachments)
	if _, err := EncodePayload(full); err != nil {
		t.Fatalf("the most files a message carries: %v", err)
	}

	bad := func(name string, f func(p *Payload)) {
		t.Helper()
		q := p
		q.Attachments = []Attachment{*a}
		q.Attachments[0].Preview = &attachment.Preview{}
		f(&q)
		if _, err := EncodePayload(q); err == nil {
			t.Errorf("%s encoded", name)
		}
	}
	bad("too many", func(p *Payload) { p.Attachments = slices.Repeat(p.Attachments, MaxAttachments+1) })
	bad("a short key", func(p *Payload) { p.Attachments[0].Key = p.Attachments[0].Key[:16] })
	bad("no ref", func(p *Payload) { p.Attachments[0].Ref = "" })
	bad("no owner", func(p *Payload) { p.Attachments[0].Owner = "" })
	bad("a path as a name", func(p *Payload) { p.Attachments[0].Name = "../etc/passwd" })
	bad("no digest", func(p *Payload) { p.Attachments[0].Blob = "" })
	bad("too large", func(p *Payload) { p.Attachments[0].Size = MaxAttachmentBytes + 1 })
	bad("a large preview", func(p *Payload) { p.Attachments[0].Preview.Data = make([]byte, MaxPreviewBytes+1) })
	bad("on a reaction", func(p *Payload) { p.Type, p.Ref, p.Reaction = PayloadReaction, "m0", "+1" })
}

// attachmentVector was computed apart from this package, with Python's
// cryptography: HKDF-SHA256 of the key with no salt and the attachment
// info, then AES-256-GCM with the last-chunk nonce and the version byte
// as additional data.
const attachmentVector = "018299b32aee517f7ba30761a89720927c30dca8d394aa"
