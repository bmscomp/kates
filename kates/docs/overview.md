# KATES — Kafka Advanced Testing & Engineering Suite

KATES is a Quarkus-based backend service that orchestrates Kafka performance tests using Apache Trogdor. It provides a REST API for running, monitoring, and analyzing benchmarks against a Strimzi-managed Kafka cluster.

## Why KATES?

Performance testing Kafka typically involves writing shell scripts around `kafka-producer-perf-test.sh` and `kafka-consumer-perf-test.sh`. This approach suffers from several limitations:

- **No orchestration** — scripts run independently with no coordination between producers and consumers
- **Limited scenarios** — shell scripts cannot model complex test patterns like stress ramp-ups, spike bursts, or capacity probing
- **No persistence** — results are ephemeral, written to stdout and lost unless manually captured
- **No API** — impossible to integrate with dashboards or CI/CD pipelines

KATES solves these problems by providing a structured, API-driven test execution engine backed by Apache Trogdor's distributed framework.

## Architecture

```
┌──────────────────┐      REST        ┌──────────────────┐
│   Client / UI    │ ───────────────► │    Kates API     │
│   (curl, Flutter)│ ◄─────────────── │  (Quarkus REST)  │
└──────────────────┘                  └────────┬─────────┘
                                               │
                                    ┌──────────┴─────────┐
                                    │                    │
                              ┌─────▼──────┐    ┌───────▼────────┐
                              │  Trogdor   │    │  Kafka Admin   │
                              │ Coordinator│    │    Client      │
                              │ (REST API) │    │  (AdminClient) │
                              └─────┬──────┘    └───────┬────────┘
                                    │                   │
                              ┌─────▼──────┐    ┌───────▼────────┐
                              │  Trogdor   │    │ Kafka Cluster  │
                              │   Agent(s) │    │  (Strimzi)     │
                              └────────────┘    └────────────────┘
```

### How It Works

1. **Client** sends a `POST /api/tests` request with a test type and optional parameters
2. **TestExecutionService** creates the necessary Kafka topics via `AdminClient`
3. **SpecFactory** translates the test type into one or more Trogdor task specifications
4. **TrogdorClient** submits each spec to the Trogdor Coordinator via REST
5. Trogdor Agents execute the actual producer/consumer workloads against the Kafka cluster
6. **KATES** polls the Coordinator for status updates and aggregates results

## Quick Start

### Prerequisites

- Java 21+
- Maven 3.8+
- Access to a Kafka cluster (default: `krafter-kafka-bootstrap.kafka.svc:9092`)
- A running Trogdor Coordinator (default: `http://trogdor-coordinator:8889`)

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

All settings live in `src/main/resources/application.properties`:

| Property | Default | Description |
|----------|---------|-------------|
| `kates.kafka.bootstrap-servers` | `krafter-kafka-bootstrap.kafka.svc:9092` | Kafka bootstrap servers |
| `kates.trogdor.coordinator-url` | `http://trogdor-coordinator:8889` | Trogdor Coordinator REST endpoint |
| `kates.defaults.replication-factor` | `3` | Default topic replication factor |
| `kates.defaults.partitions` | `3` | Default topic partition count |
| `kates.defaults.min-insync-replicas` | `2` | Default min.insync.replicas |
| `kates.defaults.acks` | `all` | Producer acknowledgment mode |
| `kates.defaults.batch-size` | `65536` | Producer batch.size (bytes) |
| `kates.defaults.linger-ms` | `5` | Producer linger.ms |
| `kates.defaults.compression-type` | `lz4` | Producer compression codec |
| `kates.defaults.record-size` | `1024` | Default message size (bytes) |
