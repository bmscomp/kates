# KATES Roadmap

> Kafka Advanced Testing & Engineering Suite

---

## ✅ Completed Features

### Core Platform
- **Quarkus Backend API** — REST API with PostgreSQL persistence, OpenAPI annotations, and native image support
- **Go CLI** — Full-featured terminal client with context management, dashboards, sparklines, and export capabilities
- **One-Command Setup** — `make all` brings up the entire stack in a single 10-step pipeline

### Performance Testing Engine
- **8 Test Types** — LOAD, STRESS, SPIKE, ENDURANCE, VOLUME, CAPACITY, ROUND_TRIP, INTEGRITY
- **Declarative Scenario Files** — YAML test definitions with SLA gates for CI/CD integration
- **Test Execution & Management** — Create, list, filter, inspect, and delete test runs
- **SLA Assertion Engine** — Automated pass/fail grading against defined thresholds
- **Strategy-Based Execution** — Routing different workload types to specialized benchmark services

### Reporting & Analytics
- **Report Generation** — Full reports with SLA verdicts and condensed summaries
- **Export Formats** — CSV and JUnit XML export for CI integration
- **Diff Comparison** — Side-by-side comparison of two test runs
- **Trend Analysis** — Historical metric trends with terminal sparklines (P99, throughput)
- **PDF Report Export** — Professional, audit-ready PDF generation for compliance reports

### Chaos Engineering
- **LitmusChaos Integration** — 6 built-in playbooks with safety guardrails
- **Resilience Testing** — Chaos-performance correlation analysis
- **Disruption Engine** — Kafka-aware fault injection with streaming status updates

### Cluster Inspection
- **Cluster Metadata** — Brokers, controller info, rack/AZ distribution
- **Topic Management** — List, describe, create, alter config, and delete topics
- **Consumer Groups** — Group listing with state, members, per-partition lag
- **Broker Configuration** — Non-default config inspection grouped by source
- **Health Check** — Comprehensive cluster health assessment

### Kafka Client
- **Interactive CLI** — `kates kafka` command suite with colour-coded output
- **Produce & Consume** — Produce records and tail topics like a log viewer
- **Topic CRUD** — Create, alter config, and delete topics from the terminal
- **Interactive TUI** — Full-screen Kafka explorer with Bubble Tea (tabs, search, consumer tail)

### Scheduling
- **Recurring Tests** — Cron-based test scheduling with CRUD management

### Observability
- **Live Dashboard** — Full-screen terminal monitoring dashboard
- **Top View** — Live view of running tests (like `kubectl top`)
- **Watch Mode** — Real-time streaming of a running test
- **Prometheus & Grafana** — 5 custom auto-provisioned dashboards:
  - Kafka Complete Monitoring
  - Kafka Cluster Health
  - Kafka Performance Metrics
  - Kafka Performance Test Results
  - Kafka JVM Metrics

### Infrastructure
- **Kind-Based Kubernetes** — Local cluster with production parity (`panda`)
- **Multi-AZ Simulation** — 3 nodes labeled `alpha`, `sigma`, `gamma` with rack awareness
- **Strimzi Kafka (KRaft)** — 3-broker cluster, no ZooKeeper dependency
- **Apicurio Schema Registry** — Connected to Kafka for schema management
- **Offline-First Image Management** — Pull-once strategy, `imagePullPolicy: Never`
- **Velero Backup** — Cluster backup and restore capability
- **Zone-Specific Storage Classes** — Multi-AZ local-path StorageClasses

### Documentation
- **The Definitive Guide** — 14-chapter book covering architecture, theory, test types, chaos engineering, observability, CLI/API reference, deployment, and recipes
- **Tutorials** — 6 hands-on tutorials from first test to CI/CD integration
- **OpenAPI Annotations** — Self-documenting API endpoints via `quarkus-smallrye-openapi`

---

## 🔮 Upcoming Features

### Near-Term
- **Health Canada Integration** — Recalls and safety alerts API for medical device data
- **Shimmer/Skeleton Loading States** — Replace spinners with shimmer placeholder cards in the Flutter UI
- **Request Cancellation** — Dio `CancelToken` integration with Riverpod lifecycle
- **Dual-Axis Charts** — P99 latency plotting with a right-side Y-axis on throughput charts
- **Workload Detail Page** — Run history and specification YAML viewer

### Mid-Term
- **Model Code Generation** — `freezed` + `json_serializable` for immutable Dart models
- **Widget Test Coverage** — Automated tests for core Flutter components (`MetricCard`, `AppShell`)
- **Granular Model Files** — Split monolithic `models.dart` into domain-specific files
- **OpenTelemetry Tracing** — Distributed tracing across the entire platform

### Long-Term
- **Multi-Cluster Federation** — Stretched clusters, active-active MirrorMaker 2, hub-spoke topologies
- **Dynamic KRaft Quorum Management** — Runtime quorum scaling without cluster restart
- **Share Groups Support** — Native support for Kafka Share Groups (KIP-932)
- **Backup & Restore Automation** — Velero CSI snapshots, MirrorMaker 2 DR, S3 archival
- **Native Image Production Builds** — GraalVM native binary for the backend
