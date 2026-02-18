# Tutorial: Your First Performance Test

This tutorial takes you from a running Kates instance to your first completed LOAD test in under 10 minutes. By the end, you will have submitted a test, watched it complete, and read the results.

## Prerequisites

- A running Kates instance (either `./mvnw quarkus:dev` locally or deployed on Kubernetes)
- A reachable Kafka cluster (verify with `curl http://localhost:8080/api/health`)
- `curl` and `jq` installed

## Step 1: Verify Connectivity

Before running any test, confirm that Kates can reach your Kafka cluster:

```bash
curl -s http://localhost:8080/api/health | jq
```

You should see:

```json
{
  "status": "UP",
  "kafka": { "status": "UP" },
  "engine": { "activeBackend": "native" }
}
```

If `kafka.status` is `DOWN`, check your `kafka.bootstrap.servers` configuration. The health endpoint tells you exactly what is wrong — the most common issue is that the bootstrap address does not match the Kafka service name in Kubernetes.

## Step 2: Submit a LOAD Test

A LOAD test sends a fixed number of messages at a steady throughput and measures the performance. Start with modest settings:

```bash
curl -X POST http://localhost:8080/api/tests \
  -H 'Content-Type: application/json' \
  -d '{
    "type": "LOAD",
    "spec": {
      "topic": "tutorial-load-test",
      "numRecords": 100000,
      "throughput": 10000,
      "recordSize": 1024,
      "partitions": 6,
      "replicationFactor": 3,
      "acks": "all"
    }
  }' | jq
```

This sends 100,000 messages of 1KB each at 10,000 messages/second. At this rate, the test completes in about 10 seconds plus warmup time.

The response includes a `runId`:

```json
{
  "runId": "a1b2c3d4-...",
  "status": "RUNNING",
  "type": "LOAD"
}
```

## Step 3: Wait for Completion

Poll the test status until it transitions to `DONE`:

```bash
# Replace with your actual runId
RUN_ID="a1b2c3d4-..."
curl -s http://localhost:8080/api/tests/$RUN_ID | jq '.status'
```

Alternatively, use a simple polling loop:

```bash
while true; do
  STATUS=$(curl -s http://localhost:8080/api/tests/$RUN_ID | jq -r '.status')
  echo "Status: $STATUS"
  if [ "$STATUS" = "DONE" ] || [ "$STATUS" = "ERROR" ]; then break; fi
  sleep 2
done
```

## Step 4: Read the Results

Once the test is `DONE`, fetch the full results:

```bash
curl -s http://localhost:8080/api/tests/$RUN_ID | jq
```

The response contains the test specification and an array of `results`. Each result represents one producer or consumer task:

```json
{
  "runId": "a1b2c3d4-...",
  "status": "DONE",
  "type": "LOAD",
  "results": [
    {
      "taskId": "produce-1",
      "recordsSent": 50000,
      "throughputRecPerSec": 10050.5,
      "throughputMBPerSec": 9.81,
      "avgLatencyMs": 3.2,
      "p50LatencyMs": 2.0,
      "p95LatencyMs": 8.5,
      "p99LatencyMs": 15.3,
      "maxLatencyMs": 45.0,
      "error": null
    },
    {
      "taskId": "produce-2",
      "recordsSent": 50000,
      "throughputRecPerSec": 10012.3,
      "..."
    }
  ]
}
```

## Step 5: Interpret the Results

Here is what each metric tells you:

| Metric | Value | What It Means |
|--------|-------|---------------|
| `throughputRecPerSec` | 10,050 | The cluster sustained the target throughput of 10,000 msg/sec — this is healthy |
| `avgLatencyMs` | 3.2ms | Average produce latency is low — the cluster is not under pressure |
| `p99LatencyMs` | 15.3ms | The slowest 1% of requests took 15ms — still very fast |
| `maxLatencyMs` | 45ms | The single slowest request took 45ms — likely a GC pause or network hiccup, nothing to worry about at this level |
| `error` | null | No errors — all messages were sent successfully |

**What "good" looks like:** For a LOAD test at moderate throughput (10K msg/sec), you expect throughput near the target, average latency under 10ms, P99 under 50ms, and zero errors. If your P99 is above 100ms or your error rate is non-zero at this low throughput, something is misconfigured — check broker disk I/O, network connectivity, and `acks` settings.

## Step 6: Export as CSV (Optional)

To export the results for spreadsheet analysis:

```bash
curl -s http://localhost:8080/api/tests/$RUN_ID/export?format=csv > results.csv
```

Open `results.csv` in Excel or Google Sheets to create charts and compare against baseline results.

## What to Try Next

Now that you have your first test working:

1. **Increase throughput** — try `throughput: 50000` or `throughput: 100000` to see where latency starts increasing
2. **Run a STRESS test** — this automatically ramps throughput across 5 steps to find your cluster's saturation point ([Tutorial: Stress Test Saturation Curve](stress-test-saturation-curve.md))
3. **Run a disruption test** — kill a broker during a load test to see how resilience holds up ([Tutorial: Your First Disruption Test](first-disruption-test.md))
