package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/pkg/detect"
)

// TestWriteGeneratedValues_FileErrorsExitTwo: a base values file that cannot
// be read, or a --values-output that cannot be written, is exit 2 — kates
// could not produce what was asked — and not exit 0 after an error line.
func TestWriteGeneratedValues_FileErrorsExitTwo(t *testing.T) {
	saved := []string{detectValuesFile, valuesOutput}
	savedDry := dryRun
	t.Cleanup(func() {
		detectValuesFile, valuesOutput = saved[0], saved[1]
		dryRun = savedDry
	})
	dryRun = false
	dir := t.TempDir()
	report := &detect.DetectReport{}

	for _, tc := range []struct {
		name, base, out, want string
	}{
		{"unreadable base", filepath.Join(dir, "missing.yaml"), "", "Failed to read base values"},
		{"unwritable output", "", filepath.Join(dir, "no-such-dir", "values.yaml"), "Failed to write values file"},
	} {
		detectValuesFile, valuesOutput = tc.base, tc.out
		err := writeGeneratedValues(report)
		var exit *exitError
		if !errors.As(err, &exit) || exit.code != couldNotTellExitCode {
			t.Errorf("%s: err = %v (%T), want exit %d", tc.name, err, err, couldNotTellExitCode)
			continue
		}
		if !strings.Contains(exit.msg, tc.want) {
			t.Errorf("%s: message %q, want it to say %q", tc.name, exit.msg, tc.want)
		}
	}

	detectValuesFile = ""
	valuesOutput = filepath.Join(dir, "values.yaml")
	if err := writeGeneratedValues(report); err != nil {
		t.Fatalf("writable output: %v", err)
	}
	if _, err := os.Stat(valuesOutput); err != nil {
		t.Errorf("values file not written: %v", err)
	}
}
