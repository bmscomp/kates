package cmd

import (
	"context"
	"fmt"

	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var webhookCmd = &cobra.Command{
	Use:   "webhook",
	Short: "Manage webhook notifications for test completion events",
}

var webhookListCmd = &cobra.Command{
	Use:   "list",
	Short: "List registered webhooks",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		hooks, err := apiClient.ListWebhooks(ctx)
		if err != nil {
			return cmdErr("Failed to list webhooks: " + err.Error())
		}
		if len(hooks) == 0 {
			output.Hint("No webhooks registered. Use 'kates webhook add' to register one.")
			return nil
		}
		rows := make([][]string, 0, len(hooks))
		for _, h := range hooks {
			rows = append(rows, []string{h.Name, h.URL, h.Events})
		}
		output.Table([]string{"Name", "URL", "Events"}, rows)
		return nil
	},
}

var webhookAddCmd = &cobra.Command{
	Use:     "add <name> <url>",
	Short:   "Register a webhook",
	Args:    cobra.ExactArgs(2),
	Example: "  kates webhook add slack https://hooks.slack.com/services/T.../B.../xxx",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		err := apiClient.RegisterWebhook(ctx, args[0], args[1])
		if err != nil {
			return cmdErr("Failed to register webhook: " + err.Error())
		}
		output.Success(fmt.Sprintf("Registered webhook '%s' → %s", args[0], args[1]))
		return nil
	},
}

var webhookRemoveCmd = &cobra.Command{
	Use:     "remove <name>",
	Aliases: []string{"rm", "delete"},
	Short:   "Unregister a webhook",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		err := apiClient.DeleteWebhook(ctx, args[0])
		if err != nil {
			return cmdErr("Failed to remove webhook: " + err.Error())
		}
		output.Success(fmt.Sprintf("Removed webhook '%s'", args[0]))
		return nil
	},
}

func init() {
	webhookCmd.AddCommand(webhookListCmd)
	webhookCmd.AddCommand(webhookAddCmd)
	webhookCmd.AddCommand(webhookRemoveCmd)
	rootCmd.AddCommand(webhookCmd)
}
