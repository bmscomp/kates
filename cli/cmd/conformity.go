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
	conformityFile  string
	conformityJUnit string
)

var conformityCmd = &cobra.Command{
	Use:   "conformity",
	Short: "Check the cluster against a reference architecture",
}

var conformityCheckCmd = &cobra.Command{
	Use:   "check -f <reference.yaml>",
	Short: "Check the cluster against a KafkaReference file",
	Long: `Sends a KafkaReference file to the Kates API, which reads the cluster
read-only and reports every declared rule: PASS, FAIL, WARN, WAIVED or UNKNOWN.

It lints the file first, offline (kates conformity lint), and stops on an
error before calling the API; warnings are printed to stderr.

Exit codes: 0 when every fail rule holds, 1 when one does not, 2 when the check
could not tell (a fail rule it could not read) or could not run (an invalid
reference, another cluster, an unreachable API). Warn rules never fail it.`,
	Example: `  kates conformity check -f platform/prod-3az.yaml
  kates conformity check -f prod-3az.yaml --output-junit conformity.xml
  kates conformity check -f prod-3az.yaml -o json`,
	Args: cobra.NoArgs,
	RunE: runConformityCheck,
}

func runConformityCheck(_ *cobra.Command, _ []string) error {
	if conformityFile == "" {
		return &exitError{code: 2, msg: "--file is required: the KafkaReference to check against"}
	}
	data, err := os.ReadFile(conformityFile)
	if err != nil {
		return &exitError{code: 2, msg: err.Error()}
	}
	if err := lintBeforeCheck(conformityFile, data); err != nil {
		return err
	}
	reference, err := conformityJSON(data)
	if err != nil {
		return &exitError{code: 2, msg: conformityFile + ": " + err.Error()}
	}
	start := time.Now()
	report, err := apiClient.ConformityCheck(context.Background(), reference)
	if err != nil {
		return &exitError{code: 2, msg: conformityFile + ": " + err.Error()}
	}
	if conformityJUnit != "" {
		if err := writeConformityJUnit(report, conformityJUnit, time.Since(start)); err != nil {
			return &exitError{code: 2, msg: "write " + conformityJUnit + ": " + err.Error()}
		}
	}
	output.Render(outputMode == "json", report, func() { renderConformity(report) })
	switch report.Verdict {
	case "PASS":
		return nil
	case "FAIL":
		return &exitError{code: 1}
	default:
		return &exitError{code: 2}
	}
}

// conformityJSON converts a YAML reference to the JSON values the API reads,
// from the node tree: a scalar keeps its text unless YAML tags it a number, a
// boolean or null. Decoding into any would turn until: 2026-12-31 into a
// timestamp, which the API refuses as a date.
func conformityJSON(data []byte) (any, error) {
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
	return conformityValue(&doc)
}

func conformityValue(n *yaml.Node) (any, error) {
	switch n.Kind {
	case yaml.DocumentNode:
		return conformityValue(n.Content[0])
	case yaml.AliasNode:
		return conformityValue(n.Alias)
	case yaml.MappingNode:
		m := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			if k := n.Content[i]; k.ShortTag() == "!!merge" {
				return nil, fmt.Errorf("line %d: merge keys (<<) are not read; write the fields out", k.Line)
			}
			v, err := conformityValue(n.Content[i+1])
			if err != nil {
				return nil, err
			}
			m[n.Content[i].Value] = v
		}
		return m, nil
	case yaml.SequenceNode:
		s := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			v, err := conformityValue(c)
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

func renderConformity(r *client.ConformityReport) {
	target := r.Target.Namespace + "/" + r.Target.Name
	if r.Target.ClusterID != "" {
		target += " (cluster " + r.Target.ClusterID + ")"
	}
	output.Banner("Conformity: "+r.Reference, target+"  │  "+conformityClusterLine(r.Cluster))

	var rows [][]string
	var notes []string
	passed := 0
	for _, res := range r.Results {
		if res.Status == "PASS" && res.Note == "" {
			passed++
			continue
		}
		rows = append(rows, []string{
			conformityStatusLabel(res.Status),
			res.Rule,
			conformityExpected(res.Expected),
			conformityObserved(res),
		})
		notes = append(notes, conformityNote(res))
	}
	if len(rows) > 0 {
		conformityFit(rows, notes)
		output.TableNotes([]string{"Status", "Rule", "Expected", "Observed"}, rows, notes)
	}
	s := r.Summary
	fmt.Fprintf(output.Out, "%s: %d fail, %d warn, %d waived, %d pass, %d unknown\n",
		conformityStatusLabel(r.Verdict), s.Fail, s.Warn, s.Waived, s.Pass, s.Unknown)
	if passed > 0 {
		output.Hint(fmt.Sprintf("%d passing rules not shown; -o json lists them", passed))
	}
}

// conformityFit cuts Expected and Observed so a row fits the terminal: they
// share what Status and Rule leave, and the rule id is never cut. A cut value
// gets a note line of its own under the row, whole.
func conformityFit(rows [][]string, notes []string) {
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

func conformityClusterLine(c client.ConformityClusterState) string {
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

func conformityStatusLabel(status string) string {
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

// conformityListenerProperties is the order a required listener's properties
// are written in, expected and observed.
var conformityListenerProperties = []string{"type", "port", "tls", "auth"}

// conformityExpected writes a constraint as the reference reads: 3, min 8Gi,
// min 4.2.0, max 4.3.9, one of tls, scram-sha-512. A required listener's is one
// constraint per property: port 9092, tls true.
func conformityExpected(e map[string]any) string {
	if v, ok := e["equals"]; ok {
		return fmt.Sprint(v)
	}
	if v, ok := e["oneOf"].([]any); ok {
		return "one of " + conformityJoin(v)
	}
	var parts []string
	if v, ok := e["min"]; ok {
		parts = append(parts, fmt.Sprint("min ", v))
	}
	if v, ok := e["max"]; ok {
		parts = append(parts, fmt.Sprint("max ", v))
	}
	for _, name := range conformityListenerProperties {
		if c, ok := e[name].(map[string]any); ok {
			parts = append(parts, name+" "+conformityExpected(c))
		}
	}
	return strings.Join(parts, ", ")
}

func conformityObserved(r client.ConformityResult) string {
	var text string
	switch v := r.Observed.(type) {
	case nil:
	case []any:
		text = conformityJoin(v)
	case map[string]any:
		var parts []string
		for _, name := range conformityListenerProperties {
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

// conformitySubjects names the first three subjects that fall short, and how
// many more there are: (a, b, c +2).
func conformitySubjects(r client.ConformityResult) string {
	if len(r.Subjects) == 0 {
		return ""
	}
	shown, more := r.Subjects, ""
	if len(shown) > 3 {
		shown, more = shown[:3], fmt.Sprintf(" +%d", len(r.Subjects)-3)
	}
	return " (" + strings.Join(shown, ", ") + more + ")"
}

// conformityNote is what goes under a row, one fact a line: the subjects that
// fall short, then the waiver, the detail and the note. The table cuts each
// line to the terminal width.
func conformityNote(r client.ConformityResult) string {
	parts := conformityDetailParts(r)
	if len(r.Subjects) > 0 {
		parts = append([]string{strings.Join(r.Subjects, ", ")}, parts...)
	}
	return strings.Join(parts, "\n")
}

// conformityDetail is the waiver, detail and note on one line, as JUnit
// shows a waived rule.
func conformityDetail(r client.ConformityResult) string {
	return strings.Join(conformityDetailParts(r), " · ")
}

func conformityDetailParts(r client.ConformityResult) []string {
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

func conformityJoin(values []any) string {
	s := make([]string, len(values))
	for i, v := range values {
		s[i] = fmt.Sprint(v)
	}
	return strings.Join(s, ", ")
}

// writeConformityJUnit writes one testsuite per top-level section and one
// testcase per rule: FAIL a failure, UNKNOWN an error, WAIVED skipped, and WARN
// a pass with its detail in system-out. An UNKNOWN warn rule passes too, as it
// cannot fail the check.
func writeConformityJUnit(r *client.ConformityReport, path string, took time.Duration) error {
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
			suite = &junitTestSuite{Name: "conformity." + section, Time: fmt.Sprintf("%.3f", took.Seconds())}
			bySection[section] = suite
		}
		tc := junitTestCase{Name: res.Rule, ClassName: "conformity." + section, Time: "0.000"}
		message := fmt.Sprintf("expected %s, observed %s%s", conformityExpected(res.Expected), conformityObserved(res), conformitySubjects(res))
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
			tc.Skipped = &junitSkipped{Message: conformityDetail(res)}
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
	conformityCheckCmd.Flags().StringVarP(&conformityFile, "file", "f", "", "KafkaReference YAML or JSON file (required)")
	conformityCheckCmd.Flags().StringVar(&conformityJUnit, "output-junit", "", "Also write a JUnit XML report to this file")
	conformityCmd.AddCommand(conformityCheckCmd)
	rootCmd.AddCommand(conformityCmd)
}
