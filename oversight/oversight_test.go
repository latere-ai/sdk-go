// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package oversight_test

import (
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"latere.ai/x/sdk-go/latere"
	"latere.ai/x/sdk-go/oversight"
)

const (
	me     = "https://auth.example|usr_42"
	action = "ses_01JA3BQ7Z4M8N2P5R6S7T8V9WX/toolu_01"

	// learned is the learned view as the API answers it, with a field the
	// package does not model.
	learned = `{"org":"` + me + `","subject":"` + me + `","entries":[
{"label":"bash go test","kind":"tool","name":"bash","approved":31,"denied":0,"approve":0.96,"uncertainty":0.2,"verdict":"allow","evidence_ratio":0.9,"thresholds":{"allow_above":0.9,"block_below":0.25},
 "cover":{"covered":true,"weight":12,"sessions":5,"min_weight":3,"min_sessions":2}},
{"label":"bash git push","kind":"tool","name":"bash","approved":2,"denied":0,"approve":0.58,"uncertainty":1.1,"verdict":"ask","evidence_ratio":0.2,"ask_kind":"unsure","thresholds":{"allow_above":0.9,"block_below":0.25}}],"model":{"source":"learned/1","answers":33},
"recent":[{"t":10,"at":"2026-10-20T09:00:00Z","value":-0.2},{"t":40,"at":"2026-11-01T09:00:00Z","value":0.4}],"added_later":1}`
	// explanation is an explanation of an ask the person is split on.
	explanation = `{"org":"` + me + `","subject":"` + me + `","action_id":"` + action + `","model":{"source":"learned/1","answers":40},"predicted_by":{"source":"learned/1","answers":38},"approve":0.52,"uncertainty":0.4,
"verdict":"ask","reason":"Your answers about steps like this do not agree.","mean":0.07,"parts":{"action":0.05,"recent":0.1,"interaction":-0.08},"evidence_ratio":0.81,"ask_kind":"split","thresholds":{"allow_above":0.9,"block_below":0.25},
"evidence":[{"action_id":"ses_1/toolu_9","label":"bash npm install","at":"2026-10-30T10:00:00Z","approve":true,"share":0.19},
{"action_id":"ses_2/toolu_4","label":"bash npm install","at":"2026-10-29T10:00:00Z","approve":false,"share":0.18}]}`
	profile = `{"org":"` + me + `","costs":{"false_allow":10,"false_block":4,"ask":1},"audit_rate":0.05,"certify":{"alpha":0.01,"delta":0.05},
"consent":{"min_weight":5,"min_sessions":3},"thresholds":{"allow_above":0.9,"block_below":0.25},"default":true}`
	preview = `{"org":"` + me + `","subject":"` + me + `","from":"2026-10-25T00:00:00Z","to":"2026-11-01T00:00:00Z","costs":{"false_allow":50,"false_block":4,"ask":1},"audit_rate":0.05,
"thresholds":{"allow_above":0.98,"block_below":0.25},"records":39,"would":{"allow":23,"ask":14,"block":2},"answered":30,"allowed_denied":1,"blocked_approved":0,"expected_asks":15.2}`
)

// A call is one request the fake API received.
type call struct {
	method, path string
	query        map[string]string
	body         map[string]any
}

// fakeAPI is a Client of a server that records each request and answers
// it with answer, which learns how many requests came before it. It
// allows retries, with no wait between them.
func fakeAPI(t *testing.T, retries int, answer func(w http.ResponseWriter, r *http.Request, n int)) (*oversight.Client, func() []call) {
	t.Helper()
	var mu sync.Mutex
	var calls []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		c := call{method: r.Method, path: r.URL.EscapedPath(), query: map[string]string{}}
		for k := range r.URL.Query() {
			c.query[k] = r.URL.Query().Get(k)
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &c.body); err != nil {
				t.Errorf("a body that is not a JSON object: %s", raw)
			}
		}
		mu.Lock()
		n := len(calls)
		calls = append(calls, c)
		mu.Unlock()
		answer(w, r, n)
	}))
	t.Cleanup(srv.Close)
	cfg, err := latere.NewConfig(latere.WithBaseURL(srv.URL), latere.WithTokenSource(latere.StaticToken("tok")),
		latere.WithMaxRetries(retries), latere.WithBackoff(time.Millisecond, time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	return oversight.New(cfg), func() []call {
		mu.Lock()
		defer mu.Unlock()
		return append([]call(nil), calls...)
	}
}

func answer(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func refuse(w http.ResponseWriter, status int, code string) {
	answer(w, status, `{"error":{"code":"`+code+`","message":"No.","details":{"request_id":"req_1"}}}`)
}

// failOnce answers 503 to the first request and body after it.
func failOnce(body string, status int) func(w http.ResponseWriter, r *http.Request, n int) {
	return func(w http.ResponseWriter, _ *http.Request, n int) {
		if n == 0 {
			refuse(w, http.StatusServiceUnavailable, "unavailable")
			return
		}
		if body == "" {
			w.WriteHeader(status)
			return
		}
		answer(w, status, body)
	}
}

func TestSubjectAndActionID(t *testing.T) {
	if got := oversight.Subject("https://auth.example/", "usr_42"); got != me {
		t.Errorf("Subject = %q", got)
	}
	if got := oversight.ActionID("ses_1", "toolu_2"); got != "ses_1/toolu_2" {
		t.Errorf("ActionID = %q", got)
	}
}

func TestGetLearned(t *testing.T) {
	c, calls := fakeAPI(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) { answer(w, http.StatusOK, learned) })
	got, err := c.GetLearned(t.Context(), &oversight.LearnedParams{Org: me, Subject: me, Agent: "agt_1", Kind: "tool"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Kinds) != 2 || got.Kinds[0].Label != "bash go test" || got.Kinds[0].Approved != 31 || got.Kinds[1].Verdict != oversight.VerdictAsk ||
		got.Kinds[1].AskKind != oversight.AskUnsure || got.Kinds[0].Thresholds.AllowAbove != 0.9 || got.Model.Answers != 33 {
		t.Errorf("kinds %+v", got.Kinds)
	}
	if cv := got.Kinds[0].Cover; cv == nil || !cv.Covered || cv.Weight != 12 || cv.Sessions != 5 || cv.MinWeight != 3 || cv.MinSessions != 2 || got.Kinds[1].Cover != nil {
		t.Errorf("covers %+v %+v", got.Kinds[0].Cover, got.Kinds[1].Cover)
	}
	if len(got.Recent) != 2 || got.Recent[1].T != 40 || got.Recent[1].Value != 0.4 || !got.Recent[1].At.Equal(time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("recent %+v", got.Recent)
	}
	if !strings.Contains(string(got.RawJSON()), "added_later") {
		t.Errorf("RawJSON lost a field: %s", got.RawJSON())
	}
	cs := calls()
	if len(cs) != 1 || cs[0].method != http.MethodGet || cs[0].path != "/v1/oversight/learned" ||
		cs[0].query["org"] != me || cs[0].query["subject"] != me || cs[0].query["agent"] != "agt_1" || cs[0].query["kind"] != "tool" {
		t.Errorf("calls %+v", cs)
	}
}

func TestGetExplanation(t *testing.T) {
	c, calls := fakeAPI(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) { answer(w, http.StatusOK, explanation) })
	got, err := c.GetExplanation(t.Context(), &oversight.ExplanationParams{Org: me, Subject: me, ActionID: action})
	if err != nil {
		t.Fatal(err)
	}
	if got.AskKind != oversight.AskSplit || got.Parts == nil || got.Parts.Recent != 0.1 || got.EvidenceRatio != 0.81 ||
		got.Model.Answers != 40 || got.PredictedBy == nil || got.PredictedBy.Answers != 38 || got.Thresholds.AllowAbove != 0.9 {
		t.Errorf("explanation %+v", got)
	}
	if got.Reason != "Your answers about steps like this do not agree." {
		t.Errorf("reason %q", got.Reason)
	}
	if len(got.Evidence) != 2 || !got.Evidence[0].Approve || got.Evidence[1].Approve || got.Evidence[1].Share != 0.18 {
		t.Errorf("evidence %+v", got.Evidence)
	}
	if len(got.RawJSON()) == 0 {
		t.Error("no RawJSON")
	}
	if cs := calls(); cs[0].path != "/v1/oversight/explanations" || cs[0].query["action_id"] != action {
		t.Errorf("calls %+v", cs)
	}
}

// A model without parts answers none, and the explanation says so.
func TestExplanationWithoutParts(t *testing.T) {
	c, _ := fakeAPI(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) {
		answer(w, http.StatusOK, `{"org":"o","subject":"s","action_id":"a/b","verdict":"allow","approve":0.95,"evidence":[]}`)
	})
	got, err := c.GetExplanation(t.Context(), &oversight.ExplanationParams{Org: "o", Subject: "s", ActionID: "a/b"})
	if err != nil || got.Parts != nil || got.AskKind != "" {
		t.Errorf("%+v %v", got, err)
	}
}

func TestProfiles(t *testing.T) {
	c, calls := fakeAPI(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) { answer(w, http.StatusOK, profile) })
	got, err := c.GetProfile(t.Context(), oversight.Scope{Org: me, Kind: "tool", Agent: "agt_1"})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Default || got.Costs.FalseAllow != 10 || got.Thresholds.AllowAbove != 0.9 || got.Certify == nil || got.Certify.Delta != 0.05 || len(got.RawJSON()) == 0 ||
		got.Consent == nil || got.Consent.MinWeight != 5 || got.Consent.MinSessions != 3 {
		t.Errorf("profile %+v", got)
	}
	saved, err := c.PutProfile(t.Context(), &oversight.Profile{Org: me, Costs: oversight.Costs{FalseAllow: 2.857, FalseBlock: 2.857, Ask: 1}, AuditRate: 0.05})
	if err != nil || saved == nil {
		t.Fatal(saved, err)
	}
	cs := calls()
	if cs[0].path != "/v1/oversight/profiles" || cs[0].query["kind"] != "tool" || cs[0].query["agent"] != "agt_1" {
		t.Errorf("get %+v", cs[0])
	}
	if cs[1].method != http.MethodPut || cs[1].body["org"] != me || cs[1].body["audit_rate"] != 0.05 {
		t.Errorf("put %+v", cs[1])
	}
	if costs, _ := cs[1].body["costs"].(map[string]any); costs["false_allow"] != 2.857 || costs["ask"] != 1.0 {
		t.Errorf("put costs %+v", cs[1].body)
	}
	if _, ok := cs[1].body["kind"]; ok {
		t.Errorf("an empty kind is sent: %+v", cs[1].body)
	}
	if _, ok := cs[1].body["consent"]; ok {
		t.Errorf("a profile without consent sends it: %+v", cs[1].body)
	}
}

// A profile read and saved again keeps its settings of the cover rule.
func TestAProfileKeepsItsConsent(t *testing.T) {
	c, calls := fakeAPI(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) { answer(w, http.StatusOK, profile) })
	got, err := c.GetProfile(t.Context(), oversight.Scope{Org: me})
	if err != nil {
		t.Fatal(err)
	}
	got.Costs.Ask = 2
	if _, err := c.PutProfile(t.Context(), got); err != nil {
		t.Fatal(err)
	}
	consent, _ := calls()[1].body["consent"].(map[string]any)
	if consent["min_weight"] != 5.0 || consent["min_sessions"] != 3.0 {
		t.Errorf("put %+v", calls()[1].body)
	}
}

// Saving a profile sets values only, so a failure is retried.
func TestPutProfileRetries(t *testing.T) {
	c, calls := fakeAPI(t, 1, failOnce(profile, http.StatusOK))
	if _, err := c.PutProfile(t.Context(), &oversight.Profile{Org: me, Costs: oversight.Costs{FalseAllow: 10, FalseBlock: 4, Ask: 1}}); err != nil {
		t.Fatal(err)
	}
	if n := len(calls()); n != 2 {
		t.Errorf("%d calls, want 2", n)
	}
}

func TestPreview(t *testing.T) {
	from, to := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	c, calls := fakeAPI(t, 1, failOnce(preview, http.StatusOK))
	got, err := c.Preview(t.Context(), &oversight.PreviewParams{Org: me, Subject: me, From: from, To: to, Costs: oversight.Costs{FalseAllow: 50, FalseBlock: 4, Ask: 1}, AuditRate: 0.05})
	if err != nil {
		t.Fatal(err)
	}
	if got.Would[oversight.VerdictAllow] != 23 || got.Would[oversight.VerdictBlock] != 2 || got.AllowedDenied != 1 || got.ExpectedAsks != 15.2 ||
		got.Records != 39 || got.Answered != 30 || got.Thresholds.AllowAbove != 0.98 || len(got.RawJSON()) == 0 {
		t.Errorf("preview %+v", got)
	}
	if got.Org != me || got.Subject != me || !got.From.Equal(from) || !got.To.Equal(to) || got.Costs.FalseAllow != 50 || got.AuditRate != 0.05 {
		t.Errorf("the preview's request %+v", got)
	}
	cs := calls()
	if len(cs) != 2 || cs[1].method != http.MethodPost || cs[1].path != "/v1/oversight/previews" || cs[1].body["from"] != "2026-10-25T00:00:00Z" {
		t.Errorf("calls %+v", cs)
	}
}

// An answer counts each time it is received, so it is never sent again.
func TestAnswerIsSentOnce(t *testing.T) {
	c, calls := fakeAPI(t, 3, failOnce("", http.StatusNoContent))
	err := c.Answer(t.Context(), &oversight.AnswerParams{Org: me, Subject: me, ActionID: action, Approve: true})
	if !errors.Is(err, oversight.ErrUnavailable) {
		t.Errorf("err %v", err)
	}
	if n := len(calls()); n != 1 {
		t.Errorf("%d calls, want 1", n)
	}
	ok, okCalls := fakeAPI(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) { w.WriteHeader(http.StatusNoContent) })
	if err := ok.Answer(t.Context(), &oversight.AnswerParams{Org: me, Subject: me, ActionID: action, Approve: false}); err != nil {
		t.Fatal(err)
	}
	b := okCalls()[0].body
	if okCalls()[0].path != "/v1/oversight/answers" || b["approve"] != false || b["action_id"] != action {
		t.Errorf("body %+v", b)
	}
	if _, ok := b["by"]; ok {
		t.Errorf("an empty by is sent: %+v", b)
	}
}

// Forgetting twice is forgetting once, so a failure is retried.
func TestForget(t *testing.T) {
	c, calls := fakeAPI(t, 1, failOnce("", http.StatusNoContent))
	if err := c.Forget(t.Context(), &oversight.ForgetParams{Org: me, Subject: me}); err != nil {
		t.Fatal(err)
	}
	cs := calls()
	if len(cs) != 2 || cs[1].path != "/v1/oversight/subjects/forget" || cs[1].body["subject"] != me {
		t.Errorf("calls %+v", cs)
	}
}

func TestRefusals(t *testing.T) {
	cases := []struct {
		status   int
		code     string
		sentinel error
	}{
		{http.StatusUnprocessableEntity, "ask_too_costly", oversight.ErrAskTooCostly},
		{http.StatusConflict, "no_decision", oversight.ErrNoDecision},
		{http.StatusServiceUnavailable, "unavailable", oversight.ErrUnavailable},
	}
	for _, tc := range cases {
		c, _ := fakeAPI(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) { refuse(w, tc.status, tc.code) })
		err := c.Answer(t.Context(), &oversight.AnswerParams{Org: me, Subject: me, ActionID: action})
		var apiErr *latere.Error
		if !errors.Is(err, tc.sentinel) || !errors.As(err, &apiErr) || apiErr.Code != tc.code || err.Error() == "" {
			t.Errorf("%s: %v", tc.code, err)
		}
	}
	// A code with no sentinel comes back as the API's error alone.
	c, _ := fakeAPI(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) { refuse(w, http.StatusForbidden, "forbidden") })
	_, err := c.GetLearned(t.Context(), &oversight.LearnedParams{Org: me, Subject: "https://auth.example|usr_99"})
	if !errors.Is(err, latere.ErrForbidden) || errors.Is(err, oversight.ErrNoDecision) {
		t.Errorf("forbidden: %v", err)
	}
}

// A call missing what names the person or the step is refused before it is
// sent.
func TestMissingNames(t *testing.T) {
	c, calls := fakeAPI(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) { answer(w, http.StatusOK, `{}`) })
	ctx := t.Context()
	errs := []error{}
	_, err := c.GetLearned(ctx, nil)
	errs = append(errs, err)
	_, err = c.GetLearned(ctx, &oversight.LearnedParams{Org: me})
	errs = append(errs, err)
	_, err = c.GetExplanation(ctx, &oversight.ExplanationParams{Org: me, Subject: me})
	errs = append(errs, err)
	_, err = c.GetProfile(ctx, oversight.Scope{})
	errs = append(errs, err)
	_, err = c.PutProfile(ctx, &oversight.Profile{})
	errs = append(errs, err)
	_, err = c.Preview(ctx, &oversight.PreviewParams{Org: me})
	errs = append(errs, err)
	errs = append(errs, c.Answer(ctx, &oversight.AnswerParams{Org: me, Subject: me}))
	errs = append(errs, c.Forget(ctx, &oversight.ForgetParams{Subject: me}))
	for i, err := range errs {
		if err == nil || !strings.HasPrefix(err.Error(), "oversight: ") {
			t.Errorf("call %d: %v", i, err)
		}
	}
	if n := len(calls()); n != 0 {
		t.Errorf("%d calls were sent", n)
	}
}

// An answer that is not the model's JSON is an error, not a zero value.
func TestUndecodable(t *testing.T) {
	bad, _ := fakeAPI(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) { answer(w, http.StatusOK, `{"entries":"x"}`) })
	if _, err := bad.GetLearned(t.Context(), &oversight.LearnedParams{Org: me, Subject: me}); err == nil {
		t.Error("entries that are not a list decoded")
	}
	for _, body := range []string{`[]`, `"x"`} {
		c, _ := fakeAPI(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) { answer(w, http.StatusOK, body) })
		if _, err := c.GetLearned(t.Context(), &oversight.LearnedParams{Org: me, Subject: me}); err == nil {
			t.Errorf("%s decoded", body)
		}
		if _, err := c.GetExplanation(t.Context(), &oversight.ExplanationParams{Org: me, Subject: me, ActionID: action}); err == nil {
			t.Errorf("%s decoded as an explanation", body)
		}
		if _, err := c.GetProfile(t.Context(), oversight.Scope{Org: me}); err == nil {
			t.Errorf("%s decoded as a profile", body)
		}
		if _, err := c.Preview(t.Context(), &oversight.PreviewParams{Org: me, Subject: me}); err == nil {
			t.Errorf("%s decoded as a preview", body)
		}
	}
}
