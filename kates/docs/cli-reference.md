# CLI Reference

The KATES CLI is a terminal-first interface for managing performance tests, diagnosing cluster health, analysing results, and automating workflows. Every command respects the `--context` and `--output` flags and communicates with a single KATES API server.

## Configuration

Before using the CLI, set up a server context:

```bash
kates ctx set local --url http://localhost:30083
kates ctx use local
```

Context configuration is stored in `~/.kates.yaml`. You can override the API URL for any single call with `--url`.

## Core Commands

### health

Quick system health check covering the API server, Kafka connectivity, and benchmark engines.

```bash
kates health
```

### cluster

Inspect Kafka cluster metadata: brokers, topics, consumer groups, and configurations.

```bash
kates cluster info
kates cluster topics
kates cluster topic kates-benchmark --partitions
kates cluster groups
kates cluster group my-consumer-group
kates cluster broker 0 --configs
kates cluster check
```

### test

Create, list, inspect, and manage performance test runs.

```bash
kates test create --type LOAD --records 100000
kates test create --type STRESS --records 500000 --producers 4
kates test list
kates test list --type LOAD --status DONE
kates test get <id>
kates test watch <id>
kates test delete <id>
```

**Sub-commands:**

| Command | Description |
|---------|-------------|
| `test create` | Start a new performance test |
| `test list` | List tests with optional type/status filters |
| `test get` | Detailed view with phase results, smart hints on failures, and throughput bar |
| `test watch` | Live-watch a running test with throughput sparkline |
| `test delete` | Stop and delete a test |
| `test types` | List available test types with descriptions |
| `test backends` | List available benchmark backends |
| `test apply` | Run tests from a YAML scenario file |
| `test scaffold` | Generate a YAML scenario template |
| `test compare` | Side-by-side metric comparison of two runs |
| `test summary` | Aggregate statistics across all completed tests |
| `test flame` | ASCII latency distribution histogram |
| `test cleanup` | Delete orphaned RUNNING tests (stuck >5 minutes) |
| `test export` | Export results to CSV or JSON file |

#### test compare

Compare two test runs side-by-side with performance deltas:

```bash
kates test compare <id1> <id2>
```

Shows ▲/▼ delta arrows for throughput, latency and records, colour-coded for whether higher is better or worse.

#### test summary

Aggregated statistics across all completed tests:

```bash
kates test summary
```

Displays total records, average/best/worst throughput, average P99 latency, success rate, and test counts by type.

#### test flame

ASCII latency distribution chart for a single test run:

```bash
kates test flame <id>
```

Renders horizontal bars for Avg, P50, P95, P99, and Max latency, colour-coded green → yellow → red by severity.

#### test cleanup

Detect and delete tests stuck in RUNNING state:

```bash
kates test cleanup              # deletes orphans
kates test cleanup --dry-run    # preview only
```

A test is considered orphaned if it has been in RUNNING state for more than 5 minutes.

#### test export

Export test results to a file:

```bash
kates test export <id>                    # CSV (default)
kates test export <id> --format json      # JSON
kates test export <id> -f results.csv     # custom path
```

## Analysis

### report

View rich test reports with SLA grading and export options.

```bash
kates report <id>
kates report <id> --csv
kates report <id> --junit
kates report diff <id1> <id2>
kates report brokers <id>
kates report snapshot <id>
```

### trend

Historical performance trend analysis with sparkline charts and regression detection.

```bash
kates trend --type LOAD --metric p99LatencyMs
kates trend --type LOAD --metric p99LatencyMs --days 30
kates trend breakdown --type STRESS --metric throughputRecPerSec
kates trend broker --broker 0 --metric avgLatencyMs
```

### resilience

Combine performance testing with chaos engineering for correlation analysis.

```bash
kates resilience --type LOAD --playbook broker-kill
```

### benchmark

Run a full test battery (LOAD → STRESS → SPIKE) and get a letter-grade scorecard:

```bash
kates benchmark
kates benchmark --records 50000
kates benchmark --backend native
```

The scorecard evaluates average throughput and worst P99 latency across all three phases and assigns a grade from A to F.

### gate

CI quality gate — run a test and exit non-zero if the grade falls below a threshold:

```bash
kates gate                              # default: min-grade C, type LOAD
kates gate --min-grade B --type STRESS
kates gate --min-grade A --records 100000 --timeout 300
```

Designed for CI/CD pipelines. Runs a complete test, computes a letter grade using the same algorithm as `benchmark`, then exits with code 0 (pass) or 1 (fail) based on the `--min-grade` threshold.

| Flag | Default | Description |
|------|---------|-------------|
| `--min-grade` | `C` | Minimum passing grade (A, B, C, D, F) |
| `--type` | `LOAD` | Test type to run |
| `--records` | `50000` | Number of records |
| `--backend` | _(auto)_ | Benchmark backend |
| `--timeout` | `180` | Timeout in seconds |

## Toolbox

### doctor

Pre-flight cluster readiness checklist:

```bash
kates doctor
```

Checks:
1. API reachable
2. Kafka connectivity
3. Broker count ≥ 3
4. ISR health (no under-replicated partitions)
5. Topics available
6. Benchmark backends registered

### replay

Re-run a previous test with identical parameters:

```bash
kates replay <id>
kates replay <id> --wait
```

Fetches the original test's specification describing type, backend, records, duration, producers etc. and submits a new test with the same values.

### explain

Plain-English narrative summary of a test run:

```bash
kates explain <id>
```

Generates a human-readable explanation including:
- Test type and what it measures
- Records processed, throughput, latency ranges
- Root cause analysis for failures (with smart hints)
- A verdict: ✓ HEALTHY, ⚠ DEGRADED, or ✖ POOR

### scenario-diff

Compare a scenario YAML file against a completed test run to detect configuration drift:

```bash
kates scenario-diff scenario.yaml 69acdf31
```

Checks the YAML fields (type, backend, spec parameters) against the actual values used in the test run and reports any differences. Useful for auditing whether a test was run with the intended configuration.

## Observability

### dashboard

Full-screen terminal monitoring dashboard with four panel rows:

```bash
kates dashboard
kates dash
kates dashboard --interval 5
```

Panels:
- **System Health** — API status, Kafka connectivity, engine backend, loaded configs
- **Test Summary** — running/pending/done/failed counts with total
- **Active Tests** — live details of running tests with throughput and latency
- **Recent Completed** — last 5 finished tests with status badges
- **Throughput ↗** — sparkline trend of aggregate throughput over time
- **P99 Latency ↘** — sparkline trend of tail latency over time
- **Cluster Detail** — broker count, topic count, ISR health, controller ID
- **Quick Commands** — common CLI commands for quick reference

### top

Live view of running tests, refreshing every 2 seconds:

```bash
kates top
```

### status

One-line system health status:

```bash
kates status
```

### version

CLI, API, and runtime version information:

```bash
kates version
```

### webhook

Manage webhook notifications for test completion events. When a test finishes (DONE or FAILED), the backend sends an HTTP POST to all registered URLs with a JSON payload.

```bash
kates webhook list
kates webhook add slack https://hooks.slack.com/services/T.../B.../xxx
kates webhook add teams https://outlook.office.com/webhook/...
kates webhook remove slack
```

The webhook payload contains:

```json
{
  "event": "test.completed",
  "testId": "69acdf31",
  "testType": "LOAD",
  "status": "DONE",
  "timestamp": "2026-02-19T01:00:00Z"
}
```

## Configuration

### ctx

Manage named server contexts (similar to kubectl contexts):

```bash
kates ctx list
kates ctx set staging --url https://kates.staging.internal
kates ctx use staging
kates ctx current
kates ctx delete old-context
```

### completion

Generate shell auto-completion scripts:

```bash
kates completion bash
kates completion zsh
kates completion fish
```

## Global Flags

| Flag | Description |
|------|-------------|
| `--url` | Override API URL for this call |
| `--context` | Use a named context for this call |
| `-o, --output` | Output format: `table` or `json` |
| `-h, --help` | Show help for any command |

## Smart Failure Hints

When `kates test get` encounters a failed test, it pattern-matches the error message against known Kafka failure patterns and suggests actionable fixes:

| Pattern | Suggestion |
|---------|------------|
| `LZ4Factory` | Missing lz4-java dependency |
| `NoClassDefFoundError` | Check Maven dependencies |
| `Connection refused` | Verify bootstrap servers |
| `TimeoutException` | Increase timeout or check broker health |
| `TopicAuthorizationException` | Check Kafka ACL configuration |
| `RecordTooLargeException` | Reduce record size or increase broker limit |
| `OutOfMemoryError` | Increase -Xmx in deployment config |
