// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"latere.ai/x/sdk-go/latere"
)

const sessions = `{"items":[
{"id":"ses_1","agent":{"id":"a1","name":"notes","version":1},"title":"First","status":"idle","stop_reason":"end_turn","updated_at":"2026-09-22T12:06:00Z"},
{"id":"ses_2","agent":{"id":"a1","name":"notes","version":1},"title":"Second","status":"running","updated_at":"2026-09-22T12:07:00Z"}]}`

// serve starts an API that answers with h and returns the options that
// point the program at it.
func serve(t *testing.T, h http.HandlerFunc) []latere.Option {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return []latere.Option{latere.WithBaseURL(srv.URL), latere.WithTokenSource(latere.StaticToken("key_test"))}
}

func TestRunPrintsTheSessionsUpToTheLimit(t *testing.T) {
	var query string
	opts := serve(t, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, sessions)
	})
	var out bytes.Buffer
	if err := run(context.Background(), &out, "notes", 1, opts...); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.HasPrefix(got, "SESSION") || !strings.Contains(got, "ses_1") || !strings.Contains(got, "idle (end_turn)") {
		t.Errorf("output = %q, want the header and the first session with its stop reason", got)
	}
	if strings.Contains(got, "ses_2") {
		t.Errorf("output = %q, want it to stop at the limit of 1", got)
	}
	if !strings.Contains(query, "agent=notes") {
		t.Errorf("query = %q, want the agent filter", query)
	}
}

func TestRunNamesTheMissingKey(t *testing.T) {
	t.Setenv("LATERE_KEY", "")
	err := run(context.Background(), &bytes.Buffer{}, "", 1)
	if err == nil || !strings.Contains(err.Error(), "LATERE_KEY") {
		t.Errorf("err = %v, want the instruction to set LATERE_KEY", err)
	}
}

func TestRunReturnsTheAPIsError(t *testing.T) {
	opts := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"error":{"code":"forbidden","message":"No."}}`)
	})
	if err := run(context.Background(), &bytes.Buffer{}, "", 1, opts...); err == nil {
		t.Error("run answered no error to a refusal")
	}
}

// failing is a writer whose every write fails.
type failing struct{}

func (failing) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestRunReturnsAWriteError(t *testing.T) {
	opts := serve(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, sessions)
	})
	// The table is buffered, so the failure surfaces when it is flushed.
	if err := run(context.Background(), failing{}, "", 2, opts...); err == nil {
		t.Error("run answered no error to a writer that fails")
	}
}

func TestRunStopsOnAnErrorMidList(t *testing.T) {
	opts := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	var out bytes.Buffer
	if err := run(context.Background(), &out, "", 5, opts...); err == nil {
		t.Error("run answered no error to a failing list")
	}
}

func TestCommand(t *testing.T) {
	opts := serve(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, sessions)
	})
	var out, errOut bytes.Buffer
	if code := command(context.Background(), []string{"-n", "2"}, &out, &errOut, opts...); code != 0 {
		t.Fatalf("exit status = %d (%s), want 0", code, errOut.String())
	}
	if !strings.Contains(out.String(), "ses_2") {
		t.Errorf("output = %q, want both sessions", out.String())
	}

	out.Reset()
	if code := command(context.Background(), []string{"-n", "x"}, &out, &errOut); code != 2 {
		t.Errorf("exit status = %d for a bad flag, want 2", code)
	}

	t.Setenv("LATERE_KEY", "")
	errOut.Reset()
	if code := command(context.Background(), nil, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "LATERE_KEY") {
		t.Errorf("exit status = %d, stderr = %q; want 1 and the instruction to set LATERE_KEY", code, errOut.String())
	}
}

func TestMainExitsWithTheCommandsStatus(t *testing.T) {
	t.Setenv("LATERE_KEY", "")
	oldArgs, oldExit := os.Args, exit
	t.Cleanup(func() { os.Args, exit = oldArgs, oldExit })
	os.Args = []string{"list-sessions"}
	code := -1
	exit = func(c int) { code = c }
	main()
	if code != 1 {
		t.Errorf("exit status = %d without a key, want 1", code)
	}
}
