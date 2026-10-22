// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package signin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// The sign-in service's own records, which a person's sign-in reads: the
// organizations they belong to and the handles people claimed. They are
// served at the service's root, outside OpenID Connect discovery, and take
// the access token a sign-in yields, the same one UserInfo takes; a token
// minted for the API is refused with 401. Neither call needs
// Config.ClientID.

// ErrNoPerson matches GetHandle's error for an id that names no one the
// sign-in service knows.
var ErrNoPerson = errors.New("signin: no one has the id")

// An Organization is one the person belongs to, as ListOrganizations
// returns it.
type Organization struct {
	// ID identifies the organization; a sign-in that acts in it carries it
	// as UserInfo.OrgID.
	ID string `json:"id"`
	// Name is the organization's name.
	Name string `json:"name"`
	// Slug is the organization's short name, as its addresses show it.
	Slug string `json:"slug"`
	// OwnerID is the id of the person who owns the organization.
	OwnerID string `json:"owner_id"`
	// Owner reports whether the person whose sign-in asked owns it.
	Owner bool `json:"owner,omitempty"`
	// JoinedAt is when the person joined the organization. The service
	// answers it as text, which is read in its ISO forms, such as
	// "2026-10-22 13:39:16.123456+00"; it is zero for text in another
	// form, which RawJSON keeps.
	JoinedAt time.Time `json:"-"`
	// ManagedBy names the identity provider that admitted the person, which
	// keeps the membership in step with its own records; empty for a
	// membership a person granted.
	ManagedBy string `json:"managed_by,omitempty"`

	raw json.RawMessage
}

// RawJSON returns the JSON the service answered for the organization, with
// every field, including those this package does not model; nil for an
// Organization the program built itself.
func (o *Organization) RawJSON() json.RawMessage { return o.raw }

// UnmarshalJSON decodes the organization, reads its joined_at, and keeps
// the JSON for RawJSON.
func (o *Organization) UnmarshalJSON(b []byte) error {
	type plain Organization
	v := struct {
		*plain
		JoinedAt string `json:"joined_at"`
	}{plain: (*plain)(o)}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	o.JoinedAt = joinedAt(v.JoinedAt)
	o.raw = append(json.RawMessage(nil), b...)
	return nil
}

// joinedLayouts are the forms a membership's time is read in: a Postgres
// timestamp with a zone as text, with an offset of hours or of hours and
// minutes, and RFC 3339. A fraction of a second after the seconds is read
// in each, as time.Parse reads one where the layout has none.
var joinedLayouts = []string{"2006-01-02 15:04:05-07", "2006-01-02 15:04:05-07:00", time.RFC3339}

// joinedAt reads text in the first of joinedLayouts it is in, and is zero
// for text in none of them.
func joinedAt(text string) time.Time {
	for _, layout := range joinedLayouts {
		if t, err := time.Parse(layout, text); err == nil {
			return t
		}
	}
	return time.Time{}
}

// held returns a copy of the Config's client that answers a redirect
// rather than following it, so the access token goes to the sign-in
// service alone. The copy shares the client's transport, so its
// connections are reused.
func (c *Config) held() *http.Client {
	held := *c.httpClient()
	held.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &held
}

// record sends a read of the service's own records at path, with query,
// as the bearer of accessToken, and decodes its answer into out. A
// redirect is answered as an *Error and never followed.
func (c *Config) record(ctx context.Context, accessToken, path string, query url.Values, out any) error {
	if accessToken == "" {
		return fmt.Errorf("signin: GET %s: the access token is empty", path)
	}
	target := c.issuer() + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return fmt.Errorf("signin: GET %s: %w", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	err = send(c.held(), req, out)
	var refused *Error
	if err != nil && !errors.As(err, &refused) {
		// A refusal names itself; a failure to reach or read the service
		// is named by the call it failed.
		return fmt.Errorf("signin: GET %s: %w", path, err)
	}
	return err
}

// ListOrganizations returns the organizations the person whose sign-in
// accessToken is belongs to, in the order they joined them; an empty
// slice for a person in none. A refusal, such as 401 for a token that is
// not a sign-in's, returns an *Error.
func (c *Config) ListOrganizations(ctx context.Context, accessToken string) ([]*Organization, error) {
	var out []*Organization
	if err := c.record(ctx, accessToken, "/me/orgs", nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []*Organization{}
	}
	return out, nil
}

// GetHandle returns the handle the person with id claimed, the name their
// addresses show, as the person whose sign-in accessToken is reads it; any
// signed-in person may read anyone's. A person who has claimed none
// returns "" and nil. An id that names no one returns an error matching
// ErrNoPerson, and one that is not an id, the UUID the sign-in service
// names a person by, such as UserInfo.Subject, returns an *Error with 400.
// The error matching ErrNoPerson is an *Error too, with the service's 404.
func (c *Config) GetHandle(ctx context.Context, accessToken, id string) (string, error) {
	if id == "" {
		return "", errors.New("signin: GetHandle: the id is empty")
	}
	var answer struct {
		Handle string `json:"handle"`
	}
	err := c.record(ctx, accessToken, "/handles", url.Values{"principal_id": {id}}, &answer)
	var refused *Error
	if errors.As(err, &refused) && refused.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("%w: %w", ErrNoPerson, refused)
	}
	if err != nil {
		return "", err
	}
	return answer.Handle, nil
}
