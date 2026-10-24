// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package account

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"latere.ai/x/sdk-go/latere"
)

// GetForecastParams says which time zone GetForecast reads the month in.
type GetForecastParams struct {
	// TimeZone is the IANA time zone the month and its days are read in,
	// such as "Europe/Berlin": the zone the person's schedules run in.
	// The API reads UTC when it is empty.
	TimeZone string
}

// SpendHalf is which half of a person's spending a forecast item's runs
// are billed to.
type SpendHalf string

// The halves of a person's spending.
const (
	// HalfPersonLed is the person's own conversations: a follow-up in a
	// conversation the person started is billed as theirs, and their
	// average a day already holds its runs.
	HalfPersonLed SpendHalf = "person_led"
	// HalfProactive is the work that runs while the person is away.
	HalfProactive SpendHalf = "proactive"
)

// FiresOn is how a forecast item runs.
type FiresOn string

// The ways a forecast item runs.
const (
	// FiresOnSchedule runs at the times of its schedule.
	FiresOnSchedule FiresOn = "schedule"
	// FiresOnEvent runs each time something arrives.
	FiresOnEvent FiresOn = "event"
	// FiresOnAt runs once at a time, as a follow-up, a wake, does.
	FiresOnAt FiresOn = "at"
)

// SkipReason is why a forecast expects a run to be skipped.
type SkipReason string

// The reasons a run is skipped.
const (
	// SkipAllowance is the item's own weekly allowance, reached over the
	// 7 days before the run.
	SkipAllowance SkipReason = "allowance"
	// SkipShare is the part of the person's credit proactive work may use
	// in a week, used up then, or no credit left at all.
	SkipShare SkipReason = "share"
)

// Forecast is what a person will have spent by the end of the calendar
// month in a time zone, and whether their credit lasts that long. Their
// own conversations are expected to cost their average a day over the
// last 28 days, and each of their triggers is simulated run by run to the
// month's end against its weekly allowance and the part of their credit
// proactive work may use. The server computes it once, so every program
// that shows it shows the same month, and the items add up to the
// account: for each day ahead, ExpectedProactiveMicroUSD is what the items
// for which InProactiveHalf reports true are expected to cost, and the
// month's proactive spending is those items' months and
// UnlistedProactiveMicroUSD. Amounts are in millionths of a US dollar.
//
// A calendar date in it, a day of the month, is a time.Time at midnight
// UTC of that date, as SpendDay's: read its Year, Month and Day. It names
// the day in Zone.
type Forecast struct {
	// Zone is the IANA time zone the month is read in.
	Zone string
	// Today is today's date in Zone.
	Today time.Time
	// Days holds every day of the month in Zone, the 1st first.
	Days []ForecastDay
	// PersonLedPerDayMicroUSD is what the person's own conversations spent
	// a day on average over the last 28 days: what each day ahead is
	// expected to cost them.
	PersonLedPerDayMicroUSD int64
	// LastMonthMicroUSD is what last month spent in all, in Zone.
	LastMonthMicroUSD int64
	// UnlistedProactiveMicroUSD is what proactive work spent this month
	// that no item's runs hold: the person's own replies in a conversation
	// a trigger started, a trigger since deleted, a turn not counted yet.
	UnlistedProactiveMicroUSD int64
	// SpendableMicroUSD, ShareBasisPoints, PoolMicroUSD and
	// ProactiveWeekMicroUSD are where the part of the credit for proactive
	// work stands now, as Spend's; the share is taken to stay as it is for
	// the rest of the month.
	SpendableMicroUSD     int64
	ShareBasisPoints      int
	PoolMicroUSD          int64
	ProactiveWeekMicroUSD int64
	// RunsOutOn is the day by whose end what is expected from now passes
	// the credit; nil when it lasts the month. The person's own spending is
	// still counted after it, at the same rate.
	RunsOutOn *time.Time
	// ProactiveSkipsFrom is the first day a run is expected to be skipped
	// because the part for proactive work is used up; nil when none is this
	// month. When it is used up now, it is a day after ProactiveAgainAt.
	ProactiveSkipsFrom *time.Time
	// ProactiveAgainAt is, while the part for proactive work is used up
	// now, when it frees up as the oldest spending leaves the 7 days;
	// adding credit frees it sooner. Nil while it is not used up.
	ProactiveAgainAt *time.Time
	// Items holds one entry for each of the person's triggers.
	Items []ForecastItem

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the forecast, with every
// field, including those this package does not model; nil for a Forecast
// the program built itself.
func (f *Forecast) RawJSON() json.RawMessage { return f.raw }

// A ForecastDay is one day of a forecast's month: what was spent so far
// and what the rest of it is expected to cost, each half apart.
type ForecastDay struct {
	// Date is the day, as a Forecast's dates are.
	Date time.Time
	// PersonLedMicroUSD and ProactiveMicroUSD are what the person's own
	// conversations and proactive work spent that day so far.
	PersonLedMicroUSD int64
	ProactiveMicroUSD int64
	// ExpectedPersonLedMicroUSD and ExpectedProactiveMicroUSD are what the
	// rest of the day is expected to cost each; zero on a day that is
	// over.
	ExpectedPersonLedMicroUSD int64
	ExpectedProactiveMicroUSD int64
}

// A ForecastItem is one trigger's month: what its runs cost so far and
// what the month comes to, its runs by day, and its runs to come.
type ForecastItem struct {
	// TriggerID is the trigger, which a Watch names as its TriggerID.
	TriggerID string
	// FiresOn is how it runs.
	FiresOn FiresOn
	// Paused says it is paused, so nothing more is expected of it.
	Paused bool
	// Half is where its runs are billed.
	Half SpendHalf
	// CountedIn is, for a follow-up in a conversation another of the
	// person's triggers started, that trigger's TriggerID: the follow-up's
	// runs are inside that trigger's month and count toward its
	// allowance. Empty otherwise.
	CountedIn string
	// PerWeekMicroUSD is its weekly allowance; zero when it has none.
	PerWeekMicroUSD int64
	// PerRunMicroUSD is the mean cost of its latest five runs that worked;
	// nil before its first, when there is no cost to estimate from.
	PerRunMicroUSD *int64
	// SpentMicroUSD is what its runs cost this month so far, and Runs how
	// many of them worked.
	SpentMicroUSD int64
	Runs          int
	// Days holds every day of the month in the forecast's zone.
	Days []ForecastItemDay
	// ForecastMicroUSD is what its month comes to: SpentMicroUSD and the
	// runs expected that are not skipped; nil with no PerRunMicroUSD.
	ForecastMicroUSD *int64
	// MostMicroUSD is, with no PerRunMicroUSD, an allowance and not
	// paused, the most it can spend by the month's end: its allowance
	// over the time left. Nil otherwise.
	MostMicroUSD *int64
	// ExpectedAt holds, for a schedule or a follow-up, the times of its
	// runs expected this month, skipped ones included.
	ExpectedAt []time.Time
	// PerDay is, for a trigger that runs when something arrives, its runs
	// a day over the last 28 days, or since it was made; zero for the
	// others.
	PerDay float64
	// ExpectedRuns is how many runs are expected this month, skipped ones
	// included; AllowanceSkips and ShareSkips how many of them its own
	// allowance and the part for proactive work would skip.
	ExpectedRuns   int
	AllowanceSkips int
	ShareSkips     int
	// Skipping is where its next run is skipped; nil when it is not.
	Skipping *ForecastSkipping
}

// InProactiveHalf reports whether the item's month is its own part of the
// proactive half: billed as proactive work and counted inside no other
// item. The months of the items for which it is true, and
// Forecast.UnlistedProactiveMicroUSD, add up to the month's proactive
// spending.
func (it *ForecastItem) InProactiveHalf() bool {
	return it != nil && it.Half == HalfProactive && it.CountedIn == ""
}

// A ForecastItemDay is one day of a ForecastItem's month.
type ForecastItemDay struct {
	// Date is the day, as a Forecast's dates are.
	Date time.Time
	// Ran and Skipped are how many of its runs worked that day and how
	// many were skipped, by its allowance or the part for proactive work.
	Ran     int
	Skipped int
	// Expected is how many runs are expected the rest of that day, and
	// ExpectedSkipped how many of those would be skipped.
	Expected        int
	ExpectedSkipped int
}

// ForecastSkipping is where a trigger's next run is skipped.
type ForecastSkipping struct {
	// Why is the reason.
	Why SkipReason
	// Until is its next run that is not skipped; nil when none is
	// expected.
	Until *time.Time
}

// forecastJSON is a Forecast as the API writes it, its dates calendar
// days.
type forecastJSON struct {
	Zone                      string             `json:"zone"`
	Today                     string             `json:"today"`
	Days                      []forecastDayJSON  `json:"days"`
	PersonLedPerDayMicroUSD   int64              `json:"person_led_per_day_micro"`
	LastMonthMicroUSD         int64              `json:"last_month_micro"`
	UnlistedProactiveMicroUSD int64              `json:"unlisted_proactive_micro"`
	SpendableMicroUSD         int64              `json:"spendable_micro"`
	ShareBasisPoints          int                `json:"share_bps"`
	PoolMicroUSD              int64              `json:"pool_micro"`
	ProactiveWeekMicroUSD     int64              `json:"proactive_week_micro"`
	RunsOutOn                 string             `json:"runs_out_on,omitempty"`
	ProactiveSkipsFrom        string             `json:"proactive_skips_from,omitempty"`
	ProactiveAgainAt          *time.Time         `json:"proactive_again_at,omitempty"`
	Items                     []forecastItemJSON `json:"items"`
}

type forecastDayJSON struct {
	Date                      string `json:"date"`
	PersonLedMicroUSD         int64  `json:"person_led_micro"`
	ProactiveMicroUSD         int64  `json:"proactive_micro"`
	ExpectedPersonLedMicroUSD int64  `json:"expected_person_led_micro"`
	ExpectedProactiveMicroUSD int64  `json:"expected_proactive_micro"`
}

type forecastItemJSON struct {
	TriggerID        string                `json:"trigger_id"`
	FiresOn          FiresOn               `json:"fires_on"`
	Paused           bool                  `json:"paused"`
	Half             SpendHalf             `json:"half"`
	CountedIn        string                `json:"counted_in,omitempty"`
	PerWeekMicroUSD  int64                 `json:"per_week_micro,omitempty"`
	PerRunMicroUSD   *int64                `json:"per_run_micro,omitempty"`
	SpentMicroUSD    int64                 `json:"spent_micro"`
	Runs             int                   `json:"runs"`
	Days             []forecastItemDayJSON `json:"days"`
	ForecastMicroUSD *int64                `json:"forecast_micro,omitempty"`
	MostMicroUSD     *int64                `json:"most_micro,omitempty"`
	ExpectedAt       []time.Time           `json:"expected_at,omitempty"`
	PerDay           float64               `json:"per_day,omitempty"`
	ExpectedRuns     int                   `json:"expected_runs"`
	AllowanceSkips   int                   `json:"allowance_skips"`
	ShareSkips       int                   `json:"share_skips"`
	Skipping         *forecastSkippingJSON `json:"skipping,omitempty"`
}

type forecastItemDayJSON struct {
	Date            string `json:"date"`
	Ran             int    `json:"ran"`
	Skipped         int    `json:"skipped"`
	Expected        int    `json:"expected"`
	ExpectedSkipped int    `json:"expected_skipped"`
}

type forecastSkippingJSON struct {
	Why   SkipReason `json:"why"`
	Until *time.Time `json:"until,omitempty"`
}

// forecastDate reads a calendar date of the forecast, or nil for none.
func forecastDate(what, s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	d, err := time.Parse(spendDate, s)
	if err != nil {
		return nil, fmt.Errorf("account: the forecast's %s %q: %w", what, s, err)
	}
	return &d, nil
}

// UnmarshalJSON decodes the forecast, its dates written as "2006-01-02",
// and keeps the JSON for RawJSON.
func (f *Forecast) UnmarshalJSON(b []byte) error {
	var v forecastJSON
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	today, err := forecastDate("today", v.Today)
	if err != nil {
		return err
	}
	out := Forecast{Zone: v.Zone, PersonLedPerDayMicroUSD: v.PersonLedPerDayMicroUSD, LastMonthMicroUSD: v.LastMonthMicroUSD,
		UnlistedProactiveMicroUSD: v.UnlistedProactiveMicroUSD, SpendableMicroUSD: v.SpendableMicroUSD, ShareBasisPoints: v.ShareBasisPoints,
		PoolMicroUSD: v.PoolMicroUSD, ProactiveWeekMicroUSD: v.ProactiveWeekMicroUSD, ProactiveAgainAt: v.ProactiveAgainAt, raw: keep(b)}
	if today != nil {
		out.Today = *today
	}
	if out.RunsOutOn, err = forecastDate("runs_out_on", v.RunsOutOn); err != nil {
		return err
	}
	if out.ProactiveSkipsFrom, err = forecastDate("proactive_skips_from", v.ProactiveSkipsFrom); err != nil {
		return err
	}
	for _, d := range v.Days {
		date, err := forecastDate("day", d.Date)
		if err != nil || date == nil {
			return fmt.Errorf("account: the forecast's day %q: %w", d.Date, err)
		}
		out.Days = append(out.Days, ForecastDay{Date: *date, PersonLedMicroUSD: d.PersonLedMicroUSD, ProactiveMicroUSD: d.ProactiveMicroUSD,
			ExpectedPersonLedMicroUSD: d.ExpectedPersonLedMicroUSD, ExpectedProactiveMicroUSD: d.ExpectedProactiveMicroUSD})
	}
	for _, it := range v.Items {
		item := ForecastItem{TriggerID: it.TriggerID, FiresOn: it.FiresOn, Paused: it.Paused, Half: it.Half, CountedIn: it.CountedIn,
			PerWeekMicroUSD: it.PerWeekMicroUSD, PerRunMicroUSD: it.PerRunMicroUSD, SpentMicroUSD: it.SpentMicroUSD, Runs: it.Runs,
			ForecastMicroUSD: it.ForecastMicroUSD, MostMicroUSD: it.MostMicroUSD, ExpectedAt: it.ExpectedAt, PerDay: it.PerDay,
			ExpectedRuns: it.ExpectedRuns, AllowanceSkips: it.AllowanceSkips, ShareSkips: it.ShareSkips}
		if s := it.Skipping; s != nil {
			item.Skipping = &ForecastSkipping{Why: s.Why, Until: s.Until}
		}
		for _, d := range it.Days {
			date, err := forecastDate("day", d.Date)
			if err != nil || date == nil {
				return fmt.Errorf("account: the forecast's day %q of %s: %w", d.Date, it.TriggerID, err)
			}
			item.Days = append(item.Days, ForecastItemDay{Date: *date, Ran: d.Ran, Skipped: d.Skipped, Expected: d.Expected, ExpectedSkipped: d.ExpectedSkipped})
		}
		out.Items = append(out.Items, item)
	}
	*f = out
	return nil
}

// GetForecast returns what the caller will have spent by the end of the
// calendar month in params.TimeZone, UTC when params is nil or names
// none: every day of the month in both halves, spent so far and expected,
// the day their credit would run out, and each of their triggers' month.
// Spending reaches it a few minutes after the turn that spent it.
//
// It reads the personal context alone: a token of an organization's
// context is refused with an error matching latere.ErrForbidden, as is a
// token narrowed by grants that cover neither starting a session nor
// sending into one. A zone the API does not know is refused with the code
// "invalid_request". A deployment whose session core does not serve the
// runs the forecast reads yet answers 503 with the code
// "forecast_unavailable", and one that keeps no wallets
// "wallet_unconfigured".
func (c *Client) GetForecast(ctx context.Context, params *GetForecastParams) (*Forecast, error) {
	q := url.Values{}
	if params != nil && params.TimeZone != "" {
		q.Set("tz", params.TimeZone)
	}
	var out Forecast
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodGet, Path: "/v1/account/wallet/forecast", Query: q}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
