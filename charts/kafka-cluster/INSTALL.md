# Kafka Cluster Chart — Installation Guide

Detailed, step-by-step guide for deploying the kafka-cluster Helm chart on any Kubernetes cluster.

## Table of Contents

- [Prerequisites](#prerequisites)
- [Option A: Local Kind Cluster](#option-a-local-kind-cluster)
- [Option B: Any Kubernetes Cluster](#option-b-any-kubernetes-cluster)
- [Post-Install Verification](#post-install-verification)
- [Connecting Applications](#connecting-applications)
- [Day-2 Operations](#day-2-operations)
- [Uninstalling](#uninstalling)

---

## Prerequisites

### Required Tools

| Tool | Minimum Version | Check |
|------|----------------|-------|
| `kubectl` | 1.25+ | `kubectl version --client` |
| `helm` | 3.12+ | `helm version` |
| `docker` | 24+ (Kind only) | `docker version` |
| `kind` | 0.20+ (Kind only) | `kind version` |
| `make` | any | `make --version` |

### Required Cluster Resources

| Environment | Controllers | Brokers | Min Nodes | Min Memory | Min CPU |
|-------------|-------------|---------|-----------|------------|---------|
| Kind | 1 | 3 | 1 (multi-role) | 8 GiB | 4 cores |
| Dev | 1 | 1 | 1 | 4 GiB | 2 cores |
| Staging | 3 | 3 | 3 | 24 GiB | 12 cores |
| Prod | 3 | 9 | 3+ | 64 GiB | 24 cores |

### Required CRDs

The chart bundles the **Strimzi Operator** as a subchart (`strimziOperator.enabled: true`). If you prefer to manage the operator separately, install it first:

```bash
helm install strimzi-kafka-operator \
  oci://quay.io/strimzi-helm/strimzi-kafka-operator \
  --version 1.0.0 \
  --namespace kafka --create-namespace \
  --wait
```

Then deploy the chart with `--set strimziOperator.enabled=false`.

---

## Option A: Local Kind Cluster

The fastest way to get a full Kafka cluster running locally.

### Step 1 — Create the Kind cluster

```bash
make cluster
```

This creates a 4-node Kind cluster named `panda` with 3 worker nodes labeled as zones (`alpha`, `sigma`, `gamma`).

### Step 2 — Load container images

```bash
./scripts/load-images-to-kind.sh
```

Pre-pulls and loads Strimzi, Kafka, and exporter images into Kind (required for air-gapped/offline environments).

### Step 3 — Deploy Kafka

```bash
make kafka-deploy
```

This automatically:
1. Builds Helm chart dependencies (Strimzi operator subchart)
2. Creates the `kafka` namespace
3. Applies Kind-specific `StorageClass` resources (`local-storage-alpha`, `local-storage-sigma`, `local-storage-gamma`)
4. Runs `helm upgrade --install` with `values-dev.yaml` + `values-kind.yaml` overlays
5. Waits for `Kafka` CR to reach `Ready` state
6. Waits for all `KafkaUser` secrets to be created

### Step 4 — Verify

```bash
kubectl get kafka -n kafka
kubectl get pods -n kafka -l strimzi.io/cluster=krafter
helm test kafka-cluster -n kafka --timeout 5m
```

### What Kind deploys

| Component | Configuration |
|-----------|--------------|
| Controllers | 1 replica, 1 GiB storage |
| Brokers | 3 pools (alpha/sigma/gamma), 1 replica each |
| Listeners | `plain` (9092), `tls` (9093) — no external NodePort |
| Tolerations | control-plane nodes allowed |
| Disabled | NetworkPolicies, CruiseControl, Kafka Exporter, alerts, PodMonitors, tiered storage, backup |

---

## Option B: Any Kubernetes Cluster

### Step 1 — Choose your environment

| Environment | Command | Overlay Files |
|-------------|---------|---------------|
| Dev | `make kafka-deploy ENV=dev` | `values-dev.yaml` |
| Staging | `make kafka-deploy ENV=staging` | `values-staging.yaml` |
| Production | `make kafka-deploy ENV=prod` | `values-prod.yaml` |
| Custom | `make kafka-deploy ENV=myenv` | `values-myenv.yaml` |

### Step 2 — Prepare the cluster (Production/Staging)

#### a. StorageClass

Ensure a `StorageClass` exists for persistent volumes. For cloud providers:

```bash
# AWS EBS
kubectl get sc gp3 && echo "OK"

# GCP Persistent Disk
kubectl get sc standard-rwo && echo "OK"

# Azure Managed Disk
kubectl get sc managed-csi && echo "OK"
```

Set the class in your overlay:

```yaml
brokerPools:
  - name: brokers-az1
    storageClass: gp3
    storageSize: 200Gi
    replicas: 3

controllers:
  storage:
    class: gp3
```

#### b. Namespaces

The chart creates resources in the release namespace. Ensure dependent namespaces exist:

```bash
kubectl create namespace kafka 2>/dev/null || true
kubectl create namespace monitoring 2>/dev/null || true   # if dashboards.enabled
```

#### c. Node labels (optional, for zone-awareness)

If your cluster nodes already have `topology.kubernetes.io/zone` labels (standard on all major cloud providers), zone-aware scheduling works automatically.

Verify:

```bash
kubectl get nodes -L topology.kubernetes.io/zone
```

### Step 3 — Deploy with Helm

#### Using Make (recommended)

```bash
make kafka-deploy ENV=prod
```

#### Using Helm directly

```bash
# Build chart dependencies
helm dependency build charts/kafka-cluster

# Install
helm upgrade --install kafka-cluster charts/kafka-cluster \
  --namespace kafka --create-namespace \
  -f charts/kafka-cluster/values-prod.yaml \
  --timeout 10m \
  --wait
```

#### Using a custom values file

```bash
helm upgrade --install kafka-cluster charts/kafka-cluster \
  --namespace kafka --create-namespace \
  -f my-custom-values.yaml \
  --timeout 10m \
  --wait
```

### Step 4 — Customize inline

Override any value at install time:

```bash
helm upgrade --install kafka-cluster charts/kafka-cluster \
  --namespace kafka --create-namespace \
  -f charts/kafka-cluster/values-prod.yaml \
  --set clusterName=my-kafka \
  --set controllers.replicas=5 \
  --set kafkaConnect.enabled=true \
  --set 'kafkaConnect.jvmOptions.-Xmx=1024m' \
  --timeout 10m \
  --wait
```

### Step 5 — Wait for readiness

```bash
# Watch the Kafka CR status
kubectl wait kafka/krafter --for=condition=Ready -n kafka --timeout=300s

# Watch individual pods
kubectl get pods -n kafka -l strimzi.io/cluster=krafter -w
```

---

## Post-Install Verification

### Helm Tests (9 tiers)

```bash
helm test kafka-cluster -n kafka --timeout 5m
```

| Tier | What It Tests |
|------|--------------|
| 1 | Bootstrap TCP connectivity, Kafka CR Ready |
| 2 | Produce/consume round-trip with SCRAM auth |
| 3 | KafkaUser CRs ready, SCRAM secrets exist |
| 4 | KRaft controller quorum health |
| 5 | KafkaTopic CRs ready, partition/replica spec |
| 6 | Listener bootstrap addresses, TLS CA secrets |
| 7 | Node pool readiness, broker pod distribution |
| 8 | CruiseControl pod running (if enabled) |
| 9 | Metrics ConfigMap, exporter, PodMonitors |

### Manual checks

```bash
# Cluster overview
kubectl get kafka,kafkanodepools,kafkatopics,kafkausers -n kafka

# Controller quorum
kubectl get pods -n kafka -l strimzi.io/pool-name=controllers

# Broker pods per pool
kubectl get pods -n kafka -l strimzi.io/kind=Kafka -o wide

# Listener addresses
kubectl get kafka krafter -n kafka \
  -o jsonpath='{.status.listeners[*].bootstrapServers}'

# User secrets
kubectl get secrets -n kafka -l strimzi.io/cluster=krafter
```

---

## Connecting Applications

### Internal SCRAM (port 9092)

```properties
bootstrap.servers=krafter-kafka-bootstrap.kafka.svc:9092
security.protocol=SASL_PLAINTEXT
sasl.mechanism=SCRAM-SHA-512
sasl.jaas.config=org.apache.kafka.common.security.scram.ScramLoginModule required \
  username="my-user" password="<password>";
```

Retrieve the password:

```bash
kubectl get secret my-user -n kafka \
  -o jsonpath='{.data.password}' | base64 -d
```

### Internal TLS (port 9093)

```properties
bootstrap.servers=krafter-kafka-bootstrap.kafka.svc:9093
security.protocol=SSL
ssl.truststore.type=PEM
ssl.truststore.certificates=<ca.crt contents>
```

Extract the CA certificate:

```bash
kubectl get secret krafter-cluster-ca-cert -n kafka \
  -o jsonpath='{.data.ca\.crt}' | base64 -d > ca.crt
```

### External NodePort (port 9094)

Only available when external listener is configured (not in Kind overlay):

```bash
# Find the NodePort
kubectl get svc krafter-kafka-external-bootstrap -n kafka \
  -o jsonpath='{.spec.ports[0].nodePort}'

# Find the node IP
kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}'
```

### Cross-namespace access

If `networkPolicies.enabled: true`, add client namespaces to the allow list:

```yaml
networkPolicies:
  allowedClientNamespaces:
    - my-app-namespace
    - another-namespace
```

---

## Day-2 Operations

### Upgrade the chart

```bash
make kafka-upgrade ENV=prod
```

Or with Helm:

```bash
helm upgrade kafka-cluster charts/kafka-cluster \
  --namespace kafka \
  -f charts/kafka-cluster/values-prod.yaml \
  --timeout 10m \
  --wait
```

### Scale brokers

Edit the overlay or set inline:

```bash
helm upgrade kafka-cluster charts/kafka-cluster \
  --namespace kafka --reuse-values \
  --set 'brokerPools[0].replicas=3' \
  --wait
```

### Add topics

Add to your values overlay or apply via `KafkaTopic` CR:

```yaml
topics:
  items:
    - name: my-new-topic
      partitions: 12
      replicas: 3
      config:
        retention.ms: "604800000"
        min.insync.replicas: "2"
```

### Add users

```yaml
users:
  items:
    - name: new-service
      authentication:
        type: scram-sha-512
      authorization:
        type: simple
        acls:
          - resource:
              type: topic
              name: "my-new-topic"
              patternType: literal
            operations: ["Read", "Write", "Describe"]
            host: "*"
```

### Enable Kafka Connect

```bash
helm upgrade kafka-cluster charts/kafka-cluster \
  --namespace kafka --reuse-values \
  --set kafkaConnect.enabled=true \
  --set 'kafkaConnect.jvmOptions.-Xmx=1024m' \
  --wait
```

### Check certificate expiry

```bash
kubectl get secret krafter-cluster-ca-cert -n kafka \
  -o jsonpath='{.data.ca\.crt}' | base64 -d | \
  openssl x509 -noout -dates
```

### Trigger manual rebalance

```bash
kubectl apply -f - <<EOF
apiVersion: kafka.strimzi.io/v1
kind: KafkaRebalance
metadata:
  name: manual-rebalance
  namespace: kafka
  labels:
    strimzi.io/cluster: krafter
spec: {}
EOF

kubectl wait kafkarebalance/manual-rebalance \
  --for=condition=ProposalReady -n kafka --timeout=120s
```

---

## Uninstalling

### Remove the Helm release

```bash
make kafka-undeploy
```

Or manually:

```bash
helm uninstall kafka-cluster -n kafka
```

### Preserved resources

These resources have `helm.sh/resource-policy: keep` and survive uninstall:

| Resource | Why |
|----------|-----|
| `Kafka` CR | Prevents accidental data loss |
| `KafkaNodePool` CRs | Preserves broker state |
| `KafkaTopic` CRs | Preserves topic definitions |
| `KafkaUser` CRs | Preserves credentials |
| `KafkaConnect` CR | Preserves connector state |

To fully remove everything:

```bash
# Delete preserved CRs
kubectl delete kafka krafter -n kafka
kubectl delete kafkanodepools --all -n kafka
kubectl delete kafkatopics --all -n kafka
kubectl delete kafkausers --all -n kafka

# Delete PVCs (THIS DESTROYS ALL DATA)
kubectl delete pvc -l strimzi.io/cluster=krafter -n kafka

# Delete namespace
kubectl delete namespace kafka
```

### Remove Strimzi CRDs (cluster-wide)

> **Warning:** This affects ALL Strimzi-managed resources in ALL namespaces.

```bash
kubectl get crd -o name | grep strimzi | xargs kubectl delete
```
