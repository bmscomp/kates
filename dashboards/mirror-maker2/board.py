"""The mirror board: a MirrorMaker 2 release that RUNS.

ONE BOARD, READ TOP TO BOTTOM. The sections answer, in order, the questions an
operator asks in an incident:

  1. Health          is it up, is it lagging, is it erroring — six numbers
  2. Task states     which connector lost a task (a partially failed connector
                     still reports RUNNING on the CR)
  3. Replication     is data moving, how far behind, how much of it
  4. Offset translation  will consumers have somewhere to resume from
  5. Errors          what is being dropped, retried, dead-lettered
  6. Replication SLO how much of the budget today has cost (when recorded)
  7. Workers         the JVM and the rebalances under all of it
  8. The path a record takes   source fetch → target produce, collapsed
  9. Source: <alias>  per-leg detail, collapsed, one row per mirror

Everything above the fold is release-wide; a fan-in release reads its legs in
the per-source rows and in the `connector` legend, which carries the alias.

WHAT THIS FILE DOES NOT DECIDE — see README.md for the whole list:

  * `$namespace`, `$pods` and `$cluster` are constants the RELEASE knows. The
    chart injects them into the generated JSON; the panels only ever name the
    variable.
  * the SLO section reads `mm2:*` recorded series, which exist only where the
    PrometheusRule that records them is installed — hence the `no-slo`
    variants rather than a panel that renders empty.
  * the per-source row `repeat`s over `$source`, whose values the chart takes
    from `.Values.mirrors`. One row is written; Grafana draws one per mirror.
  * thresholds are the chart's default `alerts.thresholds`, frozen here
    because Grafana thresholds are not templatable.

The metric names are the JMX exporter's (metrics-configmap.yml), and every one
of them is checked against the rules that produce it by
`scripts/check-metric-contract.sh mirror-maker2`. Under
metrics.type=strimziMetricsReporter the reporter names the same numbers
differently and these panels read empty; unlike the alerts, the dashboard does
NOT refuse to render there, because an empty panel is visible to the person
looking at it whereas an alert that cannot fire is silent by construction.
"""

from __future__ import annotations

from layout import Row
from panels import (constant_var, custom_var, stat, table, target, targets,
                    timeseries)

# ── Frozen at generation time ──────────────────────────────────────────────
# The chart's own defaults. A Grafana threshold is not templatable and a
# release that moves one of these gets the alert it asked for, not a line on
# a panel that has moved with it. README.md names every one of them.
LAG_MS = 60000                 # alerts.thresholds.replicationLatencyMs
LAG_WARN_MS = LAG_MS // 6      # the header stat's amber step
TRANSLATION_RED_MS = LAG_MS * 5
ERRORS_PER_SECOND = 1          # alerts.thresholds.errorRatePerSecond
REBALANCES_PER_SECOND = 0.1    # alerts.thresholds.rebalancesPerSecond
BUDGET = 0.144                 # alerts.slo.burnRate × (1 − alerts.slo.target)
SEP = "."                      # replicationPolicy.separator

NS = 'namespace="$namespace"'
PODS = 'pod=~"$pods"'
CLUSTER = 'cluster="$cluster"'


def variables(variant: str) -> list[dict]:
    return [
        constant_var("namespace", "kafka", label="Namespace"),
        constant_var("cluster", "mirror-maker2", label="Release"),
        constant_var("pods", "mirror-maker2-mirrormaker2-.*", label="Worker pods"),
        custom_var("source", ["source"], label="Source"),
    ]


def _health() -> Row:
    return Row("Is it up, is it lagging, is it erroring", [
        stat(
            "Workers reporting",
            "Pods whose metrics endpoint answered the last scrape. Counted from "
            "a series every worker always has, rather than from `up`, because "
            "`up` carries the job label the PodMonitor happens to generate and "
            "this does not.",
            targets(("count(kafka_connect_worker_metrics_task_count{%s})" % NS, "workers")),
            w=4, h=5, thresholds=[("red", None), ("green", 1)]),
        stat(
            "Tasks running",
            "Running tasks over total, across every connector of this release. "
            "Anything below 1 for longer than a rebalance is a task that died — "
            "the table below says which connector.",
            targets(("sum(kafka_connect_worker_metrics_connector_running_task_count{%s}) / "
                     "sum(kafka_connect_worker_metrics_connector_total_task_count{%s})"
                     % (NS, NS), "running")),
            w=4, h=5, unit="percentunit", min_value=0, max_value=1,
            thresholds=[("red", None), ("green", 1)]),
        stat(
            "Records replicated /s",
            "The one number that says the mirror is alive. Zero here with the "
            "connectors RUNNING is the signature of a missing source-side ACL: "
            "the connector cannot read, and says so nowhere the CR can see.",
            targets(("sum(rate(kafka_connect_source_task_metrics_source_record_write_total"
                     "{%s}[5m]))" % NS, "records/s")),
            w=4, h=5, unit="reqps", thresholds=[("red", None), ("green", 0.001)]),
        stat(
            "Replication lag (max)",
            "End-to-end latency, source append to target write. This is the "
            "number a cutover decision rests on: records written to the source "
            "within this window are not yet on the target.",
            targets(("max(kafka_connect_mirror_source_connector_replication_latency_ms_max"
                     "{%s})" % NS, "lag")),
            w=4, h=5, unit="ms",
            thresholds=[("green", None), ("yellow", LAG_WARN_MS), ("red", LAG_MS)]),
        stat(
            "Offset translation age (max)",
            "How old the newest translated consumer position is. The other "
            "clock: data can be current while the positions consumers would "
            "resume from are hours behind, and a failover then replays "
            "everything since.",
            targets(("max(kafka_connect_mirror_checkpoint_connector_checkpoint_latency_ms_max"
                     "{%s})" % NS, "translation age")),
            w=4, h=5, unit="ms",
            thresholds=[("green", None), ("yellow", LAG_MS), ("red", TRANSLATION_RED_MS)]),
        stat(
            "Errors /s",
            "Errors logged by the tasks of every connector in this release. Red "
            "is the MirrorMaker2HighErrorRate threshold. A mirror can absorb an "
            "error on every record and keep reporting RUNNING.",
            targets(("sum(rate(kafka_connect_task_error_metrics_total_errors_logged"
                     "{%s}[5m]))" % NS, "errors/s")),
            w=4, h=5, unit="reqps",
            thresholds=[("green", None), ("red", ERRORS_PER_SECOND)]),
    ])


def _task_states() -> Row:
    counts = [("running", "running"), ("failed", "failed"), ("paused", "paused"),
              ("unassigned", "unassigned"), ("restarting", "restarting"),
              ("total", "total")]
    queries = [
        target("sum by (connector) (kafka_connect_worker_metrics_connector_%s_task_count"
               "{%s})" % (metric, NS), legend, instant=True, fmt="table",
               ref=chr(ord("A") + i))
        for i, (metric, legend) in enumerate(counts)
    ]
    cell = lambda colour: [  # noqa: E731 — a cell painted by its own threshold
        {"id": "thresholds", "value": {"mode": "absolute", "steps": [
            {"color": "green", "value": None}, {"color": colour, "value": 1}]}},
        {"id": "custom.cellOptions", "value": {"type": "color-background"}},
    ]
    return Row("Connector task states", [
        table(
            "Tasks by connector and state",
            "The panel that makes a PARTIALLY failed connector visible. A "
            "MirrorSourceConnector with 3 tasks, 2 running and 1 failed, reports "
            "RUNNING on the CR and keeps replicating most partitions — the ones "
            "the dead task owned simply stop, silently. One row per connector, "
            "and the connector name carries the source alias, so a fan-in "
            "release shows which leg lost a task.",
            queries, w=24, h=8,
            transformations=[
                {"id": "merge", "options": {}},
                {"id": "organize", "options": {
                    "excludeByName": {"Time": True, "Time 1": True, "Time 2": True,
                                      "Time 3": True, "Time 4": True, "Time 5": True,
                                      "Time 6": True},
                    "renameByName": {"connector": "Connector", "Value #A": "Running",
                                     "Value #B": "Failed", "Value #C": "Paused",
                                     "Value #D": "Unassigned", "Value #E": "Restarting",
                                     "Value #F": "Total"}}},
            ],
            overrides=[
                {"matcher": {"id": "byName", "options": "Failed"},
                 "properties": cell("red")},
                {"matcher": {"id": "byName", "options": "Unassigned"},
                 "properties": cell("yellow")},
            ]),
    ])


def _replication() -> Row:
    return Row("Replication", [
        timeseries(
            "Records written to target",
            "Per connector, so a fan-in release shows each leg. The checkpoint "
            "connector appears here too — its records are the checkpoints "
            "themselves, which is how the offset-translation section below can "
            "tell 'stopped emitting' from 'emitting the same position'.",
            targets(("sum by (connector) (rate("
                     "kafka_connect_source_task_metrics_source_record_write_total"
                     "{%s}[5m]))" % NS, "{{connector}}")),
            w=8, unit="reqps"),
        timeseries(
            "Replication lag by topic",
            "End-to-end latency per replicated topic. The red line is the "
            "MirrorMaker2ReplicationLagHigh threshold. Watch this go to zero "
            "before a cutover, not merely low.",
            targets(("max by (topic) ("
                     "kafka_connect_mirror_source_connector_replication_latency_ms_max"
                     "{%s})" % NS, "{{topic}}")),
            w=8, unit="ms", threshold_style="line",
            thresholds=[("green", None), ("red", LAG_MS)]),
        timeseries(
            "Record age at source",
            "How far behind the source's log the fetchers are reading, before "
            "anything is written to the target. Rising here with flat "
            "replication lag means the source is producing faster than the "
            "mirror reads it; the two rising together is a target-side problem.",
            targets(("max by (topic) (kafka_connect_mirror_source_connector_record_age_ms_max"
                     "{%s})" % NS, "{{topic}}")),
            w=8, unit="ms"),
        timeseries(
            "Bytes replicated /s",
            "byte-rate is already a rate (bytes per second over Kafka's own "
            "window), so it is read as is; rate() over it would be the rate of "
            "a rate.",
            targets(("sum by (topic) (kafka_connect_mirror_source_connector_byte_rate"
                     "{%s})" % NS, "{{topic}}")),
            w=8, unit="Bps"),
        timeseries(
            "Polled vs written",
            "What the tasks read from the source against what they wrote to the "
            "target. A persistent gap is records being filtered, transformed "
            "away or dropped — not a backlog, which shows up in the panel to "
            "the right.",
            targets(("sum(kafka_connect_source_task_metrics_source_record_poll_rate"
                     "{%s})" % NS, "polled"),
                    ("sum(kafka_connect_source_task_metrics_source_record_write_rate"
                     "{%s})" % NS, "written")),
            w=8, unit="reqps"),
        timeseries(
            "Records in flight",
            "Records polled from the source and not yet acknowledged by the "
            "target, per connector. This is the mirror's own backlog: it grows "
            "when the target's produce path is the bottleneck, which the "
            "collapsed clients section reads directly.",
            targets(("sum by (connector) ("
                     "kafka_connect_source_task_metrics_source_record_active_count"
                     "{%s})" % NS, "{{connector}}")),
            w=8, unit="short"),
    ])


def _translation() -> Row:
    return Row("Offset translation — what a failover would resume from", [
        timeseries(
            "Translation age by source and group",
            "How old each consumer group's translated position is. Flat while "
            "records are flowing is the failure MirrorMaker2OffsetSyncStale "
            "fires on: checkpoints are being written but not moving, so a "
            "failover resumes consumers where they were hours ago.",
            targets(("max by (source, group) ("
                     "kafka_connect_mirror_checkpoint_connector_checkpoint_latency_ms_max"
                     "{%s})" % NS, "{{source}} / {{group}}")),
            w=8, unit="ms"),
        timeseries(
            "Checkpoints emitted /s",
            "The checkpoint connector's own write rate. Zero here while the "
            "source connector is still writing is MirrorMaker2CheckpointStalled "
            "— consumers will have the data on the target and no position to "
            "resume from, which is what turns a clean cutover into a full "
            "replay.",
            targets(("sum by (connector) (rate("
                     "kafka_connect_source_task_metrics_source_record_write_total"
                     '{%s,connector=~".*MirrorCheckpointConnector"}[5m]))' % NS,
                     "{{connector}}")),
            w=8, unit="reqps"),
        timeseries(
            "Offset commit success",
            "Each task's own offset commits — the mirror's record of where IT "
            "has got to, which is separate from the translated consumer "
            "positions above. Sustained below 1 means the task will redo work "
            "after a restart.",
            targets(("min by (connector) ("
                     "kafka_connect_connector_task_metrics_offset_commit_success_percentage"
                     "{%s})" % NS, "{{connector}}")),
            w=8, unit="percentunit", min_value=0, max_value=1),
    ])


def _errors() -> Row:
    return Row("Errors, retries and dead letters", [
        timeseries(
            "Errors logged /s",
            "Per connector, with the MirrorMaker2HighErrorRate threshold drawn. "
            "This is the series the alert reads and, until this board had a "
            "panel for it, the only place it existed was the alert.",
            targets(("sum by (connector) (rate("
                     "kafka_connect_task_error_metrics_total_errors_logged{%s}[5m]))" % NS,
                     "{{connector}}")),
            w=6, unit="reqps", threshold_style="line",
            thresholds=[("green", None), ("red", ERRORS_PER_SECOND)]),
        timeseries(
            "Records errored, failed, skipped /s",
            "What the errors did to the records. Skipped means tolerated and "
            "dropped — the mirror stays RUNNING and the target is quietly "
            "missing those records, which is the one error mode a lag panel "
            "cannot show.",
            targets(("sum(rate(kafka_connect_task_error_metrics_total_record_errors"
                     "{%s}[5m]))" % NS, "errors"),
                    ("sum(rate(kafka_connect_task_error_metrics_total_record_failures"
                     "{%s}[5m]))" % NS, "failures"),
                    ("sum(rate(kafka_connect_task_error_metrics_total_records_skipped"
                     "{%s}[5m]))" % NS, "skipped")),
            w=6, unit="reqps"),
        timeseries(
            "Retries and dead letters /s",
            "Retries are the mirror absorbing a transient failure; dead-letter "
            "writes are it giving up on a record. A rising DLQ produce-failure "
            "line means even the dead-letter path is not working, and those "
            "records are gone.",
            targets(("sum(rate(kafka_connect_task_error_metrics_total_retries{%s}[5m]))" % NS,
                     "retries"),
                    ("sum(rate("
                     "kafka_connect_task_error_metrics_deadletterqueue_produce_requests"
                     "{%s}[5m]))" % NS, "dead-letter writes"),
                    ("sum(rate("
                     "kafka_connect_task_error_metrics_deadletterqueue_produce_failures"
                     "{%s}[5m]))" % NS, "dead-letter write failures")),
            w=6, unit="reqps"),
        stat(
            "Last error",
            "When each connector last logged an error. `!= 0` is deliberate: a "
            "task that has never errored reports the timestamp as zero, and "
            "without the filter this panel would say every healthy connector "
            "last failed in 1970.",
            targets(("max by (connector) ("
                     "kafka_connect_task_error_metrics_last_error_timestamp{%s} != 0)" % NS,
                     "{{connector}}")),
            w=6, h=8, unit="dateTimeFromNow", text_mode="value_and_name",
            graph_mode="none"),
    ])


def _slo() -> Row:
    return Row("Replication SLO", [
        timeseries(
            "Time over the latency objective",
            "The recorded error ratios: the fraction of the last hour, and of "
            "the last five minutes, that replication latency spent above "
            "%dms. MirrorMaker2ReplicationSLOBurning fires when BOTH are above "
            "the red line, so the problem has to be sustained and still "
            "happening." % LAG_MS,
            targets(("mm2:slo_replication_latency:error_ratio_rate1h{%s,%s}" % (NS, CLUSTER),
                     "1h — {{source}}"),
                    ("mm2:slo_replication_latency:error_ratio_rate5m{%s,%s}" % (NS, CLUSTER),
                     "5m — {{source}}")),
            w=8, unit="percentunit", min_value=0, max_value=1,
            threshold_style="line", thresholds=[("green", None), ("red", BUDGET)]),
        timeseries(
            "Tasks running (recorded)",
            "mm2:tasks_running:ratio, per source rather than per connector: the "
            "SLI a status page should read, since it needs no knowledge of the "
            "exporter's names.",
            targets(("mm2:tasks_running:ratio{%s,%s}" % (NS, CLUSTER), "{{source}}")),
            w=8, unit="percentunit", min_value=0, max_value=1),
        timeseries(
            "Replication and translation (recorded)",
            "mm2:replication_latency_ms:max and mm2:checkpoint_latency_ms:max, "
            "per source. The same two clocks as the header stats, in the form "
            "the burn-rate rule and any external consumer read them.",
            targets(("mm2:replication_latency_ms:max{%s,%s}" % (NS, CLUSTER),
                     "lag — {{source}}"),
                    ("mm2:checkpoint_latency_ms:max{%s,%s}" % (NS, CLUSTER),
                     "translation — {{source}}")),
            w=8, unit="ms"),
    ])


def _workers() -> Row:
    return Row("Workers", [
        timeseries(
            "JVM heap used",
            "Both spellings: the JMX exporter agent renamed jvm_memory_bytes_used "
            "to jvm_memory_used_bytes in its 1.x line, and which one a worker "
            "image ships is not something a panel should have to know. A mirror "
            "under memory pressure slows down long before it crashes, so this "
            "shows up as lag rather than as an error.",
            targets(('sum by (pod) ((jvm_memory_used_bytes{area="heap",%s,%s} or '
                     'jvm_memory_bytes_used{area="heap",%s,%s}))'
                     % (NS, PODS, NS, PODS), "{{pod}}")),
            w=6, unit="bytes"),
        timeseries(
            "GC time /s",
            "Seconds spent collecting per second, per collector. Approaching 1 "
            "means the worker is spending its life in GC, which reads "
            "downstream as replication lag with no failed tasks.",
            targets(("sum by (pod, gc) (rate(jvm_gc_collection_seconds_sum{%s,%s}[5m]))"
                     % (NS, PODS), "{{pod}} / {{gc}}")),
            w=6, unit="percentunit"),
        timeseries(
            "Rebalances /s",
            "Tasks do not replicate during a rebalance, so a storm reads as "
            "intermittent lag with no failed tasks. The red line is the "
            "MirrorMaker2RebalanceStorm threshold; every mirror in the release "
            "is affected at once, which is why this is not per source.",
            targets(("sum(rate("
                     "kafka_connect_worker_rebalance_metrics_completed_rebalances_total"
                     "{%s}[5m]))" % NS, "rebalances/s")),
            w=6, unit="reqps", threshold_style="line",
            thresholds=[("green", None), ("red", REBALANCES_PER_SECOND)]),
        timeseries(
            "Time since last rebalance",
            "A sawtooth here is the same storm as the panel to its left, seen "
            "from the other side: each drop to zero is a rebalance, and the "
            "height of the teeth is how long the workers managed to stay still.",
            targets(("min(kafka_connect_worker_rebalance_metrics_"
                     "time_since_last_rebalance_ms{%s})" % NS, "since last rebalance")),
            w=6, unit="ms"),
        timeseries(
            "Connectors and tasks assigned",
            "What the workers believe they are running. A step down here without "
            "a corresponding step in the task-state table is an assignment the "
            "cluster lost rather than a task that failed.",
            targets(("sum(kafka_connect_worker_metrics_connector_count{%s})" % NS,
                     "connectors"),
                    ("sum(kafka_connect_worker_metrics_task_count{%s})" % NS, "tasks")),
            w=12, unit="short"),
        timeseries(
            "Startup failures",
            "Connectors and tasks that failed to start, as a rate. A worker that "
            "crash-loops on startup never reaches the task-state table at all, "
            "and this is where that shows.",
            targets(("sum(rate("
                     "kafka_connect_worker_metrics_connector_startup_failure_total"
                     "{%s}[5m]))" % NS, "connector startup failures/s"),
                    ("sum(rate(kafka_connect_worker_metrics_task_startup_failure_total"
                     "{%s}[5m]))" % NS, "task startup failures/s")),
            w=12, unit="reqps"),
    ])


def _clients() -> Row:
    """Collapsed: it answers WHERE the mirror is slow, which only matters once
    the sections above say that it is.

    The client ids are not guessable. MirrorMaker builds its own consumer
    rather than using Connect's, so the source-side client is
    `<source>-><target>|<connector>|replication-consumer` while the target-side
    one is the ordinary `connector-producer-<connector>-<task>`. Both patterns
    were read off a live worker.
    """
    return Row("The path a record takes — source fetch, target produce", [
        timeseries(
            "Read from source /s",
            "The replication consumer's own fetch rate. Compare with 'Records "
            "written to target': reading fast and writing slowly puts the "
            "bottleneck on the target.",
            targets(("sum by (topic) (kafka_consumer_fetch_manager_records_consumed_rate"
                     '{%s,client_id=~".*replication-consumer"})' % NS, "{{topic}}")),
            w=6, unit="reqps"),
        timeseries(
            "Read from source (bytes/s)",
            "The same fetch in bytes. A byte rate at the source's quota ceiling "
            "with a flat record rate is throttling rather than a stuck mirror.",
            targets(("sum by (topic) (kafka_consumer_fetch_manager_bytes_consumed_rate"
                     '{%s,client_id=~".*replication-consumer"})' % NS, "{{topic}}")),
            w=6, unit="Bps"),
        timeseries(
            "Produce to target /s",
            "Send rate against error and retry rates, for the connectors' "
            "producers. Errors and retries rising together while the send rate "
            "holds is the target rejecting or throttling writes.",
            targets(('sum(kafka_producer_record_send_rate{%s,client_id=~"connector-producer-.*"})'
                     % NS, "sent"),
                    ('sum(kafka_producer_record_error_rate{%s,client_id=~"connector-producer-.*"})'
                     % NS, "errors"),
                    ('sum(kafka_producer_record_retry_rate{%s,client_id=~"connector-producer-.*"})'
                     % NS, "retries")),
            w=6, unit="reqps"),
        timeseries(
            "Produce latency and buffer",
            "Request latency to the target, and how much producer buffer is "
            "left. Buffer heading for zero is back-pressure: the tasks will "
            "block, 'Records in flight' will climb, and replication lag will "
            "follow.",
            targets(('max(kafka_producer_request_latency_avg{%s,client_id=~"connector-producer-.*"})'
                     % NS, "request latency avg (ms)"),
                    ('max(kafka_producer_request_latency_max{%s,client_id=~"connector-producer-.*"})'
                     % NS, "request latency max (ms)"),
                    ('min(kafka_producer_buffer_available_bytes{%s,client_id=~"connector-producer-.*"})'
                     " / 1048576" % NS, "buffer available (MiB)")),
            w=6, unit="short"),
    ], collapsed=True)


def _per_source(identity: bool) -> Row:
    """One collapsed row per mirror, drawn by Grafana's own row repeat.

    HOW A SOURCE IS SELECTED, three different ways, because the beans differ:
      - worker, task and error metrics carry the CONNECTOR name, which starts
        with `<alias>->`;
      - the checkpoint bean carries `source` unconditionally;
      - the source bean carries only the REPLICATED topic name, which starts
        with the alias under a prefixing policy and says nothing at all under
        an identity one — hence the fallback in the lag panel.
    """
    if identity:
        lag_expr = ("max by (topic) ("
                    "kafka_connect_mirror_source_connector_replication_latency_ms_max"
                    "{%s})" % NS)
        lag_description = (
            "Under an identity policy the replicated topic keeps its source "
            "name, so the mirror bean carries no per-source dimension and this "
            "panel shows every topic in the release. Set "
            "add.source.alias.to.metrics on the connector to get a real source "
            "label back.")
    else:
        lag_expr = ("max by (topic) ("
                    "kafka_connect_mirror_source_connector_replication_latency_ms_max"
                    '{%s,topic=~"$source[%s].*"})' % (NS, SEP))
        lag_description = (
            "Selected by the replicated topic prefix: under a prefixing policy "
            "the alias IS the first segment of the target topic name.")

    return Row("Source: $source", [
        timeseries(
            "$source — records /s",
            "Throughput for this leg only, both its connectors.",
            targets(("sum by (connector) (rate("
                     "kafka_connect_source_task_metrics_source_record_write_total"
                     '{%s,connector=~"$source->.*"}[5m]))' % NS, "{{connector}}")),
            w=6, unit="reqps"),
        timeseries(
            "$source — replication lag", lag_description,
            targets((lag_expr, "{{topic}}")),
            w=6, unit="ms", threshold_style="line",
            thresholds=[("green", None), ("red", LAG_MS)]),
        timeseries(
            "$source — offset translation age",
            "Checkpoint latency per consumer group for this source. The "
            "checkpoint bean is tagged with the alias unconditionally, so this "
            "one needs no prefix guess.",
            targets(("max by (group) ("
                     "kafka_connect_mirror_checkpoint_connector_checkpoint_latency_ms_max"
                     '{%s,source="$source"})' % NS, "{{group}}")),
            w=6, unit="ms"),
        timeseries(
            "$source — errors /s",
            "Errors logged by this leg's tasks. On a fan-in release this is the "
            "panel that says whether an error rate belongs to the source you "
            "are looking at or to one of its neighbours.",
            targets(("sum by (connector) (rate("
                     "kafka_connect_task_error_metrics_total_errors_logged"
                     '{%s,connector=~"$source->.*"}[5m]))' % NS, "{{connector}}")),
            w=6, unit="reqps"),
    ], collapsed=True, repeat="source")


def build(variant: str = "default") -> list[Row]:
    slo = "no-slo" not in variant
    identity = "identity" in variant

    rows = [_health(), _task_states(), _replication(), _translation(), _errors()]
    if slo:
        rows.append(_slo())
    rows += [_workers(), _clients(), _per_source(identity)]
    return rows
