package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/client"
)

// The step time used to be the backend's value printed as is ("1500ms" is
// not a JUnit time), the suite time was the Unix time of the export, and a
// step with no outcome at all counted as a failure.
func TestWriteJUnitXML_TimesAndErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "disruption.xml")
	report := &client.DisruptionReport{
		PlanName: "leader-cascade",
		StepReports: []client.StepReport{
			{StepName: "kill-leader", DisruptionType: "POD_DELETE", TimeToAllReady: client.DurationOf(`"1500ms"`),
				ChaosOutcome: &client.ChaosOutcome{Verdict: "PASS"}},
			{StepName: "partition", DisruptionType: "NETWORK_PARTITION", TimeToAllReady: client.DurationOf(`"PT2S"`),
				ChaosOutcome: &client.ChaosOutcome{Verdict: "FAIL", FailureReason: "probe failed"}},
			{StepName: "no-verdict", DisruptionType: "CPU_STRESS", TimeToAllReady: client.DurationOf(`3.0`)},
			{StepName: "unreadable", DisruptionType: "DISK_FILL", TimeToAllReady: client.DurationOf(`"N/A"`),
				ChaosOutcome: &client.ChaosOutcome{Verdict: "PASS"}},
		},
	}
	if err := writeJUnitXML(report, path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	xml := string(data)
	for _, want := range []string{
		`<testsuite name="disruption:leader-cascade" tests="4" failures="1" errors="1" time="6.500">`,
		`<testcase name="kill-leader" classname="disruption.leader-cascade" time="1.500"></testcase>`,
		`<testcase name="partition" classname="disruption.leader-cascade" time="2.000">`,
		`<failure message="probe failed" type="disruption_failure">`,
		`<testcase name="no-verdict" classname="disruption.leader-cascade" time="3.000">`,
		`<error message="disruption step has no chaos outcome" type="disruption_error">`,
		`<testcase name="unreadable" classname="disruption.leader-cascade" time="0"></testcase>`,
	} {
		if !strings.Contains(xml, want) {
			t.Errorf("JUnit lacks %s:\n%s", want, xml)
		}
	}
	if strings.Count(xml, "<failure") != 1 || strings.Count(xml, "<error") != 1 {
		t.Errorf("want one failure and one error:\n%s", xml)
	}
}

func TestBackendMillis(t *testing.T) {
	for _, tc := range []struct {
		in     string
		millis int64
		ok     bool
	}{
		{`"1500ms"`, 1500, true},
		{`"PT2S"`, 2000, true},
		{`"PT1.5S"`, 1500, true},
		{`3.0`, 3000, true},
		{`0.25`, 250, true},
		{`"-5ms"`, 0, false},
		{`"N/A"`, 0, false},
		{`null`, 0, false},
		{`""`, 0, false},
	} {
		millis, ok := backendMillis(client.DurationOf(tc.in))
		if millis != tc.millis || ok != tc.ok {
			t.Errorf("backendMillis(%s) = %d, %v; want %d, %v", tc.in, millis, ok, tc.millis, tc.ok)
		}
	}
}
