// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Command list-sessions prints your sessions with agents, newest first.
//
// Create a key under Keys in the console, then run:
//
//	LATERE_KEY=... go run ./examples/list-sessions -agent latere-chat
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"text/tabwriter"

	"latere.ai/x/sdk-go/agents"
	"latere.ai/x/sdk-go/latere"
)

func main() {
	agent := flag.String("agent", "", "list only this agent's sessions, by its name or id")
	limit := flag.Int("n", 20, "how many sessions to print")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, *agent, *limit); err != nil {
		fmt.Fprintln(os.Stderr, "list-sessions:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, agent string, limit int) error {
	cfg, err := latere.NewConfig(latere.WithUserAgent("list-sessions-example/1.0"))
	if errors.Is(err, latere.ErrNoCredentials) {
		return errors.New("set LATERE_KEY to a key created in the console")
	}
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "SESSION\tSTATUS\tUPDATED\tTITLE")
	n := 0
	for s, err := range agents.New(cfg).Sessions.List(ctx, &agents.ListSessionsParams{Agent: agent}) {
		if err != nil {
			return err
		}
		status := string(s.Status)
		if s.StopReason != "" {
			status += " (" + string(s.StopReason) + ")"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", s.ID, status, s.UpdatedAt.Local().Format("2006-01-02 15:04"), s.Title)
		if n++; n >= limit {
			break
		}
	}
	return w.Flush()
}
