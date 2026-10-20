// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package llm calls language models through the models capability of the
// Latere API, in the Lux dialect: one request, response and streaming shape
// for every catalog model, whichever provider serves it.
//
// Build a Client from a latere.Config whose credentials are a key, created
// under Keys in the console:
//
//	cfg, err := latere.NewConfig() // the key in LATERE_KEY
//	if err != nil {
//		log.Fatal(err)
//	}
//	models := llm.New(cfg)
//	res, err := models.Generate(ctx, &llm.Request{
//		Model:    "anthropic/claude-sonnet-4-5",
//		Messages: []llm.Message{llm.UserText("Hello")},
//	})
//	if err != nil {
//		log.Fatal(err)
//	}
//	fmt.Println(res.Text())
//
// # Streaming
//
// Stream returns the answer as typed events, in the order
//
//	message_start (block_start (text_delta|args_delta|thinking_delta|signature_delta)* block_stop)* message_delta message_stop
//
// A tool call arrives as a block_start holding its ID and name, then its
// arguments in args_delta pieces, closed by block_stop. Usage arrives twice,
// the input side on message_start and the output side on message_delta.
//
//	stream, err := models.Stream(ctx, req)
//	if err != nil {
//		log.Fatal(err)
//	}
//	defer stream.Close()
//	for ev, err := range stream.Events() {
//		if err != nil {
//			log.Fatal(err)
//		}
//		if ev.Type == llm.EventTextDelta {
//			fmt.Print(ev.Delta)
//		}
//	}
//
// # Nothing silently lost
//
// A provider cannot represent every field of a request; one has no top_k,
// most have no prompt-cache hints. Such a field is left out of the call and
// named in Result.Loss, or Stream.Loss, rather than dropped without a word.
//
// # Other ways to call the same models
//
// The models capability also speaks the OpenAI, Anthropic and Gemini wire
// formats, so programs built on those vendors' own SDKs reach the same
// catalog with the same key: point the OpenAI SDKs at
// https://api.latere.ai/v1/models/openai/v1 and the Anthropic SDKs at
// https://api.latere.ai/v1/models/anthropic. This package is for programs
// written against the Lux dialect, which carries everything the service can
// route without adopting one vendor's request shape.
//
// The key is the credential. The models capability authenticates a key as
// itself, so a Client sends the key, not the short-lived token other
// capabilities take; see latere.Request.SendKey.
package llm
