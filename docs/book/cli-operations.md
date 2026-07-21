# Operations CLI Reference

::: {.callout-note appearance="simple"}
**Scope**: the CLI's operational command families — disruptions, chaos experiment history, resilience, schedules, observability, the interactive Lab, and deployment & lifecycle. Installation, configuration, global flags, and the everyday health, cluster, test, report, and trend commands live in [CLI Reference](10-cli-reference.md); security and analysis tooling lives in [Security & Analysis CLI Reference](cli-security-analysis.md).
:::

Reading state is safe; changing it deserves a reference of its own. The commands in this chapter inject failures, schedule recurring runs, stream live metrics, open the interactive Lab, and deploy or upgrade the stack — each one changes what the cluster is doing, so every section spells out what the command touches and how to undo it.

## Commands

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
| `--port-forward`, `-P` | After deploying, hold the terminal forwarding every service until Ctrl+C |
| `--with-schema-registry` | `none`, `apicurio`, or `confluent` (default: `apicurio`) |
| `--with-*` | Per-component toggles — `kates deploy --help` lists them, along with the per-component `*-ns` flags |

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

Aliases: `pre-flight-cluster`, `cluster-check`

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

