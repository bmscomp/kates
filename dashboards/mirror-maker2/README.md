# Kafka MirrorMaker 2 — the mirror board

**Is the mirror up, is it lagging, is it erroring — and if it is lagging,
which leg and which end?**

Open this one for a mirror that *runs*: a DR leg, a fan-in, anything watched
for months. For the hours between installing a mirror and cutting over, open
[`mirror-maker2-migration`](../mirror-maker2-migration/README.md) instead;
during a migration both are reasonable, because this board's error and worker
sections are the ones that explain why the other one is not going green.

Delivered by `charts/monitoring` with every other board (`dashboards.enabled`
there) since mirror-maker2 0.11.0. It needs the chart's `metrics.enabled` and
something scraping the workers to have data.

## Who opens it, and when

| You are… | What you are looking at |
|---|---|
| Paged by `MirrorMaker2ReplicationLagHigh` | Header stats, then **Replication** |
| Paged by `MirrorMaker2CheckpointStalled` | **Offset translation** |
| Told "replication looks fine but records are missing" | **Errors** — specifically *records skipped* |
| Chasing a slow mirror | **The path a record takes**, collapsed |
| Running a fan-in and one leg is wrong | **Source: `<alias>`**, collapsed |
| Explaining last week to someone | **Replication SLO** — the only section with a memory |

## The sections, and what a move means

Read top to bottom: the sections are in the order an incident asks its
questions.

**Is it up, is it lagging, is it erroring.** Six stats. *Workers reporting*
counts a series every worker always has rather than `up`, because `up` carries
whatever job label the PodMonitor generated. *Records replicated /s* at zero
with the connectors RUNNING is the signature of a missing source-side ACL —
the connector cannot read, and says so nowhere the CR can see. The two clocks
are separate on purpose: replication lag is how far the *data* is behind,
translation age is how far the *positions consumers would resume from* are
behind, and a mirror can be current on one and hours behind on the other.

**Connector task states.** The panel that makes a partially failed connector
visible. A MirrorSourceConnector with three tasks, two running and one failed,
reports RUNNING on the CR and keeps replicating most partitions; the ones the
dead task owned simply stop. Failed and Unassigned are painted by their own
thresholds, so the table can be read at a glance from across a room.

**Replication.** Is data moving, how far behind, how much of it. *Polled vs
written* is the pair to read together: a persistent gap is records being
filtered, transformed away or dropped — not a backlog, which shows up next
door as *records in flight*. *Bytes replicated /s* reads `byte_rate` as is,
because it is already a rate and `rate()` over it would be the rate of a rate.

**Offset translation — what a failover would resume from.** Translation age
flat while records are flowing is the failure `MirrorMaker2OffsetSyncStale`
fires on: checkpoints are being written and not moving, so a failover resumes
consumers where they were hours ago. Zero checkpoints while the source
connector is still writing is `MirrorMaker2CheckpointStalled` — the data is on
the target and no consumer knows where to start reading it.

**Errors, retries and dead letters.** *Records skipped* is the one failure
mode a lag panel cannot show: the error was tolerated, the record dropped, the
connector left RUNNING, and the mirror is up to date precisely because it
threw records away. *Last error* filters `!= 0` because a task that has never
errored reports its timestamp as zero, and without the filter every healthy
connector would appear to have last failed in 1970.

**Replication SLO.** Only on the `default` and `identity` variants — see
below. The recorded `mm2:*` series, which carry `cluster` and `source` labels
so a status page can read the mirror without knowing the exporter's names.
`MirrorMaker2ReplicationSLOBurning` fires when *both* the 1h and the 5m error
ratio are above the red line, so the problem has to be sustained and still
happening.

**Workers.** The JVM and the rebalances under all of it. Tasks do not
replicate during a rebalance, so a storm reads downstream as intermittent lag
with no failed tasks — which is why *Rebalances /s* and *Time since last
rebalance* sit next to each other: the sawtooth in the second is the same
storm seen from the other side. The heap panel reads both spellings
(`jvm_memory_used_bytes` and `jvm_memory_bytes_used`) because the exporter
agent renamed it in its 1.x line and which one a worker image ships is not
something a panel should have to know.

**The path a record takes** *(collapsed)*. Where the mirror is slow, which
only matters once the sections above say that it is. Reading fast and writing
slowly puts the bottleneck on the target: watch the producer buffer heading
for zero and records in flight climbing with it. The client ids are not
guessable — MirrorMaker builds its own consumer rather than using Connect's,
so the source-side client is `…|replication-consumer` while the target-side
one is the ordinary `connector-producer-…`.

**Source: `<alias>`** *(collapsed, one row per mirror)*. Per-leg detail, so a
fan-in reads each source separately rather than as a sum that hides the dead
one. A source is selected three different ways here because the beans differ:
worker, task and error metrics carry the connector name, which starts with
`<alias>->`; the checkpoint bean carries `source` unconditionally; and the
source bean carries only the replicated topic name, which starts with the
alias under a prefixing policy and says nothing at all under an identity one.

## One shape, everywhere

Until mirror-maker2 0.11.0 the generator built four variants — `no-slo` cut
the Replication SLO row (the `mm2:*` series it reads do not exist without
the PrometheusRule that records them), `identity` reworded the per-source
lag panel (under an identity policy the replicated topic keeps its source
name, so there is no prefix to match) — and the chart picked one from its
values. Central delivery ships the one default shape to every cluster:

- the SLO row stays on the board and shows its styled no-data state where
  the recording rules are absent, rather than being cut;
- the per-source lag panel keeps its prefix matcher and its description says
  what an identity policy does to it.

## What is a variable, and what is frozen

The JSON is static — nothing is injected at delivery time. (Through 0.10.0
the chart rewrote `uid`, `title`, the runbook `links` and the variables'
`current` per release, and filled `$source` from `mirrors[].source.alias`;
Grafana's own variables carry all of that now.)

| What | How it works in the one file |
|---|---|
| `$source` | A `custom` variable — edit its options to your source aliases. The per-source row `repeat`s over it: one row is written here, Grafana draws one per alias. An alias is a *value*, not data, on purpose: a leg whose connectors never started publishes no series carrying it, and that is the leg you most want a row for. |
| `$namespace`, `$cluster` | Ordinary `query` variables; the dropdowns resolve the releases from the data and open unpreselected. |

## The variables

| Variable | Resolves from | Which scrape puts that label there |
|---|---|---|
| `$datasource` | Grafana's own datasource list, filtered to the `prometheus` plugin | — Defaults to the org's default datasource; see `_lib/panels.py`. |
| `$namespace` | `label_values(kafka_connect_worker_rebalance_metrics_completed_rebalances_total, namespace)` | `charts/mirror-maker2`'s PodMonitor. `kafka-common.strimziRelabelings` maps `__meta_kubernetes_namespace` → `namespace`; Prometheus Operator's own PodMonitor relabelings set it too. |
| `$cluster` | `label_values(…{namespace="$namespace"}, strimzi_io_cluster)` | The same PodMonitor's `labelmap __meta_kubernetes_pod_label_(strimzi_io_.+)`. Strimzi's `Labels.generateDefaultLabels` puts `strimzi.io/cluster: <CR name>` on every operand pod, and the chart names the KafkaMirrorMaker2 after the release. |
| `$source` | a `custom` variable; edit its options to your source aliases | — not data. |

The anchor is the rebalance counter rather than anything connector-shaped
because a worker publishes it from the moment it joins the group: a release
whose connectors have all failed still fills both pickers, which is when this
board is open. `dashboards/kafka-connect` uses the same anchor.

`$pods` used to sit beside them, holding `<release>-mirrormaker2-.*`. It is
gone. A pod-name regex is not something a picker can offer and not something a
person can type, and since phase 1 put `kafka-common.strimziRelabelings` on
this chart's PodMonitor it selects nothing `strimzi_io_cluster` does not.

**`cluster` is not `strimzi_io_cluster` on the SLO row.** The `mm2:*` series
are recorded, and the SLI rules aggregate with `max by (namespace)` — which
drops every label the scrape added — before the PrometheusRule attaches
`cluster` and `source` as rule labels. So the Replication SLO panels select
`cluster="$cluster"` and every other panel selects
`strimzi_io_cluster="$cluster"`. Both hold the same string, the
KafkaMirrorMaker2's name, because
`charts/mirror-maker2/templates/alerts.yaml` labels the rules with
`mirror-maker2.fullname` and Strimzi labels the pods with the CR name, which
is that same fullname.

**Frozen at generation time.** These are the chart's own defaults, baked into
the JSON, because a Grafana threshold is not templatable:

| Number | Value | Where |
|---|---|---|
| `alerts.thresholds.replicationLatencyMs` | 60000 | red on the lag stat and the lag-by-topic line; ÷6 = 10000 is the amber step; ×5 = 300000 is red on translation age |
| `alerts.thresholds.errorRatePerSecond` | 1 | red on both error panels |
| `alerts.thresholds.rebalancesPerSecond` | 0.1 | the rebalance-storm line |
| `alerts.slo.burnRate × (1 − alerts.slo.target)` | 0.144 | the error-budget line on the SLO panel |
| `replicationPolicy.separator` | `.` | inside the character class in `topic=~"$source[.].*"` |

A release that changes one of those values gets the alert it asked for; this
board keeps drawing the default line. Changing the line means editing
`board.py` and running `scripts/gen-dashboards.py`.

## Editing it

`board.py`, then `scripts/gen-dashboards.py`, then
`python3 scripts/check-dashboards.py`. Do not edit `dashboard.json` or the
copy under `charts/monitoring/dashboards/` — `--check` fails on a
hand-edited copy.

A new series needs a catalogue entry in
`scripts/metric-contract/mirror-maker2.yaml`; if the MBean and attribute are
not described there, `scripts/check-metric-contract.sh mirror-maker2` will say
the series cannot be produced, which is the answer you want when it genuinely
cannot.

Under `metrics.type: strimziMetricsReporter` the reporter publishes the same
numbers under different names and every panel here reads empty. The alerts
refuse that combination outright; the dashboard does not, because an empty
panel is visible to the person looking at it whereas an alert that cannot fire
is silent by construction.
