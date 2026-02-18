# Playbook Catalog

When you first start with chaos engineering, the hardest part is not running the experiment — it is designing one. What should you test? What failure scenarios matter? How do you set the parameters so the experiment is meaningful without being reckless?

Kates solves this cold-start problem with a catalog of six built-in playbooks. Each playbook encodes a well-tested disruption scenario based on real-world Kafka failure patterns. They are not toy examples — they represent the failure modes that have taken down production Kafka clusters at organizations around the world.

This chapter walks through each playbook in detail: the theory behind the failure it simulates, the YAML source that defines it, what you should observe when you run it, and what the results tell you about your cluster.

## How Playbooks Work

A playbook is a YAML file stored in `src/main/resources/playbooks/`. At startup, the `DisruptionPlaybookCatalog` scans this directory, parses each YAML file into a `DisruptionPlan`, and registers it in an in-memory catalog. You can list and execute playbooks through the REST API.

### Listing Available Playbooks

```bash
curl -s http://localhost:8080/api/disruptions/playbooks | jq '.[].name'
```

Returns: `az-failure`, `leader-cascade`, `split-brain`, `storage-pressure`, `rolling-restart`, `consumer-isolation`.

### Executing a Playbook

```bash
curl -X POST http://localhost:8080/api/disruptions/playbooks/az-failure | jq
```

This submits the playbook as a `DisruptionPlan` to the `DisruptionOrchestrator`, which executes it through the same 13-step pipeline described in the [Disruption Guide](disruption-guide.md). You get back a full `DisruptionReport` with ISR tracking, SLA grading, and impact analysis.

---

## az-failure — Availability Zone Failure

### The Theory

Cloud providers organize their infrastructure into availability zones (AZs) — physically separated data centers within a region that share a metropolitan fiber network but have independent power, cooling, and networking. The promise of multi-AZ deployment is that losing an entire AZ should not cause an outage.

For Kafka, this promise depends critically on how your brokers are distributed across AZs and how your topic partitions are replicated. If you have 3 brokers spread across 3 AZs with `replication.factor=3`, then each partition has one replica per AZ. Losing an AZ kills one broker, which means one replica per partition is lost. With `min.insync.replicas=2`, you still have 2 replicas in sync, so writes continue. With `min.insync.replicas=3`, you would lose write availability — a setting that is sometimes used for maximum durability but is incompatible with AZ failure tolerance.

The subtlety is in the recovery. When the AZ comes back online and the broker restarts, it needs to catch up on all the messages it missed while it was down. For a high-throughput topic, this can mean replicating gigabytes of data, which puts additional load on the surviving brokers. If the catch-up process is slow, the cluster operates in a degraded state for an extended period — one more failure during this window would be catastrophic.

### The Playbook

```yaml
name: az-failure
description: Simulate availability zone failure by killing all brokers in a zone
category: infrastructure
steps:
  - name: kill-zone-brokers
    faultSpec:
      experimentName: az-failure-sim
      disruptionType: POD_KILL
      targetLabel: "topology.kubernetes.io/zone=zone-a"
      targetNamespace: kafka
      gracePeriodSec: 0
    steadyStateSec: 30
    observationWindowSec: 300
    requireRecovery: true
sla:
  maxP99LatencyMs: 500.0
  minThroughputRecPerSec: 5000.0
  maxRtoMs: 120000
maxAffectedBrokers: 3
autoRollback: true
```

### What to Look For

When you run this playbook, pay attention to these indicators:

**ISR behavior.** You should see ISR shrink for all partitions that had replicas on the killed brokers. The shrink should happen within `replica.lag.time.max.ms` (typically 10-30 seconds). Watch whether the ISR shrinks to 2 replicas (acceptable) or to 1 (dangerous — one more failure means data loss).

**Write availability.** If `min.insync.replicas=2` and the ISR shrinks to 2, writes continue. If it shrinks to 1, writes are rejected. This is the critical moment: does your cluster maintain write availability during an AZ failure?

**Recovery time.** The 300-second observation window gives the brokers time to restart and catch up. Look at how long it takes for the ISR to fully recover. For a lightly loaded cluster, this might take 30-60 seconds. For a cluster handling 100,000+ messages/second, it could take several minutes as the restarting brokers replicate missed data.

**The SLA grade** captures all of this into a single letter. An A means the cluster handled the AZ failure without noticeable impact. A B or C means there was degradation but within acceptable bounds. An F means the cluster's AZ resilience is fundamentally broken and needs architectural changes.

---

## leader-cascade — Cascading Leader Elections

### The Theory

Leader election is Kafka's mechanism for maintaining availability when brokers fail. When a partition's leader goes down, the controller elects a new leader from the ISR. This process typically takes a few seconds and, ideally, is invisible to well-configured clients.

A cascading leader election scenario tests what happens when leader failures chain together. You kill the leader of partition 0, a new leader is elected, then you kill that new leader. This tests whether the cluster can sustain multiple rapid leadership transitions without falling into an unstable state where leadership bounces between brokers faster than clients can update their metadata.

This scenario is more realistic than it sounds. During a rolling deployment, brokers restart one at a time. If each restart triggers leader elections, and the restart interval is shorter than the time it takes for clients to stabilize after an election, you get a cascade of elections that degrades overall throughput even though no single failure is severe.

### The Playbook

```yaml
name: leader-cascade
description: Test cascading leader elections by killing partition leaders sequentially
category: kafka-specific
steps:
  - name: kill-leader-p0
    faultSpec:
      experimentName: leader-cascade-p0
      disruptionType: POD_KILL
      targetTopic: test-topic
      targetPartition: 0
      targetNamespace: kafka
      gracePeriodSec: 0
    steadyStateSec: 10
    observationWindowSec: 60
    requireRecovery: true
  - name: kill-leader-p1
    faultSpec:
      experimentName: leader-cascade-p1
      disruptionType: POD_KILL
      targetTopic: test-topic
      targetPartition: 1
      targetNamespace: kafka
      gracePeriodSec: 0
    steadyStateSec: 10
    observationWindowSec: 60
    requireRecovery: true
  - name: kill-leader-p2
    faultSpec:
      experimentName: leader-cascade-p2
      disruptionType: POD_KILL
      targetTopic: test-topic
      targetPartition: 2
      targetNamespace: kafka
      gracePeriodSec: 0
    steadyStateSec: 10
    observationWindowSec: 60
    requireRecovery: true
sla:
  maxP99LatencyMs: 200.0
  minThroughputRecPerSec: 10000.0
  maxRtoMs: 30000
maxAffectedBrokers: 1
autoRollback: true
```

### What to Look For

The key observation here is whether each successive election is faster, slower, or about the same as the previous one. If the cluster is healthy and well-configured, each election should take roughly the same amount of time (typically 5-15 seconds). If elections get progressively slower, it may indicate that the controller is becoming overloaded with metadata operations.

Also watch the `steadyStateSec: 10` between steps. This deliberately short interval means the next kill happens only 10 seconds after the previous step completes. If the killed broker has not fully recovered by then, you are testing recovery under compounding stress — which is exactly the point.

---

## split-brain — Network Partition of the Controller

### The Theory

In Kafka, the controller is the broker (in KRaft mode) or ZooKeeper leader that manages cluster metadata: partition assignments, leader elections, topic creation, and configuration changes. A network partition that isolates the controller from the rest of the cluster is one of the most serious failures possible.

When the controller is partitioned:
- No new leader elections can occur, so any broker failure during the partition causes indefinite unavailability for affected partitions
- No topic or partition changes can be committed to the metadata log
- No configuration changes can propagate
- Existing reads and writes continue on established leaders (the data plane is independent of the control plane), but the cluster cannot adapt to any changes

The danger of a split-brain is not immediate catastrophe — it is that the cluster appears to be working while losing its ability to react to problems. If a broker fails while the controller is partitioned, you get a compounding failure that no automated process can resolve until the partition heals.

### The Playbook

```yaml
name: split-brain
description: Simulate network partition isolating the controller broker
category: network
steps:
  - name: isolate-controller
    faultSpec:
      experimentName: split-brain-sim
      disruptionType: NETWORK_PARTITION
      targetLabel: "strimzi.io/controller=true"
      targetNamespace: kafka
      chaosDurationSec: 60
    steadyStateSec: 30
    observationWindowSec: 180
    requireRecovery: true
sla:
  maxP99LatencyMs: 500.0
  minThroughputRecPerSec: 5000.0
  maxRtoMs: 60000
maxAffectedBrokers: 1
autoRollback: true
```

### What to Look For

During the 60-second partition window, existing producers and consumers should continue working normally — the data plane is separate from the control plane. The real test is what happens when the partition heals. The controller needs to reconcile its state with the rest of the cluster, which may involve metadata log catchup and potentially re-electing leaders.

Watch the Strimzi state tracker output — it records transitions in the Kafka custom resource's `Ready` condition, which can reveal whether the Strimzi operator detected the partition and took any corrective action.

---

## storage-pressure — Disk Exhaustion

### The Theory

Kafka brokers store messages in log segments on disk. When a disk fills up, the broker can no longer accept new messages. The behavior depends on the broker's configuration: with `log.retention.hours` and `log.segment.bytes`, the broker periodically deletes old segments to free space. But if the incoming data rate exceeds the retention-based deletion rate, disk usage climbs until the broker runs out of space.

Storage pressure tests are particularly important because disk usage issues are slow-building — they do not cause immediate failures, they cause gradual degradation that eventually crosses a cliff. By the time your monitoring alerts fire, you may already be in a critical state. Running this playbook lets you see exactly where that cliff is and how your cluster behaves when it hits it.

### The Playbook

```yaml
name: storage-pressure
description: Simulate storage exhaustion by filling broker log directories
category: resource
steps:
  - name: fill-disk
    faultSpec:
      experimentName: storage-pressure-sim
      disruptionType: DISK_FILL
      targetLabel: "strimzi.io/component-type=kafka"
      targetNamespace: kafka
      fillPercentage: 90
      chaosDurationSec: 120
    steadyStateSec: 30
    observationWindowSec: 180
    requireRecovery: true
sla:
  maxP99LatencyMs: 1000.0
  minThroughputRecPerSec: 1000.0
  maxErrorPercent: 5.0
maxAffectedBrokers: 3
autoRollback: true
```

### What to Look For

Watch the transition from normal operation to degraded behavior. At 85-90% disk usage, you may see increased latency as the broker's log segment management becomes more aggressive. At 95%+, the broker may start refusing writes. The observation window shows whether log retention cleanup frees enough space to restore write availability.

The `maxAffectedBrokers: 3` setting acknowledges that this playbook fills disks on all brokers simultaneously — this tests the worst-case scenario where the entire cluster is under storage pressure.

---

## rolling-restart — Zero-Downtime Maintenance

### The Theory

Rolling restarts are the most common operational event in a Kafka cluster's life. Broker upgrades, configuration changes, JVM flag updates, certificate rotations — all require restarting brokers one at a time. In theory, a rolling restart should be invisible to clients. In practice, it depends on several factors:

- **Controlled shutdown duration.** Each broker needs time to transfer leadership before shutting down. If the shutdown timeout is too short, leadership transfer is incomplete and clients see errors.
- **Readiness probe timing.** The next broker should not restart until the previous one is fully ready. If readiness probes are too lenient, two brokers may be down simultaneously.
- **Replication catch-up.** After a broker restarts, it needs to catch up on messages it missed. During this window, the ISR is short by one member.

This playbook validates that your StatefulSet rolling restart strategy, combined with your Strimzi operator configuration, actually achieves zero-downtime.

### The Playbook

```yaml
name: rolling-restart
description: Trigger a graceful rolling restart of the Kafka StatefulSet
category: operational
steps:
  - name: restart-statefulset
    faultSpec:
      experimentName: rolling-restart-sim
      disruptionType: ROLLING_RESTART
      targetLabel: "strimzi.io/component-type=kafka"
      targetNamespace: kafka
      chaosDurationSec: 300
      gracePeriodSec: 60
    steadyStateSec: 30
    observationWindowSec: 300
    requireRecovery: true
sla:
  maxP99LatencyMs: 200.0
  minThroughputRecPerSec: 8000.0
  maxErrorPercent: 0.0
maxAffectedBrokers: 1
autoRollback: false
```

### What to Look For

The critical question is: does `maxErrorPercent: 0.0` pass? If it does, your rolling restart is truly zero-downtime — no client-visible errors at any point during the restart. If it fails, you need to investigate your controlled shutdown settings, readiness probe configuration, or producer retry policies.

The `autoRollback: false` setting is deliberate — you do not want to undo a rolling restart midway through, as that would leave the cluster in a partially updated state.

---

## consumer-isolation — Consumer Network Partition

### The Theory

Most chaos experiments focus on broker failures. But in many architectures, the consumer applications are just as critical as the brokers. If your consumers lose connectivity to the Kafka brokers — due to a network issue, a misconfigured NetworkPolicy, or a cloud networking change — they stop processing messages. Unlike broker failures, which Kafka handles internally through replication and leader election, consumer failures are entirely in your application's domain.

When a consumer is network-partitioned from the brokers, several things happen. The consumer's heartbeat to the group coordinator times out, triggering a consumer group rebalance. The partitioned consumer's assigned partitions are reassigned to surviving consumers (if any). The partitioned consumer may attempt to reconnect, and when connectivity is restored, it rejoins the group and triggers another rebalance.

This playbook tests whether your consumer application handles this gracefully: Does it buffer partial results? Does it commit offsets correctly on reconnection? Does the rebalance complete within an acceptable time?

### The Playbook

```yaml
name: consumer-isolation
description: Network-partition consumer pods from Kafka brokers
category: network
steps:
  - name: isolate-consumers
    faultSpec:
      experimentName: consumer-isolation-sim
      disruptionType: NETWORK_PARTITION
      targetLabel: "app=my-consumer"
      targetNamespace: default
      chaosDurationSec: 60
    steadyStateSec: 30
    observationWindowSec: 120
    requireRecovery: true
sla:
  maxP99LatencyMs: 500.0
  maxConsumerLagRecords: 100000
  maxRtoMs: 90000
maxAffectedBrokers: 0
autoRollback: true
```

### What to Look For

The `maxAffectedBrokers: 0` indicates that this playbook does not target any Kafka brokers — it targets consumer pods. The key metric here is `maxConsumerLagRecords: 100000`. During the 60-second partition, lag will accumulate. The observation window then tracks how quickly the consumers catch up after connectivity is restored.

Also watch the lag recovery pattern. Healthy consumers should show a rapid, monotonically decreasing lag after reconnection. If the lag decreases then increases again (oscillates), it may indicate a rebalance storm where consumers keep joining and leaving the group.

---

## Writing Your Own Playbooks

The built-in playbooks cover the most common failure scenarios, but your system may have unique failure modes that require custom playbooks. Writing a custom playbook is straightforward: create a YAML file in the `src/main/resources/playbooks/` directory following the same schema as the built-in playbooks.

### Design Principles

When designing a custom playbook, think like a scientist:

1. **Start with a hypothesis.** "Our order processing pipeline can survive a 60-second network partition without losing any messages." This hypothesis defines what you are testing and how you will evaluate the results.

2. **Choose the right disruption type.** Match the disruption to the failure mode you are testing. If you are worried about broker crashes, use `POD_KILL`. If you are worried about network issues, use `NETWORK_PARTITION`. Do not use `POD_KILL` to test network resilience — the recovery path is completely different.

3. **Set realistic SLA thresholds.** Your SLA should reflect your actual business requirements, not aspirational targets. If your application can tolerate 500ms P99 latency during failures, set `maxP99LatencyMs: 500.0`. If your SLA is too tight, every experiment will get an F, which makes the grade meaningless.

4. **Use appropriate observation windows.** The observation window must be long enough for recovery to complete. A good rule of thumb: set it to at least 3× your expected recovery time. If you expect ISR to recover in 30 seconds, use `observationWindowSec: 120`.

5. **Iterate.** Run the playbook, review the results, adjust the parameters, and run again. Chaos engineering is an iterative practice, not a one-time event.

### Custom Playbook Template

```yaml
name: my-custom-playbook
description: Describe what failure mode this tests
category: custom
steps:
  - name: describe-the-fault
    faultSpec:
      experimentName: my-experiment
      disruptionType: POD_KILL
      targetTopic: my-topic
      targetPartition: 0
      targetNamespace: kafka
      gracePeriodSec: 0
    steadyStateSec: 30
    observationWindowSec: 120
    requireRecovery: true
sla:
  maxP99LatencyMs: 200.0
  minThroughputRecPerSec: 5000.0
  maxRtoMs: 60000
  maxDataLossPercent: 0.0
maxAffectedBrokers: 1
autoRollback: true
```

Save this file to `src/main/resources/playbooks/my-custom-playbook.yaml` and restart Kates. The playbook will be automatically discovered and available through the API.
