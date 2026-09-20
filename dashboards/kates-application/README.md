# Kates — Application Health

**Delivered by** `charts/monitoring` · **uid** `kates-application-health` ·
**19 panels**

## The question it answers

*Is the Kates process itself healthy?* — not the Kafka cluster it drives, not
the benchmark it is running, the Quarkus application in the middle.

Every other Kates board reads what the benchmark engine measured. This one
reads what the process running the engine is doing: how much HTTP traffic it
is serving and how much of it is failing, whether the heap and the garbage
collector are keeping up, whether the database connection pool is a
bottleneck, and what the kubelet thinks of the container.

## Who opens it

- The person whose `POST /api/runs` returned 500, or timed out.
- The person who has just deployed a new Kates image and wants to see it
  settle.
- The person looking at a benchmark result that is inexplicably worse than
  the last one, having already checked `kates-trend`, and wondering whether
  the *harness* was the bottleneck rather than the cluster.

If the question is about a benchmark run, this is the wrong board:
`kates-benchmark` for the live run, `kates-trend` for the comparison across
runs. If the question is about a Kafka broker, the Strimzi operator's own
`strimzi-kafka.json` and this repository's `kafka-kraft` cover it.

## How it is scoped

Four template variables, all derived from data rather than hard-coded:

| Variable | Comes from | What it selects |
|---|---|---|
| `$namespace` | `label_values(process_uptime_seconds, namespace)` | the namespace a Kates process runs in |
| `$job` | the same, per namespace | the ServiceMonitor scrape job, i.e. one release |
| `$pod` | the same, per job | the pods Prometheus is actually scraping |
| `$db_pod` | `kube_pod_status_ready{pod=~".*postgres.*"}` | the database pods, if Kubernetes can see them |

The board this replaced hard-coded `namespace="kates"`, `job="kates"`,
`pod=~"kates-.*"` and `container="kates"`, so it worked for exactly one
release installed under exactly one name. Deriving `$pod` from the
application's own `process_uptime_seconds` is strictly better than a name
regex: it is the set of pods being scraped, so a pod that was renamed is still
in the list and a pod from an unrelated release that happens to start with
`kates-` is not.

**Where those three labels come from, since `charts/kates`'s ServiceMonitor
sets no relabelings of its own.** They are Prometheus Operator's defaults, not
something this chart configures, and they are unconditional:
`generateServiceMonitorConfig` in `pkg/prometheus/promcfg.go` appends
`__meta_kubernetes_namespace → namespace`, `__meta_kubernetes_pod_name → pod`
and `__meta_kubernetes_service_name → job` (plus `service` and `container`) to
every scrape config it writes — `job` is then overridden only if the
ServiceMonitor sets `jobLabel`, which this one does not, so `$job` is the
Kates **Service** name. Checked against prometheus-operator v0.89.0, which is
what kube-prometheus-stack 82.4.3 — the version `charts/monitoring` pins —
deploys. So `$pod` resolves on any real cluster; a `$pod` that comes up empty
in a test fixture means the fixture is not emitting the label, not that the
variable is wrong.

`$db_pod` is the one heuristic left, and it is unavoidable: kube-state-metrics
has no edge from a Deployment to the database it connects to. The default
matches what `charts/kates` names its bundled PostgreSQL
(`<release>-postgresql-*`). An external database leaves the dropdown empty,
which is the honest answer for a database Kubernetes cannot see.

## Where each metric comes from — and what to install

This board reads **three different producers**, and a panel is empty if its
producer is not installed. That is the single most useful thing to know before
concluding something is broken.

| Source | Series on this board | Installed by |
|---|---|---|
| **The application, via Micrometer** | `process_uptime_seconds`, `http_server_requests_seconds_*`, `jvm_*`, `agroal_*` | `quarkus-micrometer-registry-prometheus`, already a dependency. Scraped by `charts/kates`'s ServiceMonitor (`metrics.serviceMonitor.enabled`). |
| **kube-state-metrics** | `kube_pod_status_ready`, `kube_pod_container_status_restarts_total` | the `kube-state-metrics` subchart of kube-prometheus-stack, which `charts/monitoring` enables by default |
| **cAdvisor, via the kubelet** | `container_cpu_usage_seconds_total`, `container_memory_rss`, `container_memory_working_set_bytes` | the kubelet's own endpoint, scraped when `kube-prometheus-stack.kubelet.enabled` is true (it is) |

Within the application's own metrics, three Micrometer binders matter, all
enabled in `kates/src/main/resources/application.properties`:

- `quarkus.micrometer.binder.http-server.enabled=true` — the HTTP row.
- `quarkus.micrometer.binder.jvm=true` — the JVM row.
- `quarkus.micrometer.binder.system=true` — `process_uptime_seconds` in the
  header.

The Agroal pool metrics need `quarkus.datasource.jdbc.enable-metrics`
(Quarkus enables it when the Micrometer extension is present). They are
registered by `io.quarkus.agroal.runtime.metrics.AgroalMetricsRecorder`, which
is where the exact names come from.

## `jvm_*` here is Micrometer's, not the JMX agent's

This matters enough to state on its own, because the same four words name two
different series across this repository.

This board reads a **Quarkus application**. The Kafka boards read a **broker
through the Prometheus JMX agent**. The two publish different names for the
same quantities:

| Quantity | Here (Micrometer) | Kafka boards (JMX agent) |
|---|---|---|
| Heap used | `jvm_memory_used_bytes{area="heap"}` | `jvm_memory_bytes_used{area="heap"}` |
| GC pause | `jvm_gc_pause_seconds_sum` (labels `cause`, `action`) | `jvm_gc_collection_seconds_sum` (label `gc`) |
| Live threads | `jvm_threads_live_threads` | `jvm_threads_current` |

Neither is a fallback for the other. Copying a JVM expression from a broker
board onto this one produces a panel that is empty for a reason nobody finds
quickly, and that is one of the two mistakes this whole refactor exists to
stop.

## The sections

### Header — four stats

`Pods ready` · `Container restarts (5m)` · `Uptime (oldest process)` ·
`Database pods ready`

Triage. Three of the four are kube-state-metrics; `Uptime` is the
application's own, which is why it is the one that still works on a cluster
with no cluster-monitoring stack. It also measures the JVM rather than the
pod: the board this replaced computed `time() - min(kube_pod_start_time)`,
which counts from the moment the kubelet started the container and therefore
includes the JVM's own startup.

### HTTP

`Request rate by method` · `Error rate, 4xx and 5xx` · `Request latency
percentiles`

4xx and 5xx are on one panel but split by status on purpose: 4xx is a client
sending something this API rejected (a malformed spec, an unknown run id) and
is not an outage; 5xx is this process failing, and every one has a stack trace
in the log.

The latency panel is the **only** place on any Kates board where
`histogram_quantile()` is correct. `http_server_requests_seconds_bucket` is a
real cumulative histogram with an `le` label. The benchmark engine's latency
series are not: they are pre-computed percentile gauges with a `quantile`
label, and they must be selected, never re-aggregated.

### JVM

`Heap used, committed and max` · `GC pause` · `Threads`

Read heap and GC together. Committed pinned at max with GC pause time rising
beside it is a heap too small for the workload — usually a benchmark holding
more in flight than the container was sized for. `jvm_gc_pause_seconds_max` is
a Micrometer gauge of the worst pause in its window, **not** a counter: never
`rate()` it.

`Threads` climbs with concurrent runs by design. What is not by design is
live staying high after the runs finish, which is a leaked executor.

### Database — the Agroal pool

`Pool connections` · `Acquisitions per second` · `Time waiting for a
connection`

`Pool connections` with `active` pinned at the ceiling and `awaiting` above
zero is contention, and raising the pool size fixes it. The same picture with
spare capacity and rising wait time is a slow database, and raising the pool
size will not.

Two corrections landed here. The board this replaced read
`agroal_blocking_time_total_seconds` divided by `agroal_blocking_time_count`,
and **neither name has ever existed**: Quarkus registers the timing gauges
with `baseUnit("milliseconds")`, so Micrometer publishes
`agroal_blocking_time_total_milliseconds`, `…_average_milliseconds` and
`…_max_milliseconds`, and there is no `_count` companion at all. The panel
rendered an empty series divided by an empty series, which draws exactly like
a pool nobody is waiting on.

`agroal_acquire_count` is a **gauge** holding a cumulative total, not a
Prometheus counter — Quarkus registers it with `Gauge.builder`, so it carries
no `_total` suffix and Prometheus does not type it as a counter. `rate()` over
it still gives the right answer because the value only increases, but it will
not be corrected for a restart the way a real counter is: expect one spike per
pod restart.

### Container resources — cAdvisor

`CPU cores used` · `Container memory`

Working set, not RSS and not the JVM heap, is the number the OOM killer acts
on, so it is the one to compare against the memory limit. A JVM whose heap is
comfortable and whose working set is at the limit is spending the difference
on metaspace, thread stacks, direct buffers and the Kafka clients' own
allocations — none of which the heap panel can see.

## Related boards

- `kates-overview` — the same process, read only through metrics the
  application publishes about itself. Works with no kube-state-metrics and no
  cAdvisor; delivered by `charts/kates`.
- `kates-benchmark` — the live run.
- `kates-trend` — runs compared against each other.
- `kates-chaos` — the same application observed while a fault is injected.
