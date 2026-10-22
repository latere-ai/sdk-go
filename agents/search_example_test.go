// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents_test

import (
	"context"
	"fmt"
	"log"

	"latere.ai/x/sdk-go/agents"
	"latere.ai/x/sdk-go/latere"
)

// Searching what was said in Latere Chat's sessions, with the words that
// matched marked.
func ExampleClient_SearchSessions() {
	ctx := context.Background()
	for found, err := range client().SearchSessions(ctx, &agents.SearchSessionsParams{Query: "release notes", Agent: "latere-chat"}) {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(found.Session.Title)
		for _, m := range found.Matches {
			for _, part := range m.Excerpt {
				if part.Match {
					fmt.Printf("[%s]", part.Text)
				} else {
					fmt.Print(part.Text)
				}
			}
			fmt.Println()
		}
	}
}

// Counting Latere Chat's sessions by what each is doing, as a list's
// header shows them.
func ExampleClient_GetSessionSummary() {
	ctx := context.Background()
	s, err := client().GetSessionSummary(ctx, &agents.SessionSummaryParams{Agent: "latere-chat"})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%d running, %d waiting for approval, %d idle\n", s.Sessions.Running, s.Sessions.WaitingForApproval, s.Sessions.Idle)
}

// Reading the conversation a share link shows, as anyone holding the
// link reads it.
func ExampleClient_GetSharedPage() {
	cfg, err := latere.NewConfig(latere.WithoutCredentials())
	if err != nil {
		log.Fatal(err)
	}
	page, err := agents.New(cfg).GetSharedPage(context.Background(), "the link's token", nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(page.Share.Title)
	for _, e := range page.Events {
		if e.Type == agents.EventAgentMessage {
			var m agents.AgentMessage
			if err := e.Decode(&m); err != nil {
				log.Fatal(err)
			}
			fmt.Println(m.Text())
		}
	}
}
