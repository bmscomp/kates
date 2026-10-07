package cmd

import (
	"strings"
	"testing"
)

func TestAuditCmd_NoFlags(t *testing.T) {
	mockResponse := `{"page":0,"size":50,"total":2,"count":2,"items":[
		{"id": 1, "timestamp": "2026-03-01T12:00:00Z", "eventType": "TEST", "action": "CREATE", "target": "run-123", "details": "Started"},
		{"id": 2, "timestamp": "2026-03-01T12:05:00Z", "eventType": "TEST", "action": "UPDATE", "target": "run-123", "details": "Finished"}
	]}`
	ts, buf := setupTest(t, "GET", "/api/audit", 200, mockResponse)
	defer ts.Close()

	auditLimit = 50
	auditType = ""
	auditSince = ""

	err := auditCmd.RunE(auditCmd, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stripAnsi(buf.String())
	if !strings.Contains(out, "CREATE") || !strings.Contains(out, "UPDATE") {
		t.Errorf("missing events in output: %s", out)
	}
	if !strings.Contains(out, "run-123") {
		t.Errorf("missing entity ID in output: %s", out)
	}
}

func TestAuditCmd_WithFilters(t *testing.T) {
	mockResponse := `{"page":0,"size":10,"total":1,"count":1,"items":[` +
		`{"id": 3, "timestamp": "2026-03-01T12:10:00Z", "eventType": "CONFIG", "action": "UPDATE", "target": "cluster", "details": "Updated"}` +
		`]}`
	ts, buf := setupTest(t, "GET", "/api/audit", 200, mockResponse)
	defer ts.Close()

	auditLimit = 10
	auditType = "CONFIG"
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

// A row names who made the change, and marks an agent's key; a row from
// before the Kates API recorded actors shows no one.
func TestAuditCmd_ShowsWhoActed(t *testing.T) {
	mockResponse := `{"page":0,"size":50,"total":3,"count":3,"items":[
		{"id": 5, "timestamp": "2026-10-07T12:00:00Z", "eventType": "webhook", "action": "CREATE", "target": "/api/webhooks", "details": "HTTP 403", "actor": "claude-on-lab", "principalType": "agent"},
		{"id": 4, "timestamp": "2026-10-07T11:00:00Z", "eventType": "test", "action": "CREATE", "target": "0a1b2c3d", "details": "schedule 'nightly'", "actor": "system:scheduler", "principalType": "system"},
		{"id": 3, "timestamp": "2026-10-01T11:00:00Z", "eventType": "test", "action": "DELETE", "target": "9f8e7d6c", "details": "Test deleted"}
	]}`
	ts, buf := setupTest(t, "GET", "/api/audit", 200, mockResponse)
	defer ts.Close()

	auditLimit, auditType, auditSince, auditActor = 50, "", "", ""

	if err := auditCmd.RunE(auditCmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stripAnsi(buf.String())
	for _, want := range []string{"claude-on-lab (agent)", "system:scheduler", "By"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestAuditCmd_Empty(t *testing.T) {
	ts, buf := setupTest(t, "GET", "/api/audit", 200, `{"page":0,"size":50,"total":0,"count":0,"items":[]}`)
	defer ts.Close()

	auditLimit = 50
	auditType = ""
	auditSince = ""
	auditActor = ""

	err := auditCmd.RunE(auditCmd, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stripAnsi(buf.String())
	if !strings.Contains(out, "No audit events found.") {
		t.Errorf("expected empty message, got: %s", out)
	}
}
