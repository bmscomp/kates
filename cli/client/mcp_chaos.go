package client

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Read-only calls the chaos tools of `kates mcp` need that the CLI had no
// method for, and fuller types for the disruption report than the CLI's own,
// which leave out the fields the tools report (fault start and end, the
// metrics Prometheus had no data for, the SLA checks it could not evaluate).

// DisruptionTemplate is one entry of GET /api/disruptions/templates
// (disruption/ChaosTemplateCatalog.java, TemplateInfo).
type DisruptionTemplate struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	Description          string `json:"description"`
	Category             string `json:"category"`
	Severity             string `json:"severity"`
	EstimatedDurationSec int    `json:"estimatedDurationSec"`
}

// DisruptionTemplates lists the chaos templates. It only reads: running a
// template is a POST this client never sends for them.
func (c *Client) DisruptionTemplates(ctx context.Context) ([]DisruptionTemplate, error) {
	return get[[]DisruptionTemplate](c, ctx, "/api/disruptions/templates")
}

// DisruptionProviders lists the registered chaos providers, each as the
// backend formats it: its name followed by " (available)" or
// " (unavailable)".
func (c *Client) DisruptionProviders(ctx context.Context) ([]string, error) {
	return get[[]string](c, ctx, "/api/disruptions/providers")
}

// DisruptionImpact is GET /api/disruptions/{id}/impact
// (disruption/DisruptionImpactScorer.java, toMap).
type DisruptionImpact struct {
	Overall    int            `json:"overall"`
	Severity   string         `json:"severity"`
	Dimensions map[string]int `json:"dimensions"`
	Factors    []string       `json:"factors"`
}

func (c *Client) DisruptionImpact(ctx context.Context, id string) (*DisruptionImpact, error) {
	path, err := pathf("/api/disruptions/%s/impact", id)
	if err != nil {
		return nil, err
	}
	return get[*DisruptionImpact](c, ctx, path)
}

// DisruptionComparison is GET /api/disruptions/{id}/compare. Deltas, Current
// and Baseline are present only when both reports have a summary.
type DisruptionComparison struct {
	CurrentID  string                     `json:"currentId"`
	BaselineID string                     `json:"baselineId"`
	Deltas     *DisruptionComparisonDelta `json:"deltas,omitempty"`
	Current    *DisruptionComparisonSide  `json:"current,omitempty"`
	Baseline   *DisruptionComparisonSide  `json:"baseline,omitempty"`
}

type DisruptionComparisonDelta struct {
	RecoveryDeltaMs        int64   `json:"recoveryDeltaMs"`
	ThroughputDeltaPercent float64 `json:"throughputDeltaPercent"`
	P99DeltaPercent        float64 `json:"p99DeltaPercent"`
}

type DisruptionComparisonSide struct {
	Status      string `json:"status"`
	SlaGrade    string `json:"slaGrade"`
	PassedSteps string `json:"passedSteps"`
}

// DisruptionCompare compares report id with report baselineID.
func (c *Client) DisruptionCompare(ctx context.Context, id, baselineID string) (*DisruptionComparison, error) {
	path, err := pathf("/api/disruptions/%s/compare", id)
	if err != nil {
		return nil, err
	}
	return get[*DisruptionComparison](c, ctx, withQuery(path, url.Values{"baselineId": {baselineID}}))
}

// DisruptionReportDetail is GET /api/disruptions/{id} with every field the
// backend's DisruptionReport carries that a reader of the report needs.
type DisruptionReportDetail struct {
	PlanName           string                   `json:"planName"`
	Status             string                   `json:"status"`
	StepReports        []DisruptionStepDetail   `json:"stepReports"`
	Summary            *DisruptionSummaryDetail `json:"summary,omitempty"`
	ValidationWarnings []string                 `json:"validationWarnings,omitempty"`
	SlaVerdict         *DisruptionSlaVerdict    `json:"slaVerdict,omitempty"`
}

type DisruptionStepDetail struct {
	StepName               string                  `json:"stepName"`
	DisruptionType         string                  `json:"disruptionType"`
	ChaosOutcome           *DisruptionChaosOutcome `json:"chaosOutcome,omitempty"`
	PodTimeline            []PodEvent              `json:"podTimeline,omitempty"`
	TimeToFirstReady       BackendDuration         `json:"timeToFirstReady"`
	TimeToAllReady         BackendDuration         `json:"timeToAllReady"`
	UnrecoveredAfter       BackendDuration         `json:"unrecoveredAfter"`
	ImpactDeltas           map[string]float64      `json:"impactDeltas,omitempty"`
	TargetedLeaderBrokerID *int                    `json:"targetedLeaderBrokerId,omitempty"`
	IsrMetrics             *DisruptionIsrDetail    `json:"isrMetrics,omitempty"`
	LagMetrics             *DisruptionLagDetail    `json:"lagMetrics,omitempty"`
	RolledBack             bool                    `json:"rolledBack"`
	RollbackReason         string                  `json:"rollbackReason,omitempty"`
	UnmeasuredMetrics      []string                `json:"unmeasuredMetrics,omitempty"`
}

// DisruptionIsrDetail is a step's ISR metrics as the report carries them
// (disruption/IsrSnapshot.java, Metrics). Its timeline, one entry per
// partition per poll, is left out.
type DisruptionIsrDetail struct {
	TimeToFullIsr       BackendDuration `json:"timeToFullIsr"`
	MinIsrDepth         int             `json:"minIsrDepth"`
	UnderReplicatedPeak int             `json:"underReplicatedPeakCount"`
	TotalPartitions     int             `json:"totalPartitions"`
}

// DisruptionLagDetail is a step's consumer-lag metrics as the report carries
// them (disruption/LagSnapshot.java, Metrics), without the timeline.
type DisruptionLagDetail struct {
	BaselineLag       int64           `json:"baselineLag"`
	PeakLag           int64           `json:"peakLag"`
	TimeToLagRecovery BackendDuration `json:"timeToLagRecovery"`
}

type DisruptionChaosOutcome struct {
	EngineName             string          `json:"engineName"`
	ExperimentName         string          `json:"experimentName"`
	ChaosStartTime         string          `json:"chaosStartTime"`
	ChaosEndTime           string          `json:"chaosEndTime"`
	ChaosDuration          BackendDuration `json:"chaosDuration"`
	Verdict                string          `json:"verdict"`
	FailureReason          string          `json:"failureReason"`
	ProbeSuccessPercentage string          `json:"probeSuccessPercentage"`
	FailStep               string          `json:"failStep"`
	Phase                  string          `json:"phase"`
}

type DisruptionSummaryDetail struct {
	TotalSteps               int             `json:"totalSteps"`
	PassedSteps              int             `json:"passedSteps"`
	WorstRecovery            BackendDuration `json:"worstRecovery"`
	AvgThroughputDegradation float64         `json:"avgThroughputDegradation"`
	MaxP99LatencySpike       float64         `json:"maxP99LatencySpike"`
	SlaViolated              bool            `json:"slaViolated"`
	WorstIsrRecovery         BackendDuration `json:"worstIsrRecovery"`
	PeakConsumerLag          int64           `json:"peakConsumerLag"`
}

// DisruptionSlaVerdict is disruption/SlaGrader.java's SlaVerdict, with the
// constraints it could not evaluate, which the CLI's SlaVerdict leaves out.
type DisruptionSlaVerdict struct {
	Grade        string                   `json:"grade"`
	Violated     bool                     `json:"violated"`
	Violations   []DisruptionSlaViolation `json:"violations,omitempty"`
	TotalChecks  int                      `json:"totalChecks"`
	PassedChecks int                      `json:"passedChecks"`
	Unevaluated  []string                 `json:"unevaluated,omitempty"`
}

type DisruptionSlaViolation struct {
	MetricName string  `json:"metricName"`
	Constraint string  `json:"constraint"`
	Threshold  float64 `json:"threshold"`
	Actual     float64 `json:"actual"`
	Severity   string  `json:"severity"`
}

func (c *Client) DisruptionReportDetail(ctx context.Context, id string) (*DisruptionReportDetail, error) {
	path, err := pathf("/api/disruptions/%s", id)
	if err != nil {
		return nil, err
	}
	return get[*DisruptionReportDetail](c, ctx, path)
}

// BackendDuration is a java.time.Duration as the backend sends it. The report
// endpoints serialise one with Jackson's default, a number of seconds
// (fractions carry the nanoseconds); the timeline and Kafka-metrics endpoints
// format their own, as "1234ms", or "N/A" when there is none. An ISO-8601
// string ("PT1.5S") is read too, in case the serialisation setting changes.
// Anything else, and null, leaves it unset rather than failing the whole
// response over one field.
type BackendDuration struct {
	Millis int64
	Set    bool
}

func (d *BackendDuration) UnmarshalJSON(b []byte) error {
	*d = BackendDuration{}
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return nil
		}
		*d = ParseBackendDuration(s)
		return nil
	}
	secs, err := strconv.ParseFloat(string(b), 64)
	if err != nil || math.IsNaN(secs) || math.IsInf(secs, 0) || math.Abs(secs) > 1e12 {
		return nil
	}
	*d = BackendDuration{Millis: int64(math.Round(secs * 1000)), Set: true}
	return nil
}

// ParseBackendDuration reads a duration the backend formatted as text:
// "1234ms", an ISO-8601 duration such as "PT1.5S", or a plain number of
// seconds. "N/A" and anything else is unset.
func ParseBackendDuration(s string) BackendDuration {
	s = strings.TrimSpace(s)
	if ms, ok := strings.CutSuffix(s, "ms"); ok {
		n, err := strconv.ParseInt(strings.TrimSpace(ms), 10, 64)
		if err != nil {
			return BackendDuration{}
		}
		return BackendDuration{Millis: n, Set: true}
	}
	if strings.HasPrefix(s, "PT") || strings.HasPrefix(s, "-PT") {
		// time.ParseDuration reads "1H2M3.5S" once lowercased.
		neg := strings.HasPrefix(s, "-")
		body := strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(s, "-"), "PT"))
		if body == "" {
			return BackendDuration{}
		}
		dur, err := time.ParseDuration(body)
		if err != nil {
			return BackendDuration{}
		}
		if neg {
			dur = -dur
		}
		return BackendDuration{Millis: dur.Milliseconds(), Set: true}
	}
	if secs, err := strconv.ParseFloat(s, 64); err == nil && !math.IsNaN(secs) && !math.IsInf(secs, 0) && math.Abs(secs) <= 1e12 {
		return BackendDuration{Millis: int64(math.Round(secs * 1000)), Set: true}
	}
	return BackendDuration{}
}
