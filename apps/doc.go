// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package apps works with apps: web apps and services Latere builds and
// serves at their own address.
//
// An app is a git repository and an address. Creating an app creates its
// repository; a push to a branch builds a preview, and a pushed tag that
// starts with "v" releases that commit to the app's address. Each build is
// a deploy, with a build log you can read or follow. This package creates,
// reads, changes and deletes apps, reads their deploys, build logs and
// releases, and decides who may open them. Deploying is a git push, which
// this package does not do.
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
// Every model the API answers keeps the JSON it was decoded from, which
// RawJSON returns with every field, including those this version of the
// package does not model yet.
package apps
