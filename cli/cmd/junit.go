package cmd

import (
	"encoding/xml"
	"fmt"
	"os"
	"time"

	"github.com/klster/kates-cli/client"
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
	Time     string          `xml:"time,attr"`
	Cases    []junitTestCase `xml:"testcase"`
}

type junitTestCase struct {
	XMLName   xml.Name      `xml:"testcase"`
	Name      string        `xml:"name,attr"`
	ClassName string        `xml:"classname,attr"`
	Time      string        `xml:"time,attr"`
	Failure   *junitFailure `xml:"failure,omitempty"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Content string `xml:",chardata"`
}

func writeJUnitXML(report *client.DisruptionReport, outputPath string) error {
	suite := junitTestSuite{
		Name:  "disruption:" + report.PlanName,
		Tests: len(report.StepReports),
	}

	for _, step := range report.StepReports {
		tc := junitTestCase{
			Name:      step.StepName,
			ClassName: "disruption." + report.PlanName,
			Time:      "0",
		}

		if step.TimeToAllReady != nil {
			tc.Time = fmt.Sprintf("%v", step.TimeToAllReady)
		}

		passed := step.ChaosOutcome != nil && step.ChaosOutcome.Verdict == "PASS"
		if !passed {
			suite.Failures++
			reason := "disruption step failed"
			if step.ChaosOutcome != nil && step.ChaosOutcome.FailureReason != "" {
				reason = step.ChaosOutcome.FailureReason
			}
			tc.Failure = &junitFailure{
				Message: reason,
				Type:    "disruption_failure",
				Content: fmt.Sprintf("Step: %s\nType: %s\nRolledBack: %v",
					step.StepName, step.DisruptionType, step.RolledBack),
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

	suite.Time = fmt.Sprintf("%.3f", float64(time.Now().Unix()))

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
