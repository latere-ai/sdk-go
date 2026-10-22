// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package account_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"

	"latere.ai/x/sdk-go/account"
)

// Offering the projects a conversation started from an app's page may
// begin in: the ones that hold the app.
func ExampleClient_ListProjects() {
	for p, err := range client().ListProjects(context.Background(), &account.ListProjectsParams{App: "5c1e7a2b-3d4f-4a6b-8c9d-0e1f2a3b4c5d"}) {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(p.ID, p.Name)
	}
}

// Showing one page of projects at a time, as a web app does between
// requests.
func ExampleClient_ListProjectsPage() {
	page, err := client().ListProjectsPage(context.Background(), &account.ListProjectsParams{Limit: 20})
	if err != nil {
		log.Fatal(err)
	}
	for _, p := range page.Items {
		fmt.Println(p.Name, len(p.Apps), "apps")
	}
	if page.NextCursor != "" {
		fmt.Println("more after", page.NextCursor)
	}
}

// Making a project for an app, with what every conversation in it reads.
func ExampleClient_CreateProject() {
	p, err := client().CreateProject(context.Background(), &account.CreateProjectParams{
		Name:         "Tide tables",
		Instructions: "Keep the site static. Use the harbor's own data when it is newer.",
		Apps:         []string{"5c1e7a2b-3d4f-4a6b-8c9d-0e1f2a3b4c5d"},
	})
	if errors.Is(err, account.ErrProjectLimit) {
		fmt.Println("this context holds as many projects as it may")
		return
	}
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(p.ID)
}

// Reading a project for its own page, and offering only what the caller
// may do with it.
func ExampleClient_GetProject() {
	p, err := client().GetProject(context.Background(), "prj_01JA4Q0Z8X3K5V7M9N1P2R4T6W")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(p.Name, "rename offered:", slices.Contains(p.Actions, account.ProjectRename))
}

// Renaming a project and removing its instructions in one change.
func ExampleClient_UpdateProject() {
	name, none := "Tide tables, 2027", ""
	p, err := client().UpdateProject(context.Background(), "prj_01JA4Q0Z8X3K5V7M9N1P2R4T6W", &account.UpdateProjectParams{Name: &name, Instructions: &none})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(p.Name)
}

// Deleting a project; its conversations and apps stay.
func ExampleClient_DeleteProject() {
	if err := client().DeleteProject(context.Background(), "prj_01JA4Q0Z8X3K5V7M9N1P2R4T6W"); err != nil {
		log.Fatal(err)
	}
}

// Adding an app to a project, which conversations started in it then
// work on.
func ExampleClient_AddProjectApp() {
	p, err := client().AddProjectApp(context.Background(), "prj_01JA4Q0Z8X3K5V7M9N1P2R4T6W", "5c1e7a2b-3d4f-4a6b-8c9d-0e1f2a3b4c5d")
	if errors.Is(err, account.ErrAppNotInContext) {
		fmt.Println("that app is not one this project may hold")
		return
	}
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(len(p.Apps), "apps")
}

// Removing an app from a project; the app stays as it is.
func ExampleClient_RemoveProjectApp() {
	if err := client().RemoveProjectApp(context.Background(), "prj_01JA4Q0Z8X3K5V7M9N1P2R4T6W", "5c1e7a2b-3d4f-4a6b-8c9d-0e1f2a3b4c5d"); err != nil {
		log.Fatal(err)
	}
}

// Showing which routines have a link or an email address, and whether
// email can start one at all.
func ExampleClient_ListTriggerInbound() {
	in, err := client().ListTriggerInbound(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	if !in.Mail.Enabled {
		fmt.Println("email cannot start a routine here")
	}
	for _, inlet := range in.Items {
		switch inlet.Kind {
		case account.InletMail:
			fmt.Println(inlet.TriggerID, "forward to", inlet.Address)
		case account.InletWebhook:
			fmt.Println(inlet.TriggerID, "has a link, issued", inlet.IssuedAt.Format("January 2"))
		}
	}
}

// Giving a routine's trigger a link another app posts to. The link is
// answered this once, so it is shown, or stored as a secret, now.
func ExampleClient_IssueTriggerHook() {
	hook, err := client().IssueTriggerHook(context.Background(), "trg_01J9ZK4W6H2T0R8M3C5V7B1N9Q")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("post to", hook.URL)
}

// Turning a routine's link off.
func ExampleClient_RemoveTriggerHook() {
	if err := client().RemoveTriggerHook(context.Background(), "trg_01J9ZK4W6H2T0R8M3C5V7B1N9Q"); err != nil {
		log.Fatal(err)
	}
}

// Giving a routine's trigger an email address mail is forwarded to.
func ExampleClient_IssueTriggerAddress() {
	a, err := client().IssueTriggerAddress(context.Background(), "trg_01J9ZM0A1B2C3D4E5F6G7H8J9K")
	if errors.Is(err, account.ErrMailNotEnabled) {
		fmt.Println("email cannot start a routine here")
		return
	}
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("forward to", a.Address)
}

// Turning a routine's email address off.
func ExampleClient_RemoveTriggerAddress() {
	if err := client().RemoveTriggerAddress(context.Background(), "trg_01J9ZM0A1B2C3D4E5F6G7H8J9K"); err != nil {
		log.Fatal(err)
	}
}
