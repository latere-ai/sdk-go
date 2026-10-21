// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"latere.ai/x/sdk-go/agents"
	"latere.ai/x/sdk-go/latere"
	"latere.ai/x/sdk-go/storage"
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
		Decision:  agents.DecisionAllow,
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
		Model: &agents.ModelChange{Name: "tier/thorough"},
	})
	if err != nil {
		log.Fatal(err)
	}
}

// Raising how much the model reasons, keeping its model, and later
// returning to the level the agent names.
func ExampleModelChange() {
	ctx := context.Background()
	id := "ses_01M34G2JF01B91B8399CD1G6YP"
	if _, err := client().UpdateSession(ctx, id, &agents.UpdateSessionParams{
		Model: &agents.ModelChange{Reasoning: agents.ReasoningHigh},
	}); err != nil {
		log.Fatal(err)
	}
	if _, err := client().UpdateSession(ctx, id, &agents.UpdateSessionParams{
		Model: &agents.ModelChange{AgentReasoning: true},
	}); err != nil {
		log.Fatal(err)
	}
}

// Sending a file with a message: a file of up to MaxAttachmentBytes goes
// with it, and a larger one goes up to Storage first, for the message to
// name by its path there.
func ExampleClient_SendMessage_files() {
	ctx := context.Background()
	cfg, err := latere.NewConfig()
	if err != nil {
		log.Fatal(err)
	}
	f, err := os.Open("talk.mp4")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		log.Fatal(err)
	}
	file, err := storage.New(cfg).UploadFile(ctx, storage.Me, "files/Uploads/talk.mp4", f, info.Size(), &storage.UploadOptions{ContentType: "video/mp4"})
	if err != nil {
		log.Fatal(err)
	}
	_, err = agents.New(cfg).SendMessage(ctx, "ses_01M34G2JF01B91B8399CD1G6YP", &agents.SendMessageParams{
		Content: []agents.ContentBlock{agents.Text("Write up the talk's three points.")},
		Attachments: []agents.NewAttachment{
			{Name: "notes.md", MediaType: "text/markdown", Data: []byte("Keep it short.")},
			{Name: "talk.mp4", Source: &agents.AttachmentSource{Owner: storage.Me, Path: file.Path}},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}

// Saving the pictures and files the agent's answers showed or linked,
// from the copies the session kept.
func ExampleClient_ReadBlob() {
	ctx := context.Background()
	ag := client()
	id := "ses_01M34G2JF01B91B8399CD1G6YP"
	for e, err := range ag.ListEvents(ctx, id, nil) {
		if err != nil {
			log.Fatal(err)
		}
		if e.Type != agents.EventFilesKept {
			continue
		}
		var kept agents.FilesKept
		if err := e.Decode(&kept); err != nil {
			log.Fatal(err)
		}
		for _, f := range kept.Files {
			r, err := ag.ReadBlob(ctx, id, f.Blob)
			if err != nil {
				log.Fatal(err)
			}
			out, err := os.Create(filepath.Base(f.Path))
			if err != nil {
				log.Fatal(err)
			}
			_, err = io.Copy(out, r)
			_ = r.Close() // the bytes are copied; closing releases the connection
			if err != nil {
				log.Fatal(err)
			}
			if err := out.Close(); err != nil {
				log.Fatal(err)
			}
			fmt.Println("saved", f.Path, f.MediaType, f.Size, "bytes")
		}
	}
}

// Showing a file the agent wrote, which the session's machine holds while
// it runs.
func ExampleClient_ReadSessionFile() {
	r, err := client().ReadSessionFile(context.Background(), "ses_01M34G2JF01B91B8399CD1G6YP", "notes/summary.md")
	switch {
	case errors.Is(err, agents.ErrFileUnavailable):
		fmt.Println("the session's machine is not running; the file can be read once the session runs again")
		return
	case errors.Is(err, agents.ErrFileTooLarge):
		fmt.Println("the file is larger than", agents.MaxSessionFileBytes, "bytes")
		return
	case errors.Is(err, latere.ErrNotFound):
		fmt.Println("there is no such file")
		return
	case err != nil:
		log.Fatal(err)
	}
	defer r.Close()
	if _, err := io.Copy(os.Stdout, r); err != nil {
		log.Fatal(err)
	}
}

// Listing the files the agent wrote or changed and the apps it released,
// from what its tool calls recorded, each file by its place in the
// session's working directory.
func ExampleToolMeta() {
	ctx := context.Background()
	workdir := ""
	for e, err := range client().ListEvents(ctx, "ses_01M34G2JF01B91B8399CD1G6YP", nil) {
		if err != nil {
			log.Fatal(err)
		}
		switch e.Type {
		case agents.EventSessionMachine:
			var m agents.SessionMachine
			if err := e.Decode(&m); err != nil {
				log.Fatal(err)
			}
			workdir = m.Machine.Workdir
		case agents.EventToolResult:
			var r agents.ToolResult
			if err := e.Decode(&r); err != nil {
				log.Fatal(err)
			}
			if r.Meta == nil {
				continue
			}
			switch {
			case r.Meta.Size != nil:
				fmt.Println("made", strings.TrimPrefix(r.Meta.Path, workdir+"/"), *r.Meta.Size, "bytes")
			case r.Meta.Publish != nil && r.Meta.Publish.Status == agents.PublishReleased:
				fmt.Println("released", r.Meta.Publish.Name, "at", r.Meta.Publish.URL)
			}
		}
	}
}

// Finding in Storage the files a session's answers linked, for a session
// that keeps its files in a folder there. Storage answers each file's own
// media type and size.
func ExampleFolder() {
	ctx := context.Background()
	cfg, err := latere.NewConfig() // the key in LATERE_KEY
	if err != nil {
		log.Fatal(err)
	}
	ag, st := agents.New(cfg), storage.New(cfg)
	id := "ses_01M34G2JF01B91B8399CD1G6YP"
	s, err := ag.GetSession(ctx, id)
	if err != nil {
		log.Fatal(err)
	}
	if s.Folder == nil {
		fmt.Println("the session keeps its files as blobs, which ReadBlob reads")
		return
	}
	for e, err := range ag.ListEvents(ctx, id, nil) {
		if err != nil {
			log.Fatal(err)
		}
		if e.Type != agents.EventFilesKept {
			continue
		}
		var kept agents.FilesKept
		if err := e.Decode(&kept); err != nil {
			log.Fatal(err)
		}
		for _, f := range kept.Files {
			if f.Ref == "" {
				continue
			}
			path := s.Folder.Path + "/" + f.Ref
			info, err := st.StatFile(ctx, s.Folder.Owner, path)
			if err != nil {
				log.Fatal(err)
			}
			fmt.Println(f.Path, "is", path, "in Storage,", info.ContentType, info.Size, "bytes")
		}
	}
}
