// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package apps_test

import (
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"latere.ai/x/sdk-go/apps"
	"latere.ai/x/sdk-go/latere"
)

func TestCreateFramePass(t *testing.T) {
	var n atomic.Int32
	c, f := newFake(t, 2, map[string]http.HandlerFunc{
		"POST /v1/apps/apps/tide-tables/frame": func(w http.ResponseWriter, r *http.Request) {
			// The first answer fails; a pass never used lapses, so the call
			// is sent again.
			if n.Add(1) == 1 {
				answer(http.StatusServiceUnavailable, `{"error":{"code":"unavailable","message":"Try again.","details":{}}}`)(w, r)
				return
			}
			answer(http.StatusOK, `{"url":"https://tide-tables.latere.site/.latere/frame?pass=p&rt=%2F","expires_at":"2026-10-11T10:11:00Z"}`)(w, r)
		},
		"POST /v1/apps/apps/elsewhere/frame": answer(http.StatusNotFound, `{"error":{"code":"frame_unavailable","message":"No frames here.","details":{}}}`),
	})
	pass, err := c.CreateFramePass(t.Context(), "tide-tables", "https://tide-tables.latere.site/")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(pass.URL, "https://tide-tables.latere.site/.latere/frame?pass=") ||
		!pass.ExpiresAt.Equal(time.Date(2026, 10, 11, 10, 11, 0, 0, time.UTC)) || len(pass.RawJSON()) == 0 {
		t.Errorf("CreateFramePass = %+v", pass)
	}
	calls := f.record()
	if len(calls) != 2 || strings.TrimSpace(calls[1].Body) != `{"address":"https://tide-tables.latere.site/"}` {
		t.Errorf("requests = %+v, want the pass asked twice with its address", calls)
	}

	_, err = c.CreateFramePass(t.Context(), "elsewhere", "https://elsewhere.latere.site/")
	if !errors.Is(err, apps.ErrFrameUnavailable) || !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("an installation without frames: %v", err)
	}
	if _, err := c.CreateFramePass(t.Context(), "tide-tables", ""); err == nil {
		t.Error("an empty address was sent")
	}
	if _, err := c.CreateFramePass(t.Context(), "", "https://x.latere.site/"); err == nil {
		t.Error("an empty slug was sent")
	}
	if n := len(f.record()); n != 3 {
		t.Errorf("made %d requests, want 3", n)
	}
}

func TestEndGateSessions(t *testing.T) {
	c, f := newFake(t, 0, map[string]http.HandlerFunc{
		"DELETE /v1/apps/apps/gate/sessions": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) },
	})
	if err := c.EndGateSessions(t.Context()); err != nil {
		t.Fatal(err)
	}
	if calls := f.record(); len(calls) != 1 || calls[0].Method != http.MethodDelete || calls[0].Header.Get("Authorization") != "Bearer tok" {
		t.Errorf("requests = %+v", calls)
	}
}
