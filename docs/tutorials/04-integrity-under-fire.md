# Tutorial 4: Data Integrity Under Fire

This tutorial verifies that your Kafka cluster delivers on its "no data loss" guarantee, even when brokers are crashing. This is the highest-confidence test you can run.

## Prerequisites

- Kates stack deployed and CLI configured
- LitmusChaos deployed for chaos-aware tests

## Part 1: Baseline Integrity Test

First, verify integrity under normal conditions:

```bash
kates test create --type INTEGRITY \
  --records 100000 \
  --consumers 1 \
  --acks all \
  --wait
```

Expected result:

```
  Data Integrity
  ──────────────
  Sent       100,000
  Acked      100,000
  Received   100,000
  Lost            0
  Duplicates      0
  Mode       idempotent
  Verdict    PASS ✅
```

If this fails, stop here — you have a configuration problem that must be fixed before chaos testing.

## Part 2: Integrity Under Broker Kill

Now the real test — produce 100K messages while killing a broker in the middle:

### Step 1: Write the Chaos Integrity Config

No scenario template carries chaos — a scaffold describes a test, and pairing
one with a fault is what `kates resilience run` is for. Its config puts the
`testRequest` and the `chaosSpec` side by side:

```bash
cat > integrity-chaos.json <<'EOF'
{
  "testRequest": {
    "testType": "INTEGRITY",
    "spec": {
      "records": 100000,
      "acks": "all",
      "consumers": 1
    }
  },
  "chaosSpec": {
    "experimentName": "kafka-pod-kill",
    "disruptionType": "POD_KILL",
    "targetNamespace": "kafka"
  },
  "steadyStateSec": 30
}
EOF
```

### Step 2: Review It

```bash
cat integrity-chaos.json
```

The run has a shape the results are read against:

```mermaid
graph LR
    P1["Phase 1<br/>Produce 50K messages<br/>Steady state"] --> P2["Phase 2<br/>Kill broker<br/>Continue producing"] --> P3["Phase 3<br/>Wait for recovery<br/>Produce remaining"] --> P4["Phase 4<br/>Consume all<br/>Verify integrity"]
```

### Step 3: Run It

```bash
kates resilience run -f integrity-chaos.json
```

Add `--dry-run` first to have the config parsed and echoed without executing.

### Step 4: Analyze the Results

```bash
kates test get <id>
```

Expected output:

```
  Data Integrity
  ──────────────
  Sent       100,000
  Acked      100,000
  Received   100,000
  Lost            0
  Duplicates      0
  Mode       idempotent
  Verdict    PASS ✅

  Timeline Events
  ┌─────────────────┬────────────────┬──────────────────────────────┐
  │ Timestamp       │ Type           │ Detail                       │
  ├─────────────────┼────────────────┼──────────────────────────────┤
  │ 1708012345000   │ PRODUCE_START  │ Started producing 100K       │
  │ 1708012375000   │ FAULT_INJECTED │ Killed broker-0              │
  │ 1708012376000   │ ISR_SHRINK     │ P0 ISR: [0,1,2] → [1,2]     │
  │ 1708012378000   │ LEADER_CHANGE  │ P0 leader: 0 → 1            │
  │ 1708012380000   │ PRODUCE_ERROR  │ 3 send timeouts (retrying)   │
  │ 1708012395000   │ BROKER_RECOVER │ Broker 0 rejoined            │
  │ 1708012410000   │ ISR_EXPAND     │ P0 ISR: [1,2] → [0,1,2]     │
  │ 1708012420000   │ PRODUCE_END    │ All 100K produced            │
  │ 1708012425000   │ CONSUME_END    │ All 100K consumed            │
  │ 1708012425001   │ VERDICT        │ PASS — zero data loss        │
  └─────────────────┴────────────────┴──────────────────────────────┘
```

## Part 3: Testing Different Failure Modes

### Network Partition

Does the cluster lose messages when a broker is network-isolated?

Create `integrity-partition.json`:

```json
{
  "testRequest": {
    "testType": "INTEGRITY",
    "spec": {
      "records": 100000,
      "acks": "all",
      "consumers": 1
    }
  },
  "chaosSpec": {
    "experimentName": "network-partition",
    "disruptionType": "NETWORK_PARTITION",
    "targetNamespace": "kafka"
  },
  "steadyStateSec": 30
}
```

```bash
kates resilience run -f integrity-partition.json
```

### CPU Stress

Does CPU saturation cause replication failures?

```json
{
  "testRequest": {
    "testType": "INTEGRITY",
    "spec": { "records": 50000, "acks": "all", "consumers": 1 }
  },
  "chaosSpec": {
    "experimentName": "cpu-stress",
    "disruptionType": "CPU_STRESS",
    "targetNamespace": "kafka"
  },
  "steadyStateSec": 20
}
```

### Multiple Failures

Create a comprehensive integrity validation plan:

```mermaid
graph TB
    T1["Test 1: Baseline<br/>INTEGRITY, no chaos<br/>Expected: PASS"] --> T2["Test 2: Pod Kill<br/>INTEGRITY + pod-kill chaos<br/>Expected: PASS"]
    T2 --> T3["Test 3: Network Partition<br/>INTEGRITY + resilience<br/>Expected: PASS"]
    T3 --> T4["Test 4: CPU Stress<br/>INTEGRITY + resilience<br/>Expected: PASS"]
    T4 --> VERDICT["All 4 passed?<br/>→ Cluster is reliable ✅"]
```

## Part 4: Understanding Failures

If an integrity test fails, here's how to diagnose:

### Scenario: DATA_LOSS Detected

```
  Data Integrity
  ──────────────
  Sent       100,000
  Acked       99,998
  Received    99,996
  Lost            2
  Lost Ranges [45231-45231], [78442-78442]
  Verdict    DATA_LOSS ❌
```

**Diagnosis checklist:**

```mermaid
graph TD
    LOSS["DATA_LOSS detected"] --> Q1{acks = all?}
    Q1 -->|No| FIX1["Fix: Set acks=all<br/>Messages weren't<br/>fully replicated"]
    Q1 -->|Yes| Q2{min.insync.replicas ≥ 2?}
    Q2 -->|No| FIX2["Fix: Set min.insync.replicas=2<br/>Single replica isn't safe"]
    Q2 -->|Yes| Q3{unclean.leader.election?}
    Q3 -->|Yes| FIX3["Fix: Disable unclean election<br/>Out-of-sync replica became leader"]
    Q3 -->|No| Q4["Check broker logs<br/>for hardware/storage errors"]
```

## Part 5: Continuous Integrity Monitoring

Schedule nightly integrity tests to catch regressions:

A schedule is a cron expression plus a test request read from a JSON file —
`--name`, `--cron` and `--request` are all required:

```bash
cat > nightly-integrity.json <<'EOF'
{
  "testType": "INTEGRITY",
  "spec": { "records": 100000, "acks": "all", "consumers": 1 }
}
EOF

# Run the integrity test every night at 2 AM
kates schedule create \
  --name "Nightly Integrity" \
  --cron "0 2 * * *" \
  --request nightly-integrity.json

# Monitor trends
kates trend --type INTEGRITY --metric errorRate --days 30
```

`--metric` takes one of the report-summary metrics the trend service knows:
`avgThroughputRecPerSec`, `peakThroughputRecPerSec`, `avgThroughputMBPerSec`,
`avgLatencyMs`, `p50LatencyMs`, `p95LatencyMs`, `p99LatencyMs`, `p999LatencyMs`,
`maxLatencyMs` and `errorRate`. Per-run loss counts are not one of them — read
those from the integrity verdict on the run itself.

A regression in the integrity trend is the most critical alert you can have — it means your cluster configuration may have changed in a way that risks data loss.

## What's Next?

- [Tutorial 5: Heatmaps, Trends, and Exports](05-observability.md) — deep analysis tools
- [Tutorial 6: CI/CD Integration](06-cicd-integration.md) — automate everything
