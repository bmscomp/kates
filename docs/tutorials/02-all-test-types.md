# Tutorial 2: Running Every Test Type

This tutorial walks through all 8 Kates test types with real commands and expected outputs. By the end, you'll understand which test to use for every scenario.

## Prerequisites

- Kates stack deployed and CLI configured (see [Tutorial 1](01-getting-started.md))
- System health verified: `kates health`

## 1. LOAD Test — Baseline Performance

The LOAD test establishes your cluster's steady-state performance at a controlled rate.

**When to use:** Before any other test. This provides the baseline you'll compare everything else against.

```bash
# List the built-in LOAD templates (quick-load, production-load, ci-gate)
kates test scaffold --type LOAD

# Run a basic load test
kates test create --type LOAD \
  --records 100000 \
  --producers 2 \
  --acks all \
  --wait
```

**What to record:** Note the P99 latency and throughput. These are your baseline numbers.

```mermaid
graph LR
    A["Your Baseline"] --> B["P99: ___ms"]
    A --> C["Throughput: ___rec/s"]
    A --> D["Errors: 0%"]
```

## 2. STRESS Test — Finding the Breaking Point

The STRESS test ramps load until the cluster can no longer keep up.

**When to use:** Capacity planning. You need to know how much headroom exists.

```bash
# Export the built-in STRESS template
kates test scaffold export stress-test -o stress.yaml

# Quick stress test with 4 producers ramping up
kates test create --type STRESS \
  --records 500000 \
  --producers 4 \
  --duration 180 \
  --wait
```

**What to watch:** Look for the phase where P99 latency jumps significantly. That's your saturation point.

```mermaid
graph LR
    subgraph Results
        R1["Phase 1: Good ✅<br/>P99 < 20ms"]
        R2["Phase 2: OK ✅<br/>P99 < 50ms"]
        R3["Phase 3: Warning ⚠<br/>P99 > 100ms"]
        R4["Phase 4: Breaking ❌<br/>P99 > 500ms"]
    end
```

## 3. SPIKE Test — Flash Sale Simulation

The SPIKE test hits the cluster with a sudden burst of traffic, then drops back to normal.

**When to use:** Preparing for events with sudden traffic increases (marketing campaigns, product launches).

```bash
# Export the built-in SPIKE template
kates test scaffold export spike-test -o spike.yaml

# View the exported YAML
cat spike.yaml

# Apply it
kates test apply -f spike.yaml --wait
```

The `spike-test` template is a single burst: 500,000 records through 32 parallel
producers over 60 seconds, `acks: "1"` and `lingerMs: 0` so the load arrives as
fast as the client can push it. Its gate allows a P99 of 500 ms and an error
rate of 1% — deliberately looser than a LOAD test, because absorbing a burst is
the thing being measured, not steady-state latency.

**Key question:** How long does P99 take to return to baseline after the spike?

## 4. ENDURANCE Test — Soak Testing

The ENDURANCE test runs at moderate load for an extended period to detect slow resource leaks.

**When to use:** Before major releases. Run overnight to catch memory leaks, thread leaks, and log accumulation.

```bash
# Export the built-in ENDURANCE template (a 1-hour soak at 5k msg/s)
kates test scaffold export endurance-soak -o endurance.yaml

# Start a 30-minute endurance test
kates test create --type ENDURANCE \
  --records 1000000 \
  --producers 2 \
  --duration 1800 \
  --wait
```

**What to watch:** Compare metrics from the first 5 minutes vs. the last 5 minutes. Any drift indicates a leak.

```mermaid
graph LR
    subgraph Healthy
        H1["Start: P99 = 15ms"] --> H2["End: P99 = 16ms<br/>✅ Stable"]
    end
    
    subgraph Leaking
        L1["Start: P99 = 15ms"] --> L2["End: P99 = 150ms<br/>❌ 10x degradation"]
    end
```

## 5. VOLUME Test — Large Data Handling

The VOLUME test focuses on large messages and data volumes to stress storage and replication.

**When to use:** When your production workload includes large messages (images, documents, large events).

```bash
# The scenario library ships no VOLUME template — drive this one from flags
# Run with large messages
kates test create --type VOLUME \
  --records 10000 \
  --record-size 102400 \
  --acks all \
  --wait
```

The 100KB record size × 10,000 records = ~1GB of data through the cluster.

**What to watch:** Does throughput (MB/s) scale linearly, or does it plateau?

## 6. CAPACITY Test — Maximum Throughput Discovery

The CAPACITY test removes all rate limiting and finds the absolute ceiling.

**When to use:** When you need hard numbers for capacity planning documents.

```bash
# The scenario library ships no CAPACITY template — drive this one from flags
# Run with unlimited throughput
kates test create --type CAPACITY \
  --records 1000000 \
  --producers 8 \
  --throughput -1 \
  --wait
```

**Key metric:** The maximum sustained throughput before errors appear.

```mermaid
graph TB
    subgraph Discovery
        P1["1 producer<br/>~50K rec/s"] --> P2["2 producers<br/>~95K rec/s"]
        P2 --> P3["4 producers<br/>~170K rec/s"]
        P3 --> P4["8 producers<br/>~200K rec/s ← MAX"]
        P4 --> P5["16 producers<br/>~195K rec/s ← Overloaded"]
    end
```

## 7. ROUND_TRIP Test — End-to-End Latency

The ROUND_TRIP test measures the complete journey from producer to consumer.

**When to use:** When you need to SLA on consumer-side delivery latency, not just producer acknowledgment.

```bash
# Export the built-in ROUND_TRIP template (exactly-once, CRC-verified)
kates test scaffold export exactly-once -o roundtrip.yaml

# Run with 1 producer and 1 consumer
kates test create --type ROUND_TRIP \
  --records 10000 \
  --producers 1 \
  --consumers 1 \
  --wait
```

**Key insight:** Round-trip latency is typically 2–5x higher than producer latency because it includes consumer fetch polling intervals.

## 8. INTEGRITY Test — Zero Data Loss Verification

The INTEGRITY test verifies that every message is persisted and deliverable.

**When to use:** Whenever you change Kafka configuration, upgrade brokers, or validate a new cluster.

```bash
# Export the built-in INTEGRITY template (transactional, zstd, CRC)
kates test scaffold export integrity-tx -o integrity.yaml

# Run data integrity verification
kates test create --type INTEGRITY \
  --records 100000 \
  --consumers 1 \
  --acks all \
  --wait
```

**Expected output:**

```
  Data Integrity
  ──────────────
  Sent       100,000
  Acked      100,000
  Received   100,000
  Lost            0
  Mode       idempotent
  Verdict    PASS ✅
```

### Integrity + Chaos (Advanced)

For the ultimate validation — verify integrity while killing a broker. No
scenario template carries chaos: a scaffold describes a test, and pairing one
with a fault is what `kates resilience run` is for. It takes a config with a
`testRequest` and a `chaosSpec` side by side:

```bash
cat > integrity-chaos.json <<'EOF'
{
  "testRequest": {
    "testType": "INTEGRITY",
    "spec": { "records": 100000, "acks": "all", "consumers": 1 }
  },
  "chaosSpec": {
    "experimentName": "kafka-pod-kill",
    "disruptionType": "POD_KILL",
    "targetNamespace": "kafka"
  },
  "steadyStateSec": 30
}
EOF

kates resilience run -f integrity-chaos.json
```

[Tutorial 4](04-integrity-under-fire.md) works through this in full.

## Quick Reference: Choosing the Right Test

```mermaid
graph TD
    Q1{What do you want<br/>to know?}
    
    Q1 -->|"Normal performance"| LOAD[Run LOAD test]
    Q1 -->|"Breaking point"| STRESS[Run STRESS test]
    Q1 -->|"Burst handling"| SPIKE[Run SPIKE test]
    Q1 -->|"Long-term stability"| ENDURANCE[Run ENDURANCE test]
    Q1 -->|"Large message support"| VOLUME[Run VOLUME test]
    Q1 -->|"Maximum throughput"| CAPACITY[Run CAPACITY test]
    Q1 -->|"Consumer delivery time"| RT[Run ROUND_TRIP test]
    Q1 -->|"Data safety"| INT[Run INTEGRITY test]
    Q1 -->|"Data safety under failure"| INTC[Run INTEGRITY via kates resilience run]
```

## Comparing All Results

After running multiple tests:

```bash
# List all your test runs
kates test list

# View trends
kates trend --type LOAD --metric p99LatencyMs --days 1
kates trend --type LOAD --metric avgThroughputRecPerSec --days 1
```

## What's Next?

- [Tutorial 3: Chaos Engineering](03-chaos-engineering.md) — inject broker failures
- [Tutorial 5: Heatmaps and Exports](05-observability.md) — deep analysis
