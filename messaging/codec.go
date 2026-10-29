// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package messaging

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// The formats of this package are binary, big-endian, with each byte
// string behind its length. A decoder takes exactly one encoding of a
// value: it refuses trailing bytes, and each format's parser refuses
// what its encoder would not write.

// errFormat is the error for bytes that are not an encoding of a format
// of this package.
var errFormat = errors.New("messaging: malformed encoding")

type writer struct{ b []byte }

func (w *writer) u8(v uint8)   { w.b = append(w.b, v) }
func (w *writer) u16(v uint16) { w.b = binary.BigEndian.AppendUint16(w.b, v) }
func (w *writer) u32(v uint32) { w.b = binary.BigEndian.AppendUint32(w.b, v) }
func (w *writer) u64(v uint64) { w.b = binary.BigEndian.AppendUint64(w.b, v) }
func (w *writer) raw(b []byte) { w.b = append(w.b, b...) }

// vec8 writes a byte string of at most 255 bytes. The formats call it
// only with lengths their validation bounds, so a longer one is a bug.
func (w *writer) vec8(b []byte) {
	if len(b) > math.MaxUint8 {
		panic("messaging: vec8 over 255 bytes")
	}
	w.u8(uint8(len(b)))
	w.raw(b)
}

// vec32 writes a byte string behind a four-byte length.
func (w *writer) vec32(b []byte) {
	if uint64(len(b)) > math.MaxUint32 {
		panic("messaging: vec32 over 4 GiB")
	}
	w.u32(uint32(len(b)))
	w.raw(b)
}

// reader decodes with a sticky error: after the first failure every
// read returns zero values and the error stays.
type reader struct {
	b   []byte
	err error
}

func (r *reader) fail(format string, args ...any) {
	if r.err == nil {
		r.err = fmt.Errorf("%w: %s", errFormat, fmt.Sprintf(format, args...))
	}
}

func (r *reader) take(n int) []byte {
	if r.err != nil {
		return nil
	}
	if n < 0 || n > len(r.b) {
		r.fail("truncated")
		return nil
	}
	out := r.b[:n:n]
	r.b = r.b[n:]
	return out
}

func (r *reader) u8() uint8 {
	b := r.take(1)
	if b == nil {
		return 0
	}
	return b[0]
}

func (r *reader) u16() uint16 {
	b := r.take(2)
	if b == nil {
		return 0
	}
	return binary.BigEndian.Uint16(b)
}

func (r *reader) u32() uint32 {
	b := r.take(4)
	if b == nil {
		return 0
	}
	return binary.BigEndian.Uint32(b)
}

func (r *reader) u64() uint64 {
	b := r.take(8)
	if b == nil {
		return 0
	}
	return binary.BigEndian.Uint64(b)
}

func (r *reader) vec8() []byte { return r.take(int(r.u8())) }

// vec32 reads a byte string behind a four-byte length; the length must
// fit in what is left, so a hostile length allocates nothing.
func (r *reader) vec32() []byte {
	n := r.u32()
	if r.err == nil && uint64(n) > uint64(len(r.b)) {
		r.fail("length %d past the end", n)
		return nil
	}
	return r.take(int(n))
}

// done fails the decoding when bytes are left over.
func (r *reader) done() error {
	if r.err == nil && len(r.b) != 0 {
		r.fail("%d trailing bytes", len(r.b))
	}
	return r.err
}

func encode(f func(*writer)) []byte {
	var w writer
	f(&w)
	return w.b
}
