package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/klster/kates-cli/client"
	"github.com/klster/kates-cli/output"
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

		fmt.Println(output.AccentStyle.Render("◉ Running resilience test..."))

		result, err := apiClient.Resilience(context.Background(), req)
		if err != nil {
			return cmdErr("Resilience test failed: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(result)
			return nil
		}

		output.Header("Resilience Test Results")
		output.KeyValue("Status", output.StatusBadge(result.Status))

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

		return nil
	},
}

func init() {
	resilienceRunCmd.Flags().StringVarP(&resilienceFile, "file", "f", "", "Path to resilience test config (YAML or JSON)")
	resilienceRunCmd.Flags().BoolVar(&resilienceDryRun, "dry-run", false, "Print parsed config without executing")

	resilienceCmd.AddCommand(resilienceRunCmd)
	rootCmd.AddCommand(resilienceCmd)
}
