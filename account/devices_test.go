// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package account_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"latere.ai/x/sdk-go/account"
	"latere.ai/x/sdk-go/latere"
)

const device = `{"id":"dev-q7v3k9m2abcdefgh","name":"Work laptop","status":"offline","online":false,"os":"darwin","arch":"arm64",
"isolation":"process","folders":[],"last_seen":"2026-10-18T10:00:00Z","created_at":"2026-10-18T09:00:00Z"}`

// deviceAPI is a Client of a server h answers, with the given retries and
// no wait between them.
func deviceAPI(t *testing.T, retries int, h http.HandlerFunc) *account.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	cfg, err := latere.NewConfig(latere.WithBaseURL(srv.URL), latere.WithTokenSource(latere.StaticToken("tok")),
		latere.WithMaxRetries(retries), latere.WithBackoff(time.Millisecond, time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	return account.New(cfg)
}

func writeError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, `{"error":{"code":"`+code+`","message":"No."}}`)
}

func TestRenameDevice(t *testing.T) {
	var method, path string
	var body map[string]any
	c := deviceAPI(t, 0, func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.EscapedPath()
		body = nil
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		switch body["name"] {
		case "Work laptop":
			_, _ = io.WriteString(w, device)
		case "\x07":
			writeError(w, http.StatusBadRequest, "invalid_name")
		default:
			writeError(w, http.StatusNotFound, "not_found")
		}
	})
	d, err := c.RenameDevice(t.Context(), "dev-q7v3k9m2abcdefgh", "Work laptop")
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "Work laptop" || d.Status != account.DeviceOffline || d.LastSeen.IsZero() || len(d.RawJSON()) == 0 {
		t.Errorf("renamed %+v", d)
	}
	if method != http.MethodPatch || path != "/v1/account/devices/dev-q7v3k9m2abcdefgh" || len(body) != 1 {
		t.Errorf("sent %s %s %v", method, path, body)
	}
	var apiErr *latere.Error
	if _, err := c.RenameDevice(t.Context(), "dev-q7v3k9m2abcdefgh", "\x07"); !errors.As(err, &apiErr) || apiErr.Code != "invalid_name" {
		t.Errorf("a name that does not print: %v", err)
	}
	if _, err := c.RenameDevice(t.Context(), "dev-gone", "Other"); !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("a computer the person does not hold: %v", err)
	}
	for _, args := range [][2]string{{"", "Work laptop"}, {"dev-q7v3k9m2abcdefgh", ""}, {"dev-q7v3k9m2abcdefgh", "  "}} {
		method = ""
		if _, err := c.RenameDevice(t.Context(), args[0], args[1]); err == nil || method != "" {
			t.Errorf("RenameDevice(%q, %q) was sent: %v", args[0], args[1], err)
		}
	}
}

func TestRenameDeviceIsRetried(t *testing.T) {
	var n atomic.Int32
	c := deviceAPI(t, 2, func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			writeError(w, http.StatusServiceUnavailable, "devices_unavailable")
			return
		}
		_, _ = io.WriteString(w, device)
	})
	if _, err := c.RenameDevice(t.Context(), "dev-q7v3k9m2abcdefgh", "Work laptop"); err != nil || n.Load() != 2 {
		t.Errorf("a rename after a 503: %v, after %d attempts", err, n.Load())
	}
}

func TestRemoveDevice(t *testing.T) {
	var method, path string
	c := deviceAPI(t, 0, func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.EscapedPath()
		switch r.URL.Path {
		case "/v1/account/devices/dev-q7v3k9m2abcdefgh":
			w.WriteHeader(http.StatusNoContent)
		case "/v1/account/devices/dev-removedremovedab":
			writeError(w, http.StatusNotFound, "not_found")
		case "/v1/account/devices/dev-proxyproxyproxyp":
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, "<html>Not Found</html>")
		default:
			writeError(w, http.StatusForbidden, "person_required")
		}
	})
	if err := c.RemoveDevice(t.Context(), "dev-q7v3k9m2abcdefgh"); err != nil {
		t.Fatal(err)
	}
	if method != http.MethodDelete || path != "/v1/account/devices/dev-q7v3k9m2abcdefgh" {
		t.Errorf("sent %s %s", method, path)
	}
	if err := c.RemoveDevice(t.Context(), "dev-removedremovedab"); err != nil {
		t.Errorf("a computer removed before: %v", err)
	}
	if err := c.RemoveDevice(t.Context(), "dev-proxyproxyproxyp"); !errors.Is(err, latere.ErrNotFound) {
		t.Errorf("a 404 that is not the API's: %v", err)
	}
	if err := c.RemoveDevice(t.Context(), "dev-keyskeyskeyskey"); !errors.Is(err, latere.ErrForbidden) {
		t.Errorf("a key's token: %v", err)
	}
	method = ""
	if err := c.RemoveDevice(t.Context(), ""); err == nil || method != "" {
		t.Errorf("a removal of no id was sent: %v", err)
	}
}

// TestRemovingTwiceSucceeds: a removal sent again after its answer was
// lost meets the API's 404, and still reports the computer removed.
func TestRemovingTwiceSucceeds(t *testing.T) {
	var n atomic.Int32
	c := deviceAPI(t, 2, func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			// The first removal goes through and its answer is lost.
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Error("the server cannot drop a connection")
				return
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close() // dropping the connection is the point
			return
		}
		writeError(w, http.StatusNotFound, "not_found")
	})
	if err := c.RemoveDevice(t.Context(), "dev-q7v3k9m2abcdefgh"); err != nil || n.Load() != 2 {
		t.Errorf("a removal whose answer was lost: %v, after %d attempts", err, n.Load())
	}
}

func TestGetEnvironmentsContext(t *testing.T) {
	var path string
	c := deviceAPI(t, 0, func(w http.ResponseWriter, r *http.Request) {
		path = r.Method + " " + r.URL.Path
		_, _ = io.WriteString(w, `{"default_environment":"default","tenant":{"id":"p-969ca7dc28704df038c75afb9e33283f",
			"labels":{"platform.latere.ai/principal":"8f1c","platform.latere.ai/tenant":"p-969ca7dc28704df038c75afb9e33283f"},
			"org_id":"","owner":"https://auth.latere.ai|8f1c"}}`)
	})
	got, err := c.GetEnvironmentsContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if path != "GET /v1/account/environments/context" {
		t.Errorf("asked %s", path)
	}
	if got.DefaultEnvironment != "default" || got.Tenant.ID != "p-969ca7dc28704df038c75afb9e33283f" || got.Tenant.OrgID != "" ||
		got.Tenant.Labels["platform.latere.ai/principal"] != "8f1c" || len(got.Tenant.Labels) != 2 || len(got.RawJSON()) == 0 {
		t.Errorf("read %+v", got)
	}
}

func TestEnvironmentsUnconfigured(t *testing.T) {
	c := deviceAPI(t, 0, func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusServiceUnavailable, "environments_unconfigured")
	})
	var apiErr *latere.Error
	if _, err := c.GetEnvironmentsContext(t.Context()); !errors.As(err, &apiErr) || apiErr.Code != "environments_unconfigured" {
		t.Errorf("a deployment without Environments: %v", err)
	}
}
