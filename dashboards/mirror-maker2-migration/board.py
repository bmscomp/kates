"""The other board: the hours a migration takes, not the months a mirror runs.

`mirror-maker2` is for a mirror that RUNS — a DR leg, a fan-in, something you
watch for months. This one asks a different question on every panel: not "is
the mirror healthy" but "can I cut over yet, and if not, what is left".

A cutover is the moment the migration stops being reversible in any cheap
sense. The runbook's checklist has three conditions before applying
values-cutover.yaml:

  1. replication lag is zero for every mirrored topic,
  2. producers have stopped and the source's end offsets are static,
  3. the mirror has had a full refresh interval to drain what was in flight.

This board can see 1 and 3. It CANNOT see 2 — nothing the MirrorMaker workers
export says whether anything is still producing to the source; that needs the
source broker's own metrics, and on a migration the source is usually the old
cluster nobody instrumented. So the go/no-go panel says DRAINED for the
conditions it can check, its description names the one it cannot, and it reads
No data rather than DRAINED when the workers are not reporting at all. A board
that implied otherwise would be worse than no board —
scripts/metric-contract/tests/mirror-maker2.cutover-gate-test.yaml holds that
panel's query to all five cases, and ci-mirror-maker2.yml extracts it from the
RENDERED board by its exact title, `Safe to cut over?`. Renaming that panel
turns the test into one that silently checks nothing.

WHAT `stopped` LOOKS LIKE HERE. The cutover primitive is
`cutover.sourceConnectorState: stopped`, and `stopped` (unlike `paused`)
releases the connector's tasks — which is the whole point, since a worker
restart cannot then quietly resume replication behind producers that have
already moved. In metrics that reads as the source connector's running task
count going to zero while the checkpoint connector's stays where it was, which
is exactly what the Cutover section plots.
"""

from __future__ import annotations

from layout import Row
from panels import constant_var, stat, table, target, targets, timeseries

# ── Frozen at generation time ──────────────────────────────────────────────
# dashboard.migration.drainedBelowMs and .translationFreshMs. DRAINED_MS is
# not only a threshold colour: it is a number inside the go/no-go panel's
# `< bool` comparison, which is why it cannot be a Grafana variable — a `$`
# there would not parse as PromQL, and the promtool gate would say so.
DRAINED_MS = 5000
FRESH_MS = 60000

NS = 'namespace="$namespace"'


def variables(variant: str) -> list[dict]:
    return [constant_var("namespace", "kafka", label="Namespace")]


def _go_no_go() -> Row:
    ready = (
        '(sum(kafka_connect_worker_metrics_task_count{%(ns)s}) >= bool 0) * '
        '((max(kafka_connect_mirror_source_connector_replication_latency_ms_max'
        '{%(ns)s} >= 0) or vector(0)) < bool %(drained)d) * '
        '((sum(kafka_connect_source_task_metrics_source_record_active_count'
        '{%(ns)s}) or vector(0)) == bool 0) * '
        '((sum(kafka_connect_worker_metrics_connector_failed_task_count'
        '{%(ns)s}) or vector(0)) == bool 0)'
    ) % {"ns": NS, "drained": DRAINED_MS}

    return Row("Can I cut over yet?", [
        stat(
            "Safe to cut over?",
            "DRAINED when all three of: replication lag below %dms, nothing in "
            "flight, and no failed tasks. The first factor of the query is the "
            "workers' own task count with no `or vector(0)` fallback, so a "
            "release that is reporting NOTHING reads as No data rather than as "
            "drained — every other factor defaults to the healthy value when "
            "its series is absent, and without that guard a dead scrape would "
            "show green. It also does NOT know whether producers have stopped "
            "writing to the source: no MirrorMaker metric says that, and "
            "confirming the source's end offsets are static (twice, 60 seconds "
            "apart) is still step 1 of the runbook's checklist. DRAINED means "
            "the mirror has caught up with what it was given, not that the "
            "source is quiet." % DRAINED_MS,
            targets((ready, "ready")),
            w=8, h=5, text_mode="value", graph_mode="none", color_mode="background",
            mappings=[{"type": "value", "options": {
                "0": {"text": "NOT YET", "color": "red", "index": 0},
                "1": {"text": "DRAINED", "color": "green", "index": 1}}}]),
        stat(
            "Replication lag",
            "The newest record's end-to-end latency, across every mirrored "
            "topic. Green below %dms, the value the go/no-go panel uses. Zero "
            "is what the checklist asks for; this is the number that says how "
            "close you are." % DRAINED_MS,
            targets(("max(kafka_connect_mirror_source_connector_replication_latency_ms_max"
                     "{%s} >= 0) or vector(0)" % NS, "lag")),
            w=4, h=5, unit="ms", thresholds=[("green", None), ("red", DRAINED_MS)]),
        stat(
            "Records in flight",
            "Polled from the source and not yet acknowledged by the target. "
            "This is the 'one full refresh interval to drain what was in "
            "flight' of the checklist, as a number rather than a wait.",
            targets(("sum(kafka_connect_source_task_metrics_source_record_active_count"
                     "{%s}) or vector(0)" % NS, "in flight")),
            w=4, h=5, unit="short", thresholds=[("green", None), ("yellow", 1)]),
        stat(
            "Catch-up ETA",
            "How long until the fetchers reach the end of the source's log, at "
            "the rate the record age has been falling over the last ten "
            "minutes. Negative means it is getting further behind, not closer — "
            "the mirror is losing to the producers, and no amount of waiting "
            "will drain it.",
            targets(("(max(kafka_connect_mirror_source_connector_record_age_ms_max{%s} >= 0) "
                     "/ -deriv(max(kafka_connect_mirror_source_connector_record_age_ms_max"
                     "{%s} >= 0)[10m:])) / 1000" % (NS, NS), "eta")),
            w=4, h=5, unit="s", decimals=0),
        stat(
            "Failed tasks",
            "Any failed task means part of the mirror is not replicating, and "
            "the partitions that task owned are behind by an unknown amount. A "
            "cutover on top of this loses those records.",
            targets(("sum(kafka_connect_worker_metrics_connector_failed_task_count{%s}) "
                     "or vector(0)" % NS, "failed")),
            w=4, h=5, thresholds=[("green", None), ("red", 1)]),
    ])


def _draining() -> Row:
    return Row("Draining", [
        timeseries(
            "Replication lag, per topic",
            "The drain, topic by topic. The line is %dms. A topic that flattens "
            "above the line while the others fall is the one holding the "
            "cutover up — usually a partition whose task died, or a topic still "
            "being produced to." % DRAINED_MS,
            targets(("max by (topic) ("
                     "kafka_connect_mirror_source_connector_replication_latency_ms_max"
                     "{%s} >= 0)" % NS, "{{topic}}")),
            w=12, unit="ms", threshold_style="line",
            thresholds=[("green", None), ("red", DRAINED_MS)]),
        timeseries(
            "Record age at the source",
            "How far behind the source's log the fetchers are reading. This is "
            "the series the catch-up ETA extrapolates: a straight line down is "
            "a healthy drain, a flat line at the top is a mirror that is "
            "keeping pace with production rather than catching up.",
            targets(("max by (topic) (kafka_connect_mirror_source_connector_record_age_ms_max"
                     "{%s} >= 0)" % NS, "{{topic}}")),
            w=12, unit="ms"),
        timeseries(
            "Records crossing /s",
            "The drain rate, per connector. Once producers have stopped this "
            "should fall to zero and stay there — and staying at zero for a "
            "full refresh interval, with lag at zero, is what 'drained' means. "
            "It falling to zero while lag is still high means something "
            "stopped, not that it finished.",
            targets(("sum by (connector) (rate("
                     "kafka_connect_source_task_metrics_source_record_write_total"
                     "{%s}[1m]))" % NS, "{{connector}}")),
            w=12, unit="reqps"),
        timeseries(
            "Records in flight",
            "The mirror's own backlog over the window. It has to reach zero and "
            "stay there before a cutover: records in flight at the moment the "
            "source connector stops are records that were read from the source "
            "and never written to the target.",
            targets(("sum by (connector) ("
                     "kafka_connect_source_task_metrics_source_record_active_count"
                     "{%s})" % NS, "{{connector}}")),
            w=12, unit="short"),
    ])


def _groups() -> Row:
    return Row("Consumer groups — will they resume in the right place?", [
        table(
            "Translated positions, by group",
            "Every consumer group MirrorMaker has translated a position for, and "
            "how old that position is. A group missing from this table has no "
            "position on the target: move it and it starts from its "
            "auto.offset.reset, which on a migration means replaying the topic "
            "or skipping everything already replicated. Amber past %dms."
            % FRESH_MS,
            [target("max by (source, group) ("
                    "kafka_connect_mirror_checkpoint_connector_checkpoint_latency_ms_max"
                    "{%s})" % NS, instant=True, fmt="table")],
            w=12, h=8,
            transformations=[
                {"id": "organize", "options": {
                    "excludeByName": {"Time": True},
                    "renameByName": {"source": "Source", "group": "Consumer group",
                                     "Value": "Translation age"}}},
            ],
            overrides=[
                {"matcher": {"id": "byName", "options": "Translation age"},
                 "properties": [
                     {"id": "unit", "value": "ms"},
                     {"id": "thresholds", "value": {"mode": "absolute", "steps": [
                         {"color": "green", "value": None},
                         {"color": "yellow", "value": FRESH_MS}]}},
                     {"id": "custom.cellOptions", "value": {"type": "color-background"}},
                 ]},
            ]),
        timeseries(
            "Translation age over the window",
            "The same numbers as a trend. Flat lines while records are still "
            "crossing mean checkpoints are being written and not moving — a "
            "failover or a cutover then resumes those consumers where they were "
            "when the line went flat.",
            targets(("max by (group) ("
                     "kafka_connect_mirror_checkpoint_connector_checkpoint_latency_ms_max"
                     "{%s})" % NS, "{{group}}")),
            w=6, unit="ms"),
        timeseries(
            "Checkpoints emitted /s",
            "The checkpoint connector's write rate. It keeps running through a "
            "cutover on purpose — consumers move one at a time and every one "
            "that has not moved still needs its position translated. Zero here "
            "during a cutover is the classic mistake: all the data on the "
            "target, and no consumer knowing where to start.",
            targets(("sum(rate(kafka_connect_source_task_metrics_source_record_write_total"
                     '{%s,connector=~".*MirrorCheckpointConnector"}[1m])) or vector(0)' % NS,
                     "checkpoints/s")),
            w=6, unit="reqps"),
    ])


def _topics(identity: bool) -> Row:
    title = "Topics — what is crossing"
    if identity:
        title += " (identity policy: target names match the source)"
        naming = "under this identity policy they are the source's own names"
    else:
        naming = "under a prefixing policy they start with the source alias"

    return Row(title, [
        table(
            "Per replicated topic",
            "Lag and record age side by side for every topic the mirror writes. "
            "The table is the cutover's punch list: a topic is done when both "
            "columns are at zero and stay there. Topic names here are the "
            "REPLICATED names — %s." % naming,
            [target("max by (topic) ("
                    "kafka_connect_mirror_source_connector_replication_latency_ms_max"
                    "{%s} >= 0)" % NS, instant=True, fmt="table", ref="A"),
             target("max by (topic) ("
                    "kafka_connect_mirror_source_connector_record_age_ms_max"
                    "{%s} >= 0)" % NS, instant=True, fmt="table", ref="B"),
             target("sum by (topic) (kafka_connect_mirror_source_connector_record_rate"
                    "{%s})" % NS, instant=True, fmt="table", ref="C")],
            w=12, h=8,
            transformations=[
                {"id": "merge", "options": {}},
                {"id": "organize", "options": {
                    "excludeByName": {"Time": True, "Time 1": True, "Time 2": True,
                                      "Time 3": True},
                    "renameByName": {"topic": "Topic", "Value #A": "Lag",
                                     "Value #B": "Record age", "Value #C": "Records /s"}}},
            ],
            overrides=[
                {"matcher": {"id": "byName", "options": "Lag"},
                 "properties": [
                     {"id": "unit", "value": "ms"},
                     {"id": "thresholds", "value": {"mode": "absolute", "steps": [
                         {"color": "green", "value": None},
                         {"color": "red", "value": DRAINED_MS}]}},
                     {"id": "custom.cellOptions", "value": {"type": "color-background"}},
                 ]},
                {"matcher": {"id": "byName", "options": "Record age"},
                 "properties": [{"id": "unit", "value": "ms"}]},
            ]),
        timeseries(
            "Bytes crossing /s, per topic",
            "byte-rate is already a rate, so it is read as is. Useful for sizing "
            "the window rather than for the go/no-go: a topic moving megabytes a "
            "second will take longer to drain than the lag number alone suggests.",
            targets(("sum by (topic) (kafka_connect_mirror_source_connector_byte_rate"
                     "{%s})" % NS, "{{topic}}")),
            w=12, unit="Bps"),
    ])


def _cutover() -> Row:
    running = {"type": "range", "options": {"from": 0.5, "to": 100000, "result": {
        "text": "RUNNING", "color": "green", "index": 1}}}
    return Row("Cutover state", [
        stat(
            "Source connector",
            "RUNNING until the cutover overlay is applied, STOPPED after. "
            "`stopped` releases the connector's tasks — which is why this reads "
            "zero rather than merely idle — so a worker restart cannot resume "
            "replication behind producers that have already moved to the target.",
            targets(("sum(kafka_connect_worker_metrics_connector_running_task_count"
                     '{%s,connector=~".*MirrorSourceConnector"}) or vector(0)' % NS,
                     "source")),
            w=6, h=5, text_mode="value", graph_mode="none", color_mode="background",
            mappings=[{"type": "value", "options": {
                "0": {"text": "STOPPED", "color": "blue", "index": 0}}}, running]),
        stat(
            "Checkpoint connector",
            "Must stay RUNNING through the cutover and until the last consumer "
            "has moved. Stopping both connectors together is the classic cutover "
            "mistake: every record is on the target and no consumer knows where "
            "to start reading it.",
            targets(("sum(kafka_connect_worker_metrics_connector_running_task_count"
                     '{%s,connector=~".*MirrorCheckpointConnector"}) or vector(0)' % NS,
                     "checkpoint")),
            w=6, h=5, text_mode="value", graph_mode="none", color_mode="background",
            mappings=[{"type": "value", "options": {
                "0": {"text": "STOPPED", "color": "red", "index": 0}}}, running]),
        stat(
            "Still replicating /s",
            "Records the SOURCE connector is writing, now. After the cutover "
            "this must be zero and stay zero — a non-zero reading here means the "
            "freeze did not take, and records are still arriving behind whatever "
            "your new producers are appending to the target.",
            targets(("sum(rate(kafka_connect_source_task_metrics_source_record_write_total"
                     '{%s,connector=~".*MirrorSourceConnector"}[1m])) or vector(0)' % NS,
                     "records/s")),
            w=6, h=5, unit="reqps"),
        stat(
            "Errors /s",
            "Errors logged by any task in the window. A migration that starts "
            "erroring mid-drain is one where the lag number stops meaning what "
            "you think it means — records may be being skipped rather than "
            "replicated.",
            targets(("sum(rate(kafka_connect_task_error_metrics_total_errors_logged"
                     "{%s}[5m])) or vector(0)" % NS, "errors/s")),
            w=6, h=5, unit="reqps", thresholds=[("green", None), ("red", 0.001)]),
        timeseries(
            "The freeze, per connector",
            "The picture a cutover should make: the source connector's line "
            "drops to zero and stays there, the checkpoint connector's keeps "
            "going. Both at zero means offset translation stopped too, and every "
            "consumer that has not moved yet is stranded.",
            targets(("sum by (connector) (rate("
                     "kafka_connect_source_task_metrics_source_record_write_total"
                     "{%s}[1m]))" % NS, "{{connector}}")),
            w=24, unit="reqps"),
    ])


def build(variant: str = "default") -> list[Row]:
    identity = "identity" in variant
    return [_go_no_go(), _draining(), _groups(), _topics(identity), _cutover()]
