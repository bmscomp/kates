package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var scheduleCmd = &cobra.Command{
	Use:   "schedule",
	Short: "Manage scheduled/recurring test runs",
}

var scheduleListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all scheduled tests",
	RunE: func(cmd *cobra.Command, args []string) error {
		schedules, err := apiClient.ListSchedules()
		if err != nil {
			output.Error("Failed to list schedules: " + err.Error())
			return nil
		}

		if outputMode == "json" {
			output.JSON(schedules)
			return nil
		}

		output.Header("Scheduled Tests")
		if len(schedules) == 0 {
			output.Hint("  No schedules configured.")
			return nil
		}

		rows := make([][]string, 0, len(schedules))
		for _, s := range schedules {
			enabled := "disabled"
			if e, ok := s["enabled"].(bool); ok && e {
				enabled = "enabled"
			}
			rows = append(rows, []string{
				valStr(s, "id"),
				valStr(s, "name"),
				valStr(s, "cronExpression"),
				enabled,
				valStr(s, "lastRunId"),
			})
		}
		output.Table([]string{"ID", "Name", "Cron", "State", "Last Run"}, rows)
		return nil
	},
}

var scheduleGetCmd = &cobra.Command{
	Use:   "get <id>",
	Short: "Show details of a scheduled test",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := apiClient.GetSchedule(args[0])
		if err != nil {
			output.Error("Schedule not found: " + err.Error())
			return nil
		}
		if outputMode == "json" {
			output.JSON(result)
			return nil
		}
		output.Header("Schedule: " + args[0])
		output.KeyValue("Name", valStr(result, "name"))
		output.KeyValue("Cron", valStr(result, "cronExpression"))
		enabled := "disabled"
		if e, ok := result["enabled"].(bool); ok && e {
			enabled = "enabled"
		}
		output.KeyValue("State", output.StatusBadge(enabled))
		output.KeyValue("Last Run", valStr(result, "lastRunId"))
		output.KeyValue("Last Run At", valStr(result, "lastRunAt"))
		output.KeyValue("Created", valStr(result, "createdAt"))
		return nil
	},
}

var (
	schedName string
	schedCron string
	schedFile string
)

var scheduleCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new scheduled test",
	Example: `  kates schedule create --name "Hourly Load Test" --cron "0 * * * *" --request request.json
  kates schedule create --name "Nightly Endurance" --cron "0 2 * * *" --request endurance.json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if schedName == "" || schedCron == "" || schedFile == "" {
			output.Error("--name, --cron, and --request are all required")
			return nil
		}

		data, err := os.ReadFile(schedFile)
		if err != nil {
			output.Error("Failed to read request file: " + err.Error())
			return nil
		}
		var testRequest interface{}
		if err := json.Unmarshal(data, &testRequest); err != nil {
			output.Error("Invalid JSON in request file: " + err.Error())
			return nil
		}

		req := map[string]interface{}{
			"name":           schedName,
			"cronExpression": schedCron,
			"enabled":        true,
			"testRequest":    testRequest,
		}

		result, err := apiClient.CreateSchedule(req)
		if err != nil {
			output.Error("Failed to create schedule: " + err.Error())
			return nil
		}
		if outputMode == "json" {
			output.JSON(result)
			return nil
		}
		output.Success(fmt.Sprintf("Schedule created: %s (%s)", valStr(result, "id"), valStr(result, "name")))
		output.KeyValue("Cron", valStr(result, "cronExpression"))
		return nil
	},
}

var scheduleDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a scheduled test",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		err := apiClient.DeleteSchedule(args[0])
		if err != nil {
			output.Error("Failed to delete schedule: " + err.Error())
			return nil
		}
		output.Success("Schedule deleted: " + args[0])
		return nil
	},
}

func init() {
	scheduleCreateCmd.Flags().StringVar(&schedName, "name", "", "Schedule name (required)")
	scheduleCreateCmd.Flags().StringVar(&schedCron, "cron", "", "Cron expression, e.g. '0 * * * *' (required)")
	scheduleCreateCmd.Flags().StringVar(&schedFile, "request", "", "Path to JSON file with test request (required)")

	scheduleCmd.AddCommand(scheduleListCmd)
	scheduleCmd.AddCommand(scheduleGetCmd)
	scheduleCmd.AddCommand(scheduleCreateCmd)
	scheduleCmd.AddCommand(scheduleDeleteCmd)
	rootCmd.AddCommand(scheduleCmd)
}
