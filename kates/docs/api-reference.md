# REST API Reference

This document provides a comprehensive reference for every REST endpoint exposed by Kates. All endpoints accept and return JSON unless otherwise noted. The base URL is `http://localhost:8080` when running locally.

The API is organized into five resource groups:

- **Test Management** — create, list, poll, and delete performance test runs
- **Cluster Information** — inspect the Kafka cluster's brokers, topics, and consumer groups
- **Disruption Management** — execute, schedule, and review chaos engineering experiments
- **Resilience Testing** — run combined performance + disruption tests
- **Health & Observability** — health checks, effective configuration, and backend availability

All endpoints are documented with OpenAPI annotations. A Swagger UI is available at `/q/swagger-ui` in dev mode.

---

## Test Management

These endpoints manage the lifecycle of performance test runs: creating tests, polling for status, listing runs, and cleaning up completed tests.

### Create a Test

```
POST /api/tests
Content-Type: application/json
```

Submits a new performance test for execution. This is an asynchronous operation — the response is returned immediately with a `202 Accepted` status while the test runs in the background.

**Request Body:**

```json
{
  "type": "LOAD",
  "backend": "native",
  "spec": {
    "topic": "my-perf-test",
    "numProducers": 3,
    "numConsumers": 2,
    "numRecords": 1000000,
    "throughput": 25000,
    "recordSize": 1024,
    "durationMs": 600000,
    "partitions": 6,
    "replicationFactor": 3,
    "minInsyncReplicas": 2,
    "acks": "all",
    "batchSize": 65536,
    "lingerMs": 5,
    "compressionType": "lz4"
  }
}
```

**Field-by-field explanation:**

- `type` (required) — one of `LOAD`, `STRESS`, `SPIKE`, `ENDURANCE`, `VOLUME`, `CAPACITY`, `ROUND_TRIP`. Determines the test execution strategy and which per-type defaults are applied. See [Test Types](test-types.md) for detailed descriptions.

- `backend` (optional) — selects the execution engine: `native` (in-process Kafka clients with virtual threads) or `trogdor` (distributed Trogdor agents). If omitted, defaults to the configured `kates.engine.default-backend`. You can override this per-request to test the same scenario on both backends.

- `spec` (optional) — the test specification. Every field is optional. Omitted values are filled from per-test-type defaults (see [Test Types](test-types.md) for the default values per type). This means you can submit a perfectly valid test with just `{"type": "LOAD"}` and all parameters will use their defaults.

**Response: `202 Accepted`**

```json
{
  "id": "a1b2c3d4",
  "testType": "LOAD",
  "status": "RUNNING",
  "spec": {
    "topic": "my-perf-test",
    "numProducers": 3,
    "numConsumers": 2,
    "numRecords": 1000000,
    "throughput": 25000,
    "recordSize": 1024,
    "durationMs": 600000,
    "partitions": 6,
    "replicationFactor": 3,
    "minInsyncReplicas": 2,
    "acks": "all",
    "batchSize": 65536,
    "lingerMs": 5,
    "compressionType": "lz4"
  },
  "results": [
    {
      "taskId": "a1b2c3d4-load-0",
      "testType": "LOAD",
      "status": "RUNNING",
      "startTime": "2026-02-13T00:30:00Z"
    }
  ],
  "createdAt": "2026-02-13T00:30:00Z"
}
```

The response includes the fully resolved `spec` (with defaults applied), so you can see exactly what parameters the test is using.

**Error Responses:**

| Status | Condition | Body |
|--------|-----------|------|
| `400 Bad Request` | Missing `type` field or invalid type value | `{"error": "type is required", "status": 400}` |
| `400 Bad Request` | Bean Validation constraint violation | `{"error": "numProducers must be >= 1", "status": 400}` |
| `500 Internal Server Error` | Topic creation failure, backend submission failure | `{"error": "...", "status": 500}` |

---

### List All Tests

```
GET /api/tests
GET /api/tests?type=LOAD
```

Returns an array of all `TestRun` objects. Each object includes the test specification, current status, and per-task results if available.

The optional `type` query parameter filters the results to only include tests of the specified type. This is useful when you have many test runs and want to see only stress tests, for example.

**Response: `200 OK`**

```json
[
  {
    "id": "a1b2c3d4",
    "testType": "LOAD",
    "status": "DONE",
    "spec": { "..." },
    "results": [ "..." ],
    "createdAt": "2026-02-13T00:30:00Z"
  }
]
```

---

### Get Test Status

```
GET /api/tests/{id}
```

Returns a single `TestRun` with refreshed status from the execution backend. Every call to this endpoint triggers a `refreshStatus()` operation — the orchestrator polls all active `BenchmarkHandle` objects via the backend to get the latest metrics. This means the response always reflects the current state of the test.

**Response when running:**

The `results` array shows in-progress metrics for each task:

```json
{
  "id": "a1b2c3d4",
  "testType": "LOAD",
  "status": "RUNNING",
  "results": [
    {
      "taskId": "a1b2c3d4-load-0",
      "status": "RUNNING",
      "throughputRecordsPerSec": 24850.5,
      "avgLatencyMs": 3.2,
      "recordsSent": 150000,
      "startTime": "2026-02-13T00:30:00Z"
    }
  ]
}
```

**Response when complete:**

```json
{
  "id": "a1b2c3d4",
  "testType": "LOAD",
  "status": "DONE",
  "results": [
    {
      "taskId": "a1b2c3d4-load-0",
      "status": "DONE",
      "throughputRecordsPerSec": 24850.5,
      "avgLatencyMs": 3.2,
      "p50LatencyMs": 2.1,
      "p95LatencyMs": 8.5,
      "p99LatencyMs": 15.3,
      "maxLatencyMs": 45.0,
      "recordsSent": 333333,
      "startTime": "2026-02-13T00:30:00Z",
      "endTime": "2026-02-13T00:40:00Z"
    }
  ]
}
```

**Error Responses:**

| Status | Condition |
|--------|-----------|
| `404 Not Found` | No test run with the given ID exists |

---

### Stop and Delete a Test

```
DELETE /api/tests/{id}
```

Stops all running backend tasks for this test run and removes it from the repository. For the Trogdor backend, this sends stop commands to the Trogdor Coordinator for each task. For the native backend, this interrupts the virtual threads.

**Response: `204 No Content`**

**Error Responses:**

| Status | Condition |
|--------|-----------|
| `404 Not Found` | No test run with the given ID exists |

---

### List Available Test Types

```
GET /api/tests/types
```

Returns the list of all available test types as a JSON array. This is useful for building dynamic UIs that need to populate a dropdown or radio group.

**Response: `200 OK`**

```json
["LOAD", "STRESS", "SPIKE", "ENDURANCE", "VOLUME", "CAPACITY", "ROUND_TRIP"]
```

---

### List Available Backends

```
GET /api/tests/backends
```

Returns the list of available execution backends. This is determined at runtime based on which backends are deployed and configured.

**Response: `200 OK`**

```json
["native", "trogdor"]
```

---

## Cluster Information

These endpoints expose information about the Kafka cluster that Kates is connected to. They use the Kafka AdminClient to query broker metadata, topic configuration, and consumer group state.

### Get Cluster Info

```
GET /api/cluster/info
```

Returns comprehensive information about the Kafka cluster, including the cluster ID, controller broker, and all broker nodes with their host, port, and rack assignment.

**Response: `200 OK`**

```json
{
  "clusterId": "xYz123AbC",
  "controller": { "id": 0, "host": "broker-0", "port": 9092, "rack": "zone-a" },
  "brokerCount": 3,
  "brokers": [
    { "id": 0, "host": "broker-0", "port": 9092, "rack": "zone-a" },
    { "id": 1, "host": "broker-1", "port": 9092, "rack": "zone-b" },
    { "id": 2, "host": "broker-2", "port": 9092, "rack": "zone-c" }
  ]
}
```

The `rack` field reflects the `broker.rack` configuration on each broker, which is typically set to the availability zone for rack-aware replication.

**Error Responses:**

| Status | Condition |
|--------|-----------|
| `503 Service Unavailable` | Kafka cluster is not reachable |

---

### List Topics

```
GET /api/cluster/topics
```

Returns a JSON array of all topic names in the cluster, excluding internal topics (those starting with `__`).

**Response: `200 OK`**

```json
["load-test", "stress-test", "my-app-events", "orders"]
```

---

## Disruption Management

These endpoints manage chaos engineering experiments: executing disruption plans, listing pre-built playbooks, scheduling recurring disruptions, and reviewing past disruption reports.

### Execute a Disruption

```
POST /api/disruptions
Content-Type: application/json
```

Executes a multi-step disruption plan. This is a synchronous operation — the response is returned only after all steps have completed (or failed). For real-time progress monitoring, use the SSE endpoint.

You can also set `dryRun=true` to simulate the disruption without actually injecting any faults:

```
POST /api/disruptions?dryRun=true
```

**Request Body:**

```json
{
  "name": "leader-failover-test",
  "description": "Kill the leader of partition 0 and observe recovery",
  "maxAffectedBrokers": 1,
  "autoRollback": true,
  "isrTrackingTopic": "my-topic",
  "lagTrackingGroupId": "my-consumer-group",
  "sla": {
    "maxP99LatencyMs": 100.0,
    "minThroughputRecPerSec": 10000.0,
    "maxRtoMs": 30000,
    "maxDataLossPercent": 0.0
  },
  "steps": [
    {
      "name": "kill-leader",
      "steadyStateSec": 30,
      "observationWindowSec": 120,
      "requireRecovery": true,
      "faultSpec": {
        "experimentName": "leader-kill",
        "disruptionType": "POD_KILL",
        "targetTopic": "my-topic",
        "targetPartition": 0,
        "targetNamespace": "kafka",
        "chaosDurationSec": 0
      }
    }
  ]
}
```

**Response: `200 OK`**

Returns a `DisruptionReport` with per-step results, metrics, and SLA grading. See the Architecture guide for the complete report structure.

**dry-run response:**

When `dryRun=true`, returns a preview showing what would happen without actually injecting faults:

```json
{
  "wouldSucceed": true,
  "totalBrokers": 3,
  "steps": [
    {
      "name": "kill-leader",
      "disruptionType": "POD_KILL",
      "targetPod": "krafter-kafka-1",
      "resolvedLeaderId": 1,
      "affectedPods": ["krafter-kafka-1"],
      "warnings": []
    }
  ],
  "warnings": [],
  "errors": []
}
```

**Error Responses:**

| Status | Condition |
|--------|-----------|
| `422 Unprocessable Entity` | Safety guard rejected the plan (blast radius exceeded, RBAC insufficient) |
| `500 Internal Server Error` | Chaos provider failure or Kafka AdminClient error |

---

### List Playbooks

```
GET /api/disruptions/playbooks
GET /api/disruptions/playbooks?category=infrastructure
```

Returns the list of pre-built disruption playbooks. Each playbook is a curated, multi-step scenario that can be executed without writing any JSON manually.

**Response: `200 OK`**

```json
[
  {
    "name": "az-failure",
    "description": "Simulates an availability zone failure by draining a node",
    "category": "infrastructure",
    "steps": [ "..." ]
  }
]
```

---

### Execute a Playbook

```
POST /api/disruptions/playbooks/{name}
POST /api/disruptions/playbooks/{name}?dryRun=true
```

Executes a pre-built playbook by name. The playbook is loaded from the catalog and converted to a `DisruptionPlan` automatically.

**Response: `200 OK`** — same format as `POST /api/disruptions`.

---

### List Disruption Reports

```
GET /api/disruptions/reports
GET /api/disruptions/reports?page=0&size=20
```

Returns paginated disruption reports from the database, ordered by execution time (most recent first).

---

### Get a Disruption Report

```
GET /api/disruptions/reports/{id}
```

Returns a specific persisted disruption report by ID.

---

### Create a Disruption Schedule

```
POST /api/disruptions/schedules
Content-Type: application/json
```

Creates a scheduled disruption that runs on a cron schedule. Useful for continuous resilience validation—run the same disruption playbook every day or every week.

```json
{
  "name": "daily-leader-failover",
  "cronExpression": "0 3 * * *",
  "playbookName": "leader-cascade",
  "enabled": true
}
```

---

### Disruption Event Stream (SSE)

```
GET /api/disruptions/stream
```

Server-Sent Events endpoint for real-time disruption progress. Events include step start/complete, fault injection, recovery status, and SLA grading results. This is designed for dashboards that want to show live disruption progress.

---

## Health & Observability

### Health Check

```
GET /api/health
```

Returns the overall health status, including Kafka connectivity, active backend, and the effective per-type test configuration. This is the most useful endpoint for debugging configuration issues — it shows exactly what values are in effect after the three-tier configuration resolution.

**Response: `200 OK`**

```json
{
  "status": "UP",
  "engine": {
    "activeBackend": "native",
    "availableBackends": ["native", "trogdor"]
  },
  "kafka": {
    "status": "UP",
    "message": "Kafka cluster is reachable",
    "bootstrapServers": "krafter-kafka-bootstrap.kafka.svc:9092"
  },
  "tests": {
    "load": {
      "partitions": 3, "batchSize": 65536, "numProducers": 1, "..."
    },
    "stress": {
      "partitions": 6, "batchSize": 131072, "numProducers": 3, "..."
    },
    "spike": { "acks": "1", "lingerMs": 0, "compressionType": "none", "..." },
    "endurance": { "throughput": 5000, "durationMs": 3600000, "..." },
    "volume": { "recordSize": 10240, "batchSize": 262144, "..." },
    "capacity": { "partitions": 12, "numProducers": 5, "..." },
    "roundtrip": { "batchSize": 16384, "compressionType": "none", "..." }
  }
}
```

The `tests` section shows the effective per-type configuration, reflecting values from the ConfigMap, `application.properties`, and built-in defaults. This is useful for verifying that ConfigMap changes took effect after a deployment restart.

**Health Status Values:**

| Status | Meaning |
|--------|---------|
| `UP` | Kafka is reachable and responsive |
| `DEGRADED` | Kafka is reachable but some checks failed (e.g., topic metadata slow) |
| `DOWN` | Kafka is not reachable |

---

### Quarkus Built-In Health

```
GET /q/health          — all health checks
GET /q/health/live     — liveness probe (is the process alive?)
GET /q/health/ready    — readiness probe (can the service handle requests?)
```

These are standard MicroProfile Health endpoints used by Kubernetes for liveness and readiness probes. The Kates `HealthResource` contributes to the readiness check via Kafka connectivity validation.

---

### Swagger UI

```
GET /q/swagger-ui
```

Interactive API explorer available in dev mode. All endpoints are documented with `@Operation`, `@Tag`, and `@APIResponse` annotations.

---

### OpenAPI Specification

```
GET /q/openapi
```

Machine-readable OpenAPI 3.0 specification in YAML format. Can be used for client code generation in any language.

---

## 6. Resilience Testing

The resilience endpoint combines a performance test with a chaos experiment in a single coordinated workflow. For full details, see [Resilience Testing](resilience-testing.md).

### Execute Resilience Test

```
POST /api/resilience
Content-Type: application/json
```

**Request Body:**

```json
{
  "testRequest": {
    "type": "LOAD",
    "backend": "native",
    "spec": {
      "topic": "resilience-test",
      "numProducers": 2,
      "numConsumers": 2,
      "numRecords": 500000,
      "throughput": 10000,
      "recordSize": 1024,
      "partitions": 6,
      "replicationFactor": 3,
      "acks": "all"
    }
  },
  "chaosSpec": {
    "experimentName": "broker-kill-during-load",
    "disruptionType": "POD_KILL",
    "targetTopic": "resilience-test",
    "targetPartition": 0,
    "chaosDurationSec": 0,
    "gracePeriodSec": 0
  },
  "steadyStateSec": 60
}
```

**Fields:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `testRequest` | `CreateTestRequest` | Yes | Performance test to run (same format as `POST /api/tests`) |
| `chaosSpec` | `FaultSpec` | Yes | Fault to inject during the test |
| `steadyStateSec` | `int` | No | Seconds to wait for steady state before injecting (default: 30) |

**Response: `200 OK`**

```json
{
  "status": "COMPLETED",
  "performanceReport": { "..." },
  "chaosOutcome": {
    "status": "COMPLETED",
    "message": "Pod krafter-kafka-1 killed successfully",
    "durationMs": 5000
  },
  "preChaosSummary": {
    "avgThroughputRecPerSec": 10200.0,
    "avgLatencyMs": 3.8,
    "p99LatencyMs": 12.0,
    "errorRate": 0.0
  },
  "postChaosSummary": {
    "avgThroughputRecPerSec": 8800.0,
    "avgLatencyMs": 6.5,
    "p99LatencyMs": 35.0,
    "errorRate": 0.2
  },
  "impactDeltas": {
    "throughputRecPerSec": -13.73,
    "avgLatencyMs": 71.05,
    "p99LatencyMs": 191.67,
    "maxLatencyMs": 450.00,
    "errorRate": 100.00
  }
}
```

**Error Responses:**

| Status | Condition |
|--------|-----------|
| `400 Bad Request` | Missing `testRequest` or `chaosSpec` field |

