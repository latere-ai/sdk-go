// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"net/url"
	"strconv"

	"latere.ai/x/sdk-go/latere"
)

// basePath is where the agents capability is served.
const basePath = "/v1/agents"

// Client reaches the agents capability of the Latere API.
type Client struct {
	// Sessions creates, lists and follows sessions.
	Sessions *SessionsService
}

// New returns a Client that sends its requests as cfg says.
func New(cfg *latere.Config) *Client {
	return &Client{Sessions: &SessionsService{cfg: cfg}}
}

// SessionsService is the sessions part of the agents API.
type SessionsService struct {
	cfg *latere.Config
}

// errNoID is returned before any request when a session id is empty.
var errNoID = errors.New("agents: the session id is empty")

// sessionPath is the path of a session, or of one of its sub-resources.
func sessionPath(id string, rest string) string {
	return basePath + "/sessions/" + url.PathEscape(id) + rest
}

// ArchiveFilter says whether a list includes archived sessions.
type ArchiveFilter string

// The archive filters of ListSessionsParams.
const (
	// ArchivedExcluded leaves archived sessions out; the default.
	ArchivedExcluded ArchiveFilter = "false"
	// ArchivedOnly lists archived sessions alone.
	ArchivedOnly ArchiveFilter = "true"
	// ArchivedIncluded lists archived sessions with the others.
	ArchivedIncluded ArchiveFilter = "any"
)

// ListSessionsParams narrows a list of sessions. The zero value lists the
// caller's sessions that are not archived, newest first.
type ListSessionsParams struct {
	// Agent keeps the sessions of one agent, by its id or its name.
	Agent string
	// Status keeps the sessions in one state.
	Status Status
	// Runner keeps the sessions that run "hosted" or "external".
	Runner string
	// Archived says whether archived sessions are listed.
	Archived ArchiveFilter
	// Root keeps the sessions of the fork tree whose top is this session.
	Root string
	// Parent keeps the sessions forked from this session.
	Parent string
	// Trigger keeps the sessions this trigger started.
	Trigger string
	// MetadataKey and MetadataValue keep the sessions whose metadata holds
	// the key with exactly the value. The API takes one such entry.
	MetadataKey, MetadataValue string
	// GroupByTree lists one session per fork tree, the tree's newest.
	GroupByTree bool
	// Limit is how many sessions a page holds at most, up to 500; the API
	// chooses when it is zero.
	Limit int
	// Cursor asks ListPage for the page after the one that returned it.
	// List ignores it.
	Cursor string
}

// query encodes the parameters for a list request.
func (p *ListSessionsParams) query(cursor string) url.Values {
	q := url.Values{}
	if p == nil {
		p = &ListSessionsParams{}
	}
	set := func(k, v string) {
		if v != "" {
			q.Set(k, v)
		}
	}
	set("agent", p.Agent)
	set("status", string(p.Status))
	set("runner", p.Runner)
	set("archived", string(p.Archived))
	set("root", p.Root)
	set("parent", p.Parent)
	set("trigger", p.Trigger)
	if p.MetadataKey != "" {
		q.Set("metadata."+p.MetadataKey, p.MetadataValue)
	}
	if p.GroupByTree {
		q.Set("group", "tree")
	}
	if p.Limit > 0 {
		q.Set("limit", strconv.Itoa(p.Limit))
	}
	set("cursor", cursor)
	return q
}

// List returns an iterator over the sessions params keeps, newest first,
// fetching pages as the loop needs them. An error ends the iteration after
// it is yielded.
func (s *SessionsService) List(ctx context.Context, params *ListSessionsParams) iter.Seq2[*Session, error] {
	return latere.Items(ctx, func(ctx context.Context, cursor string) (*latere.Page[*Session], error) {
		return s.listPage(ctx, params, cursor)
	})
}

// ListPage returns one page of the sessions params keeps, starting at
// params.Cursor, and the cursor of the next page in NextCursor.
func (s *SessionsService) ListPage(ctx context.Context, params *ListSessionsParams) (*latere.Page[*Session], error) {
	cursor := ""
	if params != nil {
		cursor = params.Cursor
	}
	return s.listPage(ctx, params, cursor)
}

func (s *SessionsService) listPage(ctx context.Context, params *ListSessionsParams, cursor string) (*latere.Page[*Session], error) {
	var page latere.Page[*Session]
	err := s.cfg.Do(ctx, &latere.Request{Method: http.MethodGet, Path: basePath + "/sessions", Query: params.query(cursor)}, &page)
	if err != nil {
		return nil, err
	}
	return &page, nil
}

// Get returns the session id names. It returns an error matching
// latere.ErrNotFound when there is no such session the caller may read.
func (s *SessionsService) Get(ctx context.Context, id string) (*Session, error) {
	if id == "" {
		return nil, errNoID
	}
	var out Session
	if err := s.cfg.Do(ctx, &latere.Request{Method: http.MethodGet, Path: sessionPath(id, "")}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateSessionParams describes a new session.
type CreateSessionParams struct {
	// Agent is the agent to talk to, by its id or its name. Required.
	Agent string
	// Message is the first message, sent with the creation; empty to send
	// one later with SendMessage.
	Message string
	// Title names the session for a person.
	Title string
	// Attended says a person follows the session, so it may stop to ask
	// them things.
	Attended bool
	// Metadata holds labels of the caller's own, which every read returns
	// and ListSessionsParams can filter on.
	Metadata map[string]string
	// Machine names where the session's commands run, such as one of the
	// person's computers: Kind MachineDevice, Environment the computer's id
	// and Workdir a folder it shares. Nil leaves it to the agent.
	Machine *Machine
	// IdempotencyKey makes a repeated creation return the first session
	// instead of a second; one is generated when it is empty.
	IdempotencyKey string
}

// Create creates a session and returns it. A creation that fails on the
// network is retried with the same idempotency key, so it creates at most
// one session.
func (s *SessionsService) Create(ctx context.Context, params *CreateSessionParams) (*Session, error) {
	if params == nil || params.Agent == "" {
		return nil, errors.New("agents: CreateSessionParams.Agent is required")
	}
	body := map[string]any{"agent": params.Agent}
	if params.Message != "" {
		body["message"] = params.Message
	}
	if params.Title != "" {
		body["title"] = params.Title
	}
	if params.Attended {
		body["attended"] = true
	}
	if len(params.Metadata) > 0 {
		body["metadata"] = params.Metadata
	}
	if params.Machine != nil {
		body["machine"] = params.Machine
	}
	var out Session
	req := &latere.Request{Method: http.MethodPost, Path: basePath + "/sessions", Body: body, IdempotencyKey: keyOr(params.IdempotencyKey)}
	if err := s.cfg.Do(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Archive archives a session, which leaves it out of lists by default, and
// returns it. Archiving an archived session changes nothing.
func (s *SessionsService) Archive(ctx context.Context, id string) (*Session, error) {
	if id == "" {
		return nil, errNoID
	}
	var out Session
	req := &latere.Request{Method: http.MethodPost, Path: sessionPath(id, "/archive"), IdempotencyKey: latere.NewIdempotencyKey()}
	if err := s.cfg.Do(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ForkSessionParams describes a fork of a session.
type ForkSessionParams struct {
	// BeforeSeq forks at the turn boundary before this sequence; zero forks
	// at the end of the log.
	BeforeSeq int64
	// Title names the new session.
	Title string
	// Message is the new session's first message.
	Message []ContentBlock
	// Attended says a person follows the new session.
	Attended bool
	// IdempotencyKey makes a repeated fork return the first fork; one is
	// generated when it is empty.
	IdempotencyKey string
}

// Fork creates a session that holds the log of session id up to a turn
// boundary, and returns the new session.
func (s *SessionsService) Fork(ctx context.Context, id string, params *ForkSessionParams) (*Session, error) {
	if id == "" {
		return nil, errNoID
	}
	if params == nil {
		params = &ForkSessionParams{}
	}
	body := map[string]any{}
	if params.BeforeSeq > 0 {
		body["before_seq"] = params.BeforeSeq
	}
	if params.Title != "" {
		body["title"] = params.Title
	}
	if len(params.Message) > 0 {
		body["message"] = map[string]any{"content": params.Message}
	}
	if params.Attended {
		body["attended"] = true
	}
	var out Session
	req := &latere.Request{Method: http.MethodPost, Path: sessionPath(id, "/fork"), Body: body, IdempotencyKey: keyOr(params.IdempotencyKey)}
	if err := s.cfg.Do(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListEventsParams says where a read of a session's log starts.
type ListEventsParams struct {
	// FromSeq is the sequence to start at; zero starts at the first event.
	FromSeq int64
	// Limit is how many events a page holds at most; the API chooses when
	// it is zero.
	Limit int
	// Cursor asks EventsPage for the page after the one that returned it.
	// Events ignores it.
	Cursor string
}

// Events returns an iterator over a session's log from params.FromSeq,
// fetching pages as the loop needs them. It ends at the newest event; use
// Stream to wait for more.
func (s *SessionsService) Events(ctx context.Context, id string, params *ListEventsParams) iter.Seq2[*Event, error] {
	return latere.Items(ctx, func(ctx context.Context, cursor string) (*latere.Page[*Event], error) {
		return s.eventsPage(ctx, id, params, cursor)
	})
}

// EventsPage returns one page of a session's log and the cursor of the
// next page in NextCursor.
func (s *SessionsService) EventsPage(ctx context.Context, id string, params *ListEventsParams) (*latere.Page[*Event], error) {
	cursor := ""
	if params != nil {
		cursor = params.Cursor
	}
	return s.eventsPage(ctx, id, params, cursor)
}

func (s *SessionsService) eventsPage(ctx context.Context, id string, params *ListEventsParams, cursor string) (*latere.Page[*Event], error) {
	if id == "" {
		return nil, errNoID
	}
	q := url.Values{}
	if params != nil {
		if params.FromSeq > 0 {
			q.Set("from_seq", strconv.FormatInt(params.FromSeq, 10))
		}
		if params.Limit > 0 {
			q.Set("limit", strconv.Itoa(params.Limit))
		}
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	var page latere.Page[*Event]
	if err := s.cfg.Do(ctx, &latere.Request{Method: http.MethodGet, Path: sessionPath(id, "/events"), Query: q}, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// MessageParams is a message to a session.
type MessageParams struct {
	// Content is the message. Required.
	Content []ContentBlock
	// IdempotencyKey makes a repeated send deliver the message once; one is
	// generated when it is empty.
	IdempotencyKey string
}

// SendMessage sends a message to an idle session, which starts its next
// turn, and returns the event the message became.
func (s *SessionsService) SendMessage(ctx context.Context, id string, params *MessageParams) (*Event, error) {
	if params == nil || len(params.Content) == 0 {
		return nil, errors.New("agents: MessageParams.Content is required")
	}
	return s.send(ctx, id, EventUserMessage, map[string]any{"content": params.Content}, params.IdempotencyKey)
}

// Interrupt stops a session's running turn and returns the event the
// interruption became. The session becomes idle with StopInterrupted.
func (s *SessionsService) Interrupt(ctx context.Context, id string) (*Event, error) {
	return s.send(ctx, id, EventUserInterrupt, map[string]any{}, "")
}

// AnswerParams answers the questions of one question call.
type AnswerParams struct {
	// ToolUseID is the question call's id, from its ToolUse. Required.
	ToolUseID string
	// Answers holds one entry per question, in the questions' order.
	Answers []AnswerEntry
	// IdempotencyKey makes a repeated answer count once; one is generated
	// when it is empty.
	IdempotencyKey string
}

// AnswerEntry answers one question. Selected alone is a choice, Text alone
// an answer in the person's own words, both a choice with a note, and
// neither leaves the question to the agent.
type AnswerEntry struct {
	// Selected holds the labels of the chosen options.
	Selected []string `json:"selected,omitempty"`
	// Text is the person's own words.
	Text string `json:"text,omitempty"`
}

// Answer answers a question the session asked, which resumes it, and
// returns the event the answer became.
func (s *SessionsService) Answer(ctx context.Context, id string, params *AnswerParams) (*Event, error) {
	if params == nil || params.ToolUseID == "" {
		return nil, errors.New("agents: AnswerParams.ToolUseID is required")
	}
	answers := params.Answers
	if answers == nil {
		answers = []AnswerEntry{}
	}
	return s.send(ctx, id, EventUserAnswer, map[string]any{"tool_use_id": params.ToolUseID, "answers": answers}, params.IdempotencyKey)
}

// Decision is the answer to a tool call or an approval.
type Decision string

// The decisions of ToolConfirmationParams.
const (
	// Allow lets the call run.
	Allow Decision = "allow"
	// Deny refuses it; the agent is told and goes on without it.
	Deny Decision = "deny"
)

// ToolConfirmationParams approves or denies a tool call or an approval.
type ToolConfirmationParams struct {
	// ToolUseID names the tool call waiting for approval. Set it or
	// ApprovalID, not both.
	ToolUseID string
	// ApprovalID names an EventApprovalRequested that asks for approval.
	ApprovalID string
	// Decision is Allow or Deny. Required.
	Decision Decision
	// Note tells the agent why, in the person's words.
	Note string
	// Remember asks the session to apply the decision to calls like this
	// one, in the pattern the approval offered.
	Remember string
	// IdempotencyKey makes a repeated confirmation count once; one is
	// generated when it is empty.
	IdempotencyKey string
}

// ConfirmTool approves or denies a tool call or an approval the session is
// waiting on, which resumes it, and returns the event the decision became.
func (s *SessionsService) ConfirmTool(ctx context.Context, id string, params *ToolConfirmationParams) (*Event, error) {
	if params == nil || (params.ToolUseID == "") == (params.ApprovalID == "") {
		return nil, errors.New("agents: ToolConfirmationParams needs exactly one of ToolUseID and ApprovalID")
	}
	if params.Decision != Allow && params.Decision != Deny {
		return nil, fmt.Errorf("agents: ToolConfirmationParams.Decision is %q, not Allow or Deny", params.Decision)
	}
	payload := map[string]any{"decision": params.Decision}
	if params.ToolUseID != "" {
		payload["tool_use_id"] = params.ToolUseID
	}
	if params.ApprovalID != "" {
		payload["approval_id"] = params.ApprovalID
	}
	if params.Note != "" {
		payload["note"] = params.Note
	}
	if params.Remember != "" {
		payload["remember"] = params.Remember
	}
	return s.send(ctx, id, EventUserToolConfirmation, payload, params.IdempotencyKey)
}

// send appends a user event to a session's log.
func (s *SessionsService) send(ctx context.Context, id string, t EventType, payload map[string]any, key string) (*Event, error) {
	if id == "" {
		return nil, errNoID
	}
	var out Event
	req := &latere.Request{
		Method:         http.MethodPost,
		Path:           sessionPath(id, "/events"),
		Body:           map[string]any{"type": t, "payload": payload},
		IdempotencyKey: keyOr(key),
	}
	if err := s.cfg.Do(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// StreamParams says where a stream of a session's log starts.
type StreamParams struct {
	// FromSeq is the sequence to start at; zero starts at the first event.
	FromSeq int64
	// Deltas also yields the live fragments of a response as the agent
	// writes it, before the EventAgentMessage that records it.
	Deltas bool
}

// A StreamItem is one item of a session's stream: an event of its log, or,
// with StreamParams.Deltas, a live fragment of a response.
type StreamItem struct {
	// Event is the log's event; nil for a delta.
	Event *Event
	// Delta is a live fragment; nil for an event.
	Delta *Delta
}

// A Delta is a live fragment of a response as the agent writes it. Deltas
// are not stored: the EventAgentMessage that follows holds the response.
type Delta struct {
	// Thread names the helper agent's thread the response belongs to;
	// empty for the session's own.
	Thread string `json:"thread,omitempty"`
	// Turn and Step place the response in the session.
	Turn int `json:"turn"`
	Step int `json:"step"`
	// Block is the index of the response's content block the text extends.
	Block int `json:"block"`
	// Kind is "text", "thinking" or "tool_input".
	Kind string `json:"kind"`
	// Text continues the block.
	Text string `json:"text"`
	// Reset discards what the step's deltas carried so far: the response is
	// being written again.
	Reset bool `json:"reset,omitempty"`
}

// Stream returns an iterator over a session's log from params.FromSeq that
// waits for each new event, and ends after the event that ends the session.
// When the connection drops it reconnects and resumes after the last event
// it yielded, so no event is lost or repeated. Stop the loop, or end ctx, to
// stop following.
func (s *SessionsService) Stream(ctx context.Context, id string, params *StreamParams) iter.Seq2[StreamItem, error] {
	return func(yield func(StreamItem, error) bool) {
		if id == "" {
			yield(StreamItem{}, errNoID)
			return
		}
		q := url.Values{}
		if params != nil {
			if params.FromSeq > 1 {
				q.Set("from_seq", strconv.FormatInt(params.FromSeq, 10))
			}
			if params.Deltas {
				q.Set("deltas", "1")
			}
		}
		req := &latere.Request{Method: http.MethodGet, Path: sessionPath(id, "/stream"), Query: q}
		for frame, err := range s.cfg.Stream(ctx, req, endsSession) {
			if err != nil {
				yield(StreamItem{}, err)
				return
			}
			item, err := decodeFrame(frame)
			if !yield(item, err) || err != nil {
				return
			}
		}
	}
}

// decodeFrame reads one frame of a session's stream.
func decodeFrame(f latere.Frame) (StreamItem, error) {
	if f.Event == "delta" {
		var d Delta
		if err := json.Unmarshal(f.Data, &d); err != nil {
			return StreamItem{}, fmt.Errorf("agents: decoding a delta: %w", err)
		}
		return StreamItem{Delta: &d}, nil
	}
	var e Event
	if err := json.Unmarshal(f.Data, &e); err != nil {
		return StreamItem{}, fmt.Errorf("agents: decoding the event %s of a stream: %w", f.ID, err)
	}
	return StreamItem{Event: &e}, nil
}

// endsSession reports whether a frame is the event that ends its session,
// after which the API closes the stream.
func endsSession(f latere.Frame) bool {
	if f.Event != string(EventSessionStatus) {
		return false
	}
	var e struct {
		Payload SessionStatus `json:"payload"`
	}
	return json.Unmarshal(f.Data, &e) == nil && e.Payload.Status == StatusEnded
}

// keyOr returns key, or a new idempotency key when it is empty.
func keyOr(key string) string {
	if key != "" {
		return key
	}
	return latere.NewIdempotencyKey()
}
