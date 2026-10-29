// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package messaging

import (
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
)

// A file a message carries and a history bundle are both sealed as a
// stream: one version byte, then the plaintext in chunks of ChunkBytes,
// the last one shorter or, for nothing at all, empty. Chunk i is sealed
// with AES-256-GCM under a key derived from the stream's key with HKDF
// for its use, with the nonce three zero bytes, i as a 64-bit big-endian
// number, and a byte that is 1 for the last chunk and 0 for every other,
// and the version byte as additional data. A stream cut short,
// reordered or extended does not open, because every chunk's position is
// in its nonce and the last is marked. Chunk i starts at byte
// ChunkOffset(i), so a reader opens any chunk alone.

const (
	// ChunkBytes is the plaintext of every chunk of a stream but the
	// last.
	ChunkBytes = 64 << 10
	// SealedChunkBytes is a full chunk once sealed.
	SealedChunkBytes = ChunkBytes + gcmTag
	// StreamKeySize is the size of a stream's key.
	StreamKeySize = 32

	streamVersion = 1
	gcmTag        = 16
)

// The HKDF info of each use of the stream.
const (
	attachmentInfo = "latere messaging attachment v1"
	bundleInfo     = "latere messaging history bundle v1"
)

// SealedSize is the length of the stream that seals n bytes.
func SealedSize(n int64) int64 {
	chunks := max(1, (n+ChunkBytes-1)/ChunkBytes)
	return 1 + n + chunks*gcmTag
}

// ChunkOffset is where chunk i starts in a sealed stream.
func ChunkOffset(i int64) int64 { return 1 + i*SealedChunkBytes }

func streamAEAD(key []byte, info string) (cipher.AEAD, error) {
	if len(key) != StreamKeySize {
		return nil, fmt.Errorf("messaging: a stream's key is %d bytes", StreamKeySize)
	}
	k, err := hkdf.Key(sha256.New, key, nil, info, 32)
	if err != nil {
		return nil, err
	}
	return gcm(k)
}

func streamNonce(i uint64, last bool) []byte {
	nonce := make([]byte, 12)
	binary.BigEndian.PutUint64(nonce[3:11], i)
	if last {
		nonce[11] = 1
	}
	return nonce
}

var streamAD = []byte{streamVersion}

// readChunk fills buf from r: n bytes read, and whether r ended.
func readChunk(r io.Reader, buf []byte) (n int, ended bool, err error) {
	n, err = io.ReadFull(r, buf)
	switch {
	case err == nil:
		return n, false, nil
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return n, true, nil
	}
	return n, false, err
}

// sealStream seals what src holds, to its end, into dst. It returns the
// plaintext's length and SHA-256, and refuses more than limit bytes.
func sealStream(dst io.Writer, src io.Reader, key []byte, info string, limit int64) (int64, []byte, error) {
	aead, err := streamAEAD(key, info)
	if err != nil {
		return 0, nil, err
	}
	if _, err := dst.Write(streamAD); err != nil {
		return 0, nil, err
	}
	h := sha256.New()
	cur, next := make([]byte, ChunkBytes), make([]byte, ChunkBytes)
	n, ended, err := readChunk(src, cur)
	if err != nil {
		return 0, nil, err
	}
	var total int64
	out := make([]byte, 0, SealedChunkBytes)
	for i := uint64(0); ; i++ {
		last, m := ended, 0
		if !last {
			if m, ended, err = readChunk(src, next); err != nil {
				return 0, nil, err
			}
			last = m == 0 && ended
		}
		if total += int64(n); total > limit {
			return 0, nil, fmt.Errorf("messaging: more than %d bytes to seal", limit)
		}
		h.Write(cur[:n])
		if _, err := dst.Write(aead.Seal(out[:0], streamNonce(i, last), cur[:n], streamAD)); err != nil {
			return 0, nil, err
		}
		if last {
			return total, h.Sum(nil), nil
		}
		cur, next, n = next, cur, m
	}
}

// openStream opens a sealed stream from src into dst, writing each chunk
// as it authenticates, and returns the plaintext's length and SHA-256.
// It refuses a stream of more than limit plaintext bytes. On an error
// dst has received a prefix the caller discards.
func openStream(dst io.Writer, src io.Reader, key []byte, info string, limit int64) (int64, hash.Hash, error) {
	aead, err := streamAEAD(key, info)
	if err != nil {
		return 0, nil, err
	}
	version := make([]byte, 1)
	if _, err := io.ReadFull(src, version); err != nil || version[0] != streamVersion {
		return 0, nil, ErrSealed
	}
	h := sha256.New()
	cur, next := make([]byte, SealedChunkBytes), make([]byte, SealedChunkBytes)
	n, ended, err := readChunk(src, cur)
	if err != nil {
		return 0, nil, err
	}
	var total int64
	plain := make([]byte, 0, ChunkBytes)
	for i := uint64(0); ; i++ {
		last, m := ended, 0
		if !last {
			if m, ended, err = readChunk(src, next); err != nil {
				return 0, nil, err
			}
			last = m == 0 && ended
		}
		p, err := aead.Open(plain[:0], streamNonce(i, last), cur[:n], streamAD)
		if err != nil {
			return 0, nil, ErrSealed
		}
		if total += int64(len(p)); total > limit {
			return 0, nil, fmt.Errorf("messaging: more than %d bytes in a sealed stream", limit)
		}
		h.Write(p)
		if _, err := dst.Write(p); err != nil {
			return 0, nil, err
		}
		if last {
			return total, h, nil
		}
		cur, next, n = next, cur, m
	}
}

// openChunk opens chunk i of a stream alone.
func openChunk(key []byte, info string, i int64, sealed []byte, last bool) ([]byte, error) {
	if i < 0 || len(sealed) < gcmTag || len(sealed) > SealedChunkBytes || (!last && len(sealed) != SealedChunkBytes) {
		return nil, ErrSealed
	}
	aead, err := streamAEAD(key, info)
	if err != nil {
		return nil, err
	}
	p, err := aead.Open(nil, streamNonce(uint64(i), last), sealed, streamAD)
	if err != nil {
		return nil, ErrSealed
	}
	return p, nil
}
