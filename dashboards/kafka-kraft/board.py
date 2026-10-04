"""Kafka — KRaft Operations.

Kafka 4.x runs on KRaft, and until this board existed not one of the
repository's thirteen dashboards read a single `raft` or `brokermetadata`
series. The framing was entirely ZooKeeper-era — broker counts, partitions,
JVM — and nothing about the quorum that now runs the cluster. Four of
charts/kafka-cluster's own alerts (KafkaRaftLeaderElections,
KafkaRaftUnknownVoters, KafkaBrokerMetadataLag, KafkaFencedBrokers) fired into
a board that showed none of their inputs.

Read top to bottom, in the order an incident asks its questions:

  (header)               the six numbers a page lands on
  Quorum and metadata    is the quorum electing, committing and propagating
  Cluster health         is there a controller, and is anything offline
  Replication            are the followers keeping up, and where
  Request path           can the brokers serve what the clients ask
  Storage                is the log healthy underneath all of it
  Tiered storage         the remote tier, when it is on (collapsed)
  Security and connections  who is failing to authenticate, and how often

THIS IS NOT A REBUILD OF strimzi-kafka.json. The Strimzi operator ships nine
boards and `charts/strimzi-operator` enables them by default; `strimzi-kafka`
covers broker health better than everything this repository shipped, and
`strimzi-kraft` covers quorum *identity* — state, leader, vote, epoch, high
watermark, append and fetch rates, commit latency average. Those series are
deliberately almost absent here. What is here is the other half: the
degradation signals neither board has — metadata lag and metadata errors, the
uncommitted-record gap, unreachable voters, election latency, fenced brokers,
controller queue time, ISR dynamics, per-error-code request failures and the
security counters.

Three naming traps run through the descriptions, because each one already
produced a dead panel somewhere in this repository:

  1. `_max_total` IS NOT A COUNTER. The exporter's 1.x line appends `_total`
     to anything a COUNTER rule names, and the KRaft rules type `.+-max` as
     COUNTER. So `kafka_server_raftmetrics_commit_latency_max_total` is a max
     GAUGE wearing a counter's name. Never rate() it.
  2. `_count_total` IS THE METER'S Count, AND THE UNIT IS NOT ALWAYS A COUNT.
     `…requesthandleravgidlepercent_count_total` is cumulative NANOSECONDS, so
     the windowed idle ratio is rate(...[5m]) / 1e9.
  3. PERCENTILES ARE PRE-COMPUTED GAUGES with a `quantile` label and no
     `_sum`. Select the quantile; never histogram_quantile.

Every series is checked against the rules that produce it by
`scripts/check-metric-contract.sh kafka-cluster`, which runs the vendored
exporter rules (charts/kafka-cluster/files/metrics/kafka-metrics.yaml) over the
bean catalogue in scripts/metric-contract/kafka-cluster.yaml. No name on this
board was derived from a JMX bean name; guessing from bean names is what put
eleven series that never existed on the boards this one replaces.
"""

from __future__ import annotations

import panels as P
from layout import Row

# ── Scope ──────────────────────────────────────────────────────────────────
# Byte-identical to the `$k` selector every alert in
# charts/kafka-cluster/templates/prometheusrule.yaml carries, so a page maps
# onto a panel without a translation step. `strimzi_io_name` is what keeps
# Cruise Control, the Kafka Exporter and the entity operator — which share the
# namespace and the cluster label, and publish their own `jvm_*` and `up` —
# out of every expression here.
SEL = ('namespace="$namespace", strimzi_io_cluster="$cluster", '
       'strimzi_io_name="$cluster-kafka"')

# The alerts label their subject with `kubernetes_pod_name`, one of
# kafka-common.strimziRelabelings' five target labels. Every per-node legend
# on this board uses the same one for the same reason.
POD = "kubernetes_pod_name"

# A series only a KRaft controller publishes, and exactly one per cluster:
# enough to populate both variables, and if it is missing this board has
# nothing to say anyway.
ANCHOR = "kafka_controller_kafkacontroller_activecontrollercount"


def zero_if_live(expr: str) -> str:
    """A zero fallback that a dead scrape cannot reach. See P.or_zero."""
    return P.or_zero(expr, "%s{%s}" % (ANCHOR, SEL))


# ── Thresholds, frozen from charts/kafka-cluster's alerts.thresholds ───────
# A Grafana threshold is not templatable. Each of these is the DEFAULT the
# chart ships; a release that moves one gets the alert it asked for and a line
# on the panel that has not moved with it. README.md names every one.
RAFT_ELECTIONS_15M = 3        # alerts.thresholds.raftElectionsPer15m
METADATA_LAG_MS = 60000       # alerts.thresholds.metadataLagMs
REQUEST_P99_MS = 1000         # alerts.thresholds.requestLatencyP99Ms
HANDLER_IDLE_RATIO = 0.3      # alerts.thresholds.handlerIdleRatio
REQUEST_QUEUE_SIZE = 100      # alerts.thresholds.requestQueueSize
LOG_FLUSH_P99_MS = 500        # alerts.thresholds.logFlushP99Ms
# alerts.slo.burnRate (14.4) × (1 − alerts.slo.target (0.999)).
SLO_BUDGET = 0.0144

# The error codes charts/kafka-cluster's availability SLI counts as the
# cluster's own fault. Client mistakes — unknown topic, authorization — are
# deliberately not in it.
SERVER_ERRORS = ("NOT_ENOUGH_REPLICAS|NOT_ENOUGH_REPLICAS_AFTER_APPEND|"
                 "REQUEST_TIMED_OUT|KAFKA_STORAGE_ERROR|UNKNOWN_SERVER_ERROR|"
                 "LEADER_NOT_AVAILABLE|COORDINATOR_NOT_AVAILABLE|NETWORK_EXCEPTION")

GREEN = [("green", None)]
GREEN_RED_1 = [("green", None), ("red", 1)]


def variables(variant: str) -> list[dict]:
    """`$namespace` and `$cluster` — which Kafka cluster this board is of.

    Real dropdowns rather than the hidden `constant` variables the MirrorMaker
    2 boards use, because the PodMonitor's relabelings put `strimzi_io_cluster`
    on every series here: one static file serves every Kafka cluster in a
    Grafana, and `scripts/gen-dashboards.py` writes it into the monitoring
    chart unmodified.
    """
    return [
        P.query_var("namespace", "label_values(%s, namespace)" % ANCHOR,
                    label="Namespace"),
        P.query_var("cluster",
                    'label_values(%s{namespace="$namespace"}, strimzi_io_cluster)'
                    % ANCHOR,
                    label="Kafka cluster"),
    ]


# ── The header ─────────────────────────────────────────────────────────────

def _header() -> list[dict]:
    return [
        P.stat(
            "Active controllers",
            "How many nodes claim to be the active controller right now. The "
            "answer is 1 on a healthy cluster and the alert "
            "KafkaActiveControllerCount fires on anything else after 3 "
            "minutes. Zero means the quorum has no leader: no topic is "
            "created, no leader is elected, no broker is fenced or unfenced, "
            "and every existing leader keeps serving — which is why a cluster "
            "with no controller can look entirely healthy from a client. Two "
            "means a split brain that KRaft's epoch fencing should make "
            "impossible, so it is nearly always two clusters' series sharing "
            "one `$cluster` value rather than a real double election.",
            P.targets("sum(%s{%s})" % (ANCHOR, SEL)),
            unit="short", thresholds=[("red", None), ("green", 1), ("red", 2)],
        ),
        P.stat(
            "Quorum epoch",
            "The controller quorum's current term. KRaft bumps it by one at "
            "every leader election, so the number itself means nothing and "
            "its rate of change means everything: a flat epoch is a stable "
            "quorum. `max` rather than `sum` because every voter reports the "
            "epoch it believes in, and during an election they disagree.",
            P.targets("max(kafka_server_raftmetrics_current_epoch{%s})" % SEL),
            unit="short", thresholds=GREEN, graph_mode="area",
        ),
        P.stat(
            "Unreachable voters",
            "Voters a node has in its quorum configuration and cannot open a "
            "connection to. Above zero for 10 minutes is KafkaRaftUnknownVoters. "
            "Until this board existed the alert had nowhere to point: the "
            "series was on no dashboard, upstream or local. A quorum of three "
            "survives one unreachable voter and loses its majority at two, so "
            "this is the number that says how much margin is left before "
            "metadata writes stop. Zero here is a measurement, not a default: "
            "the fallback is anchored to the controller's own series, so a "
            "cluster nothing is scraping reads No data rather than green.",
            P.targets(zero_if_live(
                "sum(kafka_server_raftmetrics_number_unknown_voter_connections"
                "{%s})" % SEL)),
            unit="short", thresholds=GREEN_RED_1,
        ),
        P.stat(
            "Metadata lag (worst node)",
            "How far behind the controller the slowest broker's applied "
            "metadata is, in milliseconds. This is the KRaft health number on "
            "the broker side and KafkaBrokerMetadataLag fires above "
            "%d ms for 5 minutes. A broker that is behind on metadata replay "
            "serves a stale view of topics, ACLs and leadership while looking "
            "perfectly healthy on every ZooKeeper-era panel there is — it is "
            "up, it is leading partitions, its JVM is fine, and it is "
            "answering with yesterday's configuration."
            % METADATA_LAG_MS,
            P.targets("max(kafka_server_brokermetadatametrics_last_applied_record_lag_ms"
                      "{%s})" % SEL),
            unit="ms",
            thresholds=[("green", None), ("yellow", METADATA_LAG_MS / 4),
                        ("red", METADATA_LAG_MS)],
        ),
        P.stat(
            "Fenced brokers",
            "Brokers the controller has registered and fenced. Above zero for "
            "5 minutes is KafkaFencedBrokers. Fencing is KRaft-only and has no "
            "ZooKeeper analogue: the broker process is running, its liveness "
            "and readiness probes pass, its metrics endpoint answers, and the "
            "controller has taken every partition leadership away from it "
            "because its heartbeat lapsed or it has not caught up on metadata "
            "since restarting. That is a very different incident from a broker "
            "that is down, and no pod-level signal distinguishes them. Zero "
            "here is a measurement, not a default — see Unreachable voters.",
            P.targets(zero_if_live(
                "max(kafka_controller_kafkacontroller_fencedbrokercount{%s})" % SEL)),
            unit="short", thresholds=GREEN_RED_1,
        ),
        P.stat(
            "Metadata errors",
            "Metadata records that could not be read, could not be applied, or "
            "that the controller itself rejected — the three error counters "
            "added together. Any of them above zero means a node's view of the "
            "cluster has diverged from the log, which is the one failure mode "
            "where every other panel on this board can be green and the "
            "cluster is still wrong. Nothing alerts on these; the panel in "
            "Quorum and metadata splits them into which kind. Each of the "
            "three carries its own zero fallback, because a node that has "
            "never hit one kind of error publishes no series for it and the "
            "addition would drop the other two with it — and each fallback is "
            "anchored, so all three vanish together when nothing is scraped.",
            P.targets(" + ".join(
                "(%s)" % zero_if_live("sum(%s{%s})" % (metric, SEL))
                for metric in (
                    "kafka_server_brokermetadatametrics_metadata_load_error_count",
                    "kafka_server_brokermetadatametrics_metadata_apply_error_count",
                    "kafka_controller_kafkacontroller_metadataerrorcount",
                ))),
            unit="short", thresholds=GREEN_RED_1,
        ),
    ]


# ── Quorum and metadata ────────────────────────────────────────────────────

_TABLE_TRANSFORMS = [{"id": "organize", "options": {"excludeByName": {"Time": True}}}]


def _quorum() -> list[dict]:
    return [
        P.table(
            "Quorum roles",
            "One row per node with the raft role it currently reports. "
            "`leader` is the active controller, `follower` is a voter "
            "replicating from it, `candidate` is a voter that has called an "
            "election, `unattached` is a voter that has not found a leader "
            "yet, and `observer` is a broker: brokers run a raft client too, "
            "but only to *read* the metadata log, and they never vote. A "
            "cluster with three voters should show exactly one leader and two "
            "followers, and every broker as an observer. The role is a LABEL "
            "on a synthetic `1` — the exporter's rule for `current-state` sets "
            "`value: 1` and puts the string in the label — so a node that "
            "changes role stops publishing the old series rather than "
            "publishing it as zero. This is the only identity panel here; "
            "`strimzi-kraft.json` covers leader, vote, epoch and watermark "
            "properly and this is just the frame for reading the rest.",
            [P.target("max by (%s, current_state) "
                      "(kafka_server_raftmetrics_current_state{%s})" % (POD, SEL),
                      instant=True, fmt="table")],
            w=8, transformations=_TABLE_TRANSFORMS,
        ),
        P.timeseries(
            "Leader elections in 15 minutes",
            "How many times the quorum changed term in the last quarter hour, "
            "counted as changes of the epoch each node believes in. Above %d "
            "is KafkaRaftLeaderElections and the threshold line is that same "
            "number. One election is a controller restart or a rolling update "
            "and is entirely normal. A steady drip of them is a quorum that "
            "cannot hold a leader — most often a voter whose disk cannot "
            "fsync inside the fetch timeout, or an inter-AZ link slow enough "
            "that followers time out the leader — and every election freezes "
            "metadata writes for its duration. `changes()` and not `rate()`: "
            "the epoch is a gauge that happens to increase."
            % RAFT_ELECTIONS_15M,
            P.targets(("max by (%s) (changes(kafka_server_raftmetrics_current_epoch"
                       "{%s}[15m]))" % (POD, SEL), "{{%s}}" % POD)),
            unit="short", w=8,
            thresholds=[("green", None), ("red", RAFT_ELECTIONS_15M)],
            threshold_style="line",
        ),
        P.timeseries(
            "Unreachable voters per node",
            "Per node, the voters in its quorum configuration it cannot "
            "connect to. KafkaRaftUnknownVoters fires above zero for 10 "
            "minutes and the line is drawn at 1. Read this beside 'Quorum "
            "roles': one node reporting unreachable voters while the others "
            "do not is that node's network or DNS, and every node reporting "
            "them at once is the quorum's own listener — a changed "
            "`controller.quorum.voters`, a NetworkPolicy, or a rolling update "
            "that replaced the voter set. Unlike a down broker this has no "
            "pod-level symptom at all: the process is healthy and simply "
            "cannot find its peers.",
            P.targets(("max by (%s) (kafka_server_raftmetrics_"
                       "number_unknown_voter_connections{%s})" % (POD, SEL),
                       "{{%s}}" % POD)),
            unit="short", w=8, thresholds=GREEN_RED_1, threshold_style="line",
        ),
        P.timeseries(
            "Uncommitted metadata records",
            "Records this node has appended to the metadata log and that the "
            "quorum has not yet committed — the node's own log end offset "
            "minus its high watermark. On a healthy quorum this hovers near "
            "zero and spikes for the length of one append. A gap that grows "
            "and does not come back is the signature of a LOST MAJORITY: the "
            "leader keeps accepting metadata changes and can no longer get "
            "them acknowledged by enough voters to commit, so topic "
            "creations, leader elections and broker fencing all quietly stop "
            "taking effect while nothing reports an error. Both halves are "
            "gauges; the subtraction matches on the node label.",
            P.targets(("max by (%s) (kafka_server_raftmetrics_log_end_offset{%s}) "
                       "- max by (%s) (kafka_server_raftmetrics_high_watermark{%s})"
                       % (POD, SEL, POD, SEL), "{{%s}}" % POD)),
            unit="short", w=8,
        ),
        P.timeseries(
            "Metadata commit latency",
            "How long a metadata record takes to go from appended to "
            "committed, as Kafka's own average over its sampling window and "
            "as the worst single commit in that window. **The max series is "
            "named `commit_latency_max_total` and IS NOT A COUNTER.** The "
            "exporter's 1.x line appends `_total` to everything a COUNTER rule "
            "names, and the vendored KRaft rule types `.+-max` as COUNTER to "
            "keep genuinely-monotonic `-total` attributes correctly typed; a "
            "`-max` attribute is swept up by the same pattern and comes out "
            "wearing a counter's suffix. It is a gauge in milliseconds. "
            "`rate()` over it produces a number that means nothing. Upstream's "
            "`strimzi-kraft.json` shows the average; the max is what tells you "
            "whether the average is hiding a stall.",
            P.targets(
                ('label_replace(max by (%s) (kafka_server_raftmetrics_commit_latency_avg'
                 '{%s}), "kind", "avg", "", "") or label_replace(max by (%s) '
                 '(kafka_server_raftmetrics_commit_latency_max_total{%s}), '
                 '"kind", "max", "", "")' % (POD, SEL, POD, SEL),
                 "{{%s}} {{kind}}" % POD)),
            unit="ms", w=8,
        ),
        P.timeseries(
            "Election latency",
            "How long the quorum's last elections took to resolve, average "
            "and worst. This is the number that turns 'the epoch moved' into "
            "'metadata writes were frozen for this long', because nothing is "
            "committed between a leader losing its term and the next leader "
            "taking one. Same `_max_total` trap as commit latency: "
            "`election_latency_max_total` is a max gauge in milliseconds, not "
            "a counter. Read it beside 'Leader elections in 15 minutes' — "
            "three fast elections cost less than one slow one.",
            P.targets(
                ('label_replace(max by (%s) '
                 '(kafka_server_raftmetrics_election_latency_avg{%s}), '
                 '"kind", "avg", "", "") or label_replace(max by (%s) '
                 '(kafka_server_raftmetrics_election_latency_max_total{%s}), '
                 '"kind", "max", "", "")' % (POD, SEL, POD, SEL),
                 "{{%s}} {{kind}}" % POD)),
            unit="ms", w=8,
        ),
        P.timeseries(
            "Broker metadata lag",
            "Per broker, how far behind the controller its applied metadata "
            "is, in milliseconds. KafkaBrokerMetadataLag fires above %d ms for "
            "5 minutes and the line is drawn there. One broker climbing while "
            "the rest are flat is that broker — a slow disk under the metadata "
            "log, or a GC pause long enough to stall replay. Every broker "
            "climbing together is the controller, and the quorum panels above "
            "say why. What makes this the most important number on the board "
            "is that the consequences are all silent: a lagging broker "
            "advertises stale partition leadership, enforces the ACLs it last "
            "read, and has not heard about the topic you created a minute ago. "
            "It is a GAUGE computed by the broker, not a counter."
            % METADATA_LAG_MS,
            P.targets(("max by (%s) (kafka_server_brokermetadatametrics_"
                       "last_applied_record_lag_ms{%s})" % (POD, SEL),
                       "{{%s}}" % POD)),
            unit="ms", w=12,
            thresholds=[("green", None), ("red", METADATA_LAG_MS)],
            threshold_style="line",
        ),
        P.timeseries(
            "Metadata records behind the leader",
            "The same gap as the panel to the left, measured in RECORDS "
            "rather than milliseconds: the highest applied metadata offset "
            "anywhere in the cluster, minus each broker's own. It is here "
            "because the millisecond form is computed from a timestamp inside "
            "the metadata record, so a clock problem — a node whose time "
            "jumped, a virtualised host that lost an NTP step — makes it "
            "meaningless or negative while the record gap stays honest. When "
            "the two panels disagree, believe this one. A broker parked at a "
            "constant positive offset gap has stopped applying metadata "
            "altogether rather than merely fallen behind.",
            P.targets(("scalar(max(kafka_server_brokermetadatametrics_"
                       "last_applied_record_offset{%s})) - max by (%s) "
                       "(kafka_server_brokermetadatametrics_last_applied_record_offset"
                       "{%s})" % (SEL, POD, SEL), "{{%s}}" % POD)),
            unit="short", w=12,
        ),
        P.timeseries(
            "Metadata errors by kind",
            "The three ways metadata handling fails, drawn apart. **load** is "
            "a record the broker could not read at all — corruption in the "
            "metadata log, or a metadata version the running binary does not "
            "understand, which is what a half-finished version bump looks "
            "like. **apply** is worse: the record was read and could not be "
            "applied, so the broker is silently diverged from the cluster and "
            "will stay that way until it is restarted and replays the "
            "snapshot. **controller** is the controller's own error counter. "
            "All three are cumulative gauges that only go up, so a step is one "
            "event; the board shows the value rather than a rate because a "
            "single occurrence matters and a per-second rate of a single event "
            "is unreadable. Nothing alerts on any of them, which is the gap "
            "this panel exists to close.",
            P.targets(
                ('label_replace(sum by (%s) (kafka_server_brokermetadatametrics_'
                 'metadata_load_error_count{%s}), "kind", "load", "", "") or '
                 'label_replace(sum by (%s) (kafka_server_brokermetadatametrics_'
                 'metadata_apply_error_count{%s}), "kind", "apply", "", "") or '
                 'label_replace(sum by (%s) (kafka_controller_kafkacontroller_'
                 'metadataerrorcount{%s}), "kind", "controller", "", "")'
                 % (POD, SEL, POD, SEL, POD, SEL),
                 "{{%s}} {{kind}}" % POD)),
            unit="short", w=12, thresholds=GREEN_RED_1,
        ),
        P.timeseries(
            "Raft poll idle ratio",
            "The fraction of its time the raft IO thread spent waiting for "
            "work, per node, between 0 and 1. It is the one panel that "
            "separates *the quorum is slow because the thread that runs it is "
            "saturated* from *the quorum is slow because the disk under it "
            "is*, and the two have completely different fixes. Near 1 is "
            "healthy — the thread is mostly idle. Approaching 0 while commit "
            "latency climbs means the raft client itself is the bottleneck, "
            "usually a controller sharing a node with brokers under load. "
            "Kafka computes the ratio over its own window, so this is already "
            "normalised and takes no `rate()`.",
            P.targets(("kafka_server_raftmetrics_poll_idle_ratio_avg{%s}" % SEL,
                       "{{%s}}" % POD)),
            unit="percentunit", w=12, min_value=0, max_value=1,
        ),
        P.timeseries(
            "Quorum channel requests and responses /s",
            "Traffic on the channel between quorum peers, per node: the requests "
            "this node sent and the responses it received. Read as a pair. "
            "Matched rates are a quorum talking to itself; requests pulling ahead "
            "of responses on one node is a peer that has stopped answering it — "
            "the slow inter-AZ link, or a peer that is gone — and it shows here "
            "before commit latency moves, because a commit only needs a majority "
            "and the slow peer can be the one left out. This replaced a panel "
            "that read request_latency_avg and request_latency_max_total from "
            "the same bean; the raft channel never registers those sensors (they "
            "are producer and consumer client metrics), so it drew nothing on "
            "every cluster.",
            P.targets(
                ('label_replace(max by (%s) (kafka_server_raftchannelmetrics_'
                 'request_rate{%s}), "kind", "requests", "", "")'
                 % (POD, SEL), "{{%s}} {{kind}}" % POD),
                ('label_replace(max by (%s) (kafka_server_raftchannelmetrics_'
                 'response_rate{%s}), "kind", "responses", "", "")'
                 % (POD, SEL), "{{%s}} {{kind}}" % POD),
            ),
            unit="reqps", w=12,
        ),
    ]


# ── Cluster health ─────────────────────────────────────────────────────────

def _health() -> list[dict]:
    return [
        P.timeseries(
            "Which node is the controller",
            "Which node holds the controller, over time. Only the active "
            "controller publishes a 1, so the sum across the cluster is the "
            "stat in the header and the per-node breakdown is here: the line "
            "that is up is the node in charge. Handovers appear as one line "
            "dropping and another rising, and a gap between them is time with "
            "no controller at all. KafkaActiveControllerCount alerts on the "
            "sum being anything other than 1 for 3 minutes.",
            P.targets(("sum by (%s) (%s{%s})" % (POD, ANCHOR, SEL), "{{%s}}" % POD)),
            unit="short", w=8, min_value=0,
        ),
        P.timeseries(
            "Registered and fenced brokers",
            "The controller's own count of brokers it has registered, and how "
            "many of those it has fenced. KafkaFencedBrokers fires on the "
            "fenced count above zero for 5 minutes and the line is drawn at 1. "
            "**A fenced broker is not a down broker.** It is registered, it is "
            "running, its probes pass, and the controller has removed every "
            "partition leadership from it — because its heartbeat lapsed, or "
            "because it restarted and has not caught up on the metadata log "
            "far enough to be trusted with data. Registered falling is a node "
            "leaving; fenced rising with registered flat is a node present and "
            "excluded, which no Kubernetes signal shows.",
            P.targets(
                ('label_replace(max(kafka_controller_kafkacontroller_activebrokercount'
                 '{%s}), "kind", "registered", "", "") or label_replace('
                 'max(kafka_controller_kafkacontroller_fencedbrokercount{%s}), '
                 '"kind", "fenced", "", "")' % (SEL, SEL), "{{kind}}")),
            unit="short", w=8,
        ),
        P.timeseries(
            "Broker state",
            "Each broker's own lifecycle state, as a code: 0 NOT_RUNNING, 1 "
            "STARTING, 2 RECOVERY, 3 RUNNING, 6 PENDING_CONTROLLED_SHUTDOWN, "
            "7 SHUTTING_DOWN. It is the panel that tells a planned restart "
            "from an unplanned one — a broker that passes through 6 and 7 was "
            "asked to leave, and a broker that reappears at 1 or 2 without "
            "ever showing 6 was killed. A broker stuck in 2 (RECOVERY) is "
            "rebuilding its log after an unclean shutdown and will serve "
            "nothing until it finishes, however healthy the pod looks.",
            P.targets(("max by (%s) (kafka_server_kafkaserver_brokerstate{%s})"
                       % (POD, SEL), "{{%s}}" % POD)),
            unit="short", w=8, min_value=0,
        ),
        P.timeseries(
            "Offline partitions",
            "Partitions with no leader anywhere in the cluster. Every produce "
            "and every fetch to them fails immediately; this is the most "
            "severe number on the board and KafkaOfflinePartitions fires above "
            "zero after 2 minutes. Only the active controller publishes it, so "
            "the series disappears entirely while there is no controller — an "
            "empty panel here is not the same as a zero, and 'Which node is "
            "the controller' says which one you are looking at.",
            P.targets(("sum(kafka_controller_kafkacontroller_offlinepartitionscount"
                       "{%s})" % SEL, "offline")),
            unit="short", w=8, thresholds=GREEN_RED_1, threshold_style="line",
        ),
        P.timeseries(
            "Partitions below min.insync.replicas",
            "Per broker, the partitions it leads that have fewer in-sync "
            "replicas than the topic's `min.insync.replicas`. Those partitions "
            "REFUSE writes with `acks=all` — the producer gets "
            "NOT_ENOUGH_REPLICAS — while reads and `acks=1` writes carry on, "
            "so the symptom reaching an application team is 'some of our "
            "producers are failing' rather than an outage. "
            "KafkaUnderMinIsrPartitions fires above zero after 2 minutes. This "
            "is the broker-side ReplicaManager gauge, which is the one the "
            "alert reads; `strimzi-kafka.json` sums the per-partition "
            "`kafka_cluster_partition_underminisr` instead, and the "
            "Replication section below has that one broken out by topic.",
            P.targets(("sum by (%s) (kafka_server_replicamanager_"
                       "underminisrpartitioncount{%s})" % (POD, SEL),
                       "{{%s}}" % POD)),
            unit="short", w=8, thresholds=GREEN_RED_1, threshold_style="line",
        ),
        P.timeseries(
            "Unclean leader elections (10m)",
            "Leaders elected from replicas that were NOT in sync, in the last "
            "ten minutes. KafkaUncleanLeaderElection fires on any of them, with "
            "no `for:` — one is an incident. It means the cluster chose "
            "availability over durability and COMMITTED RECORDS WERE "
            "DISCARDED: the new leader's log is shorter than the old one's, "
            "and every consumer that had read past its end will be reset "
            "backwards. `increase()` over ten minutes rather than a rate, "
            "because these are rare events and the count is what matters.",
            P.targets(("sum by (%s) (increase(kafka_controller_controllerstats_"
                       "uncleanleaderelections_total{%s}[10m]))" % (POD, SEL),
                       "{{%s}}" % POD)),
            unit="short", w=8, thresholds=GREEN_RED_1, threshold_style="line",
        ),
        P.timeseries(
            "Preferred-replica imbalance",
            "Partitions whose leader is not their preferred (first) replica. "
            "Nothing is broken while this is non-zero — every partition has a "
            "leader — but the leadership is concentrated wherever the last "
            "failover left it, so one broker is doing more work than its "
            "share and the rack-aware placement the topics were created with "
            "is no longer in effect. It is what a preferred-leader election "
            "fixes, and what Cruise Control's self-healing does on its own. "
            "Expect it to jump at every rolling restart and fall back "
            "afterwards; a value that never falls is a cluster whose leaders "
            "have not been rebalanced since its last incident.",
            P.targets(("max(kafka_controller_kafkacontroller_"
                       "preferredreplicaimbalancecount{%s})" % SEL, "imbalanced")),
            unit="short", w=8,
        ),
        P.timeseries(
            "Topics and partitions",
            "The controller's count of topics and of partitions in the whole "
            "cluster. It is scale rather than health, and it is here for two "
            "reasons. It is the denominator for almost everything else on this "
            "board — twelve under-replicated partitions out of forty is an "
            "incident and out of forty thousand is a rolling restart — and it "
            "is the only content kept from `kafka-working`, which rendered the "
            "same two numbers as gauges and nothing else. A step is a topic "
            "created or deleted; a partition count that climbs steadily is "
            "auto-creation left on, which ends at the file-descriptor and "
            "controller-queue panels. Only the active controller publishes "
            "these, so they vanish when there is no controller.",
            P.targets(
                ('label_replace(max(kafka_controller_kafkacontroller_'
                 'globaltopiccount{%s}), "kind", "topics", "", "") or '
                 'label_replace(max(kafka_controller_kafkacontroller_'
                 'globalpartitioncount{%s}), "kind", "partitions", "", "")'
                 % (SEL, SEL), "{{kind}}")),
            unit="short", w=8,
        ),
        P.timeseries(
            "Controller event queue time (p99)",
            "The 99th percentile of how long an event waited in the "
            "controller's queue before being processed, in milliseconds. This "
            "is controller SATURATION, and it is the reason a cluster can be "
            "slow to create a topic or to react to a broker leaving while "
            "every broker-side panel is calm. It climbs on very large "
            "clusters, during mass partition reassignment, and when the "
            "controller shares a node with a loaded broker. **The quantile is "
            "a pre-computed gauge carrying a `quantile` label** — Kafka's own "
            "histogram, published straight through, with no `_sum` companion. "
            "Select the quantile; `histogram_quantile` over it is meaningless.",
            P.targets(("max by (%s) (kafka_controller_controllereventmanager_"
                       'eventqueuetimems{%s, quantile="0.99"})' % (POD, SEL),
                       "{{%s}}" % POD)),
            unit="ms", w=8,
        ),
        P.timeseries(
            "Controller events /s",
            "Events the controller processed per second. Read beside the "
            "queue-time panel: high rate with low queue time is a busy, "
            "healthy controller, and low rate with high queue time is a "
            "controller blocked on something — usually the metadata log's "
            "disk. This one IS a genuine count: `_count_total` is the Yammer "
            "timer's `Count` attribute, the number of events timed, and "
            "`rate()` over it is a rate of events. That is worth saying "
            "explicitly, because the identically-shaped "
            "`…requesthandleravgidlepercent_count_total` in the Request path "
            "section is cumulative NANOSECONDS and not a count of anything.",
            P.targets(("sum by (%s) (rate(kafka_controller_controllereventmanager_"
                       "eventqueuetimems_count_total{%s}[5m]))" % (POD, SEL),
                       "{{%s}}" % POD)),
            unit="short", w=8,
        ),
    ]


# ── Replication ────────────────────────────────────────────────────────────

def _replication() -> list[dict]:
    return [
        P.timeseries(
            "ISR shrinks and expands /s",
            "Followers being dropped from in-sync replica sets, and added "
            "back, per second per broker. Read the two together: shrinks "
            "followed by matching expands is a follower that fell behind and "
            "caught up — a rolling restart draws exactly that shape and it is "
            "not an incident. Shrinks with no expands is a follower that is "
            "not coming back. KafkaISRShrinkRate fires on any shrink rate "
            "sustained for 10 minutes, which is deliberately long enough to "
            "let a restart finish. Both attributes are `…PerSec` meters, so "
            "the exporter strips `PerSec` and publishes the meter's `Count` as "
            "`…_total`: `rate()` is correct and the legacy boards' "
            "`kafka_server_replicamanager_isrshrinkspersec` was a name no rule "
            "has ever produced.",
            P.targets(
                ('label_replace(sum by (%s) (rate(kafka_server_replicamanager_'
                 'isrshrinks_total{%s}[5m])), "kind", "shrink", "", "") or '
                 'label_replace(sum by (%s) (rate(kafka_server_replicamanager_'
                 'isrexpands_total{%s}[5m])), "kind", "expand", "", "")'
                 % (POD, SEL, POD, SEL), "{{%s}} {{kind}}" % POD)),
            unit="short", w=12,
        ),
        P.timeseries(
            "Failed ISR updates /s",
            "ISR changes the broker tried to make and the controller refused. "
            "In KRaft an ISR update is an `AlterPartition` request from the "
            "leader to the controller, so failures here are the leader and the "
            "controller disagreeing about the partition's epoch — normal in "
            "small numbers during a leadership change, and a sign of a broker "
            "running on stale metadata when it persists. It pairs directly "
            "with 'Broker metadata lag' above: a broker that cannot apply "
            "metadata cannot get its ISR changes accepted either.",
            P.targets(("sum by (%s) (rate(kafka_server_replicamanager_"
                       "failedisrupdates_total{%s}[5m]))" % (POD, SEL),
                       "{{%s}}" % POD)),
            unit="short", w=12,
        ),
        P.timeseries(
            "Under-replicated partitions",
            "Per broker, the partitions it leads with at least one follower "
            "out of sync. KafkaUnderReplicatedPartitions fires above zero for "
            "5 minutes, and `kafka:under_replicated_partitions:sum` records "
            "the cluster total. This is the first replication signal to move "
            "and the least urgent: the data is still being served and still "
            "being written, on fewer copies than the topic asked for. It "
            "becomes urgent when it turns into the next two panels.",
            P.targets(("sum by (%s) (kafka_server_replicamanager_"
                       "underreplicatedpartitions{%s})" % (POD, SEL),
                       "{{%s}}" % POD)),
            unit="short", w=8, thresholds=GREEN_RED_1, threshold_style="line",
        ),
        P.timeseries(
            "Partitions at min.insync.replicas",
            "Partitions sitting exactly ON their `min.insync.replicas` floor. "
            "Nothing is failing yet and one more follower falling out stops "
            "`acks=all` writes to them — this is the warning that comes before "
            "'Partitions below min.insync.replicas', and it is the only one "
            "that gives any lead time. On a three-replica topic with "
            "`min.insync.replicas=2` it means one replica is already out. "
            "There is no alert on it on purpose: at-min is a normal steady "
            "state during a rolling restart, and alerting on it would page "
            "for every upgrade.",
            P.targets(("sum by (%s) (kafka_server_replicamanager_"
                       "atminisrpartitioncount{%s})" % (POD, SEL), "{{%s}}" % POD)),
            unit="short", w=8,
        ),
        P.timeseries(
            "Offline replicas",
            "Replicas this broker holds on a log directory that has gone "
            "offline — a failed disk, or a directory Kafka marked bad after an "
            "IO error. The replicas are not being served and not being "
            "replicated, and unlike everything else in this section the fix is "
            "storage rather than Kafka. It is the broker-side companion to "
            "'Offline log directories' in the Storage section: that one counts "
            "directories, this one counts what was on them.",
            P.targets(("sum by (%s) (kafka_server_replicamanager_"
                       "offlinereplicacount{%s})" % (POD, SEL), "{{%s}}" % POD)),
            unit="short", w=8, thresholds=GREEN_RED_1,
        ),
        P.timeseries(
            "Replication traffic",
            "Bytes per second moving between brokers for replication, in and "
            "out, per broker. Out is a broker serving followers; in is a "
            "broker catching up. A follower rebuilding after a restart shows a "
            "large, temporary `in`; a permanent imbalance where one broker's "
            "`out` dwarfs the rest is leadership concentrated on it, which the "
            "'Preferred-replica imbalance' panel explains. This traffic "
            "competes with client traffic for the same network and the same "
            "request handlers, which is why a large reassignment shows up as "
            "client latency. Both are `…PerSec` meters published as `_total` "
            "counters, so `rate()` is right.",
            P.targets(
                ('label_replace(sum by (%s) (rate(kafka_server_brokertopicmetrics_'
                 'replicationbytesin_total{%s}[5m])), "kind", "in", "", "") or '
                 'label_replace(sum by (%s) (rate(kafka_server_brokertopicmetrics_'
                 'replicationbytesout_total{%s}[5m])), "kind", "out", "", "")'
                 % (POD, SEL, POD, SEL), "{{%s}} {{kind}}" % POD)),
            unit="Bps", w=12,
        ),
        P.timeseries(
            "Partition leadership by zone",
            "Partition leaderships held in each availability zone. This panel "
            "exists because `zone` finally reaches the series: the node pools "
            "have always set a `zone` pod label and "
            "`kafka-common.strimziRelabelings` only carried `strimzi_io_*`, so "
            "every legacy legend that said `({{zone}})` rendered as `()`. On a "
            "three-AZ cluster the three lines should be within a partition or "
            "two of each other. One zone carrying most of the leadership means "
            "most client traffic is crossing a zone boundary to reach it — "
            "which is a cost problem before it is a latency problem — and a "
            "zone dropping to zero is an AZ that has lost its brokers. A "
            "cluster whose pods carry no `zone` label draws one unlabelled "
            "line, because the relabeling uses `(.+)` and gives an unzoned pod "
            "no zone at all rather than an empty one.",
            P.targets(("sum by (zone) (kafka_server_replicamanager_leadercount{%s})"
                       % SEL, "{{zone}}")),
            unit="short", w=12,
        ),
        P.timeseries(
            "Replica placement by zone",
            "Partition replicas — leaders and followers together — held in "
            "each zone. Where the panel to the left shows who is doing the "
            "work, this shows where the data is, and it is the one that says "
            "whether losing an AZ would lose a partition. With rack-aware "
            "placement and three zones the three lines should be almost "
            "identical; a zone holding materially fewer replicas means topics "
            "were created while it was down, or while `broker.rack` was not "
            "set, and those partitions have no copy there.",
            P.targets(("sum by (zone) (kafka_server_replicamanager_partitioncount{%s})"
                       % SEL, "{{zone}}")),
            unit="short", w=12,
        ),
        P.table(
            "Partitions below min ISR, by topic",
            "Which topic and which partition is refusing `acks=all` writes "
            "right now. The stat and the timeseries above count them; this "
            "names them, and it is the panel an application team's question "
            "actually needs. `kafka_cluster_partition_underminisr` is a "
            "per-partition gauge published by the leader, so each row is one "
            "partition and the `> 0` filter is what keeps the table to the "
            "ones that matter instead of every partition in the cluster.",
            [P.target("max by (topic, partition) "
                      "(kafka_cluster_partition_underminisr{%s}) > 0" % SEL,
                      instant=True, fmt="table")],
            w=12, transformations=_TABLE_TRANSFORMS,
        ),
        P.table(
            "Replicas out of sync, by partition",
            "Per partition, how many of its replicas are NOT in the in-sync "
            "set — the replica count minus the in-sync count, filtered to the "
            "partitions where that is positive. It is the detail behind "
            "'Under-replicated partitions': that panel says a broker has "
            "twelve of them, this says which twelve and how far each has "
            "fallen. A partition showing a deficit equal to its replication "
            "factor minus one has exactly the leader left and is one failure "
            "from being offline. Both halves are per-partition gauges, matched "
            "on topic and partition.",
            [P.target("max by (topic, partition) "
                      "(kafka_cluster_partition_replicascount{%s}) "
                      "- max by (topic, partition) "
                      "(kafka_cluster_partition_insyncreplicascount{%s}) > 0"
                      % (SEL, SEL), instant=True, fmt="table")],
            w=12, transformations=_TABLE_TRANSFORMS,
        ),
    ]


# ── Request path ───────────────────────────────────────────────────────────

def _requests() -> list[dict]:
    return [
        P.timeseries(
            "Request handler idle (windowed)",
            "The fraction of the last five minutes the broker's request "
            "handler threads spent idle, per broker. Below %.2f for 10 minutes "
            "is KafkaRequestHandlerSaturated and the line is drawn there. This "
            "is the single best saturation signal a Kafka broker has: at zero, "
            "every handler thread is busy and every new request queues.\n\n"
            "**Read where this expression comes from.** The obvious series is "
            "`…requesthandleravgidle_percent`, which the exporter builds from "
            "the meter's `MeanRate` — a LIFETIME mean since the broker "
            "started. It flattens: a broker that has been up for a week and "
            "has been saturated for an hour still reads a comfortable number. "
            "`…requesthandleravgidlepercent_count_total` is the same meter's "
            "`Count`, and its unit IS NOT A COUNT — Kafka accumulates idle "
            "NANOSECONDS into it. So `rate(...[5m])` gives idle nanoseconds "
            "per second, and dividing by 1e9 gives the idle fraction over the "
            "window, which moves when the broker does. The alert uses exactly "
            "this form."
            % HANDLER_IDLE_RATIO,
            P.targets(("avg by (%s) (rate(kafka_server_kafkarequesthandlerpool_"
                       "requesthandleravgidlepercent_count_total{%s}[5m])) / 1e9"
                       % (POD, SEL), "{{%s}}" % POD)),
            unit="percentunit", w=12, min_value=0, max_value=1,
            thresholds=[("red", None), ("green", HANDLER_IDLE_RATIO)],
            threshold_style="line",
        ),
        P.timeseries(
            "Network processor idle",
            "The fraction of its time each broker's network threads spent "
            "idle. These threads read and write sockets; the handler threads "
            "behind them do the work. Network idle low with handler idle "
            "healthy is a broker spending its time on connections rather than "
            "on requests — a great many clients, TLS handshakes, or clients "
            "reconnecting on every call, which the Security and connections "
            "section can confirm. This one is a plain `Value` gauge already "
            "normalised to 0–1; the corrected name is "
            "`…networkprocessoravgidle_percent` with the underscore the "
            "exporter's Percent rule inserts, and the legacy boards' "
            "`…networkprocessoravgidlepercent` was one of the seven names that "
            "never existed.",
            P.targets(("avg by (%s) (kafka_network_socketserver_"
                       "networkprocessoravgidle_percent{%s})" % (POD, SEL),
                       "{{%s}}" % POD)),
            unit="percentunit", w=12, min_value=0, max_value=1,
        ),
        P.timeseries(
            "Request and response queue depth",
            "Requests waiting for a handler thread, and responses waiting for "
            "a network thread. KafkaRequestQueueSaturated fires above %d "
            "requests for 10 minutes and the line is drawn there. A request "
            "queue that is growing is the direct consequence of the idle "
            "ratio above reaching zero, and it is what clients experience as "
            "latency: the broker is not slow at the work, it has not started "
            "the work. A response queue growing while the request queue is "
            "empty is the opposite problem — the work is done and the network "
            "threads cannot get it out, which is a socket or a client that "
            "has stopped reading."
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
            "The 99th percentile of total time per request type, in "
            "milliseconds — the whole round trip inside the broker: queue, "
            "local work, waiting for followers, response queue, send. "
            "KafkaRequestLatencyHigh fires above %d ms on Produce, "
            "FetchConsumer or FetchFollower for 10 minutes and the line is "
            "drawn there; `kafka:produce_p99_ms:max` records the Produce leg. "
            "Produce climbing alone points at replication — an `acks=all` "
            "produce waits for the ISR — and FetchFollower climbing alone is "
            "the replication path itself. **These percentiles are "
            "pre-computed gauges carrying a `quantile` label and there is no "
            "`_sum` series**, so the quantile is selected, never computed with "
            "`histogram_quantile`."
            % REQUEST_P99_MS,
            P.targets(("max by (request) (kafka_network_requestmetrics_totaltimems"
                       '{%s, quantile="0.99"})' % SEL, "{{request}}")),
            unit="ms", w=12,
            thresholds=[("green", None), ("red", REQUEST_P99_MS)],
            threshold_style="line",
        ),
        P.timeseries(
            "Request queue time p99 by type",
            "Of that total time, the part spent waiting in the queue before "
            "any work started. It is the panel that splits a latency problem "
            "in two: queue time near the total means the broker is saturated "
            "and the fix is capacity or a client that is asking for too much; "
            "queue time near zero with a high total means the work itself is "
            "slow, and the Storage section's flush latency is the next place "
            "to look. Same pre-computed `quantile` gauge as the panel to the "
            "left.",
            P.targets(("max by (request) (kafka_network_requestmetrics_"
                       'requestqueuetimems{%s, quantile="0.99"})' % SEL,
                       "{{request}}")),
            unit="ms", w=12,
        ),
        P.timeseries(
            "Requests /s by type",
            "The broker's request mix, per second. It is context rather than "
            "a fault signal, and it is the first thing to check when latency "
            "moves with nothing else on the board moving: a client that "
            "changed its batch size, a consumer group that started polling "
            "ten times as often, or a metadata-request storm from clients that "
            "cannot resolve a leader all appear here and nowhere else. The "
            "`…PerSec` meter's Count published as `_total`, so `rate()`.",
            P.targets(("sum by (request) (rate(kafka_network_requestmetrics_"
                       "requests_total{%s}[5m]))" % SEL, "{{request}}")),
            unit="reqps", w=12,
        ),
        P.timeseries(
            "Produce and fetch /s by API version",
            "The same meter broken out by the protocol version the client "
            "used. Kafka labels `RequestsPerSec` with both `request` and "
            "`version`, and nothing else in this repository looks at the "
            "version. It answers a question no other panel can: which clients "
            "are old. A client on an old Produce version cannot use newer "
            "acknowledgement or idempotence semantics, and a version "
            "disappearing after an upgrade is how you confirm a fleet actually "
            "migrated. Restricted to Produce and Fetch because those are the "
            "ones that carry data.",
            P.targets(("sum by (request, version) (rate("
                       "kafka_network_requestmetrics_requests_total"
                       '{%s, request=~"Produce|Fetch.*"}[5m]))' % SEL,
                       "{{request}} v{{version}}")),
            unit="reqps", w=12,
        ),
        P.timeseries(
            "Errors /s by code",
            "Failed responses per second, broken out by Kafka error code, with "
            "NONE excluded — the meter counts every response including the "
            "successful ones. This is the most specific diagnostic on the "
            "board: NOT_ENOUGH_REPLICAS is the ISR panels, "
            "KAFKA_STORAGE_ERROR is a log directory, LEADER_NOT_AVAILABLE is a "
            "leadership change in progress, REQUEST_TIMED_OUT is the "
            "saturation panels, and UNKNOWN_TOPIC_OR_PARTITION or "
            "TOPIC_AUTHORIZATION_FAILED are a client's own problem and "
            "deliberately not in the SLI below.",
            P.targets(("sum by (error) (rate(kafka_network_requestmetrics_errors_total"
                       '{%s, error!="NONE"}[5m]))' % SEL, "{{error}}")),
            unit="reqps", w=12,
        ),
        P.timeseries(
            "Server-side error ratio (SLI)",
            "The share of client produce and fetch responses that failed for a "
            "reason the CLUSTER owns, over five minutes. This is the "
            "availability SLI `charts/kafka-cluster` records as "
            "`kafka:request_errors:ratio_rate5m` and burns the error budget "
            "against; the expression is written out here rather than read from "
            "the recorded series so the panel works whether or not "
            "`alerts.slo.enabled` installed the rules. The line is the fast-burn "
            "budget, %.4f — burn rate 14.4 times one minus a 99.9%% target — "
            "and KafkaAvailabilitySLOBurning pages when both the 1-hour and "
            "5-minute windows are above it. Client errors are excluded by "
            "construction: only the eight server-side codes are in the "
            "numerator."
            % SLO_BUDGET,
            P.targets((
                "sum(rate(kafka_network_requestmetrics_errors_total"
                '{%s, request=~"Produce|FetchConsumer", error=~"%s"}[5m])) / '
                "sum(rate(kafka_network_requestmetrics_errors_total"
                '{%s, request=~"Produce|FetchConsumer"}[5m]))'
                % (SEL, SERVER_ERRORS, SEL), "error ratio")),
            unit="percentunit", w=12,
            thresholds=[("green", None), ("red", SLO_BUDGET)],
            threshold_style="line",
        ),
        P.timeseries(
            "Bytes rejected /s",
            "Bytes in produce requests the broker refused outright, per "
            "second per broker — almost always a batch larger than "
            "`message.max.bytes`, or a compression type the topic does not "
            "allow. Nothing alerts on it and nothing else shows it: the "
            "producer sees RecordTooLargeException, the cluster sees a "
            "perfectly healthy day, and the records are simply never written. "
            "A step change here right after a deployment is a client that "
            "raised its batch size past the broker's limit.",
            P.targets(("sum by (%s) (rate(kafka_server_brokertopicmetrics_"
                       "bytesrejected_total{%s}[5m]))" % (POD, SEL),
                       "{{%s}}" % POD)),
            unit="Bps", w=12, thresholds=GREEN,
        ),
    ]


# ── Storage ────────────────────────────────────────────────────────────────

def _storage() -> list[dict]:
    return [
        P.timeseries(
            "Log size per broker",
            "Bytes on disk in the Kafka log, per broker, summed over every "
            "partition it holds. It is the trend that says whether a volume "
            "will fill before retention catches up, and it is the thing to "
            "look at when KafkaBrokerDiskUsageHigh fires: the alert reads the "
            "kubelet's volume statistics and says the disk is filling, and "
            "this says whether Kafka is what is filling it. A broker whose "
            "line climbs away from the others is holding more partitions, or "
            "holding a partition whose retention is not deleting.",
            P.targets(("sum by (%s) (kafka_log_log_size{%s})" % (POD, SEL),
                       "{{%s}}" % POD)),
            unit="bytes", w=12,
        ),
        P.timeseries(
            "Log flush p99",
            "The 99th percentile time to flush a log segment to disk, in "
            "milliseconds. KafkaLogFlushLatencyHigh fires above %d ms for 10 "
            "minutes and the line is drawn there. This is the storage layer "
            "seen from inside Kafka, and it is upstream of almost everything "
            "else on this board: slow flushes make produce requests slow, "
            "which makes handler threads busy, which makes queues grow, which "
            "makes followers fall out of the ISR. When several sections are "
            "unhappy at once this is the panel that says whether the disk is "
            "the cause. Another pre-computed `quantile` gauge."
            % LOG_FLUSH_P99_MS,
            P.targets(("max by (%s) (kafka_log_logflushstats_logflushrateandtimems"
                       '{%s, quantile="0.99"})' % (POD, SEL), "{{%s}}" % POD)),
            unit="ms", w=12,
            thresholds=[("green", None), ("red", LOG_FLUSH_P99_MS)],
            threshold_style="line",
        ),
        P.timeseries(
            "Log flushes /s",
            "How often the brokers flush, per second. The denominator for the "
            "latency panel beside it: a high p99 over very few flushes is one "
            "slow fsync and probably noise, and the same p99 over a steady "
            "flush rate is the disk. `_count_total` here is the Yammer timer's "
            "`Count` — the number of flushes — so this genuinely is a rate of "
            "events, unlike the nanosecond accumulator behind the "
            "request-handler idle ratio.",
            P.targets(("sum by (%s) (rate(kafka_log_logflushstats_"
                       "logflushrateandtimems_count_total{%s}[5m]))" % (POD, SEL),
                       "{{%s}}" % POD)),
            unit="short", w=8,
        ),
        P.timeseries(
            "Offline log directories",
            "Log directories a broker has marked offline after an IO error. "
            "KafkaOfflineLogDirectory fires above zero after 1 minute — the "
            "shortest `for:` of any alert in the chart, because there is no "
            "benign cause. Every replica on that directory is gone until the "
            "disk comes back, and with JBOD the broker stays up and serves "
            "everything else, which is exactly why this needs its own panel: "
            "nothing at the pod level changes. 'Offline replicas' in the "
            "Replication section counts what was lost.",
            P.targets(("max by (%s) (kafka_log_logmanager_offlinelogdirectorycount"
                       "{%s})" % (POD, SEL), "{{%s}}" % POD)),
            unit="short", w=8, thresholds=GREEN_RED_1, threshold_style="line",
        ),
        P.timeseries(
            "Uncleanable partitions",
            "Partitions the log cleaner has given up on. It only affects "
            "compacted topics, and it is quiet in the worst way: compaction "
            "stops, the partition grows without bound, and the consumers that "
            "rely on a compacted topic having one record per key keep reading "
            "the old ones. `__consumer_offsets` is a compacted topic, so this "
            "can end in a cluster that cannot store offsets. The bean carries "
            "the log directory as a label, and the vendored upstream rule "
            "captures Kafka's own quotes into the value, so nothing here "
            "selects on it — the sum is over the whole broker.",
            P.targets(("sum by (%s) (kafka_log_logcleanermanager_"
                       "uncleanable_partitions_count{%s})" % (POD, SEL),
                       "{{%s}}" % POD)),
            unit="short", w=8, thresholds=GREEN_RED_1,
        ),
        P.table(
            "Largest topics on disk",
            "The twenty topics using the most disk across the cluster, summed "
            "over every partition and every replica — so a three-replica topic "
            "counts three times, which is what actually occupies the volumes. "
            "It is the panel for the question that follows a disk alert: what "
            "is it, and can its retention be shortened. Per-partition detail, "
            "log-end offsets and segment counts are on the "
            "*Kafka — Performance & Load Testing* board, which has the "
            "`$topic` selector for it.",
            [P.target("topk(20, sum by (topic) (kafka_log_log_size{%s}))" % SEL,
                      instant=True, fmt="table")],
            w=12, transformations=_TABLE_TRANSFORMS,
        ),
    ]


# ── Tiered storage ─────────────────────────────────────────────────────────

def _tiered() -> list[dict]:
    return [
        P.timeseries(
            "Remote copy and fetch throughput",
            "Bytes per second moving to and from the remote tier. `copy` is "
            "segments being offloaded off local disk; `fetch` is a consumer "
            "reading history back out of object storage. These series exist "
            "only when `tieredStorage.enabled` put "
            "`remote.log.storage.system.enable=true` on the brokers — the "
            "whole row is collapsed for that reason, and on a cluster without "
            "the remote tier it is empty rather than misleading.",
            P.targets(
                ('label_replace(sum by (%s) (rate(kafka_server_brokertopicmetrics_'
                 'remotecopybytes_total{%s}[5m])), "kind", "copy", "", "") or '
                 'label_replace(sum by (%s) (rate(kafka_server_brokertopicmetrics_'
                 'remotefetchbytes_total{%s}[5m])), "kind", "fetch", "", "")'
                 % (POD, SEL, POD, SEL), "{{%s}} {{kind}}" % POD)),
            unit="Bps", w=12,
        ),
        P.timeseries(
            "Remote copy and fetch errors /s",
            "Failures talking to the remote tier. KafkaTieredStorageCopyErrors "
            "fires on a sustained copy-error rate for 10 minutes, and the "
            "consequence is indirect but severe: a broker that cannot offload "
            "keeps the segments on LOCAL disk, so a tiered cluster sized for "
            "its remote tier starts filling volumes that were never meant to "
            "hold that much. Fetch errors are less dangerous and more visible "
            "— a consumer reading history simply fails.",
            P.targets(
                ('label_replace(sum by (%s) (rate(kafka_server_brokertopicmetrics_'
                 'remotecopyerrors_total{%s}[10m])), "kind", "copy", "", "") or '
                 'label_replace(sum by (%s) (rate(kafka_server_brokertopicmetrics_'
                 'remotefetcherrors_total{%s}[10m])), "kind", "fetch", "", "")'
                 % (POD, SEL, POD, SEL), "{{%s}} {{kind}}" % POD)),
            unit="short", w=12, thresholds=GREEN_RED_1,
        ),
        P.timeseries(
            "Remote copy lag",
            "Bytes eligible for the remote tier that have not been copied "
            "there yet, per broker. This is the leading indicator the error "
            "panel is the trailing one for: lag that grows without errors is "
            "an offload that cannot keep up with ingest — remote storage "
            "throughput, or `remote.log.manager.thread.pool.size` — and every "
            "byte of it is sitting on local disk in the meantime.",
            P.targets(("sum by (%s) (kafka_server_brokertopicmetrics_"
                       "remotecopylagbytes{%s})" % (POD, SEL), "{{%s}}" % POD)),
            unit="bytes", w=12,
        ),
        P.timeseries(
            "Remote log size",
            "Bytes the cluster holds in the remote tier. Read beside 'Log size "
            "per broker' in the Storage section: together they are the whole "
            "retention picture, and the ratio between them is whether tiering "
            "is doing its job. Remote size flat while local size climbs means "
            "nothing is being offloaded, whatever the error panel says.",
            P.targets(("sum by (%s) (kafka_server_brokertopicmetrics_"
                       "remotelogsizebytes{%s})" % (POD, SEL), "{{%s}}" % POD)),
            unit="bytes", w=12,
        ),
    ]


# ── Security and connections ───────────────────────────────────────────────

def _security() -> list[dict]:
    return [
        P.timeseries(
            "Failed authentications /s by listener",
            "Clients that failed to authenticate, per second, per listener. "
            "Nothing in this repository alerts on it, and it is the only place "
            "an expired client certificate, a rotated SCRAM password or a "
            "misconfigured OAuth issuer is visible from the cluster side — "
            "from the client side it is a connection that will not open, and "
            "from the broker's logs it is one line per attempt. A step that "
            "starts at a deployment is a credential change; a slow climb "
            "across all listeners is certificates reaching their expiry "
            "together, which is what a fleet issued on the same day does. The "
            "listener label makes it obvious whether it is internal "
            "replication traffic or external clients.",
            P.targets(("sum by (listener) (rate(kafka_server_socket_server_metrics_"
                       "failed_authentication_total{%s}[5m]))" % SEL,
                       "{{listener}}")),
            unit="short", w=12, thresholds=GREEN,
        ),
        P.timeseries(
            "Successful authentications /s by listener",
            "The denominator for the panel beside it. Failed authentications "
            "mean very different things at different scales: five a second "
            "against fifty successes is a broken client, and five a second "
            "against five successes is every client failing. It is also a "
            "churn signal in its own right — a healthy fleet holds long-lived "
            "connections and authenticates rarely, so a high successful rate "
            "means clients are reconnecting constantly, which the next panel "
            "confirms.",
            P.targets(("sum by (listener) (rate(kafka_server_socket_server_metrics_"
                       "successful_authentication_total{%s}[5m]))" % SEL,
                       "{{listener}}")),
            unit="short", w=12,
        ),
        P.timeseries(
            "New connections /s by listener",
            "Connection churn. Kafka clients are designed to open a connection "
            "and keep it, so a steady creation rate means something is "
            "reconnecting on a loop: a client with a short idle timeout, a "
            "load balancer cutting idle connections, a client library that "
            "opens a connection per call, or a broker running out of file "
            "descriptors and dropping them. Churn costs the network threads "
            "far more than the traffic does, which is why this panel and "
            "'Network processor idle' in the Request path section are usually "
            "read together.",
            P.targets(("sum by (listener) (rate(kafka_server_socket_server_metrics_"
                       "connection_creation_total{%s}[5m]))" % SEL, "{{listener}}")),
            unit="short", w=12,
        ),
        P.timeseries(
            "Open connections by node and listener",
            "How many connections each broker currently holds on each "
            "listener. `strimzi-kafka.json` has this panel too, and it is "
            "repeated here for one reason: it is the denominator that makes "
            "the three panels above readable, and an operator chasing an "
            "authentication or churn problem should not have to change board "
            "to find it. Connections concentrated on one broker are clients "
            "that are not using the bootstrap fairly, or one broker holding "
            "most of the leadership.",
            P.targets(("sum by (%s, listener) (kafka_server_socket_server_metrics_"
                       "connection_count{%s})" % (POD, SEL),
                       "{{%s}} {{listener}}" % POD)),
            unit="short", w=12,
        ),
    ]


def build(variant: str) -> list[Row]:
    return [
        Row("", _header()),
        Row("Quorum and metadata", _quorum()),
        Row("Cluster health", _health()),
        Row("Replication", _replication()),
        Row("Request path", _requests()),
        Row("Storage", _storage()),
        Row("Tiered storage (when enabled)", _tiered(), collapsed=True),
        Row("Security and connections", _security()),
    ]
