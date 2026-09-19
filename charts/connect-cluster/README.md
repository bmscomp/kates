# connect-cluster

A Strimzi `KafkaConnect` worker group and its `KafkaConnector`s, with the Kafka user, secret access, network policy, alerts and dashboard they need. It is deployed separately from the Kafka cluster ([kafka-cluster](../kafka-cluster/README.md)), so Connect upgrades never touch the brokers, and several Connect groups can share one Kafka cluster.

The worker spec, client authentication, KafkaUser, secret sync, PodMonitor and NetworkPolicy fragments come from the [kafka-common](../kafka-common/README.md) library, shared with [mirror-maker2](../mirror-maker2/README.md). Upgrading from 1.x: [docs/connect-cluster-2.0-upgrade.md](../../docs/connect-cluster-2.0-upgrade.md). Alerts: [docs/connect-cluster-runbook.md](../../docs/connect-cluster-runbook.md).

## Contents

- [Install](#install)
- [Kafka connection](#kafka-connection)
- [Connectors](#connectors)
- [Secrets](#secrets)
- [Plugins](#plugins)
- [Workers](#workers)
- [Network policy](#network-policy)
- [Observability](#observability)
- [Rails](#rails)
- [Helm tests and preflight](#helm-tests-and-preflight)
- [Values reference](#values-reference)

## Install

Needs the Strimzi operator ([strimzi-operator](../strimzi-operator/README.md)) and a Kafka cluster.

```bash
helm dependency build charts/connect-cluster      # the kafka-common library
helm upgrade --install connect-cluster charts/connect-cluster -n connect --create-namespace \
  --set kafka.namespace=kafka
kubectl -n connect wait kafkaconnect/connect-cluster --for=condition=Ready --timeout=300s
helm test connect-cluster -n connect
```

| Overlay | For |
|:---|:---|
| `values-dev.yaml` | one small worker, replication factor 1 |
| `values-kind.yaml` | `kates deploy` on kind: no monitoring, the platform's database and Schema Registry |
| `values-generic.yaml` | `kates deploy` on other clusters |
| `values-prod.yaml` | `productionMode`: the TLS listener with a chart-managed certificate user, zone spreading, a PriorityClass, the SLO alert |

`kates deploy` installs this chart itself, with `kafka.namespace`, `kafka.bootstrapServers` and the database egress set for the platform.

## Kafka connection

The bootstrap is `<clusterName>-kafka-bootstrap.<namespace>.svc.<domain>`, on 9092, or 9093 with `kafka.tls.enabled`, or `kafka.listenerPort`. `kafka.bootstrapServers` replaces it. `kafka.namespace` defaults to **this release's** namespace (mirror-maker2's defaults to `kafka`).

| `kafka.authentication.type` | Renders |
|:---|:---|
| `scram-sha-512` (default), `scram-sha-256`, `plain` | `username` and `passwordSecret` (`secretName`, default the username; `secretKey`) |
| `tls` | `certificateAndKey` from `secretName` (default the username, which is what the User Operator writes), `certificate`, `key`. Needs `kafka.tls.enabled` |
| `custom` | `sasl` and `config` from `kafka.authentication.custom` — the v1 API's home of OAuth and other SASL mechanisms |
| `""` | no authentication |

kafka-cluster's TLS listener (9093) authenticates clients by certificate, so the chart refuses `tls.enabled` with SCRAM on the computed address; use `type: tls`, or name another listener.

**Managed user.** `kafkaUser.create: true` provisions the principal in the Kafka namespace with ACLs derived from the values (`authorization.mode: auto`):

- the three internal topics, the worker group and the `connect-*` groups of sink connectors, `groupGrants` and `topicGrants`;
- the dead letter queues of the declared connectors;
- with `exactlyOnce.enabled`, the transactional IDs `connect-cluster-<groupId>` (the leader) and `<groupId>-*` (the source tasks);
- cluster `Describe` and `DescribeConfigs`.

`mode: custom` takes `acls` verbatim. A KafkaUser can be `scram-sha-512` or `tls`.

**Secret sync.** When Kafka lives in another namespace, a post-install/upgrade Job copies the managed user's Secret and, with TLS, the cluster CA into this namespace (`secretSync.watch` adds a CronJob that follows rotation; `secretSync.secrets` adds others; `method: reflector` annotates the user's Secret for kubernetes-reflector instead).

kafka-cluster's platform profile also provisions `kates-connect`. To let this chart own it, adopt the object rather than recreate it; NOTES print the commands when the user belongs to another release.

## Connectors

`connectors` is a map keyed by name, so an overlay can change or drop (`enabled: false`) one connector. `connectorDefaults` is merged under each.

```yaml
connectorDefaults:
  autoRestart: { enabled: true, maxRestarts: 10 }
  config:
    errors.log.enable: "true"

connectors:
  orders-cdc:
    class: io.debezium.connector.postgresql.PostgresConnector
    tasksMax: 1
    state: running                 # running | paused | stopped
    config:
      database.hostname: postgresql.database.svc
      database.user: "${secrets:database/pg-credentials:username}"
      database.password: "${secrets:database/pg-credentials:password}"
      database.dbname: orders
      topic.prefix: cdc
  orders-sink:
    class: io.debezium.connector.jdbc.JdbcSinkConnector
    tasksMax: 2
    deadLetterQueue:
      enabled: true                # errors.tolerance=all, and a KafkaTopic
    config:
      topics: cdc.public.orders
      connection.url: jdbc:postgresql://postgresql.database.svc:5432/orders
```

- `version` (a plugin version range, Kafka 4.1+), `listOffsets` and `alterOffsets` pass through to the `KafkaConnector`.
- **Dead letter queue** (sink connectors only): sets `errors.tolerance=all`, the queue topic (default `<groupId>-dlq-<connector>`), its replication factor (default `config.replicationFactor`) and context headers. `createTopic` (default true) renders a `KafkaTopic` in the Kafka namespace; the managed user is granted it.
- **Validation happens at render time**: a DNS-1123 name, a class, a valid state, `tasksMax` of at least 1, `topics` or `topics.regex` on every sink, a namespace in every `${secrets:…}` reference, and the required keys of known classes (Debezium PostgreSQL, MySQL, MongoDB and SQL Server, the Debezium and Aiven JDBC connectors, the Aiven S3 sink, FileStream sink, MirrorHeartbeat). Under `productionMode`, a plaintext `*password` value is refused.
- A class that is not in the table is a sink when its name contains `Sink`; `type: sink|source` overrides that.
- A `connectors` list (1.x) still renders in 2.x and is listed as deprecated.

`testConnectors` and `testTopics` exist only during `helm test`. Their values are templates: `{{ .Release.Namespace }}` and `{{ include "connect-cluster.bootstrap" . }}` work.

## Secrets

The workers resolve `${secrets:<namespace>/<name>:<key>}` with Strimzi's `KubernetesSecretConfigProvider`, as the operator-made `<release>-connect` ServiceAccount. The chart reads every connector and test connector config, and grants `get` on exactly the Secrets referenced, with one Role per namespace.

- `rbac.secretNames` adds Secrets that connectors applied outside the chart use: `name` (this namespace) or `namespace/name`. The kind and generic overlays list the ones `kates deploy`'s own connectors read.
- `rbac.allSecrets: true` restores 1.x's get/list/watch on every Secret in this and the Kafka namespace. `productionMode` refuses it.

## Plugins

| Way | How | When |
|:---|:---|:---|
| `image` | a Connect image with the plugins inside (default: `ghcr.io/bmscomp/connect`, Debezium and Aiven connectors) | recommended |
| `plugins` | OCI images mounted as volumes at start-up (`spec.plugins`) | no custom image, and every node has the ImageVolume feature (alpha in Kubernetes 1.31, beta from 1.33) |
| `build` | the operator builds and pushes an image (`spec.build`) | a registry the operator can push to |

`plugins` renders only with `imageVolumes.acknowledged: true` — the chart cannot see a feature gate — and on Kubernetes 1.31 or later. `plugins[].expect` adds connector classes to the Helm test's list.

`Chart.yaml` `appVersion` is the Kafka version the workers run; the image pin is also recorded in the `kates.io/connect-image` annotation, and `scripts/check-versions.sh` checks both.

## Workers

The worker spec comes from kafka-common:

- `replicas` is required by the v1 API. Under `autoscaling.enabled` it is seeded from `minReplicas` and read back from the live CR on upgrade.
- `nodeSelector` becomes a required node-affinity term, because Strimzi's pod template has no `nodeSelector`.
- `templateExtra` is deep-merged into `spec.template` last, so `templateExtra.pod` adds to the chart's pod template instead of replacing it. `connectContainer` is merged into `template.connectContainer`.
- The PodDisruptionBudget is rendered only when more than one worker runs.
- The container runs with a read-only root filesystem and no capabilities.

`exactlyOnce.enabled` (default true) sets `exactly.once.source.support` for the whole group; the chart refuses it in `extraConfig`. `internalTopics.prefix` (default `groupId`) names the three internal topics.

`priorityClassName` is empty by default. `priorityClass.create` renders a `kates-streaming` PriorityClass (cluster-scoped: create it from one release).

Under an HPA, Connect runs at most the sum of the declared connectors' `tasksMax`, so the chart refuses a higher `autoscaling.maxReplicas` unless `allowIdleWorkers`.

## Network policy

A deny-all policy for the workers (`networkPolicy.defaultDeny`) and one policy of explicit allows:

| Flow | Setting |
|:---|:---|
| ingress: metrics | `networkPolicy.monitoring` (namespace `monitoring`, 9404) |
| ingress: REST (8083) | `networkPolicy.restApi.clients`, the Cluster Operator in `strimziOperatorNamespace` (`strimzi-operator`), the other workers; `allowAll` opens it |
| egress: DNS, API server | `networkPolicy.dns`, `networkPolicy.apiServer` |
| egress: Kafka | the Kafka namespace's `strimzi.io/cluster` pods, on the ports the bootstrap dials; an explicit bootstrap outside the cluster may be anywhere. `networkPolicy.kafka.ports` fixes the ports, and a bootstrap port outside them is refused |
| egress: Schema Registry, OTLP | `schemaRegistry.enabled`, `tracing.endpoint` |
| egress: databases | `networkPolicy.egress.databases`, each with a matching ingress policy in the database namespace unless `createIngressPolicy: false` |
| anything else | `extraIngress`, `extraEgress` |

kafka-cluster admits Connect through its own `networkPolicy.clients`.

## Observability

- **Metrics.** `metrics.type: jmxPrometheusExporter` uses the chart's rules (`files/metrics/connect-metrics.yaml`): Strimzi's, with COUNTER rules for cumulative attributes and connector captures that stop at the bean. `strimziMetricsReporter` is available; the alerts refuse it unless `alerts.allowReporterMetrics`. `metrics.existingConfigMap` points at rules of your own.
- **Scrape.** `monitoring.podMonitor`, with the Strimzi relabelings.
- **Dashboard.** One board (`dashboards.enabled`), laid out by kafka-common's grid: health, connectors and tasks, throughput and sink lag, errors and the dead letter queue, offset commits, workers, and the client path.
- **Alerts.** Scoped to the release's workers, each with a `runbook_url`. PodMonitor and alerts render only where the `monitoring.coreos.com/v1` API exists.

| Alert | Severity | Fires when |
|:---|:---|:---|
| `KafkaConnectWorkerDown` | critical | fewer workers are scraped than the release runs |
| `KafkaConnectConnectorFailed` | critical | a connector is FAILED |
| `KafkaConnectTaskFailed` | critical | a connector has FAILED tasks |
| `KafkaConnectDeadLetterFailures` | critical | a dead letter queue refuses records |
| `KafkaConnectTaskAvailabilitySLOBurning` | critical | `alerts.slo`: task availability burns its budget |
| `KafkaConnectTasksNotRunning` | warning | tasks are neither running nor paused for 5 minutes |
| `KafkaConnectErrorsLogged` | warning | record errors above `thresholds.errorRatePerSecond` |
| `KafkaConnectDeadLetterWrites` | warning | records are dead-lettered |
| `KafkaConnectOffsetCommitFailures` | warning | offset commits fail for 10 minutes |
| `KafkaConnectSinkLag` | warning | a declared sink's group lags beyond `thresholds.sinkLagRecords` (Kafka Exporter) |
| `KafkaConnectSourceIdle` | warning | `alerts.sourceIdle`: a running source polls nothing |
| `KafkaConnectRebalanceStorm` / `RebalanceTooLong` | warning | rebalances keep happening / do not finish |
| `KafkaConnectWorkerHeapHigh` | warning | heap above `thresholds.heapUsagePercent` |

Recording rules: `connect:tasks_running:ratio`, `connect:records_processed:rate5m`, `connect:errors:rate5m`, and with the SLO `connect:task_unavailability:ratio_avg{5m,30m,1h,6h}`. `scripts/check-metric-contract.sh connect-cluster` proves every series the alerts and the dashboard read is one the rules produce.

The Strimzi operator's `strimzi-kafka-connect` dashboard (shipped by strimzi-operator) reads the upstream rules' names, not this chart's; use the chart's board.

## Rails

| Setting | Refused when |
|:---|:---|
| `kafka.authentication` | `tls` without TLS; SCRAM or PLAIN on kafka-cluster's mutual-TLS port; `custom` without its configuration; a type the v1 API does not have |
| `kafkaUser` | a type a KafkaUser cannot have; `mode: custom` without `acls` |
| `extraConfig` | `exactly.once.source.support` (use `exactlyOnce`) |
| `internalTopics` | two of the three topics share a name |
| `kafka.brokerCount` | below `config.replicationFactor` or a dead letter queue's |
| connectors | see [Connectors](#connectors); a name declared twice |
| `autoscaling` | `maxReplicas` above the declared connectors' total `tasksMax` |
| `plugins` | without `imageVolumes.acknowledged`; Kubernetes before 1.31 |
| images | `:latest` or untagged `image`, plugin references, `testImages`, `secretSync.image`, `preflight.image`, `rack.clientRackInitImage` |
| `networkPolicy.kafka.ports` | the bootstrap dials a port outside them |
| alerts | without metrics; on the metrics reporter without `allowReporterMetrics` |
| `productionMode` | no TLS; no authentication; a plaintext password in a connector; `rbac.allSecrets` |
| schema | a misspelled key (pass-through objects stay open) |

`scripts/chart-matrix/connect-cluster.yaml` renders every overlay, the `kates deploy` shapes and each toggle, checks the Strimzi CRs against the pinned CRDs, and asserts each rail's message. `helm unittest charts/connect-cluster` covers the connectors, the secret scoping, the KafkaConnect and the KafkaUser.

## Helm tests and preflight

`helm test` runs, in order:

1. The Connect tier: the credentials Secret, the CR's `Ready` condition, the workers, the REST API (called from inside a worker, which the NetworkPolicy admits), the expected plugins (`tests.expectedPlugins` plus `plugins[].expect`), and each declared connector's state and tasks.
2. The test topics and test connectors.
3. A check that every test connector reaches RUNNING with all its tasks running.

The test connectors expect the platform's demo PostgreSQL; `values-prod.yaml` turns them off.

`preflight.enabled` adds a pre-install/upgrade Job that dials the Kafka cluster with the workers' Kafka client and reports `PROTOCOL`, `DNS`, `TLS`, `AUTH`, `LISTENER` or `NETWORK`. The probe is kafka-common's, shared with mirror-maker2: `kubectl logs job/<release>-preflight`.

## Values reference

| Key | Default | |
|:---|:---|:---|
| `replicas` | `3` | worker count (seed under the HPA) |
| `image` | `ghcr.io/bmscomp/connect:3.6.2-kafka-4.3.1` | Connect image with plugins |
| `version` | `4.3.1` | Kafka version of the workers |
| `groupId` | `kates-connect-cluster` | Connect group |
| `productionMode` | `false` | production rails |
| `keepOnDelete` | `true` | keep the KafkaConnect and connectors on uninstall |
| `kafka.*` | `krafter`, release namespace, SCRAM | see [Kafka connection](#kafka-connection) |
| `exactlyOnce.enabled` | `true` | exactly-once source support |
| `internalTopics.*` | `<groupId>-offsets/configs/status` | |
| `config.*` | RF 3, JSON converters with schemas | worker config |
| `extraConfig` | producer `acks=all`, idempotence, `earliest` | further worker properties |
| `schemaRegistry.*` | off | Apicurio's Confluent-compatible API |
| `plugins`, `imageVolumes.acknowledged`, `build` | off | see [Plugins](#plugins) |
| `logging` | `external`, `rootLevel: INFO`, `loggers` | or `inline` |
| `tracing.*` | OpenTelemetry, no endpoint | |
| `env`, `connectContainer`, `templateExtra` | empty | pass-throughs |
| `jvmOptions`, `resources`, probes, `jmxOptions` | 1 GiB heap, 2–4 GiB | |
| `topologySpreadConstraints`, `podAntiAffinity`, `rack`, `podDisruptionBudget`, `tolerations`, `nodeSelector`, `nodeAffinity` | zone spread, host anti-affinity, rack on | |
| `podSecurityContext`, `dnsPolicy`, `dnsConfig`, `terminationGracePeriodSeconds`, `priorityClassName`, `priorityClass` | non-root, 30 s | |
| `connectorDefaults`, `connectors` | auto-restart 10 | see [Connectors](#connectors) |
| `testConnectors`, `testTopics` | the platform's demo pipeline | `helm test` only |
| `autoscaling.*` | off, 3–10, CPU 80% | |
| `kafkaUser.*` | off | managed principal |
| `secretSync.*` | on (job) | cross-namespace credentials |
| `rbac.secretNames`, `rbac.allSecrets` | `[]`, `false` | see [Secrets](#secrets) |
| `serviceAccount.*` | created | the test pods' account; annotations also go on the workers' |
| `restApi.service`, `restApi.ingress` | ClusterIP 8083, no ingress | |
| `networkPolicy.*` | on | see [Network policy](#network-policy) |
| `metrics.*`, `monitoring.podMonitor.*` | exporter, PodMonitor on | |
| `alerts.*` | on; `sourceIdle` off, `sinkLag` on, `slo` off | |
| `dashboards.*` | on, folder `Kafka` | |
| `preflight.*` | off | |
| `tests.expectedPlugins` | the image's connectors | |
| `imagePullSecrets`, `imagePullPolicy`, `testImages.kubectl`, `clusterDomain`, `extraLabels` | | |

1.x keys still read in 2.x, listed as `DEPRECATED` in NOTES: `podMonitors`, `monitoring.{enabled,interval,…}`, `metricsConfig`, `autoRestart`, `template`, `databaseEgress`, `kafkaUser.secretSync`, `networkPolicy.{kafkaPorts,monitoringNamespace,restApiClients}`, `alerts.thresholds.sourceLagMinutes`, a `connectors` list.
