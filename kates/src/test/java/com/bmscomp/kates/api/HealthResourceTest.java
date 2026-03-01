package com.bmscomp.kates.api;

import static io.restassured.RestAssured.given;
import static org.hamcrest.Matchers.*;
import static org.mockito.Mockito.*;

import java.util.List;

import io.quarkus.test.InjectMock;
import io.quarkus.test.junit.QuarkusTest;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.engine.TestOrchestrator;
import com.bmscomp.kates.service.KafkaAdminService;

@QuarkusTest
class HealthResourceTest {

    @InjectMock
    KafkaAdminService kafkaAdmin;

    @InjectMock
    TestOrchestrator orchestrator;

    @Test
    void healthReturnsUpWithEngineAndPerTypeConfig() {
        when(kafkaAdmin.isReachable()).thenReturn(true);
        when(orchestrator.availableBackends()).thenReturn(List.of("native", "trogdor"));

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
        when(kafkaAdmin.isReachable()).thenReturn(false);
        when(orchestrator.availableBackends()).thenReturn(List.of("native"));

        given().when()
                .get("/api/health")
                .then()
                .statusCode(200)
                .body("status", is("DEGRADED"))
                .body("kafka.status", is("DOWN"))
                .body("engine.activeBackend", is("native"));
    }
}
