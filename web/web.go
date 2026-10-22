// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package web reads what the Latere platform fetches from the web for a
// program that shows an answer, so that the program loads nothing from a
// third party: a site's icon, to draw beside a link to it.
//
// The platform reads the icon from the site on its own outbound path, so
// the site learns the platform's address and never the reader's, and
// answers it encoded again as a small PNG, never the site's own bytes. An
// icon is the site's and no one's: the route reads no credential, and
// GetIcon sends none. A Config is built with credentials all the same; a
// program that holds none gives latere.WithTokenSource a source of its
// own, which GetIcon never calls.
//
//	cfg, err := latere.NewConfig() // the key in LATERE_KEY
//	if err != nil {
//		return err
//	}
//	icon, err := web.New(cfg).GetIcon(ctx, "techcrunch.com", web.IconSmall)
//	if err != nil {
//		return err
//	}
//	if icon == nil {
//		return nil // the site has no icon; draw the link without one
//	}
//	draw(icon.Data, icon.MediaType)
//
// Pictures of the web an answer shows, which the platform reads as the
// person, are not covered yet; call them with latere.Config.Open.
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

// Client reaches the web part of the Latere API. It is safe for
// concurrent use.
type Client struct {
	cfg *latere.Config
}

// New returns a Client that sends its requests as cfg says. A cfg not
// built with latere.NewConfig makes every call fail with an error saying
// so.
func New(cfg *latere.Config) *Client { return &Client{cfg: cfg} }

// IconSize is the side of a square icon in pixels: one of the sizes the
// platform encodes icons at.
type IconSize int

// The sizes an icon is answered at.
const (
	// IconSmall is 32 pixels a side, for an icon drawn at up to 16 points
	// on a Retina display, as beside a link in running text.
	IconSmall IconSize = 32
	// IconLarge is 64 pixels a side, for an icon drawn at up to 32 points.
	IconLarge IconSize = 64
)

// MaxIconBytes is the largest icon GetIcon reads; a longer answer is
// refused with an error matching ErrIconTooLarge, never cut short. The
// platform answers a PNG of at most 64 pixels a side, about 16 KiB even
// when nothing in it compresses.
const MaxIconBytes = 64 << 10

// Errors of GetIcon to test with errors.Is.
var (
	// ErrInvalidHost matches a host the platform reads no icon for: an IP
	// address, localhost, a single name, a name with a port, or a name
	// under a suffix that is no public website's, such as local or test.
	// Such a host has no icon either, and asking again answers the same.
	// The error wraps the *latere.Error the API answered with 400, which
	// errors.As still finds.
	ErrInvalidHost = errors.New("web: the platform reads no icon for that host")
	// ErrIconTooLarge matches an answer past MaxIconBytes, or one that
	// says it is.
	ErrIconTooLarge = errors.New("web: the icon is larger than an icon the platform answers")
)

// Icon is a site's icon as the platform answered it.
type Icon struct {
	// Data is the icon's bytes, a square picture of the size asked.
	Data []byte
	// MediaType is the type the API answered the bytes as, "image/png"
	// for every icon it encodes.
	MediaType string
}

// GetIcon reads the icon of host, a site's name such as "techcrunch.com",
// at size, IconSmall or IconLarge. It answers nil and no error when the
// site has no icon the platform can read: none in its page and none at
// its /favicon.ico, or one in a form it does not read, such as SVG. The
// icon of a subdomain is its own: "docs.python.org" and "python.org" are
// two sites.
//
// host is a name alone, without a scheme or a path: a link's host with
// "www." taken off, as Latere Chat names a site. The platform reads it in
// lower case and in its ASCII form. A host it reads no icon for returns
// an error matching ErrInvalidHost.
//
// The request carries no credential, since the route reads none, so it is
// sent whatever the Config's credentials are. The platform keeps an icon
// for a week and a site without one for a day, so a program that keeps
// what it read asks again no sooner. It answers each client address a
// bounded number of icons a minute, and past them the call returns an
// error matching latere.ErrRateLimited once its retries are spent. A site
// the platform has not read yet, asked for while its reads of the web are
// spent, is answered 503 with a Retry-After of a few seconds, which each
// retry waits out: a program that draws many icons at once bounds ctx, or
// builds its Config with fewer retries, rather than hold a link's drawing
// that long. The Config's timeout bounds the wait for the answer, and ctx
// alone the read of its bytes.
func (c *Client) GetIcon(ctx context.Context, host string, size IconSize) (*Icon, error) {
	// A name of dots alone would read as a step up the route's path.
	if strings.Trim(host, ".") == "" {
		return nil, fmt.Errorf("web: GetIcon: %q is not a host, such as techcrunch.com", host)
	}
	if size != IconSmall && size != IconLarge {
		return nil, fmt.Errorf("web: GetIcon: the size %d is not IconSmall or IconLarge", size)
	}
	req := &latere.Request{
		Method:    http.MethodGet,
		Path:      "/v1/web/icons/" + url.PathEscape(host),
		Query:     url.Values{"size": {strconv.Itoa(int(size))}},
		Header:    http.Header{"Accept": {"image/*"}},
		Anonymous: true,
	}
	resp, err := c.cfg.Open(ctx, req)
	var apiErr *latere.Error
	switch {
	case errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound && apiErr.Code == "icon_not_found":
		// The platform's own answer that the site has no icon; a 404
		// without its envelope, such as a proxy's, is still an error.
		return nil, nil
	case errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusBadRequest && apiErr.Code == "invalid_host":
		return nil, &codeError{err: apiErr, sentinel: ErrInvalidHost}
	case err != nil:
		return nil, err
	}
	defer func() {
		// The bytes were read or the call failed already; a failure to
		// close the body changes neither.
		_ = resp.Body.Close()
	}()
	// The platform encodes every icon as a picture of pixels; a type that
	// is a program, as SVG is, is never answered as one.
	media, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(media, "image/") || media == "image/svg+xml" {
		return nil, fmt.Errorf("web: GetIcon: the answer is %q, not a picture", resp.Header.Get("Content-Type"))
	}
	if resp.ContentLength > MaxIconBytes {
		return nil, fmt.Errorf("web: GetIcon: the answer is %d bytes: %w", resp.ContentLength, ErrIconTooLarge)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxIconBytes+1))
	if err != nil {
		return nil, fmt.Errorf("web: GetIcon: reading the icon: %w", err)
	}
	if len(data) > MaxIconBytes {
		return nil, fmt.Errorf("web: GetIcon: the answer is past %d bytes: %w", MaxIconBytes, ErrIconTooLarge)
	}
	if len(data) == 0 {
		return nil, errors.New("web: GetIcon: the answer has no bytes")
	}
	return &Icon{Data: data, MediaType: media}, nil
}

// codeError is an *latere.Error that also matches the sentinel of its
// API code, for a refusal a caller branches on whose status alone does
// not tell it apart: a 400 may refuse any field.
type codeError struct {
	err      *latere.Error
	sentinel error
}

func (e *codeError) Error() string { return e.err.Error() }

// Is reports whether target is the sentinel of the error's code; the
// *latere.Error it unwraps to is found by errors.As.
func (e *codeError) Is(target error) bool { return target == e.sentinel }

// Unwrap returns the API's answer.
func (e *codeError) Unwrap() error { return e.err }
