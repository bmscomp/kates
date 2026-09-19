"""Kafka Connect — one release's workers, connectors and tasks.

Read top to bottom, in the order an incident asks its questions:

  (header)              up, connectors, failures, task availability, errors
  Connectors and tasks  which connector, which task, which worker
  Throughput            is data moving, and how far behind the sinks are
  Errors and DLQ        what is failing, retried, skipped, dead-lettered
  Offset commits        will a restart reprocess
  Workers               rebalances, startups, heap
  Client path           the producers and consumers under the tasks (collapsed)

Ported from charts/connect-cluster/templates/dashboard.yaml, which built the
same 31 panels out of Helm dicts and `kafka-common.grafana.layout`. Two things
changed in the move. Every panel now carries a description — the board had
none, which is the one thing `scripts/check-dashboards.py` will not allow —
and the release selector became template variables.

That second change is the interesting one. The template wrote
`namespace="<ns>", pod=~"<fullname>-connect-[0-9]+"` into all 31 expressions,
because the release name was the only thing in scope that identified the
workers. It is not any more: this chart's PodMonitor applies
`kafka-common.strimziRelabelings`, so `strimzi_io_cluster` — Strimzi's own
`strimzi.io/cluster` pod label, which for a KafkaConnect is the CR name and so
the release's fullname — is on every series. `$cluster` selects exactly the
pods the regex used to, and upstream's own `strimzi-kafka-connect.json`
selects the same way.

`uid` and `title` still come from the release: the chart template loads this
file and overwrites both, because Grafana keys a board by uid and two releases
whose names share a 16-character prefix would otherwise overwrite each other's
board. See charts/connect-cluster/templates/dashboard.yaml.
"""

from __future__ import annotations

import panels as P
from layout import Row


# The release selector, and the whole of what the per-release pod regex became.
# `namespace` scopes to the release's namespace; `strimzi_io_cluster` to its
# KafkaConnect. Both are relabelings the PodMonitor applies, so both are on
# every series these workers publish, including the synthetic `up`.
SEL = 'namespace="$namespace", strimzi_io_cluster="$cluster"'

# A series every worker publishes from the moment it joins the group, used to
# populate the two variables. The rebalance bean exists before any connector
# does, which a connector- or task-scoped series would not.
ANCHOR = "kafka_connect_worker_rebalance_metrics_completed_rebalances_total"

GREEN = [("green", None)]
GREEN_RED_1 = [("green", None), ("red", 1)]


def variables(variant: str) -> list[dict]:
    """`$namespace` and `$cluster`: together, the old pod-name regex."""
    return [
        P.query_var(
            "namespace",
            "label_values(%s, namespace)" % ANCHOR,
            label="Namespace",
        ),
        P.query_var(
            "cluster",
            'label_values(%s{namespace="$namespace"}, strimzi_io_cluster)' % ANCHOR,
            label="Connect cluster",
        ),
    ]


# ── The header ─────────────────────────────────────────────────────────────

def _header() -> list[dict]:
    return [
        P.stat(
            "Workers up",
            "How many worker pods answered Prometheus' last scrape. `up` is "
            "synthetic — Prometheus writes one per target — so this counts "
            "scrape targets, not Ready pods: a worker that is running but "
            "whose metrics port stopped answering reads as down here while "
            "the KafkaConnect still reports Ready. Below the release's "
            "replica count for 3 minutes is KafkaConnectWorkerDown. The "
            "`or vector(0)` is what makes an empty result draw a zero rather "
            "than *No data*, so a release with no workers left is visibly "
            "zero instead of visibly blank.",
            P.targets("sum(up{%s}) or vector(0)" % SEL),
            unit="short", thresholds=GREEN,
        ),
        P.stat(
            "Connectors",
            "Connectors registered with the group, counted by name. "
            "`kafka_connect_connector_metrics` is a synthetic `1` the exporter "
            "emits once per connector *per attribute* — class, type, version "
            "and status are four series sharing that one name and differing "
            "only in which label they carry — so the `max by (connector)` is "
            "not an aggregation over workers, it is what collapses those four "
            "back into one connector. Every worker reports every connector, "
            "so this is a group-wide count.",
            P.targets(
                "count(max by (connector) (kafka_connect_connector_metrics{%s})) "
                "or vector(0)" % SEL),
            unit="short", thresholds=GREEN,
        ),
        P.stat(
            "Failed connectors",
            "Connectors whose own status is `failed` — connector scope, which "
            "means the connector's `start()` or its configuration, not its "
            "data path. KafkaConnectConnectorFailed reads this. It is not the "
            "common failure: a connector normally stays RUNNING while its "
            "tasks die, which is the stat to the right. `status` is a label, "
            "so a connector that recovers stops publishing the `failed` "
            "series rather than publishing it as zero — the series goes stale "
            "and the count falls on its own.",
            P.targets(
                'count(max by (connector) (kafka_connect_connector_metrics'
                '{status="failed", %s})) or vector(0)' % SEL),
            unit="short", thresholds=GREEN_RED_1,
        ),
        P.stat(
            "Failed tasks",
            "Tasks in the `failed` state across every connector. Task scope, "
            "not connector scope, and that is the whole point: a connector "
            "with four tasks and one failure reports RUNNING on its CR and on "
            "the stat to the left, keeps replicating three quarters of its "
            "partitions, and appears only here. Each series is a synthetic `1` "
            "per (connector, task, status), so the sum is a count of tasks. "
            "KafkaConnectTaskFailed fires on it after 2 minutes.",
            P.targets(
                'sum(kafka_connect_connector_task_status{status="failed", %s}) '
                'or vector(0)' % SEL),
            unit="short", thresholds=GREEN_RED_1,
        ),
        P.stat(
            "Tasks running",
            "Running tasks over declared tasks, group-wide — the SLI behind "
            "the `connect:tasks_running:ratio` recording rule and the "
            "task-availability SLO. Both counts are *per worker*: "
            "`connector-running-task-count` is what one worker runs, so the "
            "sums are over workers and the ratio only means anything while "
            "every worker is scraping. That is also its blind spot. A worker "
            "that disappears takes its tasks out of the numerator and the "
            "denominator at the same instant, so this can read 1.0 for the "
            "length of a rebalance while tasks are in fact unassigned. Read "
            "it beside 'Workers up'.",
            P.targets(
                "sum(kafka_connect_worker_metrics_connector_running_task_count{%s}) "
                "/ sum(kafka_connect_worker_metrics_connector_total_task_count{%s})"
                % (SEL, SEL)),
            unit="percentunit", thresholds=GREEN,
        ),
        P.stat(
            "Record errors /s",
            "Errors the tasks logged, per second over five minutes. "
            "`total-errors-logged` is cumulative since the task started, but "
            "the exporter's COUNTER rules key off a `-total` *suffix* and this "
            "attribute wears its `total` at the front, so the series is "
            "published typed GAUGE while only ever going up. `rate()` is still "
            "the right function: Prometheus' type metadata does not change "
            "what `rate()` computes, and its reset handling is exactly what a "
            "task restart needs. Above `alerts.thresholds.errorRatePerSecond` "
            "is KafkaConnectErrorsLogged.",
            P.targets(
                "sum(rate(kafka_connect_task_error_metrics_total_errors_logged"
                "{%s}[5m])) or vector(0)" % SEL),
            unit="short", thresholds=GREEN,
        ),
    ]


# ── Connectors and tasks ───────────────────────────────────────────────────

_TABLE_TRANSFORMS = [{"id": "organize", "options": {"excludeByName": {"Time": True}}}]


def _tasks() -> list[dict]:
    return [
        P.table(
            "Connector status",
            "One row per connector with the state the workers report: "
            "running, paused, stopped, failed, unassigned, restarting or "
            "destroyed. `status!=\"\"` is load-bearing. The exporter publishes "
            "class, type, version and status as four series of the same name, "
            "each carrying a different label, and an absent label matches "
            "`\"\"` in PromQL — without the filter this table would show four "
            "rows per connector, three of them with an empty status. `max` "
            "rather than `sum` because every worker in the group reports the "
            "same connector, and summing would count the workers.",
            [P.target(
                'max by (connector, status) (kafka_connect_connector_metrics'
                '{status!="", %s})' % SEL, instant=True, fmt="table")],
            w=8, transformations=_TABLE_TRANSFORMS,
        ),
        P.table(
            "Task status",
            "One row per task, and the only place a PARTIALLY failed connector "
            "is visible: the CR says Ready, the connector says RUNNING, and "
            "one of its four tasks has been dead since the last rebalance — "
            "the partitions that task owned simply stopped, silently. A task "
            "runs on exactly one worker, so each row sums a single series and "
            "reads 1; it is a `sum` rather than a `max` so that a task counted "
            "twice mid-reassignment shows as 2 instead of being hidden.",
            [P.target(
                "sum by (connector, task, status) "
                "(kafka_connect_connector_task_status{%s})" % SEL,
                instant=True, fmt="table")],
            w=8, transformations=_TABLE_TRANSFORMS,
        ),
        P.timeseries(
            "Tasks per worker",
            "What the group coordinator assigned to each worker, from that "
            "worker's own view of the last rebalance. This is assignment, not "
            "health: it says where tasks were *sent*, while the task-status "
            "table says what they are *doing*. A worker sitting at zero after "
            "a rebalance while the others carry double is an assignment that "
            "did not spread — usually a connector whose `tasks.max` is below "
            "the worker count, or a worker that joined late. The bean is "
            "labelled by client id; `by (pod)` uses the scrape's own pod "
            "label, which is one per worker.",
            P.targets((
                "sum by (pod) (kafka_connect_coordinator_metrics_assigned_tasks"
                "{%s})" % SEL, "{{pod}}")),
            unit="short", w=8,
        ),
        P.timeseries(
            "Task running and paused ratio",
            "The fraction of Kafka's own sampling window each task spent "
            "running, and the fraction it spent paused. This is what "
            "separates 'running' from 'running *now*': the task-status table "
            "is a snapshot taken at scrape time, so a task that dies and "
            "restarts every twenty seconds reads RUNNING on most scrapes "
            "while its running ratio sits near 0.4. Paused is deliberate — a "
            "connector someone paused reads 0 running and 1 paused, and that "
            "is not an incident. Both are already normalised to 0–1 over "
            "Kafka's window; neither is a counter, so neither takes `rate()`.",
            P.targets((
                'label_replace(max by (connector, task) '
                '(kafka_connect_connector_task_metrics_running_ratio{%s}), '
                '"kind", "running", "", "") or label_replace(max by (connector, task) '
                '(kafka_connect_connector_task_metrics_pause_ratio{%s}), '
                '"kind", "paused", "", "")' % (SEL, SEL),
                "{{connector}}/{{task}} {{kind}}")),
            unit="percentunit", w=12, min_value=0, max_value=1,
        ),
        P.timeseries(
            "Sink partitions assigned",
            "Topic partitions the sink tasks of each connector currently "
            "hold, summed over its tasks. It should equal the partition count "
            "of the topics the connector subscribes to. Anything less means a "
            "task is holding no assignment, which is how a sink quietly stops "
            "consuming part of its input while every task reports RUNNING and "
            "the throughput panels merely look lower than yesterday. It drops "
            "to zero for the length of every consumer-group rebalance, so "
            "read a dip against 'Rebalances /s' before treating it as a "
            "fault. Source connectors publish no such series and are simply "
            "absent here.",
            P.targets((
                "sum by (connector) (kafka_connect_sink_task_metrics_partition_count"
                "{%s})" % SEL, "{{connector}}")),
            unit="short", w=12,
        ),
    ]


# ── Throughput ─────────────────────────────────────────────────────────────

def _throughput() -> list[dict]:
    return [
        P.timeseries(
            "Source records polled /s",
            "Records the source tasks pulled out of the upstream system, per "
            "second, per connector. `source-record-poll-total` does end in "
            "`-total`, so the exporter types it a true COUNTER and `rate()` is "
            "unambiguously right. Compare it with the panel to its right: "
            "poll is what the connector read, write is what reached Kafka, "
            "and a standing gap between them is what the transforms removed. "
            "Flat at zero on a RUNNING source for "
            "`alerts.thresholds.sourceIdleMinutes` is KafkaConnectSourceIdle, "
            "which is opt-in because a source that polls nothing can be "
            "entirely normal.",
            P.targets((
                "sum by (connector) (rate("
                "kafka_connect_source_task_metrics_source_record_poll_total"
                "{%s}[5m]))" % SEL, "{{connector}}")),
            unit="short", w=8,
        ),
        P.timeseries(
            "Source records written /s",
            "Records the source tasks handed to the producer for Kafka, per "
            "second, per connector. Persistently below the poll rate is "
            "records being filtered or transformed away — not a backlog, "
            "which shows up as 'Source records in flight' climbing instead. "
            "A true counter, summed over every task of the connector on every "
            "worker.",
            P.targets((
                "sum by (connector) (rate("
                "kafka_connect_source_task_metrics_source_record_write_total"
                "{%s}[5m]))" % SEL, "{{connector}}")),
            unit="short", w=8,
        ),
        P.timeseries(
            "Source records in flight",
            "Records polled from the source and not yet acknowledged by "
            "Kafka, per connector — the source connector's own backlog. It "
            "grows when the produce path is the bottleneck rather than the "
            "upstream system, and the collapsed Client path section at the "
            "bottom of this board reads that path directly: producer request "
            "latency, errors and retries. A gauge, summed over tasks.",
            P.targets((
                "sum by (connector) "
                "(kafka_connect_source_task_metrics_source_record_active_count"
                "{%s})" % SEL, "{{connector}}")),
            unit="short", w=8,
        ),
        P.timeseries(
            "Sink records read /s",
            "Records the sink tasks consumed from Kafka, per second, per "
            "connector — the *read* side of a sink, what came out of the "
            "topic before the connector tried to write it anywhere. On its "
            "own it says the consumer is working; it says nothing about the "
            "destination, which is the panel to its right.",
            P.targets((
                "sum by (connector) (rate("
                "kafka_connect_sink_task_metrics_sink_record_read_total"
                "{%s}[5m]))" % SEL, "{{connector}}")),
            unit="short", w=8,
        ),
        P.timeseries(
            "Sink records put /s",
            "Records the sink tasks passed to `SinkTask.put()`, per second, "
            "per connector — what the connector attempted to write to the "
            "destination, after transforms. Lower than the read rate means "
            "records are being dropped by an SMT or a predicate. Equal to the "
            "read rate while lag grows means the destination is slow and the "
            "connector is keeping up with it, which is a capacity problem "
            "rather than a Connect problem.",
            P.targets((
                "sum by (connector) (rate("
                "kafka_connect_sink_task_metrics_sink_record_send_total"
                "{%s}[5m]))" % SEL, "{{connector}}")),
            unit="short", w=8,
        ),
        P.timeseries(
            "Sink lag (max records behind)",
            "The largest gap, over the partitions a sink connector holds, "
            "between the offsets its consumer has reached and the offsets the "
            "task has committed. Read the name carefully, because this is lag "
            "*inside the task* — records it has taken delivery of and not yet "
            "acknowledged — and not the number of records still waiting "
            "unread in the topic. The backlog an operator usually means by "
            "'sink lag' is the consumer group's, `kafka_consumergroup_lag`, "
            "which the Kafka Exporter on the Kafka cluster publishes and "
            "which KafkaConnectSinkLag alerts on at "
            "`alerts.thresholds.sinkLagRecords`; it is not on this board "
            "because the workers do not produce it. The closest thing here is "
            "'Consumer lag (max)' in the Client path section, which the sink's "
            "own consumer measures against the end of the log. Use this panel "
            "to catch a task that has stopped committing, and those two for "
            "how far behind the destination really is.",
            P.targets((
                "max by (connector) "
                "(kafka_connect_sink_task_metrics_sink_record_lag_max{%s})" % SEL,
                "{{connector}}")),
            unit="short", w=8,
        ),
    ]


# ── Errors and dead letter queue ───────────────────────────────────────────

def _errors() -> list[dict]:
    return [
        P.timeseries(
            "Errors logged /s",
            "Per connector, the same series the header stat totals and "
            "KafkaConnectErrorsLogged reads. A connector configured with "
            "`errors.tolerance=all` logs every failure here and goes on "
            "reporting RUNNING for as long as you let it, so this panel and "
            "the two beside it are the only places that shows. Cumulative but "
            "published typed GAUGE, for the reason the header stat gives; "
            "`rate()` over it is correct.",
            P.targets((
                "sum by (connector) (rate("
                "kafka_connect_task_error_metrics_total_errors_logged{%s}[5m]))" % SEL,
                "{{connector}}")),
            unit="short", w=8,
        ),
        P.timeseries(
            "Record failures and skips /s",
            "What the errors did to the records. A *failure* is a record the "
            "task could not convert or deliver; a *skip* is a failure the task "
            "was configured to tolerate, so the record is gone and the "
            "connector is still green. Skips are the one error mode neither a "
            "lag panel nor a status table can show — the pipeline is healthy, "
            "the throughput is normal, and the destination is quietly missing "
            "rows. The two are separate attributes of the same bean, so "
            "`label_replace` gives each a `kind` to draw them apart.",
            P.targets((
                'label_replace(sum by (connector) (rate('
                'kafka_connect_task_error_metrics_total_record_failures{%s}[5m])), '
                '"kind", "failed", "", "") or label_replace(sum by (connector) (rate('
                'kafka_connect_task_error_metrics_total_records_skipped{%s}[5m])), '
                '"kind", "skipped", "", "")' % (SEL, SEL),
                "{{connector}} {{kind}}")),
            unit="short", w=8,
        ),
        P.timeseries(
            "Dead letter queue /s",
            "Records diverted to the dead-letter topic, and the writes to "
            "that topic which themselves failed. A rising `written` line is "
            "KafkaConnectDeadLetterWrites: the connector is behaving as "
            "configured and somebody has to drain the DLQ. A rising `failed` "
            "line is KafkaConnectDeadLetterFailures and is the worse of the "
            "two — the escape hatch is broken, so those records are in "
            "neither the destination nor the dead-letter topic and there is "
            "nothing left to replay them from.",
            P.targets((
                'label_replace(sum by (connector) (rate('
                'kafka_connect_task_error_metrics_deadletterqueue_produce_requests'
                '{%s}[5m])), "kind", "written", "", "") or '
                'label_replace(sum by (connector) (rate('
                'kafka_connect_task_error_metrics_deadletterqueue_produce_failures'
                '{%s}[5m])), "kind", "failed", "", "")' % (SEL, SEL),
                "{{connector}} {{kind}}")),
            unit="short", w=8,
        ),
    ]


# ── Offset commits ─────────────────────────────────────────────────────────

def _commits() -> list[dict]:
    return [
        P.timeseries(
            "Offset commit time (avg)",
            "How long a task's offset commit takes, in milliseconds, worst "
            "task per connector. Kafka averages this over its own metric "
            "window before the exporter ever sees it, so the value is already "
            "an average and `max by (connector)` picks the slowest task rather "
            "than averaging an average. Commits that run longer than "
            "`offset.flush.interval.ms` push the task off its schedule, and "
            "the first symptom is throughput sagging with not one error "
            "anywhere on the board.",
            P.targets((
                "max by (connector) "
                "(kafka_connect_connector_task_metrics_offset_commit_avg_time_ms"
                "{%s})" % SEL, "{{connector}}")),
            unit="ms", w=8,
        ),
        P.timeseries(
            "Offset commit failures",
            "The share of a task's recent commit attempts that failed. Kafka "
            "calls the attribute a percentage and publishes a fraction "
            "between 0 and 1, which is why the panel is in `percentunit` and "
            "why KafkaConnectOffsetCommitFailures fires at `> 0` rather than "
            "at `> 1`. What it costs you is paid at restart: a task resumes "
            "from its last committed offset, so a connector that has not "
            "committed for an hour reprocesses an hour — duplicates "
            "downstream for a sink, re-read source rows for a source.",
            P.targets((
                "max by (connector) (kafka_connect_connector_task_metrics_"
                "offset_commit_failure_percentage{%s})" % SEL, "{{connector}}")),
            unit="percentunit", w=8,
        ),
        P.timeseries(
            "Sink offset commits /s",
            "Successful offset commits per second for the sink tasks. "
            "`offset-commit-completion-rate` is already a rate — Kafka "
            "computes it per second over its own window — so it is read as "
            "it is; `rate()` over it would be the rate of a rate. Sitting at "
            "zero while 'Sink records read /s' is not zero means the task is "
            "consuming and never recording where it got to, and the next "
            "restart replays all of it.",
            P.targets((
                "sum by (connector) "
                "(kafka_connect_sink_task_metrics_offset_commit_completion_rate"
                "{%s})" % SEL, "{{connector}}")),
            unit="short", w=8,
        ),
    ]


# ── Workers ────────────────────────────────────────────────────────────────

def _workers() -> list[dict]:
    return [
        P.timeseries(
            "Rebalances /s",
            "Completed group rebalances per second, per worker. No task "
            "processes data during a rebalance, so a storm reads downstream "
            "as intermittent lag with nothing failed and nothing logged. "
            "Above `alerts.thresholds.rebalanceRatePerSecond` for 5 minutes is "
            "KafkaConnectRebalanceStorm. Every worker rebalances together, so "
            "the lines move as one; a single worker moving alone is that "
            "worker joining and leaving. This attribute does end in `-total`, "
            "so it is a genuine COUNTER and `rate()` is right.",
            P.targets((
                "rate(kafka_connect_worker_rebalance_metrics_completed_rebalances_total"
                "{%s}[5m])" % SEL, "{{pod}}")),
            unit="short", w=8,
        ),
        P.timeseries(
            "Rebalance time (avg)",
            "Mean duration of the recent rebalances each worker took part in. "
            "Frequent rebalances and long rebalances are different faults: "
            "frequent is usually a worker that keeps leaving the group — a "
            "liveness probe, a GC pause, `session.timeout.ms` set too tight — "
            "while long is usually a large number of connectors and tasks to "
            "reassign. Left per worker on purpose: during a rebalance the "
            "workers disagree, and the disagreement is the information.",
            P.targets((
                "kafka_connect_worker_rebalance_metrics_rebalance_avg_time_ms{%s}" % SEL,
                "{{pod}}")),
            unit="ms", w=8,
        ),
        P.timeseries(
            "Startup failures (15m)",
            "Connectors and tasks that failed to start in the last 15 "
            "minutes, counted apart. These are worker-scope counters, so a "
            "failure here happened before the connector or task ever reached "
            "the status table: a bad plugin path, a class the worker cannot "
            "load, a configuration the connector rejected in `start()`. A "
            "worker crash-looping on startup never appears in the task panels "
            "at all, and this is where it shows. `increase()` over 15 minutes "
            "rather than a rate, because these are rare events and a "
            "per-second rate of a rare event is unreadable.",
            P.targets((
                'label_replace(sum(increase('
                'kafka_connect_worker_metrics_connector_startup_failure_total'
                '{%s}[15m])), "kind", "connector", "", "") or label_replace(sum(increase('
                'kafka_connect_worker_metrics_task_startup_failure_total'
                '{%s}[15m])), "kind", "task", "", "")' % (SEL, SEL),
                "{{kind}}")),
            unit="short", w=8,
        ),
        P.timeseries(
            "Rebalance in progress",
            "1 while the worker is rebalancing, 0 otherwise. This is the "
            "series KafkaConnectRebalanceTooLong fires on — `max(...) == 1` "
            "held for 5 minutes — and until this board had a panel for it, "
            "the only place it existed was inside that alert, so the page had "
            "nowhere to look. A line stuck at 1 is a rebalance that never "
            "converged, usually a worker that cannot complete the join, and "
            "it takes the *whole group's* tasks out of service rather than "
            "that worker's share of them.",
            P.targets((
                "kafka_connect_worker_rebalance_metrics_rebalancing{%s}" % SEL,
                "{{pod}}")),
            unit="short", w=12, min_value=0, max_value=1,
        ),
        P.timeseries(
            "Time since last rebalance",
            "How long each worker has been stable. A healthy group draws one "
            "line climbing steadily; a sawtooth is the storm above seen from "
            "the other side, where the height of each tooth is how long the "
            "group managed to stay still between rebalances. This is the "
            "panel for a connector whose throughput is fine on average and "
            "terrible in bursts. Kafka resets the attribute to zero at every "
            "rebalance, so never `rate()` it — the drops are the signal, not "
            "counter resets to be corrected away.",
            P.targets((
                "kafka_connect_worker_rebalance_metrics_time_since_last_rebalance_ms"
                "{%s}" % SEL, "{{pod}}")),
            unit="ms", w=12,
        ),
        P.timeseries(
            "Heap used",
            "Worker heap, under both spellings of the name: the JMX exporter "
            "agent renamed `jvm_memory_bytes_used` to `jvm_memory_used_bytes` "
            "in its 1.x line, and which one a given worker image ships is not "
            "something a panel should have to know, so `or` takes whichever "
            "exists. Connect workers under memory pressure rarely crash — "
            "they slow down, and it surfaces as lag, or as rebalances caused "
            "by GC pauses long enough to miss a heartbeat.",
            P.targets((
                'sum by (pod) (jvm_memory_used_bytes{area="heap", %s} '
                'or jvm_memory_bytes_used{area="heap", %s})' % (SEL, SEL),
                "{{pod}}")),
            unit="bytes", w=12,
        ),
        P.timeseries(
            "Heap used / max",
            "The same heap against the JVM's maximum, which is the form "
            "KafkaConnectWorkerHeapHigh uses "
            "(`alerts.thresholds.heapUsagePercent`). Both halves of the "
            "division carry the `or` over the two exporter spellings, so the "
            "ratio cannot end up with a 1.x numerator over an 0.x denominator "
            "and render nothing at all — which is the failure mode that "
            "writing only one spelling produces, and it looks exactly like an "
            "idle cluster.",
            P.targets((
                'sum by (pod) (jvm_memory_used_bytes{area="heap", %s} '
                'or jvm_memory_bytes_used{area="heap", %s}) '
                '/ sum by (pod) (jvm_memory_max_bytes{area="heap", %s} '
                'or jvm_memory_bytes_max{area="heap", %s})' % (SEL, SEL, SEL, SEL),
                "{{pod}}")),
            unit="percentunit", w=12,
        ),
    ]


# ── Client path ────────────────────────────────────────────────────────────

def _clients() -> list[dict]:
    return [
        P.timeseries(
            "Producer records sent /s",
            "The producers underneath the tasks — one per source connector "
            "task, plus the workers' own producers for the internal offset, "
            "config and status topics. `record-send-rate` is a Kafka rate "
            "already, read as it is rather than through `rate()`. Grouped by "
            "`clientid` and not by connector because these are client beans: "
            "Connect names the client `connector-producer-<connector>-<task>`, "
            "so the connector is inside the string but is not a label.",
            P.targets((
                "sum by (clientid) (kafka_producer_producer_record_send_rate{%s})" % SEL,
                "{{clientid}}")),
            unit="short", w=8,
        ),
        P.timeseries(
            "Producer errors and retries /s",
            "Send errors and retries for those same producers. A retry is the "
            "client absorbing a transient failure; an error is what it gave "
            "up on, and a record that errors here never reached Kafka at all. "
            "Both climbing while the send rate holds steady is Kafka "
            "rejecting or throttling the writes — a broker-side answer, not a "
            "connector one. Two Kafka-computed rates, drawn apart with "
            "`label_replace`.",
            P.targets((
                'label_replace(sum by (clientid) '
                '(kafka_producer_producer_record_error_rate{%s}), '
                '"kind", "error", "", "") or label_replace(sum by (clientid) '
                '(kafka_producer_producer_record_retry_rate{%s}), '
                '"kind", "retry", "", "")' % (SEL, SEL),
                "{{clientid}} {{kind}}")),
            unit="short", w=8,
        ),
        P.timeseries(
            "Producer request latency (avg)",
            "Mean round trip to the broker for the tasks' producers. This is "
            "the first thing to look at when 'Source records in flight' is "
            "climbing with no errors anywhere: the tasks are polling fine and "
            "the produce path is the queue. Already an average over Kafka's "
            "window; `max by (clientid)` picks the worst client rather than "
            "averaging averages together.",
            P.targets((
                "max by (clientid) (kafka_producer_producer_request_latency_avg{%s})" % SEL,
                "{{clientid}}")),
            unit="ms", w=8,
        ),
        P.timeseries(
            "Consumer records /s",
            "The consumers underneath the sink tasks, and the workers' own "
            "consumers of the internal topics. Another Kafka-computed rate, "
            "read as it is. Compare it with 'Sink records put /s': consuming "
            "quickly and putting slowly is a slow destination, and the "
            "records caught in between are what the next panel measures.",
            P.targets((
                "sum by (clientid) "
                "(kafka_consumer_consumer_fetch_manager_records_consumed_rate{%s})" % SEL,
                "{{clientid}}")),
            unit="short", w=12,
        ),
        P.timeseries(
            "Consumer lag (max)",
            "The real backlog: for each client, the largest number of records "
            "any of its partitions still holds beyond the position that "
            "consumer has reached. This — rather than 'Sink lag (max records "
            "behind)' in the Throughput section — is the panel that answers "
            "'how far behind is this sink', and it is the same quantity "
            "KafkaConnectSinkLag alerts on, measured by the client instead of "
            "by the Kafka Exporter. It also covers the workers' own consumers "
            "of the internal config and status topics, which belong to no "
            "connector and so appear in none of the sink panels: a worker "
            "lagging on the status topic is a worker running on a stale view "
            "of the group.",
            P.targets((
                "max by (clientid) "
                "(kafka_consumer_consumer_fetch_manager_records_lag_max{%s})" % SEL,
                "{{clientid}}")),
            unit="short", w=12,
        ),
    ]


def build(variant: str) -> list[Row]:
    return [
        Row("", _header()),
        Row("Connectors and tasks", _tasks()),
        Row("Throughput", _throughput()),
        Row("Errors and dead letter queue", _errors()),
        Row("Offset commits", _commits()),
        Row("Workers", _workers()),
        Row("Client path", _clients(), collapsed=True),
    ]
