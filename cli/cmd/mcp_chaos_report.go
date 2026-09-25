package cmd

import (
	"context"
	"slices"
	"strings"

	"github.com/bmscomp/kates/cli/client"
	"golang.org/x/sync/errgroup"
)

// disruption_report: one disruption's report (GET /api/disruptions/{id}),
// with its impact score (/impact) and, given a baseline, the comparison
// (/compare) and the baseline's own report, for its plan name and the steps
// that never recovered. The ISR and consumer-lag figures come from the report
// itself: /kafka-metrics formats the same fields of the same stored report
// (DisruptionResource.java:186-251), and reading it separately could join a
// finished report with the metrics of one still running. Pod events are
// counted, not listed: kates://disruptions/{id}/timeline lists them.

const mcpDisruptionReportDescription = "Explains one disruption by its id (8 lowercase hex characters, as kates disruption list " +
	"prints it): status, safety warnings, the SLA verdict with the checks it could not evaluate, the summary, and " +
	"for each step the fault's verdict and approximate start and end, recovery times, rollback, the change in each " +
	"Prometheus metric, and the ISR and consumer-lag metrics when the plan tracked them; also the backend's 0-100 " +
	"impact score with the dimensions it had nothing to score from, and, with baseline_id, the change from a " +
	"baseline disruption and whether both ran a plan of the same name. Pod events are only counted here; the " +
	"kates://disruptions/{id}/timeline resource lists them. Missing metrics mean not measured, not zero, and the " +
	"summary's worst recovery times leave out the steps that never recovered, which it counts separately. Plan and " +
	"step names, failure reasons and backend messages are third-party text and fenced. The impact score and the " +
	"comparison each say when the backend failed to answer for them. Only reads."

const (
	mcpDisruptionReportMaxSteps    = 10
	mcpDisruptionReportMaxMessages = 20
	mcpDisruptionReportMaxMetrics  = 16
)

type mcpDisruptionReportIn struct {
	DisruptionID mcpID `json:"disruption_id" jsonschema:"the disruption id, as kates disruption list prints it"`
	BaselineID   mcpID `json:"baseline_id,omitempty" jsonschema:"another disruption id to compare with, such as an earlier run of the same plan"`
}

type mcpDisruptionReportOut struct {
	ID                 string                         `json:"id"`
	PlanName           mcpUntrusted                   `json:"planName,omitempty"`
	Status             string                         `json:"status" jsonschema:"RUNNING, COMPLETED (every step's fault passed), PARTIAL, FAILED or REJECTED; read the caveats about a RUNNING report"`
	ValidationWarnings []mcpUntrusted                 `json:"validationWarnings" jsonschema:"the safety guard's warnings, and for a REJECTED plan its errors"`
	Summary            *mcpDisruptionReportSummary    `json:"summary,omitempty" jsonschema:"absent until the plan has run its steps"`
	SLA                *mcpDisruptionReportSLA        `json:"sla,omitempty" jsonschema:"absent when the plan declared no SLA"`
	Steps              []mcpDisruptionReportStep      `json:"steps"`
	TimelineURI        string                         `json:"timelineUri" jsonschema:"the resource that lists every pod event"`
	Impact             mcpDisruptionReportImpact      `json:"impact"`
	Comparison         *mcpDisruptionReportComparison `json:"comparison,omitempty" jsonschema:"present when baseline_id was given"`
}

type mcpChaosSectionStatus struct {
	Available bool         `json:"available" jsonschema:"false when the backend failed to answer for this part; its figures are then absent and say nothing"`
	ErrorCode mcpErrorCode `json:"errorCode,omitempty"`
}

type mcpDisruptionReportSummary struct {
	TotalSteps                int     `json:"totalSteps"`
	PassedSteps               int     `json:"passedSteps" jsonschema:"steps whose chaos verdict was Pass"`
	WorstRecoveryMs           *int64  `json:"worstRecoveryMs,omitempty" jsonschema:"the longest timeToAllReadyMs of any step. A step without one is not counted: one that never recovered (see unrecoveredSteps), one that did not wait for recovery, and one that failed first. 0 when no step measured one"`
	UnrecoveredSteps          int     `json:"unrecoveredSteps" jsonschema:"steps with a pod the fault took down that was still not ready when Kates stopped waiting (unrecoveredAfterMs set). worstRecoveryMs leaves them out, so when this is above 0 it understates the worst recovery"`
	LongestUnrecoveredAfterMs *int64  `json:"longestUnrecoveredAfterMs,omitempty" jsonschema:"the longest unrecoveredAfterMs of those steps: a lower bound on how long they took to recover, if they did"`
	AvgThroughputDegradation  float64 `json:"avgThroughputDegradation" jsonschema:"the steps' changes in the messages-in rate (their throughputRecPerSec impact deltas) added up and divided by the number of steps, so a step Prometheus did not measure counts as 0. Each change is in percent, negative for a drop, except for a step whose rate was 0 before the fault, which adds its rate after instead. 0 when Prometheus measured nothing"`
	MaxP99LatencySpike        float64 `json:"maxP99LatencySpike" jsonschema:"the largest per-step rise in produce p99 latency (the p99LatencyMs impact delta), in percent, or the p99 after the fault for a step where it was 0 before; 0 when none rose or Prometheus measured nothing"`
	SLAViolated               bool    `json:"slaViolated"`
	WorstISRRecoveryMs        *int64  `json:"worstIsrRecoveryMs,omitempty" jsonschema:"the longest timeToFullIsrMs of any step. A step whose tracked topic's ISR was not full again while tracked is not counted (see isrUnrecoveredSteps). 0 when no topic was tracked or its ISR was never short after the fault"`
	ISRUnrecoveredSteps       int     `json:"isrUnrecoveredSteps" jsonschema:"steps whose tracker sampled the topic but did not see its ISR full again after the fault; worstIsrRecoveryMs leaves them out"`
	PeakConsumerLag           int64   `json:"peakConsumerLag" jsonschema:"the highest lag of the tracked consumer group; 0 when none was tracked or no lag was sampled"`
}

type mcpDisruptionReportSLA struct {
	Grade        string                            `json:"grade" jsonschema:"A, B, C, D or F, or - when no check could be evaluated"`
	Violated     bool                              `json:"violated"`
	TotalChecks  int                               `json:"totalChecks"`
	PassedChecks int                               `json:"passedChecks"`
	Violations   []mcpDisruptionReportSLAViolation `json:"violations"`
	Unevaluated  []mcpUntrusted                    `json:"unevaluated" jsonschema:"declared checks with nothing to compare against, left out of the grade"`
}

type mcpDisruptionReportSLAViolation struct {
	Metric     string  `json:"metric"`
	Constraint string  `json:"constraint"`
	Threshold  float64 `json:"threshold"`
	Actual     float64 `json:"actual"`
	Severity   string  `json:"severity"`
}

type mcpDisruptionReportStep struct {
	Name                   mcpUntrusted            `json:"name"`
	DisruptionType         string                  `json:"disruptionType"`
	Verdict                string                  `json:"verdict,omitempty" jsonschema:"the chaos provider's verdict: Pass, Fail, or Skipped when no fault was injected"`
	FailureReason          mcpUntrusted            `json:"failureReason,omitempty"`
	FaultStart             string                  `json:"faultStart,omitempty" jsonschema:"when the fault started, approximately"`
	FaultEnd               string                  `json:"faultEnd,omitempty" jsonschema:"when the fault ended, approximately"`
	FaultDurationMs        *int64                  `json:"faultDurationMs,omitempty"`
	TimeToFirstReadyMs     *int64                  `json:"timeToFirstReadyMs,omitempty" jsonschema:"from the fault request until the first Ready event from any watched Kafka pod"`
	TimeToAllReadyMs       *int64                  `json:"timeToAllReadyMs,omitempty" jsonschema:"from the fault request until every watched Kafka pod was ready again, after one went down"`
	UnrecoveredAfterMs     *int64                  `json:"unrecoveredAfterMs,omitempty" jsonschema:"set when a pod the fault took down was still not ready when Kates stopped waiting: a lower bound on its recovery time"`
	TargetedLeaderBrokerID *int                    `json:"targetedLeaderBrokerId,omitempty" jsonschema:"the partition leader a leader-aware step hit"`
	RolledBack             bool                    `json:"rolledBack"`
	RollbackReason         mcpUntrusted            `json:"rollbackReason,omitempty"`
	ImpactDeltas           map[string]float64      `json:"impactDeltas" jsonschema:"change of each Prometheus metric from before the fault to after, in percent, or its value after when it was 0 before; empty when Prometheus measured nothing"`
	UnmeasuredMetrics      []string                `json:"unmeasuredMetrics" jsonschema:"metrics Prometheus returned no data for after the fault; their figures are not measurements"`
	PodEvents              int                     `json:"podEvents" jsonschema:"pod events recorded during the step; the timeline resource lists them"`
	ISR                    *mcpDisruptionReportISR `json:"isr,omitempty" jsonschema:"absent when the plan tracked no topic's ISR (isrTrackingTopic), or the step failed before it measured"`
	Lag                    *mcpDisruptionReportLag `json:"lag,omitempty" jsonschema:"absent when the plan tracked no consumer group (lagTrackingGroupId), or the step failed before it measured"`
}

type mcpDisruptionReportISR struct {
	Measured                 bool   `json:"measured" jsonschema:"false when the tracker took no sample of the topic (it does not exist, or every poll failed); the figures are then absent, and the caveats say how the impact score misreads such a step"`
	TimeToFullISRMs          *int64 `json:"timeToFullIsrMs,omitempty" jsonschema:"from the fault request until the tracked topic's ISR was full again; 0 when no sample after the fault request found it short, absent when it was not full again while tracked"`
	MinISRDepth              *int   `json:"minIsrDepth,omitempty" jsonschema:"the smallest ISR of any partition in any sample, from before the fault to the end of the step"`
	UnderReplicatedPeakCount *int   `json:"underReplicatedPeakCount,omitempty" jsonschema:"the most partitions under-replicated in one sample"`
	TotalPartitions          *int   `json:"totalPartitions,omitempty"`
}

type mcpDisruptionReportLag struct {
	Measured            bool   `json:"measured" jsonschema:"false when the tracker took no sample of the group (it has no committed offsets, or every poll failed); the figures are then absent"`
	BaselineLag         *int64 `json:"baselineLag,omitempty" jsonschema:"the lag in the last sample before the fault request, or in the first sample when none came before"`
	PeakLag             *int64 `json:"peakLag,omitempty" jsonschema:"the highest lag in any sample, before the fault included"`
	LagSpike            *int64 `json:"lagSpike,omitempty" jsonschema:"peakLag minus baselineLag"`
	TimeToLagRecoveryMs *int64 `json:"timeToLagRecoveryMs,omitempty" jsonschema:"from the fault request until the lag came back within 10% of its baseline; 0 when it never rose more than 10% above the baseline after the fault; absent when it rose and had not come back within 10% while tracked"`
}

type mcpDisruptionReportImpact struct {
	mcpChaosSectionStatus
	Overall    int            `json:"overall" jsonschema:"0 to 100, weighted over the dimensions"`
	Severity   string         `json:"severity,omitempty" jsonschema:"MINIMAL, LOW, MEDIUM, HIGH or CRITICAL"`
	Dimensions map[string]int `json:"dimensions" jsonschema:"each dimension's score, 0 to 100"`
	NotScored  []string       `json:"notScored" jsonschema:"dimensions the report held nothing measured to score from: availability, latency and throughput without the report's summary (a RUNNING, REJECTED or FAILED report); latency or throughput when Prometheus measured that metric on no step; replication when no step's tracker sampled its topic's ISR; consumerLag when none sampled its group's lag. Their scores say nothing, yet overall and severity count them"`
	Factors    []mcpUntrusted `json:"factors" jsonschema:"what raised the score"`
}

type mcpDisruptionReportComparison struct {
	mcpChaosSectionStatus
	BaselineID       string                              `json:"baselineId"`
	BaselinePlanName mcpUntrusted                        `json:"baselinePlanName,omitempty" jsonschema:"the baseline's plan name; absent when the baseline's own report could not be read"`
	SamePlanName     *bool                               `json:"samePlanName,omitempty" jsonschema:"whether the baseline ran a plan of the same name. When false the deltas compare different plans. When true the steps may still differ: a plan file can change under its name, and every ad-hoc plan preview_disruption drafts without a name is mcp-adhoc-plan. Absent when the baseline's report could not be read"`
	Deltas           *mcpDisruptionReportComparisonDelta `json:"deltas,omitempty" jsonschema:"absent unless both disruptions have a summary"`
	Current          *mcpDisruptionReportComparisonSide  `json:"current,omitempty"`
	Baseline         *mcpDisruptionReportComparisonSide  `json:"baseline,omitempty"`
}

type mcpDisruptionReportComparisonDelta struct {
	RecoveryDeltaMs        int64   `json:"recoveryDeltaMs" jsonschema:"worstRecoveryMs of this disruption minus the baseline's; each leaves out its steps that never recovered, so read recoveryComparable"`
	RecoveryComparable     *bool   `json:"recoveryComparable,omitempty" jsonschema:"false when either disruption has a step that never recovered: recoveryDeltaMs then compares only the steps that did. Absent when the baseline's report could not be read"`
	ThroughputDeltaPercent float64 `json:"throughputDeltaPercent" jsonschema:"avgThroughputDegradation of this disruption minus the baseline's, in percentage points"`
	P99DeltaPercent        float64 `json:"p99DeltaPercent" jsonschema:"maxP99LatencySpike of this disruption minus the baseline's, in percentage points"`
}

type mcpDisruptionReportComparisonSide struct {
	Status      string `json:"status"`
	SLAGrade    string `json:"slaGrade"`
	PassedSteps string `json:"passedSteps" jsonschema:"passed/total"`
}

func mcpDisruptionReportTool(ctx context.Context, call *mcpCall, in mcpDisruptionReportIn) (mcpDisruptionReportOut, error) {
	var out mcpDisruptionReportOut
	if err := mcpValidateID("disruption_id", in.DisruptionID); err != nil {
		return out, err
	}
	if in.BaselineID != "" {
		if err := mcpValidateID("baseline_id", in.BaselineID); err != nil {
			return out, err
		}
	}
	id := string(in.DisruptionID)
	r, err := mcpReadDisruption(ctx, call, id, string(in.BaselineID))
	if err != nil {
		return out, err
	}

	out.ID = id
	out.PlanName = call.FenceN(r.report.PlanName, 200)
	out.Status = mcpSanitizeLine(r.report.Status, 32)
	if out.Status == "RUNNING" {
		call.Caveat(mcpCaveatDisruptionRunningStale)
	}
	out.ValidationWarnings = mcpChaosFenceAll(call, r.report.ValidationWarnings, mcpDisruptionReportMaxMessages)
	out.TimelineURI = strings.Replace(mcpTimelineURITemplate, "{id}", id, 1)
	out.Summary = mcpDisruptionReportSummaryFrom(r.report)
	out.SLA = mcpDisruptionReportSLAFrom(call, r.report.SlaVerdict)
	out.Steps = make([]mcpDisruptionReportStep, 0, len(r.report.StepReports))
	for _, s := range r.report.StepReports {
		out.Steps = append(out.Steps, mcpDisruptionReportStepFrom(call, s))
		if s.IsrMetrics != nil && !mcpISRSampled(s.IsrMetrics) {
			call.Caveat(mcpCaveatISRNotSampled)
		}
	}
	out.Steps = mcpCap(call, out.Steps, mcpDisruptionReportMaxSteps)
	out.Impact = mcpDisruptionReportImpactFrom(call, r.report, r.impact, r.impactErr)
	if in.BaselineID != "" {
		out.Comparison = mcpDisruptionReportComparisonFrom(call, string(in.BaselineID), r)
	}
	mcpFitDisruptionReport(call, &out)
	return out, nil
}

// mcpDisruptionReportReads is what disruption_report read. The report is the
// answer; the others add to it, and a failure in one of them is noted in its
// part rather than failing the call.
type mcpDisruptionReportReads struct {
	report, baseline               *client.DisruptionReportDetail
	impact                         *client.DisruptionImpact
	cmp                            *client.DisruptionComparison
	impactErr, cmpErr, baselineErr error
}

func mcpReadDisruption(ctx context.Context, call *mcpCall, id, baseline string) (mcpDisruptionReportReads, error) {
	var r mcpDisruptionReportReads
	g, gctx := errgroup.WithContext(ctx)
	call.Go(g, func() error {
		var err error
		r.report, err = call.Client().DisruptionReportDetail(gctx, id)
		return err
	})
	call.Go(g, func() error { r.impact, r.impactErr = call.Client().DisruptionImpact(gctx, id); return nil })
	if baseline != "" {
		call.Go(g, func() error { r.cmp, r.cmpErr = call.Client().DisruptionCompare(gctx, id, baseline); return nil })
		call.Go(g, func() error {
			r.baseline, r.baselineErr = call.Client().DisruptionReportDetail(gctx, baseline)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return r, err
	}
	if r.report == nil {
		return r, &mcpToolError{Code: mcpErrBackend, Message: "The Kates API returned an empty disruption report.", Retryable: true}
	}
	return r, nil
}

// mcpDisruptionReportSummaryFrom is the report's summary with what its worst
// recovery times leave out: the orchestrator takes each worst over the steps
// that measured one (DisruptionOrchestrator.java:139-140,167-176), so a step
// that never recovered adds nothing to it. The counts come from every step,
// including those the result leaves out.
func mcpDisruptionReportSummaryFrom(r *client.DisruptionReportDetail) *mcpDisruptionReportSummary {
	s := r.Summary
	if s == nil {
		return nil
	}
	out := &mcpDisruptionReportSummary{
		TotalSteps:               s.TotalSteps,
		PassedSteps:              s.PassedSteps,
		WorstRecoveryMs:          mcpChaosMillis(s.WorstRecovery),
		AvgThroughputDegradation: s.AvgThroughputDegradation,
		MaxP99LatencySpike:       s.MaxP99LatencySpike,
		SLAViolated:              s.SlaViolated,
		WorstISRRecoveryMs:       mcpChaosMillis(s.WorstIsrRecovery),
		PeakConsumerLag:          s.PeakConsumerLag,
	}
	out.UnrecoveredSteps, out.LongestUnrecoveredAfterMs = mcpUnrecoveredSteps(r)
	for _, step := range r.StepReports {
		if m := step.IsrMetrics; m != nil && mcpISRSampled(m) && !m.TimeToFullIsr.Set {
			out.ISRUnrecoveredSteps++
		}
	}
	return out
}

// mcpUnrecoveredSteps counts the steps of r with a pod still down when Kates
// stopped waiting, and the longest such wait.
func mcpUnrecoveredSteps(r *client.DisruptionReportDetail) (int, *int64) {
	n := 0
	var longest *int64
	for _, step := range r.StepReports {
		if ms := mcpChaosMillis(step.UnrecoveredAfter); ms != nil {
			n++
			if longest == nil || *ms > *longest {
				longest = ms
			}
		}
	}
	return n, longest
}

// mcpISRSampled reports whether an ISR tracker took any sample. One that took
// none reports 0 partitions (KafkaIntelligenceService.java:164-167), and every
// sample adds its partition (:141-149,173-179).
func mcpISRSampled(m *client.DisruptionIsrDetail) bool { return m.TotalPartitions > 0 }

// mcpLagSampled reports whether a lag tracker took any sample. One that took
// none reports a baseline and a peak of 0 and no recovery time
// (KafkaIntelligenceService.java:287-290); one that sampled a lag of 0
// throughout reports a recovery time of 0, since its lag never rose
// (:304-317).
func mcpLagSampled(m *client.DisruptionLagDetail) bool {
	return m.BaselineLag != 0 || m.PeakLag != 0 || m.TimeToLagRecovery.Set
}

func mcpDisruptionReportSLAFrom(call *mcpCall, v *client.DisruptionSlaVerdict) *mcpDisruptionReportSLA {
	if v == nil {
		return nil
	}
	sla := &mcpDisruptionReportSLA{
		Grade:        mcpSanitizeLine(v.Grade, 8),
		Violated:     v.Violated,
		TotalChecks:  v.TotalChecks,
		PassedChecks: v.PassedChecks,
		Violations:   make([]mcpDisruptionReportSLAViolation, 0, len(v.Violations)),
		Unevaluated:  mcpChaosFenceAll(call, v.Unevaluated, mcpDisruptionReportMaxMessages),
	}
	for _, x := range v.Violations {
		sla.Violations = append(sla.Violations, mcpDisruptionReportSLAViolation{
			Metric:     mcpSanitizeLine(x.MetricName, 64),
			Constraint: mcpSanitizeLine(x.Constraint, 64),
			Threshold:  x.Threshold,
			Actual:     x.Actual,
			Severity:   mcpSanitizeLine(x.Severity, 16),
		})
	}
	sla.Violations = mcpCap(call, sla.Violations, mcpDisruptionReportMaxMessages)
	return sla
}

func mcpDisruptionReportImpactFrom(call *mcpCall, report *client.DisruptionReportDetail, impact *client.DisruptionImpact, err error) mcpDisruptionReportImpact {
	out := mcpDisruptionReportImpact{mcpChaosSectionStatus: mcpChaosSectionOf(call, "impact", err), Dimensions: map[string]int{}, NotScored: []string{}, Factors: []mcpUntrusted{}}
	switch {
	case err != nil:
		return out
	case impact == nil:
		out.mcpChaosSectionStatus = mcpChaosSectionStatus{Available: false, ErrorCode: mcpErrBackend}
		return out
	}
	out.Overall = impact.Overall
	out.Severity = mcpSanitizeLine(impact.Severity, 16)
	for k, v := range impact.Dimensions {
		out.Dimensions[mcpSanitizeLine(k, 32)] = v
	}
	out.NotScored = mcpImpactNotScored(report)
	out.Factors = mcpChaosFenceAll(call, impact.Factors, mcpDisruptionReportMaxMessages)
	call.Caveat(mcpCaveatImpactScoreMisreads)
	return out
}

// mcpImpactNotScored names the impact dimensions the report held nothing
// measured to score from. The scorer grades availability, latency and
// throughput from the summary alone, and replication and consumer lag from
// the steps' trackers (DisruptionImpactScorer.java:30-51); the summary's
// latency and throughput figures come from the steps' p99LatencyMs and
// throughputRecPerSec deltas (DisruptionOrchestrator.java:182-187). Each such
// dimension scores 0, except replication from a tracker that took no sample
// (mcpCaveatISRNotSampled).
func mcpImpactNotScored(r *client.DisruptionReportDetail) []string {
	var p99, throughput, isr, lag bool
	for _, s := range r.StepReports {
		_, hasP99 := s.ImpactDeltas["p99LatencyMs"]
		_, hasTP := s.ImpactDeltas["throughputRecPerSec"]
		p99, throughput = p99 || hasP99, throughput || hasTP
		isr = isr || (s.IsrMetrics != nil && mcpISRSampled(s.IsrMetrics))
		lag = lag || (s.LagMetrics != nil && mcpLagSampled(s.LagMetrics))
	}
	summary := r.Summary != nil
	out := []string{}
	for _, d := range []struct {
		name   string
		scored bool
	}{
		{"availability", summary}, {"latency", summary && p99}, {"throughput", summary && throughput},
		{"replication", isr}, {"consumerLag", lag},
	} {
		if !d.scored {
			out = append(out, d.name)
		}
	}
	return out
}

func mcpDisruptionReportComparisonFrom(call *mcpCall, baseline string, r mcpDisruptionReportReads) *mcpDisruptionReportComparison {
	c := &mcpDisruptionReportComparison{mcpChaosSectionStatus: mcpChaosSectionOf(call, "comparison", r.cmpErr), BaselineID: baseline}
	switch {
	case r.cmpErr != nil:
		return c
	case r.cmp == nil:
		c.mcpChaosSectionStatus = mcpChaosSectionStatus{Available: false, ErrorCode: mcpErrBackend}
		return c
	}
	// The backend compares any two reports (DisruptionResource.java:260-332):
	// the baseline's own report says whether it ran the same plan, and
	// whether its worst recovery left steps out as this one's may.
	haveBaseline := r.baselineErr == nil && r.baseline != nil
	if r.baselineErr != nil {
		call.deps.logger.Warn("disruption_report: baseline report unavailable", "error", r.baselineErr)
	}
	if haveBaseline {
		c.BaselinePlanName = call.FenceN(r.baseline.PlanName, 200)
		same := r.baseline.PlanName == r.report.PlanName
		c.SamePlanName = &same
	}
	if d := r.cmp.Deltas; d != nil {
		c.Deltas = &mcpDisruptionReportComparisonDelta{RecoveryDeltaMs: d.RecoveryDeltaMs, ThroughputDeltaPercent: d.ThroughputDeltaPercent, P99DeltaPercent: d.P99DeltaPercent}
		if haveBaseline {
			cur, _ := mcpUnrecoveredSteps(r.report)
			base, _ := mcpUnrecoveredSteps(r.baseline)
			comparable := cur == 0 && base == 0
			c.Deltas.RecoveryComparable = &comparable
		}
	}
	c.Current, c.Baseline = mcpDisruptionComparisonSide(r.cmp.Current), mcpDisruptionComparisonSide(r.cmp.Baseline)
	return c
}

// mcpFitDisruptionReport leaves detail out of a report too large for one result. One
// id cannot be paged: messages go first, then the step prose, then steps; the
// summary, the SLA grade and the impact score always stay.
func mcpFitDisruptionReport(call *mcpCall, out *mcpDisruptionReportOut) {
	mcpChaosShrinkToFit(call, out,
		func() bool { return mcpChaosHalve(&out.Impact.Factors, 0) },
		func() bool { return out.SLA != nil && mcpChaosHalve(&out.SLA.Unevaluated, 0) },
		func() bool { return mcpChaosHalve(&out.ValidationWarnings, 0) },
		func() bool { return out.SLA != nil && mcpChaosHalve(&out.SLA.Violations, 0) },
		func() bool {
			return mcpChaosClear(out.Steps, func(s *mcpDisruptionReportStep) *mcpUntrusted { return &s.RollbackReason }) ||
				mcpChaosClear(out.Steps, func(s *mcpDisruptionReportStep) *mcpUntrusted { return &s.FailureReason })
		},
		func() bool { return mcpChaosHalve(&out.Steps, 1) },
	)
}

// mcpDisruptionReportStepFrom describes one step. It leaves out the report's
// strimziRecoveryTime: the orchestrator starts polling the Kafka resource only
// after the step's observation window and recovery wait, and returns the
// elapsed time on a timeout too (StrimziStateTracker.java:45-75,
// DisruptionOrchestrator.java:376-377), so the figure is not a recovery time.
func mcpDisruptionReportStepFrom(call *mcpCall, s client.DisruptionStepDetail) mcpDisruptionReportStep {
	step := mcpDisruptionReportStep{
		Name:                   call.FenceN(s.StepName, 200),
		DisruptionType:         mcpSanitizeLine(s.DisruptionType, 32),
		TimeToFirstReadyMs:     mcpChaosMillis(s.TimeToFirstReady),
		TimeToAllReadyMs:       mcpChaosMillis(s.TimeToAllReady),
		UnrecoveredAfterMs:     mcpChaosMillis(s.UnrecoveredAfter),
		TargetedLeaderBrokerID: s.TargetedLeaderBrokerID,
		RolledBack:             s.RolledBack,
		RollbackReason:         call.FenceN(s.RollbackReason, 300),
		ImpactDeltas:           map[string]float64{},
		UnmeasuredMetrics:      []string{},
		PodEvents:              len(s.PodTimeline),
		ISR:                    mcpDisruptionISRFrom(s.IsrMetrics),
		Lag:                    mcpDisruptionLagFrom(s.LagMetrics),
	}
	if o := s.ChaosOutcome; o != nil {
		step.Verdict = mcpSanitizeLine(o.Verdict, 16)
		step.FailureReason = call.FenceN(o.FailureReason, 300)
		step.FaultStart = mcpSanitizeLine(o.ChaosStartTime, 40)
		step.FaultEnd = mcpSanitizeLine(o.ChaosEndTime, 40)
		step.FaultDurationMs = mcpChaosMillis(o.ChaosDuration)
		if strings.EqualFold(o.Verdict, "Skipped") {
			call.Caveat(mcpCaveatChaosStepSkipped)
		}
	}
	// The backend captures nine metrics (PrometheusMetricsCapture.java:
	// 67-85); the cap only bounds a backend that sends far more.
	keys := make([]string, 0, len(s.ImpactDeltas))
	for k := range s.ImpactDeltas {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range mcpCap(call, keys, mcpDisruptionReportMaxMetrics) {
		step.ImpactDeltas[mcpSanitizeLine(k, 64)] = s.ImpactDeltas[k]
	}
	for _, m := range s.UnmeasuredMetrics {
		step.UnmeasuredMetrics = append(step.UnmeasuredMetrics, mcpSanitizeLine(m, 64))
	}
	step.UnmeasuredMetrics = mcpCap(call, step.UnmeasuredMetrics, mcpDisruptionReportMaxMetrics)
	return step
}

func mcpDisruptionISRFrom(m *client.DisruptionIsrDetail) *mcpDisruptionReportISR {
	switch {
	case m == nil:
		return nil
	case !mcpISRSampled(m):
		return &mcpDisruptionReportISR{}
	}
	return &mcpDisruptionReportISR{
		Measured:                 true,
		TimeToFullISRMs:          mcpChaosMillis(m.TimeToFullIsr),
		MinISRDepth:              &m.MinIsrDepth,
		UnderReplicatedPeakCount: &m.UnderReplicatedPeak,
		TotalPartitions:          &m.TotalPartitions,
	}
}

func mcpDisruptionLagFrom(m *client.DisruptionLagDetail) *mcpDisruptionReportLag {
	switch {
	case m == nil:
		return nil
	case !mcpLagSampled(m):
		return &mcpDisruptionReportLag{}
	}
	spike := m.PeakLag - m.BaselineLag
	return &mcpDisruptionReportLag{
		Measured:            true,
		BaselineLag:         &m.BaselineLag,
		PeakLag:             &m.PeakLag,
		LagSpike:            &spike,
		TimeToLagRecoveryMs: mcpChaosMillis(m.TimeToLagRecovery),
	}
}

func mcpDisruptionComparisonSide(s *client.DisruptionComparisonSide) *mcpDisruptionReportComparisonSide {
	if s == nil {
		return nil
	}
	return &mcpDisruptionReportComparisonSide{
		Status:      mcpSanitizeLine(s.Status, 32),
		SLAGrade:    mcpSanitizeLine(s.SlaGrade, 8),
		PassedSteps: mcpSanitizeLine(s.PassedSteps, 16),
	}
}

func mcpChaosSectionOf(call *mcpCall, what string, err error) mcpChaosSectionStatus {
	if err != nil {
		call.deps.logger.Warn("disruption_report: part unavailable", "part", what, "error", err)
		return mcpChaosSectionStatus{Available: false, ErrorCode: call.deps.classify(err).Code}
	}
	return mcpChaosSectionStatus{Available: true}
}

func mcpChaosMillis(d client.BackendDuration) *int64 {
	if !d.Set {
		return nil
	}
	ms := d.Millis
	return &ms
}
