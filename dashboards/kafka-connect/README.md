# Kafka Connect

**Delivered by** `charts/connect-cluster` · **uid** per release · **35 panels**

## The question it answers

A `KafkaConnect` reports `Ready` when its *workers* are up. That is all it
means. Every way a Connect pipeline can be broken — a failed connector, a dead
task inside a healthy connector, records being skipped on purpose, offsets
that have not committed for an hour, a group that has been rebalancing for
five minutes — leaves the CR green and the pods running.

This board is the answer to *"the connector says RUNNING, so why is the data
not arriving?"* It goes, top to bottom, in the order an incident asks:

1. Are the workers up, and is anything failed? (header)
2. Which connector, which task, which worker? (Connectors and tasks)
3. Is data moving, and how far behind is it? (Throughput)
4. What is failing, being retried, skipped or dead-lettered? (Errors and DLQ)
5. Will a restart reprocess? (Offset commits)
6. Is the group stable enough to do any of that? (Workers)
7. And if not, is the bottleneck below Connect? (Client path, collapsed)

## Who opens it

The person paged by one of `charts/connect-cluster`'s alerts, every one of
which has an input on this board, and the person who deployed a connector ten
minutes ago and wants to see it take. Both arrive knowing the connector's
name; every panel below the header is grouped by connector for that reason.

For **broker** health, **KRaft quorum** and **consumer-group lag** this is the
wrong board — those are `strimzi-kafka.json`, `strimzi-kraft.json` and the
Kafka Exporter's series, which the Strimzi operator's own dashboards cover
better than anything here could. Upstream's `strimzi-kafka-connect.json` is
*not* an alternative to this board: it reads 21 `kafka_*` names and exactly one
of them is producible by this chart's exporter rules.

## How it is scoped

Two template variables, `$namespace` and `$cluster`, and every expression on
the board carries both:

```promql
namespace="$namespace", strimzi_io_cluster="$cluster"
```

Until this refactor the board was rendered per release and carried
`namespace="<ns>", pod=~"<fullname>-connect-[0-9]+"` instead, because the
release name was the only thing in scope that identified the workers. It is
not any more. This chart's PodMonitor applies
`kafka-common.strimziRelabelings`, which labelmaps every `strimzi_io_*` pod
label onto the series; for a `KafkaConnect`, Strimzi sets
`strimzi.io/cluster` to the CR name, which this chart sets to the release
fullname. So `strimzi_io_cluster` selects exactly the pods that regex did.
Upstream's own Connect board selects the same way.

One residual: `strimzi_io_cluster` is set on `KafkaMirrorMaker2` operands too,
so a MirrorMaker 2 release whose name is *identical* to a Connect release's, in
the same namespace, would appear in `$cluster`. Two Helm releases cannot share
a name in one namespace, so this needs a deliberate `fullnameOverride`
collision to happen.

## Identity: why the uid has a digest in it

`dashboards/kafka-connect/dashboard.json` carries the base uid `kates-connect`
and the title `Kafka Connect`. The chart overwrites both, because this board
belongs to one release and one Grafana may hold several:

```
uid = kates-connect-<fullname, truncated to 16>-<sha256(fullname), truncated to 8>
```

Grafana keys a board by uid and allows 40 characters. The truncation alone is
not enough to discriminate: `kafka-connect-orders-eu` and
`kafka-connect-orders-us` truncate to the same 16 characters, and without the
digest the second release installed would silently overwrite the first
release's board. That is not hypothetical — it is the bug the MirrorMaker 2
boards shipped with until this refactor gave them the same digest suffix this
chart already had. It is load-bearing, and it has to survive any future change
to how this board is built.

`links` is overwritten too, from `alerts.runbookBaseUrl`: a fork that vendors
the runbook elsewhere repoints that value, and freezing the URL into the file
would quietly break the header link for them.

Note that `$namespace` and `$cluster` here are real **query** variables with a
dropdown, not the hidden `constant` variables the MirrorMaker 2 boards use for
the same job. They can be, because `strimzi_io_cluster` is on these series and
one board can serve every release in a Grafana. MirrorMaker 2 scopes itself by
a pod-name regex the chart injects, which no dropdown can offer.

## The sections

### Header — six stats

`Workers up` · `Connectors` · `Failed connectors` · `Failed tasks` ·
`Tasks running` · `Record errors /s`

The triage row. `Failed connectors` and `Failed tasks` sit side by side
because they are different failures and the second is far more common:
connector scope means `start()` or configuration, task scope means the data
path. `Tasks running` is the SLI the task-availability SLO burns against.

### Connectors and tasks

Which connector, which task, which worker — and, since this phase, *how much
of the time* each task was actually running. `Task status` is the only panel
on which a partially failed connector exists: a connector with four tasks and
one failure reports RUNNING everywhere else on this board and keeps moving
three quarters of its partitions.

`Sink partitions assigned` is the assignment equivalent for sinks. It should
equal the partition count of the subscribed topics; less than that is a task
holding nothing, which looks from every throughput panel like a quiet day.

### Throughput

Source poll → source write, sink read → sink put. Read each pair together:
the gap between them is what the transforms removed, and it is a different
problem from a backlog, which appears as `Source records in flight` climbing
or as lag.

### Errors and dead letter queue

Errors logged, then what those errors did to the records — failed, skipped, or
dead-lettered. **Skips are the mode nothing else catches:** with
`errors.tolerance=all` the pipeline is healthy, the throughput is normal, the
connector is green, and the destination is quietly missing rows.

### Offset commits

The "will a restart reprocess?" section. A task resumes from its last
committed offset, so a connector that has not committed for an hour reprocesses
an hour — duplicates downstream for a sink, re-read rows for a source.

### Workers

Rebalance rate, rebalance duration, whether one is in progress right now, how
long since the last one, startup failures, heap. No task processes data during
a rebalance, so a storm reads downstream as intermittent lag with nothing
failed and nothing logged, which is why it gets four panels rather than two.

### Client path (collapsed)

The producers and consumers underneath the tasks, plus the workers' own
clients for the internal offset, config and status topics. Collapsed because
it answers a question the rest of the board raises rather than one it starts
with: *the tasks are fine, so is Kafka the bottleneck?*

## The traps

### Worker, connector and task are three different scopes

Connect publishes the same idea at three levels and they disagree with each
other constantly. Getting the level wrong is how a board ends up reassuring.

| Scope | Beans | What a value means |
|---|---|---|
| **Worker** | `connect-worker-metrics`, `connect-worker-rebalance-metrics`, `connect-coordinator-metrics` | This pod's own view. Every worker publishes its own copy; aggregate across pods, and expect them to disagree during a rebalance. |
| **Connector** | `connector-metrics` | The connector's own state. Every worker in the group publishes the *same* connector, so aggregate with `max`, never `sum`. |
| **Task** | `connector-task-metrics`, `source-task-metrics`, `sink-task-metrics`, `task-error-metrics` | One task on one worker. `sum` across tasks for a connector total. |

Two consequences the panels lean on:

- `connector-running-task-count` and `connector-total-task-count` are
  **worker**-scope, so `Tasks running` sums over workers. A worker that
  vanishes removes its tasks from the numerator and the denominator at the same
  instant, and the ratio can read 1.0 for the length of a rebalance while tasks
  are in fact unassigned. Always read it beside `Workers up`.
- `kafka_connect_connector_metrics` is a synthetic `1` published once per
  connector **per attribute** — class, type, version and status are four
  series sharing one name and differing only in which label they carry. That
  is why `Connectors` counts `max by (connector)` rather than the series, and
  why `Connector status` filters `status!=""`: an absent label matches `""` in
  PromQL, so without the filter the table shows four rows per connector, three
  of them blank.

And because `status` is a label rather than a value, a connector that recovers
stops publishing its `failed` series instead of publishing it as zero. The
series goes stale, the count falls on its own — and a `status="failed"` query
over a time range shows the failure for as long as the series was fresh, not
as a step function.

### What "sink lag" actually measures

`kafka_connect_sink_task_metrics_sink_record_lag_max` — the `Sink lag (max
records behind)` panel — is **not** the backlog. It is the gap between the
offsets a sink task's consumer has reached and the offsets that task has
committed: lag *inside* the task, bounded by its in-flight window. A task that
stops committing drives it up. A destination that is hours behind does not.

The backlog an operator means by "sink lag" lives in two other places:

- `kafka_consumergroup_lag`, published by the **Kafka Exporter on the Kafka
  cluster**, is what `KafkaConnectSinkLag` alerts on
  (`alerts.thresholds.sinkLagRecords`). It is not on this board because these
  workers do not produce it, and it is the one that survives the worker going
  away.
- `Consumer lag (max)` in the Client path section, which the sink's own
  consumer measures against the end of the log. Same quantity, measured from
  the client rather than the broker, and it also covers the workers' consumers
  of the internal config and status topics — which belong to no connector and
  so appear in no sink panel at all.

### Why an error counter is typed GAUGE

`kafka_connect_task_error_metrics_total_errors_logged` is cumulative since the
task started. The exporter publishes it as a **GAUGE**. That is not a bug, and
`rate()` over it is still correct.

The rules in `files/metrics/connect-metrics.yaml` put a `COUNTER` rule ahead
of the `GAUGE` rule for every task bean, and it matches on a `-total`
*suffix*:

```yaml
- pattern: 'kafka.connect<type=(.+)-metrics, connector="?([^,"]+?)"?, task=(\d+)><>(.+-total):'
  type: COUNTER
```

Kafka wears the word at the front of these attributes — `total-errors-logged`,
`total-record-failures`, `total-records-skipped`, `total-retries` — so none of
them match, and they fall through to the `GAUGE` rule that catches `-logged`,
`-failures`, `-skipped` and `-retries`. The suffix rules exist for a real
reason (the exporter's 1.x line strips a trailing `_total` from anything not
published as a COUNTER, which is why `*-total` attributes need their own
COUNTER rule); this family simply is not spelled the way they match.

It does not matter to the query. Prometheus' type metadata does not change
what `rate()` computes, and `rate()`'s reset handling is exactly what a task
restart needs. It matters to the *reader*: do not "fix" these into `increase()`
of a gauge or, worse, read them as instantaneous values because the type says
gauge.

The ones that genuinely are counters and take `rate()` on their own terms:
`source-record-poll-total`, `source-record-write-total`,
`sink-record-read-total`, `sink-record-send-total`,
`completed-rebalances-total`, `connector-startup-failure-total`,
`task-startup-failure-total`.

### Several series are already rates — never rate() them

Kafka computes some of its own per-second rates over its own sampling window,
and the exporter publishes them as they are:

`offset-commit-completion-rate` · `record-send-rate` · `record-error-rate` ·
`record-retry-rate` · `records-consumed-rate`

`rate()` over any of these would be the rate of a rate. The same applies to
the ratios and averages — `running-ratio`, `pause-ratio`,
`offset-commit-failure-percentage`, `rebalance-avg-time-ms`,
`request-latency-avg` — which are already normalised over Kafka's window. When
those are aggregated per connector the board uses `max`, because averaging an
average across tasks hides the slow one.

`offset-commit-failure-percentage` is a fraction between 0 and 1 despite the
name, which is why its panel is in `percentunit` and why
`KafkaConnectOffsetCommitFailures` fires at `> 0`.

### `time-since-last-rebalance-ms` resets to zero

It is a millisecond gauge that Kafka zeroes at every rebalance. The drops are
the signal; do not `rate()` it and do not let a "counter reset" reading of it
smooth them away.

## Changing it

The JSON is generated. Edit `board.py`, then:

```bash
scripts/gen-dashboards.py
scripts/gen-dashboards.py --check
python3 scripts/check-dashboards.py
scripts/check-metric-contract.sh connect-cluster
helm template charts/connect-cluster -n connect \
  --api-versions monitoring.coreos.com/v1 | python3 scripts/check-dashboards.py --render
```

Name every series from `../METRICS.md` or from
`charts/connect-cluster/files/metrics/connect-metrics.yaml`, never from a JMX
bean name — the exporter renames as it publishes, and guessing is what put
eleven series that never existed on the boards this directory replaces. Every
panel needs a description saying what it shows, what it means when it moves,
and where the number comes from; `panels.py` will not build one without.
