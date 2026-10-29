// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package attachment is the file a message carries, the one type that a
// session's messages (package agents) and a conversation between people
// (package messaging) build on, so an app draws a file the same way in
// both.
//
// It holds what an app needs to draw a file before its bytes are read:
// its name, media type and size, the digest of its bytes, where the
// bytes are kept, and a preview. It sends nothing and imports the
// standard library alone.
package attachment

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"strings"
	"unicode/utf8"
)

// MaxNameBytes is the longest name of a file, in bytes.
const MaxNameBytes = 255

// Attachment is one file a message carries.
type Attachment struct {
	// Name is the file's name, one path segment of at most MaxNameBytes.
	Name string `json:"name"`
	// MediaType is the file's media type, such as "image/png"; it decides
	// only how the file is drawn.
	MediaType string `json:"media_type"`
	// Size is the file's length in bytes.
	Size int64 `json:"size"`
	// Blob is the digest of the file's bytes, "sha256:" and the hex of
	// their hash, as Digest writes it.
	Blob string `json:"blob"`
	// Ref is where the bytes are kept, as the kind of message that
	// carries the file says: a path inside a session's folder for a
	// session's message, the path of the encrypted copy in Storage for a
	// conversation between people.
	Ref string `json:"ref,omitempty"`
	// Preview is what draws the file before its bytes are read, when the
	// sender made one.
	Preview *Preview `json:"preview,omitempty"`
}

// Preview is a small stand-in for a file: a thumbnail of a picture, the
// poster frame of a video, the first page of a document, or the waveform
// of a voice note.
type Preview struct {
	// MediaType is the type of Data, an image type.
	MediaType string `json:"media_type,omitempty"`
	// Data is the preview image's bytes.
	Data []byte `json:"data,omitempty"`
	// Width and Height are the file's own dimensions in pixels, for a
	// picture, a video or a page, so the app lays it out before it loads.
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
	// DurationMillis is the length of a video or of audio.
	DurationMillis int64 `json:"duration_ms,omitempty"`
	// Waveform is the loudness of a voice note, one byte per step from
	// silence at 0 to the loudest at 255.
	Waveform []byte `json:"waveform,omitempty"`
	// Pages is a document's number of pages.
	Pages int `json:"pages,omitempty"`
}

// Digest is the Blob of a file's bytes.
func Digest(b []byte) string {
	sum := sha256.Sum256(b)
	return Sum(sum[:])
}

// NewHash is the hash Blob is written from, for bytes read as a stream:
// Sum(h.Sum(nil)) is their Blob.
func NewHash() hash.Hash { return sha256.New() }

// Sum writes a SHA-256 sum as a Blob.
func Sum(sum []byte) string { return "sha256:" + hex.EncodeToString(sum) }

// ParseBlob reads the SHA-256 sum of a Blob.
func ParseBlob(blob string) ([]byte, error) {
	hexSum, ok := strings.CutPrefix(blob, "sha256:")
	if !ok {
		return nil, fmt.Errorf("attachment: blob %q is not a sha256 digest", blob)
	}
	sum, err := hex.DecodeString(hexSum)
	if err != nil || len(sum) != sha256.Size || hexSum != strings.ToLower(hexSum) {
		return nil, fmt.Errorf("attachment: blob %q is not a sha256 digest", blob)
	}
	return sum, nil
}

// Validate checks what every attachment holds: a name that is one path
// segment, a size that is not negative, and a blob that is a digest.
func (a *Attachment) Validate() error {
	if err := ValidName(a.Name); err != nil {
		return err
	}
	if a.Size < 0 {
		return errors.New("attachment: a size is not negative")
	}
	if _, err := ParseBlob(a.Blob); err != nil {
		return err
	}
	return nil
}

// ValidName checks a file's name: not empty, at most MaxNameBytes of
// UTF-8, no slash, no control character, and not "." or "..".
func ValidName(name string) error {
	switch {
	case name == "" || name == "." || name == "..":
		return fmt.Errorf("attachment: %q is not a file name", name)
	case len(name) > MaxNameBytes:
		return fmt.Errorf("attachment: a file name is at most %d bytes", MaxNameBytes)
	case !utf8.ValidString(name):
		return errors.New("attachment: a file name is UTF-8")
	}
	for _, r := range name {
		if r == '/' || r == '\\' || r < 0x20 || r == 0x7f {
			return fmt.Errorf("attachment: %q is not one path segment", name)
		}
	}
	return nil
}
