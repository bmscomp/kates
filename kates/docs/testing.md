# Testing Guide

Testing a tool that tests other systems is a recursive challenge. Kates orchestrates Kafka performance benchmarks and chaos experiments — but how do you test the orchestrator itself without a running Kafka cluster, a Trogdor coordinator, and a set of Litmus CRDs? You use mocks. Carefully designed, behavior-accurate mocks that simulate what the real systems do, so you can verify that the orchestration logic is correct without requiring an entire distributed infrastructure in your CI pipeline.

This is not a compromise — it is the right approach. The question "does the Kafka cluster sustain 50,000 messages per second?" is answered by running an actual performance test against an actual cluster. The question "does the TestOrchestrator correctly split 50,000 messages/sec across 5 producers?" is answered by a unit test that verifies the math, the task construction, and the lifecycle management. Conflating these two questions leads to slow, flaky tests that are expensive to run and difficult to debug.

This chapter describes how the Kates test suite is organized, the testing philosophy behind each layer, how mock injection works with Quarkus CDI, and how to write new tests that follow the established patterns.

## Test Architecture

Kates uses a layered testing strategy that mirrors the application's architecture. Tests are organized into three categories based on what they exercise and what they depend on. This layering is deliberate — each layer catches a different class of bug, and together they provide high confidence without requiring a running Kafka cluster.

### Unit Tests

Unit tests exercise individual classes in isolation, with all external dependencies replaced by mocks. These tests use JUnit 5 and Mockito for mock injection.

**What they test:**

- `TestOrchestrator` — verifies that the correct `BenchmarkTask` objects are built for each test type, that per-type defaults are applied correctly, and that the backend selection logic resolves to the expected backend
- `SlaGrader` — verifies the grading algorithm (A/B/C/D/F) against a variety of metric combinations: all-pass, partial failure, critical violation, edge cases
- `DisruptionSafetyGuard` — verifies blast radius validation, RBAC permission checks, and rollback logic
- `SpecFactory` — verifies that `TestSpec` objects are correctly translated to Trogdor-specific `ProduceBenchSpec`, `ConsumeBenchSpec`, and `RoundTripWorkloadSpec` POJOs
- `TestTypeDefaults` — verifies that the three-tier configuration resolution works correctly and that per-type overrides take precedence over global defaults
- `DisruptionPlaybookCatalog` — verifies that playbook YAML files are correctly parsed and converted to `DisruptionPlan` objects

**What they mock:**

- `KafkaAdminService` — topic creation, cluster metadata
- `BenchmarkBackend` — task submission, status polling
- `ChaosProvider` — fault injection, status polling, cleanup
- `TrogdorClient` — REST API calls to the Trogdor Coordinator
- `KafkaIntelligenceService` — leader resolution, ISR/lag tracking

### Integration Tests

Integration tests exercise the interaction between multiple components. They use Quarkus test infrastructure with `@InjectMock` from `quarkus-junit5-mockito` to replace external-facing dependencies while keeping internal CDI wiring intact.

**What they test:**

- The full test orchestration flow: a request arrives at `TestResource`, passes through `TestOrchestrator`, applies defaults, builds tasks, submits to the (mocked) backend, and returns a well-formed response
- The full disruption orchestration flow: a request arrives at `DisruptionResource`, passes through `DisruptionSafetyGuard`, `KafkaIntelligenceService`, `DisruptionOrchestrator`, the (mocked) `ChaosProvider`, `SlaGrader`, and produces a `DisruptionReport`
- Configuration resolution: verifying that ConfigMap values override `application.properties` defaults
- Error handling: verifying that backend failures, topic creation failures, and chaos provider failures result in appropriate HTTP error responses

**What they mock:**

- `TrogdorClient` — the MicroProfile REST Client that calls the Trogdor Coordinator. This is always mocked because Trogdor is not available in the test environment.
- `KafkaAdminService` — mocked to return predictable cluster metadata and avoid requiring a real Kafka cluster for tests that exercise the orchestration logic
- `ChaosProvider` — mocked to return synthetic `ChaosOutcome` objects

### API Tests

API tests (also called endpoint tests) exercise the REST endpoints directly using REST Assured. They send real HTTP requests to the running Quarkus test instance and validate response status codes, headers, and JSON body content.

**What they test:**

- HTTP contract: correct status codes (200, 202, 400, 404, 422, 500), Content-Type headers, and JSON structure
- Request validation: missing required fields, invalid enum values, constraint violations
- Query parameter handling: filtering tests by type, pagination for disruption reports
- Health endpoint response format: Kafka connectivity status, backend availability, per-type configuration

**Test profile:**

API tests run against a Quarkus test instance started with the `test` profile. The test profile configuration is defined in `application.properties` under the `%test` prefix:

```properties
%test.quarkus.datasource.db-kind=h2
%test.quarkus.datasource.jdbc.url=jdbc:h2:mem:kates-test
%test.quarkus.hibernate-orm.database.generation=drop-and-create
%test.kates.kafka.bootstrap-servers=localhost:9092
%test.kates.trogdor.coordinator-url=http://localhost:8889
```

This configuration uses an in-memory H2 database (instead of PostgreSQL), drops and recreates the schema on every test run, and points to non-existent Kafka/Trogdor endpoints (which are mocked at the Java level).

## Running Tests

### Run the Full Suite

```bash
./mvnw test
```

This runs all unit, integration, and API tests. The test output shows:

- Total test count (currently 153 tests)
- Per-class results
- Failures and errors with stack traces

### Run a Specific Test Class

```bash
./mvnw test -Dtest=TestOrchestratorTest
./mvnw test -Dtest=SlaGraderTest
./mvnw test -Dtest=DisruptionSafetyGuardTest
./mvnw test -Dtest=SpecFactoryTest
./mvnw test -Dtest=DisruptionResourceTest
```

### Run a Specific Test Method

```bash
./mvnw test -Dtest=TestOrchestratorTest#testLoadTestCreatesCorrectTasks
./mvnw test -Dtest=SlaGraderTest#testAllMetricsPassYieldsGradeA
```

### Run Tests by Tag

```bash
./mvnw test -Dgroups=unit
./mvnw test -Dgroups=integration
./mvnw test -Dgroups=api
```

### Run with Verbose Output

```bash
./mvnw test -Dsurefire.useFile=false
```

This prints test output directly to the terminal instead of writing to `target/surefire-reports/`.

### Format Check Before Testing

The recommended CI test command runs formatting checks first, then compiles, then tests:

```bash
./mvnw clean spotless:check compile test
```

This ensures that code formatting violations are caught before test execution, providing faster feedback.

## Mocking REST Clients

The Trogdor backend communicates with the Trogdor Coordinator via a MicroProfile REST Client interface (`TrogdorClient`). In tests, this interface is replaced with a mock using Quarkus's `@InjectMock` annotation.

Here is the pattern used across the test suite:

```java
@QuarkusTest
class TrogdorBackendTest {

    @InjectMock
    @RestClient
    TrogdorClient trogdorClient;

    @Inject
    TrogdorBackend backend;

    @Test
    void testSubmitProducerTask() {
        // Arrange: configure the mock to return a known response
        when(trogdorClient.createTask(anyString(), any()))
                .thenReturn(Response.ok().build());

        // Act: submit a task through the backend
        BenchmarkTask task = BenchmarkTask.builder()
                .type(BenchmarkTask.Type.PRODUCE)
                .topic("test-topic")
                .throughput(10000)
                .numRecords(100000)
                .build();
        BenchmarkHandle handle = backend.submit(task);

        // Assert: verify the backend called the mock with correct arguments
        verify(trogdorClient).createTask(eq("test-topic-produce"), argThat(spec -> {
            ProduceBenchSpec produceBenchSpec = (ProduceBenchSpec) spec;
            return produceBenchSpec.getTargetTopic().equals("test-topic")
                    && produceBenchSpec.getTargetThroughput() == 10000;
        }));
    }
}
```

**Why this pattern?** The `@InjectMock` annotation from `quarkus-junit5-mockito` replaces the CDI bean with a Mockito mock for the duration of the test. This is different from standard Mockito `@Mock` injection because it works within the CDI container—the mock is injected into other CDI beans that depend on it (like `TrogdorBackend`), not just into the test class itself.

This means that when `backend.submit(task)` is called, the backend internally calls `trogdorClient.createTask(...)`, which hits the mock instead of making a real HTTP request to a Trogdor Coordinator.

## Mocking Chaos Providers

For disruption tests, the `ChaosProvider` is mocked to return synthetic outcomes:

```java
@QuarkusTest
class DisruptionOrchestratorTest {

    @InjectMock
    ChaosProvider chaosProvider;

    @InjectMock
    KafkaIntelligenceService kafkaIntelligence;

    @Inject
    DisruptionOrchestrator orchestrator;

    @Test
    void testSingleStepDisruption() {
        // Arrange: mock the chaos provider to succeed
        when(chaosProvider.triggerFault(any()))
                .thenReturn(CompletableFuture.completedFuture(
                    new ChaosOutcome(ChaosStatus.COMPLETED, "Fault injected", 5000)));
        when(chaosProvider.isAvailable()).thenReturn(true);

        // Arrange: mock leader resolution
        when(kafkaIntelligence.resolveLeader(eq("test-topic"), eq(0)))
                .thenReturn(OptionalInt.of(1));

        // Act: execute a single-step plan
        DisruptionPlan plan = new DisruptionPlan();
        plan.setName("test-disruption");
        plan.setSteps(List.of(
            new DisruptionPlan.DisruptionStep(
                "kill-leader", faultSpec, 10, 30, true)));

        DisruptionReport report = orchestrator.execute(plan);

        // Assert: verify the report contains correct step results
        assertThat(report.getStepReports()).hasSize(1);
        assertThat(report.getStepReports().get(0).outcome().status())
                .isEqualTo(ChaosStatus.COMPLETED);
    }
}
```

## Mocking the Kafka AdminClient

The `KafkaAdminService` wraps the Kafka AdminClient and is mocked to avoid requiring a running Kafka cluster:

```java
@InjectMock
KafkaAdminService kafkaAdmin;

@Test
void testTopicCreation() {
    when(kafkaAdmin.createTopic(anyString(), anyInt(), anyShort(), anyMap()))
            .thenReturn(CompletableFuture.completedFuture(null));

    // ... test code that triggers topic creation ...

    verify(kafkaAdmin).createTopic(
            eq("load-test"),
            eq(3),     // partitions
            eq((short) 3),  // replication factor
            argThat(config -> config.get("min.insync.replicas").equals("2")));
}
```

## Writing New Tests

When adding a new feature to Kates, follow this checklist for test coverage:

1. **Unit test the business logic** — if you are adding a new method to `SlaGrader`, `DisruptionSafetyGuard`, `SpecFactory`, or any other service, write unit tests that verify the method's behavior in isolation with mocked dependencies.

2. **Integration test the flow** — if your feature involves multiple CDI beans interacting (e.g., a new API endpoint that calls the orchestrator, which calls a backend), write an integration test using `@QuarkusTest` and `@InjectMock`.

3. **API test the contract** — if you are adding or modifying a REST endpoint, write a REST Assured test that verifies the HTTP contract (status codes, JSON structure, error responses).

4. **Test edge cases** — null inputs, empty lists, zero values, maximum values, negative numbers. The SLA grader tests are a good example of thorough edge case coverage.

5. **Test error paths** — backend failures, timeout scenarios, exception propagation. Verify that failures result in appropriate error responses, not 500s with stack traces.

### Test Naming Convention

Follow the `test{Condition}Yields{ExpectedResult}` pattern:

```java
testAllMetricsPassYieldsGradeA()
testOneWarningViolationYieldsGradeB()
testCriticalViolationYieldsGradeF()
testEmptySlaDefinitionYieldsGradeA()
testBlastRadiusExceededRejectsPlan()
testMissingRbacPermissionsRejectsPlan()
```

This naming convention makes it easy to understand what each test verifies by reading the test name alone, without looking at the test body.

## Export Integration Tests

The export subsystem (`CsvExporter`, `JunitXmlExporter`, `HeatmapExporter`) can be tested by verifying the output format:

```java
@Test
void testCsvExporterProducesValidCsv() {
    // Arrange: create a test run with known results
    TestRun run = createTestRunWithResults();

    // Act: export to CSV
    String csv = csvExporter.export(run);

    // Assert: verify CSV structure and values
    String[] lines = csv.split("\n");
    assertThat(lines[0]).isEqualTo("taskId,status,throughput,avgLatency,p99Latency");
    assertThat(lines[1]).contains("task-0,DONE,25000.0");
}
```

The `JunitXmlExporter` produces JUnit-compatible XML that can be consumed by CI systems (GitHub Actions, Jenkins, GitLab CI) to display test results in their native UI.

## Test Dependencies

The following test dependencies are declared in `pom.xml`:

| Dependency | Purpose |
|-----------|---------|
| `quarkus-junit5` | Quarkus test framework, `@QuarkusTest` annotation, test lifecycle |
| `quarkus-junit5-mockito` | `@InjectMock` for CDI-aware mock injection |
| `rest-assured` | HTTP endpoint testing with fluent API |
| `quarkus-jacoco` | Code coverage reporting (optional) |

All test dependencies are at the versions managed by the Quarkus BOM, so you do not need to specify explicit versions.
