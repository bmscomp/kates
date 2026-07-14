# Troubleshooting Index

A consolidated index of troubleshooting procedures from across the book. Jump to the relevant section for step-by-step diagnosis and fixes.

## Kafka Cluster

| Symptom | Likely Cause | Chapter |
|---------|-------------|---------|
| Strimzi operator `CrashLoopBackOff` with `UnsupportedVersionException` | Local chart has mismatched Kafka image map | [Kafka Deployment Engineering](15-kafka-deployment.md#strimzi-operator-crashloopbackoff) |
| Brokers crash with `ConfigException: Invalid value -1 for local.retention.bytes` | Kafka 4.1+ tightened validation — `-1` rejected when `retention.bytes` is set | [Kafka Deployment Engineering](15-kafka-deployment.md#brokers-crash-with-configexception) |
| Brokers crash immediately with `remote.log.storage.system.enable=true` | Tiered storage enabled without remote storage manager plugin JAR | [Kafka Deployment Engineering](15-kafka-deployment.md#brokers-crash-with-remotelogmanager) |
| `KafkaActiveControllerCount != 1` alert | Controller quorum lost or election in progress | [Kafka Deployment Engineering](15-kafka-deployment.md#prometheus-alerts) |
| Under-replicated partitions for extended period | Broker disk I/O saturated, network issues, or follower falling behind | [The Cluster Under Test](03-cluster.md#failure-tolerance-matrix) |
| Cruise Control `unsupported goals` error | Goals list doesn't match Strimzi's default goals | [Kafka Deployment Engineering](15-kafka-deployment.md#cruise-control-goal-mismatch) |
| Kafka CR stuck on `NotReady` (often with `UnforceableProblem`) | Strimzi CRDs missing, insufficient resources, or operator egress blocked by `generateNetworkPolicy` / isolated topology NetworkPolicy missing DNS/API server egress — can't reach controllers | [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md#kafka-cr-stuck-on-notready), [Kafka Deployment Engineering](15-kafka-deployment.md#strimzi-operator-cannot-determine-active-controller) |

## Kafka Connectivity

| Symptom | Likely Cause | Chapter |
|---------|-------------|---------|
| Kafka UI `CreateContainerConfigError` — secret not found | `KafkaUser` not applied before UI deployment | [Kafka Deployment Engineering](15-kafka-deployment.md#kafka-ui-createcontainerconfigerror) |
| Kates can't connect to Kafka | Wrong bootstrap address or NetworkPolicy blocking | [Deployment Guide](12-deployment.md#kates-cant-connect-to-kafka) |
| SCRAM authentication failure | Password rotated or KafkaUser not reconciled | [Security & Compliance](17-security.md#scram-sha-512) |
| Connection timeout from new namespace | Missing NetworkPolicy entry for the new namespace | [Security & Compliance](17-security.md#testing-network-policies) |
| `KafkaUser` secrets never created | Entity Operator (User Operator) only starts after the Kafka CR reaches `Ready` | [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md#user-secrets-not-appearing) |

## Kafka Connect

| Symptom | Likely Cause | Chapter |
|---------|-------------|---------|
| `KafkaConnector` status `FAILED` with `DebeziumException` | Wrong database credentials, replication slot still held, `wal_level` not `logical`, or `schema.include.list` matching no tables | [Operating Kafka Connect](operating-kafka-connect.md#connector-stuck-in-failed-state) |
| Connect cluster stuck in `REBALANCING` — `KafkaConnectRebalanceTooLong` alert fires | Workers crashing mid-rebalance, NetworkPolicy blocking inter-worker traffic on port 8083, or OOM kills during task assignment | [Operating Kafka Connect](operating-kafka-connect.md#rebalancing-takes-too-long) |
| Connect worker pods restart with `OOMKilled` | Container memory limit under 2× the JVM heap — off-heap memory pushes usage over the limit | [Operating Kafka Connect](operating-kafka-connect.md#connect-workers-oomkilled) |
| PostgreSQL disk usage grows while a connector is down or paused | Replication slot retains WAL segments until the connector drains them | [Operating Kafka Connect](operating-kafka-connect.md#replication-slot-wal-retention-growing) |
| `helm upgrade` of the Connect chart hangs or fails with `validation FAILED` | Pre-install hook found missing required fields in a connector config | [Operating Kafka Connect](operating-kafka-connect.md#validation-hook-blocks-deployment) |

## Performance Issues

| Symptom | Likely Cause | Chapter |
|---------|-------------|---------|
| P99 latency regression between test runs | Partition hotspot, GC pauses, or ISR changes | [Recipes & Patterns — Recipe 4](14-recipes.md#recipe-4-investigate-a-latency-regression) |
| Bimodal latency distribution in heatmap | Some requests hitting page cache, others going to disk | [Performance Theory](04-performance-theory.md#heatmaps-seeing-the-full-picture) |
| Artificially low latency measurements | Coordinated omission — tool slows down with the system | [Performance Theory](04-performance-theory.md#coordinated-omission) |
| Stress test results vary wildly between identical runs | JVM warmup (JIT), GC pauses, small sample size — increase records to 500K+, use ZGC, discard first 2–3 warmup iterations | [Performance Theory](04-performance-theory.md#the-long-tail-problem), [Deployment Guide](12-deployment.md#jvm-tuning) |
| `KafkaRequestHandlerSaturated` alert | Request handlers over 70% busy — add threads or brokers | [Kafka Deployment Engineering](15-kafka-deployment.md#prometheus-alerts) |
| `KafkaLogFlushLatencyHigh` alert | Disk I/O saturated — check storage class and disk utilization | [Kafka Deployment Engineering](15-kafka-deployment.md#prometheus-alerts) |

## Deployment Issues

| Symptom | Likely Cause | Chapter |
|---------|-------------|---------|
| Images won't load into Kind | Registry unreachable or platform mismatch (arm64/amd64) | [Deployment Guide](12-deployment.md#images-wont-load) |
| Kafka pods stuck in `Pending` | StorageClass can't provision PVCs, or no node matches the zone `nodeAffinity` rules | [Deployment Guide](12-deployment.md#kafka-pods-not-starting), [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md#pods-stuck-in-pending) |
| `helm upgrade` fails with `another operation in progress` | A previous install or upgrade was interrupted — roll back the release, then retry | [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md#helm-upgrade-fails-with-another-operation-in-progress) |
| PDB blocks rolling restart | Only 1 pod can be unavailable — intentional safety behavior | [Upgrade Playbook](18-upgrade-playbook.md#common-upgrade-issues) |
| Entity Operator never starts | Kafka CR hasn't reached `Ready` — check operator logs for `UnforceableProblem` | [Kafka Deployment Engineering](15-kafka-deployment.md#strimzi-operator-cannot-determine-active-controller) |
| PostgreSQL pod `CrashLoopBackOff` with `could not create lock file` | `readOnlyRootFilesystem: true` mutated by Kyverno — mount `emptyDir` at `/var/run/postgresql` and `/tmp` | [Deployment Guide](12-deployment.md#read-only-filesystem-compliance) |
| Pod admission rejected with Kyverno policy violation | Pod doesn't meet PSS standards — run `kates kyverno violations` to identify failing rules, then fix the manifest or add a `PolicyException` | [Security & Compliance](17-security.md#kyverno-policy-integration--admission-control) |

## CLI Issues

| Symptom | Likely Cause | Chapter |
|---------|-------------|---------|
| `kates health` killed immediately (exit 137) on macOS | macOS blocks unsigned binary — `com.apple.provenance` xattr | [Deployment Guide](12-deployment.md#cli-binary-killed-on-macos) |
| CLI connection timeout / connection refused | Backend not running or port-forward died | [Deployment Guide](12-deployment.md#kates-cant-connect-to-kafka) |

## Chaos Engineering

| Symptom | Likely Cause | Chapter |
|---------|-------------|---------|
| Litmus experiments fail to start | Chaos operator pod not running or RBAC insufficient | [Deployment Guide](12-deployment.md#litmus-experiments-fail) |
| Disruption doesn't take effect | Target pod selector doesn't match, or NetworkPolicy blocks | [Chaos Engineering in Practice](07-chaos-practice.md) |
| Cluster doesn't recover after chaos | ISR too small, `min.insync.replicas` violated | [Chaos Engineering Theory](06-chaos-theory.md) |

## Upgrades

| Symptom | Likely Cause | Chapter |
|---------|-------------|---------|
| `UnsupportedVersionException` after operator upgrade | Kafka version not supported by new operator version | [Upgrade Playbook](18-upgrade-playbook.md#version-compatibility-matrix) |
| Topics not reconciling after CRD API change | CRDs still using deprecated `v1beta2` | [Upgrade Playbook](18-upgrade-playbook.md#post-upgrade--api-migration) |
| Performance regression after Kafka upgrade | New version defaults changed — compare baseline tests | [Upgrade Playbook](18-upgrade-playbook.md#procedure) |

## Connectivity Debugging Flowchart

When you can't connect to Kafka, work through this decision tree. The commands here and in [Quick Diagnostic Commands](#quick-diagnostic-commands) assume the chart defaults — cluster name `krafter` in namespace `kafka` (set in `charts/kafka-cluster/values.yaml`); adjust them if your deployment overrides these values.

```mermaid
flowchart TD
    A["Can't connect to Kafka"] --> B{"Are Kafka pods running?"}
    B -->|No| B1["Check pod status:<br/>kubectl get pods -n kafka"]
    B1 --> B2{"Pods in Pending?"}
    B2 -->|Yes| B3["Check StorageClass and PVCs:<br/>kubectl get pvc -n kafka"]
    B2 -->|No| B4["Check pod logs:<br/>kubectl logs &lt;pod&gt; -n kafka --previous"]
    
    B -->|Yes| C{"Can you reach the bootstrap service?"}
    C -->|No| C1{"Is a NetworkPolicy blocking?"}
    C1 -->|Yes| C2["Add an ingress rule for<br/>your namespace → port 9092<br/>See Security & Compliance"]
    C1 -->|No| C3["Check DNS resolution:<br/>nslookup krafter-kafka-bootstrap.kafka"]
    C3 --> C4{"DNS resolves?"}
    C4 -->|No| C5["Check CoreDNS pods:<br/>kubectl get pods -n kube-system"]
    C4 -->|Yes| C6["Check port-forward or NodePort:<br/>kubectl port-forward svc/krafter-kafka-bootstrap 9092:9092 -n kafka"]
    
    C -->|Yes| D{"Authentication succeeds?"}
    D -->|No| D1["Check SCRAM credentials:<br/>kubectl get secret &lt;user&gt; -n kafka"]
    D1 --> D2["Verify KafkaUser reconciled:<br/>kubectl get kafkauser -n kafka"]
    
    D -->|Yes| E["Connection works!<br/>Check application config"]
```

## When to Escalate

Not every problem is a Kates problem. Use this guide to determine where to focus your investigation:

| Symptom Pattern | Likely Layer | What to Check |
|-----------------|-------------|---------------|
| `kates health` shows all components UP, but tests fail | **Kafka** | Check broker logs, partition health, ISR state |
| CLI commands return "connection refused" or timeout | **Kates backend** | Check Kates pod status, port-forward, service endpoints |
| Pods stuck in `Pending`, `CrashLoopBackOff`, or `ImagePullBackOff` | **Kubernetes** | Check node resources, StorageClass, image registry access |
| Kyverno rejecting pod creation | **Kyverno policies** | Run `kates kyverno violations` to identify which rule is failing |
| Latency numbers are unreasonably high for all tests | **Infrastructure** | Check node CPU/memory pressure, disk I/O, network bandwidth |
| Everything works locally but fails in CI | **CI environment** | Check resource limits, network access, Docker-in-Docker configuration |

::: {.callout-tip}
When filing an issue, include the output of `kates doctor` — it runs a battery of diagnostic checks and gives you a single summary of system health.
:::

## Common Issues (Additional)

| Symptom | Likely Cause | Fix |
|---------|-------------|-----|
| `kates cluster topology` returns "Cluster topology is only available when the Kates backend is deployed on Kubernetes with access to Strimzi CRDs" | Missing `ClusterRoleBinding` for the Kates service account — the backend can't query Strimzi CRDs | Verify RBAC: `kubectl get clusterrolebinding kates` — if missing, redeploy with `helm upgrade --install kates charts/kates -n kates` |
| Test results show 0 records consumed even though producers succeeded | Consumer group hasn't started consuming, or topic has no committed offsets for the group | Check consumer lag: `kates kafka group <group-name>`. If lag equals total records, the consumer never started — check Kates backend logs for consumer errors |
| `kates trend` shows no data even after running tests | Tests completed but trend queries require at least 2 data points of the same test type | Run the same test type at least twice. Trend analysis needs historical data to draw a line |

## Quick Diagnostic Commands

```bash
# Cluster overview
kubectl get kafka,kafkanodepool,kafkatopic,kafkauser -n kafka

# Pod health
kubectl get pods -n kafka -o wide

# Strimzi operator logs (last 50 lines)
kubectl logs deployment/strimzi-cluster-operator -n kafka --tail=50

# Broker logs (last crash)
kubectl logs <broker-pod> -n kafka --previous --tail=30

# Kafka status conditions
kubectl get kafka krafter -n kafka -o jsonpath='{range .status.conditions[*]}{.type}: {.status} - {.message}{"\n"}{end}'

# Under-replicated partitions
kubectl exec <broker-pod> -n kafka -- bin/kafka-topics.sh --describe --under-replicated-partitions --bootstrap-server localhost:9092

# Consumer lag
kubectl exec <broker-pod> -n kafka -- bin/kafka-consumer-groups.sh --bootstrap-server localhost:9092 --all-groups --describe

# Kyverno policy status
kates kyverno status

# Kyverno violations (all namespaces)
kates kyverno violations

# Kyverno violations (specific namespace)
kates kyverno violations --namespace kafka
```

