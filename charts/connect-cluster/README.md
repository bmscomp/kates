# connect-cluster

A Helm chart for deploying production-grade Strimzi Kafka Connect clusters on Kubernetes.

## Overview

This chart deploys a [Strimzi](https://strimzi.io/) `KafkaConnect` Custom Resource along with optional `KafkaConnector` resources, Prometheus alerts, Grafana dashboards, network policies, and a REST API service.

It is designed to be deployed **independently** from the core Kafka broker chart (`kafka-cluster`), giving teams separate upgrade lifecycles, isolated blast radius, and multi-tenancy support.

## Prerequisites

- Kubernetes 1.27+
- Helm 3.12+
- [Strimzi Operator](https://strimzi.io/) installed in the cluster
- A running Kafka cluster (deployed via `kafka-cluster` chart or otherwise)

## Installation

### Quick Start

```bash
helm install connect-cluster charts/connect-cluster \
  --namespace kafka \
  --create-namespace
```

By default, the chart assumes a Kafka cluster named `krafter` in the same namespace with TLS enabled on port `9093`.

### Custom Kafka Target

```bash
helm install connect-cluster charts/connect-cluster \
  --namespace kafka \
  --set kafka.bootstrapServers="my-kafka-bootstrap:9093" \
  --set kafka.tls.trustedCertificateSecret="my-cluster-ca-cert" \
  --set kafka.authentication.username="my-connect-user" \
  --set kafka.authentication.secretName="my-connect-user"
```

### Multi-Tenancy (Multiple Connect Clusters)

```bash
# Team A — CDC workloads
helm install data-team-connect charts/connect-cluster \
  --namespace kafka \
  --set groupId=data-team-connect

# Team B — Sink connectors
helm install backend-connect charts/connect-cluster \
  --namespace kafka \
  --set groupId=backend-connect
```

## Configuration Reference

### Core Settings

| Parameter | Description | Default |
|-----------|-------------|---------|
| `replicas` | Number of Connect worker replicas | `3` |
| `image` | Connect container image | `ghcr.io/bmscomp/connect:3.0.2` |
| `version` | Kafka version | `4.2.0` |
| `groupId` | Connect cluster group ID | `kates-connect-cluster` |
| `clusterDomain` | Kubernetes cluster DNS domain | `cluster.local` |
| `nameOverride` | Override chart name | `""` |
| `fullnameOverride` | Override full release name | `""` |

### Kafka Connection

| Parameter | Description | Default |
|-----------|-------------|---------|
| `kafka.bootstrapServers` | Kafka bootstrap servers | `krafter-kafka-bootstrap:9093` |
| `kafka.tls.enabled` | Enable TLS | `true` |
| `kafka.tls.trustedCertificateSecret` | CA certificate secret name | `krafter-cluster-ca-cert` |
| `kafka.tls.certificateKey` | Key name in the secret | `ca.crt` |
| `kafka.authentication.type` | Auth type | `scram-sha-512` |
| `kafka.authentication.username` | SCRAM username | `kates-connect` |
| `kafka.authentication.secretName` | Secret containing the password | `kates-connect` |

### Converters & Worker Config

```yaml
config:
  replicationFactor: 3
  keyConverter: io.apicurio.registry.utils.converter.AvroConverter
  valueConverter: io.apicurio.registry.utils.converter.AvroConverter
  keyConverterSchemasEnable: false
  valueConverterSchemasEnable: false
extraConfig:
  schema.registry.url: http://apicurio-registry:8080/apis/ccompat/v7
  producer.acks: "all"
  consumer.auto.offset.reset: earliest
```

### Schema Registry (Apicurio)

| Parameter | Description | Default |
|-----------|-------------|---------|
| `schemaRegistry.enabled` | Enable schema registry integration | `false` |
| `schemaRegistry.serviceName` | Registry service name | `apicurio-apicurio-registry` |
| `schemaRegistry.port` | Registry port | `80` |
| `schemaRegistry.path` | Compatibility API path | `/apis/ccompat/v7` |

The `schema.registry.url` is automatically computed as:
`http://<serviceName>.<namespace>.svc.<clusterDomain>:<port><path>`

### JVM & Resources

```yaml
jvmOptions:
  -Xms: 1024m
  -Xmx: 1024m
  gcLoggingEnabled: true
  javaSystemProperties:
    - name: com.sun.management.jmxremote
      value: "true"

resources:
  requests:
    memory: 2Gi
    cpu: 1000m
  limits:
    memory: 4Gi
    cpu: 4000m
```

### Secret Access (Strimzi 1.0.0+)

The `KubernetesSecretConfigProvider` is always enabled. Reference Kubernetes secrets
in connector configs using:

```yaml
database.password: "${secrets:<namespace>/<secret-name>:<key>}"
```

The Connect ServiceAccount is automatically granted RBAC permissions to read secrets
in its namespace.

### Connectors

Declarative `KafkaConnector` CRs with global and per-connector auto-restart:

```yaml
# Global defaults (applied to all connectors unless overridden)
autoRestart:
  enabled: true
  maxRestarts: 10

connectors:
  - name: cdc-orders
    class: io.debezium.connector.postgresql.PostgresConnector
    tasksMax: 1
    state: running
    autoRestart:        # per-connector override
      enabled: true
      maxRestarts: 20
    config:
      database.hostname: postgresql.database.svc
      database.port: "5432"
      database.user: "${file:/mnt/pg-credentials/username}"
      database.password: "${file:/mnt/pg-credentials/password}"
      database.dbname: orders
      topic.prefix: cdc.orders
```

### Strimzi Build (Custom Plugins)

Let Strimzi build a custom Connect image with plugins (alternative to pre-built images):

```yaml
build:
  output:
    type: docker
    image: my-registry/my-connect:latest
    pushSecret: my-registry-credentials
  plugins:
    - name: debezium-postgres
      artifacts:
        - type: maven
          group: io.debezium
          artifact: debezium-connector-postgres
          version: 2.5.0.Final
    - name: apicurio-converter
      artifacts:
        - type: maven
          group: io.apicurio
          artifact: apicurio-registry-distro-connect-converter
          version: 2.6.5.Final
```

### Scheduling & HA

| Parameter | Description | Default |
|-----------|-------------|---------|
| `topologySpreadConstraints.enabled` | Spread workers across AZs | `true` |
| `topologySpreadConstraints.maxSkew` | Max pod imbalance | `1` |
| `topologySpreadConstraints.topologyKey` | Topology key | `topology.kubernetes.io/zone` |
| `topologySpreadConstraints.whenUnsatisfiable` | Policy | `DoNotSchedule` |
| `podAntiAffinity.enabled` | Anti-affinity across hosts | `true` |
| `podAntiAffinity.topologyKey` | Anti-affinity key | `kubernetes.io/hostname` |
| `podDisruptionBudget.maxUnavailable` | Max unavailable during rollout | `1` |
| `priorityClassName` | Pod priority class | `system-cluster-critical` |
| `rack.enabled` | Rack awareness | `true` |
| `tolerations` | Pod tolerations | `[]` |

### RBAC & ServiceAccount

| Parameter | Description | Default |
|-----------|-------------|---------|
| `serviceAccount.create` | Create a dedicated ServiceAccount | `true` |
| `serviceAccount.annotations` | SA annotations (e.g. IRSA/Workload Identity) | `{}` |
| `serviceAccount.name` | Override SA name | `""` (defaults to fullname) |

### REST API Exposure

Expose the Connect REST API (port 8083) via a Kubernetes Service and optional Ingress:

```yaml
restApi:
  service:
    enabled: true
    type: ClusterIP
    port: 8083
  ingress:
    enabled: true
    className: nginx
    annotations:
      nginx.ingress.kubernetes.io/rewrite-target: /
    hosts:
      - host: connect.example.com
        paths:
          - path: /
            pathType: Prefix
    tls:
      - secretName: connect-tls
        hosts:
          - connect.example.com
```

### Observability

#### Prometheus Alerts

| Parameter | Description | Default |
|-----------|-------------|---------|
| `alerts.enabled` | Create PrometheusRule | `true` |
| `alerts.labels` | Labels for rule discovery | `{release: kafka}` |

Included alerts:
- **KafkaConnectTaskFailed** (critical) — Failed tasks for 2+ minutes
- **KafkaConnectWorkerDown** (critical) — Worker reporting 0 connectors
- **KafkaConnectTaskCountMismatch** (critical) — Expected vs running task mismatch
- **KafkaConnectRebalanceStorm** (warning) — Excessive rebalance rate
- **KafkaConnectHighErrorRate** (warning) — Task error rate above 1/s
- **KafkaConnectRebalanceTooLong** (warning) — Rebalance exceeding 2 minutes
- **KafkaConnectWorkerHeapHigh** (warning) — JVM heap above 85%
- **KafkaConnectSourceLag** (warning) — Source connector polling 0 records for 15+ min

#### PodMonitors

| Parameter | Description | Default |
|-----------|-------------|---------|
| `podMonitors.enabled` | Create PodMonitor | `true` |
| `podMonitors.labels` | Labels for discovery | `{release: kafka}` |

#### Grafana Dashboards

| Parameter | Description | Default |
|-----------|-------------|---------|
| `dashboards.enabled` | Deploy dashboard ConfigMap | `true` |
| `dashboards.namespace` | Target Grafana namespace | release namespace |
| `dashboards.label` | ConfigMap label for Grafana sidecar | `grafana_dashboard` |
| `dashboards.labelValue` | Label value | `"1"` |

### Network Policies

| Parameter | Description | Default |
|-----------|-------------|---------|
| `databaseEgress` | Cross-namespace egress rules for databases | `[]` |

```yaml
databaseEgress:
  - namespace: database
    port: 5432
    podSelector:
      app.kubernetes.io/name: postgresql
```

### Logging & Tracing

| Parameter | Description | Default |
|-----------|-------------|---------|
| `logging.type` | `external` or `inline` | `external` |
| `tracing.enabled` | Enable distributed tracing | `true` |
| `tracing.type` | Tracing type | `opentelemetry` |

### Probes

| Parameter | Description | Default |
|-----------|-------------|---------|
| `readinessProbe.initialDelaySeconds` | Readiness delay | `15` |
| `readinessProbe.failureThreshold` | Readiness failures | `10` |
| `livenessProbe.initialDelaySeconds` | Liveness delay | `15` |
| `livenessProbe.failureThreshold` | Liveness failures | `20` |

## Helm Tests

Run the built-in health check after deployment:

```bash
helm test connect-cluster -n kafka
```

The test pod curls the Connect REST API to verify:
1. REST API reachability (HTTP 200)
2. Cluster info retrieval
3. Connector listing
4. Installed plugin discovery

## Upgrading

```bash
helm upgrade connect-cluster charts/connect-cluster \
  --namespace kafka \
  --reuse-values \
  --set image="ghcr.io/bmscomp/connect:3.1.0"
```

> **Note:** Upgrading the Connect chart has zero impact on the Kafka brokers since they are managed by a separate chart.
