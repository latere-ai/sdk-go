// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package signin

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeIssuer is an OpenID Connect provider with the endpoints signin uses.
type fakeIssuer struct {
	t   *testing.T
	srv *httptest.Server

	mu         sync.Mutex
	challenges map[string]string // code -> PKCE challenge
	refreshes  atomic.Int32
	polls      atomic.Int32
	devicePlan []string // the token endpoint's answers to device polls
	revoked    []string
	noRefresh  bool
}

func newIssuer(t *testing.T) *fakeIssuer {
	f := &fakeIssuer{t: t, challenges: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		base := f.srv.URL
		writeJSON(w, map[string]string{
			"authorization_endpoint":        base + "/authorize",
			"token_endpoint":                base + "/token",
			"userinfo_endpoint":             base + "/userinfo",
			"device_authorization_endpoint": base + "/device/code",
			"revocation_endpoint":           base + "/revoke",
		})
	})
	mux.HandleFunc("POST /token", f.token)
	mux.HandleFunc("POST /device/code", func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("client_id") != "my-app" || !strings.Contains(r.FormValue("scope"), "offline_access") {
			t.Errorf("device request %v", r.Form)
		}
		writeJSON(w, map[string]any{"device_code": "dc_1", "user_code": "ABCD-EFGH", "verification_uri": "https://example.test/device",
			"verification_uri_complete": "https://example.test/device?code=ABCD-EFGH", "expires_in": 600, "interval": 0})
	})
	mux.HandleFunc("GET /userinfo", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer access_1" {
			oauthError(w, http.StatusUnauthorized, "invalid_token", "")
			return
		}
		writeJSON(w, map[string]any{"sub": "u_1", "name": "Al", "email": "al@example.test", "email_verified": true, "picture": "https://example.test/al.png", "org_id": "org_1", "handle": "al"})
	})
	mux.HandleFunc("POST /revoke", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.revoked = append(f.revoked, r.FormValue("token"))
		f.mu.Unlock()
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIssuer) token(w http.ResponseWriter, r *http.Request) {
	if r.FormValue("client_id") != "my-app" {
		oauthError(w, http.StatusBadRequest, "invalid_client", "")
		return
	}
	switch r.FormValue("grant_type") {
	case "authorization_code":
		f.mu.Lock()
		challenge := f.challenges[r.FormValue("code")]
		f.mu.Unlock()
		sum := sha256.Sum256([]byte(r.FormValue("code_verifier")))
		if challenge == "" || base64.RawURLEncoding.EncodeToString(sum[:]) != challenge || !strings.HasPrefix(r.FormValue("redirect_uri"), "http://127.0.0.1:") {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "the verifier does not match")
			return
		}
		writeJSON(w, map[string]any{"access_token": "access_1", "refresh_token": "refresh_1", "id_token": "id_1", "expires_in": 3600})
	case "refresh_token":
		n := f.refreshes.Add(1)
		if r.FormValue("refresh_token") == "revoked" {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "signed out")
			return
		}
		answer := map[string]any{"access_token": fmt.Sprintf("access_r%d", n), "expires_in": 3600}
		if !f.noRefresh {
			answer["refresh_token"] = fmt.Sprintf("refresh_r%d", n)
		}
		writeJSON(w, answer)
	case "urn:ietf:params:oauth:grant-type:device_code":
		i := int(f.polls.Add(1)) - 1
		if i < len(f.devicePlan) && f.devicePlan[i] == "down" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if i < len(f.devicePlan) && f.devicePlan[i] != "ok" {
			oauthError(w, http.StatusBadRequest, f.devicePlan[i], "")
			return
		}
		writeJSON(w, map[string]any{"access_token": "access_d", "refresh_token": "refresh_d", "expires_in": 3600})
	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "")
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		panic(err)
	}
}

func oauthError(w http.ResponseWriter, status int, code, desc string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]string{"error": code, "error_description": desc}); err != nil {
		panic(err)
	}
}

func (f *fakeIssuer) config() *Config { return &Config{ClientID: "my-app", Issuer: f.srv.URL + "/"} }

// browser plays the person's browser: it reads the sign-in address, records
// the PKCE challenge as the service would, and comes back to the redirect
// with the given query.
func (f *fakeIssuer) browser(back func(redirect, state string) url.Values) func(string) error {
	return func(signInURL string) error {
		u, err := url.Parse(signInURL)
		if err != nil {
			return err
		}
		q := u.Query()
		if q.Get("code_challenge_method") != "S256" || q.Get("response_type") != "code" || q.Get("client_id") != "my-app" {
			f.t.Errorf("sign-in address %s", signInURL)
		}
		f.mu.Lock()
		f.challenges["code_1"] = q.Get("code_challenge")
		f.mu.Unlock()
		go func() {
			for _, query := range []url.Values{{"state": {"wrong"}, "code": {"stolen"}}, back(q.Get("redirect_uri"), q.Get("state"))} {
				resp, err := http.Get(q.Get("redirect_uri") + "?" + query.Encode())
				if err != nil {
					f.t.Error(err)
					return
				}
				if err := resp.Body.Close(); err != nil {
					f.t.Error(err)
				}
			}
		}()
		return nil
	}
}

func TestBrowserSignsIn(t *testing.T) {
	f := newIssuer(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	tok, err := f.config().Browser(ctx, f.browser(func(_, state string) url.Values {
		return url.Values{"state": {state}, "code": {"code_1"}}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "access_1" || tok.RefreshToken != "refresh_1" || tok.IDToken != "id_1" || time.Until(tok.Expiry) < 59*time.Minute {
		t.Errorf("token %+v", tok)
	}
}

func TestBrowserDeclined(t *testing.T) {
	f := newIssuer(t)
	_, err := f.config().Browser(t.Context(), f.browser(func(_, state string) url.Values {
		return url.Values{"state": {state}, "error": {"access_denied"}, "error_description": {"no thanks"}}
	}))
	if !errors.Is(err, ErrDenied) {
		t.Errorf("a declined sign-in: %v", err)
	}
	_, err = f.config().Browser(t.Context(), f.browser(func(_, state string) url.Values { return url.Values{"state": {state}} }))
	if err == nil {
		t.Error("a return without a code succeeded")
	}
}

func TestBrowserFailures(t *testing.T) {
	f := newIssuer(t)
	if _, err := f.config().Browser(t.Context(), nil); err == nil {
		t.Error("a nil open succeeded")
	}
	boom := errors.New("no browser")
	if _, err := f.config().Browser(t.Context(), func(string) error { return boom }); !errors.Is(err, boom) {
		t.Errorf("open failing: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := f.config().Browser(ctx, func(string) error { return nil }); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a browser that never comes back: %v", err)
	}
	if _, err := (&Config{}).Browser(t.Context(), func(string) error { return nil }); err == nil {
		t.Error("a config without a client id succeeded")
	}
}

func TestDeviceSignsIn(t *testing.T) {
	defaultInterval, slowDownStep = time.Millisecond, time.Millisecond
	f := newIssuer(t)
	f.devicePlan = []string{"authorization_pending", "slow_down", "ok"}
	var shown DeviceCode
	tok, err := f.config().Device(t.Context(), func(c DeviceCode) error { shown = c; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "access_d" || shown.UserCode != "ABCD-EFGH" || shown.VerificationURIComplete == "" || time.Until(shown.ExpiresAt) < 9*time.Minute {
		t.Errorf("token %+v after showing %+v", tok, shown)
	}
	if f.polls.Load() != 3 {
		t.Errorf("polled %d times, want 3", f.polls.Load())
	}
}

func TestDeviceToleratesOneFailedPoll(t *testing.T) {
	defaultInterval, slowDownStep = time.Millisecond, time.Millisecond
	f := newIssuer(t)
	f.devicePlan = []string{"down", "authorization_pending", "down", "ok"}
	if _, err := f.config().Device(t.Context(), func(DeviceCode) error { return nil }); err != nil || f.polls.Load() != 4 {
		t.Errorf("two failed polls apart: %v after %d polls", err, f.polls.Load())
	}
	f = newIssuer(t)
	f.devicePlan = []string{"authorization_pending", "down", "down", "ok"}
	if _, err := f.config().Device(t.Context(), func(DeviceCode) error { return nil }); err == nil || f.polls.Load() != 3 {
		t.Errorf("two failed polls in a row: %v after %d polls", err, f.polls.Load())
	}
}

func TestDeviceFailures(t *testing.T) {
	defaultInterval, slowDownStep = time.Millisecond, time.Millisecond
	for code, want := range map[string]error{"access_denied": ErrDenied, "expired_token": ErrExpired} {
		f := newIssuer(t)
		f.devicePlan = []string{code}
		if _, err := f.config().Device(t.Context(), func(DeviceCode) error { return nil }); !errors.Is(err, want) {
			t.Errorf("%s: %v", code, err)
		}
	}
	f := newIssuer(t)
	if _, err := f.config().Device(t.Context(), nil); err == nil {
		t.Error("a nil prompt succeeded")
	}
	boom := errors.New("no terminal")
	if _, err := f.config().Device(t.Context(), func(DeviceCode) error { return boom }); !errors.Is(err, boom) {
		t.Errorf("a failing prompt: %v", err)
	}
	f.devicePlan = []string{"authorization_pending", "authorization_pending", "authorization_pending"}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := f.config().Device(ctx, func(DeviceCode) error { return nil }); err == nil {
		t.Error("a canceled sign-in succeeded")
	}
}

func TestRefreshAndTokenSource(t *testing.T) {
	f := newIssuer(t)
	c := f.config()
	var saved []string
	src := c.TokenSource(&Token{AccessToken: "old", RefreshToken: "refresh_0", Expiry: time.Now().Add(30 * time.Second)}, func(_ context.Context, t *Token) error {
		saved = append(saved, t.RefreshToken)
		return nil
	})
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			tok, err := src.Token(t.Context())
			if err != nil || tok.Value != "access_r1" {
				t.Errorf("token %+v, %v", tok, err)
			}
		})
	}
	wg.Wait()
	if f.refreshes.Load() != 1 || strings.Join(saved, ",") != "refresh_r1" {
		t.Errorf("10 callers caused %d refreshes, saved %v", f.refreshes.Load(), saved)
	}
	r, ok := src.(interface{ Reset() })
	if !ok {
		t.Fatal("the token source cannot be reset")
	}
	r.Reset()
	if tok, err := src.Token(t.Context()); err != nil || tok.Value != "access_r2" {
		t.Errorf("after Reset: %+v, %v", tok, err)
	}
	fresh := c.TokenSource(&Token{AccessToken: "still_good", Expiry: time.Now().Add(time.Hour)}, nil)
	if tok, err := fresh.Token(t.Context()); err != nil || tok.Value != "still_good" {
		t.Errorf("a fresh token: %+v, %v", tok, err)
	}
}

func TestRefreshFailures(t *testing.T) {
	f := newIssuer(t)
	c := f.config()
	if _, err := c.Refresh(t.Context(), "revoked"); !errors.Is(err, ErrInvalidGrant) {
		t.Errorf("a revoked refresh token: %v", err)
	}
	if _, err := c.Refresh(t.Context(), ""); err == nil {
		t.Error("an empty refresh token was sent")
	}
	f.noRefresh = true
	if tok, err := c.Refresh(t.Context(), "keep_me"); err != nil || tok.RefreshToken != "keep_me" {
		t.Errorf("a service that does not rotate: %+v, %v", tok, err)
	}
	if _, err := c.TokenSource(nil, nil).Token(t.Context()); err == nil {
		t.Error("a source without a token returned one")
	}
	boom := errors.New("keychain full")
	expired := &Token{AccessToken: "old", RefreshToken: "refresh_0", Expiry: time.Now().Add(-time.Minute)}
	if _, err := c.TokenSource(expired, func(context.Context, *Token) error { return boom }).Token(t.Context()); !errors.Is(err, boom) {
		t.Errorf("a failing save: %v", err)
	}
	if _, err := c.TokenSource(&Token{AccessToken: "old", RefreshToken: "revoked", Expiry: time.Now().Add(-time.Minute)}, nil).Token(t.Context()); !errors.Is(err, ErrInvalidGrant) {
		t.Errorf("a revoked sign-in: %v", err)
	}
}

func TestUserInfoAndRevoke(t *testing.T) {
	f := newIssuer(t)
	c := f.config()
	u, err := c.UserInfo(t.Context(), "access_1")
	if err != nil || u.Subject != "u_1" || u.Email != "al@example.test" || !u.EmailVerified || u.OrgID != "org_1" || u.Handle != "al" {
		t.Errorf("UserInfo: %+v, %v", u, err)
	}
	var oe *Error
	if _, err := c.UserInfo(t.Context(), "nope"); !errors.As(err, &oe) || oe.Code != "invalid_token" || oe.StatusCode != 401 {
		t.Errorf("a refused token: %v", err)
	}
	if err := c.Revoke(t.Context(), "refresh_1"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.revoked, ",") != "refresh_1" {
		t.Errorf("revoked %v", f.revoked)
	}
}

func TestDiscoveryFailures(t *testing.T) {
	bare := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"authorization_endpoint": "x", "token_endpoint": "y"})
	}))
	defer bare.Close()
	c := &Config{ClientID: "my-app", Issuer: bare.URL}
	if _, err := c.UserInfo(t.Context(), "t"); err == nil {
		t.Error("UserInfo without an endpoint succeeded")
	}
	if err := c.Revoke(t.Context(), "t"); err == nil {
		t.Error("Revoke without an endpoint succeeded")
	}
	if _, err := c.Device(t.Context(), func(DeviceCode) error { return nil }); err == nil {
		t.Error("Device without an endpoint succeeded")
	}
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, map[string]string{}) }))
	defer empty.Close()
	if _, err := (&Config{ClientID: "my-app", Issuer: empty.URL}).Refresh(t.Context(), "r"); err == nil {
		t.Error("a discovery naming no endpoints succeeded")
	}
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "{") }))
	defer broken.Close()
	if _, err := (&Config{ClientID: "my-app", Issuer: broken.URL}).Refresh(t.Context(), "r"); err == nil {
		t.Error("a garbled discovery succeeded")
	}
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	defer down.Close()
	var oe *Error
	if _, err := (&Config{ClientID: "my-app", Issuer: down.URL}).Refresh(t.Context(), "r"); !errors.As(err, &oe) || oe.Code != "Bad Gateway" {
		t.Errorf("a failing discovery: %v", err)
	}
	if _, err := (&Config{ClientID: "my-app", Issuer: "http://127.0.0.1:1"}).Refresh(t.Context(), "r"); err == nil {
		t.Error("an unreachable service succeeded")
	}
}

func TestErrorText(t *testing.T) {
	if s := (&Error{StatusCode: 400, Code: "invalid_grant"}).Error(); s != "signin: 400 invalid_grant" {
		t.Errorf("Error() = %q", s)
	}
	if s := (&Error{StatusCode: 400, Code: "invalid_grant", Description: "gone"}).Error(); s != "signin: 400 invalid_grant: gone" {
		t.Errorf("Error() = %q", s)
	}
	if errors.Is(&Error{Code: "invalid_client"}, ErrDenied) {
		t.Error("an unrelated code matched ErrDenied")
	}
}

func TestDefaults(t *testing.T) {
	c := &Config{}
	if c.issuer() != "https://auth.latere.ai" || c.scopes() != "openid email profile offline_access" || c.httpClient() != http.DefaultClient {
		t.Errorf("defaults %q %q", c.issuer(), c.scopes())
	}
	scopes := DefaultScopes()
	scopes[0] = "changed"
	if DefaultScopes()[0] != "openid" {
		t.Error("a caller changed the default scopes")
	}
	c.Scopes, c.HTTPClient = []string{"openid"}, &http.Client{}
	if c.scopes() != "openid" || c.httpClient() == http.DefaultClient {
		t.Error("the fields are not used")
	}
}

func TestSwitchOrganization(t *testing.T) {
	var forms []url.Values
	var mu sync.Mutex
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]string{"authorization_endpoint": srv.URL + "/authorize", "token_endpoint": srv.URL + "/token"})
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		mu.Lock()
		forms = append(forms, r.PostForm)
		mu.Unlock()
		switch r.PostForm.Get("refresh_token") {
		case "spent":
			oauthError(w, http.StatusBadRequest, "invalid_grant", "spent")
		case "not_rotated":
			writeJSON(w, map[string]any{"access_token": "access_org", "expires_in": 600})
		default:
			if r.PostForm.Get("org_id") == "org_9" {
				// The service refuses a membership it does not find as it
				// refuses a spent token.
				oauthError(w, http.StatusBadRequest, "invalid_grant", "not a member of org org_9")
				return
			}
			writeJSON(w, map[string]any{"access_token": "access_org", "refresh_token": "refresh_next", "expires_in": 600})
		}
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()
	c := &Config{ClientID: "my-app", Issuer: srv.URL}

	tok, err := c.SwitchOrganization(t.Context(), "refresh_1", "org_1")
	if err != nil || tok.AccessToken != "access_org" || tok.RefreshToken != "refresh_next" || tok.Expiry.IsZero() {
		t.Fatalf("SwitchOrganization = %+v, %v", tok, err)
	}
	if f := forms[0]; f.Get("grant_type") != "refresh_token" || f.Get("refresh_token") != "refresh_1" || f.Get("org_id") != "org_1" || f.Get("client_id") != "my-app" {
		t.Errorf("the switch sent %v", f)
	}
	// The personal account is an org_id sent empty, not one left out.
	if tok, err := c.SwitchOrganization(t.Context(), "not_rotated", ""); err != nil || tok.RefreshToken != "not_rotated" {
		t.Errorf("a switch to the personal account = %+v, %v", tok, err)
	}
	if v, ok := forms[1]["org_id"]; !ok || len(v) != 1 || v[0] != "" {
		t.Errorf("a switch to the personal account sent org_id %q, present %v", v, ok)
	}
	if _, err := c.SwitchOrganization(t.Context(), "spent", "org_1"); !errors.Is(err, ErrInvalidGrant) {
		t.Errorf("a spent refresh token: %v", err)
	}
	var oe *Error
	if _, err := c.SwitchOrganization(t.Context(), "refresh_1", "org_9"); !errors.As(err, &oe) || oe.Description != "not a member of org org_9" || !errors.Is(err, ErrInvalidGrant) {
		t.Errorf("an organization the person is not in: %v", err)
	}
	sent := len(forms)
	if _, err := c.SwitchOrganization(t.Context(), "", "org_1"); err == nil {
		t.Error("an empty refresh token was accepted")
	}
	if len(forms) != sent {
		t.Error("a switch with an empty refresh token was sent")
	}
}
