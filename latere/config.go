// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package latere

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"
)

// Version is the release of this module, sent in the User-Agent header:
// the newest release in CHANGELOG.md, or the coming one with "-dev" before
// the first.
const Version = "0.11.0"

// DefaultBaseURL is the address of the Latere API.
const DefaultBaseURL = "https://api.latere.ai"

// The defaults a Config starts from.
const (
	// DefaultMaxRetries is how many times a failed request is sent again.
	DefaultMaxRetries = 2
	// DefaultMinBackoff is the wait before the first retry.
	DefaultMinBackoff = 500 * time.Millisecond
	// DefaultMaxBackoff is the longest wait between two attempts, and the
	// longest Retry-After the SDK waits out; a longer one is returned to the
	// caller as the error.
	DefaultMaxBackoff = 30 * time.Second
	// DefaultTimeout bounds a call whose context has no deadline, its
	// retries included, so a server that stops answering cannot hold a
	// call forever. A stream is bounded only until its first answer, and
	// each attempt of a request that sends bytes by this and TransferTime
	// for their length.
	DefaultTimeout = 80 * time.Second
)

// Environment variables NewConfig reads when the matching option is absent.
const (
	// EnvKey holds a key created in the console, used when neither WithKey
	// nor WithTokenSource is given.
	EnvKey = "LATERE_KEY"
	// EnvBaseURL holds the API's address, used when WithBaseURL is not given.
	EnvBaseURL = "LATERE_BASE_URL"
)

// ErrNoCredentials is returned by NewConfig when no credentials were given
// and LATERE_KEY is not set. A program that reads only the API's public
// routes builds its Config WithoutCredentials instead.
var ErrNoCredentials = errors.New("latere: no credentials: use WithKey or WithTokenSource, or set " + EnvKey + "; WithoutCredentials reads the public routes alone")

// errNotBuilt is returned by a call on a nil Config or one not built with
// NewConfig, which has no address or credentials to send with.
var errNotBuilt = errors.New("latere: the Config is nil or was not built with NewConfig")

// Config is what a capability client is built from: where the API is, how a
// request is authenticated, and how it is sent and retried. Build one with
// NewConfig and share it; it is safe for concurrent use and does not change
// after NewConfig returns.
type Config struct {
	baseURL    *url.URL
	httpClient *http.Client
	tokens     TokenSource
	userAgent  string
	maxRetries int
	minBackoff time.Duration
	maxBackoff time.Duration
	timeout    time.Duration
	hooks      Hooks

	// sleep waits for d or until ctx ends. Tests replace it so a retry
	// takes no time.
	sleep func(ctx context.Context, d time.Duration) error
	// baseURLSet and tokensSet record that an option gave the value, so the
	// environment is read only when it did not.
	baseURLSet bool
	tokensSet  bool
	// noCredentials is set by WithoutCredentials: tokens is nil, and no
	// request carries a credential.
	noCredentials bool
}

// Hooks are called around every HTTP attempt, the token exchanges of
// NewKeyTokens and NewActorTokens included, for logging or metrics without
// the SDK depending on a telemetry library. Either may be nil. For tracing,
// prefer an instrumented transport given with WithHTTPClient.
type Hooks struct {
	// BeforeRequest is called with an attempt's request just before it is
	// sent. It may add headers; it must not read or replace the body. The
	// request carries credentials in its Authorization header: a bearer
	// token, or the key itself, for a key's exchange and for every request
	// that sets SendKey, as each of package llm's does. Never log that
	// header. A request to a signed URL, such as an upload's part, carries
	// its credential in the URL's query instead: log such a URL without
	// its query.
	BeforeRequest func(req *http.Request)
	// AfterResponse is called when an attempt ends, with its response or the
	// error that ended it, and how long it took. It must not read or close
	// the response body. A redirect's Location header is a credential too
	// when it leads to a signed link, as one to a file's bytes does: log
	// it without its query.
	AfterResponse func(req *http.Request, resp *http.Response, err error, took time.Duration)
}

// An Option sets one field of a Config.
type Option func(*Config) error

// NewConfig returns a Config with the options applied over the defaults:
// DefaultBaseURL, http.DefaultClient, DefaultMaxRetries, the backoff bounds
// and DefaultTimeout. Without WithKey, WithTokenSource or
// WithoutCredentials it uses the key in LATERE_KEY, and without WithBaseURL
// the address in LATERE_BASE_URL. It returns ErrNoCredentials when there
// are no credentials at all, and an error naming the option when an
// option's value is invalid.
func NewConfig(opts ...Option) (*Config, error) {
	c := &Config{
		httpClient: http.DefaultClient,
		maxRetries: DefaultMaxRetries,
		minBackoff: DefaultMinBackoff,
		maxBackoff: DefaultMaxBackoff,
		timeout:    DefaultTimeout,
		userAgent:  defaultUserAgent(),
		sleep:      sleepContext,
	}
	for _, opt := range opts {
		if err := opt(c); err != nil {
			return nil, err
		}
	}
	if !c.baseURLSet {
		raw := DefaultBaseURL
		if v := os.Getenv(EnvBaseURL); v != "" {
			raw = v
		}
		u, err := parseBaseURL(raw)
		if err != nil {
			return nil, fmt.Errorf("latere: %s: %w", EnvBaseURL, err)
		}
		c.baseURL = u
	}
	if !c.tokensSet {
		key := os.Getenv(EnvKey)
		if key == "" {
			return nil, ErrNoCredentials
		}
		kt, err := NewKeyTokens(key)
		if err != nil {
			return nil, fmt.Errorf("latere: %s: %w", EnvKey, err)
		}
		c.tokens = kt
	}
	// The token exchanges go through the same client, User-Agent and hooks
	// as the requests they sign, whichever order the options came in.
	if x, ok := c.tokens.(exchanger); ok {
		x.bind(&transport{client: c.httpClient, userAgent: c.userAgent, hooks: c.hooks})
	}
	return c, nil
}

// WithKey authenticates every request with key, a key created under Keys in
// the console, whose value starts with "pat_". The key is exchanged for a
// short-lived token, renewed before it expires; see KeyTokens.
func WithKey(key string) Option {
	return func(c *Config) error {
		kt, err := NewKeyTokens(key)
		if err != nil {
			return fmt.Errorf("latere: WithKey: %w", err)
		}
		c.tokens, c.tokensSet, c.noCredentials = kt, true, false
		return nil
	}
}

// WithTokenSource authenticates every request with a token from ts, asked
// before each attempt. Use it with NewActorTokens for a program that acts
// for a person who signed in.
func WithTokenSource(ts TokenSource) Option {
	return func(c *Config) error {
		if ts == nil {
			return errors.New("latere: WithTokenSource: the token source is nil")
		}
		c.tokens, c.tokensSet, c.noCredentials = ts, true, false
		return nil
	}
}

// WithoutCredentials sends every request with no credential at all, for a
// program that holds none, such as a server that shows a shared
// conversation to a visitor who has not signed in. Only the API's public
// reads answer such a request: a conversation's share that anyone may
// read, an app anyone may open and its pictures, a site's icon. Any other
// call is refused with 401, an error matching ErrUnauthorized. It takes
// the place of WithKey, WithTokenSource and LATERE_KEY, whichever came
// last wins, and a request that sets SendKey is refused before it is sent.
//
// The public reads are limited by the client address the API reads, so a
// server that reads for its visitors counts them all as itself. Where the
// installation takes the reader's address from an X-Real-IP header the
// server sends, as one reached inside its own network may, a
// Hooks.BeforeRequest given with WithHooks sets the header from the
// request's context, which carries the ctx the call was made with.
func WithoutCredentials() Option {
	return func(c *Config) error {
		c.tokens, c.tokensSet, c.noCredentials = nil, true, true
		return nil
	}
}

// WithBaseURL sends requests to the API at rawURL instead of DefaultBaseURL,
// for a self-hosted installation or a test server.
func WithBaseURL(rawURL string) Option {
	return func(c *Config) error {
		u, err := parseBaseURL(rawURL)
		if err != nil {
			return fmt.Errorf("latere: WithBaseURL: %w", err)
		}
		c.baseURL, c.baseURLSet = u, true
		return nil
	}
}

// WithHTTPClient sends requests, and the token exchanges of NewKeyTokens
// and NewActorTokens, with hc instead of http.DefaultClient. Give a client
// whose transport traces, measures or proxies requests. Bound calls with
// WithTimeout rather than hc's Timeout, which would also cut off streams.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Config) error {
		if hc == nil {
			return errors.New("latere: WithHTTPClient: the client is nil")
		}
		c.httpClient = hc
		return nil
	}
}

// WithUserAgent adds product, such as "my-app/1.2", to the User-Agent header
// before the SDK's own token, so the API's operators can tell your requests
// apart.
func WithUserAgent(product string) Option {
	return func(c *Config) error {
		product = strings.TrimSpace(product)
		if product == "" || strings.ContainsAny(product, "\r\n") {
			return errors.New("latere: WithUserAgent: the product is empty or holds a line break")
		}
		c.userAgent = product + " " + defaultUserAgent()
		return nil
	}
}

// WithMaxRetries sets how many times a failed request is sent again; zero
// turns retries off.
func WithMaxRetries(n int) Option {
	return func(c *Config) error {
		if n < 0 {
			return fmt.Errorf("latere: WithMaxRetries: %d is negative", n)
		}
		c.maxRetries = n
		return nil
	}
}

// WithBackoff sets the wait before the first retry, min, which doubles with
// each retry up to max. Each wait is jittered to between half and all of it.
func WithBackoff(min, max time.Duration) Option {
	return func(c *Config) error {
		if min <= 0 || max < min {
			return fmt.Errorf("latere: WithBackoff: want 0 < min <= max, have %v and %v", min, max)
		}
		c.minBackoff, c.maxBackoff = min, max
		return nil
	}
}

// WithTimeout bounds every call, its retries included, to d instead of
// DefaultTimeout when the call's context has no deadline of its own; zero
// leaves such calls unbounded. A stream is bounded only until the API's
// first answer, since it then lasts as long as the session. A request that
// sends bytes, Request.Content, bounds each attempt by d and TransferTime
// for their length instead, since sending them takes as long as the
// network allows.
func WithTimeout(d time.Duration) Option {
	return func(c *Config) error {
		if d < 0 {
			return fmt.Errorf("latere: WithTimeout: %v is negative", d)
		}
		c.timeout = d
		return nil
	}
}

// WithHooks calls h around every HTTP attempt.
func WithHooks(h Hooks) Option {
	return func(c *Config) error {
		c.hooks = h
		return nil
	}
}

// BaseURL returns the address requests are sent to; empty for a Config not
// built with NewConfig.
func (c *Config) BaseURL() string {
	if c == nil || c.baseURL == nil {
		return ""
	}
	return c.baseURL.String()
}

// built returns errNotBuilt when c cannot send a request.
func (c *Config) built() error {
	if c == nil || c.baseURL == nil || (c.tokens == nil && !c.noCredentials) || c.httpClient == nil || c.sleep == nil {
		return errNotBuilt
	}
	return nil
}

// parseBaseURL checks that raw is an absolute http or https URL and drops a
// trailing slash, so a request path joins it with exactly one.
func parseBaseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("%q is not an absolute http or https URL", raw)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("%q has a query or a fragment", raw)
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	return u, nil
}

// defaultUserAgent names the SDK, its version, the Go release and the
// platform, as "latere-sdk-go/0.1.0 (go1.27.3; darwin/arm64)".
func defaultUserAgent() string {
	return fmt.Sprintf("latere-sdk-go/%s (%s; %s/%s)", Version, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

// sleepContext waits for d, returning early with ctx's error when ctx ends.
func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// NewIdempotencyKey returns a random key for Request.IdempotencyKey. Reuse
// the same key when you send the same request again yourself, so the API
// answers the repeat with the first answer instead of doing the work twice.
func NewIdempotencyKey() string { return rand.Text() }
