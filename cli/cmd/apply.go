package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/klster/kates-cli/client"
	"github.com/klster/kates-cli/output"
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
	Example: `  kates test apply -f load-test.yaml
  kates test apply -f scenarios.yaml --wait

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

		output.Header(fmt.Sprintf("Applying %d scenario(s) from %s", len(sf.Scenarios), applyFile))
		fmt.Println()

		results := make([]scenarioResult, 0)

		for i, scenario := range sf.Scenarios {
			name := scenario.Name
			if name == "" {
				name = fmt.Sprintf("Scenario %d", i+1)
			}

			fmt.Printf("  %s %s (%s)...\n",
				output.AccentStyle.Render("▸"),
				output.LightStyle.Render(name),
				scenario.Type,
			)

			req := scenarioToRequest(scenario)
			result, err := apiClient.CreateTest(context.Background(), req)
			if err != nil {
				output.Error("  Failed: " + err.Error())
				results = append(results, scenarioResult{name: name, status: "FAILED", err: err.Error()})
				continue
			}

			output.Success(fmt.Sprintf("  Created: %s", truncID(result.ID)))

			if applyWait {
				finalResult, err := waitForTest(result.ID, name)
				if err != nil {
					results = append(results, scenarioResult{name: name, id: result.ID, status: "ERROR", err: err.Error()})
				} else {
					results = append(results, scenarioResult{name: name, id: result.ID, status: finalResult.Status, validate: scenario.Validate, testRun: finalResult})
				}
			} else {
				results = append(results, scenarioResult{name: name, id: result.ID, status: "SUBMITTED"})
			}
		}

		fmt.Println()
		output.SubHeader("Summary")
		rows := make([][]string, 0, len(results))
		hasViolation := false
		for _, r := range results {
			extra := ""
			if r.err != "" {
				extra = r.err
			} else if r.validate != nil && r.testRun != nil {
				violations := validateSLAs(r.testRun, r.validate)
				if len(violations) > 0 {
					extra = strings.Join(violations, "; ")
					hasViolation = true
				} else {
					extra = "✓ SLA Pass"
				}
			}
			rows = append(rows, []string{r.name, truncID(r.id), r.status, extra})
		}
		output.Table([]string{"Scenario", "ID", "Status", "Note"}, rows)

		if hasViolation {
			fmt.Println()
			output.Error("One or more SLA gates violated")
			os.Exit(1)
		}

		return nil
	},
}

type scenarioResult struct {
	name     string
	id       string
	status   string
	err      string
	validate *ValidationSpec
	testRun  *client.TestRun
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
			spec.DurationSeconds = toInt(v)
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
			spec.EnableIdempotence = toBool(v)
		}
		if v, ok := s.Spec["enableTransactions"]; ok {
			spec.EnableTransactions = toBool(v)
		}
		if v, ok := s.Spec["enableCrc"]; ok {
			spec.EnableCrc = toBool(v)
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
			if v.MaxRtoMs > 0 && ir.MaxRtoMs > v.MaxRtoMs {
				violations = append(violations, fmt.Sprintf("rto=%.0fms > %.0fms", ir.MaxRtoMs, v.MaxRtoMs))
			}
			if v.MaxRpoMs > 0 && ir.RpoMs > v.MaxRpoMs {
				violations = append(violations, fmt.Sprintf("rpo=%.0fms > %.0fms", ir.RpoMs, v.MaxRpoMs))
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

func waitForTest(id, name string) (*client.TestRun, error) {
	tick := 0
	staleRetries := 0
	for {
		result, err := apiClient.GetTest(context.Background(), id)
		if err != nil {
			return nil, err
		}
		status := strings.ToUpper(result.Status)
		switch status {
		case "DONE", "COMPLETED":
			if isStaleResult(result.Results) && staleRetries < maxStaleRetries {
				staleRetries++
				time.Sleep(2 * time.Second)
				continue
			}
			if isStaleResult(result.Results) {
				fmt.Printf("\r  %s %s → %s %s\n",
					output.WarningStyle.Render("⚠"),
					output.LightStyle.Render(name),
					output.StatusBadge(status),
					output.DimStyle.Render("(no data)"),
				)
			} else {
				fmt.Printf("\r  %s %s → %s\n",
					output.SuccessStyle.Render("✓"),
					output.LightStyle.Render(name),
					output.StatusBadge(status),
				)
			}
			return result, nil
		case "FAILED", "ERROR":
			fmt.Printf("\r  %s %s → %s\n",
				output.ErrorStyle.Render("✖"),
				output.LightStyle.Render(name),
				output.StatusBadge(status),
			)
			return result, nil
		default:
			fmt.Printf("\r  %s %s [%s]   ",
				spinnerFrame(tick),
				output.DimStyle.Render(name),
				output.AccentStyle.Render(status),
			)
			tick++
			time.Sleep(2 * time.Second)
		}
	}
}

func init() {
	testApplyCmd.Flags().StringVarP(&applyFile, "file", "f", "", "Path to scenario YAML/JSON file (required)")
	testApplyCmd.Flags().BoolVar(&applyWait, "wait", false, "Wait for each test to complete before starting next")
	testCmd.AddCommand(testApplyCmd)
}
