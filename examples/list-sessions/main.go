// SPDX-FileCopyrightText: 2026 Latere AI
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
	"io"
	"os"
	"os/signal"
	"text/tabwriter"

	"latere.ai/x/sdk-go/agents"
	"latere.ai/x/sdk-go/latere"
)

// exit ends the program; a test replaces it.
var exit = os.Exit

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := command(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	exit(code)
}

// command parses the flags in args, prints the sessions to stdout and any
// error to stderr, and returns the program's exit status.
func command(ctx context.Context, args []string, stdout, stderr io.Writer, opts ...latere.Option) int {
	fs := flag.NewFlagSet("list-sessions", flag.ContinueOnError)
	fs.SetOutput(stderr)
	agent := fs.String("agent", "", "list only this agent's sessions, by its name or id")
	limit := fs.Int("n", 20, "how many sessions to print")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := run(ctx, stdout, *agent, *limit, opts...); err != nil {
		// A failed write to stderr has nowhere to be reported.
		_, _ = fmt.Fprintln(stderr, "list-sessions:", err)
		return 1
	}
	return 0
}

func run(ctx context.Context, out io.Writer, agent string, limit int, opts ...latere.Option) error {
	cfg, err := latere.NewConfig(append([]latere.Option{latere.WithUserAgent("list-sessions-example/1.0")}, opts...)...)
	if errors.Is(err, latere.ErrNoCredentials) {
		return errors.New("set LATERE_KEY to a key created in the console")
	}
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	// The table is buffered, and Flush returns the first write error.
	_, _ = fmt.Fprintln(w, "SESSION\tSTATUS\tUPDATED\tTITLE")
	n := 0
	params := &agents.ListSessionsParams{Agent: agent, Limit: min(limit, 500)}
	for s, err := range agents.New(cfg).ListSessions(ctx, params) {
		if err != nil {
			return err
		}
		status := string(s.Status)
		if s.StopReason != "" {
			status += " (" + string(s.StopReason) + ")"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", s.ID, status, s.UpdatedAt.Local().Format("2006-01-02 15:04"), s.Title)
		if n++; n >= limit {
			break
		}
	}
	return w.Flush()
}
