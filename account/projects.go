// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package account

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"latere.ai/x/sdk-go/latere"
)

// The bounds of a project, as the API holds them.
const (
	// MaxProjectName is the most characters a project's name holds, on
	// one line, once its surrounding spaces are trimmed.
	MaxProjectName = 100
	// MaxProjectInstructions is the most bytes a project's instructions
	// hold.
	MaxProjectInstructions = 8192
	// MaxProjectApps is the most apps a project holds.
	MaxProjectApps = 5
	// MaxProjects is the most projects a context holds.
	MaxProjects = 100
	// MaxProjectsPage is the most projects one page of ListProjects
	// holds; the API answers 50 when a list names no limit.
	MaxProjectsPage = 100
)

// Errors of the project calls to test with errors.Is. The error that
// matches one wraps the *latere.Error the API answered with 409, which
// errors.As still finds with its details, and which latere.ErrConflict
// matches too.
var (
	// ErrProjectLimit matches a create or an added app past a bound: the
	// *latere.Error's Details hold "limit", "projects" for MaxProjects in
	// a context or "apps" for MaxProjectApps in a project, and "max", the
	// bound.
	ErrProjectLimit = errors.New("account: a project's bound is reached")
	// ErrAppNotInContext matches an app a project may not hold: one the
	// caller cannot open, or one of another context than the project's.
	// The *latere.Error's Details hold "app_id" and "reason", one of
	// "unknown", "other_context", "unreadable" or "no_registry", the last
	// for an installation that keeps no record of apps.
	ErrAppNotInContext = errors.New("account: the app is not one of the project's context that the caller can open")
)

// projectCodes are the API's codes for the refusals of the project calls
// that have a sentinel.
var projectCodes = map[string]error{
	"project_limit":      ErrProjectLimit,
	"app_not_in_context": ErrAppNotInContext,
}

// A Project groups the conversations started in it and holds the apps
// they work on, in one context: the person's own or an organization's.
type Project struct {
	// ID identifies the project: "prj_" and 26 characters.
	ID string `json:"id"`
	// Owner is the context the project belongs to, which never changes.
	Owner ProjectOwner `json:"owner"`
	// Name is what the project is called; names need not be unique.
	Name string `json:"name"`
	// Instructions are what every conversation started in the project
	// reads; empty for none.
	Instructions string `json:"instructions"`
	// Apps are the apps the project holds, in the order they were added,
	// each with its address and name now. A conversation started in the
	// project begins with each one's source checked out.
	Apps []ProjectApp `json:"apps"`
	// Actions are what the caller may do with the project now, so a
	// program offers nothing the API would refuse.
	Actions []ProjectAction `json:"actions"`
	// CreatedBy is the person who created it, as "<issuer>|<subject>".
	CreatedBy string `json:"created_by"`
	// CreatedAt is when it was created.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is when it was last changed or a conversation last
	// started in it; ListProjects lists the latest first.
	UpdatedAt time.Time `json:"updated_at"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the project, with every
// field, including those this package does not model; nil for a Project
// the program built itself.
func (p *Project) RawJSON() json.RawMessage { return p.raw }

// UnmarshalJSON decodes the project and keeps the JSON for RawJSON.
func (p *Project) UnmarshalJSON(b []byte) error {
	type plain Project
	if err := json.Unmarshal(b, (*plain)(p)); err != nil {
		return err
	}
	p.raw = keep(b)
	return nil
}

// ProjectOwner is the context a project belongs to.
type ProjectOwner struct {
	// Kind is whether the context is a person's own or an organization's.
	Kind OwnerKind `json:"kind"`
	// ID is the person's subject, or the organization's id.
	ID string `json:"id"`
}

// OwnerKind says whose a context is.
type OwnerKind string

// The kinds of a ProjectOwner.
const (
	// OwnerPersonal is a person's own context.
	OwnerPersonal OwnerKind = "personal"
	// OwnerOrganization is an organization's, whose members all list its
	// projects.
	OwnerOrganization OwnerKind = "organization"
)

// A ProjectApp is an app a project holds, as it is now.
type ProjectApp struct {
	// ID identifies the app, as the apps package answers it.
	ID string `json:"id"`
	// Slug is the app's slug.
	Slug string `json:"slug"`
	// Name is the app's name.
	Name string `json:"name"`
	// URL is the app's address.
	URL string `json:"url"`
}

// ProjectAction is something the caller may do with a project. In an
// organization, the project's creator and the organization's owners and
// admins change and delete it; every other member reads it and starts
// conversations in it.
type ProjectAction string

// The actions of a Project.
const (
	// ProjectRename is changing the project's name.
	ProjectRename ProjectAction = "rename"
	// ProjectEditInstructions is changing its instructions.
	ProjectEditInstructions ProjectAction = "edit_instructions"
	// ProjectAddApp is adding an app to it, and removing one.
	ProjectAddApp ProjectAction = "add_app"
	// ProjectDelete is deleting it.
	ProjectDelete ProjectAction = "delete"
)

// errNoProject is returned before any request when a project id is empty.
var errNoProject = errors.New("account: the project id is empty")

// projectPath is the path of one of the context's projects, followed by
// the segments in more.
func projectPath(id string, more ...string) string {
	p := "/v1/account/projects/" + url.PathEscape(id)
	for _, m := range more {
		p += "/" + url.PathEscape(m)
	}
	return p
}

// ListProjectsParams narrows and pages a list of projects. A nil
// *ListProjectsParams lists every project of the context.
type ListProjectsParams struct {
	// App keeps the projects that hold this app, by its id: the ones a
	// program offers when a person starts a conversation from the app's
	// page.
	App string
	// Limit is how many projects a page holds at most, up to
	// MaxProjectsPage; the API holds 50 when it is zero.
	Limit int
	// Cursor asks ListProjectsPage for the page after the one that
	// returned it. ListProjects ignores it.
	Cursor string
}

// query encodes the parameters for a list request.
func (p *ListProjectsParams) query(cursor string) url.Values {
	q := url.Values{}
	if p != nil {
		if p.App != "" {
			q.Set("app", p.App)
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

// ListProjects returns an iterator over the projects of the context the
// token names, the one worked in most recently first, fetching pages as
// the loop needs them. Every member of an organization lists the
// organization's projects. A hosted agent's token is refused with 403.
// An error ends the iteration after it is yielded.
func (c *Client) ListProjects(ctx context.Context, params *ListProjectsParams) iter.Seq2[*Project, error] {
	return latere.Items(ctx, func(ctx context.Context, cursor string) (*latere.Page[*Project], error) {
		return c.listProjectsPage(ctx, params, cursor)
	})
}

// ListProjectsPage returns one page of the projects ListProjects lists,
// starting at params.Cursor, and the cursor of the next page in
// NextCursor.
func (c *Client) ListProjectsPage(ctx context.Context, params *ListProjectsParams) (*latere.Page[*Project], error) {
	cursor := ""
	if params != nil {
		cursor = params.Cursor
	}
	return c.listProjectsPage(ctx, params, cursor)
}

func (c *Client) listProjectsPage(ctx context.Context, params *ListProjectsParams, cursor string) (*latere.Page[*Project], error) {
	var page latere.Page[*Project]
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodGet, Path: "/v1/account/projects", Query: params.query(cursor)}, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// CreateProjectParams are a new project's values.
type CreateProjectParams struct {
	// Name is what the project is called: one line of 1 to
	// MaxProjectName characters, its surrounding spaces trimmed. Required.
	Name string `json:"name"`
	// Instructions are what every conversation started in the project
	// reads, at most MaxProjectInstructions bytes; empty for none.
	Instructions string `json:"instructions,omitempty"`
	// Apps are up to MaxProjectApps app ids, each an app of the same
	// context that the caller can open, held in this order.
	Apps []string `json:"apps,omitempty"`
}

// CreateProject makes a project in the context the token names, the
// person's own or an organization's, where every member makes one, and
// returns it. A name or instructions outside their bounds are refused
// with 400 and the code "invalid_field", whose details name the field. A
// context past MaxProjects, or more than MaxProjectApps apps, returns an
// error matching ErrProjectLimit, and an app the project may not hold one
// matching ErrAppNotInContext.
//
// CreateProject is sent once and never retried: the API takes no
// idempotency key on it, so a repeat after a lost answer would make a
// second project. After a failure on the network, ListProjects tells
// whether the first one was made.
func (c *Client) CreateProject(ctx context.Context, params *CreateProjectParams) (*Project, error) {
	if params == nil || strings.TrimSpace(params.Name) == "" {
		return nil, errors.New("account: CreateProject: the name is empty")
	}
	var out Project
	req := &latere.Request{Method: http.MethodPost, Path: "/v1/account/projects", Body: params, Retry: latere.RetryNever}
	if err := c.cfg.Do(ctx, req, &out); err != nil {
		return nil, withSentinel(err, projectCodes)
	}
	return &out, nil
}

// GetProject returns one project of the context the token names, with its
// apps' addresses and names now and what the caller may do with it. A
// project of another context, or none of that id, returns an error
// matching latere.ErrNotFound.
func (c *Client) GetProject(ctx context.Context, id string) (*Project, error) {
	if id == "" {
		return nil, errNoProject
	}
	var out Project
	if err := c.get(ctx, projectPath(id), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateProjectParams is a change of a project. A nil field keeps what
// the project has.
type UpdateProjectParams struct {
	// Name is the project's new name, by CreateProjectParams.Name's rule.
	Name *string `json:"name,omitempty"`
	// Instructions are its new instructions, by
	// CreateProjectParams.Instructions's rule; an empty string removes
	// them. A conversation already started keeps the instructions it
	// started with; the next one reads the new ones.
	Instructions *string `json:"instructions,omitempty"`
}

// UpdateProject changes a project's name, its instructions or both, and
// returns the project as it is now. A change neither names returns the
// project unchanged. A member who did not create an organization's
// project, and is none of its owners and admins, is refused with 403 and
// the code "role_insufficient"; Project.Actions says beforehand. A project
// of another context, or none of that id, returns an error matching
// latere.ErrNotFound.
func (c *Client) UpdateProject(ctx context.Context, id string, params *UpdateProjectParams) (*Project, error) {
	if id == "" {
		return nil, errNoProject
	}
	if params == nil {
		params = &UpdateProjectParams{}
	}
	var out Project
	// A change sets values, so sending it again has the effect of sending
	// it once, and it is retried.
	req := &latere.Request{Method: http.MethodPatch, Path: projectPath(id), Body: params, Retry: latere.RetryAlways}
	if err := c.cfg.Do(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteProject deletes a project. Its apps and its conversations stay as
// they are; the conversations then list without a project, and their
// agents no longer reach the project's apps. Who may delete it is who may
// change it, as UpdateProject says.
//
// An id the context holds no project by returns an error matching
// latere.ErrNotFound, with the code "project_not_found": a project never
// made, one deleted before, and one of another context, such as the
// person's organization after they switched to their own account, all
// answer it. A deletion is retried after a failure on the network, so a
// retry can meet that 404 although the first attempt deleted the project.
func (c *Client) DeleteProject(ctx context.Context, id string) error {
	if id == "" {
		return errNoProject
	}
	return c.cfg.Do(ctx, &latere.Request{Method: http.MethodDelete, Path: projectPath(id)}, nil)
}

// AddProjectApp adds an app, by its id, to a project after the apps added
// before, and returns the project. Every conversation started in the
// project afterwards begins with the app's source checked out at what the
// app serves now. Adding an app the project holds already changes
// nothing, so the call is safe to repeat.
//
// The app must be one of the project's context that the caller can open,
// or the call returns an error matching ErrAppNotInContext; a project
// that holds MaxProjectApps returns one matching ErrProjectLimit. An app
// may be in several projects. A project of another context, or none of
// that id, returns an error matching latere.ErrNotFound.
func (c *Client) AddProjectApp(ctx context.Context, projectID, appID string) (*Project, error) {
	if projectID == "" {
		return nil, errNoProject
	}
	if appID == "" {
		return nil, errors.New("account: AddProjectApp: the app id is empty")
	}
	var out Project
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodPut, Path: projectPath(projectID, "apps", appID)}, &out); err != nil {
		return nil, withSentinel(err, projectCodes)
	}
	return &out, nil
}

// RemoveProjectApp removes an app from a project; the app itself stays as
// it is, and a conversation started in the project no longer works on it.
// Removing an app the project does not hold succeeds. A project of
// another context, or none of that id, returns an error matching
// latere.ErrNotFound.
func (c *Client) RemoveProjectApp(ctx context.Context, projectID, appID string) error {
	if projectID == "" {
		return errNoProject
	}
	if appID == "" {
		return errors.New("account: RemoveProjectApp: the app id is empty")
	}
	return c.cfg.Do(ctx, &latere.Request{Method: http.MethodDelete, Path: projectPath(projectID, "apps", appID)}, nil)
}
