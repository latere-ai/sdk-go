// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents

import (
	"encoding/json"
	"fmt"
	"time"
)

// EventType names the kind of an event in a session's log.
type EventType string

// The event types a client reads most. The log holds others too; an event
// of a type not listed here is still returned, with its payload unread.
const (
	// EventUserMessage is a message sent to the session.
	EventUserMessage EventType = "user.message"
	// EventUserInterrupt stopped the running turn.
	EventUserInterrupt EventType = "user.interrupt"
	// EventUserToolConfirmation approved or denied a tool call.
	EventUserToolConfirmation EventType = "user.tool_confirmation"
	// EventUserAnswer answered a question the agent asked.
	EventUserAnswer EventType = "user.answer"
	// EventAgentMessage is a response the agent wrote.
	EventAgentMessage EventType = "agent.message"
	// EventAgentToolUse is a tool call the agent made; a question is a call
	// of the tool named "question".
	EventAgentToolUse EventType = "agent.tool_use"
	// EventToolResult is the result of a tool call.
	EventToolResult EventType = "tool.result"
	// EventSessionStatus is a change of the session's status; its payload
	// is a SessionStatus.
	EventSessionStatus EventType = "session.status"
	// EventSessionError is an error the session ran into.
	EventSessionError EventType = "session.error"
	// EventTitleChanged is a new title for the session.
	EventTitleChanged EventType = "session.title_changed"
	// EventApprovalRequested asks for an approval that is not a tool call.
	EventApprovalRequested EventType = "approval.requested"
	// EventApprovalDecided records the answer to an approval.
	EventApprovalDecided EventType = "approval.decided"
	// EventMachineWaiting is a session waiting for the computer it works on.
	EventMachineWaiting EventType = "session.machine_waiting"
)

// An Event is one entry of a session's log.
type Event struct {
	// ID identifies the event, such as "evt_01M34G2JF1P53NNG7DN08D5QAG".
	ID string `json:"id"`
	// Seq is the event's place in the log, from 1.
	Seq int64 `json:"seq"`
	// SessionID is the session whose log holds the event.
	SessionID string `json:"session_id"`
	// Type is the event's kind.
	Type EventType `json:"type"`
	// Time is when the event was appended.
	Time time.Time `json:"time"`
	// Turn is the turn the event belongs to; zero before the first turn.
	Turn int `json:"turn,omitempty"`
	// Step is the step of the turn the event belongs to, when it has one.
	Step int `json:"step,omitempty"`
	// Thread names the thread of a helper agent the event belongs to; empty
	// for the session's own thread.
	Thread string `json:"thread,omitempty"`
	// Payload is the event's content, whose shape depends on Type; read it
	// with Decode.
	Payload json.RawMessage `json:"payload"`
}

// Decode unmarshals the event's payload into v, such as a *SessionStatus
// for an event of type EventSessionStatus.
func (e *Event) Decode(v any) error {
	if err := json.Unmarshal(e.Payload, v); err != nil {
		return fmt.Errorf("agents: decoding the %s payload of %s: %w", e.Type, e.ID, err)
	}
	return nil
}

// SessionStatus is the payload of an EventSessionStatus event.
type SessionStatus struct {
	// Status is the session's new state.
	Status Status `json:"status"`
	// StopReason says why it stopped, when it is idle or ended.
	StopReason StopReason `json:"stop_reason,omitempty"`
	// Detail explains the stop, when there is more to say.
	Detail string `json:"detail,omitempty"`
}

// A ContentBlock is one part of a message.
type ContentBlock struct {
	// Type is the block's kind, such as "text".
	Type string `json:"type"`
	// Text is the text of a "text" block.
	Text string `json:"text,omitempty"`
}

// Text returns a text block holding s.
func Text(s string) ContentBlock { return ContentBlock{Type: "text", Text: s} }

// A UserMessage is the payload of an EventUserMessage event.
type UserMessage struct {
	// Sender is who sent the message.
	Sender Sender `json:"sender"`
	// Content is the message, in order.
	Content []ContentBlock `json:"content"`
}

// Question is a question an agent asked, the input of an EventAgentToolUse
// event whose tool is "question".
type Question struct {
	// Question is the full question.
	Question string `json:"question"`
	// Header is a short label for the question.
	Header string `json:"header"`
	// Options are the choices, in the order they are shown.
	Options []QuestionOption `json:"options"`
	// Multiple lets the person choose several options.
	Multiple bool `json:"multiple,omitempty"`
}

// QuestionOption is one choice of a Question.
type QuestionOption struct {
	// Label names the option; an answer names it by its label.
	Label string `json:"label"`
	// Description says what choosing the option means.
	Description string `json:"description"`
	// Recommended marks the option the agent would take.
	Recommended bool `json:"recommended,omitempty"`
}

// ToolUse is the payload of an EventAgentToolUse event.
type ToolUse struct {
	// ToolUseID identifies the call; an answer or a confirmation names it.
	ToolUseID string `json:"tool_use_id"`
	// Name is the tool's name, such as "question" or "bash".
	Name string `json:"name"`
	// Input is the call's input, whose shape depends on the tool; read the
	// questions of a "question" call with Questions.
	Input json.RawMessage `json:"input"`
}

// Questions returns the questions of a call of the "question" tool, and an
// error for a call of any other tool or an input that does not decode.
func (t *ToolUse) Questions() ([]Question, error) {
	if t.Name != "question" {
		return nil, fmt.Errorf("agents: the tool call %s is %q, not a question", t.ToolUseID, t.Name)
	}
	var in struct {
		Questions []Question `json:"questions"`
	}
	if err := json.Unmarshal(t.Input, &in); err != nil {
		return nil, fmt.Errorf("agents: decoding the questions of %s: %w", t.ToolUseID, err)
	}
	return in.Questions, nil
}
