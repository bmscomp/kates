# Upgrading connect-cluster 1.3 → 2.0

connect-cluster 2.0 is built on the kafka-common library, keys connectors by name, validates them at render time, and grants the workers only the Secrets their connectors reference. 1.x values keep working through 2.x: each moved key is translated, and `helm upgrade` prints a `DEPRECATED` line in NOTES for each one still in use. They are removed in 3.0.

Background: [docs/kafka-charts-refactor-plan.md](kafka-charts-refactor-plan.md) §5 and §10.

## Before you start

- **Build the dependency.** The chart now depends on `kafka-common` (`file://../kafka-common`): run `helm dependency build charts/connect-cluster` (`kates deploy` does it).
- **Workers roll once.** The metrics ConfigMap key is `metrics-config.yml`, and the pod template's checksums now cover only the rules and the logging configuration, so later chart bumps alone no longer roll the workers.
- **Secret access narrows.** 1.x let the workers read every Secret in the Connect namespace, and every Secret in the Kafka namespace when `kafka.namespace` was set. 2.0 grants `get` on the Secrets that the connectors and test connectors in the values reference. Connectors applied some other way need their Secrets in `rbac.secretNames` (see below). `rbac.allSecrets: true` restores the old breadth for the transition.
- **Kafka egress follows the bootstrap.** 1.x allowed 9092 and 9093 whatever the bootstrap dialled; 2.0 allows the port the bootstrap dials. `networkPolicy.kafka.ports` sets them explicitly.
- **The operator namespace defaults to `strimzi-operator`** (1.x used the Kafka namespace, where `kates deploy` does not run the operator). Set `networkPolicy.strimziOperatorNamespace` if yours differs.
- **Alert names changed** (Alertmanager routes match on them): `KafkaConnectTaskCountMismatch` is `KafkaConnectTasksNotRunning`, `KafkaConnectHighErrorRate` is `KafkaConnectErrorsLogged`, and `KafkaConnectSourceLag` is `KafkaConnectSourceIdle`, which is now opt-in (`alerts.sourceIdle.enabled`). The new alerts are listed in the chart README.
- **The validation hook is gone.** Connector mistakes now fail `helm upgrade` itself, with the connector and the setting named.

## Order

1. Check the render against your values, and read the `DEPRECATED` list:

   ```bash
   helm dependency build charts/connect-cluster
   helm template connect-cluster charts/connect-cluster -n connect -f <your values> > /dev/null
   helm upgrade connect-cluster charts/connect-cluster -n connect -f <your values> --dry-run | sed -n '/^NOTES/,$p'
   ```

2. Upgrade:

   ```bash
   helm upgrade connect-cluster charts/connect-cluster -n connect -f <your values>
   ```

3. Check that the connectors still run. A connector that fails with `Forbidden` reading a Secret needs that Secret in `rbac.secretNames`; see [the runbook](connect-cluster-runbook.md#a-connector-cannot-read-a-secret).

4. Move the deprecated keys at your own pace.

## Key by key

| 1.x | 2.0 | Translated in 2.x |
|:---|:---|:---|
| `connectors: [{name, …}]` | `connectors: {<name>: {…}}` | yes |
| `autoRestart` | `connectorDefaults.autoRestart` | yes (1.x wins when set) |
| `extraConfig.exactly.once.source.support` | `exactlyOnce.enabled` | no: refused in `extraConfig`, with the reason |
| `internalTopics.prefix` (never read) | `internalTopics.prefix` (read) | — |
| `kafka.authentication.type: oauth`, `tls-external`; `tracing.type: jaeger` | `custom` (with `custom.sasl` and `custom.config`); `opentelemetry` | no: none of these ever produced a valid CR |
| `kafka.tls.trustedCertificateSecret: krafter-cluster-ca-cert` | empty = `<clusterName>-cluster-ca-cert` | — |
| `kafkaUser.secretSync.*` | `secretSync.*` (plus `watch`, `schedule`, `image`, `secrets`) | yes |
| `rbac.secretNames` (both namespaces) | `rbac.secretNames` (`name` here, or `namespace/name`) | — |
| no RBAC without `serviceAccount.create` | secret RBAC always; `serviceAccount.create` only makes the test pods' account | — |
| `databaseEgress` | `networkPolicy.egress.databases` | yes (1.x wins when set) |
| `networkPolicy.kafkaPorts`, `monitoringNamespace`, `restApiClients` | `networkPolicy.kafka.ports`, `monitoring.namespace`, `restApi.clients` | yes (1.x wins when set) |
| `podMonitors.{enabled,labels}` | `monitoring.podMonitor.{enabled,labels}` | yes |
| `monitoring.{enabled,interval,scrapeTimeout,honorLabels,metricRelabelings}` | `monitoring.podMonitor.*` | yes (`monitoring.enabled` was never read) |
| `metricsConfig.{create,configMapName,configMapKey}` | `metrics.existingConfigMap.{name,key}` | yes |
| `template` | `templateExtra` (deep-merged) | yes |
| `podSecurityContext.seccompProfile: RuntimeDefault` | `seccompProfile: {type: RuntimeDefault}` | yes |
| `alerts.thresholds.sourceLagMinutes` | `alerts.thresholds.sourceIdleMinutes` | yes |
| `priorityClassName: system-cluster-critical` | `""` | — (`values-prod.yaml` sets `kates-streaming`) |
| `Chart.yaml` `appVersion: 3.6.2` (Debezium) | `4.3.1` (Kafka); the image in `kates.io/connect-image` | — |

## Connectors applied outside the chart

`kates deploy` applies its CDC connectors with `kubectl`, next to the release. Their configs read `connect-pg-credentials` and `kates-connect`, which the kind and generic overlays list in `rbac.secretNames`. Do the same for any connector you apply by hand or with another tool:

```yaml
rbac:
  secretNames:
    - pg-credentials            # this namespace
    - vault-sync/api-token      # another namespace
```

## Moving production to TLS

`values-prod.yaml` now dials kafka-cluster's TLS listener (9093), which authenticates by certificate, so the release owns a `tls` KafkaUser and copies its certificate and the cluster CA into the Connect namespace (`secretSync.watch` follows renewals). 1.x's prod overlay used SCRAM on 9092.

If kafka-cluster's platform profile provisions `kates-connect`, hand the object over rather than recreating it:

```bash
kubectl -n kafka annotate kafkauser kates-connect --overwrite \
  meta.helm.sh/release-name=connect-cluster meta.helm.sh/release-namespace=connect
kubectl -n kafka label kafkauser kates-connect --overwrite app.kubernetes.io/managed-by=Helm
helm dependency build charts/kafka-cluster        # kafka-cluster needs kafka-common too
helm upgrade krafter charts/kafka-cluster -n kafka --reuse-values --set users.items.kates-connect.enabled=false
helm upgrade connect-cluster charts/connect-cluster -n connect -f charts/connect-cluster/values-prod.yaml
```

The User Operator switches the user from SCRAM to a certificate. Clients still holding the SCRAM password (Debezium's schema history producer, for example) need the same switch.

Staying on SCRAM is a values change: `kafka.tls.enabled: false`, `kafka.authentication.type: scram-sha-512` — which `productionMode` refuses, so set that to `false` too, knowingly.

## Rolling back

`helm rollback connect-cluster <revision>` restores the 1.3 manifests. The workers roll back onto the `kafka-metrics-config.yml` key, and the 1.x secret RBAC returns.
