// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package apps

import (
	"context"
	"encoding/json"
	"iter"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"latere.ai/x/sdk-go/latere"
)

// AppSession is a session of the agents capability that built an app: one
// a deploy's commit names in its Topos-Session trailer, or the session the
// app was created in. It is an attribution and grants nothing, since
// whoever makes a commit writes its trailer: read the session through the
// agents package, which decides whether the caller may.
type AppSession struct {
	// ID is the session's ID.
	ID string `json:"id"`
	// FirstPublishedAt is when the session's first preview deploy was
	// created; zero for the session the app was created in when no deploy
	// names it.
	FirstPublishedAt time.Time `json:"first_published_at,omitzero"`
	// LastPublishedAt is when its newest preview deploy was created; zero
	// when FirstPublishedAt is.
	LastPublishedAt time.Time `json:"last_published_at,omitzero"`
	// Deploys is how many preview deploys name the session. A release
	// serves its preview's commit and is not counted again.
	Deploys int `json:"deploys"`
	// Released reports that a deploy naming the session went live, so its
	// work reached the app's address at least once.
	Released bool `json:"released"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the session's row, with
// every field; nil for an AppSession the program built itself.
func (s *AppSession) RawJSON() json.RawMessage { return s.raw }

// UnmarshalJSON decodes the row and keeps the JSON for RawJSON.
func (s *AppSession) UnmarshalJSON(b []byte) error {
	type plain AppSession
	if err := json.Unmarshal(b, (*plain)(s)); err != nil {
		return err
	}
	s.raw = keep(b)
	return nil
}

// ListAppSessionsParams pages the sessions ListAppSessions returns. The
// zero value lists them all.
type ListAppSessionsParams struct {
	// Limit is the page size: 50 when zero, at most 200.
	Limit int
	// Cursor asks ListAppSessionsPage for the page after the one that
	// returned it. ListAppSessions ignores it.
	Cursor string
}

func (p *ListAppSessionsParams) query(cursor string) url.Values {
	q := url.Values{}
	if p != nil && p.Limit > 0 {
		q.Set("limit", strconv.Itoa(p.Limit))
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	return q
}

// ListAppSessions returns an iterator over the sessions that built the app
// slug names, newest publish first, fetching pages as the loop asks for
// more. slug may also be the app's ID. The session the app was created in
// comes last when no deploy names it, and a deploy whose commit names no
// session is in no row. An app the caller may not read returns an error
// matching latere.ErrNotFound. params may be nil.
func (c *Client) ListAppSessions(ctx context.Context, slug string, params *ListAppSessionsParams) iter.Seq2[*AppSession, error] {
	return latere.Items(ctx, func(ctx context.Context, cursor string) (*latere.Page[*AppSession], error) {
		return c.appSessionsPage(ctx, slug, params, cursor)
	})
}

// ListAppSessionsPage returns one page of the sessions that built the app
// slug names, starting at params.Cursor, and the cursor of the next page
// in NextCursor; it is empty on the last page.
func (c *Client) ListAppSessionsPage(ctx context.Context, slug string, params *ListAppSessionsParams) (*latere.Page[*AppSession], error) {
	cursor := ""
	if params != nil {
		cursor = params.Cursor
	}
	return c.appSessionsPage(ctx, slug, params, cursor)
}

func (c *Client) appSessionsPage(ctx context.Context, slug string, params *ListAppSessionsParams, cursor string) (*latere.Page[*AppSession], error) {
	if slug == "" {
		return nil, errNoSlug
	}
	var page latere.Page[*AppSession]
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodGet, Path: appPath(slug, "sessions"), Query: params.query(cursor)}, &page); err != nil {
		return nil, err
	}
	return &page, nil
}
