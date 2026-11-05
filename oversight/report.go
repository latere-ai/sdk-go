// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package oversight

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"latere.ai/x/sdk-go/latere"
)

// The fixed numbers of an organization's report.
const (
	// ReportMinMembers is the smallest group a number of a report comes
	// from: a week, an agent, and the other agents together show their
	// numbers only when this many members took part, and a number read
	// from members' checks needs checks from this many members.
	ReportMinMembers = 5
	// ReportMaxWeeks is the most weeks one report holds.
	ReportMaxWeeks = 13
)

// WeekState says what a week of a report shows.
type WeekState string

// The states of a week.
const (
	// WeekOpen is a week that ended less than 7 days ago, or has not
	// ended: it shows its bounds alone, since a member can still check its
	// steps.
	WeekOpen WeekState = "open"
	// WeekTooFew is a closed week in which fewer than ReportMinMembers
	// members took part: it shows its bounds alone, and not how many.
	WeekTooFew WeekState = "too_few"
	// WeekReady is a closed week with enough members: it shows its
	// numbers.
	WeekReady WeekState = "ready"
)

// ReportParams name the organization and the weeks GetReport reads.
type ReportParams struct {
	// Org is the organization's id. Required.
	Org string
	// From and To bound the weeks: each is a Monday at 00:00 UTC, and To
	// is after From by at most ReportMaxWeeks weeks. Both are required;
	// the API refuses other bounds with a 400 *latere.Error whose Code is
	// "invalid_query".
	From, To time.Time
}

// Report is an organization's report: how the steps of its agents went
// across all its members, one Week for each week of the request, the
// oldest first. It never shows one member: it names no person, no step
// and no time finer than a week, and its only names are agent ids. A week
// a report showed never changes, also after a member is forgotten.
type Report struct {
	Org  string    `json:"org"`
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
	// MinMembers is ReportMinMembers.
	MinMembers int    `json:"min_subjects"`
	Weeks      []Week `json:"weeks"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the report, with every
// field, including those this version of the package does not model.
func (r *Report) RawJSON() json.RawMessage { return r.raw }

// UnmarshalJSON decodes the report and keeps the JSON for RawJSON.
func (r *Report) UnmarshalJSON(b []byte) error {
	type plain Report
	if err := json.Unmarshal(b, (*plain)(r)); err != nil {
		return err
	}
	r.raw = keep(b)
	return nil
}

// Week is one week of a report. A WeekOpen or WeekTooFew week has its
// bounds and its state alone. A WeekReady week has Total, and, when it
// divides by agent, Agents and Other, whose counts add up to Total's.
type Week struct {
	From  time.Time `json:"from"`
	To    time.Time `json:"to"`
	State WeekState `json:"state"`
	// Total is the whole organization in the week.
	Total *ReportCell `json:"total,omitempty"`
	// Agents has one cell for each agent with ReportMinMembers members or
	// more in the week.
	Agents []AgentCell `json:"agents,omitempty"`
	// Other has the steps of every other agent and the steps of no agent;
	// nil when there are none. An agent with too few members is counted
	// here, and so, when the others have too few members, is the agent
	// with the fewest.
	Other *ReportCell `json:"other,omitempty"`
}

// ReportCell is the numbers of a group of a week's steps. Members,
// Decisions and FlagChecks.Checks show whenever the cell does. A number
// read from members' checks, FlagChecks.Denied and the four rates, shows
// only when its checks come from ReportMinMembers members or more and no
// member gave more than half of them; otherwise it is nil and its JSON
// path, such as "flag_reviews.denied" or "applied.false_allow", is in
// Withheld. A number withheld in one agent's cell or Other is withheld in
// all of them, so none is Total less the others.
type ReportCell struct {
	// Members counts the members with a decided step in the cell.
	Members   int           `json:"subjects"`
	Decisions VerdictCounts `json:"decisions"`
	// FlagChecks is what the checks of the steps that ran and were flagged
	// for a member to check said.
	FlagChecks FlagChecks `json:"flag_reviews"`
	// Applied is how often the verdicts applied went against the members,
	// and Learned how often the decision service's suggestions did, each
	// one estimate over every step of the cell, as GetReliability makes
	// one for one person. A check after the week closed counts as not
	// given.
	Applied Rates `json:"applied"`
	Learned Rates `json:"learned"`
	// Withheld lists the JSON paths of the numbers left out, never nil.
	Withheld []string `json:"withheld"`
}

// AgentCell is the cell of one agent.
type AgentCell struct {
	// Agent is the agent's id.
	Agent string `json:"agent"`
	ReportCell
}

// VerdictCounts count decided steps by the verdict applied.
type VerdictCounts struct {
	Allow int `json:"allow"`
	Flag  int `json:"flag"`
	Ask   int `json:"ask"`
	Block int `json:"block"`
}

// FlagChecks count the steps applied VerdictFlag that a member checked
// before the week closed, and among them those the member denied: how
// often a step that ran should have asked.
type FlagChecks struct {
	Checks int `json:"reviews"`
	// Denied is nil when withheld.
	Denied *int `json:"denied,omitempty"`
}

// Rates are one source's two error rates in a cell. A rate is nil when
// withheld; a rate no member could check has a nil Value, which is not
// zero.
type Rates struct {
	FalseAllow *Estimate `json:"false_allow,omitempty"`
	FalseBlock *Estimate `json:"false_block,omitempty"`
}

// GetReport reads the report of the organization params names. Only an
// owner or an admin of the organization reads it; anyone else is refused
// with an error matching latere.ErrForbidden. Bounds that are not Mondays
// at 00:00 UTC, or more than ReportMaxWeeks weeks apart, are refused with
// a 400 *latere.Error whose Code is "invalid_query".
func (c *Client) GetReport(ctx context.Context, params *ReportParams) (*Report, error) {
	if params == nil || params.Org == "" {
		return nil, errors.New("oversight: GetReport needs the Org")
	}
	if params.From.IsZero() || params.To.IsZero() || !params.From.Before(params.To) {
		return nil, errors.New("oversight: GetReport needs weeks whose From is before their To")
	}
	q := url.Values{
		"org":  {params.Org},
		"from": {params.From.UTC().Format(time.RFC3339Nano)}, "to": {params.To.UTC().Format(time.RFC3339Nano)},
	}
	var out Report
	if err := c.do(ctx, &latere.Request{Method: http.MethodGet, Path: BasePath + "/reports", Query: q}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
