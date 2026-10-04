package migrate

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Row statuses.
const (
	StatusPass = "PASS"
	StatusFail = "FAIL"
	StatusSkip = "SKIP"
)

// The assertion rows of a migration report, verbatim from
// scripts/test-mm2-migration.sh so the tutorials' tables stay true.
const (
	RowClusterReachable    = "cluster reachable"
	RowStrimziCRDs         = "Strimzi CRDs"
	RowTargetKafkaReady    = "target Kafka Ready"
	RowTargetCredentials   = "target credentials"
	RowSourceDeployed      = "legacy source deployed"
	RowSourceServing       = "legacy source serving"
	RowCorpusProduced      = "corpus produced"
	RowSourceEndOffsets    = "source end offsets"
	RowSourceConsumerGroup = "source consumer group"
	RowMirrorInstalled     = "MirrorMaker 2 installed"
	RowCRReady             = "CR Ready"
	RowConnectorsRunning   = "connectors RUNNING"
	RowReplicatedTopic     = "replicated topic exists"
	RowRecordCount         = "record count"
	RowRecordContent       = "record content"
	RowOffsetTranslation   = "offset translation"
	RowCutoverApplied      = "cutover applied"
	RowCutoverFroze        = "cutover froze the target"
)

// RowNames lists every row in the order the scripts recorded them.
var RowNames = []string{
	RowClusterReachable, RowStrimziCRDs, RowTargetKafkaReady, RowTargetCredentials,
	RowSourceDeployed, RowSourceServing,
	RowCorpusProduced, RowSourceEndOffsets, RowSourceConsumerGroup,
	RowMirrorInstalled, RowCRReady, RowConnectorsRunning,
	RowReplicatedTopic, RowRecordCount, RowRecordContent, RowOffsetTranslation,
	RowCutoverApplied, RowCutoverFroze,
}

// Row is one assertion of the report.
type Row struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// Report collects the assertions of a verify or run: every one lands here,
// because a migration test whose output has to be interpreted is a
// migration test nobody runs twice.
type Report struct {
	// Header states the pair, the providers, the operators and the policy.
	Header map[string]string
	Rows   []Row
	// Started is when the report was opened.
	Started time.Time
}

// NewReport opens a report with the given header, started now.
func NewReport(header map[string]string) *Report {
	if header == nil {
		header = map[string]string{}
	}
	return &Report{Header: header, Started: time.Now()}
}

// Add appends a row.
func (r *Report) Add(row Row) {
	r.Rows = append(r.Rows, row)
}

// Pass records a passed assertion.
func (r *Report) Pass(name, detail string) {
	r.Add(Row{Name: name, Status: StatusPass, Detail: detail})
}

// Fail records a failed assertion.
func (r *Report) Fail(name, detail string) {
	r.Add(Row{Name: name, Status: StatusFail, Detail: detail})
}

// Skip records an assertion that could not be made.
func (r *Report) Skip(name, detail string) {
	r.Add(Row{Name: name, Status: StatusSkip, Detail: detail})
}

// Failed reports whether any row failed — the exit code of verify and run.
func (r *Report) Failed() bool {
	_, fail, _ := r.Counts()
	return fail > 0
}

// Counts returns the number of passed, failed and skipped rows.
func (r *Report) Counts() (pass, fail, skip int) {
	for _, row := range r.Rows {
		switch row.Status {
		case StatusPass:
			pass++
		case StatusFail:
			fail++
		case StatusSkip:
			skip++
		}
	}
	return pass, fail, skip
}

// Row returns the last row recorded under name.
func (r *Report) Row(name string) (Row, bool) {
	for i := len(r.Rows) - 1; i >= 0; i-- {
		if r.Rows[i].Name == name {
			return r.Rows[i], true
		}
	}
	return Row{}, false
}

// Table renders the report as plain, aligned text: the header (keys sorted),
// the assertion table the scripts printed (RESULT, ASSERTION, DETAIL) and a
// count line. No colour and no glyphs — the cmd layer styles it.
func (r *Report) Table() string {
	var b strings.Builder
	if len(r.Header) > 0 {
		keys := make([]string, 0, len(r.Header))
		width := 0
		for k := range r.Header {
			keys = append(keys, k)
			if len(k) > width {
				width = len(k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "  %-*s  %s\n", width, k, r.Header[k])
		}
		b.WriteString("\n")
	}
	nameWidth := len("ASSERTION")
	for _, row := range r.Rows {
		if len(row.Name) > nameWidth {
			nameWidth = len(row.Name)
		}
	}
	fmt.Fprintf(&b, "  %-6s  %-*s  %s\n", "RESULT", nameWidth, "ASSERTION", "DETAIL")
	fmt.Fprintf(&b, "  %-6s  %-*s  %s\n", "------", nameWidth, strings.Repeat("-", nameWidth), "------")
	for _, row := range r.Rows {
		fmt.Fprintf(&b, "  %-6s  %-*s  %s\n", row.Status, nameWidth, row.Name, row.Detail)
	}
	pass, fail, skip := r.Counts()
	fmt.Fprintf(&b, "\n  %d passed, %d failed, %d skipped\n", pass, fail, skip)
	return b.String()
}

// reportJSON is the JSON shape of a report: the rows with their status and
// detail, so CI asserts on fields rather than grep.
type reportJSON struct {
	Header  map[string]string `json:"header"`
	Started time.Time         `json:"started"`
	Rows    []Row             `json:"rows"`
	Summary struct {
		Pass   int  `json:"pass"`
		Fail   int  `json:"fail"`
		Skip   int  `json:"skip"`
		Failed bool `json:"failed"`
	} `json:"summary"`
}

// JSON renders the report for -o json.
func (r *Report) JSON() ([]byte, error) {
	out := reportJSON{Header: r.Header, Started: r.Started, Rows: r.Rows}
	if out.Header == nil {
		out.Header = map[string]string{}
	}
	if out.Rows == nil {
		out.Rows = []Row{}
	}
	out.Summary.Pass, out.Summary.Fail, out.Summary.Skip = r.Counts()
	out.Summary.Failed = out.Summary.Fail > 0
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("render report JSON: %w", err)
	}
	return data, nil
}
