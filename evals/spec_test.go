// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package evals_test

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"latere.ai/x/sdk-go/evals"
	"latere.ai/x/sdk-go/latere"
)

// The models of this package are written by hand from the Evals API's
// OpenAPI document, vendored in openapi/evals.json by `make openapi`.
// These tests hold the two together: every field a model reads is in its
// schema and every field the schema defines is read or left out with a
// reason, every date-time is a time.Time, every route the client calls and
// every query parameter it sends is in the document, and every value of a
// named type's constants is one the document lists.
//
// The document serves its paths under the base path /v1, which the API
// serves Evals under as /v1/evals.

type schema struct {
	Format     string             `json:"format"`
	Properties map[string]*schema `json:"properties"`
	Required   []string           `json:"required"`
	Items      *schema            `json:"items"`
	Ref        string             `json:"$ref"`
	Enum       []string           `json:"enum"`
}

type parameter struct {
	Ref    string `json:"$ref"`
	In     string `json:"in"`
	Name   string `json:"name"`
	Schema struct {
		Default *int `json:"default"`
		Maximum *int `json:"maximum"`
	} `json:"schema"`
}

// The page sizes are the document's.
func TestThePageSizesAreTheDocuments(t *testing.T) {
	p, ok := loadDocument(t).Components.Parameters["Limit"]
	if !ok || p.Schema.Default == nil || p.Schema.Maximum == nil {
		t.Fatal("the document's Limit parameter names no default and no maximum")
	}
	if *p.Schema.Default != evals.DefaultLimit || *p.Schema.Maximum != evals.MaxLimit {
		t.Errorf("DefaultLimit %d and MaxLimit %d; the document's are %d and %d", evals.DefaultLimit, evals.MaxLimit, *p.Schema.Default, *p.Schema.Maximum)
	}
}

type operation struct {
	Parameters []parameter `json:"parameters"`
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
	raw, err := os.ReadFile("../openapi/evals.json")
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

// union is a schema of the properties and the requirements of several,
// for a model that reads a version's record beside its kind's fields.
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
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			out = append(out, fieldsOf(f.Type)...)
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name != "" && name != "-" {
			out = append(out, field{name, f.Type})
		}
	}
	return out
}

var timeType = reflect.TypeFor[time.Time]()

// models are the package's models, each with the schema it is held to.
func models(t *testing.T, d *document) map[string]struct {
	model  any
	schema *schema
} {
	version := d.at(t, "Version")
	return map[string]struct {
		model  any
		schema *schema
	}{
		"Version":            {evals.Version{}, version},
		"Metric":             {evals.Metric{}, union(version, d.at(t, "Metric"))},
		"Grader":             {evals.Grader{}, union(version, d.at(t, "Grader"))},
		"Check":              {evals.Check{}, d.at(t, "Grader.check")},
		"StorageFile":        {evals.StorageFile{}, d.at(t, "FileSource.arca")},
		"Check.files":        {evals.StorageFile{}, d.at(t, "Grader.check.files")},
		"ModelJudge":         {evals.ModelJudge{}, d.at(t, "Grader.model_judge")},
		"AgentJudge":         {evals.AgentJudge{}, d.at(t, "Grader.agent_judge")},
		"Annotation":         {evals.Annotation{}, d.at(t, "Grader.annotation")},
		"JudgeInputs":        {evals.JudgeInputs{}, d.at(t, "JudgeInputs")},
		"Task":               {evals.Task{}, union(version, d.at(t, "Task"))},
		"FileSource":         {evals.FileSource{}, d.at(t, "FileSource")},
		"RepositoryFile":     {evals.RepositoryFile{}, d.at(t, "FileSource.origo")},
		"Workspace":          {evals.Workspace{}, d.at(t, "Task.workspace")},
		"Environment":        {evals.Environment{}, d.at(t, "Task.environment")},
		"Suite":              {evals.Suite{}, union(version, d.at(t, "Suite"))},
		"Candidate":          {evals.Candidate{}, union(version, d.at(t, "Candidate"))},
		"AgentCandidate":     {evals.AgentCandidate{}, d.at(t, "Candidate.topos")},
		"ProgramCandidate":   {evals.ProgramCandidate{}, d.at(t, "Candidate.program")},
		"ModelAccess":        {evals.ModelAccess{}, d.at(t, "Candidate.program.lux")},
		"ProgramCredential":  {evals.ProgramCredential{}, d.at(t, "Candidate.program.credentials.[]")},
		"ExternalCandidate":  {evals.ExternalCandidate{}, d.at(t, "Candidate.external")},
		"Experiment":         {evals.Experiment{}, d.at(t, "Experiment")},
		"Reference":          {evals.Reference{}, d.at(t, "Experiment.reference")},
		"Budget":             {evals.Budget{}, d.at(t, "Experiment.budget")},
		"Progress":           {evals.Progress{}, d.at(t, "Experiment.progress")},
		"Usage":              {evals.Usage{}, d.at(t, "Experiment.usage")},
		"UsageCount":         {evals.UsageCount{}, d.at(t, "Experiment.usage.unattributed")},
		"DeclaredComparison": {evals.DeclaredComparison{}, d.at(t, "Experiment.comparisons.[]")},
		"Independence":       {evals.Independence{}, d.at(t, "Experiment.independence")},
		"Cell":               {evals.Cell{}, d.at(t, "Cell")},
		"Comparison":         {evals.Comparison{}, d.at(t, "Comparison")},
		"Declaration":        {evals.Declaration{}, d.at(t, "Comparison.declaration")},
	}
}

func TestModelsMatchTheSchemas(t *testing.T) {
	d := loadDocument(t)
	for name, c := range models(t, d) {
		fields := fieldsOf(reflect.TypeOf(c.model))
		var names []string
		for _, f := range fields {
			names = append(names, f.name)
			prop, ok := c.schema.Properties[f.name]
			if !ok {
				t.Errorf("%s.%s is not in the schema", name, f.name)
				continue
			}
			if prop.Format == "date-time" && f.typ != timeType {
				t.Errorf("%s.%s is a date-time, read as %v", name, f.name, f.typ)
			}
		}
		for _, r := range c.schema.Required {
			if !slices.Contains(names, r) {
				t.Errorf("%s does not read %s, which the schema requires", name, r)
			}
		}
	}
}

// Fields a schema defines that a model leaves out on purpose, each with
// its reason. A field missing from a model and from this list fails
// TestModelsReadEveryField, so a field the API adds is decided on.
var leftOut = map[string]string{
	"Experiment.judgments":       "the records of the gradings that belong to no trial, whose shape is each judge's; read them from RawJSON",
	"Cell.rollouts":              "how the platform handed the cell's trials to where they run, for its operators; read them from RawJSON",
	"Cell.tuple":                 "the cell's resolved setup, whose fields its candidate's kind decides; read it from RawJSON",
	"Comparison.results":         "a figure of the analysis, whose shape analysis_version names; read it from RawJSON",
	"Comparison.candidate_suite": "a figure of the analysis, whose shape analysis_version names; read it from RawJSON",
	"Comparison.correction":      "a figure of the analysis, whose shape analysis_version names; read it from RawJSON",
	"Comparison.parameters":      "a figure of the analysis, whose shape analysis_version names; read it from RawJSON",
	"Comparison.unpaired_tasks":  "a figure of the analysis, whose shape analysis_version names; read it from RawJSON",
	"Comparison.windows":         "a figure of the analysis, whose shape analysis_version names; read it from RawJSON",
}

func TestModelsReadEveryField(t *testing.T) {
	d := loadDocument(t)
	for name, c := range models(t, d) {
		var names []string
		for _, f := range fieldsOf(reflect.TypeOf(c.model)) {
			names = append(names, f.name)
		}
		for prop := range c.schema.Properties {
			if !slices.Contains(names, prop) && leftOut[name+"."+prop] == "" {
				t.Errorf("%s does not read %s; model it or add it to leftOut with a reason", name, prop)
			}
		}
	}
	for key := range leftOut {
		name, prop, _ := strings.Cut(key, ".")
		if c, ok := models(t, d)[name]; !ok || c.schema.Properties[prop] == nil {
			t.Errorf("leftOut names %s, which the document no longer has", key)
		}
	}
}

// routes are the routes the client calls, by their place in the document.
var routes = []struct{ method, path string }{
	{"get", "/metrics"}, {"get", "/metrics/{ref}"}, {"get", "/metrics/{name}/versions"},
	{"get", "/graders"}, {"get", "/graders/{ref}"}, {"get", "/graders/{name}/versions"},
	{"get", "/tasks"}, {"get", "/tasks/{ref}"}, {"get", "/tasks/{name}/versions"},
	{"get", "/suites"}, {"get", "/suites/{ref}"}, {"get", "/suites/{name}/versions"},
	{"get", "/candidates"}, {"get", "/candidates/{ref}"}, {"get", "/candidates/{name}/versions"},
	{"get", "/experiments"}, {"get", "/experiments/{id}"},
	{"get", "/experiments/{id}/cells"}, {"get", "/experiments/{id}/comparisons"},
	{"get", "/experiments/{id}/stream"},
}

func TestEndpointsAreInTheDocument(t *testing.T) {
	d := loadDocument(t)
	for _, r := range routes {
		if _, ok := d.Paths[r.path][r.method]; !ok {
			t.Errorf("%s %s is not in the document", strings.ToUpper(r.method), r.path)
		}
	}
}

// route is the document's path a request was sent to: the path without
// the base path, each segment after a collection matched to its template.
func route(path string) string {
	segs := strings.Split(strings.TrimPrefix(path, "/v1/evals"), "/")
	switch {
	case len(segs) == 3 && segs[1] == "experiments":
		segs[2] = "{id}"
	case len(segs) == 4 && segs[1] == "experiments":
		segs[2] = "{id}"
	case len(segs) == 3:
		segs[2] = "{ref}"
	case len(segs) == 4 && segs[3] == "versions":
		segs[2] = "{name}"
	}
	return strings.Join(segs, "/")
}

// TestQueriesAreTheDocuments: each query parameter a list sends, with
// every filter set, is one the document names for its route.
func TestQueriesAreTheDocuments(t *testing.T) {
	d := loadDocument(t)
	var sent []*url.URL
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent = append(sent, r.URL)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer srv.Close()
	cfg, err := latere.NewConfig(latere.WithBaseURL(srv.URL), latere.WithTokenSource(latere.StaticToken("tok")))
	if err != nil {
		t.Fatal(err)
	}
	c := evals.New(cfg)
	ctx := t.Context()
	yes := true
	versions := &evals.ListVersionsParams{Limit: 1, Cursor: "c"}
	for _, call := range []func() error{
		func() error {
			_, err := c.ListMetricsPage(ctx, &evals.ListMetricsParams{Name: "n", Type: evals.MetricCost, AllVersions: true, Limit: 1, Cursor: "c"})
			return err
		},
		func() error {
			_, err := c.ListGradersPage(ctx, &evals.ListGradersParams{Name: "n", Kind: evals.GraderCheck, Produces: "p", AllVersions: true, Limit: 1, Cursor: "c"})
			return err
		},
		func() error {
			_, err := c.ListTasksPage(ctx, &evals.ListTasksParams{Name: "n", Suite: "s", MetadataKey: "k", MetadataValue: "v", AllVersions: true, Limit: 1, Cursor: "c"})
			return err
		},
		func() error {
			_, err := c.ListSuitesPage(ctx, &evals.ListSuitesParams{Name: "n", AllVersions: true, Limit: 1, Cursor: "c"})
			return err
		},
		func() error {
			_, err := c.ListCandidatesPage(ctx, &evals.ListCandidatesParams{Name: "n", Kind: evals.CandidateProgram, AgentVersion: "a", Model: "m", MetadataKey: "k", MetadataValue: "v", AllVersions: true, Limit: 1, Cursor: "c"})
			return err
		},
		func() error { _, err := c.ListMetricVersionsPage(ctx, "n", versions); return err },
		func() error { _, err := c.ListGraderVersionsPage(ctx, "n", versions); return err },
		func() error { _, err := c.ListTaskVersionsPage(ctx, "n", versions); return err },
		func() error { _, err := c.ListSuiteVersionsPage(ctx, "n", versions); return err },
		func() error { _, err := c.ListCandidateVersionsPage(ctx, "n", versions); return err },
		func() error {
			_, err := c.ListExperimentsPage(ctx, &evals.ListExperimentsParams{Suite: "s", Candidate: "c", State: evals.ExperimentPending, Mode: evals.ModeExecute, Baseline: "b", MetadataKey: "k", MetadataValue: "v", Limit: 1, Cursor: "c"})
			return err
		},
		func() error {
			_, err := c.ListCellsPage(ctx, "exp_1", &evals.ListCellsParams{State: evals.CellPending, Task: "t", Candidate: "c", Limit: 1, Cursor: "c"})
			return err
		},
		func() error {
			_, err := c.ListExperimentComparisonsPage(ctx, "exp_1", &evals.ListExperimentComparisonsParams{Candidate: "c", ReferenceCandidate: "r", Baseline: "b",
				Automatic: &yes, Declared: &yes, Exploratory: &yes, Final: &yes, Limit: 1, Cursor: "c"})
			return err
		},
	} {
		if err := call(); err != nil {
			t.Fatal(err)
		}
	}
	for _, u := range sent {
		path := route(u.EscapedPath())
		raw, ok := d.Paths[path]["get"]
		if !ok {
			t.Errorf("GET %s, sent as %s, is not in the document", path, u.EscapedPath())
			continue
		}
		var op operation
		if err := json.Unmarshal(raw, &op); err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, p := range op.Parameters {
			if p.Ref != "" {
				p = d.Components.Parameters[strings.TrimPrefix(p.Ref, "#/components/parameters/")]
			}
			if p.In == "query" {
				names = append(names, p.Name)
			}
		}
		for key := range u.Query() {
			if strings.HasPrefix(key, "metadata.") {
				key = "metadata.<key>"
			}
			if !slices.Contains(names, key) {
				t.Errorf("GET %s sends %s, which the document does not name; it names %v", path, key, names)
			}
		}
	}
	if len(sent) != 13 {
		t.Errorf("sent %d requests", len(sent))
	}
}

// enums are the named types whose values the document lists, each with
// the schema that lists them.
func enums(t *testing.T, d *document) map[string][]string {
	return map[string][]string{
		"MetricType":      d.at(t, "Metric.type").Enum,
		"Direction":       d.at(t, "Metric.direction").Enum,
		"Level":           d.at(t, "Metric.level").Enum,
		"Vocabulary":      d.at(t, "Metric.vocabulary").Enum,
		"GraderKind":      d.at(t, "Grader.kind").Enum,
		"JudgeRole":       d.at(t, "Grader.model_judge.role").Enum,
		"WorkspaceKind":   d.at(t, "Task.workspace.kind").Enum,
		"CandidateKind":   d.at(t, "Candidate.kind").Enum,
		"ExperimentState": d.at(t, "Experiment.state").Enum,
		"Mode":            d.at(t, "ExperimentCreate.mode").Enum,
		"Scheduling":      d.at(t, "Experiment.scheduling").Enum,
		"ComparisonState": d.at(t, "Comparison.state").Enum,
		"TrialOutcome":    d.at(t, "Submit.outcome").Enum,
		"Verdict":         d.at(t, "Result.verdict").Enum,
		"Aggregate":       d.at(t, "Summary.aggregate").Enum,
	}
}

// unlisted are the named types whose values the document does not list,
// each with its reason, and the values of listed types it does not name.
var unlisted = map[string]string{
	"CellState":             "the document gives a cell's state no enum; the values are the states the API answers and its parked states",
	"TrialState":            "the document gives a trial's state no enum; the values are the states the API counts in an experiment's progress",
	"EventType":             "the document leaves a stream's event types and their data open; the values are the events the stream sends",
	"TrialOutcome.canceled": "the outcome a trial the platform stopped with its experiment ends with, which a harness's own submission cannot report, so its enum leaves it out",
}

// TestEnumsAreTheDocuments: every value a named type's constants declare
// is one the document lists for its field, and every named type is held
// to the document or listed in unlisted with its reason.
func TestEnumsAreTheDocuments(t *testing.T) {
	d := loadDocument(t)
	listed := enums(t, d)
	for typ, values := range constantsOf(t, ".") {
		doc, ok := listed[typ]
		if !ok {
			if unlisted[typ] == "" {
				t.Errorf("%s has constants the document is not checked for; add it to enums or to unlisted with a reason", typ)
			}
			continue
		}
		if len(doc) == 0 {
			t.Errorf("the document lists no values for %s", typ)
		}
		for _, v := range values {
			if !slices.Contains(doc, v) && unlisted[typ+"."+v] == "" {
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
			g, ok := decl.(*ast.GenDecl)
			if !ok || g.Tok != token.CONST {
				continue
			}
			for _, spec := range g.Specs {
				v := spec.(*ast.ValueSpec)
				ident, ok := v.Type.(*ast.Ident)
				if !ok || len(v.Values) != 1 {
					continue
				}
				lit, ok := v.Values[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				out[ident.Name] = append(out[ident.Name], strings.Trim(lit.Value, `"`))
			}
		}
	}
	return out
}
