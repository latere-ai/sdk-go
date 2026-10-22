// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package web_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"latere.ai/x/sdk-go/latere"
	"latere.ai/x/sdk-go/web"
)

// The image routes answer a picture with its headers, or a refusal whose
// code says why there is none. These tests hold the client to those
// answers, as the platform's routes give them.

// jpeg is the start of a JPEG file, enough to stand for a picture.
var jpeg = []byte("\xff\xd8\xff\xe0\x00\x10JFIF")

const address = "https://example.org/a picture.png?size=large"

func TestReadImage(t *testing.T) {
	c, tokens := iconAPI(t, 0, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/web/images" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if q := r.URL.Query(); q.Get("url") != address || q.Get("width") != "800" || len(q) != 2 {
			t.Errorf("query = %v", q)
		}
		if r.Header.Get("Authorization") != "Bearer tok" || r.Header.Get("Accept") != "image/*" {
			t.Errorf("Authorization %q, Accept %q", r.Header.Get("Authorization"), r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Content-Length", strconv.Itoa(len(jpeg)))
		w.Header().Set("Cache-Control", "private, max-age=86400")
		_, _ = w.Write(jpeg)
	})
	img, err := c.ReadImage(context.Background(), address, web.ImageSmall)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(img)
	if err := errors.Join(err, img.Close()); err != nil {
		t.Fatal(err)
	}
	if string(b) != string(jpeg) || img.MediaType() != "image/jpeg" || img.Size() != int64(len(jpeg)) ||
		img.CacheControl() != "private, max-age=86400" {
		t.Errorf("read %q as %s of %d bytes, kept %q", b, img.MediaType(), img.Size(), img.CacheControl())
	}
	if tokens.Load() != 1 {
		t.Errorf("asked for %d tokens; a picture is read as the person", tokens.Load())
	}
}

func TestReadSharedImage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/v1/shares/tok%2F1/images" {
			writeError(w, http.StatusNotFound, "share_not_found")
			return
		}
		if r.Header.Get("Authorization") != "" {
			t.Errorf("Authorization = %q from a Config without credentials", r.Header.Get("Authorization"))
		}
		if q := r.URL.Query(); q.Get("url") != address || q.Get("width") != "1600" {
			t.Errorf("query = %v", q)
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	}))
	defer srv.Close()
	cfg, err := latere.NewConfig(latere.WithBaseURL(srv.URL), latere.WithoutCredentials(), latere.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	c := web.New(cfg)
	img, err := c.ReadSharedImage(context.Background(), "tok/1", address, web.ImageLarge)
	if err != nil {
		t.Fatal(err)
	}
	if err := img.Close(); err != nil {
		t.Fatal(err)
	}
	if img.MediaType() != "image/png" || img.Size() != int64(len(png)) {
		t.Errorf("read %s of %d bytes", img.MediaType(), img.Size())
	}
	if _, err := c.ReadSharedImage(context.Background(), "tok_gone", address, web.ImageLarge); !errors.Is(err, latere.ErrNotFound) || errors.Is(err, web.ErrNoImage) {
		t.Errorf("a stopped link: %v, want ErrNotFound alone", err)
	}
	if _, err := c.ReadSharedImage(context.Background(), "", address, web.ImageLarge); err == nil {
		t.Error("a read of no token was sent")
	}
}

// Each refusal that says the address holds no picture matches ErrNoImage
// and keeps the API's error; any other stays the API's error alone.
func TestReadImageWithoutAPicture(t *testing.T) {
	for _, tc := range []struct {
		status int
		code   string
		none   bool
	}{
		{http.StatusBadRequest, "invalid_url", true},
		{http.StatusNotFound, "image_not_found", true},
		{http.StatusUnsupportedMediaType, "image_unsupported", true},
		{http.StatusUnsupportedMediaType, "image_too_large", true},
		{http.StatusBadGateway, "image_unreachable", true},
		{http.StatusUnauthorized, "unauthorized", false},
		{http.StatusForbidden, "forbidden", false},
	} {
		c, _ := iconAPI(t, 0, func(w http.ResponseWriter, r *http.Request) { writeError(w, tc.status, tc.code) })
		_, err := c.ReadImage(context.Background(), address, web.ImageLarge)
		var apiErr *latere.Error
		if errors.Is(err, web.ErrNoImage) != tc.none || !errors.As(err, &apiErr) || apiErr.Code != tc.code {
			t.Errorf("%s: %v; want ErrNoImage %v and the API's error", tc.code, err, tc.none)
		}
	}
}

// A busy image route answers 503 with a wait, which the SDK waits out.
func TestReadImageWaitsOutTheBudget(t *testing.T) {
	calls := 0
	c, _ := iconAPI(t, 2, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "0")
			writeError(w, http.StatusServiceUnavailable, "image_unavailable")
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	})
	img, err := c.ReadImage(context.Background(), address, web.ImageLarge)
	if err != nil {
		t.Fatal(err)
	}
	if err := img.Close(); err != nil || calls != 2 {
		t.Errorf("close: %v after %d calls", err, calls)
	}
}

// An answer that is no picture is refused and its body closed.
func TestReadImageOfAnAnswerThatIsNoPicture(t *testing.T) {
	c, _ := iconAPI(t, 0, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<html>")
	})
	if _, err := c.ReadImage(context.Background(), address, web.ImageLarge); err == nil || !strings.Contains(err.Error(), "not a picture") {
		t.Errorf("err = %v, want the answer refused", err)
	}
}

func TestReadImageRefusesBeforeSending(t *testing.T) {
	sent := 0
	c, _ := iconAPI(t, 0, func(w http.ResponseWriter, r *http.Request) { sent++ })
	for _, tc := range []struct {
		address string
		width   web.ImageWidth
	}{
		{address, 0},
		{address, 640},
		{"", web.ImageLarge},
		{"example.org/a.png", web.ImageLarge},
		{"ftp://example.org/a.png", web.ImageLarge},
		{"https:///a.png", web.ImageLarge},
		{"https://example.org/" + strings.Repeat("a", web.MaxImageAddress), web.ImageLarge},
	} {
		if _, err := c.ReadImage(context.Background(), tc.address, tc.width); err == nil {
			t.Errorf("ReadImage(%.40q, %d) was not refused", tc.address, tc.width)
		} else if strings.Contains(err.Error(), "example.org") {
			t.Errorf("the refusal quotes the address: %v", err)
		}
	}
	if sent != 0 {
		t.Errorf("sent %d requests", sent)
	}
}
