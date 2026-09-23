package cmd

import (
	"strings"
	"testing"
)

func TestTestGet_ShowsIntegrityRtoRpoAndVerdict(t *testing.T) {
	mockResponse := `{
		"id": "run-1",
		"testType": "INTEGRITY",
		"status": "DONE",
		"results": [{
			"phaseName": "integrity",
			"status": "DONE",
			"integrity": {
				"totalSent": 1000, "totalAcked": 1000, "totalConsumed": 998,
				"lostRecords": 2, "dataLossPercent": 0.2,
				"producerRto": 1.5, "maxRto": 1.5, "rpo": null,
				"producerRtoMs": 1500.0, "consumerRtoMs": 0.0, "maxRtoMs": 1500.0, "rpoMs": -1.0,
				"verdict": "DATA_LOSS"
			}
		}]
	}`
	ts, buf := setupTest(t, "GET", "/api/tests/run-1", 200, mockResponse)
	defer ts.Close()

	if err := testGetCmd.RunE(testGetCmd, []string{"run-1"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stripAnsi(buf.String())
	for _, want := range []string{"Producer RTO", "1500 ms", "Max RTO", "DATA_LOSS"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// rpoMs -1 is "not measured", never a zero or a hidden line.
	if !strings.Contains(out, "not measured") {
		t.Errorf("unmeasured RPO should print as not measured:\n%s", out)
	}
	if strings.Contains(out, "-1 ms") {
		t.Errorf("the -1 sentinel must not be printed as a value:\n%s", out)
	}
}
