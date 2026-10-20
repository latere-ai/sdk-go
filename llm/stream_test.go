// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package llm_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"latere.ai/x/sdk-go/latere"
	"latere.ai/x/sdk-go/llm"
)

// sseFrame writes one server-sent event of the Lux dialect.
func sseFrame(w io.Writer, event, data string) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
}

func TestStreamYieldsTheAnswerInOrder(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		var req llm.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if !req.Stream || r.Header.Get("Accept") != "text/event-stream" || r.Header.Get("Authorization") != "Bearer pat_test" {
			t.Errorf("request: stream %v, headers %v", req.Stream, r.Header)
		}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Lux-Loss", "top_k")
		w.Header().Set("Lux-Request-Id", "req_4")
		sseFrame(w, "message_start", `{"type":"message_start","id":"msg_1","model":"m","index":0,"usage":{"input_tokens":9,"output_tokens":0}}`)
		sseFrame(w, "ping", `{}`) // a kind this client does not know is skipped
		sseFrame(w, "block_start", `{"type":"block_start","index":0,"block":{"type":"tool_use","tool_use":{"id":"tu_1","name":"bash"}}}`)
		sseFrame(w, "args_delta", `{"type":"args_delta","index":0,"delta":"{\"cmd\":"}`)
		sseFrame(w, "args_delta", `{"index":0,"delta":"\"ls\"}"}`) // the type comes from the event name
		sseFrame(w, "block_stop", `{"type":"block_stop","index":0}`)
		sseFrame(w, "message_delta", `{"type":"message_delta","index":0,"stop_reason":"tool_use","usage":{"input_tokens":0,"output_tokens":7}}`)
		sseFrame(w, "message_stop", `{"type":"message_stop","index":0}`)
		sseFrame(w, "text_delta", `{"type":"text_delta","index":1,"delta":"never read"}`)
	})
	stream, err := c.Stream(t.Context(), hello())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := stream.Close(); err != nil {
			t.Error(err)
		}
	}()
	var types []llm.EventType
	var args strings.Builder
	var stop llm.StopReason
	for ev, err := range stream.Events() {
		if err != nil {
			t.Fatal(err)
		}
		types = append(types, ev.Type)
		switch ev.Type {
		case llm.EventArgsDelta:
			args.WriteString(ev.Delta)
		case llm.EventMessageDelta:
			stop = ev.StopReason
		}
	}
	want := []llm.EventType{llm.EventMessageStart, llm.EventBlockStart, llm.EventArgsDelta, llm.EventArgsDelta, llm.EventBlockStop, llm.EventMessageDelta, llm.EventMessageStop}
	if !slices.Equal(types, want) {
		t.Errorf("events = %v, want %v", types, want)
	}
	if args.String() != `{"cmd":"ls"}` || stop != llm.StopToolUse {
		t.Errorf("args = %q, stop = %q", args.String(), stop)
	}
	if !slices.Equal(stream.Loss(), []string{"top_k"}) || stream.RequestID() != "req_4" {
		t.Errorf("loss = %v, request = %q", stream.Loss(), stream.RequestID())
	}
}

func TestStreamReportsAFailurePartway(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Lux-Request-Id", "req_5")
		sseFrame(w, "message_start", `{"type":"message_start","index":0}`)
		sseFrame(w, "error", `{"error":{"code":"upstream_error","message":"The model's provider failed.","details":{"detail":"stream reset"}}}`)
	})
	stream, err := c.Stream(t.Context(), hello())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	var got error
	n := 0
	for _, err := range stream.Events() {
		if err != nil {
			got = err
			break
		}
		n++
	}
	var se *llm.StreamError
	if !errors.As(got, &se) || se.Code != "upstream_error" || se.Detail != "stream reset" || se.RequestID != "req_5" || n != 1 {
		t.Fatalf("err = %v after %d events", got, n)
	}
	if want := "llm: the stream failed (upstream_error): The model's provider failed. (stream reset) (request req_5)"; se.Error() != want {
		t.Errorf("Error() = %q, want %q", se.Error(), want)
	}
}

func TestStreamErrorFrames(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		want       llm.StreamError
	}{
		{"an older frame names its code type", `{"error":{"type":"overloaded_error","message":"busy","details":{"request_id":"req_x"}}}`, llm.StreamError{Code: "overloaded_error", Message: "busy", RequestID: "req_x"}},
		{"data that is not the envelope is the message", "upstream went away\n", llm.StreamError{Message: "upstream went away", RequestID: "req_6"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Lux-Request-Id", "req_6")
				fmt.Fprintf(w, "event: error\ndata: %s\n\n", strings.TrimSpace(tc.data))
			})
			stream, err := c.Stream(t.Context(), hello())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = stream.Close() }()
			for _, err := range stream.Events() {
				var se *llm.StreamError
				if !errors.As(err, &se) || *se != tc.want {
					t.Fatalf("err = %#v, want %#v", err, tc.want)
				}
			}
		})
	}
}

// A stream cut off before message_stop is incomplete, and says so rather
// than ending as if the answer were whole.
func TestStreamCutOffIsAnError(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sseFrame(w, "message_start", `{"type":"message_start","index":0}`)
		sseFrame(w, "text_delta", `{"type":"text_delta","index":0,"delta":"Hel"}`)
	})
	stream, err := c.Stream(t.Context(), hello())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	var got error
	for _, err := range stream.Events() {
		got = err
	}
	if !errors.Is(got, io.ErrUnexpectedEOF) {
		t.Errorf("err = %v, want io.ErrUnexpectedEOF", got)
	}
}

// Stopping the loop closes the connection at once, even against a server
// that keeps it open with keepalives, as a live stream does.
func TestStreamStopsWithTheLoop(t *testing.T) {
	closed := make(chan struct{})
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sseFrame(w, "message_start", `{"type":"message_start","index":0}`)
		sseFrame(w, "text_delta", `{"type":"text_delta","index":0,"delta":"Hel"}`)
		w.(http.Flusher).Flush()
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-r.Context().Done():
				close(closed)
				return
			case <-tick.C:
				fmt.Fprint(w, ": keepalive\n\n")
				w.(http.Flusher).Flush()
			}
		}
	})
	stream, err := c.Stream(t.Context(), hello())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	for ev, err := range stream.Events() {
		if err != nil {
			t.Fatal(err)
		}
		if ev.Type == llm.EventTextDelta {
			break
		}
	}
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the server still holds the connection after the loop stopped")
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("stopping took %v", took)
	}
	if err := stream.Close(); err != nil {
		t.Errorf("Close after the loop: %v", err)
	}
}

func TestStreamRefusedBeforeAnyEvent(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		drain(r)
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"code":"rate_limited","message":"Too many requests."}}`)
	})
	if _, err := c.Stream(t.Context(), hello()); !errors.Is(err, latere.ErrRateLimited) {
		t.Errorf("err = %v, want ErrRateLimited", err)
	}
}

func TestStreamThatIsNotAnEventStream(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{}`)
	})
	if _, err := c.Stream(t.Context(), hello()); err == nil || !strings.Contains(err.Error(), "not an event stream") {
		t.Errorf("err = %v", err)
	}
}

func TestStreamWithABrokenEvent(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sseFrame(w, "text_delta", `{"delta":`)
	})
	stream, err := c.Stream(t.Context(), hello())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	for _, err := range stream.Events() {
		if err == nil || !strings.Contains(err.Error(), "decoding a text_delta event") {
			t.Errorf("err = %v", err)
		}
	}
}

// NewStream wraps events from another source; the stream is closed once,
// by the loop's end or by Close, whichever comes first.
func TestNewStream(t *testing.T) {
	closes := 0
	events := func(yield func(llm.Event, error) bool) {
		for _, typ := range []llm.EventType{llm.EventMessageStart, llm.EventMessageStop} {
			if !yield(llm.Event{Type: typ}, nil) {
				return
			}
		}
	}
	s := llm.NewStream(events, []string{"top_k"}, "req_7", func() error { closes++; return nil })
	var got []llm.EventType
	for ev, err := range s.Events() {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, ev.Type)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || closes != 1 || s.RequestID() != "req_7" || s.Loss()[0] != "top_k" {
		t.Errorf("events %v, closes %d", got, closes)
	}
	plain := llm.NewStream(events, nil, "", nil)
	if err := plain.Close(); err != nil {
		t.Errorf("a stream with nothing to close: %v", err)
	}
}
