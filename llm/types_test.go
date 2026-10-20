// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package llm_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"latere.ai/x/sdk-go/llm"
)

func TestTextHelpers(t *testing.T) {
	if m := llm.UserText("hi"); m.Role != llm.RoleUser || len(m.Blocks) != 1 || m.Blocks[0].Type != llm.BlockText || m.Blocks[0].Text != "hi" {
		t.Errorf("UserText = %+v", m)
	}
	if m := llm.AssistantText("ok"); m.Role != llm.RoleAssistant || m.Blocks[0].Text != "ok" {
		t.Errorf("AssistantText = %+v", m)
	}
}

// Every type and constant package wire exports is named in this package, as
// wire's own of the same name, so a program never needs to import wire and a
// value added there cannot be missed here.
func TestEveryWireNameIsNamedHere(t *testing.T) {
	wireTypes, wireConsts := exported(t, "wire", func(spec ast.Spec) (string, string) {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			return s.Name.Name, ""
		case *ast.ValueSpec:
			return "", s.Names[0].Name
		}
		return "", ""
	})
	types, consts := exported(t, ".", func(spec ast.Spec) (string, string) {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			if s.Assign.IsValid() && refersToWire(s.Type, s.Name.Name) {
				return s.Name.Name, ""
			}
		case *ast.ValueSpec:
			if len(s.Values) == 1 && refersToWire(s.Values[0], s.Names[0].Name) {
				return "", s.Names[0].Name
			}
		}
		return "", ""
	})
	if len(wireTypes) == 0 || len(wireConsts) == 0 {
		t.Fatalf("read %d types and %d constants from wire", len(wireTypes), len(wireConsts))
	}
	if !slices.Equal(types, wireTypes) {
		t.Errorf("aliases %v, want wire's types %v", types, wireTypes)
	}
	if !slices.Equal(consts, wireConsts) {
		t.Errorf("constants %v, want wire's %v", consts, wireConsts)
	}
}

// exported reads the exported type and constant names that pick finds in
// the non-test files of dir, sorted.
func exported(t *testing.T, dir string, pick func(ast.Spec) (string, string)) (types, consts []string) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	typeSet, constSet := map[string]bool{}, map[string]bool{}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || (gen.Tok != token.TYPE && gen.Tok != token.CONST) {
				continue
			}
			for _, spec := range gen.Specs {
				typ, c := pick(spec)
				if ast.IsExported(typ) {
					typeSet[typ] = true
				}
				if ast.IsExported(c) {
					constSet[c] = true
				}
			}
		}
	}
	return slices.Sorted(maps.Keys(typeSet)), slices.Sorted(maps.Keys(constSet))
}

// refersToWire reports whether expr is wire.<name>.
func refersToWire(expr ast.Expr, name string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "wire" && sel.Sel.Name == name
}
