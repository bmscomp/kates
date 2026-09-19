# kafka-cluster

A [Strimzi](https://strimzi.io/) Kafka cluster in KRaft mode: node pools, topics, users, Cruise Control rebalancing, tiered storage, network policy, backup and observability, for Kafka 4.3.1 on Strimzi 1.2.0.

Upgrading from 0.4? Read [docs/kafka-cluster-1.0-upgrade.md](../../docs/kafka-cluster-1.0-upgrade.md). The 0.4 keys still work in 1.x, and NOTES list every one a release still uses.

## What it renders

| Resource | Name | Notes |
|:---|:---|:---|
| `Kafka` | `<clusterName>` | Kept on uninstall |
| `KafkaNodePool` | each `nodePools.pools[].name` | Kept on uninstall; a pool's name is its identity |
| `KafkaTopic`, `KafkaUser` | by item name | Kept on uninstall unless `keepOnDelete: false` |
| `KafkaUser` | `<clusterName>-helm-test` | The Helm tests' principal (`tests.user.create`) |
| `KafkaRebalance` templates | `<clusterName>-{add,remove}-brokers-template` | Used by `cruiseControl.autoRebalance`; optional `<clusterName>-full-rebalance` |
| `ConfigMap` | `<clusterName>-kafka-metrics`, `<clusterName>-cruise-control-metrics` | Strimzi's exporter rules, vendored |
| `PodMonitor` | `<clusterName>-{kafka,cruise-control,kafka-exporter,entity-operator}` | Where the `monitoring.coreos.com` API exists |
| `PrometheusRule` | `<clusterName>-alerts` | Same condition |
| `NetworkPolicy` | `<clusterName>-{default-deny,allow-dns,kafka,cruise-control,entity-operator,kafka-exporter}`, `<clusterName>-test-egress` | |
| Velero `Schedule`, pre-upgrade `Backup` | `<clusterName>-daily-backup`, `<clusterName>-pre-upgrade-r<revision>` | In `backup.veleroNamespace` |
| SeaweedFS subchart, `Secret`, `ConfigMap` | `<clusterName>-seaweedfs-credentials`, `<clusterName>-object-store` | With `seaweedfs.enabled` |
| Kyverno `ClusterPolicy` | `kafka-pod-security-<namespace>-<clusterName>` | With `kyvernoPolicy.enabled` |
| `ServiceAccount`, `Role`, `RoleBinding` | `<clusterName>-admin`, `<clusterName>-viewer` | Read access for the Helm tests |

Every namespaced resource carries the cluster name, so two clusters can share a namespace (give their node pools distinct names). `compatibility.legacyResourceNames: true` keeps the 0.4 names of the resources that existed then; renaming the metrics ConfigMap rolls every broker, so set it on an upgrade and clear it in a maintenance window.

**Not here any more** — they are the [`strimzi-operator`](../strimzi-operator/README.md) chart's, once per operator: the Strimzi CRDs, the drain cleaner, the operator's NetworkPolicy, scrape and alerts (`StrimziOperatorDown`, certificate expiry), and the Grafana dashboards (the operator's own, which read exactly the series this chart's rules produce).

## Prerequisites

| Requirement | Version |
|:---|:---|
| Kubernetes | ≥ 1.27 |
| Helm | ≥ 3.12 |
| Strimzi Cluster Operator | 1.2.0 — `charts/strimzi-operator`, installed first |
| prometheus-operator CRDs | optional (PodMonitors, alerts) |
| Velero, cert-manager, Kyverno | optional (`backup`, `kyvernoPolicy`) |

## Install

```bash
helm dependency build charts/kafka-cluster      # kafka-common (and seaweedfs)
helm upgrade --install krafter charts/kafka-cluster -n kafka --create-namespace \
  -f charts/kafka-cluster/values-prod.yaml \
  -f charts/kafka-cluster/values-platform.yaml
kubectl wait kafka/krafter -n kafka --for=condition=Ready --timeout=600s
helm test krafter -n kafka --timeout 5m
```

`kates deploy` does this for you, with the node pools `kates detect` generates. A `Chart.lock` left by 0.4 makes `helm dependency build` refuse the new library dependency; run `helm dependency update` once.

## Values files

| File | Use |
|:---|:---|
| `values.yaml` | A generic cluster: three controllers, three brokers, internal listeners, no topics or users |
| `values-platform.yaml` | The kates platform profile (`profile: platform`): its topics, users, super user and client grants. Layer it over any environment |
| `values-dev.yaml` | One controller and one broker, replication factor 1, features off |
| `values-kind.yaml` | Kind: feature flags only, over the detected values or `values-dev.yaml` |
| `values-ci.yaml` | The smallest cluster that works, for E2E runners |
| `values-staging.yaml` | Three controllers, three single-broker pools, policies, alerts, backup |
| `values-prod.yaml` | `productionMode`, three controllers and three pools of three brokers, quotas, a TLS NodePort listener, Velero backup of every volume with SeaweedFS behind it. Tiered storage is off until an image with a storage plugin exists (see its header) |
| `values-additional.yaml` | A second cluster beside the primary, to migrate to or from: one node each, platform users, no topics |

Overlays that bring their own pools set `controllerPools: []` and `brokerPools: []`, which clears the 0.4-shaped pools `kates detect` writes into the layer below.

## Node pools

```yaml
nodePools:
  defaults:            # every pool
    storage: { type: jbod, volumes: [{ id: 0, size: 100Gi }] }
    volume: { type: persistent-claim, deleteClaim: false }   # merged into each volume
    scheduling:
      zoneKey: topology.kubernetes.io/zone
      spread: { enabled: true, maxSkew: 1, whenUnsatisfiable: ScheduleAnyway }
      antiAffinity: { enabled: true, topologyKey: kubernetes.io/hostname }
      tolerations: []
      priorityClassName: ""
      terminationGracePeriodSeconds: null
    podSecurityContext: {}        # merged over the hardened default
    containerSecurityContext: {}  # likewise
    sysctls: []                   # namespaced sysctls only
    template: {}                  # KafkaNodePool.spec.template, merged last
  roleDefaults:
    controller: { replicas: 3, storage: { volumes: [{ id: 0, size: 10Gi }] }, jvmOptions: …, resources: … }
    broker:     { replicas: 3, jvmOptions: …, resources: … }
  pools:
    - name: controllers
      roles: [controller]
    - name: brokers
      roles: [broker]
```

Each pool is `defaults`, then `roleDefaults` for each of its roles, then the pool itself. Maps are merged key by key (a `false` overrides a `true`), lists replace. A pool may set `zone` (required node affinity on `zoneKey`, and a `zone` pod label), its own `replicas`, `storage`, `resources`, `jvmOptions`, `scheduling` and `template`. `roles: [controller, broker]` makes a dual-role pool.

- **The name is the identity.** Renaming a pool creates a new one and removes the old one with its brokers. The 0.4 keys (`controllerPools`, `brokerPools`, `controllerDefaults`, `brokerDefaults`) are translated with their names kept; setting both shapes fails.
- **Sysctls.** `sysctls` renders into the pod security context, which accepts only namespaced keys (`net.*`, `kernel.shm*`, `kernel.msg*`, `kernel.sem`, `fs.mqueue.*`). `vm.max_map_count` and other node-level keys are refused: tune nodes with a DaemonSet or the node image. 0.4's `sysctl.enabled` rendered an init container Strimzi does not support, so it never ran; it now fails with this explanation.
- **Anti-affinity.** Controller-only pools spread against every controller of the cluster; other pools against their own pods.
- **Grace period.** `terminationGracePeriodSeconds` is set on each pool's pod template, where Strimzi reads it. 0.4's `lifecycle.preStopSleepSeconds` set it on the Kafka CR's, which the pools' templates replace, so it never reached a pod.

## The Kafka CR

| Key | Default | |
|:---|:---|:---|
| `clusterName` | `krafter` | CR name, `strimzi.io/cluster`, resource-name prefix |
| `kafkaVersion` | `4.3.1` | At or above `Chart.yaml`'s `kates.io/kafka-floor` |
| `kafka.metadataVersion` | `4.2-IV1` | One step behind, so a rollback stays possible; never newer than `kafkaVersion` (refused) |
| `kafka.image` | `""` | The operator's image for `kafkaVersion` when empty |
| `kafka.listeners` | `plain` 9092 SCRAM, `tls` 9093 mTLS | NetworkPolicy ports are derived from these |
| `kafka.externalAccess.type` | `none` | `nodeport`, `loadbalancer` or `ingress` adds an `external` listener (port 9094, TLS, SCRAM); `allowedCidrs` narrows its policy rule |
| `kafka.authorization` | `simple`, no super users | The platform profile adds `kates-backend` |
| `kafka.config` | replication 3, `min.insync.replicas` 2, … | Share-group keys render only for Kafka ≥ 4.2. Replication factors above the broker count, and `min.insync.replicas` ≥ `default.replication.factor` (above 1), are refused |
| `kafka.quotas` | `{}` | `spec.kafka.quotas`; prod sets `type: strimzi` with `minAvailableRatioPerVolume: 0.1` |
| `kafka.rack.topologyKey` | `topology.kubernetes.io/zone` | Rack awareness |
| `kafka.logging`, `jmxOptions`, `livenessProbe`, `readinessProbe` | `{}` | Passed through |
| `kafka.template` | PDB `maxUnavailable: 1` | Merged over a hardened `kafkaContainer` security context |
| `kafka.clusterCa`, `kafka.clientsCa` | generated, 5 years, renew 180 days before | |
| `maintenanceTimeWindows` | `[]` | Cron windows for certificate renewal |
| `entityOperator.{topicOperator,userOperator,template}` | sized for production | |
| `kafkaExporter.*` | enabled, all topics and groups | |
| `cruiseControl.*` | enabled, capacity, `autoRebalance` add/remove | `apiUsers`, `template`, `config`, `resources` pass through; Cruise Control keeps the JMX exporter and its own rule set |

## Topics and users

```yaml
topics:
  defaults:
    partitions: 3
    replicas: null          # the broker count, at most 3
    config: {}              # min.insync.replicas defaults to replicas − 1 (1..2)
  items:
    orders: { partitions: 12, config: { retention.ms: "604800000" } }
users:
  items:
    orders-app:
      authentication: { type: scram-sha-512 }   # the default
      authorization:
        type: simple
        acls:
          - resource: { type: topic, name: orders, patternType: literal }
            operations: [Read, Write, Describe]
      quotas: {}
      template: {}          # Secret labels/annotations
```

Items are maps keyed by name, so an overlay changes one field of one topic (`--set topics.items.orders.partitions=24`) or drops one (`enabled: false`). The 0.4 list form is still accepted. A topic asking for more replicas than there are brokers is refused. With tiered storage active every topic gets `remote.storage.enable: "true"` unless it says otherwise.

**The platform profile** (`profile: platform`, `profiles/platform.yaml`) adds the platform's eight topics, six users (`kates-backend`, `kafka-ui`, `apicurio-registry`, `litmus-chaos`, `kates-connect`, `kates-mm2`), `kates-backend` as super user, and the client grants below. Your items are merged over the profile's by name, so `users.items.kates-connect.enabled=false` hands that user to `connect-cluster` (adopt the object first — a recreated KafkaUser gets a new password; see the upgrade guide).

## Rebalancing

`rebalance.*` renders two KafkaRebalance **templates** (`strimzi.io/rebalance-template`), and `cruiseControl.autoRebalance` entries without a template use them. Shared settings: `goals`, `skipHardGoalCheck`, `excludedTopics`, `replicationThrottle`, `concurrentPartitionMovementsPerBroker`, `concurrentLeaderMovements`; `rebalance.templates.<mode>` overrides them per mode. `rebalance.full.enabled` adds a standing full rebalance (approve it with `kubectl annotate kafkarebalance krafter-full-rebalance strimzi.io/rebalance=approve`, or set `rebalance.full.autoApprove`). Nothing renders without Cruise Control, and a full rebalance without it is refused.

0.4 rendered a standing `add-broker-rebalance` with no brokers, which Cruise Control rejects; it has `helm.sh/resource-policy: keep`, so delete it by hand after upgrading.

## Tiered storage

```yaml
tieredStorage:
  enabled: true
  image: registry.example.com/kafka-tiered:1.2.0-kafka-4.3.1   # carries the plugin
  remoteStorageManager:
    className: io.aiven.kafka.tieredstorage.RemoteStorageManager
    classPath: /opt/kafka/plugins/tiered-storage/*
    config:
      storage.backend.class: io.aiven.kafka.tieredstorage.storage.s3.S3Storage
      chunk.size: "4194304"
  credentials:
    existingSecret: ""        # empty with seaweedfs.enabled: its Secret
  metadata: { replicationFactor: 3 }
  topicDefault: true
  localRetentionMs: 86400000
  egress: { to: [], ports: [80, 443, 8333] }
```

The chart renders `spec.kafka.tieredStorage` (`type: custom`), sets `spec.kafka.image`, puts the remote-log-metadata replication factor and `log.local.retention.ms` into the broker config, and hands the credentials to the broker containers as `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY`. With `seaweedfs.enabled` it fills in `storage.s3.endpoint.url`, bucket, region and path-style access; your `config` wins. The brokers may reach the object store through the NetworkPolicy.

Strimzi's stock image has no storage plugin, so enabling tiered storage without `image`, with the stock image, without `className`/`classPath`, or without credentials is refused. `helm test` then runs `test-tiered-storage`, which proves segments leave local disk and are read back (allow `--timeout 15m`). The 0.4 keys (`s3.*`, `retention.*`) are translated into the plugin config.

0.4 rendered only a ConfigMap nothing read, and its backup skipped broker volumes on the premise that tiered storage protected them. Neither protected anything.

## Backup

```yaml
backup:
  enabled: true
  schedule: "0 2 * * *"
  ttl: 336h0m0s
  volumes: ""             # fs-backup (the default) | snapshot | none
  storageLocation: seaweedfs
  preUpgrade: true
```

The daily `Schedule` covers this cluster's Strimzi CRs and the Secrets, ConfigMaps, PVCs and PVs labelled `strimzi.io/cluster=<clusterName>` (Velero applies `labelSelector` to every resource, so it names the cluster and nothing else). `volumes` decides how volume data is kept: `fs-backup` copies every volume file by file (Velero's node agent), `snapshot` takes CSI snapshots (crash-consistent; read the section below), `none` keeps objects only. While tiered storage is active, broker-only pools drop out of the volume backup (`velero.io/exclude-from-backup` on their PVCs and the fs-backup exclusion on their pods); controller volumes, which hold the metadata log, are always in. `productionMode` refuses a backup that protects no broker data. A pre-upgrade `Backup` hook records the state before every upgrade (kept 30 days).

The 0.4 keys `snapshotVolumes` and `defaultVolumesToFsBackup` are read as `volumes`.

> [!CAUTION]
> **NetBackup (Veritas) and other block- or file-level backup agents must not be used for this cluster.** See below.

## Why NetBackup is Incompatible with Kafka

NetBackup (Veritas) is a **block- and file-level** backup agent designed for traditional storage workloads (databases with point-in-time recovery, filesystems, virtual machine images). Its model conflicts with Kafka's architecture at every level.

### 1. Snapshots are crash-consistent, not application-consistent

NetBackup (and any PVC snapshot tool without a Kafka-aware pre-freeze hook) captures the broker's data directory **mid-write**. A Kafka log segment being written at snapshot time will be partially flushed to disk. On restore:

- Index files reference offsets that do not exist in the data file
- The log verifier rejects the segment and truncates it
- Downstream consumers that already read those offsets receive a gap or duplicate range
- The replica-set reconciliation protocol treats the divergent broker as corrupted and triggers a full re-fetch — defeating the purpose of the snapshot

Kafka does not expose a filesystem-level quiesce API. There is no equivalent of `FLUSH TABLES WITH READ LOCK` for broker log segments.

### 2. Replica factor creates false redundancy

Kafka clusters with `replication.factor: 3` store **three identical copies** of every log segment across three brokers. A NetBackup job that snapshots all broker PVCs captures the same data three times with no additional protection. The three replicas fail identically (e.g. corruption of a specific offset range) because they share the same data at snapshot time.

At 200 Gi per broker × 9 brokers (production pool), this is **1.8 Ti of backup data** for 600 Gi of unique content — a 3× amplification with zero RPO improvement.

### 3. No topic-level or consumer-group granularity

NetBackup restores at the PVC level. A restore operation brings back the **entire cluster**, including all topics, all partitions, and all consumer group offsets — from a point in time that may be hours in the past.

There is no way to:
- Restore a single topic without restoring the entire broker
- Restore consumer group offsets independently of topic data
- Replay only the messages a specific consumer missed

The minimum restore unit is the full cluster, with a guaranteed data gap proportional to the backup interval.

### 4. Agent installation conflicts with the immutable container model

NetBackup requires a client agent running inside each container (or on the host). The Strimzi operator manages Kafka pod specs directly via `StrimziPodSet`. Any sidecar or init-container injected by NetBackup:

- Is not declared in the `KafkaNodePool` spec and will be removed on the next Strimzi reconciliation
- Requires a privileged security context that conflicts with the restricted Pod Security Standard (and `kyvernoPolicy`)
- Cannot be version-controlled alongside the Helm chart

### 5. RTO is measured in hours, not seconds

A full cluster restore from NetBackup requires:
1. Provision new PVCs on every broker node
2. Stream 200 Gi per node from the backup server (network-bound)
3. Start each broker and wait for it to verify its log segments
4. Wait for replica sync across the full partition set

In a 9-broker production cluster this takes **4–8 hours** before any consumer can reconnect. With Kafka Tiered Storage, a replacement broker fetches only the hot segments from the local object store and is available for reads **within minutes**.

### 6. Consumer offset integrity is not preserved

`__consumer_offsets` is itself a Kafka topic. Its content at snapshot time reflects the committed offsets of all consumer groups at that moment. After a restore to a snapshot taken at `T-8h`:

- All consumers that committed offsets between `T-8h` and `T` are reset to `T-8h`
- Consumers using `auto.offset.reset: latest` will skip the 8-hour gap entirely
- Consumers using `auto.offset.reset: earliest` will reprocess 8 hours of messages

Neither outcome is acceptable for an event-driven microservices architecture.

### Correct Alternative

Use the **two-layer strategy** described above:
- **Tiered Storage** → continuous, topic-level log durability with no restore operation, once an image with a remote storage manager is available
- **Velero** → daily backup of the Kubernetes objects, the controller volumes and — until tiered storage is active — the broker volumes, file by file

## Network policy

```yaml
networkPolicy:
  enabled: true
  defaultDeny: { enabled: true, selector: {} }   # empty: app.kubernetes.io/part-of=strimzi-<clusterName>
  dns: { enabled: true, selector: {}, to: {} }
  apiServer: { ports: [443, 6443], ipBlock: {} }
  monitoring: { namespace: monitoring }
  operatorNamespace: strimzi-operator
  clients:
    - name: my-app
      namespace: apps              # empty: the release namespace
      podSelector: { app.kubernetes.io/name: my-app }
      listeners: [tls]             # names from kafka.listeners; empty: every internal listener
  extraIngress: []
  extraEgress: []
```

The `<clusterName>-kafka` policy selects the brokers and controllers and admits: the cluster's own pods (9090, 9091 and every listener), the Cluster Operator (9090, 9091, 8443 and every listener), each client on the ports of the listeners it names, test pods (`kates.io/test-pod=true`) on the internal listeners, external listeners from anywhere (or `externalAccess.allowedCidrs`), and metrics from the monitoring namespace. Ports follow `kafka.listeners`, so a custom listener works; a client naming an unknown listener, or with no `podSelector`, is refused. Cruise Control, the entity operator and the Kafka Exporter get their own policies.

The policies of Connect, MirrorMaker 2 and Kafka UI pods are their charts' own, and the operator's is `strimzi-operator`'s. Strimzi's generated policies still apply beside these (NetworkPolicies are additive).

The 0.4 `networkPolicies` block is translated: `connectNamespace`, `mirrorMaker2Namespace`, `kafkaUINamespace` and `apicurioNamespace` set the namespaces of the platform profile's clients, and a 1.0 key set in a higher values layer wins over a 0.4 key in a lower one.

## Observability

- **Metrics.** `metrics.type` (empty, which means `jmxPrometheusExporter`) uses Strimzi's own rule set, vendored unchanged in `files/metrics/kafka-metrics.yaml` (`scripts/check-strimzi-metrics.sh` compares it with upstream); Cruise Control has its own (`files/metrics/cruise-control-metrics.yaml`). `strimziMetricsReporter` (with `metrics.allowList`) is available for the brokers; the alerts are refused with it unless `alerts.allowReporterMetrics`.
- **Scrape.** One PodMonitor per component, selecting `strimzi.io/cluster` and the component's `strimzi.io/name`, with the relabelings Strimzi's dashboards expect (`strimzi_io_cluster`, `namespace`, `kubernetes_pod_name`, …).
- **Dashboards.** The operator's own (Kafka, KRaft, Cruise Control, Kafka Exporter), enabled on the `strimzi-operator` chart. `scripts/metric-contract/kafka-cluster.yaml` proves every series they and the alerts read is one the vendored rules produce. 0.4's six dashboards read 36 series no rule produced.
- **Alerts.** Scoped by `namespace` and `strimzi_io_cluster`, each with a `runbook_url` into [docs/kafka-cluster-runbook.md](../../docs/kafka-cluster-runbook.md). Thresholds are in `alerts.thresholds`.

| Alert | Severity | Fires when |
|:---|:---|:---|
| `KafkaOfflinePartitions` | critical | partitions have no leader |
| `KafkaActiveControllerCount` | critical | the quorum does not have exactly one active controller |
| `KafkaUnderMinIsrPartitions` | critical | partitions refuse `acks=all` writes |
| `KafkaOfflineLogDirectory` | critical | a broker lost a log directory |
| `KafkaUncleanLeaderElection` | critical | an out-of-sync replica became leader |
| `KafkaBrokerDiskUsageCritical` / `High` | critical / warning | a volume has under 10% / 20% free |
| `KafkaNodesMissing` | warning | fewer nodes are scraped than the pools declare |
| `KafkaFencedBrokers` | warning | brokers are fenced |
| `KafkaUnderReplicatedPartitions` | warning | followers are out of sync for 5 minutes |
| `KafkaISRShrinkRate` | warning | ISRs keep shrinking for 10 minutes |
| `KafkaRaftLeaderElections` | warning | more than 3 quorum elections in 15 minutes |
| `KafkaRaftUnknownVoters` | warning | a node cannot reach every voter |
| `KafkaBrokerMetadataLag` | warning | a broker applies metadata more than 60 s late |
| `KafkaRequestLatencyHigh` | warning | p99 produce/fetch time above 1 s |
| `KafkaRequestHandlerSaturated` | warning | request handlers idle less than 30% |
| `KafkaRequestQueueSaturated` | warning | more than 100 requests queued |
| `KafkaLogFlushLatencyHigh` | warning | p99 flush above 500 ms |
| `KafkaConsumerGroupLag` / `Critical` | warning / critical | a group is 1 M / 10 M messages behind (Kafka Exporter) |
| `CruiseControlAnomalyDetected` | warning | goal violations or disk failures |
| `CruiseControlNoLoadModel` | warning | no valid metric window for an hour |
| `KafkaTieredStorageCopyErrors` | warning | segments fail to offload (tiered storage only) |
| `KafkaAvailabilitySLOBurning` | critical | `alerts.slo`: server-side request errors burn the budget (1 h/5 m or 6 h/30 m) |

Recording rules: `kafka:under_replicated_partitions:sum`, `kafka:produce_p99_ms:max`, `kafka:bytes_in:rate5m`, `kafka:disk_free_ratio:min`, and with `alerts.slo` `kafka:request_errors:ratio_rate{5m,30m,1h,6h}`.

PodMonitors and alerts render only where the `monitoring.coreos.com/v1` API exists, so a plain install on a cluster without prometheus-operator works.

## Rails

The chart refuses, with the reason and the fix:

| Setting | Refused when |
|:---|:---|
| metadata version | newer than `kafkaVersion` |
| node pools | none with the controller role, none with the broker role (or their replicas are 0), duplicate names, both pool shapes set, a `zone` without `zoneKey`, a volume without a size |
| sysctls | a key a pod cannot set; 0.4's `sysctl.enabled` |
| replication | a topic's replicas, or `offsets.topic.replication.factor` / `transaction.state.log.replication.factor` / `default.replication.factor`, above the broker count; `min.insync.replicas` above the replication factor, or equal to it (above 1) |
| listeners | duplicate names or ports; a Strimzi-reserved port; `ingress` without TLS |
| clients | an unknown listener name; no `podSelector` |
| tiered storage | no image, the stock image, no plugin class/path, no credentials |
| Cruise Control | a full rebalance without it; an unknown `autoRebalance` mode |
| moved features | `drainCleaner.enabled` (now `strimzi-operator`) |
| metrics | alerts without metrics; alerts on the reporter without `allowReporterMetrics` |
| images | `:latest` or untagged `kafka.image`, `tieredStorage.image`, test images |
| SeaweedFS | the placeholder secret with `filer.s3.enableAuth` or `productionMode` |
| `productionMode` | a NodePort listener without TLS, `deleteClaim: true`, network policy off, a backup that protects no broker data |
| schema | a misspelled key anywhere the chart owns (pass-through objects stay open) |

`scripts/chart-matrix/kafka-cluster.yaml` renders every overlay, the `kates deploy` shape and each toggle, checks the Strimzi CRs against the pinned CRDs, and asserts each rail's message. `helm unittest charts/kafka-cluster` covers the pool translation and the profile merge.

## Helm tests

`helm test <release>` runs, in order: a profiler, connectivity (CR readiness, pods, DNS, listeners), produce/consume, authorization, the KRaft quorum, topics, listeners and TLS, node pools, Cruise Control, metrics, a producer benchmark, and — with tiered storage — offload and remote reads. They authenticate as `<clusterName>-helm-test` (SCRAM, `helm-test-*` topics and groups only); `tests.user.create: false` uses the first managed user instead. The test pods use the chart's `<clusterName>-admin` ServiceAccount and the `<clusterName>-test-egress` NetworkPolicy.

## Policy and access

| Key | Default | |
|:---|:---|:---|
| `kyvernoPolicy.enabled` | `false` | A Kyverno ClusterPolicy for this namespace's pods (non-root, dropped capabilities, seccomp, no privilege escalation or host namespaces). Formerly `podSecurityPolicy` |
| `kyvernoPolicy.action` | `Audit` | or `Enforce` |
| `kyvernoPolicy.mutate` | `false` | Inject the security contexts |
| `kyvernoPolicy.excludeStrimziPods` | `true` | Strimzi pods are left out of mutation and capability checks |
| `kyvernoPolicy.policyExceptions` | off | `PolicyException`s for `namespaces` and `exemptRules` |
| `rbac.create` | `true` | `<clusterName>-admin` ServiceAccount with a read Role (plus `rbac.extraRules`) |
| `externalSecrets.*` | off | SecretStore, PushSecret, ExternalSecret wiring |

## Connecting

Inside the cluster (SCRAM on 9092):

```properties
bootstrap.servers=krafter-kafka-bootstrap.kafka.svc.cluster.local:9092
security.protocol=SASL_PLAINTEXT
sasl.mechanism=SCRAM-SHA-512
sasl.jaas.config=org.apache.kafka.common.security.scram.ScramLoginModule required username="kates-backend" password="<kubectl get secret kates-backend -o jsonpath='{.data.password}' | base64 -d>";
```

mTLS on 9093 uses the user's certificate Secret and the cluster CA (`krafter-cluster-ca-cert`). Clients in another namespace also need a `networkPolicy.clients` entry. With `kafka.externalAccess`, `kubectl get kafka krafter -o jsonpath='{.status.listeners[?(@.name=="external")].bootstrapServers}'` gives the external bootstrap.

## Troubleshooting

- **The render fails.** Read the message: every rail names the setting and the fix. `DEPRECATED` lines in NOTES list 0.4 keys still in use.
- **The Kafka CR is not Ready.** `kubectl describe kafka krafter -n kafka` shows the operator's condition. Check that the operator watches the namespace and supports `kafkaVersion` (NOTES print both).
- **Clients time out.** A NetworkPolicy is the usual cause: add the client to `networkPolicy.clients` with the listener it uses.
- **SASL failures.** The listener's `authentication.type` and the client's `security.protocol` / `sasl.mechanism` must match; SCRAM passwords are in the user's Secret.
- **`helm dependency build` fails.** A `Chart.lock` from 0.4 lists only seaweedfs; run `helm dependency update charts/kafka-cluster`.
- **Brokers rolled on upgrade to 1.0.** Expected unless `compatibility.legacyResourceNames` was set: the metrics ConfigMap's name is part of the Kafka CR. Strimzi rolls one broker at a time. The new exporter rules take effect as each broker restarts.

