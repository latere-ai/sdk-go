// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents_test

import (
	"encoding/json"
	"strings"
	"testing"

	"latere.ai/x/sdk-go/agents"
)

// toolResult decodes the payload of a tool.result event.
func toolResult(t *testing.T, payload string) agents.ToolResult {
	t.Helper()
	e := agents.Event{ID: "evt_1", Type: agents.EventToolResult, Payload: json.RawMessage(payload)}
	var r agents.ToolResult
	if err := e.Decode(&r); err != nil {
		t.Fatal(err)
	}
	return r
}

// A write or an edit records the file it left, with its size; a read
// records the file and its hash alone.
func TestAToolResultRecordsTheFileItTouched(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	for name, tc := range map[string]struct {
		payload string
		path    string
		size    int64
		sized   bool
	}{
		"a write":        {`{"tool_use_id":"call_1","content":[{"type":"text","text":"Wrote report.md."}],"outcome":"ok","meta":{"path":"/workspace/report.md","sha256":"` + sum + `","size":2048}}`, "/workspace/report.md", 2048, true},
		"an empty write": {`{"tool_use_id":"call_2","content":[],"outcome":"ok","meta":{"path":"/workspace/empty.txt","sha256":"` + sum + `","size":0}}`, "/workspace/empty.txt", 0, true},
		"a read":         {`{"tool_use_id":"call_3","content":[],"outcome":"ok","meta":{"path":"/workspace/notes.md","sha256":"` + sum + `"}}`, "/workspace/notes.md", 0, false},
	} {
		r := toolResult(t, tc.payload)
		if r.Meta == nil {
			t.Fatalf("%s: no meta", name)
		}
		if r.Meta.Path != tc.path || r.Meta.SHA256 != sum || r.Meta.Publish != nil {
			t.Errorf("%s: meta %+v", name, r.Meta)
		}
		if (r.Meta.Size != nil) != tc.sized || tc.sized && *r.Meta.Size != tc.size {
			t.Errorf("%s: size %v, want %d set %v", name, r.Meta.Size, tc.size, tc.sized)
		}
	}
}

// A publish records the app, its addresses, the deploy and where it
// stands; a field the package does not model stays readable.
func TestAPublishRecordsWhatItPutUp(t *testing.T) {
	r := toolResult(t, `{"tool_use_id":"call_4","content":[{"type":"text","text":"Released."}],"outcome":"ok","meta":{"publish":{
		"app":"tide-tables","app_id":"app_7","name":"Tide tables","url":"https://tide-tables.example","preview":"https://d1.tide-tables.example",
		"deploy":"dep_3","commit":"0a1b2c","release":"v1","status":"released","visibility":"unlisted","folder":"site","attached":true}}}`)
	p := r.Meta.Publish
	if p == nil {
		t.Fatalf("meta %s has no publish", r.Meta.RawJSON())
	}
	want := agents.PublishRecord{App: "tide-tables", AppID: "app_7", Name: "Tide tables", URL: "https://tide-tables.example", Preview: "https://d1.tide-tables.example",
		Deploy: "dep_3", Commit: "0a1b2c", Release: "v1", Status: agents.PublishReleased, Visibility: "unlisted"}
	if *p != want {
		t.Errorf("publish %+v, want %+v", *p, want)
	}
	if r.Meta.Path != "" || r.Meta.Size != nil {
		t.Errorf("a publish read as a file: %+v", r.Meta)
	}
	var raw struct {
		Publish struct {
			Folder   string `json:"folder"`
			Attached bool   `json:"attached"`
		} `json:"publish"`
	}
	if err := json.Unmarshal(r.Meta.RawJSON(), &raw); err != nil || raw.Publish.Folder != "site" || !raw.Publish.Attached {
		t.Errorf("RawJSON = %s, %v; want the record with the fields the package does not model", r.Meta.RawJSON(), err)
	}

	refused := toolResult(t, `{"tool_use_id":"call_5","content":[],"is_error":true,"outcome":"error","meta":{"publish":{
		"app":"tide-tables","status":"refused","error":{"code":"behind_live","message":"The app serves a newer commit."}}}}`)
	if p := refused.Meta.Publish; p.Status != agents.PublishRefused || p.Error == nil || p.Error.Code != "behind_live" || p.Error.Message == "" {
		t.Errorf("a refused release read as %+v", p)
	}
}

// A result that records nothing has no meta, and the meta of a tool the
// package does not model keeps all it recorded.
func TestAToolResultWithoutAFile(t *testing.T) {
	if r := toolResult(t, `{"tool_use_id":"call_6","content":[{"type":"text","text":"exit 0"}],"outcome":"ok"}`); r.Meta != nil {
		t.Errorf("a result without meta read as %+v", r.Meta)
	}
	r := toolResult(t, `{"tool_use_id":"call_7","content":[],"outcome":"ok","meta":{"todos":[{"id":"1","content":"Draft","status":"pending"}]}}`)
	if r.Meta == nil || r.Meta.Path != "" || r.Meta.Size != nil || r.Meta.Publish != nil {
		t.Fatalf("a todo call's meta read as %+v", r.Meta)
	}
	if !strings.Contains(string(r.Meta.RawJSON()), `"todos"`) {
		t.Errorf("RawJSON = %s; want the todo list", r.Meta.RawJSON())
	}
	if (&agents.ToolMeta{}).RawJSON() != nil {
		t.Error("a meta the program built has JSON")
	}
}

func TestTheMachineASessionWorksOn(t *testing.T) {
	e := agents.Event{ID: "evt_2", Type: agents.EventSessionMachine, Payload: json.RawMessage(`{"machine":{"kind":"device","id":"sbx_1",
		"environment":"dev-q7v3k9m2abcdefgh","workdir":"/Users/ann/src","os":"darwin","arch":"arm64","egress":"open"},"reason":"restarted","cause":"out_of_memory",
		"context":"The machine runs macOS."}`)}
	var m agents.SessionMachine
	if err := e.Decode(&m); err != nil {
		t.Fatal(err)
	}
	want := agents.SessionMachine{
		Machine: agents.AttachedMachine{Kind: agents.MachineDevice, ID: "sbx_1", Environment: "dev-q7v3k9m2abcdefgh", Workdir: "/Users/ann/src", OS: "darwin", Arch: "arm64"},
		Reason:  agents.MachineRestarted, Cause: "out_of_memory",
	}
	if m != want {
		t.Errorf("machine %+v, want %+v", m, want)
	}
}

// A session that keeps its files in a folder names it, and one that keeps
// them as blobs names none.
func TestASessionNamesItsFolder(t *testing.T) {
	folder := `"folder":{"owner":"https://login.example|org_9","path":"files/conversations/ses_1"},`
	c, _ := fakeAPI(t, map[string]string{
		"GET /v1/agents/sessions/ses_1": strings.Replace(session, `"title":"Notes",`, `"title":"Notes",`+folder, 1),
		"GET /v1/agents/sessions/ses_2": session,
	})
	got, err := c.GetSession(t.Context(), "ses_1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Folder == nil || got.Folder.Owner != "https://login.example|org_9" || got.Folder.Path != "files/conversations/ses_1" {
		t.Errorf("folder %+v", got.Folder)
	}
	blobs, err := c.GetSession(t.Context(), "ses_2")
	if err != nil {
		t.Fatal(err)
	}
	if blobs.Folder != nil {
		t.Errorf("a session without a folder read as %+v", blobs.Folder)
	}
}
