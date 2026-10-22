// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package apps

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"latere.ai/x/sdk-go/latere"
)

// basePath is where the API serves apps, their deploys and their releases.
// The API serves the apps document's routes under /v1/apps, so the
// document's /v1/apps is /v1/apps/apps here.
const basePath = "/v1/apps/apps"

// errNoSlug is returned before any request when an app's slug is empty.
var errNoSlug = errors.New("apps: the app's slug is empty")

// appPath is the path of an app, or of one of its sub-resources: rest is
// the segments after the app's, each escaped.
func appPath(slug string, rest ...string) string {
	p := basePath + "/" + url.PathEscape(slug)
	for _, r := range rest {
		p += "/" + url.PathEscape(r)
	}
	return p
}

// Client reaches the apps part of the Latere API. It is safe for
// concurrent use.
type Client struct {
	cfg *latere.Config
}

// New returns a Client that sends its requests as cfg says. A cfg not
// built with latere.NewConfig makes every call fail with an error saying
// so.
func New(cfg *latere.Config) *Client { return &Client{cfg: cfg} }

// Visibility says who may open an app's address.
type Visibility string

// Visibilities.
const (
	// VisibilityPublic opens the app to anyone with its address and lists
	// it in the gallery. An app becomes public when its gallery listing is
	// approved; a create that asks for it gets VisibilityUnlisted.
	VisibilityPublic Visibility = "public"
	// VisibilityUnlisted opens the app to anyone with its address, and
	// keeps it out of search engines.
	VisibilityUnlisted Visibility = "unlisted"
	// VisibilityPrivate opens the app to its owner, an organization's
	// members and the people it is shared with, each after signing in, and
	// to anyone with the app's password when it has one.
	VisibilityPrivate Visibility = "private"
)

// AppState is whether an app is published.
type AppState string

// App states.
const (
	// AppActive is a published app.
	AppActive AppState = "active"
	// AppPaused is an app its owner or the platform unpublished.
	AppPaused AppState = "paused"
	// AppDeleting is an app being deleted.
	AppDeleting AppState = "deleting"
)

// Health is how an app's live deploy is doing.
type Health string

// Healths.
const (
	// HealthNone is an app with no live deploy.
	HealthNone Health = "none"
	// HealthReady is an app that answers.
	HealthReady Health = "ready"
	// HealthDegraded is an app that answers some requests.
	HealthDegraded Health = "degraded"
	// HealthCrashloop is an app whose service keeps exiting.
	HealthCrashloop Health = "crashloop"
	// HealthSleeping is an app asleep after a while without requests; the
	// next request wakes it.
	HealthSleeping Health = "sleeping"
)

// OwnerKind is who owns an app.
type OwnerKind string

// Owner kinds.
const (
	// OwnerPersonal is a person's own app.
	OwnerPersonal OwnerKind = "personal"
	// OwnerOrganization is an organization's app.
	OwnerOrganization OwnerKind = "organization"
)

// Action is something the caller may do with an app now.
type Action string

// Actions, in the order a program shows them.
const (
	// ActionOpen opens the app.
	ActionOpen Action = "open"
	// ActionCopyLink copies the app's address.
	ActionCopyLink Action = "copy_link"
	// ActionShare changes who may open a private app.
	ActionShare Action = "share"
	// ActionUnpublish takes the app off its address.
	ActionUnpublish Action = "unpublish"
	// ActionPublish puts it back on its address.
	ActionPublish Action = "publish"
	// ActionChangeVisibility changes whether the app is public.
	ActionChangeVisibility Action = "change_visibility"
	// ActionDelete deletes the app.
	ActionDelete Action = "delete"
)

// PreviewVisibility says who may open an app's previews.
type PreviewVisibility string

// Preview visibilities.
const (
	// PreviewsPrivate opens previews to the people who may open the app
	// when it is private, each after signing in, whatever the app's own
	// visibility. It is the default.
	PreviewsPrivate PreviewVisibility = "private"
	// PreviewsInherit gives previews the app's visibility.
	PreviewsInherit PreviewVisibility = "inherit"
)

// App is an app: a git repository and an address.
type App struct {
	// ID never changes, and is also the ID of the app's repository.
	ID string `json:"id"`
	// Slug is the app's address and names it in every call.
	Slug string `json:"slug"`
	// Name is the display name.
	Name string `json:"name"`
	// Owner is the account the app belongs to.
	Owner Owner `json:"owner"`
	// Visibility says who may open the app's address.
	Visibility Visibility `json:"visibility"`
	// State says whether the app is published.
	State AppState `json:"state"`
	// Health is how the app's live deploy is doing.
	Health Health `json:"health"`
	// URL is the app's address, which serves its live release.
	URL string `json:"url"`
	// PreviewURL is the address of the app's newest preview.
	PreviewURL string `json:"preview_url"`
	// CurrentDeploy is the deploy the app's address serves; nil before the
	// first release.
	CurrentDeploy *DeploySummary `json:"current_deploy"`
	// LatestPreview is the newest ready preview; nil when none is ready.
	LatestPreview *DeploySummary `json:"latest_preview"`
	// Session is the session whose agent created the app; nil for an app
	// a person created.
	Session *SessionRef `json:"session"`
	// PublishedBySession is the session whose commit the app's address
	// serves; nil when that commit names none.
	PublishedBySession *SessionRef `json:"published_by_session"`
	// PublishedAt is when the deploy the address serves went live; zero
	// before the first release.
	PublishedAt time.Time `json:"published_at,omitzero"`
	// Actions are what the caller may do with the app now, in the order to
	// show them. Each is still decided when it is taken.
	Actions []Action `json:"actions"`
	// Thumbnail is the app's picture; nil when there is none.
	Thumbnail *Thumbnail `json:"thumbnail"`
	// Settings are the app's settings with the defaults applied.
	Settings Settings `json:"settings"`
	// Repository is the app's git repository.
	Repository Repository `json:"repository"`
	// SlugGenerated reports, in the answer of a create that named no slug,
	// that the slug was derived from the name.
	SlugGenerated bool `json:"slug_generated"`
	// Restored reports, in the answer of a create, that it restored an app
	// of the same slug deleted within the last seven days.
	Restored bool `json:"restored,omitempty"`
	// CreatedAt is when the app was created.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is when the app last changed.
	UpdatedAt time.Time `json:"updated_at"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the app, with every field,
// including those this package does not model; nil for an App the program
// built itself.
func (a *App) RawJSON() json.RawMessage { return a.raw }

// UnmarshalJSON decodes the app and keeps the JSON for RawJSON.
func (a *App) UnmarshalJSON(b []byte) error {
	type plain App
	if err := json.Unmarshal(b, (*plain)(a)); err != nil {
		return err
	}
	a.raw = keep(b)
	return nil
}

// Owner is the account an app belongs to.
type Owner struct {
	// Kind is OwnerPersonal or OwnerOrganization.
	Kind OwnerKind `json:"kind"`
	// ID is the person's subject or the organization's ID.
	ID string `json:"id"`
}

// SessionRef names a session of the agents capability by its ID.
type SessionRef struct {
	// ID is the session's ID.
	ID string `json:"id"`
}

// Thumbnail is a screenshot of a deploy's front page. Read its bytes with
// ReadThumbnail, which takes the app's slug and the deploy's ID that
// Deploy returns from URL.
type Thumbnail struct {
	// URL is the picture's path under the API's address, which names the
	// app and the deploy whose picture it is, so a newer deploy's picture
	// has another URL.
	URL string `json:"url"`
	// Width is the picture's width in pixels.
	Width int `json:"width"`
	// Height is the picture's height in pixels.
	Height int `json:"height"`
}

// Settings are an app's settings with the defaults applied.
type Settings struct {
	// SleepAfterSeconds is how long the app runs without a request before
	// it sleeps; 1800 unless set.
	SleepAfterSeconds int `json:"sleep_after_seconds"`
	// AlwaysOn reports that the app never sleeps.
	AlwaysOn bool `json:"always_on"`
	// Previews says who may open the app's previews.
	Previews PreviewSettings `json:"previews"`
}

// PreviewSettings say who may open an app's previews.
type PreviewSettings struct {
	// Visibility is PreviewsPrivate, the default, or PreviewsInherit.
	Visibility PreviewVisibility `json:"visibility"`
}

// Repository is an app's git repository.
type Repository struct {
	// CloneURL addresses the repository by the app's ID, which a rename
	// does not change.
	CloneURL string `json:"clone_url"`
	// PushURL addresses the repository by its owner and the app's slug;
	// push here to publish.
	PushURL string `json:"push_url"`
	// SSHURL addresses the repository over SSH; empty where the platform
	// serves git over HTTPS alone.
	SSHURL string `json:"ssh_url,omitempty"`
}

// CreateAppParams describes a new app. Every field is optional.
type CreateAppParams struct {
	// Name is the display name. Without a Slug, the slug is derived from
	// it.
	Name string `json:"name,omitempty"`
	// Slug is the app's address: 1 to 40 lowercase letters, digits and
	// single hyphens, not starting or ending with one. Without a Slug or a
	// Name, one is generated.
	Slug string `json:"slug,omitempty"`
	// Visibility says who may open the app; empty leaves the API's
	// default.
	Visibility Visibility `json:"visibility,omitempty"`
}

// CreateApp creates an app and its repository in the account the token
// acts in, and returns it. When the account deleted an app of the same
// slug within the last seven days, the create restores that app, with its
// ID and repository, and App.Restored reports so; it serves nothing until
// the next push. A slug another app holds returns an error matching
// latere.ErrConflict. params may be nil.
//
// CreateApp is sent once, never again after a failure: the apps API takes
// no idempotency key, so a repeat could not be told from a second create.
func (c *Client) CreateApp(ctx context.Context, params *CreateAppParams) (*App, error) {
	if params == nil {
		params = &CreateAppParams{}
	}
	var out App
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodPost, Path: basePath, Body: params, Retry: latere.RetryNever}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetApp returns the app slug names; slug may also be the app's ID. It
// returns an error matching latere.ErrNotFound when the caller has no
// such app.
//
// With a Config built with latere.WithoutCredentials it reads an app as
// anyone does: it answers only an app anyone with its address may open,
// public or unlisted and published, among every account's apps, and only
// its ID, Slug, Name and URL, as an OpenApp holds them; every other field
// is zero. Any other app returns an error matching latere.ErrNotFound, and
// such reads are limited to 120 a minute from one address, past which it
// returns an error matching latere.ErrRateLimited once its retries are
// spent.
func (c *Client) GetApp(ctx context.Context, slug string) (*App, error) {
	if slug == "" {
		return nil, errNoSlug
	}
	var out App
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodGet, Path: appPath(slug)}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AppOrder is the order ListApps returns apps in.
type AppOrder string

// App orders.
const (
	// OrderCreated is newest created first, the default.
	OrderCreated AppOrder = "created"
	// OrderPublished is newest published first, then the apps never
	// published, newest created first.
	OrderPublished AppOrder = "published"
)

// ListAppsParams narrows and orders the apps ListApps returns. The zero
// value lists every app of the account the token acts in, newest first.
type ListAppsParams struct {
	// Order is OrderCreated, the default, or OrderPublished.
	Order AppOrder
	// IDs, when set, lists only these apps, by ID, at most 50, in one
	// page.
	IDs []string
	// Limit is the page size: 50 when zero, at most 200.
	Limit int
	// Cursor asks ListAppsPage for the page after the one that returned
	// it. ListApps ignores it.
	Cursor string
}

func (p *ListAppsParams) query(cursor string) url.Values {
	q := url.Values{}
	if p != nil {
		if p.Order != "" {
			q.Set("sort", string(p.Order))
		}
		for _, id := range p.IDs {
			q.Add("id", id)
		}
		if p.Limit > 0 {
			q.Set("limit", strconv.Itoa(p.Limit))
		}
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	return q
}

// ListApps returns an iterator over the apps params keeps, fetching pages
// as the loop asks for more. params may be nil.
func (c *Client) ListApps(ctx context.Context, params *ListAppsParams) iter.Seq2[*App, error] {
	return latere.Items(ctx, func(ctx context.Context, cursor string) (*latere.Page[*App], error) {
		return c.listPage(ctx, params, cursor)
	})
}

// ListAppsPage returns one page of the apps params keeps, starting at
// params.Cursor, and the cursor of the next page in NextCursor.
func (c *Client) ListAppsPage(ctx context.Context, params *ListAppsParams) (*latere.Page[*App], error) {
	cursor := ""
	if params != nil {
		cursor = params.Cursor
	}
	return c.listPage(ctx, params, cursor)
}

func (c *Client) listPage(ctx context.Context, params *ListAppsParams, cursor string) (*latere.Page[*App], error) {
	var page latere.Page[*App]
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodGet, Path: basePath, Query: params.query(cursor)}, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// UpdateAppParams are the changes to an app. Fields left at their zero
// value do not change; at least one must be set.
type UpdateAppParams struct {
	// Name is the new display name.
	Name string `json:"name,omitempty"`
	// Slug is the new slug, and so the new address. The app's repository
	// moves with it; its CloneURL does not change.
	Slug string `json:"slug,omitempty"`
	// Visibility is who may open the app from now on.
	Visibility Visibility `json:"visibility,omitempty"`
	// State is AppPaused to unpublish an active app, or AppActive to
	// publish a paused one again.
	State AppState `json:"state,omitempty"`
	// Settings are merged into the app's settings, field by field.
	Settings *SettingsUpdate `json:"settings,omitempty"`
}

// SettingsUpdate are the settings to change; nil fields keep their value.
type SettingsUpdate struct {
	// SleepAfterSeconds is how long the app runs without a request before
	// it sleeps.
	SleepAfterSeconds *int `json:"sleep_after_seconds,omitempty"`
	// AlwaysOn, when true, keeps the app from sleeping; the platform may
	// refuse it for the account.
	AlwaysOn *bool `json:"always_on,omitempty"`
	// Previews says who may open the app's previews.
	Previews *PreviewSettings `json:"previews,omitempty"`
}

// UpdateApp changes the app slug names and returns it as changed. A new
// slug another app holds returns an error matching latere.ErrConflict.
func (c *Client) UpdateApp(ctx context.Context, slug string, params *UpdateAppParams) (*App, error) {
	if slug == "" {
		return nil, errNoSlug
	}
	if params == nil || *params == (UpdateAppParams{}) {
		return nil, errors.New("apps: UpdateApp: no change was given")
	}
	var out App
	// A change sets values, so sending it again has the effect of sending it
	// once, and it is retried.
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodPatch, Path: appPath(slug), Body: params, Retry: latere.RetryAlways}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteApp starts deleting the app slug names, its address, deploys and
// repository included. The app reads as AppDeleting until it is gone; a
// create of the same slug within seven days restores it, without the
// files of its deploys.
func (c *Client) DeleteApp(ctx context.Context, slug string) error {
	if slug == "" {
		return errNoSlug
	}
	return c.cfg.Do(ctx, &latere.Request{Method: http.MethodDelete, Path: appPath(slug)}, nil)
}

// keep returns a copy of b, which a decoder may reuse after it returns.
func keep(b []byte) json.RawMessage {
	return append(json.RawMessage(nil), b...)
}
