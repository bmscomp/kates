# Chapter 11: REST API Reference

## Introduction

The Kates backend exposes a RESTful API that the CLI and other clients use to manage tests, reports, and disruptions. Every action available in the `kates` CLI maps directly to a REST call — the CLI is a thin wrapper around this API. This means that anything you can do interactively from the command line can be automated via HTTP requests, making the REST API the foundation for scripting, CI/CD integration, and custom dashboards.

**When should you choose the REST API over the CLI or gRPC?** Use the REST API when you need to integrate Kates into shell scripts, automation pipelines, or monitoring systems that work best with JSON over HTTP. It is ideal for `curl`-based workflows, webhook integrations, and any tool that speaks HTTP natively. The API is human-readable and easy to debug — every request and response is plain JSON, so you can inspect traffic with standard tools like `curl`, `httpie`, or browser dev-tools.

If you need strongly typed clients, streaming results, or high-throughput programmatic access from Go, Java, or Python services, consider the [gRPC API](16-grpc-api.md) instead. If you prefer an interactive experience with formatted output, the [CLI](10-cli-reference.md) is the best choice. All three interfaces share the same backend service layer, so results are identical regardless of which you choose.

---

## Authentication

Kates does **not** enforce authentication by default when running inside a Kubernetes cluster with port-forwarding. However, when exposed externally (e.g., via an Ingress or LoadBalancer), you should secure access.

If token-based authentication is enabled (via the `kates.auth.enabled=true` configuration), include the token in every request:

```bash
curl -H "Authorization: Bearer <your-token>" http://localhost:30083/api/health
```

### Common Request Headers

| Header | Value | Required | Description |
|--------|-------|:---:|-------------|
| `Content-Type` | `application/json` | ✅ (POST/PUT) | Request body format |
| `Accept` | `application/json` | | Response format (default) |
| `Authorization` | `Bearer <token>` | When auth enabled | Authentication token |
| `X-Request-Id` | UUID string | | Correlation ID for tracing |

> **Tip:** When running locally via `kubectl port-forward`, no authentication headers are needed. Kubernetes RBAC controlling access to the port-forward command is sufficient.

---

## Base URL

```
http://localhost:30083
```

When running in-cluster, use the Kubernetes service DNS name: `http://kates.kates.svc.cluster.local:8080`

---

## Endpoints

### Health & System

#### GET /api/health

System health check including Kafka connectivity and engine status.

**Response:** `200 OK`

```json
{
  "status": "UP",
  "kafka": {
    "connected": true,
    "bootstrapServers": "krafter-kafka-bootstrap.kafka.svc:9092",
    "clusterId": "abc123",
    "brokerCount": 3
  },
  "engine": { "activeTests": 2, "totalCompleted": 45 }
}
```

When Kafka is unreachable, the response status code is `503 Service Unavailable` with `"status": "DOWN"` and `"kafka.connected": false`.

---

### Test Management

#### POST /api/tests

Create and start a new test run.

**Request Body:**

```json
{
  "testType": "LOAD",
  "backend": "native",
  "spec": {
    "records": 100000,
    "recordSizeBytes": 1024,
    "producers": 4,
    "consumers": 2,
    "acks": "all",
    "topic": "perf-test",
    "partitions": 3,
    "replicationFactor": 3,
    "minInsyncReplicas": 2,
    "durationSeconds": 120,
    "throughput": -1,
    "consumerGroup": "perf-cg",
    "fetchMinBytes": 1,
    "fetchMaxWaitMs": 500
  }
}
```

| Field | Type | Required | Description |
|-------|------|:---:|-------------|
| `testType` | String | ✅ | One of: LOAD, STRESS, SPIKE, ENDURANCE, VOLUME, CAPACITY, ROUND_TRIP, INTEGRITY |
| `backend` | String | | Backend engine (default: "native") |
| `spec` | Object | | Test specification overrides |

**Response:** `201 Created`

```json
{
  "id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "testType": "LOAD",
  "status": "PENDING",
  "backend": "native",
  "spec": {
    "records": 100000, "recordSizeBytes": 1024, "producers": 4, "consumers": 2,
    "acks": "all", "topic": "perf-test", "partitions": 3, "replicationFactor": 3,
    "minInsyncReplicas": 2, "durationSeconds": 120, "throughput": -1
  },
  "createdAt": "2026-02-15T20:00:00Z",
  "updatedAt": "2026-02-15T20:00:00Z"
}
```

#### GET /api/tests

List test runs with pagination and filtering.

**Query Parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `page` | int | 0 | Page number (0-indexed) |
| `size` | int | 20 | Page size |
| `type` | String | | Filter by test type |
| `status` | String | | Filter by status |

**Response:** `200 OK`

```json
{
  "items": [
    {
      "id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
      "testType": "LOAD",
      "status": "COMPLETED",
      "createdAt": "2026-02-15T20:00:00Z",
      "completedAt": "2026-02-15T20:02:05Z",
      "summary": {
        "recordsSent": 100000,
        "throughputRecordsPerSec": 8412.7,
        "avgLatencyMs": 2.4,
        "p99LatencyMs": 12.3
      }
    },
    {
      "id": "b2c3d4e5-f6a7-8901-bcde-f12345678901",
      "testType": "STRESS",
      "status": "RUNNING",
      "createdAt": "2026-02-15T20:05:00Z",
      "completedAt": null,
      "summary": null
    }
  ],
  "page": 0, "size": 20, "totalItems": 45, "totalPages": 3
}
```

#### GET /api/tests/{id}

Get full details of a test run including results, integrity data, and timeline events.

**Response:** `200 OK`

```json
{
  "id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "testType": "LOAD",
  "status": "COMPLETED",
  "backend": "native",
  "spec": { "records": 100000, "recordSizeBytes": 1024, "producers": 4, "consumers": 2, "acks": "all", "topic": "perf-test" },
  "results": [
    {
      "phaseName": "ramp-up", "recordsSent": 10000,
      "throughputRecordsPerSec": 5234.1, "throughputMbPerSec": 5.1,
      "avgLatencyMs": 4.8, "p50LatencyMs": 3.2, "p95LatencyMs": 9.6, "p99LatencyMs": 18.4, "maxLatencyMs": 45.2
    },
    {
      "phaseName": "steady-state", "recordsSent": 90000,
      "throughputRecordsPerSec": 8412.7, "throughputMbPerSec": 8.2,
      "avgLatencyMs": 2.4, "p50LatencyMs": 1.8, "p95LatencyMs": 5.6, "p99LatencyMs": 12.3, "maxLatencyMs": 34.1
    }
  ],
  "integrity": { "totalProduced": 100000, "totalConsumed": 100000, "duplicates": 0, "lost": 0, "outOfOrder": 0, "verified": true },
  "timeline": [
    { "timestamp": "2026-02-15T20:00:00Z", "event": "TEST_STARTED", "detail": "Initializing producers" },
    { "timestamp": "2026-02-15T20:00:01Z", "event": "PHASE_STARTED", "detail": "ramp-up" },
    { "timestamp": "2026-02-15T20:02:05Z", "event": "TEST_COMPLETED", "detail": "All phases finished" }
  ],
  "createdAt": "2026-02-15T20:00:00Z",
  "completedAt": "2026-02-15T20:02:05Z"
}
```

#### DELETE /api/tests/{id}

Stop and delete a test run. If the test is currently running, it is cancelled before deletion.

**Response:** `204 No Content` on success. Returns `404 Not Found` if the test ID does not exist.

---

### Reports

#### GET /api/tests/{id}/report

Get the full test report with cluster snapshot, broker metrics, and SLA verdict.

```json
{
  "testRun": { "id": "a1b2c3d4-...", "testType": "LOAD", "status": "COMPLETED" },
  "clusterId": "abc123",
  "clusterSnapshot": { "brokerCount": 3, "topicCount": 12, "totalPartitions": 36 },
  "brokerMetrics": [
    {
      "brokerId": 0, "host": "krafter-kafka-0.kafka.svc",
      "bytesInPerSec": 8523412.5, "messagesInPerSec": 8412.7,
      "activeControllerCount": 1, "underReplicatedPartitions": 0
    }
  ],
  "slaVerdict": {
    "passed": true,
    "checks": [
      { "metric": "p99LatencyMs", "threshold": 500, "actual": 12.3, "result": "PASS" },
      { "metric": "throughputRecordsPerSec", "threshold": 5000, "actual": 8412.7, "result": "PASS" },
      { "metric": "errorRate", "threshold": 0.01, "actual": 0.0, "result": "PASS" }
    ]
  },
  "summary": {
    "totalRecordsSent": 100000, "totalRecordsConsumed": 100000,
    "avgThroughputRecordsPerSec": 8412.7, "avgLatencyMs": 2.4, "p99LatencyMs": 12.3,
    "totalDurationMs": 125000, "dataIntegrityVerified": true
  },
  "generatedAt": "2026-02-15T20:05:00Z"
}
```

#### GET /api/tests/{id}/report/csv

Export report as CSV. **Response:** `200 OK` with `Content-Type: text/csv`

```
phase,records_sent,throughput_rps,throughput_mbps,avg_latency_ms,p50_ms,p95_ms,p99_ms,max_ms
ramp-up,10000,5234.1,5.1,4.8,3.2,9.6,18.4,45.2
steady-state,90000,8412.7,8.2,2.4,1.8,5.6,12.3,34.1
```

#### GET /api/tests/{id}/report/junit

Export report as JUnit XML for CI/CD integration. **Response:** `200 OK` with `Content-Type: application/xml`

```xml
<?xml version="1.0" encoding="UTF-8"?>
<testsuite name="kates-load-test" tests="3" failures="0" time="125.0">
  <testcase name="p99LatencyMs <= 500ms" time="0.001"/>
  <testcase name="throughputRecordsPerSec >= 5000" time="0.001"/>
  <testcase name="errorRate <= 0.01" time="0.001"/>
</testsuite>
```

#### GET /api/tests/{id}/report/heatmap

Export latency heatmap data.

**Query Parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `format` | String | `json` | `json` or `csv` |

**JSON Response:**

```json
{
  "runId": "a1b2c3d4-...",
  "testType": "LOAD",
  "bucketLabels": ["0–0.1ms", "0.1–0.5ms", "0.5–1ms", "1–2ms", "2–5ms", "5–10ms", "10–50ms", "50–100ms", "100–500ms", "500–1000ms"],
  "rows": [
    { "timestampMs": 1708012345000, "phaseName": "ramp-up", "buckets": [0, 12, 145, 832, 1456, 389, 23, 5, 1, 0] },
    { "timestampMs": 1708012350000, "phaseName": "steady-state", "buckets": [0, 0, 12, 145, 832, 456, 89, 23, 5, 1] }
  ]
}
```

---

### Cluster Inspection

#### GET /api/cluster

Kafka cluster metadata: brokers, topics, partitions.

```json
{
  "clusterId": "abc123",
  "controller": { "id": 0, "host": "krafter-kafka-0.kafka.svc", "port": 9092 },
  "brokers": [
    { "id": 0, "host": "krafter-kafka-0.kafka.svc", "port": 9092, "rack": "zone-a" },
    { "id": 1, "host": "krafter-kafka-1.kafka.svc", "port": 9092, "rack": "zone-b" },
    { "id": 2, "host": "krafter-kafka-2.kafka.svc", "port": 9092, "rack": "zone-c" }
  ],
  "topicCount": 12, "totalPartitions": 36
}
```

#### GET /api/cluster/topics

List all topics with partition counts.

```json
{
  "topics": [
    { "name": "kates-results", "partitions": 6, "replicationFactor": 3, "internal": false },
    { "name": "perf-test", "partitions": 3, "replicationFactor": 3, "internal": false }
  ]
}
```

#### GET /api/cluster/topics/{name}

Topic detail with partition assignments and ISR.

```json
{
  "name": "perf-test",
  "partitions": [
    { "id": 0, "leader": 0, "replicas": [0, 1, 2], "isr": [0, 1, 2] },
    { "id": 1, "leader": 1, "replicas": [1, 2, 0], "isr": [1, 2, 0] }
  ],
  "config": { "retention.ms": "604800000", "min.insync.replicas": "2", "cleanup.policy": "delete" }
}
```

#### GET /api/cluster/groups

List consumer groups with status.

```json
{ "groups": [{ "name": "perf-cg", "state": "STABLE", "members": 2, "assignedPartitions": 3 }] }
```

#### GET /api/cluster/groups/{name}

Consumer group detail with per-partition lag.

```json
{
  "name": "perf-cg", "state": "STABLE",
  "coordinator": { "id": 1, "host": "krafter-kafka-1.kafka.svc" },
  "members": [{
    "memberId": "consumer-perf-cg-1-abc123", "clientId": "consumer-perf-cg-1", "host": "/10.244.0.15",
    "assignments": [
      { "topic": "perf-test", "partition": 0, "currentOffset": 50000, "logEndOffset": 50000, "lag": 0 }
    ]
  }],
  "totalLag": 2
}
```

#### GET /api/cluster/brokers

Broker configuration listing. Returns broker IDs, hosts, rack assignments, and server-level configuration key/value pairs.

---

### Disruption Testing

#### POST /api/disruptions/run

Execute a disruption plan.

**Request Body:**

```json
{
  "name": "broker-kill-test",
  "maxAffectedBrokers": 1,
  "autoRollback": true,
  "steps": [{
    "name": "kill-broker-0",
    "faultSpec": {
      "experimentName": "broker-kill", "disruptionType": "POD_KILL",
      "targetNamespace": "kafka", "targetLabel": "strimzi.io/cluster=krafter",
      "chaosDurationSec": 30, "gracePeriodSec": 0
    },
    "steadyStateSec": 15, "observationWindowSec": 60, "requireRecovery": true
  }]
}
```

**Response:** `201 Created`

```json
{
  "id": "disrupt-7f8e9d0c-1a2b-3c4d-5e6f-789012345678",
  "name": "broker-kill-test",
  "status": "RUNNING",
  "steps": [{ "name": "kill-broker-0", "status": "PENDING" }],
  "createdAt": "2026-02-15T21:00:00Z"
}
```

#### POST /api/disruptions/dry-run

Validate a disruption plan without executing. Accepts the same request body as `POST /api/disruptions/run`.

```json
{
  "valid": true,
  "steps": [{ "name": "kill-broker-0", "targetPods": ["krafter-kafka-0"], "safetyCheck": "PASSED", "warnings": [] }],
  "estimatedDurationSec": 105
}
```

#### GET /api/disruptions

List recent disruption reports.

```json
{
  "items": [{
    "id": "disrupt-7f8e9d0c-...", "name": "broker-kill-test",
    "status": "COMPLETED", "verdict": "PASS",
    "createdAt": "2026-02-15T21:00:00Z", "completedAt": "2026-02-15T21:02:45Z"
  }],
  "page": 0, "size": 20, "totalItems": 8, "totalPages": 1
}
```

#### GET /api/disruptions/{id}

Get detailed disruption report with step-level verdicts, fault timing, recovery metrics, and observations (under-replicated partitions, leader elections).

```json
{
  "id": "disrupt-7f8e9d0c-...", "name": "broker-kill-test", "status": "COMPLETED", "verdict": "PASS",
  "steps": [{
    "name": "kill-broker-0", "status": "COMPLETED", "verdict": "PASS",
    "faultAppliedAt": "2026-02-15T21:00:15Z", "recoveryDetectedAt": "2026-02-15T21:01:12Z",
    "recoveryTimeSec": 27, "affectedPods": ["krafter-kafka-0"],
    "observations": { "underReplicatedPartitions": 3, "leaderElections": 3, "partitionsFullyReplicated": true }
  }],
  "createdAt": "2026-02-15T21:00:00Z", "completedAt": "2026-02-15T21:02:45Z"
}
```

#### GET /api/disruptions/{id}/timeline

Get pod event timeline — fault injection, leader elections, recovery events.

```json
{
  "disruptionId": "disrupt-7f8e9d0c-...",
  "events": [
    { "timestamp": "2026-02-15T21:00:15Z", "type": "FAULT_INJECTED", "detail": "Pod krafter-kafka-0 killed" },
    { "timestamp": "2026-02-15T21:00:18Z", "type": "LEADER_ELECTION", "detail": "Partition perf-test-0: new leader broker-1" },
    { "timestamp": "2026-02-15T21:01:12Z", "type": "RECOVERY_DETECTED", "detail": "ISR fully restored" }
  ]
}
```

#### GET /api/disruptions/types

List available disruption types: `POD_KILL`, `POD_FAILURE`, `NETWORK_PARTITION`, `NETWORK_LATENCY`, `DISK_FILL`, `CPU_STRESS`.

#### GET /api/disruptions/{id}/kafka-metrics

Get Kafka intelligence data — pre-fault, during-fault, and post-recovery metrics with impact deltas.

```json
{
  "metrics": {
    "preFault": { "throughputRecordsPerSec": 8412.7, "p99LatencyMs": 12.3, "underReplicatedPartitions": 0 },
    "duringFault": { "throughputRecordsPerSec": 6201.3, "p99LatencyMs": 156.8, "underReplicatedPartitions": 3 },
    "postRecovery": { "throughputRecordsPerSec": 8389.1, "p99LatencyMs": 13.1, "underReplicatedPartitions": 0 }
  },
  "impact": { "throughputDropPercent": 26.3, "latencyIncreasePercent": 1174.8, "recoveryTimeSec": 27 }
}
```

---

### Resilience Testing

#### POST /api/resilience

Run a combined performance + chaos test.

**Request Body:**

```json
{
  "testRequest": { "testType": "LOAD", "spec": { "records": 100000, "producers": 4 } },
  "chaosSpec": { "experimentName": "kafka-pod-kill", "targetNamespace": "kafka" },
  "steadyStateSec": 30
}
```

**Response:**

```json
{
  "status": "COMPLETED",
  "chaosOutcome": { "experimentName": "kafka-pod-kill", "verdict": "PASS", "chaosDuration": "30s" },
  "impactDeltas": { "throughputRecordsPerSec": -15.6, "p99LatencyMs": 596.7, "errorRate": 0.3 },
  "preChaosSummary": { "throughputRecordsPerSec": 8412.7, "p99LatencyMs": 12.3, "errorRate": 0.0 },
  "postChaosSummary": { "throughputRecordsPerSec": 7097.1, "p99LatencyMs": 609.0, "errorRate": 0.3 }
}
```

---

### Trend Analysis

#### GET /api/trends

Historical test trends.

| Parameter | Type | Description |
|-----------|------|-------------|
| `type` | String | Test type |
| `metric` | String | Metric name |
| `days` | int | Lookback period |

```json
{
  "testType": "LOAD", "metric": "p99LatencyMs", "days": 7,
  "dataPoints": [
    { "date": "2026-02-09", "value": 14.2, "runId": "run-001" },
    { "date": "2026-02-12", "value": 15.1, "runId": "run-004" },
    { "date": "2026-02-15", "value": 12.3, "runId": "run-007" }
  ],
  "trendDirection": "STABLE", "avgValue": 13.2
}
```

---

### Scheduling

#### POST /api/schedules

Create a recurring test schedule.

**Request Body:**

```json
{
  "name": "Nightly Load Regression",
  "cronExpression": "0 2 * * *",
  "enabled": true,
  "testRequest": { "testType": "LOAD", "spec": { "records": 100000, "parallelProducers": 4, "acks": "all" } }
}
```

**Response:** `201 Created`

```json
{
  "id": "sched-a1b2c3d4-...",
  "name": "Nightly Load Regression",
  "cronExpression": "0 2 * * *",
  "enabled": true,
  "createdAt": "2026-02-15T20:00:00Z"
}
```

#### GET /api/schedules

List all schedules.

```json
[{
  "id": "sched-a1b2c3d4-...",
  "name": "Nightly Load Regression",
  "cronExpression": "0 2 * * *",
  "enabled": true,
  "lastRunId": "run-d4e5f6...",
  "lastRunAt": "2026-02-15T02:00:00Z",
  "createdAt": "2026-02-14T10:00:00Z"
}]
```

#### GET /api/schedules/{id}

Get detailed schedule info including last run data, next run time, and total run count.

#### DELETE /api/schedules/{id}

Delete a schedule. Returns `204 No Content`.

---

## Error Responses

All errors follow a consistent JSON format:

```json
{ "error": "Not Found", "message": "Test run not found: abc123", "status": 404 }
```

### HTTP Error Codes

| Status | Error | Description | Common Causes |
|:---:|-------|-------------|---------------|
| 400 | Bad Request | Malformed or invalid request | Invalid `testType`, missing required fields, malformed JSON |
| 404 | Not Found | Resource does not exist | Unknown test ID, deleted report, non-existent schedule |
| 409 | Conflict | Conflicts with current state | Test already running on same topic, schedule name collision |
| 422 | Unprocessable Entity | Rejected by safety guards | `maxAffectedBrokers` exceeded, unsafe partition count |
| 500 | Internal Server Error | Unexpected server failure | Thread pool exhaustion, out-of-memory |
| 503 | Service Unavailable | Backend unreachable | Kafka connection lost, broker cluster restarting |

### Error Examples

**400 — Invalid test type:**
```json
{ "error": "Bad Request", "message": "Invalid test type: 'BENCHMARK'. Valid types: LOAD, STRESS, SPIKE, ENDURANCE, VOLUME, CAPACITY, ROUND_TRIP, INTEGRITY", "status": 400 }
```

**409 — Test already running:**
```json
{ "error": "Conflict", "message": "A test is already running on topic 'perf-test'. Cancel or wait for completion.", "status": 409 }
```

**503 — Kafka unreachable:**
```json
{ "error": "Service Unavailable", "message": "Cannot reach Kafka cluster at krafter-kafka-bootstrap.kafka.svc:9092. Connection timed out.", "status": 503 }
```

---

## API Workflows

The following `curl`-based workflows demonstrate common multi-step operations. All examples assume the API is available at `localhost:30083`.

### Workflow 1: Create a Test, Poll for Completion, Get the Report

```bash
#!/usr/bin/env bash
set -euo pipefail
BASE="http://localhost:30083"

# Create a load test
TEST_ID=$(curl -s -X POST "$BASE/api/tests" \
  -H "Content-Type: application/json" \
  -d '{"testType":"LOAD","spec":{"records":100000,"producers":4,"consumers":2,"acks":"all","topic":"perf-test","partitions":3,"replicationFactor":3}}' \
  | jq -r '.id')
echo "Created test: $TEST_ID"

# Poll until complete
while true; do
  STATUS=$(curl -s "$BASE/api/tests/$TEST_ID" | jq -r '.status')
  echo "Status: $STATUS"
  [[ "$STATUS" == "COMPLETED" || "$STATUS" == "FAILED" ]] && break
  sleep 5
done

# Fetch report and export as JUnit XML
curl -s "$BASE/api/tests/$TEST_ID/report" | jq .
curl -s "$BASE/api/tests/$TEST_ID/report/junit" -o report.xml
```

### Workflow 2: Create a Disruption and Monitor Status

```bash
#!/usr/bin/env bash
set -euo pipefail
BASE="http://localhost:30083"
PLAN='{"name":"broker-kill-test","maxAffectedBrokers":1,"autoRollback":true,"steps":[{"name":"kill-broker-0","faultSpec":{"experimentName":"broker-kill","disruptionType":"POD_KILL","targetNamespace":"kafka","targetLabel":"strimzi.io/cluster=krafter","chaosDurationSec":30},"steadyStateSec":15,"observationWindowSec":60,"requireRecovery":true}]}'

# Dry-run to validate
curl -s -X POST "$BASE/api/disruptions/dry-run" -H "Content-Type: application/json" -d "$PLAN" | jq .

# Execute
DISRUPT_ID=$(curl -s -X POST "$BASE/api/disruptions/run" -H "Content-Type: application/json" -d "$PLAN" | jq -r '.id')
echo "Disruption started: $DISRUPT_ID"

# Poll status
while true; do
  STATUS=$(curl -s "$BASE/api/disruptions/$DISRUPT_ID" | jq -r '.status')
  echo "Status: $STATUS"
  [[ "$STATUS" == "COMPLETED" || "$STATUS" == "FAILED" ]] && break
  sleep 10
done

# Inspect timeline and impact
curl -s "$BASE/api/disruptions/$DISRUPT_ID/timeline" | jq '.events[]'
curl -s "$BASE/api/disruptions/$DISRUPT_ID/kafka-metrics" | jq '.impact'
```

### Workflow 3: Export Results in Different Formats

```bash
#!/usr/bin/env bash
set -euo pipefail
BASE="http://localhost:30083"
TEST_ID="a1b2c3d4-e5f6-7890-abcd-ef1234567890"

curl -s "$BASE/api/tests/$TEST_ID/report"                   -o report.json
curl -s "$BASE/api/tests/$TEST_ID/report/csv"                -o report.csv
curl -s "$BASE/api/tests/$TEST_ID/report/junit"              -o report.xml
curl -s "$BASE/api/tests/$TEST_ID/report/heatmap?format=json" -o heatmap.json
curl -s "$BASE/api/tests/$TEST_ID/report/heatmap?format=csv"  -o heatmap.csv

echo "Exported: report.json, report.csv, report.xml, heatmap.json, heatmap.csv"
jq '{type:.testRun.testType, throughput:.summary.avgThroughputRecordsPerSec, p99:.summary.p99LatencyMs, sla:.slaVerdict.passed}' report.json
```

---

## See Also

- [Chapter 10: CLI Reference](10-cli-reference.md) — Interactive CLI commands that wrap this REST API
- [Chapter 13: Scenario Files](13-scenario-files.md) — YAML scenario definitions used with test creation endpoints
- [Chapter 16: gRPC API Reference](16-grpc-api.md) — Strongly typed alternative for CI/CD and service-to-service integration
