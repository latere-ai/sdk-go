// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package account_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"latere.ai/x/sdk-go/account"
	"latere.ai/x/sdk-go/latere"
)

// A capturedCall is one request a capturingAPI received.
type capturedCall struct {
	Method, Path, Query, ContentType string
	// Body is the request's JSON body, nil when it sent none.
	Body map[string]any
	// RawBody is the body as sent.
	RawBody string
}

// capturingAPI is a Client of a server that records each request and
// answers it with answer, which also learns how many requests came before
// it. It allows retries, with no wait between them.
func capturingAPI(t *testing.T, retries int, answer func(w http.ResponseWriter, r *http.Request, n int)) (*account.Client, func() []capturedCall) {
	t.Helper()
	var mu sync.Mutex
	var calls []capturedCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		c := capturedCall{Method: r.Method, Path: r.URL.EscapedPath(), Query: r.URL.RawQuery, ContentType: r.Header.Get("Content-Type"), RawBody: string(raw)}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &c.Body); err != nil {
				t.Errorf("%s %s sent a body that is no JSON object: %s", r.Method, r.URL.Path, raw)
			}
		}
		mu.Lock()
		n := len(calls)
		calls = append(calls, c)
		mu.Unlock()
		answer(w, r, n)
	}))
	t.Cleanup(srv.Close)
	cfg, err := latere.NewConfig(latere.WithBaseURL(srv.URL), latere.WithTokenSource(latere.StaticToken("tok")),
		latere.WithMaxRetries(retries), latere.WithBackoff(time.Millisecond, time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	return account.New(cfg), func() []capturedCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]capturedCall(nil), calls...)
	}
}

// answerJSON writes body with status.
func answerJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

// answerUnavailable answers 503, which the SDK retries where a request may
// be sent again.
func answerUnavailable(w http.ResponseWriter) {
	answerJSON(w, http.StatusServiceUnavailable, `{"error":{"code":"unavailable","message":"Try again."}}`)
}
