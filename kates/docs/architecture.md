# Architecture

This document describes the internal architecture of the KATES backend, covering package structure, class responsibilities, and the test execution lifecycle.

## Package Structure

```
com.klster.kates
├── api/                  REST endpoints (JAX-RS resources)
│   ├── TestResource      POST/GET/DELETE /api/tests
│   ├── ClusterResource   GET /api/cluster/*
│   └── HealthResource    GET /api/health
│
├── domain/               Core data model
│   ├── TestType           Enum: LOAD, STRESS, SPIKE, ...
│   ├── TestSpec           Configurable test parameters
│   ├── TestResult         Task-level metrics and status
│   ├── TestRun            Lifecycle entity (spec → results)
│   └── CreateTestRequest  API request DTO
│
├── trogdor/              Trogdor Coordinator integration
│   ├── TrogdorClient      REST client interface (@RegisterRestClient)
│   ├── SpecFactory        TestType → TrogdorSpec builder
│   └── spec/
│       ├── TrogdorSpec        Base class (class, startMs, durationMs)
│       ├── ProduceBenchSpec   Producer benchmark
│       ├── ConsumeBenchSpec   Consumer benchmark
│       └── RoundTripWorkloadSpec  End-to-end latency
│
└── service/              Business logic
    ├── TestExecutionService   Orchestration engine
    ├── TestRunRepository      In-memory storage
    └── KafkaAdminService      Topic management (AdminClient)
```

## Test Execution Lifecycle

The `TestExecutionService` orchestrates the complete lifecycle of a test run:

```
                 POST /api/tests
                       │
                       ▼
              ┌─────────────────┐
              │ Create TestRun  │  Status: PENDING
              │ Save to repo    │
              └────────┬────────┘
                       │
                       ▼
              ┌─────────────────┐
              │ Create topics   │  KafkaAdminService.createTopic()
              │ via AdminClient │  (+ -large, -count for VOLUME)
              └────────┬────────┘
                       │
                       ▼
              ┌─────────────────┐
              │ Build Trogdor   │  SpecFactory.buildSpecs()
              │ specifications  │  Returns List<TrogdorSpec>
              └────────┬────────┘
                       │
                       ▼
              ┌─────────────────┐
              │ Submit tasks to │  TrogdorClient.createTask()
              │ Coordinator     │  One per TrogdorSpec
              └────────┬────────┘  Status: RUNNING
                       │
            ┌──────────┴─────────┐
            │                    │
      ┌─────▼─────┐      ┌──────▼──────┐
      │   Task     │      │   Task      │
      │ submitted  │      │   failed    │
      │ (RUNNING)  │      │  (FAILED)   │
      └─────┬──────┘      └─────────────┘
            │
            ▼
  ┌──────────────────┐
  │ GET /api/tests/  │  Triggers refreshStatus()
  │     {id}         │  TrogdorClient.getTask()
  └────────┬─────────┘
           │
           ▼
  ┌──────────────────┐
  │ Aggregate status │  All DONE → run DONE
  │ across tasks     │  Any FAILED → run FAILED
  └──────────────────┘
```

## Key Design Decisions

### Trogdor Specs as POJOs

Rather than depending on the Trogdor library JAR, KATES models Trogdor's JSON specification format using plain Java objects with Jackson annotations. This avoids:

- **Dependency conflicts** with Kafka 4.x artifacts where Trogdor may not be separately published
- **Tight coupling** to a specific Trogdor version
- **Size bloat** from pulling in unnecessary transitive dependencies

The `@JsonProperty("class")` annotation on `TrogdorSpec.specClass` ensures the JSON output matches Trogdor's expected format:

```json
{
  "class": "org.apache.kafka.trogdor.workload.ProduceBenchSpec",
  "bootstrapServers": "...",
  "targetMessagesPerSec": 10000
}
```

### In-Memory Repository

`TestRunRepository` uses a `ConcurrentHashMap` for simplicity. This is sufficient because:

- Test runs are ephemeral — they exist only during execution
- The backing data source is Trogdor's Coordinator (stateful)
- No persistence is needed across restarts in the current design

A database-backed repository can be added later if required.

### REST Client for Trogdor

`TrogdorClient` is a Quarkus MicroProfile REST Client interface. It uses `JsonNode` for request/response bodies to stay flexible with Trogdor's JSON structure without rigid type mapping:

```java
@POST
@Path("/task/create")
JsonNode createTask(CreateTaskRequest request);

@GET
@Path("/task/{taskId}")
JsonNode getTask(@PathParam("taskId") String taskId);
```

### Topic Auto-Creation

`TestExecutionService.createTestTopic()` creates topics before running tests. If the topic already exists, the `TopicExistsException` is caught and ignored. For VOLUME tests, additional `-large` and `-count` suffixed topics are created with specific retention and size configs.

## Technology Stack

| Component | Version | Purpose |
|-----------|---------|---------|
| Quarkus | 3.31.3 | Application framework |
| Java | 21 | Language runtime |
| Kafka Clients | 4.1.1 | AdminClient for topic management |
| Jackson | (managed) | JSON serialization/deserialization |
| MicroProfile REST Client | (managed) | Trogdor REST API integration |
| SmallRye Health | (managed) | Health check probes |
| SmallRye OpenAPI | (managed) | Swagger UI and OpenAPI spec |
| JUnit 5 | (managed) | Testing framework |
| Mockito | (managed) | Mock injection for integration tests |
| REST Assured | (managed) | HTTP endpoint testing |
