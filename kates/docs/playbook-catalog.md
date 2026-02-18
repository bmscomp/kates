# Playbook Catalog

This document is a reference for the 6 pre-built disruption playbooks that ship with Kates. Each playbook is a curated, multi-step disruption scenario stored as a YAML file in `src/main/resources/playbooks/`. Playbooks are loaded at startup by the `DisruptionPlaybookCatalog` and can be executed with a single API call.

## Executing a Playbook

```bash
curl -X POST http://localhost:8080/api/disruptions/playbooks/az-failure

curl -X POST http://localhost:8080/api/disruptions/playbooks/leader-cascade?dryRun=true
```

No JSON body is required — the playbook defines all steps, fault specifications, and safety limits.

## Listing Available Playbooks

```bash
curl http://localhost:8080/api/disruptions/playbooks
curl http://localhost:8080/api/disruptions/playbooks?category=network
```

---

## az-failure

**Category:** `infrastructure`
**Description:** Simulate an availability zone failure by killing all brokers in one rack.

This playbook targets all Kafka broker pods in a specific availability zone (Kubernetes topology zone). It simulates the worst-case scenario of an entire AZ going down — a common failure mode in cloud environments.

**What it tests:**

- Can the cluster serve reads and writes with an entire AZ offline?
- Do partition leaders fail over to brokers in other AZs?
- How long does it take for ISR sets to recover after the AZ comes back?
- Does the producer `acks=all` configuration still work with reduced ISR?

**YAML source:**

```yaml
name: az-failure
description: "Simulate availability zone failure by killing all brokers in one rack"
category: infrastructure
maxAffectedBrokers: 3
autoRollback: true
steps:
  - name: kill-rack-0-brokers
    faultSpec:
      experimentName: az-failure-rack-0
      disruptionType: POD_KILL
      targetLabel: "strimzi.io/component-type=kafka,topology.kubernetes.io/zone=zone-a"
      chaosDurationSec: 30
      gracePeriodSec: 0
    steadyStateSec: 30
    observationWindowSec: 120
    requireRecovery: true
```

**Key observations:**

- `maxAffectedBrokers: 3` — allows the playbook to kill up to 3 brokers (all brokers in one AZ)
- `gracePeriodSec: 0` — SIGKILL, simulating a sudden power-off (no graceful shutdown)
- `observationWindowSec: 120` — 2 minutes for the cluster to recover and all ISR sets to be full again
- The compound label selector (`strimzi.io/component-type=kafka,topology.kubernetes.io/zone=zone-a`) ensures only brokers in zone-a are killed

**Expected result in a healthy 3-AZ cluster:** ISR shrinks immediately, new leaders elected within seconds, ISR fully recovered within 30-60 seconds after pods restart.

---

## leader-cascade

**Category:** `kafka`
**Description:** Kill partition leaders sequentially to test cascading election recovery.

This playbook kills the leaders of two different partitions of `__consumer_offsets` in sequence. It tests whether the cluster can handle rapid successive leader elections without causing a cascading failure.

**What it tests:**

- Can the cluster handle multiple leader elections in rapid succession?
- Does leader election for one partition interfere with another?
- How does the consumer offset commit mechanism behave during controller instability?

**YAML source:**

```yaml
name: leader-cascade
description: "Kill partition leaders sequentially to test cascading election recovery"
category: kafka
maxAffectedBrokers: 2
autoRollback: true
isrTrackingTopic: __consumer_offsets
steps:
  - name: kill-leader-partition-0
    faultSpec:
      experimentName: leader-cascade-p0
      disruptionType: POD_KILL
      targetTopic: __consumer_offsets
      targetPartition: 0
      chaosDurationSec: 10
      gracePeriodSec: 0
    steadyStateSec: 30
    observationWindowSec: 60
    requireRecovery: true
  - name: kill-leader-partition-1
    faultSpec:
      experimentName: leader-cascade-p1
      disruptionType: POD_KILL
      targetTopic: __consumer_offsets
      targetPartition: 1
      chaosDurationSec: 10
      gracePeriodSec: 0
    steadyStateSec: 15
    observationWindowSec: 60
    requireRecovery: true
```

**Key observations:**

- `isrTrackingTopic: __consumer_offsets` — ISR tracking is enabled for the consumer offsets topic, which is critical for consumer group coordination
- The second step has a shorter `steadyStateSec: 15` — less time between faults, increasing pressure on the cluster
- Both steps use `targetTopic` and `targetPartition` — Kates resolves the leader broker at execution time

---

## split-brain

**Category:** `network`
**Description:** Network-partition the controller/leader broker from all followers.

This playbook isolates the controller broker by dropping all network traffic to and from it. This simulates a network split where the controller thinks it's still alive but cannot communicate with the rest of the cluster.

**What it tests:**

- Does a new controller get elected when the current one is unreachable?
- How do clients handle the metadata refresh when the controller changes?
- Is there any data loss during the partition window?
- How long does it take for the cluster to fully recover after the partition heals?

**YAML source:**

```yaml
name: split-brain
description: "Network-partition the controller/leader broker from all followers"
category: network
maxAffectedBrokers: 1
autoRollback: true
steps:
  - name: isolate-controller
    faultSpec:
      experimentName: split-brain-partition
      disruptionType: NETWORK_PARTITION
      targetLabel: "strimzi.io/component-type=kafka"
      targetBrokerId: 0
      chaosDurationSec: 60
    steadyStateSec: 30
    observationWindowSec: 90
    requireRecovery: true
```

**Key observations:**

- `NETWORK_PARTITION` — this typically requires Litmus ChaosEngine CRDs (the `LitmusChaosProvider` creates a `pod-network-partition` experiment)
- `targetBrokerId: 0` — targets broker 0 specifically. Combined with the label selector, Kates resolves this to the exact pod
- `chaosDurationSec: 60` — the network partition lasts 60 seconds
- `autoRollback: true` — after the chaos duration, the network partition is actively removed

---

## storage-pressure

**Category:** `storage`
**Description:** Fill broker log directories to 90% to simulate storage exhaustion.

This playbook fills the broker's persistent volume to 90% capacity, simulating what happens when a broker runs out of disk space. Kafka brokers handle disk pressure by rejecting produce requests and triggering log segment cleanup.

**What it tests:**

- Does the broker correctly reject writes when disk is near-full?
- Does log segment retention kick in to free space?
- Do producers fail gracefully with retriable errors?
- How long does it take to recover after disk space is freed?

**YAML source:**

```yaml
name: storage-pressure
description: "Fill broker log directories to 90% to simulate storage exhaustion"
category: storage
maxAffectedBrokers: 1
autoRollback: true
steps:
  - name: fill-broker-disk
    faultSpec:
      experimentName: storage-pressure-fill
      disruptionType: DISK_FILL
      targetLabel: "strimzi.io/component-type=kafka"
      targetBrokerId: 0
      fillPercentage: 90
      chaosDurationSec: 120
    steadyStateSec: 30
    observationWindowSec: 120
    requireRecovery: true
```

**Key observations:**

- `fillPercentage: 90` — fills the disk to 90%, not 100%. This tests the broker's behavior at near-full capacity without risking a complete filesystem freeze
- `chaosDurationSec: 120` — the disk stays filled for 2 minutes, giving enough time to observe the broker's response
- `autoRollback: true` — critically important for disk fill tests. On rollback, the filled temporary files are removed

---

## rolling-restart

**Category:** `operations`
**Description:** Trigger a graceful rolling restart of the Kafka StatefulSet.

This playbook simulates a routine operational procedure — a rolling restart of all Kafka brokers. This is the most common disruption in production environments (config changes, Kafka version upgrades, certificate rotation).

**What it tests:**

- Is the rolling restart truly zero-downtime?
- How long does each broker take to rejoin the ISR?
- Do producers and consumers experience any errors during the restart?
- Is the total restart time within operational SLA?

**YAML source:**

```yaml
name: rolling-restart
description: "Trigger a graceful rolling restart of the Kafka StatefulSet"
category: operations
maxAffectedBrokers: 1
autoRollback: false
steps:
  - name: rolling-restart-brokers
    faultSpec:
      experimentName: rolling-restart-sts
      disruptionType: ROLLING_RESTART
      targetLabel: "strimzi.io/component-type=kafka"
      chaosDurationSec: 300
      gracePeriodSec: 30
    steadyStateSec: 30
    observationWindowSec: 180
    requireRecovery: true
```

**Key observations:**

- `autoRollback: false` — a rolling restart cannot be "rolled back". This is an intentional design choice — the restart must complete naturally
- `ROLLING_RESTART` — the `KubernetesChaosProvider` implements this by annotating the StatefulSet's pod template to trigger a rollout
- `chaosDurationSec: 300` — the total restart can take up to 5 minutes for a 3-broker cluster
- `gracePeriodSec: 30` — each pod gets 30 seconds to perform a graceful shutdown (flush logs, transfer leadership)
- `observationWindowSec: 180` — 3 minutes after the restart completes to verify full ISR recovery

---

## consumer-isolation

**Category:** `network`
**Description:** Network-partition consumer pods from Kafka brokers to test consumer resilience.

This playbook isolates consumer application pods from the Kafka cluster, simulating a network outage between consumers and brokers. This tests consumer reconnection logic, rebalance behavior, and offset commit handling.

**What it tests:**

- Do consumers reconnect after the network partition heals?
- Does the consumer group rebalance correctly?
- Are committed offsets preserved across the partition?
- How much lag accumulates during the outage and how quickly is it recovered?

**YAML source:**

```yaml
name: consumer-isolation
description: "Network-partition consumer pods from Kafka brokers to test consumer resilience"
category: network
maxAffectedBrokers: -1
autoRollback: true
steps:
  - name: partition-consumers
    faultSpec:
      experimentName: consumer-isolation-net
      disruptionType: NETWORK_PARTITION
      targetLabel: "app=kafka-consumer"
      targetNamespace: kates
      chaosDurationSec: 60
    steadyStateSec: 30
    observationWindowSec: 90
    requireRecovery: true
```

**Key observations:**

- `maxAffectedBrokers: -1` — this playbook targets consumers, not brokers. The value `-1` disables the broker blast radius check
- `targetLabel: "app=kafka-consumer"` — targets consumer pods, not Kafka broker pods
- `targetNamespace: kates` — consumer pods are in the kates namespace, not the kafka namespace
- This playbook is best combined with `lagTrackingGroupId` to monitor the consumer group's lag during and after the partition

## Writing Custom Playbooks

To add a new playbook, create a YAML file in `src/main/resources/playbooks/` following this format:

```yaml
name: my-custom-playbook
description: "Description of what this playbook tests"
category: custom
maxAffectedBrokers: 1
autoRollback: true
isrTrackingTopic: my-topic
lagTrackingGroupId: my-consumer-group
steps:
  - name: step-name
    faultSpec:
      experimentName: unique-experiment-name
      disruptionType: POD_KILL
      targetLabel: "strimzi.io/component-type=kafka"
      chaosDurationSec: 30
    steadyStateSec: 30
    observationWindowSec: 60
    requireRecovery: true
```

The playbook is automatically loaded by `DisruptionPlaybookCatalog` on application startup and becomes available via `GET /api/disruptions/playbooks` and `POST /api/disruptions/playbooks/{name}`.
