// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package mls

import (
	"bytes"
	"errors"
	"fmt"
)

// signedGroupInfo returns the group info of the current epoch as an
// MLSMessage, signed by this member, with the ratchet tree and the
// external public key when asked.
func (g *Group) signedGroupInfo(withTree, withExternalPub bool) ([]byte, error) {
	gi, err := g.groupInfo(withTree, withExternalPub, nil)
	if err != nil {
		return nil, err
	}
	return encode((&mlsMessage{wireFormat: WireFormatGroupInfo, groupInfo: gi}).marshal), nil
}

func (g *Group) groupInfo(withTree, withExternalPub bool, extra []Extension) (*groupInfo, error) {
	gi := &groupInfo{context: g.ctx, confirmationTag: g.confirmationTag, signer: g.priv.leaf}
	gi.extensions = cloneExtensions(extra)
	if withTree {
		gi.extensions = append(gi.extensions, Extension{Type: ExtensionRatchetTree, Data: g.tree.bytes()})
	}
	if withExternalPub {
		_, pub, err := g.s.deriveKeyPair(g.es.external)
		if err != nil {
			return nil, err
		}
		gi.extensions = append(gi.extensions, Extension{Type: ExtensionExternalPub, Data: encode(func(w *writer) { w.vec(pub) })})
	}
	sig, err := g.s.signWithLabel(g.signer, "GroupInfoTBS", encode(gi.marshalTBS))
	if err != nil {
		return nil, err
	}
	gi.signature = sig
	return gi, nil
}

// GroupInfoOptions sets what GroupInfo includes.
type GroupInfoOptions struct {
	// RatchetTree includes the ratchet tree.
	RatchetTree bool
	// ExternalPub includes the external public key, which a client needs
	// to join with an external commit.
	ExternalPub bool
}

// GroupInfo returns the signed group info of the current epoch as an
// MLSMessage.
func (g *Group) GroupInfo(opts GroupInfoOptions) ([]byte, error) {
	if g.status != statusActive {
		return nil, ErrInactive
	}
	return g.signedGroupInfo(opts.RatchetTree, opts.ExternalPub)
}

// checkWelcomeExtensions checks the extensions a committer puts in its
// Welcome's group info: each of a type of its own, and none of a type
// RFC 9420 gives the group info.
func checkWelcomeExtensions(exts []Extension) error {
	for _, e := range exts {
		if e.Type == ExtensionRatchetTree || e.Type == ExtensionExternalPub {
			return fmt.Errorf("mls: a Welcome extension of type %d, which the group info sets itself", e.Type)
		}
	}
	return uniqueExtensions(exts)
}

// WelcomeExtensions returns the extensions the committer put in the
// group info of the Welcome this member joined with, which its
// signature covers and only the joiners read; nil for a member that
// joined otherwise. They are not part of the saved state, so read them
// before saving the group.
func (g *Group) WelcomeExtensions() []Extension {
	if len(g.welcomeExtensions) == 0 {
		return nil
	}
	return cloneExtensions(g.welcomeExtensions)
}

// welcome encrypts the new epoch's group info and, for each added
// member, the joiner secret and the path secret of the lowest node it
// shares with the committer.
func (g *Group) welcome(welcomeSecret, joiner []byte, app *applied, ps *pathSecrets, extra []Extension) ([]byte, error) {
	gi, err := g.groupInfo(!g.cfg.OmitRatchetTree, false, extra)
	if err != nil {
		return nil, err
	}
	key, nonce := g.s.welcomeKeyNonce(welcomeSecret)
	egi, err := g.s.seal(key, nonce, nil, encode(gi.marshal))
	if err != nil {
		return nil, err
	}
	w := &welcome{suite: g.s.id, encryptedGroupInfo: egi}
	for i, leaf := range app.adds {
		kp := app.addKeyPackages[i]
		gs := groupSecrets{joinerSecret: joiner, psks: app.psks}
		if ps != nil {
			lca := commonAncestor(leafNodeIndex(g.priv.leaf), leafNodeIndex(leaf))
			gs.pathSecret = ps.secrets[lca]
		}
		kem, ct, err := g.s.encryptWithLabel(kp.initKey, "Welcome", egi, encode(gs.marshal))
		if err != nil {
			return nil, err
		}
		w.secrets = append(w.secrets, encryptedGroupSecrets{newMember: kp.ref(g.s), secrets: hpkeCiphertext{kemOutput: kem, ciphertext: ct}})
	}
	return encode((&mlsMessage{wireFormat: WireFormatWelcome, welcome: w}).marshal), nil
}

// WelcomeKeyPackageRefs returns the references of the key packages a
// Welcome is for, so a client finds which of its own it uses.
func WelcomeKeyPackageRefs(welcomeMessage []byte) ([][]byte, error) {
	m, err := parseMessage(welcomeMessage)
	if err != nil {
		return nil, err
	}
	if m.wireFormat != WireFormatWelcome {
		return nil, errors.New("mls: message is not a Welcome")
	}
	var out [][]byte
	for _, s := range m.welcome.secrets {
		out = append(out, bytes.Clone(s.newMember))
	}
	return out, nil
}

// JoinOptions sets what JoinGroup and JoinExternal use beyond the
// message.
type JoinOptions struct {
	// RatchetTree is the group's tree, for a Welcome or group info that
	// leaves it out.
	RatchetTree []byte
}

// JoinGroup joins a group with a Welcome made for one of this client's
// key packages, checking the group's tree and its signer.
func JoinGroup(welcomeMessage []byte, kp *KeyPackage, secrets *KeyPackageSecrets, signer *SignatureKey, opts JoinOptions, cfg Config) (*Group, error) {
	m, err := parseMessage(welcomeMessage)
	if err != nil {
		return nil, err
	}
	if m.wireFormat != WireFormatWelcome {
		return nil, errors.New("mls: message is not a Welcome")
	}
	w := m.welcome
	if w.suite != kp.kp.suite || signer.CipherSuite() != kp.kp.suite {
		return nil, errors.New("mls: Welcome, key package and signature key are for different suites")
	}
	if !bytes.Equal(signer.PublicKey(), kp.kp.leaf.signatureKey) {
		return nil, errors.New("mls: the signature key is not the key package's")
	}
	s, err := suiteFor(w.suite)
	if err != nil {
		return nil, err
	}
	ref := kp.kp.ref(s)
	var egs *encryptedGroupSecrets
	for i := range w.secrets {
		if bytes.Equal(w.secrets[i].newMember, ref) {
			egs = &w.secrets[i]
			break
		}
	}
	if egs == nil {
		return nil, errors.New("mls: the Welcome is not for this key package")
	}
	gsb, err := s.decryptWithLabel(secrets.initPriv, "Welcome", w.encryptedGroupInfo, egs.secrets.kemOutput, egs.secrets.ciphertext)
	if err != nil {
		return nil, fmt.Errorf("mls: Welcome: %w", err)
	}
	var gs groupSecrets
	if err := decode(gsb, gs.unmarshal); err != nil {
		return nil, err
	}
	if len(gs.joinerSecret) != s.nh {
		return nil, errors.New("mls: Welcome joiner secret has the wrong size")
	}
	g := &Group{cfg: cfg, publicState: publicState{s: s}, signer: signer}
	for _, id := range gs.psks {
		if id.typ == pskResumption && id.usage != resumptionApplication {
			return nil, errors.New("mls: joining a reinitialized or branched group is not supported")
		}
	}
	pskSecret, err := g.pskSecret(gs.psks)
	if err != nil {
		return nil, err
	}
	member := s.extract(gs.joinerSecret, pskSecret)
	key, nonce := s.welcomeKeyNonce(s.deriveSecret(member, "welcome"))
	gib, err := s.open(key, nonce, nil, w.encryptedGroupInfo)
	if err != nil {
		return nil, fmt.Errorf("mls: Welcome group info: %w", err)
	}
	var gi groupInfo
	if err := decode(gib, gi.unmarshal); err != nil {
		return nil, err
	}
	if gi.context.suite != s.id || gi.context.version != protocolVersionMLS10 {
		return nil, errors.New("mls: group info is for another suite or version")
	}
	g.ctx = gi.context
	for _, e := range gi.extensions {
		if e.Type != ExtensionRatchetTree && e.Type != ExtensionExternalPub {
			g.welcomeExtensions = append(g.welcomeExtensions, Extension{Type: e.Type, Data: bytes.Clone(e.Data)})
		}
	}
	tree, err := g.joinTree(&cfg, &gi, opts.RatchetTree)
	if err != nil {
		return nil, err
	}
	g.tree = tree

	own := encode(kp.kp.leaf.marshal)
	leaf := uint32(0)
	for ; leaf < tree.numLeaves(); leaf++ {
		if l := tree.leaf(leaf); l != nil && bytes.Equal(encode(l.marshal), own) {
			break
		}
	}
	if leaf == tree.numLeaves() {
		return nil, errors.New("mls: the group's tree does not hold this key package's leaf")
	}
	g.priv = &privateTree{leaf: leaf, keys: map[uint32]hpkeKey{leafNodeIndex(leaf): {priv: secrets.encryptionPriv, pub: kp.kp.leaf.encryptionKey}}}
	if pub, err := s.hpkePublic(secrets.encryptionPriv); err != nil || !bytes.Equal(pub, kp.kp.leaf.encryptionKey) {
		return nil, errors.New("mls: the key package secrets do not match its leaf")
	}
	if gs.pathSecret != nil {
		if gi.signer == leaf {
			return nil, errors.New("mls: the Welcome's signer is this member")
		}
		lca := commonAncestor(leafNodeIndex(leaf), leafNodeIndex(gi.signer))
		fdp := tree.filteredDirectPath(gi.signer)
		start := -1
		for i, x := range fdp {
			if x == lca {
				start = i
			}
		}
		if start < 0 {
			return nil, errors.New("mls: the Welcome's path secret is for a node off the signer's path")
		}
		var upn []updatePathNode
		for _, x := range fdp[start:] {
			upn = append(upn, updatePathNode{encryptionKey: tree.nodes[x].encryptionKey()})
		}
		keys, _, err := derivePathKeys(s, gs.pathSecret, fdp[start:], upn)
		if err != nil {
			return nil, err
		}
		for x, k := range keys {
			g.priv.keys[x] = k
		}
	}
	ctx := g.ctx.bytes()
	_, g.es = s.epochFromJoiner(gs.joinerSecret, pskSecret, ctx)
	if !equal(s.confirmationTag(g.es.confirmation, g.ctx.confirmedTranscriptHash), gi.confirmationTag) {
		return nil, errors.New("mls: the group info's confirmation tag does not verify")
	}
	g.confirmationTag = gi.confirmationTag
	g.interim = s.interimTranscriptHash(g.ctx.confirmedTranscriptHash, gi.confirmationTag)
	g.st = newSecretTree(s, g.es.encryption, tree.numLeaves())
	g.es.encryption = nil // consumed by the secret tree, RFC 9420, Section 9.2
	g.resumption = []resumptionPSK{{epoch: g.ctx.epoch, psk: g.es.resumption}}
	return g, nil
}

// joinTree takes the ratchet tree from a group info or the caller, and
// checks it as a joiner must, RFC 9420, Section 12.4.3.1: its hash, the
// group info's signature, every parent's parent hash, every leaf, the
// unmerged leaves and the uniqueness of keys.
func (p *publicState) joinTree(cfg *Config, gi *groupInfo, given []byte) (*ratchetTree, error) {
	if err := uniqueExtensions(gi.extensions); err != nil {
		return nil, err
	}
	var tree *ratchetTree
	var err error
	if e, ok := findExtension(gi.extensions, ExtensionRatchetTree); ok {
		tree, err = parseTree(e.Data)
	} else if given != nil {
		tree, err = parseTree(given)
	} else {
		return nil, errors.New("mls: no ratchet tree for the group")
	}
	if err != nil {
		return nil, err
	}
	s := p.s
	if !bytes.Equal(tree.treeHash(s), gi.context.treeHash) {
		return nil, treeErr("the tree does not match the group's tree hash")
	}
	signer := tree.leaf(gi.signer)
	if signer == nil {
		return nil, errors.New("mls: the group info's signer is not a member")
	}
	if err := s.verifyWithLabel(signer.signatureKey, "GroupInfoTBS", encode(gi.marshalTBS), gi.signature); err != nil {
		return nil, fmt.Errorf("mls: group info: %w", err)
	}
	if err := tree.verifyParentHashes(s); err != nil {
		return nil, err
	}
	if err := tree.verifyUnmergedLeaves(); err != nil {
		return nil, err
	}
	if err := tree.verifyUniqueKeys(); err != nil {
		return nil, err
	}
	pp := &publicState{s: s, ctx: gi.context}
	for _, i := range tree.members() {
		if err := pp.checkLeafInGroup(cfg, tree.leaf(i), i, gi.context.extensions); err != nil {
			return nil, fmt.Errorf("leaf %d: %w", i, err)
		}
	}
	if err := pp.checkGroupExtensions(tree, gi.context.extensions); err != nil {
		return nil, err
	}
	return tree, nil
}
