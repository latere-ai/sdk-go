// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package evals

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"latere.ai/x/sdk-go/latere"
)

// BasePath is the path the API serves Evals under, below the base URL a
// Config names: every request the package sends is under it. A program
// that takes the address of Evals as one URL with this path, as a
// command's flag may, cuts it off to name the base URL WithBaseURL takes.
const BasePath = "/v1/evals"

// The page sizes of every list of Evals.
const (
	// DefaultLimit is how many items a page holds when a list's Limit is
	// zero.
	DefaultLimit = 50
	// MaxLimit is the most items a page holds; a larger Limit is refused
	// with 400.
	MaxLimit = 200
)

// Client reads Evals. It is safe for concurrent use.
type Client struct {
	cfg *latere.Config
}

// New returns a Client that sends its requests as cfg says. A cfg not
// built with latere.NewConfig makes every call fail with an error saying
// so.
func New(cfg *latere.Config) *Client { return &Client{cfg: cfg} }

// Errors returned before any request is sent, when an argument a route
// needs is empty.
var (
	errNoRef  = errors.New("evals: the ref is empty")
	errNoName = errors.New("evals: the name is empty")
	errNoID   = errors.New("evals: the experiment's id is empty")
)

// A Version is the record of one version of a definition, beside the
// definition's own fields in each of Metric, Grader, Task, Suite and
// Candidate.
type Version struct {
	// ID identifies the version.
	ID string `json:"id"`
	// Owner is the context the version belongs to, a person or an
	// organization.
	Owner string `json:"owner"`
	// Name is the definition's name, unique in its owner's context among
	// definitions of its kind.
	Name string `json:"name"`
	// Digest names the version's content, such as "sha256:" and its hex:
	// two applies of the same content are one version.
	Digest string `json:"digest"`
	// Current reports whether this is the newest version of its name, the
	// one the name alone reads.
	Current bool `json:"current"`
	// CreatedAt is when the version was first applied.
	CreatedAt time.Time `json:"created_at"`
}

// Ref returns the reference that names exactly this version,
// name@digest, which every Get method of this package reads.
func (v Version) Ref() string { return v.Name + "@" + v.Digest }

// ListVersionsParams page through the versions of one name. The zero value
// lists them newest first, in pages of DefaultLimit.
type ListVersionsParams struct {
	// Limit is the page size, at most MaxLimit;
	// DefaultLimit when zero.
	Limit int
	// Cursor asks a Page method for the page after the one that returned
	// it. The iterating methods ignore it.
	Cursor string
}

// get reads the resource at path into a new T.
func get[T any](ctx context.Context, c *Client, path string) (*T, error) {
	var out T
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodGet, Path: path}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// page reads one page of the list at path.
func page[T any](ctx context.Context, c *Client, path string, q url.Values) (*latere.Page[*T], error) {
	var out latere.Page[*T]
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodGet, Path: path, Query: q}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// pageFunc returns the fetch latere.Items calls for the list at path,
// with the query that query builds for each cursor.
func pageFunc[T any](c *Client, path string, query func(cursor string) url.Values) func(context.Context, string) (*latere.Page[*T], error) {
	return func(ctx context.Context, cursor string) (*latere.Page[*T], error) {
		return page[T](ctx, c, path, query(cursor))
	}
}

// setNonEmpty sets key to value in q when value is not empty.
func setNonEmpty(q url.Values, key, value string) {
	if value != "" {
		q.Set(key, value)
	}
}

// setBool sets key to "true" or "false" in q when b is not nil.
func setBool(q url.Values, key string, b *bool) {
	if b != nil {
		q.Set(key, strconv.FormatBool(*b))
	}
}

// listQuery is the query every list of Evals shares: a page's size and
// the cursor of the page before.
func listQuery(limit int, cursor string) url.Values {
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	return q
}

// keep returns a copy of b, which a decoder may reuse after it returns.
func keep(b []byte) json.RawMessage {
	return append(json.RawMessage(nil), b...)
}
