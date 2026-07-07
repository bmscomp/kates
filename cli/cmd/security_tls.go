package cmd

import (
	"context"
	"fmt"

	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
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
				detailWidth := output.ColumnWidth(50, 30)
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

		descWidth := output.ColumnWidth(52, 30)

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
			valWidth := output.ColumnWidth(46, 30)
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
