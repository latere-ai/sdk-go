// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package evals

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"latere.ai/x/sdk-go/latere"
)

var errNoDefinition = errors.New("evals: the definition is nil")

// errPreconditions is an apply that sets both preconditions, which no
// name can satisfy: one needs a current version and the other none.
var errPreconditions = errors.New("evals: IfMatch and OnlyNew cannot both be set")

// ApplyParams are the choices of an apply. A nil *ApplyParams applies the
// definition in the context the token names, whatever the name holds.
type ApplyParams struct {
	// Owner is the context to apply in, a rendered subject; empty applies
	// in the context the token names.
	Owner string
	// IfMatch is a digest that must be the name's current version for the
	// apply to go through. When it is not, or the name has no version, the
	// call returns an error matching latere.ErrPreconditionFailed. It
	// makes a program that read a definition, changed it and applies it
	// back refuse to overwrite a change made in between.
	IfMatch string
	// OnlyNew applies only while the name has no version, so a program
	// that creates a definition never replaces one. When the name has a
	// version, the call returns an error matching
	// latere.ErrPreconditionFailed. IfMatch and OnlyNew are not set
	// together.
	OnlyNew bool
}

// conditional reports whether the apply sets a precondition.
func (p *ApplyParams) conditional() bool {
	return p != nil && (p.IfMatch != "" || p.OnlyNew)
}

// Applied is what an apply answers: the version of the name that is now
// current.
type Applied[T any] struct {
	// Version is the version as the API answers it, with its record.
	Version *T
	// Created reports a new version, which the API answers with 201. It is
	// false when the content already was a version of the name, which the
	// apply made current again and stored nothing for, answered with 200.
	Created bool
}

// recordMembers are the members of a Version that the API writes. A body
// that holds one is refused with "unknown_field", and the name is the
// path's.
var recordMembers = []string{"id", "owner", "name", "digest", "current", "created_at"}

// applyBody is the body an apply sends for def: its own members, the
// record's left out.
func applyBody(def any) (map[string]jsontext.Value, error) {
	b, err := json.Marshal(def)
	if err != nil {
		return nil, fmt.Errorf("evals: encoding the definition: %w", err)
	}
	var body map[string]jsontext.Value
	if err := json.Unmarshal(b, &body); err != nil {
		return nil, fmt.Errorf("evals: encoding the definition: %w", err)
	}
	for _, k := range recordMembers {
		delete(body, k)
	}
	return body, nil
}

// apply applies def as a version of name in the collection at kind. A
// repeat of an apply that holds a precondition may find that it no longer
// holds, so it is not sent again after an answer is lost; any other apply
// has the same effect sent twice.
func apply[T any](ctx context.Context, c *Client, kind string, def *T, name string, p *ApplyParams) (*Applied[T], error) {
	seg, err := segment(name, errNoName)
	if err != nil {
		return nil, err
	}
	if p != nil && p.IfMatch != "" && p.OnlyNew {
		return nil, errPreconditions
	}
	body, err := applyBody(def)
	if err != nil {
		return nil, err
	}
	req := &latere.Request{Method: http.MethodPut, Path: kind + "/" + seg, Body: body, Retry: latere.RetryByMethod}
	if p != nil {
		if p.Owner != "" {
			req.Query = url.Values{"owner": {p.Owner}}
		}
		switch {
		case p.IfMatch != "":
			req.Header = http.Header{"If-Match": {`"` + strings.Trim(p.IfMatch, `"`) + `"`}}
		case p.OnlyNew:
			req.Header = http.Header{"If-None-Match": {"*"}}
		}
	}
	if p.conditional() {
		req.Retry = latere.RetryAnswers
	}
	resp, err := c.cfg.Open(ctx, req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	out := Applied[T]{Version: new(T), Created: resp.StatusCode == http.StatusCreated}
	if err := json.UnmarshalDecode(jsontext.NewDecoder(resp.Body), out.Version); err != nil {
		return nil, fmt.Errorf("evals: decoding the answer to PUT %s: %w", req.Path, err)
	}
	return &out, nil
}

// ApplyMetric applies m as a version of the metric named m.Name, and
// returns the version that is now current. Content that already is a
// version of the name is made current again and stored once: the answer's
// Created is false. The record members of m, its ID, Owner, Digest,
// Current and CreatedAt, are not sent, so a Metric read with GetMetric,
// changed and applied is the new version of its name. A precondition
// params sets that does not hold returns an error matching
// latere.ErrPreconditionFailed. A definition the API refuses, with 400,
// 413 or 422, returns a *latere.Error whose Code and Field say why, such
// as "reference_not_found" for a digest the context holds no version by.
// params may be nil.
func (c *Client) ApplyMetric(ctx context.Context, m *Metric, params *ApplyParams) (*Applied[Metric], error) {
	if m == nil {
		return nil, errNoDefinition
	}
	return apply(ctx, c, metricsPath, m, m.Name, params)
}

// ApplyGrader applies g as a version of the grader named g.Name, and
// returns the version that is now current. It sends and answers as
// ApplyMetric does, and a model judge's Preamble is the platform's: send
// it as read or leave it empty. params may be nil.
func (c *Client) ApplyGrader(ctx context.Context, g *Grader, params *ApplyParams) (*Applied[Grader], error) {
	if g == nil {
		return nil, errNoDefinition
	}
	return apply(ctx, c, gradersPath, g, g.Name, params)
}

// ApplyTask applies t as a version of the task named t.Name, and returns
// the version that is now current. It sends and answers as ApplyMetric
// does; each of t's Checks is the digest of a grader of kind check the
// context holds. params may be nil.
func (c *Client) ApplyTask(ctx context.Context, t *Task, params *ApplyParams) (*Applied[Task], error) {
	if t == nil {
		return nil, errNoDefinition
	}
	return apply(ctx, c, tasksPath, t, t.Name, params)
}

// ApplySuite applies s as a version of the suite named s.Name, and
// returns the version that is now current. It sends and answers as
// ApplyMetric does; the tasks, metrics and graders s names are digests of
// versions the context holds. params may be nil.
func (c *Client) ApplySuite(ctx context.Context, s *Suite, params *ApplyParams) (*Applied[Suite], error) {
	if s == nil {
		return nil, errNoDefinition
	}
	return apply(ctx, c, suitesPath, s, s.Name, params)
}

// ApplyCandidate applies cand as a version of the candidate named
// cand.Name, and returns the version that is now current. It sends and
// answers as ApplyMetric does. params may be nil.
func (c *Client) ApplyCandidate(ctx context.Context, cand *Candidate, params *ApplyParams) (*Applied[Candidate], error) {
	if cand == nil {
		return nil, errNoDefinition
	}
	return apply(ctx, c, candidatesPath, cand, cand.Name, params)
}
