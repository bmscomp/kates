# Kates Helm Chart

Install [Kates](https://github.com/klster/kates) — Kafka Advanced Testing & Engineering Suite — on any Kubernetes cluster.

> **Schema validated** — `values.schema.json` catches invalid config at install time.

## Install

```bash
helm install kates ./charts/kates \
  --namespace kates --create-namespace \
  --set kafka.bootstrapServers=my-kafka-bootstrap.kafka.svc:9092
```

## Uninstall

```bash
helm uninstall kates -n kates
```

## Validate

```bash
helm test kates -n kates
```

## Configuration

All configuration is in [values.yaml](values.yaml). Key sections:

### Core

| Parameter | Default | Description |
|-----------|---------|-------------|
| `image.repository` | `kates` | Container image name |
| `image.tag` | `latest` | Container image tag |
| `image.pullPolicy` | `IfNotPresent` | Image pull policy |
| `replicaCount` | `1` | Number of Kates pods |
| `kafka.bootstrapServers` | `krafter-kafka-bootstrap.kafka.svc:9092` | Kafka bootstrap address |
| `engine.defaultBackend` | `native` | Benchmark engine (`native` or `trogdor`) |

### Networking

| Parameter | Default | Description |
|-----------|---------|-------------|
| `service.type` | `ClusterIP` | Service type |
| `service.port` | `8080` | Service port |
| `service.appProtocol` | `http` | Protocol hint for service mesh (Istio/Linkerd) |
| `service.nodePort` | `""` | NodePort (when type is `NodePort`) |
| `ingress.enabled` | `false` | Enable Ingress |
| `ingress.certManager.enabled` | `false` | Auto-create TLS via cert-manager |
| `ingress.certManager.issuerName` | `""` | cert-manager issuer name |
| `ingress.certManager.issuerKind` | `ClusterIssuer` | Issuer kind |
| `networkPolicy.enabled` | `false` | Enable NetworkPolicy |
| `networkPolicy.kafka.namespace` | `kafka` | Kafka egress target namespace |
| `networkPolicy.kafka.port` | `9092` | Kafka egress target port |
| `networkPolicy.prometheus.namespace` | `monitoring` | Prometheus egress target namespace |
| `networkPolicy.prometheus.port` | `9090` | Prometheus egress target port |

### Security

| Parameter | Default | Description |
|-----------|---------|-------------|
| `securityContext.runAsNonRoot` | `true` | Run pod as non-root |
| `securityContext.runAsUser` | `1000` | Pod UID |
| `containerSecurityContext.readOnlyRootFilesystem` | `true` | Read-only root FS |
| `containerSecurityContext.allowPrivilegeEscalation` | `false` | Block privilege escalation |
| `serviceAccount.create` | `true` | Create a ServiceAccount |
| `rbac.create` | `true` | Create ClusterRole/ClusterRoleBinding for Litmus CRD access |
| `rbac.extraRules` | `[]` | Additional RBAC rules to append |

### Probes & Lifecycle

| Parameter | Default | Description |
|-----------|---------|-------------|
| `probes.startup.path` | `/q/health/started` | Startup probe path |
| `probes.readiness.path` | `/api/health` | Readiness probe path |
| `probes.liveness.path` | `/q/health/live` | Liveness probe path |
| `probes.startup.failureThreshold` | `30` | Startup probe max failures |
| `probes.readiness.periodSeconds` | `10` | Readiness check interval |
| `probes.liveness.periodSeconds` | `30` | Liveness check interval |
| `lifecycle.preStopSleepSeconds` | `5` | Pre-stop sleep for graceful drain |

### Scaling & Availability

| Parameter | Default | Description |
|-----------|---------|-------------|
| `autoscaling.enabled` | `false` | Enable HPA |
| `autoscaling.behavior.scaleDown.stabilizationWindowSeconds` | `300` | Scale-down cooldown |
| `autoscaling.customMetrics` | `[]` | Custom HPA metrics (e.g., `kates_active_runs`) |
| `podDisruptionBudget.enabled` | `false` | Enable PDB |
| `podDisruptionBudget.unhealthyPodEvictionPolicy` | `IfHealthy` | Eviction policy (K8s 1.27+) |
| `strategy.type` | `RollingUpdate` | Deployment strategy |
| `strategy.rollingUpdate.maxSurge` | `1` | Max pods over desired during update |
| `strategy.rollingUpdate.maxUnavailable` | `0` | Zero-downtime updates |
| `terminationGracePeriodSeconds` | `60` | Graceful shutdown window |
| `topologySpreadConstraints` | `[]` | Pod spread across zones/nodes |

### Database

| Parameter | Default | Description |
|-----------|---------|-------------|
| `postgresql.enabled` | `true` | Deploy bundled PostgreSQL |
| `postgresql.auth.existingSecret` | `""` | Use pre-created secret for DB credentials |
| `externalDatabase.enabled` | `false` | Use an external PostgreSQL |
| `externalDatabase.existingSecret` | `""` | Use pre-created secret for external DB |

### Monitoring

| Parameter | Default | Description |
|-----------|---------|-------------|
| `metrics.serviceMonitor.enabled` | `false` | Enable Prometheus ServiceMonitor |
| `metrics.serviceMonitor.metricRelabelings` | `[]` | Metric relabeling rules |
| `metrics.serviceMonitor.relabelings` | `[]` | Target relabeling rules |
| `metrics.prometheusRule.enabled` | `false` | Enable alerting rules |
| `metrics.grafanaDashboard.enabled` | `false` | Auto-provision Grafana dashboard |

### Operations

| Parameter | Default | Description |
|-----------|---------|-------------|
| `backup.enabled` | `false` | Enable PostgreSQL backup CronJob |
| `backup.schedule` | `0 2 * * *` | Backup cron schedule |
| `backup.retention` | `7` | Days to keep backups |
| `backup.persistence.enabled` | `false` | Use PVC instead of emptyDir |
| `backup.persistence.size` | `5Gi` | PVC size |
| `backup.persistence.storageClass` | `""` | StorageClass (empty = default) |
| `backup.persistence.existingClaim` | `""` | Use an existing PVC |
| `migration.enabled` | `false` | Enable pre-upgrade migration Job |
| `cleanup.enabled` | `false` | Enable test run cleanup CronJob |
| `cleanup.schedule` | `0 4 * * 0` | Cleanup cron schedule |
| `cleanup.retentionDays` | `30` | Days to keep completed tests |

### Extensibility

| Parameter | Default | Description |
|-----------|---------|-------------|
| `extraEnv` | `[]` | Extra environment variables |
| `extraVolumes` | `[]` | Extra volumes |
| `extraVolumeMounts` | `[]` | Extra volume mounts |
| `podLabels` | `{}` | Extra pod labels |
| `podAnnotations` | `{}` | Extra pod annotations |
| `initContainers.waitForPostgres.image` | `busybox:1.36` | Init container image |

## Example: Local Kind Cluster

```bash
helm install kates ./charts/kates \
  --namespace kates --create-namespace \
  --set image.pullPolicy=Never \
  --set service.type=NodePort \
  --set service.nodePort=30083
```

## Example: Production with External DB

```bash
helm install kates ./charts/kates \
  --namespace kates --create-namespace \
  --set image.repository=ghcr.io/klster/kates \
  --set image.tag=1.0.0 \
  --set kafka.bootstrapServers=my-kafka:9092 \
  --set postgresql.enabled=false \
  --set externalDatabase.enabled=true \
  --set externalDatabase.existingSecret=my-db-secret \
  --set ingress.enabled=true \
  --set ingress.className=nginx \
  --set ingress.certManager.enabled=true \
  --set ingress.certManager.issuerName=letsencrypt-prod \
  --set ingress.hosts[0].host=kates.example.com \
  --set ingress.hosts[0].paths[0].path=/ \
  --set autoscaling.enabled=true \
  --set podDisruptionBudget.enabled=true \
  --set networkPolicy.enabled=true \
  --set metrics.serviceMonitor.enabled=true \
  --set metrics.prometheusRule.enabled=true \
  --set metrics.grafanaDashboard.enabled=true \
  --set backup.enabled=true \
  --set cleanup.enabled=true
```

## Example: EKS with ALB Ingress

```bash
helm install kates ./charts/kates \
  --namespace kates --create-namespace \
  --set image.repository=123456789.dkr.ecr.us-east-1.amazonaws.com/kates \
  --set image.tag=1.0.0 \
  --set kafka.bootstrapServers=b-1.msk-cluster.kafka.us-east-1.amazonaws.com:9092 \
  --set ingress.enabled=true \
  --set ingress.className=alb \
  --set ingress.annotations."alb\.ingress\.kubernetes\.io/scheme"=internet-facing \
  --set ingress.annotations."alb\.ingress\.kubernetes\.io/target-type"=ip \
  --set serviceAccount.annotations."eks\.amazonaws\.com/role-arn"=arn:aws:iam::role/kates
```

## Test Types

All 8 test types are pre-configured in `values.yaml` under the `tests` section. Override any parameter:

```bash
helm install kates ./charts/kates \
  --set tests.stress.numProducers=6 \
  --set tests.stress.partitions=12 \
  --set defaults.compressionType=zstd
```
