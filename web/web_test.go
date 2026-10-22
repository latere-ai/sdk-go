// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// The web part of the API has no reference document yet, so unlike the
// other packages' spec tests these hold the client to the answers the
// platform's icon route gives: a PNG with its headers, icon_not_found for
// a site without one, invalid_host for a host it reads none for, and
// icon_unavailable with a Retry-After while its budget is spent.

package web_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"latere.ai/x/sdk-go/latere"
	"latere.ai/x/sdk-go/web"
)

// png is the start of a PNG file, enough to stand for an icon's bytes.
var png = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

// iconAPI is a Client of a server h answers, whose token source counts the
// tokens it gives, with the given retries and no wait between them.
func iconAPI(t *testing.T, retries int, h http.HandlerFunc) (*web.Client, *atomic.Int32) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	var tokens atomic.Int32
	src := latere.TokenSourceFunc(func(context.Context) (latere.Token, error) {
		tokens.Add(1)
		return latere.Token{Value: "tok"}, nil
	})
	cfg, err := latere.NewConfig(latere.WithBaseURL(srv.URL), latere.WithTokenSource(src),
		latere.WithMaxRetries(retries), latere.WithBackoff(time.Millisecond, time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	return web.New(cfg), &tokens
}

func writeError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, `{"error":{"code":"`+code+`","message":"No.","details":{"detail":"why"}}}`)
}

func TestGetIcon(t *testing.T) {
	var path, size, accept, auth string
	c, tokens := iconAPI(t, 0, func(w http.ResponseWriter, r *http.Request) {
		path, size, accept, auth = r.URL.EscapedPath(), r.URL.Query().Get("size"), r.Header.Get("Accept"), r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=604800, stale-while-revalidate=2592000")
		w.Header().Set("ETag", `"3f2a"`)
		_, _ = w.Write(png)
	})
	icon, err := c.GetIcon(t.Context(), "techcrunch.com", web.IconSmall)
	if err != nil {
		t.Fatal(err)
	}
	if icon == nil || !bytes.Equal(icon.Data, png) || icon.MediaType != "image/png" {
		t.Errorf("icon = %+v", icon)
	}
	if path != "/v1/web/icons/techcrunch.com" || size != "32" || !strings.HasPrefix(accept, "image/") {
		t.Errorf("sent %s?size=%s with Accept %q", path, size, accept)
	}
	if auth != "" || tokens.Load() != 0 {
		t.Errorf("sent Authorization %q after %d tokens; the route reads no credential", auth, tokens.Load())
	}
	if _, err := c.GetIcon(t.Context(), "docs.python.org", web.IconLarge); err != nil || size != "64" {
		t.Errorf("the large size sent size=%s: %v", size, err)
	}
	if _, err := c.GetIcon(t.Context(), "bücher.de", web.IconSmall); err != nil || path != "/v1/web/icons/b%C3%BCcher.de" {
		t.Errorf("a name beyond ASCII went to %s: %v", path, err)
	}
}

// A site without an icon is no error: the platform's icon_not_found
// answers nil. A 404 without its envelope, such as a proxy's, stays one.
func TestGetIconOfASiteWithoutOne(t *testing.T) {
	c, _ := iconAPI(t, 0, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/proxied.org") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=86400")
		writeError(w, http.StatusNotFound, "icon_not_found")
	})
	icon, err := c.GetIcon(t.Context(), "nothing-here.org", web.IconSmall)
	if icon != nil || err != nil {
		t.Errorf("a site without an icon: %+v, %v; want nil and no error", icon, err)
	}
	if _, err := c.GetIcon(t.Context(), "proxied.org", web.IconSmall); !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("a 404 without the API's envelope: %v, want an error", err)
	}
}

// A host the platform reads no icon for matches ErrInvalidHost, and still
// unwraps to the API's answer.
func TestGetIconOfAHostThePlatformRefuses(t *testing.T) {
	c, _ := iconAPI(t, 2, func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusBadRequest, "invalid_host")
	})
	icon, err := c.GetIcon(t.Context(), "localhost", web.IconSmall)
	if icon != nil || !errors.Is(err, web.ErrInvalidHost) {
		t.Fatalf("localhost: %+v, %v; want ErrInvalidHost", icon, err)
	}
	var apiErr *latere.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest || apiErr.Details["detail"] != "why" {
		t.Errorf("the API's answer is not kept: %v", err)
	}
	if errors.Is(err, latere.ErrNotFound) {
		t.Error("a refused host reads as not found")
	}
	if !strings.Contains(err.Error(), "400 invalid_host") {
		t.Errorf("the error says %q, not the API's answer", err)
	}
}

// A miss past the platform's budget answers 503 with a Retry-After, and
// the call asks again once it passed.
func TestGetIconWaitsOutTheBudget(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			writeError(w, http.StatusServiceUnavailable, "icon_unavailable")
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	}))
	t.Cleanup(srv.Close)
	cfg, err := latere.NewConfig(latere.WithBaseURL(srv.URL), latere.WithTokenSource(latere.StaticToken("tok")),
		latere.WithBackoff(time.Millisecond, 2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	icon, err := web.New(cfg).GetIcon(t.Context(), "techcrunch.com", web.IconSmall)
	if err != nil || icon == nil || calls.Load() != 2 {
		t.Errorf("after a 503: %+v, %v in %d requests", icon, err, calls.Load())
	}
	if waited := time.Since(start); waited < time.Second {
		t.Errorf("asked again after %v, before the Retry-After passed", waited)
	}
}

// An answer is read up to MaxIconBytes and refused past it, never cut;
// one that is no picture, or empty, is refused.
func TestGetIconBounds(t *testing.T) {
	cases := []struct {
		name, host string
		answer     func(http.ResponseWriter)
		want       error
	}{
		{"one past the bound, its length stated", "stated.org", func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Content-Length", strconv.Itoa(web.MaxIconBytes+1))
			_, _ = w.Write(make([]byte, web.MaxIconBytes+1))
		}, web.ErrIconTooLarge},
		{"one past the bound, streamed", "streamed.org", func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "image/png")
			w.(http.Flusher).Flush()
			_, _ = w.Write(make([]byte, web.MaxIconBytes+1))
		}, web.ErrIconTooLarge},
		{"a program", "svg.org", func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "image/svg+xml")
			_, _ = io.WriteString(w, "<svg/>")
		}, nil},
		{"a page", "page.org", func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, "<html></html>")
		}, nil},
		{"no type", "untyped.org", func(w http.ResponseWriter) {
			w.Header()["Content-Type"] = nil
			_, _ = w.Write(png)
		}, nil},
		{"no bytes", "empty.org", func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "image/png")
		}, nil},
	}
	answers := map[string]func(http.ResponseWriter){}
	for _, tc := range cases {
		answers["/v1/web/icons/"+tc.host] = tc.answer
	}
	c, _ := iconAPI(t, 0, func(w http.ResponseWriter, r *http.Request) { answers[r.URL.Path](w) })
	for _, tc := range cases {
		icon, err := c.GetIcon(t.Context(), tc.host, web.IconSmall)
		if icon != nil || err == nil {
			t.Errorf("%s: %+v, %v; want an error", tc.name, icon, err)
		}
		if tc.want != nil && !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, err, tc.want)
		}
	}
	at := func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(make([]byte, web.MaxIconBytes))
	}
	answers["/v1/web/icons/at-the-bound.org"] = at
	if icon, err := c.GetIcon(t.Context(), "at-the-bound.org", web.IconSmall); err != nil || len(icon.Data) != web.MaxIconBytes {
		t.Errorf("an icon at the bound: %v", err)
	}
}

// A size the platform does not encode, and a host that is none, are
// refused before anything is sent.
func TestGetIconRefusesBeforeSending(t *testing.T) {
	var calls atomic.Int32
	c, _ := iconAPI(t, 0, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
	for _, tc := range []struct {
		host string
		size web.IconSize
	}{{"techcrunch.com", 0}, {"techcrunch.com", 48}, {"", web.IconSmall}, {".", web.IconSmall}, {"..", web.IconSmall}} {
		if _, err := c.GetIcon(t.Context(), tc.host, tc.size); err == nil {
			t.Errorf("GetIcon(%q, %d): nil error", tc.host, tc.size)
		}
	}
	if calls.Load() != 0 {
		t.Errorf("%d requests sent", calls.Load())
	}
	if _, err := web.New(nil).GetIcon(context.Background(), "techcrunch.com", web.IconSmall); err == nil {
		t.Error("a nil Config: nil error")
	}
}
