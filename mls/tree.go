// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package mls

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
)

// node is one entry of the ratchet tree: a leaf, a parent, or blank when
// both are nil.
type node struct {
	leaf   *leafNode
	parent *parentNode
}

func (n node) blank() bool { return n.leaf == nil && n.parent == nil }

// encryptionKey returns the node's HPKE public key, nil when blank.
func (n node) encryptionKey() []byte {
	switch {
	case n.leaf != nil:
		return n.leaf.encryptionKey
	case n.parent != nil:
		return n.parent.encryptionKey
	}
	return nil
}

// ratchetTree is the array representation of RFC 9420, Section 4.1,
// always holding a power of two of leaves.
type ratchetTree struct {
	nodes []node
}

// errTree is wrapped by every error a tree that breaks the protocol's
// invariants produces.
var errTree = errors.New("mls: invalid ratchet tree")

func treeErr(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errTree, fmt.Sprintf(format, args...))
}

func newTree(l *leafNode) *ratchetTree {
	return &ratchetTree{nodes: []node{{leaf: l}}}
}

func (t *ratchetTree) numLeaves() uint32 { return uint32(len(t.nodes)+1) / 2 }

func (t *ratchetTree) leaf(i uint32) *leafNode {
	x := leafNodeIndex(i)
	if x >= uint32(len(t.nodes)) {
		return nil
	}
	return t.nodes[x].leaf
}

// clone copies the tree so a staged change leaves the original intact.
// Leaf nodes are shared, since the tree never changes one in place;
// parent nodes are copied, since adding a leaf appends to their lists of
// unmerged leaves.
func (t *ratchetTree) clone() *ratchetTree {
	c := &ratchetTree{nodes: make([]node, len(t.nodes))}
	for i, n := range t.nodes {
		c.nodes[i].leaf = n.leaf
		if n.parent != nil {
			c.nodes[i].parent = n.parent.clone()
		}
	}
	return c
}

// marshal writes the tree as the ratchet_tree extension carries it, with
// no blank entries after the last node that holds a value.
func (t *ratchetTree) marshal(w *writer) {
	end := len(t.nodes)
	for end > 0 && t.nodes[end-1].blank() {
		end--
	}
	w.nested(func(w *writer) {
		for _, n := range t.nodes[:end] {
			w.optional(!n.blank(), func(w *writer) {
				if n.leaf != nil {
					w.u8(nodeTypeLeaf)
					n.leaf.marshal(w)
					return
				}
				w.u8(nodeTypeParent)
				n.parent.marshal(w)
			})
		}
	})
}

func (t *ratchetTree) bytes() []byte { return encode(t.marshal) }

// unmarshalTree reads a ratchet_tree, checks that leaves and parents sit
// at their positions and that the last entry holds a value, and extends
// it with blanks to a full tree.
func unmarshalTree(r *reader) *ratchetTree {
	t := new(ratchetTree)
	r.list(func(r *reader) {
		x := uint32(len(t.nodes))
		var n node
		if r.optional() {
			switch typ := r.u8(); typ {
			case nodeTypeLeaf:
				if !isLeaf(x) {
					r.fail("leaf at parent position %d", x)
					return
				}
				n.leaf = new(leafNode)
				n.leaf.unmarshal(r)
			case nodeTypeParent:
				if isLeaf(x) {
					r.fail("parent at leaf position %d", x)
					return
				}
				n.parent = new(parentNode)
				n.parent.unmarshal(r)
			default:
				r.fail("node type %d", typ)
				return
			}
		}
		t.nodes = append(t.nodes, n)
		if len(t.nodes) > 2*MaxLeaves-1 {
			r.fail("tree of more than %d leaves", MaxLeaves)
		}
	})
	if r.err != nil {
		return nil
	}
	if len(t.nodes) == 0 || t.nodes[len(t.nodes)-1].blank() {
		r.fail("ratchet tree is empty or ends in a blank node")
		return nil
	}
	width := 1
	for width < len(t.nodes) {
		width = 2*width + 1
	}
	for len(t.nodes) < width {
		t.nodes = append(t.nodes, node{})
	}
	return t
}

func parseTree(b []byte) (*ratchetTree, error) {
	var t *ratchetTree
	if err := decode(bytes.Clone(b), func(r *reader) { t = unmarshalTree(r) }); err != nil {
		return nil, err
	}
	return t, nil
}

// resolution returns the nodes that together cover node x's subtree, in
// the order of RFC 9420, Section 4.1.1, leaving out the leaves in
// exclude.
func (t *ratchetTree) resolution(x uint32, exclude []uint32) []uint32 {
	n := t.nodes[x]
	if isLeaf(x) {
		if n.leaf == nil || slices.Contains(exclude, nodeLeafIndex(x)) {
			return nil
		}
		return []uint32{x}
	}
	if n.parent == nil {
		return append(t.resolution(left(x), exclude), t.resolution(right(x), exclude)...)
	}
	res := []uint32{x}
	for _, l := range n.parent.unmergedLeaves {
		if !slices.Contains(exclude, l) {
			res = append(res, leafNodeIndex(l))
		}
	}
	return res
}

// filteredDirectPath returns the ancestors of a leaf whose child off the
// leaf's path has a non-empty resolution, from the leaf up.
func (t *ratchetTree) filteredDirectPath(leaf uint32) []uint32 {
	x := leafNodeIndex(leaf)
	var out []uint32
	for _, p := range directPath(x, t.numLeaves()) {
		if len(t.resolution(copathChild(p, x), nil)) > 0 {
			out = append(out, p)
		}
	}
	return out
}

// copathChild returns the child of p off the path to its descendant x.
func copathChild(p, x uint32) uint32 {
	if x < p {
		return right(p)
	}
	return left(p)
}

// treeHashes returns the tree hash of every node, RFC 9420, Section 7.8.
func (t *ratchetTree) treeHashes(s *suite) [][]byte {
	out := make([][]byte, len(t.nodes))
	t.hashSubtree(s, root(t.numLeaves()), nil, out)
	return out
}

func (t *ratchetTree) treeHash(s *suite) []byte {
	return t.hashSubtree(s, root(t.numLeaves()), nil, nil)
}

// hashSubtree returns the tree hash of node x with the leaves in exclude
// blanked and dropped from every unmerged list, and records each node's
// hash in out when it is not nil.
func (t *ratchetTree) hashSubtree(s *suite, x uint32, exclude []uint32, out [][]byte) []byte {
	var h []byte
	n := t.nodes[x]
	if isLeaf(x) {
		present := n.leaf != nil && !slices.Contains(exclude, nodeLeafIndex(x))
		h = s.hash(encode(func(w *writer) {
			w.u8(nodeTypeLeaf)
			w.u32(nodeLeafIndex(x))
			w.optional(present, func(w *writer) { n.leaf.marshal(w) })
		}))
	} else {
		lh := t.hashSubtree(s, left(x), exclude, out)
		rh := t.hashSubtree(s, right(x), exclude, out)
		h = s.hash(encode(func(w *writer) {
			w.u8(nodeTypeParent)
			w.optional(n.parent != nil, func(w *writer) {
				p := n.parent
				if len(exclude) > 0 {
					p = p.clone()
					p.unmergedLeaves = slices.DeleteFunc(p.unmergedLeaves, func(l uint32) bool {
						return slices.Contains(exclude, l)
					})
				}
				p.marshal(w)
			})
			w.vec(lh)
			w.vec(rh)
		}))
	}
	if out != nil {
		out[x] = h
	}
	return h
}

// parentHash is the parent hash of the parent node p with copath child
// whose tree hash is siblingHash, RFC 9420, Section 7.9.
func (s *suite) parentHash(p *parentNode, siblingHash []byte) []byte {
	return s.hash(encode(func(w *writer) {
		w.vec(p.encryptionKey)
		w.vec(p.parentHash)
		w.vec(siblingHash)
	}))
}

// originalSiblingHash is the tree hash of p's child c with p's unmerged
// leaves removed, the original_sibling_tree_hash of RFC 9420, Section
// 7.9. cached holds the plain tree hashes, used when no unmerged leaf of
// p sits under c.
func (t *ratchetTree) originalSiblingHash(s *suite, p, c uint32, cached [][]byte) []byte {
	var exclude []uint32
	for _, l := range t.nodes[p].parent.unmergedLeaves {
		if inSubtree(c, leafNodeIndex(l)) {
			exclude = append(exclude, l)
		}
	}
	if len(exclude) == 0 && cached != nil {
		return cached[c]
	}
	return t.hashSubtree(s, c, exclude, nil)
}

// nodeParentHash returns the parent_hash field of node x: a parent's, or
// a leaf's when a commit set it, and nil otherwise.
func (t *ratchetTree) nodeParentHash(x uint32) []byte {
	n := t.nodes[x]
	switch {
	case n.parent != nil:
		return n.parent.parentHash
	case n.leaf != nil && n.leaf.source == leafSourceCommit:
		return n.leaf.parentHash
	}
	return nil
}

// verifyParentHashes checks that every parent node is parent-hash valid,
// RFC 9420, Section 7.9.2: exactly one node below it links to it.
func (t *ratchetTree) verifyParentHashes(s *suite) error {
	hashes := t.treeHashes(s)
	for p := uint32(1); p < uint32(len(t.nodes)); p += 2 {
		pn := t.nodes[p].parent
		if pn == nil {
			continue
		}
		links := 0
		for _, c := range []uint32{left(p), right(p)} {
			sib := sibling(c)
			ph := s.parentHash(pn, t.originalSiblingHash(s, p, sib, hashes))
			res := t.resolution(c, nil)
			for _, d := range res {
				if !bytes.Equal(t.nodeParentHash(d), ph) {
					continue
				}
				// The unmerged leaves of p under c must be exactly the
				// rest of c's resolution.
				var under []uint32
				for _, l := range pn.unmergedLeaves {
					if inSubtree(c, leafNodeIndex(l)) {
						under = append(under, leafNodeIndex(l))
					}
				}
				rest := slices.DeleteFunc(slices.Clone(res), func(y uint32) bool { return y == d })
				slices.Sort(under)
				slices.Sort(rest)
				if slices.Equal(under, rest) {
					links++
				}
			}
		}
		if links != 1 {
			return treeErr("parent node %d is linked by %d nodes, want 1", p, links)
		}
	}
	return nil
}

// verifyUnmergedLeaves checks the rules RFC 9420, Section 12.4.3.1 sets
// for the unmerged leaves of each parent: each names a leaf that holds a
// member below it, and every parent between that leaf and it lists the
// leaf too.
func (t *ratchetTree) verifyUnmergedLeaves() error {
	n := t.numLeaves()
	for p := uint32(1); p < uint32(len(t.nodes)); p += 2 {
		pn := t.nodes[p].parent
		if pn == nil {
			continue
		}
		for i, l := range pn.unmergedLeaves {
			if slices.Contains(pn.unmergedLeaves[:i], l) {
				return treeErr("node %d lists unmerged leaf %d twice", p, l)
			}
			x := leafNodeIndex(l)
			if l >= n || t.nodes[x].leaf == nil || !inSubtree(p, x) || x == p {
				return treeErr("node %d lists unmerged leaf %d, which is not a member below it", p, l)
			}
			for _, a := range directPath(x, n) {
				if a == p {
					break
				}
				if an := t.nodes[a].parent; an != nil && !slices.Contains(an.unmergedLeaves, l) {
					return treeErr("node %d does not list unmerged leaf %d that node %d lists", a, l, p)
				}
			}
		}
	}
	return nil
}

// verifyUniqueKeys checks that no HPKE key appears in two nodes and no
// signature key in two leaves.
func (t *ratchetTree) verifyUniqueKeys() error {
	enc := make(map[string]bool)
	sig := make(map[string]bool)
	for x, n := range t.nodes {
		if n.blank() {
			continue
		}
		k := string(n.encryptionKey())
		if enc[k] {
			return treeErr("node %d repeats an encryption key", x)
		}
		enc[k] = true
		if n.leaf != nil {
			sk := string(n.leaf.signatureKey)
			if sig[sk] {
				return treeErr("leaf %d repeats a signature key", x/2)
			}
			sig[sk] = true
		}
	}
	return nil
}

// hasEncryptionKey reports whether any node holds the HPKE key k.
func (t *ratchetTree) hasEncryptionKey(k []byte) bool {
	for _, n := range t.nodes {
		if !n.blank() && bytes.Equal(n.encryptionKey(), k) {
			return true
		}
	}
	return false
}

// blankPath blanks every ancestor of a leaf.
func (t *ratchetTree) blankPath(leaf uint32) {
	for _, p := range directPath(leafNodeIndex(leaf), t.numLeaves()) {
		t.nodes[p] = node{}
	}
}

// extend doubles the tree with a blank right half and a blank root.
// MaxLeaves bounds a group's leaves, so a tree a delivery service hands
// out, which is parsed and hashed before anything signed vouches for it,
// costs a bounded amount of memory and time.
const MaxLeaves = 1 << 16

// errTooLarge is the error for a change that grows a group past
// MaxLeaves.
var errTooLarge = fmt.Errorf("mls: a group has at most %d leaves", MaxLeaves)

func (t *ratchetTree) extend() {
	w := len(t.nodes)
	t.nodes = append(t.nodes, make([]node, w+1)...)
}

// addLeaf places a new member's leaf in the leftmost blank leaf,
// extending the tree when every leaf is taken, and adds it to the
// unmerged leaves of the parents above it. It returns the leaf index.
func (t *ratchetTree) addLeaf(l *leafNode) uint32 {
	i := uint32(0)
	for ; i < t.numLeaves(); i++ {
		if t.nodes[leafNodeIndex(i)].leaf == nil {
			break
		}
	}
	if i == t.numLeaves() {
		t.extend()
	}
	x := leafNodeIndex(i)
	t.nodes[x] = node{leaf: l}
	for _, p := range directPath(x, t.numLeaves()) {
		if pn := t.nodes[p].parent; pn != nil {
			pn.unmergedLeaves = append(pn.unmergedLeaves, i)
		}
	}
	return i
}

// freeLeaf returns the leaf an external joiner takes: the leftmost blank
// leaf, or the first leaf past the end.
func (t *ratchetTree) freeLeaf() uint32 {
	i := uint32(0)
	for ; i < t.numLeaves(); i++ {
		if t.nodes[leafNodeIndex(i)].leaf == nil {
			break
		}
	}
	return i
}

// removeLeaf blanks a leaf and its path, then drops right halves of the
// tree that hold no member.
func (t *ratchetTree) removeLeaf(i uint32) {
	t.nodes[leafNodeIndex(i)] = node{}
	t.blankPath(i)
	t.truncate()
}

func (t *ratchetTree) truncate() {
	for t.numLeaves() > 1 {
		half := t.numLeaves() / 2
		for i := half; i < t.numLeaves(); i++ {
			if t.nodes[leafNodeIndex(i)].leaf != nil {
				return
			}
		}
		t.nodes = t.nodes[:nodeWidth(half)]
	}
}

// members returns the indices of the leaves that hold a member.
func (t *ratchetTree) members() []uint32 {
	var out []uint32
	for i := uint32(0); i < t.numLeaves(); i++ {
		if t.leaf(i) != nil {
			out = append(out, i)
		}
	}
	return out
}
