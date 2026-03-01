package com.klster.kates.api;

import static io.restassured.RestAssured.given;
import static org.hamcrest.Matchers.*;
import static org.mockito.Mockito.*;

import java.util.Map;
import java.util.Set;

import io.quarkus.test.InjectMock;
import io.quarkus.test.junit.QuarkusTest;
import org.junit.jupiter.api.Test;

import com.klster.kates.service.KafkaAdminService;

@QuarkusTest
class ClusterResourceTest {

    @InjectMock
    KafkaAdminService kafkaAdmin;

    @Test
    void clusterInfoReturnsSuccessfully() {
        when(kafkaAdmin.describeCluster())
                .thenReturn(Map.of(
                        "clusterId",
                        "test-cluster-id",
                        "brokerCount",
                        3,
                        "controller",
                        Map.of("id", 0, "host", "broker-0", "port", 9092)));

        given().when()
                .get("/api/cluster/info")
                .then()
                .statusCode(200)
                .body("clusterId", is("test-cluster-id"))
                .body("brokerCount", is(3));
    }

    @Test
    void clusterInfoReturns500WhenUnreachable() {
        when(kafkaAdmin.describeCluster()).thenThrow(new RuntimeException("Connection refused"));

        given().when()
                .get("/api/cluster/info")
                .then()
                .statusCode(500)
                .body("error", is("Internal Server Error"))
                .body("message", containsString("Failed to connect"));
    }

    @Test
    void topicsReturnsListSuccessfully() {
        when(kafkaAdmin.listTopics()).thenReturn(Set.of("topic-1", "topic-2", "topic-3"));

        given().when()
                .get("/api/cluster/topics")
                .then()
                .statusCode(200)
                .body("items", hasSize(3))
                .body("total", is(3))
                .body("page", is(0));
    }

    @Test
    void topicsReturns500OnError() {
        when(kafkaAdmin.listTopics()).thenThrow(new RuntimeException("Timeout"));

        given().when()
                .get("/api/cluster/topics")
                .then()
                .statusCode(500)
                .body("error", is("Internal Server Error"))
                .body("message", containsString("Failed to list topics"));
    }
}
