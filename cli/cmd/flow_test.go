package cmd

import (
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/client"
	"gopkg.in/yaml.v3"
)

// A step's spec is written with the request's own field names. TestSpec had
// json tags only, so yaml read it by lower-cased Go field names and a
// numRecords in a flow never reached the request.
func TestFlowTestRequest_SpecFromYAML(t *testing.T) {
	const doc = `
name: qual
steps:
  - name: load
    action: test
    type: STRESS
    spec:
      numRecords: 500
      numProducers: 4
      recordSize: 2048
      acks: all
      lingerMs: 0
      compressionType: zstd
    gate:
      minGrade: B
      maxP99: 25
      maxErrorRate: 0
`
	var p FlowPipeline
	if err := yaml.Unmarshal([]byte(doc), &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Steps) != 1 {
		t.Fatalf("steps = %d", len(p.Steps))
	}
	req := flowTestRequest(p.Steps[0])
	if req.TestType != "STRESS" {
		t.Errorf("type = %s", req.TestType)
	}
	s := req.Spec
	if s == nil || s.Records != 500 || s.ParallelProducers != 4 || s.RecordSizeBytes != 2048 || s.Acks != "all" || s.CompressionType != "zstd" {
		t.Errorf("spec = %+v, want numRecords 500, numProducers 4, recordSize 2048, acks all, zstd", s)
	}
	if s.LingerMs == nil || *s.LingerMs != 0 {
		t.Errorf("lingerMs = %v, want an explicit 0", s.LingerMs)
	}
	g := p.Steps[0].Gate
	if g == nil || g.MinGrade != "B" || g.MaxP99 == nil || *g.MaxP99 != 25 || g.MinThroughput != nil || g.MaxErrorRate == nil || *g.MaxErrorRate != 0 {
		t.Errorf("gate = %+v, want minGrade B, maxP99 25, maxErrorRate 0 and no minThroughput", g)
	}

	// records on the step takes over the spec's, and a step without a spec
	// still gets one.
	p.Steps[0].Records = 42
	if got := flowTestRequest(p.Steps[0]).Spec.Records; got != 42 {
		t.Errorf("records = %d, want the step's 42", got)
	}
	if req := flowTestRequest(FlowStep{Name: "bare", Action: "test"}); req.TestType != "LOAD" || req.Spec == nil {
		t.Errorf("bare step = %+v, want LOAD with a spec", req)
	}
}

func f64(v float64) *float64 { return &v }

// The three numeric gates used to be read and never checked.
func TestFlowGateFailure(t *testing.T) {
	ok := &client.ReportSummary{AvgThroughputRecPerSec: 40000, P99LatencyMs: 12, ErrorRate: 0.001}
	noLatency := &client.ReportSummary{AvgThroughputRecPerSec: 40000}
	tests := []struct {
		name    string
		gate    *FlowGate
		grade   string
		summary *client.ReportSummary
		want    string // a substring of the detail, "" for a met gate
	}{
		{"no gate", nil, "", nil, ""},
		{"grade met", &FlowGate{MinGrade: "B"}, "A", nil, ""},
		{"grade below", &FlowGate{MinGrade: "B"}, "C", ok, "grade C < B"},
		{"p99 met", &FlowGate{MaxP99: f64(20)}, "", ok, ""},
		{"p99 over", &FlowGate{MaxP99: f64(10)}, "", ok, "p99 12.00ms > maxP99 10.00ms"},
		{"p99 not measured", &FlowGate{MaxP99: f64(10)}, "", noLatency, "p99 not measured"},
		{"throughput met", &FlowGate{MinThroughput: f64(30000)}, "", ok, ""},
		{"throughput under", &FlowGate{MinThroughput: f64(50000)}, "", ok, "throughput 40000 rec/s < minThroughput 50000 rec/s"},
		{"error rate met", &FlowGate{MaxErrorRate: f64(0.01)}, "", ok, ""},
		{"error rate over", &FlowGate{MaxErrorRate: f64(0)}, "", ok, "error rate 0.0010 > maxErrorRate 0.0000"},
		{"no summary", &FlowGate{MaxP99: f64(10)}, "", nil, "no report summary"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := flowGateFailure(tc.gate, tc.grade, tc.summary)
			if tc.want == "" && got != "" {
				t.Errorf("detail = %q, want the gate met", got)
			}
			if tc.want != "" && !strings.Contains(got, tc.want) {
				t.Errorf("detail = %q, want %q", got, tc.want)
			}
		})
	}
}

// Grades compare in upper case on the API's scale, and a grade neither side
// knows fails: both used to rank as 0, so "" met a gate of D. The A+ and B-
// forms an older flow ranked are not grades the API gives, so a gate that
// names one fails too, rather than never being met.
func TestCheckGrade(t *testing.T) {
	for _, tc := range []struct {
		actual, minimum string
		want            bool
	}{
		{"A", "B", true}, {"b", "B", true}, {"B", "a", false}, {"a+", "A", false}, {"A", "B-", false},
		{"", "D", false}, {"X", "F", false}, {"A", "ZZ", false}, {"", "", false}, {"-", "F", false},
	} {
		if got := checkGrade(tc.actual, tc.minimum); got != tc.want {
			t.Errorf("checkGrade(%q, %q) = %v, want %v", tc.actual, tc.minimum, got, tc.want)
		}
	}
}
