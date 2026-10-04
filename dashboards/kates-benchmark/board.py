"""Kates — Benchmark (live): one run, while it is running.

  (header)   active runs, records, errors, SLA violations
  Throughput records/s and MB/s
  Latency    the percentile set and the worst single observation
  Phases     the same numbers split by phase
  SLA        which constraint is breaking, and the error rate under it

Ported from charts/monitoring/dashboards/kates-benchmark-dashboard.json, which
was **entirely non-functional**. Both of its template variables were
`label_values(kates_benchmark_records_total, …)`, and that series was
registered nowhere in the application — so both variables resolved empty,
every panel filtered on `{run_id=~"", test_type=~""}`, and the board rendered
*No data* on a cluster running flat out. Four of the eight names it read were
in the same state: `kates_benchmark_records_total`,
`kates_benchmark_latency_ms`, `kates_benchmark_latency_ms_max` and
`kates_benchmark_sla_violations` were read here, read by `kates-trend`, and
documented in `docs/book/09-observability.md`, while being registered by
nothing. The documentation and the dashboards agreed with each other and both
disagreed with the code.

Phase 6 of the refactor added the four meters to
`kates/…/engine/BenchmarkMetrics.java` rather than deleting the panels, and
wired them to the values the engine already had: `TestOrchestrator` feeds
records, latency and SLA violations from the same poll it feeds throughput
from. The expressions on this board are unchanged from the file it replaces,
except where the description says otherwise.

**Everything here is run-scoped and therefore transient.** The meters are
tagged with `run_id`, which is unbounded over time, so they are unregistered
when the run ends — otherwise every run would permanently add time series.
That is what makes this the live board: between runs it is legitimately empty,
and the history lives on `kates-trend`, whose panels read the samples these
meters already wrote.
"""

from __future__ import annotations

import panels as P
from layout import Row


SEL = 'job="$job", run_id=~"$run_id", test_type=~"$test_type"'
# The SLA gauges are not phase-scoped: a constraint belongs to the run.
RUN = 'job="$job", run_id=~"$run_id"'

# Registered in BenchmarkMetrics' constructor, so it exists from application
# startup whether or not anything is running. That makes it the only safe
# anchor for the job variable on a board whose other series come and go.
ANCHOR = "kates_benchmark_active_runs"


def zero_if_live(expr: str) -> str:
    """A zero fallback that a dead scrape cannot reach. See P.or_zero.

    Anchored on `job` alone. The anchor gauge is registered per process, not
    per run, so it carries no `run_id` or `test_type` — selecting it with this
    board's full SEL would make it vanish exactly when it is needed.
    """
    return P.or_zero(expr, '%s{job="$job"}' % ANCHOR)


GREEN = [("green", None)]
GREEN_RED_1 = [("green", None), ("red", 1)]


def variables(variant: str) -> list[dict]:
    return [
        P.query_var(
            "job",
            "label_values(%s, job)" % ANCHOR,
            label="Scrape job",
        ),
        # These two DO read a run-scoped series, and that is correct here:
        # the board shows a live run, so a dropdown of live runs is what it
        # wants. Grafana passes the dashboard's time range to the label-values
        # API, so the 30-minute default window keeps a run that has just
        # finished in the list long enough to read its result.
        P.query_var(
            "run_id",
            'label_values(kates_benchmark_records_total{job="$job"}, run_id)',
            label="Run",
            include_all=True, all_value=".*",
        ),
        P.query_var(
            "test_type",
            'label_values(kates_benchmark_records_total{job="$job"}, test_type)',
            label="Test type",
            multi=True, include_all=True, all_value=".*",
        ),
    ]


# ── Header ─────────────────────────────────────────────────────────────────

def _header() -> list[dict]:
    return [
        P.stat(
            "Active runs",
            "How many benchmark runs the engine currently holds. A process-"
            "wide gauge registered at startup and incremented per run, not "
            "tagged with a run id, so it is the one number on this board that "
            "is still there between runs. It is bounded by "
            "`kates.engine.max-concurrent-tests`; sitting at that ceiling "
            "means new submissions are being rejected. Sitting above zero "
            "with every other panel empty means a run is registered but its "
            "tasks are producing nothing — look at the Errors panel and at "
            "the application's logs.",
            P.targets("sum(%s{job=\"$job\"}) or vector(0)" % ANCHOR),
            unit="short", thresholds=GREEN,
        ),
        P.stat(
            "Records moved",
            "Total records the selected run has carried, summed over its "
            "phases. A counter: it climbs through the run and stops at the "
            "final total. Each phase's series is the sum of the latest "
            "cumulative total reported by each of that phase's tasks, clamped "
            "so it cannot fall — a task that restarts and re-counts from zero "
            "holds the counter flat rather than resetting it, because a "
            "counter reset makes `rate()` invent a spike that never happened. "
            "The zero fallback is anchored to the process' own gauge: a run "
            "that has not moved a record yet reads 0, and an application "
            "nobody is scraping reads No data. Those are the same picture "
            "otherwise, and on the tile the eye lands on first.",
            P.targets(zero_if_live(
                "sum(kates_benchmark_records_total{%s})" % SEL)),
            unit="short", thresholds=GREEN,
        ),
        P.stat(
            "Errors",
            "Task-level failures recorded for this run, across all phases. "
            "The engine increments this once per task that reaches a FAILED "
            "state, so it counts failed *tasks*, not failed records — a "
            "producer that dropped ten thousand messages and then completed "
            "contributes nothing here. Non-zero means part of the run did not "
            "do what it was asked, and every number elsewhere on this board "
            "is an average over a run that was partly not running. The zero "
            "fallback is anchored to the process' own gauge, so an "
            "unscrapeable application reads No data rather than 'no errors'.",
            P.targets(zero_if_live(
                "sum(kates_benchmark_errors_total{%s})" % SEL)),
            unit="short", thresholds=GREEN_RED_1,
        ),
        P.stat(
            "SLA constraints breaching",
            "How many of this run's SLA constraints are violated *right now*. "
            "One gauge per (constraint, severity), valued 1 while breaching "
            "and 0 once it recovers, re-evaluated on every poll against the "
            "same `SlaEvaluator` the report path uses — so a live breach here "
            "and a pass in the final report are not a contradiction, they are "
            "a constraint that recovered. A constraint that has never been "
            "violated in this run is never registered, which is why the zero "
            "fallback is load-bearing: with no violations there is no series "
            "at all. It is anchored on `kates_benchmark_active_runs` so that "
            "it can only fire while the application is actually being "
            "scraped — 'no constraint is breaching' and 'nobody is watching' "
            "are not the same tile.",
            P.targets(zero_if_live(
                "sum(kates_benchmark_sla_violations{%s})" % RUN)),
            unit="short", thresholds=GREEN_RED_1,
        ),
    ]


# ── Throughput ─────────────────────────────────────────────────────────────

def _throughput() -> list[dict]:
    return [
        P.timeseries(
            "Throughput — records/s",
            "What the backend reported on its last poll, per run and test "
            "type. A gauge the engine overwrites each poll, not a rate "
            "computed from a counter, so it is the backend's own measurement "
            "over its own window and does not need `rate()`. Until phase 6 "
            "this was only ever written once, in the instant between a run "
            "reaching its terminal state and its meters being unregistered — "
            "so in practice it was never scraped with a value at all. "
            "`TestOrchestrator` now feeds it from every poll.",
            P.targets((
                "kates_benchmark_throughput_rec_sec{%s}" % SEL,
                "{{run_id}} — {{test_type}}")),
            unit="ops", w=12,
        ),
        P.timeseries(
            "Throughput — MB/s",
            "The same measurement in bytes. Read the two together: records/s "
            "holding while MB/s falls is a run that switched to smaller "
            "records, and MB/s holding while records/s falls is the reverse. "
            "Only when both fall together has the pipeline actually slowed "
            "down. If this pins flat while records/s moves, the backend is "
            "not reporting a byte count and the number is its default, not a "
            "measurement.",
            P.targets((
                "kates_benchmark_throughput_mb_sec{%s}" % SEL,
                "{{run_id}} — {{test_type}}")),
            unit="MBs", w=12,
        ),
    ]


# ── Latency ────────────────────────────────────────────────────────────────

def _latency() -> list[dict]:
    return [
        P.timeseries(
            "Latency percentiles",
            "p50, p95, p99 and p99.9 for each phase of the run. These are "
            "**pre-computed percentile gauges with a `quantile` label**, "
            "published under the names a Micrometer summary would use but "
            "carrying the backend's own percentiles — the engine never sees "
            "individual latencies, only the aggregates a poll returns, so a "
            "real summary here would be publishing percentiles of averages "
            "under a name that claims to be a distribution. Select the "
            "quantile; never `histogram_quantile()`, and never average two of "
            "these together. A phase with several tasks reports its worst "
            "task's value for each quantile, recomputed per poll, so the line "
            "falls again when the slow task recovers.",
            P.targets(
                ('kates_benchmark_latency_ms{%s, quantile="0.5"}' % SEL,
                 "p50 {{phase}} {{run_id}}"),
                ('kates_benchmark_latency_ms{%s, quantile="0.95"}' % SEL,
                 "p95 {{phase}} {{run_id}}"),
                ('kates_benchmark_latency_ms{%s, quantile="0.99"}' % SEL,
                 "p99 {{phase}} {{run_id}}"),
                ('kates_benchmark_latency_ms{%s, quantile="0.999"}' % SEL,
                 "p99.9 {{phase}} {{run_id}}"),
            ),
            unit="ms", w=12,
        ),
        P.timeseries(
            "Worst observed latency",
            "The single slowest observation the backend has seen in each "
            "phase — `kates_benchmark_latency_ms_max`, a separate gauge and "
            "not a member of the percentile family above it, which is why it "
            "has no `quantile` label. The backend reports it cumulatively, so "
            "it is a high-water mark within the run and a step upward here is "
            "one bad request, not a trend. It is the gap between this and "
            "p99.9 that matters: a max far above p99.9 is a single stall "
            "(a GC pause, a leader election), while a max close to p99.9 "
            "means the whole tail moved.",
            P.targets((
                "kates_benchmark_latency_ms_max{%s}" % SEL,
                "max {{phase}} {{run_id}}")),
            unit="ms", w=12,
        ),
    ]


# ── Phases ─────────────────────────────────────────────────────────────────

def _phases() -> list[dict]:
    return [
        P.timeseries(
            "Record rate by phase",
            "The derivative of the records counter, per phase — how fast each "
            "phase is moving right now. This is the counter-derived rate, and "
            "the throughput gauge two rows up is the backend's own; they "
            "should agree, and a persistent disagreement means the backend's "
            "window and this 30-second one are measuring different things. A "
            "phase whose line is flat at zero while the run is RUNNING has "
            "not started or has stalled.",
            P.targets((
                "sum by (phase) (rate(kates_benchmark_records_total{%s}[30s]))" % SEL,
                "{{phase}}")),
            unit="ops", w=8,
        ),
        P.timeseries(
            "p99 latency by phase",
            "The same p99 series as the Latency row, grouped so each phase is "
            "one line. Worth its own panel because phases do different work: "
            "a produce phase and a consume phase have no reason to share a "
            "latency profile, and reading them on one axis is how a slow "
            "consumer gets blamed on the producer. `max by (phase)` collapses "
            "the phase's tasks to its worst.",
            P.targets((
                'max by (phase) (kates_benchmark_latency_ms{%s, quantile="0.99"})' % SEL,
                "p99 {{phase}}")),
            unit="ms", w=8,
        ),
        P.timeseries(
            "Records by phase",
            "Cumulative records per phase — the same counter as the header "
            "stat, split. This is the panel that says whether the phases are "
            "balanced: a produce phase far ahead of its consume phase is a "
            "backlog building, and one that never leaves zero is a phase "
            "whose tasks never started. Compare the final heights rather than "
            "the slopes; the slopes are the panel to the left.",
            P.targets((
                "sum by (phase) (kates_benchmark_records_total{%s})" % SEL,
                "{{phase}}")),
            unit="short", w=8,
        ),
    ]


# ── SLA and errors ─────────────────────────────────────────────────────────

def _sla() -> list[dict]:
    return [
        P.timeseries(
            "Error rate",
            "Task failures per second, by phase. The counter behind it moves "
            "once per failed task, so on a healthy run this is flat zero and "
            "any departure from zero is worth stopping for — it is not a "
            "per-record error rate and there is no acceptable background "
            "level. A step here with the record rate unchanged is a task "
            "dying while its siblings carry on, which is the failure that "
            "leaves the run's aggregate numbers looking almost normal.",
            P.targets((
                "sum by (phase) (rate(kates_benchmark_errors_total{%s}[1m]))" % SEL,
                "{{phase}}")),
            unit="ops", w=12,
        ),
        P.timeseries(
            "SLA constraints breaching, by constraint",
            "Which constraint is breaking, and how badly the run is judged "
            "for it. One line per (constraint, severity) at 1 while violated "
            "and 0 once it recovers — the zero matters, because a series that "
            "simply stops publishing leaves a gap that looks identical to a "
            "constraint that was never evaluated. `severity` comes from the "
            "SLA definition: CRITICAL constraints fail the run, WARNING ones "
            "are recorded and do not. Cross-check the offending metric "
            "against its panel above before concluding the threshold is "
            "wrong; a p99 constraint breaching while the p99 panel looks calm "
            "usually means one phase, not the run.",
            P.targets((
                "sum by (metric, severity) (kates_benchmark_sla_violations{%s})" % RUN,
                "{{metric}} ({{severity}})")),
            unit="short", w=12, max_value=1, decimals=0,
        ),
    ]


def build(variant: str) -> list[Row]:
    return [
        Row("", _header()),
        Row("Throughput", _throughput()),
        Row("Latency — selected quantiles, never histogram_quantile", _latency()),
        Row("Phase detail", _phases()),
        Row("SLA and errors", _sla()),
    ]
