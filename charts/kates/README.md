# Kates Helm Chart

Install [Kates](https://github.com/klster/kates) — Kafka Advanced Testing & Engineering Suite — on any Kubernetes cluster.

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

## Configuration

All configuration is in [values.yaml](values.yaml). Key sections:

| Parameter | Default | Description |
|-----------|---------|-------------|
| `image.repository` | `kates` | Container image name |
| `image.tag` | `latest` | Container image tag |
| `image.pullPolicy` | `IfNotPresent` | Image pull policy |
| `replicaCount` | `1` | Number of Kates pods |
| `kafka.bootstrapServers` | `krafter-kafka-bootstrap.kafka.svc:9092` | Kafka bootstrap address |
| `engine.defaultBackend` | `native` | Benchmark engine (`native` or `trogdor`) |
| `service.type` | `ClusterIP` | Service type (`ClusterIP`, `NodePort`, `LoadBalancer`) |
| `service.port` | `8080` | Service port |
| `service.nodePort` | `""` | NodePort (when type is `NodePort`) |
| `ingress.enabled` | `false` | Enable Ingress |
| `postgresql.enabled` | `true` | Deploy bundled PostgreSQL |
| `externalDatabase.enabled` | `false` | Use an external PostgreSQL |
| `serviceAccount.create` | `true` | Create a ServiceAccount |
| `autoscaling.enabled` | `false` | Enable HPA |
| `podDisruptionBudget.enabled` | `false` | Enable PDB |
| `metrics.serviceMonitor.enabled` | `false` | Enable Prometheus ServiceMonitor |

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
  --set externalDatabase.host=my-rds.amazonaws.com \
  --set externalDatabase.password=secret \
  --set ingress.enabled=true \
  --set ingress.className=nginx \
  --set ingress.hosts[0].host=kates.example.com \
  --set ingress.hosts[0].paths[0].path=/ \
  --set autoscaling.enabled=true \
  --set podDisruptionBudget.enabled=true \
  --set metrics.serviceMonitor.enabled=true
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
