# Grafana dashboard refactor

**Status:** plan, awaiting approval · **Base:** `main` at `c7fa18a` · **Branch:** `refactor/grafana-dashboards`

The repository ships 19 Grafana dashboards from six places. Nine of them are
deprecated, most of those are duplicates of each other, and the panels that make
the best-looking one worth keeping read metric names the exporter never emits.
This plan consolidates every dashboard into one documented directory at
`dashboards/`, deletes what is redundant, rebuilds the Kafka boards around KRaft,
and puts a gate in front of the class of mistake that produced the mess.

---

## 1. What exists today

### 1.1 Static JSON — `charts/monitoring/dashboards/*.json`, 13 files

One template globs them into a single ConfigMap. Nine are already marked
deprecated by `kates-monitoring.legacyKafkaDashboards`, gated behind
`legacyKafkaDashboards.enabled` (default `true`, documented as `false` in 1.3 and
gone in 2.0).

| Board | Panels | Distinct metrics | Genuinely dead |
|---|---:|---:|---:|
| `kafka-perf-global` | 24 | 25 | **7** |
| `kafka-all-metrics` | 13 | 15 | 1 |
| `kafka-comprehensive` | 9 | 8 | 0 |
| `kafka-working` | 7 | 6 | 1 |
| `kafka-dashboard` | 6 | 4 | 0 |
| `kafka-perf-test` | 6 | 3 | 0 |
| `kafka-performance` | 5 | 5 | 0 |
| `kafka-jvm` | 4 | 4 | 0 |
| `strimzi-operator-dashboard` | 8 | 9 | 3 |
| `kates-application` | 14 | 21 | 0 |
| `kates-benchmark` | 13 | 8 | **4** |
| `kates-trend` | 10 | 9 | 3 |
| `grafana-chaos` | 18 | 18 | 0 (but see §1.4) |

### 1.2 Chart-rendered dashboards, 6 more

`connect-cluster` (31 panels, built through `kafka-common.grafana.layout`),
`mirror-maker2` (37) and its migration board (19), `kates` (13), `kates` Kyverno
(9), `kates-chaos` (6). Plus nine upstream Strimzi boards that
`charts/strimzi-operator` enables by default.

### 1.3 The duplication

Across the nine legacy boards: **82 panels carrying 31 distinct concepts — 62%
redundant.** `kafka_controller_kafkacontroller_activebrokercount` appears in six
of them. A panel titled *Active Brokers* with an identical expression exists five
times; *Offline Partitions* four times. `kafka-jvm-dashboard.json` has **zero**
unique panels — every one of its four is a strict subset of `kafka-perf-global`'s
JVM row.

The same three scalars are rendered six times in three different panel types,
because `kafka-working` chose gauge and `kafka-performance` chose timeseries
where the rest chose stat.

### 1.4 The damage

Seven metric names in `kafka-perf-global` do not exist and cannot exist. Two root
causes, both from hand-guessing a name off a JMX bean instead of running the
rules:

- The exporter's PerSec rule strips `PerSec` and appends `_total`. The board
  reads `kafka_server_brokertopicmetrics_bytesinpersec`; the rules produce
  `kafka_server_brokertopicmetrics_bytesin_total`.
- The Percent rule inserts an underscore. The board reads
  `…requesthandleravgidlepercent`; the rules produce `…requesthandleravgidle_percent`.

**Those seven dead names are precisely the board's eight unique panels.** Every
panel that makes the most valuable-looking legacy board worth keeping — all the
throughput, all the ISR dynamics, request-handler idle — is empty on a real
cluster. The four boards with no dead metrics are the four that show almost
nothing.

Three more, at other levels:

- `strimzi-operator-dashboard.json`'s three Connect panels read Strimzi's
  upstream naming, not this repo's `connect-cluster` rules.
- Every legacy legend uses `{{zone}}`. `kafka-common.strimziRelabelings` labelmaps
  only `strimzi_io_*`, so the `zone` pod label never reaches the metrics. Every
  `({{zone}})` renders as `()`.
- `grafana-chaos`'s RTO/RPO row is dead one level down: its six `kafka:chaos:*`
  recording rules exist, but they read `kates_integrity_result_*` and
  `kafka_chaos_*` series that appear nowhere else in the repository.

### 1.5 The Kates boards have their own version of the problem

The application registers twelve meters. The boards read four names that are
registered nowhere: `kates_benchmark_records_total`, `kates_benchmark_latency_ms`,
`kates_benchmark_latency_ms_max`, `kates_benchmark_sla_violations`. All four are
also in the documentation, so **the docs and the dashboards agree with each other
and both disagree with the code.**

This cascades. Both of `kates-benchmark`'s template variables and `kates-trend`'s
one variable are `label_values(kates_benchmark_records_total, …)`. That series does
not exist, so every variable resolves empty and every panel filters on
`{run_id=~"$run_id"}`. **`kates-benchmark` is not 30% broken, it is 100% broken.**

Separately, `charts/kates`'s own board spells three Agroal metrics and two Kates
metrics differently from the monitoring chart's boards — one repository, three
spellings of the same metric.

### 1.6 Consistency

Three datasource conventions (`"Prometheus"`, `"prometheus"`, and none at all, the
last falling through to whatever Grafana's default is). Three schemaVersions (27,
38, 39). Two uid schemes, one of which collides: both MirrorMaker 2 boards
truncate the release name, so `…-release-one` and `…-release-two` produce the same
uid and overwrite each other. `connect-cluster` fixed exactly this with a
`sha256sum | trunc 8` suffix; MirrorMaker 2 never got the fix.

---

## 2. What the upstream Strimzi boards already do

This changes the shape of the answer, so it comes before the target set.

`charts/strimzi-operator` ships nine upstream boards, enabled by default, and
their label selectors match what this repo's PodMonitors emit. `strimzi-kafka.json`
covers broker health **strictly better than all nine legacy boards combined**: 30
panels, an 8-stat header, correct `_total` counters with `irate()`, correct
`_percent` idle gauges, per-listener connections, disk I/O, log size, the full
JVM/container/volume set, all scoped by templated cluster and broker.

`kafka-dashboard`, `kafka-comprehensive`, `kafka-working`, `kafka-all-metrics` and
`kafka-jvm` are subsets of it — several with dead names where upstream has live
ones.

But the reverse holds for Connect and MirrorMaker 2. Both charts ship their own
exporter rules with different name templates from Strimzi's examples:

- `strimzi-kafka-connect.json` reads 21 `kafka_*` names; **exactly one** is
  producible here.
- `strimzi-kafka-mirror-maker-2.json` reads 22; **exactly one** is producible here.

So the division of labour is asymmetric, and the plan states it plainly:

> **For Kafka, KRaft quorum identity, Cruise Control and consumer groups, upstream
> is authoritative and this repository adds only what upstream omits. For Connect
> and MirrorMaker 2, upstream is unusable and this repository's own boards are the
> only ones that work.**

---

## 3. The KRaft gap

Kafka 4.x is KRaft-only. **Not one of the repository's thirteen boards reads a
single `raft` or `brokermetadata` series.** The framing is entirely ZooKeeper-era:
broker counts, partitions, JVM — and nothing about the quorum that now runs the
cluster.

`strimzi-kraft.json` covers quorum *identity* well — state, leader, vote, epoch,
high watermark, append and fetch rates, commit latency. That is 13 of the 42
producible raft/controller/metadata series. It has no metadata-lag, no error
counters and no quorum-degradation signal.

Sixteen series are shown by nothing, upstream or local, and four of the chart's
own alerts fire into a board that shows none of their inputs:

| Series | What it tells an operator |
|---|---|
| `kafka_server_brokermetadatametrics_last_applied_record_lag_ms` | The KRaft health number on the broker side. A broker behind on metadata replay serves a stale view of topics, ACLs and leadership while looking perfectly healthy on every ZooKeeper-era panel. Alerted at 60 s; invisible on every board. |
| `…_metadata_load_error_count` | Metadata records the broker could not read — corruption, or a metadata-version bump gone wrong. |
| `…_metadata_apply_error_count` | Read but not applied: the broker is silently diverged from the cluster. |
| `…_last_applied_record_offset` | The metadata gap in records rather than milliseconds — survives a clock problem. |
| `kafka_server_raftmetrics_number_unknown_voter_connections` | Voters this node cannot reach. Alerted; on no board, so the page has nowhere to look. |
| `kafka_controller_kafkacontroller_fencedbrokercount` | KRaft-only, no ZooKeeper analogue. A fenced broker runs, passes probes and serves nothing. |
| `log_end_offset − high_watermark` | Uncommitted metadata on this node. A growing gap is the signature of a lost majority. |
| `kafka_server_raftmetrics_election_latency_max_total` | How long the last election froze metadata writes. (A max gauge despite the `_total`.) |
| `kafka_server_raftmetrics_poll_idle_ratio_avg` | Whether the raft IO thread itself is saturated — a different fix from a slow disk. |
| `kafka_server_raftchannelmetrics_request_latency_avg` | Round-trip between quorum peers; where a slow inter-AZ link shows before commit latency moves. |
| `kafka_controller_kafkacontroller_metadataerrorcount` | Controller-side metadata errors. Shown nowhere, alerted nowhere. |
| `kafka_controller_controllereventmanager_eventqueuetimems{quantile="0.99"}` | Controller queue saturation. |
| `kafka_controller_kafkacontroller_preferredreplicaimbalancecount` | Leadership skew — what a preferred-leader election fixes. |

Plus replication signals upstream omits (`atminisrpartitioncount`,
`offlinereplicacount`, `isrexpands_total`, `failedisrupdates_total`, the
per-partition `replicascount`/`insyncreplicascount` pair,
`replicationbytes{in,out}_total`), request-path detail (`requestqueuetimems`,
`requests_total{version}`, `errors_total{error}`), security signals
(`failed_authentication_*`, `connection_creation_*`), storage
(`logcleanermanager_uncleanable_partitions_count`) and the six tiered-storage
series.

**About 46 of the 96 producible broker/controller series are displayed by
nothing.** That is the substance of this refactor: the merged board turns *is a
leader elected?* into *is the quorum healthy, is metadata committing, is every
broker caught up, and is anything failing to apply it?*

---

## 4. The target set

Eleven boards, down from nineteen. Kafka goes from nine to two, as asked.

### `dashboards/kafka-kraft/` — **Kafka — KRaft Operations** (new)

The centrepiece. Sections: *Quorum & Metadata* (the 16 series above), *Cluster
Health* (controller count, offline/under-min-ISR, fenced brokers, unclean
elections), *Replication* (ISR shrink/expand paired, at-min-ISR, offline replicas,
replication bytes), *Request Path* (handler and network idle from the windowed
`_count_total` form, queue depths, per-type latency percentiles, errors by code),
*Storage* (log size, offline log dirs, flush latency, uncleanable partitions,
tiered storage when enabled), *Security & Connections* (auth failures, connection
churn per listener).

Explicitly **not** a rebuild of `strimzi-kafka.json`. The board links to it for
broker health and covers the gap.

### `dashboards/kafka-performance/` — **Kafka — Performance & Load Testing**

The only legacy content worth keeping: `kafka-perf-global` is the sole board with
per-topic templating (`$topic`) and per-partition log-end-offset, which upstream
does not have. Rebuilt on corrected names, absorbing `kafka-perf-test`,
`kafka-performance` and `kafka-working`'s topic panels.

### `dashboards/kafka-connect/` — **Kafka Connect**

Moved from `connect-cluster`. Expressions unchanged — the metric contract already
passes on all 34 references. **Content change: 31 panel descriptions**, which the
board has none of today.

### `dashboards/mirror-maker2/` and `dashboards/mirror-maker2-migration/`

Moved unchanged. Both are finished work: 37 and 19 panels, every one carrying a
description, several explaining the exact traps this exercise is about. These are
the model the others are brought up to.

### `dashboards/kates-application/`, `kates-trend/`, `kates-benchmark/`, `kates-chaos/`, `kyverno-security/`

The Kates set. Mutually disjoint (pairwise Jaccard 0.00), so they merge with
nothing. Moved, deduplicated against `charts/kates`'s competing spellings, and the
dead-metric decision in §8 applied.

### Deleted outright — 9 boards

`kafka-dashboard`, `kafka-comprehensive`, `kafka-jvm`, `kafka-all-metrics`,
`kafka-working`, `kafka-perf-test`, `kafka-performance` (content absorbed or
superseded by `strimzi-kafka.json`), and `strimzi-operator-dashboard` (its operator
half is `strimzi-operators.json`, its Connect half is connect-cluster's own board,
and a third of its names are dead).

`kafka-perf-global` is deleted as a file but survives as `kafka-performance/`.

---

## 5. Layout

```
dashboards/
  README.md                     index, conventions, how to add one
  METRICS.md                    every metric across every board, with meaning
  _lib/
    panels.py                   shared panel constructors
    layout.py                   the grid packer, moved out of kafka-common
  kafka-kraft/
    dashboard.json              the board
    README.md                   what it is for, how to read it, per-panel notes
    manifest.yaml               uid/title/target charts/conditional sections
  kafka-performance/ …
```

Each directory answers the user's request directly: the JSON, a README explaining
what the dashboard is for and what each panel means, and a manifest saying where
it is delivered.

`METRICS.md` is the cross-cutting reference — every series, its type, its labels,
one sentence of meaning, and the three naming traps that caused this refactor:

1. **`_max_total` is not a counter.** The exporter appends `_total` to anything a
   COUNTER rule names, and the KRaft rules type `.+-max` as COUNTER. So
   `kafka_server_raftmetrics_commit_latency_max_total` is a max gauge wearing a
   counter's name. Never `rate()` it.
2. **`_count_total` is the meter's Count, and the unit is not always a count.**
   `…requesthandleravgidlepercent_count_total` is cumulative nanoseconds.
3. **Percentiles are pre-computed gauges with a `quantile` label and no `_sum`.**
   Select the quantile; never `histogram_quantile`.

---

## 6. The sync mechanism

`dashboards/` is authoritative. `scripts/gen-dashboards.py` writes a copy into each
chart's `files/dashboards/`, and `--check` fails CI when a chart copy has drifted —
the pattern `gen-chart-table.sh` and `gen-version-matrix.sh` already establish.
Each chart's template becomes a `.Files.Get` wrapped by
`kafka-common.dashboardConfigMap`, which is exactly what the upstream Strimzi chart
does today and is the best in-tree precedent.

### 6.1 Six things have no Grafana-variable equivalent

Honest accounting, because it decides the generator's shape:

| Dependency | Where | Resolution |
|---|---|---|
| **uid and title** | 3 of 6 chart boards derive them per release | Generator injects both from `manifest.yaml`. The JSON is static in content, templated in identity. |
| **`$slo` gates a row plus 3 panels** | mirror-maker2, 38 panels ↔ 34 | Generator-time conditional. The panels read `mm2:*` recorded series that do not exist without the PrometheusRule, and Grafana has no "hide if absent". |
| **`range .Values.mirrors`** | one collapsed row per mirror | Generator-time repeat. A Grafana row `repeat` would be native, but the source bean carries no `source` label — only the checkpoint bean does. |
| **`$identity`** | swaps the lag expression's *shape*, not a label value | Generator-time variant. Under an identity policy there is no topic prefix to match at all. |
| **`$sep`** | inside a regex character class, `topic=~"east[.].*"` | Generator-time substitution. |
| **Thresholds** — `$th.*`, `$budget`, `$drained`, `$fresh` | `fieldConfig.thresholds.steps` and inside `< bool 5000` | Generator-time substitution. Grafana thresholds are not templatable, and `$drained` inside a PromQL comparison would break the promtool gate. |

What *does* become a variable: `$namespace` everywhere, `$job` on the Kates board,
and `$cluster` on connect-cluster via `strimzi_io_cluster`.

### 6.2 One prerequisite

`$cluster` cannot work on the MirrorMaker 2 boards yet. Its PodMonitor applies no
`kafka-common.strimziRelabelings`, so `strimzi_io_cluster` is absent from its
series. Connect-cluster has them. **Adding them to mirror-maker2's PodMonitor is a
scrape-config change and lands in its own phase, before the move.**

### 6.3 `kafka-common.grafana.layout`

One production caller (connect-cluster's dashboard) and one test harness. With
static JSON its `gridPos` is baked in and the helper has no job. It moves into
`dashboards/_lib/layout.py` rather than being deleted — the mirror-maker2 boards
hand-roll a `$y` cursor with leading commas, which is precisely what this helper
was written to replace, and the generator is where it finally gets applied to
both. `kafka-common.dashboardConfigMap` stays and widens: mirror-maker2, kates and
kates-chaos each hand-roll the same ConfigMap and should call it.

---

## 7. Gates

Six gates touch dashboards today; three are precise enough to break on a naive
move.

**Extend to everything in `dashboards/`** — the strictest gate in the repository,
`ci-mirror-maker2.yml`'s *"Dashboard JSON parses, and its layout holds"*: rejects
incomplete `gridPos`, `x + w > 24`, overlapping rectangles, a non-row panel with
no description, a target with an empty expression, a row not at `h:1,w:24,x:0`.
MirrorMaker 2 passes at 37/37 and 19/19 descriptions. **connect-cluster is 0/31,
kates 0/13, kates-chaos 0/6, kyverno 0/9** — those descriptions must be written
before the gate can go green, which is the same work the user asked for.

**Preserve exactly** — the cutover-gate test extracts the panel titled
`"Safe to cut over?"` by exact string and runs `promtool test rules` over five
input-series cases. Its inputs are all `namespace="kafka"`, so if that expression
becomes `namespace="$namespace"` every case stops matching. Either keep the
namespace literal on that one panel or teach the extraction step to substitute.

**Works unchanged** — the metric contract already strips Grafana variables before
matching, so `$namespace`/`$cluster`/`$job` are invisible to it.

**New gate: every metric in `dashboards/` must be producible.** Run
`contract.py`'s `Exporter`/`producible` over every expression in every board and
fail on a name no rule can emit. This is the gate that would have caught all
eleven dead names, and adding it is the durable outcome of this refactor.

Three beans are producible but missing from the catalogue — `LogEndOffset`,
`NumLogSegments`, `BrokerState` — so those panels are fine and the catalogue gets
three entries.

---

## 8. Two decisions for you

**The four missing Kates meters.** `kates_benchmark_records_total`,
`…_latency_ms`, `…_latency_ms_max`, `…_sla_violations` are read by two boards and
documented in the book, and registered nowhere in `BenchmarkMetrics.java`. Either
add the four meters to the application, or delete the panels and the doc rows.
Adding them is more work and makes two boards function; deleting is honest and
loses the benchmark board. **Recommendation: add them** — `kates-benchmark` exists
to show exactly these, and without them it has no reason to exist.

**The `zone` label.** Every legacy legend says `({{zone}})` and every one renders
`()`, because the relabeling only carries `strimzi_io_*`. Either add `zone` to
`kafka-common.strimziRelabelings` — zone-aware panels are genuinely useful on a
three-AZ cluster — or drop the legends. **Recommendation: add the relabeling**,
since the node pools already set the label and the rack-aware story is a selling
point of this chart.

---

## 9. Phases

Each leaves the repository green.

| # | Phase | Contents |
|---|---|---|
| 0 | Gates first | `gen-dashboards.py --check`, the producible-metric gate, the layout gate generalised. Wired to fail on today's tree so the bar is visible before anything moves. |
| 1 | Prerequisites | `strimziRelabelings` on mirror-maker2's PodMonitor; `zone` in the relabelings; three catalogue beans; `sidecar.dashboards.folderAnnotation`, without which the `grafana_folder` annotations three charts already emit stay inert. |
| 2 | Scaffold | `dashboards/` with `_lib/`, `README.md`, `METRICS.md`. Layout packer moved out of `kafka-common`. |
| 3 | Move the finished work | MirrorMaker 2's two boards and Kyverno — no content change, so they prove the mechanism honestly. |
| 4 | Connect | Move, add a `description` parameter to the panel helper, write 31 descriptions, add the rebalance-state and task-ratio panels upstream has and this does not. |
| 5 | The Kafka merge | Build `kafka-kraft` and `kafka-performance`. Delete eight legacy boards. Flip `legacyKafkaDashboards.enabled` to `false`. |
| 6 | The Kates set | Move four boards, reconcile the three spellings against `charts/kates`, apply the §8 decision. |
| 7 | Documentation | Rewrite `docs/book/09-observability.md` around the new set; per-board READMEs; retire what the old boards documented. |

## 10. What this is not

Not a Grafana version upgrade — schemaVersion is normalised to 39 across the set,
nothing more. Not a change to any alert or recording rule; the PrometheusRules are
already correct and are in fact where the correct metric names were found. Not a
rebuild of the upstream Strimzi boards, which stay authoritative for broker health,
quorum identity, Cruise Control and consumer lag.

## Appendix A — evidence

Every number here was derived by parsing the JSON with `python3`, rendering the
charts with `helm template`, and running `scripts/metric-contract/contract.py`'s own
`Exporter`/`producible` machinery over the vendored rule files and bean catalogues
— 130 producible series for `kafka-cluster`, 110 for `connect-cluster`, 93 for
`mirror-maker2`. No metric name in this document was guessed; that is the mistake
being repaired.
