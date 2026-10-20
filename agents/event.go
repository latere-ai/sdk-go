// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents

import (
	"encoding/json"
	"fmt"
	"strings"
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
	// EventUserToolResult is the result of a tool call your program ran;
	// its payload is a ToolResult.
	EventUserToolResult EventType = "user.tool_result"
	// EventAgentMessage is a response the agent wrote; its payload is an
	// AgentMessage.
	EventAgentMessage EventType = "agent.message"
	// EventAgentToolUse is a tool call the agent made; its payload is a
	// ToolUse. A question is a call of the tool named "question".
	EventAgentToolUse EventType = "agent.tool_use"
	// EventToolResult is the result of a tool call; its payload is a
	// ToolResult.
	EventToolResult EventType = "tool.result"
	// EventSessionStatus is a change of the session's status; its payload
	// is a SessionStatus.
	EventSessionStatus EventType = "session.status"
	// EventSessionError is an error the session ran into; its payload is a
	// SessionError.
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

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the event, with every field,
// including those this package does not model; nil for an Event the program
// built itself.
func (e *Event) RawJSON() json.RawMessage { return e.raw }

// UnmarshalJSON decodes the event and keeps the JSON for RawJSON.
func (e *Event) UnmarshalJSON(b []byte) error {
	type plain Event
	if err := json.Unmarshal(b, (*plain)(e)); err != nil {
		return err
	}
	e.raw = keep(b)
	return nil
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
	// Type is the block's kind. A response holds blocks of kinds this
	// type does not read, such as "tool_use", whose own event carries
	// them.
	Type BlockType `json:"type"`
	// Text is the text of a BlockText or BlockThinking block.
	Text string `json:"text,omitempty"`
}

// BlockType is the kind of a ContentBlock.
type BlockType string

// The kinds of ContentBlock a client reads.
const (
	// BlockText is text.
	BlockText BlockType = "text"
	// BlockThinking is the model's reasoning before it answers, when the
	// model shows it.
	BlockThinking BlockType = "thinking"
)

// Text returns a text block holding s.
func Text(s string) ContentBlock { return ContentBlock{Type: BlockText, Text: s} }

// textOf joins the text of the BlockText blocks of blocks.
func textOf(blocks []ContentBlock) string {
	var b strings.Builder
	for _, c := range blocks {
		if c.Type == BlockText {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

// An AgentMessage is the payload of an EventAgentMessage event: one
// response of the model, complete.
type AgentMessage struct {
	// Message is the response.
	Message Message `json:"message"`
	// StopReason says why the response ended: StopEndTurn when the agent
	// is done with the turn, or another reason, such as a tool call it
	// waits on.
	StopReason StopReason `json:"stop_reason"`
}

// Text returns the response's text, its text blocks joined, without its
// thinking.
func (m *AgentMessage) Text() string { return textOf(m.Message.Blocks) }

// A Message is a model's response.
type Message struct {
	// Role is "assistant" for a response of the agent.
	Role string `json:"role"`
	// Blocks are the response's parts, in order.
	Blocks []ContentBlock `json:"blocks"`
}

// A ToolResult is the payload of an EventToolResult event, the result of a
// tool call the platform ran, and of an EventUserToolResult event, the
// result your program sent with Client.SendToolResult.
type ToolResult struct {
	// ToolUseID names the call the result answers.
	ToolUseID string `json:"tool_use_id"`
	// Content is the result the agent read.
	Content []ContentBlock `json:"content"`
	// IsError reports that the call failed.
	IsError bool `json:"is_error,omitempty"`
	// Outcome sums the result up; empty when the tool says nothing more
	// than IsError.
	Outcome ToolOutcome `json:"outcome,omitempty"`
	// Sender is who sent an EventUserToolResult; nil for a result the
	// platform recorded.
	Sender *Sender `json:"sender,omitempty"`
}

// Text returns the result's text, its text blocks joined.
func (r *ToolResult) Text() string { return textOf(r.Content) }

// A SessionError is the payload of an EventSessionError event: something
// went wrong in the session, such as a model that is busy or a machine
// that was lost.
type SessionError struct {
	// Code identifies the error, such as "model_busy" or "machine_lost".
	Code string `json:"code"`
	// Message says what happened, in words for a person.
	Message string `json:"message"`
	// Retryable reports that sending the message again may succeed.
	Retryable bool `json:"retryable,omitempty"`
	// Detail adds what a developer needs, when there is more to say.
	Detail string `json:"detail,omitempty"`
}

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
	// Client reports a call of a tool the agent declares for its client
	// to run: your program runs it, and the session waits with
	// StopToolResult until Client.SendToolResult answers it.
	Client bool `json:"client,omitempty"`
	// Verdict is what the session's approval policy decided about the
	// call. VerdictAsk means the session waits with StopToolConfirmation
	// until Client.ConfirmTool answers the call.
	Verdict Verdict `json:"verdict,omitempty"`
	// Reason explains the verdict, when the policy gave one.
	Reason string `json:"reason,omitempty"`
}

// Verdict is what a session's approval policy decided about a tool call or
// a connection.
type Verdict string

// The verdicts of ToolUse and ApprovalRequested.
const (
	// VerdictAllow runs the call without asking.
	VerdictAllow Verdict = "allow"
	// VerdictAsk waits for a person to allow or deny the call.
	VerdictAsk Verdict = "ask"
	// VerdictDeny refuses the call; the agent is told.
	VerdictDeny Verdict = "deny"
	// VerdictFlag runs the call and marks it for review.
	VerdictFlag Verdict = "flag"
	// VerdictBlock refuses a connection without asking and records it.
	VerdictBlock Verdict = "block"
)

// ApprovalRequested is the payload of an EventApprovalRequested event:
// after a tool call ran, the session's network refused a connection the
// call made, and a person is asked whether the session may reach the host.
// Answer it with Client.ConfirmTool and ConfirmToolParams.ApprovalID.
type ApprovalRequested struct {
	// ApprovalID identifies the request; an answer names it.
	ApprovalID string `json:"approval_id"`
	// ToolUseID names the call that made the connection.
	ToolUseID string `json:"tool_use_id"`
	// Source is what raised the request.
	Source ApprovalSource `json:"source"`
	// Destination is the host the session tried to reach.
	Destination Destination `json:"destination"`
	// Reason explains the request, when there is more to say.
	Reason string `json:"reason,omitempty"`
	// Verdict is VerdictAsk for a request that waits for a person, or
	// VerdictBlock for one recorded without asking.
	Verdict Verdict `json:"verdict"`
	// More counts further connections to the same host the request
	// stands for.
	More int `json:"more,omitempty"`
}

// Destination is a host a session tried to reach.
type Destination struct {
	// Host is the host name or address.
	Host string `json:"host"`
	// Port is the port, when the request names one.
	Port int `json:"port,omitempty"`
}

// ApprovalDecided is the payload of an EventApprovalDecided event: the
// answer to an ApprovalRequested.
type ApprovalDecided struct {
	// ApprovalID names the request answered.
	ApprovalID string `json:"approval_id"`
	// Decision is DecisionAllow or DecisionDeny.
	Decision Decision `json:"decision"`
	// By is who answered, when a person did.
	By *Sender `json:"by,omitempty"`
	// Note is the answer's note, when it has one.
	Note string `json:"note,omitempty"`
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

// ToolOutcome sums up how a tool call ended.
type ToolOutcome string

// The outcomes of a tool call.
const (
	// ToolOK ran and succeeded.
	ToolOK ToolOutcome = "ok"
	// ToolError ran and failed.
	ToolError ToolOutcome = "error"
	// ToolTimeout ran out of time.
	ToolTimeout ToolOutcome = "timeout"
	// ToolUnknown named a tool the agent does not have.
	ToolUnknown ToolOutcome = "unknown_tool"
	// ToolInvalidInput was called with input the tool refused.
	ToolInvalidInput ToolOutcome = "invalid_input"
	// ToolDenied was denied: by a person, or because nobody attended the
	// session to approve it.
	ToolDenied ToolOutcome = "denied"
	// ToolBlocked was refused by the approval policy.
	ToolBlocked ToolOutcome = "blocked"
	// ToolCanceled was canceled, such as by an interrupt.
	ToolCanceled ToolOutcome = "canceled"
	// ToolUnknownEffect stopped where it cannot be known whether it took
	// effect, such as a command cut off mid-run.
	ToolUnknownEffect ToolOutcome = "unknown_effect"
	// ToolUnanswered is a question nobody chose an option of; it is not an
	// error, since the call did what it was for.
	ToolUnanswered ToolOutcome = "unanswered"
)

// ApprovalSource is what raised an approval request.
type ApprovalSource string

// The sources of an approval request.
const (
	// ApprovalFromEgress is a connection the session's network refused.
	ApprovalFromEgress ApprovalSource = "egress"
)
