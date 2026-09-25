package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A report whose status is ERROR says why, and the command prints it: the
// reason used to reach only the server log.
func TestResilienceRun_PrintsWhyTheReportIsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r.yaml")
	data := "testRequest:\n  type: LOAD\nchaosSpec:\n  experimentName: x\nsteadyStateSec: 1\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	ts, buf := setupTest(t, "POST", "/api/resilience", 200,
		`{"status":"ERROR","error":"The benchmark did not start: Concurrency limit reached"}`)
	defer ts.Close()
	resilienceFile, resilienceDryRun = path, false
	defer func() { resilienceFile = "" }()

	if err := resilienceRunCmd.RunE(resilienceRunCmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out := stripAnsi(buf.String()); !strings.Contains(out, "The benchmark did not start: Concurrency limit reached") {
		t.Errorf("the reason is not printed:\n%s", out)
	}
}
