// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"latere.ai/x/sdk-go/agents"
	"latere.ai/x/sdk-go/latere"
)

// TestARoutinesAllowanceIsWrittenAndKept: a create writes the allowance as
// spec.budget.perWeek; a change that names none keeps the stored one; a
// change sets another or, at zero, takes it off; an allowance below zero
// reaches no API.
func TestARoutinesAllowanceIsWrittenAndKept(t *testing.T) {
	tp, c := newTopos(t)
	p := weekly()
	p.PerWeekMicroUSD = 3_000_000
	r := create(t, c, p, "allowance")
	if r.PerWeekMicroUSD != 3_000_000 {
		t.Fatalf("the routine reads an allowance of %d", r.PerWeekMicroUSD)
	}
	if budget, _ := tp.manifest(r.Name)["spec"].(map[string]any)["budget"].(map[string]any); budget["perWeek"] != float64(3_000_000) {
		t.Fatalf("the create wrote the budget %v", budget)
	}

	title := "Monday news"
	got, err := c.ChangeRoutine(t.Context(), chat, r.Name, &agents.ChangeRoutineParams{Title: &title})
	if err != nil || got.PerWeekMicroUSD != 3_000_000 {
		t.Fatalf("a change of the title answered %+v, %v", got, err)
	}
	if budget, _ := tp.manifest(r.Name)["spec"].(map[string]any)["budget"].(map[string]any); budget["perWeek"] != float64(3_000_000) {
		t.Errorf("a change of the title wrote the budget %v", budget)
	}

	more := int64(5_000_000)
	if got, err := c.ChangeRoutine(t.Context(), chat, r.Name, &agents.ChangeRoutineParams{PerWeekMicroUSD: &more}); err != nil || got.PerWeekMicroUSD != more {
		t.Errorf("a change of the allowance answered %+v, %v", got, err)
	}
	zero := int64(0)
	if got, err := c.ChangeRoutine(t.Context(), chat, r.Name, &agents.ChangeRoutineParams{PerWeekMicroUSD: &zero}); err != nil || got.PerWeekMicroUSD != 0 {
		t.Errorf("taking the allowance off answered %+v, %v", got, err)
	}
	if spec := tp.manifest(r.Name)["spec"].(map[string]any); spec["budget"] != nil {
		t.Errorf("taking the allowance off left the budget %v", spec["budget"])
	}

	tp.lines()
	below := int64(-1)
	if _, err := c.ChangeRoutine(t.Context(), chat, r.Name, &agents.ChangeRoutineParams{PerWeekMicroUSD: &below}); !errors.Is(err, agents.ErrAllowanceInvalid) {
		t.Errorf("a change below zero: %v", err)
	}
	p = weekly()
	p.PerWeekMicroUSD = -1
	if _, err := c.CreateRoutine(t.Context(), p); !errors.Is(err, agents.ErrAllowanceInvalid) {
		t.Errorf("a create below zero: %v", err)
	}
	for _, line := range tp.lines() {
		if strings.HasPrefix(line, http.MethodPut) {
			t.Errorf("an allowance below zero was applied: %s", line)
		}
	}
}

// spentTrigger is a routine with an allowance, the week the API wrote at
// its latest run, and a firing the allowance skipped.
const spentTrigger = `{"apiVersion":"topos.latere.ai/v1","kind":"Trigger","metadata":{"name":"routine-0123456789abcdef0123",
"displayName":"Inbox","labels":{"applied-by":"latere-chat"}},"spec":{"agent":"agent_1","schedule":"0 9 * * *","timeZone":"UTC",
"skipIfActive":false,"budget":{"perWeek":2000000},"session":{"message":"Look at the inbox.","title":"Inbox"}},
"status":{"id":"trg_1","createdAt":"2026-10-01T09:00:00Z","counts":{"started":9,"continued":0,"held":0,"filtered":0,"skippedActive":0,
"skippedBusy":0,"skippedLate":0,"refused":0,"failed":0,"skippedBudget":2},"spend":{"windowDays":7,"spentUsdMicro":1500000,
"perWeekUsdMicro":2000000,"perLookUsdMicro":300000,"expectedLooks":3,"projectedUsdMicro":2400000}}}`

// TestATriggerReadsItsWeek: a trigger reads its allowance, the week the
// API wrote, and the firings the allowance skipped, and a routine reads
// the same; the week says it is ahead of pace and not yet at the
// allowance.
func TestATriggerReadsItsWeek(t *testing.T) {
	c, _ := fakeAPI(t, map[string]string{"GET /v1/agents/triggers/trg_1": spentTrigger})
	tr, err := c.GetTrigger(t.Context(), "trg_1")
	if err != nil {
		t.Fatal(err)
	}
	perLook, projected := int64(300_000), int64(2_400_000)
	want := &agents.TriggerSpend{WindowDays: 7, SpentMicroUSD: 1_500_000, PerWeekMicroUSD: 2_000_000, PerLookMicroUSD: &perLook, ExpectedLooks: 3, ProjectedMicroUSD: &projected}
	if !reflect.DeepEqual(tr.Status.Spend, want) || tr.Spec.Budget == nil || tr.Spec.Budget.PerWeekMicroUSD != 2_000_000 || tr.Status.Counts.SkippedBudget != 2 {
		t.Fatalf("the trigger reads %+v, %+v, %+v", tr.Spec.Budget, tr.Status.Spend, tr.Status.Counts)
	}
	if !tr.Status.Spend.AheadOfPace() || tr.Status.Spend.AtAllowance() {
		t.Errorf("a week projected past its allowance reads ahead %v, at %v", tr.Status.Spend.AheadOfPace(), tr.Status.Spend.AtAllowance())
	}
	r, ok := agents.RoutineOf(tr, chat)
	if !ok || r.PerWeekMicroUSD != 2_000_000 || r.Spend != tr.Status.Spend || r.SkippedBudget != 2 {
		t.Errorf("the routine reads %+v, %v", r, ok)
	}
}

// TestThePaceOfAWeek: a week is ahead of pace only with an allowance and
// a projection past it, and at its allowance once the spend reaches it.
func TestThePaceOfAWeek(t *testing.T) {
	n := func(v int64) *int64 { return &v }
	for name, c := range map[string]struct {
		spend     *agents.TriggerSpend
		ahead, at bool
	}{
		"nil":                  {nil, false, false},
		"no allowance":         {&agents.TriggerSpend{SpentMicroUSD: 9, ProjectedMicroUSD: n(20)}, false, false},
		"no projection":        {&agents.TriggerSpend{SpentMicroUSD: 9, PerWeekMicroUSD: 10}, false, false},
		"on pace":              {&agents.TriggerSpend{SpentMicroUSD: 5, PerWeekMicroUSD: 10, PerLookMicroUSD: n(1), ProjectedMicroUSD: n(10)}, false, false},
		"ahead":                {&agents.TriggerSpend{SpentMicroUSD: 5, PerWeekMicroUSD: 10, PerLookMicroUSD: n(2), ProjectedMicroUSD: n(11)}, true, false},
		"at the allowance":     {&agents.TriggerSpend{SpentMicroUSD: 10, PerWeekMicroUSD: 10}, false, true},
		"past the allowance":   {&agents.TriggerSpend{SpentMicroUSD: 12, PerWeekMicroUSD: 10, PerLookMicroUSD: n(2), ProjectedMicroUSD: n(14)}, true, true},
		"no allowance, spent":  {&agents.TriggerSpend{SpentMicroUSD: 12}, false, false},
		"a projection of zero": {&agents.TriggerSpend{PerWeekMicroUSD: 10, PerLookMicroUSD: n(0), ProjectedMicroUSD: n(0)}, false, false},
	} {
		if got := c.spend.AheadOfPace(); got != c.ahead {
			t.Errorf("%s: ahead of pace is %v", name, got)
		}
		if got := c.spend.AtAllowance(); got != c.at {
			t.Errorf("%s: at the allowance is %v", name, got)
		}
	}
	var none *agents.FiringSpend
	if none.AheadOfPace() || !(&agents.FiringSpend{PerWeekMicroUSD: 10, ProjectedMicroUSD: n(11)}).AheadOfPace() {
		t.Error("a firing's spend reads its pace wrong")
	}
}

// TestSetTriggerAllowanceKeepsEveryOtherField: setting an allowance
// applies the trigger again by its name with spec.budget.perWeek and no
// status, every other field as the API answered it; zero takes it off;
// below zero reaches no API.
func TestSetTriggerAllowanceKeepsEveryOtherField(t *testing.T) {
	const stored = `{"apiVersion":"topos.latere.ai/v1","kind":"Trigger","metadata":{"name":"event-3f2a9c1b7d4e5f60a1b2","labels":{"applied-by":"latere-chat"}},
"spec":{"agent":"agent_1","on":{"product":"github","verbs":["push"]},"maxActive":123456789012345678,"skipIfActive":false,
"session":{"message":"Run the retry test.","budget":{"maxCost":"0.50"}}%s},"status":{"id":"trg_3"}}`
	c, calls := fakeAPI(t, map[string]string{
		"GET /v1/agents/triggers/trg_3":                      fmt.Sprintf(stored, ""),
		"PUT /v1/agents/triggers/event-3f2a9c1b7d4e5f60a1b2": fmt.Sprintf(stored, `,"budget":{"perWeek":1500000}`),
	})
	got, err := c.SetTriggerAllowance(t.Context(), "trg_3", 1_500_000)
	if err != nil {
		t.Fatal(err)
	}
	if got.Spec.Budget == nil || got.Spec.Budget.PerWeekMicroUSD != 1_500_000 {
		t.Errorf("answered %+v", got.Spec)
	}
	put := (*calls)[1]
	spec, _ := put.Body["spec"].(map[string]any)
	budget, _ := spec["budget"].(map[string]any)
	session, _ := spec["session"].(map[string]any)
	if put.Method != http.MethodPut || budget["perWeek"] != float64(1_500_000) || spec["on"] == nil || session["budget"] == nil {
		t.Errorf("applied %+v", put)
	}
	if _, has := put.Body["status"]; has || !strings.Contains(string(put.Raw), `"maxActive":123456789012345678,`) {
		t.Errorf("applied %s", put.Raw)
	}

	c, calls = fakeAPI(t, map[string]string{
		"GET /v1/agents/triggers/trg_3":                      fmt.Sprintf(stored, `,"budget":{"perWeek":1500000}`),
		"PUT /v1/agents/triggers/event-3f2a9c1b7d4e5f60a1b2": fmt.Sprintf(stored, ""),
	})
	if got, err := c.SetTriggerAllowance(t.Context(), "trg_3", 0); err != nil || got.Spec.Budget != nil {
		t.Fatalf("taken off as %+v, %v", got, err)
	}
	if spec, _ := (*calls)[1].Body["spec"].(map[string]any); spec["budget"] != nil {
		t.Errorf("taking the allowance off applied %v", spec["budget"])
	}

	c, calls = fakeAPI(t, map[string]string{"GET /v1/agents/triggers/trg_3": fmt.Sprintf(stored, `,"budget":"none"`)})
	if _, err := c.SetTriggerAllowance(t.Context(), "trg_3", 1); err == nil || len(*calls) != 1 {
		t.Errorf("a trigger whose budget is no object was applied: %v, after %+v", err, *calls)
	}
	c, calls = fakeAPI(t, map[string]string{
		"GET /v1/agents/triggers/trg_3":                      fmt.Sprintf(stored, `,"budget":null`),
		"PUT /v1/agents/triggers/event-3f2a9c1b7d4e5f60a1b2": fmt.Sprintf(stored, `,"budget":{"perWeek":1}`),
	})
	if _, err := c.SetTriggerAllowance(t.Context(), "trg_3", 1); err != nil {
		t.Fatal(err)
	}
	if spec, _ := (*calls)[1].Body["spec"].(map[string]any); spec["budget"].(map[string]any)["perWeek"] != float64(1) {
		t.Errorf("an allowance over a null budget applied %v", spec["budget"])
	}
	c, calls = fakeAPI(t, nil)
	if _, err := c.SetTriggerAllowance(t.Context(), "trg_3", -1); !errors.Is(err, agents.ErrAllowanceInvalid) || len(*calls) != 0 {
		t.Errorf("below zero: %v, after %+v", err, *calls)
	}
	if _, err := c.SetTriggerAllowance(t.Context(), "trg_missing", 1); !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("a missing trigger: %v", err)
	}
}

// TestAnOffersAllowanceIsRead: a proposal and a change read the weekly
// allowance the call asks for as the trigger tool reads it, a JSON number
// or a string holding one, with at most six digits after the point and
// above zero; a call without one reads none; a resume ignores one; and an
// amount the tool refuses reads as no offer. The amounts are those the
// tool's own decoding was run over, with its answers. An amount past an
// int64 of millionths is refused, where the tool's multiplication wraps.
func TestAnOffersAllowanceIsRead(t *testing.T) {
	offer := func(input string) (*agents.Offer, error) {
		use := &agents.ToolUse{ToolUseID: "toolu_1", Name: agents.ToolTrigger, Verdict: agents.VerdictAsk, Input: json.RawMessage(input)}
		return use.Offer()
	}
	type read struct {
		usd   string
		micro int64
	}
	for amount, want := range map[string]*read{
		`"1.50"`:                 {"1.50", 1_500_000},
		`2`:                      {"2", 2_000_000},
		`0.000001`:               {"0.000001", 1},
		`"0.40"`:                 {"0.40", 400_000},
		`1.000000`:               {"1.000000", 1_000_000},
		`"9223372036854.775807"`: {"9223372036854.775807", 1<<63 - 1},
		`null`:                   {"", 0},
		`" 0.25 "`:               nil,
		`"0"`:                    nil,
		`0`:                      nil,
		`0.0`:                    nil,
		`"-1"`:                   nil,
		`-1`:                     nil,
		`-0.5`:                   nil,
		`"1.1234567"`:            nil,
		`1e3`:                    nil,
		`1E3`:                    nil,
		`1.5e0`:                  nil,
		`"abc"`:                  nil,
		`true`:                   nil,
		`""`:                     nil,
		`".5"`:                   nil,
		`"5."`:                   nil,
		`"+5"`:                   nil,
		`"01.5"`:                 nil,
		`"9223372036854.775808"`: nil,
		`"99999999999999999999"`: nil,
		`18446744073710`:         nil,
	} {
		for _, input := range []string{
			`{"action":"propose","name":"Inbox","ask":"Look.","why":"Daily.","schedule":"0 9 * * *","timeZone":"UTC","perWeekUsd":` + amount + `}`,
			`{"action":"change","id":"trg_1","why":"Another week.","perWeekUsd":` + amount + `}`,
		} {
			o, err := offer(input)
			switch {
			case want == nil && err == nil:
				t.Errorf("an allowance of %s read as %q, %d", amount, o.PerWeekUSD, o.PerWeekMicroUSD)
			case want != nil && err != nil:
				t.Errorf("an allowance of %s: %v", amount, err)
			case want != nil && (o.PerWeekUSD != want.usd || o.PerWeekMicroUSD != want.micro):
				t.Errorf("an allowance of %s reads %q, %d", amount, o.PerWeekUSD, o.PerWeekMicroUSD)
			}
		}
	}
	if o, err := offer(`{"action":"change","id":"trg_1","why":"Mondays instead.","schedule":"0 9 * * 1","timeZone":"UTC"}`); err != nil || o.PerWeekUSD != "" || o.PerWeekMicroUSD != 0 {
		t.Errorf("a change that names no allowance reads %+v, %v", o, err)
	}
	if o, err := offer(`{"action":"resume","id":"trg_1","perWeekUsd":"3"}`); err != nil || o.PerWeekUSD != "" || o.PerWeekMicroUSD != 0 {
		t.Errorf("a resume reads %+v, %v", o, err)
	}
}
