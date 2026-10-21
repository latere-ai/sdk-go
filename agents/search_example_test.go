// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents_test

import (
	"context"
	"fmt"
	"log"

	"latere.ai/x/sdk-go/agents"
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
