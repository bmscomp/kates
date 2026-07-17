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

The chart is a **cluster singleton**: 13 of the operator's 17 resources are cluster-scoped with hardcoded names. Two releases can never coexist.

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
| `values-prod.yaml` | Prod **uplift** — hardening, PDB, NetworkPolicy. Never applied to any cluster before; read the header. |
| `values-generic.yaml` | Unknown clusters — documents DNS-domain injection and registry redirection |

## Configuration Reference

### Chart-owned keys

| Key | Default | Description |
|-----|---------|-------------|
| `strimziVersion` | `1.1.0` | Builds the CRD bundle URL. Must equal `Chart.yaml` `appVersion` + dependency version. |
| `testImages.kubectl` | `ghcr.io/bmscomp/kates-tester:1.17.0` | Image for the CRD hook Job and Helm tests |
| `crdUpgrade.enabled` | `true` | Apply CRDs via the pre-install/pre-upgrade hook |
| `crdUpgrade.url` | `""` | Override the bundle URL (empty = derive from `strimziVersion`). Use an internal mirror when airgapped. |
| `crdUpgrade.backoffLimit` | `3` | Job retries before the install/upgrade aborts |
| `crdUpgrade.ttlSecondsAfterFinished` | `300` | Retention of a **failed** Job for log inspection. A successful Job is deleted immediately by Helm's `hook-succeeded` policy, so this only governs the failure path. |
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
| `strimzi-kafka-operator.createGlobalResources` | `true` | `false` leaves four bindings with dangling roleRefs |

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
  --set crdUpgrade.url=https://mirror.internal/strimzi-crds-1.1.0.yaml
```

## CRD Lifecycle

Helm applies a chart's `crds/` directory **on install only**. Per `helm upgrade --help`: *"no CRDs will be installed when an upgrade is performed with install flag enabled. By default, CRDs are installed if not already present."* There is no upgrade path — and `helm uninstall` never removes them either.

The upstream chart ships all ten Strimzi CRDs in `crds/`. Left alone, they would freeze at whatever version was first installed while the operator moved on, and the API server would **silently prune** fields the frozen schema does not know about — no error, no event.

The `crdUpgrade` hook closes that gap: it fetches the pinned bundle, validates it (non-empty, exactly ten CRDs, server-side dry-run) **before** mutating anything, then applies with `--server-side --force-conflicts`.

The CRDs deliberately stay in the subchart's `crds/` and are **never templated**. Templating them would break adoption of the existing release with an ownership error and — far worse — would grant `helm uninstall` the power to cascade-delete every Kafka CR in the cluster (`CRD → Kafka/KafkaNodePool → StrimziPodSet → Pods`).

## Helm Tests

```bash
helm test strimzi-operator -n strimzi-operator
```

Three hooks, all on a scoped ephemeral ServiceAccount: the operator Deployment is `Available`; all ten CRDs are `Established`; and — when `watchAnyNamespace` is true — `STRIMZI_NAMESPACE == "*"`. The last is defense-in-depth: the subchart's values block keeps `additionalProperties: true` (upstream has ~40 keys that drift each release), so a typo *inside* that block stays silent, and this assertion is what makes it loud.

## Upgrading

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

3. **This chart is a cluster singleton.** 13 of 17 resources are cluster-scoped with hardcoded names, and the leader-election Lease is namespace-scoped with a hardcoded name — so two releases cannot arbitrate, would both win, and would fight over StrimziPodSet writes. There is no parallel install-then-cutover.

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
