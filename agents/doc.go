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
// Client.AnswerQuestion, Client.ConfirmTool or Client.SendToolResult.
//
// # Scope
//
// The package covers sessions and their events: listing, reading,
// creating, forking, archiving, ending and deleting sessions, reading and
// following their logs, and sending messages, interrupts, answers,
// approvals and tool results. It does not cover yet defining agents and
// their versions, triggers, searching and counting sessions, a session's
// files, changing a session's model, resuming a session, or redacting an
// event. Call those with latere.Config.Do until it does.
package agents
