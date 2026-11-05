// SPDX-FileCopyrightText: 2026 Latere AI
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
// holds the handle FindPerson was given, or the id Person was given is not
// a person's. latere.ErrNotFound matches it too.
var ErrPersonNotFound = errors.New("messaging: person not found")

// Person is the person a handle or an id names.
type Person struct {
	// ID is the person's id, the subject of their token, which Devices,
	// Directory, ClaimKeyPackages and IsMember take as person.
	ID string `json:"id"`
	// Handle is the handle as the person holds it, empty when a person
	// found by id has not chosen one.
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
	return c.findPerson(ctx, url.Values{"handle": {handle}})
}

// Person answers the person whose id is id, such as one of a
// conversation's people, to show who they are. An id that is not a
// person's, including one whose account was closed, is an error matching
// ErrPersonNotFound, and an id that is not a UUID is a 400 with the code
// "person_query_invalid".
// Like FindPerson, the platform answers it ahead of the messaging service,
// with the same token.
func (c *Client) Person(ctx context.Context, id string) (*Person, error) {
	return c.findPerson(ctx, url.Values{"id": {id}})
}

// findPerson asks the platform's lookup with one handle or one id.
func (c *Client) findPerson(ctx context.Context, query url.Values) (*Person, error) {
	var out Person
	err := c.do(ctx, &latere.Request{Method: http.MethodGet, Path: basePath + "/people", Query: query}, false, &out)
	if err != nil {
		var apiErr *latere.Error
		if errors.As(err, &apiErr) && apiErr.Code == "person_not_found" {
			return nil, &codeError{err: apiErr, sentinel: ErrPersonNotFound}
		}
		return nil, err
	}
	return &out, nil
}
