// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package memory reads and changes what Latere remembers about a person
// across conversations.
//
// A memory is one Markdown file in the person's Storage: a name of a few
// words, a description that is the memory itself in one line, a type that
// groups it with others, such as a preference or a person, and an optional
// body with more detail. Each memory belongs to a scope: You, which every
// conversation in the person's personal context reads, or one of their
// projects, which only that project's conversations read. Conversations
// save and forget memories as they go; this package is how a program shows
// a person what is remembered and lets them add, change, move, delete,
// restore, export and import memories, and turn Remember new things,
// Search past chats and Include sensitive topics on or off.
//
//	cfg, err := latere.NewConfig() // the key in LATERE_KEY
//	if err != nil {
//		return err
//	}
//	list, err := memory.New(cfg).ListMemories(ctx)
//	if err != nil {
//		return err
//	}
//	for _, group := range list.Limits.Types {
//		for _, m := range list.Memories {
//			if m.Type == group {
//				fmt.Printf("%s: %s\n", group, m.Description)
//			}
//		}
//	}
//
// A single memory is addressed by a Ref: its scope, its project when the
// scope is a project's, and its id. Memory.Ref and Forgotten.Ref return
// the Ref of what a list answered. A change or a delete names the
// checksum the memory had when it was read, so it is refused with an error
// matching ErrChanged rather than overwrite a change made since.
//
// Memory is kept for a person in their personal context. A token of an
// organization's context, or a service account's, is refused with an
// error matching ErrPersonalOnly, and a conversation's agent's token with
// one matching ErrAgentToken, since an agent changes memory with its file
// tools.
//
// The bounds a memory is held to and the words a person asks another
// assistant before an import come with the list, in Limits and
// ImportPrompt, so a program reads them there rather than writing its own.
//
// Every resource a call returns keeps the JSON it was decoded from, which
// RawJSON returns with every field, including those this version of the
// package does not model yet.
package memory

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"latere.ai/x/sdk-go/latere"
)

// basePath is where the memory routes live.
const basePath = "/v1/account/memory"

// Client reaches the memory part of the Latere API. It is safe for
// concurrent use.
type Client struct {
	cfg *latere.Config
}

// New returns a Client that sends its requests as cfg says. A cfg not
// built with latere.NewConfig makes every call fail with an error saying
// so.
func New(cfg *latere.Config) *Client { return &Client{cfg: cfg} }

// do sends r and decodes its answer into out, with the sentinel of the
// API's code on a refusal that has one.
func (c *Client) do(ctx context.Context, r *latere.Request, out any) error {
	return withSentinel(c.cfg.Do(ctx, r, out))
}

// Scope says whose a memory is.
type Scope string

// The scopes of a memory.
const (
	// ScopeYou is the person's own memory, which every conversation in
	// their personal context reads.
	ScopeYou Scope = "you"
	// ScopeProject is a project's memory, which only that project's
	// conversations read. The project's id goes beside it.
	ScopeProject Scope = "project"
)

// Type is the group a memory is shown in. Limits.Types lists the groups in
// the order a program shows them.
type Type string

// The types of a memory.
const (
	// TypePreference is how the person likes to be answered, shown under
	// Preferences. A memory added with no type is one.
	TypePreference Type = "preference"
	// TypeProfile is a fact about the person, shown under Profile.
	TypeProfile Type = "profile"
	// TypeTopic is a topic the person keeps coming back to, shown under
	// Topics.
	TypeTopic Type = "topic"
	// TypeArea is an area of the person's life or work, shown under Areas.
	TypeArea Type = "area"
	// TypePerson is someone the person mentions, shown under People.
	TypePerson Type = "person"
)

// The sources of a memory a conversation did not save. A memory a
// conversation saved has the conversation's id as its source.
const (
	// SourceYou is a memory the person added, with AddMemory or by hand.
	SourceYou = "you"
	// SourceImport is a memory ImportMemory saved.
	SourceImport = "import"
)

// A Ref addresses one memory: its scope, the project when the scope is a
// project's, and its id in the scope. A Ref with no Scope is of You when
// it names no Project, and of the Project when it names one.
type Ref struct {
	// Scope is whose the memory is.
	Scope Scope `json:"scope,omitempty"`
	// Project is the id of the project whose memory it is, "prj_" and 26
	// characters, with ScopeProject; empty for You.
	Project string `json:"project,omitempty"`
	// ID is the memory's id in its scope, its file name without ".md":
	// lower-case letters, digits and "-", as the list answers it.
	ID string `json:"id"`
}

// path returns the path of the memory r addresses, or an error, before
// any request, when r names no id or a scope its project does not fit.
func (r Ref) path() (string, error) {
	if r.ID == "" {
		return "", errors.New("memory: the memory's id is empty")
	}
	switch {
	case r.Scope == ScopeProject || (r.Scope == "" && r.Project != ""):
		if r.Project == "" {
			return "", errors.New("memory: a project's memory names no project")
		}
		return basePath + "/projects/" + url.PathEscape(r.Project) + "/" + url.PathEscape(r.ID), nil
	case r.Scope == ScopeYou || r.Scope == "":
		if r.Project != "" {
			return "", errors.New("memory: a memory of You names a project")
		}
		return basePath + "/you/" + url.PathEscape(r.ID), nil
	}
	return "", errors.New("memory: the scope " + string(r.Scope) + " is neither ScopeYou nor ScopeProject")
}

// A Memory is one thing Latere remembers about the person.
type Memory struct {
	// Scope is whose the memory is: the person's own, or a project's.
	Scope Scope `json:"scope"`
	// Project is the project's id, on a project's memory.
	Project string `json:"project,omitempty"`
	// ID is the memory's id in its scope, which a Ref names.
	ID string `json:"id"`
	// Name is a title of a few words.
	Name string `json:"name"`
	// Description is the memory itself, in one line.
	Description string `json:"description"`
	// Type is the group the memory is shown in.
	Type Type `json:"type"`
	// Source is where the memory came from: the id of the conversation
	// that saved it, SourceYou or SourceImport.
	Source string `json:"source,omitempty"`
	// Sensitive reports that the memory is about a sensitive topic, such
	// as health or beliefs, which is kept only while Include sensitive
	// topics is on.
	Sensitive bool `json:"sensitive,omitempty"`
	// Body is more detail, on a memory read alone; a list answers none. A
	// memory names another as "[[id]]".
	Body string `json:"body,omitempty"`
	// Checksum is what IfMatch names to change or delete the memory only
	// as it was read.
	Checksum string `json:"checksum"`
	// Size is the length of the memory's file, in bytes.
	Size int64 `json:"size"`
	// Updated is when the memory was last saved, on a list and on the
	// answer of an add or a change; zero otherwise.
	Updated time.Time `json:"updated,omitzero"`

	raw json.RawMessage
}

// Ref returns the Ref that addresses the memory.
func (m *Memory) Ref() Ref { return Ref{Scope: m.Scope, Project: m.Project, ID: m.ID} }

// RawJSON returns the JSON the API answered for the memory, with every
// field, including those this package does not model; nil for a Memory the
// program built itself.
func (m *Memory) RawJSON() json.RawMessage { return m.raw }

// UnmarshalJSON decodes the memory and keeps the JSON for RawJSON.
func (m *Memory) UnmarshalJSON(b []byte) error {
	type plain Memory
	if err := json.Unmarshal(b, (*plain)(m)); err != nil {
		return err
	}
	m.raw = keep(b)
	return nil
}

// A List is the person's memory as ListMemories answers it.
type List struct {
	// Settings are the person's memory settings.
	Settings Settings `json:"settings"`
	// Memories are every memory of You and of each of the person's
	// projects, without bodies. A program groups them by Type in the order
	// of Limits.Types.
	Memories []*Memory `json:"memories"`
	// Projects are the person's projects, by id and name, for the memories
	// of a project.
	Projects []Project `json:"projects"`
	// More reports that the person's memory holds more files than one list
	// reads, so Memories leaves some out.
	More bool `json:"more"`
	// Limits are the bounds a memory is held to.
	Limits Limits `json:"limits"`
	// ImportPrompt is what a person asks another assistant before an
	// import: its answer is ImportMemoryParams.Text.
	ImportPrompt string `json:"import_prompt"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the list, with every
// field, including those this package does not model; nil for a List the
// program built itself.
func (l *List) RawJSON() json.RawMessage { return l.raw }

// UnmarshalJSON decodes the list and keeps the JSON for RawJSON.
func (l *List) UnmarshalJSON(b []byte) error {
	type plain List
	if err := json.Unmarshal(b, (*plain)(l)); err != nil {
		return err
	}
	l.raw = keep(b)
	return nil
}

// Settings are a person's memory settings.
type Settings struct {
	// Remember is Remember new things: whether conversations save new
	// memories. On by default. With it off, conversations still read what
	// is remembered.
	Remember bool `json:"remember"`
	// Chats is Search past chats: whether conversations search and read
	// the person's past conversations when one is mentioned or an answer
	// depends on something said before; in a project, only that project's
	// conversations. On by default.
	Chats bool `json:"chats"`
	// Sensitive is Include sensitive topics: whether memories about health
	// conditions, religious beliefs, political opinions, sexual
	// orientation, racial or ethnic origin, and union membership are kept.
	// Off by default.
	Sensitive bool `json:"sensitive"`
}

// A Project is one of the person's projects, as the list names it.
type Project struct {
	// ID is the project's id, which a project's memory names in Project.
	ID string `json:"id"`
	// Name is what the project is called.
	Name string `json:"name"`
}

// Limits are the bounds a memory is held to, as the API holds them.
type Limits struct {
	// MaxName is the most characters of a memory's name.
	MaxName int `json:"max_name"`
	// MaxDescription is the most characters of a memory's description.
	MaxDescription int `json:"max_description"`
	// MaxBytes is the most bytes of a memory's file, its name, description
	// and body together; a memory past it is refused with an error
	// matching ErrTooLarge.
	MaxBytes int `json:"max_bytes"`
	// MaxImportLines is the most lines one import reads.
	MaxImportLines int `json:"max_import_lines"`
	// Types are the groups, in the order a program shows them.
	Types []Type `json:"types"`
}

// ListMemories returns the person's memory settings and every memory of
// You and of each of their projects, without bodies, with the projects'
// names, the bounds a memory is held to and the words of an import. Read
// one memory's body with GetMemory.
func (c *Client) ListMemories(ctx context.Context) (*List, error) {
	var out List
	if err := c.do(ctx, &latere.Request{Method: http.MethodGet, Path: basePath}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AddMemoryParams are a memory the person adds.
type AddMemoryParams struct {
	// Scope is whose the memory is: ScopeYou, or ScopeProject with
	// Project. Empty is ScopeYou.
	Scope Scope `json:"scope,omitempty"`
	// Project is the id of the project the memory is added to, with
	// ScopeProject.
	Project string `json:"project,omitempty"`
	// Type is the memory's group; empty is TypePreference.
	Type Type `json:"type,omitempty"`
	// Name is a title of a few words, one line of at most Limits.MaxName
	// characters; empty makes one from the description's first words.
	Name string `json:"name,omitempty"`
	// Description is the memory itself, one line of at most
	// Limits.MaxDescription characters. Required.
	Description string `json:"description"`
	// Body is more detail; empty for none.
	Body string `json:"body,omitempty"`
}

// AddMemory saves one memory as the person typed it, with Source
// SourceYou, in You or one of their projects, and returns it with its
// body. Its ID is made from its name, the first one free in the scope.
// A member that breaks its rule is refused with an error matching
// ErrInvalidField, a memory past Limits.MaxBytes with one matching
// ErrTooLarge, and a project the person does not hold with one matching
// latere.ErrNotFound.
//
// AddMemory is sent once and never retried: the API takes no idempotency
// key on it, so a repeat after a lost answer would save the memory twice,
// the second under another id. After a failure on the network,
// ListMemories tells whether it was saved.
func (c *Client) AddMemory(ctx context.Context, params *AddMemoryParams) (*Memory, error) {
	if params == nil || strings.TrimSpace(params.Description) == "" {
		return nil, errors.New("memory: AddMemory: the description is empty")
	}
	var out Memory
	req := &latere.Request{Method: http.MethodPost, Path: basePath, Body: params, Retry: latere.RetryNever}
	if err := c.do(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetMemory returns the memory ref addresses, with its body and its
// checksum. A memory the scope does not hold returns an error matching
// ErrNotFound; a project the person does not hold, one matching
// latere.ErrNotFound alone.
func (c *Client) GetMemory(ctx context.Context, ref Ref) (*Memory, error) {
	p, err := ref.path()
	if err != nil {
		return nil, err
	}
	var out Memory
	if err := c.do(ctx, &latere.Request{Method: http.MethodGet, Path: p}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateMemoryParams is a change of a memory. A member left at its zero
// value keeps what the memory has.
type UpdateMemoryParams struct {
	// Name is the memory's new name, by AddMemoryParams.Name's rule.
	Name *string `json:"name,omitempty"`
	// Description is its new description, by
	// AddMemoryParams.Description's rule.
	Description *string `json:"description,omitempty"`
	// Type is its new group.
	Type Type `json:"type,omitempty"`
	// Body is its new body; a pointer to "" removes it.
	Body *string `json:"body,omitempty"`
	// Scope moves the memory: ScopeYou to the person's own, or
	// ScopeProject with Project to one of their projects.
	Scope Scope `json:"scope,omitempty"`
	// Project is the id of the project the memory moves to, with
	// ScopeProject.
	Project string `json:"project,omitempty"`
	// IfMatch makes the change only if the memory still has this checksum,
	// as Memory.Checksum names it; otherwise it is refused with an error
	// matching ErrChanged. Empty changes the memory whatever it holds.
	IfMatch string `json:"-"`
}

// moves reports whether the change moves the memory to another scope.
func (p *UpdateMemoryParams) moves() bool { return p.Scope != "" || p.Project != "" }

// UpdateMemory changes the name, the description, the type or the body of
// the memory ref addresses, or moves it to You or one of the person's
// projects, and returns it as it is now. A move gives the memory the first
// id free in its new scope and deletes it where it was, so a program reads
// the new Ref from the answer. A member that breaks its rule is refused
// with an error matching ErrInvalidField, a memory past Limits.MaxBytes
// with one matching ErrTooLarge, and a memory the scope does not hold with
// one matching ErrNotFound.
//
// A change in place only sets values, so it is retried after a failure;
// under IfMatch, a retry can find its own first attempt landed and be
// refused with ErrChanged for it, and GetMemory then tells. A move is sent
// once and never retried: a repeat after a lost answer would find nothing
// left to move and be refused with ErrNotFound, though the move succeeded;
// ListMemories tells where the memory is.
func (c *Client) UpdateMemory(ctx context.Context, ref Ref, params *UpdateMemoryParams) (*Memory, error) {
	p, err := ref.path()
	if err != nil {
		return nil, err
	}
	if params == nil {
		params = &UpdateMemoryParams{}
	}
	req := &latere.Request{Method: http.MethodPatch, Path: p, Body: params, Header: ifMatch(params.IfMatch), Retry: latere.RetryAlways}
	if params.moves() {
		req.Retry = latere.RetryNever
	}
	var out Memory
	if err := c.do(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteOptions are the choices of a delete. A nil *DeleteOptions deletes
// the memory whatever it holds.
type DeleteOptions struct {
	// IfMatch deletes the memory only if it still has this checksum, as
	// Memory.Checksum names it; otherwise the delete is refused with an
	// error matching ErrChanged.
	IfMatch string
}

// DeleteMemory deletes the memory ref addresses for good; it is not kept
// among what was recently forgotten. A memory the scope does not hold
// returns an error matching ErrNotFound. A delete is retried after a
// failure on the network, so a retry can meet the ErrNotFound of a delete
// that succeeded; the memory is gone either way.
func (c *Client) DeleteMemory(ctx context.Context, ref Ref, opts *DeleteOptions) error {
	p, err := ref.path()
	if err != nil {
		return err
	}
	var header http.Header
	if opts != nil {
		header = ifMatch(opts.IfMatch)
	}
	return c.do(ctx, &latere.Request{Method: http.MethodDelete, Path: p, Header: header}, nil)
}

// ifMatch returns the header that names checksum as an entity tag, nil
// for none.
func ifMatch(checksum string) http.Header {
	if checksum == "" {
		return nil
	}
	return http.Header{"If-Match": {`"` + strings.Trim(checksum, `"`) + `"`}}
}

// ForgetEverything deletes every memory of the person and of their
// projects for good, with what was recently forgotten; the settings stay
// as they are. A program asks the person to confirm first. Forgetting
// everything again changes nothing, so the call is retried and safe to
// repeat.
func (c *Client) ForgetEverything(ctx context.Context) error {
	return c.do(ctx, &latere.Request{Method: http.MethodDelete, Path: basePath}, nil)
}

// UpdateSettingsParams are the settings to change. A nil member keeps
// what the person has.
type UpdateSettingsParams struct {
	// Remember turns Remember new things on or off. With it off,
	// conversations still read what is remembered and save nothing new,
	// from their next save on.
	Remember *bool `json:"remember,omitempty"`
	// Chats turns Search past chats on or off. With it off, conversations
	// read none of the person's past conversations, from their next read
	// on, running ones included.
	Chats *bool `json:"chats,omitempty"`
	// Sensitive turns Include sensitive topics on or off. Turning it off
	// deletes, for good, every memory marked Sensitive; a program asks the
	// person to confirm first.
	Sensitive *bool `json:"sensitive,omitempty"`
}

// SettingsChanged are the person's memory settings after a change.
type SettingsChanged struct {
	// Remember is Remember new things, as Settings.Remember.
	Remember bool `json:"remember"`
	// Chats is Search past chats, as Settings.Chats.
	Chats bool `json:"chats"`
	// Sensitive is Include sensitive topics, as Settings.Sensitive.
	Sensitive bool `json:"sensitive"`
	// DeletedSensitive is how many memories marked sensitive the change
	// deleted, when it turned Include sensitive topics off.
	DeletedSensitive int `json:"deleted_sensitive"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the settings, with every
// field, including those this package does not model; nil for a
// SettingsChanged the program built itself.
func (s *SettingsChanged) RawJSON() json.RawMessage { return s.raw }

// UnmarshalJSON decodes the settings and keeps the JSON for RawJSON.
func (s *SettingsChanged) UnmarshalJSON(b []byte) error {
	type plain SettingsChanged
	if err := json.Unmarshal(b, (*plain)(s)); err != nil {
		return err
	}
	s.raw = keep(b)
	return nil
}

// UpdateSettings turns any of Remember new things, Search past chats and
// Include sensitive topics on or off, and returns the settings as they
// are now. A nil params, or one that sets none, changes nothing and
// returns them.
//
// A change only sets values, so it is retried after a failure. A retry
// after a lost answer that turned Include sensitive topics off finds
// nothing left to delete, and its DeletedSensitive is 0.
func (c *Client) UpdateSettings(ctx context.Context, params *UpdateSettingsParams) (*SettingsChanged, error) {
	if params == nil {
		params = &UpdateSettingsParams{}
	}
	var out SettingsChanged
	req := &latere.Request{Method: http.MethodPatch, Path: basePath + "/settings", Body: params, Retry: latere.RetryAlways}
	if err := c.do(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// A ForgottenList is what conversations forgot in the last 30 days and
// can be restored, as ListForgotten answers it.
type ForgottenList struct {
	// Forgotten are the memories, the one forgotten last first.
	Forgotten []Forgotten `json:"forgotten"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the list, with every
// field, including those this package does not model; nil for a
// ForgottenList the program built itself.
func (l *ForgottenList) RawJSON() json.RawMessage { return l.raw }

// UnmarshalJSON decodes the list and keeps the JSON for RawJSON.
func (l *ForgottenList) UnmarshalJSON(b []byte) error {
	type plain ForgottenList
	if err := json.Unmarshal(b, (*plain)(l)); err != nil {
		return err
	}
	l.raw = keep(b)
	return nil
}

// Forgotten is a memory a conversation forgot, kept until PurgesAt so the
// person can restore it.
type Forgotten struct {
	// Scope is whose the memory was.
	Scope Scope `json:"scope"`
	// Project is the project's id, on a project's memory.
	Project string `json:"project,omitempty"`
	// ID is the memory's id in its scope.
	ID string `json:"id"`
	// Size is the length of the memory's file, in bytes.
	Size int64 `json:"size"`
	// ForgottenAt is when the conversation forgot it.
	ForgottenAt time.Time `json:"forgotten_at"`
	// PurgesAt is when it is gone for good.
	PurgesAt time.Time `json:"purges_at"`
}

// Ref returns the Ref RestoreMemory takes to restore the memory.
func (f Forgotten) Ref() Ref { return Ref{Scope: f.Scope, Project: f.Project, ID: f.ID} }

// ListForgotten returns what conversations forgot in the last 30 days,
// the one forgotten last first, each until its PurgesAt. A memory the
// person deleted is gone for good and is not listed.
func (c *Client) ListForgotten(ctx context.Context) (*ForgottenList, error) {
	var out ForgottenList
	if err := c.do(ctx, &latere.Request{Method: http.MethodGet, Path: basePath + "/forgotten"}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RestoreMemory brings a memory a conversation forgot back where it was,
// and returns it with its body. Nothing forgotten of that id returns an
// error matching ErrNotFound, and a memory of the same id saved since
// one matching ErrTaken: rename or delete that one first.
//
// RestoreMemory is sent once and never retried: a repeat after a lost
// answer would find nothing left to restore and be refused with
// ErrNotFound, though the restore succeeded; GetMemory tells.
func (c *Client) RestoreMemory(ctx context.Context, ref Ref) (*Memory, error) {
	if _, err := ref.path(); err != nil {
		return nil, err
	}
	var out Memory
	req := &latere.Request{Method: http.MethodPost, Path: basePath + "/forgotten/restore", Body: ref, Retry: latere.RetryNever}
	if err := c.do(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ImportMemoryParams are what another assistant remembers, to save as
// memories.
type ImportMemoryParams struct {
	// Text is the other assistant's answer to List.ImportPrompt: one
	// memory per line, "Group: what it remembers", where Group is
	// Preference, Profile, Topic, Area, Person or Project. A line with no
	// group is a topic, and a Project line a topic of the scope imported
	// into. At most Limits.MaxImportLines lines are read. Required.
	Text string `json:"text"`
	// Scope is whose the memories are: ScopeYou, or ScopeProject with
	// Project. Empty is ScopeYou.
	Scope Scope `json:"scope,omitempty"`
	// Project is the id of the project they are imported into, with
	// ScopeProject.
	Project string `json:"project,omitempty"`
}

// Imported is what an import saved.
type Imported struct {
	// Added are the memories saved, each with Source SourceImport, without
	// bodies.
	Added []*Memory `json:"added"`
	// Skipped is how many lines the scope already held, which were left
	// out.
	Skipped int `json:"skipped"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the import, with every
// field, including those this package does not model; nil for an
// Imported the program built itself.
func (i *Imported) RawJSON() json.RawMessage { return i.raw }

// UnmarshalJSON decodes the import and keeps the JSON for RawJSON.
func (i *Imported) UnmarshalJSON(b []byte) error {
	type plain Imported
	if err := json.Unmarshal(b, (*plain)(i)); err != nil {
		return err
	}
	i.raw = keep(b)
	return nil
}

// ImportMemory saves what another assistant remembers, as it answered
// List.ImportPrompt, each line as a memory of the scope, and returns what
// it saved. A line whose memory the scope already holds is left out and
// counted in Skipped. A project the person does not hold is refused with
// an error matching latere.ErrNotFound.
//
// ImportMemory is sent once and never retried: the API takes no
// idempotency key on it, and a repeat that reached it while the first
// import was still saving could save a line twice. An import that failed
// partway keeps the lines it saved; calling ImportMemory again with the
// same text saves the rest, and counts the lines saved before in
// Skipped.
func (c *Client) ImportMemory(ctx context.Context, params *ImportMemoryParams) (*Imported, error) {
	if params == nil || strings.TrimSpace(params.Text) == "" {
		return nil, errors.New("memory: ImportMemory: the text is empty")
	}
	var out Imported
	req := &latere.Request{Method: http.MethodPost, Path: basePath + "/import", Body: params, Retry: latere.RetryNever}
	if err := c.do(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// keep returns a copy of b, which a decoder may reuse after it returns.
func keep(b []byte) json.RawMessage {
	return append(json.RawMessage(nil), b...)
}
