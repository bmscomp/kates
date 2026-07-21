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
| `dashboards.enabled` | `true` | Install Kates Grafana dashboards via sidecar labels |

Own keys are validated by `values.schema.json`; subchart keys are validated by kube-prometheus-stack itself.
