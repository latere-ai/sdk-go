// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents

// A trigger's looks are the turns its firings began, in the sessions it
// started or continued, and the turns wakes began in the sessions it
// started. The API counts what they cost over a rolling 7 days against
// the trigger's TriggerAllowance, writes that week on the trigger's
// status at each firing that counts toward it, as a TriggerSpend, and
// tells the turn the firing begins the same numbers on its message, as a
// FiringSpend. The API enforces the allowance; the numbers are for a
// program to show the week and the pace, and for the agent to pace its
// looks by.

// TriggerSpend is a trigger's week as of its latest firing that counts
// toward it, TriggerStatus.Spend. It holds the looks that ended before
// that firing.
type TriggerSpend struct {
	// WindowDays is how many days the spend is counted over: 7.
	WindowDays int `json:"windowDays"`
	// SpentMicroUSD is what the looks counted toward the trigger cost in
	// the window, in millionths of a US dollar.
	SpentMicroUSD int64 `json:"spentUsdMicro"`
	// PerWeekMicroUSD is the trigger's allowance when the spend was
	// written; zero when it named none.
	PerWeekMicroUSD int64 `json:"perWeekUsdMicro,omitempty"`
	// PerLookMicroUSD is the mean cost of the latest five looks that
	// ended; nil before any look ended.
	PerLookMicroUSD *int64 `json:"perLookUsdMicro,omitempty"`
	// ExpectedLooks is how many looks are expected from the firing that
	// wrote the spend to the end of the window, that firing's own
	// included: a schedule's fire times, one for a trigger that fires
	// once, an event trigger's recent rate over the time left, and the
	// firing's own alone for a paused trigger.
	ExpectedLooks int `json:"expectedLooks"`
	// ProjectedMicroUSD is what the window is on course to cost:
	// SpentMicroUSD plus PerLookMicroUSD times ExpectedLooks. The window
	// ends 7 days after the oldest look it counts, so this is the spend of
	// one whole window. Nil with PerLookMicroUSD.
	ProjectedMicroUSD *int64 `json:"projectedUsdMicro,omitempty"`
}

// AheadOfPace reports whether the window is on course to cost more than
// the allowance: ProjectedMicroUSD past PerWeekMicroUSD. It is false
// without an allowance or a projection, and for a nil spend.
func (s *TriggerSpend) AheadOfPace() bool {
	return s != nil && aheadOfPace(s.PerWeekMicroUSD, s.ProjectedMicroUSD)
}

// AtAllowance reports whether the spend had reached the allowance when it
// was written, so the API started no look until the oldest left the
// window. It is false without an allowance, and for a nil spend.
func (s *TriggerSpend) AtAllowance() bool {
	return s != nil && s.PerWeekMicroUSD > 0 && s.SpentMicroUSD >= s.PerWeekMicroUSD
}

// FiringSpend is the spend a firing's message carries, UserMessage.Spend:
// the week of the trigger the turn counts toward, as the firing found it,
// which the turn's model reads in one line before the message. A wake's
// message carries the week of the trigger that started its session, and
// none when a person started it.
type FiringSpend struct {
	// TriggerID is the trigger the turn counts toward: the trigger that
	// fired, or for a wake, the trigger that started the session.
	TriggerID string `json:"trigger_id"`
	// WindowDays, SpentMicroUSD, PerWeekMicroUSD, PerLookMicroUSD,
	// ExpectedLooks and ProjectedMicroUSD are the trigger's TriggerSpend
	// fields of the same names, written by the same firing.
	WindowDays        int    `json:"window_days"`
	SpentMicroUSD     int64  `json:"spent_usd_micro"`
	PerWeekMicroUSD   int64  `json:"per_week_usd_micro,omitempty"`
	PerLookMicroUSD   *int64 `json:"per_look_usd_micro,omitempty"`
	ExpectedLooks     int    `json:"expected_looks"`
	ProjectedMicroUSD *int64 `json:"projected_usd_micro,omitempty"`
}

// AheadOfPace reports whether the window was on course to cost more than
// the allowance when the turn began, which the turn's model was told:
// ProjectedMicroUSD past PerWeekMicroUSD. It is false without an
// allowance or a projection, and for a nil spend.
func (s *FiringSpend) AheadOfPace() bool {
	return s != nil && aheadOfPace(s.PerWeekMicroUSD, s.ProjectedMicroUSD)
}

// aheadOfPace reports a projection past an allowance; false when either
// is missing.
func aheadOfPace(perWeek int64, projected *int64) bool {
	return perWeek > 0 && projected != nil && *projected > perWeek
}
