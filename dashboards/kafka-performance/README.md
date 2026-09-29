# Kafka — Performance & Load Testing

**Delivered by** `charts/monitoring` · **uid** `kates-kafka-performance` ·
**40 panels**

## The question it answers

*What is this cluster doing right now, under this load, down to the
partition?* — and afterwards, *what was the number, and was it real?*

A throughput figure is only worth as much as the panels beside it. A cluster
shedding followers, failing produce requests or deleting records from the head
of the log as fast as the producer appends to the tail is not sustaining the
rate it appears to be sustaining. This board puts the throughput, the
saturation, the ISR dynamics and the per-partition detail on one page so a run
can be judged rather than just reported.

## Who opens it

- Whoever is running `scripts/test-perf-*.sh`, `kates` or a `kafka-producer-
  perf-test`, while it is running. The refresh is 10 s and the window is 1 h
  for that reason: this board is *watched*, not consulted.
- Whoever is sizing a cluster and wants to know which resource runs out first.
- Whoever is holding a throughput number from last week and wants to know why
  today's is different.

For the **controller quorum, metadata propagation, per-error-code failures and
the security counters**, the companion board is *Kafka — KRaft Operations*.
For **broker health in general** the Strimzi operator's `strimzi-kafka.json`
is authoritative and this board does not restate it.

## Where it came from

`charts/monitoring/dashboards/kafka-perf-global-dashboard.json`, rebuilt. Of
the nine legacy Kafka boards this refactor deleted, it was the only one with
anything worth keeping: **per-topic templating (`$topic`) and per-partition
log-end-offset**, neither of which the Strimzi operator's boards have. It also
absorbs the per-topic panels of `kafka-perf-test`, `kafka-performance` and
`kafka-working`, which were the same three or four queries rendered as gauges
instead of timeseries.

The rest of what those boards contained was dropped rather than merged. Across
the nine there were 82 panels carrying 31 distinct concepts; a panel titled
*Active Brokers* with an identical expression existed five times, *Offline
Partitions* four times, and `kafka-jvm-dashboard.json` had exactly **one**
unique panel out of four — three of its were a strict subset of
`kafka-perf-global`'s JVM row, and the fourth, *JVM Non-Heap Memory*, was the
only place in the nine that read `jvm_memory_used_bytes{area="nonheap"}`. That
series is carried into the JVM row below rather than dropped with the board.

### The seven dead names

**Seven metric names in `kafka-perf-global` did not exist and could not
exist**, and they were precisely its eight unique panels: all the throughput,
all the ISR dynamics, request-handler idle. Every panel that made the most
valuable-looking legacy board worth keeping was empty on every real cluster it
ever ran on. Two exporter rules explain all seven.

**A `…PerSec` attribute is a Yammer meter.** The COUNTER rule strips `PerSec`
and publishes the meter's `Count` with `_total` appended:

```yaml
- pattern: kafka.(\w+)<type=(.+), name=(.+)PerSec\w*><>Count
  name: kafka_$1_$2_$3_total
  type: COUNTER
```

**A `…Percent` attribute goes through the Percent rule, which inserts an
underscore** before `percent`:

```yaml
- pattern: kafka.(\w+)<type=(.+), name=(.+)Percent\w*><>MeanRate
  name: kafka_$1_$2_$3_percent
```

| The board read | The rules produce | Query |
|---|---|---|
| `kafka_server_brokertopicmetrics_bytesinpersec` | `kafka_server_brokertopicmetrics_bytesin_total` | `rate(…[5m])` |
| `kafka_server_brokertopicmetrics_bytesoutpersec` | `kafka_server_brokertopicmetrics_bytesout_total` | `rate(…[5m])` |
| `kafka_server_brokertopicmetrics_messagesinpersec` | `kafka_server_brokertopicmetrics_messagesin_total` | `rate(…[5m])` |
| `kafka_server_replicamanager_isrshrinkspersec` | `kafka_server_replicamanager_isrshrinks_total` | `rate(…[5m])` |
| `kafka_server_replicamanager_isrexpandspersec` | `kafka_server_replicamanager_isrexpands_total` | `rate(…[5m])` |
| `kafka_server_kafkarequesthandlerpool_requesthandleravgidlepercent` | `kafka_server_kafkarequesthandlerpool_requesthandleravgidle_percent` | read as is — a 0–1 gauge, **no** `rate()` |
| `kafka_network_socketserver_networkprocessoravgidlepercent` | `kafka_network_socketserver_networkprocessoravgidle_percent` | read as is — a 0–1 gauge, **no** `rate()` |

`kafka-working` contributed an eighth, from a different mistake:
`java_lang_memory_heapmemoryusage_used` is the raw MBean spelling. The
exporter agent publishes JVM memory as `jvm_memory_used_bytes` — or
`jvm_memory_bytes_used` on its 0.x line, which is why the heap panels here
write `a or b` and why `scripts/metric-contract/kafka-cluster.yaml` lists the
pairs under `alternatives`.

None of the seven would survive today: `scripts/check-metric-contract.sh
kafka-cluster` now runs the exporter rules over a bean catalogue and fails on
a name no rule can emit, and this board is registered with it.

## How it is scoped

Three dropdowns. `$namespace` and `$cluster` carry the same three matchers
every alert in `charts/kafka-cluster` carries:

```promql
namespace="$namespace", strimzi_io_cluster="$cluster", strimzi_io_name="$cluster-kafka"
```

`strimzi_io_name` matters more on this board than on any other, because this
one reads `jvm_*` and `process_*` — which Cruise Control, the Kafka Exporter
and the entity operator also publish, in the same namespace and under the same
`strimzi_io_cluster`. Without it the JVM and Resource usage sections would be
averaging a broker with a sidecar.

`$topic` is multi-select with an All option, where the original allowed one
topic; a run that writes to several is the normal case and the panels group by
topic anyway. It is populated from `label_values(kafka_log_log_size{…},
topic)`, so it lists the topics that exist on disk.

The uid is static and it is **not** `kafka-perf-global`. Re-using the old
board's uid would have a fresh install silently overwrite an operator's edits
to the old one, and would make a rollback look like it had worked while
Grafana still served the new panels. A deleted board should read as deleted.

## The trap that shapes half the expressions

`BrokerTopicMetrics` is registered **twice**: once per topic, and once with no
topic tag at all for the broker-wide aggregate. An absent label matches `""`
in PromQL, so

```promql
sum(rate(kafka_server_brokertopicmetrics_bytesin_total{namespace="kafka"}[5m]))
```

counts every byte twice and reports double the throughput the test actually
achieved. The board uses:

- `topic=""` for every cluster or per-broker total — the aggregate series.
  `kafka:bytes_in:rate5m` in the chart's recording rules uses the same.
- `topic=~"$topic", topic!=""` for the per-topic panels, where `topic!=""`
  excludes the aggregate, which would otherwise appear as an unlabelled line
  equal to the sum of all the others.

## The sections

### Header — six stats

`Brokers` · `Bytes in /s` · `Bytes out /s` · `Messages in /s` ·
`Under-replicated partitions` · `Request handler idle`

The numbers a run is reported with, plus the two that say whether to believe
them. `Under-replicated partitions` above zero means the cluster is borrowing
durability to hold the rate. `Request handler idle` is the headroom: it is the
difference between *hit the target* and *hit the ceiling*.

Bytes in ÷ messages in is the average on-the-wire record size after batching
and compression — the number that explains why two runs at the same byte rate
cost the brokers different amounts.

### Throughput — 7 panels

`Bytes in /s per broker` · `Bytes out /s per broker` ·
`Messages in /s per broker` · `Cluster throughput, in and out` ·
`Throughput by zone` · `Produce and fetch requests /s` ·
`Failed produce and fetch /s`

Under a steady producer load the per-broker lines should sit on top of each
other. A broker consistently above the others is holding more of the
leadership, which `Leader count per broker` in Replication confirms; one
consistently below is one the producers are not reaching.

Read the byte and message panels together. When they disagree — one broker
taking most of the messages while the byte rates look balanced, or the reverse
— the partitioning is the thing to look at.

`Cluster throughput` is the single line a test is reported against, and its
*shape* matters as much as its height: a flat ceiling is a limit somewhere
(Broker internals says whether it is the brokers), a sawtooth is a producer
backing off, and a slow decline over a long run is usually the log growing
past the page cache so that fetches start reaching the disk.

`Produce and fetch requests /s` divided into the message rate gives the
effective batch size, which is the biggest lever in a producer benchmark: the
same throughput at a tenth of the request rate costs roughly a tenth of the
request-handling work.

`Failed produce and fetch /s` is the honesty check on every other panel in the
section.

**`Throughput by zone` is new in this refactor.** The node pools have always
set a `zone` pod label and `kafka-common.strimziRelabelings` only carried
`strimzi_io_*`, which is why every legacy legend that said `({{zone}})`
rendered as `()`. Phase 1 added the relabeling. Egress skewed towards one zone
is consumers reading across a zone boundary, which costs real money on a
managed cloud and shows up in a bill long before it shows up in a latency
graph. Per-broker legends deliberately do **not** carry `{{zone}}`: the
relabeling uses `(.+)`, so an unzoned pod keeps no `zone` label and would
render the empty parentheses all over again. Zone appears only where the
expression groups `by (zone)`.

### Replication — 5 panels

`ISR shrinks and expands /s` · `Under-replicated partitions over time` ·
`Leader count per broker` · `Leaders by zone` · `Replication traffic`

**This is the section that finds the ceiling.** Followers replicate through
the same request handlers that serve clients, so when the brokers saturate the
followers fall behind first. Shrinks matched by expands is a cluster
recovering between bursts; shrinks without expands means the ceiling was
passed and stayed passed. A step in `Under-replicated partitions` that appears
when the producer rate increases and disappears when it drops marks the
throughput ceiling exactly.

`Leader count per broker` is the denominator for every per-broker panel above:
a throughput imbalance is only an imbalance once this is flat. A run whose
leadership moved halfway through is a run whose per-broker numbers cannot be
compared across the whole window.

`Replication traffic` is the traffic a capacity plan forgets. On a
three-replica topic it is about twice the client ingest, and it shares the
network and the handlers with client traffic — so when `Cluster throughput`
flattens, this says how much of the ceiling the cluster is spending on itself.

### Broker internals — 6 panels

`Request handler idle (windowed)` · `Request handler idle (lifetime mean)` ·
`Network processor idle` · `Request and response queue size` ·
`Request time p99 by type` · `Log flush p99`

The two idle panels sit side by side on purpose; see
[The two idle panels](#the-two-idle-panels) below.

Handler threads and network threads saturate for different reasons — handlers
on request volume and work, network threads on connection count, TLS and raw
bytes. A run that exhausts the network threads first is one to rerun with
fewer, busier client connections.

`Request and response queue size` is where a benchmark's latency percentiles
come from: a growing request queue means the broker has not *started* the
work, not that it is slow at it. It is also what distinguishes *the cluster is
at capacity* from *the client is not offering enough load*.

`Log flush p99` separates CPU-bound from disk-bound, and the two have
completely different answers — more brokers against saturated handlers, faster
volumes against slow flushes. A p99 that climbs as a run goes on with
throughput flat is the page cache filling and writeback reaching the device.

### JVM — 4 panels

`Heap and non-heap memory` · `GC time fraction` · `GC collections /s` ·
`Thread count`

`Heap and non-heap memory` plots three lines, not two. **Non-heap is here
because nothing else plots it.** `jvm_memory_used_bytes{area="nonheap"}` —
metaspace, the code cache, the compressed class space — was read by exactly
one of the nine deleted boards, `kafka-jvm-dashboard.json`'s *JVM Non-Heap
Memory*; `kafka-perf-global`'s JVM row is heap-only, and the Strimzi
operator's `strimzi-kafka.json` sums `jvm_memory_used_bytes` across areas
without breaking them out. It is flat on a healthy broker, `-Xmx` does not
bound it, and it is the line that moves when a plugin, an authorizer or an
interceptor leaks classes — which a heap graph cannot show. `Resident memory
and heap` in *Resource usage* shows the wider RSS gap that non-heap sits
inside.

`GC time fraction` is the honest form of *is GC hurting us*: seconds of GC per
second of wall clock. 0.02 is two per cent of the broker's time; anything
approaching 0.1 is a broker that will miss heartbeats and drop out of ISRs.
Read it with the collection rate — many short collections is a healthy young
generation, few long ones is the shape that stalls a broker. A collection rate
that climbs linearly with throughput means allocation per record, which for
Kafka usually means decompression and re-compression because the producer and
the topic disagree about the codec.

`Thread count` is a configuration panel rather than a load panel: it is where
`num.network.threads` and `num.io.threads` become visible, and where a leak in
a custom plugin, authorizer or interceptor shows up as a line that only
climbs.

### Resource usage — 3 panels

`CPU seconds /s` · `Resident memory and heap` · `Open file descriptors`

`process_cpu_seconds_total` is the JVM's own accounting rather than the
container runtime's, so it measures the broker and not the sidecars — which is
what a benchmark wants, and also why it will not show CFS throttling. A run
that plateaus with CPU at the pod's limit is CPU-bound; one that plateaus well
below it is bound by something else.

`Resident memory and heap` is the panel that explains OOM kills on a broker
whose heap graph looks comfortable. The gap between the two is everything the
JVM uses that is not heap — thread stacks, metaspace, code cache and above all
the direct byte buffers Kafka's network layer allocates. Neither series
includes the page cache holding the log; that is the kernel's, and it is the
memory that actually makes fetches fast.

`Open file descriptors` scales with two things a load test moves: one per log
segment file (partitions × segments) and one per client connection. Running
out does not fail cleanly — it produces log directories going offline and
connections being refused, which read on every other panel as a storage fault
and a client fault respectively.

### Topic detail (`$topic`) — 9 panels

`Records in selection` · `Bytes on disk in selection` · `Bytes in /s by topic`
· `Messages in /s by topic` · `Log end offset per partition` ·
`Partition size on disk` · `Log segments per partition` ·
`Log start offset per partition` · `Partition detail`

**The reason this board exists.** Nothing else in the repository, upstream
included, lets an operator point panels at the topic a load test is writing
to.

`Log end offset per partition` is the most useful panel during a run: the
**slope** is the per-partition write rate, and parallel lines mean the
producer's partitioner is spreading the load. One partition climbing faster
than the rest is a key skew — the classic reason a load test cannot reach its
target however many brokers are added, because one partition's leader is doing
all the work. Every replica draws its own line, so followers trailing their
leader is replication lag made visible.

The two stats count differently on purpose:

- **Records** is `sum(max by (topic, partition) (…logendoffset))`. The legacy
  board summed the raw series, which counts every record once per replica and
  reports three times the truth on a three-replica topic.
- **Bytes on disk** *is* summed over every replica, because that is the disk
  the cluster actually spends.

Divide one by the other for the on-disk size per record — batch headers and
whatever compression achieved, usually very different from the producer's
payload size.

`Log start offset per partition` is the panel that explains a consumer
benchmark suddenly reporting `OFFSET_OUT_OF_RANGE`, and, read against the end
offset, how many records a partition still holds. A partition whose size stops
growing while its offsets keep climbing is retention deleting from the head as
fast as the producer appends to the tail — at which point the test is no
longer measuring what it thinks it is.

`Partition detail` is the instant placement view that `kafka-perf-test`,
`kafka-performance` and `kafka-working` each carried their own copy of. Read
it for placement rather than volume: the row count per partition is its
replication factor, a missing row is a replica that is not there, and rows of
visibly different sizes for one partition are replicas that have not
converged.

## The two idle panels

`Request handler idle (windowed)` and `Request handler idle (lifetime mean)`
are the same meter, and only one of them is a saturation signal.

The series is
`kafka_server_kafkarequesthandlerpool_requesthandleravgidlepercent_count_total`,
and its name misleads twice. It is the Yammer meter's `Count`, which the
exporter names `_count` and then suffixes `_total` because the rule types it
COUNTER — and **the value is not a count of anything.** Kafka accumulates idle
NANOSECONDS into it. So:

```promql
avg by (kubernetes_pod_name) (rate(…_count_total[5m])) / 1e9
```

is idle nanoseconds per second, divided into a fraction of the five-minute
window. It moves when the broker does. `KafkaRequestHandlerSaturated`
computes exactly this and fires below `alerts.thresholds.handlerIdleRatio`
(0.3) for 10 minutes.

The other panel is the same meter's `MeanRate`, which the Percent rule
publishes as `…_requesthandleravgidle_percent` — the corrected spelling of one
of the seven dead names. It is a **lifetime** mean since the broker started,
so a node up for a week and saturated for an hour reads a comfortable number
and barely moves. It is on the board for one thing: when the two diverge, the
gap is how unusual the current load is compared with everything this broker
has ever served.

The other two traps that shaped this board — `_max_total` is not a counter,
and percentiles are pre-computed gauges with a `quantile` label and no `_sum`
— are documented in
[`../kafka-kraft/README.md`](../kafka-kraft/README.md#the-three-traps). The
second applies here to `Request time p99 by type` and `Log flush p99`.

## The alerts

This board has no alerts of its own — it is a diagnostic surface, not a
monitored one — but five of `charts/kafka-cluster`'s fire on series it draws,
and their default thresholds are frozen into the panels as lines.

| Alert | Default | Panel |
|---|---|---|
| `KafkaUnderReplicatedPartitions` | `> 0` for 5m | Under-replicated partitions (header + over time) |
| `KafkaRequestHandlerSaturated` | `handlerIdleRatio: 0.3` | Request handler idle (windowed) |
| `KafkaRequestQueueSaturated` | `requestQueueSize: 100` | Request and response queue size |
| `KafkaRequestLatencyHigh` | `requestLatencyP99Ms: 1000` | Request time p99 by type |
| `KafkaLogFlushLatencyHigh` | `logFlushP99Ms: 500` | Log flush p99 |

A Grafana threshold is not templatable, so a release that moves
`alerts.thresholds.*` gets the alert it asked for and a line that has not
moved with it.

## Changing it

The JSON is generated. Edit `board.py`, then:

```bash
scripts/gen-dashboards.py
scripts/gen-dashboards.py --check
python3 scripts/check-dashboards.py
scripts/check-metric-contract.sh kafka-cluster      # the one that matters
```

The contract is registered for this board under `external_dashboards.paths` in
`scripts/metric-contract/kafka-cluster.yaml`, because `charts/monitoring`
delivers it and nothing else in the build would check it against the exporter
rules that have to produce what it reads. That is the gate that would have
caught all seven dead names, and it is why the table at the top of this file
can be written with confidence rather than from memory. Derive every name by
running it — **never from a JMX bean name.**
