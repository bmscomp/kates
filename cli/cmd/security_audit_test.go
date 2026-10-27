package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// auditBackend is one check from the backend and a summary that counts it,
// as GET /api/security/audit answers.
const auditBackend = `{"grade":"A","timestamp":"2026-10-27T10:00:00Z",` +
	`"checks":[{"category":"auth","name":"SASL enabled","status":"PASS","detail":"ok","severity":"HIGH","compliance":"CIS 1.1"}],` +
	`"summary":{"total":1,"passed":1,"warnings":0,"failures":0}}`

// auditThroughSeam runs kates security audit --export <json> with kubectl
// scripted by answer, and returns what the command printed and the exported
// result. Every kubectl the audit runs goes through runKubectlFn: the Kyverno
// probes used to run through exec.Command, which no test could script.
func auditThroughSeam(t *testing.T, answer func(cmdStr string) (string, error)) (out string, exported map[string]interface{}, calls []string) {
	t.Helper()
	ts, buf := setupTest(t, "GET", "/api/security/audit", 200, auditBackend)
	defer ts.Close()
	runKubectlFn = func(args ...string) (string, error) {
		cmdStr := strings.Join(args, " ")
		calls = append(calls, cmdStr)
		return answer(cmdStr)
	}
	defer func() { runKubectlFn = runKubectlDefault }()
	file := filepath.Join(t.TempDir(), "audit.json")
	auditExportFile = file
	defer func() { auditExportFile = "" }()

	stdout := captureStdout(t, func() {
		if err := securityAuditCmd.RunE(securityAuditCmd, nil); err != nil {
			t.Errorf("kates security audit: %v", err)
		}
	})
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &exported); err != nil {
		t.Fatalf("the exported report is not JSON: %v\n%s", err, data)
	}
	return stripAnsi(buf.String() + stdout), exported, calls
}

func auditSummary(t *testing.T, result map[string]interface{}) string {
	t.Helper()
	s, _ := result["summary"].(map[string]interface{})
	if s == nil {
		t.Fatal("the result has no summary")
	}
	return fmt.Sprintf("total %v passed %v warnings %v failures %v", s["total"], s["passed"], s["warnings"], s["failures"])
}

// With Kyverno installed, two policies in Audit mode and no violation, the
// audit adds four checks; the summary counts them, as the table does, so the
// summary and the export agree with the table. The summary used to stay the
// backend's, one check behind every Kyverno row.
func TestSecurityAudit_KyvernoChecksCountedInSummary(t *testing.T) {
	out, exported, calls := auditThroughSeam(t, func(cmdStr string) (string, error) {
		switch {
		case strings.HasPrefix(cmdStr, "get crd clusterpolicies.kyverno.io"):
			return "clusterpolicies.kyverno.io   2026-01-01T00:00:00Z", nil
		case strings.HasPrefix(cmdStr, "get clusterpolicies "):
			return "require-labels=Audit\ndisallow-privileged=Audit", nil
		case strings.HasPrefix(cmdStr, "get clusterpolicyreports,policyreports"):
			return "", nil
		}
		return "", fmt.Errorf("unexpected kubectl %s", cmdStr)
	})
	if len(calls) != 3 {
		t.Errorf("kubectl ran %d times through the seam, want 3: %q", len(calls), calls)
	}
	checks, _ := exported["checks"].([]interface{})
	if len(checks) != 5 {
		t.Fatalf("%d checks exported, want the backend's one and four Kyverno checks: %v", len(checks), checks)
	}
	if got, want := auditSummary(t, exported), "total 5 passed 4 warnings 1 failures 0"; got != want {
		t.Errorf("summary = %s, want %s", got, want)
	}
	if exported["grade"] != "A" {
		t.Errorf("grade = %v, want A: nothing violated a policy", exported["grade"])
	}
	for _, want := range []string{"Policy Engine (Kyverno)", "Kyverno Admission Controller", "2 active policies detected", "Audit mode only", "Total Checks"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	// The printed summary is the recomputed one.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "Total Checks") && !strings.Contains(line, "5") {
			t.Errorf("the table's summary still counts the backend's checks only: %q", line)
		}
	}
}

// Without the Kyverno CRD, kubectl fails with its message on stderr, which
// the seam returns as output: that is not a sign that Kyverno is installed.
// A workload violation costs one grade and is a FAIL the summary counts.
func TestSecurityAudit_KyvernoMissingAndViolations(t *testing.T) {
	_, exported, _ := auditThroughSeam(t, func(cmdStr string) (string, error) {
		return `Error from server (NotFound): customresourcedefinitions.apiextensions.k8s.io "clusterpolicies.kyverno.io" not found`, fmt.Errorf("exit status 1")
	})
	if got, want := auditSummary(t, exported), "total 2 passed 1 warnings 0 failures 1"; got != want {
		t.Errorf("summary = %s, want %s", got, want)
	}
	checks, _ := exported["checks"].([]interface{})
	last, _ := checks[len(checks)-1].(map[string]interface{})
	if last["detail"] != "Kyverno is not installed" {
		t.Errorf("last check = %v", last)
	}

	_, exported, _ = auditThroughSeam(t, func(cmdStr string) (string, error) {
		switch {
		case strings.HasPrefix(cmdStr, "get crd "):
			return "clusterpolicies.kyverno.io   2026-01-01T00:00:00Z", nil
		case strings.HasPrefix(cmdStr, "get clusterpolicies "):
			return "require-labels=Enforce", nil
		}
		return "require-labels/check-labels on Deployment web: label app missing", nil
	})
	if got, want := auditSummary(t, exported), "total 5 passed 4 warnings 0 failures 1"; got != want {
		t.Errorf("summary = %s, want %s", got, want)
	}
	if exported["grade"] != "B" {
		t.Errorf("grade = %v, want B: a violation costs one grade", exported["grade"])
	}
}
