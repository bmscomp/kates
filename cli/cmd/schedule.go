package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/klster/kates-cli/client"
	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var scheduleCmd = &cobra.Command{
	Use:     "schedule",
	Aliases: []string{"s", "sched"},
	Short:   "Manage scheduled/recurring test runs",
}

var scheduleListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List all scheduled tests",
	RunE: func(cmd *cobra.Command, args []string) error {
		schedules, err := apiClient.ListSchedules(context.Background())
		if err != nil {
			return cmdErr("Failed to list schedules: " + err.Error())
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
			state := "disabled"
			if s.Enabled {
				state = "enabled"
			}
			rows = append(rows, []string{
				s.ID,
				s.Name,
				s.CronExpression,
				state,
				s.LastRunID,
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
		result, err := apiClient.GetSchedule(context.Background(), args[0])
		if err != nil {
			return cmdErr("Schedule not found: " + err.Error())
		}
		if outputMode == "json" {
			output.JSON(result)
			return nil
		}
		output.Header("Schedule: " + args[0])
		output.KeyValue("Name", result.Name)
		output.KeyValue("Cron", result.CronExpression)
		state := "disabled"
		if result.Enabled {
			state = "enabled"
		}
		output.KeyValue("State", output.StatusBadge(state))
		output.KeyValue("Last Run", result.LastRunID)
		output.KeyValue("Last Run At", result.LastRunAt)
		output.KeyValue("Created", result.CreatedAt)
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
			return cmdErr("--name, --cron, and --request are all required")
		}

		data, err := os.ReadFile(schedFile)
		if err != nil {
			return cmdErr("Failed to read request file: " + err.Error())
		}
		var testRequest interface{}
		if err := json.Unmarshal(data, &testRequest); err != nil {
			return cmdErr("Invalid JSON in request file: " + err.Error())
		}

		req := &client.CreateScheduleRequest{
			Name:           schedName,
			CronExpression: schedCron,
			Enabled:        true,
			TestRequest:    testRequest,
		}

		result, err := apiClient.CreateSchedule(context.Background(), req)
		if err != nil {
			return cmdErr("Failed to create schedule: " + err.Error())
		}
		if outputMode == "json" {
			output.JSON(result)
			return nil
		}
		output.Success(fmt.Sprintf("Schedule created: %s (%s)", result.ID, result.Name))
		output.KeyValue("Cron", result.CronExpression)
		return nil
	},
}

var scheduleDeleteCmd = &cobra.Command{
	Use:     "delete <id>",
	Aliases: []string{"rm"},
	Short:   "Delete a scheduled test",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		err := apiClient.DeleteSchedule(context.Background(), args[0])
		if err != nil {
			return cmdErr("Failed to delete schedule: " + err.Error())
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
