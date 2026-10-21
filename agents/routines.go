// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"latere.ai/x/sdk-go/latere"
)

// A routine is an instruction a person hands an agent once, which the
// agent then carries out again and again with nobody waiting: on a
// schedule, or each time another app sends to the routine's private link
// or an email reaches its address. A routine is a trigger of the person's
// own, written in one shape, so every program that shows a person's
// routines reads and changes the same triggers: Latere Chat's routines are
// those of the agent the platform manages for it, "latere-chat", and Latere
// Studio lists the same ones. The routine methods write and read that
// shape over the trigger endpoints; the schedule, the firing and the
// session a run starts are the API's.

// The marks and bounds of a routine.
const (
	// RoutineLabel is the label a routine's trigger carries, its value the
	// name of the agent whose routine it is. Only a trigger with the label
	// reads as a routine, so a trigger made another way, in the developer
	// console for instance, is never listed, changed or deleted as one.
	RoutineLabel = "applied-by"
	// RoutinePrefix starts the name of every routine's trigger; 20
	// lowercase hexadecimal digits follow it.
	RoutinePrefix = "routine-"
	// MaxRoutineTitle is the most characters a routine's name holds.
	MaxRoutineTitle = 60
	// MaxRoutineInstruction is the most bytes the instruction of a routine
	// on a schedule holds: the longest message a firing renders.
	MaxRoutineInstruction = 64 << 10
	// MaxEventRoutineInstruction is the most bytes the instruction of a
	// routine that starts from a link or an email holds. What arrives is
	// written into the same message after it, each value cut at 16 KiB and
	// the message refused past MaxRoutineInstruction, so an instruction of
	// this length and everything that arrives fit together.
	MaxEventRoutineInstruction = 16 << 10
)

// routineHex is how many hexadecimal digits follow RoutinePrefix.
const routineHex = 20

// routineNamePattern is the shape of a routine's trigger name. A name goes
// into the path of a request, so nothing else is sent.
var routineNamePattern = regexp.MustCompile(`^` + RoutinePrefix + `[0-9a-f]{20}$`)

// zonePattern is the shape of an IANA time zone name, as the API checks a
// trigger's timeZone. Whether the zone exists is the API's to say.
var zonePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_+-]*(/[A-Za-z0-9_+-]+)*$`)

// cronPattern is the one shape of schedule a routine writes: a minute and
// an hour of every day, or of the days of the week listed, Sunday 0 (and
// 7, as cron also reads it).
var cronPattern = regexp.MustCompile(`^([0-9]{1,2}) ([0-9]{1,2}) \* \* (\*|[0-7](?:,[0-7])*)$`)

// RoutineStart is how a routine starts.
type RoutineStart string

// The ways a routine starts.
const (
	// StartsOnSchedule runs the routine on the days and at the time of its
	// RoutineSchedule.
	StartsOnSchedule RoutineStart = "schedule"
	// StartsFromLink runs the routine each time another app sends to its
	// private link, which the platform issues; what the app sent follows
	// the instruction in the run's message.
	StartsFromLink RoutineStart = "link"
	// StartsFromEmail runs the routine each time an email reaches its
	// address, which the platform issues; the email follows the
	// instruction in the run's message.
	StartsFromEmail RoutineStart = "email"
)

// The product, and its verb, of the events a routine that starts from a
// link or an email fires on.
const (
	productWebhook = "webhook"
	productMail    = "mail"
	verbReceived   = "received"
)

// The words after the instruction in the message of a routine that starts
// from a link or an email: what arrived, read through the trigger's event
// placeholders. They are for the model, so they are in English whatever
// the person's language; a routine is read back by taking them off.
const (
	linkSuffix  = "\n\nWhat the other app sent:\n{{event.payload.body}}"
	emailSuffix = "\n\nThe email that started this run:\nFrom: {{event.actor}}\nSubject: {{event.payload.subject}}\n\n{{event.payload.text}}\n\nAttachments: {{event.payload.attachments}}"
)

// suffixOf is the words after the instruction of a routine that starts as
// starts, none for one on a schedule.
func suffixOf(starts RoutineStart) string {
	switch starts {
	case StartsFromLink:
		return linkSuffix
	case StartsFromEmail:
		return emailSuffix
	}
	return ""
}

// weekOrder is the order a routine lists its days in, Monday first.
var weekOrder = []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday}

// A RoutineSchedule is when a routine on a schedule runs: on the days
// listed, at a time of day, in a time zone.
type RoutineSchedule struct {
	// Days are the days of the week it runs on; all seven is every day. A
	// routine read back lists them in week order, Monday first.
	Days []time.Weekday
	// Hour and Minute are the time of day it runs at, on a 24-hour clock.
	Hour, Minute int
	// Zone is the IANA time zone the time is read in, such as
	// "Europe/Berlin". It is checked for its shape alone; whether the zone
	// exists is the API's to say.
	Zone string
}

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
	// routine that starts from a link or an email, and for one whose
	// schedule has a shape a routine does not write, set another way.
	Schedule *RoutineSchedule
	// Paused says the routine runs nothing until it is resumed.
	Paused bool
	// NextRunAt is when a routine on a schedule runs next; nil while it is
	// paused, and for one that starts from a link or an email.
	NextRunAt *time.Time
	// LastRunAt is when it last ran, and LastSessionID the session that
	// run started; nil and empty before its first run.
	LastRunAt     *time.Time
	LastSessionID string
	// CreatedAt is when the routine was made.
	CreatedAt time.Time

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
func RoutineName(key string) string {
	sum := sha256.Sum256([]byte(key))
	return RoutinePrefix + hex.EncodeToString(sum[:])[:routineHex]
}

// escapeTemplate writes text as a literal trigger template: every {{ is
// written \{{, so nothing a person writes is read as a placeholder.
func escapeTemplate(s string) string { return strings.ReplaceAll(s, "{{", `\{{`) }

// unescapeTemplate reads a literal template back as the text it renders.
func unescapeTemplate(s string) string { return strings.ReplaceAll(s, `\{{`, "{{") }

// cronOf is the schedule a routine writes: the minute and the hour, and
// the days' numbers in ascending order, Sunday 0, or * for all seven.
func cronOf(s RoutineSchedule) string {
	days := "*"
	if len(s.Days) < len(weekOrder) {
		var nums []string
		for d := time.Sunday; d <= time.Saturday; d++ {
			if slices.Contains(s.Days, d) {
				nums = append(nums, strconv.Itoa(int(d)))
			}
		}
		days = strings.Join(nums, ",")
	}
	return fmt.Sprintf("%d %d * * %s", s.Minute, s.Hour, days)
}

// scheduleOf reads a schedule a routine wrote back into its days and time,
// in zone, UTC when empty. A schedule of any other shape reads as none.
func scheduleOf(cron, zone string) *RoutineSchedule {
	m := cronPattern.FindStringSubmatch(cron)
	if m == nil {
		return nil
	}
	// The pattern admits one or two digits alone, so neither fails.
	minute, _ := strconv.Atoi(m[1])
	hour, _ := strconv.Atoi(m[2])
	if minute > 59 || hour > 23 {
		return nil
	}
	if zone == "" {
		zone = "UTC"
	}
	days := slices.Clone(weekOrder)
	if m[3] != "*" {
		on := map[time.Weekday]bool{}
		for n := range strings.SplitSeq(m[3], ",") {
			// The pattern admits the digits 0 to 7 alone.
			d, _ := strconv.Atoi(n)
			on[time.Weekday(d%7)] = true
		}
		days = slices.DeleteFunc(days, func(d time.Weekday) bool { return !on[d] })
	}
	return &RoutineSchedule{Days: days, Hour: hour, Minute: minute, Zone: zone}
}

// RoutineOf reads t as a routine of agent, and reports false when it is
// none: a trigger without the RoutineLabel of agent, or whose name is not
// a routine's. The instruction and the title are read as the person wrote
// them, without the words a routine that starts from a link or an email
// adds after its instruction.
func RoutineOf(t *Trigger, agent string) (*Routine, bool) {
	if t == nil || agent == "" || t.Metadata.Labels[RoutineLabel] != agent || !routineNamePattern.MatchString(t.Metadata.Name) {
		return nil, false
	}
	title := t.Metadata.DisplayName
	if title == "" {
		title = unescapeTemplate(t.Spec.Session.Title)
	}
	starts := StartsOnSchedule
	if on := t.Spec.On; on != nil {
		switch on.Product {
		case productWebhook:
			starts = StartsFromLink
		case productMail:
			starts = StartsFromEmail
		}
		// An event of another product, set another way, is no way a routine
		// starts: it reads as a schedule set elsewhere, which a change that
		// names a schedule replaces.
	}
	message := t.Spec.Session.Message
	if suffix := suffixOf(starts); suffix != "" {
		message = strings.TrimSuffix(message, suffix)
	}
	r := &Routine{
		Name: t.Metadata.Name, TriggerID: t.Status.ID, Agent: agent, Title: title, Instruction: unescapeTemplate(message),
		Starts: starts, Paused: t.Spec.Suspend, LastRunAt: t.Status.LastFiredAt, LastSessionID: t.Status.LastSessionID,
		CreatedAt: t.Status.CreatedAt, raw: t.raw,
	}
	if t.Spec.Schedule != "" {
		r.Schedule = scheduleOf(t.Spec.Schedule, t.Spec.TimeZone)
	}
	if !r.Paused && starts == StartsOnSchedule {
		r.NextRunAt = t.Status.NextFireAt
	}
	return r, true
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
	ErrRoutineTitleMissing = errors.New("agents: the routine has no title")
	// ErrRoutineTitleInvalid matches a name longer than MaxRoutineTitle
	// characters, or holding a control character, a line or paragraph
	// separator, or a bidirectional control.
	ErrRoutineTitleInvalid = errors.New("agents: the routine's title is not one short line")
	// ErrRoutineInstructionMissing matches a routine without an
	// instruction.
	ErrRoutineInstructionMissing = errors.New("agents: the routine has no instruction")
	// ErrRoutineInstructionTooLong matches an instruction past the bound of
	// how the routine starts: MaxRoutineInstruction, or
	// MaxEventRoutineInstruction for one that starts from a link or an
	// email.
	ErrRoutineInstructionTooLong = errors.New("agents: the routine's instruction is too long")
	// ErrRoutineScheduleInvalid matches a routine on a schedule without
	// one, or a schedule with no day, a day, hour or minute out of range,
	// or a zone that is not an IANA name's shape.
	ErrRoutineScheduleInvalid = errors.New("agents: the routine's schedule is not one a routine runs on")
	// ErrRoutineStartsInvalid matches a RoutineStart that is none of
	// StartsOnSchedule, StartsFromLink and StartsFromEmail.
	ErrRoutineStartsInvalid = errors.New("agents: the routine starts in no way a routine starts")
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
	if !routineNamePattern.MatchString(name) {
		return fmt.Errorf("%w: %q is not a routine's name", ErrNotRoutine, name)
	}
	return nil
}

// checkTitle answers the name trimmed, or why it is none.
func checkTitle(s string) (string, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return "", ErrRoutineTitleMissing
	case utf8.RuneCountInString(s) > MaxRoutineTitle:
		return "", fmt.Errorf("%w: it is longer than %d characters", ErrRoutineTitleInvalid, MaxRoutineTitle)
	case strings.ContainsFunc(s, func(r rune) bool {
		return unicode.IsControl(r) || unicode.In(r, unicode.Zl, unicode.Zp, unicode.Bidi_Control)
	}):
		return "", fmt.Errorf("%w: it holds a control character or a line break", ErrRoutineTitleInvalid)
	}
	return s, nil
}

// checkInstruction answers the instruction trimmed, or why it is none or
// too long for a routine that starts as starts.
func checkInstruction(s string, starts RoutineStart) (string, error) {
	s = strings.TrimSpace(s)
	bound := MaxRoutineInstruction
	if starts != StartsOnSchedule {
		bound = MaxEventRoutineInstruction
	}
	switch {
	case s == "":
		return "", ErrRoutineInstructionMissing
	case len(s) > bound:
		return "", fmt.Errorf("%w: %d bytes, past %d", ErrRoutineInstructionTooLong, len(s), bound)
	}
	return s, nil
}

// checkSchedule answers the schedule with its days in week order, each
// once, or why it is not one.
func checkSchedule(s RoutineSchedule) (RoutineSchedule, error) {
	if len(s.Days) == 0 {
		return RoutineSchedule{}, fmt.Errorf("%w: it names no day", ErrRoutineScheduleInvalid)
	}
	for _, d := range s.Days {
		if d < time.Sunday || d > time.Saturday {
			return RoutineSchedule{}, fmt.Errorf("%w: %d is no day of the week", ErrRoutineScheduleInvalid, int(d))
		}
	}
	if s.Hour < 0 || s.Hour > 23 || s.Minute < 0 || s.Minute > 59 {
		return RoutineSchedule{}, fmt.Errorf("%w: %d:%02d is no time of day", ErrRoutineScheduleInvalid, s.Hour, s.Minute)
	}
	if !zonePattern.MatchString(s.Zone) {
		return RoutineSchedule{}, fmt.Errorf("%w: the zone %q is not an IANA time zone name", ErrRoutineScheduleInvalid, s.Zone)
	}
	days := slices.DeleteFunc(slices.Clone(weekOrder), func(d time.Weekday) bool { return !slices.Contains(s.Days, d) })
	return RoutineSchedule{Days: days, Hour: s.Hour, Minute: s.Minute, Zone: s.Zone}, nil
}

// checkStarts refuses a RoutineStart that is none of the three.
func checkStarts(s RoutineStart) error {
	switch s {
	case StartsOnSchedule, StartsFromLink, StartsFromEmail:
		return nil
	}
	return fmt.Errorf("%w: %q", ErrRoutineStartsInvalid, string(s))
}

// routineFields are what a routine owns of its trigger.
type routineFields struct {
	title, instruction string
	starts             RoutineStart
	// schedule is nil where the stored schedule, of a shape set elsewhere,
	// is kept as it is.
	schedule *RoutineSchedule
	paused   bool
}

// member returns the object m holds under key, adding an empty one when
// it holds none.
func member(m map[string]any, key string) (map[string]any, error) {
	switch v := m[key].(type) {
	case map[string]any:
		return v, nil
	case nil:
		o := map[string]any{}
		m[key] = o
		return o, nil
	default:
		return nil, fmt.Errorf("agents: the trigger's %s is not an object", key)
	}
}

// routineManifest is the manifest of the routine of agent named name: the
// stored trigger's JSON as the API answered it, empty for a new routine,
// with the fields a routine owns written over it and its status left out.
// Everything else the stored trigger holds, its other labels, its
// annotations and the spec's other fields, those this package does not
// model among them, is sent back as it was, so a change undoes nothing set
// another way.
func routineManifest(agent, name string, stored json.RawMessage, f routineFields) (json.RawMessage, error) {
	m := map[string]any{}
	if len(stored) > 0 {
		dec := json.NewDecoder(bytes.NewReader(stored))
		// Numbers stay as they were written, so a large one is not rounded
		// to a float's precision.
		dec.UseNumber()
		if err := dec.Decode(&m); err != nil {
			return nil, fmt.Errorf("agents: the stored trigger %s is not a JSON object: %w", name, err)
		}
	}
	delete(m, "status")
	m["apiVersion"], m["kind"] = TriggerAPIVersion, TriggerKind
	meta, err := member(m, "metadata")
	if err != nil {
		return nil, err
	}
	labels, err := member(meta, "labels")
	if err != nil {
		return nil, err
	}
	labels[RoutineLabel] = agent
	meta["name"], meta["displayName"] = name, f.title
	spec, err := member(m, "spec")
	if err != nil {
		return nil, err
	}
	session, err := member(spec, "session")
	if err != nil {
		return nil, err
	}
	// The instruction is literal; what arrives is read through the words a
	// routine that starts from a link or an email adds after it.
	session["message"], session["title"] = escapeTemplate(f.instruction)+suffixOf(f.starts), escapeTemplate(f.title)
	// The agent by name: the API reads it in the caller's context.
	spec["agent"] = agent
	switch {
	case f.starts != StartsOnSchedule:
		product := productWebhook
		if f.starts == StartsFromEmail {
			product = productMail
		}
		// An event trigger of the platform's product, which the link or the
		// address delivers to. Its session's key is the API's default, the
		// event's resource.
		spec["on"] = map[string]any{"product": product, "verbs": []string{verbReceived}}
		delete(spec, "schedule")
		delete(spec, "timeZone")
		delete(session, "key")
	case f.schedule != nil:
		spec["schedule"], spec["timeZone"] = cronOf(*f.schedule), f.schedule.Zone
		delete(spec, "on")
	}
	// Every run of a routine shares one session key, so with the API's
	// default a run waiting on the person would skip every later run until
	// they answered.
	spec["skipIfActive"] = false
	if f.paused {
		spec["suspend"] = true
	} else {
		delete(spec, "suspend")
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("agents: the routine %s could not be written: %w", name, err)
	}
	return b, nil
}

// errNoAgent is returned before any request when a routine call names no
// agent.
var errNoAgent = errors.New("agents: the routine's agent is empty")

// applyRoutine writes the manifest of the routine of agent named name and
// answers the routine as the API stored it.
func (c *Client) applyRoutine(ctx context.Context, agent, name string, stored json.RawMessage, f routineFields) (*Routine, error) {
	manifest, err := routineManifest(agent, name, stored, f)
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
	// Paused makes the routine paused from the start.
	Paused bool
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
// ErrRoutineScheduleInvalid or ErrRoutineStartsInvalid. The API may still
// refuse the manifest, such as a zone it does not know.
//
// A create sent again with the same IdempotencyKey writes the same trigger
// again, so it makes one routine however often it is sent.
func (c *Client) CreateRoutine(ctx context.Context, params *CreateRoutineParams) (*Routine, error) {
	if params == nil || params.Agent == "" {
		return nil, errors.New("agents: CreateRoutineParams.Agent is required")
	}
	starts := params.Starts
	if starts == "" {
		starts = StartsOnSchedule
	}
	if err := checkStarts(starts); err != nil {
		return nil, err
	}
	title, err := checkTitle(params.Title)
	if err != nil {
		return nil, err
	}
	instruction, err := checkInstruction(params.Instruction, starts)
	if err != nil {
		return nil, err
	}
	f := routineFields{title: title, instruction: instruction, starts: starts, paused: params.Paused}
	if starts == StartsOnSchedule {
		if params.Schedule == nil {
			return nil, fmt.Errorf("%w: a routine on a schedule names one", ErrRoutineScheduleInvalid)
		}
		s, err := checkSchedule(*params.Schedule)
		if err != nil {
			return nil, err
		}
		f.schedule = &s
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
	// schedule needs Schedule; one that comes to start from a link or an
	// email leaves its schedule.
	Starts *RoutineStart
	// Schedule is its new schedule, for a routine on a schedule. Nil keeps
	// the schedule it has, also one of a shape a routine does not write.
	Schedule *RoutineSchedule
	// Paused pauses or resumes it. A resumed routine runs from its next
	// time on.
	Paused *bool
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
	f := routineFields{title: r.Title, instruction: r.Instruction, starts: r.Starts, paused: r.Paused}
	if params.Starts != nil {
		if err := checkStarts(*params.Starts); err != nil {
			return nil, err
		}
		f.starts = *params.Starts
	}
	if f.starts == StartsOnSchedule && r.Starts != StartsOnSchedule && params.Schedule == nil {
		return nil, fmt.Errorf("%w: a routine that comes to start on a schedule names one", ErrRoutineScheduleInvalid)
	}
	if params.Title != nil {
		if f.title, err = checkTitle(*params.Title); err != nil {
			return nil, err
		}
	}
	// The instruction is held to the bound of how the routine starts once
	// the change is made, so one that keeps its instruction and comes to
	// start from a link is held to the shorter bound too.
	if params.Instruction != nil || f.starts != r.Starts {
		text := f.instruction
		if params.Instruction != nil {
			text = *params.Instruction
		}
		if f.instruction, err = checkInstruction(text, f.starts); err != nil {
			return nil, err
		}
	}
	if params.Schedule != nil && f.starts == StartsOnSchedule {
		s, err := checkSchedule(*params.Schedule)
		if err != nil {
			return nil, err
		}
		f.schedule = &s
	}
	if params.Paused != nil {
		f.paused = *params.Paused
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
// schedule, and returns the firing, whose SessionID is the session the
// run started. The agent must exist in the caller's context, at the
// version the run should start: a program signed in as a person applies a
// managed agent with account.ApplyManagedAgent first.
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
	case r.Starts != StartsOnSchedule:
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
