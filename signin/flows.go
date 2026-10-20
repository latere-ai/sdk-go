// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package signin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

// Browser signs a person in through their browser, for a desktop app. It
// listens on a port of the loopback address, calls open with the address of
// the sign-in page, and returns the tokens once the browser comes back to
// the port. The person's browser shows a short page saying they can close
// the tab. Bound the wait with ctx; it returns ctx's error when ctx ends
// first, and an error matching ErrDenied when the person declines.
//
// It uses the authorization code flow with PKCE (RFC 7636) and a loopback
// redirect (RFC 8252), so it needs no client secret.
func (c *Config) Browser(ctx context.Context, open func(signInURL string) error) (*Token, error) {
	if open == nil {
		return nil, errors.New("signin: Browser: open is nil")
	}
	e, err := c.discover(ctx)
	if err != nil {
		return nil, err
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("signin: Browser: listening for the browser: %w", err)
	}
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		closeErr := ln.Close()
		return nil, errors.Join(fmt.Errorf("signin: Browser: the listener is on %v, not TCP", ln.Addr()), closeErr)
	}
	redirect := fmt.Sprintf("http://127.0.0.1:%d/callback", addr.Port)
	verifier, state := randomString(), randomString()
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {c.ClientID},
		"redirect_uri":          {redirect},
		"scope":                 {c.scopes()},
		"state":                 {state},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
	}
	signInURL := e.Authorization + "?" + q.Encode()

	type result struct {
		code string
		err  error
	}
	results := make(chan result, 1)
	srv := &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// The port serves the browser's one return, so no answer keeps
			// its connection, and the server shuts down without waiting.
			w.Header().Set("Connection", "close")
			if r.URL.Path != "/callback" {
				http.NotFound(w, r)
				return
			}
			got := r.URL.Query()
			// A request that is not the browser coming back from this
			// sign-in changes nothing; the sign-in goes on waiting.
			if got.Get("state") != state {
				http.Error(w, "This sign-in link is not the one this program opened.", http.StatusBadRequest)
				return
			}
			var res result
			switch {
			case got.Get("error") != "":
				res.err = &Error{StatusCode: http.StatusBadRequest, Code: got.Get("error"), Description: got.Get("error_description")}
				writePage(w, "Sign-in was canceled. You can close this tab.")
			case got.Get("code") == "":
				res.err = errors.New("signin: Browser: the browser came back without a code")
				writePage(w, "Sign-in did not finish. You can close this tab and try again.")
			default:
				res.code = got.Get("code")
				writePage(w, "You are signed in. You can close this tab.")
			}
			select {
			case results <- res:
			default:
			}
		}),
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	defer func() {
		// Shut down gracefully, so the page a handler is still sending
		// reaches the browser; a browser that holds its connection open
		// is cut off after the bound. Neither outcome changes the
		// sign-in's result, so the error is dropped.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	if err := open(signInURL); err != nil {
		return nil, fmt.Errorf("signin: Browser: opening the sign-in page: %w", err)
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err := <-serveErr:
		return nil, fmt.Errorf("signin: Browser: the loopback server stopped: %w", err)
	case res := <-results:
		if res.err != nil {
			return nil, res.err
		}
		return c.token(ctx, url.Values{
			"grant_type":    {"authorization_code"},
			"code":          {res.code},
			"redirect_uri":  {redirect},
			"code_verifier": {verifier},
		})
	}
}

// writePage answers the browser with a short page. A write that fails
// leaves the sign-in itself unaffected, so its error is dropped.
func writePage(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = fmt.Fprintf(w, "<!doctype html><meta charset=utf-8><title>Latere</title><p style=\"font:16px system-ui;margin:3em\">%s</p>", text)
}

// randomString returns 32 random bytes in base64url, for a PKCE verifier or
// a state.
func randomString() string {
	b := make([]byte, 32)
	// crypto/rand.Read never returns an error on the supported platforms.
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// The device flow's waits (RFC 8628 section 3.5): the poll interval when
// the service names none, and how much longer to wait after "slow_down".
// Variables so the package's tests can shorten them.
var (
	defaultInterval = 5 * time.Second
	slowDownStep    = 5 * time.Second
)

// A DeviceCode is what Device asks the person to do: open VerificationURI
// on any device and enter UserCode.
type DeviceCode struct {
	// UserCode is the code the person enters.
	UserCode string
	// VerificationURI is the page where they enter it.
	VerificationURI string
	// VerificationURIComplete opens the page with the code filled in, when
	// the service gives one.
	VerificationURIComplete string
	// ExpiresAt is when the code stops working.
	ExpiresAt time.Time
}

// Device signs a person in with a code they enter on another device, for a
// program in a terminal or on a machine without a browser (RFC 8628). It
// calls prompt with the code to show, then waits until the person has
// entered it and returns the tokens. It returns an error matching ErrDenied
// when the person declines, ErrExpired when the code expires first, and
// ctx's error when ctx ends.
func (c *Config) Device(ctx context.Context, prompt func(DeviceCode) error) (*Token, error) {
	if prompt == nil {
		return nil, errors.New("signin: Device: prompt is nil")
	}
	e, err := c.discover(ctx)
	if err != nil {
		return nil, err
	}
	if e.Device == "" {
		return nil, errors.New("signin: Device: the service names no device authorization endpoint")
	}
	var a struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURI         string `json:"verification_uri"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		ExpiresIn               int64  `json:"expires_in"`
		Interval                int64  `json:"interval"`
	}
	start := time.Now()
	form := url.Values{"client_id": {c.ClientID}, "scope": {c.scopes()}}
	if err := c.postForm(ctx, e.Device, form, &a); err != nil {
		return nil, fmt.Errorf("signin: Device: %w", err)
	}
	if a.DeviceCode == "" || a.UserCode == "" {
		return nil, errors.New("signin: Device: the service returned no code")
	}
	expires := start.Add(time.Duration(a.ExpiresIn) * time.Second)
	if err := prompt(DeviceCode{
		UserCode:                a.UserCode,
		VerificationURI:         a.VerificationURI,
		VerificationURIComplete: a.VerificationURIComplete,
		ExpiresAt:               expires,
	}); err != nil {
		return nil, fmt.Errorf("signin: Device: prompt: %w", err)
	}
	interval := time.Duration(a.Interval) * time.Second
	if interval <= 0 {
		interval = defaultInterval
	}
	for {
		t := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		case <-t.C:
		}
		tok, err := c.token(ctx, url.Values{
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
			"device_code": {a.DeviceCode},
		})
		if err == nil {
			return tok, nil
		}
		var oe *Error
		if !errors.As(err, &oe) {
			return nil, err
		}
		switch oe.Code {
		case "authorization_pending":
		case "slow_down":
			interval += slowDownStep
		default:
			return nil, err
		}
	}
}
