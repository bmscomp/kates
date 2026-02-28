package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var (
	badgeType   string
	badgeMetric string
)

var badgeCmd = &cobra.Command{
	Use:   "badge",
	Short: "Generate status badges for README files",
	Long: `Generate shields.io-compatible badge URLs for embedding in
README files, GitHub PRs, or dashboards.`,
	Example: `  kates badge --type LOAD --metric grade
  kates badge --type STRESS --metric p99
  kates badge --metric throughput`,
	RunE: func(cmd *cobra.Command, args []string) error {
		paged, err := apiClient.ListTests(context.Background(), badgeType, "DONE", 0, 1)
		if err != nil {
			return err
		}
		if paged == nil || len(paged.Content) == 0 {
			return cmdErr("no completed tests found")
		}

		run := paged.Content[0]
		var value, color string

		switch strings.ToLower(badgeMetric) {
		case "grade":
			report, err := apiClient.Report(context.Background(), run.ID)
			if err != nil {
				return err
			}
			if report.OverallSlaVerdict != nil {
				value = report.OverallSlaVerdict.Grade
			} else {
				value = "N/A"
			}
			switch {
			case strings.HasPrefix(value, "A"):
				color = "brightgreen"
			case strings.HasPrefix(value, "B"):
				color = "green"
			case strings.HasPrefix(value, "C"):
				color = "yellow"
			default:
				color = "red"
			}

		case "p99":
			if len(run.Results) > 0 {
				var avg float64
				for _, r := range run.Results {
					avg += r.P99LatencyMs
				}
				avg /= float64(len(run.Results))
				value = fmt.Sprintf("%.0fms", avg)
			} else {
				value = "N/A"
			}
			color = "blue"

		case "throughput":
			if len(run.Results) > 0 {
				var avg float64
				for _, r := range run.Results {
					avg += r.ThroughputRecordsPerSec
				}
				avg /= float64(len(run.Results))
				value = fmtAdvisorNum(avg) + " rec/s"
			} else {
				value = "N/A"
			}
			color = "blue"

		default:
			return cmdErr("unknown metric: " + badgeMetric + " (use: grade, p99, throughput)")
		}

		label := "kates"
		if badgeType != "" {
			label = "kates " + strings.ToLower(badgeType)
		}

		encodedLabel := strings.ReplaceAll(label, " ", "%20")
		encodedValue := strings.ReplaceAll(value, " ", "%20")
		url := fmt.Sprintf("https://img.shields.io/badge/%s-%s-%s", encodedLabel, encodedValue, color)

		fmt.Printf("URL:      %s\n", url)
		fmt.Printf("Markdown: ![%s %s](%s)\n", label, badgeMetric, url)
		fmt.Printf("HTML:     <img src=\"%s\" alt=\"%s %s\">\n", url, label, badgeMetric)

		return nil
	},
}

func init() {
	badgeCmd.Flags().StringVar(&badgeType, "type", "", "Test type filter (LOAD, STRESS, etc.)")
	badgeCmd.Flags().StringVar(&badgeMetric, "metric", "grade", "Badge metric: grade, p99, or throughput")
	rootCmd.AddCommand(badgeCmd)
}
