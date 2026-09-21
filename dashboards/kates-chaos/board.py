"""Kates — Chaos: the Litmus experiment timeline, with the Kates run over it.

  (header)      engines running, experiments passed/failed, probe success
  Kafka         broker readiness, restarts, CPU and memory during the fault
  Experiments   how long each experiment ran
  Kates         what the benchmark underneath the chaos actually saw
  RTO / RPO     collapsed, and NOT WORKING — see below and the README

Ported from charts/monitoring/dashboards/grafana-chaos-dashboard.json. Every
panel now carries a description, the hard-coded `namespace="kafka"` and
`pod=~"krafter-pool-.*"` became template variables, and the RTO/RPO row was
demoted.

**The RTO/RPO row.** Its six panels read `kafka:chaos:*` recorded series.
Those recording rules do exist — `charts/monitoring/templates/`
`prometheus-chaos-rules.yaml` defines all six — but every one of them reads a
`kates_integrity_result_*` or `kafka_chaos_*` series, and those seven names
appear **nowhere else in this repository**. Nothing emits them: not the
application (`IntegrityResult` is computed in Java and returned over the REST
API, never registered with Micrometer), not the Helm charts, not Litmus, not
any exporter this repo ships. A recording rule over a series that does not
exist records nothing, so all six panels are empty and always have been, and
four SLA alerts in the same rule file sit on the same empty series and can
never fire.

The row is kept rather than deleted, because the rules and the alerts are
still installed and deleting only the panels would leave the repository
claiming an RTO SLA it cannot observe. It is **collapsed by default** and
opens on a text panel that says what has to exist first, so nobody has to
discover six blank stat tiles during an incident to find that out.
`dashboards/kates-chaos/README.md` has the full list of what an exporter
would have to publish.

**Where each series comes from.** Four different producers on one board, and
the board is useless without saying which:

  litmuschaos_*                  the Litmus chaos-exporter
  kube_customresource_*          kube-state-metrics with custom-resource
                                 metrics configured for ChaosEngine
  kube_pod_*, container_*        kube-state-metrics and cAdvisor
  kates_benchmark_*              the Kates application itself
"""

from __future__ import annotations

import panels as P
from layout import Row


# The Kafka namespace and the pods under test. `charts/monitoring` sets
# `kafkaNamespace: kafka` for its alert rules; this board asks instead,
# because a board that hard-codes the namespace it was written against is a
# board that works on exactly one cluster.
POD = 'namespace="$namespace", pod=~"$pod"'

# kube-state-metrics publishes a readiness series for every pod in every
# namespace it watches, so it is the one anchor on this board that exists
# whether or not chaos is installed, Kafka is up, or a benchmark is running.
#
# IT IS THE ANCHOR FOR `$pod` ONLY, AND THAT IS THE POINT. The same breadth
# that makes it a good anchor made it a terrible source for `$namespace`:
# "every namespace kube-state watches" is every namespace in the cluster, so
# the picker filled with cert-manager, kube-system, ingress-nginx and the rest,
# landed on whichever sorts first, and the board opened scoped to a namespace
# that has never run a benchmark or a chaos experiment. Every panel then read
# No data, correctly, about the wrong namespace — and the reader has no way to
# tell that from a board that is broken. `$namespace` is scoped to namespaces
# holding a ChaosEngine instead; `$pod` still resolves through this anchor,
# within whichever namespace `$namespace` settled on.
ANCHOR = "kube_pod_status_ready"

GREEN = [("green", None)]
RED_ABOVE_ZERO = [("green", None), ("red", 1)]


def variables(variant: str) -> list[dict]:
    return [
        P.query_var(
            "namespace",
            "label_values(kube_customresource_chaosengine_status_engine_status, "
            "namespace)",
            label="Kafka namespace",
            description=(
                "Namespaces that hold a ChaosEngine. kube-state-metrics "
                "publishes one series per engine, and Kates leaves its engines "
                "in place after a run, so empty here means either that no "
                "experiment has ever been created on this cluster, or that "
                "kube-state-metrics is running without custom-resource metrics "
                "for ChaosEngine — charts/monitoring configures them since "
                "1.6.0; another stack has to be told the resource's shape."),
        ),
        P.query_var(
            "pod",
            'label_values(%s{namespace="$namespace"}, pod)' % ANCHOR,
            label="Pods under test",
            multi=True, include_all=True, all_value=".*",
        ),
        P.query_var(
            "kates_job",
            "label_values(kates_benchmark_active_runs, job)",
            label="Kates scrape job",
            description=(
                "The Kates release's scrape job. Empty here means Prometheus "
                "is not scraping the application at all — check that its "
                "ServiceMonitor carries the label the Prometheus CR's "
                "serviceMonitorSelector asks for."),
        ),
    ]


# ── Header ─────────────────────────────────────────────────────────────────

def _header() -> list[dict]:
    return [
        P.stat(
            "Chaos engines running",
            "ChaosEngine custom resources currently in the `initialized` "
            "state — a fault is being applied right now. This is the panel "
            "that decides how to read every other panel on the board: a "
            "broker restart with this above zero is the experiment working, "
            "and the same restart with this at zero is an incident. The same "
            "distinction drives `KafkaBrokerRestartUnexpected` in "
            "prometheus-chaos-rules.yaml. Needs kube-state-metrics with "
            "custom-resource metrics for ChaosEngine, which charts/monitoring "
            "configures since 1.6.0. The zero "
            "fallback is anchored to the *unfiltered* ChaosEngine series "
            "rather than to this board's ANCHOR, and the distinction is the "
            "whole point: kube-state being up proves nothing here, because "
            "the failure this panel used to hide was kube-state running "
            "without the custom-resource collector configured. Anchoring on a "
            "ChaosEngine series of any status is what proves the collector "
            "works, so a cluster with engines but none initialized reads 0, "
            "and a cluster the collector cannot see reads No data.",
            P.targets(P.or_zero(
                'count(kube_customresource_chaosengine_status_engine_status'
                '{namespace="$namespace", status="initialized"} == 1)',
                'kube_customresource_chaosengine_status_engine_status'
                '{namespace="$namespace"}')),
            unit="short", thresholds=GREEN,
        ),
        P.stat(
            "Experiments passed",
            "Cumulative count of chaos experiments whose hypothesis held, "
            "from the Litmus chaos-exporter. Cumulative since the exporter "
            "started, so the useful reading is the change across a GameDay "
            "rather than the absolute number. Passed means Litmus' own probes "
            "were satisfied; it says nothing about what the Kates run "
            "underneath observed, which is the bottom row of this board.",
            P.targets("sum(litmuschaos_passed_experiments) or vector(0)"),
            unit="short", thresholds=GREEN,
        ),
        P.stat(
            "Experiments failed",
            "The same counter for experiments whose hypothesis did not hold. "
            "A failure here is not necessarily a cluster problem: Litmus "
            "marks an experiment failed when it could not inject the fault at "
            "all, which is a chaos-tooling problem and means the resilience "
            "claim for that window is unsupported rather than disproved. "
            "`ChaosExperimentFailed` alerts on this. The zero fallback is "
            "anchored on the passed counter beside it: the chaos-exporter "
            "publishes both from startup, so if one is missing the exporter "
            "is not being scraped and neither tile should claim a number.",
            P.targets(P.or_zero("sum(litmuschaos_failed_experiments)",
                                "litmuschaos_passed_experiments")),
            unit="short", thresholds=RED_ABOVE_ZERO,
        ),
        P.stat(
            "Probe success rate",
            "The mean of Litmus' per-experiment probe success percentage. "
            "Probes are the experiment's own assertions — an HTTP check, a "
            "command, a Prometheus query — so this is Litmus grading itself, "
            "and it is averaged across every experiment the exporter knows "
            "about, including ones that finished hours ago. Below 50% raises "
            "`ChaosProbeSuccessLow`. Treat it as a hint to open the Litmus UI, "
            "not as a measurement of the cluster.",
            P.targets("avg(litmuschaos_probe_success_percentage) or vector(0)"),
            unit="percent", thresholds=[("red", None), ("yellow", 50), ("green", 90)],
            min_value=0, max_value=100,
        ),
    ]


# ── Kafka under fault ──────────────────────────────────────────────────────

def _kafka() -> list[dict]:
    return [
        P.timeseries(
            "Broker pod readiness",
            "The `Ready` condition per pod, 1 or 0, from kube-state-metrics. "
            "During a pod-delete experiment this is the fault itself becoming "
            "visible, and the width of the trough is how long Kubernetes took "
            "to get the broker back — which is *not* RTO. RTO is how long the "
            "clients took to recover, and it is not measured anywhere in this "
            "repository; see the collapsed row at the bottom. A pod that "
            "never comes back raises `KafkaClusterNotReadyPostChaos`.",
            P.targets((
                "sum by (pod) (kube_pod_status_ready{%s})" % POD,
                "{{pod}}")),
            unit="short", w=12, min_value=0, max_value=1, decimals=0,
        ),
        P.timeseries(
            "Broker restarts",
            "Container restarts in the last five minutes, per pod. Under an "
            "active experiment this is expected; outside one it is "
            "`KafkaBrokerRestartUnexpected`, which is the alert the whole "
            "chaos rule file is built around. `increase()` over a counter "
            "that resets when the pod is replaced undercounts across a "
            "delete-and-recreate — the right direction to be wrong in, since "
            "it cannot report restarts that did not happen.",
            P.targets((
                "sum by (pod) (increase(kube_pod_container_status_restarts_total{%s}[5m]))" % POD,
                "{{pod}}")),
            unit="short", w=12,
        ),
        P.timeseries(
            "Broker CPU cores",
            "CPU seconds per second, per broker pod, from cAdvisor. The shape "
            "to look for is *after* the fault clears: a broker that comes back "
            "and then sits at a much higher CPU than its peers is replaying "
            "log segments or catching up as a follower, and it is not really "
            "recovered yet even though its readiness probe passes. "
            "`KafkaHighCPUPostChaos` fires on exactly that above 0.8 cores "
            "with no experiment running.",
            P.targets((
                "sum by (pod) (rate(container_cpu_usage_seconds_total"
                '{%s, container="kafka"}[5m]))' % POD,
                "{{pod}}")),
            unit="short", w=12,
        ),
        P.timeseries(
            "Broker memory",
            "Container memory usage per broker pod, cAdvisor. Included "
            "because a memory-hog experiment is one of the faults Litmus "
            "injects and because a broker recovering from a delete rebuilds "
            "its page cache, which shows up here as a slow climb back to the "
            "pre-fault level. A broker whose memory never returns to that "
            "level has not finished recovering, whatever readiness says.",
            P.targets((
                'sum by (pod) (container_memory_usage_bytes{%s, container="kafka"})' % POD,
                "{{pod}}")),
            unit="bytes", w=12,
        ),
    ]


# ── Experiment history ─────────────────────────────────────────────────────

def _experiments() -> list[dict]:
    return [
        P.timeseries(
            "Experiment duration",
            "How long each Litmus experiment ran, by chaos engine and chaos "
            "result — the chaos timeline this board is organised around. Line "
            "these steps up with the troughs in `Broker pod readiness` and "
            "the dips in the Kates row below: everything on this board is "
            "read relative to when the fault was being applied. This is the "
            "duration of the *injection*, not of the recovery.",
            P.targets((
                "litmuschaos_experiment_total_duration",
                "{{chaosengine_name}} — {{chaosresult_name}}")),
            unit="s", w=24,
        ),
    ]


# ── Kates under chaos ──────────────────────────────────────────────────────

def _kates() -> list[dict]:
    return [
        P.timeseries(
            "Benchmark throughput during chaos",
            "What the Kates run underneath the experiment was actually "
            "achieving, records/second, per run. This is the resilience "
            "evidence: Litmus grades whether it injected the fault, and this "
            "panel grades whether the workload survived it. Throughput that "
            "dips and recovers is a cluster that failed over; throughput that "
            "goes to zero and stays there is one that did not. Empty means no "
            "benchmark was running during the window — a chaos experiment "
            "with no load under it proves nothing.",
            P.targets((
                'kates_benchmark_throughput_rec_sec{job="$kates_job"}',
                "{{run_id}} ({{test_type}})")),
            unit="ops", w=8,
        ),
        P.timeseries(
            "Benchmark p99 latency during chaos",
            "The run's p99, selected from the pre-computed percentile gauges "
            "the engine publishes — a `quantile` label to select, never "
            "`histogram_quantile()`. The interesting number is how high the "
            "spike goes and how long it lasts, because that is what a client "
            "of the real system would have experienced. A latency spike with "
            "no throughput dip is a failover the producers rode out; both "
            "together is an outage.",
            P.targets((
                'kates_benchmark_latency_ms{job="$kates_job", quantile="0.99"}',
                "p99 {{phase}} {{run_id}}")),
            unit="ms", w=8,
        ),
        P.timeseries(
            "Benchmark errors during chaos",
            "Task failures per second in the run underneath the experiment. "
            "Errors here are the clearest statement that the fault reached "
            "the workload: not slower, actually failing. A pod-delete "
            "experiment that produces a latency spike and no errors is a "
            "cluster whose replication and client retries did their job, and "
            "that is the result the exercise is looking for.",
            P.targets((
                'sum by (run_id, phase) (rate(kates_benchmark_errors_total'
                '{job="$kates_job"}[1m]))',
                "{{run_id}} — {{phase}}")),
            unit="ops", w=8,
        ),
    ]


# ── RTO / RPO / data integrity ─────────────────────────────────────────────

def _rto_rpo() -> list[dict]:
    return [
        P.text(
            "What this row is and when it fills",
            "Prose, because the row is empty for two very different reasons "
            "and an operator opening it during an incident has to be able to "
            "tell them apart.",
            "**These panels fill only for a run that verified integrity.**\n\n"
            "They read the `kafka:chaos:*` recording rules in "
            "`charts/monitoring/templates/prometheus-chaos-rules.yaml`, which "
            "record the `kates_integrity_result_*` series the Kates "
            "application publishes from `IntegrityResult` — one set per run, "
            "tagged `run_id` and `test_type`. A plain load test never runs the "
            "verifier, so it publishes none of them and this row stays blank. "
            "That is the expected state, not a fault.\n\n"
            "So: **blank during an ordinary run** means integrity was not "
            "verified. **Blank during a chaos run** means either the verifier "
            "has not finished yet — it publishes from the first poll after it "
            "completes — or the Kates ServiceMonitor is not being scraped, "
            "which `dashboards/METRICS.md` and tutorial 13 both cover.\n\n"
            "The four SLA alerts on these same series — `KafkaRTOExceedsSLA`, "
            "`KafkaRPOExceedsSLA`, `KafkaDataLossDetected`, "
            "`KafkaE2ELatencySpike` — fire off the recording rules, so they "
            "are live exactly when this row is.\n\n"
            "The values are in **seconds**. `IntegrityResult` holds them as "
            "`Duration` and exposes milliseconds; the publisher divides, "
            "because a millisecond value under a `_seconds` name would make "
            "`KafkaRTOExceedsSLA` — which fires above 30 seconds — fire on "
            "every run that took longer than 30ms to recover.",
            h=6,
        ),
        P.stat(
            "Producer RTO",
            "How long producers took to resume after the fault, in seconds. "
            "Reads `kafka:chaos:producer_rto_seconds`, recorded from "
            "`kates_integrity_result_producer_rto_seconds`, published from "
            "`IntegrityResult.producerRto`. Red above 30s, the threshold "
            "`KafkaRTOExceedsSLA` uses, so the panel and the alert agree by "
            "construction.",
            P.targets("kafka:chaos:producer_rto_seconds"),
            unit="s", w=6, h=5, thresholds=[("green", None), ("red", 30)],
        ),
        P.stat(
            "Consumer RTO",
            "The same for consumers: how long before the consumer side was "
            "reading again. Reads `kafka:chaos:consumer_rto_seconds`. "
            "Producer and consumer RTO are tracked separately in "
            "`IntegrityResult` on purpose — a cluster can accept writes again "
            "well before its readers have caught up, and a single recovery "
            "number hides exactly that gap.",
            P.targets("kafka:chaos:consumer_rto_seconds"),
            unit="s", w=6, h=5, thresholds=[("green", None), ("red", 30)],
        ),
        P.stat(
            "Data loss",
            "Percentage of records produced and acknowledged that were never "
            "consumed. Reads `kafka:chaos:data_loss_percent`. "
            "`KafkaDataLossDetected` alerts on the same series above 0.1%, "
            "which is this panel's red threshold. With `acks=all` and a "
            "healthy ISR this should be zero; anything else is the headline "
            "finding of the run.",
            P.targets("kafka:chaos:data_loss_percent"),
            unit="percent", w=6, h=5,
            thresholds=[("green", None), ("red", 0.1)],
        ),
        P.stat(
            "RPO",
            "The recovery point objective actually observed: the width of the "
            "window whose writes did not survive, in seconds. Reads "
            "`kafka:chaos:rpo_seconds`. With `acks=all` and a healthy ISR "
            "this should be zero, and this panel is the evidence for that "
            "claim rather than an assumption of it.",
            P.targets("kafka:chaos:rpo_seconds"),
            unit="s", w=6, h=5, thresholds=[("green", None), ("red", 5)],
        ),
        P.stat(
            "Records lost",
            "The absolute count behind the data-loss percentage. Reads "
            "`kafka:chaos:lost_records`. A percentage alone cannot be acted "
            "on — 0.4% is a handful of records on a small run and a serious "
            "incident on a large one — so the count sits beside it.",
            P.targets("kafka:chaos:lost_records"),
            unit="short", w=6, h=5, thresholds=[("green", None), ("red", 1)],
        ),
        P.stat(
            "Duplicate records",
            "Records the consumer saw more than once. Reads "
            "`kafka:chaos:duplicate_records`. Duplicates after a fault are "
            "expected without idempotence and are not data loss; they are "
            "here so a run's verdict can distinguish the two rather than "
            "reading every anomaly as loss.",
            P.targets("kafka:chaos:duplicate_records"),
            unit="short", w=6, h=5,
        ),
        P.timeseries(
            "End-to-end latency during chaos",
            "The run's p99 latency across the fault, in milliseconds. Reads "
            "`kafka:chaos:e2e_latency_ms`, which records "
            "`max by (run_id, test_type) "
            "(kates_benchmark_latency_ms{quantile=\"0.99\"})`. That rule used "
            "to read `kafka_chaos_e2e_latency_ms`, a name nothing published "
            "and which would have been misplaced if it had — `kafka_` is the "
            "JMX exporter's namespace, and this is the workload's own "
            "measurement. `KafkaE2ELatencySpike` fires off this series above "
            "10s.",
            P.targets(("kafka:chaos:e2e_latency_ms", "p99 latency")),
            unit="ms", w=12,
        ),
        P.timeseries(
            "Producer throughput during chaos",
            "Records per second across the fault. Reads "
            "`kafka:chaos:producer_throughput`, which records "
            "`sum by (run_id, test_type) (kates_benchmark_throughput_rec_sec)`. "
            "Like the panel beside it, this rule used to read a "
            "`kafka_chaos_*` name nothing published; it now reads the "
            "throughput the benchmark engine already reports, so the number "
            "is the same one `Benchmark throughput during chaos` shows above, "
            "aggregated per run rather than per phase.",
            P.targets(("kafka:chaos:producer_throughput", "records/s")),
            unit="ops", w=12,
        ),
    ]


def build(variant: str) -> list[Row]:
    return [
        Row("", _header()),
        Row("Kafka under fault", _kafka()),
        Row("Experiment history", _experiments()),
        Row("Kates during chaos — the resilience evidence that works", _kates()),
        # Collapsed, but no longer because it cannot fill: it fills only for a
        # run that verified integrity, so most of the time an operator opening
        # this board is not looking at a chaos run and should not have eight
        # blank tiles between them and the rows that do have data.
        Row("RTO / RPO / data integrity — chaos and resilience runs only",
            _rto_rpo(), collapsed=True),
    ]
