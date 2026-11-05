// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package llm

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"latere.ai/x/sdk-go/latere"
)

// benchRoundTripper answers each request with a fixed body, in process.
type benchRoundTripper struct {
	contentType string
	body        []byte
}

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
		Header:     http.Header{"Content-Type": {t.contentType}},
		Body:       io.NopCloser(bytes.NewReader(t.body)),
		Request:    r,
	}, nil
}

func benchClient(b *testing.B, contentType string, body []byte) *Client {
	b.Helper()
	cfg, err := latere.NewConfig(
		latere.WithBaseURL("https://lux.bench.invalid"),
		latere.WithTokenSource(latere.StaticToken("key_bench")),
		latere.WithHTTPClient(&http.Client{Transport: benchRoundTripper{contentType: contentType, body: body}}),
	)
	if err != nil {
		b.Fatal(err)
	}
	return New(cfg)
}

func benchResponse(blocks int) *Response {
	cost := int64(2150)
	resp := &Response{
		ID:         "msg_01M34G2JF1P53NNG7DN08D5QAG",
		Model:      "claude-sonnet-5-5",
		StopReason: "end_turn",
		Usage:      Usage{InputTokens: 2400, OutputTokens: 640, CostMicroUSD: &cost},
	}
	for i := range blocks {
		resp.Blocks = append(resp.Blocks, Block{Type: BlockText, Text: fmt.Sprintf("Section %d. %s", i, strings.Repeat("The change moves the retry logic into the shared client. ", 6))})
	}
	return resp
}

func benchRequest(turns int) *Request {
	req := &Request{Model: "claude-sonnet-5-5"}
	for i := range turns {
		req.Messages = append(req.Messages,
			Message{Role: RoleUser, Blocks: []Block{{Type: BlockText, Text: fmt.Sprintf("Step %d: summarise the change. %s", i, strings.Repeat("Keep it short. ", 4))}}},
			Message{Role: RoleAssistant, Blocks: []Block{{Type: BlockText, Text: strings.Repeat("I will inspect the working tree first. ", 5)}}},
		)
	}
	return req
}

// BenchmarkJSONGenerateDecode stands for Client.Generate: encoding the
// request and decoding the complete answer.
func BenchmarkJSONGenerateDecode(b *testing.B) {
	for _, s := range []struct {
		name          string
		blocks, turns int
	}{{"small", 1, 1}, {"medium", 20, 12}, {"large", 400, 250}} {
		b.Run(s.name, func(b *testing.B) {
			body, err := json.Marshal(benchResponse(s.blocks))
			if err != nil {
				b.Fatal(err)
			}
			c := benchClient(b, "application/json", body)
			req := benchRequest(s.turns)
			ctx := b.Context()
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			for b.Loop() {
				res, err := c.Generate(ctx, req)
				if err != nil {
					b.Fatal(err)
				}
				if len(res.Blocks) != s.blocks {
					b.Fatalf("decoded %d blocks", len(res.Blocks))
				}
			}
		})
	}
}

// benchStream builds the frames of a streamed answer of n text deltas.
func benchStream(n int) []byte {
	var w bytes.Buffer
	frame := func(event, data string) { fmt.Fprintf(&w, "event: %s\ndata: %s\n\n", event, data) }
	frame("message_start", `{"type":"message_start","id":"msg_01M34G2JF1P53NNG7DN08D5QAG","model":"claude-sonnet-5-5","index":0,"usage":{"input_tokens":2400,"output_tokens":0}}`)
	frame("block_start", `{"type":"block_start","index":0,"block":{"type":"text"}}`)
	for i := range n {
		d, err := json.Marshal(Event{Type: EventTextDelta, Index: 0, Delta: fmt.Sprintf("word%d the retry logic ", i)})
		if err != nil {
			panic(err)
		}
		frame("text_delta", string(d))
	}
	frame("block_stop", `{"type":"block_stop","index":0}`)
	frame("message_delta", `{"type":"message_delta","index":0,"stop_reason":"end_turn","usage":{"input_tokens":0,"output_tokens":640}}`)
	frame("message_stop", `{"type":"message_stop","index":0}`)
	return w.Bytes()
}

// BenchmarkJSONStreamEventsDecode stands for reading a streamed answer:
// every frame's JSON is decoded into an Event.
func BenchmarkJSONStreamEventsDecode(b *testing.B) {
	for _, s := range []struct {
		name   string
		deltas int
	}{{"small", 4}, {"medium", 100}, {"large", 2000}} {
		b.Run(s.name, func(b *testing.B) {
			body := benchStream(s.deltas)
			c := benchClient(b, "text/event-stream", body)
			req := benchRequest(1)
			ctx := b.Context()
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			for b.Loop() {
				stream, err := c.Stream(ctx, req)
				if err != nil {
					b.Fatal(err)
				}
				n := 0
				for _, err := range stream.Events() {
					if err != nil {
						b.Fatal(err)
					}
					n++
				}
				if err := stream.Close(); err != nil {
					b.Fatal(err)
				}
				if n != s.deltas+5 {
					b.Fatalf("read %d events, want %d", n, s.deltas+5)
				}
			}
		})
	}
}
