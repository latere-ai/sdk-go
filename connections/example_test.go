// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package connections_test

import (
	"context"
	"fmt"
	"log"

	"latere.ai/x/sdk-go/connections"
	"latere.ai/x/sdk-go/latere"
)

func client() *connections.Client {
	cfg, err := latere.NewConfig() // the key in LATERE_KEY
	if err != nil {
		log.Fatal(err)
	}
	return connections.New(cfg)
}

// Connecting a service that signs people in: send the person to the page
// the answer names. The browser returns to the console's Connections page,
// which finishes the connection.
func ExampleClient_CreateConnection() {
	r, err := client().CreateConnection(context.Background(), &connections.CreateConnectionParams{
		Connector: "google-calendar",
		ReturnTo:  "https://platform.latere.ai/console/connections",
	})
	if err != nil {
		log.Fatal(err)
	}
	if r.AuthorizationURL == "" {
		fmt.Println("connected:", r.Connection.Name)
		return
	}
	fmt.Println("Sign in at", r.AuthorizationURL)
}

// An app Latere registers with its own return page finishes the sign-in
// with the code the browser brings back to that page.
func ExampleClient_ConfirmConnection() {
	code := "the code from the return page's query"
	c, err := client().ConfirmConnection(context.Background(), "con_01M34G2JF01B91B8399CD1G6YP", code)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(c.Name, c.State)
}

// Connecting a service that takes a key.
func ExampleClient_CreateConnection_key() {
	r, err := client().CreateConnection(context.Background(), &connections.CreateConnectionParams{
		Connector:  "notes",
		Credential: &connections.Credential{Value: "the key the service issued"},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(r.Connection.Name, r.Connection.State)
}

// Finding the connections that need the person to sign in again.
func ExampleClient_ListConnections() {
	ctx := context.Background()
	params := &connections.ListConnectionsParams{State: connections.StateNeedsReconnect}
	for c, err := range client().ListConnections(ctx, params) {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(c.Name, c.Reason)
	}
}

// Adding an MCP server of one's own that takes a key in a header.
func ExampleClient_CreateConnector() {
	s, err := client().CreateConnector(context.Background(), &connections.CreateConnectorParams{
		Name:  "notes",
		Title: "Team notes",
		URL:   "https://notes.example.com/mcp",
		Auth: &connections.NewConnectorAuth{
			Type:   connections.AuthToken,
			Inject: &connections.Inject{Header: "X-API-Key", Format: "{value}"},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(s.Name, s.ID)
}
