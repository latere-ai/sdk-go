// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package wire_test

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"

	"latere.ai/x/sdk-go/llm/wire"
)

var wireSizes = []struct {
	name  string
	turns int
}{{"small", 1}, {"medium", 12}, {"large", 250}}

// wireRequest builds a conversation of about turns exchanges, with a tool
// call and its result in each.
func wireRequest(turns int) *wire.Request {
	maxTokens := int64(4096)
	schema := jsontext.Value(`{"type":"object","properties":{"cmd":{"type":"string","description":"The shell command to run."},"timeout":{"type":"integer"}},"required":["cmd"]}`)
	req := &wire.Request{
		Model:     "claude-sonnet-5-5",
		System:    []wire.Block{{Type: wire.BlockText, Text: "You are a careful assistant working in the user's repository."}},
		Tools:     []wire.Tool{{Name: "bash", Description: "Run a shell command and return its output.", InputSchema: schema}},
		MaxTokens: &maxTokens,
		Stream:    true,
		UserID:    "usr_01HZX3K8F7",
	}
	for i := range turns {
		req.Messages = append(req.Messages,
			wire.Message{Role: wire.RoleUser, Blocks: []wire.Block{{Type: wire.BlockText, Text: fmt.Sprintf("Step %d: list the files that changed and summarise why. %s", i, strings.Repeat("Keep it short. ", 4))}}},
			wire.Message{Role: wire.RoleAssistant, Blocks: []wire.Block{
				{Type: wire.BlockText, Text: strings.Repeat("I will inspect the working tree first. ", 5)},
				{Type: wire.BlockToolUse, ToolUse: &wire.ToolUse{ID: fmt.Sprintf("tu_%04d", i), Name: "bash", Args: jsontext.Value(`{"cmd":"git status --short && git diff --stat","timeout":30}`)}},
			}},
			wire.Message{Role: wire.RoleUser, Blocks: []wire.Block{
				{Type: wire.BlockToolResult, ToolResult: &wire.ToolResult{ToolUseID: fmt.Sprintf("tu_%04d", i), Blocks: []wire.Block{{Type: wire.BlockText, Text: strings.Repeat(" M internal/server/handler.go\n 3 files changed, 41 insertions(+), 7 deletions(-)\n", 4)}}}},
			}},
		)
	}
	return req
}

func wireResponse(blocks int) *wire.Response {
	cacheRead := int64(1800)
	cost := int64(2150)
	resp := &wire.Response{
		ID:         "msg_01M34G2JF1P53NNG7DN08D5QAG",
		Model:      "claude-sonnet-5-5",
		StopReason: wire.StopEndTurn,
		Usage:      wire.Usage{InputTokens: 2400, OutputTokens: 640, CacheReadInputTokens: &cacheRead, CostMicroUSD: &cost},
	}
	for i := range blocks {
		resp.Blocks = append(resp.Blocks, wire.Block{Type: wire.BlockText, Text: fmt.Sprintf("Section %d. %s", i, strings.Repeat("The change moves the retry logic into the shared client. ", 6))})
	}
	return resp
}

// BenchmarkJSONRequestMarshal stands for the body the client sends for a
// model call, which grows with the conversation.
func BenchmarkJSONRequestMarshal(b *testing.B) {
	for _, s := range wireSizes {
		b.Run(s.name, func(b *testing.B) {
			req := wireRequest(s.turns)
			enc, err := json.Marshal(req, json.Deterministic(true))
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(enc)))
			for b.Loop() {
				if _, err := json.Marshal(req, json.Deterministic(true)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkJSONRequestUnmarshal stands for the gateway reading the same
// body with the shared codec.
func BenchmarkJSONRequestUnmarshal(b *testing.B) {
	for _, s := range wireSizes {
		b.Run(s.name, func(b *testing.B) {
			enc, err := json.Marshal(wireRequest(s.turns))
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(enc)))
			for b.Loop() {
				var req wire.Request
				if err := json.Unmarshal(enc, &req); err != nil {
					b.Fatal(err)
				}
				if len(req.Messages) != 3*s.turns {
					b.Fatalf("decoded %d messages", len(req.Messages))
				}
			}
		})
	}
}

// BenchmarkJSONResponseUnmarshal stands for a complete model answer.
func BenchmarkJSONResponseUnmarshal(b *testing.B) {
	for _, s := range []struct {
		name   string
		blocks int
	}{{"small", 1}, {"medium", 20}, {"large", 400}} {
		b.Run(s.name, func(b *testing.B) {
			enc, err := json.Marshal(wireResponse(s.blocks))
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(enc)))
			for b.Loop() {
				var resp wire.Response
				if err := json.Unmarshal(enc, &resp); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkJSONEventUnmarshal stands for one text_delta frame of a
// streamed answer, the most frequent message of the dialect.
func BenchmarkJSONEventUnmarshal(b *testing.B) {
	enc, err := json.Marshal(wire.Event{Type: wire.EventTextDelta, Index: 0, Delta: "The change moves the retry logic "})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(enc)))
	for b.Loop() {
		var ev wire.Event
		if err := json.Unmarshal(enc, &ev); err != nil {
			b.Fatal(err)
		}
	}
}
