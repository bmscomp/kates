# Tutorial: Designing a Custom Disruption Playbook

The built-in playbooks cover common Kafka failure scenarios, but your production environment has its own unique risks. Maybe your cluster runs across two availability zones with specific partition placement. Maybe you use tiered storage. Maybe your consumers have a known sensitivity to rebalancing. This tutorial teaches you how to design and run a multi-step disruption playbook that tests the failure modes specific to your deployment.

## The Anatomy of a Playbook

A playbook is a JSON document that defines a sequence of disruption steps, each with its own fault specification, observation window, and SLA thresholds. The steps execute sequentially — each step waits for the previous one to complete and its observation window to expire before starting.

Here is the minimal structure:

```json
{
  "planName": "my-custom-playbook",
  "steps": [
    {
      "stepName": "step-1-name",
      "faultSpec": { "..." },
      "observeAfterSec": 60
    },
    {
      "stepName": "step-2-name",
      "faultSpec": { "..." },
      "observeAfterSec": 90
    }
  ],
  "maxAffectedBrokers": 1,
  "sla": { "..." }
}
```

## Step 1: Identify Your Risk

Start by answering: *What failure would cause the most damage to our specific deployment?*

Common risk patterns:

| Pattern | Risk | Playbook Design |
|---------|------|-----------------|
| "We have 3 brokers across 2 AZs" | AZ failure takes out 2 of 3 brokers | Kill 2 brokers in the same AZ, observe if the surviving broker handles all partitions |
| "Our consumers restart frequently" | Consumer rebalancing storms | Kill consumer pods in sequence, observe lag accumulation and recovery |
| "We have topics with RF=1" | Single broker failure causes data loss | Kill each broker and verify which partitions become unavailable |
| "We just upgraded Kafka" | Regression in leader election or ISR behavior | Run the standard playbooks but with tighter SLA thresholds |
| "We use tiered storage" | Remote storage latency during failover | Kill a broker and observe whether consumers reading from remote storage experience different latency |

## Step 2: Design the Steps

A good playbook has 2-4 steps that build in severity. Each step should test one thing, and the steps should be ordered from least disruptive to most disruptive.

### Example: Database-Backed Consumer Resilience

Your application consumes from Kafka and writes to a database. You want to test what happens when the consumer cannot write to the database during a Kafka disruption.

```json
{
  "planName": "consumer-db-resilience",
  "steps": [
    {
      "stepName": "baseline-broker-kill",
      "description": "Verify basic broker failure recovery works",
      "faultSpec": {
        "experimentName": "kill-broker-1",
        "disruptionType": "POD_KILL",
        "targetTopic": "orders",
        "targetPartition": 0,
        "gracePeriodSec": 0
      },
      "steadyStateSec": 15,
      "observeAfterSec": 60
    },
    {
      "stepName": "degraded-consumer-restart",
      "description": "Restart consumer while broker is recovering",
      "faultSpec": {
        "experimentName": "restart-consumer",
        "disruptionType": "ROLLING_RESTART",
        "targetLabelSelector": "app=order-consumer"
      },
      "observeAfterSec": 120
    },
    {
      "stepName": "network-partition-consumer-db",
      "description": "Partition consumer from database during Kafka load",
      "faultSpec": {
        "experimentName": "partition-consumer-db",
        "disruptionType": "NETWORK_PARTITION",
        "targetLabelSelector": "app=order-consumer",
        "targetDirection": "egress",
        "targetPort": 5432,
        "chaosDurationSec": 30
      },
      "observeAfterSec": 120
    }
  ],
  "maxAffectedBrokers": 1,
  "sla": {
    "maxP99LatencyMs": 200,
    "minThroughputRecPerSec": 5000,
    "maxErrorRate": 5.0,
    "maxRtoMs": 180000,
    "maxDataLossPercent": 0
  }
}
```

### Why This Order?

**Step 1 (broker kill)** is the baseline. If this fails, there is a fundamental issue with the Kafka configuration that must be fixed before testing anything more complex.

**Step 2 (consumer restart)** tests what happens when the consumer restarts during recovery. This is a realistic scenario — monitoring systems often restart unhealthy consumers, and if the restart happens during a broker failure, the consumer must handle both the Kafka disruption and its own startup simultaneously.

**Step 3 (network partition)** is the most severe. It simulates the consumer losing database connectivity, which means consumed messages cannot be committed to the database. The consumer's behavior in this situation (does it pause? buffer? drop?) is critical for data integrity.

## Step 3: Set Appropriate SLA Thresholds

Different steps warrant different thresholds. A broker kill should recover faster than a network partition. You can set per-step SLA overrides:

```json
{
  "steps": [
    {
      "stepName": "baseline-broker-kill",
      "sla": {
        "maxRtoMs": 60000,
        "maxP99LatencyMs": 100
      },
      "..."
    },
    {
      "stepName": "network-partition-consumer-db",
      "sla": {
        "maxRtoMs": 180000,
        "maxP99LatencyMs": 500,
        "maxErrorRate": 10.0
      },
      "..."
    }
  ]
}
```

The first step has strict thresholds (60-second recovery, 100ms P99) because broker kill recovery should be fast. The third step has relaxed thresholds (180-second recovery, 500ms P99, 10% error rate) because a network partition is inherently more disruptive.

## Step 4: Run the Playbook

```bash
curl -X POST http://localhost:8080/api/disruptions \
  -H 'Content-Type: application/json' \
  -d @consumer-db-resilience.json | jq
```

The orchestrator runs each step sequentially. After each step:
1. It captures a Prometheus snapshot (if Prometheus is available)
2. It monitors ISR and consumer lag for the observation window
3. It grades the step against its SLA
4. It waits for any auto-rollback to complete
5. It proceeds to the next step

## Step 5: Interpret the Multi-Step Report

The report contains per-step grades and an overall grade:

```json
{
  "overallGrade": "B",
  "stepReports": [
    {"stepName": "baseline-broker-kill",        "grade": "A"},
    {"stepName": "degraded-consumer-restart",     "grade": "A"},
    {"stepName": "network-partition-consumer-db", "grade": "B"}
  ]
}
```

The overall grade is "B" because one step was B. Reading the violations for that step:

```json
{
  "violations": [
    {
      "metricName": "p99LatencyMs",
      "threshold": 500.0,
      "actual": 420.0,
      "severity": "WARNING"
    }
  ]
}
```

The P99 was 420ms against a 500ms threshold — it passed the threshold but triggered a warning because it was within 10% of the limit. This is useful information: the consumer's database reconnection logic works, but it is on the edge of the SLA.

## Step 6: Iterate and Harden

Based on the results, you might:

1. **Tighten the SLA** — if 420ms P99 is actually fine for your application, lower the threshold to 450ms so you get an A
2. **Add more steps** — test what happens when the network partition lasts 60 seconds instead of 30
3. **Add to CI** — once the playbook consistently passes, add it to your CI quality gate
4. **Save as a file** — place the playbook JSON in your repo's `chaos/` directory for version control

## Design Principles for Playbooks

1. **Start simple, add complexity.** A single-step POD_KILL is always step 1. Only add multi-fault scenarios after the baseline passes.

2. **One variable per step.** Each step should change one thing from the previous state. If step 1 kills a broker and step 2 kills a different broker plus adds CPU stress, you cannot isolate which change caused a regression.

3. **Observation windows must be long enough.** The most common mistake is an observation window that ends before recovery completes. When in doubt, double the window.

4. **SLA thresholds should be based on real requirements.** Do not use arbitrary numbers. Talk to your product team: "What is the maximum acceptable delay for order confirmations?" That answer becomes your lag recovery threshold.

5. **Version control your playbooks.** Playbooks are test code. They should be reviewed, committed, and maintained with the same rigor as application code.
