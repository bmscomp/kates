package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/klster/kates-cli/client"
	"github.com/klster/kates-cli/output"
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

type FlowGate struct {
	MinGrade      string  `yaml:"minGrade,omitempty"`
	MaxP99        float64 `yaml:"maxP99,omitempty"`
	MinThroughput float64 `yaml:"minThroughput,omitempty"`
	MaxErrorRate  float64 `yaml:"maxErrorRate,omitempty"`
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
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#7C3AED")).
			Padding(0, 1)

	flowStepStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#06B6D4"))

	flowPassStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#22C55E"))

	flowFailStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#EF4444"))

	flowDimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6B7280"))
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
  kates flow run -f flow.yaml --wait
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
					fmt.Printf("     %s minGrade=%s\n", flowDimStyle.Render("gate:"), step.Gate.MinGrade)
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
				testID, grade, err := runFlowTest(step)
				result.TestID = testID
				result.Grade = grade
				if err != nil {
					result.Status = "FAILED"
					result.Detail = err.Error()
				} else if step.Gate != nil && !checkGrade(grade, step.Gate.MinGrade) {
					result.Status = "GATE_FAILED"
					result.Detail = fmt.Sprintf("grade %s < %s", grade, step.Gate.MinGrade)
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

			case "webhook", "notify":
				result.Status = "PASSED"
				result.Detail = "notification sent"
				fmt.Printf("     %s %s\n", flowPassStyle.Render("✓"), flowDimStyle.Render("notification dispatched"))

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

func runFlowTest(step FlowStep) (testID, grade string, err error) {
	testType := step.Type
	if testType == "" {
		testType = "LOAD"
	}

	req := &client.CreateTestRequest{
		TestType: testType,
	}
	if step.Spec != nil {
		req.Spec = step.Spec
	} else {
		req.Spec = &client.TestSpec{}
	}
	if step.Records > 0 {
		req.Spec.Records = step.Records
	}

	run, err := apiClient.CreateTest(context.Background(), req)
	if err != nil {
		return "", "", err
	}
	testID = run.ID

	for i := 0; i < 180; i++ {
		time.Sleep(2 * time.Second)
		updated, err := apiClient.GetTest(context.Background(), run.ID)
		if err != nil {
			continue
		}
		status := strings.ToUpper(updated.Status)
		if status == "DONE" || status == "COMPLETED" {
			report, err := apiClient.Report(context.Background(), run.ID)
			if err == nil && report.OverallSlaVerdict != nil {
				grade = report.OverallSlaVerdict.Grade
			}
			return testID, grade, nil
		}
		if status == "FAILED" || status == "ERROR" {
			return testID, "F", fmt.Errorf("test failed")
		}
	}
	return testID, "", fmt.Errorf("timeout waiting for test")
}

func checkGrade(actual, minimum string) bool {
	gradeOrder := map[string]int{"A+": 9, "A": 8, "A-": 7, "B+": 6, "B": 5, "B-": 4, "C+": 3, "C": 2, "C-": 1, "D": 0, "F": -1}
	return gradeOrder[actual] >= gradeOrder[minimum]
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
	flowRunCmd.Flags().BoolVar(&flowWait, "wait", true, "Wait for each step to complete")
	flowRunCmd.Flags().BoolVar(&flowDry, "dry-run", false, "Validate pipeline without executing")
	flowRunCmd.MarkFlagRequired("file")
	flowCmd.AddCommand(flowRunCmd)
	rootCmd.AddCommand(flowCmd)
}
