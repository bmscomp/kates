package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// TestChaosReadPathsAreEscaped: the ids the chaos reads put in a path stay
// one segment, and the baseline id stays one query value.
func TestChaosReadPathsAreEscaped(t *testing.T) {
	const id = hostileIDEscaped
	tests := []struct {
		name    string
		wantURI string
		call    clientCall
	}{
		{"DisruptionImpact", "/api/disruptions/" + id + "/impact",
			func(ctx context.Context, c *Client) error { return ignore(c.DisruptionImpact(ctx, hostileID)) }},
		{"DisruptionReportDetail", "/api/disruptions/" + id,
			func(ctx context.Context, c *Client) error { return ignore(c.DisruptionReportDetail(ctx, hostileID)) }},
		{"DisruptionCompare", "/api/disruptions/" + id + "/compare?baselineId=..%2Fx%26dryRun%3Dfalse",
			func(ctx context.Context, c *Client) error {
				return ignore(c.DisruptionCompare(ctx, hostileID, "../x&dryRun=false"))
			}},
		{"DisruptionTemplates", "/api/disruptions/templates",
			func(ctx context.Context, c *Client) error { return ignore(c.DisruptionTemplates(ctx)) }},
		{"DisruptionProviders", "/api/disruptions/providers",
			func(ctx context.Context, c *Client) error { return ignore(c.DisruptionProviders(ctx)) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := firstRequest(t, tt.call)
			if got.Method != http.MethodGet {
				t.Errorf("method = %s, want GET: these only read", got.Method)
			}
			if got.RequestURI != tt.wantURI {
				t.Errorf("request URI = %q, want %q", got.RequestURI, tt.wantURI)
			}
		})
	}
	if got := firstRequest(t, func(ctx context.Context, c *Client) error {
		return ignore(c.DisruptionCompare(ctx, "0a1b2c3d", "../x&dryRun=false"))
	}); got.Query.Get("baselineId") != "../x&dryRun=false" || len(got.Query) != 1 {
		t.Errorf("query = %v, want the baseline id as one value", got.Query)
	}
}

func TestChaosReadsRefuseDotAndEmpty(t *testing.T) {
	calls := map[string]func(c *Client, id string) error{
		"DisruptionImpact":       func(c *Client, id string) error { return ignore(c.DisruptionImpact(context.Background(), id)) },
		"DisruptionReportDetail": func(c *Client, id string) error { return ignore(c.DisruptionReportDetail(context.Background(), id)) },
		"DisruptionCompare":      func(c *Client, id string) error { return ignore(c.DisruptionCompare(context.Background(), id, "b")) },
	}
	for name, call := range calls {
		for _, id := range []string{"", ".", ".."} {
			c, requests := recordingServer(t)
			if err := call(c, id); !errors.Is(err, ErrInvalidPathSegment) {
				t.Errorf("%s(%q): err = %v, want ErrInvalidPathSegment", name, id, err)
			}
			if n := len(requests()); n != 0 {
				t.Errorf("%s(%q) sent %d requests", name, id, n)
			}
		}
	}
}

func TestBackendDuration(t *testing.T) {
	tests := []struct {
		json string
		ms   int64
		set  bool
	}{
		{`12.750000000`, 12750, true}, // Jackson's default: seconds, with nanoseconds
		{`0`, 0, true},
		{`0.0004`, 0, true},
		{`"1234ms"`, 1234, true}, // the timeline and Kafka-metrics endpoints
		{`"N/A"`, 0, false},
		{`"PT1.5S"`, 1500, true}, // ISO-8601, if the setting changes
		{`"PT2M3S"`, 123000, true},
		{`"-PT1S"`, -1000, true},
		{`"PT"`, 0, false},
		{`null`, 0, false},
		{`"soon"`, 0, false},
		{`true`, 0, false},
		{`1e300`, 0, false},
		{`{"seconds":1}`, 0, false},
	}
	for _, tt := range tests {
		var d BackendDuration
		if err := json.Unmarshal([]byte(tt.json), &d); err != nil {
			t.Errorf("%s: %v; an unreadable duration must not fail the response", tt.json, err)
			continue
		}
		if d.Set != tt.set || d.Millis != tt.ms {
			t.Errorf("%s: got %+v, want %d ms set=%v", tt.json, d, tt.ms, tt.set)
		}
	}
}

// TestDisruptionReportDetailDecodes reads a report as the backend writes one,
// fields the client does not name included.
func TestDisruptionReportDetailDecodes(t *testing.T) {
	const body = `{"planName":"playbook:leader-cascade","status":"PARTIAL","stepReports":[{"stepName":"s1","disruptionType":"POD_KILL",` +
		`"chaosOutcome":{"engineName":"e","experimentName":"x","chaosStartTime":"2026-09-25T12:00:00Z","chaosEndTime":"2026-09-25T12:00:30Z",` +
		`"chaosStartNanos":123456789012345678,"chaosDuration":30.000000000,"verdict":"Pass"},"podTimeline":[{"timestamp":"2026-09-25T12:00:01Z",` +
		`"podName":"p","eventType":"DELETED","phase":"Running","reason":"","message":""}],"timeToFirstReady":4.2,"timeToAllReady":null,` +
		`"impactDeltas":{"p99LatencyMs":12.5},"rolledBack":false,"unmeasuredMetrics":["bytesOutPerSec"],"unrecoveredAfter":300.0,` +
		`"isrMetrics":{"timeToFullIsr":8.5,"minIsrDepth":2,"underReplicatedPeakCount":3,"totalPartitions":12,"timeline":[{"timestamp":"2026-09-25T12:00:02Z",` +
		`"topic":"orders","partition":0,"leaderId":3,"isr":[3,4],"replicationFactor":3}]},` +
		`"lagMetrics":{"baselineLag":10,"peakLag":900,"timeToLagRecovery":null,"timeline":[]}}],"summary":{"totalSteps":1,"passedSteps":1,"worstRecovery":0.0,"avgThroughputDegradation":-3.5,` +
		`"maxP99LatencySpike":12.5,"slaViolated":false,"worstIsrRecovery":0.0,"peakConsumerLag":0},` +
		`"slaVerdict":{"grade":"-","violated":false,"violations":[],"totalChecks":0,"passedChecks":0,"unevaluated":["maxP999LatencyMs: not captured"]}}`
	c, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
	r, err := c.DisruptionReportDetail(context.Background(), "0a1b2c3d")
	if err != nil {
		t.Fatal(err)
	}
	s := r.StepReports[0]
	if s.ChaosOutcome.ChaosDuration.Millis != 30000 || s.TimeToFirstReady.Millis != 4200 || s.TimeToAllReady.Set ||
		s.UnrecoveredAfter.Millis != 300000 || s.ImpactDeltas["p99LatencyMs"] != 12.5 || len(s.PodTimeline) != 1 ||
		strings.Join(s.UnmeasuredMetrics, ",") != "bytesOutPerSec" || s.ChaosOutcome.ChaosStartTime != "2026-09-25T12:00:00Z" {
		t.Errorf("step = %+v", s)
	}
	if i := s.IsrMetrics; i == nil || i.TimeToFullIsr.Millis != 8500 || i.MinIsrDepth != 2 || i.UnderReplicatedPeak != 3 || i.TotalPartitions != 12 {
		t.Errorf("isr = %+v", s.IsrMetrics)
	}
	if l := s.LagMetrics; l == nil || l.BaselineLag != 10 || l.PeakLag != 900 || l.TimeToLagRecovery.Set {
		t.Errorf("lag = %+v", s.LagMetrics)
	}
	if !r.Summary.WorstRecovery.Set || r.Summary.WorstRecovery.Millis != 0 || r.Summary.AvgThroughputDegradation != -3.5 {
		t.Errorf("summary = %+v", r.Summary)
	}
	if r.SlaVerdict.Grade != "-" || len(r.SlaVerdict.Unevaluated) != 1 {
		t.Errorf("sla = %+v", r.SlaVerdict)
	}
}
