// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package mls

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// The wire encoding is the TLS presentation language of RFC 8446 with the
// variable-length vector headers of RFC 9420, Section 2.1.2: a QUIC-style
// varint of one, two or four bytes, which must be the shortest encoding
// of the length.

// maxVarint is the largest length a four-byte varint carries.
const maxVarint = 1<<30 - 1

// errDecode is wrapped by every error a malformed encoding produces, so a
// caller tells bad input from a failed check.
var errDecode = errors.New("mls: malformed encoding")

// writer appends a TLS encoding to b.
type writer struct {
	b []byte
}

func (w *writer) u8(v uint8)   { w.b = append(w.b, v) }
func (w *writer) u16(v uint16) { w.b = binary.BigEndian.AppendUint16(w.b, v) }
func (w *writer) u32(v uint32) { w.b = binary.BigEndian.AppendUint32(w.b, v) }
func (w *writer) u64(v uint64) { w.b = binary.BigEndian.AppendUint64(w.b, v) }
func (w *writer) raw(p []byte) { w.b = append(w.b, p...) }

// varint appends n in its shortest varint encoding. Lengths above
// maxVarint cannot be encoded: the reader bounds what it parses the same
// way, and the entry points that take a caller's bytes reject anything
// larger than MaxContentBytes, so reaching it is a bug in this package.
func (w *writer) varint(n int) {
	if n < 0 || n > maxVarint {
		panic(fmt.Sprintf("mls: vector length %d cannot be encoded", n))
	}
	switch {
	case n < 1<<6:
		w.u8(uint8(n))
	case n < 1<<14:
		w.u16(uint16(n) | 0x4000)
	default:
		w.u32(uint32(n) | 0x80000000)
	}
}

// vec appends p as an opaque vector with a varint length.
func (w *writer) vec(p []byte) {
	w.varint(len(p))
	w.raw(p)
}

// nested appends what f writes as a vector: the varint length of the
// encoding, then the encoding.
func (w *writer) nested(f func(w *writer)) {
	var inner writer
	f(&inner)
	w.vec(inner.b)
}

// optional appends the presence byte of an optional<T>, then f's encoding
// when present.
func (w *writer) optional(present bool, f func(w *writer)) {
	if !present {
		w.u8(0)
		return
	}
	w.u8(1)
	f(w)
}

// reader decodes a TLS encoding. The first error sticks: every later read
// returns a zero value, so a decoder reads a whole struct and checks err
// once.
type reader struct {
	b   []byte
	err error
}

func newReader(b []byte) *reader { return &reader{b: b} }

func (r *reader) fail(format string, args ...any) {
	if r.err == nil {
		r.err = fmt.Errorf("%w: %s", errDecode, fmt.Sprintf(format, args...))
	}
}

func (r *reader) take(n int) []byte {
	if r.err != nil {
		return nil
	}
	if n < 0 || n > len(r.b) {
		r.fail("need %d bytes, have %d", n, len(r.b))
		return nil
	}
	p := r.b[:n:n]
	r.b = r.b[n:]
	return p
}

func (r *reader) u8() uint8 {
	p := r.take(1)
	if p == nil {
		return 0
	}
	return p[0]
}

func (r *reader) u16() uint16 {
	p := r.take(2)
	if p == nil {
		return 0
	}
	return binary.BigEndian.Uint16(p)
}

func (r *reader) u32() uint32 {
	p := r.take(4)
	if p == nil {
		return 0
	}
	return binary.BigEndian.Uint32(p)
}

func (r *reader) u64() uint64 {
	p := r.take(8)
	if p == nil {
		return 0
	}
	return binary.BigEndian.Uint64(p)
}

// varint reads a vector length and rejects a prefix of 0b11 and any
// encoding longer than needed.
func (r *reader) varint() int {
	if r.err != nil {
		return 0
	}
	if len(r.b) == 0 {
		r.fail("need a length")
		return 0
	}
	switch r.b[0] >> 6 {
	case 0:
		return int(r.u8())
	case 1:
		v := int(r.u16() & 0x3fff)
		if r.err == nil && v < 1<<6 {
			r.fail("length %d not minimally encoded", v)
		}
		return v
	case 2:
		v := int(r.u32() & 0x3fffffff)
		if r.err == nil && v < 1<<14 {
			r.fail("length %d not minimally encoded", v)
		}
		return v
	default:
		r.fail("invalid length prefix")
		return 0
	}
}

// vec reads an opaque vector. The result aliases the input, with its
// capacity capped so an append never writes into it.
func (r *reader) vec() []byte {
	n := r.varint()
	return r.take(n)
}

// nested reads a vector and hands a reader over its bytes to f, which
// must consume all of them.
func (r *reader) nested(f func(r *reader)) {
	p := r.vec()
	if r.err != nil {
		return
	}
	inner := newReader(p)
	f(inner)
	if inner.err == nil && len(inner.b) != 0 {
		inner.fail("%d bytes left in a vector", len(inner.b))
	}
	if inner.err != nil && r.err == nil {
		r.err = inner.err
	}
}

// list reads a vector of elements, calling f until the vector's bytes are
// consumed.
func (r *reader) list(f func(r *reader)) {
	r.nested(func(inner *reader) {
		for inner.err == nil && len(inner.b) > 0 {
			f(inner)
		}
	})
}

// optional reads the presence byte of an optional<T> and reports whether
// a value follows.
func (r *reader) optional() bool {
	switch v := r.u8(); v {
	case 0:
		return false
	case 1:
		return true
	default:
		r.fail("optional presence byte %d", v)
		return false
	}
}

// done reports an error when bytes remain after a complete value.
func (r *reader) done() error {
	if r.err == nil && len(r.b) != 0 {
		r.fail("%d trailing bytes", len(r.b))
	}
	return r.err
}

// decode runs f over b and requires it to consume b exactly.
func decode(b []byte, f func(r *reader)) error {
	r := newReader(b)
	f(r)
	return r.done()
}

// encode returns what f writes.
func encode(f func(w *writer)) []byte {
	var w writer
	f(&w)
	return w.b
}
