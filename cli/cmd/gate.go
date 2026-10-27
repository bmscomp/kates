package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
	"github.com/spf13/cobra"
)

var (
	gateMinGrade string
	gateType     string
	gateRecords  int
	gateBackend  string
	gateTimeout  int
)

var gateCmd = &cobra.Command{
	Use:     "gate",
	Aliases: []string{"ci", "quality-gate"},
	Short:   "CI quality gate — run a test and exit non-zero if grade is below threshold",
	Long: `Run a performance test and evaluate the result against a minimum grade.
Exits with code 0 if the grade meets or exceeds the threshold, or code 1 if it fails.
Designed for CI/CD pipelines where you want to block deploys on performance regression.

With -o json it prints only the result, as JSON, once the test exists: the run
ID, its status, the throughput and p99 it was graded on, the grade and whether
it passed. The exit code is the same.`,
	Example: `  kates gate --min-grade B
  kates gate --min-grade C --type STRESS --records 100000
  kates gate --min-grade A --timeout 300
  kates gate --min-grade B -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Ctrl-C cancels this context (see interrupt.go), and the run it
		// started with it: a gate killed in CI used to leave the test running.
		ctx := commandContext(cmd)
		jsonOut := outputMode == "json"

		minOrdinal := gradeOrdinal(gateMinGrade)
		if minOrdinal < 0 {
			return cmdErr("Invalid grade: " + gateMinGrade + " (use A, B, C, D, or F)")
		}

		if !jsonOut {
			output.Banner("Quality Gate", fmt.Sprintf("min-grade=%s  type=%s  records=%d", gateMinGrade, gateType, gateRecords))
			fmt.Println()
		}

		spec := &client.TestSpec{Records: gateRecords}
		req := &client.CreateTestRequest{
			TestType: gateType,
			Backend:  gateBackend,
			Spec:     spec,
		}
		run, err := apiClient.CreateTest(ctx, req)
		if err != nil {
			if ctx.Err() != nil {
				return errInterrupted
			}
			return cmdErr("Failed to create test: " + err.Error())
		}
		id := run.ID

		if !jsonOut {
			output.Success(fmt.Sprintf("Test started: %s", truncID(id)))
		}

		res := gateResult{RunID: id, TestType: gateType, Records: gateRecords, Status: run.Status, MinGrade: gateMinGrade}
		// fail ends the gate without a grade. Under -o json the result still
		// goes to stdout, so a script that sees exit 1 learns which run it was
		// and how far it got; the message goes to stderr either way.
		fail := func(msg string) error {
			if jsonOut {
				res.Error = msg
				output.JSON(res)
			}
			return cmdErr(msg)
		}

		deadline := time.Now().Add(time.Duration(gateTimeout) * time.Second)
		for {
			if !sleepCtx(ctx, testPollInterval) {
				return stopStartedRun(id)
			}
			run, err = apiClient.GetTest(ctx, id)
			if err != nil {
				if ctx.Err() != nil {
					return stopStartedRun(id)
				}
				return fail("Failed to poll test: " + err.Error())
			}
			res.Status = run.Status
			status := strings.ToUpper(run.Status)
			if status == "DONE" || status == "FAILED" {
				break
			}
			if time.Now().After(deadline) {
				// A run the gate gave up on is cancelled, not left to load
				// the cluster while the next pipeline step starts.
				cancelled, note := cancelStartedRun(id)
				res.Status = cancelled
				return fail(fmt.Sprintf("Timeout after %ds — test still %s; %s", gateTimeout, run.Status, note))
			}
		}

		if strings.ToUpper(run.Status) == "FAILED" {
			return fail("Test FAILED")
		}

		report, err := apiClient.ReportSummary(ctx, id)
		if err != nil || report == nil {
			return fail("Failed to fetch report for grading")
		}

		throughput, p99 := report.AvgThroughputRecPerSec, report.P99LatencyMs
		res.AvgThroughputRecPerSec = &throughput
		// A run that measured no latency reads a P99 of 0, which grades as the
		// lowest latency there is. The backend's report fails a latency gate
		// it cannot judge, and so does this one.
		if !summaryMeasuredLatency(report) {
			return fail("P99 latency not measured: the run recorded no latency, so it cannot be graded")
		}
		res.P99LatencyMs = &p99
		res.Grade = computeGateGrade(report)
		res.Passed = gradeOrdinal(res.Grade) >= minOrdinal

		if jsonOut {
			output.JSON(res)
		} else {
			fmt.Println()
			output.SubHeader("Gate Result")
			output.KeyValue("Throughput", fmtNum(throughput)+" rec/s")
			output.KeyValue("P99 Latency", fmtFloat(p99, 3)+" ms")
			output.KeyValue("Grade", res.Grade)
			output.KeyValue("Threshold", gateMinGrade)
			fmt.Println()
		}

		if res.Passed {
			if !jsonOut {
				output.Success(fmt.Sprintf("✓ PASS — Grade %s meets minimum %s", res.Grade, gateMinGrade))
			}
			return nil
		}

		// cmdErr, not os.Exit(1): the exit code is the same, and a test can
		// run the command without it ending the test binary.
		return cmdErr(fmt.Sprintf("✖ FAIL — Grade %s below minimum %s", res.Grade, gateMinGrade))
	},
}

// gateResult is the outcome of kates gate, which -o json prints. Grade,
// Passed and the two metrics are set only once the run finished DONE and its
// report summary was read; Error says why a gate ended before that, or why a
// run that measured no latency got no grade. A metric is null until it is
// read, and P99 stays null when the run measured none: 0 would read as a
// measurement.
type gateResult struct {
	RunID                  string   `json:"runId"`
	TestType               string   `json:"testType"`
	Records                int      `json:"records"`
	Status                 string   `json:"status"`
	AvgThroughputRecPerSec *float64 `json:"avgThroughputRecPerSec"`
	P99LatencyMs           *float64 `json:"p99LatencyMs"`
	Grade                  string   `json:"grade,omitempty"`
	MinGrade               string   `json:"minGrade"`
	Passed                 bool     `json:"passed"`
	Error                  string   `json:"error,omitempty"`
}

func gradeOrdinal(g string) int {
	switch strings.ToUpper(g) {
	case "A":
		return 5
	case "B":
		return 4
	case "C":
		return 3
	case "D":
		return 2
	case "F":
		return 1
	default:
		return -1
	}
}

func computeGateGrade(s *client.ReportSummary) string {
	throughput := s.AvgThroughputRecPerSec
	p99 := s.P99LatencyMs

	switch {
	case throughput >= 50000 && p99 < 5:
		return "A"
	case throughput >= 30000 && p99 < 20:
		return "B"
	case throughput >= 15000 && p99 < 50:
		return "C"
	case throughput >= 5000 && p99 < 200:
		return "D"
	default:
		return "F"
	}
}

func init() {
	gateCmd.Flags().StringVar(&gateMinGrade, "min-grade", "C", "Minimum passing grade (A, B, C, D, F)")
	gateCmd.Flags().StringVar(&gateType, "type", "LOAD", "Test type to run")
	gateCmd.Flags().IntVar(&gateRecords, "records", 50000, "Number of records")
	gateCmd.Flags().StringVar(&gateBackend, "backend", "", "Benchmark backend")
	gateCmd.Flags().IntVar(&gateTimeout, "timeout", 180, "Timeout in seconds")
	interruptible(gateCmd)
	rootCmd.AddCommand(gateCmd)
}
