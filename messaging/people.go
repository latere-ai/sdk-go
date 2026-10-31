// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package messaging

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"latere.ai/x/sdk-go/latere"
)

// ErrPersonNotFound matches a 404 with the code "person_not_found": no one
// holds the handle FindPerson was given. latere.ErrNotFound matches it too.
var ErrPersonNotFound = errors.New("messaging: no one holds that handle")

// Person is the person a handle names.
type Person struct {
	// ID is the person's id, the subject of their token, which Devices,
	// Directory, ClaimKeyPackages and IsMember take as person.
	ID string `json:"id"`
	// Handle is the handle as the person holds it.
	Handle string `json:"handle"`
	// DisplayName is the name the person shows, empty when they set none.
	DisplayName string `json:"display_name"`
}

// FindPerson finds the person who holds exactly handle, compared without
// case, to start a conversation with someone new from the handle they
// gave. There is no search and no listing: a handle no one holds is an
// error matching ErrPersonNotFound. The platform answers it ahead of the
// messaging service, on the same origin and with the same token.
func (c *Client) FindPerson(ctx context.Context, handle string) (*Person, error) {
	var out Person
	err := c.do(ctx, &latere.Request{Method: http.MethodGet, Path: basePath + "/people", Query: url.Values{"handle": {handle}}}, false, &out)
	if err != nil {
		var apiErr *latere.Error
		if errors.As(err, &apiErr) && apiErr.Code == "person_not_found" {
			return nil, &codeError{err: apiErr, sentinel: ErrPersonNotFound}
		}
		return nil, err
	}
	return &out, nil
}
