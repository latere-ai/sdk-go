// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package agents works with sessions: conversations between a person, or a
// program, and an agent on the Latere platform.
//
// A session belongs to an agent. Every message, answer, tool call and state
// change of a session is an event in the session's log, numbered by a
// sequence that starts at 1. Read the log page by page with
// SessionsService.Events, or follow it live with SessionsService.Stream,
// which picks up where it left off when the connection drops.
//
//	cfg, err := latere.NewConfig(latere.WithKey(os.Getenv("LATERE_KEY")))
//	if err != nil {
//		return err
//	}
//	sessions := agents.New(cfg).Sessions
//	s, err := sessions.Create(ctx, &agents.CreateSessionParams{
//		Agent:   "release-notes",
//		Message: "Write the release notes for v1.4.0.",
//	})
//	if err != nil {
//		return err
//	}
//	for item, err := range sessions.Stream(ctx, s.ID, nil) {
//		if err != nil {
//			return err
//		}
//		fmt.Println(item.Event.Seq, item.Event.Type)
//	}
//
// A session that stops to ask something, or to have a tool call approved,
// is idle with StopReason StopQuestion or StopToolConfirmation; answer it
// with SessionsService.Answer or SessionsService.ConfirmTool.
package agents
