package cmd

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/bmscomp/kates/cli/output"
	"github.com/mattn/go-runewidth"
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

		injectKyvernoChecks(result)

		// The Kates API answers a failed audit with an "error" field and
		// whatever it graded before it failed: the audit did not run, so
		// exit 2 in either output mode, with the message on stderr.
		errMsg, failed := result["error"].(string)
		if outputMode == "json" {
			output.JSON(result)
			if failed {
				return couldNotTell(errMsg)
			}
			return nil
		}

		grade := fmt.Sprintf("%v", result["grade"])
		output.Banner("Security Audit", "Grade: "+gradeStyle(grade)+"  │  Kafka Cluster Posture Scan")

		if failed {
			fmt.Println()
			return couldNotTell(errMsg)
		}

		checks, _ := result["checks"].([]interface{})
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

// policyCheck is one Kyverno check in the shape of the backend's audit checks.
func policyCheck(name, status, detail, severity, cis, fix string) map[string]interface{} {
	check := map[string]interface{}{
		"category":   "policy",
		"name":       name,
		"status":     status,
		"detail":     detail,
		"severity":   severity,
		"compliance": cis,
	}
	if fix != "" {
		check["fix"] = fix
	}
	return check
}

// injectKyvernoChecks appends the Kyverno policy checks to the backend's
// audit result and recomputes its summary. The backend audits Kafka; whether
// an admission controller guards the cluster is read from Kubernetes here,
// through runKubectl, so a test can script kubectl like the rest of the
// security commands do. The probes used to run through exec.Command and the
// summary stayed the backend's, so the table listed more checks than the
// summary counted, and so did every export.
func injectKyvernoChecks(result map[string]interface{}) {
	checks, _ := result["checks"].([]interface{})
	crdOut, crdErr := runKubectl("get", "crd", "clusterpolicies.kyverno.io", "--no-headers")
	if crdErr != nil || crdOut == "" {
		checks = append(checks, policyCheck("Kyverno Admission Controller", "FAIL",
			"Kyverno is not installed", "HIGH", "CIS 5.1",
			"Run 'kates kyverno apply' to install Kyverno and recommended policies."))
		result["checks"] = checks
		recomputeAuditSummary(result)
		return
	}
	checks = append(checks, policyCheck("Kyverno Admission Controller", "PASS",
		"Kyverno is installed and protecting the cluster", "HIGH", "CIS 5.1", ""))

	polOut, err := runKubectl("get", "clusterpolicies", "-o", "jsonpath={range .items[*]}{.metadata.name}={.spec.validationFailureAction}{\"\\n\"}{end}")
	if err != nil {
		polOut = ""
	}
	activeCount, enforceCount := 0, 0
	for _, p := range strings.Split(polOut, "\n") {
		if p == "" {
			continue
		}
		activeCount++
		if strings.Contains(p, "Enforce") {
			enforceCount++
		}
	}
	if activeCount > 0 {
		checks = append(checks, policyCheck("Active ClusterPolicies", "PASS",
			fmt.Sprintf("%d active policies detected", activeCount), "HIGH", "CIS 5.2", ""))
	} else {
		checks = append(checks, policyCheck("Active ClusterPolicies", "FAIL",
			"No policies are active", "HIGH", "CIS 5.2",
			"Run 'kates kyverno apply' to deploy recommended policies."))
	}
	if enforceCount > 0 {
		checks = append(checks, policyCheck("Policy Enforcement", "PASS",
			fmt.Sprintf("%d policies are in Enforce mode", enforceCount), "MEDIUM", "CIS 5.3", ""))
	} else {
		checks = append(checks, policyCheck("Policy Enforcement", "WARN",
			"Policies are in Audit mode only", "MEDIUM", "CIS 5.3",
			"Test workloads and run 'kates kyverno enforce <policy>' when ready."))
	}

	// Workload violations cost one grade.
	polRepOut, err := runKubectl("get", "clusterpolicyreports,policyreports", "-A", "-o", "jsonpath={range .items[*].results[?(@.result=='fail')]}{.policy}/{.rule} on {.resources[*].kind} {.resources[*].name}: {.message}{\"\\n\"}{end}")
	if err == nil {
		if polRepOut != "" {
			failures := strings.Split(polRepOut, "\n")
			switch fmt.Sprintf("%v", result["grade"]) {
			case "A":
				result["grade"] = "B"
			case "B":
				result["grade"] = "C"
			case "C":
				result["grade"] = "D"
			case "D":
				result["grade"] = "F"
			}
			checks = append(checks, policyCheck("Workload Policy Violations", "FAIL",
				fmt.Sprintf("%d workload violations detected", len(failures)), "HIGH", "CIS 5.4",
				"Fix resource configurations. Example: "+failures[0]))
		} else {
			checks = append(checks, policyCheck("Workload Policy Violations", "PASS",
				"No workload violations detected", "HIGH", "CIS 5.4", ""))
		}
	}
	result["checks"] = checks
	recomputeAuditSummary(result)
}

// recomputeAuditSummary counts the result's checks into its summary's total,
// passed, warnings and failures, keeping whatever else the backend put there.
func recomputeAuditSummary(result map[string]interface{}) {
	checks, _ := result["checks"].([]interface{})
	summary, _ := result["summary"].(map[string]interface{})
	if summary == nil {
		summary = map[string]interface{}{}
	}
	total, passed, warnings, failures := 0, 0, 0, 0
	for _, c := range checks {
		check, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		total++
		switch strings.ToUpper(fmt.Sprintf("%v", check["status"])) {
		case "PASS":
			passed++
		case "WARN":
			warnings++
		case "FAIL":
			failures++
		}
	}
	summary["total"], summary["passed"], summary["warnings"], summary["failures"] = total, passed, warnings, failures
	result["summary"] = summary
}

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

// truncate shortens a string to max columns, ending it with "…" when it had
// to cut. It counts display width, not bytes: slicing bytes cut a multi-byte
// character in two and printed a broken rune.
func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	return runewidth.Truncate(s, max, "…")
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
