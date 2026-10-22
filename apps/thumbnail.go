// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package apps

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"latere.ai/x/sdk-go/latere"
)

// ThumbnailWidth is the width a deploy's picture is read at, in pixels:
// one of the two the API encodes it at.
type ThumbnailWidth int

// Thumbnail widths.
const (
	// ThumbnailLarge is 1280 by 800 pixels, the picture the API answers
	// when no width is asked.
	ThumbnailLarge ThumbnailWidth = 1280
	// ThumbnailSmall is 640 by 400 pixels, for a card or a list.
	ThumbnailSmall ThumbnailWidth = 640
)

// Deploy returns the app's slug and the deploy's ID that a Thumbnail's
// URL names, which ReadThumbnail takes, and false for a URL that is not
// the path of a deploy's picture. The slug is the one the app had when
// the API answered; ReadThumbnail also takes the app's ID in its place.
func (t Thumbnail) Deploy() (slug, deployID string, ok bool) {
	path, _, _ := strings.Cut(t.URL, "?")
	segs := strings.Split(strings.TrimPrefix(path, "/"), "/")
	n := len(segs)
	if n < 4 || segs[n-1] != "thumbnail" || segs[n-3] != "deploys" {
		return "", "", false
	}
	slug, err := url.PathUnescape(segs[n-4])
	if err != nil || slug == "" {
		return "", "", false
	}
	deployID, err = url.PathUnescape(segs[n-2])
	if err != nil || deployID == "" {
		return "", "", false
	}
	return slug, deployID, true
}

// A ThumbnailReader is a deploy's picture as ReadThumbnail opens it: its
// bytes, its media type, its length and how a cache may keep it, so a
// program can pass it on to a browser as it arrives. Read it, then Close
// it; a reader left open holds a connection.
type ThumbnailReader struct {
	body         io.ReadCloser
	mediaType    string
	size         int64
	etag         string
	cacheControl string
}

// Read reads the next bytes of the picture. A picture whose answer was cut
// off before the length it named ends with io.ErrUnexpectedEOF.
func (r *ThumbnailReader) Read(p []byte) (int, error) { return r.body.Read(p) }

// Close ends the read and releases its connection.
func (r *ThumbnailReader) Close() error { return r.body.Close() }

// MediaType returns the media type the API answered the picture as, from
// the answer's Content-Type: "image/webp" for every picture it takes.
func (r *ThumbnailReader) MediaType() string { return r.mediaType }

// Size returns the picture's length in bytes, or -1 when the answer did
// not say.
func (r *ThumbnailReader) Size() int64 { return r.size }

// ETag returns the answer's entity tag, which names when the picture was
// taken: a picture taken again answers another at the same address.
func (r *ThumbnailReader) ETag() string { return r.etag }

// CacheControl returns how the API says the picture may be kept, from the
// answer's Cache-Control: "private, max-age=86400" for a read with a
// credential, and "public, max-age=3600" for one without, so a site made
// private stops being served from a shared cache within the hour. A
// program that passes the picture on keeps it.
func (r *ThumbnailReader) CacheControl() string { return r.cacheControl }

// thumbnailCodes are the API's codes for the refusals of a picture's read
// that have a sentinel.
var thumbnailCodes = map[string]error{"thumbnail_not_found": ErrNoThumbnail}

// ReadThumbnail opens the picture of one deploy of the app slug names: a
// screenshot of its front page, as WebP, at width, ThumbnailLarge or
// ThumbnailSmall, or ThumbnailLarge when width is zero. slug may also be
// the app's ID, and deployID is the deploy's ID in full; a Thumbnail's
// Deploy returns both from its URL. The caller closes the reader.
//
// With a credential it is any read of the app, so the owner reads the
// picture of a private or unpublished app. With a Config that sends no
// credential it answers only while the address the deploy serves is
// public or unlisted and the app is published, and such reads are limited
// to 120 a minute from one address, past which it returns an error
// matching latere.ErrRateLimited once its retries are spent.
//
// An app the caller may not read, or without a credential one that is
// private or unpublished, returns an error matching latere.ErrNotFound. A
// deploy that is not the app's, or has no picture yet, returns one that
// also matches ErrNoThumbnail. The Config's timeout bounds the wait for
// the answer, and after it only ctx bounds the read.
func (c *Client) ReadThumbnail(ctx context.Context, slug, deployID string, width ThumbnailWidth) (*ThumbnailReader, error) {
	if slug == "" {
		return nil, errNoSlug
	}
	if deployID == "" {
		return nil, errNoDeploy
	}
	q := url.Values{}
	switch width {
	case 0:
	case ThumbnailLarge, ThumbnailSmall:
		q.Set("width", strconv.Itoa(int(width)))
	default:
		return nil, fmt.Errorf("apps: ReadThumbnail: the width %d is not ThumbnailLarge or ThumbnailSmall", width)
	}
	req := &latere.Request{
		Method: http.MethodGet, Path: appPath(slug, "deploys", deployID, "thumbnail"), Query: q,
		// The picture's bytes, which the API answers a refusal to as JSON
		// all the same.
		Header: http.Header{"Accept": {"*/*"}},
	}
	resp, err := c.cfg.Open(ctx, req)
	if err != nil {
		return nil, withSentinel(err, thumbnailCodes)
	}
	return &ThumbnailReader{
		body: resp.Body, mediaType: resp.Header.Get("Content-Type"), size: resp.ContentLength,
		etag: resp.Header.Get("ETag"), cacheControl: resp.Header.Get("Cache-Control"),
	}, nil
}
