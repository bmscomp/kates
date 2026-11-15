# Tutorial 1: Getting Started with Kates

This tutorial walks you through deploying the Kates stack, running your first performance test, and understanding the results.

## Step 1: Install the Tools and the CLI

You need Docker, running, with about 16 GB of RAM available to it, plus Kind, kubectl, Helm and the `kates` CLI. Homebrew installs the last four on macOS and on Linux:

```bash
brew install kind kubectl helm bmscomp/tap/kates
kates version
```

For Docker, use Docker Desktop, OrbStack or Colima on macOS, and Docker Engine on Linux. The `kates` formula also installs cosign, which `kates deploy` uses to check the signatures of the charts it pulls.

Without Homebrew on Linux, install Docker Engine, Kind, kubectl and Helm from their own install pages, then the CLI with the release installer, which checks the download against the release's checksums:

```bash
curl -fsSL https://raw.githubusercontent.com/bmscomp/kates/main/scripts/install-kates.sh | bash
```

You don't need a clone of the repository: a released `kates` installs the charts its release published. Building from source is for working on Kates itself; [Building from source](../../README.md#building-from-source) covers it.

## Step 2: Deploy the Stack

```bash
kates deploy --topology isolated
```

With no cluster reachable, `kates deploy` offers to create [`panda`](../book/appendix-a-glossary.md#gl-panda), a three-zone Kind cluster, and switches kubectl to it. Accept, and it then installs Strimzi, Kafka, monitoring, LitmusChaos and the Kates API. On a fresh machine this takes 5–10 minutes, most of it pulling images. When a cluster is already reachable, it deploys into kubectl's current context instead, so check `kubectl config current-context` first.

`--topology isolated` puts each component in its own namespace (`kafka`, `kates`, `monitoring`, `litmus`), which the rest of this tutorial uses. `--topology single` puts the whole stack in one `kates-stack` namespace instead, which is fine for a scratch cluster but means every `-n <namespace>` below needs changing.

> [!TIP]
> If `kates deploy` fails with image pull errors, check your internet connection and Docker disk space (`docker system df`). Kind needs at least 20GB of free disk space. After you fix the cause, run `kates deploy` again: it skips or upgrades what is already installed.

Verify everything is running:

```bash
kubectl get pods -n kafka
kubectl get pods -n kates
```

All pods should be `Running`. The Kates backend exposes a REST API that the CLI uses for all commands.

## Step 3: Connect the CLI

```bash
kates ports
```

`kates ports` forwards the Kates API to `http://localhost:8080` and the other services to their ports, in the background. It writes a CLI context named `ports`, with the URL and the API key from the `kates-api-key` Secret, and makes it current.

## Step 4: Verify Connectivity

```bash
kates health
```

Expected output:

```
  Kates Health Check
  ─────────────────────
  Engine      ✅ UP
  Kafka       ✅ Connected (3 brokers)
  API         ✅ Responsive
```

> [!TIP]
> If the health check fails, verify all pods are running with `kubectl get pods -A` and check the Kates backend logs with `kubectl logs -n kates -l app.kubernetes.io/name=kates`. The most common cause is the backend pod not being ready yet — wait 30 seconds and retry.

Explore the cluster:

```bash
# View cluster metadata
kates cluster

# List existing topics
kates cluster topics

# Full dashboard
kates dashboard
```

## Step 5: Run Your First Test

Let's run a simple LOAD test — 100,000 messages with default settings. This sends messages to a Kafka topic at maximum speed and measures how fast your cluster can process them:

```bash
kates test create --type LOAD --records 100000 --wait
```

The `--wait` flag makes the CLI poll until the test completes. You'll see:

```
  ◉ Test created
  ID         a1b2c3d4-...
  Type       LOAD
  Status     RUNNING

  ⏳ Waiting for completion...
  Progress: 25,000 / 100,000 (25%)
  Progress: 50,000 / 100,000 (50%)
  Progress: 75,000 / 100,000 (75%)
  Progress: 100,000 / 100,000 (100%)

  ✅ Test completed
```

## Step 6: View the Results

```bash
# List your tests
kates test list

# Get detailed results (replace with your actual ID)
kates test get <id>
```

The output shows:

```
  Test Run: a1b2c3d4
  ──────────────────
  Type       LOAD
  Status     DONE ✅
  Created    2026-02-15 20:00:00

  Results (1 phase)
  ┌─────────┬────────┬─────────┬────────────┬──────────┬──────────┐
  │ Phase   │ Status │ Records │ Throughput │ Avg Lat. │ P99 Lat. │
  ├─────────┼────────┼─────────┼────────────┼──────────┼──────────┤
  │ default │ DONE   │ 100,000 │ 45,230/s   │ 4.12ms   │ 12.34ms  │
  └─────────┴────────┴─────────┴────────────┴──────────┴──────────┘
```

## Step 7: View the Report

```bash
kates report show <id>
```

The report includes:
- **Summary** — aggregate throughput and latency
- **Cluster snapshot** — broker count, topic count at test time
- **Broker metrics** — per-broker load distribution

## Step 8: Export the Data

`export` writes to stdout, so redirect it to a file. The formats are `csv`,
`junit`, `heatmap`, `heatmap-csv`, `md` and `html`:

```bash
# CSV (spreadsheet analysis) — the default format
kates report export <id> --format csv > report.csv

# Heatmap (Grafana visualization)
kates report export <id> --format heatmap > heatmap.json

# JUnit (CI test reporting)
kates report export <id> --format junit > report.xml
```

For the report as JSON, ask `show` for it instead: `kates report show <id> -o json`.

## Step 9: Run a Test with Consumers

Let's add consumers to measure end-to-end behavior:

```bash
kates test create --type LOAD \
  --records 100000 \
  --producers 2 \
  --consumers 2 \
  --record-size 2048 \
  --acks all \
  --wait
```

## Step 10: Compare Results

Compare your two test runs:

```bash
kates report diff <id1> <id2>
```

This highlights differences in throughput, latency, and error rates between the runs.

## What's Next?

- [Tutorial 2: Running Every Test Type](02-all-test-types.md) — learn all 8 test types
- [Tutorial 3: Chaos Engineering](03-chaos-engineering.md) — inject failures
- [Tutorial 4: Data Integrity](04-integrity-under-fire.md) — verify zero data loss
