package detect

import (
	"fmt"
	"sort"
	"time"
)

// The detect report is built once, as a ReportDoc, and every output format
// (Markdown, HTML, TUI, PDF) is a view of it. A section, a table row or a
// wording is decided here, so the four views cannot disagree on what the
// cluster looks like; they only decide how it looks.

// Status grades a section, a fact, a table cell or a hint. The empty Status
// is neutral: a value that is neither good nor bad.
type Status string

const (
	StatusNone    Status = ""
	StatusOK      Status = "ok"
	StatusWarn    Status = "warn"
	StatusFail    Status = "fail"
	StatusSkipped Status = "skipped"
)

// worse returns the more severe of two statuses.
func worse(a, b Status) Status {
	rank := map[Status]int{StatusNone: 0, StatusSkipped: 1, StatusOK: 2, StatusWarn: 3, StatusFail: 4}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// ReportDoc is the whole detect report, in the order every view shows it.
type ReportDoc struct {
	Title       string
	GeneratedAt time.Time
	// Meta is the short identity line under the title.
	Meta     []Fact
	Verdict  VerdictSummary
	Sections []Section
}

// VerdictSummary is the report's headline.
type VerdictSummary struct {
	Status Status
	// Label is COMPATIBLE, COMPATIBLE WITH WARNINGS or INCOMPATIBLE. The
	// Markdown and TUI reports used to say PARTIAL for the middle one, the
	// HTML and PDF reports COMPATIBLE WITH WARNINGS; the longer one says what
	// it means, so every view now uses it.
	Label   string
	Summary string
	Fails   int
	Warns   int
}

// Section is one titled part of the report. Its parts render in the order
// they are declared: paragraphs, facts, meters, tables, code, notes, hints.
type Section struct {
	ID         string
	Title      string
	Status     Status
	Paragraphs []string
	Facts      []Fact
	Meters     []Meter
	Tables     []Table
	// Code is preformatted text, one line per entry (operator log lines).
	Code  []string
	Notes []string
	Hints []Hint
}

// Fact is a labelled value: "CNI: calico".
type Fact struct {
	Label  string
	Value  string
	Status Status
}

// Meter is a share of a whole: the CPU the deployment needs out of what the
// cluster has. Percent is clamped to 0–100.
type Meter struct {
	Label   string
	Detail  string
	Percent float64
}

// Table is a grid of cells under a header row. A Matrix table's first
// column labels its row, as the header labels its column (zone × zone).
type Table struct {
	Headers []string
	Rows    [][]Cell
	Matrix  bool
}

// Cell is one table value.
type Cell struct {
	Text   string
	Status Status
}

// Hint is one remediation: what is wrong, the commands that fix it, and
// where to read more.
type Hint struct {
	Title    string
	Check    string
	Status   Status
	Commands []string
	DocURL   string
}

// cell is a neutral Cell.
func cell(text string) Cell { return Cell{Text: text} }

// cells is a row of neutral cells.
func cells(texts ...string) []Cell {
	row := make([]Cell, len(texts))
	for i, t := range texts {
		row[i] = cell(t)
	}
	return row
}

// yesNo spells a flag in a table.
func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// okOr is StatusOK when good, else bad.
func okOr(good bool, bad Status) Status {
	if good {
		return StatusOK
	}
	return bad
}

// VerdictOf is the headline every view, and kates detect --quiet, prints.
func VerdictOf(v Verdict) VerdictSummary {
	switch {
	case v.Fails > 0:
		return VerdictSummary{Status: StatusFail, Label: "INCOMPATIBLE", Fails: v.Fails, Warns: v.Warns,
			Summary: fmt.Sprintf("%d check(s) failed and %d warning(s). Resolve the failures before deploying.", v.Fails, v.Warns)}
	case v.Warns > 0:
		return VerdictSummary{Status: StatusWarn, Label: "COMPATIBLE WITH WARNINGS", Warns: v.Warns,
			Summary: fmt.Sprintf("The cluster can run a 3-AZ Kafka deployment, with %d warning(s). Review the remediation hints.", v.Warns)}
	default:
		return VerdictSummary{Status: StatusOK, Label: "COMPATIBLE",
			Summary: "The cluster can run a 3-AZ Kafka deployment."}
	}
}

// BuildReportDoc builds the report every view renders: the verdict and one
// section per part of the cluster, in a fixed order.
func BuildReportDoc(report *DetectReport) *ReportDoc {
	hints := GenerateRemediation(report)
	doc := &ReportDoc{
		Title:       "Kafka Cluster Compatibility Report",
		GeneratedAt: time.Now().UTC(),
		Meta: []Fact{
			{Label: "Context", Value: report.Context},
			{Label: "Provider", Value: orUnknown(report.Provider)},
			{Label: "Kubernetes", Value: report.K8sVersion},
			{Label: "Helm", Value: report.HelmVersion},
		},
		Verdict: VerdictOf(report.Verdict),
	}
	builders := []func(*DetectReport) Section{
		clusterSection,
		nodesSection,
		zonesSection,
		budgetSection,
		storageSection,
		strimziSection,
		kafkaSection,
		ecosystemSection,
		monitoringSection,
		networkSection,
		latencySection,
		bandwidthSection,
		securitySection,
		admissionSection,
		netpolSection,
		checksSection,
	}
	for _, build := range builders {
		doc.Sections = append(doc.Sections, build(report))
	}
	doc.Sections = append(doc.Sections, remediationSection(hints))
	return doc
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// expiringCerts are the certificates that expire within 30 days. The
// analyzer, the remediation hints and the report all count with it.
func expiringCerts(certs []CertExpirationInfo) []CertExpirationInfo {
	var soon []CertExpirationInfo
	for _, c := range certs {
		if c.DaysLeft < 30 {
			soon = append(soon, c)
		}
	}
	return soon
}

// zoneMatrix lays a zone-to-zone measurement out as a table, zones sorted,
// whatever order the probe listed them in. at gives the cell for a pair it
// measured; a pair it did not measure, and the diagonal when skipDiagonal is
// set, reads "—". The latency and bandwidth matrices are both this.
func zoneMatrix(pairs map[[2]string]Cell, skipDiagonal bool) Table {
	seen := map[string]bool{}
	for p := range pairs {
		seen[p[0]], seen[p[1]] = true, true
	}
	zones := make([]string, 0, len(seen))
	for z := range seen {
		zones = append(zones, z)
	}
	sort.Strings(zones)

	t := Table{Headers: append([]string{"Source \\ Target"}, zones...), Matrix: true}
	for _, src := range zones {
		row := []Cell{cell(src)}
		for _, dst := range zones {
			c, ok := pairs[[2]string{src, dst}]
			if !ok || (skipDiagonal && src == dst) {
				c = cell("—")
			}
			row = append(row, c)
		}
		t.Rows = append(t.Rows, row)
	}
	return t
}

// profileDescription says what a sizing profile is for. The four reports
// each had their own sentence; this is the one they share.
func profileDescription(profile string) string {
	switch profile {
	case "production":
		return "Highly available, high-throughput production workloads across at least 3 zones."
	case "standard":
		return "Development, staging, or moderate production throughput."
	case "minimal":
		return "Sandbox and lightweight environments with a minimal footprint."
	default:
		return "Not enough capacity for any profile: scale the cluster to at least 3 CPU cores and 8Gi of memory."
	}
}
