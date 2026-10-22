// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package web_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
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
