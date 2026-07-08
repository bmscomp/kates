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

Before upgrading Kafka from 3.8 to 3.9, you want to know if the new version regresses performance. The idea is simple: capture a baseline on the current version, perform the upgrade, run the same test again, and diff the results. If P99 latency or throughput moves outside your tolerance, you have a data-backed reason to investigate before the upgrade reaches production.

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
kates report export <test-id> --format heatmap -o heatmap.json
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

You want every pull request to prove it doesn't regress Kafka performance. This workflow integrates into your CI pipeline — it runs a scenario file, exports JUnit results, and exits non-zero if the grade drops below your threshold.

```bash
# 1. Run the scenario defined in your repo
kates test apply -f ci/load-test.yaml --wait

# 2. Export results as JUnit XML for your CI system
kates report export <id> --format junit -o results.xml

# 3. Gate: fail the build if the grade is below B
kates gate -f ci/load-test.yaml --min-grade B
```

::: {.callout-tip}
See [Appendix C: CI/CD Integration](appendix-c-cicd.md) for complete GitHub Actions, GitLab CI, and Jenkins pipeline examples.
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
 Kates Health Check

  Component          Status    Details
  ─────────────────────────────────────────────────────
  API Server         ● UP      v1.17.0 (uptime 4d 12h)
  Kafka Cluster      ● UP      3 brokers, 0 under-replicated
  Schema Registry    ● UP      Apicurio 2.2.5.Final
  Test Engine        ● IDLE    No tests running
  Database           ● UP      PostgreSQL 16.3
  Monitoring         ● UP      Prometheus + Grafana

  Overall: ● HEALTHY
```

#### status

Quick one-line system status — useful for scripting and prompts.

```bash
kates status
```

Expected output:

```
● HEALTHY | 3 brokers | 42 topics | 0 running tests | v1.17.0
```

#### version

Show CLI, API, and runtime version information.

```bash
kates version
```

#### doctor

Aliases: `preflight`, `check`

Pre-flight cluster readiness checklist. The doctor command performs a deep diagnostic of your environment: Kubernetes connectivity, Strimzi operator status, Kafka AdminClient reachability, storage provisioner health, and namespace configuration. It's the first command to run when something "feels wrong" but `kates health` reports healthy — doctor checks the infrastructure layers that health doesn't.

```bash
kates doctor
kates preflight
kates check
```

Expected output:

```
 Kates Doctor — Environment Diagnostics

  Check                          Result
  ──────────────────────────────────────────────────────
  Kubernetes API                 ✔ Reachable (v1.32.4)
  kubectl context                ✔ kind-panda
  Strimzi Operator               ✔ Running (0.45.0)
  Kafka Cluster CR               ✔ Ready (3 brokers)
  KRaft Controller               ✔ Active (broker-0)
  AdminClient connectivity       ✔ Connected
  Schema Registry                ✔ Healthy
  Prometheus                     ✔ Scraping 12 targets
  Grafana                        ✔ 10 dashboards loaded
  Namespace: kafka               ✔ Exists
  Namespace: kates               ✔ Exists
  StorageClass: standard         ✔ Available
  PVC health                     ✔ 3/3 Bound

  Result: 13/13 checks passed ✔
```

**See also:** [Chapter 12: Deployment](12-deployment.md) for environment setup, [Appendix B: Troubleshooting](appendix-b-troubleshooting.md) for common diagnostic failures.

---

### Cluster Commands

Cluster commands give you direct visibility into the Kafka cluster without leaving the Kates CLI. Instead of switching between `kafka-topics.sh`, `kafka-consumer-groups.sh`, and `kubectl`, you can inspect topics, groups, brokers, and ACLs from a single interface. These commands query both the Kubernetes API and the Kafka AdminClient to give you a unified picture of cluster state.

#### cluster

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

#### cluster info

Display cluster metadata including broker list, controller identity, cluster ID, and Kafka version.

```bash
kates cluster info
```

Expected output:

```
 Kafka Cluster Info

  Cluster ID:    dQw4w9WgXcQ
  Kafka Version: 4.2.0
  Mode:          KRaft (no ZooKeeper)
  Controller:    broker-0 (id: 0)

  Brokers (3):
  ID   Host                           Port   Rack    Role
  ──   ────                           ────   ────    ────
  0    panda-broker-0.kafka.svc       9092   alpha   broker,controller
  1    panda-broker-1.kafka.svc       9092   sigma   broker
  2    panda-broker-2.kafka.svc       9092   gamma   broker

  Topics: 42 | Partitions: 186 | Consumer Groups: 7
```

#### cluster check

Run a comprehensive Kafka cluster health check. Reports broker count, controller identity, topic/partition counts, consumer groups, and partition health (under-replicated, offline). Problems are displayed inline.

```bash
kates cluster check
kates cluster check -o json
```

Output statuses: `● HEALTHY`, `▲ WARNING`, `✖ CRITICAL`.

#### cluster topology

Display the full Strimzi/Kafka cluster topology with 26 data sections. Requires the Kates backend to be deployed on Kubernetes with access to Strimzi CRDs and Kafka AdminClient APIs. This is the most comprehensive view of your cluster — use it to verify broker/controller layout after deployment or to audit infrastructure before a load test.

```bash
kates cluster topology
kates cluster topology -o json
```

Expected output (abbreviated — full output includes 26 sections):

```
 Kates Cluster Topology — 26 Sections

 ── 1. Kubernetes Platform ─────────────────────────
  Version:     v1.32.4
  Provider:    kind
  Nodes:       3 (panda-worker, panda-worker2, panda-worker3)

 ── 2. Strimzi Operator ────────────────────────────
  Version:     0.45.0
  Replicas:    1/1 Ready
  Watching:    All namespaces

 ── 3. Kafka Cluster ───────────────────────────────
  Name:        panda
  Namespace:   kafka
  Replicas:    3
  Status:      Ready
  Listeners:   PLAIN (9092), TLS (9093)

 ── 6. Controllers ─────────────────────────────────
  Active:      broker-0 (id: 0, zone: alpha)
  Mode:        KRaft

 ── 7. Brokers ─────────────────────────────────────
  ID  Pod                  Zone    CPU    Memory   Disk
  0   panda-broker-0       alpha   120m   1.2Gi    4.8Gi
  1   panda-broker-1       sigma   95m    1.1Gi    4.6Gi
  2   panda-broker-2       gamma   110m   1.2Gi    4.7Gi

  ... (19 more sections)
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

**See also:** [Chapter 3: Cluster Setup](03-cluster.md) for cluster architecture, [Chapter 9: Observability](09-observability.md) for Grafana dashboards.

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

**See also:** [Chapter 5: Test Types](05-test-types.md) for the theory behind each test type, [Chapter 13: Scenario Files](13-scenario-files.md) for YAML scenario syntax.

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
 Test Report — t-a1b2c3

  Type:       LOAD
  Status:     DONE
  Duration:   32.4s
  Backend:    native-loom
  Topic:      kates-perf-test-a1b2c3 (6 partitions, RF=3)

  ── Producer Metrics ──────────────────────────────
  Records Sent:          100,000
  Throughput:            3,086 rec/s (3.02 MB/s)
  Avg Latency:           4.12 ms
  P50 Latency:           3.00 ms
  P95 Latency:           8.00 ms
  P99 Latency:           22.00 ms
  Max Latency:           186.00 ms

  ── Consumer Metrics ──────────────────────────────
  Records Consumed:      100,000
  Consumer Throughput:   3,102 rec/s (3.04 MB/s)
  Integrity:             ✔ 100.00% (0 lost, 0 duplicates)

  ── SLA Verdict ───────────────────────────────────
  Grade: A
  P99 ≤ 50ms:           ✔ PASS (22.00ms)
  Throughput ≥ 1000:     ✔ PASS (3,086 rec/s)
  Data Loss = 0:         ✔ PASS
```

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

**See also:** [Chapter 9: Observability](09-observability.md) for heatmap interpretation and Grafana integration, [Chapter 4: Performance Theory](04-performance-theory.md) for understanding percentile metrics.

---

### Trend Analysis

Trend analysis is how you move from "this test looks fine" to "performance has been stable for weeks." The trend command queries historical test results and renders sparkline charts showing how a metric has changed over time. It's essential for catching slow regressions that no single test run would reveal — a P99 that creeps from 15ms to 25ms over a month is invisible in individual reports but obvious in a trend chart.

#### trend

Historical performance trend analysis.

```bash
kates trend --type LOAD --metric p99LatencyMs --days 30
kates trend --type LOAD --metric throughputRecordsPerSec --days 7
```

Expected output:

```
 Performance Trend — LOAD / p99LatencyMs / 30 days

  Date         P99 (ms)   Trend
  ──────────────────────────────────────────
  2026-06-09   18.0       ▁▁▁▂▂
  2026-06-16   19.5       ▁▁▂▂▃
  2026-06-23   21.0       ▁▂▂▃▃
  2026-06-30   20.0       ▁▂▂▂▃
  2026-07-07   22.0       ▂▂▃▃▃

  30-day avg: 20.1ms | Min: 17.5ms | Max: 24.0ms
  Trend: → stable (±8.2% variance)
```

| Flag | Description |
|------|-------------|
| `--type` | Test type to analyze |
| `--metric` | Metric name: `p99LatencyMs`, `avgLatencyMs`, `throughputRecordsPerSec` |
| `--days` | Lookback period in days |

**See also:** [Chapter 4: Performance Theory](04-performance-theory.md) for statistical significance and why single runs are insufficient.

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

**See also:** [Chapter 6: Chaos Theory](06-chaos-theory.md) for the principles behind chaos engineering, [Chapter 7: Chaos Practice](07-chaos-practice.md) for step-by-step chaos test walkthroughs.

---

### Resilience

Combined performance + chaos testing. Resilience tests run a load workload and inject disruptions simultaneously, then grade the cluster's ability to maintain SLA under fault conditions. This is the highest-level chaos primitive — it combines what you'd otherwise do manually with `test create` + `disruption run`.

```bash
kates resilience run --config resilience-test.json
```

**See also:** [Chapter 7: Chaos Practice](07-chaos-practice.md) for resilience test configuration.

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

**See also:** [Chapter 14: Recipes](14-recipes.md) for schedule-based regression detection patterns.

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

**See also:** [Chapter 9: Observability](09-observability.md) for Grafana dashboards and Prometheus alert rules.

---

### Interactive Lab

The lab is an interactive performance tuning workbench. It opens a full-screen TUI where you can iterate on test parameters — tweak batch size, change acks mode, adjust partition count — and immediately see the impact on throughput and latency via live sparklines. It supports A/B comparison, auto-sweep across parameter ranges, and CSV export of all iterations.

```bash
kates lab
```

Key features: parameter presets (`p`), auto-sweep (`s`), iteration diff (`d`), pin-and-compare (`c`), export (`e`), session save/load (`w`/`L`), cancel running test (`x`), retry on failure (`r`).

See [Chapter 10b: Lab](10b-lab.md) for the full guide.

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
 Kates Deployment Status

  Component             Namespace    Status     Version
  ─────────────────────────────────────────────────────
  Strimzi Operator      kafka        ● Running  0.45.0
  Kafka Cluster         kafka        ● Ready    4.2.0
  Kates Backend         kates        ● Running  1.17.0
  PostgreSQL            kates        ● Running  16.3
  Prometheus            monitoring   ● Running  2.54.0
  Grafana               monitoring   ● Running  11.6.0
  Jaeger                monitoring   ● Running  1.62.0
  LitmusChaos           litmus       ● Running  3.14.0

  Overall: 8/8 components healthy
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

**See also:** [Chapter 12: Deployment](12-deployment.md) for detailed deployment topologies and configuration, [Chapter 20: Installation Guide](20-installation-guide.md) for step-by-step setup.

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

**See also:** [Chapter 17: Security](17-security.md) for in-depth security auditing and hardening.

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

**See also:** [Chapter 17: Security](17-security.md) for Kyverno policy deep dive and custom policy authoring.

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
 Kafka Topics (42 total)

  Topic                          Partitions   RF   ISR    Status
  ─────────────────────────────────────────────────────────────────
  orders.events                  6            3    6/6    ● Healthy
  user.signups                   3            3    3/3    ● Healthy
  payments.processed             6            3    6/6    ● Healthy
  inventory.updates              3            3    3/3    ● Healthy
  kates-perf-test-a1b2c3         6            3    6/6    ● Healthy
  __consumer_offsets              50           3    50/50  ● Healthy
  ...

  Summary: 42 topics, 186 partitions, 0 under-replicated
```

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

**See also:** [Chapter 3: Cluster Setup](03-cluster.md) for Kafka cluster architecture, [Chapter 15: Kafka Deployment](15-kafka-deployment.md) for production Kafka configuration.

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
kates gate -f scenario.yaml --min-grade B
```

#### baseline

The baseline command sets a specific test run as the performance reference point for future regression detection. Once set, you can run `baseline regression <id>` to compare any new test against the baseline and see exactly where performance has changed. Baselines work hand-in-hand with trend analysis — trends show long-term drift, baselines catch acute regressions. The typical workflow is: run a comprehensive test on a known-good configuration, set it as baseline, then compare every subsequent run against it.

```bash
kates baseline set <id>
kates baseline regression <id>
```

**See also:** [Chapter 4: Performance Theory](04-performance-theory.md) for statistical significance and why multiple runs matter, [Appendix C: CI/CD Integration](appendix-c-cicd.md) for quality gate examples.

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

**See also:** [Chapter 10b: Lab](10b-lab.md) for the interactive tuning workbench, [Chapter 5: Test Types](05-test-types.md) for understanding how tuning tests differ from standard tests.

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

**See also:** [Chapter 18: Upgrade Playbook](18-upgrade-playbook.md) for using snapshots during Kafka version upgrades.

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

The badge command generates shields.io-compatible SVG badges that display your cluster's latest performance grade, security score, or build status. Embed them in your repository's README, wiki pages, or internal dashboards. Badges are generated from the most recent test or audit results — they update automatically when you run new tests. Supported badge types include `build`, `performance`, `security`, and `health`.

```bash
kates badge
kates badge --type build --output badge.svg
kates badge --type performance --output perf-badge.svg
kates badge --type security --output sec-badge.svg
```

---

### Webhook Notifications

Webhooks send HTTP POST notifications to external endpoints when specific events occur in Kates. Supported events include `test.completed`, `test.failed`, `disruption.completed`, `schedule.triggered`, and `security.audit.completed`. Use webhooks to integrate Kates with Slack, PagerDuty, Microsoft Teams, or any system that accepts incoming webhooks. Each webhook registration binds a name, a URL, and an optional event filter — if no event filter is specified, the webhook fires for all events.

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
