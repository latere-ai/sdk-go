// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"latere.ai/x/sdk-go/agents"
	"latere.ai/x/sdk-go/latere"
)

func streamClient(t *testing.T, h http.HandlerFunc) *agents.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	cfg, err := latere.NewConfig(latere.WithBaseURL(srv.URL), latere.WithTokenSource(latere.StaticToken("key_test")), latere.WithBackoff(time.Millisecond, time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	return agents.New(cfg)
}

func frame(seq int, typ, payload string) string {
	return fmt.Sprintf("id: %d\nevent: %s\ndata: {\"id\":\"evt_%d\",\"seq\":%d,\"session_id\":\"ses_1\",\"type\":%q,\"time\":\"2026-09-22T12:05:00Z\",\"payload\":%s}\n\n", seq, typ, seq, seq, typ, payload)
}

func TestStreamFollowsUntilTheSessionEnds(t *testing.T) {
	var queries, resumes []string
	s := streamClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/agents/sessions/ses_1/stream" {
			http.NotFound(w, r)
			return
		}
		queries = append(queries, r.URL.RawQuery)
		resumes = append(resumes, r.Header.Get("Last-Event-ID"))
		w.Header().Set("Content-Type", "text/event-stream")
		if r.Header.Get("Last-Event-ID") == "" {
			fmt.Fprint(w, frame(2, "user.message", `{"content":[{"type":"text","text":"Hi"}]}`))
			fmt.Fprint(w, "event: delta\ndata: {\"turn\":1,\"step\":1,\"block\":0,\"kind\":\"text\",\"text\":\"He\"}\n\n")
			fmt.Fprint(w, "event: delta\ndata: {\"subagent\":\"sub_1\",\"turn\":1,\"step\":1,\"block\":0,\"kind\":\"text\",\"text\":\"Lo\"}\n\n")
			fmt.Fprint(w, frame(3, "session.status", `{"status":"running"}`))
			return // the connection drops mid-session
		}
		fmt.Fprint(w, strings.Replace(frame(4, "agent.message", `{}`), `"seq":4,`, `"seq":4,"subagent":"sub_1",`, 1))
		fmt.Fprint(w, frame(5, "session.status", `{"status":"ended","stop_reason":"completed"}`))
		fmt.Fprint(w, frame(6, "never.read", `{}`))
	})
	var got []string
	for item, err := range s.StreamEvents(t.Context(), "ses_1", &agents.StreamEventsParams{FromSeq: 2, Deltas: true}) {
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case item.Delta != nil:
			got = append(got, "delta:"+item.Delta.Subagent+":"+item.Delta.Text)
		case item.Event != nil:
			got = append(got, fmt.Sprintf("%d:%s:%s", item.Event.Seq, item.Event.Type, item.Event.Subagent))
		}
	}
	if strings.Join(got, ",") != "2:user.message:,delta::He,delta:sub_1:Lo,3:session.status:,4:agent.message:sub_1,5:session.status:" {
		t.Errorf("streamed %v", got)
	}
	if queries[0] != "deltas=1&from_seq=2" || strings.Join(resumes, ",") != ",3" {
		t.Errorf("queries %q, resumed after %q", queries, resumes)
	}
}

func TestStreamReportsBadFrames(t *testing.T) {
	s := streamClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "id: 1\nevent: user.message\ndata: {broken\n\n")
	})
	for _, err := range s.StreamEvents(t.Context(), "ses_1", nil) {
		if err == nil || !strings.Contains(err.Error(), "decoding") {
			t.Errorf("a broken event: %v", err)
		}
	}
	s = streamClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: delta\ndata: {broken\n\n")
	})
	for _, err := range s.StreamEvents(t.Context(), "ses_1", nil) {
		if err == nil || !strings.Contains(err.Error(), "delta") {
			t.Errorf("a broken delta: %v", err)
		}
	}
	for _, err := range s.StreamEvents(t.Context(), "", nil) {
		if err == nil {
			t.Error("a stream of no id started")
		}
	}
	s = streamClient(t, func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	for _, err := range s.StreamEvents(t.Context(), "ses_1", nil) {
		if err == nil {
			t.Error("a refused stream yielded no error")
		}
	}
}

func TestStreamStopsAtOnceOnABadFrame(t *testing.T) {
	// A broken event, then a connection held open with keepalives.
	s := streamClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "id: 1\nevent: user.message\ndata: {broken\n\n")
		w.(http.Flusher).Flush()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(100 * time.Millisecond):
				fmt.Fprint(w, ": keepalive\n\n")
				w.(http.Flusher).Flush()
			}
		}
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range s.StreamEvents(context.Background(), "ses_1", &agents.StreamEventsParams{FromSeq: 1}) {
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("StreamEvents did not return after a frame it could not decode")
	}
}

func TestStreamSendsTheFirstSequence(t *testing.T) {
	var query string
	s := streamClient(t, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, frame(1, "session.status", `{"status":"ended","stop_reason":"completed"}`))
	})
	for _, err := range s.StreamEvents(t.Context(), "ses_1", &agents.StreamEventsParams{FromSeq: 1}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if query != "from_seq=1" {
		t.Errorf("FromSeq 1 sent %q", query)
	}
}
