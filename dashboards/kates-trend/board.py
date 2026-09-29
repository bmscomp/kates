"""Kates — Trend & Regression: is this build slower than the last one?

Read across time rather than down the page. Every panel on this board answers
a question about *runs*, plural — the single live run belongs on
`kates-benchmark`.

  Throughput trend     peak records/s and MB/s, one series per run
  Latency trend        p99 and p99.9, one series per run
  Volume               records each run actually moved
  Platform totals      completion, duration, SLA outcome, records (KatesMetrics)
  Disruptions          chaos injections and how long they took

Ported from charts/monitoring/dashboards/kates-trend-dashboard.json. Two
things changed in the move.

**Every panel carries a description**, which the board had none of.

**The `$test_type` variable moved to a series that outlives a run.** It was
`label_values(kates_benchmark_records_total, test_type)`, and at the time that
series was registered nowhere at all, so the variable resolved empty and every
panel on the board filtered on `test_type=~""`. Registering the four missing
meters fixed half of that, but only half: the `kates_benchmark_*` meters are
tagged with `run_id` and are *unregistered when the run ends*, because a
`run_id` label is unbounded over time and a registry that keeps them grows
forever. So they exist only while something is running — fine for a live
board, useless for a variable on a board whose whole point is the quiet hours
between runs. `kates_tests_completed_total` is a KatesMetrics counter tagged
with the same `test_type` and it is never removed, so the dropdown is
populated by every type this installation has ever run.

**Why the run-scoped panels still work.** A series that stops being published
does not stop existing in Prometheus: the samples it wrote are in the TSDB for
the retention period. `max by (run_id) (…)` over a six-hour window therefore
draws one bounded line per run, each spanning the minutes that run was alive.
That is the regression view — each build a separate line, at its own place on
the time axis — and it is why this board's default window is 6h and not 1h.
"""

from __future__ import annotations

import panels as P
from layout import Row


# Scoped by the scrape job so one Grafana can hold two Kates installations
# without averaging their benchmark history together.
JOB = 'job="$job"'
TYPE = 'job="$job", test_type=~"$test_type"'

# A KatesMetrics counter: platform-level, tagged by test_type, and never
# unregistered. See the module docstring for why the benchmark meters cannot
# be used here.
ANCHOR = "kates_tests_completed_total"


def variables(variant: str) -> list[dict]:
    return [
        P.query_var(
            "job",
            "label_values(%s, job)" % ANCHOR,
            label="Scrape job",
        ),
        P.query_var(
            "test_type",
            'label_values(%s{job="$job"}, test_type)' % ANCHOR,
            label="Test type",
            multi=True, include_all=True, all_value=".*",
        ),
    ]


# ── Throughput ─────────────────────────────────────────────────────────────

def _throughput() -> list[dict]:
    return [
        P.timeseries(
            "Peak throughput per run — records/s",
            "The highest records/second each run reached, one line per run. "
            "`kates_benchmark_throughput_rec_sec` is a gauge the engine "
            "rewrites on every poll of every phase, so `max by (run_id, "
            "test_type)` collapses the phases of one run into that run's "
            "best moment. Runs of the same test type should land at "
            "comparable heights; one that lands visibly lower than the runs "
            "beside it is the regression this board exists to catch. Each "
            "line stops when the run does, because the meter is unregistered "
            "with the run.",
            P.targets((
                "max by (run_id, test_type) (kates_benchmark_throughput_rec_sec{%s})" % TYPE,
                "{{run_id}} ({{test_type}})")),
            unit="ops", w=12,
        ),
        P.timeseries(
            "Peak throughput per run — MB/s",
            "The same runs measured in bytes rather than records. Read it "
            "beside the panel to its left: the two move together while the "
            "record size is constant, and they diverge when it is not. A run "
            "whose records/s fell but whose MB/s held is not slower, it moved "
            "bigger records — which is a configuration difference, not a "
            "regression, and is the most common false alarm this board "
            "produces.",
            P.targets((
                "max by (run_id, test_type) (kates_benchmark_throughput_mb_sec{%s})" % TYPE,
                "{{run_id}} ({{test_type}})")),
            unit="MBs", w=12,
        ),
    ]


# ── Latency ────────────────────────────────────────────────────────────────

def _latency() -> list[dict]:
    return [
        P.timeseries(
            "p99 latency per run",
            "The worst p99 each run recorded. These are pre-computed "
            "percentile gauges carrying a `quantile` label — the backend "
            "measures the percentiles and the engine publishes them — so the "
            "quantile is *selected*, never computed with "
            "`histogram_quantile()` and never averaged across runs. "
            "`max by (run_id)` takes the worst phase of each run, which is "
            "the number a regression shows up in first: throughput holds "
            "while the tail stretches.",
            P.targets((
                'max by (run_id) (kates_benchmark_latency_ms{%s, quantile="0.99"})' % TYPE,
                "p99 {{run_id}}")),
            unit="ms", w=12,
        ),
        P.timeseries(
            "p99.9 latency per run",
            "The same for the thousandth percentile — the one in a thousand "
            "requests that went worst. p99.9 is where a GC pause, a broker "
            "failover or a single slow partition shows up while p99 still "
            "looks fine, so a run whose p99 matches its neighbours and whose "
            "p99.9 does not has a tail problem rather than a throughput "
            "problem. A backend that does not measure this quantile "
            "contributes nothing rather than a zero, so an empty panel here "
            "with a populated one to the left means the backend, not the "
            "cluster.",
            P.targets((
                'max by (run_id) (kates_benchmark_latency_ms{%s, quantile="0.999"})' % TYPE,
                "p99.9 {{run_id}}")),
            unit="ms", w=12,
        ),
    ]


# ── Volume ─────────────────────────────────────────────────────────────────

def _volume() -> list[dict]:
    return [
        P.timeseries(
            "Records moved per run",
            "The total each run carried, summed over its phases. "
            "`kates_benchmark_records_total` is a counter, so the line climbs "
            "through the run and stops at the run's total — the height of the "
            "step is the answer. Two runs of the same test type that end at "
            "different heights did not do the same work, and comparing their "
            "throughput or latency is comparing two different tests. Check "
            "this panel before believing either of the two above it.",
            P.targets((
                "sum by (run_id, test_type) (kates_benchmark_records_total{%s})" % TYPE,
                "{{run_id}} ({{test_type}})")),
            unit="short", w=24,
        ),
    ]


# ── Platform ───────────────────────────────────────────────────────────────

def _platform() -> list[dict]:
    return [
        P.timeseries(
            "Tests completed per second, by outcome",
            "KatesMetrics' own counter, incremented once per run as it "
            "reaches a terminal state and tagged with the outcome. Unlike "
            "everything above, this series persists after the run — it is "
            "not tagged with a run id, so there is nothing unbounded to "
            "clean up. A `failed` line that is consistently non-zero is a "
            "broken scenario or a broken cluster, and the run ids behind it "
            "are on `kates-benchmark` within its retention window.",
            P.targets((
                "sum by (test_type, outcome) (rate(kates_tests_completed_total{%s}[5m]))" % TYPE,
                "{{test_type}} — {{outcome}}")),
            unit="short", w=8,
        ),
        P.timeseries(
            "Test duration percentiles",
            "How long a run takes, end to end, from Micrometer's `Timer` — "
            "so these quantiles are Micrometer's own, computed over the "
            "durations of completed runs rather than supplied by a backend. "
            "The `_seconds` is the timer's base unit. Duration climbing while "
            "throughput holds means the runs are simply being asked for more "
            "records; duration climbing while `Records moved per run` holds "
            "is the regression.",
            P.targets(
                ('kates_tests_duration_seconds{%s, quantile="0.5"}' % TYPE,
                 "p50 {{test_type}}"),
                ('kates_tests_duration_seconds{%s, quantile="0.95"}' % TYPE,
                 "p95 {{test_type}}"),
                ('kates_tests_duration_seconds{%s, quantile="0.99"}' % TYPE,
                 "p99 {{test_type}}"),
            ),
            unit="s", w=8,
        ),
        P.timeseries(
            "SLA evaluations per second, by result",
            "One increment per SLA evaluation at report time, tagged pass or "
            "fail. This is the *verdict* — the aggregated judgement the "
            "report layer reaches once a run is over — and it is a different "
            "question from `kates_benchmark_sla_violations` on "
            "`kates-benchmark`, which says which individual constraint is "
            "being breached right now. A run can breach a warning constraint "
            "mid-flight and still pass overall. A rising `fail` line with "
            "flat throughput and latency usually means the SLA definition "
            "changed, not the system.",
            P.targets((
                "sum by (test_type, result) (rate(kates_sla_evaluations_total{%s}[5m]))" % TYPE,
                "{{test_type}} — {{result}}")),
            unit="short", w=8,
        ),
        P.timeseries(
            "Records processed per second, all runs",
            "The platform-wide records counter, rated. This is the aggregate "
            "the installation has moved, not any one run's: it keeps "
            "climbing across runs and is never reset, which makes it the "
            "series to answer \"how much has this cluster actually been made "
            "to do this week\". Flat over a period the panels above show runs "
            "in means the runs produced nothing — every task failed at "
            "startup.",
            P.targets((
                "sum by (test_type) (rate(kates_records_processed_total{%s}[5m]))" % TYPE,
                "{{test_type}}")),
            unit="ops", w=24,
        ),
    ]


# ── Disruptions ────────────────────────────────────────────────────────────

def _disruptions() -> list[dict]:
    return [
        P.timeseries(
            "Disruptions completed per second, by outcome",
            "Chaos injections the engine itself ran — broker kills, network "
            "faults, the disruption types under `kates/…/disruption`. Not "
            "filtered by `$test_type`: a disruption is tagged with its own "
            "type, not with the test type it ran under. An outcome other than "
            "success means the disruption did not take, so any resilience "
            "conclusion drawn from that run is unsupported. Litmus-driven "
            "experiments are a different mechanism and appear on "
            "`kates-chaos`, not here.",
            P.targets((
                "sum by (disruption_type, outcome) "
                "(rate(kates_disruptions_completed_total{%s}[5m]))" % JOB,
                "{{disruption_type}} — {{outcome}}")),
            unit="short", w=12,
        ),
        P.timeseries(
            "Disruption duration percentiles",
            "How long each injection took, p50 and p95, from a Micrometer "
            "timer. This is the duration of the *injection*, not the "
            "recovery: it says how long the fault was being applied, not how "
            "long the cluster took to come back, which is RTO and which "
            "nothing in this repository currently exports — see "
            "`dashboards/kates-chaos/README.md`. A duration far longer than "
            "the disruption was configured for is an injection that hung, and "
            "the run under it was not testing what it claimed to.",
            P.targets(
                ('kates_disruptions_duration_seconds{%s, quantile="0.5"}' % JOB,
                 "p50 {{disruption_type}}"),
                ('kates_disruptions_duration_seconds{%s, quantile="0.95"}' % JOB,
                 "p95 {{disruption_type}}"),
            ),
            unit="s", w=12,
        ),
    ]


def build(variant: str) -> list[Row]:
    return [
        Row("Throughput trend", _throughput()),
        Row("Latency trend", _latency()),
        Row("Volume — read this before believing the two rows above", _volume()),
        Row("Platform totals — KatesMetrics, not run-scoped", _platform()),
        Row("Disruptions — the engine's own injections", _disruptions()),
    ]
