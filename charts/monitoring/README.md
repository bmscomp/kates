# kates-monitoring

Wraps [kube-prometheus-stack](https://github.com/prometheus-community/helm-charts/tree/main/charts/kube-prometheus-stack) with Kates-specific Grafana dashboards, chaos alerting rules, and NetworkPolicy exceptions for scraping Strimzi Kafka pods.

## Install

```bash
helm dependency build charts/monitoring
helm install monitoring charts/monitoring -n monitoring --create-namespace
helm test monitoring -n monitoring
```

Grafana is exposed as a NodePort on `30080` by default (dev convenience).

> **Change the Grafana admin password.** The default is `admin` — override
> `kube-prometheus-stack.grafana.adminPassword` (or use
> `grafana.admin.existingSecret`) before any shared deployment. NOTES.txt
> warns on install while the default is active.

## Key values

| Key | Default | Description |
|---|---|---|
| `kube-prometheus-stack.enabled` | `true` | Deploy the wrapped stack |
| `kube-prometheus-stack.*` | see values | Full pass-through to the subchart (Grafana, Prometheus, Alertmanager, exporters) |
| `chaosAlerts.enabled` | `false` | PrometheusRules for LitmusChaos experiment alerts |
| `kafkaNamespace` | `kafka` | Namespace used in Kafka PromQL expressions |
| `networkPolicy.prometheusKafkaEgress.enabled` | `true` | Allow Prometheus egress to Kafka metrics ports (for default-deny clusters) |
| `networkPolicy.prometheusKafkaEgress.ports` | `[9404]` | Scrapable ports on Kafka pods |
| `dashboards.enabled` | `true` | Install this chart's Grafana dashboards via sidecar labels |
| `dashboards.namespace` | `""` | ConfigMap namespace (empty = the release namespace) |
| `dashboards.label` / `labelValue` | `grafana_dashboard` / `"1"` | The label the Grafana sidecar watches |
| `dashboards.folder` | `""` | `grafana_folder` annotation; empty leaves the boards in the sidecar's default folder |
| `dashboards.labels` | `{}` | Extra labels, **merged with** `label`/`labelValue` |
| `kube-prometheus-stack.grafana.sidecar.dashboards.searchNamespace` | `ALL` | Which namespaces the sidecar watches for boards |

Own keys are validated by `values.schema.json`; subchart keys are validated by kube-prometheus-stack itself.

## Dashboards

`dashboards/*.json` becomes one ConfigMap the Grafana sidecar discovers —
**every board the repository builds**, twelve today. Since chart 1.5.0 this
is the single delivery route: kates (0.9.0), kates-chaos (2.2.0),
connect-cluster (2.1.0) and mirror-maker2 (0.11.0) no longer render their own
copies, and the boards' template variables (`$job`, `$namespace`/`$cluster`)
do the per-release scoping their rewrites used to.

| Board | Source | What it is for |
|---|---|---|
| **Kafka — KRaft Operations** | generated from [`dashboards/kafka-kraft/`](../../dashboards/kafka-kraft/) | Is the controller quorum healthy, is metadata committing and reaching every broker, and what is failing to replicate, serve or store the data underneath |
| **Kafka — Performance & Load Testing** | generated from [`dashboards/kafka-performance/`](../../dashboards/kafka-performance/) | What the cluster does under load, down to the partition, with a `$topic` selector for the topic a run is writing to |
| **Kates — Application Health**, **Benchmark (live)**, **Trend & Regression**, **Chaos** | generated from [`dashboards/kates-application/`](../../dashboards/kates-application/), [`kates-benchmark/`](../../dashboards/kates-benchmark/), [`kates-trend/`](../../dashboards/kates-trend/) and [`kates-chaos/`](../../dashboards/kates-chaos/) | The Kates test harness |
| **KATES — Overview**, **Kyverno Security Policies** | generated from [`dashboards/kates-overview/`](../../dashboards/kates-overview/) and [`kyverno-security/`](../../dashboards/kyverno-security/) | One Kates release about itself; the Kyverno admission plane |
| **Kates — Chaos infrastructure** | generated from [`dashboards/kates-chaos-infra/`](../../dashboards/kates-chaos-infra/) | Is the LitmusChaos execution plane installed and running at all |
| **Kafka Connect** | generated from [`dashboards/kafka-connect/`](../../dashboards/kafka-connect/) | One Connect group: workers, connectors, throughput, failures, the DLQ |
| **Kafka MirrorMaker 2**, **— migration** | generated from [`dashboards/mirror-maker2/`](../../dashboards/mirror-maker2/) and [`mirror-maker2-migration/`](../../dashboards/mirror-maker2-migration/) | A mirror that runs for months; the hours a migration runs |

All twelve are **generated**. Edit `dashboards/<board>/board.py` in
the repository root and run `scripts/gen-dashboards.py`, which rewrites the
copy here; `scripts/gen-dashboards.py --check` fails CI when a copy has
drifted, so editing the JSON in this chart does not survive. Every series the
Kafka, Connect and MirrorMaker 2 boards read is proved producible by
`scripts/check-metric-contract.sh` against the chart whose exporter has to
produce it (`kafka-cluster`, `connect-cluster`, `mirror-maker2`). All twelve
together are ~620 KiB of a ConfigMap's 1 MiB ceiling — the largest single
board is ~135 KiB, so there is room, but a new board keeps an eye on it.

### The sidecar watches every namespace

`kube-prometheus-stack.grafana.sidecar.dashboards.searchNamespace` is set to
`ALL` **here**, rather than inherited from the subchart. This chart's own
ConfigMap sits beside Grafana, but `dashboards.namespace` can point it
elsewhere, and Route D's bundle ConfigMaps land wherever `-n` says — a
sidecar watching only its own namespace picks none of those up, and the
failure is silent: the boards are generated, synced, labelled and invisible.

kube-prometheus-stack has defaulted this to `ALL` since 46.x, so the pinned
82.4.3 already behaved; pinning it here is what stops a version bump or a
partial override taking it away. `ALL` needs the cluster-scoped ClusterRole the
Grafana subchart creates while `grafana.rbac.namespaced` is false, which is its
default and this chart's. Where that is not acceptable, replace it with the
list of namespaces that hold boards — and remember that a namespace missing
from the list is a board silently not picked up, which is the same failure by
another route. The value in `values.yaml` carries the whole trade-off.

[`dashboards/README.md`](../../dashboards/README.md#installing-these-anywhere)
has the three install routes that do not involve this chart at all: the
Grafana API installer, a file-provisioning bundle, and a plain ConfigMap
bundle for a cluster that runs the sidecar but not these charts.

Broker health, KRaft quorum identity, Cruise Control and consumer-group lag
are covered by the **Strimzi operator's own** dashboards, which
`charts/strimzi-operator` enables by default. Kafka Connect and MirrorMaker 2
ship their boards with `charts/connect-cluster` and `charts/mirror-maker2`,
`charts/kates` ships **Kates — Overview** and **Kyverno Security Policies**,
and `charts/kates-chaos` ships **Kates — Chaos infrastructure** — six boards
here, twelve in [`dashboards/`](../../dashboards/README.md) altogether.

### Removed in 1.3.0

Nine deprecated boards — `kafka-dashboard`, `kafka-comprehensive`,
`kafka-jvm`, `kafka-all-metrics`, `kafka-working`, `kafka-perf-test`,
`kafka-performance`, `kafka-perf-global` and `strimzi-operator-dashboard` —
and the `legacyKafkaDashboards.enabled` value that gated them. Eight were
subsets of the Strimzi operator's boards or of each other; `kafka-perf-global`
survives, rebuilt on producible metric names, as **Kafka — Performance & Load
Testing**. Setting `legacyKafkaDashboards.enabled` now does nothing and NOTES
says so on install. `docs/grafana-dashboards-refactor-plan.md` is the account.
