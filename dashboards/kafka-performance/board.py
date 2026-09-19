"""Kafka — Performance & Load Testing.

Rebuilt from `charts/monitoring/dashboards/kafka-perf-global-dashboard.json`,
which was the only one of the nine legacy Kafka boards with anything worth
keeping: per-topic templating and per-partition log-end-offset, neither of
which the Strimzi operator's own boards have. It also absorbs the per-topic
panels of `kafka-perf-test`, `kafka-performance` and `kafka-working`, which
were the same three or four queries rendered as gauges instead of timeseries.

**Seven of that board's metric names did not exist and could not exist**, and
they were precisely its eight unique panels — all the throughput, all the ISR
dynamics, request-handler idle. Two rules of the JMX exporter explain every
one of them, and both are visible in the corrected expressions below:

  * A `…PerSec` attribute is a Yammer METER. The exporter's COUNTER rule
    strips `PerSec` and publishes the meter's `Count` with `_total` appended,
    so the series is `kafka_server_brokertopicmetrics_bytesin_total` and the
    query is `rate()` over it. `…_bytesinpersec` is a name no rule emits.
  * A `…Percent` attribute goes through the Percent rule, which inserts an
    underscore before `percent`. So it is
    `kafka_server_kafkarequesthandlerpool_requesthandleravgidle_percent`, and
    `…avgidlepercent` (no underscore) is a name no rule emits. The corrected
    series is a 0–1 GAUGE and takes no `rate()`.

`kafka-working`'s heap panel had an eighth: `java_lang_memory_heapmemoryusage_used`
is the raw MBean spelling, and the exporter agent publishes JVM memory as
`jvm_memory_used_bytes` (or `jvm_memory_bytes_used` on its 0.x line, which is
why both appear below joined by `or`).

Read top to bottom:

  (header)          the six numbers a run is judged on
  Throughput        bytes and messages, per broker and per zone
  Replication       what the load is doing to the ISRs
  Broker internals  where the broker is saturated
  JVM               heap, non-heap, GC and threads under the load
  Resource usage    CPU, memory and file descriptors
  Topic detail      the run's own topic, partition by partition

For the quorum, metadata propagation, per-error-code failures and the
security counters, the companion board is *Kafka — KRaft Operations*. For
broker health in general, the Strimzi operator's `strimzi-kafka.json` is
authoritative and this board does not restate it.
"""

from __future__ import annotations

import panels as P
from layout import Row

# The same selector every alert in charts/kafka-cluster carries. The
# `strimzi_io_name` clause matters more here than on any other board: this one
# reads `jvm_*` and `process_*`, which Cruise Control, the Kafka Exporter and
# the entity operator all publish too, in the same namespace and under the
# same `strimzi_io_cluster`.
SEL = ('namespace="$namespace", strimzi_io_cluster="$cluster", '
       'strimzi_io_name="$cluster-kafka"')

# BrokerTopicMetrics is registered TWICE: once per topic, and once with no
# topic tag at all for the broker-wide aggregate. Summing the series without
# a `topic` matcher therefore counts every byte twice. `topic=""` selects the
# aggregate (an absent label matches the empty string in PromQL) and
# `topic!=""` selects the per-topic breakdown; `kafka:bytes_in:rate5m` in the
# chart's recording rules uses the same `topic=""`.
AGG = '%s, topic=""' % SEL
BY_TOPIC = '%s, topic=~"$topic", topic!=""' % SEL

# Partition-level series carry `topic` and `partition` and have no aggregate
# form, so they only need the selector.
TOPIC_SEL = '%s, topic=~"$topic"' % SEL

POD = "kubernetes_pod_name"

# Every broker publishes it, one series per broker: enough for the variables,
# and it is what `strimzi-kafka.json` counts brokers with.
ANCHOR = "kafka_server_replicamanager_leadercount"

# charts/kafka-cluster's alerts.thresholds defaults, frozen because a Grafana
# threshold is not templatable. README.md names them.
HANDLER_IDLE_RATIO = 0.3      # alerts.thresholds.handlerIdleRatio
REQUEST_P99_MS = 1000         # alerts.thresholds.requestLatencyP99Ms
REQUEST_QUEUE_SIZE = 100      # alerts.thresholds.requestQueueSize
LOG_FLUSH_P99_MS = 500        # alerts.thresholds.logFlushP99Ms

GREEN = [("green", None)]
GREEN_RED_1 = [("green", None), ("red", 1)]

_TABLE_TRANSFORMS = [{"id": "organize", "options": {"excludeByName": {"Time": True}}}]


def variables(variant: str) -> list[dict]:
    """`$namespace`, `$cluster` and `$topic`.

    `$topic` is what made `kafka-perf-global` worth rebuilding rather than
    deleting: no other board in the repository, upstream included, lets an
    operator point the panels at the topic a load test is actually writing to.
    It is multi-select with an All option here, where the original allowed one
    topic, because a run that writes to several is the normal case and the
    panels group by topic anyway.
    """
    return [
        P.query_var("namespace", "label_values(%s, namespace)" % ANCHOR,
                    label="Namespace"),
        P.query_var("cluster",
                    'label_values(%s{namespace="$namespace"}, strimzi_io_cluster)'
                    % ANCHOR, label="Kafka cluster"),
        P.query_var(
            "topic",
            'label_values(kafka_log_log_size{namespace="$namespace", '
            'strimzi_io_cluster="$cluster"}, topic)',
            label="Topic", multi=True, include_all=True, all_value=".+"),
    ]


# ── The header ─────────────────────────────────────────────────────────────

def _header() -> list[dict]:
    return [
        P.stat(
            "Brokers",
            "Brokers publishing metrics right now, counted from a series every "
            "broker always has. During a load test this is the first thing to "
            "check when throughput drops: a broker that went away takes its "
            "share of the leadership with it, and the throughput panels show "
            "the remainder without saying why. Controller-only nodes are not "
            "counted — they lead no partitions and publish no ReplicaManager "
            "gauge.",
            P.targets("count(%s{%s}) or vector(0)" % (ANCHOR, SEL)),
            unit="short", thresholds=GREEN,
        ),
        P.stat(
            "Bytes in /s",
            "Cluster-wide ingest, averaged over five minutes. **`topic=\"\"` is "
            "load-bearing**: `BrokerTopicMetrics` is registered once per topic "
            "AND once with no topic tag for the broker aggregate, and an "
            "absent label matches `\"\"` in PromQL — so summing the series "
            "without that matcher counts every byte twice and reports double "
            "the throughput the test actually achieved. The corrected series "
            "is `…_bytesin_total`; the board this replaces read "
            "`…_bytesinpersec`, which no exporter rule has ever produced, so "
            "this panel was empty on every cluster it ever ran on.",
            P.targets("sum(rate(kafka_server_brokertopicmetrics_bytesin_total"
                      "{%s}[5m]))" % AGG),
            unit="Bps", thresholds=GREEN,
        ),
        P.stat(
            "Bytes out /s",
            "Cluster-wide egress to clients over five minutes, on the same "
            "`topic=\"\"` aggregate. Replication traffic is NOT in it — that "
            "is `ReplicationBytesOutPerSec` and has its own panel in the "
            "Replication section — so on a three-replica topic the total "
            "bytes leaving the brokers is roughly this plus twice the ingest. "
            "Worth remembering when sizing a network.",
            P.targets("sum(rate(kafka_server_brokertopicmetrics_bytesout_total"
                      "{%s}[5m]))" % AGG),
            unit="Bps", thresholds=GREEN,
        ),
        P.stat(
            "Messages in /s",
            "Records accepted per second cluster-wide. Read it against 'Bytes "
            "in /s': the ratio is the average record size on the wire, after "
            "batching and compression, and it is the number that explains why "
            "two runs at the same byte rate cost the brokers different "
            "amounts. A producer that raised its batch size moves bytes "
            "without moving messages; one that changed its payload moves "
            "messages without moving bytes.",
            P.targets("sum(rate(kafka_server_brokertopicmetrics_messagesin_total"
                      "{%s}[5m]))" % AGG),
            unit="short", thresholds=GREEN,
        ),
        P.stat(
            "Under-replicated partitions",
            "Partitions with a follower out of sync, cluster-wide. Under load "
            "this is the number that says whether the test result is "
            "trustworthy: a cluster that is shedding followers is not "
            "sustaining the throughput it appears to be sustaining, it is "
            "borrowing durability to fake it, and `acks=all` producers are "
            "about to start failing. KafkaUnderReplicatedPartitions fires "
            "above zero for 5 minutes.",
            P.targets("sum(kafka_server_replicamanager_underreplicatedpartitions"
                      "{%s}) or vector(0)" % SEL),
            unit="short", thresholds=GREEN_RED_1,
        ),
        P.stat(
            "Request handler idle",
            "The fraction of the last five minutes the request handler threads "
            "were idle, averaged over the brokers. This is the headroom "
            "number: it is what says whether a run that hit its target "
            "throughput did so with room to spare or at the ceiling. Below "
            "%.2f is KafkaRequestHandlerSaturated. The Broker internals "
            "section explains why it is a windowed rate over a `_count_total` "
            "of NANOSECONDS rather than the `_percent` gauge."
            % HANDLER_IDLE_RATIO,
            P.targets("avg(rate(kafka_server_kafkarequesthandlerpool_"
                      "requesthandleravgidlepercent_count_total{%s}[5m])) / 1e9"
                      % SEL),
            unit="percentunit", decimals=2,
            thresholds=[("red", None), ("orange", HANDLER_IDLE_RATIO),
                        ("green", 0.6)],
        ),
    ]


# ── Throughput ─────────────────────────────────────────────────────────────

def _throughput() -> list[dict]:
    return [
        P.timeseries(
            "Bytes in /s per broker",
            "Ingest per broker. Under a steady producer load the lines should "
            "sit on top of each other; a broker consistently above the others "
            "is holding more of the leadership, which 'Leader count per "
            "broker' in the Replication section confirms, and a broker "
            "consistently below is one the producers are not reaching — a "
            "partition assignment that is not spread, or a client pinned to a "
            "subset of the brokers. Corrected series: `…_bytesin_total`, a "
            "`PerSec` meter published as a counter, so `rate()`.",
            P.targets(("sum by (%s) (rate(kafka_server_brokertopicmetrics_"
                       "bytesin_total{%s}[5m]))" % (POD, AGG), "{{%s}}" % POD)),
            unit="Bps", w=12,
        ),
        P.timeseries(
            "Bytes out /s per broker",
            "Egress to clients per broker, excluding replication. Fetches are "
            "served by the leader, so this follows leadership the same way "
            "ingest does — and a consumer group that has just rebalanced will "
            "visibly move the load from one broker to another. A broker whose "
            "egress collapses while its ingest holds is one whose consumers "
            "have stopped fetching from it, which during a test usually means "
            "the consumer side of the harness is the bottleneck, not the "
            "cluster.",
            P.targets(("sum by (%s) (rate(kafka_server_brokertopicmetrics_"
                       "bytesout_total{%s}[5m]))" % (POD, AGG), "{{%s}}" % POD)),
            unit="Bps", w=12,
        ),
        P.timeseries(
            "Messages in /s per broker",
            "Records per second per broker. It diverges from the byte panel "
            "whenever record sizes are uneven across partitions, which is "
            "what a keyed producer with a skewed key distribution does: one "
            "broker takes most of the messages while the byte rates look "
            "balanced, or the reverse. When the two panels disagree, the "
            "partitioning is the thing to look at.",
            P.targets(("sum by (%s) (rate(kafka_server_brokertopicmetrics_"
                       "messagesin_total{%s}[5m]))" % (POD, AGG), "{{%s}}" % POD)),
            unit="short", w=12,
        ),
        P.timeseries(
            "Cluster throughput, in and out",
            "The two totals on one plot — the single line a load test is "
            "reported against. The shape matters as much as the height: a "
            "flat ceiling is a limit somewhere (the Broker internals section "
            "says whether it is the brokers), a sawtooth is a producer "
            "backing off and recovering, and a slow decline over a long run "
            "is usually the log growing past the page cache so that fetches "
            "start reaching the disk.",
            P.targets(
                ('label_replace(sum(rate(kafka_server_brokertopicmetrics_'
                 'bytesin_total{%s}[5m])), "kind", "in", "", "") or '
                 'label_replace(sum(rate(kafka_server_brokertopicmetrics_'
                 'bytesout_total{%s}[5m])), "kind", "out", "", "")' % (AGG, AGG),
                 "{{kind}}")),
            unit="Bps", w=12,
        ),
        P.timeseries(
            "Throughput by zone",
            "Ingest and egress summed per availability zone. The `zone` label "
            "reaches the series for the first time in this refactor — the node "
            "pools have always set the pod label and "
            "`kafka-common.strimziRelabelings` only carried `strimzi_io_*`, "
            "which is why every legacy legend that said `({{zone}})` rendered "
            "as `()`. On a three-AZ cluster the three ingest lines should "
            "match; egress skewed towards one zone is consumers reading "
            "across a zone boundary, which costs real money on a managed "
            "cloud and shows up in a bill long before it shows up in a "
            "latency graph. A cluster whose pods carry no `zone` label draws "
            "one unlabelled line.",
            P.targets(
                ('label_replace(sum by (zone) (rate(kafka_server_brokertopicmetrics_'
                 'bytesin_total{%s}[5m])), "kind", "in", "", "") or '
                 'label_replace(sum by (zone) (rate(kafka_server_brokertopicmetrics_'
                 'bytesout_total{%s}[5m])), "kind", "out", "", "")' % (AGG, AGG),
                 "{{zone}} {{kind}}")),
            unit="Bps", w=12,
        ),
        P.timeseries(
            "Produce and fetch requests /s",
            "Client requests per second, not records. Divided into the message "
            "rate it gives the effective batch size, which is the lever that "
            "matters most in a producer benchmark: the same throughput at a "
            "tenth of the request rate costs the brokers roughly a tenth of "
            "the request-handling work. A request rate that climbs while "
            "throughput is flat is a producer whose batching has stopped "
            "working — usually `linger.ms` at zero with a slow application "
            "loop.",
            P.targets(
                ('label_replace(sum(rate(kafka_server_brokertopicmetrics_'
                 'totalproducerequests_total{%s}[5m])), "kind", "produce", "", "") or '
                 'label_replace(sum(rate(kafka_server_brokertopicmetrics_'
                 'totalfetchrequests_total{%s}[5m])), "kind", "fetch", "", "")'
                 % (AGG, AGG), "{{kind}}")),
            unit="reqps", w=12,
        ),
        P.timeseries(
            "Failed produce and fetch /s",
            "The requests the brokers rejected. During a load test this is the "
            "honesty check on every other panel: throughput measured while "
            "produce requests are failing is throughput the cluster did not "
            "actually accept. Failed produce is usually NOT_ENOUGH_REPLICAS "
            "(the ISR panels below) or a timeout; failed fetch is usually a "
            "consumer asking for an offset that retention has already "
            "deleted. The KRaft Operations board breaks both out by error "
            "code.",
            P.targets(
                ('label_replace(sum(rate(kafka_server_brokertopicmetrics_'
                 'failedproducerequests_total{%s}[5m])), "kind", "produce", "", "") or '
                 'label_replace(sum(rate(kafka_server_brokertopicmetrics_'
                 'failedfetchrequests_total{%s}[5m])), "kind", "fetch", "", "")'
                 % (AGG, AGG), "{{kind}}")),
            unit="reqps", w=12, thresholds=GREEN,
        ),
    ]


# ── Replication ────────────────────────────────────────────────────────────

def _replication() -> list[dict]:
    return [
        P.timeseries(
            "ISR shrinks and expands /s",
            "Followers dropped from in-sync replica sets, and added back, per "
            "broker. In a load test this is the clearest sign that the "
            "cluster has passed the throughput it can sustain: followers "
            "replicate through the same request handlers that serve clients, "
            "so when the brokers saturate the followers are what falls behind "
            "first. Shrinks matched by expands are a cluster recovering "
            "between bursts; shrinks without expands mean the ceiling was "
            "passed and stayed passed. The corrected series are "
            "`…_isrshrinks_total` and `…_isrexpands_total` — `PerSec` meters "
            "published as counters, so `rate()`; the board this replaces read "
            "`…_isrshrinkspersec`, which no rule produces.",
            P.targets(
                ('label_replace(sum by (%s) (rate(kafka_server_replicamanager_'
                 'isrshrinks_total{%s}[5m])), "kind", "shrink", "", "") or '
                 'label_replace(sum by (%s) (rate(kafka_server_replicamanager_'
                 'isrexpands_total{%s}[5m])), "kind", "expand", "", "")'
                 % (POD, SEL, POD, SEL), "{{%s}} {{kind}}" % POD)),
            unit="short", w=12,
        ),
        P.timeseries(
            "Under-replicated partitions over time",
            "The same quantity as the header stat, per broker and over the "
            "run. The shape is the useful part: a step that appears when the "
            "producer rate increases and disappears when it drops is the "
            "cluster's throughput ceiling, marked exactly. A count that only "
            "ever climbs is a follower that has stopped replicating rather "
            "than one that is merely behind.",
            P.targets(("sum by (%s) (kafka_server_replicamanager_"
                       "underreplicatedpartitions{%s})" % (POD, SEL),
                       "{{%s}}" % POD)),
            unit="short", w=12, thresholds=GREEN_RED_1, threshold_style="line",
        ),
        P.timeseries(
            "Leader count per broker",
            "Partition leaderships per broker. It is the denominator for every "
            "per-broker panel above: a broker leading twice as many "
            "partitions should be taking twice the traffic, and a throughput "
            "imbalance is only an imbalance once this is flat. It steps at "
            "every restart and every preferred-leader election, and a run "
            "whose leadership moved halfway through is a run whose "
            "per-broker numbers cannot be compared across the whole window.",
            P.targets(("sum by (%s) (%s{%s})" % (POD, ANCHOR, SEL),
                       "{{%s}}" % POD)),
            unit="short", w=12,
        ),
        P.timeseries(
            "Leaders by zone",
            "Partition leaderships per availability zone — the zone-level view "
            "of the panel beside it, and the one that says whether a "
            "three-AZ cluster is actually being exercised evenly. A zone "
            "holding most of the leadership means most producer traffic is "
            "crossing a zone boundary, which shows up as latency in a "
            "benchmark and as cross-AZ transfer on a bill.",
            P.targets(("sum by (zone) (%s{%s})" % (ANCHOR, SEL), "{{zone}}")),
            unit="short", w=12,
        ),
        P.timeseries(
            "Replication traffic",
            "Bytes per second moving between brokers to replicate, in and "
            "out. On a three-replica topic this should be about twice the "
            "client ingest, and that is the point of having it on a "
            "performance board: it is the traffic a capacity plan forgets. It "
            "shares the network and the request handlers with client traffic, "
            "so when 'Cluster throughput' flattens, this panel says how much "
            "of the ceiling the cluster is spending on itself.",
            P.targets(
                ('label_replace(sum(rate(kafka_server_brokertopicmetrics_'
                 'replicationbytesin_total{%s}[5m])), "kind", "in", "", "") or '
                 'label_replace(sum(rate(kafka_server_brokertopicmetrics_'
                 'replicationbytesout_total{%s}[5m])), "kind", "out", "", "")'
                 % (SEL, SEL), "{{kind}}")),
            unit="Bps", w=12,
        ),
    ]


# ── Broker internals ───────────────────────────────────────────────────────

def _internals() -> list[dict]:
    return [
        P.timeseries(
            "Request handler idle (windowed)",
            "The fraction of the last five minutes each broker's request "
            "handler threads spent idle. THIS is the saturation panel; below "
            "%.2f for 10 minutes is KafkaRequestHandlerSaturated and the line "
            "is drawn there.\n\n"
            "The series is `…requesthandleravgidlepercent_count_total`, and "
            "its name is misleading twice over. It is the Yammer meter's "
            "`Count`, which the exporter names `_count` and then suffixes "
            "`_total` because the rule types it COUNTER — and the value is not "
            "a count of anything. Kafka accumulates idle NANOSECONDS into it. "
            "So `rate(...[5m])` yields idle nanoseconds per second and the "
            "division by 1e9 turns that into a fraction of the window. The "
            "alert computes exactly this."
            % HANDLER_IDLE_RATIO,
            P.targets(("avg by (%s) (rate(kafka_server_kafkarequesthandlerpool_"
                       "requesthandleravgidlepercent_count_total{%s}[5m])) / 1e9"
                       % (POD, SEL), "{{%s}}" % POD)),
            unit="percentunit", w=12, min_value=0, max_value=1,
            thresholds=[("red", None), ("green", HANDLER_IDLE_RATIO)],
            threshold_style="line",
        ),
        P.timeseries(
            "Request handler idle (lifetime mean)",
            "The same meter's `MeanRate`, which is where the exporter's "
            "Percent rule publishes it: "
            "`…_requesthandleravgidle_percent`, with the underscore the rule "
            "inserts and that the legacy boards left out — one of the seven "
            "names that never existed. It is the corrected spelling and it is "
            "STILL the wrong panel to judge a load test by, which is why it "
            "sits beside the windowed one rather than replacing it: a "
            "`MeanRate` is averaged over the whole life of the broker, so a "
            "node that has been up for a week and saturated for an hour reads "
            "a comfortable number and barely moves. Use it for one thing — "
            "when the two panels diverge, the gap is how unusual the current "
            "load is compared with everything this broker has ever served.",
            P.targets(("avg by (%s) (kafka_server_kafkarequesthandlerpool_"
                       "requesthandleravgidle_percent{%s})" % (POD, SEL),
                       "{{%s}}" % POD)),
            unit="percentunit", w=12, min_value=0, max_value=1,
        ),
        P.timeseries(
            "Network processor idle",
            "The fraction of its time each broker's network threads spent "
            "idle. These read and write the sockets; the handler threads do "
            "the work behind them. In a load test the two saturate for "
            "different reasons — handlers on request VOLUME and work, network "
            "threads on connection count, TLS and raw bytes — so a run that "
            "exhausts the network threads first is one to rerun with fewer, "
            "busier client connections. Corrected name: "
            "`…_networkprocessoravgidle_percent`, a plain 0–1 `Value` gauge "
            "that takes no `rate()`.",
            P.targets(("avg by (%s) (kafka_network_socketserver_"
                       "networkprocessoravgidle_percent{%s})" % (POD, SEL),
                       "{{%s}}" % POD)),
            unit="percentunit", w=12, min_value=0, max_value=1,
        ),
        P.timeseries(
            "Request and response queue size",
            "Requests waiting for a handler thread and responses waiting for a "
            "network thread. KafkaRequestQueueSaturated fires above %d for 10 "
            "minutes. A growing request queue is the direct consequence of the "
            "idle ratio hitting zero, and it is where a benchmark's latency "
            "percentiles come from: the broker is not slow at the work, it "
            "has not started it yet. This is the panel that distinguishes "
            "'the cluster is at capacity' from 'the client is not offering "
            "enough load'."
            % REQUEST_QUEUE_SIZE,
            P.targets(
                ('label_replace(max by (%s) (kafka_network_requestchannel_'
                 'requestqueuesize{%s}), "kind", "request", "", "") or '
                 'label_replace(max by (%s) (kafka_network_requestchannel_'
                 'responsequeuesize{%s}), "kind", "response", "", "")'
                 % (POD, SEL, POD, SEL), "{{%s}} {{kind}}" % POD)),
            unit="short", w=12,
            thresholds=[("green", None), ("red", REQUEST_QUEUE_SIZE)],
            threshold_style="line",
        ),
        P.timeseries(
            "Request time p99 by type",
            "The 99th percentile of total in-broker time per request type, in "
            "milliseconds — the number a benchmark reports as its latency, "
            "measured by the broker rather than the client. "
            "KafkaRequestLatencyHigh fires above %d ms for 10 minutes. Produce "
            "rising alone on an `acks=all` test is the ISR waiting; both "
            "rising together is saturation. **The percentile is a "
            "pre-computed gauge with a `quantile` label and no `_sum` "
            "series** — select the quantile, never `histogram_quantile`."
            % REQUEST_P99_MS,
            P.targets(("max by (request) (kafka_network_requestmetrics_totaltimems"
                       '{%s, quantile="0.99"})' % SEL, "{{request}}")),
            unit="ms", w=12,
            thresholds=[("green", None), ("red", REQUEST_P99_MS)],
            threshold_style="line",
        ),
        P.timeseries(
            "Log flush p99",
            "The 99th percentile time to flush a segment to disk. "
            "KafkaLogFlushLatencyHigh fires above %d ms for 10 minutes. On a "
            "performance board this is the panel that separates a cluster "
            "that is CPU-bound from one that is disk-bound, and the two have "
            "completely different answers: more brokers against saturated "
            "handlers, faster volumes against slow flushes. A p99 that climbs "
            "as the run goes on, with throughput flat, is the page cache "
            "filling and writeback starting to reach the device."
            % LOG_FLUSH_P99_MS,
            P.targets(("max by (%s) (kafka_log_logflushstats_logflushrateandtimems"
                       '{%s, quantile="0.99"})' % (POD, SEL), "{{%s}}" % POD)),
            unit="ms", w=12,
            thresholds=[("green", None), ("red", LOG_FLUSH_P99_MS)],
            threshold_style="line",
        ),
    ]


# ── JVM ────────────────────────────────────────────────────────────────────

def _jvm() -> list[dict]:
    return [
        P.timeseries(
            "Heap and non-heap memory",
            "Broker heap in use, how much the JVM has committed from the "
            "operating system, and everything the JVM holds that is not heap. "
            "Every term carries `or` over the two exporter spellings — the "
            "JMX exporter agent renamed `jvm_memory_bytes_used` to "
            "`jvm_memory_used_bytes` in its 1.x line, and which one a given "
            "image ships is not something a panel should have to know. "
            "`kafka-working` read `java_lang_memory_heapmemoryusage_used`, the "
            "raw MBean spelling, which the agent never publishes under that "
            "name. Used climbing to meet committed and staying there is a "
            "broker about to spend its time in GC rather than on requests — "
            "the next panel.\n\n"
            "`non-heap used` is metaspace, the code cache and the compressed "
            "class space. It is flat on a healthy broker and it is the line "
            "that moves when a plugin, an authorizer or an interceptor leaks "
            "classes — which a heap graph cannot show and `-Xmx` does not "
            "bound. `kafka-jvm-dashboard.json` was the only one of the nine "
            "deleted boards that plotted it; `kafka-perf-global`'s JVM row was "
            "heap-only and the Strimzi operator's `strimzi-kafka.json` sums "
            "`jvm_memory_used_bytes` across areas without breaking them out, "
            "so without this series the number is plotted nowhere. `Resident "
            "memory and heap` below shows the wider gap it sits inside.",
            P.targets(
                ('label_replace(sum by (%s) (jvm_memory_used_bytes{area="heap", %s} '
                 'or jvm_memory_bytes_used{area="heap", %s}), "kind", "heap used", "", "") '
                 'or label_replace(sum by (%s) (jvm_memory_committed_bytes'
                 '{area="heap", %s} or jvm_memory_bytes_committed{area="heap", %s}), '
                 '"kind", "heap committed", "", "") '
                 'or label_replace(sum by (%s) (jvm_memory_used_bytes{area="nonheap", %s} '
                 'or jvm_memory_bytes_used{area="nonheap", %s}), '
                 '"kind", "non-heap used", "", "")'
                 % (POD, SEL, SEL, POD, SEL, SEL, POD, SEL, SEL),
                 "{{%s}} {{kind}}" % POD)),
            unit="bytes", w=12,
        ),
        P.timeseries(
            "GC time fraction",
            "Seconds spent in garbage collection per second of wall clock, per "
            "collector. It is the honest form of 'is GC hurting us': 0.02 is "
            "two per cent of the broker's time, and anything approaching 0.1 "
            "is a broker that will miss heartbeats and drop out of ISRs. "
            "`jvm_gc_collection_seconds_sum` is a true counter of accumulated "
            "GC seconds, so `rate()` over it is a dimensionless fraction "
            "despite the unit picker calling it a percentage.",
            P.targets(("sum by (%s, gc) (rate(jvm_gc_collection_seconds_sum{%s}[5m]))"
                       % (POD, SEL), "{{%s}} {{gc}}" % POD)),
            unit="percentunit", w=12,
        ),
        P.timeseries(
            "GC collections /s",
            "How often each collector runs. Read with the panel beside it: "
            "many short collections is a healthy young generation keeping up "
            "with allocation, and few long ones is the shape that stalls a "
            "broker. A run whose collection rate climbs linearly with "
            "throughput is allocating per record, which for Kafka usually "
            "means decompression and re-compression because the producer and "
            "the topic disagree about the codec.",
            P.targets(("sum by (%s, gc) (rate(jvm_gc_collection_seconds_count{%s}[5m]))"
                       % (POD, SEL), "{{%s}} {{gc}}" % POD)),
            unit="short", w=12,
        ),
        P.timeseries(
            "Thread count",
            "Live JVM threads per broker. Mostly flat — the pools are sized by "
            "configuration — so it is a configuration panel rather than a "
            "load panel: it is where `num.network.threads` and "
            "`num.io.threads` become visible, and where a leak in a custom "
            "plugin, an authorizer or an interceptor shows up as a line that "
            "only climbs.",
            P.targets(("sum by (%s) (jvm_threads_current{%s})" % (POD, SEL),
                       "{{%s}}" % POD)),
            unit="short", w=12,
        ),
    ]


# ── Resource usage ─────────────────────────────────────────────────────────

def _resources() -> list[dict]:
    return [
        P.timeseries(
            "CPU seconds /s",
            "CPU consumed by the broker process, in cores. `process_cpu_"
            "seconds_total` is the JVM's own accounting rather than the "
            "container runtime's, so it measures the broker and not the "
            "sidecars — which is what a benchmark wants, and it is also why "
            "it will not show CFS throttling. A run that plateaus with this "
            "at the pod's CPU limit is CPU-bound; one that plateaus well below "
            "it is bound by something else, and the Broker internals section "
            "says which.",
            P.targets(("rate(process_cpu_seconds_total{%s}[5m])" % SEL,
                       "{{%s}}" % POD)),
            unit="short", w=12,
        ),
        P.timeseries(
            "Resident memory and heap",
            "The process's total resident set against the heap inside it. The "
            "gap is everything the JVM uses that is not heap — thread stacks, "
            "metaspace, code cache and, above all, the direct byte buffers "
            "Kafka's network layer allocates. That gap is why a Kafka broker "
            "sized only by `-Xmx` gets OOM-killed under load while its heap "
            "graph looks comfortable, and this is the panel that shows it "
            "happening. The page cache holding the log is NOT in either "
            "series; it is the kernel's, and it is the memory that actually "
            "makes fetches fast.",
            P.targets(
                ('label_replace(sum by (%s) (process_resident_memory_bytes{%s}), '
                 '"kind", "rss", "", "") or label_replace(sum by (%s) '
                 '(jvm_memory_used_bytes{area="heap", %s} or '
                 'jvm_memory_bytes_used{area="heap", %s}), "kind", "heap", "", "")'
                 % (POD, SEL, POD, SEL, SEL), "{{%s}} {{kind}}" % POD)),
            unit="bytes", w=12,
        ),
        P.timeseries(
            "Open file descriptors",
            "Descriptors the broker process holds. Kafka spends them on two "
            "things that both scale with a load test: one per log segment "
            "file — so partitions times segments — and one per client "
            "connection. Running out does not produce a clean failure; it "
            "produces log directories going offline and connections being "
            "refused, which read on every other panel as a storage fault and "
            "a client fault respectively. A line climbing steadily through a "
            "long run is the segment count, and a step is a wave of new "
            "clients.",
            P.targets(("sum by (%s) (process_open_fds{%s})" % (POD, SEL),
                       "{{%s}}" % POD)),
            unit="short", w=12,
        ),
    ]


# ── Topic detail ───────────────────────────────────────────────────────────

def _topic() -> list[dict]:
    return [
        P.stat(
            "Records in selection",
            "Total records in the selected topics, counted once per partition "
            "rather than once per replica. The legacy board summed "
            "`kafka_log_log_logendoffset` across everything, which counts "
            "every record once for every replica and reports three times the "
            "truth on a three-replica topic; `max by (topic, partition)` "
            "collapses the replicas first. It is a log-end offset, so it "
            "counts records ever written and not records still retained — "
            "compare it with the panel that pairs start and end offsets when "
            "retention is deleting.",
            P.targets("sum(max by (topic, partition) "
                      "(kafka_log_log_logendoffset{%s}))" % TOPIC_SEL),
            unit="short", w=6, h=5, thresholds=GREEN,
        ),
        P.stat(
            "Bytes on disk in selection",
            "Bytes the selected topics occupy across the cluster. Unlike the "
            "record count this is deliberately summed over every replica, "
            "because that is the disk the cluster actually spends: a "
            "three-replica topic costs three times its data. Divide it by the "
            "record count for the on-disk size per record, which includes "
            "the batch headers and whatever compression achieved — usually "
            "very different from the producer's payload size.",
            P.targets("sum(kafka_log_log_size{%s})" % TOPIC_SEL),
            unit="bytes", w=6, h=5, thresholds=GREEN,
        ),
        P.timeseries(
            "Bytes in /s by topic",
            "Ingest per topic, from the per-topic registration of "
            "`BrokerTopicMetrics`. `topic!=\"\"` excludes the broker-wide "
            "aggregate series, which shares the metric name and would appear "
            "as an unlabelled line equal to the sum of all the others. This "
            "is the panel that says whether a load generator is writing where "
            "it was told to, and it is the one to watch when a run's "
            "throughput is right in total and wrong per topic.",
            P.targets(("sum by (topic) (rate(kafka_server_brokertopicmetrics_"
                       "bytesin_total{%s}[5m]))" % BY_TOPIC, "{{topic}}")),
            unit="Bps", w=12,
        ),
        P.timeseries(
            "Messages in /s by topic",
            "Records per second per topic. Against the byte panel beside it "
            "this gives the per-topic record size, which is the quickest way "
            "to confirm that a test is producing the payload it thinks it is "
            "— a run that is a hundred times cheaper than expected is usually "
            "one whose payload never got past the generator's defaults.",
            P.targets(("sum by (topic) (rate(kafka_server_brokertopicmetrics_"
                       "messagesin_total{%s}[5m]))" % BY_TOPIC, "{{topic}}")),
            unit="short", w=12,
        ),
        P.timeseries(
            "Log end offset per partition",
            "The highest offset written to each partition, per replica. The "
            "single most useful panel during a run: the SLOPE is the "
            "per-partition write rate, and parallel lines mean the producer's "
            "partitioner is spreading the load. One partition climbing faster "
            "than the rest is a key skew — the classic cause of a load test "
            "that cannot reach its target no matter how many brokers are "
            "added, because one partition's leader is doing all the work. "
            "Every replica of a partition draws its own line, and followers "
            "trailing visibly behind their leader is replication lag made "
            "visible.",
            P.targets(("kafka_log_log_logendoffset{%s}" % TOPIC_SEL,
                       "{{topic}} p{{partition}} {{%s}}" % POD)),
            unit="short", w=12,
        ),
        P.timeseries(
            "Partition size on disk",
            "Bytes per partition per replica. It should track the offset "
            "panel; where it does not, records are of uneven size or "
            "compression is working differently across partitions. The line "
            "that matters during a long run is the one that STOPS growing "
            "while its offsets keep climbing: that is retention deleting from "
            "the head as fast as the producer appends to the tail, and it "
            "means the test is no longer measuring what it thinks it is.",
            P.targets(("kafka_log_log_size{%s}" % TOPIC_SEL,
                       "{{topic}} p{{partition}} {{%s}}" % POD)),
            unit="bytes", w=12,
        ),
        P.timeseries(
            "Log segments per partition",
            "How many segment files each partition is made of. It steps by one "
            "every `segment.bytes` or `segment.ms`, so the step rate is "
            "another read of the write rate — and the absolute number is a "
            "cost: every segment is an open file descriptor and an index in "
            "memory on the broker. A partition whose segment count only ever "
            "grows has retention that is not deleting, and it will end at the "
            "file-descriptor panel in the Resource usage section.",
            P.targets(("kafka_log_log_numlogsegments{%s}" % TOPIC_SEL,
                       "{{topic}} p{{partition}} {{%s}}" % POD)),
            unit="short", w=12,
        ),
        P.timeseries(
            "Log start offset per partition",
            "The oldest offset still on disk. Flat means nothing has been "
            "deleted yet; a line that starts climbing is retention taking "
            "effect, and the gap between it and the log end offset is how "
            "many records the partition actually still holds. It is the panel "
            "that explains a consumer benchmark suddenly reporting "
            "OFFSET_OUT_OF_RANGE: a consumer that fell behind the start "
            "offset has had its history deleted underneath it, which in a "
            "long soak test is a result rather than a fault.",
            P.targets(("kafka_log_log_logstartoffset{%s}" % TOPIC_SEL,
                       "{{topic}} p{{partition}} {{%s}}" % POD)),
            unit="short", w=12,
        ),
        P.table(
            "Partition detail",
            "A snapshot of the selected topics: one row per partition per "
            "broker, with the bytes that broker holds for it. Read it for "
            "placement rather than for volume — the row count per partition "
            "is its replication factor, a partition missing a row is a "
            "replica that is not there, and rows of visibly different sizes "
            "for the same partition are replicas that have not converged. It "
            "is the same instant view `kafka-perf-test`, `kafka-performance` "
            "and `kafka-working` each carried their own copy of.",
            [P.target("max by (topic, partition, %s) (kafka_log_log_size{%s})"
                      % (POD, TOPIC_SEL), instant=True, fmt="table")],
            w=12, transformations=_TABLE_TRANSFORMS,
        ),
    ]


def build(variant: str) -> list[Row]:
    return [
        Row("", _header()),
        Row("Throughput", _throughput()),
        Row("Replication", _replication()),
        Row("Broker internals", _internals()),
        Row("JVM", _jvm()),
        Row("Resource usage", _resources()),
        Row("Topic detail ($topic)", _topic()),
    ]
