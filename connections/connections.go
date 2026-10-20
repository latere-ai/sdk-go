// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package connections works with the services a person connects to
// Latere once, such as Google Calendar or an MCP server of their own, so
// that Latere's agents can use them without ever holding the credential.
//
// A connector is a service a person may connect: one of Latere's own, or
// an MCP server someone added. A connection is one person's connection to
// a connector. Connecting a service that signs people in answers a page to
// send the person's browser to; after they sign in, the browser returns to
// the page the program named, with a one-time code that
// Client.ConfirmConnection exchanges to finish the connection.
//
//	cfg, err := latere.NewConfig() // the key in LATERE_KEY
//	if err != nil {
//		return err
//	}
//	cs := connections.New(cfg)
//	r, err := cs.CreateConnection(ctx, &connections.CreateConnectionParams{
//		Connector: "google-calendar",
//		ReturnTo:  "https://chat.latere.ai/connected",
//	})
//	if err != nil {
//		return err
//	}
//	if r.AuthorizationURL != "" {
//		fmt.Println("Sign in at", r.AuthorizationURL)
//	}
//
// A credential is never part of an answer: no method returns one, and an
// error carries none. Each request acts in the context its token names: a
// person's own connections, or what they connected in an organization.
package connections

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

// basePath is where the connections capability's routes live.
const basePath = "/v1/connections"

// Client reaches the connections part of the Latere API. It is safe for
// concurrent use.
type Client struct {
	cfg *latere.Config
}

// New returns a Client that sends its requests as cfg says. A cfg not
// built with latere.NewConfig makes every call fail with an error saying
// so.
func New(cfg *latere.Config) *Client { return &Client{cfg: cfg} }

// A Connection is one person's connection to a connector.
type Connection struct {
	// ID is the connection's id.
	ID string `json:"id"`
	// Name is unique among the owner's connections, and the name an
	// agent's session knows the connection by.
	Name string `json:"name"`
	// Owner is whose connection it is: a person, or, for one made in an
	// organization, the organization.
	Owner string `json:"owner"`
	// Connector is the name of one of Latere's services, or the id of an
	// added server.
	Connector string `json:"connector"`
	// Kind is how an agent reaches the service.
	Kind Kind `json:"kind"`
	// Auth is how the person gave access.
	Auth AuthType `json:"auth"`
	// State is whether the connection can be used.
	State State `json:"state"`
	// Reason says why a connection needs reconnecting, such as
	// "refresh_refused", "revoked" or "credential_refused".
	Reason string `json:"reason,omitempty"`
	// Account is the account at the service the connection acts as, such
	// as a Google address.
	Account string `json:"account,omitempty"`
	// Scopes are the scopes the service granted.
	Scopes []string `json:"scopes,omitempty"`
	// Address is where an agent's session reaches the connection.
	Address string `json:"address"`
	// GitAddress is the git address, for a service with a git host.
	GitAddress string `json:"git_address,omitempty"`
	// Tools marks which of the service's tools are read-only or hidden.
	Tools ToolMarks `json:"tools"`
	// Links are pages about the connection at the service.
	Links ConnectionLinks `json:"links"`
	// CreatedBy is who made the connection.
	CreatedBy string `json:"created_by"`
	// CreatedAt is when it was made.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is when it last changed.
	UpdatedAt time.Time `json:"updated_at"`
	// ExpiresAt is when its sign-in can no longer be renewed; nil when it
	// does not expire.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	// LastUsedAt is when a session last made a request through it; nil
	// before the first.
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the connection, with every
// field, including those this package does not model; nil for a Connection
// the program built itself.
func (c *Connection) RawJSON() json.RawMessage { return c.raw }

// UnmarshalJSON decodes the connection and keeps the JSON for RawJSON.
func (c *Connection) UnmarshalJSON(b []byte) error {
	type plain Connection
	if err := json.Unmarshal(b, (*plain)(c)); err != nil {
		return err
	}
	c.raw = keep(b)
	return nil
}

// ConnectionLinks are pages about a connection at its service.
type ConnectionLinks struct {
	// Manage is where the person changes what the service grants, such as
	// which repositories on GitHub; empty when the service has no such
	// page.
	Manage string `json:"manage,omitempty"`
}

// ToolMarks name tools of a service by their names.
type ToolMarks struct {
	// ReadOnly are tools an agent may call without asking, because they
	// change nothing.
	ReadOnly []string `json:"read_only"`
	// Hidden are tools an agent never sees.
	Hidden []string `json:"hidden"`
}

// Kind is how an agent reaches a service.
type Kind string

// The kinds of service.
const (
	// KindMCP is an MCP server.
	KindMCP Kind = "mcp"
	// KindHTTP is an HTTP API, reached through a forwarding address, and a
	// git host when the service has one.
	KindHTTP Kind = "http"
)

// AuthType is how a person gives a service access.
type AuthType string

// The ways of giving access.
const (
	// AuthOAuth is by signing in at the service.
	AuthOAuth AuthType = "oauth"
	// AuthToken is by giving a key.
	AuthToken AuthType = "token"
	// AuthClientCredentials is with a client the service registered.
	AuthClientCredentials AuthType = "client_credentials"
	// AuthNone needs nothing.
	AuthNone AuthType = "none"
)

// State is whether a connection can be used.
type State string

// The states of a connection.
const (
	// StatePending is a connection whose sign-in has not finished.
	StatePending State = "pending"
	// StateConnected is a connection agents can use.
	StateConnected State = "connected"
	// StateNeedsReconnect is a connection whose access ended; see
	// Connection.Reason, and reconnect it with Client.ReconnectConnection.
	StateNeedsReconnect State = "needs_reconnect"
)

// A Connector is a service a person may connect.
type Connector struct {
	// Name is the name of one of Latere's services, such as
	// "google-calendar", or an added server's name.
	Name string `json:"name"`
	// ID is an added server's id; empty for Latere's own services.
	ID string `json:"id,omitempty"`
	// Title is what a person reads.
	Title string `json:"title"`
	// Description says what the service is.
	Description string `json:"description,omitempty"`
	// Icon is the address of the service's icon.
	Icon string `json:"icon,omitempty"`
	// Kind is how an agent reaches the service.
	Kind Kind `json:"kind"`
	// URL is where the service answers.
	URL string `json:"url"`
	// Hosts are the only hosts the credential is ever sent to.
	Hosts []string `json:"hosts"`
	// Auth is how a person gives access.
	Auth ConnectorAuth `json:"auth"`
	// Tools marks which tools are read-only or hidden.
	Tools ToolMarks `json:"tools"`
	// Owner is whose an added server is: a person's, an organization's,
	// or Latere's.
	Owner string `json:"owner,omitempty"`
	// Vouched is true for an added server an organization's owner or
	// admin, or Latere, added.
	Vouched bool `json:"vouched,omitempty"`
	// Disabled is true for an added server that was turned off.
	Disabled bool `json:"disabled,omitempty"`
	// Connections is how many connections use an added server, answered
	// to a caller who may change it.
	Connections int `json:"connections,omitempty"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the connector, with every
// field, including those this package does not model; nil for a Connector
// the program built itself.
func (c *Connector) RawJSON() json.RawMessage { return c.raw }

// UnmarshalJSON decodes the connector and keeps the JSON for RawJSON.
func (c *Connector) UnmarshalJSON(b []byte) error {
	type plain Connector
	if err := json.Unmarshal(b, (*plain)(c)); err != nil {
		return err
	}
	c.raw = keep(b)
	return nil
}

// ConnectorAuth is how a person gives a service access.
type ConnectorAuth struct {
	// Type is the way.
	Type AuthType `json:"type"`
	// Scopes are what a sign-in asks the service for.
	Scopes []string `json:"scopes,omitempty"`
	// Hint says where a person gets a key, for a service that takes one.
	Hint string `json:"hint,omitempty"`
	// Inject is where a key goes, for a service that takes it in a header
	// of its own.
	Inject *Inject `json:"inject,omitempty"`
	// Client is how Latere identifies itself where an added server signs
	// people in.
	Client ClientKind `json:"client,omitempty"`
	// RedirectURI is the return address to register a server's own sign-in
	// client with, present when Client is ClientOwn.
	RedirectURI string `json:"redirect_uri,omitempty"`
}

// Inject says where a key goes in a request to a service.
type Inject struct {
	// Header is the request header the key goes in, such as "X-API-Key".
	Header string `json:"header"`
	// Format is the header's value, with {value} where the key goes, such
	// as "Bearer {value}".
	Format string `json:"format"`
}

// ClientKind is how Latere identifies itself where an added server signs
// people in.
type ClientKind string

// The kinds of sign-in client.
const (
	// ClientOwn is a client the server's developer registered there and an
	// organization's owner or admin gave.
	ClientOwn ClientKind = "own"
	// ClientDocument is Latere's client metadata document.
	ClientDocument ClientKind = "document"
	// ClientRegistered is a registration Latere made at the server itself.
	ClientRegistered ClientKind = "registered"
)

// ListConnectionsParams narrows and pages a list of connections. The zero
// value lists all of the caller's.
type ListConnectionsParams struct {
	// Owner lists the connections of an owner the caller may read, such as
	// an organization's.
	Owner string
	// Connector keeps the connections to one connector.
	Connector string
	// State keeps the connections in one state.
	State State
	// Limit is the most connections a page holds; the API's default when 0.
	Limit int
	// Cursor starts ListConnectionsPage at the page a previous one's
	// NextCursor named; ListConnections ignores it.
	Cursor string
}

// ListConnections returns the connections params keeps, fetching pages as
// the loop asks for more.
func (c *Client) ListConnections(ctx context.Context, params *ListConnectionsParams) iter.Seq2[*Connection, error] {
	return latere.Items(ctx, func(ctx context.Context, cursor string) (*latere.Page[*Connection], error) {
		return c.connectionsPage(ctx, params, cursor)
	})
}

// ListConnectionsPage returns one page of the connections params keeps,
// starting at params.Cursor, and the cursor of the next page in
// NextCursor.
func (c *Client) ListConnectionsPage(ctx context.Context, params *ListConnectionsParams) (*latere.Page[*Connection], error) {
	cursor := ""
	if params != nil {
		cursor = params.Cursor
	}
	return c.connectionsPage(ctx, params, cursor)
}

func (c *Client) connectionsPage(ctx context.Context, params *ListConnectionsParams, cursor string) (*latere.Page[*Connection], error) {
	q := url.Values{}
	if params != nil {
		setIf(q, "owner", params.Owner)
		setIf(q, "connector", params.Connector)
		setIf(q, "state", string(params.State))
		setLimit(q, params.Limit)
	}
	setIf(q, "cursor", cursor)
	var page latere.Page[*Connection]
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodGet, Path: basePath + "/connections", Query: q}, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// errNoConnection is returned before any request when a connection's id
// is empty.
var errNoConnection = errors.New("connections: the connection's id is empty")

// connectionPath is the path of a connection, or of one of its
// sub-resources.
func connectionPath(id, rest string) string {
	return basePath + "/connections/" + url.PathEscape(id) + rest
}

// GetConnection returns the connection id names. It returns an error
// matching latere.ErrNotFound when there is no such connection the caller
// may read, including one somebody else holds.
func (c *Client) GetConnection(ctx context.Context, id string) (*Connection, error) {
	if id == "" {
		return nil, errNoConnection
	}
	var out Connection
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodGet, Path: connectionPath(id, "")}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateConnectionParams describes a connection to make.
type CreateConnectionParams struct {
	// Connector is the name of one of Latere's services, such as
	// "google-calendar", or the id of an added server. Required.
	Connector string
	// Name is unique among the owner's connections; the connector's name
	// when empty.
	Name string
	// Credential is the key, for a connector that takes one; nil for one
	// that signs people in.
	Credential *Credential
	// ReturnTo is where the browser goes after a sign-in: one of Latere's
	// own pages. Required for a connector that signs people in.
	ReturnTo string
	// Scopes are what to ask for at a sign-in, beside the connector's own.
	Scopes []string
	// IdempotencyKey makes a repeated call answer the first one's result
	// instead of making a second connection; one is generated when it is
	// empty.
	IdempotencyKey string
}

// A Credential is a key a person gives a service. It is sent once and
// never answered.
type Credential struct {
	// Value is the key.
	Value string `json:"value"`
}

// A Connected is what making or reconnecting a connection answered.
type Connected struct {
	// Connection is the connection: connected, for a key or a service
	// that needs nothing, or pending a sign-in.
	Connection *Connection
	// AuthorizationURL is where to send the person's browser to sign in;
	// empty when the connection is already connected. After the sign-in,
	// the browser returns to ReturnTo with a code for
	// Client.ConfirmConnection.
	AuthorizationURL string
}

// started is the API's answer when a connection waits for a sign-in.
type started struct {
	Connection       *Connection `json:"connection"`
	AuthorizationURL string      `json:"authorization_url"`
}

// decodeConnected reads the answer of a connect or a reconnect, which is
// either the connection or a sign-in to finish.
func decodeConnected(raw json.RawMessage) (*Connected, error) {
	var s started
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	if s.AuthorizationURL != "" {
		return &Connected{Connection: s.Connection, AuthorizationURL: s.AuthorizationURL}, nil
	}
	var conn Connection
	if err := json.Unmarshal(raw, &conn); err != nil {
		return nil, err
	}
	return &Connected{Connection: &conn}, nil
}

// CreateConnection connects a service. A connector that takes a key or
// nothing answers the connection at once; one that signs people in answers
// a pending connection and the page to send the person to.
func (c *Client) CreateConnection(ctx context.Context, params *CreateConnectionParams) (*Connected, error) {
	if params == nil || params.Connector == "" {
		return nil, errors.New("connections: CreateConnectionParams.Connector is required")
	}
	body := map[string]any{"connector": params.Connector}
	if params.Name != "" {
		body["name"] = params.Name
	}
	if params.Credential != nil {
		body["credential"] = params.Credential
	}
	if params.ReturnTo != "" {
		body["return_to"] = params.ReturnTo
	}
	if len(params.Scopes) > 0 {
		body["scopes"] = params.Scopes
	}
	key := params.IdempotencyKey
	if key == "" {
		key = latere.NewIdempotencyKey()
	}
	var raw json.RawMessage
	req := &latere.Request{Method: http.MethodPost, Path: basePath + "/connections", Body: body, IdempotencyKey: key}
	if err := c.cfg.Do(ctx, req, &raw); err != nil {
		return nil, err
	}
	out, err := decodeConnected(raw)
	if err != nil {
		return nil, errors.Join(errors.New("connections: decoding the answer to a connect"), err)
	}
	return out, nil
}

// RenameConnection renames the connection id names and returns it. The
// name must be unique among the owner's connections; a taken one returns
// an error matching latere.ErrConflict.
func (c *Client) RenameConnection(ctx context.Context, id, name string) (*Connection, error) {
	if id == "" {
		return nil, errNoConnection
	}
	var out Connection
	req := &latere.Request{Method: http.MethodPatch, Path: connectionPath(id, ""), Body: map[string]string{"name": name}}
	if err := c.cfg.Do(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteConnection disconnects the connection id names; Latere forgets
// its credential.
func (c *Client) DeleteConnection(ctx context.Context, id string) error {
	if id == "" {
		return errNoConnection
	}
	return c.cfg.Do(ctx, &latere.Request{Method: http.MethodDelete, Path: connectionPath(id, "")}, nil)
}

// ConfirmConnection finishes the sign-in of the connection id names with
// the one-time code the browser brought back to ReturnTo, and returns the
// connection. A code used before, or a sign-in that took too long,
// returns an error with status 409 or 410.
func (c *Client) ConfirmConnection(ctx context.Context, id, code string) (*Connection, error) {
	if id == "" {
		return nil, errNoConnection
	}
	var out Connection
	req := &latere.Request{Method: http.MethodPost, Path: connectionPath(id, "/confirm"), Body: map[string]string{"code": code}}
	if err := c.cfg.Do(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ReconnectConnectionParams describes a new sign-in for a connection.
type ReconnectConnectionParams struct {
	// ReturnTo is where the browser goes after the sign-in: one of
	// Latere's own pages. Required.
	ReturnTo string
	// Scopes are what to ask for beside the scopes granted before.
	Scopes []string
}

// ReconnectConnection starts a new sign-in for the connection id names,
// for one that needs reconnecting or to ask for more scopes. It returns
// the page to send the person to; finish it with Client.ConfirmConnection.
func (c *Client) ReconnectConnection(ctx context.Context, id string, params *ReconnectConnectionParams) (*Connected, error) {
	if id == "" {
		return nil, errNoConnection
	}
	if params == nil || params.ReturnTo == "" {
		return nil, errors.New("connections: ReconnectConnectionParams.ReturnTo is required")
	}
	body := map[string]any{"return_to": params.ReturnTo}
	if len(params.Scopes) > 0 {
		body["scopes"] = params.Scopes
	}
	var s started
	req := &latere.Request{Method: http.MethodPost, Path: connectionPath(id, "/reconnect"), Body: body}
	if err := c.cfg.Do(ctx, req, &s); err != nil {
		return nil, err
	}
	return &Connected{Connection: s.Connection, AuthorizationURL: s.AuthorizationURL}, nil
}

// ReplaceCredential replaces the key of the connection id names, for a
// connector that takes one.
func (c *Client) ReplaceCredential(ctx context.Context, id string, credential Credential) error {
	if id == "" {
		return errNoConnection
	}
	req := &latere.Request{Method: http.MethodPut, Path: connectionPath(id, "/credential"), Body: map[string]any{"credential": credential}}
	return c.cfg.Do(ctx, req, nil)
}

// A Tool is one of a connected service's tools.
type Tool struct {
	// Name is the tool's name.
	Name string `json:"name"`
	// Description says what the tool does.
	Description string `json:"description,omitempty"`
	// Effect is whether the tool changes anything, from the service's
	// marks.
	Effect Effect `json:"effect"`
	// Hidden is true when a hidden mark names the tool.
	Hidden bool `json:"hidden,omitempty"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the tool, with every field,
// including those this package does not model; nil for a Tool the program
// built itself.
func (t *Tool) RawJSON() json.RawMessage { return t.raw }

// UnmarshalJSON decodes the tool and keeps the JSON for RawJSON.
func (t *Tool) UnmarshalJSON(b []byte) error {
	type plain Tool
	if err := json.Unmarshal(b, (*plain)(t)); err != nil {
		return err
	}
	t.raw = keep(b)
	return nil
}

// Effect is whether a tool or a request changes anything at a service.
type Effect string

// The effects.
const (
	// EffectRead changes nothing.
	EffectRead Effect = "read"
	// EffectWrite may change something.
	EffectWrite Effect = "write"
)

// ListTools returns the tools of the MCP server the connection id names
// connects, as the server lists them now. It returns an error with status
// 409 for a connection that is not connected, and 502 or 504 when the
// server did not answer.
func (c *Client) ListTools(ctx context.Context, id string) ([]Tool, error) {
	if id == "" {
		return nil, errNoConnection
	}
	var out struct {
		Items []Tool `json:"items"`
	}
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodGet, Path: connectionPath(id, "/tools")}, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// A Call is one request a session made through a connection.
type Call struct {
	// ID is the call's id.
	ID string `json:"id"`
	// Connection is the connection it went through.
	Connection string `json:"connection"`
	// Owner is the connection's owner.
	Owner string `json:"owner"`
	// Connector is the connection's connector.
	Connector string `json:"connector"`
	// Subject is who made the request.
	Subject string `json:"subject"`
	// Session is the agent's session that made it.
	Session string `json:"session,omitempty"`
	// At is when it was made.
	At time.Time `json:"at"`
	// DurationMS is how long it took, in milliseconds.
	DurationMS int64 `json:"duration_ms"`
	// Method is the request's method.
	Method string `json:"method"`
	// Tool is the path at the service, the git service, or the MCP tool
	// called.
	Tool string `json:"tool,omitempty"`
	// Effect is whether the request could change anything.
	Effect Effect `json:"effect,omitempty"`
	// Outcome is what came of it.
	Outcome Outcome `json:"outcome"`
	// Status is the service's HTTP status; 0 when the request never
	// reached it.
	Status int `json:"status,omitempty"`
	// BytesIn is how many bytes the request sent to the service.
	BytesIn int64 `json:"bytes_in"`
	// BytesOut is how many bytes the service answered.
	BytesOut int64 `json:"bytes_out"`
	// Refs is how many branches and tags a git push named.
	Refs int `json:"refs,omitempty"`

	raw json.RawMessage
}

// RawJSON returns the JSON the API answered for the call, with every field,
// including those this package does not model; nil for a Call the program
// built itself.
func (c *Call) RawJSON() json.RawMessage { return c.raw }

// UnmarshalJSON decodes the call and keeps the JSON for RawJSON.
func (c *Call) UnmarshalJSON(b []byte) error {
	type plain Call
	if err := json.Unmarshal(b, (*plain)(c)); err != nil {
		return err
	}
	c.raw = keep(b)
	return nil
}

// Outcome is what came of a request through a connection.
type Outcome string

// The outcomes of a request.
const (
	OutcomeOK              Outcome = "ok"
	OutcomeDenied          Outcome = "denied"
	OutcomeNotConnected    Outcome = "not_connected"
	OutcomeUpstreamError   Outcome = "upstream_error"
	OutcomeUpstreamRefused Outcome = "upstream_refused"
	OutcomeTimeout         Outcome = "timeout"
	OutcomeCanceled        Outcome = "canceled"
)

// ListCallsParams pages a connection's calls.
type ListCallsParams struct {
	// Limit is the most calls a page holds; the API's default when 0.
	Limit int
	// Cursor starts ListCallsPage at the page a previous one's NextCursor
	// named; ListCalls ignores it.
	Cursor string
}

// ListCalls returns the requests sessions made through the connection id
// names, newest first, fetching pages as the loop asks for more.
func (c *Client) ListCalls(ctx context.Context, id string, params *ListCallsParams) iter.Seq2[*Call, error] {
	return latere.Items(ctx, func(ctx context.Context, cursor string) (*latere.Page[*Call], error) {
		return c.callsPage(ctx, id, params, cursor)
	})
}

// ListCallsPage returns one page of the calls of the connection id names,
// starting at params.Cursor, and the cursor of the next page in
// NextCursor.
func (c *Client) ListCallsPage(ctx context.Context, id string, params *ListCallsParams) (*latere.Page[*Call], error) {
	cursor := ""
	if params != nil {
		cursor = params.Cursor
	}
	return c.callsPage(ctx, id, params, cursor)
}

func (c *Client) callsPage(ctx context.Context, id string, params *ListCallsParams, cursor string) (*latere.Page[*Call], error) {
	if id == "" {
		return nil, errNoConnection
	}
	q := url.Values{}
	if params != nil {
		setLimit(q, params.Limit)
	}
	setIf(q, "cursor", cursor)
	var page latere.Page[*Call]
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodGet, Path: connectionPath(id, "/calls"), Query: q}, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// ListConnectorsParams narrows and pages a list of connectors. The zero
// value lists every connector the caller may connect.
type ListConnectorsParams struct {
	// Owner keeps the servers one owner added.
	Owner string
	// Kind keeps the connectors of one kind.
	Kind Kind
	// Limit is the most connectors a page holds; the API's default when 0.
	Limit int
	// Cursor starts ListConnectorsPage at the page a previous one's
	// NextCursor named; ListConnectors ignores it.
	Cursor string
}

// ListConnectors returns the connectors params keeps: Latere's services
// and the servers the caller may use, fetching pages as the loop asks for
// more.
func (c *Client) ListConnectors(ctx context.Context, params *ListConnectorsParams) iter.Seq2[*Connector, error] {
	return latere.Items(ctx, func(ctx context.Context, cursor string) (*latere.Page[*Connector], error) {
		return c.connectorsPage(ctx, params, cursor)
	})
}

// ListConnectorsPage returns one page of the connectors params keeps,
// starting at params.Cursor, and the cursor of the next page in
// NextCursor.
func (c *Client) ListConnectorsPage(ctx context.Context, params *ListConnectorsParams) (*latere.Page[*Connector], error) {
	cursor := ""
	if params != nil {
		cursor = params.Cursor
	}
	return c.connectorsPage(ctx, params, cursor)
}

func (c *Client) connectorsPage(ctx context.Context, params *ListConnectorsParams, cursor string) (*latere.Page[*Connector], error) {
	q := url.Values{}
	if params != nil {
		setIf(q, "owner", params.Owner)
		setIf(q, "kind", string(params.Kind))
		setLimit(q, params.Limit)
	}
	setIf(q, "cursor", cursor)
	var page latere.Page[*Connector]
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodGet, Path: basePath + "/connectors", Query: q}, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// errNoConnector is returned before any request when a connector's name
// or id is empty.
var errNoConnector = errors.New("connections: the connector's name or id is empty")

// connectorPath is the path of a connector, by its name or id.
func connectorPath(ref string) string {
	return basePath + "/connectors/" + url.PathEscape(ref)
}

// GetConnector returns the connector ref names, by its name or, for an
// added server, its id.
func (c *Client) GetConnector(ctx context.Context, ref string) (*Connector, error) {
	if ref == "" {
		return nil, errNoConnector
	}
	var out Connector
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodGet, Path: connectorPath(ref)}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateConnectorParams describes an MCP server to add.
type CreateConnectorParams struct {
	// Name is a DNS label of at most 40 characters, unique among the
	// owner's servers. Required.
	Name string
	// Title is what a person reads. Required.
	Title string
	// URL is the server's https address, on port 443. Required.
	URL string
	// Description says what the server is.
	Description string
	// Icon is the address of the server's icon.
	Icon string
	// Auth is how a person gives access; the server's own answer is taken
	// when nil.
	Auth *NewConnectorAuth
	// Tools marks which tools are read-only or hidden.
	Tools *ToolMarks
	// IdempotencyKey makes a repeated call answer the first one's server
	// instead of adding a second; one is generated when it is empty.
	IdempotencyKey string
}

// NewConnectorAuth is how a person gives an added server access.
type NewConnectorAuth struct {
	// Type is the way: AuthOAuth, AuthToken or AuthNone.
	Type AuthType `json:"type"`
	// Scopes are what a sign-in asks the server for.
	Scopes []string `json:"scopes,omitempty"`
	// Hint says where a person gets a key, at most 200 characters.
	Hint string `json:"hint,omitempty"`
	// Inject is where a key goes, for a server that takes it in a header
	// of its own; Authorization: Bearer when nil.
	Inject *Inject `json:"inject,omitempty"`
	// Client is a sign-in client registered at the server, taken only from
	// an organization's owner or admin. Its secret is kept sealed and
	// never answered.
	Client *ConnectorClient `json:"client,omitempty"`
}

// ConnectorClient is a sign-in client registered at an added server.
type ConnectorClient struct {
	// ID is the client's id.
	ID string `json:"id"`
	// Secret is the client's secret, at least 8 characters. It is sent once
	// and never answered.
	Secret string `json:"secret"`
}

// CreateConnector adds an MCP server for the caller, or for the
// organization the caller's token names, and returns it.
func (c *Client) CreateConnector(ctx context.Context, params *CreateConnectorParams) (*Connector, error) {
	if params == nil || params.Name == "" || params.Title == "" || params.URL == "" {
		return nil, errors.New("connections: CreateConnectorParams.Name, Title and URL are required")
	}
	body := map[string]any{"name": params.Name, "title": params.Title, "kind": KindMCP, "url": params.URL}
	if params.Description != "" {
		body["description"] = params.Description
	}
	if params.Icon != "" {
		body["icon"] = params.Icon
	}
	if params.Auth != nil {
		body["auth"] = params.Auth
	}
	if params.Tools != nil {
		body["tools"] = params.Tools
	}
	key := params.IdempotencyKey
	if key == "" {
		key = latere.NewIdempotencyKey()
	}
	var out Connector
	req := &latere.Request{Method: http.MethodPost, Path: basePath + "/connectors", Body: body, IdempotencyKey: key}
	if err := c.cfg.Do(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateConnectorParams changes an added server. A field left nil or empty
// keeps its value. A server's name, kind and URL cannot change.
type UpdateConnectorParams struct {
	// Title is what a person reads.
	Title string
	// Description says what the server is.
	Description string
	// Icon is the address of the server's icon.
	Icon string
	// Disabled turns the server off or on again.
	Disabled *bool
	// Auth replaces how a person gives access.
	Auth *NewConnectorAuth
	// Tools replaces which tools are read-only or hidden.
	Tools *ToolMarks
}

// UpdateConnector changes the added server ref names, by its name or id,
// and returns it.
func (c *Client) UpdateConnector(ctx context.Context, ref string, params *UpdateConnectorParams) (*Connector, error) {
	if ref == "" {
		return nil, errNoConnector
	}
	body := map[string]any{}
	if params != nil {
		if params.Title != "" {
			body["title"] = params.Title
		}
		if params.Description != "" {
			body["description"] = params.Description
		}
		if params.Icon != "" {
			body["icon"] = params.Icon
		}
		if params.Disabled != nil {
			body["disabled"] = *params.Disabled
		}
		if params.Auth != nil {
			body["auth"] = params.Auth
		}
		if params.Tools != nil {
			body["tools"] = params.Tools
		}
	}
	var out Connector
	if err := c.cfg.Do(ctx, &latere.Request{Method: http.MethodPatch, Path: connectorPath(ref), Body: body}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteConnector removes the added server ref names, by its name or id.
// A server connections still use returns an error with status 409.
func (c *Client) DeleteConnector(ctx context.Context, ref string) error {
	if ref == "" {
		return errNoConnector
	}
	return c.cfg.Do(ctx, &latere.Request{Method: http.MethodDelete, Path: connectorPath(ref)}, nil)
}

// setIf sets q's key to value when value is not empty.
func setIf(q url.Values, key, value string) {
	if value != "" {
		q.Set(key, value)
	}
}

// setLimit sets q's limit when limit is positive.
func setLimit(q url.Values, limit int) {
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
}
