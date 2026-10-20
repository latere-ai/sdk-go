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

func sessions() *agents.SessionsService {
	cfg, err := latere.NewConfig() // the key in LATERE_KEY
	if err != nil {
		log.Fatal(err)
	}
	return agents.New(cfg).Sessions
}

// Listing the sessions of one agent that wait for an answer.
func ExampleSessionsService_List() {
	ctx := context.Background()
	params := &agents.ListSessionsParams{Agent: "release-notes", Status: agents.StatusIdle}
	for s, err := range sessions().List(ctx, params) {
		if err != nil {
			log.Fatal(err)
		}
		if s.StopReason == agents.StopQuestion || s.StopReason == agents.StopToolConfirmation {
			fmt.Println(s.ID, s.Title)
		}
	}
}

// Paging by hand, for a program that keeps the cursor between requests.
func ExampleSessionsService_ListPage() {
	ctx := context.Background()
	params := &agents.ListSessionsParams{Limit: 20}
	for {
		page, err := sessions().ListPage(ctx, params)
		if err != nil {
			log.Fatal(err)
		}
		for _, s := range page.Items {
			fmt.Println(s.ID)
		}
		if page.NextCursor == "" {
			break
		}
		params.Cursor = page.NextCursor
	}
}

// Starting a session with its first message.
func ExampleSessionsService_Create() {
	s, err := sessions().Create(context.Background(), &agents.CreateSessionParams{
		Agent:    "release-notes",
		Message:  "Write the release notes for v1.4.0.",
		Attended: true,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(s.ID, s.Status)
}

// Following a session as it works, printing the response as it is written.
func ExampleSessionsService_Stream() {
	ctx := context.Background()
	for item, err := range sessions().Stream(ctx, "ses_01M34G2JF01B91B8399CD1G6YP", &agents.StreamParams{Deltas: true}) {
		if err != nil {
			log.Fatal(err)
		}
		switch {
		case item.Delta != nil && item.Delta.Kind == "text":
			fmt.Print(item.Delta.Text)
		case item.Event != nil && item.Event.Type == agents.EventSessionStatus:
			var st agents.SessionStatus
			if err := item.Event.Decode(&st); err != nil {
				log.Fatal(err)
			}
			fmt.Println("\nstatus:", st.Status, st.StopReason)
		}
	}
}

// Sending a message to an idle session.
func ExampleSessionsService_SendMessage() {
	_, err := sessions().SendMessage(context.Background(), "ses_01M34G2JF01B91B8399CD1G6YP", &agents.MessageParams{
		Content: []agents.ContentBlock{agents.Text("Add a section for the breaking changes.")},
	})
	if err != nil {
		log.Fatal(err)
	}
}

// Answering the question a session stopped on.
func ExampleSessionsService_Answer() {
	ctx := context.Background()
	s := sessions()
	id := "ses_01M34G2JF01B91B8399CD1G6YP"
	var call *agents.ToolUse
	for e, err := range s.Events(ctx, id, nil) {
		if err != nil {
			log.Fatal(err)
		}
		if e.Type == agents.EventAgentToolUse {
			var tu agents.ToolUse
			if err := e.Decode(&tu); err != nil {
				log.Fatal(err)
			}
			if tu.Name == "question" {
				call = &tu
			}
		}
	}
	if call == nil {
		return
	}
	questions, err := call.Questions()
	if err != nil {
		log.Fatal(err)
	}
	answers := make([]agents.AnswerEntry, len(questions))
	for i, q := range questions {
		answers[i] = agents.AnswerEntry{Selected: []string{q.Options[0].Label}}
	}
	if _, err := s.Answer(ctx, id, &agents.AnswerParams{ToolUseID: call.ToolUseID, Answers: answers}); err != nil {
		log.Fatal(err)
	}
}

// Approving a tool call the session waits on.
func ExampleSessionsService_ConfirmTool() {
	_, err := sessions().ConfirmTool(context.Background(), "ses_01M34G2JF01B91B8399CD1G6YP", &agents.ToolConfirmationParams{
		ToolUseID: "toolu_01",
		Decision:  agents.Allow,
	})
	if err != nil {
		log.Fatal(err)
	}
}
