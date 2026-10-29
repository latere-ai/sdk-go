// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package mls

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"time"
)

// errCommit is wrapped by every error a commit that breaks RFC 9420's
// rules produces.
var errCommit = errors.New("mls: invalid commit")

func commitErr(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errCommit, fmt.Sprintf(format, args...))
}

// resolvedProposal is a proposal a commit covers, with who proposed it.
type resolvedProposal struct {
	p      *proposal
	sender sender
	ref    []byte // nil for a proposal inline in the commit
}

// resolveProposals turns a commit's list into proposals and their
// senders: an inline proposal is the committer's, and a reference must
// name a pending proposal.
func resolveProposals(pending []pendingProposal, committer sender, c *commit) ([]resolvedProposal, error) {
	var out []resolvedProposal
	for _, pr := range c.proposals {
		if pr.proposal != nil {
			out = append(out, resolvedProposal{p: pr.proposal, sender: committer})
			continue
		}
		if committer.typ == senderNewMemberCommit {
			return nil, commitErr("an external commit names a proposal by reference")
		}
		i := slices.IndexFunc(pending, func(pp pendingProposal) bool { return bytes.Equal(pp.ref, pr.reference) })
		if i < 0 {
			return nil, commitErr("the commit names a proposal this member does not have")
		}
		if slices.ContainsFunc(out, func(rp resolvedProposal) bool { return bytes.Equal(rp.ref, pr.reference) }) {
			return nil, commitErr("the commit names a proposal twice")
		}
		out = append(out, resolvedProposal{p: pending[i].proposal, sender: pending[i].sender, ref: pending[i].ref})
	}
	return out, nil
}

// applied is a group's public state after a commit's proposals, before
// its path.
type applied struct {
	tree              *ratchetTree
	extensions        []Extension
	extensionsChanged bool
	props             []resolvedProposal
	replaced          []Member // the leaves updates replace, as they were
	adds              []uint32 // the leaves of added members, in the commit's order
	addKeyPackages    []*keyPackage
	removed           []Member
	updated           []uint32
	psks              []preSharedKeyID
	externalInit      []byte
	reinit            *reInit
	pathRequired      bool
}

// applyProposals checks a commit's proposal list, RFC 9420, Section
// 12.2, and applies it in the order of Section 12.3 to a copy of the
// tree.
func (p *publicState) applyProposals(cfg *Config, props []resolvedProposal, committer sender) (*applied, error) {
	external := committer.typ == senderNewMemberCommit
	app := &applied{extensions: p.ctx.extensions, props: props}
	if len(props) == 0 {
		app.pathRequired = true
	}
	var gce *proposal
	targets := make(map[uint32]bool)
	var pskIDs [][]byte
	var addKeys [][]byte
	var externalInits, removes, reinits int
	for _, rp := range props {
		pr := rp.p
		if pr.typ.pathRequired() {
			app.pathRequired = true
		}
		if external {
			switch pr.typ {
			case proposalExternalInit, proposalPSK:
			case proposalRemove:
				removes++
			default:
				return nil, commitErr("an external commit carries a proposal of type %d", pr.typ)
			}
		} else if pr.typ == proposalExternalInit {
			return nil, commitErr("a member's commit carries an external init")
		}
		switch rp.sender.typ {
		case senderExternal:
			if !pr.typ.externalAllowed() {
				return nil, commitErr("an external sender proposed type %d", pr.typ)
			}
		case senderNewMemberProposal:
			if pr.typ != proposalAdd {
				return nil, commitErr("a new member proposed type %d", pr.typ)
			}
		}
		switch pr.typ {
		case proposalUpdate:
			if rp.sender.typ != senderMember {
				return nil, commitErr("an update from a sender that is not a member")
			}
			if committer.typ == senderMember && rp.sender.index == committer.index {
				return nil, commitErr("the commit covers the committer's own update")
			}
			if targets[rp.sender.index] {
				return nil, commitErr("two updates or removes for leaf %d", rp.sender.index)
			}
			targets[rp.sender.index] = true
		case proposalRemove:
			if p.tree.leaf(pr.remove) == nil {
				return nil, commitErr("remove of leaf %d, which holds no member", pr.remove)
			}
			if committer.typ == senderMember && pr.remove == committer.index {
				return nil, commitErr("the commit removes the committer")
			}
			if targets[pr.remove] {
				return nil, commitErr("two updates or removes for leaf %d", pr.remove)
			}
			targets[pr.remove] = true
		case proposalAdd:
			if slices.ContainsFunc(addKeys, func(k []byte) bool { return bytes.Equal(k, pr.add.leaf.signatureKey) }) {
				return nil, commitErr("two adds of the same client")
			}
			addKeys = append(addKeys, pr.add.leaf.signatureKey)
		case proposalPSK:
			if len(pr.psk.nonce) != p.s.nh {
				return nil, commitErr("PSK nonce of %d bytes", len(pr.psk.nonce))
			}
			if pr.psk.typ == pskResumption && pr.psk.usage != resumptionApplication {
				return nil, commitErr("a resumption PSK for reinitialization or branching in a commit")
			}
			id := encode(pr.psk.marshal)
			if slices.ContainsFunc(pskIDs, func(o []byte) bool { return bytes.Equal(o, id) }) {
				return nil, commitErr("the same PSK twice")
			}
			pskIDs = append(pskIDs, id)
		case proposalGroupContextExtensions:
			if gce != nil {
				return nil, commitErr("two group context extensions proposals")
			}
			gce = pr
		case proposalReInit:
			reinits++
			if pr.reinit.version < protocolVersionMLS10 {
				return nil, commitErr("reinitialization to an older version")
			}
		case proposalExternalInit:
			externalInits++
		}
	}
	if reinits > 0 && len(props) > 1 {
		return nil, commitErr("a reinitialization with other proposals")
	}
	if external && externalInits != 1 {
		return nil, commitErr("an external commit needs exactly one external init")
	}
	// RFC 9420 lets an external commit remove the joiner's earlier leaf,
	// a resync. It lets a stolen signature key join and remove the
	// device it was stolen from in one step, so this package refuses it:
	// a returning device joins anew and a member removes the old leaf.
	if external && removes > 0 {
		return nil, commitErr("an external commit removes a member")
	}

	app.tree = p.tree.clone()
	if gce != nil {
		if err := uniqueExtensions(gce.extensions); err != nil {
			return nil, err
		}
		app.extensions = gce.extensions
		app.extensionsChanged = true
	}
	for _, rp := range props {
		if rp.p.typ != proposalUpdate {
			continue
		}
		i := rp.sender.index
		l := rp.p.update
		if l.source != leafSourceUpdate {
			return nil, commitErr("an update's leaf has source %d", l.source)
		}
		old := app.tree.leaf(i)
		if old == nil || bytes.Equal(old.encryptionKey, l.encryptionKey) {
			return nil, commitErr("an update for leaf %d keeps its encryption key", i)
		}
		if err := cfg.successor(old.credential, l.credential); err != nil {
			return nil, err
		}
		if err := p.checkLeafInGroup(cfg, l, i, app.extensions); err != nil {
			return nil, err
		}
		app.replaced = append(app.replaced, memberOf(i, old))
		app.tree.nodes[leafNodeIndex(i)] = node{leaf: l}
		app.tree.blankPath(i)
		app.updated = append(app.updated, i)
	}
	for _, rp := range props {
		if rp.p.typ != proposalRemove {
			continue
		}
		i := rp.p.remove
		app.removed = append(app.removed, memberOf(i, app.tree.leaf(i)))
		app.tree.removeLeaf(i)
	}
	for _, rp := range props {
		if rp.p.typ != proposalAdd {
			continue
		}
		kp := rp.p.add
		if err := p.checkKeyPackage(cfg, kp, app.extensions); err != nil {
			return nil, err
		}
		for _, m := range app.tree.members() {
			if bytes.Equal(app.tree.leaf(m).signatureKey, kp.leaf.signatureKey) {
				return nil, commitErr("an add of a client already in the group")
			}
		}
		l := kp.leaf
		app.adds = append(app.adds, app.tree.addLeaf(&l))
		app.addKeyPackages = append(app.addKeyPackages, kp)
		if app.tree.numLeaves() > MaxLeaves {
			return nil, errTooLarge
		}
	}
	for _, rp := range props {
		switch rp.p.typ {
		case proposalPSK:
			app.psks = append(app.psks, rp.p.psk)
		case proposalExternalInit:
			app.externalInit = rp.p.externalInit
		case proposalReInit:
			r := rp.p.reinit
			app.reinit = &r
		}
	}
	return app, nil
}

// checkKeyPackage checks a key package for an add, RFC 9420, Section
// 10.1, and its lifetime against the clock.
func (p *publicState) checkKeyPackage(cfg *Config, kp *keyPackage, exts []Extension) error {
	if err := kp.verify(p.s); err != nil {
		return err
	}
	lt := kp.leaf.lifetime
	now := uint64(cfg.now().Unix())
	if now < lt.NotBefore || now > lt.NotAfter {
		return commitErr("an add's key package is outside its lifetime")
	}
	if cfg.MaxLifetime > 0 && lt.NotAfter-lt.NotBefore > uint64(cfg.MaxLifetime/time.Second) {
		return commitErr("an add's key package has a lifetime longer than allowed")
	}
	return p.checkLeafInGroup(cfg, &kp.leaf, 0, exts)
}

// checkLeafInGroup checks a leaf the group takes in against itself, the
// application's credential check and the group's extensions.
func (p *publicState) checkLeafInGroup(cfg *Config, l *leafNode, leafIndex uint32, exts []Extension) error {
	if err := checkLeafSelf(p.s, l, p.ctx.groupID, leafIndex); err != nil {
		return err
	}
	if cfg.CheckCredential != nil {
		if err := cfg.CheckCredential(l.credential, l.signatureKey); err != nil {
			return fmt.Errorf("mls: credential: %w", err)
		}
	}
	return checkLeafSupports(l, exts)
}

// checkLeafSupports checks that a leaf supports the group's extensions
// and what its required_capabilities extension requires.
func checkLeafSupports(l *leafNode, exts []Extension) error {
	for _, e := range exts {
		if !l.capabilities.supportsExtension(e.Type) {
			return fmt.Errorf("mls: a member does not support group extension 0x%04x", uint16(e.Type))
		}
	}
	rcExt, ok := findExtension(exts, ExtensionRequiredCapabilities)
	if !ok {
		return nil
	}
	rc, err := unmarshalRequiredCapabilities(rcExt.Data)
	if err != nil {
		return err
	}
	for _, t := range rc.extensions {
		if !l.capabilities.supportsExtension(t) {
			return fmt.Errorf("mls: a member does not support required extension 0x%04x", uint16(t))
		}
	}
	for _, t := range rc.proposals {
		if !l.capabilities.supportsProposal(proposalType(t)) {
			return fmt.Errorf("mls: a member does not support required proposal 0x%04x", t)
		}
	}
	for _, t := range rc.credentials {
		if !slices.Contains(l.capabilities.Credentials, t) {
			return fmt.Errorf("mls: a member does not support required credential 0x%04x", uint16(t))
		}
	}
	return nil
}

// checkGroupExtensions checks the group's extensions parse and that
// every member supports them and every credential type in use.
func (p *publicState) checkGroupExtensions(t *ratchetTree, exts []Extension) error {
	if err := uniqueExtensions(exts); err != nil {
		return err
	}
	if e, ok := findExtension(exts, ExtensionExternalSenders); ok {
		if _, err := unmarshalExternalSenders(e.Data); err != nil {
			return err
		}
	}
	var inUse []CredentialType
	for _, i := range t.members() {
		if ct := t.leaf(i).credential.Type; !slices.Contains(inUse, ct) {
			inUse = append(inUse, ct)
		}
	}
	for _, i := range t.members() {
		l := t.leaf(i)
		if err := checkLeafSupports(l, exts); err != nil {
			return err
		}
		for _, ct := range inUse {
			if !slices.Contains(l.capabilities.Credentials, ct) {
				return fmt.Errorf("mls: leaf %d does not support credential type 0x%04x in use", i, uint16(ct))
			}
		}
	}
	return nil
}

// Changes is what a staged commit does to the group.
type Changes struct {
	// Sender is the committer.
	Sender            Sender
	AuthenticatedData []byte
	// Epoch is the epoch the commit starts.
	Epoch uint64
	// Added are the members the commit adds, at their new leaves. An
	// external join's new member is here too.
	Added []Member
	// Removed are the members the commit removes, as they were.
	Removed []Member
	// Updated are the members whose leaves the commit replaces, as they
	// are after it, the committer's own when the commit has a path.
	Updated []Member
	// Replaced are the leaves Updated replace, as they were before the
	// commit, in the same order.
	Replaced []Member
	// Proposals are the proposals the commit applies, in its order, each
	// with who proposed it: the committer for one the commit carries
	// inline, which has no Ref. An add's Member.Leaf is the leaf it
	// takes.
	Proposals []Proposal
	// Extensions are the group context's extensions after the commit.
	Extensions        []Extension
	ExtensionsChanged bool
	// ExternalJoin is set for a commit by a client joining on its own.
	ExternalJoin bool
	// RemovesSelf is set when the commit removes this member.
	RemovesSelf bool
	// ReInit is set when the commit closes the group for a new one.
	ReInit bool
}

// StagedCommit is a commit that has been checked but not applied.
type StagedCommit struct {
	owner     any
	epoch     uint64
	changes   Changes
	message   []byte
	welcome   []byte
	groupInfo []byte
	next      *Group
	public    *publicState
}

// Changes returns what the commit does.
func (c *StagedCommit) Changes() Changes { return c.changes }

// Message returns a commit this member made, as the MLSMessage to send.
func (c *StagedCommit) Message() []byte { return bytes.Clone(c.message) }

// Welcome returns the Welcome for the members a commit this member made
// adds, or nil when it adds none.
func (c *StagedCommit) Welcome() []byte { return bytes.Clone(c.welcome) }

// GroupInfo returns, for a commit this member made, the signed group
// info of the epoch it starts, with the ratchet tree and the external
// public key, for a client that joins with an external commit.
func (c *StagedCommit) GroupInfo() []byte { return bytes.Clone(c.groupInfo) }

// stagedPublic is a commit checked against a group's public state.
type stagedPublic struct {
	app           *applied
	committerLeaf uint32
	path          *updatePath
	fdp           []uint32
	provisional   groupContext
	next          groupContext
	interim       []byte
	changes       Changes
	// replacedCommitter is the committer's leaf before its path.
	replacedCommitter Member
}

// describeProposals describes the proposals a commit applies, in its
// order, with who proposed each: an inline one is the committer's.
func (p *publicState) describeProposals(app *applied, committer Sender) []Proposal {
	var out []Proposal
	add := 0
	for _, rp := range app.props {
		d := p.describeProposal(pendingProposal{ref: rp.ref, proposal: rp.p, sender: rp.sender})
		if rp.ref == nil {
			d.Sender = committer
			d.Sender.Credential = committer.Credential.clone()
			d.Sender.SignatureKey = bytes.Clone(committer.SignatureKey)
		}
		if rp.p.typ == proposalAdd && add < len(app.adds) {
			d.Member.Leaf = app.adds[add]
			add++
		}
		out = append(out, *d)
	}
	return out
}

// stagePublic checks a commit and applies it to a copy of the public
// state: its proposals, its path, and the new group context and
// transcript. It checks no secret: the membership tag and the
// confirmation tag are the caller's.
func (p *publicState) stagePublic(cfg *Config, pending []pendingProposal, a *authenticatedContent) (*stagedPublic, error) {
	c := a.content.commit
	committer := a.content.sender
	if committer.typ != senderMember && committer.typ != senderNewMemberCommit {
		return nil, commitErr("a commit from sender type %d", committer.typ)
	}
	props, err := resolveProposals(pending, committer, c)
	if err != nil {
		return nil, err
	}
	app, err := p.applyProposals(cfg, props, committer)
	if err != nil {
		return nil, err
	}
	if app.pathRequired && c.path == nil {
		return nil, commitErr("the commit needs an update path")
	}
	sp := &stagedPublic{app: app, path: c.path, committerLeaf: committer.index}
	external := committer.typ == senderNewMemberCommit
	if external {
		sp.committerLeaf = app.tree.freeLeaf()
		if sp.committerLeaf == app.tree.numLeaves() {
			app.tree.extend()
		}
		if app.tree.numLeaves() > MaxLeaves {
			return nil, errTooLarge
		}
	}
	if c.path != nil {
		l := &c.path.leaf
		if l.source != leafSourceCommit {
			return nil, commitErr("the path's leaf has source %d", l.source)
		}
		if err := p.checkLeafInGroup(cfg, l, sp.committerLeaf, app.extensions); err != nil {
			return nil, err
		}
		if !external {
			old := app.tree.leaf(sp.committerLeaf)
			if bytes.Equal(old.encryptionKey, l.encryptionKey) {
				return nil, commitErr("the committer's leaf keeps its encryption key")
			}
			if err := cfg.successor(old.credential, l.credential); err != nil {
				return nil, err
			}
			sp.replacedCommitter = memberOf(sp.committerLeaf, old)
		}
		keys := [][]byte{l.encryptionKey}
		for _, n := range c.path.nodes {
			keys = append(keys, n.encryptionKey)
		}
		for i, k := range keys {
			if err := p.s.checkHPKEPublicKey(k); err != nil {
				return nil, err
			}
			if app.tree.hasEncryptionKey(k) || slices.ContainsFunc(keys[:i], func(o []byte) bool { return bytes.Equal(o, k) }) {
				return nil, commitErr("the path repeats an encryption key")
			}
		}
		sp.fdp, err = app.tree.mergeUpdatePath(p.s, sp.committerLeaf, c.path)
		if err != nil {
			return nil, err
		}
	}
	if err := app.tree.verifyUniqueKeys(); err != nil {
		return nil, err
	}
	if err := p.checkGroupExtensions(app.tree, app.extensions); err != nil {
		return nil, err
	}
	sp.provisional = groupContext{
		version:                 p.ctx.version,
		suite:                   p.ctx.suite,
		groupID:                 p.ctx.groupID,
		epoch:                   p.ctx.epoch + 1,
		treeHash:                app.tree.treeHash(p.s),
		confirmedTranscriptHash: p.ctx.confirmedTranscriptHash,
		extensions:              app.extensions,
	}
	sp.next = sp.provisional
	sp.next.confirmedTranscriptHash = p.s.confirmedTranscriptHash(p.interim, a.wireFormat, &a.content, a.auth.signature)
	sp.interim = p.s.interimTranscriptHash(sp.next.confirmedTranscriptHash, a.auth.confirmationTag)

	ch := Changes{
		Sender:            p.describeSender(committer, a),
		AuthenticatedData: a.content.authenticatedData,
		Epoch:             sp.next.epoch,
		Removed:           app.removed,
		Extensions:        cloneExtensions(app.extensions),
		ExtensionsChanged: app.extensionsChanged,
		ExternalJoin:      external,
		ReInit:            app.reinit != nil,
	}
	for _, i := range app.adds {
		ch.Added = append(ch.Added, memberOf(i, app.tree.leaf(i)))
	}
	if external {
		ch.Sender.Leaf = sp.committerLeaf
		ch.Added = append(ch.Added, memberOf(sp.committerLeaf, app.tree.leaf(sp.committerLeaf)))
	}
	for _, i := range app.updated {
		ch.Updated = append(ch.Updated, memberOf(i, app.tree.leaf(i)))
	}
	ch.Replaced = slices.Clone(app.replaced)
	if c.path != nil && !external {
		ch.Updated = append(ch.Updated, memberOf(sp.committerLeaf, app.tree.leaf(sp.committerLeaf)))
		ch.Replaced = append(ch.Replaced, sp.replacedCommitter)
	}
	ch.Proposals = p.describeProposals(app, ch.Sender)
	sp.changes = ch
	return sp, nil
}

// stageCommit checks a received commit and derives the epoch it starts.
func (g *Group) stageCommit(a *authenticatedContent) (*StagedCommit, error) {
	if a.content.sender.typ == senderMember && a.content.sender.index == g.priv.leaf {
		return nil, ErrOwnMessage
	}
	sp, err := g.stagePublic(&g.cfg, g.pending, a)
	if err != nil {
		return nil, err
	}
	sc := &StagedCommit{owner: g, epoch: g.ctx.epoch, changes: sp.changes}
	for _, m := range sp.app.removed {
		if m.Leaf == g.priv.leaf {
			sc.changes.RemovesSelf = true
			return sc, nil
		}
	}
	priv := g.priv.clone()
	commitSecret := g.s.zero()
	if sp.path != nil {
		keys, cs, err := sp.app.tree.openUpdatePath(g.s, sp.committerLeaf, sp.path, sp.fdp, g.priv, sp.app.adds, sp.provisional.bytes())
		if err != nil {
			return nil, err
		}
		commitSecret = cs
		for x, k := range keys {
			priv.keys[x] = k
		}
	}
	priv.prune(sp.app.tree)
	pskSecret, err := g.pskSecret(sp.app.psks)
	if err != nil {
		return nil, err
	}
	initSecret := g.es.init
	if sp.app.externalInit != nil {
		extPriv, _, err := g.s.deriveKeyPair(g.es.external)
		if err != nil {
			return nil, err
		}
		initSecret, err = g.s.externalInitReceive(extPriv, sp.app.externalInit)
		if err != nil {
			return nil, err
		}
	}
	ctx := sp.next.bytes()
	_, es := g.s.epochFromJoiner(g.s.joinerSecret(initSecret, commitSecret, ctx), pskSecret, ctx)
	if !equal(g.s.confirmationTag(es.confirmation, sp.next.confirmedTranscriptHash), a.auth.confirmationTag) {
		return nil, commitErr("the confirmation tag does not verify")
	}
	sc.next = g.nextEpoch(sp, priv, es, a.auth.confirmationTag)
	return sc, nil
}

// nextEpoch builds the group's state in the epoch a commit starts.
func (g *Group) nextEpoch(sp *stagedPublic, priv *privateTree, es epochSecrets, tag []byte) *Group {
	next := &Group{
		cfg: g.cfg,
		publicState: publicState{
			s:               g.s,
			ctx:             sp.next,
			tree:            sp.app.tree,
			interim:         sp.interim,
			confirmationTag: tag,
		},
		priv:   priv,
		signer: g.signer,
		es:     es,
		st:     newSecretTree(g.s, es.encryption, sp.app.tree.numLeaves()),
	}
	next.es.encryption = nil // consumed by the secret tree, RFC 9420, Section 9.2
	next.resumption = append(slices.Clone(g.resumption), resumptionPSK{epoch: sp.next.epoch, psk: es.resumption})
	if over := len(next.resumption) - ResumptionPSKEpochs; over > 0 {
		next.resumption = next.resumption[over:]
	}
	if sp.app.reinit != nil {
		next.status = statusReInit
	}
	return next
}

// pskSecret looks up the PSKs a commit or a Welcome names and chains
// them.
func (g *Group) pskSecret(ids []preSharedKeyID) ([]byte, error) {
	var in []pskInput
	for _, id := range ids {
		switch id.typ {
		case pskExternal:
			if g.cfg.PSKs == nil {
				return nil, errPSK
			}
			v, ok := g.cfg.PSKs.ExternalPSK(id.id)
			if !ok {
				return nil, errPSK
			}
			in = append(in, pskInput{id: id, psk: v})
		case pskResumption:
			if id.usage != resumptionApplication || !bytes.Equal(id.groupID, g.ctx.groupID) {
				return nil, errPSK
			}
			i := slices.IndexFunc(g.resumption, func(r resumptionPSK) bool { return r.epoch == id.epoch })
			if i < 0 {
				return nil, errPSK
			}
			in = append(in, pskInput{id: id, psk: g.resumption[i].psk})
		}
	}
	return g.s.pskSecret(in), nil
}

// Merge applies a staged commit, which this group staged in its current
// epoch, and moves the group to the epoch it starts. A commit that
// removes this member, or reinitializes the group, leaves it inactive
// with every secret deleted; Epoch and Members then describe the epoch
// it was removed from. A removed member cannot derive the next epoch, so
// it stages its removal without checking the commit's confirmation tag:
// take a removal as final once the delivery service says the commit won
// its epoch.
func (g *Group) Merge(c *StagedCommit) error {
	if c == nil || c.owner != g || c.epoch != g.ctx.epoch || g.status != statusActive {
		return ErrStaleCommit
	}
	if c.changes.RemovesSelf {
		g.close(statusRemoved)
		return nil
	}
	*g = *c.next
	if g.status == statusReInit {
		g.close(statusReInit)
	}
	return nil
}

// close leaves the group inactive and deletes its secrets.
func (g *Group) close(status groupStatus) {
	g.status = status
	g.es = epochSecrets{}
	g.st = nil
	g.priv = &privateTree{leaf: g.priv.leaf}
	g.pending = nil
	g.resumption = nil
}

// CommitOptions sets what Commit changes.
type CommitOptions struct {
	// Add are the key packages of the clients to add.
	Add []*KeyPackage
	// Remove are the leaves of the members to remove.
	Remove []uint32
	// ReplaceExtensions replaces the group context's extensions with
	// Extensions.
	ReplaceExtensions bool
	Extensions        []Extension
	// IncludePending covers the pending proposals too, by reference,
	// leaving out those that conflict with the others: this member's
	// own removal, an update or remove of a member the commit removes,
	// a repeated add or PSK, and a second extensions change.
	IncludePending bool
	// AuthenticatedData travels with the commit in the clear.
	AuthenticatedData []byte
	// WelcomeExtensions travel in the group info of the commit's
	// Welcome, signed by this member and sealed to the members it adds,
	// who read them with Group.WelcomeExtensions. A commit that adds no
	// one has none.
	WelcomeExtensions []Extension
}

// Commit makes a commit with a fresh update path, which also gives this
// member new keys, and stages it. Send StagedCommit.Message, and merge
// it once the delivery service accepts it; if another commit wins the
// epoch, discard it and process that one.
func (g *Group) Commit(opts CommitOptions) (*StagedCommit, error) {
	if g.status != statusActive {
		return nil, ErrInactive
	}
	if len(opts.AuthenticatedData) > MaxContentBytes {
		return nil, errors.New("mls: authenticated data larger than MaxContentBytes")
	}
	if err := checkWelcomeExtensions(opts.WelcomeExtensions); err != nil {
		return nil, err
	}
	committer := sender{typ: senderMember, index: g.priv.leaf}
	var props []resolvedProposal
	var list []proposalOrRef
	inline := func(pr *proposal) {
		props = append(props, resolvedProposal{p: pr, sender: committer})
		list = append(list, proposalOrRef{proposal: pr})
	}
	if opts.ReplaceExtensions {
		inline(&proposal{typ: proposalGroupContextExtensions, extensions: cloneExtensions(opts.Extensions)})
	}
	for _, leaf := range opts.Remove {
		inline(&proposal{typ: proposalRemove, remove: leaf})
	}
	for _, kp := range opts.Add {
		if kp == nil {
			return nil, errors.New("mls: nil key package")
		}
		k := kp.kp
		inline(&proposal{typ: proposalAdd, add: &k})
	}
	if opts.IncludePending {
		for _, pp := range g.pending {
			if g.conflicts(pp, props) {
				continue
			}
			props = append(props, resolvedProposal{p: pp.proposal, sender: pp.sender, ref: pp.ref})
			list = append(list, proposalOrRef{reference: pp.ref})
		}
	}
	app, err := g.applyProposals(&g.cfg, props, committer)
	if err != nil {
		return nil, err
	}
	tree := app.tree
	own := g.tree.leaf(g.priv.leaf)
	tmpl := leafTemplate{
		signatureKey: own.signatureKey,
		credential:   own.credential,
		capabilities: own.capabilities,
		extensions:   own.extensions,
	}
	provisional := groupContext{
		version:                 g.ctx.version,
		suite:                   g.ctx.suite,
		groupID:                 g.ctx.groupID,
		epoch:                   g.ctx.epoch + 1,
		confirmedTranscriptHash: g.ctx.confirmedTranscriptHash,
		extensions:              app.extensions,
	}
	path, ps, _, err := tree.createUpdatePath(g.s, g.priv.leaf, tmpl, g.signer, g.ctx.groupID, app.adds, func(th []byte) []byte {
		provisional.treeHash = th
		return provisional.bytes()
	})
	if err != nil {
		return nil, err
	}
	if err := tree.verifyUniqueKeys(); err != nil {
		return nil, err
	}
	if err := g.checkGroupExtensions(tree, app.extensions); err != nil {
		return nil, err
	}
	a := &authenticatedContent{
		wireFormat: g.handshakeWireFormat(),
		content: framedContent{
			groupID:           g.ctx.groupID,
			epoch:             g.ctx.epoch,
			sender:            committer,
			authenticatedData: bytes.Clone(opts.AuthenticatedData),
			contentType:       contentCommit,
			commit:            &commit{proposals: list, path: path},
		},
	}
	oldCtx := g.ctxBytes()
	if err := g.s.signContent(g.signer, a, oldCtx); err != nil {
		return nil, err
	}
	next := provisional
	next.confirmedTranscriptHash = g.s.confirmedTranscriptHash(g.interim, a.wireFormat, &a.content, a.auth.signature)
	pskSecret, err := g.pskSecret(app.psks)
	if err != nil {
		return nil, err
	}
	ctx := next.bytes()
	joiner := g.s.joinerSecret(g.es.init, ps.commitSecret, ctx)
	welcomeSecret, es := g.s.epochFromJoiner(joiner, pskSecret, ctx)
	a.auth.confirmationTag = g.s.confirmationTag(es.confirmation, next.confirmedTranscriptHash)
	msg, err := g.frame(a, oldCtx)
	if err != nil {
		return nil, err
	}

	priv := &privateTree{leaf: g.priv.leaf, keys: map[uint32]hpkeKey{leafNodeIndex(g.priv.leaf): ps.leafKey}}
	for x, k := range ps.keys {
		priv.keys[x] = k
	}
	sp := &stagedPublic{
		app:     app,
		next:    next,
		interim: g.s.interimTranscriptHash(next.confirmedTranscriptHash, a.auth.confirmationTag),
	}
	sc := &StagedCommit{owner: g, epoch: g.ctx.epoch, message: msg}
	sc.next = g.nextEpoch(sp, priv, es, a.auth.confirmationTag)
	sc.changes = Changes{
		Sender:            g.describeSender(committer, a),
		AuthenticatedData: a.content.authenticatedData,
		Epoch:             next.epoch,
		Removed:           app.removed,
		Extensions:        cloneExtensions(app.extensions),
		ExtensionsChanged: app.extensionsChanged,
		ReInit:            app.reinit != nil,
	}
	for _, i := range app.adds {
		sc.changes.Added = append(sc.changes.Added, memberOf(i, tree.leaf(i)))
	}
	for _, i := range app.updated {
		sc.changes.Updated = append(sc.changes.Updated, memberOf(i, tree.leaf(i)))
	}
	sc.changes.Updated = append(sc.changes.Updated, memberOf(g.priv.leaf, tree.leaf(g.priv.leaf)))
	sc.changes.Replaced = append(slices.Clone(app.replaced), memberOf(g.priv.leaf, own))
	sc.changes.Proposals = g.describeProposals(app, sc.changes.Sender)

	sc.groupInfo, err = sc.next.signedGroupInfo(true, true)
	if err != nil {
		return nil, err
	}
	if len(app.adds) > 0 {
		sc.welcome, err = sc.next.welcome(welcomeSecret, joiner, app, ps, opts.WelcomeExtensions)
		if err != nil {
			return nil, err
		}
	} else if len(opts.WelcomeExtensions) > 0 {
		return nil, errors.New("mls: a commit that adds no one has no Welcome to carry extensions")
	}
	return sc, nil
}

// conflicts reports whether a pending proposal cannot join a commit that
// already covers props.
func (g *Group) conflicts(pp pendingProposal, props []resolvedProposal) bool {
	pr := pp.proposal
	target := func(rp resolvedProposal) (uint32, bool) {
		switch rp.p.typ {
		case proposalRemove:
			return rp.p.remove, true
		case proposalUpdate:
			return rp.sender.index, true
		}
		return 0, false
	}
	switch pr.typ {
	case proposalUpdate, proposalRemove:
		t, _ := target(resolvedProposal{p: pr, sender: pp.sender})
		if t == g.priv.leaf || g.tree.leaf(t) == nil {
			return true
		}
		for _, rp := range props {
			if o, ok := target(rp); ok && o == t {
				return true
			}
		}
	case proposalAdd:
		for _, rp := range props {
			if rp.p.typ == proposalAdd && bytes.Equal(rp.p.add.leaf.signatureKey, pr.add.leaf.signatureKey) {
				return true
			}
		}
	case proposalPSK:
		id := encode(pr.psk.marshal)
		for _, rp := range props {
			if rp.p.typ == proposalPSK && bytes.Equal(encode(rp.p.psk.marshal), id) {
				return true
			}
		}
	case proposalGroupContextExtensions:
		for _, rp := range props {
			if rp.p.typ == proposalGroupContextExtensions {
				return true
			}
		}
	case proposalReInit, proposalExternalInit:
		return true
	}
	return false
}
