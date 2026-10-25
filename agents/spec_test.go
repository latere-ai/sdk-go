// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package agents

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"latere.ai/x/sdk-go/internal/sse"
	"latere.ai/x/sdk-go/latere"
)

// The agents API's OpenAPI document, vendored in openapi/agents.json by
// `make openapi`, describes every body by a schema. These tests hold the
// models to it: every field a model reads is in its schema, a date-time
// is read as a time.Time, every value of the models' enums is one the
// document lists, a payload model reads every field of its schema or
// says why not in leftOut, and every request the client sends fits the
// schema of its route.

type document struct {
	Paths      map[string]map[string]json.RawMessage `json:"paths"`
	Components struct {
		Schemas map[string]*schema `json:"schemas"`
	} `json:"components"`
}

// schema is the part of a JSON Schema these tests read.
type schema struct {
	Ref                  string             `json:"$ref"`
	Type                 string             `json:"type"`
	Format               string             `json:"format"`
	Properties           map[string]*schema `json:"properties"`
	Required             []string           `json:"required"`
	Items                *schema            `json:"items"`
	AdditionalProperties json.RawMessage    `json:"additionalProperties"`
	AllOf                []*schema          `json:"allOf"`
	AnyOf                []*schema          `json:"anyOf"`
	OneOf                []*schema          `json:"oneOf"`
	Enum                 []string           `json:"enum"`
	Const                any                `json:"const"`
}

func loadDocument(t *testing.T) (*document, any) {
	t.Helper()
	raw, err := os.ReadFile("../openapi/agents.json")
	if err != nil {
		t.Fatal(err)
	}
	var d document
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		t.Fatal(err)
	}
	return &d, tree
}

// resolve follows a reference, steps past a nullable anyOf, and joins an
// allOf's members into one object.
func (d *document) resolve(s *schema) *schema {
	for s != nil && s.Ref != "" {
		s = d.Components.Schemas[strings.TrimPrefix(s.Ref, "#/components/schemas/")]
	}
	if s == nil {
		return nil
	}
	if len(s.AnyOf) > 0 {
		for _, branch := range s.AnyOf {
			if r := d.resolve(branch); r != nil && (r.Properties != nil || r.Items != nil || r.Format != "" || len(r.Enum) > 0 || r.Ref != "") {
				return r
			}
		}
	}
	if len(s.AllOf) > 0 {
		joined := &schema{Properties: map[string]*schema{}}
		for _, part := range s.AllOf {
			r := d.resolve(part)
			if r == nil {
				continue
			}
			for k, v := range r.Properties {
				joined.Properties[k] = v
			}
			joined.Required = append(joined.Required, r.Required...)
		}
		return joined
	}
	return s
}

// payload is the schema of the payload of an event of type typ.
func (d *document) payload(typ string) *schema {
	variant := d.resolve(d.Components.Schemas["Event."+typ])
	if variant == nil {
		return nil
	}
	return variant.Properties["payload"]
}

// anyValue reports a schema that allows any JSON value, as the document
// writes a field whose shape it leaves open: no type, no properties and
// nothing else to check a model against.
func (s *schema) anyValue() bool {
	return s.Ref == "" && s.Type == "" && s.Format == "" && s.Properties == nil && s.Items == nil && len(s.AllOf) == 0 &&
		len(s.AnyOf) == 0 && len(s.OneOf) == 0 && len(s.Enum) == 0 && s.Const == nil
}

// sent is the schema of the payload a person sends for type typ.
func (d *document) sent(typ string) *schema {
	variant := d.resolve(d.Components.Schemas["SendEvent."+typ])
	if variant == nil {
		return nil
	}
	return variant.Properties["payload"]
}

var timeType = reflect.TypeFor[time.Time]()

// leftOut is the fields of a payload's schema its model does not read,
// each with its reason. A field missing from a model and from this list
// fails TestModelsReadTheirSchemas, so a field the API adds is decided on.
var leftOut = map[string]string{
	"Event.payload":          "kept raw and decoded on demand with Event.Decode, since its shape follows the event's type",
	"TriggerStatus.owner":    "the subject the trigger acts as, which its reader already is",
	"TriggerStatus.identity": "the identity the trigger's sessions are started with, which no client reads yet",
}

// aheadOfTheDocument is the fields models read that the published
// document does not describe yet, by the model's Go type and the field's
// JSON name, each with the schema it will join and what it is. checkModel
// takes them as read, and TestFieldsAheadOfTheDocumentAreStillAhead fails
// once the document describes one, so the model is then held to it.
var aheadOfTheDocument = map[string]ahead{
	"TriggerSession.model": {"TriggerSession", "the model each session a firing starts runs on, as a session's update names it"},
	"TriggerSpec.times":    {"TriggerSpec", "the times a trigger fires at, once each, before the API deletes it"},
}

// ahead is a field of aheadOfTheDocument: the schema it will join, a
// component's name or "event:" and an event's type for its payload, and
// what it is.
type ahead struct{ schema, what string }

// TestFieldsAheadOfTheDocumentAreStillAhead fails once the document
// describes a field of aheadOfTheDocument, which is then taken off it.
func TestFieldsAheadOfTheDocumentAreStillAhead(t *testing.T) {
	d, _ := loadDocument(t)
	for field, a := range aheadOfTheDocument {
		var s *schema
		if typ, ok := strings.CutPrefix(a.schema, "event:"); ok {
			s = d.resolve(d.payload(typ))
		} else {
			s = d.resolve(d.Components.Schemas[a.schema])
		}
		if s == nil {
			t.Errorf("%s: the document has no %s", field, a.schema)
			continue
		}
		_, name, _ := strings.Cut(field, ".")
		if _, ok := s.Properties[name]; ok {
			t.Errorf("the document now describes %s (%s): hold the model to it and take it off aheadOfTheDocument", field, a.what)
		}
	}
}

// checkModel compares the model type typ with s: every JSON field of typ
// is a property of s, a date-time is read as a time.Time, and a nested
// struct is compared with its property's schema. With complete, every
// property of s is read or in leftOut under name.
func (d *document) checkModel(t *testing.T, name string, typ reflect.Type, s *schema, complete bool) {
	t.Helper()
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	s = d.resolve(s)
	if s == nil {
		t.Errorf("%s: the document has no schema for it", name)
		return
	}
	switch typ.Kind() {
	case reflect.Slice:
		if s.Items != nil {
			d.checkModel(t, name+"[]", typ.Elem(), s.Items, false)
		}
		return
	case reflect.Map:
		return
	case reflect.Struct:
	default:
		return
	}
	if typ == timeType || s.anyValue() {
		// A field the document leaves any JSON value is held to the
		// document by a test of its own, as ToolMeta is.
		return
	}
	read := map[string]bool{}
	for f := range typ.Fields() {
		tag, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if tag == "" || tag == "-" {
			continue
		}
		read[tag] = true
		prop, ok := s.Properties[tag]
		if !ok {
			if _, coming := aheadOfTheDocument[typ.Name()+"."+tag]; coming {
				continue
			}
			t.Errorf("%s.%s is read, and the document has no such field", name, tag)
			continue
		}
		resolved := d.resolve(prop)
		ft := f.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if resolved != nil && resolved.Format == "date-time" && ft != timeType {
			t.Errorf("%s.%s is a date-time, read as %s", name, tag, f.Type)
		}
		if ft.Kind() == reflect.Struct && ft != timeType || ft.Kind() == reflect.Slice {
			d.checkModel(t, name+"."+tag, f.Type, prop, false)
		}
	}
	if !complete {
		return
	}
	for prop := range s.Properties {
		if !read[prop] && leftOut[name+"."+prop] == "" {
			t.Errorf("%s does not read %s; model it or add it to leftOut with a reason", name, prop)
		}
	}
}

func TestModelsMatchTheSchemas(t *testing.T) {
	d, _ := loadDocument(t)
	s := d.Components.Schemas
	d.checkModel(t, "Session", reflect.TypeFor[Session](), s["Session"], false)
	d.checkModel(t, "Event", reflect.TypeFor[Event](), s["EventEnvelope"], true)
	d.checkModel(t, "Delta", reflect.TypeFor[Delta](), s["Delta"], false)
	// A model may stand for the payloads of several event types, as
	// ToolResult does for tool.result and user.tool_result; each field it
	// reads is in one of them.
	for _, p := range []struct {
		types []string
		model reflect.Type
	}{
		{[]string{"session.status"}, reflect.TypeFor[SessionStatus]()},
		{[]string{"agent.message"}, reflect.TypeFor[AgentMessage]()},
		{[]string{"agent.tool_use"}, reflect.TypeFor[ToolUse]()},
		{[]string{"tool.result", "user.tool_result"}, reflect.TypeFor[ToolResult]()},
		{[]string{"session.error"}, reflect.TypeFor[SessionError]()},
		{[]string{"user.message"}, reflect.TypeFor[UserMessage]()},
		{[]string{"session.machine"}, reflect.TypeFor[SessionMachine]()},
		{[]string{"approval.requested"}, reflect.TypeFor[ApprovalRequested]()},
		{[]string{"model.request"}, reflect.TypeFor[ModelRequest]()},
	} {
		union := &schema{Properties: map[string]*schema{}}
		for _, typ := range p.types {
			payload := d.resolve(d.payload(typ))
			if payload == nil {
				t.Errorf("%s: the document has no payload schema", typ)
				continue
			}
			for k, v := range payload.Properties {
				union.Properties[k] = v
			}
		}
		d.checkModel(t, strings.Join(p.types, "|"), p.model, union, false)
	}
	// The files an answer kept, which a client reads them by, are read
	// whole.
	d.checkModel(t, "files.kept", reflect.TypeFor[FilesKept](), d.payload("files.kept"), true)
	d.checkModel(t, "KeptFile", reflect.TypeFor[KeptFile](), s["KeptFile"], true)
	d.checkModel(t, "SkippedFile", reflect.TypeFor[SkippedFile](), s["SkippedFile"], true)
}

// metaOutsideTheDocument is the fields ToolMeta reads that none of the
// document's meta schemas names, each with its reason. A field ToolMeta
// reads that is neither in one of them nor here fails
// TestToolMetaFollowsTheDocument.
var metaOutsideTheDocument = map[string]string{
	"size":    "the size a write or an edit left, which the document names no meta schema for",
	"publish": "a publish call's record, which the document names no meta schema for",
}

// TestToolMetaFollowsTheDocument: the document leaves a tool result's
// meta any JSON value and describes the meta of some tools in schemas of
// their own. Every field ToolMeta reads is in one of them, with its type,
// or is listed in metaOutsideTheDocument.
func TestToolMetaFollowsTheDocument(t *testing.T) {
	d, _ := loadDocument(t)
	s := d.Components.Schemas
	if meta := d.resolve(d.payload("tool.result")).Properties["meta"]; meta == nil || !d.resolve(meta).anyValue() {
		t.Fatal("the document now gives tool.result's meta a schema; hold ToolMeta to it in TestModelsMatchTheSchemas")
	}
	union := &schema{Properties: map[string]*schema{}}
	for _, name := range []string{"ReadResultMeta", "QuestionResultMeta", "WebSearchResultMeta"} {
		m := d.resolve(s[name])
		if m == nil {
			t.Errorf("the document has no %s", name)
			continue
		}
		maps.Copy(union.Properties, m.Properties)
	}
	for field, reason := range metaOutsideTheDocument {
		if _, ok := union.Properties[field]; ok {
			t.Errorf("the document now names meta.%s; check ToolMeta against it and take it out of metaOutsideTheDocument (%s)", field, reason)
		}
		union.Properties[field] = &schema{}
	}
	d.checkModel(t, "ToolMeta", reflect.TypeFor[ToolMeta](), union, false)
}

// TestEnumsAreTheDocuments: every value a model's enum declares is one
// the document lists for that field.
func TestEnumsAreTheDocuments(t *testing.T) {
	d, _ := loadDocument(t)
	s := d.Components.Schemas
	declared := constantsOf(t, ".")
	for typ, values := range map[string][]string{
		"Status":     d.resolve(s["Session"].Properties["status"]).Enum,
		"StopReason": d.resolve(s["Session"].Properties["stop_reason"]).Enum,
		"EventType":  d.resolve(s["EventEnvelope"].Properties["type"]).Enum,
	} {
		if len(values) == 0 {
			t.Errorf("the document lists no values for %s", typ)
			continue
		}
		for _, v := range declared[typ] {
			if !slices.Contains(values, v) {
				t.Errorf("%s declares %q, which the document does not list", typ, v)
			}
		}
	}
}

// constantsOf is the string constants of each named type the package in
// dir declares.
func constantsOf(t *testing.T, dir string) map[string][]string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	fset := token.NewFileSet()
	for _, path := range matches {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs := spec.(*ast.ValueSpec)
				ident, ok := vs.Type.(*ast.Ident)
				if !ok {
					continue
				}
				for _, v := range vs.Values {
					if lit, ok := v.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						out[ident.Name] = append(out[ident.Name], strings.Trim(lit.Value, "\""))
					}
				}
			}
		}
	}
	return out
}

// TestRequestsFitTheirSchemas sends each request the client makes to a
// server that records its body, and checks the body against the schema
// of its route: every member is a property, and every required property
// is there.
func TestRequestsFitTheirSchemas(t *testing.T) {
	d, _ := loadDocument(t)
	s := d.Components.Schemas
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		body = nil
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Errorf("a body that is not JSON: %s", raw)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/events") {
			_, _ = io.WriteString(w, `{"id":"evt_1","seq":1,"session_id":"ses_1","type":"user.message","time":"2026-10-20T12:00:00Z","payload":{}}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"ses_1","status":"idle","created_at":"2026-10-20T12:00:00Z","updated_at":"2026-10-20T12:00:00Z"}`)
	}))
	t.Cleanup(srv.Close)
	cfg, err := latere.NewConfig(latere.WithBaseURL(srv.URL), latere.WithTokenSource(latere.StaticToken("key_test")), latere.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	c := New(cfg)
	ctx := context.Background()
	text := []ContentBlock{{Type: BlockText, Text: "Hi"}}
	for _, r := range []struct {
		name   string
		send   func() error
		schema *schema
		event  string
	}{
		{"CreateSession", func() error {
			_, err := c.CreateSession(ctx, &CreateSessionParams{Agent: "a", Message: "Hi", Title: "T", Attended: true, Metadata: map[string]string{"k": "v"}})
			return err
		}, s["CreateSessionRequest"], ""},
		{"EndSession", func() error { _, err := c.EndSession(ctx, "ses_1", StopCompleted); return err }, s["EndSessionRequest"], ""},
		{"ForkSession", func() error {
			_, err := c.ForkSession(ctx, "ses_1", &ForkSessionParams{BeforeSeq: 3, Title: "T", Message: text, Attended: true})
			return err
		}, s["ForkSessionRequest"], ""},
		{"CreateSession, on a computer", func() error {
			_, err := c.CreateSession(ctx, &CreateSessionParams{Agent: "a", Machine: &Machine{Kind: MachineDevice, Environment: "dev-q7v3k9m2abcdefgh", Workdir: "/Users/ann/src"}})
			return err
		}, s["CreateSessionRequest"], ""},
		{"UpdateSession", func() error {
			_, err := c.UpdateSession(ctx, "ses_1", &UpdateSessionParams{
				Model: &ModelChange{Name: "tier/thorough", Reasoning: ReasoningHigh}, Policy: PolicyConfirm, Title: "T",
				Metadata: map[string]string{"k": "v"}, DeleteMetadata: []string{"old"},
			})
			return err
		}, s["UpdateSessionRequest"], ""},
		{"ResumeSession", func() error {
			_, err := c.ResumeSession(ctx, "ses_1", &ResumeSessionParams{MaxCostMicroUSD: 5_000_000, Reason: "budget_raised"})
			return err
		}, s["ResumeSessionRequest"], ""},
		{"SendMessage", func() error { _, err := c.SendMessage(ctx, "ses_1", &SendMessageParams{Content: text}); return err }, nil, "user.message"},
		{"SendMessage, a picture and files", func() error {
			_, err := c.SendMessage(ctx, "ses_1", &SendMessageParams{
				Content: []ContentBlock{Text("Look."), ImageBlock("image/png", []byte("png"))},
				Attachments: []NewAttachment{
					{Name: "notes.md", MediaType: "text/markdown", Data: []byte("# Notes")},
					{Name: "talk.mp4", Source: &AttachmentSource{Owner: "me", Path: "files/talk.mp4", Version: 2}},
				},
			})
			return err
		}, nil, "user.message"},
		{"Interrupt", func() error { _, err := c.Interrupt(ctx, "ses_1"); return err }, nil, "user.interrupt"},
		{"AnswerQuestion", func() error {
			_, err := c.AnswerQuestion(ctx, "ses_1", &AnswerQuestionParams{ToolUseID: "tu_1", Answers: []Answer{{Selected: []string{"Yes"}}}})
			return err
		}, nil, "user.answer"},
		{"ConfirmTool", func() error {
			_, err := c.ConfirmTool(ctx, "ses_1", &ConfirmToolParams{ToolUseID: "tu_1", Decision: DecisionAllow})
			return err
		}, nil, "user.tool_confirmation"},
		{"SendToolResult", func() error {
			_, err := c.SendToolResult(ctx, "ses_1", &SendToolResultParams{ToolUseID: "tu_1", Content: text})
			return err
		}, nil, "user.tool_result"},
		// The least each request sends, as a program that sets only what
		// the server requires does.
		{"CreateSession, least", func() error {
			_, err := c.CreateSession(ctx, &CreateSessionParams{Agent: "a"})
			return err
		}, s["CreateSessionRequest"], ""},
		{"ForkSession, least", func() error { _, err := c.ForkSession(ctx, "ses_1", &ForkSessionParams{}); return err }, s["ForkSessionRequest"], ""},
		{"ForkSession, a copy at a turn's end", func() error {
			_, err := c.ForkSession(ctx, "ses_1", &ForkSessionParams{AtSeq: 5, Untitled: true, NewTree: true, Attended: true})
			return err
		}, s["ForkSessionRequest"], ""},
		{"ForkSession, an edit that keeps a file", func() error {
			_, err := c.ForkSession(ctx, "ses_1", &ForkSessionParams{BeforeSeq: 3, Message: text, Attachments: []NewAttachment{{Name: "a.txt", Blob: "sha256:ab"}}})
			return err
		}, s["ForkSessionRequest"], ""},
		{"SendMessage, asked and answering", func() error {
			_, err := c.SendMessage(ctx, "ses_1", &SendMessageParams{Content: text, Askable: true, Answers: "ask_1", Attachments: []NewAttachment{{Name: "a.txt", Blob: "sha256:ab"}}})
			return err
		}, nil, "user.message"},
		{"ResumeSession, least", func() error { _, err := c.ResumeSession(ctx, "ses_1", nil); return err }, s["ResumeSessionRequest"], ""},
		{"UpdateSession, the agent's reasoning", func() error {
			_, err := c.UpdateSession(ctx, "ses_1", &UpdateSessionParams{Model: &ModelChange{AgentReasoning: true}})
			return err
		}, s["UpdateSessionRequest"], ""},
		{"SendMessage, a file alone", func() error {
			_, err := c.SendMessage(ctx, "ses_1", &SendMessageParams{Attachments: []NewAttachment{{Name: "a.txt", Data: []byte("a")}}})
			return err
		}, nil, "user.message"},
		{"ConfirmTool, an approval", func() error {
			_, err := c.ConfirmTool(ctx, "ses_1", &ConfirmToolParams{ApprovalID: "apr_1", Decision: DecisionDeny})
			return err
		}, nil, "user.tool_confirmation"},
		{"SendToolResult, no content", func() error {
			_, err := c.SendToolResult(ctx, "ses_1", &SendToolResultParams{ToolUseID: "tu_1"})
			return err
		}, nil, "user.tool_result"},
	} {
		if err := r.send(); err != nil {
			t.Errorf("%s: %v", r.name, err)
			continue
		}
		want, got := r.schema, any(body)
		if r.event != "" {
			if body["type"] != r.event {
				t.Errorf("%s sent the type %v, want %s", r.name, body["type"], r.event)
			}
			want, got = d.sent(r.event), body["payload"]
		}
		d.checkBody(t, r.name, got, want)
	}
}

// checkBody checks a JSON value a request carries against s: every
// member is a property, and every required property is there.
func (d *document) checkBody(t *testing.T, name string, v any, s *schema) {
	t.Helper()
	d.checkValue(t, name, v, s, true)
}

// checkMembers checks that every member of a JSON value a request carries
// is a property of s, for a request whose schema is its resource's as the
// API answers it, which requires what the API fills in by default.
func (d *document) checkMembers(t *testing.T, name string, v any, s *schema) {
	t.Helper()
	d.checkValue(t, name, v, s, false)
}

func (d *document) checkValue(t *testing.T, name string, v any, s *schema, required bool) {
	t.Helper()
	s = d.resolve(s)
	if s == nil {
		t.Errorf("%s: the document has no schema for it", name)
		return
	}
	switch v := v.(type) {
	case map[string]any:
		if s.Properties == nil {
			return
		}
		for k, member := range v {
			prop, ok := s.Properties[k]
			if !ok {
				t.Errorf("%s sends %s, which the document does not take", name, k)
				continue
			}
			d.checkValue(t, name+"."+k, member, prop, required)
		}
		if !required {
			return
		}
		for _, r := range s.Required {
			if _, ok := v[r]; !ok {
				t.Errorf("%s does not send %s, which the document requires", name, r)
			}
		}
	case []any:
		if s.Items != nil {
			for _, item := range v {
				d.checkValue(t, name+"[]", item, s.Items, required)
			}
		}
	}
}

func TestTheStreamExampleDecodes(t *testing.T) {
	d, _ := loadDocument(t)
	var op struct {
		Responses map[string]struct {
			Content map[string]struct {
				Example string `json:"example"`
			} `json:"content"`
		} `json:"responses"`
	}
	if err := json.Unmarshal(d.Paths["/sessions/{id}/stream"]["get"], &op); err != nil {
		t.Fatal(err)
	}
	example := op.Responses["200"].Content["text/event-stream"].Example
	if example == "" {
		t.Fatal("the stream has no example")
	}
	r := sse.NewReader(strings.NewReader(example))
	n, messages := 0, 0
	for {
		f, err := r.Next()
		if err != nil {
			break
		}
		item, err := decodeFrame(latere.Frame{ID: f.ID, Event: f.Event, Data: f.Data})
		if err != nil {
			t.Errorf("frame %s: %v", f.ID, err)
		}
		if item.Event != nil && item.Event.Seq == 0 {
			t.Errorf("frame %s decoded without a sequence", f.ID)
		}
		if item.Event != nil && item.Event.Type == EventAgentMessage {
			var m AgentMessage
			if err := item.Event.Decode(&m); err != nil || m.Text() == "" || m.StopReason == "" {
				t.Errorf("the example's agent message read as %+v, %v", m, err)
			}
			messages++
		}
		n++
	}
	if n == 0 || messages == 0 {
		t.Errorf("the stream example held %d frames and %d agent messages", n, messages)
	}
}

func TestEndpointsAreInTheDocument(t *testing.T) {
	d, _ := loadDocument(t)
	// The document's paths are written under the server root /v1; the API
	// serves them under /v1/agents.
	for _, e := range []struct{ method, path string }{
		{"get", "/sessions"},
		{"post", "/sessions"},
		{"get", "/sessions/{id}"},
		{"delete", "/sessions/{id}"},
		{"post", "/sessions/{id}/archive"},
		{"post", "/sessions/{id}/unarchive"},
		{"post", "/sessions/{id}/end"},
		{"post", "/sessions/{id}/fork"},
		{"patch", "/sessions/{id}"},
		{"post", "/sessions/{id}/resume"},
		{"get", "/sessions/{id}/events"},
		{"post", "/sessions/{id}/events"},
		{"get", "/sessions/{id}/stream"},
		{"get", "/sessions/{id}/blobs/{digest}"},
		{"get", "/sessions/{id}/files"},
		{"get", "/sessions/search"},
		{"get", "/sessions/summary"},
	} {
		if _, ok := d.Paths[e.path][e.method]; !ok {
			t.Errorf("%s %s is not in the document", strings.ToUpper(e.method), e.path)
		}
	}
}
