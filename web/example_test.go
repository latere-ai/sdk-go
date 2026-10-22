// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package web_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"latere.ai/x/sdk-go/latere"
	"latere.ai/x/sdk-go/web"
)

// The icon to draw beside a link in an answer: the site's, read once
// through the platform, or none, in which case the link is drawn alone.
func ExampleClient_GetIcon() {
	ctx := context.Background()
	cfg, err := latere.NewConfig()
	if err != nil {
		log.Fatal(err)
	}
	link, err := url.Parse("https://www.techcrunch.com/2026/10/20/seed-rounds/")
	if err != nil {
		log.Fatal(err)
	}
	// The site is the link's host, without "www.", as Latere Chat names it.
	site := strings.TrimPrefix(link.Hostname(), "www.")
	icon, err := web.New(cfg).GetIcon(ctx, site, web.IconSmall)
	switch {
	case errors.Is(err, web.ErrInvalidHost):
		fmt.Println(site, "has no icon the platform reads")
	case err != nil:
		log.Fatal(err)
	case icon == nil:
		fmt.Println(site, "has no icon")
	default:
		fmt.Println(site, icon.MediaType, len(icon.Data), "bytes")
	}
}

// A server passing a picture an answer shows on to the browser that asked
// for it, as it reads it, so the browser loads nothing from the website.
func ExampleClient_ReadImage() {
	cfg, err := latere.NewConfig()
	if err != nil {
		log.Fatal(err)
	}
	client := web.New(cfg)
	http.HandleFunc("GET /pictures", func(w http.ResponseWriter, r *http.Request) {
		img, err := client.ReadImage(r.Context(), r.URL.Query().Get("url"), web.ImageSmall)
		switch {
		case errors.Is(err, web.ErrNoImage):
			http.NotFound(w, r)
			return
		case err != nil:
			http.Error(w, "The picture could not be read.", http.StatusBadGateway)
			return
		}
		defer img.Close()
		w.Header().Set("Content-Type", img.MediaType())
		w.Header().Set("Cache-Control", img.CacheControl())
		if img.Size() >= 0 {
			w.Header().Set("Content-Length", strconv.FormatInt(img.Size(), 10))
		}
		if _, err := io.Copy(w, img); err != nil {
			log.Print("the picture's answer was cut off: ", err)
		}
	})
}

// A server rendering a share link's page passes on the pictures its
// answers show, as anyone holding the link reads them.
func ExampleClient_ReadSharedImage() {
	cfg, err := latere.NewConfig(latere.WithoutCredentials())
	if err != nil {
		log.Fatal(err)
	}
	client := web.New(cfg)
	http.HandleFunc("GET /share/{token}/pictures", func(w http.ResponseWriter, r *http.Request) {
		img, err := client.ReadSharedImage(r.Context(), r.PathValue("token"), r.URL.Query().Get("url"), web.ImageSmall)
		switch {
		case errors.Is(err, web.ErrNoImage), errors.Is(err, latere.ErrNotFound):
			http.NotFound(w, r)
			return
		case err != nil:
			http.Error(w, "The picture could not be read.", http.StatusBadGateway)
			return
		}
		defer img.Close()
		w.Header().Set("Content-Type", img.MediaType())
		w.Header().Set("Cache-Control", img.CacheControl())
		if _, err := io.Copy(w, img); err != nil {
			log.Print("the picture's answer was cut off: ", err)
		}
	})
}
