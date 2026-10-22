// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package account_test

import (
	"context"
	"fmt"
	"log"

	"latere.ai/x/sdk-go/account"
)

// Changing what sessions call the person, and returning their kind of
// work to none.
func ExampleClient_UpdatePreferences() {
	callMe, none := "Mara", ""
	p, err := client().UpdatePreferences(context.Background(), &account.UpdatePreferencesParams{CallMe: &callMe, Work: &none})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("sessions call the person", p.Profile.CallMe)
}

// Letting every session of the context reach a site, as an "Always allow"
// of a confirmation does.
func ExampleClient_AddSite() {
	n, err := client().AddSite(context.Background(), "*.wikipedia.org")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(len(n.Domains), "of", n.MaxDomains, "sites")
}

// Taking a site off the context's list.
func ExampleClient_RemoveSite() {
	if err := client().RemoveSite(context.Background(), "*.wikipedia.org"); err != nil {
		log.Fatal(err)
	}
}

// Bookmarking the turn that starts at event 87 of a conversation.
func ExampleClient_BookmarkTurn() {
	b, err := client().BookmarkTurn(context.Background(), "ses_01M34G2JF01B91B8399CD1G6YP", 87)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(b.Sessions["ses_01M34G2JF01B91B8399CD1G6YP"])
}

// Removing a bookmark.
func ExampleClient_UnbookmarkTurn() {
	if err := client().UnbookmarkTurn(context.Background(), "ses_01M34G2JF01B91B8399CD1G6YP", 87); err != nil {
		log.Fatal(err)
	}
}

// Pinning an app first among the person's apps.
func ExampleClient_PinApp() {
	pins, err := client().PinApp(context.Background(), "5c1e7a2b-3d4f-4a6b-8c9d-0e1f2a3b4c5d")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(len(pins.Apps), "of", pins.MaxApps, "apps pinned")
}

// Unpinning an app.
func ExampleClient_UnpinApp() {
	if err := client().UnpinApp(context.Background(), "5c1e7a2b-3d4f-4a6b-8c9d-0e1f2a3b4c5d"); err != nil {
		log.Fatal(err)
	}
}

// Pinning a project first among the person's projects.
func ExampleClient_PinProject() {
	pins, err := client().PinProject(context.Background(), "prj_01JA4Q0Z8X3K5V7M9N1P2R4T6W")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(len(pins.Projects), "of", pins.MaxProjects, "projects pinned")
}

// Unpinning a project.
func ExampleClient_UnpinProject() {
	if err := client().UnpinProject(context.Background(), "prj_01JA4Q0Z8X3K5V7M9N1P2R4T6W"); err != nil {
		log.Fatal(err)
	}
}
