// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package mls

import (
	"bytes"
	"errors"
)

// MessageInfo is what any party reads of a message without a key.
type MessageInfo struct {
	WireFormat WireFormat
	// GroupID and Epoch are set for a public or private message.
	GroupID []byte
	Epoch   uint64
	// Kind is set for a public or private message.
	Kind MessageKind
}

// PeekMessage decodes a message's envelope and returns its wire format
// and, for a group's message, the group, the epoch and the kind of
// content, all of which travel in the clear. It checks nothing a key
// would check.
func PeekMessage(message []byte) (*MessageInfo, error) {
	m, err := parseMessage(message)
	if err != nil {
		return nil, err
	}
	info := &MessageInfo{WireFormat: m.wireFormat}
	switch m.wireFormat {
	case WireFormatPublicMessage:
		info.GroupID, info.Epoch, info.Kind = m.public.content.groupID, m.public.content.epoch, MessageKind(m.public.content.contentType)
	case WireFormatPrivateMessage:
		info.GroupID, info.Epoch, info.Kind = m.private.groupID, m.private.epoch, MessageKind(m.private.contentType)
	}
	return info, nil
}

// PublicGroup follows a group from outside it, as a delivery service
// does. It checks the signature of every public proposal and commit and
// every change a commit makes to the tree, and keeps the group's public
// state: its members, its tree and its group context. It holds no key of
// the group, so it reads no private message and cannot check a commit's
// confirmation tag, which only members can. A PublicGroup is not safe
// for concurrent use.
type PublicGroup struct {
	cfg Config
	publicState
	pending []pendingProposal
}

// NewPublicGroup starts following a group from a group info, checking
// its signature and the group's tree as a joiner does.
func NewPublicGroup(groupInfoMessage []byte, opts JoinOptions, cfg Config) (*PublicGroup, error) {
	m, err := parseMessage(groupInfoMessage)
	if err != nil {
		return nil, err
	}
	if m.wireFormat != WireFormatGroupInfo {
		return nil, errors.New("mls: message is not a group info")
	}
	gi := m.groupInfo
	s, err := suiteFor(gi.context.suite)
	if err != nil {
		return nil, err
	}
	if gi.context.version != protocolVersionMLS10 {
		return nil, errors.New("mls: group info is for another protocol version")
	}
	p := &PublicGroup{cfg: cfg, publicState: publicState{s: s, ctx: gi.context}}
	tree, err := p.joinTree(&cfg, gi, opts.RatchetTree)
	if err != nil {
		return nil, err
	}
	p.tree = tree
	p.confirmationTag = gi.confirmationTag
	p.interim = s.interimTranscriptHash(gi.context.confirmedTranscriptHash, gi.confirmationTag)
	return p, nil
}

// CipherSuite returns the group's suite.
func (p *PublicGroup) CipherSuite() CipherSuite { return p.s.id }

// GroupID returns the group's ID.
func (p *PublicGroup) GroupID() []byte { return bytes.Clone(p.ctx.groupID) }

// Epoch returns the group's current epoch.
func (p *PublicGroup) Epoch() uint64 { return p.ctx.epoch }

// Members returns the group's members in leaf order.
func (p *PublicGroup) Members() []Member { return p.members() }

// Extensions returns the group context's extensions.
func (p *PublicGroup) Extensions() []Extension { return cloneExtensions(p.ctx.extensions) }

// RatchetTree returns the group's ratchet tree.
func (p *PublicGroup) RatchetTree() []byte { return p.tree.bytes() }

// Process checks a public proposal or commit of the group. A proposal
// becomes pending; a commit is staged, and the view changes only when
// the caller merges it.
func (p *PublicGroup) Process(message []byte) (*Message, error) {
	m, err := parseMessage(message)
	if err != nil {
		return nil, err
	}
	if m.wireFormat != WireFormatPublicMessage {
		return nil, errors.New("mls: a public group reads public messages only")
	}
	a, err := p.unprotectPublic(m.public, nil)
	if err != nil {
		return nil, err
	}
	out := &Message{
		Sender:            p.describeSender(a.content.sender, a),
		Epoch:             a.content.epoch,
		AuthenticatedData: a.content.authenticatedData,
	}
	switch a.content.contentType {
	case contentProposal:
		pp, err := p.checkProposalMessage(a)
		if err != nil {
			return nil, err
		}
		p.pending = append(p.pending, pp)
		out.Kind = MessageProposal
		out.Proposal = p.describeProposal(pp)
	case contentCommit:
		sp, err := p.stagePublic(&p.cfg, p.pending, a)
		if err != nil {
			return nil, err
		}
		out.Kind = MessageCommit
		out.Commit = &StagedCommit{
			owner:   p,
			epoch:   p.ctx.epoch,
			changes: sp.changes,
			public: &publicState{
				s:               p.s,
				ctx:             sp.next,
				tree:            sp.app.tree,
				interim:         sp.interim,
				confirmationTag: a.auth.confirmationTag,
			},
		}
	default:
		return nil, errors.New("mls: an application message must be encrypted")
	}
	return out, nil
}

// Merge applies a staged commit this view staged in its current epoch.
func (p *PublicGroup) Merge(c *StagedCommit) error {
	if c == nil || c.owner != p || c.epoch != p.ctx.epoch || c.public == nil {
		return ErrStaleCommit
	}
	p.publicState = *c.public
	p.pending = nil
	return nil
}

// VerifyGroupInfo checks that a group info a member sends describes this
// view's current epoch exactly: the same group context and confirmation
// tag, a signer who is a member, a valid signature, and when it carries
// the ratchet tree, the same tree.
func (p *PublicGroup) VerifyGroupInfo(groupInfoMessage []byte) error {
	m, err := parseMessage(groupInfoMessage)
	if err != nil {
		return err
	}
	if m.wireFormat != WireFormatGroupInfo {
		return errors.New("mls: message is not a group info")
	}
	gi := m.groupInfo
	if !bytes.Equal(gi.context.bytes(), p.ctxBytes()) {
		return errors.New("mls: the group info is for another state of the group")
	}
	if !bytes.Equal(gi.confirmationTag, p.confirmationTag) {
		return errors.New("mls: the group info's confirmation tag is not the epoch's")
	}
	if err := uniqueExtensions(gi.extensions); err != nil {
		return err
	}
	if e, ok := findExtension(gi.extensions, ExtensionRatchetTree); ok && !bytes.Equal(e.Data, p.tree.bytes()) {
		return errors.New("mls: the group info carries another tree")
	}
	signer := p.tree.leaf(gi.signer)
	if signer == nil {
		return errors.New("mls: the group info's signer is not a member")
	}
	return p.s.verifyWithLabel(signer.signatureKey, "GroupInfoTBS", encode(gi.marshalTBS), gi.signature)
}
