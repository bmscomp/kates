---
toc-depth: 4
---

# REST API Reference

## Introduction

The Kates API, the service in the cluster that runs your tests ([Architecture & Design](02-architecture.md)), serves this RESTful API, which the CLI and other clients use to manage tests, reports, and disruptions. Most CLI commands call it, so a script can do over HTTP what they do. The commands that install and reach the stack run `kubectl` and `helm` instead, and a few only read local files, as the [Commands](10-cli-reference.md#commands) table in CLI Reference shows. The REST API is what scripts, CI/CD integration and custom dashboards build on.

**When should you choose the REST API over the CLI or gRPC?** Use the REST API when you need to integrate Kates into shell scripts, automation pipelines, or monitoring systems that work best with JSON over HTTP. It is ideal for `curl`-based workflows, webhook integrations, and any tool that speaks HTTP natively. The API is human-readable and easy to debug — every request and response is plain JSON, so you can inspect traffic with standard tools like `curl`, `httpie`, or browser dev-tools.

If you need strongly typed clients or high-throughput programmatic access from Go, Java, or Python services, consider the [gRPC API](16-grpc-api.md) instead; it covers test runs, cluster inspection, and health. If you prefer an interactive experience with formatted output, the [CLI](10-cli-reference.md) is the best choice. All three interfaces reach the same Kates API, so a test run is the same run whichever one started it; [gRPC API Reference](16-grpc-api.md) lists the places where gRPC responses carry less than REST.

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
| `Content-Type` | `application/json` | Yes (POST/PUT) | Request body format |
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

This chapter documents the most commonly used endpoints in the core resource families: health, tests, reports, cluster inspection, disruptions, resilience, trends, and schedules. The Kates API exposes more than is listed here — bulk operations, test cancellation, baselines, report comparison and markdown export, disruption templates and schedules, plus entire resource families (webhooks, events, cost, advisor, audit, profiles, security, Kafka client tooling, DLQ, share groups). The complete, always-current machine-readable specification is generated from the code by MicroProfile OpenAPI and served at `/q/openapi` (Swagger UI is available at `/q/swagger-ui` in dev mode).

### Health & System

Call this endpoint first to learn whether the Kates API is up and can reach Kafka; `kates health` shows the same answer.

#### GET /api/health

System health check including Kafka connectivity and the benchmark backends: `engine.activeBackend` is the one a test uses when it names none. This endpoint is public — no API key required.

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

These endpoints start performance test runs, then find, follow and delete them. A run executes in the background, so you create it and then poll it; `kates test` wraps these calls, and [Test Types Deep Dive](05-test-types.md) explains what each type measures.

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
| `type` | String | Yes | Test type — any value returned by `GET /api/tests/types` (LOAD, STRESS, SPIKE, ENDURANCE, VOLUME, CAPACITY, ROUND_TRIP, INTEGRITY, the TUNE_* family, INTEGRATION_CDC) |
| `backend` | String | | Benchmark backend: `native` or `trogdor` (default: `native`) |
| `spec` | Object | | Test specification overrides |

::: {.callout-important}
The Kates API merges `spec` with the defaults of the test type: a field the request sets wins, and the type's default fills each one it leaves out. What the fields do:

- `throughput` is the rate each producer honours, in records per second, and -1 is unlimited. `targetThroughput` is another name for it, the one `kates test create --throughput` and scenario files send. When both are set, `throughput` wins; when only `targetThroughput` is, it sets the rate in place of the type's default. In the merged `spec`, `throughput` is the rate the run used.
- `consumerGroup` names the consumer's group, and must not be empty or blank. A group that already has committed offsets on the topic resumes from them, and a LOAD or ENDURANCE consumer commits offsets as it reads, so a group an application uses would rebalance and lose its place: give a test a group of its own. An INTEGRITY run's consumer joins the name with `-integrity` appended; without one it is `integrity-cg-integrity`, and a LOAD or ENDURANCE consumer gets a group named after its task.
- `fetchMinBytes` and `fetchMaxWaitMs` become the consumer's `fetch.min.bytes` and `fetch.max.wait.ms`.
- `enableIdempotence` sets the producer's `enable.idempotence`, `false` included. Left out, the Kafka client decides, and it turns idempotence on whenever `acks` is `all`.
- `enableTransactions` makes every producer of the run transactional, committing every 100 records or every 10 seconds, whichever comes first, so a slow producer stays inside the client's 60-second transaction timeout; a LOAD or ENDURANCE consumer then reads with `read_committed`, as the INTEGRITY consumer does.
- `enableCrc` turns an INTEGRITY run's CRC check of each record on or off.
- `numProducers` sets the number of producers for STRESS and CAPACITY only, and no test type reads `numConsumers`, so a LOAD run has one producer and one consumer whatever the spec says.

The merged `spec` holds every field the type has a default for. `targetThroughput`, `consumerGroup`, the fetch settings and the three `enable` options have none, and appear in it only when the request set them, so an `enableIdempotence: false` there is always the request's.

On the `trogdor` benchmark backend, the producer settings (`acks`, `batchSize`, `lingerMs`, `compressionType`, `enableIdempotence`) and the fetch settings go into the Trogdor spec's `producerConf` and `consumerConf`. A Trogdor run stored before the Kates API kept the request, one without `requestedSpec`, ran with the Kafka client's defaults whatever those settings said, so its results do not compare with a later Trogdor run of the same spec.
:::

A field the run could not honour is refused rather than ignored: the answer is `400` with `error` `Validation Failed`, a `message` that names each field, and `fieldErrors`, one entry per field with the reason. A value that asks for nothing passes, because the run honours it anyway, such as `throughput: -1` for SPIKE, `enableCrc: false` for LOAD or `enableIdempotence: false` for INTEGRATION_CDC. So the `spec` of a run that has a `requestedSpec` is valid input again, and can be sent back as a request; an older run's `spec` holds fields the Kates API then ignored (see `GET /api/tests/{id}` below), which is why `kates replay` leaves them out.

| Field | Refused when |
|-------|--------------|
| `throughput`, `targetThroughput` | Any value but -1 for SPIKE and CAPACITY, which run their producers unthrottled, and for INTEGRATION_CDC, which runs no Kates producer |
| `consumerGroup`, `fetchMinBytes`, `fetchMaxWaitMs` | Every type but LOAD, ENDURANCE and INTEGRITY: ROUND_TRIP's consumer uses no group and the client's fetch defaults, and the rest start none |
| `enableCrc` | `true` for any type but INTEGRITY, the only one that checks CRCs |
| `enableIdempotence` | `true` when the run's `acks`, the request's or the type's default (SPIKE's is `1`), is not `all`, or for INTEGRATION_CDC |
| `enableTransactions` | `true` when `acks` is not `all`, when the request sets `enableIdempotence: false`, on the `trogdor` benchmark backend, or for INTEGRATION_CDC |
| `durationMs` | The run would last longer than `kates.engine.max-duration-ms`, two hours by default: its `durationMs`, or the type's default without one, twice that for INTEGRITY, which reads its records back for as long again |

A request with a `scenario` and its `phases` is checked the same way. Its `baseSpec` and each phase's `spec` are held to the same limits as the request's `spec`, such as a `numRecords` of at least 1 and a `topic` that is a legal Kafka topic name. A value outside them is refused first, keyed by its path in the scenario, such as `phases[0].spec.numRecords`. Each phase starts producers only, so `consumerGroup`, the fetch settings and `enableCrc: true` are refused in its `baseSpec` or in a phase's `spec`, with `fieldErrors` keyed by their path in the scenario, such as `baseSpec.consumerGroup` or `phases[0].spec.fetchMinBytes`. The producer options reach every phase, checked against the `acks` each phase runs with, and a phase's rate follows the rule above: its `throughput`, or its `targetThroughput` without one. A scenario whose phases' durations add up to more than `kates.engine.max-duration-ms` is refused with `fieldErrors` keyed `phases`. A `null` in `phases` is refused with `fieldErrors` keyed by its index, such as `phases[1]`.

A phase has a `name`, a `phaseType`, a `spec` of its own, and its own `durationMs` and `targetThroughput`; a RAMP phase also takes `rampSteps`. The Kates API ignores any other field in a phase, so a spec field such as `topic` goes in the phase's `spec` or in the `baseSpec`. The `phaseType`, one of WARMUP, RAMP, STEADY, SPIKE or COOLDOWN, decides the producers a phase starts.

The phases run one after another, in the order sent. Each starts once the durations of the phases before it have passed, even when they stopped early at their `numRecords`, so a scenario lasts its phases' durations added up. Until its turn comes, a phase's tasks show as `PENDING` in the run's `results`, each with the `startTime` it is due to start at. A phase's spec is the `baseSpec` as sent with the phase's own fields over it, and none of the test type's defaults, so a WARMUP, STEADY or COOLDOWN phase that no rate reaches runs unthrottled. A SPIKE phase runs unthrottled whatever its rate. A RAMP phase runs `rampSteps` producers in turn, each for an equal share of the phase's duration: the first at its rate divided by `rampSteps`, the next at twice that, and so on up to the rate. A phase without a `phaseType` is refused, as is one with settings its type couldn't honour, each keyed by the field to change:

| Field | Refused when |
|-------|--------------|
| `phases[i].phaseType` | The phase has none |
| `phases[i].rampSteps` | Any value but 1 in a WARMUP, STEADY, SPIKE or COOLDOWN phase, which starts one producer |
| `phases[i].targetThroughput`, `phases[i].spec.throughput`, `phases[i].spec.targetThroughput` | Any value but -1 in a SPIKE phase; a rate it takes from the `baseSpec` passes |
| `phases[i].targetThroughput` | A RAMP phase has no rate, or one below 1 |
| `phases[i].rampSteps` | A RAMP phase has under 1 or over 100 steps, or more than its rate in rec/s, since each step needs at least 1 rec/s |

```json
{
  "status": 400,
  "error": "Validation Failed",
  "message": "spec.consumerGroup: STRESS starts no consumer; only LOAD, ENDURANCE and INTEGRITY do",
  "fieldErrors": {
    "consumerGroup": "STRESS starts no consumer; only LOAD, ENDURANCE and INTEGRITY do"
  }
}
```

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
    "replicationFactor": 3, "partitions": 3, "minInsyncReplicas": 2
  },
  "requestedSpec": {
    "numRecords": 100000, "recordSize": 1024, "acks": "all", "topic": "perf-test", "partitions": 3,
    "replicationFactor": 3, "minInsyncReplicas": 2, "durationMs": 120000, "throughput": -1
  },
  "createdAt": "2026-02-15T20:00:00Z"
}
```

The `spec` in the response is the merged one: the request's values, and the LOAD defaults for everything it leaves out that LOAD has a default for. `requestedSpec` is the request's own `spec`, only the fields it set, so the two tell a requested value from a default; a request without a `spec` gets an empty one. The Kates API stores both, and `kates replay` sends `requestedSpec` back to start the run again.

Run IDs are 8-character UUID prefixes. `status` moves through `PENDING` and `RUNNING`, and ends at `DONE` or `FAILED`. There is no cancelled status: `POST /api/tests/{id}/cancel` stores the run as `FAILED` and answers `{"id": ..., "status": "FAILED", "reason": "cancelled", ...}`, and each task it stopped carries the error `Cancelled by user`. The cancel also ends the run's workers and gives back its place among the `kates.engine.max-concurrent-tests` running tests. A run that finishes on its own while the cancel is being made keeps its own ending, and the cancel answers `409`.

A run still `RUNNING` five minutes (`kates.engine.reaper-grace-ms`) after the time it was set to last, counted from its creation, is stopped and stored as `FAILED` too. That time is its `durationMs`, twice that for INTEGRITY, or a scenario's phases added up; INTEGRATION_CDC, which has no duration of its own, gets `kates.engine.max-duration-ms`. Each task that had not finished carries an error that starts `Timeout:`, and the tasks that had keep their results.

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

**Response:** `200 OK` (`spec` and `requestedSpec` cut down to four fields; they are the merged spec and the request's own fields shown under [POST /api/tests](#post-apitests)). A run stored before the Kates API kept the request has no `requestedSpec`, and its `spec` shows `targetThroughput`, `consumerGroup`, the fetch settings and the three `enable` options at their Java defaults whatever the request said: the Kates API dropped them then, and the run went without them.

```json
{
  "id": "a1b2c3d4",
  "testType": "LOAD",
  "status": "DONE",
  "backend": "native",
  "spec": { "topic": "perf-test", "numRecords": 100000, "recordSize": 1024, "acks": "all" },
  "requestedSpec": { "topic": "perf-test", "numRecords": 100000, "recordSize": 1024, "acks": "all" },
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

Stop and delete a test run with its results. A run that is still `PENDING` or `RUNNING` is stopped first: its tasks stop, and it gives back its place among the `kates.engine.max-concurrent-tests` running tests. Its end is then announced as a failure, as a cancelled run's is: webhooks get its `test.completed` event with status `FAILED`, and `GET /api/events/stream` sends a `failed` event whose detail is `deleted`. Deleting a run that has already ended announces nothing. To stop a run and keep it, cancel it with `POST /api/tests/{id}/cancel` instead.

**Response:** `204 No Content` on success. Returns `404 Not Found` if the test ID does not exist.

---

### Reports

These endpoints return a run's results as one JSON report, or export them as CSV, JUnit XML or latency heatmap data; `kates report show` and `kates report export` read them.

#### GET /api/tests/{id}/report

Get the full test report: the summary, the cluster snapshot, per-broker figures and `overallSlaVerdict`, which says whether the run met its SLA thresholds. The per-broker figures split the run's throughput by each broker's share of the topic's partition leaders when the report was built, not during the run. The Kates API builds a finished run's report the first time something asks for it and keeps it in memory, among the 200 reports it has used most recently. Once the report drops out of those, or the Kates API restarts, the next request builds it again with a new cluster snapshot.

```json
{
  "run": { "id": "a1b2c3d4", "testType": "LOAD", "status": "DONE" },
  "summary": {
    "totalRecords": 200000,
    "avgThroughputRecPerSec": 8405.4, "peakThroughputRecPerSec": 8412.7, "avgThroughputMBPerSec": 8.2,
    "avgLatencyMs": 2.4, "p50LatencyMs": 1.8, "p95LatencyMs": 5.6, "p99LatencyMs": 12.3,
    "p999LatencyMs": 0.0, "maxLatencyMs": 34.1,
    "totalErrors": 0, "errorRate": 0.0, "durationMs": 0
  },
  "phases": [
    { "phaseName": "produce", "metrics": { "p99LatencyMs": 12.3 } },
    { "phaseName": "consume", "metrics": { "p99LatencyMs": 0.0 } }
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

When an SLA is violated, `overallSlaVerdict.violations` contains entries of the form `{ "metric": "p99LatencyMs", "threshold": 500.0, "actual": 612.4, "severity": "CRITICAL" }`. Two kinds of entry carry a `reason` instead of a number, and -1 where the number would be:

| Entry | When | `reason` |
|-------|------|----------|
| A latency gate: `p99LatencyMs`, `p999LatencyMs` or `avgLatencyMs` | No task measured that latency | `not measured` |
| `status`, always the first entry | The run is `FAILED`, with or without an SLA | `FAILED:` followed by the first task error, or `FAILED before any task ran` |

A run that is not yet `DONE` or `FAILED` carries a passing verdict, since figures that are still climbing are not judged.

The summary is computed from the run's task rows, and the example is a LOAD run of 100,000 records: one producer row and one consumer row. The table shows how each summary field combines the rows.

| Field | How the task rows combine |
|-------|---------------------------|
| `totalRecords` | Summed over every task, producer and consumer alike |
| `avgThroughputRecPerSec`, `avgThroughputMBPerSec` | The mean of the rates of the tasks that have started, not their sum |
| `peakThroughputRecPerSec` | The fastest task's rate |
| `p50LatencyMs`, `p95LatencyMs`, `p99LatencyMs` | The producer's; with several producers, the highest of theirs |
| `avgLatencyMs` | The producers' mean latency, weighted by their records |
| `maxLatencyMs` | The slowest producer's |
| `totalErrors`, `errorRate` | Tasks that ended with an error, and that count divided by `totalRecords` |
| `p999LatencyMs`, `durationMs` | Always 0 |

A task that has not started is `PENDING` with no records, as a scenario's later phase is until its turn, and the means leave it out. So while a scenario runs, its throughput is that of the phases under way or done, and a phase that has not started reads 0 in `phases`. A task that a cancel or a failure ends before its turn is stored `FAILED`, and counts as a rate of 0.

Latency leaves consumers out because they don't measure it: a consumer's row carries no latency on the native backend and only poll times on Trogdor. A LOAD or ENDURANCE run's P99 is therefore its producer's send-to-acknowledgement P99. A task keeps its percentiles but not its latency histogram, so the percentiles of several producers cannot be merged. The highest of them is an upper bound on the run's percentile, so it never understates the tail. `kates report show`, `report diff`, `report compare`, the regression check, `kates trend` and the resilience comparison all read this summary.

When no task measured latency, every latency field reads 0, which means not measured, and the verdict fails a latency gate instead of comparing it with that 0. A task keeps no P99.9, so an SLA's `maxP999LatencyMs` fails as not measured on every finished run.

#### GET /api/tests/{id}/report/csv

Export report as CSV. **Response:** `200 OK` with `Content-Type: text/csv`

```text
runId,testType,backend,phase,recordsSent,throughputRecPerSec,throughputMBPerSec,avgLatencyMs,p50LatencyMs,p95LatencyMs,p99LatencyMs,maxLatencyMs,error
a1b2c3d4,LOAD,native,ramp-up,10000,5234.1,5.1,4.8,3.2,9.6,18.4,45.2,
a1b2c3d4,LOAD,native,steady-state,90000,8412.7,8.2,2.4,1.8,5.6,12.3,34.1,
```

A `# Summary` block with aggregate metrics is appended after the per-result rows.

#### GET /api/tests/{id}/report/junit

Export report as JUnit XML for CI/CD integration. Each test result maps to a `<testcase>`, with a `<failure>` when its task ended with an error. Each `overallSlaVerdict` violation follows as a `<testcase>` named `SLA-<metric>` with a `<failure>`, so a `FAILED` run always has `SLA-status`. The `tests` and `failures` attributes count every test case and every failure, and a run with no task and no violation is itself the one test case. **Response:** `200 OK` with `Content-Type: application/xml`, or `409 Conflict` with a JSON error until the run is `DONE` or `FAILED`: before then its verdict passes, so an early export would read as green.

```xml
<?xml version="1.0" encoding="UTF-8"?>
<testsuite name="LOAD" tests="2" failures="0" errors="0">
  <testcase name="ramp-up" classname="kates.LOAD" time="15.000"/>
  <testcase name="steady-state" classname="kates.LOAD" time="110.000"/>
</testsuite>
```

A run that failed before any task ran, such as one whose topic could not be created, exports one failing test case:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<testsuite name="LOAD" tests="1" failures="1" errors="0">
  <testcase name="SLA-status" classname="kates.sla">
    <failure message="status FAILED before any task ran" type="SlaViolation"/>
  </testcase>
</testsuite>
```

#### GET /api/tests/{id}/report/heatmap

Export latency heatmap data. Returns `404` with a plain-text message when the Kates API holds no heatmap for the run: a `trogdor` run, a native run older than its 50 most recent, or a run from before the pod last started.

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

*(Arrays truncated for readability — the real heatmap uses 25 latency buckets spanning 0 ms to 10 s.)*

The Kates API adds a row each time it polls a running native-backend test, every 5 s by default and on each read of the run, and the row counts everything recorded so far in the 25 buckets. A run with several tasks gets one row per running task at each poll, each counting that task's records, and `phase` is the task's scenario phase or, outside a scenario, its workload, such as `produce` or `consume`. [Latency Heatmaps](09-observability.md#latency-heatmaps) in Observability & Monitoring explains how to read one.

---

### Cluster Inspection

These endpoints describe the Kafka cluster the Kates API is connected to — its brokers, topics, consumer groups and broker configuration — and change nothing; `kates cluster` shows the same data.

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

Topic detail with partition assignments, ISR, and eight configuration keys: `cleanup.policy`, `retention.ms`, `retention.bytes`, `min.insync.replicas`, `compression.type`, `segment.bytes`, `max.message.bytes` and `message.timestamp.type`. `configs` holds the value in force of each, wherever it is set, and `configSources` says what set it, as Kafka names the source: `DYNAMIC_TOPIC_CONFIG` for the topic, `STATIC_BROKER_CONFIG` or `DYNAMIC_BROKER_CONFIG` for a broker, `DYNAMIC_DEFAULT_BROKER_CONFIG` for the cluster-wide default, `DEFAULT_CONFIG` for Kafka's default. On the `krafter` cluster, `min.insync.replicas` comes from the brokers. `GET /api/kafka/topics/{name}` answers the same.

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
  "configs": { "retention.ms": "604800000", "min.insync.replicas": "2", "cleanup.policy": "delete" },
  "configSources": { "retention.ms": "DYNAMIC_TOPIC_CONFIG", "min.insync.replicas": "STATIC_BROKER_CONFIG", "cleanup.policy": "STATIC_BROKER_CONFIG" }
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

A disruption plan injects faults into the Kafka cluster step by step, measures how the cluster recovers and, when the plan's `sla` block sets a threshold, grades the result against it; a playbook is a ready-made plan with no `sla` block, so it gets no grade. `kates disruption` wraps these endpoints, and [Chaos Engineering in Practice](07-chaos-practice.md) explains plans, the safety guard and the grades.

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

Disruption IDs are 8-character UUID prefixes. `status` is `RUNNING` while the plan executes, then one of `COMPLETED`, `PARTIAL` (some steps failed), `FAILED`, or `INTERRUPTED` (the process running the plan died; see below). Plans that violate the safety guard are rejected up front and return `422 Unprocessable Entity` with status `REJECTED` (see [Error Responses](#error-responses)). Only one plan may run against a cluster at a time: a `POST` while another plan is in flight returns `409 Conflict`. Concurrent plans would race the rollback state stored on the target and could leave a node pool or StatefulSet under-scaled. [Safety Guardrails](07-chaos-practice.md#safety-guardrails) in Chaos Engineering in Practice lists every reason the safety guard refuses a plan.

> **Orphan recovery.** If the Kates API pod is killed mid-plan, the injected faults would otherwise persist with nothing left to undo them. On startup Kates removes abandoned `managed-by=kates` NetworkPolicies in the Kafka namespace (`kates.chaos.kafka.namespace`), restores the KafkaNodePools and StatefulSets there that still carry a scale-down snapshot, and marks stranded `RUNNING` reports as `INTERRUPTED`. It runs once, at startup, and touches only faults older than `kates.chaos.orphan-recovery.min-age-sec` (default 900), so a fault younger than that when Kates restarts stays until you remove it or Kates restarts again.

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

Get the full disruption report: per-step recovery timings, pod event timeline, pre/post metrics with impact deltas, ISR and consumer-lag tracking, and the SLA grade when the plan has an `sla` block. The grade, in `slaVerdict.grade`, is A, B, C, D or F, or `-` when no constraint could be evaluated.

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

List available disruption types with descriptions. Returns an array of `{ "name": ..., "description": ... }` objects covering: `POD_KILL`, `POD_DELETE`, `NETWORK_PARTITION`, `NETWORK_LATENCY`, `CPU_STRESS`, `MEMORY_STRESS`, `IO_STRESS`, `DNS_ERROR`, `DISK_FILL`, `ROLLING_RESTART`, `LEADER_ELECTION`, `SCALE_DOWN`, `NODE_DRAIN`. The list is the same whatever the chaos provider; [Chaos Engineering in Practice](07-chaos-practice.md) says which types each provider runs.

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

A resilience run starts a test run, injects one fault while it runs, and compares a snapshot of the run taken before the fault with a summary of the whole run taken after the recovery wait; `kates resilience run` sends this request. A disruption plan starts no test run and measures the cluster itself; [Chaos Engineering in Practice](07-chaos-practice.md) explains both. A resilience run doesn't go through the safety guard: nothing counts the brokers its fault hits, and nothing rolls it back. Its fault is held to the same [Fault Parameter Limits](07-chaos-practice.md#fault-parameter-limits) as a plan's, though.

#### POST /api/resilience

Start a test run and inject one fault while it runs. The call is long-running: whitespace is streamed as a keep-alive while the fault runs and recovery is measured, and the JSON report is written after that, usually before the test run itself has finished.

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

The workload has to be running when the fault lands. At 500 records per second the 180,000 records take 360 s, while the fault is triggered after `steadyStateSec` (30 s), lasts `chaosDurationSec` (30 s), and the run then waits up to `maxRecoveryWaitSec` for recovery. Without `throughput` the producer runs unthrottled and can finish before the fault. A run that has ended when `steadyStateSec` is up gets no fault, and the report is `ERROR`; one that ends after that, before the fault goes in, leaves both summaries describing a run the fault never touched. `testRequest` is the body of [POST /api/tests](#post-apitests), so the same fields apply: `throughput` is the rate limit, and LOAD runs one producer and one consumer, so the spec sets no producer count. `disruptionType` picks the fault: on the default `litmus-crd` chaos provider `POD_KILL` runs the LitmusChaos `pod-delete` experiment, and the outcome reports that name, while `experimentName` only names the ChaosEngine. Without `disruptionType`, Litmus runs the experiment that `experimentName` names, and the `kates-chaos` chart installs none called `kafka-pod-kill`. The selector adds `strimzi.io/broker-role=true` because `strimzi.io/component-type=kafka` alone also matches the KRaft controllers, and the random pick could then kill a controller instead of a broker.

The call returns once the probes pass after the fault, or once `maxRecoveryWaitSec` runs out, so with this example the response usually arrives while the LOAD run is still producing. `postChaosSummary` and `impactDeltas` cover the run up to that moment. When `testRequest` is a scenario, a phase still waiting for its turn is left out of the throughput of both summaries. The run keeps producing, and keeps its place among the `kates.engine.max-concurrent-tests` running tests, until it reaches `DONE`; its final numbers are then at `GET /api/tests/{id}`, with the id from `performanceReport.run.id`.

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

A `testRequest` that `POST /api/tests` would refuse for a field its type or benchmark backend cannot apply is refused here too, with the same `400` and `fieldErrors`, before the stream starts and before any fault is injected. The same goes for a `null` in a scenario's `phases`, and for a `testRequest` without a `type`, keyed `type`, unless its `scenario` has a `type` of its own. So is a `testRequest` whose `spec` has a value outside the limits `POST /api/tests` sets, such as a `numRecords` below 1 or a `topic` that isn't a legal Kafka topic name. As on `POST /api/tests`, a scenario's `baseSpec` and each phase's `spec` are held to the same limits, with `fieldErrors` keyed by their path in the scenario, such as `phases[0].spec.numRecords`. A `chaosSpec` with a parameter outside the fault parameter limits is refused the same way: `fieldErrors` names each parameter, and `message` prefixes it with `chaosSpec.`.

`status` is one of `COMPLETED`, `CHAOS_FAILED`, `INTERRUPTED`, or `ERROR`; with `ERROR`, `error` says why, for example that the benchmark did not start. Kates injects the fault only into a test run that is `RUNNING`, and `steadyStateSec` counts from the moment the run's tasks are submitted. A run that has failed or finished by then, or when `steadyStateSec` is up, gets neither the fault nor the probes: `error` says the benchmark ended before the fault, names the run, and quotes the errors its tasks reported. A run whose tasks are still being submitted after five minutes gets `ERROR` too, with an `error` saying it had not started. Impact deltas are percentage changes between the pre- and post-chaos summaries. Durations are in seconds: `chaosDuration` runs from the moment Kates creates the fault to the chaos outcome's `verdict`, so on Litmus it includes the experiment's start-up. `recoveryTime` runs from that `verdict` until every probe passes, or until `maxRecoveryWaitSec` runs out.

#### GET /api/resilience/scenarios

List the pre-built resilience scenarios, each a fault with the probes that watch it. Each entry carries the scenario's `id`, `name`, `description` and `disruptionType`, the number of its probes as `probeCount`, and its `chaosDurationSec` and `maxRecoveryWaitSec`.

```json
[
  {
    "id": "broker-crash",
    "name": "Broker Crash",
    "description": "Kill a random broker pod and verify cluster recovers with full ISR",
    "disruptionType": "POD_DELETE",
    "probeCount": 2,
    "chaosDurationSec": 30,
    "maxRecoveryWaitSec": 120
  }
]
```

#### POST /api/resilience/scenarios/{id}

Run a resilience scenario: a test run from the body's `testRequest`, with the scenario's fault and probes. The call answers like [POST /api/resilience](#post-apiresilience), with whitespace while the run goes on and then one JSON object: the scenario's id under `scenario`, and the report under `report`. A resilience scenario isn't the `scenario` of a test request, which holds a test's phases.

**Request Body:**

```json
{
  "testRequest": {
    "type": "LOAD",
    "spec": { "numRecords": 180000, "throughput": 500, "recordSize": 1024, "acks": "all" }
  }
}
```

The scenario brings the rest of the request: its fault and probes, a `steadyStateSec` of 30, and its own `maxRecoveryWaitSec`. The fault goes into the `kafka` namespace and hits one broker pod at random, picked by `strimzi.io/component-type=kafka,strimzi.io/broker-role=true`, so it never hits a dedicated KRaft controller. Three more fields of the body change the fault: `targetLabel` replaces that selector, `targetPod` names the pod to hit, and `chaosDurationSec` sets how long it lasts. The Kates API ignores any other field.

**Response** (cut down, with illustrative numbers):

```json
{
  "scenario": "broker-crash",
  "report": { "status": "COMPLETED", "recoveryTime": 41.027310000 }
}
```

The body is checked before the stream starts and before any fault is injected, and refused with a `400` in these cases:

- It has no `testRequest`, or one that isn't a test request.
- Its `testRequest` is one `POST /api/resilience` would refuse, as above, and the answer has the same `fieldErrors`.
- Its `targetLabel` isn't a label selector, and `fieldErrors` has the key `targetLabel`.
- Its `chaosDurationSec` is outside the [Fault Parameter Limits](07-chaos-practice.md#fault-parameter-limits), and `fieldErrors` has the key `chaosDurationSec`.

An unknown `id` gets `404 Not Found`. The `node-maintenance` scenario is listed, but every call to run it gets a `400`. A scenario can't name the node its `NODE_DRAIN` drains, and Kates sets no `TARGET_NODE`, so the Litmus `node-drain` experiment would drain the node of a random pod in any namespace. To drain a node, send a `NODE_DRAIN` to `POST /api/resilience` with the node in `chaosSpec.envOverrides.TARGET_NODE`, as [LitmusChaos Integration](07-chaos-practice.md#litmuschaos-integration) describes.

---

### Trend Analysis

A trend follows one metric across a test type's `DONE` runs over a number of days, compares each run with a baseline averaged over the most recent of them, and flags regressions; `kates trend` charts the same data. A `FAILED` run is left out, because its numbers stop wherever it failed, and so is a run still in flight.

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

A schedule stores a test request with a cron expression, and while the schedule is enabled the Kates API submits that request as a new run each time the expression fires; `kates schedule` calls every one of these endpoints except `PUT`.

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

A schedule stores `testRequest` with only the fields it sets. Each firing is a `POST /api/tests` of it, merged with the test type's defaults of that day, and the run's `requestedSpec` holds the same fields.

A `testRequest` that `POST /api/tests` would refuse, for a field the run could not honour, a run longer than `kates.engine.max-duration-ms` or a scenario spec value outside its limits, is refused here too, and the schedule isn't saved. The answer is the same `400`, with each field keyed in `fieldErrors`, and named in `message`, by its path in the schedule, such as `testRequest.spec.consumerGroup` or `testRequest.scenario.phases[0].targetThroughput`.

A `testRequest` without a `type`, or with a `spec` value outside the limits `POST /api/tests` sets, such as a `numProducers` above 100, gets the `400` that `POST /api/tests` gives it, with `fieldErrors` keyed by field name. A `backend` the Kates API doesn't have is refused too, keyed `testRequest.backend`, or `testRequest.scenario.backend` for a scenario that names its own. `POST /api/tests` refuses that one with a `400` that has no `fieldErrors`.

A firing the Kates API refuses starts no run, and the reason is in the server log only. That happens to a schedule saved before the Kates API checked `testRequest`, and to one whose request it refuses later, after an upgrade or a change to its settings such as a lower `kates.engine.max-duration-ms`. A firing also holds the request's `spec`, and a scenario's, to the limits `POST /api/tests` sets, such as a `numProducers` of at most 100, and refuses a stored value outside them. Disable such a schedule, or `PUT` a `testRequest` the Kates API accepts.

A schedule created before the Kates API kept the request stored every spec field, those it did not set at their Java defaults. When the Kates API upgrades, its database migration removes the defaults stored for `targetThroughput`, the fetch settings and the three `enable` options, which runs then ignored, so such a schedule runs as it did. A value the schedule did set for one of them now reaches the run, or is refused at each firing if the type cannot apply it. Its other fields keep the stored values, the Java defaults rather than the type's, all within the limits a firing holds `spec` to. `PUT` the schedule's `testRequest` again to have the type's defaults fill them in.

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

Update a schedule. Accepts the same body as `POST /api/schedules` (fields you omit keep their current values, except `enabled`, which is always applied). Returns the updated schedule. A `testRequest` in the body that `POST /api/schedules` would refuse gets the same `400`, whatever the reason, and the schedule stays as it was. A body without one leaves the stored request unchecked, so you can rename or disable a schedule whose firings the Kates API refuses.

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
| 400 | Bad Request | Malformed or invalid request | Invalid `type`, missing required fields, malformed JSON, a resilience `chaosSpec` parameter outside the fault parameter limits |
| 401 | Unauthorized | Missing API key | Security enabled and no `Authorization`/`X-API-Key` header sent |
| 403 | Forbidden | Invalid API key | Key does not match `kates.api.key` |
| 404 | Not Found | Resource does not exist | Unknown test ID, deleted report, non-existent schedule |
| 409 | Conflict | Conflicts with current state | Cancelling a test that is not running; starting a disruption while one is already running |
| 422 | Unprocessable Entity | Rejected by the safety guard | No broker pods in the Kafka namespace, a `targetLabel` that doesn't parse, a fault parameter outside its limit, `maxAffectedBrokers` exceeded, every broker hit |
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

Walk the core loop against a running Kates API — no CLI involved. With `make ports` running:

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

- Most CLI commands call this API, so `curl` and `jq` can script them; those that install and reach the stack run `kubectl` and `helm` instead
- Authentication is on by default: pass the key as `Authorization: Bearer` or `X-API-Key`, expect `401` without one and `403` with a wrong one; only `/api/health`, `/openapi`, and everything under `/q/` stay public
- Test execution and disruption execution are both asynchronous — the `POST` returns `202 Accepted` with an id and you poll `GET /api/tests/{id}` or `GET /api/disruptions/{id}` until the status is terminal
- One report feeds many consumers: JSON for dashboards, CSV for spreadsheets, JUnit XML for CI jobs, and heatmap data for latency visualization
- This chapter covers the core endpoint families only; the complete, always-current spec lives at `/q/openapi`

When shell scripts hit their limits — typed clients, service-to-service calls — the same operations are available over protocol buffers in [gRPC API Reference](16-grpc-api.md).
