# Testing Guide

Kates has 88 tests across 9 test classes. All tests run with `mvn test`.

## Test Architecture

Tests are split into three tiers:

### Unit Tests (no Quarkus context)
- **`TestRunTest`** — domain model: id uniqueness, default status, `addResult`, constructor
- **`TestSpecDefaultsTest`** — verifies all 14 default values match design spec
- **`TrogdorSpecSerializationTest`** — Jackson JSON output: `@JsonProperty("class")`, null field omission, topic key format, field completeness

### Integration Tests (`@QuarkusTest`)
- **`SpecFactoryTest`** — validates `SpecFactory.buildSpecs()` for every test type: spec count, throughput values per ramp step, duration splitting, topic naming, min duration enforcement, producer config propagation
- **`TestRunRepositoryTest`** — CRUD operations, `findByType` filter, update semantics, concurrent thread safety (50 threads)

### API + Mocked Service Tests (`@QuarkusTest` + `@InjectMock`)
- **`TestExecutionServiceTest`** — orchestration with mocked `TrogdorClient` and `KafkaAdminService`: topic creation, task submission count, status transitions, `refreshStatus` from Trogdor JSON, `stopTest`, error handling
- **`TestResourceTest`** — REST endpoints: create returns 202, list, get types, validation (missing type → 400), delete unknown → 404, invalid type filter
- **`ClusterResourceTest`** — cluster info and topics with mocked Kafka: success and error paths
- **`HealthResourceTest`** — UP/DEGRADED based on Kafka connectivity, Trogdor UNKNOWN

## Mocking REST Clients

Quarkus REST Client interfaces (`@RegisterRestClient`) require the `@RestClient` qualifier when mocking:

```java
@InjectMock
@RestClient
TrogdorClient trogdorClient;
```

Regular CDI beans use `@InjectMock` alone:

```java
@InjectMock
KafkaAdminService kafkaAdmin;
```

The `quarkus-junit5-mockito` dependency provides `@InjectMock` support.

## Running Specific Tests

```bash
# All tests
mvn test

# Single class
mvn test -Dtest=com.klster.kates.trogdor.SpecFactoryTest

# Pattern match
mvn test -Dtest="*ResourceTest"

# Specific method
mvn test -Dtest="SpecFactoryTest#stressTestHasCorrectThroughputValues"
```

## Test Profile

`src/test/resources/application.properties` overrides production settings:

```properties
kates.kafka.bootstrap-servers=localhost:9092
kates.trogdor.coordinator-url=http://localhost:8889
```

This ensures tests never connect to real infrastructure.
