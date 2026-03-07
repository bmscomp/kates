# Appendix A: Glossary

Quick reference for terms used throughout this book.

| Term | Definition |
|------|------------|
| **ACK** | Acknowledgment from Kafka confirming a message has been persisted. Modes: `0` (fire-and-forget), `1` (leader only), `all` (all ISR replicas) |
| **ACL** | Access Control List — rules that grant or deny specific operations on Kafka resources (topics, groups, cluster) to specific principals |
| **AZ** | Availability Zone — an isolated failure domain within a data center or cloud region |
| **Baseline** | A reference measurement taken under normal conditions, used for comparison after changes or chaos injection |
| **Broker** | A Kafka server that stores partition data and serves produce/consume requests. In the krafter cluster, brokers run as dedicated pods separate from controllers |
| **CDC** | Change Data Capture — a pattern for tracking database changes as a stream of events |
| **Consumer Group** | A set of consumers that cooperatively read from topic partitions. Kafka assigns each partition to exactly one consumer within the group |
| **Consumer Lag** | The difference between the latest offset produced and the latest offset consumed. Indicates how far behind a consumer is |
| **Controller** | A KRaft node responsible for metadata management — leader election, partition assignment, and cluster coordination. Three controllers form the Raft quorum |
| **Coordinated Omission** | A measurement bias where slow responses prevent new requests from being issued, causing artificially low latency measurements |
| **CRC32** | A checksum algorithm used to verify message integrity. Kates can attach CRC32 checksums to test messages for corruption detection |
| **Cruise Control** | LinkedIn's open-source tool for automated Kafka partition rebalancing based on broker resource utilization |
| **Disruption** | A controlled fault injection — killing pods, partitioning networks, or stressing resources |
| **Drain Cleaner** | Strimzi component that intercepts Kubernetes node drain events and gracefully rolls Kafka pods instead of killing them |
| **Entity Operator** | Strimzi component running both the Topic Operator and User Operator in a single pod |
| **Game Day** | A structured chaos engineering session with defined hypotheses, controlled experiments, and documented findings |
| **gRPC** | Google Remote Procedure Call — a high-performance binary protocol using HTTP/2 and Protocol Buffers for service-to-service communication |
| **Heatmap** | A visualization showing the full latency distribution over time. Each row is one second; each column is a latency bucket |
| **Idempotency** | Kafka producer feature that deduplicates retried messages, preventing duplicates in the log |
| **ISR** | In-Sync Replicas — the set of replicas fully caught up with the partition leader. Writes require acknowledgment from all ISR members when `acks=all` |
| **JMX** | Java Management Extensions — the standard monitoring interface for JVM applications. Kafka exports metrics via JMX |
| **Kafka Exporter** | Strimzi component that exposes consumer lag and topic offset metrics not available through JMX |
| **KafkaNodePool** | Strimzi CRD for managing groups of Kafka nodes with the same role and configuration (e.g., broker pools per zone) |
| **KafkaTopic** | Strimzi CRD for declarative topic management — the Topic Operator reconciles these into actual Kafka topics |
| **KafkaUser** | Strimzi CRD for declarative user management — the User Operator creates SCRAM credentials and ACL rules |
| **Kind** | Kubernetes IN Docker — a tool for running local Kubernetes clusters using Docker containers as nodes |
| **KRaft** | Kafka Raft — Kafka's built-in consensus protocol that replaces ZooKeeper for metadata management |
| **LitmusChaos** | A Kubernetes-native chaos engineering framework that uses CRDs to define and manage chaos experiments |
| **mTLS** | Mutual TLS — both client and server present certificates for authentication. Used on the TLS listener (port 9093) |
| **NetworkPolicy** | Kubernetes resource that controls pod-to-pod network traffic. The kafka namespace uses default-deny with explicit allow rules |
| **P50 / P95 / P99** | Percentile latency metrics. P99 = 99% of requests completed within this time. P99 is more useful than averages for understanding worst-case behavior |
| **Partition** | A topic is divided into partitions for parallelism. Each partition is an ordered, immutable sequence of records |
| **PDB** | Pod Disruption Budget — Kubernetes resource limiting how many pods in a set can be unavailable simultaneously. Set to `maxUnavailable: 1` for Kafka |
| **Playbook** | A pre-defined YAML file describing a multi-step disruption scenario with safety parameters |
| **Protobuf** | Protocol Buffers — Google's language-neutral serialization format used by gRPC for message encoding |
| **PVC** | Persistent Volume Claim — a Kubernetes storage request that binds to a Persistent Volume |
| **Quorum** | The minimum number of Raft voters that must agree for a metadata operation to succeed. For 3 controllers, quorum = 2 |
| **Rebalance** | The process of redistributing partition assignments among consumers in a consumer group. During rebalancing, all consumers stop processing |
| **RF** | Replication Factor — the number of copies of each partition maintained across brokers |
| **RPO** | Recovery Point Objective — the maximum acceptable amount of data loss measured in time |
| **RTO** | Recovery Time Objective — the maximum acceptable time from failure to full recovery |
| **Scenario File** | A YAML/JSON file defining one or more test specifications with optional SLA validation gates (see [Chapter 13](13-scenario-files.md)) |
| **SCRAM-SHA-512** | Salted Challenge Response Authentication Mechanism — a password-based auth protocol used on the plain (9092) and external (9094) Kafka listeners |
| **Share Group** | Kafka 4.x feature (KIP-932) enabling queue-style competing-consumer semantics alongside traditional consumer groups |
| **SLA** | Service Level Agreement — quantitative targets for system behavior (e.g., "P99 latency ≤ 50ms") |
| **Sparkline** | A compact inline chart showing a trend over time, rendered as Unicode block characters in the terminal |
| **SSE** | Server-Sent Events — a protocol for streaming real-time updates from server to client over HTTP |
| **Strimzi** | A Kubernetes operator for managing Apache Kafka clusters declaratively via CRDs |
| **StrimziPodSet** | Strimzi's replacement for StatefulSets — provides finer-grained control over pod lifecycle and rolling updates |
| **Throughput** | The rate of successful message delivery, typically measured in records/second or MB/second |
| **Tiered Storage** | Kafka feature (KIP-405) that offloads cold log segments to object storage (e.g., S3/MinIO), reducing local disk requirements |
| **Velero** | Kubernetes backup tool used for daily PVC snapshots and pre-upgrade backups of the Kafka namespace |
