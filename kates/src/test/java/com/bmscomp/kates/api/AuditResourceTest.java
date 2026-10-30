package com.bmscomp.kates.api;

import static org.junit.jupiter.api.Assertions.*;

import java.time.Instant;
import java.util.List;
import java.util.Map;

import io.quarkus.test.InjectMock;
import io.quarkus.test.junit.QuarkusTest;
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

        Mockito.when(auditService.list(0, 50, null, null, null, null)).thenReturn(events);
        Mockito.when(auditService.count(null, null, null, null)).thenReturn(2L);

        var response = RestAssured.given()
                .when()
                .get("/api/audit")
                .then()
                .statusCode(200)
                .extract()
                .body()
                .jsonPath();

        assertEquals(2, response.getInt("count"));
        assertEquals(2, response.getList("items").size());
    }

    @Test
    void listRespectsPageSize() {
        var events = List.of(Map.<String, Object>of("type", "test", "action", "c"));

        Mockito.when(auditService.list(2, 2, null, null, null, null)).thenReturn(events);
        Mockito.when(auditService.count(null, null, null, null)).thenReturn(3L);

        var response = RestAssured.given()
                .queryParam("page", 1)
                .queryParam("size", 2)
                .when()
                .get("/api/audit")
                .then()
                .statusCode(200)
                .extract()
                .body()
                .jsonPath();

        assertEquals(1, response.getList("items").size());
        assertEquals(3, response.getInt("total"));
    }

    @Test
    void listFiltersByType() {
        Mockito.when(auditService.list(0, 50, "topic", null, null, null)).thenReturn(List.of());

        var response = RestAssured.given()
                .queryParam("type", "topic")
                .when()
                .get("/api/audit")
                .then()
                .statusCode(200)
                .extract()
                .body()
                .jsonPath();

        assertEquals(0, response.getInt("count"));
    }

    @Test
    void passesUntilOnAndRefusesOneThatIsNotAnInstant() {
        Instant until = Instant.parse("2026-10-30T12:00:00Z");
        Mockito.when(auditService.list(0, 50, null, null, until, null)).thenReturn(List.of());
        Mockito.when(auditService.count(null, null, until, null)).thenReturn(7L);

        RestAssured.given()
                .queryParam("until", "2026-10-30T12:00:00Z")
                .get("/api/audit")
                .then()
                .statusCode(200)
                .body("total", org.hamcrest.Matchers.equalTo(7));
        RestAssured.given().queryParam("until", "noon").get("/api/audit").then().statusCode(400);
    }
}
