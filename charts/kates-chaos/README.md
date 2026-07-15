# kates-chaos

Chaos engineering for Kates/Kafka on Kubernetes. Wraps the [LitmusChaos](https://litmuschaos.io/) **execution plane** (`litmus-core` subchart: chaos operator, exporter, CRDs) with Kafka-specific RBAC, ChaosExperiment/ChaosEngine templating, monitoring, network isolation, and admission policy.

> For the full deployment narrative, troubleshooting, and architecture diagram, see the [Kates Chaos Chart — Deployment Guide](../../docs/kates-chaos-chart.md). For chaos-engineering theory, see the [chaos book chapters](../../docs/book/06-chaos-theory.md).

## Overview

This chart deploys the LitmusChaos **execution plane only** — it does **not** deploy the ChaosCenter web portal (frontend / GraphQL / auth-server / MongoDB). Chaos is driven declaratively through the `engines:` values or by applying `ChaosEngine` resources. If you want the UI, install ChaosCenter separately.

What it manages:

- **RBAC** — a `litmus-admin` ServiceAccount + Role in each `target` namespace, a coordinator ClusterRole binding in each `coordinator` namespace, and optional cluster-scoped node-chaos RBAC.
- **ChaosExperiments** — installed into your target namespaces via a post-install Job.
- **ChaosEngines** — rendered from `engines:`, with first-class probes and pod components.
- **Monitoring** — optional ServiceMonitor + Grafana dashboard.
- **NetworkPolicy / Kyverno / PDB / GameDay** — optional, all off by default.

## Prerequisites

| Requirement | Minimum | Notes |
|-------------|---------|-------|
| Kubernetes | 1.25+ | Any managed or self-managed cluster |
| Helm | 3.12+ | Uses the `litmus-core` subchart dependency |
| LitmusChaos CRDs | 3.28 | Applied before install (hooks depend on them) |
| Target namespaces | — | Every `rbac.targetNamespaces` entry must exist |

## Installation

### Quick Start

```bash
# 1. CRDs (hooks depend on them)
kubectl apply -f config/litmus/chaos-litmus-chaos-enable.yml
kubectl wait --for=condition=Established crd/chaosexperiments.litmuschaos.io --timeout=60s

# 2. Dependencies
helm dependency build charts/kates-chaos

# 3. Install
helm upgrade --install chaos charts/kates-chaos \
  -n litmus --create-namespace \
  -f charts/kates-chaos/values-generic.yaml --wait
```

### Overlays

| Overlay | Use | Notable defaults |
|---------|-----|------------------|
| `values-kind.yaml` | Local Kind / dev | `pullPolicy: IfNotPresent`, NetworkPolicy off |
| `values-generic.yaml` | Production K8s | `pullPolicy: Always`, NetworkPolicy + Kyverno on |

## Configuration Reference

### Core

| Parameter | Description | Default |
|-----------|-------------|---------|
| `nameOverride` | Override chart name | `""` |
| `fullnameOverride` | Override full release name | `""` |
| `commonLabels` | Labels added to every resource | `{}` |
| `commonAnnotations` | Annotations added to every resource | `{}` |
| `serviceAccount.name` | Chaos service account (shared by RBAC + engines) | `litmus-admin` |

### Global & Images

| Parameter | Description | Default |
|-----------|-------------|---------|
| `global.imageRegistry` | Registry prefix for all chart-managed images | `""` |
| `global.imagePullPolicy` | Pull policy for chart-managed pods | `IfNotPresent` |
| `global.imagePullSecrets` | Pull secrets for chart-managed pods | `[]` |
| `global.clusterDomain` | Cluster DNS domain | `cluster.local` |
| `images.kubectl.repository` | Image for installer Job / GameDay / tests | `registry.k8s.io/kubectl` |
| `images.kubectl.tag` | kubectl image tag | `v1.33.0` |
| `images.kubectl.digest` | Pin by digest (wins over tag) | `""` |
| `images.goRunner.repository` | Default runtime for experiments | `litmuschaos/go-runner` |
| `images.goRunner.tag` | go-runner image tag | `3.28.0` |

The `litmus-core` subchart images (operator/runner) are set under the `litmus-core:` key.

### RBAC

| Parameter | Description | Default |
|-----------|-------------|---------|
| `rbac.enabled` | Render cross-namespace RBAC | `true` |
| `rbac.targetNamespaces` | List of `{name, role}` — `target` (fault injection) or `coordinator` (create engines/read results) | `[{kafka, target}, {kates, coordinator}]` |
| `rbac.targetNamespaces[].serviceAccount` | SA bound for a `coordinator` entry | `default` |
| `rbac.nodeChaos.enabled` | Cluster-scoped RBAC for node-level chaos | `true` |
| `rbac.kafkaNamespace` | **Deprecated** — use `targetNamespaces` | `""` |
| `rbac.katesNamespace` | **Deprecated** — use `targetNamespaces` | `""` |

### Experiments

| Parameter | Description | Default |
|-----------|-------------|---------|
| `experiments.enabled` | Install ChaosExperiment definitions (post-install Job) | `true` |
| `experiments.targetNamespaces` | Namespaces to install into (empty = every `target` namespace) | `[]` |
| `experiments.installer.backoffLimit` | Installer Job retries | `3` |
| `experiments.installer.ttlSecondsAfterFinished` | Installer Job TTL | `300` |
| `experiments.installer.resources` / `securityContext` / `nodeSelector` / `tolerations` | Installer pod tuning | `{}` / `[]` |
| `experiments.definitions[].name` | Experiment name | — |
| `experiments.definitions[].scope` | `Namespaced` or `Cluster` | `Namespaced` |
| `experiments.definitions[].env` | Default env baked into the ChaosExperiment | — |
| `experiments.definitions[].permissions` | `spec.definition.permissions` (runner RBAC) | — |

Default definitions: `pod-delete`, `pod-cpu-hog`, `pod-memory-hog`, `pod-network-partition`, `pod-io-stress`, `pod-dns-error`, `node-drain`.

### Engines

Each key under `engines:` renders a `ChaosEngine`. Probes and pod `components` are first-class.

| Parameter | Description | Default |
|-----------|-------------|---------|
| `engines.<name>.enabled` | Render this engine | `false` |
| `engines.<name>.appNamespace` | Target namespace | primary `target` ns |
| `engines.<name>.appLabel` | App selector, e.g. `strimzi.io/kind=Kafka` | — |
| `engines.<name>.appKind` | `statefulset` / `deployment` / … | `statefulset` |
| `engines.<name>.experiment` | Experiment name | `<name>` |
| `engines.<name>.serviceAccount` | Override the chaos SA | `serviceAccount.name` |
| `engines.<name>.engineState` | `active` / `stop` | `active` |
| `engines.<name>.annotationCheck` | Require chaos annotation on the target | `false` |
| `engines.<name>.jobCleanUpPolicy` | `delete` / `retain` | — |
| `engines.<name>.duration` / `interval` | `TOTAL_CHAOS_DURATION` / `CHAOS_INTERVAL` | `30` / `10` |
| `engines.<name>.force` | Set `FORCE=true` | `false` |
| `engines.<name>.targetPods` / `podsAffectedPerc` | Scope the blast radius | — |
| `engines.<name>.env` | Extra experiment env (map) | `{}` |
| `engines.<name>.components` | `nodeSelector` / `resources` / `tolerations` for the runner | `{}` |
| `engines.<name>.probes` | Litmus probes (`cmdProbe`/`httpProbe`/`promProbe`), passed through | `[]` |

### Network Policy

| Parameter | Description | Default |
|-----------|-------------|---------|
| `networkPolicy.enabled` | Scope chaos-infra traffic | `false` |
| `networkPolicy.apiServer.enabled` | Egress to the Kubernetes API server (**operator needs it**) | `true` |
| `networkPolicy.apiServer.ports` / `ipBlock` | API-server ports / CIDR pin | `[443,6443]` / `{}` |
| `networkPolicy.dns.enabled` | DNS egress | `true` |
| `networkPolicy.monitoring.enabled` | Prometheus scrape ingress to the exporter | `true` |
| `networkPolicy.monitoring.namespace` / `port` | Scrape source ns / exporter port | `monitoring` / `8080` |
| `networkPolicy.targets.enabled` | Egress to `rbac.targetNamespaces` | `true` |
| `networkPolicy.extraIngress` / `extraEgress` | Raw rules appended verbatim | `[]` |
| `networkPolicies.enabled` | **Deprecated** alias — OR'd with `networkPolicy.enabled` | `false` |

### Monitoring

| Parameter | Description | Default |
|-----------|-------------|---------|
| `monitoring.serviceMonitor.enabled` | Create a ServiceMonitor for the exporter | `false` |
| `monitoring.serviceMonitor.selector` | Exporter Service label selector | `{app: litmus}` |
| `monitoring.serviceMonitor.port` / `path` / `interval` | Scrape endpoint | `http` / `/metrics` / `30s` |
| `monitoring.serviceMonitor.scrapeTimeout` / `honorLabels` | Scrape tuning | `""` / `false` |
| `monitoring.serviceMonitor.relabelings` / `metricRelabelings` | Relabel rules | `[]` |
| `monitoring.grafanaDashboard.enabled` | Ship the dashboard ConfigMap | `false` |
| `monitoring.grafanaDashboard.namespace` | Namespace used in dashboard PromQL | release ns |
| `monitoring.grafanaDashboard.folder` | `grafana_folder` annotation | `""` |

> The `litmus-core` subchart can ship its own exporter ServiceMonitor — leave `monitoring.serviceMonitor.enabled=false` to avoid duplicate scrapes, and enable it only if you manage scraping here.

### Kyverno, PDB & GameDay

| Parameter | Description | Default |
|-----------|-------------|---------|
| `kyvernoPolicy.enabled` | Pod-security ClusterPolicy (needs Kyverno) | `false` |
| `kyvernoPolicy.action` | `Audit` / `Enforce` | `Audit` |
| `kyvernoPolicy.namespaces` | Namespaces the policy applies to (empty = release ns) | `[]` |
| `kyvernoPolicy.excludeSelector` | Pods skipped (need privileges) | `chaosUID Exists` |
| `pdb.enabled` | PodDisruptionBudget (requires `selector`) | `false` |
| `pdb.minAvailable` / `maxUnavailable` / `selector` | PDB config | `1` / `""` / `{}` |
| `gameday.enabled` | Run the validation Job | `false` |
| `gameday.checks` | Data-driven `{name, command}` checks (empty = default set) | `[]` |

## Examples

### Kafka leader-kill with an ISR-recovery probe

```yaml
engines:
  kafka-leader-kill:
    enabled: true
    appLabel: "strimzi.io/kind=Kafka"
    experiment: pod-delete
    duration: 60
    force: true
    targetPods: "krafter-pool-alpha-0"
    podsAffectedPerc: "100"
    jobCleanUpPolicy: retain
    probes:
      - name: verify-isr-failover
        type: cmdProbe
        mode: PostChaos
        runProperties: { probeTimeout: "120s", interval: "10s", retry: 6 }
        cmdProbe/inputs:
          command: "kubectl exec -n kafka krafter-pool-alpha-1 -- kafka-topics.sh --bootstrap-server localhost:9092 --list"
          comparator: { type: string, criteria: contains, value: "orders" }
```

### Multiple target namespaces

```yaml
rbac:
  targetNamespaces:
    - { name: kafka,     role: target }
    - { name: streaming, role: target }
    - { name: kates,     role: coordinator }
experiments:
  targetNamespaces: [kafka, streaming]   # install experiments into both
```

### Locked-down cluster (pin the API server, custom GameDay)

```yaml
networkPolicy:
  enabled: true
  apiServer:
    ipBlock: { cidr: 10.0.0.1/32 }
gameday:
  enabled: true
  checks:
    - { name: CRDs installed, command: "kubectl get crd chaosengines.litmuschaos.io" }
    - { name: Brokers Running, command: "kubectl get pods -n kafka -l strimzi.io/kind=Kafka | grep Running" }
```

## Helm Tests

```bash
helm test chaos -n litmus
```

Runs two hooks: the chaos **operator** is `Available`, and the Litmus **CRDs** are established with experiments installed. Both use a scoped, ephemeral test ServiceAccount.

## Upgrading

### 1.x → 2.0.0 (breaking)

2.0.0 rescopes the chart to the execution plane and removes portal assumptions. Renamed keys are **still honored** (deprecated, win when set):

| Old | New |
|-----|-----|
| `rbac.kafkaNamespace` / `rbac.katesNamespace` | `rbac.targetNamespaces` (list of `{name, role}`) |
| `networkPolicies.enabled` | `networkPolicy.enabled` (now structured) |
| `experiments.image` (string) | `images.kubectl` / `experiments.installer.image` |

Behavioral changes to review:

- **No web portal.** NOTES, Helm tests, and GameDay now validate the operator/CRDs, not a frontend/MongoDB. Install ChaosCenter separately for a UI.
- **`pdb.enabled` now requires `pdb.selector`** (the chart ships no stateful workload to protect by default).
- **`kyvernoPolicy.excludeContainers` was removed** — use `kyvernoPolicy.excludeSelector` (a real pod selector).
- **NetworkPolicy adds Kubernetes API-server egress** — required by the operator; pin `apiServer.ipBlock` on strict clusters.
