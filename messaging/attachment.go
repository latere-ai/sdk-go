// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package messaging

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"

	"latere.ai/x/sdk-go/attachment"
)

// A file a message carries is encrypted on the sender's device under a
// key of its own, as a stream (see ChunkBytes), and the ciphertext is
// uploaded to Storage. The message carries the rest, inside the group's
// encryption: where the ciphertext is, the key, the digest of the
// plaintext, the size, the media type, the name and the preview. Whoever
// stores the ciphertext sees its length and nothing else of the file.

// The limits of a message's files.
const (
	// MaxAttachmentBytes is the largest file a message carries. A larger
	// one cannot be attached: nothing splits it.
	MaxAttachmentBytes = 2 << 30
	// MaxAttachments is how many files one message carries.
	MaxAttachments = 10
	// MaxPreviewBytes is the largest preview image of one file, which
	// travels inside the message.
	MaxPreviewBytes = 16 << 10
	// MaxWaveformBytes is the most steps a voice note's waveform has.
	MaxWaveformBytes = 256
)

// Attachment is a file as a message between people carries it: the
// attachment every kind of message shares, where the ciphertext is, and
// the key that opens it.
type Attachment struct {
	attachment.Attachment
	// Owner is whose Storage space holds the ciphertext, in the form the
	// storage package names a space; Ref is its path there.
	Owner string `json:"owner"`
	// Key opens the ciphertext.
	Key []byte `json:"key"`
}

// SealAttachment encrypts the file src holds, to its end, under a fresh
// key into dst, and returns the attachment with its Size, Blob and Key
// set. The caller sets the rest: the name, the media type, the preview,
// and Owner and Ref once the ciphertext is uploaded. A file of more than
// MaxAttachmentBytes is refused.
func SealAttachment(dst io.Writer, src io.Reader) (*Attachment, error) {
	key := make([]byte, StreamKeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	n, sum, err := sealStream(dst, src, key, attachmentInfo, MaxAttachmentBytes)
	if err != nil {
		return nil, err
	}
	return &Attachment{Attachment: attachment.Attachment{Size: n, Blob: attachment.Sum(sum)}, Key: key}, nil
}

// OpenAttachment decrypts the ciphertext src holds into dst, writing each
// chunk as it authenticates, and checks at the end that the file is the
// attachment's size and digest. On an error, dst holds a prefix the
// caller discards: write to a temporary file and keep it only when the
// error is nil.
func OpenAttachment(dst io.Writer, src io.Reader, a *Attachment) error {
	if err := a.validate(); err != nil {
		return err
	}
	n, h, err := openStream(dst, src, a.Key, attachmentInfo, MaxAttachmentBytes)
	if err != nil {
		return err
	}
	if n != a.Size || attachment.Sum(h.Sum(nil)) != a.Blob {
		return errors.New("messaging: the file is not the one its message describes")
	}
	return nil
}

// OpenAttachmentChunk decrypts chunk i of an attachment's ciphertext
// alone, the bytes from ChunkOffset(i), for a reader that seeks. The
// last chunk is the one that holds byte Size-1, or chunk 0 of an empty
// file. The chunk authenticates by itself; the digest of the whole file
// is checked only by OpenAttachment.
func OpenAttachmentChunk(a *Attachment, i int64, sealed []byte) ([]byte, error) {
	if err := a.validate(); err != nil {
		return nil, err
	}
	last := i == max(0, (a.Size-1)/ChunkBytes)
	return openChunk(a.Key, attachmentInfo, i, sealed, last)
}

func (a *Attachment) validate() error {
	if err := a.Attachment.Validate(); err != nil {
		return err
	}
	if len(a.Key) != StreamKeySize {
		return fmt.Errorf("messaging: an attachment's key is %d bytes", StreamKeySize)
	}
	if a.Size > MaxAttachmentBytes {
		return fmt.Errorf("messaging: a file is at most %d bytes", int64(MaxAttachmentBytes))
	}
	if p := a.Preview; p != nil {
		if len(p.Data) > MaxPreviewBytes {
			return fmt.Errorf("messaging: a preview is at most %d bytes", MaxPreviewBytes)
		}
		if len(p.Waveform) > MaxWaveformBytes {
			return fmt.Errorf("messaging: a waveform is at most %d steps", MaxWaveformBytes)
		}
		if p.Width < 0 || p.Height < 0 || p.DurationMillis < 0 || p.Pages < 0 {
			return errors.New("messaging: a preview's measures are not negative")
		}
	}
	return nil
}

// sealBytes and openBytes seal a value held in memory as a stream.
func sealBytes(key []byte, info string, plain []byte, limit int64) ([]byte, error) {
	var out bytes.Buffer
	out.Grow(int(SealedSize(int64(len(plain)))))
	if _, _, err := sealStream(&out, bytes.NewReader(plain), key, info, limit); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func openBytes(key []byte, info string, sealed []byte, limit int64) ([]byte, error) {
	var out bytes.Buffer
	if _, _, err := openStream(&out, bytes.NewReader(sealed), key, info, limit); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
