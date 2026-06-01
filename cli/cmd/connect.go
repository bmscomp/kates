package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

var connectNamespace string
var connectFollow bool

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

var connectLogsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Tail Kafka Connect worker logs",
	RunE: func(cmd *cobra.Command, args []string) error {
		connectArgs := []string{"logs", "-l", "strimzi.io/kind=KafkaConnect", "-n", connectNamespace, "--tail=100"}
		if connectFollow {
			connectArgs = append(connectArgs, "-f")
		}
		c := exec.CommandContext(context.Background(), "kubectl", connectArgs...)
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()
	},
}

var connectRestartTaskCmd = &cobra.Command{
	Use:   "restart-task [connector] [taskId]",
	Short: "Restart a specific connector task",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		annotation := fmt.Sprintf("strimzi.io/restart-task=%s", args[1])
		out, err := exec.CommandContext(context.Background(), "kubectl", "annotate", "kafkaconnector", args[0], "-n", connectNamespace, annotation, "--overwrite").CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to restart task %s on connector %s: %s", args[1], args[0], string(out))
		}
		fmt.Printf("Task %s on connector %s restart triggered.\n", args[1], args[0])
		return nil
	},
}

var connectConfigCmd = &cobra.Command{
	Use:   "config [name]",
	Short: "Show connector configuration",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		format := "jsonpath={.spec.config}"
		if outputMode == "json" {
			format = "-o=json"
		} else if outputMode == "yaml" {
			format = "-o=yaml"
		}
		cmdArgs := []string{"get", "kafkaconnector", args[0], "-n", connectNamespace}
		if strings.HasPrefix(format, "jsonpath") {
			cmdArgs = append(cmdArgs, "-o", format)
		} else {
			cmdArgs = append(cmdArgs, format)
		}
		out, err := exec.CommandContext(context.Background(), "kubectl", cmdArgs...).Output()
		if err != nil {
			return fmt.Errorf("failed to get config for connector %s: %w", args[0], err)
		}
		fmt.Println(string(out))
		return nil
	},
}

var connectDeleteCmd = &cobra.Command{
	Use:   "delete [name]",
	Short: "Delete a connector",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		out, err := exec.CommandContext(context.Background(), "kubectl", "delete", "kafkaconnector", args[0], "-n", connectNamespace).CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to delete connector %s: %s", args[0], string(out))
		}
		fmt.Printf("Connector %s deleted.\n", args[0])
		return nil
	},
}

var connectScaleCmd = &cobra.Command{
	Use:   "scale [replicas]",
	Short: "Scale Kafka Connect workers",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		patch := fmt.Sprintf(`{"spec":{"replicas":%s}}`, args[0])
		// Find the KafkaConnect resource name
		nameOut, err := exec.CommandContext(context.Background(), "kubectl", "get", "kafkaconnect", "-n", connectNamespace, "-o", "jsonpath={.items[0].metadata.name}").Output()
		if err != nil {
			return fmt.Errorf("failed to find KafkaConnect resource: %w", err)
		}
		name := strings.TrimSpace(string(nameOut))
		if name == "" {
			return fmt.Errorf("no KafkaConnect resource found in namespace %s", connectNamespace)
		}
		out, patchErr := exec.CommandContext(context.Background(), "kubectl", "patch", "kafkaconnect", name, "-n", connectNamespace, "--type=merge", "-p", patch).CombinedOutput()
		if patchErr != nil {
			return fmt.Errorf("failed to scale connect: %s", string(out))
		}
		fmt.Printf("Kafka Connect %s scaled to %s replicas.\n", name, args[0])
		return nil
	},
}

func init() {
	defaultNS := "kafka"
	if envNS := os.Getenv("KATES_KAFKA_NS"); envNS != "" {
		defaultNS = envNS
	}
	kafkaConnectCmd.PersistentFlags().StringVarP(&connectNamespace, "namespace", "n", defaultNS, "Namespace where Kafka Connect is deployed")

	kafkaConnectCmd.AddCommand(connectStatusCmd)
	kafkaConnectCmd.AddCommand(connectConnectorsCmd)
	kafkaConnectCmd.AddCommand(connectConnectorCmd)
	kafkaConnectCmd.AddCommand(connectTasksCmd)
	kafkaConnectCmd.AddCommand(connectRestartCmd)
	kafkaConnectCmd.AddCommand(connectPauseCmd)
	kafkaConnectCmd.AddCommand(connectResumeCmd)
	kafkaConnectCmd.AddCommand(connectPluginsCmd)
	connectLogsCmd.Flags().BoolVarP(&connectFollow, "follow", "f", false, "Stream logs continuously")
	kafkaConnectCmd.AddCommand(connectLogsCmd)
	kafkaConnectCmd.AddCommand(connectRestartTaskCmd)
	kafkaConnectCmd.AddCommand(connectConfigCmd)
	kafkaConnectCmd.AddCommand(connectDeleteCmd)
	kafkaConnectCmd.AddCommand(connectScaleCmd)
}
