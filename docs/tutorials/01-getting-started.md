# Tutorial 1: Getting Started with Kates

This tutorial walks you through deploying the Kates stack, running your first performance test, and understanding the results.

## Step 1: Deploy the Infrastructure

If you haven't already deployed the cluster, run:

```bash
make all
```

This creates the Kind cluster, pulls all container images, and deploys Kafka, monitoring, and LitmusChaos.

Verify everything is running:

```bash
make status
```

Expected output: all pods across `kafka`, `monitoring`, and `litmus` namespaces should be `Running`.

## Step 2: Deploy Kates

```bash
make kates
```

This builds the Kates Quarkus application, creates a Docker image, loads it into Kind, and deploys it to the `kates` namespace.

Verify:

```bash
kubectl get pods -n kates
```

You should see the Kates pod running.

## Step 3: Install and Configure the CLI

```bash
# Build and install
make cli-install

# Create a context
kates ctx set local --url http://localhost:30083
kates ctx use local
```

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

Let's run a simple LOAD test — 100,000 messages with default settings:

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

```bash
# JSON (programmatic consumption)
kates report export <id> --format json -o report.json

# CSV (spreadsheet analysis)
kates report export <id> --format csv -o report.csv

# Heatmap (Grafana visualization)
kates report export <id> --format heatmap -o heatmap.json
```

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
