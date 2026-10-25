// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package routine_test

import (
	"encoding/json"
	"errors"
	"go/build"
	"reflect"
	"strings"
	"testing"
	"time"

	"latere.ai/x/sdk-go/agents/routine"
)

// monday is every Monday at 9:00 in Berlin.
var monday = routine.Schedule{Days: []time.Weekday{time.Monday}, Hour: 9, Minute: 0, Zone: "Europe/Berlin"}

// decode reads a manifest as a JSON object.
func decode(t *testing.T, b json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("the manifest %s is not a JSON object: %v", b, err)
	}
	return m
}

// TestANewRoutinesManifest: a routine on a schedule is the trigger the
// clients write: the agent by name in its label and its spec, the name
// and the title, the instruction literal, the schedule and its zone, and
// skipIfActive false.
func TestANewRoutinesManifest(t *testing.T) {
	f, err := routine.New(routine.Fields{Title: " Weekly sign-ups ", Instruction: "Count the {{new}} sign-ups.", Schedule: &monday})
	if err != nil {
		t.Fatal(err)
	}
	name := routine.Name("key-1")
	b, err := routine.Manifest("latere-chat", name, nil, f)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"apiVersion": "topos.latere.ai/v1", "kind": "Trigger",
		"metadata": map[string]any{"name": name, "displayName": "Weekly sign-ups", "labels": map[string]any{"applied-by": "latere-chat"}},
		"spec": map[string]any{
			"agent": "latere-chat", "schedule": "0 9 * * 1", "timeZone": "Europe/Berlin", "skipIfActive": false,
			"session": map[string]any{"message": `Count the \{{new}} sign-ups.`, "title": "Weekly sign-ups"},
		},
	}
	if got := decode(t, b); !reflect.DeepEqual(got, want) {
		t.Fatalf("the manifest is\n%v\nwant\n%v", got, want)
	}
}

// TestTheNameIsTheKeys: a key names one trigger, of the routine's shape,
// and another key another.
func TestTheNameIsTheKeys(t *testing.T) {
	a, b := routine.Name("ses_1/toolu_1"), routine.Name("ses_1/toolu_2")
	if a != routine.Name("ses_1/toolu_1") || a == b {
		t.Fatalf("the names are %s and %s", a, b)
	}
	if !routine.IsName(a) || len(a) != len(routine.Prefix)+20 {
		t.Fatalf("%s is not a routine's name", a)
	}
	for _, name := range []string{"routine-", "nightly", routine.Prefix + strings.Repeat("A", 20), a + "0"} {
		if routine.IsName(name) {
			t.Errorf("%q reads as a routine's name", name)
		}
	}
}

// TestTheScheduleRoundTrips: every schedule a routine writes reads back as
// it was, and a schedule of another shape reads as none.
func TestTheScheduleRoundTrips(t *testing.T) {
	week := []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday}
	for _, s := range []routine.Schedule{
		monday,
		{Days: week, Hour: 7, Minute: 30, Zone: "UTC"},
		{Days: []time.Weekday{time.Monday, time.Sunday}, Hour: 23, Minute: 59, Zone: "Asia/Shanghai"},
	} {
		cron := routine.Cron(s)
		if got := routine.ParseSchedule(cron, s.Zone); got == nil || !reflect.DeepEqual(*got, s) {
			t.Errorf("%s read back as %+v, want %+v", cron, got, s)
		}
	}
	if got := routine.ParseSchedule("0 9 * * 7", ""); got == nil || got.Zone != "UTC" || !reflect.DeepEqual(got.Days, []time.Weekday{time.Sunday}) {
		t.Errorf("Sunday as 7, with no zone, read as %+v", got)
	}
	for _, cron := range []string{"*/5 * * * *", "0 9 1 * *", "0 24 * * *", "60 9 * * *", "0 9 * * 1-5", ""} {
		if got := routine.ParseSchedule(cron, "UTC"); got != nil {
			t.Errorf("%q read as %+v", cron, got)
		}
	}
}

// TestARoutineReadsBackAsWritten: each way a routine starts is read back
// from the trigger its manifest makes, the words added after the
// instruction of one that starts on arrival taken off.
func TestARoutineReadsBackAsWritten(t *testing.T) {
	for _, f := range []routine.Fields{
		{Title: "Weekly sign-ups", Instruction: "Count the {{new}} sign-ups.", Starts: routine.OnSchedule, Schedule: &monday},
		{Title: "From the shop", Instruction: "File the order.", Starts: routine.FromLink, Paused: true},
		{Title: "Invoices", Instruction: "Save the invoice.", Starts: routine.FromEmail},
	} {
		name := routine.Name(f.Title)
		b, err := routine.Manifest("latere-chat", name, nil, f)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Metadata struct {
				Name, DisplayName string
				Labels            map[string]string
			}
			Spec struct {
				Schedule, TimeZone string
				On                 *struct{ Product string }
				Session            struct{ Message, Title string }
				Suspend            bool
			}
		}
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatal(err)
		}
		tr := routine.Trigger{Name: doc.Metadata.Name, DisplayName: doc.Metadata.DisplayName, Labels: doc.Metadata.Labels,
			Schedule: doc.Spec.Schedule, TimeZone: doc.Spec.TimeZone, Message: doc.Spec.Session.Message, Title: doc.Spec.Session.Title, Suspend: doc.Spec.Suspend}
		if doc.Spec.On != nil {
			tr.Product = doc.Spec.On.Product
		}
		got, ok := routine.Read(tr, "latere-chat")
		if !ok || !reflect.DeepEqual(got, f) {
			t.Errorf("%s read back as %+v (%v), want %+v", f.Title, got, ok, f)
		}
		if _, ok := routine.Read(tr, "another-agent"); ok {
			t.Errorf("%s reads as another agent's routine", f.Title)
		}
	}
	if _, ok := routine.Read(routine.Trigger{Name: "nightly", Labels: map[string]string{routine.Label: "latere-chat"}}, "latere-chat"); ok {
		t.Error("a trigger of another name reads as a routine")
	}
}

// TestAChangeKeepsWhatTheRoutineDoesNotOwn: written over a stored trigger,
// the manifest keeps its other labels, its annotations, the spec's other
// fields and a large number as written, drops the status, and keeps a
// schedule set elsewhere when the fields name none.
func TestAChangeKeepsWhatTheRoutineDoesNotOwn(t *testing.T) {
	stored := json.RawMessage(`{"apiVersion":"topos.latere.ai/v1","kind":"Trigger",
		"metadata":{"name":"routine-0123456789abcdef0123","labels":{"team":"growth"},"annotations":{"note":"kept"}},
		"spec":{"agent":"latere-chat","schedule":"*/30 * * * *","timeZone":"UTC","maxActive":12345678901234567890,"session":{"message":"old","policy":"continue"}},
		"status":{"id":"trg_1"}}`)
	f := routine.Fields{Title: "Renamed", Instruction: "New words", Starts: routine.OnSchedule}
	b, err := routine.Manifest("latere-chat", "routine-0123456789abcdef0123", stored, f)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"status"`) || !strings.Contains(string(b), `12345678901234567890`) {
		t.Fatalf("the manifest is %s", b)
	}
	m := decode(t, b)
	meta, spec := m["metadata"].(map[string]any), m["spec"].(map[string]any)
	if meta["labels"].(map[string]any)["team"] != "growth" || meta["annotations"].(map[string]any)["note"] != "kept" {
		t.Errorf("the metadata is %v", meta)
	}
	if spec["schedule"] != "*/30 * * * *" || spec["session"].(map[string]any)["policy"] != "continue" {
		t.Errorf("the spec is %v", spec)
	}
	if _, err := routine.Manifest("latere-chat", "routine-0123456789abcdef0123", json.RawMessage(`[]`), f); err == nil {
		t.Error("a stored trigger that is no object was written over")
	}
	if _, err := routine.Manifest("latere-chat", "routine-0123456789abcdef0123", json.RawMessage(`{"spec":[]}`), f); err == nil {
		t.Error("a stored spec that is no object was written over")
	}
}

// TestTheChecks: New refuses each field a routine cannot take with its
// error, before anything is written.
func TestTheChecks(t *testing.T) {
	long := strings.Repeat("x", routine.MaxTitle+1)
	cases := []struct {
		name string
		f    routine.Fields
		want error
	}{
		{"no title", routine.Fields{Title: "  ", Instruction: "x", Schedule: &monday}, routine.ErrTitleMissing},
		{"a long title", routine.Fields{Title: long, Instruction: "x", Schedule: &monday}, routine.ErrTitleInvalid},
		{"a line break", routine.Fields{Title: "a\nb", Instruction: "x", Schedule: &monday}, routine.ErrTitleInvalid},
		{"no instruction", routine.Fields{Title: "t", Schedule: &monday}, routine.ErrInstructionMissing},
		{"a long instruction on arrival", routine.Fields{Title: "t", Instruction: strings.Repeat("x", routine.MaxEventInstruction+1), Starts: routine.FromLink}, routine.ErrInstructionTooLong},
		{"no schedule", routine.Fields{Title: "t", Instruction: "x"}, routine.ErrScheduleInvalid},
		{"no day", routine.Fields{Title: "t", Instruction: "x", Schedule: &routine.Schedule{Hour: 9, Zone: "UTC"}}, routine.ErrScheduleInvalid},
		{"no day of the week", routine.Fields{Title: "t", Instruction: "x", Schedule: &routine.Schedule{Days: []time.Weekday{8}, Zone: "UTC"}}, routine.ErrScheduleInvalid},
		{"no time of day", routine.Fields{Title: "t", Instruction: "x", Schedule: &routine.Schedule{Days: []time.Weekday{1}, Hour: 24, Zone: "UTC"}}, routine.ErrScheduleInvalid},
		{"no zone's shape", routine.Fields{Title: "t", Instruction: "x", Schedule: &routine.Schedule{Days: []time.Weekday{1}, Zone: "Berlin time"}}, routine.ErrScheduleInvalid},
		{"no way to start", routine.Fields{Title: "t", Instruction: "x", Starts: "hourly"}, routine.ErrStartsInvalid},
	}
	for _, tc := range cases {
		if _, err := routine.New(tc.f); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, err, tc.want)
		}
	}
	f, err := routine.New(routine.Fields{Title: "t", Instruction: "x", Starts: routine.FromEmail, Schedule: &monday})
	if err != nil || f.Schedule != nil {
		t.Errorf("a routine on arrival kept a schedule: %+v, %v", f, err)
	}
	s, err := routine.CheckSchedule(routine.Schedule{Days: []time.Weekday{time.Sunday, time.Monday, time.Monday}, Hour: 9, Zone: "UTC"})
	if err != nil || !reflect.DeepEqual(s.Days, []time.Weekday{time.Monday, time.Sunday}) {
		t.Errorf("the days were put in week order as %v, %v", s.Days, err)
	}
}

// TestItImportsTheStandardLibraryAlone: a server imports the shape without
// this module's HTTP client and credentials.
func TestItImportsTheStandardLibraryAlone(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range pkg.Imports {
		if strings.Contains(strings.SplitN(path, "/", 2)[0], ".") {
			t.Errorf("package routine imports %s", path)
		}
	}
}

// TestTheAllowanceIsWrittenAndKept: a new routine's allowance is its
// spec.budget.perWeek, read back by Read; a change whose fields carry none
// keeps the stored allowance and the budget's other members; zero takes
// it off, and the budget with it once nothing else is in it; an allowance
// below zero is refused by New and CheckAllowance.
func TestTheAllowanceIsWrittenAndKept(t *testing.T) {
	perWeek := int64(2_500_000)
	f, err := routine.New(routine.Fields{Title: "Inbox", Instruction: "Look at the inbox.", Schedule: &monday, PerWeekMicroUSD: &perWeek})
	if err != nil {
		t.Fatal(err)
	}
	name := routine.Name("key-allowance")
	b, err := routine.Manifest("latere-chat", name, nil, f)
	if err != nil {
		t.Fatal(err)
	}
	spec := decode(t, b)["spec"].(map[string]any)
	if budget, _ := spec["budget"].(map[string]any); budget["perWeek"] != float64(2_500_000) || len(budget) != 1 {
		t.Fatalf("the allowance is written as %v", spec["budget"])
	}
	read, ok := routine.Read(routine.Trigger{Name: name, Labels: map[string]string{routine.Label: "latere-chat"}, Schedule: "0 9 * * 1",
		TimeZone: "Europe/Berlin", Message: "Look at the inbox.", Title: "Inbox", PerWeekMicroUSD: 2_500_000}, "latere-chat")
	if !ok || read.PerWeekMicroUSD == nil || *read.PerWeekMicroUSD != 2_500_000 {
		t.Fatalf("the allowance reads back as %+v, %v", read, ok)
	}
	if read, _ := routine.Read(routine.Trigger{Name: name, Labels: map[string]string{routine.Label: "latere-chat"}, Message: "m"}, "latere-chat"); read.PerWeekMicroUSD != nil {
		t.Errorf("a trigger with no allowance reads as one of %d", *read.PerWeekMicroUSD)
	}

	stored := json.RawMessage(`{"apiVersion":"topos.latere.ai/v1","kind":"Trigger","metadata":{"name":"` + name + `"},
"spec":{"agent":"latere-chat","schedule":"0 9 * * 1","timeZone":"Europe/Berlin","budget":{"perWeek":9007199254740993,"later":"kept"},
"session":{"message":"Look at the inbox.","title":"Inbox"}}}`)
	kept, err := routine.Manifest("latere-chat", name, stored, routine.Fields{Title: "Inbox", Instruction: "Look at the inbox.", Starts: routine.OnSchedule})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(kept), `"budget":{"later":"kept","perWeek":9007199254740993}`) {
		t.Errorf("a change that names no allowance wrote %s", kept)
	}
	zero := int64(0)
	off, err := routine.Manifest("latere-chat", name, stored, routine.Fields{Title: "Inbox", Instruction: "Look at the inbox.", Starts: routine.OnSchedule, PerWeekMicroUSD: &zero})
	if err != nil {
		t.Fatal(err)
	}
	if budget := decode(t, off)["spec"].(map[string]any)["budget"].(map[string]any); budget["perWeek"] != nil || budget["later"] != "kept" {
		t.Errorf("taking the allowance off left the budget %v", budget)
	}
	only := json.RawMessage(`{"spec":{"agent":"latere-chat","budget":{"perWeek":1},"session":{"message":"m"}}}`)
	if b, err := routine.Manifest("latere-chat", name, only, routine.Fields{Title: "Inbox", Instruction: "m", PerWeekMicroUSD: &zero}); err != nil || strings.Contains(string(b), "budget") {
		t.Errorf("taking the only allowance off wrote %s, %v", b, err)
	}
	if _, err := routine.Manifest("latere-chat", name, json.RawMessage(`{"spec":{"budget":"none"}}`), routine.Fields{Title: "Inbox", Instruction: "m", PerWeekMicroUSD: &perWeek}); err == nil {
		t.Error("an allowance was written into a budget that is no object")
	}

	below := int64(-1)
	if _, err := routine.New(routine.Fields{Title: "Inbox", Instruction: "m", Schedule: &monday, PerWeekMicroUSD: &below}); !errors.Is(err, routine.ErrAllowanceInvalid) {
		t.Errorf("an allowance below zero: %v", err)
	}
	if err := routine.CheckAllowance(0); err != nil {
		t.Errorf("no allowance: %v", err)
	}
}

// TestWriteAllowance: the allowance is written into a spec of its own,
// and taking it off a spec without one changes nothing.
func TestWriteAllowance(t *testing.T) {
	spec := map[string]any{"agent": "a"}
	if err := routine.WriteAllowance(spec, 0); err != nil || len(spec) != 1 {
		t.Errorf("taking off no allowance left %v, %v", spec, err)
	}
	if err := routine.WriteAllowance(spec, 7); err != nil || spec["budget"].(map[string]any)["perWeek"] != int64(7) {
		t.Errorf("the allowance is written as %v, %v", spec, err)
	}
	spec["budget"] = "none"
	if err := routine.WriteAllowance(spec, 0); err != nil || spec["budget"] != "none" {
		t.Errorf("taking off from a budget that is no object left %v, %v", spec, err)
	}
}

// days is whole seconds n days from now, as the API writes a time back.
func days(n ...int) []time.Time {
	now := time.Now().Truncate(time.Second)
	out := make([]time.Time, len(n))
	for i, d := range n {
		out[i] = now.Add(time.Duration(d) * 24 * time.Hour)
	}
	return out
}

// utc is the dates as a trigger's spec.times holds them once decoded.
func utc(dates []time.Time) []any {
	out := make([]any, len(dates))
	for i, d := range dates {
		out[i] = d.UTC().Format(time.RFC3339Nano)
	}
	return out
}

// readBack reads a manifest as package agents reads the trigger it makes.
func readBack(t *testing.T, b json.RawMessage) routine.Trigger {
	t.Helper()
	var doc struct {
		Metadata struct {
			Name, DisplayName string
			Labels            map[string]string
		}
		Spec struct {
			Schedule, TimeZone string
			Times              []time.Time
			On                 *struct{ Product string }
			Session            struct {
				Message, Title, Notify string
				Model                  *struct{ Name string }
			}
			Suspend bool
		}
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	tr := routine.Trigger{Name: doc.Metadata.Name, DisplayName: doc.Metadata.DisplayName, Labels: doc.Metadata.Labels, Schedule: doc.Spec.Schedule,
		TimeZone: doc.Spec.TimeZone, Times: doc.Spec.Times, Message: doc.Spec.Session.Message, Title: doc.Spec.Session.Title,
		Notify: doc.Spec.Session.Notify, Suspend: doc.Spec.Suspend}
	if doc.Spec.On != nil {
		tr.Product = doc.Spec.On.Product
	}
	if doc.Spec.Session.Model != nil {
		tr.Model = doc.Spec.Session.Model.Name
	}
	return tr
}

// TestARoutineOnDates: a routine on dates is written with its dates in
// UTC, in ascending order, as spec.times and the zone the person picked
// as spec.timeZone, with no schedule and no spec.at, even for one date,
// and reads back as it was written; with no zone it writes none and
// reads back in UTC. Its instruction is held to the bound of a routine on
// a schedule, since nothing arrives after it.
func TestARoutineOnDates(t *testing.T) {
	dates := days(9, 2, 30)
	cet := time.FixedZone("CET", 3600)
	for i := range dates {
		dates[i] = dates[i].In(cet)
	}
	f, err := routine.New(routine.Fields{Title: "Rent", Instruction: "Remind me to pay the rent.", Starts: routine.OnDates, Dates: dates,
		DatesZone: "Europe/Berlin", Schedule: &monday})
	if err != nil {
		t.Fatal(err)
	}
	sorted := []time.Time{dates[1], dates[0], dates[2]}
	if f.Schedule != nil || !reflect.DeepEqual(f.Dates, sorted) {
		t.Errorf("New gave %+v", f)
	}
	name := routine.Name("key-dates")
	b, err := routine.Manifest("latere-chat", name, nil, f)
	if err != nil {
		t.Fatal(err)
	}
	spec := decode(t, b)["spec"].(map[string]any)
	if !reflect.DeepEqual(spec["times"], utc(sorted)) || spec["timeZone"] != "Europe/Berlin" || spec["schedule"] != nil || spec["on"] != nil || spec["at"] != nil {
		t.Fatalf("the spec is %v", spec)
	}
	got, ok := routine.Read(readBack(t, b), "latere-chat")
	if !ok || got.Starts != routine.OnDates || got.DatesZone != "Europe/Berlin" || got.Schedule != nil || len(got.Dates) != 3 {
		t.Fatalf("it reads back as %+v, %v", got, ok)
	}
	for i := range sorted {
		if !got.Dates[i].Equal(sorted[i]) {
			t.Errorf("date %d reads back as %s, want %s", i, got.Dates[i], sorted[i])
		}
	}
	got.Dates = f.Dates
	if !reflect.DeepEqual(got, f) {
		t.Errorf("it reads back as %+v, want %+v", got, f)
	}
	// Read orders the list however the trigger holds it.
	tr := readBack(t, b)
	tr.Times = []time.Time{sorted[2], sorted[0], sorted[1]}
	if got, _ := routine.Read(tr, "latere-chat"); !got.Dates[0].Equal(sorted[0]) || !got.Dates[2].Equal(sorted[2]) {
		t.Errorf("an unordered list reads as %v", got.Dates)
	}

	one, err := routine.New(routine.Fields{Title: "Bank", Instruction: "Remind me to call the bank.", Starts: routine.OnDates, Dates: days(1)})
	if err != nil {
		t.Fatal(err)
	}
	b, err = routine.Manifest("latere-chat", name, nil, one)
	if err != nil {
		t.Fatal(err)
	}
	if spec := decode(t, b)["spec"].(map[string]any); !reflect.DeepEqual(spec["times"], utc(one.Dates)) || spec["at"] != nil || spec["timeZone"] != nil {
		t.Errorf("one date with no zone wrote %v", spec)
	}
	if got, _ := routine.Read(readBack(t, b), "latere-chat"); got.DatesZone != "UTC" || got.Starts != routine.OnDates {
		t.Errorf("no zone reads back as %+v", got)
	}

	long := strings.Repeat("x", routine.MaxEventInstruction+1)
	if _, err := routine.New(routine.Fields{Title: "t", Instruction: long, Starts: routine.OnDates, Dates: days(1)}); err != nil {
		t.Errorf("an instruction past the bound of a link: %v", err)
	}
	if err := routine.CheckStarts(routine.OnDates); err != nil {
		t.Errorf("OnDates: %v", err)
	}
	onSchedule, err := routine.New(routine.Fields{Title: "t", Instruction: "x", Schedule: &monday, Dates: days(1), DatesZone: "Europe/Berlin"})
	if err != nil || onSchedule.Dates != nil || onSchedule.DatesZone != "" {
		t.Errorf("a routine on a schedule kept dates: %+v, %v", onSchedule, err)
	}
	// A trigger with no times, such as one with the one time of a wake set
	// another way, is no routine on dates: it reads as a schedule set
	// elsewhere.
	if got, _ := routine.Read(routine.Trigger{Name: name, Labels: map[string]string{routine.Label: "latere-chat"}, Message: "m"}, "latere-chat"); got.Starts != routine.OnSchedule {
		t.Errorf("a trigger with no times reads as %+v", got)
	}
}

// TestTheDatesAreCheckedBeforeTheyAreWritten: a routine on dates without
// one, with more than MaxDates, with the same time twice, with none after
// now, or with a zone of no IANA name's shape is refused with
// ErrDatesInvalid; a list with a date that has passed beside one to come
// is taken, so a change can write back what it read.
func TestTheDatesAreCheckedBeforeTheyAreWritten(t *testing.T) {
	now := time.Now()
	many := make([]time.Time, routine.MaxDates+1)
	for i := range many {
		many[i] = now.Add(time.Duration(i+1) * time.Hour)
	}
	for _, tc := range []struct {
		name  string
		dates []time.Time
		zone  string
	}{
		{"no dates", nil, "UTC"},
		{"an empty list", []time.Time{}, "UTC"},
		{"more than the most", many, "UTC"},
		{"a date twice", []time.Time{now.Add(time.Hour), now.Add(2 * time.Hour), now.Add(time.Hour).In(time.FixedZone("CET", 3600))}, "UTC"},
		{"every date passed", []time.Time{now.Add(-time.Hour), now.Add(-time.Minute)}, "UTC"},
		{"a zone of another shape", []time.Time{now.Add(time.Hour)}, "Berlin time"},
	} {
		if _, err := routine.New(routine.Fields{Title: "t", Instruction: "x", Starts: routine.OnDates, Dates: tc.dates, DatesZone: tc.zone}); !errors.Is(err, routine.ErrDatesInvalid) {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	if _, err := routine.CheckDates([]time.Time{now}, "", now); !errors.Is(err, routine.ErrDatesInvalid) {
		t.Errorf("now: %v", err)
	}
	mixed, err := routine.CheckDates([]time.Time{now.Add(time.Hour), now.Add(-time.Hour)}, "", now)
	if err != nil || len(mixed) != 2 || !mixed[0].Before(mixed[1]) {
		t.Errorf("a passed date beside one to come: %v, %v", mixed, err)
	}
	if _, err := routine.CheckDates(many[:routine.MaxDates], "Europe/Berlin", now); err != nil {
		t.Errorf("the most dates: %v", err)
	}
}

// TestARoutineMovesBetweenSchedulesDatesAndLinks: a routine on a schedule
// that comes to start on dates loses its schedule, its events and a time
// set another way; one on dates that comes to start on a schedule or from
// a link loses its dates; a change of one on dates that names no dates
// keeps the stored dates and zone.
func TestARoutineMovesBetweenSchedulesDatesAndLinks(t *testing.T) {
	name := routine.Name("key-moves")
	dates := days(2, 4)
	scheduled := json.RawMessage(`{"metadata":{"name":"` + name + `"},"spec":{"agent":"latere-chat","schedule":"0 9 * * 1","timeZone":"Europe/Berlin",
"on":{"product":"github","verbs":["push"]},"at":"2026-10-24T07:00:00Z","session":{"message":"m","title":"t","key":"kept"}}}`)
	b, err := routine.Manifest("latere-chat", name, scheduled, routine.Fields{Title: "t", Instruction: "m", Starts: routine.OnDates, Dates: dates, DatesZone: "Asia/Shanghai"})
	if err != nil {
		t.Fatal(err)
	}
	spec := decode(t, b)["spec"].(map[string]any)
	if spec["schedule"] != nil || spec["on"] != nil || spec["at"] != nil || !reflect.DeepEqual(spec["times"], utc(dates)) || spec["timeZone"] != "Asia/Shanghai" ||
		spec["session"].(map[string]any)["key"] != "kept" {
		t.Fatalf("to dates wrote %v", spec)
	}

	onDates := b
	kept, err := routine.Manifest("latere-chat", name, onDates, routine.Fields{Title: "Renamed", Instruction: "m", Starts: routine.OnDates})
	if err != nil {
		t.Fatal(err)
	}
	if spec := decode(t, kept)["spec"].(map[string]any); !reflect.DeepEqual(spec["times"], utc(dates)) || spec["timeZone"] != "Asia/Shanghai" {
		t.Errorf("a change that names no dates wrote %v", spec)
	}
	// A shape that reads no dates, such as one written before routines ran
	// on dates, reads the trigger as on a schedule it does not know, and
	// keeps the dates.
	if b, err := routine.Manifest("latere-chat", name, onDates, routine.Fields{Title: "t", Instruction: "m", Starts: routine.OnSchedule}); err != nil ||
		decode(t, b)["spec"].(map[string]any)["times"] == nil {
		t.Errorf("a change on a schedule it does not know wrote %s, %v", b, err)
	}

	toSchedule, err := routine.Manifest("latere-chat", name, onDates, routine.Fields{Title: "t", Instruction: "m", Starts: routine.OnSchedule, Schedule: &monday})
	if err != nil {
		t.Fatal(err)
	}
	if spec := decode(t, toSchedule)["spec"].(map[string]any); spec["times"] != nil || spec["schedule"] != "0 9 * * 1" || spec["timeZone"] != "Europe/Berlin" {
		t.Errorf("to a schedule wrote %v", spec)
	}
	toLink, err := routine.Manifest("latere-chat", name, onDates, routine.Fields{Title: "t", Instruction: "m", Starts: routine.FromLink})
	if err != nil {
		t.Fatal(err)
	}
	if spec := decode(t, toLink)["spec"].(map[string]any); spec["times"] != nil || spec["timeZone"] != nil || spec["on"] == nil {
		t.Errorf("to a link wrote %v", spec)
	}
}

// TestTheModelIsWrittenAndKept: a routine's model is written as its
// session's model name and read back; a change that names none keeps the
// stored model and its reasoning, a new name keeps the reasoning, and ""
// takes the model off with its reasoning.
func TestTheModelIsWrittenAndKept(t *testing.T) {
	thorough, quick, none := "tier/thorough", "tier/quick", ""
	f, err := routine.New(routine.Fields{Title: "Digest", Instruction: "Write the digest.", Schedule: &monday, Model: &thorough})
	if err != nil {
		t.Fatal(err)
	}
	name := routine.Name("key-model")
	b, err := routine.Manifest("latere-chat", name, nil, f)
	if err != nil {
		t.Fatal(err)
	}
	if session := decode(t, b)["spec"].(map[string]any)["session"].(map[string]any); !reflect.DeepEqual(session["model"], map[string]any{"name": "tier/thorough"}) {
		t.Fatalf("the model is written as %v", session["model"])
	}
	if got, _ := routine.Read(readBack(t, b), "latere-chat"); got.Model == nil || *got.Model != thorough {
		t.Errorf("the model reads back as %v", got.Model)
	}
	if got, _ := routine.Read(routine.Trigger{Name: name, Labels: map[string]string{routine.Label: "latere-chat"}, Message: "m"}, "latere-chat"); got.Model != nil {
		t.Errorf("a trigger with no model reads as one of %q", *got.Model)
	}

	stored := json.RawMessage(`{"metadata":{"name":"` + name + `"},"spec":{"agent":"latere-chat","schedule":"0 9 * * 1","timeZone":"Europe/Berlin",
"session":{"message":"m","model":{"name":"tier/thorough","reasoning":"high"}}}}`)
	model := func(f routine.Fields) any {
		t.Helper()
		b, err := routine.Manifest("latere-chat", name, stored, f)
		if err != nil {
			t.Fatal(err)
		}
		return decode(t, b)["spec"].(map[string]any)["session"].(map[string]any)["model"]
	}
	base := routine.Fields{Title: "Digest", Instruction: "m", Starts: routine.OnSchedule}
	if got := model(base); !reflect.DeepEqual(got, map[string]any{"name": "tier/thorough", "reasoning": "high"}) {
		t.Errorf("a change that names no model wrote %v", got)
	}
	base.Model = &quick
	if got := model(base); !reflect.DeepEqual(got, map[string]any{"name": "tier/quick", "reasoning": "high"}) {
		t.Errorf("a new model wrote %v", got)
	}
	base.Model = &none
	if got := model(base); got != nil {
		t.Errorf("taking the model off wrote %v", got)
	}
	if _, err := routine.Manifest("latere-chat", name, json.RawMessage(`{"spec":{"session":{"message":"m","model":"tier/quick"}}}`),
		routine.Fields{Title: "t", Instruction: "m", Model: &quick}); err == nil {
		t.Error("a model was written into one that is no object")
	}
}

// TestNotifyIsWrittenAndKept: a routine's way of telling the person is
// its session's notify, read back as written; a change that names none
// keeps the stored one, and New refuses one a trigger does not take.
func TestNotifyIsWrittenAndKept(t *testing.T) {
	f, err := routine.New(routine.Fields{Title: "Inbox", Instruction: "Look at the inbox.", Schedule: &monday, Notify: "judged"})
	if err != nil {
		t.Fatal(err)
	}
	name := routine.Name("key-notify")
	b, err := routine.Manifest("latere-chat", name, nil, f)
	if err != nil {
		t.Fatal(err)
	}
	if session := decode(t, b)["spec"].(map[string]any)["session"].(map[string]any); session["notify"] != "judged" {
		t.Fatalf("notify is written as %v", session["notify"])
	}
	if got, _ := routine.Read(readBack(t, b), "latere-chat"); got.Notify != "judged" {
		t.Errorf("notify reads back as %q", got.Notify)
	}
	kept, err := routine.Manifest("latere-chat", name, b, routine.Fields{Title: "Renamed", Instruction: "m", Starts: routine.OnSchedule})
	if err != nil {
		t.Fatal(err)
	}
	if session := decode(t, kept)["spec"].(map[string]any)["session"].(map[string]any); session["notify"] != "judged" {
		t.Errorf("a change that names none wrote %v", session["notify"])
	}
	if b, err := routine.Manifest("latere-chat", name, nil, routine.Fields{Title: "t", Instruction: "m", Schedule: &monday}); err != nil || strings.Contains(string(b), "notify") {
		t.Errorf("a routine that names none wrote %s, %v", b, err)
	}
	if _, err := routine.New(routine.Fields{Title: "t", Instruction: "m", Schedule: &monday, Notify: "sometimes"}); !errors.Is(err, routine.ErrNotifyInvalid) {
		t.Errorf("a way of telling there is none of: %v", err)
	}
	for _, n := range []string{"always", "judged"} {
		if err := routine.CheckNotify(n); err != nil {
			t.Errorf("%s: %v", n, err)
		}
	}
}
