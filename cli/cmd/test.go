package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var (
	testTypeFlag   string
	testStatusFlag string
	testPageFlag   int
	testSizeFlag   int
)

var testCmd = &cobra.Command{
	Use:     "test",
	Aliases: []string{"t"},
	Short:   "Manage performance test runs",
}

var testListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List test runs with optional filters",
	Example: `  kates test list
  kates test list --type LOAD --status DONE
  kates test list --page 0 --size 10`,
	RunE: func(cmd *cobra.Command, args []string) error {
		data, err := apiClient.ListTests(testTypeFlag, testStatusFlag, testPageFlag, testSizeFlag)
		if err != nil {
			output.Error("Failed to list tests: " + err.Error())
			return nil
		}

		if outputMode == "json" {
			output.RawJSON(data)
			return nil
		}

		var paged struct {
			Content    []map[string]interface{} `json:"content"`
			Page       int                      `json:"page"`
			Size       int                      `json:"size"`
			TotalItems int                      `json:"totalItems"`
			TotalPages int                      `json:"totalPages"`
		}
		if err := json.Unmarshal(data, &paged); err != nil {
			output.RawJSON(data)
			return nil
		}

		output.Header("Test Runs")
		if len(paged.Content) == 0 {
			output.Hint("No test runs found.")
			output.Hint("Start one with: kates test create --type LOAD --records 100000")
			return nil
		}

		rows := make([][]string, 0, len(paged.Content))
		for _, run := range paged.Content {
			id := truncID(valStr(run, "id"))
			testType := valStr(run, "testType")
			status := valStr(run, "status")
			backend := valStr(run, "backend")
			created := formatTime(valStr(run, "createdAt"))
			rows = append(rows, []string{id, testType, status, backend, created})
		}
		output.Table([]string{"ID", "Type", "Status", "Backend", "Created"}, rows)

		totalPages := paged.TotalPages
		if totalPages == 0 && paged.TotalItems > 0 {
			totalPages = (paged.TotalItems + paged.Size - 1) / paged.Size
		}
		output.Hint(fmt.Sprintf("Page %d of %d · %d total runs", paged.Page+1, totalPages, paged.TotalItems))
		return nil
	},
}

var testGetCmd = &cobra.Command{
	Use:     "get <id>",
	Aliases: []string{"show", "inspect"},
	Short:   "Show details of a specific test run",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := apiClient.GetTest(args[0])
		if err != nil {
			output.Error("Test not found: " + err.Error())
			return nil
		}

		if outputMode == "json" {
			output.JSON(result)
			return nil
		}

		testType := valStr(result, "testType")
		status := valStr(result, "status")
		output.Banner("Test Run", fmt.Sprintf("%s · %s", testType, truncID(args[0])))

		output.SubHeader("Details")
		output.KeyValue("ID", valStr(result, "id"))
		output.KeyValue("Type", testType)
		output.KeyValue("Status", output.StatusBadge(status))
		output.KeyValue("Backend", valStr(result, "backend"))
		output.KeyValue("Scenario", valStr(result, "scenarioName"))
		output.KeyValue("Created", formatTime(valStr(result, "createdAt")))

		// Results
		if results, ok := result["results"].([]interface{}); ok && len(results) > 0 {
			output.SubHeader(fmt.Sprintf("Results (%d phases)", len(results)))
			rows := make([][]string, 0)
			for _, r := range results {
				if m, ok := r.(map[string]interface{}); ok {
					phase := valStr(m, "phaseName")
					if phase == "—" {
						phase = "main"
					}
					rows = append(rows, []string{
						phase,
						valStr(m, "status"),
						fmtNum(numVal(m, "recordsSent")),
						fmtFloat(numVal(m, "throughputRecordsPerSec"), 1),
						fmtFloat(numVal(m, "avgLatencyMs"), 2),
						fmtFloat(numVal(m, "p99LatencyMs"), 2),
					})
				}
			}
			output.Table(
				[]string{"Phase", "Status", "Records", "Throughput", "Avg Lat.", "P99 Lat."},
				rows,
			)
		}
		return nil
	},
}

var (
	createType       string
	createBackend    string
	createRecords    int
	createProducers  int
	createRecordSize int
	createDuration   int
	createTopic      string
)

var testCreateCmd = &cobra.Command{
	Use:     "create",
	Aliases: []string{"run", "start"},
	Short:   "Start a new performance test",
	Example: `  kates test create --type LOAD --records 100000
  kates test create --type ENDURANCE --duration 300 --producers 4
  kates test create --type BURST --records 50000 --backend native`,
	RunE: func(cmd *cobra.Command, args []string) error {
		req := map[string]interface{}{
			"testType": strings.ToUpper(createType),
		}
		if createBackend != "" {
			req["backend"] = createBackend
		}
		if createRecords > 0 {
			spec := map[string]interface{}{"records": createRecords}
			if createProducers > 0 {
				spec["parallelProducers"] = createProducers
			}
			if createRecordSize > 0 {
				spec["recordSizeBytes"] = createRecordSize
			}
			if createDuration > 0 {
				spec["durationSeconds"] = createDuration
			}
			if createTopic != "" {
				spec["topic"] = createTopic
			}
			req["spec"] = spec
		}

		result, err := apiClient.CreateTest(req)
		if err != nil {
			output.Error("Failed to create test: " + err.Error())
			return nil
		}

		if outputMode == "json" {
			output.JSON(result)
			return nil
		}

		id := valStr(result, "id")
		output.Success("Test created successfully")
		output.KeyValue("ID", id)
		output.KeyValue("Type", valStr(result, "testType"))
		output.KeyValue("Status", output.StatusBadge(valStr(result, "status")))

		if createWait {
			fmt.Println()
			pollUntilDone(id)
		} else {
			output.Hint("Track progress: kates test watch " + id)
		}
		return nil
	},
}

var testDeleteCmd = &cobra.Command{
	Use:     "delete <id>",
	Aliases: []string{"rm"},
	Short:   "Stop and delete a test run",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		err := apiClient.DeleteTest(args[0])
		if err != nil {
			output.Error("Failed to delete test: " + err.Error())
			return nil
		}
		output.Success("Test deleted: " + truncID(args[0]))
		return nil
	},
}

var testTypesCmd = &cobra.Command{
	Use:   "types",
	Short: "List available test types",
	RunE: func(cmd *cobra.Command, args []string) error {
		types, err := apiClient.TestTypes()
		if err != nil {
			output.Error("Failed to get test types: " + err.Error())
			return nil
		}
		if outputMode == "json" {
			output.JSON(types)
			return nil
		}
		output.Header("Test Types")
		rows := make([][]string, len(types))
		for i, t := range types {
			rows[i] = []string{t, describeType(t)}
		}
		output.Table([]string{"Type", "Description"}, rows)
		return nil
	},
}

var testBackendsCmd = &cobra.Command{
	Use:   "backends",
	Short: "List available benchmark backends",
	RunE: func(cmd *cobra.Command, args []string) error {
		backends, err := apiClient.Backends()
		if err != nil {
			output.Error("Failed to get backends: " + err.Error())
			return nil
		}
		if outputMode == "json" {
			output.JSON(backends)
			return nil
		}
		output.Header("Benchmark Backends")
		rows := make([][]string, len(backends))
		for i, b := range backends {
			rows[i] = []string{b}
		}
		output.Table([]string{"Backend"}, rows)
		return nil
	},
}

func valStr(m map[string]interface{}, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return "—"
	}
	return fmt.Sprintf("%v", v)
}

func numVal(m map[string]interface{}, key string) float64 {
	v, ok := m[key]
	if !ok {
		return 0
	}
	if f, ok := v.(float64); ok {
		return f
	}
	return 0
}

func fmtNum(v float64) string {
	if v >= 1_000_000 {
		return fmt.Sprintf("%.1fM", v/1_000_000)
	}
	if v >= 1_000 {
		return fmt.Sprintf("%.1fK", v/1_000)
	}
	return fmt.Sprintf("%.0f", v)
}

func fmtFloat(v float64, precision int) string {
	return fmt.Sprintf("%.*f", precision, v)
}

func truncID(id string) string {
	if len(id) > 12 {
		return id[:12] + "…"
	}
	return id
}

func formatTime(ts string) string {
	if len(ts) > 19 {
		return ts[:10] + " " + ts[11:19]
	}
	return ts
}

func describeType(t string) string {
	switch t {
	case "LOAD":
		return "Standard load test with target throughput"
	case "STRESS":
		return "High-volume multi-producer stress test"
	case "SPIKE":
		return "Sudden burst of traffic to test elasticity"
	case "ENDURANCE":
		return "Long-running soak test for stability"
	case "VOLUME":
		return "Large message payload throughput test"
	case "CAPACITY":
		return "Maximum capacity planning workload"
	case "ROUND_TRIP":
		return "End-to-end produce → consume latency"
	default:
		return ""
	}
}

func init() {
	testListCmd.Flags().StringVar(&testTypeFlag, "type", "", "Filter by test type (LOAD, ENDURANCE, BURST, etc.)")
	testListCmd.Flags().StringVar(&testStatusFlag, "status", "", "Filter by status (PENDING, RUNNING, DONE, FAILED)")
	testListCmd.Flags().IntVar(&testPageFlag, "page", 0, "Page number (0-indexed)")
	testListCmd.Flags().IntVar(&testSizeFlag, "size", 20, "Page size")

	testCreateCmd.Flags().StringVar(&createType, "type", "LOAD", "Test type")
	testCreateCmd.Flags().StringVar(&createBackend, "backend", "", "Benchmark backend")
	testCreateCmd.Flags().IntVar(&createRecords, "records", 0, "Number of records to send")
	testCreateCmd.Flags().IntVar(&createProducers, "producers", 0, "Number of parallel producers")
	testCreateCmd.Flags().IntVar(&createRecordSize, "record-size", 0, "Record size in bytes")
	testCreateCmd.Flags().IntVar(&createDuration, "duration", 0, "Duration in seconds")
	testCreateCmd.Flags().StringVar(&createTopic, "topic", "", "Kafka topic name")
	testCreateCmd.Flags().BoolVar(&createWait, "wait", false, "Wait for test to complete and print results")

	testCmd.AddCommand(testListCmd)
	testCmd.AddCommand(testGetCmd)
	testCmd.AddCommand(testCreateCmd)
	testCmd.AddCommand(testDeleteCmd)
	testCmd.AddCommand(testTypesCmd)
	testCmd.AddCommand(testBackendsCmd)
	rootCmd.AddCommand(testCmd)
}
