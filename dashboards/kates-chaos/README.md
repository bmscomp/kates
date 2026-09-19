# Kates — Chaos

**Delivered by** `charts/monitoring` · **uid** `kafka-chaos-dashboard` ·
**23 panels**, six of which cannot work — see below

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
is installed, Kafka is up, or a benchmark is running.

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

> **One dishonest zero.** `Chaos engines running` ends in `or vector(0)`. If
> kube-state-metrics is not configured with custom-resource metrics for
> `ChaosEngine`, the series does not exist and the panel draws a reassuring
> zero — indistinguishable from "no experiment running". Same for the
> `KafkaChaosExperimentActive` alert, and for the `and on() count(…) == 0`
> guard on `KafkaBrokerRestartUnexpected`, which without that configuration is
> always true. If you rely on those alerts, confirm the series exists first.

## The RTO / RPO / data-integrity row does not work

It is at the bottom, **collapsed**, with a text panel at the top of it saying
so. This section is the long version.

### What is wrong

Its six panels read `kafka:chaos:*` recorded series. Those recording rules
exist — `charts/monitoring/templates/prometheus-chaos-rules.yaml` defines all
six, in the `kafka-chaos-rto-rpo` group. But every one of them reads a series
that **appears nowhere else in this repository**:

| Recorded series | Reads | Emitted by |
|---|---|---|
| `kafka:chaos:producer_rto_seconds` | `kates_integrity_result_producer_rto_seconds` | nothing |
| `kafka:chaos:consumer_rto_seconds` | `kates_integrity_result_consumer_rto_seconds` | nothing |
| `kafka:chaos:max_rto_seconds` | both of the above | nothing |
| `kafka:chaos:rpo_seconds` | `kates_integrity_result_rpo_seconds` | nothing |
| `kafka:chaos:data_loss_percent` | `kates_integrity_result_data_loss_percent` | nothing |
| `kafka:chaos:e2e_latency_ms` | `kafka_chaos_e2e_latency_ms` | nothing |
| `kafka:chaos:producer_throughput` | `kafka_chaos_producer_throughput_records_per_sec` | nothing |

Not the application, not the Helm charts, not Litmus, not any exporter this
repository ships. A `grep` for `kates_integrity_result` or `kafka_chaos_`
across the whole tree returns the rule file and the refactor plan, and nothing
else. A recording rule over a series that does not exist records nothing, so
all six panels are empty and always have been.

Four alerts sit on the same empty series — `KafkaRTOExceedsSLA`,
`KafkaRPOExceedsSLA`, `KafkaDataLossDetected`, `KafkaE2ELatencySpike`. They
install cleanly and can never fire. **An RTO SLA that is documented, alerted
on, and unobservable is worse than no RTO SLA**, which is why this is written
down rather than quietly deleted.

### Why the row is kept

Deleting six panels while leaving seven recording rules and four alerts in
place would hide the problem one level down instead of fixing it — the same
mistake in a different file. Keeping the row, collapsed, with the explanation
attached, leaves the gap visible and closable.

### What would have to exist

The values are already computed. `com.bmscomp.kates.domain.IntegrityResult`
carries `producerRto`, `consumerRto`, `maxRto`, `rpo`, `dataLossPercent`,
`lostRecords` and the rest, and the integrity verifier fills them in on every
run that carries an integrity check. They reach the REST API and the run
report. They are never registered with Micrometer.

Closing the gap means publishing them. Two ways, and the choice determines
whether the recording rules survive as written:

1. **Register them in the application**, the way phase 6 registered the four
   missing benchmark meters. That is the smaller change, but note that an
   integrity result belongs to a *finished* run, while every `kates_benchmark_*`
   meter is unregistered when the run ends — so these would have to be
   KatesMetrics-style (`test_type`-tagged, process-lifetime), not
   BenchmarkMetrics-style. The recording rules would then need their series
   names adjusted to match whatever is registered, because
   `kates_integrity_result_producer_rto_seconds` is not a name Micrometer
   would produce from any natural meter name.
2. **Ship an exporter or a Pushgateway job** that scrapes the report API and
   publishes the seven names the rules already read. That keeps the rules and
   the alerts exactly as they are.

Until one of those exists, **the resilience evidence on this board is the
*Kates during chaos* row**: throughput, latency and errors observed by the
workload while the fault was applied. That row does work, it reads series the
application actually publishes, and for most questions it is the better
answer anyway — it measures what a client experienced rather than what a
verifier concluded afterwards.

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
