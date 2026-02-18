# Tutorial: Your First Disruption Test

This tutorial walks you through killing a Kafka broker leader and interpreting the ISR recovery timeline. By the end, you will understand how Kates orchestrates a disruption, what each section of the report means, and how to use the SLA grade to assess cluster resilience.

## Prerequisites

- A running Kates instance connected to a 3-broker Kafka cluster
- The cluster has `replication.factor=3` and `min.insync.replicas=2`
- `curl` and `jq` installed

## Step 1: Create a Test Topic

First, create a topic to target. The disruption will kill the broker hosting the leader for partition 0 of this topic:

```bash
curl -X POST http://localhost:8080/api/tests \
  -H 'Content-Type: application/json' \
  -d '{
    "type": "LOAD",
    "spec": {
      "topic": "disruption-target",
      "numRecords": 1000,
      "throughput": 100,
      "partitions": 6,
      "replicationFactor": 3
    }
  }' | jq '.runId'
```

This tiny LOAD test creates the topic and writes a few messages to ensure all partitions have leaders assigned. Wait for it to complete.

## Step 2: Submit a Single-Step Disruption

Now run the disruption. This plan kills the leader for partition 0 and observes the recovery:

```bash
curl -X POST http://localhost:8080/api/disruptions \
  -H 'Content-Type: application/json' \
  -d '{
    "planName": "first-disruption",
    "steps": [
      {
        "stepName": "kill-partition-leader",
        "faultSpec": {
          "experimentName": "kill-leader-p0",
          "disruptionType": "POD_KILL",
          "targetTopic": "disruption-target",
          "targetPartition": 0,
          "gracePeriodSec": 0
        },
        "observeAfterSec": 90,
        "steadyStateSec": 10
      }
    ],
    "maxAffectedBrokers": 1,
    "sla": {
      "maxP99LatencyMs": 100,
      "minThroughputRecPerSec": 5000,
      "maxErrorRate": 1.0,
      "maxRtoMs": 120000
    }
  }' | jq
```

Let's break down what each field does:

- **`disruptionType: "POD_KILL"`** — kills the pod hosting the leader for partition 0. Kubernetes will automatically restart it.
- **`targetTopic` + `targetPartition`** — enables leader-aware targeting. Kates will resolve which broker hosts the leader for partition 0 and kill that specific pod.
- **`gracePeriodSec: 0`** — kills the pod immediately, without a graceful shutdown. This simulates a crash, not a clean shutdown.
- **`observeAfterSec: 90`** — after the kill, watch the cluster for 90 seconds to capture the full recovery timeline.
- **`steadyStateSec: 10`** — wait 10 seconds before injecting the fault to capture baseline metrics.
- **`maxAffectedBrokers: 1`** — safety guard: prevent the experiment from affecting more than 1 broker.
- **`sla`** — the thresholds used for grading.

## Step 3: Wait for Completion

The disruption takes approximately 100 seconds (10s steady state + instant kill + 90s observation). Poll for completion:

```bash
REPORT_ID="your-report-id"
while true; do
  STATUS=$(curl -s http://localhost:8080/api/disruptions/$REPORT_ID | jq -r '.status')
  echo "Status: $STATUS"
  if [ "$STATUS" = "COMPLETED" ] || [ "$STATUS" = "ERROR" ]; then break; fi
  sleep 5
done
```

## Step 4: Read the Report

```bash
curl -s http://localhost:8080/api/disruptions/$REPORT_ID | jq
```

The report has four main sections. Let's examine each:

### 4a: Step Reports

```json
{
  "stepReports": [
    {
      "stepName": "kill-partition-leader",
      "status": "COMPLETED",
      "chaosOutcome": {
        "experimentName": "kill-leader-p0",
        "targetPod": "krafter-kafka-1",
        "status": "INJECTED",
        "startedAt": "2026-02-18T03:50:00Z",
        "completedAt": "2026-02-18T03:50:01Z"
      },
      "grade": "A"
    }
  ]
}
```

**`targetPod: "krafter-kafka-1"`** — Kates resolved partition 0's leader to broker 1, which runs on pod `krafter-kafka-1`. This is the pod that was killed.

**`grade: "A"`** — all SLA thresholds were met. The cluster handled the failure gracefully.

### 4b: ISR Timeline

```json
{
  "isrTimeline": [
    {"timestamp": "03:49:50Z", "isr": [0, 1, 2], "event": "BASELINE"},
    {"timestamp": "03:50:01Z", "isr": [0, 1, 2], "event": "STEADY"},
    {"timestamp": "03:50:12Z", "isr": [0, 2],    "event": "SHRINK"},
    {"timestamp": "03:50:45Z", "isr": [0, 1, 2], "event": "EXPAND"}
  ]
}
```

Reading this timeline:

- **t=0s (03:49:50Z):** Baseline captured. ISR is [0, 1, 2] — all three replicas are in sync.
- **t=11s:** The kill happened at t=10s. The ISR does not change immediately because the controller waits for `replica.lag.time.max.ms` (default 30 seconds) before evicting the dead replica.
- **t=22s (03:50:12Z):** ISR shrinks to [0, 2]. Broker 1's replica has been evicted because it has not fetched for 10+ seconds (the actual delay depends on `replica.lag.time.max.ms` and when the last successful fetch happened).
- **t=55s (03:50:45Z):** ISR expands back to [0, 1, 2]. The killed pod restarted (Kubernetes auto-restart), the broker came back up, its follower replica caught up with the leader, and the controller added it back to the ISR.

**Key metric: ISR recovery time = 33 seconds** (from shrink at 03:50:12Z to expand at 03:50:45Z). This is well within the 120-second SLA threshold.

### 4c: Consumer Lag

```json
{
  "lagTimeline": [
    {"timestamp": "03:49:50Z", "lag": 0},
    {"timestamp": "03:50:12Z", "lag": 150},
    {"timestamp": "03:50:25Z", "lag": 0}
  ]
}
```

Consumer lag spiked briefly during the leader election (some messages queued while the partition had no leader) and recovered once the new leader started serving fetches.

### 4d: SLA Verdict

```json
{
  "overallGrade": "A",
  "slaVerdict": {
    "grade": "A",
    "violated": false,
    "violations": [],
    "totalChecks": 4,
    "passedChecks": 4
  }
}
```

All 4 SLA checks (P99 latency, throughput, error rate, recovery time) passed. Grade A.

## Step 5: What If the Grade Was Not A?

If you see a lower grade, check the `violations` array:

```json
{
  "violations": [
    {
      "metricName": "p99LatencyMs",
      "constraint": "max",
      "threshold": 100.0,
      "actual": 250.0,
      "severity": "CRITICAL"
    }
  ]
}
```

This tells you exactly which metric failed and by how much. Common causes:

| Violation | Likely Cause | Fix |
|-----------|-------------|-----|
| P99 latency > threshold | ISR eviction timeout is too long | Reduce `replica.lag.time.max.ms` |
| Throughput below min | Cluster is too small for the workload | Add brokers or reduce test throughput |
| Error rate > max | Timeout settings too aggressive | Increase `request.timeout.ms` and `delivery.timeout.ms` |
| Recovery time > max | Slow broker restart or slow replication catch-up | Check pod resource limits, disk I/O |

## Step 6: Try a Multi-Step Disruption

Once you are comfortable with single-step disruptions, try a two-step plan that kills a broker and then scales down the cluster:

```bash
curl -X POST http://localhost:8080/api/disruptions \
  -H 'Content-Type: application/json' \
  -d '{
    "planName": "two-step-disruption",
    "steps": [
      {
        "stepName": "kill-leader",
        "faultSpec": {
          "disruptionType": "POD_KILL",
          "targetTopic": "disruption-target",
          "targetPartition": 0
        },
        "observeAfterSec": 60
      },
      {
        "stepName": "scale-down",
        "faultSpec": {
          "disruptionType": "SCALE_DOWN",
          "targetCount": 2
        },
        "observeAfterSec": 120
      }
    ],
    "maxAffectedBrokers": 1
  }'
```

Each step gets its own grade, and the overall grade is the worst of all steps. This is how you build up to the complex multi-step playbooks in the Playbook Catalog.

## What to Try Next

1. **Try `NETWORK_PARTITION` instead of `POD_KILL`** — this tests producer retry behavior rather than leader election
2. **Use a built-in playbook** — try the `az-failure-recovery` playbook for a more realistic scenario
3. **Combine with performance** — run a resilience test that measures throughput impact during the disruption ([Tutorial: CI/CD Quality Gate](ci-cd-quality-gate.md))
