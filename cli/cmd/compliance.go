package cmd

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	complianceFile  string
	complianceJUnit string
)

var complianceCmd = &cobra.Command{
	Use: "compliance",
	// The command's name until it became compliance; scripts may still call it.
	Aliases: []string{"conformity"},
	Short:   "Check the cluster against a reference architecture",
}

var complianceCheckCmd = &cobra.Command{
	Use:   "check -f <reference.yaml>",
	Short: "Check the cluster against a KafkaReference file",
	Long: `Sends a KafkaReference file to the Kates API, which reads the cluster
read-only and reports every declared rule: PASS, FAIL, WARN, WAIVED or UNKNOWN.

It lints the file first, offline (kates compliance lint), and stops on an
error before calling the API; warnings are printed to stderr.

Exit codes: 0 when every fail rule holds, 1 when one does not, 2 when the check
could not tell (a fail rule it could not read) or could not run (an invalid
reference, another cluster, an unreachable API). Warn rules never fail it.`,
	Example: `  kates compliance check -f platform/prod-3az.yaml
  kates compliance check -f prod-3az.yaml --output-junit compliance.xml
  kates compliance check -f prod-3az.yaml -o json`,
	Args: cobra.NoArgs,
	RunE: runComplianceCheck,
}

func runComplianceCheck(_ *cobra.Command, _ []string) error {
	if complianceFile == "" {
		return &exitError{code: 2, msg: "--file is required: the KafkaReference to check against"}
	}
	data, err := os.ReadFile(complianceFile)
	if err != nil {
		return &exitError{code: 2, msg: err.Error()}
	}
	if err := lintBeforeCheck(complianceFile, data); err != nil {
		return err
	}
	reference, err := complianceJSON(data)
	if err != nil {
		return &exitError{code: 2, msg: complianceFile + ": " + err.Error()}
	}
	start := time.Now()
	report, err := apiClient.ComplianceCheck(context.Background(), reference)
	if err != nil {
		return &exitError{code: 2, msg: complianceFile + ": " + err.Error()}
	}
	if complianceJUnit != "" {
		if err := writeComplianceJUnit(report, complianceJUnit, time.Since(start)); err != nil {
			return &exitError{code: 2, msg: "write " + complianceJUnit + ": " + err.Error()}
		}
	}
	output.Render(outputMode == "json", report, func() { renderCompliance(report) })
	switch report.Verdict {
	case "PASS":
		return nil
	case "FAIL":
		return &exitError{code: 1}
	default:
		return &exitError{code: 2}
	}
}

// complianceJSON converts a YAML reference to the JSON values the API reads,
// from the node tree: a scalar keeps its text unless YAML tags it a number, a
// boolean or null. Decoding into any would turn until: 2026-12-31 into a
// timestamp, which the API refuses as a date.
func complianceJSON(data []byte) (any, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := dec.Decode(&doc); errors.Is(err, io.EOF) {
		return nil, errors.New("the file is empty")
	} else if err != nil {
		return nil, err
	}
	// yaml.v3 refuses a repeated key, an anchor that holds itself and runaway
	// aliasing only when it decodes into a value, never into a node tree.
	var probe any
	if err := doc.Decode(&probe); err != nil {
		return nil, err
	}
	for {
		var next yaml.Node
		err := dec.Decode(&next)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(next.Content) > 0 && next.Content[0].ShortTag() != "!!null" {
			return nil, fmt.Errorf("line %d: a second document; one KafkaReference per file", next.Line)
		}
	}
	return complianceValue(&doc)
}

func complianceValue(n *yaml.Node) (any, error) {
	switch n.Kind {
	case yaml.DocumentNode:
		return complianceValue(n.Content[0])
	case yaml.AliasNode:
		return complianceValue(n.Alias)
	case yaml.MappingNode:
		m := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			if k := n.Content[i]; k.ShortTag() == "!!merge" {
				return nil, fmt.Errorf("line %d: merge keys (<<) are not read; write the fields out", k.Line)
			}
			v, err := complianceValue(n.Content[i+1])
			if err != nil {
				return nil, err
			}
			m[n.Content[i].Value] = v
		}
		return m, nil
	case yaml.SequenceNode:
		s := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			v, err := complianceValue(c)
			if err != nil {
				return nil, err
			}
			s = append(s, v)
		}
		return s, nil
	}
	switch n.ShortTag() {
	case "!!int", "!!float", "!!bool", "!!null":
		var v any
		if err := n.Decode(&v); err != nil {
			return nil, err
		}
		return v, nil
	}
	return n.Value, nil
}

func renderCompliance(r *client.ComplianceReport) {
	target := r.Target.Namespace + "/" + r.Target.Name
	if r.Target.ClusterID != "" {
		target += " (cluster " + r.Target.ClusterID + ")"
	}
	output.Banner("Compliance: "+r.Reference, target+"  │  "+complianceClusterLine(r.Cluster))

	var rows [][]string
	var notes []string
	passed := 0
	for _, res := range r.Results {
		if res.Status == "PASS" && res.Note == "" {
			passed++
			continue
		}
		rows = append(rows, []string{
			complianceStatusLabel(res.Status),
			res.Rule,
			complianceExpected(res.Expected),
			complianceObserved(res),
		})
		notes = append(notes, complianceNote(res))
	}
	if len(rows) > 0 {
		complianceFit(rows, notes)
		output.TableNotes([]string{"Status", "Rule", "Expected", "Observed"}, rows, notes)
	}
	s := r.Summary
	fmt.Fprintf(output.Out, "%s: %d fail, %d warn, %d waived, %d pass, %d unknown\n",
		complianceStatusLabel(r.Verdict), s.Fail, s.Warn, s.Waived, s.Pass, s.Unknown)
	if passed > 0 {
		output.Hint(fmt.Sprintf("%d passing rules not shown; -o json lists them", passed))
	}
}

// complianceFit cuts Expected and Observed so a row fits the terminal: they
// share what Status and Rule leave, and the rule id is never cut. A cut value
// gets a note line of its own under the row, whole.
func complianceFit(rows [][]string, notes []string) {
	used := 2 * 4 // the two spaces before each column
	for _, col := range []int{0, 1} {
		widest := len("Status")
		for _, row := range rows {
			widest = max(widest, ansi.StringWidth(row[col]))
		}
		used += widest
	}
	cell := max((output.TermWidth()-used)/2, 12)
	for i, row := range rows {
		for _, col := range []int{2, 3} {
			if ansi.StringWidth(row[col]) <= cell {
				continue
			}
			whole := []string{"", "", "expected ", "observed "}[col] + row[col]
			notes[i] = strings.TrimPrefix(notes[i]+"\n"+whole, "\n")
			row[col] = ansi.Truncate(row[col], cell, ellipsis())
		}
	}
}

func complianceClusterLine(c client.ComplianceClusterState) string {
	var parts []string
	if c.Ready != nil {
		if *c.Ready {
			parts = append(parts, "Ready")
		} else {
			parts = append(parts, output.WarningStyle.Render("not Ready, results may change"))
		}
	}
	if c.Generation != nil && c.ObservedGeneration != nil && *c.Generation != *c.ObservedGeneration {
		parts = append(parts, output.WarningStyle.Render("reconcile pending"))
	}
	if c.PodsNotReady != nil && *c.PodsNotReady > 0 {
		parts = append(parts, fmt.Sprintf("%d pods not Ready", *c.PodsNotReady))
	}
	return strings.Join(parts, ", ")
}

func complianceStatusLabel(status string) string {
	switch status {
	case "PASS":
		return output.SuccessStyle.Render("✓ PASS")
	case "FAIL":
		return output.ErrorStyle.Render("✖ FAIL")
	case "WARN":
		return output.WarningStyle.Render("▲ WARN")
	case "WAIVED":
		return output.DimStyle.Render("○ WAIVED")
	default:
		return output.WarningStyle.Render("? " + status)
	}
}

// complianceListenerProperties is the order a required listener's properties
// are written in, expected and observed.
var complianceListenerProperties = []string{"type", "port", "tls", "auth"}

// complianceExpected writes a constraint as the reference reads: 3, min 8Gi,
// min 4.2.0, max 4.3.9, one of tls, scram-sha-512. A required listener's is one
// constraint per property: port 9092, tls true.
func complianceExpected(e map[string]any) string {
	if v, ok := e["equals"]; ok {
		return fmt.Sprint(v)
	}
	if v, ok := e["oneOf"].([]any); ok {
		return "one of " + complianceJoin(v)
	}
	var parts []string
	if v, ok := e["min"]; ok {
		parts = append(parts, fmt.Sprint("min ", v))
	}
	if v, ok := e["max"]; ok {
		parts = append(parts, fmt.Sprint("max ", v))
	}
	for _, name := range complianceListenerProperties {
		if c, ok := e[name].(map[string]any); ok {
			parts = append(parts, name+" "+complianceExpected(c))
		}
	}
	return strings.Join(parts, ", ")
}

func complianceObserved(r client.ComplianceResult) string {
	var text string
	switch v := r.Observed.(type) {
	case nil:
	case []any:
		text = complianceJoin(v)
	case map[string]any:
		var parts []string
		for _, name := range complianceListenerProperties {
			if p, ok := v[name]; ok {
				parts = append(parts, fmt.Sprint(name, " ", p))
			}
		}
		text = strings.Join(parts, ", ")
	default:
		text = fmt.Sprint(v)
	}
	return text
}

// complianceSubjects names the first three subjects that fall short, and how
// many more there are: (a, b, c +2).
func complianceSubjects(r client.ComplianceResult) string {
	if len(r.Subjects) == 0 {
		return ""
	}
	shown, more := r.Subjects, ""
	if len(shown) > 3 {
		shown, more = shown[:3], fmt.Sprintf(" +%d", len(r.Subjects)-3)
	}
	return " (" + strings.Join(shown, ", ") + more + ")"
}

// complianceNote is what goes under a row, one fact a line: the subjects that
// fall short, then the waiver, the detail and the note. The table cuts each
// line to the terminal width.
func complianceNote(r client.ComplianceResult) string {
	parts := complianceDetailParts(r)
	if len(r.Subjects) > 0 {
		parts = append([]string{strings.Join(r.Subjects, ", ")}, parts...)
	}
	return strings.Join(parts, "\n")
}

// complianceDetail is the waiver, detail and note on one line, as JUnit
// shows a waived rule.
func complianceDetail(r client.ComplianceResult) string {
	return strings.Join(complianceDetailParts(r), " · ")
}

func complianceDetailParts(r client.ComplianceResult) []string {
	var parts []string
	if r.Waiver != nil {
		parts = append(parts, fmt.Sprintf("%s, waived until %s by %s: %s", r.Waiver.Status, r.Waiver.Until, r.Waiver.By, r.Waiver.Reason))
	}
	if r.Detail != "" {
		parts = append(parts, r.Detail)
	}
	if r.Note != "" {
		parts = append(parts, r.Note)
	}
	return parts
}

func complianceJoin(values []any) string {
	s := make([]string, len(values))
	for i, v := range values {
		s[i] = fmt.Sprint(v)
	}
	return strings.Join(s, ", ")
}

// writeComplianceJUnit writes one testsuite per top-level section and one
// testcase per rule: FAIL a failure, UNKNOWN an error, WAIVED skipped, and WARN
// a pass with its detail in system-out. An UNKNOWN warn rule passes too, as it
// cannot fail the check.
func writeComplianceJUnit(r *client.ComplianceReport, path string, took time.Duration) error {
	bySection := map[string]*junitTestSuite{}
	for _, res := range r.Results {
		// A rule with no id (a malformed report) has no section; indexing
		// the empty split panicked.
		section := "unknown"
		if parts := strings.FieldsFunc(res.Rule, func(c rune) bool { return c == '.' || c == '[' }); len(parts) > 0 {
			section = parts[0]
		}
		suite := bySection[section]
		if suite == nil {
			suite = &junitTestSuite{Name: "compliance." + section, Time: fmt.Sprintf("%.3f", took.Seconds())}
			bySection[section] = suite
		}
		tc := junitTestCase{Name: res.Rule, ClassName: "compliance." + section, Time: "0.000"}
		message := fmt.Sprintf("expected %s, observed %s%s", complianceExpected(res.Expected), complianceObserved(res), complianceSubjects(res))
		switch res.Status {
		case "FAIL":
			tc.Failure = &junitFailure{Message: message, Type: "fail", Content: res.Detail}
			suite.Failures++
		case "UNKNOWN":
			if res.Severity == "warn" {
				tc.SystemOut = "UNKNOWN (warn): " + res.Detail
			} else {
				tc.Error = &junitError{Message: res.Detail, Type: "unknown"}
				suite.Errors++
			}
		case "WAIVED":
			tc.Skipped = &junitSkipped{Message: complianceDetail(res)}
			suite.Skipped++
		case "WARN":
			tc.SystemOut = "WARN: " + message + "; " + res.Detail
		}
		suite.Cases = append(suite.Cases, tc)
		suite.Tests++
	}
	names := make([]string, 0, len(bySection))
	for name := range bySection {
		names = append(names, name)
	}
	sort.Strings(names)
	suites := junitTestSuites{}
	for _, name := range names {
		suites.Suites = append(suites.Suites, *bySection[name])
	}
	data, err := xml.MarshalIndent(suites, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append([]byte(xml.Header), data...), 0o644)
}

func init() {
	complianceCheckCmd.Flags().StringVarP(&complianceFile, "file", "f", "", "KafkaReference YAML or JSON file (required)")
	complianceCheckCmd.Flags().StringVar(&complianceJUnit, "output-junit", "", "Also write a JUnit XML report to this file")
	complianceCmd.AddCommand(complianceCheckCmd)
	rootCmd.AddCommand(complianceCmd)
}
