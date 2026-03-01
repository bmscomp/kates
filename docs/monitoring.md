# Monitoring

This document covers the monitoring infrastructure: how it is deployed, what dashboards are available, and how metrics flow from Kafka and Kates into Grafana.

## Stack

| Component | Version | Source |
|---|---|---|
| Prometheus | Managed by kube-prometheus-stack | Remote Helm chart (`prometheus-community`) |
| Grafana | 12.3.1 | Bundled with kube-prometheus-stack |
| kube-prometheus-stack | `PROMETHEUS_STACK_VERSION` in `versions.env` | `prometheus-community/kube-prometheus-stack` |

The monitoring stack is installed from the **remote** `prometheus-community` Helm repository — no local chart is stored in this repo.

## Deploy

```bash
make monitoring
```

This runs `scripts/deploy-monitoring.sh`, which:

1. Adds the `prometheus-community` Helm repo
2. Installs `kube-prometheus-stack` with values from `config/monitoring.yaml`
3. Creates a ConfigMap with all Kates Grafana dashboards
4. Labels the ConfigMap with `grafana_dashboard=1` so the Grafana sidecar auto-discovers it

### Access

| Service | URL |
|---|---|
| Grafana | `http://localhost:30080` (NodePort) |
| Prometheus | `http://localhost:9090` (port-forward) |

Default Grafana credentials: `admin` / `admin`.

## Dashboards

Four custom dashboards are deployed as JSON files in `config/monitoring/`:

### Kates Benchmark & Phase

**File:** `kates-benchmark-dashboard.json`
**UID:** `kates-benchmark-overview`

Real-time view of active benchmark runs with drill-down by run and phase.

| Row | Panels |
|---|---|
| Benchmark Status | Active runs, total records, total errors, SLA violations |
| Throughput | Records/sec and MB/sec timeseries |
| Latency | Percentiles (p50/p95/p99/p99.9) and max latency |
| Phase Detail | Throughput by phase, latency by phase (p99), records by phase |
| SLA & Errors | Error rate and SLA violations by metric/severity |

**Template variables:** `$run_id`, `$test_type`

### Kates Trend & Platform

**File:** `kates-trend-dashboard.json`
**UID:** `kates-trend-analysis`

Historical view comparing performance across runs, plus platform-level aggregate metrics.

| Row | Panels |
|---|---|
| Throughput Trend | Peak throughput across runs |
| Latency Trend | p99 and p99.9 latency trend |
| Regression Detection | Total records per run |
| Platform Stats | Tests completed (by outcome), test duration (p50/p95/p99), SLA pass/fail rate, records processed rate |
| Disruptions | Disruption completion rate, disruption duration (p50/p95) |

**Template variable:** `$test_type`

### Kates Application Health

**File:** `kates-application-dashboard.json`
**UID:** `kates-application-health`

Operational health of the Kates application itself (Quarkus runtime).

| Row | Panels |
|---|---|
| Pod Status | Pods ready, restart count, uptime, Postgres ready |
| HTTP Server | Request rate (by method), error rate (4xx/5xx), request latency (p50/p95/p99) |
| JVM | Heap memory (used/committed/max), GC pause duration, thread count (live/daemon/peak) |
| Database | Agroal pool connections (active/available/max used), DB acquire time |
| Resource Usage | CPU usage (per pod), memory RSS and working set |

### Kafka Chaos Dashboard

**File:** `grafana-chaos-dashboard.json`
**UID:** `kafka-chaos-dashboard`

Correlates LitmusChaos experiments with Kafka cluster health and Kates benchmark performance.

| Row | Panels |
|---|---|
| Chaos Experiment Status | Active engines, passed/failed experiments, probe success rate |
| Kafka Health During Chaos | Broker pod status, restarts, CPU usage, memory usage |
| Chaos Experiment History | Experiment duration over time |
| RTO / RPO / Data Integrity | Producer RTO, consumer RTO, data loss %, RPO, E2E latency, producer throughput |
| Kates During Chaos | Benchmark throughput overlay, p99 latency overlay, error rate during chaos |

## Metrics Reference

### BenchmarkMetrics (per-run, real-time)

Registered by `BenchmarkMetrics.java` — labeled with `run_id`, `test_type`, and `phase`:

| Prometheus Metric | Type | Description |
|---|---|---|
| `kates_benchmark_active_runs` | Gauge | Number of active benchmark runs |
| `kates_benchmark_throughput_rec_sec` | Gauge | Current throughput in records/sec |
| `kates_benchmark_throughput_mb_sec` | Gauge | Current throughput in MB/sec |
| `kates_benchmark_latency_ms` | Summary | Request latency distribution (p50/p95/p99/p99.9) |
| `kates_benchmark_records_total` | Counter | Total records processed |
| `kates_benchmark_errors_total` | Counter | Total errors |
| `kates_benchmark_sla_violations` | Counter | SLA violation events |

### KatesMetrics (platform-level, cumulative)

Registered by `KatesMetrics.java` — persistent across benchmark runs:

| Prometheus Metric | Type | Description |
|---|---|---|
| `kates_tests_completed_total` | Counter | Total tests completed (by test_type, outcome) |
| `kates_tests_duration_seconds` | Timer | Test execution duration (p50/p95/p99) |
| `kates_tests_throughput_rec_sec` | Summary | Final throughput per completed test (records/sec) |
| `kates_tests_throughput_mb_sec` | Summary | Final throughput per completed test (MB/sec) |
| `kates_sla_evaluations_total` | Counter | SLA evaluation outcomes (pass/fail) |
| `kates_disruptions_completed_total` | Counter | Disruption executions completed (by type, outcome) |
| `kates_disruptions_duration_seconds` | Timer | Disruption execution duration (p50/p95) |
| `kates_records_processed_total` | Counter | Cumulative records processed across all tests |

## Configuration

The monitoring values file is `config/monitoring.yaml`. Key settings:

| Setting | Value | Notes |
|---|---|---|
| `grafana.adminPassword` | `admin` | Change in production |
| `grafana.service.type` | `NodePort` | Port `30080` |
| `grafana.sidecar.dashboards.enabled` | `true` | Auto-discovers ConfigMaps labeled `grafana_dashboard=1` |
| `grafana.image.tag` | `12.3.1` | Pinned for stability |
| Image pull policy | `IfNotPresent` | All components |

### Upgrading

To upgrade the monitoring stack, update `PROMETHEUS_STACK_VERSION` in `versions.env` and re-run:

```bash
make monitoring
```

To check available versions:

```bash
helm search repo prometheus-community/kube-prometheus-stack --versions | head -10
```
