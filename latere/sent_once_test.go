// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package latere_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"latere.ai/x/sdk-go/agents"
	"latere.ai/x/sdk-go/apps"
	"latere.ai/x/sdk-go/connections"
	"latere.ai/x/sdk-go/latere"
	"latere.ai/x/sdk-go/llm"
)

// Every call that is sent once stays sent once after a server error, and
// every PATCH that only sets values is sent again, whatever the package.
// The README and latere's package doc list the calls sent once.
func TestTheCallsSentOnceAndThePatchesRetried(t *testing.T) {
	var sends atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sends.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)
	cfg, err := latere.NewConfig(latere.WithBaseURL(srv.URL), latere.WithKey("pat_test"),
		latere.WithTokenSource(latere.StaticToken("tok")), latere.WithBackoff(time.Millisecond, time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	ag, ap, cs, ml := agents.New(cfg), apps.New(cfg), connections.New(cfg), llm.New(cfg)
	ctx := t.Context()
	title := "t"
	calls := map[string]struct {
		call  func(context.Context) error
		sends int32
	}{
		"FireTrigger": {func(ctx context.Context) error { _, err := ag.FireTrigger(ctx, "trg_1", nil); return err }, 1},
		"CreateApp":   {func(ctx context.Context) error { _, err := ap.CreateApp(ctx, nil); return err }, 1},
		"Invite": {func(ctx context.Context) error {
			_, err := ap.Invite(ctx, "app_1", &apps.InviteParams{Emails: []string{"a@example.com"}})
			return err
		}, 1},
		"ConfirmConnection": {func(ctx context.Context) error { _, err := cs.ConfirmConnection(ctx, "con_1", "code"); return err }, 1},
		"ReconnectConnection": {func(ctx context.Context) error {
			_, err := cs.ReconnectConnection(ctx, "con_1", &connections.ReconnectConnectionParams{ReturnTo: "https://platform.latere.ai/console/connections"})
			return err
		}, 1},
		"Generate": {func(ctx context.Context) error { _, err := ml.Generate(ctx, &llm.Request{Model: "m"}); return err }, 1},
		"Stream":   {func(ctx context.Context) error { _, err := ml.Stream(ctx, &llm.Request{Model: "m"}); return err }, 1},
		"UpdateSession": {func(ctx context.Context) error {
			_, err := ag.UpdateSession(ctx, "ses_1", &agents.UpdateSessionParams{Title: title})
			return err
		}, 1 + latere.DefaultMaxRetries},
		"UpdateApp": {func(ctx context.Context) error {
			_, err := ap.UpdateApp(ctx, "tides", &apps.UpdateAppParams{Name: title})
			return err
		}, 1 + latere.DefaultMaxRetries},
		"RenameConnection": {func(ctx context.Context) error { _, err := cs.RenameConnection(ctx, "con_1", title); return err }, 1 + latere.DefaultMaxRetries},
		"UpdateConnector": {func(ctx context.Context) error {
			_, err := cs.UpdateConnector(ctx, "notes", &connections.UpdateConnectorParams{Title: title})
			return err
		}, 1 + latere.DefaultMaxRetries},
		"CountTokens": {func(ctx context.Context) error { _, err := ml.CountTokens(ctx, &llm.Request{Model: "m"}); return err }, 1 + latere.DefaultMaxRetries},
	}
	for name, tc := range calls {
		sends.Store(0)
		if err := tc.call(ctx); err == nil {
			t.Errorf("%s: a 502 succeeded", name)
		}
		if got := sends.Load(); got != tc.sends {
			t.Errorf("%s: sent %d times, want %d", name, got, tc.sends)
		}
	}
}
