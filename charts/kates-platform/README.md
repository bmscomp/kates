# kates-platform

Umbrella chart deploying the full Kates platform in one release: the Strimzi-based Kafka cluster (`kafka-cluster`) and the Kates application (`kates`). Both subcharts resolve from local `file://` references in this repository, so the umbrella always ships the code you checked out.

## Install

```bash
# The three Kafka subcharts each depend on the kafka-common library, and
# `helm dependency build` takes one chart at a time.
for c in kafka-cluster mirror-maker2 connect-cluster; do helm dependency build "charts/$c"; done
helm dependency update charts/kates-platform
helm install platform charts/kates-platform -n kates --create-namespace
helm test platform -n kates
```

The Strimzi operator must be running (see `charts/strimzi-operator`) before the Kafka CRs reconcile.

## Key values

| Key | Default | Description |
|---|---|---|
| `kafka-cluster.enabled` | `true` | Deploy the Kafka cluster subchart |
| `kafka-cluster.profile` | `platform` | The kafka-cluster profile carrying the platform's topics, users and client grants |
| `kafka-cluster.*` | subchart defaults | Full pass-through — see `charts/kafka-cluster/values.yaml` |
| `kates.enabled` | `true` | Deploy the Kates application subchart |
| `kates.*` | subchart defaults | Full pass-through — see `charts/kates/values.yaml` |
| `apicurio-registry.enabled` | `false` | Deploy Apicurio Registry (KafkaSQL storage against the `kafka-cluster` subchart) |
| `mirror-maker2.enabled` | `false` | Deploy MirrorMaker 2 (needs a real `mirrors[].source`) |
| `connect-cluster.enabled` | `false` | Deploy Kafka Connect next to Kafka (`kates deploy` installs it as its own release instead) |

Subchart values are validated by each subchart's own `values.schema.json`.

## Version pinning

`Chart.yaml` pins the subcharts with ranges (`>=1.0.0 <2.0.0` for kafka-cluster, `>=0.8.0 <1.0.0` for mirror-maker2, `>=2.0.0 <3.0.0` for connect-cluster, `>=0.5.0 <1.0.0` for kates, `>=0.2.0 <1.0.0` for apicurio-registry); the `file://` repository always resolves to the local working tree, and `Chart.lock` records the exact version at build time.
