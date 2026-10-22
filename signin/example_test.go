// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package signin_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"time"

	"latere.ai/x/sdk-go/latere"
	"latere.ai/x/sdk-go/signin"
)

// Signing a person in from a desktop app, then calling the API as them.
func ExampleConfig_Browser() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	signer := &signin.Config{ClientID: "my-app"}
	tok, err := signer.Browser(ctx, func(url string) error {
		return exec.Command("open", url).Run() // macOS; use xdg-open on Linux
	})
	if err != nil {
		log.Fatal(err)
	}
	session := signer.TokenSource(tok, func(ctx context.Context, t *signin.Token) error {
		// Store t.RefreshToken in the system keychain here.
		return nil
	})
	tokens, err := latere.NewActorTokens(session)
	if err != nil {
		log.Fatal(err)
	}
	cfg, err := latere.NewConfig(latere.WithTokenSource(tokens))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(cfg.BaseURL())
}

// Signing a person in from a terminal with a code they enter elsewhere.
func ExampleConfig_Device() {
	signer := &signin.Config{ClientID: "my-cli"}
	tok, err := signer.Device(context.Background(), func(c signin.DeviceCode) error {
		fmt.Printf("Open %s and enter %s\n", c.VerificationURI, c.UserCode)
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}
	info, err := signer.UserInfo(context.Background(), tok.AccessToken)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("signed in as", info.Email)
}

// Showing whom a sign-in names: the organizations they can act in, and
// the handle of someone else.
func ExampleConfig_ListOrganizations() {
	signer := &signin.Config{ClientID: "my-app"}
	ctx := context.Background()
	tok, err := signer.Browser(ctx, func(url string) error {
		return exec.Command("open", url).Run()
	})
	if err != nil {
		log.Fatal(err)
	}
	orgs, err := signer.ListOrganizations(ctx, tok.AccessToken)
	if err != nil {
		log.Fatal(err)
	}
	for _, org := range orgs {
		fmt.Println(org.Name, org.Slug)
	}
	handle, err := signer.GetHandle(ctx, tok.AccessToken, "0b6f1c1e-0000-4000-8000-000000000001")
	switch {
	case errors.Is(err, signin.ErrNoPerson):
		fmt.Println("no one has that id")
	case err != nil:
		log.Fatal(err)
	case handle == "":
		fmt.Println("they have claimed no handle")
	default:
		fmt.Println("@" + handle)
	}
}

// Creating a key for the signed-in person, which a script then calls the
// API with, and revoking it once it is no longer needed. Creating a key
// takes a sign-in through a client Latere registers as first party.
func ExampleConfig_CreateKey() {
	signer := &signin.Config{ClientID: "my-cli"}
	ctx := context.Background()
	tok, err := signer.Device(ctx, func(c signin.DeviceCode) error {
		fmt.Printf("Open %s and enter %s\n", c.VerificationURI, c.UserCode)
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}
	key, err := signer.CreateKey(ctx, tok.AccessToken, &signin.CreateKeyParams{
		Name:      "nightly report",
		Grants:    json.RawMessage(`[{"type":"latere-authz","actions":["lux:model.use"],"datatypes":["Model"],"locations":["https://api.latere.ai"]}]`),
		ExpiresAt: time.Now().AddDate(0, 3, 0),
	})
	if err != nil {
		log.Fatal(err)
	}
	cfg, err := latere.NewConfig(latere.WithKey(key.Value))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(cfg.BaseURL(), key.Prefix)
	if err := signer.RevokeKey(ctx, tok.AccessToken, key.ID); err != nil && !errors.Is(err, signin.ErrNoKey) {
		log.Fatal(err)
	}
}
