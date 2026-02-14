package cmd

import (
	"encoding/json"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

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
	MaxP99Latency float64 `yaml:"maxP99LatencyMs,omitempty" json:"maxP99LatencyMs,omitempty"`
	MaxAvgLatency float64 `yaml:"maxAvgLatencyMs,omitempty" json:"maxAvgLatencyMs,omitempty"`
	MinThroughput float64 `yaml:"minThroughputRecPerSec,omitempty" json:"minThroughputRecPerSec,omitempty"`
	MaxErrorRate  float64 `yaml:"maxErrorRate,omitempty" json:"maxErrorRate,omitempty"`
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
			// Try single scenario
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

			req := map[string]interface{}{"testType": strings.ToUpper(scenario.Type)}
			if scenario.Backend != "" {
				req["backend"] = scenario.Backend
			}
			if scenario.Spec != nil {
				req["spec"] = scenario.Spec
			}

			result, err := apiClient.CreateTest(context.Background(), req)
			if err != nil {
				output.Error("  Failed: " + err.Error())
				results = append(results, scenarioResult{name: name, status: "FAILED", err: err.Error()})
				continue
			}

			id := mapStr(result, "id")
			output.Success(fmt.Sprintf("  Created: %s", truncID(id)))

			if applyWait {
				finalResult, err := waitForTest(id, name)
				if err != nil {
					results = append(results, scenarioResult{name: name, id: id, status: "ERROR", err: err.Error()})
				} else {
					status := mapStr(finalResult, "status")
					results = append(results, scenarioResult{name: name, id: id, status: status, validate: scenario.Validate})
				}
			} else {
				results = append(results, scenarioResult{name: name, id: id, status: "SUBMITTED"})
			}
		}

		// Summary
		fmt.Println()
		output.SubHeader("Summary")
		rows := make([][]string, 0, len(results))
		for _, r := range results {
			extra := ""
			if r.err != "" {
				extra = r.err
			}
			rows = append(rows, []string{r.name, truncID(r.id), r.status, extra})
		}
		output.Table([]string{"Scenario", "ID", "Status", "Note"}, rows)

		return nil
	},
}

type scenarioResult struct {
	name     string
	id       string
	status   string
	err      string
	validate *ValidationSpec
}

func waitForTest(id, name string) (map[string]interface{}, error) {
	tick := 0
	for {
		result, err := apiClient.GetTest(context.Background(), id)
		if err != nil {
			return nil, err
		}
		status := strings.ToUpper(mapStr(result, "status"))
		switch status {
		case "DONE", "COMPLETED":
			fmt.Printf("\r  %s %s → %s\n",
				output.SuccessStyle.Render("✓"),
				output.LightStyle.Render(name),
				output.StatusBadge(status),
			)
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
