package cmd

import (
	"fmt"
	"strings"

	"github.com/bmscomp/kates/cli/client"
)

// How kates test apply --wait grades a scenario's validate block.
const (
	// slaScopeReport grades the run as a whole, as its report does: the
	// latency of latencyOf, the throughput of the producers added up, and
	// the integrity of every row that carries some. The default.
	slaScopeReport = "report"
	// slaScopeTask grades every task row on its own, consumers included, as
	// apply did before the report scope: each row must meet each gate.
	slaScopeTask = "task"
)

// slaScopes are the values --sla-scope takes.
var slaScopes = []string{slaScopeReport, slaScopeTask}

// scenarioSLAScope is the scope a scenario's gates are graded at: task when
// its validate block sets perTask: true, whatever --sla-scope says, and the
// flag's scope otherwise.
func scenarioSLAScope(v *ValidationSpec, flagScope string) string {
	if v != nil && v.PerTask {
		return slaScopeTask
	}
	if flagScope == "" {
		return slaScopeReport
	}
	return flagScope
}

// gradeScenario grades a finished run against its scenario's validate block
// at scope. It returns nil for a scenario without one.
func gradeScenario(run *client.TestRun, v *ValidationSpec, scope string) *applySLAResult {
	if v == nil {
		return nil
	}
	violations := validateSLAs(run, v)
	if scope == slaScopeReport {
		violations = validateSLAsAggregate(run, v)
	}
	return &applySLAResult{
		Scope:        scope,
		Violations:   nonNil(violations),
		NotEvaluable: unevaluableSLAs(run, v),
	}
}

// producerRows are the rows whose throughput is the run's: the rows
// latencyRows chooses from, every row but a consumer's, or the consumers'
// when there is nothing else. A run's producers run side by side (STRESS and
// CAPACITY start one task per producer at once), so their rates add up.
func producerRows(results []client.PhaseResult) []client.PhaseResult {
	var rows []client.PhaseResult
	for _, r := range results {
		if r.PhaseName != "consume" {
			rows = append(rows, r)
		}
	}
	if len(rows) == 0 {
		return results
	}
	return rows
}

// runAggregate is what the report scope grades: one figure per gate for the
// whole run.
type runAggregate struct {
	latency    runLatency
	measured   bool
	throughput float64
	// integrity is set when a row carries integrity data.
	integrity                bool
	dataLossPercent          float64
	outOfOrder, crcFailures  int64
	rtoMs, rpoMs             float64
	rtoMeasured, rpoMeasured bool
}

// aggregateRun reads a run's rows the way its report summary does
// (MetricUtils.computeSummary): latency from the rows latencyOf reads, a
// consumer's left out. Throughput is the producers' rates added up, since
// they ran at once; the summary's mean of every row would hold sixteen
// producers to what one sends. Integrity takes the worst loss, RTO and RPO
// of the rows that carry it, and adds up their out-of-order records and CRC
// failures.
func aggregateRun(results []client.PhaseResult) runAggregate {
	a := runAggregate{latency: latencyOf(results), measured: measuredLatency(results)}
	for _, r := range producerRows(results) {
		a.throughput += r.ThroughputRecordsPerSec
	}
	for _, r := range results {
		ir := r.Integrity
		if ir == nil {
			continue
		}
		a.integrity = true
		a.dataLossPercent = max(a.dataLossPercent, ir.DataLossPercent)
		a.outOfOrder += ir.OutOfOrderCount
		a.crcFailures += ir.CrcFailures
		if rto, ok := ir.MeasuredMaxRtoMs(); ok {
			a.rtoMs, a.rtoMeasured = max(a.rtoMs, rto), true
		}
		if rpo, ok := ir.MeasuredRpoMs(); ok {
			a.rpoMs, a.rpoMeasured = max(a.rpoMs, rpo), true
		}
	}
	return a
}

// validateSLAsAggregate is validateSLAs at the report scope: each gate is
// checked once, against the run's aggregate, so a STRESS run's sixteen
// producers meet minThroughputRecPerSec together and a LOAD run's consumer is
// not held to its producer's rate. The messages read as validateSLAs's.
func validateSLAsAggregate(run *client.TestRun, v *ValidationSpec) []string {
	var violations []string
	if isFailedStatus(strings.ToUpper(run.Status)) {
		violations = append(violations, runFailure(run))
	}
	a := aggregateRun(run.Results)
	if !a.measured {
		if v.MaxP99Latency > 0 {
			violations = append(violations, "p99 not measured")
		}
		if v.MaxAvgLatency > 0 {
			violations = append(violations, "avg not measured")
		}
	} else {
		if v.MaxP99Latency > 0 && a.latency.P99Ms > v.MaxP99Latency {
			violations = append(violations, fmt.Sprintf("p99=%.0fms > %.0fms", a.latency.P99Ms, v.MaxP99Latency))
		}
		if v.MaxAvgLatency > 0 && a.latency.AvgMs > v.MaxAvgLatency {
			violations = append(violations, fmt.Sprintf("avg=%.0fms > %.0fms", a.latency.AvgMs, v.MaxAvgLatency))
		}
	}
	if v.MinThroughput > 0 && a.throughput < v.MinThroughput {
		violations = append(violations, fmt.Sprintf("throughput=%.0f < %.0f rec/s", a.throughput, v.MinThroughput))
	}
	if !a.integrity {
		return violations
	}
	if v.MaxDataLoss >= 0 && a.dataLossPercent > v.MaxDataLoss {
		violations = append(violations, fmt.Sprintf("dataLoss=%.4f%% > %.4f%%", a.dataLossPercent, v.MaxDataLoss))
	}
	if a.rtoMeasured && v.MaxRtoMs > 0 && a.rtoMs > v.MaxRtoMs {
		violations = append(violations, fmt.Sprintf("rto=%.0fms > %.0fms", a.rtoMs, v.MaxRtoMs))
	}
	if a.rpoMeasured && v.MaxRpoMs > 0 && a.rpoMs > v.MaxRpoMs {
		violations = append(violations, fmt.Sprintf("rpo=%.0fms > %.0fms", a.rpoMs, v.MaxRpoMs))
	}
	if v.MaxOutOfOrder >= 0 && a.outOfOrder > v.MaxOutOfOrder {
		violations = append(violations, fmt.Sprintf("outOfOrder=%d > %d", a.outOfOrder, v.MaxOutOfOrder))
	}
	if v.MaxCrcFailures >= 0 && a.crcFailures > v.MaxCrcFailures {
		violations = append(violations, fmt.Sprintf("crcFail=%d > %d", a.crcFailures, v.MaxCrcFailures))
	}
	return violations
}

// checkApplySLAFlags refuses, with exit 2, SLA flags that do not make sense
// together: a scope other than report or task, --no-sla beside --sla-scope,
// and either one without --wait, which grades nothing.
func checkApplySLAFlags(wait, noSLA bool, scope string) error {
	if scope != "" && scope != slaScopeReport && scope != slaScopeTask {
		return couldNotTell(fmt.Sprintf("--sla-scope %q is not one of %s", scope, strings.Join(slaScopes, ", ")))
	}
	if noSLA && scope != "" {
		return couldNotTell("--no-sla and --sla-scope do not go together: --no-sla checks no gate")
	}
	if !wait && noSLA {
		return couldNotTell("--no-sla needs --wait: without it no gate is checked anyway")
	}
	if !wait && scope != "" {
		return couldNotTell("--sla-scope needs --wait: without it no gate is checked")
	}
	return nil
}
