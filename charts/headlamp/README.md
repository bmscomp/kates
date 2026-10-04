# headlamp

[Headlamp](https://headlamp.dev) — a Kubernetes dashboard — packaged for the Kates platform with read-only RBAC covering core resources plus the CRDs Kates cares about (Strimzi `kafka.strimzi.io`, LitmusChaos `litmuschaos.io`, Prometheus `monitoring.coreos.com`).

## Install

```bash
helm install headlamp charts/headlamp -n headlamp --create-namespace
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

The full value reference is `values.yaml`; every key is validated by `values.schema.json`.

## Notes

Access is read-only by design: the shipped ClusterRole grants only `get`/`list`/`watch`. To let Headlamp act on resources, extend `rbac.rules` deliberately.
