// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package account

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"latere.ai/x/sdk-go/latere"
)

// ErrMailNotEnabled matches a call on a trigger's email address on an
// installation that receives no email, so no address can start a
// trigger. The API answers it with 404, so latere.ErrNotFound matches it
// too; TriggerInbound.Mail says beforehand whether email is on.
var ErrMailNotEnabled = errors.New("account: this installation receives no email, so a trigger has no address")

// addressCodes are the API's codes for the refusals of the address calls
// that have a sentinel.
var addressCodes = map[string]error{
	"mail_not_enabled": ErrMailNotEnabled,
}

// TriggerInbound is whether a trigger can be started by email, and the
// caller's triggers that have a link or an address, as
// ListTriggerInbound answers them.
type TriggerInbound struct {
	// Mail is whether the installation receives email.
	Mail InboundMail `json:"mail"`
	// Items are the caller's links and addresses, oldest first.
	Items []TriggerInlet `json:"items"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the links and addresses,
// with every field, including those this package does not model; nil for
// a TriggerInbound the program built itself.
func (t *TriggerInbound) RawJSON() json.RawMessage { return t.raw }

// UnmarshalJSON decodes the links and addresses and keeps the JSON for
// RawJSON.
func (t *TriggerInbound) UnmarshalJSON(b []byte) error {
	type plain TriggerInbound
	if err := json.Unmarshal(b, (*plain)(t)); err != nil {
		return err
	}
	t.raw = keep(b)
	return nil
}

// InboundMail is whether an installation receives email for triggers.
type InboundMail struct {
	// Enabled reports that a trigger can be given an email address.
	Enabled bool `json:"enabled"`
	// Domain is the domain addresses are on, such as "in.latere.ai";
	// empty while email is off.
	Domain string `json:"domain"`
}

// A TriggerInlet is a trigger's way in from outside: a link another app
// posts to, or an email address mail is sent or forwarded to.
type TriggerInlet struct {
	// TriggerID is the trigger's id, as agents.Trigger's Status.ID and
	// agents.Routine's TriggerID name it.
	TriggerID string `json:"trigger_id"`
	// Kind is whether it is a link or an address.
	Kind InletKind `json:"kind"`
	// Address is the email address of an InletMail; empty for a link,
	// whose secret is never listed, only answered by IssueTriggerHook.
	Address string `json:"address,omitempty"`
	// IssuedAt is when the link or the address was issued.
	IssuedAt time.Time `json:"issued_at"`
}

// InletKind says what a TriggerInlet is.
type InletKind string

// The kinds of a TriggerInlet.
const (
	// InletWebhook is a link, which starts the trigger on events of
	// product "webhook" and verb "received".
	InletWebhook InletKind = "webhook"
	// InletMail is an email address, which starts the trigger on events of
	// product "mail" and verb "received".
	InletMail InletKind = "mail"
)

// A TriggerHook is the link IssueTriggerHook gave a trigger.
type TriggerHook struct {
	// TriggerID is the trigger's id.
	TriggerID string `json:"trigger_id"`
	// URL is the link. It carries its own secret, of which the platform
	// keeps only a hash, so it is answered this once: keep it as a
	// credential.
	URL string `json:"url"`
	// IssuedAt is when it was issued.
	IssuedAt time.Time `json:"issued_at"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the link, with every
// field, including those this package does not model; nil for a
// TriggerHook the program built itself.
func (h *TriggerHook) RawJSON() json.RawMessage { return h.raw }

// UnmarshalJSON decodes the link and keeps the JSON for RawJSON.
func (h *TriggerHook) UnmarshalJSON(b []byte) error {
	type plain TriggerHook
	if err := json.Unmarshal(b, (*plain)(h)); err != nil {
		return err
	}
	h.raw = keep(b)
	return nil
}

// A TriggerAddress is the email address IssueTriggerAddress gave a
// trigger.
type TriggerAddress struct {
	// TriggerID is the trigger's id.
	TriggerID string `json:"trigger_id"`
	// Address is the email address.
	Address string `json:"address"`
	// IssuedAt is when it was issued.
	IssuedAt time.Time `json:"issued_at"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the address, with every
// field, including those this package does not model; nil for a
// TriggerAddress the program built itself.
func (a *TriggerAddress) RawJSON() json.RawMessage { return a.raw }

// UnmarshalJSON decodes the address and keeps the JSON for RawJSON.
func (a *TriggerAddress) UnmarshalJSON(b []byte) error {
	type plain TriggerAddress
	if err := json.Unmarshal(b, (*plain)(a)); err != nil {
		return err
	}
	a.raw = keep(b)
	return nil
}

// errNoTrigger is returned before any request when a trigger id is empty.
var errNoTrigger = errors.New("account: the trigger id is empty")

// inletPath is the path of a trigger's link or address.
func inletPath(triggerID, kind string) string {
	return "/v1/account/triggers/" + url.PathEscape(triggerID) + "/" + kind
}

// ListTriggerInbound returns whether the installation receives email, and
// the caller's triggers that have a link or an address. A link is listed
// without its secret; an address is listed whole, since a person forwards
// to it again. A key's token lists the triggers its grants cover
// "topos:trigger.update" on.
//
// Triggers are applied in the personal context alone: a token that names
// an organization's is refused with 403, whose details' "reason" is
// "organization_triggers_unavailable".
func (c *Client) ListTriggerInbound(ctx context.Context) (*TriggerInbound, error) {
	var out TriggerInbound
	if err := c.get(ctx, "/v1/account/triggers/inbound", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// IssueTriggerHook gives the caller's trigger, by its id, its own link,
// replacing the one it had, which stops working, and returns the link,
// which is answered this once. A POST to the link, signed out, starts the
// trigger with what was sent as the event's payload; the trigger fires on
// events of product "webhook" and verb "received", as an
// agents.StartsFromLink routine does.
//
// A failure is sent again, though the call is a POST without an
// idempotency key: a repeat replaces the link the first one issued, so
// the link answered is the trigger's one link that works, as a lost
// answer would have left it. A trigger the caller does not hold, or one
// that fires on no link, returns an error matching latere.ErrNotFound; an
// organization's context is refused as ListTriggerInbound says.
func (c *Client) IssueTriggerHook(ctx context.Context, triggerID string) (*TriggerHook, error) {
	if triggerID == "" {
		return nil, errNoTrigger
	}
	var out TriggerHook
	req := &latere.Request{Method: http.MethodPost, Path: inletPath(triggerID, "hook"), Retry: latere.RetryAlways}
	if err := c.cfg.Do(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RemoveTriggerHook turns the trigger's link off, so a post to it starts
// nothing. Turning off a link that is not there succeeds. A trigger the
// caller does not hold returns an error matching latere.ErrNotFound.
func (c *Client) RemoveTriggerHook(ctx context.Context, triggerID string) error {
	if triggerID == "" {
		return errNoTrigger
	}
	return c.cfg.Do(ctx, &latere.Request{Method: http.MethodDelete, Path: inletPath(triggerID, "hook")}, nil)
}

// IssueTriggerAddress gives the caller's trigger, by its id, its own email
// address, replacing the one it had, which stops working, and returns the
// address. A message sent or forwarded to it starts the trigger, at most
// 30 runs a day, with its sender as the event's actor and its sender,
// subject, text and first attachments as the payload; the trigger fires
// on events of product "mail" and verb "received", as an
// agents.StartsFromEmail routine does.
//
// A failure is sent again, though the call is a POST without an
// idempotency key: a repeat replaces the address the first one issued, so
// the address answered is the trigger's one address that works. An
// installation that receives no email returns an error matching
// ErrMailNotEnabled. A trigger the caller does not hold, or one that
// fires on no mail, returns an error matching latere.ErrNotFound; an
// organization's context is refused as ListTriggerInbound says.
func (c *Client) IssueTriggerAddress(ctx context.Context, triggerID string) (*TriggerAddress, error) {
	if triggerID == "" {
		return nil, errNoTrigger
	}
	var out TriggerAddress
	req := &latere.Request{Method: http.MethodPost, Path: inletPath(triggerID, "address"), Retry: latere.RetryAlways}
	if err := c.cfg.Do(ctx, req, &out); err != nil {
		return nil, withSentinel(err, addressCodes)
	}
	return &out, nil
}

// RemoveTriggerAddress turns the trigger's email address off, so mail to
// it starts nothing. Turning off an address that is not there succeeds.
// An installation that receives no email returns an error matching
// ErrMailNotEnabled, and a trigger the caller does not hold one matching
// latere.ErrNotFound.
func (c *Client) RemoveTriggerAddress(ctx context.Context, triggerID string) error {
	if triggerID == "" {
		return errNoTrigger
	}
	err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodDelete, Path: inletPath(triggerID, "address")}, nil)
	return withSentinel(err, addressCodes)
}
