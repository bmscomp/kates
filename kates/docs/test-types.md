# Test Types

Kates supports seven performance test types, each designed to stress different aspects of a Kafka cluster. Every test type encodes a specific testing philosophy—a hypothesis about what you are trying to learn—and translates that philosophy into concrete Kafka workloads through the `SpecFactory`. Understanding when and why to use each test type is as important as knowing how to configure it.

## LOAD — Steady-State Throughput

### Concept

A LOAD test measures the baseline throughput and latency characteristics of your Kafka cluster under a sustained, controlled workload. This is the most fundamental test type and the one you should always run first. Think of it as taking the cluster's vital signs before doing anything else.

The idea is simple: send a known number of messages at a known rate and measure what comes back. If you set the throughput to 50,000 messages per second and the cluster can sustain that rate with acceptable latency, you know your baseline. If latency spikes or throughput drops, you have a problem to investigate before moving on to more aggressive testing.

### How It Works

A LOAD test runs the following workload:

1. Creates a test topic with the configured partition count, replication factor, and min.insync.replicas
2. Spawns N producer tasks and M consumer tasks in parallel, where N and M are configurable (defaults: 1 producer, 1 consumer)
3. Each producer sends `numRecords / numProducers` messages at `throughput / numProducers` messages per second. The work is split evenly across producers so that total throughput across all producers equals the configured target.
4. Each consumer reads from the same topic with a shared consumer group, so messages are distributed across consumers via Kafka's standard partition assignment protocol
5. Each producer task reports its throughput (records/sec), average latency, P50/P95/P99/max latency, and total records sent
6. The test completes when all producers have sent their share of records

### When to Use

Run a LOAD test when you need to:

- **Establish a performance baseline** — before any configuration changes, hardware upgrades, or Kafka version upgrades, capture the cluster's current throughput and latency so you have a reference point for comparison
- **Validate hardware sizing** — after deploying a new cluster or adding brokers, confirm that the cluster can handle your expected workload
- **Compare configurations** — run the same LOAD test before and after a configuration change (e.g., changing `batch.size`, `compression.type`, or `linger.ms`) to measure the impact
- **Regression testing** — include a LOAD test in your CI pipeline to catch performance regressions introduced by application changes

### Trogdor Mapping

When using the Trogdor backend, a LOAD test generates:
- N × `ProduceBenchSpec` — one per producer task
- M × `ConsumeBenchSpec` — one per consumer task

All tasks start simultaneously and run in parallel.

### Example

```bash
curl -X POST http://localhost:8080/api/tests \
  -H 'Content-Type: application/json' \
  -d '{
    "type": "LOAD",
    "spec": {
      "numProducers": 5,
      "numConsumers": 3,
      "numRecords": 1000000,
      "throughput": 50000,
      "recordSize": 1024
    }
  }'
```

This test sends 1 million records at 50,000 records/sec using 5 producers (each sending 200,000 records at 10,000 records/sec) and 3 consumers reading in parallel.

---

## STRESS — Ramp-Up Until Failure

### Concept

A STRESS test systematically increases the load on your Kafka cluster until it saturates. The goal is to find the throughput ceiling—the point at which the cluster cannot keep up with the incoming message rate, causing latency to spike, producer backpressure to engage, and potentially ISR shrinkage.

This is the test that answers the question: "How much load can my cluster handle before it breaks?" The answer is not a single number; it is a curve. By watching latency and throughput at each escalation step, you can identify the knee of the curve—the point at which performance degrades non-linearly—and use that as your practical throughput limit.

### How It Works

A STRESS test executes 5 sequential producer phases with increasing throughput:

| Step | Throughput | Purpose |
|------|-----------|---------|
| 1 | 10,000 msg/sec | Warm-up, establish baseline |
| 2 | 25,000 msg/sec | Moderate load, verify linear scaling |
| 3 | 50,000 msg/sec | Significant load, watch for latency inflection |
| 4 | 100,000 msg/sec | Heavy load, likely near saturation |
| 5 | Unlimited | Maximum throughput, push to failure |

Each step runs for `durationMs / 5` (e.g., if total duration is 15 minutes, each step runs for 3 minutes).

The STRESS test uses aggressive batching settings to maximize producer throughput:
- `batch.size=131072` (128 KB, double the default)
- `linger.ms=10` (double the default, allows more records to accumulate per batch)
- `partitions=6` (double the default, more parallelism)
- `numProducers=3` (triple the default, more concurrent writers)

These settings are intentionally tuned to push the broker, not the producer client. You want the producer to be as efficient as possible so that the bottleneck shows up in the broker.

### When to Use

Run a STRESS test when you need to:

- **Find the saturation point** — determine the maximum sustainable throughput before performance degrades
- **Identify bottlenecks** — observe which resource (network, disk, CPU) saturates first by correlating with broker metrics
- **Test broker backpressure handling** — verify that producers handle `TIMEOUT` and `NOT_ENOUGH_REPLICAS` errors gracefully when the cluster is overwhelmed
- **Capacity planning** — use the saturation point as the upper bound when sizing your cluster for production workloads with a safety margin

### Trogdor Mapping

5 × `ProduceBenchSpec` submitted sequentially, each with increasing throughput values.

### Interpreting Results

Look at the throughput and P99 latency for each step:

- If throughput scales linearly from step 1 through step 4 with consistent latency, your cluster has headroom
- If latency jumps sharply at step 3 or 4, that is your saturation point. Plan for sustained throughput below that level
- If step 5 (unlimited) shows throughput only marginally higher than step 4, you have found the actual ceiling
- If you see ISR shrinkage during any step, the cluster is critically overloaded at that throughput level

---

## SPIKE — Sudden Burst and Recovery

### Concept

A SPIKE test simulates a sudden, dramatic increase in traffic followed by a return to normal. This is the "flash sale" scenario—a calm system suddenly receives 10–100× its normal traffic for a short period, and you need to know: Will the cluster survive? Will it queue up and process the burst eventually? Will it lose messages? How long until latency returns to normal?

Unlike the STRESS test, which applies gradually increasing pressure, the SPIKE test transitions instantly from a minimal baseline to maximum throughput. This stresses different parts of the Kafka internals—particularly the request queue, the buffer pool, and the batch accumulator—because there is no warm-up period for the JVM's JIT compiler, caches, or OS page cache.

### How It Works

A SPIKE test runs in three distinct phases:

**Phase 1 — Baseline (60 seconds):** A single producer sends 1,000 messages per second. This establishes the "normal" state: low latency, low CPU usage, warm caches. The metrics captured during this phase serve as the baseline for measuring recovery.

**Phase 2 — Spike (120 seconds):** Three concurrent producers send messages at unlimited throughput—as fast as the broker will accept them. This is an instantaneous 100–1000× increase in traffic, depending on the cluster's capacity. During this phase, you expect to see latency spike, consumer lag increase, ISR potentially shrink if replication cannot keep up, and broker CPU and disk I/O saturate.

**Phase 3 — Recovery (60 seconds):** A single producer returns to 1,000 messages per second. The question is: how long does it take for latency to return to Phase 1 levels? Does consumer lag recover? Do ISR sets expand back to their full size?

Configuration overrides for SPIKE tests:
- `acks=1` — use weaker durability to avoid masking the spike's impact on replication
- `compression-type=none` — avoid compression overhead that could smooth out the spike
- `linger.ms=0` — send each batch immediately, do not wait to accumulate more records

### When to Use

Run a SPIKE test when you need to:

- **Simulate traffic bursts** — marketing campaigns, flash sales, Black Friday, viral events
- **Test ISR shrinkage and expansion** — verify that replicas fall out of sync and recover within acceptable time
- **Validate consumer lag recovery** — confirm that consumers can catch up after a burst
- **Measure recovery time** — quantify how long the cluster takes to return to normal after a spike
- **Test producer backpressure** — verify that producer's `buffer.memory` and `max.block.ms` handle the burst without throwing errors

### Trogdor Mapping

5 × `ProduceBenchSpec`: 1 baseline + 3 concurrent burst + 1 recovery.

---

## ENDURANCE — Long-Duration Soak Test

### Concept

An ENDURANCE test runs a sustained, moderate workload for an extended period—typically 1 hour or more. The goal is to detect time-dependent issues that only manifest after prolonged operation: memory leaks, garbage collection pressure, log segment accumulation, file handle exhaustion, or gradual performance degradation.

Short-duration tests tell you how the cluster performs in the short term. Endurance tests tell you whether that performance is sustainable. A cluster that handles 50,000 messages per second for 10 minutes might start showing GC pauses at the 30-minute mark, or run out of disk space at the 2-hour mark due to log segment accumulation.

### How It Works

An ENDURANCE test runs a single producer and a single consumer at a sustained rate for a long period:

- The minimum enforced duration is 1 hour. Even if you specify a shorter duration, the orchestrator overrides it to 3,600,000 ms. This ensures that time-dependent issues have enough time to manifest.
- The default throughput is 5,000 messages per second if not specified. This is intentionally moderate — the goal is not to maximize throughput but to sustain a realistic workload long enough to expose degradation.
- The total number of messages is calculated dynamically: `maxMessages = throughput × (durationMs / 1000)`. For the default configuration (5,000 msg/sec × 3,600 seconds), this means 18 million messages.
- A single consumer reads from the topic to verify that all produced messages are consumed and to measure end-to-end latency over time.

### When to Use

Run an ENDURANCE test when you need to:

- **Pre-production validation** — before promoting a cluster to production, run a soak test to confirm stability over hours
- **SLA verification** — confirm that the cluster maintains its throughput and latency guarantees over extended periods, not just during short bursts
- **JVM stability testing** — detect memory leaks, class loading issues, or GC pathologies that only appear after hours of operation
- **Log compaction behavior** — observe how log segment rotation, retention enforcement, and compaction interact under sustained load
- **Connection stability** — verify that Kafka client connections, Kubernetes service discovery, and DNS resolution remain stable over hours

### Trogdor Mapping

1 × `ProduceBenchSpec` + 1 × `ConsumeBenchSpec`, both running for the full duration.

---

## VOLUME — Large and Numerous Messages

### Concept

A VOLUME test stresses the cluster with extreme message sizes and counts. Kafka is designed for small, frequent messages (the "log" abstraction), but many real-world workloads involve large payloads (JSON documents, Avro records, protobuf-encoded events) or massive message volumes. This test reveals how the cluster handles edge cases in message size and count that standard benchmarks do not cover.

### How It Works

A VOLUME test runs two concurrent producer workloads on separate topics:

**Large Messages Workload:**
- 50,000 messages × 100 KB each = 5 GB total data
- Dedicated topic with suffix `-large`
- `max.request.size=1048576` (1 MB) to allow large messages
- Topic-level `max.message.bytes=1048576`
- `retention.ms=1800000` (30 minutes) to prevent disk exhaustion

**High Count Workload:**
- 5,000,000 messages × 1 KB each = 5 GB total data
- Dedicated topic with suffix `-count`
- Aggressive batching: `batch.size=262144` (256 KB), `linger.ms=50`
- Same retention and message size limits

Both workloads run at unlimited throughput to maximize disk I/O pressure.

Configuration overrides for VOLUME tests:
- `partitions=6` — more parallelism for handling large volumes
- `batch.size=262144` (256 KB) — accommodate large messages in batches
- `linger.ms=50` — wait longer to fill batches with large records
- `record.size=10240` (10 KB) — default record size for volume tests is larger

### When to Use

Run a VOLUME test when you need to:

- **Test message size limits** — verify that the cluster's `message.max.bytes` and `max.request.size` settings are compatible with your largest payloads
- **Test broker memory pressure** — large messages consume more buffer memory on both the producer and broker side
- **Test disk I/O patterns** — large messages produce larger log segments and different I/O patterns than small messages
- **Test segment rotation** — high message volumes trigger log segment rotation more frequently, which involves disk operations and potential performance impact
- **Validate retention policies** — verify that time-based or size-based retention correctly cleans up large volumes of data

### Trogdor Mapping

2 × `ProduceBenchSpec` on separate topics (different record sizes, same total data volume).

---

## CAPACITY — Maximum Throughput Discovery

### Concept

A CAPACITY test systematically probes the cluster to find its maximum sustainable throughput. Unlike the STRESS test, which uses aggressive batching to push the cluster as hard as possible, the CAPACITY test uses default producer configurations. This gives you a realistic picture of what throughput your actual applications—using normal producer settings—can achieve.

Think of STRESS as "how fast can the cluster possibly go?" and CAPACITY as "how fast will it go in practice?"

### How It Works

A CAPACITY test runs 6 sequential probes with doubling throughput:

| Probe | Throughput | Purpose |
|-------|-----------|---------|
| 1 | 5,000 msg/sec | Baseline, establish minimum |
| 2 | 10,000 msg/sec | 2× baseline |
| 3 | 20,000 msg/sec | 4× baseline |
| 4 | 40,000 msg/sec | 8× baseline |
| 5 | 80,000 msg/sec | 16× baseline |
| 6 | Unlimited | Maximum achievable |

Each probe runs for `durationMs / 6` (e.g., if total duration is 20 minutes, each probe runs for ~3.3 minutes).

Configuration overrides for CAPACITY tests:
- `partitions=12` — high parallelism for accurate capacity measurement
- `numProducers=5` — multiple producers to avoid client-side bottlenecks
- `durationMs=1200000` (20 minutes) — each probe needs enough time to stabilize

### When to Use

Run a CAPACITY test when you need to:

- **Capacity planning** — you need to provision hardware for a specific throughput target and want to know how much the current cluster can handle
- **Hardware procurement** — you are comparing different machine types (CPU cores, disk types, network bandwidth) and need an apples-to-apples throughput comparison
- **Before/after comparison** — you changed a broker configuration (e.g., `num.io.threads`, `num.network.threads`, `log.flush.interval.messages`) and want to measure the impact on maximum throughput
- **Growth planning** — you need to extrapolate when you will outgrow your current cluster based on your traffic growth rate

### Interpreting Results

The key metric to watch is the relationship between throughput and P99 latency across probes:

- **Linear scaling** — throughput doubles at each probe with stable latency → the cluster has headroom, the tested level is well within capacity
- **Latency knee** — throughput increases but P99 latency starts climbing non-linearly → this is the saturation point. Plan for sustained throughput at or below the previous probe level
- **Throughput plateau** — throughput at probe N is only marginally higher than probe N-1 → you have found the ceiling
- **Degradation** — throughput at a higher probe is actually lower than the previous probe → the cluster is severely overloaded, backpressure or ISR shrinkage is limiting throughput

### Trogdor Mapping

6 × `ProduceBenchSpec` submitted sequentially with doubling throughput.

---

## ROUND_TRIP — End-to-End Latency

### Concept

A ROUND_TRIP test measures the complete produce-to-consume latency through the Kafka cluster. While producer benchmarks measure produce latency (time from `producer.send()` to broker acknowledgment), they do not measure the full journey: produce → broker replication → consumer fetch → application delivery. The round-trip latency includes all of these components and is the metric that matters most for latency-sensitive applications.

This test is indispensable for SLA validation. If your SLA promises "end-to-end message delivery within 50ms at P99," you need to measure the actual round-trip latency under realistic conditions to know whether you can meet that promise.

### How It Works

A ROUND_TRIP test uses Trogdor's `RoundTripWorkloadSpec`, which is a specialized workload that:

1. Produces a message with a unique identifier and a timestamp
2. Immediately starts consuming from the same topic
3. Matches produced messages with consumed messages by identifier
4. Computes the time difference between production and consumption as the round-trip latency
5. Aggregates latency into percentile distributions: P50, P95, P99, P99.9, max

The default throughput is 1,000 messages per second if not specified. This is intentionally low because round-trip measurements are most meaningful under moderate load. Under extreme load, round-trip latency is dominated by queueing effects, not by Kafka's inherent replication and delivery latency.

Configuration overrides for ROUND_TRIP tests:
- `batch.size=16384` (16 KB) — smaller batches reduce batching latency
- `linger.ms=0` — send each batch immediately, do not wait for more records
- `compression-type=none` — avoid compression/decompression overhead
- `throughput=10000` — moderate rate for accurate latency measurement

### When to Use

Run a ROUND_TRIP test when you need to:

- **SLA validation** — verify that end-to-end latency meets your contractual obligations
- **Replication latency measurement** — measure the impact of `acks=all` vs `acks=1` on end-to-end latency
- **Cross-zone latency** — if your brokers are spread across availability zones, measure the impact of cross-zone replication
- **Configuration tuning** — measure how changes to `batch.size`, `linger.ms`, `compression.type`, and `num.io.threads` affect end-to-end latency
- **Broker comparison** — compare latency across different broker hardware, JVM versions, or Kafka versions

### Example

```bash
curl -X POST http://localhost:8080/api/tests \
  -H 'Content-Type: application/json' \
  -d '{
    "type": "ROUND_TRIP",
    "spec": {
      "throughput": 500,
      "numRecords": 100000,
      "acks": "all"
    }
  }'
```

### Trogdor Mapping

1 × `RoundTripWorkloadSpec`.

## Test Spec Parameters

All parameters are configurable at three levels, in priority order:

1. **API request** — values in the `spec` object of `POST /api/tests` always take the highest priority. This allows individual test runs to override any default.
2. **Per-type defaults** — `kates.tests.{type}.*` properties or `KATES_TESTS_{TYPE}_*` environment variables. These are set in `application.properties` or the Kubernetes ConfigMap and apply to all test runs of that type unless overridden by the API request.
3. **Built-in defaults** — hardcoded fallback values in the `TestTypeDefaults` CDI bean. These are the values used when neither the API request nor the per-type configuration provides a value.

### Full Parameter Reference

| Parameter | Global Default | Description |
|-----------|---------------|-------------|
| `topic` | auto-generated | Topic name. If omitted, Kates generates one based on the test type (e.g., `load-test`, `stress-test`). Providing an explicit topic name allows you to test against existing topics. |
| `numProducers` | `1` | Number of concurrent producer tasks. The total throughput is split evenly across producers. More producers enable higher aggregate throughput. |
| `numConsumers` | `1` | Number of concurrent consumer tasks. Consumers share a consumer group, so partitions are distributed across them. |
| `numRecords` | `1,000,000` | Total messages to produce across all producers. Each producer sends `numRecords / numProducers` messages. |
| `throughput` | `-1` (unlimited) | Target messages per second across all producers. `-1` means each producer sends as fast as possible. |
| `recordSize` | `1024` | Message payload size in bytes. Each record is filled with random bytes of this size. |
| `durationMs` | `600,000` (10 min) | Test duration in milliseconds. For multi-step tests (STRESS, SPIKE, CAPACITY), the total duration is divided among the steps. |
| `partitions` | `3` | Topic partition count. Higher values provide more parallelism for both producers and consumers. |
| `replicationFactor` | `3` | Topic replication factor. Higher values improve durability but increase replication traffic. |
| `minInsyncReplicas` | `2` | Minimum replicas that must acknowledge a write. Combined with `acks=all`, this controls the durability/availability trade-off. |
| `acks` | `all` | Producer acknowledgment mode. `all` = all in-sync replicas must acknowledge. `1` = only the leader. `0` = fire-and-forget (no acknowledgment). |
| `batchSize` | `65,536` | Producer `batch.size` in bytes. Larger batches improve throughput at the cost of latency and memory. |
| `lingerMs` | `5` | Producer `linger.ms`. Time the producer waits to accumulate more records before sending a batch. |
| `compressionType` | `lz4` | Producer compression codec. Options: `none`, `gzip`, `snappy`, `lz4`, `zstd`. |

### Per-Type Defaults Summary

The following table shows the effective default for each test type. Bold values indicate overrides from the global default:

| Type | Partitions | Batch Size | Linger | Acks | Compression | Producers | Duration |
|------|-----------|------------|--------|------|-------------|-----------|----------|
| LOAD | 3 | 64 KB | 5 ms | all | lz4 | 1 | 10 min |
| STRESS | **6** | **128 KB** | **10 ms** | all | lz4 | **3** | **15 min** |
| SPIKE | 3 | 128 KB | **0 ms** | **1** | **none** | 1 | **5 min** |
| ENDURANCE | 3 | 64 KB | 5 ms | all | lz4 | 1 | **1 hour** |
| VOLUME | **6** | **256 KB** | **50 ms** | all | lz4 | 1 | 10 min |
| CAPACITY | **12** | 128 KB | 10 ms | all | lz4 | **5** | **20 min** |
| ROUNDTRIP | 3 | **16 KB** | **0 ms** | all | **none** | 1 | 10 min |

Bold values indicate overrides from the global default. Each override is chosen to optimize for the test type's specific goal.

### Overriding via ConfigMap

To change a per-type default without rebuilding the application:

```bash
kubectl edit configmap kates-config -n kates
# Change the desired value, e.g.:
# KATES_TESTS_STRESS_PARTITIONS: "12"
kubectl rollout restart deployment/kates -n kates
```

After the pod restarts, the new value takes effect for all subsequent test runs of that type. You can verify the change via the `/api/health` endpoint, which shows the effective per-type configuration.
