package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"
	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// HelmRelease represents a discovered Helm release.
type HelmRelease struct {
	Name       string `json:"name"`
	Namespace  string `json:"namespace"`
	Status     string `json:"status"`
	Revision   string `json:"revision"`
	Chart      string `json:"chart"`
	AppVersion string `json:"app_version"`
}

// HelmTestHook represents a single test hook result.
type HelmTestHook struct {
	Name     string        `json:"name"`
	Phase    string        `json:"phase"` // "Succeeded", "Failed", "Running"
	Duration time.Duration `json:"duration"`
	Logs     string        `json:"logs,omitempty"`
}

// HelmTestResult is the result of testing one release.
type HelmTestResult struct {
	Release   string         `json:"release"`
	Namespace string         `json:"namespace"`
	Chart     string         `json:"chart"`
	Status    string         `json:"status"` // "passed", "failed", "skipped"
	Hooks     []HelmTestHook `json:"hooks"`
	Error     string         `json:"error,omitempty"`
	Duration  time.Duration  `json:"duration"`
}

// HelmTestSuiteResult is the full suite result.
type HelmTestSuiteResult struct {
	Timestamp time.Time        `json:"timestamp"`
	Cluster   string           `json:"cluster"`
	Results   []HelmTestResult `json:"results"`
	Passed    int              `json:"passed"`
	Failed    int              `json:"failed"`
	Skipped   int              `json:"skipped"`
	Duration  time.Duration    `json:"duration"`
}

// ---------------------------------------------------------------------------
// Variables & flags
// ---------------------------------------------------------------------------

var (
	helmTestNamespace  string
	helmTestTimeout    string
	helmTestVerbose    bool
	helmTestExport     string // "" (none), "json", "md", "pdf"
	helmTestRelease    string // override release name
	helmTestKubeconfig string
)

// knownComponents maps a component alias to well-known release name prefixes.
var knownComponents = map[string][]string{
	"kafka": {"kafka-cluster", "krafter"},
	"kates": {"kates"},
	"chaos": {"chaos", "kates-chaos"},
}

// ---------------------------------------------------------------------------
// Commands
// ---------------------------------------------------------------------------

var helmTestCmd = &cobra.Command{
	Use:   "helm [component] [-- extra-helm-args...]",
	Short: "Run Helm tests for deployed Kates ecosystem components",
	Long: `Run Helm tests against all deployed Kates components on the cluster.

Automatically discovers Kafka, Kates, and Chaos Helm releases and runs
helm test for each. Results are displayed with pass/fail status per test
hook, with optional verbose pod logs and export to JSON/Markdown/PDF.

You can pass extra native arguments to the underlying 'helm test' command
by appending them after a '--' separator (e.g. to filter tests).

Examples:
  kates test helm                              # test all detected components
  kates test helm kafka                        # test only Kafka
  kates test helm --release krafter            # explicit release name
  kates test helm --verbose                    # show pod logs on failure
  kates test helm --timeout 5m                 # custom timeout
  kates test helm --export md                  # export results to Markdown
  kates test helm --export json                # export results to JSON
  kates test helm kafka -- --filter my-test    # run only 'my-test' hook
  kates test helm -o json                      # raw JSON output (no styling)`,
	Args: cobra.ArbitraryArgs,
	RunE: runHelmTests,
}

// ---------------------------------------------------------------------------
// Main flow
// ---------------------------------------------------------------------------

func runHelmTests(cmd *cobra.Command, args []string) error {
	suiteStart := time.Now()

	// 1. Current kubectl context
	cluster := currentKubeContext()

	// 2. Banner
	ns := helmTestNamespace
	output.Banner("Helm Test Suite", fmt.Sprintf("Cluster: %s │ Namespace: %s", cluster, ns))

	// 3. Discover releases
	var releases []HelmRelease
	if helmTestRelease != "" {
		// Explicit release override — fabricate a release entry
		releases = []HelmRelease{{
			Name:      helmTestRelease,
			Namespace: ns,
			Chart:     "(override)",
		}}
	} else {
		releases = discoverHelmReleases(ns)
	}

	// 4. Parse arguments (component and passthrough args)
	var component string
	var extraArgs []string
	if len(args) > 0 {
		if !strings.HasPrefix(args[0], "-") {
			component = strings.ToLower(args[0])
			extraArgs = args[1:]
		} else {
			extraArgs = args
		}
	}

	// 5. Optional component filter
	if component != "" {
		releases = filterByComponent(releases, component)
	}

	// 6. Discovery section
	output.SubHeader("Discovering Releases")
	if len(releases) == 0 {
		output.Warn("No Kates ecosystem releases found in namespace " + ns)
		output.Hint("Install with: helm install krafter ./charts/kafka-cluster -n " + ns)
		return nil
	}
	for _, r := range releases {
		status := output.SuccessStyle.Render("● deployed")
		if r.Status != "" && strings.ToLower(r.Status) != "deployed" {
			status = output.WarningStyle.Render("◈ " + r.Status)
		}
		fmt.Fprintf(output.Out, "  %s  %-30s %s\n",
			status,
			output.AccentStyle.Render(r.Name),
			output.DimStyle.Render(r.Chart))
	}

	// 7. Run tests per release
	var results []HelmTestResult
	for _, rel := range releases {
		fmt.Fprintln(output.Out)
		displayName := helmDisplayName(rel)
		output.Header(fmt.Sprintf("%s (%s)", displayName, rel.Name))

		result := runSingleHelmTest(rel, extraArgs)
		results = append(results, result)

		// Per-hook display
		for _, hook := range result.Hooks {
			icon := output.SuccessStyle.Render("✓ Passed")
			if hook.Phase == "Failed" {
				icon = output.ErrorStyle.Render("✗ Failed")
			} else if hook.Phase == "Running" {
				icon = output.AccentStyle.Render("◉ Running")
			}
			dur := fmt.Sprintf("%6.1fs", hook.Duration.Seconds())
			fmt.Fprintf(output.Out, "  ● %-42s %s  %s\n",
				hook.Name, icon, output.DimStyle.Render(dur))

			// Show logs on failure (always) or if verbose
			if hook.Logs != "" && (hook.Phase == "Failed" || helmTestVerbose) {
				displayHookLogs(hook)
			}
		}

		if result.Status == "failed" {
			// Diagnostic hints
			for _, hook := range result.Hooks {
				if hook.Phase == "Failed" {
					hints := helmTestHints(hook.Name, hook.Logs)
					if len(hints) > 0 {
						fmt.Fprintln(output.Out)
						output.SubHeader("Diagnostics")
						for _, h := range hints {
							output.Hint("  💡 " + h)
						}
					}
				}
			}
		}

		if result.Error != "" {
			// Extract just the Error: line if it exists to avoid dumping the whole log again
			lines := strings.Split(result.Error, "\n")
			for _, l := range lines {
				if strings.HasPrefix(l, "Error:") || strings.Contains(l, "unable to get pod logs") {
					output.Error(l)
				}
			}
		}
	}

	// 7. Build suite result
	suite := HelmTestSuiteResult{
		Timestamp: time.Now(),
		Cluster:   cluster,
		Results:   results,
		Duration:  time.Since(suiteStart),
	}
	for _, r := range results {
		switch r.Status {
		case "passed":
			suite.Passed++
		case "failed":
			suite.Failed++
		case "skipped":
			suite.Skipped++
		}
	}

	// 8. Summary banner
	displaySuiteSummary(suite)

	// 9. JSON output mode — return raw data
	if outputMode == "json" {
		output.JSON(suite)
		return nil
	}

	// 10. Export
	if helmTestExport != "" {
		if err := handleHelmTestExport(suite); err != nil {
			return cmdErr("Export failed: " + err.Error())
		}
	}

	// 11. Exit with error if failures
	if suite.Failed > 0 {
		return cmdErr(fmt.Sprintf("%d release(s) had test failures", suite.Failed))
	}
	return nil
}

// ---------------------------------------------------------------------------
// Discovery
// ---------------------------------------------------------------------------

func discoverHelmReleases(namespace string) []HelmRelease {
	var discovered []HelmRelease
	seen := map[string]bool{}

	// Try `helm list` to get all releases in namespace
	args := []string{"list", "-n", namespace, "-o", "json"}
	if helmTestKubeconfig != "" {
		args = append(args, "--kubeconfig", helmTestKubeconfig)
	}
	out, err := runHelmCmd(args...)
	if err == nil {
		var releases []HelmRelease
		if json.Unmarshal([]byte(out), &releases) == nil {
			for _, r := range releases {
				if isKatesEcosystemChart(r.Chart) {
					r.Namespace = namespace
					discovered = append(discovered, r)
					seen[r.Name] = true
				}
			}
		}
	}

	// Also probe well-known release names
	for _, names := range knownComponents {
		for _, name := range names {
			if seen[name] {
				continue
			}
			statusArgs := []string{"status", name, "-n", namespace, "-o", "json"}
			if helmTestKubeconfig != "" {
				statusArgs = append(statusArgs, "--kubeconfig", helmTestKubeconfig)
			}
			statusOut, err := runHelmCmd(statusArgs...)
			if err != nil {
				continue
			}
			var statusResult struct {
				Name string `json:"name"`
				Info struct {
					Status string `json:"status"`
				} `json:"info"`
				Chart struct {
					Metadata struct {
						Name    string `json:"name"`
						Version string `json:"version"`
					} `json:"metadata"`
				} `json:"chart"`
			}
			if json.Unmarshal([]byte(statusOut), &statusResult) == nil && statusResult.Name != "" {
				discovered = append(discovered, HelmRelease{
					Name:      statusResult.Name,
					Namespace: namespace,
					Status:    statusResult.Info.Status,
					Chart:     statusResult.Chart.Metadata.Name + "-" + statusResult.Chart.Metadata.Version,
				})
				seen[statusResult.Name] = true
			}
		}
	}

	return discovered
}

func isKatesEcosystemChart(chart string) bool {
	lower := strings.ToLower(chart)
	prefixes := []string{"kafka-cluster", "kates", "kates-chaos", "chaos", "krafter"}
	for _, p := range prefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

func filterByComponent(releases []HelmRelease, component string) []HelmRelease {
	names, ok := knownComponents[component]
	if !ok {
		// Treat as a release name prefix
		names = []string{component}
	}
	var filtered []HelmRelease
	for _, r := range releases {
		for _, n := range names {
			if strings.HasPrefix(strings.ToLower(r.Name), n) ||
				strings.HasPrefix(strings.ToLower(r.Chart), n) {
				filtered = append(filtered, r)
				break
			}
		}
	}
	return filtered
}

// ---------------------------------------------------------------------------
// Single release test
// ---------------------------------------------------------------------------

func runSingleHelmTest(rel HelmRelease, extraArgs []string) HelmTestResult {
	start := time.Now()

	args := []string{"test", rel.Name, "-n", rel.Namespace, "--timeout", helmTestTimeout, "--logs"}
	if helmTestKubeconfig != "" {
		args = append(args, "--kubeconfig", helmTestKubeconfig)
	}
	if len(extraArgs) > 0 {
		args = append(args, extraArgs...)
	}

	rawOutput, err := runHelmCmd(args...)
	duration := time.Since(start)

	result := HelmTestResult{
		Release:   rel.Name,
		Namespace: rel.Namespace,
		Chart:     rel.Chart,
		Duration:  duration,
	}

	if err != nil {
		result.Status = "failed"
		result.Error = strings.TrimSpace(rawOutput)
		if result.Error == "" {
			result.Error = err.Error()
		}
		// Still try to parse whatever output we got
		result.Hooks = parseHelmTestOutput(rawOutput)
		if len(result.Hooks) == 0 {
			// If no hooks could be parsed, check if it's a "no tests" situation
			if strings.Contains(rawOutput, "no tests found") ||
				strings.Contains(rawOutput, "no test") {
				result.Status = "skipped"
				result.Error = "no test hooks defined"
			}
		}
		return result
	}

	result.Hooks = parseHelmTestOutput(rawOutput)

	// Determine overall status
	result.Status = "passed"
	for _, h := range result.Hooks {
		if h.Phase == "Failed" {
			result.Status = "failed"
			break
		}
	}
	if len(result.Hooks) == 0 {
		result.Status = "skipped"
	}

	return result
}

// ---------------------------------------------------------------------------
// Output parser
// ---------------------------------------------------------------------------

var (
	reTestPod      = regexp.MustCompile(`(?i)^TEST SUITE:\s*(.+)$`)
	reTestPhase    = regexp.MustCompile(`(?i)^Phase:\s*(\S+)`)
	reLastStarted  = regexp.MustCompile(`(?i)^Last Started:\s*(.+)$`)
	reLastComplete = regexp.MustCompile(`(?i)^Last Completed:\s*(.+)$`)
	rePodLogs      = regexp.MustCompile(`(?i)^POD LOGS:\s*(.*)$`)
)

func parseHelmTestOutput(rawOutput string) []HelmTestHook {
	var hooks []HelmTestHook
	lines := strings.Split(rawOutput, "\n")

	var currentHook *HelmTestHook
	var lastStarted time.Time
	var collectingLogs bool
	var logLines []string

	flushHook := func() {
		if currentHook != nil {
			if collectingLogs && len(logLines) > 0 {
				currentHook.Logs = strings.Join(logLines, "\n")
			}
			hooks = append(hooks, *currentHook)
		}
		currentHook = nil
		collectingLogs = false
		logLines = nil
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// TEST SUITE: <pod-name>
		if m := reTestPod.FindStringSubmatch(trimmed); m != nil {
			flushHook()
			currentHook = &HelmTestHook{Name: strings.TrimSpace(m[1])}
			continue
		}

		// Phase: Succeeded / Failed
		if m := reTestPhase.FindStringSubmatch(trimmed); m != nil && currentHook != nil {
			currentHook.Phase = strings.TrimSpace(m[1])
			continue
		}

		// Last Started:
		if m := reLastStarted.FindStringSubmatch(trimmed); m != nil {
			if t, err := parseHelmTimestamp(strings.TrimSpace(m[1])); err == nil {
				lastStarted = t
			}
			continue
		}

		// Last Completed:
		if m := reLastComplete.FindStringSubmatch(trimmed); m != nil && currentHook != nil {
			if t, err := parseHelmTimestamp(strings.TrimSpace(m[1])); err == nil {
				if !lastStarted.IsZero() {
					currentHook.Duration = t.Sub(lastStarted)
				}
			}
			continue
		}

		// POD LOGS:
		if m := rePodLogs.FindStringSubmatch(trimmed); m != nil {
			if collectingLogs && currentHook != nil && len(logLines) > 0 {
				// Previous log section ends
				currentHook.Logs = strings.Join(logLines, "\n")
				logLines = nil
			}
			collectingLogs = true
			continue
		}

		// Collect log lines
		if collectingLogs {
			logLines = append(logLines, line)
		}
	}

	flushHook()
	return hooks
}

func parseHelmTimestamp(s string) (time.Time, error) {
	// Helm uses Go's default time format or RFC3339
	formats := []string{
		time.RFC3339,
		"Mon Jan 2 15:04:05 2006 -0700",
		"2006-01-02 15:04:05.999999999 -0700 MST",
		time.RFC3339Nano,
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse %q", s)
}

// ---------------------------------------------------------------------------
// Display helpers
// ---------------------------------------------------------------------------

func helmDisplayName(rel HelmRelease) string {
	chart := strings.ToLower(rel.Chart)
	switch {
	case strings.HasPrefix(chart, "kafka-cluster") || strings.HasPrefix(chart, "krafter"):
		return "Kafka Cluster"
	case strings.HasPrefix(chart, "kates-chaos") || strings.HasPrefix(chart, "chaos"):
		return "Chaos Engine"
	case strings.HasPrefix(chart, "kates"):
		return "Kates Platform"
	default:
		if len(rel.Name) == 0 {
			return rel.Name
		}
		return strings.ToUpper(rel.Name[:1]) + rel.Name[1:]
	}
}

func displayHookLogs(hook HelmTestHook) {
	lines := strings.Split(hook.Logs, "\n")
	// Trim trailing empty lines
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return
	}
	// Cap at 20 lines
	truncated := false
	if len(lines) > 20 {
		lines = lines[len(lines)-20:]
		truncated = true
	}
	fmt.Fprintln(output.Out)
	border := output.DimStyle.Render("    ┃ ")
	if truncated {
		fmt.Fprintln(output.Out, output.DimStyle.Render("    ┃ ... (truncated)"))
	}
	for _, l := range lines {
		fmt.Fprintln(output.Out, border+output.DimStyle.Render(l))
	}
	fmt.Fprintln(output.Out)
}

func displaySuiteSummary(suite HelmTestSuiteResult) {
	fmt.Fprintln(output.Out)

	passedStr := output.SuccessStyle.Render(fmt.Sprintf("%d passed", suite.Passed))
	failedStr := output.ErrorStyle.Render(fmt.Sprintf("%d failed", suite.Failed))
	skippedStr := output.DimStyle.Render(fmt.Sprintf("%d skipped", suite.Skipped))
	durStr := fmt.Sprintf("%.1fs", suite.Duration.Seconds())

	line1 := fmt.Sprintf("  Results: %s · %s · %s", passedStr, failedStr, skippedStr)
	line2 := fmt.Sprintf("  Total time: %s", durStr)

	content := line1 + "\n" + line2
	box := output.BoxStyle.Padding(1, 3)
	fmt.Fprintln(output.Out, box.Render(content))
}

// ---------------------------------------------------------------------------
// Diagnostic hints
// ---------------------------------------------------------------------------

func helmTestHints(hookName, logs string) []string {
	var hints []string
	lower := strings.ToLower(logs)
	hookLower := strings.ToLower(hookName)

	if strings.Contains(lower, "timed out") || strings.Contains(lower, "operation now in progress") {
		hints = append(hints, "TCP connection timed out — check NetworkPolicies and listener ports")
		hints = append(hints, "Verify: kubectl get kafka -n <ns> -o jsonpath='{.items[0].status.listeners}'")
	}
	if strings.Contains(lower, "dns") || strings.Contains(lower, "nxdomain") || strings.Contains(lower, "resolve") {
		hints = append(hints, "DNS resolution failed — check global.clusterDomain in your values.yaml")
		hints = append(hints, "Verify: kubectl exec -it <pod> -- nslookup <svc>.kafka.svc")
	}
	if strings.Contains(lower, "sasl") || strings.Contains(lower, "authentication") {
		hints = append(hints, "SASL authentication error — check KafkaUser credentials")
	}
	if strings.Contains(lower, "not found") && strings.Contains(hookLower, "produce") {
		hints = append(hints, "Produce/consume test pod not created — previous tier may have failed")
	}
	if strings.Contains(lower, "exec format error") {
		hints = append(hints, "Architecture mismatch — rebuild tester image with multi-arch support")
		hints = append(hints, "Fix: docker buildx build --platform linux/amd64,linux/arm64 -t kates-tester .")
	}
	if strings.Contains(lower, "connection refused") {
		hints = append(hints, "Service endpoint unreachable — check if broker pods are running")
		hints = append(hints, "Verify: kubectl get pods -n <ns> -l strimzi.io/kind=Kafka")
	}
	if strings.Contains(lower, "crashloopbackoff") || strings.Contains(lower, "error") && strings.Contains(lower, "image") {
		hints = append(hints, "Test pod crash — check image pull policy and registry access")
	}

	return hints
}

// ---------------------------------------------------------------------------
// Export functions
// ---------------------------------------------------------------------------

func handleHelmTestExport(suite HelmTestSuiteResult) error {
	switch strings.ToLower(helmTestExport) {
	case "json":
		content := exportHelmTestJSON(suite)
		filename := fmt.Sprintf("kates-helm-test-%s.json", suite.Timestamp.Format("20060102-150405"))
		if err := os.WriteFile(filename, []byte(content), 0644); err != nil {
			return err
		}
		output.Success("Exported to " + filename)
	case "md", "markdown":
		content := exportHelmTestMarkdown(suite)
		filename := fmt.Sprintf("kates-helm-test-%s.md", suite.Timestamp.Format("20060102-150405"))
		if err := os.WriteFile(filename, []byte(content), 0644); err != nil {
			return err
		}
		output.Success("Exported to " + filename)
	case "pdf":
		filename := fmt.Sprintf("kates-helm-test-%s.pdf", suite.Timestamp.Format("20060102-150405"))
		if err := exportHelmTestPDF(suite, filename); err != nil {
			return err
		}
		output.Success("Exported to " + filename)
	default:
		return fmt.Errorf("unknown export format %q — use json, md, or pdf", helmTestExport)
	}
	return nil
}

func exportHelmTestJSON(suite HelmTestSuiteResult) string {
	data, err := json.MarshalIndent(suite, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(data)
}

func exportHelmTestMarkdown(suite HelmTestSuiteResult) string {
	var sb strings.Builder

	sb.WriteString("# Kates Helm Test Report\n\n")
	sb.WriteString("| Field | Value |\n|-------|-------|\n")
	sb.WriteString(fmt.Sprintf("| Cluster | %s |\n", suite.Cluster))
	sb.WriteString(fmt.Sprintf("| Timestamp | %s |\n", suite.Timestamp.Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("| Duration | %.1fs |\n", suite.Duration.Seconds()))
	sb.WriteString(fmt.Sprintf("| Passed | %d |\n", suite.Passed))
	sb.WriteString(fmt.Sprintf("| Failed | %d |\n", suite.Failed))
	sb.WriteString(fmt.Sprintf("| Skipped | %d |\n", suite.Skipped))
	sb.WriteString("\n---\n\n")

	for _, r := range suite.Results {
		statusEmoji := "✅"
		if r.Status == "failed" {
			statusEmoji = "❌"
		} else if r.Status == "skipped" {
			statusEmoji = "⏭️"
		}
		sb.WriteString(fmt.Sprintf("## %s %s (`%s`)\n\n", statusEmoji, r.Release, r.Chart))

		if len(r.Hooks) > 0 {
			sb.WriteString("| Hook | Phase | Duration |\n|------|-------|----------|\n")
			for _, h := range r.Hooks {
				phase := h.Phase
				if phase == "Succeeded" {
					phase = "✅ Succeeded"
				} else if phase == "Failed" {
					phase = "❌ Failed"
				}
				sb.WriteString(fmt.Sprintf("| %s | %s | %.1fs |\n",
					h.Name, phase, h.Duration.Seconds()))
			}
			sb.WriteString("\n")
		}

		if r.Error != "" {
			sb.WriteString("### Error\n\n")
			sb.WriteString("```\n" + r.Error + "\n```\n\n")
		}

		// Failure details with logs
		for _, h := range r.Hooks {
			if h.Phase == "Failed" && h.Logs != "" {
				sb.WriteString(fmt.Sprintf("### Pod Logs: %s\n\n", h.Name))
				sb.WriteString("```\n" + h.Logs + "\n```\n\n")
			}
		}
	}

	sb.WriteString("---\n\n")
	sb.WriteString(fmt.Sprintf("*Generated by `kates test helm` — %s*\n", suite.Timestamp.Format(time.RFC3339)))
	return sb.String()
}

func exportHelmTestPDF(suite HelmTestSuiteResult, filename string) error {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetAutoPageBreak(true, 15)

	// Title page
	pdf.AddPage()
	pdf.SetFont("Helvetica", "B", 24)
	pdf.SetTextColor(100, 80, 200)
	pdf.CellFormat(0, 14, "Kates Helm Test Report", "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 10)
	pdf.SetTextColor(120, 120, 120)
	pdf.CellFormat(0, 7, fmt.Sprintf("Cluster: %s", suite.Cluster), "", 1, "L", false, 0, "")
	pdf.CellFormat(0, 7, fmt.Sprintf("Generated: %s", suite.Timestamp.Format(time.RFC3339)), "", 1, "L", false, 0, "")
	pdf.CellFormat(0, 7, fmt.Sprintf("Duration: %.1fs", suite.Duration.Seconds()), "", 1, "L", false, 0, "")
	pdf.Ln(6)

	// Summary
	pdf.SetFont("Helvetica", "B", 14)
	pdf.SetTextColor(60, 60, 60)
	pdf.CellFormat(0, 10, "Summary", "", 1, "L", false, 0, "")

	colWidths := []float64{60, 30}
	summaryHeaders := []string{"Metric", "Count"}
	pdf.SetFont("Helvetica", "B", 9)
	pdf.SetFillColor(30, 41, 59)
	pdf.SetTextColor(196, 181, 253)
	for i, h := range summaryHeaders {
		pdf.CellFormat(colWidths[i], 7, h, "1", 0, "L", true, 0, "")
	}
	pdf.Ln(-1)

	pdf.SetFont("Helvetica", "", 9)
	pdf.SetTextColor(60, 60, 60)
	summaryRows := [][]string{
		{"Passed", fmt.Sprintf("%d", suite.Passed)},
		{"Failed", fmt.Sprintf("%d", suite.Failed)},
		{"Skipped", fmt.Sprintf("%d", suite.Skipped)},
	}
	for _, row := range summaryRows {
		for i, cell := range row {
			pdf.CellFormat(colWidths[i], 6, cell, "1", 0, "L", false, 0, "")
		}
		pdf.Ln(-1)
	}
	pdf.Ln(6)

	// Per-release results
	for _, r := range suite.Results {
		pdf.SetFont("Helvetica", "B", 12)
		pdf.SetTextColor(100, 80, 200)
		statusIcon := "[PASS]"
		if r.Status == "failed" {
			statusIcon = "[FAIL]"
		} else if r.Status == "skipped" {
			statusIcon = "[SKIP]"
		}
		pdf.CellFormat(0, 8, fmt.Sprintf("%s %s (%s)", statusIcon, r.Release, r.Chart), "", 1, "L", false, 0, "")

		if len(r.Hooks) > 0 {
			hookCols := []float64{80, 30, 30}
			hookHeaders := []string{"Hook", "Phase", "Duration"}
			pdf.SetFont("Helvetica", "B", 8)
			pdf.SetFillColor(30, 41, 59)
			pdf.SetTextColor(196, 181, 253)
			for i, h := range hookHeaders {
				pdf.CellFormat(hookCols[i], 7, h, "1", 0, "L", true, 0, "")
			}
			pdf.Ln(-1)

			pdf.SetFont("Helvetica", "", 8)
			for _, h := range r.Hooks {
				switch h.Phase {
				case "Succeeded":
					pdf.SetTextColor(16, 185, 129)
				case "Failed":
					pdf.SetTextColor(239, 68, 68)
				default:
					pdf.SetTextColor(60, 60, 60)
				}
				hookName := h.Name
				if len(hookName) > 45 {
					hookName = hookName[:44] + "..."
				}
				pdf.CellFormat(hookCols[0], 6, hookName, "1", 0, "L", false, 0, "")
				pdf.CellFormat(hookCols[1], 6, h.Phase, "1", 0, "L", false, 0, "")
				pdf.SetTextColor(60, 60, 60)
				pdf.CellFormat(hookCols[2], 6, fmt.Sprintf("%.1fs", h.Duration.Seconds()), "1", 0, "L", false, 0, "")
				pdf.Ln(-1)
			}
		}

		if r.Error != "" {
			pdf.Ln(2)
			pdf.SetFont("Helvetica", "", 8)
			pdf.SetTextColor(239, 68, 68)
			errText := r.Error
			if len(errText) > 200 {
				errText = errText[:197] + "..."
			}
			pdf.MultiCell(0, 5, "Error: "+errText, "", "L", false)
		}
		pdf.Ln(4)
	}

	// Footer
	pdf.SetFont("Helvetica", "I", 8)
	pdf.SetTextColor(150, 150, 150)
	pdf.CellFormat(0, 6, fmt.Sprintf("Generated by kates test helm — %s", suite.Timestamp.Format(time.RFC3339)), "", 1, "C", false, 0, "")

	return pdf.OutputFileAndClose(filename)
}

// ---------------------------------------------------------------------------
// Helm / kubectl helpers
// ---------------------------------------------------------------------------

func runHelmCmd(args ...string) (string, error) {
	cmd := exec.Command("helm", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	combined := stdout.String() + stderr.String()
	return strings.TrimSpace(combined), err
}

func currentKubeContext() string {
	args := []string{"config", "current-context"}
	if helmTestKubeconfig != "" {
		args = append([]string{"--kubeconfig", helmTestKubeconfig}, args...)
	}
	cmd := exec.Command("kubectl", args...)
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

// ---------------------------------------------------------------------------
// init
// ---------------------------------------------------------------------------

func init() {
	helmTestCmd.Flags().StringVarP(&helmTestNamespace, "namespace", "n", "kafka", "Kubernetes namespace to scan")
	helmTestCmd.Flags().StringVar(&helmTestTimeout, "timeout", "3m", "Per-release test timeout")
	helmTestCmd.Flags().BoolVarP(&helmTestVerbose, "verbose", "v", false, "Show full pod logs (always shown on failure)")
	helmTestCmd.Flags().StringVar(&helmTestExport, "export", "", "Export results: json, md, pdf")
	helmTestCmd.Flags().StringVar(&helmTestRelease, "release", "", "Override release name (skip auto-detection)")
	helmTestCmd.Flags().StringVar(&helmTestKubeconfig, "kubeconfig", "", "Path to kubeconfig file")
	testCmd.AddCommand(helmTestCmd)
}
