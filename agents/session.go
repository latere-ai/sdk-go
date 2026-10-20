// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents

import (
	"encoding/json"
	"time"
)

// Status is the state of a session.
type Status string

// The statuses of a session.
const (
	// StatusIdle is a session waiting for its next message, or for an
	// answer when StopReason says it asked something.
	StatusIdle Status = "idle"
	// StatusRunning is a session whose agent is working on a turn.
	StatusRunning Status = "running"
	// StatusEnded is a session that takes no more messages.
	StatusEnded Status = "ended"
)

// StopReason says why a session is idle or ended.
type StopReason string

// Why an idle session stopped.
const (
	// StopEndTurn is a turn that finished; the session waits for a message.
	StopEndTurn StopReason = "end_turn"
	// StopToolConfirmation is a tool call waiting for approval; see
	// Client.ConfirmTool.
	StopToolConfirmation StopReason = "tool_confirmation"
	// StopToolResult is a tool call your program runs, a ToolUse with
	// Client set, waiting for its result; see Client.SendToolResult.
	StopToolResult StopReason = "tool_result"
	// StopQuestion is a question waiting for an answer; see
	// Client.AnswerQuestion.
	StopQuestion StopReason = "question"
	// StopBudget is a session that spent its budget.
	StopBudget StopReason = "budget"
	// StopTurnLimit is a turn that reached its step limit.
	StopTurnLimit StopReason = "turn_limit"
	// StopOutputLimit is a response that reached its length limit.
	StopOutputLimit StopReason = "output_limit"
	// StopInterrupted is a turn stopped by Client.Interrupt.
	StopInterrupted StopReason = "interrupted"
	// StopError is a turn that failed.
	StopError StopReason = "error"
	// StopMachineWaiting is a turn waiting for the computer it works on,
	// which is offline or has not let the session in yet.
	StopMachineWaiting StopReason = "machine_waiting"
)

// Why an ended session ended.
const (
	// StopCompleted is a session whose work is done; Client.EndSession
	// takes it.
	StopCompleted StopReason = "completed"
	// StopFailed is a session that could not go on.
	StopFailed StopReason = "failed"
	// StopCanceled is a session whose work was given up; Client.EndSession
	// takes it.
	StopCanceled StopReason = "canceled"
	// StopExpired is a session that reached its maximum age.
	StopExpired StopReason = "expired"
)

// A Session is a conversation with an agent.
type Session struct {
	// ID identifies the session, such as "ses_01M34G2JF01B91B8399CD1G6YP".
	ID string `json:"id"`
	// Agent is the agent the session talks to, at the version it runs.
	Agent SessionAgent `json:"agent"`
	// Title names the session for a person; empty until one is set.
	Title string `json:"title,omitempty"`
	// Initiator is who started the session.
	Initiator Sender `json:"initiator"`
	// Runner is where the agent runs.
	Runner Runner `json:"runner,omitempty"`
	// Status is the session's state.
	Status Status `json:"status"`
	// StopReason says why an idle or ended session stopped; empty while it
	// runs.
	StopReason StopReason `json:"stop_reason,omitempty"`
	// Turn counts the turns the session has run.
	Turn int `json:"turn"`
	// LastSeq is the sequence of the newest event in the session's log.
	LastSeq int64 `json:"last_seq"`
	// Model is the model the next turn runs on, when the session names one.
	Model *Model `json:"model,omitempty"`
	// Machine is where the session's commands run, when it has a machine.
	Machine *Machine `json:"machine,omitempty"`
	// Attended reports whether a person follows the session, so it may stop
	// to ask them things.
	Attended bool `json:"attended,omitempty"`
	// Metadata holds the caller's own labels, set at creation.
	Metadata map[string]string `json:"metadata,omitempty"`
	// Parent is the session and sequence this one was forked from; nil for
	// a session no fork made.
	Parent *Parent `json:"parent,omitempty"`
	// Root is the id of the session at the top of this one's fork tree;
	// empty for a session no fork made.
	Root string `json:"root,omitempty"`
	// TriggerID is the trigger that started the session, if one did.
	TriggerID string `json:"trigger_id,omitempty"`
	// Budget is what the session has spent.
	Budget *Budget `json:"budget,omitempty"`
	// CreatedAt is when the session was created.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is when the session last changed.
	UpdatedAt time.Time `json:"updated_at"`
	// ExpiresAt is when the session ends if it is still open.
	ExpiresAt time.Time `json:"expires_at,omitzero"`
	// ArchivedAt is when the session was archived; nil when it is not.
	ArchivedAt *time.Time `json:"archived_at,omitempty"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the session, with every
// field, including those this package does not model; nil for a Session the
// program built itself.
func (s *Session) RawJSON() json.RawMessage { return s.raw }

// UnmarshalJSON decodes the session and keeps the JSON for RawJSON.
func (s *Session) UnmarshalJSON(b []byte) error {
	type plain Session
	if err := json.Unmarshal(b, (*plain)(s)); err != nil {
		return err
	}
	s.raw = keep(b)
	return nil
}

// Runner is where a session's agent runs.
type Runner string

// The runners of a session.
const (
	// RunnerHosted runs the agent on the platform.
	RunnerHosted Runner = "hosted"
	// RunnerExternal runs the agent in a program outside the platform.
	RunnerExternal Runner = "external"
)

// SessionAgent is the agent a session talks to.
type SessionAgent struct {
	// ID identifies the agent.
	ID string `json:"id"`
	// Name is the agent's name among its owner's agents.
	Name string `json:"name"`
	// Version is the agent's version the session runs.
	Version int `json:"version"`
}

// A Sender is whoever wrote an event or started a session.
type Sender struct {
	// Subject identifies the sender.
	Subject string `json:"subject"`
	// Name is the sender's display name, when known.
	Name string `json:"name,omitempty"`
	// Kind is what the sender is.
	Kind SenderKind `json:"kind"`
}

// SenderKind is what a Sender is.
type SenderKind string

// The kinds of Sender.
const (
	// SenderPerson is a person, directly or through a key of theirs.
	SenderPerson SenderKind = "person"
	// SenderTrigger is a trigger that started a session or sent to it.
	SenderTrigger SenderKind = "trigger"
	// SenderService is the platform itself.
	SenderService SenderKind = "service"
)

// Model is the model a session's turns run on.
type Model struct {
	// Name is the model's routed name, such as "tier/quick" for an Effort
	// level.
	Name string `json:"name"`
	// Reasoning is how much the model reasons, when it takes a level.
	Reasoning string `json:"reasoning,omitempty"`
}

// MachineKind is what kind of machine a session's commands run on.
type MachineKind string

// The kinds of Machine.
const (
	// MachineSandbox is a sandbox on the platform.
	MachineSandbox MachineKind = "cella"
	// MachineDevice is one of the person's own computers.
	MachineDevice MachineKind = "device"
	// MachineHost is the machine of a runner outside the platform.
	MachineHost MachineKind = "host"
)

// Machine is where a session's commands run.
type Machine struct {
	// Kind is the machine's kind.
	Kind MachineKind `json:"kind"`
	// Environment names the environment of a MachineDevice: the computer's
	// id, from account.Device.
	Environment string `json:"environment,omitempty"`
	// Image is the sandbox image of a MachineSandbox.
	Image string `json:"image,omitempty"`
	// Workdir is the directory the session works in.
	Workdir string `json:"workdir,omitempty"`
}

// Parent is where a forked session came from.
type Parent struct {
	// SessionID is the session it was forked from.
	SessionID string `json:"session_id"`
	// Seq is the sequence of the last event it shares with that session.
	Seq int64 `json:"seq"`
}

// Budget is what a session has spent.
type Budget struct {
	// SpentMicroUSD is the session's spending so far, in millionths of a
	// US dollar.
	SpentMicroUSD int64 `json:"spent_cost_usd_micro"`
}
