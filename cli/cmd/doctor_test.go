package cmd

import (
	"encoding/json"
	"errors"
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

// -o json: one JSON document with camelCase keys, and no banner before it.
func TestDoctorRenderChecks_JSON(t *testing.T) {
	buf := output.ResetForTesting()
	defer func() { output.Out, output.Err = os.Stdout, os.Stderr }()
	origMode := outputMode
	outputMode = "json"
	defer func() { outputMode = origMode }()

	renderChecks([]checkResult{{"API Reachable", true, "Connected", ""}})

	var got []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, buf.String())
	}
	if len(got) != 1 || got[0]["name"] != "API Reachable" || got[0]["passed"] != true || got[0]["detail"] != "Connected" {
		t.Errorf("JSON = %s, want camelCase name/passed/detail/remediation", buf.String())
	}
	if _, ok := got[0]["remediation"]; !ok {
		t.Errorf("JSON lacks remediation: %s", buf.String())
	}
}

// A failed required check exits 1; a failed Kyverno check is a warning.
func TestDoctorResult(t *testing.T) {
	if err := doctorResult([]checkResult{
		{"API Reachable", true, "Connected", ""},
		{"Kyverno Installed", false, "CRD not found", "install it"},
	}); err != nil {
		t.Errorf("a failed Kyverno check alone: err = %v, want nil", err)
	}
	err := doctorResult([]checkResult{
		{"API Reachable", true, "Connected", ""},
		{"Kafka Connected", false, "Kafka unreachable", "deploy it"},
	})
	var exit *exitError
	if !errors.As(err, &exit) || exit.code != 1 {
		t.Errorf("a failed Kafka check: err = %v (%T), want exit 1", err, err)
	}
}
