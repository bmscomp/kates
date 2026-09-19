# Metrics

Every series read by a board in `dashboards/`, with its type, its labels, what
it means when it moves, and how to query it when that is not obvious.

The list is derived mechanically: every `targets[].expr` and every query
template variable in every `dashboards/*/dashboard.json`, run through the same
PromQL tokenizer `scripts/metric-contract/contract.py` uses, then deduplicated.
**211 distinct series across twelve boards.** Nothing here was typed from a JMX
bean name, which is the mistake this whole directory exists to stop.

For what a *board* is for, read its own `README.md`. This file is the
cross-cutting reference: one row per series, whichever board reads it.

---

## Four naming traps

These four are why this file exists. Each one produced a dead or misleading
panel somewhere in this repository, and three of them make a series look like
something it is not.

### 1. `_max_total` is not a counter

The JMX exporter's 1.x line reserves `_total` for counters: a `COUNTER` rule's
series is always published with it, and anything else has it stripped. The
vendored KRaft rules type `.+-total|.+-max` as `COUNTER` in one pattern, to
keep the genuinely monotonic `-total` attributes correctly typed:

```yaml
- pattern: "kafka.server<type=raft-metrics><>(.+-total|.+-max):"
  name: kafka_server_raftmetrics_$1
  type: COUNTER
```

So three series are **max gauges in milliseconds wearing a counter's name**:
`kafka_server_raftmetrics_commit_latency_max_total`,
`kafka_server_raftmetrics_election_latency_max_total` and
`kafka_server_raftchannelmetrics_request_latency_max_total`. `rate()` over any
of them yields a number that means nothing. Their `_avg` companions are
ordinary gauges and are read as they are.

### 2. `_count_total` is the meter's `Count`, and the unit is not always a count

The exporter names a Yammer meter's or timer's `Count` attribute `…_count` and
then appends `_total` because the rule types it `COUNTER`. Sometimes the unit
really is events —
`kafka_controller_controllereventmanager_eventqueuetimems_count_total` counts
controller events, `kafka_log_logflushstats_logflushrateandtimems_count_total`
counts flushes — and `rate()` gives events per second.

And sometimes it is not.
`kafka_server_kafkarequesthandlerpool_requesthandleravgidlepercent_count_total`
is **cumulative nanoseconds of idle time**, which is why the idle ratio is

```promql
avg by (kubernetes_pod_name) (rate(…_count_total[5m])) / 1e9
```

— idle nanoseconds per second, divided back into a fraction of the window.

### 3. Percentiles are pre-computed gauges with a `quantile` label and no `_sum`

Kafka's histograms and timers publish their percentiles as MBean attributes,
and the exporter emits them as gauges carrying `quantile="0.99"`. There is no
`_sum`, no `le` and no bucket series, so nothing on any Kafka board can be —
or needs to be — computed with `histogram_quantile()`. Select the quantile.
The upstream rule file says it in a comment: *"Emulate Prometheus 'Summary'
metrics for the exported 'Histogram's. Note that these are missing the '_sum'
metric!"*

The same shape applies to the Kates engine's `kates_benchmark_latency_ms` and
to `kates_tests_duration_seconds`, for different reasons — see their rows.
`http_server_requests_seconds_bucket` and
`kyverno_admission_review_duration_seconds_bucket` are the only two real
cumulative histograms in this directory, and they are the only two places
`histogram_quantile()` is correct.

### 4. `BrokerTopicMetrics` is registered twice

Kafka registers the throughput meters once **per topic** and once with **no
topic tag** for the broker aggregate. An absent label matches `""` in PromQL,
so a query with no `topic` matcher counts every byte twice — once in the
per-topic series and once in the aggregate.

```promql
sum(rate(kafka_server_brokertopicmetrics_bytesin_total{…, topic=""}[5m]))     # cluster total
sum by (topic) (rate(…_bytesin_total{…, topic=~"$topic", topic!=""}[5m]))     # per topic
```

`kafka-performance` reads this family heavily and carries the matcher on every
panel.

---

## How to read a row

| Column | Meaning |
|---|---|
| **Series** | The exact Prometheus name. Copy it; do not derive it from a bean. |
| **Type & labels** | The type the producing rule assigns, then the labels the *rule* attaches. Scrape-added labels are listed once per source below and not repeated. |
| **Boards** | Which board in this directory reads it. |

Verify a name before using it. Every series below that this repository's own
exporter rules produce was checked by running `contract.py`'s
`Exporter`/`producible` machinery over
`charts/kafka-cluster/files/metrics/*.yaml`,
`charts/connect-cluster/files/metrics/*.yaml` and the inline rules in
`charts/mirror-maker2/templates/metrics-configmap.yaml`, against the MBean
catalogues in `scripts/metric-contract/*.yaml`:

```bash
scripts/check-metric-contract.sh kafka-cluster
scripts/check-metric-contract.sh connect-cluster
scripts/check-metric-contract.sh mirror-maker2
```

## Is a panel empty because nothing is wrong, or because nothing publishes it?

The most useful question this file answers. Of the 211 series, **136 are
produced by JMX exporter rules this repository ships and proves** with
`scripts/check-metric-contract.sh`; the other 75 come from somewhere else.

| Producer | Series | Shipped by this repo? | If the panel is empty |
|---|---:|---|---|
| Kafka JMX exporter rules (broker/controller) | 77 | Yes — `charts/kafka-cluster/files/metrics/kafka-metrics.yaml`, vendored from Strimzi | Nothing is wrong, or `metrics.enabled` is off, or (tiered storage) the feature is not enabled |
| Kafka Connect JMX exporter rules | 54 | Yes — `charts/connect-cluster/files/metrics/connect-metrics.yaml` and the inline rules in `charts/mirror-maker2` | Nothing is wrong, or no connector of that kind is deployed |
| MirrorMaker 2 mirror-connector rules | 5 | Yes — `charts/mirror-maker2/templates/metrics-configmap.yaml` | Nothing is being replicated, or the partition is idle (see the `NaN` note) |
| Prometheus recording rules — `mm2:*` | 5 | Yes — `charts/mirror-maker2/templates/alerts.yaml` | `alerts.enabled` and `alerts.slo.enabled` are not both set |
| Prometheus recording rules — `kafka:chaos:*` | 6 | Rules yes, **inputs no** | **Nothing publishes their inputs.** See [the dead six](#the-dead-six-kafkachaos) |
| The Kates application, via Micrometer | 26 | Yes — `kates/`, scraped by `charts/kates`'s ServiceMonitor | No run is in flight (the `run_id`-scoped meters), or the binder is off |
| JVM and process, from whichever agent scrapes the pod | 17 | The collectors come with the agent, not from a rule: the JMX exporter agent on Kafka, Connect and MirrorMaker 2 pods, Micrometer's JVM binder on Kates pods | The board is reading the other agent's spelling — see [JVM and process](#jvm-and-process--micrometers-spelling-not-the-jmx-agents) |
| kube-state-metrics | 5 | Subchart of `kube-prometheus-stack`, on by default in `charts/monitoring` | One of the five needs extra configuration — see `kube_customresource_*` |
| cAdvisor, via the kubelet | 4 | Scraped when `kube-prometheus-stack.kubelet.enabled` (it is) | The pod selector matched nothing |
| LitmusChaos chaos-exporter | 8 | **No.** Install LitmusChaos separately, and its exporter is off by default even then | Litmus is not installed, or `litmus-core.exporter.enabled` is false |
| Kyverno | 3 | **No.** Install Kyverno separately | Kyverno is not installed, or is not scraped |
| Prometheus itself | 1 (`up`) | — | The target selector matched nothing |

**Eighteen series cannot fill in on a cluster built only from this
repository's charts**, and they fall into two very different groups.

*Install something.* The eight `litmuschaos_*` need LitmusChaos **and** its
chaos-exporter, which `charts/kates-chaos` leaves off by default
(`litmus-core.exporter.enabled`); the three
`kyverno_*` need Kyverno; `kube_customresource_chaosengine_status_engine_status`
needs kube-state-metrics configured with custom-resource metrics for
`ChaosEngine`, which is not its default. Each is a supported add-on and the
panels fill in once it is there.

*Build something.* The six `kafka:chaos:*` records cannot be made to work by
installing anything: their inputs are names no exporter, chart or application
in this repository has ever published. Closing that gap means registering the
values with Micrometer in the Kates engine. See
[the dead six](#the-dead-six-kafkachaos).

---

## Labels every series carries

Not repeated in the rows below.

**Kafka, Connect and MirrorMaker 2 pods** are scraped by PodMonitors that apply
`kafka-common.strimziRelabelings`, which adds `namespace`,
`kubernetes_pod_name`, `node_name`, `node_ip`, `zone` and a labelmap of the
pods' `strimzi_io_*` labels (`strimzi_io_cluster`, `strimzi_io_name`,
`strimzi_io_kind`). Prometheus adds `job`, `instance`, `pod` and `container`.

`zone` is new in this refactor. The node pools always set the pod label; the
relabelings only carried `strimzi_io_*`, so every legacy legend that said
`{{zone}}` rendered as `()`. The regex is `(.+)`, so a pod with no `zone` label
keeps none rather than joining an empty-string bucket.

**The Kates application** is scraped by a ServiceMonitor, so its series carry
`job`, `namespace`, `pod` and `instance`. The `kates-*` boards scope on `$job`.

**The MirrorMaker 2 boards scope on hidden `constant` variables** —
`$namespace`, `$cluster`, `$pods` — that the chart fills in per release, rather
than on the query dropdowns the Kafka and Connect boards use. The board file
itself therefore names no namespace, no release and no pod regex. Its
PodMonitor does apply the same relabelings (phase 1 added them), so the
`strimzi_io_*` labels are on the series either way.

**`kates-chaos-infra` scopes the same way**, on `$namespace` and
`$operator_deployment`, which `charts/kates-chaos` fills in per release. Its
`$namespace` is where the LitmusChaos execution plane runs and is **not** the
namespace on any other board: `kates-chaos`'s `$namespace` is the Kafka one,
and the experiments' target namespace appears on neither.

---

# Kafka brokers and controllers

Produced by `charts/kafka-cluster/files/metrics/kafka-metrics.yaml`, vendored
byte-for-byte from Strimzi and checked against upstream by
`scripts/check-strimzi-metrics.sh`. `lowercaseOutputName: true` and
`lowercaseOutputLabelNames` is **not** set, so series names are lower case and
rule-written label names keep their original case (`networkProcessor`).

## KRaft quorum — `kafka.server<type=raft-metrics>`, `<type=raft-channel-metrics>`

One set per node. Voters run the Raft protocol; brokers run the same client as
observers, so most of these exist on every pod. Read all of them together with
`kafka-kraft`'s *Quorum & Metadata* section.

| Series | Type & labels | Boards | What it means, and how to read it |
|---|---|---|---|
| `kafka_server_raftmetrics_current_state` | info series, value always `1`, label `current_state` | kafka-kraft | Which role this node plays in the quorum: `leader`, `candidate`, `follower`, `voted`, `unattached`, `resigned` or `observer`. The state is in the label, not the value, so `sum()` over it counts nodes, not states — group by `current_state` and the pod. Exactly one `leader` is health; a `candidate` that never resolves is an election that cannot conclude. |
| `kafka_server_raftmetrics_current_epoch` | GAUGE, no rule labels | kafka-kraft | The Raft term. The value is meaningless on its own and the *rate of change* is everything: each increment is one leader election, and metadata writes are frozen for the duration of each. Query `changes(…[15m])`, which is what `KafkaRaftLeaderElections` alerts on. |
| `kafka_server_raftmetrics_high_watermark` | GAUGE | kafka-kraft | The highest metadata-log offset a majority of voters has acknowledged — the committed frontier. Only useful next to `log_end_offset`. |
| `kafka_server_raftmetrics_log_end_offset` | GAUGE | kafka-kraft | The highest offset this node has written locally. `log_end_offset − high_watermark` is uncommitted metadata on this node, and a gap that grows and does not close is the signature of a lost majority: the cluster does not stop, it freezes. |
| `kafka_server_raftmetrics_number_unknown_voter_connections` | GAUGE | kafka-kraft | Voters this node currently cannot reach. It is the margin left before the majority is gone: on a three-voter quorum, 1 is a warning and 2 is an outage in progress. `KafkaRaftUnknownVoters` fires above zero for 10 minutes. |
| `kafka_server_raftmetrics_poll_idle_ratio_avg` | GAUGE, 0–1 | kafka-kraft | Fraction of its time the Raft IO thread spent idle. Already a ratio — no `rate()`. Read beside commit latency: slow commits with an idle thread is the disk under the metadata log; slow commits with this at zero is the thread itself, usually a controller co-located with a loaded broker. Two completely different fixes. |
| `kafka_server_raftmetrics_commit_latency_avg` | GAUGE, milliseconds | kafka-kraft | How long a metadata record takes to be acknowledged by a majority. This is the latency of every topic creation, leadership change and ACL update in the cluster. |
| `kafka_server_raftmetrics_commit_latency_max_total` | **max GAUGE despite the name**, milliseconds | kafka-kraft | The worst commit in the sampling window. **Trap 1 — never `rate()` it.** The typed `COUNTER` comes from the rule pattern, not from the attribute. |
| `kafka_server_raftmetrics_election_latency_avg` | GAUGE, milliseconds | kafka-kraft | How long an election took. Three fast elections cost the cluster less than one slow one, because metadata writes are frozen for exactly this long each time. |
| `kafka_server_raftmetrics_election_latency_max_total` | **max GAUGE**, milliseconds | kafka-kraft | The worst election. Trap 1. |
| `kafka_server_raftchannelmetrics_request_latency_avg` | GAUGE, milliseconds | kafka-kraft | Round-trip time on the channel between quorum peers. This is where a slow inter-AZ link shows up *first* — before commit latency moves, because a commit only needs a majority and the slow peer can be the one left out. |
| `kafka_server_raftchannelmetrics_request_latency_max_total` | **max GAUGE**, milliseconds | kafka-kraft | The worst peer round-trip. Trap 1. |

## Broker metadata — `kafka.server<type=broker-metadata-metrics>`

The broker side of KRaft, and the failure mode with no ZooKeeper equivalent:
a broker can fall behind the metadata log while looking perfectly healthy.

| Series | Type & labels | Boards | What it means, and how to read it |
|---|---|---|---|
| `kafka_server_brokermetadatametrics_last_applied_record_lag_ms` | GAUGE, milliseconds | kafka-kraft | How far behind the metadata log this broker is. A lagging broker is up, answers its probes, leads its partitions and serves its clients on an old view of the cluster: advertising leadership that has moved, enforcing the ACLs it last read, unaware of a topic created a minute ago. `KafkaBrokerMetadataLag` fires above 60 s. Computed from a timestamp *inside* the record, so a clock problem makes it meaningless — cross-check the offset form below. |
| `kafka_server_brokermetadatametrics_last_applied_record_offset` | GAUGE | kafka-kraft | The last metadata offset this broker applied. The same gap measured in records rather than milliseconds, and immune to a clock problem: `scalar(max(…)) - max by (kubernetes_pod_name) (…)` is how many records each broker is behind the furthest-ahead one. When this and the millisecond form disagree, believe this one. |
| `kafka_server_brokermetadatametrics_metadata_load_error_count` | GAUGE, cumulative within a process | kafka-kraft | Metadata records the broker could not *read* — corruption, or a metadata-version bump gone wrong. Cumulative but typed GAUGE, and it resets to zero when the broker restarts: read the level and watch for it to move rather than rating it. Nothing in this chart alerts on it. |
| `kafka_server_brokermetadatametrics_metadata_apply_error_count` | GAUGE, cumulative within a process | kafka-kraft | Records the broker read and could not *apply* — the worse of the two. The node is silently diverged from the cluster and stays diverged until it restarts and replays a snapshot. Nothing alerts on it either; the `Metadata errors by kind` panel is the only place it appears. |

## Controller and cluster health — `kafka.controller<…>`

**Only the active controller publishes the `KafkaController` gauges.** When
there is no controller the series vanishes rather than going to zero, so an
empty panel is not a zero.

| Series | Type & labels | Boards | What it means, and how to read it |
|---|---|---|---|
| `kafka_controller_kafkacontroller_activecontrollercount` | GAUGE, 0 or 1 per node | kafka-kraft | Whether this node is the active controller. `sum()` across the cluster must be exactly 1; `sum by (kubernetes_pod_name)` says which node. Zero means the quorum has no leader and nothing new can happen while every existing leader keeps serving — which is why a cluster with no controller can look entirely healthy from a client. `KafkaActiveControllerCount` fires on anything but 1 after 3 minutes. |
| `kafka_controller_kafkacontroller_activebrokercount` | GAUGE | kafka-kraft | Brokers registered and unfenced, as the controller sees them. Only meaningful beside the fenced count. |
| `kafka_controller_kafkacontroller_fencedbrokercount` | GAUGE | kafka-kraft | Brokers the controller has fenced. A fenced broker is running and passing its probes while serving nothing — registered, heartbeat lapsed or metadata not caught up, and stripped of every partition leadership — which is why it is not visible as a down pod. The signal is fenced rising while registered stays flat. `KafkaFencedBrokers` fires above zero for 5 minutes. KRaft-only; there is no ZooKeeper analogue. |
| `kafka_controller_kafkacontroller_offlinepartitionscount` | GAUGE | kafka-kraft | Partitions with no leader at all: every produce and every fetch for them fails. The most severe number on any board here, and the one whose series disappears exactly when the cluster is worst off. `KafkaOfflinePartitions` fires above zero for 2 minutes. |
| `kafka_controller_kafkacontroller_globaltopiccount` | GAUGE | kafka-kraft | Topics in the cluster. Scale rather than health — it is the denominator that makes the rest readable, since twelve under-replicated partitions out of forty is an incident and out of forty thousand is a rolling restart. |
| `kafka_controller_kafkacontroller_globalpartitioncount` | GAUGE | kafka-kraft | Partitions in the cluster. Same purpose. |
| `kafka_controller_kafkacontroller_preferredreplicaimbalancecount` | GAUGE | kafka-kraft | Partitions whose leader is not the first replica in the assignment — leadership skew, which is what a preferred-leader election fixes. It climbs after every broker restart and should fall back on its own; one that stays high is load concentrated on the brokers that came back first. |
| `kafka_controller_kafkacontroller_metadataerrorcount` | GAUGE, cumulative within a process | kafka-kraft | Metadata errors on the controller side rather than the broker side. Shown nowhere before this board and alerted nowhere. |
| `kafka_controller_controllerstats_uncleanleaderelections_total` | COUNTER | kafka-kraft | Leaders elected from a replica that was *not* in the ISR. Every one of these is acknowledged data discarded, so the useful query is `increase(…[10m])` and the useful threshold is any value at all. `KafkaUncleanLeaderElection` has no `for:` for that reason. |
| `kafka_controller_controllereventmanager_eventqueuetimems` | GAUGE, label `quantile` | kafka-kraft | How long a controller event waited in the queue before being processed. **Trap 3** — select `quantile="0.99"`, never `histogram_quantile()`. High queue time with a low event rate is a controller that is blocked, and it is why a cluster can be slow to create a topic while every broker-side panel is calm. |
| `kafka_controller_controllereventmanager_eventqueuetimems_count_total` | COUNTER, events | kafka-kraft | The event *count* behind that histogram. Here the `_count_total` really is a count, so `rate()` is events per second. High rate with low queue time is a busy healthy controller. |

## Replication — `kafka.server<type=ReplicaManager>`, `kafka.cluster<type=Partition>`

The `ReplicaManager` gauges are per broker and sum across the cluster. The
`kafka.cluster` partition gauges are per topic-partition and name what the
counts count.

| Series | Type & labels | Boards | What it means, and how to read it |
|---|---|---|---|
| `kafka_server_replicamanager_underreplicatedpartitions` | GAUGE | kafka-kraft, kafka-performance | Partitions led by this broker whose ISR is smaller than the replication factor. A follower is behind; clients see nothing yet. First rung of the escalation. `KafkaUnderReplicatedPartitions` fires above zero for 5 minutes. |
| `kafka_server_replicamanager_atminisrpartitioncount` | GAUGE | kafka-kraft | Partitions whose ISR is exactly at `min.insync.replicas`: one more failure stops writes. The only rung with lead time, and deliberately un-alerted — it is a normal steady state through a rolling restart, and alerting on it would page for every upgrade. |
| `kafka_server_replicamanager_underminisrpartitioncount` | GAUGE | kafka-kraft | Partitions below `min.insync.replicas`. `acks=all` produce requests now fail with `NOT_ENOUGH_REPLICAS`. `KafkaUnderMinIsrPartitions` fires above zero for 2 minutes. |
| `kafka_server_replicamanager_offlinereplicacount` | GAUGE | kafka-kraft | Replicas this broker holds that are offline, usually because their log directory is. Distinct from offline *partitions*, which is cluster-wide and controller-published. |
| `kafka_server_replicamanager_partitioncount` | GAUGE | kafka-kraft | Partitions this broker hosts a replica of. `sum by (zone)` is where the data is, and whether losing an availability zone would lose a partition. |
| `kafka_server_replicamanager_leadercount` | GAUGE | kafka-kraft, kafka-performance | Partitions this broker leads. `sum by (zone)` is where the *work* is — a zone carrying most of it means most client traffic crosses a zone boundary, which is a cost problem before it is a latency problem. Also the cheapest broker count on the performance board (`count(…)`). |
| `kafka_server_replicamanager_isrshrinks_total` | COUNTER | kafka-kraft, kafka-performance | Replicas removed from an ISR. Always read paired with expands: shrink-then-expand is a follower that fell behind and caught up, while shrinks with no matching expands is a follower that is gone. `KafkaISRShrinkRate` fires above zero for 10 minutes. |
| `kafka_server_replicamanager_isrexpands_total` | COUNTER | kafka-kraft, kafka-performance | Replicas added back to an ISR. The recovery half of the pair. |
| `kafka_server_replicamanager_failedisrupdates_total` | COUNTER | kafka-kraft | ISR changes the controller refused. In KRaft an ISR change is an `AlterPartition` request from the leader to the controller, so a failure is the leader and the controller disagreeing about the partition epoch — which pairs directly with broker metadata lag: a broker that cannot apply metadata cannot get its ISR changes accepted either. |
| `kafka_cluster_partition_underminisr` | GAUGE, 0 or 1, labels `topic`, `partition` | kafka-kraft | Per partition, whether it is below `min.insync.replicas`. This is what names the partitions the `ReplicaManager` count counts, which is the first thing anyone asks. Filter `> 0`; the series exists for every partition. |
| `kafka_cluster_partition_replicascount` | GAUGE, labels `topic`, `partition` | kafka-kraft | The partition's replication factor as assigned. |
| `kafka_cluster_partition_insyncreplicascount` | GAUGE, labels `topic`, `partition` | kafka-kraft | How many of those replicas are currently in sync. `replicascount − insyncreplicascount` per partition is the deficit; a partition showing RF−1 has only its leader left. |

## Throughput — `kafka.server<type=BrokerTopicMetrics>`

All `COUNTER`. **Trap 4 applies to the whole family:** the meters Kafka
registers per topic carry a `topic` label and the broker aggregate carries
none, and an absent label matches `""`. Use `topic=""` for a cluster total and
`topic!=""` when grouping by topic.

| Series | Type & labels | Boards | What it means, and how to read it |
|---|---|---|---|
| `kafka_server_brokertopicmetrics_bytesin_total` | COUNTER, label `topic` on the per-topic registration | kafka-performance | Bytes accepted from producers. `rate(…[5m])` is bytes per second. The single most useful number on the performance board, and the one whose legacy spelling (`…_bytesinpersec`) no rule has ever produced. |
| `kafka_server_brokertopicmetrics_bytesout_total` | COUNTER, label `topic` | kafka-performance | Bytes served to consumers. Bytes out much greater than bytes in means several consumer groups reading the same data — fine, but it means a read-amplification test as well as a write one. |
| `kafka_server_brokertopicmetrics_messagesin_total` | COUNTER, label `topic` | kafka-performance | Records accepted. Divided into bytes in, it gives the average record size actually landing, which is the fastest way to spot a producer batching differently from the test's intent. |
| `kafka_server_brokertopicmetrics_totalproducerequests_total` | COUNTER | kafka-performance | Produce requests, successful or not. The denominator for the failure rate below. |
| `kafka_server_brokertopicmetrics_totalfetchrequests_total` | COUNTER | kafka-performance | Fetch requests. Same role. |
| `kafka_server_brokertopicmetrics_failedproducerequests_total` | COUNTER | kafka-performance | Produce requests the broker refused. Non-zero during a load test usually means the ISR panels are also unhappy. |
| `kafka_server_brokertopicmetrics_failedfetchrequests_total` | COUNTER | kafka-performance | Fetch requests the broker refused, most often a consumer asking for an offset that retention has already removed. |
| `kafka_server_brokertopicmetrics_bytesrejected_total` | COUNTER | kafka-kraft | Bytes in produce requests the broker refused outright — almost always a batch larger than `message.max.bytes`, or a compression type the topic does not allow. Nothing alerts on it and nothing else shows it: the producer sees `RecordTooLargeException`, the cluster sees a healthy day, and the records are simply never written. A step change right after a deploy is a client that raised its batch size past the broker's limit. |
| `kafka_server_brokertopicmetrics_replicationbytesin_total` | COUNTER | kafka-kraft, kafka-performance | Bytes this broker received from leaders as a follower. Replication traffic is invisible in the client-facing throughput panels and is frequently the thing actually saturating the network. |
| `kafka_server_brokertopicmetrics_replicationbytesout_total` | COUNTER | kafka-kraft, kafka-performance | Bytes this broker served to its followers. In and out should track each other cluster-wide; a broker whose out is far above the others is leading far more than its share. |

## Tiered storage (KIP-405 / KIP-963)

Registered **only** when `remote.log.storage.system.enable=true`, which
`tieredStorage.enabled` sets. `optional` in the metric catalogue and under
`live.absentOk`, so a live-scrape job does not expect them. The `kafka-kraft`
board keeps them in a collapsed row, which is the honest alternative to six
permanently empty panels.

The consequence chain worth knowing: a broker that cannot offload keeps
segments on **local** disk, so a cluster sized for its remote tier starts
filling volumes never meant to hold that much. Copy lag is the leading
indicator, the error counters are the trailing one.

| Series | Type & labels | Boards | What it means, and how to read it |
|---|---|---|---|
| `kafka_server_brokertopicmetrics_remotecopybytes_total` | COUNTER, optional | kafka-kraft | Bytes uploaded to the remote tier. `rate()` for upload throughput. |
| `kafka_server_brokertopicmetrics_remotefetchbytes_total` | COUNTER, optional | kafka-kraft | Bytes read back from the remote tier to serve a consumer. Sustained remote fetch means consumers are reading beyond the local retention window — correct, but much slower than a local read. |
| `kafka_server_brokertopicmetrics_remotecopyerrors_total` | COUNTER, optional | kafka-kraft | Failed uploads. `KafkaTieredStorageCopyErrors` fires above zero for 10 minutes. |
| `kafka_server_brokertopicmetrics_remotefetcherrors_total` | COUNTER, optional | kafka-kraft | Failed remote reads — a consumer got an error rather than old data. |
| `kafka_server_brokertopicmetrics_remotecopylagbytes` | GAUGE, bytes, optional | kafka-kraft | Bytes eligible for upload that have not been uploaded yet. The leading indicator: this climbing is local disk filling with segments that should already be gone. |
| `kafka_server_brokertopicmetrics_remotelogsizebytes` | GAUGE, bytes, optional | kafka-kraft | How much data currently lives in the remote tier. Capacity and cost, not health. |

## Request path and network

| Series | Type & labels | Boards | What it means, and how to read it |
|---|---|---|---|
| `kafka_server_kafkarequesthandlerpool_requesthandleravgidlepercent_count_total` | COUNTER, **cumulative nanoseconds** | kafka-kraft, kafka-performance | The best saturation signal a broker has, and **Trap 2**: the meter's `Count` is idle *nanoseconds*, not a count. `avg(rate(…[5m])) / 1e9` is the fraction of the window the handler threads were idle. Below 0.3 the broker is the bottleneck; `KafkaRequestHandlerSaturated` computes exactly this. |
| `kafka_server_kafkarequesthandlerpool_requesthandleravgidle_percent` | GAUGE, 0–1 | kafka-performance | The same meter's `MeanRate` — a **lifetime** mean since the broker started, published under the Percent rule's spelling with the underscore the legacy boards left out. It flattens: a broker up for a week and saturated for an hour still reads a comfortable number. Kept beside the windowed form so the contrast is visible, never as a substitute for it. |
| `kafka_network_socketserver_networkprocessoravgidle_percent` | GAUGE, 0–1 | kafka-kraft, kafka-performance | Fraction of its time the network threads spent idle. These read and write sockets; the handler threads do the work behind them. Already normalised — no `rate()`. Network idle low with handler idle healthy is a broker spending its time on connections rather than requests: many clients, TLS handshakes, or clients reconnecting on every call. |
| `kafka_network_requestchannel_requestqueuesize` | GAUGE | kafka-kraft, kafka-performance | Requests waiting for a handler thread. Near zero on a healthy broker; growing means requests arrive faster than they are served. `KafkaRequestQueueSaturated` fires above 100. |
| `kafka_network_requestchannel_responsequeuesize` | GAUGE | kafka-kraft, kafka-performance | Responses waiting to be written back. A response queue growing while the request queue is flat points at the network threads or the clients, not at the broker's work. |
| `kafka_network_requestmetrics_totaltimems` | GAUGE, labels `request`, `quantile` | kafka-kraft, kafka-performance | End-to-end broker-side latency per request type. Trap 3 — select `quantile="0.99"`. `KafkaRequestLatencyHigh` fires at 1000 ms. |
| `kafka_network_requestmetrics_requestqueuetimems` | GAUGE, labels `request`, `quantile` | kafka-kraft | How much of that total was spent queued. Queue time near the total means saturation and the fix is capacity; queue time near zero with a high total means the work itself is slow, and log flush latency is the next place to look. |
| `kafka_network_requestmetrics_requests_total` | COUNTER, labels `request`, `version` | kafka-kraft | Requests per type, broken down by **API version** — a label nothing else in this repository reads, and the only way to answer *which clients are old*. A version disappearing after an upgrade is how you confirm a fleet actually migrated. |
| `kafka_network_requestmetrics_errors_total` | COUNTER, labels `request`, `error` | kafka-kraft | Responses by Kafka error code. The meter counts **every** response, so `error="NONE"` is the success bucket: exclude it to see failures, keep it to form a ratio. The most specific diagnostic on the KRaft board — `NOT_ENOUGH_REPLICAS` points at the ISR panels, `KAFKA_STORAGE_ERROR` at a log directory, `REQUEST_TIMED_OUT` at saturation, and `UNKNOWN_TOPIC_OR_PARTITION` at the client. |

## Log and storage

| Series | Type & labels | Boards | What it means, and how to read it |
|---|---|---|---|
| `kafka_log_log_size` | GAUGE, bytes, labels `topic`, `partition` | kafka-kraft, kafka-performance | Bytes on disk for one partition. `sum by (kubernetes_pod_name)` is per-broker disk, `topk(20, sum by (topic) (…))` is what is actually filling it. |
| `kafka_log_log_logendoffset` | GAUGE, labels `topic`, `partition` | kafka-performance | The next offset to be written. `sum(max by (topic, partition) (…))` counts the records in a selection — the `max by` matters, because every replica of a partition publishes this and summing raw multiplies by the replication factor. |
| `kafka_log_log_logstartoffset` | GAUGE, labels `topic`, `partition` | kafka-performance | The oldest offset still retained. It only moves when retention or a delete-records call removes data, so a rising start offset during a test is retention cutting into data the test is still producing. |
| `kafka_log_log_numlogsegments` | GAUGE, labels `topic`, `partition` | kafka-performance | Segment files for one partition. Segment count drives open file descriptors and recovery time after an unclean shutdown. |
| `kafka_log_logflushstats_logflushrateandtimems` | GAUGE, label `quantile` | kafka-kraft, kafka-performance | How long an fsync of the log takes. Trap 3. Upstream of almost everything else: slow flushes make produce slow, which makes handlers busy, which makes queues grow, which makes followers fall out of the ISR. When several sections are unhappy at once, this says whether the disk is the cause. `KafkaLogFlushLatencyHigh` fires at 500 ms. |
| `kafka_log_logflushstats_logflushrateandtimems_count_total` | COUNTER, flushes | kafka-kraft | The denominator for the percentile above — a p99 over three flushes in the window is one slow fsync, not a pattern. |
| `kafka_log_logmanager_offlinelogdirectorycount` | GAUGE | kafka-kraft | Log directories the broker has taken offline after an I/O error. With JBOD the broker stays up and serves everything else, so nothing at the pod level changes. `KafkaOfflineLogDirectory` has the shortest `for:` in the chart (1 minute) because there is no benign cause. |
| `kafka_log_logcleanermanager_uncleanable_partitions_count` | GAUGE, label `logDirectory` **with Kafka's quotes inside the value** | kafka-kraft | Partitions the log cleaner has given up on. Only affects compacted topics, and it is quiet in the worst way: compaction stops, the partition grows without bound, and consumers that rely on one record per key keep reading old ones — and `__consumer_offsets` is compacted. The upstream rule captures the directory tag including Kafka's own quotation marks (tracked as `known_label_defects` in the contract), so no selector on that label matches; sum over the broker instead. |
| `kafka_server_kafkaserver_brokerstate` | GAUGE, enum | kafka-kraft | The broker's own lifecycle state as a code: 0 NOT_RUNNING, 1 STARTING, 2 RECOVERY, 3 RUNNING, 6 PENDING_CONTROLLED_SHUTDOWN, 7 SHUTTING_DOWN. It tells a planned restart from an unplanned one — a broker that passes through 6 and 7 was asked to leave, one that reappears at 1 or 2 without ever showing 6 was killed, and one parked at 2 is rebuilding its log and will serve nothing until it finishes, however healthy the pod looks. |

## Security and connections — `kafka.server<type=socket-server-metrics>`

Labels written by the rule: `listener` and `networkProcessor` (that one keeps
its camel case — `lowercaseOutputLabelNames` is not set in the vendored file).
Nothing in this repository alerts on any of these.

| Series | Type & labels | Boards | What it means, and how to read it |
|---|---|---|---|
| `kafka_server_socket_server_metrics_failed_authentication_total` | COUNTER, labels `listener`, `networkProcessor` | kafka-kraft | Rejected authentications. The only place an expired client certificate, a rotated SCRAM password or a misconfigured OAuth issuer is visible from the cluster side. A step at a deployment is a credential change; a slow climb across every listener is certificates issued on the same day reaching expiry together. |
| `kafka_server_socket_server_metrics_successful_authentication_total` | COUNTER, same labels | kafka-kraft | The denominator — five failures a second against fifty successes is a broken client, against five successes it is every client. It is also a churn signal in its own right: a healthy fleet holds long-lived connections and authenticates rarely. |
| `kafka_server_socket_server_metrics_connection_creation_total` | COUNTER, same labels | kafka-kraft | New connections per second per listener. Churn costs the network threads far more than traffic does, which is why this is read beside network processor idle. |
| `kafka_server_socket_server_metrics_connection_count` | GAUGE, same labels | kafka-kraft | Currently open connections. The denominator that makes the other three readable. Also on `strimzi-kafka.json`; repeated here so an operator chasing an auth problem does not have to change board. |

---

# Kafka Connect and MirrorMaker 2 workers

Two rule sets produce these names: `charts/connect-cluster/files/metrics/connect-metrics.yaml`
and the inline rules in `charts/mirror-maker2/templates/metrics-configmap.yaml`.
They agree on the `kafka_connect_*` families below and **disagree on the
embedded-client families and on one label name** — see
[Two rule sets, two spellings](#two-rule-sets-two-spellings).

## Worker, connector and task are three different scopes

Connect publishes the same idea at three levels, and getting the level wrong is
how a board ends up reassuring.

| Scope | Beans | Aggregate with |
|---|---|---|
| **Worker** | `connect-worker-metrics`, `connect-worker-rebalance-metrics`, `connect-coordinator-metrics` | `sum` across pods — and expect them to disagree during a rebalance |
| **Connector** | `connector-metrics` | `max by (connector)` — every worker publishes the *same* connector |
| **Task** | `connector-task-metrics`, `source-task-metrics`, `sink-task-metrics`, `task-error-metrics` | `sum` across tasks for a connector total |

## Worker and rebalance

| Series | Type & labels | Boards | What it means, and how to read it |
|---|---|---|---|
| `kafka_connect_worker_metrics_connector_count` | GAUGE | mirror-maker2 | Connectors assigned to this worker. Sum across workers for the group total. |
| `kafka_connect_worker_metrics_task_count` | GAUGE | mirror-maker2, mirror-maker2-migration | Tasks assigned to this worker. `count(…)` over it is the cheapest "how many workers are reporting", which is why the migration board's go/no-go expression starts with it and deliberately gives it no `or vector(0)` fallback: a release reporting nothing must read as *No data*, not as drained. |
| `kafka_connect_worker_metrics_connector_running_task_count` | GAUGE, label `connector` | kafka-connect, mirror-maker2, mirror-maker2-migration | Tasks of one connector this worker believes are running. Worker-scope, so a worker that vanishes removes its tasks from numerator and denominator at the same instant — the running/total ratio can read 1.0 for the length of a rebalance while tasks are in fact unassigned. Always read beside the worker count. |
| `kafka_connect_worker_metrics_connector_total_task_count` | GAUGE, label `connector` | kafka-connect, mirror-maker2 | Tasks the connector is configured for. The denominator of that ratio. |
| `kafka_connect_worker_metrics_connector_failed_task_count` | GAUGE, label `connector` | mirror-maker2, mirror-maker2-migration | Tasks in FAILED. Above zero is always actionable: Connect does not retry a failed task on its own. |
| `kafka_connect_worker_metrics_connector_paused_task_count` | GAUGE, label `connector` | mirror-maker2 | Tasks deliberately paused. Distinguishes an operator action from a failure. |
| `kafka_connect_worker_metrics_connector_unassigned_task_count` | GAUGE, label `connector` | mirror-maker2 | Tasks with nowhere to run — more tasks than the group's capacity, or a rebalance that has not converged. |
| `kafka_connect_worker_metrics_connector_restarting_task_count` | GAUGE, label `connector` | mirror-maker2 | Tasks mid-restart. Brief is normal; sustained is a task that fails on startup and is being retried. |
| `kafka_connect_worker_metrics_connector_startup_failure_total` | COUNTER | kafka-connect, mirror-maker2 | Connectors that failed to start. `increase(…[15m])` catches a bad config that never produced a running task to fail later. |
| `kafka_connect_worker_metrics_task_startup_failure_total` | COUNTER | kafka-connect, mirror-maker2 | Tasks that failed to start. Same query, one level down. |
| `kafka_connect_worker_rebalance_metrics_completed_rebalances_total` | COUNTER | kafka-connect, mirror-maker2 | Group rebalances completed. Tasks stop during a rebalance, so a storm reads downstream as intermittent lag with no connector ever going red. Also the series both boards' `$namespace`/`$cluster` variables are built from, because a worker publishes it whether or not any connector is deployed. |
| `kafka_connect_worker_rebalance_metrics_rebalance_avg_time_ms` | GAUGE, milliseconds | kafka-connect | How long a rebalance takes. Already an average over Kafka's window — no `rate()`. Long rebalances multiply the cost of a frequent one. |
| `kafka_connect_worker_rebalance_metrics_rebalancing` | GAUGE, 0 or 1 | kafka-connect | Whether a rebalance is in progress right now. Pinned at 1 is a group that cannot converge, which is the failure the two panels above only imply. |
| `kafka_connect_worker_rebalance_metrics_time_since_last_rebalance_ms` | GAUGE, milliseconds | kafka-connect, mirror-maker2 | Milliseconds since the last rebalance — **it resets to zero at every rebalance**. The drops are the signal. Do not `rate()` it and do not let a counter-reset reading smooth the drops away. |
| `kafka_connect_coordinator_metrics_assigned_tasks` | GAUGE, label `clientid` (the rule writes `clientId`; `lowercaseOutputLabelNames` folds it) | kafka-connect | Tasks the group coordinator assigned to this worker. `sum by (pod)` shows whether the group is balanced; one worker holding everything is a rebalance that placed badly. |

## Connector and task status — two info series

Both publish a constant `1` and carry the state in a **label**. That has two
consequences every panel here is built around.

| Series | Type & labels | Boards | What it means, and how to read it |
|---|---|---|---|
| `kafka_connect_connector_metrics` | info series, value `1`, labels `connector` plus one of `connector_class` / `connector_type` / `connector_version` / `status` | kafka-connect | One series **per connector per attribute** — class, type, version and status share the name and differ only in which extra label they carry. Count with `count(max by (connector) (…))`, never over the raw series, and filter `status!=""` when reading status or the table shows four rows per connector, three of them blank (an absent label matches `""`). |
| `kafka_connect_connector_task_status` | info series, value `1`, labels `connector`, `task`, `status` | kafka-connect | One task's state: `running`, `paused`, `failed`, `unassigned`, `restarting`, `destroyed`. Because the state is a label, a task that recovers **stops publishing its `failed` series** instead of publishing a zero — the series goes stale and the count falls on its own, and a `status="failed"` query over a range shows the failure for as long as the series was fresh rather than as a step function. |

## Source tasks

| Series | Type & labels | Boards | What it means, and how to read it |
|---|---|---|---|
| `kafka_connect_source_task_metrics_source_record_poll_total` | COUNTER, labels `connector`, `task` | kafka-connect | Records the connector produced from its source system. `rate()` for records per second read. |
| `kafka_connect_source_task_metrics_source_record_write_total` | COUNTER, labels `connector`, `task` | kafka-connect, mirror-maker2, mirror-maker2-migration | Records written to Kafka after transforms. Polled far above written is a transform or filter dropping records; written far above polled is not possible. On the MirrorMaker boards this is *the* throughput number, filtered by `connector=~".*MirrorSourceConnector"` or `".*MirrorCheckpointConnector"` to separate data from checkpoints. |
| `kafka_connect_source_task_metrics_source_record_active_count` | GAUGE, labels `connector`, `task` | kafka-connect, mirror-maker2, mirror-maker2-migration | Records polled but not yet acknowledged by the producer — the in-flight window. On the migration board this reaching zero is one of the three cutover conditions, because it is what "nothing left in the pipe" looks like in metrics. |
| `kafka_connect_source_task_metrics_source_record_poll_rate` | GAUGE, **already a rate**, labels `connector`, `task` | mirror-maker2 | Kafka's own per-second poll rate over its sampling window. Never `rate()` it. |
| `kafka_connect_source_task_metrics_source_record_write_rate` | GAUGE, **already a rate**, labels `connector`, `task` | mirror-maker2 | The write half. Plotted against the poll rate, the gap between the two lines is what the transform chain is discarding. |

## Sink tasks

| Series | Type & labels | Boards | What it means, and how to read it |
|---|---|---|---|
| `kafka_connect_sink_task_metrics_sink_record_read_total` | COUNTER, labels `connector`, `task` | kafka-connect | Records the sink task consumed from Kafka. |
| `kafka_connect_sink_task_metrics_sink_record_send_total` | COUNTER, labels `connector`, `task` | kafka-connect | Records handed to the sink's `put()` after transforms. Read minus send is what the transform chain dropped. |
| `kafka_connect_sink_task_metrics_sink_record_lag_max` | GAUGE, records, labels `connector`, `task` | kafka-connect | **Not the backlog.** It is the gap between the offsets the task's consumer has reached and the offsets that task has committed — lag *inside* the task, bounded by its in-flight window. A task that stops committing drives it up; a destination hours behind does not. The backlog an operator means lives in `kafka_consumergroup_lag` (the Kafka Exporter, on the Kafka cluster) and in the client's own `records_lag_max`. |
| `kafka_connect_sink_task_metrics_partition_count` | GAUGE, labels `connector`, `task` | kafka-connect | Topic-partitions assigned to this task. Uneven across the tasks of one connector is a consumer-group assignment that placed badly, and it caps how much a single task can do. |
| `kafka_connect_sink_task_metrics_offset_commit_completion_rate` | GAUGE, **already a rate**, labels `connector`, `task` | kafka-connect | Successful offset commits per second. No `rate()`. Falling to zero while records keep flowing is the state that makes a restart reprocess. |

## Task state and offset commits — `connector-task-metrics`

| Series | Type & labels | Boards | What it means, and how to read it |
|---|---|---|---|
| `kafka_connect_connector_task_metrics_offset_commit_avg_time_ms` | GAUGE, milliseconds, labels `connector`, `task` | kafka-connect | How long a commit takes. Already averaged over Kafka's window; aggregate with `max` across tasks, because averaging an average hides the slow task. Commits approaching `offset.flush.timeout.ms` become the failures below. |
| `kafka_connect_connector_task_metrics_offset_commit_failure_percentage` | GAUGE, **fraction 0–1 despite the name**, labels `connector`, `task` | kafka-connect | The share of commits that failed. Display in `percentunit`; `KafkaConnectOffsetCommitFailures` fires at `> 0`. A connector that is moving data and not committing will reprocess everything since its last successful commit on the next restart. |
| `kafka_connect_connector_task_metrics_offset_commit_success_percentage` | GAUGE, fraction 0–1, labels `connector`, `task` | mirror-maker2 | The same quantity from the other side; aggregate with `min by (connector)` so one bad task is not averaged away. |
| `kafka_connect_connector_task_metrics_running_ratio` | GAUGE, fraction 0–1, labels `connector`, `task` | kafka-connect | Fraction of the window the task spent in RUNNING. Already a ratio. Below 1 while the task status says `running` is a task that keeps being restarted. |
| `kafka_connect_connector_task_metrics_pause_ratio` | GAUGE, fraction 0–1, labels `connector`, `task` | kafka-connect | Fraction of the window the task spent paused. Plotted against the running ratio, the two should sum to about 1; a shortfall is time spent neither running nor paused, which is time spent restarting. |

## Task errors and the dead letter queue

**All of these are typed `GAUGE` and all of them are cumulative.** Kafka spells
the attributes with the word at the *front* (`total-errors-logged`,
`total-record-failures`), so they miss the `-total` *suffix* rule that would
type them `COUNTER` and fall through to the gauge rule. It does not change the
query — Prometheus' type metadata does not affect what `rate()` computes, and
`rate()`'s reset handling is exactly what a task restart needs. It changes what
a reader should conclude: do not read them as instantaneous values because the
type says gauge.

| Series | Type & labels | Boards | What it means, and how to read it |
|---|---|---|---|
| `kafka_connect_task_error_metrics_total_errors_logged` | GAUGE, cumulative, labels `connector`, `task` | kafka-connect, mirror-maker2, mirror-maker2-migration | Every error the task logged, tolerated or not. `rate()` for errors per second. The headline error number on three boards. |
| `kafka_connect_task_error_metrics_total_record_errors` | GAUGE, cumulative, labels `connector`, `task` | mirror-maker2 | Records that hit an error in the transform or converter chain. |
| `kafka_connect_task_error_metrics_total_record_failures` | GAUGE, cumulative, labels `connector`, `task` | kafka-connect, mirror-maker2 | Records that failed after retries were exhausted. |
| `kafka_connect_task_error_metrics_total_records_skipped` | GAUGE, cumulative, labels `connector`, `task` | kafka-connect, mirror-maker2 | Records dropped under `errors.tolerance: all`. This is the quiet data-loss number: the task stays RUNNING, no lag panel moves, and the records are gone. |
| `kafka_connect_task_error_metrics_total_retries` | GAUGE, cumulative, labels `connector`, `task` | mirror-maker2 | Retry attempts. Rising with a flat failure count is a task surviving a flaky dependency; rising *with* failures is a retry budget being exhausted. |
| `kafka_connect_task_error_metrics_deadletterqueue_produce_requests` | GAUGE, cumulative, labels `connector`, `task` | kafka-connect, mirror-maker2 | Records written to the dead letter topic. The recoverable form of the skip above — the records still exist somewhere. |
| `kafka_connect_task_error_metrics_deadletterqueue_produce_failures` | GAUGE, cumulative, labels `connector`, `task` | kafka-connect, mirror-maker2 | Dead-letter writes that themselves failed, so the record is gone for good. Plot against requests; any gap is unrecoverable loss. |
| `kafka_connect_task_error_metrics_last_error_timestamp` | GAUGE, Unix milliseconds, labels `connector`, `task` | mirror-maker2 | When the task last errored. A timestamp, not a count: filter `!= 0` (zero means never) and display as a date, never `rate()`. Useful for "did this stop an hour ago or is it happening now". |

## MirrorMaker 2 mirror connectors

Produced only by `charts/mirror-maker2`'s own rules, which come **before** the
generic Connect patterns in the file — with the generic rules first, every
mirror metric collapsed into a client-labelled Connect series and the topic and
partition dimensions were lost.

The `source` label exists only when the connector runs with
`add.source.alias.to.metrics: true` (KIP-1006, off by default); both shapes are
in the rules, wider first. The `topic` tag is the **replicated** name, which is
why a per-source selector can match an alias prefix under a prefixing
replication policy and cannot under an identity one.

| Series | Type & labels | Boards | What it means, and how to read it |
|---|---|---|---|
| `kafka_connect_mirror_source_connector_replication_latency_ms_max` | GAUGE, milliseconds, labels `target`, `topic`, `partition`, optionally `source` | mirror-maker2, mirror-maker2-migration | End-to-end: source append to target write. The number a DR objective is written against. **A partition with nothing to replicate reports `NaN`**, and `max` ignores NaN only while some partition has a number — an idle mirror is NaN everywhere, which reads as "over the objective" in a `<= bool` comparison. Hence `{…} >= 0` on the migration board and in the recording rules. |
| `kafka_connect_mirror_source_connector_record_age_ms_max` | GAUGE, milliseconds, same labels | mirror-maker2, mirror-maker2-migration | How old the records the fetchers are currently reading are — how far behind the source's log the mirror is *reading*, as opposed to how long the crossing takes. On the migration board this is the series the catch-up ETA extrapolates: a straight line down is a healthy drain, a flat line at the top is a mirror keeping pace with production rather than catching up. Same NaN treatment. |
| `kafka_connect_mirror_source_connector_byte_rate` | GAUGE, **already a rate**, same labels | mirror-maker2, mirror-maker2-migration | Bytes per second crossing, per replicated topic. Never `rate()`. |
| `kafka_connect_mirror_source_connector_record_rate` | GAUGE, **already a rate**, same labels | mirror-maker2-migration | Records per second crossing, per replicated topic. |
| `kafka_connect_mirror_checkpoint_connector_checkpoint_latency_ms_max` | GAUGE, milliseconds, labels `source`, `target`, `group`, `topic`, `partition` | mirror-maker2, mirror-maker2-migration | How stale the **offset translations** are for a consumer group — how far behind the positions a failover would resume from. Deliberately separate from replication lag: the data can be current while the translations are hours old, and a failover then rewinds consumers by hours. `group` is the label that makes it actionable. |

## Embedded clients inside the workers

Every Connect worker runs producers and consumers of its own. **This is where
the two rule sets disagree**, so the same quantity has two names and two label
spellings depending on which chart deployed the worker.

| Series | Type & labels | Boards | What it means, and how to read it |
|---|---|---|---|
| `kafka_producer_producer_record_send_rate` | GAUGE, already a rate, label `clientid` | kafka-connect | Records per second the worker's producers sent to Kafka. `clientid` distinguishes each connector's producer from the worker's own internal-topic producer. |
| `kafka_producer_producer_record_error_rate` | GAUGE, already a rate, label `clientid` | kafka-connect | Records the producer failed to send. Non-zero with a healthy task means the retries are still absorbing it — for now. |
| `kafka_producer_producer_record_retry_rate` | GAUGE, already a rate, label `clientid` | kafka-connect | Records being retried. Retries rising before errors do is the earliest warning the target cluster is struggling. |
| `kafka_producer_producer_request_latency_avg` | GAUGE, milliseconds, label `clientid` | kafka-connect | Produce round-trip from the worker. Already averaged; aggregate with `max`. |
| `kafka_consumer_consumer_fetch_manager_records_consumed_rate` | GAUGE, already a rate, label `clientid` | kafka-connect | Records per second the worker's consumers fetched. Covers the sink connectors' consumers *and* the workers' own config/status/offset topic consumers, which belong to no connector. |
| `kafka_consumer_consumer_fetch_manager_records_lag_max` | GAUGE, records, label `clientid` | kafka-connect | How far behind the end of the log the consumer is, measured by the client. The real backlog, as opposed to the sink task's in-flight lag. |
| `kafka_producer_record_send_rate` | GAUGE, already a rate, label `client_id` | mirror-maker2 | The MirrorMaker spelling of the same producer meter. Select `client_id=~"connector-producer-.*"` to exclude the worker's internal producers. |
| `kafka_producer_record_error_rate` | GAUGE, already a rate, label `client_id` | mirror-maker2 | Produce errors against the target cluster. |
| `kafka_producer_record_retry_rate` | GAUGE, already a rate, label `client_id` | mirror-maker2 | Produce retries against the target cluster. |
| `kafka_producer_request_latency_avg` | GAUGE, milliseconds, label `client_id` | mirror-maker2 | Produce round-trip to the target. |
| `kafka_producer_request_latency_max` | GAUGE, milliseconds, label `client_id` | mirror-maker2 | Worst produce round-trip in Kafka's window. A max measurable, not a counter — no `rate()`. |
| `kafka_producer_buffer_available_bytes` | GAUGE, bytes, label `client_id` | mirror-maker2 | Free space in the producer's accumulator. Falling towards zero means the producer is about to block, which stalls replication from the *source* side even though every target-side panel looks fine. `min(...)` is the right aggregate. |
| `kafka_consumer_fetch_manager_records_consumed_rate` | GAUGE, already a rate, labels `client_id`, `topic` | mirror-maker2 | Records per second read from the **source** cluster, per topic. Carries `topic`, which the connect-cluster spelling does not. |
| `kafka_consumer_fetch_manager_bytes_consumed_rate` | GAUGE, already a rate, labels `client_id`, `topic` | mirror-maker2 | Bytes per second read from the source, per topic. |

### Two rule sets, two spellings

Worth stating plainly, because copying an expression from one board to the
other silently produces an empty panel.

| Quantity | `charts/connect-cluster` | `charts/mirror-maker2` |
|---|---|---|
| Consumer fetch rate | `kafka_consumer_consumer_fetch_manager_records_consumed_rate` | `kafka_consumer_fetch_manager_records_consumed_rate` |
| Producer send rate | `kafka_producer_producer_record_send_rate` | `kafka_producer_record_send_rate` |
| Client identity label | `clientid` (the rule writes `clientId`; `lowercaseOutputLabelNames: true` folds it) | `client_id` |
| Topic on consumer series | absent | present |

The doubled word in the connect-cluster names is not a typo: its rule is
`name: kafka_$1_$2_$4` over `kafka.(producer|consumer|…)<type=(.+)-metrics, …>`,
so the domain and the bean type both land in the name. MirrorMaker 2's rules
name those families explicitly and drop the repetition.

---

# Prometheus recording rules

## `mm2:*` — `charts/mirror-maker2/templates/alerts.yaml`

Recorded only when `alerts.enabled` **and** `alerts.slo.enabled` are both set.
That is why the mirror board has a `no-slo` variant: Grafana has no "hide this
row if the series is absent", so the generator builds the board both ways and
the chart picks one. All five carry `namespace` from the recording expression
and `cluster` (and usually `source`) from the rule's own labels.

| Series | Type & labels | Boards | What it means, and how to read it |
|---|---|---|---|
| `mm2:replication_latency_ms:max` | recorded GAUGE, labels `namespace`, `cluster`, `source` | mirror-maker2 | Worst replication latency for one source leg, recorded so a status page and the SLO rules read the same number without knowing the exporter's names. Under a prefixing policy it is scoped by the alias's topic prefix; under an identity policy there is no prefix, so the rule is release-wide and carries no `source`. |
| `mm2:checkpoint_latency_ms:max` | recorded GAUGE, labels `namespace`, `cluster`, `source` | mirror-maker2 | The same for offset-translation age, scoped by the `source` label on the checkpoint bean. |
| `mm2:tasks_running:ratio` | recorded GAUGE, 0–1, labels `namespace`, `cluster`, `source` | mirror-maker2 | Running tasks over total tasks for one leg. Already a ratio. |
| `mm2:slo_replication_latency:error_ratio_rate5m` | recorded GAUGE, 0–1, labels `namespace`, `cluster` | mirror-maker2 | The fraction of the last five minutes the leg spent **over** its latency objective — `1 - avg_over_time((SLI <= bool objective)[5m:])`. The fast window of a burn-rate alert. |
| `mm2:slo_replication_latency:error_ratio_rate1h` | recorded GAUGE, 0–1, labels `namespace`, `cluster` | mirror-maker2 | The same over an hour. `MirrorMaker2ReplicationSLOBurning` needs both windows over budget, so a bad hour that ended ten minutes ago does not page and neither does one slow minute. |

## The dead six: `kafka:chaos:*`

`charts/monitoring/templates/prometheus-chaos-rules.yaml` defines seven
recording rules in the `kafka-chaos-rto-rpo` group and they install cleanly.
**Every one of them reads a series that nothing in this repository publishes.**
Six are read by `kates-chaos`; the seventh, `kafka:chaos:max_rto_seconds`, is
read only by an alert and appears on no board.

| Recorded series | Reads | Published by |
|---|---|---|
| `kafka:chaos:producer_rto_seconds` | `kates_integrity_result_producer_rto_seconds` | nothing |
| `kafka:chaos:consumer_rto_seconds` | `kates_integrity_result_consumer_rto_seconds` | nothing |
| `kafka:chaos:rpo_seconds` | `kates_integrity_result_rpo_seconds` | nothing |
| `kafka:chaos:data_loss_percent` | `kates_integrity_result_data_loss_percent` | nothing |
| `kafka:chaos:e2e_latency_ms` | `kafka_chaos_e2e_latency_ms` | nothing |
| `kafka:chaos:producer_throughput` | `kafka_chaos_producer_throughput_records_per_sec` | nothing |

A recording rule over a series that does not exist records nothing, so all six
are permanently empty, and four alerts sitting on them (`KafkaRTOExceedsSLA`,
`KafkaRPOExceedsSLA`, `KafkaDataLossDetected`, `KafkaE2ELatencySpike`) can
never fire.

**To make the row work**, the values have to be registered with Micrometer:
`IntegrityResult` already carries `producerRto`, `consumerRto`, `maxRto`, `rpo`
and `dataLossPercent`, the verifier fills them in on every run with an
integrity check, and they reach the REST API and the run report — they are
simply never published as metrics. `dashboards/kates-chaos/README.md` sets out
the two ways to close the gap. Until then the row is collapsed with a text
panel at the top of it saying so, because an RTO SLA that is documented,
alerted on and unobservable is worse than no RTO SLA.

---

# The Kates application — Micrometer

`quarkus-micrometer-registry-prometheus`, exposed at `/q/metrics` and scraped
by `charts/kates`'s ServiceMonitor. Micrometer's naming turns the dots into
underscores and appends `_total` to a counter whose name does not already end
in it — so the boards read the *published* spelling, not the registered one.
`BenchmarkMetricsTest` asserts every name below against a real
`PrometheusMeterRegistry`, because guessing that translation is what left four
of these names unregistered for so long.

## Benchmark meters — per run, and they disappear between runs

Registered by `BenchmarkMetrics.java`. **Every meter here is tagged with
`run_id`, which is unbounded over time, so `endRun` unregisters the whole set
when a run finishes.** That is deliberate — without it each run permanently
added time series and Prometheus memory grew without bound — and it is why
`kates-benchmark` is empty between runs by design, and why `kates-trend`'s
template variables are built on a platform counter instead.

| Series | Type & labels | Boards | What it means, and how to read it |
|---|---|---|---|
| `kates_benchmark_active_runs` | GAUGE, no tags | kates-benchmark, kates-chaos, kates-overview | Benchmark runs in flight. The one benchmark series that exists with no run active (it reads zero), which is why `kates-chaos`'s `$kates_job` variable is built on it. |
| `kates_benchmark_throughput_rec_sec` | GAUGE, labels `run_id`, `test_type`, `backend` | kates-benchmark, kates-chaos, kates-overview, kates-trend | Current throughput in records per second, as the backend last reported it. Already a rate — no `rate()`. On the trend board, `max by (run_id, test_type)` turns it into one peak per run. |
| `kates_benchmark_throughput_mb_sec` | GAUGE, labels `run_id`, `test_type`, `backend` | kates-benchmark, kates-trend | The same in megabytes per second. Read beside the record rate: the two diverging means the record size changed, not the cluster. |
| `kates_benchmark_records_total` | COUNTER, labels `run_id`, `test_type`, `phase` | kates-benchmark, kates-trend | Records the phase has processed. Fed with each task's **cumulative** total clamped upward (`Math::max`), so the sum is monotonic and `rate(…[30s])` means something; a backend that restarts a task and re-counts from zero therefore holds the counter flat rather than resetting it. |
| `kates_benchmark_errors_total` | COUNTER, labels `run_id`, `test_type`, `phase` | kates-benchmark, kates-chaos | Tasks that reached a FAILED state, per phase. `rate()` for an error rate; the absolute value is the run's total. |
| `kates_benchmark_latency_ms` | GAUGE with a `quantile` label, plus `run_id`, `test_type`, `phase` | kates-benchmark, kates-chaos, kates-trend | Latency percentiles in milliseconds — `0.5`, `0.95`, `0.99`, `0.999`. **Not a Micrometer `DistributionSummary`**, despite publishing under the names one would use: the engine never sees individual latencies, only the aggregates a poll returns, and feeding those into a summary would publish percentiles *of averages*. Trap 3 applies — select the quantile, never `histogram_quantile()`, and never average two quantiles together. A phase with several tasks reports the worst task's value, recomputed on every poll, so a phase recovers on the board when its slowest task does. |
| `kates_benchmark_latency_ms_max` | GAUGE, labels `run_id`, `test_type`, `phase` | kates-benchmark | The worst latency observed in the phase. A max gauge — no `rate()`. |
| `kates_benchmark_sla_violations` | GAUGE, 0 or 1, labels `run_id`, `test_type`, `metric`, `severity` | kates-benchmark | 1 while that SLA constraint is violated by the run, 0 once it recovers. Zero rather than removal on purpose: the board graphs `sum by (metric, severity)` over time, and a constraint that stopped publishing would leave a gap that reads like a constraint that was never evaluated. A constraint that has never been violated is never registered at all, so the board shows only what actually broke. |

## Platform meters — cumulative, and they outlive every run

Registered by `KatesMetrics.java`. No `run_id`, so they survive an idle
cluster — which is exactly when a trend board is opened.

| Series | Type & labels | Boards | What it means, and how to read it |
|---|---|---|---|
| `kates_tests_completed_total` | COUNTER, labels `test_type`, `outcome` | kates-overview, kates-trend | Tests that finished, by outcome. `rate(…[5m])` by `outcome` is the pass/fail mix over time; the raw value is the platform's lifetime total. `kates-trend`'s `$job` and `$test_type` variables are built on it precisely because it does not empty out between runs. |
| `kates_tests_duration_seconds` | Micrometer `Timer` with `publishPercentiles(0.5, 0.95, 0.99)`, label `test_type` plus `quantile` | kates-trend | How long a test takes end to end. The `quantile` series are **client-side percentiles computed per JVM instance** — select one, and do not aggregate them across replicas, because percentiles do not average. The Timer also publishes `_count`, `_sum` and `_max` for that. |
| `kates_tests_throughput_rec_sec` / `kates_tests_throughput_mb_sec` | `DistributionSummary`, label `test_type` | *(no board reads them)* | Final throughput per completed test. Registered and exported; nothing in `dashboards/` plots them, and they are listed here so a reader finds them rather than re-adding them under a new name. |
| `kates_sla_evaluations_total` | COUNTER, labels `test_type`, `result` (`pass`/`fail`) | kates-trend | SLA evaluation outcomes across every run. `sum by (test_type, result) (rate(…[5m]))` is the pass rate over time — the platform-level counterpart to the per-run violation gauge. |
| `kates_records_processed_total` | COUNTER, label `test_type` | kates-trend | Cumulative records processed by the whole platform. Unlike `kates_benchmark_records_total` it does not disappear when the run does, which makes it the only honest "how much has this installation ever moved". |
| `kates_disruptions_completed_total` | COUNTER, labels `disruption_type`, `outcome` | kates-trend | The engine's **own** injected disruptions, not LitmusChaos experiments. Two different chaos mechanisms; this one is the engine's. |
| `kates_disruptions_duration_seconds` | `Timer` with `publishPercentiles(0.5, 0.95)`, label `disruption_type` plus `quantile` | kates-trend | How long a disruption lasted. Same client-side-percentile caveat as the test duration. |

## HTTP server — the Quarkus Micrometer binder

`quarkus.micrometer.binder.http-server.enabled=true`.

| Series | Type & labels | Boards | What it means, and how to read it |
|---|---|---|---|
| `http_server_requests_seconds_count` | COUNTER, labels `method`, `uri`, `status`, `outcome` | kates-application, kates-overview | Requests served by the Kates REST API. `rate()` by `method` is traffic, by `status=~"4..\|5.."` is failure. 4xx and 5xx are different problems: a 4xx is a client sending something this API rejected (a malformed spec, an unknown run id) and is not an outage; every 5xx has a stack trace in the log. |
| `http_server_requests_seconds_bucket` | **real cumulative histogram**, label `le` plus the above | kates-application, kates-overview | Request latency. The only place on any Kates board where `histogram_quantile(0.99, sum by (le) (rate(…[1m])))` is correct — everything else with percentiles here is a pre-computed gauge. |

## JVM and process — **Micrometer's spelling, not the JMX agent's**

This is the second trap the Kates boards exist around. The same four words name
two different series in this repository, and neither is a fallback for the
other: copying a JVM expression from a broker board onto a Kates board produces
an empty panel for a reason nobody finds quickly.

| Quantity | Kates boards (Micrometer) | Kafka / Connect / MM2 boards (JMX agent) |
|---|---|---|
| Heap used | `jvm_memory_used_bytes{area="heap"}` | `jvm_memory_bytes_used{area="heap"}` (0.x) or `jvm_memory_used_bytes` (1.x) |
| GC | `jvm_gc_pause_seconds_sum` (labels `cause`, `action`) | `jvm_gc_collection_seconds_sum` (label `gc`) |
| Live threads | `jvm_threads_live_threads` | `jvm_threads_current` |
| CPU | `process_cpu_usage`, `system_cpu_usage` (0–1 gauges) | `process_cpu_seconds_total` (counter) |

The overlap is real: `jvm_memory_used_bytes`, `jvm_memory_committed_bytes` and
`jvm_memory_max_bytes` are **both** Micrometer's names and the JMX agent's 1.x
names, which is why the Kafka boards write `jvm_memory_used_bytes{…} or
jvm_memory_bytes_used{…}` — a worker image may carry either agent line, a live
capture holds exactly one of each pair, and the contract's `alternatives`
records that.

| Series | Type & labels | Boards | What it means, and how to read it |
|---|---|---|---|
| `jvm_memory_used_bytes` | GAUGE, bytes, label `area` (`heap`/`nonheap`), `id` | kafka-connect, kafka-performance, kates-application, kates-overview, mirror-maker2 | Heap (or non-heap) in use. Micrometer's name on the Kates boards; the JMX agent's 1.x name on the others, always written with `or jvm_memory_bytes_used`. |
| `jvm_memory_bytes_used` | GAUGE, bytes, label `area` | kafka-connect, kafka-performance, mirror-maker2 | The JMX agent's 0.x spelling of the same quantity. |
| `jvm_memory_committed_bytes` | GAUGE, bytes, label `area` | kafka-performance, kates-application, kates-overview | Heap the JVM has actually reserved from the OS. Committed pinned at max with GC time rising beside it is a heap too small for the workload. |
| `jvm_memory_bytes_committed` | GAUGE, bytes, label `area` | kafka-performance | The 0.x spelling. |
| `jvm_memory_max_bytes` | GAUGE, bytes, label `area` | kafka-connect, kates-application | The configured ceiling. Used/max is the only heap number that is comparable across differently-sized pods. |
| `jvm_memory_bytes_max` | GAUGE, bytes, label `area` | kafka-connect | The 0.x spelling. |
| `jvm_gc_collection_seconds_sum` | COUNTER, seconds, label `gc` | kafka-performance, mirror-maker2 | **JMX agent.** Cumulative time in GC. `rate()` gives seconds of GC per second — the fraction of wall clock the JVM spent collecting, which is directly comparable to a tail-latency plot. |
| `jvm_gc_collection_seconds_count` | COUNTER, label `gc` | kafka-performance | **JMX agent.** Collections. Read with the sum: many short collections and one long one have the same total and very different latency effects. |
| `jvm_gc_pause_seconds_sum` | COUNTER, seconds, labels `cause`, `action` | kates-application | **Micrometer.** Cumulative pause time. `rate()` for the pause fraction. `cause` and `action` tell an allocation-failure young collection from a full one. |
| `jvm_gc_pause_seconds_max` | GAUGE, seconds | kates-application | **Micrometer.** The worst pause in the binder's window — a gauge, not a counter. Never `rate()` it. |
| `jvm_threads_current` | GAUGE | kafka-performance | **JMX agent.** Live threads on a broker. |
| `jvm_threads_live_threads` | GAUGE | kates-application | **Micrometer.** Live threads. Climbing with concurrent runs is by design; staying high after the runs finish is a leaked executor. |
| `jvm_threads_daemon_threads` | GAUGE | kates-application | **Micrometer.** Daemon threads. Live minus daemon is the non-daemon set that can keep the JVM from exiting. |
| `jvm_threads_peak_threads` | GAUGE | kates-application | **Micrometer.** High-water mark since start. It never falls, so it is a ceiling to size against rather than a health signal. |
| `process_cpu_seconds_total` | COUNTER, seconds | kafka-performance | **JMX agent.** `rate()` is CPU cores used by the broker process. |
| `process_resident_memory_bytes` | GAUGE, bytes | kafka-performance | **JMX agent.** RSS of the broker process. Plotted against heap, the gap is page cache, direct buffers and the JVM itself — and Kafka leans on page cache hard, so RSS far above heap is normal and healthy here. |
| `process_open_fds` | GAUGE | kafka-performance | **JMX agent.** Open file descriptors. Kafka holds one per log segment plus one per connection, so this tracks segment count and client count, and hitting the limit takes the broker down in a way no Kafka metric explains. |
| `process_uptime_seconds` | GAUGE, seconds | kates-application, kates-overview | **Micrometer.** Seconds since the JVM started — the process, not the pod, so it excludes container start and JVM boot. Both Kates boards build their `$namespace`/`$job`/`$pod` variables on it, because it exists for every scraped instance whether or not anything else is happening. |
| `process_cpu_usage` | GAUGE, 0–1 | kates-overview | **Micrometer.** This process's share of the machine. Already normalised. |
| `system_cpu_usage` | GAUGE, 0–1 | kates-overview | **Micrometer.** The whole machine's. Process low with system high is a noisy neighbour, not this application. |

## The Agroal connection pool

Registered by Quarkus' `AgroalMetricsRecorder` when the datasource and
Micrometer extensions are both present. Labelled by datasource.

Two corrections landed here in this refactor. The board this replaced read
`agroal_blocking_time_total_seconds` divided by `agroal_blocking_time_count`,
and **neither name has ever existed**: Quarkus registers the timing gauges with
`baseUnit("milliseconds")`, so the published names end in `_milliseconds` and
there is no `_count` companion at all. An empty series divided by an empty
series draws exactly like a pool nobody is waiting on. The other three were
spelled `agroal_pool_*` where Quarkus publishes `agroal_*`.

| Series | Type & labels | Boards | What it means, and how to read it |
|---|---|---|---|
| `agroal_active_count` | GAUGE | kates-application, kates-overview | Connections currently checked out. Pinned at the pool ceiling with `awaiting` above zero is contention, and raising the pool size fixes it. |
| `agroal_available_count` | GAUGE | kates-application, kates-overview | Connections idle in the pool and ready to hand out. |
| `agroal_max_used_count` | GAUGE | kates-application, kates-overview | High-water mark of concurrent use. It never falls, so it sizes the pool rather than diagnosing it. |
| `agroal_awaiting_count` | GAUGE | kates-application, kates-overview | Threads blocked waiting for a connection. Anything above zero for any length of time is the application throttling itself. |
| `agroal_blocking_time_average_milliseconds` | GAUGE, milliseconds | kates-application, kates-overview | Mean wait to acquire a connection. Rising **with** the pool at its ceiling is a pool too small; rising **with spare capacity** is a slow database, and a bigger pool will not help. That distinction is the whole point of reading this panel beside `Pool connections`. |
| `agroal_blocking_time_max_milliseconds` | GAUGE, milliseconds | kates-application, kates-overview | Worst wait. A gauge — no `rate()`. |
| `agroal_acquire_count` | **GAUGE holding a cumulative total** | kates-application | Acquisitions since start. Quarkus registers it with `Gauge.builder`, so it carries no `_total` suffix and Prometheus does not type it as a counter. `rate()` still gives the right answer because the value only increases, but it will not be corrected for a restart the way a real counter is — expect one spike per pod restart. |

---

# Kubernetes and the cluster add-ons

## kube-state-metrics

The `kube-state-metrics` subchart of `kube-prometheus-stack`, which
`charts/monitoring` enables by default. Not produced by any rule in this
repository, and the metric contract lists `^kube_` as `external`.

| Series | Type & labels | Boards | What it means, and how to read it |
|---|---|---|---|
| `kube_pod_status_ready` | GAUGE, 0 or 1, labels `namespace`, `pod`, `condition` | kates-application, kates-chaos | Whether a pod's readiness condition holds. Filter `condition="true"` — the series also exists for `condition="false"` and `"unknown"`, and summing without the filter counts every pod three times. Published for every pod in every watched namespace whether or not anything else is installed, which is why both boards build their `$namespace`/`$pod` variables on it. |
| `kube_pod_container_status_restarts_total` | COUNTER, labels `namespace`, `pod`, `container` | kates-application, kates-chaos | Container restarts. `increase(…[5m])` is the only useful form: the raw value is cumulative for the pod's lifetime, so a pod that crash-looped yesterday and is fine now still reads high. |
| `kube_pod_status_phase` | GAUGE, 0 or 1, labels `namespace`, `pod`, `phase` | kyverno-security, kates-chaos-infra | Pod phase as a one-hot set. Used on the Kyverno board with `phase="Running"` to show the controller pods — deliberately *not* from Kyverno itself, because a webhook that is down cannot report that it is down. An empty table means either no controller is Running or kube-state-metrics is not installed, and those are very different problems. |
| `kube_deployment_status_replicas_available` | GAUGE, labels `namespace`, `deployment` | kates-chaos-infra | Replicas of a Deployment that are available. Used for the Litmus chaos operator, because an operator that is down cannot report that it is down — and because the board this replaces asked for pods matching `.*chaos-operator.*`, which is the operator's *container* name: its Deployment is named by litmus-core's `fullnameOverride` (`litmus`), so that matcher could never match and the tile was a permanent, reassuring zero. The Deployment name is a hidden `constant` the chart injects. |
| `kube_customresource_chaosengine_status_engine_status` | GAUGE, labels `namespace`, `status` | kates-chaos | Litmus `ChaosEngine` custom resources and their status. **Requires kube-state-metrics to be configured with custom-resource metrics for `ChaosEngine`, which is not the default.** Without that configuration the series does not exist, the panel's `or vector(0)` draws a reassuring zero indistinguishable from "no experiment running", and the `and on() count(…) == 0` guard on `KafkaBrokerRestartUnexpected` is always true. Confirm the series exists before relying on either. |

## cAdvisor, via the kubelet

Scraped when `kube-prometheus-stack.kubelet.enabled` is true, which it is.
`^container_` is `external` in the metric contract. All carry `namespace`,
`pod`, `container` and `image`; filter `container!=""` and `container!="POD"`
or the pause container and the pod-level rollup are counted as well.

| Series | Type & labels | Boards | What it means, and how to read it |
|---|---|---|---|
| `container_cpu_usage_seconds_total` | COUNTER, seconds | kates-application, kates-chaos | Cumulative CPU. `rate(…[5m])` is cores used, directly comparable to the container's CPU limit. |
| `container_memory_working_set_bytes` | GAUGE, bytes | kates-application | **The number the OOM killer acts on** — anonymous memory plus active page cache. This is the one to compare against the memory limit, not RSS and not the JVM heap. |
| `container_memory_rss` | GAUGE, bytes | kates-application | Resident anonymous memory. Lower than working set, and not what triggers an OOM kill; useful beside it to see how much of the footprint is page cache. |
| `container_memory_usage_bytes` | GAUGE, bytes | kates-chaos | The broadest of the three — working set plus inactive cache. It routinely sits near the limit on a healthy container and is the one most likely to look alarming for no reason. |

## LitmusChaos

Published by the Litmus **chaos-exporter**, which is installed with LitmusChaos
and **is not shipped by this repository**. `charts/kates-chaos` deploys the
execution plane, but leaves the exporter itself off
(`litmus-core.exporter.enabled`), so all eight can be empty on a cluster that
does have Litmus. If they are, Litmus is not installed, its exporter is off,
or nothing is scraping it — nothing is wrong with the cluster.

The counters are cumulative since the exporter started, not since the current
Game Day, so an absolute value carries an unknown amount of history.

The exporter publishes three families: per-ChaosResult (`litmuschaos_passed_experiments`
and friends), namespace-scoped aggregates, and cluster-scoped aggregates.
litmus-core deploys it with an empty `WATCH_NAMESPACE`, which makes it
aggregate across every namespace — so the `litmuschaos_cluster_scoped_*`
family is the one that fills and `litmuschaos_namespace_scoped_*` is always
empty on this install. Nothing here reads the namespace-scoped family.

| Series | Type & labels | Boards | What it means, and how to read it |
|---|---|---|---|
| `litmuschaos_passed_experiments` | GAUGE, cumulative | kates-chaos | Experiments Litmus graded as passed. "Passed" means Litmus' own probes were satisfied; it says nothing about what the workload underneath experienced, which is what the Kates rows beside it are for. |
| `litmuschaos_failed_experiments` | GAUGE, cumulative | kates-chaos | Experiments Litmus graded as failed. Often not a cluster problem — Litmus marks an experiment failed when its own probe could not run or the chaos could not be injected at all. |
| `litmuschaos_probe_success_percentage` | GAUGE, 0–100 | kates-chaos | Share of probes that succeeded. A percentage, not a fraction. |
| `litmuschaos_experiment_total_duration` | GAUGE, seconds, labels `chaosengine_name`, `chaosengine_context`, `chaosresult_name`, `chaosresult_namespace` | kates-chaos, kates-chaos-infra | How long each experiment ran — the duration of the *injection*, not of the recovery. On a timeline it is the fault window itself, and everything else on the chaos board is read relative to it. The exporter names it `_total_duration`, with no unit suffix, even though the value is seconds; there is no `litmuschaos_experiment_duration_seconds`. |
| `litmuschaos_experiment_verdict` | GAUGE, 0 or 1, labels `chaosresult_verdict` (`Pass`/`Fail`/`Stopped`/`Awaited`), `chaosengine_name`, `chaosengine_context`, `chaosresult_name`, `chaosresult_namespace`, `app_kind`, `app_label`, `app_namespace`, `probe_success_percentage` | kates-chaos-infra | One series per ChaosResult carrying its current verdict **as a label**. **The label is `chaosresult_verdict`, not `verdict`** — the board this replaces selected `verdict="Pass"`, which matched nothing and therefore counted every verdict. Select the value AND compare to 1: the exporter sets the series to 0 for an Awaited verdict, and back to 0 once a verdict has been repeated for longer than its `TSDB_SCRAPE_INTERVAL`, so a count of the series and a count of the ones reading 1 are different numbers. Never `rate()` or `increase()` it — it is a gauge that flips between 0 and 1, and the run total is the cluster-scoped counter below. |
| `litmuschaos_cluster_scoped_experiments_run_count` | GAUGE, cumulative, no labels | kates-chaos-infra | Every experiment run the exporter has seen in any namespace: the sum of passed + failed + awaited across every ChaosResult in the cluster. This is the series a *how much chaos have we actually run* panel wants. Monotonic, so read the change across a GameDay rather than the absolute number. |
| `litmuschaos_cluster_scoped_passed_experiments` | GAUGE, cumulative, no labels | kates-chaos-infra | The same aggregate for passed runs. Plotted beside the run count: a step in runs with no step here or in the failed line is an experiment still awaited. |
| `litmuschaos_cluster_scoped_failed_experiments` | GAUGE, cumulative, no labels | kates-chaos-infra | The same aggregate for failed runs. As with the per-result counter, a failure is often a chaos-tooling problem rather than a cluster problem. |

## Kyverno

Published by the Kyverno admission controller, **not shipped by this
repository**. `charts/kates` ships the *board* when `kyvernoPolicy.enabled` and
`kyvernoPolicy.grafanaDashboard` are both set and the cluster has the
`kyverno.io/v1` API; Kyverno itself, and a scrape of it, are separate.

| Series | Type & labels | Boards | What it means, and how to read it |
|---|---|---|---|
| `kyverno_admission_requests_total` | COUNTER, labels `resource_request_operation`, `request_allowed` | kyverno-security | Admission requests the webhook handled. It rises with deployments, not with violations. `request_allowed="false"` is the refused set, and it carries `or vector(0)` on the board because with every policy in Audit mode nothing is ever refused, the series does not exist, and *No data* would look like a broken board rather than a quiet one. Use `increase(…[1h])`. |
| `kyverno_policy_results_total` | COUNTER, labels `policy_name`, `rule_name`, `rule_result` (`pass`/`fail`/`warn`), plus `policy_namespace`, `policy_type`, `rule_type`, `resource_kind`, `resource_namespace`, `resource_request_operation` | kyverno-security | Every rule evaluation, by verdict. In Audit mode a `fail` is a *recorded violation* rather than a refused request, so this grows while the blocked count stays at zero — and that gap is exactly the list of things that will start being refused when the policy moves to Enforce. `by (rule_name)` is the level at which a violation actually gets fixed. **There is no `policy` label and no `rule` label** — the board this replaces grouped on both, and a group on a label that does not exist collapses every series into one unlabelled line. The two panels that did (*Policy Violations Over Time*, *Violations by Rule*) now group on `policy_name` and `rule_name`. |
| `kyverno_admission_review_duration_seconds_bucket` | **real cumulative histogram**, label `le` | kyverno-security | The tail the API server waits on for every mutating and validating request in the cluster. `histogram_quantile()` is correct here. p50 flat with p99 climbing is a few expensive policies or a few large objects; all three percentiles rising together is the engine itself. Past the webhook timeout it reads to everyone else as a failed deploy. |

## Prometheus itself

| Series | Type & labels | Boards | What it means, and how to read it |
|---|---|---|---|
| `up` | GAUGE, 0 or 1, per target | kafka-connect | Whether Prometheus' last scrape of a target succeeded. `sum(up{…})` is the worker count on the Connect board. Scope it as tightly as the boards do: every sidecar and operator in the same namespace has its own `up`, so a loose selector counts them too. |

---

## Changing a board

The JSON is generated. Edit `board.py`, then:

```bash
scripts/gen-dashboards.py
scripts/gen-dashboards.py --check
python3 scripts/check-dashboards.py
scripts/check-metric-contract.sh kafka-cluster     # or connect-cluster, mirror-maker2
```

`ci-kafka-charts.yml`'s `Dashboards` job adds one more that has no local
script: every `targets[].expr` on every board, wrapped as a recording rule and
put through `promtool check rules`. Keep template variables inside label
values — `namespace="$namespace"` is a string literal to PromQL, and a `$` in
a duration, a range or an operand is not PromQL at all. That job fails on any
`$` it cannot resolve rather than skipping the query.

Name every series from this file or from the rule file that produces it, never
from a JMX bean name. The exporter renames as it publishes — `BytesInPerSec`
becomes `bytesin_total`, `AvgIdlePercent` becomes `avgidle_percent` — and
guessing that translation is what put eleven series that never existed on the
boards this directory replaced.
