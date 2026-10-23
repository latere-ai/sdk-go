// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package llm_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"latere.ai/x/sdk-go/latere"
	"latere.ai/x/sdk-go/llm"
)

// newClient returns a Client for a test server running h, whose credential
// is a key (sent as itself) and whose retries wait no time.
func newClient(t *testing.T, h http.HandlerFunc, opts ...llm.Option) *llm.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	kt, err := latere.NewKeyTokens("pat_test", latere.WithIssuer(srv.URL+"/issuer"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := latere.NewConfig(latere.WithBaseURL(srv.URL), latere.WithTokenSource(kt), latere.WithBackoff(time.Millisecond, time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	return llm.New(cfg, opts...)
}

func hello() *llm.Request {
	return &llm.Request{Model: "anthropic/claude-sonnet-4.5", Messages: []llm.Message{llm.UserText("Hello")}}
}

func TestGenerateSendsTheKeyAndReadsTheAnswer(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/models/lux/v1/generate" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer pat_test" {
			t.Errorf("Authorization = %q, want the key itself", got)
		}
		var req llm.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Stream || req.Model != "anthropic/claude-sonnet-4.5" || req.Messages[0].Blocks[0].Text != "Hello" {
			t.Errorf("request = %+v", req)
		}
		w.Header().Set("Lux-Loss", "top_k,cache_control")
		w.Header().Set("Lux-Request-Id", "req_1")
		fmt.Fprint(w, `{"id":"msg_1","model":"anthropic/claude-sonnet-4.5","blocks":[{"type":"thinking","text":"hm"},{"type":"text","text":"Hel"},{"type":"text","text":"lo"}],"stop_reason":"end_turn","usage":{"input_tokens":12,"output_tokens":3,"cost_usd_micro":0}}`)
	})
	req := hello()
	req.Stream = true // Generate sends its own copy with Stream false
	res, err := c.Generate(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !req.Stream {
		t.Error("Generate changed the caller's request")
	}
	if res.Text() != "Hello" || res.StopReason != llm.StopEndTurn || res.Usage.InputTokens != 12 {
		t.Errorf("result = %+v", res)
	}
	if res.Usage.CostMicroUSD == nil || *res.Usage.CostMicroUSD != 0 {
		t.Errorf("cost = %v, want a reported zero", res.Usage.CostMicroUSD)
	}
	if !slices.Equal(res.Loss, []string{"top_k", "cache_control"}) || res.RequestID != "req_1" {
		t.Errorf("loss = %v, request = %q", res.Loss, res.RequestID)
	}
}

// A generation may have run before a failure, so Generate never sends it a
// second time; the refusal arrives as the API's error with its request id.
func TestGenerateIsNeverSentTwice(t *testing.T) {
	var calls atomic.Int32
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, `{"error":{"code":"upstream_error","message":"The model's provider failed.","details":{"request_id":"req_2"}}}`)
	})
	_, err := c.Generate(t.Context(), hello())
	var apiErr *latere.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "upstream_error" || apiErr.RequestID != "req_2" {
		t.Fatalf("err = %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1", calls.Load())
	}
}

func TestNilRequestsAreRefused(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("no request expected") })
	if _, err := c.Generate(t.Context(), nil); err == nil {
		t.Error("Generate(nil) succeeded")
	}
	if _, err := c.Stream(t.Context(), nil); err == nil {
		t.Error("Stream(nil) succeeded")
	}
	if _, err := c.CountTokens(t.Context(), nil); err == nil {
		t.Error("CountTokens(nil) succeeded")
	}
}

func TestGenerateReportsABrokenAnswer(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"blocks":`) })
	if _, err := c.Generate(t.Context(), hello()); err == nil || !strings.Contains(err.Error(), "decoding the answer") {
		t.Errorf("err = %v", err)
	}
}

// Generate decodes the answer's one JSON value and reads no further, so an
// answer whose connection ends early after a complete value is kept: the
// model has run and may have been billed, and nothing of the answer is
// missing. An answer that ends inside its value, or holds none, is refused.
func TestGenerateKeepsAnAnswerCutOffAfterItsValue(t *testing.T) {
	const answer = `{"id":"msg_1","model":"anthropic/claude-sonnet-4.5","blocks":[{"type":"text","text":"Hello"}],"stop_reason":"end_turn","usage":{"input_tokens":12,"output_tokens":1}}`
	// write sends body, after promising length bytes when length is not
	// zero, and then closes the connection without ending the answer when
	// cut is set.
	write := func(body string, length int, cut bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if length > 0 {
				w.Header().Set("Content-Length", strconv.Itoa(length))
			}
			if _, err := io.WriteString(w, body); err != nil {
				t.Error(err)
				return
			}
			if !cut {
				return
			}
			rc := http.NewResponseController(w)
			if err := rc.Flush(); err != nil {
				t.Error(err)
				return
			}
			conn, _, err := rc.Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			if err := conn.Close(); err != nil {
				t.Error(err)
			}
		}
	}
	half := answer[:len(answer)/2]
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		wantErr error
	}{
		{"complete", write(answer, 0, false), nil},
		{"whitespace after the value", write(answer+"\n \n", 0, false), nil},
		{"promised length short after the value", write(answer, len(answer)+10, false), nil},
		{"connection closed after the value", write(answer, 0, true), nil},
		{"promised length short inside the value", write(half, len(answer), false), io.ErrUnexpectedEOF},
		{"connection closed inside the value", write(half, 0, true), io.ErrUnexpectedEOF},
		{"no value", write("", 0, false), io.EOF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := newClient(t, tc.handler).Generate(t.Context(), hello())
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) || !strings.Contains(err.Error(), "decoding the answer") {
					t.Fatalf("err = %v, want a decoding error wrapping %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if res.Text() != "Hello" || res.StopReason != llm.StopEndTurn || res.Usage.OutputTokens != 1 {
				t.Errorf("result = %+v", res)
			}
		})
	}
}

func TestLabelsTravelSorted(t *testing.T) {
	var got []string
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("Lux-Labels"))
		switch r.URL.Path {
		case "/v1/models/lux/v1/count_tokens":
			fmt.Fprint(w, `{"input_tokens":1}`)
		default:
			fmt.Fprint(w, `{"blocks":[],"usage":{}}`)
		}
	}, llm.WithLabels(map[string]string{"tenant": "acme", "project": "web"}))
	if _, err := c.Generate(t.Context(), hello()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CountTokens(t.Context(), hello()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"project=web,tenant=acme", "project=web,tenant=acme"}) {
		t.Errorf("labels = %q", got)
	}
	plain := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if v, ok := r.Header["Lux-Labels"]; ok {
			t.Errorf("a Client without labels sent %q", v)
		}
		fmt.Fprint(w, `{"blocks":[],"usage":{}}`)
	}, llm.WithLabels(nil))
	if _, err := plain.Generate(t.Context(), hello()); err != nil {
		t.Fatal(err)
	}
}

// A label the service would drop is left out, so a comma or an equals sign
// in one cannot change the others, and at most eight are sent.
func TestLabelsTheServiceWouldDropAreLeftOut(t *testing.T) {
	var got string
	labels := map[string]string{
		"tenant":     "acme",
		"bad,key":    "x",
		"injected":   "a,owner=mallory",
		"equals":     "a=b",
		"empty":      "",
		"path":       "team/web:prod",
		"with space": "x",
	}
	for i := range 10 {
		labels[fmt.Sprintf("k%d", i)] = "v"
	}
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Lux-Labels")
		fmt.Fprint(w, `{"blocks":[],"usage":{}}`)
	}, llm.WithLabels(labels))
	if _, err := c.Generate(t.Context(), hello()); err != nil {
		t.Fatal(err)
	}
	if want := "k0=v,k1=v,k2=v,k3=v,k4=v,k5=v,k6=v,k7=v"; got != want {
		t.Errorf("labels = %q, want %q", got, want)
	}
	c = newClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Lux-Labels")
		fmt.Fprint(w, `{"blocks":[],"usage":{}}`)
	}, llm.WithLabels(map[string]string{"tenant": "acme", "injected": "a,owner=mallory", "path": "team/web:prod"}))
	if _, err := c.Generate(t.Context(), hello()); err != nil {
		t.Fatal(err)
	}
	if want := "path=team/web:prod,tenant=acme"; got != want {
		t.Errorf("labels = %q, want %q", got, want)
	}
}

// Counting changes nothing, so a failure that may pass is retried.
func TestCountTokensRetriesAndReportsAnEstimate(t *testing.T) {
	var calls atomic.Int32
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models/lux/v1/count_tokens" {
			http.NotFound(w, r)
			return
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if r.Header.Get("Authorization") != "Bearer pat_test" || r.Header.Get("Idempotency-Key") != "" {
			t.Errorf("headers = %v; want the key and no idempotency key, which the models capability does not read", r.Header)
		}
		w.Header().Set("Lux-Estimated", "true")
		w.Header().Set("Lux-Request-Id", "req_3")
		fmt.Fprint(w, `{"input_tokens":42}`)
	})
	tc, err := c.CountTokens(t.Context(), hello())
	if err != nil {
		t.Fatal(err)
	}
	if tc.InputTokens != 42 || !tc.Estimated || tc.RequestID != "req_3" || calls.Load() != 2 {
		t.Errorf("count = %+v after %d calls", tc, calls.Load())
	}
}

func TestCountTokensReportsABrokenAnswer(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `nope`) })
	if _, err := c.CountTokens(t.Context(), hello()); err == nil {
		t.Error("a broken count succeeded")
	}
}

func TestListModels(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models/lux/v1/models" || r.Header.Get("Authorization") != "Bearer pat_test" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"object":"list","data":[
			{"id":"openai/gpt-5","object":"model","created":0,"owned_by":"openai"},
			{"id":"anthropic/claude-sonnet-4.5","object":"model","created":1760000000,"owned_by":"anthropic","context_window":200000,"max_output_tokens":64000,"input_modalities":["text","image"],"pricing":{"currency":"USD","per":1000000,"input":"3","output":"15","cached_input":"0.3","cache_write":"3.75"}}]}`)
	})
	models, err := c.ListModels(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "anthropic/claude-sonnet-4.5" || models[1].ID != "openai/gpt-5" {
		t.Fatalf("models = %+v, want them sorted by id", models)
	}
	m := models[0]
	if m.ContextWindow != 200000 || m.MaxOutputTokens != 64000 || !slices.Equal(m.InputModalities, []string{"text", "image"}) || !m.Created.Equal(time.Unix(1760000000, 0)) {
		t.Errorf("model = %+v", m)
	}
	if m.Pricing == nil || *m.Pricing != (llm.Pricing{Currency: "USD", Per: 1000000, Input: "3", Output: "15", CachedInput: "0.3", CacheWrite: "3.75"}) {
		t.Errorf("pricing = %+v", m.Pricing)
	}
	if !models[1].Created.IsZero() || models[1].Pricing != nil {
		t.Errorf("a model without figures = %+v", models[1])
	}
}

func TestListModelsRefused(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"code":"unauthenticated","message":"This request needs a valid credential."}}`)
	})
	if _, err := c.ListModels(t.Context()); !errors.Is(err, latere.ErrUnauthorized) {
		t.Errorf("err = %v", err)
	}
}

func TestAConfigThatIsNotBuiltFailsEveryCall(t *testing.T) {
	c := llm.New(nil)
	if _, err := c.Generate(t.Context(), hello()); err == nil {
		t.Error("Generate succeeded without a Config")
	}
	if _, err := c.ListModels(t.Context()); err == nil {
		t.Error("ListModels succeeded without a Config")
	}
	if _, err := c.Stream(t.Context(), hello()); err == nil {
		t.Error("Stream succeeded without a Config")
	}
}

// drain reads what is left of a request body, so the server's handler can
// write its answer before the client stops reading.
func drain(r *http.Request) { _, _ = io.Copy(io.Discard, r.Body) }
