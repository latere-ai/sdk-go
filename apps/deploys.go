// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package apps

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"latere.ai/x/sdk-go/latere"
)

// errNoDeploy is returned before any request when a deploy's ID is empty.
var errNoDeploy = errors.New("apps: the deploy's id is empty")

// ComponentKind is what a component of a deploy runs as.
type ComponentKind string

// Component kinds.
const (
	// KindStatic serves files.
	KindStatic ComponentKind = "static"
	// KindService runs a server.
	KindService ComponentKind = "service"
	// KindWorker runs a process that serves no requests.
	KindWorker ComponentKind = "worker"
	// KindCron runs a process on a schedule.
	KindCron ComponentKind = "cron"
)

// DeployStatus is where a deploy is in its life, from waiting to live.
type DeployStatus string

// Deploy statuses, in the order a deploy moves through them.
const (
	DeployWaiting   DeployStatus = "waiting"
	DeployQueued    DeployStatus = "queued"
	DeployBuilding  DeployStatus = "building"
	DeployBuilt     DeployStatus = "built"
	DeployScanning  DeployStatus = "scanning"
	DeployDeploying DeployStatus = "deploying"
	// DeployReady is a preview that serves at its own address.
	DeployReady DeployStatus = "ready"
	// DeployLive is the deploy of a release, which the app's address
	// serves.
	DeployLive DeployStatus = "live"
	// DeploySuperseded is the deploy of a release a newer one replaced.
	DeploySuperseded DeployStatus = "superseded"
	DeployFailed     DeployStatus = "failed"
	DeployCanceled   DeployStatus = "canceled"
)

// ComponentStatus is where a component's build is.
type ComponentStatus string

// Component statuses.
const (
	ComponentQueued   ComponentStatus = "queued"
	ComponentBuilding ComponentStatus = "building"
	ComponentBuilt    ComponentStatus = "built"
	ComponentFailed   ComponentStatus = "failed"
	ComponentCanceled ComponentStatus = "canceled"
)

// Deploy is one build of an app: a preview of a pushed branch, or the
// deploy of a release.
type Deploy struct {
	// ID identifies the deploy. Calls that take it also take a prefix of
	// at least 8 hexadecimal digits that names one deploy of the app.
	ID string `json:"id"`
	// Seq is the deploy's number within its app.
	Seq int64 `json:"seq"`
	// Kind is the kind of the component that serves "/".
	Kind ComponentKind `json:"kind"`
	// Status is where the deploy is.
	Status DeployStatus `json:"status"`
	// Preview is true for a preview and false for the deploy of a
	// release.
	Preview bool `json:"preview"`
	// CommitSHA is the commit that was built; empty when none is recorded.
	CommitSHA string `json:"commit_sha"`
	// Ref is the branch or tag that was pushed, as a full reference name
	// such as "refs/heads/main".
	Ref string `json:"ref"`
	// Client is what created the deploy: "cli", "harness", "console",
	// "api" or "push".
	Client string `json:"client"`
	// CreatedBy is the subject that created the deploy.
	CreatedBy string `json:"created_by"`
	// URL is the app's address.
	URL string `json:"url"`
	// PreviewURL is the deploy's own address.
	PreviewURL string `json:"preview_url"`
	// LogsURL is the path the deploy's build log is served at.
	LogsURL string `json:"logs_url"`
	// FromDeploy is, for the deploy of a release, the ID of the preview
	// whose build it serves.
	FromDeploy string `json:"from_deploy"`
	// Components are what the deploy builds.
	Components []Component `json:"components"`
	// Error is why a failed or refused deploy ended; nil otherwise.
	Error *DeployError `json:"error"`
	// CreatedAt is when the deploy was created.
	CreatedAt time.Time `json:"created_at"`
	// StartedAt is when it started building; zero until then.
	StartedAt time.Time `json:"started_at,omitzero"`
	// BuiltAt is when it was built; zero until then.
	BuiltAt time.Time `json:"built_at,omitzero"`
	// ReadyAt is when it became ready; zero until then.
	ReadyAt time.Time `json:"ready_at,omitzero"`
	// LiveAt is when it went live; zero until then.
	LiveAt time.Time `json:"live_at,omitzero"`
	// FinishedAt is when it failed or was canceled; zero otherwise.
	FinishedAt time.Time `json:"finished_at,omitzero"`
	// Thumbnail is the deploy's screenshot; nil until it is taken.
	Thumbnail *Thumbnail `json:"thumbnail"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the deploy, with every
// field; nil for a Deploy the program built itself.
func (d *Deploy) RawJSON() json.RawMessage { return d.raw }

// UnmarshalJSON decodes the deploy and keeps the JSON for RawJSON.
func (d *Deploy) UnmarshalJSON(b []byte) error {
	type plain Deploy
	if err := json.Unmarshal(b, (*plain)(d)); err != nil {
		return err
	}
	d.raw = keep(b)
	return nil
}

// DeploySummary is a deploy as an app's CurrentDeploy and LatestPreview
// show it.
type DeploySummary struct {
	// ID identifies the deploy.
	ID string `json:"id"`
	// Seq is the deploy's number within its app.
	Seq int64 `json:"seq"`
	// Kind is the kind of the component that serves "/".
	Kind ComponentKind `json:"kind"`
	// Status is DeployLive or DeploySuperseded for a release, DeployReady
	// for a preview.
	Status DeployStatus `json:"status"`
	// ReadyAt is when the deploy became ready; zero until then.
	ReadyAt time.Time `json:"ready_at,omitzero"`
	// LiveAt is when it went live; zero until then.
	LiveAt time.Time `json:"live_at,omitzero"`
	// CommitSHA is the commit the deploy serves; empty when it has none.
	CommitSHA string `json:"commit_sha"`
	// Thumbnail is the deploy's picture; nil until one is captured.
	Thumbnail *Thumbnail `json:"thumbnail"`
}

// Component is one part of a deploy, built and served on its own.
type Component struct {
	// Name names the component.
	Name string `json:"name"`
	// Kind is what the component runs as.
	Kind ComponentKind `json:"kind"`
	// Route is the path prefix the component serves; empty when it serves
	// none.
	Route string `json:"route"`
	// Status is where the component's build is.
	Status ComponentStatus `json:"status"`
}

// DeployError is why a deploy failed or was refused.
type DeployError struct {
	// Code names the failure, such as "build_failed".
	Code string `json:"code"`
	// Message is the detail recorded with it.
	Message string `json:"message"`
}

// ListDeploys returns every deploy of the app slug names, newest first.
func (c *Client) ListDeploys(ctx context.Context, slug string) ([]*Deploy, error) {
	if slug == "" {
		return nil, errNoSlug
	}
	var out struct {
		Deploys []*Deploy `json:"deploys"`
	}
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodGet, Path: appPath(slug, "deploys")}, &out); err != nil {
		return nil, err
	}
	return out.Deploys, nil
}

// GetDeploy returns one deploy of the app slug names. id is the deploy's
// ID, or a prefix of at least 8 hexadecimal digits that names one deploy;
// a shorter prefix, or one two deploys share, returns an error.
func (c *Client) GetDeploy(ctx context.Context, slug, id string) (*Deploy, error) {
	if slug == "" {
		return nil, errNoSlug
	}
	if id == "" {
		return nil, errNoDeploy
	}
	var out Deploy
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodGet, Path: appPath(slug, "deploys", id)}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// LogLine is one line of a build log.
type LogLine struct {
	// Seq is the line's number within its component's log.
	Seq int64 `json:"seq"`
	// Time is when the line was written.
	Time time.Time `json:"ts"`
	// Step is the step of the build that wrote the line: "fetch",
	// "build", "publish" or "platform".
	Step string `json:"src"`
	// Component is the component whose log the line belongs to.
	Component string `json:"component"`
	// Text is the line, without its line break.
	Text string `json:"line"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the line; nil for a
// LogLine the program built itself.
func (l *LogLine) RawJSON() json.RawMessage { return l.raw }

// UnmarshalJSON decodes the line and keeps the JSON for RawJSON.
func (l *LogLine) UnmarshalJSON(b []byte) error {
	type plain LogLine
	if err := json.Unmarshal(b, (*plain)(l)); err != nil {
		return err
	}
	l.raw = keep(b)
	return nil
}

// BuildStatus is how a build ended, as a followed log's last event says.
type BuildStatus string

// Build statuses.
const (
	BuildBuilt    BuildStatus = "built"
	BuildFailed   BuildStatus = "failed"
	BuildCanceled BuildStatus = "canceled"
	// BuildBuilding is sent only when the log was read without following
	// a deploy that is still building.
	BuildBuilding BuildStatus = "building"
)

// LogEnd is the last event of a followed build log.
type LogEnd struct {
	// Status is how the build ended.
	Status BuildStatus `json:"status"`
	// Error is why the build failed; nil otherwise.
	Error *BuildError `json:"error,omitempty"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the end; nil for a LogEnd
// the program built itself.
func (e *LogEnd) RawJSON() json.RawMessage { return e.raw }

// UnmarshalJSON decodes the end and keeps the JSON for RawJSON.
func (e *LogEnd) UnmarshalJSON(b []byte) error {
	type plain LogEnd
	if err := json.Unmarshal(b, (*plain)(e)); err != nil {
		return err
	}
	e.raw = keep(b)
	return nil
}

// BuildError is why a build failed.
type BuildError struct {
	// Code names the failure.
	Code string `json:"code"`
	// Message is one sentence about it, for a person.
	Message string `json:"message"`
	// Hint says what to do about it.
	Hint string `json:"hint"`
	// Component is the component that failed; empty when none is to
	// blame.
	Component string `json:"component"`
}

// DeployLogParams chooses which part of a build log to read. The zero
// value reads every component's log from the start.
type DeployLogParams struct {
	// Component reads one component's log instead of every component's.
	Component string
	// Since reads only the lines after the one with this Seq.
	Since int64
}

func (p *DeployLogParams) query(follow bool) url.Values {
	q := url.Values{}
	if p != nil {
		if p.Component != "" {
			q.Set("component", p.Component)
		}
		if p.Since > 0 {
			q.Set("since", strconv.FormatInt(p.Since, 10))
		}
	}
	if follow {
		q.Set("follow", "1")
	}
	return q
}

// maxLogLine bounds one line of a stored build log.
const maxLogLine = 1 << 20

// ReadDeployLog returns an iterator over the build log of one deploy as it
// is stored now, oldest line first, the components merged by time. id is
// as for GetDeploy. To keep reading while the deploy builds, use
// FollowDeployLog. Stopping the loop closes the connection.
func (c *Client) ReadDeployLog(ctx context.Context, slug, id string, params *DeployLogParams) iter.Seq2[*LogLine, error] {
	return func(yield func(*LogLine, error) bool) {
		if slug == "" {
			yield(nil, errNoSlug)
			return
		}
		if id == "" {
			yield(nil, errNoDeploy)
			return
		}
		req := &latere.Request{
			Method: http.MethodGet, Path: appPath(slug, "deploys", id, "logs"), Query: params.query(false),
			Header: http.Header{"Accept": {"application/x-ndjson"}},
		}
		resp, err := c.cfg.Open(ctx, req)
		if err != nil {
			yield(nil, err)
			return
		}
		// The body is closed unread when the loop stops early: what is left
		// of the log is not needed.
		defer func() { _ = resp.Body.Close() }()
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64<<10), maxLogLine)
		for scanner.Scan() {
			line := bytes.TrimSpace(scanner.Bytes())
			if len(line) == 0 {
				continue
			}
			var l LogLine
			if err := json.Unmarshal(line, &l); err != nil {
				yield(nil, fmt.Errorf("apps: decoding a line of the build log: %w", err))
				return
			}
			if !yield(&l, nil) {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			yield(nil, fmt.Errorf("apps: reading the build log: %w", err))
		}
	}
}

// LogEvent is one event of a followed build log: a Line, or the End that
// closes the log.
type LogEvent struct {
	// Line is a line of the log; nil on the end.
	Line *LogLine
	// End is the log's last event, which says how the build ended; nil on
	// a line.
	End *LogEnd
}

// FollowDeployLog returns an iterator over the build log of one deploy,
// the lines already written and then each line as it is written, until
// the build ends: the last event's End says how. id is as for GetDeploy.
// When the connection drops, it picks up after the last line it delivered.
// Stopping the loop closes the connection.
func (c *Client) FollowDeployLog(ctx context.Context, slug, id string, params *DeployLogParams) iter.Seq2[LogEvent, error] {
	return func(yield func(LogEvent, error) bool) {
		if slug == "" {
			yield(LogEvent{}, errNoSlug)
			return
		}
		if id == "" {
			yield(LogEvent{}, errNoDeploy)
			return
		}
		req := &latere.Request{Method: http.MethodGet, Path: appPath(slug, "deploys", id, "logs"), Query: params.query(true)}
		for frame, err := range c.cfg.Stream(ctx, req, isLogEnd) {
			if err != nil {
				yield(LogEvent{}, err)
				return
			}
			ev, err := decodeLogFrame(frame)
			if !yield(ev, err) || err != nil {
				return
			}
		}
	}
}

// isLogEnd reports the frame that closes a followed build log.
func isLogEnd(f latere.Frame) bool { return f.Event == "end" }

// decodeLogFrame reads one frame of a followed build log.
func decodeLogFrame(f latere.Frame) (LogEvent, error) {
	if isLogEnd(f) {
		var e LogEnd
		if err := json.Unmarshal(f.Data, &e); err != nil {
			return LogEvent{}, fmt.Errorf("apps: decoding the end of the build log: %w", err)
		}
		return LogEvent{End: &e}, nil
	}
	var l LogLine
	if err := json.Unmarshal(f.Data, &l); err != nil {
		return LogEvent{}, fmt.Errorf("apps: decoding a line of the build log: %w", err)
	}
	return LogEvent{Line: &l}, nil
}
