package com.klster.kates.api;

import com.klster.kates.service.KafkaAdminService;
import io.quarkus.test.InjectMock;
import io.quarkus.test.junit.QuarkusTest;
import org.junit.jupiter.api.Test;

import static io.restassured.RestAssured.given;
import static org.hamcrest.Matchers.*;
import static org.mockito.Mockito.*;

@QuarkusTest
class HealthResourceTest {

    @InjectMock
    KafkaAdminService kafkaAdmin;

    @Test
    void healthReturnsUpWhenKafkaReachable() {
        when(kafkaAdmin.isReachable()).thenReturn(true);

        given()
                .when().get("/api/health")
                .then()
                .statusCode(200)
                .body("status", is("UP"))
                .body("kafka.status", is("UP"));
    }

    @Test
    void healthReturnsDegradedWhenKafkaUnreachable() {
        when(kafkaAdmin.isReachable()).thenReturn(false);

        given()
                .when().get("/api/health")
                .then()
                .statusCode(200)
                .body("status", is("DEGRADED"))
                .body("kafka.status", is("DOWN"));
    }

    @Test
    void healthAlwaysReturnsTrogdorUnknown() {
        when(kafkaAdmin.isReachable()).thenReturn(true);

        given()
                .when().get("/api/health")
                .then()
                .statusCode(200)
                .body("trogdor.status", is("UNKNOWN"));
    }
}
