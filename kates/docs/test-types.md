# Test Types

KATES supports seven performance test types, each designed to stress different aspects of a Kafka cluster. Every test type translates into specific Trogdor task specifications via the `SpecFactory`.

## LOAD — Steady-State Throughput

**Purpose:** Measure baseline throughput and latency under a sustained, controlled workload.

**What it does:**
- Spawns N producer tasks and M consumer tasks in parallel
- Each producer sends `numRecords / numProducers` messages at `throughput / numProducers` msg/sec
- Consumers read from the same topic with a shared consumer group

**When to use:** Establishing performance baselines, validating hardware sizing, comparing configurations.

**Trogdor mapping:**
- N × `ProduceBenchSpec` + M × `ConsumeBenchSpec`

**Example:**

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

---

## STRESS — Ramp-Up Until Failure

**Purpose:** Find the throughput ceiling by gradually increasing load until the cluster saturates.

**What it does:**
- Executes 5 sequential producer phases with increasing throughput:
  - Step 1: 10,000 msg/sec
  - Step 2: 25,000 msg/sec
  - Step 3: 50,000 msg/sec
  - Step 4: 100,000 msg/sec
  - Step 5: Unlimited (maximum throughput)
- Each step runs for `durationMs / 5`
- Uses aggressive batching: `batch.size=131072`, `linger.ms=10`

**When to use:** Finding the saturation point, identifying bottlenecks (network, disk, CPU), testing broker backpressure handling.

**Trogdor mapping:**
- 5 × `ProduceBenchSpec` (sequential, increasing throughput)

---

## SPIKE — Sudden Burst and Recovery

**Purpose:** Test cluster resilience to sudden traffic spikes and measure recovery behavior.

**What it does:**
- **Phase 1 — Baseline:** 60 seconds at 1,000 msg/sec (calm state)
- **Phase 2 — Spike:** 120 seconds with 3 concurrent producers at unlimited throughput
- **Phase 3 — Recovery:** 60 seconds at 1,000 msg/sec (return to normal)

**When to use:** Simulating traffic bursts (marketing campaigns, flash sales), testing ISR shrinkage/expansion, validating consumer lag recovery.

**Trogdor mapping:**
- 5 × `ProduceBenchSpec` (1 baseline + 3 burst + 1 recovery)

---

## ENDURANCE — Long-Duration Soak Test

**Purpose:** Detect time-dependent issues like memory leaks, GC pressure, log segment accumulation, or performance degradation over hours.

**What it does:**
- Runs 1 producer and 1 consumer at a sustained rate for an extended period
- Minimum enforced duration: 1 hour (even if a shorter duration is specified)
- `maxMessages = throughput × (durationMs / 1000)` — calculates the exact number of messages for the duration
- Default throughput: 5,000 msg/sec if not specified

**When to use:** Pre-production validation, SLA verification, JVM stability testing, log compaction behavior.

**Trogdor mapping:**
- 1 × `ProduceBenchSpec` + 1 × `ConsumeBenchSpec`

---

## VOLUME — Large and Numerous Messages

**Purpose:** Test behavior with extreme message sizes and counts.

**What it does:**
- **Large messages spec:** 50,000 messages × 100 KB each (5 GB total), with `max.request.size=1048576`
- **High count spec:** 5,000,000 messages × 1 KB each (5 GB total), with aggressive batching

Both run at unlimited throughput. Separate topics are created (`-large` and `-count` suffixes) with `retention.ms=1800000` and `max.message.bytes=1048576`.

**When to use:** Testing message size limits, broker memory pressure, disk I/O patterns, segment rotation.

**Trogdor mapping:**
- 2 × `ProduceBenchSpec` (different record sizes)

---

## CAPACITY — Maximum Throughput Discovery

**Purpose:** Systematically probe the cluster to find its maximum sustainable throughput.

**What it does:**
- Runs 6 sequential probes with increasing throughput:
  - 5,000 → 10,000 → 20,000 → 40,000 → 80,000 → Unlimited
- Each probe runs for `durationMs / 6`
- Unlike STRESS, uses default producer configs (not aggressive batching)

**When to use:** Capacity planning, hardware procurement decisions, before/after comparison for configuration changes.

**Trogdor mapping:**
- 6 × `ProduceBenchSpec` (sequential probes)

**Interpreting results:** The last probe step where latency remains acceptable indicates the cluster's sustainable capacity. A sharp increase in P99 latency signals the saturation point.

---

## ROUND_TRIP — End-to-End Latency

**Purpose:** Measure the complete produce-to-consume latency through the Kafka cluster.

**What it does:**
- Uses Trogdor's `RoundTripWorkloadSpec` which produces messages and measures time until the same messages are consumed
- Captures precise latency percentiles (P50, P95, P99, max)
- Default throughput: 1,000 msg/sec if not specified

**When to use:** SLA validation, measuring replication latency impact, comparing `acks=all` vs `acks=1`, testing cross-zone latency.

**Trogdor mapping:**
- 1 × `RoundTripWorkloadSpec`

**Example:**

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

## Test Spec Parameters

| Parameter | Default | Description |
|-----------|---------|-------------|
| `topic` | auto-generated | Topic name (e.g. `load-test`, `stress-test`) |
| `numProducers` | `1` | Number of concurrent producer tasks |
| `numConsumers` | `1` | Number of concurrent consumer tasks |
| `numRecords` | `1,000,000` | Total messages to produce |
| `throughput` | `-1` (unlimited) | Target messages per second |
| `recordSize` | `1024` | Message payload size in bytes |
| `durationMs` | `600,000` (10 min) | Test duration in milliseconds |
| `partitions` | `3` | Topic partition count |
| `replicationFactor` | `3` | Topic replication factor |
| `minInsyncReplicas` | `2` | min.insync.replicas topic config |
| `acks` | `all` | Producer acks setting |
| `batchSize` | `65,536` | Producer batch.size in bytes |
| `lingerMs` | `5` | Producer linger.ms |
| `compressionType` | `lz4` | Producer compression codec |
