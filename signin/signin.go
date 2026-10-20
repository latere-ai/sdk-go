// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package signin signs a person in to Latere from a program of yours, with
// standard OAuth 2.0 and OpenID Connect: in their browser from a desktop
// app (Browser), or with a code they enter on another device from a
// terminal (Device). It then keeps the sign-in fresh (TokenSource), says
// who signed in (UserInfo), and ends it (Revoke).
//
// A sign-in's token opens the sign-in service only. To call the API as the
// person, exchange it for API tokens with latere.NewActorTokens:
//
//	signer := &signin.Config{ClientID: "my-app"}
//	tok, err := signer.Browser(ctx, openInBrowser)
//	if err != nil {
//		return err
//	}
//	tokens, err := latere.NewActorTokens(signer.TokenSource(tok, saveToKeychain))
//	if err != nil {
//		return err
//	}
//	cfg, err := latere.NewConfig(latere.WithTokenSource(tokens))
//
// # Registering your program
//
// Signing a person in needs a client id that Latere registers for your
// program; registration is not self-service yet. To ask for one, write to
// contact@latere.ai with your program's name, what it does, and which flows
// it uses. The client is registered with api.latere.ai among its
// audiences, so its sign-ins can be exchanged for API tokens. For Browser,
// it is registered with exactly the redirect address
// http://127.0.0.1/callback: Browser picks a free port when it runs, and
// the sign-in service accepts any port on that address, but the path must
// be /callback. For Device, it is registered for the device flow.
//
// A program that does not act for a person, such as a script or a server,
// needs none of this: it uses a key with latere.WithKey.
package signin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"latere.ai/x/sdk-go/latere"
)

// DefaultScopes returns the scopes a sign-in asks for when Config.Scopes is
// nil: who the person is, and a refresh token that keeps them signed in.
// Each call returns a new slice, which the caller may change.
func DefaultScopes() []string {
	return []string{"openid", "email", "profile", "offline_access"}
}

// Config describes how your program signs a person in. Its fields are read
// on first use and must not change after.
type Config struct {
	// ClientID is your program's client id. Required.
	ClientID string
	// Issuer is the sign-in service; latere.DefaultIssuer when empty.
	Issuer string
	// Scopes are the scopes asked for; DefaultScopes when nil.
	Scopes []string
	// HTTPClient sends the requests; http.DefaultClient when nil.
	HTTPClient *http.Client

	mu        sync.Mutex
	endpoints *endpoints
}

// A Token is what a sign-in yields. Keep RefreshToken somewhere safe, such
// as the system keychain: it keeps the person signed in.
type Token struct {
	// AccessToken opens the sign-in service as the person; exchange it with
	// latere.NewActorTokens to call the API.
	AccessToken string `json:"access_token"`
	// RefreshToken obtains new tokens without the person; empty when the
	// sign-in did not ask for offline_access.
	RefreshToken string `json:"refresh_token,omitempty"`
	// IDToken is the OpenID Connect ID token, when the service issued one.
	// The package does not verify its signature or claims: verify it
	// before trusting what it says, or ask UserInfo, which the service
	// answers over its own connection.
	IDToken string `json:"id_token,omitempty"`
	// Expiry is when AccessToken expires; zero when the service did not
	// say.
	Expiry time.Time `json:"expiry,omitzero"`
}

// endpoints are the addresses the sign-in service publishes in its
// discovery document.
type endpoints struct {
	Authorization string `json:"authorization_endpoint"`
	Token         string `json:"token_endpoint"`
	UserInfo      string `json:"userinfo_endpoint"`
	Device        string `json:"device_authorization_endpoint"`
	Revocation    string `json:"revocation_endpoint"`
}

// Error is an error the sign-in service answered with, in the OAuth form
// {"error", "error_description"}.
type Error struct {
	// StatusCode is the response's HTTP status.
	StatusCode int
	// Code is the OAuth error code, such as "invalid_grant".
	Code string
	// Description explains the error, when the service did.
	Description string
}

// Error returns the code and the description.
func (e *Error) Error() string {
	if e.Description == "" {
		return fmt.Sprintf("signin: %d %s", e.StatusCode, e.Code)
	}
	return fmt.Sprintf("signin: %d %s: %s", e.StatusCode, e.Code, e.Description)
}

// Is matches ErrDenied, ErrExpired and ErrInvalidGrant by the OAuth code.
func (e *Error) Is(target error) bool {
	switch target {
	case ErrDenied:
		return e.Code == "access_denied"
	case ErrExpired:
		return e.Code == "expired_token"
	case ErrInvalidGrant:
		return e.Code == "invalid_grant"
	}
	return false
}

// Errors to test a sign-in's error against with errors.Is.
var (
	// ErrDenied is a person who declined the sign-in.
	ErrDenied = errors.New("signin: the person declined")
	// ErrExpired is a device code that expired before the person entered
	// it.
	ErrExpired = errors.New("signin: the code expired")
	// ErrInvalidGrant is a refresh token the service no longer accepts:
	// the person signed out, or it was revoked or used twice. Sign the
	// person in again.
	ErrInvalidGrant = errors.New("signin: the sign-in is no longer valid")
)

func (c *Config) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

func (c *Config) issuer() string {
	if c.Issuer != "" {
		return strings.TrimSuffix(c.Issuer, "/")
	}
	return latere.DefaultIssuer
}

func (c *Config) scopes() string {
	if c.Scopes != nil {
		return strings.Join(c.Scopes, " ")
	}
	return strings.Join(DefaultScopes(), " ")
}

// discover returns the service's endpoints, reading its discovery document
// once.
func (c *Config) discover(ctx context.Context) (*endpoints, error) {
	if c.ClientID == "" {
		return nil, errors.New("signin: Config.ClientID is required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.endpoints != nil {
		return c.endpoints, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.issuer()+"/.well-known/openid-configuration", nil)
	if err != nil {
		return nil, fmt.Errorf("signin: discovery: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	var e endpoints
	if err := c.do(req, &e); err != nil {
		return nil, fmt.Errorf("signin: discovery: %w", err)
	}
	if e.Authorization == "" || e.Token == "" {
		return nil, errors.New("signin: discovery: the service names no authorization or token endpoint")
	}
	c.endpoints = &e
	return &e, nil
}

// do sends req and decodes a 2xx JSON answer into out, which may be nil.
// An error answer becomes an *Error.
func (c *Config) do(req *http.Request, out any) error {
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer func() {
		// The answer is read in full below; a failed close only costs the
		// connection's reuse.
		_ = resp.Body.Close()
	}()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("reading the %d answer: %w", resp.StatusCode, err)
	}
	if resp.StatusCode/100 != 2 {
		var oe struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		e := &Error{StatusCode: resp.StatusCode}
		if json.Unmarshal(body, &oe) == nil && oe.Error != "" {
			e.Code, e.Description = oe.Error, oe.Description
		} else {
			e.Code = http.StatusText(resp.StatusCode)
		}
		return e
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decoding the answer: %w", err)
	}
	return nil
}

// postForm sends form to endpoint and decodes the answer into out.
func (c *Config) postForm(ctx context.Context, endpoint string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	return c.do(req, out)
}

// tokenAnswer is the token endpoint's answer.
type tokenAnswer struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

// token posts a grant to the token endpoint and returns the tokens.
func (c *Config) token(ctx context.Context, form url.Values) (*Token, error) {
	e, err := c.discover(ctx)
	if err != nil {
		return nil, err
	}
	form.Set("client_id", c.ClientID)
	start := time.Now()
	var a tokenAnswer
	if err := c.postForm(ctx, e.Token, form, &a); err != nil {
		return nil, err
	}
	if a.AccessToken == "" {
		return nil, errors.New("signin: the token endpoint returned no access token")
	}
	t := &Token{AccessToken: a.AccessToken, RefreshToken: a.RefreshToken, IDToken: a.IDToken}
	if a.ExpiresIn > 0 {
		t.Expiry = start.Add(time.Duration(a.ExpiresIn) * time.Second)
	}
	return t, nil
}

// Refresh exchanges a refresh token for new tokens. A service that rotates
// refresh tokens returns a new one, and the old one stops working: keep the
// new one. It returns an error matching ErrInvalidGrant when the refresh
// token is no longer accepted.
func (c *Config) Refresh(ctx context.Context, refreshToken string) (*Token, error) {
	if refreshToken == "" {
		return nil, errors.New("signin: the refresh token is empty")
	}
	t, err := c.token(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}})
	if err != nil {
		return nil, err
	}
	if t.RefreshToken == "" {
		// A service that does not rotate keeps the refresh token valid.
		t.RefreshToken = refreshToken
	}
	return t, nil
}

// TokenSource returns a source of the person's access token for
// latere.NewActorTokens. It returns tok's access token while it is valid and
// refreshes it with the refresh token shortly before it expires, one
// refresh at a time however many requests wait. save, which may be nil, is
// called with every refreshed token before the token is used, so your
// program can store the newest refresh token; a save that fails fails the
// request that caused the refresh.
func (c *Config) TokenSource(tok *Token, save func(context.Context, *Token) error) latere.TokenSource {
	r := &refresher{cfg: c, save: save}
	if tok != nil {
		r.current = *tok
	}
	r.reuse = latere.ReuseToken(latere.TokenSourceFunc(r.next), refreshEarly)
	return r
}

// refreshEarly is how long before its expiry an access token is refreshed.
const refreshEarly = time.Minute

// refresher holds the newest token of one sign-in.
type refresher struct {
	cfg   *Config
	save  func(context.Context, *Token) error
	reuse *latere.ReuseTokenSource

	mu      sync.Mutex
	current Token
	stale   bool
}

// Token returns the access token, refreshing it when it is about to expire.
func (r *refresher) Token(ctx context.Context) (latere.Token, error) { return r.reuse.Token(ctx) }

// Reset makes the next call refresh, for an access token the service
// refused before its expiry.
func (r *refresher) Reset() {
	r.mu.Lock()
	r.stale = true
	r.mu.Unlock()
	r.reuse.Reset()
}

// next returns the held access token while it is fresh, and otherwise
// refreshes it. The ReuseTokenSource around it calls it once at a time.
func (r *refresher) next(ctx context.Context) (latere.Token, error) {
	r.mu.Lock()
	cur, stale := r.current, r.stale
	r.mu.Unlock()
	fresh := cur.AccessToken != "" && !stale && (cur.Expiry.IsZero() || time.Now().Add(refreshEarly).Before(cur.Expiry))
	if fresh {
		return latere.Token{Value: cur.AccessToken, Expiry: cur.Expiry}, nil
	}
	if cur.RefreshToken == "" {
		return latere.Token{}, errors.New("signin: the access token expired and there is no refresh token; sign in again")
	}
	t, err := r.cfg.Refresh(ctx, cur.RefreshToken)
	if err != nil {
		return latere.Token{}, err
	}
	if r.save != nil {
		if err := r.save(ctx, t); err != nil {
			return latere.Token{}, fmt.Errorf("signin: saving the refreshed token: %w", err)
		}
	}
	r.mu.Lock()
	r.current, r.stale = *t, false
	r.mu.Unlock()
	return latere.Token{Value: t.AccessToken, Expiry: t.Expiry}, nil
}

// UserInfo is who signed in, as the sign-in service says.
type UserInfo struct {
	// Subject identifies the person at the sign-in service.
	Subject string `json:"sub"`
	// Name is the person's name.
	Name string `json:"name,omitempty"`
	// Email is the person's email address.
	Email string `json:"email,omitempty"`
	// EmailVerified reports whether the address was confirmed.
	EmailVerified bool `json:"email_verified,omitempty"`
	// Picture is the address of the person's picture.
	Picture string `json:"picture,omitempty"`
}

// UserInfo returns who the access token belongs to, from the service's
// userinfo endpoint.
func (c *Config) UserInfo(ctx context.Context, accessToken string) (*UserInfo, error) {
	e, err := c.discover(ctx)
	if err != nil {
		return nil, err
	}
	if e.UserInfo == "" {
		return nil, errors.New("signin: the service names no userinfo endpoint")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.UserInfo, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	var u UserInfo
	if err := c.do(req, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// Revoke ends a refresh token at the sign-in service, signing the person
// out of your program (RFC 7009). Revoking a token that is already invalid
// succeeds.
func (c *Config) Revoke(ctx context.Context, token string) error {
	e, err := c.discover(ctx)
	if err != nil {
		return err
	}
	if e.Revocation == "" {
		return errors.New("signin: the service names no revocation endpoint")
	}
	form := url.Values{"token": {token}, "token_type_hint": {"refresh_token"}, "client_id": {c.ClientID}}
	return c.postForm(ctx, e.Revocation, form, nil)
}
