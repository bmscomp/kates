# Kates Chaos Engine — Specification and Implementation Plan

|                     |                                                                                                                                                                                                                            |
| ------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Document**        | `specs/chaos.md`                                                                                                                                                                                                           |
| **Status**          | Draft for review                                                                                                                                                                                                           |
| **Date**            | 2026-09-22                                                                                                                                                                                                                 |
| **Scope**           | A Rust-built, Strimzi-native chaos engine for Apache Kafka that replaces the LitmusChaos execution plane in Kates                                                                                                          |
| **Replaces**        | `charts/kates-chaos` → `litmus-core` 3.28 subchart, `LitmusChaosProvider`, `chaos/litmus/*` models, `config/litmus/**`, Litmus images in `images.env`                                                                      |
| **Keeps**           | Everything above the `ChaosProvider` SPI: disruption plans, playbooks, scheduler, plan validation, Kafka intelligence (leader resolution, ISR/lag trackers), Prometheus capture, SLA grading, reports, REST API, gRPC, CLI |
| **Adds**            | `chaos/` — a Rust workspace producing `kates-chaos-controller` and `kates-chaos-agent`; three CRDs in API group `chaos.kates.io`; chart `kates-chaos` 3.0.0                                                                |
| **Target platform** | Kubernetes ≥ 1.32 (Kind 1.34 in development), containerd or CRI-O, cgroup v2, Strimzi 1.x (`v1` APIs), Kafka 4.x in KRaft mode                                                                                             |

---

## Contents

**Part I — Context**
1. [Purpose, scope, audience](#1-purpose-scope-audience)
2. [Glossary](#2-glossary)
3. [How chaos works in Kates today](#3-how-chaos-works-in-kates-today)
4. [Defect register](#4-defect-register)
5. [Environment facts that shape the design](#5-environment-facts-that-shape-the-design)

**Part II — Design**

6. [Design principles](#6-design-principles)
7. [Concepts borrowed from Litmus, re-designed](#7-concepts-borrowed-from-litmus-re-designed)
8. [Architecture](#8-architecture)
9. [API reference](#9-api-reference)
10. [Target resolution](#10-target-resolution)
11. [Fault catalog](#11-fault-catalog)
12. [Living with the Strimzi Cluster Operator](#12-living-with-the-strimzi-cluster-operator)
13. [Lifecycle and state machine](#13-lifecycle-and-state-machine)
14. [Revert guarantees](#14-revert-guarantees)
15. [Safety model](#15-safety-model)
16. [Steady-state checks](#16-steady-state-checks)
17. [Time and measurement semantics](#17-time-and-measurement-semantics)
18. [Kates integration](#18-kates-integration)
19. [Packaging and deployment](#19-packaging-and-deployment)
20. [Rust implementation](#20-rust-implementation)
21. [Security](#21-security)
22. [Observability](#22-observability)
23. [Verification strategy](#23-verification-strategy)

**Part III — Implementation plan**

24. [Plan overview](#24-plan-overview)
25. [Milestones and work packages](#25-milestones-and-work-packages)
26. [Pull-request sequence](#26-pull-request-sequence)
27. [Definition of done](#27-definition-of-done)
28. [Risks, open questions, decision log](#28-risks-open-questions-decision-log)

**Appendices**

A. [Parity matrix](#appendix-a--parity-matrix)
B. [Example resources](#appendix-b--example-resources)
C. [Kafka and Strimzi settings that govern expected timings](#appendix-c--kafka-and-strimzi-settings-that-govern-expected-timings)

---

# Part I — Context

## 1. Purpose, scope, audience

Kates exists to answer one question about a Kafka cluster: *what happens to throughput, latency, durability, and availability when something breaks, and does the cluster recover inside its SLA?* Everything Kates reports — the A–F grade, RPO/RTO, time-to-first-ready, ISR recovery time, lag peaks — is anchored to one instant: **the moment the fault took effect**. It is also bounded by one promise: **the fault goes away when the experiment says it does**.

Today Kates delegates fault injection to LitmusChaos. Litmus is a general-purpose chaos platform for any Kubernetes workload. It knows nothing about Kafka, Strimzi, KRaft quorums, partition leadership, or in-sync replicas. Kates compensates in Java, but the seams leak: faults hit the wrong pod, start times are wrong by the time it takes to schedule pods, some mapped experiments are not installed, and the probes that are supposed to guard the cluster pass when they cannot connect (§4).

This document specifies a replacement engine, written in Rust, that is **Kafka-first and Strimzi-native**:

- It picks targets in Kafka terms: partition leader, active KRaft controller, group coordinator, broker vs. controller role, node pool, and availability zone.
- It computes blast radius in Kafka terms: per-partition in-sync replicas against `min.insync.replicas`, and KRaft voter majority.
- It cooperates with the Strimzi Cluster Operator instead of fighting it.
- It reports fault timing with sub-millisecond precision.
- It guarantees revert even when Kates, the engine's controller, the node agent, or the API server fail mid-experiment.

**Audience.** Kates maintainers who will build the engine, reviewers who will approve the design, and operators who will run it against their Strimzi clusters.

**Out of scope for this document.** Kates' performance engine, the Rust load-generation worker discussed separately, and anything about clusters not managed by Strimzi.

---

## 2. Glossary

| Term                        | Meaning in this document                                                                                                                                           |
| --------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **Fault**                   | A single, bounded perturbation of a Kafka cluster (kill a pod, add latency, fill a disk), declared as a `ChaosFault` resource.                                     |
| **Fault type**              | The mechanism, e.g. `PodKill`, `NetworkLatency`. It says *what* happens.                                                                                           |
| **Selector**                | Which Kafka nodes a fault targets, e.g. `leaderOf`, `activeController`, `zone`. It says *to whom*. Fault types and selectors are orthogonal.                       |
| **Target**                  | One resolved pod, and a container within it, that a fault acts on. A fault has one or more targets.                                                                |
| **Injection**               | The node-local application of a fault to one target, declared as a `ChaosInjection` resource and executed by the agent. API-level faults have no injection object. |
| **Unavailable-class fault** | A fault that removes a Kafka node from service: kill, delete, hold-down, drain, partition, pause, zone outage. Counts against availability budgets.                |
| **Degraded-class fault**    | A fault that slows a Kafka node but leaves it in service: latency, loss, bandwidth, stress, fill, throttle, DNS errors.                                            |
| **Check**                   | A steady-state assertion evaluated by the controller: Kafka-native, Kubernetes, HTTP, PromQL, or exec.                                                             |
| **Revert**                  | Undoing everything a fault did. Revert is idempotent and journaled.                                                                                                |
| **Journal**                 | The write-ahead record of every mutation and its undo, persisted *before* the mutation happens.                                                                    |
| **Deadline**                | The absolute time by which a fault must be reverted regardless of any other state. The agent enforces it locally.                                                  |
| **Run**                     | One execution of a Kates disruption plan, identified by `kates.io/run-id`. A run may own many faults.                                                              |
| **Node (Kafka)**            | A KRaft process with a node ID, acting as broker, controller, or both. "Kubernetes node" is always written out in full.                                            |
| **Voter**                   | A KRaft controller participating in the metadata quorum.                                                                                                           |
| **Operator**                | The Strimzi Cluster Operator, unless stated otherwise.                                                                                                             |
| **Engine**                  | The controller plus the agents.                                                                                                                                    |

---

## 3. How chaos works in Kates today

### 3.1 Control flow

```text
kates disruption run · playbook · scheduler · REST
        │
        ▼
DisruptionOrchestrator ──┬── DisruptionConcurrencyGuard   in-memory, per JVM
        │                ├── DisruptionSafetyGuard        validate, dry-run, rollback
        │                ├── KafkaIntelligenceService     leader resolution, IsrTracker, LagTracker
        │                ├── K8sPodWatcher                TFR / TAR
        │                ├── StrimziStateTracker          Kafka CR Ready
        │                ├── PrometheusMetricsCapture     baseline / impact deltas
        │                └── SlaGrader                    A–F grade
        ▼
ChaosCoordinator ────────┬── litmus-crd                   creates ChaosEngine, polls ChaosResult every 5 s
  kates.chaos.provider   ├── kubernetes                   pod delete, NetworkPolicy, StatefulSets, ephemeral containers
  = litmus-crd           ├── hybrid                       litmus-crd if litmuschaos.io CRDs exist, else kubernetes
  application.properties └── noop                         injects nothing
```

`CompoundChaosOrchestrator` runs several `FaultSpec`s in parallel or in sequence across providers. `ProbeRegistry` attaches default probes per `DisruptionType` (from `KafkaProbes`) when a step declares none. `DisruptionOrphanReconciler` removes Kates-managed NetworkPolicies and restores annotated StatefulSets on startup.

### 3.2 What Litmus provides

| `DisruptionType`         | Litmus experiment                                                                  | Installed by `charts/kates-chaos`?                            |
| ------------------------ | ---------------------------------------------------------------------------------- | ------------------------------------------------------------- |
| `POD_KILL`, `POD_DELETE` | `pod-delete` with `FORCE=true`, `SEQUENCE=serial`                                  | yes                                                           |
| `LEADER_ELECTION`        | `pod-delete`                                                                       | yes                                                           |
| `SCALE_DOWN`             | `pod-delete`                                                                       | yes                                                           |
| `ROLLING_RESTART`        | `pod-delete`                                                                       | yes                                                           |
| `CPU_STRESS`             | `pod-cpu-hog`                                                                      | yes                                                           |
| `MEMORY_STRESS`          | `pod-memory-hog`                                                                   | yes                                                           |
| `IO_STRESS`              | `pod-io-stress` (receives `fillPercentage` as `FILESYSTEM_UTILIZATION_PERCENTAGE`) | yes                                                           |
| `DNS_ERROR`              | `pod-dns-error` (receives `targetTopic` as `TARGET_HOSTNAMES`)                     | yes                                                           |
| `NETWORK_PARTITION`      | `pod-network-partition`                                                            | yes                                                           |
| `NODE_DRAIN`             | `node-drain`                                                                       | yes                                                           |
| `DISK_FILL`              | `disk-fill`                                                                        | **no** — only in `config/litmus/experiments`, applied by hand |
| `NETWORK_LATENCY`        | `pod-network-latency`                                                              | **no** — same                                                 |

To provide those, the chart deploys the chaos operator, chaos runner, go-runner, and an optional exporter. It also ships three CRDs, an experiment-installer Job, the `litmus-admin` ServiceAccount and Roles in each target namespace, a node-chaos ClusterRole, Kyverno policies, and a GameDay template. `images.env` lists 6 core Litmus images and 9 portal images for Kind preloading.

Every experiment run schedules a runner pod, which schedules an experiment Job, which (for network and stress faults) schedules a helper pod on the target's Kubernetes node. That is three pod startups between "Kates asked" and "the fault exists".

### 3.3 What Kates does not use from Litmus

ChaosCenter (portal, GraphQL server, auth server, MongoDB) — already scoped out of the chart. Argo-based chaos workflows — `kafka-gameday-workflow.yaml` exists but is not wired into Kates, whose playbooks and scheduler cover the same need. ChaosHub cloud experiments (AWS, GCP, Azure, VMware). `httpProbe` and `promProbe` — Kates has `PrometheusMetricsCapture`. Resilience scores — Kates has SLA grades and `DisruptionImpactScorer`.

---

## 4. Defect register

Each item below was verified against the code on `main` (`fcf4183`) and, where marked ◉, against the live Kind cluster `kind-panda` on 2026-09-22. Every item becomes a regression test (§23.7).

| #       | Defect                                                                                                                                                                                                                                                                  | Evidence                                                                                                                                     | Consequence                                                                                                                                                                            |
| ------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **D1**  | **Fault start time is sampled before the fault exists.** `startNanos` is taken when the ChaosEngine CR is created. The orchestrator marks disruption start on the pod watcher and the ISR/lag trackers before calling `triggerFault`.                                   | `LitmusChaosProvider.java:55`; `DisruptionOrchestrator.java:307-310`                                                                         | RPO/RTO, TFR/TAR, and ISR/lag recovery all include the scheduling and start latency of three pods. With an image pull this is unbounded.                                               |
| **D2**  | **Fault end time is quantized to 5 s.**                                                                                                                                                                                                                                 | `LitmusChaosProvider.java:77`                                                                                                                | Chaos duration and every "after" window are off by up to 5 s.                                                                                                                          |
| **D3**  | **`LEADER_ELECTION` kills a random pod.** Kates resolves the leader and sets `targetBrokerId`, but the Litmus path never reads it. It picks `Math.random()` over the label.                                                                                             | `LitmusChaosProvider.java:37, 240`                                                                                                           | On the default cluster the leader is hit 1 time in 6.                                                                                                                                  |
| **D4**  | **`ROLLING_RESTART` and `SCALE_DOWN` do not do what they say.** On Litmus they are one pod delete. On the kubernetes provider they act on StatefulSets, which Strimzi stopped creating when it moved to StrimziPodSets. ◉ The `kafka` namespace has zero StatefulSets.  | `LitmusChaosProvider.java:38-39`; `KubernetesChaosProvider.java:199-251`; `DisruptionSafetyGuard.java:243-308`; `DisruptionOrphanReconciler` | The kubernetes provider silently does nothing. Its rollback and orphan paths for these faults are dead code.                                                                           |
| **D5**  | **KRaft controllers are counted as brokers.** `strimzi.io/component-type=kafka` matches both roles. ◉ Brokers are node IDs 0–2 and controllers 3–5.                                                                                                                     | `DisruptionSafetyGuard.java:105`; `KubernetesChaosProvider.java:329`                                                                         | Blast radius sees 6 "brokers", so killing all 3 real brokers passes validation. `targetBrokerId=3` kills a controller.                                                                 |
| **D6**  | **CPU and IO stress on the kubernetes provider are rejected.** Ephemeral containers can only be added through the `pods/ephemeralcontainers` subresource, but the provider calls `replace` on the pod. The stress image is not in `images.env`.                         | `KubernetesChaosProvider.java:305`                                                                                                           | Without Litmus, CPU and IO stress never run.                                                                                                                                           |
| **D7**  | **Default probes pass when they cannot run.** The command `kafka-topics.sh … 2>/dev/null \| grep -c 'Topic:' \|\| echo '0'` prints 0 on any failure, and the comparator is `<=`. ◉ The plain listener requires SCRAM-SHA-512, and the probe passes no credentials.      | `KafkaProbes.java:20-22, 37`                                                                                                                 | `isr-health-check`, `min-isr-check`, and `partition-availability` fail open.                                                                                                           |
| **D8**  | **Probes run a JVM inside a broker under test.** Every cmd probe `exec`s `kafka-topics.sh` in the first Kafka pod in list order.                                                                                                                                        | `ProbeExecutor.java:80`                                                                                                                      | The measurement perturbs the target. It fails outright when the first pod is the one that was killed, and it competes for CPU during CPU stress.                                       |
| **D9**  | **Threshold-based auto-rollback is never evaluated.** `AutoRollbackGuard.evaluate` has no callers.                                                                                                                                                                      | repository-wide search: only a reflection registration                                                                                       | ISR-depth and lag-spike limits never abort a fault. Only the recovery timeout does.                                                                                                    |
| **D10** | **`cleanup(engineName)` ignores its argument.** It deletes every `managed-by=kates` ChaosEngine and NetworkPolicy.                                                                                                                                                      | `LitmusChaosProvider.java:333`; `KubernetesChaosProvider.java:362`                                                                           | Cleaning up one step of a compound fault tears down its siblings.                                                                                                                      |
| **D11** | **The concurrency guard is per JVM.**                                                                                                                                                                                                                                   | `DisruptionConcurrencyGuard` (a `ConcurrentHashMap`)                                                                                         | With more than one Kates replica, two plans can hit the same cluster.                                                                                                                  |
| **D12** | **The `az-failure` playbook matches nothing.** Its label string contains a comma, and the parser splits on the first `=`, producing one bogus key/value. ◉ Pods carry `zone=alpha`, not `topology.kubernetes.io/zone=zone-a`. `POD_KILL` kills one pod, not a zone.     | `playbooks/az-failure.yaml:11`; `resolvePodName`                                                                                             | The headline multi-AZ scenario does not run.                                                                                                                                           |
| **D13** | **Leader re-targeting drops fields.** The rebuilt `FaultSpec` loses `memoryMb`, `ioWorkers`, and `probes`.                                                                                                                                                              | `DisruptionOrchestrator.java:255-270`                                                                                                        | Custom probes and stress parameters are lost on topic-targeted steps.                                                                                                                  |
| **D14** | **Disk fill on this storage would fill the host.** Fill percentage is computed against the filesystem that backs the volume. ◉ Every Kafka PV is a `rancher.io/local-path` directory on one 1.8 TB filesystem shared by all three Kind nodes and by `kates-postgresql`. | `LitmusChaosProvider` `FILL_PERCENTAGE`; `config/litmus/experiments/kafka-disk-fill.yaml`                                                    | "80 %" means writing ~1.4 TB, filling every node and the Kates database with it. The kubelet's `nodefs` eviction threshold would fire first, turning a disk fault into mass evictions. |

D3, D5, D9, D12, and D13 live entirely in Java and are fixed in milestone M1 regardless of the engine (§25). The engine removes the whole class behind D1, D2, D6, D7, D8, D10, D11, and D14.

---

## 5. Environment facts that shape the design

Observed on `kind-panda` on 2026-09-22. These are the defaults Kates deploys, so the engine must handle them out of the box and treat deviations as configuration, not surprises.

### 5.1 Platform

| Fact                  | Value                                                                                                                                                                                                                                            | Design implication                                                                                                                                                                                 |
| --------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Kubernetes            | 1.34 (`kindest/node:v1.34.11`), 3 Kubernetes nodes named after zones `alpha`, `sigma`, `gamma`                                                                                                                                                   | `selectableFields` on CRDs is GA (1.32+), and so is `ValidatingAdmissionPolicy` (1.30+). Both are used.                                                                                            |
| Container runtime     | containerd, socket `/run/containerd/containerd.sock`                                                                                                                                                                                             | CRI `ContainerStatus` gives the container PID.                                                                                                                                                     |
| cgroup                | v2 (`cgroup2fs`), systemd driver, controllers `cpuset cpu io memory hugetlb pids rdma`, `cgroup.freeze` present                                                                                                                                  | Stressors join the target container's cgroup. Pause uses the v2 freezer. Throttling uses `io.max`.                                                                                                 |
| Container cgroup path | `/kubelet.slice/kubelet-kubepods.slice/kubelet-kubepods-pod<uid_with_underscores>.slice/cri-containerd-<id>.scope`                                                                                                                               | Resolved from `/proc/<pid>/cgroup`. Guaranteed-QoS pods sit directly under `kubepods`, not under `burstable`/`besteffort`.                                                                         |
| Kernel                | 7.0 linuxkit. **Present:** `sch_netem`, `sch_prio`, `sch_htb`, `sch_tbf`, `sch_ingress`, `cls_u32`, `cls_bpf`, `cls_matchall`, `act_mirred`, `nf_tables` (inet, nat, redir, reject, ct), PSI, `CONFIG_TIME_NS`. **Absent:** `ifb`, `cls_flower`. | Traffic classification uses **u32**, not flower. **Ingress shaping is unavailable** without IFB, so the engine must probe kernel capabilities per node and reject unsupported parameters up front. |
| Node filesystem       | `/var` and `/` are the same 1.8 TB `/dev/vda1` (Docker VM disk) on every node                                                                                                                                                                    | Disk faults must detect shared filesystems (D14).                                                                                                                                                  |

### 5.2 Strimzi and Kafka

| Fact                | Value                                                                                                                                                                                                                                                  | Design implication                                                                                                                                                                          |
| ------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Strimzi             | 1.2.0; `Kafka`, `KafkaNodePool`, `StrimziPodSet` all served and stored at `v1`                                                                                                                                                                         | The engine reads `v1` only.                                                                                                                                                                 |
| Kafka               | 4.3.1, KRaft, **dedicated roles**                                                                                                                                                                                                                      | Broker and controller faults are different experiments with different safety budgets.                                                                                                       |
| Node pools          | `brokers-alpha` [0], `brokers-gamma` [1], `brokers-sigma` [2], `controllers-alpha` [3], `controllers-gamma` [4], `controllers-sigma` [5]; one replica each                                                                                             | `KafkaNodePool.status.nodeIds` and `.status.roles` are the authoritative node-ID↔pool↔role map. Parsing pod names is only a cross-check.                                                    |
| Pod naming          | `<cluster>-<pool>-<nodeId>`, e.g. `krafter-brokers-sigma-2`                                                                                                                                                                                            |                                                                                                                                                                                             |
| Pod labels          | `strimzi.io/cluster`, `strimzi.io/component-type=kafka` (**both roles**), `strimzi.io/broker-role`, `strimzi.io/controller-role`, `strimzi.io/pool-name`, `strimzi.io/pod-name`, `strimzi.io/controller=strimzipodset`, plus chart-added `zone=<zone>` | Role must come from the `*-role` labels (D5).                                                                                                                                               |
| Placement           | Required node affinity `topology.kubernetes.io/zone In [<zone>]`; zone-specific StorageClasses; topology spread by pool                                                                                                                                | A Kafka pod cannot move off its zone. Drained or deleted pods wait for their Kubernetes node to come back.                                                                                  |
| Rack awareness      | `spec.kafka.rack.topologyKey: topology.kubernetes.io/zone`; `kafka-init` init container writes `broker.rack`                                                                                                                                           | Zone = rack. Replica placement is rack-aware, so one zone outage costs at most one replica per partition when RF = 3.                                                                       |
| Broker ports        | `8443` Kafka Agent (Strimzi), `9091` replication (internal clients, mTLS), `9092` plain (SCRAM-SHA-512), `9093` TLS (mTLS), `9404` Prometheus                                                                                                          | These ports define the peer classes for network faults (§11.3).                                                                                                                             |
| Controller ports    | `8443` Kafka Agent, `9090` control plane, `9404` Prometheus                                                                                                                                                                                            |                                                                                                                                                                                             |
| Probes              | `exec /opt/kafka/kafka_liveness.sh` and `kafka_readiness.sh`; period 10 s, timeout 5 s, failureThreshold 3, initialDelay 15 s; no startup probe                                                                                                        | A broker paused longer than ≈ 30 s (3 × 10 s, each probe up to 5 s) is restarted by the kubelet, and the pause turns into a container restart. The engine reads the probe config and warns. |
| Termination grace   | 30 s                                                                                                                                                                                                                                                   | `PodDelete` with the default grace gives Kafka a controlled shutdown.                                                                                                                       |
| Resources           | requests = limits: `cpu: 8`, `memory: 16Gi` → Guaranteed QoS                                                                                                                                                                                           | `cpu.max` = 8 CPUs of quota, so CPU stress in-cgroup causes CFS throttling of the broker. `memory.max` = 16 GiB, and page cache counts toward it.                                           |
| Storage             | JBOD volume `data-0` at `/var/lib/kafka/data-0`; log dir `/var/lib/kafka/data-0/kafka-log<nodeId>`; brokers 200Gi, controllers 20Gi; `rancher.io/local-path`                                                                                           | The PVC size is **not enforced** by local-path (D14).                                                                                                                                       |
| PodDisruptionBudget | `krafter-kafka`: `minAvailable: 5` across all 6 Kafka pods, so **1 disruption allowed for brokers and controllers combined**                                                                                                                           | Any Eviction-based fault can take at most one Kafka pod at a time. Draining a Kubernetes node that hosts a broker *and* a controller cannot complete (§11.4.1).                             |
| Cluster config      | `min.insync.replicas=2`, `default.replication.factor=3`, `unclean.leader.election.enable=false`, `controller.quorum.election.timeout.ms=5000`, `controller.quorum.fetch.timeout.ms=10000`, `group.share.enable=true`                                   | Blast-radius math uses these (§15.4). Expected timings in Appendix C derive from them.                                                                                                      |
| Listener auth       | `plain` 9092 SCRAM-SHA-512, `tls` 9093 mTLS                                                                                                                                                                                                            | The engine needs its own `KafkaUser` (§19.5).                                                                                                                                               |
| Operators           | Cluster Operator in `strimzi-operator` (`name=strimzi-cluster-operator`); Entity Operator in `kafka` with PDB `maxUnavailable: 1`; **Cruise Control not deployed**                                                                                     | The operator talks to brokers over 9091 and 8443 (§12).                                                                                                                                     |
| Operator stability  | **The Cluster Operator had restarted 197 times in 47 h** when these facts were collected                                                                                                                                                               | Chaos results are confounded if the operator is flapping. Preflight checks operator stability (§15.3).                                                                                      |

---

# Part II — Design

## 6. Design principles

**P1 — Kafka semantics first.** Target, budget, and measure in Kafka's vocabulary: node IDs, roles, leaders, ISR, `min.insync.replicas`, voters, coordinators. Kubernetes objects are how we get there, not what we reason about.

**P2 — Cooperate with Strimzi.** The Cluster Operator is a reconciler that will try to heal what the engine breaks. The engine declares how it wants the operator to behave during each fault — observe or pause — and journals every annotation it sets. It never edits `Kafka` or `KafkaNodePool` specs, never changes node IDs, and never touches PVCs.

**P3 — No fault outlives its intent.** Every mutation is journaled before it happens. Every fault carries an absolute deadline enforced by the component closest to the fault. Deletion always means "abort and revert".

**P4 — Timestamps come from the actor.** The component that performed a change records when the change took effect, right after the call returns. Nobody infers timing from polling.

**P5 — Fail closed.** A check that cannot run fails. A selector that resolves to nothing fails. A capability that the kernel lacks is rejected before injection. Nothing falls back to "first pod" or "assume permitted".

**P6 — Declarative and observable.** Faults are Kubernetes resources with status, conditions, events, and printer columns. `kubectl get cf -A` is a complete view of all chaos in the cluster.

**P7 — Mechanism × selector.** A fault type says *what* happens. A selector says *to whom*. "Leader election" is not a mechanism, it is `PodKill` with `leaderOf`. This keeps the catalog small and the combinations large.

**P8 — Least privilege, even for the privileged part.** The node agent needs root-level powers on every node. It gets explicit capabilities rather than `privileged: true`, acts only on objects that name its node, re-validates every target, and exposes no control API.

---

## 7. Concepts borrowed from Litmus, re-designed

Litmus got several things right. The engine keeps the ideas, drops the machinery, and reshapes each for Kafka. No Litmus CRD, image, library, or experiment is used.

| Litmus concept                                                                                      | Idea worth keeping                                                 | What changes in Kates                                                                                                                                                                                                                                                | Kates name                                                            |
| --------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------- |
| `ChaosExperiment` — a reusable definition of a fault, with its RBAC                                 | A catalog of fault definitions, separate from any run              | The catalog is **compiled into the engine** and versioned with it, so there is no installer Job and no drift between definition and runner. Kates keeps its own higher-level templates and playbooks.                                                                | Fault types (§11), advertised in `ChaosPolicy.status.supportedFaults` |
| `ChaosEngine` — binds an experiment to a target and runs it                                         | One declarative object per fault run                               | Targets are Kafka selectors, not app labels. Runs in-process in the controller and agent, not in runner and experiment pods.                                                                                                                                         | `ChaosFault`                                                          |
| `ChaosResult` — verdict, probe success %, fail step                                                 | A durable result with a verdict                                    | The result lives in `ChaosFault.status`, next to the journal, per-target timestamps, and check timelines. It is garbage-collected by `ttlSecondsAfterFinished`, as Jobs are, because Kates persists reports in its own database.                                     | `ChaosFault.status`                                                   |
| Probes: `cmdProbe`, `httpProbe`, `k8sProbe`, `promProbe`; modes SOT, EOT, Edge, Continuous, OnChaos | A steady-state hypothesis evaluated around the fault               | **Kafka-native checks** over the Kafka protocol, run by the controller outside the brokers. Phases are renamed and made explicit (`Before`, `OnInject`, `During`, `After`, `Throughout`). Checks can expect failure, and recovery is measured with `After … within`. | Checks (§16)                                                          |
| `annotationCheck` — the target app must opt in                                                      | Consent is declared on the target                                  | The opt-in lives on the **Strimzi `Kafka` CR**: `chaos.kates.io/enabled: "true"`. The whole cluster opts in, not individual Deployments.                                                                                                                             | Opt-in annotation (§15.2)                                             |
| `engineState: stop`                                                                                 | Stop a running fault declaratively                                 | `spec.abort: true` keeps the object and its status. Deleting the object aborts it too. Both revert.                                                                                                                                                                  | `spec.abort`, finalizer                                               |
| `TOTAL_CHAOS_DURATION`, `CHAOS_INTERVAL`, `RAMP_TIME`, `SEQUENCE`                                   | Duration, repetition inside a window, warm-up, serial vs. parallel | Typed fields instead of environment variables: `duration`, `repeat.every`, `delay`, `rollout.strategy`.                                                                                                                                                              | `spec.timing`, `spec.target.rollout`                                  |
| `TARGET_PODS`, `PODS_AFFECTED_PERC`                                                                 | Choose one, some, or all targets                                   | `target.mode: One \| All \| Count \| Percent`, with a seed recorded for reproducibility.                                                                                                                                                                             | `spec.target.mode`                                                    |
| Chaos exporter metrics                                                                              | Chaos visible in Prometheus                                        | Native `/metrics` on the controller and agent. Fault windows are also emitted as Grafana annotations on every Kates board.                                                                                                                                           | §22                                                                   |
| Resilience score                                                                                    | A single number per run                                            | Kates' SLA grade and impact scorer already do this. The engine reports the check pass ratio, which maps to today's `probeSuccessPercentage` field.                                                                                                                   | `status.checksPassedRatio`                                            |
| ChaosHub                                                                                            | Shareable catalog                                                  | Kates playbooks (`kates/src/main/resources/playbooks`) remain the shareable unit.                                                                                                                                                                                    | —                                                                     |
| Helper pods per fault                                                                               | Node-local execution                                               | One long-running agent per Kubernetes node. There is no per-fault pod scheduling, which is why start time is precise (D1).                                                                                                                                           | `kates-chaos-agent`                                                   |

---

## 8. Architecture

### 8.1 Components

```text
┌────────────────────────────────────────────────────────────────────────────────────┐
│ namespace: kates                                                                   │
├────────────────────────────────────────────────────────────────────────────────────┤
│ Kates (Quarkus)                                                                    │
│ DisruptionOrchestrator ──► ChaosCoordinator ──► KatesChaosProvider                 │
└───────────────────────────────────────┬────────────────────────────────────────────┘
                                        │ ChaosFault: create · watch · abort · delete
                                        ▼
┌────────────────────────────────────────────────────────────────────────────────────┐
│ namespace: kafka  (target cluster)                                                 │
├────────────────────────────────────────────────────────────────────────────────────┤
│ Kafka/krafter        annotated chaos.kates.io/enabled="true"                       │
│ KafkaNodePool ×6     brokers-{alpha,gamma,sigma}, controllers-{alpha,gamma,sigma}  │
│ StrimziPodSet ×6     one per node pool                                             │
│ Pods                 krafter-brokers-*-{0,1,2}, krafter-controllers-*-{3,4,5}      │
│ ChaosFault           created by Kates                                              │
│ ChaosInjection       created by the controller, owned by its ChaosFault            │
└────────────────────────────────────────────────────────────────────────────────────┘
          ▲                                           ▲
          │ reconcile ChaosFault                      │ watch ChaosInjection where
          │ create ChaosInjection                     │ spec.nodeName = own node
          │ delete · evict · cordon · annotate        │ inject into pod netns / cgroup
          │ Kafka checks over mTLS :9093              │
┌─────────┴───────────────────────────────────────────┴──────────────────────────────┐
│ namespace: kates-chaos  (PSA enforce=privileged)                                   │
├────────────────────────────────────────────────────────────────────────────────────┤
│ kates-chaos-controller   Deployment · 2 replicas · 1 active (Lease)                │
│   • reconciles ChaosFault, owns ChaosInjection                                     │
│   • preflight: opt-in, policy, operator health, Kafka health, rebalances           │
│   • resolves targets: Kafka CR, KafkaNodePools, pods, nodes, Kafka metadata        │
│   • blast radius per partition and per KRaft quorum                                │
│   • API-level faults: pod kill/delete, rolling restart, hold-down, drain           │
│   • steady-state checks over the Kafka protocol (mTLS KafkaUser)                   │
│   • journal · finalizers · deadlines · cluster Lease · kill switch · quarantine    │
│   • /metrics · events · OTel traces                                                │
├────────────────────────────────────────────────────────────────────────────────────┤
│ kates-chaos-agent        DaemonSet · hostPID · explicit capabilities               │
│   • watches ChaosInjection for its own node (selectableFields)                     │
│   • re-validates namespace, pod UID, container ID                                  │
│   • finds the container PID via CRI (fallback: /proc scan)                         │
│   • acts in the target's netns and cgroup; files via /proc/<pid>/root              │
│   • local deadline timers (dead-man's switch) · hostPath journal                   │
│   • startup sweep reverts every artifact tagged kates-chaos                        │
│   • reports per-node kernel capabilities and clock offset                          │
└───────────────────────────────────────┬────────────────────────────────────────────┘
                                        │ observe · optionally pause via Kafka CR
                                        ▼
┌────────────────────────────────────────────────────────────────────────────────────┐
│ namespace: strimzi-operator                                                        │
├────────────────────────────────────────────────────────────────────────────────────┤
│ strimzi-cluster-operator   reconciles Kafka, KafkaNodePool, StrimziPodSet          │
└────────────────────────────────────────────────────────────────────────────────────┘
```

### 8.2 Responsibilities

| Concern                                                        | Kates (Java)             | Controller (Rust) | Agent (Rust)                 |
| -------------------------------------------------------------- | ------------------------ | ----------------- | ---------------------------- |
| Plans, steps, steady-state waits, observation windows          | ✔                        |                   |                              |
| Plan-level policy (`maxAffectedBrokers`, dry-run preview, SLA) | ✔                        |                   |                              |
| ISR/lag trackers, Prometheus deltas, SLA grading, reports      | ✔ (unchanged)            |                   |                              |
| Opt-in, policy, preflight                                      |                          | ✔                 |                              |
| Target resolution at injection time                            | hints only               | ✔                 | re-validates identity        |
| Per-partition and per-quorum blast radius                      |                          | ✔                 |                              |
| API-level faults                                               |                          | ✔                 |                              |
| Node-level faults                                              |                          |                   | ✔                            |
| Steady-state checks and abort conditions                       | consumes results         | ✔                 |                              |
| Deadlines                                                      | requests abort           | ✔ API faults      | ✔ node faults, authoritative |
| Revert                                                         | deletes or aborts the CR | ✔                 | ✔                            |

### 8.3 Decision: Kubernetes resources, not an RPC API

| Concern                 | CRD + controller (chosen)                                                                                     | gRPC service called by Kates                         |
| ----------------------- | ------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------- |
| Kates dies mid-fault    | Fault keeps its deadline; the controller reverts on schedule                                                  | Orphaned unless the service adds its own persistence |
| Multiple Kates replicas | API server serializes; a Lease gives cluster-wide exclusion                                                   | The service needs its own locking                    |
| AuthN/AuthZ             | Kubernetes RBAC, already used by Kates                                                                        | mTLS plus authorization, to build and run            |
| Audit                   | `kubectl get cf`, events, audit log                                                                           | Custom endpoints                                     |
| Operator ergonomics     | `kubectl delete cf --all -n kafka` is an emergency stop                                                       | Needs a CLI or curl                                  |
| Latency                 | Watch delivery is typically 10–100 ms, but **not on the timing path**: timestamps are taken by the actor (P4) | Lower, irrelevant for the same reason                |

### 8.4 Decision: controller → agent through `ChaosInjection` in the target namespace

- **Same namespace as the `ChaosFault`.** The fault owns the injection through an `ownerReference`, and Kubernetes forbids cross-namespace owner references (the garbage collector treats them as unresolvable). Keeping both in the target namespace gives correct GC and lets a single `kubectl get cf,ci -n kafka` show everything.
- **The agent selects its work by `spec.nodeName`.** The CRD declares `selectableFields: [{jsonPath: .spec.nodeName}]` (GA in Kubernetes 1.32), so the agent's watch uses a field selector evaluated by the API server. For clusters older than 1.32, the controller also stamps the label `chaos.kates.io/node=<name>` and the agent falls back to a label selector.
- **No agent listener.** There's no Service, no certificate, and no NetworkPolicy hole. A restarted agent lists its node's injections and converges. Every injection can be inspected and deleted with `kubectl`.
- **Cost.** One object per target per fault. At Kates' scale, single-digit concurrent targets, this is negligible.

### 8.5 Decision: API-level faults stay in the controller

Pod deletion, eviction, cordon, and Strimzi annotations are API calls. Routing them through the agent would add a hop and a failure mode with no benefit. The controller records `injectedAt` when the API call returns (P4).

---

## 9. API reference

API group `chaos.kates.io`, version `v1alpha1`. All CRDs are generated from Rust types (§20.3) and committed under `charts/kates-chaos/crds/`. A CI gate regenerates them and fails on any difference. The Java model classes are generated from the same YAML, so the schema has one source of truth.

| Kind             | Scope                          | Short name | Created by                       | Purpose                                                                       |
| ---------------- | ------------------------------ | ---------- | -------------------------------- | ----------------------------------------------------------------------------- |
| `ChaosFault`     | Namespaced (target namespace)  | `cf`       | Kates, or a human with `kubectl` | One fault run: what, to whom, how long, under which safety rules, checked how |
| `ChaosInjection` | Namespaced (same as its fault) | `ci`       | Controller only                  | One node-local application of a fault to one container                        |
| `ChaosPolicy`    | Cluster                        | `cpol`     | Helm chart (singleton `default`) | Allowed namespaces, defaults, kill switch, quarantine, capabilities report    |

All three kinds share the category `kates-chaos`, so `kubectl get kates-chaos -A` lists everything.

### 9.1 `ChaosFault` — spec

```yaml
apiVersion: chaos.kates.io/v1alpha1
kind: ChaosFault
metadata:
  name: leader-cascade-s1-7f3a2c           # == ChaosOutcome.engineName
  namespace: kafka
  labels:
    kates.io/run-id: 8e1d5f0c              # lease holder identity (§15.6)
    kates.io/plan: leader-cascade
    kates.io/step: kill-leader-1
  annotations:
    kates.io/experiment: leader-cascade-1  # FaultSpec.experimentName
    kates.io/traceparent: 00-…             # W3C trace context from Kates (§22.4)
spec:
  cluster: krafter                         # Strimzi Kafka CR name, same namespace. Required.
  type: PodKill                            # §11
  target:                                  # §10
    leaderOf: { topic: orders, partition: 0 }
    role: Broker                           # Broker | Controller | Any       (default Broker)
    mode: One                              # One | All | Count | Percent    (default One)
    value: 1                               # for Count / Percent
    container: kafka                       # default kafka
    rollout:                               # multi-target faults only
      strategy: Parallel                   # Parallel | Serial
      interval: 0s                         # pause between targets when Serial
  timing:
    delay: 0s                              # wait before resolving and injecting (≤ 1h)
    duration: 30s                          # how long the fault holds; 0s for instantaneous faults
    repeat:                                # re-inject inside the window (kill-type faults)
      every: 20s                           # e.g. kill the current leader every 20 s for 2 minutes
      reResolve: true                      # re-run target resolution on each repetition
  params: {}                               # type-specific, see §11
  operator:
    mode: Observe                          # Observe | Pause   (§12.3)
  safety:
    maxUnavailableBrokers: 1               # default 1
    maxUnavailableControllers: 1           # default: largest value keeping a voter majority
    protectMinIsr: true                    # per-partition check (§15.4); default true for unavailable-class
    allowUnderMinIsr: false                # permit faults that stop acks=all produce on some partitions
    allowUnreadyCluster: false             # permit injection when Kafka CR is not Ready
    abortOn:                               # evaluated every 2 s while Active (§16.6)
      offlinePartitions: 0                 # abort if > 0
      underMinIsrPartitions: null          # null = not evaluated
      controllerQuorumLost: true
    revertGrace: 30s                       # added to the deadline
  checks: []                               # §16
  abort: false                             # set true to stop and revert, keeping the object
  ttlSecondsAfterFinished: 3600            # delete the object this long after a terminal phase
```

**Field rules**, enforced by `ValidatingAdmissionPolicy` (§15.1) and re-checked by the controller:

- Exactly one of `target.{podNames, nodeIds, leaderOf, activeController, quorumFollowers, coordinatorOf, pool, zone, selector}` is set.
- `timing.duration ≤ 1h` and `timing.delay ≤ 1h`. `repeat.every` must be less than `duration` and at least 5 s.
- `spec` is **immutable after creation**, except `abort` (false → true only) and `ttlSecondsAfterFinished`. Changing a running fault is a delete plus a create.
- `params` is validated per type by a tagged-union schema (`oneOf` keyed by `type`).

### 9.2 `ChaosFault` — status

```yaml
status:
  observedGeneration: 1
  phase: Completed            # §13
  verdict: Pass               # Pass | Fail | Aborted | Error   (set on terminal phases)
  reason: ""                  # machine-readable, e.g. NoTargets, BlastRadius, Quarantined
  message: ""
  seed: 1583920113            # RNG seed for One/Count/Percent selection
  deadline:   "2026-09-22T10:02:02.141207Z"
  resolvedAt: "2026-09-22T10:01:02.118734Z"
  injectedAt: "2026-09-22T10:01:02.141207Z"   # earliest target effect
  revertedAt: "2026-09-22T10:01:32.144950Z"   # latest target revert (instantaneous faults: == injectedAt)
  targets:
    - pod: krafter-brokers-sigma-2
      podUID: 505e70b7-4a3d-4761-b134-3e1a596d49bb
      node: sigma
      zone: sigma
      pool: brokers-sigma
      nodeId: 2
      roles: [Broker]
      reason: "leader of orders-0 at 10:01:02.118Z"
      injection: leader-cascade-s1-7f3a2c-0     # node-level faults only
      injectedAt: "…"
      revertedAt: "…"
      replacement: { podUID: "…", readyAt: "…" } # kill-type faults
  repetitions: 0
  checks:
    - name: isr-health
      phase: During
      evaluations: 15
      passed: 15
      lastResult: Pass
      firstFailAt: null
      recoveredAt: null        # After-phase checks: first pass after revert
  checksPassedRatio: "100"     # → ChaosOutcome.probeSuccessPercentage
  failStep: ""                 # → ChaosOutcome.failStep
  journal:                     # §14.1
    - seq: 1
      actor: controller
      action: DeletePod
      object: kafka/Pod/krafter-brokers-sigma-2
      undo: None
      state: Applied
      at: "…"
  conditions:                  # metav1.Condition
    - type: Accepted           # passed admission, policy, preflight
    - type: Injected
    - type: Reverted
    - type: ChecksPassed
    - type: OperatorPaused     # present only when operator.mode=Pause
```

Printer columns: `TYPE`, `CLUSTER`, `TARGETS` (pod names, truncated), `PHASE`, `VERDICT`, `INJECTED` (age), `DURATION`.

### 9.3 `ChaosInjection`

Internal. Users never create it. It carries a **fully resolved** instruction, so the agent never resolves anything on its own.

```yaml
apiVersion: chaos.kates.io/v1alpha1
kind: ChaosInjection
metadata:
  name: leader-cascade-s1-7f3a2c-0
  namespace: kafka
  ownerReferences: [{ kind: ChaosFault, name: leader-cascade-s1-7f3a2c, controller: true }]
  finalizers: [chaos.kates.io/revert]
  labels: { chaos.kates.io/node: sigma }            # fallback selector for Kubernetes < 1.32
spec:
  nodeName: sigma                                   # selectableField
  pod: { namespace: kafka, name: krafter-brokers-sigma-2, uid: 505e70b7-… }
  containerName: kafka
  containerID: containerd://b2c40ce5…               # from pod.status.containerStatuses
  action: NetworkLatency
  params:
    interface: eth0
    netem: { delay: 100ms, jitter: 10ms, correlation: 25 }
    match:                                          # already resolved to addresses and ports
      - { cidr: 10.244.1.7/32, dport: 9091 }
      - { cidr: 10.244.3.4/32, dport: 9091 }
  notBefore: "…"                                    # honours timing.delay and rollout interval
  expiresAt: "…"                                    # hard deadline, enforced locally
status:
  phase: Applied        # Pending | Applying | Applied | Reverting | Reverted | Refused | Failed
  appliedAt: "…"
  revertedAt: "…"
  artifacts:            # everything the agent created, sufficient for a fresh process to revert
    - { kind: Qdisc, netns: "/proc/412733/ns/net#4026532871", dev: eth0, handle: "7ac1:" }
  refusal: ""           # TargetReplaced | TargetMismatch | Unsupported | NotAllowed
```

**Identity contract.** The agent refuses (`Refused/TargetReplaced`) if the pod UID or container ID it observes differs from the spec. A fault never lands on a new incarnation of a pod that was replaced after resolution.

### 9.4 `ChaosPolicy` (cluster-scoped singleton `default`)

```yaml
apiVersion: chaos.kates.io/v1alpha1
kind: ChaosPolicy
metadata: { name: default }
spec:
  allowedNamespaces: [kafka]           # faults elsewhere are Rejected
  requireOptIn: true                   # Kafka CR must carry chaos.kates.io/enabled=true
  frozen: false                        # kill switch (§15.7)
  maxConcurrentFaultsPerCluster: 8
  operatorStableFor: 10m               # preflight: no Cluster Operator restart within this window (§12.4)
  agent:
    maxInjectionLifetime: 2h           # hard ceiling on any injection's expiresAt (§14.2)
  kubeletEvictionHard:                 # used when node configz is unreadable (§11.5.4)
    nodefsAvailable: "10%"
  defaults:
    operatorMode: Observe
    revertGrace: 30s
    ttlSecondsAfterFinished: 3600
  diskFill:
    sharedFilesystemPolicy: Budget     # Budget | Refuse | Allow   (§11.5.4)
    maxBytes: 5Gi
  quarantine: []                       # clusters blocked after RevertFailed (§15.8)
status:
  controller: { leader: kates-chaos-controller-7d9f-xk2p, version: 0.1.0 }
  supportedFaults: [PodKill, PodDelete, …]
  nodes:                               # reported by each agent
    - name: sigma
      agentVersion: 0.1.0
      ready: true
      clockOffsetMs: 0.4
      capabilities: { netem: true, u32: true, flower: false, ifb: false, nftables: true,
                      cgroupFreeze: true, ioMax: true, psi: true }
```

### 9.5 Versioning

`v1alpha1` changes are additive until `v1beta1`. No conversion webhook is planned: when `v1beta1` ships, both versions are served with the same schema plus renames handled by the controller on read. The storage version moves once no `v1alpha1` objects remain, and faults are short-lived with a TTL, so that's quick.

---

## 10. Target resolution

Resolution runs in the controller **at injection time** — after `timing.delay`, and again on every `repeat` when `reResolve: true`. It never runs at creation time, because leadership moves during Kates' steady-state wait.

### 10.1 Algorithm

1. **Load the cluster.**
   - Read `Kafka/<cluster>`. Reject with `ClusterNotFound` if absent.
   - Reject with `NotOptedIn` if `ChaosPolicy.requireOptIn` is set and the `chaos.kates.io/enabled` annotation is not `"true"`.
   - Reject with `ClusterNotReady` if `status.conditions[Ready]` is not `True` and `allowUnreadyCluster` is false.
   - Reject with `ReconciliationPaused` if the CR carries `strimzi.io/pause-reconciliation: "true"` set by someone else. The journal can tell ours from theirs.
2. **Load the topology.**
   - List `KafkaNodePool`s with `strimzi.io/cluster=<cluster>`, and take `status.nodeIds` and `status.roles`. This is the authoritative node-ID → pool → role map.
   - List pods with `strimzi.io/cluster=<cluster>,strimzi.io/component-type=kafka`. Join them to the map by `strimzi.io/pool-name` and the numeric suffix of `strimzi.io/pod-name`. A mismatch rejects the fault with `TopologyInconsistent`.
   - For each pod, read `spec.nodeName`, then the Kubernetes node's `topology.kubernetes.io/zone` label. Fall back to the pod's `zone` label. That gives each Kafka node its zone.
3. **Apply the selector** (§10.2). Restrict to `target.role` (default `Broker`). This is the fix for D5.
4. **Apply the mode.**
   - `One` picks uniformly at random and records the seed.
   - `Count` and `Percent` pick without replacement, and `Percent` rounds down to at least 1.
   - `All` takes every match.
5. **Validate.** Every target pod must be `Running` and `Ready`, and hold a container named `target.container` with a running `containerID`. Otherwise reject with `TargetNotReady`, unless the fault type tolerates it (`NodeDrain`, `ZoneOutage`).
6. **Record.** Write `status.targets[]` with the reason for each pick, e.g. "leader of orders-0 at T" or "active controller at T, epoch E".
7. **Empty result** → `phase: Failed`, `reason: NoTargets`. There is never a fallback.

### 10.2 Selectors

| Selector                                                        | Resolves to                                                            | Kafka API / source                                      | Notes                                                                                                         |
| --------------------------------------------------------------- | ---------------------------------------------------------------------- | ------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------- |
| `podNames: [...]`                                               | Named pods                                                             | Kubernetes                                              | Must belong to `cluster`.                                                                                     |
| `nodeIds: [...]`                                                | Kafka nodes by ID                                                      | `KafkaNodePool.status.nodeIds`                          | Replaces suffix matching (D5).                                                                                |
| `pool: <name>`                                                  | Pods of a node pool                                                    | `KafkaNodePool`                                         |                                                                                                               |
| `zone: <zone>`                                                  | Kafka nodes in a zone                                                  | Kubernetes node labels                                  | Fixes D12.                                                                                                    |
| `leaderOf: {topic, partition}`                                  | The current partition leader                                           | Metadata / `DescribeTopicPartitions`                    | Fixes D3. Records leader epoch.                                                                               |
| `replicasOf: {topic, partition, include: Followers\|All}`       | Replica set of a partition                                             | Metadata                                                | e.g. partition the followers of a hot partition.                                                              |
| `activeController: true`                                        | Current KRaft metadata leader                                          | `DescribeQuorum` (`leaderId`)                           | Forces `role: Controller`. Tests controller failover.                                                         |
| `quorumFollowers: true`                                         | Voters that are not the leader                                         | `DescribeQuorum`                                        |                                                                                                               |
| `coordinatorOf: {group: <id>, type: Group\|Transaction\|Share}` | Broker coordinating a consumer group, transactional ID, or share group | `FindCoordinator` (key types GROUP, TRANSACTION, SHARE) | Tests rebalances, transaction recovery, and share-group state (`group.share.enable=true` on the dev cluster). |
| `selector: LabelSelector`                                       | Generic                                                                | Kubernetes                                              | A real `LabelSelector` with `matchLabels` and `matchExpressions`, not a `k=v` string (D12).                   |

### 10.3 Multi-target rollout

- `Parallel` injects all targets together. `status.injectedAt` is the earliest target's effect.
- `Serial` injects one target at a time with `interval` between them. The whole fault's deadline covers the full rollout.
- For kill-type faults with `repeat`, each repetition resolves afresh when `reResolve: true`. That makes "kill whoever is the leader of `orders-0` every 20 s" a *leader cascade*, where each kill hits the newly elected leader.

---

## 11. Fault catalog

### 11.1 Overview

| Fault type         | Class                            | Actor              | `DisruptionType`                                                 | Milestone |
| ------------------ | -------------------------------- | ------------------ | ---------------------------------------------------------------- | --------- |
| `ContainerKill`    | Unavailable                      | Agent              | `CONTAINER_KILL` (new)                                           | M3        |
| `PodKill`          | Unavailable                      | Controller         | `POD_KILL`; `LEADER_ELECTION` (with the `leaderOf` selector, M2) | M1        |
| `PodDelete`        | Unavailable                      | Controller         | `POD_DELETE`                                                     | M1        |
| `ProcessPause`     | Unavailable                      | Agent              | `PROCESS_PAUSE` (new)                                            | M3        |
| `RollingRestart`   | Unavailable (one at a time)      | Controller         | `ROLLING_RESTART`                                                | M2        |
| `BrokerHoldDown`   | Unavailable                      | Controller         | `SCALE_DOWN`                                                     | M2        |
| `NodeDrain`        | Unavailable                      | Controller         | `NODE_DRAIN`                                                     | M2        |
| `ZoneOutage`       | Unavailable (zone)               | Controller + Agent | `ZONE_OUTAGE` (new)                                              | M5        |
| `NetworkPartition` | Unavailable                      | Agent              | `NETWORK_PARTITION`                                              | M3        |
| `NetworkLatency`   | Degraded                         | Agent              | `NETWORK_LATENCY`                                                | M3        |
| `NetworkLoss`      | Degraded                         | Agent              | `NETWORK_LOSS` (new)                                             | M3        |
| `NetworkBandwidth` | Degraded                         | Agent              | `NETWORK_BANDWIDTH` (new)                                        | M3        |
| `DnsError`         | Degraded                         | Agent              | `DNS_ERROR`                                                      | M4        |
| `CpuStress`        | Degraded                         | Agent              | `CPU_STRESS`                                                     | M4        |
| `MemoryStress`     | Degraded                         | Agent              | `MEMORY_STRESS`                                                  | M4        |
| `IoStress`         | Degraded                         | Agent              | `IO_STRESS`                                                      | M4        |
| `DiskFill`         | Degraded → Unavailable when full | Agent              | `DISK_FILL`                                                      | M4        |
| `DiskThrottle`     | Degraded                         | Agent              | `DISK_THROTTLE` (new)                                            | M4        |

Every fault below is described with the same headings: **Intent**, **Expected Kafka behaviour**, **Mechanism**, **Parameters**, **Revert**, **`injectedAt` means**, and **Pitfalls and guards**.

### 11.2 Process and pod lifecycle

#### 11.2.1 `ContainerKill` — a broker crash

- **Intent.** Reproduce the most common real incident: the Kafka JVM dies (OOM kill, segfault, `kill -9`). The pod, its IP, its volume mount, and the node's page cache survive. The kubelet restarts the container in place.
- **Expected Kafka behaviour.**
  - The broker stops heartbeating. The active controller fences it after `broker.session.timeout.ms` (default 9 s), then elects new leaders for the partitions it led.
  - During that window, produce and fetch to those partitions fail with timeouts or `NOT_LEADER_OR_FOLLOWER`.
  - The container restarts (`restartCount` +1), runs log recovery for segments past the recovery checkpoint (an unclean shutdown), re-registers, catches up, and rejoins the ISR.
  - Preferred leaders return at the next `leader.imbalance.check.interval.seconds` (300 s) if `auto.leader.rebalance.enable` is on. That is a second, smaller disturbance, which observation windows must include.
- **Mechanism.** The agent resolves the container's PID 1 (Strimzi images run `tini` as PID 1) and its `java` child. It sends the signal to the JVM, and `tini` exits with it.
- **Parameters.** `signal: SIGKILL | SIGTERM` (default `SIGKILL`); `process: Jvm | Init` (default `Jvm`).
- **Revert.** None needed. Completion waits for the container to be `Ready` again and records `status.targets[].replacement.readyAt`.
- **`injectedAt` means** the `kill(2)` call returned.
- **Pitfalls and guards.** Restarts are subject to CrashLoopBackOff: a second kill within the backoff window delays the restart exponentially. `repeat.every` below 30 s on the same target is rejected unless `reResolve` selects a different node.

#### 11.2.2 `PodKill` — forced pod deletion

- **Intent.** Parity with today's `POD_KILL`: the pod object disappears immediately and StrimziPodSet recreates it.
- **Expected Kafka behaviour.** Like `ContainerKill`, plus pod recreation: new UID, new IP, `kafka-init` runs again to write `broker.rack`, a cold JVM, and log recovery. Pod DNS names stay stable, so peers reconnect through the headless Service. Time to Ready is longer than `ContainerKill`.
- **Mechanism.** `DELETE pod` with `gracePeriodSeconds: 0`.
- **Parameters.** None.
- **Revert.** None. Completion waits for the replacement pod (new UID) to become Ready.
- **`injectedAt` means** the DELETE call returned.
- **Pitfalls and guards.** A forced delete removes the API object before the kubelet has stopped the old container. Strimzi recreates a pod with the same name on the same Kubernetes node, and for a short time two containers may use the same log directory. Kafka's log-directory `.lock` file stops the second JVM from starting, and it retries. This is why the spec recommends **`ContainerKill` for crash experiments** and keeps `PodKill` for parity and for "pod lost" semantics.

#### 11.2.3 `PodDelete` — graceful removal

- **Intent.** A planned restart, as a node upgrade or eviction would cause.
- **Expected Kafka behaviour.** SIGTERM starts a controlled shutdown (`controlled.shutdown.enable`, default on). The broker asks the controller to move its leaderships away *before* stopping, so clients see brief `NOT_LEADER_OR_FOLLOWER` retries and almost no unavailability. Anything that exceeds `terminationGracePeriodSeconds` (30 s) is SIGKILLed.
- **Mechanism.** `DELETE pod` with `gracePeriodSeconds` = `params.gracePeriodSec`, default: the pod's own value.
- **Revert.** None. Completion waits for the replacement pod to be Ready.
- **`injectedAt` means** the DELETE call returned, which is when SIGTERM is delivered.

#### 11.2.4 `ProcessPause` — a zombie broker

- **Intent.** Reproduce a long stop-the-world GC pause, a hung disk syscall, or a VM freeze. The process exists, holds its TCP connections, and answers nothing. Pod-level faults can't reproduce this, and it is the scenario that exercises KRaft fencing and leader-epoch protection most directly.
- **Expected Kafka behaviour.**
  - Heartbeats stop, and after `broker.session.timeout.ms` the broker is fenced and its leaderships move.
  - Clients with in-flight requests to the paused broker wait until `request.timeout.ms`.
  - On resume, the broker discovers it has been fenced, truncates to the new leader epochs where needed, catches up, and rejoins the ISR.
  - A paused **controller** that was the active leader causes a quorum election after `controller.quorum.fetch.timeout.ms` (10 s on the dev cluster).
- **Mechanism.** The agent writes `1` to the container cgroup's `cgroup.freeze` (cgroup v2 freezer) and waits until `cgroup.events` reports `frozen 1`. This freezes every process in the container atomically, including the Strimzi Kafka Agent that runs inside the broker JVM. If the freezer is unavailable, it falls back to `SIGSTOP` for each PID in `cgroup.procs`.
- **Parameters.** `allowLivenessRestart: false`.
- **Revert.** Write `0` to `cgroup.freeze` and wait for `frozen 0`. The fallback path sends `SIGCONT`.
- **`injectedAt` means** `cgroup.events` reported `frozen 1`.
- **Pitfalls and guards.** Strimzi's liveness probe is an `exec`. Its process is spawned into the frozen cgroup, so it hangs until its 5 s timeout. After `failureThreshold` failures (3 × 10 s) the kubelet kills the container, and a pause longer than about 30 s silently becomes a `ContainerKill`. The controller reads the target's probe settings and **rejects** such a duration unless `allowLivenessRestart: true`. If a restart happens anyway, it is recorded as `status.targets[].livenessRestart`.

#### 11.2.5 `RollingRestart`

- **Intent.** Rehearse the most frequent planned operation (config change, upgrade, certificate renewal) and measure client impact per step.
- **Expected Kafka behaviour.** One node down at a time, each with a controlled shutdown. Leaderships move before each stop, return through preferred-leader election, and the ISR is restored between steps.
- **Mechanism — `strategy: Sequential` (default).**
  1. Order the targets:
     - Brokers first, by node ID.
     - Then controllers, with the **active controller last**, so the quorum leader changes only once.
  2. For each target:
     1. Graceful `PodDelete`.
     2. Wait for the replacement to be Ready.
     3. Wait until `UnderReplicatedPartitions == 0`.
     4. For controllers, wait until `DescribeQuorum` shows every voter within `params.maxVoterLag` of the high watermark.
     5. Then wait `rollout.interval`.
- **Mechanism — `strategy: Strimzi`.**
  - Annotate each target's StrimziPodSet with `strimzi.io/manual-rolling-update: "true"`.
  - The Cluster Operator rolls the pods at its next reconciliation, in its own safe order, using its own availability checks.
  - This tests the operator's roll logic instead of the engine's.
- **Parameters.** `strategy`, `order: BrokersFirst | ControllersFirst`, `maxVoterLag` (default 1000 offsets), `stepTimeout` (default 10 min).
- **Revert.**
  - `Sequential`: nothing to undo. An abort stops *between* steps and never leaves two nodes down.
  - `Strimzi`: remove any annotation the operator has not yet consumed.
- **`injectedAt` means:**
  - `Sequential`: the first DELETE returned.
  - `Strimzi`: the first target pod was observed `NotReady`. The operator acts on its own reconciliation schedule (periodic, 120 s by default), so annotation time is meaningless.
- **Pitfalls and guards.** `duration` does not apply: the fault ends when every target has restarted and is healthy, or at `stepTimeout × targets`. In `Strimzi` mode the operator may defer a roll it judges unsafe, and the fault reports `Deferred` rather than fighting it.

#### 11.2.6 `BrokerHoldDown` (replaces `SCALE_DOWN`)

- **Intent.** Keep one Kafka node absent for `duration` *without changing cluster topology*. Node IDs, `KafkaNodePool` replicas, and partition assignments all stay the same. This is what today's `SCALE_DOWN` was meant to test: running degraded for minutes, not seconds.
- **Why not scale the node pool.** Reducing `KafkaNodePool.spec.replicas` is a permanent topology change. It removes a node ID and requires partitions to be moved off first (Strimzi blocks it otherwise). That is a migration, not a fault.
- **Expected Kafka behaviour.** The node leaves the ISR of every partition it replicated. Partitions stay available when RF − 1 ≥ `min.insync.replicas` (true for RF 3 / minISR 2). Durability margin is reduced for the whole window. When the node returns it catches up, and the catch-up time is a key output.
- **Mechanism.** The strategy is chosen automatically, and the choice is recorded in status:

  | Strategy | How it works | When chosen | Side effects |
  |---|---|---|---|
  | `CordonPinned` | Cordon the target's Kubernetes node, then delete the pod. The replacement stays `Pending`, because the pod is **pinned**: required node affinity plus a node-local PV mean it can only run there. | Pod is pinned to its current Kubernetes node. True on Kind (§5.2). | Blocks new scheduling of *any* pod onto that Kubernetes node for the duration. Pods already running are untouched. |
  | `PauseReconciliation` | Set `strimzi.io/pause-reconciliation: "true"` on the `Kafka` CR, wait for the `ReconciliationPaused` condition, then delete the pod. | Pod not pinned, **and** spike S1 confirms the StrimziPodSet controller does not recreate pods while the parent `Kafka` is paused. | The operator reconciles nothing for this cluster during the window. |
  | `SchedulingGate` | A mutating admission policy adds a `schedulingGates` entry to the recreated pod, matched by name. The engine removes the gate at revert. | Pod not pinned and S1 negative. Needs `MutatingAdmissionPolicy` or a small webhook (§28, Q2). | Only the one pod is affected. |

- **Parameters.** `strategy: Auto | CordonPinned | PauseReconciliation | SchedulingGate` (default `Auto`).
- **Revert.** Uncordon, unless the node was already cordoned (journaled). Unpause, only if the engine paused it (journaled). Remove the gate. Then wait for the pod to be Ready and the ISR to be restored, and record `catchUpAt`.
- **`injectedAt` means** the DELETE call returned.

### 11.3 Network faults

#### 11.3.1 Common mechanics

**Where rules live.** Everything runs **inside the target pod's network namespace**: the agent opens `/proc/<pid>/ns/net` and calls `setns(CLONE_NEWNET)` on a dedicated thread, then runs `nft` or `tc` from that thread (§20.6). Nothing is installed on the Kubernetes node's host namespace, and nothing depends on the CNI. When the pod dies, every rule dies with its namespace.

**Peer classes.** The controller resolves peer classes to concrete IPs and ports, so the agent never needs to understand Kafka.

| Peer class     | Resolves to                                                                                                                                                               | Why it matters                                                                                                 |
| -------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------- |
| `Replication`  | Other brokers' pod IPs, port 9091                                                                                                                                         | Follower fetches and leader responses. ISR membership.                                                         |
| `ControlPlane` | Controller pod IPs, port 9090                                                                                                                                             | Broker heartbeats and metadata fetches. KRaft Raft traffic between controllers.                                |
| `Clients`      | Every address not in the cluster, on the listener ports from `Kafka.spec.kafka.listeners` (9092 and 9093 today, plus any NodePort, LoadBalancer, Route, or Ingress ports) | Producer and consumer traffic.                                                                                 |
| `Operators`    | Cluster Operator and Entity Operator pod IPs, ports 9091 and 8443                                                                                                         | The Cluster Operator reads broker state through the Kafka Agent (8443) and uses the Admin API over 9091 (§12). |
| `Node`         | The target's Kubernetes node IP                                                                                                                                           | kubelet traffic: HTTP or TCP probes, if configured.                                                            |
| `Pods`         | `{selector}`, resolved to pod IPs                                                                                                                                         | e.g. one consumer application.                                                                                 |
| `Cidrs`        | Explicit CIDRs, optional ports                                                                                                                                            | Anything else.                                                                                                 |

Defaults: `NetworkPartition` affects `[Replication, ControlPlane, Clients]`. `Operators` and `Node` are **exempt** unless listed. Cutting the operator off changes its behaviour mid-experiment (§12.4). Cutting off the kubelet turns a partition into a restart.

**Peer churn.** Peers are stored in nftables *sets* (`ip daddr @peers`). If a peer pod is replaced during the fault and gets a new IP, the controller updates the injection's match list. The agent applies the change as a single atomic nft transaction (`flush set` + `add element`). tc filters for the affected class are replaced the same way.

**Directionality and ingress.** `tc` shapes only **egress**. The dev kernel has no `ifb` module (§5.1), so ingress shaping is unavailable. The engine implements direction by *where* it injects:

- `Egress`: rules in the target's netns, matching destination addresses.
- `Ingress`: rules in each peer's netns, matching the target's address. This creates one extra injection per peer.
- `Both`: both of the above.

Partitions (nftables) can filter both directions from the target's netns alone, because nftables has `input` and `output` hooks.

**Ownership markers.**
- nftables tables are named `kates_chaos_<faultId>_<n>`.
- tc qdiscs use handles from a reserved range, `0x7a00–0x7aff`, and every handle is recorded in `status.artifacts`.
- The startup sweep (§14.3) removes anything in these namespaces that no live injection claims.

**Classification.** Filters use `cls_u32`, matching destination IP (`/32`) and destination port. `cls_flower` would be simpler but isn't built into the dev kernel. The agent's capability report lets a future release use flower where it exists.

#### 11.3.2 `NetworkPartition`

- **Intent.** Cut traffic between a Kafka node and chosen peer classes. Partial and asymmetric partitions are the point: they are the scenarios Kafka's protocol is designed around.
- **Expected Kafka behaviour, by peer set:**

  | Peers cut | What Kafka does |
  |---|---|
  | `[ControlPlane]` on a broker | Heartbeats fail. The broker is fenced after `broker.session.timeout.ms` and its leaderships move. The broker may keep serving clients from stale metadata until it learns the new leader epochs. **Whether any `acks=1` writes accepted in that window are later truncated** is exactly what Kates' `DataIntegrityVerifier` should measure. |
  | `[Replication]` on a leader | Followers can't fetch. After `replica.lag.time.max.ms` (30 s) the leader shrinks the ISR to itself. With `min.insync.replicas=2`, `acks=all` produces fail with `NOT_ENOUGH_REPLICAS`, and `acks=1` keeps working. The leader stays leader, because it still heartbeats. |
  | `[Clients]` | Brokers and quorum are healthy. Clients see timeouts and metadata refresh to other brokers only for partitions led elsewhere. This isolates client retry behaviour. |
  | `[ControlPlane]` on a quorum follower | Voter lag grows. The quorum is healthy while a majority remains. |
  | Everything on the active controller | Quorum election after `controller.quorum.fetch.timeout.ms` (10 s on the dev cluster). Brokers reconnect to the new leader. |

- **Mechanism.** One nftables table with `input` and `output` chains at priority `-10`. They drop (or reject) packets matching the peer sets and ports. There is **no** `ct state established accept` rule, so existing TCP connections stall exactly as they would in a real network failure. NetworkPolicy often leaves established flows alone, depending on the CNI.
- **Parameters.** `peers: [...]` (default above), `direction: Both | Ingress | Egress` (default `Both`), `action: Drop | Reject` (default `Drop` — `Reject` sends TCP RST or ICMP unreachable, which is a fast failure and a different experiment).
- **Revert.** `nft delete table inet kates_chaos_<id>_<n>`.
- **`injectedAt` means** the `nft -f` transaction committed.

#### 11.3.3 `NetworkLatency`

- **Intent.** Slow links: a cross-AZ hop, a congested switch, a degraded NIC.
- **Expected Kafka behaviour.**
  - Latency on `Replication` from a leader adds to every `acks=all` produce for partitions it leads, because the high watermark advances only after follower fetches.
  - Latency on `ControlPlane` slows metadata propagation and, beyond the session timeout, causes fencing.
  - Latency on `Clients` affects only the clients.
- **Mechanism.**
  - A root `prio` qdisc with 3 bands and a priomap that sends all traffic to band 2 (unaffected).
  - A `netem` qdisc attached to band 3.
  - `u32` filters that steer matching destination IP/port into band 3.
  - Only matching traffic is delayed. Everything else, including kubelet and operator traffic, is untouched.
- **Parameters.** `delay` (100ms), `jitter` (0), `correlation` (0 %), `distribution: Normal | Pareto | ParetoNormal` (requires `jitter`), `peers`, `direction` (default `Egress`).
- **Revert.** `tc qdisc del dev eth0 root handle <h>:`.
- **`injectedAt` means** the last `tc` command returned.
- **Pitfalls and guards.** netem delay beyond `request.timeout.ms` or the session timeouts turns latency into unavailability. The controller warns when `delay + jitter` exceeds 50 % of `broker.session.timeout.ms` on `ControlPlane`.

#### 11.3.4 `NetworkLoss`

- **Mechanism.** Same qdisc tree, with `netem loss <p>% [correlation]`, `duplicate`, `corrupt`, and `reorder` (reorder requires a delay).
- **Expected Kafka behaviour.** TCP retransmissions, producer retries, and idempotent-producer deduplication at work. Replica fetcher throughput collapses non-linearly as loss rises.
- **Parameters.** `loss`, `lossCorrelation`, `duplicate`, `corrupt`, `reorder`, `peers`, `direction`.

#### 11.3.5 `NetworkBandwidth`

- **Mechanism.** An `htb` class with `rate`/`ceil` on the matching band, or `netem rate` when combined with delay.
- **Expected Kafka behaviour.** Replication on the constrained link falls behind under load, and followers drop out of the ISR once they lag more than `replica.lag.time.max.ms`. This is how "ISR shrink under throughput" is reproduced without killing anything.
- **Parameters.** `rate` (e.g. `10mbit`), `burst`, `peers`, `direction`.

#### 11.3.6 `DnsError`

- **Intent.** Name resolution failing or lying.
- **Expected Kafka behaviour.** Brokers and clients address each other by name: advertised listeners use pod DNS names under the `<cluster>-kafka-brokers` headless Service, and bootstrap uses a Service name. Existing connections are unaffected. New connections and reconnections fail.
  - The JVM caches successful lookups (`networkaddress.cache.ttl`, 30 s by default without a security manager) and failed ones (`networkaddress.cache.negative.ttl`, 10 s).
  - So the effect starts after up to 30 s. Checks and analysis must allow for that.
- **Mechanism.** No cross-node DNS traffic is involved.
  - **Bind a local resolver inside the target netns.** The agent creates UDP and TCP sockets *on a thread that has entered the target netns*, bound to `127.0.0.1:<port>`. A socket stays in the namespace where it was created. The agent serves them with an in-process resolver (`hickory-server`).
  - **Redirect DNS to it.** An nftables `nat output` chain in the target netns redirects destination port 53 to that local port.
  - **Answer queries.** Names matching `params.hostnames` get the configured failure. Every other query is forwarded to the pod's original nameservers, read from `/proc/<pid>/root/etc/resolv.conf`.
- **Parameters.** `hostnames: [glob]` (required, replacing the misuse of `targetTopic`), `response: NXDomain | ServFail | Timeout | Rewrite` (`Rewrite` answers with `params.address`, for "wrong IP" scenarios).
- **Revert.** Delete the nat table, then close the sockets.

### 11.4 Infrastructure faults

#### 11.4.1 `NodeDrain`

- **Intent.** Rehearse Kubernetes-node maintenance: cordon, then evict through the Eviction API, respecting PodDisruptionBudgets.
- **Expected behaviour on Strimzi — the interaction with PDBs and pinned volumes:**
  - Strimzi's `krafter-kafka` PDB allows **one** disruption across all six Kafka pods.
  - On the dev topology each Kubernetes node hosts one broker and one controller.
  1. The first eviction succeeds.
  2. The evicted pod is recreated by its StrimziPodSet and stays `Pending`, because it is pinned to the cordoned node.
  3. While it is Pending, the PDB allows zero disruptions, so the second eviction returns HTTP 429 **for the whole window**.
  - A drain therefore never completes on Kind. That is correct behaviour, and the engine reports it instead of looping.
- **Mechanism.**
  1. Cordon (`spec.unschedulable: true`).
  2. Evict Kafka pods on the Kubernetes node in `params.order` (default: brokers first, then controllers), plus other pods when `includeNonKafka`.
  3. HTTP 429 → record `blockedByPDB` for that pod and stop evicting. No retry storm.
- **Parameters.** `order: BrokersFirst | ControllersFirst`, `includeNonKafka: false`, `evictionPolicy: RespectPDB | DeleteIfBlocked` (default `RespectPDB`; `DeleteIfBlocked` falls back to `PodDelete`, and only if blast radius still allows it).
- **Revert.** Uncordon, unless the node was already cordoned before the fault (journaled). Pending pods then schedule.
- **`injectedAt` means** the first eviction returned 201. The cordon time is recorded separately.

#### 11.4.2 `ZoneOutage`

- **Intent.** Lose an availability zone, the scenario Kates' three-zone Kind topology exists for (D12).
- **Expected Kafka behaviour.**
  - With rack-aware placement (`broker.rack` = zone) and RF 3 across 3 zones, each partition loses at most one replica.
  - The ISR drops to 2 and still meets `min.insync.replicas=2`.
  - The KRaft quorum keeps 2 of 3 voters.
  - The cluster should stay fully available, with degraded durability.
  - Any partition whose replicas are *not* spread across zones shows up in the preflight blast-radius report as the cluster's real exposure.
- **Mechanism, by `params.mode`:**
  - `Isolate` (default): a `NetworkPartition` on every Kafka pod in the zone. Peers are every address outside the zone (other zones' Kafka pods and all clients). `Node` and `Operators` stay exempt unless specified.
  - `Kill`: cordon every Kubernetes node in the zone, then `PodKill` every Kafka pod in it. Pinned replacements stay `Pending` until revert, like a zone that never comes back within the window.
  - `Freeze`: `ProcessPause` on every Kafka pod in the zone.
- **Blast radius.** `safety.maxUnavailableZones` (default 1) replaces the broker and controller counts for this type. Per-partition `protectMinIsr` still applies (§15.4).
- **Revert.** Per mode: delete the nft tables, uncordon (journaled), or thaw.

### 11.5 Resource faults

#### 11.5.1 Stressor execution model

- **Spawning.** The agent spawns a child process of its own binary (`kates-chaos-agent stress …`). Before the child starts working, the agent **moves it into the target container's cgroup** by writing its PID to `<container-scope>/cgroup.procs`.
- **Why the cgroup matters.** The stressor then competes under the broker's own CPU, memory, and IO limits, like a noisy thread inside the broker. It also dies with the container if the container is killed (cgroup-scoped kill), so it can't be orphaned.
- **Filesystem access.** The agent resolves every path through `/proc/<pid>/root/…`, e.g. `/proc/<pid>/root/var/lib/kafka/data-0`, so it sees the volume exactly as the broker does without switching mount namespaces. Switching mount namespaces with `setns(CLONE_NEWNS)` would require a single-threaded process.
- **Ownership.** Each stressor sets its process title to `kates-chaos-stress:<faultId>`. The journal records its PID **and** start time (field 22 of `/proc/<pid>/stat`), so revert never signals a recycled PID.
- **Observed effect.** The agent samples the container's `cpu.stat` (`nr_throttled`, `throttled_usec`), `memory.stat`, `io.stat`, and PSI files (`cpu.pressure`, `memory.pressure`, `io.pressure`) before and during the fault. It publishes the deltas in `status.targets[].observations`. That turns "the stressor ran" into "the broker was throttled for 41 % of the window".

#### 11.5.2 `CpuStress`

- **Expected Kafka behaviour.** Broker pods are Guaranteed QoS with `cpu.max` = 8 CPUs, so the stressor's threads consume quota and CFS throttles the broker's network and request-handler threads. Request latency rises and p99 is hit first. Replica fetchers slow, and with enough throttling followers fall out of the ISR.
- **Parameters.** `workers` (default = `cpuCores` from `FaultSpec`, 1), `load` (0–100 % duty cycle per worker, default 100).
- **Revert.** SIGKILL the stressor's process group.

#### 11.5.3 `MemoryStress`

- **Expected Kafka behaviour.** The broker heap is fixed by `-Xmx`, but Kafka's performance depends on the **page cache**, which cgroup v2 charges to the container (`memory.current` includes file pages). Anonymous memory pressure makes the kernel reclaim the page cache first.
  - Consumers that were reading the log tail from cache start hitting disk, and fetch latency jumps.
  - Producers are mostly unaffected.
  - This is the realistic "noisy neighbour" effect on Kafka, and it's more useful than an OOM.
- **Mechanism.** `mmap` anonymous memory and touch every page, in chunks, up to the target.
- **Parameters.**
  - `mb` (500).
  - `mode: Reclaim | Oom`, default `Reclaim`. `Reclaim` caps the allocation at `memory.max − anon − 256Mi`, so the kernel reclaims cache but never OOM-kills. `Oom` needs `allowOomKill: true` and is an unavailable-class fault.
- **Revert.** SIGKILL the stressor. The memory is released immediately.

#### 11.5.4 `DiskFill`

- **Intent.** Approach and reach a full log directory.
- **Expected Kafka behaviour.**
  - Near full: nothing, until writes fail.
  - Full: the write fails with an `IOException`, and Kafka marks the log directory **offline**.
  - With a single JBOD directory (`data-0`), a broker with no online log directories shuts down, and its partitions fail over.
  - After space is freed the broker must be restarted. The engine performs a graceful `PodDelete` at revert when `params.restartIfOffline` is set.
- **The shared-filesystem hazard (D14).** On the dev cluster every PV is a directory on the same 1.8 TB filesystem as every Kubernetes node's root and Kates' own database. "Fill to 80 %" is meaningless there and destructive. The engine therefore classifies the log directory's filesystem before sizing anything:

  | Check | Source |
  |---|---|
  | PV type | `PersistentVolume.spec.local`/`hostPath`, or provisioner `rancher.io/local-path` |
  | Shares the node root filesystem? | `statvfs(<logdir>).f_fsid == statvfs(/proc/1/root).f_fsid`, seen from the agent with `hostPID` |

  When the filesystem is shared, `ChaosPolicy.diskFill.sharedFilesystemPolicy` decides:
  - `Budget` (default) converts the request into a fixed byte budget capped at `diskFill.maxBytes` (5Gi).
  - `Refuse` rejects the fault.
  - `Allow` sizes against the whole filesystem and requires explicit opt-in.
- **Kubelet eviction guard.** The controller rejects any fill that would push the Kubernetes node's `nodefs.available` below the kubelet's `evictionHard` threshold (default 10 %), unless `allowNodePressure: true`. Crossing that threshold makes the kubelet evict pods, which is a different experiment.
- **Mechanism.** `fallocate(2)` a file `.kates-chaos-fill-<id>` in the **volume root** (`/var/lib/kafka/data-0/`), not inside `kafka-log<id>/`, where Kafka scans for partition directories. If `fallocate` is unsupported, it writes zeroes in 64 MiB chunks, rate-limited.
- **Parameters.** `mode: Percent | Bytes | Headroom` (fill to leave exactly N bytes free), `percent` (80, hard cap 95 unless `allowFull`), `bytes`, `headroom`, `restartIfOffline: true`.
- **Revert.** `unlink`. The space is freed immediately because the agent holds no open descriptor.

#### 11.5.5 `IoStress`

- **Mechanism.** Stressor workers do `O_DIRECT` writes plus `fsync` (or reads, or both) on scratch files in `/var/lib/kafka/data-0/.kates-chaos-io-<id>/`. The files rotate within `maxBytes` (default 1Gi), so IO stress can't turn into a disk fill.
- **Expected Kafka behaviour.** Log flushes and segment rolls compete for the device. `acks=all` produce latency rises through slower follower appends.
- **Parameters.** `workers` (2), `blockSize` (1Mi), `mode: Write | Read | Mixed`, `maxBytes`.
- **Pitfall.** On a shared filesystem the IO load hits every pod on every Kubernetes node that shares the device (on Kind: all of them). This is flagged in status as `sharedDevice: true`.

#### 11.5.6 `DiskThrottle`

- **Intent.** A slow disk, the most under-tested Kafka failure mode.
- **Mechanism.**
  - Resolve the log directory's backing block device `major:minor` from `/proc/<pid>/mountinfo` and `/sys/dev/block`.
  - Write `"<maj>:<min> rbps=… wbps=… riops=… wiops=…"` to the container cgroup's `io.max`.
  - This requires the `io` controller to be enabled in the parent's `cgroup.subtree_control`, which the agent checks.
- **Expected Kafka behaviour.** `fsync` and segment writes stall. Produce latency rises in proportion. Followers on throttled disks lag and drop out of the ISR.
- **Parameters.** `readBps`, `writeBps`, `readIops`, `writeIops` (at least one).
- **Revert.** Restore the previous `io.max` line, which the journal recorded. Usually that means `max` for every field.

---

## 12. Living with the Strimzi Cluster Operator

### 12.1 What the operator does while chaos runs

| Operator behaviour                                                                                                                                                                                                                                                               | Effect on an experiment                                                                                                                                   |
| -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------- |
| The **StrimziPodSet controller** recreates any missing pod of a PodSet, event-driven, within seconds. Same name, same Kubernetes node (pinned).                                                                                                                                  | Kill and delete faults self-heal. That is intended. `BrokerHoldDown` must prevent it (§11.2.6).                                                           |
| **Kafka reconciliation** runs on changes and periodically (`STRIMZI_FULL_RECONCILIATION_INTERVAL_MS`, 120 s default). It renders config, rolls pods that need it (config drift, certificate renewal, `strimzi.io/manual-rolling-update`), and updates `Kafka.status.conditions`. | A reconciliation during a fault can **roll pods the engine did not touch**, e.g. during CA certificate renewal. That confounds the experiment.            |
| The **KafkaRoller** checks, through the Admin API on 9091 and the Kafka Agent on 8443, that restarting a broker won't take partitions below `min.insync.replicas`, and that controller restarts keep a quorum.                                                                   | During a fault that already costs availability, the operator may refuse or defer its own rolls. With `operator.mode: Observe` that is what should happen. |
| The operator reaches each broker over **9091 (mTLS Admin)** and **8443 (Kafka Agent)**.                                                                                                                                                                                          | A network fault that cuts these links changes what the operator believes about the broker. The `Operators` peer class is exempt by default (§11.3.1).     |
| The **Entity Operator** (topic and user operators) talks to brokers continuously.                                                                                                                                                                                                | It logs errors during faults. Not a safety concern.                                                                                                       |
| **Cruise Control** (not deployed in dev) moves partitions via `KafkaRebalance`.                                                                                                                                                                                                  | Reassignments in flight change replica sets and blast radius. Preflight refuses (§15.3).                                                                  |

### 12.2 Rules of engagement

1. The engine **never changes `spec`** of any Strimzi resource.
2. The only Strimzi mutations allowed are **annotations**:
   - `strimzi.io/manual-rolling-update` on `StrimziPodSet`
   - `strimzi.io/pause-reconciliation` on `Kafka`

   A `ValidatingAdmissionPolicy` bound to the controller's ServiceAccount enforces this (§21.2).
3. Each annotation change is journaled **with the previous value**. Revert restores that value. The engine never removes an annotation it did not add.
4. The engine never deletes or modifies PVCs, PVs, `KafkaTopic`, `KafkaUser`, or `KafkaNodePool` resources.

### 12.3 `spec.operator.mode`

- **`Observe` (default).** The operator behaves normally. The controller watches the target cluster's pods and records any restart it didn't cause in `status.observations.externalRestarts[]`, with timestamps, so Kates can tell engine-caused from operator-caused disruption in the report.
- **`Pause`.**
  1. Before injection, the controller sets `strimzi.io/pause-reconciliation: "true"` on the `Kafka` CR.
  2. It waits until the operator reports the `ReconciliationPaused` condition, so the pause is in effect before the clock starts.
  3. It injects, and removes the annotation at revert.
  - Use this for long network or resource faults, so the operator doesn't "heal" by restarting pods.
  - Refused when the annotation is already set by someone else.
  - Whether the StrimziPodSet controller also stops recreating pods while paused is spike S1. `Pause` makes no claim about it until S1 settles it.

### 12.4 Operator health as a precondition

A flapping operator makes results meaningless. The dev cluster's operator had restarted 197 times in 47 hours when this spec was written. Preflight (§15.3) therefore requires:

- the Cluster Operator Deployment is `Available`;
- the operator container has not restarted in the last `ChaosPolicy.spec.operatorStableFor` (default 10 min);
- `Kafka.status.observedGeneration == metadata.generation` and `Ready=True`;
- every StrimziPodSet of the cluster has `status.currentPods == status.pods`, which means no roll is in progress.

---

## 13. Lifecycle and state machine

### 13.1 Phases

```text
              ┌───────────┐
              │  Pending  │
              └─────┬─────┘
                    ▼
              ┌───────────┐  policy · opt-in · frozen · quarantined         ┌──────────────┐
              │ Accepted  ├────────────────────────────────────────────────►│              │
              └─────┬─────┘                                                 │              │
                    ▼                                                       │              │
              ┌───────────┐  cluster Lease not acquired by deadline         │   Rejected   │
              │ Scheduled ├────────────────────────────────────────────────►│  (terminal)  │
              └─────┬─────┘                                                 │              │
                    ▼                                                       │              │
              ┌───────────┐  preflight · blast radius · Before checks       │              │
              │ Resolving ├────────────────────────────────────────────────►│              │
              └─────┬─────┘                                                 └──────────────┘
                    │  no targets resolved                                  ┌──────────────┐
                    ├──────────────────────────────────────────────────────►│    Failed    │
                    │                                                       │  (NoTargets) │
                    ▼                                                       └──────────────┘
              ┌───────────┐
  ┌───────────┤ Injecting │
  │           └─────┬─────┘
  │ a target        ▼
  │ failed to ┌───────────┐
  │ apply     │  Active   │
  │           └─────┬─────┘
  │                 │  duration elapsed · repeat exhausted · abortOn breach
  │                 │  spec.abort · deletion · ChaosPolicy.frozen · deadline
  │                 ▼
  │           ┌───────────┐  undo failed after retries                      ┌──────────────┐
  └──────────►│ Reverting ├────────────────────────────────────────────────►│ RevertFailed │
              └─────┬─────┘                                                 │  (terminal,  │
                    ▼                                                       │ quarantines) │
              ┌───────────┐                                                 └──────────────┘
              │ Verifying │  After checks until they pass or `within` elapses
              └─────┬─────┘
                    ▼
              ┌───────────┐
              │ Completed │  verdict: Pass · Fail · Aborted · Error   (terminal)
              └───────────┘
```

| Phase          | Entered when                                      | Controller does                                                                                                                                     | Leaves when                                                                                                   |
| -------------- | ------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------- |
| `Pending`      | Object created                                    | Adds finalizer `chaos.kates.io/revert`; sets `deadline = creationTimestamp + delay + duration (or repeat window, or rollout timeout) + revertGrace` | Next reconcile                                                                                                |
| `Accepted`     | Policy and static validation pass                 | Condition `Accepted=True`                                                                                                                           | Immediately                                                                                                   |
| `Scheduled`    | Waiting for `delay` and the cluster Lease (§15.6) | Requeues at `notBefore`; holds or waits for the Lease                                                                                               | `delay` elapsed and Lease held → `Resolving`. Lease not obtained before `deadline` → `Rejected(ClusterBusy)`. |
| `Resolving`    | —                                                 | Preflight (§15.3), target resolution (§10), blast radius (§15.4), `Before` checks (§16)                                                             | All pass → `Injecting`. Otherwise → `Rejected` or `Failed`.                                                   |
| `Injecting`    | —                                                 | `operator.mode: Pause` handling; journal + API mutations, or creates `ChaosInjection`s; waits for every target to report applied                    | All targets applied → `Active`. Any target fails → `Reverting` with `verdict: Error`.                         |
| `Active`       | —                                                 | `During`/`Throughout` checks; `abortOn` every 2 s; `repeat` scheduling                                                                              | See diagram                                                                                                   |
| `Reverting`    | —                                                 | Executes the undo journal in reverse order; deletes `ChaosInjection`s and waits for their finalizers; unpauses the operator                         | All undo done → `Verifying`. An undo fails after retries → `RevertFailed`.                                    |
| `Verifying`    | —                                                 | `After` checks until they pass or their `within` elapses                                                                                            | → `Completed` with a verdict                                                                                  |
| `Completed`    | —                                                 | Releases the Lease if last of the run; sets TTL timer                                                                                               | TTL → object deleted                                                                                          |
| `RevertFailed` | —                                                 | Emits a Warning event, adds the cluster to `ChaosPolicy.spec.quarantine`, raises `kates_chaos_quarantined`                                          | Human action only (§15.8)                                                                                     |

### 13.2 Verdict rules

| Verdict   | Condition                                                                                                                                                                             |
| --------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `Pass`    | Injected on all targets, reverted cleanly, and every check met its expectation (`minPassRatio`, `After … within`).                                                                    |
| `Fail`    | Injected and reverted cleanly, but at least one check missed its expectation. *The fault worked and the cluster did not meet the hypothesis.* This is a finding, not an engine error. |
| `Aborted` | Stopped early by `abortOn`, `spec.abort`, deletion, freeze, or deadline. `failStep` names the trigger.                                                                                |
| `Error`   | The engine could not do what was asked: injection failed on a target, or a check could not be evaluated for reasons other than the cluster's state.                                   |

### 13.3 Abort and deletion semantics

- `spec.abort: true` → `Reverting` → `Verifying` (After checks still run, because recovery after an abort is still worth measuring) → `Completed{Aborted}`. The object remains for inspection.
- `DELETE` → the same path. The finalizer holds the object until revert completes, then removes itself.
- **Namespace deletion** → every fault in it is deleted, and so reverted, before the namespace goes.
- **Forced finalizer removal by a human** leaves node artifacts protected by the agent's own deadline and sweep (§14). The controller's journal is gone with the object, so API-level undo (uncordon, unpause) falls to `ChaosPolicy` reconciliation, which scans for engine-owned annotations and cordons (§14.4).

### 13.4 Reconcile idempotency

Every phase handler is safe to re-run after a crash at any line:

- Mutations are guarded by journal entries (§14.1).
- `ChaosInjection` names are deterministic (`<fault>-<targetIndex>`), and `create` treats "already exists" as success.
- Status updates use server-side apply with field manager `kates-chaos-controller`, and phase transitions check `observedGeneration`.

---

## 14. Revert guarantees

### 14.1 Write-ahead journal

The invariant is simple: **no cluster mutation happens unless its undo is already persisted.**

1. **Record intent.** Append `{seq, actor, action, object, undo, state: Intended, at}` to `status.journal`. This is a status patch guarded by `resourceVersion`; a conflict re-reads and retries.
2. **Mutate.**
3. **Mark the outcome.** Patch the entry to `Applied`. If the mutation provably did not happen (4xx other than conflict), mark it `NotApplied`.

On controller start, and on every reconcile of a non-terminal fault, every entry that is `Intended` or `Applied` and whose phase is `Reverting` or later has its `undo` executed. **Every undo is idempotent:**

| Action                      | Undo                                        | Idempotent because                                 |
| --------------------------- | ------------------------------------------- | -------------------------------------------------- |
| `DeletePod`, `EvictPod`     | `None` (Strimzi recreates)                  | —                                                  |
| `Cordon{wasCordoned:false}` | `Uncordon`                                  | Uncordoning a schedulable node is a no-op          |
| `Cordon{wasCordoned:true}`  | `None`                                      | The engine never uncordons a node a human cordoned |
| `Annotate{key, previous}`   | Restore `previous` (or remove if absent)    | Uses SSA with the engine's own field manager       |
| `CreateInjection{name}`     | Delete injection and wait for its finalizer | Deleting an absent object is success               |

The agent keeps the same kind of journal for node-level work in `/var/lib/kates-chaos/journal/<injection-uid>.json` (hostPath). Each entry is written with `fsync` of the file and its directory *before* the syscall that applies the fault.

### 14.2 Deadlines — the dead-man's switch

- **The controller** requeues every non-terminal fault at `status.deadline` and forces `Reverting` when it passes.
- **The agent** arms a local timer for each injection at `spec.expiresAt`, which the controller sets to `deadline − revertGrace/2`, so node faults are reverted before the controller's own deadline. **The agent's timer does not depend on the API server, the controller, or network connectivity.** If the agent can't write status when the timer fires, it reverts anyway, records the result in its local journal, and reports it when the API is reachable again.
- `ChaosPolicy.spec.agent.maxInjectionLifetime` (default 2h) is a hard ceiling that no `expiresAt` can exceed. It protects against a bad controller writing a far-future deadline.

### 14.3 Agent startup sweep

On every start, before watching:

1. **Replay the journal.** For each record: if past `expiresAt`, revert; otherwise re-arm its timer.
2. **Sweep for unclaimed artifacts.** For every running container on the Kubernetes node (enumerated through CRI):
   - nft tables named `kates_chaos_*`;
   - qdiscs with handles in `0x7a00–0x7aff`;
   - `cgroup.freeze = 1` on a container this agent froze (recorded in the journal; the agent never thaws something it didn't freeze);
   - `io.max` lines the journal recorded;
   - `.kates-chaos-fill-*` and `.kates-chaos-io-*` in Kafka volume roots;
   - processes titled `kates-chaos-stress:*`.

   Anything not claimed by a live `ChaosInjection` is reverted and reported as `kates_chaos_swept_artifacts_total`.
3. **Report capabilities and clock offset** to `ChaosPolicy.status.nodes[]`.

### 14.4 Crash matrix

| Failure during `Active`        | Who reverts                                                                                                                                                                         | Worst-case overrun                    |
| ------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------- |
| Kates pod dies                 | Controller at `duration`. Kates' `DisruptionOrphanReconciler` only marks the report `INTERRUPTED`.                                                                                  | 0                                     |
| Controller dies (leader)       | **Node faults:** agent timer at `expiresAt`. **API faults:** the standby replica takes the Lease within `leaseDuration` (15 s) and replays the journal. Kill-type faults self-heal. | Node: 0. API: ≤ lease takeover (15 s) |
| Both controller replicas down  | Node faults: agent timer. API faults: none until a controller returns. `kates_chaos_controller_up == 0` alerts.                                                                     | Node: 0. API: until recovery          |
| Agent dies                     | DaemonSet restarts it; startup sweep (§14.3)                                                                                                                                        | ≤ agent restart (seconds)             |
| API server unreachable         | Agent timer; controller retries with backoff                                                                                                                                        | Node: 0                               |
| Kubernetes node reboots        | netns, qdiscs, nft, freeze, stressors, and `io.max` vanish with the containers. Fill files persist and are removed by the sweep on agent start.                                     | ≤ node boot                           |
| Target pod replaced mid-fault  | Its netns and cgroup died with it. The agent marks the injection `Reverted{TargetGone}` and still removes fill or IO files by path if the same PVC is remounted.                    | 0                                     |
| Human force-removes finalizers | Agent sweep plus `ChaosPolicy` reconcile, which finds engine-owned annotations and cordons via field-manager ownership                                                              | ≤ next `ChaosPolicy` resync (60 s)    |

---

## 15. Safety model

Defence in depth. Each layer assumes the one before it is buggy.

```text
                                     ChaosFault request
                                              ▼
┌──────────────────────────────────────────────────────────────────────────────────────────┐
│ 1  Plan validation       maxAffectedBrokers, dry-run preview                  Kates      │
├──────────────────────────────────────────────────────────────────────────────────────────┤
│ 2  Admission (CEL)       static bounds on the ChaosFault spec                 API server │
├──────────────────────────────────────────────────────────────────────────────────────────┤
│ 3  ChaosPolicy           allowlist, opt-in, freeze, quarantine, concurrency   controller │
├──────────────────────────────────────────────────────────────────────────────────────────┤
│ 4  Preflight             Kafka + operator health, rebalances, steady state    controller │
├──────────────────────────────────────────────────────────────────────────────────────────┤
│ 5  Blast radius          per-partition ISR vs min ISR, KRaft majority         controller │
├──────────────────────────────────────────────────────────────────────────────────────────┤
│ 6  Runtime abort         abortOn, every 2 s while Active                      controller │
├──────────────────────────────────────────────────────────────────────────────────────────┤
│ 7  Agent re-validation   namespace, pod UID, container ID, capability         agent      │
├──────────────────────────────────────────────────────────────────────────────────────────┤
│ 8  Deadlines             controller and agent, independently                  both       │
└─────────────────────────────────────────────┬────────────────────────────────────────────┘
                                              ▼
                                       fault injected
```

### 15.1 Admission

A `ValidatingAdmissionPolicy` with CEL rules, and a binding to all namespaces:

- exactly one target selector;
- `duration ≤ 1h`, `delay ≤ 1h`, `repeat.every ≥ 5s` and `< duration`;
- `params.percent ≤ 95` unless `params.allowFull`;
- `spec` is immutable except `abort` (false → true) and `ttlSecondsAfterFinished`;
- `ChaosInjection` may be created only by the controller's ServiceAccount (`request.userInfo.username` check).

No webhook server, no certificates.

### 15.2 Opt-in and scope

- The target `Kafka` CR must carry `chaos.kates.io/enabled: "true"` when `ChaosPolicy.spec.requireOptIn`. The Kates `kafka-cluster` chart sets it in the Kind values and leaves it off in generic values.
- The fault's namespace must be in `ChaosPolicy.spec.allowedNamespaces`.
- Targets must carry `strimzi.io/cluster=<spec.cluster>`. Pods of the engine, Kates, and `kube-system` can never be targeted, and the agent re-checks this.

### 15.3 Preflight

All must hold at `Resolving` (reason on failure in brackets):

| Check                                                                               | Source                       | Reason                            |
| ----------------------------------------------------------------------------------- | ---------------------------- | --------------------------------- |
| Kafka CR `Ready=True`, `observedGeneration == generation`                           | Kubernetes                   | `ClusterNotReady`                 |
| No roll in progress: every StrimziPodSet `currentPods == pods == readyPods`         | Kubernetes                   | `RollInProgress`                  |
| Cluster Operator available and stable (§12.4)                                       | Kubernetes                   | `OperatorUnstable`                |
| No `KafkaRebalance` in `Rebalancing` or `ProposalReady` with auto-approval          | Kubernetes                   | `RebalanceInProgress`             |
| No partition reassignment in flight                                                 | `ListPartitionReassignments` | `ReassignmentInProgress`          |
| `UnderReplicatedPartitions == 0`, `OfflinePartitions == 0`                          | Metadata                     | `NotSteady`                       |
| KRaft quorum has a leader, and every voter's lag ≤ threshold                        | `DescribeQuorum`             | `QuorumUnhealthy`                 |
| All `Before` checks pass                                                            | §16                          | `SteadyStateNotMet`               |
| Every target Kubernetes node's agent is Ready and supports the fault's capabilities | `ChaosPolicy.status.nodes`   | `AgentUnavailable`, `Unsupported` |

### 15.4 Blast radius

Computed on the **resolved** targets, just before injection.

```text
affectedPods   = targets
               ∪ (NodeDrain / ZoneOutage: every Kafka pod on affected Kubernetes nodes)
unavailable    = affectedPods where fault class = Unavailable
brokersDown    = { nodeId(p) | p ∈ unavailable, Broker ∈ roles(p) }
votersDown     = { nodeId(p) | p ∈ unavailable, Controller ∈ roles(p) }

require |brokersDown| ≤ safety.maxUnavailableBrokers                       (default 1)
require |votersDown|  ≤ min(safety.maxUnavailableControllers,
                            ⌊(|voters| − 1) / 2⌋)                          (keep a majority)
if type = ZoneOutage:  require |zones(unavailable)| ≤ safety.maxUnavailableZones (default 1)

if safety.protectMinIsr:
    for each partition p of every topic, internal topics included
            (__consumer_offsets, __transaction_state, __share_group_state):
        remainingReplicas = replicas(p) \ brokersDown
        remainingIsr      = isr(p)      \ brokersDown
        minIsr            = effective min.insync.replicas of topic(p)      (DescribeConfigs, cached per run)
        if |remainingReplicas| = 0          → reject(WouldGoOffline, p)     # never overridable
        if |remainingIsr| < minIsr
           and not safety.allowUnderMinIsr  → reject(WouldBreakMinIsr, p)

if the fault evicts pods (NodeDrain):
    report PDB.status.disruptionsAllowed for every PDB selecting affected pods
    (no rejection — PDB denials are observed behaviour, §11.4.1)
```

Degraded-class faults skip the availability rules but are listed with their targets. With `protectMinIsr`, a degraded fault that is *expected* to shrink the ISR (e.g. `NetworkPartition{peers:[Replication]}` on a leader) is classified as unavailable for the partitions it leads.

**Why ISR and not replicas:** preflight already requires `URP == 0`, so ISR equals replicas at injection time. Using the ISR keeps the rule correct if a future release relaxes that preflight.

**The report.** `status.blastRadius` lists `brokersDown`, `votersDown`, zones, the count of partitions whose ISR would drop to `minIsr`, and the partitions that would break it. Kates' dry-run surfaces this instead of today's pod-count estimate.

### 15.5 Runtime abort — `safety.abortOn`

Evaluated by the controller every 2 s while `Active`, using the Kafka checks in §16.2:

| Key                                        | Aborts when                                                                                |
| ------------------------------------------ | ------------------------------------------------------------------------------------------ |
| `offlinePartitions: n`                     | `OfflinePartitions > n`                                                                    |
| `underMinIsrPartitions: n`                 | `UnderMinIsrPartitions > n`                                                                |
| `controllerQuorumLost: true`               | `DescribeQuorum` has no leader for longer than 2 × `controller.quorum.election.timeout.ms` |
| `consumerLag: {group, max}`                | Lag of the group > `max`                                                                   |
| `produceFailures: {topic, maxConsecutive}` | The canary produce fails more than N times in a row                                        |

On breach: `verdict: Aborted`, `failStep: abortOn.<key>`, and the observed value recorded. Kates' `kates.chaos.rollback.*` properties (`min-isr-depth`, `max-lag-spike`) are translated into these keys by `KatesChaosProvider`. That finally connects the rollback thresholds that D9 shows are never evaluated today.

### 15.6 Concurrency

- A `coordination.k8s.io/v1` Lease named `kates-chaos-<namespace>-<cluster>` in `kates-chaos`, held by `kates.io/run-id`. It is taken at `Scheduled`, renewed while any fault of the run is non-terminal, and released when the run's last fault completes.
- Faults of the **same run** share the Lease, so `CompoundChaosOrchestrator` can run concurrent faults. Blast radius is then computed over the **union** of the run's active unavailable targets.
- Faults of a different run wait in `Scheduled` (`reason: ClusterBusy`) until the Lease frees or their deadline passes.
- `ChaosPolicy.spec.maxConcurrentFaultsPerCluster` caps the total.

This replaces the per-JVM guard (D11) with a cluster-wide one. The Java guard stays as a fast in-process check.

### 15.7 Kill switch

`ChaosPolicy.spec.frozen: true` (CLI: `kates chaos freeze`):

- every `Active` fault goes to `Reverting` with `failStep: frozen`;
- new faults are rejected with `Frozen`;
- agents refuse new injections, which they read from the policy object they already watch.

`kates chaos thaw` clears it. The freeze is itself a Kubernetes object, so it is audited and survives restarts.

### 15.8 Quarantine

`RevertFailed` means the engine does not know the cluster's state. The cluster (`namespace/name`) is appended to `ChaosPolicy.spec.quarantine` with the fault name and failed journal entry. Faults against a quarantined cluster are rejected until a human inspects it and runs `kates chaos quarantine clear <ns>/<cluster>`. Chaos is never stacked on an unknown state.

---

## 16. Steady-state checks

### 16.1 Model

A check is a hypothesis about the cluster, evaluated **by the controller, outside the brokers** (fixes D8), in explicit phases:

| Phase        | When evaluated                                               | Semantics                                                                                                                   |
| ------------ | ------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------- |
| `Before`     | Once, in `Resolving`                                         | Must pass, or the fault is rejected (`SteadyStateNotMet`).                                                                  |
| `OnInject`   | Once, immediately after every target is applied              | Captures the instantaneous effect.                                                                                          |
| `During`     | Every `interval` while `Active`                              | Pass ratio must be ≥ `minPassRatio` (default 1.0).                                                                          |
| `After`      | Every `interval` from revert until it passes, up to `within` | Must pass within `within`. **The first pass time is recorded as `recoveredAt`**, a direct time-to-steady-state measurement. |
| `Throughout` | From `Before` to the end of `After`                          | Pass ratio across the whole run.                                                                                            |

`expect: Pass | Fail` (default `Pass`) lets a check assert a failure. For example: *during a replication partition of the leader, `acks=all` produce to `orders` must fail* — proving the fault had the intended effect and Kafka enforced `min.insync.replicas`.

**Fail-closed.** Any error (connect, TLS, SASL, timeout, parse, unknown topic) counts as a *failed evaluation* and is recorded with its error. There are no vacuous passes (fixes D7).

**Check spec.** Every entry of `spec.checks[]` has this shape, with exactly one kind key (`kafka`, `k8s`, `http`, `promql`, `exec`):

```yaml
- name: isr-restored              # unique within the fault; used in failStep
  kafka: { check: UnderReplicatedPartitions, max: 0, topics: [orders] }
  phase: After                    # Before | OnInject | During | After | Throughout
  interval: 5s                    # During / After / Throughout (default 5s, min 1s)
  timeout: 5s                     # per evaluation (default 5s)
  startAfter: 0s                  # During only: skip evaluations in the first N seconds of Active
  within: 3m                      # After only: must pass within this window after revert
  minPassRatio: 1.0               # During / Throughout (0.0–1.0)
  expect: Pass                    # Pass | Fail
```

### 16.2 Kafka checks

The controller connects to the cluster with the `kates-chaos` `KafkaUser` (§19.5) over the TLS listener (9093, mTLS).

| Check                                           | Kafka API                                                       | Passes when                                                                              |
| ----------------------------------------------- | --------------------------------------------------------------- | ---------------------------------------------------------------------------------------- |
| `UnderReplicatedPartitions{max, topics?}`       | Metadata (or `DescribeTopicPartitions`, KIP-966)                | count(`isr < replicas`) ≤ max                                                            |
| `OfflinePartitions{max}`                        | Metadata                                                        | count(`leader = -1`) ≤ max                                                               |
| `UnderMinIsrPartitions{max}`                    | Metadata + `DescribeConfigs`                                    | count(`isr < min.insync.replicas`) ≤ max                                                 |
| `ProduceAck{topic, acks: All\|Leader, timeout}` | `Produce`                                                       | Ack within timeout with no error; latency recorded                                       |
| `EndToEnd{topic, timeout}`                      | `Produce` + `ListOffsets` + `Fetch`                             | The record produced is fetched back within timeout, like a canary                        |
| `ControllerQuorum{maxVoterLag}`                 | `DescribeQuorum`                                                | A leader exists and every voter's `logEndOffset` is within `maxVoterLag` of the leader's |
| `LeaderOf{topic, partition, notNodeIds?}`       | Metadata                                                        | The leader exists (and is not in `notNodeIds`) — detects leadership moves                |
| `BrokersRegistered{min}`                        | `DescribeCluster`                                               | Registered broker count ≥ min                                                            |
| `ConsumerLag{group, max}`                       | `OffsetFetch` + `ListOffsets`                                   | Σ lag ≤ max                                                                              |
| `GroupState{group, states}`                     | `DescribeGroups` (classic) or `ConsumerGroupDescribe` (KIP-848) | State ∈ states (e.g. `Stable`)                                                           |

The controller keeps **one** long-lived connection per broker and caches topic configs for a run. A Metadata round trip costs microseconds on the broker side, where the old probe started a JVM in the broker every 10 s.

The canary topic `kates-probe` (RF 3, `min.insync.replicas=2`, one partition per broker via explicit assignment) is created by the kafka-cluster chart as a `KafkaTopic`, not by the engine (P2).

### 16.3 Kubernetes checks

| Check                  | Passes when                                                |
| ---------------------- | ---------------------------------------------------------- |
| `KafkaReady`           | `Kafka.status.conditions[Ready] == True`                   |
| `PodsReady{role, min}` | At least `min` Kafka pods of `role` are Ready              |
| `PodSetsCurrent`       | Every StrimziPodSet has `currentPods == readyPods == pods` |

### 16.4 HTTP and PromQL checks

- `Http{url, expectStatus, bodyContains?, timeout}` — for application health endpoints.
- `PromQL{query, comparator, value}` — runs against the Prometheus that Kates already uses (`kates.prometheus.url`), e.g. `sum(kafka_server_replicamanager_underreplicatedpartitions) == 0`.

### 16.5 Exec checks (compatibility)

`Exec{pod, container, command, expect}` runs in a **named** pod that must not be a target unless `allowOnTarget: true`. A non-zero exit fails. This exists so existing `ProbeSpec{type: cmdProbe}` definitions keep working. The built-in Kafka checks replace every default probe in `KafkaProbes`.

### 16.6 Scoring and mapping to Kates

- `status.checks[]` holds per-check evaluations, passes, `firstFailAt`, and `recoveredAt`.
- `checksPassedRatio` = passed evaluations ÷ total evaluations × 100, as a string. This is today's `probeSuccessPercentage`.
- `failStep` = `check.<name>` or `abortOn.<key>` for the first failure. This is today's `failStep`.
- `ProbeRegistry` defaults map to Kafka checks:
  - `isrHealth` → `UnderReplicatedPartitions`
  - `minIsr` → `UnderMinIsrPartitions`
  - `partitionAvailability` → `OfflinePartitions`
  - `clusterReady` → `KafkaReady`
  - `producerThroughput` → `ProduceAck`
  - `consumerLatency` → `ConsumerLag`

---

## 17. Time and measurement semantics

### 17.1 What `injectedAt` means, per fault

| Fault                       | `injectedAt` is when…                                         | Clock              |
| --------------------------- | ------------------------------------------------------------- | ------------------ |
| `PodKill`, `PodDelete`      | the DELETE returned 200                                       | controller         |
| `ContainerKill`             | `kill(2)` returned                                            | agent              |
| `ProcessPause`              | `cgroup.events` read `frozen 1`                               | agent              |
| `RollingRestart` Sequential | the first DELETE returned                                     | controller         |
| `RollingRestart` Strimzi    | the first target pod was observed `Ready=False`               | controller (watch) |
| `BrokerHoldDown`            | the DELETE returned                                           | controller         |
| `NodeDrain`                 | the first eviction returned 201                               | controller         |
| Network faults              | the `nft`/`tc` command that completes the rule set returned 0 | agent              |
| `DnsError`                  | the redirect rule committed                                   | agent              |
| Stress faults               | the stressor signalled "running" over its pipe                | agent              |
| `DiskFill`                  | `fallocate` returned                                          | agent              |
| `DiskThrottle`              | the `io.max` write returned                                   | agent              |

All timestamps are UTC RFC 3339 with microseconds, sampled with `SystemTime::now()` immediately after the call.

### 17.2 Mapping onto Kates' monotonic clock

`ChaosOutcome.chaosStartNanos` must be comparable with `AckTracker`'s `System.nanoTime()` timestamps. `KatesChaosProvider`:

1. just before creating the CR, samples `wall₀ = Instant.now()` and `mono₀ = System.nanoTime()`;
2. on completion computes `chaosStartNanos = mono₀ + (injectedAt − wall₀).toNanos()`.

This assumes the agent's wall clock and the JVM's agree:

- on Kind all nodes share the host kernel clock, so the error is effectively zero;
- on real clusters, chrony-synchronised nodes are typically within 1 ms, far below Kafka's recovery timescales (seconds).

The agent measures its offset against the controller (NTP-style, over the status round trip) and publishes `kates_chaos_clock_offset_seconds{node}`. `KatesChaosProvider` warns in the report when |offset| > 10 ms.

### 17.3 Orchestrator alignment (fixes D1 for every provider)

`DisruptionOrchestrator` currently calls `session.markDisruptionStart()` and the tracker equivalents before `triggerFault`. The change:

- add `markDisruptionStart(Instant at)` overloads;
- call them **after** the outcome arrives, with `outcome.chaosStartTime()`;
- recompute TFR and TAR from that instant.

The pod watcher, ISR tracker, and lag tracker already record raw events with timestamps, so re-basing after the fact is exact.

### 17.4 Observations

Besides timestamps, the controller records Kafka-observed milestones at 1 s resolution in `status.observations`:

- `leaderMovedAt` per targeted partition;
- `firstUnderReplicatedAt` and `underReplicatedClearedAt`;
- `quorumLeaderChangedAt`;
- `externalRestarts` (§12.3);
- per-target resource deltas (§11.5.1).

Kates copies them into the step report as additive fields.

---

## 18. Kates integration

### 18.1 Java changes

| File                                                               | Change                                                                                                                                                                                                                                                                                                                                                                                                                                                              | Fixes       |
| ------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------- |
| `chaos/KatesChaosProvider.java` (**new**, `@Named("kates-chaos")`) | Builds a `ChaosFault` from a `FaultSpec` (§18.2) and creates it. Follows it with a fabric8 **informer** scoped to its name, not polling. Completes the future on a terminal phase, maps status to `ChaosOutcome` (§18.3), and handles the clock mapping (§17.2). `pollStatus` reads `status.phase`. `cleanup(engineName)` deletes **that** CR and waits for its finalizer. `isAvailable()` requires the CRDs plus `ChaosPolicy.status.controller.leader` to be set. | D1, D2, D10 |
| `chaos/kates/**` (**generated**)                                   | Model classes generated from `charts/kates-chaos/crds/*.yaml` by `io.fabric8:java-generator-maven-plugin`; registered in `NativePayloadReflectionConfig` for the native image                                                                                                                                                                                                                                                                                       | —           |
| `HybridChaosProvider`                                              | Detection order: `chaos.kates.io` CRDs with a live controller → `litmuschaos.io` CRDs → `kubernetes`                                                                                                                                                                                                                                                                                                                                                                | —           |
| `application.properties`                                           | `kates.chaos.provider=hybrid` (was `litmus-crd`), plus the new `kates.chaos.engine.*` keys (§18.4)                                                                                                                                                                                                                                                                                                                                                                  | —           |
| `DisruptionType`                                                   | Add `CONTAINER_KILL`, `PROCESS_PAUSE`, `NETWORK_LOSS`, `NETWORK_BANDWIDTH`, `DISK_THROTTLE`, `ZONE_OUTAGE`. Other providers throw `UnsupportedOperationException`, as the kubernetes provider already does for unknown types.                                                                                                                                                                                                                                       | —           |
| `FaultSpec`                                                        | Add `targetRole`, `targetZone`, `targetPool`, `targetNodeIds`, and `Map<String,String> params` for typed per-type extras. Deprecate `envOverrides` (Litmus-only) and the use of `targetTopic` as DNS hostnames. Add `toBuilder()`.                                                                                                                                                                                                                                  | D13         |
| `DisruptionOrchestrator`                                           | Use `toBuilder()` for leader re-targeting. Re-base disruption start on `outcome.chaosStartTime()` (§17.3). While the observation window runs, evaluate `AutoRollbackGuard` every `isrPollIntervalMs` and call `chaosCoordinator.cleanup(engineName)` on a breach. That covers non-engine providers; the engine enforces the same limits itself via `abortOn`.                                                                                                       | D1, D9, D13 |
| `DisruptionSafetyGuard`                                            | Classify by `strimzi.io/broker-role` and `strimzi.io/controller-role`. With `kates-chaos` active, delegate the blast-radius preview to a server-side dry run: create the fault with the `chaos.kates.io/dry-run: "true"` annotation, and the controller stops after `Resolving` with `status.blastRadius`. `rollback()` becomes "delete the `ChaosFault`". The RBAC check becomes `create chaosfaults` and fails closed. The StatefulSet code is removed.           | D4, D5      |
| `DisruptionOrphanReconciler`                                       | With `kates-chaos` active, only marks reports `INTERRUPTED`. The StatefulSet branch is removed.                                                                                                                                                                                                                                                                                                                                                                     | D4          |
| `KubernetesChaosProvider`                                          | STS branches removed. CPU/IO stress use the `pods/ephemeralcontainers` subresource, or are dropped from this provider in favour of the engine.                                                                                                                                                                                                                                                                                                                      | D4, D6      |
| `ProbeExecutor`, `KafkaProbes`                                     | Kept for the `kubernetes`/`noop` providers. Comparators fail closed. `KatesChaosProvider` translates `ProbeSpec`s into engine checks (§16.6).                                                                                                                                                                                                                                                                                                                       | D7          |
| `playbooks/az-failure.yaml`                                        | Rewritten as a `ZONE_OUTAGE` step with `targetZone: alpha`                                                                                                                                                                                                                                                                                                                                                                                                          | D12         |
| `ChaosTemplateCatalog`, `DisruptionPlaybookCatalog`                | New templates: *leader cascade* (`POD_KILL` + `leaderOf` + repeat), *controller failover* (`POD_KILL` + active controller), *zombie broker* (`PROCESS_PAUSE`), *ISR shrink* (replication partition), *slow disk* (`DISK_THROTTLE`), *zone isolation* (`ZONE_OUTAGE`)                                                                                                                                                                                                | —           |
| `DisruptionReport.StepReport`                                      | Additive fields: `faultTargets`, `checkResults`, `observations`, `blastRadius`                                                                                                                                                                                                                                                                                                                                                                                      | —           |
| `charts/kates/templates/rbac.yaml`                                 | Add `chaos.kates.io` `chaosfaults` (create, get, list, watch, delete, patch), `chaosfaults/status` (get), `chaospolicies` (get, list, watch)                                                                                                                                                                                                                                                                                                                        | —           |

### 18.2 `FaultSpec` → `ChaosFault`

| `FaultSpec`                                                                                                                                | `ChaosFault`                                                                                                                   |
| ------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------ |
| `experimentName` + epoch ms                                                                                                                | `metadata.name` (DNS-1123-sanitised, ≤ 63 chars); original in `kates.io/experiment` annotation                                 |
| `targetNamespace`                                                                                                                          | `metadata.namespace`                                                                                                           |
| `kates.chaos.kafka.cluster`                                                                                                                | `spec.cluster`                                                                                                                 |
| `disruptionType`                                                                                                                           | `spec.type` (§11.1). `LEADER_ELECTION` → `PodKill` + `leaderOf`. `SCALE_DOWN` → `BrokerHoldDown`.                              |
| First non-empty of `targetPod`, `targetNodeIds`/`targetBrokerId`, `targetTopic+targetPartition`, `targetZone`, `targetPool`, `targetLabel` | `spec.target.{podNames, nodeIds, leaderOf, zone, pool, selector}`. `targetLabel` is parsed as a real selector, so commas work. |
| `targetRole`                                                                                                                               | `spec.target.role`                                                                                                             |
| `chaosDurationSec`, `delayBeforeSec`                                                                                                       | `spec.timing.duration`, `spec.timing.delay`                                                                                    |
| `networkLatencyMs`, `fillPercentage`, `cpuCores`, `memoryMb`, `ioWorkers`, `gracePeriodSec`, `params`                                      | `spec.params.*`, per type                                                                                                      |
| `probes` or `ProbeRegistry` defaults                                                                                                       | `spec.checks`                                                                                                                  |
| plan `autoRollback` + `kates.chaos.rollback.*`                                                                                             | `spec.safety.abortOn`                                                                                                          |
| plan `maxAffectedBrokers`                                                                                                                  | `spec.safety.maxUnavailableBrokers` (the smaller of the two)                                                                   |
| run id (new, generated per `DisruptionOrchestrator.execute`)                                                                               | label `kates.io/run-id`                                                                                                        |

### 18.3 `ChaosFault.status` → `ChaosOutcome`

| `ChaosOutcome`           | Source                                                                      |
| ------------------------ | --------------------------------------------------------------------------- |
| `engineName`             | `metadata.name`                                                             |
| `experimentName`         | annotation `kates.io/experiment`                                            |
| `chaosStartTime`         | `status.injectedAt`                                                         |
| `chaosEndTime`           | `status.revertedAt`                                                         |
| `chaosStartNanos`        | §17.2                                                                       |
| `chaosDuration`          | `revertedAt − injectedAt`                                                   |
| `verdict`                | `status.verdict` (`Pass`/`Fail`/`Aborted`/`Error`); `isPass()` is unchanged |
| `failureReason`          | `status.reason: status.message`                                             |
| `probeSuccessPercentage` | `status.checksPassedRatio`                                                  |
| `failStep`               | `status.failStep`                                                           |
| `phase`                  | `status.phase`                                                              |

The record shape is unchanged, so reports, the CLI (`kates chaos show`, `kates disruption status`), and the database need no migration.

### 18.4 Configuration

```properties
kates.chaos.provider=hybrid
kates.chaos.engine.ttl-after-finished-sec=3600
kates.chaos.engine.completion-grace-sec=120     # future timeout = deadline + grace
kates.chaos.engine.operator-mode=Observe        # default for plans that don't set it
kates.chaos.engine.dry-run-timeout-sec=30
```

### 18.5 CLI

- `kates deploy` installs the engine by default (`--chaos-engine=kates|litmus`, default `kates`). It creates the `kates-chaos` namespace with PSA labels before installing the chart (§19.2).
- New commands (Cobra, following `cli/cmd/chaos.go` style):
  - `kates chaos faults [-A]` — table of `ChaosFault`s
  - `kates chaos freeze` / `kates chaos thaw` — the kill switch
  - `kates chaos quarantine list|clear <ns>/<cluster>`
  - `kates chaos nodes` — agent readiness, versions, capabilities, clock offset
- `kates disruption types` needs no change, because it reads the backend.
- The existing gates `check-cli-compat.sh` and `check-cli-style.sh` must stay green.

---

## 19. Packaging and deployment

### 19.1 Chart `kates-chaos` 3.0.0

```text
charts/kates-chaos/
├── Chart.yaml                          version 3.0.0 · appVersion = engine version
│                                       litmus-core dependency only when engine=litmus
├── crds/                               generated: chaosfaults, chaosinjections, chaospolicies
└── templates/
    ├── engine/
    │   ├── controller-deployment.yaml  2 replicas · zone anti-affinity · leader election
    │   ├── controller-rbac.yaml        ClusterRole + per-namespace Roles (Secrets by name)
    │   ├── controller-service.yaml     metrics
    │   ├── controller-pdb.yaml         minAvailable: 1
    │   ├── agent-daemonset.yaml        hostPID · capabilities · hostPath mounts · tolerate all
    │   ├── agent-rbac.yaml
    │   ├── chaospolicy.yaml            singleton `default`, from values.policy
    │   ├── admission-policy.yaml       ValidatingAdmissionPolicy + binding (§15.1, §21.2)
    │   ├── servicemonitor.yaml
    │   └── crd-upgrade-job.yaml        pre-install/pre-upgrade hook: server-side apply of crds/
    ├── litmus/                         existing templates, rendered only when engine=litmus
    ├── kyverno-policies.yaml           excludeSelector gains the agent's component label
    └── tests/                          helm test: controller and agents Ready, dry-run fault resolves
```

`values.yaml` (excerpt):

```yaml
engine: kates                         # kates | litmus
kateschaos:
  controller:
    image: { repository: ghcr.io/bmscomp/kates-chaos-controller, tag: "", digest: "" }
    replicas: 2
    resources: { requests: { cpu: 50m, memory: 64Mi }, limits: { memory: 128Mi } }
    logLevel: info
  agent:
    image: { repository: ghcr.io/bmscomp/kates-chaos-agent, tag: "", digest: "" }
    resources: { requests: { cpu: 10m, memory: 24Mi }, limits: { memory: 96Mi } }
    runtimeSocket: /run/containerd/containerd.sock   # /var/run/crio/crio.sock for CRI-O
    stateDir: /var/lib/kates-chaos
    nodeSelector: {}
    tolerations: [{ operator: Exists }]
  policy:
    allowedNamespaces: [kafka]
    requireOptIn: true
    operatorStableFor: 10m
    diskFill: { sharedFilesystemPolicy: Budget, maxBytes: 5Gi }
```

### 19.2 Namespaces and Pod Security

- The engine runs in its own namespace, `kates-chaos`, labelled `pod-security.kubernetes.io/enforce: privileged`. That is required by the agent's `hostPID` and capabilities.
- The `kafka` and `kates` namespaces keep their stricter levels, because nothing privileged runs there.
- Helm cannot reliably label its own release namespace, so `kates deploy` creates and labels it. The chart's `NOTES.txt` and `helm test` check the label and explain the fix when it's missing.

### 19.3 CRD lifecycle

Helm installs `crds/` on first install only and never upgrades them. The chart therefore ships a `pre-install,pre-upgrade` hook Job that `kubectl apply --server-side`s the CRDs, the same pattern the repository already uses for CRD upgrades (`CRD_UPGRADE_IMAGES` in `images.env`). CRDs are never deleted on uninstall. Deleting them would cascade into the finalizers of any live fault.

### 19.4 Images and versions

| File                                                           | Change                                                                                          |
| -------------------------------------------------------------- | ----------------------------------------------------------------------------------------------- |
| `versions.env`                                                 | `KATES_CHAOS_VERSION`                                                                           |
| `images.env`                                                   | `KATES_CHAOS_IMAGES=(controller agent)`; Litmus arrays kept but loaded only for `engine=litmus` |
| `scripts/load-images-to-kind.sh`, `scripts/download-charts.sh` | Follow `images.env`. A default Kind setup stops pulling 15 Litmus images.                       |
| `scripts/gen-version-matrix.sh`, `scripts/check-versions.sh`   | Cover the new keys, and the gates stay green                                                    |

### 19.5 Kafka access for the controller

The `kafka-cluster` chart gains, behind `chaos.enabled` (true in `values-kind.yaml`):

- the `chaos.kates.io/enabled: "true"` annotation on the `Kafka` CR;
- a `KafkaUser` `kates-chaos` with `authentication: tls` and ACLs:
  - Describe and DescribeConfigs on `Cluster`;
  - Describe and DescribeConfigs on `Topic *`;
  - Write on `Topic kates-probe`;
  - Read and Describe on `Group kates-chaos-*`;
  - Describe on `Group *` (for lag);
  - Describe on `TransactionalId *` (for coordinator lookup);
- a `KafkaTopic` `kates-probe`: 3 partitions, RF 3, `min.insync.replicas: 2`.

The controller discovers the bootstrap address from `Kafka.status.listeners[?(@.name=="tls")].bootstrapServers`. It reads the client certificate from the Secret the User Operator creates (`kates-chaos`: `user.crt`, `user.key`) and the cluster CA from `<cluster>-cluster-ca-cert` (`ca.crt`). Its Role in each allowed namespace grants `get` on exactly those Secrets by `resourceNames`.

### 19.6 Upgrade path from 2.x

1. **3.0.0 with `engine: kates`.** Litmus CRDs, Roles, and experiments are no longer rendered. Existing `ChaosEngine`s must be deleted first; `helm upgrade` fails with instructions if any exist, via a `lookup` guard (skipped with `--dry-run`).
2. **3.0.0 with `engine: litmus`.** Identical to 2.2.1 behaviour. Supported for one minor release.
3. **3.1.0** removes the `litmus` path (M6).

The `artifacthub.io/changes` annotation carries an **ACTION REQUIRED** entry, following the chart's existing convention.

---

## 20. Rust implementation

### 20.1 Why Rust for this engine specifically

| Property                        | Why it matters here                                                                                                                                                                  |
| ------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| No garbage collector            | Timestamps are taken right after syscalls (P4). A GC pause between the syscall and `SystemTime::now()` would reintroduce the error this project removes.                             |
| Memory safety without a runtime | The agent runs as root with `CAP_SYS_ADMIN` on every Kubernetes node. Memory-safety bugs there are node compromises.                                                                 |
| Small static binaries           | Controller ≈ 15 MB and agent ≈ 12 MB, statically linked. Distroless controller image. Idle RSS within the G5 budget (controller ≤ 64 MiB, agent ≤ 24 MiB).                           |
| Mature Kubernetes stack         | `kube-rs` (CNCF) provides the watcher/reflector/controller runtime, finalizer helpers, CRD derivation, and server-side apply — the same building blocks as controller-runtime in Go. |
| Types for invariants            | Phases, journal states, and fault params are enums with exhaustive matching. An illegal transition does not compile or is a tested pure function (§20.5).                            |
| Direct Linux access             | `nix` and `rustix` give safe wrappers for `setns`, signals, `fallocate`, `statvfs`, and pidfds, with no cgo-style boundary.                                                          |

### 20.2 Workspace

```text
chaos/
├── Cargo.toml                  [workspace] · resolver = "3" · edition = "2024"
├── rust-toolchain.toml         pinned stable; bumped by Dependabot (cargo ecosystem)
├── deny.toml                   licenses (Apache-2.0-compatible), advisories, bans: openssl, native-tls
├── clippy.toml
├── crates/
│   ├── kates-chaos-api/        CRD types, params, enums, validation, conditions; bin `crdgen`
│   ├── kates-chaos-safety/     PURE: blast radius, policy, phase transitions, verdicts
│   ├── kates-chaos-kafka/      Kafka protocol client (§20.7): metadata, configs, quorum,
│   │                           coordinators, produce/fetch canary, lag
│   ├── kates-chaos-strimzi/    Strimzi v1 types (partial, serde-tolerant), topology model,
│   │                           peer and listener resolution
│   ├── kates-chaos-controller/  bin: reconcilers, API faults, journal, lease, checks, metrics
│   ├── kates-chaos-inject/     lib, Linux: netns executor, nft, tc, cgroup v2, signals,
│   │                           fill, stressors
│   ├── kates-chaos-agent/      bin: injection reconciler, CRI client, timers, journal, sweep,
│   │                           DNS responder, `stress` subcommand
│   └── kates-chaos-testkit/    fakes (FakeKafka, FakeKube via tower-test), topology
│                               fixtures captured from kind-panda, builders
├── xtask/                      cargo xtask crdgen | e2e | images | release
└── docker/
    ├── controller.Dockerfile
    └── agent.Dockerfile
```

Dependency direction (no cycles, no bin → bin):

```text
┌────────────────────────┐                        ┌───────────────────┐
│ kates-chaos-controller │ bin                    │ kates-chaos-agent │ bin
└────────┬────────┬──────┘                        └───────────┬───────┘
         │        └───────────────┐                           │
         ▼                        ▼                           ▼
┌───────────────────┐   ┌─────────────────────┐   ┌────────────────────┐
│ kates-chaos-kafka │   │ kates-chaos-strimzi │   │ kates-chaos-inject │ Linux primitives
└───────────────────┘   └─────────┬───────────┘   └───────────┬────────┘
                                  ▼                           │
                        ┌────────────────────┐                │
                        │ kates-chaos-safety │ pure: no I/O   │
                        └─────────┬──────────┘                │
                                  ▼                           ▼
┌──────────────────────────────────────────────────────────────────────────────┐
│ kates-chaos-api          CRD types, params, enums, validation, conditions    │
└──────────────────────────────────────────────────────────────────────────────┘

Arrows show direct dependencies between layers. The controller also depends on
safety and api directly; kates-chaos-kafka has no internal dependencies.
kates-chaos-testkit (dev-dependency only) ──► api, safety, strimzi, kafka
```

`kates-chaos-safety` has **no I/O dependencies**, not even `tokio` or `kube`. Everything that decides whether a fault may run is a pure function over data, and is exhaustively and property-tested.

### 20.3 API crate

```rust
// crates/kates-chaos-api/src/fault.rs
#[derive(CustomResource, Serialize, Deserialize, Clone, Debug, JsonSchema, PartialEq)]
#[kube(
    group = "chaos.kates.io", version = "v1alpha1", kind = "ChaosFault",
    namespaced, status = "ChaosFaultStatus", shortname = "cf", category = "kates-chaos",
    printcolumn = r#"{"name":"Type","type":"string","jsonPath":".spec.type"}"#,
    printcolumn = r#"{"name":"Cluster","type":"string","jsonPath":".spec.cluster"}"#,
    printcolumn = r#"{"name":"Phase","type":"string","jsonPath":".status.phase"}"#,
    printcolumn = r#"{"name":"Verdict","type":"string","jsonPath":".status.verdict"}"#,
    printcolumn = r#"{"name":"Injected","type":"date","jsonPath":".status.injectedAt"}"#
)]
#[serde(rename_all = "camelCase")]
pub struct ChaosFaultSpec {
    pub cluster: String,
    #[serde(flatten)]
    pub fault: FaultKind,                 // tagged union: `type` + `params`
    pub target: TargetSpec,
    #[serde(default)]
    pub timing: Timing,
    #[serde(default)]
    pub operator: OperatorSpec,
    #[serde(default)]
    pub safety: SafetySpec,
    #[serde(default)]
    pub checks: Vec<CheckSpec>,
    #[serde(default)]
    pub abort: bool,
    pub ttl_seconds_after_finished: Option<u32>,
}

#[derive(Serialize, Deserialize, Clone, Debug, JsonSchema, PartialEq)]
#[serde(tag = "type", content = "params", rename_all = "PascalCase")]
pub enum FaultKind {
    ContainerKill(ContainerKillParams),
    PodKill {},
    PodDelete(PodDeleteParams),
    ProcessPause(ProcessPauseParams),
    RollingRestart(RollingRestartParams),
    BrokerHoldDown(HoldDownParams),
    NodeDrain(NodeDrainParams),
    ZoneOutage(ZoneOutageParams),
    NetworkPartition(PartitionParams),
    NetworkLatency(LatencyParams),
    NetworkLoss(LossParams),
    NetworkBandwidth(BandwidthParams),
    DnsError(DnsParams),
    CpuStress(CpuParams),
    MemoryStress(MemoryParams),
    IoStress(IoParams),
    DiskFill(FillParams),
    DiskThrottle(ThrottleParams),
}

impl FaultKind {
    pub const fn class(&self) -> FaultClass { /* exhaustive match: Unavailable | Degraded */ }
    pub const fn actor(&self) -> Actor { /* Controller | Agent | Both */ }
    pub fn capabilities(&self) -> &'static [Capability] { /* e.g. [Netem, U32] */ }
}
```

- Durations use a `Duration` newtype that serialises as Kubernetes-style strings (`"30s"`, `"5m"`) with a JSON-schema `pattern`.
- Quantities (`5Gi`) use `k8s_openapi::apimachinery::pkg::api::resource::Quantity`, parsed into bytes by a checked helper.
- `crdgen` emits the CRDs with `CustomResourceExt::crd()`. It post-processes them to add `selectableFields` for `ChaosInjection.spec.nodeName` (if the derive does not support it directly) and `x-kubernetes-validations` (CEL) for the immutability rules. The output goes to `charts/kates-chaos/crds/`.
- An `insta` snapshot test pins the generated YAML. `cargo xtask crdgen --check` runs in CI.

### 20.4 Controller

**Process layout.** A single `tokio` multi-threaded runtime runs these tasks:

| Task     | Role                                                                                                                                                    |
| -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `leader` | Lease elector (`coordination.k8s.io/v1`, `leaseDuration` 15 s, renew 10 s, retry 2 s). Only the leader runs reconcilers; the standby keeps warm caches. |
| `faults` | `kube::runtime::Controller<ChaosFault>`                                                                                                                 |
| `policy` | `Controller<ChaosPolicy>`: aggregates agent reports, enforces freeze, sweeps orphan annotations and cordons                                             |
| `checks` | Per-fault check schedulers (tokio intervals), results fed back through a channel and a requeue                                                          |
| `kafka`  | Connection pool per Kafka cluster                                                                                                                       |
| `http`   | `/metrics`, `/healthz`, `/readyz` (axum)                                                                                                                |

**Fault controller wiring.**

```rust
// crates/kates-chaos-controller/src/faults/mod.rs
pub async fn run(ctx: Arc<Ctx>) {
    let faults     = Api::<ChaosFault>::all(ctx.kube.clone());
    let injections = Api::<ChaosInjection>::all(ctx.kube.clone());
    let pods       = Api::<Pod>::all(ctx.kube.clone());

    Controller::new(faults, watcher::Config::default())
        .owns(injections, watcher::Config::default())
        // A Kafka pod event (Ready flip, replacement) wakes every fault that targets it.
        .watches(pods,
                 watcher::Config::default().labels("strimzi.io/component-type=kafka"),
                 ctx.targets_index.mapper())
        .with_config(controller::Config::default().concurrency(8))
        .graceful_shutdown_on(shutdown_signal())
        .run(reconcile, error_policy, ctx)
        .for_each(|res| async move { if let Err(e) = res { tracing::warn!(error = %e, "reconcile") } })
        .await;
}

async fn reconcile(fault: Arc<ChaosFault>, ctx: Arc<Ctx>) -> Result<Action, Error> {
    let ns  = fault.namespace().ok_or(Error::NotNamespaced)?;
    let api = Api::<ChaosFault>::namespaced(ctx.kube.clone(), &ns);
    finalizer(&api, FINALIZER, fault, |event| async {
        match event {
            finalizer::Event::Apply(f)   => phases::advance(&f, &ctx).await,
            finalizer::Event::Cleanup(f) => phases::revert_and_release(&f, &ctx).await,
        }
    })
    .await
    .map_err(Error::from)
}

fn error_policy(_f: Arc<ChaosFault>, err: &Error, _ctx: Arc<Ctx>) -> Action {
    Action::requeue(err.backoff())   // 1s → 30s exponential, jittered; permanent errors → status + no requeue
}
```

**Phase handlers** (`phases/*.rs`) are async functions `fn(&ChaosFault, &Ctx) -> Result<Action>`. Each one:

1. reads the current phase;
2. computes the next state with `kates_chaos_safety::transition(phase, event)`;
3. performs I/O through the journal (§20.4.1);
4. writes status with SSA (`Patch::Apply`, field manager `kates-chaos-controller`, `force`);
5. returns a requeue: `Action::requeue(until_next_deadline)` for timers, `Action::await_change()` otherwise.

**API-level faults** implement one trait:

```rust
#[async_trait]
pub trait ApiFault: Send + Sync {
    /// Journal intents for this fault on these targets. No side effects.
    fn plan(&self, targets: &[Target], spec: &ChaosFaultSpec) -> Vec<Intent>;
    /// Perform one intent. Must be safe to call again after a crash.
    async fn apply(&self, cx: &FaultCx<'_>, intent: &Intent) -> Result<Applied, FaultError>;
    /// Undo one applied intent. Idempotent.
    async fn undo(&self, cx: &FaultCx<'_>, intent: &Intent) -> Result<(), FaultError>;
    /// When the fault's effect is over (for kill-type faults: replacement Ready).
    fn completion(&self) -> Completion;
}
```

Implementations: `PodKill`, `PodDelete`, `RollingRestartSequential`, `RollingRestartStrimzi`, `BrokerHoldDown{CordonPinned|PauseReconciliation|SchedulingGate}`, `NodeDrain`, and the API half of `ZoneOutage`.

#### 20.4.1 Journal

```rust
pub struct Journal<'a> { api: &'a Api<ChaosFault>, fault: &'a ChaosFault }

impl Journal<'_> {
    /// Persist intent (status patch with resourceVersion precondition), THEN run `act`,
    /// THEN persist the outcome. A crash anywhere leaves a replayable record.
    pub async fn record<F, Fut>(&self, intent: Intent, act: F) -> Result<Applied, Error>
    where F: FnOnce() -> Fut, Fut: Future<Output = Result<Applied, FaultError>> { … }

    pub async fn replay_undo(&self, faults: &FaultRegistry) -> Result<(), Error> { … } // reverse seq order
}
```

The journal lives in status because that is the only place co-located with the object, atomically versioned, and deleted with it. The status write before every mutation adds one API round trip (a few milliseconds), which is acceptable because `injectedAt` is sampled *after* the mutation, not before the journal write.

### 20.5 Pure safety crate

```rust
pub fn transition(from: Phase, ev: PhaseEvent) -> Result<Phase, IllegalTransition>;

pub fn blast_radius(
    topo: &Topology,            // nodes, roles, zones, voters
    parts: &[PartitionState],   // replicas, isr, leader, topic
    min_isr: &TopicMinIsr,      // effective min.insync.replicas per topic
    targets: &[TargetRef],
    class: FaultClass,
    safety: &SafetySpec,
) -> Result<BlastReport, BlastViolation>;

pub fn verdict(checks: &[CheckOutcome], revert: RevertOutcome, aborted: Option<AbortCause>) -> Verdict;
```

Tests:
- **exhaustive** over `Phase × PhaseEvent` (a table-driven test asserts the full transition matrix);
- **property-based** with `proptest`: random topologies (1–9 brokers, 1–5 voters, RF 1–5, rack spread on or off), random target sets. Invariants: never allows `|remainingReplicas| = 0`; never allows losing the voter majority; accepts a single broker loss when RF 3/minISR 2/rack-aware; is monotonic (adding a target never turns a rejection into acceptance);
- **fixture** tests over the captured `kind-panda` topology (3 brokers, 3 voters, 3 zones), including the D5 regression.

### 20.6 Agent

**Process layout.**

```text
main()
├── single-threaded prologue (before the tokio runtime starts)
│   ├── setns into the host cgroup namespace  cgroup paths read as the host sees them
│   ├── flock the hostPath journal directory  exactly one agent per node
│   └── parse flags, install signal handlers
├── tokio runtime (current_thread + blocking pool)
│   ├── injection reconciler                  Controller<ChaosInjection>, field selector spec.nodeName
│   ├── policy watcher                        freeze, maxInjectionLifetime
│   ├── timer wheel                           one sleep_until per injection expiresAt
│   ├── reporter                              capabilities, clock offset → ChaosPolicy.status.nodes (SSA)
│   └── http                                  /metrics, /healthz
└── privileged work never runs on tokio threads (see the netns executor)
```

**Finding the target process.**

```rust
pub struct TargetProc {
    pub pid: Pid,                 // container init (tini)
    pub jvm: Option<Pid>,         // child with comm == "java"
    pub pidfd: OwnedFd,           // pidfd_open: stable handle, immune to PID reuse
    pub netns: PathBuf,           // /proc/<pid>/ns/net
    pub cgroup: PathBuf,          // /sys/fs/cgroup/<path from /proc/<pid>/cgroup>
    pub root: PathBuf,            // /proc/<pid>/root
}

pub async fn locate(cri: &CriClient, inj: &ChaosInjection) -> Result<TargetProc, LocateError> {
    // 1. CRI ContainerStatus(verbose=true) → info["info"].pid          (containerd, CRI-O)
    // 2. fallback: scan /proc/*/cgroup for the container ID
    // 3. verify: cgroup path contains pod UID (underscored) and container ID; netns inode ≠ host
    // 4. pidfd_open(pid) so later signals cannot hit a recycled PID
}
```

The CRI client is generated with `tonic-build` from the vendored `k8s.io/cri-api` `runtime/v1` protobuf and talks over a Unix socket.

**The netns executor.** `setns` changes the *calling thread's* namespace. Running it on a tokio blocking-pool thread would leave that pooled thread in the pod's network namespace, where it could later run unrelated work. Every namespace operation therefore runs on a **fresh OS thread that exits afterwards**:

```rust
pub fn in_netns<T, F>(netns: &Path, f: F) -> Result<T, InjectError>
where
    T: Send + 'static,
    F: FnOnce() -> Result<T, InjectError> + Send + 'static,
{
    let ns = File::open(netns)?;
    std::thread::Builder::new()
        .name("kates-netns".into())
        .spawn(move || {
            nix::sched::setns(ns.as_fd(), CloneFlags::CLONE_NEWNET)?;
            f() // child processes spawned here (nft, tc) inherit this netns
        })?
        .join()
        .map_err(|_| InjectError::ExecutorPanicked)?
}
```

Sockets created inside `f` stay in the pod's netns after the thread exits. The DNS responder (§11.3.6) relies on this, and hands the sockets back to tokio via `from_std`.

**Injectors.**

```rust
pub trait Injector: Send + Sync {
    fn action(&self) -> Action;
    fn requires(&self) -> &'static [Capability];
    /// Apply and return everything needed to undo — enough for a fresh process with no memory.
    fn apply(&self, t: &TargetProc, p: &ActionParams, id: &InjectionId) -> Result<Artifacts, InjectError>;
    /// Idempotent; tolerates partially applied artifacts and vanished targets.
    fn revert(&self, a: &Artifacts) -> Result<(), InjectError>;
    /// Enumerate artifacts of this kind present for a target (for the startup sweep).
    fn discover(&self, t: &TargetProc) -> Result<Vec<Artifacts>, InjectError>;
}
```

| Module   | Implements                          | Mechanism notes                                                                                                                                            |
| -------- | ----------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `nft`    | Partition, DNS redirect             | Renders an nft script from a typed AST (never string-concatenated user input) and pipes it to `nft -f -` inside the netns. Sets for peers; atomic updates. |
| `tc`     | Latency, Loss, Bandwidth            | `prio` + `netem`/`htb` + `u32` filters; handles in `0x7a00–0x7aff`; `tc -json qdisc show` for discovery                                                    |
| `cgroup` | Pause, Throttle, stressor placement | `cgroup.freeze` + poll `cgroup.events`; `io.max` read-modify-write with journaled previous line; `cgroup.procs` writes                                     |
| `signal` | ContainerKill, Pause fallback       | `pidfd_send_signal`                                                                                                                                        |
| `fs`     | DiskFill, IoStress scratch          | `statvfs`, `fallocate`, fsid comparison, all paths under `/proc/<pid>/root`                                                                                |
| `stress` | CPU, Memory, IO                     | The `kates-chaos-agent stress` subcommand, moved into the target cgroup before it starts working, and reporting "running" over a pipe                      |
| `dns`    | DnsError                            | `hickory-server` request handler with glob rules; upstream forwarding to the target's own `resolv.conf` nameservers                                        |

**Local journal and timers.** Each injection gets one JSON file, written via temp file + `fsync` + `rename` + directory `fsync`. Timers are rebuilt from the files at start (§14.3). If the timer and a controller-initiated revert race, a per-injection `tokio::sync::Mutex` serialises them, and `revert` is idempotent anyway.

**Capabilities probe** (at start, every 10 min, cached per node):
- run `tc qdisc add … netem` / `del` in a throwaway netns created with `unshare(CLONE_NEWNET)` on an executor thread;
- `nft list ruleset`;
- check `cgroup.freeze` and `io.max` existence;
- test the `u32` and `flower` classifiers;
- test `ifb` with `ip link add … type ifb` followed by delete.

The result goes to `ChaosPolicy.status.nodes[].capabilities`.

### 20.7 Kafka client

**Decision: a small client over the pure-Rust `kafka-protocol` codec, not `rdkafka`.**

| Need                                                                 | `rdkafka` (librdkafka)             | `kafka-protocol` + own connection layer                  |
| -------------------------------------------------------------------- | ---------------------------------- | -------------------------------------------------------- |
| `DescribeQuorum` (API 55)                                            | not exposed                        | ✔                                                        |
| `ListPartitionReassignments` (API 46)                                | not exposed                        | ✔                                                        |
| `DescribeTopicPartitions` (API 75), `ConsumerGroupDescribe` (API 69) | partial or version-dependent       | ✔                                                        |
| Static musl binary                                                   | C build (cmake, SSL, zstd), fiddly | pure Rust + `rustls`                                     |
| Produce/Fetch canary                                                 | ✔                                  | Needs record-batch encode/decode (provided by the crate) |
| Connection management, retries, metadata refresh                     | ✔ built in                         | ~1–1.5 kLOC to write                                     |

The controller needs about fifteen request types, each a single request/response with no consumer-group membership. That makes the pure-Rust route smaller in total risk than binding a C library and still missing two critical APIs. Scope of `kates-chaos-kafka`:

- **Connection.** `tokio` TCP + `tokio-rustls` with a client certificate (mTLS on the `tls` listener). SASL/SCRAM is a v2 feature; mTLS avoids it entirely.
- **Protocol.** `ApiVersions` negotiation per connection; highest mutually supported version per request.
- **Bootstrap.** Metadata from bootstrap, then one connection per broker (keyed by node ID) and the controller-forwarding path for `DescribeQuorum` (brokers forward it to the active controller).
- **Requests.** `Metadata`, `DescribeTopicPartitions`, `DescribeConfigs`, `DescribeCluster`, `DescribeQuorum`, `ListPartitionReassignments`, `FindCoordinator`, `OffsetFetch`, `ListOffsets`, `DescribeGroups`, `ConsumerGroupDescribe`, `Produce` (acks −1/1), `Fetch` (single partition, from offset).
- **Timeouts.** Every call has a deadline. Errors are typed (`KafkaError::{Timeout, Auth, Broker(code), Io}`), and checks map every error to a failed evaluation (fail-closed).

Spike S2 (§25) validates this against Kafka 4.3.1. `rdkafka` stays the documented fallback for produce/fetch only.

### 20.8 Crates (selected)

| Purpose        | Crate                                                                                                          |
| -------------- | -------------------------------------------------------------------------------------------------------------- |
| Kubernetes     | `kube` (`runtime`, `derive`, `rustls-tls`, `ws`), `k8s-openapi` (feature pinned to the lowest supported minor) |
| Async          | `tokio`, `futures`, `tokio-util`                                                                               |
| Schema / serde | `schemars`, `serde`, `serde_json`, `serde_yaml`                                                                |
| Kafka          | `kafka-protocol`, `tokio-rustls`, `rustls-pemfile`                                                             |
| Linux          | `nix`, `rustix` (pidfd, statvfs, fallocate), `procfs`                                                          |
| CRI            | `tonic`, `prost`, `hyper-util` (Unix socket connector)                                                         |
| DNS            | `hickory-server`, `hickory-proto`                                                                              |
| HTTP           | `axum`                                                                                                         |
| Observability  | `tracing`, `tracing-subscriber` (JSON), `tracing-opentelemetry`, `opentelemetry-otlp`, `prometheus-client`     |
| CLI / config   | `clap` (derive, env)                                                                                           |
| Errors         | `thiserror` (libraries), `anyhow` (binaries' `main` only)                                                      |
| Tests          | `proptest`, `insta`, `tower-test`, `rstest`, `tempfile`                                                        |

### 20.9 Build, images, CI

- **Targets.** `x86_64-unknown-linux-musl` and `aarch64-unknown-linux-musl`, built with `cargo zigbuild` in CI. No QEMU for compilation; the existing buildx/QEMU pipeline assembles the multi-arch manifests.
- **Controller image.** `gcr.io/distroless/static-debian12:nonroot` plus the static binary.
- **Agent image.** `debian:trixie-slim` with `nftables` and `iproute2` (for `tc` and its netem distribution tables), plus the static binary, running as root. Package versions are pinned, and Trivy scans it through the existing `security.yml`.
- **Profile.** `release`: `lto = "thin"`, `codegen-units = 1`, `panic = "abort"` for the controller. The agent uses `panic = "unwind"` so a panic on one executor thread is contained and reported, not fatal.
- **Workflow `ci-chaos.yml`**, path-filtered on `chaos/**` and `charts/kates-chaos/**`:
  1. `cargo fmt --check`
  2. `cargo clippy --all-targets -- -D warnings`
  3. `cargo test --workspace` (unit, property, snapshot)
  4. `cargo deny check`
  5. `cargo xtask crdgen --check`
  6. injector integration tests (`sudo -E cargo test -p kates-chaos-inject --features privileged-tests`)
  7. image build (no push on PRs)
- **E2E.** A job in `integration.yml` on Kind (§23.4).
- **Lints.** `#![forbid(unsafe_code)]` in every crate except `kates-chaos-inject`, where each `unsafe` block carries a `// SAFETY:` comment and CI runs `cargo geiger` to report the count. `clippy::pedantic` is warn-level in `safety` and `api`.
- **Dependabot.** Add the `cargo` ecosystem for `/chaos`, grouped like the existing groups.

### 20.10 Conventions

- No panics on revert paths. Every error is recorded, and the next undo still runs.
- No `unwrap()` outside tests. Errors carry context (`thiserror` variants with the object and action).
- Every Kubernetes write uses SSA with an explicit field manager (`kates-chaos-controller` or `kates-chaos-agent`). Ownership of every field is visible in `managedFields`, which is how orphaned engine state is found (§14.4).
- Logs are structured JSON with `fault`, `injection`, `target`, `phase`, `trace_id`, and `span_id`.
- Every public function in `safety` and `api` is documented with the invariant it upholds.

---

## 21. Security

### 21.1 Threat model

| Threat                                                                    | Mitigation                                                                                                                                                                                              |
| ------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| A user with `create chaosfaults` in `kafka` disrupts things outside Kafka | Targets are restricted to pods with `strimzi.io/cluster=<spec.cluster>` in allowed namespaces; the agent re-checks; admission forbids user-created `ChaosInjection`                                     |
| A compromised controller directs the agent at arbitrary pods              | The agent independently checks the namespace allowlist (read from `ChaosPolicy`) and the Strimzi labels on the target pod, and refuses otherwise. `maxInjectionLifetime` caps any deadline.             |
| A compromised agent (root on the Kubernetes node)                         | Explicit capabilities, not `privileged`; no network listener beyond metrics; read-only root filesystem; minimal image; signed; RBAC limited to its own node's injections (field selector) and pod `get` |
| Tampering with the journal                                                | Journal status is writable only by the controller's field manager, enforced by the admission policy on `/status` updates for `ChaosFault`. The agent's journal lives under a root-only hostPath (0700). |
| Chaos left running                                                        | Deadlines at two layers, sweep, quarantine, kill switch                                                                                                                                                 |
| Kafka credential misuse                                                   | Dedicated `KafkaUser` with Describe-level ACLs plus one canary topic; certificate rotated by the User Operator                                                                                          |
| Supply chain                                                              | `cargo-deny` (advisories, licenses, bans), pinned toolchain, SBOM (syft), Trivy, signed images, Dependabot for cargo                                                                                    |

### 21.2 RBAC

**Controller** (ClusterRole, plus namespace Roles for Secrets):

| Resource                                                                              | Verbs                                     | Why                                             |
| ------------------------------------------------------------------------------------- | ----------------------------------------- | ----------------------------------------------- |
| `chaosfaults`, `chaosfaults/status`                                                   | get, list, watch, patch, update           | Reconcile                                       |
| `chaosinjections`, `chaosinjections/status`                                           | get, list, watch, create, delete, patch   | Dispatch                                        |
| `chaospolicies`, `chaospolicies/status`                                               | get, list, watch, patch                   | Policy                                          |
| `pods`                                                                                | get, list, watch, delete                  | Kill and delete faults                          |
| `pods/eviction`                                                                       | create                                    | Drain                                           |
| `nodes`                                                                               | get, list, watch, patch                   | Zone lookup, cordon                             |
| `kafkas.kafka.strimzi.io`                                                             | get, list, watch, patch                   | Pause annotation only (see below)               |
| `kafkanodepools`, `strimzipodsets`                                                    | get, list, watch; `strimzipodsets`: patch | Topology; manual-rolling-update annotation only |
| `kafkarebalances`                                                                     | get, list, watch                          | Preflight                                       |
| `poddisruptionbudgets`                                                                | get, list, watch                          | Drain reporting                                 |
| `persistentvolumeclaims`, `persistentvolumes`                                         | get                                       | Shared-filesystem detection                     |
| `deployments` (strimzi-operator ns)                                                   | get                                       | Operator health                                 |
| `leases` (kates-chaos ns)                                                             | get, create, update                       | Leader election and cluster Leases              |
| `events.k8s.io/events`                                                                | create, patch                             | Events                                          |
| `secrets` (per allowed ns, `resourceNames: [kates-chaos, <cluster>-cluster-ca-cert]`) | get                                       | Kafka mTLS                                      |

A `ValidatingAdmissionPolicy` bound to the controller's ServiceAccount restricts its `patch` on `kafkas` and `strimzipodsets` to changes of exactly the two allowed annotations (`object.spec == oldObject.spec` and a CEL check over annotation keys). This turns P2 from a convention into an enforced rule.

**Agent** (ClusterRole):

| Resource                                    | Verbs                                                                             |
| ------------------------------------------- | --------------------------------------------------------------------------------- |
| `chaosinjections`, `chaosinjections/status` | get, list, watch, patch (API-server field selector on its own node)               |
| `chaospolicies`                             | get, list, watch                                                                  |
| `chaospolicies/status`                      | patch (its own `nodes[]` entry, via SSA field manager `kates-chaos-agent-<node>`) |
| `pods`                                      | get                                                                               |

**Agent pod security context:**

```yaml
hostPID: true
hostNetwork: false
securityContext: { runAsUser: 0, seccompProfile: { type: RuntimeDefault } }
containers:
  - name: agent
    securityContext:
      privileged: false
      allowPrivilegeEscalation: false
      readOnlyRootFilesystem: true
      capabilities:
        drop: [ALL]
        add: [NET_ADMIN, SYS_ADMIN, SYS_PTRACE, KILL, DAC_OVERRIDE, SYS_RESOURCE]
    volumeMounts:
      - { name: cgroup,  mountPath: /sys/fs/cgroup }               # rw — freeze, io.max, cgroup.procs
      - { name: cri,     mountPath: /run/containerd/containerd.sock, readOnly: true }
      - { name: state,   mountPath: /var/lib/kates-chaos }
```

`SYS_ADMIN` is needed for `setns` into network and cgroup namespaces. `SYS_PTRACE` is needed for `/proc/<pid>/ns/*` and `/proc/<pid>/root` of other containers. If `RuntimeDefault` seccomp blocks `setns` on a given runtime, spike S4 decides between a custom seccomp profile (preferred, shipped in the chart) and `Unconfined`.

---

## 22. Observability

### 22.1 Metrics

| Metric                                       | Type      | Labels                                                   |
| -------------------------------------------- | --------- | -------------------------------------------------------- |
| `kates_chaos_faults_total`                   | counter   | `type`, `verdict`                                        |
| `kates_chaos_faults_active`                  | gauge     | `type`, `cluster`                                        |
| `kates_chaos_injection_latency_seconds`      | histogram | `type` — CR creation → `injectedAt`, the quantity D1 hid |
| `kates_chaos_revert_duration_seconds`        | histogram | `type`                                                   |
| `kates_chaos_revert_failures_total`          | counter   | `type`                                                   |
| `kates_chaos_checks_total`                   | counter   | `check`, `phase`, `result`                               |
| `kates_chaos_aborts_total`                   | counter   | `trigger`                                                |
| `kates_chaos_rejections_total`               | counter   | `reason`                                                 |
| `kates_chaos_quarantined`                    | gauge     | `cluster`                                                |
| `kates_chaos_controller_leader`              | gauge     | `pod`                                                    |
| `kates_chaos_agent_up`                       | gauge     | `node`                                                   |
| `kates_chaos_agent_capability`               | gauge     | `node`, `capability`                                     |
| `kates_chaos_clock_offset_seconds`           | gauge     | `node`                                                   |
| `kates_chaos_swept_artifacts_total`          | counter   | `node`, `kind`                                           |
| `kates_chaos_kafka_request_duration_seconds` | histogram | `api`                                                    |

### 22.2 Events

A `Normal` event for every phase transition and journal step. A `Warning` event for rejections, aborts, revert failures, and quarantine. Reason strings match `status.reason`.

### 22.3 Dashboards and alerts

- `dashboards/kates-chaos-infra/board.py` is rebuilt on `kates_chaos_*`, keeping uid `kates-chaos-overview`. Panels: active faults, injection latency, revert duration, aborts by trigger, agent health and capabilities, clock offset.
- A Grafana annotation query on `kates_chaos_faults_active` changes draws fault windows on every Kates board. The monitoring chart provisions it.
- `scripts/metric-contract/monitoring.yaml` is updated, so the metric-contract gate covers the new series.
- PrometheusRule alerts:
  - `KatesChaosRevertFailed` (critical)
  - `KatesChaosQuarantined` (critical)
  - `KatesChaosAgentDown` (warning, for 5 min)
  - `KatesChaosFaultPastDeadline` (critical)
  - `KatesChaosClockSkew` (> 50 ms for 10 min)

### 22.4 Traces

`KatesChaosProvider` writes the current OpenTelemetry context into the `kates.io/traceparent` annotation. The controller continues the trace:

- span `chaos.fault`, with attributes type, cluster, and targets;
- child spans `chaos.resolve`, `chaos.inject` (one per target), `chaos.check`, and `chaos.revert`.

The agent continues from the injection's annotation. The Jaeger trace of a disruption run then contains the injection itself, on the same timeline as the Kafka client spans.

---

## 23. Verification strategy

### 23.1 Layers

| Layer                  | Scope                                                                                                                                                                               | Runs                                                  |
| ---------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------- |
| Unit                   | Pure logic: phases, blast radius, verdicts, param validation, peer resolution, nft/tc script rendering (golden files)                                                               | `cargo test`, every PR                                |
| Property               | Blast-radius invariants, transition safety, journal replay ends with zero `Applied` entries for any crash point                                                                     | `cargo test`, every PR                                |
| Contract               | CRD snapshots; Java model generation compiles; `FaultSpec` → `ChaosFault` mapping round-trip                                                                                        | every PR (`ci-chaos.yml` + `ci.yml`)                  |
| Controller integration | `tower-test` mocked API server; FakeKafka; full reconcile sequences for every API fault, including crash-replay                                                                     | every PR                                              |
| Injector integration   | Root on a GitHub runner, inside throwaway netns and cgroups created by the test: apply → assert present → revert → assert absent, for every injector; sweep finds planted artifacts | every PR touching `inject`/`agent`                    |
| E2E                    | Kind with the repository's `config/cluster.yaml`, Strimzi, Kafka, the engine, and a canary client                                                                                   | `integration.yml`, PRs with the `e2e` label + nightly |
| Chaos-on-chaos         | Engine component failures during faults                                                                                                                                             | nightly                                               |
| Soak                   | 200 sequential random faults; zero leftovers; bounded memory                                                                                                                        | weekly                                                |
| Provider conformance   | Same plan on `litmus-crd` and `kates-chaos`: report shapes identical                                                                                                                | until M6                                              |

### 23.2 E2E assertions per fault

Every fault must satisfy **effect**, **timing**, and **clean revert**.

| Fault                                            | Effect assertion                                                                                                                 | Timing assertion                                           |
| ------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------- |
| `ContainerKill`                                  | Target `restartCount` +1; pod UID unchanged; leaders of its partitions moved within `broker.session.timeout.ms` + 5 s            | `injectedAt` ≤ first canary error on an affected partition |
| `PodKill` / `PodDelete`                          | Pod UID changed; `PodDelete` shows controlled shutdown (leaders moved *before* the container exited)                             | as above                                                   |
| `PodKill` + `leaderOf`                           | Target was leader of the partition at `resolvedAt`; a new leader was elected                                                     | leader change observed after `injectedAt`                  |
| `PodKill` + `activeController`                   | `DescribeQuorum` leader changed                                                                                                  | —                                                          |
| `ProcessPause`                                   | `cgroup.freeze=1` during; broker fenced after the session timeout; unfenced and back in ISR after revert                         | pause ≤ liveness budget, so no restart                     |
| `RollingRestart`                                 | Every target restarted exactly once, in order; URP returned to 0 between steps                                                   | steps ≥ `interval` apart                                   |
| `BrokerHoldDown`                                 | Pod absent or Pending for `duration` ± 2 s; back and in ISR after revert                                                         | —                                                          |
| `NodeDrain`                                      | Node unschedulable during; one Kafka eviction; `blockedByPDB` recorded for the second                                            | —                                                          |
| `NetworkPartition{Replication}` on a leader      | ISR shrinks to the leader after `replica.lag.time.max.ms`; `acks=all` canary fails with `NOT_ENOUGH_REPLICAS`; `acks=1` succeeds | —                                                          |
| `NetworkPartition{ControlPlane}` on a broker     | Broker fenced; leaders moved                                                                                                     | —                                                          |
| `NetworkLatency{100ms, Replication}` on a leader | `acks=all` canary p50 to its partitions rises by 100 ms ± 20 %; other partitions unchanged                                       | effect starts within 1 s of `injectedAt`                   |
| `NetworkLoss{20%}`                               | Canary retries > 0; no data loss (integrity verifier)                                                                            | —                                                          |
| `DnsError`                                       | New connections from the target to the matched names fail after the JVM cache TTL; existing ones continue                        | —                                                          |
| `CpuStress`                                      | Target `cpu.stat.nr_throttled` increases; p99 rises                                                                              | —                                                          |
| `MemoryStress{Reclaim}`                          | Target `memory.stat.file` drops; no OOM kill                                                                                     | —                                                          |
| `DiskFill{Budget}`                               | File of the budgeted size exists; node `nodefs.available` above the eviction threshold                                           | —                                                          |
| `DiskThrottle`                                   | `io.max` line set; produce latency rises                                                                                         | —                                                          |
| `ZoneOutage{Isolate}`                            | All Kafka pods in the zone isolated; cluster stays available (`OfflinePartitions == 0`)                                          | —                                                          |

**Clean revert (every fault):**
- no nft tables `kates_chaos_*`;
- no qdisc handles in `0x7a00–0x7aff`;
- `cgroup.freeze == 0`;
- `io.max` equal to its pre-fault value;
- no `.kates-chaos-*` files;
- no `kates-chaos-stress:*` processes;
- no engine-owned annotations;
- every Kubernetes node schedulable unless it was cordoned before;
- `ChaosInjection` objects gone;
- Kafka `URP == 0` within the `After` window.

### 23.3 Chaos-on-chaos

| Scenario                                                                              | Assertion                                                                                      |
| ------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------- |
| Kill the leader controller during `Active` (network fault)                            | The agent reverts at `expiresAt`. The standby replays the journal; the fault ends `Completed`. |
| Kill the agent during `Active`                                                        | The restarted agent reverts expired injections or re-arms live ones. No duplicate apply.       |
| Block agent egress to the API server during `Active` (NetworkPolicy on `kates-chaos`) | Revert at `expiresAt` anyway; status reconciled after unblock.                                 |
| Delete the target pod during `Active`                                                 | Injection `Reverted{TargetGone}`; fault completes.                                             |
| Force-remove finalizers                                                               | The `ChaosPolicy` sweep removes engine-owned annotations and cordons within 60 s.              |
| `ChaosPolicy.frozen=true` during five concurrent faults                               | All reverted within 5 s.                                                                       |

### 23.4 E2E environment

- Kind from `config/cluster.yaml`: three zones and the repository's images.
- Strimzi and Kafka from `charts/strimzi-operator` and `charts/kafka-cluster` with `chaos.enabled=true`.
- The engine from `charts/kates-chaos`.
- A small canary client Deployment built from `xtask` (reusing `kates-chaos-kafka`) that produces with `acks=all` and `acks=1` and consumes continuously, exporting per-partition latency and error counts.
- Tests are Rust (`kates-chaos-e2e` crate, run by `cargo xtask e2e`). Each test creates `ChaosFault`s through `kube` and asserts on Kubernetes state, Kafka state, canary metrics, and agent artifacts (inspected via `kubectl debug node/…` or the agent's `/debug/artifacts` endpoint, enabled only in e2e builds).

### 23.5 Performance budgets

Measured on Kind and enforced in the nightly run:

| Budget                                                    | Limit             |
| --------------------------------------------------------- | ----------------- |
| Injection latency (CR created → `injectedAt`), API faults | p95 ≤ 500 ms      |
| Injection latency, node faults                            | p95 ≤ 1 s         |
| Revert latency (deadline → `revertedAt`)                  | p95 ≤ 1 s         |
| Agent idle RSS / CPU                                      | ≤ 24 MiB / ≤ 5 m  |
| Controller idle RSS / CPU                                 | ≤ 64 MiB / ≤ 20 m |
| Check scheduling jitter                                   | ≤ 100 ms          |

### 23.6 Java-side tests

- `KatesChaosProviderTest`: fabric8 mock server; spec mapping; status → outcome; clock mapping; cleanup only deletes its own CR.
- `DisruptionOrchestratorTest`: disruption start re-based on the outcome; `AutoRollbackGuard` wired; `toBuilder` preserves all fields.
- `DisruptionSafetyGuardTest`: role classification on the 3 + 3 topology.
- `@QuarkusIntegrationTest` provider conformance (M1–M5).

### 23.7 Regression tests for the defect register

| Defect | Test                                                                                                                               |
| ------ | ---------------------------------------------------------------------------------------------------------------------------------- |
| D1, D2 | E2E: `\|injectedAt − first observed canary error\| ≤ 1 s`; `revertedAt` resolution below 1 ms                                      |
| D3     | E2E: `leaderOf` kills the leader in 20/20 runs                                                                                     |
| D4     | E2E: `RollingRestart` restarts every target; `BrokerHoldDown` holds for `duration`                                                 |
| D5     | Unit (safety) on the 3 + 3 fixture; Java `DisruptionSafetyGuardTest`                                                               |
| D6     | E2E: `CpuStress` throttles the target                                                                                              |
| D7     | Unit: every Kafka check fails on connection refused, TLS failure, and timeout                                                      |
| D8     | Assertion: no `exec` into targets during any default-check run (API audit log in Kind)                                             |
| D9     | E2E: `abortOn.underMinIsrPartitions` aborts a replication partition                                                                |
| D10    | E2E: two concurrent faults in one run; aborting one leaves the other `Active`                                                      |
| D11    | E2E: two runs against one cluster; the second waits in `Scheduled/ClusterBusy`                                                     |
| D12    | E2E: `az-failure` playbook isolates zone `alpha`                                                                                   |
| D13    | Java unit: leader re-targeting preserves probes and stress params                                                                  |
| D14    | E2E: `DiskFill{Percent: 80}` on local-path is converted to the byte budget, and node free space stays above the eviction threshold |

---

# Part III — Implementation plan

## 24. Plan overview

### 24.1 Streams

| Stream                  | Owner profile                   | Content                                                                               |
| ----------------------- | ------------------------------- | ------------------------------------------------------------------------------------- |
| **R — Rust controller** | Rust + Kubernetes controllers   | `api`, `safety`, `strimzi`, `kafka`, `controller` crates                              |
| **A — Rust agent**      | Rust + Linux networking/cgroups | `inject`, `agent` crates                                                              |
| **J — Java**            | Quarkus / fabric8               | `KatesChaosProvider`, model generation, orchestrator and safety fixes, templates      |
| **P — Platform**        | Helm, CI, Go CLI                | Chart 3.0.0, images, versions, CI workflows, `kates deploy`, CLI commands, dashboards |
| **D — Docs**            | any                             | Book chapters, `scripts/CHAOS_TESTS.md`, migration guide, chart README                |

### 24.2 Milestones at a glance

| M      | Name                             | Delivers                                                                                                                                                                                  | Depends on             | Effort (indicative) |
| ------ | -------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------- | ------------------- |
| **M0** | Foundations and spikes           | Seven spikes answered; `chaos/` workspace, CI, crdgen, image build skeleton                                                                                                               | —                      | 2 wk                |
| **M1** | Controller core + first faults   | CRDs, policy, lifecycle, journal, finalizers, Lease, deadlines; `PodKill`, `PodDelete` with Kubernetes selectors; `KatesChaosProvider`; Java-only defect fixes                            | M0                     | 4 wk                |
| **M2** | Kafka awareness                  | Kafka client; `leaderOf`/`activeController`/`coordinatorOf`; per-partition blast radius; checks; `abortOn`; `RollingRestart`, `BrokerHoldDown`, `NodeDrain`; `repeat`                     | M1                     | 4 wk                |
| **M3** | Agent + network + process faults | Agent runtime, CRI lookup, netns executor, journal, timers, sweep, capabilities; `ContainerKill`, `ProcessPause`, `NetworkPartition`, `NetworkLatency`, `NetworkLoss`, `NetworkBandwidth` | M1 (parallel with M2)  | 5 wk                |
| **M4** | Resource faults + DNS            | `CpuStress`, `MemoryStress`, `IoStress`, `DiskFill`, `DiskThrottle`, `DnsError`                                                                                                           | M3                     | 3 wk                |
| **M5** | Zone outage + cut-over           | `ZoneOutage`; default engine switch; CLI; dashboards; docs; chart 3.0.0 release                                                                                                           | M2, M4                 | 3 wk                |
| **M6** | Litmus removal                   | Delete Litmus code, config, images, chart path                                                                                                                                            | M5 + one minor release | 1 wk                |

**Critical path:** M0 → M1 → M3 → M4 → M5. M2 runs in parallel with M3 when two engineers are available (stream R on M2, stream A on M3).

| Staffing      | Total      |
| ------------- | ---------- |
| One engineer  | ≈ 22 weeks |
| Two engineers | ≈ 15 weeks |

The M1 exit alone fixes D3, D5, D9, D10, D11, D12, and D13 for Kates users, because the Java fixes ship there. That makes M1 worth delivering even if later milestones are re-planned.

### 24.3 Sequencing view (two engineers)

```text
week         1   2   3   4   5   6   7   8   9  10  11  12  13  14  15
            ───┬───┬───┬───┬───┬───┬───┬───┬───┬───┬───┬───┬───┬───┬───
M0  R+A     ███████
M1  R+J             ███████████████
M2  R                               ███████████████
M3  A                               ███████████████████
M4  A                                                   ███████████
M5  R+P+D                                                   ███████████
M6          one minor release after M5
```

---

## 25. Milestones and work packages

Each work package lists its **tasks**, **deliverables** (paths), **tests**, and **exit criteria**. A milestone is done when all of its work packages meet their exit criteria and the Definition of Done in §27.

### M0 — Foundations and spikes (2 weeks)

**WP0.1 Spikes.** Each spike produces a short written finding in `specs/chaos-spikes.md` (throwaway code lives on a scratch branch):

| Spike  | Question                                                                                                                                                                                                                                                                                    | Method                                                                            | Affects                     |
| ------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------- | --------------------------- |
| **S1** | Does the StrimziPodSet controller recreate a deleted pod while the parent `Kafka` has `strimzi.io/pause-reconciliation: "true"`? What does the operator do with manual-rolling-update on an unready pod?                                                                                    | Strimzi 1.2 on Kind: pause, delete pod, observe for 5 min; repeat with annotation | §11.2.6 strategy set, §12.3 |
| **S2** | Can a `kafka-protocol`-based client do mTLS, Metadata, DescribeQuorum (via broker forwarding), ListPartitionReassignments, DescribeConfigs, and Produce(acks=-1) against Kafka 4.3.1, and build as static musl for amd64 and arm64?                                                         | Prototype in `kates-chaos-kafka`; `cargo zigbuild`                                | §20.7 decision              |
| **S3** | In a broker pod's netns on Kind: does the thread-scoped `setns` executor work; do `prio`+`netem`+`u32` shape only matched traffic; does an nft drop without `ct established accept` stall existing Kafka connections; does a socket created in the netns serve DNS through an nft redirect? | Manual prototype on `kind-panda`                                                  | §11.3                       |
| **S4** | What is the minimal privilege set for the agent on containerd with `RuntimeDefault` seccomp: host cgroupns `setns`, CRI `ContainerStatus` PID lookup, `/proc/<pid>/root` access?                                                                                                            | DaemonSet prototype, capability bisection                                         | §21.2                       |
| **S5** | `cgroup.freeze` on a Strimzi broker: does kubelet's liveness restart happen at the predicted time; does SIGKILL reach frozen tasks; is `io` enabled in the parents' `cgroup.subtree_control` so `io.max` works?                                                                             | Manual on `kind-panda`                                                            | §11.2.4, §11.5.6            |
| **S6** | Shared filesystem detection and kubelet eviction thresholds: `f_fsid` comparison from the agent; reading `evictionHard` (node `configz` needs `nodes/proxy`, else a `ChaosPolicy` override)                                                                                                 | Prototype                                                                         | §11.5.4                     |
| **S7** | How does the Cluster Operator react when a broker is cut off from it (9091, 8443), and when a broker is killed mid-reconciliation?                                                                                                                                                          | Kind; operator logs; Kafka CR conditions                                          | §12.1, peer defaults        |

**WP0.2 Workspace and CI.**
- Tasks: create `chaos/` workspace with all crates as empty libs or bins; `rust-toolchain.toml`; `deny.toml`; `xtask` with `crdgen`; `ci-chaos.yml` (fmt, clippy, test, deny, crdgen check); Dockerfiles; Dependabot cargo entry; `.gitignore` for `chaos/target`.
- Deliverables: `chaos/**`, `.github/workflows/ci-chaos.yml`, `.github/dependabot.yml`.
- Exit: CI green on an empty workspace; images build for amd64 and arm64.

**WP0.3 Fixture capture.**
- Tasks: an `xtask` command that snapshots a live cluster's topology (Kafka CR, node pools, PodSets, pods, nodes, PDBs, Kafka metadata) into JSON fixtures; capture `kind-panda`.
- Deliverables: `chaos/crates/kates-chaos-testkit/fixtures/kind-panda/*.json`.
- Exit: fixtures load into the `strimzi` topology model.

**M0 exit.** All seven spikes documented with a decision. The architecture changes they force are applied to this spec in the same PR (§28.3).

### M1 — Controller core and first faults (4 weeks)

**WP1.1 API crate and CRDs.**
- Tasks: all types from §9; params for every fault type (so the schema is complete from day one, even for faults not yet implemented, which are rejected as `Unsupported`); CEL validations; `selectableFields`; printer columns; `crdgen`; snapshot tests.
- Deliverables: `crates/kates-chaos-api`, `charts/kates-chaos/crds/*.yaml`.
- Exit: `kubectl apply` of the CRDs on Kind; admission rejects every invalid example in `testkit/fixtures/invalid/`.

**WP1.2 Safety crate (phase machine, verdicts, policy).**
- Tasks: `transition`, `verdict`, policy evaluation (allowlist, opt-in, freeze, quarantine, concurrency cap); exhaustive transition test.
- Exit: 100 % branch coverage on `transition` and `verdict`.

**WP1.3 Strimzi topology.**
- Tasks: partial serde types for Strimzi `v1` resources (unknown fields tolerated); topology builder (node ID ↔ pool ↔ role ↔ pod ↔ Kubernetes node ↔ zone); Kubernetes selectors (`podNames`, `nodeIds`, `pool`, `zone`, `selector`); pinned-pod detection (required affinity + PV node affinity).
- Exit: fixture tests on `kind-panda`: brokers {0,1,2}, voters {3,4,5}, zones mapped, all pods pinned.

**WP1.4 Controller runtime.**
- Tasks: leader election; fault controller wiring; finalizer; phase handlers `Pending → … → Completed` for API faults; journal with replay; deadlines; cluster Lease; `ChaosPolicy` controller (freeze, quarantine, status); SSA everywhere; events; metrics; health endpoints.
- Tests: `tower-test` reconcile sequences, including crash-at-every-step replay for `PodKill`.
- Exit: on Kind, `PodKill` and `PodDelete` with `podNames`, `nodeIds`, `pool`, and `zone` selectors pass their e2e assertions; killing the controller at any point yields a completed or reverted fault.

**WP1.5 Faults `PodKill`, `PodDelete`.**
- Tasks: `ApiFault` implementations, replacement tracking, `repeat` without re-resolution.
- Exit: e2e rows in §23.2.

**WP1.6 Java: provider and model generation.**
- Tasks: `java-generator-maven-plugin` wiring; `KatesChaosProvider`; hybrid detection; `FaultSpec` extensions and `toBuilder()`; clock mapping; native-image reflection registration; fabric8 mock-server tests.
- Deliverables: `kates/src/main/java/com/bmscomp/kates/chaos/KatesChaosProvider.java`, `kates/pom.xml`, tests.
- Exit: `kates disruption run` of a POD_KILL plan on Kind produces a report whose `chaosStartTime` equals `status.injectedAt`.

**WP1.7 Java: engine-independent fixes.**
- Tasks: D3 (Litmus path honours `targetBrokerId`/`targetPod`), D5 (role classification), D9 (wire `AutoRollbackGuard`), D10 (cleanup by name), D12 (label selector parsing + playbook zone label), D13 (`toBuilder`), D1 re-basing in the orchestrator.
- Exit: Java unit tests for each; existing test suite green. These ship in their own PRs, independently of the engine (§26).

**WP1.8 Chart skeleton.**
- Tasks: `templates/engine/*` behind `engine: kates` (**not default**); RBAC; PSA namespace handling in `kates deploy --chaos-engine=kates`; CRD upgrade hook; helm tests.
- Exit: `helm install` on Kind with `engine=kates`; `helm test` green; `gen-chart-table.sh --check` green.

**M1 exit.**
- API faults run end-to-end from Kates.
- All the Java-only fixes are merged.
- The regression tests for D1 (API faults), D2, D5, D10, D11, D12, and D13 pass.

### M2 — Kafka awareness (4 weeks)

**WP2.1 Kafka client** (per the S2 outcome).
- Tasks: connection pool with mTLS, `ApiVersions`, the request set in §20.7, typed errors, deadlines; Secret loading and rotation (watch the Secret); bootstrap from `Kafka.status.listeners`.
- Tests: against a Kafka 4.3.1 container in CI (`testcontainers`-style via `xtask`), plus FakeKafka for unit tests.
- Exit: all requests pass against a real broker; the client survives a broker restart.

**WP2.2 Kafka selectors.** `leaderOf`, `replicasOf`, `activeController`, `quorumFollowers`, `coordinatorOf` (Group, Transaction, Share), with the reason recorded; `reResolve` on repeat.

**WP2.3 Blast radius (per partition and per quorum).** Wire `kates-chaos-safety::blast_radius` to live metadata and configs; `status.blastRadius`; dry-run annotation support; property tests.

**WP2.4 Checks engine.** Check scheduler; Kafka, Kubernetes, HTTP, PromQL, and Exec checks; phases; `expect`; `within`/`recoveredAt`; `checksPassedRatio`, `failStep`; `abortOn`.

**WP2.5 Preflight.** Every row of §15.3, including operator stability and reassignment detection.

**WP2.6 Faults `RollingRestart`, `BrokerHoldDown`, `NodeDrain`.** Per §11.2.5, §11.2.6 (strategies per S1), §11.4.1 (PDB behaviour).

**WP2.7 Java.** Checks mapping from `ProbeSpec`/`ProbeRegistry`; `abortOn` mapping from rollback properties; dry-run via the annotation; new templates (leader cascade, controller failover).

**M2 exit.**
- `LEADER_ELECTION` hits the leader in 20/20 runs (D3).
- Blast radius rejects "all brokers" and accepts "one broker" on the 3 + 3 topology (D5).
- `abortOn` aborts a fault (D9).
- Default checks fail closed (D7) and never `exec` into targets (D8).

### M3 — Agent, network and process faults (5 weeks)

**WP3.1 Agent runtime.** Prologue (cgroupns, flock); injection controller with field selector and label fallback; timers; local journal; status reporting; capabilities probe; `/metrics`; policy watch (freeze).

**WP3.2 Target location.** CRI client (containerd and CRI-O protobufs vendored); `/proc` fallback; identity verification; pidfd.

**WP3.3 Netns executor, nft and tc modules.** Typed script ASTs with golden tests; executor thread discipline; artifact recording; discovery for the sweep.

**WP3.4 Controller dispatch.** `ChaosInjection` creation with deterministic names; resolved peers (§11.3.1); peer-churn updates; `Injecting → Active` waits for all injections `Applied`; revert via deletion with finalizers.

**WP3.5 Faults.** `ContainerKill`, `ProcessPause` (freeze + fallback + liveness budget guard), `NetworkPartition`, `NetworkLatency`, `NetworkLoss`, `NetworkBandwidth`; direction via peer-side injections.

**WP3.6 Startup sweep and chaos-on-chaos.** Sweep for every artifact kind; the nightly chaos-on-chaos suite (§23.3).

**WP3.7 Privileged integration tests.** GitHub runner tests in throwaway netns and cgroups (§23.1).

**M3 exit.**
- Network and process faults meet §23.2.
- Chaos-on-chaos green for node faults.
- Asymmetric partition (broker ↔ controllers only) demonstrably fences the broker while clients still connect.
- D1 holds for node faults: `|injectedAt − effect| ≤ 1 s`.

### M4 — Resource faults and DNS (3 weeks)

- **WP4.1 Stressor subcommand.** CPU, memory (Reclaim/Oom), IO; cgroup placement; ready pipe; PID + start-time journaling; resource observations (`cpu.stat`, `memory.stat`, `io.stat`, PSI).
- **WP4.2 `DiskFill`.** Filesystem classification (S6); budget conversion; eviction guard; `restartIfOffline`.
- **WP4.3 `DiskThrottle`.** Device resolution; `io.max` read-modify-write with journaled previous value.
- **WP4.4 `DnsError`.** Socket-in-netns responder; glob rules; upstream forwarding; nft redirect.
- **WP4.5 Java.** Map `CPU_STRESS`, `MEMORY_STRESS`, `IO_STRESS`, `DISK_FILL`, `DNS_ERROR` params; templates (slow disk, zombie broker).

**M4 exit.**
- Every fault of §11.5 and §11.3.6 meets §23.2.
- D6 and D14 regression tests pass.

### M5 — Zone outage and cut-over (3 weeks)

- **WP5.1 `ZoneOutage`.** Isolate, Kill, and Freeze modes; zone-aware blast radius; `az-failure` playbook rewritten (D12).
- **WP5.2 Default switch.** Chart 3.0.0 with `engine: kates` default; `kates.chaos.provider=hybrid`; Litmus images behind `engine=litmus`; `kates deploy` defaults; upgrade guard for existing `ChaosEngine`s.
- **WP5.3 CLI.** `kates chaos faults|freeze|thaw|quarantine|nodes`; `deploy_components.go` engine selection; compat and style gates.
- **WP5.4 Observability.** Dashboard rebuild, fault-window annotations, alerts, metric contract.
- **WP5.5 Documentation.**
  - Book chapters on chaos engineering: new engine, fault catalog, safety model, Strimzi interplay.
  - `scripts/CHAOS_TESTS.md` and the `scripts/test-chaos-*.sh` ports.
  - `charts/kates-chaos/README.md`.
  - Migration guide `docs/chaos-migration.md`.
- **WP5.6 Release.** Versions, images, chart publication through the existing `release.yml` and `publish-charts.yml`.

**M5 exit.**
- A fresh `kates deploy -i` on Kind installs the engine and pulls zero Litmus images.
- Every row of Appendix A is green.
- The provider conformance suite passes.

### M6 — Litmus removal (1 week, one minor release after M5)

- Delete `LitmusChaosProvider`, `chaos/litmus/*`, `config/litmus/**`, the Litmus branch of `HybridChaosProvider`, `templates/litmus/*`, the `litmus-core` dependency, Litmus images and versions, `litmuschaos.io` RBAC in `charts/kates`, and Makefile `litmus*` targets.
- Update `gen-version-matrix.sh`, `load-images-to-kind.sh`, and the chart table.
- **Exit:** `grep -ri litmus` finds only the changelog and the migration guide.

---

## 26. Pull-request sequence

The repository's conventions apply to every PR:
- squash-merge;
- all checks green before merge, including non-required ones;
- diffs limited to intentionally changed files, with no repository-wide formatter runs;
- PR bodies in the house `## Why` style.

Titles follow the conventional-commit style already used on `main`.

| #   | Title                                                                                                 | Stream | Milestone |
| --- | ----------------------------------------------------------------------------------------------------- | ------ | --------- |
| 1   | `docs(specs): Kates chaos engine specification` (this document)                                       | D      | —         |
| 2   | `fix(disruption): classify KRaft controllers separately from brokers` (D5)                            | J      | M1        |
| 3   | `fix(disruption): preserve all FaultSpec fields when re-targeting the leader` (D13)                   | J      | M1        |
| 4   | `fix(chaos): clean up only the named engine` (D10)                                                    | J      | M1        |
| 5   | `fix(chaos): honour targetBrokerId on the Litmus path` (D3)                                           | J      | M1        |
| 6   | `fix(disruption): evaluate AutoRollbackGuard during observation` (D9)                                 | J      | M1        |
| 7   | `fix(playbooks): real label selectors and zone labels in az-failure` (D12, interim until ZONE_OUTAGE) | J      | M1        |
| 8   | `fix(disruption): re-base disruption start on the chaos outcome` (D1, all providers)                  | J      | M1        |
| 9   | `feat(chaos): scaffold the Rust workspace, CI and image builds`                                       | P/R    | M0        |
| 10  | `docs(specs): spike findings S1–S7` + spec amendments                                                 | D      | M0        |
| 11  | `feat(chaos): ChaosFault, ChaosInjection, ChaosPolicy APIs and CRD generation`                        | R      | M1        |
| 12  | `feat(chaos): pure safety core — phases, verdicts, policy`                                            | R      | M1        |
| 13  | `feat(chaos): Strimzi topology model and Kubernetes selectors`                                        | R      | M1        |
| 14  | `feat(chaos): controller runtime — leader election, finalizers, journal, deadlines, lease`            | R      | M1        |
| 15  | `feat(chaos): PodKill and PodDelete`                                                                  | R      | M1        |
| 16  | `feat(chart): kates-chaos engine templates behind engine=kates`                                       | P      | M1        |
| 17  | `feat(chaos): KatesChaosProvider and generated models`                                                | J      | M1        |
| 18  | `feat(chaos): Kafka protocol client`                                                                  | R      | M2        |
| 19  | `feat(chaos): Kafka-aware selectors`                                                                  | R      | M2        |
| 20  | `feat(chaos): per-partition and quorum blast radius`                                                  | R      | M2        |
| 21  | `feat(chaos): steady-state checks, preflight and abortOn`                                             | R      | M2        |
| 22  | `feat(chaos): RollingRestart, BrokerHoldDown, NodeDrain`                                              | R      | M2        |
| 23  | `feat(disruption): map probes and rollback thresholds to engine checks`                               | J      | M2        |
| 24  | `feat(chaos): node agent runtime and target location`                                                 | A      | M3        |
| 25  | `feat(chaos): netns executor, nftables and tc modules`                                                | A      | M3        |
| 26  | `feat(chaos): ContainerKill and ProcessPause`                                                         | A      | M3        |
| 27  | `feat(chaos): network faults with Kafka peer classes`                                                 | A/R    | M3        |
| 28  | `test(chaos): chaos-on-chaos and startup sweep`                                                       | A      | M3        |
| 29  | `feat(chaos): CPU, memory and IO stress`                                                              | A      | M4        |
| 30  | `feat(chaos): DiskFill and DiskThrottle with shared-filesystem safety`                                | A      | M4        |
| 31  | `feat(chaos): DnsError`                                                                               | A      | M4        |
| 32  | `feat(chaos): ZoneOutage and the az-failure playbook`                                                 | R/A/J  | M5        |
| 33  | `feat(cli): chaos engine commands and deploy default`                                                 | P      | M5        |
| 34  | `feat(dashboards): chaos engine board, fault annotations and alerts`                                  | P      | M5        |
| 35  | `docs(book): chaos engine chapters and migration guide`                                               | D      | M5        |
| 36  | `chore(release): kates-chaos 3.0.0 with engine=kates by default`                                      | P      | M5        |
| 37  | `chore(chaos): remove LitmusChaos`                                                                    | all    | M6        |

PRs 2–8 are independent of the engine and can merge first. They improve today's behaviour immediately and shrink the later diffs.

---

## 27. Definition of done

### 27.1 Per fault type

- [ ] Implemented in the engine, with params validated by schema and controller.
- [ ] Unit tests for planning and rendering; property tests where it touches blast radius.
- [ ] Injector integration test (node faults): apply, verify, revert, verify absent, discover.
- [ ] E2E: effect, timing, and clean-revert assertions (§23.2) on Kind.
- [ ] Chaos-on-chaos for node faults (§23.3).
- [ ] `DisruptionType` mapping and `FaultSpec` params in Java, with tests.
- [ ] At least one template or playbook uses it.
- [ ] Documented in the book (intent, Kafka behaviour, params, pitfalls), mirroring §11.
- [ ] Appendix A row green.

### 27.2 Per milestone

- [ ] Every work package's exit criterion met.
- [ ] Performance budgets (§23.5) hold for everything shipped so far.
- [ ] No open `RevertFailed` or leftover-artifact findings in the nightly run for 5 consecutive nights.
- [ ] This spec amended for any design change made during the milestone.

### 27.3 Per pull request

- [ ] All CI checks green, including non-required ones; `ci-chaos.yml`, `ci.yml`, lint, chart gates, version gates.
- [ ] Only intentionally changed files in the diff.
- [ ] Tests added for new behaviour; regression test added for any fixed defect.
- [ ] Docs and chart `artifacthub.io/changes` updated where user-visible.

---

## 28. Risks, open questions, decision log

### 28.1 Risks

| ID  | Risk                                                                                                      | Likelihood      | Impact | Mitigation                                                                                                                      |
| --- | --------------------------------------------------------------------------------------------------------- | --------------- | ------ | ------------------------------------------------------------------------------------------------------------------------------- |
| R1  | The agent is root on every Kubernetes node                                                                | certain         | high   | Capabilities not privileged; own namespace; re-validation; no listener; signed minimal image; opt-in install                    |
| R2  | Kernel feature variance (no IFB or flower on linuxkit; Bottlerocket/COS differences)                      | high            | medium | Per-node capability report; faults rejected up front as `Unsupported`; u32 everywhere                                           |
| R3  | Pure-Rust Kafka client effort exceeds estimate                                                            | medium          | medium | S2 spike; narrow request set; `rdkafka` fallback for produce/fetch                                                              |
| R4  | Strimzi behaviour changes between minor versions (PodSet controller, pause semantics, KafkaRoller checks) | medium          | medium | E2E pinned to the Strimzi version in `versions.env`; `chart-matrix` job extended to the engine; §12 behaviours covered by tests |
| R5  | A third language in the repository                                                                        | certain         | low    | The engine is a separate deployable behind a CRD contract; path-filtered CI; Java and Go contributors never need Rust           |
| R6  | Seccomp `RuntimeDefault` blocks `setns` on some runtimes                                                  | medium          | medium | S4; ship a custom seccomp profile in the chart                                                                                  |
| R7  | Shared-filesystem environments (local-path, hostPath) make disk and IO faults leak beyond the target      | certain on Kind | high   | Budget mode default, eviction guard, `sharedDevice` flag, explicit opt-in for full fills                                        |
| R8  | Operator instability confounds results (observed: 197 restarts in 47 h)                                   | observed        | medium | Operator-stability preflight; `externalRestarts` in reports; investigate the operator restarts separately                       |

### 28.2 Open questions

| ID  | Question                                                                                                  | Decided by          | Default until decided                                  |
| --- | --------------------------------------------------------------------------------------------------------- | ------------------- | ------------------------------------------------------ |
| Q1  | Does `PauseReconciliation` hold a pod down?                                                               | S1                  | `CordonPinned` only                                    |
| Q2  | Is `SchedulingGate` worth a `MutatingAdmissionPolicy` (beta in 1.34, may be off by default) or a webhook? | After S1, if needed | Not implemented                                        |
| Q3  | Should `RollingRestart` default to `Sequential` or `Strimzi`?                                             | Product             | `Sequential`                                           |
| Q4  | Should `requireOptIn` default to true on generic (non-Kind) installs?                                     | Product             | true                                                   |
| Q5  | Should the engine support SASL/SCRAM in v1 for clusters without a TLS listener?                           | Demand              | mTLS only                                              |
| Q6  | Should `ChaosFault` also be usable standalone (without Kates) as a public API?                            | Product             | Yes, it already is; documentation deferred to after M5 |

### 28.3 Decision log

| ADR    | Decision                                                                   | Rationale                                                          | Section       |
| ------ | -------------------------------------------------------------------------- | ------------------------------------------------------------------ | ------------- |
| ADR-1  | Kubernetes resources + controller instead of an RPC service                | Survives Kates crashes; RBAC; audit; multi-replica safety          | §8.3          |
| ADR-2  | `ChaosInjection` lives in the fault's namespace, with owner references     | Cross-namespace owner references are invalid; correct GC           | §8.4          |
| ADR-3  | Agent selects work with `selectableFields` on `spec.nodeName`              | Server-side filtering; label fallback for < 1.32                   | §8.4          |
| ADR-4  | Timestamps from the actor, mapped onto the JVM monotonic clock             | Removes D1/D2 at the source                                        | §17           |
| ADR-5  | Write-ahead journal in status + agent hostPath journal                     | Crash-safe revert with no external store                           | §14.1         |
| ADR-6  | Network faults inside the pod netns (nft + tc), not NetworkPolicy          | CNI-independent; established flows affected; peer-selective        | §11.3         |
| ADR-7  | u32 classifiers, egress-only shaping, peer-side injection for ingress      | `cls_flower` and `ifb` absent on the dev kernel                    | §5.1, §11.3.1 |
| ADR-8  | cgroup v2 freezer for `ProcessPause`                                       | Atomic and container-wide; SIGSTOP fallback                        | §11.2.4       |
| ADR-9  | Stressors join the target container's cgroup                               | Realistic contention under the broker's own limits; no orphans     | §11.5.1       |
| ADR-10 | Byte-budget disk fill on shared filesystems                                | Prevents host-wide damage (D14)                                    | §11.5.4       |
| ADR-11 | Pure-Rust Kafka client over `kafka-protocol` (pending S2)                  | Needs DescribeQuorum and ListPartitionReassignments; static builds | §20.7         |
| ADR-12 | Namespace work on fresh OS threads, never tokio threads                    | `setns` is per thread; pooled threads must not leak namespaces     | §20.6         |
| ADR-13 | Engine never edits Strimzi specs; two annotations only, admission-enforced | Cooperate with the operator (P2)                                   | §12.2         |
| ADR-14 | Fault types are mechanisms; Kafka semantics live in selectors              | Small catalog, rich combinations (P7)                              | §6, §10       |

---

## Appendix A — Parity matrix

| `DisruptionType`    | Litmus today                               | `kubernetes` provider today | Kates engine                                     | Milestone |
| ------------------- | ------------------------------------------ | --------------------------- | ------------------------------------------------ | --------- |
| `POD_KILL`          | ✔ random pod unless `targetPod`            | ✔ (controller-prone, D5)    | `PodKill`, role-aware                            | M1        |
| `POD_DELETE`        | ✔                                          | ✔                           | `PodDelete`                                      | M1        |
| `LEADER_ELECTION`   | ✖ random pod (D3)                          | ✔ via `targetBrokerId` (D5) | `PodKill` + `leaderOf`, re-resolved at injection | M2        |
| `ROLLING_RESTART`   | ✖ single delete (D4)                       | ✖ no-op on Strimzi (D4)     | `RollingRestart` Sequential / Strimzi            | M2        |
| `SCALE_DOWN`        | ✖ single delete (D4)                       | ✖ no-op on Strimzi (D4)     | `BrokerHoldDown`                                 | M2        |
| `NODE_DRAIN`        | ✔                                          | ✖                           | `NodeDrain`, PDB-aware                           | M2        |
| `NETWORK_PARTITION` | ✔                                          | ✔ CNI-dependent             | `NetworkPartition`, peer-selective, asymmetric   | M3        |
| `NETWORK_LATENCY`   | ✖ not installed                            | ✖                           | `NetworkLatency`, peer and port selective        | M3        |
| `CPU_STRESS`        | ✔                                          | ✖ rejected (D6)             | `CpuStress` in the target cgroup                 | M4        |
| `MEMORY_STRESS`     | ✔                                          | ✖                           | `MemoryStress`, page-cache aware, OOM-guarded    | M4        |
| `IO_STRESS`         | ✔ (param mis-mapped)                       | ✖ rejected (D6)             | `IoStress` on the data volume, bounded           | M4        |
| `DISK_FILL`         | ✖ not installed; unsafe on shared FS (D14) | ✖                           | `DiskFill` with budgets and eviction guard       | M4        |
| `DNS_ERROR`         | ✔ (hostnames via `targetTopic`)            | ✖                           | `DnsError`, typed hostnames                      | M4        |
| `CONTAINER_KILL`    | —                                          | —                           | `ContainerKill`                                  | M3        |
| `PROCESS_PAUSE`     | —                                          | —                           | `ProcessPause`                                   | M3        |
| `NETWORK_LOSS`      | —                                          | —                           | `NetworkLoss`                                    | M3        |
| `NETWORK_BANDWIDTH` | —                                          | —                           | `NetworkBandwidth`                               | M3        |
| `DISK_THROTTLE`     | —                                          | —                           | `DiskThrottle`                                   | M4        |
| `ZONE_OUTAGE`       | — (D12)                                    | —                           | `ZoneOutage`                                     | M5        |

---

## Appendix B — Example resources

### B.1 Leader cascade: kill whoever leads `orders-0`, every 20 s, for 2 minutes

```yaml
apiVersion: chaos.kates.io/v1alpha1
kind: ChaosFault
metadata: { name: leader-cascade-orders-0, namespace: kafka, labels: { kates.io/run-id: demo-1 } }
spec:
  cluster: krafter
  type: ContainerKill
  params: { signal: SIGKILL }
  target:
    leaderOf: { topic: orders, partition: 0 }
  timing:
    duration: 2m
    repeat: { every: 20s, reResolve: true }
  checks:
    - name: no-offline
      kafka: { check: OfflinePartitions, max: 0 }
      phase: Throughout
      interval: 2s
    - name: isr-restored
      kafka: { check: UnderReplicatedPartitions, max: 0 }
      phase: After
      within: 3m
```

### B.2 Controller failover

```yaml
spec:
  cluster: krafter
  type: PodKill
  target: { activeController: true, role: Controller }
  checks:
    - name: quorum-healthy-again
      kafka: { check: ControllerQuorum, maxVoterLag: 1000 }
      phase: After
      within: 60s
    - name: produce-keeps-working
      kafka: { check: ProduceAck, topic: kates-probe, acks: All, timeout: 5s }
      phase: During
      interval: 1s
      minPassRatio: 0.9
```

### B.3 ISR shrink: cut a leader from its followers only

```yaml
spec:
  cluster: krafter
  type: NetworkPartition
  target: { leaderOf: { topic: orders, partition: 0 } }
  timing: { duration: 90s }
  params: { peers: [Replication], direction: Both, action: Drop }
  safety: { allowUnderMinIsr: true, abortOn: { offlinePartitions: 0 } }
  checks:
    - name: acks-all-must-fail
      kafka: { check: ProduceAck, topic: orders, acks: All, timeout: 5s }
      phase: During
      interval: 5s
      expect: Fail
      startAfter: 40s          # after replica.lag.time.max.ms (30 s) + margin
```

### B.4 Zombie broker

```yaml
spec:
  cluster: krafter
  type: ProcessPause
  target: { nodeIds: [2] }
  timing: { duration: 20s }      # below the 30 s liveness budget
  checks:
    - name: leaders-moved
      kafka: { check: LeaderOf, topic: orders, partition: 0, notNodeIds: [2] }
      phase: During
      interval: 1s
      minPassRatio: 0.4          # true once the broker is fenced (~9 s)
```

### B.5 Zone isolation

```yaml
spec:
  cluster: krafter
  type: ZoneOutage
  target: { zone: alpha, role: Any, mode: All }
  params: { mode: Isolate }
  timing: { duration: 3m }
  operator: { mode: Pause }
  safety: { maxUnavailableZones: 1, abortOn: { offlinePartitions: 0, controllerQuorumLost: true } }
```

### B.6 Slow disk on one broker

```yaml
spec:
  cluster: krafter
  type: DiskThrottle
  target: { nodeIds: [1] }
  params: { writeBps: 5Mi, writeIops: 200 }
  timing: { duration: 5m }
```

### B.7 Disk fill on shared local-path storage (converted to a budget)

```yaml
spec:
  cluster: krafter
  type: DiskFill
  target: { nodeIds: [0] }
  params: { mode: Percent, percent: 80 }   # on kind-panda: Budget → 5Gi, status.message explains
  timing: { duration: 5m }
```

---

## Appendix C — Kafka and Strimzi settings that govern expected timings

| Setting                                                                    | Default           | Dev cluster                                        | Governs                                                                                                                                   |
| -------------------------------------------------------------------------- | ----------------- | -------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------- |
| `broker.session.timeout.ms`                                                | 9000              | default                                            | How long a silent broker stays unfenced: the leader-unavailability window after `ContainerKill`, `ProcessPause`, `ControlPlane` partition |
| `broker.heartbeat.interval.ms`                                             | 2000              | default                                            | Heartbeat cadence to the controller                                                                                                       |
| `controller.quorum.election.timeout.ms`                                    | 1000              | **5000**                                           | Time before a voter starts an election                                                                                                    |
| `controller.quorum.fetch.timeout.ms`                                       | 2000              | **10000**                                          | Time before followers suspect the quorum leader: active-controller kill, pause, partition                                                 |
| `controller.quorum.election.backoff.max.ms`                                | 1000              | **5000**                                           | Election retry backoff                                                                                                                    |
| `replica.lag.time.max.ms`                                                  | 30000             | default                                            | Time before a non-fetching follower leaves the ISR: replication partition, bandwidth, disk throttle                                       |
| `min.insync.replicas`                                                      | 1                 | **2**                                              | When `acks=all` produce fails; blast-radius rule                                                                                          |
| `unclean.leader.election.enable`                                           | false             | false                                              | Offline partitions stay offline rather than losing data                                                                                   |
| `auto.leader.rebalance.enable` / `leader.imbalance.check.interval.seconds` | true / 300        | default                                            | Second leadership movement after recovery                                                                                                 |
| `controlled.shutdown.enable`                                               | true              | default                                            | `PodDelete` moves leaders before stopping                                                                                                 |
| `request.timeout.ms` (clients)                                             | 30000             | client-side                                        | How long clients wait on a paused or partitioned broker                                                                                   |
| `delivery.timeout.ms` (producer)                                           | 120000            | client-side                                        | When a producer gives up on a record                                                                                                      |
| `session.timeout.ms` (classic consumer)                                    | 45000             | client-side, `group.min.session.timeout.ms=6000`   | Rebalance after a consumer's coordinator is lost                                                                                          |
| `networkaddress.cache.ttl` / `.negative.ttl` (JVM)                         | 30 s / 10 s       | default                                            | Delay before `DnsError` affects new connections                                                                                           |
| Kafka container liveness                                                   | —                 | exec, period 10 s, timeout 5 s, failureThreshold 3 | Budget before a paused broker is restarted by the kubelet                                                                                 |
| `terminationGracePeriodSeconds`                                            | 30                | 30                                                 | Controlled-shutdown budget for `PodDelete`                                                                                                |
| `STRIMZI_FULL_RECONCILIATION_INTERVAL_MS` (operator)                       | 120000            | default                                            | When `strimzi.io/manual-rolling-update` is acted on                                                                                       |
| PDB `krafter-kafka`                                                        | Strimzi-generated | `minAvailable: 5` of 6                             | Eviction-based faults: one Kafka pod at a time                                                                                            |
