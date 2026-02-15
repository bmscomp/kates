# Chapter 11: REST API Reference

The KATES backend exposes a RESTful API that the CLI and other clients use to manage tests, reports, and disruptions.

## Base URL

```
http://localhost:30083
```

## Endpoints

### Health & System

#### GET /api/health

System health check including Kafka connectivity and engine status.

**Response:**

```json
{
  "status": "UP",
  "kafka": {
    "connected": true,
    "bootstrapServers": "krafter-kafka-bootstrap.kafka.svc:9092",
    "clusterId": "abc123",
    "brokerCount": 3
  },
  "engine": {
    "activeTests": 2,
    "totalCompleted": 45
  }
}
```

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
  "id": "a1b2c3d4-...",
  "testType": "LOAD",
  "status": "PENDING",
  "createdAt": "2026-02-15T20:00:00Z"
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

**Response:**

```json
{
  "items": [ ... ],
  "page": 0,
  "size": 20,
  "totalItems": 45,
  "totalPages": 3
}
```

#### GET /api/tests/{id}

Get full details of a test run including results, integrity data, and timeline events.

#### DELETE /api/tests/{id}

Stop and delete a test run.

---

### Reports

#### GET /api/tests/{id}/report

Get the full test report with cluster snapshot, broker metrics, and SLA verdict.

```json
{
  "testRun": { ... },
  "clusterId": "abc123",
  "clusterSnapshot": {
    "brokerCount": 3,
    "topicCount": 12,
    "totalPartitions": 36
  },
  "brokerMetrics": [ ... ],
  "slaVerdict": {
    "passed": true,
    "checks": [
      { "metric": "p99LatencyMs", "threshold": 500, "actual": 12.3, "result": "PASS" }
    ]
  },
  "summary": { ... },
  "generatedAt": "2026-02-15T20:05:00Z"
}
```

#### GET /api/tests/{id}/report/csv

Export report as CSV.

#### GET /api/tests/{id}/report/junit

Export report as JUnit XML.

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
  "bucketLabels": ["0–0.1ms", "0.1–0.5ms", ..., "5000–10000ms"],
  "bucketBoundaries": [[0, 0.1], [0.1, 0.5], ...],
  "rows": [
    {
      "timestampMs": 1708012345000,
      "phaseName": "steady-state",
      "buckets": [0, 0, 12, 145, 832, 456, 89, 23, 5, 1, ...]
    }
  ]
}
```

---

### Cluster Inspection

#### GET /api/cluster

Kafka cluster metadata: brokers, topics, partitions.

#### GET /api/cluster/topics

List all topics with partition counts.

#### GET /api/cluster/topics/{name}

Topic detail with partition assignments and ISR.

#### GET /api/cluster/groups

List consumer groups with status.

#### GET /api/cluster/groups/{name}

Consumer group detail with per-partition lag.

#### GET /api/cluster/brokers

Broker configuration listing.

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
  "steps": [
    {
      "name": "kill-broker-0",
      "faultSpec": {
        "experimentName": "broker-kill",
        "disruptionType": "POD_KILL",
        "targetNamespace": "kafka",
        "targetLabel": "strimzi.io/cluster=krafter",
        "chaosDurationSec": 30,
        "gracePeriodSec": 0
      },
      "steadyStateSec": 15,
      "observationWindowSec": 60,
      "requireRecovery": true
    }
  ]
}
```

#### POST /api/disruptions/dry-run

Validate a disruption plan without executing.

#### GET /api/disruptions

List recent disruption reports.

#### GET /api/disruptions/{id}

Get detailed disruption report.

#### GET /api/disruptions/{id}/timeline

Get pod event timeline.

#### GET /api/disruptions/types

List available disruption types.

#### GET /api/disruptions/{id}/kafka-metrics

Get Kafka intelligence data for a disruption.

---

### Resilience Testing

#### POST /api/resilience

Run a combined performance + chaos test.

**Request Body:**

```json
{
  "testRequest": {
    "testType": "LOAD",
    "spec": { "records": 100000, "producers": 4 }
  },
  "chaosSpec": {
    "experimentName": "kafka-pod-kill",
    "targetNamespace": "kafka"
  },
  "steadyStateSec": 30
}
```

**Response:**

```json
{
  "status": "COMPLETED",
  "chaosOutcome": {
    "experimentName": "kafka-pod-kill",
    "verdict": "PASS",
    "chaosDuration": "30s"
  },
  "impactDeltas": {
    "throughputRecordsPerSec": -15.6,
    "p99LatencyMs": 596.7,
    "errorRate": 0.3
  },
  "preChaosSummary": { ... },
  "postChaosSummary": { ... }
}
```

---

### Trend Analysis

#### GET /api/trends

Historical test trends.

**Query Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `type` | String | Test type |
| `metric` | String | Metric name |
| `days` | int | Lookback period |

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
  "testRequest": {
    "testType": "LOAD",
    "spec": {
      "records": 100000,
      "parallelProducers": 4,
      "acks": "all"
    }
  }
}
```

**Response:** `201 Created`

```json
{
  "id": "sched-a1b2c3...",
  "name": "Nightly Load Regression",
  "cronExpression": "0 2 * * *",
  "enabled": true,
  "createdAt": "2026-02-15T20:00:00Z"
}
```

#### GET /api/schedules

List all schedules.

**Response:**

```json
[
  {
    "id": "sched-a1b2c3...",
    "name": "Nightly Load Regression",
    "cronExpression": "0 2 * * *",
    "enabled": true,
    "lastRunId": "run-d4e5f6...",
    "lastRunAt": "2026-02-15T02:00:00Z",
    "createdAt": "2026-02-14T10:00:00Z"
  }
]
```

#### GET /api/schedules/{id}

Get detailed schedule info including last run data.

#### DELETE /api/schedules/{id}

Delete a schedule. Returns `204 No Content`.

## Error Responses

All errors follow this format:

```json
{
  "error": "Not Found",
  "message": "Test run not found: abc123",
  "status": 404
}
```

| Status | Meaning |
|:---:|---|
| 400 | Invalid request (bad parameters) |
| 404 | Resource not found |
| 409 | Conflict (test already running, etc.) |
| 422 | Validation failure (safety guard rejected) |
| 500 | Internal server error |
