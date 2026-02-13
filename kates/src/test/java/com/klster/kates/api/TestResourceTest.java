package com.klster.kates.api;

import com.klster.kates.service.KafkaAdminService;
import com.klster.kates.trogdor.TrogdorClient;
import io.quarkus.test.InjectMock;
import io.quarkus.test.junit.QuarkusTest;
import org.eclipse.microprofile.rest.client.inject.RestClient;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import static io.restassured.RestAssured.given;
import static org.hamcrest.Matchers.*;
import static org.mockito.Mockito.*;

@QuarkusTest
class TestResourceTest {

    @InjectMock
    @RestClient
    TrogdorClient trogdorClient;

    @InjectMock
    KafkaAdminService kafkaAdmin;

    @BeforeEach
    void setUp() {
        doNothing().when(kafkaAdmin).createTopic(anyString(), anyInt(), anyInt(), any());
    }

    @Test
    void listTestsReturnsPagedResponse() {
        given()
                .when().get("/api/tests")
                .then()
                .statusCode(200)
                .body("content", notNullValue())
                .body("page", is(0))
                .body("size", is(50))
                .body("totalElements", greaterThanOrEqualTo(0));
    }

    @Test
    void listTestsAcceptsPagination() {
        given()
                .queryParam("page", 0)
                .queryParam("size", 10)
                .when().get("/api/tests")
                .then()
                .statusCode(200)
                .body("page", is(0))
                .body("size", is(10));
    }

    @Test
    void listTestsWithInvalidTypeReturns400() {
        given()
                .queryParam("type", "INVALID")
                .when().get("/api/tests")
                .then()
                .statusCode(400)
                .body("error", is("Bad Request"))
                .body("message", containsString("INVALID"));
    }

    @Test
    void getTestTypesReturnsAllTypes() {
        given()
                .when().get("/api/tests/types")
                .then()
                .statusCode(200)
                .body("$", hasSize(7))
                .body("$", hasItems("LOAD", "STRESS", "SPIKE", "ENDURANCE",
                        "VOLUME", "CAPACITY", "ROUND_TRIP"));
    }

    @Test
    void createTestRequiresType() {
        given()
                .contentType("application/json")
                .body("{}")
                .when().post("/api/tests")
                .then()
                .statusCode(400);
    }

    @Test
    void getUnknownTestReturns404() {
        given()
                .when().get("/api/tests/nonexistent")
                .then()
                .statusCode(404)
                .body("error", is("Not Found"));
    }

    @Test
    void deleteUnknownTestReturns404() {
        given()
                .when().delete("/api/tests/nonexistent")
                .then()
                .statusCode(404)
                .body("error", is("Not Found"));
    }

    @Test
    void createTestReturns202Accepted() {
        com.fasterxml.jackson.databind.ObjectMapper mapper = new com.fasterxml.jackson.databind.ObjectMapper();
        when(trogdorClient.createTask(any())).thenReturn(mapper.createObjectNode());

        given()
                .contentType("application/json")
                .body("{\"type\": \"ROUND_TRIP\", \"spec\": {}}")
                .when().post("/api/tests")
                .then()
                .statusCode(202)
                .body("testType", is("ROUND_TRIP"))
                .body("id", notNullValue())
                .body("status", anyOf(is("RUNNING"), is("FAILED")));
    }
}
