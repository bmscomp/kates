package cmd

import (
	"os"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/output"
)

func TestDoctorRenderChecks(t *testing.T) {
	buf := output.ResetForTesting()
	defer func() { output.Out = os.Stdout }()

	checks := []checkResult{
		{"API Reachable", true, "Connected", ""},
		{"Kyverno Ready", false, "Admission controller not running", "Check pod events in 'kyverno' namespace."},
	}

	renderChecks(checks)

	outStr := buf.String()

	if !strings.Contains(outStr, "API Reachable") {
		t.Errorf("Expected output to contain 'API Reachable', got:\n%s", outStr)
	}

	if !strings.Contains(outStr, "WARN") {
		t.Errorf("Expected output to contain 'WARN' for failing Kyverno check, got:\n%s", outStr)
	}

	if !strings.Contains(outStr, "Remediations") {
		t.Errorf("Expected output to contain 'Remediations', got:\n%s", outStr)
	}

	if !strings.Contains(outStr, "Check pod events") {
		t.Errorf("Expected output to contain the remediation text, got:\n%s", outStr)
	}
}
