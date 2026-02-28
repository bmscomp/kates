package cmd

import (
	"context"
	"fmt"

	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var (
	auditLimit int
	auditType  string
	auditSince string
)

var auditCmd = &cobra.Command{
	Use:   "audit",
	Short: "View audit log of cluster mutations",
	Long:  "Shows a log of all mutating operations (test creates/deletes, topic changes, disruption runs) with timestamps and details.",
	Example: `  kates audit
  kates audit --limit 20 --type test
  kates audit --type topic --since 2024-01-01T00:00:00Z
  kates audit -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		events, err := apiClient.Audit(context.Background(), auditLimit, auditType, auditSince)
		if err != nil {
			return cmdErr("Failed to fetch audit log: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(events)
			return nil
		}

		if len(events) == 0 {
			output.Hint("No audit events found.")
			if auditType != "" || auditSince != "" {
				output.Hint("Try removing filters to see more results.")
			}
			return nil
		}

		output.Banner("Audit Log", fmt.Sprintf("%d events", len(events)))

		rows := make([][]string, 0, len(events))
		for _, e := range events {
			action := e.Action
			switch action {
			case "CREATE":
				action = output.SuccessStyle.Render("+ " + action)
			case "DELETE":
				action = output.ErrorStyle.Render("- " + action)
			case "UPDATE", "ALTER":
				action = output.AccentStyle.Render("~ " + action)
			}

			target := e.Target
			if len(target) > 20 {
				target = target[:17] + "..."
			}

			details := e.Details
			if len(details) > 30 {
				details = details[:27] + "..."
			}

			rows = append(rows, []string{
				formatTime(e.Timestamp),
				action,
				e.EventType,
				target,
				details,
			})
		}

		output.Table([]string{"Time", "Action", "Type", "Target", "Details"}, rows)
		return nil
	},
}

func init() {
	auditCmd.Flags().IntVar(&auditLimit, "limit", 50, "Maximum number of events to show")
	auditCmd.Flags().StringVar(&auditType, "type", "", "Filter by event type (test, topic, disruption, resilience)")
	auditCmd.Flags().StringVar(&auditSince, "since", "", "Show events after this ISO-8601 timestamp")
	rootCmd.AddCommand(auditCmd)
}
