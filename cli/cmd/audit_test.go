package cmd

import (
	"strings"
	"testing"
)

func TestAuditCmd_NoFlags(t *testing.T) {
	mockResponse := `[
		{"id": 1, "timestamp": "2026-03-01T12:00:00Z", "eventType": "TEST", "action": "CREATE", "target": "run-123", "details": "Started"},
		{"id": 2, "timestamp": "2026-03-01T12:05:00Z", "eventType": "TEST", "action": "UPDATE", "target": "run-123", "details": "Finished"}
	]`
	ts, buf := setupTest(t, "GET", "/api/audit", 200, mockResponse)
	defer ts.Close()

	// Clear flags if set by other tests
	auditLimit = 50
	auditType = ""
	auditSince = ""

	err := auditCmd.RunE(auditCmd, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stripAnsi(buf.String())
	if !strings.Contains(out, "CREATE") || !strings.Contains(out, "UPDATE") { // Changed to check for actions
		t.Errorf("missing events in output: %s", out)
	}
	if !strings.Contains(out, "run-123") {
		t.Errorf("missing entity ID in output: %s", out)
	}
}

func TestAuditCmd_WithFilters(t *testing.T) {
	mockResponse := `[` +
		`{"id": 3, "timestamp": "2026-03-01T12:10:00Z", "eventType": "CONFIG", "action": "UPDATE", "target": "cluster", "details": "Updated"}` +
		`]`
	// The test harness will assert that the path matches exactly
	ts, buf := setupTest(t, "GET", "/api/audit", 200, mockResponse)
	defer ts.Close()

	auditLimit = 10
	auditType = "CONFIG" // Changed to match new eventType
	auditSince = "1h"

	err := auditCmd.RunE(auditCmd, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stripAnsi(buf.String())
	if !strings.Contains(out, "CONFIG") {
		t.Errorf("missing filtered event: %s", out)
	}
}

func TestAuditCmd_Empty(t *testing.T) {
	ts, buf := setupTest(t, "GET", "/api/audit", 200, `[]`)
	defer ts.Close()

	auditLimit = 50
	auditType = ""
	auditSince = ""

	err := auditCmd.RunE(auditCmd, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stripAnsi(buf.String())
	if !strings.Contains(out, "No audit events found.") {
		t.Errorf("expected empty message, got: %s", out)
	}
}
