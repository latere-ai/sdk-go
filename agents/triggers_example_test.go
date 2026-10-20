// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents_test

import (
	"context"
	"fmt"
	"log"

	"latere.ai/x/sdk-go/agents"
)

// A trigger that starts a session every Monday at 9:00 in Berlin. Applying
// it again, after changing the message for instance, replaces the trigger
// of the same name instead of adding a second.
func ExampleClient_ApplyTrigger() {
	ctx := context.Background()
	t, err := client().ApplyTrigger(ctx, &agents.Trigger{
		Metadata: agents.TriggerMetadata{Name: "weekly-digest", DisplayName: "Weekly digest"},
		Spec: agents.TriggerSpec{
			Agent:    "release-notes",
			Schedule: "0 9 * * 1",
			TimeZone: "Europe/Berlin",
			Session:  agents.TriggerSession{Message: "Summarize last week's releases."},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(t.Status.ID, t.Status.NextFireAt)
}

// Listing the caller's triggers with how often each started a session.
func ExampleClient_ListTriggers() {
	ctx := context.Background()
	for t, err := range client().ListTriggers(ctx, nil) {
		if err != nil {
			log.Fatal(err)
		}
		started := int64(0)
		if t.Status.Counts != nil {
			started = t.Status.Counts.Started
		}
		fmt.Println(t.Metadata.Name, started)
	}
}

// Running a schedule trigger now, outside its schedule.
func ExampleClient_FireTrigger() {
	ctx := context.Background()
	f, err := client().FireTrigger(ctx, "weekly-digest", nil)
	if err != nil {
		log.Fatal(err)
	}
	if f.Outcome == agents.FiringStarted {
		fmt.Println("started", f.SessionID)
	}
}
