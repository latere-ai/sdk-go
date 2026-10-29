// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package mls

import "math/bits"

// Tree math over the array representation of a ratchet tree, RFC 9420,
// Appendix C. Leaves sit at even node indices, a node's level is the
// number of trailing one bits of its index, and the tree always holds a
// power of two of leaves.

// level returns the height of node x above the leaves.
func level(x uint32) uint32 {
	return uint32(bits.TrailingZeros32(^x))
}

func isLeaf(x uint32) bool { return x&1 == 0 }

func leafNodeIndex(leaf uint32) uint32 { return 2 * leaf }

func nodeLeafIndex(x uint32) uint32 { return x / 2 }

// nodeWidth returns the number of nodes in a tree with n leaves.
func nodeWidth(n uint32) uint32 {
	if n == 0 {
		return 0
	}
	return 2*(n-1) + 1
}

// root returns the root's node index in a tree with n leaves.
func root(n uint32) uint32 {
	w := nodeWidth(n)
	return 1<<(31-bits.LeadingZeros32(w)) - 1
}

// left and right return the children of the parent node x.
func left(x uint32) uint32 {
	k := level(x)
	return x ^ (1 << (k - 1))
}

func right(x uint32) uint32 {
	k := level(x)
	return x ^ (3 << (k - 1))
}

// parent returns the parent of a node that is not the root.
func parent(x uint32) uint32 {
	k := level(x)
	b := (x >> (k + 1)) & 1
	return (x | (1 << k)) ^ (b << (k + 1))
}

// sibling returns the other child of x's parent.
func sibling(x uint32) uint32 {
	p := parent(x)
	if x < p {
		return right(p)
	}
	return left(p)
}

// directPath returns the ancestors of x from its parent to the root.
func directPath(x, n uint32) []uint32 {
	r := root(n)
	var d []uint32
	for x != r {
		x = parent(x)
		d = append(d, x)
	}
	return d
}

// inSubtree reports whether node y is x or one of its descendants.
func inSubtree(x, y uint32) bool {
	span := uint32(1)<<level(x) - 1
	return y >= x-span && y <= x+span
}

// commonAncestor returns the lowest node whose subtree holds both leaves
// x and y.
func commonAncestor(x, y uint32) uint32 {
	if x == y {
		return x
	}
	k := uint32(0)
	for x != y {
		x, y = x>>1, y>>1
		k++
	}
	return x<<k + 1<<(k-1) - 1
}
