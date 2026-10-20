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

func client() *agents.Client {
	cfg, err := latere.NewConfig() // the key in LATERE_KEY
	if err != nil {
		log.Fatal(err)
	}
	return agents.New(cfg)
}

// Listing the sessions of one agent that wait for an answer.
func ExampleClient_ListSessions() {
	ctx := context.Background()
	params := &agents.ListSessionsParams{Agent: "release-notes", Status: agents.StatusIdle}
	for s, err := range client().ListSessions(ctx, params) {
		if err != nil {
			log.Fatal(err)
		}
		if s.StopReason == agents.StopQuestion || s.StopReason == agents.StopToolConfirmation {
			fmt.Println(s.ID, s.Title)
		}
	}
}

// Paging by hand, for a program that keeps the cursor between requests.
func ExampleClient_ListSessionsPage() {
	ctx := context.Background()
	ag := client()
	params := &agents.ListSessionsParams{Limit: 20}
	for {
		page, err := ag.ListSessionsPage(ctx, params)
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
func ExampleClient_CreateSession() {
	s, err := client().CreateSession(context.Background(), &agents.CreateSessionParams{
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
func ExampleClient_StreamEvents() {
	ctx := context.Background()
	for item, err := range client().StreamEvents(ctx, "ses_01M34G2JF01B91B8399CD1G6YP", &agents.StreamEventsParams{Deltas: true}) {
		if err != nil {
			log.Fatal(err)
		}
		switch {
		case item.Delta != nil && item.Delta.Kind == agents.DeltaText:
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

// Reading the agent's responses in a session's log.
func ExampleAgentMessage() {
	ctx := context.Background()
	for e, err := range client().ListEvents(ctx, "ses_01M34G2JF01B91B8399CD1G6YP", nil) {
		if err != nil {
			log.Fatal(err)
		}
		if e.Type != agents.EventAgentMessage {
			continue
		}
		var m agents.AgentMessage
		if err := e.Decode(&m); err != nil {
			log.Fatal(err)
		}
		fmt.Println(m.Text())
	}
}

// Sending a message to an idle session.
func ExampleClient_SendMessage() {
	_, err := client().SendMessage(context.Background(), "ses_01M34G2JF01B91B8399CD1G6YP", &agents.SendMessageParams{
		Content: []agents.ContentBlock{agents.Text("Add a section for the breaking changes.")},
	})
	if err != nil {
		log.Fatal(err)
	}
}

// Answering the question a session stopped on.
func ExampleClient_AnswerQuestion() {
	ctx := context.Background()
	ag := client()
	id := "ses_01M34G2JF01B91B8399CD1G6YP"
	var call *agents.ToolUse
	for e, err := range ag.ListEvents(ctx, id, nil) {
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
	answers := make([]agents.Answer, len(questions))
	for i, q := range questions {
		answers[i] = agents.Answer{Selected: []string{q.Options[0].Label}}
	}
	if _, err := ag.AnswerQuestion(ctx, id, &agents.AnswerQuestionParams{ToolUseID: call.ToolUseID, Answers: answers}); err != nil {
		log.Fatal(err)
	}
}

// Approving a tool call the session waits on.
func ExampleClient_ConfirmTool() {
	_, err := client().ConfirmTool(context.Background(), "ses_01M34G2JF01B91B8399CD1G6YP", &agents.ConfirmToolParams{
		ToolUseID: "toolu_01",
		Decision:  agents.Allow,
	})
	if err != nil {
		log.Fatal(err)
	}
}

// Running a tool the agent declares for its client, and sending the result.
func ExampleClient_SendToolResult() {
	ctx := context.Background()
	ag := client()
	id := "ses_01M34G2JF01B91B8399CD1G6YP"
	for item, err := range ag.StreamEvents(ctx, id, nil) {
		if err != nil {
			log.Fatal(err)
		}
		if item.Event == nil || item.Event.Type != agents.EventAgentToolUse {
			continue
		}
		var call agents.ToolUse
		if err := item.Event.Decode(&call); err != nil {
			log.Fatal(err)
		}
		if !call.Client {
			continue
		}
		result := "It is 21°C in Berlin." // what your program's tool returned
		_, err = ag.SendToolResult(ctx, id, &agents.SendToolResultParams{
			ToolUseID: call.ToolUseID,
			Content:   []agents.ContentBlock{agents.Text(result)},
		})
		if err != nil {
			log.Fatal(err)
		}
	}
}

// Ending a session whose work is done, so it takes no more messages.
func ExampleClient_EndSession() {
	s, err := client().EndSession(context.Background(), "ses_01M34G2JF01B91B8399CD1G6YP", agents.StopCompleted)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(s.Status, s.StopReason)
}

// Running a session's next turn at another Effort level.
func ExampleClient_UpdateSession() {
	_, err := client().UpdateSession(context.Background(), "ses_01M34G2JF01B91B8399CD1G6YP", &agents.UpdateSessionParams{
		Model: &agents.Model{Name: "tier/thorough"},
	})
	if err != nil {
		log.Fatal(err)
	}
}
