# headlamp

[Headlamp](https://headlamp.dev) — a Kubernetes dashboard — packaged for the Kates platform with read-only RBAC covering core resources plus the CRDs Kates cares about (Strimzi `kafka.strimzi.io`, LitmusChaos `litmuschaos.io`, Prometheus `monitoring.coreos.com`).

## Install

```bash
helm install headlamp oci://ghcr.io/bmscomp/charts/headlamp -n headlamp --create-namespace
kubectl port-forward -n headlamp svc/headlamp 8080:80
```

Run the bundled connectivity test after install:

```bash
helm test headlamp -n headlamp
```

## Key values

| Key | Default | Description |
|---|---|---|
| `replicaCount` | `1` | Dashboard replicas |
| `image.repository` / `image.tag` | `ghcr.io/headlamp-k8s/headlamp` / `v0.40.1` | Headlamp image |
| `config.baseURL` | `""` | Base URL when served behind a path-rewriting proxy |
| `config.pluginsDir` | `/headlamp/plugins` | Plugin directory inside the container |
| `rbac.create` | `true` | Create the read-only ClusterRole/Binding |
| `rbac.rules` | see values | Full read-only rule set (core, apps, batch, networking, RBAC, Strimzi, Litmus, monitoring) |
| `service.type` / `service.port` | `ClusterIP` / `80` | Service exposure |
| `securityContext` / `containerSecurityContext` | hardened | Non-root, read-only rootfs, all capabilities dropped |
| `global.imageRegistry` | `""` | Registry prepended to every image (Headlamp and the Helm test) |
| `imagePullSecrets` | `[]` | Pull secrets for every pod, the Helm test included |
| `podLabels` | `{}` | Extra labels on every pod (the chart's own labels win on a clash) |
| `test.image` | `curlimages/curl:8.7.1` | Helm test image |
| `test.podSecurityContext` / `test.securityContext` | uid 100, seccomp `RuntimeDefault`, all capabilities dropped | Helm test pod security |
| `test.resources` | `25m`/`32Mi` requests, `100m`/`64Mi` limits | Helm test pod resources |

Every pod, the Helm test included, passes Pod Security Standards `restricted` with the defaults.

The full value reference is `values.yaml`; every key is validated by `values.schema.json`.

## Notes

Access is read-only by design: the shipped ClusterRole grants only `get`/`list`/`watch`. To let Headlamp act on resources, extend `rbac.rules` deliberately.
