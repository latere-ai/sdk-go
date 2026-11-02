// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package oversight

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"latere.ai/x/sdk-go/latere"
)

// The page bounds of ListCases.
const (
	// DefaultCasesLimit is how many cases a page holds when Limit is zero.
	DefaultCasesLimit = 100
	// MaxCasesLimit is the most cases a page holds.
	MaxCasesLimit = 500
)

// CasesParams name whose steps ListCases reads, and which.
type CasesParams struct {
	// Org names the context and Subject the person, as the package
	// documentation says. Both are required.
	Org, Subject string
	// ActionID keeps the one step it names, as ActionID builds it.
	ActionID string
	// Pending keeps the steps the person was shown, before or after they
	// ran, that they have not checked yet: their queue of steps to check.
	Pending bool
	// Limit is how many cases a page holds at most, up to MaxCasesLimit;
	// zero is DefaultCasesLimit.
	Limit int
	// Cursor asks ListCasesPage for the page after the one that returned
	// it. ListCases ignores it.
	Cursor string
}

// Case is everything the decision service holds about one step: the step
// without its input, the suggestion if one was made, the decision, and
// the person's answers.
type Case struct {
	Action CaseAction `json:"action"`
	// Prediction is the decision service's suggestion; nil for a step it
	// was not asked about.
	Prediction *Prediction `json:"prediction,omitempty"`
	// Decision is what was done; nil for a step suggested about but not
	// yet decided.
	Decision *Decision `json:"decision,omitempty"`
	// Answers are the person's answers about the step, oldest first.
	Answers []RecordedAnswer `json:"answers,omitempty"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the case, with every
// field, including those this version of the package does not model.
func (c *Case) RawJSON() json.RawMessage { return c.raw }

// UnmarshalJSON decodes the case and keeps the JSON for RawJSON.
func (c *Case) UnmarshalJSON(b []byte) error {
	type plain Case
	if err := json.Unmarshal(b, (*plain)(c)); err != nil {
		return err
	}
	c.raw = keep(b)
	return nil
}

// CaseAction is a step as the decision service keeps it. Its input is
// never kept.
type CaseAction struct {
	Org      string `json:"org"`
	Subject  string `json:"subject"`
	ActionID string `json:"action_id"`
	// Kind is the domain of the step and Name the operation within it,
	// such as "tool" and "bash".
	Kind string `json:"kind"`
	Name string `json:"name"`
	// Effect is the widest effect the step can have: "none", "read",
	// "write" or "external".
	Effect string `json:"effect,omitempty"`
	// Actor is who proposed the step, when the caller said.
	Actor *Actor `json:"actor,omitempty"`
}

// Actor is who proposed a step.
type Actor struct {
	// Kind is the kind of proposer, such as "agent".
	Kind string `json:"kind"`
	// ID names the proposer, such as an agent's id.
	ID string `json:"id,omitempty"`
	// Session is the conversation the step was proposed in.
	Session string `json:"session,omitempty"`
}

// Prediction is the decision service's suggestion for one step, as it was
// made.
type Prediction struct {
	// Verdict is the suggestion: VerdictAllow, VerdictAsk or VerdictBlock.
	Verdict Verdict `json:"verdict"`
	// Approve is the chance, from 0 to 1, that the person allows the step.
	Approve float64 `json:"approve"`
	// Uncertainty is how unsure the decision service is of Approve.
	Uncertainty float64 `json:"uncertainty"`
	// EvidenceRatio is how much the person's answers say about the step,
	// from 0 for nothing to 1; nil from a decision service that did not
	// record it.
	EvidenceRatio *float64 `json:"evidence_ratio,omitempty"`
	// AskKind says why the suggestion is to ask; empty for another
	// verdict, or from a decision service that did not record it.
	AskKind AskKind `json:"ask_kind,omitempty"`
	// Thresholds and AuditRate are those of the trust profile that
	// applied, recorded because a profile can change.
	Thresholds Thresholds `json:"thresholds"`
	AuditRate  float64    `json:"audit_rate"`
	// Profile is the scope of the trust profile that applied, the most
	// specific one saved: with Agent, the agent's own; without it, the
	// organization's, for one Kind of step where Kind is set, or the
	// built-in default where none is saved. Subject is set as well when
	// the person's own profile made one of its numbers stricter;
	// Thresholds and AuditRate are then the stricter of the two. Empty
	// from a decision service that did not record it.
	Profile Scope `json:"profile"`
	// Source names the model, such as "learned/3".
	Source string `json:"source"`
	// Reason is one sentence for the person; never show it to an agent.
	Reason string `json:"reason,omitempty"`
}

// Decision is what was done about a step.
type Decision struct {
	// Verdict is the verdict applied.
	Verdict Verdict `json:"verdict"`
	// ReviewProbability is the chance, fixed before the step ran, that the
	// person is shown it: 1 for a step that waited for them.
	ReviewProbability float64 `json:"review_probability"`
	// Source names what decided, as the caller that recorded the decision
	// sent it, such as a rule set "rules/1". Topos sends its mode and the
	// layer that decided the step, such as "topos/progressive/score" or
	// "topos/progressive/learned", and an older Topos the mode alone, such
	// as "topos/confirm".
	Source string `json:"source"`
}

// RecordedAnswer is one answer of the person about a step.
type RecordedAnswer struct {
	// Approve is whether the person allows the step, or would have.
	Approve bool `json:"approve"`
	// By is who answered.
	By string    `json:"by"`
	At time.Time `json:"at"`
	// Review is true for the answer that checks a step the person was
	// shown, the one answer that counts toward how often the decision
	// service is wrong.
	Review bool `json:"review"`
}

// ListCases returns an iterator over the steps params keeps, oldest
// first, fetching pages as the loop needs them. An error ends the
// iteration after it is yielded. It refuses a caller that may not read
// the person's steps with an error matching latere.ErrForbidden.
func (c *Client) ListCases(ctx context.Context, params *CasesParams) iter.Seq2[*Case, error] {
	return latere.Items(ctx, func(ctx context.Context, cursor string) (*latere.Page[*Case], error) {
		return c.casesPage(ctx, params, cursor)
	})
}

// ListCasesPage returns one page of the steps params keeps, oldest first,
// starting at params.Cursor, and the cursor of the next page in
// NextCursor.
func (c *Client) ListCasesPage(ctx context.Context, params *CasesParams) (*latere.Page[*Case], error) {
	cursor := ""
	if params != nil {
		cursor = params.Cursor
	}
	return c.casesPage(ctx, params, cursor)
}

func (c *Client) casesPage(ctx context.Context, params *CasesParams, cursor string) (*latere.Page[*Case], error) {
	if params == nil || params.Org == "" || params.Subject == "" {
		return nil, errors.New("oversight: ListCases needs the Org and the Subject")
	}
	q := url.Values{"org": {params.Org}, "subject": {params.Subject}}
	if params.ActionID != "" {
		q.Set("action_id", params.ActionID)
	}
	if params.Pending {
		q.Set("pending", "true")
	}
	if params.Limit > 0 {
		q.Set("limit", strconv.Itoa(params.Limit))
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	// The API answers a page of cases as entries, not items.
	var page struct {
		Entries    []*Case `json:"entries"`
		NextCursor string  `json:"next_cursor,omitempty"`
	}
	if err := c.do(ctx, &latere.Request{Method: http.MethodGet, Path: BasePath + "/cases", Query: q}, &page); err != nil {
		return nil, err
	}
	if page.Entries == nil {
		page.Entries = []*Case{}
	}
	return &latere.Page[*Case]{Items: page.Entries, NextCursor: page.NextCursor}, nil
}

// The sources GetReliability reports on besides a rule set's or a
// signal's own.
const (
	// OfLearned is the decision service's own suggestions.
	OfLearned = "learned"
	// OfApplied is the verdicts applied, whatever decided them.
	OfApplied = "applied"
)

// ReliabilityParams name whose verdicts GetReliability checks, against
// which person, over which window.
type ReliabilityParams struct {
	// Org names the context and Subject the person, as the package
	// documentation says. Both are required.
	Org, Subject string
	// Of is OfLearned, OfApplied, or a decision's source such as
	// "rules/1". Required.
	Of string
	// From and To bound the window; From must be before To. Both are
	// required.
	From, To time.Time
}

// Reliability is how often one source's verdicts went against one person
// over one window. A source allowed a step when it let it run with no one
// asked, and blocked one when it stopped it.
type Reliability struct {
	Org     string    `json:"org"`
	Subject string    `json:"subject"`
	Of      string    `json:"of"`
	From    time.Time `json:"from"`
	To      time.Time `json:"to"`
	// FalseAllow estimates the share of the source's allows the person
	// would have denied; FalseBlock the share of its blocks they would
	// have allowed.
	FalseAllow Estimate `json:"false_allow"`
	FalseBlock Estimate `json:"false_block"`
	// Certificate tests the trust profile's claim against the person's
	// latest checks.
	Certificate Certificate `json:"certificate"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the report, with every
// field, including those this version of the package does not model.
func (r *Reliability) RawJSON() json.RawMessage { return r.raw }

// UnmarshalJSON decodes the report and keeps the JSON for RawJSON.
func (r *Reliability) UnmarshalJSON(b []byte) error {
	type plain Reliability
	if err := json.Unmarshal(b, (*plain)(r)); err != nil {
		return err
	}
	r.raw = keep(b)
	return nil
}

// Estimate is one error rate, estimated from the steps the person checked.
type Estimate struct {
	// Value is the rate, from 0 to 1; nil when no step in the window
	// could be checked, so the rate is unknown, which is not zero.
	Value *float64 `json:"value"`
	// Interval is a 95% interval for the rate, present with Value.
	Interval *[2]float64 `json:"interval,omitempty"`
	// Covered is the share of the source's verdicts in the window the
	// estimate speaks for: those the person could have been shown.
	Covered float64 `json:"covered"`
	// Reviews counts the checks the estimate rests on, and Audits those
	// among them of steps picked at random.
	Reviews int `json:"reviews"`
	Audits  int `json:"audits"`
	// Unanswered counts steps the person was shown and has not checked.
	Unanswered int `json:"unanswered"`
}

// Certificate tests a trust profile's claim, a false-allow rate below
// Alpha at confidence 1 - Delta, against the person's latest run of
// checked steps they agreed with.
type Certificate struct {
	Alpha float64 `json:"alpha"`
	Delta float64 `json:"delta"`
	// Clean counts the checked steps in a row, latest first, the person
	// agreed with; one they would have denied ends the run.
	Clean int `json:"clean"`
	// Needed is how long the run must be for the claim to hold.
	Needed int  `json:"needed"`
	Holds  bool `json:"holds"`
}

// GetReliability reports how often the source params names went against
// the person over the window. It refuses a caller that may not read the
// person's reports with an error matching latere.ErrForbidden.
func (c *Client) GetReliability(ctx context.Context, params *ReliabilityParams) (*Reliability, error) {
	if params == nil || params.Org == "" || params.Subject == "" || params.Of == "" {
		return nil, errors.New("oversight: GetReliability needs the Org, the Subject and Of")
	}
	if params.From.IsZero() || params.To.IsZero() || !params.From.Before(params.To) {
		return nil, errors.New("oversight: GetReliability needs a window whose From is before its To")
	}
	q := url.Values{
		"org": {params.Org}, "subject": {params.Subject}, "of": {params.Of},
		"from": {params.From.UTC().Format(time.RFC3339Nano)}, "to": {params.To.UTC().Format(time.RFC3339Nano)},
	}
	var out Reliability
	if err := c.do(ctx, &latere.Request{Method: http.MethodGet, Path: BasePath + "/reliability", Query: q}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
