// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package mls

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"maps"
)

// hpkeKey is an HPKE key pair, the private key as SerializePrivateKey
// encodes it.
type hpkeKey struct {
	priv []byte
	pub  []byte
}

// privateTree holds a member's private keys in the ratchet tree: its
// leaf's and those of the parents above it it knows.
type privateTree struct {
	leaf uint32
	keys map[uint32]hpkeKey // node index to key pair
}

func (p *privateTree) clone() *privateTree {
	return &privateTree{leaf: p.leaf, keys: maps.Clone(p.keys)}
}

// prune drops the keys of nodes the tree no longer holds with the same
// public key, which a change to the tree has replaced or blanked.
func (p *privateTree) prune(t *ratchetTree) {
	for x, k := range p.keys {
		if x >= uint32(len(t.nodes)) || !bytes.Equal(t.nodes[x].encryptionKey(), k.pub) {
			delete(p.keys, x)
		}
	}
}

// keyFor returns the private key of node x, when the member holds it.
func (p *privateTree) keyFor(x uint32) ([]byte, bool) {
	k, ok := p.keys[x]
	return k.priv, ok
}

// pathSecrets is what a committer derives for its update path.
type pathSecrets struct {
	leafKey      hpkeKey
	nodes        []uint32          // the filtered direct path
	secrets      map[uint32][]byte // node index to path secret
	keys         map[uint32]hpkeKey
	commitSecret []byte
}

func (s *suite) nodeKey(pathSecret []byte) (hpkeKey, error) {
	priv, pub, err := s.deriveKeyPair(s.deriveSecret(pathSecret, "node"))
	if err != nil {
		return hpkeKey{}, err
	}
	return hpkeKey{priv: priv, pub: pub}, nil
}

// setPathParentHashes fills the parent_hash of each node on a filtered
// direct path, top down, and returns the parent hash the leaf signs.
// The nodes must have been reset by the path, so none has unmerged
// leaves and each sibling's tree hash is its original one.
func (t *ratchetTree) setPathParentHashes(s *suite, leaf uint32, fdp []uint32) []byte {
	x := leafNodeIndex(leaf)
	var ph []byte
	for i := len(fdp) - 1; i >= 0; i-- {
		p := fdp[i]
		pn := t.nodes[p].parent
		pn.parentHash = ph
		ph = s.parentHash(pn, t.hashSubtree(s, copathChild(p, x), nil, nil))
	}
	return ph
}

// leafTemplate is what a committer keeps or changes of its leaf.
type leafTemplate struct {
	signatureKey []byte
	credential   Credential
	capabilities Capabilities
	extensions   []Extension
}

// createUpdatePath resets a committer's path in t, which already has the
// committed proposals applied, and returns the update path, the secrets
// it derived and the provisional group context it encrypted to. context
// returns the provisional group context for the tree hash of the new
// tree. Leaves in exclude, the members the commit adds, get no
// ciphertexts.
func (t *ratchetTree) createUpdatePath(s *suite, leaf uint32, tmpl leafTemplate, signer *SignatureKey, groupID []byte,
	exclude []uint32, context func(treeHash []byte) []byte) (*updatePath, *pathSecrets, []byte, error) {
	x := leafNodeIndex(leaf)
	t.blankPath(leaf)
	fdp := t.filteredDirectPath(leaf)

	leafPriv, leafPub, err := s.generateKeyPair()
	if err != nil {
		return nil, nil, nil, err
	}
	ps := &pathSecrets{
		leafKey: hpkeKey{priv: leafPriv, pub: leafPub},
		nodes:   fdp,
		secrets: make(map[uint32][]byte),
		keys:    make(map[uint32]hpkeKey),
	}
	secret := make([]byte, s.nh)
	if _, err := rand.Read(secret); err != nil {
		return nil, nil, nil, err
	}
	for _, p := range fdp {
		k, err := s.nodeKey(secret)
		if err != nil {
			return nil, nil, nil, err
		}
		ps.secrets[p] = secret
		ps.keys[p] = k
		t.nodes[p] = node{parent: &parentNode{encryptionKey: k.pub}}
		secret = s.deriveSecret(secret, "path")
	}
	ps.commitSecret = secret

	l := &leafNode{
		encryptionKey: leafPub,
		signatureKey:  tmpl.signatureKey,
		credential:    tmpl.credential,
		capabilities:  tmpl.capabilities,
		source:        leafSourceCommit,
		parentHash:    t.setPathParentHashes(s, leaf, fdp),
		extensions:    tmpl.extensions,
	}
	if err := l.sign(s, signer, groupID, leaf); err != nil {
		return nil, nil, nil, err
	}
	t.nodes[x] = node{leaf: l}

	gc := context(t.treeHash(s))
	path := &updatePath{leaf: *l}
	for _, p := range fdp {
		un := updatePathNode{encryptionKey: ps.keys[p].pub}
		for _, r := range t.resolution(copathChild(p, x), exclude) {
			kem, ct, err := s.encryptWithLabel(t.nodes[r].encryptionKey(), "UpdatePathNode", gc, ps.secrets[p])
			if err != nil {
				return nil, nil, nil, err
			}
			un.encryptedPathSecret = append(un.encryptedPathSecret, hpkeCiphertext{kemOutput: kem, ciphertext: ct})
		}
		path.nodes = append(path.nodes, un)
	}
	return path, ps, gc, nil
}

// mergeUpdatePath applies a committer's update path to t, which already
// has the committed proposals applied, and checks the parent hash the
// committer's leaf signs. It returns the filtered direct path.
func (t *ratchetTree) mergeUpdatePath(s *suite, leaf uint32, path *updatePath) ([]uint32, error) {
	x := leafNodeIndex(leaf)
	t.blankPath(leaf)
	fdp := t.filteredDirectPath(leaf)
	if len(path.nodes) != len(fdp) {
		return nil, treeErr("update path has %d nodes, the filtered direct path %d", len(path.nodes), len(fdp))
	}
	for i, p := range fdp {
		t.nodes[p] = node{parent: &parentNode{encryptionKey: path.nodes[i].encryptionKey}}
	}
	ph := t.setPathParentHashes(s, leaf, fdp)
	if !bytes.Equal(ph, path.leaf.parentHash) {
		return nil, treeErr("the committer's leaf does not sign the parent hash of its path")
	}
	l := path.leaf
	t.nodes[x] = node{leaf: &l}
	return fdp, nil
}

// decryptPathSecret finds the ciphertext of a merged update path meant
// for the member holding priv and opens it. It returns the path secret
// and the position on the filtered direct path of the node it is for.
func (t *ratchetTree) decryptPathSecret(s *suite, sender uint32, path *updatePath, fdp []uint32, priv *privateTree,
	exclude []uint32, gc []byte) ([]byte, int, error) {
	sx := leafNodeIndex(sender)
	mx := leafNodeIndex(priv.leaf)
	for i, p := range fdp {
		c := copathChild(p, sx)
		if !inSubtree(c, mx) {
			continue
		}
		res := t.resolution(c, exclude)
		if len(path.nodes[i].encryptedPathSecret) != len(res) {
			return nil, 0, treeErr("update path node %d has %d ciphertexts for a resolution of %d", p, len(path.nodes[i].encryptedPathSecret), len(res))
		}
		for j, r := range res {
			key, ok := priv.keyFor(r)
			if !ok {
				continue
			}
			ct := path.nodes[i].encryptedPathSecret[j]
			secret, err := s.decryptWithLabel(key, "UpdatePathNode", gc, ct.kemOutput, ct.ciphertext)
			if err != nil {
				return nil, 0, fmt.Errorf("mls: path secret: %w", err)
			}
			return secret, i, nil
		}
		return nil, 0, fmt.Errorf("mls: no key for the update path's ciphertexts")
	}
	return nil, 0, fmt.Errorf("mls: the update path does not cover this member")
}

// openUpdatePath decrypts the path secret meant for the member and
// derives the keys of the path's nodes from there up and the commit
// secret.
func (t *ratchetTree) openUpdatePath(s *suite, sender uint32, path *updatePath, fdp []uint32, priv *privateTree,
	exclude []uint32, gc []byte) (map[uint32]hpkeKey, []byte, error) {
	secret, i, err := t.decryptPathSecret(s, sender, path, fdp, priv, exclude, gc)
	if err != nil {
		return nil, nil, err
	}
	return derivePathKeys(s, secret, fdp[i:], path.nodes[i:])
}

// derivePathKeys derives the keys of the nodes from a path secret up,
// checks each against the public key the path carries, and returns the
// commit secret.
func derivePathKeys(s *suite, secret []byte, nodes []uint32, upn []updatePathNode) (map[uint32]hpkeKey, []byte, error) {
	keys := make(map[uint32]hpkeKey)
	for k, p := range nodes {
		key, err := s.nodeKey(secret)
		if err != nil {
			return nil, nil, err
		}
		if !bytes.Equal(key.pub, upn[k].encryptionKey) {
			return nil, nil, treeErr("node %d's key does not match its path secret", p)
		}
		keys[p] = key
		secret = s.deriveSecret(secret, "path")
	}
	return keys, secret, nil
}
