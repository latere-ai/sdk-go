// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package wire_test

import (
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"

	"latere.ai/x/sdk-go/llm/wire"
)

// hi is a user turn of one text block.
var hi = wire.Message{Role: wire.RoleUser, Blocks: []wire.Block{{Type: wire.BlockText, Text: "Hi"}}}

// The wire format is a contract with the service: field names, their order
// and omitempty rules decide the bytes sent. This request sets every field,
// so a renamed, reordered or retagged field changes the golden.
func TestRequestWireFormat(t *testing.T) {
	maxTokens, topK := int64(256), int64(40)
	temperature, topP := 0.5, 0.9
	req := wire.Request{
		Model:       "anthropic/claude-sonnet-4.5",
		System:      []wire.Block{{Type: wire.BlockText, Text: "Be brief.", CacheHint: true}},
		Messages:    []wire.Message{hi, {Role: wire.RoleAssistant, Blocks: []wire.Block{{Type: wire.BlockToolUse, ToolUse: &wire.ToolUse{ID: "tu_1", Name: "bash", Args: json.RawMessage(`{"cmd":"ls"}`)}}}}},
		Tools:       []wire.Tool{{Name: "bash", Description: "run", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		ServerTools: []wire.ServerTool{{Type: "web_search", Name: "search", Config: json.RawMessage(`{}`)}},
		ToolChoice:  &wire.ToolChoice{Mode: wire.ToolChoiceTool, Name: "bash", DisableParallel: true},
		MaxTokens:   &maxTokens, Temperature: &temperature, TopP: &topP, TopK: &topK,
		StopSequences:   []string{"END"},
		Stream:          true,
		Reasoning:       &wire.Reasoning{Effort: wire.EffortHigh},
		Schema:          &wire.ResponseSchema{Name: "answer", Schema: json.RawMessage(`{"type":"object"}`), Strict: true},
		WebSearch:       &wire.WebSearch{ContextSize: "low"},
		UserID:          "u_1",
		CacheKey:        "k",
		LogProbs:        true,
		TopLogProbs:     2,
		ReasoningReplay: true,
	}
	got, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"model":"anthropic/claude-sonnet-4.5","system":[{"type":"text","text":"Be brief.","cache_hint":true}],` +
		`"messages":[{"role":"user","blocks":[{"type":"text","text":"Hi"}]},{"role":"assistant","blocks":[{"type":"tool_use","tool_use":{"id":"tu_1","name":"bash","args":{"cmd":"ls"}}}]}],` +
		`"tools":[{"name":"bash","description":"run","input_schema":{"type":"object"}}],"server_tools":[{"type":"web_search","name":"search","config":{}}],` +
		`"tool_choice":{"mode":"tool","name":"bash","disable_parallel":true},"max_tokens":256,"temperature":0.5,"top_p":0.9,"top_k":40,` +
		`"stop_sequences":["END"],"stream":true,"reasoning":{"effort":"high"},"schema":{"name":"answer","schema":{"type":"object"},"strict":true},` +
		`"web_search":{"context_size":"low"},"user_id":"u_1","cache_key":"k","logprobs":true,"top_logprobs":2,"reasoning_replay":true}`
	if string(got) != want {
		t.Errorf("request =\n%s\nwant\n%s", got, want)
	}
	minimal, err := json.Marshal(wire.Request{Model: "m", Messages: []wire.Message{hi}})
	if err != nil {
		t.Fatal(err)
	}
	if string(minimal) != `{"model":"m","messages":[{"role":"user","blocks":[{"type":"text","text":"Hi"}]}]}` {
		t.Errorf("a minimal request leaves unset fields out, got %s", minimal)
	}
}

func TestResponseAndEventWireFormat(t *testing.T) {
	read, cost := int64(0), int64(12)
	res := wire.Response{
		ID: "msg_1", Model: "m", StopReason: wire.StopStopSequence, StopSequence: "END",
		Blocks:   []wire.Block{{Type: wire.BlockThinking, Text: "hm", Signature: "sig"}, {Type: wire.BlockOpaque, Opaque: &wire.Opaque{Dialect: wire.DialectOpenAIResponses, Kind: "reasoning", Raw: json.RawMessage(`{"id":"rs_1"}`)}}},
		Usage:    wire.Usage{InputTokens: 3, OutputTokens: 4, CacheReadInputTokens: &read, ReasoningTokens: 2, CostUSDMicro: &cost},
		LogProbs: []wire.TokenLogProb{{Token: "a", Bytes: []byte("a"), LogProb: wire.LogProb(math.Inf(-1)), Top: []wire.TokenLogProb{{Token: "b", LogProb: -0.5}}}},
	}
	got, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":"msg_1","model":"m","blocks":[{"type":"thinking","text":"hm","signature":"sig"},{"type":"opaque","opaque":{"dialect":"openai-responses","kind":"reasoning","raw":{"id":"rs_1"}}}],` +
		`"stop_reason":"stop_sequence","stop_sequence":"END","usage":{"input_tokens":3,"output_tokens":4,"cache_read_input_tokens":0,"reasoning_tokens":2,"cost_usd_micro":12},` +
		`"logprobs":[{"token":"a","bytes":"YQ==","logprob":null,"top":[{"token":"b","logprob":-0.5}]}]}`
	if string(got) != want {
		t.Errorf("response =\n%s\nwant\n%s", got, want)
	}
	ev, err := json.Marshal(wire.Event{Type: wire.EventBlockStart, Index: 1, Block: &wire.Block{Type: wire.BlockImage, Image: &wire.Image{MediaType: "image/png", URL: "https://example.com/a.png"}}})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"type":"block_start","index":1,"block":{"type":"image","image":{"media_type":"image/png","url":"https://example.com/a.png"}}}`; string(ev) != want {
		t.Errorf("event = %s, want %s", ev, want)
	}
}

// The fields the public documentation of the Lux dialect lists are the
// fields the types carry, under the same names.
func TestTheDocumentedFieldsAreCarried(t *testing.T) {
	for _, tc := range []struct {
		value any
		want  []string
	}{
		{wire.Request{}, []string{"model", "system", "messages", "tools", "tool_choice", "max_tokens", "temperature", "top_p", "top_k", "stop_sequences", "stream", "reasoning", "schema", "user_id"}},
		{wire.Response{}, []string{"id", "model", "blocks", "stop_reason", "stop_sequence", "usage"}},
		{wire.Usage{}, []string{"input_tokens", "output_tokens", "cache_read_input_tokens", "cache_write_input_tokens", "reasoning_tokens"}},
		{wire.Block{}, []string{"type", "text", "image", "tool_use", "tool_result", "signature", "redacted", "cache_hint"}},
		{wire.Event{}, []string{"type", "index", "block", "delta", "stop_reason", "usage"}},
		{wire.ToolChoice{}, []string{"mode", "name", "disable_parallel"}},
		{wire.ResponseSchema{}, []string{"name", "description", "schema", "strict"}},
	} {
		names := jsonNames(reflect.TypeOf(tc.value))
		for _, want := range tc.want {
			if !slices.Contains(names, want) {
				t.Errorf("%T has no field %q (it has %v)", tc.value, want, names)
			}
		}
	}
}

func jsonNames(t reflect.Type) []string {
	var out []string
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		out = append(out, name)
	}
	return out
}

func TestLogProbTravelsNullForMinusInfinity(t *testing.T) {
	for _, tc := range []struct {
		in   wire.LogProb
		want string
	}{
		{wire.LogProb(math.Inf(-1)), "null"},
		{wire.LogProb(math.NaN()), "null"},
		{-1.25, "-1.25"},
	} {
		got, err := json.Marshal(tc.in)
		if err != nil || string(got) != tc.want {
			t.Errorf("Marshal(%v) = %s, %v, want %s", tc.in, got, err, tc.want)
		}
	}
	var back []wire.LogProb
	if err := json.Unmarshal([]byte(`[null,-0.5]`), &back); err != nil {
		t.Fatal(err)
	}
	if !math.IsInf(float64(back[0]), -1) || back[1] != -0.5 {
		t.Errorf("Unmarshal = %v", back)
	}
	var bad wire.LogProb
	if err := json.Unmarshal([]byte(`"x"`), &bad); err == nil {
		t.Error("a string read as a log probability")
	}
}

func TestResponseText(t *testing.T) {
	r := wire.Response{Blocks: []wire.Block{{Type: wire.BlockText, Text: "a"}, {Type: wire.BlockToolUse}, {Type: wire.BlockText, Text: "b"}}}
	if r.Text() != "ab" {
		t.Errorf("Text = %q", r.Text())
	}
}
