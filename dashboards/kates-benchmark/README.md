# Kates — Benchmark (live)

**Delivered by** `charts/monitoring` · **uid** `kates-benchmark-overview` ·
**17 panels**

## The question it answers

*What is this run doing, right now?*

One benchmark run, while it is in flight: how fast it is moving records, what
the latency distribution looks like, which phase is ahead of which, whether
anything has failed, and which SLA constraint is breaking.

## Who opens it

The person who just submitted a run and is watching it, and the person
answering "is it still going?" during a GameDay. Both arrive knowing the run
id, which is why `$run_id` is the first dropdown.

Between runs this board is **legitimately empty**. That is not a fault, and
the section below explains why. The history lives on `kates-trend`.

## It used to be entirely non-functional

This is the board the refactor plan singled out, so it is worth stating
plainly what was wrong with it.

Both of its template variables were
`label_values(kates_benchmark_records_total, …)`. That series was **registered
nowhere in the application**. So both variables resolved empty, every panel
filtered on `{run_id=~"", test_type=~""}`, and the board rendered *No data*
against a cluster running flat out. Not thirty percent broken — completely
broken, in a way that looked exactly like an idle system.

Four of the eight names it read were in that state:

| Name | Read by | Registered by |
|---|---|---|
| `kates_benchmark_records_total` | this board, `kates-trend` | nothing |
| `kates_benchmark_latency_ms` | this board, `kates-trend`, `kates-chaos` | nothing |
| `kates_benchmark_latency_ms_max` | this board | nothing |
| `kates_benchmark_sla_violations` | this board | nothing |

All four were also documented in `docs/book/09-observability.md`, so the
documentation and the dashboards agreed with each other and both disagreed
with the code.

Phase 6 added the four meters to
`kates/src/main/java/com/bmscomp/kates/engine/BenchmarkMetrics.java` rather
than deleting the panels — this board exists to show exactly these, and
without them it has no reason to exist — and wired them to values the engine
already had. `TestOrchestrator.publishLiveMetrics` feeds records and latency
from the same poll it feeds throughput from, and the SLA violations come from
the same `SlaEvaluator` the report path uses.

While doing that, one more thing came out: `kates_benchmark_throughput_rec_sec`
*was* registered, but it was only ever written in the instant between a run
reaching its terminal state and its meters being unregistered. In practice it
was never scraped with a value at all. It is now fed from every poll.

## Everything here is transient, on purpose

Every `kates_benchmark_*` meter is tagged with `run_id`. Run ids are unbounded
over time, so `BenchmarkMetrics.endRun` **unregisters** the meters when the run
finishes — otherwise every run would permanently add time series and
Prometheus' memory would grow without bound. The number of run ids alive at
once is bounded by `kates.engine.max-concurrent-tests`.

Two consequences:

1. **This board is empty between runs.** By design.
2. **`kates-trend` still works.** A series that stops being published does not
   stop existing: its samples are in the TSDB for the retention period. The
   trend board reads them over a six-hour window and draws one bounded line per
   run.

The one variable that must *not* depend on a transient series is a variable
on a board meant to be useful when nothing is running — which is why
`kates-trend`'s `$test_type` was moved to `kates_tests_completed_total`, and
why this board's `$job` comes from `kates_benchmark_active_runs`, a
process-wide gauge registered at startup. `$run_id` and `$test_type` here
*do* read the transient series, and that is correct: a board showing a live
run wants a dropdown of live runs. Grafana passes the dashboard's time range
to the label-values API, so the 30-minute default window keeps a run that has
just finished in the list long enough to read its result.

## Where each metric comes from

Everything on this board is published by **the Kates application itself**,
through Micrometer, and scraped by `charts/kates`'s ServiceMonitor. No
kube-state-metrics, no cAdvisor, no exporter.

| Series | Meter | Updated from |
|---|---|---|
| `kates_benchmark_active_runs` | gauge, registered at startup | `startRun` / `endRun` |
| `kates_benchmark_records_total` | function counter per phase | every poll, plus the task's final total |
| `kates_benchmark_throughput_rec_sec`, `…_mb_sec` | gauges per run | every poll |
| `kates_benchmark_latency_ms{quantile}` | gauges per phase and quantile | every poll |
| `kates_benchmark_latency_ms_max` | gauge per phase | every poll |
| `kates_benchmark_errors_total` | counter per phase | every poll: registered at zero, +1 the first time a task polls FAILED |
| `kates_benchmark_sla_violations` | gauge per constraint and severity | every poll, via `SlaEvaluator` |

## Two things about the shapes

**`kates_benchmark_records_total` is a real counter and is monotonic by
construction.** Each phase's counter reads the sum of the latest cumulative
total reported by each of that phase's tasks, and each task's total is clamped
so it can only move up. A backend that restarts a task and re-counts from zero
therefore holds the counter flat rather than resetting it — a counter reset
makes `rate()` invent a spike that never happened.

**The latency series are pre-computed percentile gauges, not a distribution.**
They publish under exactly the names a Micrometer `DistributionSummary` would
use — `kates_benchmark_latency_ms{quantile="…"}` and
`kates_benchmark_latency_ms_max` — but the values are the *backend's* own
percentiles. The engine never sees individual latencies, only the aggregates a
poll returns, so feeding those into a summary would publish percentiles of
averages under a name claiming to be a latency distribution. This is the same
shape the Kafka JMX exporter uses, and the rule that goes with it is the same:
**select the quantile; never `histogram_quantile()`, and never average two
quantiles together.** A phase with several tasks reports its worst task's
value for each quantile, recomputed on every poll, so the line falls again
when the slow task recovers.

## The sections

### Header — four stats

`Active runs` · `Records moved` · `Errors` · `SLA constraints breaching`

`Active runs` is the only one still populated between runs. `Errors` counts
failed *tasks*, not failed records: a producer that dropped ten thousand
messages and then completed contributes nothing. Non-zero means part of the
run did not run, and every other number on the board is an average over a run
that was partly not running.

### Throughput

`records/s` and `MB/s` side by side. Records/s holding while MB/s falls is a
run that switched to smaller records, not a slower pipeline. Only when both
fall together has anything actually slowed down.

### Latency

`Latency percentiles` · `Worst observed latency`

The gap between `_max` and p99.9 is the reading: a max far above p99.9 is one
stall (a GC pause, a leader election), while a max close to p99.9 means the
whole tail moved. `_max` is cumulative within the run, so a step upward is one
bad observation, not a trend.

### Phase detail

`Record rate by phase` · `p99 latency by phase` · `Records by phase`

Phases do different work; a produce phase and a consume phase have no reason
to share a latency profile, and reading them on one axis is how a slow
consumer gets blamed on the producer. `Records by phase` is the panel that
says whether the phases are balanced — a produce phase far ahead of its
consume phase is a backlog building.

`Record rate by phase` is the counter-derived rate; the Throughput row is the
backend's own gauge. They should agree, and a persistent disagreement means
the backend's window and this 30-second one are measuring different things.

### SLA and errors

`Error rate` · `SLA constraints breaching, by constraint`

A constraint reads 1 while violated and 0 once it recovers. The zero matters:
a series that simply stops publishing leaves a gap that looks identical to a
constraint that was never evaluated. A constraint never violated in this run
is never registered at all, which is why the header stat's zero fallback is
load-bearing. It is **anchored** on `kates_benchmark_active_runs`, a gauge
registered at process startup, so "no constraint is breaching" and "nobody is
scraping the application" are not the same tile — see [the zero-fallback
convention](../README.md#zeros-that-mean-measured-and-zeros-that-mean-absent).

`severity` comes from the SLA definition — CRITICAL fails the run, WARNING is
recorded and does not — so a live breach here and a pass in the final report
are not a contradiction. They are a constraint that recovered.

## Related boards

- `kates-trend` — the same runs, compared, and the platform counters that
  outlive them. `kates_sla_evaluations_total` there is the *verdict*; the SLA
  panels here are which individual constraint is breaking right now.
- `kates-application` — the Quarkus process running the engine.
- `kates-chaos` — this run's throughput, latency and errors laid over a Litmus
  experiment timeline.
