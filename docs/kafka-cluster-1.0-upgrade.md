# Upgrading kafka-cluster 0.4 → 1.0 (and strimzi-operator 0.2 → 0.3)

kafka-cluster 1.0 changes how node pools, topics, users and network policy are described, names every resource after its cluster, and hands the operator-wide pieces to strimzi-operator 0.3. Nothing a 0.4 release set stops working in 1.x: the 0.4 keys are translated, and `helm upgrade` prints a `DEPRECATED` line in NOTES for each one still in use. They are removed in 2.0.

Background: [docs/kafka-charts-refactor-plan.md](kafka-charts-refactor-plan.md) §4, §7 and §10.

## Before you start

- **Brokers roll.** The metrics ConfigMap is now `<clusterName>-kafka-metrics`, and its name is part of the Kafka CR. Set `compatibility.legacyResourceNames=true` to keep the 0.4 names during the upgrade and clear it in a maintenance window. Strimzi rolls one broker at a time either way, and the new exporter rules take effect as each broker restarts.
- **Alert names changed** (Alertmanager routes match on them): `KafkaRaftLeaderElectionRate` is `KafkaRaftLeaderElections`, `KafkaRaftUncommittedRecords` is `KafkaRaftUnknownVoters`, and `StrimziOperatorDown` and the certificate alerts come from strimzi-operator. New alerts are listed in the chart README.
- **The dashboards are gone from kafka-cluster.** Enable the operator's (`strimzi-kafka-operator.dashboards.enabled`, on by default in strimzi-operator 0.3). External dashboards that read the old hand-rolled series names need the new ones (the upstream Strimzi rules).
- **`helm dependency build` may refuse** with a `Chart.lock` from 0.4: run `helm dependency update charts/kafka-cluster` once.

## Order

1. **kafka-cluster to 1.0**, with `drainCleaner.enabled=false` if you had it on (1.0 refuses `true`). Helm removes the 0.4 drain cleaner, the CRD hook, the dashboards and the operator NetworkPolicy the chart wrote into the operator's namespace.

   ```bash
   helm dependency update charts/kafka-cluster
   helm upgrade krafter charts/kafka-cluster -n kafka \
     -f <your values> \
     -f charts/kafka-cluster/values-platform.yaml \
     --set compatibility.legacyResourceNames=true
   ```

2. **strimzi-operator to 0.3**, with `drainCleaner.enabled=true` where you want it (`values-prod.yaml` has it, with two replicas and a cert-manager certificate). The operator's NetworkPolicy is on by default.

   ```bash
   helm dependency build charts/strimzi-operator
   helm upgrade strimzi-operator charts/strimzi-operator -n strimzi-operator \
     --reset-values -f charts/strimzi-operator/values-prod.yaml
   ```

   Upgrading the operator first is safe: an object a kafka-cluster 0.4 release still owns is skipped, NOTES name the owner, and the next upgrade after step 1 creates it.

3. **Clean up by hand.** 0.4's `add-broker-rebalance` and `full-rebalance` KafkaRebalances carry `helm.sh/resource-policy: keep`, so Helm leaves them behind; the first was never valid. The same goes for the `kafka-seaweedfs-credentials` Secret if you used SeaweedFS without `seaweedfs.s3.existingSecret`: point `existingSecret` at it to keep using it (with `legacyResourceNames` the chart keeps rendering it under that name).

   ```bash
   kubectl -n kafka delete kafkarebalance add-broker-rebalance full-rebalance --ignore-not-found
   ```

4. **Later, in a maintenance window**, drop `compatibility.legacyResourceNames` (brokers roll again).

`kates deploy` layers `values-platform.yaml` itself and keeps working with `kates detect`'s 0.4-shaped values.

## Key by key

| 0.4 | 1.0 | Translated in 1.x |
|:---|:---|:---|
| `controllerPools[]`, `brokerPools[]` `{name, zone, replicas, storageSize, storageClass, resources, jvmOptions}` | `nodePools.pools[]` `{name, roles, zone, replicas, storage.volumes[], resources, jvmOptions}` | yes, names kept. Setting both fails |
| `controllerDefaults`, `brokerDefaults` | `nodePools.roleDefaults.controller`, `.broker`; shared settings in `nodePools.defaults` | yes, with 0.4 pools; ignored (with a note) when an overlay clears those pools with `controllerPools: []` |
| `*Defaults.deleteClaim` | `nodePools.defaults.volume.deleteClaim` | yes |
| `*Defaults.topologySpreadConstraints`, `podAntiAffinity`, `tolerations`, `priorityClassName` | `nodePools.defaults.scheduling.{spread,antiAffinity,tolerations,priorityClassName}` | yes |
| `*Defaults.sysctl.enabled` | `nodePools.defaults.sysctls` (namespaced keys only) | no: it never ran, and now fails with the reason |
| `lifecycle.preStopSleepSeconds` | `nodePools.defaults.scheduling.terminationGracePeriodSeconds` | no: it never reached a pod |
| `kafka.metricsConfig.type` | `metrics.type` | yes, unless `metrics.type` is set (it is empty by default, and empty means `jmxPrometheusExporter`) |
| `kafka.replicas`, `zookeeper.*` | — | ignored (never read) |
| the `external` NodePort listener in the base values | `kafka.externalAccess.type: nodeport` | your own `kafka.listeners` are kept as they are |
| `kafka.authorization.superUsers: [kates-backend]` in the base values | the platform profile | with `profile: platform` |
| `topics.items[]`, `users.items[]` | maps keyed by name | yes |
| the platform's topics and users in the base values | `profile: platform` (`values-platform.yaml`) | no: add the file or `--set profile=platform` |
| `kafkaUI.managedUser: false` | `users.items.kafka-ui.enabled: false` | yes |
| `networkPolicies.enabled`, `defaultDeny`, `defaultDenySelector`, `allowDNS`, `allowDNSSelector`, `monitoringNamespace`, `operatorNamespace` | `networkPolicy.{enabled, defaultDeny.enabled, defaultDeny.selector, dns.enabled, dns.selector, monitoring.namespace, operatorNamespace}` | yes; a 1.0 key set in a higher layer wins |
| `networkPolicies.connectNamespace`, `mirrorMaker2Namespace`, `kafkaUINamespace`, `apicurioNamespace` | the namespace of the matching `networkPolicy.clients` entry | yes — and since 1.0 keeps the clients themselves in the platform profile, a release that names a namespace without a matching client is refused, with the two ways out |
| `networkPolicies.operatorPolicy` | strimzi-operator `operatorPolicy.enabled` | no (noted) |
| `networkPolicies.kafkaUI` | the kafka-ui chart's own policy | no (noted) |
| `podMonitors.{enabled,labels}` | `monitoring.podMonitor.{enabled,labels}` | yes |
| `podSecurityPolicy.*` | `kyvernoPolicy.*` | yes |
| `dashboards.*` | strimzi-operator `strimzi-kafka-operator.dashboards.*` | no (noted) |
| `crdUpgrade.enabled` | strimzi-operator `crdUpgrade.enabled` | no (noted; `kates detect` still writes it) |
| `drainCleaner.*` | strimzi-operator `drainCleaner.*` | no: `enabled: true` fails |
| `strimziOperator`, `strimzi-kafka-operator`, `kafkaConnect` | the strimzi-operator and connect-cluster charts | ignored (noted) |
| `tieredStorage.s3.*`, `retention.*`, `metadata.partitions` | `tieredStorage.remoteStorageManager.config`, `localRetentionMs`, `kafka.config` | yes |
| `tieredStorage.credentials.accessKeyId/secretAccessKey` | a Secret named by `credentials.existingSecret` | no (noted) |
| `backup.snapshotVolumes`, `defaultVolumesToFsBackup` | `backup.volumes` | yes, unless `backup.volumes` is set: it is empty by default so that a value set anywhere wins over the pair a 0.4 release carries |
| `testImages.bash` | — | ignored |

## Tiered storage

0.4 never enabled it: nothing referenced its ConfigMap. 1.0 renders it, and requires what makes it work — an image carrying a remote storage manager plugin (`tieredStorage.image`; Strimzi's image has none), the plugin's `className` and `classPath`, and credentials. `values-prod.yaml` keeps it off until such an image exists and backs up broker volumes meanwhile (`backup.volumes: fs-backup`). Turning it on for an existing cluster rolls every broker onto the new image; existing topics offload only after `remote.storage.enable: "true"` is set on them (the chart sets it on the topics it manages).

## Users that move to other charts

The platform profile still provisions `kates-connect` and `kates-mm2` (and `kafka-ui`). When `connect-cluster`, `mirror-maker2` or `kafka-ui` should own one, **adopt** it rather than recreate it — a recreated KafkaUser gets a new password, and every client holding the old one fails:

```bash
kubectl -n kafka annotate kafkauser kates-connect --overwrite \
  meta.helm.sh/release-name=connect-cluster meta.helm.sh/release-namespace=connect
kubectl -n kafka label kafkauser kates-connect --overwrite app.kubernetes.io/managed-by=Helm
helm upgrade krafter charts/kafka-cluster -n kafka --reuse-values --set users.items.kates-connect.enabled=false
helm dependency build charts/connect-cluster      # the kafka-common library
helm upgrade --install connect-cluster charts/connect-cluster -n connect --set kafkaUser.create=true
```

The KafkaUser carries `helm.sh/resource-policy: keep`, so the kafka-cluster upgrade leaves it in place for the new owner.

## Rolling back

`helm rollback krafter <revision>` restores the 0.4 manifests. With `compatibility.legacyResourceNames` still set, the rollback changes only the Kafka CR's annotations and its (unused) pod template; without it, brokers roll back onto `kafka-metrics`. Roll strimzi-operator back first if you enabled its drain cleaner, so the two releases do not both claim it.
