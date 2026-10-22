// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package apps

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"latere.ai/x/sdk-go/latere"
)

// ListingState is where an app is in the gallery.
type ListingState string

// Listing states.
const (
	// ListingNone is an app that never asked to be shown, or withdrew.
	ListingNone ListingState = "none"
	// ListingPending waits for a review.
	ListingPending ListingState = "pending"
	// ListingListed is shown in the gallery.
	ListingListed ListingState = "listed"
	// ListingDeclined was declined; the decision says why.
	ListingDeclined ListingState = "declined"
	// ListingRemoved was taken out of the gallery after it was shown.
	ListingRemoved ListingState = "removed"
)

// ListingAction is something the caller may do with an app's listing now.
type ListingAction string

// Listing actions.
const (
	// ListingAsk asks to show the app, with RequestListing.
	ListingAsk ListingAction = "ask"
	// ListingChange changes what the card says, with RequestListing.
	ListingChange ListingAction = "change"
	// ListingAskAgain asks again after a decline or a removal, with
	// RequestListing.
	ListingAskAgain ListingAction = "ask_again"
	// ListingWithdraw takes the app out of the gallery, with
	// WithdrawListing.
	ListingWithdraw ListingAction = "withdraw"
)

// Category is the shelf of the gallery an app's card is on.
type Category string

// Categories.
const (
	CategoryBusiness    Category = "business"
	CategoryEvents      Category = "events"
	CategoryExplainers  Category = "explainers"
	CategoryTools       Category = "tools"
	CategoryGames       Category = "games"
	CategoryMapsAndData Category = "maps-and-data"
	CategoryLearning    Category = "learning"
	CategoryPortfolios  Category = "portfolios"
)

// DecisionOutcome is what a review of a listing decided.
type DecisionOutcome string

// Decision outcomes.
const (
	// DecisionApproved showed the app in the gallery.
	DecisionApproved DecisionOutcome = "approved"
	// DecisionDeclined did not.
	DecisionDeclined DecisionOutcome = "declined"
	// DecisionRemoved took a listed app out of the gallery.
	DecisionRemoved DecisionOutcome = "removed"
	// DecisionChangeDeclined declined a change of a listed card, which
	// keeps showing the card it had.
	DecisionChangeDeclined DecisionOutcome = "change_declined"
)

// Listing is an app's place in the gallery as its owner reads it: the
// state of its listing, what its card says, a change waiting for review,
// the last decision, and what the caller may do now.
type Listing struct {
	// App is the app's slug.
	App string `json:"app"`
	// State is where the app is in the gallery; ListingNone for an app
	// with no listing.
	State ListingState `json:"state"`
	// Category is the card's shelf; empty with no listing.
	Category Category `json:"category"`
	// Summary is the card's one line; empty with no listing.
	Summary string `json:"summary"`
	// Author is the name the card shows; empty for none.
	Author string `json:"author"`
	// Proposed is a change of a listed card that waits for review; nil
	// when none does.
	Proposed *ListingCard `json:"proposed"`
	// RequestedAt is when the app last asked to be shown; zero when it
	// never did.
	RequestedAt time.Time `json:"requested_at,omitzero"`
	// ListedAt is when the app was shown; zero when it is not.
	ListedAt time.Time `json:"listed_at,omitzero"`
	// Decision is the last review's decision; nil before the first.
	Decision *ListingDecision `json:"decision"`
	// GalleryURL is the app's page in the gallery while it is listed.
	GalleryURL string `json:"gallery_url"`
	// Actions are what the caller may do now, each still decided when it
	// is taken.
	Actions []ListingAction `json:"actions"`
	// Blocked says why the caller cannot ask now, empty when they can:
	// "listing_needs_public" for an app only invited people open,
	// "listing_app_unavailable" for one with nothing published,
	// "gallery_off_for_organization", "role_insufficient", or another
	// reason of the authorizer.
	Blocked string `json:"blocked"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the listing, with every
// field; nil for a Listing the program built itself.
func (l *Listing) RawJSON() json.RawMessage { return l.raw }

// UnmarshalJSON decodes the listing and keeps the JSON for RawJSON.
func (l *Listing) UnmarshalJSON(b []byte) error {
	type plain Listing
	if err := json.Unmarshal(b, (*plain)(l)); err != nil {
		return err
	}
	l.raw = keep(b)
	return nil
}

// ListingCard is what a listing's card says beyond the app itself.
type ListingCard struct {
	// Category is the card's shelf.
	Category Category `json:"category"`
	// Summary is the card's one line.
	Summary string `json:"summary"`
	// Author is the name the card shows; empty for none.
	Author string `json:"author"`
}

// ListingDecision is a review's decision on a listing, in the reviewer's
// words, with the rule it cites.
type ListingDecision struct {
	// Outcome is what the review decided.
	Outcome DecisionOutcome `json:"outcome"`
	// At is when it decided.
	At time.Time `json:"at"`
	// Reason names why, such as "summary_unclear" or "not_working".
	Reason string `json:"reason"`
	// Clause is the clause of the rule the decision cites.
	Clause string `json:"clause"`
	// Explanation is the reviewer's own words.
	Explanation string `json:"explanation"`
	// Ground is what the rule belongs to: "gallery_rules", "terms" or
	// "law".
	Ground string `json:"ground"`
	// Automated reports that no person decided; always false.
	Automated bool `json:"automated"`
	// AfterReport reports that a report about the app led to the review.
	AfterReport bool `json:"after_report"`
}

// ListingParams are what an app's card says, for RequestListing.
type ListingParams struct {
	// Category is the card's shelf; required.
	Category Category `json:"category"`
	// Summary is the card's one line, of at most 90 characters and with
	// no web address; required.
	Summary string `json:"summary"`
	// Author is the name the card shows, one line of at most 40
	// characters; empty for none.
	Author string `json:"author,omitempty"`
	// Note is a note to the reviewer of at most 500 characters, for
	// another look after a removal.
	Note string `json:"note,omitempty"`
}

// GetListing returns the listing of the app slug names: its state, card,
// last decision and what the caller may do now, for any app the caller
// may read. slug may also be the app's ID. An app with no listing answers
// the state ListingNone. An app the caller may not read returns an error
// matching latere.ErrNotFound.
func (c *Client) GetListing(ctx context.Context, slug string) (*Listing, error) {
	if slug == "" {
		return nil, errNoSlug
	}
	var out Listing
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodGet, Path: appPath(slug, "listing")}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RequestListing asks to show the app slug names in the gallery, changes
// what its card says, or asks again after a decline or a removal, and
// returns the listing as it stands after the ask. A platform admin
// reviews every listing before it shows: a waiting listing takes the new
// card, and a listed one keeps showing its approved card and holds the
// change beside it until it is decided. The app keeps its visibility until
// an approval, which makes an app anyone with its address opens public.
// The decision, and a pause or a takedown, are sent to the email address
// of the token's person.
//
// It returns an error with the code "listing_invalid" and 400 for a card
// the gallery does not take, whose Details name the "field" and the
// "reason"; one matching latere.ErrForbidden when the caller may not
// change the app or the organization's gallery is off; and one matching
// latere.ErrConflict, with the code "listing_needs_public" for an app only
// invited people open, "listing_app_unavailable" for one unpublished,
// paused or never released, or "app_deleting". An app the caller may not
// read returns an error matching latere.ErrNotFound.
func (c *Client) RequestListing(ctx context.Context, slug string, params *ListingParams) (*Listing, error) {
	if slug == "" {
		return nil, errNoSlug
	}
	if params == nil || params.Category == "" || params.Summary == "" {
		return nil, errors.New("apps: RequestListing: a card needs a category and a summary")
	}
	var out Listing
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodPut, Path: appPath(slug, "listing"), Body: params}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// WithdrawListing takes the app slug names out of the gallery, or out of
// its review, at once, whatever its listing's state; its history stays. An
// app the gallery made public becomes unlisted: it keeps opening for
// anyone with its address and leaves search engines. It succeeds whether
// or not a listing existed. An app the caller may not change returns an
// error matching latere.ErrNotFound or latere.ErrForbidden.
func (c *Client) WithdrawListing(ctx context.Context, slug string) error {
	if slug == "" {
		return errNoSlug
	}
	return c.cfg.Do(ctx, &latere.Request{Method: http.MethodDelete, Path: appPath(slug, "listing")}, nil)
}
