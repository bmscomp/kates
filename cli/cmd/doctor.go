package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bmscomp/kates/cli/output"
	"github.com/spf13/cobra"
)

type checkResult struct {
	Name        string `json:"name"`
	Passed      bool   `json:"passed"`
	Detail      string `json:"detail"`
	Remediation string `json:"remediation"`
}

// optionalCheck reports whether a failed check is a warning rather than a
// failure: Kyverno is recommended, not required.
func optionalCheck(c checkResult) bool {
	return strings.Contains(c.Name, "Kyverno")
}

// doctorResult is what the command returns once the checks are rendered: nil
// when every required check passed, else exit 1. It used to return nil with a
// failing cluster, so a CI step running kates doctor never failed.
func doctorResult(checks []checkResult) error {
	for _, c := range checks {
		if !c.Passed && !optionalCheck(c) {
			return &exitError{code: 1}
		}
	}
	return nil
}

var (
	doctorTimeout string
	doctorKyverno bool
)

var doctorCmd = &cobra.Command{
	Use:     "doctor",
	Aliases: []string{"preflight", "check"},
	Short:   "Pre-flight cluster readiness checklist",
	Long: `Checks that the Kates API is reachable, Kafka is connected with at least three
brokers and in-sync replicas, topics and benchmark backends are there, and,
unless --kyverno=false, that Kyverno is installed with its policies passing.

--timeout bounds the whole checklist: a check still waiting when it runs out
fails with "timed out", and the checks after it fail at once.`,
	Example: `  kates doctor
  kates doctor --timeout 30s
  kates doctor --kyverno=false -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		timeout, err := parseTimeoutFlag(doctorTimeout)
		if err != nil {
			return couldNotTell(err.Error())
		}
		ctx, cancel := context.WithTimeout(commandContext(cmd), timeout)
		defer cancel()

		// Under -o json stdout is the JSON and nothing else.
		if outputMode != "json" {
			output.Banner("Kates Doctor", "Pre-flight cluster readiness")
			fmt.Println()
		}
		checks := runDoctorChecks(ctx, doctorKyverno)
		renderChecks(checks)
		return doctorResult(checks)
	},
}

// doctorDetail is the detail of a check that could not get its answer: that
// the --timeout ran out when it did, else otherwise.
func doctorDetail(ctx context.Context, otherwise string) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timed out: --timeout " + doctorTimeout + " ran out"
	}
	return otherwise
}

// runDoctorChecks runs the checklist within ctx, the Kyverno checks only when
// kyverno is set.
func runDoctorChecks(ctx context.Context, kyverno bool) []checkResult {
	var checks []checkResult
	health, err := apiClient.Health(ctx)
	if err != nil {
		return append(checks, checkResult{"API Reachable", false, doctorDetail(ctx, err.Error()), "Ensure kubeconfig is valid and API server is reachable."})
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
		checks = append(checks, checkResult{"Broker Count ≥ 3", false, doctorDetail(ctx, "Could not query cluster"), "Verify Kafka cluster is running and accessible."})
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
		checks = append(checks, checkResult{"ISR Health", false, doctorDetail(ctx, "Could not check ISR"), "Check kates API logs."})
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
		checks = append(checks, checkResult{"Topics Available", false, doctorDetail(ctx, "Could not list topics"), "Verify Kafka cluster accessibility."})
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
		checks = append(checks, checkResult{"Benchmark Backends", false, doctorDetail(ctx, "No backends available"), "Deploy kates-tester using 'kates deploy'."})
	}

	if kyverno {
		checks = append(checks, kyvernoChecks(ctx)...)
	}
	return checks
}

// kyvernoChecks are the doctor's Kyverno checks, through kubectl.
func kyvernoChecks(ctx context.Context) []checkResult {
	kc := newKubectl("")
	if !kc.CRDExists(ctx, "clusterpolicies.kyverno.io") {
		return []checkResult{{"Kyverno Installed", false, doctorDetail(ctx, "CRD not found"), "Run 'kates kyverno apply' or 'kates deploy --with-kyverno' to install."}}
	}
	checks := []checkResult{{"Kyverno Installed", true, "CRD present", ""}}

	podOut, _ := kc.Output(ctx, "get", "pods", "-n", "kyverno", "-l", "app.kubernetes.io/component=admission-controller", "--field-selector=status.phase=Running", "--no-headers")
	if len(podOut) > 0 {
		checks = append(checks, checkResult{"Kyverno Ready", true, "Admission controller running", ""})
	} else {
		checks = append(checks, checkResult{"Kyverno Ready", false, doctorDetail(ctx, "Admission controller not running"), "Check pod events in 'kyverno' namespace or run 'kubectl get pods -n kyverno'."})
	}

	polOut, _ := kc.Output(ctx, "get", "clusterpolicies", "--no-headers")
	if len(polOut) > 0 {
		checks = append(checks, checkResult{"Kyverno Policies", true, fmt.Sprintf("%d policies active", len(strings.Split(strings.TrimSpace(string(polOut)), "\n"))), ""})
	} else {
		checks = append(checks, checkResult{"Kyverno Policies", false, doctorDetail(ctx, "No active policies"), "Run 'kates kyverno apply' to configure recommended policies."})
	}

	polRepOut, _ := kc.Output(ctx, "get", "clusterpolicyreports,policyreports", "-A", "-o", `jsonpath={range .items[*].results[?(@.result=='fail')]}{.policy}/{.rule} on {.resources[*].kind} {.resources[*].name}: {.message}{"\n"}{end}`)
	outStr := strings.TrimSpace(string(polRepOut))
	switch {
	case len(outStr) > 0:
		failures := strings.Split(outStr, "\n")
		detail := fmt.Sprintf("%d policy violations detected", len(failures))
		remediation := "Review and fix workload configurations. Example: " + failures[0]
		if len(failures) > 1 {
			remediation += fmt.Sprintf(" (and %d more)", len(failures)-1)
		}
		checks = append(checks, checkResult{"Kyverno Violations", false, detail, remediation})
	case ctx.Err() != nil:
		checks = append(checks, checkResult{"Kyverno Violations", false, doctorDetail(ctx, "Could not read policy reports"), ""})
	default:
		checks = append(checks, checkResult{"Kyverno Violations", true, "No workload violations", ""})
	}
	return checks
}

func renderChecks(checks []checkResult) {
	output.Render(outputMode == "json", checks, func() {
		rows := make([][]string, 0, len(checks))
		passed := 0

		// Print a clean table
		for _, c := range checks {
			status := "PASS"
			if !c.Passed {
				status = "FAILED"
				if optionalCheck(c) {
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
	})
}

func init() {
	addTimeoutFlag(doctorCmd, &doctorTimeout, "2m")
	doctorCmd.Flags().BoolVar(&doctorKyverno, "kyverno", true, "Run the Kyverno checks; --kyverno=false skips them, for a cluster without Kyverno")
	rootCmd.AddCommand(doctorCmd)
}
