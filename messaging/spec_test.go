// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package messaging_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"latere.ai/x/sdk-go/latere"
	"latere.ai/x/sdk-go/messaging"
)

// The models of the Client are written by hand from the messaging
// service's OpenAPI document, vendored in openapi/messaging.json by
// `make openapi`. These tests hold the two together: every field a model
// reads or sends is in its schema and every field the schema has is in
// the model, the values the package names are the ones the document
// lists, each call is an operation of the document answered with the
// status and schema the Client reads and naming the device exactly where
// the document requires it, and each refusal the package names is a code
// the document describes under the status the sentinel says.

type schema struct {
	Type       any                `json:"type"`
	Format     string             `json:"format"`
	Properties map[string]*schema `json:"properties"`
	Required   []string           `json:"required"`
	Items      *schema            `json:"items"`
	Ref        string             `json:"$ref"`
	Enum       []string           `json:"enum"`
}

type parameter struct {
	Ref      string `json:"$ref"`
	Name     string `json:"name"`
	In       string `json:"in"`
	Required bool   `json:"required"`
}

type operation struct {
	Parameters  []parameter `json:"parameters"`
	RequestBody struct {
		Content map[string]struct {
			Schema *schema `json:"schema"`
		} `json:"content"`
	} `json:"requestBody"`
	Responses map[string]struct {
		Content map[string]struct {
			Schema *schema `json:"schema"`
		} `json:"content"`
	} `json:"responses"`
}

type document struct {
	Paths      map[string]map[string]json.RawMessage `json:"paths"`
	Components struct {
		Schemas    map[string]*schema   `json:"schemas"`
		Parameters map[string]parameter `json:"parameters"`
	} `json:"components"`
}

func loadDocument(t *testing.T) *document {
	t.Helper()
	raw, err := os.ReadFile("../openapi/messaging.json")
	if err != nil {
		t.Fatal(err)
	}
	var d document
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	return &d
}

func readOperation(t *testing.T, d *document, method, path string) *operation {
	t.Helper()
	raw, ok := d.Paths[path][method]
	if !ok {
		t.Fatalf("%s %s is not in the document", strings.ToUpper(method), path)
	}
	var op operation
	if err := json.Unmarshal(raw, &op); err != nil {
		t.Fatal(err)
	}
	return &op
}

var (
	timeType  = reflect.TypeFor[time.Time]()
	bytesType = reflect.TypeFor[[]byte]()
)

func jsonFields(t reflect.Type) map[string]reflect.Type {
	out := map[string]reflect.Type{}
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			out[name] = t.Field(i).Type
		}
	}
	return out
}

// holdToSchema checks that every field model has is in sch, a date-time
// as a time.Time and bytes as []byte, that model has every field sch
// has, and each field sch requires.
func holdToSchema(t *testing.T, name string, model any, sch *schema) {
	t.Helper()
	if sch == nil {
		t.Errorf("%s: the document has no schema for it", name)
		return
	}
	fields := jsonFields(reflect.TypeOf(model))
	for f, typ := range fields {
		prop, ok := sch.Properties[f]
		if !ok {
			t.Errorf("%s.%s is not in the schema", name, f)
			continue
		}
		if prop.Format == "date-time" && typ != timeType {
			t.Errorf("%s.%s is a date-time, read as %v", name, f, typ)
		}
		if prop.Ref == "#/components/schemas/Bytes" && typ != bytesType {
			t.Errorf("%s.%s is bytes, read as %v", name, f, typ)
		}
	}
	for prop := range sch.Properties {
		if _, ok := fields[prop]; !ok {
			t.Errorf("%s does not have %s", name, prop)
		}
	}
	for _, r := range sch.Required {
		if _, ok := fields[r]; !ok {
			t.Errorf("%s does not have %s, which the schema requires", name, r)
		}
	}
}

func TestModelsMatchTheSchemas(t *testing.T) {
	s := loadDocument(t).Components.Schemas
	for name, c := range map[string]struct {
		model  any
		schema *schema
	}{
		"DeviceRecord":      {messaging.DeviceRecord{}, s["DeviceRecord"]},
		"Revocation":        {messaging.Revocation{}, s["Revocation"]},
		"DeviceList":        {messaging.DeviceList{}, s["DeviceList"]},
		"Recovery":          {messaging.Recovery{}, s["Recovery"]},
		"KeyPackageCount":   {messaging.KeyPackageCount{}, s["KeyPackageCount"]},
		"ClaimedKeyPackage": {messaging.ClaimedKeyPackage{}, s["ClaimedKeyPackage"]},
		"Conversation":      {messaging.Conversation{}, s["Conversation"]},
		"ConversationList":  {messaging.ConversationList{}, s["ConversationList"]},
		"Outgoing":          {messaging.Outgoing{}, s["MessageSend"]},
		"Sent":              {messaging.Sent{}, s["MessageSent"]},
		"Message":           {messaging.Message{}, s["Message"]},
		"MessageList":       {messaging.MessageList{}, s["MessageList"]},
		"Welcome":           {messaging.Welcome{}, s["Welcome"]},
		"Person":            {messaging.Person{}, s["Person"]},
	} {
		holdToSchema(t, name, c.model, c.schema)
	}
}

// text is what a value is on the wire.
func text(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("%v is not a string on the wire: %s", v, b)
	}
	return s
}

// The kinds and levels the package names are the ones the document lists.
func TestTheValuesAreTheDocuments(t *testing.T) {
	s := loadDocument(t).Components.Schemas
	for _, c := range []struct {
		name   string
		values []any
	}{
		{"MessageKind", []any{messaging.MessageApplication, messaging.MessageProposal, messaging.MessageCommit}},
		{"ConversationKind", []any{messaging.KindOneOnOne, messaging.KindGroup}},
		{"Level", []any{messaging.LevelOff, messaging.LevelReadsAlong, messaging.LevelTakesPart}},
		{"RecordKind", []any{messaging.RecordDevice, messaging.RecordRecovery}},
	} {
		var declared []string
		for _, v := range c.values {
			declared = append(declared, text(t, v))
		}
		listed := s[c.name]
		if listed == nil || !slices.Equal(slices.Sorted(slices.Values(declared)), slices.Sorted(slices.Values(listed.Enum))) {
			t.Errorf("%s: the package declares %v; the document lists %+v", c.name, declared, listed)
		}
	}
}

// call is one call of the Client as the document describes it.
type call struct {
	method, path, status, request, answer string
	asDevice                              bool
}

// calls are the Client's calls, each with the device header when it
// sends one.
var calls = []call{
	{"get", "/v1/messaging/service", "200", "", "Service", false},
	{"get", "/v1/messaging/people/{person}/devices", "200", "", "DeviceList", false},
	{"put", "/v1/messaging/devices/{device}", "204", "DeviceRecord", "", false},
	{"post", "/v1/messaging/revocations", "204", "Revocation", "", false},
	{"put", "/v1/messaging/recovery", "204", "Recovery", "", false},
	{"get", "/v1/messaging/recovery", "200", "", "Recovery", false},
	{"post", "/v1/messaging/devices/{device}/transfer", "204", "Transfer", "", false},
	{"get", "/v1/messaging/devices/{device}/transfer", "200", "", "Transfer", true},
	{"post", "/v1/messaging/key-packages", "204", "KeyPackagePublish", "", true},
	{"get", "/v1/messaging/key-packages", "200", "", "KeyPackageCount", true},
	{"post", "/v1/messaging/key-packages/claim", "200", "KeyPackageClaim", "ClaimedKeyPackages", false},
	{"post", "/v1/messaging/conversations", "201", "ConversationCreate", "Conversation", true},
	{"get", "/v1/messaging/conversations", "200", "", "ConversationList", false},
	{"get", "/v1/messaging/conversations/{id}", "200", "", "Conversation", false},
	{"get", "/v1/messaging/conversations/{id}/group-info", "200", "", "GroupInfo", true},
	{"post", "/v1/messaging/conversations/{id}/messages", "201", "MessageSend", "MessageSent", true},
	{"get", "/v1/messaging/conversations/{id}/messages", "200", "", "MessageList", true},
	{"get", "/v1/messaging/welcomes", "200", "", "WelcomeList", true},
	{"delete", "/v1/messaging/welcomes/{id}", "204", "", "", true},
	{"get", "/v1/messaging/conversations/{id}/people/{person}", "200", "", "Membership", false},
	{"get", "/v1/messaging/people", "200", "", "Person", false},
}

// Every operation of the document is a call of the Client, answered with
// the status and the schema the Client reads, taking the body it sends,
// and requiring the device header exactly where the Client sends it.
func TestCallsAreTheDocuments(t *testing.T) {
	d := loadDocument(t)
	seen := map[string]bool{}
	for _, c := range calls {
		key := strings.ToUpper(c.method) + " " + c.path
		seen[key] = true
		op := readOperation(t, d, c.method, c.path)
		answer, ok := op.Responses[c.status]
		if !ok {
			t.Errorf("%s answers no %s", key, c.status)
			continue
		}
		if got := refOf(answer.Content["application/json"].Schema); got != c.answer {
			t.Errorf("%s answers %q; the Client reads %q", key, got, c.answer)
		}
		if got := refOf(op.RequestBody.Content["application/json"].Schema); got != c.request {
			t.Errorf("%s takes %q; the Client sends %q", key, got, c.request)
		}
		required := false
		for _, p := range op.Parameters {
			if p.Ref != "" {
				p = d.Components.Parameters[strings.TrimPrefix(p.Ref, "#/components/parameters/")]
			}
			if p.In == "header" && p.Name == messaging.DeviceHeader {
				required = p.Required
			}
		}
		if required != c.asDevice {
			t.Errorf("%s requires the device header: %v; the Client sends it: %v", key, required, c.asDevice)
		}
	}
	for path, ops := range d.Paths {
		for method := range ops {
			if key := strings.ToUpper(method) + " " + path; !seen[key] {
				t.Errorf("%s is in the document and the Client does not call it", key)
			}
		}
	}
}

func refOf(s *schema) string {
	if s == nil {
		return ""
	}
	return strings.TrimPrefix(s.Ref, "#/components/schemas/")
}

// The wait and the list limits the package states are the document's.
func TestTheLimitsAreTheDocuments(t *testing.T) {
	raw, err := os.ReadFile("../openapi/messaging.json")
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Components struct {
			Parameters map[string]struct {
				Schema struct {
					Maximum float64 `json:"maximum"`
				} `json:"schema"`
			} `json:"parameters"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	if got := time.Duration(d.Components.Parameters["Wait"].Schema.Maximum) * time.Second; got != messaging.MaxWait {
		t.Errorf("the document waits at most %v; the package says %v", got, messaging.MaxWait)
	}
}

// Each code the package has a sentinel for is one the document lists
// under the status the sentinel says, and the SDK answers it with the
// sentinel and with the sentinel of its status.
func TestTheCodesAreTheDocuments(t *testing.T) {
	codes := loadDocument(t).Components.Schemas["ErrorCode"]
	if codes == nil {
		t.Fatal("the document has no ErrorCode")
	}
	raw, err := os.ReadFile("../openapi/messaging.json")
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Components struct {
			Schemas map[string]struct {
				Description string `json:"description"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	described := d.Components.Schemas["ErrorCode"].Description
	for _, c := range []struct {
		code     string
		status   int
		sentinel error
		status2  error
	}{
		{"not_your_device", http.StatusForbidden, messaging.ErrNotYourDevice, latere.ErrForbidden},
		{"exists", http.StatusConflict, messaging.ErrExists, latere.ErrConflict},
		{"epoch_taken", http.StatusConflict, messaging.ErrEpochTaken, latere.ErrConflict},
		{"wrong_epoch", http.StatusConflict, messaging.ErrWrongEpoch, latere.ErrConflict},
		{"refused", http.StatusUnprocessableEntity, messaging.ErrRefused, nil},
		{"too_large", http.StatusRequestEntityTooLarge, messaging.ErrTooLarge, nil},
		{"authorizer_unavailable", http.StatusServiceUnavailable, messaging.ErrAuthorizerUnavailable, nil},
	} {
		if !slices.Contains(codes.Enum, c.code) || !strings.Contains(described, fmt.Sprintf("`%s` (%d)", c.code, c.status)) {
			t.Errorf("the document does not list %s under %d", c.code, c.status)
		}
		err := refusedWith(t, c.status, c.code)
		if !errors.Is(err, c.sentinel) || (c.status2 != nil && !errors.Is(err, c.status2)) {
			t.Errorf("%d %s: %v matches neither its sentinel nor its status's", c.status, c.code, err)
		}
		var apiErr *latere.Error
		if !errors.As(err, &apiErr) || apiErr.Code != c.code {
			t.Errorf("%d %s: the service's answer is not under the error", c.status, c.code)
		}
	}
}
