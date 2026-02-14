package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var watchInterval int

var testWatchCmd = &cobra.Command{
	Use:   "watch <id>",
	Short: "Live-watch a running test until completion",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		tick := 0

		for {
			result, err := apiClient.GetTest(context.Background(), id)
			if err != nil {
				return cmdErr("Failed to fetch test: " + err.Error())
			}

			status := mapStr(result, "status")

			// Clear screen
			fmt.Print("\033[2J\033[H")

			output.Banner("Test Watch", fmt.Sprintf("%s · %s", mapStr(result, "testType"), truncID(id)))

			output.SubHeader("Status")
			output.KeyValue("Test ID", id)
			output.KeyValue("Type", mapStr(result, "testType"))
			output.KeyValue("Backend", mapStr(result, "backend"))
			output.KeyValue("Status", output.StatusBadge(status))
			output.KeyValue("Created", formatTime(mapStr(result, "createdAt")))

			// Show results if available
			if results, ok := result["results"].([]interface{}); ok && len(results) > 0 {
				output.SubHeader(fmt.Sprintf("Results (%d phases)", len(results)))
				rows := make([][]string, 0)
				for _, r := range results {
					if m, ok := r.(map[string]interface{}); ok {
						phase := mapStr(m, "phaseName")
						if phase == "—" {
							phase = "main"
						}
						rows = append(rows, []string{
							phase,
							mapStr(m, "status"),
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

			// Terminal checks
			switch status {
			case "DONE", "COMPLETED":
				output.Success("Test completed successfully")
				output.Hint(fmt.Sprintf("View report: kates report show %s", id))
				return nil
			case "FAILED", "ERROR":
				return cmdErr("Test failed")
			default:
				fmt.Println()
				fmt.Printf("  %s Refreshing every %ds... (Ctrl+C to stop)\n",
					spinnerFrame(tick),
					watchInterval,
				)
			}

			tick++
			time.Sleep(time.Duration(watchInterval) * time.Second)
		}
	},
}

var testListWatchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Auto-refreshing test list",
	RunE: func(cmd *cobra.Command, args []string) error {
		tick := 0

		for {
			data, err := apiClient.ListTests(context.Background(), testTypeFlag, testStatusFlag, testPageFlag, testSizeFlag)
			if err != nil {
				output.Error("Failed to list tests: " + err.Error())
				time.Sleep(time.Duration(watchInterval) * time.Second)
				tick++
				continue
			}

			paged, _ := ParsePaged(data)

			// Clear screen
			fmt.Print("\033[2J\033[H")
			output.Header("Test Runs (live)")

			if len(paged.Content) == 0 {
				output.Hint("No test runs found.")
			} else {
				rows := make([][]string, 0, len(paged.Content))
				for _, run := range paged.Content {
					rows = append(rows, []string{
						truncID(mapStr(run, "id")),
						mapStr(run, "testType"),
						mapStr(run, "status"),
						mapStr(run, "backend"),
						formatTime(mapStr(run, "createdAt")),
					})
				}
				output.Table([]string{"ID", "Type", "Status", "Backend", "Created"}, rows)
				output.Hint(fmt.Sprintf("%d items total", paged.TotalItems))
			}

			fmt.Printf("\n  %s Refreshing every %ds... (Ctrl+C to stop)\n",
				spinnerFrame(tick),
				watchInterval,
			)

			tick++
			time.Sleep(time.Duration(watchInterval) * time.Second)
		}
	},
}

// --wait flag for test create
var createWait bool

func pollUntilDone(id string) {
	tick := 0
	for {
		result, err := apiClient.GetTest(context.Background(), id)
		if err != nil {
			output.Error("Polling failed: " + err.Error())
			return
		}
		status := mapStr(result, "status")
		switch status {
		case "DONE", "COMPLETED":
			fmt.Printf("\r  %s Test completed                              \n",
				output.SuccessStyle.Render("✓"),
			)
			// Print summary
			summary, err := apiClient.ReportSummary(context.Background(), id)
			if err == nil {
				output.SubHeader("Results")
				output.KeyValue("Throughput", fmt.Sprintf("%s rec/s", fmtNum(numVal(summary, "avgThroughputRecPerSec"))))
				output.KeyValue("P99 Latency", fmt.Sprintf("%s ms", fmtFloat(numVal(summary, "p99LatencyMs"), 2)))
				output.KeyValue("Error Rate", fmt.Sprintf("%.4f%%", numVal(summary, "errorRate")*100))
			}
			output.Hint(fmt.Sprintf("Full report: kates report show %s", id))
			return
		case "FAILED", "ERROR":
			fmt.Printf("\r  %s Test failed                                 \n",
				output.ErrorStyle.Render("✖"),
			)
			return
		default:
			elapsed := time.Duration(tick*2) * time.Second
			fmt.Printf("\r  %s Waiting... %s [%s]   ",
				spinnerFrame(tick),
				output.DimStyle.Render(elapsed.String()),
				output.AccentStyle.Render(status),
			)
			tick++
			time.Sleep(2 * time.Second)
		}
	}
}

func init() {
	testWatchCmd.Flags().IntVar(&watchInterval, "interval", 3, "Refresh interval in seconds")
	testCmd.AddCommand(testWatchCmd)
	// Note: testListWatchCmd is a subcommand of 'test list', but Cobra doesn't
	// support subcommands on commands with RunE. We add it as 'test watch' instead.
}
