// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package apps

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"latere.ai/x/sdk-go/latere"
)

// FramePass is the address a frame points at to show a private app in a
// page of another site, to the person signed in to that page, without a
// second sign-in.
type FramePass struct {
	// URL is the frame's address: on the host of the address asked for,
	// with a pass good once, for that host. Use it once, as it is, and ask
	// again each time the frame is drawn.
	URL string `json:"url"`
	// ExpiresAt is when the pass in URL stops being redeemable, 60 seconds
	// after it was handed out.
	ExpiresAt time.Time `json:"expires_at"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the pass, with every
// field; nil for a FramePass the program built itself.
func (f *FramePass) RawJSON() json.RawMessage { return f.raw }

// UnmarshalJSON decodes the pass and keeps the JSON for RawJSON.
func (f *FramePass) UnmarshalJSON(b []byte) error {
	type plain FramePass
	if err := json.Unmarshal(b, (*plain)(f)); err != nil {
		return err
	}
	f.raw = keep(b)
	return nil
}

// frameCodes are the API's codes for the refusals of a frame pass that
// have a sentinel.
var frameCodes = map[string]error{"frame_unavailable": ErrFrameUnavailable}

// CreateFramePass hands out the address to point a frame at so that a
// page of another site shows the private app slug names, at address, to
// the person whose token the Config carries. address is the https address
// the frame is about to show, on one of the app's hosts, with the path,
// query and fragment it opens at; slug may also be the app's ID.
//
// A private app's own sign-in is a page no frame of another site can
// show; the pass stands in for it. It is redeemed once, by a frame that a
// page of another site draws, within 60 seconds, for a cookie that only a
// frame carries and that lasts an hour. Ask again on every mount and
// reload of the frame. A frame that cannot show the app shows a page that
// offers it in a new tab and posts the page that frames it one message,
// {"type": "latere:frame", "state": "expired"} or "refused": on
// "expired", ask once more and draw the new URL; on "refused", the
// browser keeps no cookie for the frame, and a new pass ends the same
// way. End what the passes made when the person signs out, with
// EndGateSessions.
//
// A person's token is allowed for an app of their own account, and an
// organization member's for the organization's app; an agent's session
// token and a key whose grants do not name gate.access are refused with
// an error matching latere.ErrForbidden. An address that is not https on
// one of the app's hosts is refused with 400 and the code
// "invalid_request". An app the caller may not read returns an error
// matching latere.ErrNotFound, and an installation that names no page
// that may frame a private app one that also matches ErrFrameUnavailable.
// The caller may ask 20 times a minute for one app.
//
// A pass that is never used lapses, so CreateFramePass is sent again
// after a failure like a read: a repeat hands out a second pass, and the
// first one, if it was made, is never redeemed.
func (c *Client) CreateFramePass(ctx context.Context, slug, address string) (*FramePass, error) {
	if slug == "" {
		return nil, errNoSlug
	}
	if address == "" {
		return nil, errors.New("apps: CreateFramePass: the address is empty")
	}
	var out FramePass
	req := &latere.Request{Method: http.MethodPost, Path: appPath(slug, "frame"), Body: map[string]string{"address": address}, Retry: latere.RetryAlways}
	if err := c.cfg.Do(ctx, req, &out); err != nil {
		return nil, withSentinel(err, frameCodes)
	}
	return &out, nil
}

// EndGateSessions ends every sign-in the person whose token the Config
// carries has at private apps, in every account and on every browser: the
// ones frame passes made and the ones they made at an app's own sign-in.
// A program that shows private apps in frames calls it when the person
// signs out of it, before it drops their token: nothing else tells the
// apps of that sign-out. An app's host refuses each ended sign-in at its
// next check, within 60 seconds by default and at most 600. It succeeds
// whether or not a sign-in existed.
//
// An agent's session token and a key whose grants do not name
// gate.access for every app are refused with an error matching
// latere.ErrForbidden. The caller may end their sign-ins 30 times a
// minute. When the API's authorizer gives no answer, nothing is ended and
// the call fails with 503 once its retries are spent; call it again.
func (c *Client) EndGateSessions(ctx context.Context) error {
	return c.cfg.Do(ctx, &latere.Request{Method: http.MethodDelete, Path: basePath + "/gate/sessions"}, nil)
}
