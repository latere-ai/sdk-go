// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package latere

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"latere.ai/x/sdk-go/internal/sse"
)

func TestItemsFetchesPagesAsNeeded(t *testing.T) {
	pages := map[string]*Page[int]{
		"":   {Items: []int{1, 2}, NextCursor: "c2"},
		"c2": {Items: []int{}, NextCursor: "c3"},
		"c3": {Items: []int{3}},
	}
	var fetched []string
	fetch := func(_ context.Context, cursor string) (*Page[int], error) {
		fetched = append(fetched, cursor)
		return pages[cursor], nil
	}
	var got []int
	for v, err := range Items(t.Context(), fetch) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, v)
	}
	if fmt.Sprint(got) != "[1 2 3]" || fmt.Sprint(fetched) != "[ c2 c3]" {
		t.Errorf("got %v from pages %q", got, fetched)
	}
	fetched = nil
	for v := range Items(t.Context(), fetch) {
		if v == 1 {
			break
		}
	}
	if len(fetched) != 1 {
		t.Errorf("a loop that stopped at once fetched %q", fetched)
	}
}

func TestItemsYieldsTheErrorOnce(t *testing.T) {
	boom := errors.New("down")
	n := 0
	for _, err := range Items(t.Context(), func(context.Context, string) (*Page[int], error) { return nil, boom }) {
		n++
		if !errors.Is(err, boom) {
			t.Errorf("yielded %v", err)
		}
	}
	if n != 1 {
		t.Errorf("yielded %d times, want 1", n)
	}
	repeat := func(context.Context, string) (*Page[int], error) {
		return &Page[int]{Items: []int{1}, NextCursor: "same"}, nil
	}
	count := 0
	for range Items(t.Context(), repeat) {
		count++
		if count > 5 {
			t.Fatal("a cursor that repeats loops forever")
		}
	}
}

// eventServer serves a stream of numbered frames that drops after every
// two frames until the last, and records each connection's Last-Event-ID.
func eventServer(t *testing.T, total int) (*httptest.Server, *[]string) {
	t.Helper()
	var resumes []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("Accept = %q", r.Header.Get("Accept"))
		}
		last := r.Header.Get("Last-Event-ID")
		resumes = append(resumes, last)
		from := 1
		if last != "" {
			n, err := strconv.Atoi(last)
			if err != nil {
				t.Fatal(err)
			}
			from = n + 1
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": keepalive\n\n")
		for i := from; i <= total && i < from+2; i++ {
			kind := "tick"
			if i == total {
				kind = "end"
			}
			fmt.Fprintf(w, "id: %d\nevent: %s\ndata: {\"n\":%d}\n\n", i, kind, i)
			fmt.Fprint(w, "event: delta\ndata: {}\n\n")
		}
		// Returning closes the connection, which the client reads as a drop
		// unless the last frame was the end.
	}))
	return srv, &resumes
}

func TestStreamResumesAfterADrop(t *testing.T) {
	srv, resumes := eventServer(t, 5)
	defer srv.Close()
	c, waits := testConfig(t, srv)
	var ids []string
	for f, err := range c.Stream(t.Context(), &Request{Method: http.MethodGet, Path: "/v1/stream"}, func(f Frame) bool { return f.Event == "end" }) {
		if err != nil {
			t.Fatal(err)
		}
		if f.ID != "" {
			ids = append(ids, f.ID)
		}
	}
	if strings.Join(ids, ",") != "1,2,3,4,5" {
		t.Errorf("frames %v, want each once in order", ids)
	}
	if strings.Join(*resumes, ",") != ",2,4" {
		t.Errorf("connections resumed after %q", *resumes)
	}
	if len(*waits) != 2 {
		t.Errorf("waited %d times between connections, want 2", len(*waits))
	}
}

func TestStreamGivesUpWhenNothingArrives(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
	}))
	defer srv.Close()
	c, _ := testConfig(t, srv)
	var last error
	for _, err := range c.Stream(t.Context(), &Request{Method: http.MethodGet, Path: "/v1/stream"}, nil) {
		last = err
	}
	if last == nil || !strings.Contains(last.Error(), "dropped") {
		t.Errorf("an empty stream ended with %v", last)
	}
	if calls.Load() != 1+DefaultMaxRetries {
		t.Errorf("connected %d times, want %d", calls.Load(), 1+DefaultMaxRetries)
	}
}

func TestStreamStopsWithTheLoop(t *testing.T) {
	srv, resumes := eventServer(t, 5)
	defer srv.Close()
	c, _ := testConfig(t, srv)
	for f := range c.Stream(t.Context(), &Request{Method: http.MethodGet, Path: "/v1/stream"}, nil) {
		if f.ID == "1" {
			break
		}
	}
	if len(*resumes) != 1 {
		t.Errorf("a loop that stopped connected %d times", len(*resumes))
	}
}

func TestStreamReturnsAConnectError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusForbidden, "forbidden", "No.")
	}))
	defer srv.Close()
	c, _ := testConfig(t, srv)
	for _, err := range c.Stream(t.Context(), &Request{Method: http.MethodGet, Path: "/v1/stream"}, nil) {
		if !errors.Is(err, ErrForbidden) {
			t.Errorf("a refused stream: %v", err)
		}
	}
}

func TestStreamEndsWithTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "id: 1\ndata: {}\n\n")
		cancel()
	}))
	defer srv.Close()
	c, _ := testConfig(t, srv)
	var last error
	for _, err := range c.Stream(ctx, &Request{Method: http.MethodGet, Path: "/v1/stream"}, nil) {
		if err != nil {
			last = err
		}
	}
	if !errors.Is(last, context.Canceled) {
		t.Errorf("a canceled stream ended with %v", last)
	}
}

func TestStreamStopsWhenTheWaitIsCanceled(t *testing.T) {
	srv, _ := eventServer(t, 5)
	defer srv.Close()
	c, _ := testConfig(t, srv)
	c.sleep = func(context.Context, time.Duration) error { return context.Canceled }
	var last error
	for _, err := range c.Stream(t.Context(), &Request{Method: http.MethodGet, Path: "/v1/stream"}, nil) {
		if err != nil {
			last = err
		}
	}
	if !errors.Is(last, context.Canceled) {
		t.Errorf("a canceled wait ended with %v", last)
	}
}

// liveServer serves three frames, then holds the connection open with a
// keepalive comment every 100ms, as the API does for a session that is
// still open.
func liveServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for i := 1; i <= 3; i++ {
			fmt.Fprintf(w, "id: %d\ndata: {}\n\n", i)
		}
		w.(http.Flusher).Flush()
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-tick.C:
				fmt.Fprint(w, ": keepalive\n\n")
				w.(http.Flusher).Flush()
			}
		}
	}))
}

func TestStreamStopsAtOnceOnALiveConnection(t *testing.T) {
	srv := liveServer(t)
	defer srv.Close()
	c, _ := testConfig(t, srv)
	done := make(chan error, 1)
	go func() {
		// No deadline: stopping must not wait for the stream to end.
		for f, err := range c.Stream(context.Background(), &Request{Method: http.MethodGet, Path: "/s"}, nil) {
			if err != nil {
				done <- err
				return
			}
			if f.ID == "2" {
				break
			}
		}
		done <- nil
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the loop stopped, but Stream did not return while the connection stayed open")
	}
}

func TestStreamTimeoutBoundsTheConnect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	c, _ := testConfig(t, srv, WithTimeout(50*time.Millisecond), WithMaxRetries(0))
	start := time.Now()
	for _, err := range c.Stream(t.Context(), &Request{Method: http.MethodGet, Path: "/s"}, nil) {
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("a stream that never answers: %v", err)
		}
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("the stream waited %v past a 50ms timeout", took)
	}
}

func TestStreamTimeoutSparesTheStream(t *testing.T) {
	srv := liveServer(t)
	defer srv.Close()
	c, _ := testConfig(t, srv, WithTimeout(50*time.Millisecond))
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	n := 0
	start := time.Now()
	for _, err := range c.Stream(ctx, &Request{Method: http.MethodGet, Path: "/s"}, nil) {
		if err != nil {
			t.Fatal(err)
		}
		if n++; n == 3 {
			break
		}
		// A reader slower than the timeout keeps its stream.
		time.Sleep(100 * time.Millisecond)
	}
	if time.Since(start) < 150*time.Millisecond {
		t.Error("the reader did not outlast the timeout")
	}
}

func TestStreamEndsOnAnOversizedEvent(t *testing.T) {
	var connects atomic.Int32
	big := strings.Repeat("x", sse.MaxLine+1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connects.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "id: 1\ndata: {}\n\nid: 2\ndata: %s\n\n", big)
	}))
	defer srv.Close()
	c, _ := testConfig(t, srv)
	var last error
	for _, err := range c.Stream(t.Context(), &Request{Method: http.MethodGet, Path: "/s"}, nil) {
		last = err
	}
	if !errors.Is(last, bufio.ErrTooLong) || connects.Load() != 1 {
		t.Errorf("an event past the bound: %v after %d connections, want ErrTooLong after 1", last, connects.Load())
	}
}
