// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// EventType names the kind of an event in a session's log.
type EventType string

// The event types a client reads most. The log holds others too; an event
// of a type not listed here is still returned, with its payload unread.
const (
	// EventUserMessage is a message sent to the session.
	EventUserMessage EventType = "user.message"
	// EventUserInterrupt stopped the running turn.
	EventUserInterrupt EventType = "user.interrupt"
	// EventUserToolConfirmation approved or denied a tool call.
	EventUserToolConfirmation EventType = "user.tool_confirmation"
	// EventUserAnswer answered a question the agent asked.
	EventUserAnswer EventType = "user.answer"
	// EventUserToolResult is the result of a tool call your program ran;
	// its payload is a ToolResult.
	EventUserToolResult EventType = "user.tool_result"
	// EventAgentMessage is a response the agent wrote; its payload is an
	// AgentMessage.
	EventAgentMessage EventType = "agent.message"
	// EventAgentToolUse is a tool call the agent made; its payload is a
	// ToolUse. A question is a call of the tool named "question".
	EventAgentToolUse EventType = "agent.tool_use"
	// EventToolResult is the result of a tool call; its payload is a
	// ToolResult.
	EventToolResult EventType = "tool.result"
	// EventSessionStatus is a change of the session's status; its payload
	// is a SessionStatus.
	EventSessionStatus EventType = "session.status"
	// EventSessionError is an error the session ran into; its payload is a
	// SessionError.
	EventSessionError EventType = "session.error"
	// EventTitleChanged is a new title for the session.
	EventTitleChanged EventType = "session.title_changed"
	// EventApprovalRequested asks for an approval that is not a tool call.
	EventApprovalRequested EventType = "approval.requested"
	// EventApprovalDecided records the answer to an approval.
	EventApprovalDecided EventType = "approval.decided"
	// EventMachineWaiting is a session waiting for the computer it works on.
	EventMachineWaiting EventType = "session.machine_waiting"
	// EventSessionMachine is the machine the session's commands run on
	// from this event on: its first, one it moved to, a new one in place
	// of one that was lost, or its own restarted. Its payload is a
	// SessionMachine, whose working directory holds the files later events
	// name.
	EventSessionMachine EventType = "session.machine"
	// EventFilesKept follows an EventAgentMessage that shows pictures or
	// links files of the session's machine, and records the copies the
	// session kept of them; its payload is a FilesKept.
	EventFilesKept EventType = "files.kept"
)

// An Event is one entry of a session's log.
type Event struct {
	// ID identifies the event, such as "evt_01M34G2JF1P53NNG7DN08D5QAG".
	ID string `json:"id"`
	// Seq is the event's place in the log, from 1.
	Seq int64 `json:"seq"`
	// SessionID is the session whose log holds the event.
	SessionID string `json:"session_id"`
	// Type is the event's kind.
	Type EventType `json:"type"`
	// Time is when the event was appended.
	Time time.Time `json:"time"`
	// Turn is the turn the event belongs to; zero before the first turn.
	Turn int `json:"turn,omitempty"`
	// Step is the step of the turn the event belongs to, when it has one.
	Step int `json:"step,omitempty"`
	// Thread names the thread of a helper agent the event belongs to; empty
	// for the session's own thread.
	Thread string `json:"thread,omitempty"`
	// Payload is the event's content, whose shape depends on Type; read it
	// with Decode.
	Payload json.RawMessage `json:"payload"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the event, with every field,
// including those this package does not model; nil for an Event the program
// built itself.
func (e *Event) RawJSON() json.RawMessage { return e.raw }

// UnmarshalJSON decodes the event and keeps the JSON for RawJSON.
func (e *Event) UnmarshalJSON(b []byte) error {
	type plain Event
	if err := json.Unmarshal(b, (*plain)(e)); err != nil {
		return err
	}
	e.raw = keep(b)
	return nil
}

// Decode unmarshals the event's payload into v, such as a *SessionStatus
// for an event of type EventSessionStatus.
func (e *Event) Decode(v any) error {
	if err := json.Unmarshal(e.Payload, v); err != nil {
		return fmt.Errorf("agents: decoding the %s payload of %s: %w", e.Type, e.ID, err)
	}
	return nil
}

// SessionStatus is the payload of an EventSessionStatus event.
type SessionStatus struct {
	// Status is the session's new state.
	Status Status `json:"status"`
	// StopReason says why it stopped, when it is idle or ended.
	StopReason StopReason `json:"stop_reason,omitempty"`
	// Detail explains the stop, when there is more to say.
	Detail string `json:"detail,omitempty"`
	// Outcome is whether the turn that stopped brought anything to the
	// person, on the idle status that ends a turn a trigger or a wake
	// began; empty otherwise.
	Outcome TurnOutcome `json:"outcome,omitempty"`
}

// A ContentBlock is one part of a message.
type ContentBlock struct {
	// Type is the block's kind. A response holds blocks of kinds this
	// type does not read, such as "tool_use", whose own event carries
	// them.
	Type BlockType `json:"type"`
	// Text is the text of a BlockText or BlockThinking block.
	Text string `json:"text,omitempty"`
	// Image is the picture of a BlockImage block.
	Image *Image `json:"image,omitempty"`
}

// An Image is the picture of a BlockImage block. A message sends it as
// Data, PNG, JPEG, GIF or WebP, with the MediaType its bytes are; the API
// refuses a picture by URL in a message. ImageBlock builds one.
type Image struct {
	// MediaType is the picture's format, such as "image/png".
	MediaType string `json:"media_type,omitempty"`
	// Data is the picture, base64-encoded.
	Data string `json:"data,omitempty"`
	// URL is where a picture the API did not keep is, in place of Data.
	URL string `json:"url,omitempty"`
}

// BlockType is the kind of a ContentBlock.
type BlockType string

// The kinds of ContentBlock a client reads.
const (
	// BlockText is text.
	BlockText BlockType = "text"
	// BlockThinking is the model's reasoning before it answers, when the
	// model shows it.
	BlockThinking BlockType = "thinking"
	// BlockImage is a picture, which a message sends for the model to see.
	BlockImage BlockType = "image"
)

// Text returns a text block holding s.
func Text(s string) ContentBlock { return ContentBlock{Type: BlockText, Text: s} }

// ImageBlock returns an image block holding data, a picture of the
// mediaType it is: "image/png", "image/jpeg", "image/gif" or
// "image/webp". A message holds at most MaxImages of them, each at most
// MaxImageBytes.
func ImageBlock(mediaType string, data []byte) ContentBlock {
	return ContentBlock{Type: BlockImage, Image: &Image{MediaType: mediaType, Data: base64.StdEncoding.EncodeToString(data)}}
}

// textOf joins the text of the BlockText blocks of blocks.
func textOf(blocks []ContentBlock) string {
	var b strings.Builder
	for _, c := range blocks {
		if c.Type == BlockText {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

// An AgentMessage is the payload of an EventAgentMessage event: one
// response of the model, complete.
type AgentMessage struct {
	// Message is the response.
	Message Message `json:"message"`
	// StopReason says why the response ended: StopEndTurn when the agent
	// is done with the turn, or another reason, such as a tool call it
	// waits on.
	StopReason StopReason `json:"stop_reason"`
}

// Text returns the response's text, its text blocks joined, without its
// thinking.
func (m *AgentMessage) Text() string { return textOf(m.Message.Blocks) }

// A Message is a model's response.
type Message struct {
	// Role is "assistant" for a response of the agent.
	Role string `json:"role"`
	// Blocks are the response's parts, in order.
	Blocks []ContentBlock `json:"blocks"`
}

// A ToolResult is the payload of an EventToolResult event, the result of a
// tool call the platform ran, and of an EventUserToolResult event, the
// result your program sent with Client.SendToolResult.
type ToolResult struct {
	// ToolUseID names the call the result answers.
	ToolUseID string `json:"tool_use_id"`
	// Content is the result the agent read.
	Content []ContentBlock `json:"content"`
	// IsError reports that the call failed.
	IsError bool `json:"is_error,omitempty"`
	// Outcome sums the result up; empty when the tool says nothing more
	// than IsError.
	Outcome ToolOutcome `json:"outcome,omitempty"`
	// Sender is who sent an EventUserToolResult; nil for a result the
	// platform recorded.
	Sender *Sender `json:"sender,omitempty"`
	// Meta is what the tool recorded beside the result, such as the file a
	// write left or what a publish put up; nil for a result that records
	// nothing, such as every EventUserToolResult.
	Meta *ToolMeta `json:"meta,omitempty"`
}

// Text returns the result's text, its text blocks joined.
func (r *ToolResult) Text() string { return textOf(r.Content) }

// ToolMeta is what a tool call recorded beside its result, the Meta of a
// ToolResult. Its shape is the tool's own: a file tool's names the file,
// and a publish's what it published. The fields are those a client reads
// to show what a session made; RawJSON holds the whole record, with the
// parts of other tools this package does not model, such as the todo list
// a todo call leaves.
type ToolMeta struct {
	// Path is the file a read, a write or an edit touched, as its path on
	// the session's machine, such as "/workspace/report.md": a path the
	// agent named relative to the working directory is recorded as the
	// absolute path it names there. Empty on the results of other tools.
	Path string `json:"path,omitempty"`
	// SHA256 is the hex SHA-256 of the file's content at the read or the
	// write; empty for a read of a file that does not exist.
	SHA256 string `json:"sha256,omitempty"`
	// Size is the length in bytes of the file a write or an edit left.
	// Only a write or an edit that succeeded sets it, so a result with
	// Size set names a file the agent made or changed, and one with Path
	// and no Size a file it read.
	Size *int64 `json:"size,omitempty"`
	// Publish is what a publish call recorded; nil on the results of other
	// tools.
	Publish *PublishRecord `json:"publish,omitempty"`

	raw json.RawMessage
}

// RawJSON returns the JSON the result recorded as its meta, with every
// field, including those this package does not model; nil for a ToolMeta
// the program built itself.
func (m *ToolMeta) RawJSON() json.RawMessage { return m.raw }

// UnmarshalJSON decodes the meta and keeps the JSON for RawJSON.
func (m *ToolMeta) UnmarshalJSON(b []byte) error {
	type plain ToolMeta
	if err := json.Unmarshal(b, (*plain)(m)); err != nil {
		return err
	}
	m.raw = keep(b)
	return nil
}

// A PublishRecord is what a publish call recorded in its result's Meta:
// the app it published to, the addresses of the app and of the version
// the call put up, and where that version stands. A call that only
// changed who can open the app records the app, its address and its
// Visibility, with no Deploy and no Status.
//
// The record is the tool's report of the app's answer at the time of the
// call. A client that reads the app as it is now does so with package
// apps, and one that shows an address checks first that it is an http or
// https URL.
type PublishRecord struct {
	// App is the app's slug, its name in its address, which changes when
	// the app is renamed.
	App string `json:"app"`
	// AppID is the app's id, which a rename keeps: two records name the
	// same app when their AppIDs match, or, where one has none, their
	// Apps. Empty where the call learned no id.
	AppID string `json:"app_id,omitempty"`
	// Name is the app's name for a person.
	Name string `json:"name,omitempty"`
	// URL is the app's own address, where its released version is served.
	URL string `json:"url,omitempty"`
	// Preview is the address of the version the call put up, once the
	// build named one.
	Preview string `json:"preview,omitempty"`
	// Deploy is the id of the deploy that carries the version.
	Deploy string `json:"deploy,omitempty"`
	// Commit is the commit the call published or released.
	Commit string `json:"commit,omitempty"`
	// Release is the tag of a release.
	Release string `json:"release,omitempty"`
	// Status is where the version stands; empty for a call that only
	// changed who can open the app.
	Status PublishStatus `json:"status,omitempty"`
	// Visibility is who may open the app after the call: "private",
	// "unlisted" or "public", the values of apps.Visibility. Empty where
	// the call learned none.
	Visibility string `json:"visibility,omitempty"`
	// Error is the reason the app refused the call: a build or a release
	// that failed, was canceled or was refused, or a change of who can
	// open the app that was refused. Nil otherwise.
	Error *PublishError `json:"error,omitempty"`
}

// PublishStatus is where the version a publish call put up stands.
type PublishStatus string

// The statuses of a PublishRecord. A preview is building, ready, failed
// or canceled; a release is pending, released or refused.
const (
	// PublishBuilding is a preview whose build had not finished when the
	// call returned.
	PublishBuilding PublishStatus = "building"
	// PublishReady is a preview that built, served at the record's Preview.
	PublishReady PublishStatus = "ready"
	// PublishFailed is a preview whose build failed.
	PublishFailed PublishStatus = "failed"
	// PublishCanceled is a preview whose build was canceled.
	PublishCanceled PublishStatus = "canceled"
	// PublishPending is a release that was not yet live when the call
	// returned.
	PublishPending PublishStatus = "pending"
	// PublishReleased is a release served at the app's URL.
	PublishReleased PublishStatus = "released"
	// PublishRefused is a release the app refused, such as one whose
	// commit does not hold the commit the app serves.
	PublishRefused PublishStatus = "refused"
)

// PublishError is why an app refused a publish call.
type PublishError struct {
	// Code identifies the refusal, such as "behind_live".
	Code string `json:"code"`
	// Reason narrows the code, when the refusal names one, such as
	// "role_insufficient" for a change of who can open an organization's
	// app that the caller's role does not reach.
	Reason string `json:"reason,omitempty"`
	// Message says what happened, in words for a person.
	Message string `json:"message,omitempty"`
}

// A SessionError is the payload of an EventSessionError event: something
// went wrong in the session, such as a model that is busy or a machine
// that was lost.
type SessionError struct {
	// Code identifies the error, such as "model_busy" or "machine_lost".
	Code string `json:"code"`
	// Message says what happened, in words for a person.
	Message string `json:"message"`
	// Retryable reports that sending the message again may succeed.
	Retryable bool `json:"retryable,omitempty"`
	// Detail adds what a developer needs, when there is more to say.
	Detail string `json:"detail,omitempty"`
}

// SessionMachine is the payload of an EventSessionMachine event: the
// machine the session's commands run on from this event on, and why the
// session has it. A client that places the paths a session's later
// events name, such as a ToolMeta's Path or a link in an answer, reads
// the working directory of the last one before them.
type SessionMachine struct {
	// Machine is the machine.
	Machine AttachedMachine `json:"machine"`
	// Reason is why the session has it.
	Reason MachineReason `json:"reason"`
	// Cause is why a MachineRestarted machine had stopped, when its
	// provider says, such as "out_of_memory".
	Cause string `json:"cause,omitempty"`
}

// An AttachedMachine is a machine a session's commands run on, as the
// session recorded it.
type AttachedMachine struct {
	// Kind is the machine's kind.
	Kind MachineKind `json:"kind"`
	// ID identifies the machine at its provider, such as a sandbox's id.
	ID string `json:"id,omitempty"`
	// Environment is the environment the machine runs in: for a
	// MachineDevice, the computer's id, from account.Device.
	Environment string `json:"environment,omitempty"`
	// Workdir is the absolute path of the directory the session works in.
	// A path the agent names relative to it, in a tool call or a link of
	// an answer, is a file inside it.
	Workdir string `json:"workdir,omitempty"`
	// OS and Arch are the machine's operating system and architecture as
	// Go names them, such as "linux" and "arm64", when the machine said.
	OS   string `json:"os,omitempty"`
	Arch string `json:"arch,omitempty"`
}

// MachineReason is why a session has the machine an EventSessionMachine
// names.
type MachineReason string

// The reasons of a SessionMachine.
const (
	// MachineAttached is the session's first machine, or the first of a
	// fork.
	MachineAttached MachineReason = "attached"
	// MachineHandoff is a machine other than the one the session had,
	// which the session moved to.
	MachineHandoff MachineReason = "handoff"
	// MachineRestored is a new machine given back the files of the
	// session's working directory.
	MachineRestored MachineReason = "restored"
	// MachineReplaced is a new, empty machine in the place of one that was
	// lost: the files of the working directory are gone.
	MachineReplaced MachineReason = "replaced"
	// MachineRestarted is the machine the session had, running again after
	// it stopped on a failure: the files of its working directory are
	// kept, and the processes that ran on it are gone.
	MachineRestarted MachineReason = "restarted"
)

// A UserMessage is the payload of an EventUserMessage event.
type UserMessage struct {
	// Sender is who sent the message.
	Sender Sender `json:"sender"`
	// Content is the message, in order.
	Content []ContentBlock `json:"content"`
	// Attachments are the files the message carries, as the session keeps
	// them.
	Attachments []Attachment `json:"attachments,omitempty"`
	// FiringID is the firing that sent the message, for a message a
	// trigger sent; its turn is one a trigger began.
	FiringID string `json:"firing_id,omitempty"`
	// Wake is the wake that sent the message, whose content is the note
	// the agent wrote to itself; nil for any other message.
	Wake *WakeFiring `json:"wake,omitempty"`
}

// A WakeFiring is the wake a message came from.
type WakeFiring struct {
	// TriggerID is the wake's trigger, gone once it fired.
	TriggerID string `json:"trigger_id"`
	// SetAt is when the agent set it, and DueAt when it was to fire; a
	// wake held while every trigger was fires after DueAt.
	SetAt time.Time `json:"set_at"`
	DueAt time.Time `json:"due_at"`
	// Count is the wake's place in a row of wakes with no word from the
	// person who started the session between them, from 1.
	Count int `json:"count"`
}

// An Attachment is a file a message carries, as the session keeps it:
// the agent finds it in the session's working directory at Path.
type Attachment struct {
	// Name is the file's name, as the message sent it.
	Name string `json:"name"`
	// MediaType is the file's media type.
	MediaType string `json:"media_type"`
	// Size is the file's length in bytes.
	Size int64 `json:"size"`
	// Blob is the digest the session keeps the bytes by, "sha256:" and
	// the hex of their hash.
	Blob string `json:"blob"`
	// Path is where the agent finds the file, relative to the session's
	// working directory.
	Path string `json:"path"`
	// Ref is where the file is in the session's folder of files, its path
	// inside the Session's Folder, for a session that keeps one; empty
	// otherwise.
	Ref string `json:"ref,omitempty"`
	// Source is the file of the sender's Storage the bytes were read
	// from, for a file the message named by its source; nil otherwise.
	Source *AttachmentSource `json:"source,omitempty"`
	// Inline reports a short text file the model reads in its request,
	// beside the line that names it.
	Inline bool `json:"inline,omitempty"`
}

// FilesKept is the payload of an EventFilesKept event: the files of the
// session's machine that an answer showed as pictures or linked, which
// the session kept a copy of, and those it did not. A client shows a
// kept file from its copy, with Client.ReadBlob and its Blob, and may
// read one it did not keep with Client.ReadSessionFile while the
// session's machine runs.
type FilesKept struct {
	// MessageID is the id of the EventAgentMessage that names the files.
	MessageID string `json:"message"`
	// Files are the files the session kept.
	Files []KeptFile `json:"files"`
	// Skipped are the files the answer named that the session did not
	// keep, each with why.
	Skipped []SkippedFile `json:"skipped"`
}

// A KeptFile is a file of the session's machine that an answer showed or
// linked, as the session kept a copy of it.
type KeptFile struct {
	// Path is the file as the answer named it, such as "chart.png".
	Path string `json:"path"`
	// Resolved is the file's absolute path on the session's machine,
	// inside its working directory.
	Resolved string `json:"resolved"`
	// Blob is the digest the session keeps the copy by, "sha256:" and the
	// hex of its hash; read the copy with Client.ReadBlob.
	Blob string `json:"blob"`
	// MediaType is the file's media type: for a picture the answer
	// showed, the format of its bytes; for a file it linked, the type its
	// name's extension names, or else its leading bytes.
	MediaType string `json:"media_type"`
	// Size is the file's length in bytes.
	Size int64 `json:"size"`
	// Width and Height are a shown picture's size in pixels, and 0 for a
	// linked file, whatever its bytes are.
	Width  int `json:"width"`
	Height int `json:"height"`
	// Ref is where the copy is in the session's folder of files, its path
	// inside the Session's Folder, for a session that keeps one; empty
	// otherwise.
	Ref string `json:"ref,omitempty"`
}

// A SkippedFile is a file an answer named that the session did not keep.
type SkippedFile struct {
	// Path is the file as the answer named it.
	Path string `json:"path"`
	// Reason is why the session did not keep it.
	Reason SkipReason `json:"reason"`
}

// SkipReason is why a session did not keep a file an answer named.
type SkipReason string

// The reasons of a SkippedFile.
const (
	// SkipNotFound is a path nothing is at.
	SkipNotFound SkipReason = "not_found"
	// SkipTooLarge is a file past the size a file is kept at, or a picture
	// past the pixels a picture is kept at.
	SkipTooLarge SkipReason = "too_large"
	// SkipNotAnImage is a file shown as a picture that is no PNG, JPEG,
	// GIF or WebP, an SVG included, or no regular file.
	SkipNotAnImage SkipReason = "not_an_image"
	// SkipNotAFile is a link to something that is no regular file, such
	// as a directory.
	SkipNotAFile SkipReason = "not_a_file"
	// SkipOutsideWorkdir is a path outside the session's working
	// directory, or a link that leads out of it.
	SkipOutsideWorkdir SkipReason = "outside_workdir"
	// SkipNoMachine is a file named in a turn that opened no machine;
	// keeping a file never opens one.
	SkipNoMachine SkipReason = "no_machine"
	// SkipLimit is a file past the pictures or the linked files one
	// answer keeps, or past the bytes its linked files are kept at
	// together.
	SkipLimit SkipReason = "limit"
	// SkipUnavailable is a read or a copy that failed, which the turn
	// went on past.
	SkipUnavailable SkipReason = "unavailable"
)

// An AttachmentSource names a file of a person's Storage, which a message
// carries by reference: the platform reads its bytes when the message is
// sent and keeps a copy with the session.
type AttachmentSource struct {
	// Owner is the space the file is in: "me" for the sender's own, as
	// storage.Me names it, or the owner of a space shared with them.
	Owner string `json:"owner"`
	// Path is the file's path in the space, starting with "files/".
	Path string `json:"path"`
	// Version is the version of the file to read, counting from 1; zero
	// reads the file as it stands.
	Version int `json:"version,omitempty"`
}

// Question is a question an agent asked, the input of an EventAgentToolUse
// event whose tool is "question".
type Question struct {
	// Question is the full question.
	Question string `json:"question"`
	// Header is a short label for the question.
	Header string `json:"header"`
	// Options are the choices, in the order they are shown.
	Options []QuestionOption `json:"options"`
	// Multiple lets the person choose several options.
	Multiple bool `json:"multiple,omitempty"`
}

// QuestionOption is one choice of a Question.
type QuestionOption struct {
	// Label names the option; an answer names it by its label.
	Label string `json:"label"`
	// Description says what choosing the option means.
	Description string `json:"description"`
	// Recommended marks the option the agent would take.
	Recommended bool `json:"recommended,omitempty"`
}

// ToolUse is the payload of an EventAgentToolUse event.
type ToolUse struct {
	// ToolUseID identifies the call; an answer or a confirmation names it.
	ToolUseID string `json:"tool_use_id"`
	// Name is the tool's name, such as "question" or "bash".
	Name string `json:"name"`
	// Input is the call's input, whose shape depends on the tool; read the
	// questions of a "question" call with Questions.
	Input json.RawMessage `json:"input"`
	// Client reports a call of a tool the agent declares for its client
	// to run: your program runs it, and the session waits with
	// StopToolResult until Client.SendToolResult answers it.
	Client bool `json:"client,omitempty"`
	// Verdict is what the session's approval policy decided about the
	// call. VerdictAsk means the session waits with StopToolConfirmation
	// until Client.ConfirmTool answers the call.
	Verdict Verdict `json:"verdict,omitempty"`
	// Reason explains the verdict, when the policy gave one.
	Reason string `json:"reason,omitempty"`
}

// Verdict is what a session's approval policy decided about a tool call or
// a connection.
type Verdict string

// The verdicts of ToolUse and ApprovalRequested.
const (
	// VerdictAllow runs the call without asking.
	VerdictAllow Verdict = "allow"
	// VerdictAsk waits for a person to allow or deny the call.
	VerdictAsk Verdict = "ask"
	// VerdictDeny refuses the call; the agent is told.
	VerdictDeny Verdict = "deny"
	// VerdictFlag runs the call and marks it for review.
	VerdictFlag Verdict = "flag"
	// VerdictBlock refuses a connection without asking and records it.
	VerdictBlock Verdict = "block"
)

// ApprovalRequested is the payload of an EventApprovalRequested event:
// after a tool call ran, the session's network refused a connection the
// call made, and a person is asked whether the session may reach the host.
// Answer it with Client.ConfirmTool and ConfirmToolParams.ApprovalID.
type ApprovalRequested struct {
	// ApprovalID identifies the request; an answer names it.
	ApprovalID string `json:"approval_id"`
	// ToolUseID names the call that made the connection.
	ToolUseID string `json:"tool_use_id"`
	// Source is what raised the request.
	Source ApprovalSource `json:"source"`
	// Destination is the host the session tried to reach.
	Destination Destination `json:"destination"`
	// Reason explains the request, when there is more to say.
	Reason string `json:"reason,omitempty"`
	// Verdict is VerdictAsk for a request that waits for a person, or
	// VerdictBlock for one recorded without asking.
	Verdict Verdict `json:"verdict"`
	// More counts further connections to the same host the request
	// stands for.
	More int `json:"more,omitempty"`
}

// Destination is a host a session tried to reach.
type Destination struct {
	// Host is the host name or address.
	Host string `json:"host"`
	// Port is the port, when the request names one.
	Port int `json:"port,omitempty"`
}

// ApprovalDecided is the payload of an EventApprovalDecided event: the
// answer to an ApprovalRequested.
type ApprovalDecided struct {
	// ApprovalID names the request answered.
	ApprovalID string `json:"approval_id"`
	// Decision is DecisionAllow or DecisionDeny.
	Decision Decision `json:"decision"`
	// By is who answered, when a person did.
	By *Sender `json:"by,omitempty"`
	// Note is the answer's note, when it has one.
	Note string `json:"note,omitempty"`
}

// Questions returns the questions of a call of the "question" tool, and an
// error for a call of any other tool or an input that does not decode.
func (t *ToolUse) Questions() ([]Question, error) {
	if t.Name != "question" {
		return nil, fmt.Errorf("agents: the tool call %s is %q, not a question", t.ToolUseID, t.Name)
	}
	var in struct {
		Questions []Question `json:"questions"`
	}
	if err := json.Unmarshal(t.Input, &in); err != nil {
		return nil, fmt.Errorf("agents: decoding the questions of %s: %w", t.ToolUseID, err)
	}
	return in.Questions, nil
}

// ToolOutcome sums up how a tool call ended.
type ToolOutcome string

// The outcomes of a tool call.
const (
	// ToolOK ran and succeeded.
	ToolOK ToolOutcome = "ok"
	// ToolError ran and failed.
	ToolError ToolOutcome = "error"
	// ToolTimeout ran out of time.
	ToolTimeout ToolOutcome = "timeout"
	// ToolUnknown named a tool the agent does not have.
	ToolUnknown ToolOutcome = "unknown_tool"
	// ToolInvalidInput was called with input the tool refused.
	ToolInvalidInput ToolOutcome = "invalid_input"
	// ToolDenied was denied: by a person, or because nobody attended the
	// session to approve it.
	ToolDenied ToolOutcome = "denied"
	// ToolBlocked was refused by the approval policy.
	ToolBlocked ToolOutcome = "blocked"
	// ToolCanceled was canceled, such as by an interrupt.
	ToolCanceled ToolOutcome = "canceled"
	// ToolUnknownEffect stopped where it cannot be known whether it took
	// effect, such as a command cut off mid-run.
	ToolUnknownEffect ToolOutcome = "unknown_effect"
	// ToolUnanswered is a question nobody chose an option of; it is not an
	// error, since the call did what it was for.
	ToolUnanswered ToolOutcome = "unanswered"
)

// ApprovalSource is what raised an approval request.
type ApprovalSource string

// The sources of an approval request.
const (
	// ApprovalFromEgress is a connection the session's network refused.
	ApprovalFromEgress ApprovalSource = "egress"
)
