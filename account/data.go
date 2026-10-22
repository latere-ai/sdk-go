// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package account

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"

	"latere.ai/x/sdk-go/latere"
)

// DataPlaces are where the installation keeps sessions and runs their
// machines, as its operator states them.
type DataPlaces struct {
	// Sessions is where sessions are kept; nil when the operator has not
	// stated it, which a program says rather than guess one.
	Sessions *Place `json:"sessions,omitempty"`
	// Machines is where a session's machine runs; nil when not stated.
	Machines *Place `json:"machines,omitempty"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the places, with every
// field, including those this package does not model; nil for a DataPlaces
// the program built itself.
func (d *DataPlaces) RawJSON() json.RawMessage { return d.raw }

// UnmarshalJSON decodes the places and keeps the JSON for RawJSON.
func (d *DataPlaces) UnmarshalJSON(b []byte) error {
	type plain DataPlaces
	if err := json.Unmarshal(b, (*plain)(d)); err != nil {
		return err
	}
	d.raw = keep(b)
	return nil
}

// A Place is a country and a place within it.
type Place struct {
	// Country is an ISO 3166-1 alpha-2 country code, such as "DE".
	Country string `json:"country"`
	// Place is the place within the country, such as "Frankfurt".
	Place string `json:"place"`
}

// GetDataPlaces returns where the installation keeps sessions and where it
// runs a session's machine, as its operator states them.
func (c *Client) GetDataPlaces(ctx context.Context) (*DataPlaces, error) {
	var out DataPlaces
	if err := c.get(ctx, "/v1/account/data", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// MaxDataTermsModels is the most models one GetModelDataTerms names.
const MaxDataTermsModels = 20

// ModelDataTerms are the data terms of the models a GetModelDataTerms
// named, one entry each, in the order named.
type ModelDataTerms struct {
	// Models are the terms, one for each name asked.
	Models []ModelTerms `json:"models"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the terms, with every
// field, including those this package does not model; nil for a
// ModelDataTerms the program built itself.
func (m *ModelDataTerms) RawJSON() json.RawMessage { return m.raw }

// UnmarshalJSON decodes the terms and keeps the JSON for RawJSON.
func (m *ModelDataTerms) UnmarshalJSON(b []byte) error {
	type plain ModelDataTerms
	if err := json.Unmarshal(b, (*plain)(m)); err != nil {
		return err
	}
	m.raw = keep(b)
	return nil
}

// ModelTerms are one model's data terms, its name as a person reads it,
// and its price. A name the catalog does not hold has Name alone, and a
// model whose terms have not been read yet its names alone.
type ModelTerms struct {
	// Name is the model's name as it was asked, "<vendor>/<model>".
	Name string `json:"name"`
	// DisplayName is the name a person reads, such as "DeepSeek V4 Flash"
	// for "deepseek/deepseek-v4-flash"; empty when the catalog states none,
	// and the model is then named by Name.
	DisplayName string `json:"display_name,omitempty"`
	// Retention is "none" when no provider that serves the model keeps a
	// request, and "provider" when a request may reach one that keeps it
	// under its own terms; empty when the terms are not known.
	Retention string `json:"retention,omitempty"`
	// ServedBy are the providers that serve the model, present with
	// Retention. A model served by several does not say which one answered
	// a given request.
	ServedBy []ServingProvider `json:"served_by,omitempty"`
	// Omitted is how many serving providers ServedBy leaves out, present
	// with Retention.
	Omitted int `json:"omitted,omitempty"`
	// Price is what a request to the model is charged at; nil when the
	// catalog does not hold the model or it carries no price.
	Price *Price `json:"price,omitempty"`
}

// A ServingProvider is a provider that serves a model.
type ServingProvider struct {
	// Provider is the provider's name.
	Provider string `json:"provider"`
	// Retention is "none" when every endpoint of this provider for the
	// model keeps nothing, and "provider" otherwise.
	Retention string `json:"retention"`
	// Countries are the countries the provider states it runs in, as ISO
	// 3166-1 alpha-2 codes; empty when it states none.
	Countries []string `json:"countries"`
}

// GetModelDataTerms returns the data terms of the models names lists, as
// a session's log names the model that ran a turn, such as
// "anthropic/claude-sonnet-5.5": whether a request to each can be kept by
// a provider that serves it, the providers with the countries each
// states, the name a person reads and the price. The terms are those
// listed today, not those of the day a turn ran.
//
// It names at least one model and at most MaxDataTermsModels, none of them
// empty, or it fails before any request. A catalog that cannot be read now
// is answered with 503 and the code "data_terms_unavailable".
func (c *Client) GetModelDataTerms(ctx context.Context, names ...string) (*ModelDataTerms, error) {
	// The API counts the distinct names, so a name given twice is sent
	// once and counted once.
	q := url.Values{}
	for _, n := range names {
		if n == "" {
			return nil, errors.New("account: GetModelDataTerms: a model's name is empty")
		}
		if !slices.Contains(q["name"], n) {
			q.Add("name", n)
		}
	}
	if n := len(q["name"]); n == 0 || n > MaxDataTermsModels {
		return nil, fmt.Errorf("account: GetModelDataTerms names %d distinct models; it names 1 to %d", n, MaxDataTermsModels)
	}
	var out ModelDataTerms
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodGet, Path: "/v1/account/data/models", Query: q}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
