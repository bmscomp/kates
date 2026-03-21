# Kafka Cluster Helm Chart

Production-ready Apache Kafka deployment on Kubernetes using the [Strimzi](https://strimzi.io/) operator with KRaft consensus, zone-aware broker pools, SCRAM-SHA-512 authentication, and full observability.

## Features

- **KRaft Mode** — No ZooKeeper. Native Kafka metadata management via Raft consensus
- **Zone-Aware Broker Pools** — Dedicated `KafkaNodePool` per availability zone for rack-aware replication
- **Security** — SCRAM-SHA-512 + TLS listeners, ACL authorization, per-user quotas, zero-trust NetworkPolicies
- **Observability** — JMX Prometheus exporter, 5 Grafana dashboards, PrometheusRules, PodMonitors
- **Operations** — Cruise Control auto-rebalancing, Drain Cleaner, automated certificate rotation
- **Resilience** — PodDisruptionBudgets, topology spread constraints, graceful preStop hooks
- **Optional** — Tiered Storage (S3/MinIO), Kafka Connect, Velero backups, Kyverno pod security

## Prerequisites

| Requirement | Version |
|-------------|---------|
| Kubernetes | ≥ 1.25 |
| Helm | ≥ 3.12 |
| Strimzi Operator | 0.51.0 (bundled as subchart) |

## Quick Start

### 1. Install with the bundled Strimzi operator

```bash
helm install kafka-cluster charts/kafka-cluster \
  --namespace kafka --create-namespace \
  --wait --timeout 600s
```

### 2. Install without the operator (already deployed)

```bash
helm install kafka-cluster charts/kafka-cluster \
  --namespace kafka --create-namespace \
  --set strimziOperator.enabled=false \
  --wait --timeout 600s
```

### 3. Upgrade

```bash
helm upgrade kafka-cluster charts/kafka-cluster \
  --namespace kafka --reuse-values
```

### 4. Run tests

```bash
helm test kafka-cluster --namespace kafka
```

Tests run in 3 tiers:
1. **Connectivity** — Bootstrap TCP + Kafka CR Ready + broker pod health
2. **Produce/Consume** — Round-trip with SCRAM authentication
3. **Authorization** — KafkaUser readiness + SCRAM secret verification

### 5. Uninstall

```bash
helm uninstall kafka-cluster --namespace kafka
```

> **Note:** Resources annotated with `helm.sh/resource-policy: keep` (Kafka CR, NodePools, Topics, Users) are preserved after uninstall to prevent data loss. Delete them manually if needed.

## Environment Overlays

The chart ships with 3 environment-specific overlays:

```bash
# Development (minimal resources, single replicas)
helm install kafka-cluster charts/kafka-cluster -f charts/kafka-cluster/values-dev.yaml

# Staging (production security, moderate resources)
helm install kafka-cluster charts/kafka-cluster -f charts/kafka-cluster/values-staging.yaml

# Production (full HA, enforced PSP, backup with PVC)
helm install kafka-cluster charts/kafka-cluster -f charts/kafka-cluster/values-prod.yaml
```

| Setting | Dev | Staging | Prod |
|---------|-----|---------|------|
| Controller replicas | 1 | 3 | 3 |
| Controller storage | 1Gi | 10Gi | 10Gi |
| Broker storage | 10Gi | 100Gi | 100Gi |
| NetworkPolicies | ❌ | ✅ | ✅ |
| Pod Security | ❌ | Enforce | Enforce |
| RBAC | ❌ | ✅ | ✅ |
| Backup persistence | ❌ | ✅ (20Gi) | ✅ (50Gi) |
| PreStop hook | 5s | 15s | 15s |

## Architecture

```
┌─────────────────────────────────────────────────────┐
│                    kafka namespace                   │
│                                                     │
│  ┌─────────────┐  ┌──────────┐  ┌──────────┐       │
│  │ controller-0│  │broker-α-0│  │broker-σ-0│       │
│  │ controller-1│  │  zone:α  │  │  zone:σ  │       │
│  │ controller-2│  └──────────┘  └──────────┘       │
│  │  (KRaft)    │  ┌──────────┐                      │
│  └─────────────┘  │broker-γ-0│                      │
│                   │  zone:γ  │                      │
│  ┌──────────────┐ └──────────┘                      │
│  │Entity Operator│                                  │
│  │ ├─ TopicOp   │  ┌──────────────┐                 │
│  │ └─ UserOp    │  │Cruise Control│                 │
│  └──────────────┘  └──────────────┘                 │
│                                                     │
│  ┌──────────────┐  ┌──────────────┐                 │
│  │Kafka Exporter│  │Drain Cleaner │                 │
│  └──────────────┘  └──────────────┘                 │
└─────────────────────────────────────────────────────┘
         ▲               ▲               ▲
    port 9092        port 9093       port 9094
   SASL_PLAINTEXT    SASL_TLS      NodePort+TLS
```

## Configuration Reference

### Cluster Identity

| Parameter | Description | Default |
|-----------|-------------|---------|
| `clusterName` | Kafka cluster name (Kubernetes resource name) | `krafter` |
| `kafkaVersion` | Apache Kafka version | `4.1.1` |
| `strimziVersion` | Strimzi operator version | `0.51.0` |

### Listeners

Define Kafka listeners in `kafka.listeners`:

```yaml
kafka:
  listeners:
    - name: plain           # Internal SCRAM (no TLS)
      port: 9092
      type: internal
      tls: false
      authentication:
        type: scram-sha-512
    - name: tls             # Internal mutual TLS
      port: 9093
      type: internal
      tls: true
      authentication:
        type: tls
    - name: external        # NodePort with TLS + SCRAM
      port: 9094
      type: nodeport
      tls: true
      authentication:
        type: scram-sha-512
      configuration:
        bootstrap:
          nodePort: 32100
```

Supported listener types: `internal`, `route`, `loadbalancer`, `nodeport`, `ingress`, `cluster-ip`.
Supported auth types: `scram-sha-512`, `tls`, `tls-external`.

### Authorization & Super Users

```yaml
kafka:
  authorization:
    type: simple       # ACL-based authorization
    superUsers:
      - kates-backend  # Bypass ACLs for this user
```

Supported types: `simple`, `opa`, `keycloak`, `custom`.

### Broker Configuration

Kafka broker properties are set via `kafka.config`:

```yaml
kafka:
  config:
    min.insync.replicas: 2
    default.replication.factor: 3
    log.retention.hours: 24
    log.retention.bytes: 10737418240   # 10 GiB
    auto.create.topics.enable: false
    message.max.bytes: 10485760        # 10 MiB
    group.share.enable: true           # Kafka 4.x Share Groups
```

### KRaft Controllers

| Parameter | Description | Default |
|-----------|-------------|---------|
| `controllers.replicas` | Number of KRaft controllers (1-9) | `3` |
| `controllers.storage.size` | PVC size per controller | `5Gi` |
| `controllers.storage.class` | StorageClass name | `""` |
| `controllers.jvmOptions.-Xms` | JVM initial heap | `512m` |
| `controllers.jvmOptions.-Xmx` | JVM max heap | `512m` |
| `controllers.resources.requests.memory` | Memory request | `1Gi` |
| `controllers.resources.limits.cpu` | CPU limit | `1000m` |

### Zone-Aware Broker Pools

Each pool creates a `KafkaNodePool` CR pinned to an availability zone:

```yaml
brokerPools:
  - name: brokers-alpha
    zone: alpha
    replicas: 1
    storageSize: 50Gi
    storageClass: local-storage-alpha

  - name: brokers-sigma
    zone: sigma
    replicas: 1
    storageSize: 50Gi
    storageClass: local-storage-sigma
```

Shared defaults for all pools are in `brokerDefaults`:

| Parameter | Description | Default |
|-----------|-------------|---------|
| `brokerDefaults.jvmOptions.-Xmx` | Max heap per broker | `2048m` |
| `brokerDefaults.resources.requests.memory` | Memory request | `4Gi` |
| `brokerDefaults.resources.limits.cpu` | CPU limit | `2000m` |
| `brokerDefaults.deleteClaim` | Delete PVC on pool removal | `false` |

### Topics

Declarative topic management via `KafkaTopic` CRs:

```yaml
topics:
  - name: my-topic
    partitions: 6
    replicas: 3
    config:
      retention.ms: "172800000"      # 2 days
      min.insync.replicas: "2"
      cleanup.policy: delete
      compression.type: lz4
```

### Users

Declarative user management with SCRAM/TLS auth, quotas, and fine-grained ACLs:

```yaml
users:
  - name: my-app
    authentication:
      type: scram-sha-512
    quotas:
      producerByteRate: 52428800     # 50 MB/s
      consumerByteRate: 104857600    # 100 MB/s
      requestPercentage: 25
    authorization:
      type: simple
      acls:
        - resource:
            type: topic
            name: "my-topic"
            patternType: literal
          operations: ["Read", "Write", "Describe"]
          host: "*"
```

### Certificate Authority

Automated certificate rotation with zero-downtime:

| Parameter | Description | Default |
|-----------|-------------|---------|
| `kafka.clusterCa.validityDays` | CA certificate lifetime | `1825` (5 years) |
| `kafka.clusterCa.renewalDays` | Renew before expiry | `180` (6 months) |
| `kafka.clusterCa.certificateExpirationPolicy` | On renewal: `replace-key` or `renew-certificate` | `replace-key` |

The same config applies to `kafka.clientsCa`.

### Cruise Control

Automated partition rebalancing:

| Parameter | Description | Default |
|-----------|-------------|---------|
| `cruiseControl.enabled` | Enable Cruise Control | `true` |
| `cruiseControl.brokerCapacity.cpu` | Broker CPU capacity | `2000m` |
| `cruiseControl.brokerCapacity.inboundNetwork` | Network capacity | `50MiB/s` |
| `cruiseControl.autoRebalance` | Auto-rebalance triggers | `add-brokers`, `remove-brokers` |

### Lifecycle & Graceful Shutdown

| Parameter | Description | Default |
|-----------|-------------|---------|
| `lifecycle.preStopSleepSeconds` | Seconds to wait before SIGTERM (0-120) | `15` |

The chart auto-calculates `terminationGracePeriodSeconds` = `preStopSleepSeconds + 30`.

### RBAC

| Parameter | Description | Default |
|-----------|-------------|---------|
| `rbac.create` | Create ServiceAccount, Role, RoleBinding | `true` |
| `rbac.extraRules` | Additional RBAC rules to append | `[]` |
| `serviceAccount.annotations` | Annotations on the ServiceAccount | `{}` |

Extension example for Litmus Chaos integration:

```yaml
rbac:
  extraRules:
    - apiGroups: ["litmuschaos.io"]
      resources: ["chaosengines", "chaosexperiments"]
      verbs: ["get", "list", "create", "delete"]
```

### Network Policies

Zero-trust network segmentation:

| Parameter | Description | Default |
|-----------|-------------|---------|
| `networkPolicies.enabled` | Enable default-deny + allow rules | `true` |
| `networkPolicies.allowedClientNamespaces` | Namespaces allowed to reach brokers | `[kates, litmus]` |
| `networkPolicies.monitoringNamespace` | Namespace for Prometheus scrape access | `monitoring` |

Created policies: `default-deny`, `allow-dns`, `kafka-brokers`, `kafka-controllers`, `strimzi-operator`, `kafka-ui`, `cruise-control`, `strimzi-drain-cleaner`, `kafka-connect`, `kafka-mirror-maker`, `entity-operator`, `kafka-exporter` (12 total).

### Observability

#### Grafana Dashboards

| Parameter | Description | Default |
|-----------|-------------|---------|
| `dashboards.enabled` | Deploy dashboard ConfigMaps | `true` |
| `dashboards.namespace` | Target Grafana namespace | `monitoring` |
| `dashboards.brokerDashboard` | Broker metrics (handlers, ISR, JVM) | `true` |
| `dashboards.kraftDashboard` | KRaft quorum metrics | `true` |
| `dashboards.cruiseControlDashboard` | CC balancedness + proposals | `true` |
| `dashboards.connectDashboard` | Connect task/connector metrics | `false` |

#### Prometheus Alerts

| Parameter | Description | Default |
|-----------|-------------|---------|
| `alerts.enabled` | Create PrometheusRule | `true` |
| `alerts.labels` | Labels for rule discovery | `{release: monitoring}` |

#### PodMonitors

| Parameter | Description | Default |
|-----------|-------------|---------|
| `podMonitors.enabled` | Create PodMonitors for Prometheus | `true` |
| `podMonitors.labels` | Labels for discovery | `{release: monitoring}` |

### Tiered Storage (S3/MinIO)

```yaml
tieredStorage:
  enabled: true
  s3:
    bucketName: kafka-tiered-storage
    region: us-east-1
    endpointUrl: "http://minio.velero.svc:9000"
    pathStyleAccessEnabled: true
  credentials:
    existingSecret: ""              # Use an existing secret, or...
    accessKeyId: minio              # ...provide inline credentials
    secretAccessKey: minio123
  retention:
    localRetentionMs: 86400000      # Keep 1 day locally
```

### Kafka Connect

```yaml
kafkaConnect:
  enabled: true
  replicas: 2
  groupId: my-connect-cluster
  config:
    replicationFactor: 3
  resources:
    requests:
      memory: 512Mi
    limits:
      memory: 1Gi
  build:
    output:
      type: docker
      image: my-registry/my-connect:latest
    plugins: []
```

### Backup (Velero)

| Parameter | Description | Default |
|-----------|-------------|---------|
| `backup.enabled` | Create Velero Schedule + pre-upgrade Backup | `false` |
| `backup.schedule` | Cron schedule for daily backups | `0 2 * * *` |
| `backup.ttl` | Backup retention | `168h0m0s` (7 days) |
| `backup.persistence.enabled` | Create PVC for backup storage | `false` |
| `backup.persistence.size` | PVC size | `20Gi` |
| `backup.persistence.existingClaim` | Use existing PVC | `""` |

### Strimzi Operator (Subchart)

| Parameter | Description | Default |
|-----------|-------------|---------|
| `strimziOperator.enabled` | Deploy the Strimzi operator subchart | `true` |
| `strimzi-kafka-operator.replicas` | Operator replicas | `1` |
| `strimzi-kafka-operator.watchAnyNamespace` | Watch all namespaces | `true` |
| `strimzi-kafka-operator.resources.limits.memory` | Operator memory limit | `512Mi` |

Set `strimziOperator.enabled: false` if the operator is already installed in the cluster.

### Drain Cleaner

| Parameter | Description | Default |
|-----------|-------------|---------|
| `drainCleaner.enabled` | Deploy Strimzi Drain Cleaner | `true` |
| `drainCleaner.image` | Container image | `quay.io/strimzi/drain-cleaner:0.51.0` |

### Pod Security

| Parameter | Description | Default |
|-----------|-------------|---------|
| `podSecurityPolicy.enabled` | Create Kyverno ClusterPolicy | `false` |
| `podSecurityPolicy.action` | `Audit` or `Enforce` | `Audit` |

## Makefile Targets

```bash
make kafka-chart-deps       # Download dependencies
make kafka-chart-lint       # Lint all environments
make kafka-chart-template   # Render templates → .build/kafka-rendered.yaml
make kafka-chart-package    # Package → .build/kafka-cluster-0.1.0.tgz
make kafka-chart-push       # Push to OCI registry
make kafka-chart-test       # Run helm test against live cluster
make kafka-chart-all        # deps + lint + template + package
```

Override the registry: `make kafka-chart-push CHART_REGISTRY=oci://my-registry/charts`

## Connecting to the Cluster

### From inside the cluster (SCRAM)

```bash
bootstrap: <clusterName>-kafka-bootstrap.<namespace>.svc:9092
security.protocol: SASL_PLAINTEXT
sasl.mechanism: SCRAM-SHA-512
sasl.jaas.config: ...ScramLoginModule required username="my-user" password="<from-secret>";
```

Retrieve the SCRAM password:

```bash
kubectl get secret my-user -n kafka \
  -o jsonpath='{.data.password}' | base64 -d
```

### From outside the cluster (NodePort + TLS)

```bash
bootstrap: <node-ip>:32100
security.protocol: SASL_SSL
sasl.mechanism: SCRAM-SHA-512
ssl.truststore.location: /path/to/truststore.p12
```

Extract the cluster CA certificate:

```bash
kubectl get secret <clusterName>-cluster-ca-cert -n kafka \
  -o jsonpath='{.data.ca\.crt}' | base64 -d > ca.crt
```

## Troubleshooting

### Cluster not becoming Ready

```bash
kubectl get kafka <clusterName> -n kafka -o yaml | yq '.status'
kubectl get pods -n kafka -l strimzi.io/cluster=<clusterName>
kubectl logs -n kafka <pod-name> --tail=50
```

### SASL handshake failures

If you see `Unexpected Kafka request of type METADATA during SASL handshake` in broker logs, a client is connecting without SASL to a SASL-protected listener. Verify:

```bash
# Check listener configuration
kubectl get kafka <clusterName> -n kafka -o jsonpath='{.spec.kafka.listeners}' | python3 -m json.tool

# Check client security.protocol matches the listener
```

### CRD version mismatch

If Strimzi CRDs are outdated, the CRD upgrade hook runs automatically on `helm install`. To run manually:

```bash
kubectl apply -f https://strimzi.io/install/latest?namespace=kafka --server-side
```

### Rolling restart stuck

Check PodDisruptionBudget:

```bash
kubectl get pdb -n kafka
kubectl describe pdb <clusterName>-kafka -n kafka
```

## Schema Validation

The chart includes `values.schema.json` for install-time validation:

```bash
# This will fail with a clear error:
helm install kafka-cluster charts/kafka-cluster --set controllers.replicas=-1
# Error: values don't meet the specifications of the schema:
# - at '/controllers/replicas': minimum: got -1, want 1

helm install kafka-cluster charts/kafka-cluster --set lifecycle.preStopSleepSeconds=999
# Error: at '/lifecycle/preStopSleepSeconds': maximum: got 999, want 120
```
