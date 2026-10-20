// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package apps_test

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"latere.ai/x/sdk-go/apps"
)

const deployJSON = `{"id":"0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0","seq":4,"kind":"service","status":"failed","preview":true,
	"commit_sha":"abc","ref":"refs/heads/main","client":"push","created_by":"usr_1","url":"https://tide-tables.latere.site",
	"preview_url":"https://0f1e2d3c--tide-tables.latere.site","logs_url":"/v1/deploys/0f1e/logs","from_deploy":null,
	"components":[{"name":"web","kind":"service","route":"/","status":"failed"}],"error":{"code":"build_failed","message":"exit 1"},
	"created_at":"2026-10-07T10:00:00Z","started_at":"2026-10-07T10:00:05Z","built_at":null,"ready_at":null,"live_at":null,
	"finished_at":"2026-10-07T10:02:00Z","thumbnail":null}`

func TestDeploys(t *testing.T) {
	c, f := newFake(t, 0, map[string]http.HandlerFunc{
		"GET /v1/apps/apps/tide-tables/deploys":          answer(http.StatusOK, `{"deploys":[`+deployJSON+`]}`),
		"GET /v1/apps/apps/tide-tables/deploys/0f1e2d3c": answer(http.StatusOK, deployJSON),
	})
	list, err := c.ListDeploys(t.Context(), "tide-tables")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("ListDeploys = %d deploys", len(list))
	}
	d := list[0]
	if d.Status != apps.DeployFailed || d.Kind != apps.KindService || !d.Preview || d.Error.Code != "build_failed" ||
		d.Components[0].Status != apps.ComponentFailed || !d.BuiltAt.IsZero() || d.FinishedAt.IsZero() {
		t.Errorf("ListDeploys decoded %+v", d)
	}
	got, err := c.GetDeploy(t.Context(), "tide-tables", "0f1e2d3c")
	if err != nil {
		t.Fatal(err)
	}
	if got.Seq != 4 || !strings.Contains(string(got.RawJSON()), `"logs_url"`) {
		t.Errorf("GetDeploy = %+v", got)
	}
	if p := f.record()[1].Path; p != "/v1/apps/apps/tide-tables/deploys/0f1e2d3c" {
		t.Errorf("GetDeploy path = %s", p)
	}
}

func TestReadDeployLog(t *testing.T) {
	c, f := newFake(t, 0, map[string]http.HandlerFunc{
		"GET /v1/apps/apps/tide-tables/deploys/d1/logs": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/x-ndjson")
			for i := 1; i <= 3; i++ {
				fmt.Fprintf(w, `{"seq":%d,"ts":"2026-10-07T10:00:0%dZ","src":"build","component":"web","line":"step %d"}`+"\n", i, i, i)
			}
		},
	})
	var lines []string
	for l, err := range c.ReadDeployLog(t.Context(), "tide-tables", "d1", &apps.DeployLogParams{Component: "web", Since: 7}) {
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, l.Text)
		if l.Step != "build" || l.Component != "web" || l.Time.IsZero() || len(l.RawJSON()) == 0 {
			t.Errorf("decoded %+v", l)
		}
	}
	if strings.Join(lines, "|") != "step 1|step 2|step 3" {
		t.Errorf("read %v", lines)
	}
	r := f.record()[0]
	if r.Query != "component=web&since=7" || r.Header.Get("Accept") != "application/x-ndjson" {
		t.Errorf("request = %+v", r)
	}
}

func TestReadDeployLogReportsABadLine(t *testing.T) {
	c, _ := newFake(t, 0, map[string]http.HandlerFunc{
		"GET /v1/apps/apps/x/deploys/d1/logs": func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, `{"seq":1,"line":"ok"}`+"\nnot json\n")
		},
	})
	var n int
	var last error
	for _, err := range c.ReadDeployLog(t.Context(), "x", "d1", nil) {
		n++
		last = err
	}
	if n != 2 || last == nil {
		t.Errorf("read %d items ending in %v, want a line and an error", n, last)
	}
}

// Following a log picks up after the last line delivered when the
// connection drops, and ends with the build's outcome.
func TestFollowDeployLogResumesAndEnds(t *testing.T) {
	var opened atomic.Int32
	c, f := newFake(t, 2, map[string]http.HandlerFunc{
		"GET /v1/apps/apps/tide-tables/deploys/d1/logs": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			if opened.Add(1) == 1 {
				fmt.Fprint(w, "id: web:1\ndata: {\"seq\":1,\"component\":\"web\",\"line\":\"one\"}\n\n")
				fmt.Fprint(w, "id: web:2\ndata: {\"seq\":2,\"component\":\"web\",\"line\":\"two\"}\n\n")
				return // the connection drops before the end
			}
			fmt.Fprint(w, "id: web:3\ndata: {\"seq\":3,\"component\":\"web\",\"line\":\"three\"}\n\n")
			fmt.Fprint(w, "event: end\ndata: {\"status\":\"failed\",\"error\":{\"code\":\"build_failed\",\"message\":\"The build failed.\",\"hint\":\"Read the log.\",\"component\":\"web\"}}\n\n")
		},
	})
	var lines []string
	var end *apps.LogEnd
	for ev, err := range c.FollowDeployLog(t.Context(), "tide-tables", "d1", nil) {
		if err != nil {
			t.Fatal(err)
		}
		if ev.Line != nil {
			lines = append(lines, ev.Line.Text)
		}
		if ev.End != nil {
			end = ev.End
		}
	}
	if strings.Join(lines, ",") != "one,two,three" {
		t.Errorf("followed %v", lines)
	}
	if end == nil || end.Status != apps.BuildFailed || end.Error.Hint != "Read the log." {
		t.Errorf("end = %+v", end)
	}
	calls := f.record()
	if len(calls) != 2 || calls[0].Query != "follow=1" || calls[1].Header.Get("Last-Event-ID") != "web:2" ||
		calls[0].Header.Get("Accept") != "text/event-stream" {
		t.Errorf("requests = %+v", calls)
	}
}

// Stopping the loop of a followed log returns at once, though the server
// keeps the stream open.
func TestFollowDeployLogStopsWithTheLoop(t *testing.T) {
	c, _ := newFake(t, 0, map[string]http.HandlerFunc{
		"GET /v1/apps/apps/x/deploys/d1/logs": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "id: web:1\ndata: {\"seq\":1,\"line\":\"one\"}\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		},
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range c.FollowDeployLog(t.Context(), "x", "d1", nil) {
			break
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("breaking out of FollowDeployLog did not return")
	}
}

// A frame of a followed log that does not decode ends the iteration with
// an error, for a line and for the end alike.
func TestFollowDeployLogReportsABadFrame(t *testing.T) {
	for name, frame := range map[string]string{
		"a line":  "id: web:1\ndata: not json\n\n",
		"the end": "event: end\ndata: not json\n\n",
	} {
		c, _ := newFake(t, 0, map[string]http.HandlerFunc{
			"GET /v1/apps/apps/x/deploys/d1/logs": func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, frame)
			},
		})
		var last error
		for _, err := range c.FollowDeployLog(t.Context(), "x", "d1", nil) {
			last = err
		}
		if last == nil {
			t.Errorf("%s that does not decode gave no error", name)
		}
	}
}

// A stored log line longer than the bound ends the read with an error
// rather than holding the whole line in memory.
func TestReadDeployLogBoundsALine(t *testing.T) {
	c, _ := newFake(t, 0, map[string]http.HandlerFunc{
		"GET /v1/apps/apps/x/deploys/d1/logs": func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, `{"line":"`+strings.Repeat("x", 2<<20)+`"}`+"\n")
		},
	})
	var last error
	for _, err := range c.ReadDeployLog(t.Context(), "x", "d1", nil) {
		last = err
	}
	if last == nil {
		t.Error("a 2 MiB line was read without an error")
	}
}

// Stopping the loop over a stored log closes it after the line asked for.
func TestReadDeployLogStopsWithTheLoop(t *testing.T) {
	c, _ := newFake(t, 0, map[string]http.HandlerFunc{
		"GET /v1/apps/apps/x/deploys/d1/logs": func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, `{"seq":1,"line":"one"}`+"\n"+`{"seq":2,"line":"two"}`+"\n")
		},
	})
	var n int
	for range c.ReadDeployLog(t.Context(), "x", "d1", nil) {
		n++
		break
	}
	if n != 1 {
		t.Errorf("read %d lines after a break", n)
	}
}
