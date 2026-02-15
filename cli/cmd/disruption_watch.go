package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/klster/kates-cli/client"
	"github.com/spf13/cobra"
)

func init() {
	disruptionCmd.AddCommand(disruptionWatchCmd)
	disruptionCmd.AddCommand(disruptionPlaybookCmd)
	disruptionPlaybookCmd.AddCommand(disruptionPlaybookListCmd)
	disruptionPlaybookCmd.AddCommand(disruptionPlaybookRunCmd)
	disruptionCmd.AddCommand(disruptionScheduleCmd)
	disruptionScheduleCmd.AddCommand(disruptionScheduleListCmd)
	disruptionScheduleCmd.AddCommand(disruptionScheduleCreateCmd)
	disruptionScheduleCmd.AddCommand(disruptionScheduleDeleteCmd)
	disruptionScheduleCreateCmd.Flags().StringVar(&disruptSchedPlaybook, "playbook", "", "Playbook name")
	disruptionScheduleCreateCmd.Flags().StringVar(&disruptSchedCron, "cron", "", "Cron expression (5-field)")
}

var disruptionWatchCmd = &cobra.Command{
	Use:   "watch <disruption-id>",
	Short: "Stream real-time progress events from a running disruption",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		disruptionID := args[0]
		url := fmt.Sprintf("%s/api/disruptions/%s/stream", apiClient.BaseURL, disruptionID)

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return fmt.Errorf("failed to create SSE request: %w", err)
		}
		req.Header.Set("Accept", "text/event-stream")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("failed to connect to SSE stream: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("unexpected status: %d", resp.StatusCode)
		}

		fmt.Printf("📡 Watching disruption: %s\n", disruptionID)
		fmt.Println(strings.Repeat("─", 60))

		scanner := bufio.NewScanner(resp.Body)
		var eventName string

		for scanner.Scan() {
			line := scanner.Text()

			if strings.HasPrefix(line, "event:") {
				eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
				continue
			}

			if strings.HasPrefix(line, "data:") {
				data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				var event client.DisruptionSSEEvent
				if err := json.Unmarshal([]byte(data), &event); err != nil {
					fmt.Printf("  %s %s\n", eventIcon(eventName), data)
					continue
				}
				printSSEEvent(eventName, &event)
				if eventName == "COMPLETED" || eventName == "FAILED" {
					fmt.Println(strings.Repeat("─", 60))
					fmt.Printf("🏁 Disruption %s\n", strings.ToLower(eventName))
					return nil
				}
			}
		}
		return scanner.Err()
	},
}

var disruptionPlaybookCmd = &cobra.Command{
	Use:   "playbook",
	Short: "Manage pre-built disruption playbooks",
}

var disruptionPlaybookListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available disruption playbooks",
	RunE: func(cmd *cobra.Command, args []string) error {
		entries, err := apiClient.PlaybookList(context.Background())
		if err != nil {
			return fmt.Errorf("failed to list playbooks: %w", err)
		}
		if len(entries) == 0 {
			fmt.Println("  No playbooks available")
			return nil
		}
		fmt.Printf("\n  %-20s %-12s %-5s %s\n", "NAME", "CATEGORY", "STEPS", "DESCRIPTION")
		fmt.Printf("  %s\n", strings.Repeat("─", 70))
		for _, e := range entries {
			fmt.Printf("  %-20s %-12s %-5d %s\n", e.Name, e.Category, e.Steps, e.Description)
		}
		fmt.Println()
		return nil
	},
}

var disruptionPlaybookRunCmd = &cobra.Command{
	Use:   "run <playbook-name>",
	Short: "Execute a pre-built disruption playbook",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		fmt.Printf("  🎯 Running playbook: %s\n", name)
		result, err := apiClient.PlaybookRun(context.Background(), name)
		if err != nil {
			return fmt.Errorf("failed to run playbook: %w", err)
		}
		fmt.Printf("  ✅ Disruption ID: %s\n", result.ID)
		fmt.Printf("  📊 Status: %s\n", result.Report.Status)
		if result.Report.SlaVerdict != nil {
			fmt.Printf("  🏆 SLA Grade: %s\n", result.Report.SlaVerdict.Grade)
		}
		return nil
	},
}

var disruptionScheduleCmd = &cobra.Command{
	Use:   "schedule",
	Short: "Manage scheduled recurring disruption tests",
}

var disruptionScheduleListCmd = &cobra.Command{
	Use:   "list",
	Short: "List disruption schedules",
	RunE: func(cmd *cobra.Command, args []string) error {
		entries, err := apiClient.DisruptionScheduleList(context.Background())
		if err != nil {
			return fmt.Errorf("failed to list schedules: %w", err)
		}
		if len(entries) == 0 {
			fmt.Println("  No disruption schedules configured")
			return nil
		}
		fmt.Printf("\n  %-10s %-20s %-15s %-8s %-15s %s\n",
			"ID", "NAME", "CRON", "ENABLED", "PLAYBOOK", "LAST RUN")
		fmt.Printf("  %s\n", strings.Repeat("─", 80))
		for _, e := range entries {
			enabled := "✓"
			if !e.Enabled {
				enabled = "✗"
			}
			lastRun := e.LastRunAt
			if lastRun == "" {
				lastRun = "never"
			}
			fmt.Printf("  %-10s %-20s %-15s %-8s %-15s %s\n",
				e.ID, e.Name, e.CronExpression, enabled, e.PlaybookName, lastRun)
		}
		fmt.Println()
		return nil
	},
}

var disruptSchedPlaybook string
var disruptSchedCron string

var disruptionScheduleCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new disruption schedule",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if disruptSchedCron == "" {
			return fmt.Errorf("--cron is required")
		}
		if disruptSchedPlaybook == "" {
			return fmt.Errorf("--playbook is required")
		}
		body := map[string]interface{}{
			"name":           name,
			"cronExpression": disruptSchedCron,
			"playbookName":   disruptSchedPlaybook,
			"enabled":        true,
		}
		_, err := apiClient.DisruptionScheduleCreate(context.Background(), body)
		if err != nil {
			return fmt.Errorf("failed to create schedule: %w", err)
		}
		fmt.Printf("  ✅ Schedule '%s' created (cron: %s, playbook: %s)\n", name, disruptSchedCron, disruptSchedPlaybook)
		return nil
	},
}

var disruptionScheduleDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a disruption schedule",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		if err := apiClient.DisruptionScheduleDelete(context.Background(), id); err != nil {
			return fmt.Errorf("failed to delete schedule: %w", err)
		}
		fmt.Printf("  ✅ Schedule %s deleted\n", id)
		return nil
	},
}

func printSSEEvent(eventType string, event *client.DisruptionSSEEvent) {
	step := ""
	if event.StepName != "" {
		step = fmt.Sprintf("[%s] ", event.StepName)
	}
	fmt.Printf("  %s %s%s\n", eventIcon(eventType), step, event.Message)
}

func eventIcon(eventType string) string {
	switch eventType {
	case "STARTED":
		return "🚀"
	case "STEP_STARTED":
		return "▶️"
	case "METRICS_BASELINE":
		return "📊"
	case "FAULT_INJECTED":
		return "💥"
	case "RECOVERY_WAITING":
		return "⏳"
	case "METRICS_CAPTURED":
		return "📈"
	case "STEP_COMPLETED":
		return "✅"
	case "ROLLBACK":
		return "⏪"
	case "SLA_GRADED":
		return "🏆"
	case "COMPLETED":
		return "🎉"
	case "FAILED":
		return "❌"
	default:
		return "ℹ️"
	}
}
