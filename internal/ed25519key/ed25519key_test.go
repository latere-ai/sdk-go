// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package ed25519key

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"math/big"
	"slices"
	"testing"
)

// p is the field's prime, 2^255 - 19.
var p = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 255), big.NewInt(19))

// yOf is the y an encoding names, mod p.
func yOf(enc []byte) *big.Int {
	b := slices.Clone(enc)
	b[31] &= 0x7f
	slices.Reverse(b)
	y := new(big.Int).SetBytes(b)
	return y.Mod(y, p)
}

// lowOrder reports whether the point of y has an order dividing 8, by
// its Montgomery u = (1+y)/(1-y): X25519 clamps every scalar to a
// multiple of 8, so it answers zero for such a point, which crypto/ecdh
// refuses. y = 1 is the identity, whose u is the point at infinity.
func lowOrder(t *testing.T, y *big.Int) bool {
	t.Helper()
	if y.Cmp(big.NewInt(1)) == 0 {
		return true
	}
	num := new(big.Int).Add(big.NewInt(1), y)
	den := new(big.Int).Sub(big.NewInt(1), y)
	den.Mod(den, p)
	u := new(big.Int).Mul(num, new(big.Int).ModInverse(den, p))
	u.Mod(u, p)
	enc := make([]byte, 32)
	u.FillBytes(enc)
	slices.Reverse(enc)
	pub, err := ecdh.X25519().NewPublicKey(enc)
	if err != nil {
		t.Fatal(err)
	}
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, err = priv.ECDH(pub)
	return err != nil
}

// Every listed encoding, with either sign bit, is of small order, and
// they name the five values of y the points of order dividing 8 have.
func TestSmallOrderList(t *testing.T) {
	ys := map[string]bool{}
	for _, s := range smallOrder {
		for _, sign := range []byte{0, 0x80} {
			enc := s
			enc[31] |= sign
			if !SmallOrder(enc[:]) {
				t.Errorf("%x: not refused", enc)
			}
		}
		y := yOf(s[:])
		if !lowOrder(t, y) {
			t.Errorf("%x: the point of y %v is not of small order", s, y)
		}
		ys[y.String()] = true
	}
	if len(ys) != 5 {
		t.Errorf("the list names %d values of y, want the 5 of the points of order dividing 8", len(ys))
	}
}

// The identity point's key takes a signature anyone makes, R the
// identity and S zero, for every message.
func TestIdentityKeySignsForAnyone(t *testing.T) {
	identity := smallOrder[1][:]
	sig := append(slices.Clone(identity), make([]byte, 32)...)
	for _, msg := range []string{"", "anything at all", "a revocation"} {
		if !ed25519.Verify(identity, []byte(msg), sig) {
			t.Fatalf("the universal signature does not verify over %q, so the list guards nothing here", msg)
		}
	}
	if !SmallOrder(identity) {
		t.Error("the identity key is not refused")
	}
}

// A key a device makes is not refused, and neither is a key of another
// length, which is its caller's to refuse.
func TestOrdinaryKeys(t *testing.T) {
	for range 200 {
		pub, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		if SmallOrder(pub) {
			t.Fatalf("%x: refused", pub)
		}
		if lowOrder(t, yOf(pub)) {
			t.Fatalf("%x: the order test calls an ordinary key small, so it proves nothing of the list", pub)
		}
	}
	for _, n := range []int{0, 31, 33} {
		if SmallOrder(make([]byte, n)) {
			t.Errorf("a key of %d bytes is refused here", n)
		}
	}
	near := smallOrder[1]
	near[0] ^= 2
	if SmallOrder(near[:]) {
		t.Error("an encoding one bit from the identity's is refused")
	}
}
