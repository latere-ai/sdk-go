// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package messaging

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"latere.ai/x/sdk-go/latere"
	"latere.ai/x/sdk-go/mls"
)

// basePath is where the messaging routes live.
const basePath = "/v1/messaging"

// DeviceHeader names the device a request acts for.
const DeviceHeader = "Sermo-Device"

// MaxWait is the longest Messages waits for a message to arrive.
const MaxWait = 30 * time.Second

// Client reaches the messaging service for one device of a person: the
// directory, the key packages, each conversation's log and group info,
// the Welcomes filed for the device and the backup key's recovery and
// transfer. The service holds ciphertext and public state alone; this
// package's Policy checks everything a device takes from it. A Client is
// safe for concurrent use.
//
// The Config's credential is the person's own token, such as package
// signin gives a device after sign-in; a platform key reaches no
// conversation.
type Client struct {
	cfg    *latere.Config
	device string
}

// NewClient returns a Client that sends its requests as cfg says, acting
// for device: the id of the device's record in the person's directory.
// Calls that act for a device name it in DeviceHeader, and the service
// refuses them with an error matching ErrNotYourDevice until the device's
// record stands. A cfg not built with latere.NewConfig makes every call
// fail with an error saying so.
func NewClient(cfg *latere.Config, device string) *Client {
	return &Client{cfg: cfg, device: device}
}

// Device is the id of the device the Client acts for.
func (c *Client) Device() string { return c.device }

// Errors of the service's refusals to test with errors.Is, one for each
// code a caller branches on. The error that matches one wraps the
// *latere.Error the service answered, which errors.As still finds with
// its status, its message, its detail and its request id.
var (
	// ErrNotYourDevice matches a 403 with the code "not_your_device": the
	// Client's device is not a device of the caller's that stands, never
	// registered or revoked. latere.ErrForbidden matches it too.
	ErrNotYourDevice = errors.New("messaging: the device is not one of the caller's that stands")
	// ErrExists matches a 409 with the code "exists": a conversation of the
	// id is there already, or a record's id is registered with other keys,
	// or its key is revoked. latere.ErrConflict matches it too.
	ErrExists = errors.New("messaging: that exists already")
	// ErrEpochTaken matches a 409 with the code "epoch_taken": another
	// commit took the epoch first. Read the log, merge what came, and
	// commit again if it is still wanted; a commit sent again after its
	// first send was taken is refused this way too, and the log shows it as
	// the device's own. latere.ErrConflict matches it too.
	ErrEpochTaken = errors.New("messaging: another commit took the epoch first")
	// ErrWrongEpoch matches a 409 with the code "wrong_epoch": an
	// application message is of an epoch that has ended. Read the log and
	// send it again in the current epoch. latere.ErrConflict matches it
	// too.
	ErrWrongEpoch = errors.New("messaging: the message is of an epoch that has ended")
	// ErrTooLarge matches a 413 with the code "too_large": the body, a
	// message or a count is over the service's bound.
	ErrTooLarge = errors.New("messaging: that is too large to send")
	// ErrAuthorizerUnavailable matches a 503 with the code
	// "authorizer_unavailable": the service could not ask whether the
	// caller may. The calls that are safe to repeat were retried already;
	// try again shortly.
	ErrAuthorizerUnavailable = errors.New("messaging: the service could not check the request")
)

// codes are the service's codes of the refusals that have a sentinel. A
// commit or message the conversation's rules refuse is "refused", which
// ErrRefused matches as it does the Policy's own refusals.
var codes = map[string]error{
	"not_your_device":        ErrNotYourDevice,
	"exists":                 ErrExists,
	"epoch_taken":            ErrEpochTaken,
	"wrong_epoch":            ErrWrongEpoch,
	"refused":                ErrRefused,
	"too_large":              ErrTooLarge,
	"authorizer_unavailable": ErrAuthorizerUnavailable,
}

// codeError is an *latere.Error that also matches the sentinel of its
// code, for a refusal whose status alone does not tell it apart, such as
// the three kinds of 409.
type codeError struct {
	err      *latere.Error
	sentinel error
}

func (e *codeError) Error() string { return e.err.Error() }

// Is reports whether target is the sentinel of the error's code; the
// *latere.Error it unwraps to matches the sentinels of its status.
func (e *codeError) Is(target error) bool { return target == e.sentinel }

// Unwrap returns the service's answer.
func (e *codeError) Unwrap() error { return e.err }

func withSentinel(err error) error {
	var apiErr *latere.Error
	if !errors.As(err, &apiErr) {
		return err
	}
	if sentinel, ok := codes[apiErr.Code]; ok {
		return &codeError{err: apiErr, sentinel: sentinel}
	}
	return err
}

// do sends r, as the device when asDevice, and decodes the answer into
// out. Only the calls that act for a device name it: a device registers
// its record before it stands, so a call that names it then is refused.
func (c *Client) do(ctx context.Context, r *latere.Request, asDevice bool, out any) error {
	if asDevice {
		r.Header = http.Header{DeviceHeader: {c.device}}
	}
	return withSentinel(c.cfg.Do(ctx, r, out))
}

// ExternalSender is the service as an MLS external sender, which every
// conversation's group names: CreateOptions takes it.
func (c *Client) ExternalSender(ctx context.Context) (mls.ExternalSender, error) {
	var out struct {
		ExternalSender struct {
			SignatureKey []byte `json:"signature_key"`
			Identity     string `json:"identity"`
		} `json:"external_sender"`
	}
	if err := c.do(ctx, &latere.Request{Method: http.MethodGet, Path: basePath + "/service"}, false, &out); err != nil {
		return mls.ExternalSender{}, err
	}
	return mls.ExternalSender{SignatureKey: out.ExternalSender.SignatureKey,
		Credential: mls.BasicCredential([]byte(out.ExternalSender.Identity))}, nil
}

// DeviceList is what the service lists of a person's directory: every
// record and every revocation, verified by no one until NewDirectory
// checks them.
type DeviceList struct {
	Records     []DeviceRecord `json:"records"`
	Revocations []Revocation   `json:"revocations"`
}

// Devices lists a person's directory as the service holds it. Directory
// checks it.
func (c *Client) Devices(ctx context.Context, person string) (*DeviceList, error) {
	var out DeviceList
	if err := c.do(ctx, &latere.Request{Method: http.MethodGet, Path: basePath + "/people/" + url.PathEscape(person) + "/devices"}, false, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Directory is a person's directory as NewDirectory verifies what the
// service lists, with the revocations the app kept for the person as
// known. It returns how many listed records did not stand with dropped.
func (c *Client) Directory(ctx context.Context, person string, known []Revocation) (d *Directory, dropped int, err error) {
	list, err := c.Devices(ctx, person)
	if err != nil {
		return nil, 0, err
	}
	d, dropped = NewDirectory(person, list.Records, list.Revocations, known)
	return d, dropped, nil
}

// RegisterDevice registers a record of the caller's in their directory:
// a new device vouched for by another of theirs, a new root, or the same
// record vouched for again. The service runs Directory.CheckRecord
// first.
func (c *Client) RegisterDevice(ctx context.Context, r DeviceRecord) error {
	return c.do(ctx, &latere.Request{Method: http.MethodPut, Path: basePath + "/devices/" + url.PathEscape(r.ID), Body: r}, false, nil)
}

// Revoke keeps a revocation of one of the caller's keys, signed by a
// record under the same root. The service deletes the revoked device's
// key packages and what was left for it, and proposes its removal from
// every conversation. A revocation sent twice is kept once.
func (c *Client) Revoke(ctx context.Context, v Revocation) error {
	return c.do(ctx, &latere.Request{Method: http.MethodPost, Path: basePath + "/revocations", Body: v, Retry: latere.RetryAlways}, false, nil)
}

// Recovery is a person's recovery record and their backup key wrapped
// by the recovery key.
type Recovery struct {
	Record  DeviceRecord `json:"record"`
	Wrapped []byte       `json:"wrapped"`
}

// PutRecovery registers the caller's recovery record and wrapped backup
// key, replacing the earlier ones.
func (c *Client) PutRecovery(ctx context.Context, r Recovery) error {
	return c.do(ctx, &latere.Request{Method: http.MethodPut, Path: basePath + "/recovery", Body: r}, false, nil)
}

// Recovery is the caller's recovery record and wrapped backup key. An
// error matching latere.ErrNotFound says they have none.
func (c *Client) Recovery(ctx context.Context) (*Recovery, error) {
	var out Recovery
	if err := c.do(ctx, &latere.Request{Method: http.MethodGet, Path: basePath + "/recovery"}, false, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type transfer struct {
	Sealed []byte `json:"sealed"`
}

// LeaveTransfer leaves the backup key sealed to a new device of the
// caller's, BackupKey.SealTo's answer, replacing what was left for it
// before.
func (c *Client) LeaveTransfer(ctx context.Context, device string, sealed []byte) error {
	return c.do(ctx, &latere.Request{Method: http.MethodPost, Path: basePath + "/devices/" + url.PathEscape(device) + "/transfer",
		Body: transfer{Sealed: sealed}, Retry: latere.RetryAlways}, false, nil)
}

// TakeTransfer is what was left for the Client's device, which the
// service then deletes; OpenBackupKey opens it. An error matching
// latere.ErrNotFound says nothing is left, and an answer lost on the way
// is lost with it, so the approving device leaves it again.
func (c *Client) TakeTransfer(ctx context.Context) ([]byte, error) {
	var out transfer
	if err := c.do(ctx, &latere.Request{Method: http.MethodGet, Path: basePath + "/devices/" + url.PathEscape(c.device) + "/transfer"}, true, &out); err != nil {
		return nil, err
	}
	return out.Sealed, nil
}

// PublishKeyPackages adds key packages of the Client's device, made with
// KeyPackageOptions, and replaces its last resort when lastResort is not
// nil. Each one-time key package is handed out once; the last resort is
// handed out when none is left, and kept.
func (c *Client) PublishKeyPackages(ctx context.Context, packages []*mls.KeyPackage, lastResort *mls.KeyPackage) error {
	body := struct {
		KeyPackages [][]byte `json:"key_packages"`
		LastResort  []byte   `json:"last_resort,omitempty"`
	}{KeyPackages: make([][]byte, 0, len(packages))}
	for _, kp := range packages {
		body.KeyPackages = append(body.KeyPackages, kp.Bytes())
	}
	if lastResort != nil {
		body.LastResort = lastResort.Bytes()
	}
	// An answer that says it failed added nothing, so it is sent again; a
	// failure on the network may have added them, and a key package added
	// twice would be handed out twice.
	return c.do(ctx, &latere.Request{Method: http.MethodPost, Path: basePath + "/key-packages", Body: body, Retry: latere.RetryAnswers}, true, nil)
}

// KeyPackageCount is how many one-time key packages a device has left,
// and whether it has a last resort.
type KeyPackageCount struct {
	Count      int  `json:"count"`
	LastResort bool `json:"last_resort"`
}

// KeyPackageCount is what the Client's device has left, so it publishes
// more before it runs out.
func (c *Client) KeyPackageCount(ctx context.Context) (*KeyPackageCount, error) {
	var out KeyPackageCount
	if err := c.do(ctx, &latere.Request{Method: http.MethodGet, Path: basePath + "/key-packages"}, true, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ClaimedKeyPackage is a key package handed out for one device of a
// person. CheckKeyPackage checks it before a commit adds it.
type ClaimedKeyPackage struct {
	Device     string `json:"device"`
	KeyPackage []byte `json:"key_package"`
}

// ClaimKeyPackages hands out a key package for each device of person
// that has one, to add them to conversation. The list may be shorter than
// the person's devices, or empty.
func (c *Client) ClaimKeyPackages(ctx context.Context, person, conversation string) ([]ClaimedKeyPackage, error) {
	var out struct {
		KeyPackages []ClaimedKeyPackage `json:"key_packages"`
	}
	body := struct {
		Person       string `json:"person"`
		Conversation string `json:"conversation"`
	}{person, conversation}
	// Each claim spends key packages, so only an answer that says it
	// failed is sent again.
	if err := c.do(ctx, &latere.Request{Method: http.MethodPost, Path: basePath + "/key-packages/claim", Body: body, Retry: latere.RetryAnswers}, false, &out); err != nil {
		return nil, err
	}
	return out.KeyPackages, nil
}

// Conversation is what the service holds of a conversation outside its
// group: its state as of the group's current epoch and the end of its
// log.
type Conversation struct {
	ID     string   `json:"id"`
	Kind   Kind     `json:"kind"`
	Level  Level    `json:"level"`
	People []string `json:"people"`
	Owners []string `json:"owners"`
	// Epoch is the group's current epoch.
	Epoch uint64 `json:"epoch"`
	// Seq is the sequence number of the log's last message, 0 before the
	// first.
	Seq     int64     `json:"seq"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

// CreateConversation creates a conversation from its creator's group
// info of epoch 0, with the ratchet tree and the external public key; the
// group's id is GroupID(id) and its one leaf the Client's device. A
// conversation of the id there already is refused with an error matching
// ErrExists, an earlier create of the Client's among them.
func (c *Client) CreateConversation(ctx context.Context, id string, groupInfo []byte) (*Conversation, error) {
	body := struct {
		ID        string `json:"id"`
		GroupInfo []byte `json:"group_info"`
	}{id, groupInfo}
	var out Conversation
	if err := c.do(ctx, &latere.Request{Method: http.MethodPost, Path: basePath + "/conversations", Body: body, Retry: latere.RetryAnswers}, true, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ConversationList is a page of the caller's conversations by id.
type ConversationList struct {
	Conversations []Conversation `json:"conversations"`
	// Next is the after of the next page, when this one is full; the next
	// page may be empty.
	Next string `json:"next,omitempty"`
}

// Conversations lists the caller's conversations by id after the id
// after, limit of them; a limit of 0 is the service's default page.
func (c *Client) Conversations(ctx context.Context, after string, limit int) (*ConversationList, error) {
	q := url.Values{}
	if after != "" {
		q.Set("after", after)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var out ConversationList
	if err := c.do(ctx, &latere.Request{Method: http.MethodGet, Path: basePath + "/conversations", Query: q}, false, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Conversation reads one conversation of the caller's.
func (c *Client) Conversation(ctx context.Context, id string) (*Conversation, error) {
	var out Conversation
	if err := c.do(ctx, &latere.Request{Method: http.MethodGet, Path: basePath + "/conversations/" + url.PathEscape(id)}, false, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GroupInfo is the group info of a conversation's current epoch, with
// the ratchet tree and the external public key, for the Client's device
// to join from with Policy.JoinExternal.
func (c *Client) GroupInfo(ctx context.Context, conversation string) ([]byte, error) {
	var out struct {
		GroupInfo []byte `json:"group_info"`
	}
	if err := c.do(ctx, &latere.Request{Method: http.MethodGet, Path: basePath + "/conversations/" + url.PathEscape(conversation) + "/group-info"}, true, &out); err != nil {
		return nil, err
	}
	return out.GroupInfo, nil
}

// Outgoing is an MLS message the Client's device sends to a
// conversation: an application message, a proposal, or a commit with the
// group info of the epoch it starts and, when it adds devices, the
// Welcome for them.
type Outgoing struct {
	Message   []byte `json:"message"`
	Welcome   []byte `json:"welcome,omitempty"`
	GroupInfo []byte `json:"group_info,omitempty"`
}

// MessageKind is what a message of the log is.
type MessageKind string

// The kinds of a message of the log.
const (
	MessageApplication MessageKind = "application"
	MessageProposal    MessageKind = "proposal"
	MessageCommit      MessageKind = "commit"
)

// Sent is where the service put a message in the log.
type Sent struct {
	Seq   int64       `json:"seq"`
	Epoch uint64      `json:"epoch"`
	Kind  MessageKind `json:"kind"`
}

// Send appends a message to a conversation's log, after the service
// checks it against the group's public state and the conversation's
// rules. A commit that another took the epoch before is refused with an
// error matching ErrEpochTaken, an application message of an ended epoch
// with one matching ErrWrongEpoch, and a change the rules refuse with one
// matching ErrRefused. Merge a commit only once Send takes it.
func (c *Client) Send(ctx context.Context, conversation string, m Outgoing) (*Sent, error) {
	var out Sent
	// An answer that says it failed appended nothing, so it is sent again;
	// a failure on the network may have appended it, and the log says.
	if err := c.do(ctx, &latere.Request{Method: http.MethodPost, Path: basePath + "/conversations/" + url.PathEscape(conversation) + "/messages",
		Body: m, Retry: latere.RetryAnswers}, true, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Message is one message of a conversation's log, as it was sent.
type Message struct {
	Seq     int64       `json:"seq"`
	Epoch   uint64      `json:"epoch"`
	Kind    MessageKind `json:"kind"`
	Message []byte      `json:"message"`
	// Person and Device sent it, as the service authenticated them; both
	// are empty on the service's own proposal. Which member sent an
	// application message is in its ciphertext, which the device checks.
	Person  string    `json:"person,omitempty"`
	Device  string    `json:"device,omitempty"`
	Created time.Time `json:"created"`
}

// MessageList is a page of a conversation's log.
type MessageList struct {
	Messages []Message `json:"messages"`
	// Next is the after to read from next time: the last sequence number
	// the page passed, messages of epochs the device may not read among
	// them, or the after asked for when there were none.
	Next int64 `json:"next"`
}

// Messages reads a conversation's log after the sequence number after,
// oldest first, limit of them, 0 for the service's default page. With
// wait above zero and nothing new, the service holds the read up to wait,
// at most MaxWait, for a message to arrive.
func (c *Client) Messages(ctx context.Context, conversation string, after int64, limit int, wait time.Duration) (*MessageList, error) {
	q := url.Values{}
	if after > 0 {
		q.Set("after", strconv.FormatInt(after, 10))
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if s := int64(min(wait, MaxWait) / time.Second); s > 0 {
		q.Set("wait", strconv.FormatInt(s, 10))
	}
	var out MessageList
	if err := c.do(ctx, &latere.Request{Method: http.MethodGet, Path: basePath + "/conversations/" + url.PathEscape(conversation) + "/messages", Query: q}, true, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Welcome is a Welcome filed for the Client's device by the commit that
// added it, with the commit's history notice.
type Welcome struct {
	ID           string `json:"id"`
	Conversation string `json:"conversation"`
	// Seq is the sequence number of the commit that added the device.
	Seq     int64  `json:"seq"`
	Welcome []byte `json:"welcome"`
	// Notice is the commit's authenticated data, ParseCommitNotice's
	// input; empty when it had none.
	Notice  []byte    `json:"notice,omitempty"`
	Created time.Time `json:"created"`
}

// Welcomes lists the Welcomes filed for the Client's device. Policy.JoinGroup
// joins from one, and DeleteWelcome drops it once joined.
func (c *Client) Welcomes(ctx context.Context) ([]Welcome, error) {
	var out struct {
		Welcomes []Welcome `json:"welcomes"`
	}
	if err := c.do(ctx, &latere.Request{Method: http.MethodGet, Path: basePath + "/welcomes"}, true, &out); err != nil {
		return nil, err
	}
	return out.Welcomes, nil
}

// DeleteWelcome drops a Welcome the Client's device has joined with.
func (c *Client) DeleteWelcome(ctx context.Context, id string) error {
	return c.do(ctx, &latere.Request{Method: http.MethodDelete, Path: basePath + "/welcomes/" + url.PathEscape(id)}, true, nil)
}

// IsMember reports whether person is one of a conversation's people now,
// for a storage service deciding a read of a file sent there.
func (c *Client) IsMember(ctx context.Context, conversation, person string) (bool, error) {
	var out struct {
		Person string `json:"person"`
		Member bool   `json:"member"`
	}
	if err := c.do(ctx, &latere.Request{Method: http.MethodGet,
		Path: basePath + "/conversations/" + url.PathEscape(conversation) + "/people/" + url.PathEscape(person)}, false, &out); err != nil {
		return false, err
	}
	return out.Member, nil
}
