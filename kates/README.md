# Kates Backend

Quarkus-based REST API that orchestrates Kafka performance tests, chaos engineering, and trend analysis. Communicates with a Strimzi-managed Kafka cluster and stores results in PostgreSQL.

## Tech Stack

| Component | Technology |
|-----------|------------|
| Framework | Quarkus 3.x (Java 21) |
| Database | PostgreSQL (Flyway migrations) |
| Kafka | Strimzi KRaft (no ZooKeeper) |
| Build | Maven Wrapper (`./mvnw`) |
| Container | Multi-stage Dockerfile (JVM + native) |

## Package Structure

```
src/main/java/com/klster/kates/
├── api/            REST endpoints (JAX-RS resources)
├── chaos/          Chaos provider implementations (Litmus, K8s, NoOp)
├── config/         Application configuration beans
├── disruption/     Disruption orchestration, safety guards, timeline events
├── domain/         Core domain models (TestSpec, TestRun, TestResult)
├── engine/         Benchmark execution engine (native Kafka backend)
├── export/         Export services (CSV, JUnit, heatmap)
├── persistence/    Panache repositories (PostgreSQL)
├── report/         Report generation and SLA grading
├── resilience/     Resilience testing (chaos + performance correlation)
├── schedule/       Cron-based test scheduling
├── service/        Kafka admin service, cluster introspection
├── trend/          Phase-level trend analysis
└── trogdor/        Trogdor backend integration (legacy)
```

## Development

### Dev Mode (hot reload)

```bash
./mvnw quarkus:dev
```

Starts on `http://localhost:8080` with Swagger UI at `/q/swagger-ui` and live reload.

### Run Tests

```bash
./mvnw test
```

### Build JVM Image

```bash
./mvnw package -DskipTests -B
docker build -f Dockerfile -t kates:latest ..
```

### Build Native Image (GraalVM)

Requires GraalVM 21+ and Docker:

```bash
./mvnw package -Dnative -DskipTests -B
docker build -f Dockerfile.native -t kates:latest .
```

### Deploy to Kind

```bash
# From project root:
make kates          # Build + deploy (full pipeline)
make kates-native   # Build native + deploy
make kates-logs     # Stream logs
make kates-redeploy # Restart without rebuilding
```

## Configuration

All test parameters are configurable via `application.properties` or environment variables in the Kubernetes ConfigMap (`kates/k8s/configmap.yaml`).

MicroProfile Config maps environment variables automatically:
`KATES_TESTS_STRESS_PARTITIONS` → `kates.tests.stress.partitions`

## API

After deployment, the API is available at `http://localhost:30083`. See the [REST API Reference](../docs/book/11-api-reference.md) for full endpoint documentation.
