// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package memory_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"

	"latere.ai/x/sdk-go/latere"
	"latere.ai/x/sdk-go/memory"
)

func client() *memory.Client {
	cfg, err := latere.NewConfig() // the key in LATERE_KEY
	if err != nil {
		log.Fatal(err)
	}
	return memory.New(cfg)
}

// Showing what is remembered: You first, then each project, each grouped
// by type in the order the API gives.
func ExampleClient_ListMemories() {
	list, err := client().ListMemories(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	scopes := []memory.Ref{{Scope: memory.ScopeYou}}
	names := map[string]string{"": "You"}
	for _, p := range list.Projects {
		scopes = append(scopes, memory.Ref{Scope: memory.ScopeProject, Project: p.ID})
		names[p.ID] = p.Name
	}
	for _, scope := range scopes {
		fmt.Println(names[scope.Project])
		for _, group := range list.Limits.Types {
			for _, m := range list.Memories {
				if m.Scope == scope.Scope && m.Project == scope.Project && m.Type == group {
					fmt.Printf("  %s: %s\n", group, m.Description)
				}
			}
		}
	}
	if list.More {
		fmt.Println("Some memories are not shown.")
	}
}

// Adding a memory to a project, which only that project's conversations
// read.
func ExampleClient_AddMemory() {
	m, err := client().AddMemory(context.Background(), &memory.AddMemoryParams{
		Scope:       memory.ScopeProject,
		Project:     "prj_01JA4Q0Z8X3K5V7M9N1P2R4T6W",
		Type:        memory.TypeTopic,
		Description: "Uses the harbor's own tide tables, not the national ones.",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("saved as", m.ID)
}

// Changing a memory only as it was read: a change made in the meantime,
// by a conversation or another device, is not overwritten.
func ExampleClient_UpdateMemory() {
	ctx := context.Background()
	mem := client()
	ref := memory.Ref{Scope: memory.ScopeYou, ID: "short-lists"}
	m, err := mem.GetMemory(ctx, ref)
	if err != nil {
		log.Fatal(err)
	}
	description := "Keep lists under four things."
	_, err = mem.UpdateMemory(ctx, ref, &memory.UpdateMemoryParams{Description: &description, IfMatch: m.Checksum})
	switch {
	case errors.Is(err, memory.ErrChanged):
		fmt.Println("The memory changed since it was opened; open it again.")
	case err != nil:
		log.Fatal(err)
	}
}

// Moving a memory from You into a project: it gets an id in the project,
// which the answer names.
func ExampleClient_UpdateMemory_move() {
	m, err := client().UpdateMemory(context.Background(), memory.Ref{ID: "short-lists"}, &memory.UpdateMemoryParams{
		Scope:   memory.ScopeProject,
		Project: "prj_01JA4Q0Z8X3K5V7M9N1P2R4T6W",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("now", m.Project, m.ID)
}

// Turning Include sensitive topics off deletes every memory marked
// sensitive, so a program asks the person first.
func ExampleClient_UpdateSettings() {
	off := false
	s, err := client().UpdateSettings(context.Background(), &memory.UpdateSettingsParams{Sensitive: &off})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("deleted", s.DeletedSensitive, "sensitive memories")
}

// Restoring what a conversation forgot.
func ExampleClient_RestoreMemory() {
	ctx := context.Background()
	mem := client()
	forgotten, err := mem.ListForgotten(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, f := range forgotten.Forgotten {
		m, err := mem.RestoreMemory(ctx, f.Ref())
		if errors.Is(err, memory.ErrTaken) {
			fmt.Println(f.ID, "was saved again since; rename or delete it first")
			continue
		}
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println("restored", m.Name)
	}
}

// Writing the export to a file under the name the API gives it. A zip cut
// short ends the copy with ErrExportIncomplete, and the file is removed.
func ExampleClient_ExportMemory() {
	e, err := client().ExportMemory(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := e.Close(); err != nil {
			log.Print(err)
		}
	}()
	name := e.FileName()
	if name == "" {
		name = "latere-memory.zip"
	}
	f, err := os.Create(name)
	if err != nil {
		log.Fatal(err)
	}
	_, err = io.Copy(f, e)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		if removeErr := os.Remove(name); removeErr != nil {
			log.Print(removeErr)
		}
		if errors.Is(err, memory.ErrExportIncomplete) {
			log.Fatal("the export did not finish; try again")
		}
		log.Fatal(err)
	}
	fmt.Println("wrote", name)
}

// Importing what another assistant remembers: the person asks it the
// list's ImportPrompt and pastes its answer.
func ExampleClient_ImportMemory() {
	ctx := context.Background()
	mem := client()
	list, err := mem.ListMemories(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Ask your other assistant:", list.ImportPrompt)
	answer := "Preference: Keep lists under five things.\nPerson: Mia is my sister."
	im, err := mem.ImportMemory(ctx, &memory.ImportMemoryParams{Text: answer})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("saved", len(im.Added), "memories;", im.Skipped, "were remembered already")
}

// Holding what Tidy memory changes at night for the person's OK, from its
// next run on.
func ExampleClient_UpdateSettings_askFirst() {
	s, err := client().UpdateSettings(context.Background(), &memory.UpdateSettingsParams{Review: memory.ReviewAsk})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("review:", s.Review)
}

// Showing what Tidy memory did each night, and what waits for the person.
func ExampleClient_ListRuns() {
	runs, err := client().ListRuns(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	for _, r := range runs.Runs {
		if r.State == memory.RunRunning {
			continue
		}
		fmt.Printf("%s: %d added, %d updated, %d removed", r.CreatedAt.Format("Jan 2"), r.Counts.Added, r.Counts.Updated, r.Counts.Removed)
		if r.Counts.Waiting > 0 {
			fmt.Printf(", %d waiting until %s", r.Counts.Waiting, r.ExpiresAt.Format("Jan 2"))
		}
		fmt.Println()
	}
}

// Showing each change of a run, with the memory before and after it.
func ExampleClient_GetRun() {
	r, err := client().GetRun(context.Background(), "ses_01JA5C2M8Q4R6T8V0W2X4Y6Z8A")
	if err != nil {
		log.Fatal(err)
	}
	for _, ch := range r.Changes {
		fmt.Printf("%s %s (%s)\n", ch.Kind, ch.Name, ch.State)
		if ch.Held == memory.HeldYours {
			fmt.Println("  You wrote this memory, so it waits for your OK.")
		}
		fmt.Printf("  before:\n%s  after:\n%s", ch.Before, ch.After)
	}
}

// Applying one waiting change of a run. A failure may follow an apply
// that landed, so a program reads the run again rather than apply twice.
func ExampleClient_ApplyRun() {
	ctx := context.Background()
	mem := client()
	const run = "ses_01JA5C2M8Q4R6T8V0W2X4Y6Z8A"
	r, err := mem.ApplyRun(ctx, run, &memory.RunDecisionParams{Changes: []string{"trains"}})
	switch {
	case errors.Is(err, memory.ErrRunBusy):
		fmt.Println("This run is being updated; try again in a moment.")
		return
	case err != nil:
		if r, err = mem.GetRun(ctx, run); err != nil {
			log.Fatal(err)
		}
	}
	fmt.Println("the run is", r.State)
}

// Declining everything of a run that still waits.
func ExampleClient_DeclineRun() {
	r, err := client().DeclineRun(context.Background(), "ses_01JA5C2M8Q4R6T8V0W2X4Y6Z8A", nil)
	if errors.Is(err, memory.ErrRunNotWaiting) {
		fmt.Println("Nothing of this run waits for you.")
		return
	}
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("the run is", r.State)
}

// Undoing what a run applied, and saying what was left because the person
// changed it since.
func ExampleClient_UndoRun() {
	u, err := client().UndoRun(context.Background(), "ses_01JA5C2M8Q4R6T8V0W2X4Y6Z8A", nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("the run is", u.State)
	for _, id := range u.MovedOn {
		fmt.Println(id, "changed since, so it was left as it is")
	}
}

// Linking a memory a run of Tidy memory wrote to the run.
func ExampleTidyRunOf() {
	m := memory.Memory{Name: "Market stall", Source: "dream:ses_01JA5C2M8Q4R6T8V0W2X4Y6Z8A"}
	if run, ok := memory.TidyRunOf(m.Source); ok {
		fmt.Printf("%s: from Tidy memory, run %s\n", m.Name, run)
	}
	// Output: Market stall: from Tidy memory, run ses_01JA5C2M8Q4R6T8V0W2X4Y6Z8A
}
