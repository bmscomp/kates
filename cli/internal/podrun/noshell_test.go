package podrun

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestNoShellPrograms is the gate the migration plan asks for: this package
// never composes a shell program. The one `/bin/sh -c` it contains is the
// client pod's constant entrypoint in pod.go; no other place in the package
// may spell `sh -c` / `bash -c` inside a string, or put a "-c" literal right
// after a shell name in an argv. (kubectl's own `-c <container>` flag is not
// a shell and is not counted.) Comments are not code and are not counted.
func TestNoShellPrograms(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]int{"pod.go": 1}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		if count := countShellLiterals(file); count > allowed[f] {
			t.Errorf("%s composes a shell program (%d shell literals, %d allowed)", f, count, allowed[f])
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
