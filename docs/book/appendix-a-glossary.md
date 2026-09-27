---
toc-depth: 2
---

# Glossary

This Glossary defines the terms the book uses, each written for the reader least likely to know it. When a chapter links a term, the link lands on its entry here, so you can read one entry and go back. To browse, pick a letter from the list below or from the page's table of contents.

## How to Read an Entry

Each entry opens with a one-sentence definition. Where Kates gives the term a meaning of its own, the next sentences say what it means in Kates: the field, flag or command behind it, and its value on `krafter`, the Kafka cluster under test. The entry ends with a link to the chapter that explains the concept in full. A few short entries only send you from another name to the one this book uses, such as AZ to Zone.

Terms sort by their letters, ignoring backticks and case, so `min.insync.replicas` files under M. Jump to a letter: [A](#a) · [B](#b) · [C](#c) · [D](#d) · [E](#e) · [F](#f) · [G](#g) · [H](#h) · [I](#i) · [J](#j) · [K](#k) · [L](#l) · [M](#m) · [N](#n) · [O](#o) · [P](#p) · [Q](#q) · [R](#r) · [S](#s) · [T](#t) · [U](#u) · [V](#v) · [W](#w) · [Z](#z).

## A

### `acks` {#gl-acks}

The producer setting that says which replicas must have a record before the leader acknowledges it: `0` (none), `1` (the leader) or `all` (every replica in the ISR). Every test spec has `acks`; it defaults to `all` for every test type except SPIKE, which defaults to `1`. See [The Cluster Under Test](03-cluster.md#replication-configuration).

### ACL {#gl-acl}

An access control list entry that allows or denies a principal an operation, such as Read or Write, on a Kafka resource like a topic, a consumer group or the cluster. `KafkaUser` resources carry them; `kates security acl-map` shows who can do what. See [Security & Compliance](17-security.md#acl-model).

### Admission Webhook {#gl-admission-webhook}

A Kubernetes extension point that sees each API request before the object is stored and can change or reject it; Kyverno runs as one. See [Security & Compliance](17-security.md).

### API Key {#gl-api-key}

The shared secret every REST and gRPC call to the Kates API must carry, as `Authorization: Bearer <key>` or `X-API-Key: <key>`, except REST `/api/health` and Quarkus's own `/q/` endpoints such as `/q/metrics`. The `kates` chart generates it into the `kates-api-key` Secret and keeps it across upgrades; a CLI context carries it. Whoever holds it can start load and faults. See [REST API Reference](11-api-reference.md#authentication).

### Apicurio Registry {#gl-apicurio-registry}

A schema registry that stores Avro, JSON Schema and Protobuf schemas for Kafka clients, so producers and consumers can evolve a record's shape safely. `make all` deploys it with `kates deploy --with-schema-registry apicurio`. See [Kafka Connect & CDC Pipelines](21-kafka-connect.md#schema-registry-integration).

### appVersion {#gl-appversion}

The field of a Helm chart's `Chart.yaml` that names the release of the software the chart deploys, as opposed to `version`, which numbers the chart itself. The `kates` chart deploys the Kates API image its `image.tag` names, which the chart keeps equal to its `appVersion`; its `version` changes whenever the chart does. See [Version & Compatibility Matrix](appendix-d-versions.md).

### Availability Zone {#gl-availability-zone}

The failure domain this book calls a [zone](#gl-zone).

### AZ {#gl-az}

Availability zone, which this book calls a [zone](#gl-zone).

## B

### Baseline {#gl-baseline}

A reference measurement that later runs are compared against to spot a regression. `kates test baseline set <run-id>` marks a stored run as the baseline for its test type on the Kates API, and `kates report regression <run-id>` compares a run against it; profiles and snapshots are local files instead. See [CLI Reference](10-cli-reference.md#test-baseline).

### Benchmark Backend {#gl-benchmark-backend}

The part of Kates that generates a test's load, chosen by the test's `backend` field: `native`, the default, or `trogdor`. `native` runs producers and consumers on virtual threads inside the Kates API; `trogdor` submits produce, consume and round-trip workload specs to a Trogdor coordinator and injects no faults. See [Test Types Deep Dive](05-test-types.md).

### Blast Radius {#gl-blast-radius}

How much of a system one fault can reach. In a disruption plan it's the set of distinct brokers the steps hit, which `maxAffectedBrokers` caps and which can never be every broker; rollback undoes only a `NETWORK_PARTITION` or a `SCALE_DOWN`. See [Chaos Engineering in Practice](07-chaos-practice.md#safety-guardrails).

### Broker {#gl-broker}

A Kafka server that stores partition replicas and serves produce and fetch requests. On `krafter` the brokers run in pods of their own, one broker pool per zone on `panda`, separate from the controllers. See [The Cluster Under Test](03-cluster.md#physical-topology).

## C

### CDC {#gl-cdc}

Change data capture: turning each insert, update and delete in a database into an event on a stream. See [Kafka Connect & CDC Pipelines](21-kafka-connect.md#change-data-capture-with-debezium).

### Change Data Capture {#gl-change-data-capture}

See [CDC](#gl-cdc).

### Chaos Backend {#gl-chaos-backend}

This book calls it the [chaos provider](#gl-chaos-provider).

### Chaos Experiment {#gl-chaos-experiment}

Chaos engineering's unit of work: faults injected on purpose to test a steady-state hypothesis. You run one as a disruption (a plan or playbook) or as a resilience run. `kates chaos list` lists disruptions under this name, and LitmusChaos calls its fault definitions `ChaosExperiment` resources. See [Chaos Engineering Theory](06-chaos-theory.md#core-principles).

### Chaos Provider {#gl-chaos-provider}

The component that carries out a fault, chosen by `kates.chaos.provider`: `litmus-crd` (the default), `kubernetes`, `hybrid` or `noop`. `litmus-crd` drives LitmusChaos, `kubernetes` calls the Kubernetes API itself (and needs the `kates` chart's `rbac.directChaos`), `hybrid` picks one of those two once and keeps it, and `noop` injects nothing; an unknown or unavailable provider falls back to `noop`. See [Chaos Engineering in Practice](07-chaos-practice.md).

### Clients CA {#gl-clients-ca}

The certificate authority Strimzi runs per Kafka cluster to sign the certificates of `KafkaUser`s that authenticate with TLS. See [Security & Compliance](17-security.md#certificate-management).

### Cluster CA {#gl-cluster-ca}

The certificate authority Strimzi runs per Kafka cluster to sign the brokers' and controllers' certificates, which clients using TLS must trust. See [Security & Compliance](17-security.md#certificate-management).

### ClusterPolicy {#gl-clusterpolicy}

A Kyverno resource that holds cluster-wide admission rules: mutate, validate, generate and verify images. See [Security & Compliance](17-security.md#cluster-policies).

### Committed Offset {#gl-committed-offset}

The [offset](#gl-offset) a consumer group has saved for a partition: where a restarted consumer resumes.

### Connector {#gl-connector}

A Kafka Connect plugin instance that streams data into Kafka from an external system (a source connector) or out of Kafka to one (a sink connector), split into up to `tasksMax` tasks. The `connect-cluster` chart declares connectors as `KafkaConnector` resources. See [Kafka Connect & CDC Pipelines](21-kafka-connect.md#connector-lifecycle).

### Consumer Group {#gl-consumer-group}

A set of consumers that share the reading of a topic: Kafka assigns each partition to one consumer in the group, so a group gains nothing from more consumers than partitions. LOAD and ENDURANCE consumers join the group `consumerGroup` names, and an INTEGRITY run's verifying consumer joins that name plus `-integrity`; a named group resumes from the offsets it committed before, so give a test a group of its own. See [The Cluster Under Test](03-cluster.md#consumer-groups-offsets-and-lag).

### Consumer Lag {#gl-consumer-lag}

How far a consumer group trails a partition: the latest offset in the log minus the offset the group has committed, summed over partitions for a topic or group. `kates cluster groups describe` and a disruption's lag tracking compute it exactly this way, from the committed offsets and each partition's latest offset. See [The Cluster Under Test](03-cluster.md#consumer-groups-offsets-and-lag).

### Context (CLI) {#gl-cli-context}

A named set of CLI settings — a Kates API URL, its API key, a proxy and an output format — kept in `~/.kates.yaml` and chosen with `kates ctx use`; it's unrelated to your `kubectl` context. See [CLI Reference](10-cli-reference.md#context-management).

### Controller {#gl-controller}

A KRaft node that holds the cluster's metadata — which brokers are live, which replica leads each partition — as a voter in the Raft quorum. `krafter` runs three controllers in pods of their own, separate from the brokers. See [The Cluster Under Test](03-cluster.md#the-kraft-quorum).

### Coordinated Omission {#gl-coordinated-omission}

A measurement bias: when the system stalls, a tool that waits for each response stalls with it and never sends the requests that would have waited, so the worst latencies go unrecorded. The native benchmark backend sends without waiting for acknowledgments and paces to the target rate, but it doesn't correct for the bias, so a stall that blocks the producer is under-recorded; cross-check with the heatmap. See [Performance Theory](04-performance-theory.md#coordinated-omission).

### Cosign {#gl-cosign}

A tool that signs container images and verifies their signatures; Kyverno uses Cosign public keys to admit only signed images. See [Security & Compliance](17-security.md#cosign-image-verification).

### CRC32 {#gl-crc32}

A 32-bit checksum that detects corrupted bytes. Each INTEGRITY record starts with a 28-byte header whose last four bytes are a CRC32 of the first 24, checked when the record is read back unless the spec sets `enableCrc: false`; a mismatch gives the CORRUPTION verdict. See [Data Integrity Verification](08-data-integrity.md#sequence-number-tracking).

### CRD {#gl-crd}

A CustomResourceDefinition: it adds a resource kind, such as Strimzi's `Kafka`, to the Kubernetes API, and deleting it deletes every resource of that kind. See [Deploying the Strimzi Operator](deploying-strimzi-operator.md#the-crd-lifecycle-gap).

### Cruise Control {#gl-cruise-control}

LinkedIn's tool, run by Strimzi, that moves partition replicas between brokers to balance their load when a `KafkaRebalance` asks it to. The `kafka-cluster` chart enables it by default, but the values `kates deploy` generates and the Kind overlay turn it off, so on `panda` existing partitions stay where they are. See [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md).

### Custom Resource {#gl-custom-resource}

An object of a kind a CRD adds to Kubernetes, such as a `Kafka` or a `KafkaTopic`, holding desired state that an operator makes real. See [Deploying the Strimzi Operator](deploying-strimzi-operator.md).

## D

### Dead Letter Queue {#gl-dead-letter-queue}

See [DLQ](#gl-dlq).

### Debezium {#gl-debezium}

An open-source CDC platform that runs as Kafka Connect source connectors and streams row-level changes from PostgreSQL (its write-ahead log), MySQL (its binlog) and MongoDB (change streams) into Kafka. See [Kafka Connect & CDC Pipelines](21-kafka-connect.md#change-data-capture-with-debezium).

### Disruption {#gl-disruption}

One run of a disruption plan or a playbook, with its own ID, status, report and timeline, and an SLA grade when the plan has an `sla` block. `kates disruption run`, `kates disruption playbook run` and `POST /api/disruptions` start one; `kates disruption status <id>` reads it. See [Chaos Engineering in Practice](07-chaos-practice.md#execution-lifecycle).

### Disruption Plan {#gl-disruption-plan}

A named, ordered list of steps, each a fault with its steady-state wait, observation window and recovery check, plus guardrails and an optional `sla` block. Run it with `kates disruption run --config <file>` or `POST /api/disruptions`; `?dryRun=true` or `--dry-run` previews it. A plan runs no workload: it watches the cluster through Prometheus and the pod watcher. See [Chaos Engineering in Practice](07-chaos-practice.md#playbook-yaml-structure).

### Disruption Rollback {#gl-disruption-rollback}

What Kates undoes after a failed disruption step while the plan's `autoRollback` is on: the NetworkPolicies it created for a `NETWORK_PARTITION`, or a `SCALE_DOWN`'s missing broker, and nothing for any other fault type. A step fails this way when its recovery check times out (`requireRecovery` on) or when it throws; ISR depth, lag and the SLA grade never trigger it. See [Chaos Engineering in Practice](07-chaos-practice.md#safety-guardrails).

### Disruption Type {#gl-disruption-type}

Which fault a step injects: one of the 13 values of `DisruptionType`, such as `POD_KILL`, `NETWORK_PARTITION` or `SCALE_DOWN`. `kates disruption types` lists them; which of them a fault can use depends on the chaos provider. See [Chaos Engineering in Practice](07-chaos-practice.md#disruption-types).

### DLQ {#gl-dlq}

Dead letter queue: a topic that receives the records a consumer or sink connector couldn't process, so one bad record doesn't stop the rest. Sink connectors can route failures to one. The platform profile also creates `kates-dlq`, which the Kates API polls every 30 seconds, logging whatever arrives. See [Kafka Connect & CDC Pipelines](21-kafka-connect.md#dead-letter-queue-dlq).

### Drain Cleaner {#gl-drain-cleaner}

A Strimzi webhook that intercepts the eviction of a Kafka pod during a node drain and has the operator roll the pod instead, keeping partitions in sync. The `strimzi-operator` chart deploys it only when `drainCleaner.enabled` is true: off by default, on (with two replicas) in `values-prod.yaml`. See [Kafka Deployment Engineering](15-kafka-deployment.md#strimzi-drain-cleaner).

## E

### emptyDir {#gl-emptydir}

A Kubernetes volume that starts empty on the pod's node and is deleted with the pod, used for writable scratch paths when the root filesystem is read-only. See [Deployment Guide](12-deployment.md#read-only-filesystem-compliance).

### Entity Operator {#gl-entity-operator}

The Strimzi pod that runs a Kafka cluster's Topic Operator and User Operator, which turn `KafkaTopic` and `KafkaUser` resources into topics, users and the users' Secrets. A client pod that mounts a user's Secret can't start until the `Kafka` resource is Ready, the Entity Operator is running and the User Operator has written that Secret. See [Kafka Deployment Engineering](15-kafka-deployment.md#entity-operator).

### Exactly-Once Semantics {#gl-exactly-once-semantics}

A guarantee that each record takes effect once despite retries and crashes; Kafka builds it from idempotent producers and transactions, and Kafka Connect adds exactly-once delivery for source connectors. See [Kafka Connect & CDC Pipelines](21-kafka-connect.md#exactly-once-semantics).

### Exit Code {#gl-exit-code}

The number a command returns to the shell, 0 for success, which a CI step reads to pass or fail. `kates test apply --wait` exits 1 when a scenario fails to submit, fails or violates a gate; `kates gate` exits 1 below `--min-grade`; `kates disruption run --fail-on-sla-breach` exits 1 when the SLA is violated. See [CLI Reference](10-cli-reference.md#exit-codes).

## F

### Fault {#gl-fault}

One injected failure: a disruption type aimed at the pods a label selector picks (or at the node or node pool that runs them), held for `chaosDurationSec`. A disruption plan's step carries one as `faultSpec`, a resilience run as `chaosSpec`; a chaos provider carries it out. See [Chaos Engineering in Practice](07-chaos-practice.md#targeting-pods).

### Fencing {#gl-fencing}

The KRaft controller's act of ceasing to count a broker as live once its session expires, so the broker loses the leadership of its partitions and leaves every ISR. The controller unfences it once its heartbeats get through again and it has caught up with the metadata log; a broker that restarts registers again first. See [The Cluster Under Test](03-cluster.md#the-kraft-quorum).

## G

### Game Day {#gl-game-day}

A planned session in which a team runs chaos experiments against a written hypothesis, with a rollback plan and a debrief. `make gameday` runs `scripts/gameday.sh`, an automated seven-phase version: pre-flight, baseline, chaos injection, observation, recovery validation, post-flight and report. See [Chaos Engineering Theory](06-chaos-theory.md#the-game-day-methodology).

### Gate {#gl-gate}

A pass/fail threshold in a scenario's `validate` block, such as `maxP99LatencyMs`, which `kates test apply --wait` checks after each run. A violated gate makes the command exit 1; without `--wait` no gate is checked. `kates gate` is a separate command, whose letter is a [performance grade](#gl-performance-grade). See [Scenario Files & SLA Gates](13-scenario-files.md#validation-reference-sla-gates).

### GraalVM Native Image {#gl-graalvm-native-image}

A Java application compiled ahead of time by GraalVM into a standalone binary, which starts without a JVM and uses GraalVM's own garbage collector (the Serial GC unless the build picks another). On a Kind cluster `kates deploy` runs the Kates API as a native image built from your checkout, and other clusters get the JVM image with generational ZGC. A local run's P99 and Max therefore include Serial GC pauses. See [Deployment Guide](12-deployment.md#native-image-build).

### Grade {#gl-grade}

Unqualified, the [SLA grade](#gl-sla-grade) of a disruption plan. Kates also gives a [performance grade](#gl-performance-grade) and a [security grade](#gl-security-grade).

### Grafana {#gl-grafana}

The dashboard server that draws Prometheus data as boards. The monitoring stack installs it; the Kates boards read the live meters of a run in flight. See [Observability & Monitoring](09-observability.md#the-dashboard-set).

### gRPC {#gl-grpc}

A remote procedure call protocol that carries Protocol Buffers messages over HTTP/2. The Kates API serves a gRPC API beside REST on the same port, 8080, covering fewer operations. See [gRPC API Reference](16-grpc-api.md).

### Guardrail {#gl-guardrail}

A limit on what a disruption plan may break, such as `maxAffectedBrokers`, which the [safety guard](#gl-safety-guard) enforces.

## H

### Heatmap {#gl-heatmap}

A chart of latency over time: one row per moment, one column per latency bucket, each cell the number of records in it. The Kates API adds a row each time it polls a test running on the native benchmark backend: every 5 s by default, and on every read of the run. Each row counts everything recorded so far, in 25 buckets whose last holds every latency from 7.5 s up. See [Observability & Monitoring](09-observability.md#latency-heatmaps).

### Helm Release {#gl-helm-release}

One installed instance of a Helm chart, with its own name, namespace and stored values. `kates deploy` installs `krafter` as the release `krafter` and `make kafka` as the release `kafka-cluster`; change either from its own `helm get values`, not with `--reuse-values`. See [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md).

### Helm Test {#gl-helm-test}

A pod a chart ships that `helm test` runs against an installed release to check that it works. The `kafka-cluster` chart ships a suite of them that produce, consume and check the cluster. See [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md#run-helm-tests).

### High Watermark {#gl-high-watermark}

The offset below which every replica in a partition's ISR has the records; consumers read only below it. See [The Cluster Under Test](03-cluster.md#replication-configuration).

## I

### Idempotent Producer {#gl-idempotent-producer}

A Kafka producer whose retried sends the broker writes only once, so a retry can't duplicate a record; the Kafka producer is idempotent by default whenever `acks` is `all`. `enableIdempotence` sets it explicitly; the Kates API refuses it, and `enableTransactions`, unless `acks` is `all`. See [Data Integrity Verification](08-data-integrity.md#integrity-modes).

### Isolated Topology {#gl-isolated-topology}

The default namespace layout of `kates deploy` (`--topology isolated`): Kafka, the Kates API, the monitoring stack and the chaos tooling each get a namespace of their own, `kafka`, `kates`, `monitoring` and `litmus` by default. The Strimzi operator goes to `strimzi-operator` in both topologies, so the operator's namespace is not what 'isolated' means; `make all` asks which topology you want. In this book 'topology' means only this namespace layout. See [Deployment Guide](12-deployment.md#single-namespace-vs-multi-namespace).

### ISR {#gl-isr}

The in-sync replicas: the replicas of a partition that are caught up with its leader, the leader included; an `acks=all` write waits for all of them. On `krafter` the ISR of a healthy partition is all three replicas; `min.insync.replicas` is the floor below which `acks=all` writes stop, not the ISR's size. See [The Cluster Under Test](03-cluster.md#replication-configuration).

### ISR Shrink {#gl-isr-shrink}

A partition's ISR losing a member because a follower fell behind or its broker failed, which leaves the partition under-replicated until it catches up. With `min.insync.replicas=2`, a shrink from three to two keeps `acks=all` writes flowing on one copy fewer, and a shrink to one stops them: durability erodes before availability. See [Chaos Engineering Theory](06-chaos-theory.md#isr-shrink-and-expand).

## J

### Jaeger {#gl-jaeger}

A distributed tracing system with its own UI. The Kates API exports trace spans to it over OTLP, to `jaeger-collector.monitoring.svc:4317` by default. See [Observability & Monitoring](09-observability.md#distributed-tracing).

### JMX {#gl-jmx}

Java Management Extensions: the JVM's standard interface for exposing metrics, which Kafka uses and the Prometheus JMX exporter reads. See [Observability & Monitoring](09-observability.md).

## K

### Kafka Connect {#gl-kafka-connect}

Kafka's framework for streaming data between Kafka and other systems through connectors run by a group of workers. The `connect-cluster` chart deploys it, managed by Strimzi. See [Kafka Connect & CDC Pipelines](21-kafka-connect.md).

### Kafka Exporter {#gl-kafka-exporter}

A Strimzi component that publishes consumer-group lag and topic offsets as Prometheus metrics, which the brokers' JMX metrics don't carry. The `kafka-cluster` chart enables it by default; the values `kates deploy` generates and the Kind overlay turn it off, so `panda` has none. See [Kafka Deployment Engineering](15-kafka-deployment.md#kafka-exporter).

### Kafka UI {#gl-kafka-ui}

A web UI (kafbat/kafka-ui) for browsing a Kafka cluster's topics, consumer groups and brokers. `kates deploy` installs it by default (`--with-kafka-ui`); `make ports` forwards it to `localhost:30081`. See [The Cluster Under Test](03-cluster.md#access-points).

### KafkaConnector {#gl-kafkaconnector}

The Strimzi resource that declares one connector — its class, `tasksMax` and configuration — for a Kafka Connect cluster Strimzi runs. See [Operating Kafka Connect](operating-kafka-connect.md).

### KafkaNodePool {#gl-kafkanodepool}

The Strimzi resource for a group of Kafka nodes with the same roles and settings, such as one broker pool per zone. On `panda` the generated values give one broker pool and one controller pool per zone. See [Kafka Deployment Engineering](15-kafka-deployment.md#kafkanodepool-configuration).

### KafkaRebalance {#gl-kafkarebalance}

The Strimzi resource that asks Cruise Control for a partition-reassignment proposal and carries it out once approved. See [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md).

### KafkaTopic {#gl-kafkatopic}

The Strimzi resource that declares a topic; the Topic Operator creates and updates the topic to match. The `kafka-cluster` chart renders one for each entry under `topics.items`. See [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md).

### KafkaUser {#gl-kafkauser}

The Strimzi resource that declares a Kafka user, its authentication, ACLs and quotas; the User Operator creates the credentials and writes them to a Secret. The `kafka-cluster` chart renders one for each entry under `users.items`. See [Kafka Deployment Engineering](15-kafka-deployment.md#kafkauser-management).

### Kates {#gl-kates}

The Kafka Advanced Testing & Engineering Suite, in two halves: the `kates` CLI on your machine and the Kates API in the cluster, which runs load tests and faults against a Kafka cluster; Helm charts deploy both. See [Introduction](01-introduction.md#what-is-kates).

### Kates API {#gl-kates-api}

The Kates server: the Quarkus application that serves the REST and gRPC APIs, runs benchmark backends and faults, and stores results. The `kates` chart deploys it as the `kates` Deployment and Service, serving REST and gRPC on port 8080, in the `kates` namespace under the isolated topology; `make ports` forwards it to `localhost:30083`. See [Architecture & Design](02-architecture.md#high-level-architecture).

### Kind {#gl-kind}

Kubernetes IN Docker: a tool that runs a Kubernetes cluster in Docker containers, one container per node. `make cluster` creates the `panda` cluster with it. See [Deployment Guide](12-deployment.md#kubernetes-cluster).

### KRaft {#gl-kraft}

Kafka's built-in Raft consensus, in which a quorum of controllers keeps the cluster's metadata in a replicated log instead of ZooKeeper. `krafter` runs three dedicated controllers, so its quorum survives one controller loss. See [The Cluster Under Test](03-cluster.md#the-kraft-quorum).

### `krafter` {#gl-krafter}

The Kafka cluster under test: the name the `kafka-cluster` chart and `kates deploy` give it by default, running on the `panda` Kind cluster in the book's lab. Its pods are named after it (`krafter-brokers-alpha-0`); the Kates API's chaos settings point at it (`kates.chaos.kafka.cluster=krafter`). See Before You Start in the [Preface](index.qmd).

### Kyverno {#gl-kyverno}

A Kubernetes policy engine that runs as an admission webhook and mutates, validates or generates resources as they are created. Optional: `kates deploy --with-kyverno` installs it (off by default), and `kates kyverno status` lists its ClusterPolicies, or says it isn't installed. See [Security & Compliance](17-security.md).

## L

### Lab (`kates lab`) {#gl-lab}

Kates's terminal UI for tuning by iteration: each iteration is a full test run with its own ID, and sweeps, warm-up runs and median mode each cost as many runs. See [Lab — Interactive Performance Tuning](10b-lab.md#iteration-workflow).

### Leader Election {#gl-leader-election}

The controller choosing a new leader for a partition, from its ISR, when the old leader's broker fails or is fenced. Until a producer learns the new leader, its writes to the partition fail and are retried; in a disruption plan, a `LEADER_ELECTION` step with `targetTopic` kills the pod of that partition's leader to force one. See [The Cluster Under Test](03-cluster.md#what-happens-during-a-broker-failure).

### Library Chart {#gl-library-chart}

A Helm chart of `type: library` that holds templates other charts include and deploys nothing on its own. `kafka-common` is one, shared by `kafka-cluster`, `connect-cluster` and `mirror-maker2`. See [Version & Compatibility Matrix](appendix-d-versions.md).

### Listener {#gl-listener}

A named port on which a broker accepts clients, with its own TLS and authentication settings. `krafter` always has `plain` (9092, SCRAM-SHA-512, no TLS) and `tls` (9093, mutual TLS); `external` (9094) exists only where the values declare it. 'plain' means no TLS, not no authentication. See [The Cluster Under Test](03-cluster.md#listeners).

### LitmusChaos {#gl-litmuschaos}

A Kubernetes-native chaos engineering framework whose `ChaosEngine` and `ChaosExperiment` resources define and run faults. The default chaos provider, `litmus-crd`, drives it; the `kates-chaos` chart deploys its execution plane. See [Chaos Engineering in Practice](07-chaos-practice.md#litmuschaos-integration).

### Log Truncation {#gl-log-truncation}

A replica deleting records past the point it shares with the new leader so its log matches, which loses records only when the new leader lacked them. See [The Cluster Under Test](03-cluster.md#replication-configuration).

### Lost Record {#gl-lost-record}

In an INTEGRITY run, an acknowledged record whose sequence number the verifying consumer never reads back; only acknowledged records count, because only they carry a durability promise. `Lost` is counted per sequence number, not as Acked minus Consumed; `Data Loss` is Lost as a percentage of Sent, and any lost record makes the verdict DATA_LOSS. See [Data Integrity Verification](08-data-integrity.md#sequence-number-tracking).

## M

### MCP {#gl-mcp}

The Model Context Protocol, through which an AI agent calls a server's tools and reads its resources. `kates mcp` serves Kates over stdio, read-only: it never starts load or faults, and it won't start without an explicit context and an `--allow-cluster` clusterId. See [CLI Reference](10-cli-reference.md#mcp-server-for-ai-agents).

### Metadata Log {#gl-metadata-log}

The replicated log in which KRaft controllers record every metadata change — topics, partition leaders, ISRs, broker registrations — and from which brokers learn them. See [The Cluster Under Test](03-cluster.md#the-kraft-quorum).

### Metadata Version {#gl-metadata-version}

The KRaft metadata format a cluster runs, which can be no newer than its Kafka version and is raised as a separate step after a Kafka upgrade. The `kafka-cluster` chart refuses a `kafka.metadataVersion` newer than `kafkaVersion`, and `kates deploy` refuses a Kafka version outside the operator's supported window. See [Upgrade Playbook](18-upgrade-playbook.md#kafka-version-upgrade).

### Metric Contract {#gl-metric-contract}

The rule that every series an alert or a dashboard panel reads is one the chart's exporter rules actually produce, which `scripts/check-metric-contract.sh` checks in CI. See [CI/CD Pipeline](appendix-c-cicd.md#the-render-matrix).

### `min.insync.replicas` {#gl-min-insync-replicas}

The smallest ISR a partition's leader accepts an `acks=all` write with: a floor, not a target, below which the leader refuses such writes rather than keep too few copies. 2 on `krafter` (3 replicas), so one broker can fail without stopping `acks=all` writes, and a second stops them. A test's `minInsyncReplicas` sets it when Kates creates the test topic; a topic that already exists keeps its own. See [The Cluster Under Test](03-cluster.md#replication-configuration).

### MirrorMaker 2 {#gl-mirrormaker-2}

Kafka Connect connectors that copy topics from a source cluster to a target cluster and translate consumer-group offsets between them. The `mirror-maker2` chart deploys it; `kates deploy --with-mirror-maker2` adds a loopback mirror of the primary cluster. See [Cross-Cluster Replication and Migration](22-mirror-maker2-migration.md#the-model).

### mTLS {#gl-mtls}

Mutual TLS: both client and server present certificates, so each authenticates the other. `krafter`'s `tls` listener, on port 9093, authenticates clients this way. See [Security & Compliance](17-security.md#mtls-mutual-tls).

## N

### NetworkPolicy {#gl-networkpolicy}

A Kubernetes resource that says which pods may connect to which; policies add up, so a connection that any policy selecting the pod allows gets through. On `krafter` two sets select the brokers, the `kafka-cluster` chart's and the one Strimzi generates, so a deny-all closes only what no other policy opens; Strimzi's admits every pod to a listener without `networkPolicyPeers`. See [Security & Compliance](17-security.md#network-policies).

### networkPolicyPeers {#gl-networkpolicypeers}

The field of a Strimzi listener that lists who may connect to it; Strimzi builds its generated policy's rule for the listener from it, and an empty or missing list admits every connection. No listener the charts ship sets it, and the `externalAccess` preset can't, so as shipped every pod reaches the client listeners; a listener you declare in `kafka.listeners` can carry it. See [Security & Compliance](17-security.md#closing-the-listeners).

### Node ID {#gl-node-id}

The number that identifies a Kafka node, broker or controller, in KRaft; Strimzi ends each Kafka pod's name with it. A pod is named `<cluster>-<pool>-<node ID>`, as in `krafter-brokers-alpha-0`, and a fault's `targetBrokerId` picks the broker pod whose name ends in `-<id>`, or the first broker when none does. See [The Cluster Under Test](03-cluster.md#physical-topology).

### Noise Floor {#gl-noise-floor}

The spread between repeated runs of the same test with nothing changed; a difference smaller than it is noise, not a regression. See [Performance Theory](04-performance-theory.md#statistical-significance).

## O

### Offset {#gl-offset}

A record's position in a partition, a number that only grows; a consumer group commits the offset it will read next, which is where a restarted consumer resumes. See [The Cluster Under Test](03-cluster.md#consumer-groups-offsets-and-lag).

### Offset Translation {#gl-offset-translation}

MirrorMaker 2 mapping a consumer group's committed source offsets to target offsets through checkpoints, so a group moved to the target resumes at or before its old position. See [Cross-Cluster Replication and Migration](22-mirror-maker2-migration.md#offset-translation-is-the-half-people-skip).

### Open-Loop Load {#gl-open-loop-load}

Load whose send schedule doesn't depend on responses, as opposed to closed-loop load, which sends each request only after the previous answer and so slows down with the system. The native benchmark backend is open-loop until its producer's buffer fills, when sends block and the loop closes again. See [Performance Theory](04-performance-theory.md#coordinated-omission).

### Operator {#gl-operator}

A Kubernetes controller that watches custom resources and keeps creating, changing and deleting objects until the cluster matches them; while it's down, changes to those resources wait. The Strimzi Cluster Operator is its own Helm release, `strimzi-operator`, in the `strimzi-operator` namespace. See [Deploying the Strimzi Operator](deploying-strimzi-operator.md).

### OTLP {#gl-otlp}

The OpenTelemetry Protocol, the wire format for exporting traces, metrics and logs. The Kates API exports trace spans over OTLP gRPC to Jaeger. See [Observability & Monitoring](09-observability.md#distributed-tracing).

## P

### Page Cache {#gl-page-cache}

The memory the operating system uses to keep recently read and written file data, so Kafka serves recent records without touching disk. A broker's page cache is roughly its pod memory minus its JVM heap; the `kafka-cluster` chart fixes the broker heap at 2048m. See [The Cluster Under Test](03-cluster.md#resource-budget).

### `panda` {#gl-panda}

The three-node Kind cluster the book's lab runs on, created by `make cluster` (or by `make all` when no cluster is reachable); its nodes `alpha`, `sigma` and `gamma` each stand for a zone. See Before You Start in the [Preface](index.qmd).

### Partition {#gl-partition}

One ordered, append-only log of a topic's records; a topic is split into partitions so several brokers and consumers can share it. See [The Cluster Under Test](03-cluster.md#topics-partitions-and-leaders).

### Partition Leader {#gl-partition-leader}

The replica of a partition that handles its produce and fetch requests; the other replicas follow it. In a disruption plan, a fault's `targetTopic` and `targetPartition` aim it at the broker leading that partition when the step starts; a resilience run ignores them. See [The Cluster Under Test](03-cluster.md#what-happens-during-a-broker-failure).

### PDB {#gl-pdb}

A PodDisruptionBudget: a Kubernetes limit on how many pods of a set may be down at once through voluntary disruptions such as a node drain. The `kafka-cluster` chart gives Kafka pods one with `maxUnavailable: 1`. See [Kafka Deployment Engineering](15-kafka-deployment.md).

### Percentile (P50, P95, P99, P99.9) {#gl-percentile}

The latency under which that share of requests finished: P99 = 15 ms means 99% took 15 ms or less. Kates reports the mean, P50, P95, P99, P99.9 and Max for every run. P99 of N records rests on the slowest N/100 of them, and Max on one record. See [Performance Theory](04-performance-theory.md#the-percentile-solution).

### Performance Grade {#gl-performance-grade}

The letter, A to F, that `kates gate` gives the test run it starts and waits for, from the run's average throughput and P99 against fixed thresholds that don't adapt to cluster size. `kates gate` needs, for example, at least 50,000 records per second with P99 under 5 ms for an A, and exits 1 below `--min-grade` (default C); `kates benchmark` grades a LOAD, STRESS, SPIKE battery from a score instead. It is not the [SLA grade](#gl-sla-grade). See [CLI Reference](10-cli-reference.md#gate).

### PersistentVolumeClaim {#gl-persistentvolumeclaim}

See [PVC](#gl-pvc).

### Plan {#gl-plan}

Short for [disruption plan](#gl-disruption-plan).

### Platform Profile {#gl-platform-profile}

The `kafka-cluster` chart's `profile: platform` preset, which adds the topics, users and network grants the Kates platform needs, such as `kates-events` and the `kates-backend` super user. `kates deploy`, `scripts/deploy-kafka*.sh` and `kates-platform` select it; it merges under the release's own values. See [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md#profiles).

### Playbook {#gl-playbook}

A disruption plan that ships inside Kates as YAML and runs by name; there are six: `leader-cascade`, `split-brain`, `az-failure`, `rolling-restart`, `consumer-isolation` and `storage-pressure`. `kates disruption playbook show` prints the plan one runs and `playbook run --dry-run` previews it. A playbook can't carry an `sla` block, so it gets no SLA grade. See [Chaos Engineering in Practice](07-chaos-practice.md#built-in-playbooks).

### PodDisruptionBudget {#gl-poddisruptionbudget}

See [PDB](#gl-pdb).

### PolicyException {#gl-policyexception}

A Kyverno resource that exempts named resources or namespaces from specific policy rules without disabling the policy. See [Security & Compliance](17-security.md#policy-exceptions).

### PolicyReport {#gl-policyreport}

A resource from the Policy Report API that stores the results of policy checks; `kates kyverno violations` reads them. See [Security & Compliance](17-security.md#kates-cli-kyverno-subcommands).

### PostgreSQL {#gl-postgresql}

An open-source relational database. The Kates API stores test runs, results, schedules and disruption reports in the PostgreSQL the `kates` chart deploys; Debezium's CDC example captures a separate one. See [Architecture & Design](02-architecture.md#data-model).

### productionMode {#gl-productionmode}

A switch in the `kafka-cluster` and `strimzi-operator` charts that adds render-time rails, so `helm install` or `upgrade` fails instead of deploying an unsafe setting. In `kafka-cluster` it refuses, among others, a NodePort listener without TLS, `networkPolicy.enabled: false` and `deleteClaim: true`; in `strimzi-operator` it refuses a single Drain Cleaner replica. `values-prod.yaml` turns it on. See [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md#production).

### Profile {#gl-profile}

In Kates, a test run's metrics saved under a name by `kates profile save`, as a JSON file under `~/.kates/profiles` on your machine, for `kates profile compare` and `kates profile assert`. See [CLI Reference](10-cli-reference.md#profile-commands).

### Prometheus {#gl-prometheus}

A time-series database that scrapes metrics from HTTP endpoints and answers PromQL queries; Grafana draws its data. It scrapes the brokers' JMX exporter and the Kates API's `/q/metrics`, which exports a run's live meters only while the run is in flight; finished results live in PostgreSQL. See [Observability & Monitoring](09-observability.md#observability-architecture).

### PrometheusRule {#gl-prometheusrule}

A Prometheus Operator resource holding alerting and recording rules. The `kafka-cluster` chart renders its alerts as one where that API exists, and each rule's `runbook_url` points at its entry in `docs/kafka-cluster-runbook.md`. See [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md#prometheusrule-alerts).

### Protobuf {#gl-protobuf}

Protocol Buffers: Google's language-neutral binary format for structured messages, which gRPC uses. `grpcurl` prints Kates's gRPC responses as proto3 JSON: fields at their zero value are left out, 64-bit integers are strings and names are camelCase. See [gRPC API Reference](16-grpc-api.md).

### Protocol Floor {#gl-protocol-floor}

The oldest broker a Kafka 4.x client can talk to: Kafka 4.0 removed the client protocol versions older than Kafka 2.1 (KIP-896), so a 4.x MirrorMaker 2 reads only from 2.1 or newer. See [Migrating a Legacy Kafka Source](23-legacy-source-migration.md#the-cliff-and-where-it-is).

### Purgatory {#gl-purgatory}

Where a broker parks a request it can't answer yet, such as an `acks=all` produce waiting for the ISR to have the record. See [Observability & Monitoring](09-observability.md#reading-the-dashboards-a-diagnostic-walkthrough).

### PVC {#gl-pvc}

A PersistentVolumeClaim: a pod's request for storage, bound to a PersistentVolume. Each `krafter` broker and controller has one, from a zone-specific StorageClass on `panda`. See [The Cluster Under Test](03-cluster.md#physical-topology).

## Q

### Quorum {#gl-quorum}

The majority of Raft voters that must agree before a metadata change commits: two of three controllers, so three controllers survive one loss. See [The Cluster Under Test](03-cluster.md#the-kraft-quorum).

### Quota {#gl-quota}

A broker-enforced cap on a client's byte rate or request time, measured over a time window; the broker enforces it by delaying responses, so a throttled client sees latency rather than errors. See [Multi-Tenancy](19-multi-tenancy.md#quota-strategy).

## R

### Rack {#gl-rack}

Strimzi's name for a [zone](#gl-zone); in this book `rack` names only the Strimzi field.

### readOnlyRootFilesystem {#gl-readonlyrootfilesystem}

A container security setting that mounts the root filesystem read-only, so the container can write only to volumes mounted for it. See [Security & Compliance](17-security.md#security-contexts).

### Rebalance {#gl-rebalance}

Three things in this book: a consumer-group rebalance reassigns a group's partitions when a member joins or leaves; a Kafka Connect rebalance reassigns tasks among workers; a Cruise Control rebalance moves partition replicas between brokers. The native benchmark backend's consumers use the classic group protocol. Under the eager protocol every member stops during a consumer-group rebalance; cooperative and KIP-848 rebalances pause only the partitions that move. See [Chaos Engineering Theory](06-chaos-theory.md#consumer-group-rebalance).

### Recipe {#gl-recipe}

An end-to-end workflow in Recipes & Patterns, such as validating a Kafka upgrade or running a nightly regression suite, from the first command to the result. See [Recipes & Patterns](14-recipes.md).

### Reconciliation {#gl-reconciliation}

An operator's loop of comparing a custom resource's desired state with what is running and changing the cluster to close the gap. See [Kafka Deployment Engineering](15-kafka-deployment.md#reconciliation-loop).

### Replication Factor {#gl-replication-factor}

See [RF](#gl-rf).

### Replication Slot {#gl-replication-slot}

A named PostgreSQL cursor through which a logical-decoding client such as Debezium reads the write-ahead log; the server keeps WAL until the slot's reader has consumed it. See [Kafka Connect & CDC Pipelines](21-kafka-connect.md#postgresql-cdc-pipeline).

### Request Handler Idle Ratio {#gl-request-handler-idle-ratio}

The share of time a broker's request-handler threads are free: 0.3 means they are busy 70% of the time. See [Observability & Monitoring](09-observability.md#reading-the-dashboards-a-diagnostic-walkthrough).

### Resilience Run {#gl-resilience-run}

A Kates test run with one fault injected after `steadyStateSec`, which reports the run's before-and-after impact and, for an INTEGRITY workload, lets the verifier measure RPO from the fault. Start one with `kates resilience run -f <file>` or `POST /api/resilience`, giving a `testRequest`, a `chaosSpec` and optional probes. Unlike a disruption plan, it sees what a client sees; it passes through no safety guard and has no rollback or SLA grade. See [Chaos Engineering in Practice](07-chaos-practice.md).

### Resilience Test {#gl-resilience-test}

The CLI help's and the REST API's name for a [resilience run](#gl-resilience-run).

### RF {#gl-rf}

Replication factor: how many copies of each partition the cluster keeps, one per broker. 3 on `krafter` (`default.replication.factor`); a test's `replicationFactor` sets it when Kates creates the test topic, and a topic that already exists keeps its own. See [The Cluster Under Test](03-cluster.md#replication-configuration).

### RPO {#gl-rpo}

Recovery point objective: how much recently written data a failure may lose, measured in time. Kates measures it rather than sets it. An INTEGRITY run inside a resilience run reports how long before the fault its oldest lost acknowledged record was sent: `0 ms` when none was lost, 'not measured' without a marked fault. A gate's `maxRpoMs` is the objective. See [Data Integrity Verification](08-data-integrity.md#integrity-under-chaos).

### RTO {#gl-rto}

Recovery time objective: how long recovery from a failure may take. Kates measures recovery three ways: the ISR whole again (`kates disruption kafka-metrics`), the broker pods ready again (what a plan's `maxRtoMs` grades), and an INTEGRITY run's longest send or read gap (Producer and Consumer RTO). The gate or threshold is the objective. See [Chaos Engineering Theory](06-chaos-theory.md#key-metrics-during-chaos).

## S

### Safety Guard {#gl-safety-guard}

The check Kates runs on a disruption plan before any fault: it refuses the plan when no broker pod matches, when the steps would hit more brokers than `maxAffectedBrokers`, or when no broker would be left untouched. Before each step's fault it also requires every Kafka pod, controllers included, to be Running and Ready. It never counts controllers and doesn't check under-replicated partitions or the KRaft quorum, and a resilience run doesn't pass through it. See [Chaos Engineering in Practice](07-chaos-practice.md#safety-guardrails).

### Saturation Point {#gl-saturation-point}

The load at which a stage of the produce path can't keep up, so requests queue, latency climbs steeply and throughput stops growing. See [Performance Theory](04-performance-theory.md#the-two-pillars-throughput-and-latency).

### Scenario File {#gl-scenario-file}

A YAML or JSON file with a `scenarios` list, each a test type, a `spec` and optional gates in `validate`, run with `kates test apply -f`. See [Scenario Files & SLA Gates](13-scenario-files.md#file-format).

### SCRAM-SHA-512 {#gl-scram-sha-512}

A salted challenge-response password mechanism for Kafka authentication. `krafter`'s `plain` listener (9092) and the `external` listener (9094) use it. See [Security & Compliance](17-security.md#scram-sha-512).

### Secret {#gl-secret}

A Kubernetes object holding credentials; a pod can use only Secrets in its own namespace. The User Operator writes each `KafkaUser`'s Secret into the Kafka cluster's namespace only, so a client elsewhere needs a copy, refreshed when the password rotates. See [Security & Compliance](17-security.md#cross-namespace-credential-synchronization).

### Security Grade {#gl-security-grade}

The letter, A to F, that `kates security audit` gives a cluster from how many of its security checks fail or warn; `kates security gate` exits non-zero below `--min-grade` (default B). See [CLI Reference](10-cli-reference.md#security-gate).

### Share Group {#gl-share-group}

A Kafka 4.x consumer grouping (KIP-932) in which members share partitions record by record, queue-style, instead of owning whole partitions. The Kates API exposes one over `/api/share-groups`. See [Kafka Deployment Engineering](15-kafka-deployment.md).

### Single-Namespace Topology {#gl-single-namespace-topology}

The `kates deploy --topology single` layout, which puts Kafka, the Kates API, the monitoring stack and the chaos tooling in one namespace, `kates-stack` unless `--namespace` names another. Option 1 in `make all`. The Strimzi operator still goes to `strimzi-operator`, and Kafka Connect's optional CDC database to `database`. See [Deployment Guide](12-deployment.md#single-namespace-vs-multi-namespace).

### SLA {#gl-sla}

Service level agreement, the word Kates uses for the pass/fail targets in a scenario's `validate` block, a disruption plan's `sla` block and `--fail-on-sla-breach`: SLO-style targets, not contracts. A test report without an SLA still shows its SLA section as passed, with no violations. See [Scenario Files & SLA Gates](13-scenario-files.md#validation-reference-sla-gates).

### SLA Grade {#gl-sla-grade}

The letter a disruption plan's `sla` block earns: A when every check passes, F on any critical miss, B, C or D by the share of failed checks, and `-` when nothing could be evaluated. A miss is critical when P99 or recovery time exceeds twice its limit, a step never recovers within that limit, or throughput falls below half its minimum; a plan without `sla`, and so every playbook, gets no grade. See [Chaos Engineering in Practice](07-chaos-practice.md#sla-grading).

### SLI {#gl-sli}

Service level indicator: the measured quantity an objective is set on, such as P99 latency or the share of successful requests. See [Observability & Monitoring](09-observability.md#alerting).

### SLO {#gl-slo}

Service level objective: a target for an SLI over a period, such as P99 under 100 ms for 99.9% of a month. Kates's 'SLA' thresholds are SLO-style targets for one run. See [Observability & Monitoring](09-observability.md#alerting).

### SMT {#gl-smt}

Single Message Transform: a small in-line change a connector applies to each record as it passes, chained in order. See [Kafka Connect & CDC Pipelines](21-kafka-connect.md#single-message-transforms-smts).

### Snapshot {#gl-snapshot}

In Kates, the cluster's brokers, topics and consumer groups saved by `kates snapshot create` as a JSON file under `~/.kates/snapshots`, for `kates snapshot diff`. See [CLI Reference](10-cli-reference.md#snapshot-commands).

### Sparkline {#gl-sparkline}

A small inline chart of a trend over time, drawn in the terminal with Unicode block characters. See [Observability & Monitoring](09-observability.md#cluster-watch).

### SSE {#gl-sse}

Server-Sent Events: a server streaming updates to a client over one long-lived HTTP response. `GET /api/disruptions/{id}/stream` streams a disruption's events this way. See [Chaos Engineering in Practice](07-chaos-practice.md#real-time-monitoring).

### Steady-State Hypothesis {#gl-steady-state-hypothesis}

A statement, in numbers you can measure, of what a system should show before, during and after a fault, such as a P99 bound and zero lost records: the claim a chaos experiment tests. See [Chaos Engineering Theory](06-chaos-theory.md).

### Strimzi {#gl-strimzi}

A Kubernetes operator that runs Apache Kafka from custom resources such as `Kafka`, `KafkaNodePool`, `KafkaTopic` and `KafkaUser`. Its Cluster Operator is its own Helm release, `strimzi-operator`, installed and upgraded separately from the Kafka clusters it manages. See [Deploying the Strimzi Operator](deploying-strimzi-operator.md#upgrading-the-operator).

### StrimziPodSet {#gl-strimzipodset}

The resource Strimzi runs Kafka pods from instead of a StatefulSet, which lets the operator manage each pod individually. See [Kafka Deployment Engineering](15-kafka-deployment.md).

### Super User {#gl-super-user}

A Kafka principal that ACLs never restrict, listed in the `Kafka` resource's `superUsers`. The platform profile makes `kates-backend`, the Kates API's Kafka user, a super user. See [Security & Compliance](17-security.md#granting-full-cluster-rights-super-user).

## T

### Task (Kafka Connect) {#gl-connect-task}

One unit of a connector's work, run by one Kafka Connect worker; a connector splits into up to `tasksMax` tasks. See [Kafka Connect & CDC Pipelines](21-kafka-connect.md).

### Tenant {#gl-tenant}

In this book, one service sharing a Kafka cluster: one `KafkaUser` and one topic prefix, which no other tenant's prefix may begin, because a prefixed ACL would then match that tenant's topics too. See [Multi-Tenancy](19-multi-tenancy.md#multi-tenancy-model).

### Test Run {#gl-test-run}

One execution of a test spec, with its own ID; its status is PENDING, RUNNING, STOPPING, DONE or FAILED. The Kates API runs at most three at once by default (`kates.engine.max-concurrent-tests`) and answers another with 429, and it fails a run still RUNNING 30 minutes after it was created (`kates.engine.max-duration-ms`). See [REST API Reference](11-api-reference.md#test-management).

### Test Spec {#gl-test-spec}

The settings of one test — records, record size, `acks`, topic, rate and the rest — sent as `spec`; the Kates API merges it with the test type's defaults and refuses with a 400 a setting the type can't honour. A run keeps both the merged `spec` and the `requestedSpec` as sent. CLI flags, scenario-file keys and API fields name some settings differently. See [REST API Reference](11-api-reference.md#post-apitests).

### Test Type {#gl-test-type}

What a test measures, named by the `TestType` enum: LOAD (steady rate), STRESS and CAPACITY (the ceiling), SPIKE (a burst), ENDURANCE (time), VOLUME (record size), ROUND_TRIP (produce-to-consume latency) and INTEGRITY (loss). TUNE_REPLICATION, TUNE_ACKS, TUNE_BATCHING, TUNE_COMPRESSION, TUNE_PARTITIONS (parameter sweeps) and INTEGRATION_CDC complete the enum; `kates test types` lists them. See [Test Types Deep Dive](05-test-types.md#test-type-overview).

### Throughput {#gl-throughput}

How much a system delivers per unit of time, in records per second (rec/s in tables) or megabytes per second: small records saturate a cluster on the first, large ones on the second. Kates reports both, as average throughput (records over the run's elapsed time) and peak. See [Performance Theory](04-performance-theory.md#throughput).

### Tiered Storage {#gl-tiered-storage}

A Kafka feature (KIP-405) that moves older log segments to object storage, so brokers keep less on local disk. The `kafka-cluster` chart supports it only with a Kafka image that carries a remote storage manager plugin. See [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md#tiered-storage).

### Tombstone {#gl-tombstone}

A record with a key and a null value; in a compacted topic it tells Kafka to drop earlier records with that key. See [Kafka Connect & CDC Pipelines](21-kafka-connect.md#change-data-capture-with-debezium).

### Topic {#gl-topic}

A named stream of records in Kafka, split into partitions; producers write to it and consumer groups read from it. See [The Cluster Under Test](03-cluster.md#topics-partitions-and-leaders).

### Topology {#gl-topology}

In this book, the namespace layout `kates deploy --topology` picks: the [isolated topology](#gl-isolated-topology) or the [single-namespace topology](#gl-single-namespace-topology).

### Transactional Outbox {#gl-transactional-outbox}

A pattern that writes a change and an event about it to the database in one transaction, then has a separate relay publish the event, so neither is lost if the process dies in between. The Kates API stores each run in PostgreSQL and, whenever its status changes, records a lifecycle event in the same transaction; a separate poller sends the event to Kafka and deletes it only once the broker acknowledges it. See [Architecture & Design](02-architecture.md#graceful-degradation).

### Trogdor {#gl-trogdor}

Apache Kafka's distributed test framework, in which a coordinator hands JSON task specs to agents. One of the two benchmark backends (`backend: trogdor`): Kates submits only produce, consume and round-trip workload specs, and never uses Trogdor to inject faults. See [Test Types Deep Dive](05-test-types.md).

## U

### Unclean Leader Election {#gl-unclean-leader-election}

Electing a partition leader from outside the ISR when no in-sync replica is left, which brings the partition back at the price of losing the records only the ISR had. `krafter` disables it (`unclean.leader.election.enable: false`), so a partition with no in-sync replica stays offline instead, and the chart alerts if one ever happens. See [The Cluster Under Test](03-cluster.md#replication-configuration).

### Under-Replicated Partition {#gl-under-replicated-partition}

A partition whose ISR has fewer members than its replication factor. `kates cluster check` counts them; a healthy `krafter` has none. See [Chaos Engineering Theory](06-chaos-theory.md#key-metrics-during-chaos).

## V

### Values Overlay {#gl-values-overlay}

A Helm values file layered over a chart's defaults, such as `values-kind.yaml` or `values-prod.yaml`; maps merge key by key, while a list is replaced whole. `kates deploy` layers the `.build/values-detected.yaml` it generates under the platform profile. See [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md#environment-overlays).

### Velero {#gl-velero}

A Kubernetes backup tool that snapshots resources and volumes. The `kafka-cluster` chart's `backup` block renders a Velero schedule of the cluster's objects and, optionally, its volumes; `values-prod.yaml` turns it on daily with a 14-day TTL. See [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md#velero-backup).

### Verdict {#gl-verdict}

An INTEGRITY run's outcome: DATA_LOSS if an acknowledged record is missing, otherwise CORRUPTION on a CRC failure, ORDERING_VIOLATION on a record out of order, DUPLICATES_DETECTED on a duplicate, and PASS. `kates test get <id>` prints it in the Data Integrity section. The 'SLA Verdict' heading of `kates report show` is a pass or fail against SLA thresholds, not this. See [Data Integrity Verification](08-data-integrity.md#interpreting-integrity-results).

### Virtual Thread {#gl-virtual-thread}

A lightweight Java thread (Java 21) that the JVM schedules onto a few operating-system threads, so blocking one is cheap. The native benchmark backend runs each test task on its own virtual thread. See [Architecture & Design](02-architecture.md#technology-stack).

## W

### Worker (Kafka Connect) {#gl-connect-worker}

A Kafka Connect process; the workers that share a `groupId` form one Connect cluster and share its tasks, rebalancing them when a worker joins or leaves. The `connect-cluster` chart's `groupId` is `kates-connect-cluster` by default. See [Kafka Connect & CDC Pipelines](21-kafka-connect.md).

## Z

### Zone {#gl-zone}

A failure domain the cluster spreads replicas across; in this book a zone, Strimzi's rack and a cloud availability zone (AZ) are the same thing. The `kafka-cluster` chart sets Strimzi's rack from each node's `topology.kubernetes.io/zone` label, and Kafka spreads a new partition's replicas across the zones. The `panda` nodes are named after their zones: `alpha`, `sigma`, `gamma`. See [The Cluster Under Test](03-cluster.md#node-labeling-and-zone-simulation).
