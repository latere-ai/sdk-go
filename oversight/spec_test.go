// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package oversight

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"latere.ai/x/sdk-go/latere"
)

// The models of this package are written by hand from the Oversight API's
// OpenAPI document, the document of Limen, the decision service, vendored
// in openapi/oversight.json by `make openapi`. These tests hold the two
// together: every field a model reads is in its schema, with a Go type
// that holds the schema's JSON type, a date-time as a time.Time and a
// value that can be null as a pointer; every field the schema requires is
// read, and every field it defines is read or left out with a reason;
// every request the client sends fits the schema of its route, and every
// route and query parameter it sends is in the document; and every value
// of a named type's constants, and every code that has a sentinel, is one
// the document names.
//
// The document writes its paths under /v1, as Limen serves them; the API
// serves them under BasePath, /v1/oversight.

type document struct {
	Paths      map[string]map[string]json.RawMessage `json:"paths"`
	Components struct {
		Schemas    map[string]*schema   `json:"schemas"`
		Parameters map[string]parameter `json:"parameters"`
		Responses  map[string]struct {
			Codes []string `json:"x-limen-codes"`
		} `json:"responses"`
	} `json:"components"`
}

// schema is the part of a JSON Schema these tests read.
type schema struct {
	Ref        string             `json:"$ref"`
	Type       json.RawMessage    `json:"type"`
	Format     string             `json:"format"`
	Properties map[string]*schema `json:"properties"`
	Required   []string           `json:"required"`
	Items      *schema            `json:"items"`
	Enum       []string           `json:"enum"`
	Examples   []string           `json:"examples"`
	Default    any                `json:"default"`
	Minimum    *float64           `json:"minimum"`
	Maximum    *float64           `json:"maximum"`
	MinItems   *int               `json:"minItems"`
	MaxItems   *int               `json:"maxItems"`
}

// types is the JSON types the schema allows: one, as "integer", or
// several, as "number" and "null".
func (s *schema) types() []string {
	var one string
	if json.Unmarshal(s.Type, &one) == nil {
		return []string{one}
	}
	var many []string
	_ = json.Unmarshal(s.Type, &many)
	return many
}

type parameter struct {
	Ref      string  `json:"$ref"`
	In       string  `json:"in"`
	Name     string  `json:"name"`
	Required bool    `json:"required"`
	Schema   *schema `json:"schema"`
}

type operation struct {
	Parameters  []parameter `json:"parameters"`
	RequestBody struct {
		Content map[string]struct {
			Schema *schema `json:"schema"`
		} `json:"content"`
	} `json:"requestBody"`
}

func loadDocument(t *testing.T) *document {
	t.Helper()
	raw, err := os.ReadFile("../openapi/oversight.json")
	if err != nil {
		t.Fatal(err)
	}
	var d document
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	return &d
}

// resolve follows a schema's $ref within the document.
func (d *document) resolve(s *schema) *schema {
	for s != nil && s.Ref != "" {
		s = d.Components.Schemas[strings.TrimPrefix(s.Ref, "#/components/schemas/")]
	}
	return s
}

// at walks a dotted path of properties from a schema, "[]" stepping into
// an array's items.
func (d *document) at(t *testing.T, name string) *schema {
	t.Helper()
	parts := strings.Split(name, ".")
	s := d.resolve(d.Components.Schemas[parts[0]])
	for _, p := range parts[1:] {
		if s == nil {
			break
		}
		if p == "[]" {
			s = d.resolve(s.Items)
			continue
		}
		s = d.resolve(s.Properties[p])
	}
	if s == nil {
		t.Fatalf("the document has no schema at %s", name)
	}
	return s
}

// operation is the document's operation of method on path, a path as the
// document writes it.
func (d *document) operation(t *testing.T, method, path string) (*operation, bool) {
	t.Helper()
	raw, ok := d.Paths[path][strings.ToLower(method)]
	if !ok {
		return nil, false
	}
	var op operation
	if err := json.Unmarshal(raw, &op); err != nil {
		t.Fatal(err)
	}
	for i, p := range op.Parameters {
		if p.Ref != "" {
			op.Parameters[i] = d.Components.Parameters[strings.TrimPrefix(p.Ref, "#/components/parameters/")]
		}
	}
	return &op, true
}

// documentPath is the document's path of a path the client sends.
func documentPath(path string) string {
	return "/v1" + strings.TrimPrefix(path, BasePath)
}

// union is a schema of the properties and the requirements of several,
// for a model that stands for more than one schema.
func union(parts ...*schema) *schema {
	out := &schema{Properties: map[string]*schema{}}
	for _, p := range parts {
		for k, v := range p.Properties {
			out.Properties[k] = v
		}
		out.Required = append(out.Required, p.Required...)
	}
	return out
}

// field is one field of a model by its JSON name, an embedded struct's
// fields included.
type field struct {
	name string
	typ  reflect.Type
}

func fieldsOf(t reflect.Type) []field {
	var out []field
	for f := range t.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if f.Anonymous && f.Type.Kind() == reflect.Struct && name == "" {
			out = append(out, fieldsOf(f.Type)...)
			continue
		}
		if name != "" && name != "-" {
			out = append(out, field{name, f.Type})
		}
	}
	return out
}

var timeType = reflect.TypeFor[time.Time]()

// models are the package's models, each with the schema it is held to.
func models(t *testing.T, d *document) map[string]struct {
	model  reflect.Type
	schema *schema
} {
	type m = struct {
		model  reflect.Type
		schema *schema
	}
	return map[string]m{
		"Learned":     {reflect.TypeFor[Learned](), d.at(t, "Learned")},
		"ModelRef":    {reflect.TypeFor[ModelRef](), d.at(t, "Learned.model")},
		"LearnedKind": {reflect.TypeFor[LearnedKind](), d.at(t, "Learned.entries.[]")},
		"TrendPoint":  {reflect.TypeFor[TrendPoint](), d.at(t, "Learned.recent.[]")},
		"Explanation": {reflect.TypeFor[Explanation](), d.at(t, "Explanation")},
		"Parts":       {reflect.TypeFor[Parts](), d.at(t, "Explanation.parts")},
		"Evidence":    {reflect.TypeFor[Evidence](), d.at(t, "Explanation.evidence.[]")},
		// Cover stands for the cover of a prediction and of a learned
		// entry, and for that of an explanation, which adds nearest.
		"Cover":           {reflect.TypeFor[Cover](), union(d.at(t, "Prediction.cover"), d.at(t, "Explanation.cover"))},
		"NearestApproval": {reflect.TypeFor[NearestApproval](), d.at(t, "Explanation.cover.nearest.[]")},
		"Profile":         {reflect.TypeFor[Profile](), d.at(t, "Profile")},
		"Costs":           {reflect.TypeFor[Costs](), d.at(t, "Profile.costs")},
		"Thresholds":      {reflect.TypeFor[Thresholds](), d.at(t, "Profile.thresholds")},
		"Certify":         {reflect.TypeFor[Certify](), d.at(t, "Profile.certify")},
		"Consent":         {reflect.TypeFor[Consent](), d.at(t, "Profile.consent")},
		"Scope":           {reflect.TypeFor[Scope](), d.at(t, "Prediction.profile")},
		"PreviewParams":   {reflect.TypeFor[PreviewParams](), d.at(t, "PreviewRequest")},
		"Preview":         {reflect.TypeFor[Preview](), d.at(t, "Preview")},
		"AnswerParams":    {reflect.TypeFor[AnswerParams](), d.at(t, "Answer")},
		"ForgetParams":    {reflect.TypeFor[ForgetParams](), d.at(t, "ForgetRequest")},
		"pageOfCases":     {reflect.TypeFor[pageOfCases](), d.at(t, "Page")},
		"Case":            {reflect.TypeFor[Case](), d.at(t, "Page.entries.[]")},
		"CaseAction":      {reflect.TypeFor[CaseAction](), d.at(t, "Case.action")},
		"Actor":           {reflect.TypeFor[Actor](), d.at(t, "Case.action.actor")},
		"Prediction":      {reflect.TypeFor[Prediction](), d.at(t, "Case.prediction")},
		"StepScope":       {reflect.TypeFor[StepScope](), d.at(t, "Case.prediction.scope")},
		"Crosses":         {reflect.TypeFor[Crosses](), d.at(t, "Case.prediction.scope.crosses")},
		"Decision":        {reflect.TypeFor[Decision](), d.at(t, "Case.decision")},
		"RecordedAnswer":  {reflect.TypeFor[RecordedAnswer](), d.at(t, "Case.answers.[]")},
		"Reliability":     {reflect.TypeFor[Reliability](), d.at(t, "Reliability")},
		"Estimate":        {reflect.TypeFor[Estimate](), d.at(t, "Reliability.false_allow")},
		"Certificate":     {reflect.TypeFor[Certificate](), d.at(t, "Reliability.certificate")},
		"Calibration":     {reflect.TypeFor[Calibration](), d.at(t, "Calibration")},
		"CalibrationBin":  {reflect.TypeFor[CalibrationBin](), d.at(t, "Calibration.bins.[]")},
		"Report":          {reflect.TypeFor[Report](), d.at(t, "Report")},
		"Week":            {reflect.TypeFor[Week](), d.at(t, "Report.weeks.[]")},
		"ReportCell":      {reflect.TypeFor[ReportCell](), d.at(t, "Report.weeks.[].total")},
		"AgentCell":       {reflect.TypeFor[AgentCell](), d.at(t, "Report.weeks.[].agents.[]")},
		"VerdictCounts":   {reflect.TypeFor[VerdictCounts](), d.at(t, "ReportCell.decisions")},
		"FlagChecks":      {reflect.TypeFor[FlagChecks](), d.at(t, "ReportCell.flag_reviews")},
		"Rates":           {reflect.TypeFor[Rates](), d.at(t, "ReportCell.applied")},
	}
}

// leftOut is the fields a schema defines that a model leaves out on
// purpose, by the model's name in models and the field's JSON name, each
// with its reason. A field missing from a model and from this list fails
// TestModelsReadEveryField, so a field the API adds is decided on, and
// TestFieldsLeftOutAreStillInTheDocument fails once the document drops
// one, so it is then taken off.
var leftOut = map[string]string{
	"LearnedKind.minimum":    minimum,
	"Explanation.minimum":    minimum,
	"Prediction.minimum":     minimum,
	"CaseAction.input":       "a step's input, which the decision service never keeps, so a case never has it",
	"CaseAction.target":      sentWithTheStep,
	"CaseAction.environment": sentWithTheStep,
	"CaseAction.scope":       sentWithTheStep,
	"CaseAction.reversible":  sentWithTheStep,
	"CaseAction.attributes":  "the further facts the decision point's domain sent with the step, whose shape is the domain's own; read them from Case.RawJSON",
	"CaseAction.signals":     "the other sources' opinions the decision point sent with the step, such as a rule's score; read them from Case.RawJSON",
	"Decision.action":        "the step a decision point sends with a decision the decision service made no prediction for; a case's decision never has it, since the case holds the step in Action",
}

const (
	minimum         = "the answer minimum of Limen v0.6.0, which the cover rule replaced; Limen v0.7.0 and later keep the field for older readers and never set it, so read Cover"
	sentWithTheStep = "the decision point's own description of the step as it sent it; what the decision service read of the step's reach is Prediction.Scope. Read it from Case.RawJSON"
)

// aheadOfTheDocument is the fields models read that the published
// document does not describe yet, by the model's name in models and the
// field's JSON name, each with what it is. TestModelsMatchTheSchemas takes
// them as read, and TestFieldsAheadOfTheDocumentAreStillAhead fails once
// the document describes one, so the model is then held to it.
var aheadOfTheDocument = map[string]string{}

// enumsAheadOfTheDocument is the values the named types' constants
// declare that the published document does not list yet, by the type and
// the value, each with what it is. TestEnumsAreTheDocuments takes them as
// listed, and TestEnumsAheadOfTheDocumentAreStillAhead fails once the
// document lists one, so the type is then held to it.
var enumsAheadOfTheDocument = map[string]string{}

// TestModelsMatchTheSchemas: every field a model reads is in its schema,
// with a Go type that holds the schema's JSON type, and every field the
// schema requires is read.
func TestModelsMatchTheSchemas(t *testing.T) {
	d := loadDocument(t)
	for name, c := range models(t, d) {
		var names []string
		for _, f := range fieldsOf(c.model) {
			names = append(names, f.name)
			prop, ok := c.schema.Properties[f.name]
			if !ok {
				if aheadOfTheDocument[name+"."+f.name] == "" {
					t.Errorf("%s.%s is read, and the schema has no such field", name, f.name)
				}
				continue
			}
			if problem := holds(f.typ, d.resolve(prop)); problem != "" {
				t.Errorf("%s.%s %s", name, f.name, problem)
			}
		}
		for _, r := range c.schema.Required {
			if !slices.Contains(names, r) {
				t.Errorf("%s does not read %s, which the schema requires", name, r)
			}
		}
	}
}

// holds says why a field of the Go type typ cannot hold a value of s, or
// returns "". A date-time is a time.Time, a value that can be null a
// pointer, so null is not read as zero, a number a float, and an integer
// an integer or a float.
func holds(typ reflect.Type, s *schema) string {
	types := s.types()
	if slices.Contains(types, "null") && typ.Kind() != reflect.Pointer {
		return "can be null, and is read as " + typ.String()
	}
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if s.Format == "date-time" || typ == timeType {
		if s.Format != "date-time" || typ != timeType {
			return "is read as " + typ.String() + ", and the schema's format is " + strconv.Quote(s.Format)
		}
		return ""
	}
	var kinds []reflect.Kind
	for _, jt := range types {
		switch jt {
		case "string":
			kinds = append(kinds, reflect.String)
		case "integer":
			kinds = append(kinds, reflect.Int, reflect.Int32, reflect.Int64, reflect.Float64)
		case "number":
			kinds = append(kinds, reflect.Float64)
		case "boolean":
			kinds = append(kinds, reflect.Bool)
		case "array":
			kinds = append(kinds, reflect.Slice, reflect.Array)
		case "object":
			kinds = append(kinds, reflect.Struct, reflect.Map)
		}
	}
	if len(kinds) > 0 && !slices.Contains(kinds, typ.Kind()) {
		return "is a JSON " + strings.Join(types, " or ") + ", and is read as " + typ.String()
	}
	return ""
}

// TestModelsReadEveryField: every field a schema defines is read by its
// model or listed in leftOut.
func TestModelsReadEveryField(t *testing.T) {
	d := loadDocument(t)
	for name, c := range models(t, d) {
		var names []string
		for _, f := range fieldsOf(c.model) {
			names = append(names, f.name)
		}
		for prop := range c.schema.Properties {
			if !slices.Contains(names, prop) && leftOut[name+"."+prop] == "" {
				t.Errorf("%s does not read %s; model it or add it to leftOut with a reason", name, prop)
			}
		}
	}
}

// TestFieldsLeftOutAreStillInTheDocument fails once the document no
// longer defines a field of leftOut, which is then taken off it.
func TestFieldsLeftOutAreStillInTheDocument(t *testing.T) {
	d := loadDocument(t)
	all := models(t, d)
	for key := range leftOut {
		name, prop, _ := strings.Cut(key, ".")
		c, ok := all[name]
		if !ok || c.schema.Properties[prop] == nil {
			t.Errorf("leftOut names %s, which the document no longer has: take it off", key)
			continue
		}
		for _, f := range fieldsOf(c.model) {
			if f.name == prop {
				t.Errorf("leftOut names %s, which %s now reads: take it off", key, name)
			}
		}
	}
}

// TestFieldsAheadOfTheDocumentAreStillAhead fails once the document
// describes a field of aheadOfTheDocument, which is then taken off it.
func TestFieldsAheadOfTheDocumentAreStillAhead(t *testing.T) {
	d := loadDocument(t)
	all := models(t, d)
	for key, what := range aheadOfTheDocument {
		name, prop, _ := strings.Cut(key, ".")
		c, ok := all[name]
		if !ok {
			t.Errorf("%s: models has no %s", key, name)
			continue
		}
		if !slices.ContainsFunc(fieldsOf(c.model), func(f field) bool { return f.name == prop }) {
			t.Errorf("%s: %s does not read it", key, name)
		}
		if c.schema.Properties[prop] != nil {
			t.Errorf("the document now describes %s (%s): hold the model to it and take it off aheadOfTheDocument", key, what)
		}
	}
}

// documentEnums is the values the document lists for each named type
// whose constants the package declares, by the type's name, each read
// where a route answers or takes it.
func documentEnums(t *testing.T, d *document) map[string][]string {
	return map[string][]string{
		// The document's verdicts. A suggestion, a Prediction's or an
		// Explanation's Verdict, is one of them but VerdictFlag.
		"Verdict":          d.at(t, "Case.decision.verdict").Enum,
		"AskKind":          d.at(t, "Case.prediction.ask_kind").Enum,
		"Undo":             d.at(t, "Case.prediction.scope.undo").Enum,
		"TargetClass":      d.at(t, "Case.prediction.scope.target_class").Enum,
		"Contact":          d.at(t, "Case.prediction.scope.contact").Enum,
		"CalibrationState": d.at(t, "Calibration.bins.[].state").Enum,
		"WeekState":        d.at(t, "Report.weeks.[].state").Enum,
	}
}

// listedAsExamples is the named types whose values the document gives as
// examples and not as an enum, each with where and why.
// TestEnumsAreTheDocuments holds their constants to the examples, and
// TestEnumsListedAsExamplesAreStillExamples fails once the document gives
// one an enum, so it then moves to documentEnums.
var listedAsExamples = map[string]struct{ at, why string }{
	"Surface": {"Answer.surface", "a surface outside the document's list is no surface and not an error, so the document gives the list as examples"},
}

// TestEnumsAreTheDocuments: every value a named type's constants declare
// is one the document lists for its field, every value the document lists
// is declared, the document's unknown as Unknown, and every named type is
// held to the document.
func TestEnumsAreTheDocuments(t *testing.T) {
	d := loadDocument(t)
	listed := documentEnums(t, d)
	declared := constantsOf(t, ".")
	for typ, values := range declared {
		doc, ok := listed[typ]
		if !ok {
			e, ok := listedAsExamples[typ]
			if !ok {
				t.Errorf("%s has constants the document is not checked for; add it to documentEnums or listedAsExamples", typ)
				continue
			}
			doc = d.at(t, e.at).Examples
		}
		if len(doc) == 0 {
			t.Errorf("the document lists no values for %s", typ)
			continue
		}
		for _, v := range values {
			if !slices.Contains(doc, v) && enumsAheadOfTheDocument[typ+"."+v] == "" {
				t.Errorf("%s declares %q, which the document does not list", typ, v)
			}
		}
		for _, v := range doc {
			if !slices.Contains(values, v) && v != Unknown {
				t.Errorf("the document lists %q for %s, which no constant declares", v, typ)
			}
		}
	}
	for typ := range listed {
		if _, ok := declared[typ]; !ok {
			t.Errorf("documentEnums names %s, which declares no constants", typ)
		}
	}
}

// TestEnumsListedAsExamplesAreStillExamples fails once the document gives
// a type of listedAsExamples an enum, which is then held to it.
func TestEnumsListedAsExamplesAreStillExamples(t *testing.T) {
	d := loadDocument(t)
	for typ, e := range listedAsExamples {
		if s := d.at(t, e.at); len(s.Enum) > 0 {
			t.Errorf("the document now gives %s an enum at %s: move it to documentEnums and take it off listedAsExamples (%s)", typ, e.at, e.why)
		}
	}
}

// TestEnumsAheadOfTheDocumentAreStillAhead fails once the document lists a
// value of enumsAheadOfTheDocument, which is then taken off it.
func TestEnumsAheadOfTheDocumentAreStillAhead(t *testing.T) {
	d := loadDocument(t)
	listed := documentEnums(t, d)
	declared := constantsOf(t, ".")
	for key, what := range enumsAheadOfTheDocument {
		typ, value, _ := strings.Cut(key, ".")
		if !slices.Contains(declared[typ], value) {
			t.Errorf("%s: %s declares no such value", key, typ)
		}
		if slices.Contains(listed[typ], value) {
			t.Errorf("the document now lists %s (%s): take it off enumsAheadOfTheDocument", key, what)
		}
	}
}

// constantsOf is the string constants of each named type the package in
// dir declares.
func constantsOf(t *testing.T, dir string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, f := range sourceFiles(t, dir) {
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
						out[ident.Name] = append(out[ident.Name], strings.Trim(lit.Value, `"`))
					}
				}
			}
		}
	}
	return out
}

// sourceFiles is the parsed files of the package in dir, without its
// tests.
func sourceFiles(t *testing.T, dir string) []*ast.File {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	var out []*ast.File
	fset := token.NewFileSet()
	for _, path := range matches {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, f)
	}
	return out
}

// TestCodesAreTheDocuments: each code that has a sentinel is one the
// document names, as a response of its own or in a response's
// x-limen-codes.
func TestCodesAreTheDocuments(t *testing.T) {
	d := loadDocument(t)
	named := map[string]bool{}
	for name, r := range d.Components.Responses {
		if len(r.Codes) == 0 {
			named[name] = true
		}
		for _, c := range r.Codes {
			named[c] = true
		}
	}
	for code := range codes {
		if !named[code] {
			t.Errorf("the code %q has a sentinel, and the document does not name it", code)
		}
	}
}

// TestTheBoundsAreTheDocuments: the package's fixed numbers are the
// document's.
func TestTheBoundsAreTheDocuments(t *testing.T) {
	d := loadDocument(t)
	op, ok := d.operation(t, http.MethodGet, documentPath(BasePath+"/cases"))
	if !ok {
		t.Fatal("the document has no GET /v1/cases")
	}
	i := slices.IndexFunc(op.Parameters, func(p parameter) bool { return p.Name == "limit" })
	if i < 0 || op.Parameters[i].Schema.Maximum == nil {
		t.Fatal("the document's limit of the cases names no maximum")
	}
	limit := op.Parameters[i].Schema
	def, ok := limit.Default.(float64)
	if !ok {
		t.Fatalf("the document's limit of the cases has the default %v, not a number", limit.Default)
	}
	for _, b := range []struct {
		name      string
		sdk       int
		doc       *float64
		docBounds string
	}{
		{"DefaultCasesLimit", DefaultCasesLimit, &def, "the default of the cases' limit"},
		{"MaxCasesLimit", MaxCasesLimit, limit.Maximum, "the maximum of the cases' limit"},
		{"MaxAnswerMS", MaxAnswerMS, d.at(t, "Answer.answer_ms").Maximum, "the maximum of an answer's answer_ms"},
		{"ReportMinMembers", ReportMinMembers, d.at(t, "ReportCell.subjects").Minimum, "the minimum of a report cell's subjects"},
	} {
		if b.doc == nil || int(*b.doc) != b.sdk {
			t.Errorf("%s is %d, and %s is %v", b.name, b.sdk, b.docBounds, b.doc)
		}
	}
	bins := d.at(t, "Calibration.bins")
	if bins.MinItems == nil || bins.MaxItems == nil || *bins.MinItems != CalibrationBinCount || *bins.MaxItems != CalibrationBinCount {
		t.Errorf("CalibrationBinCount is %d, and the document's bins hold %v to %v", CalibrationBinCount, bins.MinItems, bins.MaxItems)
	}
}

// routes are the routes the client calls, as it sends them.
var routes = []struct{ method, path string }{
	{http.MethodGet, BasePath + "/learned"},
	{http.MethodGet, BasePath + "/explanations"},
	{http.MethodGet, BasePath + "/profiles"},
	{http.MethodPut, BasePath + "/profiles"},
	{http.MethodDelete, BasePath + "/profiles"},
	{http.MethodPost, BasePath + "/previews"},
	{http.MethodPost, BasePath + "/answers"},
	{http.MethodPost, BasePath + "/subjects/forget"},
	{http.MethodGet, BasePath + "/cases"},
	{http.MethodGet, BasePath + "/reliability"},
	{http.MethodGet, BasePath + "/calibration"},
	{http.MethodGet, BasePath + "/reports"},
}

// TestEndpointsAreInTheDocument: every route of routes is in the
// document, and every path the package's source sends a request to is in
// routes.
func TestEndpointsAreInTheDocument(t *testing.T) {
	d := loadDocument(t)
	for _, r := range routes {
		if _, ok := d.operation(t, r.method, documentPath(r.path)); !ok {
			t.Errorf("%s %s, %s in the document, is not in the document", r.method, r.path, documentPath(r.path))
		}
	}
	for _, f := range sourceFiles(t, ".") {
		ast.Inspect(f, func(n ast.Node) bool {
			b, ok := n.(*ast.BinaryExpr)
			if !ok || b.Op != token.ADD {
				return true
			}
			if x, ok := b.X.(*ast.Ident); !ok || x.Name != "BasePath" {
				return true
			}
			lit, ok := b.Y.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			path := BasePath + strings.Trim(lit.Value, `"`)
			if !slices.ContainsFunc(routes, func(r struct{ method, path string }) bool { return r.path == path }) {
				t.Errorf("the package sends a request to %s, which routes does not list", path)
			}
			return true
		})
	}
}

// recorder is a server that records each request the client sends and
// answers it with a body the client decodes.
type recorder struct {
	t    *testing.T
	sent []sent
}

type sent struct {
	method string
	url    *url.URL
	body   any
}

func (rec *recorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		rec.t.Error(err)
	}
	s := sent{method: r.Method, url: r.URL}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &s.body); err != nil {
			rec.t.Errorf("%s %s sent a body that is not JSON: %s", r.Method, r.URL.Path, raw)
		}
	}
	rec.sent = append(rec.sent, s)
	w.Header().Set("Content-Type", "application/json")
	if strings.HasSuffix(r.URL.Path, "/cases") {
		_, _ = io.WriteString(w, `{"entries":[]}`)
		return
	}
	_, _ = io.WriteString(w, `{}`)
}

func newRecorder(t *testing.T) (*recorder, *Client) {
	t.Helper()
	rec := &recorder{t: t}
	srv := httptest.NewServer(rec)
	t.Cleanup(srv.Close)
	cfg, err := latere.NewConfig(latere.WithBaseURL(srv.URL), latere.WithTokenSource(latere.StaticToken("key_test")), latere.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	return rec, New(cfg)
}

// TestRequestsFitTheirSchemas sends each request with a body the client
// makes, the fullest and the least, and checks the body against the
// schema of its route: every member is a property, and every required
// property is there.
func TestRequestsFitTheirSchemas(t *testing.T) {
	d := loadDocument(t)
	rec, c := newRecorder(t)
	ctx := context.Background()
	const person, org = "https://auth.example|usr_42", "org_1"
	step := ActionID("ses_1", "toolu_1")
	from, to := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	ms, waited, yes, no := int64(4200), int64(60_000), true, false
	costs := Costs{FalseAllow: 10, FalseBlock: 4, Ask: 1}
	for _, r := range []struct {
		name string
		send func() error
	}{
		{"PutProfile, an agent's", func() error {
			_, err := c.PutProfile(ctx, &Profile{Org: org, Kind: "tool", Agent: "agt_1", Costs: costs, AuditRate: 0.05,
				Certify: &Certify{Alpha: 0.05, Delta: 0.1}, Consent: &Consent{MinWeight: 5, MinSessions: 3}})
			return err
		}},
		{"PutProfile, a person's own", func() error {
			_, err := c.PutProfile(ctx, &Profile{Org: org, Subject: person, Costs: costs, AuditRate: 0.1})
			return err
		}},
		{"PutProfile, least", func() error { _, err := c.PutProfile(ctx, &Profile{Org: person}); return err }},
		{"Preview", func() error {
			_, err := c.Preview(ctx, &PreviewParams{Org: person, Subject: person, From: from, To: to, Costs: costs, AuditRate: 0.05})
			return err
		}},
		{"Preview, least", func() error { _, err := c.Preview(ctx, &PreviewParams{Org: person, Subject: person}); return err }},
		{"Answer", func() error {
			return c.Answer(ctx, &AnswerParams{Org: person, Subject: person, ActionID: step, Approve: true, By: person,
				AnswerMS: &ms, WaitedMS: &waited, DetailsOpened: &yes, SuggestionShown: &no, Surface: SurfacePrompt, Remembered: true})
		}},
		{"Answer, least", func() error { return c.Answer(ctx, &AnswerParams{Org: person, Subject: person, ActionID: step}) }},
		{"Forget", func() error { return c.Forget(ctx, &ForgetParams{Org: person, Subject: person}) }},
	} {
		rec.sent = nil
		if err := r.send(); err != nil {
			t.Errorf("%s: %v", r.name, err)
			continue
		}
		if len(rec.sent) != 1 {
			t.Errorf("%s sent %d requests", r.name, len(rec.sent))
			continue
		}
		s := rec.sent[0]
		op, ok := d.operation(t, s.method, documentPath(s.url.Path))
		if !ok {
			t.Errorf("%s: %s %s is not in the document", r.name, s.method, documentPath(s.url.Path))
			continue
		}
		body := op.RequestBody.Content["application/json"].Schema
		if body == nil {
			t.Errorf("%s: the document gives %s %s no JSON body", r.name, s.method, documentPath(s.url.Path))
			continue
		}
		d.checkBody(t, r.name, s.body, body)
	}
}

// checkBody checks a JSON value a request carries against s: every
// member is a property, and every required property is there.
func (d *document) checkBody(t *testing.T, name string, v any, s *schema) {
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
			d.checkBody(t, name+"."+k, member, prop)
		}
		for _, r := range s.Required {
			if _, ok := v[r]; !ok {
				t.Errorf("%s does not send %s, which the document requires", name, r)
			}
		}
	case []any:
		if s.Items != nil {
			for _, item := range v {
				d.checkBody(t, name+"[]", item, s.Items)
			}
		}
	}
}

// TestQueriesAreTheDocuments sends each request with a query, with every
// parameter set, and checks the query against the parameters of its
// route: each one sent is one the document names, and each one the
// document requires is sent.
func TestQueriesAreTheDocuments(t *testing.T) {
	d := loadDocument(t)
	rec, c := newRecorder(t)
	ctx := context.Background()
	const person, org = "https://auth.example|usr_42", "org_1"
	step := ActionID("ses_1", "toolu_1")
	from, to := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	calls := []func() error{
		func() error {
			_, err := c.GetLearned(ctx, &LearnedParams{Org: org, Subject: person, Agent: "agt_1", Kind: "tool"})
			return err
		},
		func() error {
			_, err := c.GetExplanation(ctx, &ExplanationParams{Org: org, Subject: person, ActionID: step})
			return err
		},
		func() error { _, err := c.GetProfile(ctx, Scope{Org: org, Kind: "tool", Agent: "agt_1"}); return err },
		func() error { _, err := c.GetProfile(ctx, Scope{Org: org, Subject: person}); return err },
		func() error { return c.DeleteProfile(ctx, Scope{Org: org, Kind: "tool", Agent: "agt_1"}) },
		func() error { return c.DeleteProfile(ctx, Scope{Org: org, Subject: person}) },
		func() error {
			_, err := c.ListCasesPage(ctx, &CasesParams{Org: org, Subject: person, ActionID: step, Pending: true, Limit: 10, Cursor: "c"})
			return err
		},
		func() error {
			_, err := c.GetReliability(ctx, &ReliabilityParams{Org: org, Subject: person, Of: OfLearned, From: from, To: to})
			return err
		},
		func() error {
			_, err := c.GetCalibration(ctx, &CalibrationParams{Org: org, Subject: person, From: from, To: to})
			return err
		},
		func() error { _, err := c.GetReport(ctx, &ReportParams{Org: org, From: from, To: to}); return err },
	}
	for _, call := range calls {
		if err := call(); err != nil {
			t.Fatal(err)
		}
	}
	if len(rec.sent) != len(calls) {
		t.Fatalf("sent %d requests for %d calls", len(rec.sent), len(calls))
	}
	for _, s := range rec.sent {
		path := documentPath(s.url.Path)
		op, ok := d.operation(t, s.method, path)
		if !ok {
			t.Errorf("%s %s, sent as %s, is not in the document", s.method, path, s.url.Path)
			continue
		}
		var names []string
		for _, p := range op.Parameters {
			if p.In != "query" {
				continue
			}
			names = append(names, p.Name)
			if p.Required && !s.url.Query().Has(p.Name) {
				t.Errorf("%s %s does not send %s, which the document requires", s.method, path, p.Name)
			}
		}
		for key := range s.url.Query() {
			if !slices.Contains(names, key) {
				t.Errorf("%s %s sends %s, which the document does not name; it names %v", s.method, path, key, names)
			}
		}
	}
}
