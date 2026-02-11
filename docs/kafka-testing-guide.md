# Kafka Cluster Testing — Chaos Engineering and Load Testing Guide

This document covers how to stress-test the klster Kafka cluster using chaos engineering with LitmusChaos and examines load testing frameworks suitable for Kafka workloads. It is written for engineers who want to validate the resilience, performance, and fault-tolerance of the cluster.

## The Cluster Under Test

Before injecting any chaos, it helps to understand what we're trying to break.

**krafter** is a 3-broker Apache Kafka 4.1.1 cluster running in KRaft mode (no ZooKeeper) on a Kind Kubernetes environment. The Strimzi operator manages the full lifecycle.

### Topology

```
┌──────────────────────────────────────────────────────┐
│                   Kind Cluster "panda"                │
│                                                      │
│  ┌──────────────┐ ┌──────────────┐ ┌──────────────┐  │
│  │  Node: alpha  │ │  Node: sigma  │ │  Node: gamma  │ │
│  │  Control+Work │ │  Worker       │ │  Worker       │ │
│  │              │ │              │ │              │  │
│  │  pool-alpha-0│ │  pool-sigma-0│ │  pool-gamma-0│  │
│  │  Broker+Ctrl │ │  Broker+Ctrl │ │  Broker+Ctrl │  │
│  │  10Gi PVC    │ │  10Gi PVC    │ │  10Gi PVC    │  │
│  └──────────────┘ └──────────────┘ └──────────────┘  │
│                                                      │
│  Rack awareness: topology.kubernetes.io/zone         │
│  Each broker pinned to its zone via nodeAffinity     │
└──────────────────────────────────────────────────────┘
```

### Resilience Configuration

| Parameter | Value | Why It Matters |
|-----------|-------|----------------|
| `default.replication.factor` | 3 | Every topic partition exists on all 3 brokers |
| `min.insync.replicas` | 2 | Writes succeed even when 1 broker is down |
| `offsets.topic.replication.factor` | 3 | Consumer group metadata survives broker loss |
| `transaction.state.log.replication.factor` | 3 | Exactly-once semantics survive broker loss |
| `transaction.state.log.min.isr` | 2 | Transactions work with 1 broker down |
| KRaft mode | 3 controllers | No ZooKeeper SPOF; controller quorum built-in |
| Zone-pinned storage | local-path per zone | PVCs survive pod restarts within the same zone |

The cluster should tolerate the loss of exactly 1 broker without data loss or write unavailability. Losing 2 brokers simultaneously should cause write rejection but no data loss. These are the properties we want to verify through chaos testing.

### Monitoring Stack

Prometheus scrapes JMX metrics from each broker via a JMX Prometheus exporter sidecar. Grafana dashboards (auto-provisioned) provide visibility:

- Kafka Cluster Health — broker count, offline partitions, zone distribution
- Kafka Performance Metrics — topic throughput, partition growth
- Kafka JVM Metrics — heap, GC pressure, thread counts per zone
- Kafka Performance Test Results — throughput and latency from perf-test jobs

Access Grafana at `http://localhost:30080` (admin/admin) and Kafka UI at `http://localhost:30081`.

---

## Part 1: Chaos Engineering with LitmusChaos

### What Is Chaos Engineering?

Chaos engineering is the practice of deliberately introducing faults into a system to expose weaknesses before they cause production incidents. The core principle from Netflix's original Chaos Monkey paper is simple: if your system can't survive controlled failure in a test environment, it definitely can't survive uncontrolled failure in production.

For Kafka specifically, chaos engineering answers questions like:

- Does my cluster actually recover when a broker dies?
- What happens to in-flight messages during a network partition?
- How long does leader re-election take?
- Does my consumer group rebalance correctly after a broker restart?
- What is the impact of CPU starvation on message latency?

### Architecture: How LitmusChaos Works in klster

```
┌─────────────────────────────────────────────┐
│                litmus namespace              │
│                                             │
│  ┌─────────────┐     ┌──────────────────┐   │
│  │ Litmus       │     │ Chaos Operator   │   │
│  │ Portal       │────▶│ (watches for     │   │
│  │ (UI + API)   │     │  ChaosEngines)   │   │
│  └─────────────┘     └────────┬─────────┘   │
│                               │              │
│  ┌─────────────┐     ┌───────▼──────────┐   │
│  │ Subscriber   │     │ Workflow         │   │
│  │ (connects    │     │ Controller       │   │
│  │  infra to    │     │ (Argo-based)     │   │
│  │  portal)     │     └────────┬─────────┘   │
│  └─────────────┘              │              │
│                               │              │
│  ┌─────────────┐     ┌───────▼──────────┐   │
│  │ Event        │     │ Chaos Exporter   │   │
│  │ Tracker      │     │ (metrics →       │   │
│  │              │     │  Prometheus)     │   │
│  └─────────────┘     └──────────────────┘   │
└─────────────────────────────────────────────┘
                        │
                ┌───────▼──────────┐
                │  kafka namespace  │
                │                  │
                │  ChaosEngine     │
                │  → chaos-runner  │
                │  → experiment    │
                │  → probes        │
                └──────────────────┘
```

The chaos operator watches for `ChaosEngine` resources. When you apply one, it creates a chaos-runner pod that executes the experiment (pod-delete, network-latency, etc.) and runs the probes to validate system behavior during and after the fault.

### Setting Up the Chaos Environment

One command sets up everything via CLI (no UI required):

```bash
make chaos-kafka
```

This runs `setup-kafka-chaos.sh`, which:
1. Verifies the Kafka cluster and Litmus portal are running
2. Applies the infrastructure manifest (with offline-patched image names)
3. Installs `ChaosExperiment` CRDs (pod-delete, pod-network-partition, pod-cpu-hog)
4. Configures RBAC for the chaos runner in the `kafka` namespace

### Available Experiments

#### Broker Pod Deletion

**What it does:** Kills one Kafka broker pod to simulate a node crash or pod eviction.

**What we learn:** How fast leader re-election happens, whether in-flight messages are lost, how Strimzi's operator handles pod recreation.

```bash
make chaos-kafka-pod-delete
```

The experiment targets `krafter-pool-alpha-0` (the control-plane node broker) and runs for 30 seconds. A probe continuously checks the Kafka cluster CR status to verify the cluster reports itself as "Ready" even during the disruption.

**Expected behavior:** Strimzi recreates the pod. The remaining 2 brokers (σ, γ) continue serving reads and writes because `min.insync.replicas` = 2. Leader election for partitions whose leader was on α should complete in under 10 seconds. Consumer groups rebalance automatically.

**Config file:** `config/litmus-experiments/kafka-pod-delete.yaml`

---

#### Network Partition

**What it does:** Isolates one broker from the other two using Kubernetes NetworkPolicies, simulating a network split between availability zones.

**What we learn:** Whether the KRaft controller quorum handles split-brain correctly, how ISR shrinkage behaves, whether clients can still produce to remaining in-sync replicas.

```bash
make chaos-kafka-network-partition
```

The experiment isolates `krafter-pool-alpha-0` from both `pool-gamma-0` and `pool-sigma-0` for 60 seconds, blocking Kafka internal ports (9091–9093). Two probes run:
- `check-kafka-survives-partition`: continuous health check
- `check-leader-election`: verifies KRaft metadata leader exists

**Expected behavior:** The partitioned broker falls out of the ISR for all partitions it was replicating. The KRaft quorum (2 of 3 controllers still connected) maintains the metadata log. When the partition heals, the broker catches up from the ISR leader's log and rejoins.

**Config file:** `config/litmus-experiments/kafka-network-partition.yaml`

---

#### CPU Stress

**What it does:** Pegs the CPU to 80% load on 2 cores inside the Kafka container.

**What we learn:** The impact on message latency, GC pressure, request handler thread starvation, and whether the JVM degrades gracefully under CPU contention.

```bash
make chaos-kafka-cpu-stress
```

Targets `krafter-pool-gamma-0` for 60 seconds. Two probes:
- `check-kafka-health`: continuous cluster readiness check
- `check-producer-latency`: edge probe that runs a 100-message perf-test and measures average latency

**Expected behavior:** Producer latency should increase (possibly 2–5×) but messages should not be lost. GC pauses may increase, visible in the Kafka JVM Grafana dashboard. The broker should remain in the ISR if it can keep up with replication within the configured `replica.lag.time.max.ms`.

**Config file:** `config/litmus-experiments/kafka-cpu-stress.yaml`

---

#### Disk Fill

**What it does:** Fills 80% of the Kafka data directory (`/var/lib/kafka/data`) with junk data.

**What we learn:** How Kafka handles disk pressure, whether log retention policies kick in, and whether the broker gracefully rejects writes when approaching capacity.

The experiment targets `krafter-pool-sigma-0` for 120 seconds and runs probes checking cluster health and log directory integrity.

**Expected behavior:** Kafka should start rejecting produce requests once the log directory is full. The remaining 2 brokers should still accept writes for partitions whose leader can be moved. Topic data on the stressed broker should not become corrupt — it should be truncated or the broker should shut down cleanly.

**Config file:** `config/litmus-experiments/kafka-disk-fill.yaml`

---

#### Network Latency Injection

**What it does:** Adds 2000ms of latency to all network traffic on a broker pod.

**What we learn:** The cascading effect of a slow broker on the entire cluster. Whether the broker drops out of ISR due to replication lag, what the producer timeout behavior looks like, and how consumer group coordination handles delayed heartbeats.

**Expected behavior:** The affected broker should eventually be removed from the ISR. Producers with `acks=all` will experience timeouts proportional to the injected latency. Consumers connected to this broker may time out and trigger a rebalance.

**Config file:** `config/litmus-experiments/kafka-network-latency.yaml`

---

#### Consumer Group Disruption

**What it does:** Force-deletes 50% of consumer pods to simulate consumer crashes.

**What we learn:** How fast the consumer group rebalances, whether consumer lag spikes and recovers, whether exactly-once semantics are maintained during rebalance.

**Note:** This experiment requires consumer application pods to be deployed (target label: `app=kafka-consumer`). Modify `APP_LABEL` in the config to match your actual consumers.

**Config file:** `config/litmus-experiments/kafka-consumer-chaos.yaml`

### Advanced: Probes

Probes are assertions that run during chaos experiments to validate whether the system meets expectations. The project includes three reusable probe configurations:

#### ISR Health Probe

Monitors In-Sync Replica counts during chaos. Checks both under-replicated partitions (broker in ISR but falling behind) and unavailable partitions (no leader available).

- Threshold: fewer than 50 under-replicated partitions (expected during single-broker failure)
- Fail condition: more than 5 unavailable partitions

**Config file:** `config/litmus-experiments/isr-health-probe.yaml`

#### Producer Throughput Probe

Runs a quick 100-message perf-test to verify the cluster can still accept writes. Fails if throughput drops below 10 records/sec.

**Config file:** `config/litmus-experiments/producer-throughput-probe.yaml`

#### Consumer Latency Probe

Measures end-to-end latency (produce → consume round-trip). Injects 500ms of network latency and checks whether total E2E latency stays below 10 seconds.

**Config file:** `config/litmus-experiments/consumer-latency-probe.yaml`

### Orchestrated Chaos: The GameDay Workflow

For comprehensive resilience validation, the GameDay workflow runs an Argo-based sequence of experiments with health checks between each stage:

```
Pre-Chaos Health Check
    ↓
Network Latency Injection (60s)
    ↓
Recovery Wait (30s)
    ↓
Mid-Chaos Health Check
    ↓
CPU Stress Injection (60s)
    ↓
Recovery Wait (30s)
    ↓
Pod Delete (Broker Failure) (60s)
    ↓
Recovery Wait (60s)
    ↓
Post-Chaos Health Check
    ↓
Generate Report
```

Run it with:
```bash
kubectl apply -f config/litmus-experiments/kafka-gameday-workflow.yaml
kubectl get workflows -n kafka -w
```

The workflow automatically generates a report including final cluster status, broker pod states, chaos result verdicts, and recent events.

### Scheduled (Recurring) Chaos

A `CronWorkflow` is pre-configured to run weekly chaos (Sundays at 02:00 UTC). It is **suspended by default**. To enable:

```bash
kubectl patch cronworkflow kafka-chaos-scheduled -n kafka --type merge -p '{"spec":{"suspend":false}}'
```

The scheduled run includes pre-flight safety checks (cluster healthy, all brokers up, no active experiments), runs a pod-delete experiment, verifies recovery, and sends a notification (webhook placeholder).

### Monitoring Chaos Results

```bash
# Quick status
make chaos-kafka-status

# Detailed results
kubectl get chaosresults -n kafka
kubectl describe chaosresult <name> -n kafka

# Live logs from chaos runner
kubectl logs -f <runner-pod-name> -n kafka

# Chaos metrics in Prometheus
# Chaos exporter exposes metrics at :8080/metrics in the litmus namespace
```

---

## Part 2: Load Testing Frameworks for Kafka

Load testing validates whether the cluster can handle production-level throughput, message sizes, and concurrent producer/consumer counts. While chaos engineering asks "what breaks?", load testing asks "how much can it handle and where does it start to degrade?"

### Built-In: kafka-producer-perf-test and kafka-consumer-perf-test

The cluster already ships with a performance test suite (`make test`) that uses Kafka's built-in perf tools. It produces 1 million messages (1KB each) with `acks=all` across 3 partitions and then consumes them, measuring throughput and latency.

```bash
make test
```

This is the simplest starting point and measures raw cluster capacity without external dependencies.

**Strengths:**
- Zero dependencies — uses the Strimzi Kafka image already loaded in Kind
- Measures true cluster limits (no client overhead)
- Supports batch size, linger, compression tuning via producer props

**Limitations:**
- No configurable workload patterns (constant throughput only)
- No multi-producer/consumer coordination
- No built-in reporting or dashboards
- Single-threaded producer

### Framework Comparison

Here is a comparison of the most mature Kafka load testing frameworks, evaluated for use with the krafter cluster:

#### Apache Kafka's Built-In Tools

**Best for:** Quick baseline benchmarks, CI smoke tests

Already available via the Strimzi image. No external tooling needed. Provides raw throughput numbers (records/sec, MB/sec) and latency percentiles.

```bash
# Example: 1M messages, 1KB, max throughput, acks=all
kafka-producer-perf-test.sh \
  --topic benchmark \
  --num-records 1000000 \
  --record-size 1024 \
  --throughput -1 \
  --producer-props bootstrap.servers=krafter-kafka-bootstrap:9092 acks=all
```

#### OpenMessaging Benchmark (OMB)

**Best for:** Standardized cross-system benchmarks, conference-grade comparisons

[github.com/openmessaging/benchmark](https://github.com/openmessaging/benchmark)

A vendor-neutral benchmark framework maintained by the Linux Foundation. Supports Kafka, Pulsar, RabbitMQ, RocketMQ. Runs as a distributed set of workers and publishes structured JSON results.

**Key features:**
- YAML-driven workload definitions (message size, throughput target, producer/consumer ratio)
- End-to-end latency histograms (P50, P95, P99, P99.9)
- Multi-driver architecture — can compare Kafka against Pulsar with the same workload
- Distributed workers for scaling beyond single-node limits

**Considerations:**
- Requires deploying worker pods (JVM-based)
- The Kafka driver needs the bootstrap server configured
- Best deployed as Kubernetes Jobs targeting the kafka namespace

#### Trogdor (Apache Kafka's Internal Load Generator)

**Best for:** Advanced workload simulation, sustained throughput testing

Part of the Apache Kafka source tree (`tools/src/main/java/org/apache/kafka/trogdor/`). It's the tool Kafka committers use for internal performance validation.

**Key features:**
- Coordinator + Agent architecture (distributed by design)
- ProduceBench, ConsumeBench, RoundTripWorkload tasks
- Configurable message patterns, key distributions, and value generators
- Fault injection capabilities (network slowdown, disk slowdown)

**Considerations:**
- Requires building from Kafka source
- Less documentation than other tools
- More suited for Kafka-specific deep testing than general benchmarking

#### kcat (formerly kafkacat)

**Best for:** Ad-hoc testing, scripted pipelines, quick produce/consume

A lightweight command-line tool that acts as both a producer and consumer. Written in C using librdkafka, so it has very low overhead.

```bash
# Produce 100K messages from /dev/urandom
dd if=/dev/urandom bs=1024 count=100000 | kcat -P -b krafter-kafka-bootstrap:9092 -t stress-test

# Consume and count
kcat -C -b krafter-kafka-bootstrap:9092 -t stress-test -e | wc -l
```

**Key features:**
- Sub-millisecond overhead per message
- JSON output mode for scripting
- Metadata inspection (partitions, leaders, ISR)
- Supports Avro/Protobuf with schema registry

#### k6 with xk6-kafka Extension

**Best for:** Teams already using k6 for HTTP load testing, JavaScript-based scenarios

[github.com/mostafa/xk6-kafka](https://github.com/mostafa/xk6-kafka)

k6 is a modern load testing tool built in Go. The xk6-kafka extension adds native Kafka producer/consumer support. Test scenarios are written in JavaScript.

```javascript
import { Writer, produce } from 'k6/x/kafka';

const writer = new Writer({
  brokers: ['krafter-kafka-bootstrap.kafka.svc:9092'],
  topic: 'k6-test',
});

export default function () {
  produce(writer, [{
    key: 'key-' + __ITER,
    value: 'message-' + __ITER,
  }]);
}

export const options = {
  vus: 50,
  duration: '2m',
};
```

**Key features:**
- Familiar JavaScript syntax
- Built-in metrics (throughput, latency, error rate)
- Cloud execution option (k6 Cloud)
- Integrates with Grafana for dashboards

#### Strimzi Canary

**Best for:** Continuous health monitoring, SLA validation

[github.com/strimzi/strimzi-canary](https://github.com/strimzi/strimzi-canary)

A lightweight sidecar that continuously produces and consumes messages on a canary topic, exposing Prometheus metrics for end-to-end latency, produce failures, and consume lag.

**Key features:**
- Native Strimzi integration
- Prometheus metrics out of the box
- Topic-level granularity
- Designed for long-running baseline measurement, not peak load testing

### Recommendation Matrix

| Scenario | Recommended Tool | Why |
|----------|-----------------|-----|
| Quick baseline | `kafka-producer-perf-test` | Already available, zero setup |
| Sustained throughput | Trogdor or OMB | Distributed workers, structured results |
| Latency measurement | OMB or k6 | Percentile histograms, configurable load |
| Scripted CI tests | kcat + shell scripts | Lightweight, easy to containerize |
| Kafka vs. Pulsar comparison | OMB | Vendor-neutral, multi-driver |
| Continuous health | Strimzi Canary | Always-on, Prometheus-native |
| Chaos + load combined | `make test` + LitmusChaos | Both already deployed in klster |

---

## Part 3: Testing Scenarios for the krafter Cluster

Given the cluster's multi-AZ topology, KRaft mode, and replication factor 3, here are concrete testing scenarios ordered by increasing severity.

### Tier 1 — Baseline Validation

These tests verify the cluster works correctly under normal conditions.

#### 1.1 Throughput Baseline

```bash
make test
```

Run the built-in 1M-message test. Record the baseline throughput and latency numbers. These become the reference point for evaluating degradation during chaos.

**Metrics to capture:**
- Producer: records/sec, avg latency (ms), P99 latency
- Consumer: MB/sec, messages/sec

#### 1.2 Topic Partition Health

```bash
kubectl exec -n kafka krafter-pool-alpha-0 -- kafka-topics.sh \
  --describe --bootstrap-server localhost:9092 \
  --under-replicated-partitions

kubectl exec -n kafka krafter-pool-alpha-0 -- kafka-topics.sh \
  --describe --bootstrap-server localhost:9092 \
  --unavailable-partitions
```

Both commands should return empty results.

#### 1.3 Consumer Group Rebalance Time

Deploy a consumer group, then add/remove members:

```bash
# Produce background traffic
kubectl exec -n kafka krafter-pool-alpha-0 -- kafka-producer-perf-test.sh \
  --topic rebalance-test --num-records 1000000 --record-size 512 --throughput 1000 \
  --producer-props bootstrap.servers=localhost:9092

# Measure how long it takes a new consumer to join and start consuming
```

### Tier 2 — Single Fault Injection

Each test introduces exactly one fault and validates recovery.

#### 2.1 Broker Failure and Recovery

```bash
make chaos-kafka-pod-delete
```

**Observe:**
1. Leader re-election time (check Grafana "Kafka Cluster Health" dashboard)
2. Under-replicated partition count and duration
3. Whether any producer messages are lost (check chaos result verdict)
4. Time from pod deletion to full ISR restoration

**Pass criteria:** Cluster returns to full ISR within 5 minutes. No messages lost. Producers with `acks=all` experience at most a few seconds of increased latency.

#### 2.2 Zone Isolation (Network Partition)

```bash
make chaos-kafka-network-partition
```

**Observe:**
1. KRaft controller quorum behavior (2 of 3 controllers still connected)
2. ISR shrinkage on the isolated broker
3. Whether clients on the same node as the isolated broker can still produce
4. Time to re-join ISR after partition heals

**Pass criteria:** The remaining 2-broker quorum continues serving traffic. The isolated broker rejoins cleanly.

#### 2.3 Resource Exhaustion (CPU)

```bash
make chaos-kafka-cpu-stress
```

**Observe:**
1. JVM GC pressure (Grafana JVM dashboard)
2. Request handler thread saturation
3. Producer latency increase factor
4. Whether the broker drops out of ISR due to replication lag

**Pass criteria:** Latency increases but no messages are lost. The broker stays in ISR (if CPU stress doesn't exceed what the JVM can tolerate).

### Tier 3 — Multi-Fault and Compound Scenarios

These simulate realistic production incidents where multiple things go wrong simultaneously.

#### 3.1 GameDay: Cascading Failures

```bash
kubectl apply -f config/litmus-experiments/kafka-gameday-workflow.yaml
```

This runs network latency → recovery → CPU stress → recovery → pod delete → final health check. The goal is to verify the cluster can survive a sequence of different failures without cumulative degradation.

#### 3.2 Load Test Under Chaos

The most realistic scenario: sustained traffic while injecting faults.

```bash
# Terminal 1: Start sustained traffic
make test

# Terminal 2: While traffic is running, inject chaos
make chaos-kafka-pod-delete
```

**Observe:**
- Message loss count (produce success count vs. consume count)
- Latency spike duration and magnitude
- Whether the consumer group recovers and catches up
- Time from fault injection to throughput recovery

#### 3.3 Rolling Restart Under Load

Simulate a Kafka version upgrade:

```bash
# Start background traffic
kubectl exec -n kafka krafter-pool-alpha-0 -- kafka-producer-perf-test.sh \
  --topic rolling-test --num-records 5000000 --record-size 1024 --throughput 5000 \
  --producer-props bootstrap.servers=localhost:9092 acks=all &

# Trigger a rolling restart by modifying a Kafka config
kubectl annotate kafka krafter -n kafka strimzi.io/manual-rolling-update=true
```

**Pass criteria:** Zero message loss. Throughput dips briefly during each broker restart but recovers.

### Tier 4 — Destructive Testing (Recovery Validation)

These tests deliberately push the cluster into a degraded state and validate recovery procedures.

#### 4.1 Dual Broker Failure

```bash
kubectl delete pod krafter-pool-alpha-0 krafter-pool-sigma-0 -n kafka
```

With 2 of 3 brokers down, `min.insync.replicas` = 2 means writes should be rejected. The remaining broker should serve reads for partitions it leads.

**Observe:**
- Producer error: `NotEnoughReplicasException`
- Consumer: continues reading from partitions led by the surviving broker
- Recovery: when both pods restart, full ISR restoration time

#### 4.2 Persistent Volume Loss

```bash
kubectl delete pvc data-0-krafter-pool-gamma-0 -n kafka
kubectl delete pod krafter-pool-gamma-0 -n kafka
```

Simulates a storage failure. The broker should come up with an empty log directory and replicate all data from the other 2 brokers.

**Observe:**
- Time to fully replicate all partitions
- Whether the broker correctly registers as a follower for all partitions
- Total data transferred (watch network metrics in Grafana)

---

## Quick Reference

### Commands

| Command | Description |
|---------|-------------|
| `make chaos-kafka` | Set up the chaos environment (one-time) |
| `make chaos-kafka-pod-delete` | Kill a Kafka broker |
| `make chaos-kafka-network-partition` | Isolate a broker from the cluster |
| `make chaos-kafka-cpu-stress` | CPU stress on a broker |
| `make chaos-kafka-all` | Run all 3 experiments |
| `make chaos-kafka-status` | View chaos engines and results |
| `make test` | Run 1M-message performance test |
| `make chaos-ui` | Open Litmus UI (http://localhost:9091) |

### Monitoring

| Dashboard | Access | What to Watch |
|-----------|--------|---------------|
| Grafana | http://localhost:30080 | Kafka metrics, JVM, chaos impact |
| Kafka UI | http://localhost:30081 | Topics, partitions, consumer groups |
| Litmus UI | `make chaos-ui` → :9091 | Experiment status, environment, infra |

### File Map

| File | Purpose |
|------|---------|
| `setup-kafka-chaos.sh` | CLI chaos environment setup |
| `test-kafka-performance.sh` | 1M-message perf test |
| `config/kafka.yaml` | Kafka cluster definition |
| `config/litmus-experiments/*.yaml` | All chaos experiment definitions |
| `config/litmus/chaos-litmus-chaos-enable.yml` | Infrastructure registration manifest |
