# Architecture

This document describes the internal architecture of the KATES backend, covering package structure, class responsibilities, and the test execution lifecycle.

## Package Structure

```
com.klster.kates
├── api/                  REST endpoints (JAX-RS resources)
│   ├── TestResource      POST/GET/DELETE /api/tests, GET /api/tests/backends
│   ├── ClusterResource   GET /api/cluster/*
│   └── HealthResource    GET /api/health (backend-aware, per-type config)
│
├── config/               Configuration management
│   └── TestTypeDefaults  Per-test-type configurable defaults (CDI bean)
│
├── domain/               Core data model
│   ├── TestType           Enum: LOAD, STRESS, SPIKE, ENDURANCE, VOLUME, CAPACITY, ROUND_TRIP
│   ├── TestSpec           Configurable test parameters
│   ├── TestResult         Task-level metrics and status
│   ├── TestRun            Lifecycle entity (spec → results, tracks backend used)
│   └── CreateTestRequest  API request DTO (type, spec, backend)
│
├── engine/               Pluggable execution backends
│   ├── BenchmarkBackend   SPI interface (submit, poll, stop)
│   ├── BenchmarkTask      Backend-agnostic workload descriptor (builder pattern)
│   ├── BenchmarkHandle    Opaque task identifier returned by backends
│   ├── BenchmarkStatus    Unified task status + metrics snapshot
│   ├── BenchmarkException Common exception type for backend failures
│   ├── NativeKafkaBackend In-process execution using Kafka clients + virtual threads
│   ├── TrogdorBackend     Adapter wrapping TrogdorClient + SpecFactory
│   └── TestOrchestrator   Routes execution to backends, applies per-type defaults
│
├── trogdor/              Trogdor Coordinator integration
│   ├── TrogdorClient      REST client interface (@RegisterRestClient)
│   ├── SpecFactory        TestType → TrogdorSpec builder
│   └── spec/
│       ├── TrogdorSpec        Base class (class, startMs, durationMs)
│       ├── ProduceBenchSpec   Producer benchmark
│       ├── ConsumeBenchSpec   Consumer benchmark
│       └── RoundTripWorkloadSpec  End-to-end latency
│
└── service/              Business logic
    ├── TestExecutionService   Legacy orchestration (Trogdor-only)
    ├── TestRunRepository      In-memory storage
    └── KafkaAdminService      Topic management (AdminClient)
```

## Test Execution Lifecycle

The `TestOrchestrator` orchestrates the complete lifecycle of a test run using pluggable backends:

```
                 POST /api/tests
                   (type, spec, backend?)
                       │
                       ▼
              ┌─────────────────┐
              │ Apply per-type  │  TestTypeDefaults.forType(type)
              │ defaults to spec│  Merge with user-supplied values
              └────────┬────────┘
                       │
                       ▼
              ┌─────────────────┐
              │ Create TestRun  │  Status: PENDING
              │ Save to repo    │
              └────────┬────────┘
                       │
                       ▼
              ┌─────────────────┐
              │ Create topics   │  KafkaAdminService.createTopic()
              │ via AdminClient │
              └────────┬────────┘
                       │
                       ▼
              ┌─────────────────┐
              │ Build backend-  │  buildTasks() → List<BenchmarkTask>
              │ agnostic tasks  │
              └────────┬────────┘
                       │
                       ▼
              ┌─────────────────┐
              │ Resolve backend │  "native" or "trogdor"
              │ Submit tasks    │  backend.submit(task) → BenchmarkHandle
              └────────┬────────┘  Status: RUNNING
                       │
            ┌──────────┴─────────┐
            │                    │
      ┌─────▼─────┐      ┌──────▼──────┐
      │   Task     │      │   Task      │
      │ submitted  │      │   failed    │
      │ (RUNNING)  │      │  (FAILED)   │
      └─────┬──────┘      └─────────────┘
            │
            ▼
  ┌──────────────────┐
  │ GET /api/tests/  │  Triggers refreshStatus()
  │     {id}         │  backend.poll(handle) → BenchmarkStatus
  └────────┬─────────┘
           │
           ▼
  ┌──────────────────┐
  │ Aggregate status │  All DONE → run DONE
  │ across tasks     │  Any FAILED → run FAILED
  └──────────────────┘
```

## Per-Test-Type Configuration

The `TestTypeDefaults` CDI bean provides per-test-type configuration with a three-tier resolution:

```
Priority 1 (highest): ConfigMap env vars    → KATES_TESTS_STRESS_PARTITIONS=12
Priority 2:          application.properties → kates.tests.stress.partitions=6
Priority 3 (lowest): @ConfigProperty default → built-in "6" in Java
```

Each test type has tuned defaults:

| Type | Key Overrides |
|------|---------------|
| STRESS | 6 partitions, 131KB batch, 3 producers |
| SPIKE | acks=1, no compression, linger=0 |
| ENDURANCE | throughput=5000, duration=1h |
| VOLUME | 10KB records, 256KB batch, linger=50ms |
| CAPACITY | 12 partitions, 5 producers, duration=20min |
| ROUND_TRIP | 16KB batch, no compression, throughput=10000 |

The `TestOrchestrator.applyTypeDefaults()` method merges type defaults with user-supplied spec values. User-provided values in the API request always take priority.

## Key Design Decisions

### Pluggable Backends (BenchmarkBackend SPI)

The `BenchmarkBackend` interface defines a Service Provider Interface for execution engines:

```java
public interface BenchmarkBackend {
    String name();
    BenchmarkHandle submit(BenchmarkTask task);
    BenchmarkStatus poll(BenchmarkHandle handle);
    void stop(BenchmarkHandle handle);
}
```

This enables running tests without external dependencies (native backend) or with distributed load generation (trogdor backend). New backends can be added by implementing this interface and annotating with `@ApplicationScoped @Named("mybackend")`.

### Backend-Agnostic Task Model

`BenchmarkTask` and `BenchmarkStatus` provide a unified contract that abstracts away backend-specific details. The orchestrator builds tasks once, and the chosen backend translates them to its native format.

### Trogdor Specs as POJOs

Rather than depending on the Trogdor library JAR, KATES models Trogdor's JSON specification format using plain Java objects with Jackson annotations. This avoids dependency conflicts, tight coupling, and size bloat.

### In-Memory Repository

`TestRunRepository` uses a `ConcurrentHashMap` for simplicity. Test runs are ephemeral — they exist only during execution. A database-backed repository can be added later if required.

### ConfigMap-Driven Configuration

All test parameters are exposed as environment variables in the Kubernetes ConfigMap `kates-config`. MicroProfile Config automatically maps `KATES_TESTS_STRESS_PARTITIONS` → `kates.tests.stress.partitions`, allowing operators to tune test defaults without rebuilding the application.

## Technology Stack

| Component | Version | Purpose |
|-----------|---------|---------|
| Quarkus | 3.31.3 | Application framework |
| Java | 21 | Language runtime (virtual threads for native backend) |
| Kafka Clients | 4.1.1 | AdminClient for topic management, native backend execution |
| Jackson | (managed) | JSON serialization/deserialization |
| MicroProfile REST Client | (managed) | Trogdor REST API integration |
| MicroProfile Config | (managed) | ConfigMap / env var / properties resolution |
| SmallRye Health | (managed) | Health check probes |
| SmallRye OpenAPI | (managed) | Swagger UI and OpenAPI spec |
| JUnit 5 | (managed) | Testing framework |
| Mockito | (managed) | Mock injection for integration tests |
| REST Assured | (managed) | HTTP endpoint testing |
