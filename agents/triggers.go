// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents

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

// A trigger starts sessions of an agent on its own: on a schedule, or when
// an event arrives from a connected product. Each time it fires is a
// firing, recorded with what came of it. Triggers are written as
// manifests, like agents, and applied by name: applying a manifest whose
// name exists replaces that trigger's spec, so a program can keep its
// triggers in files and apply them on every deploy.

// TriggerAPIVersion and TriggerKind are a trigger manifest's apiVersion
// and kind. Client.ApplyTrigger fills them in when a Trigger leaves them
// empty.
const (
	TriggerAPIVersion = "topos.latere.ai/v1"
	TriggerKind       = "Trigger"
)

// A Trigger is a trigger manifest: what fires it, the session each firing
// starts or continues, and, as the API answers it, its firing record.
type Trigger struct {
	// APIVersion is the manifest's version, TriggerAPIVersion.
	APIVersion string `json:"apiVersion"`
	// Kind is TriggerKind.
	Kind string `json:"kind"`
	// Metadata names the trigger.
	Metadata TriggerMetadata `json:"metadata"`
	// Spec is what the trigger fires on and what each firing does.
	Spec TriggerSpec `json:"spec"`
	// Status is the API's record of the trigger, written by the API and
	// ignored when a trigger is applied.
	Status TriggerStatus `json:"status,omitzero"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the trigger, with every
// field, including those this package does not model; nil for a Trigger the
// program built itself.
func (t *Trigger) RawJSON() json.RawMessage { return t.raw }

// UnmarshalJSON decodes the trigger and keeps the JSON for RawJSON.
func (t *Trigger) UnmarshalJSON(b []byte) error {
	type plain Trigger
	if err := json.Unmarshal(b, (*plain)(t)); err != nil {
		return err
	}
	t.raw = keep(b)
	return nil
}

// TriggerMetadata names a trigger. Apart from Name, none of it is part of
// the spec, so changing it changes no version.
type TriggerMetadata struct {
	// Name identifies the trigger among its owner's: lowercase letters,
	// digits and hyphens, at most 63 characters.
	Name string `json:"name"`
	// DisplayName is the name a person reads; empty shows Name.
	DisplayName string `json:"displayName,omitempty"`
	// Labels are the caller's own key-value pairs.
	Labels map[string]string `json:"labels,omitempty"`
	// Annotations are the caller's own notes, not meant for filtering.
	Annotations map[string]string `json:"annotations,omitempty"`
}

// TriggerSpec is what a trigger fires on and the session each firing
// starts or continues. A trigger sets Schedule or On, never both.
type TriggerSpec struct {
	// Agent is the agent the sessions run, by name or id. The API answers
	// the agent's id, and a trigger always runs the agent's latest version.
	Agent string `json:"agent"`
	// Schedule is a five-field cron expression, or @hourly, @daily or
	// @weekly.
	Schedule string `json:"schedule,omitempty"`
	// TimeZone is the IANA zone Schedule is read in; UTC when empty.
	TimeZone string `json:"timeZone,omitempty"`
	// On selects the events the trigger fires on.
	On *EventFilter `json:"on,omitempty"`
	// Session is the session a firing starts, or the message it sends to
	// the open session its key names.
	Session TriggerSession `json:"session"`
	// SkipIfActive skips a firing while a session the trigger started is
	// still working; the API's default when nil.
	SkipIfActive *bool `json:"skipIfActive,omitempty"`
	// MaxActive bounds how many of the trigger's sessions work at once;
	// the API's default when nil.
	MaxActive *int `json:"maxActive,omitempty"`
	// MaxAge is how late a scheduled firing may still run, as a Go
	// duration such as "1h"; a later one is skipped.
	MaxAge string `json:"maxAge,omitempty"`
	// Suspend stops the trigger from firing until it is applied without.
	Suspend bool `json:"suspend,omitempty"`
}

// An EventFilter selects events: every field given must match, and a list
// matches when any of its entries does. An entry of Verbs or Resources is
// exact, or ends in * and matches every value it is a prefix of.
type EventFilter struct {
	// Product is the product the events come from, such as "github".
	Product string `json:"product"`
	// Verbs are the kinds of event, such as "release.published".
	Verbs []string `json:"verbs"`
	// Resources are the things the events are about, such as a repository.
	Resources []string `json:"resources,omitempty"`
	// Match holds conditions on the event's payload.
	Match []PayloadMatch `json:"match,omitempty"`
}

// A PayloadMatch matches the value at Path, a dotted path into the event
// such as "payload.action", against the entries of In.
type PayloadMatch struct {
	// Path is where in the event the value is.
	Path string `json:"path"`
	// In lists the values that match.
	In []string `json:"in"`
}

// TriggerSession is the session a firing starts or continues. Message,
// Title, Key and a repository's URL and Ref are templates: {{event.resource}}
// and the like are filled in from the event, and \{{ writes a literal {{.
// A field left out is the agent's own.
type TriggerSession struct {
	// Message is the first message of a new session, or the message sent
	// to a continued one.
	Message string `json:"message"`
	// Title names a new session for a person.
	Title string `json:"title,omitempty"`
	// Policy is whether a firing starts a new session or continues the
	// open one its Key names; SessionPolicyNew when empty.
	Policy SessionPolicy `json:"policy,omitempty"`
	// Key names a firing's session, for SessionPolicyContinue: empty on a
	// schedule, and the event's resource on an event, when left out.
	Key string `json:"key,omitempty"`
	// Machine is where the session's commands run; the agent's when nil.
	Machine *Machine `json:"machine,omitempty"`
	// Resources are what the session attaches: memory stores and git
	// repositories.
	Resources []SessionResource `json:"resources,omitempty"`
	// Budget bounds what the session may spend.
	Budget *TriggerBudget `json:"budget,omitempty"`
	// Limits bound how long the session and each turn may take.
	Limits *SessionLimits `json:"limits,omitempty"`
	// EndOnIdle ends the session once it stops working instead of leaving
	// it open; the API's default when nil.
	EndOnIdle *bool `json:"endOnIdle,omitempty"`
}

// SessionPolicy is whether a firing starts a session or continues one.
type SessionPolicy string

// The session policies.
const (
	// SessionPolicyNew starts a new session for every firing.
	SessionPolicyNew SessionPolicy = "new"
	// SessionPolicyContinue sends the message to the open session the
	// firing's key names, and starts one when there is none.
	SessionPolicyContinue SessionPolicy = "continue"
)

// Access is how a session may use a memory store.
type Access string

// The ways a session may use a memory store.
const (
	// AccessReadWrite reads and writes the store.
	AccessReadWrite Access = "readWrite"
	// AccessReadOnly reads it alone.
	AccessReadOnly Access = "readOnly"
)

// A SessionResource is one thing a triggered session attaches.
type SessionResource struct {
	// Type is what is attached.
	Type ResourceType `json:"type"`
	// MemoryStore names a memory store, by name or id, for
	// ResourceMemoryStore.
	MemoryStore string `json:"memoryStore,omitempty"`
	// Access is how the session may use a memory store; the store's own
	// setting when empty.
	Access Access `json:"access,omitempty"`
	// URL is a git repository's address, for ResourceRepository.
	URL string `json:"url,omitempty"`
	// Ref is the branch, tag or commit of the repository to check out.
	Ref string `json:"ref,omitempty"`
}

// ResourceType is the kind of a session's resource.
type ResourceType string

// The kinds of resource.
const (
	// ResourceMemoryStore is a memory store of files.
	ResourceMemoryStore ResourceType = "memoryStore"
	// ResourceRepository is a git repository.
	ResourceRepository ResourceType = "repository"
)

// TriggerBudget bounds what a triggered session may spend.
type TriggerBudget struct {
	// MaxCost is the most it may spend, in US dollars, as a decimal string
	// such as "2.50".
	MaxCost string `json:"maxCost,omitempty"`
}

// SessionLimits bound how long a triggered session may take, as Go
// durations such as "10m". An empty field is the agent's.
type SessionLimits struct {
	// TurnTimeout is how long one turn may take.
	TurnTimeout string `json:"turnTimeout,omitempty"`
	// MaxAge is how long the session may stay open.
	MaxAge string `json:"maxAge,omitempty"`
}

// TriggerStatus is the API's record of a trigger.
type TriggerStatus struct {
	// ID is the trigger's id, "trg_" followed by a ULID.
	ID string `json:"id,omitempty"`
	// Version counts the distinct specs the trigger has had.
	Version int `json:"version,omitempty"`
	// Digest identifies the current spec.
	Digest string `json:"digest,omitempty"`
	// CreatedAt is when the trigger was first applied.
	CreatedAt time.Time `json:"createdAt,omitzero"`
	// ArchivedAt is when the trigger was archived; nil when it was not.
	ArchivedAt *time.Time `json:"archivedAt,omitempty"`
	// LastFiredAt is when the trigger last started or continued a session.
	LastFiredAt *time.Time `json:"lastFiredAt,omitempty"`
	// LastSessionID is the session it last started or continued.
	LastSessionID string `json:"lastSessionId,omitempty"`
	// NextFireAt is when a schedule fires next.
	NextFireAt *time.Time `json:"nextFireAt,omitempty"`
	// Counts is how many firings came to each outcome.
	Counts *FiringCounts `json:"counts,omitempty"`
}

// FiringCounts counts a trigger's firings by outcome.
type FiringCounts struct {
	// Started firings started a session.
	Started int64 `json:"started"`
	// Continued firings sent their message to an open session.
	Continued int64 `json:"continued"`
	// Held firings wait for a session to become free.
	Held int64 `json:"held"`
	// Filtered events did not match the trigger's filter.
	Filtered int64 `json:"filtered"`
	// SkippedActive firings came while a session was still working.
	SkippedActive int64 `json:"skippedActive"`
	// SkippedBusy firings came while the trigger had MaxActive sessions.
	SkippedBusy int64 `json:"skippedBusy"`
	// SkippedLate firings came later than MaxAge allows.
	SkippedLate int64 `json:"skippedLate"`
	// Refused firings were refused, such as by the agent's owner's policy.
	Refused int64 `json:"refused"`
	// Failed firings failed to start or continue a session.
	Failed int64 `json:"failed"`
}

// A Firing is one time a trigger fired, and what came of it.
type Firing struct {
	// ID is the firing's id, "frg_" followed by a ULID; empty for an event
	// the filter left out, which is not recorded.
	ID string `json:"id,omitempty"`
	// TriggerID is the trigger that fired.
	TriggerID string `json:"trigger_id"`
	// Origin is what fired it.
	Origin FiringOrigin `json:"origin"`
	// Event is the event that fired it; nil for a schedule or a manual
	// firing.
	Event *FiringEvent `json:"event,omitempty"`
	// Key names the session the firing goes to.
	Key string `json:"key"`
	// Outcome is what came of the firing; empty when it was not recorded.
	Outcome FiringOutcome `json:"outcome,omitempty"`
	// Reason explains a refused or failed firing, for a person.
	Reason string `json:"reason,omitempty"`
	// SessionID is the session the firing started or continued.
	SessionID string `json:"session_id,omitempty"`
	// ReceivedAt is when the firing arrived.
	ReceivedAt time.Time `json:"received_at"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the firing, with every
// field, including those this package does not model; nil for a Firing the
// program built itself.
func (f *Firing) RawJSON() json.RawMessage { return f.raw }

// UnmarshalJSON decodes the firing and keeps the JSON for RawJSON.
func (f *Firing) UnmarshalJSON(b []byte) error {
	type plain Firing
	if err := json.Unmarshal(b, (*plain)(f)); err != nil {
		return err
	}
	f.raw = keep(b)
	return nil
}

// FiringEvent identifies the event a firing came from.
type FiringEvent struct {
	// ID is the event's id, unique per product.
	ID string `json:"id"`
	// Product is the product it came from.
	Product string `json:"product"`
	// Verb is its kind.
	Verb string `json:"verb"`
	// Resource is what it is about.
	Resource string `json:"resource"`
}

// FiringOrigin is what fired a trigger.
type FiringOrigin string

// The origins of a firing.
const (
	// OriginSchedule is the trigger's schedule.
	OriginSchedule FiringOrigin = "schedule"
	// OriginManual is a call to Client.FireTrigger without an event.
	OriginManual FiringOrigin = "manual"
	// OriginEvent is an event that arrived.
	OriginEvent FiringOrigin = "event"
)

// FiringOutcome is what came of a firing.
type FiringOutcome string

// The outcomes of a firing.
const (
	// FiringStarted started a session.
	FiringStarted FiringOutcome = "started"
	// FiringContinued sent its message to an open session.
	FiringContinued FiringOutcome = "continued"
	// FiringHeld waits for a session to become free.
	FiringHeld FiringOutcome = "held"
	// FiringFiltered is an event the trigger's filter left out; it is
	// counted, and a list of firings does not hold it.
	FiringFiltered FiringOutcome = "filtered"
	// FiringSkippedActive came while the trigger's session was still
	// working.
	FiringSkippedActive FiringOutcome = "skipped_active"
	// FiringSkippedBusy came while the trigger had MaxActive sessions.
	FiringSkippedBusy FiringOutcome = "skipped_busy"
	// FiringSkippedLate came later than MaxAge allows.
	FiringSkippedLate FiringOutcome = "skipped_late"
	// FiringRefused was refused, such as by the agent's owner's policy;
	// Firing.Reason says why.
	FiringRefused FiringOutcome = "refused"
	// FiringFailed failed to start or continue a session; Firing.Reason
	// says why.
	FiringFailed FiringOutcome = "failed"
)

// ListTriggersParams narrows and pages a list of triggers. The zero value
// lists the caller's own.
type ListTriggersParams struct {
	// Owner lists the triggers of another owner the caller may read, such
	// as an organization's; the caller's own when empty.
	Owner string
	// Limit is the most triggers a page holds; the API's default when 0.
	Limit int
	// Cursor starts ListTriggersPage at the page a previous one's
	// NextCursor named; ListTriggers ignores it.
	Cursor string
}

// ListTriggers returns the triggers params keeps, newest first, fetching
// pages as the loop asks for more.
func (c *Client) ListTriggers(ctx context.Context, params *ListTriggersParams) iter.Seq2[*Trigger, error] {
	return latere.Items(ctx, func(ctx context.Context, cursor string) (*latere.Page[*Trigger], error) {
		return c.triggersPage(ctx, params, cursor)
	})
}

// ListTriggersPage returns one page of the triggers params keeps, starting
// at params.Cursor, and the cursor of the next page in NextCursor.
func (c *Client) ListTriggersPage(ctx context.Context, params *ListTriggersParams) (*latere.Page[*Trigger], error) {
	cursor := ""
	if params != nil {
		cursor = params.Cursor
	}
	return c.triggersPage(ctx, params, cursor)
}

func (c *Client) triggersPage(ctx context.Context, params *ListTriggersParams, cursor string) (*latere.Page[*Trigger], error) {
	q := url.Values{}
	if params != nil {
		if params.Owner != "" {
			q.Set("owner", params.Owner)
		}
		if params.Limit > 0 {
			q.Set("limit", strconv.Itoa(params.Limit))
		}
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	var page latere.Page[*Trigger]
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodGet, Path: basePath + "/triggers", Query: q}, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// errNoTrigger is returned before any request when a trigger's id or name
// is empty.
var errNoTrigger = errors.New("agents: the trigger's id or name is empty")

// triggerPath is the path of a trigger, by id or name, or of one of its
// sub-resources.
func triggerPath(ref, rest string) string {
	return basePath + "/triggers/" + url.PathEscape(ref) + rest
}

// GetTrigger returns the trigger ref names, by its id or its name. It
// returns an error matching latere.ErrNotFound when there is no such
// trigger the caller may read.
func (c *Client) GetTrigger(ctx context.Context, ref string) (*Trigger, error) {
	if ref == "" {
		return nil, errNoTrigger
	}
	var out Trigger
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodGet, Path: triggerPath(ref, "")}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ApplyTrigger creates the trigger t describes, or replaces the spec and
// metadata of the trigger of the same name, and returns it as stored. An
// empty APIVersion or Kind is filled in, and t.Status is not sent. Applying
// the same manifest twice changes nothing, so the call is safe to retry.
//
// Applying replaces the whole spec. A program that reads a trigger with
// GetTrigger, changes it and applies it drops any field this version of
// the SDK does not model, such as one the API added later; send the JSON
// Trigger.RawJSON holds, changed, through latere.Config.Do to keep them.
func (c *Client) ApplyTrigger(ctx context.Context, t *Trigger) (*Trigger, error) {
	if t == nil || t.Metadata.Name == "" {
		return nil, errors.New("agents: the trigger's Metadata.Name is required")
	}
	manifest := *t
	if manifest.APIVersion == "" {
		manifest.APIVersion = TriggerAPIVersion
	}
	if manifest.Kind == "" {
		manifest.Kind = TriggerKind
	}
	manifest.Status = TriggerStatus{}
	var out Trigger
	req := &latere.Request{Method: http.MethodPut, Path: triggerPath(t.Metadata.Name, ""), Body: manifest}
	if err := c.cfg.Do(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteTrigger deletes the trigger ref names, by its id or its name. The
// sessions it started are kept.
func (c *Client) DeleteTrigger(ctx context.Context, ref string) error {
	if ref == "" {
		return errNoTrigger
	}
	return c.cfg.Do(ctx, &latere.Request{Method: http.MethodDelete, Path: triggerPath(ref, "")}, nil)
}

// FireTriggerParams is a firing a program asks for.
type FireTriggerParams struct {
	// Event is the event an event trigger fires on. Nil fires a schedule
	// trigger now.
	Event *TriggerEvent
	// IdempotencyKey makes a repeated call answer the first firing instead
	// of firing again; one is generated when it is empty. A redelivered
	// event is also recognized by its ID.
	IdempotencyKey string
}

// A TriggerEvent is an event delivered to an event trigger.
type TriggerEvent struct {
	// ID identifies the event within its product; a second delivery with
	// the same ID is answered with the first one's firing.
	ID string `json:"id"`
	// Product is the product it comes from.
	Product string `json:"product"`
	// Verb is its kind, such as "release.published".
	Verb string `json:"verb"`
	// Resource is what it is about, such as "example/widgets@v1.4.0".
	Resource string `json:"resource"`
	// Actor is who caused it at the product.
	Actor string `json:"actor,omitempty"`
	// Subject names the person the event is about, as an issuer-qualified
	// subject, when the product gives one; the trigger's filter and
	// templates read it as event.subject.
	Subject string `json:"subject,omitempty"`
	// Time is when it happened.
	Time time.Time `json:"time,omitzero"`
	// Payload is the event's own data, which the trigger's filter and
	// templates read under "payload".
	Payload json.RawMessage `json:"payload,omitempty"`
}

// FireTrigger fires the trigger ref names, by its id or its name, and
// returns the firing. An event trigger needs params.Event; a schedule
// trigger fires now without one. A firing whose session failed to start
// returns both the firing, whose Outcome and Reason say what went wrong,
// and an *latere.Error with status 503; firing again runs it again.
//
// FireTrigger is sent once, never again after a failure: the API forgets
// a firing's idempotency key when it answers with a server error, so a
// repeat would fire the trigger again. Pass the same IdempotencyKey to
// fire again yourself; an answered firing then returns its first answer.
func (c *Client) FireTrigger(ctx context.Context, ref string, params *FireTriggerParams) (*Firing, error) {
	if ref == "" {
		return nil, errNoTrigger
	}
	req := &latere.Request{Method: http.MethodPost, Path: triggerPath(ref, "/fire")}
	if params != nil {
		req.IdempotencyKey = params.IdempotencyKey
		if params.Event != nil {
			req.Body = params.Event
		}
	}
	if req.IdempotencyKey == "" {
		req.IdempotencyKey = latere.NewIdempotencyKey()
	}
	req.Retry = latere.RetryNever
	var out Firing
	if err := c.cfg.Do(ctx, req, &out); err != nil {
		var apiErr *latere.Error
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusServiceUnavailable {
			// The API answers a firing that failed to start its session with
			// the firing itself, so the caller sees why.
			var failed Firing
			if json.Unmarshal(apiErr.Body, &failed) == nil && failed.TriggerID != "" {
				return &failed, err
			}
		}
		return nil, err
	}
	return &out, nil
}

// ListFiringsParams pages a trigger's firings.
type ListFiringsParams struct {
	// Limit is the most firings a page holds; the API's default when 0.
	Limit int
	// Cursor starts ListFiringsPage at the page a previous one's
	// NextCursor named; ListFirings ignores it.
	Cursor string
}

// ListFirings returns the firings of the trigger ref names, newest first,
// fetching pages as the loop asks for more.
func (c *Client) ListFirings(ctx context.Context, ref string, params *ListFiringsParams) iter.Seq2[*Firing, error] {
	return latere.Items(ctx, func(ctx context.Context, cursor string) (*latere.Page[*Firing], error) {
		return c.firingsPage(ctx, ref, params, cursor)
	})
}

// ListFiringsPage returns one page of the firings of the trigger ref names,
// starting at params.Cursor, and the cursor of the next page in NextCursor.
func (c *Client) ListFiringsPage(ctx context.Context, ref string, params *ListFiringsParams) (*latere.Page[*Firing], error) {
	cursor := ""
	if params != nil {
		cursor = params.Cursor
	}
	return c.firingsPage(ctx, ref, params, cursor)
}

func (c *Client) firingsPage(ctx context.Context, ref string, params *ListFiringsParams, cursor string) (*latere.Page[*Firing], error) {
	if ref == "" {
		return nil, errNoTrigger
	}
	q := url.Values{}
	if params != nil && params.Limit > 0 {
		q.Set("limit", strconv.Itoa(params.Limit))
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	var page latere.Page[*Firing]
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodGet, Path: triggerPath(ref, "/firings"), Query: q}, &page); err != nil {
		return nil, err
	}
	return &page, nil
}
