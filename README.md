# klster — Local Kubernetes Cluster

Local Kubernetes cluster using [Kind](https://kind.sigs.k8s.io/) with 3 nodes simulating availability zones, fully offline container image management, and a complete Kafka + monitoring stack.

## Features

- **Offline-first** — all images pulled once, loaded into Kind, deployed with `imagePullPolicy: Never`
- **One-command setup** — `make all` brings up the entire stack
- **Multi-AZ simulation** — 3 nodes labeled `alpha`, `sigma`, `gamma`
- **Monitoring** — Prometheus, Grafana, and custom Kafka dashboards
- **Kafka (Strimzi KRaft)** — 3-broker cluster with rack awareness
- **Chaos engineering** — LitmusChaos with Prometheus integration
- **Schema registry** — Apicurio connected to Kafka
- **Performance testing** — built-in 1M-message Kafka benchmark

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
| Litmus UI | `make chaos-ui` → http://localhost:9091 | admin / litmus |

### Destroy

```bash
make destroy
```

## Image Management

All images are defined in `images.env` — the single source of truth. Both `pull-images.sh` and `load-images-to-kind.sh` source this file, eliminating version drift.

### How It Works

1. **Pull** — `pull-images.sh` downloads images to a local Docker registry (`localhost:5001`), detecting platform (arm64/amd64) automatically
2. **Load** — `load-images-to-kind.sh` pulls from the local registry and loads into Kind nodes. No internet fallback — fails if the image isn't in the registry
3. **Deploy** — all Helm values and manifests use `imagePullPolicy: Never`, ensuring Kubernetes only uses images already on Kind nodes

### Managing Images Individually

```bash
# Pull all images (skips already-cached)
./pull-images.sh

# Load all images into Kind (skips already-loaded)
./load-images-to-kind.sh

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
./load-images-to-kind.sh
```

Or load a specific image manually:
```bash
kind load docker-image <image>:<tag> --name panda
```

### Pull timeout (quay.io, scarf.sh)

The pull script continues past failures. Re-run to retry only the failed images:
```bash
./pull-images.sh
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
| `config/monitoring.yaml` | Prometheus + Grafana Helm values |
| `config/kafka.yaml` | Kafka KRaft cluster + node pools |
| `config/kafka-ui.yaml` | Kafka UI deployment manifest |
| `config/apicurio-values-offline.yaml` | Apicurio Registry Helm values |
| `config/litmus-values.yaml` | LitmusChaos Helm values |
| `config/storage-classes.yaml` | Zone-specific storage classes |
