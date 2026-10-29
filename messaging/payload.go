// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package messaging

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// What a device puts in an MLS application message is a Payload: what
// was said, with the message's id and time, padded with zeros to one of
// the PaddedSizes so a message's length says little of what it holds.
// The payload is inside the sender's signature, so its id and time are
// the sender's word wherever the message is shared.

// MaxPayloadBytes is the most bytes an encoded payload takes, padding
// included, and the largest of PaddedSizes.
const MaxPayloadBytes = 256 << 10

// PaddedSizes are the sizes an encoded payload is padded to, smallest
// first.
var PaddedSizes = [...]int{256, 1 << 10, 4 << 10, 16 << 10, 64 << 10, MaxPayloadBytes}

// PayloadType is what a payload says.
type PayloadType string

// The types of payload.
const (
	// PayloadText is a message someone wrote. A Latere answer posted to
	// the group is one too, with AskedBy and Ref set.
	PayloadText PayloadType = "text"
	// PayloadName names the conversation; Text is the new name. The name
	// is a message, not part of the group's state, so the service never
	// reads it.
	PayloadName PayloadType = "name"
	// PayloadEdit replaces the text of the sender's message Ref.
	PayloadEdit PayloadType = "edit"
	// PayloadDelete withdraws the sender's message Ref.
	PayloadDelete PayloadType = "delete"
	// PayloadReaction reacts to message Ref with Reaction; an empty
	// Reaction takes the sender's reaction back.
	PayloadReaction PayloadType = "reaction"
)

// Payload is the content of one application message.
type Payload struct {
	// ID is the message's id, made by the sender, unique in the
	// conversation.
	ID string `json:"id"`
	// SentAt is when the sender sent it, by the sender's clock.
	SentAt time.Time   `json:"sent_at"`
	Type   PayloadType `json:"type"`
	// Text is what was written, or the conversation's new name.
	Text string `json:"text,omitempty"`
	// Ref is the id of the message an edit, deletion, reaction or answer
	// is about.
	Ref string `json:"ref,omitempty"`
	// Reaction is an emoji.
	Reaction string `json:"reaction,omitempty"`
	// AskedBy is, on a Latere answer, the person who asked.
	AskedBy string `json:"asked_by,omitempty"`
	// Attachments are the files a text carries, at most MaxAttachments.
	Attachments []Attachment `json:"attachments,omitempty"`
}

// payloadVersion is the first byte of an encoded payload.
const payloadVersion = 1

// EncodePayload encodes a payload and pads it to the smallest of
// PaddedSizes it fits.
func EncodePayload(p Payload) ([]byte, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	body, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	n := 1 + 4 + len(body)
	for _, size := range PaddedSizes {
		if n <= size {
			out := make([]byte, size)
			out[0] = payloadVersion
			binary.BigEndian.PutUint32(out[1:], uint32(len(body)))
			copy(out[5:], body)
			return out, nil
		}
	}
	return nil, fmt.Errorf("messaging: a payload of %d bytes is over %d", n, MaxPayloadBytes)
}

// ParsePayload decodes an encoded payload. It refuses one whose length
// is not one of PaddedSizes, whose padding is not zeros, or whose JSON
// is not exactly what EncodePayload writes for it, so no two decoders
// read one payload two ways.
func ParsePayload(b []byte) (Payload, error) {
	sized := false
	for _, size := range PaddedSizes {
		sized = sized || len(b) == size
	}
	if !sized || b[0] != payloadVersion {
		return Payload{}, errFormat
	}
	n := binary.BigEndian.Uint32(b[1:5])
	if uint64(n) > uint64(len(b)-5) {
		return Payload{}, errFormat
	}
	for _, c := range b[5+n:] {
		if c != 0 {
			return Payload{}, fmt.Errorf("%w: padding is not zeros", errFormat)
		}
	}
	var p Payload
	if err := json.Unmarshal(b[5:5+n], &p); err != nil {
		return Payload{}, fmt.Errorf("%w: %v", errFormat, err)
	}
	if err := p.validate(); err != nil {
		return Payload{}, err
	}
	canon, err := json.Marshal(p)
	if err != nil {
		return Payload{}, err
	}
	if !bytes.Equal(canon, b[5:5+n]) {
		return Payload{}, fmt.Errorf("%w: the payload is not in its one encoding", errFormat)
	}
	return p, nil
}

func (p *Payload) validate() error {
	if err := validID("message", p.ID); err != nil {
		return err
	}
	if p.SentAt.IsZero() {
		return errors.New("messaging: a payload has the time it was sent")
	}
	switch p.Type {
	case PayloadText, PayloadName:
	case PayloadEdit, PayloadDelete, PayloadReaction:
		if p.Ref == "" {
			return fmt.Errorf("messaging: a %s names the message it is about", p.Type)
		}
	default:
		return fmt.Errorf("messaging: unknown payload type %q", p.Type)
	}
	if p.Ref != "" {
		if err := validID("message", p.Ref); err != nil {
			return err
		}
	}
	if p.AskedBy != "" {
		if err := validID("person", p.AskedBy); err != nil {
			return err
		}
	}
	if len(p.Attachments) > 0 && p.Type != PayloadText {
		return fmt.Errorf("messaging: a %s carries no file", p.Type)
	}
	if len(p.Attachments) > MaxAttachments {
		return fmt.Errorf("messaging: a message carries at most %d files", MaxAttachments)
	}
	for i := range p.Attachments {
		a := &p.Attachments[i]
		if err := a.validate(); err != nil {
			return err
		}
		if err := validPlace(a.Owner, a.Ref); err != nil {
			return err
		}
	}
	return nil
}
