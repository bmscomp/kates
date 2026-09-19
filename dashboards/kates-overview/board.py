"""KATES — Overview: one release's own board, from the application chart.

  (header)   uptime, active runs, tests completed, request rate
  API        request rate by endpoint, latency, 5xx
  JVM        heap, non-heap, CPU
  Database   the Agroal pool

Folded in from `charts/kates/templates/grafana-dashboard.yaml`, which shipped
13 panels of hand-written JSON inside a Helm template. It is kept rather than
deleted because it is not a subset of `kates-application`: it is delivered by
a *different chart* — the application chart itself, installable with nothing
but a Grafana beside it — and every expression on it reads a series the
application publishes about itself. `kates-application` cannot make that
claim: half of it is kube-state-metrics and cAdvisor, and on a cluster with
neither, that board's header and its whole bottom row are blank while this one
is complete.

So: this is what the application knows about itself, and `kates-application`
is what the cluster knows about the application. Anyone running the full
`charts/monitoring` stack should use that one.

**Six names on the board this replaces did not exist.** All six were guessed
rather than read off the registry:

  agroal_pool_active_count            -> agroal_active_count
  agroal_pool_available_count         -> agroal_available_count
  agroal_pool_max_used_count          -> agroal_max_used_count
  kates_active_runs                   -> kates_benchmark_active_runs
  kates_benchmark_throughput_
      records_per_sec                 -> kates_benchmark_throughput_rec_sec
  kates_benchmark_p99_latency_ms      -> kates_benchmark_latency_ms
                                         {quantile="0.99"}

The Agroal three are Quarkus' own: `io.quarkus.agroal.runtime.metrics.`
`AgroalMetricsRecorder` registers `agroal.active.count`, with no `pool`
segment anywhere in it. The Kates three are this repository's own:
`BenchmarkMetrics` registers `kates.benchmark.active.runs` and
`kates.benchmark.throughput.rec.sec`, and there has never been a
`p99_latency_ms` meter under any spelling — the percentiles are one series
with a `quantile` label.

**Four more panels were dropped**, because the names they read are not
published by this application at all and no rename fixes them:

  process_cpu_seconds_total        Prometheus simpleclient's default exports.
  process_resident_memory_bytes    Quarkus' Micrometer registry never calls
                                   `DefaultExports.initialize()`, so neither
                                   series exists. Replaced by
                                   `process_cpu_usage` and the non-heap gauge,
                                   which Micrometer's system and JVM binders
                                   do publish.
  kafka_admin_client_*             Micrometer's Kafka client binder, which
                                   instruments clients the Kafka extension
                                   creates. This application builds its own
                                   AdminClient, and nothing binds it.
  vertx_eventbus_delivery_time_    Vert.x's own metrics options, which Quarkus
      seconds_bucket               does not enable. The HTTP row above covers
                                   the request path that actually carries this
                                   API's traffic.

The deleted template also wrote `{{ task_id }}` into two legends. No meter in
`BenchmarkMetrics` has ever carried a `task_id` tag; those legends rendered
empty.
"""

from __future__ import annotations

import panels as P
from layout import Row


# One release. `job` is the ServiceMonitor's job label, which for charts/kates
# is the Service name and therefore the release fullname — exactly what the
# deleted template baked into all 13 expressions as a literal.
APP = 'job="$job"'

ANCHOR = "process_uptime_seconds"

GREEN = [("green", None)]


def variables(variant: str) -> list[dict]:
    return [
        P.query_var(
            "job",
            "label_values(%s, job)" % ANCHOR,
            label="Release",
        ),
    ]


def _header() -> list[dict]:
    return [
        P.stat(
            "Uptime",
            "How long this release's JVM has been up, from Micrometer's "
            "uptime binder (`quarkus.micrometer.binder.system=true`). "
            "Resetting repeatedly is a restart loop; this is the first thing "
            "to check when the API is intermittently unreachable and the "
            "Deployment looks healthy.",
            P.targets("max(process_uptime_seconds{%s})" % APP),
            unit="s", thresholds=GREEN, graph_mode="none",
        ),
        P.stat(
            "Active test runs",
            "Benchmark runs the engine currently holds. Bounded by "
            "`kates.engine.max-concurrent-tests`, so sitting at that ceiling "
            "means further submissions are being rejected with a concurrency "
            "error rather than queued. The board this replaces read "
            "`kates_active_runs`, which has never been registered; the meter "
            "is `kates.benchmark.active.runs`.",
            P.targets("sum(kates_benchmark_active_runs{%s}) or vector(0)" % APP),
            unit="short",
            thresholds=[("green", None), ("yellow", 5), ("red", 10)],
        ),
        P.stat(
            "Tests completed",
            "Every benchmark run this process has taken to a terminal state "
            "since startup, whatever the outcome. A counter that never "
            "resets, so the number itself only means something relative to "
            "the uptime beside it. The pass/fail split and the per-run detail "
            "are on Kates — Trend & Regression.",
            P.targets("sum(kates_tests_completed_total{%s}) or vector(0)" % APP),
            unit="short", thresholds=[("blue", None)],
        ),
        P.stat(
            "Request rate",
            "Requests per second across every endpoint. "
            "`http_server_requests_seconds_count` is the count half of "
            "Micrometer's HTTP timer — `_seconds` is the timer's base unit "
            "and not this series' unit, which is requests. Near zero with "
            "runs active is normal: the engine polls the backends itself and "
            "nothing has to call the API for a run to proceed.",
            P.targets(
                "sum(rate(http_server_requests_seconds_count{%s}[5m])) or vector(0)" % APP),
            unit="reqps", thresholds=GREEN,
        ),
    ]


def _api() -> list[dict]:
    return [
        P.timeseries(
            "Request rate by endpoint",
            "Traffic split by method and matched URI template. Quarkus "
            "reports the *template* (`/api/runs/{id}`), not the resolved "
            "path, so the cardinality stays bounded no matter how many run "
            "ids exist — an endpoint appearing here with a literal id in it "
            "means something is serving a path the framework did not match, "
            "and that is worth fixing before it becomes a cardinality "
            "problem.",
            P.targets((
                "sum by (method, uri) (rate(http_server_requests_seconds_count{%s}[5m]))" % APP,
                "{{method}} {{uri}}")),
            unit="reqps", w=12,
        ),
        P.timeseries(
            "Request latency percentiles",
            "p50, p95 and p99 of server-side request duration in "
            "milliseconds, computed from the timer's histogram buckets. This "
            "is a real cumulative histogram with an `le` label, so "
            "`histogram_quantile()` is correct here — unlike the benchmark "
            "engine's latency series, which are pre-computed percentile "
            "gauges and must be selected by their `quantile` label instead. "
            "Summing by `le` before taking the quantile gives a percentile "
            "across the pods rather than a mean of per-pod percentiles.",
            P.targets(
                ("histogram_quantile(0.50, sum by (le) "
                 "(rate(http_server_requests_seconds_bucket{%s}[5m]))) * 1000" % APP, "p50"),
                ("histogram_quantile(0.95, sum by (le) "
                 "(rate(http_server_requests_seconds_bucket{%s}[5m]))) * 1000" % APP, "p95"),
                ("histogram_quantile(0.99, sum by (le) "
                 "(rate(http_server_requests_seconds_bucket{%s}[5m]))) * 1000" % APP, "p99"),
            ),
            unit="ms", w=12,
        ),
        P.timeseries(
            "Server errors",
            "5xx responses per second, by status. Every one of these has a "
            "stack trace in the application log. Kept apart from 4xx "
            "deliberately: a rejected test spec is a client error and is not "
            "this process failing, and mixing the two is how a board teaches "
            "people to ignore its error panel.",
            P.targets((
                'sum by (status) (rate(http_server_requests_seconds_count'
                '{%s, status=~"5.."}[5m]))' % APP,
                "{{status}}")),
            unit="reqps", w=12,
        ),
        P.timeseries(
            "Benchmark throughput",
            "Records per second reported by whatever runs are in flight, per "
            "run and test type. Empty between runs by design — these meters "
            "are tagged with `run_id` and unregistered when the run ends, "
            "because an unbounded label that is never cleaned up grows "
            "Prometheus without limit. The live detail is on Kates — "
            "Benchmark; this panel is here so one board can answer \"is it "
            "doing anything at all\".",
            P.targets((
                "kates_benchmark_throughput_rec_sec{%s}" % APP,
                "{{run_id}} ({{test_type}})")),
            unit="ops", w=12,
        ),
    ]


def _jvm() -> list[dict]:
    return [
        P.timeseries(
            "JVM memory",
            "Heap used and committed, and total non-heap — metaspace, code "
            "cache, compressed class space. Non-heap is here because the "
            "board this replaces plotted `process_resident_memory_bytes` "
            "beside the heap to catch memory the heap cannot see, and that "
            "series does not exist in a Quarkus Micrometer registry: it comes "
            "from the Prometheus Java client's default exports, which Quarkus "
            "never initialises. Non-heap is the part of that gap this "
            "application actually publishes; the rest (thread stacks, direct "
            "buffers, the Kafka clients' own allocations) needs cAdvisor and "
            "is on Kates — Application Health.",
            P.targets(
                ('sum(jvm_memory_used_bytes{%s, area="heap"})' % APP, "heap used"),
                ('sum(jvm_memory_committed_bytes{%s, area="heap"})' % APP, "heap committed"),
                ('sum(jvm_memory_used_bytes{%s, area="nonheap"})' % APP, "non-heap used"),
            ),
            unit="bytes", w=12,
        ),
        P.timeseries(
            "CPU",
            "The process' share of the available CPU and the machine's "
            "overall share, both as Micrometer's system binder reports them: "
            "ratios in [0,1], not cores and not seconds. The board this "
            "replaces read `process_cpu_seconds_total`, a Prometheus "
            "simpleclient default export that Quarkus does not register. "
            "Process CPU near 1 means this JVM is saturating what it was "
            "given; system CPU near 1 with process CPU low means it is "
            "competing with something else on the node.",
            P.targets(
                ("max(process_cpu_usage{%s})" % APP, "process"),
                ("max(system_cpu_usage{%s})" % APP, "system"),
            ),
            unit="percentunit", w=12, min_value=0, max_value=1,
        ),
    ]


def _database() -> list[dict]:
    return [
        P.timeseries(
            "Connection pool",
            "The Agroal pool: connections checked out, idle in the pool, the "
            "high-water mark, and threads blocked waiting for one. Quarkus "
            "registers these as `agroal.active.count` and friends — there is "
            "no `pool` segment in any of them, which is what the "
            "`agroal_pool_*` names on the board this replaces were reading "
            "for. `awaiting` above zero is the signal: everything else can "
            "look busy without anything being wrong.",
            P.targets(
                ("sum(agroal_active_count{%s})" % APP, "active"),
                ("sum(agroal_available_count{%s})" % APP, "available"),
                ("sum(agroal_max_used_count{%s})" % APP, "max used"),
                ("sum(agroal_awaiting_count{%s})" % APP, "awaiting"),
            ),
            unit="short", w=12,
        ),
        P.timeseries(
            "Time waiting for a connection",
            "Average and worst time a request spent blocked on the pool, in "
            "milliseconds. Quarkus registers these gauges with a "
            "`milliseconds` base unit, which Micrometer appends to the name — "
            "hence the suffix, and hence the fact that "
            "`agroal_blocking_time_total_seconds` (read by the monitoring "
            "chart's own board until this refactor) matches nothing. Rising "
            "here with `active` pinned at the ceiling is pool contention; "
            "rising with spare capacity is a slow database.",
            P.targets(
                ("max(agroal_blocking_time_average_milliseconds{%s})" % APP, "average"),
                ("max(agroal_blocking_time_max_milliseconds{%s})" % APP, "worst"),
            ),
            unit="ms", w=12,
        ),
    ]


def build(variant: str) -> list[Row]:
    return [
        Row("", _header()),
        Row("API", _api()),
        Row("JVM", _jvm()),
        Row("Database — the Agroal pool", _database()),
    ]
