// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package oversight_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"latere.ai/x/sdk-go/oversight"
)

// casesWithCover holds a step whose scope no approval covers, a wider ask,
// and one the person allowed in too few sessions, a few ask, with a field
// of its scope the package reads as not known.
const casesWithCover = `{"entries":[
{"action":{"org":"` + me + `","subject":"` + me + `","action_id":"` + action + `","kind":"tool","name":"publish","effect":"external"},
 "prediction":{"verdict":"ask","approve":0.73,"uncertainty":0.4,"evidence_ratio":0.54,"ask_kind":"wider","thresholds":{"allow_above":0.65,"block_below":0.35},"audit_rate":0.05,"source":"learned/1",
  "reason":"This person never allowed an action of this reach: publish: make public. Limen suggests ask.",
  "scope":{"undo":"irreversible","crosses":{"machine":false,"remote":true,"network":"unknown","public":true},"target_class":"unknown","target_kind":"app","contact":"first"},
  "cover":{"covered":false,"weight":0,"sessions":0,"min_weight":3,"min_sessions":2}}},
{"action":{"org":"` + me + `","subject":"` + me + `","action_id":"ses_2/toolu_3","kind":"tool","name":"publish","effect":"external"},
 "prediction":{"verdict":"ask","approve":0.81,"uncertainty":0.3,"evidence_ratio":0.7,"ask_kind":"few","thresholds":{"allow_above":0.65,"block_below":0.35},"audit_rate":0.05,"source":"learned/1",
  "scope":{"undo":"recoverable","crosses":{"remote":"yes","public":"no"},"target_class":"own","target_kind":"app","contact":"repeat"},
  "cover":{"covered":false,"weight":2,"sessions":1,"min_weight":3,"min_sessions":2}}},
{"action":{"org":"` + me + `","subject":"` + me + `","action_id":"ses_3/toolu_1","kind":"tool","name":"read","effect":"read"},
 "prediction":{"verdict":"allow","approve":0.97,"uncertainty":0.2,"thresholds":{"allow_above":0.65,"block_below":0.35},"audit_rate":0.05,"source":"learned/1"}}]}`

// A case's prediction reads the scope the decision service compared and
// how far the person's approvals cover it, and one from a service that
// sends neither reads with none.
func TestAPredictionReadsItsScopeAndCover(t *testing.T) {
	c, _ := fakeAPI(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) { answer(w, http.StatusOK, casesWithCover) })
	page, err := c.ListCasesPage(t.Context(), &oversight.CasesParams{Org: me, Subject: me})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 {
		t.Fatalf("%d cases", len(page.Items))
	}
	wider := page.Items[0].Prediction
	if wider.AskKind != oversight.AskWider {
		t.Errorf("ask kind %q", wider.AskKind)
	}
	s := wider.Scope
	if s == nil || s.Undo != oversight.UndoIrreversible || s.TargetClass != oversight.Unknown || s.TargetKind != "app" || s.Contact != oversight.ContactFirst {
		t.Fatalf("scope %+v", s)
	}
	x := s.Crosses
	if x.Machine == nil || *x.Machine || x.Remote == nil || !*x.Remote || x.Network != nil || x.Organization != nil || x.Public == nil || !*x.Public {
		t.Errorf("crosses %+v", x)
	}
	if cv := wider.Cover; cv == nil || cv.Covered || cv.Weight != 0 || cv.Sessions != 0 || cv.MinWeight != 3 || cv.MinSessions != 2 || cv.Nearest != nil {
		t.Errorf("cover %+v", wider.Cover)
	}

	few := page.Items[1].Prediction
	if few.AskKind != oversight.AskFew || few.Cover == nil || few.Cover.Weight != 2 || few.Cover.Sessions != 1 {
		t.Errorf("few: %q, %+v", few.AskKind, few.Cover)
	}
	if fx := few.Scope.Crosses; fx.Remote == nil || !*fx.Remote || fx.Public == nil || *fx.Public || few.Scope.TargetClass != oversight.TargetOwn || few.Scope.Undo != oversight.UndoRecoverable {
		t.Errorf("few: scope %+v, crosses %+v", few.Scope, fx)
	}

	if old := page.Items[2].Prediction; old.Scope != nil || old.Cover != nil {
		t.Errorf("an older prediction: %+v %+v", old.Scope, old.Cover)
	}
}

// A boundary is read as true or false, or as yes or no; null, an unknown
// word and a boundary left out are not known; a number is refused.
func TestCrossesReadsEachWayABoundaryIsWritten(t *testing.T) {
	var x oversight.Crosses
	if err := json.Unmarshal([]byte(`{"machine":true,"remote":"yes","network":false,"organization":"no","public":null}`), &x); err != nil {
		t.Fatal(err)
	}
	if x.Machine == nil || !*x.Machine || x.Remote == nil || !*x.Remote || x.Network == nil || *x.Network || x.Organization == nil || *x.Organization || x.Public != nil {
		t.Errorf("crosses %+v", x)
	}
	var none oversight.Crosses
	if err := json.Unmarshal([]byte(`{"machine":"maybe"}`), &none); err != nil || none.Machine != nil {
		t.Errorf("an unknown word: %+v, %v", none, err)
	}
	if err := json.Unmarshal([]byte(`null`), &none); err != nil || none.Machine != nil {
		t.Errorf("null: %+v, %v", none, err)
	}
	for _, bad := range []string{`{"public":1}`, `[]`} {
		if err := json.Unmarshal([]byte(bad), &none); err == nil {
			t.Errorf("%s was read", bad)
		}
	}
}

// An explanation reads its cover, with the nearest approvals whose scope
// the step exceeds, the closest first.
func TestAnExplanationReadsItsCover(t *testing.T) {
	raw := `{"org":"` + me + `","subject":"` + me + `","action_id":"` + action + `","model":{"source":"learned/1","answers":3},"approve":0.73,"uncertainty":0.4,"verdict":"ask",
"mean":0.6,"evidence_ratio":0.54,"ask_kind":"wider","thresholds":{"allow_above":0.65,"block_below":0.35},"evidence":[],
"cover":{"covered":false,"weight":0,"sessions":0,"min_weight":3,"min_sessions":2,
 "nearest":[{"action_id":"ses_1/toolu_2","label":"publish: update","at":"2026-11-01T09:00:00Z"},{"action_id":"ses_0/toolu_5","label":"publish: production","at":"2026-10-30T09:00:00Z"}]}}`
	c, _ := fakeAPI(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) { answer(w, http.StatusOK, raw) })
	e, err := c.GetExplanation(t.Context(), &oversight.ExplanationParams{Org: me, Subject: me, ActionID: action})
	if err != nil {
		t.Fatal(err)
	}
	if e.AskKind != oversight.AskWider || e.Cover == nil || e.Cover.Covered || len(e.Cover.Nearest) != 2 {
		t.Fatalf("explanation %+v, cover %+v", e, e.Cover)
	}
	n := e.Cover.Nearest[0]
	if n.ActionID != "ses_1/toolu_2" || n.Label != "publish: update" || n.At.IsZero() {
		t.Errorf("nearest %+v", n)
	}
}

// An answer sends how the person answered only where the caller set it.
func TestAnAnswerSaysHowThePersonAnswered(t *testing.T) {
	c, calls := fakeAPI(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) { w.WriteHeader(http.StatusNoContent) })
	ms, waited, opened, shown := int64(3100), int64(9000), true, false
	full := &oversight.AnswerParams{Org: me, Subject: me, ActionID: action, Approve: true,
		AnswerMS: &ms, WaitedMS: &waited, DetailsOpened: &opened, SuggestionShown: &shown, Surface: oversight.SurfaceQueue, Remembered: true}
	if err := c.Answer(t.Context(), full); err != nil {
		t.Fatal(err)
	}
	if err := c.Answer(t.Context(), &oversight.AnswerParams{Org: me, Subject: me, ActionID: action, Approve: false, Surface: oversight.SurfacePrompt}); err != nil {
		t.Fatal(err)
	}
	got := calls()
	if len(got) != 2 {
		t.Fatalf("sent %d requests", len(got))
	}
	b := got[0].body
	if b["answer_ms"] != float64(3100) || b["waited_ms"] != float64(9000) || b["details_opened"] != true || b["suggestion_shown"] != false ||
		b["surface"] != "queue" || b["remembered"] != true {
		t.Errorf("sent %v", b)
	}
	one := got[1].body
	for _, name := range []string{"answer_ms", "waited_ms", "details_opened", "suggestion_shown", "remembered"} {
		if _, ok := one[name]; ok {
			t.Errorf("sent %s, which the caller did not set: %v", name, one)
		}
	}
	if one["surface"] != "prompt" || one["approve"] != false {
		t.Errorf("sent %v", one)
	}
}

// A time outside its range is refused before anything is sent.
func TestAnAnswerRefusesATimeOutOfRange(t *testing.T) {
	c, calls := fakeAPI(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) { w.WriteHeader(http.StatusNoContent) })
	over, below := int64(oversight.MaxAnswerMS+1), int64(-1)
	for name, p := range map[string]*oversight.AnswerParams{
		"answer over a day": {Org: me, Subject: me, ActionID: action, AnswerMS: &over},
		"answer below 0":    {Org: me, Subject: me, ActionID: action, AnswerMS: &below},
		"waited below 0":    {Org: me, Subject: me, ActionID: action, WaitedMS: &below},
	} {
		if err := c.Answer(t.Context(), p); err == nil {
			t.Errorf("%s was sent", name)
		}
	}
	if n := len(calls()); n != 0 {
		t.Errorf("sent %d requests", n)
	}
}

// The certificate reads how many checks of its clean run are considered
// answers, and one from a service that does not count them reads nil.
func TestTheCertificateCountsConsideredChecks(t *testing.T) {
	var r oversight.Reliability
	if err := json.Unmarshal([]byte(`{"certificate":{"alpha":0.01,"delta":0.05,"clean":120,"needed":299,"holds":false,"considered":31}}`), &r); err != nil {
		t.Fatal(err)
	}
	if r.Certificate.Considered == nil || *r.Certificate.Considered != 31 {
		t.Errorf("considered %v", r.Certificate.Considered)
	}
	var old oversight.Reliability
	if err := json.Unmarshal([]byte(reliability), &old); err != nil || old.Certificate.Considered != nil {
		t.Errorf("an older certificate: %v, %v", old.Certificate.Considered, err)
	}
}

// The ask kinds read as their constants.
func TestTheAskKinds(t *testing.T) {
	for raw, want := range map[string]oversight.AskKind{"unsure": oversight.AskUnsure, "split": oversight.AskSplit, "short": oversight.AskShort, "wider": oversight.AskWider, "few": oversight.AskFew} {
		var k oversight.LearnedKind
		if err := json.Unmarshal([]byte(`{"label":"publish","verdict":"ask","ask_kind":"`+raw+`"}`), &k); err != nil || k.AskKind != want {
			t.Errorf("%s: %q, %v", raw, k.AskKind, err)
		}
	}
}
