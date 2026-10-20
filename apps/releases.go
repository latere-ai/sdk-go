// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package apps

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"latere.ai/x/sdk-go/latere"
)

// ReleaseStatus is where a release is.
type ReleaseStatus string

// Release statuses.
const (
	// ReleasePending is a release waiting for its deploy.
	ReleasePending ReleaseStatus = "pending"
	// ReleaseReleased is the release the app's address serves.
	ReleaseReleased ReleaseStatus = "released"
	// ReleaseSuperseded is a release a newer one replaced; Reason says
	// how.
	ReleaseSuperseded ReleaseStatus = "superseded"
	// ReleaseRefused is a release the platform did not accept; Reason
	// says why.
	ReleaseRefused ReleaseStatus = "refused"
	// ReleaseFailed is a release whose deploy failed.
	ReleaseFailed ReleaseStatus = "failed"
)

// Release is a pushed tag that starts with "v", which publishes its commit
// at the app's address.
type Release struct {
	// ID identifies the release.
	ID string `json:"id"`
	// Tag is the tag that was pushed.
	Tag string `json:"tag"`
	// CommitSHA is the commit the tag names, which never changes.
	CommitSHA string `json:"commit_sha"`
	// Status is where the release is.
	Status ReleaseStatus `json:"status"`
	// Reason says why the release has its status, for a superseded or
	// refused release.
	Reason string `json:"reason"`
	// SourceDeploy is the ID of the preview of the release's commit; empty
	// until there is one.
	SourceDeploy string `json:"source_deploy"`
	// Deploy is the ID of the deploy that serves the release; empty until
	// it is created.
	Deploy string `json:"deploy"`
	// Via is how the release was asked for: "push" or "api".
	Via string `json:"via"`
	// CreatedBy is the subject that asked for the release.
	CreatedBy string `json:"created_by"`
	// CreatedAt is when the release was asked for.
	CreatedAt time.Time `json:"created_at"`
	// ReleasedAt is when it went live; zero until then.
	ReleasedAt time.Time `json:"released_at,omitzero"`
	// SupersededAt is when a newer release replaced it; zero until then.
	SupersededAt time.Time `json:"superseded_at,omitzero"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the release, with every
// field; nil for a Release the program built itself.
func (r *Release) RawJSON() json.RawMessage { return r.raw }

// UnmarshalJSON decodes the release and keeps the JSON for RawJSON.
func (r *Release) UnmarshalJSON(b []byte) error {
	type plain Release
	if err := json.Unmarshal(b, (*plain)(r)); err != nil {
		return err
	}
	r.raw = keep(b)
	return nil
}

// ReleaseDetail is a release with the deploys it names.
type ReleaseDetail struct {
	// Release is the release.
	Release Release `json:"release"`
	// SourceDeploy is the preview of the release's commit; nil until there
	// is one.
	SourceDeploy *Deploy `json:"source_deploy,omitempty"`
	// Deploy is the deploy that serves the release; nil until it is
	// created.
	Deploy *Deploy `json:"deploy,omitempty"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered, with every field; nil for a
// ReleaseDetail the program built itself.
func (r *ReleaseDetail) RawJSON() json.RawMessage { return r.raw }

// UnmarshalJSON decodes the detail and keeps the JSON for RawJSON.
func (r *ReleaseDetail) UnmarshalJSON(b []byte) error {
	type plain ReleaseDetail
	if err := json.Unmarshal(b, (*plain)(r)); err != nil {
		return err
	}
	r.raw = keep(b)
	return nil
}

// ListReleases returns every release of the app slug names, newest first.
func (c *Client) ListReleases(ctx context.Context, slug string) ([]*Release, error) {
	if slug == "" {
		return nil, errNoSlug
	}
	var out struct {
		Releases []*Release `json:"releases"`
	}
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodGet, Path: appPath(slug, "releases")}, &out); err != nil {
		return nil, err
	}
	return out.Releases, nil
}

// GetRelease returns the release tag names, with its deploys. It returns
// an error matching latere.ErrNotFound when the app has no such release.
func (c *Client) GetRelease(ctx context.Context, slug, tag string) (*ReleaseDetail, error) {
	if slug == "" {
		return nil, errNoSlug
	}
	if tag == "" {
		return nil, errors.New("apps: the release's tag is empty")
	}
	var out ReleaseDetail
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodGet, Path: appPath(slug, "releases", tag)}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
