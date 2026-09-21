# KATES — Overview

**Delivered by** `charts/kates` · **uid** `kates-overview` ·
**12 panels in 3 rows**

## The question it answers

*Is this Kates release up and doing anything?*

The application's own account of itself: uptime, how many runs it is holding,
how much API traffic it is serving and how fast, how the JVM and the
connection pool are doing.

## Who opens it

Whoever installed `charts/kates`. That is the point of the board — it ships
with the application chart, and it reads **only series the application
publishes about itself**, so it works with nothing beside it but a Grafana.

If you are running the full `charts/monitoring` stack, open
`kates-application` instead. It answers a bigger question with more sources.

## Why this board still exists

It is not a subset of `kates-application`, and the difference is not panel
count — it is **prerequisites**.

`kates-application` is what the *cluster* knows about the application: half of
it is kube-state-metrics (pods ready, restarts) and cAdvisor (container CPU
and memory). On a cluster with neither, that board's header and its whole
bottom row are blank.

This board is what the *application* knows about itself. Every expression on
it reads a Micrometer series from `/q/metrics`. There is nothing here that a
running Kates process and a Prometheus scraping it cannot produce.

The two overlap on HTTP, JVM and the pool, and they should: those are the
questions both audiences ask. Where they differ is which of them can be
trusted on a given cluster.

## How it is scoped

One variable, `$job`, from `label_values(process_uptime_seconds, job)` — the
ServiceMonitor's job label, which for `charts/kates` is the Service name and
therefore the release fullname.

The template this replaces interpolated `{{ include "kates.fullname" . }}`
into 16 of its 21 expressions as a literal, which is why the board had to be
rendered per release and why the ConfigMap grew a copy of the whole JSON
document for every install. With the selector as a variable, one copy in a
Grafana serves every Kates release it can see and the dropdown separates them
— so unlike the connect-cluster and mirror-maker2 boards, there is no per-
release uid to collide and nothing is injected at render time.

## Where each metric comes from

All of it: **the Kates application, through Micrometer**, scraped by
`charts/kates`'s ServiceMonitor (`metrics.serviceMonitor.enabled`).

Four binders, all configured in `kates/src/main/resources/application.properties`:

| Binder | Setting | Series here |
|---|---|---|
| HTTP server | `quarkus.micrometer.binder.http-server.enabled=true` for `_count`, `_sum` and `_max`; the `_bucket` series behind *Request latency percentiles* comes from the `HttpLatencyHistogram` `MeterFilter` (eleven fixed SLO boundaries, 5 ms to 10 s), because the binder alone publishes no buckets | `http_server_requests_seconds_*` |
| JVM | `quarkus.micrometer.binder.jvm=true` | `jvm_memory_*` |
| System | `quarkus.micrometer.binder.system=true` | `process_uptime_seconds`, `process_cpu_usage`, `system_cpu_usage` |
| Agroal | `quarkus.datasource.metrics.enabled=true` — off by default whatever extensions are present; the application sets it | `agroal_*` |

Plus this repository's own meters, from
`kates/src/main/java/com/bmscomp/kates/engine/`: `kates_benchmark_active_runs`
and `kates_benchmark_throughput_rec_sec` (`BenchmarkMetrics`) and
`kates_tests_completed_total` (`KatesMetrics`).

The buckets and the pool switch both postdate the `1.22.0` image, which
publishes neither: on it, *Request latency percentiles* and the *Database*
row are empty while everything else on the board draws.

## Eleven names were wrong, and five of them were renamable

The board this replaces was 130 lines of hand-written JSON inside a Helm
template. Eleven of the names in it could never produce data.

**Five were misspelled**, and a rename fixed each one in place:

| Was read | Actually published | Registered by |
|---|---|---|
| `agroal_pool_active_count` | `agroal_active_count` | `AgroalMetricsRecorder` |
| `agroal_pool_available_count` | `agroal_available_count` | `AgroalMetricsRecorder` |
| `agroal_pool_max_used_count` | `agroal_max_used_count` | `AgroalMetricsRecorder` |
| `kates_active_runs` | `kates_benchmark_active_runs` | `BenchmarkMetrics` |
| `kates_benchmark_throughput_records_per_sec` | `kates_benchmark_throughput_rec_sec` | `BenchmarkMetrics` |

There is no `pool` segment anywhere in Quarkus' Agroal metric names.

Because the monitoring chart's own board spelled the same five metrics the
*other* way, this repository shipped **three spellings of the same metric**
(counting `agroal_blocking_time_total_seconds`, which `kates-application` read
and which is also wrong — see that board's README). They now all agree, and
they agree with the code.

**Six more had no fix on this board**, and three panels went with them:

| Was read | Why it cannot work here | What happened to its panel |
|---|---|---|
| `kates_benchmark_p99_latency_ms` | there has never been a `p99_latency_ms` meter under any spelling — the percentiles are one series with a `quantile` label. `kates_benchmark_latency_ms{quantile="0.99"}` does publish the number (added in phase 6), **but this board does not read it**: see below | **Benchmark P99 Latency (ms)** — dropped |
| `kafka_admin_client_connection_count`, `kafka_admin_client_request_total` | Micrometer's Kafka client binder instruments clients the Kafka *extension* creates; this application builds its own `AdminClient` and nothing binds it | **Kafka Admin Connections** — dropped |
| `vertx_eventbus_delivery_time_seconds_bucket` | Vert.x's own metrics options, which Quarkus does not enable | **Vert.x Event Loop Latency** — dropped; the API row covers the request path this application's traffic actually takes |
| `process_cpu_seconds_total` | a Prometheus simpleclient *default export*; Quarkus' Micrometer registry never calls `DefaultExports.initialize()` | **CPU Usage** survived, renamed **CPU**, on `process_cpu_usage` and `system_cpu_usage` |
| `process_resident_memory_bytes` | same | **JVM Heap / Native Memory** survived, renamed **JVM memory**, with `jvm_memory_used_bytes{area="nonheap"}` as the part of the gap the application does publish |

**The p99 panel was dropped, not renamed**, which is the one to be clear about
because the rename looks so available. `kates_benchmark_latency_ms` exists and
is correct — but it is a *per-run* meter, registered when a run starts and
unregistered when it ends, and this board's job is what one release knows
about itself with nothing else installed. A panel that is empty except while a
run is in flight belongs on the boards for a run in flight:
[`kates-benchmark`](../kates-benchmark/README.md) reads it in full detail and
[`kates-chaos`](../kates-chaos/README.md) reads it across a fault. **No
`kates_benchmark_latency_*` series appears anywhere on this board.**

Two legends also wrote `{{ task_id }}`. No meter in `BenchmarkMetrics` has ever
carried a `task_id` tag, so both rendered empty.

The Kafka admin-client panel is the one genuine loss: knowing how the
application's own Kafka connections are behaving is useful, and nothing else
covers it. Getting it back means binding `KafkaClientMetrics` to the clients
the engine creates, which is an application change, not a dashboard one.

## The panel arithmetic

| | |
|---|---:|
| Panels at HEAD (3 stat, 10 timeseries, no rows) | 13 |
| Dropped — *Benchmark P99 Latency (ms)*, *Kafka Admin Connections*, *Vert.x Event Loop Latency* | −3 |
| Added — *Request rate by endpoint*, *Time waiting for a connection* | +2 |
| **Panels now** | **12** |
| Row headers added — *API*, *JVM*, *Database — the Agroal pool*; the old board had none | +3 |
| **Objects `scripts/check-dashboards.py` counts** | **15** |

Ten panels survived, and nine of them were renamed: *Active Test Runs* →
*Active test runs*, *Total Completed Tests* → *Tests completed*, *Request
Rate* → *Request rate*, *Request Latency* → *Request latency percentiles*,
*Error Rate (5xx)* → *Server errors*, *Benchmark Throughput (records/s)* →
*Benchmark throughput*, *JVM Heap / Native Memory* → *JVM memory*, *CPU Usage*
→ *CPU*, *Database Connections* → *Connection pool*. Only **Uptime** kept its
title.

That is where an earlier version of this section went wrong. It headed a table
of four rows — five series across four panels — with "four panels were
dropped", but two of those four panels had survived inside renamed panels
(*CPU Usage* and *JVM Heap / Native Memory*), and one of the genuinely dropped
panels, *Benchmark P99 Latency (ms)*, was in the *renamed* table instead.
Three panels were dropped, not four.

## The sections

### Header — four stats

`Uptime` · `Active test runs` · `Tests completed` · `Request rate`

`Active test runs` is bounded by `kates.engine.max-concurrent-tests`; sitting
at that ceiling means further submissions are being rejected with a
concurrency error rather than queued. `Request rate` near zero with runs
active is normal — the engine polls its backends itself, and nothing has to
call the API for a run to proceed.

### API

`Request rate by endpoint` · `Request latency percentiles` · `Server errors` ·
`Benchmark throughput`

Quarkus reports the URI *template* (`/api/runs/{id}`), not the resolved path,
so the cardinality stays bounded no matter how many run ids exist. An endpoint
appearing with a literal id in it means something is serving a path the
framework did not match — worth fixing before it becomes a cardinality
problem.

`Benchmark throughput` is empty between runs, by design: that meter is tagged
with `run_id` and is unregistered when the run ends, because an unbounded
label that is never cleaned up grows Prometheus without limit. It is here so
one board can answer "is it doing anything at all"; the live detail is on
`kates-benchmark`.

### JVM

`JVM memory` · `CPU`

Non-heap is plotted beside the heap because the board this replaces plotted
RSS there, to catch memory the heap cannot see — and RSS is not available
here. Non-heap (metaspace, code cache, compressed class space) is the part of
that gap the application does publish. The rest — thread stacks, direct
buffers, the Kafka clients' allocations — needs cAdvisor and is on
`kates-application`.

`process_cpu_usage` and `system_cpu_usage` are **ratios in [0, 1]**, not cores
and not seconds. Process near 1 means this JVM is saturating what it was
given; system near 1 with process low means it is competing with something
else on the node.

### Database — the Agroal pool

`Connection pool` · `Time waiting for a connection`

`awaiting` above zero is the signal; everything else can look busy without
anything being wrong. Rising wait time with `active` pinned at the ceiling is
pool contention and a bigger pool fixes it; rising wait time with spare
capacity is a slow database and a bigger pool will not.

## Delivery

`charts/monitoring` ships this board with every other one, in its
`<release>-kates-dashboards` ConfigMap (`dashboards.enabled`). Through kates
0.8.0 the application chart carried its own copy as
`<fullname>-grafana-dashboard`; that object goes away when the release is
upgraded, and the sidecar re-provisions the board — same uid, so existing
Grafana links keep resolving — from the monitoring ConfigMap instead.

That file is generated by `scripts/gen-dashboards.py` from `board.py` in this
directory, and `gen-dashboards.py --check` fails when the copy drifts. Helm
cannot read a file outside its own chart directory, which is why the copy
exists at all.

## Related boards

- `kates-application` — the same process plus what the cluster sees of it.
- `kates-benchmark`, `kates-trend`, `kates-chaos` — the benchmark itself.
- `kyverno-security` — the other board `charts/kates` delivers.
