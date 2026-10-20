// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package apps

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"latere.ai/x/sdk-go/latere"
)

// accessBase is where the API serves who may open an app.
const accessBase = "/v1/account/apps"

// errNoAppID is returned before any request when an app's ID is empty.
var errNoAppID = errors.New("apps: the app's id is empty")

// accessPath is the path of an app's access, or of one part of it.
func accessPath(appID string, rest ...string) string {
	p := accessBase + "/" + url.PathEscape(appID) + "/access"
	for _, r := range rest {
		p += "/" + url.PathEscape(r)
	}
	return p
}

// AccessAction is a change to who may open an app that the caller may
// make now.
type AccessAction string

// Access actions.
const (
	// AccessInvite lets the caller invite people.
	AccessInvite AccessAction = "invite"
	// AccessRevoke lets the caller take a person's access away.
	AccessRevoke AccessAction = "revoke"
	// AccessSetPassword lets the caller set or remove the app's password,
	// and makes a password open the app.
	AccessSetPassword AccessAction = "set_password"
)

// Role is what a person invited to an app may do.
type Role string

// Roles.
const (
	// RoleViewer opens the app at its address and its previews, and
	// nothing else of it.
	RoleViewer Role = "viewer"
)

// AccessState is whether an invited person has signed in yet.
type AccessState string

// Access states.
const (
	// AccessPending is an invite nobody has signed in with yet.
	AccessPending AccessState = "pending"
	// AccessActive is an invite a person signed in with.
	AccessActive AccessState = "active"
)

// Access says who may open a private app besides its owner.
type Access struct {
	// AppID is the app's ID.
	AppID string `json:"app_id"`
	// People are the people invited to the app.
	People []*Person `json:"people"`
	// Organization is the organization whose every member opens the app;
	// nil for a person's own app.
	Organization *AccessOrganization `json:"organization"`
	// Password says when the app's password was set; nil when it has
	// none. The password itself is in no answer.
	Password *Password `json:"password"`
	// Actions are the changes the caller may make now.
	Actions []AccessAction `json:"actions"`
	// Limits bound the people and the password.
	Limits AccessLimits `json:"limits"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered, with every field; nil for an
// Access the program built itself.
func (a *Access) RawJSON() json.RawMessage { return a.raw }

// UnmarshalJSON decodes the access and keeps the JSON for RawJSON.
func (a *Access) UnmarshalJSON(b []byte) error {
	type plain Access
	if err := json.Unmarshal(b, (*plain)(a)); err != nil {
		return err
	}
	a.raw = keep(b)
	return nil
}

// AccessOrganization is the organization that owns an app.
type AccessOrganization struct {
	// ID is the organization's ID.
	ID string `json:"id"`
}

// Password says when an app's password was set.
type Password struct {
	// SetAt is when the password was set.
	SetAt time.Time `json:"set_at"`
}

// AccessLimits bound who an app may be shared with.
type AccessLimits struct {
	// MaxPeople is the most people with access to one app, pending
	// invites included.
	MaxPeople int `json:"max_people"`
	// MaxEmailsPerInvite is the most addresses one Invite names.
	MaxEmailsPerInvite int `json:"max_emails_per_invite"`
	// MinPassword is the fewest characters a password has.
	MinPassword int `json:"min_password"`
	// MaxPassword is the most characters a password has.
	MaxPassword int `json:"max_password"`
}

// Person is someone invited to an app, by their email address.
type Person struct {
	// ID identifies the invite; give it to RevokeAccess.
	ID string `json:"id"`
	// Role is what the person may do.
	Role Role `json:"role"`
	// Email is the address invited.
	Email string `json:"email"`
	// State is AccessPending until a person signs in at the app with the
	// address, AccessActive after.
	State AccessState `json:"state"`
	// InvitedAt is when the address was invited.
	InvitedAt time.Time `json:"invited_at"`
	// ClaimedAt is when a person first signed in with it; zero until then.
	ClaimedAt time.Time `json:"claimed_at,omitzero"`
	// Account is the account that signed in with the address, when the
	// platform can name it; nil otherwise.
	Account *PersonAccount `json:"person,omitempty"`
}

// PersonAccount is the account of a person who signed in with an invited
// address.
type PersonAccount struct {
	// DisplayName is the name the person shows.
	DisplayName string `json:"display_name,omitempty"`
	// Handle is the person's handle.
	Handle string `json:"handle,omitempty"`
}

// GetAccess returns who may open the app appID names besides its owner,
// and what the caller may change. appID is the app's ID, not its slug.
func (c *Client) GetAccess(ctx context.Context, appID string) (*Access, error) {
	if appID == "" {
		return nil, errNoAppID
	}
	var out Access
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodGet, Path: accessPath(appID)}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// InviteParams names the people to invite.
type InviteParams struct {
	// Emails are the addresses to invite, at most
	// AccessLimits.MaxEmailsPerInvite. Required.
	Emails []string `json:"emails"`
	// Role is what the people invited may do; RoleViewer when empty.
	Role Role `json:"role,omitempty"`
}

// Invites is the answer of an Invite.
type Invites struct {
	// People are the addresses newly invited.
	People []*Person `json:"people"`
	// Skipped are the addresses not invited, each with the reason.
	Skipped []SkippedInvite `json:"skipped"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered, with every field; nil for
// Invites the program built itself.
func (i *Invites) RawJSON() json.RawMessage { return i.raw }

// UnmarshalJSON decodes the invites and keeps the JSON for RawJSON.
func (i *Invites) UnmarshalJSON(b []byte) error {
	type plain Invites
	if err := json.Unmarshal(b, (*plain)(i)); err != nil {
		return err
	}
	i.raw = keep(b)
	return nil
}

// SkippedInvite is an address an Invite did not invite.
type SkippedInvite struct {
	// Email is the address.
	Email string `json:"email"`
	// Reason is why the address was skipped.
	Reason SkipReason `json:"reason"`
}

// Invite invites people to the app appID names by their email addresses.
// The platform sends no email: a person opens the app once they sign in at
// it with an invited address.
// Addresses already invited, and the caller's own, are skipped rather
// than refused. Inviting too many people in a day returns an error
// matching latere.ErrRateLimited.
//
// Invite is sent once, never again after a failure: this part of the API
// takes no idempotency key, and a repeat would count the people again
// against the day's invitations.
func (c *Client) Invite(ctx context.Context, appID string, params *InviteParams) (*Invites, error) {
	if appID == "" {
		return nil, errNoAppID
	}
	if params == nil || len(params.Emails) == 0 {
		return nil, errors.New("apps: Invite: no email address was given")
	}
	var out Invites
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodPost, Path: accessPath(appID, "invites"), Body: params, Retry: latere.RetryNever}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RevokeAccess takes away the access of the person whose invite accessID
// names, a Person.ID, from the app appID names.
func (c *Client) RevokeAccess(ctx context.Context, appID, accessID string) error {
	if appID == "" {
		return errNoAppID
	}
	if accessID == "" {
		return errors.New("apps: the access id is empty")
	}
	return c.cfg.Do(ctx, &latere.Request{Method: http.MethodDelete, Path: accessPath(appID, accessID)}, nil)
}

// SetPassword sets the password of the app appID names, replacing any it
// had. The password opens the private app to anyone who has it, while
// Access.Actions names AccessSetPassword. Its length must be within
// AccessLimits.MinPassword and MaxPassword.
func (c *Client) SetPassword(ctx context.Context, appID, password string) error {
	if appID == "" {
		return errNoAppID
	}
	if password == "" {
		return errors.New("apps: SetPassword: the password is empty")
	}
	body := map[string]string{"password": password}
	return c.cfg.Do(ctx, &latere.Request{Method: http.MethodPut, Path: accessPath(appID, "password"), Body: body}, nil)
}

// RemovePassword removes the password of the app appID names, so only the
// people with access open it.
func (c *Client) RemovePassword(ctx context.Context, appID string) error {
	if appID == "" {
		return errNoAppID
	}
	return c.cfg.Do(ctx, &latere.Request{Method: http.MethodDelete, Path: accessPath(appID, "password")}, nil)
}

// SkipReason is why an invite skipped an address.
type SkipReason string

// The reasons an invite skips an address.
const (
	// SkipSelf is the caller's own address.
	SkipSelf SkipReason = "self"
	// SkipListed is an address already invited, or named twice.
	SkipListed SkipReason = "listed"
)
