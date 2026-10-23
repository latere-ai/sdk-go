// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"latere.ai/x/sdk-go/agents/routine"
)

// The names of the tools an agent keeps standing work with. A program
// shows a held call of ToolTrigger or ToolWake as an offer, which
// ToolUse.Offer reads.
const (
	// ToolTrigger is the tool an agent lists, offers, changes, pauses,
	// resumes and deletes the person's triggers of it with. Its calls that
	// offer, change, resume and delete wait for the person who started
	// the session every time, whatever the session's approvals, and run,
	// as theirs, only once they allow them. A pause runs at once.
	ToolTrigger = "trigger"
	// ToolWake is the tool an agent sets or cancels the session's next
	// wake with: a trigger that fires once and sends the agent's note back
	// to the session. A wake set after too many in a row with no word from
	// the person waits for them; any other runs at once.
	ToolWake = "wake"
	// ToolNotify is the tool an agent brings what a turn a trigger or a
	// wake began found to the person with, making the turn's outcome
	// TurnNotified; the Session's Notice is its latest call.
	ToolNotify = "notify"
)

// OfferAction is what an Offer asks the person to allow.
type OfferAction string

// The actions of an Offer.
const (
	// OfferPropose makes a new trigger: a routine, for one on a schedule,
	// or a trigger that fires on the events On selects.
	OfferPropose OfferAction = "propose"
	// OfferChange changes the trigger TriggerID names: the fields the
	// offer sets replace that trigger's, and it keeps every other.
	OfferChange OfferAction = "change"
	// OfferResume resumes the paused trigger TriggerID names.
	OfferResume OfferAction = "resume"
	// OfferDelete deletes the trigger TriggerID names; the sessions it
	// started are kept.
	OfferDelete OfferAction = "delete"
	// OfferWake sets the session's next wake, after the agent has woken
	// itself too many times in a row with no word from the person. Their
	// allow is their word, so the row starts again; their deny leaves the
	// session without a wake.
	OfferWake OfferAction = "wake"
)

// An Offer is standing work an agent offers the person in a session: a
// held call of its trigger tool, or of its wake tool. The session waits
// with StopToolConfirmation until the person who started it answers with
// Client.ConfirmTool: DecisionAllow runs the call, as theirs, and
// DecisionDeny runs nothing. A reply the person writes instead is sent
// as a DecisionDeny whose Note holds their words, which the agent reads
// and may offer again from. An answer from anyone else is refused with
// latere.ErrForbidden.
//
// The fields hold what the call asks, to word the offer from. A change,
// a resume and a delete name their trigger by TriggerID alone: read it
// with Client.GetTrigger for its name and what a change would change.
type Offer struct {
	// ToolUseID is the call ConfirmToolParams.ToolUseID answers.
	ToolUseID string
	// Action is what the call asks to do.
	Action OfferAction
	// TriggerID is the trigger a change, a resume or a delete acts on;
	// empty for a proposal and a wake.
	TriggerID string
	// Name is the trigger's name, as the person would call it, for a
	// proposal and for a change that renames it; for a wake, the one line
	// the agent wrote of what it will look for.
	Name string
	// Ask is what each run of the trigger asks, for a proposal and for a
	// change that changes it.
	Ask string
	// Why is the sentence the agent wrote of why it offers the trigger,
	// which says the schedule or the event back, for a proposal and a
	// change.
	Why string
	// Schedule is when a trigger on a schedule would run, for a proposal
	// of one and a change of its schedule. It is nil for a schedule of a
	// shape no routine runs on, which the call is refused for before
	// anyone is asked.
	Schedule *RoutineSchedule
	// On selects the events a trigger would fire on, for a proposal of one
	// and a change of what it fires on.
	On *EventFilter
	// Notify is whether every run would bring its result to the person, or
	// the agent would judge, for a proposal or a change that names it;
	// empty when the call leaves it out, so a proposal's trigger takes the
	// API's default and a change keeps the trigger's.
	Notify NotifyPolicy
	// PerWeekUSD is the weekly allowance the call asks for, as the agent
	// wrote it, a decimal amount of US dollars such as "1.50": its
	// estimate of what the trigger's looks cost in a week, which the API
	// enforces as the trigger's TriggerAllowance once the person allows
	// the call. Empty when the call names none, so a proposal's trigger
	// has no allowance and a change keeps the trigger's.
	PerWeekUSD string
	// PerWeekMicroUSD is PerWeekUSD in millionths of a US dollar, the unit
	// of TriggerAllowance and TriggerSpend; zero when the call names none.
	PerWeekMicroUSD int64
	// At is when a wake would fire, for one set at a time; nil for one set
	// After a while.
	At *time.Time
	// After is how long after its call a wake would fire, for one set that
	// way: the ToolUse's event's Time plus After. Zero for one set At a
	// time.
	After time.Duration
	// Note is what the agent wrote to itself for a wake: what to look at
	// and what to do with each answer, which the wake sends back to the
	// session.
	Note string
}

// wakeSet is the action of a call of the wake tool that sets a wake.
const wakeSet = "set"

// Offer returns what a held call of the trigger tool or the wake tool
// offers, and an error for a call of any other tool, a call that offers
// nothing, or an input that does not decode. The trigger tool's list and
// pause offer nothing, since neither waits for anyone, nor does the wake
// tool's cancel; its set is an offer only while it is held, its Verdict
// VerdictAsk.
func (t *ToolUse) Offer() (*Offer, error) {
	switch t.Name {
	case ToolTrigger:
		return t.triggerOffer()
	case ToolWake:
		return t.wakeOffer()
	}
	return nil, fmt.Errorf("agents: the tool call %s is %q, neither %s nor %s", t.ToolUseID, t.Name, ToolTrigger, ToolWake)
}

// triggerOffer reads a call of the trigger tool.
func (t *ToolUse) triggerOffer() (*Offer, error) {
	var in struct {
		Action   string       `json:"action"`
		ID       string       `json:"id"`
		Name     string       `json:"name"`
		Ask      string       `json:"ask"`
		Why      string       `json:"why"`
		Schedule string       `json:"schedule"`
		TimeZone string       `json:"timeZone"`
		On       *EventFilter `json:"on"`
		Notify   NotifyPolicy `json:"notify"`
		// The agent writes the allowance as a string or as a number.
		PerWeekUSD json.RawMessage `json:"perWeekUsd"`
	}
	if err := json.Unmarshal(t.Input, &in); err != nil {
		return nil, fmt.Errorf("agents: decoding the offer of %s: %w", t.ToolUseID, err)
	}
	o := &Offer{ToolUseID: t.ToolUseID, Action: OfferAction(in.Action), Name: in.Name, Ask: in.Ask, Why: in.Why, On: in.On, Notify: in.Notify}
	switch o.Action {
	case OfferPropose:
	case OfferChange, OfferResume, OfferDelete:
		o.TriggerID = in.ID
	default:
		return nil, fmt.Errorf("agents: the trigger call %s is a %q, which offers nothing", t.ToolUseID, in.Action)
	}
	if o.Action != OfferPropose && o.Action != OfferChange {
		// A resume and a delete carry the id alone; anything else the
		// input holds is not what they do.
		o.Name, o.Ask, o.Why, o.On, o.Notify = "", "", "", nil, ""
		return o, nil
	}
	if in.Schedule != "" {
		o.Schedule = routine.ParseSchedule(in.Schedule, in.TimeZone)
	}
	usd, micro, err := parseUSD(in.PerWeekUSD)
	if err != nil {
		return nil, fmt.Errorf("agents: the trigger call %s asks for a weekly allowance of %s: %w", t.ToolUseID, in.PerWeekUSD, err)
	}
	o.PerWeekUSD, o.PerWeekMicroUSD = usd, micro
	return o, nil
}

// microsPerUSD is how many millionths of a US dollar make one.
const microsPerUSD = 1_000_000

// parseUSD reads the weekly allowance a call of the trigger tool asks for
// as the tool reads it, and returns it as written and in millionths of a
// dollar. The tool decodes it as a JSON number, or a JSON string holding
// one, and takes digits, a point and at most six more, above zero: so
// neither a sign, an exponent, a space nor a point without digits before
// it is an amount. It returns "" and 0 for an amount left out or null, and
// an error for any other, and for one past what millionths of a dollar
// hold in an int64.
func parseUSD(raw json.RawMessage) (string, int64, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return "", 0, nil
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return "", 0, err
	}
	text := n.String()
	if text == "" {
		return "", 0, nil
	}
	whole, frac, _ := strings.Cut(text, ".")
	if whole == "" || len(frac) > 6 || strings.HasPrefix(whole, "-") {
		return "", 0, errors.New("it is not a decimal amount of dollars with at most six digits after the point")
	}
	// A JSON number's whole part is digits after the sign the check above
	// refused, so a failure is an exponent, or digits past an int64.
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return "", 0, fmt.Errorf("it is not a decimal amount of dollars: %w", err)
	}
	var f int64
	if frac != "" {
		// A JSON number's fraction is digits, and an exponent after them
		// fails here.
		if f, err = strconv.ParseInt(frac+strings.Repeat("0", 6-len(frac)), 10, 64); err != nil {
			return "", 0, fmt.Errorf("it is not a decimal amount of dollars: %w", err)
		}
	}
	if w > (math.MaxInt64-f)/microsPerUSD {
		return "", 0, errors.New("it is too large")
	}
	micro := w*microsPerUSD + f
	if micro <= 0 {
		return "", 0, errors.New("it is not above zero")
	}
	return text, micro, nil
}

// wakeOffer reads a held call of the wake tool.
func (t *ToolUse) wakeOffer() (*Offer, error) {
	var in struct {
		Action string `json:"action"`
		At     string `json:"at"`
		After  string `json:"after"`
		Title  string `json:"title"`
		Note   string `json:"note"`
	}
	if err := json.Unmarshal(t.Input, &in); err != nil {
		return nil, fmt.Errorf("agents: decoding the wake of %s: %w", t.ToolUseID, err)
	}
	if in.Action != wakeSet {
		return nil, fmt.Errorf("agents: the wake call %s is a %q, which offers nothing", t.ToolUseID, in.Action)
	}
	if t.Verdict != VerdictAsk {
		return nil, fmt.Errorf("agents: the wake call %s does not wait for the person, so it offers nothing", t.ToolUseID)
	}
	o := &Offer{ToolUseID: t.ToolUseID, Action: OfferWake, Name: in.Title, Note: in.Note}
	switch {
	case in.At != "":
		at, err := time.Parse(time.RFC3339, in.At)
		if err != nil {
			return nil, fmt.Errorf("agents: the wake call %s is at %q: %w", t.ToolUseID, in.At, err)
		}
		o.At = &at
	case in.After != "":
		after, err := time.ParseDuration(in.After)
		if err != nil {
			return nil, fmt.Errorf("agents: the wake call %s is after %q: %w", t.ToolUseID, in.After, err)
		}
		o.After = after
	}
	return o, nil
}
