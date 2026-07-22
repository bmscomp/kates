# kates-platform

Umbrella chart deploying the full Kates platform in one release: the Strimzi-based Kafka cluster (`kafka-cluster`) and the Kates application (`kates`). Both subcharts resolve from local `file://` references in this repository, so the umbrella always ships the code you checked out.

## Install

```bash
helm dependency build charts/kates-platform
helm install platform charts/kates-platform -n kates --create-namespace
helm test platform -n kates
```

The Strimzi operator must be running (see `charts/strimzi-operator`) before the Kafka CRs reconcile.

## Key values

| Key | Default | Description |
|---|---|---|
| `kafka-cluster.enabled` | `true` | Deploy the Kafka cluster subchart |
| `kafka-cluster.*` | subchart defaults | Full pass-through — see `charts/kafka-cluster/values.yaml` |
| `kates.enabled` | `true` | Deploy the Kates application subchart |
| `kates.*` | subchart defaults | Full pass-through — see `charts/kates/values.yaml` |

Subchart values are validated by each subchart's own `values.schema.json`.

## Version pinning

`Chart.yaml` pins the subcharts with tolerant ranges (`>=0.2.0 <1.0.0` for kafka-cluster, `>=0.5.0 <1.0.0` for kates); the `file://` repository always resolves to the local working tree, and `Chart.lock` records the exact version at build time.
