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

Own keys are validated by `values.schema.json`; subchart keys are validated by kube-prometheus-stack itself.

## Dashboards

`dashboards/*.json` becomes one ConfigMap the Grafana sidecar discovers.

| Board | Source | What it is for |
|---|---|---|
| **Kafka — KRaft Operations** | generated from [`dashboards/kafka-kraft/`](../../dashboards/kafka-kraft/) | Is the controller quorum healthy, is metadata committing and reaching every broker, and what is failing to replicate, serve or store the data underneath |
| **Kafka — Performance & Load Testing** | generated from [`dashboards/kafka-performance/`](../../dashboards/kafka-performance/) | What the cluster does under load, down to the partition, with a `$topic` selector for the topic a run is writing to |
| **Kates — Application Health**, **Benchmark (live)**, **Trend & Regression**, **Chaos** | generated from [`dashboards/kates-application/`](../../dashboards/kates-application/), [`kates-benchmark/`](../../dashboards/kates-benchmark/), [`kates-trend/`](../../dashboards/kates-trend/) and [`kates-chaos/`](../../dashboards/kates-chaos/) | The Kates test harness |

All six are **generated**. Edit `dashboards/<board>/board.py` in
the repository root and run `scripts/gen-dashboards.py`, which rewrites the
copy here; `scripts/gen-dashboards.py --check` fails CI when a copy has
drifted, so editing the JSON in this chart does not survive. Every series the
two Kafka boards read is proved producible by
`scripts/check-metric-contract.sh kafka-cluster`.

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
