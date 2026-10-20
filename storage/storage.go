// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"latere.ai/x/sdk-go/latere"
)

// basePath is where the API serves Storage.
const basePath = "/v1/storage"

// Me names your own space as an owner: the space of whoever the
// credential speaks for.
const Me = "me"

// The sizes the hosted platform holds a file to.
const (
	// MaxWriteBytes is the largest file WriteFile sends in one request; a
	// larger one is refused with 413, and goes up in parts, which
	// UploadFile does by itself.
	MaxWriteBytes = 16 << 20
	// MaxFileBytes is the largest file Storage keeps.
	MaxFileBytes = 5 << 30
)

// errNoPath is returned before any request when a file's owner or path is
// empty.
var errNoPath = errors.New("storage: the owner and the path are required")

// filePath is the endpoint of the file or folder at path in owner's space,
// each segment escaped on its own so a name with a space or a percent sign
// reaches the API as written.
func filePath(owner, path string) (string, error) {
	if owner == "" || path == "" {
		return "", errNoPath
	}
	segs := strings.Split(path, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return basePath + "/files/" + url.PathEscape(owner) + "/" + strings.Join(segs, "/"), nil
}

// Client calls the Storage API. Create one with New; it is safe for
// concurrent use.
type Client struct {
	cfg *latere.Config
}

// New returns a Client that sends its requests as cfg says.
func New(cfg *latere.Config) *Client { return &Client{cfg: cfg} }

// A File is a file in a space, as a write, a completed upload or a listing
// answers it.
type File struct {
	// Path is the file's place in its space, starting with "files/".
	Path string `json:"path"`
	// Size is the file's length in bytes.
	Size int64 `json:"size"`
	// Checksum identifies the file's bytes; a write conditional on them
	// names it in WriteOptions.IfMatch.
	Checksum string `json:"checksum"`
	// ChecksumKind is ChecksumSHA256 for a file written in one request,
	// and ChecksumETag, object storage's own label, for one assembled
	// from parts.
	ChecksumKind ChecksumKind `json:"checksum_kind"`
	// ContentType is the media type the file was written with.
	ContentType string `json:"content_type"`
	// Public reports that a public link marked the file, so URL reads it
	// without a credential.
	Public bool `json:"is_public"`
	// Modified is when the file's current bytes were written.
	Modified time.Time `json:"modified"`
	// URL is where a public file is read without a credential; empty for
	// every other file.
	URL string `json:"url,omitempty"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the file, including any
// field File does not model.
func (f *File) RawJSON() json.RawMessage { return f.raw }

// UnmarshalJSON decodes the file and keeps the JSON for RawJSON.
func (f *File) UnmarshalJSON(b []byte) error {
	type plain File
	if err := json.Unmarshal(b, (*plain)(f)); err != nil {
		return err
	}
	f.raw = keep(b)
	return nil
}

// ChecksumKind says what a File's Checksum is.
type ChecksumKind string

// The kinds of checksum.
const (
	// ChecksumSHA256 is the SHA-256 of the file's bytes, in hex.
	ChecksumSHA256 ChecksumKind = "sha256"
	// ChecksumETag is object storage's label for a file assembled from
	// parts, which is not a digest of its bytes.
	ChecksumETag ChecksumKind = "etag"
)

// WriteOptions are the choices of a write. A nil *WriteOptions writes with
// no condition and the type "application/octet-stream".
type WriteOptions struct {
	// ContentType is the file's media type, stored and served back.
	ContentType string
	// IfMatch writes only if the file still holds the bytes of this
	// checksum, as a File's Checksum names them; otherwise the write is
	// refused with latere.ErrPreconditionFailed. Use it when two writers
	// share a path.
	IfMatch string
	// IfNoneMatch writes only if nothing is at the path yet, so a write
	// never replaces a file; otherwise it is refused with
	// latere.ErrPreconditionFailed.
	IfNoneMatch bool
}

// WriteFile writes size bytes of content to path in owner's space, in one
// request, and returns the file. A file that was there is kept as a
// version. A file larger than MaxWriteBytes is refused with 413; write it
// with UploadFile. A retry after a failure on the network writes the same
// bytes again, which may keep one more version of them.
func (c *Client) WriteFile(ctx context.Context, owner, path string, content io.ReaderAt, size int64, opts *WriteOptions) (*File, error) {
	p, err := filePath(owner, path)
	if err != nil {
		return nil, err
	}
	if content == nil {
		return nil, errors.New("storage: WriteFile has no content")
	}
	r := &latere.Request{Method: http.MethodPut, Path: p, Content: content, ContentLength: size}
	if opts != nil {
		r.ContentType = opts.ContentType
		r.Header = http.Header{}
		if opts.IfMatch != "" {
			r.Header.Set("If-Match", strconv.Quote(opts.IfMatch))
		}
		if opts.IfNoneMatch {
			r.Header.Set("If-None-Match", "*")
		}
	}
	var out File
	if err := c.cfg.Do(ctx, r, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// A FileInfo is what a file's headers say about it, without its bytes.
type FileInfo struct {
	// Size is the file's length in bytes.
	Size int64
	// ContentType is the media type the file was written with.
	ContentType string
	// Checksum is the file's checksum, as File's Checksum names it.
	Checksum string
	// Modified is when the file's current bytes were written.
	Modified time.Time
}

// StatFile returns what the file at path in owner's space is, without
// reading its bytes. A path that holds nothing returns ErrNotFound.
func (c *Client) StatFile(ctx context.Context, owner, path string) (*FileInfo, error) {
	p, err := filePath(owner, path)
	if err != nil {
		return nil, err
	}
	resp, err := c.cfg.Open(ctx, &latere.Request{Method: http.MethodHead, Path: p})
	if err != nil {
		return nil, err
	}
	// A HEAD answer has no body to lose.
	_ = resp.Body.Close()
	return infoOf(resp), nil
}

// infoOf reads a file's headers.
func infoOf(resp *http.Response) *FileInfo {
	info := &FileInfo{
		Size:        resp.ContentLength,
		ContentType: resp.Header.Get("Content-Type"),
		Checksum:    strings.Trim(resp.Header.Get("ETag"), `"`),
	}
	if t, err := http.ParseTime(resp.Header.Get("Last-Modified")); err == nil {
		info.Modified = t
	}
	return info
}

// A FileReader is a file's bytes as ReadFile opens them. Read them, then
// Close it; a reader left open holds a connection.
type FileReader struct {
	body io.ReadCloser
	info *FileInfo
}

// Read reads the next bytes of the file.
func (r *FileReader) Read(p []byte) (int, error) { return r.body.Read(p) }

// Close ends the read and releases its connection.
func (r *FileReader) Close() error { return r.body.Close() }

// Size returns the file's length in bytes, or -1 when the answer did not
// say.
func (r *FileReader) Size() int64 { return r.info.Size }

// ContentType returns the media type the file was written with.
func (r *FileReader) ContentType() string { return r.info.ContentType }

// ReadFile opens the bytes of the file at path in owner's space. A file
// larger than MaxWriteBytes, or one a public link marked, is served by
// object storage, which ReadFile follows to; your credential is not sent
// there. A path that holds nothing returns ErrNotFound. The caller closes
// the reader.
func (c *Client) ReadFile(ctx context.Context, owner, path string) (*FileReader, error) {
	p, err := filePath(owner, path)
	if err != nil {
		return nil, err
	}
	resp, err := c.cfg.Open(ctx, &latere.Request{Method: http.MethodGet, Path: p, Header: http.Header{"Accept": {"*/*"}}})
	if err != nil {
		return nil, err
	}
	return &FileReader{body: resp.Body, info: infoOf(resp)}, nil
}

// DownloadURLOptions are the choices of a download link. A nil
// *DownloadURLOptions asks for a link that serves the file as it is.
type DownloadURLOptions struct {
	// Attachment has the link save the file under its own name, rather
	// than show it, when a browser opens it.
	Attachment bool
}

// GetDownloadURL returns a short-lived link that serves the bytes of the
// file at path in owner's space without a credential, for a browser or
// another program to read. A link to a file lasts five minutes; a public
// file's does not expire. A path that holds nothing returns ErrNotFound.
func (c *Client) GetDownloadURL(ctx context.Context, owner, path string, opts *DownloadURLOptions) (string, error) {
	p, err := filePath(owner, path)
	if err != nil {
		return "", err
	}
	q := url.Values{"inline": {"0"}}
	if opts != nil && opts.Attachment {
		q.Set("download", "1")
	}
	resp, err := c.cfg.Open(ctx, &latere.Request{Method: http.MethodGet, Path: p, Query: q, KeepRedirect: true})
	if err != nil {
		return "", err
	}
	// The link is in the headers; the body of a redirect says nothing more.
	_ = resp.Body.Close()
	loc := resp.Header.Get("Location")
	if resp.StatusCode/100 != 3 || loc == "" {
		return "", fmt.Errorf("storage: the API answered %d with no link to %s", resp.StatusCode, path)
	}
	u, err := url.Parse(loc)
	if err != nil {
		return "", fmt.Errorf("storage: the link the API answered is not an address: %w", err)
	}
	if !u.IsAbs() {
		base, err := url.Parse(c.cfg.BaseURL())
		if err != nil {
			return "", fmt.Errorf("storage: the base URL is not an address: %w", err)
		}
		u = base.ResolveReference(u)
	}
	return u.String(), nil
}

// ListFilesParams narrow a listing. A nil *ListFilesParams lists every
// file under the folder, a page of 100 at a time.
type ListFilesParams struct {
	// Query keeps the files whose name contains this text, ignoring case.
	Query string
	// Recent lists the newest files first, in one page.
	Recent bool
	// Limit is the page size: 100 when zero, at most 1000.
	Limit int
	// Cursor asks ListFilesPage for the page after the one that returned
	// it. ListFiles ignores it.
	Cursor string
}

func (p *ListFilesParams) query(cursor string) url.Values {
	q := url.Values{"list": {"1"}}
	if p != nil {
		if p.Query != "" {
			q.Set("q", p.Query)
		}
		if p.Recent {
			q.Set("recent", "1")
		}
		if p.Limit > 0 {
			q.Set("limit", strconv.Itoa(p.Limit))
		}
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	return q
}

// A FilesPage is one page of a folder's listing.
type FilesPage struct {
	// Files are the page's files: every file under the folder, at any
	// depth, not only those directly in it.
	Files []*File `json:"entries"`
	// Folders are the folders the page's files imply, as paths.
	Folders []string `json:"prefixes"`
	// Usage is what the whole space holds, answered for a listing of
	// "files", the top of the space; nil otherwise.
	Usage *Usage `json:"space,omitempty"`
	// NextCursor asks for the next page; empty on the last.
	NextCursor string `json:"next_cursor,omitempty"`
}

// Usage is what a space holds.
type Usage struct {
	// Bytes is the size of every file in the space, versions included.
	Bytes int64 `json:"bytes"`
	// Files is how many files the space holds.
	Files int64 `json:"files"`
}

// ListFiles returns an iterator over every file under the folder at path
// in owner's space, at any depth, fetching pages as the loop asks for
// more. params may be nil.
func (c *Client) ListFiles(ctx context.Context, owner, path string, params *ListFilesParams) iter.Seq2[*File, error] {
	return latere.Items(ctx, func(ctx context.Context, cursor string) (*latere.Page[*File], error) {
		page, err := c.listPage(ctx, owner, path, params, cursor)
		if err != nil {
			return nil, err
		}
		return &latere.Page[*File]{Items: page.Files, NextCursor: page.NextCursor}, nil
	})
}

// ListFilesPage returns one page of the folder at path in owner's space,
// starting at params.Cursor, with the folders it implies.
func (c *Client) ListFilesPage(ctx context.Context, owner, path string, params *ListFilesParams) (*FilesPage, error) {
	cursor := ""
	if params != nil {
		cursor = params.Cursor
	}
	return c.listPage(ctx, owner, path, params, cursor)
}

func (c *Client) listPage(ctx context.Context, owner, path string, params *ListFilesParams, cursor string) (*FilesPage, error) {
	p, err := filePath(owner, path)
	if err != nil {
		return nil, err
	}
	var page FilesPage
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodGet, Path: p, Query: params.query(cursor)}, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// keep returns a copy of b, which a decoder may reuse after it returns.
func keep(b []byte) json.RawMessage {
	return append(json.RawMessage(nil), b...)
}
