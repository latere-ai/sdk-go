// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents

import "slices"

// A ContextReport is how full a model request's input window was, and
// what filled it: the Context of an EventModelRequest, and the latest of
// them for the session's own thread as Session.ContextReport. A program
// shows how full a conversation's context is from UsedFraction, and what
// fills it from PartsInOrder.
type ContextReport struct {
	// Window is the model's input window, in tokens; zero when the
	// model catalog names none.
	Window int64 `json:"window"`
	// Used is the input tokens the request used.
	Used int64 `json:"used"`
	// Source is where Used came from.
	Source ContextSource `json:"source"`
	// Parts are what the request was made of, in the order of the
	// ReportPartKind constants, the last ContextFree. The parts but
	// ContextFree sum to Used.
	Parts []ReportPart `json:"parts"`
}

// UsedFraction returns the share of the window the request used, Used
// over Window, from 0 to 1: a request an estimate puts past the window
// reads as 1. It reports false, with 0, when the window is not known, and
// for a nil report.
func (r *ContextReport) UsedFraction() (float64, bool) {
	if r == nil || r.Window <= 0 {
		return 0, false
	}
	f := float64(r.Used) / float64(r.Window)
	return min(max(f, 0), 1), true
}

// PartsInOrder returns the parts in the order the ReportPartKind
// constants are declared, ContextFree last, followed by any part of a kind
// this version of the package does not know, in the order the API
// answered them. The API answers the parts in that order already; a
// program that draws them as one bar calls this so its order never
// depends on it. The report's own Parts are left as they are.
func (r *ContextReport) PartsInOrder() []ReportPart {
	if r == nil {
		return nil
	}
	rank := func(k ReportPartKind) int {
		if i := slices.Index(contextPartOrder, k); i >= 0 {
			return i
		}
		return len(contextPartOrder)
	}
	parts := slices.Clone(r.Parts)
	slices.SortStableFunc(parts, func(a, b ReportPart) int { return rank(a.Part) - rank(b.Part) })
	return parts
}

// A ReportPart is one part of what a model request was made of.
type ReportPart struct {
	// Part is which part it is.
	Part ReportPartKind `json:"part"`
	// Tokens is how many tokens of the window it took: for each part but
	// ContextFree, the harness's estimate of it, scaled so those parts sum
	// to the report's Used; for ContextFree, the window less Used.
	Tokens int64 `json:"tokens"`
}

// ReportPartKind is which part of a model request a ReportPart is.
type ReportPartKind string

// The parts of a ContextReport, in the order a request holds them.
const (
	// ContextInstructions is the system prompt, the date included.
	ContextInstructions ReportPartKind = "instructions"
	// ContextTools is the tool definitions.
	ContextTools ReportPartKind = "tools"
	// ContextMessages is the conversation.
	ContextMessages ReportPartKind = "messages"
	// ContextFiles is the files people brought into a message that the
	// request carries.
	ContextFiles ReportPartKind = "files"
	// ContextSummary is the summary of the history compacted away.
	ContextSummary ReportPartKind = "summary"
	// ContextFree is what the window had left: Window less Used, never
	// below zero.
	ContextFree ReportPartKind = "free"
)

// contextPartOrder is the order PartsInOrder puts the parts in.
var contextPartOrder = []ReportPartKind{ContextInstructions, ContextTools, ContextMessages, ContextFiles, ContextSummary, ContextFree}

// ContextSource is where a ContextReport's Used came from.
type ContextSource string

// The sources of a ContextReport's Used.
const (
	// ContextFromProvider is the model provider's count of the request's
	// input tokens, its cache reads and writes included.
	ContextFromProvider ContextSource = "provider"
	// ContextEstimated is the harness's estimate, for a response that
	// counted no input tokens, and for a request that failed or was cut
	// short.
	ContextEstimated ContextSource = "estimate"
)
