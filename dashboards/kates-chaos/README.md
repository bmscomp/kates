# Kates — Chaos

**Delivered by** `charts/monitoring` · **uid** `kafka-chaos-dashboard` ·
**25 panels**, nine of them on a row that fills only for a run that verified
integrity — see below

## The question it answers

*We injected a fault. What did the cluster, and the workload on it, actually
do?*

The Litmus experiment timeline, with the Kafka brokers and the Kates benchmark
run laid over it on the same time axis. Litmus grades whether it managed to
inject the fault; this board grades whether anything survived it.

## Who opens it

Whoever is running a GameDay, during it and afterwards. And whoever is
answering the question the chaos alert rules pose in
`charts/monitoring/templates/prometheus-chaos-rules.yaml`: *was this broker
restart an experiment or an incident?* — which is exactly what the
`Chaos engines running` stat at the top left decides.

## How to read it

Top to bottom is the order the questions arrive:

1. Is a fault being applied right now, and how have the experiments been
   going? (header)
2. What did the brokers do — did they go away, come back, and settle?
   (*Kafka under fault*)
3. When exactly was each fault applied? (*Experiment history*)
4. What did the workload underneath experience? (*Kates during chaos*)

Every reading on this board is relative to step 3. Line the experiment
duration steps up with the troughs in broker readiness and the dips in the
Kates row: a restart inside a fault window is the experiment working, and the
same restart outside one is an incident — the distinction
`KafkaBrokerRestartUnexpected` is built on.

## How it is scoped

| Variable | Query | Notes |
|---|---|---|
| `$namespace` | `label_values(kube_pod_status_ready, namespace)` | the Kafka namespace; `charts/monitoring` calls it `kafka` by default in its alert rules |
| `$pod` | `label_values(kube_pod_status_ready{namespace="$namespace"}, pod)` | multi-select, defaults to all |
| `$kates_job` | `label_values(kates_benchmark_active_runs, job)` | which Kates release ran the load |

The board this replaces hard-coded `namespace="kafka"` and
`pod=~"krafter-pool-.*"`, the pod name of one particular Kafka cluster in one
particular namespace. `$pod` defaults to every pod in the namespace, which
during chaos is usually what you want: an experiment may hit anything in
there, and the CPU and memory panels narrow to `container="kafka"` anyway.

`kube_pod_status_ready` is the anchor because kube-state-metrics publishes it
for every pod in every namespace it watches — it exists whether or not chaos
is installed, Kafka is up, or a benchmark is running. The labels are
kube-state-metrics' own: `namespace` and `pod` are on the series it exports,
not added by a relabeling, so nothing about this scrape has to be configured
for the dropdowns to fill.

**There is no `$cluster` here, and that is not an oversight.** Every Kafka
panel on this board reads kube-state-metrics or cAdvisor — `kube_pod_*`,
`container_*` — and neither carries `strimzi_io_cluster`: Strimzi's pod labels
reach a series only through a PodMonitor relabeling, and neither of those
producers is scraped through one. `$pod` is the cluster selector, which is
also why it is multi-select. The cost is the one thing to know before using
the board on a busy cluster: because the anchor is every namespace
kube-state-metrics watches, `$namespace` opens on whichever namespace sorts
first, not on the Kafka one. It is a visible dropdown, so the fix is to pick —
but pick before reading, not after.

## Where each metric comes from — four producers on one board

This is the board where "what do I need to install for this panel to fill in"
matters most, because it reads four unrelated sources and two of them are
optional add-ons.

| Source | Series | Installed by |
|---|---|---|
| **Litmus chaos-exporter** | `litmuschaos_passed_experiments`, `litmuschaos_failed_experiments`, `litmuschaos_probe_success_percentage`, `litmuschaos_experiment_total_duration` | LitmusChaos, separately from this repository's charts. Not shipped here. |
| **kube-state-metrics, custom resources** | `kube_customresource_chaosengine_status_engine_status` | kube-state-metrics **with custom-resource metrics configured for `ChaosEngine`**. Not configured by default — see the warning below. |
| **kube-state-metrics + cAdvisor** | `kube_pod_status_ready`, `kube_pod_container_status_restarts_total`, `container_cpu_usage_seconds_total`, `container_memory_usage_bytes` | the `kube-state-metrics` subchart and the kubelet, both on by default in `charts/monitoring` |
| **The Kates application** | `kates_benchmark_throughput_rec_sec`, `kates_benchmark_latency_ms`, `kates_benchmark_errors_total` | `charts/kates`, scraped by its ServiceMonitor |

> **The dishonest zero, and what replaced it.** `Chaos engines running` used
> to end in a bare `or vector(0)`: with kube-state-metrics running without
> custom-resource metrics for `ChaosEngine` the series does not exist, and the
> panel drew a reassuring zero indistinguishable from "no experiment running".
> Its fallback is now **anchored to the unfiltered ChaosEngine series**, which
> is the only thing that proves the collector is configured — kube-state being
> up proves nothing here. A cluster with engines but none initialized reads 0;
> a cluster the collector cannot see reads *No data*. See [the zero-fallback
> convention](../README.md#zeros-that-mean-measured-and-zeros-that-mean-absent).
>
> **The alerts still have the old problem.** `KafkaChaosExperimentActive`, and
> the `and on() count(…) == 0` guard on `KafkaBrokerRestartUnexpected`, both
> read the same series and neither can be anchored the way a panel can — the
> guard is silently always true without that configuration. If you rely on
> either, confirm the series exists first.
>
> **`$namespace` no longer offers every namespace in the cluster.** It used to
> resolve through `kube_pod_status_ready`, which kube-state publishes for every
> pod everywhere, so the picker filled with `cert-manager`, `kube-system` and
> the rest and the board opened on whichever sorted first — every panel then
> honestly reading *No data* about the wrong namespace. It is scoped to
> namespaces holding a ChaosEngine instead. An empty picker is now itself the
> diagnosis, and the variable's tooltip says so.

## The RTO / RPO / data-integrity row

It is at the bottom, **collapsed**, and it fills only for a run that verified
integrity. This section is why it is shaped that way, and what it used to be.

### What it reads

Its eight panels read `kafka:chaos:*` recorded series, defined by
`charts/monitoring/templates/prometheus-chaos-rules.yaml` in the
`kafka-chaos-rto-rpo` group:

| Recorded series | Reads | Published by |
|---|---|---|
| `kafka:chaos:producer_rto_seconds` | `kates_integrity_result_producer_rto_seconds` | `BenchmarkMetrics` |
| `kafka:chaos:consumer_rto_seconds` | `kates_integrity_result_consumer_rto_seconds` | `BenchmarkMetrics` |
| `kafka:chaos:max_rto_seconds` | `kates_integrity_result_max_rto_seconds` | `BenchmarkMetrics` |
| `kafka:chaos:rpo_seconds` | `kates_integrity_result_rpo_seconds` | `BenchmarkMetrics` |
| `kafka:chaos:data_loss_percent` | `kates_integrity_result_data_loss_percent` | `BenchmarkMetrics` |
| `kafka:chaos:lost_records` | `kates_integrity_result_lost_records` | `BenchmarkMetrics` |
| `kafka:chaos:duplicate_records` | `kates_integrity_result_duplicate_records` | `BenchmarkMetrics` |
| `kafka:chaos:e2e_latency_ms` | `kates_benchmark_latency_ms{quantile="0.99"}` | `BenchmarkMetrics` |
| `kafka:chaos:producer_throughput` | `kates_benchmark_throughput_rec_sec` | `BenchmarkMetrics` |

`com.bmscomp.kates.engine.BenchmarkMetrics` registers the seven
`kates_integrity_result_*` gauges from `IntegrityResult` on the first
verification a run reports, tagged `run_id` and `test_type`, and unregisters
them with the rest of the run's meters when it ends.

### When it is empty, and what that means

**Lazily registered, deliberately.** A run that never verifies integrity —
every plain load test — publishes none of these, so the row stays blank. That
is the expected state. A zero would read as "recovered instantly", which is a
much stronger claim than "was never measured", and every load test in the
system would be making it.

So, reading a blank row:

* **During an ordinary run** — integrity was not verified. Nothing is wrong.
* **During a chaos run** — either the verifier has not finished (these publish
  from the first poll after it completes), or the Kates ServiceMonitor is not
  being scraped. Tutorial 13's troubleshooting section covers the second.

### What this used to be

Until this release the row was collapsed behind a panel explaining that it
could never fill, and that was accurate. Every series above read a name
nothing published: the verifier computed RTO, RPO and data loss in Java and
returned them over the REST API, but never registered them with Micrometer. A
recording rule over a series that does not exist records nothing, so all of
them were empty, and the four alerts sitting on them — `KafkaRTOExceedsSLA`,
`KafkaRPOExceedsSLA`, `KafkaDataLossDetected`, `KafkaE2ELatencySpike` —
installed cleanly and could never fire.

Two further faults in the same file came out with it. It contained
`max without(instance) (a, b)`, which is not valid PromQL — an aggregation
takes one expression, not two — so Prometheus would have refused the entire
`PrometheusRule` and loaded **none** of its four groups, including the three
that were correct. And it had no `monitoring.coreos.com/v1` capability check,
unlike every other chart here, so enabling it without the Prometheus Operator
failed at apply time after the rest of the chart had installed.

All three were invisible for the same reason: the file lives behind
`chaosAlerts.enabled`, which defaults to `false`, and nothing in the repository
had ever rendered it with that toggle on. `scripts/chart-matrix/monitoring.yaml`
now renders every toggle and puts the result through promtool, and
`scripts/metric-contract/monitoring.yaml` holds every name in it to a
catalogue.

Two of the names could not be fixed by publishing them, because they should
not have existed: `kafka_chaos_e2e_latency_ms` and
`kafka_chaos_producer_throughput_records_per_sec` are the workload's own
measurements under the JMX exporter's `kafka_` namespace, and Kates already
reports both. Those two rules now read the series that exist rather than
duplicating them under a second name.

### The unit trap

`IntegrityResult` holds these as `Duration` and exposes **milliseconds**; the
series names and every alert threshold are in **seconds**.
`BenchmarkMetrics.recordIntegrity` divides. Had it not, `KafkaRTOExceedsSLA`
— which fires above 30, meaning thirty seconds — would have fired on every run
that took longer than 30 milliseconds to recover. `BenchmarkMetricsTest`
asserts the conversion with a 45-second RTO rather than only asserting the
names.

### The row above it still matters

The *Kates during chaos* row — throughput, latency and errors observed by the
workload while the fault was applied — is not redundant now that this one
works. It measures what a client experienced during the fault; this row
measures what a verifier concluded afterwards. For most questions the first is
the better answer, and it fills for every run rather than only for verified
ones.

## The sections

### Header — four stats

`Chaos engines running` · `Experiments passed` · `Experiments failed` ·
`Probe success rate`

The Litmus counters are cumulative since the exporter started, so the useful
reading is the change across a GameDay, not the absolute number. "Passed"
means Litmus' own probes were satisfied; it says nothing about what the
workload saw.

A *failed* experiment is often not a cluster problem: Litmus marks an
experiment failed when it could not inject the fault at all, which means the
resilience claim for that window is unsupported rather than disproved.

### Kafka under fault

`Broker pod readiness` · `Broker restarts` · `Broker CPU cores` ·
`Broker memory`

Readiness is the fault becoming visible, and the width of the trough is how
long Kubernetes took to get the broker back — which is **not** RTO. RTO is how
long the clients took to recover.

The two panels to watch *after* the fault clears are CPU and memory: a broker
that comes back and then sits at much higher CPU than its peers is replaying
segments or catching up as a follower, and a broker whose memory never returns
to its pre-fault level has not finished rebuilding its page cache. Neither is
recovered, whatever readiness says. `KafkaHighCPUPostChaos` fires on the
first of those.

### Experiment history

`Experiment duration` — the timeline everything else is read against.

### Kates during chaos

`Benchmark throughput during chaos` · `Benchmark p99 latency during chaos` ·
`Benchmark errors during chaos`

The resilience evidence that works. Throughput that dips and recovers is a
cluster that failed over; throughput that goes to zero and stays there is one
that did not. A latency spike with no errors is a failover the clients rode
out — the result the exercise is looking for. Empty means no benchmark was
running, and a chaos experiment with no load under it proves nothing.

The p99 series is selected by its `quantile` label — these are pre-computed
percentile gauges, never `histogram_quantile()`. See
`dashboards/kates-benchmark/README.md`.

### RTO / RPO / data integrity

Collapsed, and covered above.

## Related boards

- `kates-benchmark` — the run in the bottom row, in full detail.
- `kates-application` — the Kates process itself, if it is the thing that
  wobbled rather than the cluster.
- `kafka-kraft` and the Strimzi operator's `strimzi-kafka.json` — broker-side
  detail: ISR dynamics, controller elections, the replication the failover
  actually depended on.
