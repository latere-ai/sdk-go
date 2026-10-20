// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"latere.ai/x/sdk-go/latere"
)

// The models capability's routes for the Lux dialect.
const (
	generatePath    = "/v1/models/lux/v1/generate"
	countTokensPath = "/v1/models/lux/v1/count_tokens"
	modelsPath      = "/v1/models/lux/v1/models"
)

// The headers the models capability answers with.
const (
	// lossHeader names the request's fields the model's provider could not
	// represent, comma separated.
	lossHeader = "Lux-Loss"
	// estimatedHeader is "true" on a token count that is an estimate.
	estimatedHeader = "Lux-Estimated"
	// labelsHeader carries the request's labels.
	labelsHeader = "Lux-Labels"
	// requestIDHeader names the request in the service's records.
	requestIDHeader = "Lux-Request-Id"
)

// maxResponse bounds how much of a generation's answer is read: well past
// any answer a model's output limit allows, so it stops only a broken
// server.
const maxResponse = 64 << 20

// Client calls models through the models capability of the Latere API, in
// the Lux dialect. It is safe for concurrent use.
//
// The models capability authenticates a key as itself, so the Config's
// credentials are a key: WithKey, or the LATERE_KEY variable. A Config built
// with WithTokenSource sends its token unchanged, which the models
// capability accepts only when that token is a key's value.
type Client struct {
	cfg    *latere.Config
	labels string
}

// An Option configures a Client.
type Option func(*Client)

// WithLabels labels every request the Client sends, for your own reporting:
// the service records the labels with the request and shows them in its
// request history. Labels never change who is billed or what the key may
// do. The service keeps up to 8 labels, each key 1 to 64 characters of
// letters, digits and ._- and each value 1 to 128 characters of letters,
// digits and ._:/-. A label outside that rule is left out before the
// request is sent, as the service would drop it, so a value holding a
// comma or an equals sign cannot change the labels beside it; past 8
// valid labels, those first in key order are sent. Build one Client per
// set of labels; Clients share their Config.
func WithLabels(labels map[string]string) Option {
	return func(c *Client) { c.labels = formatLabels(labels) }
}

// New returns a Client that sends its requests as cfg says. A cfg not built
// with latere.NewConfig makes every call fail with an error saying so.
func New(cfg *latere.Config, opts ...Option) *Client {
	c := &Client{cfg: cfg}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Result is a complete answer and what the service reported about it.
type Result struct {
	Response
	// Loss names the request's fields the model's provider could not
	// represent, which were left out rather than dropped silently. Empty
	// when the provider took every field.
	Loss []string
	// RequestID names the request in the service's records; quote it when
	// you report a problem.
	RequestID string
}

// errNoRequest is returned before any request when req is nil.
var errNoRequest = errors.New("llm: the request is nil")

// Generate sends req and waits for the whole answer. It sets req's Stream to
// false on the copy it sends, so the same Request serves Stream too.
//
// The answer must arrive within the Config's timeout, latere.DefaultTimeout
// unless WithTimeout changed it, or within ctx's deadline when ctx has one;
// for a long answer, use Stream or a longer deadline.
//
// Generate is sent once, never again after a failure, since the model may
// have run and been billed: a refusal returns a *latere.Error, a failure on
// the way the transport's error.
func (c *Client) Generate(ctx context.Context, req *Request) (*Result, error) {
	if req == nil {
		return nil, errNoRequest
	}
	wire := *req
	wire.Stream = false
	resp, err := c.cfg.Open(ctx, c.request(generatePath, &wire, latere.RetryNever))
	if err != nil {
		return nil, err
	}
	defer closeBody(resp)
	out := &Result{Loss: parseLoss(resp.Header), RequestID: resp.Header.Get(requestIDHeader)}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponse)).Decode(&out.Response); err != nil {
		return nil, fmt.Errorf("llm: decoding the answer: %w", causeOr(ctx, err))
	}
	return out, nil
}

// TokenCount is how many input tokens a request holds.
type TokenCount struct {
	// InputTokens is the count.
	InputTokens int64
	// Estimated says the count is an estimate, because the model's provider
	// has no way to count exactly; use it for rough budgeting only.
	Estimated bool
	// RequestID names the request in the service's records.
	RequestID string
}

// CountTokens returns how many input tokens req holds, without generating
// anything. It is not billed, and counts as one request against the key's
// rate limit. Since counting changes nothing, it is sent again after a
// failure that may pass, as the Config's retries allow.
func (c *Client) CountTokens(ctx context.Context, req *Request) (*TokenCount, error) {
	if req == nil {
		return nil, errNoRequest
	}
	wire := *req
	wire.Stream = false
	resp, err := c.cfg.Open(ctx, c.request(countTokensPath, &wire, latere.RetryAlways))
	if err != nil {
		return nil, err
	}
	defer closeBody(resp)
	var body struct {
		InputTokens int64 `json:"input_tokens"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponse)).Decode(&body); err != nil {
		return nil, fmt.Errorf("llm: decoding the token count: %w", causeOr(ctx, err))
	}
	return &TokenCount{
		InputTokens: body.InputTokens,
		Estimated:   resp.Header.Get(estimatedHeader) == "true",
		RequestID:   resp.Header.Get(requestIDHeader),
	}, nil
}

// Model is a catalog model the key may call.
type Model struct {
	// ID is the model's catalog name, "vendor/model", for Request.Model.
	ID string
	// OwnedBy is who offers the model.
	OwnedBy string
	// Created is when the model was added to the catalog; zero when the
	// service does not say.
	Created time.Time
	// ContextWindow is how many tokens the model reads in one request, and
	// MaxOutputTokens how many it writes in one answer; zero when unknown.
	ContextWindow   int
	MaxOutputTokens int
	// InputModalities are the kinds of content the model reads, such as
	// "text" and "image".
	InputModalities []string
	// Pricing is the model's prices, or nil when the catalog lists none.
	Pricing *Pricing
}

// Pricing is a model's prices, each a decimal string, as exact as the
// catalog states it, per Per tokens.
type Pricing struct {
	// Currency is an ISO 4217 code, such as "USD".
	Currency string
	// Per is how many tokens each price is for, 1,000,000.
	Per int
	// Input and Output are the prices of input and output tokens;
	// CachedInput of input read from the prompt cache, and CacheWrite of
	// input written to it. An empty price is not listed.
	Input, Output, CachedInput, CacheWrite string
}

// modelEntry is one model as the service lists it.
type modelEntry struct {
	ID              string   `json:"id"`
	Created         int64    `json:"created"`
	OwnedBy         string   `json:"owned_by"`
	ContextWindow   int      `json:"context_window"`
	MaxOutputTokens int      `json:"max_output_tokens"`
	InputModalities []string `json:"input_modalities"`
	Pricing         *struct {
		Currency    string `json:"currency"`
		Per         int    `json:"per"`
		Input       string `json:"input"`
		Output      string `json:"output"`
		CachedInput string `json:"cached_input"`
		CacheWrite  string `json:"cache_write"`
	} `json:"pricing"`
}

// ListModels returns the catalog models the key may call, sorted by ID.
func (c *Client) ListModels(ctx context.Context) ([]Model, error) {
	var body struct {
		Data []modelEntry `json:"data"`
	}
	r := &latere.Request{Method: http.MethodGet, Path: modelsPath, SendKey: true}
	if err := c.cfg.Do(ctx, r, &body); err != nil {
		return nil, err
	}
	out := make([]Model, 0, len(body.Data))
	for _, e := range body.Data {
		m := Model{
			ID:              e.ID,
			OwnedBy:         e.OwnedBy,
			ContextWindow:   e.ContextWindow,
			MaxOutputTokens: e.MaxOutputTokens,
			InputModalities: e.InputModalities,
		}
		if e.Created > 0 {
			m.Created = time.Unix(e.Created, 0).UTC()
		}
		if p := e.Pricing; p != nil {
			m.Pricing = &Pricing{Currency: p.Currency, Per: p.Per, Input: p.Input, Output: p.Output, CachedInput: p.CachedInput, CacheWrite: p.CacheWrite}
		}
		out = append(out, m)
	}
	slices.SortFunc(out, func(a, b Model) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

// request builds a POST of body to path, with the key itself as the
// credential, the Client's labels, and retry, the rule for sending it
// again. The models capability reads no idempotency key, so none is sent.
func (c *Client) request(path string, body *Request, retry latere.RetryRule) *latere.Request {
	r := &latere.Request{Method: http.MethodPost, Path: path, Body: body, SendKey: true, Retry: retry}
	if c.labels != "" {
		r.Header = http.Header{labelsHeader: {c.labels}}
	}
	return r
}

// maxLabels, maxLabelKey and maxLabelValue are the bounds the service
// keeps labels to.
const (
	maxLabels     = 8
	maxLabelKey   = 64
	maxLabelValue = 128
)

// formatLabels writes labels in the header's form: key=value pairs sorted
// by key, joined by commas, at most maxLabels of them. A label the service
// would drop is left out, so no key or value can hold the comma or the
// equals sign the form separates pairs with. A nil or empty map yields "".
func formatLabels(labels map[string]string) string {
	var b strings.Builder
	n := 0
	for _, k := range slices.Sorted(maps.Keys(labels)) {
		v := labels[k]
		if n == maxLabels || !labelText(k, maxLabelKey, "") || !labelText(v, maxLabelValue, ":/") {
			continue
		}
		if n > 0 {
			b.WriteByte(',')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(v)
		n++
	}
	return b.String()
}

// labelText reports whether s is 1 to max characters of letters, digits,
// '.', '_', '-' and the characters extra names.
func labelText(s string, max int, extra string) bool {
	if s == "" || len(s) > max {
		return false
	}
	for _, r := range s {
		switch {
		case 'a' <= r && r <= 'z', 'A' <= r && r <= 'Z', '0' <= r && r <= '9', strings.ContainsRune("._-"+extra, r):
		default:
			return false
		}
	}
	return true
}

// parseLoss reads the fields the provider could not represent.
func parseLoss(h http.Header) []string {
	v := h.Get(lossHeader)
	if v == "" {
		return nil
	}
	return strings.Split(v, ",")
}

// causeOr returns why ctx ended, when it has, and err otherwise, so a read
// cut off by the deadline matches context.DeadlineExceeded.
func causeOr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	return err
}

// closeBody closes an answer's body. The answer was read to its end or is
// abandoned, so a failed close changes nothing for the caller and its error
// is dropped.
func closeBody(resp *http.Response) {
	_ = resp.Body.Close()
}
