package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
	"github.com/mattn/go-runewidth"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// parseResilienceFile reads a resilience file as the body of POST
// /api/resilience, every field as the file has it, and kates resilience run
// sends it so. Decoded into a struct, the file lost every field the struct
// lacked: a probe's mode, expectedOutput, comparator, intervalSec and
// timeoutSec, and testRequest.scenario. The Kates API then ran on its
// defaults, so each probe was an Edge probe that passed whatever its
// command printed (comparator contains, expectedOutput ""). The struct also
// dropped every 0 an omitempty field held, so gracePeriodSec: 0 became the
// API's 30 and targetBrokerId: 0 a random broker, and it sent steadyStateSec
// 0 for a file that had none, where the API's default is 30 seconds.
//
// A .json file keeps its numbers as written. A value JSON can't hold, such as
// YAML's .inf, is an error here rather than when the request is sent.
func parseResilienceFile(path string, data []byte) (map[string]any, error) {
	var cfg map[string]any
	var err error
	if strings.HasSuffix(path, ".json") {
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		err = dec.Decode(&cfg)
		// Decode stops after one value, where Unmarshal refused what follows.
		if _, rest := dec.Token(); err == nil && rest != io.EOF {
			err = errors.New("invalid data after the top-level JSON value")
		}
	} else {
		err = yaml.Unmarshal(data, &cfg)
	}
	if err != nil {
		return nil, err
	}
	if _, err := json.Marshal(cfg); err != nil {
		return nil, fmt.Errorf("it doesn't convert to JSON: %w", err)
	}
	return cfg, nil
}

// checkResilienceFile refuses a file without the fields every resilience run
// needs. A scenario may carry the test's type itself: the Kates API takes
// testRequest.scenario.type in place of testRequest.type.
func checkResilienceFile(cfg map[string]any) error {
	test, _ := cfg["testRequest"].(map[string]any)
	scenario, _ := test["scenario"].(map[string]any)
	if mapStrEmpty(test, "type") == "" && mapStrEmpty(scenario, "type") == "" {
		return errors.New("testRequest.type is required")
	}
	chaos, _ := cfg["chaosSpec"].(map[string]any)
	if mapStrEmpty(chaos, "experimentName") == "" {
		return errors.New("chaosSpec.experimentName is required")
	}
	return nil
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
	Long: `Run a test and inject one fault while it runs. The file is the body of
POST /api/resilience, sent as written, so its keys are the Kates API's field
names: the API ignores a field it doesn't have, and gives each field the file
leaves out its default. --dry-run prints the body.`,
	Example: `  kates resilience run -f resilience-test.yaml
  kates resilience run -f resilience-test.json    # JSON still supported
  kates resilience run -f config.yaml --dry-run

  # Example resilience-test.yaml:
  testRequest:
    type: LOAD
    spec:
      numRecords: 180000     # at 500 records/s: 360 s of load
      throughput: 500
      recordSize: 1024
      acks: all

  chaosSpec:
    experimentName: kafka-pod-kill
    disruptionType: POD_KILL
    targetNamespace: kafka
    targetLabel: "strimzi.io/component-type=kafka,strimzi.io/broker-role=true"
    chaosDurationSec: 30

  steadyStateSec: 30`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if resilienceFile == "" {
			return cmdErr("--file / -f is required (path to YAML or JSON file)")
		}

		data, err := os.ReadFile(resilienceFile)
		if err != nil {
			return cmdErr("Failed to read config file: " + err.Error())
		}

		cfg, err := parseResilienceFile(resilienceFile, data)
		if err != nil {
			return cmdErr("Failed to parse config: " + err.Error())
		}
		if err := checkResilienceFile(cfg); err != nil {
			return cmdErr(err.Error())
		}

		if resilienceDryRun {
			printDryRun("Would run resilience test", cfg)
			return nil
		}

		// Above a table only: ahead of -o json it left stdout unreadable as
		// JSON.
		if outputMode != "json" {
			fmt.Println(output.AccentStyle.Render("◉ Running resilience test..."))
		}

		result, err := apiClient.Resilience(context.Background(), cfg)
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
	resilienceRunCmd.Flags().BoolVar(&resilienceDryRun, "dry-run", false, "Print the request body without sending it")

	resilienceCmd.AddCommand(resilienceRunCmd)
	rootCmd.AddCommand(resilienceCmd)
}
