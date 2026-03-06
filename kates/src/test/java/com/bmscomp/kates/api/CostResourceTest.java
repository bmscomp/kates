package com.bmscomp.kates.api;

import static io.restassured.RestAssured.given;
import static org.hamcrest.Matchers.*;
import static org.mockito.Mockito.*;

import io.quarkus.test.InjectMock;
import io.quarkus.test.junit.QuarkusTest;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.service.ClusterHealthService;

@QuarkusTest
class CostResourceTest {

    @InjectMock
    ClusterHealthService clusterHealthService;

    @Test
    void estimateReturnsAwsCost() {
        when(clusterHealthService.brokerCount()).thenReturn(3);

        given().contentType("application/json")
                .body("""
                        {"cloud":"aws","records":100000,"recordSize":512,
                         "durationSeconds":300,"brokers":3,"replicas":3}""")
                .when()
                .post("/api/cost/estimate")
                .then()
                .statusCode(200)
                .body("provider", containsString("AWS"))
                .body("totalCost", notNullValue())
                .body("breakdown.storageGB", notNullValue());
    }

    @Test
    void estimateRejectsUnknownProvider() {
        given().contentType("application/json")
                .body("{\"cloud\":\"oracle\"}")
                .when()
                .post("/api/cost/estimate")
                .then()
                .statusCode(400)
                .body("error", is("Bad Request"))
                .body("message", containsString("oracle"));
    }

    @Test
    void estimateUsesClusterBrokerCountWhenAvailable() {
        when(clusterHealthService.brokerCount()).thenReturn(5);

        given().contentType("application/json")
                .body("{\"cloud\":\"gcp\"}")
                .when()
                .post("/api/cost/estimate")
                .then()
                .statusCode(200)
                .body("clusterBrokers", is(5));
    }

    @Test
    void estimateFallsBackWhenClusterUnreachable() {
        when(clusterHealthService.brokerCount()).thenReturn(0);

        given().contentType("application/json")
                .body("{\"cloud\":\"azure\"}")
                .when()
                .post("/api/cost/estimate")
                .then()
                .statusCode(200)
                .body("clusterBrokers", is("unknown (using estimate)"));
    }
}
