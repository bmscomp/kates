package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

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
		if outputMode == "json" {
			out, err := exec.CommandContext(context.Background(), "kubectl", "get", "kafkaconnector", "-n", connectNamespace, "-o", "json").Output()
			if err != nil {
				return fmt.Errorf("failed to get kafkaconnector: %w", err)
			}
			fmt.Print(string(out))
			return nil
		}

		out, err := exec.CommandContext(context.Background(), "kubectl", "get", "kafkaconnector", "-n", connectNamespace, "-o", "json").Output()
		if err != nil {
			return fmt.Errorf("failed to get kafkaconnector: %w", err)
		}

		var list kafkaConnectorList
		if err := json.Unmarshal(out, &list); err != nil {
			// Keep old behavior as fallback when JSON parsing fails.
			fallback, fbErr := exec.CommandContext(context.Background(), "kubectl", "get", "kafkaconnector", "-n", connectNamespace).Output()
			if fbErr != nil {
				return fmt.Errorf("failed to parse connector output (%v) and fallback list failed: %w", err, fbErr)
			}
			fmt.Print(string(fallback))
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tCLUSTER\tCONNECTOR CLASS\tMAX TASKS\tREADY\tRUNNING TASKS\tTASK STATES")
		for _, item := range list.Items {
			running := 0
			statesSet := map[string]struct{}{}
			for _, task := range item.Status.ConnectorStatus.Tasks {
				if strings.EqualFold(task.State, "RUNNING") {
					running++
				}
				if task.State != "" {
					statesSet[task.State] = struct{}{}
				}
			}
			states := make([]string, 0, len(statesSet))
			for s := range statesSet {
				states = append(states, s)
			}
			sort.Strings(states)
			taskStates := "-"
			if len(states) > 0 {
				taskStates = strings.Join(states, ",")
			}

			maxTasks := item.Spec.TasksMax
			runningTasks := strconv.Itoa(running)
			if maxTasks > 0 {
				runningTasks = fmt.Sprintf("%d/%d", running, maxTasks)
			}

			fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\t%s\n",
				item.Metadata.Name,
				item.Metadata.Labels["strimzi.io/cluster"],
				item.Spec.Class,
				maxTasks,
				isReadyConditionTrue(item.Status.Conditions),
				runningTasks,
				taskStates,
			)
		}
		return w.Flush()
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
		out, err := exec.CommandContext(context.Background(), "kubectl", "get", "kafkaconnector", args[0], "-n", connectNamespace, "-o", "json").Output()
		if err != nil {
			return fmt.Errorf("failed to get tasks for connector %s: %w", args[0], err)
		}

		var connector kafkaConnector
		if err := json.Unmarshal(out, &connector); err != nil {
			return fmt.Errorf("failed to parse connector %s status: %w", args[0], err)
		}

		tasks := connector.Status.ConnectorStatus.Tasks
		if outputMode == "json" {
			b, err := json.Marshal(tasks)
			if err != nil {
				return fmt.Errorf("failed to marshal tasks: %w", err)
			}
			fmt.Println(string(b))
			return nil
		}

		if len(tasks) == 0 {
			connectorState := connector.Status.ConnectorStatus.Connector.State
			if connectorState == "" {
				connectorState = "UNKNOWN"
			}
			fmt.Printf("No tasks currently reported for connector %s (connector state: %s, ready: %s).\n",
				args[0],
				connectorState,
				isReadyConditionTrue(connector.Status.Conditions),
			)
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tSTATE\tWORKER ID\tVERSION")
		for _, task := range tasks {
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\n", task.ID, task.State, task.WorkerID, task.Version)
		}
		w.Flush()
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
	defaultNS := detectConnectNamespace()
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

// detectConnectNamespace resolves the namespace where Kafka Connect is deployed.
// Priority: KATES_CONNECT_NS env → live cluster auto-detect → KATES_KAFKA_NS env → "kafka".
func detectConnectNamespace() string {
	if envNS := os.Getenv("KATES_CONNECT_NS"); envNS != "" {
		return envNS
	}

	// Auto-detect from cluster: find the namespace of any KafkaConnect CR
	out, err := exec.Command("kubectl", "get", "kafkaconnect", "-A",
		"-o", "jsonpath={.items[0].metadata.namespace}").Output()
	if err == nil {
		ns := strings.TrimSpace(string(out))
		if ns != "" {
			return ns
		}
	}

	// Backwards compatibility fallback
	if envNS := os.Getenv("KATES_KAFKA_NS"); envNS != "" {
		return envNS
	}
	return "kafka"
}

type kafkaConnectorList struct {
	Items []kafkaConnector `json:"items"`
}

type kafkaConnector struct {
	Metadata struct {
		Name   string            `json:"name"`
		Labels map[string]string `json:"labels"`
	} `json:"metadata"`
	Spec struct {
		Class    string `json:"class"`
		TasksMax int    `json:"tasksMax"`
	} `json:"spec"`
	Status struct {
		Conditions      []statusCondition `json:"conditions"`
		ConnectorStatus struct {
			Connector struct {
				State string `json:"state"`
			} `json:"connector"`
			Tasks []connectorTask `json:"tasks"`
		} `json:"connectorStatus"`
	} `json:"status"`
}

type statusCondition struct {
	Type   string `json:"type"`
	Status string `json:"status"`
}

type connectorTask struct {
	ID       int    `json:"id"`
	State    string `json:"state"`
	WorkerID string `json:"worker_id"`
	Version  string `json:"version"`
}

func isReadyConditionTrue(conditions []statusCondition) string {
	for _, c := range conditions {
		if c.Type == "Ready" {
			return c.Status
		}
	}
	return "Unknown"
}
