// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"time"

	"latere.ai/x/sdk-go/latere"
)

// An Upload is an upload in parts as StartUpload opens it: the parts to
// put, each to its own signed link, and how long the links last.
type Upload struct {
	// ID names the upload in CompleteUpload and AbortUpload.
	ID string `json:"id"`
	// Owner is the space the file goes into, written in full.
	Owner string `json:"owner"`
	// Path is where the file appears once the upload completes.
	Path string `json:"path"`
	// PartSize is the length of every part but the last, which holds the
	// rest of the file.
	PartSize int64 `json:"part_size"`
	// PartCount is how many parts the file is put in.
	PartCount int `json:"part_count"`
	// PartURLs holds one signed link per part, in order: part n, counting
	// from 1, is put to PartURLs[n-1].
	PartURLs []string `json:"part_urls"`
	// ExpiresAt is when the upload and its links lapse, 24 hours after it
	// opened.
	ExpiresAt time.Time `json:"expires_at"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the upload, including any
// field Upload does not model.
func (u *Upload) RawJSON() json.RawMessage { return u.raw }

// UnmarshalJSON decodes the upload and keeps the JSON for RawJSON.
func (u *Upload) UnmarshalJSON(b []byte) error {
	type plain Upload
	if err := json.Unmarshal(b, (*plain)(u)); err != nil {
		return err
	}
	u.raw = keep(b)
	return nil
}

// StartUploadParams describe the file an upload in parts writes.
type StartUploadParams struct {
	// Owner is the space the file goes into, such as Me. Required.
	Owner string `json:"owner"`
	// Path is where the file appears, starting with "files/". Required.
	Path string `json:"path"`
	// Size is the file's length in bytes, at most MaxFileBytes. Required.
	Size int64 `json:"size"`
	// ContentType is the file's media type, stored and served back.
	ContentType string `json:"content_type,omitempty"`
}

// StartUpload opens an upload in parts and returns it. It is sent once,
// since this part of the API takes no idempotency key; an upload a failure
// left open lapses by itself at its ExpiresAt. An answer this package
// cannot use is given up, and its error is an *UploadError naming it.
func (c *Client) StartUpload(ctx context.Context, params *StartUploadParams) (*Upload, error) {
	if params == nil || params.Owner == "" || params.Path == "" {
		return nil, errors.New("storage: StartUploadParams.Owner and Path are required")
	}
	if params.Size <= 0 {
		return nil, fmt.Errorf("storage: StartUploadParams.Size is %d; it is the file's length, above 0", params.Size)
	}
	var out Upload
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodPost, Path: basePath + "/uploads", Body: params, Retry: latere.RetryNever}, &out); err != nil {
		return nil, err
	}
	if out.PartCount != len(out.PartURLs) || out.PartCount == 0 {
		err := fmt.Errorf("storage: the upload names %d parts and %d links", out.PartCount, len(out.PartURLs))
		return nil, c.giveUp(ctx, out.ID, err)
	}
	return &out, nil
}

// An UploadError is an upload in parts that failed once it was open: the
// upload's id, and why. UploadFile gives such an upload up; one it could
// not give up lapses by itself at its ExpiresAt.
type UploadError struct {
	// ID names the upload, for AbortUpload.
	ID string
	// Err is why it failed.
	Err error
}

func (e *UploadError) Error() string { return "storage: upload " + e.ID + ": " + e.Err.Error() }

// Unwrap returns why the upload failed.
func (e *UploadError) Unwrap() error { return e.Err }

// giveUp aborts the upload named id after err and returns err as an
// *UploadError. The abort runs on a context of its own, since ctx may be
// what ended the upload; a give-up that fails costs nothing more, since
// the upload lapses at its ExpiresAt in any case, and the caller hears the
// failure that called for it.
func (c *Client) giveUp(ctx context.Context, id string, err error) error {
	if id == "" {
		return err
	}
	abort, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	_ = c.AbortUpload(abort, id)
	return &UploadError{ID: id, Err: err}
}

// A Part is one part of an upload once it is put: its number and the
// label object storage answered, which CompleteUpload names it by.
type Part struct {
	// N is the part's number, counting from 1.
	N int `json:"n"`
	// ETag is object storage's label for the part's bytes.
	ETag string `json:"etag"`
}

// PutPart puts part n of u, counting from 1: size bytes of content, which
// must be u.PartSize for every part but the last. It goes straight to
// object storage by the part's signed link, without your credential, and
// is retried after a failure on the network, since putting a part again
// replaces it. Each attempt has the Config's timeout and
// latere.TransferTime for the part's size, and an attempt cut off by that
// bound is sent again too.
func (c *Client) PutPart(ctx context.Context, u *Upload, n int, content io.ReaderAt, size int64) (Part, error) {
	if u == nil || n < 1 || n > len(u.PartURLs) {
		return Part{}, fmt.Errorf("storage: the upload has no part %d", n)
	}
	if content == nil {
		return Part{}, errors.New("storage: PutPart has no content")
	}
	resp, err := c.cfg.Open(ctx, &latere.Request{Method: http.MethodPut, SignedURL: u.PartURLs[n-1], Content: content, ContentLength: size})
	if err != nil {
		return Part{}, err
	}
	// Object storage answers a part with its label in the headers; its
	// body says nothing more.
	_ = resp.Body.Close()
	etag := resp.Header.Get("ETag")
	if etag == "" {
		return Part{}, fmt.Errorf("storage: object storage did not confirm part %d", n)
	}
	return Part{N: n, ETag: etag}, nil
}

// CompleteUploadParams are the parts of an upload to make into its file,
// and the conditions of the write, as WriteOptions has them.
type CompleteUploadParams struct {
	// Parts names every part, in any order. Required.
	Parts []Part
	// IfMatch completes only if the file at the path still holds the
	// bytes of this checksum; otherwise latere.ErrPreconditionFailed.
	IfMatch string
	// IfNoneMatch completes only if nothing is at the path yet; otherwise
	// latere.ErrPreconditionFailed.
	IfNoneMatch bool
}

// CompleteUpload makes the parts of the upload named id into its file and
// returns the file. The storage API keeps an upload until it completes, so
// a completion it answered with a server error is sent again, but one that
// failed on the network is not: if that one completed, the upload is
// gone, and a repeat would be refused with ErrNotFound though the file is
// there. StatFile on the upload's path says whether it completed.
func (c *Client) CompleteUpload(ctx context.Context, id string, params *CompleteUploadParams) (*File, error) {
	if id == "" {
		return nil, errors.New("storage: the upload's id is empty")
	}
	if params == nil || len(params.Parts) == 0 {
		return nil, errors.New("storage: CompleteUploadParams names no part")
	}
	sorted := slices.Clone(params.Parts)
	slices.SortFunc(sorted, func(a, b Part) int { return a.N - b.N })
	var out File
	r := &latere.Request{
		Method: http.MethodPost, Path: basePath + "/uploads/" + url.PathEscape(id) + "/complete",
		Body: map[string]any{"parts": sorted}, Header: http.Header{}, Retry: latere.RetryAnswers,
	}
	conditions(r.Header, params.IfMatch, params.IfNoneMatch)
	if err := c.cfg.Do(ctx, r, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AbortUpload gives up the upload named id, so Storage lets its parts go.
func (c *Client) AbortUpload(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("storage: the upload's id is empty")
	}
	return c.cfg.Do(ctx, &latere.Request{Method: http.MethodDelete, Path: basePath + "/uploads/" + url.PathEscape(id)}, nil)
}

// UploadOptions are the choices of UploadFile. A nil *UploadOptions
// uploads with the type "application/octet-stream", four parts at a time,
// and no condition.
type UploadOptions struct {
	// ContentType is the file's media type, stored and served back.
	ContentType string
	// Progress, when set, is called as the bytes go up, with the bytes
	// sent so far and the file's size. It is called from the HTTP
	// transport's goroutines that write the request bodies, one call at a
	// time, so it must return quickly. A part sent again after a failure
	// counts from its start again, so the count may go back.
	Progress func(sent, total int64)
	// Parallel is how many parts are put at once: 4 when zero.
	Parallel int
	// IfMatch and IfNoneMatch are the write's conditions, as WriteOptions
	// has them; a file in parts is held to them when it completes.
	IfMatch     string
	IfNoneMatch bool
}

// defaultParallel is how many parts UploadFile puts at once by default.
const defaultParallel = 4

// UploadFile writes size bytes of content to path in owner's space and
// returns the file: in one request when it is at most MaxWriteBytes, and
// in parts otherwise, put straight to object storage several at a time.
// When a part fails after its retries, the parts on their way are
// stopped; when a part or the completion fails, the upload is given up,
// and the error is an *UploadError naming it.
func (c *Client) UploadFile(ctx context.Context, owner, path string, content io.ReaderAt, size int64, opts *UploadOptions) (*File, error) {
	if content == nil {
		return nil, errors.New("storage: UploadFile has no content")
	}
	if owner == "" || path == "" {
		return nil, errNoPath
	}
	if opts == nil {
		opts = &UploadOptions{}
	}
	progress := newTally(size, opts.Progress)
	if size <= MaxWriteBytes {
		progress.report(0, 0)
		f, err := c.WriteFile(ctx, owner, path, progress.reader(0, content), size,
			&WriteOptions{ContentType: opts.ContentType, IfMatch: opts.IfMatch, IfNoneMatch: opts.IfNoneMatch})
		if err != nil {
			return nil, err
		}
		progress.report(0, size)
		return f, nil
	}
	u, err := c.StartUpload(ctx, &StartUploadParams{Owner: owner, Path: path, Size: size, ContentType: opts.ContentType})
	if err != nil {
		return nil, err
	}
	parts, err := c.putParts(ctx, u, content, size, opts.Parallel, progress)
	if err != nil {
		return nil, c.giveUp(ctx, u.ID, err)
	}
	f, err := c.CompleteUpload(ctx, u.ID, &CompleteUploadParams{Parts: parts, IfMatch: opts.IfMatch, IfNoneMatch: opts.IfNoneMatch})
	if err != nil {
		return nil, c.giveUp(ctx, u.ID, err)
	}
	return f, nil
}

// putParts puts every part of u, parallel at a time, and returns them in
// order. The first part that fails stops the rest.
func (c *Client) putParts(ctx context.Context, u *Upload, content io.ReaderAt, size int64, parallel int, progress *tally) ([]Part, error) {
	if parallel <= 0 {
		parallel = defaultParallel
	}
	// The parts must cover the file exactly, the last one holding the
	// rest, or the file Storage makes of them is not the one given.
	if u.PartSize <= 0 || int64(u.PartCount-1)*u.PartSize >= size || int64(u.PartCount)*u.PartSize < size {
		return nil, fmt.Errorf("storage: the upload has %d parts of %d bytes, which do not fit a file of %d bytes", u.PartCount, u.PartSize, size)
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	parts := make([]Part, u.PartCount)
	slots := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	for i := range u.PartCount {
		off := int64(i) * u.PartSize
		n := min(u.PartSize, size-off)
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return nil, context.Cause(ctx)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			part, err := c.PutPart(ctx, u, i+1, progress.reader(i, io.NewSectionReader(content, off, n)), n)
			if err != nil {
				cancel(err)
				return
			}
			parts[i] = part
		}()
	}
	wg.Wait()
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	return parts, nil
}

// A tally counts the bytes of an upload as they are read to be sent, per
// part, and reports the sum.
type tally struct {
	mu     sync.Mutex
	total  int64
	sent   map[int]int64
	report func(part int, sent int64)
}

func newTally(total int64, progress func(sent, total int64)) *tally {
	t := &tally{total: total, sent: map[int]int64{}}
	t.report = func(part int, sent int64) {
		if progress == nil {
			return
		}
		t.mu.Lock()
		defer t.mu.Unlock()
		t.sent[part] = sent
		var sum int64
		for _, s := range t.sent {
			sum += s
		}
		progress(sum, t.total)
	}
	return t
}

// reader wraps one part's content so reading it reports how far the part
// has come.
func (t *tally) reader(part int, r io.ReaderAt) io.ReaderAt {
	return &counting{r: r, part: part, report: t.report}
}

// counting reports the end of each read of its content, which the HTTP
// client reads in order from the start for each attempt.
type counting struct {
	r      io.ReaderAt
	part   int
	report func(part int, sent int64)
}

func (c *counting) ReadAt(p []byte, off int64) (int, error) {
	n, err := c.r.ReadAt(p, off)
	if n > 0 {
		c.report(c.part, off+int64(n))
	}
	return n, err
}
