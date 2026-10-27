package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every string in an audit result is the backend's, and the cluster's own
// names end up in it. The HTML export wrote them as markup and the Markdown
// export let a | split a table cell.
func TestExportAuditReport_EscapesBackendStrings(t *testing.T) {
	result := map[string]interface{}{
		"grade":     "B",
		"timestamp": "2026-01-01T00:00:00Z <b>",
		"summary":   map[string]interface{}{"total": 1, "passed": 0, "warnings": 0, "failures": 1},
		"checks": []interface{}{
			map[string]interface{}{
				"status":     "FAIL",
				"compliance": "CIS 1.1 & 1.2",
				"name":       "<script>alert(1)</script>",
				"severity":   "HIGH|CRITICAL",
				"detail":     "topic \"a|b\" <img src=x onerror=alert(1)>",
				"fix":        "set allow.everyone.if.no.acl.found=false & restart",
			},
		},
	}
	dir := t.TempDir()

	htmlPath := filepath.Join(dir, "audit.html")
	if err := exportAuditReport(result, htmlPath); err != nil {
		t.Fatal(err)
	}
	html, _ := os.ReadFile(htmlPath)
	for _, raw := range []string{"<script>", "<img", "<b>"} {
		if strings.Contains(string(html), raw) {
			t.Errorf("HTML carries %q unescaped:\n%s", raw, html)
		}
	}
	for _, want := range []string{"&lt;script&gt;alert(1)&lt;/script&gt;", "CIS 1.1 &amp; 1.2", "&lt;img src=x onerror=alert(1)&gt;", "restart", "00Z &lt;b&gt;"} {
		if !strings.Contains(string(html), want) {
			t.Errorf("HTML lacks %q:\n%s", want, html)
		}
	}

	mdPath := filepath.Join(dir, "audit.md")
	if err := exportAuditReport(result, mdPath); err != nil {
		t.Fatal(err)
	}
	md, _ := os.ReadFile(mdPath)
	row := ""
	for _, line := range strings.Split(string(md), "\n") {
		if strings.Contains(line, "alert(1)") {
			row = line
		}
	}
	if row == "" {
		t.Fatalf("no table row for the check:\n%s", md)
	}
	if !strings.Contains(row, `HIGH\|CRITICAL`) || !strings.Contains(row, `"a\|b"`) {
		t.Errorf("| in a cell is not escaped: %s", row)
	}
	if cells := strings.Count(strings.ReplaceAll(row, `\|`, ""), "|"); cells != 6 {
		t.Errorf("row has %d cell separators, want 6: %s", cells, row)
	}
}

// truncate cut bytes, so a multi-byte character at the cut printed broken.
func TestTruncate_RuneSafe(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"short", "short"},
		{"exactly10!", "exactly10!"},
		{"abcdefghijk", "abcdefghi…"},
		{"ééééééééééé", "ééééééééé…"},
		{"日本語日本語日本語", "日本語日…"},
	} {
		if got := truncate(tc.in, 10); got != tc.want {
			t.Errorf("truncate(%q, 10) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := truncate("anything", 0); got != "" {
		t.Errorf("truncate(_, 0) = %q", got)
	}
}

// Every export skips a check that is not an object, and marks a status other
// than FAIL and WARN as a pass.
func TestAuditChecksAndMarks(t *testing.T) {
	result := map[string]interface{}{"checks": []interface{}{
		map[string]interface{}{"status": "FAIL"}, "junk", 7,
		map[string]interface{}{"status": "WARN"}, map[string]interface{}{"status": "SKIP"},
	}}
	var marks []string
	for _, chk := range auditChecks(result) {
		marks = append(marks, auditMark(chk, "pass", "warn", "fail"))
	}
	if got := strings.Join(marks, " "); got != "fail warn pass" {
		t.Errorf("marks = %q, want fail warn pass", got)
	}
	if got := auditChecks(map[string]interface{}{}); len(got) != 0 {
		t.Errorf("no checks: %v", got)
	}

	txt := auditText(result)
	if !strings.Contains(txt, "[FAIL]") || !strings.Contains(txt, "[WARN]") || strings.Count(txt, "[PASS]") != 1 {
		t.Errorf("text export:\n%s", txt)
	}
	if !strings.Contains(txt, "REMEDIATION\n  - <nil>: <nil>\n  - <nil>: <nil>\n  - <nil>: <nil>\n") {
		t.Errorf("every check that did not PASS needs a remediation line:\n%s", txt)
	}
}
