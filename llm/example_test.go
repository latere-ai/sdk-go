// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package llm_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"latere.ai/x/sdk-go/latere"
	"latere.ai/x/sdk-go/llm"
)

func models() *llm.Client {
	cfg, err := latere.NewConfig() // the key in LATERE_KEY
	if err != nil {
		log.Fatal(err)
	}
	return llm.New(cfg)
}

// One question, one complete answer.
func ExampleClient_Generate() {
	ctx := context.Background()
	maxTokens := int64(256)
	res, err := models().Generate(ctx, &llm.Request{
		Model:     "anthropic/claude-sonnet-4-5",
		System:    []llm.Block{{Type: llm.BlockText, Text: "Answer in one sentence."}},
		Messages:  []llm.Message{llm.UserText("Why is the sky blue?")},
		MaxTokens: &maxTokens,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Text())
	if len(res.Loss) > 0 {
		fmt.Println("the provider could not take:", strings.Join(res.Loss, ", "))
	}
}

// Printing an answer as it is written.
func ExampleClient_Stream() {
	ctx := context.Background()
	stream, err := models().Stream(ctx, &llm.Request{
		Model:    "anthropic/claude-sonnet-4-5",
		Messages: []llm.Message{llm.UserText("Write a haiku about rain.")},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer stream.Close()
	var usage llm.Usage
	for ev, err := range stream.Events() {
		if err != nil {
			log.Fatal(err)
		}
		switch ev.Type {
		case llm.EventTextDelta:
			fmt.Print(ev.Delta)
		case llm.EventMessageStart, llm.EventMessageDelta:
			if ev.Usage != nil {
				usage.InputTokens += ev.Usage.InputTokens
				usage.OutputTokens += ev.Usage.OutputTokens
			}
		}
	}
	fmt.Printf("\n%d tokens in, %d out\n", usage.InputTokens, usage.OutputTokens)
}

// A tool call: the model asks, the program answers, the model goes on.
func ExampleClient_Generate_tools() {
	ctx := context.Background()
	c := models()
	req := &llm.Request{
		Model:    "anthropic/claude-sonnet-4-5",
		Messages: []llm.Message{llm.UserText("What time is it in Berlin?")},
		Tools: []llm.Tool{{
			Name:        "clock",
			Description: "Tells the current time in a city.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`),
		}},
	}
	res, err := c.Generate(ctx, req)
	if err != nil {
		log.Fatal(err)
	}
	for res.StopReason == llm.StopToolUse {
		req.Messages = append(req.Messages, llm.Message{Role: llm.RoleAssistant, Blocks: res.Blocks})
		answer := llm.Message{Role: llm.RoleUser}
		for _, b := range res.Blocks {
			if b.Type == llm.BlockToolUse {
				answer.Blocks = append(answer.Blocks, llm.Block{
					Type:       llm.BlockToolResult,
					ToolResult: &llm.ToolResult{ToolUseID: b.ToolUse.ID, Blocks: []llm.Block{{Type: llm.BlockText, Text: "14:05"}}},
				})
			}
		}
		req.Messages = append(req.Messages, answer)
		if res, err = c.Generate(ctx, req); err != nil {
			log.Fatal(err)
		}
	}
	fmt.Println(res.Text())
}

// Counting a request's tokens before sending it, without spending any.
func ExampleClient_CountTokens() {
	ctx := context.Background()
	tc, err := models().CountTokens(ctx, &llm.Request{
		Model:    "anthropic/claude-sonnet-4-5",
		Messages: []llm.Message{llm.UserText("A long document...")},
	})
	if err != nil {
		log.Fatal(err)
	}
	if tc.Estimated {
		fmt.Println("about", tc.InputTokens, "tokens")
		return
	}
	fmt.Println(tc.InputTokens, "tokens")
}

// Listing the models the key may call, with their prices.
func ExampleClient_ListModels() {
	ctx := context.Background()
	list, err := models().ListModels(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, m := range list {
		if m.Pricing != nil {
			fmt.Printf("%s: %s %s in, %s out per %d tokens\n", m.ID, m.Pricing.Input, m.Pricing.Currency, m.Pricing.Output, m.Pricing.Per)
			continue
		}
		fmt.Println(m.ID)
	}
}

// Labeling requests for your own reporting, here by tenant.
func ExampleWithLabels() {
	cfg, err := latere.NewConfig()
	if err != nil {
		log.Fatal(err)
	}
	acme := llm.New(cfg, llm.WithLabels(map[string]string{"tenant": "acme"}))
	res, err := acme.Generate(context.Background(), &llm.Request{
		Model:    "anthropic/claude-sonnet-4-5",
		Messages: []llm.Message{llm.UserText("Hello")},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Text())
}
