// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"latere.ai/x/sdk-go/agents"
	"latere.ai/x/sdk-go/latere"
)

const trigger = `{"apiVersion":"topos.latere.ai/v1","kind":"Trigger","metadata":{"name":"weekly-digest","displayName":"Weekly digest","labels":{"chat":"routine"}},
"spec":{"agent":"agent_1","schedule":"0 9 * * 1","timeZone":"Europe/Berlin","session":{"message":"Write the digest.","title":"Digest","policy":"continue","key":"digest",
"machine":{"kind":"device","environment":"env_1","workdir":"~/notes"},"resources":[{"type":"repository","url":"https://example.com/r.git","ref":"main"}],
"budget":{"maxCost":"2.50"},"limits":{"turnTimeout":"10m","maxAge":"1h"},"endOnIdle":true},"skipIfActive":true,"maxActive":2,"maxAge":"1h","suspend":false},
"status":{"id":"trg_1","version":3,"digest":"sha256:ab","createdAt":"2026-09-22T12:30:00Z","lastFiredAt":"2026-09-29T07:00:00Z","lastSessionId":"ses_9",
"nextFireAt":"2026-10-06T07:00:00Z","counts":{"started":2,"continued":1,"held":0,"filtered":0,"skippedActive":0,"skippedBusy":0,"skippedLate":0,"refused":0,"failed":0}}}`

const firing = `{"id":"frg_1","trigger_id":"trg_1","origin":"event","event":{"id":"e1","product":"github","verb":"release.published","resource":"example/widgets@v1.4.0"},
"key":"example/widgets@v1.4.0","outcome":"started","session_id":"ses_9","received_at":"2026-09-22T12:40:00Z"}`

func TestGetTrigger(t *testing.T) {
	c, calls := fakeAPI(t, map[string]string{"GET /v1/agents/triggers/weekly-digest": trigger})
	got, err := c.GetTrigger(t.Context(), "weekly-digest")
	if err != nil {
		t.Fatal(err)
	}
	s := got.Spec
	if got.Metadata.DisplayName != "Weekly digest" || s.Schedule != "0 9 * * 1" || s.Session.Policy != agents.SessionPolicyContinue ||
		s.Session.Machine.Kind != agents.MachineDevice || s.Session.Resources[0].Type != agents.ResourceRepository ||
		s.Session.Budget.MaxCost != "2.50" || s.Session.Limits.TurnTimeout != "10m" || !*s.Session.EndOnIdle || !*s.SkipIfActive || *s.MaxActive != 2 {
		t.Errorf("decoded the spec as %+v", s)
	}
	st := got.Status
	if st.ID != "trg_1" || st.Version != 3 || st.CreatedAt.IsZero() || st.NextFireAt == nil || st.LastSessionID != "ses_9" || st.Counts.Started != 2 {
		t.Errorf("decoded the status as %+v", st)
	}
	if (*calls)[0].Method != http.MethodGet {
		t.Errorf("sent %+v", (*calls)[0])
	}
	if _, err := c.GetTrigger(t.Context(), "missing"); !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("a missing trigger: %v", err)
	}
	if _, err := c.GetTrigger(t.Context(), ""); err == nil {
		t.Error("an empty ref was sent")
	}
}

func TestListTriggersPagesWithTheOwner(t *testing.T) {
	c, calls := fakeAPI(t, map[string]string{"GET /v1/agents/triggers": `{"items":[` + trigger + `],"next_cursor":""}`})
	n := 0
	for got, err := range c.ListTriggers(t.Context(), &agents.ListTriggersParams{Owner: "org_1", Limit: 50, Cursor: "ignored"}) {
		if err != nil {
			t.Fatal(err)
		}
		if got.Metadata.Name != "weekly-digest" {
			t.Errorf("listed %+v", got)
		}
		n++
	}
	if n != 1 {
		t.Errorf("listed %d triggers", n)
	}
	if q := (*calls)[0].Query; q != "limit=50&owner=org_1" {
		t.Errorf("sent the query %q", q)
	}
	page, err := c.ListTriggersPage(t.Context(), &agents.ListTriggersParams{Cursor: "c2"})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("one page: %+v, %v", page, err)
	}
	if q := (*calls)[1].Query; q != "cursor=c2" {
		t.Errorf("a page sent the query %q", q)
	}
	if _, err := c.ListTriggersPage(t.Context(), nil); err != nil {
		t.Errorf("nil params: %v", err)
	}
}

func TestApplyTriggerFillsTheEnvelopeAndSendsNoStatus(t *testing.T) {
	c, calls := fakeAPI(t, map[string]string{"PUT /v1/agents/triggers/weekly-digest": trigger})
	in := &agents.Trigger{
		Metadata: agents.TriggerMetadata{Name: "weekly-digest", Labels: map[string]string{"chat": "routine"}},
		Spec: agents.TriggerSpec{Agent: "latere-chat", Schedule: "0 9 * * 1", TimeZone: "Europe/Berlin",
			Session: agents.TriggerSession{Message: "Write the digest."}},
		Status: agents.TriggerStatus{ID: "trg_old"},
	}
	got, err := c.ApplyTrigger(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status.ID != "trg_1" {
		t.Errorf("answered %+v", got.Status)
	}
	body := (*calls)[0].Body
	if body["apiVersion"] != agents.TriggerAPIVersion || body["kind"] != agents.TriggerKind {
		t.Errorf("sent the envelope %v %v", body["apiVersion"], body["kind"])
	}
	if _, has := body["status"]; has {
		t.Errorf("sent a status: %v", body["status"])
	}
	spec, _ := body["spec"].(map[string]any)
	if spec["agent"] != "latere-chat" || spec["timeZone"] != "Europe/Berlin" {
		t.Errorf("sent the spec %v", spec)
	}
	for _, unset := range []string{"on", "at", "skipIfActive", "maxActive", "maxAge", "suspend"} {
		if _, has := spec[unset]; has {
			t.Errorf("sent the unset %s: %v", unset, spec[unset])
		}
	}
	session, _ := spec["session"].(map[string]any)
	for _, unset := range []string{"target", "attended", "notify"} {
		if _, has := session[unset]; has {
			t.Errorf("sent the unset session.%s: %v", unset, session[unset])
		}
	}
	if in.APIVersion != "" || in.Status.ID != "trg_old" {
		t.Errorf("changed the caller's trigger: %+v", in)
	}
	for _, bad := range []*agents.Trigger{nil, {}} {
		if _, err := c.ApplyTrigger(t.Context(), bad); err == nil {
			t.Errorf("applied %+v without a name", bad)
		}
	}
}

func TestDeleteTrigger(t *testing.T) {
	c, calls := fakeAPI(t, map[string]string{"DELETE /v1/agents/triggers/trg_1": ""})
	if err := c.DeleteTrigger(t.Context(), "trg_1"); err != nil {
		t.Fatal(err)
	}
	if (*calls)[0].Method != http.MethodDelete {
		t.Errorf("sent %+v", (*calls)[0])
	}
	if err := c.DeleteTrigger(t.Context(), ""); err == nil {
		t.Error("an empty ref was sent")
	}
}

func TestFireTriggerSendsTheEventAndAKey(t *testing.T) {
	c, calls := fakeAPI(t, map[string]string{"POST /v1/agents/triggers/trg_1/fire": firing})
	got, err := c.FireTrigger(t.Context(), "trg_1", &agents.FireTriggerParams{
		Event: &agents.TriggerEvent{ID: "e1", Product: "github", Verb: "release.published", Resource: "example/widgets@v1.4.0",
			Time: time.Date(2026, 9, 22, 12, 40, 0, 0, time.UTC), Payload: json.RawMessage(`{"tag":"v1.4.0"}`)},
		IdempotencyKey: "fire-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Outcome != agents.FiringStarted || got.Origin != agents.OriginEvent || got.Event.Resource != "example/widgets@v1.4.0" || got.ReceivedAt.IsZero() {
		t.Errorf("answered %+v", got)
	}
	sent := (*calls)[0]
	if sent.Key != "fire-1" || sent.Body["verb"] != "release.published" || sent.Body["payload"].(map[string]any)["tag"] != "v1.4.0" {
		t.Errorf("sent %+v", sent)
	}
	if _, has := sent.Body["actor"]; has {
		t.Error("sent an empty actor")
	}
}

func TestFireTriggerWithoutAnEventSendsNoBody(t *testing.T) {
	c, calls := fakeAPI(t, map[string]string{"POST /v1/agents/triggers/trg_1/fire": strings.Replace(firing, `"origin":"event"`, `"origin":"manual"`, 1)})
	got, err := c.FireTrigger(t.Context(), "trg_1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Origin != agents.OriginManual {
		t.Errorf("answered %+v", got)
	}
	if sent := (*calls)[0]; sent.Body != nil || sent.Key == "" {
		t.Errorf("a manual firing sent %+v; want no body and a generated key", sent)
	}
	if _, err := c.FireTrigger(t.Context(), "", nil); err == nil {
		t.Error("an empty ref was sent")
	}
}

// A firing whose session failed to start is answered 503 with the firing
// itself; the call returns both, so the caller reads why.
func TestFireTriggerReturnsTheFailedFiringWithItsError(t *testing.T) {
	failed := strings.Replace(strings.Replace(firing, `"outcome":"started"`, `"outcome":"failed","reason":"The agent's model is not available."`, 1), `"session_id":"ses_9",`, ``, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, failed)
	}))
	t.Cleanup(srv.Close)
	cfg, err := latere.NewConfig(latere.WithBaseURL(srv.URL), latere.WithTokenSource(latere.StaticToken("key_test")), latere.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	got, err := agents.New(cfg).FireTrigger(t.Context(), "trg_1", nil)
	var apiErr *latere.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("err = %v, want a 503", err)
	}
	if got == nil || got.Outcome != agents.FiringFailed || got.Reason == "" || got.TriggerID == "" {
		t.Errorf("firing = %+v, want the failed firing with its reason", got)
	}
}

// A 503 that is not a firing, from a proxy for instance, returns no firing.
func TestFireTriggerReturnsNoFiringForAnotherRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, "upstream down")
	}))
	t.Cleanup(srv.Close)
	cfg, err := latere.NewConfig(latere.WithBaseURL(srv.URL), latere.WithTokenSource(latere.StaticToken("key_test")), latere.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := agents.New(cfg).FireTrigger(t.Context(), "trg_1", nil); got != nil || err == nil {
		t.Errorf("answered %+v, %v; want no firing and the error", got, err)
	}
}

func TestListFirings(t *testing.T) {
	c, calls := fakeAPI(t, map[string]string{"GET /v1/agents/triggers/trg_1/firings": `{"items":[` + firing + `],"next_cursor":""}`})
	n := 0
	for got, err := range c.ListFirings(t.Context(), "trg_1", &agents.ListFiringsParams{Limit: 10}) {
		if err != nil {
			t.Fatal(err)
		}
		if got.ID != "frg_1" {
			t.Errorf("listed %+v", got)
		}
		n++
	}
	if n != 1 || (*calls)[0].Query != "limit=10" {
		t.Errorf("listed %d firings with the query %q", n, (*calls)[0].Query)
	}
	if _, err := c.ListFiringsPage(t.Context(), "trg_1", &agents.ListFiringsParams{Cursor: "c2"}); err != nil {
		t.Fatal(err)
	}
	if q := (*calls)[1].Query; q != "cursor=c2" {
		t.Errorf("a page sent the query %q", q)
	}
	if _, err := c.ListFiringsPage(t.Context(), "", nil); err == nil {
		t.Error("an empty ref was sent")
	}
	for _, err := range c.ListFirings(t.Context(), "", nil) {
		if err == nil {
			t.Error("an empty ref listed firings")
		}
	}
}

func TestTriggerCallsReturnTheAPIsRefusal(t *testing.T) {
	c, _ := fakeAPI(t, map[string]string{})
	if _, err := c.ListTriggersPage(t.Context(), nil); !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("list: %v", err)
	}
	if _, err := c.ApplyTrigger(t.Context(), &agents.Trigger{Metadata: agents.TriggerMetadata{Name: "x"}}); !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("apply: %v", err)
	}
	if _, err := c.FireTrigger(t.Context(), "x", nil); !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("fire: %v", err)
	}
	if _, err := c.ListFiringsPage(t.Context(), "x", nil); !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("firings: %v", err)
	}
}

// TestATriggerThatFiresOnceContinuesItsTarget: a trigger that fires once
// sends its time, the session it continues, whether its sessions wait for
// their owner and how its turns notify, and reads back the call that made
// it.
func TestATriggerThatFiresOnceContinuesItsTarget(t *testing.T) {
	answer := `{"apiVersion":"topos.latere.ai/v1","kind":"Trigger","metadata":{"name":"wake-01m34g2jf01b91b8399cd1g6yp","displayName":"Whether the refund arrived"},
"spec":{"agent":"agent_1","at":"2026-10-24T07:00:00Z","session":{"message":"Look at the inbox.","policy":"continue","target":"ses_1","attended":true,"notify":"judged"}},
"status":{"id":"trg_2","createdAt":"2026-10-23T09:00:00Z","origin":{"kind":"wake","sessionId":"ses_1","toolUseId":"toolu_7"}}}`
	c, calls := fakeAPI(t, map[string]string{"PUT /v1/agents/triggers/wake-01m34g2jf01b91b8399cd1g6yp": answer})
	at := time.Date(2026, 10, 24, 9, 0, 0, 0, time.FixedZone("CEST", 2*60*60))
	got, err := c.ApplyTrigger(t.Context(), &agents.Trigger{
		Metadata: agents.TriggerMetadata{Name: "wake-01m34g2jf01b91b8399cd1g6yp", DisplayName: "Whether the refund arrived"},
		Spec: agents.TriggerSpec{Agent: "latere-chat", At: at, Session: agents.TriggerSession{
			Message: "Look at the inbox.", Policy: agents.SessionPolicyContinue, Target: "ses_1", Attended: true, Notify: agents.NotifyJudged,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := (*calls)[0].Body["spec"].(map[string]any)
	session, _ := spec["session"].(map[string]any)
	if spec["at"] != "2026-10-24T09:00:00+02:00" || session["target"] != "ses_1" || session["attended"] != true || session["notify"] != "judged" {
		t.Errorf("sent the spec %v", spec)
	}
	if !got.Spec.At.Equal(at) || got.Spec.Session.Target != "ses_1" || !got.Spec.Session.Attended || got.Spec.Session.Notify != agents.NotifyJudged {
		t.Errorf("read the spec back as %+v", got.Spec)
	}
	want := &agents.TriggerOrigin{Kind: agents.TriggerFromWake, SessionID: "ses_1", ToolUseID: "toolu_7"}
	if o := got.Status.Origin; o == nil || *o != *want {
		t.Errorf("read the origin as %+v, want %+v", o, want)
	}
}

// TestTheHoldIsReadAndSet: the hold is read, set and released at its own
// route, answers with when it was put on, and passes the API's refusal
// through.
func TestTheHoldIsReadAndSet(t *testing.T) {
	c, calls := fakeAPI(t, map[string]string{
		"GET /v1/agents/triggers/hold": `{"held":true,"since":"2026-10-23T08:00:00Z","by":"alice"}`,
		"PUT /v1/agents/triggers/hold": `{"held":false}`,
	})
	h, err := c.GetTriggerHold(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !h.Held || h.Since == nil || !h.Since.Equal(time.Date(2026, 10, 23, 8, 0, 0, 0, time.UTC)) || !strings.Contains(string(h.RawJSON()), `"by"`) {
		t.Errorf("read the hold as %+v, %s", h, h.RawJSON())
	}
	h, err = c.SetTriggerHold(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	if h.Held || h.Since != nil {
		t.Errorf("released the hold as %+v", h)
	}
	if put := (*calls)[1]; put.Method != http.MethodPut || put.Path != "/v1/agents/triggers/hold" || len(put.Body) != 1 || put.Body["held"] != false {
		t.Errorf("sent %+v", put)
	}
	if _, err := c.SetTriggerHold(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if (*calls)[2].Body["held"] != true {
		t.Errorf("put the hold on with %v", (*calls)[2].Body)
	}

	var bad agents.TriggerHold
	if err := json.Unmarshal([]byte(`{"held":"yes"}`), &bad); err == nil {
		t.Error("a hold whose held is no boolean decoded")
	}

	refused, _ := fakeAPI(t, nil)
	if _, err := refused.GetTriggerHold(t.Context()); !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("a refusal of the read: %v", err)
	}
	if _, err := refused.SetTriggerHold(t.Context(), true); !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("a refusal of the set: %v", err)
	}
}

// TestHoldNamesNoTrigger: the trigger calls refuse the name "hold" before
// any request, since its path is the hold's and a manifest applied there
// would set it.
func TestHoldNamesNoTrigger(t *testing.T) {
	c, calls := fakeAPI(t, map[string]string{"PUT /v1/agents/triggers/hold": `{"held":false}`, "GET /v1/agents/triggers/hold": `{"held":true}`, "DELETE /v1/agents/triggers/hold": ""})
	if _, err := c.ApplyTrigger(t.Context(), &agents.Trigger{Metadata: agents.TriggerMetadata{Name: "hold"}}); err == nil {
		t.Error("applied a trigger named hold")
	}
	if _, err := c.GetTrigger(t.Context(), "hold"); err == nil {
		t.Error("read the hold as a trigger")
	}
	if err := c.DeleteTrigger(t.Context(), "hold"); err == nil {
		t.Error("deleted the hold as a trigger")
	}
	if len(*calls) != 0 {
		t.Errorf("sent %+v", *calls)
	}
}
