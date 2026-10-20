// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package storage reads and writes files in Latere Storage: a space of
// files and folders for a person or an organization, with versions,
// which agents and Latere's apps read and write too.
//
// A space is named by its owner: Me is your own, and an organization's is
// named by its subject, as the API writes it in a response. A path is the
// file's place in the space and always starts with "files/", such as
// "files/notes/today.md". A folder is a prefix of paths; one with nothing
// in it does not exist.
//
// UploadFile writes a file of any size, up to MaxFileBytes: in one request
// when it fits in MaxWriteBytes, and in parts otherwise, put straight to
// object storage, with the progress reported as it goes.
//
//	cfg, err := latere.NewConfig() // the key in LATERE_KEY
//	if err != nil {
//		return err
//	}
//	f, err := os.Open("talk.mp4")
//	if err != nil {
//		return err
//	}
//	defer f.Close()
//	info, err := f.Stat()
//	if err != nil {
//		return err
//	}
//	st := storage.New(cfg)
//	file, err := st.UploadFile(ctx, storage.Me, "files/videos/talk.mp4", f, info.Size(), &storage.UploadOptions{ContentType: "video/mp4"})
//	if err != nil {
//		return err
//	}
//	fmt.Println("stored", file.Path, file.Size, "bytes")
//
// ReadFile streams a file's bytes back, following the API to object
// storage for a large file, and GetDownloadURL answers a short-lived link
// that serves them without a credential, for a browser or another program.
//
// The steps of an upload in parts, StartUpload, PutPart, CompleteUpload and
// AbortUpload, are there for a program that spreads the parts over several
// machines or resumes an upload across runs; UploadFile does all of them.
//
// Every File the API answers keeps the JSON it was decoded from, which
// RawJSON returns with every field, including those this version of the
// package does not model yet.
package storage
