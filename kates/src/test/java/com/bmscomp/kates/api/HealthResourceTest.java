package com.bmscomp.kates.api;

import static io.restassured.RestAssured.given;
import static org.hamcrest.Matchers.*;
import static org.mockito.Mockito.*;

import java.util.List;

import io.quarkus.test.junit.QuarkusMock;
import io.quarkus.test.junit.QuarkusTest;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.engine.TestOrchestrator;
import com.bmscomp.kates.service.ClusterHealthService;

@QuarkusTest
class HealthResourceTest {

    /**
     * Stubs the mocks first and installs them after, rather than stubbing
     * {@code @InjectMock} fields that are already live.
     *
     * TestOrchestrator carries a {@code @Scheduled} reconciler that fires every
     * five seconds, and while a mock replaces the bean the scheduler calls that
     * void method on the mock. Mockito records the last call on a mock for
     * {@code when(...)} across threads, so a tick landing between
     * {@code orchestrator.availableBackends()} and {@code thenReturn} made the
     * stub fail with CannotStubVoidMethodWithReturnValue. The test passed on
     * its own and failed now and then in the full suite. A mock the scheduler
     * cannot reach until it is fully stubbed has no such window.
     */
    private static void installMocks(boolean kafkaReachable, List<String> backends) {
        ClusterHealthService clusterHealthService = mock(ClusterHealthService.class);
        when(clusterHealthService.isReachable()).thenReturn(kafkaReachable);
        TestOrchestrator orchestrator = mock(TestOrchestrator.class);
        when(orchestrator.availableBackends()).thenReturn(backends);
        QuarkusMock.installMockForType(clusterHealthService, ClusterHealthService.class);
        QuarkusMock.installMockForType(orchestrator, TestOrchestrator.class);
    }

    @Test
    void healthReturnsUpWithEngineAndPerTypeConfig() {
        installMocks(true, List.of("native", "trogdor"));

        given().when()
                .get("/api/health")
                .then()
                .statusCode(200)
                .body("status", is("UP"))
                .body("engine.activeBackend", is("native"))
                .body("engine.availableBackends", hasItems("native", "trogdor"))
                .body("kafka.status", is("UP"))
                .body("kafka.bootstrapServers", notNullValue())
                .body("tests.load", notNullValue())
                .body("tests.stress", notNullValue())
                .body("tests.spike", notNullValue())
                .body("tests.endurance", notNullValue())
                .body("tests.volume", notNullValue())
                .body("tests.capacity", notNullValue())
                .body("tests.roundtrip", notNullValue())
                .body("tests.load.partitions", is(3))
                .body("tests.stress.partitions", is(6))
                .body("tests.stress.numProducers", is(3))
                .body("tests.volume.recordSize", is(10240))
                .body("tests.roundtrip.compressionType", is("none"))
                .body("tests.endurance.throughput", is(5000))
                .body("tests.capacity.partitions", is(12));
    }

    @Test
    void healthReturnsDegradedWhenKafkaUnreachable() {
        installMocks(false, List.of("native"));

        given().when()
                .get("/api/health")
                .then()
                .statusCode(200)
                .body("status", is("DEGRADED"))
                .body("kafka.status", is("DOWN"))
                .body("engine.activeBackend", is("native"));
    }
}
