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

	"github.com/bmscomp/kates/cli/internal/kubectl"
	"github.com/bmscomp/kates/cli/output"
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
		kc := kubectl.New(connectNamespace)
		ctx := context.Background()
		out, err := kc.Output(ctx, "get", "kafkaconnect", "-n", connectNamespace)
		if err != nil {
			return fmt.Errorf("failed to get kafkaconnect: %w", err)
		}
		output.Render(outputMode == "json", string(out), func() {
			fmt.Print(string(out))
		})
		return nil
	},
}

var connectConnectorsCmd = &cobra.Command{
	Use:   "connectors",
	Short: "List all KafkaConnector CRs",
	RunE: func(cmd *cobra.Command, args []string) error {
		kc := kubectl.New(connectNamespace)
		ctx := context.Background()

		out, err := kc.Output(ctx, "get", "kafkaconnector", "-n", connectNamespace, "-o", "json")
		if err != nil {
			return fmt.Errorf("failed to get kafkaconnector: %w", err)
		}

		var list kafkaConnectorList
		if err := json.Unmarshal(out, &list); err != nil {
			// Keep old behavior as fallback when JSON parsing fails.
			fallback, fbErr := kc.Output(ctx, "get", "kafkaconnector", "-n", connectNamespace)
			if fbErr != nil {
				return fmt.Errorf("failed to parse connector output (%v) and fallback list failed: %w", err, fbErr)
			}
			fmt.Print(string(fallback))
			return nil
		}

		output.Render(outputMode == "json", list, func() {
			rows := make([][]string, 0, len(list.Items))
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

				rows = append(rows, []string{
					item.Metadata.Name,
					item.Metadata.Labels["strimzi.io/cluster"],
					item.Spec.Class,
					fmt.Sprintf("%d", maxTasks),
					isReadyConditionTrue(item.Status.Conditions),
					runningTasks,
					taskStates,
				})
			}
			output.Table([]string{"Name", "Cluster", "Connector Class", "Max Tasks", "Ready", "Running Tasks", "Task States"}, rows)
		})
		return nil
	},
}

var connectConnectorCmd = &cobra.Command{
	Use:   "connector [name]",
	Short: "Describe a connector",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		kc := kubectl.New(connectNamespace)
		ctx := context.Background()
		argsCmd := []string{"get", "kafkaconnector", args[0], "-n", connectNamespace}
		if outputMode != "json" {
			argsCmd = append(argsCmd, "-o", "yaml")
		} else {
			argsCmd = append(argsCmd, "-o", "json")
		}
		out, err := kc.Output(ctx, argsCmd...)
		if err != nil {
			return fmt.Errorf("failed to describe connector %s: %w", args[0], err)
		}
		output.Render(outputMode == "json", string(out), func() {
			fmt.Print(string(out))
		})
		return nil
	},
}

var connectTasksCmd = &cobra.Command{
	Use:   "tasks [name]",
	Short: "Show task-level status for a connector",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		kc := kubectl.New(connectNamespace)
		ctx := context.Background()

		var connector kafkaConnector
		if err := kc.JSON(ctx, &connector, "get", "kafkaconnector", args[0], "-n", connectNamespace); err != nil {
			return fmt.Errorf("failed to get tasks for connector %s: %w", args[0], err)
		}

		tasks := connector.Status.ConnectorStatus.Tasks
		output.Render(outputMode == "json", tasks, func() {
			if len(tasks) == 0 {
				output.Hint(fmt.Sprintf("No tasks found for connector %s.", args[0]))
				return
			}

			output.Banner(fmt.Sprintf("Connector: %s", args[0]), fmt.Sprintf("%d tasks", len(tasks)))
			rows := make([][]string, 0, len(tasks))
			for _, task := range tasks {
				rows = append(rows, []string{
					fmt.Sprintf("%d", task.ID),
					task.State,
					task.WorkerID,
					task.Version,
				})
			}
			output.Table([]string{"Task ID", "State", "Worker ID", "Version"}, rows)
		})
		return nil
	},
}

var connectRestartCmd = &cobra.Command{
	Use:   "restart [name]",
	Short: "Restart a connector",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		kc := kubectl.New(connectNamespace)
		ctx := context.Background()
		_, err := kc.Run(ctx, "annotate", "kafkaconnector", args[0], "-n", connectNamespace, "strimzi.io/restart=true", "--overwrite")
		if err != nil {
			return fmt.Errorf("failed to restart connector %s: %w", args[0], err)
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
		kc := kubectl.New(connectNamespace)
		ctx := context.Background()
		_, err := kc.Run(ctx, "patch", "kafkaconnector", args[0], "-n", connectNamespace, "--type=merge", "-p", `{"spec":{"state":"paused"}}`)
		if err != nil {
			return fmt.Errorf("failed to pause connector %s: %w", args[0], err)
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
		kc := kubectl.New(connectNamespace)
		ctx := context.Background()
		_, err := kc.Run(ctx, "patch", "kafkaconnector", args[0], "-n", connectNamespace, "--type=merge", "-p", `{"spec":{"state":"running"}}`)
		if err != nil {
			return fmt.Errorf("failed to resume connector %s: %w", args[0], err)
		}
		fmt.Printf("Connector %s resumed.\n", args[0])
		return nil
	},
}

var connectPluginsCmd = &cobra.Command{
	Use:   "plugins",
	Short: "List installed connector plugins",
	RunE: func(cmd *cobra.Command, args []string) error {
		kc := kubectl.New(connectNamespace)
		ctx := context.Background()
		out, err := kc.Output(ctx, "get", "kafkaconnect", "-n", connectNamespace, "-o", "jsonpath={.items[0].status.connectorPlugins}")
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
		kc := kubectl.New(connectNamespace)
		ctx := context.Background()
		annotation := fmt.Sprintf("strimzi.io/restart-task=%s", args[1])
		_, err := kc.Run(ctx, "annotate", "kafkaconnector", args[0], "-n", connectNamespace, annotation, "--overwrite")
		if err != nil {
			return fmt.Errorf("failed to restart task %s on connector %s: %w", args[1], args[0], err)
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
		kc := kubectl.New(connectNamespace)
		ctx := context.Background()
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
		out, err := kc.Output(ctx, cmdArgs...)
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
		kc := kubectl.New(connectNamespace)
		ctx := context.Background()
		_, err := kc.Run(ctx, "delete", "kafkaconnector", args[0], "-n", connectNamespace)
		if err != nil {
			return fmt.Errorf("failed to delete connector %s: %w", args[0], err)
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
		kc := kubectl.New(connectNamespace)
		ctx := context.Background()
		patch := fmt.Sprintf(`{"spec":{"replicas":%s}}`, args[0])
		// Find the KafkaConnect resource name
		nameOut, err := kc.Output(ctx, "get", "kafkaconnect", "-n", connectNamespace, "-o", "jsonpath={.items[0].metadata.name}")
		if err != nil {
			return fmt.Errorf("failed to find KafkaConnect resource: %w", err)
		}
		name := strings.TrimSpace(string(nameOut))
		if name == "" {
			return fmt.Errorf("no KafkaConnect resource found in namespace %s", connectNamespace)
		}
		_, patchErr := kc.Run(ctx, "patch", "kafkaconnect", name, "-n", connectNamespace, "--type=merge", "-p", patch)
		if patchErr != nil {
			return fmt.Errorf("failed to scale connect: %w", patchErr)
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
	kc := kubectl.New("")
	out, err := kc.Output(context.Background(), "get", "kafkaconnect", "-A",
		"-o", "jsonpath={.items[0].metadata.namespace}")
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
