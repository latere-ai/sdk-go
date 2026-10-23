// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package agents works with sessions: conversations between a person, or a
// program, and an agent on the Latere platform.
//
// A session belongs to an agent. Every message, answer, tool call and state
// change of a session is an event in the session's log, numbered by a
// sequence that starts at 1. Read the log page by page with
// Client.ListEvents, or follow it live with Client.StreamEvents, which picks
// up where it left off when the connection drops.
//
//	cfg, err := latere.NewConfig() // the key in LATERE_KEY
//	if err != nil {
//		return err
//	}
//	ag := agents.New(cfg)
//	s, err := ag.CreateSession(ctx, &agents.CreateSessionParams{
//		Agent:   "release-notes",
//		Message: "Write the release notes for v1.4.0.",
//	})
//	if err != nil {
//		return err
//	}
//	for item, err := range ag.StreamEvents(ctx, s.ID, nil) {
//		if err != nil {
//			return err
//		}
//		fmt.Println(item.Event.Seq, item.Event.Type)
//	}
//
// A session that stops to ask something, to have a tool call approved, or
// to wait for a tool your program runs, is idle with StopReason
// StopQuestion, StopToolConfirmation or StopToolResult; answer it with
// Client.AnswerQuestion, Client.ConfirmTool or Client.SendToolResult. A
// held call of the trigger tool is an agent's offer of standing work, a
// new routine or event trigger, a change, a resume or a delete, and a
// held call of the wake tool its offer to keep looking again; ToolUse.Offer
// reads either, and only the person who started the session answers it.
//
// # Files
//
// A session keeps a copy of each file a message carried, and of each
// picture or file of its machine an answer showed or linked, which an
// EventFilesKept records. Client.ReadBlob reads a copy by the digest its
// record names, after the session ends too. Client.ReadSessionFile reads
// a file of the session's working directory as it is now, while the
// session's machine runs.
//
// The results of the agent's tool calls record what else it made: a
// ToolResult's Meta names the file a write or an edit left, with its
// size, and what a publish put up, as a PublishRecord. An
// EventSessionMachine names the machine's working directory, which those
// files are inside. A session that keeps its files in Storage names its
// folder there in Session.Folder, and a kept file's Ref is its place in
// that folder.
//
// # A page an agent made
//
// An HTML file of a session, such as one the agent wrote or one an answer
// linked, is a page a program may show as a page rather than as text. Its
// scripts are the agent's, so the page runs closed off from everything of
// the program's, under PageSandbox and PagePolicy, the rules Latere Chat
// and Latere Studio both follow. A program shows the page:
//
//   - in a frame or view whose sandbox is PageSandbox and nothing more;
//   - from an opaque origin of the page's own, such as an iframe's srcdoc
//     or an address of a scheme or host that serves this page alone, never
//     the program's own origin;
//   - with PagePolicy first in the page's head, as
//     <meta http-equiv="Content-Security-Policy" content="...">, where a
//     browser reads it before anything it governs, put there without
//     running the page and after its doctype, so the page is drawn in the
//     mode its doctype asks for;
//   - where the program answers the page's address itself, with the
//     answer's Content-Security-Policy set to "sandbox " + PageSandbox +
//     "; " + PagePolicy as well;
//   - sending no referrer, by an iframe's referrerpolicy="no-referrer" or
//     an answer's Referrer-Policy: no-referrer.
//
// # Routines
//
// A routine is an instruction a person hands an agent once, which runs
// again and again with nobody waiting: on a schedule, or each time another
// app sends to its private link or an email reaches its address. It is a
// trigger written in one shape, labeled RoutineLabel with its agent's name
// and named RoutinePrefix and 20 hexadecimal digits, so every program that
// shows a person's routines reads and changes the same ones: Latere Chat's
// routines are those of its managed agent, "latere-chat", which Latere
// Studio lists too.
//
//	r, err := ag.CreateRoutine(ctx, &agents.CreateRoutineParams{
//		Agent:          "latere-chat",
//		Title:          "Weekly news",
//		Instruction:    "Summarize this week's news on climate policy.",
//		Schedule:       &agents.RoutineSchedule{Days: []time.Weekday{time.Monday}, Hour: 9, Zone: "Europe/Berlin"},
//		IdempotencyKey: key,
//	})
//
// Client.ListRoutines, Client.GetRoutine, Client.ChangeRoutine,
// Client.DeleteRoutine and Client.RunRoutine read and change them; a
// change keeps every field of the trigger it does not name. The agent must
// exist in the caller's context first: a program signed in as a person
// makes a managed agent exist with account.ApplyManagedAgent. A routine's
// runs are the sessions whose TriggerID is its TriggerID.
//
// The shape is package routine's, which names the manifest, the name a key
// makes and the checks, and imports the standard library alone, for a
// server that applies a routine without this package's client.
//
// # Watches
//
// An agent may keep standing work for a person beyond the routines they
// make: it offers a routine or a trigger that fires on events, which is
// made, as the person's, once they accept, and it sets a wake, a trigger
// that fires once and sends its note back to the session, to look again
// later. Client.ListWatches reads all of it in one shape, a Watch each:
// its kind, when it looks next, the sentence of why it exists, who set
// it, the session it came from, whether it is paused, and a wake's place
// in its row.
//
//	whys := map[string]string{} // kept between readings
//	for w, err := range ag.ListWatches(ctx, &agents.ListWatchesParams{Agent: "latere-chat", KnownWhys: whys}) {
//		if err != nil {
//			return err
//		}
//		whys[w.TriggerID] = w.Why
//		fmt.Println(w.Kind, w.Title, w.Why, w.SetBy)
//	}
//
// An offer's why is in the call it was offered with, not on its trigger,
// so the first reading reads the logs of the sessions offers came from;
// a reading given the whys of the last reads one page of triggers and one
// session per session a watch came from. Client.SuspendTrigger pauses or
// resumes one watch, keeping every other field of its trigger;
// Client.SetTriggerHold pauses every trigger of the person at once and
// leaves each one's own pause as it was, and Client.GetTriggerHold reads
// it. A Session names its pending
// wake, whether its latest turn a trigger began was TurnQuiet or
// TurnNotified, and the latest Notice its agent brought the person.
//
// # Scope
//
// The package covers sessions and their events: listing, searching,
// reading, creating, changing, forking, archiving, resuming, ending and
// deleting sessions, reading and following their logs, sending messages,
// interrupts, answers, approvals and tool results, and reading a
// session's files. It also covers triggers, which start sessions on a
// schedule or when an event arrives: applying, listing, reading, firing
// and deleting them, listing their firings and holding them all, and the
// routines and watches written as triggers. It does not cover yet
// defining agents and their versions, counting sessions, or redacting an
// event. Call those with latere.Config.Do until it does.
package agents
