# Concept Registry

A concept explained in four chapters gets four slightly different explanations, and the reader can't tell which one is right. This registry gives every concept the book teaches one home: the section that explains it in full, as [Book Style Sheet](STYLE.md) describes. Everywhere else, a chapter uses the concept's gloss, or a clause cut from it, and links the home section by its heading. The last column names the concept's main Glossary entry, whose definition links back to the same home.

Use the registry at three moments:

- Before you explain a concept, find its row. If the chapter you're editing isn't the home, write the gloss and link the home instead of explaining the concept again.
- Before you teach a concept that has no row, add one: the concept, its home, a one-sentence gloss that is true of the code, and a Glossary anchor. Add the Glossary entry in the same pull request.
- When you rename or move a home section, or change what a gloss says, update its row in the same pull request.

A home marked "planned" doesn't exist yet: until the phase that writes it merges, link the chapter by its H1 title, with no fragment. An anchor is the `{#gl-…}` id of a Glossary entry, which you link as `appendix-a-glossary.md#gl-<slug>` with the term as the link text; the entries after "also" are the other terms the home teaches.

This file is about the book rather than in it: the render leaves it out, and the style check and the metrics skip it, as they skip STYLE.md and README.md. The link check reads it, so every link here must resolve.

## The Preface and Part I — Foundations

The Preface is home to the lab, The Cluster Under Test to the Kafka mechanics behind every number the book shows, and Architecture & Design to where Kates keeps its results:

| Concept | Home | Gloss | Glossary anchor |
|--------------|--------------------|--------------------------------------|------------|
| The lab: `panda`, its zones and `krafter` | Before You Start, in the [Preface](index.qmd) | `panda` is the three-node Kind cluster the book's lab runs on, its nodes `alpha`, `sigma` and `gamma` each standing for a zone, and `krafter` is the Kafka cluster under test on it. | `gl-krafter`; also `gl-panda`, `gl-zone` |
| Consumer groups, committed offsets and consumer lag | planned: Kafka You Need for This Book (Phase B), in [The Cluster Under Test](03-cluster.md) | Consumer lag is, for each partition, the latest offset in the log minus the offset the consumer group last committed, which is where a restarted consumer resumes. | `gl-consumer-lag`; also `gl-consumer-group`, `gl-offset` |
| `acks`, the ISR and `min.insync.replicas` | [Replication Configuration](03-cluster.md#replication-configuration), in [The Cluster Under Test](03-cluster.md) | With `acks=all` the leader acknowledges a write only once every replica in the ISR (the replicas caught up with it) has it, and `min.insync.replicas`, 2 on `krafter`, is the smallest ISR it accepts such a write with. | `gl-min-insync-replicas`; also `gl-acks`, `gl-isr` |
| Unclean leader election, log truncation and the high watermark | [Replication Configuration](03-cluster.md#replication-configuration), in [The Cluster Under Test](03-cluster.md) | With unclean leader election off, as on `krafter`, Kafka elects a leader only from the ISR, so an election can't truncate away records it acknowledged under `acks=all`, and a partition with no in-sync replica stays offline instead. | `gl-unclean-leader-election`; also `gl-high-watermark`, `gl-log-truncation` |
| Partition leadership, leader election and client retry | [What Happens During a Broker Failure](03-cluster.md#what-happens-during-a-broker-failure), in [The Cluster Under Test](03-cluster.md) | When a partition's leader broker fails, the controller elects a new leader from the ISR, and until producers learn of it their writes to that partition fail and the client retries them. | `gl-leader-election`; also `gl-partition-leader` |
| The KRaft quorum, controllers, fencing and the metadata log | planned: Kafka You Need for This Book (Phase B), in [The Cluster Under Test](03-cluster.md) | `krafter` keeps its metadata in a KRaft quorum of three controllers, which needs a majority of two to commit a change, so it survives exactly one controller loss. | `gl-kraft`; also `gl-controller`, `gl-quorum`, `gl-fencing`, `gl-metadata-log` |
| Rack, zone and AZ, and rack-aware placement | [Node Labeling and Zone Simulation](03-cluster.md#node-labeling-and-zone-simulation), in [The Cluster Under Test](03-cluster.md) | In this book a zone, Strimzi's rack and a cloud availability zone are one failure domain, set from each node's `topology.kubernetes.io/zone` label, and Kafka spreads a partition's replicas across racks when it creates the partition. | `gl-zone` |
| Page cache and the memory budget | [Resource Budget](03-cluster.md#resource-budget), in [The Cluster Under Test](03-cluster.md) | A broker's page cache, the memory the operating system uses to serve recent records without touching disk, is roughly its pod memory minus its JVM heap, which the `kafka-cluster` chart's defaults fix at 2048m. | `gl-page-cache` |
| Listeners: `plain`, `tls` and `external` | [Listeners](03-cluster.md#listeners), in [The Cluster Under Test](03-cluster.md) | `krafter` always has two internal listeners, `plain` on 9092 (SCRAM-SHA-512 without TLS) and `tls` on 9093 (mutual TLS), and an `external` one on 9094 only where the values declare it, so 'plain' means no TLS, not no authentication. | `gl-listener`; also `gl-scram-sha-512`, `gl-mtls` |
| Strimzi pod names and node IDs | [Physical Topology](03-cluster.md#physical-topology), in [The Cluster Under Test](03-cluster.md) | Strimzi names each Kafka pod after the cluster, its node pool and its node ID, as in `krafter-brokers-alpha-0`, and a fault's `targetBrokerId` picks the broker pod whose name ends in that ID, or the first broker when none does. | `gl-node-id`; also `gl-kafkanodepool` |
| Where results live, and Kates sharing the cluster it tests | planned: Where Results Live (Phase B), and Kates Shares the Cluster It Tests (Phase C), in [Architecture & Design](02-architecture.md) | The Kates API stores each run in PostgreSQL and, whenever its status changes, queues a lifecycle event in the same transaction for an outbox poller to publish to `kates-test-events` on the cluster under test. | `gl-transactional-outbox`; also `gl-postgresql` |

## Part II — Performance Testing

Part II is home to measurement: how latency and throughput behave under load, what each test type measures, and how a gate turns a number into a pass or a fail:

| Concept | Home | Gloss | Glossary anchor |
|--------------|--------------------|--------------------------------------|------------|
| Queueing and the saturation knee | planned: Why Latency Bends: Queues (Phase D), in [Performance Theory](04-performance-theory.md) | As load approaches the rate a stage of the produce path can serve, requests queue in front of it, so latency climbs steeply while throughput levels off: that bend is the saturation point. | `gl-saturation-point` |
| Open- and closed-loop load, and coordinated omission | [Coordinated Omission](04-performance-theory.md#coordinated-omission), in [Performance Theory](04-performance-theory.md) | The native benchmark backend sends without waiting for each acknowledgment and paces sends to the target rate, but it doesn't correct for coordinated omission, so a stall that blocks the producer is under-recorded. | `gl-coordinated-omission`; also `gl-open-loop-load` |
| How many samples a percentile needs | [The Percentile Solution](04-performance-theory.md#the-percentile-solution), in [Performance Theory](04-performance-theory.md) | P99 of N records rests on the slowest N/100 of them and Max on a single record, so a run with few records gives an unstable tail. | `gl-percentile` |
| The noise floor, and calling a regression | [Statistical Significance](04-performance-theory.md#statistical-significance), in [Performance Theory](04-performance-theory.md) | A difference between two runs is a regression only when it's larger than the spread between repeated identical runs, the noise floor, and points the same way every time you repeat it. | `gl-noise-floor`; also `gl-baseline` |
| Cost per record versus cost per byte | [Throughput](04-performance-theory.md#throughput), in [Performance Theory](04-performance-theory.md) | Kates reports throughput in records per second and in megabytes per second, and small records saturate a cluster on the first while large ones saturate it on the second. | `gl-throughput` |
| The benchmark backend: native versus Trogdor | planned: Two Ways to Generate Load (Phase D), in [Test Types Deep Dive](05-test-types.md) | The benchmark backend, a test's `backend` field, generates its load: `native`, the default, runs producers and consumers inside the Kates API, and `trogdor` submits workload specs to a Trogdor coordinator and injects no faults. | `gl-benchmark-backend`; also `gl-trogdor` |
| Choosing a test type, and which latency each reports | [Test Type Overview](05-test-types.md#test-type-overview), in [Test Types Deep Dive](05-test-types.md) | Each test type answers one question: LOAD a steady rate, STRESS and CAPACITY the ceiling, SPIKE a burst, ENDURANCE time, VOLUME record size, ROUND_TRIP produce-to-consume latency, and INTEGRITY whether acknowledged records survive. | `gl-test-type` |
| Choosing a gate threshold | planned: Choosing Thresholds (Phase D), in [Scenario Files & SLA Gates](13-scenario-files.md) | Set a gate from a baseline of several runs plus a margin wider than their run-to-run spread, not from the number you hope to see. | `gl-gate` |
| Gate, grade and verdict; where a run's SLA comes from; SLA versus SLO | [Validation Reference (SLA Gates)](13-scenario-files.md#validation-reference-sla-gates), in [Scenario Files & SLA Gates](13-scenario-files.md) | A gate is a threshold in a scenario's `validate` block, a grade is the letter a disruption plan's `sla` block earns, and a verdict is an INTEGRITY run's outcome; Kates's 'SLA' thresholds are SLO-style targets, not a contract. | `gl-sla`; also `gl-gate`, `gl-sla-grade`, `gl-verdict`, `gl-slo`, `gl-sli` |
| What a Lab iteration costs, and which parameters a run uses | [Iteration Workflow](10b-lab.md#iteration-workflow), in [Lab — Interactive Performance Tuning](10b-lab.md) | Each Lab iteration is a full Kates test run with its own ID, so a sweep, the warm-up runs and median mode's three runs each cost a test's time. | `gl-lab` |

## Part III — Chaos & Integrity

Part III is home to faults and what they cost: the words for a fault, the chaos provider and safety guard that carry one out, how recovery is measured, and what counts as lost:

| Concept | Home | Gloss | Glossary anchor |
|--------------|--------------------|--------------------------------------|------------|
| The SLA grade for disruption plans | [SLA Grading](07-chaos-practice.md#sla-grading), in [Chaos Engineering in Practice](07-chaos-practice.md) | A disruption plan's `sla` block earns a letter grade: A when every check passes, F on any critical miss, B, C or D by the share of failed checks, and `-` when nothing could be evaluated. | `gl-sla-grade` |
| Fault vocabulary, and plan versus resilience run | planned: Two Ways to Run a Fault (Phase B), in [Chaos Engineering in Practice](07-chaos-practice.md) | A disruption plan injects faults and watches the cluster from outside, through Prometheus and the pod watcher, while a resilience run injects one fault into a running Kates test and sees what its client sees. | `gl-fault`; also `gl-disruption`, `gl-disruption-plan`, `gl-disruption-type`, `gl-playbook`, `gl-resilience-run`, `gl-chaos-experiment` |
| The chaos provider and its RBAC | planned: Choosing a Chaos Provider (Phase B), in [Chaos Engineering in Practice](07-chaos-practice.md) | The chaos provider, set by `kates.chaos.provider`, carries out faults: `litmus-crd` (the default) through LitmusChaos, `kubernetes` through the Kubernetes API, `hybrid` picking one of those at startup, and `noop`, which injects nothing. | `gl-chaos-provider`; also `gl-litmuschaos` |
| The safety guard, blast radius, `maxAffectedBrokers` and rollback | [Safety Guardrails](07-chaos-practice.md#safety-guardrails), in [Chaos Engineering in Practice](07-chaos-practice.md) | Before a plan runs, Kates refuses it when its steps would hit more broker pods than `maxAffectedBrokers` or leave no broker untouched, and before each fault it requires every Kafka pod to be Running and Ready. | `gl-safety-guard`; also `gl-blast-radius`, `gl-disruption-rollback` |
| A steady-state hypothesis made measurable | [1. Build a Hypothesis Around Steady State](06-chaos-theory.md#1-build-a-hypothesis-around-steady-state), in [Chaos Engineering Theory](06-chaos-theory.md) | A steady-state hypothesis says, in numbers you can measure, what the cluster should show before, during and after a fault, such as a P99 bound and zero lost records. | `gl-steady-state-hypothesis` |
| ISR shrink: why durability erodes before availability | [ISR Shrink and Expand](06-chaos-theory.md#isr-shrink-and-expand), in [Chaos Engineering Theory](06-chaos-theory.md) | As the ISR shrinks from three to two, each new acknowledged record has one copy fewer while writes carry on, and at one, below `min.insync.replicas`, the leader refuses `acks=all` writes: durability erodes before availability does. | `gl-isr-shrink`; also `gl-under-replicated-partition` |
| Recovery measured three ways; RTO and RPO as measurements | planned: Recovery, Measured Three Ways (Phase D), in [Chaos Engineering Theory](06-chaos-theory.md) | Kates reports recovery three ways — the ISR whole again, the broker pods ready again (what a plan's `maxRtoMs` grades) and an INTEGRITY run's send or read gap — and its RTO and RPO are measurements, not objectives. | `gl-rto`; also `gl-rpo` |
| Game Day | [The Game Day Methodology](06-chaos-theory.md#the-game-day-methodology), in [Chaos Engineering Theory](06-chaos-theory.md) | A Game Day is a planned session in which a team runs chaos experiments against a written hypothesis, with a rollback plan and a debrief, and `make gameday` runs an automated seven-phase version of it. | `gl-game-day` |
| Consumer-group rebalance, and the three things called "rebalance" | [Consumer Group Rebalance](06-chaos-theory.md#consumer-group-rebalance), in [Chaos Engineering Theory](06-chaos-theory.md) | A consumer-group rebalance reassigns a group's partitions when a member joins or leaves, and Kates's test consumers use the classic group protocol, the default of the Kafka client they run on. | `gl-rebalance` |
| Only acknowledged records carry a promise; Lost is counted per sequence | [Sequence Number Tracking](08-data-integrity.md#sequence-number-tracking), in [Data Integrity Verification](08-data-integrity.md) | Only acknowledged records carry a durability promise, and an INTEGRITY run counts as lost each acknowledged sequence number it never reads back, not the difference between two counts. | `gl-lost-record`; also `gl-verdict` |
| The guarantee ladder: durability, idempotence, transactions, CRC | [Integrity Modes](08-data-integrity.md#integrity-modes), in [Data Integrity Verification](08-data-integrity.md) | The Kafka producer is idempotent by default with `acks=all`, so an INTEGRITY run has two practical modes, idempotent and transactional (`enableTransactions`), plus a per-record CRC check that's on unless `enableCrc: false`. | `gl-idempotent-producer`; also `gl-exactly-once-semantics`, `gl-crc32` |

## Part IV — Observability

Observability & Monitoring is home to where a run's numbers live and how to read them:

| Concept | Home | Gloss | Glossary anchor |
|--------------|--------------------|--------------------------------------|------------|
| Live meters in Prometheus versus stored results in PostgreSQL | [Observability Architecture](09-observability.md#observability-architecture), in [Observability & Monitoring](09-observability.md) | The Kates API exports a run's live meters to Prometheus only while the run is in flight and keeps finished results in PostgreSQL, which is why the live boards go quiet between runs while `kates trend` keeps history. | `gl-prometheus`; also `gl-postgresql` |
| Heatmap rows, the 25 buckets, and reading a heatmap | [Latency Heatmaps](09-observability.md#latency-heatmaps), in [Observability & Monitoring](09-observability.md) | A heatmap row is taken each time the Kates API polls a running native-backend test, every 5 s by default and on each read of the run, and counts everything recorded so far in 25 latency buckets. | `gl-heatmap` |
| Produce purgatory, request-handler idle, quantile gauges, label cardinality | [Reading the Dashboards: A Diagnostic Walkthrough](09-observability.md#reading-the-dashboards-a-diagnostic-walkthrough), in [Observability & Monitoring](09-observability.md) | Produce purgatory is where a broker parks an `acks=all` request until the ISR has the record, and a request-handler idle ratio of 0.3 means the broker's request threads are busy 70% of the time. | `gl-purgatory`; also `gl-request-handler-idle-ratio` |

## Part V — Deployment & Operations

Part V is home to the platform around the cluster: the operator, the charts, security, tenancy, upgrades, Kafka Connect and migration:

| Concept | Home | Gloss | Glossary anchor |
|--------------|--------------------|--------------------------------------|------------|
| Operator, custom resource, CRD and reconciliation | planned: How the Operator Works (Phase D), in [Deploying the Strimzi Operator](deploying-strimzi-operator.md) | The Strimzi Cluster Operator watches custom resources such as `Kafka` and `KafkaNodePool` and changes the cluster's pods, Secrets and other objects until they match, so while it's down, changes to those resources wait. | `gl-operator`; also `gl-custom-resource`, `gl-crd`, `gl-reconciliation` |
| Installing and upgrading the operator | [Upgrading the Operator](deploying-strimzi-operator.md#upgrading-the-operator), in [Deploying the Strimzi Operator](deploying-strimzi-operator.md) | The Strimzi operator is its own Helm release, `strimzi-operator` in the `strimzi-operator` namespace, installed and upgraded separately from the Kafka clusters it manages. | `gl-strimzi` |
| Which Kates image runs where, and whose GC shows in P99 | planned: JVM or Native Image (Phase C), in [Deployment Guide](12-deployment.md) | On a Kind cluster `kates deploy` runs the Kates API as a native image, which uses GraalVM's Serial GC, and on other clusters the JVM image with generational ZGC, so a local run's P99 and Max include Serial GC pauses. | `gl-graalvm-native-image`; also `gl-virtual-thread` |
| Single versus isolated namespace topology | [Single-Namespace vs Multi-Namespace](12-deployment.md#single-namespace-vs-multi-namespace), in [Deployment Guide](12-deployment.md) | `kates deploy --topology isolated`, the default, gives Kafka, the Kates API and the chaos tooling their own namespaces (`kafka`, `kates`, `litmus`), `--topology single` puts them in one (`kates-stack` by default), and the operator goes to `strimzi-operator` either way. | `gl-isolated-topology`; also `gl-single-namespace-topology` |
| PrometheusRule alerts and their runbooks | [PrometheusRule Alerts](20-installation-guide.md#prometheusrule-alerts), in [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md) | The `kafka-cluster` chart renders its alerts as a PrometheusRule where the Prometheus Operator's API exists, and each rule's `runbook_url` points at its entry in `docs/kafka-cluster-runbook.md`. | `gl-prometheusrule` |
| Topics and users as code | [Topics & Users Reference](20-installation-guide.md#topics--users-reference), in [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md) | The `kafka-cluster` chart turns each entry under `topics.items` and `users.items` into a Strimzi `KafkaTopic` or `KafkaUser` resource, which the Entity Operator applies to the cluster. | `gl-kafkatopic`; also `gl-kafkauser` |
| Cruise Control and `KafkaRebalance` | [Cruise Control & Rebalance](20-installation-guide.md#cruise-control--rebalance), in [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md) | Cruise Control moves partition replicas between brokers when a `KafkaRebalance` asks it to, and although the `kafka-cluster` chart enables it by default, the values `kates deploy` generates and the Kind overlay turn it off. | `gl-cruise-control`; also `gl-kafkarebalance` |
| Helm releases, the two release names, values layering | planned: Helm in Two Minutes, and How Values Are Layered (Phase C), in [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md) | `kates deploy` installs `krafter` as the Helm release `krafter` and `make kafka` installs it as the release `kafka-cluster`, and you change either from its own `helm get values`, never with `--reuse-values`. | `gl-helm-release`; also `gl-values-overlay`, `gl-platform-profile` |
| `productionMode` and the render-time rails | [Production](20-installation-guide.md#production), in [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md) | `productionMode: true` makes the `kafka-cluster` chart fail the render, and so `helm install` or `upgrade`, rather than deploy an unsafe setting such as a NodePort listener without TLS or `networkPolicy.enabled: false`. | `gl-productionmode` |
| NetworkPolicy additivity and `networkPolicyPeers` | planned: How the Policies Combine (Phase B), in [Security & Compliance](17-security.md) | NetworkPolicies add up rather than override, so as shipped every pod reaches the Kafka client listeners: Strimzi's generated policy admits every source to a listener without `networkPolicyPeers`, whatever the chart's own policies say. | `gl-networkpolicy`; also `gl-networkpolicypeers` |
| Kyverno admission policies | [Kyverno Policy Integration & Admission Control](17-security.md#kyverno-policy-integration--admission-control), in [Security & Compliance](17-security.md) | Kyverno is an optional admission controller that `kates deploy --with-kyverno` installs; its policies patch resources as they are created and reject those that break a rule in Enforce mode, or only report them in Audit mode. | `gl-kyverno`; also `gl-admission-webhook`, `gl-clusterpolicy` |
| Certificates and CAs | [Certificate Management](17-security.md#certificate-management), in [Security & Compliance](17-security.md) | Strimzi runs two certificate authorities per Kafka cluster: the cluster CA, which signs the Kafka nodes' certificates, and the clients CA, which signs the certificates of `KafkaUser`s that authenticate with TLS. | `gl-cluster-ca`; also `gl-clients-ca` |
| What Kates can do to your cluster, and who can make it | planned: Securing Kates (Phase D), in [Security & Compliance](17-security.md) | Every REST and gRPC call except REST `/api/health` and Quarkus's `/q/` endpoints needs the key in the `kates-api-key` Secret, and its holder can start load and faults that Kates runs as a Kafka super user with cluster-wide Kubernetes permissions. | `gl-api-key`; also `gl-super-user` |
| Secrets are namespace-local | [Cross-Namespace Credential Synchronization](17-security.md#cross-namespace-credential-synchronization), in [Security & Compliance](17-security.md) | The User Operator writes each `KafkaUser`'s Secret only into the Kafka cluster's namespace, and a pod can't use a Secret from another namespace, so a client elsewhere needs a copy that you refresh when the password rotates. | `gl-secret` |
| A tenant and prefix-scoped tenancy | [Multi-Tenancy Model](19-multi-tenancy.md#multi-tenancy-model), in [Multi-Tenancy](19-multi-tenancy.md) | In this book a tenant is one service with one `KafkaUser` and one topic prefix, and no tenant's prefix may begin another's, because a prefixed ACL would then match the other tenant's topics. | `gl-tenant` |
| Quotas: byte-rate mechanics and `requestPercentage` | planned: How Quotas Work (Phase D), in [Multi-Tenancy](19-multi-tenancy.md) | A Kafka quota caps a client's byte rate over a time window, and the broker enforces it by delaying its responses, so a throttled tenant sees latency rather than errors. | `gl-quota` |
| The operator's Kafka version window and the KRaft metadata version | planned: Two Versions, Two Steps (Phase D), in [Upgrade Playbook](18-upgrade-playbook.md) | A Strimzi operator release supports a window of Kafka versions, and the KRaft metadata version can never be newer than the running Kafka version, so the two versions change in separate steps. | `gl-metadata-version` |
| The Connect model: connector, task, worker group, rebalance | planned: How Connect Divides the Work (Phase D), in [Kafka Connect & CDC Pipelines](21-kafka-connect.md) | A connector splits its work into up to `tasksMax` tasks, and the Kafka Connect workers that share a `groupId` share those tasks, rebalancing them when a worker joins or leaves. | `gl-connector`; also `gl-connect-task`, `gl-connect-worker` |
| The Debezium CDC lifecycle | [Change Data Capture with Debezium](21-kafka-connect.md#change-data-capture-with-debezium), in [Kafka Connect & CDC Pipelines](21-kafka-connect.md) | Debezium's PostgreSQL connector first snapshots the captured tables, then streams their changes from the write-ahead log through a replication slot, which holds on to WAL until the connector has read it. | `gl-debezium`; also `gl-cdc`, `gl-replication-slot`, `gl-tombstone` |
| What a Connect rebalance costs, and who owns connector state | planned: a section on rebalance cost and who owns connector state (Phase D), in [Operating Kafka Connect](operating-kafka-connect.md) | When a Kafka Connect rebalance moves a task, the task resumes from its last committed offset, so records processed after that commit can be delivered twice. | `gl-kafkaconnector` |
| MirrorMaker 2: offset translation, checkpoints, identity policy, presets | [Offset Translation Is the Half People Skip](22-mirror-maker2-migration.md#offset-translation-is-the-half-people-skip), in [Cross-Cluster Replication and Migration](22-mirror-maker2-migration.md) | MirrorMaker 2 copies topics from a source cluster to a target and translates each consumer group's committed offsets through checkpoints, so a group moved to the target resumes at or before its old position. | `gl-offset-translation`; also `gl-mirrormaker-2` |
| The protocol floor (KIP-896) and the legacy version cliffs | [The Cliff, and Where It Is](23-legacy-source-migration.md#the-cliff-and-where-it-is), in [Migrating a Legacy Kafka Source](23-legacy-source-migration.md) | Kafka 4.0 removed the client protocol versions older than Kafka 2.1 (KIP-896), so a 4.x MirrorMaker 2 reads only from a source broker running 2.1 or newer. | `gl-protocol-floor` |
| End-to-end workflows | [Recipes & Patterns](14-recipes.md), the whole chapter | Recipes & Patterns walks through complete workflows, such as validating a Kafka upgrade or running a nightly regression suite, from the first command to the result. | `gl-recipe` |

## Part VI — Reference and the Appendices

The reference chapters and the appendices are home to what readers look up, such as the test spec, exit codes, grades, run states, CLI contexts and chart versions:

| Concept | Home | Gloss | Glossary anchor |
|--------------|--------------------|--------------------------------------|------------|
| Test spec fields, defaults, refusals, and the merged `spec` versus `requestedSpec` | [POST /api/tests](11-api-reference.md#post-apitests), in [REST API Reference](11-api-reference.md) | The Kates API merges a request's `spec` with its test type's defaults, keeps both the merged `spec` and the `requestedSpec` as sent, and refuses with a 400 any setting the type can't honour. | `gl-test-spec` |
| Exit codes and the scripting contract | [Exit Codes](10-cli-reference.md#exit-codes), in [CLI Reference](10-cli-reference.md) | `kates test apply --wait` exits 1 when a scenario fails to submit, fails or violates a gate, so a scenario without a `validate` block can't fail a pipeline on its numbers. | `gl-exit-code` |
| The performance grade (`kates gate`, `benchmark`, `badge`) | planned: How Kates Grades a Run (Phase C), in [CLI Reference](10-cli-reference.md) | `kates gate` starts a test run and grades it from A to F on its average throughput and P99 against fixed thresholds that don't adapt to cluster size, and `kates benchmark` grades a battery of runs from a score instead. | `gl-performance-grade`; also `gl-security-grade` |
| The CLI context, and which commands go through the API, Kubernetes or local files | [Context Management](10-cli-reference.md#context-management), in [CLI Reference](10-cli-reference.md) | A CLI context, kept in `~/.kates.yaml`, is a named Kates API URL with its API key, proxy and output settings, and it's separate from your `kubectl` context. | `gl-cli-context` |
| Baselines, profiles and snapshots | planned: a table comparing baselines, profiles and snapshots (Phase C), in [CLI Reference](10-cli-reference.md) | A baseline is a run the Kates API keeps per test type for later runs to compare against, while `kates profile` and `kates snapshot` save a run's metrics and the cluster's state as local files under `~/.kates`. | `gl-baseline`; also `gl-profile`, `gl-snapshot` |
| The MCP server | [MCP Server for AI Agents](10-cli-reference.md#mcp-server-for-ai-agents), in [CLI Reference](10-cli-reference.md) | `kates mcp` serves Kates to an AI agent over the Model Context Protocol on stdio, read-only, and it never starts load or faults and won't start without an explicit context and an `--allow-cluster` clusterId. | `gl-mcp` |
| Run states across interfaces, and the concurrency limit | [Test Management](11-api-reference.md#test-management), in [REST API Reference](11-api-reference.md) | A test run's status is PENDING, RUNNING, STOPPING, DONE or FAILED, and the Kates API runs at most three runs at once by default (`kates.engine.max-concurrent-tests`), answering another with 429. | `gl-test-run` |
| Reading proto3 JSON output | planned: Reading grpcurl Output (Phase D), in [gRPC API Reference](16-grpc-api.md) | `grpcurl` prints responses as proto3 JSON, in which fields at their zero value are left out, 64-bit integers appear as strings and field names are camelCase. | `gl-protobuf` |
| The Strimzi readiness chain | planned: the readiness chain, in two sentences and a diagram (Phase D), in [Troubleshooting Index](appendix-b-troubleshooting.md) | A client pod that mounts a `KafkaUser`'s Secret can't start until the `Kafka` resource is Ready, the Entity Operator is running and its User Operator has written that Secret. | `gl-entity-operator` |
| Chart version versus `appVersion` | [Version & Compatibility Matrix](appendix-d-versions.md), the whole appendix | A Helm chart's `version` numbers the chart itself while its `appVersion` names the release of the software it deploys, and a library chart such as `kafka-common` deploys nothing on its own. | `gl-appversion`; also `gl-library-chart` |
| The metric contract | [The Render Matrix](appendix-c-cicd.md#the-render-matrix), in [CI/CD Pipeline](appendix-c-cicd.md) | The metric contract is the rule that every series an alert or dashboard panel reads is one the chart's exporter rules actually produce, which `scripts/check-metric-contract.sh` checks in CI. | `gl-metric-contract` |

## The Running Example

The book follows one question from the Preface to the last recipe: is `krafter` ready for a payments workload? Use it wherever a chapter needs a worked example, so that the examples add up to one answer. The question needs throughput and latency (Part II), failure and loss (Part III) and the explanation of both (Part IV), and payments make durability concrete: a payment the cluster has acknowledged must not disappear.

### The Situation and the Targets

The Introduction and the Part pages state the situation in these words, in the second person, with no invented people, companies, dates or incidents:

> You run the payments platform, and before it moves onto `krafter` you have to say whether the cluster can carry it. The book turns that into targets a Kates run can check. `krafter` must take 2,000 records per second of 1 KiB with `acks=all`, and answer writes with a P99 of 100 ms or less from send to acknowledgment. It must also lose no acknowledged record when one broker or one zone fails.

Each target has a field that sets or checks it and a place where Kates reports it. Write every one into the files, even where it matches a default, so that a changed default can't change the example:

| Target | Set or checked by | Where you read it |
|------------------|------------------------------------------------|-------------------------------------|
| 2,000 rec/s | `targetThroughput: 2000` offers it (`throughput` in a resilience file); `minThroughputRecPerSec` gates it | The `produce` and `consume` rows of `kates test get <id>` |
| 1 KiB records | `recordSizeBytes: 1024` in a scenario file; `recordSize: 1024` in a resilience file | The run's spec |
| `acks=all` | `acks: all`, with `replicationFactor: 3` and `minInsyncReplicas: 2` | The run's spec |
| P99 ≤ 100 ms | `maxP99LatencyMs: 100` | The `produce` row of `kates test get <id>` |
| No loss, one broker down | `payments-integrity-broker.yaml`: INTEGRITY through a `POD_KILL` of one broker | `Lost` and `Verdict` of the INTEGRITY run, once it's `DONE` |
| No loss, one zone down | `payments-integrity-zone.yaml`: the same, killing every Kafka pod in zone `alpha` | The same |

Four facts from the code shape how a chapter writes about these targets:

- The target's P99 is the `produce` row's: send to acknowledgment, timed in the producer's callback. A `consume` row measures no latency and shows 0, and report summaries average the rows' P99s, so quote the `produce` row and never call the target end-to-end.
- The throughput gate can't be 2,000. The rate limiter never makes up for a late send, and the measured rate also counts the time to build and close the producer, so a run offered 2,000 records per second measures less and `minThroughputRecPerSec: 2000` fails.
- The Kates API applies `replicationFactor` and `minInsyncReplicas` only when it creates the topic. The first run that names `payments-load` creates it, and later runs leave it as it is.
- `kates resilience run` returns after the fault, often while its INTEGRITY run is still producing, and its summary shows no integrity result. Find that run with `kates test list --type INTEGRITY` and read it with `kates test get <id>` once it's `DONE`. Start the zone file only after the broker file's run is `DONE`, because both use the same topic and consumer group.

### Names

Everything the example creates starts with `payments`, after the `<service-name>-<domain>[-<qualifier>]` convention in [Multi-Tenancy](19-multi-tenancy.md). Each file appears in full once, in its home chapter, and other chapters show only the lines they change:

| Name | What it is | Home |
|------------------------------|-------------------------------------------------|---------------------------|
| `payments-load` | The LOAD scenario's topic | Part II |
| `payments-perf` | The LOAD run's consumer group | Part II |
| `payments-integrity` | The INTEGRITY runs' topic | Part III |
| `payments-verify` | INTEGRITY's `consumerGroup`, which Kates reads as `payments-verify-integrity` | Part III |
| `payments` | The tenant: its `KafkaUser` and its ACL prefix | [Multi-Tenancy](19-multi-tenancy.md) |
| `payments-events` | The platform's own topic in the tenant block; no Kates run writes to it | [Multi-Tenancy](19-multi-tenancy.md) |
| `payments-scenarios.yaml` | The scenario file | [Scenario Files & SLA Gates](13-scenario-files.md) |
| `payments-broker-loss.json`, `payments-zone-loss.json` | The disruption plans, in JSON because `kates disruption run` reads only JSON | [Chaos Engineering in Practice](07-chaos-practice.md) |
| `payments-integrity-broker.yaml`, `payments-integrity-zone.yaml` | The resilience files | [Data Integrity Verification](08-data-integrity.md) |

Naming the topics keeps the example off the default topics that every unnamed run shares, such as `load-test`. The example assumes the isolated topology, as the Quick Start does, so `krafter` is in the `kafka` namespace.

### What Each Part Adds

Each Part adds one artifact toward the answer, and each artifact is a file, a command or a report that Kates produces:

| Part | The artifact | How you make it |
|----------------------------|--------------------------------------------|------------------------------------------|
| I — Foundations | The lab, and a first LOAD report | `make all`, then the Quick Start |
| II — Performance Testing | `payments-scenarios.yaml`, whose gates encode the throughput and P99 targets, and its LOAD baseline | `kates test apply -f payments-scenarios.yaml --wait`; `kates test baseline set <run-id>` |
| III — Chaos & Integrity | Two graded plans and two integrity verdicts | `kates disruption run --config <plan> --fail-on-sla-breach`; `kates resilience run -f <file>` |
| IV — Observability | The explanation of a run | `kates report diff`, `kates report heatmap`, `kates trend --type LOAD --phase produce`; the Grafana boards |
| V — Deployment & Operations | The tenant block, a re-test around an upgrade, and the readiness sign-off | The scenario file before and after the upgrade, compared with `kates report diff` |
| VI — Reference | None: the fields, flags and exit codes the files use | The reference chapters |

[Recipes & Patterns](14-recipes.md) closes the book with the readiness sign-off. `krafter` is ready when all three checks hold:

1. `kates test apply -f payments-scenarios.yaml --wait` exits 0.
2. Both resilience runs report `Status` COMPLETED, and their INTEGRITY runs, once `DONE`, show `Lost` 0 and an RPO rather than "not measured".
3. Both plans report `Status` COMPLETED and `Grade` A, and exit 0 under `--fail-on-sla-breach`.

The third check reads the status and the grade as well as the exit code, because `--fail-on-sla-breach` exits 1 only on a violated constraint. A PARTIAL plan with no violation exits 0, and so does a grade of `-`, which means nothing was graded.

### The Tutorials

The [Kates Tutorials](../tutorials/README.md) stay practice beside the chapters, and Tutorial 1 stays a tutorial rather than merging into the Quick Start. They keep their own names, such as the default `load-test` topic and `broker-kill-plan.json`, and each Part page links the ones that exercise its artifact:

| Artifact | The tutorials that exercise it |
|------------------------------|---------------------------------------------------------|
| Part I's lab and first run | 1, Getting Started with Kates; 8, Deploy, Detect & Clean |
| Part II's scenario file and gates | 2, Running Every Test Type; 6, CI/CD Integration |
| Part III's plans and verdicts | 3, Chaos Engineering with Kates; 4, Data Integrity Under Fire, for the broker verdict only; 6 |
| Part IV's explanation | 5, Heatmaps, Trends, and Exports; 13, Using the Grafana Dashboards |
| None: outside the example | 7, Kyverno & Security; 9 to 12, Kafka Connect and MirrorMaker 2; 14, the AI agent, which goes with Part VI |

### Using It in a Chapter

Use the running example in the narrative chapters of Parts I to IV, on the Part pages, and in Multi-Tenancy, the Upgrade Playbook and Recipes & Patterns. The reference chapters, the appendices, the install and deploy chapters, Security & Compliance and the Connect and MirrorMaker 2 chapters don't need it. When you use it:

- Use the names and values here exactly, and add a name here before a chapter uses it.
- State the targets at most once per chapter; the Part pages carry them.
- Never state an outcome, such as "`krafter` passes" or a measured P99, unless you captured it, and label every number with its source ("a LOAD run of `payments-scenarios.yaml` on `panda`").
- Say which P99 you mean. A plan's `sla` block grades the slowest broker's produce-request P99 from Prometheus, so never compare it with a gate's.
- Use the words the [Terminology](STYLE.md#terminology) table fixes: gate for a scenario's `validate` block, grade for a plan's `sla` block, verdict for an integrity result.
- When a concept needs a setting the example doesn't use, such as `acks=1`, use it and say in one sentence how it bears on the targets.

### Not Settled Yet

These wait on the maintainer, so don't write around them:

- The throughput floor: how far below 2,000 `minThroughputRecPerSec` sits, or whether the scenario offers more than 2,000 and gates at 2,000.
- The LOAD duration: whether the scenario sets `durationSeconds` above LOAD's 600 s default, so that a slow run still sends every record.
- Compression: a Kates record is a 28-byte header padded with zeros, which LOAD's default lz4 compresses to far less than 1 KiB, so the example sets `compressionType: none`, which needs approval.
- The plans' `sla` blocks: the targets set no recovery time, and a plan's P99 isn't the target's.
- Duplicates: whether the sign-off needs a PASS verdict or only `Lost` 0, since DUPLICATES_DETECTED loses nothing.
- Results: no number here was measured, and whether `krafter` meets the targets on `panda` is unknown until someone captures the runs.
