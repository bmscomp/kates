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
	gateMinGrade string
	gateType     string
	gateRecords  int
	gateBackend  string
	gateTimeout  string
)

// defaultGateTimeout bounds the wait for the gate's run; it used to be an
// integer count of seconds, which --timeout still accepts.
const defaultGateTimeout = "180"

var gateCmd = &cobra.Command{
	Use:     "gate",
	Aliases: []string{"ci", "quality-gate"},
	Short:   "CI quality gate — run a test and exit non-zero if grade is below threshold",
	Long: `Run a performance test and evaluate the result against a minimum grade.
Exits with code 0 if the grade meets or exceeds the threshold, or code 1 if it fails.
Designed for CI/CD pipelines where you want to block deploys on performance regression.

The grade comes from the run's average throughput and P99 latency; a run must
clear both numbers of a rung to earn it:

` + grade.GateThresholds() + `

When --timeout passes with the run still going, the run is cancelled and the
gate fails. --timeout takes a duration such as 10m, or a number of seconds as
it always has.

With -o json it prints only the result, as JSON, once the test exists: the run
ID, its status, the throughput and p99 it was graded on, the grade and whether
it passed. The exit code is the same.`,
	Example: `  kates gate --min-grade B
  kates gate --min-grade C --type STRESS --records 100000
  kates gate --min-grade A --timeout 5m
  kates gate --min-grade B -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Ctrl-C cancels this context (see interrupt.go), and the run it
		// started with it: a gate killed in CI used to leave the test running.
		ctx := commandContext(cmd)
		jsonOut := outputMode == "json"

		minGrade, err := grade.Parse(gateMinGrade)
		if err != nil {
			return cmdErr("Invalid grade: " + err.Error())
		}
		timeout, err := parseTimeoutFlag(gateTimeout)
		if err != nil {
			return cmdErr(err.Error())
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

		run, err = followRun(ctx, id, followOpts{
			interval:     testPollInterval,
			deadline:     time.Now().Add(timeout),
			onPoll:       func(r *client.TestRun) { res.Status = r.Status },
			cancelOnStop: stopStartedRun,
		})
		switch {
		case errors.Is(err, errInterrupted):
			return err
		case errors.Is(err, errFollowDeadline):
			// A run the gate gave up on is cancelled, not left to load the
			// cluster while the next pipeline step starts.
			still := res.Status
			cancelled, note := cancelStartedRun(id)
			res.Status = cancelled
			return fail(fmt.Sprintf("Timeout after %s — test still %s; %s", timeout, still, note))
		case err != nil:
			return fail("Failed to poll test: " + err.Error())
		}

		if isFailedStatus(run.Status) {
			return fail("Test " + output.Printable(run.Status))
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
		got := grade.ForGate(throughput, p99)
		res.Grade = got.String()
		res.Passed = grade.AtLeast(got, minGrade)

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

func init() {
	gateCmd.Flags().StringVar(&gateMinGrade, "min-grade", "C", "Minimum passing grade ("+grade.Letters+")")
	gateCmd.Flags().StringVar(&gateType, "type", "LOAD", "Test type to run")
	addRecordsFlag(gateCmd, &gateRecords, 50000)
	gateCmd.Flags().StringVar(&gateBackend, "backend", "", "Benchmark backend")
	addTimeoutFlag(gateCmd, &gateTimeout, defaultGateTimeout)
	interruptible(gateCmd)
	rootCmd.AddCommand(gateCmd)
}
