package cmd

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/spf13/cobra"
)

var connectNamespace string

var kafkaConnectCmd = &cobra.Command{
	Use:   "connect",
	Short: "Manage Kafka Connect (via Strimzi CRDs)",
}

var connectStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show Connect cluster status",
	RunE: func(cmd *cobra.Command, args []string) error {
		argsCmd := []string{"get", "kafkaconnect", "-n", connectNamespace}
		if outputMode == "json" {
			argsCmd = append(argsCmd, "-o", "json")
		}
		out, err := exec.CommandContext(context.Background(), "kubectl", argsCmd...).Output()
		if err != nil {
			return fmt.Errorf("failed to get kafkaconnect: %w", err)
		}
		fmt.Print(string(out))
		return nil
	},
}

var connectConnectorsCmd = &cobra.Command{
	Use:   "connectors",
	Short: "List all KafkaConnector CRs",
	RunE: func(cmd *cobra.Command, args []string) error {
		argsCmd := []string{"get", "kafkaconnector", "-n", connectNamespace}
		if outputMode == "json" {
			argsCmd = append(argsCmd, "-o", "json")
		}
		out, err := exec.CommandContext(context.Background(), "kubectl", argsCmd...).Output()
		if err != nil {
			return fmt.Errorf("failed to get kafkaconnector: %w", err)
		}
		fmt.Print(string(out))
		return nil
	},
}

var connectConnectorCmd = &cobra.Command{
	Use:   "connector [name]",
	Short: "Describe a connector",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		argsCmd := []string{"get", "kafkaconnector", args[0], "-n", connectNamespace}
		if outputMode == "json" {
			argsCmd = append(argsCmd, "-o", "json")
		} else {
			argsCmd = append(argsCmd, "-o", "yaml") 
		}
		out, err := exec.CommandContext(context.Background(), "kubectl", argsCmd...).Output()
		if err != nil {
			return fmt.Errorf("failed to describe connector %s: %w", args[0], err)
		}
		fmt.Print(string(out))
		return nil
	},
}

var connectTasksCmd = &cobra.Command{
	Use:   "tasks [name]",
	Short: "Show task-level status for a connector",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		out, err := exec.CommandContext(context.Background(), "kubectl", "get", "kafkaconnector", args[0], "-n", connectNamespace, "-o", "jsonpath={.status.connectorStatus.tasks}").Output()
		if err != nil {
			return fmt.Errorf("failed to get tasks for connector %s: %w", args[0], err)
		}
		if string(out) == "" {
			fmt.Println("No tasks found or connector status unavailable.")
			return nil
		}
		fmt.Println(string(out))
		return nil
	},
}

var connectRestartCmd = &cobra.Command{
	Use:   "restart [name]",
	Short: "Restart a connector",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		out, err := exec.CommandContext(context.Background(), "kubectl", "annotate", "kafkaconnector", args[0], "-n", connectNamespace, "strimzi.io/restart=true", "--overwrite").CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to restart connector %s: %s", args[0], string(out))
		}
		fmt.Printf("Connector %s restart triggered.\n", args[0])
		return nil
	},
}

var connectPauseCmd = &cobra.Command{
	Use:   "pause [name]",
	Short: "Pause a connector",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		out, err := exec.CommandContext(context.Background(), "kubectl", "patch", "kafkaconnector", args[0], "-n", connectNamespace, "--type=merge", "-p", `{"spec":{"state":"paused"}}`).CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to pause connector %s: %s", args[0], string(out))
		}
		fmt.Printf("Connector %s paused.\n", args[0])
		return nil
	},
}

var connectResumeCmd = &cobra.Command{
	Use:   "resume [name]",
	Short: "Resume a connector",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		out, err := exec.CommandContext(context.Background(), "kubectl", "patch", "kafkaconnector", args[0], "-n", connectNamespace, "--type=merge", "-p", `{"spec":{"state":"running"}}`).CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to resume connector %s: %s", args[0], string(out))
		}
		fmt.Printf("Connector %s resumed.\n", args[0])
		return nil
	},
}

var connectPluginsCmd = &cobra.Command{
	Use:   "plugins",
	Short: "List installed connector plugins",
	RunE: func(cmd *cobra.Command, args []string) error {
		out, err := exec.CommandContext(context.Background(), "kubectl", "get", "kafkaconnect", "-n", connectNamespace, "-o", "jsonpath={.items[0].status.connectorPlugins}").Output()
		if err != nil {
			return fmt.Errorf("failed to get plugins (ensure a KafkaConnect cluster is running): %w", err)
		}
		if string(out) == "" {
			fmt.Println("No plugins found or status unavailable.")
			return nil
		}
		
		if outputMode == "json" {
			fmt.Println(string(out))
		} else {
			// Basic formatting for non-json output
			fmt.Println("Installed Connector Plugins:")
			fmt.Println(string(out))
		}
		return nil
	},
}

func init() {
	kafkaConnectCmd.PersistentFlags().StringVarP(&connectNamespace, "namespace", "n", "kafka", "Namespace where Kafka Connect is deployed")

	kafkaConnectCmd.AddCommand(connectStatusCmd)
	kafkaConnectCmd.AddCommand(connectConnectorsCmd)
	kafkaConnectCmd.AddCommand(connectConnectorCmd)
	kafkaConnectCmd.AddCommand(connectTasksCmd)
	kafkaConnectCmd.AddCommand(connectRestartCmd)
	kafkaConnectCmd.AddCommand(connectPauseCmd)
	kafkaConnectCmd.AddCommand(connectResumeCmd)
	kafkaConnectCmd.AddCommand(connectPluginsCmd)
}
