// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package account_test

import (
	"errors"
	"io"
	"net/http"
	"testing"

	"latere.ai/x/sdk-go/account"
	"latere.ai/x/sdk-go/latere"
)

func TestListTriggerInbound(t *testing.T) {
	c, got := recordingAPI(t, 0, func(w http.ResponseWriter, r *http.Request, _ int) {
		_, _ = io.WriteString(w, `{"items":[{"issued_at":"2026-10-08T09:00:00Z","kind":"webhook","trigger_id":"trg_1"},
			{"address":"r-k3m9@in.latere.ai","issued_at":"2026-10-08T09:05:00Z","kind":"mail","trigger_id":"trg_2"}],
			"mail":{"domain":"in.latere.ai","enabled":true}}`)
	})
	in, err := c.ListTriggerInbound(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if s := (*got)[0]; s.method != http.MethodGet || s.path != "/v1/account/triggers/inbound" || len(s.query) != 0 {
		t.Errorf("sent %+v", s)
	}
	if !in.Mail.Enabled || in.Mail.Domain != "in.latere.ai" || len(in.Items) != 2 || in.Items[0].Kind != account.InletWebhook ||
		in.Items[0].Address != "" || in.Items[1].Kind != account.InletMail || in.Items[1].Address != "r-k3m9@in.latere.ai" ||
		in.Items[1].TriggerID != "trg_2" || in.Items[1].IssuedAt.IsZero() || len(in.RawJSON()) == 0 {
		t.Errorf("read %+v", in)
	}
}

func TestAnOrganizationHasNoInlets(t *testing.T) {
	c, _ := recordingAPI(t, 0, func(w http.ResponseWriter, r *http.Request, _ int) {
		writeError(w, http.StatusForbidden, "forbidden")
	})
	if _, err := c.ListTriggerInbound(t.Context()); !errors.Is(err, latere.ErrForbidden) {
		t.Errorf("an organization's context: %v", err)
	}
}

func TestIssueTriggerHook(t *testing.T) {
	c, got := recordingAPI(t, 2, func(w http.ResponseWriter, r *http.Request, n int) {
		switch {
		case r.URL.EscapedPath() == "/v1/account/triggers/trg_other/hook":
			writeError(w, http.StatusNotFound, "not_found")
		case n == 1:
			// A failure after which the link may have been issued: the
			// issue is sent again, and the second link is the one that
			// works.
			writeError(w, http.StatusServiceUnavailable, "triggers_inbound_unavailable")
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"issued_at":"2026-10-08T09:00:00Z","trigger_id":"trg_1","url":"https://api.latere.ai/v1/hooks/q2lw"}`)
		}
	})
	h, err := c.IssueTriggerHook(t.Context(), "trg_1")
	if err != nil || h.URL != "https://api.latere.ai/v1/hooks/q2lw" || h.TriggerID != "trg_1" || h.IssuedAt.IsZero() || len(h.RawJSON()) == 0 {
		t.Fatalf("issued %+v, %v", h, err)
	}
	if len(*got) != 2 {
		t.Fatalf("sent %d issues, want the one the API failed sent again", len(*got))
	}
	if s := (*got)[1]; s.method != http.MethodPost || s.path != "/v1/account/triggers/trg_1/hook" || s.body != nil {
		t.Errorf("sent %+v", s)
	}
	if _, err := c.IssueTriggerHook(t.Context(), "trg_other"); !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("a trigger the caller does not hold: %v", err)
	}
	if err := c.RemoveTriggerHook(t.Context(), "trg_1"); err != nil {
		t.Fatal(err)
	}
	if s := (*got)[3]; s.method != http.MethodDelete || s.path != "/v1/account/triggers/trg_1/hook" {
		t.Errorf("sent %+v", s)
	}
	if _, err := c.IssueTriggerHook(t.Context(), ""); err == nil {
		t.Error("an empty trigger id was issued a link")
	}
	if err := c.RemoveTriggerHook(t.Context(), ""); err == nil || len(*got) != 4 {
		t.Errorf("an empty trigger id's link was removed: %v", err)
	}
}

func TestIssueTriggerAddress(t *testing.T) {
	c, got := recordingAPI(t, 0, func(w http.ResponseWriter, r *http.Request, _ int) {
		switch {
		case r.URL.EscapedPath() == "/v1/account/triggers/trg_off/address":
			writeError(w, http.StatusNotFound, "mail_not_enabled")
		case r.URL.EscapedPath() == "/v1/account/triggers/trg_other/address":
			writeError(w, http.StatusNotFound, "not_found")
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"address":"r-k3m9@in.latere.ai","issued_at":"2026-10-08T09:05:00Z","trigger_id":"trg_2"}`)
		}
	})
	a, err := c.IssueTriggerAddress(t.Context(), "trg_2")
	if err != nil || a.Address != "r-k3m9@in.latere.ai" || a.TriggerID != "trg_2" || a.IssuedAt.IsZero() || len(a.RawJSON()) == 0 {
		t.Fatalf("issued %+v, %v", a, err)
	}
	if s := (*got)[0]; s.method != http.MethodPost || s.path != "/v1/account/triggers/trg_2/address" || s.body != nil {
		t.Errorf("sent %+v", s)
	}
	if _, err := c.IssueTriggerAddress(t.Context(), "trg_off"); !errors.Is(err, account.ErrMailNotEnabled) || !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("an installation without email: %v", err)
	}
	if _, err := c.IssueTriggerAddress(t.Context(), "trg_other"); !errors.Is(err, latere.ErrNotFound) || errors.Is(err, account.ErrMailNotEnabled) {
		t.Errorf("a trigger the caller does not hold: %v", err)
	}
	if err := c.RemoveTriggerAddress(t.Context(), "trg_2"); err != nil {
		t.Fatal(err)
	}
	if s := (*got)[3]; s.method != http.MethodDelete || s.path != "/v1/account/triggers/trg_2/address" {
		t.Errorf("sent %+v", s)
	}
	if err := c.RemoveTriggerAddress(t.Context(), "trg_off"); !errors.Is(err, account.ErrMailNotEnabled) {
		t.Errorf("turning off an address without email: %v", err)
	}
	if _, err := c.IssueTriggerAddress(t.Context(), ""); err == nil {
		t.Error("an empty trigger id was issued an address")
	}
	if err := c.RemoveTriggerAddress(t.Context(), ""); err == nil || len(*got) != 5 {
		t.Errorf("an empty trigger id's address was removed: %v", err)
	}
}
