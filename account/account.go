// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package account reads and changes what the Latere API knows about the
// person a request acts for, and that no single capability owns: the
// Effort levels a session can run at, the wallet turns are paid from and
// the payment pages that add to it, preferences with their sites, pins
// and bookmarks, the links that share a conversation, the person's
// computers, where the installation keeps sessions and the data terms of
// the models that ran them, what brought a person at sign-in, and the
// labels a create in Environments carries.
//
//	cfg, err := latere.NewConfig() // the key in LATERE_KEY
//	if err != nil {
//		return err
//	}
//	w, err := account.New(cfg).GetWallet(ctx)
//	if err != nil {
//		return err
//	}
//	if w.SpendableMicroUSD != nil {
//		fmt.Printf("%.2f USD left\n", float64(*w.SpendableMicroUSD)/1e6)
//	}
//
// Each request reads the context its token names: the person's own, or an
// organization's.
package account

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

// Client reaches the account part of the Latere API. It is safe for
// concurrent use.
type Client struct {
	cfg *latere.Config
}

// New returns a Client that sends its requests as cfg says. A cfg not
// built with latere.NewConfig makes every call fail with an error saying
// so.
func New(cfg *latere.Config) *Client { return &Client{cfg: cfg} }

// get reads path into out.
func (c *Client) get(ctx context.Context, path string, out any) error {
	return c.cfg.Do(ctx, &latere.Request{Method: http.MethodGet, Path: path}, out)
}

// ApplyManagedAgent makes sure the caller has their own copy of a managed
// agent Latere publishes, such as "latere-chat", at its live version, and
// returns the copy. Sessions with the agent are then created with
// agents.CreateSessionParams{Agent: id}. Calling it again changes nothing
// unless a newer version went live.
func (c *Client) ApplyManagedAgent(ctx context.Context, id string) (*AppliedAgent, error) {
	if id == "" {
		return nil, errors.New("account: the managed agent's id is empty")
	}
	var out AppliedAgent
	req := &latere.Request{Method: http.MethodPut, Path: "/v1/account/agents/" + url.PathEscape(id)}
	if err := c.cfg.Do(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AppliedAgent is the caller's copy of a managed agent.
type AppliedAgent struct {
	// ID is the managed agent's id, which is also the copy's name.
	ID string `json:"id"`
	// Version is the managed agent's version the copy carries.
	Version int `json:"version"`
	// Model is the routed name of the Effort level a new session starts at.
	Model string `json:"model"`
	// Agent is the copy among the caller's agents.
	Agent AgentCopy `json:"agent"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the applied agent, with
// every field, including those this package does not model; nil for an
// AppliedAgent the program built itself.
func (a *AppliedAgent) RawJSON() json.RawMessage { return a.raw }

// UnmarshalJSON decodes the applied agent and keeps the JSON for RawJSON.
func (a *AppliedAgent) UnmarshalJSON(b []byte) error {
	type plain AppliedAgent
	if err := json.Unmarshal(b, (*plain)(a)); err != nil {
		return err
	}
	a.raw = keep(b)
	return nil
}

// AgentCopy is the caller's copy of a managed agent, as an agent of the
// agents capability.
type AgentCopy struct {
	// ID is the copy's agent id.
	ID string `json:"id"`
	// Version is the copy's version, which a new session runs.
	Version int `json:"version"`
}

// GetEffortLevels returns the Effort levels a session can run at for the
// caller, what the next turn on each costs, and how Auto picks among them.
func (c *Client) GetEffortLevels(ctx context.Context) (*EffortLevels, error) {
	var out EffortLevels
	if err := c.get(ctx, "/v1/account/effort", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// EffortLevels are the levels of effort a session's model runs at.
type EffortLevels struct {
	// Levels are the levels, least effort first.
	Levels []EffortLevel `json:"levels"`
	// Auto picks a level for each message.
	Auto *EffortAuto `json:"auto,omitempty"`
	// ZeroRetention reports whether model requests go only to endpoints
	// that keep nothing of them, for the caller in this context.
	ZeroRetention bool `json:"zero_retention,omitempty"`
	// ZeroRetentionBy says who keeps ZeroRetention on: "organization" or
	// "person".
	ZeroRetentionBy string `json:"zero_retention_by,omitempty"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the Effort levels, with
// every field, including those this package does not model; nil for an
// EffortLevels the program built itself.
func (e *EffortLevels) RawJSON() json.RawMessage { return e.raw }

// UnmarshalJSON decodes the Effort levels and keeps the JSON for RawJSON.
func (e *EffortLevels) UnmarshalJSON(b []byte) error {
	type plain EffortLevels
	if err := json.Unmarshal(b, (*plain)(e)); err != nil {
		return err
	}
	e.raw = keep(b)
	return nil
}

// An EffortLevel is one level of effort.
type EffortLevel struct {
	// ID is "quick", "thorough" or "sustained".
	ID string `json:"id"`
	// Name is the routed model name for the level, "tier/<id>"; give it as
	// a session's model to run on the level.
	Name string `json:"name"`
	// Order is the level's place, least effort first, from 0.
	Order int `json:"order"`
	// Available reports whether a message on the level would run now.
	Available bool `json:"available"`
	// Reason is the refusal a message would get when the level is not
	// available, such as "budget_exhausted" or "rate_limited".
	Reason string `json:"reason,omitempty"`
	// ResetsAt is when a "rate_limited" level opens again.
	ResetsAt time.Time `json:"resets_at,omitzero"`
	// Retention is "none" when no request on the level can be kept by a
	// provider, and "provider" when one may be.
	Retention string `json:"retention,omitempty"`
	// Next is what the next turn on the level costs.
	Next *NextTurn `json:"next,omitempty"`
	// FreeTurns counts the caller's free turns on the level today.
	FreeTurns *FreeTurns `json:"free_turns,omitempty"`
	// Models are the models the level may run for the caller, in the
	// level's order of preference.
	Models []EffortModel `json:"models,omitempty"`
}

// EffortAuto describes Auto, which picks a level for each message.
type EffortAuto struct {
	// Name is the routed model name for Auto.
	Name string `json:"name"`
	// Available reports whether a message on Auto would run now.
	Available bool `json:"available"`
	// Reason is why Auto is not available, when it is not.
	Reason string `json:"reason,omitempty"`
	// Levels are the ids of the levels Auto picks among.
	Levels []string `json:"levels"`
	// Reads is "message" when Auto reads what a message asks, or "shape"
	// when it judges by length and attachments.
	Reads string `json:"reads"`
	// Asks lists the levels Auto asks the person about before running.
	Asks []string `json:"asks,omitempty"`
	// Retention is "none" or "provider", as for a level.
	Retention string `json:"retention"`
}

// An EffortModel is a model a level may run.
type EffortModel struct {
	// Name is the model's name in the catalog.
	Name string `json:"name"`
	// Charge is "none" for a free model or "credit" for a priced one.
	Charge string `json:"charge"`
	// Available reports whether a message with this model would run now.
	Available bool `json:"available"`
	// Reason is why it is not available, when it is not.
	Reason string `json:"reason,omitempty"`
	// FreeAgainAt is when a rate-limited free model opens again.
	FreeAgainAt time.Time `json:"free_again_at,omitzero"`
	// Retention is "none" or "provider".
	Retention string `json:"retention,omitempty"`
}

// NextTurn is what the next turn on a level costs.
type NextTurn struct {
	// Charge is "none" for a free turn or "credit" for one paid from the
	// wallet.
	Charge string `json:"charge"`
	// Price is the price of the model a "credit" turn runs.
	Price *Price `json:"price,omitempty"`
	// FreeAgainAt is when free turns open again, when they are what kept a
	// free model from running.
	FreeAgainAt time.Time `json:"free_again_at,omitzero"`
	// SponsoredOnly reports that the turn draws on credit Latere gave alone.
	SponsoredOnly bool `json:"sponsored_only,omitempty"`
}

// Price is a model's price per a number of tokens. Amounts are decimal
// strings, to keep their precision.
type Price struct {
	// Currency is the prices' currency, such as "USD".
	Currency string `json:"currency"`
	// Per is the number of tokens each price is for.
	Per int `json:"per"`
	// Input is the price of Per input tokens.
	Input string `json:"input"`
	// Output is the price of Per output tokens.
	Output string `json:"output"`
	// CachedInput is the price of Per cached input tokens, when stated.
	CachedInput string `json:"cachedInput,omitempty"`
	// CacheWrite is the price of writing Per tokens to the cache, when
	// stated.
	CacheWrite string `json:"cacheWrite,omitempty"`
}

// FreeTurns counts free turns on a level.
type FreeTurns struct {
	// Bound is how many free turns one person may start in a day.
	Bound int `json:"bound"`
	// Used is how many the caller started today.
	Used int `json:"used"`
	// ResetsAt is when the count resets.
	ResetsAt time.Time `json:"resets_at"`
}

// GetWallet returns the wallet the caller's turns are paid from in the
// token's context.
func (c *Client) GetWallet(ctx context.Context) (*Wallet, error) {
	var out Wallet
	if err := c.get(ctx, "/v1/account/wallet", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// WalletState is whether a wallet has credit.
type WalletState string

// The states of a wallet.
const (
	// WalletOpen is a wallet with credit.
	WalletOpen WalletState = "Open"
	// WalletExhausted is a wallet that spent its credit, or an
	// organization's that reached its monthly limit.
	WalletExhausted WalletState = "Exhausted"
)

// Wallet is the credit turns are paid from.
type Wallet struct {
	// State is WalletOpen or WalletExhausted; empty when the wallet cannot
	// be read now.
	State WalletState `json:"state"`
	// SpendableMicroUSD is the credit left, in millionths of a US dollar;
	// nil for a member of an organization, who sees no amount.
	SpendableMicroUSD *int64 `json:"spendable_micro,omitempty"`
	// Bought reports whether the wallet ever held bought credit; false for
	// a member of an organization.
	Bought bool `json:"bought,omitempty"`
	// NextGrant is the next free credit, when one is coming.
	NextGrant *NextGrant `json:"next_grant,omitempty"`
	// TopUp says what a payment adds, when the wallet can be topped up.
	TopUp *TopUp `json:"top_up,omitempty"`
	// LimitResetsAt is when an organization's monthly limit opens again,
	// for an organization that pays after it spends; zero otherwise.
	LimitResetsAt time.Time `json:"limit_resets_at,omitzero"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the wallet, with every
// field, including those this package does not model; nil for a Wallet the
// program built itself.
func (w *Wallet) RawJSON() json.RawMessage { return w.raw }

// UnmarshalJSON decodes the wallet and keeps the JSON for RawJSON.
func (w *Wallet) UnmarshalJSON(b []byte) error {
	type plain Wallet
	if err := json.Unmarshal(b, (*plain)(w)); err != nil {
		return err
	}
	w.raw = keep(b)
	return nil
}

// NextGrant is free credit on its way.
type NextGrant struct {
	// AmountMicroUSD is the credit, in millionths of a US dollar.
	AmountMicroUSD int64 `json:"amount_micro"`
	// At is when it lands; what is left of the current grant expires then.
	At time.Time `json:"at"`
}

// TopUp says what a payment adds to a wallet.
type TopUp struct {
	// MinMicroUSD is the smallest payment.
	MinMicroUSD int64 `json:"min_micro"`
	// SpreadBasisPoints is the share of a payment kept back.
	SpreadBasisPoints int `json:"spread_bps"`
	// SpreadFixedMicroUSD is the amount kept back from each payment.
	SpreadFixedMicroUSD int64 `json:"spread_fixed_micro"`
}

// GetPreferences returns the caller's preferences in the token's context.
func (c *Client) GetPreferences(ctx context.Context) (*Preferences, error) {
	var out Preferences
	if err := c.get(ctx, "/v1/account/preferences", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Preferences are a person's settings in one context.
type Preferences struct {
	// Network is which sites a session may reach.
	Network Network `json:"network"`
	// Sharing is whether conversations may be shared by link.
	Sharing Sharing `json:"sharing"`
	// Profile is what sessions are told about the person.
	Profile Profile `json:"profile"`
	// Instructions are the person's standing instructions to every session.
	Instructions Instructions `json:"instructions"`
	// Pins are the sessions, apps and projects the person pinned.
	Pins *Pins `json:"pins,omitempty"`
	// Bookmarks are the turns the person bookmarked.
	Bookmarks *Bookmarks `json:"bookmarks,omitempty"`
	// Data holds the person's own data retention setting.
	Data *DataPreferences `json:"data,omitempty"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the preferences, with every
// field, including those this package does not model; nil for a Preferences
// the program built itself.
func (p *Preferences) RawJSON() json.RawMessage { return p.raw }

// UnmarshalJSON decodes the preferences and keeps the JSON for RawJSON.
func (p *Preferences) UnmarshalJSON(b []byte) error {
	type plain Preferences
	if err := json.Unmarshal(b, (*plain)(p)); err != nil {
		return err
	}
	p.raw = keep(b)
	return nil
}

// Network is which sites a session may reach.
type Network struct {
	// Mode is "listed", "package_managers", "ask" or "open".
	Mode string `json:"mode"`
	// Domains are the sites added, lower-cased and sorted.
	Domains []string `json:"domains"`
	// ManagedBy is "you" or "organization".
	ManagedBy string `json:"managed_by"`
	// Editable reports whether the caller may change it.
	Editable bool `json:"editable"`
	// Modes are the modes the context allows, narrowest first.
	Modes []string `json:"modes"`
	// PackageManagers are the registries "package_managers" and "ask" reach.
	PackageManagers []string `json:"package_managers"`
	// MaxDomains is the most sites the context holds.
	MaxDomains int `json:"max_domains"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the network section, with
// every field, including those this package does not model; nil for a
// Network the program built itself.
func (n *Network) RawJSON() json.RawMessage { return n.raw }

// UnmarshalJSON decodes the network section and keeps the JSON for
// RawJSON.
func (n *Network) UnmarshalJSON(b []byte) error {
	type plain Network
	if err := json.Unmarshal(b, (*plain)(n)); err != nil {
		return err
	}
	n.raw = keep(b)
	return nil
}

// Sharing is whether conversations may be shared by link.
type Sharing struct {
	// Mode is "off", "members" or "anyone".
	Mode string `json:"mode"`
	// ManagedBy is "you" or "organization".
	ManagedBy string `json:"managed_by"`
	// Editable reports whether the caller may change it.
	Editable bool `json:"editable"`
	// Modes are the modes the context offers, narrowest first.
	Modes []string `json:"modes"`
}

// Profile is what sessions are told about the person.
type Profile struct {
	// CallMe is what the person asks to be called; empty when not set.
	CallMe string `json:"call_me"`
	// Work is one of Works; empty when not set.
	Work string `json:"work"`
	// Works are the kinds of work a person chooses from.
	Works []string `json:"works"`
	// MaxCallMe is the most characters CallMe holds.
	MaxCallMe int `json:"max_call_me"`
}

// Instructions are standing instructions to every session.
type Instructions struct {
	// Text is the instructions; empty when not set.
	Text string `json:"text"`
	// MaxChars is the most characters Text holds.
	MaxChars int `json:"max_chars"`
}

// Pins are the sessions, apps and projects a person pinned in one context.
type Pins struct {
	// Sessions are the pinned sessions' ids, in the order they were pinned.
	Sessions []string `json:"sessions"`
	// Apps are the pinned apps' ids.
	Apps []string `json:"apps"`
	// Projects are the pinned projects' ids.
	Projects []string `json:"projects"`
	// MaxSessions is the most sessions a context holds pinned.
	MaxSessions int `json:"max_sessions"`
	// MaxApps is the most apps a context holds pinned.
	MaxApps int `json:"max_apps"`
	// MaxProjects is the most projects a context holds pinned.
	MaxProjects int `json:"max_projects"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the pins, with every field,
// including those this package does not model; nil for a Pins the
// program built itself.
func (p *Pins) RawJSON() json.RawMessage { return p.raw }

// UnmarshalJSON decodes the pins and keeps the JSON for RawJSON.
func (p *Pins) UnmarshalJSON(b []byte) error {
	type plain Pins
	if err := json.Unmarshal(b, (*plain)(p)); err != nil {
		return err
	}
	p.raw = keep(b)
	return nil
}

// Bookmarks are the turns a person bookmarked.
type Bookmarks struct {
	// Sessions maps a session's id to the sequences of its bookmarked
	// turns, ascending.
	Sessions map[string][]int64 `json:"sessions"`
	// MaxPerSession is the most turns one session holds bookmarked.
	MaxPerSession int `json:"max_per_session"`
	// MaxSessions is the most sessions a context holds bookmarks in.
	MaxSessions int `json:"max_sessions"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the bookmarks, with every
// field, including those this package does not model; nil for a Bookmarks
// the program built itself.
func (b *Bookmarks) RawJSON() json.RawMessage { return b.raw }

// UnmarshalJSON decodes the bookmarks and keeps the JSON for RawJSON.
func (b *Bookmarks) UnmarshalJSON(data []byte) error {
	type plain Bookmarks
	if err := json.Unmarshal(data, (*plain)(b)); err != nil {
		return err
	}
	b.raw = keep(data)
	return nil
}

// DataPreferences hold a person's data retention setting.
type DataPreferences struct {
	// ZeroRetention reports whether the person's model requests go only to
	// endpoints that keep nothing of them.
	ZeroRetention bool `json:"zero_retention"`
	// ZeroRetentionBy is "organization" or "person" while it is on.
	ZeroRetentionBy string `json:"zero_retention_by,omitempty"`
	// ZeroRetentionRoutes reports whether models no such endpoint serves
	// stay closed rather than open.
	ZeroRetentionRoutes bool `json:"zero_retention_routes,omitempty"`
}

// PinSession pins a session in the token's context and returns the pins.
// Pinning a pinned session changes nothing.
func (c *Client) PinSession(ctx context.Context, sessionID string) (*Pins, error) {
	if sessionID == "" {
		return nil, errors.New("account: the session id is empty")
	}
	var out Pins
	req := &latere.Request{Method: http.MethodPut, Path: "/v1/account/preferences/pins/" + url.PathEscape(sessionID)}
	if err := c.cfg.Do(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UnpinSession unpins a session in the token's context. Unpinning a session
// that is not pinned succeeds.
func (c *Client) UnpinSession(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return errors.New("account: the session id is empty")
	}
	req := &latere.Request{Method: http.MethodDelete, Path: "/v1/account/preferences/pins/" + url.PathEscape(sessionID)}
	return c.cfg.Do(ctx, req, nil)
}

// ListDevices returns the person's computers connected to Latere, and how
// many they may hold. Only a person's own token may list them.
func (c *Client) ListDevices(ctx context.Context) (*Devices, error) {
	var out Devices
	if err := c.get(ctx, "/v1/account/devices", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Devices are a person's computers.
type Devices struct {
	// Items are the computers.
	Items []Device `json:"items"`
	// Limit is how many computers the person may hold.
	Limit int `json:"limit"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the list of computers, with every field,
// including those this package does not model; nil for a Devices the
// program built itself.
func (d *Devices) RawJSON() json.RawMessage { return d.raw }

// UnmarshalJSON decodes the list of computers and keeps the JSON for RawJSON.
func (d *Devices) UnmarshalJSON(b []byte) error {
	type plain Devices
	if err := json.Unmarshal(b, (*plain)(d)); err != nil {
		return err
	}
	d.raw = keep(b)
	return nil
}

// A Device is one of a person's computers, which sessions can work on.
type Device struct {
	// ID identifies the computer; a session names it as its machine's
	// environment.
	ID string `json:"id"`
	// Name is what the person calls it.
	Name string `json:"name"`
	// Status is whether the computer serves sessions now.
	Status DeviceStatus `json:"status"`
	// Online reports whether the computer serves sessions now.
	Online bool `json:"online"`
	// OS is the operating system it reports, such as "darwin"; empty until
	// it has connected.
	OS string `json:"os,omitempty"`
	// Arch is its architecture, such as "arm64".
	Arch string `json:"arch,omitempty"`
	// Isolation is how a session's commands are confined on it: "process",
	// the operating system's sandbox.
	Isolation string `json:"isolation"`
	// LastSeen is when it was last heard from; zero when it never was.
	LastSeen time.Time `json:"last_seen,omitzero"`
	// Folders are the folders a session may work in.
	Folders []DeviceFolder `json:"folders"`
	// Network is the network it allows a session.
	Network *DeviceNetwork `json:"network,omitempty"`
	// CreatedAt is when it was connected.
	CreatedAt time.Time `json:"created_at"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the device, with every
// field, including those this package does not model; nil for a Device the
// program built itself.
func (d *Device) RawJSON() json.RawMessage { return d.raw }

// UnmarshalJSON decodes the device and keeps the JSON for RawJSON.
func (d *Device) UnmarshalJSON(b []byte) error {
	type plain Device
	if err := json.Unmarshal(b, (*plain)(d)); err != nil {
		return err
	}
	d.raw = keep(b)
	return nil
}

// DeviceStatus is whether a computer serves sessions.
type DeviceStatus string

// The statuses of a Device.
const (
	// DeviceOnline is a computer that serves sessions now.
	DeviceOnline DeviceStatus = "online"
	// DeviceOffline is a computer that does not.
	DeviceOffline DeviceStatus = "offline"
	// DeviceUnknown is a computer whose state could not be read.
	DeviceUnknown DeviceStatus = "unknown"
)

// A DeviceFolder is a folder a computer lets sessions work in.
type DeviceFolder struct {
	// Path is the folder's path on the computer.
	Path string `json:"path"`
	// Write reports whether sessions may change files in it.
	Write bool `json:"write"`
}

// DeviceNetwork is the network a computer allows a session.
type DeviceNetwork struct {
	// Mode is "none", "allowlist" or "open": no network, the hosts Hosts
	// names, or any public host but those Blocked names. Under "open",
	// anything a session can read in the computer's folders can be sent
	// to any public host.
	Mode string `json:"mode"`
	// Hosts are the hosts an "allowlist" names, or under "open" the ports
	// beyond 443 and 80 it admits, each as "host:port".
	Hosts []string `json:"hosts"`
	// Blocked are the hosts a computer on "open" blocks: a host, an
	// address, or "*.example.com" for the names below it, alone or as
	// "host:port". Empty under the other modes.
	Blocked []string `json:"blocked"`
}

// MaxDeviceName is the most characters a computer's name holds, once
// trimmed.
const MaxDeviceName = 64

// errNoDevice is returned before any request when a device id is empty.
var errNoDevice = errors.New("account: the device id is empty")

// devicePath is the path of one of the person's computers.
func devicePath(id string) string { return "/v1/account/devices/" + url.PathEscape(id) }

// RenameDevice gives one of the person's computers a new name and returns
// the computer. The API trims the name and refuses one that is empty, is
// longer than MaxDeviceName characters, or holds a character that does
// not print, with 400 and the code "invalid_name". An id the person holds
// no computer by returns an error matching latere.ErrNotFound. Only a
// person's own token may rename one; a key's is refused with 403.
func (c *Client) RenameDevice(ctx context.Context, id, name string) (*Device, error) {
	if id == "" {
		return nil, errNoDevice
	}
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("account: RenameDevice: the name is empty")
	}
	var out Device
	// A rename sets a value, so sending it again has the effect of sending
	// it once, and it is retried.
	req := &latere.Request{Method: http.MethodPatch, Path: devicePath(id), Body: map[string]string{"name": name}, Retry: latere.RetryAlways}
	if err := c.cfg.Do(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RemoveDevice removes one of the person's computers for good: its keys
// are revoked, its connection is closed, and the work of sessions on it
// ends. Connecting the computer again makes a new device. Only a person's
// own token may remove one; a key's is refused with 403.
//
// Removing a computer the person no longer holds succeeds, so a removal
// is safe to repeat: the API answers 404 for an id it holds no computer
// by, the same for one removed before and one that was never the
// person's, and a removal the SDK sends again after a lost answer meets
// that 404 although the first one removed the computer. A mistyped id
// therefore returns nil too; ListDevices tells which computers remain.
func (c *Client) RemoveDevice(ctx context.Context, id string) error {
	if id == "" {
		return errNoDevice
	}
	err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodDelete, Path: devicePath(id)}, nil)
	var apiErr *latere.Error
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound && apiErr.Code == "not_found" {
		// The API's own answer that it holds no such computer; a 404
		// without its envelope, such as a proxy's, is still an error.
		return nil
	}
	return err
}

// The label that marks an environment in Environments as one of a
// person's computers. A program that connects a computer, by creating its
// environment, writes LabelKind with the value KindDevice beside the
// tenant labels GetEnvironmentsContext answers; the platform admits that
// one pair on an environment and on nothing else.
const (
	// LabelKind is the key of the label.
	LabelKind = "platform.latere.ai/kind"
	// KindDevice is its value for a person's computer.
	KindDevice = "device"
)

// GetEnvironmentsContext returns the labels that what a program creates in
// Environments, such as a sandbox, a Secret or a computer's environment,
// must carry in the token's context, and the environment a manifest that
// names none runs in. Environments
// refuses a create without these labels, or with another account's, with
// 403. They change with the context and with nothing else, so one read
// serves every create in a context. Any token of the API reads them, a
// key's included. A deployment without Environments answers 503 with the
// code "environments_unconfigured".
func (c *Client) GetEnvironmentsContext(ctx context.Context) (*EnvironmentsContext, error) {
	var out EnvironmentsContext
	if err := c.get(ctx, "/v1/account/environments/context", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// EnvironmentsContext is what a create in Environments carries in one
// context.
type EnvironmentsContext struct {
	// Tenant is the account a create lands in.
	Tenant Tenant `json:"tenant"`
	// DefaultEnvironment is the environment a manifest that names none
	// runs in; empty where the deployment names none.
	DefaultEnvironment string `json:"default_environment"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the context, with every
// field, including those this package does not model; nil for an
// EnvironmentsContext the program built itself.
func (e *EnvironmentsContext) RawJSON() json.RawMessage { return e.raw }

// UnmarshalJSON decodes the context and keeps the JSON for RawJSON.
func (e *EnvironmentsContext) UnmarshalJSON(b []byte) error {
	type plain EnvironmentsContext
	if err := json.Unmarshal(b, (*plain)(e)); err != nil {
		return err
	}
	e.raw = keep(b)
	return nil
}

// A Tenant is the account a create in Environments lands in.
type Tenant struct {
	// ID names the account: "p-" and 32 hexadecimal digits for a
	// person's own, "o-" and 32 for an organization's.
	ID string `json:"id"`
	// Owner is who owns what is created: the issuer and the token's
	// subject.
	Owner string `json:"owner"`
	// OrgID is the context's organization; empty in a person's own.
	OrgID string `json:"org_id"`
	// Labels are the labels to write, as they are, into the
	// metadata.labels of every manifest created, beside labels of one's
	// own.
	Labels map[string]string `json:"labels"`
}
