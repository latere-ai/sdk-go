// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package connections_test

import (
	"encoding/json"
	"testing"

	"latere.ai/x/sdk-go/connections"
)

// Each resource the package answers keeps the JSON it was decoded from, a
// field the package does not model included.
func TestEveryResourceKeepsItsJSON(t *testing.T) {
	for name, v := range map[string]interface {
		RawJSON() json.RawMessage
	}{
		"connection": &connections.Connection{},
		"connector":  &connections.Connector{},
		"tool":       &connections.Tool{},
		"call":       &connections.Call{},
	} {
		if v.RawJSON() != nil {
			t.Errorf("%s: one the program built has JSON", name)
		}
		raw := `{"added_later":"kept"}`
		if err := json.Unmarshal([]byte(raw), v); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(v.RawJSON()) != raw {
			t.Errorf("%s: RawJSON = %s, want %s", name, v.RawJSON(), raw)
		}
	}
}

// A connection answered by the API keeps its JSON alongside the decoded
// fields.
func TestAConnectionKeepsTheJSONTheAPIAnswered(t *testing.T) {
	c, _ := fakeAPI(t, map[string]string{"GET /v1/connections/connections/con_1": connection})
	got, err := c.GetConnection(t.Context(), "con_1")
	if err != nil {
		t.Fatal(err)
	}
	if string(got.RawJSON()) != connection || got.ID != "con_1" {
		t.Errorf("connection %s with %.40s; want the decoded id and the whole answer", got.ID, got.RawJSON())
	}
}

// JSON of the wrong shape is refused, and nothing is kept.
func TestAResourceRefusesJSONOfTheWrongShape(t *testing.T) {
	for _, v := range []interface {
		RawJSON() json.RawMessage
	}{
		&connections.Connection{},
		&connections.Connector{},
		&connections.Tool{},
		&connections.Call{},
	} {
		if err := json.Unmarshal([]byte(`[]`), v); err == nil {
			t.Errorf("%T decoded an array", v)
		}
		if v.RawJSON() != nil {
			t.Errorf("%T kept JSON it refused", v)
		}
	}
}

// A connect's answer keeps its JSON, whichever shape it takes: the
// connection, or a sign-in to finish.
func TestConnectedKeepsTheJSONTheAPIAnswered(t *testing.T) {
	signIn := `{"connection":` + connection + `,"authorization_url":"https://accounts.example.com/o/authorize"}`
	c, _ := fakeAPI(t, map[string]string{
		"POST /v1/connections/connections":                 connection,
		"POST /v1/connections/connections/con_1/reconnect": signIn,
	})
	got, err := c.CreateConnection(t.Context(), &connections.CreateConnectionParams{Connector: "google-calendar"})
	if err != nil {
		t.Fatal(err)
	}
	if string(got.RawJSON()) != connection {
		t.Errorf("a connected answer kept %.40s", got.RawJSON())
	}
	again, err := c.ReconnectConnection(t.Context(), "con_1", &connections.ReconnectConnectionParams{ReturnTo: "https://chat.latere.ai/connected"})
	if err != nil {
		t.Fatal(err)
	}
	if string(again.RawJSON()) != signIn || again.AuthorizationURL == "" || again.Connection == nil {
		t.Errorf("a sign-in answer: %+v with %.40s; want the sign-in and its JSON", again, again.RawJSON())
	}
	if (&connections.Connected{}).RawJSON() != nil {
		t.Error("a Connected the program built has JSON")
	}
}
