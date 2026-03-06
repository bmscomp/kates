package com.bmscomp.kates.api;

import static org.junit.jupiter.api.Assertions.*;

import java.util.List;
import java.util.Map;

import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.InjectMock;
import io.restassured.RestAssured;
import org.junit.jupiter.api.Test;
import org.mockito.Mockito;

import com.bmscomp.kates.service.AuditService;

@QuarkusTest
class AuditResourceTest {

    @InjectMock
    AuditService auditService;

    @Test
    void listReturnsPagedEvents() {
        var events = List.of(
                Map.<String, Object>of("type", "test", "action", "created"),
                Map.<String, Object>of("type", "topic", "action", "deleted"));

        Mockito.when(auditService.list(500, null, null)).thenReturn(events);

        var response = RestAssured.given()
                .when().get("/api/audit")
                .then().statusCode(200)
                .extract().body().jsonPath();

        assertEquals(2, response.getInt("count"));
        assertEquals(2, response.getList("items").size());
    }

    @Test
    void listRespectsPageSize() {
        var events = List.of(
                Map.<String, Object>of("type", "test", "action", "a"),
                Map.<String, Object>of("type", "test", "action", "b"),
                Map.<String, Object>of("type", "test", "action", "c"));

        Mockito.when(auditService.list(500, null, null)).thenReturn(events);

        var response = RestAssured.given()
                .queryParam("size", 2)
                .when().get("/api/audit")
                .then().statusCode(200)
                .extract().body().jsonPath();

        assertEquals(2, response.getList("items").size());
        assertEquals(3, response.getInt("total"));
    }

    @Test
    void listFiltersByType() {
        Mockito.when(auditService.list(500, "topic", null)).thenReturn(List.of());

        var response = RestAssured.given()
                .queryParam("type", "topic")
                .when().get("/api/audit")
                .then().statusCode(200)
                .extract().body().jsonPath();

        assertEquals(0, response.getInt("count"));
    }
}
