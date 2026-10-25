// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package routine holds the shape of a routine's trigger, and nothing that
// sends it: the label and the name the trigger carries, the name made from
// a create's key, the manifest's fields, its weekly allowance, model and
// way of telling the person among them, reading a trigger back as a
// routine, and the checks a routine's title, instruction, schedule,
// dates, allowance and way of telling must pass. It also holds the shape of the
// trigger an agent's offer on events no routine starts from is written as:
// its name, the words after its ask, and reading it back.
//
// A program that keeps a person's routines uses this shape through package
// agents, which names a routine's types and constants, writes and reads
// routines over the trigger endpoints, and reads both shapes as a person's
// watches. This package is for code that writes or reads the shape
// without calling the Latere API, such as a server that applies what an
// agent offered once its owner said yes, so every program reads one
// definition. It imports the standard library alone and opens no
// connection.
package routine

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// The marks and bounds of a routine.
const (
	// Label is the label a routine's trigger carries, its value the name of
	// the agent whose routine it is. Only a trigger with the label reads as
	// a routine, so a trigger made another way is never listed, changed or
	// deleted as one.
	Label = "applied-by"
	// Prefix starts the name of every routine's trigger; 20 lowercase
	// hexadecimal digits follow it.
	Prefix = "routine-"
	// MaxTitle is the most characters a routine's name holds.
	MaxTitle = 60
	// MaxInstruction is the most bytes the instruction of a routine on a
	// schedule or on dates holds: the longest message a firing renders.
	MaxInstruction = 64 << 10
	// MaxEventInstruction is the most bytes the instruction of a routine
	// that starts from a link or an email holds. What arrives is written
	// into the same message after it, each value cut at 16 KiB and the
	// message refused past MaxInstruction, so an instruction of this length
	// and everything that arrives fit together.
	MaxEventInstruction = 16 << 10
	// MaxDates is the most dates a routine on dates runs at, the most
	// times the API takes in a trigger's spec.times.
	MaxDates = 100
)

// The trigger manifest's kind and version a routine is written as.
const (
	apiVersion = "topos.latere.ai/v1"
	kind       = "Trigger"
)

// nameHex is how many hexadecimal digits follow Prefix.
const nameHex = 20

// namePattern is the shape of a routine's trigger name. A name goes into
// the path of a request, so nothing else is sent.
var namePattern = regexp.MustCompile(`^` + Prefix + `[0-9a-f]{20}$`)

// zonePattern is the shape of an IANA time zone name, as the API checks a
// trigger's timeZone. Whether the zone exists is the API's to say.
var zonePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_+-]*(/[A-Za-z0-9_+-]+)*$`)

// cronPattern is the one shape of schedule a routine writes: a minute and
// an hour of every day, or of the days of the week listed, Sunday 0 (and
// 7, as cron also reads it).
var cronPattern = regexp.MustCompile(`^([0-9]{1,2}) ([0-9]{1,2}) \* \* (\*|[0-7](?:,[0-7])*)$`)

// Start is how a routine starts.
type Start string

// The ways a routine starts.
const (
	// OnSchedule runs the routine on the days and at the time of its
	// Schedule.
	OnSchedule Start = "schedule"
	// FromLink runs the routine each time another app sends to its private
	// link, which the platform issues; what the app sent follows the
	// instruction in the run's message.
	FromLink Start = "link"
	// FromEmail runs the routine each time an email reaches its address,
	// which the platform issues; the email follows the instruction in the
	// run's message.
	FromEmail Start = "email"
	// OnDates runs the routine once at each of its Dates, times a schedule
	// cannot say, written as the trigger's spec.times. The API deletes the
	// trigger after the last; the sessions the runs started keep what they
	// did.
	OnDates Start = "dates"
)

// The ways a trigger's spec.session.notify tells the person what a run
// did, as the API takes them.
const (
	notifyAlways = "always"
	notifyJudged = "judged"
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
func suffixOf(starts Start) string {
	switch starts {
	case FromLink:
		return linkSuffix
	case FromEmail:
		return emailSuffix
	}
	return ""
}

// weekOrder is the order a routine lists its days in, Monday first.
var weekOrder = []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday}

// A Schedule is when a routine on a schedule runs: on the days listed, at
// a time of day, in a time zone.
type Schedule struct {
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

// Fields are what a routine owns of its trigger.
type Fields struct {
	// Title is the routine's name, as a person reads it, and the title of
	// each session a run starts.
	Title string
	// Instruction is what the agent is asked to do on each run, as the
	// person wrote it. It is written as literal text: a {{ in it is never
	// read as a placeholder.
	Instruction string
	// Starts is how the routine starts.
	Starts Start
	// Schedule is when a routine on a schedule runs. Nil, for a routine on
	// a schedule, keeps the schedule its stored trigger has, one of a shape
	// a routine does not write among them.
	Schedule *Schedule
	// Dates are the times a routine on dates runs at, once each, the
	// trigger's spec.times: 1 to MaxDates distinct times, at least one of
	// them after now when they are written. A date that has passed may
	// stay in the list and never runs, so a change can write back the
	// dates it read. Nil, for a routine on dates, keeps the dates and the
	// zone its stored trigger has. Read and CheckDates list them in
	// ascending order.
	Dates []time.Time
	// DatesZone is the IANA time zone the person picked the dates in, such
	// as "Europe/Berlin", written with Dates as the trigger's
	// spec.timeZone. Each run comes at its date whatever the zone; the zone
	// is kept so a program can say the dates back to the person as they
	// picked them. Empty is UTC. It is checked for its shape alone.
	DatesZone string
	// Model is the model each run's session starts on, the trigger's
	// spec.session.model.name, such as a routed name of an Effort level.
	// Nil keeps the model the stored trigger names; "" takes the model
	// off, so runs start on the agent's own; any other name is written
	// over the stored one, keeping a reasoning level the stored trigger
	// names. Read sets it to the trigger's model, nil for a trigger that
	// names none.
	Model *string
	// Notify is whether each run brings its result to the person, the
	// trigger's spec.session.notify: "always" for every run, or "judged"
	// for the runs the agent decides need them, which takes an agent with
	// the notify tool. Empty keeps what the stored trigger has, which is
	// "always" for a trigger that names none. Read sets it to the
	// trigger's, empty for a trigger that names none.
	Notify string
	// Paused says the routine runs nothing until it is resumed.
	Paused bool
	// PerWeekMicroUSD is the routine's weekly allowance, the trigger's
	// spec.budget.perWeek, in millionths of a US dollar: the most the runs
	// counted toward it may cost in a rolling 7 days. Once they have, the
	// API starts no run until the oldest leave the 7 days. Nil keeps the
	// allowance the stored trigger has, none for a new routine, and zero
	// takes it off. Read sets it to the trigger's allowance, nil for a
	// trigger with none, so a shape that does not carry the allowance
	// keeps it through a change.
	PerWeekMicroUSD *int64
}

// Name returns the name of the trigger a routine made with key is written
// under: Prefix and the first 20 hexadecimal digits of the key's SHA-256.
// The same key always names the same trigger, so a create sent again with
// its key replaces the routine it made rather than making a second.
func Name(key string) string {
	sum := sha256.Sum256([]byte(key))
	return Prefix + hex.EncodeToString(sum[:])[:nameHex]
}

// IsName reports whether name has the shape of a routine's trigger name.
func IsName(name string) bool { return namePattern.MatchString(name) }

// escapeTemplate writes text as a literal trigger template: every {{ is
// written \{{, so nothing a person writes is read as a placeholder.
func escapeTemplate(s string) string { return strings.ReplaceAll(s, "{{", `\{{`) }

// unescapeTemplate reads a literal template back as the text it renders.
func unescapeTemplate(s string) string { return strings.ReplaceAll(s, `\{{`, "{{") }

// Cron returns the schedule a routine writes for s: the minute and the
// hour, and the days' numbers in ascending order, Sunday 0, or * for all
// seven.
func Cron(s Schedule) string {
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

// ParseSchedule reads a schedule of the shape a routine writes, a minute
// and an hour of every day or of the days listed, into its days and time,
// in zone, UTC when empty. It returns nil for a schedule of any other
// shape, which no routine writes.
func ParseSchedule(cron, zone string) *Schedule {
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
	return &Schedule{Days: days, Hour: hour, Minute: minute, Zone: zone}
}

// Trigger is what a routine reads of its trigger: its name, display name
// and labels, its schedule, its time or the product of the events it fires
// on, and its session's message, title, model and way of telling.
type Trigger struct {
	Name, DisplayName string
	Labels            map[string]string
	// Schedule and TimeZone are the trigger's spec.schedule and
	// spec.timeZone, and Product the product of its spec.on, "" for a
	// trigger on a schedule.
	Schedule, TimeZone, Product string
	// Times is its spec.times, nil for a trigger that does not fire at a
	// list of times.
	Times []time.Time
	// Message and Title are its spec.session's.
	Message, Title string
	// Model is the name of its spec.session.model, and Notify its
	// spec.session.notify; "" for a trigger that names none.
	Model, Notify string
	Suspend       bool
	// PerWeekMicroUSD is its spec.budget.perWeek, its weekly allowance in
	// millionths of a US dollar; zero for a trigger with none.
	PerWeekMicroUSD int64
}

// Read reads t as a routine of agent, and reports false when it is none: a
// trigger without the Label of agent, or whose name is not a routine's.
// The instruction and the title are read as the person wrote them,
// without the words a routine that starts from a link or an email adds
// after its instruction. A trigger with Times reads as a routine on
// dates, its dates in ascending order and its DatesZone the trigger's
// zone, UTC when it names none. Schedule is nil for a routine that starts
// from a link or an email or on dates, and for one whose schedule has a
// shape a routine does not write, set another way.
func Read(t Trigger, agent string) (Fields, bool) {
	if agent == "" || t.Labels[Label] != agent || !IsName(t.Name) {
		return Fields{}, false
	}
	title := t.DisplayName
	if title == "" {
		title = unescapeTemplate(t.Title)
	}
	starts := OnSchedule
	switch {
	case len(t.Times) > 0:
		// The API holds a trigger to one of a schedule, events, a time and
		// a list of times.
		starts = OnDates
	case t.Product == productWebhook:
		starts = FromLink
	case t.Product == productMail:
		starts = FromEmail
	}
	// An event of another product, set another way, is no way a routine
	// starts: it reads as a schedule set elsewhere, which a change that
	// names a schedule replaces.
	message := t.Message
	if suffix := suffixOf(starts); suffix != "" {
		message = strings.TrimSuffix(message, suffix)
	}
	f := Fields{Title: title, Instruction: unescapeTemplate(message), Starts: starts, Paused: t.Suspend, Notify: t.Notify}
	if t.PerWeekMicroUSD > 0 {
		perWeek := t.PerWeekMicroUSD
		f.PerWeekMicroUSD = &perWeek
	}
	if t.Model != "" {
		model := t.Model
		f.Model = &model
	}
	switch {
	case starts == OnDates:
		f.Dates, f.DatesZone = slices.SortedFunc(slices.Values(t.Times), time.Time.Compare), t.TimeZone
		if f.DatesZone == "" {
			f.DatesZone = "UTC"
		}
	case t.Schedule != "":
		f.Schedule = ParseSchedule(t.Schedule, t.TimeZone)
	}
	return f, true
}

// The errors of the checks, each matched with errors.Is and returned
// wrapped with what was wrong.
var (
	// ErrTitleMissing matches a routine without a name.
	ErrTitleMissing = errors.New("routine: the routine has no title")
	// ErrTitleInvalid matches a name longer than MaxTitle characters, or
	// holding a control character, a line or paragraph separator, or a
	// bidirectional control.
	ErrTitleInvalid = errors.New("routine: the routine's title is not one short line")
	// ErrInstructionMissing matches a routine without an instruction.
	ErrInstructionMissing = errors.New("routine: the routine has no instruction")
	// ErrInstructionTooLong matches an instruction past the bound of how
	// the routine starts: MaxInstruction, or MaxEventInstruction for one
	// that starts from a link or an email.
	ErrInstructionTooLong = errors.New("routine: the routine's instruction is too long")
	// ErrScheduleInvalid matches a routine on a schedule without one, or a
	// schedule with no day, a day, hour or minute out of range, or a zone
	// that is not an IANA name's shape.
	ErrScheduleInvalid = errors.New("routine: the routine's schedule is not one a routine runs on")
	// ErrStartsInvalid matches a Start that is none of OnSchedule, FromLink,
	// FromEmail and OnDates.
	ErrStartsInvalid = errors.New("routine: the routine starts in no way a routine starts")
	// ErrDatesInvalid matches a routine on dates without one, with more
	// than MaxDates, with the same time twice, with none after now, which
	// the API refuses too, or with a zone that is not an IANA name's shape.
	ErrDatesInvalid = errors.New("routine: the routine's dates are not ones a routine runs at")
	// ErrAllowanceInvalid matches a weekly allowance below zero. Zero is
	// none: it takes an allowance off.
	ErrAllowanceInvalid = errors.New("routine: the weekly allowance is below zero")
	// ErrNotifyInvalid matches a way of telling the person that is neither
	// "always" nor "judged".
	ErrNotifyInvalid = errors.New("routine: the routine tells the person in no way a trigger does")
)

// CheckTitle returns the title trimmed, or an error matching
// ErrTitleMissing or ErrTitleInvalid.
func CheckTitle(s string) (string, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return "", ErrTitleMissing
	case utf8.RuneCountInString(s) > MaxTitle:
		return "", fmt.Errorf("%w: it is longer than %d characters", ErrTitleInvalid, MaxTitle)
	case strings.ContainsFunc(s, func(r rune) bool {
		return unicode.IsControl(r) || unicode.In(r, unicode.Zl, unicode.Zp, unicode.Bidi_Control)
	}):
		return "", fmt.Errorf("%w: it holds a control character or a line break", ErrTitleInvalid)
	}
	return s, nil
}

// CheckInstruction returns the instruction trimmed, or an error matching
// ErrInstructionMissing or ErrInstructionTooLong for a routine that starts
// as starts.
func CheckInstruction(s string, starts Start) (string, error) {
	s = strings.TrimSpace(s)
	bound := MaxInstruction
	// What arrives follows the instruction of a routine that starts from a
	// link or an email; one on a schedule or on dates has nothing after
	// it.
	if starts != OnSchedule && starts != OnDates {
		bound = MaxEventInstruction
	}
	switch {
	case s == "":
		return "", ErrInstructionMissing
	case len(s) > bound:
		return "", fmt.Errorf("%w: %d bytes, past %d", ErrInstructionTooLong, len(s), bound)
	}
	return s, nil
}

// CheckSchedule returns the schedule with its days in week order, each
// once, or an error matching ErrScheduleInvalid.
func CheckSchedule(s Schedule) (Schedule, error) {
	if len(s.Days) == 0 {
		return Schedule{}, fmt.Errorf("%w: it names no day", ErrScheduleInvalid)
	}
	for _, d := range s.Days {
		if d < time.Sunday || d > time.Saturday {
			return Schedule{}, fmt.Errorf("%w: %d is no day of the week", ErrScheduleInvalid, int(d))
		}
	}
	if s.Hour < 0 || s.Hour > 23 || s.Minute < 0 || s.Minute > 59 {
		return Schedule{}, fmt.Errorf("%w: %d:%02d is no time of day", ErrScheduleInvalid, s.Hour, s.Minute)
	}
	if !zonePattern.MatchString(s.Zone) {
		return Schedule{}, fmt.Errorf("%w: the zone %q is not an IANA time zone name", ErrScheduleInvalid, s.Zone)
	}
	days := slices.DeleteFunc(slices.Clone(weekOrder), func(d time.Weekday) bool { return !slices.Contains(s.Days, d) })
	return Schedule{Days: days, Hour: s.Hour, Minute: s.Minute, Zone: s.Zone}, nil
}

// CheckDates returns the dates of a routine on dates in ascending order,
// or an error matching ErrDatesInvalid for a list of no date or of more
// than MaxDates, one that holds the same time twice, one with no date
// after now, or a zone, when given, that is not an IANA name's shape. A
// date that has passed is kept beside one to come: it never runs. The API
// refuses a list with nothing to come when the trigger is written, so a
// caller hears of it first.
func CheckDates(dates []time.Time, zone string, now time.Time) ([]time.Time, error) {
	switch {
	case len(dates) == 0:
		return nil, fmt.Errorf("%w: a routine on dates names at least one", ErrDatesInvalid)
	case len(dates) > MaxDates:
		return nil, fmt.Errorf("%w: %d dates, past %d", ErrDatesInvalid, len(dates), MaxDates)
	case zone != "" && !zonePattern.MatchString(zone):
		return nil, fmt.Errorf("%w: the zone %q is not an IANA time zone name", ErrDatesInvalid, zone)
	}
	sorted := slices.SortedFunc(slices.Values(dates), time.Time.Compare)
	for i := 1; i < len(sorted); i++ {
		if sorted[i].Equal(sorted[i-1]) {
			return nil, fmt.Errorf("%w: %s is named twice", ErrDatesInvalid, sorted[i].UTC().Format(time.RFC3339Nano))
		}
	}
	if last := sorted[len(sorted)-1]; !last.After(now) {
		return nil, fmt.Errorf("%w: every date has passed, the last %s", ErrDatesInvalid, last.UTC().Format(time.RFC3339Nano))
	}
	return sorted, nil
}

// CheckNotify returns an error matching ErrNotifyInvalid for a way of
// telling the person that is neither "always" nor "judged".
func CheckNotify(notify string) error {
	switch notify {
	case notifyAlways, notifyJudged:
		return nil
	}
	return fmt.Errorf("%w: %q", ErrNotifyInvalid, notify)
}

// CheckAllowance returns an error matching ErrAllowanceInvalid for a
// weekly allowance, in millionths of a US dollar, below zero.
func CheckAllowance(perWeekMicroUSD int64) error {
	if perWeekMicroUSD < 0 {
		return fmt.Errorf("%w: %d", ErrAllowanceInvalid, perWeekMicroUSD)
	}
	return nil
}

// CheckStarts returns an error matching ErrStartsInvalid for a Start that
// is none of the four.
func CheckStarts(s Start) error {
	switch s {
	case OnSchedule, FromLink, FromEmail, OnDates:
		return nil
	}
	return fmt.Errorf("%w: %q", ErrStartsInvalid, string(s))
}

// New returns the fields of a new routine trimmed and checked: Starts is
// OnSchedule when empty, a routine on a schedule names one, a routine on
// dates names dates CheckDates takes, an allowance is not below zero, and
// a way of telling, when given, is one a trigger takes. The schedule of a
// routine that does not start on one is dropped, and the dates of one
// that does not start on dates. An error matches one of the check errors.
func New(f Fields) (Fields, error) {
	if f.Starts == "" {
		f.Starts = OnSchedule
	}
	if err := CheckStarts(f.Starts); err != nil {
		return Fields{}, err
	}
	if f.PerWeekMicroUSD != nil {
		if err := CheckAllowance(*f.PerWeekMicroUSD); err != nil {
			return Fields{}, err
		}
	}
	if f.Notify != "" {
		if err := CheckNotify(f.Notify); err != nil {
			return Fields{}, err
		}
	}
	var err error
	if f.Title, err = CheckTitle(f.Title); err != nil {
		return Fields{}, err
	}
	if f.Instruction, err = CheckInstruction(f.Instruction, f.Starts); err != nil {
		return Fields{}, err
	}
	if f.Starts == OnDates {
		if f.Dates, err = CheckDates(f.Dates, f.DatesZone, time.Now()); err != nil {
			return Fields{}, err
		}
		f.Schedule = nil
		return f, nil
	}
	f.Dates, f.DatesZone = nil, ""
	if f.Starts != OnSchedule {
		f.Schedule = nil
		return f, nil
	}
	if f.Schedule == nil {
		return Fields{}, fmt.Errorf("%w: a routine on a schedule names one", ErrScheduleInvalid)
	}
	s, err := CheckSchedule(*f.Schedule)
	if err != nil {
		return Fields{}, err
	}
	f.Schedule = &s
	return f, nil
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
		return nil, fmt.Errorf("routine: the trigger's %s is not an object", key)
	}
}

// WriteAllowance writes a weekly allowance into spec, a trigger's spec as
// a JSON object: perWeekMicroUSD as its budget.perWeek, or, at zero, the
// trigger's allowance taken off, and its budget with it once nothing else
// is in it. Any other member of the budget is kept. It returns an error
// when the spec's budget is not an object.
func WriteAllowance(spec map[string]any, perWeekMicroUSD int64) error {
	if perWeekMicroUSD == 0 {
		budget, ok := spec["budget"].(map[string]any)
		if !ok {
			// No budget, or one of no shape a trigger has, holds no
			// allowance to take off.
			return nil
		}
		delete(budget, "perWeek")
		if len(budget) == 0 {
			delete(spec, "budget")
		}
		return nil
	}
	budget, err := member(spec, "budget")
	if err != nil {
		return err
	}
	budget["perWeek"] = perWeekMicroUSD
	return nil
}

// writeModel writes the model of a routine into session, a trigger's
// spec.session as a JSON object: nothing for a nil model, the session's
// model taken off for "", and otherwise the name over the stored model,
// whose other members, its reasoning among them, are kept. It returns an
// error when the session's model is not an object.
func writeModel(session map[string]any, model *string) error {
	switch {
	case model == nil:
		return nil
	case *model == "":
		delete(session, "model")
		return nil
	}
	m, err := member(session, "model")
	if err != nil {
		return err
	}
	m["name"] = *model
	return nil
}

// Manifest returns the trigger manifest of the routine of agent named name,
// the agent by its name, which the API reads in the context of whoever
// applies it. It writes f over stored, the trigger's JSON as the API
// answered it, or over nothing for a new routine, and leaves the status
// out. Everything else stored holds, its other labels, its annotations and
// the spec's other fields among them, is written back as it was, so a
// change undoes nothing set another way; an f whose PerWeekMicroUSD or
// Model is nil, or whose Notify is empty, keeps what the stored trigger
// has of it with the rest. A routine on dates is written with its
// spec.times, even for one date, and the schedule, the events or the one
// time of how it started before are taken off; one that comes to start
// another way has its spec.times taken off. f is written as it is: New
// and the checks are the caller's to apply first.
func Manifest(agent, name string, stored json.RawMessage, f Fields) (json.RawMessage, error) {
	m := map[string]any{}
	if len(stored) > 0 {
		dec := json.NewDecoder(bytes.NewReader(stored))
		// Numbers stay as they were written, so a large one is not rounded
		// to a float's precision.
		dec.UseNumber()
		if err := dec.Decode(&m); err != nil {
			return nil, fmt.Errorf("routine: the stored trigger %s is not a JSON object: %w", name, err)
		}
	}
	delete(m, "status")
	m["apiVersion"], m["kind"] = apiVersion, kind
	meta, err := member(m, "metadata")
	if err != nil {
		return nil, err
	}
	labels, err := member(meta, "labels")
	if err != nil {
		return nil, err
	}
	labels[Label] = agent
	meta["name"], meta["displayName"] = name, f.Title
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
	session["message"], session["title"] = escapeTemplate(f.Instruction)+suffixOf(f.Starts), escapeTemplate(f.Title)
	if err := writeModel(session, f.Model); err != nil {
		return nil, err
	}
	if f.Notify != "" {
		session["notify"] = f.Notify
	}
	spec["agent"] = agent
	switch {
	case f.Starts == OnDates:
		// Nil dates keep the stored ones with their zone. The dates are
		// written in UTC, the zone beside them, since the API reads the
		// times alone to fire them. A single date is a list too: the one
		// time a trigger fires at, spec.at, is a wake's.
		if f.Dates != nil {
			times := make([]string, len(f.Dates))
			for i, d := range f.Dates {
				times[i] = d.UTC().Format(time.RFC3339Nano)
			}
			spec["times"] = times
			if f.DatesZone != "" {
				spec["timeZone"] = f.DatesZone
			} else {
				delete(spec, "timeZone")
			}
		}
		delete(spec, "schedule")
		delete(spec, "on")
		delete(spec, "at")
	case f.Starts != OnSchedule:
		product := productWebhook
		if f.Starts == FromEmail {
			product = productMail
		}
		// An event trigger of the platform's product, which the link or the
		// address delivers to. Its session's key is the API's default, the
		// event's resource.
		spec["on"] = map[string]any{"product": product, "verbs": []string{verbReceived}}
		delete(spec, "schedule")
		delete(spec, "timeZone")
		delete(spec, "at")
		delete(spec, "times")
		delete(session, "key")
	case f.Schedule != nil:
		spec["schedule"], spec["timeZone"] = Cron(*f.Schedule), f.Schedule.Zone
		delete(spec, "on")
		delete(spec, "at")
		delete(spec, "times")
	}
	// Every run of a routine shares one session key, so with the API's
	// default a run waiting on the person would skip every later run until
	// they answered.
	spec["skipIfActive"] = false
	if f.Paused {
		spec["suspend"] = true
	} else {
		delete(spec, "suspend")
	}
	if f.PerWeekMicroUSD != nil {
		if err := WriteAllowance(spec, *f.PerWeekMicroUSD); err != nil {
			return nil, err
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("routine: the routine %s could not be written: %w", name, err)
	}
	return b, nil
}
