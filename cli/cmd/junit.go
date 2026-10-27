package cmd

import (
	"encoding/xml"
	"fmt"
	"os"

	"github.com/bmscomp/kates/cli/client"
)

type junitTestSuites struct {
	XMLName xml.Name         `xml:"testsuites"`
	Suites  []junitTestSuite `xml:"testsuite"`
}

type junitTestSuite struct {
	XMLName  xml.Name        `xml:"testsuite"`
	Name     string          `xml:"name,attr"`
	Tests    int             `xml:"tests,attr"`
	Failures int             `xml:"failures,attr"`
	Errors   int             `xml:"errors,attr"`
	Skipped  int             `xml:"skipped,attr,omitempty"`
	Time     string          `xml:"time,attr"`
	Cases    []junitTestCase `xml:"testcase"`
}

type junitTestCase struct {
	XMLName   xml.Name      `xml:"testcase"`
	Name      string        `xml:"name,attr"`
	ClassName string        `xml:"classname,attr"`
	Time      string        `xml:"time,attr"`
	Failure   *junitFailure `xml:"failure,omitempty"`
	Error     *junitError   `xml:"error,omitempty"`
	Skipped   *junitSkipped `xml:"skipped,omitempty"`
	SystemOut string        `xml:"system-out,omitempty"`
}

type junitSkipped struct {
	Message string `xml:"message,attr"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Content string `xml:",chardata"`
}

// junitError is a test case that could not be judged, as opposed to one that
// was judged and failed: a step with no chaos outcome, a rule that could not
// be read. CI shows the two counts apart.
type junitError struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Content string `xml:",chardata"`
}

// backendMillis is a step's duration in milliseconds, read when the report
// was decoded from any of the backend's forms ("1234ms", "PT1.5S" or a
// number of seconds). ok is false for a value it cannot read, N/A among
// them, and for a negative one.
func backendMillis(d client.Duration) (millis int64, ok bool) {
	millis, ok = d.Millis()
	if !ok || millis < 0 {
		return 0, false
	}
	return millis, true
}

// junitSeconds is the decimal seconds a JUnit time attribute holds.
func junitSeconds(millis int64) string {
	return fmt.Sprintf("%.3f", float64(millis)/1000)
}

func writeJUnitXML(report *client.DisruptionReport, outputPath string) error {
	suite := junitTestSuite{
		Name:  "disruption:" + report.PlanName,
		Tests: len(report.StepReports),
	}

	// The report carries no start or end, so the suite's time is the steps'
	// recovery times added up. It used to be the Unix time of the export,
	// which CI read as a run of fifty-odd years.
	var totalMillis int64
	for _, step := range report.StepReports {
		tc := junitTestCase{
			Name:      step.StepName,
			ClassName: "disruption." + report.PlanName,
			Time:      "0",
		}

		if millis, ok := backendMillis(step.TimeToAllReady); ok {
			tc.Time = junitSeconds(millis)
			totalMillis += millis
		}

		content := fmt.Sprintf("Step: %s\nType: %s\nRolledBack: %v", step.StepName, step.DisruptionType, step.RolledBack)
		switch {
		case step.ChaosOutcome == nil:
			// No verdict at all: the step was not judged, which is not the
			// same as judged and failed.
			suite.Errors++
			tc.Error = &junitError{
				Message: "disruption step has no chaos outcome",
				Type:    "disruption_error",
				Content: content,
			}
		case step.ChaosOutcome.Verdict != "PASS":
			suite.Failures++
			reason := "disruption step failed"
			if step.ChaosOutcome.FailureReason != "" {
				reason = step.ChaosOutcome.FailureReason
			}
			tc.Failure = &junitFailure{
				Message: reason,
				Type:    "disruption_failure",
				Content: content,
			}
		}

		suite.Cases = append(suite.Cases, tc)
	}

	if report.SlaVerdict != nil && report.SlaVerdict.Violated {
		for _, v := range report.SlaVerdict.Violations {
			suite.Tests++
			suite.Failures++
			suite.Cases = append(suite.Cases, junitTestCase{
				Name:      fmt.Sprintf("SLA: %s (%s)", v.MetricName, v.Constraint),
				ClassName: "disruption." + report.PlanName + ".sla",
				Time:      "0",
				Failure: &junitFailure{
					Message: fmt.Sprintf("%s %s: threshold=%.2f actual=%.2f",
						v.MetricName, v.Constraint, v.Threshold, v.Actual),
					Type: "sla_violation",
					Content: fmt.Sprintf("Severity: %s\nMetric: %s\nConstraint: %s\nThreshold: %.2f\nActual: %.2f",
						v.Severity, v.MetricName, v.Constraint, v.Threshold, v.Actual),
				},
			})
		}
	}

	suite.Time = junitSeconds(totalMillis)

	suites := junitTestSuites{
		Suites: []junitTestSuite{suite},
	}

	data, err := xml.MarshalIndent(suites, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JUnit XML: %w", err)
	}

	content := xml.Header + string(data) + "\n"
	if err := os.WriteFile(outputPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write JUnit XML to %s: %w", outputPath, err)
	}

	return nil
}
