package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bmscomp/kates/cli/output"
	"github.com/spf13/cobra"
)

// ── Root command ─────────────────────────────────────────────────────────────

var kyvernoCmd = &cobra.Command{
	Use:     "kyverno",
	Aliases: []string{"kyv", "policy"},
	Short:   "Kyverno policy engine — status, violations, and mode management",
	Long: `Manage Kyverno security policies across your cluster.

Commands:
  status       Show all ClusterPolicies with mode, readiness, and rule counts
  violations   Pretty-print policy violations grouped by namespace
  enforce      Switch a policy from Audit to Enforce mode
  audit        Switch a policy from Enforce to Audit mode
  check        Hold the kates chart to the cluster's policies before installing it`,
	Example: `  kates kyverno status
  kates kyverno violations
  kates kyverno violations --namespace kafka
  kates kyverno enforce kates-pod-security-standards
  kates kyverno audit kates-pod-security-standards`,
}

// ── kyverno status ───────────────────────────────────────────────────────────

var kyvernoStatusCmd = &cobra.Command{
	Use:     "status",
	Aliases: []string{"st", "list"},
	Short:   "Show all Kyverno ClusterPolicies with status and rule counts",
	Example: "  kates kyverno status",
	RunE: func(cmd *cobra.Command, args []string) error {
		kc := newKubectl("")
		ctx := context.Background()

		output.Banner("Kyverno Policy Status", "Cluster-wide admission policies")
		fmt.Println()

		// Check if Kyverno is installed
		if !kc.CRDExists(ctx, "clusterpolicies.kyverno.io") {
			output.Error("Kyverno is not installed in this cluster")
			output.Hint("Install with: helm install kyverno kyverno/kyverno -n kyverno --create-namespace")
			return nil
		}

		// Get policies as JSON
		var result struct {
			Items []struct {
				Metadata struct {
					Name string `json:"name"`
				} `json:"metadata"`
				Spec struct {
					ValidationFailureAction string `json:"validationFailureAction"`
					Rules                   []struct {
						Name     string      `json:"name"`
						Mutate   interface{} `json:"mutate"`
						Validate interface{} `json:"validate"`
					} `json:"rules"`
				} `json:"spec"`
				Status struct {
					Ready bool `json:"ready"`
				} `json:"status"`
			} `json:"items"`
		}
		// A kubectl failure exits 1. These four commands used to print it and
		// return nil, so a script saw a clean exit with no status.
		if err := kc.JSON(ctx, &result, "get", "clusterpolicies"); err != nil {
			return cmdErr("Failed to query ClusterPolicies: " + err.Error())
		}

		if len(result.Items) == 0 {
			output.Warn("No ClusterPolicies found")
			output.Hint("Deploy with: helm upgrade --set kyvernoPolicy.enabled=true")
			return nil
		}

		rows := make([][]string, 0, len(result.Items))
		totalValidate, totalMutate := 0, 0

		for _, item := range result.Items {
			validate, mutate := 0, 0
			for _, rule := range item.Spec.Rules {
				if rule.Validate != nil {
					validate++
				}
				if rule.Mutate != nil {
					mutate++
				}
			}
			totalValidate += validate
			totalMutate += mutate

			readyStr := "✗"
			if item.Status.Ready {
				readyStr = "✓"
			}

			action := item.Spec.ValidationFailureAction
			if action == "" {
				action = "Audit"
			}

			rows = append(rows, []string{
				item.Metadata.Name,
				action,
				readyStr,
				fmt.Sprintf("%d", validate),
				fmt.Sprintf("%d", mutate),
			})
		}

		output.Table(
			[]string{"Policy", "Mode", "Ready", "Validate", "Mutate"},
			rows,
		)

		output.Success(fmt.Sprintf("  %d policies active — %d validate rules, %d mutate rules",
			len(result.Items), totalValidate, totalMutate))
		fmt.Println()

		// Show violation summary
		violationOut, err := kc.Output(ctx, "get", "policyreport", "-A", "--no-headers")
		if err == nil && len(violationOut) > 0 {
			totalFail, nsFails := policyReportFailures(string(violationOut))
			if totalFail > 0 {
				output.Warn(fmt.Sprintf("  %d violations detected across %d namespace(s) (run 'kates kyverno violations' for details)",
					totalFail, len(nsFails)))
			} else {
				output.Success("  No policy violations detected")
			}
		}
		fmt.Println()
		return nil
	},
}

// ── kyverno violations ──────────────────────────────────────────────────────

var kyvernoViolationsNs string

var kyvernoViolationsCmd = &cobra.Command{
	Use:     "violations",
	Aliases: []string{"viol", "fails"},
	Short:   "Show policy violations grouped by namespace and pod",
	Example: `  kates kyverno violations
  kates kyverno violations --namespace kafka`,
	RunE: func(cmd *cobra.Command, args []string) error {
		kc := newKubectl("")
		ctx := context.Background()

		output.Banner("Kyverno Violations", "Policy audit report")
		fmt.Println()

		kubectlArgs := []string{"get", "policyreport", "-o", "json"}
		if kyvernoViolationsNs != "" {
			kubectlArgs = append(kubectlArgs, "-n", kyvernoViolationsNs)
		} else {
			kubectlArgs = append(kubectlArgs, "-A")
		}

		out, err := kc.Output(ctx, kubectlArgs...)
		if err != nil {
			return cmdErr("Failed to query PolicyReports: " + err.Error())
		}

		var reportList struct {
			Items []struct {
				Metadata struct {
					Namespace string `json:"namespace"`
				} `json:"metadata"`
				Scope *struct {
					Name string `json:"name"`
				} `json:"scope"`
				Results []struct {
					Policy  string `json:"policy"`
					Rule    string `json:"rule"`
					Result  string `json:"result"`
					Message string `json:"message"`
				} `json:"results"`
			} `json:"items"`
		}
		if err := json.Unmarshal(out, &reportList); err != nil {
			return cmdErr("Failed to parse report data: " + err.Error())
		}

		totalFails := 0
		nsViolations := map[string][]struct {
			pod, rule, message string
		}{}

		for _, item := range reportList.Items {
			ns := item.Metadata.Namespace
			podName := "unknown"
			if item.Scope != nil {
				podName = item.Scope.Name
			}
			for _, r := range item.Results {
				if r.Result == "fail" {
					totalFails++
					msg := r.Message
					if len(msg) > 80 {
						msg = msg[:77] + "..."
					}
					nsViolations[ns] = append(nsViolations[ns], struct{ pod, rule, message string }{
						pod: podName, rule: r.Rule, message: msg,
					})
				}
			}
		}

		if totalFails == 0 {
			output.Success("  No policy violations found — all pods are compliant!")
			fmt.Println()
			return nil
		}

		for ns, violations := range nsViolations {
			output.SubHeader(fmt.Sprintf("Namespace: %s (%d violations)", ns, len(violations)))
			rows := make([][]string, len(violations))
			for i, v := range violations {
				rows[i] = []string{v.pod, v.rule, v.message}
			}
			output.Table([]string{"Pod", "Rule", "Message"}, rows)
			fmt.Println()
		}

		output.Warn(fmt.Sprintf("  Total: %d violations across %d namespace(s)",
			totalFails, len(nsViolations)))
		fmt.Println()
		return nil
	},
}

// ── kyverno enforce ─────────────────────────────────────────────────────────

var kyvernoEnforceCmd = &cobra.Command{
	Use:     "enforce [policy-name]",
	Short:   "Switch a ClusterPolicy from Audit to Enforce mode",
	Args:    cobra.ExactArgs(1),
	Example: "  kates kyverno enforce kates-pod-security-standards",
	RunE: func(cmd *cobra.Command, args []string) error {
		kc := newKubectl("")
		ctx := context.Background()
		policyName := args[0]
		patchJSON := `{"spec":{"validationFailureAction":"Enforce"}}`

		_, err := kc.Run(ctx, "patch", "clusterpolicy", policyName,
			"--type=merge", "-p", patchJSON)
		if err != nil {
			return cmdErr(fmt.Sprintf("Failed to enforce policy '%s': %v", policyName, err))
		}
		output.Success(fmt.Sprintf("  Policy '%s' switched to Enforce mode", policyName))
		output.Warn("  ⚠ Non-compliant pods will now be BLOCKED from deploying")
		fmt.Println()
		return nil
	},
}

// ── kyverno audit ───────────────────────────────────────────────────────────

var kyvernoAuditCmd = &cobra.Command{
	Use:     "audit [policy-name]",
	Short:   "Switch a ClusterPolicy from Enforce to Audit mode",
	Args:    cobra.ExactArgs(1),
	Example: "  kates kyverno audit kates-pod-security-standards",
	RunE: func(cmd *cobra.Command, args []string) error {
		kc := newKubectl("")
		ctx := context.Background()
		policyName := args[0]
		patchJSON := `{"spec":{"validationFailureAction":"Audit"}}`

		_, err := kc.Run(ctx, "patch", "clusterpolicy", policyName,
			"--type=merge", "-p", patchJSON)
		if err != nil {
			return cmdErr(fmt.Sprintf("Failed to switch policy '%s' to Audit: %v", policyName, err))
		}
		output.Success(fmt.Sprintf("  Policy '%s' switched to Audit mode", policyName))
		output.Hint("  Violations will be logged but NOT blocked")
		fmt.Println()
		return nil
	},
}

// ── Registration ─────────────────────────────────────────────────────────────

func init() {
	kyvernoViolationsCmd.Flags().StringVarP(&kyvernoViolationsNs, "namespace", "n", "", "Filter violations by namespace")

	kyvernoCmd.AddCommand(kyvernoStatusCmd)
	kyvernoCmd.AddCommand(kyvernoViolationsCmd)
	kyvernoCmd.AddCommand(kyvernoEnforceCmd)
	kyvernoCmd.AddCommand(kyvernoAuditCmd)
	rootCmd.AddCommand(kyvernoCmd)
}

// policyReportFailures counts the failed results in `kubectl get policyreport
// -A --no-headers` output, in all and per namespace. FAIL is the sixth
// column; a line with fewer columns, or a count that is not a number, adds
// nothing.
func policyReportFailures(out string) (total int, perNamespace map[string]int) {
	perNamespace = map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		var fail int
		if _, err := fmt.Sscanf(fields[5], "%d", &fail); err != nil {
			continue
		}
		total += fail
		if fail > 0 {
			perNamespace[fields[0]] += fail
		}
	}
	return total, perNamespace
}
