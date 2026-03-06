package com.bmscomp.kates.api;


import java.util.List;
import java.util.Map;
import java.util.Set;

import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.InjectMock;
import io.restassured.RestAssured;
import io.restassured.http.ContentType;
import org.junit.jupiter.api.Test;
import org.mockito.Mockito;

import com.bmscomp.kates.service.ConsumerGroupService;
import com.bmscomp.kates.service.KafkaClientService;
import com.bmscomp.kates.service.TopicService;
import com.bmscomp.kates.service.ClusterHealthService;

@QuarkusTest
class KafkaClientResourceTest {

    @InjectMock
    TopicService topicService;

    @InjectMock
    ConsumerGroupService consumerGroupService;

    @InjectMock
    KafkaClientService kafkaClientService;

    @InjectMock
    ClusterHealthService clusterHealthService;

    @Test
    void brokersReturns200() {
        Mockito.when(clusterHealthService.describeCluster())
                .thenReturn(Map.of("nodes", List.of(), "controllerId", 0));

        RestAssured.given()
                .when().get("/api/kafka/brokers")
                .then().statusCode(200);
    }

    @Test
    void topicsReturns200() {
        Mockito.when(topicService.listTopics()).thenReturn(Set.of("t1", "t2"));
        Mockito.when(topicService.describeTopics(Mockito.anyCollection())).thenReturn(Map.of());

        RestAssured.given()
                .when().get("/api/kafka/topics")
                .then().statusCode(200);
    }

    @Test
    void groupsReturns200() {
        Mockito.when(consumerGroupService.listConsumerGroups()).thenReturn(List.of());

        RestAssured.given()
                .when().get("/api/kafka/groups")
                .then().statusCode(200);
    }

    @Test
    void consumeReturns200() {
        Mockito.when(kafkaClientService.fetchRecords("test-topic", "latest", 20))
                .thenReturn(List.of());

        RestAssured.given()
                .when().get("/api/kafka/consume/test-topic")
                .then().statusCode(200);
    }

    @Test
    void produceReturns201() {
        RestAssured.given()
                .contentType(ContentType.JSON)
                .body("{\"key\":\"k1\",\"value\":\"v1\"}")
                .when().post("/api/kafka/produce/test-topic")
                .then().statusCode(201);

        Mockito.verify(kafkaClientService).produceRecord("test-topic", "k1", "v1");
    }

    @Test
    void createTopicReturns201() {
        RestAssured.given()
                .contentType(ContentType.JSON)
                .body("{\"name\":\"new-topic\",\"partitions\":3,\"replicationFactor\":1}")
                .when().post("/api/kafka/topics")
                .then().statusCode(201);

        Mockito.verify(topicService).createTopic("new-topic", 3, 1, null);
    }

    @Test
    void deleteTopicReturns204() {
        RestAssured.given()
                .when().delete("/api/kafka/topics/old-topic")
                .then().statusCode(204);

        Mockito.verify(topicService).deleteTopic("old-topic");
    }
}
