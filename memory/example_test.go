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
