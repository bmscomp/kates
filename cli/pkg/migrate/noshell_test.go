package migrate

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestNoShellPrograms is the gate the migration plan asks for: nothing in
// this package composes a shell program — no string spells `sh -c` or
// `bash -c`, and no argv puts "-c" after a shell name. This package runs no
// process at all, so the allowance is zero everywhere.
func TestNoShellPrograms(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		if n := countShellLiterals(file); n > 0 {
			t.Errorf("%s composes a shell program (%d shell literals)", f, n)
		}
		for _, imp := range file.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == "os/exec" {
				t.Errorf("%s imports os/exec; this package returns data and runs nothing", f)
			}
		}
	}
}

func countShellLiterals(file *ast.File) int {
	shells := map[string]bool{"sh": true, "/bin/sh": true, "bash": true, "/bin/bash": true}
	count := 0
	countSeq := func(exprs []ast.Expr) {
		prev := ""
		for _, e := range exprs {
			s, ok := stringLit(e)
			if ok && s == "-c" && shells[prev] {
				count++
			}
			prev = s
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.BasicLit:
			if s, ok := stringLit(x); ok && (strings.Contains(s, "sh -c") || strings.Contains(s, "bash -c")) {
				count++
			}
		case *ast.CompositeLit:
			countSeq(x.Elts)
		case *ast.CallExpr:
			countSeq(x.Args)
		}
		return true
	})
	return count
}

func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return s, true
}
