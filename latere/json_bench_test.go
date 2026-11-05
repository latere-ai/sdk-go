// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package latere

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// benchTransport answers every request with one fixed JSON body, in
// process, so a benchmark measures the SDK's codec and not a network.
type benchTransport struct {
	body []byte
}

func (t benchTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Body != nil {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			return nil, err
		}
		r.Body.Close()
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(t.body)),
		Request:    r,
	}, nil
}

func benchConfig(b *testing.B, body []byte) *Config {
	b.Helper()
	c, err := NewConfig(
		WithBaseURL("https://api.bench.invalid"),
		WithTokenSource(StaticToken("key_bench")),
		WithHTTPClient(&http.Client{Transport: benchTransport{body: body}}),
	)
	if err != nil {
		b.Fatal(err)
	}
	return c
}

type benchItem struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Status    string            `json:"status"`
	Created   time.Time         `json:"created_at"`
	Labels    map[string]string `json:"labels"`
	Notes     string            `json:"notes"`
	TokensIn  int64             `json:"tokens_in"`
	TokensOut int64             `json:"tokens_out"`
	Tags      []string          `json:"tags"`
}

type benchList struct {
	Items      []benchItem `json:"items"`
	NextCursor string      `json:"next_cursor,omitempty"`
}

// benchPayload builds a deterministic listing of about n items.
func benchPayload(n int) benchList {
	base := time.Date(2026, 3, 14, 9, 26, 53, 0, time.UTC)
	out := benchList{NextCursor: "cur_01M34G2JF1P53NNG7DN08D5QAG"}
	for i := range n {
		out.Items = append(out.Items, benchItem{
			ID:        fmt.Sprintf("ses_01M34G2JF1P53NNG7DN%05d", i),
			Name:      fmt.Sprintf("Quarterly report draft %d", i),
			Status:    "idle",
			Created:   base.Add(time.Duration(i) * time.Minute),
			Labels:    map[string]string{"team": "platform", "env": "prod", "owner": "usr_01HZ"},
			Notes:     strings.Repeat("The agent summarised the findings. ", 3),
			TokensIn:  int64(1200 + i*7),
			TokensOut: int64(340 + i*3),
			Tags:      []string{"report", "weekly", "finance"},
		})
	}
	return out
}

var benchSizes = []struct {
	name  string
	items int
}{{"small", 1}, {"medium", 25}, {"large", 500}}

// BenchmarkJSONConfigDoDecode stands for decoding any platform API
// listing answer through Config.Do.
func BenchmarkJSONConfigDoDecode(b *testing.B) {
	for _, s := range benchSizes {
		b.Run(s.name, func(b *testing.B) {
			body := mustMarshalBench(b, benchPayload(s.items))
			cfg := benchConfig(b, body)
			req := &Request{Method: http.MethodGet, Path: "/v1/agents/sessions"}
			ctx := b.Context()
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			for b.Loop() {
				var out benchList
				if err := cfg.Do(ctx, req, &out); err != nil {
					b.Fatal(err)
				}
				if len(out.Items) != s.items {
					b.Fatalf("decoded %d items, want %d", len(out.Items), s.items)
				}
			}
		})
	}
}

// BenchmarkJSONConfigDoEncode stands for encoding a request body, such as
// a session's first message, and decoding the small answer.
func BenchmarkJSONConfigDoEncode(b *testing.B) {
	for _, s := range benchSizes {
		b.Run(s.name, func(b *testing.B) {
			payload := benchPayload(s.items)
			encoded := mustMarshalBench(b, payload)
			cfg := benchConfig(b, []byte(`{"id":"ses_01M34G2JF1P53NNG7DN08D5QAG","status":"running"}`))
			req := &Request{Method: http.MethodPost, Path: "/v1/agents/sessions", Body: payload, IdempotencyKey: "idem_01M34G2JF1"}
			ctx := b.Context()
			b.ReportAllocs()
			b.SetBytes(int64(len(encoded)))
			for b.Loop() {
				var out struct {
					ID     string `json:"id"`
					Status string `json:"status"`
				}
				if err := cfg.Do(ctx, req, &out); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func mustMarshalBench(b *testing.B, v any) []byte {
	b.Helper()
	out, err := json.Marshal(v)
	if err != nil {
		b.Fatal(err)
	}
	return out
}
