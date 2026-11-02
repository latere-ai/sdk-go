// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package oversight_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"latere.ai/x/sdk-go/latere"
	"latere.ai/x/sdk-go/oversight"
)

const (
	// casesPage1 holds a split ask the person was asked about and
	// allowed, and a step decided before the decision service recorded
	// why it asks, with a field the package does not model.
	casesPage1 = `{"entries":[
{"action":{"org":"` + me + `","subject":"` + me + `","action_id":"` + action + `","kind":"tool","name":"bash","effect":"write","actor":{"kind":"agent","id":"agt_1","session":"ses_01JA3BQ7Z4M8N2P5R6S7T8V9WX"}},
 "prediction":{"org":"` + me + `","subject":"` + me + `","action_id":"` + action + `","verdict":"ask","approve":0.55,"uncertainty":0.4,"evidence_ratio":0.8,"ask_kind":"split",
  "thresholds":{"allow_above":0.9,"block_below":0.25},"profile":{"org":"` + me + `"},"audit_rate":0.05,"source":"learned/3","reason":"You allowed and denied steps like this."},
 "decision":{"org":"` + me + `","subject":"` + me + `","action_id":"` + action + `","verdict":"ask","review_probability":1,"source":"learned/3"},
 "answers":[{"approve":true,"by":"` + me + `","at":"2026-11-01T09:00:00Z","review":true}],"added_later":1},
{"action":{"org":"` + me + `","subject":"` + me + `","action_id":"ses_2/toolu_7","kind":"tool","name":"read","effect":"read"},
 "prediction":{"verdict":"allow","approve":0.97,"uncertainty":0.2,"thresholds":{"allow_above":0.9,"block_below":0.25},"audit_rate":0.05,"source":"learned/2"},
 "decision":{"verdict":"allow","review_probability":0.05,"source":"rules/1"}}],"next_cursor":"c2"}`
	casesPage2 = `{"entries":[{"action":{"org":"` + me + `","subject":"` + me + `","action_id":"ses_3/toolu_1","kind":"tool","name":"bash","effect":"write"}}]}`

	reliability = `{"org":"` + me + `","subject":"` + me + `","of":"learned","from":"2026-10-02T00:00:00Z","to":"2026-11-01T00:00:00Z",
"false_allow":{"value":0.02,"interval":[0,0.09],"covered":0.6,"reviews":40,"audits":12,"unanswered":3},
"false_block":{"value":null,"covered":0,"reviews":0,"audits":0,"unanswered":0},
"certificate":{"alpha":0.01,"delta":0.05,"clean":120,"needed":299,"holds":false},"added_later":1}`
)

// The cases come a page at a time, as entries the package reads as
// items, and the iterator follows the cursor.
func TestListCases(t *testing.T) {
	c, calls := fakeAPI(t, 0, func(w http.ResponseWriter, r *http.Request, _ int) {
		if r.URL.Query().Get("cursor") == "c2" {
			answer(w, http.StatusOK, casesPage2)
			return
		}
		answer(w, http.StatusOK, casesPage1)
	})
	page, err := c.ListCasesPage(t.Context(), &oversight.CasesParams{Org: me, Subject: me, Pending: true, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.NextCursor != "c2" {
		t.Fatalf("page %+v", page)
	}
	first := page.Items[0]
	if first.Action.ActionID != action || first.Action.Kind != "tool" || first.Action.Name != "bash" || first.Action.Effect != "write" ||
		first.Action.Actor == nil || first.Action.Actor.ID != "agt_1" {
		t.Errorf("action %+v", first.Action)
	}
	p := first.Prediction
	if p == nil || p.Verdict != oversight.VerdictAsk || p.Approve != 0.55 || p.AskKind != oversight.AskSplit || p.EvidenceRatio == nil || *p.EvidenceRatio != 0.8 ||
		p.Thresholds.AllowAbove != 0.9 || p.AuditRate != 0.05 || p.Source != "learned/3" || p.Reason == "" {
		t.Errorf("prediction %+v", p)
	}
	if d := first.Decision; d == nil || d.Verdict != oversight.VerdictAsk || d.ReviewProbability != 1 || d.Source != "learned/3" {
		t.Errorf("decision %+v", d)
	}
	if len(first.Answers) != 1 || !first.Answers[0].Approve || !first.Answers[0].Review || first.Answers[0].By != me ||
		!first.Answers[0].At.Equal(time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("answers %+v", first.Answers)
	}
	if !strings.Contains(string(first.RawJSON()), "added_later") {
		t.Errorf("RawJSON lost a field: %s", first.RawJSON())
	}
	// A prediction made before Limen recorded the evidence ratio has none.
	if second := page.Items[1]; second.Prediction.EvidenceRatio != nil || second.Prediction.AskKind != "" || len(second.Answers) != 0 {
		t.Errorf("second %+v", second.Prediction)
	}
	cs := calls()
	if cs[0].method != http.MethodGet || cs[0].path != "/v1/oversight/cases" || cs[0].query["org"] != me || cs[0].query["subject"] != me ||
		cs[0].query["pending"] != "true" || cs[0].query["limit"] != "2" {
		t.Errorf("call %+v", cs[0])
	}
	if _, ok := cs[0].query["cursor"]; ok {
		t.Errorf("a first page sends a cursor: %+v", cs[0])
	}

	var ids []string
	for k, err := range c.ListCases(t.Context(), &oversight.CasesParams{Org: me, Subject: me, ActionID: action}) {
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, k.Action.ActionID)
	}
	if len(ids) != 3 || ids[2] != "ses_3/toolu_1" {
		t.Errorf("ids %v", ids)
	}
	cs = calls()
	if len(cs) != 3 || cs[1].query["action_id"] != action || cs[2].query["cursor"] != "c2" {
		t.Errorf("calls %+v", cs)
	}
	if _, ok := cs[1].query["pending"]; ok {
		t.Errorf("pending false is sent: %+v", cs[1])
	}
}

// A prediction names the scope whose trust profile applied to its step:
// the organization's, an agent's own, and, with Subject, one the
// member's own profile made stricter. A prediction recorded without it
// reads as an empty scope.
func TestPredictionProfile(t *testing.T) {
	const page = `{"entries":[
{"action":{"org":"org_1","subject":"` + member + `","action_id":"ses_1/toolu_1","kind":"tool","name":"bash","effect":"write"},
 "prediction":{"verdict":"ask","approve":0.6,"uncertainty":0.3,"thresholds":{"allow_above":0.9,"block_below":0.25},"profile":{"org":"org_1"},"audit_rate":0.05,"source":"learned/1"}},
{"action":{"org":"org_1","subject":"` + member + `","action_id":"ses_1/toolu_2","kind":"tool","name":"bash","effect":"write"},
 "prediction":{"verdict":"ask","approve":0.6,"uncertainty":0.3,"thresholds":{"allow_above":0.65,"block_below":0.35},"profile":{"org":"org_1","kind":"tool","agent":"agt_release"},"audit_rate":0.05,"source":"learned/1"}},
{"action":{"org":"org_1","subject":"` + member + `","action_id":"ses_1/toolu_3","kind":"tool","name":"bash","effect":"write"},
 "prediction":{"verdict":"ask","approve":0.6,"uncertainty":0.3,"thresholds":{"allow_above":0.98,"block_below":0.25},"profile":{"org":"org_1","agent":"agt_release","subject":"` + member + `"},"audit_rate":0.1,"source":"learned/1"}},
{"action":{"org":"org_1","subject":"` + member + `","action_id":"ses_1/toolu_4","kind":"tool","name":"bash","effect":"write"},
 "prediction":{"verdict":"ask","approve":0.6,"uncertainty":0.3,"thresholds":{"allow_above":0.9,"block_below":0.25},"audit_rate":0.05,"source":"learned/1"}}]}`
	c, calls := fakeAPI(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) { answer(w, http.StatusOK, page) })
	got, err := c.ListCasesPage(t.Context(), &oversight.CasesParams{Org: "org_1", Subject: member})
	if err != nil {
		t.Fatal(err)
	}
	want := []oversight.Scope{
		{Org: "org_1"},
		{Org: "org_1", Kind: "tool", Agent: "agt_release"},
		{Org: "org_1", Agent: "agt_release", Subject: member},
		{},
	}
	if len(got.Items) != len(want) {
		t.Fatalf("%d cases, want %d", len(got.Items), len(want))
	}
	for i, k := range got.Items {
		if k.Prediction == nil || k.Prediction.Profile != want[i] {
			t.Errorf("case %d: profile %+v, want %+v", i, k.Prediction, want[i])
		}
	}
	if cs := calls(); len(cs) != 1 || cs[0].query["org"] != "org_1" || cs[0].query["subject"] != member {
		t.Errorf("calls %+v", cs)
	}
}

// An empty list is a page with no items, not nil.
func TestListCasesEmpty(t *testing.T) {
	c, _ := fakeAPI(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) { answer(w, http.StatusOK, `{"entries":null}`) })
	page, err := c.ListCasesPage(t.Context(), &oversight.CasesParams{Org: me, Subject: me})
	if err != nil || page.Items == nil || len(page.Items) != 0 || page.NextCursor != "" {
		t.Errorf("%+v %v", page, err)
	}
}

func TestGetReliability(t *testing.T) {
	from, to := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	c, calls := fakeAPI(t, 1, failOnce(reliability, http.StatusOK))
	got, err := c.GetReliability(t.Context(), &oversight.ReliabilityParams{Org: me, Subject: me, Of: oversight.OfLearned, From: from, To: to})
	if err != nil {
		t.Fatal(err)
	}
	fa := got.FalseAllow
	if fa.Value == nil || *fa.Value != 0.02 || fa.Interval == nil || fa.Interval[1] != 0.09 || fa.Covered != 0.6 || fa.Reviews != 40 || fa.Audits != 12 || fa.Unanswered != 3 {
		t.Errorf("false allow %+v", fa)
	}
	if got.FalseBlock.Value != nil || got.FalseBlock.Interval != nil {
		t.Errorf("an unknown rate decoded as %+v", got.FalseBlock)
	}
	if got.Certificate.Clean != 120 || got.Certificate.Needed != 299 || got.Certificate.Holds || got.Of != oversight.OfLearned || !got.From.Equal(from) {
		t.Errorf("report %+v", got)
	}
	if !strings.Contains(string(got.RawJSON()), "added_later") {
		t.Errorf("RawJSON lost a field: %s", got.RawJSON())
	}
	cs := calls()
	if len(cs) != 2 || cs[1].path != "/v1/oversight/reliability" || cs[1].query["of"] != "learned" ||
		cs[1].query["from"] != "2026-10-02T00:00:00Z" || cs[1].query["to"] != "2026-11-01T00:00:00Z" || cs[1].query["subject"] != me {
		t.Errorf("calls %+v", cs)
	}
}

// A call missing the person or a window is refused before it is sent,
// and a refusal is the API's error.
func TestCasesAndReliabilityRefusals(t *testing.T) {
	c, calls := fakeAPI(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) { answer(w, http.StatusOK, `{}`) })
	ctx := t.Context()
	now := time.Now()
	_, err1 := c.ListCasesPage(ctx, &oversight.CasesParams{Org: me})
	_, err2 := c.ListCasesPage(ctx, nil)
	_, err3 := c.GetReliability(ctx, &oversight.ReliabilityParams{Org: me, Subject: me, From: now.Add(-time.Hour), To: now})
	_, err4 := c.GetReliability(ctx, &oversight.ReliabilityParams{Org: me, Subject: me, Of: oversight.OfApplied, From: now, To: now})
	for i, err := range []error{err1, err2, err3, err4} {
		if err == nil || !strings.HasPrefix(err.Error(), "oversight: ") {
			t.Errorf("call %d: %v", i, err)
		}
	}
	if n := len(calls()); n != 0 {
		t.Errorf("%d calls were sent", n)
	}

	forbidden, _ := fakeAPI(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) { refuse(w, http.StatusForbidden, "forbidden") })
	for _, err := range forbidden.ListCases(ctx, &oversight.CasesParams{Org: me, Subject: "https://auth.example|usr_99"}) {
		var apiErr *latere.Error
		if !errors.Is(err, latere.ErrForbidden) || !errors.As(err, &apiErr) {
			t.Errorf("forbidden: %v", err)
		}
	}
}
