// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package mls

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"time"
)

// MaxContentBytes is the largest application message, authenticated
// data or group ID the package encrypts or sends.
const MaxContentBytes = 1 << 24

// ResumptionPSKEpochs is how many epochs' resumption PSKs a group keeps,
// the current one included, for commits that inject one.
const ResumptionPSKEpochs = 16

// Config sets the checks and choices a group makes beyond RFC 9420. It
// is not stored with the group's state: pass it again to UnmarshalGroup.
type Config struct {
	// CheckCredential decides whether a credential is valid for the
	// signature key it comes with. It is called for every leaf a group
	// takes in: joining, an add, an update and a committer's new leaf.
	// Nil accepts every credential, which is right only when the
	// application checks the members of each staged commit itself.
	CheckCredential func(cred Credential, signatureKey []byte) error
	// AcceptProposal decides whether a proposal that arrives in a message
	// of its own may become pending, which lets a commit with
	// IncludePending carry it. It runs after the proposal is checked on
	// its own, with the group as it is at the proposal's epoch. Nil
	// accepts a member's proposals and an external sender's removals, and
	// refuses the rest, a client proposing its own add and an external
	// sender's other proposals, which RFC 9420 leaves to the application
	// (Section 12.1.8).
	AcceptProposal func(GroupState, Proposal) error
	// CheckSuccessor decides whether a leaf's new credential, in an
	// update or a committer's path, may replace the one it had, RFC
	// 9420, Section 5.3.1. Nil accepts only the same credential.
	CheckSuccessor func(old, new Credential) error
	// PSKs supplies the external PSKs commits and Welcomes may name.
	PSKs PSKStore
	// Now is the clock for key package lifetimes; nil uses time.Now.
	Now func() time.Time
	// MaxLifetime rejects a key package whose lifetime spans more than
	// this. Zero sets no bound.
	MaxLifetime time.Duration
	// PublicHandshake sends proposals and commits as public messages,
	// signed but not encrypted, so a delivery service can follow the
	// group's membership. Application messages are always encrypted.
	PublicHandshake bool
	// PaddingBlock pads a private message's content to a multiple of
	// this many bytes; zero uses DefaultPaddingBlock.
	PaddingBlock int
	// OmitRatchetTree leaves the ratchet tree out of the group info a
	// Welcome carries, for a delivery service that hands it out.
	OmitRatchetTree bool
}

// GroupState is a group's public state at one epoch, as a hook of
// Config sees it.
type GroupState struct {
	groupID    []byte
	epoch      uint64
	extensions []Extension
	members    []Member
}

// GroupID returns the group's id.
func (s GroupState) GroupID() []byte { return bytes.Clone(s.groupID) }

// Epoch returns the epoch.
func (s GroupState) Epoch() uint64 { return s.epoch }

// Extensions returns the group context's extensions.
func (s GroupState) Extensions() []Extension { return cloneExtensions(s.extensions) }

// Members returns the members in leaf order.
func (s GroupState) Members() []Member { return slices.Clone(s.members) }

func (p *publicState) state() GroupState {
	return GroupState{groupID: p.ctx.groupID, epoch: p.ctx.epoch, extensions: p.ctx.extensions, members: p.members()}
}

func (c *Config) acceptProposal(g GroupState, p Proposal) error {
	if c.AcceptProposal != nil {
		return c.AcceptProposal(g, p)
	}
	switch {
	case p.Sender.Kind == SenderMember:
		return nil
	case p.Sender.Kind == SenderExternal && p.Kind == ProposalRemove:
		return nil
	}
	return errors.New("mls: the group takes no such proposal from outside it")
}

func (c *Config) successor(old, new Credential) error {
	if c.CheckSuccessor != nil {
		return c.CheckSuccessor(old.clone(), new.clone())
	}
	if !old.equal(new) {
		return errors.New("mls: a leaf's new credential is not its old one")
	}
	return nil
}

func (c *Config) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Errors a caller tells apart.
var (
	// ErrWrongGroup is returned for a message of another group.
	ErrWrongGroup = errors.New("mls: message is for another group")
	// ErrWrongEpoch is returned for a message of another epoch than the
	// group's current one.
	ErrWrongEpoch = errors.New("mls: message is for another epoch")
	// ErrOwnMessage is returned for a message this member sent. A
	// member merges its own commit with Merge rather than processing it.
	ErrOwnMessage = errors.New("mls: message is from this member")
	// ErrInactive is returned by a group this member was removed from,
	// or one a committed reinitialization closed.
	ErrInactive = errors.New("mls: this member is no longer in the group")
	// ErrPendingProposals is returned by Encrypt while proposals of the
	// epoch wait for a commit, which RFC 9420 requires first.
	ErrPendingProposals = errors.New("mls: proposals wait for a commit")
	// ErrStaleCommit is returned by Merge for a staged commit of an epoch
	// the group has left.
	ErrStaleCommit = errors.New("mls: staged commit is for another epoch")
)

type groupStatus uint8

const (
	statusActive  groupStatus = 0
	statusRemoved groupStatus = 1
	statusReInit  groupStatus = 2
)

// publicState is what any party following a group knows: the context,
// the tree and the transcript.
type publicState struct {
	s               *suite
	ctx             groupContext
	tree            *ratchetTree
	interim         []byte
	confirmationTag []byte
}

func (p *publicState) ctxBytes() []byte { return p.ctx.bytes() }

// pendingProposal is a proposal received or sent in the current epoch,
// which a commit may name by reference.
type pendingProposal struct {
	ref      []byte
	proposal *proposal
	sender   sender
}

type resumptionPSK struct {
	epoch uint64
	psk   []byte
}

// Group is one member's state of an MLS group. A Group is not safe for
// concurrent use.
type Group struct {
	cfg Config
	publicState
	priv       *privateTree
	signer     *SignatureKey
	es         epochSecrets
	st         *secretTree
	pending    []pendingProposal
	resumption []resumptionPSK
	status     groupStatus
	// welcomeExtensions are those of the Welcome this member joined
	// with, kept until the group merges a commit or is saved.
	welcomeExtensions []Extension
	// joinedFrom is the member that signed the group info this member
	// joined with, kept until the group merges a commit or is saved.
	joinedFrom *Member
}

// CreateOptions sets what CreateGroup puts in a new group.
type CreateOptions struct {
	// Extensions are the group context's extensions.
	Extensions []Extension
	// SupportedExtensions are the extension types beyond RFC 9420's the
	// creator supports, listed in its leaf's capabilities.
	SupportedExtensions []ExtensionType
	// LeafExtensions are carried in the creator's leaf.
	LeafExtensions []Extension
	// Lifetime of the creator's leaf; the zero value means from an hour
	// ago to DefaultKeyPackageLifetime from now.
	Lifetime Lifetime
}

// CreateGroup starts a group with one member, its creator, in epoch 0.
// The group ID must not be shared with another group; a random value of
// 32 bytes serves.
func CreateGroup(groupID []byte, signer *SignatureKey, cred Credential, opts CreateOptions, cfg Config) (*Group, error) {
	s, err := suiteFor(signer.CipherSuite())
	if err != nil {
		return nil, err
	}
	if len(groupID) == 0 || len(groupID) > MaxContentBytes {
		return nil, errors.New("mls: group ID must be 1 to MaxContentBytes bytes")
	}
	if err := uniqueExtensions(opts.Extensions); err != nil {
		return nil, err
	}
	kp, secrets, err := NewKeyPackage(signer, cred, KeyPackageOptions{
		Lifetime:       opts.Lifetime,
		Extensions:     append(append([]ExtensionType{}, opts.SupportedExtensions...), extensionTypes(opts.Extensions)...),
		LeafExtensions: opts.LeafExtensions,
		Now:            cfg.Now,
	})
	if err != nil {
		return nil, err
	}
	leaf := kp.kp.leaf
	g := &Group{
		cfg: cfg,
		publicState: publicState{
			s:    s,
			tree: newTree(&leaf),
			ctx: groupContext{
				version:    protocolVersionMLS10,
				suite:      s.id,
				groupID:    bytes.Clone(groupID),
				extensions: cloneExtensions(opts.Extensions),
			},
		},
		priv: &privateTree{leaf: 0, keys: map[uint32]hpkeKey{0: {priv: secrets.encryptionPriv, pub: leaf.encryptionKey}}},
	}
	if err := g.checkGroupExtensions(g.tree, g.ctx.extensions); err != nil {
		return nil, err
	}
	g.signer = signer
	g.ctx.treeHash = g.tree.treeHash(s)
	epoch := make([]byte, s.nh)
	if _, err := rand.Read(epoch); err != nil {
		return nil, err
	}
	g.es = s.epochSecrets(epoch)
	g.confirmationTag = s.confirmationTag(g.es.confirmation, nil)
	g.interim = s.interimTranscriptHash(nil, g.confirmationTag)
	g.st = newSecretTree(s, g.es.encryption, g.tree.numLeaves())
	g.es.encryption = nil // consumed by the secret tree, RFC 9420, Section 9.2
	g.resumption = []resumptionPSK{{epoch: 0, psk: g.es.resumption}}
	return g, nil
}

func extensionTypes(exts []Extension) []ExtensionType {
	var out []ExtensionType
	for _, e := range exts {
		out = append(out, e.Type)
	}
	return out
}

// CipherSuite returns the group's suite.
func (g *Group) CipherSuite() CipherSuite { return g.s.id }

// GroupID returns the group's ID.
func (g *Group) GroupID() []byte { return bytes.Clone(g.ctx.groupID) }

// Epoch returns the group's current epoch.
func (g *Group) Epoch() uint64 { return g.ctx.epoch }

// Leaf returns this member's leaf index.
func (g *Group) Leaf() uint32 { return g.priv.leaf }

// Active reports whether this member is still in the group and may send
// in it.
func (g *Group) Active() bool { return g.status == statusActive }

// Extensions returns the group context's extensions.
func (g *Group) Extensions() []Extension { return cloneExtensions(g.ctx.extensions) }

// EpochAuthenticator returns a value every member of the epoch derives
// alike, which members compare out of band to detect an impersonator.
func (g *Group) EpochAuthenticator() []byte { return bytes.Clone(g.es.authenticator) }

// Export derives a secret of length bytes for the label and context from
// the epoch, MLS-Exporter of RFC 9420, Section 8.5. Every member of the
// epoch derives the same secret, and nobody outside it can.
func (g *Group) Export(label string, context []byte, length int) ([]byte, error) {
	if g.status != statusActive {
		return nil, ErrInactive
	}
	return g.s.exporter(g.es.exporter, label, context, length)
}

// Member is one leaf of a group.
type Member struct {
	Leaf         uint32
	Credential   Credential
	SignatureKey []byte
	Capabilities Capabilities
	Extensions   []Extension
}

func memberOf(i uint32, l *leafNode) Member {
	return Member{
		Leaf:         i,
		Credential:   l.credential.clone(),
		SignatureKey: bytes.Clone(l.signatureKey),
		Capabilities: l.capabilities.clone(),
		Extensions:   cloneExtensions(l.extensions),
	}
}

// Members returns the group's members in leaf order.
func (g *Group) Members() []Member { return g.publicState.members() }

func (p *publicState) members() []Member {
	var out []Member
	for _, i := range p.tree.members() {
		out = append(out, memberOf(i, p.tree.leaf(i)))
	}
	return out
}

// Member returns the member at a leaf.
func (g *Group) Member(leaf uint32) (Member, bool) {
	l := g.tree.leaf(leaf)
	if l == nil {
		return Member{}, false
	}
	return memberOf(leaf, l), true
}

// RatchetTree returns the group's ratchet tree as the ratchet_tree
// extension encodes it, for a joiner whose Welcome or group info leaves
// it out.
func (g *Group) RatchetTree() []byte { return g.tree.bytes() }

// epochSecrets derives an epoch's secrets from its epoch secret.
func (s *suite) epochSecrets(epoch []byte) epochSecrets {
	return epochSecrets{
		senderData:    s.deriveSecret(epoch, "sender data"),
		encryption:    s.deriveSecret(epoch, "encryption"),
		exporter:      s.deriveSecret(epoch, "exporter"),
		external:      s.deriveSecret(epoch, "external"),
		confirmation:  s.deriveSecret(epoch, "confirm"),
		membership:    s.deriveSecret(epoch, "membership"),
		resumption:    s.deriveSecret(epoch, "resumption"),
		authenticator: s.deriveSecret(epoch, "authentication"),
		init:          s.deriveSecret(epoch, "init"),
	}
}

// SenderKind is who sent a message.
type SenderKind uint8

// The kinds of sender.
const (
	// SenderMember is a member of the group, named by its leaf.
	SenderMember SenderKind = 1
	// SenderExternal is one of the group's external senders, named by
	// its position in the external_senders extension.
	SenderExternal SenderKind = 2
	// SenderNewMember is a client proposing its own add, or committing
	// its own external join.
	SenderNewMember SenderKind = 3
)

// Sender is who sent a message.
type Sender struct {
	Kind SenderKind
	// Leaf is a member's leaf, or the leaf an external join takes.
	Leaf uint32
	// ExternalIndex is an external sender's position.
	ExternalIndex uint32
	Credential    Credential
	SignatureKey  []byte
}

// MessageKind is what a processed message carried.
type MessageKind uint8

// The kinds of message.
const (
	MessageApplication MessageKind = 1
	MessageProposal    MessageKind = 2
	MessageCommit      MessageKind = 3
)

// Message is what Process found in a message.
type Message struct {
	Kind              MessageKind
	Sender            Sender
	Epoch             uint64
	AuthenticatedData []byte
	// ApplicationData is an application message's plaintext.
	ApplicationData []byte
	// Signed is an application message's content with its sender's
	// signature, which VerifySignedContent checks again later, such as
	// after the message is shared with a member who joined since.
	Signed []byte
	// Proposal is a proposal message's proposal, now pending.
	Proposal *Proposal
	// Commit is a commit, staged: nothing changes until Merge.
	Commit *StagedCommit
}

// ProposalKind is the kind of a proposal.
type ProposalKind uint16

// The proposal kinds of RFC 9420.
const (
	ProposalAdd                    ProposalKind = 1
	ProposalUpdate                 ProposalKind = 2
	ProposalRemove                 ProposalKind = 3
	ProposalPSK                    ProposalKind = 4
	ProposalReInit                 ProposalKind = 5
	ProposalExternalInit           ProposalKind = 6
	ProposalGroupContextExtensions ProposalKind = 7
)

// String names the kind as RFC 9420 does.
func (k ProposalKind) String() string {
	switch k {
	case ProposalAdd:
		return "add"
	case ProposalUpdate:
		return "update"
	case ProposalRemove:
		return "remove"
	case ProposalPSK:
		return "psk"
	case ProposalReInit:
		return "reinit"
	case ProposalExternalInit:
		return "external_init"
	case ProposalGroupContextExtensions:
		return "group_context_extensions"
	}
	return fmt.Sprintf("proposal(%d)", uint16(k))
}

// Proposal describes a pending proposal.
type Proposal struct {
	Kind ProposalKind
	// Ref is the reference a commit names the proposal by.
	Ref    []byte
	Sender Sender
	// Leaf is the leaf a remove or an update is for.
	Leaf uint32
	// Member is the client an add adds, or an update's new leaf. A
	// pending add's Member.Leaf is 0, since its leaf is known only when a
	// commit applies it; in Changes.Proposals it is the leaf it took.
	Member *Member
	// Extensions is a group context extensions proposal's new list.
	Extensions []Extension
}

func (p *publicState) describeSender(sd sender, a *authenticatedContent) Sender {
	out := Sender{Kind: SenderKind(0)}
	switch sd.typ {
	case senderMember:
		out.Kind, out.Leaf = SenderMember, sd.index
		if l := p.tree.leaf(sd.index); l != nil {
			out.Credential, out.SignatureKey = l.credential.clone(), bytes.Clone(l.signatureKey)
		}
	case senderExternal:
		out.Kind, out.ExternalIndex = SenderExternal, sd.index
		if es, err := p.externalSenders(); err == nil && int(sd.index) < len(es) {
			out.Credential, out.SignatureKey = es[sd.index].credential.clone(), bytes.Clone(es[sd.index].signatureKey)
		}
	case senderNewMemberProposal:
		out.Kind = SenderNewMember
		if a != nil && a.content.proposal != nil && a.content.proposal.add != nil {
			l := &a.content.proposal.add.leaf
			out.Credential, out.SignatureKey = l.credential.clone(), bytes.Clone(l.signatureKey)
		}
	case senderNewMemberCommit:
		out.Kind = SenderNewMember
		if a != nil && a.content.commit != nil && a.content.commit.path != nil {
			l := &a.content.commit.path.leaf
			out.Credential, out.SignatureKey = l.credential.clone(), bytes.Clone(l.signatureKey)
		}
	}
	return out
}

func (p *publicState) describeProposal(pp pendingProposal) *Proposal {
	pr := pp.proposal
	out := &Proposal{Kind: ProposalKind(pr.typ), Ref: bytes.Clone(pp.ref), Sender: p.describeSender(pp.sender, nil)}
	switch pr.typ {
	case proposalAdd:
		m := memberOf(0, &pr.add.leaf)
		out.Member = &m
	case proposalUpdate:
		out.Leaf = pp.sender.index
		m := memberOf(pp.sender.index, pr.update)
		out.Member = &m
	case proposalRemove:
		out.Leaf = pr.remove
	case proposalGroupContextExtensions:
		out.Extensions = cloneExtensions(pr.extensions)
	}
	return out
}

// PendingProposals returns the proposals of the current epoch that wait
// for a commit.
func (g *Group) PendingProposals() []Proposal {
	var out []Proposal
	for _, pp := range g.pending {
		out = append(out, *g.describeProposal(pp))
	}
	return out
}

// DiscardProposal drops a pending proposal, by its reference, that the
// application will not commit.
func (g *Group) DiscardProposal(ref []byte) {
	for i, pp := range g.pending {
		if bytes.Equal(pp.ref, ref) {
			g.pending = append(g.pending[:i:i], g.pending[i+1:]...)
			return
		}
	}
}

func (p *publicState) externalSenders() ([]externalSender, error) {
	e, ok := findExtension(p.ctx.extensions, ExtensionExternalSenders)
	if !ok {
		return nil, nil
	}
	return unmarshalExternalSenders(e.Data)
}

// Encrypt protects an application message for the group's current
// epoch. authenticatedData travels in the clear beside the ciphertext,
// bound to it.
func (g *Group) Encrypt(plaintext, authenticatedData []byte) ([]byte, error) {
	if g.status != statusActive {
		return nil, ErrInactive
	}
	if len(g.pending) > 0 {
		return nil, ErrPendingProposals
	}
	if len(plaintext) > MaxContentBytes || len(authenticatedData) > MaxContentBytes {
		return nil, errors.New("mls: message larger than MaxContentBytes")
	}
	a := &authenticatedContent{
		wireFormat: WireFormatPrivateMessage,
		content: framedContent{
			groupID:           g.ctx.groupID,
			epoch:             g.ctx.epoch,
			sender:            sender{typ: senderMember, index: g.priv.leaf},
			authenticatedData: bytes.Clone(authenticatedData),
			contentType:       contentApplication,
			applicationData:   bytes.Clone(plaintext),
		},
	}
	return g.protect(a)
}

// protect signs a member's content and protects it in the wire format
// it names, returning the MLSMessage.
func (g *Group) protect(a *authenticatedContent) ([]byte, error) {
	ctx := g.ctxBytes()
	if err := g.s.signContent(g.signer, a, ctx); err != nil {
		return nil, err
	}
	return g.frame(a, ctx)
}

// frame protects already signed content.
func (g *Group) frame(a *authenticatedContent, ctx []byte) ([]byte, error) {
	if a.wireFormat == WireFormatPublicMessage {
		pm := &publicMessage{content: a.content, auth: a.auth}
		pm.membershipTag = g.s.membershipTag(g.es.membership, a, ctx)
		return encode((&mlsMessage{wireFormat: WireFormatPublicMessage, public: pm}).marshal), nil
	}
	pm, err := g.s.encryptContent(g.st, g.es.senderData, a, g.cfg.PaddingBlock)
	if err != nil {
		return nil, err
	}
	return encode((&mlsMessage{wireFormat: WireFormatPrivateMessage, private: pm}).marshal), nil
}

func (g *Group) handshakeWireFormat() WireFormat {
	if g.cfg.PublicHandshake {
		return WireFormatPublicMessage
	}
	return WireFormatPrivateMessage
}

// ProposeRemove proposes removing the member at a leaf, such as this
// member's own to leave the group, which another member then commits.
func (g *Group) ProposeRemove(leaf uint32, authenticatedData []byte) ([]byte, error) {
	if g.status != statusActive {
		return nil, ErrInactive
	}
	if g.tree.leaf(leaf) == nil {
		return nil, fmt.Errorf("mls: no member at leaf %d", leaf)
	}
	a := &authenticatedContent{
		wireFormat: g.handshakeWireFormat(),
		content: framedContent{
			groupID:           g.ctx.groupID,
			epoch:             g.ctx.epoch,
			sender:            sender{typ: senderMember, index: g.priv.leaf},
			authenticatedData: bytes.Clone(authenticatedData),
			contentType:       contentProposal,
			proposal:          &proposal{typ: proposalRemove, remove: leaf},
		},
	}
	msg, err := g.protect(a)
	if err != nil {
		return nil, err
	}
	g.pending = append(g.pending, pendingProposal{ref: g.s.proposalRef(a), proposal: a.content.proposal, sender: a.content.sender})
	return msg, nil
}

// Process authenticates a message of the group and returns what it
// carries. A proposal becomes pending; a commit is staged, and the group
// changes only when the caller merges it.
func (g *Group) Process(message []byte) (*Message, error) {
	if g.status != statusActive {
		return nil, ErrInactive
	}
	m, err := parseMessage(message)
	if err != nil {
		return nil, err
	}
	var a *authenticatedContent
	switch m.wireFormat {
	case WireFormatPublicMessage:
		a, err = g.unprotectPublic(m.public, g.es.membership)
		if err == nil && a.content.sender.typ == senderMember && a.content.sender.index == g.priv.leaf {
			return nil, ErrOwnMessage
		}
	case WireFormatPrivateMessage:
		// A group whose handshakes are public takes none encrypted, which
		// would change it past what a delivery service can follow.
		if g.cfg.PublicHandshake && m.private.contentType != contentApplication {
			return nil, errors.New("mls: this group's proposals and commits are public messages")
		}
		a, err = g.unprotectPrivate(m.private)
	default:
		return nil, fmt.Errorf("mls: a group processes public and private messages, not wire format %d", m.wireFormat)
	}
	if err != nil {
		return nil, err
	}
	out := &Message{
		Sender:            g.describeSender(a.content.sender, a),
		Epoch:             a.content.epoch,
		AuthenticatedData: a.content.authenticatedData,
	}
	switch a.content.contentType {
	case contentApplication:
		out.Kind = MessageApplication
		out.ApplicationData = a.content.applicationData
		out.Signed = signedRecord(g.s.id, a, g.ctxBytes())
	case contentProposal:
		pp, err := g.checkProposalMessage(&g.cfg, g.pending, a)
		if err != nil {
			return nil, err
		}
		g.pending = append(g.pending, pp)
		out.Kind = MessageProposal
		out.Proposal = g.describeProposal(pp)
	case contentCommit:
		sc, err := g.stageCommit(a)
		if err != nil {
			return nil, err
		}
		out.Kind = MessageCommit
		out.Commit = sc
	}
	return out, nil
}

// unprotectPublic checks a public message's group, epoch, membership tag
// and signature. The membership key is nil for a party outside the
// group, which then checks no tag.
func (p *publicState) unprotectPublic(m *publicMessage, membershipKey []byte) (*authenticatedContent, error) {
	c := &m.content
	if !bytes.Equal(c.groupID, p.ctx.groupID) {
		return nil, ErrWrongGroup
	}
	if c.epoch != p.ctx.epoch {
		return nil, ErrWrongEpoch
	}
	a := &authenticatedContent{wireFormat: WireFormatPublicMessage, content: m.content, auth: m.auth}
	ctx := p.ctxBytes()
	var key []byte
	switch c.sender.typ {
	case senderMember:
		if c.contentType == contentApplication {
			return nil, errors.New("mls: an application message must be encrypted")
		}
		l := p.tree.leaf(c.sender.index)
		if l == nil {
			return nil, fmt.Errorf("mls: no member at sender leaf %d", c.sender.index)
		}
		if membershipKey != nil && !equal(p.s.membershipTag(membershipKey, a, ctx), m.membershipTag) {
			return nil, errors.New("mls: membership tag does not verify")
		}
		key = l.signatureKey
	case senderExternal:
		if c.contentType != contentProposal || !c.proposal.typ.externalAllowed() {
			return nil, errors.New("mls: an external sender may send only some proposals")
		}
		es, err := p.externalSenders()
		if err != nil {
			return nil, err
		}
		if int(c.sender.index) >= len(es) {
			return nil, fmt.Errorf("mls: no external sender %d", c.sender.index)
		}
		key = es[c.sender.index].signatureKey
	case senderNewMemberProposal:
		if c.contentType != contentProposal || c.proposal.typ != proposalAdd {
			return nil, errors.New("mls: a new member may propose only its own add")
		}
		key = c.proposal.add.leaf.signatureKey
	case senderNewMemberCommit:
		if c.contentType != contentCommit || c.commit.path == nil {
			return nil, errors.New("mls: a new member's commit must carry a path")
		}
		key = c.commit.path.leaf.signatureKey
	}
	if err := p.s.verifyContent(key, a, ctx); err != nil {
		return nil, err
	}
	return a, nil
}

func (g *Group) unprotectPrivate(m *privateMessage) (*authenticatedContent, error) {
	if !bytes.Equal(m.groupID, g.ctx.groupID) {
		return nil, ErrWrongGroup
	}
	if m.epoch != g.ctx.epoch {
		return nil, ErrWrongEpoch
	}
	check := func(leaf uint32) error {
		if leaf == g.priv.leaf {
			return ErrOwnMessage
		}
		if g.tree.leaf(leaf) == nil {
			return fmt.Errorf("mls: no member at sender leaf %d", leaf)
		}
		return nil
	}
	a, advance, err := g.s.decryptContent(g.st, g.es.senderData, m, check)
	if err != nil {
		return nil, err
	}
	if err := g.s.verifyContent(g.tree.leaf(a.content.sender.index).signatureKey, a, g.ctxBytes()); err != nil {
		return nil, err
	}
	advance()
	return a, nil
}

// checkProposalMessage checks a proposal on its own against the current
// epoch, as RFC 9420, Section 12.1 has each kind checked, asks the
// configuration whether to accept it, and returns it as pending.
func (p *publicState) checkProposalMessage(cfg *Config, pending []pendingProposal, a *authenticatedContent) (pendingProposal, error) {
	pr := a.content.proposal
	sd := a.content.sender
	ref := p.s.proposalRef(a)
	if slices.ContainsFunc(pending, func(pp pendingProposal) bool { return bytes.Equal(pp.ref, ref) }) {
		return pendingProposal{}, errors.New("mls: the proposal is already pending")
	}
	switch pr.typ {
	case proposalExternalInit:
		return pendingProposal{}, errors.New("mls: an external init is only valid inline in an external commit")
	case proposalUpdate:
		if sd.typ != senderMember {
			return pendingProposal{}, errors.New("mls: only a member proposes an update")
		}
		old := p.tree.leaf(sd.index)
		if pr.update.source != leafSourceUpdate || bytes.Equal(old.encryptionKey, pr.update.encryptionKey) {
			return pendingProposal{}, errors.New("mls: an update has source update and a new encryption key")
		}
		if err := cfg.successor(old.credential, pr.update.credential); err != nil {
			return pendingProposal{}, err
		}
		if err := p.checkLeafInGroup(cfg, pr.update, sd.index, p.ctx.extensions); err != nil {
			return pendingProposal{}, err
		}
	case proposalRemove:
		if p.tree.leaf(pr.remove) == nil {
			return pendingProposal{}, fmt.Errorf("mls: remove of leaf %d, which holds no member", pr.remove)
		}
	case proposalAdd:
		if err := p.checkKeyPackage(cfg, pr.add, p.ctx.extensions); err != nil {
			return pendingProposal{}, err
		}
		for _, m := range p.tree.members() {
			if bytes.Equal(p.tree.leaf(m).signatureKey, pr.add.leaf.signatureKey) {
				return pendingProposal{}, errors.New("mls: an add of a client already in the group")
			}
		}
	case proposalPSK:
		if len(pr.psk.nonce) != p.s.nh || pr.psk.typ == pskResumption && pr.psk.usage != resumptionApplication {
			return pendingProposal{}, errors.New("mls: a PSK proposal of the wrong nonce or usage")
		}
	case proposalGroupContextExtensions:
		if err := p.checkGroupExtensions(p.tree, pr.extensions); err != nil {
			return pendingProposal{}, err
		}
	case proposalReInit:
		if pr.reinit.version < protocolVersionMLS10 {
			return pendingProposal{}, errors.New("mls: reinitialization to an older version")
		}
	}
	pp := pendingProposal{ref: ref, proposal: pr, sender: sd}
	if err := cfg.acceptProposal(p.state(), *p.describeProposal(pp)); err != nil {
		return pendingProposal{}, err
	}
	return pp, nil
}

// signedRecord encodes an application message's content with its
// signature and the group context the signature covers.
func signedRecord(cs CipherSuite, a *authenticatedContent, ctx []byte) []byte {
	return encode(func(w *writer) {
		w.u16(uint16(cs))
		w.u16(uint16(a.wireFormat))
		a.content.marshal(w)
		w.vec(ctx)
		w.vec(a.auth.signature)
	})
}

// SignedContent is an application message whose signature
// VerifySignedContent checked.
type SignedContent struct {
	CipherSuite       CipherSuite
	GroupID           []byte
	Epoch             uint64
	Leaf              uint32
	AuthenticatedData []byte
	ApplicationData   []byte
}

// VerifySignedContent checks the signature of a record Message.Signed
// returned against the sender's signature public key and returns what
// the message said. The caller decides whether the key was the sender's
// in that epoch.
func VerifySignedContent(record, signatureKey []byte) (*SignedContent, error) {
	var cs CipherSuite
	var a authenticatedContent
	var ctx []byte
	err := decode(bytes.Clone(record), func(r *reader) {
		cs = CipherSuite(r.u16())
		a.wireFormat = WireFormat(r.u16())
		a.content.unmarshal(r)
		ctx = r.vec()
		a.auth.signature = r.vec()
	})
	if err != nil {
		return nil, err
	}
	s, err := suiteFor(cs)
	if err != nil {
		return nil, err
	}
	if a.content.contentType != contentApplication || a.content.sender.typ != senderMember {
		return nil, errors.New("mls: record is not a member's application message")
	}
	var gc groupContext
	if err := decode(ctx, gc.unmarshal); err != nil {
		return nil, err
	}
	if gc.suite != cs || !bytes.Equal(gc.groupID, a.content.groupID) || gc.epoch != a.content.epoch {
		return nil, errors.New("mls: record's group context does not match its content")
	}
	if err := s.verifyContent(signatureKey, &a, ctx); err != nil {
		return nil, err
	}
	return &SignedContent{
		CipherSuite:       cs,
		GroupID:           a.content.groupID,
		Epoch:             a.content.epoch,
		Leaf:              a.content.sender.index,
		AuthenticatedData: a.content.authenticatedData,
		ApplicationData:   a.content.applicationData,
	}, nil
}
