# Kafka MirrorMaker 2 — the mirror board

**Is the mirror up, is it lagging, is it erroring — and if it is lagging,
which leg and which end?**

Open this one for a mirror that *runs*: a DR leg, a fan-in, anything watched
for months. For the hours between installing a mirror and cutting over, open
[`mirror-maker2-migration`](../mirror-maker2-migration/README.md) instead;
during a migration both are reasonable, because this board's error and worker
sections are the ones that explain why the other one is not going green.

Delivered by `charts/mirror-maker2` (`dashboard.enabled`) as a ConfigMap the
Grafana sidecar picks up. It needs `metrics.enabled` and something scraping
the workers.

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

## Variants

Two things change the panel list rather than a label value, so the generator
builds all four combinations and the chart picks one:

| Variant | Chosen when | Difference |
|---|---|---|
| `default` | SLO recorded, prefixing policy | 38 panels |
| `no-slo` | `alerts.enabled` or `alerts.slo.enabled` is false | 34 panels — the SLO row and its three panels are gone, and every row below shifts by 9 |
| `identity` | an identity replication policy | the per-source lag panel loses its topic-prefix matcher and says so in its description |
| `identity-no-slo` | both | |

The SLO row is a variant rather than a hidden panel because the `mm2:*` series
it reads do not exist without the PrometheusRule that records them, and
Grafana has no "hide this row if the series is absent". The identity split is
a variant because it changes the shape of an expression, not a label value:
under an identity policy the replicated topic keeps its source name, so there
is no prefix to match at all.

## What the chart injects, and what is frozen

The JSON here is static in content and templated in identity.
`charts/mirror-maker2/templates/dashboard.yaml` loads it with `.Files.Get`,
parses it, and sets:

| What | Why it cannot be in the file |
|---|---|
| `uid` | `kates-mm2-<name, 21>-<sha256 of the whole name, 8>`. Grafana keys a board by uid and allows 40 characters; the scheme this replaces truncated to 30, so two releases sharing a 30-character prefix overwrote each other's board. |
| `title` | One Grafana, several releases. |
| `links` | `alerts.runbookBaseUrl` — a fork that vendors the runbook elsewhere repoints it there. |
| `$namespace`, `$cluster`, `$pods` | Hidden `constant` variables the release fills in. Every expression names the variable; the file names no namespace, no release and no pod regex. |
| `$source` | A `custom` variable whose values are the `mirrors[].source.alias` list. The per-source row `repeat`s over it: one row is written here, Grafana draws one per mirror, and the aliases can be anything. |

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
copies under `charts/mirror-maker2/files/dashboards/` — `--check` fails on a
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
