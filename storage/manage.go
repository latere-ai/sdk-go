// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"latere.ai/x/sdk-go/latere"
)

// errNoDestination is returned before any request when a move names no
// path to move to.
var errNoDestination = errors.New("storage: the path to move to is required")

// MoveFile moves the file at path in owner's space to the path to, in the
// same space, and returns the file at its new path. A move renames and
// copies no bytes; the file's versions, its bookmarks and a share of
// that one file go with it. A share of a folder above it stays where it
// is.
//
// A path holding no file returns an error matching latere.ErrNotFound, a
// destination that already holds one is refused with 409 and the code
// "path_taken" (latere.ErrConflict), and a destination equal to path with
// 400. Move a folder with MoveFolder.
//
// The Storage API takes no idempotency key, so MoveFile is sent once and
// never retried: a repeat after an answer lost on the network would find
// the file gone from path and be refused with 404, though the first move
// succeeded. After such a failure, StatFile on to tells whether it moved.
func (c *Client) MoveFile(ctx context.Context, owner, path, to string) (*File, error) {
	p, err := filePath(owner, path)
	if err != nil {
		return nil, err
	}
	if to == "" {
		return nil, errNoDestination
	}
	var out File
	req := &latere.Request{Method: http.MethodPost, Path: p, Body: map[string]string{"move_to": to}, Retry: latere.RetryNever}
	if err := c.cfg.Do(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// A MovedFolder is a folder as MoveFolder left it.
type MovedFolder struct {
	// Path is the folder's new path.
	Path string `json:"path"`
	// Files is how many files moved with it.
	Files int `json:"files"`
	// Bytes is the size of those files.
	Bytes int64 `json:"bytes"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the move, including any
// field MovedFolder does not model.
func (m *MovedFolder) RawJSON() json.RawMessage { return m.raw }

// UnmarshalJSON decodes the move and keeps the JSON for RawJSON.
func (m *MovedFolder) UnmarshalJSON(b []byte) error {
	type plain MovedFolder
	if err := json.Unmarshal(b, (*plain)(m)); err != nil {
		return err
	}
	m.raw = keep(b)
	return nil
}

// MoveFolder moves the folder at path in owner's space, with every file
// under it, to the path to, in one change: every file moves, or none
// does. The files' versions, their bookmarks and the shares of paths
// under the folder go with them.
//
// A folder with nothing under it returns an error matching
// latere.ErrNotFound; a destination path any of its files would land on
// that already holds a file, one in the trash included, is refused with
// 409 and the code "path_taken" (latere.ErrConflict); a destination
// inside the folder itself is refused with 400; and a folder that
// changed while it moved is refused with 412
// (latere.ErrPreconditionFailed), having moved nothing.
//
// Like MoveFile, MoveFolder is sent once and never retried, since a
// repeat after a lost answer would find nothing left at path. After such
// a failure, ListFilesPage on to tells whether it moved.
func (c *Client) MoveFolder(ctx context.Context, owner, path, to string) (*MovedFolder, error) {
	p, err := filePath(owner, path)
	if err != nil {
		return nil, err
	}
	if to == "" {
		return nil, errNoDestination
	}
	var out MovedFolder
	req := &latere.Request{Method: http.MethodPost, Path: p, Body: map[string]any{"move_to": to, "recursive": true}, Retry: latere.RetryNever}
	if err := c.cfg.Do(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteOptions are the choices of a delete. A nil *DeleteOptions moves
// what it deletes to the space's trash, unless the space keeps no trash,
// in which case every delete is permanent.
type DeleteOptions struct {
	// Permanent removes the file and its versions at once, rather than
	// moving them to the trash, from which they can be restored.
	Permanent bool
}

// query encodes the options and whether the delete is of a folder.
func (o *DeleteOptions) query(folder bool) url.Values {
	q := url.Values{}
	if folder {
		q.Set("recursive", "1")
	}
	if o != nil && o.Permanent {
		q.Set("permanent", "1")
	}
	return q
}

// DeleteFile moves the file at path in owner's space to the trash, or
// with opts.Permanent removes it with its versions. A path holding no
// file returns an error matching latere.ErrNotFound. Delete a folder
// with DeleteFolder.
//
// A delete is retried after a failure on the network, so a retry can
// meet the 404 of a delete that succeeded; the path holds no file either
// way.
func (c *Client) DeleteFile(ctx context.Context, owner, path string, opts *DeleteOptions) error {
	p, err := filePath(owner, path)
	if err != nil {
		return err
	}
	return c.cfg.Do(ctx, &latere.Request{Method: http.MethodDelete, Path: p, Query: opts.query(false)}, nil)
}

// DeleteFolder moves the folder at path in owner's space, with every file
// under it, to the trash in one change, or with opts.Permanent removes
// them with their versions. A folder named "files/notes" holds the files
// under "files/notes/", never "files/notes-old". A folder with nothing
// under it returns an error matching latere.ErrNotFound.
//
// As with DeleteFile, a retry after a failure on the network can meet
// the 404 of a delete that succeeded.
func (c *Client) DeleteFolder(ctx context.Context, owner, path string, opts *DeleteOptions) error {
	p, err := filePath(owner, path)
	if err != nil {
		return err
	}
	return c.cfg.Do(ctx, &latere.Request{Method: http.MethodDelete, Path: p, Query: opts.query(true)}, nil)
}
