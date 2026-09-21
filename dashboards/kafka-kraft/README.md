# Kafka — KRaft Operations

**Delivered by** `charts/monitoring` · **uid** `kates-kafka-kraft` · **61 panels**

## The question it answers

Kafka 4.x is KRaft-only. The cluster's metadata — which topics exist, which
partitions exist, who leads each one, which brokers are alive, what the ACLs
say — lives in a replicated log managed by a *controller quorum*, and every
broker is a reader of that log. Almost nothing about that is visible on a
ZooKeeper-era dashboard, and until this board existed **not one of the
thirteen boards this repository shipped read a single `raft` or
`brokermetadata` series.**

This is the board for *is the quorum healthy, is metadata committing, has
every broker applied it, and is anything failing to?* — and, below that, the
replication, request-path and storage detail that the Strimzi operator's own
boards leave out.

It is explicitly **not** a rebuild of `strimzi-kafka.json`. See
[What upstream already covers](#what-upstream-already-covers).

## Who opens it

- Whoever is paged by `KafkaRaftLeaderElections`, `KafkaRaftUnknownVoters`,
  `KafkaBrokerMetadataLag` or `KafkaFencedBrokers` — four alerts that, before
  this board, fired into a dashboard showing **none of their inputs**. Each
  now has a panel, and the panel carries the alert's own threshold as a line.
- Whoever is looking at a cluster where everything is green and something is
  wrong anyway: a topic that was created and is not visible, an ACL change
  that has not taken effect, a broker that is up and serving nothing.
- Whoever is holding a page from any of the thirteen other `kafka-cluster`
  alerts that have an input here (the table at the bottom maps all seventeen).

For a **load test**, the companion board is *Kafka — Performance & Load
Testing*, which has the per-topic selector and the per-partition detail.

## If you have only ever run ZooKeeper Kafka

Four ideas carry most of the board. None of them has a ZooKeeper analogue.

### The quorum

In the ZooKeeper world, one broker was elected controller and ZooKeeper — a
separate cluster, with its own quorum, its own disks and its own dashboards —
held the metadata. In KRaft, a set of Kafka nodes *are* the quorum. They are
called **voters**, they run the Raft protocol among themselves, and one of
them is the **leader** (the *active controller*). The other voters are
**followers**. Every broker also runs a Raft client, but as an **observer**:
it replicates the metadata log to read it and never votes.

A quorum needs a majority to commit anything. Three voters tolerate one
failure; five tolerate two. Lose the majority and the cluster does not stop —
that is the surprising part — it *freezes*: existing leaders keep serving
produce and fetch, and nothing new can happen. No topic is created, no leader
is re-elected, no broker is fenced or unfenced. Clients see a perfectly
healthy cluster until they need one of those things.

**On this board:** `Quorum roles` names the voters and their states. `Active
controllers` must be exactly 1. `Unreachable voters` is the margin left before
the majority is gone. `Uncommitted metadata records` is the frozen state
happening, visible as a gap that grows and does not come back.

### Metadata lag

Because brokers *read* the metadata log rather than being told about changes,
a broker can fall behind it. **This is the KRaft failure mode with no
ZooKeeper equivalent, and it is silent.** A lagging broker is up. It answers
its probes, publishes its metrics, leads its partitions and serves its
clients. It is simply operating on an old view of the cluster: advertising
partition leadership that has moved, enforcing the ACLs it last read, unaware
of the topic created a minute ago.

`KafkaBrokerMetadataLag` fires above 60 s. The `Broker metadata lag` panel is
the alert's input, and `Metadata records behind the leader` is the same gap
measured in records rather than milliseconds — which matters because the
millisecond form is computed from a timestamp inside the record, so a clock
problem makes it meaningless while the record gap stays honest.

### Fenced is not down

The controller tracks broker liveness by heartbeat. A broker whose heartbeat
lapses, or that has restarted and not yet caught up on the metadata log, is
**fenced**: still registered, still running, and stripped of every partition
leadership.

A fenced broker passes every Kubernetes check there is. The pod is Running,
both probes pass, the container has not restarted, and the metrics endpoint
answers. It just serves nothing. `KafkaFencedBrokers` fires above zero for 5
minutes, and `Registered and fenced brokers` is where the two numbers are
drawn together, because the interesting signal is fenced rising while
registered stays flat — a node present and excluded.

### Metadata errors

A record the broker could not read (`load`), a record it read and could not
apply (`apply`), or one the controller itself rejected. **Nothing in this
chart alerts on any of them**, and an `apply` error in particular means a node
is silently diverged from the cluster and will stay diverged until it restarts
and replays a snapshot. The `Metadata errors by kind` panel is the only place
they appear.

## What upstream already covers

`charts/strimzi-operator` ships nine upstream boards and enables them by
default; their label selectors match what this repository's PodMonitors emit.
Two of them matter here:

| Board | Covers | Read it for |
|---|---|---|
| `strimzi-kafka.json` | broker health: an 8-stat header, `irate()`d `_total` counters, `_percent` idle gauges, per-listener connections, disk I/O, log size, the full JVM / container / volume set | Anything about a broker as a machine |
| `strimzi-kraft.json` | quorum **identity**: state, current leader, current vote, epoch, high watermark, log end offset, append and fetch rates, commit latency **average** | Who is the leader and what is the quorum doing |

`strimzi-kafka.json` covers broker health **strictly better than all nine
legacy boards combined**, which is why eight of them were deleted rather than
merged.

This board covers the other half — the degradation signals neither upstream
board has. Of the 42 producible raft / controller / metadata series,
`strimzi-kraft.json` shows 13; it has no metadata-lag, no error counters and
no quorum-degradation signal at all. `Quorum roles` is the single identity
panel kept here, purely as the frame for reading the rest.

## How it is scoped

Two dropdowns, `$namespace` and `$cluster`, and **every expression carries the
same three matchers the alerts carry**:

```promql
namespace="$namespace", strimzi_io_cluster="$cluster", strimzi_io_name="$cluster-kafka"
```

That is byte-identical to `$k` in
`charts/kafka-cluster/templates/prometheusrule.yaml`, deliberately: a page and
a panel select the same series, so there is no translation step in the middle
of an incident. All three labels come from
`kafka-common.strimziRelabelings`, which the chart's PodMonitors apply.

`strimzi_io_name` is the one that is easy to think optional. Cruise Control,
the Kafka Exporter and the entity operator run in the same namespace under the
same `strimzi_io_cluster` and publish their own `up` and `jvm_*`; without it,
`sum(up{...})` counts them.

Unlike the MirrorMaker 2 boards these are real **query** variables with a
dropdown, not hidden `constant`s the chart injects. They can be, because
`strimzi_io_cluster` is on every series: one static file serves every Kafka
cluster in one Grafana, and `scripts/gen-dashboards.py` copies it into
`charts/monitoring/dashboards/` unmodified.

The uid is static (`kates-kafka-kraft`) for the same reason. Connect and
MirrorMaker 2 boards belong to a *release* and their charts rewrite the uid
with a digest of the release name to stop two releases overwriting each
other's board. This one belongs to a *cluster*, there is one monitoring
release, and the cluster is chosen in the dropdown.

## The sections

### Header — six stats

`Active controllers` · `Quorum epoch` · `Unreachable voters` ·
`Metadata lag (worst node)` · `Fenced brokers` · `Metadata errors`

The triage row, and the four alerts with no board are three of the six.
`Quorum epoch` is the odd one: the number means nothing, its *rate of change*
means everything. A flat epoch is a stable quorum.

### Quorum and metadata — 11 panels

`Quorum roles` · `Leader elections in 15 minutes` · `Unreachable voters per node` ·
`Uncommitted metadata records` · `Metadata commit latency` ·
`Election latency` · `Broker metadata lag` ·
`Metadata records behind the leader` · `Metadata errors by kind` ·
`Raft poll idle ratio` · `Quorum channel requests and responses /s`

Reads left to right as *is the quorum electing, is it committing, is it
propagating*. Three pairs are meant to be read together:

- **Elections and election latency.** Three fast elections cost less than one
  slow one. Metadata writes are frozen for the duration of each.
- **Commit latency and raft poll idle ratio.** Slow commits with an idle raft
  thread is the disk under the metadata log. Slow commits with the raft thread
  at zero is the thread itself, usually a controller co-located with a loaded
  broker — a completely different fix.
- **Metadata lag in milliseconds and in records.** When they disagree, believe
  the records.

`Quorum channel requests and responses /s` is where a slow inter-AZ link
shows up *first* — before commit latency moves, because a commit only needs a
majority and one slow peer can be the one left out. Read it as a pair per
node: requests pulling ahead of responses is a peer that stopped answering.
It replaced a panel that read the channel's `request_latency_avg` and
`request_latency_max_total`, which the raft channel never publishes (they are
producer and consumer client sensors), so it drew nothing on any cluster.

### Cluster health — 10 panels

`Which node is the controller` · `Registered and fenced brokers` ·
`Broker state` · `Offline partitions` ·
`Partitions below min.insync.replicas` · `Unclean leader elections (10m)` ·
`Preferred-replica imbalance` · `Topics and partitions` ·
`Controller event queue time (p99)` · `Controller events /s`

`Offline partitions` is the most severe number on the board, and it has a
trap: **only the active controller publishes it**, so the series vanishes when
there is no controller. An empty panel is not a zero, and `Which node is the
controller` says which one you are looking at.

`Broker state` distinguishes a planned restart from an unplanned one — a
broker that passed through 6 (PENDING_CONTROLLED_SHUTDOWN) and 7 was asked to
leave; one that reappears at 1 or 2 without ever showing 6 was killed. A
broker parked at 2 (RECOVERY) is rebuilding its log and will serve nothing
until it finishes.

`Topics and partitions` is scale rather than health, and it is the only
content kept from `kafka-working` (which rendered the same two numbers as
gauges and nothing else). It is the denominator for most of this board:
twelve under-replicated partitions out of forty is an incident, and out of
forty thousand it is a rolling restart.

The two controller panels are saturation: high event rate with low queue time
is a busy healthy controller; low rate with high queue time is a controller
blocked, and it is why a cluster can be slow to create a topic while every
broker-side panel is calm.

### Replication — 10 panels

`ISR shrinks and expands /s` · `Failed ISR updates /s` ·
`Under-replicated partitions` · `Partitions at min.insync.replicas` ·
`Offline replicas` · `Replication traffic` ·
`Partition leadership by zone` · `Replica placement by zone` ·
`Partitions below min ISR, by topic` · `Replicas out of sync, by partition`

An escalation, in order of how much it costs you:

| Panel | What has happened | What clients see |
|---|---|---|
| Under-replicated | a follower is behind | nothing |
| At min ISR | one more failure stops writes | nothing |
| Below min ISR | too few in sync | `acks=all` produce fails |
| Offline partitions | no leader at all | everything fails |

`At min ISR` is the only one with lead time and has no alert on purpose — it
is a normal steady state through a rolling restart, and alerting on it would
page for every upgrade.

`Failed ISR updates` is subtler than it looks. In KRaft an ISR change is an
`AlterPartition` request from the leader to the controller, so failures are
the leader and the controller disagreeing about the partition epoch — which
pairs directly with `Broker metadata lag`: a broker that cannot apply metadata
cannot get its ISR changes accepted either.

The two tables name what the timeseries count. `Replicas out of sync, by
partition` gives, per partition, replica count minus in-sync count: a
partition showing a deficit of RF−1 has only its leader left.

**The two zone panels are new in this refactor.** The node pools have always
set a `zone` pod label; `kafka-common.strimziRelabelings` only carried
`strimzi_io_*`, so every legacy legend that said `({{zone}})` rendered as
`()`. Phase 1 added the relabeling. `Partition leadership by zone` says where
the work is (a zone carrying most of it means most client traffic crosses a
zone boundary — a cost problem before a latency problem); `Replica placement
by zone` says where the data is, and whether losing an AZ would lose a
partition. The regex is `(.+)`, so a pod with no `zone` label keeps none and
draws one unlabelled line rather than joining an empty-string bucket.

### Request path — 10 panels

`Request handler idle (windowed)` · `Network processor idle` ·
`Request and response queue depth` · `Request time p99 by type` ·
`Request queue time p99 by type` · `Requests /s by type` ·
`Produce and fetch /s by API version` · `Errors /s by code` ·
`Server-side error ratio (SLI)` · `Bytes rejected /s`

`Request handler idle (windowed)` is the best saturation signal a broker has,
and the expression behind it is the second of the three traps below — read it
before trusting the panel.

`Request time p99` and `Request queue time p99` split a latency problem in
two: queue time near the total means the broker is saturated and the fix is
capacity; queue time near zero with a high total means the work itself is
slow, and `Log flush p99` in Storage is the next place to look.

`Server-side error ratio (SLI)` writes out the same expression
`charts/kafka-cluster` records as `kafka:request_errors:ratio_rate5m` rather
than reading the recorded series, so the panel works whether or not
`alerts.slo.enabled` installed the rules. Its line is the fast-burn budget,
0.0144.

`Produce and fetch /s by API version` uses a label nothing else in this
repository looks at, and answers a question no other panel can: **which
clients are old.** A version disappearing after an upgrade is how you confirm
a fleet actually migrated.

### Storage — 6 panels

`Log size per broker` · `Log flush p99` · `Log flushes /s` ·
`Offline log directories` · `Uncleanable partitions` ·
`Largest topics on disk`

`Log flush p99` is upstream of almost everything else on the board: slow
flushes make produce slow, which makes handlers busy, which makes queues grow,
which makes followers fall out of the ISR. When several sections are unhappy
at once, this is the panel that says whether the disk is the cause.
`Log flushes /s` is its denominator — a high p99 over three flushes is one
slow fsync.

`Offline log directories` has the shortest `for:` of any alert in the chart
(1 minute) because there is no benign cause; with JBOD the broker stays up and
serves everything else, so nothing at the pod level changes.

`Uncleanable partitions` is quiet in the worst way. It only affects compacted
topics: compaction stops, the partition grows without bound, and consumers
that rely on one record per key keep reading old ones. `__consumer_offsets` is
compacted, so this can end in a cluster that cannot store offsets.

### Tiered storage (collapsed) — 4 panels

`Remote copy and fetch throughput` · `Remote copy and fetch errors /s` ·
`Remote copy lag` · `Remote log size`

Collapsed because the six `RemoteLog` beans only exist when
`tieredStorage.enabled` put `remote.log.storage.system.enable=true` on the
brokers. A collapsed row costs nothing on a cluster without the remote tier,
which is the honest alternative to four permanently empty panels. They are
`optional` in the metric contract's catalogue and under `live.absentOk`, so
the live-scrape job does not expect them.

The consequence chain worth knowing: a broker that cannot offload keeps
segments on **local** disk, so a cluster sized for its remote tier starts
filling volumes that were never meant to hold that much. `Remote copy lag` is
the leading indicator; the error panel is the trailing one.

### Security and connections — 4 panels

`Failed authentications /s by listener` ·
`Successful authentications /s by listener` ·
`New connections /s by listener` · `Open connections by node and listener`

Nothing in this repository alerts on authentication failures, and this is the
only place an expired client certificate, a rotated SCRAM password or a
misconfigured OAuth issuer is visible from the cluster side. A step at a
deployment is a credential change; a slow climb across all listeners is
certificates issued on the same day reaching expiry together.

The successful rate is the denominator — five failures a second against fifty
successes is a broken client, against five successes it is every client — and
it is a churn signal in its own right, because a healthy fleet holds long-lived
connections and authenticates rarely. `New connections /s` confirms it. Churn
costs the network threads far more than traffic does, which is why this panel
and `Network processor idle` are read together.

`Open connections by node and listener` also exists on `strimzi-kafka.json`.
It is repeated here because it is the denominator that makes the other three
readable, and an operator chasing an auth problem should not have to change
board to find it.

## The three traps

Each of these produced a dead or misleading panel somewhere in this
repository. They are repeated in the panel descriptions because that is where
they will be read.

### 1. `_max_total` is not a counter

The JMX exporter's 1.x line reserves `_total` for counters: a COUNTER is
always exposed with it, and anything else has it stripped. The vendored KRaft
rules type `.+-total|.+-max` as COUNTER — the `-max` half is swept up by the
same pattern to keep the genuinely monotonic `-total` attributes correctly
typed.

```yaml
- pattern: "kafka.server<type=raft-metrics><>(.+-total|.+-max):"
  name: kafka_server_raftmetrics_$1
  type: COUNTER
```

So these two are **max gauges in milliseconds wearing a counter's name**:

- `kafka_server_raftmetrics_commit_latency_max_total`
- `kafka_server_raftmetrics_election_latency_max_total`

`rate()` over either yields a number that means nothing. The `_avg`
companions are ordinary gauges and are read as they are.

### 2. `_count_total` is the meter's `Count`, and the unit is not always a count

The exporter names a Yammer meter's or timer's `Count` attribute `…_count`
and then appends `_total` because the rule types it COUNTER. Sometimes that is
a count of events:

- `kafka_controller_controllereventmanager_eventqueuetimems_count_total` —
  controller events. `rate()` is a rate of events.
- `kafka_log_logflushstats_logflushrateandtimems_count_total` — flushes.
  Likewise.

And sometimes it is not:

- `kafka_server_kafkarequesthandlerpool_requesthandleravgidlepercent_count_total`
  is **cumulative nanoseconds of idle time**.

Which is why the idle ratio is

```promql
avg by (kubernetes_pod_name) (rate(…_count_total[5m])) / 1e9
```

— idle nanoseconds per second, divided into a fraction of the window.
`KafkaRequestHandlerSaturated` computes exactly this.

The obvious-looking alternative,
`kafka_server_kafkarequesthandlerpool_requesthandleravgidle_percent`, is the
same meter's `MeanRate`: a **lifetime** mean since the broker started. It
flattens — a broker up for a week and saturated for an hour still reads a
comfortable number. It is the corrected name (the legacy boards' `…avgidlepercent`,
with no underscore, is one of the seven names no rule produces) and it is
still the wrong panel for judging saturation. It is on the *Performance*
board, beside the windowed one, where the contrast is the point.

### 3. Percentiles are pre-computed gauges with a `quantile` label and no `_sum`

Kafka's histograms and timers publish their percentiles as attributes. The
exporter emits them as gauges carrying `quantile="0.99"` and **there is no
`_sum` and no bucket series**, so nothing on this board can be — or needs to
be — computed with `histogram_quantile`. Select the quantile:

```promql
kafka_network_requestmetrics_totaltimems{…, quantile="0.99"}
kafka_network_requestmetrics_requestqueuetimems{…, quantile="0.99"}
kafka_controller_controllereventmanager_eventqueuetimems{…, quantile="0.99"}
kafka_log_logflushstats_logflushrateandtimems{…, quantile="0.99"}
```

The upstream rule comment says it plainly: *"Emulate Prometheus 'Summary'
metrics for the exported 'Histogram's. Note that these are missing the '_sum'
metric!"*

### A fourth, local to this cluster: `BrokerTopicMetrics` is registered twice

Once per topic, and once with no topic tag for the broker aggregate. An absent
label matches `""` in PromQL, so summing without a `topic` matcher counts
every byte twice. This board only reads that family in `Bytes rejected /s`
(broker-level only, so no ambiguity); the *Performance* board leans on it
heavily and uses `topic=""` for totals and `topic!=""` for per-topic.

## The alerts

Every alert in `charts/kafka-cluster/templates/prometheusrule.yaml` that has
an input on this board, with the panel that shows it. Thresholds are the
chart's defaults, frozen into the panels because a Grafana threshold is not
templatable — a release that moves `alerts.thresholds.*` gets the alert it
asked for and a line that has not moved with it.

| Alert | Default | Panel |
|---|---|---|
| `KafkaRaftLeaderElections` | `raftElectionsPer15m: 3` | Leader elections in 15 minutes |
| `KafkaRaftUnknownVoters` | `> 0` for 10m | Unreachable voters (header + panel) |
| `KafkaBrokerMetadataLag` | `metadataLagMs: 60000` | Broker metadata lag (header + panel) |
| `KafkaFencedBrokers` | `> 0` for 5m | Fenced brokers (header), Registered and fenced brokers |
| `KafkaActiveControllerCount` | `!= 1` for 3m | Active controllers (header), Which node is the controller |
| `KafkaOfflinePartitions` | `> 0` for 2m | Offline partitions |
| `KafkaUnderMinIsrPartitions` | `> 0` for 2m | Partitions below min.insync.replicas |
| `KafkaUncleanLeaderElection` | any, no `for:` | Unclean leader elections (10m) |
| `KafkaUnderReplicatedPartitions` | `> 0` for 5m | Under-replicated partitions |
| `KafkaISRShrinkRate` | `> 0` for 10m | ISR shrinks and expands /s |
| `KafkaOfflineLogDirectory` | `> 0` for 1m | Offline log directories |
| `KafkaRequestLatencyHigh` | `requestLatencyP99Ms: 1000` | Request time p99 by type |
| `KafkaRequestHandlerSaturated` | `handlerIdleRatio: 0.3` | Request handler idle (windowed) |
| `KafkaRequestQueueSaturated` | `requestQueueSize: 100` | Request and response queue depth |
| `KafkaLogFlushLatencyHigh` | `logFlushP99Ms: 500` | Log flush p99 |
| `KafkaTieredStorageCopyErrors` | `> 0` for 10m | Remote copy and fetch errors /s |
| `KafkaAvailabilitySLOBurning` | budget `0.0144` | Server-side error ratio (SLI) |

The four with no input before this board are the first four rows.

Not on this board, and deliberately: `KafkaNodesMissing` and
`KafkaBrokerDiskUsageHigh` / `Critical` read `up` and the kubelet's volume
statistics, which are cluster-infrastructure signals rather than Kafka ones;
`KafkaConsumerGroupLag` reads the Kafka Exporter's series, which
`strimzi-kafka-exporter.json` covers; `CruiseControlAnomalyDetected` and
`CruiseControlNoLoadModel` are `strimzi-cruise-control.json`'s.

## Changing it

The JSON is generated. Edit `board.py`, then:

```bash
scripts/gen-dashboards.py
scripts/gen-dashboards.py --check
python3 scripts/check-dashboards.py
scripts/check-metric-contract.sh kafka-cluster      # the one that matters
```

The metric contract is the gate that makes this board trustworthy. It runs the
vendored exporter rules
(`charts/kafka-cluster/files/metrics/kafka-metrics.yaml`) over the MBean
catalogue in `scripts/metric-contract/kafka-cluster.yaml` and fails on any
name those rules cannot produce; this board is registered there under
`external_dashboards.paths`, because `charts/monitoring` delivers it and
nothing else in the build would ever check it. Derive names by running that
machinery, **never from a JMX bean name** — the exporter renames as it
publishes, and guessing from bean names is exactly what produced the eleven
dead names this refactor exists to clear out.

Every panel needs a description saying what it shows, what it means when it
moves, and where the number comes from. `panels.py` will not build one
without.
