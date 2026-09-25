package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type TestScenario struct {
	Name     string                 `yaml:"name" json:"name"`
	Type     string                 `yaml:"type" json:"type"`
	Backend  string                 `yaml:"backend,omitempty" json:"backend,omitempty"`
	Spec     map[string]interface{} `yaml:"spec,omitempty" json:"spec,omitempty"`
	Validate *ValidationSpec        `yaml:"validate,omitempty" json:"validate,omitempty"`
}

type ValidationSpec struct {
	MaxP99Latency  float64 `yaml:"maxP99LatencyMs,omitempty" json:"maxP99LatencyMs,omitempty"`
	MaxAvgLatency  float64 `yaml:"maxAvgLatencyMs,omitempty" json:"maxAvgLatencyMs,omitempty"`
	MinThroughput  float64 `yaml:"minThroughputRecPerSec,omitempty" json:"minThroughputRecPerSec,omitempty"`
	MaxErrorRate   float64 `yaml:"maxErrorRate,omitempty" json:"maxErrorRate,omitempty"`
	MaxDataLoss    float64 `yaml:"maxDataLossPercent,omitempty" json:"maxDataLossPercent,omitempty"`
	MaxRtoMs       float64 `yaml:"maxRtoMs,omitempty" json:"maxRtoMs,omitempty"`
	MaxRpoMs       float64 `yaml:"maxRpoMs,omitempty" json:"maxRpoMs,omitempty"`
	MaxOutOfOrder  int64   `yaml:"maxOutOfOrder,omitempty" json:"maxOutOfOrder,omitempty"`
	MaxCrcFailures int64   `yaml:"maxCrcFailures,omitempty" json:"maxCrcFailures,omitempty"`
}

type ScenarioFile struct {
	Scenarios []TestScenario `yaml:"scenarios" json:"scenarios"`
}

var (
	applyFile string
	applyWait bool
)

var testApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Run tests from a YAML/JSON scenario file",
	Long: `Submit each scenario in a YAML or JSON file as a test run. With --wait it
waits for each run to finish before the next and checks the SLA gates in its
validate block.

In a terminal --wait shows a spinner. Without one (a pipe, a CI job, an agent's
shell), or with --plain, it prints a plain line to stderr each time a run's
status changes instead. With -o json it prints nothing but the summary, as
JSON on stdout. The exit code is the same in every mode.`,
	Example: `  kates test apply -f load-test.yaml
  kates test apply -f scenarios.yaml --wait
  kates test apply -f scenarios.yaml --wait -o json

  # Example scenario file (load-test.yaml):
  scenarios:
    - name: "Quick Load Test"
      type: LOAD
      spec:
        records: 100000
        parallelProducers: 2
      validate:
        maxP99LatencyMs: 50
        minThroughputRecPerSec: 10000`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if applyFile == "" {
			return cmdErr("--file is required. Provide a YAML or JSON scenario file.")
		}

		data, err := os.ReadFile(applyFile)
		if err != nil {
			return cmdErr("Failed to read file: " + err.Error())
		}

		var sf ScenarioFile
		if strings.HasSuffix(applyFile, ".json") {
			err = json.Unmarshal(data, &sf)
		} else {
			err = yaml.Unmarshal(data, &sf)
		}
		if err != nil {
			var single TestScenario
			if yaml.Unmarshal(data, &single) == nil && single.Type != "" {
				sf.Scenarios = []TestScenario{single}
			} else {
				return cmdErr("Invalid scenario file: " + err.Error())
			}
		}

		if len(sf.Scenarios) == 0 {
			return cmdErr("No scenarios found in file")
		}
		// Checked before the first test starts, so a file with a mistake
		// runs none of its tests rather than the ones above it.
		var problems []string
		for i, scenario := range sf.Scenarios {
			for _, p := range scenarioSpecProblems(scenario) {
				problems = append(problems, fmt.Sprintf("scenario %d (%s): %s", i+1, scenario.Name, p))
			}
		}
		if len(problems) > 0 {
			return cmdErr("Invalid scenario file: " + strings.Join(problems, "; "))
		}

		jsonOut := outputMode == "json"
		// The spinner needs a terminal on stdin and stdout. Without one,
		// Bubble Tea fails to open /dev/tty and every --wait scenario ended
		// as ERROR although its run went on; and under -o json its frames
		// would land in the JSON.
		useTUI := IsInteractive() && !jsonOut
		var progress io.Writer
		if !jsonOut {
			progress = output.Err
			output.Header(fmt.Sprintf("Applying %d scenario(s) from %s", len(sf.Scenarios), applyFile))
			fmt.Println()
		}

		res := applyResult{File: applyFile, Waited: applyWait, Scenarios: make([]applyScenarioResult, 0, len(sf.Scenarios))}

		for i, scenario := range sf.Scenarios {
			name := scenario.Name
			if name == "" {
				name = fmt.Sprintf("Scenario %d", i+1)
			}

			if !jsonOut {
				fmt.Printf("  %s %s (%s)...\n",
					output.AccentStyle.Render("▸"),
					output.LightStyle.Render(name),
					scenario.Type,
				)
			}

			sr := applyScenarioResult{Name: name, Type: strings.ToUpper(scenario.Type)}
			req := scenarioToRequest(scenario)
			result, err := apiClient.CreateTest(context.Background(), req)
			if err != nil {
				if !jsonOut {
					output.Error("  Failed: " + err.Error())
				}
				sr.Status, sr.Error = "FAILED", err.Error()
				res.Scenarios = append(res.Scenarios, sr)
				continue
			}
			sr.RunID = result.ID

			if !jsonOut {
				output.Success(fmt.Sprintf("  Created: %s", truncID(result.ID)))
			}

			if !applyWait {
				sr.Status = "SUBMITTED"
				res.Scenarios = append(res.Scenarios, sr)
				continue
			}

			var finalResult *client.TestRun
			if useTUI {
				finalResult, err = waitForTestTUI(result.ID, name)
			} else {
				finalResult, err = waitForTestPlain(context.Background(), result.ID, name, progress)
			}
			if err != nil {
				sr.Status, sr.Error = "ERROR", err.Error()
			} else {
				sr.Status = finalResult.Status
				if scenario.Validate != nil {
					sr.SLA = &applySLAResult{
						Violations:   nonNil(validateSLAs(finalResult, scenario.Validate)),
						NotEvaluable: unevaluableSLAs(finalResult, scenario.Validate),
					}
				}
			}
			res.Scenarios = append(res.Scenarios, sr)
		}

		hasViolation := false
		hasUnevaluable := false
		hasFailure := false
		for _, r := range res.Scenarios {
			if r.SLA != nil && len(r.SLA.Violations) > 0 {
				hasViolation = true
			}
			if r.SLA != nil && len(r.SLA.NotEvaluable) > 0 {
				hasUnevaluable = true
			}
			if r.Error != "" || isFailedStatus(strings.ToUpper(r.Status)) {
				hasFailure = true
			}
		}

		if jsonOut {
			output.JSON(res)
		} else {
			renderApplySummary(res, hasUnevaluable)
		}

		if hasViolation {
			// A silentErr instead of os.Exit(1): the same exit code, and a
			// test can run the command without it ending the test binary.
			if !jsonOut {
				fmt.Println()
			}
			return cmdErr("One or more SLA gates violated")
		}
		// A FAILED scenario is a failed run even without SLA gates. Only SLA
		// violations used to set the exit code, so a batch whose tests crashed
		// outright still exited 0.
		if hasFailure {
			return cmdErr("one or more scenarios failed")
		}

		return nil
	},
}

// applyResult is what kates test apply did with a scenario file: the summary
// table shows it and -o json prints it as is.
type applyResult struct {
	File      string                `json:"file"`
	Waited    bool                  `json:"waited"`
	Scenarios []applyScenarioResult `json:"scenarios"`
}

// applyScenarioResult is one scenario's row. Status is SUBMITTED without
// --wait, the run's final status with it, FAILED (no RunID) when the run
// could not be created and ERROR when waiting failed; Error then says why.
type applyScenarioResult struct {
	Name   string          `json:"name"`
	Type   string          `json:"type"`
	RunID  string          `json:"runId,omitempty"`
	Status string          `json:"status"`
	Error  string          `json:"error,omitempty"`
	SLA    *applySLAResult `json:"sla,omitempty"`
}

// applySLAResult grades a finished run against its scenario's validate
// block. NotEvaluable names gates the run produced no measurement for: they
// neither pass nor fail, and do not change the exit code.
type applySLAResult struct {
	Violations   []string `json:"violations"`
	NotEvaluable []string `json:"notEvaluable,omitempty"`
}

func renderApplySummary(res applyResult, hasUnevaluable bool) {
	fmt.Println()
	output.SubHeader("Summary")
	rows := make([][]string, 0, len(res.Scenarios))
	for _, r := range res.Scenarios {
		extra := ""
		if r.Error != "" {
			extra = r.Error
		} else if r.SLA != nil {
			notes := append([]string{}, r.SLA.Violations...)
			if len(r.SLA.NotEvaluable) > 0 {
				notes = append(notes, "not evaluable: "+strings.Join(r.SLA.NotEvaluable, ", "))
			}
			if len(notes) > 0 {
				extra = strings.Join(notes, "; ")
			} else {
				extra = "✓ SLA Pass"
			}
		}
		rows = append(rows, []string{r.Name, truncID(r.RunID), r.Status, extra})
	}
	output.Table([]string{"Scenario", "ID", "Status", "Note"}, rows)

	if hasUnevaluable {
		fmt.Println()
		output.Warn("Some SLA gates were not evaluated: the run did not measure what they check")
	}
}

// nonNil keeps an empty list a list in JSON, so "no violations" reads as []
// rather than null.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func scenarioToRequest(s TestScenario) *client.CreateTestRequest {
	req := &client.CreateTestRequest{
		TestType: strings.ToUpper(s.Type),
		Backend:  s.Backend,
	}
	if s.Spec != nil {
		spec := &client.TestSpec{}
		if v, ok := s.Spec["records"]; ok {
			spec.Records = toInt(v)
		}
		if v, ok := s.Spec["parallelProducers"]; ok {
			spec.ParallelProducers = toInt(v)
		}
		if v, ok := s.Spec["recordSizeBytes"]; ok {
			spec.RecordSizeBytes = toInt(v)
		}
		if v, ok := s.Spec["durationSeconds"]; ok {
			// Scenario files declare SECONDS; the wire field is milliseconds.
			// Without the conversion a 300-second scenario ran for 300ms —
			// every "passing" scenario test finished 1000x early.
			spec.DurationMs = toInt(v) * 1000
		}
		if v, ok := s.Spec["topic"]; ok {
			spec.Topic = fmt.Sprintf("%v", v)
		}
		if v, ok := s.Spec["acks"]; ok {
			spec.Acks = fmt.Sprintf("%v", v)
		}
		if v, ok := s.Spec["batchSize"]; ok {
			spec.BatchSize = toInt(v)
		}
		if v, ok := s.Spec["lingerMs"]; ok {
			spec.LingerMs = toInt(v)
		}
		if v, ok := s.Spec["compressionType"]; ok {
			spec.CompressionType = fmt.Sprintf("%v", v)
		}
		if v, ok := s.Spec["numConsumers"]; ok {
			spec.NumConsumers = toInt(v)
		}
		if v, ok := s.Spec["replicationFactor"]; ok {
			spec.ReplicationFactor = toInt(v)
		}
		if v, ok := s.Spec["partitions"]; ok {
			spec.Partitions = toInt(v)
		}
		if v, ok := s.Spec["minInsyncReplicas"]; ok {
			spec.MinInsyncReplicas = toInt(v)
		}
		if v, ok := s.Spec["consumerGroup"]; ok {
			spec.ConsumerGroup = fmt.Sprintf("%v", v)
		}
		if v, ok := s.Spec["targetThroughput"]; ok {
			spec.TargetThroughput = toInt(v)
		}
		if v, ok := s.Spec["fetchMinBytes"]; ok {
			spec.FetchMinBytes = toInt(v)
		}
		if v, ok := s.Spec["fetchMaxWaitMs"]; ok {
			spec.FetchMaxWaitMs = toInt(v)
		}
		if v, ok := s.Spec["enableIdempotence"]; ok {
			spec.EnableIdempotence = toBoolPtr(v)
		}
		if v, ok := s.Spec["enableTransactions"]; ok {
			spec.EnableTransactions = toBoolPtr(v)
		}
		if v, ok := s.Spec["enableCrc"]; ok {
			spec.EnableCrc = toBoolPtr(v)
		}
		req.Spec = spec
	}
	return req
}

func toInt(v interface{}) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	default:
		return 0
	}
}

func toBool(v interface{}) bool {
	switch b := v.(type) {
	case bool:
		return b
	case string:
		return b == "true"
	default:
		return false
	}
}

// toBoolPtr reads a key the file sets as true or false, so that false is sent
// too. Anything else is nil and sends nothing: yes, on, or a bare key used to
// become an explicit false, which turned CRC checks or the producer's
// idempotence off. scenarioSpecProblems refuses such a file before anything
// is sent.
func toBoolPtr(v interface{}) *bool {
	switch b := v.(type) {
	case bool:
		return &b
	case string:
		if b == "true" || b == "false" {
			t := b == "true"
			return &t
		}
	}
	return nil
}

// scenarioBoolKeys are the spec keys scenarioToRequest sends as true or false.
var scenarioBoolKeys = []string{"enableIdempotence", "enableTransactions", "enableCrc"}

// scenarioSpecProblems names the spec keys of a scenario that
// scenarioToRequest cannot send as written.
func scenarioSpecProblems(s TestScenario) []string {
	var problems []string
	for _, key := range scenarioBoolKeys {
		if v, ok := s.Spec[key]; ok && toBoolPtr(v) == nil {
			problems = append(problems, fmt.Sprintf("spec.%s is %v; write true or false", key, v))
		}
	}
	return problems
}

func validateSLAs(run *client.TestRun, v *ValidationSpec) []string {
	var violations []string

	for _, r := range run.Results {
		if v.MaxP99Latency > 0 && r.P99LatencyMs > v.MaxP99Latency {
			violations = append(violations, fmt.Sprintf("p99=%.0fms > %.0fms", r.P99LatencyMs, v.MaxP99Latency))
		}
		if v.MaxAvgLatency > 0 && r.AvgLatencyMs > v.MaxAvgLatency {
			violations = append(violations, fmt.Sprintf("avg=%.0fms > %.0fms", r.AvgLatencyMs, v.MaxAvgLatency))
		}
		if v.MinThroughput > 0 && r.ThroughputRecordsPerSec < v.MinThroughput {
			violations = append(violations, fmt.Sprintf("throughput=%.0f < %.0f rec/s", r.ThroughputRecordsPerSec, v.MinThroughput))
		}

		if r.Integrity != nil {
			ir := r.Integrity
			if v.MaxDataLoss >= 0 && ir.DataLossPercent > v.MaxDataLoss {
				violations = append(violations, fmt.Sprintf("dataLoss=%.4f%% > %.4f%%", ir.DataLossPercent, v.MaxDataLoss))
			}
			// An unmeasured RTO/RPO is skipped here, not passed: unevaluableSLAs
			// reports the gate so the summary never shows it as green.
			if rto, ok := ir.MeasuredMaxRtoMs(); ok && v.MaxRtoMs > 0 && rto > v.MaxRtoMs {
				violations = append(violations, fmt.Sprintf("rto=%.0fms > %.0fms", rto, v.MaxRtoMs))
			}
			if rpo, ok := ir.MeasuredRpoMs(); ok && v.MaxRpoMs > 0 && rpo > v.MaxRpoMs {
				violations = append(violations, fmt.Sprintf("rpo=%.0fms > %.0fms", rpo, v.MaxRpoMs))
			}
			if v.MaxOutOfOrder >= 0 && ir.OutOfOrderCount > v.MaxOutOfOrder {
				violations = append(violations, fmt.Sprintf("outOfOrder=%d > %d", ir.OutOfOrderCount, v.MaxOutOfOrder))
			}
			if v.MaxCrcFailures >= 0 && ir.CrcFailures > v.MaxCrcFailures {
				violations = append(violations, fmt.Sprintf("crcFail=%d > %d", ir.CrcFailures, v.MaxCrcFailures))
			}
		}
	}

	return violations
}

// unevaluableSLAs names the declared RTO/RPO gates the run produced no
// measurement for. Those gates cannot fail, so reporting them as passed would
// claim a check that never happened.
func unevaluableSLAs(run *client.TestRun, v *ValidationSpec) []string {
	rtoMeasured, rpoMeasured := false, false
	for _, r := range run.Results {
		if r.Integrity == nil {
			continue
		}
		if _, ok := r.Integrity.MeasuredMaxRtoMs(); ok {
			rtoMeasured = true
		}
		if _, ok := r.Integrity.MeasuredRpoMs(); ok {
			rpoMeasured = true
		}
	}

	var gates []string
	if v.MaxRtoMs > 0 && !rtoMeasured {
		gates = append(gates, "maxRtoMs (RTO not measured)")
	}
	if v.MaxRpoMs > 0 && !rpoMeasured {
		gates = append(gates, "maxRpoMs (RPO not measured)")
	}
	return gates
}

type waitModel struct {
	id     string
	name   string
	spin   spinner.Model
	status string
	result *client.TestRun
	err    error
}

type waitResultMsg struct {
	test *client.TestRun
	err  error
}

func (m waitModel) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, m.fetchTest())
}

// applyPollInterval is how often test apply --wait asks for a run's status.
// Tests shorten it.
var applyPollInterval = 2 * time.Second

func (m waitModel) fetchTest() tea.Cmd {
	return func() tea.Msg {
		time.Sleep(applyPollInterval)
		res, err := apiClient.GetTest(context.Background(), m.id)
		return waitResultMsg{test: res, err: err}
	}
}

func (m waitModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" || msg.String() == "q" {
			m.err = fmt.Errorf("aborted")
			return m, tea.Quit
		}
	case waitResultMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, tea.Quit
		}
		m.result = msg.test
		m.status = strings.ToUpper(msg.test.Status)
		if m.status == "DONE" || m.status == "COMPLETED" || m.status == "FAILED" || m.status == "ERROR" {
			return m, tea.Quit
		}
		return m, m.fetchTest()
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m waitModel) View() string {
	if m.result != nil {
		if m.status == "DONE" || m.status == "COMPLETED" {
			return fmt.Sprintf("  %s %s → %s\n", output.SuccessStyle.Render("✓"), output.LightStyle.Render(m.name), output.StatusBadge(m.status))
		}
		if m.status == "FAILED" || m.status == "ERROR" {
			return fmt.Sprintf("  %s %s → %s\n", output.ErrorStyle.Render("✖"), output.LightStyle.Render(m.name), output.StatusBadge(m.status))
		}
	}
	return fmt.Sprintf("  %s %s [%s]", m.spin.View(), output.DimStyle.Render(m.name), output.AccentStyle.Render(m.status))
}

// waitForTestTUI is the seam tests use to see which way apply waits without
// starting a Bubble Tea program. It holds waitForTest by default.
var waitForTestTUI = waitForTest

func waitForTest(id, name string) (*client.TestRun, error) {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = output.AccentStyle

	m := waitModel{
		id:     id,
		name:   name,
		spin:   s,
		status: "WAITING",
	}

	p := tea.NewProgram(m)
	finalModel, err := p.Run()
	if err != nil {
		return nil, err
	}

	wm := finalModel.(waitModel)
	if wm.err != nil {
		return nil, wm.err
	}
	return wm.result, nil
}

// waitForTestPlain is waitForTest without a terminal. It polls as the spinner
// does, ends on the same statuses, and writes a plain line to progress each
// time the status changes; progress is nil under -o json, where stdout carries
// the result and nothing else.
func waitForTestPlain(ctx context.Context, id, name string, progress io.Writer) (*client.TestRun, error) {
	last := ""
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(applyPollInterval):
		}
		run, err := apiClient.GetTest(ctx, id)
		if err != nil {
			return nil, err
		}
		status := strings.ToUpper(run.Status)
		if progress != nil && status != last {
			fmt.Fprintf(progress, "  %s (%s): %s\n", name, id, status)
			last = status
		}
		switch status {
		case "DONE", "COMPLETED", "FAILED", "ERROR":
			return run, nil
		}
	}
}

func init() {
	testApplyCmd.Flags().StringVarP(&applyFile, "file", "f", "", "Path to scenario YAML/JSON file (required)")
	testApplyCmd.Flags().BoolVar(&applyWait, "wait", false, "Wait for each test to complete before starting next")
	testCmd.AddCommand(testApplyCmd)
}
