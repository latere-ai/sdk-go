// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package account

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"latere.ai/x/sdk-go/latere"
)

// preferencesPath is the path of the caller's preferences.
const preferencesPath = "/v1/account/preferences"

// UpdatePreferencesParams are the preferences to change. A nil field keeps
// its value, and a pointer to "" returns a member to its default: the
// network's mode to the context's ("ask" for a person, "listed" for an
// organization), sharing to the context's, and the profile and the
// instructions to none. ZeroRetention's default is false.
//
// Sites, pins and bookmarks are not set here but one at a time, with
// AddSite, PinSession, BookmarkTurn and their pairs, so that two programs
// writing at once do not drop each other's.
type UpdatePreferencesParams struct {
	// NetworkMode is which sites a session may reach: "listed",
	// "package_managers", "ask" or "open", among the context's
	// Network.Modes. A mode the context's plan does not allow is refused
	// with 400 and the code "mode_not_offered".
	NetworkMode *string
	// SharingMode is whether conversations may be shared by link: "off"
	// or "anyone", among the context's Sharing.Modes.
	SharingMode *string
	// CallMe is what sessions call the person, at most Profile.MaxCallMe
	// characters on one line.
	CallMe *string
	// Work is the person's kind of work, one of Profile.Works.
	Work *string
	// Instructions are the person's standing instructions to every
	// session, at most Instructions.MaxChars characters.
	Instructions *string
	// ZeroRetention sends the model requests of the person's sessions,
	// their own keys and their personal Parsing only to endpoints that
	// keep nothing of them, and every session they hold and key they own
	// takes it at once. Turning it off releases their keys and no session
	// that took it.
	ZeroRetention *bool
}

// patch returns the merge patch the parameters send: each member set, a
// text member of "" as null, which returns it to its default.
func (p *UpdatePreferencesParams) patch() map[string]map[string]any {
	out := map[string]map[string]any{}
	text := func(section, member string, v *string) {
		if v == nil {
			return
		}
		if out[section] == nil {
			out[section] = map[string]any{}
		}
		if *v == "" {
			out[section][member] = nil
			return
		}
		out[section][member] = *v
	}
	text("network", "mode", p.NetworkMode)
	text("sharing", "mode", p.SharingMode)
	text("profile", "call_me", p.CallMe)
	text("profile", "work", p.Work)
	text("instructions", "text", p.Instructions)
	if p.ZeroRetention != nil {
		out["data"] = map[string]any{"zero_retention": *p.ZeroRetention}
	}
	return out
}

// UpdatePreferences changes the caller's preferences in the token's
// context and returns them whole, as GetPreferences reads them. Every
// value is checked before anything is written, so a change is applied
// whole or refused whole. A change that sets nothing fails before any
// request.
//
// A value outside its rule is refused with 400 and the code
// "invalid_field", whose Details' "field" names the member. In an
// organization's context only its owners and admins change the network
// and sharing, and anyone else is refused with 403 and the code
// "managed_by_organization".
func (c *Client) UpdatePreferences(ctx context.Context, params *UpdatePreferencesParams) (*Preferences, error) {
	if params == nil {
		return nil, errors.New("account: UpdatePreferences changes nothing")
	}
	body := params.patch()
	if len(body) == 0 {
		return nil, errors.New("account: UpdatePreferences changes nothing")
	}
	var out Preferences
	// A merge patch sets values, so sending it again has the effect of
	// sending it once, and it is retried.
	req := &latere.Request{Method: http.MethodPatch, Path: preferencesPath, Body: body, Retry: latere.RetryAlways}
	if err := c.cfg.Do(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// errNoSite is returned before any request when a site is empty.
var errNoSite = errors.New("account: the site is empty")

// sitePath is the path of one site of the network's list.
func sitePath(site string) string {
	return preferencesPath + "/network/domains/" + url.PathEscape(site)
}

// AddSite adds site to the token's context's list of sites, which every
// session in the context reaches, and returns the network section. site is
// a host name such as "example.com", or one with a single leading "*.",
// such as "*.example.com", which covers every name under it and not the
// name itself; the API reads it lower-cased and drops one trailing dot.
// Adding a site already there changes nothing, so a program adds one
// without reading the list first.
//
// A site outside that rule, such as an address, a port or a single label,
// is refused with 400 and the code "invalid_field", and a list that holds
// Network.MaxDomains sites with "ceiling_exceeded". In an organization's
// context only its owners and admins add one; anyone else is refused with
// 403 and the code "managed_by_organization".
func (c *Client) AddSite(ctx context.Context, site string) (*Network, error) {
	if site == "" {
		return nil, errNoSite
	}
	var out Network
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodPut, Path: sitePath(site)}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RemoveSite removes site from the token's context's list of sites.
// Removing a site that is not there succeeds. In an organization's context
// only its owners and admins remove one; anyone else is refused with 403
// and the code "managed_by_organization".
func (c *Client) RemoveSite(ctx context.Context, site string) error {
	if site == "" {
		return errNoSite
	}
	return c.cfg.Do(ctx, &latere.Request{Method: http.MethodDelete, Path: sitePath(site)}, nil)
}

// bookmarkPath is the path of the bookmark of one turn.
func bookmarkPath(sessionID string, seq int64) (string, error) {
	if sessionID == "" {
		return "", errors.New("account: the session id is empty")
	}
	if seq < 1 {
		return "", errors.New("account: a turn's sequence is positive")
	}
	return preferencesPath + "/bookmarks/" + url.PathEscape(sessionID) + "/" + strconv.FormatInt(seq, 10), nil
}

// BookmarkTurn bookmarks the turn whose first event has sequence seq in a
// session of the token's context, so a program shows it in the session's
// outline on every device, and returns the bookmarks. Bookmarking a turn
// already bookmarked changes nothing, so a program bookmarks one without
// reading the bookmarks first.
//
// A session holds at most Bookmarks.MaxPerSession bookmarks and a context
// bookmarks turns in at most Bookmarks.MaxSessions sessions; past either
// the API refuses with 400 and the code "ceiling_exceeded". A session the
// caller does not read in the token's context returns an error matching
// latere.ErrNotFound.
func (c *Client) BookmarkTurn(ctx context.Context, sessionID string, seq int64) (*Bookmarks, error) {
	p, err := bookmarkPath(sessionID, seq)
	if err != nil {
		return nil, err
	}
	var out Bookmarks
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodPut, Path: p}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UnbookmarkTurn removes the bookmark of the turn at seq of a session in
// the token's context. Removing one that is not there succeeds; an id that
// is no session's returns an error matching latere.ErrNotFound.
func (c *Client) UnbookmarkTurn(ctx context.Context, sessionID string, seq int64) error {
	p, err := bookmarkPath(sessionID, seq)
	if err != nil {
		return err
	}
	return c.cfg.Do(ctx, &latere.Request{Method: http.MethodDelete, Path: p}, nil)
}

// pin pins, with PUT, or unpins, with DELETE, the app or the project of
// the kind named, among the pins of the token's context, and returns the
// pins a pin answers.
func (c *Client) pin(ctx context.Context, method, kind, id string) (*Pins, error) {
	if id == "" {
		return nil, errors.New("account: the " + kind + " id is empty")
	}
	path := preferencesPath + "/pins/" + kind + "s/" + url.PathEscape(id)
	if method == http.MethodDelete {
		return nil, c.cfg.Do(ctx, &latere.Request{Method: method, Path: path}, nil)
	}
	var out Pins
	if err := c.cfg.Do(ctx, &latere.Request{Method: method, Path: path}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PinApp pins an app in the token's context, by its id, after the apps
// pinned before it, and returns the pins. Only an app of the token's
// context that the caller can open is pinned: the person's own in their
// personal context, the organization's in an organization's. Pinning an
// app already pinned changes nothing.
//
// A context that holds Pins.MaxApps pinned apps is refused with 400 and
// the code "ceiling_exceeded". An id of no app the caller can open in the
// token's context returns an error matching latere.ErrNotFound.
func (c *Client) PinApp(ctx context.Context, appID string) (*Pins, error) {
	return c.pin(ctx, http.MethodPut, "app", appID)
}

// UnpinApp unpins an app in the token's context. Unpinning an app that is
// not pinned succeeds.
func (c *Client) UnpinApp(ctx context.Context, appID string) error {
	_, err := c.pin(ctx, http.MethodDelete, "app", appID)
	return err
}

// PinProject pins a project in the token's context, by its id, after the
// projects pinned before it, and returns the pins. Only a project the
// caller reads in the token's context is pinned. Pinning a project already
// pinned changes nothing.
//
// A context that holds Pins.MaxProjects pinned projects is refused with
// 400 and the code "ceiling_exceeded". An id of no project the caller
// reads returns an error matching latere.ErrNotFound.
func (c *Client) PinProject(ctx context.Context, projectID string) (*Pins, error) {
	return c.pin(ctx, http.MethodPut, "project", projectID)
}

// UnpinProject unpins a project in the token's context. Unpinning a
// project that is not pinned succeeds.
func (c *Client) UnpinProject(ctx context.Context, projectID string) error {
	_, err := c.pin(ctx, http.MethodDelete, "project", projectID)
	return err
}
