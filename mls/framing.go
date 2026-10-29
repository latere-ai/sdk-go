// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package mls

import (
	"crypto/rand"
	"fmt"
)

// authenticatedContent is a message's content with its signature and,
// for a commit, its confirmation tag, before or after protection.
type authenticatedContent struct {
	wireFormat WireFormat
	content    framedContent
	auth       framedContentAuthData
}

func (a *authenticatedContent) marshal(w *writer) {
	w.u16(uint16(a.wireFormat))
	a.content.marshal(w)
	a.auth.marshal(w, a.content.contentType)
}

func (a *authenticatedContent) unmarshal(r *reader) {
	a.wireFormat = WireFormat(r.u16())
	a.content.unmarshal(r)
	a.auth.unmarshal(r, a.content.contentType)
}

// proposalRef is the reference a commit names a proposal by.
func (s *suite) proposalRef(a *authenticatedContent) []byte {
	return s.refHash(labelPrefix+"Proposal Reference", encode(a.marshal))
}

// framedContentTBS is what a message's signature covers, RFC 9420,
// Section 6.1. A member's or a new member's commit also covers the
// group context, which binds the signature to the group and epoch.
func framedContentTBS(wf WireFormat, c *framedContent, groupContext []byte) []byte {
	return encode(func(w *writer) {
		w.u16(protocolVersionMLS10)
		w.u16(uint16(wf))
		c.marshal(w)
		if c.sender.typ == senderMember || c.sender.typ == senderNewMemberCommit {
			w.raw(groupContext)
		}
	})
}

func (s *suite) signContent(key *SignatureKey, a *authenticatedContent, groupContext []byte) error {
	sig, err := s.signWithLabel(key, "FramedContentTBS", framedContentTBS(a.wireFormat, &a.content, groupContext))
	if err != nil {
		return err
	}
	a.auth.signature = sig
	return nil
}

func (s *suite) verifyContent(pub []byte, a *authenticatedContent, groupContext []byte) error {
	return s.verifyWithLabel(pub, "FramedContentTBS", framedContentTBS(a.wireFormat, &a.content, groupContext), a.auth.signature)
}

// membershipTag is the MAC that proves a public message's sender holds
// the epoch's membership key.
func (s *suite) membershipTag(membershipKey []byte, a *authenticatedContent, groupContext []byte) []byte {
	return s.mac(membershipKey, encode(func(w *writer) {
		w.raw(framedContentTBS(a.wireFormat, &a.content, groupContext))
		a.auth.marshal(w, a.content.contentType)
	}))
}

// DefaultPaddingBlock is the multiple of bytes a private message's
// content is padded to when Config sets no other, so its length tells
// little about what it says.
const DefaultPaddingBlock = 32

func ratchetTypeFor(ct contentType) ratchetType {
	if ct == contentApplication {
		return ratchetApplication
	}
	return ratchetHandshake
}

func privateContentAAD(groupID []byte, epoch uint64, ct contentType, ad []byte) []byte {
	return encode(func(w *writer) {
		w.vec(groupID)
		w.u64(epoch)
		w.u8(uint8(ct))
		w.vec(ad)
	})
}

func senderDataAAD(groupID []byte, epoch uint64, ct contentType) []byte {
	return encode(func(w *writer) {
		w.vec(groupID)
		w.u64(epoch)
		w.u8(uint8(ct))
	})
}

func (s *suite) senderDataKeyNonce(senderDataSecret, ciphertext []byte) (key, nonce []byte) {
	sample := ciphertext
	if len(sample) > s.nh {
		sample = sample[:s.nh]
	}
	return s.expandWithLabel(senderDataSecret, "key", sample, s.nk),
		s.expandWithLabel(senderDataSecret, "nonce", sample, s.nn)
}

func applyReuseGuard(nonce []byte, guard [4]byte) []byte {
	out := append([]byte{}, nonce...)
	for i := range guard {
		out[i] ^= guard[i]
	}
	return out
}

// encryptContent protects a member's content as a PrivateMessage with
// the next key of the sender's ratchet, RFC 9420, Section 6.3.
func (s *suite) encryptContent(st *secretTree, senderDataSecret []byte, a *authenticatedContent, padBlock int) (*privateMessage, error) {
	c := &a.content
	if c.sender.typ != senderMember {
		return nil, fmt.Errorf("mls: only a member sends a private message")
	}
	kn, gen, err := st.next(c.sender.index, ratchetTypeFor(c.contentType))
	if err != nil {
		return nil, err
	}
	var guard [4]byte
	if _, err := rand.Read(guard[:]); err != nil {
		return nil, err
	}
	pt := encode(func(w *writer) {
		c.marshalBody(w)
		a.auth.marshal(w, c.contentType)
	})
	if padBlock <= 0 {
		padBlock = DefaultPaddingBlock
	}
	if rem := len(pt) % padBlock; rem != 0 {
		pt = append(pt, make([]byte, padBlock-rem)...)
	}
	ct, err := s.seal(kn.key, applyReuseGuard(kn.nonce, guard), privateContentAAD(c.groupID, c.epoch, c.contentType, c.authenticatedData), pt)
	if err != nil {
		return nil, err
	}
	sd := encode(func(w *writer) {
		w.u32(c.sender.index)
		w.u32(gen)
		w.raw(guard[:])
	})
	sdKey, sdNonce := s.senderDataKeyNonce(senderDataSecret, ct)
	esd, err := s.seal(sdKey, sdNonce, senderDataAAD(c.groupID, c.epoch, c.contentType), sd)
	if err != nil {
		return nil, err
	}
	return &privateMessage{
		groupID:             c.groupID,
		epoch:               c.epoch,
		contentType:         c.contentType,
		authenticatedData:   c.authenticatedData,
		encryptedSenderData: esd,
		ciphertext:          ct,
	}, nil
}

// decryptContent opens a PrivateMessage. checkSender vets the sender's
// leaf before any key of its ratchet is derived. It returns the content,
// whose signature the caller still checks, and a function that moves the
// sender's ratchet past the message once the caller has.
func (s *suite) decryptContent(st *secretTree, senderDataSecret []byte, m *privateMessage, checkSender func(leaf uint32) error) (*authenticatedContent, func(), error) {
	sdKey, sdNonce := s.senderDataKeyNonce(senderDataSecret, m.ciphertext)
	sd, err := s.open(sdKey, sdNonce, senderDataAAD(m.groupID, m.epoch, m.contentType), m.encryptedSenderData)
	if err != nil {
		return nil, nil, fmt.Errorf("mls: sender data: %w", err)
	}
	var leaf, gen uint32
	var guard [4]byte
	if err := decode(sd, func(r *reader) {
		leaf = r.u32()
		gen = r.u32()
		copy(guard[:], r.take(4))
	}); err != nil {
		return nil, nil, err
	}
	if err := checkSender(leaf); err != nil {
		return nil, nil, err
	}
	kn, advance, err := st.get(leaf, ratchetTypeFor(m.contentType), gen)
	if err != nil {
		return nil, nil, err
	}
	pt, err := s.open(kn.key, applyReuseGuard(kn.nonce, guard), privateContentAAD(m.groupID, m.epoch, m.contentType, m.authenticatedData), m.ciphertext)
	if err != nil {
		return nil, nil, err
	}
	a := &authenticatedContent{
		wireFormat: WireFormatPrivateMessage,
		content: framedContent{
			groupID:           m.groupID,
			epoch:             m.epoch,
			sender:            sender{typ: senderMember, index: leaf},
			authenticatedData: m.authenticatedData,
			contentType:       m.contentType,
		},
	}
	r := newReader(pt)
	a.content.unmarshalBody(r)
	a.auth.unmarshal(r, m.contentType)
	if r.err != nil {
		return nil, nil, r.err
	}
	for _, b := range r.b {
		if b != 0 {
			return nil, nil, fmt.Errorf("%w: padding is not all zero", errDecode)
		}
	}
	return a, advance, nil
}
