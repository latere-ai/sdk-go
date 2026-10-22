// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package routine holds the shape of a routine's trigger, and nothing that
// sends it: the label and the name the trigger carries, the name made from
// a create's key, the manifest's fields, reading a trigger back as a
// routine, and the checks a routine's title, instruction and schedule must
// pass.
//
// A program that keeps a person's routines uses this shape through package
// agents, which names each of its types and constants and writes and reads
// routines over the trigger endpoints. This package is for code that
// writes or reads the shape without calling the Latere API, such as a
// server that applies a routine an agent offered once its owner said yes,
// so every program reads one definition. It imports the standard library
// alone and opens no connection.
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
	// schedule holds: the longest message a firing renders.
	MaxInstruction = 64 << 10
	// MaxEventInstruction is the most bytes the instruction of a routine
	// that starts from a link or an email holds. What arrives is written
	// into the same message after it, each value cut at 16 KiB and the
	// message refused past MaxInstruction, so an instruction of this length
	// and everything that arrives fit together.
	MaxEventInstruction = 16 << 10
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
	// Paused says the routine runs nothing until it is resumed.
	Paused bool
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
// and labels, its schedule or the product of the events it fires on, and
// its session's message and title.
type Trigger struct {
	Name, DisplayName string
	Labels            map[string]string
	// Schedule and TimeZone are the trigger's spec.schedule and
	// spec.timeZone, and Product the product of its spec.on, "" for a
	// trigger on a schedule.
	Schedule, TimeZone, Product string
	// Message and Title are its spec.session's.
	Message, Title string
	Suspend        bool
}

// Read reads t as a routine of agent, and reports false when it is none: a
// trigger without the Label of agent, or whose name is not a routine's.
// The instruction and the title are read as the person wrote them,
// without the words a routine that starts from a link or an email adds
// after its instruction. Schedule is nil for a routine that starts from a
// link or an email, and for one whose schedule has a shape a routine does
// not write, set another way.
func Read(t Trigger, agent string) (Fields, bool) {
	if agent == "" || t.Labels[Label] != agent || !IsName(t.Name) {
		return Fields{}, false
	}
	title := t.DisplayName
	if title == "" {
		title = unescapeTemplate(t.Title)
	}
	starts := OnSchedule
	switch t.Product {
	case productWebhook:
		starts = FromLink
	case productMail:
		starts = FromEmail
	}
	// An event of another product, set another way, is no way a routine
	// starts: it reads as a schedule set elsewhere, which a change that
	// names a schedule replaces.
	message := t.Message
	if suffix := suffixOf(starts); suffix != "" {
		message = strings.TrimSuffix(message, suffix)
	}
	f := Fields{Title: title, Instruction: unescapeTemplate(message), Starts: starts, Paused: t.Suspend}
	if t.Schedule != "" {
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
	// ErrStartsInvalid matches a Start that is none of OnSchedule, FromLink
	// and FromEmail.
	ErrStartsInvalid = errors.New("routine: the routine starts in no way a routine starts")
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
	if starts != OnSchedule {
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

// CheckStarts returns an error matching ErrStartsInvalid for a Start that
// is none of the three.
func CheckStarts(s Start) error {
	switch s {
	case OnSchedule, FromLink, FromEmail:
		return nil
	}
	return fmt.Errorf("%w: %q", ErrStartsInvalid, string(s))
}

// New returns the fields of a new routine trimmed and checked: Starts is
// OnSchedule when empty, and a routine on a schedule names one. An error
// matches one of the check errors.
func New(f Fields) (Fields, error) {
	if f.Starts == "" {
		f.Starts = OnSchedule
	}
	if err := CheckStarts(f.Starts); err != nil {
		return Fields{}, err
	}
	var err error
	if f.Title, err = CheckTitle(f.Title); err != nil {
		return Fields{}, err
	}
	if f.Instruction, err = CheckInstruction(f.Instruction, f.Starts); err != nil {
		return Fields{}, err
	}
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

// Manifest returns the trigger manifest of the routine of agent named name,
// the agent by its name, which the API reads in the context of whoever
// applies it. It writes f over stored, the trigger's JSON as the API
// answered it, or over nothing for a new routine, and leaves the status
// out. Everything else stored holds, its other labels, its annotations and
// the spec's other fields among them, is written back as it was, so a
// change undoes nothing set another way. f is written as it is: New and
// the checks are the caller's to apply first.
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
	spec["agent"] = agent
	switch {
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
		delete(session, "key")
	case f.Schedule != nil:
		spec["schedule"], spec["timeZone"] = Cron(*f.Schedule), f.Schedule.Zone
		delete(spec, "on")
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
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("routine: the routine %s could not be written: %w", name, err)
	}
	return b, nil
}
