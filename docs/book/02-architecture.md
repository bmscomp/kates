# Chapter 2: Architecture & Design

KATES is composed of four major subsystems: the **backend engine**, the **CLI**, the **infrastructure layer**, and the **observability stack**. This chapter explains how they fit together.

## High-Level Architecture

```mermaid
graph TB
    subgraph CLI["KATES CLI (Go)"]
        CLI1[test / report / disruption]
        CLI2[trend / dashboard / top]
        CLI3[cluster / health / scaffold]
    end
    
    subgraph Backend["KATES Backend (Quarkus / Java)"]
        API[REST API]
        ORCH[TestOrchestrator]
        ENG[Benchmark Engine]
        CHAOS[Disruption Orchestrator]
        RPT[Report Generator]
        EXP[Exporters]
    end
    
    subgraph Infra["Kubernetes Cluster"]
        K8S[Kubernetes API]
        KAFKA[Kafka Brokers]
        PROM[Prometheus]
        GRAF[Grafana]
        LIT[LitmusChaos]
    end
    
    CLI1 --> API
    CLI2 --> API
    CLI3 --> API
    API --> ORCH
    ORCH --> ENG
    ORCH --> CHAOS
    ORCH --> RPT
    RPT --> EXP
    CHAOS --> K8S
    CHAOS --> LIT
    ENG --> KAFKA
    PROM --> KAFKA
    GRAF --> PROM
```

## Backend Engine

The backend is a **Quarkus application** running in JVM mode (with native image support via GraalVM). It exposes a REST API and manages the full test lifecycle.

### Component Map

```mermaid
graph LR
    subgraph Domain
        TR[TestRun]
        TS[TestSpec]
        TT[TestType]
        TRes[TestResult]
    end
    
    subgraph Engine
        TO[TestOrchestrator]
        NKB[NativeKafkaBackend]
        LH[LatencyHistogram]
        BS[BenchmarkStatus]
        BM[BenchmarkMetrics]
    end
    
    subgraph Report
        RG[ReportGenerator]
        CS[ClusterSnapshot]
        BrokerM[BrokerMetrics]
        SLA[SlaVerdict]
    end
    
    subgraph Export
        CSV[CsvExporter]
        JUnit[JunitXmlExporter]
        HM[HeatmapExporter]
    end
    
    TO --> NKB
    TO --> RG
    NKB --> LH
    NKB --> BS
    RG --> CS
    RG --> BrokerM
    RG --> SLA
    RG --> CSV
    RG --> JUnit
    RG --> HM
```

### TestOrchestrator

The `TestOrchestrator` is the central coordinator. When a test is created, it:

1. **Resolves defaults** — merges the incoming `TestSpec` with `TestTypeDefaults` for the chosen test type
2. **Creates the topic** — ensures the Kafka topic exists with the required partition count and replication factor
3. **Launches workers** — delegates to the `BenchmarkBackend` to start producer/consumer tasks
4. **Polls status** — periodically polls each `BenchmarkHandle` for `BenchmarkStatus` updates
5. **Collects heatmap data** — captures per-second latency bucket distributions during polling
6. **Generates reports** — produces a `TestReport` with summary metrics, SLA verdicts, and broker correlation

### NativeKafkaBackend

The in-process Kafka benchmark engine. Each test launches `WorkerState` threads that:

- **Produce** messages with configurable record size, acknowledgment mode, and throughput throttling
- **Consume** messages with configurable consumer group, fetch settings, and poll timeout
- **Record latency** in a lock-free `LatencyHistogram` (1024 logarithmic buckets, microsecond precision)
- **Track integrity** — sequence numbers, acknowledgment gaps, and consumer-side deduplication

### LatencyHistogram

The histogram is the heart of latency measurement. It uses **logarithmic bucketing** to provide high resolution at low latencies (sub-millisecond) while covering tails up to 10+ seconds.

```mermaid
graph LR
    subgraph Internal["1024 Logarithmic Buckets"]
        B1["0.001ms"] --> B2["0.01ms"] --> B3["0.1ms"] --> B4["1ms"] --> B5["10ms"] --> B6["100ms"] --> B7["1000ms"]
    end
    
    subgraph Export["25 Heatmap Buckets"]
        H1["0–0.1ms"]
        H2["0.1–0.5ms"]
        H3["0.5–1ms"]
        H4["1–5ms"]
        H5["5–50ms"]
        H6["50–500ms"]
        H7["500ms–10s"]
    end
    
    Internal -->|exportBuckets| Export
```

Key methods:

| Method | Lock | Purpose |
|--------|------|---------|
| `record(latencyUs)` | Read | Record a single latency observation |
| `getPercentile(p)` | Read | Compute p50/p95/p99 from cumulative distribution |
| `exportBuckets()` | Read | Compress to 25 heatmap ranges (non-destructive) |
| `snapshotAndReset()` | Write | Atomic capture + reset for windowed collection |

## Disruption Engine

The disruption subsystem provides **Kubernetes-native chaos injection** with Kafka awareness.

```mermaid
graph TB
    subgraph Control
        DO[DisruptionOrchestrator]
        DSG[DisruptionSafetyGuard]
        DPC[DisruptionPlaybookCatalog]
    end
    
    subgraph Intelligence
        KIS[KafkaIntelligenceService]
        ISR[ISR Tracking]
        LAG[Consumer Lag]
        LEAD[Leader Resolution]
    end
    
    subgraph Providers
        HCP[HybridChaosProvider]
        KCP[KubernetesChaosProvider]
        LCP[LitmusChaosProvider]
    end
    
    subgraph Reporting
        DR[DisruptionReport]
        SG[SlaGrader]
        PMC[PrometheusMetricsCapture]
    end
    
    DO --> DSG
    DO --> KIS
    DO --> HCP
    DO --> DR
    DPC --> DO
    KIS --> ISR
    KIS --> LAG
    KIS --> LEAD
    HCP --> KCP
    HCP --> LCP
    DR --> SG
    DR --> PMC
```

### Disruption Types

| Type | Description | Implementation |
|------|-------------|----------------|
| `POD_KILL` | Immediately terminate a broker pod | `kubectl delete pod --grace-period=0` |
| `POD_DELETE` | Gracefully delete a broker pod | `kubectl delete pod` |
| `NETWORK_PARTITION` | Isolate a broker from the cluster | Litmus `pod-network-partition` |
| `NETWORK_LATENCY` | Inject latency into broker network | Litmus `pod-network-latency` |
| `CPU_STRESS` | Saturate CPU on a broker node | Litmus `pod-cpu-hog` |
| `DISK_FILL` | Fill the broker's persistent volume | Litmus `disk-fill` |
| `ROLLING_RESTART` | Restart all brokers sequentially | Kubernetes rolling update |
| `LEADER_ELECTION` | Force leader re-election for a partition | Kill the current leader broker |
| `SCALE_DOWN` | Reduce the number of broker replicas | Strimzi scale operation |
| `NODE_DRAIN` | Drain a Kubernetes node | `kubectl drain` |

### Safety Guardrails

The `DisruptionSafetyGuard` validates every disruption plan before execution:

- **Maximum affected brokers** — prevents killing more than N brokers simultaneously
- **ISR health check** — refuses to proceed if partitions are already under-replicated
- **Quorum protection** — ensures the KRaft metadata quorum maintains majority
- **Auto-rollback** — monitors cluster health during execution and rolls back if thresholds are breached

## CLI Architecture

The CLI is a **standalone Go binary** built with Cobra. It communicates with the backend exclusively through the REST API.

```mermaid
graph TD
    subgraph Config
        CTX[~/.kates.yaml]
        CTXM[Context Manager]
    end
    
    subgraph Commands
        TEST[test create/list/get/delete/watch/apply/scaffold]
        REPORT[report show/summary/export/compare/diff/brokers]
        DISRUPT[disruption run/list/status/timeline/types/kafka-metrics]
        RESIL[resilience run]
        TREND[trend]
        OPS[health/cluster/top/dashboard/status]
    end
    
    subgraph Output
        TABLE[Table Renderer]
        JSON[JSON Printer]
        SPARK[Sparkline Charts]
        BADGE[Status Badges]
        BAR[Metric Bars]
    end
    
    CTX --> CTXM
    CTXM --> Commands
    Commands --> TABLE
    Commands --> JSON
    Commands --> SPARK
```

Key design decisions:

- **Multi-context support** — like `kubectl`, the CLI supports named contexts for targeting different KATES instances
- **Rich terminal output** — tables, colored badges, metric bars, sparkline charts, and ASCII banners
- **Scaffold templates** — `kates test scaffold --type LOAD` generates ready-to-use YAML scenario files
- **Streaming watch** — `kates test watch` and `kates disruption watch` provide real-time progress updates

## Data Flow

This diagram traces a complete test execution from CLI command to final report:

```mermaid
sequenceDiagram
    participant CLI as KATES CLI
    participant API as REST API
    participant Orch as TestOrchestrator
    participant Engine as NativeKafkaBackend
    participant Kafka as Kafka Cluster
    participant Hist as LatencyHistogram
    participant Report as ReportGenerator
    
    CLI->>API: POST /api/tests {type: LOAD, spec: {...}}
    API->>Orch: createTest(type, spec)
    Orch->>Kafka: Create topic (if needed)
    Orch->>Engine: start(handles)
    Engine->>Kafka: Produce messages
    Engine->>Hist: record(latencyUs)
    
    loop Every poll interval
        Orch->>Engine: poll(handle)
        Engine->>Hist: exportBuckets()
        Engine-->>Orch: BenchmarkStatus + heatmapBuckets
        Orch->>Orch: Accumulate heatmap rows
    end
    
    CLI->>API: GET /api/tests/{id}
    API->>Orch: getTest(id)
    Orch-->>CLI: TestRun (status, results)
    
    CLI->>API: GET /api/tests/{id}/report
    API->>Report: generate(testRun)
    Report->>Kafka: captureSnapshot (broker metrics)
    Report-->>CLI: TestReport (summary, SLA, brokers)
```

## Technology Stack

| Component | Technology | Version | Purpose |
|-----------|-----------|---------|---------|
| Backend | Quarkus | 3.x | REST framework, CDI, native compilation |
| Runtime | Java | 21+ | Virtual threads, modern GC |
| Build | Maven | 3.x | Backend build system |
| CLI | Go | 1.22+ | Cross-platform binary |
| CLI Framework | Cobra | Latest | Command parsing, help generation |
| Cluster | Kind | Latest | Local Kubernetes simulation |
| Kafka | Apache Kafka | 4.1.1 | KRaft mode, no ZooKeeper |
| Operator | Strimzi | 0.49.1 | Kafka lifecycle management |
| Chaos | LitmusChaos | Latest | Advanced chaos experiments |
| Monitoring | Prometheus + Grafana | Latest | Metrics collection and visualization |
| Registry | Apicurio | Latest | Schema registry for Kafka |
| Database | PostgreSQL | Latest | Test results and schedule persistence |

## Data Model

KATES uses PostgreSQL for persistent storage. The schema is managed by Flyway migrations in `kates/src/main/resources/db/migration/`.

```mermaid
erDiagram
    test_runs ||--o{ test_results : "has many"
    
    test_runs {
        varchar id PK
        varchar test_type
        varchar status
        timestamptz created_at
        varchar backend
        varchar scenario_name
        text spec_json
        text sla_json
        text labels_json
    }
    
    test_results {
        bigserial id PK
        varchar test_run_id FK
        varchar test_type
        varchar status
        bigint records_sent
        double throughput_rec_per_sec
        double avg_latency_ms
        double p50_latency_ms
        double p95_latency_ms
        double p99_latency_ms
        double max_latency_ms
        varchar phase_name
    }
    
    scheduled_test_runs {
        varchar id PK
        varchar name
        varchar cron_expression
        boolean enabled
        text request_json
        varchar last_run_id
        timestamptz created_at
    }
    
    disruption_reports {
        varchar id PK
        varchar plan_name
        varchar status
        varchar sla_grade
        timestamptz created_at
        text report_json
    }
    
    disruption_schedules {
        varchar id PK
        varchar name
        varchar cron_expression
        boolean enabled
        varchar playbook_name
        text plan_json
        varchar last_run_id
        timestamptz created_at
    }
```

### Migration History

| Version | File | Purpose |
|:---:|------|---------|
| V1 | `V1__create_test_tables.sql` | `test_runs` + `test_results` with indexes on type, status, created_at |
| V2 | `V2__create_schedules_table.sql` | `scheduled_test_runs` for recurring test automation |
| V3 | `V3__create_disruption_reports.sql` | `disruption_reports` with SLA grade tracking |
| V4 | `V4__create_disruption_schedules.sql` | `disruption_schedules` for recurring chaos tests |
