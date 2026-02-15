# Deployment Guide

## Prerequisites

Kates runs as a Quarkus application and requires:

1. **Kafka Cluster** — a running Kafka cluster accessible via bootstrap servers
2. **Execution Backend** (choose one):
   - **Native** (default) — no external dependencies, runs workloads in-process using virtual threads
   - **Trogdor** — Apache Trogdor Coordinator + Agent(s) for distributed load generation

## Running Locally

### Dev Mode

```bash
cd kates
mvn quarkus:dev
```

This starts Kates on port 8080 with hot reload, Swagger UI at `/q/swagger-ui`, and live coding.

Override the default cluster connection in `application.properties` or via environment variables:

```bash
mvn quarkus:dev \
  -Dkates.kafka.bootstrap-servers=localhost:9092 \
  -Dkates.engine.default-backend=native
```

### Building a JAR

```bash
mvn clean package -DskipTests
java -jar target/quarkus-app/quarkus-run.jar
```

## Kubernetes Deployment

Kates ships with ready-to-use manifests in `kates/k8s/`. The deploy script applies them in order:

```bash
make kates-deploy
# or manually:
kubectl apply -f kates/k8s/namespace.yaml
kubectl apply -f kates/k8s/configmap.yaml
kubectl apply -f kates/k8s/deployment.yaml
kubectl apply -f kates/k8s/service.yaml
```

### ConfigMap — `kates-config`

The ConfigMap is the primary way to configure Kates in Kubernetes. It contains environment variables that MicroProfile Config maps to application properties automatically (`KATES_TESTS_STRESS_PARTITIONS` → `kates.tests.stress.partitions`).

**To change test defaults at runtime:**

```bash
kubectl edit configmap kates-config -n kates
# Change e.g. KATES_TESTS_STRESS_PARTITIONS: "12"
kubectl rollout restart deployment/kates -n kates
```

The ConfigMap includes three sections:

#### Core Settings

```yaml
KATES_KAFKA_BOOTSTRAP_SERVERS: "krafter-kafka-bootstrap.kafka.svc:9092"
KATES_TROGDOR_COORDINATOR_URL: "http://trogdor-coordinator.kafka.svc:8889"
KATES_ENGINE_DEFAULT_BACKEND: "native"
```

#### Global Defaults (fallback for all test types)

```yaml
KATES_DEFAULTS_REPLICATION_FACTOR: "3"
KATES_DEFAULTS_PARTITIONS: "3"
KATES_DEFAULTS_MIN_INSYNC_REPLICAS: "2"
KATES_DEFAULTS_ACKS: "all"
KATES_DEFAULTS_BATCH_SIZE: "65536"
KATES_DEFAULTS_LINGER_MS: "5"
KATES_DEFAULTS_COMPRESSION_TYPE: "lz4"
KATES_DEFAULTS_RECORD_SIZE: "1024"
KATES_DEFAULTS_NUM_RECORDS: "1000000"
KATES_DEFAULTS_THROUGHPUT: "-1"
KATES_DEFAULTS_DURATION_MS: "600000"
KATES_DEFAULTS_NUM_PRODUCERS: "1"
KATES_DEFAULTS_NUM_CONSUMERS: "1"
```

#### Per-Test-Type Overrides

Each test type has its own set of tuned environment variables. Only the values that differ from global defaults need to be set:

| Test Type | Key Env Vars |
|-----------|-------------|
| LOAD | All global defaults |
| STRESS | `KATES_TESTS_STRESS_PARTITIONS=6`, `BATCH_SIZE=131072`, `NUM_PRODUCERS=3` |
| SPIKE | `KATES_TESTS_SPIKE_ACKS=1`, `COMPRESSION_TYPE=none`, `LINGER_MS=0` |
| ENDURANCE | `KATES_TESTS_ENDURANCE_THROUGHPUT=5000`, `DURATION_MS=3600000` |
| VOLUME | `KATES_TESTS_VOLUME_RECORD_SIZE=10240`, `BATCH_SIZE=262144`, `LINGER_MS=50` |
| CAPACITY | `KATES_TESTS_CAPACITY_PARTITIONS=12`, `NUM_PRODUCERS=5`, `DURATION_MS=1200000` |
| ROUNDTRIP | `KATES_TESTS_ROUNDTRIP_BATCH_SIZE=16384`, `COMPRESSION_TYPE=none`, `THROUGHPUT=10000` |

The full list of env vars is in `kates/k8s/configmap.yaml`.

### Kates Deployment

The deployment uses `envFrom` to inject all ConfigMap values as environment variables:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kates
  namespace: kates
spec:
  replicas: 1
  selector:
    matchLabels:
      app: kates
  template:
    metadata:
      labels:
        app: kates
    spec:
      containers:
      - name: kates
        image: kates:latest
        imagePullPolicy: Never
        ports:
        - containerPort: 8080
        envFrom:
        - configMapRef:
            name: kates-config
        readinessProbe:
          httpGet:
            path: /api/health
            port: 8080
          initialDelaySeconds: 5
        livenessProbe:
          httpGet:
            path: /q/health/live
            port: 8080
          initialDelaySeconds: 10
```

### Trogdor Coordinator (optional)

Only required when using the `trogdor` backend. The Trogdor Coordinator and Agent processes are part of the Apache Kafka distribution:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: trogdor-coordinator
  namespace: kafka
spec:
  replicas: 1
  selector:
    matchLabels:
      app: trogdor-coordinator
  template:
    metadata:
      labels:
        app: trogdor-coordinator
    spec:
      containers:
      - name: coordinator
        image: apache/kafka:4.1.1
        command:
        - /opt/kafka/bin/trogdor.sh
        - coordinator
        - --node-name coordinator
        - --coordinator.config /opt/kafka/config/trogdor-coordinator.conf
        ports:
        - containerPort: 8889
```

### Trogdor Agent (optional)

Each Trogdor Agent runs the actual workloads. Deploy one or more:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: trogdor-agent
  namespace: kafka
spec:
  replicas: 3
  selector:
    matchLabels:
      app: trogdor-agent
  template:
    metadata:
      labels:
        app: trogdor-agent
    spec:
      containers:
      - name: agent
        image: apache/kafka:4.1.1
        command:
        - /opt/kafka/bin/trogdor.sh
        - agent
        - --node-name agent
        - --agent.config /opt/kafka/config/trogdor-agent.conf
        ports:
        - containerPort: 8888
```

## Configuration Resolution Order

MicroProfile Config resolves properties in this priority order:

| Priority | Source | Example |
|----------|--------|---------|
| 1 (highest) | System properties | `-Dkates.tests.stress.partitions=12` |
| 2 | Environment variables / ConfigMap | `KATES_TESTS_STRESS_PARTITIONS=12` |
| 3 | `application.properties` | `kates.tests.stress.partitions=6` |
| 4 (lowest) | `@ConfigProperty defaultValue` | Built-in `"6"` in Java code |

This means ConfigMap values override `application.properties` defaults, and system properties override everything.

## Environment Variable Reference

| Property | Environment Variable | Description |
|----------|---------------------|-------------|
| `kates.kafka.bootstrap-servers` | `KATES_KAFKA_BOOTSTRAP_SERVERS` | Kafka bootstrap servers |
| `kates.trogdor.coordinator-url` | `KATES_TROGDOR_COORDINATOR_URL` | Trogdor Coordinator endpoint |
| `kates.engine.default-backend` | `KATES_ENGINE_DEFAULT_BACKEND` | Default backend (`native` or `trogdor`) |
| `kates.tests.{type}.partitions` | `KATES_TESTS_{TYPE}_PARTITIONS` | Per-type partition count |
| `kates.tests.{type}.batch-size` | `KATES_TESTS_{TYPE}_BATCH_SIZE` | Per-type producer batch size |
| `kates.tests.{type}.num-producers` | `KATES_TESTS_{TYPE}_NUM_PRODUCERS` | Per-type producer count |
| ... | ... | All 13 parameters × 7 test types |
