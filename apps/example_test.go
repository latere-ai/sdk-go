// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package apps_test

import (
	"context"
	"fmt"
	"log"

	"latere.ai/x/sdk-go/apps"
	"latere.ai/x/sdk-go/latere"
)

func client() *apps.Client {
	cfg, err := latere.NewConfig() // the key in LATERE_KEY
	if err != nil {
		log.Fatal(err)
	}
	return apps.New(cfg)
}

// Creating an app, then publishing it with a git push of a tag that starts
// with "v" to its repository.
func ExampleClient_CreateApp() {
	a, err := client().CreateApp(context.Background(), &apps.CreateAppParams{Name: "Tide tables"})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("git remote add latere", a.Repository.PushURL)
	fmt.Println("git tag v1.0.0 && git push latere v1.0.0")
	fmt.Println("then open", a.URL)
}

// Listing every app, newest published first.
func ExampleClient_ListApps() {
	for a, err := range client().ListApps(context.Background(), &apps.ListAppsParams{Order: apps.OrderPublished}) {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(a.Slug, a.State, a.URL)
	}
}

// Following the build of the newest deploy, then saying how it ended.
func ExampleClient_FollowDeployLog() {
	ctx := context.Background()
	c := client()
	deploys, err := c.ListDeploys(ctx, "tide-tables")
	if err != nil {
		log.Fatal(err)
	}
	if len(deploys) == 0 {
		fmt.Println("nothing was pushed yet")
		return
	}
	for ev, err := range c.FollowDeployLog(ctx, "tide-tables", deploys[0].ID, nil) {
		if err != nil {
			log.Fatal(err)
		}
		switch {
		case ev.Line != nil:
			fmt.Printf("%s | %s\n", ev.Line.Component, ev.Line.Text)
		case ev.End.Status == apps.BuildFailed && ev.End.Error != nil:
			fmt.Println("the build failed:", ev.End.Error.Message, ev.End.Error.Hint)
		default:
			fmt.Println("the build ended:", ev.End.Status)
		}
	}
}

// Making an app private and opening it to one more person.
func ExampleClient_Invite() {
	ctx := context.Background()
	c := client()
	a, err := c.UpdateApp(ctx, "tide-tables", &apps.UpdateAppParams{Visibility: apps.VisibilityPrivate})
	if err != nil {
		log.Fatal(err)
	}
	invites, err := c.Invite(ctx, a.ID, &apps.InviteParams{Emails: []string{"pilot@harbor.example"}})
	if err != nil {
		log.Fatal(err)
	}
	for _, p := range invites.People {
		fmt.Println("invited", p.Email)
	}
}
