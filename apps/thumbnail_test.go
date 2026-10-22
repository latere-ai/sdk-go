// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package apps_test

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"

	"latere.ai/x/sdk-go/apps"
	"latere.ai/x/sdk-go/latere"
)

const deployID = "9f2c6a1e-2b7d-4c4f-9e0a-5d1b7f3c8a21"

func TestReadThumbnail(t *testing.T) {
	picture := []byte("RIFF\x00\x00\x00\x00WEBPVP8 ")
	c, f := newFake(t, 0, map[string]http.HandlerFunc{
		"GET /v1/apps/apps/tide-tables/deploys/" + deployID + "/thumbnail": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "image/webp")
			w.Header().Set("Cache-Control", "private, max-age=86400")
			w.Header().Set("ETag", `"t1"`)
			w.Header().Set("Content-Length", fmt.Sprint(len(picture)))
			_, _ = w.Write(picture)
		},
		"GET /v1/apps/apps/tide-tables/deploys/gone/thumbnail": answer(http.StatusNotFound,
			`{"error":{"code":"thumbnail_not_found","message":"No picture.","details":{"deploy_id":"gone","thumbnail_status":"pending","reason":""}}}`),
		"GET /v1/apps/apps/private/deploys/" + deployID + "/thumbnail": answer(http.StatusNotFound,
			`{"error":{"code":"app_not_found","message":"No app.","details":{"slug":"private"}}}`),
	})
	r, err := c.ReadThumbnail(t.Context(), "tide-tables", deployID, apps.ThumbnailSmall)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if string(got) != string(picture) || r.MediaType() != "image/webp" || r.Size() != int64(len(picture)) ||
		r.ETag() != `"t1"` || r.CacheControl() != "private, max-age=86400" {
		t.Errorf("read %q as %s, %d bytes, %s, %s", got, r.MediaType(), r.Size(), r.ETag(), r.CacheControl())
	}

	_, err = c.ReadThumbnail(t.Context(), "tide-tables", "gone", 0)
	var apiErr *latere.Error
	if !errors.Is(err, apps.ErrNoThumbnail) || !errors.Is(err, latere.ErrNotFound) || !errors.As(err, &apiErr) ||
		apiErr.Details["thumbnail_status"] != "pending" {
		t.Errorf("a deploy without a picture: %v", err)
	}
	// A missing app is no missing picture.
	if _, err := c.ReadThumbnail(t.Context(), "private", deployID, apps.ThumbnailLarge); !errors.Is(err, latere.ErrNotFound) || errors.Is(err, apps.ErrNoThumbnail) {
		t.Errorf("a missing app: %v", err)
	}

	for _, bad := range []struct {
		slug, id string
		width    apps.ThumbnailWidth
	}{{"", deployID, 0}, {"tide-tables", "", 0}, {"tide-tables", deployID, 800}} {
		if _, err := c.ReadThumbnail(t.Context(), bad.slug, bad.id, bad.width); err == nil {
			t.Errorf("ReadThumbnail(%q, %q, %d) was sent", bad.slug, bad.id, bad.width)
		}
	}
	calls := f.record()
	if len(calls) != 3 || calls[0].Query != "width=640" || calls[1].Query != "" || calls[2].Query != "width=1280" {
		t.Errorf("requests = %+v", calls)
	}
	if calls[0].Header.Get("Accept") != "*/*" {
		t.Errorf("Accept = %q, want any type", calls[0].Header.Get("Accept"))
	}
}

func TestThumbnailDeploy(t *testing.T) {
	for _, c := range []struct {
		url, slug, id string
		ok            bool
	}{
		{"/v1/apps/apps/hello-20417/deploys/" + deployID + "/thumbnail", "hello-20417", deployID, true},
		{"/v1/apps/hello-20417/deploys/" + deployID + "/thumbnail?width=640", "hello-20417", deployID, true},
		{"/v1/apps/apps/hello-20417/deploys/" + deployID, "", "", false},
		{"/v1/apps/thumbnails/a.png", "", "", false},
		{"", "", "", false},
		{"/v1/apps/apps//deploys/x/thumbnail", "", "", false},
	} {
		slug, id, ok := apps.Thumbnail{URL: c.url}.Deploy()
		if slug != c.slug || id != c.id || ok != c.ok {
			t.Errorf("Deploy(%q) = %q, %q, %v; want %q, %q, %v", c.url, slug, id, ok, c.slug, c.id, c.ok)
		}
	}
}
