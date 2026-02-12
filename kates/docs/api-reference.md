# REST API Reference

All endpoints return JSON. Base URL: `http://localhost:8080`.

## Test Management

### Create a Test

```
POST /api/tests
Content-Type: application/json
```

**Request Body:**

```json
{
  "type": "LOAD",
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

The `spec` object is optional — all fields have sensible defaults. Only `type` is required.

**Response: `202 Accepted`**

```json
{
  "id": "a1b2c3d4",
  "testType": "LOAD",
  "status": "RUNNING",
  "spec": { ... },
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

### List All Tests

```
GET /api/tests
GET /api/tests?type=LOAD
```

Returns an array of `TestRun` objects. The optional `type` query parameter filters by test type.

### Get Test Status

```
GET /api/tests/{id}
```

Returns a single `TestRun` with refreshed status from Trogdor. The response includes accumulated metrics for each task.

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

### Stop and Delete a Test

```
DELETE /api/tests/{id}
```

Stops all running Trogdor tasks for this test run and removes it from the repository.

**Response: `204 No Content`**

### List Available Test Types

```
GET /api/tests/types
```

**Response:**

```json
["LOAD", "STRESS", "SPIKE", "ENDURANCE", "VOLUME", "CAPACITY", "ROUND_TRIP"]
```

## Cluster Information

### Get Cluster Info

```
GET /api/cluster/info
```

**Response:**

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

### List Topics

```
GET /api/cluster/topics
```

Returns a JSON array of topic names.

## Health Check

```
GET /api/health
```

**Response:**

```json
{
  "status": "UP",
  "kafka": {
    "status": "UP",
    "message": "Kafka cluster is reachable"
  },
  "trogdor": {
    "status": "UNKNOWN",
    "message": "Trogdor health check requires coordinator deployment"
  }
}
```

The overall `status` is `UP` when Kafka is reachable, or `DEGRADED` when it is not.
