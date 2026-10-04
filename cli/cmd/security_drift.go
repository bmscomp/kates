package cmd

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/bmscomp/kates/cli/output"
	"github.com/spf13/cobra"
)

var securityBaselineCmd = &cobra.Command{
	Use:     "baseline",
	Aliases: []string{"base"},
	Short:   "Save current security posture as baseline for drift detection",
	Example: `  kates security baseline --save
  kates security drift`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !baselineSave {
			return cmdErr("Use --save to capture the current posture as baseline.\nThen run 'kates security drift' to compare.")
		}

		result, err := apiClient.SecurityBaselineSave(context.Background())
		if err != nil {
			return cmdErr("Baseline save failed: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(result)
			return nil
		}

		output.Banner("Security Baseline", "Snapshot Saved")
		output.KeyValue("Status", output.SuccessStyle.Render("Saved"))
		output.KeyValue("Grade", gradeStyle(fmt.Sprintf("%v", result["grade"])))
		output.KeyValue("Checks", fmt.Sprintf("%v", result["checks"]))
		output.KeyValue("Timestamp", fmt.Sprintf("%v", result["timestamp"]))

		return nil
	},
}

var securityDriftCmd = &cobra.Command{
	Use:     "drift",
	Short:   "Compare current security posture against saved baseline",
	Example: "  kates security drift",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := apiClient.SecurityDrift(context.Background())
		if err != nil {
			return cmdErr("Drift detection failed: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(result)
			return nil
		}

		if errMsg, ok := result["error"].(string); ok {
			return cmdErr(errMsg)
		}

		baseGrade := fmt.Sprintf("%v", result["baselineGrade"])
		currGrade := fmt.Sprintf("%v", result["currentGrade"])
		output.Banner("Security Drift", "Baseline "+gradeStyle(baseGrade)+" → Current "+gradeStyle(currGrade))

		drifts, _ := result["drifts"].([]interface{})
		if len(drifts) > 0 {
			rows := make([][]string, 0, len(drifts))
			for _, d := range drifts {
				drift, ok := d.(map[string]interface{})
				if !ok {
					continue
				}
				change := fmt.Sprintf("%v", drift["change"])
				icon := " "
				switch change {
				case "IMPROVED":
					icon = output.SuccessStyle.Render("↑")
				case "DEGRADED":
					icon = output.ErrorStyle.Render("↓")
				case "UNCHANGED":
					icon = "="
				}

				detail := ""
				if fix, ok := drift["fix"]; ok && fix != nil {
					detail = truncate(fmt.Sprintf("%v", fix), 45)
				}

				rows = append(rows, []string{
					icon,
					fmt.Sprintf("%v", drift["check"]),
					fmt.Sprintf("%v", drift["baseline"]),
					fmt.Sprintf("%v", drift["current"]),
					detail,
				})
			}
			output.Table([]string{"", "Check", "Baseline", "Current", "Fix"}, rows)
		}

		summary, _ := result["summary"].(map[string]interface{})
		if summary != nil {
			fmt.Println()
			output.KeyValue("Improved", output.SuccessStyle.Render(fmt.Sprintf("%v", summary["improved"])))
			output.KeyValue("Degraded", output.ErrorStyle.Render(fmt.Sprintf("%v", summary["degraded"])))
			output.KeyValue("Unchanged", fmt.Sprintf("%v", summary["unchanged"]))
		}

		return nil
	},
}

var securityGateCmd = &cobra.Command{
	Use:   "gate",
	Short: "CI/CD security gate — exit non-zero if grade is below threshold",
	Example: `  kates security gate --min-grade B
  kates security gate --min-grade A -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := apiClient.SecurityGate(context.Background(), secGateMinGrade)
		if err != nil {
			return cmdErr("Security gate failed: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(result)
			passed, _ := result["passed"].(bool)
			if !passed {
				return &silentErr{msg: "security gate failed"}
			}
			return nil
		}

		passed, _ := result["passed"].(bool)
		currentGrade := fmt.Sprintf("%v", result["currentGrade"])
		requiredGrade := fmt.Sprintf("%v", result["requiredGrade"])

		if passed {
			output.Banner("Security Gate", output.SuccessStyle.Render("PASSED")+"  │  "+gradeStyle(currentGrade)+" ≥ "+gradeStyle(requiredGrade))
		} else {
			output.Banner("Security Gate", output.ErrorStyle.Render("FAILED")+"  │  "+gradeStyle(currentGrade)+" < "+gradeStyle(requiredGrade))

			failingChecks, _ := result["failingChecks"].([]interface{})
			if len(failingChecks) > 0 {
				fixWidth := output.ColumnWidth(36, 30)

				fmt.Println()
				output.SubHeader("Failing Checks (fix to raise grade)")
				rows := make([][]string, 0, len(failingChecks))
				for _, f := range failingChecks {
					fc, ok := f.(map[string]interface{})
					if !ok {
						continue
					}
					rows = append(rows, []string{
						statusIcon(fmt.Sprintf("%v", fc["status"])),
						fmt.Sprintf("%v", fc["check"]),
						truncate(fmt.Sprintf("%v", fc["fix"]), fixWidth),
					})
				}
				output.Table([]string{"", "Check", "Remediation"}, rows)
			}

			return cmdErr("Security gate failed: grade " + currentGrade + " is below required " + requiredGrade)
		}

		return nil
	},
}

var securityNetpolCmd = &cobra.Command{
	Use:     "netpol",
	Aliases: []string{"network", "network-policy"},
	Short:   "Audit Kubernetes NetworkPolicies around Kafka pods",
	Example: "  kates security netpol",
	RunE: func(cmd *cobra.Command, args []string) error {
		if outputMode == "json" {
			return cmdErr("JSON mode not supported for netpol — run kubectl directly")
		}

		output.Banner("Network Policy Audit", "Kubernetes NetworkPolicy Inspection")

		nameWidth := output.ColumnWidth(50, 30)

		seen := make(map[string]bool)
		defaults := []string{"kafka", "kates", "strimzi-system"}
		for _, ns := range defaults {
			seen[ns] = true
		}

		// Dynamically discover namespaces with Helm releases (owner=helm)
		if dynamicNSList, err := runKubectl("get", "secrets", "-A", "-l", "owner=helm", "-o", "jsonpath={.items[*].metadata.namespace}"); err == nil {
			for _, ns := range strings.Fields(dynamicNSList) {
				seen[ns] = true
			}
		}

		var namespaces []string
		for ns := range seen {
			namespaces = append(namespaces, ns)
		}
		sort.Strings(namespaces)

		totalPolicies := 0

		for _, ns := range namespaces {
			out, err := runKubectl("get", "networkpolicies", "-n", ns, "-o", "jsonpath={range .items[*]}{.metadata.name}:{.spec.podSelector.matchLabels}:{.spec.policyTypes[*]}\n{end}")
			if err != nil {
				output.KeyValue(ns, output.DimStyle.Render("namespace not found or no access"))
				continue
			}
			lines := strings.Split(strings.TrimSpace(out), "\n")
			if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
				fmt.Println()
				output.Warn(ns + ": No NetworkPolicies found")
				continue
			}

			fmt.Println()
			output.SubHeader(fmt.Sprintf("%s (%d policies)", ns, len(lines)))
			rows := make([][]string, 0, len(lines))
			for _, line := range lines {
				parts := strings.SplitN(line, ":", 3)
				name := parts[0]
				selector := ""
				types := ""
				if len(parts) > 1 {
					selector = parts[1]
				}
				if len(parts) > 2 {
					types = parts[2]
				}
				rows = append(rows, []string{
					"✓",
					truncate(name, nameWidth),
					selector,
					types,
				})
			}
			output.Table([]string{"", "Policy", "Pod Selector", "Types"}, rows)
			totalPolicies += len(lines)
		}

		ingress, _ := runKubectl("get", "networkpolicies", "-A", "-o", "jsonpath={range .items[?(@.spec.ingress)]}{.metadata.namespace}/{.metadata.name}\n{end}")
		ingressCount := 0
		if ingress != "" {
			ingressCount = len(strings.Split(strings.TrimSpace(ingress), "\n"))
		}

		fmt.Println()
		output.KeyValue("Total Policies", fmt.Sprintf("%d", totalPolicies))
		output.KeyValue("Policies with Ingress Rules", fmt.Sprintf("%d", ingressCount))
		if totalPolicies == 0 {
			output.Warn("No NetworkPolicies found — Kafka pods are exposed to all namespaces")
		}

		return nil
	},
}
