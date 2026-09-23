# Playbook Catalog

When you first start with chaos engineering, the hardest part is not running the experiment — it is designing one. What should you test? What failure scenarios matter? How do you set the parameters so the experiment is meaningful without being reckless?

Kates solves this cold-start problem with a catalog of six built-in playbooks. Each playbook encodes a well-tested disruption scenario based on real-world Kafka failure patterns. They are not toy examples — they represent the failure modes that have taken down production Kafka clusters at organizations around the world.

This chapter walks through each playbook in detail: the theory behind the failure it simulates, the YAML source that defines it, what you should observe when you run it, and what the results tell you about your cluster.

## How Playbooks Work

A playbook is a YAML file stored in `src/main/resources/playbooks/`. At startup, the `DisruptionPlaybookCatalog` loads the playbooks named in its `PLAYBOOK_NAMES` array from that directory and registers them in an in-memory catalog; running one converts it into a `DisruptionPlan`. It does not scan the directory, so a file whose name is not in the array is never loaded. You can list and execute playbooks through the REST API.

### Listing Available Playbooks

```bash
curl -s http://localhost:8080/api/disruptions/playbooks | jq '.[].name'
```

Returns: `az-failure`, `leader-cascade`, `split-brain`, `storage-pressure`, `rolling-restart`, `consumer-isolation`.

### Executing a Playbook

```bash
curl -X POST http://localhost:8080/api/disruptions/playbooks/az-failure | jq
```

This submits the playbook as a `DisruptionPlan` to the `DisruptionOrchestrator`, which executes it through the same 13-step pipeline described in the [Disruption Guide](disruption-guide.md). The call returns `202 Accepted` with a report id; poll `GET /api/disruptions/{id}` for progress and the final `DisruptionReport`. A playbook carries no SLA thresholds, so its report has no SLA grade — for a graded run, submit the plan yourself (see [Submitting a Plan Instead](#submitting-a-plan-instead)).

---

## az-failure — Availability Zone Failure

### The Theory

Cloud providers organize their infrastructure into availability zones (AZs) — physically separated data centers within a region that share a metropolitan fiber network but have independent power, cooling, and networking. The promise of multi-AZ deployment is that losing an entire AZ should not cause an outage.

For Kafka, this promise depends critically on how your brokers are distributed across AZs and how your topic partitions are replicated. If you have 3 brokers spread across 3 AZs with `replication.factor=3`, then each partition has one replica per AZ. Losing an AZ kills one broker, which means one replica per partition is lost. With `min.insync.replicas=2`, you still have 2 replicas in sync, so writes continue. With `min.insync.replicas=3`, you would lose write availability — a setting that is sometimes used for maximum durability but is incompatible with AZ failure tolerance.

The subtlety is in the recovery. When the AZ comes back online and the broker restarts, it needs to catch up on all the messages it missed while it was down. For a high-throughput topic, this can mean replicating gigabytes of data, which puts additional load on the surviving brokers. If the catch-up process is slow, the cluster operates in a degraded state for an extended period — one more failure during this window would be catastrophic.

### The Playbook

```yaml
name: az-failure
description: "Simulate an availability zone failure by killing every Kafka pod in zone alpha"
category: infrastructure
maxAffectedBrokers: 3
autoRollback: true
steps:
  - name: kill-zone-alpha
    faultSpec:
      experimentName: az-failure-alpha
      disruptionType: POD_KILL
      targetLabel: "strimzi.io/component-type=kafka,zone=alpha"
      targetAll: true
      chaosDurationSec: 30
      gracePeriodSec: 0
    steadyStateSec: 30
    observationWindowSec: 120
    requireRecovery: true
```

### How the Zone Is Targeted

`topology.kubernetes.io/zone` is a *node* label; Kubernetes does not copy it onto the pods scheduled there, so a pod selector on it matches nothing. The playbook selects on the pod label `zone: alpha` instead, which the `kafka-cluster` chart puts on every pod of a node pool pinned with `zone:` — `kates detect --generate-values` pins one broker pool to each of the lab's `alpha`, `sigma` and `gamma` zones. `targetLabel` is a full Kubernetes label selector, so both requirements must hold.

`targetAll: true` is what makes this a zone failure: without it, a label-targeted fault hits one random pod the selector matches. With it, every matching pod is killed, and the safety guard counts each one against `maxAffectedBrokers` — a zone holding more than three Kafka pods is rejected before anything is killed. The direct Kubernetes backend kills them all at once. The Litmus backend hands them to `pod-delete` as one `TARGET_PODS` list, which it runs with `SEQUENCE=serial`, so it deletes them one at a time.

If your pools spread across zones without a `zone:` pin, no pod carries the label: the dry run warns that the selector matches no broker pod and the step fails with `No pods found matching label selector`. To fail another zone, post the same step to `POST /api/disruptions` with the selector changed; built-in playbooks take no parameters.

### What to Look For

When you run this playbook, pay attention to these indicators:

**ISR behavior.** You should see ISR shrink for all partitions that had replicas on the killed brokers. The shrink should happen within `replica.lag.time.max.ms` (typically 10-30 seconds). Watch whether the ISR shrinks to 2 replicas (acceptable) or to 1 (dangerous — one more failure means data loss).

**Write availability.** If `min.insync.replicas=2` and the ISR shrinks to 2, writes continue. If it shrinks to 1, writes are rejected. This is the critical moment: does your cluster maintain write availability during an AZ failure?

**Recovery time.** The 120-second observation window gives the brokers time to restart and catch up. Look at how long it takes for the ISR to fully recover. For a lightly loaded cluster, this might take 30-60 seconds. For a cluster handling 100,000+ messages/second, it could take several minutes as the restarting brokers replicate missed data.

**The SLA grade** captures all of this into a single letter — but only for a plan that defines SLA thresholds, which the playbook does not. To get one, post its step to `POST /api/disruptions` with an `sla` block (see [Submitting a Plan Instead](#submitting-a-plan-instead)). An A means the cluster handled the AZ failure without noticeable impact. A B or C means there was degradation but within acceptable bounds. An F means the cluster's AZ resilience is fundamentally broken and needs architectural changes.

---

## leader-cascade — Cascading Leader Elections

### The Theory

Leader election is Kafka's mechanism for maintaining availability when brokers fail. When a partition's leader goes down, the controller elects a new leader from the ISR. This process typically takes a few seconds and, ideally, is invisible to well-configured clients.

A cascading leader election scenario tests what happens when leader failures chain together. You kill the leader of partition 0, a new leader is elected, then you kill the leader of partition 1 — which may be a broker that just took over leadership. This tests whether the cluster can sustain multiple rapid leadership transitions without falling into an unstable state where leadership bounces between brokers faster than clients can update their metadata.

This scenario is more realistic than it sounds. During a rolling deployment, brokers restart one at a time. If each restart triggers leader elections, and the restart interval is shorter than the time it takes for clients to stabilize after an election, you get a cascade of elections that degrades overall throughput even though no single failure is severe.

### The Playbook

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

### How the Leaders Are Targeted

Neither step names a pod. Each names a partition of `__consumer_offsets`, and when the step starts, Kates looks up that partition's current leader and kills its pod. `__consumer_offsets` exists on any cluster that has served a consumer group, so the playbook needs no topic of your own, and `isrTrackingTopic: __consumer_offsets` records the ISR shrinking and recovering in each step. The second lookup happens after the first election has settled, so the two steps normally kill two different brokers — which is what `maxAffectedBrokers: 2` allows for.

To run the cascade against one of your own topics, post the same steps to `POST /api/disruptions` with `targetTopic` changed; built-in playbooks take no parameters.

### What to Look For

The key observation here is whether the second election is faster, slower, or about the same as the first. If the cluster is healthy and well-configured, both should take roughly the same amount of time (typically 5-15 seconds). If the second is noticeably slower, it may indicate that the controller is becoming overloaded with metadata operations.

Also watch the gap between the steps. The first step waits `steadyStateSec: 30` before its kill; the second waits only 15. Both steps set `requireRecovery: true`, and Kates checks that every broker pod is Running and Ready before it injects a fault, so the second kill never lands while a broker pod is down — the step fails instead. It can land while the restarted broker is still catching up, though: a broker's pod turns Ready before the broker has rejoined the ISR of every partition it hosts. If the ISR tracking shows `__consumer_offsets` still under-replicated when the second kill lands, you are testing recovery under compounding stress — which is exactly the point.

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

### Which Node Is Isolated

The playbook does not look up the active controller. `targetBrokerId: 0` aims the fault at the pod whose name ends in `-0` among the pods labelled `strimzi.io/component-type=kafka` — node 0, whatever role your node pools give it. It is the active controller only if it leads the metadata quorum when the step runs; `kafka-metadata-quorum.sh --bootstrap-server <broker>:9092 describe --status` prints the quorum's `LeaderId`, so check it before you run the playbook.

With the direct Kubernetes backend, the partition is a NetworkPolicy that denies all ingress and egress for that pod, held for `chaosDurationSec: 60` and then deleted; the Litmus backend runs `pod-network-partition` instead. To isolate a different node, post the same step to `POST /api/disruptions` with `targetBrokerId` changed; built-in playbooks take no parameters.

### What to Look For

If node 0 is a dedicated controller, existing producers and consumers should continue working normally during the 60-second partition — the data plane is separate from the control plane. If it is also a broker, clients of the partitions it leads see errors until the controller moves leadership to another replica. The real test is what happens when the partition heals, which the 90-second observation window covers. The controller needs to reconcile its state with the rest of the cluster, which may involve metadata log catchup and potentially re-electing leaders.

Watch the Strimzi state tracker output — it records transitions in the Kafka custom resource's `Ready` condition, which can reveal whether the Strimzi operator detected the partition and took any corrective action.

---

## storage-pressure — Disk Exhaustion

### The Theory

Kafka brokers store messages in log segments on disk. When a disk fills up, the broker can no longer accept new messages. The behavior depends on the broker's configuration: with `log.retention.hours` and `log.segment.bytes`, the broker periodically deletes old segments to free space. But if the incoming data rate exceeds the retention-based deletion rate, disk usage climbs until the broker runs out of space.

Storage pressure tests are particularly important because disk usage issues are slow-building — they do not cause immediate failures, they cause gradual degradation that eventually crosses a cliff. By the time your monitoring alerts fire, you may already be in a critical state. Running this playbook lets you see exactly where that cliff is and how your cluster behaves when it hits it.

### The Playbook

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

### How the Disk Is Filled

`DISK_FILL` has no direct Kubernetes implementation; it runs only as the LitmusChaos `disk-fill` experiment. The playbook therefore needs the Litmus backend — the default `kates.chaos.provider=litmus-crd`, or `hybrid` with the Litmus CRDs installed. With the direct Kubernetes backend, the step fails as unsupported.

The step fills one broker's disk: `targetBrokerId: 0` aims it at node 0, and `maxAffectedBrokers: 1` allows no more. It fills the disk to 90% (`fillPercentage`) for 120 seconds (`chaosDurationSec`), then the 120-second observation window follows.

### What to Look For

Watch the transition from normal operation to degraded behavior. At 85-90% disk usage, you may see increased latency as the broker's log segment management becomes more aggressive. At 95%+, the broker may start refusing writes. The observation window shows whether log retention cleanup frees enough space to restore write availability.

Because only one broker's disk fills, this tests how the cluster copes with a single broker running short of space while its peers stay healthy — not the whole cluster under storage pressure.

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

### What to Look For

The critical question is whether clients see any errors at all during the restart. The playbook sets no SLA, so its report does not answer that with a grade: watch your producers' and consumers' error counts through the 180-second observation window, or submit the step as your own plan with an `sla` block that sets `maxErrorRate: 0` (see [Submitting a Plan Instead](#submitting-a-plan-instead)). No errors means your rolling restart is truly zero-downtime. If there are errors, you need to investigate your controlled shutdown settings, readiness probe configuration, or producer retry policies.

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

### What the Step Targets

The step cuts pods labelled `app=kafka-consumer` in the `kates` namespace off from the network. Kates deploys nothing with that label, so out of the box the step fails with `No pods found matching label selector`. Label your own consumer's pods `app=kafka-consumer` and run them in `kates`, or post the step to `POST /api/disruptions` with your consumer's label and namespace; built-in playbooks take no parameters.

`maxAffectedBrokers: -1` is the default, and it switches the broker cap off: the safety guard enforces `maxAffectedBrokers` only when it is greater than zero, so `0` would do the same. The playbook partitions consumers, not brokers, so a broker cap has nothing to limit. The guard's other checks still apply — it still requires broker pods to exist, and still rejects a plan that would disrupt every broker.

### What to Look For

During the 60-second partition, lag accumulates; the 90-second observation window that follows covers the catch-up after connectivity is restored. The playbook sets no `lagTrackingGroupId`, so its report carries no consumer-lag figures. Watch the group yourself with `kafka-consumer-groups.sh --bootstrap-server <broker>:9092 --describe --group <group>`, or post the step as your own plan with `lagTrackingGroupId` set to your consumer group, and the report records the baseline lag, the peak and the time to recover.

Also watch the lag recovery pattern. Healthy consumers should show a rapid, monotonically decreasing lag after reconnection. If the lag decreases then increases again (oscillates), it may indicate a rebalance storm where consumers keep joining and leaving the group.

---

## Writing Your Own Playbooks

The built-in playbooks cover the most common failure scenarios, but your system may have unique failure modes that require custom playbooks. Writing a custom playbook is straightforward: create a YAML file in the `src/main/resources/playbooks/` directory following the same schema as the built-in playbooks, then register it with the catalog. If you would rather not rebuild Kates, submit the same steps as a plan instead.

### Design Principles

When designing a custom playbook, think like a scientist:

1. **Start with a hypothesis.** "Our order processing pipeline can survive a 60-second network partition without losing any messages." This hypothesis defines what you are testing and how you will evaluate the results.

2. **Choose the right disruption type.** Match the disruption to the failure mode you are testing. If you are worried about broker crashes, use `POD_KILL`. If you are worried about network issues, use `NETWORK_PARTITION`. Do not use `POD_KILL` to test network resilience — the recovery path is completely different.

3. **Set realistic SLA thresholds.** A playbook cannot carry an SLA; only a plan submitted to `POST /api/disruptions` can (see [Submitting a Plan Instead](#submitting-a-plan-instead)). Your SLA should reflect your actual business requirements, not aspirational targets. If your application can tolerate 500ms P99 latency during failures, set `maxP99LatencyMs: 500.0`. If your SLA is too tight, every experiment will get an F, which makes the grade meaningless.

4. **Use appropriate observation windows.** The observation window must be long enough for recovery to complete. A good rule of thumb: set it to at least 3× your expected recovery time. If you expect ISR to recover in 30 seconds, use `observationWindowSec: 120`.

5. **Iterate.** Run the playbook, review the results, adjust the parameters, and run again. Chaos engineering is an iterative practice, not a one-time event.

### Custom Playbook Template

```yaml
name: my-custom-playbook
description: Describe what failure mode this tests
category: custom
maxAffectedBrokers: 1
autoRollback: true
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
```

### Registering the Playbook

Save this file to `src/main/resources/playbooks/my-custom-playbook.yaml`, add `"my-custom-playbook"` to the `PLAYBOOK_NAMES` array in `DisruptionPlaybookCatalog`, then rebuild and redeploy Kates. The catalog loads only the names in that array, and it reads them from the classpath, so a file that is not listed there never appears in the API, and neither does one added after Kates was built.

The loader also rejects keys it does not know. It accepts the top-level fields `name`, `description`, `category`, `maxAffectedBrokers`, `autoRollback`, `isrTrackingTopic` and `steps`, and in each step only the fields the playbooks in this chapter use. Anything else — an `sla:` block, say — makes the file fail to parse: Kates logs `Failed to load playbook` and leaves it out of the catalog.

### Submitting a Plan Instead

`POST /api/disruptions` takes the same plan as JSON and needs no rebuild. It also accepts what a playbook cannot carry: an `sla` block, which gets the run an SLA grade, and `lagTrackingGroupId`, which records a consumer group's lag. [Anatomy of a Plan](disruption-guide.md#anatomy-of-a-plan) in the Disruption Guide shows the format, and adding `?dryRun=true` shows what a plan would hit without running it.

Write each `faultSpec` out in full. A JSON fault spec does not get the defaults a playbook step gets: an omitted `targetNamespace` or `targetLabel` is unset rather than `kafka` and `strimzi.io/component-type=kafka`, and an omitted `targetBrokerId` is `0` — node 0 — rather than `-1`, a random pod.
