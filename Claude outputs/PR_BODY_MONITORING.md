A plan, not an implementation. `docs/monitoring-enhancement-plan.md` audits what the platform monitors today for Kafka, Kafka Connect and MirrorMaker 2, and lays out the work in phases that each merge on their own.

## What the audit found

The stack is broader than it is sound. JMX exporter rules on every Strimzi CR, Kafka Exporter, PodMonitors, twelve Kafka dashboards, PrometheusRules for all three components — but nothing ever checked that the series the dashboards and alerts read are ones the rules can produce.

1. **Broker throughput is not exported at all.** The Kafka rule `kafka.server<type=(.+), name=(.+)><>Value` matches only Gauge attributes; `BrokerTopicMetrics` are Meters (`Count`, `OneMinuteRate`, …), so `BytesInPerSec` and friends never leave the broker — while every throughput panel queries `kafka_server_brokertopicmetrics_bytesinpersec`.
2. **Three Kafka alerts can never fire** — `KafkaISRShrinkRate`, `KafkaRequestHandlerSaturated`, `KafkaLogFlushLatencyHigh` — for the same reason. Two more (raft, certificate expiry) are marked *verify* because the names are plausible and unproven.
3. **JVM panels query a naming family the exporter does not emit** — three spellings are in play (`jvm_memory_used_bytes`, `java_lang_memory_*`, `jvm_memory_bytes_used`), and at least one is empty on every board.
4. **Two Connect alerts measure the wrong thing.** `KafkaConnectWorkerDown` (`connector_count == 0`) fires on every fresh install and cannot see a worker that is actually down; `KafkaConnectSourceLag` (`poll_total == 0`) tests a monotonic counter and fires exactly once per task, ever. There is no alert on connector state even though `status` is already exported as a label.
5. **Nothing routes an alert anywhere.** Alertmanager is deployed with no routes, receivers or inhibition; all 34 alerts end in its UI.
6. **Dashboard sprawl** — nine `kafka-*` boards in `charts/monitoring` plus three in the kafka-cluster chart, no canonical one, several sharing the empty panels from findings 1 and 3.
7. **MM2 defines `MirrorMaker2ReplicationLagHigh` twice** (per source and aggregate), so a silence or route on the name catches both.
8. **The defect class is structural.** MM2 avoided findings 1–4 by care, not by a gate — so the plan starts with the gate.

## The plan

- **Phase 0 — make what exists true.** Explicit Kafka rules modelled on Strimzi's reference `kafka-metrics.yaml` (Meters via `Count`/`OneMinuteRate`, Timers via `99thPercentile`, KRaft by attribute); one JVM family; rewrite the three dead alerts; Connect `WorkerDown` on `absent()`/`up`, `SourceLag` on `increase()`, plus `ConnectorNotRunning` from `kafka_connect_connector_metrics{status!="running"} == 1`, sink lag from Kafka Exporter and a DLQ-rate warning; rename MM2's per-source alert; retire the duplicate boards. One PR per chart.
- **Phase 1 — a metric contract, gated in CI.** `scripts/check-metric-contract.sh <chart>` derives the series each chart's rules can produce and fails on any dashboard or alert reference outside that set — static, seconds, would have caught findings 1–3. MM2's promtool/JSON/runbook-anchor gates extended to Kafka and Connect; a golden scrape in the integration run for what static analysis cannot see.
- **Phase 2 — runbooks and routing.** A runbook per alert with MM2's anchor gate; an Alertmanager route tree keyed on `severity` and `cluster`, a receiver that works on kind, values for Slack/PagerDuty, and critical-inhibits-warning.
- **Phase 3 — one board per component**, plus a shared JVM board, generated from a source so a rename is one edit, gated like the rules.
- **Phase 4 — depth**: SLO recording rules, quota/throttling metrics, tracing through the OTLP egress the Connect chart already renders, Kafka Exporter scope in the overlays, a decision on Cruise Control.

Each phase has a verification row in the doc and an order-of-work note: Phase 0 first, Phase 1 before Phase 3, Phases 2 and 3 independent, Phase 4 a backlog.

## Verification

- `check-book-style.sh` and `check-mermaid.sh` pass on the branch
- the file is a plain doc — no chart, workflow or script changes in this PR

🤖 Generated with [Claude Code](https://claude.com/claude-code)

https://claude.ai/code/session_014jXzsrpsEwtY63MeLoK3QN
