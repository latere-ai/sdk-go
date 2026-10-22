// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package account

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

// A Share is a read-only link to a session, up to one of its turns. Anyone
// holding the link reads that part of the conversation, signed out, and
// never what follows: the messages, the answers, a summary of each step,
// the pictures an answer showed and the files it linked, and never the
// person's own images or files, a command or its output, or the sharer's
// name.
type Share struct {
	// ID identifies the share: "shr_" and 26 characters.
	ID string `json:"id"`
	// SessionID is the session the link shows.
	SessionID string `json:"session_id"`
	// URL is the link itself, made again for every answer, so it can be
	// copied again later.
	URL string `json:"url"`
	// Audience is who reads the link.
	Audience ShareAudience `json:"audience"`
	// UpToSeq is the sequence of the session's last event the link shows.
	UpToSeq int64 `json:"up_to_seq"`
	// SessionLastSeq is the sequence of the session's last event now, so a
	// program can say the link shows less than the conversation and offer
	// UpdateShare; zero when the session cannot be read now.
	SessionLastSeq int64 `json:"session_last_seq,omitempty"`
	// Title is the session's title at the last event the link shows. A
	// session's title follows what its conversation is about, so a link
	// keeps the title of the part it shares.
	Title string `json:"title"`
	// State is ShareLive while the link reads, and ShareSuspended while
	// its read is refused.
	State ShareState `json:"state"`
	// Reason says why a suspended link's read is refused; empty for a live
	// one.
	Reason ShareReason `json:"reason,omitempty"`
	// CreatedAt is when the link was made.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is when it last moved to a later turn.
	UpdatedAt time.Time `json:"updated_at"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the share, with every
// field, including those this package does not model; nil for a Share the
// program built itself.
func (s *Share) RawJSON() json.RawMessage { return s.raw }

// UnmarshalJSON decodes the share and keeps the JSON for RawJSON.
func (s *Share) UnmarshalJSON(b []byte) error {
	type plain Share
	if err := json.Unmarshal(b, (*plain)(s)); err != nil {
		return err
	}
	s.raw = keep(b)
	return nil
}

// ShareAudience is who reads a share's link.
type ShareAudience string

// The audiences of a Share.
const (
	// ShareAnyone is anyone holding the link, signed out.
	ShareAnyone ShareAudience = "anyone"
	// ShareMembers is reserved for the members of an organization; the API
	// makes no such link yet.
	ShareMembers ShareAudience = "members"
)

// ShareState is whether a share's link reads.
type ShareState string

// The states of a Share.
const (
	// ShareLive is a link that reads.
	ShareLive ShareState = "live"
	// ShareSuspended is a link whose read is refused for its Reason. It
	// works again once the reason is gone; DeleteShare stops it for good.
	ShareSuspended ShareState = "suspended"
)

// ShareReason is why a suspended share's read is refused.
type ShareReason string

// The reasons a Share is suspended.
const (
	// ShareSharingOff is a link of a context that does not allow sharing
	// now, such as an organization whose owners turned sharing off.
	ShareSharingOff ShareReason = "sharing_off"
	// ShareAudienceNarrowed is a link whose audience the context's setting
	// no longer admits.
	ShareAudienceNarrowed ShareReason = "audience_narrowed"
	// ShareSessionUnreachable is a link of a session that was deleted.
	ShareSessionUnreachable ShareReason = "session_unreachable"
)

// sharesPath is the path of the caller's shares.
const sharesPath = "/v1/account/shares"

// errNoShare is returned before any request when a share's id is empty.
var errNoShare = errors.New("account: the share's id is empty")

// sharePath is the path of one share.
func sharePath(id string) string { return sharesPath + "/" + url.PathEscape(id) }

// ListSharesParams narrow and page ListShares and ListSharesPage. A nil
// *ListSharesParams lists every link of the caller in the token's context.
type ListSharesParams struct {
	// SessionID keeps the links of one session.
	SessionID string
	// All lists every link of the organization's sessions, for its owners
	// and admins; anyone else is refused with 403 and the code
	// "role_insufficient".
	All bool
	// Limit is the most links a page holds, up to 200; the API's 50 when
	// zero.
	Limit int
	// Cursor starts ListSharesPage at the page a previous one's NextCursor
	// named; ListShares ignores it.
	Cursor string
}

// ListShares returns the caller's links in the token's context, newest
// first, fetching pages as the loop asks for more. A key's token lists the
// links of the sessions its grants let it read, and no others.
func (c *Client) ListShares(ctx context.Context, params *ListSharesParams) iter.Seq2[*Share, error] {
	return latere.Items(ctx, func(ctx context.Context, cursor string) (*latere.Page[*Share], error) {
		return c.sharesPage(ctx, params, cursor)
	})
}

// ListSharesPage returns one page of the links ListShares lists, starting
// at params.Cursor, and the cursor of the next page in NextCursor.
func (c *Client) ListSharesPage(ctx context.Context, params *ListSharesParams) (*latere.Page[*Share], error) {
	cursor := ""
	if params != nil {
		cursor = params.Cursor
	}
	return c.sharesPage(ctx, params, cursor)
}

func (c *Client) sharesPage(ctx context.Context, params *ListSharesParams, cursor string) (*latere.Page[*Share], error) {
	q := url.Values{}
	if params != nil {
		if params.SessionID != "" {
			q.Set("session_id", params.SessionID)
		}
		if params.All {
			q.Set("all", "true")
		}
		if params.Limit > 0 {
			q.Set("limit", strconv.Itoa(params.Limit))
		}
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	var page latere.Page[*Share]
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodGet, Path: sharesPath, Query: q}, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// CreateShare makes a read-only link to a session the caller started, up
// to its last finished turn: its last event while it is idle or ended,
// and the turn before while it runs. UpdateShare moves the link to a later
// turn. A person's own context always shares; an organization's shares
// once its owners and admins turn sharing on.
//
// The call is sent once and never retried: the shares part of the API
// takes no idempotency key, so a repeat would make a second link. After a
// failure on the network, ListShares with the session's id tells whether
// the link was made.
//
// The API refuses a session the caller did not start with 403 and the
// code "not_initiator", a context that does not share with "sharing_off",
// and a session of another context than the token's with "wrong_context".
// A session with no finished turn is refused with 409 and
// "nothing_to_share", and a person holding 1,000 links with
// "share_limit_reached"; a person makes at most 30 links an hour. A
// session the caller does not read returns an error matching
// latere.ErrNotFound.
func (c *Client) CreateShare(ctx context.Context, sessionID string) (*Share, error) {
	if sessionID == "" {
		return nil, errors.New("account: CreateShare: the session id is empty")
	}
	var out Share
	req := &latere.Request{Method: http.MethodPost, Path: sharesPath, Body: map[string]string{"session_id": sessionID}, Retry: latere.RetryNever}
	if err := c.cfg.Do(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateShare moves the link of share id to its session's last finished
// turn now, so its readers see what followed, and returns the share. The
// link itself does not change. Only the link's creator moves it.
//
// A suspended link is refused with 409 and the code "share_suspended", and
// a session with no finished turn now with "nothing_to_share". An id of no
// link the caller reaches returns an error matching latere.ErrNotFound.
func (c *Client) UpdateShare(ctx context.Context, id string) (*Share, error) {
	if id == "" {
		return nil, errNoShare
	}
	var out Share
	// The move sets the bound to the latest turn, so sending it again has
	// the effect of sending it once, and it is retried.
	req := &latere.Request{Method: http.MethodPatch, Path: sharePath(id), Body: map[string]string{"up_to": "latest"}, Retry: latere.RetryAlways}
	if err := c.cfg.Do(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteShare deletes the link of share id, so it stops working for
// everyone at once. Its creator deletes it, and an organization's owner or
// admin deletes any link of the organization's sessions. Deleting a link
// that is not there succeeds, so the call is safe to repeat; a link of
// someone else's that the caller may not delete returns an error matching
// latere.ErrNotFound.
func (c *Client) DeleteShare(ctx context.Context, id string) error {
	if id == "" {
		return errNoShare
	}
	return c.cfg.Do(ctx, &latere.Request{Method: http.MethodDelete, Path: sharePath(id)}, nil)
}
