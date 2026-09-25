# REST API Reference

## Introduction

The Kates backend exposes a RESTful API that the CLI and other clients use to manage tests, reports, and disruptions. Every action available in the `kates` CLI maps directly to a REST call — the CLI is a thin wrapper around this API. This means that anything you can do interactively from the command line can be automated via HTTP requests, making the REST API the foundation for scripting, CI/CD integration, and custom dashboards.

**When should you choose the REST API over the CLI or gRPC?** Use the REST API when you need to integrate Kates into shell scripts, automation pipelines, or monitoring systems that work best with JSON over HTTP. It is ideal for `curl`-based workflows, webhook integrations, and any tool that speaks HTTP natively. The API is human-readable and easy to debug — every request and response is plain JSON, so you can inspect traffic with standard tools like `curl`, `httpie`, or browser dev-tools.

If you need strongly typed clients or high-throughput programmatic access from Go, Java, or Python services, consider the [gRPC API](16-grpc-api.md) instead; it covers test runs, cluster inspection, and health. If you prefer an interactive experience with formatted output, the [CLI](10-cli-reference.md) is the best choice. All three interfaces share the same backend service layer, so a test run is the same run whichever one started it; [gRPC API Reference](16-grpc-api.md) lists the places where gRPC responses carry less than REST.

After this chapter, you can:

- Authenticate any request with an API key and name the endpoints that stay public
- Create a test with `POST /api/tests`, poll it to completion, and export the report as JSON, CSV, or JUnit XML
- Validate a disruption plan with a dry run, execute it, and read the recovery timings and SLA grade it returns
- Decode any failure from the shared error envelope and its HTTP status code

---

## Authentication

Kates enforces API-key authentication **by default**: `kates.api.security-enabled=true` in `application.properties` (the `%dev` and `%test` profiles disable it). The expected key comes from the `kates.api.key` property, which reads the `KATES_API_KEY` environment variable; the Helm chart provisions it as a Kubernetes Secret via the `apiKey` values in `charts/kates/values.yaml`: `enabled`, `value` (a random 32-character key when empty, kept across upgrades), and `existingSecret` with `secretKey` to use a Secret you manage.

On a default install the chart generates a random key into the `kates-api-key` Secret in the `kates` namespace. Read it into a shell variable once; every example in this chapter uses it:

```bash
export KATES_API_KEY="$(kubectl get secret kates-api-key -n kates -o jsonpath='{.data.api-key}' | base64 -d)"
```

The CLI reads the same variable, ahead of the key stored in its context.

Include the key in every request, using either header form:

```bash
curl -H "Authorization: Bearer $KATES_API_KEY" http://localhost:30083/api/tests
curl -H "X-API-Key: $KATES_API_KEY" http://localhost:30083/api/tests
```

Requests without a key receive `401 Unauthorized`; requests with a wrong key receive `403 Forbidden`. The paths `/api/health`, `/openapi`, and everything under `/q/` (metrics, OpenAPI spec) are public and never require a key.

### Common Request Headers

| Header | Value | Required | Description |
|--------|-------|:---:|-------------|
| `Content-Type` | `application/json` | ✅ (POST/PUT) | Request body format |
| `Accept` | `application/json` | | Response format (default) |
| `Authorization` | `Bearer <api-key>` | When security enabled | API key (alternative: `X-API-Key`) |
| `X-API-Key` | `<api-key>` | When security enabled | API key (alternative to `Authorization`) |

::: {.callout-tip}
In Quarkus dev mode and in tests, security is switched off (`%dev.kates.api.security-enabled=false`), so no authentication headers are needed there.
:::

---

## Base URL

```text
http://localhost:30083
```

`localhost:30083` is the local end of the port-forward that `make ports` (`scripts/port-forward.sh`) starts to port 8080 of the `kates` Service; nothing answers there until it runs. The number matches the NodePort the Kind overlay assigns (`charts/kates/values-kind.yaml`), but the Kind cluster does not publish NodePorts on the host, so the forward is what makes the API reachable. Without the repository's scripts, forward the Service yourself with `kubectl port-forward svc/kates -n kates 30083:8080`, or run `kates ports`, which forwards it to `localhost:8080` instead. When running in-cluster, use the Kubernetes service DNS name: `http://kates.kates.svc.cluster.local:8080`

---

## Endpoints

This chapter documents the most commonly used endpoints in the core resource families: health, tests, reports, cluster inspection, disruptions, resilience, trends, and schedules. The backend exposes more than is listed here — bulk operations, test cancellation, baselines, report comparison and markdown export, disruption templates and schedules, resilience scenarios, plus entire resource families (webhooks, events, cost, advisor, audit, profiles, security, Kafka client tooling, DLQ, share groups). The complete, always-current machine-readable specification is generated from the code by MicroProfile OpenAPI and served at `/q/openapi` (Swagger UI is available at `/q/swagger-ui` in dev mode).

### Health & System

#### GET /api/health

System health check including Kafka connectivity and engine status. This endpoint is public — no API key required.

**Response:** `200 OK`

```json
{
  "status": "UP",
  "engine": { "activeBackend": "native", "availableBackends": ["native", "trogdor"] },
  "kafka": {
    "status": "UP",
    "bootstrapServers": "krafter-kafka-bootstrap.kafka.svc.cluster.local:9092",
    "message": "Kafka cluster is reachable"
  }
}
```

The response also contains a `tests` object with the resolved default configuration for every test type. When Kafka is unreachable, the endpoint still returns `200 OK`, but with `"status": "DEGRADED"` and `"kafka.status": "DOWN"`.

---

### Test Management

#### POST /api/tests

Create and start a new test run. Execution is asynchronous — poll `GET /api/tests/{id}` for progress.

**Request Body:**

```json
{
  "type": "LOAD",
  "backend": "native",
  "spec": {
    "numRecords": 100000,
    "recordSize": 1024,
    "acks": "all",
    "topic": "perf-test",
    "partitions": 3,
    "replicationFactor": 3,
    "minInsyncReplicas": 2,
    "durationMs": 120000,
    "throughput": -1
  }
}
```

| Field | Type | Required | Description |
|-------|------|:---:|-------------|
| `type` | String | ✅ | Test type — any value returned by `GET /api/tests/types` (LOAD, STRESS, SPIKE, ENDURANCE, VOLUME, CAPACITY, ROUND_TRIP, INTEGRITY, the TUNE_* family, INTEGRATION_CDC) |
| `backend` | String | | Backend engine (default: "native") |
| `spec` | Object | | Test specification overrides |

::: {.callout-important}
The backend merges `spec` with the defaults of the test type, and the merge keeps only `topic`, `numRecords`, `recordSize`, `throughput`, `durationMs`, `acks`, `batchSize`, `lingerMs`, `compressionType`, `partitions`, `replicationFactor`, `minInsyncReplicas`, `numProducers` and `numConsumers`. It drops the other fields the request accepts — `targetThroughput`, `consumerGroup`, `fetchMinBytes`, `fetchMaxWaitMs`, `enableIdempotence`, `enableTransactions` and `enableCrc` — so they have no effect: the response's `spec` leaves `consumerGroup` out and shows the others at their defaults. The echoed `enableIdempotence: false` does not make the producer non-idempotent: the Kafka client turns idempotence on whenever `acks` is `all`. `throughput` is the only rate limit: `kates test create --throughput` sends `targetThroughput` and does not slow a run down. Of the fields kept, `numProducers` sets the number of producers for STRESS and CAPACITY only, and no test type reads `numConsumers`, so a LOAD run has one producer and one consumer whatever the spec says.
:::

**Response:** `202 Accepted`

```json
{
  "id": "a1b2c3d4",
  "testType": "LOAD",
  "status": "PENDING",
  "backend": "native",
  "spec": {
    "topic": "perf-test", "numRecords": 100000, "recordSize": 1024, "throughput": -1,
    "acks": "all", "batchSize": 65536, "lingerMs": 5, "compressionType": "lz4",
    "numProducers": 1, "numConsumers": 1, "durationMs": 120000,
    "replicationFactor": 3, "partitions": 3, "minInsyncReplicas": 2,
    "targetThroughput": -1, "fetchMinBytes": 1, "fetchMaxWaitMs": 500,
    "enableIdempotence": false, "enableTransactions": false, "enableCrc": true
  },
  "createdAt": "2026-02-15T20:00:00Z"
}
```

The `spec` in the response is the merged one: the request's values, and the LOAD defaults for everything it leaves out.

Run IDs are 8-character UUID prefixes. `status` moves through `PENDING`, `RUNNING`, `STOPPING`, and ends at `DONE` or `FAILED`.

#### GET /api/tests

List test runs with pagination and filtering.

**Query Parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `page` | int | 0 | Page number (0-indexed) |
| `size` | int | 50 | Page size (max 200) |
| `type` | String | | Filter by test type |
| `status` | String | | Filter by status (PENDING, RUNNING, STOPPING, DONE, FAILED) |

**Response:** `200 OK`

```json
{
  "items": [
    {
      "id": "a1b2c3d4",
      "testType": "LOAD",
      "status": "DONE",
      "backend": "native",
      "createdAt": "2026-02-15T20:00:00Z"
    },
    {
      "id": "b2c3d4e5",
      "testType": "STRESS",
      "status": "RUNNING",
      "backend": "native",
      "createdAt": "2026-02-15T20:05:00Z"
    }
  ],
  "page": 0, "size": 50, "total": 2, "count": 2
}
```

#### GET /api/tests/{id}

Get full details of a test run, refreshing its status. The run carries one result entry per task/phase; for INTEGRITY tests each result also includes an `integrity` object (lost/duplicate records, RTO/RPO).

**Response:** `200 OK` (`spec` cut down to four fields; it is the merged spec shown under [POST /api/tests](#post-apitests))

```json
{
  "id": "a1b2c3d4",
  "testType": "LOAD",
  "status": "DONE",
  "backend": "native",
  "spec": { "topic": "perf-test", "numRecords": 100000, "recordSize": 1024, "acks": "all" },
  "results": [
    {
      "taskId": "a1b2c3d4-produce-0", "testType": "LOAD", "phaseName": "produce", "status": "DONE", "recordsSent": 100000,
      "throughputRecordsPerSec": 8412.7, "throughputMBPerSec": 8.2,
      "avgLatencyMs": 2.4, "p50LatencyMs": 1.9, "p95LatencyMs": 6.8, "p99LatencyMs": 12.3, "maxLatencyMs": 41.6,
      "startTime": "2026-02-15T20:00:01.108342Z", "endTime": "2026-02-15T20:00:13.527115Z"
    },
    {
      "taskId": "a1b2c3d4-consume-0", "testType": "LOAD", "phaseName": "consume", "status": "DONE", "recordsSent": 100000,
      "throughputRecordsPerSec": 8391.2, "throughputMBPerSec": 8.2,
      "avgLatencyMs": 0.0, "p50LatencyMs": 0.0, "p95LatencyMs": 0.0, "p99LatencyMs": 0.0, "maxLatencyMs": 0.0,
      "startTime": "2026-02-15T20:00:01.109020Z", "endTime": "2026-02-15T20:00:13.640388Z"
    }
  ],
  "createdAt": "2026-02-15T20:00:00.412587Z"
}
```

A LOAD run has exactly two tasks, `<id>-produce-0` in phase `produce` and `<id>-consume-0` in phase `consume`, however many producers and consumers the spec asks for. On the consumer, `recordsSent` counts the records it consumed; it measures no latency, so its latency fields are always `0.0`, and the run's latency lives on the producer.

#### DELETE /api/tests/{id}

Stop and delete a test run. If the test is currently running, it is cancelled before deletion.

**Response:** `204 No Content` on success. Returns `404 Not Found` if the test ID does not exist.

---

### Reports

#### GET /api/tests/{id}/report

Get the full test report with cluster snapshot, broker metrics, and SLA verdict.

```json
{
  "run": { "id": "a1b2c3d4", "testType": "LOAD", "status": "DONE" },
  "summary": {
    "totalRecords": 100000,
    "avgThroughputRecPerSec": 8412.7, "peakThroughputRecPerSec": 9120.4, "avgThroughputMBPerSec": 8.2,
    "avgLatencyMs": 2.4, "p50LatencyMs": 1.8, "p95LatencyMs": 5.6, "p99LatencyMs": 12.3,
    "p999LatencyMs": 21.7, "maxLatencyMs": 34.1,
    "totalErrors": 0, "errorRate": 0.0, "durationMs": 125000
  },
  "phases": [
    { "phaseName": "steady-state", "metrics": { "avgThroughputRecPerSec": 8412.7, "p99LatencyMs": 12.3 } }
  ],
  "clusterSnapshot": {
    "clusterId": "4L6g3nShT-eMCtK--X86sw",
    "brokerCount": 3,
    "controllerId": 0,
    "brokers": [ { "id": 0, "host": "krafter-brokers-alpha-0.krafter-kafka-brokers.kafka.svc", "port": 9092, "rack": "alpha" } ]
  },
  "brokerMetrics": [
    {
      "brokerId": 0, "host": "krafter-brokers-alpha-0.krafter-kafka-brokers.kafka.svc", "isController": true,
      "leaderPartitions": 12, "replicaPartitions": 36, "underReplicatedPartitions": 0,
      "leaderSharePercent": 33.3, "skewed": false
    }
  ],
  "overallSlaVerdict": { "passed": true, "violations": [] },
  "generatedAt": "2026-02-15T20:05:00Z"
}
```

When an SLA is violated, `overallSlaVerdict.violations` contains entries of the form `{ "metric": "p99LatencyMs", "threshold": 500.0, "actual": 612.4, "severity": "CRITICAL" }`.

#### GET /api/tests/{id}/report/csv

Export report as CSV. **Response:** `200 OK` with `Content-Type: text/csv`

```text
runId,testType,backend,phase,recordsSent,throughputRecPerSec,throughputMBPerSec,avgLatencyMs,p50LatencyMs,p95LatencyMs,p99LatencyMs,maxLatencyMs,error
a1b2c3d4,LOAD,native,ramp-up,10000,5234.1,5.1,4.8,3.2,9.6,18.4,45.2,
a1b2c3d4,LOAD,native,steady-state,90000,8412.7,8.2,2.4,1.8,5.6,12.3,34.1,
```

A `# Summary` block with aggregate metrics is appended after the per-result rows.

#### GET /api/tests/{id}/report/junit

Export report as JUnit XML for CI/CD integration. Each test result maps to a `<testcase>`; SLA violations are appended as extra `<testcase>` entries with `<failure>` elements. **Response:** `200 OK` with `Content-Type: application/xml`

```xml
<?xml version="1.0" encoding="UTF-8"?>
<testsuite name="LOAD" tests="2" failures="0" errors="0">
  <testcase name="ramp-up" classname="kates.LOAD" time="15.000"/>
  <testcase name="steady-state" classname="kates.LOAD" time="110.000"/>
</testsuite>
```

#### GET /api/tests/{id}/report/heatmap

Export latency heatmap data. Returns `404` with a plain-text message when no heatmap data was recorded for the run.

**Query Parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `format` | String | `json` | `json` or `csv` |

**JSON Response:**

```json
{
  "runId": "a1b2c3d4",
  "testType": "LOAD",
  "bucketLabels": ["0ms-0.5ms", "0.5ms-1ms", "1ms-2ms", "2ms-3ms", "3ms-5ms"],
  "bucketBoundaries": [[0.0, 0.5], [0.5, 1.0], [1.0, 2.0], [2.0, 3.0], [3.0, 5.0]],
  "rows": [
    { "timestampMs": 1771185645000, "phase": "ramp-up", "counts": [0, 12, 145, 832, 1456] },
    { "timestampMs": 1771185650000, "phase": "steady-state", "counts": [0, 0, 12, 145, 832] }
  ]
}
```

*(Arrays truncated for readability — the real heatmap uses 25 latency buckets spanning 0 ms to 10 s. Each row is a 1-second sampling window.)*

---

### Cluster Inspection

#### GET /api/cluster/info

Kafka cluster metadata: cluster ID, controller, and brokers.

```json
{
  "clusterId": "4L6g3nShT-eMCtK--X86sw",
  "controller": { "id": 1, "host": "krafter-brokers-gamma-1.krafter-kafka-brokers.kafka.svc", "port": 9092, "rack": "gamma" },
  "brokerCount": 3,
  "brokers": [
    { "id": 0, "host": "krafter-brokers-alpha-0.krafter-kafka-brokers.kafka.svc", "port": 9092, "rack": "alpha" },
    { "id": 1, "host": "krafter-brokers-gamma-1.krafter-kafka-brokers.kafka.svc", "port": 9092, "rack": "gamma" },
    { "id": 2, "host": "krafter-brokers-sigma-2.krafter-kafka-brokers.kafka.svc", "port": 9092, "rack": "sigma" }
  ]
}
```

Hosts and racks are what the brokers advertise: here the `krafter` cluster that `kates deploy` creates on the Kind cluster, with one broker pool per zone. `controller` is whatever Kafka's `DescribeCluster` answer names, and a KRaft broker answers with an arbitrary live broker rather than the active controller, so it can change between calls. The KRaft quorum leader is `kraftQuorum.leaderId` in the `GET /api/cluster/check` response.

#### GET /api/cluster/topics

List topic names, paginated (`page`, `size` query parameters; `size` defaults to 50, max 200).

```json
{ "page": 0, "size": 50, "total": 2, "count": 2, "items": ["kates-results", "perf-test"] }
```

#### GET /api/cluster/topics/{name}

Topic detail with partition assignments, ISR, and key configuration entries.

```json
{
  "name": "perf-test",
  "internal": false,
  "partitions": 3,
  "replicationFactor": 3,
  "partitionInfo": [
    { "partition": 0, "leader": 0, "replicas": [0, 1, 2], "isr": [0, 1, 2], "underReplicated": false },
    { "partition": 1, "leader": 1, "replicas": [1, 2, 0], "isr": [1, 2, 0], "underReplicated": false }
  ],
  "configs": { "retention.ms": "604800000", "min.insync.replicas": "2", "cleanup.policy": "delete" }
}
```

#### GET /api/cluster/groups

List consumer groups with state, paginated (`page`, `size` query parameters).

```json
{ "page": 0, "size": 50, "total": 1, "count": 1, "items": [{ "groupId": "perf-cg", "state": "Stable" }] }
```

#### GET /api/cluster/groups/{id}

Consumer group detail with per-partition offsets and lag.

```json
{
  "groupId": "perf-cg",
  "state": "Stable",
  "members": 2,
  "offsets": [
    { "topic": "perf-test", "partition": 0, "currentOffset": 50000, "endOffset": 50000, "lag": 0 },
    { "topic": "perf-test", "partition": 1, "currentOffset": 49998, "endOffset": 50000, "lag": 2 }
  ],
  "totalLag": 2
}
```

#### GET /api/cluster/brokers/{id}/configs

Non-default configuration entries for a specific broker.

```json
[
  { "name": "min.insync.replicas", "value": "2", "source": "STATIC_BROKER_CONFIG", "readOnly": false },
  { "name": "log.retention.hours", "value": "168", "source": "STATIC_BROKER_CONFIG", "readOnly": false }
]
```

---

### Disruption Testing

#### POST /api/disruptions

Execute a disruption plan. Execution is **asynchronous** — the plan is validated, accepted, and run in the background, so the call returns immediately with a report id. A plan runs for minutes (steady state + chaos duration + observation window + recovery timeout per step), which is longer than most proxies and load balancers will hold a connection open; poll `GET /api/disruptions/{id}` for progress and the final report. Add the `dryRun=true` query parameter to validate the plan without injecting any faults.

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
      "targetNamespace": "kafka",
      "targetLabel": "strimzi.io/component-type=kafka,strimzi.io/broker-role=true",
      "targetBrokerId": 0, "chaosDurationSec": 30, "gracePeriodSec": 0
    },
    "steadyStateSec": 15, "observationWindowSec": 60, "requireRecovery": true
  }]
}
```

The selector matches broker pods only, and `targetBrokerId` picks the broker whose pod name ends in `-0`. A broader selector such as `strimzi.io/cluster=krafter` also matches the KRaft controllers, the Entity Operator and the Kafka exporter, and without `targetBrokerId` the step kills one matching pod at random.

**Response:** `202 Accepted`

```json
{
  "id": "7f8e9d0c",
  "status": "RUNNING",
  "planName": "broker-kill-test"
}
```

Then poll `GET /api/disruptions/{id}` until the status is terminal:

```json
{
  "planName": "broker-kill-test",
  "status": "COMPLETED",
  "stepReports": [{
    "stepName": "kill-broker-0",
    "disruptionType": "POD_KILL",
    "timeToFirstReady": 27.000000000,
    "timeToAllReady": 41.000000000,
    "impactDeltas": { "throughputRecPerSec": -15.6, "p99LatencyMs": 596.7 },
    "rolledBack": false
  }],
  "summary": {
    "totalSteps": 1, "passedSteps": 1, "worstRecovery": 41.000000000,
    "avgThroughputDegradation": 15.6, "maxP99LatencySpike": 596.7, "slaViolated": false
  }
}
```

Disruption IDs are 8-character UUID prefixes. `status` is `RUNNING` while the plan executes, then one of `COMPLETED`, `PARTIAL` (some steps failed), `FAILED`, or `INTERRUPTED` (the process running the plan died; see below). Plans that violate the safety guard are rejected up front and return `422 Unprocessable Entity` with status `REJECTED` (see [Error Responses](#error-responses)). Only one plan may run against a cluster at a time — a `POST` while another plan is in flight returns `409 Conflict`, because concurrent plans race the rollback state stored on the target and can leave the StatefulSet under-scaled.

> **Orphan recovery.** If the Kates pod is killed mid-plan, the injected faults would otherwise persist with nothing left to undo them. On startup Kates removes abandoned `managed-by=kates` NetworkPolicies, restores KafkaNodePools and StatefulSets still carrying a scale-down snapshot, and marks stranded `RUNNING` reports as `INTERRUPTED`. Only faults older than `kates.chaos.orphan-recovery.min-age-sec` (default 900) are touched, so a plan running on another replica is never disturbed.

**Dry run** — `POST /api/disruptions?dryRun=true` with the same request body returns `200 OK`:

```json
{
  "wouldSucceed": true,
  "totalBrokers": 3,
  "steps": [{
    "name": "kill-broker-0",
    "disruptionType": "POD_KILL",
    "targetPod": "krafter-brokers-alpha-0",
    "resolvedLeaderId": null,
    "affectedPods": ["krafter-brokers-alpha-0"],
    "warnings": []
  }],
  "warnings": [],
  "errors": []
}
```

#### GET /api/disruptions

List recent disruption reports. Supports `planName`, `page`, and `size` (default 50) query parameters.

```json
{
  "page": 0,
  "size": 50,
  "count": 1,
  "items": [{
    "id": "7f8e9d0c",
    "planName": "broker-kill-test",
    "status": "COMPLETED",
    "slaGrade": "A",
    "createdAt": "2026-02-15T21:02:45Z"
  }]
}
```

#### GET /api/disruptions/{id}

Get the full disruption report: per-step recovery timings, pod event timeline, pre/post metrics with impact deltas, ISR and consumer-lag tracking, and the SLA verdict (letter grade A/B/C/F).

```json
{
  "planName": "broker-kill-test",
  "status": "COMPLETED",
  "stepReports": [{
    "stepName": "kill-broker-0",
    "disruptionType": "POD_KILL",
    "podTimeline": [
      { "timestamp": "2026-02-15T21:00:15Z", "podName": "krafter-brokers-alpha-0", "eventType": "DELETED", "phase": "Running", "reason": "Killing", "message": "Pod deleted" }
    ],
    "timeToFirstReady": 27.000000000,
    "timeToAllReady": 41.000000000,
    "impactDeltas": { "throughputRecPerSec": -15.6, "p99LatencyMs": 596.7 },
    "rolledBack": false
  }],
  "summary": {
    "totalSteps": 1, "passedSteps": 1, "worstRecovery": 41.000000000,
    "avgThroughputDegradation": 15.6, "maxP99LatencySpike": 596.7, "slaViolated": false
  },
  "slaVerdict": { "grade": "A", "violated": false, "violations": [], "totalChecks": 3, "passedChecks": 3 }
}
```

#### GET /api/disruptions/{id}/timeline

Get pod-level events and recovery times per step.

```json
[
  {
    "step": "kill-broker-0",
    "type": "POD_KILL",
    "events": [
      { "timestamp": "2026-02-15T21:00:15Z", "podName": "krafter-brokers-alpha-0", "eventType": "DELETED", "phase": "Running", "reason": "Killing", "message": "Pod deleted" }
    ],
    "timeToFirstReady": "27000ms",
    "timeToAllReady": "41000ms"
  }
]
```

#### GET /api/disruptions/types

List available disruption types with descriptions. Returns an array of `{ "name": ..., "description": ... }` objects covering: `POD_KILL`, `POD_DELETE`, `NETWORK_PARTITION`, `NETWORK_LATENCY`, `CPU_STRESS`, `MEMORY_STRESS`, `IO_STRESS`, `DNS_ERROR`, `DISK_FILL`, `ROLLING_RESTART`, `LEADER_ELECTION`, `SCALE_DOWN`, `NODE_DRAIN`.

#### GET /api/disruptions/{id}/kafka-metrics

Get Kafka intelligence data captured during the disruption — ISR recovery and consumer-lag metrics per step.

```json
[
  {
    "step": "kill-broker-0",
    "disruptionType": "POD_KILL",
    "isr": { "timeToFullIsr": "41000ms", "minIsrDepth": 2, "underReplicatedPeakCount": 3, "totalPartitions": 36 },
    "lag": { "baselineLag": 0, "peakLag": 12500, "lagSpike": 12500, "timeToLagRecovery": "35000ms" }
  }
]
```

#### GET /api/disruptions/playbooks

List the built-in playbooks. Each entry carries the playbook's `name`, `description`, `category`, and `steps`, the number of steps it has.

```json
[
  { "name": "rolling-restart", "description": "Restart every Kafka pod one at a time through the Strimzi Cluster Operator", "category": "operations", "steps": 1 }
]
```

#### GET /api/disruptions/playbooks/{name}

Get the disruption plan a playbook runs, resolved from its YAML the same way `POST /api/disruptions/playbooks/{name}` resolves it. The plan is named `playbook:<name>`, and each fault carries every field, with the default the playbook runs with wherever the YAML sets none. An unknown name returns `404 Not Found`.

```json
{
  "name": "playbook:rolling-restart",
  "description": "Restart every Kafka pod one at a time through the Strimzi Cluster Operator",
  "steps": [{
    "name": "rolling-restart-brokers",
    "faultSpec": {
      "experimentName": "rolling-restart-sts", "disruptionType": "ROLLING_RESTART",
      "targetNamespace": "kafka", "targetLabel": "strimzi.io/component-type=kafka",
      "targetPod": "", "targetAll": false, "targetBrokerId": -1, "targetTopic": "", "targetPartition": 0,
      "chaosDurationSec": 600, "delayBeforeSec": 0, "gracePeriodSec": 30,
      "networkLatencyMs": 100, "fillPercentage": 80, "cpuCores": 1, "memoryMb": 500, "ioWorkers": 2,
      "envOverrides": {}, "probes": []
    },
    "steadyStateSec": 30, "observationWindowSec": 180, "requireRecovery": true
  }],
  "maxAffectedBrokers": 1, "autoRollback": false,
  "isrTrackingTopic": null, "lagTrackingGroupId": null, "sla": null, "testType": null,
  "baselineDurationSec": 60, "isrPollIntervalMs": 2000, "lagPollIntervalMs": 2000
}
```

The response is a complete disruption plan, the body `POST /api/disruptions` takes. Posted unchanged to `POST /api/disruptions?dryRun=true`, it previews the playbook without injecting a fault:

```bash
BASE="http://localhost:30083"
AUTH="X-API-Key: $KATES_API_KEY"
curl -s -H "$AUTH" "$BASE/api/disruptions/playbooks/leader-cascade" \
  | curl -s -X POST "$BASE/api/disruptions?dryRun=true" -H "$AUTH" -H "Content-Type: application/json" -d @- \
  | jq .
```

#### POST /api/disruptions/playbooks/{name}

Run a playbook. It takes no body and goes through the launcher `POST /api/disruptions` uses: `202 Accepted` with the report id, status `RUNNING`, and the plan name `playbook:<name>`; `422 Unprocessable Entity` with status `REJECTED` when the safety guard refuses the plan; `409 Conflict` while another plan runs against the cluster; `404 Not Found` for an unknown name. This endpoint has no dry run. Preview a playbook through `GET /api/disruptions/playbooks/{name}` and the plan dry run instead.

---

### Resilience Testing

#### POST /api/resilience

Run a combined performance + chaos test. The call is long-running: whitespace is streamed as a keep-alive while the fault runs and recovery is measured, and the JSON report is written after that, usually before the test run itself has finished.

**Request Body:**

```json
{
  "testRequest": {
    "type": "LOAD",
    "spec": { "numRecords": 180000, "throughput": 500, "recordSize": 1024, "acks": "all" }
  },
  "chaosSpec": {
    "experimentName": "kafka-pod-kill",
    "disruptionType": "POD_KILL",
    "targetNamespace": "kafka",
    "targetLabel": "strimzi.io/component-type=kafka,strimzi.io/broker-role=true",
    "chaosDurationSec": 30
  },
  "steadyStateSec": 30
}
```

Optional fields: `probes` (steady-state probe definitions) and `maxRecoveryWaitSec` (default 120).

The workload has to be running when the fault lands. At 500 records/s the 180,000 records take 360 s, while the fault is triggered after `steadyStateSec` (30 s), lasts `chaosDurationSec` (30 s), and the run then waits up to `maxRecoveryWaitSec` for recovery. Without `throughput` the producer runs unthrottled and can finish before the fault is triggered, and both summaries then describe a run the fault never touched. `testRequest` is the body of [POST /api/tests](#post-apitests), so the same fields apply: `throughput` is the rate limit, and LOAD runs one producer and one consumer, so the spec sets no producer count. `disruptionType` picks the fault: on the default LitmusChaos backend `POD_KILL` runs the `pod-delete` experiment, and the outcome reports that name, while `experimentName` only names the ChaosEngine. Without `disruptionType`, Litmus runs the experiment that `experimentName` names, and the `kates-chaos` chart installs none called `kafka-pod-kill`. The selector adds `strimzi.io/broker-role=true` because `strimzi.io/component-type=kafka` alone also matches the KRaft controllers, and the random pick could then kill a controller instead of a broker.

The call returns once the probes pass after the fault, or once `maxRecoveryWaitSec` runs out, so with this example the response usually arrives while the LOAD run is still producing. `postChaosSummary` and `impactDeltas` cover the run up to that moment. The run keeps producing, and keeps its place among the `kates.engine.max-concurrent-tests` running tests, until it reaches `DONE`; its final numbers are then at `GET /api/tests/{id}`, with the id from `performanceReport.run.id`.

**Response** (cut down, with illustrative numbers):

```json
{
  "status": "COMPLETED",
  "chaosOutcome": { "experimentName": "pod-delete", "verdict": "Pass", "chaosDuration": 68.214530000 },
  "impactDeltas": { "throughputRecPerSec": -2.7, "avgLatencyMs": 216.1, "p99LatencyMs": 4891.9, "maxLatencyMs": 8909.6, "errorRate": 0.0 },
  "preChaosSummary": { "avgThroughputRecPerSec": 499.6, "avgLatencyMs": 3.1, "p99LatencyMs": 6.2, "maxLatencyMs": 20.8, "errorRate": 0.0 },
  "postChaosSummary": { "avgThroughputRecPerSec": 486.3, "avgLatencyMs": 9.8, "p99LatencyMs": 309.5, "maxLatencyMs": 1874.0, "errorRate": 0.0 },
  "recoveryTime": 41.027310000
}
```

`status` is one of `COMPLETED`, `CHAOS_FAILED`, `INTERRUPTED`, or `ERROR`. Impact deltas are percentage changes between the pre- and post-chaos summaries. Durations are in seconds: `chaosDuration` runs from the moment Kates creates the fault to its verdict, so on Litmus it includes the experiment's start-up, and `recoveryTime` runs from the verdict until every probe passes, or until `maxRecoveryWaitSec` runs out.

---

### Trend Analysis

#### GET /api/trends

Historical test trends with baseline comparison and regression detection.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `type` | String | (required) | Test type |
| `metric` | String | `avgThroughputRecPerSec` | Summary metric name (e.g. `p99LatencyMs`) |
| `days` | int | 30 | Lookback period |
| `baselineWindow` | int | 5 | Number of runs used for the baseline average |
| `phase` | String | | Restrict analysis to a named test phase |

```json
{
  "testType": "LOAD", "metric": "p99LatencyMs",
  "dataPoints": [
    { "timestamp": "2026-02-09T02:00:14Z", "runId": "aa11bb22", "value": 14.2 },
    { "timestamp": "2026-02-12T02:00:09Z", "runId": "cc33dd44", "value": 15.1 },
    { "timestamp": "2026-02-15T02:00:11Z", "runId": "ee55ff66", "value": 12.3 }
  ],
  "baseline": 13.9,
  "regressions": []
}
```

---

### Scheduling

#### POST /api/schedules

Create a recurring test schedule. Cron expressions use the 5-field Unix format (`minute hour day-of-month month day-of-week`, evaluated in UTC).

**Request Body:**

```json
{
  "name": "Nightly Load Regression",
  "cronExpression": "0 2 * * *",
  "enabled": true,
  "testRequest": { "type": "LOAD", "spec": { "numRecords": 100000, "acks": "all" } }
}
```

**Response:** `201 Created`

```json
{
  "id": "e5f6a7b8",
  "name": "Nightly Load Regression",
  "cronExpression": "0 2 * * *",
  "enabled": true,
  "lastRunId": null,
  "lastRunAt": null
}
```

Schedule IDs, like run IDs, are 8-character UUID prefixes.

#### GET /api/schedules

List all schedules.

```json
[{
  "id": "e5f6a7b8",
  "name": "Nightly Load Regression",
  "cronExpression": "0 2 * * *",
  "enabled": true,
  "lastRunId": "d4e5f6a7",
  "lastRunAt": "2026-02-15T02:00:00Z"
}]
```

#### GET /api/schedules/{id}

Get a single schedule, including the ID and time of its last triggered run.

#### PUT /api/schedules/{id}

Update a schedule. Accepts the same body as `POST /api/schedules` (fields you omit keep their current values, except `enabled`, which is always applied). Returns the updated schedule.

#### DELETE /api/schedules/{id}

Delete a schedule. Returns `204 No Content`.

---

## Error Responses

Errors follow a consistent JSON format:

```json
{ "status": 404, "error": "Not Found", "message": "Test run not found: abc123" }
```

The one exception is the disruption safety-guard rejection (`422`), which returns the shape shown in the examples below.

### HTTP Error Codes

| Status | Error | Description | Common Causes |
|:---:|-------|-------------|---------------|
| 400 | Bad Request | Malformed or invalid request | Invalid `type`, missing required fields, malformed JSON |
| 401 | Unauthorized | Missing API key | Security enabled and no `Authorization`/`X-API-Key` header sent |
| 403 | Forbidden | Invalid API key | Key does not match `kates.api.key` |
| 404 | Not Found | Resource does not exist | Unknown test ID, deleted report, non-existent schedule |
| 409 | Conflict | Conflicts with current state | Cancelling a test that is not running; starting a disruption while one is already running |
| 422 | Unprocessable Entity | Rejected by safety guards | `maxAffectedBrokers` exceeded, plan would affect all brokers |
| 500 | Internal Server Error | Unexpected server failure | Kafka admin call failed, cluster unreachable |
| 503 | Service Unavailable | Dependent system unavailable | Kubernetes API not reachable |

### Error Examples

**400 — Invalid test type:**
```json
{ "status": 400, "error": "Bad Request", "message": "Invalid test type: BENCHMARK" }
```

**409 — Cancelling a test that is not running:**
```json
{ "status": 409, "error": "Conflict", "message": "Test is not running (status: DONE)" }
```

**422 — Disruption plan rejected by the safety guard:**
```json
{
  "id": "7f8e9d0c",
  "status": "REJECTED",
  "validationWarnings": ["ERROR: Plan would affect ALL 3 brokers — cluster would lose availability"]
}
```

---

## API Workflows

The following `curl`-based workflows demonstrate common multi-step operations. All examples assume the API is forwarded to `localhost:30083` (see [Base URL](#base-url)) and `KATES_API_KEY` holds the key (see [Authentication](#authentication)); with `set -u`, a script stops at its `AUTH=` line if the variable is unset.

### Workflow 1: Create a Test, Poll for Completion, Get the Report

```bash
#!/usr/bin/env bash
set -euo pipefail
BASE="http://localhost:30083"
AUTH="X-API-Key: $KATES_API_KEY"

# Create a load test
TEST_ID=$(curl -s -X POST "$BASE/api/tests" -H "$AUTH" \
  -H "Content-Type: application/json" \
  -d '{"type":"LOAD","spec":{"numRecords":100000,"acks":"all","topic":"perf-test","partitions":3,"replicationFactor":3}}' \
  | jq -r '.id')
echo "Created test: $TEST_ID"

# Poll until complete
while true; do
  STATUS=$(curl -s -H "$AUTH" "$BASE/api/tests/$TEST_ID" | jq -r '.status')
  echo "Status: $STATUS"
  [[ "$STATUS" == "DONE" || "$STATUS" == "FAILED" ]] && break
  sleep 5
done

# Fetch report and export as JUnit XML
curl -s -H "$AUTH" "$BASE/api/tests/$TEST_ID/report" | jq .
curl -s -H "$AUTH" "$BASE/api/tests/$TEST_ID/report/junit" -o report.xml
```

### Workflow 2: Validate and Execute a Disruption

Disruption execution is asynchronous. The `POST` validates the plan, returns `202 Accepted` with a report id, and runs the steps in the background — poll `GET /api/disruptions/{id}` until the status is terminal. Only one plan runs against a cluster at a time; a second `POST` while one is in flight is refused with `409 Conflict`.

```bash
#!/usr/bin/env bash
set -euo pipefail
BASE="http://localhost:30083"
AUTH="X-API-Key: $KATES_API_KEY"
PLAN='{"name":"broker-kill-test","maxAffectedBrokers":1,"autoRollback":true,"steps":[{"name":"kill-broker-0","faultSpec":{"experimentName":"broker-kill","disruptionType":"POD_KILL","targetNamespace":"kafka","targetLabel":"strimzi.io/component-type=kafka,strimzi.io/broker-role=true","targetBrokerId":0,"chaosDurationSec":30},"steadyStateSec":15,"observationWindowSec":60,"requireRecovery":true}]}'

# Dry-run to validate (no faults injected)
curl -s -X POST "$BASE/api/disruptions?dryRun=true" -H "$AUTH" -H "Content-Type: application/json" -d "$PLAN" | jq .

# Execute — returns 202 immediately with the report id
DISRUPT_ID=$(curl -s -X POST "$BASE/api/disruptions" -H "$AUTH" -H "Content-Type: application/json" -d "$PLAN" | jq -r '.id')
echo "Disruption started: $DISRUPT_ID"

# Poll until the report reaches a terminal status
while true; do
  STATUS=$(curl -s -H "$AUTH" "$BASE/api/disruptions/$DISRUPT_ID" | jq -r '.status')
  echo "Status: $STATUS"
  case "$STATUS" in COMPLETED|PARTIAL|FAILED|REJECTED|INTERRUPTED) break ;; esac
  sleep 10
done

# Inspect recovery timings and Kafka impact
curl -s -H "$AUTH" "$BASE/api/disruptions/$DISRUPT_ID/timeline" | jq '.[] | {step, timeToFirstReady, timeToAllReady}'
curl -s -H "$AUTH" "$BASE/api/disruptions/$DISRUPT_ID/kafka-metrics" | jq '.[] | {step, isr, lag}'
```

### Workflow 3: Export Results in Different Formats

```bash
#!/usr/bin/env bash
set -euo pipefail
BASE="http://localhost:30083"
AUTH="X-API-Key: $KATES_API_KEY"
TEST_ID="${1:?usage: $0 <test-id>}"   # an ID from kates test list or GET /api/tests

curl -s -H "$AUTH" "$BASE/api/tests/$TEST_ID/report"                   -o report.json
curl -s -H "$AUTH" "$BASE/api/tests/$TEST_ID/report/csv"                -o report.csv
curl -s -H "$AUTH" "$BASE/api/tests/$TEST_ID/report/junit"              -o report.xml
curl -s -H "$AUTH" "$BASE/api/tests/$TEST_ID/report/heatmap?format=json" -o heatmap.json
curl -s -H "$AUTH" "$BASE/api/tests/$TEST_ID/report/heatmap?format=csv"  -o heatmap.csv

echo "Exported: report.json, report.csv, report.xml, heatmap.json, heatmap.csv"
jq '{type:.run.testType, throughput:.summary.avgThroughputRecPerSec, p99:.summary.p99LatencyMs, sla:.overallSlaVerdict.passed}' report.json
```

::: {.callout-tip}
**Try it**

Walk the core loop against a running backend — no CLI involved. With `make ports` running:

```bash
BASE="http://localhost:30083"
export KATES_API_KEY="$(kubectl get secret kates-api-key -n kates -o jsonpath='{.data.api-key}' | base64 -d)"

# Public endpoint — answers without any key
curl -s "$BASE/api/health" | jq '{status, kafka: .kafka.status}'

# Ask the API which test types it accepts
curl -s -H "X-API-Key: $KATES_API_KEY" "$BASE/api/tests/types" | jq .

# Create a small ROUND_TRIP test, then poll it
TEST_ID=$(curl -s -X POST "$BASE/api/tests" -H "X-API-Key: $KATES_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"type":"ROUND_TRIP","spec":{"numRecords":1000}}' | jq -r '.id')
curl -s -H "X-API-Key: $KATES_API_KEY" "$BASE/api/tests/$TEST_ID" | jq '.status'
```

The health check works with no key, the create call returns `202 Accepted` with an 8-character run ID, and re-running the last command shows `status` advancing from `PENDING` through `RUNNING` to `DONE`.
:::

---

## See Also

- [CLI Reference](10-cli-reference.md) — Interactive CLI commands that wrap this REST API
- [Scenario Files & SLA Gates](13-scenario-files.md) — YAML scenario definitions used with test creation endpoints
- [gRPC API Reference](16-grpc-api.md) — Strongly typed alternative for CI/CD and service-to-service integration

## Summary

- Every CLI action maps to a REST endpoint — anything you do interactively can be scripted with `curl` and `jq` against the same backend service layer
- Authentication is on by default: pass the key as `Authorization: Bearer` or `X-API-Key`, expect `401` without one and `403` with a wrong one; only `/api/health`, `/openapi`, and everything under `/q/` stay public
- Test execution and disruption execution are both asynchronous — the `POST` returns `202 Accepted` with an id and you poll `GET /api/tests/{id}` or `GET /api/disruptions/{id}` until the status is terminal
- One report feeds many consumers: JSON for dashboards, CSV for spreadsheets, JUnit XML for CI gates, and heatmap data for latency visualization
- This chapter covers the core endpoint families only; the complete, always-current spec lives at `/q/openapi`

When shell scripts hit their limits — typed clients, service-to-service calls — the same operations are available over protocol buffers in [gRPC API Reference](16-grpc-api.md).
