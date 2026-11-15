package com.bmscomp.kates.it;

import static io.restassured.RestAssured.given;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.time.Duration;

import io.quarkus.test.InjectMock;
import io.quarkus.test.common.QuarkusTestResource;
import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.junit.TestProfile;
import io.restassured.http.ContentType;
import io.restassured.response.Response;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.service.TopicService;

/**
 * PostgreSQL unreachable while the Kates API runs: a write answers 503 with Retry-After within the
 * JDBC socket timeout, instead of hanging on TCP (s07 of plans/j4-resilient-quorum.md).
 */
@QuarkusTest
@TestProfile(NoSchedulersTestProfile.class)
@QuarkusTestResource(value = PostgresTestResource.class, restrictToAnnotatedClass = true)
class DatabaseOutageIT {

    @InjectMock
    TopicService topicService;

    @Test
    void aCreateWithTheDatabasePausedAnswers503Fast() {
        // A first request, so the pool holds connections that the pause then strands.
        given().when().get("/api/tests").then().statusCode(200);
        String id = PostgresTestResource.container().getContainerId();
        var docker = PostgresTestResource.container().getDockerClient();
        docker.pauseContainerCmd(id).exec();
        try {
            long start = System.nanoTime();
            // Without the JDBC timeouts the request hangs on TCP for minutes: give up at 60 s.
            Response response = given().config(io.restassured.RestAssured.config()
                            .httpClient(io.restassured.config.HttpClientConfig.httpClientConfig()
                                    .setParam("http.socket.timeout", 60_000)))
                    .contentType(ContentType.JSON)
                    .body(ItSupport.createRequest(TestType.LOAD, ItSupport.singleBrokerSpec(null)))
                    .when()
                    .post("/api/tests");
            Duration took = Duration.ofNanos(System.nanoTime() - start);

            assertEquals(503, response.statusCode(), response.asString());
            assertNotNull(response.header("Retry-After"));
            assertTrue(took.compareTo(Duration.ofSeconds(35)) < 0, "answered after " + took);
        } finally {
            docker.unpauseContainerCmd(id).exec();
        }
        assertTrue(
                ItSupport.waitUntil(
                        Duration.ofSeconds(20),
                        () -> given().when().get("/api/tests").statusCode() == 200),
                "the Kates API answers again once its database is back");
    }
}
