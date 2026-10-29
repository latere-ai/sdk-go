// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package mls

import (
	"errors"
	"fmt"
	"maps"
	"slices"
)

// The secret tree of RFC 9420, Section 9: from an epoch's encryption
// secret, a secret per leaf, and from each leaf's secret two hash
// ratchets, one for handshake messages and one for application
// messages. A secret is deleted once what it derives has been taken, so
// a later compromise does not open messages already read.

// MaxForwardGenerations is how far ahead of the next expected message a
// sender's ratchet may be moved by one received message. A message
// claiming a later generation is rejected rather than making the
// receiver derive and keep that many keys.
const MaxForwardGenerations = 1024

// MaxSkippedKeys is how many keys of skipped generations each ratchet
// keeps for messages that arrive out of order. The oldest is dropped
// first.
const MaxSkippedKeys = 128

type ratchetType uint8

const (
	ratchetHandshake   ratchetType = 0
	ratchetApplication ratchetType = 1
)

// keyNonce is one generation's AEAD key and nonce.
type keyNonce struct {
	key   []byte
	nonce []byte
}

// ratchet is a sender's hash ratchet: the secret of the next generation
// and the unused keys of skipped ones.
type ratchet struct {
	secret     []byte
	generation uint32
	skipped    map[uint32]keyNonce
}

// secretTree derives and deletes the secrets of one epoch.
type secretTree struct {
	s       *suite
	leaves  uint32
	secrets map[uint32][]byte // node index to a secret not yet split
	ratchet map[uint64]*ratchet
}

func newSecretTree(s *suite, encryptionSecret []byte, leaves uint32) *secretTree {
	return &secretTree{
		s:       s,
		leaves:  leaves,
		secrets: map[uint32][]byte{root(leaves): encryptionSecret},
		ratchet: make(map[uint64]*ratchet),
	}
}

func ratchetID(leaf uint32, typ ratchetType) uint64 { return uint64(leaf)<<1 | uint64(typ) }

// errGeneration is returned for a message whose key the ratchet no
// longer has or will not derive.
var errGeneration = errors.New("mls: message generation out of range")

// leafRatchets derives a leaf's two ratchets from the tree, deleting each
// parent secret once both of its children are derived.
func (t *secretTree) leafRatchets(leaf uint32) error {
	if leaf >= t.leaves {
		return fmt.Errorf("mls: leaf %d outside a tree of %d", leaf, t.leaves)
	}
	x := leafNodeIndex(leaf)
	path := append([]uint32{x}, directPath(x, t.leaves)...)
	// Find the lowest node on the path whose secret is still held.
	i := 0
	for ; i < len(path); i++ {
		if _, ok := t.secrets[path[i]]; ok {
			break
		}
	}
	if i == len(path) {
		return fmt.Errorf("%w: leaf %d's secret was already used", errGeneration, leaf)
	}
	for ; i > 0; i-- {
		p := path[i]
		secret := t.secrets[p]
		t.secrets[left(p)] = t.s.expandWithLabel(secret, "tree", []byte("left"), t.s.nh)
		t.secrets[right(p)] = t.s.expandWithLabel(secret, "tree", []byte("right"), t.s.nh)
		delete(t.secrets, p)
	}
	secret := t.secrets[x]
	delete(t.secrets, x)
	t.ratchet[ratchetID(leaf, ratchetHandshake)] = &ratchet{secret: t.s.expandWithLabel(secret, "handshake", nil, t.s.nh)}
	t.ratchet[ratchetID(leaf, ratchetApplication)] = &ratchet{secret: t.s.expandWithLabel(secret, "application", nil, t.s.nh)}
	return nil
}

func (t *secretTree) ratchetFor(leaf uint32, typ ratchetType) (*ratchet, error) {
	if r, ok := t.ratchet[ratchetID(leaf, typ)]; ok {
		return r, nil
	}
	if err := t.leafRatchets(leaf); err != nil {
		return nil, err
	}
	return t.ratchet[ratchetID(leaf, typ)], nil
}

// step derives a generation's key and nonce from its ratchet secret and
// returns the next generation's secret.
func (t *secretTree) step(secret []byte, gen uint32) (keyNonce, []byte) {
	kn := keyNonce{
		key:   t.s.deriveTreeSecret(secret, "key", gen, t.s.nk),
		nonce: t.s.deriveTreeSecret(secret, "nonce", gen, t.s.nn),
	}
	return kn, t.s.deriveTreeSecret(secret, "secret", gen, t.s.nh)
}

// next returns the key and nonce of a sender's own next generation and
// advances the ratchet past it.
func (t *secretTree) next(leaf uint32, typ ratchetType) (keyNonce, uint32, error) {
	r, err := t.ratchetFor(leaf, typ)
	if err != nil {
		return keyNonce{}, 0, err
	}
	if r.generation == ^uint32(0) {
		return keyNonce{}, 0, fmt.Errorf("%w: the ratchet is exhausted", errGeneration)
	}
	gen := r.generation
	kn, nextSecret := t.step(r.secret, gen)
	r.secret, r.generation = nextSecret, gen+1
	return kn, gen, nil
}

// get returns a received generation's key and nonce and a function that
// commits the ratchet's change. The ratchet is changed only once the
// message has been authenticated, so a forged message cannot move it.
func (t *secretTree) get(leaf uint32, typ ratchetType, gen uint32) (keyNonce, func(), error) {
	r, err := t.ratchetFor(leaf, typ)
	if err != nil {
		return keyNonce{}, nil, err
	}
	if gen < r.generation {
		kn, ok := r.skipped[gen]
		if !ok {
			return keyNonce{}, nil, fmt.Errorf("%w: generation %d was used or dropped", errGeneration, gen)
		}
		return kn, func() { delete(r.skipped, gen) }, nil
	}
	if gen-r.generation > MaxForwardGenerations {
		return keyNonce{}, nil, fmt.Errorf("%w: generation %d is %d ahead", errGeneration, gen, gen-r.generation)
	}
	if gen == ^uint32(0) {
		return keyNonce{}, nil, fmt.Errorf("%w: the ratchet is exhausted", errGeneration)
	}
	skipped := make(map[uint32]keyNonce)
	secret := r.secret
	for g := r.generation; g < gen; g++ {
		var kn keyNonce
		kn, secret = t.step(secret, g)
		skipped[g] = kn
	}
	kn, nextSecret := t.step(secret, gen)
	commit := func() {
		if r.skipped == nil {
			r.skipped = make(map[uint32]keyNonce)
		}
		maps.Copy(r.skipped, skipped)
		if over := len(r.skipped) - MaxSkippedKeys; over > 0 {
			gens := slices.Sorted(maps.Keys(r.skipped))
			for _, g := range gens[:over] {
				delete(r.skipped, g)
			}
		}
		r.secret, r.generation = nextSecret, gen+1
	}
	return kn, commit, nil
}
