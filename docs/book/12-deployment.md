# Chapter 12: Deployment Guide

This chapter covers everything needed to deploy and operate the full Kates stack — from prerequisites to production operation.

## Prerequisites

| Tool | Version | Purpose |
|------|---------|---------|
| Docker | 20.10+ | Container runtime |
| Kind | 0.20+ | Local Kubernetes cluster |
| kubectl | 1.28+ | Kubernetes CLI |
| Helm | 3.12+ | Kubernetes package manager |
| jq | 1.6+ | JSON processing (optional) |
| Go | 1.22+ | CLI compilation (if building from source) |
| Java | 21+ | Backend compilation (if building from source) |
| Maven | 3.9+ | Backend build (bundled as `mvnw`) |

## Quick Deployment

```bash
# One command — deploys everything
make all
```

This executes a 10-step pipeline:

```mermaid
graph TD
    S1["Step 1<br/>Create Kind cluster 'panda'<br/>+ local Docker registry"] --> S2["Step 2<br/>Pull all images<br/>to local registry"]
    S2 --> S3["Step 3<br/>Load images<br/>into Kind nodes"]
    S3 --> S4["Step 4<br/>Deploy Prometheus<br/>& Grafana"]
    S4 --> S5["Step 5<br/>Wait for monitoring<br/>readiness"]
    S5 --> S6["Step 6<br/>Deploy Strimzi Kafka<br/>(KRaft mode)"]
    S6 --> S7["Step 7<br/>Wait for Kafka<br/>readiness"]
    S7 --> S8["Step 8<br/>Deploy Kafka UI"]
    S8 --> S9["Step 9<br/>Deploy Apicurio<br/>Registry"]
    S9 --> S10["Step 10<br/>Deploy LitmusChaos"]
```

## Component-by-Component Deployment

If you need to deploy components individually:

### Kubernetes Cluster

```bash
# Start Kind cluster with 3 nodes
make cluster
```

Creates a Kind cluster named `panda` with:
- 1 control-plane node (alpha)
- 2 worker nodes (sigma, gamma)
- Zone labels for rack awareness
- Local-path storage provisioner per zone

### Image Management

```bash
# Pull all images to local registry
make images

# Check registry status
make registry-status
```

All images are defined in `images.env`. The pull script detects your platform (arm64/amd64) and pulls the correct architecture.

### Monitoring Stack

```bash
# Deploy Prometheus + Grafana
make monitoring
```

Deploys:
- Prometheus with Kafka JMX scrape targets
- Grafana with 7 pre-provisioned dashboards
- NodePort service at port 30080

### Kafka

```bash
# Deploy Strimzi operator + krafter cluster
make kafka

# Deploy Kafka UI
make ui

# Deploy schema registry
make apicurio
```

For deep Kafka configuration details (broker tuning, security, Cruise Control, troubleshooting), see [Chapter 15: Kafka Deployment Engineering](15-kafka-deployment.md).

### LitmusChaos

```bash
# Deploy LitmusChaos operator
make litmus

# Access Litmus UI
make chaos-ui
# → http://localhost:9091 (admin/litmus)

# Deploy chaos experiments
make chaos-experiments
```

### Kates Application

```bash
# Build + deploy (full pipeline)
make kates

# Or step by step:
make kates-build     # Build JVM image + load into Kind
make kates-deploy    # Apply K8s manifests

# Native image (GraalVM)
make kates-native
```

### Kates Application Configuration

#### Fault Tolerance Timeouts

All `@Timeout` annotations in Kates services are externally configurable via MicroProfile Fault Tolerance properties. The defaults are set in `application.properties` and overridable at deploy time through the ConfigMap.

Pattern: `<fully.qualified.class>/<method>/Timeout/value=<millis>`

```properties
# Example: increase describeTopicDetail timeout to 60 seconds
com.bmscomp.kates.service.TopicService/describeTopicDetail/Timeout/value=60000
```

In `k8s/configmap.yaml` the equivalent env var is:

```yaml
COM_BMSCOMP_KATES_SERVICE_TOPICSERVICE_DESCRIBETOPICDETAIL_TIMEOUT_VALUE: "60000"
```

All 13 annotated methods across `TopicService`, `ClusterHealthService`, and `ConsumerGroupService` have corresponding entries in both files.

#### JVM Tuning

The deployment uses **ZGC** (Z Garbage Collector) for sub-millisecond GC pauses, which prevents throughput dips during stress tests:

```yaml
# k8s/deployment.yaml
- name: JAVA_TOOL_OPTIONS
  value: "-Xms512m -Xmx2560m -XX:+UseZGC -XX:+ZGenerational"
```

| GC | Max Pause | Best For |
|----|:-:|----------|
| G1 (default) | ~10–200ms | General workloads |
| ZGC | < 1ms | Latency-sensitive benchmarking |
| Shenandoah | < 1ms | Alternative low-pause GC |

### Kates CLI

```bash
# Build + install locally
make cli-install

# Cross-compile for all platforms
make cli-build

# Cleanup build artifacts
make cli-clean
```

> [!NOTE]
> **macOS:** `make cli-install` automatically strips provenance/quarantine extended attributes and ad-hoc codesigns the binary. See [Chapter 10: CLI Reference](10-cli-reference.md#installation) for manual install instructions.

## Access Points

After deployment, set up port forwarding:

```bash
make ports
```

| Service | URL | Credentials |
|---------|-----|-------------|
| Grafana | http://localhost:30080 | admin / admin |
| Kafka UI | http://localhost:30081 | — |
| Apicurio Registry | http://localhost:30082 | — |
| Kates API | http://localhost:30083 | — |
| Jaeger UI | http://localhost:30086 | — |
| Prometheus | http://localhost:30090 | — |
| Litmus UI | `make chaos-ui` → http://localhost:9091 | admin / litmus |

## CLI Configuration

```bash
# Connect the CLI to Kates
kates ctx set local --url http://localhost:30083
kates ctx use local

# Verify connectivity
kates health
```

## Makefile Reference

```mermaid
graph TB
    subgraph Infrastructure
        ALL[make all<br/>Complete setup]
        CLUSTER[make cluster]
        IMAGES[make images]
        MONITOR[make monitoring]
        KAFKA[make kafka]
        UI[make ui]
        APICURIO[make apicurio]
        LITMUS[make litmus]
    end
    
    subgraph Kates
        K[make kates]
        KB[make kates-build]
        KN[make kates-native]
        KD[make kates-deploy]
        KR[make kates-redeploy]
        KL[make kates-logs]
        KU[make kates-undeploy]
    end
    
    subgraph CLI
        CB[make cli-build]
        CI[make cli-install]
        CC[make cli-clean]
    end
    
    subgraph Testing
        T[make test]
        TL[make test-load]
        TS[make test-stress]
        TSP[make test-spike]
        TE[make test-endurance]
        TV[make test-volume]
        TC[make test-capacity]
    end
    
    subgraph Chaos
        CK[make chaos-kafka]
        CKP[make chaos-kafka-pod-delete]
        CKN[make chaos-kafka-network-partition]
        CKC[make chaos-kafka-cpu-stress]
        CKA[make chaos-kafka-all]
        CKS[make chaos-kafka-status]
    end
    
    subgraph Operations
        PORTS[make ports]
        STATUS[make status]
        DESTROY[make destroy]
    end
```

### Full Target List

| Target | Description |
|--------|-------------|
| `make all` | Complete setup (cluster → images → all services) |
| `make cluster` | Start Kind cluster only |
| `make images` | Pull and load all images |
| `make monitoring` | Deploy Prometheus & Grafana |
| `make kafka` | Deploy Strimzi Kafka |
| `make ui` | Deploy Kafka UI |
| `make apicurio` | Deploy Apicurio Registry |
| `make litmus` | Deploy LitmusChaos |
| `make kates` | Build + deploy Kates application |
| `make kates-build` | Build Kates JVM image |
| `make kates-native` | Build Kates native image (see below) |
| `make kates-deploy` | Apply Kates K8s manifests |
| `make kates-redeploy` | Restart Kates deployment |
| `make kates-logs` | Stream Kates logs |
| `make kates-undeploy` | Remove Kates |
| `make cli-build` | Cross-compile CLI |
| `make cli-install` | Build + install CLI locally |

### Native Image Build

`make kates-native` builds a GraalVM native image of the Kates backend using Quarkus's native compilation pipeline. This produces a standalone binary with dramatically faster startup.

**Prerequisites:**
- GraalVM 21+ with `native-image` component installed
- Docker (used by Quarkus for in-container native builds)
- ~6GB free memory during compilation

**Build time:** Expect 3–8 minutes depending on hardware (native compilation is significantly slower than JVM builds).

**Startup comparison:**

| Mode | Startup Time | Memory at Idle | Use Case |
|------|:---:|:---:|----------|
| JVM (`make kates`) | ~2s | ~200MB | Development, debugging |
| Native (`make kates-native`) | ~0.05s | ~50MB | Production, CI/CD |

The native image is the recommended deployment mode for production and CI/CD environments where fast startup and low memory footprint matter.

```bash
# Build native image (in-container build, no local GraalVM needed)
make kates-native

# Verify
kubectl logs deployment/kates -n kates | head -1
# → started in 0.047s
```
| `make test` | Run baseline perf test |
| `make test-load` | Run load test |
| `make test-stress` | Run stress test |
| `make test-spike` | Run spike test |
| `make test-endurance` | Run endurance test |
| `make test-volume` | Run volume test |
| `make test-capacity` | Run capacity test |
| `make chaos-kafka` | Set up Kafka chaos |
| `make chaos-kafka-pod-delete` | Run pod-delete chaos |
| `make chaos-kafka-network-partition` | Run network partition |
| `make chaos-kafka-cpu-stress` | Run CPU stress |
| `make chaos-kafka-all` | Run all chaos experiments |
| `make chaos-kafka-status` | Check chaos status |
| `make gameday` | Run automated GameDay validation pipeline |
| `make velero` | Deploy Velero backup |
| `make chart-lint` | Lint Kates Helm chart |
| `make ports` | Start port forwarding |
| `make status` | Check cluster status |
| `make destroy` | Destroy everything |

## Security Configuration

The Kafka cluster uses multiple layers of security:

### Authentication

- **SCRAM-SHA-512** on the plain (9092) and external (9094) listeners
- **TLS mutual auth** on the TLS listener (9093)
- Credentials managed via `KafkaUser` CRs in `config/kafka/kafka-users.yaml`

### Certificate Rotation

Certificates are auto-managed by Strimzi:
- **Cluster CA**: 5-year validity, auto-renewed 180 days before expiry
- **Clients CA**: 5-year validity, auto-renewed 180 days before expiry
- Policy: `replace-key` (new key pair on renewal)

### Network Policies

`config/kafka/kafka-networkpolicies.yaml` implements default-deny with client whitelisting:
- Only kates, kafka-ui, apicurio, litmus, and monitoring can reach brokers
- Controller mesh traffic isolated
- Operator access scoped to Kafka pods + K8s API

### ACL Management

ACLs are declared via `KafkaUser` CRs (GitOps):
- `kates-backend` — superUser with full access
- `kafka-ui` — read-only on all topics
- `apicurio-registry` — read/write on internal topics

## GameDay Validation

Run an automated 7-phase validation pipeline:

```bash
make gameday
```

Phases: pre-flight → baseline → chaos-inject → observe → recover → post-flight → report

## Troubleshooting

### Images Won't Load

```bash
# Check registry health
make registry-status

# Manually pull and load
./pull-images.sh
./load-images-to-kind.sh
```

### Kafka Pods Not Starting

```bash
# Check Strimzi operator logs
kubectl logs -f deployment/strimzi-cluster-operator -n kafka

# Check Kafka pod events
kubectl describe pod pool-alpha-0 -n kafka
```

### CLI Binary Killed on macOS

If `kates health` is immediately killed (exit code 137), macOS is blocking the unsigned binary:

```bash
# Fix: reinstall with codesigning
make cli-install

# Or manually
sudo xattr -dr com.apple.provenance /usr/local/bin/kates
sudo xattr -dr com.apple.quarantine /usr/local/bin/kates
sudo codesign -f -s - /usr/local/bin/kates
```

### Kates Can't Connect to Kafka

```bash
# Verify Kafka service
kubectl get svc -n kafka

# Check Kates logs
make kates-logs

# Verify bootstrap address in configmap
kubectl get configmap kates-config -n kates -o yaml
```

### Litmus Experiments Fail

```bash
# Check chaos operator
kubectl get pods -n litmus

# Check experiment status
make chaos-kafka-status

# View experiment logs
kubectl logs -f -l app=chaos-operator -n litmus
```

## Destroying the Environment

```bash
# Destroy everything (cluster + images + registry)
make destroy
```

This deletes the Kind cluster and all associated resources.
