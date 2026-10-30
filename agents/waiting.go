// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents

import (
	"cmp"
	"encoding/json"
	"slices"
)

// MessagesRead is the payload of an EventMessagesRead event: the waiting
// messages the request after it is the first to read, in log order.
type MessagesRead struct {
	// EventIDs are the messages' event ids.
	EventIDs []string `json:"event_ids"`
}

// UserWithdraw is the payload of an EventUserWithdraw event: its sender
// took back a waiting message before the agent read it.
type UserWithdraw struct {
	// Sender is who withdrew the message, the one who sent it.
	Sender Sender `json:"sender"`
	// EventID is the withdrawn message's event id.
	EventID string `json:"event_id"`
}

// DetailSendNow is the SessionStatus.Detail of a turn that stopped with
// StopInterrupted because a message was sent with
// SendMessageParams.SendNow. A client shows such a stop as the person's
// own choice, not as an error.
const DetailSendNow = "send_now"

// MessageState is where a waiting message stands.
type MessageState string

// The states of a waiting message.
const (
	// MessageWaiting is a message the agent has not read yet, which its
	// sender may withdraw with Client.Withdraw.
	MessageWaiting MessageState = "waiting"
	// MessageRead is a message a request to the model read.
	MessageRead MessageState = "read"
	// MessageWithdrawn is a message its sender took back before the agent
	// read it, which the agent never reads.
	MessageWithdrawn MessageState = "withdrawn"
)

// A WaitingMessage is a message of the session's own conversation sent
// while one of its turns ran. A message sent while the session was idle
// started its turn, and is not one.
type WaitingMessage struct {
	// Event is the message's event, and Message its payload.
	Event   Event
	Message UserMessage
	// State is where the message stands.
	State MessageState
	// ReadSeq is the sequence of the EventMessagesRead that read the
	// message, where the agent read it, which a client that shows the
	// conversation in the order the agent read it places the message at;
	// zero for a message not read, and for one a request recorded before
	// the API wrote that event read where it stands.
	ReadSeq int64
	// SentNowStop is set on a message sent with SendNow whose turn
	// stopped with DetailSendNow: the next turn read it.
	SentNowStop bool
}

// WaitingMessages lists the waiting messages of a session's log in log
// order, each with its state. Pass the whole log, or every event from
// the start of the earliest turn to show: a message is waiting once it
// follows a SessionStatus of StatusRunning and comes before the status
// that ends that turn. It is read once an EventMessagesRead names it
// before any EventUserWithdraw of it, or once a request of the session's
// own conversation recorded before the API wrote EventMessagesRead built
// its fold past it; it is withdrawn once an EventUserWithdraw names it
// first.
func WaitingMessages(events []Event) []WaitingMessage {
	evs := slices.Clone(events)
	slices.SortStableFunc(evs, func(a, b Event) int { return cmp.Compare(a.Seq, b.Seq) })

	var out []WaitingMessage
	at := map[string]int{}
	open := false
	var opened []int
	for _, e := range evs {
		if redacted(e) {
			continue
		}
		switch e.Type {
		case EventSessionStatus:
			var p SessionStatus
			if e.Decode(&p) != nil {
				continue
			}
			// A status while a turn is open ends that turn, a running one
			// too: it is a runner taking over a turn nobody ended.
			if open {
				cut := p.Status == StatusIdle && p.StopReason == StopInterrupted && p.Detail == DetailSendNow
				for _, i := range opened {
					out[i].SentNowStop = cut && out[i].Message.SendNow
				}
			}
			opened = opened[:0]
			open = p.Status == StatusRunning
		case EventUserMessage:
			if !open || e.Subagent != "" {
				continue
			}
			w := WaitingMessage{Event: e, State: MessageWaiting}
			if e.Decode(&w.Message) != nil {
				continue
			}
			at[e.ID] = len(out)
			opened = append(opened, len(out))
			out = append(out, w)
		case EventUserWithdraw:
			var p UserWithdraw
			if e.Decode(&p) != nil {
				continue
			}
			if i, ok := at[p.EventID]; ok && out[i].State == MessageWaiting {
				out[i].State = MessageWithdrawn
			}
		case EventMessagesRead:
			var p MessagesRead
			if e.Decode(&p) != nil {
				continue
			}
			for _, id := range p.EventIDs {
				if i, ok := at[id]; ok && out[i].State == MessageWaiting {
					out[i].State, out[i].ReadSeq = MessageRead, e.Seq
				}
			}
		case EventModelRequest:
			var p ModelRequest
			if e.Subagent != "" || e.Decode(&p) != nil || p.NamesReads || p.Purpose != "" {
				continue
			}
			for i := range out {
				if out[i].State == MessageWaiting && out[i].Event.Seq <= p.FoldSeq {
					out[i].State = MessageRead
				}
			}
		}
	}
	return out
}

// redacted reports whether an event's payload is a tombstone, whose
// fields are gone.
func redacted(e Event) bool {
	var p struct {
		Tombstone bool `json:"tombstone"`
	}
	return json.Unmarshal(e.Payload, &p) == nil && p.Tombstone
}
