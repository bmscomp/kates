package detect

import (
	"bytes"
	"strings"
	"testing"
)

var fixtures = []struct {
	name   string
	report func() *DetectReport
}{
	{"healthy", healthyReport},
	{"degraded", degradedReport},
}

func TestRenderMarkdownGolden(t *testing.T) {
	for _, f := range fixtures {
		t.Run(f.name, func(t *testing.T) {
			var buf bytes.Buffer
			renderMarkdownDoc(fixtureDoc(f.report()), &buf)
			checkGolden(t, f.name+".md", buf.Bytes())
		})
	}
}

func TestBuildReportDocSectionsInOrder(t *testing.T) {
	want := []string{"cluster", "nodes", "zones", "budget", "storage", "strimzi", "kafka", "ecosystem",
		"monitoring", "network", "latency", "bandwidth", "security", "admission", "netpol", "checks", "remediation"}
	for _, f := range fixtures {
		doc := BuildReportDoc(f.report())
		var got []string
		for _, s := range doc.Sections {
			got = append(got, s.ID)
			if s.Title == "" {
				t.Errorf("%s: section %q has no title", f.name, s.ID)
			}
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: sections = %v, want %v", f.name, got, want)
		}
	}
}

func section(t *testing.T, doc *ReportDoc, id string) Section {
	t.Helper()
	for _, s := range doc.Sections {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("no section %q", id)
	return Section{}
}

func TestVerdictOfLabels(t *testing.T) {
	for _, c := range []struct {
		v      Verdict
		label  string
		status Status
	}{
		{Verdict{}, "COMPATIBLE", StatusOK},
		{Verdict{Warns: 2}, "COMPATIBLE WITH WARNINGS", StatusWarn},
		{Verdict{Fails: 1, Warns: 2}, "INCOMPATIBLE", StatusFail},
	} {
		got := VerdictOf(c.v)
		if got.Label != c.label || got.Status != c.status {
			t.Errorf("VerdictOf(%+v) = %s/%s, want %s/%s", c.v, got.Label, got.Status, c.label, c.status)
		}
	}
}

// The latency matrix is what the probe measured, zones sorted; a run that
// measured nothing gets a skipped section, not a table.
func TestLatencySection(t *testing.T) {
	s := section(t, BuildReportDoc(healthyReport()), "latency")
	if s.Status != StatusOK || len(s.Tables) != 1 {
		t.Fatalf("healthy latency = %s with %d tables", s.Status, len(s.Tables))
	}
	tbl := s.Tables[0]
	if got := strings.Join(tbl.Headers, ","); got != `Source \ Target,eu-west-1a,eu-west-1b,eu-west-1c` {
		t.Errorf("headers = %s, want sorted zones", got)
	}
	if tbl.Rows[0][1].Text != "0.05ms" || tbl.Rows[0][2].Text != "0.90ms" {
		t.Errorf("row 0 = %+v, want the measured values", tbl.Rows[0])
	}

	s = section(t, BuildReportDoc(degradedReport()), "latency")
	if s.Status != StatusSkipped || len(s.Tables) != 0 {
		t.Errorf("unmeasured latency = %s with %d tables, want skipped with none", s.Status, len(s.Tables))
	}
}

func TestZoneMatrixDiagonal(t *testing.T) {
	tbl := zoneMatrix(map[[2]string]Cell{
		{"b", "a"}: cell("1"), {"a", "b"}: cell("2"), {"a", "a"}: cell("self"),
	}, true)
	got := ""
	for _, row := range tbl.Rows {
		for _, c := range row {
			got += c.Text + " "
		}
		got += "/ "
	}
	if got != "a — 2 / b 1 — / " {
		t.Errorf("matrix = %q", got)
	}
}

// Every way the Secret probe can end reads the same in every view, and a
// probe that did not run is "skipped".
func TestSecretAuditFact(t *testing.T) {
	for _, c := range []struct {
		a      SecretCreationAudit
		status Status
		value  string
	}{
		{SecretCreationAudit{}, StatusSkipped, "skipped"},
		{SecretCreationAudit{NamespaceCreated: true, SecretCreated: true}, StatusOK, "works"},
		{SecretCreationAudit{NamespaceCreated: true, BlockedByPolicy: true, PolicyName: "p"}, StatusFail, "blocked by admission policy p"},
		{SecretCreationAudit{NamespaceCreated: true, ErrorMsg: "boom"}, StatusFail, "failed: boom"},
		{SecretCreationAudit{ErrorMsg: "denied"}, StatusFail, "namespace creation failed: denied"},
	} {
		f := secretAuditFact(c.a)
		if f.Status != c.status || !strings.Contains(f.Value, c.value) {
			t.Errorf("secretAuditFact(%+v) = %s %q, want %s containing %q", c.a, f.Status, f.Value, c.status, c.value)
		}
	}
}

func TestExpiringCerts(t *testing.T) {
	got := expiringCerts([]CertExpirationInfo{{SecretName: "a", DaysLeft: 29}, {SecretName: "b", DaysLeft: 30}, {SecretName: "c", DaysLeft: -1}})
	if len(got) != 2 || got[0].SecretName != "a" || got[1].SecretName != "c" {
		t.Errorf("expiringCerts = %+v, want a and c", got)
	}
}
