package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/output"
)

func TestDeployFilesStayInACheckout(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(operatorChartDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(operatorChartDir, "Chart.yaml"), []byte("name: strimzi-operator\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	values, errorLog, err := deployFiles()
	if err != nil || values != ".build/values-detected.yaml" || errorLog != "deploy-error.log" {
		t.Fatalf("in a checkout: %q %q %v; the Makefile reads them in the working directory", values, errorLog, err)
	}
}

func TestDeployFilesGoUnderTheStateDirElsewhere(t *testing.T) {
	t.Chdir(t.TempDir())
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	values, errorLog, err := deployFiles()
	dir := filepath.Join(state, "kates", "deploy")
	if err != nil || values != filepath.Join(dir, "values-detected.yaml") || errorLog != filepath.Join(dir, "deploy-error.log") {
		t.Fatalf("XDG_STATE_HOME set: %q %q %v", values, errorLog, err)
	}

	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", home)
	values, _, err = deployFiles()
	if want := filepath.Join(home, ".local", "state", "kates", "deploy", "values-detected.yaml"); err != nil || values != want {
		t.Fatalf("without XDG_STATE_HOME: %q %v, want %s", values, err, want)
	}
}

func TestSaveDeployErrorSaysWhere(t *testing.T) {
	log := filepath.Join(t.TempDir(), "kates", "deploy", "deploy-error.log")
	out := output.ResetForTesting()
	t.Cleanup(func() { output.Out, output.Err = os.Stdout, os.Stderr })
	saveDeployError(log, errors.New("helm upgrade kates: timed out"))
	if data, err := os.ReadFile(log); err != nil || string(data) != "helm upgrade kates: timed out" {
		t.Fatalf("error log %q, %v", data, err)
	}
	if !strings.Contains(out.String(), log) {
		t.Fatalf("the path was not printed: %q", out.String())
	}
}
