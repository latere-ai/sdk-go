// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package connections_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"latere.ai/x/sdk-go/connections"
	"latere.ai/x/sdk-go/latere"
)

// call is one request the test server received.
type call struct {
	Method, Path, Query, Key string
	Body                     map[string]any
}

// fakeAPI answers each request with the body routes holds for its method
// and path, and records the requests. A route whose body starts with a
// three-digit status and a space answers with that status.
func fakeAPI(t *testing.T, routes map[string]string) (*connections.Client, *[]call) {
	t.Helper()
	var calls []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := call{Method: r.Method, Path: r.URL.EscapedPath(), Query: r.URL.RawQuery, Key: r.Header.Get("Idempotency-Key")}
		if raw, err := io.ReadAll(r.Body); err != nil {
			t.Error(err)
		} else if len(raw) > 0 {
			if err := json.Unmarshal(raw, &c.Body); err != nil {
				t.Errorf("a body that is not JSON: %s", raw)
			}
		}
		calls = append(calls, c)
		body, ok := routes[r.Method+" "+r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"error":{"code":"connection_not_found","message":"No such connection."}}`)
			return
		}
		if body == "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		status := http.StatusOK
		if len(body) > 4 && body[3] == ' ' {
			if _, err := fmt.Sscanf(body[:3], "%d", &status); err == nil {
				body = body[4:]
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	cfg, err := latere.NewConfig(latere.WithBaseURL(srv.URL), latere.WithTokenSource(latere.StaticToken("key_test")), latere.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	return connections.New(cfg), &calls
}

const connection = `{"id":"con_1","name":"calendar","owner":"https://auth.latere.ai|alice","connector":"google-calendar","kind":"mcp","auth":"oauth",
"state":"connected","account":"alice@example.com","scopes":["calendar.readonly"],"address":"https://connect.latere.ai/con_1",
"tools":{"read_only":["list_events"],"hidden":[]},"links":{"manage":"https://myaccount.google.com/permissions"},
"created_by":"https://auth.latere.ai|alice","created_at":"2026-10-01T09:00:00Z","updated_at":"2026-10-02T09:00:00Z",
"expires_at":"2027-04-01T09:00:00Z","last_used_at":"2026-10-03T09:00:00Z"}`

const connector = `{"name":"notes","id":"mcp_1","title":"Notes","description":"Team notes","icon":"https://example.com/i.png","kind":"mcp",
"url":"https://notes.example.com/mcp","hosts":["notes.example.com"],"auth":{"type":"token","hint":"Settings, then Keys",
"inject":{"header":"X-API-Key","format":"{value}"}},"tools":{"read_only":["search"],"hidden":["admin"]},
"owner":"https://auth.latere.ai|alice","vouched":false,"disabled":false,"connections":2}`

func TestGetConnection(t *testing.T) {
	c, calls := fakeAPI(t, map[string]string{"GET /v1/connections/connections/con_1": connection})
	got, err := c.GetConnection(t.Context(), "con_1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != connections.StateConnected || got.Kind != connections.KindMCP || got.Auth != connections.AuthOAuth ||
		got.Tools.ReadOnly[0] != "list_events" || got.Links.Manage == "" || got.ExpiresAt == nil || got.LastUsedAt == nil || got.CreatedAt.IsZero() {
		t.Errorf("decoded %+v", got)
	}
	if (*calls)[0].Method != http.MethodGet {
		t.Errorf("sent %+v", (*calls)[0])
	}
	if _, err := c.GetConnection(t.Context(), "con_missing"); !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("a missing connection: %v", err)
	}
}

func TestListConnectionsSendsTheFilters(t *testing.T) {
	c, calls := fakeAPI(t, map[string]string{"GET /v1/connections/connections": `{"items":[` + connection + `],"next_cursor":""}`})
	n := 0
	for got, err := range c.ListConnections(t.Context(), &connections.ListConnectionsParams{
		Owner: "org_1", Connector: "google-calendar", State: connections.StateNeedsReconnect, Limit: 5, Cursor: "ignored",
	}) {
		if err != nil {
			t.Fatal(err)
		}
		if got.ID != "con_1" {
			t.Errorf("listed %+v", got)
		}
		n++
	}
	if n != 1 {
		t.Errorf("listed %d", n)
	}
	if q := (*calls)[0].Query; q != "connector=google-calendar&limit=5&owner=org_1&state=needs_reconnect" {
		t.Errorf("sent the query %q", q)
	}
	if _, err := c.ListConnectionsPage(t.Context(), &connections.ListConnectionsParams{Cursor: "c2"}); err != nil {
		t.Fatal(err)
	}
	if q := (*calls)[1].Query; q != "cursor=c2" {
		t.Errorf("a page sent the query %q", q)
	}
	if _, err := c.ListConnectionsPage(t.Context(), nil); err != nil {
		t.Errorf("nil params: %v", err)
	}
}

func TestCreateConnectionAnswersASignIn(t *testing.T) {
	pending := strings.Replace(connection, `"state":"connected"`, `"state":"pending"`, 1)
	c, calls := fakeAPI(t, map[string]string{"POST /v1/connections/connections": `201 {"connection":` + pending + `,"authorization_url":"https://accounts.google.com/o/oauth2/auth?x=1"}`})
	got, err := c.CreateConnection(t.Context(), &connections.CreateConnectionParams{
		Connector: "google-calendar", Name: "calendar", ReturnTo: "https://platform.latere.ai/console/connections", Scopes: []string{"calendar.events"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.AuthorizationURL == "" || got.Connection.State != connections.StatePending {
		t.Errorf("answered %+v", got)
	}
	sent := (*calls)[0]
	if sent.Key == "" || sent.Body["return_to"] != "https://platform.latere.ai/console/connections" || sent.Body["name"] != "calendar" || sent.Body["scopes"] == nil {
		t.Errorf("sent %+v", sent)
	}
	if _, has := sent.Body["credential"]; has {
		t.Error("sent a credential the caller did not give")
	}
}

func TestCreateConnectionWithAKeyAnswersTheConnection(t *testing.T) {
	c, calls := fakeAPI(t, map[string]string{"POST /v1/connections/connections": `201 ` + connection})
	got, err := c.CreateConnection(t.Context(), &connections.CreateConnectionParams{
		Connector: "notes", Credential: &connections.Credential{Value: "secret-key"}, IdempotencyKey: "k1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.AuthorizationURL != "" || got.Connection.ID != "con_1" {
		t.Errorf("answered %+v", got)
	}
	sent := (*calls)[0]
	if sent.Key != "k1" || sent.Body["credential"].(map[string]any)["value"] != "secret-key" {
		t.Errorf("sent %+v", sent)
	}
	for _, bad := range []*connections.CreateConnectionParams{nil, {}} {
		if _, err := c.CreateConnection(t.Context(), bad); err == nil {
			t.Errorf("connected %+v without a connector", bad)
		}
	}
}

func TestCreateConnectionReportsAnAnswerItCannotRead(t *testing.T) {
	c, _ := fakeAPI(t, map[string]string{"POST /v1/connections/connections": `201 ["not", "a", "connection"]`})
	if _, err := c.CreateConnection(t.Context(), &connections.CreateConnectionParams{Connector: "notes"}); err == nil {
		t.Error("an answer that is no connection was read")
	}
	c, _ = fakeAPI(t, map[string]string{"POST /v1/connections/connections": `201 {"id":7}`})
	if _, err := c.CreateConnection(t.Context(), &connections.CreateConnectionParams{Connector: "notes"}); err == nil {
		t.Error("a connection with a wrong field type was read")
	}
}

func TestChangingAConnection(t *testing.T) {
	c, calls := fakeAPI(t, map[string]string{
		"PATCH /v1/connections/connections/con_1":          connection,
		"DELETE /v1/connections/connections/con_1":         "",
		"POST /v1/connections/connections/con_1/confirm":   connection,
		"PUT /v1/connections/connections/con_1/credential": "",
		"POST /v1/connections/connections/con_1/reconnect": `{"connection":` + connection + `,"authorization_url":"https://example.com/signin"}`,
	})
	if got, err := c.RenameConnection(t.Context(), "con_1", "work-calendar"); err != nil || got.ID != "con_1" {
		t.Fatalf("rename: %+v, %v", got, err)
	}
	if b := (*calls)[0].Body; b["name"] != "work-calendar" {
		t.Errorf("rename sent %v", b)
	}
	if got, err := c.ConfirmConnection(t.Context(), "con_1", "code-1"); err != nil || got.ID != "con_1" {
		t.Fatalf("confirm: %+v, %v", got, err)
	}
	if b := (*calls)[1].Body; b["code"] != "code-1" {
		t.Errorf("confirm sent %v", b)
	}
	if err := c.ReplaceCredential(t.Context(), "con_1", connections.Credential{Value: "new-key"}); err != nil {
		t.Fatal(err)
	}
	if b := (*calls)[2].Body; b["credential"].(map[string]any)["value"] != "new-key" {
		t.Errorf("replace sent %v", b)
	}
	r, err := c.ReconnectConnection(t.Context(), "con_1", &connections.ReconnectConnectionParams{ReturnTo: "https://chat.latere.ai/back", Scopes: []string{"x"}})
	if err != nil || r.AuthorizationURL == "" || r.Connection.ID != "con_1" {
		t.Fatalf("reconnect: %+v, %v", r, err)
	}
	if b := (*calls)[3].Body; b["return_to"] != "https://chat.latere.ai/back" || b["scopes"] == nil {
		t.Errorf("reconnect sent %v", b)
	}
	if err := c.DeleteConnection(t.Context(), "con_1"); err != nil {
		t.Fatal(err)
	}
	if (*calls)[4].Method != http.MethodDelete {
		t.Errorf("delete sent %+v", (*calls)[4])
	}
	if _, err := c.ReconnectConnection(t.Context(), "con_1", nil); err == nil {
		t.Error("a reconnect without ReturnTo was sent")
	}
}

func TestToolsAndCalls(t *testing.T) {
	c, calls := fakeAPI(t, map[string]string{
		"GET /v1/connections/connections/con_1/tools": `{"items":[{"name":"search","description":"Searches","effect":"read","hidden":false}]}`,
		"GET /v1/connections/connections/con_1/calls": `{"items":[{"id":"call_1","connection":"con_1","owner":"o","connector":"notes","subject":"s","session":"ses_1",
"at":"2026-10-03T09:00:00Z","duration_ms":120,"method":"POST","tool":"search","effect":"read","outcome":"ok","status":200,"bytes_in":10,"bytes_out":900,"refs":0}],"next_cursor":""}`,
	})
	tools, err := c.ListTools(t.Context(), "con_1")
	if err != nil || len(tools) != 1 || tools[0].Effect != connections.EffectRead {
		t.Fatalf("tools: %+v, %v", tools, err)
	}
	n := 0
	for got, err := range c.ListCalls(t.Context(), "con_1", &connections.ListCallsParams{Limit: 1}) {
		if err != nil {
			t.Fatal(err)
		}
		if got.Outcome != connections.OutcomeOK || got.DurationMS != 120 || got.At.IsZero() {
			t.Errorf("listed %+v", got)
		}
		n++
	}
	if n != 1 || (*calls)[1].Query != "limit=1" {
		t.Errorf("listed %d calls with the query %q", n, (*calls)[1].Query)
	}
	if _, err := c.ListCallsPage(t.Context(), "con_1", &connections.ListCallsParams{Cursor: "c2"}); err != nil {
		t.Fatal(err)
	}
	if q := (*calls)[2].Query; q != "cursor=c2" {
		t.Errorf("a page sent the query %q", q)
	}
}

func TestConnectors(t *testing.T) {
	c, calls := fakeAPI(t, map[string]string{
		"GET /v1/connections/connectors":          `{"items":[` + connector + `],"next_cursor":""}`,
		"GET /v1/connections/connectors/notes":    connector,
		"POST /v1/connections/connectors":         `201 ` + connector,
		"PATCH /v1/connections/connectors/notes":  connector,
		"DELETE /v1/connections/connectors/notes": "",
	})
	n := 0
	for got, err := range c.ListConnectors(t.Context(), &connections.ListConnectorsParams{Owner: "o", Kind: connections.KindMCP, Limit: 3}) {
		if err != nil {
			t.Fatal(err)
		}
		if got.Auth.Type != connections.AuthToken || got.Auth.Inject.Header != "X-API-Key" || got.Connections != 2 {
			t.Errorf("listed %+v", got)
		}
		n++
	}
	if n != 1 || (*calls)[0].Query != "kind=mcp&limit=3&owner=o" {
		t.Errorf("listed %d with the query %q", n, (*calls)[0].Query)
	}
	if _, err := c.ListConnectorsPage(t.Context(), &connections.ListConnectorsParams{Cursor: "c2"}); err != nil {
		t.Fatal(err)
	}
	if got, err := c.GetConnector(t.Context(), "notes"); err != nil || got.Name != "notes" {
		t.Fatalf("get: %+v, %v", got, err)
	}
	made, err := c.CreateConnector(t.Context(), &connections.CreateConnectorParams{
		Name: "notes", Title: "Notes", URL: "https://notes.example.com/mcp", Description: "Team notes", Icon: "https://example.com/i.png",
		Auth:  &connections.NewConnectorAuth{Type: connections.AuthOAuth, Client: &connections.ConnectorClient{ID: "cid", Secret: "a-secret"}},
		Tools: &connections.ToolMarks{ReadOnly: []string{"search"}},
	})
	if err != nil || made.Name != "notes" {
		t.Fatalf("create: %+v, %v", made, err)
	}
	sent := (*calls)[3]
	if sent.Key == "" || sent.Body["kind"] != "mcp" || sent.Body["auth"].(map[string]any)["client"].(map[string]any)["secret"] != "a-secret" {
		t.Errorf("create sent %+v", sent)
	}
	off := true
	if _, err := c.UpdateConnector(t.Context(), "notes", &connections.UpdateConnectorParams{
		Title: "Team notes", Description: "d", Icon: "https://example.com/j.png", Disabled: &off,
		Auth: &connections.NewConnectorAuth{Type: connections.AuthNone}, Tools: &connections.ToolMarks{Hidden: []string{"admin"}},
	}); err != nil {
		t.Fatal(err)
	}
	if b := (*calls)[4].Body; b["disabled"] != true || b["title"] != "Team notes" || b["tools"] == nil {
		t.Errorf("update sent %v", b)
	}
	n = len(*calls)
	for name, params := range map[string]*connections.UpdateConnectorParams{"nil": nil, "empty": {}} {
		if _, err := c.UpdateConnector(t.Context(), "notes", params); err == nil {
			t.Errorf("a %s update was not refused", name)
		}
	}
	if len(*calls) != n {
		t.Errorf("an empty update was sent: %+v", (*calls)[n:])
	}
	if err := c.DeleteConnector(t.Context(), "notes"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []*connections.CreateConnectorParams{nil, {Name: "n"}, {Name: "n", Title: "t"}} {
		if _, err := c.CreateConnector(t.Context(), bad); err == nil {
			t.Errorf("created %+v", bad)
		}
	}
}

func TestEmptyIDsAreNotSent(t *testing.T) {
	c, calls := fakeAPI(t, map[string]string{})
	ctx := t.Context()
	checks := map[string]error{}
	_, checks["get"] = c.GetConnection(ctx, "")
	_, checks["rename"] = c.RenameConnection(ctx, "", "x")
	checks["delete"] = c.DeleteConnection(ctx, "")
	_, checks["confirm"] = c.ConfirmConnection(ctx, "", "x")
	_, checks["reconnect"] = c.ReconnectConnection(ctx, "", nil)
	checks["credential"] = c.ReplaceCredential(ctx, "", connections.Credential{})
	_, checks["tools"] = c.ListTools(ctx, "")
	_, checks["calls"] = c.ListCallsPage(ctx, "", nil)
	_, checks["connector"] = c.GetConnector(ctx, "")
	_, checks["update"] = c.UpdateConnector(ctx, "", nil)
	checks["remove"] = c.DeleteConnector(ctx, "")
	for name, err := range checks {
		if err == nil {
			t.Errorf("%s with an empty id returned no error", name)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("sent %d requests", len(*calls))
	}
}

func TestCallsReturnTheAPIsRefusal(t *testing.T) {
	c, _ := fakeAPI(t, map[string]string{})
	ctx := t.Context()
	checks := map[string]error{}
	_, checks["list"] = c.ListConnectionsPage(ctx, nil)
	_, checks["create"] = c.CreateConnection(ctx, &connections.CreateConnectionParams{Connector: "x"})
	_, checks["rename"] = c.RenameConnection(ctx, "x", "y")
	_, checks["confirm"] = c.ConfirmConnection(ctx, "x", "y")
	_, checks["reconnect"] = c.ReconnectConnection(ctx, "x", &connections.ReconnectConnectionParams{ReturnTo: "https://example.com"})
	_, checks["tools"] = c.ListTools(ctx, "x")
	_, checks["calls"] = c.ListCallsPage(ctx, "x", nil)
	_, checks["connectors"] = c.ListConnectorsPage(ctx, nil)
	_, checks["connector"] = c.GetConnector(ctx, "x")
	_, checks["add"] = c.CreateConnector(ctx, &connections.CreateConnectorParams{Name: "n", Title: "t", URL: "https://example.com"})
	_, checks["update"] = c.UpdateConnector(ctx, "x", &connections.UpdateConnectorParams{Title: "t"})
	for name, err := range checks {
		if !errors.Is(err, latere.ErrNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
