// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package mls

import (
	"bytes"
	"errors"
	"fmt"
)

// ExternalJoinOptions sets what JoinExternal puts in its commit.
type ExternalJoinOptions struct {
	// RatchetTree is the group's tree, for a group info that leaves it
	// out.
	RatchetTree []byte
	// SupportedExtensions are the extension types beyond RFC 9420's the
	// client supports, listed in its leaf's capabilities.
	SupportedExtensions []ExtensionType
	// LeafExtensions are carried in the client's leaf.
	LeafExtensions []Extension
	// AuthenticatedData travels with the commit in the clear.
	AuthenticatedData []byte
}

// JoinExternal joins a group on the client's own with an external
// commit, RFC 9420, Section 12.4.3.2, from a group info that carries the
// group's external public key. It returns the group in the epoch the
// commit starts and the commit to send. Keep the group only once the
// delivery service accepts the commit: if another commit wins the
// epoch, the join failed and starts again from a newer group info.
func JoinExternal(groupInfoMessage []byte, signer *SignatureKey, cred Credential, opts ExternalJoinOptions, cfg Config) (*Group, []byte, error) {
	m, err := parseMessage(groupInfoMessage)
	if err != nil {
		return nil, nil, err
	}
	if m.wireFormat != WireFormatGroupInfo {
		return nil, nil, errors.New("mls: message is not a group info")
	}
	gi := m.groupInfo
	s, err := suiteFor(gi.context.suite)
	if err != nil {
		return nil, nil, err
	}
	if signer.CipherSuite() != s.id || gi.context.version != protocolVersionMLS10 {
		return nil, nil, errors.New("mls: the signature key is for another suite than the group")
	}
	if len(opts.AuthenticatedData) > MaxContentBytes {
		return nil, nil, errors.New("mls: authenticated data larger than MaxContentBytes")
	}
	g := &Group{cfg: cfg, publicState: publicState{s: s, ctx: gi.context}, signer: signer}
	tree, err := g.joinTree(&cfg, gi, opts.RatchetTree)
	if err != nil {
		return nil, nil, err
	}
	g.tree = tree
	g.confirmationTag = gi.confirmationTag
	g.interim = s.interimTranscriptHash(gi.context.confirmedTranscriptHash, gi.confirmationTag)
	ext, ok := findExtension(gi.extensions, ExtensionExternalPub)
	if !ok {
		return nil, nil, errors.New("mls: the group info has no external public key")
	}
	var externalPub []byte
	if err := decode(ext.Data, func(r *reader) { externalPub = r.vec() }); err != nil {
		return nil, nil, err
	}
	kemOutput, initSecret, err := s.externalInitSend(externalPub)
	if err != nil {
		return nil, nil, err
	}

	committer := sender{typ: senderNewMemberCommit}
	ei := &proposal{typ: proposalExternalInit, externalInit: kemOutput}
	app, err := g.applyProposals(&cfg, []resolvedProposal{{p: ei, sender: committer}}, committer)
	if err != nil {
		return nil, nil, err
	}
	leaf := app.tree.freeLeaf()
	if leaf == app.tree.numLeaves() {
		app.tree.extend()
	}
	tmpl := leafTemplate{
		signatureKey: signer.PublicKey(),
		credential:   cred,
		capabilities: defaultCapabilities(s.id, cred.Type, append(append([]ExtensionType{}, opts.SupportedExtensions...), extensionTypes(gi.context.extensions)...), opts.LeafExtensions),
		extensions:   cloneExtensions(opts.LeafExtensions),
	}
	provisional := groupContext{
		version:                 gi.context.version,
		suite:                   s.id,
		groupID:                 gi.context.groupID,
		epoch:                   gi.context.epoch + 1,
		confirmedTranscriptHash: gi.context.confirmedTranscriptHash,
		extensions:              app.extensions,
	}
	path, ps, _, err := app.tree.createUpdatePath(s, leaf, tmpl, signer, gi.context.groupID, nil, func(th []byte) []byte {
		provisional.treeHash = th
		return provisional.bytes()
	})
	if err != nil {
		return nil, nil, err
	}
	if err := g.checkLeafInGroup(&cfg, app.tree.leaf(leaf), leaf, app.extensions); err != nil {
		return nil, nil, err
	}
	if err := app.tree.verifyUniqueKeys(); err != nil {
		return nil, nil, err
	}
	if err := g.checkGroupExtensions(app.tree, app.extensions); err != nil {
		return nil, nil, err
	}
	a := &authenticatedContent{
		wireFormat: WireFormatPublicMessage,
		content: framedContent{
			groupID:           gi.context.groupID,
			epoch:             gi.context.epoch,
			sender:            committer,
			authenticatedData: bytes.Clone(opts.AuthenticatedData),
			contentType:       contentCommit,
			commit:            &commit{proposals: []proposalOrRef{{proposal: ei}}, path: path},
		},
	}
	if err := s.signContent(signer, a, g.ctxBytes()); err != nil {
		return nil, nil, err
	}
	next := provisional
	next.confirmedTranscriptHash = s.confirmedTranscriptHash(g.interim, a.wireFormat, &a.content, a.auth.signature)
	ctx := next.bytes()
	_, es := s.epochFromJoiner(s.joinerSecret(initSecret, ps.commitSecret, ctx), s.zero(), ctx)
	a.auth.confirmationTag = s.confirmationTag(es.confirmation, next.confirmedTranscriptHash)
	msg := encode((&mlsMessage{wireFormat: WireFormatPublicMessage, public: &publicMessage{content: a.content, auth: a.auth}}).marshal)

	priv := &privateTree{leaf: leaf, keys: map[uint32]hpkeKey{leafNodeIndex(leaf): ps.leafKey}}
	for x, k := range ps.keys {
		priv.keys[x] = k
	}
	g.priv = priv
	sp := &stagedPublic{app: app, next: next, interim: s.interimTranscriptHash(next.confirmedTranscriptHash, a.auth.confirmationTag)}
	return g.nextEpoch(sp, priv, es, a.auth.confirmationTag), msg, nil
}

// ExternalSender is a party outside a group allowed to propose adds and
// removes to it, such as the delivery service, listed in the group's
// external_senders extension.
type ExternalSender struct {
	SignatureKey []byte
	Credential   Credential
}

// ExternalSendersExtension returns the group context extension that
// lists the external senders, in the order their proposals name them.
func ExternalSendersExtension(senders []ExternalSender) Extension {
	es := make([]externalSender, len(senders))
	for i, s := range senders {
		es[i] = externalSender{signatureKey: s.SignatureKey, credential: s.Credential}
	}
	return Extension{Type: ExtensionExternalSenders, Data: marshalExternalSenders(es)}
}

// ProposeExternalRemove makes, for the external sender at senderIndex,
// a proposal to remove the member at a leaf from a group in an epoch.
// A member of the group commits it. The proposal is a public message,
// the only kind an external sender can send.
func ProposeExternalRemove(signer *SignatureKey, groupID []byte, epoch uint64, senderIndex, leaf uint32) ([]byte, error) {
	s, err := suiteFor(signer.CipherSuite())
	if err != nil {
		return nil, err
	}
	if len(groupID) == 0 || len(groupID) > MaxContentBytes {
		return nil, fmt.Errorf("mls: group ID of %d bytes", len(groupID))
	}
	a := &authenticatedContent{
		wireFormat: WireFormatPublicMessage,
		content: framedContent{
			groupID:     bytes.Clone(groupID),
			epoch:       epoch,
			sender:      sender{typ: senderExternal, index: senderIndex},
			contentType: contentProposal,
			proposal:    &proposal{typ: proposalRemove, remove: leaf},
		},
	}
	if err := s.signContent(signer, a, nil); err != nil {
		return nil, err
	}
	return encode((&mlsMessage{wireFormat: WireFormatPublicMessage, public: &publicMessage{content: a.content, auth: a.auth}}).marshal), nil
}
