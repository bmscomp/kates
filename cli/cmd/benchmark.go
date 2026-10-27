package cmd

import (
	"errors"
	"fmt"
	"time"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
	"github.com/bmscomp/kates/cli/pkg/grade"
	"github.com/spf13/cobra"
)

var (
	benchRecords int
	benchTimeout string
)

var benchmarkCmd = &cobra.Command{
	Use:     "benchmark",
	Aliases: []string{"bench"},
	Short:   "Run a full test battery (LOAD → STRESS → SPIKE) with a letter-grade scorecard",
	Long: `Run a LOAD, a STRESS and a SPIKE test one after another and grade each
from its peak throughput and p99 latency, then give an overall grade.

Each test scores from 0 to 100, half for throughput and half for latency, and
the score gives its grade; the overall grade averages the scores:

` + grade.BenchmarkThresholds() + `

A run that reports no final status within --timeout, or whose status cannot
be read three times in a row, shows as ERROR: it may still be going, so the
battery stops there and the tests after it show as SKIPPED.

With -o json it prints only the scorecard, as JSON, when the battery ends.`,
	Example: `  kates benchmark
  kates benchmark --records 100000
  kates benchmark --timeout 10m
  kates benchmark -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Ctrl-C cancels this context (see interrupt.go), and the run the
		// battery is waiting for with it.
		ctx := commandContext(cmd)
		jsonOut := outputMode == "json"
		types := []string{"LOAD", "STRESS", "SPIKE"}

		timeout, err := parseTimeoutFlag(benchTimeout)
		if err != nil {
			return cmdErr(err.Error())
		}

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

			final, err := followRun(ctx, run.ID, followOpts{
				interval:     testPollInterval,
				deadline:     time.Now().Add(timeout),
				maxErrors:    benchmarkPollFailures,
				cancelOnStop: stopStartedRun,
			})
			if errors.Is(err, errInterrupted) {
				return err
			}
			if err != nil {
				// The outcome is unknown, not a failure, as test apply reports
				// a run it lost track of. It used to read FAILED, and the next
				// test started on top of a run that may still put load on the
				// cluster.
				if errors.Is(err, errFollowDeadline) {
					err = fmt.Errorf("no final status after %s", timeout)
				}
				msg := err.Error() + "; the run may still be going"
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
			res.OverallGrade = grade.FromScore(totalScore / float64(scored)).String()
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

// benchmarkPollFailures is how many GetTest errors in a row end a phase. One
// error used to be skipped without a word, so an API that had gone away was
// polled, in vain, for the whole six minutes.
const benchmarkPollFailures = followMaxErrors

// defaultBenchTimeout bounds the wait for each phase's run. The old cap was
// hidden: 120 polls of 3 s, which no flag could lengthen for a slow cluster.
const defaultBenchTimeout = "30m"

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
		score := grade.BenchmarkScore(peak, maxP99)
		bt.Score = &score
		bt.Grade = grade.FromScore(score).String()
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

func gradeColor(g string) string {
	switch g {
	case "A":
		return output.SuccessStyle.Bold(true).Render("★ " + g)
	case "B":
		return output.SuccessStyle.Render(g)
	case "C":
		return output.WarningStyle.Render(g)
	default:
		return output.ErrorStyle.Render(g)
	}
}

// testPollInterval is how often gate, benchmark and flow ask for a run's
// status. Tests shorten it.
var testPollInterval = 3 * time.Second

func init() {
	addRecordsFlag(benchmarkCmd, &benchRecords, 50000)
	addTimeoutFlag(benchmarkCmd, &benchTimeout, defaultBenchTimeout)
	interruptible(benchmarkCmd)
	rootCmd.AddCommand(benchmarkCmd)
}
