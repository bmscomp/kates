# Kates — Kafka Advanced Testing & Engineering Suite

A terminal-first platform for **performance testing**, **chaos engineering**, and **operational resilience** of Apache Kafka clusters. Runs on a local Kind-based Kubernetes environment with production-parity infrastructure.

## Features

- **8 test types** — LOAD, STRESS, SPIKE, ENDURANCE, VOLUME, CAPACITY, ROUND_TRIP, INTEGRITY
- **Chaos engineering** — 7 built-in playbooks with LitmusChaos, SLA grading, and safety guardrails
- **Scenario files** — Declarative YAML test definitions with SLA gates for CI/CD
- **CLI** — Full-featured terminal client with dashboards, sparklines, trend analysis, and export
- **Kafka client** — Interactive Kafka CLI with topic CRUD, produce, consume, and consumer group inspection
- **Interactive TUI** — Full-screen Kafka explorer with tab-based navigation, search, and consumer tail
- **Backend API** — Quarkus REST API with PostgreSQL persistence and native image support
- **Kafka 4.2 (Strimzi KRaft)** — Role-separated controllers/brokers, SCRAM-SHA-512 auth, TLS, rack awareness
- **Tiered Storage** — KIP-405 cold segment offload to MinIO
- **Share Groups** — KIP-932 work-queue consumers with server-side message distribution
- **Dead Letter Queue** — Automated DLQ consumer with alerting and `/api/dlq/stats` endpoint
- **Schema enforcement** — JSON schemas for kates topics via Apicurio Registry
- **Distributed tracing** — OpenTelemetry OTLP → Jaeger with Kafka client instrumentation
- **Security** — NetworkPolicies, certificate auto-rotation, ACL GitOps via KafkaUser CRs
- **CI/CD** — Backend CI pipeline + Kind-based integration tests + GameDay automation
- **Monitoring** — Prometheus, Grafana with 10+ dashboards, 20+ PrometheusRule alerts
- **Offline-first infrastructure** — all images pulled once, loaded into Kind, deployed with `imagePullPolicy: Never`
- **One-command setup** — `make all` brings up the entire stack
- **Multi-AZ simulation** — 3-node Kind cluster labeled `alpha`, `sigma`, `gamma`

## Prerequisites

- [Docker](https://www.docker.com/)
- [Kind](https://kind.sigs.k8s.io/docs/user/quick-start/#installation)
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [Helm](https://helm.sh/docs/intro/install/)
- [jq](https://stedolan.github.io/jq/) (optional, for registry status)

## Quick Start

```bash
make all
```

This runs a 10-step pipeline:

| Step | Action |
|------|--------|
| 1 | Create Kind cluster `panda` + local Docker registry |
| 2 | Pull all images to local registry (`localhost:5001`) |
| 3 | Load images from registry into Kind nodes |
| 4 | Deploy Prometheus & Grafana |
| 5 | Wait for monitoring readiness |
| 6 | Deploy Strimzi Kafka (KRaft mode) |
| 7 | Wait for Kafka readiness |
| 8 | Deploy Kafka UI |
| 9 | Deploy Apicurio Registry |
| 10 | Deploy LitmusChaos |

### Access Points

| Service | URL | Credentials |
|---------|-----|-------------|
| Grafana | http://localhost:30080 | admin / admin |
| Kafka UI | http://localhost:30081 | — |
| Apicurio Registry | http://localhost:30082 | — |
| Kates API | http://localhost:30083 | — |
| Jaeger UI | http://localhost:30086 | — |
| Prometheus | http://localhost:30090 | — |
| Litmus UI | `make chaos-ui` → http://localhost:9091 | admin / litmus |

### Destroy

```bash
make destroy
```

## Image Management

All images are defined in `images.env` — the single source of truth. Both `scripts/pull-images.sh` and `scripts/load-images-to-kind.sh` source this file, eliminating version drift.

### How It Works

1. **Pull** — `scripts/pull-images.sh` downloads images to a local Docker registry (`localhost:5001`), detecting platform (arm64/amd64) automatically
2. **Load** — `scripts/load-images-to-kind.sh` pulls from the local registry and loads into Kind nodes. No internet fallback — fails if the image isn't in the registry
3. **Deploy** — all Helm values and manifests use `imagePullPolicy: Never`, ensuring Kubernetes only uses images already on Kind nodes

### Managing Images Individually

```bash
# Pull all images (skips already-cached)
./scripts/pull-images.sh

# Load all images into Kind (skips already-loaded)
./scripts/load-images-to-kind.sh

# Check what's in the registry
make registry-status

# Check what's loaded in Kind
docker exec -it panda-control-plane crictl images
```

### Updating an Image

```bash
# 1. Pull the new version
docker pull provectuslabs/kafka-ui:v1.0.0

# 2. Tag and push to local registry
docker tag provectuslabs/kafka-ui:v1.0.0 localhost:5001/provectuslabs/kafka-ui:v1.0.0
docker push localhost:5001/provectuslabs/kafka-ui:v1.0.0

# 3. Load into Kind
kind load docker-image provectuslabs/kafka-ui:v1.0.0 --name panda

# 4. Update the tag in images.env and the relevant config, then redeploy
```

## Makefile Targets

| Target | Description |
|--------|-------------|
| `make all` | Full setup (cluster + registry + images + all services) |
| `make cluster` | Start Kind cluster only |
| `make images` | Pull and load all images |
| `make monitoring` | Deploy Prometheus & Grafana |
| `make kafka` | Deploy Kafka (Strimzi) |
| `make ui` | Deploy Kafka UI |
| `make apicurio` | Deploy Apicurio Registry |
| `make litmus` | Deploy LitmusChaos |
| `make chaos-ui` | Port-forward Litmus UI |
| `make chaos-experiments` | Apply chaos experiments |
| `make velero` | Deploy Velero backup |
| `make test` | Run Kafka performance test (1M messages) |
| `make gameday` | Run automated GameDay validation pipeline |
| `make chart-lint` | Lint Kates Helm chart |
| `make ports` | Start port forwarding |
| `make status` | Check cluster status |
| `make destroy` | Destroy cluster |

## Monitoring & Dashboards

Custom Grafana dashboards (auto-provisioned):
- **Kafka Complete Monitoring** — all metrics, brokers, topics, zones, JVM
- **Kafka Cluster Health** — broker status, offline partitions, zone distribution
- **Kafka Performance Metrics** — topic growth, partitions, broker count
- **Kafka Performance Test Results** — perf-test throughput, message counts
- **Kafka JVM Metrics** — heap memory, GC rate, thread count per zone
- **Strimzi Operator & Kafka Connect** — reconciliation p99, success/failure rates, Connect task health

### Distributed Tracing

OpenTelemetry traces are exported via OTLP to Jaeger. Auto-instrumented spans cover:
- REST API calls (JAX-RS)
- Kafka producer/consumer operations
- Database queries (JDBC)

Access the Jaeger UI at http://localhost:30086 after deployment.

## Documentation

| Resource | Content |
|----------|---------|
| [The Definitive Guide](docs/book/README.md) | 14-chapter book covering architecture, performance theory, test types, chaos engineering, data integrity, observability, CLI/API reference, deployment, scenario files, and recipes |
| [Tutorials](docs/tutorials/README.md) | 6 hands-on tutorials from first test to CI/CD integration |

## Performance Testing

```bash
make test
```

Creates a `performance` namespace, produces 1M messages across 3 partitions, consumes them, and displays throughput/latency metrics.

## Cluster Topology

Defined in `config/cluster.yaml`:

| Node | Role | Zone |
|------|------|------|
| alpha | Control Plane + Worker | alpha |
| sigma | Worker | sigma |
| gamma | Worker | gamma |

Resource labels simulate instance types (3 CPU, 6 GB RAM, 10 GB storage). Kind uses host Docker resources.

## Troubleshooting

### Pod stuck in `ErrImageNeverPull`

Image not loaded in Kind nodes. Fix:
```bash
./scripts/load-images-to-kind.sh
```

Or load a specific image manually:
```bash
kind load docker-image <image>:<tag> --name panda
```

### Pull timeout (quay.io, scarf.sh)

The pull script continues past failures. Re-run to retry only the failed images:
```bash
./scripts/pull-images.sh
```

### Check pod image pull status

```bash
kubectl describe pod <pod-name> -n <namespace> | grep -A5 Events
```

## Configuration Files

| File | Purpose |
|------|---------|
| `images.env` | Central image manifest (all image:tag definitions) |
| `config/cluster.yaml` | Kind cluster topology (3 nodes, 3 zones) |
| `config/kafka/` | Kafka cluster, metrics, users, topics, alerts, rebalance, backup, drain cleaner |
| `config/kafka-connect/` | Kafka Connect cluster, MirrorMaker 2 |
| `config/kafka-ui/` | Kafka UI deployment manifest |
| `config/apicurio/` | Apicurio Registry Helm values |
| `config/monitoring/` | Prometheus + Grafana values, dashboards |
| `config/litmus/` | LitmusChaos values, experiments, RBAC |
| `config/storage/` | Zone-specific storage classes |
| `config/velero/` | Velero + MinIO Helm values |

## Kates CLI

**Kates** (Kafka Advanced Testing & Engineering Suite) is a terminal-first CLI for performance testing, chaos engineering, and trend analysis against the Kafka cluster. It communicates with the Kates backend API.

### Installation

```bash
cd cli
go build -o kates .
mv kates /usr/local/bin/  # or keep in-place
```

### Context Management

Kates uses a context system similar to `kubectl`. Configuration is stored in `~/.kates.yaml`.

```bash
# Create a context pointing to the Kates API
kates ctx set local --url http://localhost:30083

# Switch to a context
kates ctx use local

# Show all contexts
kates ctx show

# Print the active context
kates ctx current
```

### Commands

#### Health & Status

```bash
kates health            # System health, Kafka connectivity, engine status
kates status            # One-line system status
kates version           # CLI, API, and runtime version info
```

#### Cluster Inspection

```bash
kates cluster info                 # Cluster metadata — brokers, controller, rack/AZ
kates cluster topics               # List all Kafka topics
kates cluster topics describe <t>  # Detailed topic metadata, configs, partition health
kates cluster broker configs <id>  # Non-default broker config (grouped by source)
kates cluster check                # Comprehensive cluster health check
kates cluster groups               # List consumer groups with state and members
kates cluster groups describe <g>  # Consumer group offsets and per-partition lag
```

#### Kafka Client

```bash
kates kafka brokers                                    # Broker list with rack/AZ and roles
kates kafka topics                                     # List topics with ISR health
kates kafka topic <name>                               # Describe topic partitions and config
kates kafka create-topic <name> --partitions 3 --replication-factor 3  # Create a topic
kates kafka alter-topic <name> --config retention.ms=172800000         # Alter topic config
kates kafka delete-topic <name> --yes                  # Delete a topic
kates kafka groups                                     # List consumer groups
kates kafka group <id>                                 # Consumer group lag detail
kates kafka consume <topic> --offset earliest          # Fetch records from a topic
kates kafka consume <topic> -f                         # Tail a topic (like tail -f)
kates kafka produce <topic> --key k --value v          # Produce a record
kates kafka tui                                        # Launch interactive full-screen explorer
```

#### Test Execution

```bash
kates test create --type LOAD --records 100000    # Start a load test
kates test create --type STRESS --producers 8     # Multi-producer stress test
kates test list                                    # List all test runs
kates test list --type LOAD --status DONE          # Filter by type and status
kates test show <id>                               # Inspect a specific run
kates test delete <id>                             # Delete a test run
kates test apply -f scenario.yaml --wait           # Apply a YAML test definition
```

Available test types: `LOAD`, `STRESS`, `SPIKE`, `ENDURANCE`, `VOLUME`, `CAPACITY`, `ROUND_TRIP`.

#### Reports & Export

```bash
kates report show <id>              # Full report with SLA verdict
kates report summary <id>           # Condensed metrics summary
kates report export csv <id>        # Export results as CSV
kates report export junit <id>      # Export as JUnit XML (CI integration)
kates report diff <id1> <id2>       # Side-by-side comparison of two runs
```

#### Trend Analysis

```bash
kates trend --type LOAD --metric p99LatencyMs --days 30     # P99 trend over 30 days
kates trend --type STRESS --metric throughput --days 7       # Throughput sparkline
```

#### Schedules

```bash
kates schedule list                                               # List all schedules
kates schedule show <id>                                          # Inspect a schedule
kates schedule create --name nightly --cron "0 2 * * *" -f s.yaml # Create a recurring test
kates schedule delete <id>                                        # Remove a schedule
```

#### Resilience Testing

```bash
kates resilience --experiment pod-kill --duration 60s   # Chaos-performance correlation
```

#### Observability

```bash
kates dashboard      # Full-screen monitoring dashboard (alias: dash)
kates top            # Live view of running tests (like kubectl top)
kates watch <id>     # Real-time streaming of a running test
```

### Global Flags

| Flag | Description |
|------|-------------|
| `-o, --output` | Output format: `table` (default) or `json` |
| `--url` | Override API URL for a single call |
| `--context` | Use a named context for a single call |
| `-h, --help` | Show help for any command |

### Project Structure

```
cli/
├── main.go              # Entry point
├── cmd/                 # Cobra command definitions
│   ├── root.go          # Root command, context loading, flags
│   ├── cluster.go       # cluster info/topics/broker/check
│   ├── groups.go        # consumer group commands
│   ├── test.go          # test create/list/show/delete/apply
│   ├── report.go        # report show/summary/export/diff
│   ├── trend.go         # trend analysis with sparklines
│   ├── schedule.go      # schedule CRUD
│   ├── resilience.go    # chaos-performance correlation
│   ├── dashboard.go     # full-screen dashboard
│   ├── top.go           # live test monitoring
│   ├── watch.go         # streaming test output
│   ├── health.go        # health check
│   ├── status.go        # one-line status
│   ├── config.go        # ctx set/use/show/delete/current
│   ├── version.go       # version info
│   ├── helpers.go       # shared formatting utilities
│   └── helpers_test.go  # unit tests for helpers
├── client/              # HTTP API client
│   ├── client.go        # All API methods with retry logic
│   ├── types.go         # Request/response type definitions
│   └── client_test.go   # httptest-based tests for all endpoints
├── output/              # Terminal rendering utilities
│   ├── output.go        # Tables, banners, sparklines, config lists
│   └── output_test.go   # Output rendering tests
└── build.sh             # Cross-platform build script
```

### Running Tests

```bash
cd cli
go test ./... -v
```

