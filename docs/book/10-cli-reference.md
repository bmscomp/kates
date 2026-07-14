# CLI Reference

Reference for the Kates CLI — the commands, flags, and output formats you'll use day to day.

## Installation

```bash
# Build and install locally
make cli-install

# Or build for cross-platform distribution
make cli-build
# Binaries in cli/dist/ for macOS (amd64/arm64) and Linux (amd64/arm64)
```

::: {.callout-note}
**macOS:** `make cli-install` automatically strips `com.apple.provenance` / `com.apple.quarantine` extended attributes and ad-hoc codesigns the binary. If you install manually (e.g. `cp` instead of `make`), the kernel may SIGKILL the binary. Fix with:
```bash
sudo xattr -dr com.apple.provenance /usr/local/bin/kates
sudo xattr -dr com.apple.quarantine /usr/local/bin/kates
sudo codesign -f -s - /usr/local/bin/kates
```
:::


## Common Workflows

Before diving into individual commands, here are the workflows you'll use most often. Each one chains multiple commands into a real-world task — they're the reason the CLI exists as a unified tool rather than a collection of scripts.

### Workflow 1: Performance Regression Check

Before upgrading Kafka to a new version, you want to know if the new version regresses performance. The idea is simple: capture a baseline on the current version, perform the upgrade, run the same test again, and diff the results. If P99 latency or throughput moves outside your tolerance, you have a data-backed reason to investigate before the upgrade reaches production.

```bash
# 1. Verify the cluster is healthy before starting
kates health

# 2. Run a baseline load test on the current Kafka version
kates test create --type LOAD --records 100000 --wait
# → note the test ID, e.g. t-a1b2c3

# 3. Perform the Kafka upgrade (outside of Kates)

# 4. Run the same load test on the new version
kates test create --type LOAD --records 100000 --wait
# → note the new test ID, e.g. t-d4e5f6

# 5. Compare the two runs side by side
kates report diff t-a1b2c3 t-d4e5f6
```

### Workflow 2: Investigating Consumer Lag

A consumer group's lag is climbing and you need to diagnose why. Is it a slow consumer? An overloaded broker? A hot partition? This workflow narrows the problem from "lag is high" to a specific root cause in under two minutes.

```bash
# 1. Find which consumer groups are lagging
kates kafka groups

# 2. Drill into the lagging group for per-partition detail
kates kafka group my-lagging-group

# 3. Check broker health — is one broker overloaded?
kates cluster watch

# 4. If a specific broker looks hot, check its load distribution
kates report brokers <latest-test-id>
```

### Workflow 3: Chaos Resilience Validation

You want to prove your cluster can survive a broker failure — not just "it stays up" but "it recovers within your SLA window and doesn't lose data." This workflow runs a chaos test while monitoring live throughput, then examines the recovery timeline.

```bash
# 1. Run a chaos test that kills a broker during a load test
kates disruption run --config broker-kill.json

# 2. In another terminal, watch live throughput during the test
kates top

# 3. After the test completes, check results and recovery time
kates disruption status <id>

# 4. Export the latency heatmap for detailed post-mortem analysis
kates report export <test-id> --format heatmap > heatmap.json
```

### Workflow 4: Pre-Production Cluster Validation

You've just deployed a new Kafka cluster and want to validate it end-to-end before handing it to application teams. This workflow runs progressively deeper checks: first a quick health check, then a deep diagnostic, then a topology audit, and finally a sustained endurance run to shake out issues that only appear under sustained load.

```bash
# 1. Quick system health check
kates health

# 2. Deep diagnostic — checks Kubernetes, Strimzi, connectivity, and more
kates doctor

# 3. Verify the broker/controller layout and zone distribution
kates cluster topology

# 4. Run a 30-minute endurance test to stress-test under sustained load
kates test create --type ENDURANCE --duration 1800 --wait

# 5. Check the endurance results against historical baselines
kates trend --type ENDURANCE --metric p99LatencyMs --days 30
```

### Workflow 5: CI/CD Quality Gate

You want every pull request to prove it doesn't regress Kafka performance. This workflow integrates into your CI pipeline — it runs a scenario file, exports JUnit results, and runs a quality-gate test that exits non-zero if the grade drops below your threshold.

```bash
# 1. Run the scenario defined in your repo
kates test apply -f ci/load-test.yaml --wait

# 2. Export results as JUnit XML for your CI system
kates report export <id> --format junit > results.xml

# 3. Gate: run a gate test and fail the build if the grade is below B
kates gate --min-grade B --type LOAD --records 100000
```

::: {.callout-tip}
See [CI/CD Pipeline](appendix-c-cicd.md) for complete GitHub Actions, GitLab CI, and Jenkins pipeline examples.
:::


## Configuration

Kates CLI uses a config file at `~/.kates.yaml` for managing server contexts.

### Proxy Configuration

The Kates CLI fully supports HTTP proxies. You can either use standard proxy environment variables or configure it persistently per context.

**Option 1: Context Proxy (Recommended)**
```bash
# Configure a specific proxy for a single environment context
kates ctx set prod --url https://kates.company.com --proxy http://proxy-server.internal:8080

# If the proxy uses self-signed SSL interception, bypass certificate validation:
kates ctx set prod --url https://kates.company.com --proxy http://proxy-server.internal:8080 --insecure
```

**Option 2: Global Environment Variables**
The CLI natively respects standard OS proxy variables:
```bash
export HTTP_PROXY="http://proxy-server.internal:8080"
export HTTPS_PROXY="http://proxy-server.internal:8080"
export NO_PROXY="localhost,127.0.0.1"
```

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

### Health, Status & Diagnostics

These are the commands you reach for first. Whether you're starting your day, triaging an incident, or validating a deployment, health and status commands give you a quick read on whether the system is behaving. Run `kates health` before and after any significant change — it's cheap and tells you immediately if something broke.

#### health

Check system health, Kafka connectivity, and engine status.

```bash
kates health
```

Expected output:

```
 Kates Health Dashboard — System Status: UP

  Engine
  Active Backend:   native
  Available:        [native trogdor]

  Kafka Cluster
  Status:           ● UP
  Bootstrap:        krafter-kafka-bootstrap.kafka.svc:9092

  Performance Tests
  Test        Records   Partitions   Producers   Acks   Compress
  ─────────────────────────────────────────────────────────────
  load        100000    3            2           all    lz4
  stress      5000000   6            16          1      lz4
  ...
```

#### status

Quick one-line system status — useful for scripting and prompts.

```bash
kates status
```

Expected output:

```
  ✓ local │ UP │ Kafka ✓ │ 12 configs │ 0 running │ 8 done │ 0 failed
```

If the API is unreachable, the line shows the context name, its URL, and `unreachable` instead.

#### version

Show CLI and runtime version information (CLI version, commit, build date, Go runtime), plus API reachability and the active backend when the server is up.

```bash
kates version
```

#### doctor

Aliases: `preflight`, `check`

Pre-flight cluster readiness checklist. The doctor command verifies that the Kates API is reachable, Kafka is connected, the broker count meets the 3-broker minimum, ISR health is clean, topics are listable, and benchmark backends are available. It also checks whether Kyverno is installed with active policies and no workload violations (Kyverno checks warn rather than fail — it's optional but recommended). Failing checks come with remediation hints. It's the first command to run when something "feels wrong" but `kates health` reports healthy.

```bash
kates doctor
kates preflight
kates check
```

Expected output:

```
 Kates Doctor — Pre-flight cluster readiness

  Check                Status   Detail
  ─────────────────────────────────────────────────────────────
  API Reachable        PASS     Connected
  Kafka Connected      PASS     krafter-kafka-bootstrap.kafka.svc:9092
  Broker Count ≥ 3     PASS     3 brokers detected
  ISR Health           PASS     All replicas in sync
  Topics Available     PASS     42 topics found
  Benchmark Backends   PASS     [native trogdor]
  Kyverno Installed    PASS     CRD present
  Kyverno Ready        PASS     Admission controller running
  Kyverno Policies     PASS     6 policies active
  Kyverno Violations   PASS     No workload violations

  ✓ 10/10 checks passed — cluster is ready for testing!
```

**See also:** [Deployment Guide](12-deployment.md) for environment setup, [Troubleshooting Index](appendix-b-troubleshooting.md) for common diagnostic failures.

---

### Cluster Commands

Cluster commands give you direct visibility into the Kafka cluster without leaving the Kates CLI. Instead of switching between `kafka-topics.sh`, `kafka-consumer-groups.sh`, and `kubectl`, you can inspect topics, groups, brokers, and ACLs from a single interface. These commands query both the Kubernetes API and the Kafka AdminClient to give you a unified picture of cluster state.

#### cluster

Kafka cluster metadata and inspection.

```bash
# Cluster overview
kates cluster info

# List topics
kates cluster topics

# Topic detail with partition layout
kates cluster topics describe <topic-name>

# Consumer groups
kates cluster groups

# Consumer group detail with lag
kates cluster groups describe <group-id>

# Non-default configuration for a broker
kates cluster broker configs <broker-id>

# Full cluster topology
kates cluster topology

# Critical Kafka health alerts
kates cluster alerts
```

#### cluster info

Display cluster metadata including broker count, controller identity, cluster ID, and the broker list with rack/AZ placement. The controller broker is marked with ★ in the Role column.

```bash
kates cluster info
```

Expected output:

```
 Kafka Cluster — Cluster ID: dQw4w9WgXcQ

  Overview
  Broker Count:   3

  Controller
  Node ID:    0
  Host:       krafter-broker-0.kafka.svc
  Port:       9092
  Rack / AZ:  alpha

  Brokers (3)
  ID   Host                         Port   Rack / AZ   Role
  ──   ────                         ────   ─────────   ────
  0    krafter-broker-0.kafka.svc   9092   alpha       ★
  1    krafter-broker-1.kafka.svc   9092   sigma
  2    krafter-broker-2.kafka.svc   9092   gamma
```

#### cluster check

Run a comprehensive Kafka cluster health check. Reports broker count, controller identity, topic/partition counts, consumer groups, and partition health (under-replicated, offline). Problems are displayed inline.

```bash
kates cluster check
kates cluster check -o json
```

Output statuses: `● HEALTHY`, `▲ WARNING`, `✖ CRITICAL`.

#### cluster topology

Display the full Strimzi/Kafka cluster topology, section by section — from the Kubernetes platform down to individual PVCs and endpoints. Requires the Kates backend to be deployed on Kubernetes with access to Strimzi CRDs and Kafka AdminClient APIs. This is the most comprehensive view of your cluster — use it to verify broker/controller layout after deployment or to audit infrastructure before a load test.

```bash
kates cluster topology
kates cluster topology -o json
```

Expected output (abbreviated — full output includes all sections listed below):

```
 Kafka Cluster Topology — Cluster: krafter  │  Kafka 4.2.0  │  KRaft Mode

  Kubernetes Platform
  Version:   v1.31.4
  Platform:  linux/arm64
  Nodes:     3

  Strimzi Operator
  Version:     1.0.0
  Components:  ✓ Operator  ✓ Entity Operator  ✓ Cruise Control

  Kafka Cluster
  Cluster ID:  dQw4w9WgXcQ
  Namespace:   kafka
  Brokers:     3
  Status:      ✓ Ready

  Controllers (3)
  ...

  Brokers (3)
  ...

  (more sections: node pools, certificates, ACLs, PVCs, services, ...)
```

| Section | Source |
|---------|--------|
| Kubernetes Platform | K8s API |
| Strimzi Operator | Deployment |
| Kafka Cluster | CR + AdminClient |
| Kafka Broker Configuration | CR |
| Node Pools | CRD |
| Controllers | AdminClient + Pods |
| Brokers | AdminClient + Pods |
| Entity Operator | CR |
| Cruise Control | CR |
| Kafka Exporter | CR |
| TLS Certificates | CR |
| Metrics & Monitoring | CR + PodMonitors |
| Managed Topics | CRD |
| Kafka Users | CRD |
| Consumer Groups | AdminClient |
| Access Control Lists | AdminClient |
| Log Directories | AdminClient |
| Feature Flags | AdminClient |
| Kafka Rebalances | CRD |
| Strimzi Drain Cleaner | Deployment |
| Strimzi Pod Sets | CRD |
| Network Policies | K8s API |
| Persistent Volume Claims | K8s API |
| Services | K8s API |
| Endpoints | K8s API |
| Kafka Connect | CRD |
| MirrorMaker2 | CRD |

#### cluster alerts

Show critical Kafka health alerts from PrometheusRule CRDs — the alert rules the `kafka-cluster` chart installs, organized into the groups listed below. Alerts are sorted by severity (critical first) with styled indicators.

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

**See also:** [The Cluster Under Test](03-cluster.md) for cluster architecture, [Observability & Monitoring](09-observability.md) for Grafana dashboards.

---

### Test Commands

Test commands are the core of Kates. They let you create, monitor, and manage performance test runs against your Kafka cluster. Whether you're running a quick load test to sanity-check throughput or a multi-hour endurance test to catch memory leaks and log-roll spikes, the workflow is always the same: create a test, optionally watch it in real time, then inspect the results. For repeatable, version-controlled test definitions, use `test apply` with YAML scenario files instead of inline flags.

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

Browse and export the built-in library of ready-to-use YAML scenario templates.

```bash
kates test scaffold                        # list all templates
kates test scaffold --type LOAD           # filter by test type
kates test scaffold show production-load  # preview a template
kates test scaffold export ci-gate        # write ci-gate.yaml to current dir
kates test scaffold export ci-gate -o my-gate.yaml
kates test scaffold export --all           # export every template
```

| Template | Type | Description |
|----------|------|-------------|
| `quick-load` | LOAD | Quick smoke test — 50k records, 2 producers, p99 < 100ms gate |
| `production-load` | LOAD | Production-grade — 1M records, 8 producers, acks=all, lz4, strict SLA |
| `stress-test` | STRESS | High-throughput stress — 5M records, 16 producers, find breaking points |
| `endurance-soak` | ENDURANCE | 1-hour soak at 5k msg/s — detect GC pauses and log compaction issues |
| `exactly-once` | ROUND_TRIP | E2E integrity — idempotent + transactional, zero-loss, CRC verification |
| `integrity-tx` | INTEGRITY | Transactional integrity — 4 producers, zstd, CRC, zero-loss verification |
| `spike-test` | SPIKE | Burst traffic — 32 producers for 60s, test backpressure handling |
| `ci-gate` | LOAD | CI pipeline gate — fast 10k-record validation with strict zero-error SLA |

**See also:** [Test Types Deep Dive](05-test-types.md) for the theory behind each test type, [Scenario Files & SLA Gates](13-scenario-files.md) for YAML scenario syntax.

---

### Report Commands

After a test completes, reports are where the numbers become answers. Report commands let you view full results, export them for CI pipelines, diff two runs side by side, and drill into per-broker metrics to find hot spots. The `report diff` command is particularly powerful — it highlights exactly where two runs diverge, making it the go-to tool for before/after comparisons during upgrades, tuning, and regression checks.

#### report show

```bash
kates report show <id>
```

Display the full report for a test run.

Expected output:

```
 Performance Report — Test: a1b2c3

  Throughput
  Total Records:     100,000
  Avg Throughput:    3,086 rec/s
  Peak Throughput:   3,412 rec/s
  Avg MB/s:          3.02

  Latency Distribution
  Average  ▓▓░░░░░░░░░░░░░░░░░░    4.12 ms
  P50      ▓▓░░░░░░░░░░░░░░░░░░    3.00 ms
  P95      ▓▓▓░░░░░░░░░░░░░░░░░    8.00 ms
  P99      ▓▓▓▓▓░░░░░░░░░░░░░░░   22.00 ms
  Max      ▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓  186.00 ms

  Reliability
  Error Rate:   0.0000%

  SLA Verdict
  ✓ All SLA thresholds met

  Export: kates report export a1b2c3 --format csv
```

If SLA thresholds are violated, the verdict section instead lists each violation in a Metric / Threshold / Actual / Status table.

#### report summary

```bash
kates report summary <id>
```

Condensed summary of key metrics.

#### report export

```bash
kates report export <id> --format csv
kates report export <id> --format junit
kates report export <id> --format md
kates report export <id> --format heatmap > heatmap.json
kates report export <id> --format heatmap-csv > heatmap.csv
```

| Format | Description |
|--------|-------------|
| `csv` | Metrics as CSV spreadsheet |
| `junit` | JUnit XML for CI/CD |
| `md` | Markdown report |
| `html` | HTML report |
| `heatmap` | Latency heatmap as JSON |
| `heatmap-csv` | Latency heatmap as CSV |

When run in a terminal, the export is written to an auto-named file (e.g. `kates-report-<id>.csv`); when piped or redirected, it goes to stdout. For the full report as JSON, use the global output flag instead: `kates report show <id> -o json`.

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

**See also:** [Observability & Monitoring](09-observability.md) for heatmap interpretation and Grafana integration, [Performance Theory](04-performance-theory.md) for understanding percentile metrics.

---

### Trend Analysis

Trend analysis is how you move from "this test looks fine" to "performance has been stable for weeks." The trend command queries historical test results and renders sparkline charts showing how a metric has changed over time. It's essential for catching slow regressions that no single test run would reveal — a P99 that creeps from 15ms to 25ms over a month is invisible in individual reports but obvious in a trend chart.

#### trend

Historical performance trend analysis.

```bash
kates trend --type LOAD --metric p99LatencyMs --days 30
kates trend --type LOAD --metric avgThroughputRecPerSec --days 7
kates trend --type SPIKE --phase spike --metric avgThroughputRecPerSec
kates trend --type ENDURANCE --all-phases --metric p99LatencyMs
kates trend phases --type SPIKE --days 30
```

Expected output:

```
 Trend Analysis — LOAD · p99LatencyMs · 30d window

  Baseline:   20.10

  Trend Chart
  ▁▂▂▃▃  → stable  (5 data points)

  Min:       18.00
  Max:       22.00
  Average:   20.10

  Data Points
  Run ID     Timestamp             Value
  ─────────────────────────────────────────
  a1b2c3     2026-06-09T02:00:00Z  18.00
  d4e5f6     2026-06-16T02:00:00Z  19.50
  ...
```

Runs that deviate significantly from the baseline are listed in a separate "Regressions Detected" table with the deviation percentage.

| Flag | Default | Description |
|------|---------|-------------|
| `--type` | | Test type to analyze (required) |
| `--metric` | `avgThroughputRecPerSec` | Metric name: `avgThroughputRecPerSec`, `peakThroughputRecPerSec`, `avgThroughputMBPerSec`, `avgLatencyMs`, `p50LatencyMs`, `p95LatencyMs`, `p99LatencyMs`, `p999LatencyMs`, `maxLatencyMs`, `errorRate` |
| `--days` | 30 | Lookback period in days |
| `--baseline` | 5 | Number of recent runs used to compute the baseline |
| `--phase` | | Phase name to analyze (omit for overall) |
| `--all-phases` | false | Show trends for all phases side-by-side |
| `--broker` | | Broker ID to scope trend analysis |

Use `kates trend phases --type <TYPE>` to list the phase names available for a test type.

**See also:** [Performance Theory](04-performance-theory.md) for statistical significance and why single runs are insufficient.

---

### Disruption Commands

Disruption commands run controlled chaos experiments against your Kafka cluster. They inject real faults — broker kills, network partitions, disk pressure — while measuring the impact on throughput, latency, and data integrity. Every disruption follows a lifecycle: validate the plan, establish a steady-state baseline, inject the fault, observe recovery, and produce a verdict. The `--dry-run` flag lets you validate plans without actually breaking anything.

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

**See also:** [Chaos Engineering Theory](06-chaos-theory.md) for the principles behind chaos engineering, [Chaos Engineering in Practice](07-chaos-practice.md) for step-by-step chaos test walkthroughs.

---

### Chaos Experiment History

Chaos commands browse the history of past chaos experiment reports and their probe results — the record left behind by disruption and resilience runs.

Aliases: `cx`

#### chaos list

List recent chaos experiment reports with ID, plan name, status, SLA grade, and date.

```bash
kates chaos list
kates chaos list --limit 50
```

| Flag | Default | Description |
|------|---------|-------------|
| `--limit` | 20 | Maximum reports to display |

#### chaos show

Show a detailed chaos experiment report with per-probe breakdown.

```bash
kates chaos show <id>
```

---

### Resilience

Combined performance + chaos testing. Resilience tests run a load workload and inject disruptions simultaneously, then grade the cluster's ability to maintain SLA under fault conditions. This is the highest-level chaos primitive — it combines what you'd otherwise do manually with `test create` + `disruption run`.

```bash
kates resilience run -f resilience-test.yaml
kates resilience run -f resilience-test.json    # JSON also supported
kates resilience run -f resilience-test.yaml --dry-run
```

The config file has three parts: a `testRequest` (the same shape as a test creation request), a `chaosSpec` (experiment name, target namespace/label, duration, disruption type), and an optional `steadyStateSec` baseline period:

```yaml
testRequest:
  type: LOAD
  spec:
    numRecords: 100000
    numProducers: 2
    recordSize: 512

chaosSpec:
  experimentName: kafka-broker-pod-kill
  targetNamespace: kafka
  targetLabel: "strimzi.io/component-type=kafka"
  chaosDurationSec: 30
  disruptionType: POD_KILL

steadyStateSec: 30
```

**See also:** [Chaos Engineering in Practice](07-chaos-practice.md) for resilience test configuration.

---

### Schedule Commands

Schedule commands let you automate recurring test runs on a cron schedule. Instead of manually running load tests every night, you define a schedule once and Kates executes it automatically. Each run produces a full report, so you can combine schedules with `kates trend` to build continuous performance baselines over weeks or months.

Aliases: `s`, `sched`

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

**See also:** [Recipes & Patterns](14-recipes.md) for schedule-based regression detection patterns.

---

### Observability & Monitoring

Observability commands give you real-time and historical visibility into what Kates and Kafka are doing. The `dashboard` command opens a full-screen TUI with live metrics, `top` shows running tests like `kubectl top` shows pods, and `watch` streams a single test's progress. These are the commands you keep running in a side terminal during performance tests and chaos experiments.

#### dashboard

Full-screen monitoring dashboard.

```bash
kates dashboard
kates dash
```

#### top

Live view of running tests.

```bash
kates top
```

**See also:** [Observability & Monitoring](09-observability.md) for Grafana dashboards and Prometheus alert rules.

---

### Interactive Lab

The lab is an interactive performance tuning workbench. It opens a full-screen TUI where you can iterate on test parameters — tweak batch size, change acks mode, adjust partition count — and immediately see the impact on throughput and latency via live sparklines. It supports A/B comparison, auto-sweep across parameter ranges, and CSV export of all iterations.

```bash
kates lab
```

Key features: parameter presets (`p`), auto-sweep (`s`), iteration diff (`d`), pin-and-compare (`c`), export (`e`), session save/load (`w`/`L`), cancel running test (`x`), retry on failure (`r`).

See [Lab — Interactive Performance Tuning](10b-lab.md) for the full guide.

---

### Deployment & Lifecycle

Deployment commands manage the full lifecycle of the Kates stack — from initial deployment to teardown. The `deploy` command can set up the entire stack (Kafka, Kates backend, monitoring, chaos engine) with a single interactive wizard, while `clean` tears everything down cleanly, including finalizer stripping for Strimzi CRDs that can otherwise block namespace deletion.

#### deploy

Deploy the Kates stack (Kafka, Kates, Chaos, Schema Registry).

```bash
kates deploy
kates deploy -i
```

#### deploy status

Show the current deployment status of all Kates-managed components.

```bash
kates deploy status
```

Expected output:

```
  Operators & CRDs
    Strimzi Operator      [Healthy]
    Cert-Manager          [Healthy]
    Kyverno               [Healthy]

  Core Infrastructure
    Kafka (krafter)       [Healthy]
    PostgreSQL (CDC)      [Healthy]
    Kafka Connect         [Healthy]
    Monitoring Stack      [Healthy]

  Applications
    Apicurio Registry     [Healthy]
    Kates Backend         [Healthy]
    Kafka UI              [Healthy]
    Litmus Chaos          [Healthy]
```

#### clean

Remove all Kates-managed resources and namespaces.

```bash
kates clean
kates clean --force
```

#### detect

Aliases: `preflight-cluster`, `cluster-check`

Deep cluster compatibility report for 3-AZ Kafka.

```bash
kates detect
kates preflight-cluster
kates cluster-check
```

#### ports

Port-forward all Kates services to localhost.

```bash
kates ports
```

#### auto

Auto-detect cluster configuration and deploy Kafka.

```bash
kates auto
```

#### operator

Run the Kates Environment Operator.

```bash
kates operator
```

#### init

Initialize a new Kates workspace with config, scenarios, and CI gate.

```bash
kates init
```

#### upgrade

Build from source and install a new version of the Kates CLI.

```bash
kates upgrade
```

**See also:** [Deployment Guide](12-deployment.md) for detailed deployment topologies and configuration, [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md) for step-by-step setup.

---

### Security Commands

Security commands audit, test, and enforce security posture across your Kafka cluster. They cover TLS inspection, ACL verification, penetration testing, compliance mapping, and drift detection. The security suite produces a letter grade (A–F) for your cluster's security posture, making it easy to track improvements over time and gate CI/CD pipelines on minimum security standards.

Aliases: `sec`

```bash
kates security
kates sec
```

#### security audit

Aliases: `scan`

Run a full security posture audit with A–F grading.

```bash
kates security audit
kates security scan
kates sec audit -o json
```

#### security tls-inspect

Aliases: `tls`

Inspect TLS configuration, protocol versions, and cipher suites.

```bash
kates security tls-inspect
kates sec tls
```

#### security auth-test

Aliases: `auth`

Probe ACL rules for a specific user to verify least-privilege access.

```bash
kates security auth-test
kates sec auth
```

#### security pentest

Aliases: `pen`

Run adversarial penetration tests against the cluster.

```bash
kates security pentest
kates sec pen
```

#### security compliance

Aliases: `comply`

Map security checks to CIS Kafka Benchmark, SOC2, and PCI-DSS frameworks.

```bash
kates security compliance
kates sec comply
```

#### security baseline

Aliases: `base`

Save current security posture as baseline for drift detection.

```bash
kates security baseline
kates sec base
```

#### security drift

Compare current security posture against saved baseline.

```bash
kates security drift
kates sec drift
```

#### security gate

CI/CD security gate — exit non-zero if grade is below threshold.

```bash
kates security gate
kates sec gate --min-grade B
```

#### security certs

Aliases: `cert`, `certificates`

Inspect SSL/TLS certificate configuration across brokers.

```bash
kates security certs
kates sec cert
kates sec certificates
```

#### security cve

Check for known CVEs.

```bash
kates security cve
kates sec cve
```

#### security secrets

Audit Kubernetes secrets management.

```bash
kates security secrets
kates sec secrets
```

#### security netpol

Audit NetworkPolicy coverage.

```bash
kates security netpol
kates sec netpol
```

#### security acl-map

Visualize ACL topology.

```bash
kates security acl-map
kates sec acl-map
```

#### security config-diff

Diff security configs between clusters.

```bash
kates security config-diff
kates sec config-diff
```

#### security trend

Track security posture over time.

```bash
kates security trend
kates sec trend
```

**See also:** [Security & Compliance](17-security.md) for in-depth security auditing and hardening.

---

### Kyverno Policy Commands

Kyverno commands let you manage Kubernetes admission policies for your Kafka cluster. They provide visibility into which policies are active, which workloads are violating them, and tools to switch between Audit and Enforce modes. The `kyverno detect` command can even scan your cluster and recommend policies based on what it finds.

Aliases: `kyv`, `policy`

```bash
kates kyverno
kates kyv
kates policy
```

#### kyverno status

Aliases: `st`, `list`

Show all ClusterPolicies with mode, readiness, and rule counts.

```bash
kates kyverno status
kates kyv st
kates kyv list
```

#### kyverno violations

Aliases: `viol`, `fails`

Show policy violations grouped by namespace and pod.

```bash
kates kyverno violations
kates kyv viol
kates kyv fails --namespace kafka
```

| Flag | Description |
|------|-------------|
| `--namespace` | Filter violations by namespace |

#### kyverno enforce

Switch a ClusterPolicy to Enforce mode.

```bash
kates kyverno enforce <policy>
kates kyv enforce disallow-privilege-escalation
```

#### kyverno audit

Switch a ClusterPolicy to Audit mode.

```bash
kates kyverno audit <policy>
kates kyv audit disallow-privilege-escalation
```

#### kyverno detect

Introspect the cluster and recommend Kyverno policies based on workloads, ingress, and namespace structures. It detects third-party policies and suggests Kates-native baseline policies.

```bash
kates kyverno detect
```

#### kyverno apply

Automatically apply Kyverno policies based on cluster detection recommendations. Can optionally install the Kyverno Admission Controller if missing.

```bash
kates kyverno apply
kates kyverno apply --dry-run
kates kyverno apply --mode Enforce --yes --with-netpol
```

| Flag | Description |
|---|---|
| `--mode` | Validation mode: `Audit` or `Enforce` (default: `Audit`) |
| `--with-cosign` | Enable Cosign image signature verification |
| `--with-netpol` | Enable NetworkPolicy generation |
| `--yes`, `-y` | Skip confirmation prompt |
| `--dry-run` | Show what would be applied without executing |

**See also:** [Security & Compliance](17-security.md) for Kyverno policy deep dive and custom policy authoring.

---

### Kafka Client Commands

Kafka commands give you direct visibility into the cluster without leaving the Kates CLI. Instead of switching between `kafka-topics.sh`, `kafka-consumer-groups.sh`, and `kubectl`, you can inspect topics, groups, brokers, and ACLs from a single interface. The `kafka tui` command opens a full-screen interactive explorer for browsing topics, consuming messages, and inspecting consumer groups — it's the fastest way to poke around a cluster.

#### kafka

Interactive Kafka client.

```bash
kates kafka
```

#### kafka brokers

List brokers with ID, host, port, rack, and controller status.

```bash
kates kafka brokers
```

#### kafka topics

List all topics with partition, replication, and ISR health.

```bash
kates kafka topics
```

Expected output:

```
 Kafka Topics (42)

  Topic                    Type       Partitions   Rep. Factor   ISR Health
  ──────────────────────────────────────────────────────────────────────────
  orders.events                       6            3             ✓ HEALTHY
  user.signups                        3            3             ✓ HEALTHY
  payments.processed                  6            3             ✓ HEALTHY
  kates-events             system     3            3             ✓ HEALTHY
  __consumer_offsets       internal   50           3             ✓ HEALTHY
  ...
```

Topics with under-replicated partitions show `⚠ N under-replicated` in the ISR Health column. Use `--filter <substring>` to narrow the list.

#### kafka topic

Describe a topic — partitions, ISR, offsets, and configuration.

```bash
kates kafka topic <name>
kates kafka topic my-events
```

#### kafka groups

List consumer groups with state, members, and lag summary.

```bash
kates kafka groups
```

#### kafka group

Describe a consumer group with per-partition offsets and lag.

```bash
kates kafka group <id>
kates kafka group my-consumer-group
```

#### kafka consume

Fetch records from a topic (latest N records, or tail with `--follow`).

```bash
kates kafka consume <topic>
kates kafka consume my-events
kates kafka consume my-events --follow
```

| Flag | Description |
|------|-------------|
| `--follow` | Tail the topic continuously |

#### kafka produce

Produce a record to a topic (from flag or stdin).

```bash
kates kafka produce <topic>
kates kafka produce my-events --value '{"key": "value"}'
echo '{"key": "value"}' | kates kafka produce my-events
```

#### kafka create-topic

Create a new topic.

```bash
kates kafka create-topic <name>
kates kafka create-topic my-new-topic --partitions 6 --replication-factor 3
```

| Flag | Description |
|------|-------------|
| `--partitions` | Number of partitions |
| `--replication-factor` | Replication factor |

#### kafka alter-topic

Alter topic configuration entries.

```bash
kates kafka alter-topic <name>
kates kafka alter-topic my-events --set retention.ms=604800000
```

#### kafka delete-topic

Delete a topic (with confirmation prompt).

```bash
kates kafka delete-topic <name>
kates kafka delete-topic my-old-topic
```

#### kafka tui

Launch interactive Kafka explorer (full-screen TUI).

```bash
kates kafka tui
```

#### kafka connect

Manage Kafka Connect (via Strimzi CRDs) — inspect the Connect cluster, list and describe connectors, and perform lifecycle operations. All subcommands accept `-n`/`--namespace` to select the namespace where Connect is deployed (auto-detected by default: `KATES_CONNECT_NS` env var, then live cluster detection, then `KATES_KAFKA_NS`, then `kafka`).

```bash
kates kafka connect status                  # Connect cluster status
kates kafka connect connectors              # List all KafkaConnector CRs
kates kafka connect connector <name>        # Describe a connector
kates kafka connect tasks <name>            # Task-level status for a connector
kates kafka connect config <name>           # Show connector configuration
kates kafka connect plugins                 # List installed connector plugins
kates kafka connect logs --follow           # Tail Connect worker logs
kates kafka connect restart <name>          # Restart a connector
kates kafka connect restart-task <name> <taskId>
kates kafka connect pause <name>
kates kafka connect resume <name>
kates kafka connect delete <name>
kates kafka connect scale <replicas>        # Scale Connect workers
```

| Flag | Description |
|------|-------------|
| `-n`, `--namespace` | Namespace where Kafka Connect is deployed |
| `-f`, `--follow` | (`logs` only) Stream logs continuously |

**See also:** [Kafka Connect & CDC Pipelines](21-kafka-connect.md) for Connect cluster deployment and connector configuration, [The Cluster Under Test](03-cluster.md) for Kafka cluster architecture, [Kafka Deployment Engineering](15-kafka-deployment.md) for production Kafka configuration.

---

### Analysis & Optimization Commands

Analysis commands take raw test results and turn them into actionable recommendations. The `benchmark` command runs a full battery of tests and grades your cluster with a letter score. The `advisor` analyzes a specific run and suggests configuration improvements. The `explain` command produces a plain-English summary — useful when you need to share results with people who don't want to read latency tables.

#### benchmark

Aliases: `bench`

Run a full test battery (LOAD → STRESS → SPIKE) with a letter-grade scorecard.

```bash
kates benchmark
kates bench
```

#### advisor

Analyze test results and recommend configuration improvements.

```bash
kates advisor <run-id>
kates advisor abc123
```

#### explain

Aliases: `why`, `interpret`

Plain-English summary and verdict for a test run.

```bash
kates explain <id>
kates why <id>
kates interpret <id>
```

#### replay

Re-run a previous test with the same parameters.

```bash
kates replay <id>
kates replay abc123
```

#### gate

Aliases: `ci`, `quality-gate`

CI quality gate — run a test and exit non-zero if grade is below threshold.

```bash
kates gate
kates ci
kates quality-gate
kates gate --min-grade B
kates gate --min-grade C --type STRESS --records 100000
kates gate --min-grade A --timeout 300
```

| Flag | Default | Description |
|------|---------|-------------|
| `--min-grade` | `C` | Minimum passing grade (A, B, C, D, F) |
| `--type` | `LOAD` | Test type to run |
| `--records` | 50000 | Number of records |
| `--backend` | | Benchmark backend |
| `--timeout` | 180 | Timeout in seconds |

#### baseline

The baseline command sets a specific test run as the performance reference point for future regression detection. Once set, you can run `baseline regression <id>` to compare any new test against the baseline and see exactly where performance has changed. Baselines work hand-in-hand with trend analysis — trends show long-term drift, baselines catch acute regressions. The typical workflow is: run a comprehensive test on a known-good configuration, set it as baseline, then compare every subsequent run against it.

```bash
kates baseline set <id>
kates baseline regression <id>
```

**See also:** [Performance Theory](04-performance-theory.md) for statistical significance and why multiple runs matter, [CI/CD Pipeline](appendix-c-cicd.md) for quality gate examples.

---

### Tuning Commands

Tuning commands automate the tedious process of testing different Kafka configurations. Instead of manually running five tests with different `acks` settings, `tune run TUNE_ACKS` does it for you and presents a comparison table. Each tuning type sweeps across a specific configuration dimension — replication factor, batch size, compression codec, or partition count — so you can find the optimal setting for your workload.

#### tune

Configuration & tuning tests.

```bash
kates tune
```

#### tune run

Run a tuning test.

```bash
kates tune run <type>
kates tune run TUNE_REPLICATION
kates tune run TUNE_ACKS
kates tune run TUNE_BATCHING
kates tune run TUNE_COMPRESSION
kates tune run TUNE_PARTITIONS
```

| Type | Description |
|------|-------------|
| `TUNE_REPLICATION` | Test different replication factor settings |
| `TUNE_ACKS` | Test different acks modes |
| `TUNE_BATCHING` | Test different batch size configurations |
| `TUNE_COMPRESSION` | Test different compression codecs |
| `TUNE_PARTITIONS` | Test different partition counts |

#### tune report

Show tuning comparison report.

```bash
kates tune report <run-id>
kates tune report abc123
```

#### tune types

List available tuning tests.

```bash
kates tune types
```

**See also:** [Lab — Interactive Performance Tuning](10b-lab.md) for the interactive tuning workbench, [Test Types Deep Dive](05-test-types.md) for understanding how tuning tests differ from standard tests.

---

### Profile Commands

Save, compare, and assert named performance profiles.

```bash
kates profile
```

#### profile save

Save a test run as a named performance profile.

```bash
kates profile save <name> <run-id>
kates profile save baseline abc123
```

#### profile list

List all saved profiles.

```bash
kates profile list
```

#### profile compare

Compare two profiles side by side.

```bash
kates profile compare <name1> <name2>
kates profile compare baseline optimized
```

#### profile assert

Assert a test run meets a profile's thresholds.

```bash
kates profile assert <name> <run-id>
kates profile assert baseline def456
```

---

### Cost Estimation

The cost command estimates the cloud infrastructure costs associated with running a given test configuration at production scale. It factors in broker instance types, storage volumes, network transfer, and test duration to produce a rough cost estimate. Use it to answer questions like "how much would it cost to run this endurance test for 24 hours on EKS?" before committing real resources. Costs are estimated, not exact — they use published on-demand pricing for common cloud providers.

#### cost

Estimate cloud costs for test configurations.

```bash
kates cost
```

#### cost estimate

Estimate resource costs.

```bash
kates cost estimate
kates cost estimate --records 1000000 --record-size 1024 --duration 3600
```

| Flag | Description |
|------|-------------|
| `--records` | Number of records |
| `--record-size` | Record payload size in bytes |
| `--duration` | Test duration in seconds |

---

### Snapshot Commands

Snapshot commands capture the full state of your Kafka cluster — topics, partitions, consumer groups, broker configs, ACLs — at a point in time. The primary use case is before/after comparison: take a snapshot before a change, make the change, take another snapshot, then diff them. The diff shows exactly what changed: new topics, altered configs, shifted partition leaders. Snapshots are stored server-side, so they persist across CLI sessions.

#### snapshot

Capture, list, and compare cluster state snapshots.

```bash
kates snapshot
```

#### snapshot create

Capture current cluster state as a named snapshot.

```bash
kates snapshot create <name>
kates snapshot create pre-upgrade
```

#### snapshot list

List all saved snapshots.

```bash
kates snapshot list
```

#### snapshot diff

Compare two snapshots.

```bash
kates snapshot diff <name1> <name2>
kates snapshot diff pre-upgrade post-upgrade
```

**See also:** [Upgrade Playbook](18-upgrade-playbook.md) for using snapshots during Kafka version upgrades.

---

### Flow Pipelines

A flow is a declarative multi-step pipeline defined in YAML. Flows let you chain multiple Kates operations — tests, disruptions, reports, gates — into a single automated sequence. Each step can depend on the output of previous steps, and the pipeline stops on first failure. Use flows for complex validation sequences that would otherwise require a shell script: "run a load test, then a chaos test, then diff the results, then gate on grade B or better."

#### flow

Declarative multi-step pipeline orchestrator.

```bash
kates flow
```

#### flow run

Execute a flow pipeline from a YAML file.

```bash
kates flow run -f pipeline.yaml
```

| Flag | Description |
|------|-------------|
| `-f` | Path to flow pipeline YAML file |

---

### Badge Generation

The badge command generates shields.io-compatible badge URLs from your most recent completed test — ready to paste into a repository README, GitHub PR, or dashboard. It prints the raw URL plus ready-made Markdown and HTML snippets. The badge value comes from the latest `DONE` test (optionally filtered by test type), so it reflects your most recent results each time you regenerate it.

```bash
kates badge                                # grade badge from the latest test
kates badge --type LOAD --metric grade
kates badge --type STRESS --metric p99
kates badge --metric throughput
```

| Flag | Default | Description |
|------|---------|-------------|
| `--type` | | Test type filter (LOAD, STRESS, etc.) |
| `--metric` | `grade` | Badge metric: `grade`, `p99`, or `throughput` |

---

### Webhook Notifications

Webhooks send HTTP POST notifications to external endpoints when a test finishes. The backend fires a `test.completed` event whenever a test reaches `DONE` or `FAILED` status; the payload carries the event name, test ID, test type, final status, and timestamp (the event name is also sent in the `X-Kates-Event` header). Use webhooks to integrate Kates with Slack, PagerDuty, Microsoft Teams, or any system that accepts incoming webhooks. Each webhook registration binds a name to a URL. Deliveries are retried, and events that still fail are parked in a dead-letter queue.

#### webhook

Manage webhook notifications for test completion events.

```bash
kates webhook
```

#### webhook list

List registered webhooks.

```bash
kates webhook list
```

#### webhook add

Register a webhook.

```bash
kates webhook add <name> <url>
kates webhook add slack-alerts https://hooks.slack.com/services/...
kates webhook add pagerduty https://events.pagerduty.com/integration/...
```

#### webhook remove

Aliases: `rm`, `delete`

Unregister a webhook.

```bash
kates webhook remove <name>
kates webhook rm <name>
kates webhook delete <name>
```

---

### Developer & Help Commands

#### docs

Man-style documentation for all Kates commands.

```bash
kates docs
kates docs test create
kates docs security audit
```

#### tldr

Quick command reference cheatsheet.

```bash
kates tldr
kates tldr security
kates tldr kafka
```

#### changelog

Generate changelog from audit events.

```bash
kates changelog
kates changelog --since 2025-01-01 --until 2025-01-31
```

| Flag | Description |
|------|-------------|
| `--since` | Start date for changelog range |
| `--until` | End date for changelog range |

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
