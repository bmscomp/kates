# Appendix A: Glossary

Quick reference for terms used throughout this book.

| Term | Definition |
|------|------------|
| **ACK** | Acknowledgment from Kafka confirming a message has been persisted. Modes: `0` (fire-and-forget), `1` (leader only), `all` (all ISR replicas) |
| **AZ** | Availability Zone — an isolated failure domain within a data center or cloud region |
| **Baseline** | A reference measurement taken under normal conditions, used for comparison after changes or chaos injection |
| **Broker** | A single Kafka server that stores and serves topic partitions |
| **CDC** | Change Data Capture — a pattern for tracking database changes as a stream of events |
| **Consumer Group** | A set of consumers that cooperatively read from topic partitions. Kafka assigns each partition to exactly one consumer within the group |
| **Consumer Lag** | The difference between the latest offset produced and the latest offset consumed. Indicates how far behind a consumer is |
| **Coordinated Omission** | A measurement bias where slow responses prevent new requests from being issued, causing artificially low latency measurements |
| **CRC32** | A checksum algorithm used to verify message integrity. Kates can attach CRC32 checksums to test messages for corruption detection |
| **Disruption** | A controlled fault injection — killing pods, partitioning networks, or stressing resources |
| **Game Day** | A structured chaos engineering session with defined hypotheses, controlled experiments, and documented findings |
| **Heatmap** | A visualization showing the full latency distribution over time. Each row is one second; each column is a latency bucket |
| **Idempotency** | Kafka producer feature that deduplicates retried messages, preventing duplicates in the log |
| **ISR** | In-Sync Replicas — the set of replicas that are fully caught up with the partition leader. Writes require acknowledgment from all ISR members when `acks=all` |
| **JMX** | Java Management Extensions — the standard monitoring interface for JVM applications. Kafka exports metrics via JMX |
| **KRaft** | Kafka Raft — Kafka's built-in consensus protocol that replaces ZooKeeper for metadata management |
| **Kind** | Kubernetes IN Docker — a tool for running local Kubernetes clusters using Docker containers as nodes |
| **LitmusChaos** | A Kubernetes-native chaos engineering framework that uses CRDs to define and manage chaos experiments |
| **P50 / P95 / P99** | Percentile latency metrics. P99 = 99% of requests completed within this time. P99 is more useful than averages for understanding worst-case behavior |
| **Partition** | A topic is divided into partitions for parallelism. Each partition is an ordered, immutable sequence of records |
| **Playbook** | A pre-defined YAML file describing a multi-step disruption scenario with safety parameters |
| **PVC** | Persistent Volume Claim — a Kubernetes storage request that binds to a Persistent Volume |
| **Rebalance** | The process of redistributing partition assignments among consumers in a consumer group. During rebalancing, all consumers stop processing |
| **RF** | Replication Factor — the number of copies of each partition maintained across brokers |
| **RPO** | Recovery Point Objective — the maximum acceptable amount of data loss measured in time |
| **RTO** | Recovery Time Objective — the maximum acceptable time from failure to full recovery |
| **Scenario File** | A YAML/JSON file defining one or more test specifications with optional SLA validation gates (see [Chapter 13](13-scenario-files.md)) |
| **SLA** | Service Level Agreement — quantitative targets for system behavior (e.g., "P99 latency ≤ 50ms") |
| **Sparkline** | A compact inline chart showing a trend over time, rendered as Unicode block characters in the terminal |
| **SSE** | Server-Sent Events — a protocol for streaming real-time updates from server to client over HTTP |
| **Strimzi** | A Kubernetes operator for managing Apache Kafka clusters declaratively via CRDs |
| **Throughput** | The rate of successful message delivery, typically measured in records/second or MB/second |
