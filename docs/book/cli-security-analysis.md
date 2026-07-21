# Security & Analysis CLI Reference

::: {.callout-note appearance="simple"}
**Scope**: the CLI's security, Kyverno policy, Kafka client, analysis & optimization, tuning, profile, cost, snapshot, flow, badge, webhook, and developer command families. Installation, configuration, global flags, and the everyday commands live in [CLI Reference](10-cli-reference.md); operational commands live in [Operations CLI Reference](cli-operations.md).
:::

This chapter collects the inspection and hardening half of the CLI: auditing the cluster's security posture, enforcing policies, talking to Kafka directly, analyzing and tuning performance, and the developer tooling that rounds out the toolbox.

## Commands

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

#### test baseline

The baseline commands set a specific test run as the performance reference point for future regression detection. Once a baseline is set, `kates report regression <id>` compares any new test against it and shows exactly where performance has changed. Baselines work hand-in-hand with trend analysis — trends show long-term drift, baselines catch acute regressions. The typical workflow is: run a comprehensive test on a known-good configuration, set it as baseline, then compare every subsequent run against it.

```bash
kates test baseline set <id>       # mark a run as the baseline for its type
kates test baseline show <type>    # show the current baseline for a test type
kates test baseline list           # list all configured baselines
kates test baseline unset <type>   # remove the baseline for a test type
kates report regression <id>       # compare a run against its type's baseline
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

#### plugin

Manage CLI plugins. Any executable named `kates-<name>` placed in `~/.kates/plugins/` or on your `PATH` is discovered automatically and becomes callable as `kates <name>`; `plugin list` shows everything the CLI has found and where.

```bash
kates plugin list
```

#### theme

Preview the CLI's built-in color palettes. The display is informational — palettes are not yet configurable.

```bash
kates theme list
kates theme preview <name>
```

