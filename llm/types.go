// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package llm

import "latere.ai/x/sdk-go/llm/wire"

// The request, response and stream types are the Lux dialect's wire format,
// defined in package wire, which the Lux gateway's codec also reads, so the
// client and the service cannot drift. Each is named here so a program needs
// this package alone.
type (
	Request        = wire.Request
	Message        = wire.Message
	Block          = wire.Block
	Image          = wire.Image
	ToolUse        = wire.ToolUse
	ToolResult     = wire.ToolResult
	Opaque         = wire.Opaque
	Tool           = wire.Tool
	ServerTool     = wire.ServerTool
	WebSearch      = wire.WebSearch
	ToolChoice     = wire.ToolChoice
	Reasoning      = wire.Reasoning
	ResponseSchema = wire.ResponseSchema
	Usage          = wire.Usage
	Response       = wire.Response
	Event          = wire.Event
	TokenLogProb   = wire.TokenLogProb
	LogProb        = wire.LogProb
	Role           = wire.Role
	BlockType      = wire.BlockType
	ToolChoiceMode = wire.ToolChoiceMode
	Effort         = wire.Effort
	StopReason     = wire.StopReason
	EventType      = wire.EventType
	Dialect        = wire.Dialect
)

// The roles of a turn.
const (
	RoleUser      = wire.RoleUser
	RoleAssistant = wire.RoleAssistant
)

// The kinds of a block's content.
const (
	BlockText             = wire.BlockText
	BlockImage            = wire.BlockImage
	BlockToolUse          = wire.BlockToolUse
	BlockToolResult       = wire.BlockToolResult
	BlockThinking         = wire.BlockThinking
	BlockRedactedThinking = wire.BlockRedactedThinking
	BlockOpaque           = wire.BlockOpaque
)

// The tool choice modes.
const (
	// ToolChoiceAuto lets the model decide.
	ToolChoiceAuto = wire.ToolChoiceAuto
	// ToolChoiceAny makes the model call some tool.
	ToolChoiceAny = wire.ToolChoiceAny
	// ToolChoiceNone keeps the model from calling tools.
	ToolChoiceNone = wire.ToolChoiceNone
	// ToolChoiceTool makes the model call the tool ToolChoice.Name names.
	ToolChoiceTool = wire.ToolChoiceTool
)

// The thinking efforts. A provider's own levels pass through unchanged, so
// an Effort beyond these is not an error.
const (
	EffortMinimal = wire.EffortMinimal
	EffortLow     = wire.EffortLow
	EffortMedium  = wire.EffortMedium
	EffortHigh    = wire.EffortHigh
)

// The reasons an answer ends.
const (
	// StopEndTurn is a finished answer.
	StopEndTurn = wire.StopEndTurn
	// StopToolUse is an answer that waits for tool results.
	StopToolUse = wire.StopToolUse
	// StopMaxTokens is an answer cut at Request.MaxTokens.
	StopMaxTokens = wire.StopMaxTokens
	// StopStopSequence is an answer ended by one of Request.StopSequences.
	StopStopSequence = wire.StopStopSequence
	// StopRefusal is an answer the model declined to give.
	StopRefusal = wire.StopRefusal
)

// The kinds of stream events, in the order a stream holds them.
const (
	EventMessageStart   = wire.EventMessageStart
	EventBlockStart     = wire.EventBlockStart
	EventTextDelta      = wire.EventTextDelta
	EventArgsDelta      = wire.EventArgsDelta
	EventThinkingDelta  = wire.EventThinkingDelta
	EventSignatureDelta = wire.EventSignatureDelta
	EventBlockStop      = wire.EventBlockStop
	EventMessageDelta   = wire.EventMessageDelta
	EventMessageStop    = wire.EventMessageStop
)

// The wire formats an Opaque item comes from.
const (
	DialectAnthropicMessages = wire.DialectAnthropicMessages
	DialectOpenAIChat        = wire.DialectOpenAIChat
	DialectOpenAIResponses   = wire.DialectOpenAIResponses
	DialectLux               = wire.DialectLux
)

// UserText returns a user turn holding one text block.
func UserText(text string) Message {
	return Message{Role: RoleUser, Blocks: []Block{{Type: BlockText, Text: text}}}
}

// AssistantText returns an assistant turn holding one text block.
func AssistantText(text string) Message {
	return Message{Role: RoleAssistant, Blocks: []Block{{Type: BlockText, Text: text}}}
}
