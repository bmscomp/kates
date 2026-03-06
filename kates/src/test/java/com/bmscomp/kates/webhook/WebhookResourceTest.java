package com.bmscomp.kates.webhook;

import static org.junit.jupiter.api.Assertions.*;

import java.util.List;

import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.InjectMock;
import io.restassured.RestAssured;
import io.restassured.http.ContentType;
import org.junit.jupiter.api.Test;
import org.mockito.Mockito;

@QuarkusTest
class WebhookResourceTest {

    @InjectMock
    WebhookService webhookService;

    @Test
    void listReturnsWebhooks() {
        var reg = new WebhookService.WebhookRegistration("test-hook", "http://example.com/hook", "DONE");
        Mockito.when(webhookService.list()).thenReturn(List.of(reg));

        var response = RestAssured.given()
                .when().get("/api/webhooks")
                .then().statusCode(200)
                .extract().body().jsonPath();

        assertEquals(1, response.getList("").size());
        assertEquals("test-hook", response.getString("[0].name"));
    }

    @Test
    void registerReturns201() {
        RestAssured.given()
                .contentType(ContentType.JSON)
                .body("{\"name\":\"hook1\",\"url\":\"http://localhost/cb\",\"events\":\"DONE\"}")
                .when().post("/api/webhooks")
                .then().statusCode(201);

        Mockito.verify(webhookService).register(Mockito.any());
    }

    @Test
    void registerRejects400WhenNameMissing() {
        RestAssured.given()
                .contentType(ContentType.JSON)
                .body("{\"url\":\"http://localhost/cb\"}")
                .when().post("/api/webhooks")
                .then().statusCode(400);
    }

    @Test
    void registerRejects400WhenUrlMissing() {
        RestAssured.given()
                .contentType(ContentType.JSON)
                .body("{\"name\":\"hook1\"}")
                .when().post("/api/webhooks")
                .then().statusCode(400);
    }

    @Test
    void unregisterReturns204() {
        RestAssured.given()
                .when().delete("/api/webhooks/hook1")
                .then().statusCode(204);

        Mockito.verify(webhookService).unregister("hook1");
    }
}
