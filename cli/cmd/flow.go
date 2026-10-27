package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
	"github.com/bmscomp/kates/cli/pkg/theme"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type FlowPipeline struct {
	Name  string     `yaml:"name"`
	Steps []FlowStep `yaml:"steps"`
}

type FlowStep struct {
	Name     string           `yaml:"name"`
	Action   string           `yaml:"action"`
	Scenario string           `yaml:"scenario,omitempty"`
	Type     string           `yaml:"type,omitempty"`
	Records  int              `yaml:"records,omitempty"`
	Playbook string           `yaml:"playbook,omitempty"`
	Config   string           `yaml:"config,omitempty"`
	URL      string           `yaml:"url,omitempty"`
	Template string           `yaml:"template,omitempty"`
	Gate     *FlowGate        `yaml:"gate,omitempty"`
	Expect   *FlowExpect      `yaml:"expect,omitempty"`
	OnFail   string           `yaml:"onFail,omitempty"`
	Spec     *client.TestSpec `yaml:"spec,omitempty"`
}

// FlowGate is what a test step's run must meet. The numeric thresholds are
// pointers so that a gate can set maxErrorRate: 0, which a plain float could
// not tell from no gate at all.
type FlowGate struct {
	MinGrade      string   `yaml:"minGrade,omitempty"`
	MaxP99        *float64 `yaml:"maxP99,omitempty"`
	MinThroughput *float64 `yaml:"minThroughput,omitempty"`
	MaxErrorRate  *float64 `yaml:"maxErrorRate,omitempty"`
}

type FlowExpect struct {
	MaxP99DeltaPercent       float64 `yaml:"maxP99DeltaPercent,omitempty"`
	MaxThroughputDropPercent float64 `yaml:"maxThroughputDropPercent,omitempty"`
}

type FlowResult struct {
	Step    string
	Action  string
	Status  string
	TestID  string
	Grade   string
	Detail  string
	Elapsed time.Duration
}

var (
	flowFile string
	flowWait bool
	flowDry  bool

	flowTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(theme.OnDark).
			Background(theme.Accent).
			Padding(0, 1)

	flowStepStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(theme.Info)

	flowPassStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(theme.Success)

	flowFailStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(theme.Error)

	flowDimStyle = lipgloss.NewStyle().
			Foreground(theme.Muted)
)

var flowCmd = &cobra.Command{
	Use:   "flow",
	Short: "Declarative multi-step pipeline orchestrator",
	Long: `Run multi-step workflows that chain tests, chaos experiments,
quality gates, and notifications in a single YAML pipeline.

Each step runs sequentially. If a gate fails, the pipeline
stops and reports the failure.`,
}

var flowRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Execute a flow pipeline from a YAML file",
	Example: `  kates flow run -f release-qual.yaml
  kates flow run -f flow.yaml --wait=false
  kates flow run -f flow.yaml --dry-run`,
	RunE: func(cmd *cobra.Command, args []string) error {
		data, err := os.ReadFile(flowFile)
		if err != nil {
			return fmt.Errorf("cannot read flow file: %w", err)
		}

		data = []byte(os.ExpandEnv(string(data)))

		var pipeline FlowPipeline
		if err := yaml.Unmarshal(data, &pipeline); err != nil {
			return fmt.Errorf("invalid flow YAML: %w", err)
		}

		if len(pipeline.Steps) == 0 {
			return cmdErr("flow has no steps")
		}

		fmt.Println(flowTitleStyle.Width(60).Render(
			fmt.Sprintf("  ▸ FLOW  %s  (%d steps)", pipeline.Name, len(pipeline.Steps)),
		))
		fmt.Println()

		if flowDry {
			output.Warn("DRY RUN — no steps will be executed")
			fmt.Println()
			for i, step := range pipeline.Steps {
				fmt.Printf("  %s  %-20s  %s\n",
					flowDimStyle.Render(fmt.Sprintf("%d.", i+1)),
					flowStepStyle.Render(step.Name),
					flowDimStyle.Render(step.Action),
				)
				if step.Gate != nil {
					fmt.Printf("     %s %s\n", flowDimStyle.Render("gate:"), flowGateSummary(step.Gate))
				}
			}
			fmt.Println()
			output.Hint("Remove --dry-run to execute")
			return nil
		}

		var results []FlowResult
		failed := false

		for i, step := range pipeline.Steps {
			stepLabel := fmt.Sprintf("[%d/%d]", i+1, len(pipeline.Steps))
			fmt.Printf("  %s %s  %s\n",
				flowDimStyle.Render(stepLabel),
				flowStepStyle.Render(step.Name),
				flowDimStyle.Render("→ "+step.Action),
			)

			start := time.Now()
			result := FlowResult{Step: step.Name, Action: step.Action}

			switch step.Action {
			case "test":
				if step.Expect != nil {
					// An expectation that was never checked read as met.
					result.Status = "FAILED"
					result.Detail = "expect: not implemented in this version"
					break
				}
				testID, grade, summary, err := runFlowTest(step, flowWait)
				result.TestID = testID
				result.Grade = grade
				if err != nil {
					result.Status = "FAILED"
					result.Detail = err.Error()
				} else if !flowWait {
					result.Status = "PASSED"
					result.Detail = "started; not waited for (--wait=false)"
				} else if detail := flowGateFailure(step.Gate, grade, summary); detail != "" {
					result.Status = "GATE_FAILED"
					result.Detail = detail
				} else {
					result.Status = "PASSED"
				}

			case "wait":
				dur := 10 * time.Second
				if step.Records > 0 {
					dur = time.Duration(step.Records) * time.Second
				}
				fmt.Printf("     %s\n", flowDimStyle.Render(fmt.Sprintf("waiting %s…", dur)))
				time.Sleep(dur)
				result.Status = "PASSED"

			case "expect", "webhook", "notify":
				// These used to print that a notification went out and pass,
				// while sending nothing.
				result.Status = "FAILED"
				result.Detail = step.Action + ": not implemented in this version"

			default:
				result.Status = "SKIPPED"
				result.Detail = "unknown action: " + step.Action
			}

			result.Elapsed = time.Since(start)
			results = append(results, result)

			if result.Status == "PASSED" {
				fmt.Printf("     %s %s\n", flowPassStyle.Render("✓"), flowDimStyle.Render(result.Elapsed.Truncate(time.Second).String()))
			} else {
				fmt.Printf("     %s %s\n", flowFailStyle.Render("✖ "+result.Detail), flowDimStyle.Render(result.Elapsed.Truncate(time.Second).String()))
				failed = true
				if step.OnFail != "continue" {
					break
				}
			}
			fmt.Println()
		}

		printFlowSummary(pipeline.Name, results)

		if failed {
			return cmdErr("pipeline failed")
		}
		return nil
	},
}

// flowTestRequest is the request a test step starts: its spec, which the
// YAML gives under the request's own field names (numRecords, acks, ...),
// with records and type on the step taking over.
func flowTestRequest(step FlowStep) *client.CreateTestRequest {
	testType := step.Type
	if testType == "" {
		testType = "LOAD"
	}
	req := &client.CreateTestRequest{TestType: testType}
	if step.Spec != nil {
		spec := *step.Spec
		req.Spec = &spec
	} else {
		req.Spec = &client.TestSpec{}
	}
	if step.Records > 0 {
		req.Spec.Records = step.Records
	}
	return req
}

// runFlowTest starts a test step's run and, when wait is set, follows it to
// its end and returns its grade and report summary, which the gates judge.
func runFlowTest(step FlowStep, wait bool) (testID, grade string, summary *client.ReportSummary, err error) {
	run, err := apiClient.CreateTest(context.Background(), flowTestRequest(step))
	if err != nil {
		return "", "", nil, err
	}
	testID = run.ID
	if !wait {
		return testID, "", nil, nil
	}

	for i := 0; i < 180; i++ {
		time.Sleep(2 * time.Second)
		updated, err := apiClient.GetTest(context.Background(), run.ID)
		if err != nil {
			continue
		}
		status := strings.ToUpper(updated.Status)
		if status == "DONE" || status == "COMPLETED" {
			report, err := apiClient.Report(context.Background(), run.ID)
			if err == nil {
				summary = report.Summary
				if report.OverallSlaVerdict != nil {
					grade = report.OverallSlaVerdict.Grade
				}
			}
			return testID, grade, summary, nil
		}
		if status == "FAILED" || status == "ERROR" {
			return testID, "F", nil, fmt.Errorf("test failed")
		}
	}
	return testID, "", nil, fmt.Errorf("timeout waiting for test")
}

// flowGateFailure judges a finished run against its step's gate and returns
// what failed, naming the metric, or "" when the gate is met or there is
// none. The numeric thresholds used to be read from the YAML and never
// checked, so a step passed whatever its p99 or throughput was.
func flowGateFailure(gate *FlowGate, grade string, s *client.ReportSummary) string {
	if gate == nil {
		return ""
	}
	if gate.MinGrade != "" && !checkGrade(grade, gate.MinGrade) {
		return fmt.Sprintf("grade %s < %s", grade, gate.MinGrade)
	}
	if gate.MaxP99 == nil && gate.MinThroughput == nil && gate.MaxErrorRate == nil {
		return ""
	}
	if s == nil {
		return "no report summary to judge the gate's thresholds"
	}
	if gate.MaxP99 != nil {
		// A run that measured no latency reads a p99 of 0, the lowest there
		// is; it fails the gate as the backend's own report does.
		if !summaryMeasuredLatency(s) {
			return fmt.Sprintf("p99 not measured (maxP99 %.2fms)", *gate.MaxP99)
		}
		if s.P99LatencyMs > *gate.MaxP99 {
			return fmt.Sprintf("p99 %.2fms > maxP99 %.2fms", s.P99LatencyMs, *gate.MaxP99)
		}
	}
	if gate.MinThroughput != nil && s.AvgThroughputRecPerSec < *gate.MinThroughput {
		return fmt.Sprintf("throughput %.0f rec/s < minThroughput %.0f rec/s", s.AvgThroughputRecPerSec, *gate.MinThroughput)
	}
	if gate.MaxErrorRate != nil && s.ErrorRate > *gate.MaxErrorRate {
		return fmt.Sprintf("error rate %.4f > maxErrorRate %.4f", s.ErrorRate, *gate.MaxErrorRate)
	}
	return ""
}

// flowGradeOrder ranks the grades a gate may name. A grade not in it, the
// backend's or the gate's, fails the gate: both used to rank as 0, so an
// unknown grade met a gate of D and a gate on an unknown grade passed A.
var flowGradeOrder = map[string]int{"A+": 9, "A": 8, "A-": 7, "B+": 6, "B": 5, "B-": 4, "C+": 3, "C": 2, "C-": 1, "D": 0, "F": -1}

func checkGrade(actual, minimum string) bool {
	a, okA := flowGradeOrder[strings.ToUpper(strings.TrimSpace(actual))]
	m, okM := flowGradeOrder[strings.ToUpper(strings.TrimSpace(minimum))]
	return okA && okM && a >= m
}

// flowGateSummary names a gate's thresholds for the dry run.
func flowGateSummary(g *FlowGate) string {
	var parts []string
	if g.MinGrade != "" {
		parts = append(parts, "minGrade="+g.MinGrade)
	}
	if g.MaxP99 != nil {
		parts = append(parts, fmt.Sprintf("maxP99=%.2f", *g.MaxP99))
	}
	if g.MinThroughput != nil {
		parts = append(parts, fmt.Sprintf("minThroughput=%.0f", *g.MinThroughput))
	}
	if g.MaxErrorRate != nil {
		parts = append(parts, fmt.Sprintf("maxErrorRate=%.4f", *g.MaxErrorRate))
	}
	if len(parts) == 0 {
		return "(none)"
	}
	return strings.Join(parts, " ")
}

func printFlowSummary(name string, results []FlowResult) {
	fmt.Println(flowTitleStyle.Width(60).Render("  Pipeline Summary — " + name))
	fmt.Println()

	passed, failed, skipped := 0, 0, 0
	var totalTime time.Duration
	for _, r := range results {
		totalTime += r.Elapsed
		switch r.Status {
		case "PASSED":
			passed++
		case "SKIPPED":
			skipped++
		default:
			failed++
		}
	}

	for _, r := range results {
		var badge string
		switch r.Status {
		case "PASSED":
			badge = flowPassStyle.Render("✓ PASS")
		case "SKIPPED":
			badge = flowDimStyle.Render("○ SKIP")
		default:
			badge = flowFailStyle.Render("✖ FAIL")
		}
		detail := ""
		if r.Grade != "" {
			detail = flowDimStyle.Render(fmt.Sprintf(" (grade: %s)", r.Grade))
		}
		if r.TestID != "" {
			detail += flowDimStyle.Render(fmt.Sprintf(" [%s]", r.TestID))
		}
		fmt.Printf("  %s  %-20s %s%s\n", badge, r.Step, flowDimStyle.Render(r.Elapsed.Truncate(time.Second).String()), detail)
	}

	fmt.Println()
	verdict := flowPassStyle.Render("ALL PASSED")
	if failed > 0 {
		verdict = flowFailStyle.Render(fmt.Sprintf("%d FAILED", failed))
	}
	fmt.Printf("  %s  %s/%d passed  %s total\n\n",
		verdict,
		fmt.Sprintf("%d", passed),
		len(results),
		totalTime.Truncate(time.Second).String(),
	)
}

func init() {
	flowRunCmd.Flags().StringVarP(&flowFile, "file", "f", "", "Path to flow pipeline YAML (required)")
	flowRunCmd.Flags().BoolVar(&flowWait, "wait", true, "Wait for each test step's run to finish and judge its gate; --wait=false starts the run and moves on")
	flowRunCmd.Flags().BoolVar(&flowDry, "dry-run", false, "Validate pipeline without executing")
	flowRunCmd.MarkFlagRequired("file")
	flowCmd.AddCommand(flowRunCmd)
	rootCmd.AddCommand(flowCmd)
}
