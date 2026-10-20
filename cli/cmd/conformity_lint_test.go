package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
)

const lintHeader = "apiVersion: conformity.kates.io/v1alpha1\nkind: KafkaReference\nmetadata: {name: t}\n"

func writeReference(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestConformityLintPrintsCompilerDiagnostics(t *testing.T) {
	bad := writeReference(t, "bad.yaml", lintHeader+"spec:\n  brokers:\n    cuont: 3\n")
	good := writeReference(t, "good.yaml", lintHeader+"spec:\n  brokers:\n    count: 3\n")
	buf := output.ResetForTesting()

	err := runConformityLint(nil, []string{good, bad})

	if exitCode(err) != 1 {
		t.Errorf("exit %d, want 1 for an error", exitCode(err))
	}
	out := buf.String()
	if !strings.Contains(out, bad+`:6:5: error: spec.brokers.cuont: unknown field (did you mean "count"?) [unknown-field]`+
		"\n  6 |     cuont: 3\n    |     ^\n") {
		t.Errorf("no compiler-style diagnostic with its source line:\n%s", out)
	}
	if !strings.Contains(out, "2 errors, 0 warnings in 2 files") {
		t.Errorf("no summary:\n%s", out)
	}
}

// With -o json, lint lists its findings and prints nothing else.
func TestConformityLintJSON(t *testing.T) {
	bad := writeReference(t, "bad.yaml", lintHeader+"spec:\n  brokers:\n    count: 3\n    cuont: 3\n")
	buf := output.ResetForTesting()
	outputMode = "json"
	t.Cleanup(func() { outputMode = "" })

	err := runConformityLint(nil, []string{bad})

	if exitCode(err) != 1 {
		t.Errorf("exit %d, want 1 for an error", exitCode(err))
	}
	var findings []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &findings); err != nil {
		t.Fatalf("not a JSON list: %v\n%s", err, buf.String())
	}
	want := map[string]any{"file": bad, "line": 7.0, "col": 5.0, "severity": "error", "code": "unknown-field",
		"path": "spec.brokers.cuont", "message": `unknown field (did you mean "count"?)`}
	if len(findings) != 1 || !reflect.DeepEqual(findings[0], want) {
		t.Errorf("findings %v, want [%v]", findings, want)
	}
}

func TestConformityLintPassesWithWarningsOnly(t *testing.T) {
	file := writeReference(t, "dup.yaml", lintHeader+"spec:\n  brokers:\n    count: {oneOf: [3, 3]}\n")
	buf := output.ResetForTesting()

	if err := runConformityLint(nil, []string{file}); err != nil {
		t.Errorf("exit %d, want 0 for a warning", exitCode(err))
	}
	if !strings.Contains(buf.String(), "[duplicate-value]") {
		t.Errorf("the warning is not printed:\n%s", buf.String())
	}
	if exitCode(runConformityLint(nil, []string{filepath.Join(t.TempDir(), "missing.yaml")})) != 2 {
		t.Error("an unreadable file does not exit 2")
	}
}

func TestConformityFmt(t *testing.T) {
	messy := lintHeader + "spec:\n  brokers: {count: {equals: 3}, zones: 3, zones: 3}\n"
	file := writeReference(t, "messy.yaml", messy)
	output.ResetForTesting()

	conformityFmtCheck = true
	err := runConformityFmt(nil, []string{file})
	conformityFmtCheck = false
	if exitCode(err) != 1 {
		t.Errorf("--check: exit %d, want 1 for an unformatted file", exitCode(err))
	}

	conformityFmtWrite = true
	err = runConformityFmt(nil, []string{file})
	conformityFmtWrite = false
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(file)
	if want := "apiVersion: conformity.kates.io/v1alpha1\nkind: KafkaReference\nmetadata:\n  name: t\nspec:\n  brokers:\n    count: 3\n    zones: 3\n"; string(got) != want {
		t.Errorf("-w wrote:\n%s", got)
	}

	conformityFmtCheck = true
	defer func() { conformityFmtCheck = false }()
	if err := runConformityFmt(nil, []string{file}); err != nil {
		t.Errorf("--check on a formatted file: %v", err)
	}
	if exitCode(runConformityFmt(nil, []string{writeReference(t, "ref.json", "{}")})) != 1 {
		t.Error("fmt does not refuse a JSON file")
	}
}

// A key set twice to the same value is among them: the API refuses it, so
// lint does too, and fmt removes it.
func TestConformityCheckStopsOnALintErrorBeforeTheAPI(t *testing.T) {
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer ts.Close()
	apiClient = client.New(ts.URL)
	defer func() { conformityFile = "" }()

	for _, spec := range []string{"count: {min: 5, max: 3}", "count: 3\n    count: 3"} {
		file := writeReference(t, "bad.yaml", lintHeader+"spec:\n  brokers:\n    "+spec+"\n")
		output.ResetForTesting()
		if exitCode(runConformityLint(nil, []string{file})) != 1 {
			t.Errorf("%s: lint does not exit 1", spec)
		}
		conformityFile = file
		buf := output.ResetForTesting()

		err := runConformityCheck(nil, nil)

		if exitCode(err) != 2 || called || !strings.Contains(err.Error(), "kates conformity lint lists them") {
			t.Errorf("%s: exit %d (%v), API called %v; want 2 from lint and no call\n%s", spec, exitCode(err), err, called, buf.String())
		}
	}
}
