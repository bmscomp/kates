package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
	"github.com/spf13/cobra"
)

func init() {
	disruptionCmd.AddCommand(disruptionWatchCmd)
	disruptionCmd.AddCommand(disruptionPlaybookCmd)
	disruptionPlaybookCmd.AddCommand(disruptionPlaybookListCmd)
	disruptionPlaybookCmd.AddCommand(disruptionPlaybookShowCmd)
	disruptionPlaybookCmd.AddCommand(disruptionPlaybookRunCmd)
	disruptionPlaybookRunCmd.Flags().BoolVar(&playbookRunDryRun, "dry-run", false,
		"Preview the playbook without injecting any fault (lists the pods each step hits, checks the blast radius; exits 1 if UNSAFE)")
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
		url, err := apiClient.DisruptionStreamURL(disruptionID)
		if err != nil {
			return err
		}

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
			fmt.Printf("  %-20s %-12s %-5d %s\n",
				output.Printable(e.Name), output.Printable(e.Category), e.Steps, output.Printable(e.Description))
		}
		fmt.Println()
		return nil
	},
}

var disruptionPlaybookShowCmd = &cobra.Command{
	Use:   "show <playbook-name>",
	Short: "Show the disruption plan a playbook runs",
	Long: `Show the disruption plan a playbook runs, as the backend resolves it from
the playbook's YAML: each step's fault, targets and timings, with the defaults
the YAML leaves out filled in.

With -o json the plan is printed as the backend returns it. That JSON is a
complete disruption plan, which 'kates disruption run --config' accepts.`,
	Example: `  kates disruption playbook show leader-cascade
  kates disruption playbook show leader-cascade -o json > plan.json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		raw, err := apiClient.PlaybookPlan(context.Background(), name)
		if err != nil {
			return fmt.Errorf("failed to fetch playbook: %w", err)
		}
		// The backend's JSON, not a re-encoding of the view below, which
		// names only the fields this command prints.
		if outputMode == "json" {
			output.JSON(raw)
			return nil
		}
		var plan playbookPlanView
		if err := json.Unmarshal(raw, &plan); err != nil {
			return fmt.Errorf("failed to read playbook plan: %w", err)
		}
		renderPlaybookPlan(name, &plan)
		return nil
	},
}

var playbookRunDryRun bool

var disruptionPlaybookRunCmd = &cobra.Command{
	Use:   "run <playbook-name>",
	Short: "Execute a pre-built disruption playbook",
	Long: `Execute a pre-built disruption playbook and wait for its report.

With --dry-run the playbook's plan goes to the same dry run as
'kates disruption run --dry-run', and nothing starts: it lists the pods each
step would hit and checks the blast radius, and it exits 1 when the verdict is
UNSAFE, which is when running the playbook would be refused.`,
	Example: `  kates disruption playbook run leader-cascade --dry-run
  kates disruption playbook run leader-cascade`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if playbookRunDryRun {
			// The run endpoint has no dry run, so the preview fetches the plan
			// the playbook runs and posts it to the plan dry run. Both come
			// from the same resolution of the YAML on the backend.
			plan, err := apiClient.PlaybookPlan(context.Background(), name)
			if err != nil {
				return fmt.Errorf("failed to fetch playbook: %w", err)
			}
			return runDryRun(plan)
		}
		// With -o json, stdout carries the result and nothing else, as it
		// does for the dry run.
		if outputMode != "json" {
			fmt.Fprintf(output.Out, "  🎯 Running playbook: %s\n", name)
		}
		result, err := apiClient.PlaybookRun(context.Background(), name)
		if err != nil {
			return fmt.Errorf("failed to run playbook: %w", err)
		}
		if outputMode == "json" {
			output.JSON(result)
			return nil
		}
		fmt.Fprintf(output.Out, "  ✅ Disruption ID: %s\n", output.Printable(result.ID))
		fmt.Fprintf(output.Out, "  📊 Status: %s\n", output.Printable(result.Report.Status))
		if result.Report.SlaVerdict != nil {
			fmt.Fprintf(output.Out, "  🏆 SLA Grade: %s\n", output.Printable(result.Report.SlaVerdict.Grade))
		}
		return nil
	},
}

// playbookPlanView is the part of a resolved playbook plan that
// `playbook show` prints. The plan itself stays raw JSON (-o json and the
// dry run use it as is), so a field missing here is missing only from the
// table.
type playbookPlanView struct {
	Name               string `json:"name"`
	Description        string `json:"description"`
	MaxAffectedBrokers int    `json:"maxAffectedBrokers"`
	AutoRollback       bool   `json:"autoRollback"`
	IsrTrackingTopic   string `json:"isrTrackingTopic"`
	Steps              []struct {
		Name                 string             `json:"name"`
		FaultSpec            *playbookFaultView `json:"faultSpec"`
		SteadyStateSec       int                `json:"steadyStateSec"`
		ObservationWindowSec int                `json:"observationWindowSec"`
		RequireRecovery      bool               `json:"requireRecovery"`
	} `json:"steps"`
}

// playbookFaultView names every FaultSpec field a chaos provider reads to
// choose or size a fault. It leaves out envOverrides and probes, which
// playbook YAML cannot set.
type playbookFaultView struct {
	ExperimentName  string `json:"experimentName"`
	DisruptionType  string `json:"disruptionType"`
	TargetNamespace string `json:"targetNamespace"`
	TargetLabel     string `json:"targetLabel"`
	TargetPod       string `json:"targetPod"`
	TargetAll       bool   `json:"targetAll"`
	// A pointer, because 0 is a broker: a plan without the field must not
	// read as one aimed at broker 0. The backend sends -1 for "none".
	TargetBrokerID   *int   `json:"targetBrokerId"`
	TargetTopic      string `json:"targetTopic"`
	TargetPartition  int    `json:"targetPartition"`
	ChaosDurationSec int    `json:"chaosDurationSec"`
	DelayBeforeSec   int    `json:"delayBeforeSec"`
	GracePeriodSec   int    `json:"gracePeriodSec"`
	FillPercentage   int    `json:"fillPercentage"`
	NetworkLatencyMs int    `json:"networkLatencyMs"`
	CPUCores         int    `json:"cpuCores"`
	MemoryMb         int    `json:"memoryMb"`
	IOWorkers        int    `json:"ioWorkers"`
}

// faultParameters returns the rows for the fields that size a fault of the
// step's type, as the Kubernetes and LitmusChaos providers read them. Every
// fault carries all of them, with defaults where the YAML sets none, so a row
// for a type that ignores the field would describe nothing that runs.
func faultParameters(fs *playbookFaultView) [][2]string {
	switch fs.DisruptionType {
	case "POD_DELETE":
		// The Kubernetes provider deletes with this grace period; the
		// LitmusChaos provider force-deletes, as it does for POD_KILL.
		return [][2]string{{"Grace Period", fmt.Sprintf("%ds", fs.GracePeriodSec)}}
	case "DISK_FILL":
		return [][2]string{{"Fill Percentage", fmt.Sprintf("%d%%", fs.FillPercentage)}}
	case "IO_STRESS":
		// Litmus hands fillPercentage to its IO stress experiment as the
		// filesystem utilisation; both providers read the worker count.
		return [][2]string{
			{"Fill Percentage", fmt.Sprintf("%d%%", fs.FillPercentage)},
			{"IO Workers", fmt.Sprintf("%d", fs.IOWorkers)},
		}
	case "CPU_STRESS":
		return [][2]string{{"CPU Cores", fmt.Sprintf("%d", fs.CPUCores)}}
	case "MEMORY_STRESS":
		return [][2]string{{"Memory", fmt.Sprintf("%d MB", fs.MemoryMb)}}
	case "NETWORK_LATENCY":
		return [][2]string{{"Latency", fmt.Sprintf("%dms", fs.NetworkLatencyMs)}}
	}
	return nil
}

// renderPlaybookPlan prints what each step is set to do. Which pods that
// comes to depends on the cluster (a label match, a partition's current
// leader), so it points at the dry run for that rather than guessing here.
//
// Every string in the plan comes from the backend, so each one goes through
// output.Printable: a description carrying an escape sequence would otherwise
// act on the reader's terminal.
func renderPlaybookPlan(playbook string, plan *playbookPlanView) {
	output.Header("Playbook Plan: " + output.Printable(plan.Name))
	if plan.Description != "" {
		output.KeyValue("Description", output.Printable(plan.Description))
	}
	limit := "none"
	if plan.MaxAffectedBrokers > 0 {
		limit = fmt.Sprintf("%d", plan.MaxAffectedBrokers)
	}
	output.KeyValue("Max Affected Brokers", limit)
	output.KeyValue("Auto Rollback", yesNo(plan.AutoRollback))
	if plan.IsrTrackingTopic != "" {
		output.KeyValue("ISR Tracking Topic", output.Printable(plan.IsrTrackingTopic))
	}

	for i, step := range plan.Steps {
		fs := step.FaultSpec
		kind := "no fault"
		switch {
		case fs == nil:
		case fs.DisruptionType != "":
			kind = output.Printable(fs.DisruptionType)
		default:
			// Not "no fault": with no type, the LitmusChaos provider runs
			// experimentName as the Litmus experiment, whatever it names, and
			// the Kubernetes provider fails the step. The dry run calls this
			// type "unknown".
			kind = "no disruptionType"
		}
		output.SubHeader(fmt.Sprintf("Step %d: %s (%s)", i+1, output.Printable(step.Name), kind))
		if fs != nil {
			if fs.DisruptionType == "" {
				output.KeyValue("Litmus Experiment", output.Printable(fs.ExperimentName))
			}
			output.KeyValue("Namespace", output.Printable(fs.TargetNamespace))
			output.KeyValue("Label Selector", output.Printable(fs.TargetLabel))
			if fs.TargetPod != "" {
				output.KeyValue("Target Pod", output.Printable(fs.TargetPod))
			}
			if fs.TargetAll {
				output.KeyValue("Target All", "yes")
			}
			if fs.TargetTopic != "" {
				output.KeyValue("Leader Of", fmt.Sprintf("%s-%d", output.Printable(fs.TargetTopic), fs.TargetPartition))
			}
			if fs.TargetBrokerID != nil && *fs.TargetBrokerID >= 0 {
				output.KeyValue("Target Broker", fmt.Sprintf("%d", *fs.TargetBrokerID))
			}
			for _, row := range faultParameters(fs) {
				output.KeyValue(row[0], row[1])
			}
			if fs.DelayBeforeSec > 0 {
				output.KeyValue("Delay Before", fmt.Sprintf("%ds", fs.DelayBeforeSec))
			}
			output.KeyValue("Chaos Duration", fmt.Sprintf("%ds", fs.ChaosDurationSec))
		}
		output.KeyValue("Steady State", fmt.Sprintf("%ds", step.SteadyStateSec))
		output.KeyValue("Observation Window", fmt.Sprintf("%ds", step.ObservationWindowSec))
		output.KeyValue("Require Recovery", yesNo(step.RequireRecovery))
	}

	fmt.Fprintln(output.Out)
	output.Hint("See which pods each step hits: kates disruption playbook run " + playbook + " --dry-run")
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
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
