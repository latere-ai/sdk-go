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
	"strings"
	"time"
)

// A person's keys are records of the sign-in service too. A key is a
// credential a program presents to call the API as the person, within what
// its grants allow, without the person's sign-in. A sign-in creates and
// revokes the person's keys with its access token, the one UserInfo takes,
// at the service's root; neither call needs Config.ClientID.

// keysPath is where the sign-in service keeps a person's keys.
const keysPath = "/me/keys"

// ErrNoKey matches RevokeKey's error for an id that names no key of the
// person whose sign-in asked.
var ErrNoKey = errors.New("signin: the person holds no key with the id")

// KeyStatus is where a key is in its life.
type KeyStatus string

// The statuses of a key.
const (
	// KeyActive is a key that obtains tokens.
	KeyActive KeyStatus = "active"
	// KeyPendingApproval is a member key that waits for an admin of its
	// organization to approve it, under a policy that asks for that. It
	// obtains no token until then.
	KeyPendingApproval KeyStatus = "pending_approval"
	// KeyRevoked is a key revoked by the person or by an admin of its
	// organization, or because their membership of it ended.
	KeyRevoked KeyStatus = "revoked"
	// KeyExpired is a key past its ExpiresAt.
	KeyExpired KeyStatus = "expired"
)

// A Key is a key the person holds, as the sign-in service answers it.
type Key struct {
	// ID identifies the key; RevokeKey takes it.
	ID string `json:"id"`
	// Name tells the key apart in the person's list.
	Name string `json:"name"`
	// Prefix is the first characters of the key's value, which tell keys
	// apart where the value is not shown.
	Prefix string `json:"prefix"`
	// Value is the key itself, which a program presents with
	// latere.WithKey. It is set on the Key CreateKey returns alone: the
	// service keeps only a hash of it and never shows it again.
	Value string `json:"key,omitempty"`
	// OrgID is the organization a member key acts in; empty for a
	// personal key, which acts in the person's own account.
	OrgID string `json:"org_id,omitempty"`
	// Status is where the key is in its life.
	Status KeyStatus `json:"status"`
	// Grants is what the key may do, as CreateKeyParams.Grants gave it
	// and as every token the key obtains carries it.
	Grants json.RawMessage `json:"grants,omitempty"`
	// CreatedAt is when the key was created.
	CreatedAt time.Time `json:"created_at"`
	// ExpiresAt is when the key stops working; zero for a key that does
	// not expire.
	ExpiresAt time.Time `json:"expires_at,omitzero"`
	// LastUsedAt is when the key last obtained a token; zero for one that
	// never has, and on the Key CreateKey returns.
	LastUsedAt time.Time `json:"last_used_at,omitzero"`
	// RevokedAt is when the key was revoked; zero for one that is not.
	RevokedAt time.Time `json:"revoked_at,omitzero"`

	raw json.RawMessage
}

// RawJSON returns the JSON the service answered for the key, with every
// field, including those this package does not model; nil for a Key the
// program built itself.
func (k *Key) RawJSON() json.RawMessage { return k.raw }

// UnmarshalJSON decodes the key and keeps the JSON for RawJSON.
func (k *Key) UnmarshalJSON(b []byte) error {
	type plain Key
	if err := json.Unmarshal(b, (*plain)(k)); err != nil {
		return err
	}
	k.raw = append(json.RawMessage(nil), b...)
	return nil
}

// CreateKeyParams describe a key to create.
type CreateKeyParams struct {
	// Name tells the key apart in the person's list, such as the program
	// and the computer it runs on. Required.
	Name string
	// Grants is what the key may do: a JSON array of authorization
	// details (RFC 9396), each naming actions, the kinds of resource they
	// act on, and the API they are spent at. Required: the service
	// refuses a list that is empty, malformed or names what no published
	// vocabulary does, with a code that starts with "grant_".
	Grants json.RawMessage
	// OrgID makes a member key, which acts in that organization; empty
	// makes a personal key. The organization's policy decides whether a
	// member key obtains tokens at once, waits for an admin's approval, or
	// is refused.
	OrgID string
	// ExpiresAt is when the key stops working, which must be in the
	// future; zero makes a key that does not expire.
	ExpiresAt time.Time
}

// CreateKey creates a key for the person whose sign-in accessToken is, and
// returns it with its Value, which the service shows this once.
//
// The token must be the person's own sign-in through a client Latere
// registers as first party, such as its own apps and command line: a sign-in
// through another client is refused with 403 and the code
// "first_party_required", and a token a key or an agent obtained with 403
// and "sign_in_required".
//
// A person asks for at most ten keys an hour, refused ones included, and
// past that is refused with 429 and the code "rate_limit"; they hold at most
// twenty that are not revoked or expired, and past that are refused with
// 409 and "key_limit". A member key is refused with 403 and "not_a_member"
// for a person not in the organization, and "member_keys_off" for one whose
// policy lets no member create keys. A list of grants the service does not
// accept is refused with 400 and a code that starts with "grant_", with the
// entry it refused in the Error's Detail. A refusal returns an *Error with
// the service's code.
//
// CreateKey is sent once, never again after a failure: the service takes
// no idempotency key, and a repeat would create a second key.
func (c *Config) CreateKey(ctx context.Context, accessToken string, params *CreateKeyParams) (*Key, error) {
	if params == nil || strings.TrimSpace(params.Name) == "" {
		return nil, errors.New("signin: CreateKey: the key's name is empty")
	}
	if len(params.Grants) == 0 {
		return nil, errors.New("signin: CreateKey: the key's grants are empty")
	}
	body := struct {
		Name      string          `json:"name"`
		Grants    json.RawMessage `json:"grants"`
		OrgID     string          `json:"org_id,omitempty"`
		ExpiresAt string          `json:"expires_at,omitempty"`
	}{Name: params.Name, Grants: params.Grants, OrgID: params.OrgID}
	if !params.ExpiresAt.IsZero() {
		body.ExpiresAt = params.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	var k Key
	if err := c.record(ctx, http.MethodPost, accessToken, keysPath, nil, body, &k); err != nil {
		return nil, err
	}
	if k.ID == "" || k.Value == "" {
		return nil, fmt.Errorf("signin: POST %s: the service answered no key", keysPath)
	}
	return &k, nil
}

// RevokeKey revokes the key id of the person whose sign-in accessToken is:
// it obtains no token from then on, and a token it obtained before lives
// to its expiry. Revoking a key that is revoked already succeeds. The id
// is a Key's ID, as CreateKey and ListKeys answer it; an id of that form
// that names no key of the person returns an error matching ErrNoKey,
// which is an *Error too, with the service's 404, and the service refuses
// an id of another form with an *Error.
func (c *Config) RevokeKey(ctx context.Context, accessToken, id string) error {
	if id == "" {
		return errors.New("signin: RevokeKey: the id is empty")
	}
	err := c.record(ctx, http.MethodDelete, accessToken, keysPath+"/"+url.PathEscape(id), nil, nil, nil)
	var refused *Error
	if errors.As(err, &refused) && refused.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: %w", ErrNoKey, refused)
	}
	return err
}

// ListKeysParams say which of the person's keys ListKeys answers. The zero
// value keeps every key.
type ListKeysParams struct {
	// OrgID keeps the member keys of that organization alone.
	OrgID string
	// Personal keeps the personal keys alone. It cannot be set with
	// OrgID.
	Personal bool
}

// ListKeys returns the keys of the person whose sign-in accessToken is,
// newest first, revoked and expired ones included, and never with their
// Value; an empty slice for a person who holds none. A refusal, such as 401
// for a token that is not a sign-in's, returns an *Error.
func (c *Config) ListKeys(ctx context.Context, accessToken string, params *ListKeysParams) ([]*Key, error) {
	var query url.Values
	if params != nil {
		switch {
		case params.Personal && params.OrgID != "":
			return nil, errors.New("signin: ListKeys: Personal and OrgID are both set")
		case params.Personal:
			// The service narrows to the personal keys on an org_id that
			// is present and empty.
			query = url.Values{"org_id": {""}}
		case params.OrgID != "":
			query = url.Values{"org_id": {params.OrgID}}
		}
	}
	var answer struct {
		Keys []*Key `json:"keys"`
	}
	if err := c.record(ctx, http.MethodGet, accessToken, keysPath, query, nil, &answer); err != nil {
		return nil, err
	}
	if answer.Keys == nil {
		answer.Keys = []*Key{}
	}
	return answer.Keys, nil
}
