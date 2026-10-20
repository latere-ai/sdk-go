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
// the answer names, then finish with the code the browser brings back.
func ExampleClient_CreateConnection() {
	ctx := context.Background()
	cs := client()
	r, err := cs.CreateConnection(ctx, &connections.CreateConnectionParams{
		Connector: "google-calendar",
		ReturnTo:  "https://example.com/connected",
	})
	if err != nil {
		log.Fatal(err)
	}
	if r.AuthorizationURL == "" {
		fmt.Println("connected:", r.Connection.Name)
		return
	}
	fmt.Println("Sign in at", r.AuthorizationURL)
	// Later, on the page at ReturnTo, with the code from its query:
	code := "the code from the return address"
	c, err := cs.ConfirmConnection(ctx, r.Connection.ID, code)
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
