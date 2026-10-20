// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package llm

import (
	"encoding/json"
	"math"
)

// The request, response and stream types below are the Lux dialect's wire
// format, the same JSON the models capability reads and writes. Their field
// names, order and omitempty rules are part of that format: a field left
// unset is left out of the request.

// Request is one generation: the model to call, the conversation so far, and
// how to answer. Model and Messages are required.
type Request struct {
	// Model is a catalog model name, "vendor/model", that the key may call;
	// ListModels lists them.
	Model string `json:"model"`
	// System is the system prompt, as content blocks.
	System []Block `json:"system,omitempty"`
	// Messages are the conversation's turns, oldest first.
	Messages []Message `json:"messages"`
	// Tools are the tools the model may call.
	Tools []Tool `json:"tools,omitempty"`
	// ServerTools are tools the provider runs itself, such as its own web
	// search.
	ServerTools []ServerTool `json:"server_tools,omitempty"`
	// ToolChoice constrains how the model uses Tools; nil lets it decide.
	ToolChoice *ToolChoice `json:"tool_choice,omitempty"`
	// MaxTokens caps the answer's length in tokens.
	MaxTokens *int64 `json:"max_tokens,omitempty"`
	// Temperature, TopP and TopK are the sampling controls; nil leaves each
	// at the provider's default.
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	TopK        *int64   `json:"top_k,omitempty"`
	// StopSequences end the answer when any of them is generated.
	StopSequences []string `json:"stop_sequences,omitempty"`
	// Stream asks for server-sent events. Client.Generate and Client.Stream
	// set it themselves, so one Request serves both.
	Stream bool `json:"stream,omitempty"`
	// Reasoning turns on extended thinking.
	Reasoning *Reasoning `json:"reasoning,omitempty"`
	// Schema asks for an answer that conforms to a JSON Schema.
	Schema *ResponseSchema `json:"schema,omitempty"`
	// WebSearch asks the provider to ground its answer in a search it runs.
	WebSearch *WebSearch `json:"web_search,omitempty"`
	// UserID is a stable identifier of your end user, for the provider's
	// abuse monitoring.
	UserID string `json:"user_id,omitempty"`
	// CacheKey names a prompt-cache prefix, for providers that key their
	// cache by name rather than by Block.CacheHint.
	CacheKey string `json:"cache_key,omitempty"`
	// LogProbs asks for each answer token's log probability, and
	// TopLogProbs for that many alternatives at each position.
	LogProbs    bool `json:"logprobs,omitempty"`
	TopLogProbs int  `json:"top_logprobs,omitempty"`
	// ReasoningReplay sends earlier thinking blocks back to a provider that
	// accepts them, so a long tool-using turn keeps its reasoning.
	ReasoningReplay bool `json:"reasoning_replay,omitempty"`
}

// Message is one turn of a conversation.
type Message struct {
	// Role is who wrote the turn. Tool results are blocks in a user turn.
	Role Role `json:"role"`
	// Blocks are the turn's content.
	Blocks []Block `json:"blocks"`
}

// Block is one piece of content. Type says which of the other fields is set.
type Block struct {
	// Type is the kind of content.
	Type BlockType `json:"type"`
	// Text is the content of a BlockText or BlockThinking block.
	Text string `json:"text,omitempty"`
	// Image is the content of a BlockImage block.
	Image *Image `json:"image,omitempty"`
	// ToolUse is the content of a BlockToolUse block: a call the model made.
	ToolUse *ToolUse `json:"tool_use,omitempty"`
	// ToolResult is the content of a BlockToolResult block: your answer to a
	// call.
	ToolResult *ToolResult `json:"tool_result,omitempty"`
	// Signature is the provider's token on a BlockThinking block, which it
	// needs to accept the block back in a later request.
	Signature string `json:"signature,omitempty"`
	// Redacted is the content of a BlockRedactedThinking block, opaque to
	// you.
	Redacted string `json:"redacted,omitempty"`
	// Opaque is the content of a BlockOpaque block.
	Opaque *Opaque `json:"opaque,omitempty"`
	// CacheHint marks the end of a prompt-cache prefix, for providers that
	// support one. It does not apply to thinking blocks.
	CacheHint bool `json:"cache_hint,omitempty"`
}

// Image is image content, inline or by URL.
type Image struct {
	// MediaType is the image's MIME type, such as "image/png".
	MediaType string `json:"media_type,omitempty"`
	// Data is the image, base64-encoded. Set Data or URL.
	Data string `json:"data,omitempty"`
	// URL is where the provider fetches the image.
	URL string `json:"url,omitempty"`
}

// ToolUse is a call the model made to one of the request's tools.
type ToolUse struct {
	// ID names the call; answer it with a ToolResult carrying the same ID.
	ID string `json:"id"`
	// Name is the tool's name.
	Name string `json:"name"`
	// Args are the call's arguments, JSON matching the tool's input schema.
	Args json.RawMessage `json:"args,omitempty"`
}

// ToolResult is your answer to a ToolUse.
type ToolResult struct {
	// ToolUseID is the ID of the call answered.
	ToolUseID string `json:"tool_use_id"`
	// Blocks are the answer's content.
	Blocks []Block `json:"blocks,omitempty"`
	// IsError says the tool failed, so Blocks describe the failure.
	IsError bool `json:"is_error,omitempty"`
}

// Opaque is an item a provider returned that only that provider can read,
// such as an encrypted reasoning item, carried unchanged so it can be sent
// back to the same provider in a later turn.
type Opaque struct {
	// Dialect names the wire format the item came from.
	Dialect Dialect `json:"dialect"`
	// Kind is the provider's own name for the item.
	Kind string `json:"kind,omitempty"`
	// Raw is the item, exactly as the provider sent it.
	Raw json.RawMessage `json:"raw"`
}

// Tool is a tool the model may call.
type Tool struct {
	// Name is how the model names the tool in a ToolUse.
	Name string `json:"name"`
	// Description tells the model what the tool does and when to use it.
	Description string `json:"description,omitempty"`
	// InputSchema is the JSON Schema of the tool's arguments.
	InputSchema json.RawMessage `json:"input_schema"`
}

// ServerTool is a tool the provider runs itself.
type ServerTool struct {
	// Type selects the tool, in the provider's vocabulary.
	Type string `json:"type"`
	// Name is the tool's name, when the provider asks for one.
	Name string `json:"name,omitempty"`
	// Config holds the provider's own options for the tool, passed
	// unchanged.
	Config json.RawMessage `json:"config,omitempty"`
}

// WebSearch asks the provider to ground its answer in a search it runs.
type WebSearch struct {
	// ContextSize is how much search context to use, in the provider's
	// vocabulary, such as "low" or "high".
	ContextSize string `json:"context_size,omitempty"`
	// UserLocation biases results geographically, in the provider's own
	// shape, passed unchanged.
	UserLocation json.RawMessage `json:"user_location,omitempty"`
}

// ToolChoice constrains how the model uses the request's tools.
type ToolChoice struct {
	// Mode says whether and how the model calls tools.
	Mode ToolChoiceMode `json:"mode"`
	// Name is the tool the model must call, with ToolChoiceTool.
	Name string `json:"name,omitempty"`
	// DisableParallel makes the model call one tool at a time.
	DisableParallel bool `json:"disable_parallel,omitempty"`
}

// Reasoning turns on extended thinking. Set Effort or BudgetTokens, not
// both.
type Reasoning struct {
	// Effort is how hard the model thinks, on a provider-neutral scale.
	Effort Effort `json:"effort,omitempty"`
	// BudgetTokens is how many tokens the model may spend thinking.
	BudgetTokens int64 `json:"budget_tokens,omitempty"`
}

// ResponseSchema asks for an answer that is JSON conforming to Schema.
type ResponseSchema struct {
	// Name names the schema, for providers that ask for one.
	Name string `json:"name,omitempty"`
	// Description says what the answer is for.
	Description string `json:"description,omitempty"`
	// Schema is the JSON Schema the answer conforms to.
	Schema json.RawMessage `json:"schema"`
	// Strict asks the provider to enforce the schema exactly, where it can.
	Strict bool `json:"strict,omitempty"`
}

// Usage is what one generation consumed.
type Usage struct {
	// InputTokens and OutputTokens are the tokens read and written.
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	// CacheReadInputTokens and CacheWriteInputTokens are the input tokens
	// served from and written to the provider's prompt cache, or nil when
	// the provider reported no such figure. Nil means not measured; a
	// pointer to zero means measured and none.
	CacheReadInputTokens  *int64 `json:"cache_read_input_tokens,omitempty"`
	CacheWriteInputTokens *int64 `json:"cache_write_input_tokens,omitempty"`
	// ReasoningTokens are the output tokens spent thinking.
	ReasoningTokens int64 `json:"reasoning_tokens,omitempty"`
	// CostUSDMicro is what the generation cost, in millionths of a US
	// dollar, or nil when no cost was reported. Nil means unknown, not free:
	// a reported zero cost is a pointer to zero.
	CostUSDMicro *int64 `json:"cost_usd_micro,omitempty"`
}

// Response is a complete answer. Its blocks use the request's vocabulary, so
// an answer appends to Request.Messages as an assistant turn unchanged.
type Response struct {
	// ID identifies the answer.
	ID string `json:"id"`
	// Model is the model that answered.
	Model string `json:"model"`
	// Blocks are the answer's content.
	Blocks []Block `json:"blocks"`
	// StopReason says why the answer ended.
	StopReason StopReason `json:"stop_reason"`
	// StopSequence is the stop sequence that ended the answer, with
	// StopStopSequence.
	StopSequence string `json:"stop_sequence,omitempty"`
	// Usage is what the answer consumed.
	Usage Usage `json:"usage"`
	// LogProbs are the answer tokens' log probabilities, in order, when the
	// request asked for them.
	LogProbs []TokenLogProb `json:"logprobs,omitempty"`
}

// Event is one event of a streamed answer. Type says which fields are set.
// A stream is, in order:
//
//	message_start (block_start (text_delta|args_delta|thinking_delta|signature_delta)* block_stop)* message_delta message_stop
type Event struct {
	// Type is the kind of event.
	Type EventType `json:"type"`
	// ID and Model are the answer's, on EventMessageStart.
	ID    string `json:"id,omitempty"`
	Model string `json:"model,omitempty"`
	// Index is the position of the block an event belongs to.
	Index int `json:"index"`
	// Block is the block that opens, on EventBlockStart; for a tool call it
	// holds the call's ID and name, and its Args arrive as EventArgsDelta.
	Block *Block `json:"block,omitempty"`
	// Delta is the next piece of a block: text for EventTextDelta and
	// EventThinkingDelta, serialized JSON for EventArgsDelta, and the
	// signature for EventSignatureDelta.
	Delta string `json:"delta,omitempty"`
	// StopReason and StopSequence end the answer, on EventMessageDelta.
	StopReason   StopReason `json:"stop_reason,omitempty"`
	StopSequence string     `json:"stop_sequence,omitempty"`
	// Usage is reported twice: the input side on EventMessageStart and the
	// output side on EventMessageDelta. Add both.
	Usage *Usage `json:"usage,omitempty"`
	// LogProbs are the log probabilities of the tokens in Delta, when the
	// request asked for them.
	LogProbs []TokenLogProb `json:"logprobs,omitempty"`
}

// TokenLogProb is one token and its log probability.
type TokenLogProb struct {
	// Token is the token's text.
	Token string `json:"token"`
	// Bytes are the token's UTF-8 bytes, base64 in JSON. A character can
	// span several tokens, so joining Bytes rebuilds text that joining Token
	// cannot. Nil when the provider reports none.
	Bytes []byte `json:"bytes,omitempty"`
	// LogProb is the token's natural-log probability.
	LogProb LogProb `json:"logprob"`
	// Top are the most likely alternatives at this position, most likely
	// first, as many as Request.TopLogProbs asked for. Their own Top is
	// empty.
	Top []TokenLogProb `json:"top,omitempty"`
}

// LogProb is a natural-log probability. A token that could not have been
// drawn scores negative infinity, which JSON cannot write: it travels as
// null and reads back as math.Inf(-1).
type LogProb float64

// MarshalJSON writes the value, or null when it is not finite.
func (l LogProb) MarshalJSON() ([]byte, error) {
	f := float64(l)
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return []byte("null"), nil
	}
	return json.Marshal(f)
}

// UnmarshalJSON reads the value, and null as math.Inf(-1).
func (l *LogProb) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*l = LogProb(math.Inf(-1))
		return nil
	}
	var f float64
	if err := json.Unmarshal(data, &f); err != nil {
		return err
	}
	*l = LogProb(f)
	return nil
}

// Role is who wrote a turn.
type Role string

// The roles of a turn.
const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// BlockType is the kind of a Block's content.
type BlockType string

// The kinds of content.
const (
	BlockText             BlockType = "text"
	BlockImage            BlockType = "image"
	BlockToolUse          BlockType = "tool_use"
	BlockToolResult       BlockType = "tool_result"
	BlockThinking         BlockType = "thinking"
	BlockRedactedThinking BlockType = "redacted_thinking"
	BlockOpaque           BlockType = "opaque"
)

// ToolChoiceMode says whether and how the model calls tools.
type ToolChoiceMode string

// The tool choice modes.
const (
	// ToolChoiceAuto lets the model decide.
	ToolChoiceAuto ToolChoiceMode = "auto"
	// ToolChoiceAny makes the model call some tool.
	ToolChoiceAny ToolChoiceMode = "any"
	// ToolChoiceNone keeps the model from calling tools.
	ToolChoiceNone ToolChoiceMode = "none"
	// ToolChoiceTool makes the model call the tool ToolChoice.Name names.
	ToolChoiceTool ToolChoiceMode = "tool"
)

// Effort is how hard the model thinks. A provider's own levels pass through
// unchanged, so a value beyond these constants is not an error.
type Effort string

// The thinking efforts.
const (
	EffortMinimal Effort = "minimal"
	EffortLow     Effort = "low"
	EffortMedium  Effort = "medium"
	EffortHigh    Effort = "high"
)

// StopReason says why an answer ended.
type StopReason string

// The reasons an answer ends.
const (
	// StopEndTurn is a finished answer.
	StopEndTurn StopReason = "end_turn"
	// StopToolUse is an answer that waits for tool results.
	StopToolUse StopReason = "tool_use"
	// StopMaxTokens is an answer cut at Request.MaxTokens.
	StopMaxTokens StopReason = "max_tokens"
	// StopStopSequence is an answer ended by one of Request.StopSequences.
	StopStopSequence StopReason = "stop_sequence"
	// StopRefusal is an answer the model declined to give.
	StopRefusal StopReason = "refusal"
)

// EventType is the kind of a stream Event.
type EventType string

// The kinds of stream events, in the order a stream holds them.
const (
	EventMessageStart   EventType = "message_start"
	EventBlockStart     EventType = "block_start"
	EventTextDelta      EventType = "text_delta"
	EventArgsDelta      EventType = "args_delta"
	EventThinkingDelta  EventType = "thinking_delta"
	EventSignatureDelta EventType = "signature_delta"
	EventBlockStop      EventType = "block_stop"
	EventMessageDelta   EventType = "message_delta"
	EventMessageStop    EventType = "message_stop"
)

// Dialect names a provider wire format, the origin of an Opaque item.
type Dialect string

// The wire formats an Opaque item comes from.
const (
	DialectAnthropicMessages Dialect = "anthropic-messages"
	DialectOpenAIChat        Dialect = "openai-chat"
	DialectOpenAIResponses   Dialect = "openai-responses"
	DialectLux               Dialect = "lux"
)

// UserText returns a user turn holding one text block.
func UserText(text string) Message {
	return Message{Role: RoleUser, Blocks: []Block{{Type: BlockText, Text: text}}}
}

// AssistantText returns an assistant turn holding one text block.
func AssistantText(text string) Message {
	return Message{Role: RoleAssistant, Blocks: []Block{{Type: BlockText, Text: text}}}
}

// Text returns the text of r's text blocks, joined in order, leaving out
// thinking and tool calls.
func (r *Response) Text() string {
	var out []byte
	for _, b := range r.Blocks {
		if b.Type == BlockText {
			out = append(out, b.Text...)
		}
	}
	return string(out)
}
