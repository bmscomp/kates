package cmd

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/klster/kates-cli/client"
	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var benchRecords int

var benchmarkCmd = &cobra.Command{
	Use:     "benchmark",
	Aliases: []string{"bench"},
	Short:   "Run a full test battery (LOAD → STRESS → SPIKE) with a letter-grade scorecard",
	Example: `  kates benchmark
  kates benchmark --records 100000`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		types := []string{"LOAD", "STRESS", "SPIKE"}

		output.Banner("Kates Benchmark", "Full performance battery")
		fmt.Println()

		type benchResult struct {
			testType   string
			run        *client.TestRun
			throughput float64
			avgLat     float64
			p99Lat     float64
			passed     bool
			err        string
		}

		var results []benchResult

		for i, tt := range types {
			output.SubHeader(fmt.Sprintf("Phase %d/%d: %s", i+1, len(types), tt))

			req := &client.CreateTestRequest{
				TestType: tt,
				Spec: &client.TestSpec{
					Records: benchRecords,
				},
			}

			run, err := apiClient.CreateTest(ctx, req)
			if err != nil {
				output.Error(fmt.Sprintf("Failed to create %s test: %s", tt, err.Error()))
				results = append(results, benchResult{testType: tt, err: err.Error()})
				continue
			}

			output.Hint(fmt.Sprintf("  Running %s → %s", tt, truncID(run.ID)))

			final := waitForCompletion(ctx, run.ID)
			if final == nil {
				results = append(results, benchResult{testType: tt, err: "timeout waiting for result"})
				continue
			}

			br := benchResult{
				testType: tt,
				run:      final,
				passed:   final.Status == "DONE",
			}

			for _, r := range final.Results {
				if r.ThroughputRecordsPerSec > br.throughput {
					br.throughput = r.ThroughputRecordsPerSec
				}
				if r.AvgLatencyMs > br.avgLat {
					br.avgLat = r.AvgLatencyMs
				}
				if r.P99LatencyMs > br.p99Lat {
					br.p99Lat = r.P99LatencyMs
				}
			}

			if final.Status == "FAILED" {
				for _, r := range final.Results {
					if r.Error != "" {
						br.err = r.Error
						break
					}
				}
			}

			results = append(results, br)
			output.Success(fmt.Sprintf("  %s completed: %.0f rec/s, p99=%.2fms", tt, br.throughput, br.p99Lat))
			fmt.Println()
		}

		output.SubHeader("Benchmark Scorecard")
		rows := make([][]string, len(results))
		totalScore := 0.0
		scored := 0

		for i, r := range results {
			status := "DONE"
			throughput := "—"
			latency := "—"
			grade := "—"

			if r.err != "" {
				status = "FAILED"
			}
			if r.throughput > 0 {
				throughput = fmtNum(r.throughput) + " rec/s"
			}
			if r.p99Lat > 0 {
				latency = fmtFloat(r.p99Lat, 2) + " ms"
			}

			if r.passed && r.throughput > 0 {
				score := gradeScore(r.throughput, r.p99Lat)
				totalScore += score
				scored++
				grade = letterGrade(score)
			}

			rows[i] = []string{r.testType, status, throughput, latency, grade}
		}

		output.Table([]string{"Test", "Status", "Throughput", "P99 Latency", "Grade"}, rows)

		if scored > 0 {
			avg := totalScore / float64(scored)
			overall := letterGrade(avg)
			label := output.AccentStyle.Render("Overall Grade")
			gradeStyled := gradeColor(overall)
			fmt.Printf("\n  %s    %s\n\n", label, gradeStyled)
		}

		return nil
	},
}

func gradeScore(throughput, p99 float64) float64 {
	tScore := math.Min(throughput/50000.0, 1.0) * 50
	lScore := 50.0
	if p99 > 0 {
		lScore = math.Max(0, 50.0-(p99*5.0))
	}
	return tScore + lScore
}

func letterGrade(score float64) string {
	switch {
	case score >= 90:
		return "A"
	case score >= 80:
		return "B"
	case score >= 70:
		return "C"
	case score >= 60:
		return "D"
	default:
		return "F"
	}
}

func gradeColor(grade string) string {
	switch grade {
	case "A":
		return output.SuccessStyle.Bold(true).Render("★ " + grade)
	case "B":
		return output.SuccessStyle.Render(grade)
	case "C":
		return output.WarningStyle.Render(grade)
	default:
		return output.ErrorStyle.Render(grade)
	}
}

func waitForCompletion(ctx context.Context, id string) *client.TestRun {
	for i := 0; i < 120; i++ {
		time.Sleep(3 * time.Second)
		run, err := apiClient.GetTest(ctx, id)
		if err != nil {
			continue
		}
		status := strings.ToUpper(run.Status)
		if status == "DONE" || status == "FAILED" {
			return run
		}
	}
	return nil
}

func init() {
	benchmarkCmd.Flags().IntVar(&benchRecords, "records", 50000, "Records per test phase")
	rootCmd.AddCommand(benchmarkCmd)
}
