// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package apps works with apps: web apps and services Latere builds and
// serves at their own address.
//
// An app is a git repository and an address. Creating an app creates its
// repository; a push to a branch builds a preview, and a pushed tag that
// starts with "v" releases that commit to the app's address. Each build is
// a deploy, with a build log you can read or follow. This package creates,
// reads, changes and deletes apps, reads their deploys, build logs,
// releases, pictures and visits, lists the sessions that built them,
// decides who may open them and whether the gallery shows them, and hands
// out the frame passes that show a private app in a page of another site.
// Deploying is a git push, which this package does not do.
//
//	cfg, err := latere.NewConfig() // the key in LATERE_KEY
//	if err != nil {
//		return err
//	}
//	ap := apps.New(cfg)
//	a, err := ap.CreateApp(ctx, &apps.CreateAppParams{Name: "Tide tables"})
//	if err != nil {
//		return err
//	}
//	fmt.Println("push to", a.Repository.PushURL, "to publish at", a.URL)
//
// An app is named by its slug, its address, in every call but those about
// who may open it, which take its ID: the slug changes when the app is
// renamed, the ID never does.
//
// A few reads also answer without a credential, as anyone with an app's
// address reads it: GetApp, GetDeployApp and ReadThumbnail, through a
// Config that sends none, answer a public or unlisted app that is
// published, and nothing of any other.
//
// A deploy's picture is bytes, not JSON: ReadThumbnail returns a
// ThumbnailReader with its media type, its length and how a cache may
// keep it, which a program copies to a browser as it arrives.
//
// Every model the API answers keeps the JSON it was decoded from, which
// RawJSON returns with every field, including those this version of the
// package does not model yet.
package apps
