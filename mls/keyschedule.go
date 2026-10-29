// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package mls

import (
	"bytes"
	"errors"
	"fmt"
)

// epochSecrets are the secrets an epoch's key schedule derives, RFC
// 9420, Section 8.
type epochSecrets struct {
	senderData []byte
	// encryption seeds the secret tree and is deleted once it has.
	encryption    []byte
	exporter      []byte
	external      []byte
	confirmation  []byte
	membership    []byte
	resumption    []byte
	authenticator []byte
	init          []byte
}

// joinerSecret derives the joiner secret from the previous epoch's init
// secret, the commit secret and the new group context.
func (s *suite) joinerSecret(initSecret, commitSecret, groupContext []byte) []byte {
	return s.expandWithLabel(s.extract(initSecret, commitSecret), "joiner", groupContext, s.nh)
}

// epochFromJoiner derives the welcome secret and the epoch's secrets from
// a joiner secret, the PSK secret and the group context.
func (s *suite) epochFromJoiner(joiner, pskSecret, groupContext []byte) (welcomeSecret []byte, es epochSecrets) {
	member := s.extract(joiner, pskSecret)
	welcomeSecret = s.deriveSecret(member, "welcome")
	epoch := s.expandWithLabel(member, "epoch", groupContext, s.nh)
	es = s.epochSecrets(epoch)
	return welcomeSecret, es
}

// welcomeKeyNonce derives the key and nonce that encrypt a Welcome's
// group info.
func (s *suite) welcomeKeyNonce(welcomeSecret []byte) (key, nonce []byte) {
	return s.expandWithLabel(welcomeSecret, "key", nil, s.nk),
		s.expandWithLabel(welcomeSecret, "nonce", nil, s.nn)
}

// zero returns the all-zero secret of the suite's hash size.
func (s *suite) zero() []byte { return make([]byte, s.nh) }

// pskInput is one PSK and its ID, for the PSK secret.
type pskInput struct {
	id  preSharedKeyID
	psk []byte
}

// pskSecret chains the PSKs into the key schedule, RFC 9420, Section
// 8.4. With no PSKs it is the all-zero secret.
func (s *suite) pskSecret(psks []pskInput) []byte {
	secret := s.zero()
	for i, p := range psks {
		label := encode(func(w *writer) {
			p.id.marshal(w)
			w.u16(uint16(i))
			w.u16(uint16(len(psks)))
		})
		input := s.expandWithLabel(s.extract(s.zero(), p.psk), "derived psk", label, s.nh)
		secret = s.extract(input, secret)
	}
	return secret
}

// MaxExportBytes is the largest secret Export derives: 255 hash outputs
// of the smallest hash a supported suite uses, the bound of HKDF.
const MaxExportBytes = 255 * 32

// exporter is MLS-Exporter, RFC 9420, Section 8.5.
func (s *suite) exporter(exporterSecret []byte, label string, context []byte, length int) ([]byte, error) {
	if length < 0 || length > MaxExportBytes {
		return nil, fmt.Errorf("mls: export of %d bytes, the most is %d", length, MaxExportBytes)
	}
	return s.expandWithLabel(s.deriveSecret(exporterSecret, label), "exported", s.hash(context), length), nil
}

// confirmationTag is the MAC that confirms an epoch's transcript.
func (s *suite) confirmationTag(confirmationKey, confirmedTranscriptHash []byte) []byte {
	return s.mac(confirmationKey, confirmedTranscriptHash)
}

// confirmedTranscriptHash advances the confirmed transcript hash over a
// commit, RFC 9420, Section 8.2.
func (s *suite) confirmedTranscriptHash(interim []byte, wf WireFormat, content *framedContent, signature []byte) []byte {
	return s.hash(encode(func(w *writer) {
		w.raw(interim)
		w.u16(uint16(wf))
		content.marshal(w)
		w.vec(signature)
	}))
}

func (s *suite) interimTranscriptHash(confirmed, confirmationTag []byte) []byte {
	return s.hash(encode(func(w *writer) {
		w.raw(confirmed)
		w.vec(confirmationTag)
	}))
}

// errPSK is returned when a commit or a Welcome names a PSK the member
// does not have.
var errPSK = errors.New("mls: unknown pre-shared key")

// PSKStore supplies the external pre-shared keys a group's commits and
// Welcomes may name, looked up by their ID.
type PSKStore interface {
	ExternalPSK(id []byte) ([]byte, bool)
}

// PSKs is a PSKStore over a map from a PSK's ID to its value.
type PSKs map[string][]byte

// ExternalPSK returns the PSK with the ID.
func (p PSKs) ExternalPSK(id []byte) ([]byte, bool) {
	v, ok := p[string(id)]
	return bytes.Clone(v), ok
}
