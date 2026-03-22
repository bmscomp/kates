package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-pdf/fpdf"
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
			tw := output.TermWidth()
			detailWidth := tw - 62
			if detailWidth < 30 {
				detailWidth = 30
			}

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

			categoryOrder := []string{"auth", "authz", "transport", "topics", "config", "durability", "network", "dos", "limits"}
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
					label = strings.Title(cat)
				}
				output.SubHeader(label)

				rows := make([][]string, 0, len(group))
				for _, check := range group {
					name := fmt.Sprintf("%v", check["name"])
					status := fmt.Sprintf("%v", check["status"])
					detail := fmt.Sprintf("%v", check["detail"])
					severity := fmt.Sprintf("%v", check["severity"])
					cis := fmt.Sprintf("%v", check["compliance"])
					rows = append(rows, []string{statusIcon(status), cis, name, severity, truncate(detail, detailWidth)})
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

		if auditExportFile != "" {
			if err := exportAuditReport(result, auditExportFile); err != nil {
				return cmdErr("Export failed: " + err.Error())
			}
			fmt.Printf("\n  📄 Report exported to %s\n", auditExportFile)
		}

		return nil
	},
}

var (
	authTestUser    string
	pentestName     string
	secGateMinGrade string
	baselineSave    bool
	auditExportFile string
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
			tw := output.TermWidth()
			detailWidth := tw - 56
			if detailWidth < 30 {
				detailWidth = 30
			}

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
					truncate(fmt.Sprintf("%v", test["detail"]), detailWidth),
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
				tw := output.TermWidth()
				fixWidth := tw - 42
				if fixWidth < 30 {
					fixWidth = 30
				}

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
						truncate(fmt.Sprintf("%v", c["fix"]), fixWidth),
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

var securityCertsCmd = &cobra.Command{
	Use:     "certs",
	Aliases: []string{"cert", "certificates"},
	Short:   "Inspect SSL/TLS certificate configuration across brokers",
	Example: "  kates security certs",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := apiClient.SecurityCerts(context.Background())
		if err != nil {
			return cmdErr("Certificate check failed: " + err.Error())
		}
		if outputMode == "json" {
			output.JSON(result)
			return nil
		}

		output.Banner("Certificate Check", "SSL/TLS Configuration Inspection")

		certs, _ := result["certificates"].([]interface{})
		for _, c := range certs {
			cert, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			fmt.Println()
			output.KeyValue("Broker", fmt.Sprintf("%v", cert["broker"]))
			output.KeyValue("SSL Protocol", fmt.Sprintf("%v", cert["sslProtocol"]))
			output.KeyValue("Client Auth", fmt.Sprintf("%v", cert["clientAuth"]))
			output.KeyValue("Hostname Verify", fmt.Sprintf("%v", cert["endpointIdentification"]))
			output.KeyValue("Cipher Suites", fmt.Sprintf("%v", cert["cipherSuites"]))
			output.KeyValue("Enabled Protocols", fmt.Sprintf("%v", cert["enabledProtocols"]))

			checks, _ := cert["checks"].([]interface{})
			if len(checks) > 0 {
				tw := output.TermWidth()
				detailWidth := tw - 50
				if detailWidth < 30 {
					detailWidth = 30
				}
				rows := make([][]string, 0, len(checks))
				for _, ch := range checks {
					chk, ok := ch.(map[string]interface{})
					if !ok {
						continue
					}
					rows = append(rows, []string{
						statusIcon(fmt.Sprintf("%v", chk["status"])),
						fmt.Sprintf("%v", chk["name"]),
						fmt.Sprintf("%v", chk["severity"]),
						truncate(fmt.Sprintf("%v", chk["detail"]), detailWidth),
					})
				}
				output.Table([]string{"", "Check", "Severity", "Detail"}, rows)
			}
		}
		return nil
	},
}

var securityCVECmd = &cobra.Command{
	Use:     "cve",
	Aliases: []string{"vuln", "vulnerabilities"},
	Short:   "Check running Kafka version against known CVEs",
	Example: "  kates security cve",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := apiClient.SecurityCVE(context.Background())
		if err != nil {
			return cmdErr("CVE check failed: " + err.Error())
		}
		if outputMode == "json" {
			output.JSON(result)
			return nil
		}

		version := fmt.Sprintf("%v", result["kafkaVersion"])
		grade := fmt.Sprintf("%v", result["grade"])
		gradeStyled := output.SuccessStyle.Render(grade)
		if grade == "FAIL" {
			gradeStyled = output.ErrorStyle.Render(grade)
		}
		output.Banner("CVE Vulnerability Check", "Kafka "+version+"  │  "+gradeStyled)

		tw := output.TermWidth()
		descWidth := tw - 52
		if descWidth < 30 {
			descWidth = 30
		}

		vulns, _ := result["vulnerabilities"].([]interface{})
		if len(vulns) > 0 {
			output.SubHeader("Vulnerabilities")
			rows := make([][]string, 0, len(vulns))
			for _, v := range vulns {
				cve, ok := v.(map[string]interface{})
				if !ok {
					continue
				}
				rows = append(rows, []string{
					"✗",
					fmt.Sprintf("%v", cve["id"]),
					fmt.Sprintf("%v", cve["severity"]),
					truncate(fmt.Sprintf("%v", cve["description"]), descWidth),
				})
			}
			output.Table([]string{"", "CVE", "Severity", "Description"}, rows)
		}

		patched, _ := result["patched"].([]interface{})
		if len(patched) > 0 {
			output.SubHeader("Patched")
			rows := make([][]string, 0, len(patched))
			for _, v := range patched {
				cve, ok := v.(map[string]interface{})
				if !ok {
					continue
				}
				rows = append(rows, []string{
					"✓",
					fmt.Sprintf("%v", cve["id"]),
					fmt.Sprintf("%v", cve["severity"]),
					truncate(fmt.Sprintf("%v", cve["title"]), descWidth),
				})
			}
			output.Table([]string{"", "CVE", "Severity", "Title"}, rows)
		}

		summary, _ := result["summary"].(map[string]interface{})
		if summary != nil {
			fmt.Println()
			output.KeyValue("Total CVEs Checked", fmt.Sprintf("%v", summary["total"]))
			output.KeyValue("Vulnerable", output.ErrorStyle.Render(fmt.Sprintf("%v", summary["vulnerable"])))
			output.KeyValue("Patched", output.SuccessStyle.Render(fmt.Sprintf("%v", summary["patched"])))
		}
		return nil
	},
}

var securityConfigDiffCmd = &cobra.Command{
	Use:     "config-diff",
	Aliases: []string{"diff", "consistency"},
	Short:   "Compare security configuration across all brokers for consistency",
	Example: "  kates security config-diff",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := apiClient.SecurityConfigDiff(context.Background())
		if err != nil {
			return cmdErr("Config consistency check failed: " + err.Error())
		}
		if outputMode == "json" {
			output.JSON(result)
			return nil
		}

		grade := fmt.Sprintf("%v", result["grade"])
		gradeStyled := output.SuccessStyle.Render(grade)
		if grade == "WARN" {
			gradeStyled = output.WarningStyle.Render(grade)
		}
		brokerCount := fmt.Sprintf("%v", result["brokerCount"])
		output.Banner("Config Consistency", brokerCount+" Brokers  │  "+gradeStyled)

		tw := output.TermWidth()

		mismatches, _ := result["mismatches"].([]interface{})
		if len(mismatches) > 0 {
			output.SubHeader(fmt.Sprintf("Mismatches (%d)", len(mismatches)))
			for _, m := range mismatches {
				mm, ok := m.(map[string]interface{})
				if !ok {
					continue
				}
				key := fmt.Sprintf("%v", mm["key"])
				output.Warn(key)
				values, _ := mm["values"].(map[string]interface{})
				for broker, val := range values {
					fmt.Printf("     Broker %s: %s\n", broker, val)
				}
			}
		}

		consistent, _ := result["consistent"].([]interface{})
		if len(consistent) > 0 {
			valWidth := tw - 46
			if valWidth < 30 {
				valWidth = 30
			}
			output.SubHeader(fmt.Sprintf("Consistent (%d)", len(consistent)))
			rows := make([][]string, 0, len(consistent))
			for _, c := range consistent {
				cc, ok := c.(map[string]interface{})
				if !ok {
					continue
				}
				rows = append(rows, []string{
					"✓",
					fmt.Sprintf("%v", cc["key"]),
					truncate(fmt.Sprintf("%v", cc["value"]), valWidth),
				})
			}
			output.Table([]string{"", "Config Key", "Value"}, rows)
		}

		fmt.Println()
		output.KeyValue("Keys Checked", fmt.Sprintf("%v", result["keysChecked"]))
		output.KeyValue("Mismatches", output.ErrorStyle.Render(fmt.Sprintf("%v", result["mismatchCount"])))
		return nil
	},
}

func exportAuditReport(result map[string]interface{}, filePath string) error {
	ext := filepath.Ext(filePath)
	if ext == ".json" {
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(filePath, data, 0644)
	}

	if ext == ".pdf" {
		return exportAuditPDF(result, filePath)
	}

	grade := fmt.Sprintf("%v", result["grade"])
	summary, _ := result["summary"].(map[string]interface{})
	checks, _ := result["checks"].([]interface{})

	var sb strings.Builder
	if ext == ".md" {
		sb.WriteString("# Kafka Security Audit Report\n\n")
		sb.WriteString(fmt.Sprintf("**Grade: %s** | Generated: %v\n\n", grade, result["timestamp"]))
		if summary != nil {
			sb.WriteString(fmt.Sprintf("| Metric | Count |\n|--------|-------|\n"))
			sb.WriteString(fmt.Sprintf("| Total | %v |\n", summary["total"]))
			sb.WriteString(fmt.Sprintf("| Passed | %v |\n", summary["passed"]))
			sb.WriteString(fmt.Sprintf("| Warnings | %v |\n", summary["warnings"]))
			sb.WriteString(fmt.Sprintf("| Failures | %v |\n\n", summary["failures"]))
		}
		sb.WriteString("| Status | CIS | Check | Severity | Detail |\n")
		sb.WriteString("|--------|-----|-------|----------|--------|\n")
		for _, c := range checks {
			chk, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			status := fmt.Sprintf("%v", chk["status"])
			icon := "✓"
			if status == "FAIL" {
				icon = "✗"
			} else if status == "WARN" {
				icon = "⚠"
			}
			sb.WriteString(fmt.Sprintf("| %s | %v | %v | %v | %v |\n",
				icon, chk["compliance"], chk["name"], chk["severity"], chk["detail"]))
		}
		return os.WriteFile(filePath, []byte(sb.String()), 0644)
	}

	if ext == ".txt" {
		sb.WriteString(fmt.Sprintf("KAFKA SECURITY AUDIT REPORT\n"))
		sb.WriteString(fmt.Sprintf("Grade: %s\n", grade))
		sb.WriteString(fmt.Sprintf("Generated: %v\n\n", result["timestamp"]))
		if summary != nil {
			sb.WriteString(fmt.Sprintf("Total: %v  |  Passed: %v  |  Warnings: %v  |  Failures: %v\n\n",
				summary["total"], summary["passed"], summary["warnings"], summary["failures"]))
		}
		for _, c := range checks {
			chk, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			status := fmt.Sprintf("%v", chk["status"])
			icon := "[PASS]"
			if status == "FAIL" {
				icon = "[FAIL]"
			} else if status == "WARN" {
				icon = "[WARN]"
			}
			sb.WriteString(fmt.Sprintf("  %s  %-8v  %-28v  %-8v  %v\n",
				icon, chk["compliance"], chk["name"], chk["severity"], chk["detail"]))
		}
		sb.WriteString("\nREMEDIATION\n")
		for _, c := range checks {
			chk, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			if fmt.Sprintf("%v", chk["status"]) != "PASS" {
				sb.WriteString(fmt.Sprintf("  - %v: %v\n", chk["name"], chk["fix"]))
			}
		}
		return os.WriteFile(filePath, []byte(sb.String()), 0644)
	}
	sb.WriteString(`<!DOCTYPE html><html><head><meta charset="utf-8">
<title>Kafka Security Audit</title>
<style>
body{font-family:'Segoe UI',system-ui,sans-serif;background:#0f172a;color:#e2e8f0;margin:0;padding:2rem}
.container{max-width:1100px;margin:0 auto}
h1{color:#c4b5fd;border-bottom:2px solid #7c3aed;padding-bottom:.5rem}
.grade{font-size:3rem;font-weight:bold;text-align:center;padding:1rem;border-radius:12px;margin:1rem 0}
.grade-a,.grade-b{background:#065f46;color:#10b981}
.grade-c{background:#78350f;color:#f59e0b}
.grade-d,.grade-f{background:#7f1d1d;color:#ef4444}
.summary{display:flex;gap:1rem;margin:1rem 0}
.card{background:#1e293b;border-radius:8px;padding:1rem 1.5rem;flex:1;text-align:center}
.card h3{color:#94a3b8;margin:0 0 .5rem 0;font-size:.85rem;text-transform:uppercase}
.card .num{font-size:2rem;font-weight:bold}
.pass{color:#10b981}.warn{color:#f59e0b}.fail{color:#ef4444}
table{width:100%;border-collapse:collapse;margin:1rem 0}
th{background:#1e293b;color:#c4b5fd;padding:.6rem;text-align:left;font-size:.8rem;text-transform:uppercase}
td{padding:.5rem .6rem;border-bottom:1px solid #334155;font-size:.9rem}
tr:hover{background:#1e293b}
.status-pass{color:#10b981}.status-warn{color:#f59e0b}.status-fail{color:#ef4444}
footer{text-align:center;color:#64748b;margin-top:2rem;font-size:.8rem}
</style></head><body><div class="container">
`)

	sb.WriteString(fmt.Sprintf("<h1>Kafka Security Audit Report</h1>\n"))
	gradeClass := "grade-" + strings.ToLower(grade)
	sb.WriteString(fmt.Sprintf(`<div class="grade %s">Grade: %s</div>`, gradeClass, grade))

	if summary != nil {
		sb.WriteString(`<div class="summary">`)
		sb.WriteString(fmt.Sprintf(`<div class="card"><h3>Total</h3><div class="num">%v</div></div>`, summary["total"]))
		sb.WriteString(fmt.Sprintf(`<div class="card"><h3>Passed</h3><div class="num pass">%v</div></div>`, summary["passed"]))
		sb.WriteString(fmt.Sprintf(`<div class="card"><h3>Warnings</h3><div class="num warn">%v</div></div>`, summary["warnings"]))
		sb.WriteString(fmt.Sprintf(`<div class="card"><h3>Failures</h3><div class="num fail">%v</div></div>`, summary["failures"]))
		sb.WriteString(`</div>`)
	}

	sb.WriteString(`<table><thead><tr><th>Status</th><th>CIS</th><th>Check</th><th>Severity</th><th>Detail</th><th>Remediation</th></tr></thead><tbody>`)
	for _, c := range checks {
		chk, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		status := fmt.Sprintf("%v", chk["status"])
		statusClass := "status-pass"
		icon := "✓"
		if status == "FAIL" {
			statusClass = "status-fail"
			icon = "✗"
		} else if status == "WARN" {
			statusClass = "status-warn"
			icon = "⚠"
		}
		sb.WriteString(fmt.Sprintf(`<tr><td class="%s">%s</td><td>%v</td><td>%v</td><td>%v</td><td>%v</td><td>%v</td></tr>`,
			statusClass, icon, chk["compliance"], chk["name"], chk["severity"], chk["detail"], chk["fix"]))
	}
	sb.WriteString("</tbody></table>\n")
	sb.WriteString(fmt.Sprintf(`<footer>Generated by kates security audit — %v</footer>`, result["timestamp"]))
	sb.WriteString("</div></body></html>")

	return os.WriteFile(filePath, []byte(sb.String()), 0644)
}

func exportAuditPDF(result map[string]interface{}, filePath string) error {
	pdf := fpdf.New("L", "mm", "A4", "")
	pdf.SetAutoPageBreak(true, 15)
	pdf.AddPage()

	grade := fmt.Sprintf("%v", result["grade"])
	summary, _ := result["summary"].(map[string]interface{})
	checks, _ := result["checks"].([]interface{})

	pdf.SetFont("Helvetica", "B", 22)
	pdf.SetTextColor(100, 80, 200)
	pdf.CellFormat(0, 12, "Kafka Security Audit Report", "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 9)
	pdf.SetTextColor(120, 120, 120)
	pdf.CellFormat(0, 6, fmt.Sprintf("Generated: %v", result["timestamp"]), "", 1, "L", false, 0, "")
	pdf.Ln(4)

	pdf.SetFont("Helvetica", "B", 36)
	switch grade {
	case "A", "B":
		pdf.SetTextColor(16, 185, 129)
	case "C":
		pdf.SetTextColor(245, 158, 11)
	default:
		pdf.SetTextColor(239, 68, 68)
	}
	pdf.CellFormat(40, 20, "Grade: "+grade, "", 1, "L", false, 0, "")
	pdf.Ln(2)

	if summary != nil {
		pdf.SetFont("Helvetica", "", 11)
		pdf.SetTextColor(60, 60, 60)
		pdf.CellFormat(0, 7, fmt.Sprintf("Total: %v   |   Passed: %v   |   Warnings: %v   |   Failures: %v",
			summary["total"], summary["passed"], summary["warnings"], summary["failures"]), "", 1, "L", false, 0, "")
		pdf.Ln(4)
	}

	colWidths := []float64{10, 20, 55, 20, 170}
	headers := []string{"", "CIS", "Check", "Severity", "Detail"}
	pdf.SetFont("Helvetica", "B", 8)
	pdf.SetFillColor(30, 41, 59)
	pdf.SetTextColor(196, 181, 253)
	for i, h := range headers {
		pdf.CellFormat(colWidths[i], 7, h, "1", 0, "L", true, 0, "")
	}
	pdf.Ln(-1)

	pdf.SetFont("Helvetica", "", 8)
	for _, c := range checks {
		chk, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		status := fmt.Sprintf("%v", chk["status"])
		icon := "OK"
		if status == "FAIL" {
			icon = "FAIL"
			pdf.SetTextColor(239, 68, 68)
		} else if status == "WARN" {
			icon = "WARN"
			pdf.SetTextColor(245, 158, 11)
		} else {
			pdf.SetTextColor(16, 185, 129)
		}
		pdf.CellFormat(colWidths[0], 6, icon, "1", 0, "C", false, 0, "")
		pdf.SetTextColor(60, 60, 60)
		pdf.CellFormat(colWidths[1], 6, fmt.Sprintf("%v", chk["compliance"]), "1", 0, "L", false, 0, "")
		pdf.CellFormat(colWidths[2], 6, fmt.Sprintf("%v", chk["name"]), "1", 0, "L", false, 0, "")
		pdf.CellFormat(colWidths[3], 6, fmt.Sprintf("%v", chk["severity"]), "1", 0, "L", false, 0, "")
		detail := fmt.Sprintf("%v", chk["detail"])
		if len(detail) > 95 {
			detail = detail[:94] + "..."
		}
		pdf.CellFormat(colWidths[4], 6, detail, "1", 0, "L", false, 0, "")
		pdf.Ln(-1)
	}

	pdf.Ln(6)
	pdf.SetFont("Helvetica", "B", 12)
	pdf.SetTextColor(100, 80, 200)
	pdf.CellFormat(0, 8, "Remediation", "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 9)
	pdf.SetTextColor(60, 60, 60)
	for _, c := range checks {
		chk, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		if fmt.Sprintf("%v", chk["status"]) != "PASS" {
			pdf.CellFormat(0, 5, fmt.Sprintf("- %v: %v", chk["name"], chk["fix"]), "", 1, "L", false, 0, "")
		}
	}

	return pdf.OutputFileAndClose(filePath)
}

var securityACLMapCmd = &cobra.Command{
	Use:     "acl-map",
	Aliases: []string{"coverage", "acl-coverage"},
	Short:   "Show ACL coverage matrix — which users can access which topics",
	Example: "  kates security acl-map",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := apiClient.SecurityACLMap(context.Background())
		if err != nil {
			return cmdErr("ACL coverage check failed: " + err.Error())
		}
		if outputMode == "json" {
			output.JSON(result)
			return nil
		}

		grade := fmt.Sprintf("%v", result["grade"])
		gradeStyled := output.SuccessStyle.Render(grade)
		if grade == "WARN" {
			gradeStyled = output.WarningStyle.Render(grade)
		}
		output.Banner("ACL Coverage Map", fmt.Sprintf("%v Topics  │  %v ACLs  │  %s", result["totalTopics"], result["totalAcls"], gradeStyled))

		tw := output.TermWidth()
		opsWidth := tw - 44
		if opsWidth < 30 {
			opsWidth = 30
		}

		topics, _ := result["topics"].([]interface{})
		for _, t := range topics {
			topic, ok := t.(map[string]interface{})
			if !ok {
				continue
			}
			covered, _ := topic["covered"].(bool)
			icon := "✓"
			if !covered {
				icon = "✗"
			}
			topicName := fmt.Sprintf("%v", topic["topic"])
			users, _ := topic["users"].(map[string]interface{})
			if len(users) == 0 {
				fmt.Printf("  %s  %-35s  %s\n", output.ErrorStyle.Render(icon),
					topicName, output.ErrorStyle.Render("NO ACL RULES"))
			} else {
				fmt.Printf("  %s  %-35s\n", output.SuccessStyle.Render(icon), topicName)
				for user, ops := range users {
					opsStr := fmt.Sprintf("%v", ops)
					fmt.Printf("       %-25s  %s\n", user, truncate(opsStr, opsWidth))
				}
			}
		}

		fmt.Println()
		uncovered := fmt.Sprintf("%v", result["uncoveredTopics"])
		if uncovered != "0" {
			output.KeyValue("Uncovered Topics", output.ErrorStyle.Render(uncovered))
		} else {
			output.KeyValue("Uncovered Topics", output.SuccessStyle.Render("0"))
		}
		return nil
	},
}

var securityTrendCmd = &cobra.Command{
	Use:     "trend",
	Aliases: []string{"history", "scores"},
	Short:   "Show security audit score trend over time",
	Example: "  kates security trend",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := apiClient.SecurityTrend(context.Background())
		if err != nil {
			return cmdErr("Score trend failed: " + err.Error())
		}
		if outputMode == "json" {
			output.JSON(result)
			return nil
		}

		trend := fmt.Sprintf("%v", result["trend"])
		trendStyled := trend
		switch trend {
		case "IMPROVING":
			trendStyled = output.SuccessStyle.Render("↑ IMPROVING")
		case "DEGRADING":
			trendStyled = output.ErrorStyle.Render("↓ DEGRADING")
		case "STABLE":
			trendStyled = output.WarningStyle.Render("→ STABLE")
		case "BASELINE":
			trendStyled = output.DimStyle.Render("● BASELINE")
		case "NO_DATA":
			trendStyled = output.DimStyle.Render("○ NO DATA")
		}

		output.Banner("Security Score Trend", trendStyled)

		history, _ := result["history"].([]interface{})
		if len(history) == 0 {
			fmt.Println("  No audit history yet. Run 'kates security audit' to collect data.")
			return nil
		}

		rows := make([][]string, 0, len(history))
		gradeMap := map[string]string{"A": "█████", "B": "████░", "C": "███░░", "D": "██░░░", "F": "█░░░░"}
		for i, h := range history {
			snap, ok := h.(map[string]interface{})
			if !ok {
				continue
			}
			g := fmt.Sprintf("%v", snap["grade"])
			bar := gradeMap[g]
			if bar == "" {
				bar = "░░░░░"
			}
			rows = append(rows, []string{
				fmt.Sprintf("#%d", i+1),
				gradeStyle(g),
				bar,
				fmt.Sprintf("%v", snap["timestamp"]),
			})
		}
		output.Table([]string{"#", "Grade", "Score", "Timestamp"}, rows)

		fmt.Println()
		output.KeyValue("Total Snapshots", fmt.Sprintf("%v", result["totalSnapshots"]))
		if cg, ok := result["currentGrade"]; ok {
			output.KeyValue("Current Grade", gradeStyle(fmt.Sprintf("%v", cg)))
		}
		return nil
	},
}

var securitySecretsCmd = &cobra.Command{
	Use:     "secrets",
	Aliases: []string{"secret", "scan"},
	Short:   "Scan topic names and configurations for sensitive patterns",
	Example: "  kates security secrets",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := apiClient.SecuritySecrets(context.Background())
		if err != nil {
			return cmdErr("Secret scan failed: " + err.Error())
		}
		if outputMode == "json" {
			output.JSON(result)
			return nil
		}

		grade := fmt.Sprintf("%v", result["grade"])
		gradeStyled := output.SuccessStyle.Render(grade)
		if grade == "WARN" {
			gradeStyled = output.WarningStyle.Render(grade)
		}
		output.Banner("Secret Scanner", fmt.Sprintf("%v Topics Scanned  │  %s", result["topicsScanned"], gradeStyled))

		tw := output.TermWidth()
		detailWidth := tw - 55
		if detailWidth < 30 {
			detailWidth = 30
		}

		findings, _ := result["findings"].([]interface{})
		if len(findings) == 0 {
			fmt.Println()
			output.Success("No sensitive patterns detected")
		} else {
			rows := make([][]string, 0, len(findings))
			for _, f := range findings {
				finding, ok := f.(map[string]interface{})
				if !ok {
					continue
				}
				rows = append(rows, []string{
					"⚠",
					fmt.Sprintf("%v", finding["location"]),
					fmt.Sprintf("%v", finding["topic"]),
					fmt.Sprintf("%v", finding["pattern"]),
					fmt.Sprintf("%v", finding["severity"]),
					truncate(fmt.Sprintf("%v", finding["detail"]), detailWidth),
				})
			}
			output.Table([]string{"", "Location", "Topic", "Pattern", "Severity", "Detail"}, rows)
		}

		fmt.Println()
		output.KeyValue("Findings", fmt.Sprintf("%v", result["findingsCount"]))
		output.KeyValue("Patterns Checked", fmt.Sprintf("%v", result["patternsChecked"]))
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

		tw := output.TermWidth()
		nameWidth := tw - 50
		if nameWidth < 30 {
			nameWidth = 30
		}

		namespaces := []string{"kafka", "kates", "strimzi-system"}
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

func runKubectl(args ...string) (string, error) {
	cmd := exec.Command("kubectl", args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func init() {
	securityAuditCmd.Flags().StringVar(&auditExportFile, "export", "", "Export report to file (.html, .md, .txt, .pdf, or .json)")
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
