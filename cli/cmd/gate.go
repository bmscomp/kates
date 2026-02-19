package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/klster/kates-cli/client"
	"github.com/klster/kates-cli/output"
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
Designed for CI/CD pipelines where you want to block deploys on performance regression.`,
	Example: `  kates gate --min-grade B
  kates gate --min-grade C --type STRESS --records 100000
  kates gate --min-grade A --timeout 300`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		minOrdinal := gradeOrdinal(gateMinGrade)
		if minOrdinal < 0 {
			return cmdErr("Invalid grade: " + gateMinGrade + " (use A, B, C, D, or F)")
		}

		output.Banner("Quality Gate", fmt.Sprintf("min-grade=%s  type=%s  records=%d", gateMinGrade, gateType, gateRecords))
		fmt.Println()

		spec := &client.TestSpec{Records: gateRecords}
		req := &client.CreateTestRequest{
			TestType: gateType,
			Backend:  gateBackend,
			Spec:     spec,
		}
		run, err := apiClient.CreateTest(ctx, req)
		if err != nil {
			return cmdErr("Failed to create test: " + err.Error())
		}

		output.Success(fmt.Sprintf("Test started: %s", truncID(run.ID)))

		deadline := time.Now().Add(time.Duration(gateTimeout) * time.Second)
		for {
			time.Sleep(3 * time.Second)
			run, err = apiClient.GetTest(ctx, run.ID)
			if err != nil {
				return cmdErr("Failed to poll test: " + err.Error())
			}
			status := strings.ToUpper(run.Status)
			if status == "DONE" || status == "FAILED" {
				break
			}
			if time.Now().After(deadline) {
				return cmdErr(fmt.Sprintf("Timeout after %ds — test still %s", gateTimeout, run.Status))
			}
		}

		if strings.ToUpper(run.Status) == "FAILED" {
			output.Error("Test FAILED")
			os.Exit(1)
		}

		report, err := apiClient.ReportSummary(ctx, run.ID)
		if err != nil || report == nil {
			return cmdErr("Failed to fetch report for grading")
		}

		grade := computeGateGrade(report)
		gradeOrd := gradeOrdinal(grade)

		fmt.Println()
		output.SubHeader("Gate Result")
		output.KeyValue("Throughput", fmtNum(report.AvgThroughputRecPerSec)+" rec/s")
		output.KeyValue("P99 Latency", fmtFloat(report.P99LatencyMs, 3)+" ms")
		output.KeyValue("Grade", grade)
		output.KeyValue("Threshold", gateMinGrade)
		fmt.Println()

		if gradeOrd >= minOrdinal {
			output.Success(fmt.Sprintf("✓ PASS — Grade %s meets minimum %s", grade, gateMinGrade))
			return nil
		}

		output.Error(fmt.Sprintf("✖ FAIL — Grade %s below minimum %s", grade, gateMinGrade))
		os.Exit(1)
		return nil
	},
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
	rootCmd.AddCommand(gateCmd)
}
