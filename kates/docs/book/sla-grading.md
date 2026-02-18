# SLA Grading: Quantifying Resilience

How do you answer the question "Is our Kafka cluster resilient enough?" The word "enough" implies a threshold — a boundary between acceptable and unacceptable behavior. SLA (Service Level Agreement) grading is the mechanism Kates uses to make this boundary explicit, measurable, and automatable.

This chapter explains the grading algorithm, the philosophy behind the threshold design, how grades map to actionable decisions, and how to use the grading system in CI/CD pipelines.

## Why Grade at All?

A disruption test produces a lot of data: ISR timelines, consumer lag snapshots, Prometheus metrics, pod event streams. This data is valuable for post-mortem analysis, but it does not answer the binary question a CI/CD pipeline needs: pass or fail?

Grades bridge this gap. They transform a complex, multi-dimensional observation into a single letter (A through F) that can be compared against a threshold, logged, trended, and used to gate deployments. They are the interface between the rich observability data (designed for humans) and the pass/fail decision (designed for automation).

## The Grading Algorithm

The SLA grader evaluates each disruption step independently. For each step, it collects the observed metrics after the observation window and compares them against the SLA definition's thresholds.

### Metric Collection

After a disruption step completes and the observation window expires, the grader collects:

| Metric | Source | What It Measures |
|--------|--------|-----------------|
| ISR recovery time | `KafkaIntelligenceService.isrTracking` | Seconds from ISR shrink to full ISR restoration |
| Consumer lag recovery time | `KafkaIntelligenceService.lagTracking` | Seconds from peak lag to baseline lag |
| Under-replicated partitions | `PrometheusMetricsCapture` | Count of partitions where ISR < replication factor at end of observation |
| Throughput delta | `PrometheusMetricsCapture` | Percentage change in throughput (pre vs post) |
| P99 latency delta | `PrometheusMetricsCapture` | Percentage change in P99 latency (pre vs post) |
| Error count | Test results | Number of produce or consume errors during the step |
| Data integrity | `IntegrityResult` (if ROUND_TRIP) | Lost messages, duplicated messages, out-of-order messages |

### Threshold Comparison

Each metric is compared against its SLA threshold to produce a per-metric verdict:

```
PASS    — metric is within SLA
WARN    — metric exceeds SLA by less than the warning margin (default 10%)
FAIL    — metric exceeds SLA
```

The default SLA thresholds, which can be overridden in the disruption plan, are:

| Metric | Default Threshold | Rationale |
|--------|-------------------|-----------|
| ISR recovery time | 120 seconds | A healthy cluster with 3 replicas typically recovers ISR within 60s. 120s allows for slow catch-up under load. |
| Lag recovery time | 180 seconds | Consumer lag recovery depends on consumer throughput. 180s is generous enough for moderate-throughput consumers. |
| Under-replicated partitions (at end) | 0 | After the observation window, all partitions should be fully replicated. Non-zero indicates a persistent issue. |
| Throughput delta | -20% | A 20% throughput drop is noticeable but acceptable during recovery. More than 20% suggests a systemic problem. |
| P99 latency delta | +200% | P99 can spike dramatically during leader election. A 3× increase (200%) is acceptable for brief periods. |
| Error count | 0 | Any error during the step suggests a configuration issue (timeouts too short, retries too few). |
| Lost messages | 0 | Data loss is never acceptable with `acks=all` and `min.insync.replicas >= 2`. |

### Grade Computation

The overall grade for a step is the worst per-metric verdict, mapped to a letter:

| Condition | Grade |
|-----------|-------|
| All metrics PASS | **A** |
| All metrics PASS or WARN, at most 1 WARN | **B** |
| At most 2 metrics FAIL, no critical failures | **C** |
| Multiple metrics FAIL, or any critical metric > 2× threshold | **D** |
| Data loss, or any critical metric > 5× threshold | **F** |

A "critical" metric is one that indicates data loss or cluster-level failure: lost messages, active controller count ≠ 1, or under-replicated partitions persisting past the observation window.

The overall report grade is the worst grade across all steps. If a 3-step disruption plan produces grades [A, B, A], the overall grade is B.

## Designing Good SLA Thresholds

The default thresholds are deliberately conservative — they are designed to pass clusters that are reasonably well-configured and fail clusters that have serious issues. But your production requirements should drive your thresholds, not the defaults.

### Start with Your Application's Requirements

Work backward from what your application needs:

1. **What is the maximum acceptable latency?** If your application has a 100ms SLA for API responses and spends 20ms on business logic, you have 80ms for Kafka. Set P99 latency threshold accordingly.

2. **What is the maximum acceptable downtime?** If your SLA allows 1 minute of degraded service per month, your ISR recovery threshold should be well under 60 seconds (to leave margin for non-test failures).

3. **Can your application tolerate message loss?** If not, set `lostMessages` threshold to 0 and use `acks=all` with `min.insync.replicas=2`. If your application has idempotent consumers and can tolerate rare duplicates, you might relax the error count threshold.

### Per-Fault Thresholds

Different faults warrant different thresholds. A `POD_KILL` (which Kubernetes auto-restarts) should recover faster than a `SCALE_DOWN` (which requires manual scaling back up). A `NETWORK_PARTITION` (which causes split-brain scenarios) deserves stricter ISR thresholds than a `CPU_STRESS` (which just slows processing).

You can set per-step SLA overrides in your disruption plan:

```json
{
  "steps": [
    {
      "stepName": "kill-leader",
      "sla": {
        "isrRecoveryTimeSec": 60,
        "throughputDeltaPercent": -10,
        "p99LatencyDeltaPercent": 100
      }
    }
  ]
}
```

## Using Grades in CI/CD

The most powerful use of SLA grading is automated quality gating. Instead of running a disruption test, reading the report, and manually deciding whether to deploy, you let the grade make the decision.

### The Quality Gate Pattern

```yaml
- name: Run resilience gate
  run: |
    REPORT=$(curl -s -X POST http://kates:8080/api/disruptions \
      -H 'Content-Type: application/json' \
      -d @disruption-plan.json)
    GRADE=$(echo "$REPORT" | jq -r '.overallGrade')
    echo "Resilience grade: $GRADE"
    if [[ "$GRADE" =~ ^[DE]$ ]]; then
      echo "::error::Resilience grade $GRADE below threshold (minimum: C)"
      exit 1
    fi
```

This pattern gates deployments on a minimum grade of C. Grades A-C allow the deploy to proceed. Grades D-F fail the build.

### Trend Tracking

Grades are most valuable when tracked over time. A cluster that consistently scores A on broker-kill tests but suddenly scores C after a Kafka version upgrade has regressed. A cluster that consistently scores D on network-partition tests has a persistent configuration issue.

Export the grade alongside the test metadata:

```bash
echo "$DATE,$GRADE,$COMMIT_SHA,$KAFKA_VERSION" >> resilience-grades.csv
```

Over time, this CSV becomes a resilience regression log — a historical record of how your cluster's resilience has changed with each change to the infrastructure.

## The Philosophy of Grading

SLA grading is intentionally opinionated. It reduces a complex, multidimensional observation to a single letter. This is a lossy compression — you lose detail. But the detail is still available in the full disruption report for anyone who needs it. The grade serves a different purpose: it makes resilience *comparable*, *trendable*, and *automatable*.

A common objection is: "But our situation is more nuanced than a single letter." That is true. It is also true of unit test pass/fail, code coverage percentages, and security vulnerability scores. All of these are simplifications of complex realities. Their value is not in perfectly representing the truth — it is in making the truth actionable. A CI pipeline cannot act on nuance. It can act on a grade.
