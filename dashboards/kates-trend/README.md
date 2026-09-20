# Kates — Trend & Regression

**Delivered by** `charts/monitoring` · **uid** `kates-trend-analysis` ·
**16 panels**

## The question it answers

*Is this build slower than the last one?*

Every panel here is about **runs, plural**. The single live run belongs on
`kates-benchmark`; this board is what you open after a week of nightly
benchmarks to find out whether the numbers moved, and which number moved
first.

## Who opens it

- Whoever owns the performance budget, weekly.
- Whoever is about to approve a release and wants the last N runs of each
  test type on one screen.
- Whoever is looking at one bad run on `kates-benchmark` and needs to know
  whether it is an outlier or the new normal.

## How to read it — one line per run

`max by (run_id, test_type) (…)` is the idiom on the first three rows, and it
is what makes this a trend board rather than a time-series board.

The `kates_benchmark_*` meters are tagged with `run_id` and are unregistered
when the run ends (see `dashboards/kates-benchmark/README.md` for why). A
series that stops being published does not stop existing, though: its samples
stay in the TSDB for the retention period. So over a six-hour window each run
draws **one bounded line, spanning the minutes it was alive**, and a
regression is the line that lands visibly lower — or higher, on the latency
rows — than the ones beside it.

That is why the default window is `now-6h` and not `now-1h`. A one-hour window
usually holds one run, and one run is not a trend.

## How it is scoped, and the variable that was wrong

| Variable | Query |
|---|---|
| `$job` | `label_values(kates_tests_completed_total, job)` |
| `$test_type` | `label_values(kates_tests_completed_total{job="$job"}, test_type)` |

`$test_type` used to be `label_values(kates_benchmark_records_total,
test_type)`, and at the time that series was registered nowhere at all — so
the variable resolved empty and every panel on the board filtered on
`test_type=~""`. Phase 6 registered the meter, which fixed half of it.

Only half, because the fix is not enough on its own: `kates_benchmark_*` is
run-scoped and unregistered at the end of a run, so it exists only while
something is running. That is fine for a variable on a live board. It is
useless for a variable on a board whose entire purpose is the quiet hours
between runs — the dropdown would empty out the moment the cluster went idle.

`kates_tests_completed_total` is a KatesMetrics counter carrying the same
`test_type` label, incremented once per run as it reaches a terminal state and
never removed. The dropdown is now populated by every test type this
installation has ever run.

## Where each metric comes from

Everything is published by **the Kates application**, through Micrometer, and
scraped by `charts/kates`'s ServiceMonitor. No kube-state-metrics, no cAdvisor,
no exporter — this board works anywhere the application is scraped.

There are two families on it, and telling them apart is most of knowing how to
read it:

| Family | Registered in | Tagged with | Lifetime |
|---|---|---|---|
| `kates_benchmark_*` | `BenchmarkMetrics` | `run_id`, `test_type`, `phase` | **the run** — unregistered at the end of it |
| `kates_tests_*`, `kates_sla_*`, `kates_records_*`, `kates_disruptions_*` | `KatesMetrics` | `test_type` or `disruption_type` | **the process** — never removed |

The first family is the top three rows: per-run measurement, one line per run.
The second is the bottom two: platform totals that keep accumulating, where
`rate()` is the natural reading.

## The sections

### Throughput trend

`Peak throughput per run — records/s` · `— MB/s`

`max by (run_id, test_type)` collapses the phases of a run into that run's
best moment. Runs of the same test type should land at comparable heights.

Read the two panels together: a run whose records/s fell but whose MB/s held
is not slower, it moved bigger records. That is a configuration difference,
not a regression, and it is the most common false alarm this board produces.

### Latency trend

`p99 latency per run` · `p99.9 latency per run`

The tail stretches before the throughput falls, so this is usually where a
regression shows up first. A run whose p99 matches its neighbours and whose
p99.9 does not has a tail problem, not a throughput problem.

These are **pre-computed percentile gauges with a `quantile` label** — the
backend measures the percentiles and the engine publishes them. Select the
quantile; never `histogram_quantile()`. A backend that does not measure a
quantile contributes nothing rather than a zero, so an empty p99.9 panel
beside a populated p99 panel is a statement about the backend, not the
cluster.

### Volume

`Records moved per run`

**Read this before believing the two rows above it.** Two runs of the same
test type that end at different heights did not do the same work, and
comparing their throughput or latency is comparing two different tests. The
counter climbs through each run and stops at its total; the height of the step
is the answer.

### Platform totals — KatesMetrics, not run-scoped

`Tests completed per second, by outcome` · `Test duration percentiles` ·
`SLA evaluations per second, by result` · `Records processed per second, all
runs`

`kates_tests_duration_seconds` is a Micrometer `Timer`, so *these* quantiles
are Micrometer's own, computed over the durations of completed runs. Duration
climbing while throughput holds means the runs are being asked for more
records; duration climbing while `Records moved per run` holds is the
regression.

`kates_sla_evaluations_total` is the **verdict** — the aggregated judgement
the report layer reaches once a run is over. That is a different question from
`kates_benchmark_sla_violations` on `kates-benchmark`, which says which
individual constraint is breaching right now. A run can breach a warning
constraint mid-flight and still pass overall. A rising `fail` line with flat
throughput and latency usually means the SLA definition changed, not the
system.

### Disruptions — the engine's own injections

`Disruptions completed per second, by outcome` · `Disruption duration
percentiles`

Faults the Kates engine injected itself (`kates/…/disruption`), as opposed to
Litmus experiments, which are on `kates-chaos`. Not filtered by `$test_type`:
a disruption is tagged with its own type, not with the test type it ran under.

An outcome other than success means the disruption did not take, so any
resilience conclusion drawn from that run is unsupported. And the duration is
the duration of the **injection**, not of the recovery — recovery time is RTO,
and nothing in this repository currently exports it. See
`dashboards/kates-chaos/README.md`.

## Related boards

- `kates-benchmark` — the live run, in detail.
- `kates-application` — the process; check it when a regression has no
  explanation on this board, because a heap that started swapping or a
  connection pool that started blocking is a harness problem, not a cluster
  one.
- `kates-chaos` — runs observed while a fault is injected.
