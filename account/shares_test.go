// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package account_test

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"latere.ai/x/sdk-go/account"
	"latere.ai/x/sdk-go/latere"
)

const share = `{"id":"shr_01JA7Q1C2D3E4F5G6H7J8K9M0N","session_id":"ses_1","url":"https://chat.latere.ai/share/q2Lw","audience":"anyone",
"up_to_seq":212,"session_last_seq":240,"title":"Trip to Lisbon","state":"live","created_at":"2026-10-06T10:00:00Z","updated_at":"2026-10-06T11:00:00Z"}`

func TestListShares(t *testing.T) {
	c, calls := capturingAPI(t, 0, func(w http.ResponseWriter, r *http.Request, n int) {
		if r.URL.Query().Get("cursor") == "" {
			answerJSON(w, http.StatusOK, `{"items":[`+share+`],"next_cursor":"c2"}`)
			return
		}
		answerJSON(w, http.StatusOK, `{"items":[{"id":"shr_2","session_id":"ses_1","url":"u","audience":"anyone","up_to_seq":3,"title":"t",
			"state":"suspended","reason":"sharing_off","created_at":"2026-10-05T10:00:00Z","updated_at":"2026-10-05T10:00:00Z"}]}`)
	})
	var got []*account.Share
	for s, err := range c.ListShares(t.Context(), &account.ListSharesParams{SessionID: "ses_1", All: true, Limit: 1}) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, s)
	}
	if len(got) != 2 {
		t.Fatalf("listed %d shares, want 2", len(got))
	}
	first, second := got[0], got[1]
	if first.Audience != account.ShareAnyone || first.State != account.ShareLive || first.UpToSeq != 212 || first.SessionLastSeq != 240 ||
		!first.CreatedAt.Equal(time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)) || first.UpdatedAt.IsZero() || first.URL == "" || string(first.RawJSON()) == "" {
		t.Errorf("first share: %+v", first)
	}
	if second.State != account.ShareSuspended || second.Reason != account.ShareSharingOff || second.SessionLastSeq != 0 {
		t.Errorf("second share: %+v", second)
	}
	sent := calls()
	if sent[0].Method != http.MethodGet || sent[0].Path != "/v1/account/shares" || sent[0].Query != "all=true&limit=1&session_id=ses_1" ||
		sent[1].Query != "all=true&cursor=c2&limit=1&session_id=ses_1" {
		t.Errorf("requests: %+v", sent)
	}

	page, err := c.ListSharesPage(t.Context(), &account.ListSharesParams{Cursor: "c2"})
	if err != nil || len(page.Items) != 1 || page.NextCursor != "" {
		t.Errorf("ListSharesPage: %+v, %v", page, err)
	}
	if q := calls()[2].Query; q != "cursor=c2" {
		t.Errorf("ListSharesPage's query is %q, want the cursor alone", q)
	}
	if _, err := c.ListSharesPage(t.Context(), nil); err != nil || calls()[3].Query != "" {
		t.Errorf("ListSharesPage(nil): %v, query %q", err, calls()[3].Query)
	}
}

// A share is made once: the shares part of the API takes no key, so a
// repeat would make a second link.
func TestCreateShareIsSentOnce(t *testing.T) {
	c, calls := capturingAPI(t, 3, func(w http.ResponseWriter, r *http.Request, n int) {
		if n == 0 {
			answerUnavailable(w)
			return
		}
		answerJSON(w, http.StatusCreated, share)
	})
	var apiErr *latere.Error
	if _, err := c.CreateShare(t.Context(), "ses_1"); !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("a share the API refused with 503: %v, want that refusal", err)
	}
	if len(calls()) != 1 {
		t.Fatalf("sent %d times, want once", len(calls()))
	}
	s, err := c.CreateShare(t.Context(), "ses_1")
	if err != nil || s.ID != "shr_01JA7Q1C2D3E4F5G6H7J8K9M0N" {
		t.Fatalf("CreateShare: %+v, %v", s, err)
	}
	sent := calls()[1]
	if sent.Method != http.MethodPost || sent.Path != "/v1/account/shares" || len(sent.Body) != 1 || sent.Body["session_id"] != "ses_1" {
		t.Errorf("request: %+v", sent)
	}
}

func TestUpdateAndDeleteShare(t *testing.T) {
	c, calls := capturingAPI(t, 1, func(w http.ResponseWriter, r *http.Request, n int) {
		switch {
		case r.Method == http.MethodPatch && n == 0:
			answerUnavailable(w)
		case r.Method == http.MethodPatch:
			answerJSON(w, http.StatusOK, share)
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/account/shares/shr_gone":
			answerJSON(w, http.StatusNotFound, `{"error":{"code":"not_found","message":"No such link."}}`)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	s, err := c.UpdateShare(t.Context(), "shr_1")
	if err != nil || s.UpToSeq != 212 {
		t.Fatalf("UpdateShare: %+v, %v", s, err)
	}
	sent := calls()
	if len(sent) != 2 || sent[1].Method != http.MethodPatch || sent[1].Path != "/v1/account/shares/shr_1" || len(sent[1].Body) != 1 || sent[1].Body["up_to"] != "latest" {
		t.Errorf("UpdateShare sent %+v; want one retried move to the latest turn", sent)
	}
	if err := c.DeleteShare(t.Context(), "shr_1"); err != nil {
		t.Errorf("DeleteShare: %v", err)
	}
	if err := c.DeleteShare(t.Context(), "shr_gone"); !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("DeleteShare of a link the caller may not delete: %v, want ErrNotFound", err)
	}
	if got := calls()[2]; got.Method != http.MethodDelete || got.Path != "/v1/account/shares/shr_1" {
		t.Errorf("DeleteShare sent %+v", got)
	}
}

func TestShareCallsNeedAnID(t *testing.T) {
	c, calls := capturingAPI(t, 0, func(w http.ResponseWriter, r *http.Request, n int) { w.WriteHeader(http.StatusNoContent) })
	for name, err := range map[string]error{
		"CreateShare": second(c.CreateShare(t.Context(), "")),
		"UpdateShare": second(c.UpdateShare(t.Context(), "")),
		"DeleteShare": c.DeleteShare(t.Context(), ""),
	} {
		if err == nil {
			t.Errorf("%s with no id: no error", name)
		}
	}
	if n := len(calls()); n != 0 {
		t.Errorf("sent %d requests, want none", n)
	}
}

// Each call returns the API's refusal as it came.
func TestTheNewCallsReturnTheAPIsRefusal(t *testing.T) {
	c, _ := capturingAPI(t, 0, func(w http.ResponseWriter, r *http.Request, n int) {
		answerJSON(w, http.StatusForbidden, `{"error":{"code":"forbidden","message":"No."}}`)
	})
	mode := "open"
	for name, err := range map[string]error{
		"ListSharesPage":    second(c.ListSharesPage(t.Context(), nil)),
		"CreateShare":       second(c.CreateShare(t.Context(), "ses_1")),
		"UpdateShare":       second(c.UpdateShare(t.Context(), "shr_1")),
		"DeleteShare":       c.DeleteShare(t.Context(), "shr_1"),
		"StartCheckout":     second(c.StartCheckout(t.Context(), &account.StartCheckoutParams{AmountMicroUSD: 1, SuccessURL: "https://a/", CancelURL: "https://a/"})),
		"GetDataPlaces":     second(c.GetDataPlaces(t.Context())),
		"GetModelDataTerms": second(c.GetModelDataTerms(t.Context(), "vendor/a")),
		"UpdatePreferences": second(c.UpdatePreferences(t.Context(), &account.UpdatePreferencesParams{NetworkMode: &mode})),
		"AddSite":           second(c.AddSite(t.Context(), "example.com")),
		"RemoveSite":        c.RemoveSite(t.Context(), "example.com"),
		"BookmarkTurn":      second(c.BookmarkTurn(t.Context(), "ses_1", 1)),
		"UnbookmarkTurn":    c.UnbookmarkTurn(t.Context(), "ses_1", 1),
		"PinApp":            second(c.PinApp(t.Context(), "app_1")),
		"UnpinApp":          c.UnpinApp(t.Context(), "app_1"),
		"PinProject":        second(c.PinProject(t.Context(), "prj_1")),
		"UnpinProject":      c.UnpinProject(t.Context(), "prj_1"),
		"RecordArrival":     c.RecordArrival(t.Context(), &account.RecordArrivalParams{SourceHost: "example.com"}),
	} {
		if !errors.Is(err, latere.ErrForbidden) {
			t.Errorf("%s: %v, want the API's 403", name, err)
		}
	}
	for s, err := range c.ListShares(t.Context(), nil) {
		if s != nil || !errors.Is(err, latere.ErrForbidden) {
			t.Errorf("ListShares: %v, %v", s, err)
		}
	}
}
