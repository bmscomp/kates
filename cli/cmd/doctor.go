package cmd

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

type checkResult struct {
	Name        string
	Passed      bool
	Detail      string
	Remediation string
}

var doctorCmd = &cobra.Command{
	Use:     "doctor",
	Aliases: []string{"preflight", "check"},
	Short:   "Pre-flight cluster readiness checklist",
	Example: "  kates doctor",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		var checks []checkResult

		output.Banner("Kates Doctor", "Pre-flight cluster readiness")
		fmt.Println()

		health, err := apiClient.Health(ctx)
		if err != nil {
			checks = append(checks, checkResult{"API Reachable", false, err.Error(), "Ensure kubeconfig is valid and API server is reachable."})
			renderChecks(checks)
			return nil
		}
		checks = append(checks, checkResult{"API Reachable", true, "Connected", ""})

		if health.Kafka != nil && health.Kafka.Status == "UP" {
			checks = append(checks, checkResult{"Kafka Connected", true, health.Kafka.BootstrapServers, ""})
		} else {
			msg := "Kafka unreachable"
			if health.Kafka != nil {
				msg = health.Kafka.Message
			}
			checks = append(checks, checkResult{"Kafka Connected", false, msg, "Deploy Kafka using 'kates deploy' or check broker logs."})
		}

		cluster, err := apiClient.ClusterInfo(ctx)
		if err == nil && cluster != nil {
			count := 0
			if brokers := cluster.Brokers; brokers != nil {
				count = len(brokers)
			}
			checks = append(checks, checkResult{
				"Broker Count ≥ 3",
				count >= 3,
				fmt.Sprintf("%d brokers detected", count),
				"Ensure at least 3 brokers are deployed for high availability.",
			})
		} else {
			checks = append(checks, checkResult{"Broker Count ≥ 3", false, "Could not query cluster", "Verify Kafka cluster is running and accessible."})
		}

		clusterHealth, err := apiClient.ClusterCheck(ctx)
		if err == nil && clusterHealth != nil {
			urp := clusterHealth.PartitionHealth.UnderReplicated
			offline := clusterHealth.PartitionHealth.Offline
			healthy := urp == 0 && offline == 0
			detail := "All replicas in sync"
			if !healthy {
				detail = fmt.Sprintf("%d under-replicated, %d offline", urp, offline)
			}
			checks = append(checks, checkResult{"ISR Health", healthy, detail, "Run 'kates advisor' to check broker health and partition assignments."})
		} else {
			checks = append(checks, checkResult{"ISR Health", false, "Could not check ISR", "Check kates API logs."})
		}

		topics, err := apiClient.Topics(ctx)
		if err == nil {
			checks = append(checks, checkResult{
				"Topics Available",
				len(topics) > 0,
				fmt.Sprintf("%d topics found", len(topics)),
				"Create topics manually or run a benchmark to generate them.",
			})
		} else {
			checks = append(checks, checkResult{"Topics Available", false, "Could not list topics", "Verify Kafka cluster accessibility."})
		}

		backends, err := apiClient.Backends(ctx)
		if err == nil {
			checks = append(checks, checkResult{
				"Benchmark Backends",
				len(backends) > 0,
				fmt.Sprintf("%v", backends),
				"Deploy a benchmarking backend like kates-tester.",
			})
		} else {
			checks = append(checks, checkResult{"Benchmark Backends", false, "No backends available", "Deploy kates-tester using 'kates deploy'."})
		}

		// Kyverno Checks
		crdOut, _ := exec.Command("kubectl", "get", "crd", "clusterpolicies.kyverno.io", "--no-headers").Output()
		if len(crdOut) > 0 {
			checks = append(checks, checkResult{"Kyverno Installed", true, "CRD present", ""})
			
			podOut, _ := exec.Command("kubectl", "get", "pods", "-n", "kyverno", "-l", "app.kubernetes.io/component=admission-controller", "--field-selector=status.phase=Running", "--no-headers").Output()
			if len(podOut) > 0 {
				checks = append(checks, checkResult{"Kyverno Ready", true, "Admission controller running", ""})
			} else {
				checks = append(checks, checkResult{"Kyverno Ready", false, "Admission controller not running", "Check pod events in 'kyverno' namespace or run 'kubectl get pods -n kyverno'."})
			}

			polOut, _ := exec.Command("kubectl", "get", "clusterpolicies", "--no-headers").Output()
			if len(polOut) > 0 {
				checks = append(checks, checkResult{"Kyverno Policies", true, fmt.Sprintf("%d policies active", len(strings.Split(strings.TrimSpace(string(polOut)), "\n"))), ""})
			} else {
				checks = append(checks, checkResult{"Kyverno Policies", false, "No active policies", "Run 'kates kyverno apply' to configure recommended policies."})
			}

			polRepOut, _ := exec.Command("kubectl", "get", "clusterpolicyreports,policyreports", "-A", "-o", "jsonpath={range .items[*].results[?(@.result=='fail')]}{.policy}/{.rule} on {.resources[*].kind} {.resources[*].name}: {.message}\\n").Output()
			outStr := strings.TrimSpace(string(polRepOut))
			if len(outStr) > 0 {
				failures := strings.Split(outStr, "\n")
				detail := fmt.Sprintf("%d policy violations detected", len(failures))
				remediation := "Review and fix workload configurations. Example: " + failures[0]
				if len(failures) > 1 {
					remediation += fmt.Sprintf(" (and %d more)", len(failures)-1)
				}
				checks = append(checks, checkResult{"Kyverno Violations", false, detail, remediation})
			} else {
				checks = append(checks, checkResult{"Kyverno Violations", true, "No workload violations", ""})
			}

		} else {
			checks = append(checks, checkResult{"Kyverno Installed", false, "CRD not found", "Run 'kates kyverno apply' or 'kates deploy --with-kyverno' to install."})
		}

		renderChecks(checks)
		return nil
	},
}

func renderChecks(checks []checkResult) {
	rows := make([][]string, 0, len(checks))
	passed := 0
	
	// Print a clean table
	for _, c := range checks {
		status := "PASS"
		if !c.Passed {
			status = "FAILED"
			if strings.Contains(c.Name, "Kyverno") {
				status = "WARN" // Kyverno is optional but recommended
			}
		} else {
			passed++
		}
		rows = append(rows, []string{c.Name, status, c.Detail})
	}
	output.Table([]string{"Check", "Status", "Detail"}, rows)

	// Print Remediations separately for clarity and deep verbosity
	hasRemediations := false
	for _, c := range checks {
		if !c.Passed && c.Remediation != "" {
			if !hasRemediations {
				output.SubHeader("Remediations")
				hasRemediations = true
			}
			output.Hint(fmt.Sprintf("%s: %s", output.KeyStyle.Render(c.Name), c.Remediation))
		}
	}
	if hasRemediations {
		fmt.Println()
	}

	summary := fmt.Sprintf("%d/%d checks passed", passed, len(checks))
	if passed == len(checks) {
		output.Success("✓ " + summary + " — cluster is ready for testing!")
	} else {
		output.Warn("⚠ " + summary + " — review failing checks above")
	}
	fmt.Println()
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}
