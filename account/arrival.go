// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package account

import (
	"context"
	"errors"
	"net/http"

	"latere.ai/x/sdk-go/latere"
)

// RecordArrivalParams are what brought a person to a product where they
// signed in. Either may be empty, and a value of another form than its
// own is dropped by the API rather than refused.
type RecordArrivalParams struct {
	// SourceHost is the host name of the website that referred the person,
	// such as "news.ycombinator.com", at most 253 bytes.
	SourceHost string `json:"source_host,omitempty"`
	// LandingPath is the path on the product they first landed on, such as
	// "/blog/why-latere", with no query and no fragment, at most 200
	// bytes.
	LandingPath string `json:"landing_path,omitempty"`
}

// RecordArrival records the website that referred the signed-in person and
// the page they first landed on, once: the person's record is written only
// when they have none, so the first sign-in that carried a source is the
// one kept, and the call succeeds whatever was kept. The platform shows it
// only when it was written within ten minutes of the account's creation.
// A call that names neither fails before any request.
//
// Only a person's own sign-in records an arrival: a hosted agent's, a
// service account's and a key's token are refused with 403.
func (c *Client) RecordArrival(ctx context.Context, params *RecordArrivalParams) error {
	if params == nil || (params.SourceHost == "" && params.LandingPath == "") {
		return errors.New("account: RecordArrival names neither a source nor a landing page")
	}
	return c.cfg.Do(ctx, &latere.Request{Method: http.MethodPut, Path: "/v1/account/arrival", Body: params}, nil)
}
