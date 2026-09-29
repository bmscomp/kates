package cmd

import (
	"context"
	"fmt"

	"github.com/bmscomp/kates/cli/output"
	"github.com/spf13/cobra"
)

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
				fixWidth := output.ColumnWidth(42, 30)

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

		detailWidth := output.ColumnWidth(55, 30)

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
