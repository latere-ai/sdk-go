// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package oversight reads what Latere learned about when to ask a person,
// and lets that person correct it.
//
// A step is one tool call of an agent in a session. Before a step runs,
// the platform's rules decide it, or, for a step the rules leave open, it
// asks the decision service how likely the person is to allow it. The
// decision service learns that likelihood from the person's own Allow and
// Deny answers.
//
// In learned mode, the decision service decides a step of a progressive
// session if it has evidence about the person. Elsewhere, the rules and
// the risk score decide, and the decision service only suggests. It has
// evidence if it suggests allow or block, or if it asks with AskSplit, the
// person's answers about this kind of step disagree, or with AskWider, the
// step reaches wider than any the person allowed. For an ask of another
// kind (AskUnsure, AskShort, AskFew), the rules and the risk score decide.
//
// The decision service suggests allow only for a step that the person's
// approvals cover: approvals of the same kind of step, of a Scope at least
// as wide, with enough weight from enough sessions. A quick approval with
// no details opened has no weight. A step that is not covered is an ask,
// AskWider or AskFew, and its Prediction says how far the cover is in
// Cover.
//
// This package is how a program shows a person what it learned and lets
// them steer it:
//
//   - GetLearned lists each kind of step the person answered, with the
//     chance they allow it and what the decision service would suggest
//     now, and how much the person let steps run lately.
//   - GetExplanation says why the decision service suggested what it did
//     for one step: the answers that moved it most, and how much of it
//     comes from steps like this one and how much from the person's
//     answers lately.
//   - Preview counts what another trust profile would have done with the
//     person's recent steps, and GetProfile and PutProfile read and choose
//     the profile. DeleteProfile removes a saved one, so an agent goes
//     back to its organization's profile. A person's own profile in an
//     organization, a Scope with Subject, only ever makes the
//     organization's profile stricter for that person.
//   - GetReport reads an organization's weekly report: how its agents'
//     steps went across all its members, never one person.
//   - ListCases lists the steps the decision service holds about the
//     person, each with its suggestion, its decision and the person's
//     answers, or only those waiting for the person to check them.
//   - GetReliability reports how often the decision service, or a rule
//     set, went against the person over a window.
//   - GetCalibration compares the chances the decision service stated
//     for the person's steps with the rates their checks measured, in ten
//     bins, which say when a client may show a chance.
//   - Answer tells the decision service what the person thinks of a step,
//     such as one that ran on its own and was picked for them to check.
//   - Forget removes everything the decision service holds about the
//     person.
//
// Every call names a context by Org and the person by Subject. In a
// person's own context, both are the person's subject: their issuer and
// their sign-in id with a "|" between them, as Subject builds. In an
// organization's context, Org is the organization's id, which has no "|",
// and Subject is the person's subject. A step is in the context that its
// decision was sent under. The step's tool use in the agents package
// names it as DecisionOrg. A tool use without DecisionOrg is in the
// context of the person who started the session. A step is named by its
// ActionID: the conversation's session id and the step's tool use id with
// a "/" between them, as ActionID builds.
//
//	cfg, err := latere.NewConfig() // the key in LATERE_KEY
//	if err != nil {
//		return err
//	}
//	me := oversight.Subject("https://auth.latere.ai", sub)
//	learned, err := oversight.New(cfg).GetLearned(ctx, &oversight.LearnedParams{Org: me, Subject: me})
//	if err != nil {
//		return err
//	}
//	for _, k := range learned.Kinds {
//		fmt.Printf("%s: %.0f%% (%s)\n", k.Label, 100*k.Approve, k.Verdict)
//	}
//
// Every resource a call returns keeps the JSON it was decoded from, which
// RawJSON returns with every field, including those this version of the
// package does not model yet.
package oversight

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"latere.ai/x/sdk-go/latere"
)

// BasePath is the path the API serves the oversight routes under, below
// the base URL a Config names: every request the package sends is under
// it. A program that takes the address of the oversight API as one URL
// with this path, as a command's flag may, cuts it off to name the base
// URL WithBaseURL takes.
const BasePath = "/v1/oversight"

// Client reaches the oversight part of the Latere API. It is safe for
// concurrent use.
type Client struct {
	cfg *latere.Config
}

// New returns a Client that sends its requests as cfg says. A cfg not
// built with latere.NewConfig makes every call fail with an error saying
// so.
func New(cfg *latere.Config) *Client { return &Client{cfg: cfg} }

// do sends r and decodes its answer into out, with the sentinel of the
// API's code on a refusal that has one.
func (c *Client) do(ctx context.Context, r *latere.Request, out any) error {
	return withSentinel(c.cfg.Do(ctx, r, out))
}

// Subject returns the subject of a person: their issuer, with no trailing
// slash, and their sign-in id, with a "|" between them. In a person's own
// context it is also the Org of every call.
func Subject(issuer, sub string) string {
	return strings.TrimRight(issuer, "/") + "|" + sub
}

// ActionID returns the id of a step of a conversation: the session's id
// and the step's tool use id, with a "/" between them.
func ActionID(session, toolUseID string) string { return session + "/" + toolUseID }

// Verdict is what happens to a step.
type Verdict string

// The verdicts, from the one that lets the most through.
const (
	// VerdictAllow runs the step, and no one checks it.
	VerdictAllow Verdict = "allow"
	// VerdictFlag runs the step, and the person checks it after it ran.
	VerdictFlag Verdict = "flag"
	// VerdictAsk holds the step until the person allows or denies it.
	VerdictAsk Verdict = "ask"
	// VerdictBlock does not run the step.
	VerdictBlock Verdict = "block"
)

// AskKind says why the decision service suggests asking about a step.
type AskKind string

// The kinds of asking.
const (
	// AskUnsure is a step the person's answers say little about yet, an
	// EvidenceRatio below 0.5; more answers can move it.
	AskUnsure AskKind = "unsure"
	// AskSplit is a step the person's answers say a lot about, an
	// EvidenceRatio of 0.5 or more, with Approve still between the
	// thresholds, and the person both allowed and denied steps of its
	// kind: the answers disagree. A decision service that does not send
	// AskShort also gives AskSplit for answers that agree.
	AskSplit AskKind = "split"
	// AskShort is a step the person's answers say a lot about, an
	// EvidenceRatio of 0.5 or more, with Approve still between the
	// thresholds, and the answers agree: two Allows of a step and no Deny
	// can give 0.80 under an AllowAbove of 0.90, and more Allows can move
	// it above 0.90.
	AskShort AskKind = "short"
	// AskWider is a step that no approval of the person covers: no step of
	// its kind they allowed had a Scope this wide. The model alone would
	// suggest allow.
	AskWider AskKind = "wider"
	// AskFew is a step whose Scope the person allowed, but with too little
	// weight or in too few sessions, as its Cover says. The model alone
	// would suggest allow.
	AskFew AskKind = "few"
)

// LearnedParams names whose learned view GetLearned reads.
type LearnedParams struct {
	// Org names the context and Subject the person, as the package
	// documentation says. Both are required.
	Org, Subject string
	// Agent and Kind narrow the cost profile the suggestions are made
	// under to an agent's or a kind of step's, as GetProfile does. Empty
	// is the person's own.
	Agent, Kind string
}

// ModelRef names one model the decision service fitted for a person: where
// its predictions come from and how many of the person's answers it was
// fitted on. Two predictions with the same ModelRef came from the same
// model.
type ModelRef struct {
	Source  string `json:"source"`
	Answers int64  `json:"answers"`
}

// Learned is what the decision service learned about a person.
type Learned struct {
	Org     string `json:"org"`
	Subject string `json:"subject"`
	// Model is the model the view is of: the person's current one.
	Model ModelRef `json:"model"`
	// Kinds are the kinds of step the person answered, one for each label,
	// the most answered first.
	Kinds []LearnedKind `json:"entries"`
	// Recent is how much the person let steps run lately, at up to 20
	// points of their history, the last one now.
	Recent []TrendPoint `json:"recent"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the view, with every
// field, including those this version of the package does not model.
func (l *Learned) RawJSON() json.RawMessage { return l.raw }

// UnmarshalJSON decodes the view and keeps the JSON for RawJSON.
func (l *Learned) UnmarshalJSON(b []byte) error {
	type plain Learned
	if err := json.Unmarshal(b, (*plain)(l)); err != nil {
		return err
	}
	l.raw = keep(b)
	return nil
}

// LearnedKind is one kind of step the person answered.
type LearnedKind struct {
	// Label names the kind of step from a fixed vocabulary, such as
	// "bash git push" or "web_fetch". It never holds text of a step's
	// arguments.
	Label string `json:"label"`
	// Kind and Name are the kind and the name of the steps of the label,
	// such as "tool" and "bash".
	Kind string `json:"kind"`
	Name string `json:"name"`
	// Approved and Denied count the person's answers to steps of the
	// label.
	Approved int `json:"approved"`
	Denied   int `json:"denied"`
	// Approve is the chance, from 0 to 1, that the person allows a step of
	// the label now, and Uncertainty how unsure the decision service is of
	// it.
	Approve     float64 `json:"approve"`
	Uncertainty float64 `json:"uncertainty"`
	// Verdict is what the decision service would suggest for a step of the
	// label now, under Thresholds, those of the cost profile that applies.
	Verdict    Verdict    `json:"verdict"`
	Thresholds Thresholds `json:"thresholds"`
	// EvidenceRatio and AskKind are as an Explanation of a step of the
	// label now would have them, so a view can draw a step the decision
	// service cannot tell yet apart from one the person is split on.
	EvidenceRatio float64 `json:"evidence_ratio"`
	AskKind       AskKind `json:"ask_kind,omitempty"`
	// Cover is how far the person's approvals cover a step of the label
	// now, as a Prediction's Cover says, without Nearest; nil from a
	// decision service that does not send it.
	Cover *Cover `json:"cover,omitempty"`
}

// TrendPoint is how much the person let steps run at one point of their
// history: above 0 they let more run than the decision service assumes of
// anyone, below 0 less.
type TrendPoint struct {
	// T counts the person's decisions up to the point.
	T     int64     `json:"t"`
	At    time.Time `json:"at"`
	Value float64   `json:"value"`
}

// GetLearned reads what the decision service learned about the person
// params names. It refuses a caller that may not read the person's steps
// with an error matching latere.ErrForbidden.
func (c *Client) GetLearned(ctx context.Context, params *LearnedParams) (*Learned, error) {
	if params == nil || params.Org == "" || params.Subject == "" {
		return nil, errors.New("oversight: GetLearned needs the Org and the Subject")
	}
	q := url.Values{"org": {params.Org}, "subject": {params.Subject}}
	if params.Agent != "" {
		q.Set("agent", params.Agent)
	}
	if params.Kind != "" {
		q.Set("kind", params.Kind)
	}
	var out Learned
	if err := c.do(ctx, &latere.Request{Method: http.MethodGet, Path: BasePath + "/learned", Query: q}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ExplanationParams names the step GetExplanation explains.
type ExplanationParams struct {
	// Org names the step's context and Subject the person, as the
	// package documentation says; ActionID the step, as ActionID builds
	// it. All are required.
	Org, Subject, ActionID string
}

// Explanation says why the decision service suggested what it did for one
// step. Every number is exact for the decision service's model: the
// shares of the evidence and the parts add up to what it predicted.
type Explanation struct {
	Org      string `json:"org"`
	Subject  string `json:"subject"`
	ActionID string `json:"action_id"`
	// Model is the model that explains: the person's current one.
	// PredictedBy is the model that made the step's recorded prediction,
	// nil when there is none. When the two differ, the decision service
	// learned since, and the explanation is of what it suggests now.
	Model       ModelRef  `json:"model"`
	PredictedBy *ModelRef `json:"predicted_by,omitempty"`
	// Approve, Uncertainty and Verdict are what Model predicts for the
	// step, under Thresholds: those of the recorded prediction, or of the
	// profile that applies now.
	Approve     float64    `json:"approve"`
	Uncertainty float64    `json:"uncertainty"`
	Verdict     Verdict    `json:"verdict"`
	Thresholds  Thresholds `json:"thresholds"`
	// Mean is the model's value behind Approve.
	Mean float64 `json:"mean"`
	// Parts split Mean into what the model knows of steps like this one,
	// whenever they were, of the person's answers lately, about any step,
	// and of the two together. Nil for a model that has no parts.
	Parts *Parts `json:"parts,omitempty"`
	// EvidenceRatio is the share, from 0 to 1, of the model's uncertainty
	// about the step that the person's answers removed.
	EvidenceRatio float64 `json:"evidence_ratio"`
	// AskKind is why the decision service suggests asking, when Verdict is
	// VerdictAsk, and empty otherwise.
	AskKind AskKind `json:"ask_kind,omitempty"`
	// Reason is one sentence for the person saying why Verdict is what it
	// is, as a Prediction's Reason does; never show it to an agent. The
	// reason of an ask states no chance. Empty from a decision service
	// that does not send it.
	Reason string `json:"reason,omitempty"`
	// Evidence are the person's answers that moved the prediction most:
	// those toward the verdict's side first, then those against it.
	Evidence []Evidence `json:"evidence"`
	// Cover is how far the person's approvals cover the step, with the
	// nearest approvals whose Scope the step exceeds; nil from a decision
	// service that does not send it.
	Cover *Cover `json:"cover,omitempty"`
	// Calibration is the state of the bin of the recorded prediction's
	// Approve, not of the Approve explained now, in the person's
	// calibration report for the last CalibrationWindow. Empty for a step
	// with no recorded prediction, and from a decision service without the
	// report; a client then states no probability. The type Calibration
	// says when a client may show Approve.
	Calibration CalibrationState `json:"calibration,omitempty"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the explanation, with
// every field, including those this version of the package does not model.
func (e *Explanation) RawJSON() json.RawMessage { return e.raw }

// UnmarshalJSON decodes the explanation and keeps the JSON for RawJSON.
func (e *Explanation) UnmarshalJSON(b []byte) error {
	type plain Explanation
	if err := json.Unmarshal(b, (*plain)(e)); err != nil {
		return err
	}
	e.raw = keep(b)
	return nil
}

// Parts are the parts of an explained prediction. A positive part leans
// toward running the step, a negative one toward stopping it.
type Parts struct {
	// Action is what the model knows of steps like this one.
	Action float64 `json:"action"`
	// Recent is what the person's answers lately, about any step, add.
	Recent float64 `json:"recent"`
	// Interaction is what the two together add.
	Interaction float64 `json:"interaction"`
}

// Evidence is one of the person's answers that moved a prediction.
type Evidence struct {
	// ActionID is the answered step, as ActionID builds it.
	ActionID string `json:"action_id"`
	// Label names its kind of step, as LearnedKind.Label does.
	Label string    `json:"label"`
	At    time.Time `json:"at"`
	// Approve is whether the person allowed the step.
	Approve bool `json:"approve"`
	// Share is how much of the prediction the answer moved, from 0 to 1.
	Share float64 `json:"share"`
}

// GetExplanation explains the prediction for the step params names. It
// refuses a caller that may not read the person's steps with an error
// matching latere.ErrForbidden.
func (c *Client) GetExplanation(ctx context.Context, params *ExplanationParams) (*Explanation, error) {
	if params == nil || params.Org == "" || params.Subject == "" || params.ActionID == "" {
		return nil, errors.New("oversight: GetExplanation needs the Org, the Subject and the ActionID")
	}
	q := url.Values{"org": {params.Org}, "subject": {params.Subject}, "action_id": {params.ActionID}}
	var out Explanation
	if err := c.do(ctx, &latere.Request{Method: http.MethodGet, Path: BasePath + "/explanations", Query: q}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Costs are what a trust profile states, in any one unit: only their
// ratios matter. Each kind of error must cost more than two asks.
type Costs struct {
	// FalseAllow is the cost of running a step the person would deny.
	FalseAllow float64 `json:"false_allow"`
	// FalseBlock is the cost of stopping a step the person would allow.
	FalseBlock float64 `json:"false_block"`
	// Ask is the cost of asking the person once.
	Ask float64 `json:"ask"`
}

// Thresholds are what the decision service suggests at each chance: above
// AllowAbove it suggests running a step, below BlockBelow stopping it, and
// between them, both included, asking.
type Thresholds struct {
	AllowAbove float64 `json:"allow_above"`
	BlockBelow float64 `json:"block_below"`
}

// Certify is the claim the decision service's reliability is tested
// against: wrongly letting a step through less often than Alpha, with
// confidence 1 - Delta.
type Certify struct {
	Alpha float64 `json:"alpha"`
	Delta float64 `json:"delta"`
}

// Consent is what a trust profile sets for the cover rule: how much
// weight the person's approvals need, and from how many sessions, before
// the decision service suggests allow for a step. A profile can make the
// decision service's own settings greater and never smaller; zero is its
// own setting. A Cover names the settings that applied in MinWeight and
// MinSessions.
type Consent struct {
	MinWeight   int `json:"min_weight,omitempty"`
	MinSessions int `json:"min_sessions,omitempty"`
}

// Scope is what a trust profile applies to: an organization, which is a
// person's subject in their own context and the organization's id in an
// organization's, and optionally one kind of step, one agent, or both.
//
// A Scope with Subject is that person's own trust profile in the
// organization, for all their steps there: it has no Kind and no Agent,
// and in a person's own context Subject is Org. The decision service
// applies the stricter of the organization's profile and the person's own
// to each of the person's steps, number by number: the higher of the two
// allow thresholds, the lower of the two block thresholds, the higher
// audit rate, the stricter claim and the greater of each Consent setting.
// A person's own profile never makes the organization's looser.
//
// A Prediction names the scope whose profile applied to its step in the
// same form, with Subject set only where the person's own profile made a
// number stricter.
type Scope struct {
	Org   string `json:"org"`
	Kind  string `json:"kind,omitempty"`
	Agent string `json:"agent,omitempty"`
	// Subject names the person whose own profile the scope is, as Subject
	// builds it.
	Subject string `json:"subject,omitempty"`
}

// query is the scope as the profile routes read it.
func (s Scope) query() url.Values {
	q := url.Values{"org": {s.Org}}
	for name, v := range map[string]string{"kind": s.Kind, "agent": s.Agent, "subject": s.Subject} {
		if v != "" {
			q.Set(name, v)
		}
	}
	return q
}

// Profile is a trust profile: the costs a scope states and what follows
// from them.
type Profile struct {
	Org   string `json:"org"`
	Kind  string `json:"kind,omitempty"`
	Agent string `json:"agent,omitempty"`
	// Subject is set on a person's own profile in the organization, as
	// Scope says.
	Subject string `json:"subject,omitempty"`
	Costs   Costs  `json:"costs"`
	// AuditRate is the share, from 0 to 1, of the steps run on their own
	// that are picked at random for the person to check.
	AuditRate float64  `json:"audit_rate"`
	Certify   *Certify `json:"certify,omitempty"`
	// Consent makes the settings of the cover rule greater for the steps
	// of the scope; nil is the decision service's own settings. A profile
	// read with GetProfile and saved again with PutProfile keeps it.
	Consent *Consent `json:"consent,omitempty"`
	// Thresholds follow from Costs; a value set when saving is ignored.
	Thresholds Thresholds `json:"thresholds"`
	// Default is true for the built-in profile of a scope with none saved;
	// a value set when saving is ignored.
	Default bool `json:"default,omitempty"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the profile, with every
// field, including those this version of the package does not model.
func (p *Profile) RawJSON() json.RawMessage { return p.raw }

// UnmarshalJSON decodes the profile and keeps the JSON for RawJSON.
func (p *Profile) UnmarshalJSON(b []byte) error {
	type plain Profile
	if err := json.Unmarshal(b, (*plain)(p)); err != nil {
		return err
	}
	p.raw = keep(b)
	return nil
}

// GetProfile reads the trust profile that applies to scope: the most
// specific one saved, or the built-in default, which says so in Default.
// The Kind and the Agent of the profile it returns say which scope that
// is: a profile with the Agent of scope is the agent's own, and one
// without it is the organization's.
//
// With Subject, GetProfile reads the person's own profile as they saved
// it, with Subject set, and the organization's profile, with no Subject,
// when they saved none. It never returns the stricter of the two that the
// decision service applies; a program reads both and compares their
// thresholds.
//
// It refuses a caller that may not read the scope's profiles with an
// error matching latere.ErrForbidden.
func (c *Client) GetProfile(ctx context.Context, scope Scope) (*Profile, error) {
	if scope.Org == "" {
		return nil, errors.New("oversight: GetProfile needs the Org")
	}
	var out Profile
	if err := c.do(ctx, &latere.Request{Method: http.MethodGet, Path: BasePath + "/profiles", Query: scope.query()}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteProfile removes the trust profile saved for exactly scope, so the
// next one applies: an agent's scope goes back to its organization's
// profile, a person's own goes back to the organization's, and an
// organization's goes back to the built-in default. It changes no step
// already suggested about. Removing a profile that is not saved is no
// error, so it is retried like a read. It refuses a caller that may not
// save the scope's profiles with an error matching latere.ErrForbidden.
func (c *Client) DeleteProfile(ctx context.Context, scope Scope) error {
	if scope.Org == "" {
		return errors.New("oversight: DeleteProfile needs the Org")
	}
	return c.do(ctx, &latere.Request{Method: http.MethodDelete, Path: BasePath + "/profiles", Query: scope.query()}, nil)
}

// PutProfile saves profile for its scope and returns it as saved, with its
// thresholds. A profile with Subject is that person's own profile in the
// organization, as Scope says. Costs under which the decision service
// would decide a step of a person it knows nothing about, each kind of
// error costing two asks or less, are refused with an error matching
// ErrAskTooCostly. It sets values only, so it is retried like a read.
func (c *Client) PutProfile(ctx context.Context, profile *Profile) (*Profile, error) {
	if profile == nil || profile.Org == "" {
		return nil, errors.New("oversight: PutProfile needs a profile with its Org")
	}
	var out Profile
	if err := c.do(ctx, &latere.Request{Method: http.MethodPut, Path: BasePath + "/profiles", Body: profile, Retry: latere.RetryAlways}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PreviewParams name the person, the window and the trust profile Preview
// counts.
type PreviewParams struct {
	// Org and Subject name the person. Both are required.
	Org     string `json:"org"`
	Subject string `json:"subject"`
	// From and To bound the window of the person's steps, From included.
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
	// Costs and AuditRate are the profile to count under.
	Costs     Costs   `json:"costs"`
	AuditRate float64 `json:"audit_rate"`
}

// Preview is what a trust profile would have done with the person's steps
// in a window, from what the decision service predicted for each as it
// was made.
type Preview struct {
	// Org, Subject, From, To, Costs and AuditRate are those of the
	// request.
	Org       string    `json:"org"`
	Subject   string    `json:"subject"`
	From      time.Time `json:"from"`
	To        time.Time `json:"to"`
	Costs     Costs     `json:"costs"`
	AuditRate float64   `json:"audit_rate"`
	// Thresholds are those of the previewed costs.
	Thresholds Thresholds `json:"thresholds"`
	// Records counts the steps with a prediction in the window, and Would
	// counts them by the verdict the profile suggests.
	Records int             `json:"records"`
	Would   map[Verdict]int `json:"would"`
	// Answered counts the steps the person answered. AllowedDenied counts
	// those the profile would have run that the person denied, and
	// BlockedApproved those it would have stopped that the person allowed.
	Answered        int `json:"answered"`
	AllowedDenied   int `json:"allowed_denied"`
	BlockedApproved int `json:"blocked_approved"`
	// ExpectedAsks is how many times it would have asked, with the steps
	// picked at random to check, as an expected count.
	ExpectedAsks float64 `json:"expected_asks"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the preview, with every
// field, including those this version of the package does not model.
func (p *Preview) RawJSON() json.RawMessage { return p.raw }

// UnmarshalJSON decodes the preview and keeps the JSON for RawJSON.
func (p *Preview) UnmarshalJSON(b []byte) error {
	type plain Preview
	if err := json.Unmarshal(b, (*plain)(p)); err != nil {
		return err
	}
	p.raw = keep(b)
	return nil
}

// Preview counts what the trust profile params names would have done with
// the person's steps in the window. It changes nothing, so it is retried
// like a read. Costs that ask too little are refused with an error
// matching ErrAskTooCostly.
func (c *Client) Preview(ctx context.Context, params *PreviewParams) (*Preview, error) {
	if params == nil || params.Org == "" || params.Subject == "" {
		return nil, errors.New("oversight: Preview needs the Org and the Subject")
	}
	var out Preview
	if err := c.do(ctx, &latere.Request{Method: http.MethodPost, Path: BasePath + "/previews", Body: params, Retry: latere.RetryAlways}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AnswerParams are the person's answer about one step.
type AnswerParams struct {
	// Org, Subject and ActionID name the step: its context, the person,
	// and the step, as the package documentation says. All are required.
	Org      string `json:"org"`
	Subject  string `json:"subject"`
	ActionID string `json:"action_id"`
	// Approve is whether the person allows the step, or, for a step that
	// already ran, would have.
	Approve bool `json:"approve"`
	// By names who answered, when the caller relays the answer of someone
	// other than itself.
	By string `json:"by,omitempty"`
	// AnswerMS, WaitedMS, DetailsOpened, SuggestionShown, Surface and
	// Remembered say how the person answered, so the decision service can
	// weigh the answer. Set each one the caller measured and leave out each
	// one it did not; never guess. None holds text.
	//
	// AnswerMS is the milliseconds from the moment the question was visible
	// to the person to the answer, from 0 to MaxAnswerMS, as the client
	// measured it.
	AnswerMS *int64 `json:"answer_ms,omitempty"`
	// WaitedMS is the milliseconds from the decision that showed the step
	// to the answer, as the caller that recorded the decision measured it.
	WaitedMS *int64 `json:"waited_ms,omitempty"`
	// DetailsOpened is whether the person opened the step's details, such
	// as the full input, the change and the target, before answering; nil
	// where the client offered none.
	DetailsOpened *bool `json:"details_opened,omitempty"`
	// SuggestionShown is whether the client showed any part of the
	// suggestion, such as its verdict, the chance, the reason or an
	// explanation, before the person answered.
	SuggestionShown *bool `json:"suggestion_shown,omitempty"`
	// Surface is where the person answered.
	Surface Surface `json:"surface,omitempty"`
	// Remembered is true when the answer also made a standing rule for
	// later steps. The rule itself is never sent.
	Remembered bool `json:"remembered,omitempty"`
}

// MaxAnswerMS is the largest AnswerParams.AnswerMS: one day.
const MaxAnswerMS = 86_400_000

// Surface is where a person answered about a step.
type Surface string

// The surfaces of an answer.
const (
	// SurfacePrompt is a question in the conversation while the step
	// waits.
	SurfacePrompt Surface = "prompt"
	// SurfaceNotification is a system notification.
	SurfaceNotification Surface = "notification"
	// SurfaceQueue is a list of waiting questions or of reviews.
	SurfaceQueue Surface = "queue"
	// SurfaceBulk is one gesture that answers more than one step.
	SurfaceBulk Surface = "bulk"
	// SurfaceRule is a standing rule, not the person at that time. A
	// caller sends a rule's decision as a decision, not as an answer; an
	// answer it sends for one has no weight.
	SurfaceRule Surface = "rule"
)

// Answer tells the decision service the person's answer about a step whose
// decision is recorded; a step with none is refused with an error matching
// ErrNoDecision. The first answer to a step the person was shown counts
// toward how often the decision service is wrong; every answer teaches it.
//
// Answer is sent once and never retried: each answer it receives counts,
// so a repeat after a failure the answer survived would count twice.
func (c *Client) Answer(ctx context.Context, params *AnswerParams) error {
	if params == nil || params.Org == "" || params.Subject == "" || params.ActionID == "" {
		return errors.New("oversight: Answer needs the Org, the Subject and the ActionID")
	}
	if ms := params.AnswerMS; ms != nil && (*ms < 0 || *ms > MaxAnswerMS) {
		return fmt.Errorf("oversight: AnswerParams.AnswerMS is %d, not from 0 to %d", *ms, MaxAnswerMS)
	}
	if ms := params.WaitedMS; ms != nil && *ms < 0 {
		return fmt.Errorf("oversight: AnswerParams.WaitedMS is %d, below 0", *ms)
	}
	return c.do(ctx, &latere.Request{Method: http.MethodPost, Path: BasePath + "/answers", Body: params, Retry: latere.RetryNever}, nil)
}

// ForgetParams name the person whose data Forget removes.
type ForgetParams struct {
	Org     string `json:"org"`
	Subject string `json:"subject"`
}

// Forget removes everything the decision service holds about the person in
// the organization: their steps, their answers, what it learned from them,
// and their own trust profile there. The weeks of the organization's
// report that were already read stay as they were: they hold no record of
// one person. Forgetting twice is forgetting once, so it is retried like a
// read.
func (c *Client) Forget(ctx context.Context, params *ForgetParams) error {
	if params == nil || params.Org == "" || params.Subject == "" {
		return errors.New("oversight: Forget needs the Org and the Subject")
	}
	return c.do(ctx, &latere.Request{Method: http.MethodPost, Path: BasePath + "/subjects/forget", Body: params, Retry: latere.RetryAlways}, nil)
}

// keep copies b, which the decoder may reuse.
func keep(b []byte) json.RawMessage {
	return append(json.RawMessage(nil), b...)
}
