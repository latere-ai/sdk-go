// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package messaging_test

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"latere.ai/x/sdk-go/latere"
	"latere.ai/x/sdk-go/messaging"
	"latere.ai/x/sdk-go/mls"
)

// A request is one request the fake service received.
type request struct {
	method, path, query, device, auth string
	// body is the request's JSON body, nil when it sent none.
	body map[string]any
}

// fakeService is a Client of a server that records each request and
// answers it with answer, which learns how many came before it. It
// allows retries, with no wait between them.
func fakeService(t *testing.T, retries int, answer func(w http.ResponseWriter, r *http.Request, n int)) (*messaging.Client, func() []request) {
	t.Helper()
	var mu sync.Mutex
	var got []request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		q := request{method: r.Method, path: r.URL.EscapedPath(), query: r.URL.RawQuery,
			device: strings.Join(r.Header.Values(messaging.DeviceHeader), ","), auth: r.Header.Get("Authorization")}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &q.body); err != nil {
				t.Errorf("%s %s sent a body that is no JSON object: %s", r.Method, r.URL.Path, raw)
			}
		}
		mu.Lock()
		n := len(got)
		got = append(got, q)
		mu.Unlock()
		answer(w, r, n)
	}))
	t.Cleanup(srv.Close)
	cfg, err := latere.NewConfig(latere.WithBaseURL(srv.URL), latere.WithTokenSource(latere.StaticToken("person-token")),
		latere.WithMaxRetries(retries), latere.WithBackoff(time.Millisecond, time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	return messaging.NewClient(cfg, "phone-1"), func() []request {
		mu.Lock()
		defer mu.Unlock()
		return append([]request(nil), got...)
	}
}

func answer(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func refuse(w http.ResponseWriter, status int, code string) {
	answer(w, status, `{"error":{"code":"`+code+`","message":"No.","details":{"detail":"for the test"}}}`)
}

// refusedWith returns the error of a call the service refuses with
// status and code, sent once.
func refusedWith(t *testing.T, status int, code string) error {
	t.Helper()
	c, _ := fakeService(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) { refuse(w, status, code) })
	_, err := c.Conversation(t.Context(), "c1")
	return err
}

// b64 is bytes as the wire has them.
func b64(t *testing.T, b []byte) string {
	t.Helper()
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Trim(string(raw), `"`)
}

func newRecord(t *testing.T) messaging.DeviceRecord {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	r, err := messaging.NewDeviceRecord("alice", "phone-1", key, enc.PublicKey(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Sign(key); err != nil {
		t.Fatal(err)
	}
	return r
}

const conversation = `{"id":"c1","kind":"group","level":"reads_along","people":["alice","bob"],"owners":["alice"],
"epoch":3,"seq":9,"created":"2026-10-29T10:00:00Z","updated":"2026-10-29T10:41:07Z"}`

// Each call is the request the document describes: its method, path,
// query, body, and the device header exactly on the calls that act for
// the device, and the answer decodes into the Client's model.
func TestCalls(t *testing.T) {
	rec := newRecord(t)
	signer, err := mls.GenerateSignatureKey(messaging.Suite)
	if err != nil {
		t.Fatal(err)
	}
	id, err := messaging.DeviceIdentity("alice", "phone-1")
	if err != nil {
		t.Fatal(err)
	}
	kp, _, err := mls.NewKeyPackage(signer, id.Credential(), messaging.KeyPackageOptions())
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	for _, c := range []struct {
		name                        string
		status                      int
		body                        string
		method, path, query, device string
		sent                        map[string]any
		call                        func(c *messaging.Client) error
	}{
		{"external sender", 200, `{"external_sender":{"signature_key":"AAEC","identity":"sermo"}}`, "GET", "/v1/messaging/service", "", "", nil,
			func(c *messaging.Client) error {
				es, err := c.ExternalSender(ctx)
				if err == nil && (!bytes.Equal(es.SignatureKey, []byte{0, 1, 2}) || !bytes.Equal(es.Credential.Identity, []byte("sermo"))) {
					t.Errorf("external sender %+v", es)
				}
				return err
			}},
		{"devices", 200, `{"records":[],"revocations":[]}`, "GET", "/v1/messaging/people/bob%2Fx/devices", "", "", nil,
			func(c *messaging.Client) error { _, err := c.Devices(ctx, "bob/x"); return err }},
		{"directory", 200, `{"records":[],"revocations":[]}`, "GET", "/v1/messaging/people/bob/devices", "", "", nil,
			func(c *messaging.Client) error {
				d, dropped, err := c.Directory(ctx, "bob", nil)
				if err == nil && (d.Person() != "bob" || dropped != 0 || len(d.Devices()) != 0) {
					t.Errorf("directory of %s, %d dropped", d.Person(), dropped)
				}
				return err
			}},
		{"register", 204, "", "PUT", "/v1/messaging/devices/phone-1", "", "", map[string]any{"id": "phone-1", "person": "alice"},
			func(c *messaging.Client) error { return c.RegisterDevice(ctx, rec) }},
		{"revoke", 204, "", "POST", "/v1/messaging/revocations", "", "", map[string]any{"by": "phone-1"},
			func(c *messaging.Client) error {
				return c.Revoke(ctx, messaging.Revocation{Person: "alice", Key: rec.SignatureKey, By: "phone-1", Signature: []byte{1}})
			}},
		{"put recovery", 204, "", "PUT", "/v1/messaging/recovery", "", "", map[string]any{"wrapped": b64(t, []byte("w"))},
			func(c *messaging.Client) error {
				return c.PutRecovery(ctx, messaging.Recovery{Record: rec, Wrapped: []byte("w")})
			}},
		{"recovery", 200, `{"record":{"person":"alice","id":"r"},"wrapped":"dw=="}`, "GET", "/v1/messaging/recovery", "", "", nil,
			func(c *messaging.Client) error {
				r, err := c.Recovery(ctx)
				if err == nil && (r.Record.ID != "r" || string(r.Wrapped) != "w") {
					t.Errorf("recovery %+v", r)
				}
				return err
			}},
		{"leave a transfer", 204, "", "POST", "/v1/messaging/devices/tablet/transfer", "", "", map[string]any{"sealed": b64(t, []byte("s"))},
			func(c *messaging.Client) error { return c.LeaveTransfer(ctx, "tablet", []byte("s")) }},
		{"take it", 200, `{"sealed":"cw=="}`, "GET", "/v1/messaging/devices/phone-1/transfer", "", "phone-1", nil,
			func(c *messaging.Client) error {
				s, err := c.TakeTransfer(ctx)
				if err == nil && string(s) != "s" {
					t.Errorf("sealed %q", s)
				}
				return err
			}},
		{"publish", 204, "", "POST", "/v1/messaging/key-packages", "", "phone-1",
			map[string]any{"key_packages": []any{b64(t, kp.Bytes())}, "last_resort": b64(t, kp.Bytes())},
			func(c *messaging.Client) error { return c.PublishKeyPackages(ctx, []*mls.KeyPackage{kp}, kp) }},
		{"publish none", 204, "", "POST", "/v1/messaging/key-packages", "", "phone-1", map[string]any{"key_packages": []any{}},
			func(c *messaging.Client) error { return c.PublishKeyPackages(ctx, nil, nil) }},
		{"count", 200, `{"count":7,"last_resort":true}`, "GET", "/v1/messaging/key-packages", "", "phone-1", nil,
			func(c *messaging.Client) error {
				n, err := c.KeyPackageCount(ctx)
				if err == nil && (n.Count != 7 || !n.LastResort) {
					t.Errorf("count %+v", n)
				}
				return err
			}},
		{"claim", 200, `{"key_packages":[{"device":"pixel","key_package":"AQ=="}]}`, "POST", "/v1/messaging/key-packages/claim", "", "",
			map[string]any{"person": "bob", "conversation": "c1"},
			func(c *messaging.Client) error {
				got, err := c.ClaimKeyPackages(ctx, "bob", "c1")
				if err == nil && (len(got) != 1 || got[0].Device != "pixel" || !bytes.Equal(got[0].KeyPackage, []byte{1})) {
					t.Errorf("claimed %+v", got)
				}
				return err
			}},
		{"create", 201, conversation, "POST", "/v1/messaging/conversations", "", "phone-1", map[string]any{"id": "c1", "group_info": "AQ=="},
			func(c *messaging.Client) error {
				got, err := c.CreateConversation(ctx, "c1", []byte{1})
				if err == nil && (got.ID != "c1" || got.Kind != messaging.KindGroup || got.Level != messaging.LevelReadsAlong ||
					got.Epoch != 3 || got.Seq != 9 || len(got.People) != 2 || !got.Updated.Equal(time.Date(2026, 10, 29, 10, 41, 7, 0, time.UTC))) {
					t.Errorf("conversation %+v", got)
				}
				return err
			}},
		{"list", 200, `{"conversations":[` + conversation + `],"next":"c1"}`, "GET", "/v1/messaging/conversations", "after=c0&limit=10", "", nil,
			func(c *messaging.Client) error {
				l, err := c.Conversations(ctx, "c0", 10)
				if err == nil && (len(l.Conversations) != 1 || l.Next != "c1") {
					t.Errorf("list %+v", l)
				}
				return err
			}},
		{"list by default", 200, `{"conversations":[]}`, "GET", "/v1/messaging/conversations", "", "", nil,
			func(c *messaging.Client) error { _, err := c.Conversations(ctx, "", 0); return err }},
		{"read", 200, conversation, "GET", "/v1/messaging/conversations/c1", "", "", nil,
			func(c *messaging.Client) error { _, err := c.Conversation(ctx, "c1"); return err }},
		{"group info", 200, `{"group_info":"AgM="}`, "GET", "/v1/messaging/conversations/c1/group-info", "", "phone-1", nil,
			func(c *messaging.Client) error {
				gi, err := c.GroupInfo(ctx, "c1")
				if err == nil && !bytes.Equal(gi, []byte{2, 3}) {
					t.Errorf("group info %v", gi)
				}
				return err
			}},
		{"send", 201, `{"seq":10,"epoch":3,"kind":"commit"}`, "POST", "/v1/messaging/conversations/c1/messages", "", "phone-1",
			map[string]any{"message": "AQ==", "group_info": "Ag=="},
			func(c *messaging.Client) error {
				s, err := c.Send(ctx, "c1", messaging.Outgoing{Message: []byte{1}, GroupInfo: []byte{2}})
				if err == nil && (s.Seq != 10 || s.Kind != messaging.MessageCommit) {
					t.Errorf("sent %+v", s)
				}
				return err
			}},
		{"messages", 200, `{"messages":[{"seq":10,"epoch":3,"kind":"application","message":"AQ==","person":"bob","device":"pixel",
"created":"2026-10-29T10:41:07Z"}],"next":10}`, "GET", "/v1/messaging/conversations/c1/messages", "after=9&limit=5&wait=30", "phone-1", nil,
			func(c *messaging.Client) error {
				l, err := c.Messages(ctx, "c1", 9, 5, time.Hour)
				if err == nil && (len(l.Messages) != 1 || l.Next != 10 || l.Messages[0].Person != "bob" || l.Messages[0].Kind != messaging.MessageApplication) {
					t.Errorf("messages %+v", l)
				}
				return err
			}},
		{"messages at once", 200, `{"messages":[],"next":0}`, "GET", "/v1/messaging/conversations/c1/messages", "", "phone-1", nil,
			func(c *messaging.Client) error { _, err := c.Messages(ctx, "c1", 0, 0, 0); return err }},
		{"welcomes", 200, `{"welcomes":[{"id":"w1","conversation":"c1","seq":4,"welcome":"AQ==","notice":"Ag==","created":"2026-10-29T10:41:07Z"}]}`,
			"GET", "/v1/messaging/welcomes", "", "phone-1", nil,
			func(c *messaging.Client) error {
				ws, err := c.Welcomes(ctx)
				if err == nil && (len(ws) != 1 || ws[0].ID != "w1" || !bytes.Equal(ws[0].Notice, []byte{2})) {
					t.Errorf("welcomes %+v", ws)
				}
				return err
			}},
		{"delete a welcome", 204, "", "DELETE", "/v1/messaging/welcomes/w1", "", "phone-1", nil,
			func(c *messaging.Client) error { return c.DeleteWelcome(ctx, "w1") }},
		{"membership", 200, `{"person":"bob","member":true}`, "GET", "/v1/messaging/conversations/c1/people/bob", "", "", nil,
			func(c *messaging.Client) error {
				ok, err := c.IsMember(ctx, "c1", "bob")
				if err == nil && !ok {
					t.Error("bob is not a member")
				}
				return err
			}},
	} {
		t.Run(c.name, func(t *testing.T) {
			client, got := fakeService(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) {
				if c.body == "" {
					w.WriteHeader(c.status)
					return
				}
				answer(w, c.status, c.body)
			})
			if err := c.call(client); err != nil {
				t.Fatal(err)
			}
			reqs := got()
			if len(reqs) != 1 {
				t.Fatalf("sent %d requests", len(reqs))
			}
			r := reqs[0]
			if r.method != c.method || r.path != c.path || r.query != c.query || r.device != c.device || r.auth != "Bearer person-token" {
				t.Errorf("sent %s %s?%s as %q with %q; want %s %s?%s as %q", r.method, r.path, r.query, r.device, r.auth, c.method, c.path, c.query, c.device)
			}
			if e, ok := callOf(r.method, r.path); !ok {
				t.Errorf("%s %s is no call of the document", r.method, r.path)
			} else if (r.device != "") != e.asDevice {
				t.Errorf("%s %s sent the device header %q; the document requires it: %v", r.method, r.path, r.device, e.asDevice)
			}
			for k, v := range c.sent {
				if got, _ := json.Marshal(r.body[k]); string(got) != mustJSON(t, v) {
					t.Errorf("sent %s %s, want %s", k, got, mustJSON(t, v))
				}
			}
			if c.sent == nil && r.body != nil {
				t.Errorf("sent a body %v", r.body)
			}
		})
	}
}

// callOf is the call of the document a request is, by its method and
// its path against each call's template.
func callOf(method, path string) (call, bool) {
	segments := strings.Split(path, "/")
	for _, c := range calls {
		template := strings.Split(c.path, "/")
		if !strings.EqualFold(c.method, method) || len(template) != len(segments) {
			continue
		}
		match := true
		for i, part := range template {
			if !strings.HasPrefix(part, "{") && part != segments[i] {
				match = false
				break
			}
		}
		if match {
			return c, true
		}
	}
	return call{}, false
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Every call returns the service's refusal, with its sentinel.
func TestCallsRefused(t *testing.T) {
	c, _ := fakeService(t, 0, func(w http.ResponseWriter, _ *http.Request, _ int) {
		refuse(w, http.StatusForbidden, "not_your_device")
	})
	ctx := t.Context()
	for name, call := range map[string]func() error{
		"external sender": func() error { _, err := c.ExternalSender(ctx); return err },
		"devices":         func() error { _, err := c.Devices(ctx, "bob"); return err },
		"directory":       func() error { _, _, err := c.Directory(ctx, "bob", nil); return err },
		"recovery":        func() error { _, err := c.Recovery(ctx); return err },
		"take":            func() error { _, err := c.TakeTransfer(ctx); return err },
		"count":           func() error { _, err := c.KeyPackageCount(ctx); return err },
		"claim":           func() error { _, err := c.ClaimKeyPackages(ctx, "bob", "c1"); return err },
		"create":          func() error { _, err := c.CreateConversation(ctx, "c1", nil); return err },
		"list":            func() error { _, err := c.Conversations(ctx, "", 0); return err },
		"group info":      func() error { _, err := c.GroupInfo(ctx, "c1"); return err },
		"send":            func() error { _, err := c.Send(ctx, "c1", messaging.Outgoing{}); return err },
		"messages":        func() error { _, err := c.Messages(ctx, "c1", 0, 0, 0); return err },
		"welcomes":        func() error { _, err := c.Welcomes(ctx); return err },
		"membership":      func() error { _, err := c.IsMember(ctx, "c1", "bob"); return err },
	} {
		if err := call(); !errors.Is(err, messaging.ErrNotYourDevice) || !errors.Is(err, latere.ErrForbidden) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if !strings.Contains(refusedWith(t, http.StatusConflict, "epoch_taken").Error(), "No.") {
		t.Error("the refusal's message is not in its error")
	}
	if err := refusedWith(t, http.StatusBadRequest, "invalid"); errors.Is(err, messaging.ErrRefused) || !errors.As(err, new(*latere.Error)) {
		t.Errorf("a code with no sentinel: %v", err)
	}
	if c.Device() != "phone-1" {
		t.Errorf("device %q", c.Device())
	}
}

// A call that spends or appends is sent again after an answer that says
// it failed, and never after a failure on the network, after which it may
// have been done; a revocation, which is kept once, is sent again after
// either.
func TestRetries(t *testing.T) {
	ctx := t.Context()
	t.Run("after a 503", func(t *testing.T) {
		for name, call := range map[string]func(c *messaging.Client) error{
			"send": func(c *messaging.Client) error {
				_, err := c.Send(ctx, "c1", messaging.Outgoing{Message: []byte{1}})
				return err
			},
			"claim":   func(c *messaging.Client) error { _, err := c.ClaimKeyPackages(ctx, "bob", "c1"); return err },
			"publish": func(c *messaging.Client) error { return c.PublishKeyPackages(ctx, nil, nil) },
			"create":  func(c *messaging.Client) error { _, err := c.CreateConversation(ctx, "c1", nil); return err },
		} {
			c, got := fakeService(t, 1, func(w http.ResponseWriter, _ *http.Request, n int) {
				if n == 0 {
					refuse(w, http.StatusServiceUnavailable, "authorizer_unavailable")
					return
				}
				answer(w, http.StatusOK, `{}`)
			})
			if err := call(c); err != nil {
				t.Errorf("%s: %v", name, err)
			}
			if n := len(got()); n != 2 {
				t.Errorf("%s: sent %d times", name, n)
			}
		}
	})
	t.Run("after the connection drops", func(t *testing.T) {
		for name, c := range map[string]struct {
			call  func(c *messaging.Client) error
			sends int
		}{
			"send": {func(c *messaging.Client) error {
				_, err := c.Send(ctx, "c1", messaging.Outgoing{Message: []byte{1}})
				return err
			}, 1},
			"claim":  {func(c *messaging.Client) error { _, err := c.ClaimKeyPackages(ctx, "bob", "c1"); return err }, 1},
			"revoke": {func(c *messaging.Client) error { return c.Revoke(ctx, messaging.Revocation{}) }, 2},
			"leave":  {func(c *messaging.Client) error { return c.LeaveTransfer(ctx, "tablet", []byte{1}) }, 2},
		} {
			client, got := fakeService(t, 1, func(w http.ResponseWriter, _ *http.Request, n int) {
				if n == 0 {
					hangUp(t, w)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			})
			err := c.call(client)
			if n := len(got()); n != c.sends {
				t.Errorf("%s: sent %d times, want %d (%v)", name, n, c.sends, err)
			}
			if c.sends == 1 && err == nil {
				t.Errorf("%s: a dropped connection was not reported", name)
			}
		}
	})
}

// hangUp closes the connection without an answer.
func hangUp(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	conn, _, err := http.NewResponseController(w).Hijack()
	if err != nil {
		t.Fatal(err)
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetLinger(0)
	}
	if err := conn.Close(); err != nil {
		t.Error(err)
	}
}

// A read that waits for messages ends with its context.
func TestMessagesEndWithTheContext(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	c, _ := fakeService(t, 0, func(w http.ResponseWriter, r *http.Request, _ int) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.Messages(ctx, "c1", 0, 0, messaging.MaxWait); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("got %v", err)
	}
}
