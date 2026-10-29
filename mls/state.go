// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package mls

import (
	"bytes"
	"errors"
	"maps"
	"slices"
)

// stateVersion is the version of the encoding MarshalBinary writes.
const stateVersion uint8 = 1

// MarshalBinary encodes the member's whole state of the group, its
// private keys and the secrets of the current epoch included, so the
// group survives a restart. The encoding is secret: store it as the
// member's private keys are stored, and replace it after every change,
// since an old copy can decrypt messages the current state has deleted
// the keys of.
func (g *Group) MarshalBinary() ([]byte, error) {
	signer, err := g.signer.Bytes()
	if err != nil {
		return nil, err
	}
	return encode(func(w *writer) {
		w.u8(stateVersion)
		w.u8(uint8(g.status))
		w.u16(uint16(g.s.id))
		g.ctx.marshal(w)
		g.tree.marshal(w)
		w.vec(g.interim)
		w.vec(g.confirmationTag)
		w.u32(g.priv.leaf)
		w.nested(func(w *writer) {
			for _, x := range slices.Sorted(maps.Keys(g.priv.keys)) {
				k := g.priv.keys[x]
				w.u32(x)
				w.vec(k.priv)
				w.vec(k.pub)
			}
		})
		w.vec(signer)
		for _, s := range g.es.list() {
			w.vec(*s)
		}
		w.optional(g.st != nil, func(w *writer) { g.st.marshal(w) })
		w.nested(func(w *writer) {
			for _, pp := range g.pending {
				w.vec(pp.ref)
				pp.sender.marshal(w)
				pp.proposal.marshal(w)
			}
		})
		w.nested(func(w *writer) {
			for _, r := range g.resumption {
				w.u64(r.epoch)
				w.vec(r.psk)
			}
		})
	}), nil
}

// list returns the epoch's secrets in their encoding order.
func (es *epochSecrets) list() []*[]byte {
	return []*[]byte{&es.senderData, &es.encryption, &es.exporter, &es.external, &es.confirmation,
		&es.membership, &es.resumption, &es.authenticator, &es.init}
}

func (t *secretTree) marshal(w *writer) {
	w.u32(t.leaves)
	w.nested(func(w *writer) {
		for _, x := range slices.Sorted(maps.Keys(t.secrets)) {
			w.u32(x)
			w.vec(t.secrets[x])
		}
	})
	w.nested(func(w *writer) {
		for _, id := range slices.Sorted(maps.Keys(t.ratchet)) {
			r := t.ratchet[id]
			w.u64(id)
			w.vec(r.secret)
			w.u32(r.generation)
			w.nested(func(w *writer) {
				for _, gen := range slices.Sorted(maps.Keys(r.skipped)) {
					kn := r.skipped[gen]
					w.u32(gen)
					w.vec(kn.key)
					w.vec(kn.nonce)
				}
			})
		}
	})
}

func unmarshalSecretTree(r *reader, s *suite) *secretTree {
	t := &secretTree{s: s, secrets: make(map[uint32][]byte), ratchet: make(map[uint64]*ratchet)}
	t.leaves = r.u32()
	r.list(func(r *reader) {
		x := r.u32()
		t.secrets[x] = r.vec()
	})
	r.list(func(r *reader) {
		id := r.u64()
		rt := &ratchet{secret: r.vec(), generation: r.u32(), skipped: make(map[uint32]keyNonce)}
		r.list(func(r *reader) {
			gen := r.u32()
			rt.skipped[gen] = keyNonce{key: r.vec(), nonce: r.vec()}
		})
		t.ratchet[id] = rt
	})
	return t
}

// UnmarshalGroup restores a group from what MarshalBinary encoded, with
// the configuration to run it under.
func UnmarshalGroup(state []byte, cfg Config) (*Group, error) {
	g := &Group{cfg: cfg}
	var signer []byte
	var tree *ratchetTree
	var cs CipherSuite
	err := decode(bytes.Clone(state), func(r *reader) {
		if v := r.u8(); r.err == nil && v != stateVersion {
			r.fail("state version %d", v)
			return
		}
		g.status = groupStatus(r.u8())
		cs = CipherSuite(r.u16())
		s, err := suiteFor(cs)
		if err != nil {
			r.fail("%v", err)
			return
		}
		g.s = s
		g.ctx.unmarshal(r)
		tree = unmarshalTree(r)
		g.interim = r.vec()
		g.confirmationTag = r.vec()
		g.priv = &privateTree{leaf: r.u32(), keys: make(map[uint32]hpkeKey)}
		r.list(func(r *reader) {
			x := r.u32()
			g.priv.keys[x] = hpkeKey{priv: r.vec(), pub: r.vec()}
		})
		signer = r.vec()
		for _, p := range g.es.list() {
			*p = r.vec()
		}
		if r.optional() {
			g.st = unmarshalSecretTree(r, s)
		}
		r.list(func(r *reader) {
			var pp pendingProposal
			pp.ref = r.vec()
			pp.sender.unmarshal(r)
			pp.proposal = new(proposal)
			pp.proposal.unmarshal(r)
			g.pending = append(g.pending, pp)
		})
		r.list(func(r *reader) {
			g.resumption = append(g.resumption, resumptionPSK{epoch: r.u64(), psk: r.vec()})
		})
	})
	if err != nil {
		return nil, err
	}
	g.tree = tree
	if g.status > statusReInit {
		return nil, errors.New("mls: state has an unknown status")
	}
	if g.ctx.suite != cs || !bytes.Equal(tree.treeHash(g.s), g.ctx.treeHash) {
		return nil, errors.New("mls: state is inconsistent")
	}
	if g.signer, err = NewSignatureKey(cs, signer); err != nil {
		return nil, err
	}
	if g.status == statusActive {
		own := tree.leaf(g.priv.leaf)
		if own == nil || !bytes.Equal(own.signatureKey, g.signer.PublicKey()) || g.st == nil || g.st.leaves != tree.numLeaves() {
			return nil, errors.New("mls: state is inconsistent")
		}
	}
	return g, nil
}

// MarshalBinary encodes the view so a delivery service keeps it across
// restarts. It holds no secret.
func (p *PublicGroup) MarshalBinary() ([]byte, error) {
	return encode(func(w *writer) {
		w.u8(stateVersion)
		w.u16(uint16(p.s.id))
		p.ctx.marshal(w)
		p.tree.marshal(w)
		w.vec(p.interim)
		w.vec(p.confirmationTag)
		w.nested(func(w *writer) {
			for _, pp := range p.pending {
				w.vec(pp.ref)
				pp.sender.marshal(w)
				pp.proposal.marshal(w)
			}
		})
	}), nil
}

// UnmarshalPublicGroup restores a view from what MarshalBinary encoded.
func UnmarshalPublicGroup(state []byte, cfg Config) (*PublicGroup, error) {
	p := &PublicGroup{cfg: cfg}
	var cs CipherSuite
	err := decode(bytes.Clone(state), func(r *reader) {
		if v := r.u8(); r.err == nil && v != stateVersion {
			r.fail("state version %d", v)
			return
		}
		cs = CipherSuite(r.u16())
		s, err := suiteFor(cs)
		if err != nil {
			r.fail("%v", err)
			return
		}
		p.s = s
		p.ctx.unmarshal(r)
		p.tree = unmarshalTree(r)
		p.interim = r.vec()
		p.confirmationTag = r.vec()
		r.list(func(r *reader) {
			var pp pendingProposal
			pp.ref = r.vec()
			pp.sender.unmarshal(r)
			pp.proposal = new(proposal)
			pp.proposal.unmarshal(r)
			p.pending = append(p.pending, pp)
		})
	})
	if err != nil {
		return nil, err
	}
	if p.ctx.suite != cs || !bytes.Equal(p.tree.treeHash(p.s), p.ctx.treeHash) {
		return nil, errors.New("mls: state is inconsistent")
	}
	return p, nil
}
