# Chapter 10: CLI Reference

Complete reference for the Kates CLI — every command, flag, and output format.

## Installation

```bash
# Build and install locally
make cli-install

# Or build for cross-platform distribution
make cli-build
# Binaries in cli/dist/ for macOS (amd64/arm64) and Linux (amd64/arm64)
```

## Configuration

Kates CLI uses a config file at `~/.kates.yaml` for managing server contexts.

### Context Management

```bash
# Set a context
kates ctx set local --url http://localhost:30083

# Use a context
kates ctx use local

# List contexts
kates ctx list

# Override context for a single call
kates --url http://other-server:8080 health
kates --context staging test list
```

### Config File Format

```yaml
current-context: local
contexts:
  local:
    url: http://localhost:30083
    output: table
  staging:
    url: https://kates-staging.example.com
    output: table
```

## Global Flags

| Flag | Short | Description |
|------|-------|-------------|
| `--url` | | Override API URL for this call |
| `--output` | `-o` | Output format: `table` or `json` |
| `--context` | | Use a specific context |
| `--help` | `-h` | Show help |

## Commands

### health

Check system health, Kafka connectivity, and engine status.

```bash
kates health
```

### status

Quick one-line system status.

```bash
kates status
```

### version

Show CLI, API, and runtime version information.

```bash
kates version
```

---

### cluster

Kafka cluster metadata and inspection.

```bash
# Cluster overview
kates cluster

# List topics
kates cluster topics

# Topic detail with partition layout
kates cluster topic <topic-name>

# Consumer groups
kates cluster groups

# Consumer group detail with lag
kates cluster group <group-name>

# Broker configuration
kates cluster brokers

# Full cluster topology (26 sections)
kates cluster topology

# Critical Kafka health alerts
kates cluster alerts
```

#### cluster check

Run a comprehensive Kafka cluster health check. Reports broker count, controller identity, topic/partition counts, consumer groups, and partition health (under-replicated, offline). Problems are displayed inline.

```bash
kates cluster check
kates cluster check -o json
```

Output statuses: `● HEALTHY`, `▲ WARNING`, `✖ CRITICAL`.

#### cluster topology

Display the full Strimzi/Kafka cluster topology with 26 data sections. Requires the Kates backend to be deployed on Kubernetes with access to Strimzi CRDs and Kafka AdminClient APIs.

```bash
kates cluster topology
kates cluster topology -o json
```

| # | Section | Source |
|---|---------|--------|
| 1 | Kubernetes Platform | K8s API |
| 2 | Strimzi Operator | Deployment |
| 3 | Kafka Cluster | CR + AdminClient |
| 4 | Kafka Config | CR |
| 5 | Node Pools | CRD |
| 6 | Controllers | AdminClient + Pods |
| 7 | Brokers | AdminClient + Pods |
| 8 | Entity Operator | CR |
| 9 | Cruise Control | CR |
| 10 | Kafka Exporter | CR |
| 11 | TLS Certificates | CR |
| 12 | Metrics & Monitoring | CR + PodMonitors |
| 13 | Managed Topics | CRD |
| 14 | Kafka Users | CRD |
| 15 | Consumer Groups | AdminClient |
| 16 | ACLs | AdminClient |
| 17 | Log Directories | AdminClient |
| 18 | Feature Flags | AdminClient |
| 19 | Kafka Rebalances | CRD |
| 20 | Strimzi Drain Cleaner | Deployment |
| 21 | Strimzi Pod Sets | CRD |
| 22 | Network Policies | K8s API |
| 23 | PVCs | K8s API |
| 24 | Services | K8s API |
| 25 | Endpoints | K8s API |
| 26 | Connect / MirrorMaker2 | CRD |

#### cluster alerts

Show critical Kafka health alerts from PrometheusRule CRDs. Displays 16 alert rules across 8 groups that can affect cluster health. Alerts are sorted by severity (critical first) with styled indicators.

Returns **exit code 2** when critical alerts are configured — useful for CI/CD health gates.

```bash
# Show all alerts
kates cluster alerts

# Filter by severity
kates cluster alerts --severity critical
kates cluster alerts --severity warning

# Filter by alert group
kates cluster alerts --group kafka.kraft
kates cluster alerts --group kafka.cluster

# JSON output for scripting
kates cluster alerts -o json

# CI/CD health gate
kates cluster alerts --severity critical && echo "safe"
```

| Flag | Description |
|------|-------------|
| `--severity` | Filter by severity: `critical` or `warning` |
| `--group` | Filter by alert group (e.g. `kafka.cluster`, `kafka.kraft`, `kafka.certificates`) |

Alert groups: `kafka.cluster`, `kafka.consumer`, `kafka.kraft`, `kafka.network`, `strimzi.operator`, `kafka.replication`, `kafka.performance`, `kafka.cruisecontrol`, `kafka.certificates`.

#### cluster watch

Live-refreshing cluster health dashboard with sparkline trends. The display auto-refreshes and tracks the last 30 polls for under-replicated partitions, offline partitions, and partition count trends.

```bash
# Default 5-second refresh
kates cluster watch

# Custom interval
kates cluster watch --interval 10
```

| Flag | Default | Description |
|------|---------|-------------|
| `--interval` | 5 | Refresh interval in seconds |

---

### test

Manage performance test runs.

#### test list

```bash
kates test list
kates test list --type LOAD --status DONE
kates test list --page 0 --size 20
```

| Flag | Description |
|------|-------------|
| `--type` | Filter by test type: LOAD, STRESS, SPIKE, ENDURANCE, VOLUME, CAPACITY, ROUND_TRIP, INTEGRITY |
| `--status` | Filter by status: PENDING, RUNNING, DONE, FAILED |
| `--page` | Page number (0-indexed) |
| `--size` | Page size |

#### test create

```bash
kates test create --type LOAD --records 100000
kates test create --type STRESS --producers 8 --duration 300 --wait
kates test create --type INTEGRITY --records 50000 --acks all --wait
```

| Flag | Description |
|------|-------------|
| `--type` | Test type (required) |
| `--records` | Number of records |
| `--record-size` | Record payload size in bytes |
| `--producers` | Number of producer threads |
| `--consumers` | Number of consumer threads |
| `--consumer-group` | Consumer group name |
| `--acks` | Producer acks mode: `0`, `1`, `all` |
| `--topic` | Target topic name |
| `--partitions` | Topic partition count |
| `--replication-factor` | Topic replication factor |
| `--min-isr` | Minimum in-sync replicas |
| `--duration` | Test duration in seconds |
| `--throughput` | Target throughput (rec/s), -1 for unlimited |
| `--fetch-min-bytes` | Consumer fetch minimum bytes |
| `--fetch-max-wait-ms` | Consumer fetch maximum wait |
| `--backend` | Backend engine to use |
| `--wait` | Wait for test completion |

#### test get

Aliases: `show`, `inspect`

```bash
kates test get <id>
kates test show <id>
kates test inspect <id>
```

Shows detailed test results including phases, metrics, integrity data, and timeline events.

#### test delete

Aliases: `rm`

```bash
kates test delete <id>
kates test rm <id>
```

#### test watch

```bash
kates test watch <id>
```

Live-stream test progress to the terminal.

#### test apply

```bash
kates test apply -f scenario.yaml
kates test apply -f scenario.yaml --wait
```

Apply a YAML scenario file. Supports multi-phase tests with SLA definitions.

#### test scaffold

```bash
kates test scaffold --type LOAD
kates test scaffold --type STRESS -o stress-test.yaml
kates test scaffold --type INTEGRITY_CHAOS -o chaos-integrity.yaml
```

Generate a YAML scaffold template for any test type.

| Type | Description |
|------|-------------|
| `LOAD` | Standard load test scenario |
| `STRESS` | Multi-phase ramp-up stress test |
| `SPIKE` | Three-phase spike simulation |
| `ENDURANCE` | Long-running soak test |
| `VOLUME` | Large message volume test |
| `CAPACITY` | Progressive capacity discovery |
| `ROUND_TRIP` | End-to-end latency measurement |
| `INTEGRITY` | Data integrity verification |
| `INTEGRITY_CHAOS` | Integrity + chaos injection |

---

### report

View and export test reports.

#### report show

```bash
kates report show <id>
```

Display the full report for a test run.

#### report summary

```bash
kates report summary <id>
```

Condensed summary of key metrics.

#### report export

```bash
kates report export <id> --format json
kates report export <id> --format csv
kates report export <id> --format junit -o results.xml
kates report export <id> --format heatmap -o heatmap.json
kates report export <id> --format heatmap-csv -o heatmap.csv
```

| Format | Description |
|--------|-------------|
| `json` | Full report as JSON |
| `csv` | Metrics as CSV spreadsheet |
| `junit` | JUnit XML for CI/CD |
| `heatmap` | Latency heatmap as JSON |
| `heatmap-csv` | Latency heatmap as CSV |

#### report diff

```bash
kates report diff <id1> <id2>
```

Side-by-side comparison of two test runs.

#### report compare

```bash
kates report compare <id1>,<id2>,<id3>
```

Summary comparison across multiple runs.

#### report brokers

```bash
kates report brokers <id>
```

Per-broker metrics for a test run.

---

### trend

Historical performance trend analysis.

```bash
kates trend --type LOAD --metric p99LatencyMs --days 30
kates trend --type LOAD --metric throughputRecordsPerSec --days 7
```

| Flag | Description |
|------|-------------|
| `--type` | Test type to analyze |
| `--metric` | Metric name: `p99LatencyMs`, `avgLatencyMs`, `throughputRecordsPerSec` |
| `--days` | Lookback period in days |

---

### disruption

Kubernetes-aware disruption testing.

#### disruption run

```bash
kates disruption run --config plan.json
kates disruption run --config plan.json --dry-run
kates disruption run --config plan.json --fail-on-sla-breach --output-junit results.xml
```

| Flag | Description |
|------|-------------|
| `--config` | Path to disruption plan JSON file (required) |
| `--dry-run` | Validate plan without executing |
| `--fail-on-sla-breach` | Exit with non-zero if SLA is breached |
| `--output-junit` | Write JUnit XML to file |

#### disruption list

```bash
kates disruption list
```

List recent disruption test reports.

#### disruption status

```bash
kates disruption status <id>
```

Show detailed disruption report with step-by-step results.

#### disruption timeline

```bash
kates disruption timeline <id>
```

Show pod event timeline for a disruption test.

#### disruption types

```bash
kates disruption types
```

List all available disruption types.

#### disruption kafka-metrics

```bash
kates disruption kafka-metrics <id>
```

Show Kafka intelligence data: ISR tracking, consumer lag, leader targeting.

#### disruption watch

```bash
kates disruption watch <id>
```

Real-time SSE progress stream for disruption tests.

---

### resilience

Combined performance + chaos testing.

```bash
kates resilience run --config resilience-test.json
```

---

### schedule

Aliases: `s`, `sched`

Automated recurring test schedules.

#### schedule list

Aliases: `ls`

```bash
kates schedule list
```

Shows all schedules with ID, name, cron expression, enabled state, and last run ID.

#### schedule get

```bash
kates schedule get <id>
```

Shows detailed schedule info: name, cron expression, enabled state, last run ID, last run time, and creation time.

#### schedule create

```bash
kates schedule create --name "Hourly Load Test" --cron "0 * * * *" --request request.json
kates schedule create --name "Nightly Endurance" --cron "0 2 * * *" --request endurance.json
```

| Flag | Required | Description |
|------|:---:|-------------|
| `--name` | ✅ | Human-readable schedule name |
| `--cron` | ✅ | Cron expression (e.g., `0 * * * *`) |
| `--request` | ✅ | Path to JSON file containing the test request body |

The request file should contain the same JSON body you would send to `POST /api/tests`.

#### schedule delete

Aliases: `rm`

```bash
kates schedule delete <id>
```

---

### dashboard

Full-screen monitoring dashboard.

```bash
kates dashboard
kates dash
```

### top

Live view of running tests.

```bash
kates top
```

---

### lab

Interactive performance tuning workbench. Opens a full-screen TUI for iterative parameter tuning with live results, sparklines, A/B comparison, auto-sweep, and CSV export.

```bash
kates lab
```

Key features: parameter presets (`p`), auto-sweep (`s`), iteration diff (`d`), pin-and-compare (`c`), export (`e`), session save/load (`w`/`L`), cancel running test (`x`), retry on failure (`r`).

See [Chapter 10b: Lab](10b-lab.md) for the full guide.

---

## Output Modes

All commands support two output modes:

```bash
# Table output (default) — human-readable with colors
kates test list -o table

# JSON output — structured, machine-readable
kates test list -o json
```

## Shell Completion

```bash
# Bash
kates completion bash > /etc/bash_completion.d/kates

# Zsh
kates completion zsh > "${fpath[1]}/_kates"

# Fish
kates completion fish > ~/.config/fish/completions/kates.fish
```
