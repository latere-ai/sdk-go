// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"net/url"
	"strings"
	"time"

	"latere.ai/x/sdk-go/latere"
)

// sharesPath is where the API serves the read of a shared conversation,
// by its link's token.
const sharesPath = "/v1/shares"

// errNoToken is returned before any request when a share's token is
// empty.
var errNoToken = errors.New("agents: the share's token is empty")

// sharePath is the path of a share's read, or of a route under it.
func sharePath(token, rest string) string {
	return sharesPath + "/" + url.PathEscape(token) + rest
}

// A Share is a link to a conversation as the person reading it sees it:
// the conversation's title and how far the link shows it. A person makes
// and stops links in their account; this is what anyone holding one
// reads.
type Share struct {
	// Title is the conversation's title at the turn the link shows it up
	// to.
	Title string `json:"title"`
	// SessionCreatedAt is when the conversation began.
	SessionCreatedAt time.Time `json:"session_created_at"`
	// SharedAt is when the link was made.
	SharedAt time.Time `json:"shared_at"`
	// UpdatedAt is when the link last moved to a later turn.
	UpdatedAt time.Time `json:"updated_at"`
	// UpToSeq is the last event of the log the link shows: the end of the
	// last turn that had finished when it was made or moved.
	UpToSeq int64 `json:"up_to_seq"`
}

// A SharedPage is one page of a shared conversation: the share, and the
// events of its log a reader sees, in order.
type SharedPage struct {
	// Share is the link the page is read through.
	Share Share `json:"share"`
	// Events are the page's events, at most 500. A reader sees the
	// session's own thread alone, and of it the messages, the answers,
	// the tool calls and their results, the answers to questions, the
	// interrupts, the status and model changes, the compactions and the
	// files kept, each with what of its payload a reader may see; a
	// redacted event's payload is {"tombstone": true}. Decode them as the
	// events of ListEvents; a shared event names no SessionID.
	Events []*Event `json:"events"`
	// NextCursor names the next page in SharedPageParams.Cursor; empty on
	// the last.
	NextCursor string `json:"next_cursor,omitempty"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the page, with every
// field, including those this package does not model.
func (p *SharedPage) RawJSON() json.RawMessage { return p.raw }

// UnmarshalJSON decodes the page and keeps the JSON for RawJSON.
func (p *SharedPage) UnmarshalJSON(b []byte) error {
	type plain SharedPage
	if err := json.Unmarshal(b, (*plain)(p)); err != nil {
		return err
	}
	p.raw = keep(b)
	return nil
}

// SharedPageParams picks a page of a shared conversation.
type SharedPageParams struct {
	// Cursor starts the page where a previous page's NextCursor said; the
	// first page when empty.
	Cursor string
}

// GetSharedPage reads one page of the conversation a share link shows,
// by the link's token, the last part of its address. A nil params reads
// the first page.
//
// A link anyone may read, the one kind of link the API makes, reads no
// credential and answers the same to every reader. The read sends the
// Config's credential all the same, since the share's audience, which
// Share in package account names, may be an organization's members, whom
// the read answers by their token alone. A program with no credential of
// its own, such as a server rendering a link's page for a visitor, reads
// with a Config built with latere.WithoutCredentials. A token that is no link's, a
// link that was stopped, and a link the reader may not read all return
// an error matching latere.ErrNotFound, so a link's existence is never
// told to one who may not read it. Each client address and each link may
// be read only so often a minute; past that the call returns an error
// matching latere.ErrRateLimited once its retries are spent.
func (c *Client) GetSharedPage(ctx context.Context, token string, params *SharedPageParams) (*SharedPage, error) {
	if token == "" {
		return nil, errNoToken
	}
	q := url.Values{}
	if params != nil && params.Cursor != "" {
		q.Set("cursor", params.Cursor)
	}
	var out SharedPage
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodGet, Path: sharePath(token, ""), Query: q}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListSharedEvents returns an iterator over the events a share link
// shows, in order, fetching pages as the loop needs them; read the
// share's title with GetSharedPage. It reads as GetSharedPage does, and
// an error ends the iteration after it is yielded.
func (c *Client) ListSharedEvents(ctx context.Context, token string) iter.Seq2[*Event, error] {
	return latere.Items(ctx, func(ctx context.Context, cursor string) (*latere.Page[*Event], error) {
		page, err := c.GetSharedPage(ctx, token, &SharedPageParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		return &latere.Page[*Event]{Items: page.Events, NextCursor: page.NextCursor}, nil
	})
}

// ReadSharedBlob opens a file the shared conversation kept, by its
// digest, as a KeptFile of a files.kept event the link shows names it;
// the caller closes the reader. The file's leading bytes decide its
// answer: a PNG, JPEG, GIF or WebP is answered as its own media type, to
// show in place, and any other file, an SVG or a page included, as
// "application/octet-stream" with a ContentDisposition of "attachment"
// and the file's name made safe, to save; the KeptFile holds the file's
// own type and size. A picture an answer showed is answered up to 5 MiB,
// and a larger one as not found.
//
// A digest no event within the link's bound names returns an error
// matching latere.ErrNotFound, as GetSharedPage's refusals do, and it
// reads a credential as GetSharedPage does. A link's files are answered
// a few at a time on each server; a read past that is answered 503 with
// a wait, which the SDK retries by its policy. The Config's timeout
// bounds the wait for the answer, and after it only ctx bounds the read.
func (c *Client) ReadSharedBlob(ctx context.Context, token, digest string) (*FileReader, error) {
	if token == "" {
		return nil, errNoToken
	}
	// A digest is an algorithm and a hash, such as "sha256:" and its hex,
	// which also keeps a path segment of "." or ".." out of the route.
	if algorithm, sum, ok := strings.Cut(digest, ":"); !ok || algorithm == "" || sum == "" {
		return nil, fmt.Errorf("agents: ReadSharedBlob: %q is not a digest, such as a KeptFile's Blob", digest)
	}
	resp, err := c.cfg.Open(ctx, &latere.Request{Method: http.MethodGet, Path: sharePath(token, "/blobs/"+url.PathEscape(digest)), Header: acceptAny()})
	if err != nil {
		return nil, err
	}
	return newFileReader(resp), nil
}
