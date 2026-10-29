// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package mls

import (
	"bytes"
	"compress/gzip"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The interoperability test vectors of the MLS working group, filtered
// to the suites the package implements; testdata/README.md says where
// they come from.

type hexBytes []byte

func (h *hexBytes) UnmarshalJSON(b []byte) error {
	var s *string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if s == nil {
		*h = nil
		return nil
	}
	v, err := hex.DecodeString(*s)
	if err != nil {
		return err
	}
	*h = v
	return nil
}

func loadVectors(t testing.TB, name string, v any) {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name+".json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewDecoder(zr).Decode(v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func mustSuite(t *testing.T, cs uint16) *suite {
	t.Helper()
	s, err := suiteFor(CipherSuite(cs))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func checkBytes(t *testing.T, what string, got, want []byte) {
	t.Helper()
	if !bytes.Equal(got, want) {
		t.Errorf("%s:\n got %x\nwant %x", what, got, want)
	}
}

func TestVectorsTreeMath(t *testing.T) {
	var vs []struct {
		NLeaves uint32    `json:"n_leaves"`
		NNodes  uint32    `json:"n_nodes"`
		Root    uint32    `json:"root"`
		Left    []*uint32 `json:"left"`
		Right   []*uint32 `json:"right"`
		Parent  []*uint32 `json:"parent"`
		Sibling []*uint32 `json:"sibling"`
	}
	loadVectors(t, "tree-math", &vs)
	for _, v := range vs {
		if got := nodeWidth(v.NLeaves); got != v.NNodes {
			t.Errorf("nodeWidth(%d) = %d, want %d", v.NLeaves, got, v.NNodes)
		}
		r := root(v.NLeaves)
		if r != v.Root {
			t.Errorf("root(%d) = %d, want %d", v.NLeaves, r, v.Root)
		}
		for x := uint32(0); x < v.NNodes; x++ {
			check := func(what string, want *uint32, f func() uint32, ok bool) {
				t.Helper()
				if want == nil {
					if ok {
						t.Errorf("%s(%d) in %d leaves defined, want none", what, x, v.NLeaves)
					}
					return
				}
				if !ok || f() != *want {
					t.Errorf("%s(%d) in %d leaves wrong, want %d", what, x, v.NLeaves, *want)
				}
			}
			check("left", v.Left[x], func() uint32 { return left(x) }, !isLeaf(x))
			check("right", v.Right[x], func() uint32 { return right(x) }, !isLeaf(x))
			check("parent", v.Parent[x], func() uint32 { return parent(x) }, x != r)
			check("sibling", v.Sibling[x], func() uint32 { return sibling(x) }, x != r)
		}
	}
}

func TestVectorsDeserialization(t *testing.T) {
	var vs []struct {
		Header hexBytes `json:"vlbytes_header"`
		Length int      `json:"length"`
	}
	loadVectors(t, "deserialization", &vs)
	for _, v := range vs {
		r := newReader(v.Header)
		if n := r.varint(); r.err != nil || n != v.Length || len(r.b) != 0 {
			t.Errorf("varint(%x) = %d, %v; want %d", v.Header, n, r.err, v.Length)
		}
		var w writer
		w.varint(v.Length)
		checkBytes(t, "varint encoding", w.b, v.Header)
	}
}

func TestVectorsCryptoBasics(t *testing.T) {
	var vs []struct {
		CipherSuite uint16 `json:"cipher_suite"`
		RefHash     struct {
			Label string   `json:"label"`
			Value hexBytes `json:"value"`
			Out   hexBytes `json:"out"`
		} `json:"ref_hash"`
		ExpandWithLabel struct {
			Secret  hexBytes `json:"secret"`
			Label   string   `json:"label"`
			Context hexBytes `json:"context"`
			Length  int      `json:"length"`
			Out     hexBytes `json:"out"`
		} `json:"expand_with_label"`
		DeriveSecret struct {
			Secret hexBytes `json:"secret"`
			Label  string   `json:"label"`
			Out    hexBytes `json:"out"`
		} `json:"derive_secret"`
		DeriveTreeSecret struct {
			Secret     hexBytes `json:"secret"`
			Label      string   `json:"label"`
			Generation uint32   `json:"generation"`
			Length     int      `json:"length"`
			Out        hexBytes `json:"out"`
		} `json:"derive_tree_secret"`
		SignWithLabel struct {
			Priv      hexBytes `json:"priv"`
			Pub       hexBytes `json:"pub"`
			Content   hexBytes `json:"content"`
			Label     string   `json:"label"`
			Signature hexBytes `json:"signature"`
		} `json:"sign_with_label"`
		EncryptWithLabel struct {
			Priv       hexBytes `json:"priv"`
			Pub        hexBytes `json:"pub"`
			Label      string   `json:"label"`
			Context    hexBytes `json:"context"`
			Plaintext  hexBytes `json:"plaintext"`
			KEMOutput  hexBytes `json:"kem_output"`
			Ciphertext hexBytes `json:"ciphertext"`
		} `json:"encrypt_with_label"`
	}
	loadVectors(t, "crypto-basics", &vs)
	if len(vs) != 4 {
		t.Fatalf("%d suites in the vectors, want 4", len(vs))
	}
	for _, v := range vs {
		s := mustSuite(t, v.CipherSuite)
		checkBytes(t, "RefHash", s.refHash(v.RefHash.Label, v.RefHash.Value), v.RefHash.Out)
		e := v.ExpandWithLabel
		checkBytes(t, "ExpandWithLabel", s.expandWithLabel(e.Secret, e.Label, e.Context, e.Length), e.Out)
		checkBytes(t, "DeriveSecret", s.deriveSecret(v.DeriveSecret.Secret, v.DeriveSecret.Label), v.DeriveSecret.Out)
		d := v.DeriveTreeSecret
		checkBytes(t, "DeriveTreeSecret", s.deriveTreeSecret(d.Secret, d.Label, d.Generation, d.Length), d.Out)

		sg := v.SignWithLabel
		if err := s.verifyWithLabel(sg.Pub, sg.Label, sg.Content, sg.Signature); err != nil {
			t.Errorf("suite %d: VerifyWithLabel: %v", v.CipherSuite, err)
		}
		key, err := NewSignatureKey(CipherSuite(v.CipherSuite), sg.Priv)
		if err != nil {
			t.Fatal(err)
		}
		checkBytes(t, "signature public key", key.PublicKey(), sg.Pub)
		sig, err := s.signWithLabel(key, sg.Label, sg.Content)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.verifyWithLabel(sg.Pub, sg.Label, sg.Content, sig); err != nil {
			t.Errorf("suite %d: own signature: %v", v.CipherSuite, err)
		}

		en := v.EncryptWithLabel
		pt, err := s.decryptWithLabel(en.Priv, en.Label, en.Context, en.KEMOutput, en.Ciphertext)
		if err != nil {
			t.Fatalf("suite %d: DecryptWithLabel: %v", v.CipherSuite, err)
		}
		checkBytes(t, "DecryptWithLabel", pt, en.Plaintext)
		kem, ct, err := s.encryptWithLabel(en.Pub, en.Label, en.Context, en.Plaintext)
		if err != nil {
			t.Fatal(err)
		}
		pt, err = s.decryptWithLabel(en.Priv, en.Label, en.Context, kem, ct)
		if err != nil {
			t.Fatal(err)
		}
		checkBytes(t, "EncryptWithLabel round trip", pt, en.Plaintext)
	}
}

func TestVectorsSecretTree(t *testing.T) {
	var vs []struct {
		CipherSuite uint16 `json:"cipher_suite"`
		SenderData  struct {
			Secret     hexBytes `json:"sender_data_secret"`
			Ciphertext hexBytes `json:"ciphertext"`
			Key        hexBytes `json:"key"`
			Nonce      hexBytes `json:"nonce"`
		} `json:"sender_data"`
		EncryptionSecret hexBytes `json:"encryption_secret"`
		Leaves           [][]struct {
			Generation       uint32   `json:"generation"`
			HandshakeKey     hexBytes `json:"handshake_key"`
			HandshakeNonce   hexBytes `json:"handshake_nonce"`
			ApplicationKey   hexBytes `json:"application_key"`
			ApplicationNonce hexBytes `json:"application_nonce"`
		} `json:"leaves"`
	}
	loadVectors(t, "secret-tree", &vs)
	for _, v := range vs {
		s := mustSuite(t, v.CipherSuite)
		key, nonce := s.senderDataKeyNonce(v.SenderData.Secret, v.SenderData.Ciphertext)
		checkBytes(t, "sender data key", key, v.SenderData.Key)
		checkBytes(t, "sender data nonce", nonce, v.SenderData.Nonce)
		st := newSecretTree(s, v.EncryptionSecret, uint32(len(v.Leaves)))
		for i, gens := range v.Leaves {
			for _, g := range gens {
				for _, typ := range []ratchetType{ratchetHandshake, ratchetApplication} {
					kn, advance, err := st.get(uint32(i), typ, g.Generation)
					if err != nil {
						t.Fatalf("leaf %d generation %d: %v", i, g.Generation, err)
					}
					advance()
					wantKey, wantNonce := g.HandshakeKey, g.HandshakeNonce
					if typ == ratchetApplication {
						wantKey, wantNonce = g.ApplicationKey, g.ApplicationNonce
					}
					checkBytes(t, "ratchet key", kn.key, wantKey)
					checkBytes(t, "ratchet nonce", kn.nonce, wantNonce)
				}
			}
		}
	}
}

func TestVectorsKeySchedule(t *testing.T) {
	var vs []struct {
		CipherSuite       uint16   `json:"cipher_suite"`
		GroupID           hexBytes `json:"group_id"`
		InitialInitSecret hexBytes `json:"initial_init_secret"`
		Epochs            []struct {
			TreeHash                hexBytes `json:"tree_hash"`
			CommitSecret            hexBytes `json:"commit_secret"`
			PSKSecret               hexBytes `json:"psk_secret"`
			ConfirmedTranscriptHash hexBytes `json:"confirmed_transcript_hash"`
			GroupContext            hexBytes `json:"group_context"`
			JoinerSecret            hexBytes `json:"joiner_secret"`
			WelcomeSecret           hexBytes `json:"welcome_secret"`
			InitSecret              hexBytes `json:"init_secret"`
			SenderDataSecret        hexBytes `json:"sender_data_secret"`
			EncryptionSecret        hexBytes `json:"encryption_secret"`
			ExporterSecret          hexBytes `json:"exporter_secret"`
			EpochAuthenticator      hexBytes `json:"epoch_authenticator"`
			ExternalSecret          hexBytes `json:"external_secret"`
			ConfirmationKey         hexBytes `json:"confirmation_key"`
			MembershipKey           hexBytes `json:"membership_key"`
			ResumptionPSK           hexBytes `json:"resumption_psk"`
			ExternalPub             hexBytes `json:"external_pub"`
			Exporter                struct {
				Label   string   `json:"label"`
				Context hexBytes `json:"context"`
				Length  int      `json:"length"`
				Secret  hexBytes `json:"secret"`
			} `json:"exporter"`
		} `json:"epochs"`
	}
	loadVectors(t, "key-schedule", &vs)
	for _, v := range vs {
		s := mustSuite(t, v.CipherSuite)
		init := v.InitialInitSecret
		for i, e := range v.Epochs {
			gc := groupContext{
				version: protocolVersionMLS10, suite: CipherSuite(v.CipherSuite), groupID: v.GroupID,
				epoch: uint64(i), treeHash: e.TreeHash, confirmedTranscriptHash: e.ConfirmedTranscriptHash,
			}
			gcb := gc.bytes()
			checkBytes(t, "group context", gcb, e.GroupContext)
			joiner := s.joinerSecret(init, e.CommitSecret, gcb)
			checkBytes(t, "joiner secret", joiner, e.JoinerSecret)
			welcome, es := s.epochFromJoiner(joiner, e.PSKSecret, gcb)
			checkBytes(t, "welcome secret", welcome, e.WelcomeSecret)
			checkBytes(t, "init secret", es.init, e.InitSecret)
			checkBytes(t, "sender data secret", es.senderData, e.SenderDataSecret)
			checkBytes(t, "encryption secret", es.encryption, e.EncryptionSecret)
			checkBytes(t, "exporter secret", es.exporter, e.ExporterSecret)
			checkBytes(t, "epoch authenticator", es.authenticator, e.EpochAuthenticator)
			checkBytes(t, "external secret", es.external, e.ExternalSecret)
			checkBytes(t, "confirmation key", es.confirmation, e.ConfirmationKey)
			checkBytes(t, "membership key", es.membership, e.MembershipKey)
			checkBytes(t, "resumption psk", es.resumption, e.ResumptionPSK)
			_, pub, err := s.deriveKeyPair(es.external)
			if err != nil {
				t.Fatal(err)
			}
			checkBytes(t, "external pub", pub, e.ExternalPub)
			exp, err := s.exporter(es.exporter, e.Exporter.Label, e.Exporter.Context, e.Exporter.Length)
			if err != nil {
				t.Fatal(err)
			}
			checkBytes(t, "exporter", exp, e.Exporter.Secret)
			init = es.init
		}
	}
}

func TestVectorsPSKSecret(t *testing.T) {
	var vs []struct {
		CipherSuite uint16 `json:"cipher_suite"`
		PSKs        []struct {
			ID    hexBytes `json:"psk_id"`
			PSK   hexBytes `json:"psk"`
			Nonce hexBytes `json:"psk_nonce"`
		} `json:"psks"`
		PSKSecret hexBytes `json:"psk_secret"`
	}
	loadVectors(t, "psk_secret", &vs)
	for _, v := range vs {
		s := mustSuite(t, v.CipherSuite)
		var in []pskInput
		for _, p := range v.PSKs {
			in = append(in, pskInput{id: preSharedKeyID{typ: pskExternal, id: p.ID, nonce: p.Nonce}, psk: p.PSK})
		}
		checkBytes(t, "psk secret", s.pskSecret(in), v.PSKSecret)
	}
}

func TestVectorsTranscriptHashes(t *testing.T) {
	var vs []struct {
		CipherSuite                  uint16   `json:"cipher_suite"`
		ConfirmationKey              hexBytes `json:"confirmation_key"`
		AuthenticatedContent         hexBytes `json:"authenticated_content"`
		InterimTranscriptHashBefore  hexBytes `json:"interim_transcript_hash_before"`
		ConfirmedTranscriptHashAfter hexBytes `json:"confirmed_transcript_hash_after"`
		InterimTranscriptHashAfter   hexBytes `json:"interim_transcript_hash_after"`
	}
	loadVectors(t, "transcript-hashes", &vs)
	for _, v := range vs {
		s := mustSuite(t, v.CipherSuite)
		var a authenticatedContent
		if err := decode(v.AuthenticatedContent, a.unmarshal); err != nil {
			t.Fatal(err)
		}
		if a.content.contentType != contentCommit {
			t.Fatal("authenticated content is not a commit")
		}
		confirmed := s.confirmedTranscriptHash(v.InterimTranscriptHashBefore, a.wireFormat, &a.content, a.auth.signature)
		checkBytes(t, "confirmed transcript hash", confirmed, v.ConfirmedTranscriptHashAfter)
		checkBytes(t, "confirmation tag", s.confirmationTag(v.ConfirmationKey, confirmed), a.auth.confirmationTag)
		checkBytes(t, "interim transcript hash", s.interimTranscriptHash(confirmed, a.auth.confirmationTag), v.InterimTranscriptHashAfter)
	}
}

func TestVectorsTreeValidation(t *testing.T) {
	var vs []struct {
		CipherSuite uint16     `json:"cipher_suite"`
		Tree        hexBytes   `json:"tree"`
		GroupID     hexBytes   `json:"group_id"`
		Resolutions [][]uint32 `json:"resolutions"`
		TreeHashes  []hexBytes `json:"tree_hashes"`
	}
	loadVectors(t, "tree-validation", &vs)
	for n, v := range vs {
		s := mustSuite(t, v.CipherSuite)
		tree, err := parseTree(v.Tree)
		if err != nil {
			t.Fatalf("vector %d: %v", n, err)
		}
		checkBytes(t, "tree encoding", tree.bytes(), v.Tree)
		if len(tree.nodes) != len(v.TreeHashes) {
			t.Fatalf("vector %d: %d nodes, want %d", n, len(tree.nodes), len(v.TreeHashes))
		}
		hashes := tree.treeHashes(s)
		for i := range tree.nodes {
			res := tree.resolution(uint32(i), nil)
			if len(res) != len(v.Resolutions[i]) || (len(res) > 0 && !equalUint32(res, v.Resolutions[i])) {
				t.Errorf("vector %d: resolution(%d) = %v, want %v", n, i, res, v.Resolutions[i])
			}
			checkBytes(t, "tree hash", hashes[i], v.TreeHashes[i])
		}
		if err := tree.verifyParentHashes(s); err != nil {
			t.Errorf("vector %d: %v", n, err)
		}
		if err := tree.verifyUnmergedLeaves(); err != nil {
			t.Errorf("vector %d: %v", n, err)
		}
		if err := tree.verifyUniqueKeys(); err != nil {
			t.Errorf("vector %d: %v", n, err)
		}
		for _, i := range tree.members() {
			if err := tree.leaf(i).verify(s, v.GroupID, i); err != nil {
				t.Errorf("vector %d: leaf %d: %v", n, i, err)
			}
		}
	}
}

func equalUint32(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// roundTrip decodes b with f and checks that the encoding m produces
// is b again.
func roundTrip(t *testing.T, what string, b []byte, unmarshal func(r *reader), marshal func(w *writer)) {
	t.Helper()
	if err := decode(b, unmarshal); err != nil {
		t.Errorf("%s: %v", what, err)
		return
	}
	checkBytes(t, what+" re-encoding", encode(marshal), b)
}

func TestVectorsMessages(t *testing.T) {
	var vs []map[string]hexBytes
	loadVectors(t, "messages", &vs)
	for _, v := range vs {
		for _, k := range []string{"mls_welcome", "mls_group_info", "mls_key_package", "public_message_application",
			"public_message_proposal", "public_message_commit", "private_message"} {
			var m mlsMessage
			roundTrip(t, k, v[k], m.unmarshal, m.marshal)
		}
		var tree *ratchetTree
		roundTrip(t, "ratchet_tree", v["ratchet_tree"], func(r *reader) { tree = unmarshalTree(r) }, func(w *writer) { tree.marshal(w) })
		var gs groupSecrets
		roundTrip(t, "group_secrets", v["group_secrets"], gs.unmarshal, gs.marshal)
		var c commit
		roundTrip(t, "commit", v["commit"], c.unmarshal, c.marshal)
		// The proposal vectors are the proposal bodies, without the type.
		bodies := map[string]proposalType{
			"add_proposal": proposalAdd, "update_proposal": proposalUpdate, "remove_proposal": proposalRemove,
			"pre_shared_key_proposal": proposalPSK, "re_init_proposal": proposalReInit,
			"external_init_proposal": proposalExternalInit, "group_context_extensions_proposal": proposalGroupContextExtensions,
		}
		for k, typ := range bodies {
			var p proposal
			b := append(encode(func(w *writer) { w.u16(uint16(typ)) }), v[k]...)
			roundTrip(t, k, b, p.unmarshal, p.marshal)
		}
	}
}

func TestVectorsWelcome(t *testing.T) {
	var vs []struct {
		CipherSuite uint16   `json:"cipher_suite"`
		InitPriv    hexBytes `json:"init_priv"`
		SignerPub   hexBytes `json:"signer_pub"`
		KeyPackage  hexBytes `json:"key_package"`
		Welcome     hexBytes `json:"welcome"`
	}
	loadVectors(t, "welcome", &vs)
	for _, v := range vs {
		s := mustSuite(t, v.CipherSuite)
		kp, err := ParseKeyPackage(v.KeyPackage)
		if err != nil {
			t.Fatal(err)
		}
		m, err := parseMessage(v.Welcome)
		if err != nil {
			t.Fatal(err)
		}
		w := m.welcome
		ref := kp.Ref()
		var egs *encryptedGroupSecrets
		for i := range w.secrets {
			if bytes.Equal(w.secrets[i].newMember, ref) {
				egs = &w.secrets[i]
			}
		}
		if egs == nil {
			t.Fatal("no secrets for the key package")
		}
		gsb, err := s.decryptWithLabel(v.InitPriv, "Welcome", w.encryptedGroupInfo, egs.secrets.kemOutput, egs.secrets.ciphertext)
		if err != nil {
			t.Fatal(err)
		}
		var gs groupSecrets
		if err := decode(gsb, gs.unmarshal); err != nil {
			t.Fatal(err)
		}
		member := s.extract(gs.joinerSecret, s.pskSecret(nil))
		key, nonce := s.welcomeKeyNonce(s.deriveSecret(member, "welcome"))
		gib, err := s.open(key, nonce, nil, w.encryptedGroupInfo)
		if err != nil {
			t.Fatal(err)
		}
		var gi groupInfo
		if err := decode(gib, gi.unmarshal); err != nil {
			t.Fatal(err)
		}
		if err := s.verifyWithLabel(v.SignerPub, "GroupInfoTBS", encode(gi.marshalTBS), gi.signature); err != nil {
			t.Fatal(err)
		}
		ctx := gi.context.bytes()
		_, es := s.epochFromJoiner(gs.joinerSecret, s.pskSecret(nil), ctx)
		checkBytes(t, "confirmation tag", s.confirmationTag(es.confirmation, gi.context.confirmedTranscriptHash), gi.confirmationTag)
	}
}

type passiveVector struct {
	CipherSuite  uint16 `json:"cipher_suite"`
	ExternalPSKs []struct {
		ID  hexBytes `json:"psk_id"`
		PSK hexBytes `json:"psk"`
	} `json:"external_psks"`
	KeyPackage                hexBytes `json:"key_package"`
	SignaturePriv             hexBytes `json:"signature_priv"`
	EncryptionPriv            hexBytes `json:"encryption_priv"`
	InitPriv                  hexBytes `json:"init_priv"`
	Welcome                   hexBytes `json:"welcome"`
	RatchetTree               hexBytes `json:"ratchet_tree"`
	InitialEpochAuthenticator hexBytes `json:"initial_epoch_authenticator"`
	Epochs                    []struct {
		Proposals          []hexBytes `json:"proposals"`
		Commit             hexBytes   `json:"commit"`
		EpochAuthenticator hexBytes   `json:"epoch_authenticator"`
	} `json:"epochs"`
}

func runPassive(t *testing.T, name string) {
	var vs []passiveVector
	loadVectors(t, name, &vs)
	for n, v := range vs {
		kp, err := ParseKeyPackage(v.KeyPackage)
		if err != nil {
			t.Fatalf("%s %d: %v", name, n, err)
		}
		signer, err := NewSignatureKey(CipherSuite(v.CipherSuite), v.SignaturePriv)
		if err != nil {
			t.Fatal(err)
		}
		psks := PSKs{}
		for _, p := range v.ExternalPSKs {
			psks[string(p.ID)] = p.PSK
		}
		lt := kp.Lifetime()
		cfg := Config{PSKs: psks, Now: func() time.Time { return time.Unix(int64(lt.NotBefore), 0) }}
		g, err := JoinGroup(v.Welcome, kp, &KeyPackageSecrets{initPriv: v.InitPriv, encryptionPriv: v.EncryptionPriv}, signer,
			JoinOptions{RatchetTree: v.RatchetTree}, cfg)
		if err != nil {
			t.Fatalf("%s %d: join: %v", name, n, err)
		}
		checkBytes(t, "initial epoch authenticator", g.EpochAuthenticator(), v.InitialEpochAuthenticator)
		for e, ep := range v.Epochs {
			for _, p := range ep.Proposals {
				if _, err := g.Process(p); err != nil {
					t.Fatalf("%s %d epoch %d: proposal: %v", name, n, e, err)
				}
			}
			m, err := g.Process(ep.Commit)
			if err != nil {
				t.Fatalf("%s %d epoch %d: commit: %v", name, n, e, err)
			}
			if m.Kind != MessageCommit {
				t.Fatalf("%s %d epoch %d: not a commit", name, n, e)
			}
			if err := g.Merge(m.Commit); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(g.EpochAuthenticator(), ep.EpochAuthenticator) {
				t.Fatalf("%s %d epoch %d: epoch authenticator differs", name, n, e)
			}
		}
	}
}

func TestVectorsPassiveClientWelcome(t *testing.T) { runPassive(t, "passive-client-welcome") }
func TestVectorsPassiveClientHandlingCommit(t *testing.T) {
	runPassive(t, "passive-client-handling-commit")
}
func TestVectorsPassiveClientRandom(t *testing.T) { runPassive(t, "passive-client-random") }

func TestVectorsTreeOperations(t *testing.T) {
	var vs []struct {
		CipherSuite    uint16   `json:"cipher_suite"`
		TreeBefore     hexBytes `json:"tree_before"`
		Proposal       hexBytes `json:"proposal"`
		ProposalSender uint32   `json:"proposal_sender"`
		TreeHashBefore hexBytes `json:"tree_hash_before"`
		TreeAfter      hexBytes `json:"tree_after"`
		TreeHashAfter  hexBytes `json:"tree_hash_after"`
	}
	loadVectors(t, "tree-operations", &vs)
	for n, v := range vs {
		s := mustSuite(t, v.CipherSuite)
		tree, err := parseTree(v.TreeBefore)
		if err != nil {
			t.Fatal(err)
		}
		checkBytes(t, "tree hash before", tree.treeHash(s), v.TreeHashBefore)
		var p proposal
		if err := decode(v.Proposal, p.unmarshal); err != nil {
			t.Fatal(err)
		}
		switch p.typ {
		case proposalAdd:
			tree.addLeaf(&p.add.leaf)
		case proposalRemove:
			tree.removeLeaf(p.remove)
		case proposalUpdate:
			tree.nodes[leafNodeIndex(v.ProposalSender)] = node{leaf: p.update}
			tree.blankPath(v.ProposalSender)
		default:
			t.Fatalf("vector %d: proposal type %d", n, p.typ)
		}
		checkBytes(t, "tree after", tree.bytes(), v.TreeAfter)
		checkBytes(t, "tree hash after", tree.treeHash(s), v.TreeHashAfter)
	}
}

func TestVectorsTreeKEM(t *testing.T) {
	var vs []struct {
		CipherSuite             uint16   `json:"cipher_suite"`
		GroupID                 hexBytes `json:"group_id"`
		Epoch                   uint64   `json:"epoch"`
		ConfirmedTranscriptHash hexBytes `json:"confirmed_transcript_hash"`
		RatchetTree             hexBytes `json:"ratchet_tree"`
		LeavesPrivate           []struct {
			Index          uint32   `json:"index"`
			EncryptionPriv hexBytes `json:"encryption_priv"`
			SignaturePriv  hexBytes `json:"signature_priv"`
			PathSecrets    []struct {
				Node       uint32   `json:"node"`
				PathSecret hexBytes `json:"path_secret"`
			} `json:"path_secrets"`
		} `json:"leaves_private"`
		UpdatePaths []struct {
			Sender        uint32     `json:"sender"`
			UpdatePath    hexBytes   `json:"update_path"`
			PathSecrets   []hexBytes `json:"path_secrets"`
			CommitSecret  hexBytes   `json:"commit_secret"`
			TreeHashAfter hexBytes   `json:"tree_hash_after"`
		} `json:"update_paths"`
	}
	loadVectors(t, "treekem", &vs)
	for n, v := range vs {
		s := mustSuite(t, v.CipherSuite)
		tree, err := parseTree(v.RatchetTree)
		if err != nil {
			t.Fatal(err)
		}
		privs := make(map[uint32]*privateTree)
		signers := make(map[uint32]*SignatureKey)
		for _, lp := range v.LeavesPrivate {
			pt := &privateTree{leaf: lp.Index, keys: make(map[uint32]hpkeKey)}
			pub, err := s.hpkePublic(lp.EncryptionPriv)
			if err != nil {
				t.Fatal(err)
			}
			checkBytes(t, "leaf key", pub, tree.leaf(lp.Index).encryptionKey)
			pt.keys[leafNodeIndex(lp.Index)] = hpkeKey{priv: lp.EncryptionPriv, pub: pub}
			for _, ps := range lp.PathSecrets {
				k, err := s.nodeKey(ps.PathSecret)
				if err != nil {
					t.Fatal(err)
				}
				checkBytes(t, "path key", k.pub, tree.nodes[ps.Node].encryptionKey())
				pt.keys[ps.Node] = k
			}
			privs[lp.Index] = pt
			if signers[lp.Index], err = NewSignatureKey(CipherSuite(v.CipherSuite), lp.SignaturePriv); err != nil {
				t.Fatal(err)
			}
		}
		context := func(treeHash []byte) []byte {
			gc := groupContext{version: protocolVersionMLS10, suite: s.id, groupID: v.GroupID, epoch: v.Epoch,
				treeHash: treeHash, confirmedTranscriptHash: v.ConfirmedTranscriptHash}
			return gc.bytes()
		}
		for u, up := range v.UpdatePaths {
			var path updatePath
			if err := decode(up.UpdatePath, path.unmarshal); err != nil {
				t.Fatal(err)
			}
			merged := tree.clone()
			fdp, err := merged.mergeUpdatePath(s, up.Sender, &path)
			if err != nil {
				t.Fatalf("vector %d path %d: %v", n, u, err)
			}
			th := merged.treeHash(s)
			checkBytes(t, "tree hash after", th, up.TreeHashAfter)
			gc := context(th)
			for j, pt := range privs {
				if j == up.Sender || tree.leaf(j) == nil {
					continue
				}
				secret, i, err := merged.decryptPathSecret(s, up.Sender, &path, fdp, pt, nil, gc)
				if err != nil {
					t.Fatalf("vector %d path %d leaf %d: %v", n, u, j, err)
				}
				checkBytes(t, "path secret", secret, up.PathSecrets[j])
				_, cs, err := derivePathKeys(s, secret, fdp[i:], path.nodes[i:])
				if err != nil {
					t.Fatal(err)
				}
				checkBytes(t, "commit secret", cs, up.CommitSecret)
			}

			// A path this package makes opens for every other member.
			fresh := tree.clone()
			own := tree.leaf(up.Sender)
			tmpl := leafTemplate{signatureKey: own.signatureKey, credential: own.credential, capabilities: own.capabilities, extensions: own.extensions}
			newPath, ps, gc2, err := fresh.createUpdatePath(s, up.Sender, tmpl, signers[up.Sender], v.GroupID, nil, context)
			if err != nil {
				t.Fatal(err)
			}
			check := tree.clone()
			fdp2, err := check.mergeUpdatePath(s, up.Sender, newPath)
			if err != nil {
				t.Fatalf("vector %d path %d: own path: %v", n, u, err)
			}
			checkBytes(t, "own path tree hash", check.treeHash(s), fresh.treeHash(s))
			for j, pt := range privs {
				if j == up.Sender || tree.leaf(j) == nil {
					continue
				}
				_, cs, err := check.openUpdatePath(s, up.Sender, newPath, fdp2, pt, nil, gc2)
				if err != nil {
					t.Fatalf("vector %d path %d leaf %d: own path: %v", n, u, j, err)
				}
				checkBytes(t, "own commit secret", cs, ps.commitSecret)
			}
		}
	}
}

func TestVectorsMessageProtection(t *testing.T) {
	var vs []struct {
		CipherSuite             uint16   `json:"cipher_suite"`
		GroupID                 hexBytes `json:"group_id"`
		Epoch                   uint64   `json:"epoch"`
		TreeHash                hexBytes `json:"tree_hash"`
		ConfirmedTranscriptHash hexBytes `json:"confirmed_transcript_hash"`
		SignaturePriv           hexBytes `json:"signature_priv"`
		SignaturePub            hexBytes `json:"signature_pub"`
		EncryptionSecret        hexBytes `json:"encryption_secret"`
		SenderDataSecret        hexBytes `json:"sender_data_secret"`
		MembershipKey           hexBytes `json:"membership_key"`
		Proposal                hexBytes `json:"proposal"`
		ProposalPub             hexBytes `json:"proposal_pub"`
		ProposalPriv            hexBytes `json:"proposal_priv"`
		Commit                  hexBytes `json:"commit"`
		CommitPub               hexBytes `json:"commit_pub"`
		CommitPriv              hexBytes `json:"commit_priv"`
		Application             hexBytes `json:"application"`
		ApplicationPriv         hexBytes `json:"application_priv"`
	}
	loadVectors(t, "message-protection", &vs)
	for _, v := range vs {
		s := mustSuite(t, v.CipherSuite)
		gc := (&groupContext{version: protocolVersionMLS10, suite: s.id, groupID: v.GroupID, epoch: v.Epoch,
			treeHash: v.TreeHash, confirmedTranscriptHash: v.ConfirmedTranscriptHash}).bytes()
		signer, err := NewSignatureKey(s.id, v.SignaturePriv)
		if err != nil {
			t.Fatal(err)
		}
		anyLeaf := func(uint32) error { return nil }

		unprotectPublic := func(b []byte) *authenticatedContent {
			t.Helper()
			m, err := parseMessage(b)
			if err != nil || m.wireFormat != WireFormatPublicMessage {
				t.Fatalf("public message: %v", err)
			}
			a := &authenticatedContent{wireFormat: WireFormatPublicMessage, content: m.public.content, auth: m.public.auth}
			if !equal(s.membershipTag(v.MembershipKey, a, gc), m.public.membershipTag) {
				t.Fatal("membership tag does not verify")
			}
			if err := s.verifyContent(v.SignaturePub, a, gc); err != nil {
				t.Fatal(err)
			}
			return a
		}
		unprotectPrivate := func(st *secretTree, b []byte) *authenticatedContent {
			t.Helper()
			m, err := parseMessage(b)
			if err != nil || m.wireFormat != WireFormatPrivateMessage {
				t.Fatalf("private message: %v", err)
			}
			a, advance, err := s.decryptContent(st, v.SenderDataSecret, m.private, anyLeaf)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.verifyContent(v.SignaturePub, a, gc); err != nil {
				t.Fatal(err)
			}
			advance()
			return a
		}
		protect := func(ct contentType, body []byte, wf WireFormat) []byte {
			t.Helper()
			a := &authenticatedContent{wireFormat: wf, content: framedContent{
				groupID: v.GroupID, epoch: v.Epoch, sender: sender{typ: senderMember, index: 1}, contentType: ct,
			}}
			if err := decode(body, a.content.unmarshalBody); err != nil {
				t.Fatal(err)
			}
			if err := s.signContent(signer, a, gc); err != nil {
				t.Fatal(err)
			}
			if ct == contentCommit {
				a.auth.confirmationTag = make([]byte, s.nh)
			}
			if wf == WireFormatPublicMessage {
				pm := &publicMessage{content: a.content, auth: a.auth, membershipTag: s.membershipTag(v.MembershipKey, a, gc)}
				return encode((&mlsMessage{wireFormat: wf, public: pm}).marshal)
			}
			pm, err := s.encryptContent(newSecretTree(s, v.EncryptionSecret, 2), v.SenderDataSecret, a, 0)
			if err != nil {
				t.Fatal(err)
			}
			return encode((&mlsMessage{wireFormat: wf, private: pm}).marshal)
		}
		body := func(a *authenticatedContent) []byte { return encode(a.content.marshalBody) }

		for _, c := range []struct {
			ct        contentType
			raw       []byte
			pub, priv []byte
		}{
			{contentProposal, v.Proposal, v.ProposalPub, v.ProposalPriv},
			{contentCommit, v.Commit, v.CommitPub, v.CommitPriv},
			{contentApplication, encode(func(w *writer) { w.vec(v.Application) }), nil, v.ApplicationPriv},
		} {
			if c.pub != nil {
				checkBytes(t, "public body", body(unprotectPublic(c.pub)), c.raw)
				checkBytes(t, "own public body", body(unprotectPublic(protect(c.ct, c.raw, WireFormatPublicMessage))), c.raw)
			}
			checkBytes(t, "private body", body(unprotectPrivate(newSecretTree(s, v.EncryptionSecret, 2), c.priv)), c.raw)
			checkBytes(t, "own private body", body(unprotectPrivate(newSecretTree(s, v.EncryptionSecret, 2), protect(c.ct, c.raw, WireFormatPrivateMessage))), c.raw)
		}
	}
}
