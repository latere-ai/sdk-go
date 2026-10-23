// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package account

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"latere.ai/x/sdk-go/latere"
)

// MaxSpendDays is the most UTC days GetSpend reads at once.
const MaxSpendDays = 90

// spendDate is how the API writes a SpendDay's date.
const spendDate = time.DateOnly

// GetSpendParams says how far back GetSpend reads.
type GetSpendParams struct {
	// Days is how many UTC days to read, today's included, from 1 to
	// MaxSpendDays; the API's 28 when zero.
	Days int
}

// Spend is the person's spending by day, split into what they led and
// what ran with nobody watching, and where their share of credit for the
// latter stands. Work that runs with nobody watching, proactive work, is
// counted by session: what a session a trigger's firing started spends is
// proactive, and what a session the person started spends is theirs, the
// turns a wake began in it included.
type Spend struct {
	// Days holds one entry for each UTC day read, oldest first and today
	// last; a day with no spending reads zero in both.
	Days []SpendDay `json:"days"`
	// ShareBasisPoints is the share of the person's credit that goes to
	// proactive work, in hundredths of a percent: 2000, a fifth, until
	// they have spent for 28 days, then between 500 and 5000, moved once a
	// week halfway toward one less the part of their credit they spent
	// themselves over the last 28 days.
	ShareBasisPoints int `json:"share_bps"`
	// ShareMovedAt is when the share last moved, or was first read. It
	// moves again once 7 days have passed; reading the spend may move it.
	ShareMovedAt time.Time `json:"share_moved_at"`
	// SpendableMicroUSD is the credit the wallet can spend now, in
	// millionths of a US dollar, as Wallet.SpendableMicroUSD.
	SpendableMicroUSD int64 `json:"spendable_micro"`
	// ProactiveWeekMicroUSD is what proactive work spent in the last 7
	// days, in millionths of a US dollar.
	ProactiveWeekMicroUSD int64 `json:"proactive_week_micro"`
	// PoolMicroUSD is what proactive work may spend in a rolling 7 days:
	// the share of SpendableMicroUSD plus ProactiveWeekMicroUSD, so what it
	// spent does not shrink its own pool. Once ProactiveWeekMicroUSD
	// reaches it, a trigger's firing and a wake start nothing until the
	// oldest spending leaves the 7 days, and a trigger an agent offers
	// names a weekly allowance no larger than it.
	PoolMicroUSD int64 `json:"pool_micro"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the spend, with every
// field, including those this package does not model; nil for a Spend the
// program built itself.
func (s *Spend) RawJSON() json.RawMessage { return s.raw }

// UnmarshalJSON decodes the spend and keeps the JSON for RawJSON.
func (s *Spend) UnmarshalJSON(b []byte) error {
	type plain Spend
	if err := json.Unmarshal(b, (*plain)(s)); err != nil {
		return err
	}
	s.raw = keep(b)
	return nil
}

// ProactiveLeftMicroUSD returns what proactive work may still spend in
// the 7 days: PoolMicroUSD less ProactiveWeekMicroUSD, never below zero.
// At zero, no trigger's firing and no wake starts anything.
func (s *Spend) ProactiveLeftMicroUSD() int64 {
	if s == nil {
		return 0
	}
	return max(s.PoolMicroUSD-s.ProactiveWeekMicroUSD, 0)
}

// A SpendDay is what one UTC day spent.
type SpendDay struct {
	// Date is the day, at midnight UTC.
	Date time.Time `json:"date"`
	// PersonLedMicroUSD is what the person's own sessions spent that day,
	// the turns wakes began in them included, in millionths of a US
	// dollar.
	PersonLedMicroUSD int64 `json:"person_led_micro"`
	// ProactiveMicroUSD is what the sessions a trigger's firing started
	// spent that day, in millionths of a US dollar.
	ProactiveMicroUSD int64 `json:"proactive_micro"`
}

// spendDayJSON is a SpendDay as the API writes it, its date a calendar
// day rather than a timestamp.
type spendDayJSON struct {
	Date              string `json:"date"`
	PersonLedMicroUSD int64  `json:"person_led_micro"`
	ProactiveMicroUSD int64  `json:"proactive_micro"`
}

// UnmarshalJSON decodes a day, its date written as "2006-01-02".
func (d *SpendDay) UnmarshalJSON(b []byte) error {
	var v spendDayJSON
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	date, err := time.Parse(spendDate, v.Date)
	if err != nil {
		return fmt.Errorf("account: the day %q of the spend: %w", v.Date, err)
	}
	*d = SpendDay{Date: date, PersonLedMicroUSD: v.PersonLedMicroUSD, ProactiveMicroUSD: v.ProactiveMicroUSD}
	return nil
}

// MarshalJSON writes a day as the API does, its date as "2006-01-02" in
// UTC.
func (d SpendDay) MarshalJSON() ([]byte, error) {
	return json.Marshal(spendDayJSON{Date: d.Date.UTC().Format(spendDate), PersonLedMicroUSD: d.PersonLedMicroUSD, ProactiveMicroUSD: d.ProactiveMicroUSD})
}

// GetSpend returns the caller's spending by day, split into what they led
// and what ran with nobody watching, with their share of credit for the
// latter, its pool and what it spent in the last 7 days. Spending reaches
// it a few minutes after the turn that spent it.
//
// It reads the personal context alone: a token of an organization's
// context is refused with an error matching latere.ErrForbidden, as is a
// token narrowed by grants that cover neither starting a session nor
// sending into one. Days outside 1 to MaxSpendDays are refused before any
// request, and a deployment that keeps no wallets answers 503 with the
// code "wallet_unconfigured".
func (c *Client) GetSpend(ctx context.Context, params *GetSpendParams) (*Spend, error) {
	q := url.Values{}
	if params != nil && params.Days != 0 {
		if params.Days < 1 || params.Days > MaxSpendDays {
			return nil, fmt.Errorf("account: GetSpend: %d days is not from 1 to %d", params.Days, MaxSpendDays)
		}
		q.Set("days", strconv.Itoa(params.Days))
	}
	var out Spend
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodGet, Path: "/v1/account/wallet/spend", Query: q}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
