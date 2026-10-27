package cmd

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// exit0Marker, on the line of a `return nil` or the line above it, says the
// output.Error before it is a note and the command did what was asked.
const exit0Marker = "// exit0:"

// TestNoExitZeroAfterError walks every command's Run and RunE body in this
// package and fails on an output.Error(...) followed, as the next statement
// of its block, by `return nil`: the command told the user it failed and the
// shell that it did not (exitcodes.go). A legitimate case carries an exit0
// comment with its reason.
func TestNoExitZeroAfterError(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var parsed []*ast.File
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		parsed = append(parsed, af)
	}

	var hits []string
	for _, body := range runBodies(parsed) {
		file := fset.File(body.Pos())
		marked := exit0Lines(file, parsed)
		ast.Inspect(body, func(n ast.Node) bool {
			for _, stmts := range statementLists(n) {
				for i := 0; i+1 < len(stmts); i++ {
					if !isOutputError(stmts[i]) || !isReturnNil(stmts[i+1]) {
						continue
					}
					line := fset.Position(stmts[i+1].Pos()).Line
					if marked[line] || marked[line-1] {
						continue
					}
					hits = append(hits, fmt.Sprintf("%s:%d", file.Name(), line))
				}
			}
			return true
		})
	}
	sort.Strings(hits)
	perFile := map[string]int{}
	for _, h := range hits {
		perFile[h[:strings.Index(h, ":")]]++
	}
	for _, h := range hits {
		if file := h[:strings.Index(h, ":")]; perFile[file] <= exitZeroBacklog[file] {
			continue
		}
		t.Errorf("%s: output.Error then return nil exits 0 after reporting a failure; return cmdErr, couldNotTell or an error, or mark the return with %s <reason>", h, exit0Marker)
	}
	for file, n := range exitZeroBacklog {
		if perFile[file] < n {
			t.Logf("%s has %d hits left of %d: lower its exitZeroBacklog entry", file, perFile[file], n)
		}
	}
}

// exitZeroBacklog counts the hits still in files other wave-3 items are
// rewriting, so they are fixed there rather than in two places at once. A new
// hit in one of these files fails the test; a fix lowers the count here.
//
//	detect.go    runDetect: a values file that cannot be read or written
//	             exits 0 (four returns); couldNotTell is the fix.
//	security.go  security audit: an "error" field in the API's result exits 0.
var exitZeroBacklog = map[string]int{
	"detect.go":   4,
	"security.go": 1,
}

// runBodies returns the body of every Run and RunE of a cobra.Command in
// files: function literals in place, and the declaration a name refers to.
func runBodies(files []*ast.File) []*ast.BlockStmt {
	decls := map[string]*ast.FuncDecl{}
	for _, f := range files {
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Body != nil {
				decls[fd.Name.Name] = fd
			}
		}
	}
	var bodies []*ast.BlockStmt
	seen := map[*ast.BlockStmt]bool{}
	add := func(b *ast.BlockStmt) {
		if b != nil && !seen[b] {
			seen[b] = true
			bodies = append(bodies, b)
		}
	}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			kv, ok := n.(*ast.KeyValueExpr)
			if !ok {
				return true
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok || (key.Name != "Run" && key.Name != "RunE") {
				return true
			}
			switch v := kv.Value.(type) {
			case *ast.FuncLit:
				add(v.Body)
			case *ast.Ident:
				if fd := decls[v.Name]; fd != nil {
					add(fd.Body)
				}
			}
			return true
		})
	}
	return bodies
}

// statementLists returns the statement lists n holds directly: a block's, or
// a case's in a switch or select.
func statementLists(n ast.Node) [][]ast.Stmt {
	switch x := n.(type) {
	case *ast.BlockStmt:
		return [][]ast.Stmt{x.List}
	case *ast.CaseClause:
		return [][]ast.Stmt{x.Body}
	case *ast.CommClause:
		return [][]ast.Stmt{x.Body}
	}
	return nil
}

func isOutputError(s ast.Stmt) bool {
	es, ok := s.(*ast.ExprStmt)
	if !ok {
		return false
	}
	call, ok := es.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Error" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "output"
}

func isReturnNil(s ast.Stmt) bool {
	ret, ok := s.(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return false
	}
	id, ok := ret.Results[0].(*ast.Ident)
	return ok && id.Name == "nil"
}

// exit0Lines returns the lines of file that carry an exit0 marker.
func exit0Lines(file *token.File, files []*ast.File) map[int]bool {
	lines := map[int]bool{}
	for _, f := range files {
		for _, cg := range f.Comments {
			for _, c := range cg.List {
				if file.Base() <= int(c.Pos()) && int(c.Pos()) < file.Base()+file.Size() &&
					strings.Contains(c.Text, exit0Marker) {
					lines[file.Line(c.Pos())] = true
				}
			}
		}
	}
	return lines
}

// noJSONForm exits 2 with the message a script can grep for.
func TestNoJSONForm(t *testing.T) {
	err := noJSONForm(webhookListCmd)
	var exit *exitError
	if !errors.As(err, &exit) || exit.code != couldNotTellExitCode {
		t.Fatalf("err = %v (%T), want exit %d", err, err, couldNotTellExitCode)
	}
	if !strings.Contains(exit.msg, "this command has no JSON output") || !strings.Contains(exit.msg, "kates webhook list") {
		t.Errorf("msg = %q", exit.msg)
	}
}
