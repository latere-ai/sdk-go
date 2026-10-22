// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package apps

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"latere.ai/x/sdk-go/latere"
)

// MaxVisitDays is the most days GetVisits lists, and how long the API
// keeps an app's counts.
const MaxVisitDays = 400

// Visits are how often an app's released site was opened: its page loads
// and the people who loaded a page each UTC day, and their totals. A visit
// is a load of a page of the released site in a browser tab; a preview, a
// frame, a capture, a crawler and a request for a file that is no page are
// not counted. A visitor is counted once a day, so a total of visitors
// over several days is the sum of each day's. No cookie is set and no
// address is kept with the counts.
type Visits struct {
	// ID is the app's ID.
	ID string `json:"id"`
	// Slug is the app's slug.
	Slug string `json:"slug"`
	// Today is the day the counts end, as YYYY-MM-DD in TimeZone.
	Today string `json:"today"`
	// TimeZone is the zone the days are counted in, "UTC".
	TimeZone string `json:"time_zone"`
	// CountedSince is the first day the app's visits were counted, as
	// YYYY-MM-DD: the later of the day the app was made and the day the
	// platform began counting. A day before it shows no visit because none
	// was counted.
	CountedSince string `json:"counted_since"`
	// Totals are the counts of today, the last 7 days and the last 30
	// days, today included.
	Totals VisitTotals `json:"totals"`
	// Days are the counts of each day, oldest first, from the later of the
	// range's first day and the day the app was created, to today, with
	// zeros for a day with nothing counted.
	Days []VisitDay `json:"days"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the visits, with every
// field; nil for Visits the program built itself.
func (v *Visits) RawJSON() json.RawMessage { return v.raw }

// UnmarshalJSON decodes the visits and keeps the JSON for RawJSON.
func (v *Visits) UnmarshalJSON(b []byte) error {
	type plain Visits
	if err := json.Unmarshal(b, (*plain)(v)); err != nil {
		return err
	}
	v.raw = keep(b)
	return nil
}

// VisitTotals are an app's counts over three ranges that end today.
type VisitTotals struct {
	// Today is today's counts.
	Today VisitCounts `json:"today"`
	// Last7Days are the counts of the last 7 days, today included.
	Last7Days VisitCounts `json:"last_7_days"`
	// Last30Days are the counts of the last 30 days, today included.
	Last30Days VisitCounts `json:"last_30_days"`
}

// VisitCounts are the page loads and the visitors of a range of days.
type VisitCounts struct {
	// Visits is how many pages were loaded.
	Visits int `json:"visits"`
	// Visitors is the sum of each day's visitors.
	Visitors int `json:"visitors"`
}

// VisitDay is one day's counts.
type VisitDay struct {
	// Day is the day, as YYYY-MM-DD in the Visits' TimeZone.
	Day string `json:"day"`
	// Visits is how many pages were loaded that day.
	Visits int `json:"visits"`
	// Visitors is how many people loaded a page that day.
	Visitors int `json:"visitors"`
	// Final is false for a day whose counts can still grow: today, and
	// yesterday for 15 minutes after midnight UTC.
	Final bool `json:"final"`
}

// GetVisits returns how often the released site of the app slug names was
// opened over the last days days, today included: 30 when days is zero,
// at most MaxVisitDays. slug may also be the app's ID. The owner of a
// personal app, every member of an organization that owns one, and an
// agent's session acting for its person may read them; an app the caller
// may not read returns an error matching latere.ErrNotFound.
func (c *Client) GetVisits(ctx context.Context, slug string, days int) (*Visits, error) {
	if slug == "" {
		return nil, errNoSlug
	}
	if days < 0 || days > MaxVisitDays {
		return nil, errors.New("apps: GetVisits: days is from 1 to MaxVisitDays, or zero for 30")
	}
	q := url.Values{}
	if days > 0 {
		q.Set("days", strconv.Itoa(days))
	}
	var out Visits
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodGet, Path: appPath(slug, "visits"), Query: q}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
