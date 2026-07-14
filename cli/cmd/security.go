package cmd

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/bmscomp/kates/cli/output"
	"github.com/spf13/cobra"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
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

		// Inject Kyverno checks
		checks, _ := result["checks"].([]interface{})
		crdOut, _ := exec.Command("kubectl", "get", "crd", "clusterpolicies.kyverno.io", "--no-headers").Output()
		if len(crdOut) > 0 {
			checks = append(checks, map[string]interface{}{
				"category":   "policy",
				"name":       "Kyverno Admission Controller",
				"status":     "PASS",
				"detail":     "Kyverno is installed and protecting the cluster",
				"severity":   "HIGH",
				"compliance": "CIS 5.1",
			})

			polOut, _ := exec.Command("kubectl", "get", "clusterpolicies", "-o", "jsonpath={range .items[*]}{.metadata.name}={.spec.validationFailureAction}{\"\\n\"}{end}").Output()
			policies := strings.Split(strings.TrimSpace(string(polOut)), "\n")
			activeCount, enforceCount := 0, 0
			for _, p := range policies {
				if p == "" {
					continue
				}
				activeCount++
				if strings.Contains(p, "Enforce") {
					enforceCount++
				}
			}

			if activeCount > 0 {
				checks = append(checks, map[string]interface{}{
					"category":   "policy",
					"name":       "Active ClusterPolicies",
					"status":     "PASS",
					"detail":     fmt.Sprintf("%d active policies detected", activeCount),
					"severity":   "HIGH",
					"compliance": "CIS 5.2",
				})
			} else {
				checks = append(checks, map[string]interface{}{
					"category":   "policy",
					"name":       "Active ClusterPolicies",
					"status":     "FAIL",
					"detail":     "No policies are active",
					"severity":   "HIGH",
					"compliance": "CIS 5.2",
					"fix":        "Run 'kates kyverno apply' to deploy recommended policies.",
				})
			}

			if enforceCount > 0 {
				checks = append(checks, map[string]interface{}{
					"category":   "policy",
					"name":       "Policy Enforcement",
					"status":     "PASS",
					"detail":     fmt.Sprintf("%d policies are in Enforce mode", enforceCount),
					"severity":   "MEDIUM",
					"compliance": "CIS 5.3",
				})
			} else {
				checks = append(checks, map[string]interface{}{
					"category":   "policy",
					"name":       "Policy Enforcement",
					"status":     "WARN",
					"detail":     "Policies are in Audit mode only",
					"severity":   "MEDIUM",
					"compliance": "CIS 5.3",
					"fix":        "Test workloads and run 'kates kyverno enforce <policy>' when ready.",
				})
			}
			// Detect workload violations and penalize grade
			polRepOut, err := exec.Command("kubectl", "get", "clusterpolicyreports,policyreports", "-A", "-o", "jsonpath={range .items[*].results[?(@.result=='fail')]}{.policy}/{.rule} on {.resources[*].kind} {.resources[*].name}: {.message}{\"\\n\"}{end}").Output()
			if err == nil {
				outStr := strings.TrimSpace(string(polRepOut))
				if len(outStr) > 0 {
					failures := strings.Split(outStr, "\n")

					// Downgrade grade dynamically
					gradeStr := fmt.Sprintf("%v", result["grade"])
					if gradeStr == "A" {
						result["grade"] = "B"
					} else if gradeStr == "B" {
						result["grade"] = "C"
					} else if gradeStr == "C" {
						result["grade"] = "D"
					} else if gradeStr == "D" {
						result["grade"] = "F"
					}

					checks = append(checks, map[string]interface{}{
						"category":   "policy",
						"name":       "Workload Policy Violations",
						"status":     "FAIL",
						"detail":     fmt.Sprintf("%d workload violations detected", len(failures)),
						"severity":   "HIGH",
						"compliance": "CIS 5.4",
						"fix":        "Fix resource configurations. Example: " + failures[0],
					})
				} else {
					checks = append(checks, map[string]interface{}{
						"category":   "policy",
						"name":       "Workload Policy Violations",
						"status":     "PASS",
						"detail":     "No workload violations detected",
						"severity":   "HIGH",
						"compliance": "CIS 5.4",
					})
				}
			}

		} else {
			checks = append(checks, map[string]interface{}{
				"category":   "policy",
				"name":       "Kyverno Admission Controller",
				"status":     "FAIL",
				"detail":     "Kyverno is not installed",
				"severity":   "HIGH",
				"compliance": "CIS 5.1",
				"fix":        "Run 'kates kyverno apply' to install Kyverno and recommended policies.",
			})
		}
		result["checks"] = checks

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

		checks, _ = result["checks"].([]interface{})
		if len(checks) > 0 {
			detailWidth := output.ColumnWidth(62, 30)

			parsed := make([]map[string]interface{}, 0, len(checks))
			for _, c := range checks {
				check, ok := c.(map[string]interface{})
				if ok {
					parsed = append(parsed, check)
				}
			}

			sort.Slice(parsed, func(i, j int) bool {
				return severityRank(fmt.Sprintf("%v", parsed[i]["severity"])) <
					severityRank(fmt.Sprintf("%v", parsed[j]["severity"]))
			})

			categoryOrder := []string{"auth", "authz", "transport", "topics", "config", "durability", "network", "dos", "limits", "policy"}
			categoryLabel := map[string]string{
				"auth":       "Authentication",
				"authz":      "Authorization",
				"transport":  "Transport Security",
				"topics":     "Topic Health",
				"config":     "Broker Configuration",
				"durability": "Data Durability",
				"network":    "Network & Threading",
				"dos":        "DoS Protection",
				"limits":     "Resource Limits",
				"policy":     "Policy Engine (Kyverno)",
			}

			grouped := make(map[string][]map[string]interface{})
			for _, check := range parsed {
				cat := fmt.Sprintf("%v", check["category"])
				grouped[cat] = append(grouped[cat], check)
			}

			for _, cat := range categoryOrder {
				group := grouped[cat]
				if len(group) == 0 {
					continue
				}
				label := categoryLabel[cat]
				if label == "" {
					label = cases.Title(language.English).String(cat)
				}
				output.SubHeader(label)

				rows := make([][]string, 0, len(group))
				for _, check := range group {
					name := fmt.Sprintf("%v", check["name"])
					status := fmt.Sprintf("%v", check["status"])
					detail := fmt.Sprintf("%v", check["detail"])
					severity := fmt.Sprintf("%v", check["severity"])
					cis := fmt.Sprintf("%v", check["compliance"])
					displayDetail := detail
					if !auditVerbose {
						displayDetail = truncate(detail, detailWidth)
					}
					rows = append(rows, []string{statusIcon(status), cis, name, severity, displayDetail})
				}
				output.Table([]string{"", "CIS", "Check", "Severity", "Detail"}, rows)
			}

			hasIssues := false
			for _, c := range checks {
				check, ok := c.(map[string]interface{})
				if !ok {
					continue
				}
				status := fmt.Sprintf("%v", check["status"])
				if status != "PASS" || auditVerbose {
					if !hasIssues {
						fmt.Println()
						if auditVerbose {
							output.SubHeader("Deep Dive & Remediations")
						} else {
							output.SubHeader("Remediation")
						}
						hasIssues = true
					}
					fix := fmt.Sprintf("%v", check["fix"])
					name := fmt.Sprintf("%v", check["name"])
					if fix == "" || fix == "<nil>" {
						fix = "No specific remediation required."
					}

					fmt.Printf("  %s  %s\n", statusIcon(status), output.KeyStyle.Render(name))
					if auditVerbose {
						detail := fmt.Sprintf("%v", check["detail"])
						fmt.Printf("     %s %s\n", output.DimStyle.Render("Detail:"), detail)
					}
					if status != "PASS" {
						fmt.Printf("     %s %s\n", output.AccentStyle.Render("Fix:"), fix)
					}
					fmt.Println()
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

		if auditExportFile != "" {
			if err := exportAuditReport(result, auditExportFile); err != nil {
				return cmdErr("Export failed: " + err.Error())
			}
			fmt.Printf("\n  📄 Report exported to %s\n", auditExportFile)
		}

		return nil
	},
}

// Shared flag variables used across security subcommand files.
var (
	authTestUser    string
	pentestName     string
	secGateMinGrade string
	baselineSave    bool
	auditExportFile string
	auditVerbose    bool
)

// statusIcon returns a styled status icon for table display.
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

// gradeStyle returns a styled grade string with appropriate colour.
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

// truncate shortens a string to max characters, appending "…" if needed.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

// severityRank maps severity strings to sort-friendly integers (lower = more severe).
func severityRank(sev string) int {
	switch sev {
	case "CRITICAL":
		return 0
	case "HIGH":
		return 1
	case "MEDIUM":
		return 2
	case "LOW":
		return 3
	default:
		return 4
	}
}

// runKubectlFn is the underlying kubectl executor (swappable for tests).
var runKubectlFn = runKubectlDefault

func runKubectlDefault(args ...string) (string, error) {
	cmd := exec.Command("kubectl", args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func runKubectl(args ...string) (string, error) {
	return runKubectlFn(args...)
}

func init() {
	securityAuditCmd.Flags().StringVar(&auditExportFile, "export", "", "Export report to file (.html, .md, .txt, .pdf, or .json)")
	securityAuditCmd.Flags().BoolVarP(&auditVerbose, "verbose", "v", false, "Enable deep and verbose auditing output")
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
	securityCmd.AddCommand(securityCertsCmd)
	securityCmd.AddCommand(securityCVECmd)
	securityCmd.AddCommand(securityConfigDiffCmd)
	securityCmd.AddCommand(securityACLMapCmd)
	securityCmd.AddCommand(securityTrendCmd)
	securityCmd.AddCommand(securitySecretsCmd)
	securityCmd.AddCommand(securityNetpolCmd)

	rootCmd.AddCommand(securityCmd)
}
