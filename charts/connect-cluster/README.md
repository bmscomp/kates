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

### Managed KafkaUser

Set `kafkaUser.create=true` and the chart provisions everything the Connect cluster needs to authenticate — the Strimzi `KafkaUser` (in the Kafka namespace), least-privilege ACLs derived from chart values, and the credentials Secret in the Connect namespace:

```yaml
kafkaUser:
  create: true
  authorization:
    mode: auto          # ACLs from groupId, internal topics, exactly-once, topicGrants
  topicGrants:          # connector data topics
    - name: cdc
      patternType: prefix
  secretSync:
    method: job         # or "reflector" if kubernetes-reflector is installed
```

Auto mode grants: internal topics (`<groupId>-offsets/configs/status`), worker group `<groupId>` + `connect-*` sink groups, declared `topicGrants`, `transactionalId <groupId>*` when `exactly.once.source.support` is enabled, and cluster `Describe`. Use `mode: custom` + `acls:` for verbatim ACLs. Requires the Strimzi User Operator; supported auth types: `scram-sha-512`, `scram-sha-256`, `tls`.

When Kafka and Connect are in different namespaces, a post-install/upgrade hook Job copies the generated Secret into the Connect namespace (re-runs on upgrades to pick up rotation). With `method: reflector` the Secret is annotated for [kubernetes-reflector](https://github.com/emberstack/kubernetes-reflector) instead.

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
| `schemaRegistry.namespace` | Registry namespace | Kafka namespace |
| `schemaRegistry.podSelector` | Pod labels for NetworkPolicy egress | `{app.kubernetes.io/name: apicurio-registry}` |

The `schema.registry.url` is automatically computed as:
`http://<serviceName>.<registryNamespace>.svc.<clusterDomain>:<port><path>`

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
in its namespace. To restrict access to specific secrets (recommended for production):

```yaml
rbac:
  secretNames:
    - kates-connect
    - connect-pg-credentials
```

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

### Autoscaling (HPA)

`KafkaConnect` exposes the scale subresource, so a standard `autoscaling/v2` HPA can drive worker count. When enabled, the chart stops rendering `spec.replicas` so upgrades don't fight the autoscaler.

```yaml
autoscaling:
  enabled: true
  minReplicas: 3
  maxReplicas: 10
  targetCPUUtilizationPercentage: 80
```

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
- **KafkaConnectRebalanceTooLong** (warning) — Worker stuck rebalancing for 5+ minutes
- **KafkaConnectWorkerHeapHigh** (warning) — JVM heap above 85%
- **KafkaConnectSourceLag** (warning) — Source connector polling 0 records for 15+ min

#### PodMonitors

| Parameter | Description | Default |
|-----------|-------------|---------|
| `podMonitors.enabled` | Create PodMonitor | `true` |
| `podMonitors.labels` | Labels for discovery | `{release: kafka}` |

#### Metrics ConfigMap

The chart ships its own JMX Prometheus Exporter rules (Connect worker, rebalance, connector, and task metrics), so it works standalone in any namespace:

| Parameter | Description | Default |
|-----------|-------------|---------|
| `metricsConfig.create` | Create the metrics ConfigMap in-chart | `true` |
| `metricsConfig.configMapName` | Name (or existing ConfigMap when `create: false`) | `<fullname>-metrics` |
| `metricsConfig.configMapKey` | Key within the ConfigMap | `kafka-metrics-config.yml` |

#### Grafana Dashboards

| Parameter | Description | Default |
|-----------|-------------|---------|
| `dashboards.enabled` | Deploy dashboard ConfigMap | `true` |
| `dashboards.namespace` | Target Grafana namespace | release namespace |
| `dashboards.label` | ConfigMap label for Grafana sidecar | `grafana_dashboard` |
| `dashboards.labelValue` | Label value | `"1"` |

### Network Policies

**Default-deny by default.** The chart ships a deny-all Ingress+Egress policy for the Connect pods; every allowed flow is an explicit, individually configurable rule. Anything not listed is blocked — declare connector endpoints via `databaseEgress` or `networkPolicy.extraEgress`.

| Parameter | Description | Default |
|-----------|-------------|---------|
| `networkPolicy.enabled` | Create NetworkPolicies | `true` |
| `networkPolicy.defaultDeny.enabled` | Deny-all policy for Connect pods | `true` |
| `networkPolicy.dns.*` | DNS egress; pin `namespaceSelector`/`podSelector` to kube-dns on strict clusters | any dest, port 53 |
| `networkPolicy.apiServer.*` | API server egress; pin `ipBlock` to the control-plane CIDR | any dest, 443/6443 |
| `networkPolicy.workerToWorker.enabled` | 8083 leader forwarding | `true` |
| `networkPolicy.kafka.ports` | Broker egress ports | `[9092, 9093]` |
| `networkPolicy.monitoring.*` | Metrics scrape ingress | `monitoring` ns, 9404 |
| `networkPolicy.restApi.clients` | Clients allowed on 8083 (no catch-all; `allowAll: true` restores open behavior) | kates UI |
| `networkPolicy.tracing.*` | OTLP egress (port parsed from `tracing.endpoint`) | auto |
| `networkPolicy.extraIngress` / `extraEgress` | Raw rule fragments, appended verbatim | `[]` |
| `databaseEgress` | Cross-namespace egress rules for databases (`createIngressPolicy: false` skips the reciprocal policy in the DB namespace) | `[]` |

Deprecated (still honored, win when set): `kafkaPorts` → `kafka.ports`, `monitoringNamespace` → `monitoring.namespace`, `restApiClients` → `restApi.clients`.

> **Upgrade note (1.2.0):** default-deny ships enabled and the former `namespaceSelector: {}` catch-all on 8083 was removed (kubelet probes bypass NetworkPolicy — it only opened the REST API cluster-wide). In-cluster REST clients must be listed in `networkPolicy.restApi.clients`; undeclared connector egress will be blocked.

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
| `tracing.endpoint` | OTLP collector endpoint (required for traces to be exported) | `""` |
| `tracing.serviceName` | `OTEL_SERVICE_NAME` | release fullname |

```yaml
tracing:
  enabled: true
  endpoint: http://otel-collector.observability.svc:4317
```

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
