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
