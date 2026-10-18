package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// resetFlags puts cmd's named flags back to unset when the test ends, and
// whenever the test calls the function it returns, as a new process would
// have them: runCommandLine parses into the package's variables and marks the
// flags changed.
func resetFlags(t *testing.T, cmd *cobra.Command, names ...string) func() {
	t.Helper()
	reset := func() {
		for _, name := range names {
			f := cmd.Flags().Lookup(name)
			_ = f.Value.Set(f.DefValue)
			f.Changed = false
		}
	}
	t.Cleanup(reset)
	return reset
}

// scaffold export's own -o/--output hid the global flag, so
// `kates test scaffold export quick-load -o json` wrote the template to a
// file named json.
func TestScaffoldExport_OutputIsTheGlobalFlag(t *testing.T) {
	resetFlags(t, scaffoldExportCmd, "dir", "file", "output")
	dir := t.TempDir()

	if err := runCommandLine(t, "test", "scaffold", "export", "quick-load", "--dir", dir, "-o", "json"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "json")); err == nil {
		t.Error("-o json wrote the template to a file named json")
	}
	if _, err := os.Stat(filepath.Join(dir, "quick-load.yaml")); err != nil {
		t.Errorf("-o json: the template is not under its own name: %v", err)
	}
	if outputMode != "json" {
		t.Errorf("outputMode = %q, want json from the global -o", outputMode)
	}

	if err := runCommandLine(t, "test", "scaffold", "export", "quick-load", "--dir", dir, "-f", "mine.yaml"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "mine.yaml")); err != nil {
		t.Errorf("-f mine.yaml: %v", err)
	}
}

// ctx set's own --output hid the global flag, so `ctx set … -o json` failed
// with "unknown shorthand flag". The context's default is --default-output.
func TestCtxSet_DefaultOutputLeavesOutputGlobal(t *testing.T) {
	reset := resetFlags(t, ctxSetCmd, "url", "default-output", "output")
	t.Setenv("HOME", t.TempDir())

	_, stderr, err := commandOutput(t, func() error {
		return runCommandLine(t, "ctx", "set", "lab", "--url", "http://lab:8080", "--default-output", "json", "-o", "json")
	})
	if err != nil {
		t.Fatalf("ctx set with -o: %v", err)
	}
	if got := loadConfig().Contexts["lab"].Output; got != "json" {
		t.Errorf("the context's output = %q, want json from --default-output", got)
	}
	if strings.Contains(stderr, "--default-output") {
		t.Errorf("warned with --default-output given:\n%s", stderr)
	}

	reset()
	_, stderr, err = commandOutput(t, func() error {
		return runCommandLine(t, "ctx", "set", "staging", "--url", "http://staging:8080", "--output", "json")
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := loadConfig().Contexts["staging"].Output; got != "table" || !strings.Contains(stderr, "--default-output") {
		t.Errorf("--output alone: the context's output = %q and stderr %q, want table and a note naming --default-output", got, stderr)
	}
}
