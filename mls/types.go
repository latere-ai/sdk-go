// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package mls

import (
	"bytes"
	"fmt"
	"slices"
)

// The structures of RFC 9420 and their encodings. Each type has marshal,
// which appends its encoding, and unmarshal, which reads it and leaves an
// error on the reader for malformed input. A decoded value aliases the
// bytes it was read from; the exported entry points copy a caller's
// bytes before decoding, so nothing aliases memory the caller reuses.

const protocolVersionMLS10 uint16 = 1

// WireFormat is the kind of an MLSMessage: a public or private message
// of a group, a Welcome, a group info or a key package.
type WireFormat uint16

// The wire formats of RFC 9420, Section 6.
const (
	WireFormatPublicMessage  WireFormat = 1
	WireFormatPrivateMessage WireFormat = 2
	WireFormatWelcome        WireFormat = 3
	WireFormatGroupInfo      WireFormat = 4
	WireFormatKeyPackage     WireFormat = 5
)

// ExtensionType is the code point of an extension, RFC 9420, Section
// 17.3. An application defines its own in the private-use range from
// 0xF000.
type ExtensionType uint16

// The extension types RFC 9420 defines. Every client supports them, so a
// leaf's capabilities do not list them.
const (
	ExtensionApplicationID        ExtensionType = 0x0001
	ExtensionRatchetTree          ExtensionType = 0x0002
	ExtensionRequiredCapabilities ExtensionType = 0x0003
	ExtensionExternalPub          ExtensionType = 0x0004
	ExtensionExternalSenders      ExtensionType = 0x0005
)

func (t ExtensionType) isDefault() bool {
	return t >= ExtensionApplicationID && t <= ExtensionExternalSenders
}

// Extension is a typed value carried in a group context, a group info, a
// key package or a leaf node.
type Extension struct {
	Type ExtensionType
	Data []byte
}

func marshalExtensions(w *writer, exts []Extension) {
	w.nested(func(w *writer) {
		for _, e := range exts {
			w.u16(uint16(e.Type))
			w.vec(e.Data)
		}
	})
}

func unmarshalExtensions(r *reader) []Extension {
	var exts []Extension
	r.list(func(r *reader) {
		exts = append(exts, Extension{Type: ExtensionType(r.u16()), Data: r.vec()})
	})
	return exts
}

// uniqueExtensions reports an error when a list names a type twice,
// which would leave which of the two applies to the reader.
func uniqueExtensions(exts []Extension) error {
	for i, e := range exts {
		for _, o := range exts[:i] {
			if o.Type == e.Type {
				return fmt.Errorf("mls: extension 0x%04x listed twice", uint16(e.Type))
			}
		}
	}
	return nil
}

func findExtension(exts []Extension, t ExtensionType) (Extension, bool) {
	for _, e := range exts {
		if e.Type == t {
			return e, true
		}
	}
	return Extension{}, false
}

func cloneExtensions(exts []Extension) []Extension {
	out := make([]Extension, len(exts))
	for i, e := range exts {
		out[i] = Extension{Type: e.Type, Data: bytes.Clone(e.Data)}
	}
	return out
}

// clone returns a copy of the credential that shares no memory with it.
func (c Credential) clone() Credential {
	out := Credential{Type: c.Type, Identity: bytes.Clone(c.Identity)}
	for _, cert := range c.Certificates {
		out.Certificates = append(out.Certificates, bytes.Clone(cert))
	}
	return out
}

// equal reports whether two credentials are the same.
func (c Credential) equal(o Credential) bool {
	return c.Type == o.Type && bytes.Equal(c.Identity, o.Identity) &&
		slices.EqualFunc(c.Certificates, o.Certificates, bytes.Equal)
}

// CredentialType is the kind of a member's credential.
type CredentialType uint16

// The credential types RFC 9420 defines.
const (
	CredentialBasic CredentialType = 0x0001
	CredentialX509  CredentialType = 0x0002
)

// Credential binds a member's identity to its signature key. The
// application decides what an identity means and whether a credential
// is valid for the key it comes with.
type Credential struct {
	Type CredentialType
	// Identity is a basic credential's identity.
	Identity []byte
	// Certificates is an X.509 credential's chain, leaf first, each
	// certificate DER-encoded.
	Certificates [][]byte
}

// BasicCredential returns a basic credential for the identity.
func BasicCredential(identity []byte) Credential {
	return Credential{Type: CredentialBasic, Identity: bytes.Clone(identity)}
}

func (c *Credential) marshal(w *writer) {
	w.u16(uint16(c.Type))
	switch c.Type {
	case CredentialBasic:
		w.vec(c.Identity)
	case CredentialX509:
		w.nested(func(w *writer) {
			for _, cert := range c.Certificates {
				w.vec(cert)
			}
		})
	}
}

func (c *Credential) unmarshal(r *reader) {
	c.Type = CredentialType(r.u16())
	switch c.Type {
	case CredentialBasic:
		c.Identity = r.vec()
	case CredentialX509:
		r.list(func(r *reader) { c.Certificates = append(c.Certificates, r.vec()) })
	default:
		r.fail("credential type 0x%04x", uint16(c.Type))
	}
}

// Capabilities lists what a client supports: its protocol versions,
// cipher suites, credential types, and the extension and proposal types
// beyond the ones every client supports.
type Capabilities struct {
	Versions     []uint16
	CipherSuites []CipherSuite
	Extensions   []ExtensionType
	Proposals    []uint16
	Credentials  []CredentialType
}

// clone returns a copy of the capabilities that shares no memory with
// them.
func (c Capabilities) clone() Capabilities {
	return Capabilities{
		Versions:     slices.Clone(c.Versions),
		CipherSuites: slices.Clone(c.CipherSuites),
		Extensions:   slices.Clone(c.Extensions),
		Proposals:    slices.Clone(c.Proposals),
		Credentials:  slices.Clone(c.Credentials),
	}
}

func (c *Capabilities) marshal(w *writer) {
	w.nested(func(w *writer) {
		for _, v := range c.Versions {
			w.u16(v)
		}
	})
	w.nested(func(w *writer) {
		for _, v := range c.CipherSuites {
			w.u16(uint16(v))
		}
	})
	w.nested(func(w *writer) {
		for _, v := range c.Extensions {
			w.u16(uint16(v))
		}
	})
	w.nested(func(w *writer) {
		for _, v := range c.Proposals {
			w.u16(v)
		}
	})
	w.nested(func(w *writer) {
		for _, v := range c.Credentials {
			w.u16(uint16(v))
		}
	})
}

func (c *Capabilities) unmarshal(r *reader) {
	r.list(func(r *reader) { c.Versions = append(c.Versions, r.u16()) })
	r.list(func(r *reader) { c.CipherSuites = append(c.CipherSuites, CipherSuite(r.u16())) })
	r.list(func(r *reader) { c.Extensions = append(c.Extensions, ExtensionType(r.u16())) })
	r.list(func(r *reader) { c.Proposals = append(c.Proposals, r.u16()) })
	r.list(func(r *reader) { c.Credentials = append(c.Credentials, CredentialType(r.u16())) })
}

// supportsExtension reports whether a leaf with these capabilities can
// hold or process an extension of type t.
func (c *Capabilities) supportsExtension(t ExtensionType) bool {
	return t.isDefault() || slices.Contains(c.Extensions, t)
}

func (c *Capabilities) supportsProposal(t proposalType) bool {
	return t.isDefault() || slices.Contains(c.Proposals, uint16(t))
}

// Lifetime bounds when a key package's leaf is valid, in seconds since
// the Unix epoch.
type Lifetime struct {
	NotBefore uint64
	NotAfter  uint64
}

type leafNodeSource uint8

const (
	leafSourceKeyPackage leafNodeSource = 1
	leafSourceUpdate     leafNodeSource = 2
	leafSourceCommit     leafNodeSource = 3
)

// leafNode is a member's leaf in the ratchet tree. A leaf node in the
// tree is never changed in place; a change replaces it.
type leafNode struct {
	encryptionKey []byte
	signatureKey  []byte
	credential    Credential
	capabilities  Capabilities
	source        leafNodeSource
	lifetime      Lifetime // for source key_package
	parentHash    []byte   // for source commit
	extensions    []Extension
	signature     []byte
}

func (l *leafNode) marshalContent(w *writer) {
	w.vec(l.encryptionKey)
	w.vec(l.signatureKey)
	l.credential.marshal(w)
	l.capabilities.marshal(w)
	w.u8(uint8(l.source))
	switch l.source {
	case leafSourceKeyPackage:
		w.u64(l.lifetime.NotBefore)
		w.u64(l.lifetime.NotAfter)
	case leafSourceCommit:
		w.vec(l.parentHash)
	}
	marshalExtensions(w, l.extensions)
}

func (l *leafNode) marshal(w *writer) {
	l.marshalContent(w)
	w.vec(l.signature)
}

// tbs returns the LeafNodeTBS the signature covers. A leaf made for a
// group, by an update or a commit, binds the group and its position.
func (l *leafNode) tbs(groupID []byte, leafIndex uint32) []byte {
	return encode(func(w *writer) {
		l.marshalContent(w)
		if l.source == leafSourceUpdate || l.source == leafSourceCommit {
			w.vec(groupID)
			w.u32(leafIndex)
		}
	})
}

func (l *leafNode) unmarshal(r *reader) {
	l.encryptionKey = r.vec()
	l.signatureKey = r.vec()
	l.credential.unmarshal(r)
	l.capabilities.unmarshal(r)
	l.source = leafNodeSource(r.u8())
	switch l.source {
	case leafSourceKeyPackage:
		l.lifetime.NotBefore = r.u64()
		l.lifetime.NotAfter = r.u64()
	case leafSourceUpdate:
	case leafSourceCommit:
		l.parentHash = r.vec()
	default:
		r.fail("leaf node source %d", l.source)
	}
	l.extensions = unmarshalExtensions(r)
	l.signature = r.vec()
}

func (l *leafNode) sign(s *suite, key *SignatureKey, groupID []byte, leafIndex uint32) error {
	sig, err := s.signWithLabel(key, "LeafNodeTBS", l.tbs(groupID, leafIndex))
	if err != nil {
		return err
	}
	l.signature = sig
	return nil
}

func (l *leafNode) verify(s *suite, groupID []byte, leafIndex uint32) error {
	return s.verifyWithLabel(l.signatureKey, "LeafNodeTBS", l.tbs(groupID, leafIndex), l.signature)
}

// parentNode is a parent in the ratchet tree. Copies are made before a
// change, since a staged commit shares nodes with the state it came from.
type parentNode struct {
	encryptionKey  []byte
	parentHash     []byte
	unmergedLeaves []uint32
}

func (p *parentNode) marshal(w *writer) {
	w.vec(p.encryptionKey)
	w.vec(p.parentHash)
	w.nested(func(w *writer) {
		for _, l := range p.unmergedLeaves {
			w.u32(l)
		}
	})
}

func (p *parentNode) unmarshal(r *reader) {
	p.encryptionKey = r.vec()
	p.parentHash = r.vec()
	r.list(func(r *reader) { p.unmergedLeaves = append(p.unmergedLeaves, r.u32()) })
}

func (p *parentNode) clone() *parentNode {
	return &parentNode{
		encryptionKey:  p.encryptionKey,
		parentHash:     p.parentHash,
		unmergedLeaves: slices.Clone(p.unmergedLeaves),
	}
}

const (
	nodeTypeLeaf   uint8 = 1
	nodeTypeParent uint8 = 2
)

// keyPackage is a client's offer to be added to a group.
type keyPackage struct {
	version    uint16
	suite      CipherSuite
	initKey    []byte
	leaf       leafNode
	extensions []Extension
	signature  []byte
}

func (k *keyPackage) marshalContent(w *writer) {
	w.u16(k.version)
	w.u16(uint16(k.suite))
	w.vec(k.initKey)
	k.leaf.marshal(w)
	marshalExtensions(w, k.extensions)
}

func (k *keyPackage) marshal(w *writer) {
	k.marshalContent(w)
	w.vec(k.signature)
}

func (k *keyPackage) unmarshal(r *reader) {
	k.version = r.u16()
	k.suite = CipherSuite(r.u16())
	k.initKey = r.vec()
	k.leaf.unmarshal(r)
	k.extensions = unmarshalExtensions(r)
	k.signature = r.vec()
}

func (k *keyPackage) ref(s *suite) []byte {
	return s.refHash(labelPrefix+"KeyPackage Reference", encode(k.marshal))
}

type proposalType uint16

const (
	proposalAdd                    proposalType = 1
	proposalUpdate                 proposalType = 2
	proposalRemove                 proposalType = 3
	proposalPSK                    proposalType = 4
	proposalReInit                 proposalType = 5
	proposalExternalInit           proposalType = 6
	proposalGroupContextExtensions proposalType = 7
)

func (t proposalType) isDefault() bool {
	return t >= proposalAdd && t <= proposalGroupContextExtensions
}

// pathRequired reports whether a commit covering a proposal of type t
// must carry an update path, RFC 9420, Section 12.4.
func (t proposalType) pathRequired() bool {
	switch t {
	case proposalAdd, proposalPSK, proposalReInit:
		return false
	}
	return true
}

// externalAllowed reports whether an external sender may propose t,
// RFC 9420, Section 12.1.8.
func (t proposalType) externalAllowed() bool {
	switch t {
	case proposalAdd, proposalRemove, proposalPSK, proposalReInit, proposalGroupContextExtensions:
		return true
	}
	return false
}

type pskType uint8

const (
	pskExternal   pskType = 1
	pskResumption pskType = 2
)

type resumptionUsage uint8

const (
	resumptionApplication resumptionUsage = 1
	resumptionReInit      resumptionUsage = 2
	resumptionBranch      resumptionUsage = 3
)

// preSharedKeyID names a PSK injected into the key schedule.
type preSharedKeyID struct {
	typ     pskType
	id      []byte // external
	usage   resumptionUsage
	groupID []byte // resumption
	epoch   uint64 // resumption
	nonce   []byte
}

func (p *preSharedKeyID) marshal(w *writer) {
	w.u8(uint8(p.typ))
	switch p.typ {
	case pskExternal:
		w.vec(p.id)
	case pskResumption:
		w.u8(uint8(p.usage))
		w.vec(p.groupID)
		w.u64(p.epoch)
	}
	w.vec(p.nonce)
}

func (p *preSharedKeyID) unmarshal(r *reader) {
	p.typ = pskType(r.u8())
	switch p.typ {
	case pskExternal:
		p.id = r.vec()
	case pskResumption:
		p.usage = resumptionUsage(r.u8())
		if p.usage < resumptionApplication || p.usage > resumptionBranch {
			r.fail("resumption usage %d", p.usage)
		}
		p.groupID = r.vec()
		p.epoch = r.u64()
	default:
		r.fail("PSK type %d", p.typ)
	}
	p.nonce = r.vec()
}

type reInit struct {
	groupID    []byte
	version    uint16
	suite      CipherSuite
	extensions []Extension
}

// proposal is one change to a group. Exactly the field its type names is
// set.
type proposal struct {
	typ          proposalType
	add          *keyPackage
	update       *leafNode
	remove       uint32
	psk          preSharedKeyID
	reinit       reInit
	externalInit []byte // kem_output
	extensions   []Extension
}

func (p *proposal) marshal(w *writer) {
	w.u16(uint16(p.typ))
	switch p.typ {
	case proposalAdd:
		p.add.marshal(w)
	case proposalUpdate:
		p.update.marshal(w)
	case proposalRemove:
		w.u32(p.remove)
	case proposalPSK:
		p.psk.marshal(w)
	case proposalReInit:
		w.vec(p.reinit.groupID)
		w.u16(p.reinit.version)
		w.u16(uint16(p.reinit.suite))
		marshalExtensions(w, p.reinit.extensions)
	case proposalExternalInit:
		w.vec(p.externalInit)
	case proposalGroupContextExtensions:
		marshalExtensions(w, p.extensions)
	}
}

func (p *proposal) unmarshal(r *reader) {
	p.typ = proposalType(r.u16())
	switch p.typ {
	case proposalAdd:
		p.add = new(keyPackage)
		p.add.unmarshal(r)
	case proposalUpdate:
		p.update = new(leafNode)
		p.update.unmarshal(r)
	case proposalRemove:
		p.remove = r.u32()
	case proposalPSK:
		p.psk.unmarshal(r)
	case proposalReInit:
		p.reinit.groupID = r.vec()
		p.reinit.version = r.u16()
		p.reinit.suite = CipherSuite(r.u16())
		p.reinit.extensions = unmarshalExtensions(r)
	case proposalExternalInit:
		p.externalInit = r.vec()
	case proposalGroupContextExtensions:
		p.extensions = unmarshalExtensions(r)
	default:
		r.fail("proposal type 0x%04x", uint16(p.typ))
	}
}

const (
	proposalOrRefProposal  uint8 = 1
	proposalOrRefReference uint8 = 2
)

type proposalOrRef struct {
	proposal  *proposal // inline, when ref is nil
	reference []byte
}

func (p *proposalOrRef) marshal(w *writer) {
	if p.proposal != nil {
		w.u8(proposalOrRefProposal)
		p.proposal.marshal(w)
		return
	}
	w.u8(proposalOrRefReference)
	w.vec(p.reference)
}

func (p *proposalOrRef) unmarshal(r *reader) {
	switch t := r.u8(); t {
	case proposalOrRefProposal:
		p.proposal = new(proposal)
		p.proposal.unmarshal(r)
	case proposalOrRefReference:
		p.reference = r.vec()
	default:
		r.fail("proposal-or-reference type %d", t)
	}
}

type hpkeCiphertext struct {
	kemOutput  []byte
	ciphertext []byte
}

func (c *hpkeCiphertext) marshal(w *writer) {
	w.vec(c.kemOutput)
	w.vec(c.ciphertext)
}

func (c *hpkeCiphertext) unmarshal(r *reader) {
	c.kemOutput = r.vec()
	c.ciphertext = r.vec()
}

type updatePathNode struct {
	encryptionKey       []byte
	encryptedPathSecret []hpkeCiphertext
}

type updatePath struct {
	leaf  leafNode
	nodes []updatePathNode
}

func (u *updatePath) marshal(w *writer) {
	u.leaf.marshal(w)
	w.nested(func(w *writer) {
		for i := range u.nodes {
			n := &u.nodes[i]
			w.vec(n.encryptionKey)
			w.nested(func(w *writer) {
				for j := range n.encryptedPathSecret {
					n.encryptedPathSecret[j].marshal(w)
				}
			})
		}
	})
}

func (u *updatePath) unmarshal(r *reader) {
	u.leaf.unmarshal(r)
	r.list(func(r *reader) {
		var n updatePathNode
		n.encryptionKey = r.vec()
		r.list(func(r *reader) {
			var c hpkeCiphertext
			c.unmarshal(r)
			n.encryptedPathSecret = append(n.encryptedPathSecret, c)
		})
		u.nodes = append(u.nodes, n)
	})
}

type commit struct {
	proposals []proposalOrRef
	path      *updatePath
}

func (c *commit) marshal(w *writer) {
	w.nested(func(w *writer) {
		for i := range c.proposals {
			c.proposals[i].marshal(w)
		}
	})
	w.optional(c.path != nil, func(w *writer) { c.path.marshal(w) })
}

func (c *commit) unmarshal(r *reader) {
	r.list(func(r *reader) {
		var p proposalOrRef
		p.unmarshal(r)
		c.proposals = append(c.proposals, p)
	})
	if r.optional() {
		c.path = new(updatePath)
		c.path.unmarshal(r)
	}
}

type senderType uint8

const (
	senderMember            senderType = 1
	senderExternal          senderType = 2
	senderNewMemberProposal senderType = 3
	senderNewMemberCommit   senderType = 4
)

type sender struct {
	typ   senderType
	index uint32 // the leaf index of a member, the position of an external sender
}

func (s *sender) marshal(w *writer) {
	w.u8(uint8(s.typ))
	if s.typ == senderMember || s.typ == senderExternal {
		w.u32(s.index)
	}
}

func (s *sender) unmarshal(r *reader) {
	s.typ = senderType(r.u8())
	switch s.typ {
	case senderMember, senderExternal:
		s.index = r.u32()
	case senderNewMemberProposal, senderNewMemberCommit:
	default:
		r.fail("sender type %d", s.typ)
	}
}

type contentType uint8

const (
	contentApplication contentType = 1
	contentProposal    contentType = 2
	contentCommit      contentType = 3
)

// framedContent is a message's content before it is protected.
type framedContent struct {
	groupID           []byte
	epoch             uint64
	sender            sender
	authenticatedData []byte
	contentType       contentType
	applicationData   []byte
	proposal          *proposal
	commit            *commit
}

func (c *framedContent) marshalBody(w *writer) {
	switch c.contentType {
	case contentApplication:
		w.vec(c.applicationData)
	case contentProposal:
		c.proposal.marshal(w)
	case contentCommit:
		c.commit.marshal(w)
	}
}

func (c *framedContent) unmarshalBody(r *reader) {
	switch c.contentType {
	case contentApplication:
		c.applicationData = r.vec()
	case contentProposal:
		c.proposal = new(proposal)
		c.proposal.unmarshal(r)
	case contentCommit:
		c.commit = new(commit)
		c.commit.unmarshal(r)
	default:
		r.fail("content type %d", c.contentType)
	}
}

func (c *framedContent) marshal(w *writer) {
	w.vec(c.groupID)
	w.u64(c.epoch)
	c.sender.marshal(w)
	w.vec(c.authenticatedData)
	w.u8(uint8(c.contentType))
	c.marshalBody(w)
}

func (c *framedContent) unmarshal(r *reader) {
	c.groupID = r.vec()
	c.epoch = r.u64()
	c.sender.unmarshal(r)
	c.authenticatedData = r.vec()
	c.contentType = contentType(r.u8())
	c.unmarshalBody(r)
}

type framedContentAuthData struct {
	signature       []byte
	confirmationTag []byte // for a commit
}

func (a *framedContentAuthData) marshal(w *writer, ct contentType) {
	w.vec(a.signature)
	if ct == contentCommit {
		w.vec(a.confirmationTag)
	}
}

func (a *framedContentAuthData) unmarshal(r *reader, ct contentType) {
	a.signature = r.vec()
	if ct == contentCommit {
		a.confirmationTag = r.vec()
	}
}

type publicMessage struct {
	content       framedContent
	auth          framedContentAuthData
	membershipTag []byte // for a member sender
}

func (m *publicMessage) marshal(w *writer) {
	m.content.marshal(w)
	m.auth.marshal(w, m.content.contentType)
	if m.content.sender.typ == senderMember {
		w.vec(m.membershipTag)
	}
}

func (m *publicMessage) unmarshal(r *reader) {
	m.content.unmarshal(r)
	m.auth.unmarshal(r, m.content.contentType)
	if m.content.sender.typ == senderMember {
		m.membershipTag = r.vec()
	}
}

type privateMessage struct {
	groupID             []byte
	epoch               uint64
	contentType         contentType
	authenticatedData   []byte
	encryptedSenderData []byte
	ciphertext          []byte
}

func (m *privateMessage) marshal(w *writer) {
	w.vec(m.groupID)
	w.u64(m.epoch)
	w.u8(uint8(m.contentType))
	w.vec(m.authenticatedData)
	w.vec(m.encryptedSenderData)
	w.vec(m.ciphertext)
}

func (m *privateMessage) unmarshal(r *reader) {
	m.groupID = r.vec()
	m.epoch = r.u64()
	m.contentType = contentType(r.u8())
	if m.contentType < contentApplication || m.contentType > contentCommit {
		r.fail("content type %d", m.contentType)
	}
	m.authenticatedData = r.vec()
	m.encryptedSenderData = r.vec()
	m.ciphertext = r.vec()
}

// groupContext summarizes a group's state in an epoch.
type groupContext struct {
	version                 uint16
	suite                   CipherSuite
	groupID                 []byte
	epoch                   uint64
	treeHash                []byte
	confirmedTranscriptHash []byte
	extensions              []Extension
}

func (g *groupContext) marshal(w *writer) {
	w.u16(g.version)
	w.u16(uint16(g.suite))
	w.vec(g.groupID)
	w.u64(g.epoch)
	w.vec(g.treeHash)
	w.vec(g.confirmedTranscriptHash)
	marshalExtensions(w, g.extensions)
}

func (g *groupContext) unmarshal(r *reader) {
	g.version = r.u16()
	g.suite = CipherSuite(r.u16())
	g.groupID = r.vec()
	g.epoch = r.u64()
	g.treeHash = r.vec()
	g.confirmedTranscriptHash = r.vec()
	g.extensions = unmarshalExtensions(r)
}

func (g *groupContext) bytes() []byte { return encode(g.marshal) }

type groupInfo struct {
	context         groupContext
	extensions      []Extension
	confirmationTag []byte
	signer          uint32
	signature       []byte
}

func (g *groupInfo) marshalTBS(w *writer) {
	g.context.marshal(w)
	marshalExtensions(w, g.extensions)
	w.vec(g.confirmationTag)
	w.u32(g.signer)
}

func (g *groupInfo) marshal(w *writer) {
	g.marshalTBS(w)
	w.vec(g.signature)
}

func (g *groupInfo) unmarshal(r *reader) {
	g.context.unmarshal(r)
	g.extensions = unmarshalExtensions(r)
	g.confirmationTag = r.vec()
	g.signer = r.u32()
	g.signature = r.vec()
}

type groupSecrets struct {
	joinerSecret []byte
	pathSecret   []byte // nil when absent
	psks         []preSharedKeyID
}

func (g *groupSecrets) marshal(w *writer) {
	w.vec(g.joinerSecret)
	w.optional(g.pathSecret != nil, func(w *writer) { w.vec(g.pathSecret) })
	w.nested(func(w *writer) {
		for i := range g.psks {
			g.psks[i].marshal(w)
		}
	})
}

func (g *groupSecrets) unmarshal(r *reader) {
	g.joinerSecret = r.vec()
	if r.optional() {
		g.pathSecret = r.vec()
		if g.pathSecret == nil {
			g.pathSecret = []byte{}
		}
	}
	r.list(func(r *reader) {
		var p preSharedKeyID
		p.unmarshal(r)
		g.psks = append(g.psks, p)
	})
}

type encryptedGroupSecrets struct {
	newMember []byte
	secrets   hpkeCiphertext
}

type welcome struct {
	suite              CipherSuite
	secrets            []encryptedGroupSecrets
	encryptedGroupInfo []byte
}

func (m *welcome) marshal(w *writer) {
	w.u16(uint16(m.suite))
	w.nested(func(w *writer) {
		for i := range m.secrets {
			w.vec(m.secrets[i].newMember)
			m.secrets[i].secrets.marshal(w)
		}
	})
	w.vec(m.encryptedGroupInfo)
}

func (m *welcome) unmarshal(r *reader) {
	m.suite = CipherSuite(r.u16())
	r.list(func(r *reader) {
		var e encryptedGroupSecrets
		e.newMember = r.vec()
		e.secrets.unmarshal(r)
		m.secrets = append(m.secrets, e)
	})
	m.encryptedGroupInfo = r.vec()
}

// mlsMessage is the envelope every message travels in. Exactly the field
// its wire format names is set.
type mlsMessage struct {
	wireFormat WireFormat
	public     *publicMessage
	private    *privateMessage
	welcome    *welcome
	groupInfo  *groupInfo
	keyPackage *keyPackage
}

func (m *mlsMessage) marshal(w *writer) {
	w.u16(protocolVersionMLS10)
	w.u16(uint16(m.wireFormat))
	switch m.wireFormat {
	case WireFormatPublicMessage:
		m.public.marshal(w)
	case WireFormatPrivateMessage:
		m.private.marshal(w)
	case WireFormatWelcome:
		m.welcome.marshal(w)
	case WireFormatGroupInfo:
		m.groupInfo.marshal(w)
	case WireFormatKeyPackage:
		m.keyPackage.marshal(w)
	}
}

func (m *mlsMessage) unmarshal(r *reader) {
	if v := r.u16(); r.err == nil && v != protocolVersionMLS10 {
		r.fail("protocol version %d", v)
		return
	}
	m.wireFormat = WireFormat(r.u16())
	switch m.wireFormat {
	case WireFormatPublicMessage:
		m.public = new(publicMessage)
		m.public.unmarshal(r)
	case WireFormatPrivateMessage:
		m.private = new(privateMessage)
		m.private.unmarshal(r)
	case WireFormatWelcome:
		m.welcome = new(welcome)
		m.welcome.unmarshal(r)
	case WireFormatGroupInfo:
		m.groupInfo = new(groupInfo)
		m.groupInfo.unmarshal(r)
	case WireFormatKeyPackage:
		m.keyPackage = new(keyPackage)
		m.keyPackage.unmarshal(r)
	default:
		r.fail("wire format %d", m.wireFormat)
	}
}

// parseMessage decodes an MLSMessage from a private copy of b.
func parseMessage(b []byte) (*mlsMessage, error) {
	m := new(mlsMessage)
	if err := decode(bytes.Clone(b), m.unmarshal); err != nil {
		return nil, err
	}
	return m, nil
}

// externalSender is an entry of the external_senders extension.
type externalSender struct {
	signatureKey []byte
	credential   Credential
}

func unmarshalExternalSenders(data []byte) ([]externalSender, error) {
	var out []externalSender
	err := decode(data, func(r *reader) {
		r.list(func(r *reader) {
			var e externalSender
			e.signatureKey = r.vec()
			e.credential.unmarshal(r)
			out = append(out, e)
		})
	})
	return out, err
}

func marshalExternalSenders(senders []externalSender) []byte {
	return encode(func(w *writer) {
		w.nested(func(w *writer) {
			for i := range senders {
				w.vec(senders[i].signatureKey)
				senders[i].credential.marshal(w)
			}
		})
	})
}

// requiredCapabilities is the required_capabilities extension.
type requiredCapabilities struct {
	extensions  []ExtensionType
	proposals   []uint16
	credentials []CredentialType
}

// RequiredCapabilitiesExtension returns the group context extension that
// requires every member to support the extension types, proposal kinds
// and credential types listed, so a client that does not understand
// them cannot be added.
func RequiredCapabilitiesExtension(extensions []ExtensionType, proposals []ProposalKind, credentials []CredentialType) Extension {
	return Extension{Type: ExtensionRequiredCapabilities, Data: encode(func(w *writer) {
		w.nested(func(w *writer) {
			for _, t := range extensions {
				w.u16(uint16(t))
			}
		})
		w.nested(func(w *writer) {
			for _, p := range proposals {
				w.u16(uint16(p))
			}
		})
		w.nested(func(w *writer) {
			for _, c := range credentials {
				w.u16(uint16(c))
			}
		})
	})}
}

func unmarshalRequiredCapabilities(data []byte) (requiredCapabilities, error) {
	var rc requiredCapabilities
	err := decode(data, func(r *reader) {
		r.list(func(r *reader) { rc.extensions = append(rc.extensions, ExtensionType(r.u16())) })
		r.list(func(r *reader) { rc.proposals = append(rc.proposals, r.u16()) })
		r.list(func(r *reader) { rc.credentials = append(rc.credentials, CredentialType(r.u16())) })
	})
	return rc, err
}
