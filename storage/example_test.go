// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package storage_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"

	"latere.ai/x/sdk-go/latere"
	"latere.ai/x/sdk-go/storage"
)

func client() *storage.Client {
	cfg, err := latere.NewConfig() // the key in LATERE_KEY
	if err != nil {
		log.Fatal(err)
	}
	return storage.New(cfg)
}

// Uploading a file of any size, with its progress.
func ExampleClient_UploadFile() {
	f, err := os.Open("talk.mp4")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		log.Fatal(err)
	}
	file, err := client().UploadFile(context.Background(), storage.Me, "files/videos/talk.mp4", f, info.Size(), &storage.UploadOptions{
		ContentType: "video/mp4",
		Progress: func(sent, total int64) {
			fmt.Printf("\r%d%%", sent*100/total)
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("\nstored", file.Path)
}

// Writing a small file only if nothing is at its path yet.
func ExampleClient_WriteFile() {
	note := []byte("# Today\n")
	_, err := client().WriteFile(context.Background(), storage.Me, "files/notes/today.md", bytes.NewReader(note), int64(len(note)), &storage.WriteOptions{
		ContentType: "text/markdown",
		IfNoneMatch: true,
	})
	if err != nil {
		log.Fatal(err)
	}
}

// Reading a file's bytes into a local file.
func ExampleClient_ReadFile() {
	r, err := client().ReadFile(context.Background(), storage.Me, "files/videos/talk.mp4")
	if err != nil {
		log.Fatal(err)
	}
	defer r.Close()
	out, err := os.Create("talk.mp4")
	if err != nil {
		log.Fatal(err)
	}
	if _, err := io.Copy(out, r); err != nil {
		log.Fatal(err)
	}
	if err := out.Close(); err != nil {
		log.Fatal(err)
	}
}

// Listing every Markdown file under a folder, at any depth.
func ExampleClient_ListFiles() {
	for f, err := range client().ListFiles(context.Background(), storage.Me, "files/notes", &storage.ListFilesParams{Query: ".md"}) {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(f.Path, f.Size)
	}
}

// A link a browser can open for five minutes, saving the file.
func ExampleClient_GetDownloadURL() {
	link, err := client().GetDownloadURL(context.Background(), storage.Me, "files/reports/q3.pdf", &storage.DownloadURLOptions{Attachment: true})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(link)
}

// Renaming a folder, with every file under it, in one change.
func ExampleClient_MoveFolder() {
	moved, err := client().MoveFolder(context.Background(), storage.Me, "files/drafts", "files/talks/2026")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("moved", moved.Files, "files to", moved.Path)
}

// Moving a file to the trash, from which it can be restored.
func ExampleClient_DeleteFile() {
	if err := client().DeleteFile(context.Background(), storage.Me, "files/notes/old.md", nil); err != nil {
		log.Fatal(err)
	}
}

// Listing what other people shared with you, and reading a shared file.
func ExampleClient_ListSharedWithMe() {
	ctx := context.Background()
	c := client()
	for g, err := range c.ListSharedWithMe(ctx, nil) {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(g.Owner, g.PathPrefix, g.Permission)
		if info, err := c.StatFile(ctx, g.Owner, g.PathPrefix); err == nil {
			fmt.Println("a file of", info.Size, "bytes")
		}
	}
}
