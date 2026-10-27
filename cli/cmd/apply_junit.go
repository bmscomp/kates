package cmd

import (
	"encoding/xml"
	"fmt"
	"os"
	"strings"
	"time"
)

// writeApplyJUnit writes kates test apply --wait's JUnit report: one
// testsuite for the file, one testcase per scenario, as disruption run and
// conformity check write theirs.
//
//   - A run that ended DONE whose gates hold passes; gates that could not be
//     evaluated, and the run id, go in system-out.
//   - Violated SLA gates are a failure of type sla_violation. A run that ended
//     FAILED (or CANCELLED by someone else) is a failure of type run_failed,
//     its gate violations listed with it; with --no-sla it is the only kind.
//   - A scenario that could not be judged is an error: submit_error when its
//     run could not be created, wait_error when kates lost track of it.
//   - The scenario Ctrl-C stopped, and those after it, which were never
//     started, are skipped.
//
// A testcase's time is its scenario's, from the request that created its run
// to its end; the suite's is the apply's wall clock.
func writeApplyJUnit(path string, res applyResult, notStarted []TestScenario, took time.Duration) error {
	suite := junitTestSuite{Name: "apply:" + res.File, Time: fmt.Sprintf("%.3f", took.Seconds())}
	for _, r := range res.Scenarios {
		tc := applyJUnitCase(r)
		switch {
		case tc.Failure != nil:
			suite.Failures++
		case tc.Error != nil:
			suite.Errors++
		case tc.Skipped != nil:
			suite.Skipped++
		}
		suite.Cases = append(suite.Cases, tc)
	}
	for i, s := range notStarted {
		suite.Skipped++
		suite.Cases = append(suite.Cases, junitTestCase{
			Name:      scenarioName(s, len(res.Scenarios)+i),
			ClassName: applyJUnitClass(strings.ToUpper(s.Type)),
			Time:      "0.000",
			Skipped:   &junitSkipped{Message: "not started: the apply was interrupted"},
		})
	}
	suite.Tests = len(suite.Cases)

	data, err := xml.MarshalIndent(junitTestSuites{Suites: []junitTestSuite{suite}}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(append([]byte(xml.Header), data...), '\n'), 0o644)
}

func applyJUnitClass(testType string) string {
	if testType == "" {
		return "apply"
	}
	return "apply." + testType
}

// applyJUnitCase is one scenario's testcase.
func applyJUnitCase(r applyScenarioResult) junitTestCase {
	tc := junitTestCase{
		Name:      r.Name,
		ClassName: applyJUnitClass(r.Type),
		Time:      fmt.Sprintf("%.3f", r.elapsed.Seconds()),
	}
	var out []string
	if r.RunID != "" {
		out = append(out, "run "+r.RunID+": "+r.Status)
	}
	switch {
	case r.interrupted:
		tc.Skipped = &junitSkipped{Message: r.Status + ": " + r.Error}
	case r.RunID == "":
		tc.Error = &junitError{Message: r.Error, Type: "submit_error"}
	case r.Error != "":
		tc.Error = &junitError{Message: r.Error, Type: "wait_error"}
	case isFailedStatus(r.Status):
		content := ""
		if r.SLA != nil {
			content = strings.Join(r.SLA.Violations, "\n")
		}
		tc.Failure = &junitFailure{Message: "run " + strings.ToUpper(r.Status), Type: "run_failed", Content: content}
	case r.SLA != nil && len(r.SLA.Violations) > 0:
		tc.Failure = &junitFailure{
			Message: strings.Join(r.SLA.Violations, "; "),
			Type:    "sla_violation",
			Content: "scope: " + r.SLA.Scope + "\n" + strings.Join(r.SLA.Violations, "\n"),
		}
	}
	if r.SLA != nil && len(r.SLA.NotEvaluable) > 0 {
		out = append(out, "not evaluable: "+strings.Join(r.SLA.NotEvaluable, ", "))
	}
	tc.SystemOut = strings.Join(out, "\n")
	return tc
}
