// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"time"

	"latere.ai/x/sdk-go/agents/routine"
	"latere.ai/x/sdk-go/latere"
)

// A routine is an instruction a person hands an agent once, which the
// agent then carries out with nobody waiting: on a schedule, each time
// another app sends to the routine's private link or an email reaches its
// address, or at each of a few dates the person picked. A routine is a
// trigger of the person's
// own, written in one shape, so every program that shows a person's
// routines reads and changes the same triggers: Latere Chat's routines are
// those of the agent the platform manages for it, "latere-chat", and Latere
// Studio lists the same ones. The routine methods write and read that
// shape over the trigger endpoints; the schedule, the firing and the
// session a run starts are the API's.

// The marks and bounds of a routine, package routine's.
const (
	// RoutineLabel is the label a routine's trigger carries, its value the
	// name of the agent whose routine it is. Only a trigger with the label
	// reads as a routine, so a trigger made another way, in the developer
	// console for instance, is never listed, changed or deleted as one.
	RoutineLabel = routine.Label
	// RoutinePrefix starts the name of every routine's trigger; 20
	// lowercase hexadecimal digits follow it.
	RoutinePrefix = routine.Prefix
	// MaxRoutineTitle is the most characters a routine's name holds.
	MaxRoutineTitle = routine.MaxTitle
	// MaxRoutineInstruction is the most bytes the instruction of a routine
	// on a schedule or on dates holds: the longest message a firing
	// renders.
	MaxRoutineInstruction = routine.MaxInstruction
	// MaxEventRoutineInstruction is the most bytes the instruction of a
	// routine that starts from a link or an email holds. What arrives is
	// written into the same message after it, each value cut at 16 KiB and
	// the message refused past MaxRoutineInstruction, so an instruction of
	// this length and everything that arrives fit together.
	MaxEventRoutineInstruction = routine.MaxEventInstruction
	// MaxRoutineDates is the most dates a routine on dates runs at, the
	// most times the API takes in a trigger's spec.times.
	MaxRoutineDates = routine.MaxDates
)

// RoutineStart is how a routine starts.
type RoutineStart = routine.Start

// The ways a routine starts.
const (
	// StartsOnSchedule runs the routine on the days and at the time of its
	// RoutineSchedule.
	StartsOnSchedule = routine.OnSchedule
	// StartsFromLink runs the routine each time another app sends to its
	// private link, which the platform issues; what the app sent follows the
	// instruction in the run's message.
	StartsFromLink = routine.FromLink
	// StartsFromEmail runs the routine each time an email reaches its
	// address, which the platform issues; the email follows the instruction
	// in the run's message.
	StartsFromEmail = routine.FromEmail
	// StartsOnDates runs the routine once at each of its Dates, times a
	// schedule cannot say. The API deletes the routine after the last; the
	// sessions the runs started keep what they did.
	StartsOnDates = routine.OnDates
)

// A RoutineSchedule is when a routine on a schedule runs: on the days
// listed, at a time of day, in a time zone.
type RoutineSchedule = routine.Schedule

// A Routine is a routine as read from its trigger.
type Routine struct {
	// Name is the name of the routine's trigger, RoutinePrefix and 20
	// hexadecimal digits, and the routine's id in the routine methods.
	Name string
	// TriggerID is the trigger's id, which each session a run starts names
	// as its TriggerID.
	TriggerID string
	// Agent is the agent whose routine it is, the value of its
	// RoutineLabel.
	Agent string
	// Title is the routine's name, as a person reads it, and the title of
	// each session a run starts.
	Title string
	// Instruction is what the agent is asked to do on each run, as the
	// person wrote it.
	Instruction string
	// Starts is how the routine starts.
	Starts RoutineStart
	// Schedule is when a routine on a schedule runs. It is nil for a
	// routine that starts from a link or an email or on dates, and for one
	// whose schedule has a shape a routine does not write, set another way.
	Schedule *RoutineSchedule
	// Dates are the times a routine on dates runs at, once each, in
	// ascending order, those that have passed among them, and DatesZone
	// the IANA time zone the person picked them in, UTC when they picked
	// none, so a program can say the dates back in that zone. Nil and
	// empty for the other ways a routine starts.
	Dates     []time.Time
	DatesZone string
	// Model is the model each run's session starts on, such as
	// "tier/thorough"; empty when runs start on the agent's own model.
	Model string
	// Notify is whether every run brings its result to the person, or
	// only the runs the agent judges need them; NotifyAlways for a routine
	// that names none.
	Notify NotifyPolicy
	// Paused says the routine runs nothing until it is resumed.
	Paused bool
	// NextRunAt is when a routine on a schedule runs next, or the first of
	// the Dates of one on dates that is after now when it is read; nil
	// while it is paused, for one on dates with no date left, and for one
	// that starts from a link or an email.
	NextRunAt *time.Time
	// LastRunAt is when it last ran, and LastSessionID the session that
	// run started; nil and empty before its first run.
	LastRunAt     *time.Time
	LastSessionID string
	// CreatedAt is when the routine was made.
	CreatedAt time.Time
	// Origin is the offer that made the routine, for one an agent offered
	// in a session and the person accepted, when they did so at
	// CreatedAt; nil for a routine the person made.
	Origin *TriggerOrigin
	// PerWeekMicroUSD is the routine's weekly allowance, in millionths of
	// a US dollar: the most its runs, and the looks of the wakes set in
	// the sessions it started, may cost in a rolling 7 days. Zero when it
	// has none.
	PerWeekMicroUSD int64
	// Spend is the routine's week as of its latest run: what its runs cost
	// in the 7 days before it, and the pace they are on. Nil for a routine
	// that has not run since the API began writing it.
	Spend *TriggerSpend
	// SkippedBudget counts the runs the allowance skipped, which started
	// nothing because the week's spend was at it.
	SkippedBudget int64

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the routine's trigger,
// with every field, including those this package does not model; nil for
// a Routine the program built itself.
func (r *Routine) RawJSON() json.RawMessage { return r.raw }

// RoutineName returns the name of the trigger a routine made with key is
// written under: RoutinePrefix and the first 20 hexadecimal digits of the
// key's SHA-256. The same key always names the same trigger, so a create
// sent again with its key replaces the routine it made rather than making
// a second.
func RoutineName(key string) string { return routine.Name(key) }

// RoutineOf reads t as a routine of agent, and reports false when it is
// none: a trigger without the RoutineLabel of agent, or whose name is not
// a routine's. The instruction and the title are read as the person wrote
// them, without the words a routine that starts from a link or an email
// adds after its instruction. A trigger that fires at its spec's Times
// reads as a routine on dates.
func RoutineOf(t *Trigger, agent string) (*Routine, bool) {
	if t == nil {
		return nil, false
	}
	f, ok := routine.Read(shapeOf(t), agent)
	if !ok {
		return nil, false
	}
	r := &Routine{
		Name: t.Metadata.Name, TriggerID: t.Status.ID, Agent: agent, Title: f.Title, Instruction: f.Instruction,
		Starts: f.Starts, Schedule: f.Schedule, Dates: f.Dates, DatesZone: f.DatesZone, Notify: NotifyPolicy(f.Notify), Paused: f.Paused,
		LastRunAt: t.Status.LastFiredAt, LastSessionID: t.Status.LastSessionID, CreatedAt: t.Status.CreatedAt, Origin: t.Status.Origin,
		PerWeekMicroUSD: perWeekOf(t), Spend: t.Status.Spend, SkippedBudget: skippedBudgetOf(t), raw: t.raw,
	}
	if f.Model != nil {
		r.Model = *f.Model
	}
	if r.Notify == "" {
		r.Notify = NotifyAlways
	}
	switch {
	case r.Paused:
	case r.Starts == StartsOnSchedule:
		r.NextRunAt = t.Status.NextFireAt
	case r.Starts == StartsOnDates:
		r.NextRunAt = nextDate(r.Dates, time.Now())
	}
	return r, true
}

// nextDate is the first of dates, in ascending order, after now; nil when
// every one has passed.
func nextDate(dates []time.Time, now time.Time) *time.Time {
	for _, d := range dates {
		if d.After(now) {
			return &d
		}
	}
	return nil
}

// perWeekOf is t's weekly allowance, zero when it has none.
func perWeekOf(t *Trigger) int64 {
	if t.Spec.Budget == nil {
		return 0
	}
	return t.Spec.Budget.PerWeekMicroUSD
}

// skippedBudgetOf is how many of t's firings its allowance skipped.
func skippedBudgetOf(t *Trigger) int64 {
	if t.Status.Counts == nil {
		return 0
	}
	return t.Status.Counts.SkippedBudget
}

// shapeOf is what package routine reads of t.
func shapeOf(t *Trigger) routine.Trigger {
	shape := routine.Trigger{Name: t.Metadata.Name, DisplayName: t.Metadata.DisplayName, Labels: t.Metadata.Labels,
		Schedule: t.Spec.Schedule, TimeZone: t.Spec.TimeZone, Times: t.Spec.Times, Message: t.Spec.Session.Message, Title: t.Spec.Session.Title,
		Notify: string(t.Spec.Session.Notify), Suspend: t.Spec.Suspend, PerWeekMicroUSD: perWeekOf(t)}
	if t.Spec.On != nil {
		shape.Product = t.Spec.On.Product
	}
	if m := t.Spec.Session.Model; m != nil {
		shape.Model = m.Name
	}
	return shape
}

// The errors of the routine methods, each matched with errors.Is. Each is
// returned wrapped with what was wrong, and every one but ErrNotRoutine
// and ErrRoutinePaused before any request is sent.
var (
	// ErrNotRoutine matches a routine call for a name that names no
	// routine of the agent: a name of another shape, refused before any
	// request, or a trigger without the agent's RoutineLabel. It also
	// matches latere.ErrNotFound, as the API's answer for a name that names
	// no trigger does, so one check covers a missing routine.
	ErrNotRoutine error = notRoutine{}
	// ErrRoutineTitleMissing matches a routine without a name.
	ErrRoutineTitleMissing = routine.ErrTitleMissing
	// ErrRoutineTitleInvalid matches a name longer than MaxRoutineTitle
	// characters, or holding a control character, a line or paragraph
	// separator, or a bidirectional control.
	ErrRoutineTitleInvalid = routine.ErrTitleInvalid
	// ErrRoutineInstructionMissing matches a routine without an
	// instruction.
	ErrRoutineInstructionMissing = routine.ErrInstructionMissing
	// ErrRoutineInstructionTooLong matches an instruction past the bound of
	// how the routine starts: MaxRoutineInstruction, or
	// MaxEventRoutineInstruction for one that starts from a link or an
	// email.
	ErrRoutineInstructionTooLong = routine.ErrInstructionTooLong
	// ErrRoutineScheduleInvalid matches a routine on a schedule without
	// one, or a schedule with no day, a day, hour or minute out of range,
	// or a zone that is not an IANA name's shape.
	ErrRoutineScheduleInvalid = routine.ErrScheduleInvalid
	// ErrRoutineStartsInvalid matches a RoutineStart that is none of
	// StartsOnSchedule, StartsFromLink, StartsFromEmail and StartsOnDates.
	ErrRoutineStartsInvalid = routine.ErrStartsInvalid
	// ErrRoutineDatesInvalid matches a routine on dates without one, with
	// more than MaxRoutineDates, with the same time twice, with none after
	// now, which the API refuses too, or with a zone that is not an IANA
	// name's shape.
	ErrRoutineDatesInvalid = routine.ErrDatesInvalid
	// ErrRoutineNotifyInvalid matches a NotifyPolicy that is neither
	// NotifyAlways nor NotifyJudged.
	ErrRoutineNotifyInvalid = routine.ErrNotifyInvalid
	// ErrAllowanceInvalid matches a weekly allowance below zero, given to
	// CreateRoutine, ChangeRoutine or SetTriggerAllowance. Zero is none.
	ErrAllowanceInvalid = routine.ErrAllowanceInvalid
	// ErrRoutinePaused matches a run of a paused routine, refused before
	// it is fired, and the API's refusal of a firing of a trigger paused
	// since it was read, which also unwraps to the *latere.Error.
	ErrRoutinePaused = errors.New("agents: the routine is paused")
	// ErrRoutineStartsOnArrival matches a run of a routine that starts
	// from a link or an email, which would work on nothing that arrived.
	ErrRoutineStartsOnArrival = errors.New("agents: the routine runs when something arrives, not on request")
)

// notRoutine is ErrNotRoutine: a comparable value, so errors.Is finds it,
// that also matches latere.ErrNotFound.
type notRoutine struct{}

func (notRoutine) Error() string { return "agents: no such routine" }

// Is reports whether target is latere.ErrNotFound.
func (notRoutine) Is(target error) bool { return target == latere.ErrNotFound }

// checkName refuses a name of another shape than a routine's, before it
// goes into a request's path.
func checkName(name string) error {
	if !routine.IsName(name) {
		return fmt.Errorf("%w: %q is not a routine's name", ErrNotRoutine, name)
	}
	return nil
}

// errNoAgent is returned before any request when a routine call names no
// agent.
var errNoAgent = errors.New("agents: the routine's agent is empty")

// applyRoutine writes the manifest of the routine of agent named name and
// answers the routine as the API stored it.
func (c *Client) applyRoutine(ctx context.Context, agent, name string, stored json.RawMessage, f routine.Fields) (*Routine, error) {
	manifest, err := routine.Manifest(agent, name, stored, f)
	if err != nil {
		return nil, err
	}
	var out Trigger
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodPut, Path: triggerPath(name, ""), Body: manifest}, &out); err != nil {
		return nil, err
	}
	r, ok := RoutineOf(&out, agent)
	if !ok {
		return nil, fmt.Errorf("agents: the API answered the routine %s without the label %s: %s", name, RoutineLabel, agent)
	}
	return r, nil
}

// readRoutine reads the trigger of the routine of agent named name.
func (c *Client) readRoutine(ctx context.Context, agent, name string) (*Trigger, *Routine, error) {
	if agent == "" {
		return nil, nil, errNoAgent
	}
	if err := checkName(name); err != nil {
		return nil, nil, err
	}
	t, err := c.GetTrigger(ctx, name)
	if err != nil {
		return nil, nil, err
	}
	r, ok := RoutineOf(t, agent)
	if !ok {
		return nil, nil, fmt.Errorf("%w: the trigger %s is not a routine of %s", ErrNotRoutine, name, agent)
	}
	return t, r, nil
}

// routinePage is how many triggers a page of ListRoutines asks for when
// ListRoutinesParams.Limit is zero.
const routinePage = 100

// ListRoutinesParams names the agent whose routines to list, and pages
// them.
type ListRoutinesParams struct {
	// Agent is the agent whose routines to list, by name, such as
	// "latere-chat". Required.
	Agent string
	// Limit is how many triggers a page reads, of which the routines are
	// kept, up to 500; 100 when zero. A page may hold fewer routines.
	Limit int
	// Cursor starts ListRoutinesPage at the page a previous one's
	// NextCursor named; ListRoutines ignores it.
	Cursor string
}

// ListRoutines returns an iterator over the caller's routines of
// params.Agent, newest first as the trigger list orders them, reading the
// trigger list a page at a time and keeping the triggers RoutineOf reads
// as routines. An empty Agent yields an error before any request.
func (c *Client) ListRoutines(ctx context.Context, params *ListRoutinesParams) iter.Seq2[*Routine, error] {
	return latere.Items(ctx, func(ctx context.Context, cursor string) (*latere.Page[*Routine], error) {
		return c.routinesPage(ctx, params, cursor)
	})
}

// ListRoutinesPage returns the routines of params.Agent among one page of
// the caller's triggers, starting at params.Cursor, and the cursor of the
// next page in NextCursor. A page may hold no routine while NextCursor
// names another.
func (c *Client) ListRoutinesPage(ctx context.Context, params *ListRoutinesParams) (*latere.Page[*Routine], error) {
	cursor := ""
	if params != nil {
		cursor = params.Cursor
	}
	return c.routinesPage(ctx, params, cursor)
}

func (c *Client) routinesPage(ctx context.Context, params *ListRoutinesParams, cursor string) (*latere.Page[*Routine], error) {
	if params == nil || params.Agent == "" {
		return nil, errors.New("agents: ListRoutinesParams.Agent is required")
	}
	limit := params.Limit
	if limit <= 0 {
		limit = routinePage
	}
	page, err := c.triggersPage(ctx, &ListTriggersParams{Limit: limit}, cursor)
	if err != nil {
		return nil, err
	}
	out := &latere.Page[*Routine]{Items: []*Routine{}, NextCursor: page.NextCursor}
	for _, t := range page.Items {
		if r, ok := RoutineOf(t, params.Agent); ok {
			out.Items = append(out.Items, r)
		}
	}
	return out, nil
}

// GetRoutine returns the caller's routine of agent named name. A name that
// names no routine of agent returns an error matching latere.ErrNotFound:
// ErrNotRoutine for a trigger that is none, or the API's answer when
// there is no trigger of the name.
func (c *Client) GetRoutine(ctx context.Context, agent, name string) (*Routine, error) {
	_, r, err := c.readRoutine(ctx, agent, name)
	return r, err
}

// CreateRoutineParams describes a new routine.
type CreateRoutineParams struct {
	// Agent is the agent the routine's runs are sessions of, by name, such
	// as "latere-chat". Required. It must exist in the caller's context:
	// a program signed in as a person makes a managed agent exist with
	// account.ApplyManagedAgent first.
	Agent string
	// Title is the routine's name, one line of at most MaxRoutineTitle
	// characters. Required.
	Title string
	// Instruction is what the agent is asked to do on each run, of at most
	// MaxRoutineInstruction bytes, or MaxEventRoutineInstruction for a
	// routine that starts from a link or an email. Required. It is written
	// as literal text: a {{ in it is never read as a placeholder.
	Instruction string
	// Starts is how the routine starts; StartsOnSchedule when empty. The
	// platform issues the link or the address of a routine that starts
	// from one, once the routine exists.
	Starts RoutineStart
	// Schedule is when a routine on a schedule runs. Required for one.
	Schedule *RoutineSchedule
	// Dates are the times a routine on dates runs at, once each: 1 to
	// MaxRoutineDates distinct times, at least one of them after now.
	// Required for one. A single date is a routine that runs once.
	Dates []time.Time
	// DatesZone is the IANA time zone the person picked the dates in, such
	// as "Europe/Berlin"; UTC when empty. Each run comes at its date
	// whatever the zone: it is kept so the routine read back says the
	// dates in it.
	DatesZone string
	// Model is the model each run's session starts on, such as
	// "tier/thorough" for the Thorough Effort level; empty starts runs on
	// the agent's own model. It is sent as it is, for the API to check.
	Model string
	// Notify is whether every run brings its result to the person,
	// NotifyAlways, or only the runs the agent judges need them,
	// NotifyJudged, which takes an agent with the notify tool. Empty
	// writes none, which the API reads as NotifyAlways.
	Notify NotifyPolicy
	// Paused makes the routine paused from the start.
	Paused bool
	// PerWeekMicroUSD is the routine's weekly allowance, in millionths of
	// a US dollar: the most its runs may cost in a rolling 7 days, past
	// which the API starts no run until the oldest leave the 7 days. Zero
	// gives it none; below zero is refused.
	PerWeekMicroUSD int64
	// IdempotencyKey names the routine's trigger, RoutineName of it, so a
	// create sent again with the same key replaces the routine it made
	// instead of making a second; one is generated when it is empty.
	IdempotencyKey string
}

// CreateRoutine makes a routine and returns it as the API stored it. The
// title and the instruction are trimmed and checked before any request:
// a routine they do not make returns an error matching one of
// ErrRoutineTitleMissing, ErrRoutineTitleInvalid,
// ErrRoutineInstructionMissing, ErrRoutineInstructionTooLong,
// ErrRoutineScheduleInvalid, ErrRoutineDatesInvalid, ErrRoutineStartsInvalid,
// ErrRoutineNotifyInvalid or ErrAllowanceInvalid. The API may still refuse
// the manifest, such as a zone or a model it does not know, or one without
// an allowance where the installation requires one.
//
// A create sent again with the same IdempotencyKey writes the same trigger
// again, so it makes one routine however often it is sent.
func (c *Client) CreateRoutine(ctx context.Context, params *CreateRoutineParams) (*Routine, error) {
	if params == nil || params.Agent == "" {
		return nil, errors.New("agents: CreateRoutineParams.Agent is required")
	}
	fields := routine.Fields{Title: params.Title, Instruction: params.Instruction, Starts: params.Starts, Schedule: params.Schedule,
		Dates: params.Dates, DatesZone: params.DatesZone, Notify: string(params.Notify), Paused: params.Paused}
	if params.PerWeekMicroUSD != 0 {
		perWeek := params.PerWeekMicroUSD
		fields.PerWeekMicroUSD = &perWeek
	}
	if params.Model != "" {
		model := params.Model
		fields.Model = &model
	}
	f, err := routine.New(fields)
	if err != nil {
		return nil, err
	}
	key := params.IdempotencyKey
	if key == "" {
		key = latere.NewIdempotencyKey()
	}
	return c.applyRoutine(ctx, params.Agent, RoutineName(key), nil, f)
}

// ChangeRoutineParams is a change of a routine. A nil field keeps what
// the routine has.
type ChangeRoutineParams struct {
	// Title is the routine's new name.
	Title *string
	// Instruction is its new instruction, checked against the bound of how
	// the routine starts once the change is made.
	Instruction *string
	// Starts changes how it starts. A routine that comes to start on a
	// schedule needs Schedule, and one that comes to start on dates needs
	// Dates; one that comes to start another way leaves its schedule or
	// its dates.
	Starts *RoutineStart
	// Schedule is its new schedule, for a routine on a schedule. Nil keeps
	// the schedule it has, also one of a shape a routine does not write.
	Schedule *RoutineSchedule
	// Dates are its new dates, for a routine on dates, held to what
	// CreateRoutineParams.Dates takes; a date that has passed may stay
	// beside one to come, so a change can write back the Dates it read.
	// DatesZone is the IANA time zone the person picked them in, UTC when
	// empty. Nil keeps the dates and the zone it has; DatesZone is read
	// with Dates alone.
	Dates     []time.Time
	DatesZone string
	// Model is the model its runs' sessions start on, such as
	// "tier/thorough"; "" returns them to the agent's own model, and nil
	// keeps the model it has. A reasoning level set on the stored trigger
	// another way is kept while it has a model.
	Model *string
	// Notify changes whether every run brings its result to the person,
	// NotifyAlways, or only the runs the agent judges need them,
	// NotifyJudged. Empty keeps the one it has.
	Notify NotifyPolicy
	// Paused pauses or resumes it. A resumed routine runs from its next
	// time on.
	Paused *bool
	// PerWeekMicroUSD is its new weekly allowance, in millionths of a US
	// dollar; zero takes the allowance off, and below zero is refused. Nil
	// keeps the allowance it has.
	PerWeekMicroUSD *int64
}

// ChangeRoutine changes the caller's routine of agent named name and
// returns it as the API stored it. It reads the routine's trigger, writes
// the fields the change names over it, and applies it, so every other
// field of the trigger is kept as the API answered it, those this package
// does not model among them. A name that names no routine returns an error
// matching latere.ErrNotFound, and a change the routine cannot take one of
// CreateRoutine's errors, before the trigger is written.
//
// A change is read, then written: a change made elsewhere between the two
// is replaced by this one.
func (c *Client) ChangeRoutine(ctx context.Context, agent, name string, params *ChangeRoutineParams) (*Routine, error) {
	stored, r, err := c.readRoutine(ctx, agent, name)
	if err != nil {
		return nil, err
	}
	if params == nil {
		params = &ChangeRoutineParams{}
	}
	// The schedule is left nil, so the stored one is kept unless the change
	// names one, also one of a shape a routine does not write.
	f := routine.Fields{Title: r.Title, Instruction: r.Instruction, Starts: r.Starts, Paused: r.Paused}
	if params.Starts != nil {
		if err := routine.CheckStarts(*params.Starts); err != nil {
			return nil, err
		}
		f.Starts = *params.Starts
	}
	if f.Starts == StartsOnSchedule && r.Starts != StartsOnSchedule && params.Schedule == nil {
		return nil, fmt.Errorf("%w: a routine that comes to start on a schedule names one", ErrRoutineScheduleInvalid)
	}
	if f.Starts == StartsOnDates && r.Starts != StartsOnDates && params.Dates == nil {
		return nil, fmt.Errorf("%w: a routine that comes to start on dates names them", ErrRoutineDatesInvalid)
	}
	if params.Title != nil {
		if f.Title, err = routine.CheckTitle(*params.Title); err != nil {
			return nil, err
		}
	}
	// The instruction is held to the bound of how the routine starts once
	// the change is made, so one that keeps its instruction and comes to
	// start from a link is held to the shorter bound too.
	if params.Instruction != nil || f.Starts != r.Starts {
		text := f.Instruction
		if params.Instruction != nil {
			text = *params.Instruction
		}
		if f.Instruction, err = routine.CheckInstruction(text, f.Starts); err != nil {
			return nil, err
		}
	}
	if params.Schedule != nil && f.Starts == StartsOnSchedule {
		s, err := routine.CheckSchedule(*params.Schedule)
		if err != nil {
			return nil, err
		}
		f.Schedule = &s
	}
	// The dates are left nil unless the change names them, so the stored
	// ones are kept with their zone, and are not checked again: a change
	// of the name of a routine whose last date has passed while its run
	// waits is no change of its dates.
	if params.Dates != nil && f.Starts == StartsOnDates {
		if f.Dates, err = routine.CheckDates(params.Dates, params.DatesZone, time.Now()); err != nil {
			return nil, err
		}
		f.DatesZone = params.DatesZone
	}
	if params.Paused != nil {
		f.Paused = *params.Paused
	}
	// The model and the way of telling are left unset unless the change
	// names them, so the stored ones are kept as written.
	if params.Model != nil {
		model := *params.Model
		f.Model = &model
	}
	if params.Notify != "" {
		if err := routine.CheckNotify(string(params.Notify)); err != nil {
			return nil, err
		}
		f.Notify = string(params.Notify)
	}
	// The allowance is left nil unless the change names one, so the stored
	// one is kept.
	if params.PerWeekMicroUSD != nil {
		if err := routine.CheckAllowance(*params.PerWeekMicroUSD); err != nil {
			return nil, err
		}
		perWeek := *params.PerWeekMicroUSD
		f.PerWeekMicroUSD = &perWeek
	}
	// The trigger was read from the API, so its JSON is the API's whole
	// answer.
	return c.applyRoutine(ctx, agent, name, stored.RawJSON(), f)
}

// DeleteRoutine deletes the caller's routine of agent named name, after
// reading that its trigger is a routine of agent; the sessions its runs
// started are kept. A name that names no routine returns an error
// matching latere.ErrNotFound, and nothing is deleted.
func (c *Client) DeleteRoutine(ctx context.Context, agent, name string) error {
	if _, _, err := c.readRoutine(ctx, agent, name); err != nil {
		return err
	}
	return c.DeleteTrigger(ctx, name)
}

// RunRoutineParams is a run of a routine a program asks for.
type RunRoutineParams struct {
	// IdempotencyKey makes a repeated run answer the first run's firing
	// instead of running again, so a second press of a button runs the
	// routine once; one is generated when it is empty.
	IdempotencyKey string
}

// RunRoutine runs the caller's routine of agent named name now, outside its
// schedule or its dates, and returns the firing, whose SessionID is the
// session the run started. The agent must exist in the caller's context,
// at the version the run should start: a program signed in as a person
// applies a managed agent with account.ApplyManagedAgent first.
//
// A paused routine returns an error matching ErrRoutinePaused, and one
// that starts from a link or an email one matching
// ErrRoutineStartsOnArrival, without firing. A routine paused elsewhere
// since it was read is refused by the API with an error matching both
// ErrRoutinePaused and latere.ErrConflict. A run whose session failed to
// start returns the firing and the API's error, as FireTrigger does.
//
// RunRoutine is sent once, never again after a failure, as FireTrigger
// is: pass the same IdempotencyKey to run it again yourself.
func (c *Client) RunRoutine(ctx context.Context, agent, name string, params *RunRoutineParams) (*Firing, error) {
	_, r, err := c.readRoutine(ctx, agent, name)
	if err != nil {
		return nil, err
	}
	switch {
	case r.Starts == StartsFromLink, r.Starts == StartsFromEmail:
		return nil, fmt.Errorf("%w: %s starts from a %s", ErrRoutineStartsOnArrival, name, r.Starts)
	case r.Paused:
		return nil, fmt.Errorf("%w: %s", ErrRoutinePaused, name)
	}
	fire := &FireTriggerParams{}
	if params != nil {
		fire.IdempotencyKey = params.IdempotencyKey
	}
	fired, err := c.FireTrigger(ctx, name, fire)
	var apiErr *latere.Error
	if err != nil && fired == nil && errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusConflict {
		// The API refuses a firing of a suspended trigger as a conflict.
		return nil, &codeError{err: apiErr, sentinel: ErrRoutinePaused}
	}
	return fired, err
}
