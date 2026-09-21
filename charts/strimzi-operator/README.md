# strimzi-operator

The [Strimzi](https://strimzi.io/) Kafka Operator. Wraps the upstream `strimzi-kafka-operator` chart with pinned kates defaults, an owned CRD-upgrade hook, and a strict values schema.

> For the deployment narrative, the migration from the ad-hoc install, and the CRD mechanics, see the book chapter [Deploying the Strimzi Operator](../../docs/book/deploying-strimzi-operator.md). Versions live in the [Version & Compatibility Matrix](../../docs/book/appendix-d-versions.md).

## Overview

The operator was previously installed by three ad-hoc `helm install oci://...` calls that had drifted apart (different memory limits, different timeouts, one silently-misspelled flag). This chart makes that install a reviewable artifact.

What it manages:

- **The operator** — via the upstream `strimzi-kafka-operator` subchart, pinned to one version, with the union of what the three retired call sites actually did.
- **CRD lifecycle** — a `pre-install`/`pre-upgrade` hook Job applies the Strimzi CRD bundle with server-side apply. This is the part Helm does not do for you (see [CRD Lifecycle](#crd-lifecycle)).
- **A strict schema** — `values.schema.json` rejects the stale flat keys the old call sites used, so a mistake fails loudly instead of being silently ignored.
- **Helm tests** — the operator is `Available`, all ten CRDs are `Established`, and the watch scope did not collapse.

In its default shape the chart is a **cluster singleton**: 13 of the operator's 17 resources are cluster-scoped with hardcoded names, and a cluster-wide operator watches every namespace, so two such releases can never coexist. The one other shape it knows is an **additional, namespace-scoped operator** — `values-namespace-scope.yaml` — which watches only its own namespace, reuses the primary's cluster-scoped RBAC (`createGlobalResources: false`), never touches the CRDs, and binds its ServiceAccount through unique-name copies of the three bindings that flag skips (`globalBindings.enabled`). The rules for when a second operator may exist — namespace scope only, same CRD generation, no newer than the primary's — are the CLI's to enforce and are written down in [the multi-version plan](../../docs/kafka-multi-version-deploy-plan.md) (§2.3, §2.10).

## Prerequisites

| Requirement | Minimum | Notes |
|-------------|---------|-------|
| **`helm dependency build`** | — | **Required.** The chart will not render without it — see below. |
| Kubernetes | 1.27+ | Enforced via `kubeVersion` |
| Helm | 3.12+ | Tested on Helm 4.2.x |
| Egress to `quay.io` | — | `helm dependency build` pulls the operator subchart |
| Egress to `github.com` | — | The CRD hook fetches the bundle on **every** install and upgrade. Mirror it via `crdUpgrade.url` — see `values-prod.yaml`. |

**The dependency build is not optional.** This chart declares a subchart, so without it every `helm template`/`install`/`upgrade`/`lint` fails before contacting the cluster:

```text
Error: an error occurred while checking for chart dependencies: found in Chart.yaml, but missing in charts/ directory: strimzi-kafka-operator
```

Note that `helm lint` only **warns** about this, so a green lint does not mean a deployable chart.

## Installation

```bash
# 1. Dependencies (REQUIRED — see above)
helm dependency build charts/strimzi-operator

# 2. Install
helm upgrade --install strimzi-operator charts/strimzi-operator \
  -n strimzi-operator --create-namespace \
  --timeout 5m --wait
```

Verify the watch scope actually took effect:

```bash
kubectl get deploy strimzi-cluster-operator -n strimzi-operator \
  -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="STRIMZI_NAMESPACE")].value}'
```

```text
*
```

### Environment Overlays

| Overlay | Purpose |
|---------|---------|
| `values-kind.yaml` | Local kind — lowers the operator to 384Mi |
| `values-dev.yaml` | Dev — `logLevel: DEBUG` |
| `values-prod.yaml` | Prod **uplift** — hardening, PDB, upstream NetworkPolicy, `productionMode`, and the drain cleaner (two replicas, cert-manager certificate). Read the header. |
| `values-generic.yaml` | Unknown clusters — documents DNS-domain injection and registry redirection |
| `values-namespace-scope.yaml` | An **additional** operator, co-located with the Kafka namespace it watches: `watchAnyNamespace: false`, `createGlobalResources: false`, `crdUpgrade.enabled: false`, `globalBindings.enabled: true`, dashboards and drain cleaner off (once per Kubernetes cluster), kind-sized. Never for the primary. |

## Configuration Reference

### Chart-owned keys

| Key | Default | Description |
|-----|---------|-------------|
| `strimziVersion` | `1.2.0` | Builds the CRD bundle URL. Must equal `Chart.yaml` `appVersion` + dependency version. |
| `testImages.kubectl` | `ghcr.io/bmscomp/kates-tester:1.23.0` | Image for the CRD hook Job and Helm tests |
| `crdUpgrade.enabled` | `true` | Apply CRDs via the pre-install/pre-upgrade hook |
| `crdUpgrade.url` | `""` | Override the bundle URL (empty = derive from `strimziVersion`). Use an internal mirror when airgapped. |
| `crdUpgrade.backoffLimit` | `3` | Job retries before the install/upgrade aborts |
| `crdUpgrade.ttlSecondsAfterFinished` | `300` | Retention of a **failed** Job for log inspection. A successful Job is deleted immediately by Helm's `hook-succeeded` policy, so this only governs the failure path. |
| `globalBindings.enabled` | `false` | Render `ClusterRoleBinding`s named `strimzi-cluster-operator-<namespace>-{global,kafka-broker-delegation,kafka-client-delegation}` binding this release's ServiceAccount to the primary's `strimzi-cluster-operator-global`, `strimzi-kafka-broker` and `strimzi-kafka-client` ClusterRoles. On for additional operators (`createGlobalResources: false` skips upstream's fixed-name copies). |
| `operatorPolicy.enabled` | `true` | Render the Cluster Operator's NetworkPolicy in the release namespace: probe/metrics ingress on 8080, egress to DNS, the API server and every `strimzi.io/kind` pod in the watched namespaces (all namespaces under `watchAnyNamespace`) on 9090–9093, 8443 and 8083. `charts/kafka-cluster` 0.4 wrote this policy into the operator namespace; while such a release still owns the object, this chart skips it and NOTES say so. kafka-cluster 1.0 no longer renders it. |
| `productionMode` | `false` | Refuse unsafe production settings (a single drain-cleaner replica) |
| `drainCleaner.enabled` | `false` | The [Strimzi Drain Cleaner](https://github.com/strimzi/drain-cleaner): a validating webhook that turns node-drain evictions of Kafka pods into operator-driven rolls. **One per Kubernetes cluster** — enable it on the primary operator's release only. Moved here from kafka-cluster 0.4. |
| `drainCleaner.tls.certManager.enabled` | `false` | Issue the webhook certificate with cert-manager and inject its CA (`issuerRef` empty: a self-signed Issuer). Without this, `tls.secretName` plus `tls.caBundle` are required — the render fails otherwise, because an uncallable webhook with `failurePolicy: Ignore` lets every eviction through. |
| `drainCleaner.replicas` | `1` | 2+ in production; a PDB is rendered above one replica |
| `drainCleaner.denyEviction` | `true` | Deny the eviction and have the operator roll the pod (Strimzi's recommended mode) |
| `drainCleaner.namespaces` | `[]` | Namespaces whose evictions are intercepted (empty: the operator's watch scope) |
| `drainCleaner.image` | `quay.io/strimzi/drain-cleaner:1.6.1` | Floating tags are refused |
| `monitoring.podMonitor.enabled` | `true` | PodMonitor for the operator (`http` port). Rendered only where the `monitoring.coreos.com/v1` API exists. |
| `alerts.enabled` | `true` | `StrimziOperatorDown`, `StrimziReconciliationsFailing`, `KafkaCertificateExpiringSoon` and `KafkaCertificateExpiryCritical` (on `strimzi_certificate_expiration_timestamp_ms`, for every cluster the operator manages). Thresholds: `alerts.thresholds.certificate{Warning,Critical}Days` (30, 7). Runbook: `docs/kafka-cluster-runbook.md`. |
| `nameOverride` / `fullnameOverride` | `""` | Standard name overrides |
| `commonLabels` / `commonAnnotations` | `{}` | Applied to chart-owned resources |

### Operator keys (`strimzi-kafka-operator.*`)

Only these deviate from stock upstream. Everything else in the [upstream values](https://github.com/strimzi/strimzi-kafka-operator/blob/main/helm-charts/helm3/strimzi-kafka-operator/values.yaml) passes through.

| Key | Default | Why |
|-----|---------|-----|
| `strimzi-kafka-operator.replicas` | `1` | All three retired call sites |
| `strimzi-kafka-operator.watchAnyNamespace` | `true` | All three retired call sites; verified live. This default is what makes the retired flat key harmless. |
| `strimzi-kafka-operator.operationTimeoutMs` | `900000` | Live value (upstream: 300000) |
| `strimzi-kafka-operator.resources.{limits,requests}.memory` | `768Mi` | Live value (upstream: 384Mi). **Memory only** — cpu inherits upstream's 1000m/200m rather than duplicating it here. |
| `strimzi-kafka-operator.leaderElection.enable` | `true` | The **correct** key. See [Upgrading](#upgrading). |
| `strimzi-kafka-operator.kubernetesServiceDnsDomain` | `cluster.local` | Upstream only emits `KUBERNETES_SERVICE_DNS_DOMAIN` when this differs — callers on custom domains **must** inject it. |
| `strimzi-kafka-operator.fullReconciliationIntervalMs` | `120000` | Upstream default, verified live |
| `strimzi-kafka-operator.createGlobalResources` | `true` | `false` is the additional-operator shape: the fixed-name ClusterRoles and bindings come from the primary's release. On its own it leaves the ServiceAccount unbound from three of them — pair it with `globalBindings.enabled: true`. |
| `strimzi-kafka-operator.watchNamespaces` | `[]` | With `watchAnyNamespace: false`, the namespaces to watch. The release namespace is always included, so `[]` means "only my own namespace". |
| `strimzi-kafka-operator.dashboards.enabled` | `true` | The operator's nine Grafana dashboards (Kafka, KRaft, Cruise Control, Kafka Exporter, Connect, MirrorMaker 2, Kafka Bridge, Kafka OAuth, operators). They read exactly the series kafka-cluster's vendored exporter rules produce; kafka-cluster 1.0 has no dashboards of its own. Names are fixed: enable on one operator release. |

> **There is no `global` block.** Upstream contains zero `.Values.global` references and ignores `global.imageRegistry` entirely. Declaring it to satisfy the schema would convert a loud failure into a silent one, so `--set global.*` is rejected. For registry redirection use `strimzi-kafka-operator.defaultImageRegistry`.

## Examples

Redirect images to an internal registry:

```bash
helm upgrade --install strimzi-operator charts/strimzi-operator -n strimzi-operator \
  --set strimzi-kafka-operator.defaultImageRegistry=registry.internal \
  --set strimzi-kafka-operator.defaultImageRepository=strimzi
```

Non-default cluster DNS domain:

```bash
helm upgrade --install strimzi-operator charts/strimzi-operator -n strimzi-operator \
  -f charts/strimzi-operator/values-generic.yaml \
  --set strimzi-kafka-operator.kubernetesServiceDnsDomain="${CLUSTER_DOMAIN}"
```

Airgapped CRD mirror:

```bash
helm upgrade --install strimzi-operator charts/strimzi-operator -n strimzi-operator \
  --set crdUpgrade.url=https://mirror.internal/strimzi-crds-1.2.0.yaml
```

## CRD Lifecycle

Helm applies a chart's `crds/` directory **on install only**. Per `helm upgrade --help`: *"no CRDs will be installed when an upgrade is performed with install flag enabled. By default, CRDs are installed if not already present."* There is no upgrade path — and `helm uninstall` never removes them either.

The upstream chart ships all ten Strimzi CRDs in `crds/`. Left alone, they would freeze at whatever version was first installed while the operator moved on, and the API server would **silently prune** fields the frozen schema does not know about — no error, no event.

The `crdUpgrade` hook closes that gap: it fetches the pinned bundle, validates it (non-empty; every one of the ten CRDs this platform renders CRs for is defined in it — not an exact count, since other Strimzi releases ship more or fewer; server-side dry-run) **before** mutating anything, then applies with `--server-side --force-conflicts`.

CRDs belong to the **newest** operator on the cluster — the primary's release. An additional operator (`values-namespace-scope.yaml`) runs with the hook disabled, because an older release's bundle would roll the CRDs back.

The CRDs deliberately stay in the subchart's `crds/` and are **never templated**. Templating them would break adoption of the existing release with an ownership error and — far worse — would grant `helm uninstall` the power to cascade-delete every Kafka CR in the cluster (`CRD → Kafka/KafkaNodePool → StrimziPodSet → Pods`).

## Helm Tests

```bash
helm test strimzi-operator -n strimzi-operator
```

Three hooks, all on a scoped ephemeral ServiceAccount: the operator Deployment is `Available`; all ten CRDs are `Established`; and — when `watchAnyNamespace` is true — `STRIMZI_NAMESPACE == "*"`. The last is defense-in-depth: the subchart's values block keeps `additionalProperties: true` (upstream has ~40 keys that drift each release), so a typo *inside* that block stays silent, and this assertion is what makes it loud.

## Upgrading

### 0.2 → 0.3 (with kafka-cluster 1.0)

The drain cleaner, the operator's NetworkPolicy, scrape and alerts, and the dashboards move here from `kafka-cluster`. Upgrade in this order:

1. `kafka-cluster` to 1.0 with `drainCleaner.enabled=false` (1.0 refuses `true`). Helm removes its drain cleaner and its copy of the operator NetworkPolicy.
2. This chart to 0.3, with `drainCleaner.enabled=true` where you had it (`values-prod.yaml` does).

Upgrading this chart first is safe: an object still owned by a `kafka-cluster` 0.4 release is skipped (NOTES name the owner), and the next upgrade of this chart after step 1 creates it. See [docs/kafka-cluster-1.0-upgrade.md](../../docs/kafka-cluster-1.0-upgrade.md).

### Ad-hoc OCI install → this chart

Adoption is a pure in-place patch — every resource name is identical, so nothing is recreated. Retarget your `--set` flags:

| Old | New |
|-----|-----|
| `--set watchAnyNamespace=X` | `--set strimzi-kafka-operator.watchAnyNamespace=X` |
| `--set replicas=X` | `--set strimzi-kafka-operator.replicas=X` |
| `--set resources.*` | `--set strimzi-kafka-operator.resources.*` |
| `--set operationTimeoutMs=X` | `--set strimzi-kafka-operator.operationTimeoutMs=X` |
| `--set kubernetesServiceDnsDomain=X` | `--set strimzi-kafka-operator.kubernetesServiceDnsDomain=X` |
| `--set leaderElection.enabled=X` | `--set strimzi-kafka-operator.leaderElection.enable=X` — **the old key was a silent no-op** |
| `--set global.imageRegistry=X` | `--set strimzi-kafka-operator.defaultImageRegistry=X` — **`global` was always ignored by upstream** |

**Behavioral changes to review:**

1. **NEVER upgrade without explicit values — bare `helm upgrade` *is* `--reuse-values`.** Helm copies the previous release's stored values when none are supplied. Those stored values are the stale flat keys from the retired call sites, which this chart's schema rejects:

   ```text
   Error: UPGRADE FAILED: values don't meet the specifications of the schema(s) in the following chart(s):
   strimzi-operator:
   - at '': additional properties 'kubernetesServiceDnsDomain', 'leaderElection', 'replicas', 'resources', 'watchAnyNamespace', 'operationTimeoutMs' not allowed
   ```

   Pass `--reset-values` (or `-f <overlay>`) — the rule is *always pass explicit values*, which is stronger than "never pass `--reuse-values`". This pain is **one-time**: after the first `--reset-values` adoption the stored config is clean and later bare upgrades pass.

2. **`helm upgrade` does not update CRDs.** The `crdUpgrade` hook does. See [CRD Lifecycle](#crd-lifecycle).

3. **A cluster-wide operator is a cluster singleton.** 13 of 17 resources are cluster-scoped with hardcoded names, and the leader-election Lease is namespace-scoped with a hardcoded name — so two cluster-wide releases cannot arbitrate, would both win, and would fight over StrimziPodSet writes. There is no parallel install-then-cutover. Additional operators are possible only in the namespace-scoped shape (`values-namespace-scope.yaml`), in their own namespaces, beside a primary that is itself namespace-scoped.

4. **Adopting rolls the entire Kafka data plane.** The operand image tracks the operator version and no node pool pins an image, so every broker and controller restarts. This needs a maintenance window and the `strimzi.io/pause-reconciliation` procedure — see the [book chapter](../../docs/book/deploying-strimzi-operator.md).

### Troubleshooting

Adoption ownership errors:

```text
invalid ownership metadata; label validation error: missing key "app.kubernetes.io/managed-by": must be set to "Helm"; annotation validation error: missing key "meta.helm.sh/release-name"
```

```text
annotation validation error: key "meta.helm.sh/release-name" must equal "X": current value is "Y"
```

Remedy: `kubectl label --overwrite` / `kubectl annotate --overwrite` the offending resource, then retry.
