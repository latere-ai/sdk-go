// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package apps_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"

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

// Listing the sessions whose agents published the app, newest publish
// first, with whether their work went live.
func ExampleClient_ListAppSessions() {
	for s, err := range client().ListAppSessions(context.Background(), "tide-tables", nil) {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(s.ID, s.Deploys, "previews, released:", s.Released)
	}
}

// Reading how often a site was opened over the last week.
func ExampleClient_GetAppVisits() {
	v, err := client().GetAppVisits(context.Background(), "tide-tables", 7)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(v.Totals.Last7Days.Visits, "page loads by", v.Totals.Last7Days.Visitors, "visitors")
	for _, d := range v.Days {
		fmt.Println(d.Day, d.Visits)
	}
}

// Reading a deploy a conversation kept by its ID, without its app's slug.
func ExampleClient_GetDeployByID() {
	d, err := client().GetDeployByID(context.Background(), "9f2c6a1e-2b7d-4c4f-9e0a-5d1b7f3c8a21")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(d.Seq, d.Status, d.PreviewURL)
}

// Finding where a deploy's app is now, after its owner renamed it.
func ExampleClient_GetDeployApp() {
	a, err := client().GetDeployApp(context.Background(), "9f2c6a1e-2b7d-4c4f-9e0a-5d1b7f3c8a21")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(a.Name, "is at", a.URL)
}

// Passing a deploy's picture on to a browser as it arrives, with the type
// and the caching the API gave it.
func ExampleClient_ReadThumbnail() {
	http.HandleFunc("GET /pictures/{slug}/{deploy}", func(w http.ResponseWriter, r *http.Request) {
		pic, err := client().ReadThumbnail(r.Context(), r.PathValue("slug"), r.PathValue("deploy"), apps.ThumbnailSmall)
		if errors.Is(err, latere.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "the picture could not be read", http.StatusBadGateway)
			return
		}
		defer func() {
			if err := pic.Close(); err != nil {
				log.Print(err)
			}
		}()
		w.Header().Set("Content-Type", pic.MediaType())
		w.Header().Set("Cache-Control", pic.CacheControl())
		if pic.Size() >= 0 {
			w.Header().Set("Content-Length", strconv.FormatInt(pic.Size(), 10))
		}
		if _, err := io.Copy(w, pic); err != nil {
			log.Print(err)
		}
	})
}

// Showing a private site in a frame of another site's page, to the person
// signed in to that page.
func ExampleClient_CreateFramePass() {
	pass, err := client().CreateFramePass(context.Background(), "tide-tables", "https://tide-tables.latere.site/harbors/?day=today")
	if errors.Is(err, apps.ErrFrameUnavailable) {
		fmt.Println("link to the site instead of framing it")
		return
	}
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("<iframe src=%q></iframe>\n", pass.URL)
}

// Ending the person's sign-ins at private apps as they sign out of a page
// that framed them.
func ExampleClient_EndGateSessions() {
	if err := client().EndGateSessions(context.Background()); err != nil {
		log.Print("the sign-ins at private apps were not ended: ", err)
	}
}

// Reading what the person may do about an app's place in the gallery.
func ExampleClient_GetListing() {
	l, err := client().GetListing(context.Background(), "tide-tables")
	if err != nil {
		log.Fatal(err)
	}
	if l.Blocked != "" {
		fmt.Println("cannot ask now:", l.Blocked)
		return
	}
	if l.Decision != nil {
		fmt.Println("last review:", l.Decision.Outcome, l.Decision.Explanation)
	}
	fmt.Println(l.State, l.Actions)
}

// Asking to show an app in the gallery.
func ExampleClient_RequestListing() {
	l, err := client().RequestListing(context.Background(), "tide-tables", &apps.ListingParams{
		Category: apps.CategoryTools,
		Summary:  "High and low water for the harbors of the North Sea.",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(l.State)
}

// Taking an app out of the gallery.
func ExampleClient_WithdrawListing() {
	if err := client().WithdrawListing(context.Background(), "tide-tables"); err != nil {
		log.Fatal(err)
	}
}
