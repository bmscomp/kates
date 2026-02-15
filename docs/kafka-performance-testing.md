# Kafka Performance Testing on Strimzi

A hands-on guide to measuring, tuning, and validating Kafka performance on the krafter cluster. Everything here is specific to this project's topology: 3 KRaft brokers running on Strimzi 0.49.1, deployed across three availability zones in a Kind cluster.

---

## Chapter 1: Understanding the Cluster

Before you can measure performance, you need to understand what you're measuring. The krafter cluster is not a generic Kafka installation — its topology, resource limits, and storage architecture all constrain and shape performance characteristics.

### Physical Topology

```
                        Kind Cluster "panda"
                        ┌───────────────────────────────────────────────┐
                        │                                               │
  ┌───────────────────┐ │ ┌───────────────────┐ ┌───────────────────┐   │
  │   Node: alpha     │ │ │   Node: sigma     │ │   Node: gamma     │   │
  │   (control-plane) │ │ │   (worker)        │ │   (worker)        │   │
  │                   │ │ │                   │ │                   │   │
  │ pool-alpha-0      │ │ │ pool-sigma-0      │ │ pool-gamma-0      │   │
  │ ├─ Broker + Ctrl  │ │ │ ├─ Broker + Ctrl  │ │ ├─ Broker + Ctrl  │   │
  │ ├─ 2Gi memory     │ │ │ ├─ 2Gi memory     │ │ ├─ 2Gi memory     │   │
  │ └─ 10Gi PVC       │ │ │ └─ 10Gi PVC       │ │ └─ 10Gi PVC       │   │
  │    (local-alpha)  │ │ │    (local-sigma)  │ │    (local-gamma)  │   │
  └───────────────────┘ │ └───────────────────┘ └───────────────────┘   │
                        └───────────────────────────────────────────────┘
```

Each broker runs as a combined controller+broker in KRaft mode. There is no ZooKeeper. The three brokers form both the metadata quorum (controller role) and the data plane (broker role).

### Resource Budget

| Resource | Per Broker | Total Cluster |
|----------|-----------|---------------|
| Memory | 2Gi (request = limit) | 6Gi |
| Storage | 10Gi persistent | 30Gi |
| CPU | Not explicitly limited | Shared with Kind node |
| Network | Kind internal (loopback-speed) | No real network latency |

The memory limit of 2Gi is hard — the JVM, page cache, and Kafka metadata must all fit within this. This is a tight budget that makes performance tuning more important than on a production cluster with 64Gi per broker.

### Replication Configuration

| Parameter | Value | Performance Impact |
|-----------|-------|--------------------|
| `default.replication.factor` | 3 | Every write is replicated to all brokers |
| `min.insync.replicas` | 2 | Writes with `acks=all` block until 2 replicas confirm |
| `offsets.topic.replication.factor` | 3 | Consumer group coordination is fully replicated |
| `transaction.state.log.replication.factor` | 3 | Exactly-once has full replication overhead |
| `transaction.state.log.min.isr` | 2 | Transaction commits need 2 replicas |

With RF=3 and ISR=2, every produce request with `acks=all` requires the leader to wait for at least one follower to replicate the data before acknowledging. This is the single largest factor in producer latency on this cluster.

### Listeners

| Name | Port | TLS | Use |
|------|------|-----|-----|
| `plain` | 9092 | No | Internal cluster communication, perf tests |
| `tls` | 9093 | Yes | Encrypted traffic (adds CPU overhead) |

Performance tests should use port 9092 (plain) for baseline measurements. TLS adds measurable CPU overhead — test both to quantify the cost of encryption on this 2Gi-per-broker cluster.

### Monitoring Stack

Prometheus scrapes JMX metrics from each broker every 15 seconds via a JMX Prometheus exporter sidecar. The exporter rules are defined in `config/kafka-metrics.yaml` and cover:

- `kafka.server.*` — Broker-level metrics (request rates, bytes in/out, ISR stats)
- `kafka.network.*` — Network handler threads, request queue depth
- `kafka.log.*` — Log segment sizes, per-topic/partition stats
- `kafka.controller.*` — KRaft controller metrics (active controller, leader elections)
- `kafka.coordinator.*` — Group coordinator stats (consumer group joins, commits)
- `java.lang.*` — JVM heap, GC, thread counts

Grafana (http://localhost:30080, admin/admin) has 7 pre-provisioned dashboards:

| Dashboard | What It Shows |
|-----------|---------------|
| Kafka Cluster Health | Broker count, offline partitions, zone distribution |
| Kafka Performance Metrics | Topic throughput, partition growth |
| Kafka JVM Metrics | Heap usage, GC pressure, thread counts per zone |
| Kafka Performance Test Results | Results from perf-test Jobs |
| Kafka Working Dashboard | Combined operational view |
| Kafka Comprehensive Dashboard | Deep-dive across all metric categories |
| Kafka All Metrics Dashboard | Raw JMX metric explorer |

---

## Chapter 2: How Kafka Performance Works

Performance in Kafka is not one number. It is the interaction of the producer write path, the replication protocol, the consumer read path, and the underlying OS page cache. Understanding each piece lets you identify where your bottleneck actually is.

### The Producer Write Path

```
Client Application
    │
    ▼
┌──────────────────────────────────────────────────────────────┐
│                       KafkaProducer                          │
│                                                              │
│  1. Serialize key + value                                    │
│  2. Partition assignment (hash(key) % numPartitions)         │
│  3. Append to RecordAccumulator batch buffer                 │
│     └─ batch.size (default 16KB) controls batch size         │
│     └─ linger.ms (default 0ms) controls wait time            │
│  4. Compress batch (if compression.type != none)             │
│  5. Sender thread picks up full/expired batches              │
│  6. Send ProduceRequest to partition leader                  │
│                                                              │
│  max.in.flight.requests.per.connection = how many batches    │
│  can be in-flight simultaneously (default 5)                 │
└──────────────────────────────────────────────────────────────┘
    │
    ▼ ProduceRequest
┌──────────────────────────────────────────────────────────────┐
│                     Broker (Leader)                          │
│                                                              │
│  1. Validate request (auth, schema, quota)                   │
│  2. Append to leader log (local disk write via page cache)   │
│  3. If acks=all: wait for followers to replicate             │
│     └─ Followers fetch from leader's log                     │
│     └─ Wait until ISR count >= min.insync.replicas           │
│  4. Return ProduceResponse with offset                       │
└──────────────────────────────────────────────────────────────┘
```

**Where time is spent:**
- Serialization: microseconds (negligible unless using schema registry)
- Batching wait: 0ms to `linger.ms` (controlled by you)
- Compression: 0.1–2ms per batch depending on codec and batch size
- Network round-trip: sub-millisecond in Kind (real: 0.1–2ms cross-AZ)
- Disk write: sub-millisecond (page cache absorbs it)
- Replication wait (`acks=all`): the dominant cost — depends on follower fetch latency

### The Replication Protocol

```
Leader receives ProduceRequest
    │
    ├─ Writes to local log → increments LEO (Log End Offset)
    │
    ├─ Followers poll leader (replica.fetch.min.bytes, replica.fetch.wait.max.ms)
    │   └─ Follower writes to local log → increments follower LEO
    │   └─ Follower sends FetchResponse with its LEO back to leader
    │
    ├─ Leader tracks each follower's LEO
    │   └─ When follower LEO >= leader LEO: follower is "in-sync"
    │   └─ High Watermark = min(LEO) across all ISR members
    │
    └─ ProduceResponse sent when HW advances past the produced offset
       (which requires min.insync.replicas followers to catch up)
```

**Key insight:** The replication fetch interval (`replica.fetch.wait.max.ms`, default 500ms) is an upper bound. Followers normally fetch as fast as they can. But if a follower is slow (CPU-starved, network-lagged, disk-slow), it delays the high watermark advance, which delays the producer acknowledgment.

### The Consumer Read Path

```
┌──────────────────────────────────────────────────────────────┐
│                      KafkaConsumer                           │
│                                                              │
│  1. poll(Duration) → triggers FetchRequest to broker         │
│     └─ fetch.min.bytes: minimum data before broker responds  │
│     └─ fetch.max.wait.ms: max time broker waits to fill      │
│     └─ max.partition.fetch.bytes: max data per partition     │
│  2. Broker reads from log (usually served from page cache)   │
│  3. Deserialize records                                      │
│  4. Application processes records                            │
│  5. Commit offsets (auto or manual)                          │
│     └─ auto.commit.interval.ms (default 5000ms)              │
│  6. Next poll()                                              │
│                                                              │
│  max.poll.records: max records returned per poll (default    │
│  500). Controls processing batch size.                       │
│  max.poll.interval.ms: max time between polls before the     │
│  consumer is considered dead (default 300s).                 │
└──────────────────────────────────────────────────────────────┘
```

**Consumer throughput** is bounded by:
1. **Partition count** — a single consumer can only process partitions assigned to it
2. **Processing time** — if your processing per record is slow, you need more partitions and consumers
3. **Fetch efficiency** — small fetches waste network round-trips; large fetches reduce consumer lag faster

### KRaft vs ZooKeeper: Performance Differences

Since krafter runs in KRaft mode (no ZooKeeper), there are specific performance characteristics:

| Operation | ZooKeeper | KRaft | Impact |
|-----------|-----------|-------|--------|
| Leader election | ZK session timeout (6–18s) | Raft election timeout (~1–3s) | Faster failover |
| Metadata propagation | Async ZK watches | Raft log replication | More predictable |
| Controller failover | ZK ephemeral nodes | Raft leader election | No external dependency |
| Partition limit | ~200K practically | Higher (metadata in Raft log) | Better scaling |

KRaft eliminates the ZooKeeper round-trip for metadata operations, which means partition creation, leader election, and configuration changes are faster. This matters during chaos experiments and rolling restarts.

---

## Chapter 3: Key Metrics and Where to Find Them

Performance testing without metrics is just running commands and hoping. This chapter maps every important metric to where you can observe it in the klster monitoring stack.

### Broker Metrics

These come from the JMX exporter sidecar, scraped by Prometheus and visible in Grafana.

#### Throughput

| Metric | PromQL | Dashboard | What It Means |
|--------|--------|-----------|---------------|
| Bytes In/sec | `kafka_server_brokertopicmetrics_bytesinpersec` | Performance Metrics | Total data ingestion rate across all topics |
| Bytes Out/sec | `kafka_server_brokertopicmetrics_bytesoutpersec` | Performance Metrics | Total data consumption rate |
| Messages In/sec | `kafka_server_brokertopicmetrics_messagesinpersec` | Performance Metrics | Record ingestion rate |
| Produce Request Rate | `kafka_server_brokertopicmetrics_totalproducerequestspersec` | Cluster Health | How many produce requests the broker handles |
| Fetch Request Rate | `kafka_server_brokertopicmetrics_totalfetchrequestspersec` | Cluster Health | How many fetch requests (consumer + replication) |

#### Latency

| Metric | PromQL | What It Means |
|--------|--------|---------------|
| Produce Total Time | `kafka_server_brokertopicmetrics_producetotaltimems` | End-to-end time for a produce request |
| Fetch Total Time | `kafka_server_brokertopicmetrics_fetchtotaltimems` | End-to-end time for a fetch request |
| Request Queue Time | `kafka_network_requestmetrics_requestqueuetimems` | Time waiting in the request queue (sign of thread starvation) |
| Response Send Time | `kafka_network_requestmetrics_responsesendtimems` | Time to write the response to the socket |

#### Replication Health

| Metric | PromQL | Critical Threshold |
|--------|--------|--------------------|
| Under-replicated Partitions | `kafka_server_replicamanager_underreplicatedpartitions` | > 0 means data risk |
| Offline Partitions | `kafka_controller_kafkacontroller_offlinepartitionscount` | > 0 means data unavailable |
| ISR Shrinks/sec | `kafka_server_replicamanager_isrshrinkspersec` | Sustained > 0 means replication trouble |
| ISR Expands/sec | `kafka_server_replicamanager_isrexpandspersec` | Should follow shrinks during recovery |
| Active Controller Count | `kafka_controller_kafkacontroller_activecontrollercount` | Must be exactly 1 |

#### Request Handler Threads

| Metric | PromQL | What It Means |
|--------|--------|---------------|
| Request Handler Idle % | `kafka_server_kafkarequesthandlerpool_requesthandleravgidlepercent` | Below 0.3 = thread starvation |
| Network Processor Idle % | `kafka_network_socketserver_networkprocessoravgidlepercent` | Below 0.3 = network bottleneck |

### JVM Metrics

Available in the "Kafka JVM Metrics" Grafana dashboard, sourced from `java.lang.*` JMX beans.

| Metric | PromQL | Warning Signal |
|--------|--------|---------------|
| Heap Used | `java_lang_memory_heapmemoryusage_used` | Approaching 2Gi limit |
| GC Pause Time | `java_lang_garbagecollector_collectiontime` | Sustained high = GC pressure |
| GC Count | `java_lang_garbagecollector_collectioncount` | Rapid increase = memory pressure |
| Thread Count | `java_lang_threading_threadcount` | Unusual growth = thread leak |

### Useful PromQL Queries

**Total cluster throughput (MB/sec):**
```promql
sum(rate(kafka_server_brokertopicmetrics_bytesinpersec[5m])) / 1024 / 1024
```

**Per-broker message rate:**
```promql
rate(kafka_server_brokertopicmetrics_messagesinpersec[5m])
```

**Replication lag detection:**
```promql
kafka_server_replicamanager_underreplicatedpartitions > 0
```

**Request handler saturation (alert if below 30% idle):**
```promql
kafka_server_kafkarequesthandlerpool_requesthandleravgidlepercent < 0.3
```

**GC pressure (% of time spent in GC):**
```promql
rate(java_lang_garbagecollector_collectiontime[5m])
  / rate(java_lang_garbagecollector_collectioncount[5m])
```

---

## Chapter 4: Producer Tuning

The producer is where most performance tuning happens. Every parameter interacts with others, so tuning is about finding the right balance for your workload.

### acks — The Durability vs Latency Trade-off

The `acks` setting controls how many replicas must acknowledge a write before the producer considers it successful.

| Setting | Latency | Durability | When to Use |
|---------|---------|------------|-------------|
| `acks=0` | Lowest (~0.5ms) | None — fire and forget | Metrics, logs you can afford to lose |
| `acks=1` | Low (~1–3ms) | Leader only | Low-latency use cases with acceptable risk |
| `acks=all` | Highest (~3–10ms) | Full ISR | Financial transactions, event sourcing |

On the krafter cluster with `min.insync.replicas=2`, `acks=all` means the leader waits for at least 1 follower to replicate before responding. This adds the follower's fetch + write latency to every produce request.

**Benchmarking the difference:**

```bash
# acks=0 (fire and forget)
kubectl exec -n kafka krafter-pool-alpha-0 -- \
  bin/kafka-producer-perf-test.sh \
    --topic acks-benchmark \
    --num-records 100000 \
    --record-size 1024 \
    --throughput -1 \
    --producer-props \
      bootstrap.servers=localhost:9092 \
      acks=0

# acks=1 (leader only)
kubectl exec -n kafka krafter-pool-alpha-0 -- \
  bin/kafka-producer-perf-test.sh \
    --topic acks-benchmark \
    --num-records 100000 \
    --record-size 1024 \
    --throughput -1 \
    --producer-props \
      bootstrap.servers=localhost:9092 \
      acks=1

# acks=all (full ISR — this is what make test uses)
kubectl exec -n kafka krafter-pool-alpha-0 -- \
  bin/kafka-producer-perf-test.sh \
    --topic acks-benchmark \
    --num-records 100000 \
    --record-size 1024 \
    --throughput -1 \
    --producer-props \
      bootstrap.servers=localhost:9092 \
      acks=all
```

Record the throughput (records/sec) and average latency (ms) for each. The difference between `acks=0` and `acks=all` on this cluster directly measures replication overhead.

### batch.size — Amortizing Per-Request Overhead

The producer accumulates records into batches before sending them. Each batch becomes a single ProduceRequest over the network, amortizing the per-message overhead.

| Value | Effect |
|-------|--------|
| `16384` (16KB, default) | Reasonable for low-volume workloads |
| `65536` (64KB) | Better throughput for sustained workloads |
| `131072` (128KB) | Near-optimal for high-throughput pipelines |
| `262144` (256KB) | Diminishing returns; increases memory pressure |

On the krafter cluster (2Gi memory per broker), batch sizes above 128KB start eating into the producer's `buffer.memory` budget and the broker's request handler memory.

**How to test:**
```bash
for BATCH in 16384 65536 131072; do
  echo "--- batch.size=$BATCH ---"
  kubectl exec -n kafka krafter-pool-alpha-0 -- \
    bin/kafka-producer-perf-test.sh \
      --topic batch-benchmark \
      --num-records 500000 \
      --record-size 1024 \
      --throughput -1 \
      --producer-props \
        bootstrap.servers=localhost:9092 \
        acks=all \
        batch.size=$BATCH
done
```

### linger.ms — Trading Latency for Throughput

When a batch is not full, the producer waits up to `linger.ms` milliseconds for more records before sending what it has. A value of 0 means send immediately (no waiting).

| Value | Effect |
|-------|--------|
| `0` (default) | Minimum latency; small batches |
| `5` | Good balance — producers at moderate throughput fill batches reasonably |
| `20` | High throughput; noticeable latency addition |
| `100` | Maximum batching; 100ms added to every message's latency |

The interaction between `batch.size` and `linger.ms` is critical:
- If your produce rate fills a batch within `linger.ms`, the batch is sent when full (linger doesn't add latency)
- If your produce rate is slower than one batch per `linger.ms`, every message gets `linger.ms` added to its latency

**Rule of thumb:** Set `linger.ms` to roughly `batch.size / (your_expected_bytes_per_second)` to ensure batches fill before the linger timer fires.

### compression.type — CPU vs Network vs Disk

Compression reduces the size of each batch on the wire and on disk, but costs CPU.

| Codec | Ratio | Speed | When to Use |
|-------|-------|-------|-------------|
| `none` | 1.0x | Zero CPU | Low volume, CPU-constrained |
| `lz4` | ~2–3x | Very fast | Default choice for throughput |
| `snappy` | ~2x | Fast | Legacy compatibility |
| `zstd` | ~3–5x | Moderate | Bandwidth-constrained, archival |
| `gzip` | ~3–5x | Slow | Maximum compression, latency-tolerant |

On the krafter cluster with 2Gi per broker, CPU is not the bottleneck — network and disk are artificially fast (Kind loopback). In a production environment, `lz4` is almost always the right choice. For this dev cluster, `none` vs `lz4` benchmarks quantify the CPU cost.

**Benchmarking:**
```bash
for CODEC in none lz4 snappy zstd gzip; do
  echo "--- compression.type=$CODEC ---"
  kubectl exec -n kafka krafter-pool-alpha-0 -- \
    bin/kafka-producer-perf-test.sh \
      --topic compression-benchmark \
      --num-records 500000 \
      --record-size 1024 \
      --throughput -1 \
      --producer-props \
        bootstrap.servers=localhost:9092 \
        acks=all \
        compression.type=$CODEC
done
```

### buffer.memory — The Producer's Total Memory Budget

`buffer.memory` (default 32MB) is the total memory the producer allocates for buffering records waiting to be sent. When this budget is exhausted, `send()` blocks for up to `max.block.ms` (default 60s).

On the krafter cluster, the perf-test commands run inside the broker pod, sharing the broker's 2Gi memory. Keep `buffer.memory` conservative (32–64MB) to avoid starving the broker.

### max.in.flight.requests.per.connection

Controls how many batches can be in-flight (sent but not yet acknowledged) per broker connection.

| Value | Effect |
|-------|--------|
| `1` | Strict ordering — no reordering on retry; lowest throughput |
| `5` (default) | Good throughput; possible message reordering on retry |
| `5` + `enable.idempotence=true` | Good throughput + strict ordering (Kafka handles dedup on retry) |

**Recommendation for krafter:** Use `enable.idempotence=true` (the default since Kafka 3.0) with `max.in.flight.requests.per.connection=5`. This gives you both high throughput and exactly-once delivery guarantees.

### Producer Tuning Quick Reference

For the krafter cluster (2Gi, RF=3, ISR=2, 3 partitions), recommended starting points:

| Parameter | Low Latency | High Throughput | Balanced |
|-----------|-------------|-----------------|----------|
| `acks` | `1` | `all` | `all` |
| `batch.size` | `16384` | `131072` | `65536` |
| `linger.ms` | `0` | `20` | `5` |
| `compression.type` | `none` | `lz4` | `lz4` |
| `buffer.memory` | `33554432` | `67108864` | `33554432` |
| `max.in.flight` | `5` | `5` | `5` |

---

## Chapter 5: Consumer Tuning

Consumer performance is about how fast you can read data, process it, and commit offsets without falling behind or triggering a rebalance.

### fetch.min.bytes — Minimum Fetch Size

The broker delays its response to a FetchRequest until it has at least `fetch.min.bytes` of data to return (or `fetch.max.wait.ms` expires).

| Value | Effect |
|-------|--------|
| `1` (default) | Respond immediately, even for 1 byte. Lowest latency. |
| `1024` | Wait for 1KB. Reduces fetch request volume. |
| `65536` | Wait for 64KB. Better throughput, higher latency. |

For perf testing on krafter: use `1` (default) when measuring consumer latency, increase to `65536` when measuring maximum throughput.

### fetch.max.wait.ms

Maximum time the broker waits to satisfy `fetch.min.bytes`. Default is 500ms. This is the upper bound on consumer latency when the topic has no new data.

### max.poll.records — Processing Batch Size

The maximum number of records returned per `poll()` call. Default is 500.

| Value | Effect |
|-------|--------|
| `100` | Smaller batches, more frequent polls, lower per-batch latency |
| `500` (default) | Reasonable for most workloads |
| `1000–5000` | Higher throughput if processing is fast and you want fewer polls |

If your processing per record is expensive (>10ms), you want smaller `max.poll.records` to avoid exceeding `max.poll.interval.ms` and triggering a rebalance.

### max.partition.fetch.bytes

Maximum data the broker returns per partition per fetch (default 1MB). With 3 partitions on krafter, a consumer receiving from all 3 could get up to 3MB per fetch.

### Consumer Parallelism

Kafka's parallelism model for consumers is partition-based. One partition can only be consumed by one consumer in a group.

```
Topic: perf-test (3 partitions)

1 consumer  → reads P0, P1, P2 (max throughput = 1 consumer's rate)
2 consumers → C1 reads P0,P1;  C2 reads P2 (unbalanced)
3 consumers → C1 reads P0; C2 reads P1; C3 reads P2 (optimal)
4 consumers → C1:P0, C2:P1, C3:P2, C4:idle (wasted)
```

On the krafter cluster with 3-partition topics, the maximum useful consumer count per group is 3.

### Consumer Perf Test

```bash
kubectl exec -n kafka krafter-pool-alpha-0 -- \
  bin/kafka-consumer-perf-test.sh \
    --topic perf-test \
    --bootstrap-server localhost:9092 \
    --messages 1000000 \
    --threads 1 \
    --group perf-consumer-group \
    --show-detailed-stats
```

**Output fields:**
- `MB.sec` — consumption throughput in megabytes per second
- `nMsg.sec` — messages consumed per second
- `rebalance.time.ms` — time spent in consumer group rebalance

### Consumer Lag Monitoring

Consumer lag is the difference between the latest produced offset and the last committed consumer offset. It's the most important consumer health metric.

```bash
kubectl exec -n kafka krafter-pool-alpha-0 -- \
  bin/kafka-consumer-groups.sh \
    --bootstrap-server localhost:9092 \
    --group perf-test-group \
    --describe
```

Output shows per-partition LAG. If lag is steadily increasing, you need more consumers or faster processing.

**PromQL for consumer lag:**
```promql
kafka_server_brokertopicmetrics_messagesinpersec
  - on(topic) kafka_server_brokertopicmetrics_bytesoutpersec
```

---

## Chapter 6: Broker and Strimzi Tuning

Broker tuning on Strimzi differs from bare-metal Kafka because resource allocation happens through Kubernetes resource specs and Strimzi CRD fields, not `server.properties`.

### Memory Allocation

In `config/kafka.yaml`, each `KafkaNodePool` specifies:

```yaml
resources:
  requests:
    memory: 2Gi
  limits:
    memory: 2Gi
```

This 2Gi covers:
1. **JVM heap** — Strimzi defaults to ~50% of the container memory for heap
2. **OS page cache** — The remaining ~1Gi is used by the OS for caching log segments
3. **JVM metaspace, thread stacks, direct buffers** — ~200–400MB

**Tuning via Strimzi `jvmOptions`:**

To explicitly control JVM memory, add to the Kafka CR:

```yaml
spec:
  kafka:
    jvmOptions:
      -Xms: 1024m
      -Xmx: 1024m
      gcLoggingEnabled: true
```

This gives 1Gi to heap and leaves ~1Gi for page cache. On a 2Gi broker, this is about right. Over-allocating heap starves the page cache, which is critical for Kafka's performance (Kafka reads/writes are served from page cache).

### JVM Garbage Collector

Kafka 4.1.1 defaults to G1GC. The choices:

| GC | Best For | Strimzi Config |
|----|----------|----------------|
| G1GC (default) | General-purpose, good latency/throughput balance | No config needed |
| ZGC | Sub-millisecond pause times, large heaps | `-XX:+UseZGC` in `jvmOptions` |
| Shenandoah | Low-latency alternative to ZGC | `-XX:+UseShenandoahGC` |

For the krafter cluster with 1Gi heap, G1GC is fine. ZGC shines at 8Gi+ heaps. You can switch via Strimzi:

```yaml
spec:
  kafka:
    jvmOptions:
      javaSystemProperties:
        - name: "-XX:+UseZGC"
```

### Broker Thread Pool Configuration

These go in `spec.kafka.config` in the Kafka CR:

| Parameter | Default | Tuning Guidance |
|-----------|---------|-----------------|
| `num.network.threads` | 3 | Handles network I/O. Increase if `NetworkProcessorAvgIdlePercent` < 0.3 |
| `num.io.threads` | 8 | Handles disk I/O. Increase if `RequestHandlerAvgIdlePercent` < 0.3 |
| `num.replica.fetchers` | 1 | Threads fetching from leaders. Increase if replication lag is high |
| `socket.send.buffer.bytes` | 102400 | Socket buffer for sends. Increase for cross-DC replication |
| `socket.receive.buffer.bytes` | 102400 | Socket buffer for receives |

On the krafter Kind cluster, the defaults are adequate. In production with cross-AZ latency, bump `num.replica.fetchers` to 2–4 and increase socket buffers to 1MB.

### Log Segment Configuration

| Parameter | Default | Effect |
|-----------|---------|--------|
| `log.segment.bytes` | 1073741824 (1GB) | Log files roll at this size |
| `log.retention.hours` | 168 (7 days) | Data retained for this long |
| `log.retention.bytes` | -1 (unlimited) | Per-partition size limit |
| `log.cleanup.policy` | delete | `delete` or `compact` |

On the krafter cluster with 10Gi per broker, a 1GB segment size means at most 10 segments per broker before disk pressure. If running sustained perf tests, consider lowering `log.retention.hours` to 1 or adding `log.retention.bytes` to prevent filling the disk:

```yaml
spec:
  kafka:
    config:
      log.retention.hours: 1
      log.retention.bytes: 5368709120  # 5Gi per partition
```

### replica.lag.time.max.ms

Default is 30,000ms (30s). If a follower hasn't fetched from the leader within this window, it's removed from the ISR. During perf testing:

- Too low (e.g., 5s): causes ISR churn under load, leading to write rejections
- Too high (e.g., 60s): delays detection of genuinely stuck followers

30s is fine for perf testing. Only lower it if you need faster failover detection.

### Strimzi Resource Requests in Practice

For performance testing, you might want to temporarily increase broker resources. Edit `config/kafka.yaml`:

```yaml
spec:
  replicas: 1
  roles:
    - controller
    - broker
  resources:
    requests:
      memory: 4Gi
      cpu: "2"
    limits:
      memory: 4Gi
      cpu: "2"
```

Then reapply:

```bash
kubectl apply -f config/kafka.yaml
```

Strimzi performs a rolling restart automatically. Watch with:

```bash
kubectl get pods -n kafka -w
```

---

## Chapter 7: Running Performance Tests

### The Built-In Performance Test

The simplest path to a performance baseline:

```bash
make test
```

This runs `test-kafka-performance.sh`, which:

1. Creates a `performance` namespace
2. Creates a `perf-test` topic (3 partitions, RF=3)
3. Deploys a producer Job: 1M messages × 1KB, unlimited throughput, `acks=all`, `batch.size=16384`, `linger.ms=10`
4. Waits for completion, prints results
5. Deploys a consumer Job: consumes 1M messages with detailed stats
6. Prints consumer results

**Reading the Producer Output:**

```
1000000 records sent, 45321.5 records/sec (44.26 MB/sec),
  12.3 ms avg latency, 215.0 ms max latency,
  8 ms 50th, 23 ms 95th, 45 ms 99th, 189 ms 99.9th.
```

| Field | Meaning |
|-------|---------|
| `records/sec` | Sustained throughput |
| `MB/sec` | Data throughput (records/sec × record_size) |
| `avg latency` | Mean time from `send()` to acknowledgment |
| `50th` | Median latency (P50) |
| `95th` | 95th percentile — the latency 95% of messages beat |
| `99th` | 99th percentile — tail latency |
| `99.9th` | Near-worst-case latency |

**Healthy baseline for krafter (rough expectations):**
- Producer: 30,000–60,000 records/sec, ~30–50 MB/sec, P99 < 100ms
- Consumer: 50–100 MB/sec

These numbers will vary based on Host machine CPU/memory and Docker resource allocation.

### Custom Performance Tests

For more control, exec into a broker pod directly:

```bash
kubectl exec -it -n kafka krafter-pool-alpha-0 -- /bin/bash
```

Then run targeted tests:

**Throughput ceiling (unlimited, no acks):**
```bash
bin/kafka-producer-perf-test.sh \
  --topic custom-perf \
  --num-records 5000000 \
  --record-size 1024 \
  --throughput -1 \
  --producer-props \
    bootstrap.servers=localhost:9092 \
    acks=0
```

**Latency-focused (limited throughput to avoid saturation):**
```bash
bin/kafka-producer-perf-test.sh \
  --topic latency-test \
  --num-records 100000 \
  --record-size 256 \
  --throughput 10000 \
  --producer-props \
    bootstrap.servers=localhost:9092 \
    acks=all \
    linger.ms=0
```

**Large messages (100KB per record):**
```bash
bin/kafka-producer-perf-test.sh \
  --topic large-msg-test \
  --num-records 50000 \
  --record-size 102400 \
  --throughput -1 \
  --producer-props \
    bootstrap.servers=localhost:9092 \
    acks=all \
    max.request.size=1048576
```

### Creating Test Topics with Specific Configurations

```bash
kubectl exec -n kafka krafter-pool-alpha-0 -- \
  bin/kafka-topics.sh --create \
    --bootstrap-server localhost:9092 \
    --topic high-partition-test \
    --partitions 12 \
    --replication-factor 3 \
    --config min.insync.replicas=2 \
    --config retention.ms=3600000
```

Higher partition counts allow more parallelism but increase metadata overhead and memory usage. On the 2Gi krafter brokers, stay below 100 partitions total.

### Cleaning Up After Tests

```bash
# Delete the performance namespace and all Jobs
kubectl delete namespace performance

# Delete test topics
kubectl exec -n kafka krafter-pool-alpha-0 -- \
  bin/kafka-topics.sh --delete \
    --bootstrap-server localhost:9092 \
    --topic perf-test

# List all topics to verify cleanup
kubectl exec -n kafka krafter-pool-alpha-0 -- \
  bin/kafka-topics.sh --list \
    --bootstrap-server localhost:9092
```

---

## Chapter 8: Advanced Testing Scenarios

These go beyond basic throughput measurements to answer questions that matter in production.

### Scenario 1: Sustained Load Over Time

Basic perf tests run for seconds. Production clusters run for months. Sustained tests reveal:
- Memory leaks in the broker JVM
- GC pressure building over time
- Log segment rolling behavior under load
- Page cache thrashing

```bash
# 30-minute sustained load at 10,000 msg/sec
kubectl exec -n kafka krafter-pool-alpha-0 -- \
  bin/kafka-producer-perf-test.sh \
    --topic sustained-test \
    --num-records 18000000 \
    --record-size 1024 \
    --throughput 10000 \
    --producer-props \
      bootstrap.servers=localhost:9092 \
      acks=all \
      batch.size=65536 \
      linger.ms=5 \
      compression.type=lz4
```

While running, watch the "Kafka JVM Metrics" Grafana dashboard for:
- Heap usage trend (should be sawtooth, not climbing)
- GC pause durations (should stay consistent)
- Thread count (should be stable)

### Scenario 2: Multi-Producer Throughput

Deploy multiple standalone producer Jobs to test cluster-level throughput:

```bash
for i in 1 2 3; do
cat <<EOF | kubectl apply -f -
apiVersion: batch/v1
kind: Job
metadata:
  name: multi-producer-$i
  namespace: kafka
spec:
  backoffLimit: 0
  template:
    spec:
      restartPolicy: Never
      containers:
      - name: producer
        image: quay.io/strimzi/kafka:0.49.0-kafka-4.1.1
        imagePullPolicy: Never
        command: ["/bin/bash", "-c"]
        args:
        - |
          bin/kafka-producer-perf-test.sh \
            --topic multi-producer-test \
            --num-records 500000 \
            --record-size 1024 \
            --throughput -1 \
            --producer-props \
              bootstrap.servers=krafter-kafka-bootstrap.kafka.svc:9092 \
              acks=all \
              batch.size=65536 \
              linger.ms=5
EOF
done
```

Compare the aggregate throughput (sum of all producers) against the single-producer baseline. If aggregate throughput doesn't scale linearly, the bottleneck is on the broker side (threads, disk, or memory).

### Scenario 3: acks Comparison Under Identical Load

```bash
for ACKS in 0 1 all; do
  TOPIC="acks-$ACKS-test"
  kubectl exec -n kafka krafter-pool-alpha-0 -- \
    bin/kafka-topics.sh --create --if-not-exists \
      --bootstrap-server localhost:9092 \
      --topic $TOPIC --partitions 3 --replication-factor 3

  echo "=== acks=$ACKS ==="
  kubectl exec -n kafka krafter-pool-alpha-0 -- \
    bin/kafka-producer-perf-test.sh \
      --topic $TOPIC \
      --num-records 500000 \
      --record-size 1024 \
      --throughput -1 \
      --producer-props \
        bootstrap.servers=localhost:9092 \
        acks=$ACKS \
        batch.size=65536 \
        linger.ms=5
done
```

This directly quantifies the cost of durability on the krafter cluster.

### Scenario 4: Load Test Under Chaos

The most realistic scenario: what happens to performance when a broker dies?

**Terminal 1 — Start sustained load:**
```bash
kubectl exec -n kafka krafter-pool-alpha-0 -- \
  bin/kafka-producer-perf-test.sh \
    --topic chaos-load-test \
    --num-records 5000000 \
    --record-size 1024 \
    --throughput 5000 \
    --producer-props \
      bootstrap.servers=localhost:9092 \
      acks=all
```

**Terminal 2 — While load is running, kill a broker:**
```bash
make chaos-kafka-pod-delete
```

Watch the Grafana dashboards for:
- Throughput dip magnitude and duration
- Latency spike height (P99)
- Under-replicated partition count and recovery time
- Whether any errors appear in the producer output

### Scenario 5: Compression Benchmark Matrix

Systematic comparison across message sizes and codecs:

```bash
for SIZE in 256 1024 10240 102400; do
  for CODEC in none lz4 snappy zstd; do
    TOPIC="compress-${SIZE}-${CODEC}"
    kubectl exec -n kafka krafter-pool-alpha-0 -- \
      bin/kafka-topics.sh --create --if-not-exists \
        --bootstrap-server localhost:9092 \
        --topic $TOPIC --partitions 3 --replication-factor 3

    echo "=== size=$SIZE codec=$CODEC ==="
    kubectl exec -n kafka krafter-pool-alpha-0 -- \
      bin/kafka-producer-perf-test.sh \
        --topic $TOPIC \
        --num-records 100000 \
        --record-size $SIZE \
        --throughput -1 \
        --producer-props \
          bootstrap.servers=localhost:9092 \
          acks=all \
          compression.type=$CODEC
  done
done
```

Record the results in a matrix to see where compression helps most. Compression has the biggest impact on larger, compressible messages.

### Scenario 6: End-to-End Latency

Producer-to-consumer round-trip latency. Run a producer at controlled throughput and a consumer simultaneously:

**Terminal 1 — Producer at fixed rate:**
```bash
kubectl exec -n kafka krafter-pool-alpha-0 -- \
  bin/kafka-producer-perf-test.sh \
    --topic e2e-latency \
    --num-records 100000 \
    --record-size 512 \
    --throughput 1000 \
    --producer-props \
      bootstrap.servers=localhost:9092 \
      acks=all
```

**Terminal 2 — Consumer reading same topic:**
```bash
kubectl exec -n kafka krafter-pool-alpha-0 -- \
  bin/kafka-consumer-perf-test.sh \
    --topic e2e-latency \
    --bootstrap-server localhost:9092 \
    --messages 100000 \
    --group e2e-group
```

The consumer's `MB.sec` divided by the producer's `MB/sec` gives you the consumption/production ratio. If the consumer is faster than the producer, lag stays at zero. If slower, lag builds.

### Scenario 7: Data Integrity Under Chaos

The ultimate validation: produce sequenced records with CRC32 checksums, inject a broker failure mid-flight, then verify every record was delivered without corruption or reordering.

Unlike Scenario 4 (which only watches for throughput dips), this scenario verifies **data correctness** through the failure window.

```bash
# Scaffold the combined integrity + chaos test:
kates test scaffold --type INTEGRITY_CHAOS -o integrity-chaos.yaml

# Review the generated YAML (it includes chaos config and SLA gates)
cat integrity-chaos.yaml

# Run it:
kates test apply -f integrity-chaos.yaml --wait
```

The generated YAML includes:
- INTEGRITY workload with `enableCrc: true` and `enableIdempotence: true`
- A `chaos` block targeting a specific Litmus experiment
- SLA gates for data loss, RTO, RPO, ordering, and CRC integrity

**Example output:**

```
╭─────────────────────────────────────────────╮
│  KATES Test Summary                         │
├──────────────┬────────┬─────────────────────┤
│ Scenario     │ Status │ Result              │
├──────────────┼────────┼─────────────────────┤
│ Integrity    │ DONE   │ ✓ SLA Pass          │
│ Under Chaos  │        │                     │
╰──────────────┴────────┴─────────────────────╯

  Data Integrity
    Sent        500,000
    Consumed    500,000
    Lost        0
    CRC Fail    0
    Out of Ord  0
    Verdict     ✓ PASS

  Integrity Timeline
    ┌───────────────┬──────────┬─────────────────────┐
    │ Timestamp     │ Type     │ Detail              │
    ├───────────────┼──────────┼─────────────────────┤
    │ 1707933600000 │ SUMMARY  │ verdict=PASS lost=0 │
    └───────────────┴──────────┴─────────────────────┘
```

If any SLA gate is violated, the CLI prints the violations and exits with code 1:

```
  ✗ SLA Violations:
    p99=142ms > 100ms
    dataLoss=0.02% > 0.00%
```

**What makes this different from manual chaos testing:**
- Every record is tracked by sequence number, not just aggregate throughput
- CRC32 detects bit-level corruption that throughput tests miss
- Per-partition ordering verification catches reordering bugs
- RTO/RPO give measurable recovery metrics, not just visual Grafana inspection
- SLA gates make it CI/CD-compatible — the test either passes or fails with a clear reason

**Pass criteria:**
- Zero data loss with idempotent producer + `acks=all`
- RTO < 10,000ms
- RPO < 5,000ms
- Zero CRC failures
- Zero ordering violations

---

## Chapter 9: Strimzi-Specific Considerations

Running Kafka on Strimzi introduces behaviors and constraints that don't exist on bare-metal. Understanding them is critical for accurate performance interpretation.

### KafkaNodePool Resource Isolation

Each `KafkaNodePool` in `config/kafka.yaml` independently specifies resource limits. This means the alpha, sigma, and gamma brokers can be configured with different resources:

```yaml
# In config/kafka.yaml
apiVersion: kafka.strimzi.io/v1beta2
kind: KafkaNodePool
metadata:
  name: pool-alpha
spec:
  resources:
    requests:
      memory: 2Gi
    limits:
      memory: 2Gi
```

**Performance implication:** If you give one pool more memory for testing, rack-aware replica placement may route traffic to the better-resourced broker. This skews results unless all pools have identical resources.

### Rack Awareness and Replica Placement

The Kafka CR enables rack awareness:

```yaml
spec:
  kafka:
    rack:
      topologyKey: topology.kubernetes.io/zone
```

This means Kafka places replicas across different zones (alpha, sigma, gamma). With RF=3 and 3 zones, every partition has exactly one replica per zone. This is optimal for fault tolerance but means every replication hop is cross-zone.

On Kind, "cross-zone" is just inter-container traffic (sub-millisecond). In production on cloud providers, cross-AZ latency is 0.5–2ms, which adds directly to `acks=all` latency.

### Zone-Pinned Storage

Each broker's PVC is bound to a zone-specific StorageClass:

```yaml
storage:
  type: jbod
  volumes:
    - id: 0
      type: persistent-claim
      size: 10Gi
      class: local-storage-alpha
      deleteClaim: false
```

The StorageClass uses `volumeBindingMode: WaitForFirstConsumer` and `allowedTopologies` to pin the PVC to the correct node.

**Performance implication:** If a broker pod is rescheduled (e.g., after `kubectl delete pod`), it comes back on the same node with the same data. No data migration. Recovery is fast because the log directory is already populated — the broker only needs to catch up from its last committed offset.

### Strimzi Operator Overhead

The Strimzi operator runs in the `kafka` namespace and consumes resources:
- **Cluster Operator**: ~200MB RSS, reconciles every 2 minutes by default
- **Entity Operator** (Topic + User operators): ~250MB RSS, watches for topic/user CR changes
- **PodMonitors**: Two PodMonitor resources add Prometheus scrape targets

During performance tests, the operator is mostly idle. But if you create/delete topics rapidly (e.g., test cleanup), the Topic Operator reconciles each change, which can briefly compete for Kubernetes API server resources.

### Rolling Restarts During Performance Tests

Strimzi config changes trigger rolling restarts. If you modify the Kafka CR during a performance test, the operator will restart brokers one at a time. Each restart:

1. Drains connections from the target broker
2. Stops the broker process
3. Waits for the pod to terminate
4. Starts a new pod with updated config
5. Waits for the broker to join the cluster and catch up

During step 2–4, the cluster runs with N-1 brokers. With ISR=2 and RF=3, writes continue but latency increases (only 2 replicas available, replication paths change).

**Best practice:** Never modify the Kafka CR while a performance test is running unless you're specifically testing rolling restart behavior.

### Entity Operator as a Performance Factor

The Entity Operator watches for `KafkaTopic` CRDs and reconciles them into actual Kafka topics. If you create topics via `kafka-topics.sh` (which the perf tests do), the Entity Operator detects the topic and may create a `KafkaTopic` CR for it, or may conflict if a CR already exists.

For performance testing, create topics directly via `kafka-topics.sh` and avoid creating `KafkaTopic` CRDs. This bypasses the Entity Operator and gives you direct control over topic configuration.

---

## Chapter 10: Performance Anti-Patterns and Troubleshooting

### Anti-Pattern 1: Default Batching at High Throughput

**Symptom:** Producer reports high latency but low throughput.
**Cause:** `batch.size=16384` and `linger.ms=0` means every `send()` triggers a tiny request. The producer saturates the broker's request handler threads with thousands of small requests per second.
**Fix:** Increase `batch.size` to 65536+ and set `linger.ms` to 5–10.

### Anti-Pattern 2: acks=all Without Understanding the Cost

**Symptom:** Producer throughput is 3× lower than expected.
**Cause:** `acks=all` waits for ISR replication. If one follower is slow (e.g., garbage collecting), every produce request waits for it.
**Fix:** Either accept the latency (it's the cost of durability) or use `acks=1` if you can tolerate leader-only durability.

### Anti-Pattern 3: Too Many Partitions

**Symptom:** Broker memory usage is high, leader elections take longer, consumer rebalances are slow.
**Cause:** Each partition consumes memory (index files, log segments, metadata). On the krafter cluster with 2Gi per broker, hundreds of partitions cause significant overhead.
**Fix:** Keep total partitions per broker under 100. For 3 brokers with RF=3, this means ~100 topic-partitions total.

### Anti-Pattern 4: Consumer Processing Blocking poll()

**Symptom:** Consumer gets kicked from the group (rebalance every few minutes), messages are processed but then reprocessed.
**Cause:** Processing takes longer than `max.poll.interval.ms` (300s default). The broker thinks the consumer is dead and reassigns its partitions.
**Fix:** Reduce `max.poll.records` or increase `max.poll.interval.ms`. Better: decouple processing from polling by handing records off to a worker thread pool.

### Anti-Pattern 5: Testing with Non-Representative Messages

**Symptom:** Perf test shows 100K msg/sec but production does 5K msg/sec.
**Cause:** Perf tests use random bytes. Production messages may be larger, require serialization (Avro/Protobuf with schema registry), or have complex key distributions that cause partition skew.
**Fix:** Generate test messages that match production characteristics in size, compressibility, and key distribution.

### Anti-Pattern 6: Ignoring JVM GC During Tests

**Symptom:** Consistent throughput with periodic spikes in P99 latency.
**Cause:** G1GC mixed collections. At 1Gi heap with sustained throughput, the old generation fills and G1GC pauses to clean it.
**Fix:** Monitor GC via the JVM Grafana dashboard. If pauses exceed 100ms, consider tuning G1GC:

```yaml
spec:
  kafka:
    jvmOptions:
      -XX:
        MaxGCPauseMillis: "50"
        InitiatingHeapOccupancyPercent: "35"
```

### Troubleshooting Quick Reference

| Symptom | Check First | Likely Cause | Fix |
|---------|-------------|--------------|-----|
| Producer latency > 100ms | `acks` setting | `acks=all` with slow follower | Check ISR health, reduce `acks` |
| Producer throughput < 10K/sec | `batch.size`, `linger.ms` | Tiny batches | Increase both |
| Consumer lag increasing | Consumer count vs partitions | Too few consumers | Add consumers up to partition count |
| Under-replicated partitions | Broker resource usage | Memory/CPU pressure | Check JVM heap, GC |
| Broker OOM killed | JVM heap size | Heap too large, no page cache | Reduce `-Xmx`, leave room for OS |
| Rebalance storms | `max.poll.interval.ms` | Slow processing | Increase interval or reduce `max.poll.records` |
| Disk full | `log.retention.*` | No retention policy | Set `log.retention.hours` or `log.retention.bytes` |
| High request queue time | `num.io.threads` | IO threads saturated | Increase `num.io.threads` |
| Network processor idle < 30% | `num.network.threads` | Network threads saturated | Increase `num.network.threads` |
| Slow leader election | KRaft quorum health | Network partition or resource starvation | Check all 3 controllers are healthy |

### Diagnostic Flowchart

```
Throughput lower than expected?
    │
    ├─ Is producer latency high (>50ms)?
    │   ├─ Yes → Check acks setting
    │   │         ├─ acks=all → Check IsrShrinks, follower lag
    │   │         └─ acks=1   → Check request queue time
    │   └─ No  → Check batch size / linger.ms
    │             └─ Small batches? → Increase batch.size + linger.ms
    │
    ├─ Is consumer lag growing?
    │   ├─ Consumer count < partition count? → Add consumers
    │   └─ Processing slow? → Profile application, reduce max.poll.records
    │
    └─ Are broker metrics healthy?
        ├─ RequestHandlerIdlePercent < 0.3? → Increase num.io.threads
        ├─ NetworkProcessorIdlePercent < 0.3? → Increase num.network.threads
        ├─ Heap > 80%? → Reduce -Xmx or add memory to node pool
        └─ UnderReplicatedPartitions > 0? → Check follower broker health
```

---

## Chapter 11: Types of Performance Testing

Performance testing is not a single activity — it is a family of related methodologies, each designed to answer a different question about system behavior. This chapter defines the seven standard types, maps each one to concrete scenarios on the krafter cluster, and provides ready-to-run commands and pass/fail criteria.

### Taxonomy

| Type | Question It Answers | Duration | Load Profile |
|------|---------------------|----------|--------------|
| Load | Can the system handle expected production traffic? | 10–30 min | Steady, at target rate |
| Stress | Where does the system break? | 15–45 min | Ramping beyond limits |
| Spike | How does the system react to sudden bursts? | 5–15 min | Sharp peaks and valleys |
| Endurance (Soak) | Does the system degrade over time? | 2–72 hours | Steady, extended |
| Scalability | How well does the system scale up or out? | 30–60 min | Steady, across configurations |
| Volume | Can the system handle large datasets? | 30–120 min | Bulk data injection |
| Capacity | What is the maximum the system can sustain? | 30–60 min | Binary-search ramp |
| Round-Trip | What is the true produce→consume latency? | 10–30 min | Steady, measured per-record |
| Integrity | Is every record accounted for without corruption? | 10–60 min | Produce→consume→verify |

### 1. Load Testing

**Goal:** Verify that the system performs within acceptable limits under expected production workloads. Load testing does not try to break the system — it validates that normal operating conditions produce acceptable throughput, latency, and resource usage.

**What it measures:**
- Throughput at target load (records/sec, MB/sec)
- Latency percentiles (P50, P95, P99) at target load
- Resource consumption (CPU, heap, disk I/O) under sustained expected traffic
- Consumer lag stability

**Scenario for krafter:** Simulate 5 concurrent producers each sending 200,000 messages (1KB) at a combined rate of 25,000 msg/sec, while 3 consumers read from the same topic. This mimics a realistic multi-application production environment.

```bash
# Automated via:
make test-load

# Or manually — create the topic
kubectl exec -n kafka krafter-pool-alpha-0 -- \
  bin/kafka-topics.sh --create --if-not-exists \
    --bootstrap-server localhost:9092 \
    --topic load-test \
    --partitions 3 \
    --replication-factor 3 \
    --config min.insync.replicas=2

# Deploy 5 concurrent producer Jobs
for i in 1 2 3 4 5; do
cat <<EOF | kubectl apply -f -
apiVersion: batch/v1
kind: Job
metadata:
  name: load-producer-$i
  namespace: kafka
spec:
  backoffLimit: 0
  template:
    spec:
      restartPolicy: Never
      containers:
      - name: producer
        image: quay.io/strimzi/kafka:0.49.0-kafka-4.1.1
        imagePullPolicy: Never
        command: ["/bin/bash", "-c"]
        args:
        - |
          bin/kafka-producer-perf-test.sh \
            --topic load-test \
            --num-records 200000 \
            --record-size 1024 \
            --throughput 5000 \
            --producer-props \
              bootstrap.servers=krafter-kafka-bootstrap.kafka.svc:9092 \
              acks=all \
              batch.size=65536 \
              linger.ms=5 \
              compression.type=lz4
EOF
done

# Deploy 3 concurrent consumer Jobs
for i in 1 2 3; do
cat <<EOF | kubectl apply -f -
apiVersion: batch/v1
kind: Job
metadata:
  name: load-consumer-$i
  namespace: kafka
spec:
  backoffLimit: 0
  template:
    spec:
      restartPolicy: Never
      containers:
      - name: consumer
        image: quay.io/strimzi/kafka:0.49.0-kafka-4.1.1
        imagePullPolicy: Never
        command: ["/bin/bash", "-c"]
        args:
        - |
          bin/kafka-consumer-perf-test.sh \
            --topic load-test \
            --bootstrap-server krafter-kafka-bootstrap.kafka.svc:9092 \
            --messages 333334 \
            --group load-test-group \
            --show-detailed-stats
EOF
done
```

**Metrics to watch (Grafana — "Kafka Performance Testing" dashboard):**
- Row 2 (Throughput): Bytes In/sec should show ~25 MB/sec aggregate, balanced across brokers
- Row 4 (Broker Internals): Request Handler Idle should stay above 50%
- Row 5 (JVM): Heap usage in a healthy sawtooth pattern

**Pass criteria:**
- Aggregate throughput ≥ 20,000 records/sec
- P99 latency < 100ms
- Consumer lag returns to 0 within 60 seconds of producers finishing
- No under-replicated partitions

**Cleanup:**
```bash
kubectl delete jobs -n kafka -l 'job-name in (load-producer-1,load-producer-2,load-producer-3,load-producer-4,load-producer-5,load-consumer-1,load-consumer-2,load-consumer-3)'
kubectl exec -n kafka krafter-pool-alpha-0 -- bin/kafka-topics.sh --delete --bootstrap-server localhost:9092 --topic load-test
```

### 2. Stress Testing

**Goal:** Push the system beyond its normal operating limits to discover the breaking point. Stress testing answers the question: "at what throughput does the cluster start failing, and does it recover gracefully after the overload stops?"

**What it measures:**
- The throughput at which errors first appear
- The nature of the failure (timeout, `NotEnoughReplicasException`, OOM, thread starvation)
- Recovery time after overload is removed
- Whether data integrity is maintained through the failure and recovery cycle

**Scenario for krafter:** Ramp throughput in steps — 10K, 25K, 50K, 100K, unlimited — and observe where the 2Gi brokers begin to fail. On a 3-broker Kind cluster, memory and request handler threads are the expected bottleneck.

```bash
# Automated via:
make test-stress

# Or manually — ramp through increasing throughput targets
for THROUGHPUT in 10000 25000 50000 100000 -1; do
  TOPIC="stress-$THROUGHPUT"
  kubectl exec -n kafka krafter-pool-alpha-0 -- \
    bin/kafka-topics.sh --create --if-not-exists \
      --bootstrap-server localhost:9092 \
      --topic $TOPIC --partitions 3 --replication-factor 3

  echo "=== Throughput target: $THROUGHPUT msg/sec ==="
  kubectl exec -n kafka krafter-pool-alpha-0 -- \
    bin/kafka-producer-perf-test.sh \
      --topic $TOPIC \
      --num-records 500000 \
      --record-size 1024 \
      --throughput $THROUGHPUT \
      --producer-props \
        bootstrap.servers=localhost:9092 \
        acks=all \
        batch.size=131072 \
        linger.ms=10

  echo "--- Cooling down 30 seconds ---"
  sleep 30
done
```

**Recovery validation:** After the unlimited-rate run (which is likely to stress the cluster), run a simple 100K-message test at a known-good rate to verify the cluster returns to baseline performance:

```bash
kubectl exec -n kafka krafter-pool-alpha-0 -- \
  bin/kafka-producer-perf-test.sh \
    --topic stress-recovery \
    --num-records 100000 \
    --record-size 1024 \
    --throughput 10000 \
    --producer-props \
      bootstrap.servers=localhost:9092 \
      acks=all
```

**Integration with chaos experiments:** Combine stress testing with `make chaos-kafka-cpu-stress` to test the cluster under simultaneous CPU pressure and high throughput. This is where stress testing and chaos engineering overlap.

**Metrics to watch:**
- Row 4 (Broker Internals): Request Handler Idle dropping below 30% signals thread starvation
- Row 5 (JVM): watch for GC pauses exceeding 200ms
- Row 3 (Replication): ISR shrinks indicate followers can't keep up

**Pass criteria:**
- Cluster sustains ≥ 50,000 msg/sec without errors (stress test — not the breaking point)
- After overload, recovery to baseline throughput within 2 minutes
- No data corruption or permanent ISR loss

### 3. Spike Testing

**Goal:** Test the system's response to sudden, dramatic changes in load — both spikes up and drops back down. This simulates real-world events like flash sales, DDoS attacks, or marketing campaign launches.

**What it measures:**
- System stability during abrupt load increase (10× baseline or more)
- Latency behavior during the spike (how bad does P99 get?)
- Recovery speed after the spike subsides
- Whether any messages are lost or duplicated during the transition

**Scenario for krafter:** Simulate a flash sale by running a low baseline (1,000 msg/sec), then spiking to 50,000 msg/sec for 2 minutes, then dropping back to baseline.

```bash
# Automated via:
make test-spike

# Or manually — Phase 1: baseline (1 minute at 1K msg/sec)
echo "=== Phase 1: Baseline (1,000 msg/sec for 60s) ==="
kubectl exec -n kafka krafter-pool-alpha-0 -- \
  bin/kafka-producer-perf-test.sh \
    --topic spike-test \
    --num-records 60000 \
    --record-size 1024 \
    --throughput 1000 \
    --producer-props \
      bootstrap.servers=localhost:9092 \
      acks=all \
      batch.size=65536 \
      linger.ms=5

# Phase 2: spike (2 minutes at unlimited throughput, 3 concurrent producers)
echo "=== Phase 2: SPIKE (3 concurrent producers, unlimited) ==="
for i in 1 2 3; do
cat <<EOF | kubectl apply -f -
apiVersion: batch/v1
kind: Job
metadata:
  name: spike-burst-$i
  namespace: kafka
spec:
  backoffLimit: 0
  template:
    spec:
      restartPolicy: Never
      containers:
      - name: producer
        image: quay.io/strimzi/kafka:0.49.0-kafka-4.1.1
        imagePullPolicy: Never
        command: ["/bin/bash", "-c"]
        args:
        - |
          bin/kafka-producer-perf-test.sh \
            --topic spike-test \
            --num-records 500000 \
            --record-size 1024 \
            --throughput -1 \
            --producer-props \
              bootstrap.servers=krafter-kafka-bootstrap.kafka.svc:9092 \
              acks=all \
              batch.size=131072 \
              linger.ms=10
EOF
done

# Wait for burst to complete
kubectl wait --for=condition=complete --timeout=300s job/spike-burst-1 job/spike-burst-2 job/spike-burst-3 -n kafka || true

# Phase 3: recovery (back to baseline)
echo "=== Phase 3: Recovery baseline (1,000 msg/sec) ==="
kubectl exec -n kafka krafter-pool-alpha-0 -- \
  bin/kafka-producer-perf-test.sh \
    --topic spike-test \
    --num-records 60000 \
    --record-size 1024 \
    --throughput 1000 \
    --producer-props \
      bootstrap.servers=localhost:9092 \
      acks=all
```

**Metrics to watch:**
- Row 2 (Throughput): should show a flat line → sharp spike → flat line pattern
- Row 4 (Broker Internals): Request Handler Idle will dip during spike — how low?
- Row 3 (Replication): any ISR churn during the spike?

**Pass criteria:**
- No message loss across all 3 phases
- P99 latency during spike stays below 500ms
- Post-spike baseline performance matches pre-spike within 10%
- No under-replicated partitions during or after the spike

### 4. Endurance (Soak) Testing

**Goal:** Run the system under expected load for an extended period to detect problems that only emerge over time: memory leaks, GC degradation, log segment accumulation, file descriptor exhaustion, and performance drift.

**What it measures:**
- Throughput stability over hours (does it stay constant or decay?)
- Heap usage trend (sawtooth is healthy; climbing is a memory leak)
- GC pause duration trend (constant is good; growing means old-gen filling)
- Disk usage growth and log segment rolling behavior
- Thread count stability

**Scenario for krafter:** Produce at a steady 5,000 msg/sec for 1 hour (the default, can be extended to 48–72 hours for full soak). This is conservative enough that the 2Gi brokers should handle it indefinitely — the point is to watch for slow degradation.

```bash
# Automated via:
make test-endurance

# For a longer soak (adjust DURATION_MINUTES):
DURATION_MINUTES=2880 ./test-perf-endurance.sh  # 48 hours

# Or manually — 1-hour sustained test
kubectl exec -n kafka krafter-pool-alpha-0 -- \
  bin/kafka-producer-perf-test.sh \
    --topic endurance-test \
    --num-records 18000000 \
    --record-size 1024 \
    --throughput 5000 \
    --producer-props \
      bootstrap.servers=localhost:9092 \
      acks=all \
      batch.size=65536 \
      linger.ms=5 \
      compression.type=lz4
```

This builds on Chapter 8 Scenario 1 (Sustained Load Over Time) but adds formal structure and explicit degradation-detection criteria.

**What to watch during a soak — periodic checkpoints:**

Run this every 15 minutes during the soak to capture JVM state:

```bash
# Heap and GC snapshot
kubectl exec -n kafka krafter-pool-alpha-0 -- \
  bash -c 'echo "=== $(date) ==="; \
    echo "Heap:"; \
    cat /proc/$(pgrep -f kafka)/status | grep -E "VmRSS|VmSize"; \
    echo "Threads: $(ls /proc/$(pgrep -f kafka)/task | wc -l)"'
```

Or use the Grafana "Kafka JVM Metrics" dashboard to observe trends continuously.

**Pass criteria:**
- Throughput variance < 10% across the entire duration
- Heap usage peaks remain consistent (no upward trend)
- GC pause durations do not increase over time
- No under-replicated partitions
- Thread count remains stable (±5 threads)
- Disk usage grows linearly (no unexpected spikes from log segment issues)

### 5. Scalability Testing

**Goal:** Measure how performance changes when you scale system resources up (more memory/CPU per broker) or out (more partitions, more consumers). Scalability testing determines the relationship between resource investment and performance gain.

**What it measures:**
- Throughput improvement per additional partition
- Throughput improvement per additional consumer
- Effect of increased broker memory on latency
- Whether adding resources yields linear, sub-linear, or no improvement

**Scenario for krafter:** Test the same workload across increasing partition counts to measure horizontal scalability within the existing 3-broker cluster.

```bash
# Horizontal scalability: partitions
for PARTITIONS in 1 3 6 9 12; do
  TOPIC="scale-p${PARTITIONS}"
  kubectl exec -n kafka krafter-pool-alpha-0 -- \
    bin/kafka-topics.sh --create --if-not-exists \
      --bootstrap-server localhost:9092 \
      --topic $TOPIC --partitions $PARTITIONS --replication-factor 3

  echo "=== Partitions: $PARTITIONS ==="
  kubectl exec -n kafka krafter-pool-alpha-0 -- \
    bin/kafka-producer-perf-test.sh \
      --topic $TOPIC \
      --num-records 500000 \
      --record-size 1024 \
      --throughput -1 \
      --producer-props \
        bootstrap.servers=localhost:9092 \
        acks=all \
        batch.size=65536 \
        linger.ms=5

  sleep 15
done
```

**Consumer scalability:** Test how throughput scales with consumer count by deploying 1, 2, and 3 consumers against a 3-partition topic (see Chapter 5 — Consumer Parallelism for the theory).

**Vertical scalability (resource adjustment):** To test the effect of more memory, temporarily modify the `KafkaNodePool` resources in `config/kafka.yaml` (see Chapter 6 — Strimzi Resource Requests in Practice), run the same benchmark, and compare. Remember to restore original values afterward.

**Pass criteria:**
- Throughput scales at least 2× from 1 partition to 3 partitions
- Throughput scales at least 1.5× from 3 partitions to 9 partitions (sub-linear is expected)
- Beyond 12 partitions on this cluster, diminishing returns are expected (document the plateau)

### 6. Volume Testing

**Goal:** Test system performance when processing very large amounts of data. Volume testing is about data quantity — millions of records, large messages, or both — and its impact on disk, compaction, page cache, and segment management.

**What it measures:**
- Throughput degradation as total data volume grows
- Log segment rolling and compaction behavior
- Disk I/O patterns under heavy write load
- Impact of large messages on batch efficiency and memory pressure
- Consumer performance reading from large topics

**Scenario for krafter:** Produce 5 million records at 100KB each (≈500 GB before replication), then measure consumer throughput. On the krafter cluster with 10Gi per broker, this will fill the disks, so use short retention.

```bash
# Automated via:
make test-volume

# Or manually — create topic with short retention
kubectl exec -n kafka krafter-pool-alpha-0 -- \
  bin/kafka-topics.sh --create --if-not-exists \
    --bootstrap-server localhost:9092 \
    --topic volume-test \
    --partitions 3 \
    --replication-factor 3 \
    --config min.insync.replicas=2 \
    --config retention.ms=1800000 \
    --config max.message.bytes=1048576

# Large-message volume test (100KB per record)
echo "=== Volume test: 50,000 x 100KB ==="
kubectl exec -n kafka krafter-pool-alpha-0 -- \
  bin/kafka-producer-perf-test.sh \
    --topic volume-test \
    --num-records 50000 \
    --record-size 102400 \
    --throughput -1 \
    --producer-props \
      bootstrap.servers=localhost:9092 \
      acks=all \
      max.request.size=1048576 \
      batch.size=131072

# High-count volume test (5M x 1KB)
echo "=== Volume test: 5,000,000 x 1KB ==="
kubectl exec -n kafka krafter-pool-alpha-0 -- \
  bin/kafka-producer-perf-test.sh \
    --topic volume-test-count \
    --num-records 5000000 \
    --record-size 1024 \
    --throughput -1 \
    --producer-props \
      bootstrap.servers=localhost:9092 \
      acks=all \
      batch.size=131072 \
      linger.ms=10 \
      compression.type=lz4
```

**Metrics to watch:**
- Row 6 (Resource Usage): disk space growth per broker
- Row 7 (Test Topic Detail): log segment count and size per partition
- Row 5 (JVM): heap pressure from large batches
- Row 2 (Throughput): throughput curve — does it flatten as disk fills?

**Pass criteria:**
- Large messages (100KB): throughput ≥ 10 MB/sec sustained
- High count (5M × 1KB): no throughput degradation in the last 20% of the run compared to the first 20%
- Log segments roll correctly at `log.segment.bytes` boundary
- Disk usage stays within available capacity (retention policy handles cleanup)

### 7. Capacity Testing

**Goal:** Determine the absolute maximum sustained throughput and concurrent user count the system can handle before performance drops below acceptable thresholds. Unlike stress testing (which finds the breaking point), capacity testing finds the usable maximum — the highest load where SLAs are still met.

**What it measures:**
- Maximum sustained throughput where P99 latency stays below a defined threshold
- Maximum number of concurrent producers before latency degrades
- Effective cluster bandwidth ceiling
- The relationship between throughput and latency (the "latency knee")

**Scenario for krafter:** Use a binary-search approach to find the maximum throughput where P99 stays under 500ms. Start at 1,000 msg/sec, double until P99 exceeds threshold, then bisect to find the precise capacity point.

```bash
# Automated via:
make test-capacity

# Or manually — stepwise capacity discovery
for THROUGHPUT in 5000 10000 20000 40000 80000 -1; do
  echo "=== Capacity probe: $THROUGHPUT msg/sec ==="
  kubectl exec -n kafka krafter-pool-alpha-0 -- \
    bin/kafka-producer-perf-test.sh \
      --topic capacity-test \
      --num-records 200000 \
      --record-size 1024 \
      --throughput $THROUGHPUT \
      --producer-props \
        bootstrap.servers=localhost:9092 \
        acks=all \
        batch.size=65536 \
        linger.ms=5

  echo "--- Record the P99 latency from the output above ---"
  sleep 10
done
```

The first throughput level where P99 exceeds your threshold (e.g., 500ms) is your capacity ceiling. The level just below it is your maximum sustainable throughput.

**Multi-producer capacity:** Repeat the test with increasing producer counts (1, 2, 3, 5, 10 concurrent producers) to find the maximum concurrent producer count.

**Documenting the capacity envelope:**

After running capacity tests, fill in this table for your cluster:

| Metric | Value |
|--------|-------|
| Max throughput (single producer, P99 < 100ms) | ___ msg/sec |
| Max throughput (single producer, P99 < 500ms) | ___ msg/sec |
| Max throughput (5 producers, P99 < 500ms) | ___ msg/sec aggregate |
| Max concurrent producers (P99 < 500ms @ 10K msg/sec each) | ___ producers |
| Max consumer throughput (single consumer) | ___ MB/sec |
| Max sustained throughput (1-hour soak, P99 < 200ms) | ___ msg/sec |

This table becomes the cluster's performance specification and the basis for capacity planning decisions.

### 8. Round-Trip Testing

**Goal:** Measure the true end-to-end latency from the moment a record is produced to the moment it is consumed. Unlike separate producer/consumer perf tests, round-trip testing uses a single pipeline that timestamps each record and measures the actual deliver-through latency.

**What it measures:**
- Produce-to-consume latency (P50, P95, P99)
- End-to-end throughput through the entire pipeline
- The gap between producer-side latency and true delivery latency
- Impact of consumer group rebalances on end-to-end timing

**When to use:** When your application cares about how fast a consumer sees a produced message, not just how fast the producer gets an ack. This is critical for event-driven architectures, CDC pipelines, and stream processing.

**Scenario for krafter:** Produce 100,000 records at a controlled rate, consume them from the same topic, and measure per-record round-trip time.

```bash
# Scaffold a round-trip test:
kates test scaffold --type ROUND_TRIP -o round-trip.yaml

# Run it:
kates test apply -f round-trip.yaml --wait
```

The KATES CLI uses the ROUND_TRIP workload, which embeds a nanosecond timestamp in each record's payload. The consumer extracts the timestamp and computes the delivery latency per record, then aggregates into percentiles.

**Pass criteria:**
- P99 round-trip latency < 200ms (with `acks=all`, RF=3, ISR=2)
- No records lost in transit
- Consumer lag returns to 0 within 30 seconds after producers finish

### 9. Data Integrity Testing

**Goal:** Verify that every record produced is consumed exactly once, without corruption, in the correct order, and within acceptable recovery time bounds. Integrity testing goes beyond throughput and latency — it answers the question: "did the cluster lose, corrupt, duplicate, or reorder any data?"

**What it measures:**
- **Data loss** — records that were acked by the broker but never consumed
- **Duplicates** — records consumed more than once
- **CRC32 corruption** — payload checksum mismatches indicating bit-level corruption
- **Ordering violations** — records arriving out of sequence within a partition
- **RTO (Recovery Time Objective)** — how long write/read availability was interrupted
- **RPO (Recovery Point Objective)** — how much data was at risk during a failure window

**How it works:** The KATES native backend implements a produce→consume→verify pipeline:

```
Producer                          Kafka                         Consumer
┌─────────────┐                ┌─────────┐                ┌──────────────┐
│ Sequenced   │──── acks=all──▶│ Topic   │──── fetch ────▶│ Decode       │
│ Payload     │                │ (RF=3)  │                │ Verify CRC   │
│ ┌─────────┐ │                └─────────┘                │ Check Order  │
│ │ RunID   │ │                                           │ Track Seq    │
│ │ SeqNum  │ │                                           └──────┬───────┘
│ │ Stamp   │ │                                                  │
│ │ CRC32   │ │           ┌──────────────────────┐               │
│ │ Payload │ │           │ DataIntegrityVerifier │◀──────────────┘
│ └─────────┘ │           │ BitSet reconciliation │
└─────────────┘           │ LostRange detection   │
                          │ Timeline events        │
                          └──────────┬─────────────┘
                                     │
                              IntegrityResult
                           ┌──────────────────┐
                           │ totalSent         │
                           │ totalConsumed     │
                           │ lostRecords       │
                           │ crcFailures       │
                           │ outOfOrderCount   │
                           │ producerRtoMs     │
                           │ consumerRtoMs     │
                           │ rpoMs             │
                           │ verdict           │
                           │ timeline[]        │
                           └──────────────────┘
```

Each record carries a 28-byte header: 4-byte run ID hash, 8-byte sequence number, 8-byte nanosecond timestamp, 4-byte partition hint, and 4-byte CRC32 computed over the payload. The consumer decodes, verifies the CRC, checks per-partition ordering, and feeds sequence numbers into a `BitSet`-based reconciler that detects gaps (lost records) and overlaps (duplicates).

**Scenario for krafter:**

```bash
# Scaffold an integrity test:
kates test scaffold --type INTEGRITY -o integrity.yaml

# Run it with SLA enforcement:
kates test apply -f integrity.yaml --wait
```

The scaffold generates a ready-to-use YAML:

```yaml
scenarios:
  - name: "Data Integrity Baseline"
    type: INTEGRITY
    backend: native
    spec:
      records: 1000000
      parallelProducers: 1
      numConsumers: 1
      recordSizeBytes: 512
      topic: "integrity-test"
      acks: "all"
      partitions: 6
      replicationFactor: 3
      minInsyncReplicas: 2
      enableCrc: true
      enableIdempotence: true
    validate:
      maxDataLossPercent: 0
      maxRtoMs: 5000
      maxRpoMs: 1000
      maxOutOfOrder: 0
      maxCrcFailures: 0
```

**SLA validation gates:** The `validate` block defines pass/fail thresholds. After the test completes, the CLI checks each gate and either prints "✓ SLA Pass" or lists the violations and exits with code 1. This is suitable for CI/CD pipelines.

| Gate | What It Checks |
|------|----------------|
| `maxDataLossPercent` | % of acked records not consumed |
| `maxRtoMs` | Maximum acceptable recovery time |
| `maxRpoMs` | Maximum acceptable recovery point |
| `maxOutOfOrder` | Maximum per-partition ordering violations |
| `maxCrcFailures` | Maximum payload checksum mismatches |

**Producer modes:**

| Mode | Config | Effect |
|------|--------|--------|
| Standard | (default) | Fire-and-forget acks, no dedup |
| Idempotent | `enableIdempotence: true` | Exactly-once per partition via producer epoch |
| Transactional | `enableTransactions: true` | Atomic batch commits, consumer reads `read_committed` |

**Timeline events:** The integrity verifier records timestamped events during analysis:

| Type | When Emitted |
|------|--------------|
| `CRC_FAILURE` | A record's CRC32 doesn't match its payload |
| `OUT_OF_ORDER` | A record arrives before its expected sequence within a partition |
| `LOST_RANGE` | A contiguous range of acked sequences was never consumed |
| `SUMMARY` | Final verdict with aggregate lost/duplicate counts |

The CLI displays the last 20 timeline events in a table after the integrity result panel. The report generator includes up to 50 events in the exported markdown.

**Pass criteria (baseline — no chaos):**
- Zero data loss (every acked record consumed)
- Zero CRC failures
- Zero ordering violations
- Verdict = `PASS`

**Pass criteria (with chaos):**
- Data loss ≤ threshold (depends on chaos type and `acks` setting)
- RTO < 10,000ms (cluster recovers within 10 seconds)
- RPO < 5,000ms (at most 5 seconds of data at risk)
- Zero CRC failures (corruption is never acceptable)

### Choosing the Right Test Type

Use this flowchart to decide which test type to run:

```
What do you need to know?
    │
    ├─ "Can it handle our expected traffic?"
    │   └─ Load Testing (Section 1)
    │
    ├─ "Where does it break?"
    │   └─ Stress Testing (Section 2)
    │
    ├─ "Can it handle sudden surges?"
    │   └─ Spike Testing (Section 3)
    │
    ├─ "Will it degrade over time?"
    │   └─ Endurance Testing (Section 4)
    │
    ├─ "Does adding resources help?"
    │   └─ Scalability Testing (Section 5)
    │
    ├─ "Can it handle massive datasets?"
    │   └─ Volume Testing (Section 6)
    │
    ├─ "What's the absolute maximum?"
    │   └─ Capacity Testing (Section 7)
    │
    ├─ "What's the true produce→consume latency?"
    │   └─ Round-Trip Testing (Section 8)
    │
    └─ "Is every record accounted for?"
        └─ Data Integrity Testing (Section 9)
```

### Automation Scripts

Each test type has a corresponding script in the project root:

| Script | Makefile Target | Default Duration |
|--------|----------------|-----------------|
| `test-perf-load.sh` | `make test-load` | ~10 min |
| `test-perf-stress.sh` | `make test-stress` | ~15 min |
| `test-perf-spike.sh` | `make test-spike` | ~10 min |
| `test-perf-endurance.sh` | `make test-endurance` | ~60 min |
| `test-perf-volume.sh` | `make test-volume` | ~20 min |
| `test-perf-capacity.sh` | `make test-capacity` | ~15 min |

All scripts are configurable via environment variables (e.g., `PRODUCERS=5`, `DURATION_MINUTES=120`). Run with `--help` or read the script header for details.

**KATES CLI tests** (scaffold + apply workflow):

| Command | Test Type | What It Does |
|---------|-----------|-------------|
| `kates test scaffold --type LOAD` | Load | Scaffold a KATES load test YAML |
| `kates test scaffold --type STRESS` | Stress | Scaffold a stress-ramp scenario |
| `kates test scaffold --type SPIKE` | Spike | Scaffold a spike-burst scenario |
| `kates test scaffold --type ENDURANCE` | Endurance | Scaffold a long-running soak test |
| `kates test scaffold --type VOLUME` | Volume | Scaffold a high-volume data test |
| `kates test scaffold --type CAPACITY` | Capacity | Scaffold a binary-search capacity test |
| `kates test scaffold --type ROUND_TRIP` | Round-Trip | Scaffold a produce→consume latency test |
| `kates test scaffold --type INTEGRITY` | Integrity | Scaffold per-record reconciliation + CRC |
| `kates test scaffold --type INTEGRITY_CHAOS` | Integrity + Chaos | Scaffold integrity test with fault injection |

Generate a YAML, then run it:

```bash
kates test scaffold --type INTEGRITY -o integrity.yaml
kates test apply -f integrity.yaml --wait
```

The `--wait` flag blocks until completion and runs SLA validation. Exit code 0 = all SLAs passed; exit code 1 = violations detected.

---

## Chapter 12: KATES CLI-Driven Testing

The previous chapters cover manual performance testing using raw Kafka tools (`kafka-producer-perf-test.sh`, `kafka-consumer-perf-test.sh`) and infrastructure-level chaos experiments. KATES (Kafka Advanced Testing & Engineering Suite) provides a higher-level abstraction: **declarative test scenarios** that combine workload specification, execution, monitoring, integrity verification, and SLA enforcement into a single workflow.

### Why KATES?

Manual perf tests give you raw numbers. KATES gives you **verdicts**.

| Capability | Manual Tools | KATES |
|------------|-------------|-------|
| Throughput measurement | ✓ | ✓ |
| Latency percentiles | ✓ | ✓ |
| Per-record data integrity | ✗ | ✓ |
| CRC32 corruption detection | ✗ | ✓ |
| Per-partition ordering verification | ✗ | ✓ |
| Automated SLA pass/fail | ✗ | ✓ |
| CI/CD exit codes | ✗ | ✓ |
| Report generation | ✗ | ✓ |
| Chaos + integrity combined | Manual setup | Declarative YAML |

### The Scaffold → Apply → Validate Workflow

Every KATES test follows a three-step workflow:

```
1. Scaffold       Generate a YAML test definition
   kates test scaffold --type INTEGRITY -o test.yaml

2. Apply          Submit to the KATES backend and execute
   kates test apply -f test.yaml --wait

3. Validate       Automatic SLA checks on completion
   ✓ SLA Pass  (exit 0)  or  ✗ Violations listed (exit 1)
```

### Available Test Types

| Type | Backend | What It Does |
|------|---------|-------------|
| `LOAD` | trogdor/native | Sustained producer throughput at target rate |
| `STRESS` | trogdor/native | Ramping throughput to find breaking point |
| `SPIKE` | trogdor/native | Sudden burst followed by recovery |
| `ENDURANCE` | trogdor/native | Extended soak for degradation detection |
| `VOLUME` | trogdor/native | Large dataset processing |
| `CAPACITY` | trogdor/native | Binary-search for maximum throughput |
| `ROUND_TRIP` | native | Produce→consume latency measurement |
| `INTEGRITY` | native | Per-record reconciliation + CRC + ordering |
| `INTEGRITY_CHAOS` | native | Integrity test with fault injection |

The `native` backend is required for INTEGRITY and ROUND_TRIP because these need per-record sequencing that the Trogdor backend does not support.

### YAML Anatomy

A test definition has three sections: `spec` (workload), optional `chaos` (fault injection), and optional `validate` (SLA gates).

```yaml
scenarios:
  - name: "Descriptive name"
    type: INTEGRITY              # Test type from the table above
    backend: native              # "native" or "trogdor"
    spec:
      records: 1000000           # Total records to produce
      parallelProducers: 1       # Concurrent producer count
      numConsumers: 1            # Consumer count
      recordSizeBytes: 512       # Payload size per record
      durationSeconds: 300       # Max duration (0 = unlimited)
      topic: "my-test"           # Kafka topic name
      acks: "all"                # Producer acks setting
      batchSize: 65536           # Producer batch size
      lingerMs: 5                # Producer linger
      compressionType: "lz4"     # Compression codec
      consumerGroup: "my-cg"     # Consumer group ID
      partitions: 6              # Topic partitions
      replicationFactor: 3       # Topic replication factor
      minInsyncReplicas: 2       # Topic min ISR
      enableCrc: true            # CRC32 per-record checksums
      enableIdempotence: true    # Idempotent producer
      enableTransactions: false  # Transactional producer
    chaos:                       # Optional: fault injection
      experimentName: "kafka-broker-kill"
      chaosDurationSec: 30
      steadyStateSec: 60
    validate:                    # Optional: SLA gates
      maxP99Latency: 100         # Max P99 latency (ms)
      maxAvgLatency: 50          # Max average latency (ms)
      minThroughput: 10000       # Min throughput (records/sec)
      maxErrorRate: 0            # Max error rate (%)
      maxDataLossPercent: 0      # Max data loss (%)
      maxRtoMs: 5000             # Max recovery time (ms)
      maxRpoMs: 1000             # Max recovery point (ms)
      maxOutOfOrder: 0           # Max ordering violations
      maxCrcFailures: 0          # Max CRC mismatches
```

### SLA Validation

The `validate` block supports 9 gates split into two categories:

**Performance gates:**

| Gate | Threshold | Violation Example |
|------|-----------|-------------------|
| `maxP99Latency` | P99 latency in ms | `p99=142ms > 100ms` |
| `maxAvgLatency` | Average latency in ms | `avg=65ms > 50ms` |
| `minThroughput` | Minimum records/sec | `throughput=8200 < 10000` |
| `maxErrorRate` | Error percentage | `errors=2.5% > 0%` |

**Integrity gates:**

| Gate | Threshold | Violation Example |
|------|-----------|-------------------|
| `maxDataLossPercent` | % of acked records not consumed | `dataLoss=0.02% > 0%` |
| `maxRtoMs` | Recovery time in ms | `rto=12000ms > 10000ms` |
| `maxRpoMs` | Recovery point in ms | `rpo=6000ms > 5000ms` |
| `maxOutOfOrder` | Ordering violations | `outOfOrder=3 > 0` |
| `maxCrcFailures` | Checksum mismatches | `crcFailures=1 > 0` |

When all gates pass, the CLI prints `✓ SLA Pass`. When any gate is violated, it prints each violation and exits with code 1. This makes KATES tests suitable for CI/CD pipelines where a non-zero exit code blocks the pipeline.

### Integrity Result Display

After an INTEGRITY test completes, the CLI displays a detailed panel:

```
  Data Integrity
    Sent          1,000,000
    Acked         1,000,000
    Consumed      1,000,000
    Lost          0
    Duplicates    0
    Data Loss     0.0000%
    Producer RTO  0 ms
    Consumer RTO  0 ms
    CRC Failures  0
    Out of Order  0
    Mode          idempotent
    Verdict       ✓ PASS
```

If timeline events were recorded (CRC failures, ordering violations, or lost ranges), the CLI displays the last 20 in a table:

```
  Integrity Timeline
    ┌───────────────┬──────────────┬──────────────────────────────┐
    │ Timestamp     │ Type         │ Detail                       │
    ├───────────────┼──────────────┼──────────────────────────────┤
    │ 1707933612345 │ CRC_FAILURE  │ partition=2 seq=48721        │
    │ 1707933612890 │ OUT_OF_ORDER │ partition=1 expected=5 got=3 │
    │ 1707933650000 │ LOST_RANGE   │ from=48722 to=48730 count=9  │
    │ 1707933660000 │ SUMMARY      │ verdict=DATA_LOSS lost=9     │
    └───────────────┴──────────────┴──────────────────────────────┘
```

### Report Export

The KATES backend generates a markdown report for each completed test. For tests with integrity results, the report includes a "Data Integrity" section with a full metrics table and up to 50 timeline events.

### Practical Examples

**Baseline integrity (no chaos):**
```bash
kates test scaffold --type INTEGRITY -o baseline.yaml
kates test apply -f baseline.yaml --wait
```

**Integrity under broker kill:**
```bash
kates test scaffold --type INTEGRITY_CHAOS -o chaos.yaml
kates test apply -f chaos.yaml --wait
```

**CI/CD pipeline integration:**
```bash
#!/bin/bash
set -e
kates test scaffold --type INTEGRITY -o /tmp/integrity.yaml
kates test apply -f /tmp/integrity.yaml --wait
# Exit code 1 if any SLA is violated — pipeline fails automatically
```

**Compare producer modes:**
```bash
# Standard (no dedup)
kates test scaffold --type INTEGRITY -o standard.yaml
# Edit: set enableIdempotence: false
kates test apply -f standard.yaml --wait

# Idempotent
kates test scaffold --type INTEGRITY -o idempotent.yaml
kates test apply -f idempotent.yaml --wait

# Compare data loss and duplicate counts between the two runs
```

### Context Management

KATES uses a kubectl-style context system stored in `~/.kates.yaml`. This lets you switch between local, staging, and production environments without re-typing URLs.

| Command | Description |
|---------|-------------|
| `kates ctx show` | List all contexts with active marker (`→`) |
| `kates ctx set <name> --url <url>` | Create or update a context |
| `kates ctx use <name>` | Switch active context |
| `kates ctx current` | Print active context name and URL |
| `kates ctx delete <name>` | Remove a context |

**Quick start example:**
```bash
kates ctx set local --url http://localhost:30083
kates ctx use local
kates ctx set staging --url https://kates-staging.company.com --output json
kates ctx show
```

**Config file structure (`~/.kates.yaml`):**
```yaml
current-context: local
contexts:
  local:
    url: http://localhost:30083
    output: table
  staging:
    url: https://kates-staging.company.com
    output: json
```

### Cluster Inspection

Inspect Kafka cluster state without leaving the terminal. All commands support `-o json` for scripting.

| Command | Description |
|---------|-------------|
| `kates cluster info` | Cluster metadata: ID, controller, broker table (ID, host, port, rack, role) |
| `kates cluster topics` | List all Kafka topics |
| `kates cluster topics describe <topic>` | Topic detail: partitions, replication factor, configs, per-partition ISR/leader, under-replicated flags |
| `kates cluster groups` | List consumer groups with state and member count |
| `kates cluster groups describe <group>` | Per-partition lag table (topic, partition, current, end, lag) |
| `kates cluster broker configs <id>` | Non-default broker configuration, grouped by source (STATIC_BROKER, DYNAMIC, etc.) |
| `kates cluster check` | Full cluster health check: broker count, partition health, under-replicated/offline partitions, problems list |
| `kates cluster watch` | Live-refresh cluster health with sparkline trends (under-replicated, offline, partitions) |

**Example — diagnosing under-replicated partitions:**
```bash
kates cluster check                     # Overall health report
kates cluster topics describe my-topic  # Which partitions are under-replicated?
kates cluster broker configs 0          # Is min.insync.replicas configured?
kates cluster watch --interval 5        # Watch partition health in real-time
```

**Example — consumer group lag investigation:**
```bash
kates cluster groups                       # Find group ID
kates cluster groups describe my-consumer  # Per-partition lag with color-coded high-lag cells
```

### Test Lifecycle

Create, run, and monitor performance tests.

| Command | Description | Key Flags |
|---------|-------------|-----------|
| `kates test create` | Create and run a test | `--type`, `--records`, `--partitions`, `--acks` |
| `kates test scaffold` | Generate a YAML test spec | `--type` (LOAD, STRESS, SPIKE, ENDURANCE, VOLUME, CAPACITY, ROUND_TRIP, INTEGRITY, INTEGRITY_CHAOS) |
| `kates test apply` | Submit a YAML spec to the engine | `-f`, `--wait` |
| `kates test list` | List all test runs | `--type`, `--status`, `--page`, `--size` |
| `kates test list watch` | Auto-refresh test list | `--interval` |
| `kates test get <id>` | Show test details | |
| `kates test watch <id>` | Live-watch a running test until completion | `--interval` |
| `kates test status <id>` | Check SLA pass/fail status | |
| `kates test delete <id>` | Delete a test run | |

**Workflow — imperative test run:**
```bash
kates test create --type LOAD --records 500000 --partitions 6 --acks all --wait
```

**Workflow — YAML-driven test run:**
```bash
kates test scaffold --type ENDURANCE -o endurance.yaml
# Edit YAML to customize spec, chaos, and validation blocks
kates test apply -f endurance.yaml --wait
```

**Workflow — watch a running test:**
```bash
kates test list          # Find the run ID
kates test watch abc123  # Live view: status, phases, throughput, latency
```

### Reporting & Analysis

View, compare, and export test results.

| Command | Description | Key Flags |
|---------|-------------|-----------|
| `kates report show <id>` | Full report: throughput, latency distribution (bar chart), error rate, SLA verdict | |
| `kates report summary <id>` | Compact metrics table | |
| `kates report diff <id1> <id2>` | Side-by-side metric comparison with delta percentages and ▲/▼ indicators | |
| `kates report compare <id1,id2,...>` | Compare metrics across multiple runs | |
| `kates report export <id>` | Export to file or stdout | `--format csv\|junit` |

**Example — regression detection between two runs:**
```bash
$ kates report diff abc123 def456

  ╔══════════════════════════════════╗
  ║          Report Diff             ║
  ╟──────────────────────────────────╢
  ║  abc123 vs def456                ║
  ╚══════════════════════════════════╝

  ┌─────────────────┬───────────┬───────────┬─────────┬───┐
  │ Metric          │ abc123    │ def456    │ Delta   │   │
  ├─────────────────┼───────────┼───────────┼─────────┼───┤
  │ Avg Throughput  │ 45,231    │ 42,100    │ -6.9%   │ ▼ │
  │ P99 Latency     │ 12.5 ms   │ 18.3 ms   │ +46.4%  │ ▼ │
  │ Error Rate      │ 0.00%     │ 0.00%     │ =       │ = │
  └─────────────────┴───────────┴───────────┴─────────┴───┘
```

**Example — CI artifact export:**
```bash
kates report export abc123 --format junit > test-results.xml
kates report export abc123 --format csv > metrics.csv
```

When piped (non-terminal), output goes to stdout; when run interactively, it writes to `kates-report-<id>.csv` or `kates-report-<id>.xml`.

### Observability

Real-time monitoring tools for live clusters and running tests.

**`kates dashboard` (alias: `dash`)**

Full-screen TUI with auto-refresh. Displays:
- System Health panel (API, Kafka, engine, config count)
- Test Summary panel (running, pending, done, failed counts)
- Active Tests panel (per-test throughput and latency)
- Recent Completed panel (last 5 finished tests)
- Sparkline charts: Throughput ↗ and P99 Latency ↘ trends

```bash
kates dashboard --interval 3   # Refresh every 3 seconds
kates dash                     # Short alias
```

**`kates top`**

Like `kubectl top` but for KATES tests. Shows a one-liner status bar plus active/recent test tables with throughput and latency columns.

```bash
kates top --interval 5
```

**`kates status`**

Single-line system status. Perfect for shell prompts or quick checks:

```
  ✓ local │ UP │ Kafka ✓ │ 4 configs │ 1 running │ 12 done │ 0 failed
```

**`kates trend`**

Historical metric analysis with sparkline charts and automatic regression detection.

```bash
kates trend --type LOAD --metric p99LatencyMs --days 30
kates trend --type ENDURANCE --metric avgThroughputRecPerSec --days 7 --baseline 3
```

| Flag | Default | Description |
|------|---------|-------------|
| `--type` | (required) | Test type to analyze |
| `--metric` | `avgThroughputRecPerSec` | Metric to plot |
| `--days` | `30` | Look-back window |
| `--baseline` | `5` | Number of recent runs for baseline calculation |

Output includes: sparkline with trend direction (↗/→/↘), min/max/average statistics, per-run data points, and any detected regressions (>20% deviation from baseline).

### Resilience Testing

Correlate performance metrics with chaos experiments in a single run.

```bash
kates resilience run --config resilience-test.json
```

**Config file format (`resilience-test.json`):**
```json
{
  "testRequest": {
    "testType": "LOAD",
    "spec": { "records": 100000 }
  },
  "chaosSpec": {
    "experimentName": "kafka-pod-kill",
    "targetNamespace": "kafka"
  },
  "steadyStateSec": 30
}
```

**Output includes:**
- Chaos Outcome (experiment, verdict, duration)
- Impact Analysis (per-metric % change with ▲/▼ markers)
- Pre-Chaos Baseline vs Post-Chaos Impact (throughput, P99 latency, error rate)

This differs from `kates test apply` with `INTEGRITY_CHAOS` in that `resilience run` focuses on **performance impact measurement** (before vs after), while `INTEGRITY_CHAOS` focuses on **per-record data loss and corruption detection**.

### Scheduling

Automate recurring test runs with cron expressions.

| Command | Description |
|---------|-------------|
| `kates schedule list` | List all schedules (ID, name, cron, state, last run) |
| `kates schedule get <id>` | Show schedule details |
| `kates schedule create` | Create a new scheduled test |
| `kates schedule delete <id>` | Remove a schedule |

**Example — hourly smoke test and nightly endurance:**
```bash
# Create a request file
cat > load-test.json << 'EOF'
{
  "testType": "LOAD",
  "spec": { "records": 10000, "partitions": 3 }
}
EOF

kates schedule create --name "Hourly Smoke" --cron "0 * * * *" --request load-test.json
kates schedule create --name "Nightly Endurance" --cron "0 2 * * *" --request endurance.json
kates schedule list
```

---

## Quick Reference

### Commands

| Command | What It Does |
|---------|-------------|
| `make test` | Run the standard 1M-message perf test |
| `kubectl exec -n kafka krafter-pool-alpha-0 -- bin/kafka-producer-perf-test.sh ...` | Custom producer perf test |
| `kubectl exec -n kafka krafter-pool-alpha-0 -- bin/kafka-consumer-perf-test.sh ...` | Custom consumer perf test |
| `kates health` | System health and Kafka connectivity |
| `kates status` | One-line system status (for scripts/prompts) |
| `kates cluster info` | Cluster metadata and broker table |
| `kates cluster topics describe <topic>` | Topic config, partitions, ISR detail |
| `kates cluster groups describe <group>` | Consumer group per-partition lag |
| `kates cluster broker configs <id>` | Non-default broker configuration |
| `kates cluster check` | Full cluster health check |
| `kates cluster watch` | Live cluster health dashboard |
| `kates test scaffold --type <TYPE> -o test.yaml` | Generate a KATES test definition |
| `kates test apply -f test.yaml --wait` | Run a KATES test with SLA validation |
| `kates test list` | List recent test runs |
| `kates test watch <id>` | Live-watch a running test |
| `kates report show <id>` | Full performance report |
| `kates report diff <id1> <id2>` | Side-by-side run comparison |
| `kates report export <id> --format csv\|junit` | Export report |
| `kates trend --type <TYPE> --metric <metric>` | Historical trend with spark charts |
| `kates resilience run --config <file>` | Performance + chaos correlation test |
| `kates schedule create --name <n> --cron <expr> --request <file>` | Recurring test schedule |
| `kates dashboard` | Full-screen monitoring TUI |
| `kates top` | Live test activity view |

### Monitoring

| Dashboard | URL | Use |
|-----------|-----|-----|
| Grafana | http://localhost:30080 | All dashboards (admin/admin) |
| Kafka UI | http://localhost:30081 | Topics, partitions, consumer groups |

Use the **"Kafka Performance Testing"** dashboard in Grafana during all perf tests — it consolidates throughput, replication, broker internals, JVM, and per-topic detail into a single view. See the Dashboard Panel Reference appendix below.

### Key Files

| File | Purpose |
|------|---------|
| `config/kafka.yaml` | Kafka cluster + node pool definitions |
| `config/kafka-metrics.yaml` | JMX exporter rules for Prometheus |
| `config/kafka-perf-global-dashboard.yaml` | Unified performance testing Grafana dashboard |
| `config/monitoring.yaml` | Prometheus + Grafana Helm values |
| `test-kafka-performance.sh` | Built-in 1M-message perf test |
| `config/storage-classes.yaml` | Zone-pinned StorageClasses |

---

## Appendix: Performance Testing Dashboard Reference

The **"Kafka Performance Testing"** dashboard (`config/kafka-perf-global-dashboard.yaml`) is a single-pane view designed for use during all performance tests. It has a `$topic` template variable — select your test topic from the dropdown to populate the "Test Topic Detail" row.

Open it at: **Grafana → Dashboards → Kafka Performance Testing**

### Row 1 — Cluster Health

Four stat panels that should always be green during a healthy test.

| Panel | Metric | Healthy Value | Alert Threshold | Action |
|-------|--------|---------------|-----------------|--------|
| Active Brokers | `kafka_controller_kafkacontroller_activebrokercount` | 3 | < 3 (red) | Check pod status: `kubectl get pods -n kafka` |
| Offline Partitions | `kafka_controller_kafkacontroller_offlinepartitionscount` | 0 | ≥ 1 (red) | Partitions with no available leader — immediate data unavailability |
| Under-Replicated Partitions | `sum(kafka_server_replicamanager_underreplicatedpartitions)` | 0 | ≥ 1 (red) | Followers can't keep up — check follower broker JVM/network |
| Active Controller | `kafka_controller_kafkacontroller_activecontrollercount` | 1 | ≠ 1 (red) | Must always be exactly 1. If 0, no metadata operations are possible |

### Row 2 — Throughput

The core performance panels. These metrics were previously exported by JMX but not visualized in any dashboard.

| Panel | Metric | What It Shows | Reading It |
|-------|--------|---------------|------------|
| Bytes In / sec | `kafka_server_brokertopicmetrics_bytesinpersec` | Producer write throughput per broker | Lines should be roughly equal across brokers (balanced partitions). A flat line at zero means no produce traffic. |
| Bytes Out / sec | `kafka_server_brokertopicmetrics_bytesoutpersec` | Consumer + replication read throughput | Higher than Bytes In because it includes follower replication fetches. With RF=3, expect ~3× Bytes In. |
| Messages In / sec | `kafka_server_brokertopicmetrics_messagesinpersec` | Message rate per broker | Divide by Bytes In to get average message size. |
| Total Cluster Throughput | Stacked BytesIn + BytesOut | Aggregate I/O across all brokers | The total envelope of cluster traffic. Useful for capacity planning. |

**During a 1M-message perf test**, expect:
- Bytes In: 30–50 MB/sec per broker (varies with batch size and record size)
- Messages In: 10,000–20,000 msg/sec per broker
- Bytes Out: 2–3× Bytes In (replication plus any consumer traffic)

### Row 3 — Replication

Critical during chaos testing and sustained load scenarios.

| Panel | Metric | What It Shows | Reading It |
|-------|--------|---------------|------------|
| ISR Shrinks / Expands | `kafka_server_replicamanager_isrshrinkspersec` / `isrexpandspersec` | Rate of ISR membership changes | Should be 0 during normal operation. Spikes during broker restarts or chaos experiments are expected. Sustained shrinks without matching expands = problem. |
| Under-Replicated (over time) | `kafka_server_replicamanager_underreplicatedpartitions` | Per-broker under-replicated count as a timeseries | Correlate with ISR shrinks. A spike that returns to 0 within 30s is a healthy recovery. Sustained > 0 means a follower is stuck. |
| Leader Count | `kafka_server_replicamanager_leadercount` | Number of partition leaders per broker | Should be roughly equal across 3 brokers. Skew > 2× indicates leader imbalance — run `kafka-leader-election.sh`. |

### Row 4 — Broker Internals

These panels detect thread saturation — the most common bottleneck under high load.

| Panel | Metric | Healthy Range | Danger Zone | Fix |
|-------|--------|---------------|-------------|-----|
| Request Handler Idle % | `kafka_server_kafkarequesthandlerpool_requesthandleravgidlepercent` | > 0.5 (50%+ idle) | < 0.3 | Increase `num.io.threads` in Kafka CR config |
| Network Processor Idle % | `kafka_network_socketserver_networkprocessoravgidlepercent` | > 0.5 | < 0.3 | Increase `num.network.threads` |
| Request / Response Queue Size | `kafka_network_requestchannel_requestqueuesize` / `responsequeuesize` | < 10 | > 50 and growing | Broker is saturated — reduce producer throughput or add resources |

**How to read during a test:** If throughput plateaus but latency climbs, check these panels. They tell you whether the bottleneck is in network I/O handling (Network Processor) or disk/computation (Request Handler).

### Row 5 — JVM

Three panels to detect garbage collection issues and memory pressure.

| Panel | Metric | What It Shows | Reading It |
|-------|--------|---------------|------------|
| JVM Heap (Used vs Committed) | `jvm_memory_used_bytes{area="heap"}` / `committed` | Heap usage and allocation | Used should follow a sawtooth pattern (GC cycles). If Used approaches Committed and stays there, heap is too small. |
| GC Collection Rate | `rate(jvm_gc_collection_seconds_count[1m])` | GC events per second | G1GC young collections at 1–5/sec is normal. Frequent old-gen collections (> 1/sec) indicate heap pressure. |
| Thread Count | `jvm_threads_current` | JVM thread count per broker | Should be stable (typically 80–150). Climbing threads may indicate a connection leak. |

### Row 6 — Resource Usage

OS-level resource consumption per broker.

| Panel | Metric | What It Shows | Reading It |
|-------|--------|---------------|------------|
| CPU Usage | `rate(process_cpu_seconds_total[1m])` | CPU cores consumed per broker | On Kind, a value of 0.5–1.0 cores is typical during perf tests. Sustained > 2 cores indicates CPU bottleneck. |
| Memory (RSS vs Heap) | `process_resident_memory_bytes` / `jvm_memory_used_bytes{area="heap"}` | Total process memory vs JVM heap | RSS - Heap = off-heap memory (page cache, direct buffers, metaspace). If RSS approaches the container limit, the broker risks OOMKill. |
| Network I/O | `rate(process_network_receive_bytes_total[1m])` / `transmit` | Network bytes in/out per broker | Correlates with the Throughput row but at the OS level. Useful for detecting if the bottleneck is in Kafka processing or in raw network capacity. |

### Row 7 — Test Topic Detail ($topic)

These panels are driven by the `$topic` template variable. Select your test topic from the dropdown at the top.

| Panel | Metric | What It Shows | Reading It |
|-------|--------|---------------|------------|
| Log End Offset | `kafka_log_log_logendoffset{topic="$topic"}` | Message count per partition per broker | During a produce test, lines should climb at a steady rate. Uneven rates across partitions indicate key-based partition skew. |
| Topic Size | `kafka_log_log_size{topic="$topic"}` | On-disk bytes per partition per broker | With RF=3, every partition appears on 3 brokers. Sizes should match across replicas. |
| Log Segments | `kafka_log_log_numlogsegments{topic="$topic"}` | Segment file count per partition | Segments roll at `log.segment.bytes` (default 1GB). If you see many segments, your test generated significant data. |
| Partition Detail | Same as Topic Size, table view | Tabular view of partition placement | Shows which broker hosts each partition replica and in which zone. Confirms rack-aware placement. |

### Using the Dashboard During a Performance Test

1. **Before the test:** Open the dashboard, select `$topic` (or `All`), confirm all 4 health stat panels are green
2. **During the test:** Focus on Row 2 (Throughput) to watch real-time performance. If throughput drops or latency spikes:
   - Check Row 4 (Broker Internals) for thread saturation
   - Check Row 3 (Replication) for ISR churn
   - Check Row 5 (JVM) for GC pressure
3. **After the test:** Switch to Row 7 (Test Topic Detail) to verify data distribution and partition balance
