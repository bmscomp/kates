# Monitoring enhancement plan: Kafka, Kafka Connect, MirrorMaker 2

What the platform monitors today for each of the three, what is broken or
missing, and a phased plan to fix it — ordered so that the first phase makes
the existing dashboards and alerts true before any new ones are added.

Written against `main` at v1.22.0. Every "today" claim below was read from the
chart templates, not from a running cluster; the two places where a live
scrape is the only way to be sure are marked **verify**.

## What exists today

| | Kafka (`charts/kafka-cluster`) | Kafka Connect (`charts/connect-cluster`) | MirrorMaker 2 (`charts/mirror-maker2`) |
|---|---|---|---|
| JMX exporter rules | 13, broad catch-alls | 9 | 12, specific to MM2's MBeans |
| Extra exporters | Kafka Exporter (consumer lag), Cruise Control | — | — |
| Alerts | 17 | 8 | 9, with runbook links |
| Dashboards | 3 in the chart + 9 `kafka-*` in `charts/monitoring` | 1 (9 panels) | 1 (22 panels, per-source rows) |
| Scrape | PodMonitor for the operator and every Strimzi pod | PodMonitor | PodMonitor |
| Runbooks | none | none | every alert, anchors gated in CI |
| CI gates on rules / dashboards | none | none | JSON parse, runbook anchors; promtool only "when available", which on GitHub's runners is never |
| Alert routing | Alertmanager deployed, **no routes or receivers** | | |

MirrorMaker 2 has the discipline in form: its rules name the MBeans they
mean, its alerts carry runbooks, and CI proves the runbook anchors resolve.
Kafka and Connect predate that, and it shows. It did not save MirrorMaker 2
from the same class of defect — finding 7 — because the defect lives in what
the exporter does with a name, which no amount of care in the templates can
see. Only a scrape can.

## Findings

The list is ordered by how much of the current monitoring each one silently
disables.

### 1. Broker throughput is not exported at all

`charts/kafka-cluster/templates/metrics-configmap.yaml` exports `kafka.server`
MBeans through one rule:

```
kafka.server<type=(.+), name=(.+)><>Value
```

That matches Yammer *Gauges*. `BrokerTopicMetrics` — `BytesInPerSec`,
`BytesOutPerSec`, `MessagesInPerSec`, `TotalProduceRequestsPerSec`,
`FailedFetchRequestsPerSec` and the rest — are *Meters*, whose attributes are
`Count`, `OneMinuteRate`, `MeanRate`. There is no `Value`, so no rule matches
and nothing is exported. The only `Count` rule in the file is for
`kafka.network` `RequestMetrics`.

Meanwhile the dashboards query exactly those series:
`kafka_server_brokertopicmetrics_bytesinpersec` and `_bytesoutpersec` appear
in the monitoring chart's Kafka dashboards and in the kafka-cluster chart's
"Bytes In/Out Per Broker" panel. Those panels can only ever be empty.

### 2. Three Kafka alerts reference series no rule can produce

| Alert | Metric in `expr` | Why it never exists |
|---|---|---|
| `KafkaISRShrinkRate` | `kafka_server_replicamanager_isrshrinkspersec` | `IsrShrinksPerSec` is a Meter — no `Value` attribute |
| `KafkaRequestHandlerSaturated` | `kafka_server_kafkarequesthandlerpool_requesthandleravgidlepercent` | a Meter, same reason |
| `KafkaLogFlushLatencyHigh` | `kafka_log_logflushrateandduration_logflushrateandtimems` | the MBean is `kafka.log:type=LogFlushStats` (a Timer, no `Value`, no topic/partition labels), and the metric name in the expression matches no rule's output under any reading |

Two more need a live scrape to settle (**verify**): `KafkaRaftLeaderElectionRate`
and `KafkaRaftUncommittedRecords` read
`kafka_server_raft_metrics_<name>`, but KRaft's `raft-metrics` MBean registers
its values as *attributes* (`current-leader`, `commit-latency-avg`, …) rather
than as `name=` keys, which the rule `kafka.server<type=raft-metrics,
name=(.+)><>Value` would not match; and the two certificate alerts read
`strimzi_certificate_not_after`, a name that should be checked against what the
operator actually emits.

An alert that cannot fire is worse than no alert: the runbook says it is
covered.

### 3. JVM panels query a naming family the exporter does not emit

Dashboards use `jvm_memory_used_bytes` and `jvm_gc_collection_seconds_count`.
The JMX rules emit `java_lang_memory_*` and `java_lang_garbagecollector_*`
(**verify** — the JMX exporter also registers its own `jvm_memory_bytes_used`
family when run as an agent, which is a third spelling). Whichever is live, at
least one of the three is empty on every board.

### 4. Two Connect alerts measure the wrong thing

`KafkaConnectWorkerDown` is `kafka_connect_worker_metrics_connector_count == 0`.
A worker with no connectors deployed — every fresh install — fires a critical
"worker down" after three minutes; a worker that *is* down produces no series
at all, which `== 0` cannot see. It needs `absent()` on a worker series, or
`up == 0` on the PodMonitor target.

`KafkaConnectSourceLag` is
`kafka_connect_source_task_metrics_source_record_poll_total == 0`. That is a
monotonic counter: the alert fires for a task that has never polled and can
never fire again once it has. The intended check is
`increase(...[<window>]) == 0`.

There is also no alert on connector *state* (a connector `PAUSED` or `FAILED`
while its tasks report fine), no sink consumer-lag alert even though Kafka
Exporter already exports `kafka_consumergroup_lag` for the `connect-*` groups,
and nothing on dead-letter-queue writes.

### 5. Nothing routes an alert anywhere

`charts/monitoring` deploys Alertmanager and defines no routes, receivers or
inhibition rules of its own — not in `values.yaml`, not in either overlay, not
in a template. All 34 alerts across the three charts, working or not, end in
the Alertmanager UI. Critical does not inhibit warning, so a broker outage
would page (if anything paged) as two alerts.

### 6. Dashboard sprawl, and no owner

Nine `kafka-*` dashboards in `charts/monitoring/dashboards/` (`dashboard`,
`all-metrics`, `comprehensive`, `working`, `performance`, `perf-test`,
`perf-global`, `jvm`, plus `strimzi-operator`) and three more in the
kafka-cluster chart overlap heavily. A reader cannot tell which is canonical,
and several of them share the empty panels from findings 1 and 3.

### 7. MirrorMaker 2's connector alerts cannot match, and two of its series do not exist

Found by running the chart's exporter rules on a real worker (the Strimzi
`kafka:1.2.0-kafka-4.3.1` image, JMX exporter 1.6.0) — the first time anyone
had. Two behaviours of the exporter that the templates cannot show:

Kafka quotes an ObjectName value that carries a character outside
`[A-Za-z0-9._%-]`, and every connector name does (`source->target.…`). The
exporter copies whatever a capture group matched into the label, so a rule
written `connector=([^,]+)` labels every series
`connector="\"source->target.MirrorSourceConnector\""`, quotes included, and
no alert selecting `connector=~"<alias>->.*"` can match. `TaskFailed`,
`NoRecordsReplicated`, `CheckpointStalled`, `OffsetSyncStale` and
`HighErrorRate` were dead on every release.

The exporter's 1.x line reserves `_total` for counters and strips it from
anything else. Under a GAUGE rule, `source-record-write-total` is published
as `kafka_connect_source_task_metrics_source_record_write`, so the three
alerts and four panels reading `…_write_total`, and `RebalanceStorm` reading
`…_completed_rebalances_total`, read nothing. `NoRecordsReplicated` has an
`or vector(0)` fallback that reads a missing series as "no records", so it
did not go quiet — it fired permanently.

Neither is visible from the rules, and both survive `promtool check rules`.
An earlier draft of this document listed, as finding 7, a duplicated alert
name in the MM2 chart; that was a misreading (the two rules sit in mutually
exclusive branches on the replication policy), and this is what was there
instead.

### 8. The class of defect is structural

Findings 1–4 and 7 shipped because nothing checks that the series an alert
or a dashboard reads is one the rules can produce — and 7 shows that reading
the rules is not enough either, because the exporter has opinions about names
that only a scrape reveals. The plan therefore starts with a gate that does
both: a static check against a catalogue of the MBeans, and a live scrape
that keeps the catalogue honest.

## Plan

Four phases. Each is independently mergeable and leaves the platform better
than it found it; none depends on a later one.

### Phase 0 — Make what exists true

No new dashboards, no new alert families. Every panel and alert that exists
either works after this phase or is deleted.

**Kafka rules.** Replace the catch-alls with an explicit rule set modelled on
Strimzi's reference `kafka-metrics.yaml`, which is written for these exact
MBeans:

- `BrokerTopicMetrics` and the other Meters via `<>Count` (counter) and
  `<>OneMinuteRate` (gauge), per topic where the MBean has one
- `ReplicaManager` gauges *and* meters (`IsrShrinksPerSec`,
  `IsrExpandsPerSec` as counters)
- `LogFlushStats` as a Timer: `Count` plus `99thPercentile`
- `KafkaRequestHandlerPool` idle percent from its `OneMinuteRate`
- KRaft `raft-metrics` by attribute: `kafka.server<type=raft-metrics><>(.+)`
- `RequestMetrics` percentiles as today, which are correct
- one JVM family, chosen deliberately (see below), and used everywhere

**JVM naming.** Pick the JMX exporter's built-in `jvm_*` collectors and drop
the two `java.lang` rules, then point every dashboard's JVM panel at the same
names. One spelling, three charts.

**Kafka alerts.** Rewrite the three dead ones against the new series:

```promql
# ISR shrinking on any broker, sustained
rate(kafka_server_replicamanager_isrshrinks_total[5m]) > 0
# request handlers near saturation
kafka_server_kafkarequesthandlerpool_requesthandleravgidlepercent_oneminuterate < 0.2
# log flush p99 over budget
kafka_log_logflushstats_logflushrateandtimems{quantile="0.99"} > 1000
```

Settle the two **verify** items against a live `/metrics` and fix or delete.

**Connect alerts.** `WorkerDown` on `absent()` / `up`; `SourceLag` on
`increase()`; add `ConnectorNotRunning` — the status is already exported as a
label, so it is one line:

```promql
kafka_connect_connector_metrics{status!="running"} == 1
```

add sink lag from `kafka_consumergroup_lag{consumergroup=~"connect-.*"}`, which
Kafka Exporter already produces; add a DLQ-rate warning from
`task-error-metrics`.

**MM2.** Capture inside Kafka's quotes (`connector=\"?([^,\"]+)\"?`) on every
rule with a connector or client-id label, and give the `-total` attributes a
COUNTER rule ahead of the gauge rule so the suffix survives. Shipped with the
Phase 1 gate, since the gate is what found it.

**Dashboards.** Retire the `kafka-*` boards whose panels duplicate another
board's; keep one Kafka board in `charts/monitoring` and fix its queries. This
phase does not redesign any board — it makes the surviving ones render.

Verification: a kind deploy, then for each chart a script that fetches
`/metrics` from one pod and asserts every series named in its alerts and
dashboards is present. That script is the seed of Phase 1's gate.

### Phase 1 — A metric contract, gated in CI

What MM2 has by hand, every chart gets by machine.

- `scripts/check-metric-contract.sh <chart>`: simulate the JMX exporter over
  a hand-written catalogue of the MBeans the workload registers
  (`scripts/metric-contract/<chart>.yaml`) — first matching rule wins, `$N`
  substitution, the unsafe-character rewrite, `lowercaseOutputName`, and the
  1.x rule that only a COUNTER keeps `_total` — collect every series name
  referenced in the PrometheusRule and dashboard JSON, and fail on any
  reference outside what comes out, or any label that would carry Kafka's
  quotes. Static, no cluster, seconds. Run against the Kafka rules it reports
  findings 1–3; run against MM2's 0.4.0 rules it reports both halves of 7.
  MirrorMaker 2 is the first chart with a contract; Kafka and Connect need
  their catalogues written.
- Extend the MM2 gates — promtool on the rendered rules, dashboard JSON parses,
  runbook anchors resolve — to the Kafka and Connect charts, in `ci.yml`'s Helm
  Lint job.
- A golden scrape: capture `/metrics` from one broker, one Connect worker
  and one MM2 worker into an artifact and run the same check in `--scrape`
  mode against it — every reference must be observed, and the catalogue is
  diffed against reality both ways. That catches what static analysis cannot:
  a Kafka upgrade renaming an attribute, or an exporter behaviour nobody
  modelled (finding 7 was found exactly this way). MM2 has it as an on-demand
  job beside the migration e2e; the broker and Connect captures can ride the
  existing integration workflow.

### Phase 2 — Runbooks and routing

- Runbooks for every Kafka and Connect alert, in the book, with the anchor gate
  MM2 already has. Each runbook names the metric, the threshold, what to look
  at first, and what *not* to do (MM2's "do not cut over while lag is
  non-trivial" is the template).
- Alertmanager configuration in `charts/monitoring`: a route tree keyed on
  `severity` and `cluster`, a default webhook receiver that works on kind (a
  log sink), values for Slack and PagerDuty receivers, and inhibition so that a
  critical on a cluster mutes its warnings. Test hooks that post a synthetic
  alert and assert the receiver saw it.

### Phase 3 — One board per component

Replace the surviving dashboards with one canonical board each for Kafka,
Kafka Connect and MirrorMaker 2, plus a shared JVM board, each with the same
top row (is it up, is it lagging, is it erroring) so an operator reads all
three the same way. Keep the Kates benchmark, trend and chaos boards — they
are about Kates, not Kafka. Generate the boards from a source (jsonnet, or a
small Go generator in the CLI) so a metric rename is one edit, and gate the
output the same way as the rules.

### Phase 4 — Depth

Only after the above holds:

- Recording rules and SLOs: cluster availability (active controller and no
  offline partitions), produce/fetch p99 latency, MM2 end-to-end replication
  latency, Connect task availability. Kates' own trend dashboard can then read
  SLO burn rather than raw counters.
- Quota and throttling metrics (`kafka.server:type=ClientQuotaManager`), which
  the load tests exercise and nobody can currently see.
- Tracing: the Connect chart already renders OTLP egress and the repo ships
  Jaeger; wire a Connect and an MM2 worker to it and add a "trace this
  record" note to the runbooks.
- Kafka Exporter scope: `topicRegex: ".*"` and `groupRegex: ".*"` are fine on
  kind and expensive on a real cluster; expose them in the overlays with
  guidance.
- Cruise Control: the anomaly alert exists; decide whether the platform relies
  on it or drops it, and document either.

## Verification, per phase

| Phase | Proof |
|---|---|
| 0 | every panel on every surviving board renders on kind; every alert's series exists in a live scrape; promtool passes |
| 1 | the contract script fails on a deliberately broken rule and passes on `main`; CI runs it for all three charts |
| 2 | a synthetic critical reaches the default receiver on kind; every alert has a resolving runbook anchor |
| 3 | three boards, three JSON files, all generated, all gated |
| 4 | SLO recording rules evaluated in the integration run; a trace from a Connect record visible in Jaeger |

## Order of work and size

Phase 0 is the priority and is one PR per chart: Kafka (rules, alerts, board
fixes — the largest), Connect (alerts), MM2 (the exporter fixes, shipped
together with the Phase 1 gate and its own Phase 4 SLO because the gate is
what found them). Phase 1's remaining work — catalogues for Kafka and
Connect, the check in `ci.yml`'s Helm Lint job — should land before Phase 3,
so the new boards are born gated. Phases 2 and 3 are independent of each
other. Phase 4 is a backlog, not a commitment.

## Open questions

- Which receiver is real? Slack, PagerDuty, or only a webhook — the routing
  values should default to whatever the team will actually watch.
- Is Cruise Control used, or deployed because the chart could? Its dashboard
  and alert cost scrape volume on every cluster.
- Should Kates' own metrics (`BenchmarkMetrics`) join the contract? They are
  read by the trend board and are a fourth, unguarded family.
