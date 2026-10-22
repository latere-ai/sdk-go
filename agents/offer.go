// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents

import (
	"encoding/json"
	"fmt"

	"latere.ai/x/sdk-go/agents/routine"
)

// ToolTrigger is the name of the tool an agent offers a person a routine
// with. Its call waits for the person who started the session every time,
// whatever the session's approvals, and the routine is made, as theirs,
// only once they allow it.
const ToolTrigger = "trigger"

// An Offer is a routine an agent offers the person in a session: a call
// of the trigger tool that proposes one. The session waits with
// StopToolConfirmation until the person who started it answers with
// Client.ConfirmTool: DecisionAllow makes the routine, which ListRoutines
// then lists as any other, and DecisionDeny makes nothing. A reply the
// person writes instead is sent as a DecisionDeny whose Note holds their
// words, which the agent reads and may offer again from.
type Offer struct {
	// ToolUseID is the call ConfirmToolParams.ToolUseID answers.
	ToolUseID string
	// Name is the routine's name, as the person would call it.
	Name string
	// Ask is what each run of the routine asks.
	Ask string
	// Why is the sentence the agent wrote of why it offers the routine,
	// which says the schedule back.
	Why string
	// Schedule is when the routine would run. It is nil for a schedule of
	// a shape no routine runs on, which the call is refused for before
	// anyone is asked.
	Schedule *RoutineSchedule
}

// Offer returns the routine a call of the trigger tool offers, and an
// error for a call of any other tool, a call that offers nothing, such as
// one that lists, or an input that does not decode.
func (t *ToolUse) Offer() (*Offer, error) {
	if t.Name != ToolTrigger {
		return nil, fmt.Errorf("agents: the tool call %s is %q, not %s", t.ToolUseID, t.Name, ToolTrigger)
	}
	var in struct {
		Action   string `json:"action"`
		Name     string `json:"name"`
		Ask      string `json:"ask"`
		Why      string `json:"why"`
		Schedule string `json:"schedule"`
		TimeZone string `json:"timeZone"`
	}
	if err := json.Unmarshal(t.Input, &in); err != nil {
		return nil, fmt.Errorf("agents: decoding the offer of %s: %w", t.ToolUseID, err)
	}
	if in.Action != "propose" {
		return nil, fmt.Errorf("agents: the trigger call %s is a %q, which offers nothing", t.ToolUseID, in.Action)
	}
	return &Offer{ToolUseID: t.ToolUseID, Name: in.Name, Ask: in.Ask, Why: in.Why, Schedule: routine.ParseSchedule(in.Schedule, in.TimeZone)}, nil
}
