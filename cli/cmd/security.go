package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var securityCmd = &cobra.Command{
	Use:     "security",
	Aliases: []string{"sec"},
	Short:   "Kafka security auditing, ACL testing, TLS inspection, and penetration testing",
	Example: `  kates security audit
  kates security tls-inspect
  kates security auth-test --user kafka-ui
  kates security pentest --test metadata-leak`,
}

var securityAuditCmd = &cobra.Command{
	Use:     "audit",
	Aliases: []string{"scan"},
	Short:   "Run a full security posture audit with A-F grading",
	Example: "  kates security audit",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := apiClient.SecurityAudit(context.Background())
		if err != nil {
			return cmdErr("Security audit failed: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(result)
			return nil
		}

		grade := fmt.Sprintf("%v", result["grade"])
		output.Banner("Security Audit", "Grade: "+gradeStyle(grade)+"  │  Kafka Cluster Posture Scan")

		if errMsg, ok := result["error"].(string); ok {
			fmt.Println()
			output.Error(errMsg)
			return nil
		}

		checks, _ := result["checks"].([]interface{})
		if len(checks) > 0 {
			rows := make([][]string, 0, len(checks))
			for _, c := range checks {
				check, ok := c.(map[string]interface{})
				if !ok {
					continue
				}
				name := fmt.Sprintf("%v", check["name"])
				status := fmt.Sprintf("%v", check["status"])
				detail := fmt.Sprintf("%v", check["detail"])
				severity := fmt.Sprintf("%v", check["severity"])
				cis := fmt.Sprintf("%v", check["compliance"])
				rows = append(rows, []string{statusIcon(status), cis, name, severity, truncate(detail, 55)})
			}
			output.Table([]string{"", "CIS", "Check", "Severity", "Detail"}, rows)

			hasIssues := false
			for _, c := range checks {
				check, ok := c.(map[string]interface{})
				if !ok {
					continue
				}
				status := fmt.Sprintf("%v", check["status"])
				if status != "PASS" {
					if !hasIssues {
						fmt.Println()
						output.SubHeader("Remediation")
						hasIssues = true
					}
					fix := fmt.Sprintf("%v", check["fix"])
					name := fmt.Sprintf("%v", check["name"])
					fmt.Printf("  %s  %s\n     %s\n", statusIcon(status), name, fix)
				}
			}
		}

		summary, _ := result["summary"].(map[string]interface{})
		if summary != nil {
			fmt.Println()
			output.KeyValue("Total Checks", fmt.Sprintf("%v", summary["total"]))
			output.KeyValue("Passed", output.SuccessStyle.Render(fmt.Sprintf("%v", summary["passed"])))
			output.KeyValue("Warnings", output.WarningStyle.Render(fmt.Sprintf("%v", summary["warnings"])))
			output.KeyValue("Failures", output.ErrorStyle.Render(fmt.Sprintf("%v", summary["failures"])))
			output.KeyValue("Grade", gradeStyle(grade))
		}

		return nil
	},
}

var (
	authTestUser string
	pentestName  string
	secGateMinGrade string
	baselineSave bool
)

var securityTLSCmd = &cobra.Command{
	Use:     "tls-inspect",
	Aliases: []string{"tls"},
	Short:   "Inspect TLS configuration, protocol versions, and cipher suites",
	Example: "  kates security tls-inspect",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := apiClient.SecurityTLS(context.Background())
		if err != nil {
			return cmdErr("TLS inspection failed: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(result)
			return nil
		}

		output.Banner("TLS Inspection", "Certificate & Protocol Analysis")

		checks, _ := result["checks"].([]interface{})
		if len(checks) > 0 {
			rows := make([][]string, 0, len(checks))
			for _, c := range checks {
				check, ok := c.(map[string]interface{})
				if !ok {
					continue
				}
				rows = append(rows, []string{
					statusIcon(fmt.Sprintf("%v", check["status"])),
					fmt.Sprintf("%v", check["name"]),
					fmt.Sprintf("%v", check["detail"]),
				})
			}
			output.Table([]string{"", "Check", "Detail"}, rows)
		}

		return nil
	},
}

var securityAuthTestCmd = &cobra.Command{
	Use:     "auth-test",
	Aliases: []string{"auth"},
	Short:   "Probe ACL rules for a specific user to verify least-privilege access",
	Example: "  kates security auth-test --user kafka-ui",
	RunE: func(cmd *cobra.Command, args []string) error {
		if authTestUser == "" {
			return cmdErr("--user flag is required. Example: kates security auth-test --user kafka-ui")
		}

		result, err := apiClient.SecurityAuthTest(context.Background(), authTestUser)
		if err != nil {
			return cmdErr("Auth test failed: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(result)
			return nil
		}

		output.Banner("ACL Auth Test", "User: "+authTestUser)

		checks, _ := result["checks"].([]interface{})
		if len(checks) > 0 {
			rows := make([][]string, 0, len(checks))
			for _, c := range checks {
				check, ok := c.(map[string]interface{})
				if !ok {
					continue
				}
				rows = append(rows, []string{
					statusIcon(fmt.Sprintf("%v", check["status"])),
					fmt.Sprintf("%v", check["name"]),
					fmt.Sprintf("%v", check["detail"]),
				})
			}
			output.Table([]string{"", "Check", "Detail"}, rows)
		}

		aclList, _ := result["acls"].([]interface{})
		if len(aclList) > 0 {
			fmt.Println()
			output.SubHeader(fmt.Sprintf("ACL Rules for User:%s (%d)", authTestUser, len(aclList)))
			rows := make([][]string, 0, len(aclList))
			for _, a := range aclList {
				acl, ok := a.(map[string]interface{})
				if !ok {
					continue
				}
				rows = append(rows, []string{
					fmt.Sprintf("%v", acl["resource"]),
					fmt.Sprintf("%v", acl["name"]),
					fmt.Sprintf("%v", acl["pattern"]),
					fmt.Sprintf("%v", acl["operation"]),
					fmt.Sprintf("%v", acl["permission"]),
				})
			}
			output.Table([]string{"Resource", "Name", "Pattern", "Operation", "Permission"}, rows)
		}

		return nil
	},
}

var securityPentestCmd = &cobra.Command{
	Use:     "pentest",
	Aliases: []string{"pen"},
	Short:   "Run adversarial penetration tests against the cluster",
	Example: `  kates security pentest
  kates security pentest --test metadata-leak
  kates security pentest --test auto-create`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if pentestName == "" {
			pentestName = "all"
		}

		result, err := apiClient.SecurityPentest(context.Background(), pentestName)
		if err != nil {
			return cmdErr("Pentest failed: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(result)
			return nil
		}

		output.Banner("Penetration Test", "Adversarial Security Assessment")

		tests, _ := result["tests"].([]interface{})
		if len(tests) > 0 {
			rows := make([][]string, 0, len(tests))
			for _, t := range tests {
				test, ok := t.(map[string]interface{})
				if !ok {
					continue
				}
				res := fmt.Sprintf("%v", test["result"])
				icon := "✓"
				if res == "VULNERABLE" {
					icon = "✗"
				}
				rows = append(rows, []string{
					icon,
					fmt.Sprintf("%v", test["name"]),
					res,
					fmt.Sprintf("%v", test["severity"]),
					truncate(fmt.Sprintf("%v", test["detail"]), 50),
				})
			}
			output.Table([]string{"", "Test", "Result", "Severity", "Detail"}, rows)
		}

		summary, _ := result["summary"].(map[string]interface{})
		if summary != nil {
			fmt.Println()
			output.KeyValue("Total Tests", fmt.Sprintf("%v", summary["total"]))
			output.KeyValue("Protected", output.SuccessStyle.Render(fmt.Sprintf("%v", summary["protected"])))
			output.KeyValue("Vulnerable", output.ErrorStyle.Render(fmt.Sprintf("%v", summary["vulnerable"])))
		}

		return nil
	},
}

var securityComplianceCmd = &cobra.Command{
	Use:     "compliance",
	Aliases: []string{"comply"},
	Short:   "Map security checks to CIS Kafka Benchmark, SOC2, and PCI-DSS frameworks",
	Example: "  kates security compliance",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := apiClient.SecurityCompliance(context.Background())
		if err != nil {
			return cmdErr("Compliance report failed: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(result)
			return nil
		}

		grade := fmt.Sprintf("%v", result["grade"])
		output.Banner("Compliance Report", "Security Grade: "+gradeStyle(grade))

		frameworks := []string{"CIS Kafka Benchmark", "SOC2 Type II", "PCI-DSS v4.0"}
		for _, fw := range frameworks {
			fwData, ok := result[fw].(map[string]interface{})
			if !ok {
				continue
			}

			compliance := fmt.Sprintf("%v", fwData["compliance"])
			fmt.Println()
			output.SubHeader(fmt.Sprintf("%s  (%s compliant)", fw, compliance))

			controls, _ := fwData["controls"].([]interface{})
			if len(controls) > 0 {
				rows := make([][]string, 0, len(controls))
				for _, ctrl := range controls {
					c, ok := ctrl.(map[string]interface{})
					if !ok {
						continue
					}
					rows = append(rows, []string{
						statusIcon(fmt.Sprintf("%v", c["status"])),
						fmt.Sprintf("%v", c["controlId"]),
						fmt.Sprintf("%v", c["check"]),
						truncate(fmt.Sprintf("%v", c["fix"]), 50),
					})
				}
				output.Table([]string{"", "Control", "Check", "Remediation"}, rows)
			}
		}

		return nil
	},
}

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
	Use:     "gate",
	Short:   "CI/CD security gate — exit non-zero if grade is below threshold",
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
				os.Exit(1)
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
						truncate(fmt.Sprintf("%v", fc["fix"]), 55),
					})
				}
				output.Table([]string{"", "Check", "Remediation"}, rows)
			}

			os.Exit(1)
		}

		return nil
	},
}

func statusIcon(status string) string {
	switch strings.ToUpper(status) {
	case "PASS":
		return output.SuccessStyle.Render("✓")
	case "WARN":
		return output.WarningStyle.Render("▲")
	case "FAIL":
		return output.ErrorStyle.Render("✗")
	default:
		return "?"
	}
}

func gradeStyle(grade string) string {
	switch grade {
	case "A":
		return output.SuccessStyle.Render("A")
	case "B":
		return output.SuccessStyle.Render("B")
	case "C":
		return output.WarningStyle.Render("C")
	case "D":
		return output.WarningStyle.Render("D")
	case "F":
		return output.ErrorStyle.Render("F")
	default:
		return grade
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

func init() {
	securityAuthTestCmd.Flags().StringVar(&authTestUser, "user", "", "Kafka username to test ACLs for")
	securityPentestCmd.Flags().StringVar(&pentestName, "test", "all", "Specific pentest to run (auto-create, large-message, metadata-leak, connection-flood, unencrypted, acl-bypass, or all)")
	securityBaselineCmd.Flags().BoolVar(&baselineSave, "save", false, "Save current posture as baseline")
	securityGateCmd.Flags().StringVar(&secGateMinGrade, "min-grade", "B", "Minimum passing grade (A, B, C, D, F)")

	securityCmd.AddCommand(securityAuditCmd)
	securityCmd.AddCommand(securityTLSCmd)
	securityCmd.AddCommand(securityAuthTestCmd)
	securityCmd.AddCommand(securityPentestCmd)
	securityCmd.AddCommand(securityComplianceCmd)
	securityCmd.AddCommand(securityBaselineCmd)
	securityCmd.AddCommand(securityDriftCmd)
	securityCmd.AddCommand(securityGateCmd)

	rootCmd.AddCommand(securityCmd)
}
