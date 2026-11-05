// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"latere.ai/x/sdk-go/latere"
)

type benchRoundTripper struct{ body []byte }

func (t benchRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Body != nil {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			return nil, err
		}
		r.Body.Close()
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(t.body)),
		Request:    r,
	}, nil
}

func benchClient(b *testing.B, body []byte) *Client {
	b.Helper()
	cfg, err := latere.NewConfig(
		latere.WithBaseURL("https://api.bench.invalid"),
		latere.WithTokenSource(latere.StaticToken("key_bench")),
		latere.WithHTTPClient(&http.Client{Transport: benchRoundTripper{body: body}}),
	)
	if err != nil {
		b.Fatal(err)
	}
	return New(cfg)
}

// benchEvents builds a session log of n events: a user message, then
// tool calls with their results and the agent's replies.
func benchEvents(n int) []*Event {
	base := time.Date(2026, 3, 14, 9, 26, 53, 0, time.UTC)
	events := make([]*Event, 0, n)
	for i := range n {
		var typ EventType
		var payload any
		switch i % 4 {
		case 0:
			typ = EventUserMessage
			payload = map[string]any{"content": []ContentBlock{Text("Please summarise the changes in the working tree and run the tests.")}}
		case 1:
			typ = EventAgentToolUse
			payload = ToolUse{ToolUseID: fmt.Sprintf("tu_%05d", i), Name: "bash", Input: jsontext.Value(`{"cmd":"git status --short && go test ./...","timeout":120}`)}
		case 2:
			typ = EventToolResult
			payload = ToolResult{ToolUseID: fmt.Sprintf("tu_%05d", i-1), Content: []ContentBlock{Text(strings.Repeat("ok  \tlatere.ai/x/sdk-go/latere\t0.412s\n", 6))}}
		default:
			typ = EventAgentMessage
			payload = AgentMessage{Message: Message{Role: "assistant", Blocks: []ContentBlock{Text(strings.Repeat("Three files changed and the tests pass. ", 5))}}, StopReason: "end_turn"}
		}
		p, err := json.Marshal(payload)
		if err != nil {
			panic(err)
		}
		events = append(events, &Event{
			ID:        fmt.Sprintf("evt_01M34G2JF1P53NNG7DN%05d", i),
			Seq:       int64(i + 1),
			SessionID: "ses_01M34G2JF1P53NNG7DN08D5QAG",
			Type:      typ,
			Time:      base.Add(time.Duration(i) * time.Second),
			Turn:      i/4 + 1,
			Step:      i % 4,
			Payload:   p,
		})
	}
	return events
}

var benchEventSizes = []struct {
	name   string
	events int
}{{"small", 2}, {"medium", 24}, {"large", 450}}

// BenchmarkJSONEventsPageDecode stands for Client.ListEventsPage: a page
// of a session's log decoded through Config.Do, each event keeping its
// raw JSON.
func BenchmarkJSONEventsPageDecode(b *testing.B) {
	for _, s := range benchEventSizes {
		b.Run(s.name, func(b *testing.B) {
			body, err := json.Marshal(latere.Page[*Event]{Items: benchEvents(s.events), NextCursor: "cur_01M34G2JF1P53NNG7DN08D5QAG"})
			if err != nil {
				b.Fatal(err)
			}
			c := benchClient(b, body)
			ctx := b.Context()
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			for b.Loop() {
				page, err := c.ListEventsPage(ctx, "ses_01M34G2JF1P53NNG7DN08D5QAG", &ListEventsParams{Limit: s.events})
				if err != nil {
					b.Fatal(err)
				}
				if len(page.Items) != s.events {
					b.Fatalf("decoded %d events, want %d", len(page.Items), s.events)
				}
			}
		})
	}
}

// BenchmarkJSONStreamFrameDecode stands for the decoding of every frame
// of StreamEvents: events for the log and deltas for live text.
func BenchmarkJSONStreamFrameDecode(b *testing.B) {
	events := benchEvents(4)
	var frames []latere.Frame
	size := 0
	for _, e := range events {
		data, err := json.Marshal(e)
		if err != nil {
			b.Fatal(err)
		}
		frames = append(frames, latere.Frame{ID: e.ID, Event: string(e.Type), Data: data})
	}
	for i := range 8 {
		data, err := json.Marshal(Delta{Turn: 1, Step: 1, Block: 0, Kind: DeltaText, Text: fmt.Sprintf("part %d of the reply ", i)})
		if err != nil {
			b.Fatal(err)
		}
		frames = append(frames, latere.Frame{Event: "delta", Data: data})
	}
	for _, f := range frames {
		size += len(f.Data)
	}
	b.ReportAllocs()
	b.SetBytes(int64(size))
	for b.Loop() {
		for _, f := range frames {
			if _, err := decodeFrame(f); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// BenchmarkJSONEventPayloadDecode stands for Event.Decode, reading the
// typed payload of a tool result from a log entry.
func BenchmarkJSONEventPayloadDecode(b *testing.B) {
	for _, s := range []struct {
		name  string
		lines int
	}{{"small", 1}, {"medium", 100}, {"large", 4000}} {
		b.Run(s.name, func(b *testing.B) {
			p, err := json.Marshal(ToolResult{ToolUseID: "tu_00001", Content: []ContentBlock{Text(strings.Repeat("ok  \tlatere.ai/x/sdk-go/latere\t0.412s\n", s.lines))}})
			if err != nil {
				b.Fatal(err)
			}
			e := &Event{ID: "evt_01M34G2JF1P53NNG7DN00001", Type: EventToolResult, Payload: p}
			b.ReportAllocs()
			b.SetBytes(int64(len(p)))
			for b.Loop() {
				var r ToolResult
				if err := e.Decode(&r); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
