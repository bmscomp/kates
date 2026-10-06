package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
	"github.com/mattn/go-runewidth"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type ResilienceConfig struct {
	TestRequest struct {
		Type    string                 `yaml:"type" json:"type"`
		Backend string                 `yaml:"backend,omitempty" json:"backend,omitempty"`
		Spec    map[string]interface{} `yaml:"spec,omitempty" json:"spec,omitempty"`
	} `yaml:"testRequest" json:"testRequest"`

	ChaosSpec struct {
		ExperimentName   string            `yaml:"experimentName" json:"experimentName"`
		TargetNamespace  string            `yaml:"targetNamespace,omitempty" json:"targetNamespace,omitempty"`
		TargetLabel      string            `yaml:"targetLabel,omitempty" json:"targetLabel,omitempty"`
		TargetPod        string            `yaml:"targetPod,omitempty" json:"targetPod,omitempty"`
		TargetAll        bool              `yaml:"targetAll,omitempty" json:"targetAll,omitempty"`
		ChaosDurationSec int               `yaml:"chaosDurationSec,omitempty" json:"chaosDurationSec,omitempty"`
		DelayBeforeSec   int               `yaml:"delayBeforeSec,omitempty" json:"delayBeforeSec,omitempty"`
		DisruptionType   string            `yaml:"disruptionType,omitempty" json:"disruptionType,omitempty"`
		TargetBrokerId   int               `yaml:"targetBrokerId,omitempty" json:"targetBrokerId,omitempty"`
		NetworkLatencyMs int               `yaml:"networkLatencyMs,omitempty" json:"networkLatencyMs,omitempty"`
		FillPercentage   int               `yaml:"fillPercentage,omitempty" json:"fillPercentage,omitempty"`
		CpuCores         int               `yaml:"cpuCores,omitempty" json:"cpuCores,omitempty"`
		MemoryMb         int               `yaml:"memoryMb,omitempty" json:"memoryMb,omitempty"`
		IoWorkers        int               `yaml:"ioWorkers,omitempty" json:"ioWorkers,omitempty"`
		GracePeriodSec   int               `yaml:"gracePeriodSec,omitempty" json:"gracePeriodSec,omitempty"`
		TargetTopic      string            `yaml:"targetTopic,omitempty" json:"targetTopic,omitempty"`
		TargetPartition  int               `yaml:"targetPartition,omitempty" json:"targetPartition,omitempty"`
		EnvOverrides     map[string]string `yaml:"envOverrides,omitempty" json:"envOverrides,omitempty"`
	} `yaml:"chaosSpec" json:"chaosSpec"`

	SteadyStateSec     int `yaml:"steadyStateSec" json:"steadyStateSec"`
	MaxRecoveryWaitSec int `yaml:"maxRecoveryWaitSec,omitempty" json:"maxRecoveryWaitSec,omitempty"`

	Probes []struct {
		Name     string `yaml:"name" json:"name"`
		Type     string `yaml:"type" json:"type"`
		Endpoint string `yaml:"endpoint,omitempty" json:"endpoint,omitempty"`
		Command  string `yaml:"command,omitempty" json:"command,omitempty"`
	} `yaml:"probes,omitempty" json:"probes,omitempty"`
}

var resilienceFile string
var resilienceDryRun bool

var resilienceCmd = &cobra.Command{
	Use:   "resilience",
	Short: "Run combined performance + chaos resilience tests",
}

var resilienceRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Execute a resilience test from a YAML or JSON config file",
	Example: `  kates resilience run -f resilience-test.yaml
  kates resilience run -f resilience-test.json    # JSON still supported
  kates resilience run -f config.yaml --dry-run

  # Example resilience-test.yaml:
  testRequest:
    type: LOAD
    spec:
      numRecords: 100000
      numProducers: 2
      recordSize: 512

  chaosSpec:
    experimentName: kafka-broker-pod-kill
    targetNamespace: kafka
    targetLabel: "strimzi.io/component-type=kafka"
    chaosDurationSec: 30
    disruptionType: POD_KILL

  steadyStateSec: 30`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if resilienceFile == "" {
			return cmdErr("--file / -f is required (path to YAML or JSON file)")
		}

		data, err := os.ReadFile(resilienceFile)
		if err != nil {
			return cmdErr("Failed to read config file: " + err.Error())
		}

		var cfg ResilienceConfig
		if strings.HasSuffix(resilienceFile, ".json") {
			err = json.Unmarshal(data, &cfg)
		} else {
			err = yaml.Unmarshal(data, &cfg)
		}
		if err != nil {
			return cmdErr("Failed to parse config: " + err.Error())
		}

		if cfg.TestRequest.Type == "" {
			return cmdErr("testRequest.type is required")
		}
		if cfg.ChaosSpec.ExperimentName == "" {
			return cmdErr("chaosSpec.experimentName is required")
		}

		payload, _ := json.Marshal(cfg)
		var req interface{}
		json.Unmarshal(payload, &req)

		if resilienceDryRun {
			printDryRun("Would run resilience test", req)
			return nil
		}

		// Above a table only: ahead of -o json it left stdout unreadable as
		// JSON.
		if outputMode != "json" {
			fmt.Println(output.AccentStyle.Render("◉ Running resilience test..."))
		}

		result, err := apiClient.Resilience(context.Background(), req)
		if err != nil {
			errMsg := err.Error()
			if strings.Contains(errMsg, "EOF") || strings.Contains(errMsg, "connection refused") || strings.Contains(errMsg, "connection reset") {
				output.Warn("Connection error — common causes:")
				output.Hint("  • Backend not running: verify with 'kates health'")
				output.Hint("  • Port-forward not active: run 'kates ports'")
				output.Hint("  • API key missing: set with 'kates ctx set <name> --api-key <key>'")
				output.Hint(fmt.Sprintf("  • Current endpoint: %s", apiURL))
			}
			return cmdErr("Resilience test failed: " + errMsg)
		}

		if outputMode == "json" {
			printResilienceJSON(result.Raw)
			return nil
		}

		output.Header("Resilience Test Results")
		output.KeyValue("Status", output.StatusBadge(result.Status))
		if id := result.RunID(); id != "" {
			output.KeyValue("Test Run", output.Printable(id))
		}
		if result.Error != "" {
			output.KeyValue("Error", result.Error)
		}

		if chaos := result.ChaosOutcome; chaos != nil {
			output.SubHeader("Chaos Outcome")
			output.KeyValue("Experiment", chaos.ExperimentName)
			output.KeyValue("Verdict", output.StatusBadge(chaos.Verdict))
			output.KeyValue("Duration", chaos.ChaosDuration.String()+"s")
			if chaos.Phase != "" {
				output.KeyValue("Phase", chaos.Phase)
			}
			if chaos.FailStep != "" {
				output.KeyValue("Fail Step", chaos.FailStep)
			}
			if chaos.ProbeSuccess != "" {
				output.KeyValue("Probe Success", renderProbeGauge(chaos.ProbeSuccess))
			}
			if chaos.FailureReason != "" {
				output.KeyValue("Failure Reason", chaos.FailureReason)
			}
		}

		printResilienceProbes(result)

		if len(result.ImpactDeltas) > 0 {
			output.SubHeader("Impact Analysis (% change)")
			rows := make([][]string, 0, len(result.ImpactDeltas))
			for metric, v := range result.ImpactDeltas {
				marker := ""
				if v > 10 {
					marker = "▲"
				} else if v < -10 {
					marker = "▼"
				}
				rows = append(rows, []string{metric, fmt.Sprintf("%+.1f%%", v), marker})
			}
			output.Table([]string{"Metric", "Change", ""}, rows)
		}

		showSummary := func(label string, s *client.ReportSummary) {
			if s != nil {
				output.SubHeader(label)
				output.KeyValue("Throughput (rec/s)", fmt.Sprintf("%.1f", s.AvgThroughputRecPerSec))
				output.KeyValue("P99 Latency (ms)", fmt.Sprintf("%.2f", s.P99LatencyMs))
				output.KeyValue("Error Rate", fmt.Sprintf("%.4f%%", s.ErrorRate*100))
			}
		}
		showSummary("Pre-Chaos Baseline", result.PreChaosSummary)
		showSummary("Post-Chaos Impact", result.PostChaosSummary)

		if id := result.RunID(); id != "" {
			fmt.Fprintln(output.Out)
			output.Hint("Full details: kates test get " + output.Printable(id))
		}
		return nil
	},
}

// printResilienceJSON prints the report as the Kates API sent it, indented.
// It used to print the struct the CLI reads the report into, which held only
// what the table showed, so -o json dropped the recovery time, every probe
// result and the test run with its id. json.Indent also keeps each number as
// written and each <, > and &, which encoding the report again would escape.
func printResilienceJSON(raw json.RawMessage) {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		fmt.Fprintln(output.Out, string(raw))
		return
	}
	fmt.Fprintln(output.Out, buf.String())
}

// printResilienceProbes prints what the probes saw: the recovery time they
// measured, or that no poll found them all passing, how many of their
// evaluations passed before the fault, during it and after the recovery
// wait, and each probe that failed, with what it printed. During the fault
// only Continuous probes run, each of them many times, so a probe's row
// counts its failed evaluations out of its runs in that phase, and shows
// what the last failed one printed.
func printResilienceProbes(r *client.ResilienceResult) {
	if !r.RecoveryTime.Set && !r.UnrecoveredAfter.Set &&
		r.BaselineProbes == nil && r.DuringChaosProbes == nil && r.PostRecoveryProbes == nil {
		return
	}
	output.SubHeader("Probes and Recovery")
	switch {
	case r.RecoveryTime.Set:
		output.KeyValue("Recovery Time", fmt.Sprintf("%d ms", r.RecoveryTime.Millis))
	case r.UnrecoveredAfter.Set:
		output.KeyValue("Recovery Time", output.StatusBadge("NOT RECOVERED")+
			fmt.Sprintf(" %d ms after the fault", r.UnrecoveredAfter.Millis))
	}

	type probeRuns struct {
		phase, name, lastFailure string
		failed, runs             int
	}
	var failing []*probeRuns
	for _, phase := range []struct {
		name, none string
		results    []client.ProbeResult
	}{
		{"Baseline", "none ran", r.BaselineProbes},
		{"During the fault", "none ran: only Continuous probes run during the fault", r.DuringChaosProbes},
		{"After recovery", "none ran", r.PostRecoveryProbes},
	} {
		if phase.results == nil {
			continue
		}
		if len(phase.results) == 0 {
			output.KeyValue(phase.name, phase.none)
			continue
		}
		passed := 0
		var probes []*probeRuns
		byName := map[string]*probeRuns{}
		for _, res := range phase.results {
			probe := byName[res.Name]
			if probe == nil {
				probe = &probeRuns{phase: phase.name, name: res.Name}
				byName[res.Name] = probe
				probes = append(probes, probe)
			}
			probe.runs++
			if res.Passed {
				passed++
				continue
			}
			probe.failed++
			probe.lastFailure = res.Output
		}
		output.KeyValue(phase.name, fmt.Sprintf("%d/%d passed", passed, len(phase.results)))
		for _, probe := range probes {
			if probe.failed > 0 {
				failing = append(failing, probe)
			}
		}
	}
	if len(failing) == 0 {
		return
	}

	fmt.Fprintln(output.Out)
	width, tail := output.ColumnWidth(50, 30), "…"
	if output.ASCII() {
		tail = "..."
	}
	rows := make([][]string, 0, len(failing))
	for _, probe := range failing {
		printed := strings.TrimSpace(output.Printable(probe.lastFailure))
		if printed == "" {
			printed = "(no output)"
		}
		rows = append(rows, []string{
			probe.phase,
			output.Printable(probe.name),
			fmt.Sprintf("%d/%d", probe.failed, probe.runs),
			runewidth.Truncate(printed, width, tail),
		})
	}
	output.Table([]string{"Phase", "Probe", "Failed", "Output"}, rows)
}

func init() {
	resilienceRunCmd.Flags().StringVarP(&resilienceFile, "file", "f", "", "Path to resilience test config (YAML or JSON)")
	resilienceRunCmd.Flags().BoolVar(&resilienceDryRun, "dry-run", false, "Print parsed config without executing")

	resilienceCmd.AddCommand(resilienceRunCmd)
	rootCmd.AddCommand(resilienceCmd)
}
