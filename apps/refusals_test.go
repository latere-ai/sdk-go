// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package apps_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"latere.ai/x/sdk-go/apps"
	"latere.ai/x/sdk-go/latere"
)

// Each new read and write returns the API's refusal as it came, and a
// malformed answer as an error, never a half-read model.
func TestRefusalsAndMalformedAnswers(t *testing.T) {
	notFound := answer(http.StatusNotFound, `{"error":{"code":"app_not_found","message":"No app.","details":{"slug":"gone"}}}`)
	malformed := answer(http.StatusOK, `{"id":`)
	c, _ := newFake(t, 0, map[string]http.HandlerFunc{
		"GET /v1/apps/apps/gone/sessions":           notFound,
		"GET /v1/apps/apps/gone/visits":             notFound,
		"GET /v1/apps/apps/gone/listing":            notFound,
		"PUT /v1/apps/apps/private/listing":         answer(http.StatusConflict, `{"error":{"code":"listing_needs_public","message":"Only invited people open it.","details":{}}}`),
		"GET /v1/apps/apps/bad/sessions":            malformed,
		"GET /v1/apps/apps/bad/visits":              malformed,
		"GET /v1/apps/apps/bad/listing":             malformed,
		"PUT /v1/apps/apps/bad/listing":             malformed,
		"GET /v1/apps/deploys/bad":                  malformed,
		"GET /v1/apps/deploys/bad/app":              malformed,
		"POST /v1/apps/apps/bad/frame":              malformed,
		"DELETE /v1/apps/apps/gone/listing":         notFound,
		"DELETE /v1/apps/apps/gate/sessions":        answer(http.StatusForbidden, `{"error":{"code":"forbidden","message":"No.","details":{"reason":"session_read_only"}}}`),
		"GET /v1/apps/apps/bad/deploys/d/thumbnail": answer(http.StatusTooManyRequests, `{"error":{"code":"rate_limited","message":"Slow down.","details":{"limit":120}}}`),
	})
	ctx := t.Context()
	card := &apps.ListingParams{Category: apps.CategoryTools, Summary: "A tool."}

	for name, err := range map[string]error{
		"ListAppSessionsPage": func() error { _, err := c.ListAppSessionsPage(ctx, "gone", nil); return err }(),
		"GetAppVisits":        func() error { _, err := c.GetAppVisits(ctx, "gone", 0); return err }(),
		"GetListing":          func() error { _, err := c.GetListing(ctx, "gone"); return err }(),
		"WithdrawListing":     c.WithdrawListing(ctx, "gone"),
	} {
		var apiErr *latere.Error
		if !errors.Is(err, latere.ErrNotFound) || !errors.As(err, &apiErr) || apiErr.Code != "app_not_found" {
			t.Errorf("%s: %v, want the API's app_not_found", name, err)
		}
	}
	_, err := c.RequestListing(ctx, "private", card)
	var apiErr *latere.Error
	if !errors.Is(err, latere.ErrConflict) || !errors.As(err, &apiErr) || apiErr.Code != "listing_needs_public" {
		t.Errorf("RequestListing of a private app: %v", err)
	}
	if err := c.EndGateSessions(ctx); !errors.Is(err, latere.ErrForbidden) {
		t.Errorf("EndGateSessions with an agent's token: %v", err)
	}
	if _, err := c.ReadThumbnail(ctx, "bad", "d", 0); !errors.Is(err, latere.ErrRateLimited) || errors.Is(err, apps.ErrNoThumbnail) {
		t.Errorf("ReadThumbnail past the limit: %v", err)
	}

	for name, err := range map[string]error{
		"ListAppSessionsPage": func() error { _, err := c.ListAppSessionsPage(ctx, "bad", nil); return err }(),
		"GetAppVisits":        func() error { _, err := c.GetAppVisits(ctx, "bad", 0); return err }(),
		"GetListing":          func() error { _, err := c.GetListing(ctx, "bad"); return err }(),
		"RequestListing":      func() error { _, err := c.RequestListing(ctx, "bad", card); return err }(),
		"GetDeployByID":       func() error { _, err := c.GetDeployByID(ctx, "bad"); return err }(),
		"GetDeployApp":        func() error { _, err := c.GetDeployApp(ctx, "bad"); return err }(),
		"CreateFramePass":     func() error { _, err := c.CreateFramePass(ctx, "bad", "https://bad.latere.site/"); return err }(),
	} {
		if err == nil || errors.Is(err, latere.ErrNotFound) {
			t.Errorf("%s of a malformed answer: %v, want a decoding error", name, err)
		}
	}
	if _, err := c.GetListing(ctx, ""); err == nil {
		t.Error("GetListing sent an empty slug")
	}
	if _, err := c.GetAppVisits(ctx, "", 0); err == nil {
		t.Error("GetAppVisits sent an empty slug")
	}
	if _, err := c.RequestListing(ctx, "", card); err == nil {
		t.Error("RequestListing sent an empty slug")
	}
}

// The error a sentinel matches says what the API said, and a failure that
// never reached the API matches no sentinel.
func TestSentinelErrors(t *testing.T) {
	c, _ := newFake(t, 0, map[string]http.HandlerFunc{
		"GET /v1/apps/apps/a/deploys/d/thumbnail": answer(http.StatusNotFound, `{"error":{"code":"thumbnail_not_found","message":"The deploy has no picture yet.","details":{}}}`),
	})
	_, err := c.ReadThumbnail(t.Context(), "a", "d", 0)
	if err == nil || !strings.Contains(err.Error(), "The deploy has no picture yet.") {
		t.Errorf("the error reads %v, want the API's message", err)
	}

	cfg, err := latere.NewConfig(latere.WithBaseURL("http://127.0.0.1:1"), latere.WithTokenSource(latere.StaticToken("tok")),
		latere.WithMaxRetries(0), latere.WithTimeout(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	_, err = apps.New(cfg).ReadThumbnail(t.Context(), "a", "d", 0)
	if err == nil || errors.Is(err, apps.ErrNoThumbnail) {
		t.Errorf("an API that cannot be reached: %v", err)
	}
}

// A row that is not a JSON object is no row.
func TestModelsRefuseMalformedJSON(t *testing.T) {
	for name, v := range map[string]any{
		"AppSession": &apps.AppSession{}, "Visits": &apps.Visits{}, "OpenApp": &apps.OpenApp{},
		"FramePass": &apps.FramePass{}, "Listing": &apps.Listing{},
	} {
		if err := json.Unmarshal([]byte(`[]`), v); err == nil {
			t.Errorf("%s decoded an array", name)
		}
	}
	if _, _, ok := (apps.Thumbnail{URL: "/v1/apps/apps/%zz/deploys/d/thumbnail"}).Deploy(); ok {
		t.Error("a slug that is not escaped right was read")
	}
	if _, _, ok := (apps.Thumbnail{URL: "/v1/apps/apps/a/deploys/%zz/thumbnail"}).Deploy(); ok {
		t.Error("a deploy ID that is not escaped right was read")
	}
}
