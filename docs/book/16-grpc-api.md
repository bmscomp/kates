# Chapter 16: gRPC API Reference

Kates exposes a gRPC API alongside the REST API for high-throughput programmatic access from CI pipelines, other services, and language-native clients. Both APIs share the same backend service layer — identical behavior, different wire format.

## When to Use gRPC vs REST

| Criterion | gRPC | REST |
|-----------|------|------|
| CI/CD pipelines | ✅ Strongly typed, fast | ⚠️ Requires JSON parsing |
| Browser access | ❌ Requires proxy | ✅ Native |
| Service mesh integration | ✅ HTTP/2 multiplexing | ✅ Standard |
| Code generation | ✅ Automatic from `.proto` | ❌ Manual |
| Debugging | ⚠️ Binary format | ✅ Human-readable |

## Connection

The gRPC server runs on the same Kates pod, on port **9000** (default Quarkus gRPC port):

```bash
# Port-forward to access locally
kubectl port-forward deployment/kates -n kates 9000:9000

# Test with grpcurl
grpcurl -plaintext localhost:9000 list
```

## Service Definitions

The protobuf contract is defined in [`kates.proto`](file:///Users/bmscomp/production/klster/kates/src/main/proto/kates.proto). Three services are available:

### TestService

The primary service for test execution — create, query, cancel, and delete test runs.

| RPC | Request | Response | Description |
|-----|---------|----------|-------------|
| `CreateTest` | `CreateTestRequest` | `TestRun` | Start a new test execution |
| `GetTest` | `GetTestRequest` | `TestRun` | Retrieve a test by ID |
| `ListTests` | `ListTestsRequest` | `ListTestsResponse` | Paginated test listing |
| `CancelTest` | `CancelTestRequest` | `TestRun` | Cancel a running test |
| `DeleteTest` | `DeleteTestRequest` | `Empty` | Delete a test and its results |

**Example — Create a load test:**

```bash
grpcurl -plaintext -d '{
  "type": "LOAD",
  "num_records": 100000,
  "record_size": 1024,
  "labels": {"env": "staging"}
}' localhost:9000 kates.TestService/CreateTest
```

**Example — List tests with pagination:**

```bash
grpcurl -plaintext -d '{
  "type": "LOAD",
  "page": 0,
  "size": 10
}' localhost:9000 kates.TestService/ListTests
```

### ClusterService

Introspects the Kafka cluster — same data as `kates cluster` CLI commands.

| RPC | Request | Response | Description |
|-----|---------|----------|-------------|
| `GetClusterInfo` | `Empty` | `ClusterInfo` | Cluster ID, controller, and broker list |
| `ListTopics` | `ListTopicsRequest` | `ListTopicsResponse` | Paginated topic listing |
| `GetTopicDetail` | `GetTopicRequest` | `TopicDetail` | Topic config, partitions, and RF |
| `ListConsumerGroups` | `ListGroupsRequest` | `ListGroupsResponse` | Consumer groups with state |

**Example — Get cluster info:**

```bash
grpcurl -plaintext localhost:9000 kates.ClusterService/GetClusterInfo
```

**Example — Get topic details:**

```bash
grpcurl -plaintext -d '{"name": "kates-results"}' \
  localhost:9000 kates.ClusterService/GetTopicDetail
```

### HealthService

Lightweight health check for liveness probes and service mesh routing.

| RPC | Request | Response | Description |
|-----|---------|----------|-------------|
| `Check` | `Empty` | `HealthResponse` | Engine status + Kafka connectivity |

**Example:**

```bash
grpcurl -plaintext localhost:9000 kates.HealthService/Check
```

**Response:**

```json
{
  "status": "UP",
  "engine": {
    "activeBackend": "native",
    "availableBackends": ["native"]
  },
  "kafka": {
    "status": "UP",
    "bootstrapServers": "krafter-kafka-bootstrap.kafka:9092",
    "message": "Kafka cluster is reachable"
  }
}
```

## Message Types

### Test Types

```protobuf
enum TestType {
  TEST_TYPE_UNSPECIFIED = 0;
  LOAD = 1;       STRESS = 2;      SPIKE = 3;
  ENDURANCE = 4;  VOLUME = 5;      CAPACITY = 6;
  ROUND_TRIP = 7; INTEGRITY = 8;
  TUNE_REPLICATION = 9;  TUNE_ACKS = 10;
  TUNE_BATCHING = 11;    TUNE_COMPRESSION = 12;
  TUNE_PARTITIONS = 13;
}
```

### TestSpec

Configuration for a test run:

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `num_records` | int64 | — | Total messages to produce |
| `record_size` | int32 | 1024 | Message size in bytes |
| `throughput` | int64 | -1 | Target records/s (-1 = unlimited) |
| `acks` | string | "all" | Producer acknowledgment mode |
| `batch_size` | int32 | 16384 | Producer batch size bytes |
| `linger_ms` | int32 | 5 | Producer linger delay |
| `compression_type` | string | "none" | none, lz4, snappy, zstd, gzip |
| `num_producers` | int32 | 1 | Parallel producer threads |
| `num_consumers` | int32 | 0 | Parallel consumer threads |
| `duration_ms` | int64 | 0 | Duration-based test (0 = record-based) |
| `replication_factor` | int32 | 3 | Topic replication factor |
| `partitions` | int32 | 6 | Topic partition count |
| `min_insync_replicas` | int32 | 2 | Topic ISR constraint |

### TestResult

Per-phase result metrics:

| Field | Type | Description |
|-------|------|-------------|
| `records_sent` | int64 | Total records produced |
| `throughput_records_per_sec` | double | Achieved throughput |
| `throughput_mb_per_sec` | double | Achieved MB/s |
| `avg_latency_ms` | double | Mean latency |
| `p50_latency_ms` | double | Median latency |
| `p95_latency_ms` | double | 95th percentile |
| `p99_latency_ms` | double | 99th percentile |
| `max_latency_ms` | double | Maximum observed latency |
| `phase_name` | string | Phase identifier (for multi-phase tests) |

### Pagination

All list RPCs use the same pagination pattern:

| Request Field | Description |
|---------------|-------------|
| `page` | Zero-based page index |
| `size` | Items per page (default 50, max 200) |

| Response Field | Description |
|----------------|-------------|
| `items` | Array of results |
| `page` | Current page |
| `size` | Page size used |
| `total` | Total number of items |

## Error Handling

gRPC uses standard status codes:

| Status | When |
|--------|------|
| `INVALID_ARGUMENT` | Missing or invalid test type, unknown enum |
| `NOT_FOUND` | Test ID or topic doesn't exist |
| `INTERNAL` | Backend execution failure |

**Error response example:**

```
ERROR:
  Code: NotFound
  Message: Test not found: abc-123
```

## Client Code Generation

Generate typed clients from `kates.proto`:

```bash
# Go
protoc --go_out=. --go-grpc_out=. kates.proto

# Java
protoc --java_out=. --grpc-java_out=. kates.proto

# Python
python -m grpc_tools.protoc -I. --python_out=. --grpc_python_out=. kates.proto
```

The proto file is bundled in the Kates repository at `kates/src/main/proto/kates.proto`.

## REST vs gRPC Equivalence

| REST Endpoint | gRPC RPC |
|--------------|----------|
| `POST /api/tests` | `TestService/CreateTest` |
| `GET /api/tests/{id}` | `TestService/GetTest` |
| `GET /api/tests` | `TestService/ListTests` |
| `POST /api/tests/{id}/cancel` | `TestService/CancelTest` |
| `DELETE /api/tests/{id}` | `TestService/DeleteTest` |
| `GET /api/cluster` | `ClusterService/GetClusterInfo` |
| `GET /api/cluster/topics` | `ClusterService/ListTopics` |
| `GET /api/cluster/topics/{name}` | `ClusterService/GetTopicDetail` |
| `GET /api/cluster/groups` | `ClusterService/ListConsumerGroups` |
| `GET /api/health` | `HealthService/Check` |

For REST endpoint details, see [Chapter 11: REST API Reference](11-api-reference.md).
