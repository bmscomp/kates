package cmd

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
	"github.com/spf13/cobra"
)

var benchRecords int

var benchmarkCmd = &cobra.Command{
	Use:     "benchmark",
	Aliases: []string{"bench"},
	Short:   "Run a full test battery (LOAD → STRESS → SPIKE) with a letter-grade scorecard",
	Long: `Run a LOAD, a STRESS and a SPIKE test one after another and grade each
from its peak throughput and p99 latency, then give an overall grade.

A run that reports no final status in time shows as ERROR: it may still be
going, so the battery stops there and the tests after it show as SKIPPED.

With -o json it prints only the scorecard, as JSON, when the battery ends.`,
	Example: `  kates benchmark
  kates benchmark --records 100000
  kates benchmark -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		jsonOut := outputMode == "json"
		types := []string{"LOAD", "STRESS", "SPIKE"}

		if !jsonOut {
			output.Banner("Kates Benchmark", "Full performance battery")
			fmt.Println()
		}

		res := benchmarkResult{RecordsPerTest: benchRecords}
		lost := "" // the type of a run that never reported a final status

		for i, tt := range types {
			if lost != "" {
				res.Tests = append(res.Tests, benchmarkTest{TestType: tt, Status: "SKIPPED",
					Error: "not started: the " + lost + " run may still be going"})
				continue
			}
			if !jsonOut {
				output.SubHeader(fmt.Sprintf("Phase %d/%d: %s", i+1, len(types), tt))
			}

			req := &client.CreateTestRequest{
				TestType: tt,
				Spec: &client.TestSpec{
					Records: benchRecords,
				},
			}

			run, err := apiClient.CreateTest(ctx, req)
			if err != nil {
				if !jsonOut {
					output.Error(fmt.Sprintf("Failed to create %s test: %s", tt, err.Error()))
				}
				res.Tests = append(res.Tests, benchmarkTest{TestType: tt, Status: "FAILED", Error: err.Error()})
				continue
			}

			if !jsonOut {
				output.Hint(fmt.Sprintf("  Running %s → %s", tt, truncID(run.ID)))
			}

			final := waitForCompletion(ctx, run.ID)
			if final == nil {
				// The outcome is unknown, not a failure, as test apply reports
				// a run it lost track of. It used to read FAILED, and the next
				// test started on top of a run that may still put load on the
				// cluster.
				msg := fmt.Sprintf("no final status after %s; the run may still be going", benchmarkPolls*testPollInterval)
				if !jsonOut {
					output.Error(fmt.Sprintf("  %s: %s", tt, msg))
				}
				res.Tests = append(res.Tests, benchmarkTest{TestType: tt, RunID: run.ID, Status: "ERROR", Error: msg})
				lost = tt
				continue
			}

			bt := scoreBenchmarkRun(tt, final)
			res.Tests = append(res.Tests, bt)
			if !jsonOut {
				throughput, p99 := "—", "—"
				if bt.PeakThroughputRecPerSec != nil {
					throughput = fmt.Sprintf("%.0f rec/s", *bt.PeakThroughputRecPerSec)
				}
				if bt.MaxP99LatencyMs != nil {
					p99 = fmt.Sprintf("%.2fms", *bt.MaxP99LatencyMs)
				}
				output.Success(fmt.Sprintf("  %s completed: %s, p99=%s", tt, throughput, p99))
				fmt.Println()
			}
		}

		totalScore := 0.0
		scored := 0
		for _, t := range res.Tests {
			if t.Score != nil {
				totalScore += *t.Score
				scored++
			}
		}
		if scored > 0 {
			res.OverallGrade = letterGrade(totalScore / float64(scored))
		}

		output.Render(jsonOut, res, func() { renderBenchmark(res) })
		return nil
	},
}

// benchmarkResult is the scorecard kates benchmark prints: the table shows
// it, -o json prints it as is.
type benchmarkResult struct {
	RecordsPerTest int             `json:"recordsPerTest"`
	Tests          []benchmarkTest `json:"tests"`
	// OverallGrade averages the scores of the tests that finished DONE with
	// throughput; it is empty when none did.
	OverallGrade string `json:"overallGrade,omitempty"`
}

// benchmarkTest is one row of the scorecard. Status is the run's final
// status, FAILED when the run could not be created, ERROR when it reported no
// final status in time, and SKIPPED for a test not started after that. The
// throughput is the highest any phase reached and the p99 the highest any
// phase had; each is null when no phase reported it.
type benchmarkTest struct {
	TestType                string   `json:"testType"`
	RunID                   string   `json:"runId,omitempty"`
	Status                  string   `json:"status"`
	PeakThroughputRecPerSec *float64 `json:"peakThroughputRecPerSec"`
	MaxP99LatencyMs         *float64 `json:"maxP99LatencyMs"`
	Score                   *float64 `json:"score,omitempty"`
	Grade                   string   `json:"grade,omitempty"`
	Error                   string   `json:"error,omitempty"`
}

// benchmarkPolls is how many times benchmark asks for a run's status before
// it gives up on the run.
const benchmarkPolls = 120

// scoreBenchmarkRun turns a finished run into its scorecard row. Only a run
// that finished DONE with throughput is graded.
func scoreBenchmarkRun(testType string, run *client.TestRun) benchmarkTest {
	bt := benchmarkTest{TestType: testType, RunID: run.ID, Status: run.Status}
	var peak, maxP99 float64
	for _, r := range run.Results {
		peak = max(peak, r.ThroughputRecordsPerSec)
		maxP99 = max(maxP99, r.P99LatencyMs)
	}
	bt.PeakThroughputRecPerSec, bt.MaxP99LatencyMs = reported(peak), reported(maxP99)

	// A FAILED run used to show as DONE unless one of its phases carried an
	// error message; the status now comes from the run itself.
	if run.Status == "FAILED" {
		for _, r := range run.Results {
			if r.Error != "" {
				bt.Error = r.Error
				break
			}
		}
	}

	if run.Status == "DONE" && peak > 0 {
		score := gradeScore(peak, maxP99)
		bt.Score = &score
		bt.Grade = letterGrade(score)
	}
	return bt
}

// reported returns a metric a run's phases reported, or nil for 0, which the
// backend sends for a metric a phase did not measure. The table shows nil as a
// dash and JSON as null, so a script never reads a missing figure as zero.
func reported(v float64) *float64 {
	if v <= 0 {
		return nil
	}
	return &v
}

func renderBenchmark(res benchmarkResult) {
	output.SubHeader("Benchmark Scorecard")
	rows := make([][]string, len(res.Tests))
	for i, t := range res.Tests {
		throughput := "—"
		latency := "—"
		grade := "—"
		if t.PeakThroughputRecPerSec != nil {
			throughput = fmtNum(*t.PeakThroughputRecPerSec) + " rec/s"
		}
		if t.MaxP99LatencyMs != nil {
			latency = fmtFloat(*t.MaxP99LatencyMs, 2) + " ms"
		}
		if t.Grade != "" {
			grade = t.Grade
		}
		rows[i] = []string{t.TestType, t.Status, throughput, latency, grade}
	}

	output.Table([]string{"Test", "Status", "Throughput", "P99 Latency", "Grade"}, rows)

	if res.OverallGrade != "" {
		label := output.AccentStyle.Render("Overall Grade")
		gradeStyled := gradeColor(res.OverallGrade)
		fmt.Printf("\n  %s    %s\n\n", label, gradeStyled)
	}
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

// testPollInterval is how often gate and benchmark ask for a run's status.
// Tests shorten it.
var testPollInterval = 3 * time.Second

func waitForCompletion(ctx context.Context, id string) *client.TestRun {
	for i := 0; i < benchmarkPolls; i++ {
		time.Sleep(testPollInterval)
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
