// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package llm_test

import (
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"

	"latere.ai/x/sdk-go/llm"
)

// The wire format is a contract with the service: field names, their order
// and omitempty rules decide the bytes sent. This request sets every field,
// so a renamed, reordered or retagged field changes the golden.
func TestRequestWireFormat(t *testing.T) {
	maxTokens, topK := int64(256), int64(40)
	temperature, topP := 0.5, 0.9
	req := llm.Request{
		Model:       "anthropic/claude-sonnet-4.5",
		System:      []llm.Block{{Type: llm.BlockText, Text: "Be brief.", CacheHint: true}},
		Messages:    []llm.Message{llm.UserText("Hi"), {Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockToolUse, ToolUse: &llm.ToolUse{ID: "tu_1", Name: "bash", Args: json.RawMessage(`{"cmd":"ls"}`)}}}}},
		Tools:       []llm.Tool{{Name: "bash", Description: "run", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		ServerTools: []llm.ServerTool{{Type: "web_search", Name: "search", Config: json.RawMessage(`{}`)}},
		ToolChoice:  &llm.ToolChoice{Mode: llm.ToolChoiceTool, Name: "bash", DisableParallel: true},
		MaxTokens:   &maxTokens, Temperature: &temperature, TopP: &topP, TopK: &topK,
		StopSequences:   []string{"END"},
		Stream:          true,
		Reasoning:       &llm.Reasoning{Effort: llm.EffortHigh},
		Schema:          &llm.ResponseSchema{Name: "answer", Schema: json.RawMessage(`{"type":"object"}`), Strict: true},
		WebSearch:       &llm.WebSearch{ContextSize: "low"},
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
	minimal, err := json.Marshal(llm.Request{Model: "m", Messages: []llm.Message{llm.UserText("Hi")}})
	if err != nil {
		t.Fatal(err)
	}
	if string(minimal) != `{"model":"m","messages":[{"role":"user","blocks":[{"type":"text","text":"Hi"}]}]}` {
		t.Errorf("a minimal request leaves unset fields out, got %s", minimal)
	}
}

func TestResponseAndEventWireFormat(t *testing.T) {
	read, cost := int64(0), int64(12)
	res := llm.Response{
		ID: "msg_1", Model: "m", StopReason: llm.StopStopSequence, StopSequence: "END",
		Blocks:   []llm.Block{{Type: llm.BlockThinking, Text: "hm", Signature: "sig"}, {Type: llm.BlockOpaque, Opaque: &llm.Opaque{Dialect: llm.DialectOpenAIResponses, Kind: "reasoning", Raw: json.RawMessage(`{"id":"rs_1"}`)}}},
		Usage:    llm.Usage{InputTokens: 3, OutputTokens: 4, CacheReadInputTokens: &read, ReasoningTokens: 2, CostUSDMicro: &cost},
		LogProbs: []llm.TokenLogProb{{Token: "a", Bytes: []byte("a"), LogProb: llm.LogProb(math.Inf(-1)), Top: []llm.TokenLogProb{{Token: "b", LogProb: -0.5}}}},
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
	ev, err := json.Marshal(llm.Event{Type: llm.EventBlockStart, Index: 1, Block: &llm.Block{Type: llm.BlockImage, Image: &llm.Image{MediaType: "image/png", URL: "https://example.com/a.png"}}})
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
		{llm.Request{}, []string{"model", "system", "messages", "tools", "tool_choice", "max_tokens", "temperature", "top_p", "top_k", "stop_sequences", "stream", "reasoning", "schema", "user_id"}},
		{llm.Response{}, []string{"id", "model", "blocks", "stop_reason", "stop_sequence", "usage"}},
		{llm.Usage{}, []string{"input_tokens", "output_tokens", "cache_read_input_tokens", "cache_write_input_tokens", "reasoning_tokens"}},
		{llm.Block{}, []string{"type", "text", "image", "tool_use", "tool_result", "signature", "redacted", "cache_hint"}},
		{llm.Event{}, []string{"type", "index", "block", "delta", "stop_reason", "usage"}},
		{llm.ToolChoice{}, []string{"mode", "name", "disable_parallel"}},
		{llm.ResponseSchema{}, []string{"name", "description", "schema", "strict"}},
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
		in   llm.LogProb
		want string
	}{
		{llm.LogProb(math.Inf(-1)), "null"},
		{llm.LogProb(math.NaN()), "null"},
		{-1.25, "-1.25"},
	} {
		got, err := json.Marshal(tc.in)
		if err != nil || string(got) != tc.want {
			t.Errorf("Marshal(%v) = %s, %v, want %s", tc.in, got, err, tc.want)
		}
	}
	var back []llm.LogProb
	if err := json.Unmarshal([]byte(`[null,-0.5]`), &back); err != nil {
		t.Fatal(err)
	}
	if !math.IsInf(float64(back[0]), -1) || back[1] != -0.5 {
		t.Errorf("Unmarshal = %v", back)
	}
	var bad llm.LogProb
	if err := json.Unmarshal([]byte(`"x"`), &bad); err == nil {
		t.Error("a string read as a log probability")
	}
}

func TestTextHelpers(t *testing.T) {
	if m := llm.AssistantText("ok"); m.Role != llm.RoleAssistant || m.Blocks[0].Text != "ok" {
		t.Errorf("AssistantText = %+v", m)
	}
	r := llm.Response{Blocks: []llm.Block{{Type: llm.BlockText, Text: "a"}, {Type: llm.BlockToolUse}, {Type: llm.BlockText, Text: "b"}}}
	if r.Text() != "ab" {
		t.Errorf("Text = %q", r.Text())
	}
}
