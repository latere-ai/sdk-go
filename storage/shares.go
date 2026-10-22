// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package storage

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

// A Permission is what a share lets its grantee do, a rung of a ladder in
// which each rung allows what the ones below it do.
type Permission string

// The rungs of a share.
const (
	// PermissionRead reads the files under the shared path.
	PermissionRead Permission = "read"
	// PermissionWrite also writes, moves and deletes them.
	PermissionWrite Permission = "write"
	// PermissionManage also shares them with others.
	PermissionManage Permission = "manage"
)

// A GranteeKind says whom a share is given to.
type GranteeKind string

// The kinds of grantee.
const (
	// GranteeSubject is a person, a service or an organization, named
	// by Grant.Grantee.
	GranteeSubject GranteeKind = "subject"
	// GranteeLink is whoever holds a link's token.
	GranteeLink GranteeKind = "link"
	// GranteePublic is anyone, without a credential.
	GranteePublic GranteeKind = "public"
)

// A GrantStatus says whether a share still works.
type GrantStatus string

// The states of a share.
const (
	// GrantActive is a share in force.
	GrantActive GrantStatus = "active"
	// GrantRevoked is a share its owner took back, kept for the record.
	GrantRevoked GrantStatus = "revoked"
)

// A Grant is a share of a file or a folder of a space: the path it
// covers, everything under it included, whom it is given to and what it
// lets them do.
type Grant struct {
	// ID is the share's id.
	ID string `json:"id"`
	// Owner is the space the shared path is in, as the API writes an
	// owner, which names it in ReadFile, ListFiles and the other calls
	// that take an owner.
	Owner string `json:"owner"`
	// PathPrefix is the shared path: a file, or a folder and every file
	// under it.
	PathPrefix string `json:"path_prefix"`
	// GranteeKind says whom the share is given to.
	GranteeKind GranteeKind `json:"grantee_kind"`
	// Grantee names the grantee of GranteeSubject; empty for the others.
	Grantee string `json:"grantee,omitempty"`
	// Permission is what the share lets the grantee do.
	Permission Permission `json:"permission"`
	// Status says whether the share works.
	Status GrantStatus `json:"status"`
	// ExpiresAt is when the share stops working; zero for one that does
	// not expire.
	ExpiresAt time.Time `json:"expires_at"`
	// CreatedBy names who made the share.
	CreatedBy string `json:"created_by"`
	// CreatedAt is when it was made.
	CreatedAt time.Time `json:"created_at"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the share, including any
// field Grant does not model.
func (g *Grant) RawJSON() json.RawMessage { return g.raw }

// UnmarshalJSON decodes the share and keeps the JSON for RawJSON.
func (g *Grant) UnmarshalJSON(b []byte) error {
	type plain Grant
	if err := json.Unmarshal(b, (*plain)(g)); err != nil {
		return err
	}
	g.raw = keep(b)
	return nil
}

// ListSharedParams narrow a listing of shares. A nil *ListSharedParams
// lists every share, a page of 100 at a time.
type ListSharedParams struct {
	// Limit is the page size: 100 when zero, at most 1000.
	Limit int
	// Cursor asks ListSharedWithMePage for the page after the one that
	// returned it. ListSharedWithMe ignores it.
	Cursor string
}

func (p *ListSharedParams) query(cursor string) url.Values {
	q := url.Values{}
	if p != nil && p.Limit > 0 {
		q.Set("limit", strconv.Itoa(p.Limit))
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	return q
}

// ListSharedWithMe returns an iterator over the shares other people gave
// the caller, of their files and folders in their spaces, fetching pages
// as the loop asks for more. Read a shared file with ReadFile or StatFile,
// and a shared folder with ListFiles, by the Grant's Owner and a path at
// or under its PathPrefix. params may be nil.
func (c *Client) ListSharedWithMe(ctx context.Context, params *ListSharedParams) iter.Seq2[*Grant, error] {
	return latere.Items(ctx, func(ctx context.Context, cursor string) (*latere.Page[*Grant], error) {
		page, err := c.sharedPage(ctx, params, cursor)
		if err != nil {
			return nil, err
		}
		return &latere.Page[*Grant]{Items: page.Grants, NextCursor: page.NextCursor}, nil
	})
}

// A GrantsPage is one page of shares.
type GrantsPage struct {
	// Grants are the page's shares.
	Grants []*Grant `json:"entries"`
	// NextCursor asks for the next page; empty on the last.
	NextCursor string `json:"next_cursor,omitempty"`
}

// ListSharedWithMePage returns one page of the shares other people gave
// the caller, starting at params.Cursor. A page may hold fewer shares
// than its limit while more follow, since a share the caller's
// organization does not let them open is left out.
func (c *Client) ListSharedWithMePage(ctx context.Context, params *ListSharedParams) (*GrantsPage, error) {
	cursor := ""
	if params != nil {
		cursor = params.Cursor
	}
	return c.sharedPage(ctx, params, cursor)
}

func (c *Client) sharedPage(ctx context.Context, params *ListSharedParams, cursor string) (*GrantsPage, error) {
	var page GrantsPage
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodGet, Path: basePath + "/shares/with-me", Query: params.query(cursor)}, &page); err != nil {
		return nil, err
	}
	return &page, nil
}
