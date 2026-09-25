# CLI Reference

Reference for the Kates CLI — the commands, flags, and output formats you'll use day to day.

This chapter serves two readers: the operator scanning for a flag mid-incident, and the newcomer building a mental map of what the CLI can do. After this chapter, you can:

- Chain individual commands into complete workflows — regression checks, lag investigations, chaos validation, and CI gates
- Manage contexts with `kates ctx` so one binary drives local, staging, and production
- Locate the right command family for any task, from test lifecycle to security auditing
- Switch the commands that have a JSON form to JSON output and wire them into scripts and pipelines

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

# 4. Run a 25-minute endurance test, inside the 30-minute limit on any run (see test create)
kates test create --type ENDURANCE --duration 1500 --wait

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
# Set a context, with the API key the kates chart generated
kates ctx set local --url http://localhost:30083 \
  --api-key "$(kubectl get secret kates-api-key -n kates -o jsonpath='{.data.api-key}' | base64 -d)"

# Use a context
kates ctx use local

# List contexts (the active one is marked →)
kates ctx show

# Override context for a single call
kates --url http://other-server:8080 health
kates --context staging test list
```

The `kates` chart turns API-key authentication on by default and generates the key into the `kates-api-key` Secret; the command above reads it from the `kates` namespace of the default install. The URL answers only while something forwards the API to it: `make ports` forwards it to `localhost:30083`, `kates ports` to `localhost:8080`, and when only one of those two ports answers, the CLI uses that one. `kates health` reads a public endpoint and succeeds without a key, so check a new context with `kates test list` instead. `kates ctx set` stores a context without switching to it — the configuration always carries a built-in `default` context pointing at `http://localhost:8080` — so follow it with `kates ctx use`.

The context stores the key in plain text in `~/.kates.yaml`. The CLI writes that file readable by you alone (mode `0600`); a file that an earlier release left readable by others is tightened to `0600` the next time the CLI saves it, on `kates ctx use` for instance. That keeps other users of the machine out, not other programs you run; to keep the key out of the file, leave `--api-key` out of the context and export the key as `KATES_API_KEY` for the session instead.

A command that changes the file first takes a lock on `~/.kates.yaml.lock`, which stays beside it, and then writes a complete new file in place of the old one. Two commands that change contexts at once therefore do not undo each other's changes, and a save that fails leaves the old file as it was.

### Config File Format

```yaml
current-context: local
contexts:
  default:
    url: http://localhost:8080
    output: table
  local:
    url: http://localhost:30083
    output: table
    api-key: <api-key>
  staging:
    url: https://kates-staging.example.com
    output: table
  ports:
    url: http://localhost:8080
    output: table
    api-key: <api-key>
    key-source: kates-api-key@pbkdf2-sha256:<iterations>:<salt>:<digest>
```

`kates ports` writes the `ports` context. `key-source` marks a key that `kates ports` or `kates deploy` copied from the `kates-api-key` Secret, with a salted PBKDF2-SHA256 digest of that key. Those two commands replace a key only while its `key-source` matches it. A key you set with `kates ctx set`, bring in with `kates ctx import`, or change in the file has no matching `key-source`, and both commands leave it in place.

## Global Flags

| Flag | Short | Description |
|------|-------|-------------|
| `--url` | | Override API URL for this call |
| `--output` | `-o` | Output format: `table` or `json` |
| `--context` | | Use a specific context |
| `--api-key` | | API key for this call, in place of the context's |
| `--plain` | | Disable interactive prompts and fancy UI formatting |
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

If the API is unreachable — or rejects the API key — the line shows the context name, its URL, and `unreachable` instead, and the command still exits `0`.

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

# Kafka alert rules defined in the Kafka namespace
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
 Kafka Cluster Topology — Cluster: krafter  │  Kafka 4.3.1  │  KRaft Mode

  Kubernetes Platform
  Version:   v1.34.11
  Platform:  linux/arm64
  Nodes:     3

  Strimzi Operator
  Version:     1.2.0
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

List the Kafka alert rules defined in PrometheusRule resources — the rule definitions, not whether they are firing. The backend reads every PrometheusRule in its Kafka namespace (`kates.topology.kafka-namespace`, `kafka` by default) and keeps the `critical` and `warning` rules whose names are on a fixed list of Kafka health alerts. Each is printed with its expression, `for` duration and description, critical first.

```bash
# Show every listed rule
kates cluster alerts

# Filter by severity
kates cluster alerts --severity critical
kates cluster alerts --severity warning

# Filter by alert group
kates cluster alerts --group kafka-cluster.krafter.availability
kates cluster alerts --group kafka-cluster.krafter.storage

# JSON output for scripting
kates cluster alerts -o json
```

| Flag | Description |
|------|-------------|
| `--severity` | Filter by severity: `critical` or `warning` |
| `--group` | Filter by alert group (e.g. `kafka-cluster.krafter.availability`, `kafka-cluster.krafter.storage`) |

Group names are scoped to the cluster they cover, so two clusters in one namespace never collide. The `kafka-cluster` chart renders `kafka-cluster.<cluster>.` followed by `availability`, `consumers`, `cruise-control`, `kraft`, `performance`, `records`, `replication` and `storage`, plus `slo` when `alerts.slo.enabled` is set. The `records` group holds recording rules only. The fixed list covers only part of the alerts in the others: nothing from `kraft` or `slo`, and neither `KafkaConsumerGroupLag`, `KafkaUnderMinIsrPartitions` nor `KafkaNodesMissing`, among others. `kubectl get prometheusrule <cluster>-alerts -n kafka -o yaml` shows every rule the chart installed.

The operator's own alerts — including `StrimziOperatorDown` and the certificate-expiry rules — live in the `strimzi-operator.<namespace>` group, in the operator's own namespace (`strimzi-operator` on a default install), which the command does not read.

The exit status counts rules, not alerts. The command exits `1` whenever a `critical` rule on its list is defined, and `0` when none is — including when no PrometheusRule exists at all. `--severity` and `--group` narrow what is printed but not that count, with one exception: in table output, a filter that matches nothing exits `0`. A failed API call exits `1`. The command's own `--help` text says it exits `2` and gives `kafka.cluster` and `kafka.kraft` as group names; it exits `1`, and no group carries those names.

::: {.callout-important}
**Not a health gate**

`kates cluster alerts` never asks Prometheus which alerts are firing. A default install defines four critical rules on its list — `KafkaOfflinePartitions`, `KafkaActiveControllerCount`, `KafkaBrokerDiskUsageCritical` and `KafkaConsumerGroupLagCritical` — so the command exits `1` on a healthy cluster, and a cluster without PrometheusRules exits `0` however unhealthy it is. To gate a pipeline on alerts that are firing, query Prometheus for `ALERTS{alertstate="firing", severity="critical"}` instead.
:::

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

Test commands are the core of Kates. They let you create, monitor, and manage performance test runs against your Kafka cluster. Whether you're running a quick load test to sanity-check throughput or a 25-minute endurance test to catch memory leaks and log-roll spikes, the workflow is always the same: create a test, optionally watch it in real time, then inspect the results. For repeatable, version-controlled test definitions, use `test apply` with YAML scenario files instead of inline flags.

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
| `--type` | Test type (default `LOAD`) |
| `--records` | Number of records |
| `--record-size` | Record payload size in bytes |
| `--producers` | Number of producers. STRESS and CAPACITY start this many; every other type runs one producer whatever the flag says |
| `--consumers` | Accepted, but no test type reads it: LOAD, ENDURANCE and INTEGRITY run one consumer, the other types none |
| `--consumer-group` | Dropped by the backend, which names each consumer group itself |
| `--acks` | Producer acks mode: `0`, `1`, `all` |
| `--topic` | Target topic name |
| `--partitions` | Topic partition count |
| `--replication-factor` | Topic replication factor |
| `--min-isr` | Minimum in-sync replicas |
| `--duration` | Test duration in seconds; the backend fails any run still going after 30 minutes (see below) |
| `--throughput` | Does not limit the rate: it sends `targetThroughput`, which the backend drops (see the callout below) |
| `--fetch-min-bytes` | Dropped by the backend |
| `--fetch-max-wait-ms` | Dropped by the backend |
| `--backend` | Backend engine to use |
| `--wait` | Wait for test completion |

::: {.callout-important}
**`--throughput` does not rate-limit a run**

The backend merges every request with its test type's defaults before it builds the run, and the merge drops `targetThroughput` — the field `--throughput` sends — along with `consumerGroup` and the two fetch settings. A run therefore produces at its type's default rate whatever `--throughput` says: unthrottled for most types, 5,000 records/s for ENDURANCE and 10,000 records/s for ROUND_TRIP on a default install. The rate comes from the API field `throughput` instead — SPIKE and CAPACITY ignore even that — and no `kates test create` flag sets it. A `kates resilience run` file can set it, because its `spec` goes to the API as written (see Resilience, below).
:::

::: {.callout-important}
**No run lasts longer than 30 minutes**

The backend fails any run that is still `RUNNING` 30 minutes after it was created: it stops the run's producers and consumers and marks the run `FAILED` with the error `Timeout: exceeded max duration of 1800000ms`, and `--wait` then exits 1. It checks once a minute, so the run fails between 30 and 31 minutes after creation. The clock starts before the backend creates the topic and starts the run's tasks, while `--duration` counts from the task start, so a run with a `--duration` of 1,800 s is still going when the limit passes and can fail just before it ends; keep `--duration` to 1,700 s or less. A run that ends on its record count is cut off the same way. The default ENDURANCE run sends 10M records at 5,000 records/s, which takes about 33 minutes, so on a default install it fails unless `--records 8500000` or fewer, or `--duration 1700` or less, brings it under the limit.

The limit is the backend setting `kates.engine.max-duration-ms`, 1,800,000 ms by default, and the `kates` chart has no value for it. To allow longer runs, set the environment variable `KATES_ENGINE_MAX_DURATION_MS` through the chart's `extraEnv`. Start from the values the release runs with, and upgrade from a checkout of the version it runs, so the upgrade changes nothing else:

```bash
# Every value the release was installed with — its files and its --set flags
helm get values kates -n kates -o yaml > kates-current.yaml
```

Add the variable to `extraEnv` in `kates-current.yaml`, next to any entries already there:

```yaml
extraEnv:
  - name: KATES_ENGINE_MAX_DURATION_MS
    value: "7200000"   # two hours, in milliseconds
```

```bash
helm upgrade kates charts/kates -n kates -f kates-current.yaml --timeout 8m --wait
```

`kates deploy` upgrades the release from its own values files each time it runs, which drops the entry, so repeat the upgrade after it.
:::

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

Apply a YAML scenario file. Each scenario can carry SLA gates in a `validate` block, which the CLI checks only with `--wait` — see [Scenario Files & SLA Gates](13-scenario-files.md) for the syntax and the exit codes.

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

| Template | Type | What it runs |
|----------|------|--------------|
| `quick-load` | LOAD | 50k records of 1 KiB through one producer and one consumer; gates on P99 ≤ 100 ms and at least 5,000 records/s |
| `production-load` | LOAD | Up to 1M records of 2 KiB with `acks=all`, lz4 and 12 partitions for at most 300 s, through one producer and one consumer; gates on P99 ≤ 50 ms, average ≤ 10 ms and at least 50,000 records/s |
| `stress-test` | STRESS | 16 producers, each sending up to 5M records of 512 B with `acks=1` and snappy to 24 partitions; gates each producer on P99 ≤ 200 ms and at least 100,000 records/s |
| `endurance-soak` | ENDURANCE | 10M records of 1 KiB at 5,000 records/s through one producer and one consumer; gates on P99 ≤ 100 ms and average ≤ 20 ms. The records take about 33 minutes, past the 30-minute limit on any run (see the callout under `test create`), so on a default install the run fails and `kates test apply --wait` exits 1. Set `records` to 8,500,000 or fewer in the exported file to run it |
| `exactly-once` | ROUND_TRIP | 100k records of 256 B with `acks=all` through one producer at 10,000 records/s; gates on P99 ≤ 200 ms. No transactions and no integrity check run, so its loss, ordering and CRC gates have nothing to check |
| `integrity-tx` | INTEGRITY | 200k records of 512 B with `acks=all` and zstd through one producer and one consumer — CRC-checked, idempotent by the Kafka producer's default, not transactional; gates on zero loss, zero out-of-order, zero CRC failures and P99 ≤ 150 ms |
| `spike-test` | SPIKE | One unthrottled producer sending up to 500k records of 1 KiB with `acks=1` for at most 60 s; gates on P99 ≤ 500 ms |
| `ci-gate` | LOAD | 10k records of 512 B with `acks=all` through one producer and one consumer; gates on P99 ≤ 100 ms and at least 1,000 records/s |

`kates test scaffold` prints the CLI's own one-line descriptions, which promise more than the runs deliver: multiple producers for `quick-load`, `production-load`, `integrity-tx` and `spike-test`, a one-hour soak that the 30-minute run limit rules out, transactions and an integrity check for `exactly-once`, transactions for `integrity-tx`, and a zero-error gate for `ci-gate`. The table above says what the backend runs. The backend keeps a file's producer count only for STRESS and CAPACITY, reads `numConsumers` for no type, and drops `targetThroughput` (see the callout under `test create`) and the integrity options `enableIdempotence`, `enableTransactions` and `enableCrc`, so every INTEGRITY run is CRC-checked and none is transactional. The files also declare gates that `kates test apply` does not check: `maxErrorRate` in `production-load`, `endurance-soak`, `spike-test` and `ci-gate`, `maxDuplicatePercent` in `integrity-tx`, and `maxDataLossPercent` in `ci-gate`, whose LOAD run reports no integrity result — see [Scenario Files & SLA Gates](13-scenario-files.md).

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
| `--dry-run` | Validate plan without executing; exits 1 when the verdict is UNSAFE |
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

#### disruption playbook list

```bash
kates disruption playbook list
```

List the built-in playbooks with their category, step count, and description.

#### disruption playbook show

```bash
kates disruption playbook show leader-cascade
kates disruption playbook show leader-cascade -o json > plan.json
```

Show the plan a playbook runs, as the backend resolves it from the playbook's YAML, with the defaults the YAML leaves out filled in. For each step it prints the fault type, the namespace and label selector, the target the YAML names (every matching pod, a broker ID, or the leader of a partition), the fields that size a fault of that type (the grace period of a `POD_DELETE`, the fill percentage of a `DISK_FILL` or `IO_STRESS`, and so on), the chaos duration, the steady-state and observation windows, and whether the step waits for recovery. A step whose `faultSpec` has no `disruptionType` shows as `no disruptionType`, with its `experimentName` as the Litmus experiment it runs on the LitmusChaos backend. Which pods a step hits depends on the cluster at the time; `disruption playbook run --dry-run` shows that.

With `-o json` the command prints the plan as the backend returns it. That is a complete disruption plan, which `kates disruption run --config` accepts, so a saved copy is a starting point for a plan of your own.

#### disruption playbook run

```bash
kates disruption playbook run leader-cascade --dry-run
kates disruption playbook run leader-cascade
```

| Flag | Description |
|------|-------------|
| `--dry-run` | Preview the playbook without injecting any fault; exits 1 when the verdict is UNSAFE |

Run a playbook and wait for its report; the command prints the disruption ID and the final status, or with `-o json` the ID and the report as JSON. With `--dry-run` it fetches the playbook's plan and sends it to the same dry run as `disruption run --dry-run`, which resolves partition leaders, lists the pods each step would hit, and checks the blast radius. It starts nothing. It prints the dry-run result, as JSON with `-o json`, and exits 1 when the verdict is UNSAFE, which is when running the playbook would be refused. The dry run checks RBAC for some fault types only, and reports a missing permission as a step warning, not in the verdict; [Chaos Engineering in Practice](07-chaos-practice.md) lists which.

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

The config file has three parts: a `testRequest`, which is the API's test creation request, so its `spec` takes the API's field names (`numRecords`, `throughput`, `recordSize`) rather than a scenario file's; a `chaosSpec` (experiment name, target namespace and label selector, duration, disruption type); and `steadyStateSec`, the seconds of load before the fault. Leave `steadyStateSec` out and the CLI sends `0`, so the fault is triggered as the load starts.

```yaml
testRequest:
  type: LOAD
  spec:
    numRecords: 180000     # at 500 records/s: 360 s of load
    throughput: 500
    recordSize: 1024
    acks: all

chaosSpec:
  experimentName: kafka-broker-pod-kill
  disruptionType: POD_KILL
  targetNamespace: kafka
  targetLabel: "strimzi.io/component-type=kafka,strimzi.io/broker-role=true"
  chaosDurationSec: 30

steadyStateSec: 30
```

The rate limit keeps the load running across the fault: 180,000 records at 500 records/s take 360 s, while the fault is triggered after `steadyStateSec` (30 s) and lasts `chaosDurationSec` (30 s). An unthrottled run can finish before the fault is triggered, and its results then describe a run the fault never touched. `throughput` is the rate the run honours, and LOAD runs one producer and one consumer whatever `numProducers` says, so it is the whole rate. The selector adds `strimzi.io/broker-role=true` because `strimzi.io/component-type=kafka` alone also matches the KRaft controllers, and the fault could then hit a controller instead of a broker. The example in `kates resilience run --help` has neither the rate limit nor the broker selector; start from this one.

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
kates deploy --yes
```

| Flag | Description |
|---|---|
| `--interactive`, `-i` | Force the configuration wizard. It also opens for a bare `kates deploy` with no flags, when attached to a terminal |
| `--yes`, `-y` | Never prompt — fail instead of asking. Use in scripts and pipelines |
| `--dry-run` | Print the deployment plan and stop before the Helm pipeline runs |
| `--topology` | `isolated` (a namespace per component) or `single` (default: `isolated`) |
| `--namespace` | Target namespace when `--topology single` (default: `kates-stack`) |
| `--ha` | Multi-AZ high availability: replicas 3, `min.insync.replicas` 2, zone spread (default: `true`) |
| `--port-forward`, `-P` | After deploying, run [`kates ports`](#ports): forward every service in the background and make the `ports` context current |
| `--with-schema-registry` | `none`, `apicurio`, or `confluent` (default: `apicurio`) |
| `--with-*` | Per-component toggles — `kates deploy --help` lists them, along with the per-component `*-ns` flags |
| `--operator-scope` | `cluster` (default): one Strimzi operator watching every namespace. `namespace`: one operator per Kafka namespace, so older Kafka lines can run beside the primary under an operator of their own |
| `--strimzi-version` | Operator version to install. Default: the repository pin; `latest` for the newest published. The chart is fetched and read before anything is installed |
| `--strimzi-chart` | A local `strimzi-kafka-operator` tarball for air-gapped mirrors |
| `--kafka-version` | Kafka version of the primary cluster. Default: the newest the selected operator supports; `latest` spells the default |
| `--kafka-name` | Name of the primary Kafka cluster (default: `krafter`) |
| `--with-mirror-maker2` | Deploy MirrorMaker 2 as a loopback mirror of the primary — the chart's shipped shape, enough to exercise connectors, offset syncs, checkpoints and the `kates-mm2` ACLs without a second cluster (default: `false`). Real sources are `kates migrate up --from …` |
| `--mm2-ns` | Namespace for MirrorMaker 2 in the isolated topology (default: `kafka`, where the `kates-mm2` credential lives; elsewhere the credential is copied) |

**The API key.** When the deploy succeeds, `kates deploy` stores the key from the `kates-api-key` Secret in the active CLI context, the current one or the one `--context` names, if that context has no key or holds one that kates copied from the Secret before. A key you set there yourself stays, and `kates deploy` says so; the [Config File Format](#config-file-format) shows how it tells the two apart.

**The wizard.** `kates deploy -i` (or a bare `kates deploy` on a terminal) walks four screens, and nothing is installed until the last one is confirmed:

| Screen | What it asks |
|:---|:---|
| What to deploy | topology, schema registry, HA sizing, and the components — Strimzi, Kafka Connect + PostgreSQL, Kafka UI, MirrorMaker 2, chaos, monitoring, Cert-Manager, Kyverno |
| Versions | the operator scope — *mono cluster* (one Strimzi, one Kafka version, the operator watching every namespace) or *namespace-scoped* (one operator per Kafka namespace); the Strimzi version (the pin first, then the catalogue when reachable); then the Kafka version for the primary, whose options are exactly the chosen operator's window, newest first — with the pinned Strimzi 1.2.0 that is `4.3.1 4.3.0 4.2.1 4.2.0`, and an older operator offers its own, older window |
| Namespaces | one input per selected component (isolated topology only) |
| Review | every choice as it will resolve — operator and where its chart came from, Kafka version and metadata version, window, components, sizing, namespaces — and a Deploy/Cancel confirmation |

Versions are resolved before the first Helm call — the "Resolving Versions" phase prints the operator version and where its chart came from, the Kafka window it supports, the Kafka version chosen (and whether it was defaulted), the metadata version, and what the operator already on the cluster allows: the same version converges; an older one is upgraded after a confirmation that lists the clusters that will roll; a newer one is refused (Strimzi does not downgrade); a Kafka version outside the window is refused with the window and every way forward. Under cluster scope the refusal says why no second operator can help — a cluster-wide operator watches every namespace — and names the legacy provider and namespace scope as the ways out.

```bash
kates deploy --kafka-version 4.2.1                      # a supported version other than the newest
kates deploy --strimzi-version 1.0.1 --kafka-version 4.2.0
kates deploy --operator-scope namespace                 # one operator per Kafka namespace
kates deploy --dry-run --strimzi-version latest          # shows the resolution, installs nothing
```

::: {.callout-warning}
`--dry-run` is not inert. It stops before the Helm pipeline, but the cluster gate, pre-flight introspection, and Kind StorageClass bootstrap all run first — the last of these writes StorageClasses into the cluster. It will not create a Kind cluster, but it is a plan preview, not a read-only mode.
:::

`deploy` works out which cluster it is deploying to before it asks you anything else — there is no point configuring a deployment that has nowhere to go. What happens next depends on what it finds:

| What it finds | What it does |
|---|---|
| One reachable cluster | Uses it, without asking |
| Several reachable clusters | Asks you to pick one |
| No reachable cluster, Docker and kind available | Offers to create a local three-zone kind cluster |
| No reachable cluster, kind missing | Explains how to install kind |
| No reachable cluster, Docker stopped | Asks you to start Docker |
| No Docker at all | Explains both ways forward |

When nothing is reachable, any contexts you do have configured are listed by name rather than treated as absent — a kubeconfig that has gone stale looks nothing like a machine with no cluster, and the difference decides what you do about it.

The kind offer has three further conditions. It is skipped under `--dry-run`, and it stops rather than proceeding when a kind cluster of that name already exists but does not answer (recreating would destroy it) or when the topology config cannot be read from the current directory. Each case prints the command that resolves it.

::: {.callout-note}
`--yes` never guesses. When several clusters are reachable and nothing can be asked, `deploy` fails and tells you to choose with `kubectl config use-context` rather than picking one for you. Selecting a cluster silently is how a deployment lands somewhere it was never meant to go.
:::

The same applies without a terminal. Piped or scripted runs take the flag defaults instead of opening the wizard, and any state that needs an answer becomes an error carrying the command that resolves it.

**See also:** [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md) for first-time setup, and [Deployment Guide](12-deployment.md) for what the stack looks like once it is up.

#### deploy status

Show the current deployment status of all Kates-managed components.

```bash
kates deploy status
```

Expected output:

```text
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

It exits `0` whatever it finds unless you ask otherwise: `--fail-on-error` exits `2` when compatibility checks fail or the cluster cannot be inspected, and `--fail-on-warning` exits `1` on warnings.

#### ports

Port-forward all Kates services to localhost.

```bash
kates ports
```

It first stops every `kubectl port-forward` you are running, including ones it did not start, such as those from `make ports`. It then forwards the API to `localhost:8080`, points the CLI context `ports` at that address, creating it the first time, and makes it the current context. No other context changes. The forwards keep running in the background after the command returns.

A local port that another program already listens on is not forwarded, and the table marks it `[IN USE]`. When that port is the API's, `kates ports` leaves the `ports` context as it is and sends no key, since the program on the port, not the API, would receive it.

It reads the API key from the `kates-api-key` Secret in the namespace where it found the API's Service, and nowhere else, and checks it with a request to `/api/tests/types`, which needs the key; `/api/health` would accept any key. A key the API accepts goes into `ports`, and so does one it cannot check because the API does not answer or fails with a status other than 401 or 403. A key the API rejects does not: `kates ports` says so and leaves the key that `ports` already had. A key you put in `ports` yourself, with `kates ctx set`, `kates ctx import` or an editor, stays whatever the Secret holds; `kates ports` reports whether the API accepts it.

`--context` and `KATES_CONTEXT` do not change where `kates ports` writes, but they still outrank the current context for later commands, so `kates ports` warns when either names another context.

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

### Versions and Operators

What can run here is read from the charts and the cluster, never from a table inside the CLI: each operator chart states the Kafka versions it supports, the live operator Deployment states what it watches, and the Strimzi Helm index states which operator versions exist.

#### versions

```bash
kates versions                       # operators on this cluster (or the pin), their windows, the legacy range
kates versions strimzi [--resolve]   # the catalogue of published operator versions and their status
kates versions kafka --strimzi-version 1.0.1
kates versions --offline             # never touch the network
```

`versions strimzi` marks each version `pinned`, `newer than pinned`, `supported`, or `below floor` (hidden without `--all`); the Kafka window is shown for every chart already cached, and `--resolve` pulls the rest. `versions kafka` prints one operator's window, the newest entry, the CRD API it serves and stores, and the metadata version derived for each entry.

#### operators list

```bash
kates operators list
kates operators list -o json
```

Every Strimzi Cluster Operator on the cluster, discovered from its Deployment: namespace, version, scope (`cluster` or `namespaces`), watched namespaces, Kafka window, and role — the primary's operator owns the CRDs and cluster-scoped RBAC; additional operators are marked adjacent to it (the configuration Strimzi tests) or not. A cluster-wide and a namespaced operator together is flagged: they would reconcile the same namespaces.

---

### Migration Commands

`kates migrate` stands up an old Kafka beside the platform's primary, mirrors it with MirrorMaker 2, proves the records and the consumer offsets arrived, rehearses the cutover and tears everything down — from one pair of versions. It replaces `scripts/test-mm2-migration.sh`, `scripts/mm2-kafka-cli.sh` and `scripts/build-legacy-kafka-image.sh`, and the Makefile `mm2-*` targets now call it.

```bash
kates migrate pairs                          # every old → new pair this cluster can stand up, with the provider each source gets
kates migrate plan --from 2.8.2              # what up would create — nothing changes
kates migrate up   --from 2.8.2 [--to <version>] [-i]
kates migrate up   --from 2.8.2 --from 3.9.1 # two sources, one target, one MirrorMaker 2 release
kates migrate status | verify | cutover | rollback | down [--name m282-431]
kates migrate run  --from 2.8.2 [--keep] [--skip-build] [-o json]    # up → verify → cutover → down, one report
```

`--from` is resolved to a provider: a version the primary's operator supports becomes a Strimzi cluster; 2.x and 3.x become a `legacy-kafka` cluster (ZooKeeper below 3.3.0, the built KRaft image up to 3.6, the official image from 3.7.0); a version below 2.1.0 is refused (KIP-896). `--to` defaults to the primary as it runs, so the migration ends where the backend, Kafka UI and `kates test` already point. The lab is named `m<from>-<to>` with the dots dropped — `m282-431` for 2.8.2 onto a 4.3.1 primary — every release it creates carries `kates.io/lab` labels, and `status`, `cutover` and `down` find it from the cluster — there is no state file. The report keeps the script's eighteen rows (`cluster reachable` … `cutover froze the target`) and exits `1` on any failed one; `-o json` carries them per row.

#### Several sources in one release

`--from` is repeatable on `plan`, `up` and `run`. Each source gets its own **alias** derived from its version (`src282`, `src391` — lowercase and dash-free, because a dot is MirrorMaker's own separator between alias and topic), and from that alias its own namespace (`kafka-m282-391-431-src282`), release, credential, corpus topic (`kates.orders.src282`) and entry in the generated `mirrors:` list — one MirrorMaker 2 release reading both. The lab is named for all of them (`m282-391-431`); with one `--from` nothing changes: the lab is `m282-431`, the alias is `source`, the release is `m282-431-src`.

Every per-source assertion becomes its own leg of the report, named by the alias (`record count [src391]`), while the rows about the release itself — `MirrorMaker 2 installed`, `CR Ready`, `connectors RUNNING`, `cutover applied` — stay single. `status` prints one `source <alias>` block per leg and `down` removes every source it finds under the lab's label.

Two combinations the chart refuses at render time are refused by the CLI first, naming both `--from` values and touching nothing:

- **the same alias twice** (`--from 2.8.2 --from 2.8.2`) — an alias names the replicated topic prefix, the offset-syncs topic and the checkpoints topic, so two sources sharing one interleave into a single set of target topics without any error;
- **an identity fan-in over one topic name** (`--from 2.8.2 --from 3.9.1 --topics orders`) — under the identity policy topic names are kept, so both legs would write `orders` on the target. The refusal names the three ways out: `--policy default` (each source's topics land as `<alias>.<topic>`), dropping `--topics` so the lab gives each source its own corpus, or one lab per source.

#### Read-only sources

`--read-only-source` (on `plan`, `up`, `run` and `migrate mirror deploy`) sets `readOnlySource: true` on every mirror, which the chart turns into `offset-syncs.topic.location: target` ([KIP-716](https://cwiki.apache.org/confluence/display/KAFKA/KIP-716:+Allow+configuring+the+location+of+the+offset-syncs+topic+with+MirrorMaker2)) on both connectors and into the matching target ACL. It is **off by default**, and the difference is what the source principal must be granted:

| `--read-only-source` | `offset-syncs.topic.location` | The source principal needs |
|:---|:---|:---|
| off (default) | `source` — Kafka's own | `Read` + `Describe` on the mirrored topics, **and** `Create` + `Write` + `Describe` on `mm2-offset-syncs.*` |
| on | `target` | `Read` + `Describe`, and nothing else — the mirror writes nothing to the source |

`plan` prints both lines under **offset-syncs**, and the `run` report header carries the same sentence. The lab's own `legacy-kafka` source is a cluster the CLI owns both ends of, so the default stands there; the flag is for `--from-bootstrap` against a cluster you do not own. Flipping it on a **running** mirror restarts translation from scratch: the new offset-syncs topic starts empty, so checkpoints regress until it catches up.

The building blocks the front door is made of are commands too: `migrate image build`, `migrate source deploy|status|remove`, `migrate mirror deploy|status|cutover|rollback|remove`, `migrate target topics|offsets <topic>|groups|group <id>`.

::: {.callout-note}
In this version every source is `legacy-kafka`: a Strimzi-operated source (an in-window version, or a dropped line under its own operator with `--operator-scope namespace`) is described by `plan` and refused by `up` with "not yet implemented — use --source-provider legacy". That is true of a `--from` in a set as much as on its own: a fan-in that includes a Strimzi source stays `plan`-only. `--to` other than the primary's version is `plan`-only for the same reason.
:::

**See also:** [Migrating Kafka 2.x to 4.x](../tutorials/11-migrating-kafka-2x-to-4x.md), [Migrating Kafka 3.x to 4.x](../tutorials/12-migrating-kafka-3x-to-4x.md), the [MirrorMaker 2 runbook](../mirror-maker2-runbook.md).

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

Probe ACL rules for a specific user to verify least-privilege access. `--user` names the Kafka user and is required; without it the command prints an example and exits `1`.

```bash
kates security auth-test --user kafka-ui
kates sec auth --user kates-backend
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

Save current security posture as baseline for drift detection. The save needs `--save`: without it the command saves nothing, prints how to use the flag, and exits `1`. The backend keeps one baseline in its database, and each save replaces it.

```bash
kates security baseline --save
kates sec base --save
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

The parent of the Kafka client commands below; on its own it lists them. The interactive explorer is `kates kafka tui`.

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

```text
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
kates kafka alter-topic <name> --config <key>=<value>
kates kafka alter-topic my-events --config retention.ms=604800000
kates kafka alter-topic my-events --config retention.ms=604800000 --config cleanup.policy=compact
```

`--config` takes a `key=value` entry and repeats for each config you set; at least one is required. `--dry-run` prints the request JSON instead of sending it.

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

#### test baseline

`test baseline` marks a specific test run as the performance reference point for its test type, and `report regression` compares a later run against it to show exactly where performance changed. Baselines work hand-in-hand with trend analysis — trends show long-term drift, baselines catch acute regressions. The typical workflow is: run a comprehensive test on a known-good configuration, set it as the baseline for its type, then compare every subsequent run against it.

```bash
kates test baseline set <run-id>
kates test baseline list
kates test baseline show <type>
kates test baseline unset <type>

kates report regression <run-id>
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

Snapshot commands record a small picture of your Kafka cluster at a point in time: the broker count, the names of its topics and consumer groups, and the name of the current context. The use case is before/after comparison: take a snapshot before a change, make the change, take another snapshot, then diff them. The diff shows the three counts side by side and lists the topics and groups added or removed; configuration, partition layout and ACLs are not recorded. Snapshots are JSON files under `~/.kates/snapshots/` on the machine that ran the CLI, one per name — a second `create` with the same name replaces the first.

::: {.callout-warning}
`kates snapshot create` does not fail when the API does. It skips each API call that fails and records zero for what that call would have returned, so with the API unreachable or the API key missing it saves a snapshot of zero brokers, topics and groups and exits `0`. Check the counts it prints before you rely on it.
:::

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

**See also:** [Upgrade Playbook](18-upgrade-playbook.md), whose pre-upgrade snapshot is a Velero backup — a `kates snapshot` is not a backup.

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

`-o` is a global flag with two values, `table` and `json`, and JSON support varies by command. The commands whose sections in this chapter show `-o json` — `test list`, `report show`, `cluster check`, `cluster topology`, `cluster alerts`, `security audit`, `operators list` and `migrate run` — print JSON, and so do some others, such as `test get` and `cluster info`:

```bash
# Table output (default) — human-readable with colors
kates test list -o table

# JSON output — structured, machine-readable
kates test list -o json
```

Many commands have no JSON form and print the same output whatever `-o` says, among them `status`, `ctx show`, `snapshot`, `profile`, `kyverno`, `gate`, `advisor`, `explain`, `benchmark`, `tune`, `test compare`, `test summary`, `webhook list`, `changelog` and the full-screen views (`dashboard`, `top`, `lab`). Check a command's output before you pipe it into `jq`.

Output degrades automatically. When stdout is not a terminal — a pipe, a redirect, a CI log — color codes are stripped, refreshing displays become append-only lines, and full-screen TUIs refuse with a plain-text explanation instead of launching. `NO_COLOR` and `TERM=dumb` are honored; `--plain` (or `KATES_PLAIN=1`) forces the fully plain, pure-ASCII form; `KATES_ASCII=1` keeps colors but swaps glyphs for ASCII.

## Exit Codes

For most commands the exit code is a contract: `0` means the thing you asked for happened.

| Code | Meaning |
|------|---------|
| `0` | The requested operation completed — a `--wait` test finished successfully, a deploy applied, a deletion was confirmed and performed |
| `1` | The operation failed, a followed test finished `FAILED`, the connection was lost mid-follow (outcome unknown), or a confirmation was declined or could not be asked |
| `2` | `kates detect --fail-on-error` found failing compatibility checks or could not inspect the cluster; `kates auto` could not inspect a cluster it reached (an unreachable cluster exits `1`) |

Consequences worth knowing in scripts:

- `kates test create --wait` and `kates test watch` exit `1` when the test itself fails — not just when the request fails.
- `kates cluster alerts` exits `1` wherever a critical alert rule is defined, firing or not, which on a default install means always — see its section above.
- A declined confirmation exits `1` in `deploy`, `kafka delete-topic`, `kyverno apply` and `migrate`. A script that forgot `--yes` fails loudly instead of reporting success for work it did not do.
- Those confirmations are never answered implicitly. With no terminal attached, the command fails and tells you to pass `--yes`, rather than assuming either answer.

::: {.callout-warning}
**Commands that exit 0 on failure**

Some commands report a failure on screen and still exit `0`, so a script cannot rely on their status. Among them:

- `kates clean` when you decline its confirmation: it prints `Cancelled.` Unattended runs pass `--force`.
- `kates ctx use`, `kates ctx delete` and `kates ctx export --name` with a context that does not exist, `kates ctx import` with a file it cannot read or parse, and a mistyped subcommand under a command group — `kates ctx list`, for instance — which prints the group's help.
- `kates status` when the API does not answer or rejects the API key: it prints `unreachable`.
- `kates ports` when it finds no Kates services, some forwards fail, or the API rejects a key it checks.
- `kates snapshot create` when the API fails: it saves a snapshot of zeros.
- `kates doctor`, whatever its checks find, and `kates cluster check` on `WARNING` and `CRITICAL` alike.
- `kates security audit` and `kates security tls-inspect` when the backend reports that the check itself failed.
- `kates detect` on failing checks or a cluster it cannot inspect, unless you pass `--fail-on-error` (`--fail-on-warning` reacts to warnings only); with it, still when it cannot read its `--values` file.
- `kates kyverno status`, `violations`, `enforce` and `audit` when Kyverno is missing or the change fails.
- `kates advisor` when the run or its report is not found.
:::

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
- `-o table` is for humans and `-o json` for scripts, on the commands that have a JSON form, such as `test list`, `report show` and `cluster check`; many commands have none, and not every command's exit code can gate a pipeline.
- When you can't remember a command, the CLI documents itself: `kates tldr` for a cheatsheet, `kates docs` for man-style detail, and shell completion for everything in between.

Most of these commands talk to the Kates HTTP API, and [REST API Reference](11-api-reference.md) documents those endpoints for when a script or integration needs to skip the CLI. Others work without it, among them: `deploy`, `clean`, `detect`, `auto`, `ports`, `kyverno`, `migrate`, `versions`, `operators`, `kafka connect`, `doctor dns` and `doctor network` drive `kubectl` and `helm` against your current Kubernetes context; `ctx`, `snapshot list`, `snapshot diff`, `profile list` and `profile compare` read files in your home directory; and `cost estimate`, `tldr`, `docs` and `completion` need neither.
