# kates-platform

Umbrella chart deploying the full Kates platform in one release: the Strimzi-based Kafka cluster (`kafka-cluster`) and the Kates application (`kates`). Both subcharts resolve from local `file://` references in this repository, so each published version of the umbrella packs the two charts as they were at its release.

## Install

```bash
# The published chart carries its subcharts and their kafka-common library.
helm install platform oci://ghcr.io/bmscomp/charts/kates-platform -n kates --create-namespace
helm test platform -n kates
```

The Strimzi operator must be running (install `oci://ghcr.io/bmscomp/charts/strimzi-operator` first) before the Kafka CRs reconcile.

Any namespace works: Kafka and Kates both run in the release namespace, and the umbrella points Kates at it (the `kates.kafka.*`, `kates.trogdor.coordinatorUrl` and `kates.networkPolicy.kafka.namespace` rows below) and admits Kates to the brokers from it. The kates chart's own defaults name namespace `kafka`, where `kates deploy` installs Kafka. Nothing in the umbrella installs a Trogdor coordinator.

## Cluster or namespace install

One key picks who can install the release: `global.rbacScope`. Every subchart reads it as the default of its own `rbac.scope`.

| `global.rbacScope` | Installed by | Cluster-scoped RBAC | Left out |
|---|---|---|---|
| `cluster` (default) | a cluster admin | the `kates` ClusterRole; Strimzi's ClusterRoleBindings for broker and worker rack awareness | nothing |
| `namespace` | a namespace admin | none | rack awareness (Kafka, Connect, MirrorMaker 2), NodePort listeners (refused), Kyverno ClusterPolicies and Connect's PriorityClass, Kates' chaos and disruption |

```bash
helm install platform oci://ghcr.io/bmscomp/charts/kates-platform -n team-a --set global.rbacScope=namespace
```

Either way a cluster admin installs the Strimzi operator first, cluster-wide. For a namespace install the team also needs the Strimzi kinds in `admin`: install the operator with `--set strimzi-kafka-operator.createAggregateRoles=true`. Kafka UI, Apicurio Registry and CDC PostgreSQL never need a cluster grant, so they have no switch. The standalone charts take the same key as `rbac.scope`.

## Key values

| Key | Default | Description |
|---|---|---|
| `kafka-cluster.enabled` | `true` | Deploy the Kafka cluster subchart |
| `kafka-cluster.profile` | `platform` | The kafka-cluster profile carrying the platform's topics, users and client grants |
| `kafka-cluster.*` | subchart defaults | Full pass-through — see `charts/kafka-cluster/values.yaml` |
| `kates.enabled` | `true` | Deploy the Kates application subchart |
| `kates.*` | subchart defaults | Full pass-through — see `charts/kates/values.yaml` |
| `kates.kafka.bootstrapServers` | `krafter-kafka-bootstrap.{{ .Release.Namespace }}.svc:9092` | The kafka-cluster subchart's bootstrap Service; rename `krafter` with `kafka-cluster.clusterName` |
| `kates.kafka.clusterName` | `krafter` | The Kafka cluster the Kates API's chaos, topology, cluster alerts and compliance act on; rename it with `kafka-cluster.clusterName` |
| `kates.kafka.topicNamespace` | `{{ .Release.Namespace }}` | Where INTEGRATION_CDC manages its KafkaTopic CRs |
| `kates.trogdor.coordinatorUrl` | `http://trogdor-coordinator.{{ .Release.Namespace }}.svc:8889` | A Trogdor coordinator next to Kafka, if you run one |
| `kates.networkPolicy.kafka.namespace` | `{{ .Release.Namespace }}` | Namespace the Kates NetworkPolicy allows egress to for the brokers and Trogdor, and the one the Kates API's chaos, topology, cluster alerts and compliance act in |
| `kafka-cluster.networkPolicy.clients` | `kates` from the release namespace | Overrides the platform profile's `kates` grant (namespace `kates`); a list set here replaces it |
| `global.rbacScope` | `cluster` | The install mode of the release, every subchart's `rbac.scope` default: `cluster` needs a cluster admin; `namespace` leaves nothing cluster-scoped. See [Cluster or namespace install](#cluster-or-namespace-install) |
| `kates.rbac.cdcSecretNamespaces` | `[]` | Namespaces where the Kates API may read the `postgresql` Secret; INTEGRATION_CDC needs its database's namespace here, since the kates ClusterRole grants nothing on Secrets |
| `apicurio-registry.enabled` | `false` | Deploy Apicurio Registry (KafkaSQL storage against the `kafka-cluster` subchart) |
| `mirror-maker2.enabled` | `false` | Deploy MirrorMaker 2 (needs a real `mirrors[].source`) |
| `connect-cluster.enabled` | `false` | Deploy Kafka Connect next to Kafka (`kates deploy` installs it as its own release instead) |
| `test.image` | `busybox:1.37` | Image of the umbrella's Helm test pod (needs `nc`; runs as uid 65534, Pod Security "restricted"); `global.imageRegistry`, when set, is prepended with a slash |
| `test.resources` | 25m/32Mi requests, 100m/64Mi limits | Resources of the Helm test pod |
| `podLabels` | `{}` | Labels added to the Helm test pod; its own labels win a clash. Each subchart's pods take theirs from its own `podLabels` (`kates.podLabels`, `kafka-cluster.podLabels`, …) |
| `imagePullSecrets` | `[]` | Pull secrets of the Helm test pod, after `global.imagePullSecrets` (which kafka-cluster and kates read too; the other subcharts take their own imagePullSecrets); an entry is a name or `{name: …}` |

Subchart values are validated by each subchart's own `values.schema.json`.

## Version pinning

`Chart.yaml` pins the subcharts with ranges (`>=1.0.12 <2.0.0` for kafka-cluster, `>=0.11.8 <1.0.0` for mirror-maker2, `>=2.1.10 <3.0.0` for connect-cluster, `>=0.13.0 <1.0.0` for kates, `>=0.4.1 <1.0.0` for apicurio-registry); each floor is the version the umbrella was last rendered with. The `file://` repository always resolves to the local working tree, and `Chart.lock` records the exact version at build time.

Helm installs whatever sits in `charts/` without checking it against those ranges, so the umbrella checks itself: rendering fails, naming the subchart, while `charts/` holds an enabled subchart outside its range. A `charts/` left from an older build would otherwise install its old subcharts, such as kates 0.9.1, whose ClusterRole read every Secret in the cluster. Run `make platform-chart-deps` to refresh it.
