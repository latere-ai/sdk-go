// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"latere.ai/x/sdk-go/latere"
)

// ImageWidth is the most pixels wide the platform answers a picture of
// the web at: one of the widths it encodes pictures within.
type ImageWidth int

// The widths a picture of the web is answered within. A picture narrower
// than the width asked is answered at its own.
const (
	// ImageSmall is at most 800 pixels wide, for a picture drawn at up to
	// 400 points on a Retina display, as in a column of text.
	ImageSmall ImageWidth = 800
	// ImageLarge is at most 1600 pixels wide, for a picture opened on its
	// own.
	ImageLarge ImageWidth = 1600
)

// MaxImageAddress is the longest address of a picture of the web, in
// bytes, the platform reads; a longer one is refused before any request.
const MaxImageAddress = 2048

// ErrNoImage matches a refusal that says the address holds no picture the
// platform shows: an address that is no public website's, one that
// answered no image, an image in a format the platform does not show,
// such as SVG, or too large to, and a website it could not reach. The
// error wraps the *latere.Error the API answered, whose Code tells which;
// asking again answers the same, but for a website that could not be
// reached, which may answer later.
var ErrNoImage = errors.New("web: the platform shows no picture at that address")

// noImageCodes are the API's codes for the refusals ErrNoImage matches.
var noImageCodes = map[string]bool{
	"invalid_url":       true,
	"image_not_found":   true,
	"image_unsupported": true,
	"image_too_large":   true,
	"image_unreachable": true,
}

// An ImageReader is a picture of the web as the platform answered it:
// its bytes, encoded again by the platform, never the website's own, with
// their media type and length. Read it, then Close it; a reader left open
// holds a connection.
type ImageReader struct {
	body         io.ReadCloser
	mediaType    string
	size         int64
	cacheControl string
}

// Read reads the next bytes of the picture. A picture whose answer was
// cut off before the length it named ends with io.ErrUnexpectedEOF.
func (r *ImageReader) Read(p []byte) (int, error) { return r.body.Read(p) }

// Close ends the read and releases its connection.
func (r *ImageReader) Close() error { return r.body.Close() }

// MediaType returns the picture's media type, "image/jpeg" or
// "image/png".
func (r *ImageReader) MediaType() string { return r.mediaType }

// Size returns the picture's length in bytes, or -1 when the answer did
// not say.
func (r *ImageReader) Size() int64 { return r.size }

// CacheControl returns the answer's Cache-Control, which says how long
// the picture may be kept and by whom: "private, max-age=86400", a day by
// the reader alone. A program that passes the picture on to a browser
// sends it with the picture.
func (r *ImageReader) CacheControl() string { return r.cacheControl }

// ReadImage opens the picture at address, a picture on a public website
// an answer shows, such as a Markdown image's address, through the
// platform at no more than width pixels wide; the caller closes the
// reader. The platform fetches the picture on its own outbound path, so
// the website learns the platform's address and never the reader's, and
// answers it encoded again, of at most 2 MiB: as a JPEG when it is opaque
// and as a PNG otherwise.
//
// The call is made as the person or key the Config's credentials speak
// for, and a key whose access was narrowed must be one that reads
// sessions. An address the platform shows no picture for returns an
// error matching ErrNoImage. The platform answers each person at most 300
// pictures a minute; past that the call returns an error matching
// latere.ErrRateLimited once its retries are spent. The Config's timeout
// bounds the wait for the answer, and after it only ctx bounds the read.
func (c *Client) ReadImage(ctx context.Context, address string, width ImageWidth) (*ImageReader, error) {
	if err := checkImage("ReadImage", address, width); err != nil {
		return nil, err
	}
	return c.openImage(ctx, "/v1/web/images", address, width)
}

// ReadSharedImage opens a picture of the web that an answer of a shared
// conversation shows, by the share link's token, as ReadImage opens one
// for a person; the caller closes the reader. It is for the reader of a
// share, who may hold no credential: the platform reads only an address a
// Markdown image of a kept answer within the link's bound names, so a
// link is no way to fetch any other.
//
// A share anyone may read reads no credential, so a Config built with
// latere.WithoutCredentials reads it; a share for the members of an
// organization answers a member's token alone. A token that is no link's,
// a link that was stopped or that the reader may not read, and an address
// the shared part does not name all return an error matching
// latere.ErrNotFound; an address the platform shows no picture for
// returns one matching ErrNoImage.
func (c *Client) ReadSharedImage(ctx context.Context, token, address string, width ImageWidth) (*ImageReader, error) {
	if token == "" {
		return nil, errors.New("web: ReadSharedImage: the share's token is empty")
	}
	if err := checkImage("ReadSharedImage", address, width); err != nil {
		return nil, err
	}
	return c.openImage(ctx, "/v1/shares/"+url.PathEscape(token)+"/images", address, width)
}

// checkImage refuses, before any request, an address or a width the
// platform would refuse.
func checkImage(call, address string, width ImageWidth) error {
	if width != ImageSmall && width != ImageLarge {
		return fmt.Errorf("web: %s: the width %d is not ImageSmall or ImageLarge", call, width)
	}
	if len(address) > MaxImageAddress {
		return fmt.Errorf("web: %s: the address is %d bytes, longer than MaxImageAddress", call, len(address))
	}
	u, err := url.Parse(address)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		// The address is not quoted: a person reads the picture it names,
		// which is theirs to keep out of a log.
		return fmt.Errorf("web: %s: the address is not an http or https address of a picture", call)
	}
	return nil
}

// openImage reads the picture at address through path.
func (c *Client) openImage(ctx context.Context, path, address string, width ImageWidth) (*ImageReader, error) {
	resp, err := c.cfg.Open(ctx, &latere.Request{
		Method: http.MethodGet,
		Path:   path,
		Query:  url.Values{"url": {address}, "width": {strconv.Itoa(int(width))}},
		Header: http.Header{"Accept": {"image/*"}},
	})
	var apiErr *latere.Error
	switch {
	case errors.As(err, &apiErr) && noImageCodes[apiErr.Code]:
		return nil, &codeError{err: apiErr, sentinel: ErrNoImage}
	case err != nil:
		return nil, err
	}
	media, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(media, "image/") {
		// The answer is no picture and is not read; closing it loses
		// nothing a caller could use.
		_ = resp.Body.Close()
		return nil, fmt.Errorf("web: the answer is %q, not a picture", resp.Header.Get("Content-Type"))
	}
	return &ImageReader{body: resp.Body, mediaType: media, size: resp.ContentLength, cacheControl: resp.Header.Get("Cache-Control")}, nil
}
