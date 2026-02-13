# KATES — Kafka Advanced Testing & Engineering Suite

KATES is a Quarkus-based backend service that orchestrates Kafka performance tests using pluggable execution backends. It provides a REST API for running, monitoring, and analyzing benchmarks against a Strimzi-managed Kafka cluster.

## Why KATES?

Performance testing Kafka typically involves writing shell scripts around `kafka-producer-perf-test.sh` and `kafka-consumer-perf-test.sh`. This approach suffers from several limitations:

- **No orchestration** — scripts run independently with no coordination between producers and consumers
- **Limited scenarios** — shell scripts cannot model complex test patterns like stress ramp-ups, spike bursts, or capacity probing
- **No persistence** — results are ephemeral, written to stdout and lost unless manually captured
- **No API** — impossible to integrate with dashboards or CI/CD pipelines

KATES solves these problems by providing a structured, API-driven test execution engine with pluggable backends and per-test-type configuration.

## Architecture

```
┌──────────────────┐      REST        ┌──────────────────┐
│   Client / UI    │ ───────────────► │    Kates API     │
│   (curl, Flutter)│ ◄─────────────── │  (Quarkus REST)  │
└──────────────────┘                  └────────┬─────────┘
                                               │
                                    ┌──────────┴─────────┐
                                    │  TestOrchestrator   │
                                    │  + TestTypeDefaults │
                                    └──────────┬─────────┘
                                               │
                              ┌────────────────┼────────────────┐
                              │                │                │
                       ┌──────▼──────┐  ┌──────▼──────┐ ┌──────▼────────┐
                       │   Native    │  │   Trogdor   │ │  Kafka Admin  │
                       │   Backend   │  │   Backend   │ │    Client     │
                       │ (in-process)│  │ (REST API)  │ │ (AdminClient) │
                       └──────┬──────┘  └──────┬──────┘ └──────┬────────┘
                              │                │               │
                              └───────┬────────┘        ┌──────▼────────┐
                                      │                 │ Kafka Cluster │
                                      ▼                 │  (Strimzi)    │
                              ┌───────────────┐         └───────────────┘
                              │ Kafka Cluster │
                              └───────────────┘
```

### How It Works

1. **Client** sends a `POST /api/tests` request with a test type, optional parameters, and optional backend
2. **TestOrchestrator** applies per-test-type defaults from `TestTypeDefaults`, merging with any user-supplied values
3. **TestOrchestrator** creates the necessary Kafka topics via `KafkaAdminService`
4. **TestOrchestrator** selects the execution backend (native or trogdor) and builds backend-agnostic `BenchmarkTask` objects
5. The chosen **BenchmarkBackend** executes the actual workloads against the Kafka cluster
6. **KATES** polls the backend for status updates and aggregates results

### Execution Backends

| Backend | Description | When to Use |
|---------|-------------|-------------|
| `native` | In-process Kafka clients using virtual threads | Development, CI, no external dependencies |
| `trogdor` | Apache Trogdor Coordinator/Agent framework | Production, distributed load generation |

## Quick Start

### Prerequisites

- Java 21+
- Maven 3.8+
- Access to a Kafka cluster (default: `krafter-kafka-bootstrap.kafka.svc:9092`)

### Build and Run

```bash
cd kates

# Build
mvn clean package -DskipTests

# Run in dev mode (hot reload, Swagger UI)
mvn quarkus:dev

# Run tests
mvn test
```

### Dev Mode Features

- **Swagger UI**: http://localhost:8080/q/swagger-ui
- **Health**: http://localhost:8080/q/health
- **OpenAPI spec**: http://localhost:8080/q/openapi

## Configuration

### Core Settings

| Property | Default | Description |
|----------|---------|-------------|
| `kates.kafka.bootstrap-servers` | `krafter-kafka-bootstrap.kafka.svc:9092` | Kafka bootstrap servers |
| `kates.trogdor.coordinator-url` | `http://trogdor-coordinator:8889` | Trogdor Coordinator REST endpoint |
| `kates.engine.default-backend` | `native` | Default execution backend (`native` or `trogdor`) |

### Per-Test-Type Defaults

Each test type has its own set of configurable defaults. These are set via properties like `kates.tests.{type}.{param}` or via ConfigMap environment variables like `KATES_TESTS_{TYPE}_{PARAM}`.

| Parameter | Global Default | Description |
|-----------|---------------|-------------|
| `partitions` | `3` | Topic partition count |
| `replication-factor` | `3` | Topic replication factor |
| `min-insync-replicas` | `2` | min.insync.replicas topic config |
| `acks` | `all` | Producer acknowledgment mode |
| `batch-size` | `65536` | Producer batch.size (bytes) |
| `linger-ms` | `5` | Producer linger.ms |
| `compression-type` | `lz4` | Producer compression codec |
| `record-size` | `1024` | Default message size (bytes) |
| `num-records` | `1000000` | Total messages to produce |
| `throughput` | `-1` | Target messages/sec (-1 = unlimited) |
| `duration-ms` | `600000` | Test duration (ms) |
| `num-producers` | `1` | Concurrent producer tasks |
| `num-consumers` | `1` | Concurrent consumer tasks |

Override per type in `application.properties`:

```properties
kates.tests.stress.partitions=6
kates.tests.stress.num-producers=3
kates.tests.volume.record-size=10240
kates.tests.roundtrip.compression-type=none
```

Or via ConfigMap environment variables:

```yaml
KATES_TESTS_STRESS_PARTITIONS: "6"
KATES_TESTS_STRESS_NUM_PRODUCERS: "3"
KATES_TESTS_VOLUME_RECORD_SIZE: "10240"
KATES_TESTS_ROUNDTRIP_COMPRESSION_TYPE: "none"
```

See [Deployment Guide](deployment.md) for full ConfigMap reference.
