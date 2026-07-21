# CLI Reference

Reference for the Kates CLI — the commands, flags, and output formats you'll use day to day.

This chapter serves two readers: the operator scanning for a flag mid-incident, and the newcomer building a mental map of what the CLI can do. After this chapter, you can:

- Chain individual commands into complete workflows — regression checks, lag investigations, chaos validation, and CI gates
- Manage contexts with `kates ctx` so one binary drives local, staging, and production
- Locate the right command family for any task, from test lifecycle to security auditing
- Switch any command to JSON output and wire it into scripts and pipelines

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
# Note the test ID (e.g. t-a1b2c3)

# 3. Perform the Kafka upgrade (outside of Kates)

# 4. Run the same load test on the new version
kates test create --type LOAD --records 100000 --wait
# Note the new test ID (e.g. t-d4e5f6)

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

```text
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

```text
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

```text
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

#### audit

View the audit log of cluster mutations. Every state-changing operation — test creation, topic changes, disruptions, resilience runs — lands in the audit log with a timestamp and event type, so you can reconstruct what changed and when. The `changelog` command (under Developer & Help Commands) renders these same events as a release-notes-style document.

```bash
kates audit
kates audit --type test --limit 20
kates audit --since 2026-07-01T00:00:00Z
```

| Flag | Default | Description |
|------|---------|-------------|
| `--limit` | 50 | Maximum number of events to show |
| `--type` | | Filter by event type (`test`, `topic`, `disruption`, `resilience`) |
| `--since` | | Show events after this ISO-8601 timestamp |

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

```text
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

```text
 Kafka Cluster Topology — Cluster: krafter  │  Kafka 4.3.0  │  KRaft Mode

  Kubernetes Platform
  Version:   v1.31.4
  Platform:  linux/arm64
  Nodes:     3

  Strimzi Operator
  Version:     1.1.0
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
kates watch <id>                  # root-level shortcut
kates watch <id> --interval 5
```

Live-stream test progress to the terminal, refreshing every 3 seconds by default (`--interval` adjusts it).

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
| `quick-load` | LOAD | Quick smoke test — 50k records, 2 producers, P99 < 100ms gate |
| `production-load` | LOAD | Production-grade — 1M records, 8 producers, acks=all, lz4, strict SLA |
| `stress-test` | STRESS | High-throughput stress — 5M records, 16 producers, find breaking points |
| `endurance-soak` | ENDURANCE | 1-hour soak at 5k msg/s — detect GC pauses and log compaction issues |
| `exactly-once` | ROUND_TRIP | E2E integrity — idempotent + transactional, zero-loss, CRC verification |
| `integrity-tx` | INTEGRITY | Transactional integrity — 4 producers, zstd, CRC, zero-loss verification |
| `spike-test` | SPIKE | Burst traffic — 32 producers for 60s, test backpressure handling |
| `ci-gate` | LOAD | CI pipeline gate — fast 10k-record validation with strict zero-error SLA |

**See also:** [Test Types Deep Dive](05-test-types.md) for the theory behind each test type, [Scenario Files & SLA Gates](13-scenario-files.md) for YAML scenario syntax.

#### scenario-diff

Alias: `sdiff`

Compare a scenario YAML against a completed test run to detect config drift. The scenario file in your repo says one thing — did the run actually execute with those parameters? `scenario-diff` flags every field where the file and the run diverge, which makes it the fastest way to catch a stale scenario or an out-of-band override.

```bash
kates scenario-diff scenario.yaml <test-id>
kates sdiff scenario.yaml <test-id>
```

**See also:** [Scenario Files & SLA Gates](13-scenario-files.md) for the scenario schema.

---

### Report Commands

After a test completes, reports are where the numbers become answers. Report commands let you view full results, export them for CI pipelines, diff two runs side by side, and drill into per-broker metrics to find hot spots. The `report diff` command is particularly powerful — it highlights exactly where two runs diverge, making it the go-to tool for before/after comparisons during upgrades, tuning, and regression checks.

#### report show

```bash
kates report show <id>
```

Display the full report for a test run.

Expected output:

```text
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
kates diff <id1> <id2>            # root-level shortcut
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

```text
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

The remaining command families live in two companion chapters. [Operations CLI Reference](cli-operations.md) covers the commands that change the system — disruptions, chaos experiment history, resilience, schedules, observability, the interactive Lab, and deployment & lifecycle. [Security & Analysis CLI Reference](cli-security-analysis.md) covers the inspection and hardening toolbox — security, Kyverno policies, the Kafka client, analysis & optimization, tuning, profiles, cost, snapshots, flows, badges, webhooks, and developer tooling.

## Output Modes

All commands support two output modes:

```bash
# Table output (default) — human-readable with colors
kates test list -o table

# JSON output — structured, machine-readable
kates test list -o json
```

Output degrades automatically. When stdout is not a terminal — a pipe, a redirect, a CI log — color codes are stripped, refreshing displays become append-only lines, and full-screen TUIs refuse with a plain-text explanation instead of launching. `NO_COLOR` and `TERM=dumb` are honored; `--plain` (or `KATES_PLAIN=1`) forces the fully plain, pure-ASCII form; `KATES_ASCII=1` keeps colors but swaps glyphs for ASCII.

## Exit Codes

The exit code is a contract: `0` means the thing you asked for happened.

| Code | Meaning |
|------|---------|
| `0` | The requested operation completed — a `--wait` test finished successfully, a deploy applied, a deletion was confirmed and performed |
| `1` | Anything else: the operation failed, a followed test finished `FAILED`, the connection was lost mid-follow (outcome unknown), a confirmation was declined or could not be asked |

Three consequences worth knowing in scripts:

- `kates test create --wait` and `kates test watch` exit `1` when the test itself fails — not just when the request fails.
- A declined confirmation exits `1`. A script that forgot `--yes` fails loudly instead of reporting success for work it did not do.
- Confirmations are never answered implicitly. With no terminal attached, a command that needs consent fails and tells you to pass `--yes`, rather than assuming either answer.

## Shell Completion

```bash
# Bash
kates completion bash > /etc/bash_completion.d/kates

# Zsh
kates completion zsh > "${fpath[1]}/_kates"

# Fish
kates completion fish > ~/.config/fish/completions/kates.fish
```

::: {.callout-tip}
**Try it**

None of these commands need a running cluster — they read from the binary itself, so they work the moment `make cli-install` finishes:

```bash
kates version
kates tldr
kates tldr kafka
kates docs test create
```

Expect a version banner (with "API: not reachable" when no server is up), a cheatsheet of the most-used commands, its Kafka-specific subset, and man-style documentation for `kates test create`.
:::

## Summary

- The Common Workflows section is the map: regression checking, lag investigation, chaos validation, pre-production checkout, and CI gating each chain a handful of commands into a repeatable task.
- Contexts (`kates ctx set`, `kates ctx use`) let one binary target every environment; `--url` and `--context` override the active context for a single call.
- `kates health`, `kates status`, and `kates doctor` form an escalating diagnostic ladder — start cheap, go deep only when something looks wrong.
- Every command supports `-o table` for humans and `-o json` for scripts — JSON mode is what makes the CI/CD workflows possible.
- When you can't remember a command, the CLI documents itself: `kates tldr` for a cheatsheet, `kates docs` for man-style detail, and shell completion for everything in between.

Every one of these commands talks to the same HTTP API, and [REST API Reference](11-api-reference.md) documents those endpoints for when a script or integration needs to skip the CLI.
