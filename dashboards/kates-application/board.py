"""Kates — Application Health: the Quarkus process behind the benchmark API.

Read top to bottom, in the order "is the API misbehaving?" is actually asked:

  (header)        pods ready, restarts, uptime, database pods
  HTTP            request rate, errors, latency
  JVM             heap, GC pause, threads
  Database        the Agroal connection pool
  Container       CPU and memory as the kubelet sees them

Ported from charts/monitoring/dashboards/kates-application-dashboard.json.
Three things changed in the move, all of them corrections.

**Every panel carries a description.** The board had none, which is the one
thing `scripts/check-dashboards.py` will not allow.

**The hard-coded selectors became variables.** The file read
`namespace="kates"`, `job="kates"`, `pod=~"kates-.*"` and `container="kates"`,
so it worked for exactly one release installed under exactly one name. The
namespace, job and pod list are now derived from the application's own
`process_uptime_seconds` series, which means the pod list is the set of pods
Prometheus is actually scraping rather than the set whose names happen to
start with `kates-`.

**Two Agroal names were wrong.** `agroal_blocking_time_total_seconds` and
`agroal_blocking_time_count` do not exist and never did. Quarkus registers the
pool's timing gauges with `baseUnit("milliseconds")`, so Micrometer publishes
`agroal_blocking_time_total_milliseconds`, `…_average_milliseconds` and
`…_max_milliseconds`, and there is no `_count` companion at all — the count of
acquisitions is a separate gauge, `agroal_acquire_count`. The panel that
divided the one by the other rendered empty over an empty. See
io.quarkus.agroal.runtime.metrics.AgroalMetricsRecorder for the full list.

**The `jvm_*` names here are Micrometer's, not the JMX agent's.** This board
reads a Quarkus application, not a Kafka broker. Micrometer's JVM binder
publishes `jvm_memory_used_bytes{area="heap"}`, `jvm_gc_pause_seconds` and
`jvm_threads_live_threads`; the Strimzi/Prometheus JMX agent that the broker
boards read publishes `jvm_memory_bytes_used{area="heap"}`,
`jvm_gc_collection_seconds` and `jvm_threads_current` for the same quantities.
Neither set is a fallback for the other, and copying an expression from a
broker board onto this one produces a panel that is empty for a reason no one
will find quickly.
"""

from __future__ import annotations

import panels as P
from layout import Row


# The application's own series, scoped by namespace and scrape job. `job` is
# the ServiceMonitor's, which for charts/kates is the Service name, so one
# Grafana can hold several Kates installs and the dropdown separates them.
APP = 'namespace="$namespace", job="$job"'

# kube-state-metrics and cAdvisor series for the same pods. These carry no
# `job` label of the application's, so they are selected by the pod list the
# `$pod` variable derived from the application's own series.
POD = 'namespace="$namespace", pod=~"$pod"'

# A series every Kates process publishes from startup, whether or not a
# benchmark has ever run — Micrometer's uptime binder, enabled by
# `quarkus.micrometer.binder.system=true`. The benchmark meters would not do:
# they exist only while a run is in flight, so the variables would empty out
# the moment the cluster went quiet.
ANCHOR = "process_uptime_seconds"

GREEN = [("green", None)]
# Zero of something that must exist is the outage, not the resting state.
RED_AT_ZERO = [("red", None), ("green", 1)]
GREEN_AMBER_RED = [("green", None), ("yellow", 1), ("red", 5)]


def variables(variant: str) -> list[dict]:
    return [
        P.query_var(
            "namespace",
            "label_values(%s, namespace)" % ANCHOR,
            label="Namespace",
        ),
        P.query_var(
            "job",
            'label_values(%s{namespace="$namespace"}, job)' % ANCHOR,
            label="Scrape job",
        ),
        P.query_var(
            "pod",
            'label_values(%s{namespace="$namespace", job="$job"}, pod)' % ANCHOR,
            label="Pod",
            multi=True, include_all=True, all_value=".*",
        ),
        # The application cannot tell Prometheus which pod its database runs
        # in — kube-state-metrics has no edge from a Deployment to the
        # StatefulSet it connects to. charts/kates names the pod
        # `<release>-postgresql-*`, so the dropdown is seeded by matching that
        # and the operator picks. An external database leaves it empty, which
        # is the honest answer for a database Kubernetes cannot see.
        P.query_var(
            "db_pod",
            'label_values(kube_pod_status_ready{namespace="$namespace", '
            'pod=~".*postgres.*"}, pod)',
            label="Database pod",
            multi=True, include_all=True, all_value=".*",
        ),
    ]


# ── Header ─────────────────────────────────────────────────────────────────

def _header() -> list[dict]:
    return [
        P.stat(
            "Pods ready",
            "How many Kates pods kube-state-metrics reports with the `Ready` "
            "condition true. This is the *Kubernetes* view: a pod is Ready "
            "when its readiness probe passes, which for this application "
            "means `/q/health/ready` answered. Below the Deployment's replica "
            "count means a rollout is in progress or a pod is failing its "
            "probe. Needs kube-state-metrics; without it this panel is the "
            "only one in the header that reads *No data* while the "
            "application is perfectly healthy. Red at zero: no pod Ready is "
            "the outage, not the resting state.",
            P.targets(
                'sum(kube_pod_status_ready{%s, condition="true"}) or vector(0)' % POD),
            unit="short", thresholds=RED_AT_ZERO,
        ),
        P.stat(
            "Container restarts (5m)",
            "Containers in these pods that restarted in the last five "
            "minutes, summed. Any non-zero value is worth reading the logs "
            "over: this application has no crash-and-retry design, so a "
            "restart is an OOM kill, a liveness probe that timed out, or a "
            "node event. `increase()` over a counter that resets when the pod "
            "is replaced undercounts across a rollout, which is the correct "
            "direction to be wrong in — it will not invent restarts that did "
            "not happen. kube-state-metrics. The zero fallback is anchored to "
            "kube-state's own readiness series for the same pods, not to "
            "`process_uptime_seconds`: restarts are measured from outside the "
            "JVM and must still read while the JVM is down. What it rules out "
            "is the other case — kube-state absent, or `$pod` matching "
            "nothing, drawing a green zero.",
            P.targets(P.or_zero(
                "sum(increase(kube_pod_container_status_restarts_total{%s}[5m]))" % POD,
                "kube_pod_status_ready{%s}" % POD)),
            unit="short", thresholds=GREEN_AMBER_RED,
        ),
        P.stat(
            "Uptime (oldest process)",
            "How long the longest-running JVM in the selection has been up, "
            "from Micrometer's own uptime binder. The JVM's, not the pod's: "
            "the board used to read `time() - min(kube_pod_start_time)`, "
            "which counts from the moment the kubelet started the container "
            "and so includes the JVM's startup. This value also survives "
            "having no kube-state-metrics at all. A number that keeps "
            "resetting to a few minutes is the restart loop the panel to the "
            "left counts.",
            P.targets("max(process_uptime_seconds{%s})" % APP),
            unit="s", thresholds=GREEN, graph_mode="none",
        ),
        P.stat(
            "Database pods ready",
            "The same Ready condition for the pods the `$db_pod` variable "
            "selected — by default anything in the namespace with `postgres` "
            "in its name, which is what charts/kates calls its bundled "
            "PostgreSQL. Zero here with the pool panels below showing no "
            "available connections is a database outage; zero here with a "
            "healthy pool means the application is talking to a database "
            "outside the cluster and this panel has nothing to see. "
            "kube-state-metrics.",
            P.targets(
                'sum(kube_pod_status_ready{namespace="$namespace", '
                'pod=~"$db_pod", condition="true"}) or vector(0)'),
            unit="short", thresholds=RED_AT_ZERO,
        ),
    ]


# ── HTTP ───────────────────────────────────────────────────────────────────

def _http() -> list[dict]:
    return [
        P.timeseries(
            "Request rate by method",
            "Requests per second reaching the Quarkus HTTP layer, split by "
            "method. `http_server_requests_seconds_count` is the count half "
            "of Micrometer's HTTP server timer, so `rate()` over it is a "
            "request rate — it is not a duration despite the `_seconds` in "
            "the name, which is the timer's base unit leaking into every one "
            "of its series. The binder is on because "
            "`quarkus.micrometer.binder.http-server.enabled=true`; with it "
            "off this whole row is empty.",
            P.targets((
                "sum by (method) (rate(http_server_requests_seconds_count{%s}[1m]))" % APP,
                "{{method}}")),
            unit="reqps", w=8,
        ),
        P.timeseries(
            "Error rate, 4xx and 5xx",
            "The same counter filtered to failing responses and split by "
            "status. Read the two classes apart: 4xx is a client sending "
            "something this API rejected — a malformed test spec, a run id "
            "that does not exist — and is not an outage, while 5xx is this "
            "process failing and every one of them has a stack trace in the "
            "logs. A 5xx rate that tracks the request rate is a broken "
            "deployment; a 5xx rate that tracks the benchmark load is the "
            "engine exhausting something.",
            P.targets((
                'sum by (status) (rate(http_server_requests_seconds_count'
                '{%s, status=~"4..|5.."}[1m]))' % APP,
                "{{status}}")),
            unit="reqps", w=8,
        ),
        P.timeseries(
            "Request latency percentiles",
            "p50, p95 and p99 of server-side request duration, computed from "
            "the timer's histogram buckets. This one *is* `histogram_quantile`"
            " territory and the only place on any Kates board that it is: "
            "`http_server_requests_seconds_bucket` is a real cumulative "
            "histogram with an `le` label, unlike the benchmark engine's "
            "latency series, which are pre-computed percentile gauges "
            "carrying a `quantile` label and must be selected, never "
            "re-aggregated. Summing by `le` before the quantile is what makes "
            "the result a percentile across all pods rather than a mean of "
            "per-pod percentiles.",
            P.targets(
                ("histogram_quantile(0.50, sum by (le) "
                 "(rate(http_server_requests_seconds_bucket{%s}[1m])))" % APP, "p50"),
                ("histogram_quantile(0.95, sum by (le) "
                 "(rate(http_server_requests_seconds_bucket{%s}[1m])))" % APP, "p95"),
                ("histogram_quantile(0.99, sum by (le) "
                 "(rate(http_server_requests_seconds_bucket{%s}[1m])))" % APP, "p99"),
            ),
            unit="s", w=8,
        ),
    ]


# ── JVM ────────────────────────────────────────────────────────────────────

def _jvm() -> list[dict]:
    return [
        P.timeseries(
            "Heap used, committed and max",
            "The three heap numbers, summed over the pools inside the heap "
            "area. Used climbing towards committed is normal; committed "
            "climbing towards max and staying there, with GC pause time "
            "rising beside it, is the shape of a heap that is too small for "
            "the workload — which for this process usually means a benchmark "
            "run holding more in flight than the container was sized for. "
            "`max` reads -1 for a pool with no configured maximum, which is "
            "why it is drawn rather than used as a denominator.",
            P.targets(
                ('sum(jvm_memory_used_bytes{%s, area="heap"})' % APP, "used"),
                ('sum(jvm_memory_committed_bytes{%s, area="heap"})' % APP, "committed"),
                ('sum(jvm_memory_max_bytes{%s, area="heap"})' % APP, "max"),
            ),
            unit="bytes", w=8,
        ),
        P.timeseries(
            "GC pause",
            "Seconds of garbage-collection pause per second, split by the "
            "collector's cause and action, plus the longest single pause. The "
            "rate is the one to watch — 0.1 means a tenth of every second is "
            "spent stopped — and it is what turns up as HTTP p99 latency in "
            "the row above without any corresponding rise in p50. The `_max` "
            "series is a Micrometer gauge of the worst pause in its window, "
            "not a counter: never `rate()` it.",
            P.targets(
                ("sum by (cause, action) (rate(jvm_gc_pause_seconds_sum{%s}[1m]))" % APP,
                 "{{cause}} / {{action}}"),
                ("max(jvm_gc_pause_seconds_max{%s})" % APP, "longest pause"),
            ),
            unit="s", w=8,
        ),
        P.timeseries(
            "Threads",
            "Live, daemon and peak thread counts. The benchmark engine starts "
            "worker threads per task, so this climbs with concurrent runs by "
            "design; what is not by design is live staying high after the "
            "runs finish, which is a leaked executor and eventually an OOM. "
            "Peak never falls, so the gap between peak and live is the "
            "high-water mark of everything that has run since startup.",
            P.targets(
                ("sum(jvm_threads_live_threads{%s})" % APP, "live"),
                ("sum(jvm_threads_daemon_threads{%s})" % APP, "daemon"),
                ("sum(jvm_threads_peak_threads{%s})" % APP, "peak"),
            ),
            unit="short", w=8,
        ),
    ]


# ── Database ───────────────────────────────────────────────────────────────

def _database() -> list[dict]:
    return [
        P.timeseries(
            "Pool connections",
            "The Agroal pool, by state: active (checked out), available "
            "(idle in the pool) and the maximum simultaneously active since "
            "startup. Active pinned at the pool size with `awaiting` above "
            "zero is a pool that is too small or a query that is too slow — "
            "the next two panels say which. All four are Micrometer gauges "
            "Quarkus registers per datasource, so the `datasource` label "
            "separates them if this application ever gains a second one.",
            P.targets(
                ("sum(agroal_active_count{%s})" % APP, "active"),
                ("sum(agroal_available_count{%s})" % APP, "available"),
                ("sum(agroal_max_used_count{%s})" % APP, "max used"),
                ("sum(agroal_awaiting_count{%s})" % APP, "awaiting"),
            ),
            unit="short", w=8,
        ),
        P.timeseries(
            "Acquisitions per second",
            "How often the application takes a connection out of the pool. "
            "`agroal_acquire_count` is a *gauge* holding a cumulative total, "
            "not a Prometheus counter — Quarkus registers it with "
            "`Gauge.builder`, so it carries no `_total` suffix and Prometheus "
            "does not type it as a counter. `rate()` over it still gives the "
            "right answer because the underlying value only ever increases, "
            "but it will not be corrected for a restart the way a real "
            "counter is: expect one spike per pod restart.",
            P.targets(("sum(rate(agroal_acquire_count{%s}[1m]))" % APP, "acquisitions/s")),
            unit="ops", w=8,
        ),
        P.timeseries(
            "Time waiting for a connection",
            "How long a request spent blocked waiting for the pool, average "
            "and worst case, in milliseconds. This is the panel that "
            "distinguishes a pool that is too small from a database that is "
            "too slow: waiting time rising while `Pool connections` shows "
            "active pinned at the ceiling is contention for the pool, and "
            "raising the pool size fixes it. Both series are Micrometer "
            "gauges that Quarkus registers with a `milliseconds` base unit, "
            "which is where the suffix comes from — the board this replaced "
            "read `agroal_blocking_time_total_seconds` divided by "
            "`agroal_blocking_time_count`, and neither of those names has "
            "ever existed.",
            P.targets(
                ("max(agroal_blocking_time_average_milliseconds{%s})" % APP, "average"),
                ("max(agroal_blocking_time_max_milliseconds{%s})" % APP, "worst"),
            ),
            unit="ms", w=8,
        ),
    ]


# ── Container ──────────────────────────────────────────────────────────────

def _container() -> list[dict]:
    return [
        P.timeseries(
            "CPU cores used",
            "CPU seconds consumed per second, per container, which is cores. "
            "cAdvisor, via the kubelet — a different source from everything "
            "above it on this board, and one that is absent on a cluster "
            "whose kubelet metrics are not scraped. Compare it with the "
            "container's CPU limit rather than with the node's size: a "
            "process throttled at its limit shows up here as a flat line at "
            "exactly the limit and in the JVM row as rising GC pause.",
            P.targets((
                "sum by (pod, container) (rate(container_cpu_usage_seconds_total"
                '{%s, container!="", container!="POD"}[5m]))' % POD,
                "{{pod}} / {{container}}")),
            unit="short", w=12,
        ),
        P.timeseries(
            "Container memory",
            "Resident set size and working set, per container, as the kubelet "
            "sees them. Working set is the number the OOM killer acts on, so "
            "it — not RSS and not the JVM heap — is what to compare against "
            "the memory limit. A JVM whose heap is comfortable and whose "
            "working set is at the limit is spending the difference on "
            "metaspace, thread stacks, direct buffers and the Kafka clients' "
            "own allocations, none of which the heap panel above can see. "
            "cAdvisor.",
            P.targets(
                ("sum by (pod) (container_memory_rss"
                 '{%s, container!="", container!="POD"})' % POD, "RSS {{pod}}"),
                ("sum by (pod) (container_memory_working_set_bytes"
                 '{%s, container!="", container!="POD"})' % POD, "working set {{pod}}"),
            ),
            unit="bytes", w=12,
        ),
    ]


def build(variant: str) -> list[Row]:
    return [
        Row("", _header()),
        Row("HTTP", _http()),
        Row("JVM — Micrometer's names, not the JMX agent's", _jvm()),
        Row("Database — the Agroal pool", _database()),
        Row("Container resources — cAdvisor", _container()),
    ]
