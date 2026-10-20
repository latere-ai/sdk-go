// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package latere_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"latere.ai/x/sdk-go/latere"
	"latere.ai/x/sdk-go/signin"
)

// A program that calls the API with the key in LATERE_KEY, naming itself
// and bounding each call to 30 seconds.
func ExampleNewConfig() {
	cfg, err := latere.NewConfig(
		latere.WithUserAgent("my-app/1.0"),
		latere.WithTimeout(30*time.Second),
	)
	if errors.Is(err, latere.ErrNoCredentials) {
		log.Fatal("create a key under Keys in the console and set LATERE_KEY")
	}
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(cfg.BaseURL())
}

// A program that keeps its key somewhere other than LATERE_KEY.
func ExampleWithKey() {
	cfg, err := latere.NewConfig(latere.WithKey(os.Getenv("MY_APP_LATERE_KEY")))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(cfg.BaseURL())
}

// A program that acts for a person who signed in: their sign-in, from
// package signin, is exchanged for short-lived API tokens.
func ExampleNewActorTokens() {
	ctx := context.Background()
	signer := &signin.Config{ClientID: "my-app"}
	tok, err := signer.Device(ctx, func(c signin.DeviceCode) error {
		fmt.Printf("Open %s and enter %s\n", c.VerificationURI, c.UserCode)
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}
	tokens, err := latere.NewActorTokens(signer.TokenSource(tok, nil))
	if err != nil {
		log.Fatal(err)
	}
	cfg, err := latere.NewConfig(latere.WithTokenSource(tokens))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(cfg.BaseURL())
}

// Telling the common errors apart, and reading the API's code.
func ExampleError() {
	var err error // returned by any call
	switch {
	case errors.Is(err, latere.ErrNotFound):
		fmt.Println("no such session")
	case errors.Is(err, latere.ErrRateLimited):
		var apiErr *latere.Error
		if errors.As(err, &apiErr) {
			fmt.Println("retry after", apiErr.RetryAfter)
		}
	case err != nil:
		var apiErr *latere.Error
		if errors.As(err, &apiErr) {
			fmt.Printf("%s: %s (request %s)\n", apiErr.Code, apiErr.Message, apiErr.RequestID)
		}
	}
}

// Calling an endpoint the capability packages do not cover yet.
func ExampleConfig_Do() {
	cfg, err := latere.NewConfig()
	if err != nil {
		log.Fatal(err)
	}
	var out map[string]any
	err = cfg.Do(context.Background(), &latere.Request{Method: http.MethodGet, Path: "/v1/account/data"}, &out)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(out)
}

// Logging every attempt, retries included.
func ExampleWithHooks() {
	cfg, err := latere.NewConfig(latere.WithHooks(latere.Hooks{
		AfterResponse: func(req *http.Request, resp *http.Response, err error, took time.Duration) {
			if err != nil {
				log.Printf("%s %s failed after %v: %v", req.Method, req.URL.Path, took, err)
				return
			}
			log.Printf("%s %s %d in %v", req.Method, req.URL.Path, resp.StatusCode, took)
		},
	}))
	if err != nil {
		log.Fatal(err)
	}
	_ = cfg
}

// Iterating a list endpoint the capability packages do not cover yet.
func ExampleItems() {
	cfg, err := latere.NewConfig()
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	type trigger struct {
		ID string `json:"id"`
	}
	fetch := func(ctx context.Context, cursor string) (*latere.Page[trigger], error) {
		var page latere.Page[trigger]
		req := &latere.Request{Method: http.MethodGet, Path: "/v1/agents/triggers"}
		if cursor != "" {
			req.Query = map[string][]string{"cursor": {cursor}}
		}
		return &page, cfg.Do(ctx, req, &page)
	}
	for tr, err := range latere.Items(ctx, fetch) {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(tr.ID)
	}
}
