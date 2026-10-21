// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"latere.ai/x/sdk-go/latere"
)

// The trigger models are held to the document's schemas as the session
// models are (spec_test.go): every field a model reads is in its schema,
// a date-time is read as a time.Time, and a model the API answers whole
// reads every field of its schema or says why not in leftOut.

func TestTriggersMatchTheSchemas(t *testing.T) {
	d, _ := loadDocument(t)
	s := d.Components.Schemas
	d.checkModel(t, "Trigger", reflect.TypeFor[Trigger](), s["Trigger"], true)
	d.checkModel(t, "TriggerMetadata", reflect.TypeFor[TriggerMetadata](), s["ObjectMeta"], true)
	d.checkModel(t, "TriggerSpec", reflect.TypeFor[TriggerSpec](), s["TriggerSpec"], true)
	d.checkModel(t, "TriggerSession", reflect.TypeFor[TriggerSession](), s["TriggerSession"], true)
	d.checkModel(t, "EventFilter", reflect.TypeFor[EventFilter](), s["TriggerOn"], true)
	d.checkModel(t, "PayloadMatch", reflect.TypeFor[PayloadMatch](), s["TriggerMatch"], true)
	d.checkModel(t, "TriggerStatus", reflect.TypeFor[TriggerStatus](), s["ManifestStatus"], true)
	d.checkModel(t, "FiringCounts", reflect.TypeFor[FiringCounts](), s["TriggerCounts"], true)
	d.checkModel(t, "Firing", reflect.TypeFor[Firing](), s["Firing"], true)
	d.checkModel(t, "FiringEvent", reflect.TypeFor[FiringEvent](), s["FiringEvent"], true)
	d.checkModel(t, "TriggerEvent", reflect.TypeFor[TriggerEvent](), s["Envelope"], true)
}

// TestTriggerRequestsFitTheirSchemas checks the bodies ApplyTrigger and
// FireTrigger send against the schemas of their routes. An applied
// manifest is held to its members alone: its route's schema is the
// Trigger the API answers, which requires the fields the API fills in by
// default, and the route's own example leaves them out.
func TestTriggerRequestsFitTheirSchemas(t *testing.T) {
	d, _ := loadDocument(t)
	s := d.Components.Schemas
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		body = nil
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("a body that is not JSON: %s", raw)
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/fire") {
			_, _ = io.WriteString(w, `{"id":"frg_1","trigger_id":"trg_1","origin":"fire","key":"k","received_at":"2026-10-20T12:00:00Z"}`)
			return
		}
		_, _ = io.WriteString(w, `{"apiVersion":"topos.latere.ai/v1","kind":"Trigger","metadata":{"name":"weekly"},"spec":{"agent":"a","session":{"message":"Go."}}}`)
	}))
	t.Cleanup(srv.Close)
	cfg, err := latere.NewConfig(latere.WithBaseURL(srv.URL), latere.WithTokenSource(latere.StaticToken("key_test")), latere.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	c := New(cfg)
	ctx := context.Background()
	skip, active, end := true, 2, true
	for _, r := range []struct {
		name    string
		send    func() error
		schema  *schema
		members bool
	}{
		{"ApplyTrigger, a schedule", func() error {
			_, err := c.ApplyTrigger(ctx, &Trigger{
				Metadata: TriggerMetadata{Name: "weekly", DisplayName: "Weekly", Labels: map[string]string{"team": "docs"}},
				Spec: TriggerSpec{
					Agent: "a", Schedule: "0 9 * * 1", TimeZone: "Europe/Berlin", SkipIfActive: &skip, MaxActive: &active, MaxAge: "1h",
					Session: TriggerSession{
						Message: "Go.", Title: "Digest", Policy: SessionPolicyNew, Key: "{{event.id}}", EndOnIdle: &end,
						Budget: &TriggerBudget{MaxCost: "2.50"}, Limits: &SessionLimits{TurnTimeout: "10m", MaxAge: "1d"},
					},
				},
			})
			return err
		}, s["Trigger"], true},
		{"ApplyTrigger, an event", func() error {
			_, err := c.ApplyTrigger(ctx, &Trigger{
				Metadata: TriggerMetadata{Name: "on-release"},
				Spec: TriggerSpec{
					Agent:   "a",
					On:      &EventFilter{Product: "github", Verbs: []string{"release.published"}, Match: []PayloadMatch{{Path: "tag", In: []string{"v1"}}}},
					Session: TriggerSession{Message: "Go."},
				},
			})
			return err
		}, s["Trigger"], true},
		{"FireTrigger", func() error {
			_, err := c.FireTrigger(ctx, "weekly", &FireTriggerParams{Event: &TriggerEvent{
				ID: "e1", Product: "github", Verb: "release.published", Resource: "r", Actor: "ann", Subject: "bob",
				Time: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC), Payload: json.RawMessage(`{"tag":"v1"}`),
			}})
			return err
		}, s["Envelope"], false},
	} {
		if err := r.send(); err != nil {
			t.Errorf("%s: %v", r.name, err)
			continue
		}
		if r.members {
			d.checkMembers(t, r.name, body, r.schema)
		} else {
			d.checkBody(t, r.name, body, r.schema)
		}
	}
}

func TestTriggerEndpointsAreInTheDocument(t *testing.T) {
	d, _ := loadDocument(t)
	for _, e := range []struct{ method, path string }{
		{"get", "/triggers"},
		{"put", "/triggers/{name}"},
		{"get", "/triggers/{ref}"},
		{"delete", "/triggers/{ref}"},
		{"post", "/triggers/{ref}/fire"},
		{"get", "/triggers/{ref}/firings"},
	} {
		if _, ok := d.Paths[e.path][e.method]; !ok {
			t.Errorf("%s %s is not in the document", strings.ToUpper(e.method), e.path)
		}
	}
}
