// SPDX-FileCopyrightText: 2026 Latere AI
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
	member = "https://auth.example|usr_7"
	// own is a member's own profile in an organization, as the API answers
	// it.
	own = `{"org":"org_1","subject":"` + member + `","costs":{"false_allow":50,"false_block":4,"ask":1},"audit_rate":0.1,
"certify":{"alpha":0.01,"delta":0.05},"thresholds":{"allow_above":0.98,"block_below":0.25}}`
	// report is a report of three weeks: one with too few members, one
	// ready and divided by agent, and one open, with a field the package
	// does not model.
	report = `{"org":"org_1","from":"2026-09-28T00:00:00Z","to":"2026-10-19T00:00:00Z","min_subjects":5,"weeks":[
{"from":"2026-09-28T00:00:00Z","to":"2026-10-05T00:00:00Z","state":"too_few"},
{"from":"2026-10-05T00:00:00Z","to":"2026-10-12T00:00:00Z","state":"ready",
 "total":{"subjects":12,"decisions":{"allow":1610,"flag":92,"ask":212,"block":9},"flag_reviews":{"reviews":81,"denied":2},
  "applied":{"false_allow":{"value":0.021,"interval":[0.006,0.07],"covered":1,"reviews":81,"audits":81,"unanswered":11}},
  "learned":{"false_allow":{"value":null,"covered":0,"reviews":0,"audits":0,"unanswered":0}},
  "withheld":["applied.false_block","learned.false_block"]},
 "agents":[{"agent":"agt_release","subjects":9,"decisions":{"allow":1200,"flag":70,"ask":150,"block":6},"flag_reviews":{"reviews":63},"applied":{},"learned":{},
  "withheld":["flag_reviews.denied","applied.false_allow","applied.false_block","learned.false_allow","learned.false_block"]}],
 "other":{"subjects":7,"decisions":{"allow":410,"flag":22,"ask":62,"block":3},"flag_reviews":{"reviews":18},"applied":{},"learned":{},
  "withheld":["flag_reviews.denied","applied.false_allow","applied.false_block","learned.false_allow","learned.false_block"]}},
{"from":"2026-10-12T00:00:00Z","to":"2026-10-19T00:00:00Z","state":"open"}],"added_later":true}`
)

// A member's own profile in an organization is read and saved with its
// subject, and a read or a save of the organization's sends none.
func TestPersonProfile(t *testing.T) {
	c, calls := fakeAPI(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) { answer(w, http.StatusOK, own) })
	got, err := c.GetProfile(t.Context(), oversight.Scope{Org: "org_1", Subject: member})
	if err != nil {
		t.Fatal(err)
	}
	if got.Subject != member || got.Thresholds.AllowAbove != 0.98 || got.AuditRate != 0.1 {
		t.Errorf("profile %+v", got)
	}
	if _, err := c.PutProfile(t.Context(), &oversight.Profile{Org: "org_1", Subject: member, Costs: oversight.Costs{FalseAllow: 50, FalseBlock: 4, Ask: 1}, AuditRate: 0.1}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetProfile(t.Context(), oversight.Scope{Org: "org_1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PutProfile(t.Context(), &oversight.Profile{Org: "org_1", Costs: oversight.Costs{FalseAllow: 10, FalseBlock: 4, Ask: 1}}); err != nil {
		t.Fatal(err)
	}
	cs := calls()
	if cs[0].query["subject"] != member || cs[0].query["org"] != "org_1" || len(cs[0].query) != 2 {
		t.Errorf("get %+v", cs[0])
	}
	if cs[1].body["subject"] != member {
		t.Errorf("put %+v", cs[1])
	}
	if _, ok := cs[2].query["subject"]; ok {
		t.Errorf("a read of the organization's profile sent a subject: %+v", cs[2])
	}
	if _, ok := cs[3].body["subject"]; ok {
		t.Errorf("a save of the organization's profile sent a subject: %+v", cs[3].body)
	}
}

// Removing a profile names its scope in the query, is no error when none
// is saved, and is retried like a read.
func TestDeleteProfile(t *testing.T) {
	c, calls := fakeAPI(t, 1, failOnce("", http.StatusNoContent))
	if err := c.DeleteProfile(t.Context(), oversight.Scope{Org: "org_1", Agent: "agt_release"}); err != nil {
		t.Fatal(err)
	}
	cs := calls()
	if len(cs) != 2 || cs[1].method != http.MethodDelete || cs[1].path != "/v1/oversight/profiles" ||
		cs[1].query["org"] != "org_1" || cs[1].query["agent"] != "agt_release" || len(cs[1].query) != 2 || cs[1].body != nil {
		t.Errorf("calls %+v", cs)
	}
	mine, mineCalls := fakeAPI(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) { w.WriteHeader(http.StatusNoContent) })
	if err := mine.DeleteProfile(t.Context(), oversight.Scope{Org: "org_1", Subject: member}); err != nil {
		t.Fatal(err)
	}
	if q := mineCalls()[0].query; q["subject"] != member || q["kind"] != "" || q["agent"] != "" {
		t.Errorf("query %+v", q)
	}
	no, _ := fakeAPI(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) { refuse(w, http.StatusForbidden, "forbidden") })
	if err := no.DeleteProfile(t.Context(), oversight.Scope{Org: "org_1", Subject: "https://auth.example|usr_8"}); !errors.Is(err, latere.ErrForbidden) {
		t.Errorf("another member's profile: %v", err)
	}
}

func TestGetReport(t *testing.T) {
	from, to := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	c, calls := fakeAPI(t, 1, failOnce(report, http.StatusOK))
	got, err := c.GetReport(t.Context(), &oversight.ReportParams{Org: "org_1", From: from, To: to.In(time.FixedZone("CET", 3600))})
	if err != nil {
		t.Fatal(err)
	}
	if got.Org != "org_1" || got.MinMembers != oversight.ReportMinMembers || len(got.Weeks) != 3 || !strings.Contains(string(got.RawJSON()), "added_later") {
		t.Fatalf("report %+v", got)
	}
	few, ready, open := got.Weeks[0], got.Weeks[1], got.Weeks[2]
	if few.State != oversight.WeekTooFew || few.Total != nil || few.Agents != nil || few.Other != nil || !few.To.Equal(time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("too few %+v", few)
	}
	if open.State != oversight.WeekOpen || open.Total != nil {
		t.Errorf("open %+v", open)
	}
	if ready.State != oversight.WeekReady || ready.Total == nil || ready.Other == nil || len(ready.Agents) != 1 {
		t.Fatalf("ready %+v", ready)
	}
	tot := ready.Total
	if tot.Members != 12 || tot.Decisions.Allow != 1610 || tot.Decisions.Block != 9 || tot.FlagChecks.Checks != 81 || tot.FlagChecks.Denied == nil || *tot.FlagChecks.Denied != 2 {
		t.Errorf("total %+v", tot)
	}
	if fa := tot.Applied.FalseAllow; fa == nil || fa.Value == nil || *fa.Value != 0.021 || fa.Interval == nil || fa.Unanswered != 11 {
		t.Errorf("applied %+v", tot.Applied)
	}
	if tot.Applied.FalseBlock != nil || tot.Learned.FalseAllow == nil || tot.Learned.FalseAllow.Value != nil || len(tot.Withheld) != 2 {
		t.Errorf("a withheld or an unknown rate: %+v, %+v, %v", tot.Applied, tot.Learned, tot.Withheld)
	}
	a := ready.Agents[0]
	if a.Agent != "agt_release" || a.Members != 9 || a.FlagChecks.Checks != 63 || a.FlagChecks.Denied != nil || a.Applied.FalseAllow != nil || len(a.Withheld) != 5 {
		t.Errorf("agent %+v", a)
	}
	if ready.Other.Members != 7 || ready.Other.Decisions.Ask+a.Decisions.Ask != tot.Decisions.Ask {
		t.Errorf("other %+v", ready.Other)
	}
	cs := calls()
	if len(cs) != 2 || cs[1].method != http.MethodGet || cs[1].path != "/v1/oversight/reports" || cs[1].query["org"] != "org_1" ||
		cs[1].query["from"] != "2026-09-28T00:00:00Z" || cs[1].query["to"] != "2026-10-19T00:00:00Z" {
		t.Errorf("calls %+v", cs)
	}
}

// A member who may not read the report, and weeks the API refuses, come
// back as the API's errors.
func TestGetReportRefusals(t *testing.T) {
	from, to := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	no, _ := fakeAPI(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) { refuse(w, http.StatusForbidden, "forbidden") })
	if _, err := no.GetReport(t.Context(), &oversight.ReportParams{Org: "org_1", From: from, To: to}); !errors.Is(err, latere.ErrForbidden) {
		t.Errorf("a member: %v", err)
	}
	bad, _ := fakeAPI(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) { refuse(w, http.StatusBadRequest, "invalid_query") })
	_, err := bad.GetReport(t.Context(), &oversight.ReportParams{Org: "org_1", From: from.Add(time.Hour), To: to})
	var apiErr *latere.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest || apiErr.Code != "invalid_query" {
		t.Errorf("a bound that is not a Monday: %v", err)
	}
	for _, p := range []*oversight.ReportParams{nil, {From: from, To: to}, {Org: "org_1", To: to}, {Org: "org_1", From: to, To: from}} {
		if _, err := no.GetReport(t.Context(), p); err == nil || !strings.HasPrefix(err.Error(), "oversight: ") {
			t.Errorf("%+v: %v", p, err)
		}
	}
	if err := no.DeleteProfile(t.Context(), oversight.Scope{Subject: member}); err == nil || !strings.HasPrefix(err.Error(), "oversight: ") {
		t.Errorf("a removal with no Org: %v", err)
	}
	undecodable, _ := fakeAPI(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) { answer(w, http.StatusOK, `[]`) })
	if _, err := undecodable.GetReport(t.Context(), &oversight.ReportParams{Org: "org_1", From: from, To: to}); err == nil {
		t.Error("a list decoded as a report")
	}
}
