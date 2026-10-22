// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package apps_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"latere.ai/x/sdk-go/apps"
)

const listingJSON = `{"actions":["ask_again","withdraw"],"app":"kornhaus","author":null,"blocked":null,"category":"business",
	"decision":{"after_report":false,"at":"2026-10-07T15:02:11Z","automated":false,"clause":"2",
	"explanation":"The line names the street but not what the site is for.","ground":"gallery_rules","outcome":"declined","reason":"summary_unclear"},
	"gallery_url":null,"listed_at":null,"proposed":null,"requested_at":"2026-10-07T09:12:40Z","state":"declined",
	"summary":"Bread and opening hours for a bakery in Ottensen."}`

func TestListing(t *testing.T) {
	c, f := newFake(t, 0, map[string]http.HandlerFunc{
		"GET /v1/apps/apps/kornhaus/listing": answer(http.StatusOK, listingJSON),
		"PUT /v1/apps/apps/kornhaus/listing": answer(http.StatusAccepted, `{"actions":["change","withdraw"],"app":"kornhaus","author":"Anna","blocked":null,"category":"business",
				"decision":null,"gallery_url":"https://gallery.latere.site/kornhaus","listed_at":"2026-10-08T09:00:00Z",
				"proposed":{"category":"events","summary":"Market days.","author":null},"requested_at":"2026-10-09T09:00:00Z","state":"listed","summary":"Bread."}`),
		"DELETE /v1/apps/apps/kornhaus/listing": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) },
	})
	ctx := t.Context()

	l, err := c.GetListing(ctx, "kornhaus")
	if err != nil {
		t.Fatal(err)
	}
	if l.App != "kornhaus" || l.State != apps.ListingDeclined || l.Category != apps.CategoryBusiness || l.Author != "" ||
		l.Blocked != "" || l.Proposed != nil || !l.ListedAt.IsZero() ||
		!l.RequestedAt.Equal(time.Date(2026, 10, 7, 9, 12, 40, 0, time.UTC)) ||
		l.Decision == nil || l.Decision.Outcome != apps.DecisionDeclined || l.Decision.Reason != "summary_unclear" ||
		l.Decision.Ground != "gallery_rules" || l.Decision.At.IsZero() ||
		len(l.Actions) != 2 || l.Actions[0] != apps.ListingAskAgain || len(l.RawJSON()) == 0 {
		t.Errorf("GetListing = %+v", l)
	}

	l, err = c.RequestListing(ctx, "kornhaus", &apps.ListingParams{Category: apps.CategoryEvents, Summary: "Market days."})
	if err != nil {
		t.Fatal(err)
	}
	if l.State != apps.ListingListed || l.Author != "Anna" || l.Proposed == nil || l.Proposed.Category != apps.CategoryEvents ||
		l.GalleryURL == "" || l.ListedAt.IsZero() || l.Decision != nil {
		t.Errorf("RequestListing = %+v", l)
	}
	// A card without an author sends none, which the gallery reads as no
	// name on the card.
	if put := f.record()[1].Body; strings.TrimSpace(put) != `{"category":"events","summary":"Market days."}` {
		t.Errorf("the card sent = %s", put)
	}
	if _, err := c.RequestListing(ctx, "kornhaus", &apps.ListingParams{Summary: "No shelf."}); err == nil {
		t.Error("a card without a category was sent")
	}
	if _, err := c.RequestListing(ctx, "kornhaus", nil); err == nil {
		t.Error("no card was sent")
	}

	if err := c.WithdrawListing(ctx, "kornhaus"); err != nil {
		t.Fatal(err)
	}
	if err := c.WithdrawListing(ctx, ""); err == nil {
		t.Error("an empty slug was sent")
	}
	calls := f.record()
	if len(calls) != 3 || calls[0].Method != "GET" || calls[1].Method != "PUT" || calls[2].Method != "DELETE" {
		t.Errorf("requests = %+v", calls)
	}
}
