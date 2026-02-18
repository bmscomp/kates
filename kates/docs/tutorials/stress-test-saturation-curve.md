# Tutorial: Finding Your Cluster's Breaking Point with a Stress Test

A LOAD test tells you how the cluster performs at a specific throughput. A STRESS test tells you where the cluster *breaks*. It automatically ramps throughput across five steps — from comfortable to overwhelming — and the results reveal the saturation point: the throughput level beyond which latency degrades unacceptably.

This tutorial walks you through running a STRESS test, reading the results, and plotting the saturation curve.

## Prerequisites

- A running Kates instance connected to a Kafka cluster
- `curl`, `jq`, and a spreadsheet application (Excel, Google Sheets, or LibreOffice Calc)

## Step 1: Submit the STRESS Test

```bash
curl -X POST http://localhost:8080/api/tests \
  -H 'Content-Type: application/json' \
  -d '{
    "type": "STRESS",
    "spec": {
      "topic": "stress-test-saturation",
      "numRecords": 500000,
      "recordSize": 1024,
      "partitions": 6,
      "replicationFactor": 3,
      "acks": "all",
      "numProducers": 3
    }
  }' | jq '.runId'
```

The STRESS test will run 5 sequential phases, each sending 100,000 messages (500,000 / 5) at increasing throughput:

| Phase | Throughput Target |
|-------|-------------------|
| Step 1 | 10,000 msg/sec |
| Step 2 | 25,000 msg/sec |
| Step 3 | 50,000 msg/sec |
| Step 4 | 100,000 msg/sec |
| Step 5 | 200,000 msg/sec |

This will take approximately 2-5 minutes depending on your cluster's capacity.

## Step 2: Export as CSV

Once the test completes, export the results:

```bash
RUN_ID="your-run-id"
curl -s "http://localhost:8080/api/tests/$RUN_ID/export?format=csv" > stress-results.csv
```

The CSV contains one row per phase, with throughput and latency columns:

```csv
runId,testType,backend,phase,recordsSent,throughputRecPerSec,...,p99LatencyMs,maxLatencyMs,error
abc123,STRESS,native,ramp-10k,100000,10050.12,...,12.0,35.0,
abc123,STRESS,native,ramp-25k,100000,24800.45,...,18.0,55.0,
abc123,STRESS,native,ramp-50k,100000,48200.78,...,45.0,180.0,
abc123,STRESS,native,ramp-100k,100000,72000.34,...,250.0,1200.0,
abc123,STRESS,native,ramp-200k,100000,65000.12,...,1500.0,5000.0,
```

## Step 3: Plot the Saturation Curve

Open the CSV in a spreadsheet and create a chart with two data series:

1. **X-axis:** Phase (or target throughput)
2. **Y-axis (primary):** `throughputRecPerSec` (actual throughput achieved)
3. **Y-axis (secondary):** `p99LatencyMs` (tail latency)

The resulting chart typically looks like this:

```
Throughput (msg/s)     P99 Latency (ms)
      |                       |
 70K ━━━━━━━╸peak            │ ┏━━━━━━━━━━ 1500
      |    ╱   ╲              │╱ 
 50K ━━━╱━━━━━━━╲━━━━        ┃   250
      |╱           ╲          │
 25K ╱━━━━━━━━━━━━╲━        │
    ╱               ╲        │ 45
 10K━━━━━━━━━━━━━━━━━        │ 12-18
      |   |   |   |   |      │
     10K 25K 50K 100K 200K   └──────── Target
```

## Step 4: Read the Curve

The saturation curve reveals four regions:

### Linear Region (Steps 1-2: 10K-25K msg/sec)
- **Actual throughput ≈ target throughput** — the cluster can easily handle this load
- **P99 latency is flat and low (12-18ms)** — no queueing pressure
- **Interpretation:** This is the "safe zone." If your production workload is here, you have ample headroom.

### Sublinear Region (Step 3: 50K msg/sec)
- **Actual throughput is close to target but slightly below** (48,200 vs 50,000)
- **P99 latency starts rising** (45ms vs 18ms at the lower step) — request queueing is beginning
- **Interpretation:** The cluster is working harder. Some requests are waiting in the broker's request queue before being processed.

### Saturation Point (Step 4: 100K msg/sec target)
- **Actual throughput peaks but is well below target** (72,000 vs 100,000)
- **P99 latency rises sharply** (250ms) — the classic "hockey stick" inflection
- **Interpretation:** This is the saturation point. The cluster's actual capacity is approximately 70,000-75,000 msg/sec. Beyond this, the request queue grows faster than it drains.

### Overload Region (Step 5: 200K msg/sec target)
- **Actual throughput *decreases*** (65,000 vs 72,000 at the previous step)
- **P99 latency is extreme** (1,500ms) — the queue is overflowing
- **Interpretation:** The cluster is in distress. Throughput drops because the broker spends more time managing timeouts and retries than processing messages. You may also see errors in this step.

## Step 5: Determine Your Capacity Limit

Your usable capacity is **not** the saturation point — it is the saturation point with a safety margin:

| Use Case | Recommended Headroom | Example (72K saturation) |
|----------|---------------------|--------------------------|
| Production workload | 50% headroom | 36,000 msg/sec max target |
| Staging/pre-prod | 30% headroom | 50,000 msg/sec max target |
| Short burst tolerance | 10% headroom | 65,000 msg/sec max target |

**Why 50% headroom for production?** Because production traffic is not constant. You need margin for traffic spikes, broker failures (which reduce capacity by ~33% in a 3-broker cluster), and consumer rebalancing latency.

## Step 6: Compare Before and After Changes

The real power of the STRESS test is comparison. Run it:
- Before and after a Kafka version upgrade
- Before and after changing `batch.size` or `compression.type`
- Before and after adding a broker or repartitioning topics
- Before and after infrastructure changes (disk upgrades, network reconfiguration)

Plot both curves on the same chart. If the saturation point moved right (higher throughput), the change improved capacity. If it moved left, the change reduced capacity.

## What to Try Next

1. **Try different `acks` settings** — run the same STRESS test with `acks=1` to see how much throughput you gain by relaxing durability
2. **Try different `batch.size`** — run with `batch.size=65536` to see if batching improves throughput
3. **Add a disruption** — run a resilience test to see how the saturation curve changes when a broker is down ([Tutorial: Your First Disruption Test](first-disruption-test.md))
