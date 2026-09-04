package com.bmscomp.kates.it;

import static io.restassured.RestAssured.given;
import static org.hamcrest.Matchers.emptyOrNullString;
import static org.hamcrest.Matchers.equalTo;
import static org.hamcrest.Matchers.greaterThanOrEqualTo;
import static org.hamcrest.Matchers.hasItem;
import static org.hamcrest.Matchers.hasSize;
import static org.hamcrest.Matchers.instanceOf;
import static org.hamcrest.Matchers.is;
import static org.hamcrest.Matchers.lessThan;
import static org.hamcrest.Matchers.not;
import static org.hamcrest.Matchers.notNullValue;
import static org.hamcrest.Matchers.nullValue;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.time.Duration;
import java.util.List;
import java.util.Map;
import jakarta.inject.Inject;

import io.quarkus.test.common.QuarkusTestResource;
import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.junit.TestProfile;
import io.restassured.http.ContentType;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.service.TopicService;

/**
 * The Kafka-facing HTTP surface against a real broker.
 *
 * <p>{@code KafkaClientResourceTest} mocks every collaborator, so its
 * assertions stop at "the resource called the service and returned 201" — the
 * AdminClient calls, the topic config round-trip and the produce/consume path
 * are never executed. Before this class the entire real-broker coverage of the
 * suite was {@code KafkaAdminIT}'s single {@code ping()}.
 */
@QuarkusTest
@TestProfile(IntegrationTestProfile.class)
@QuarkusTestResource(value = PostgresTestResource.class, restrictToAnnotatedClass = true)
@QuarkusTestResource(value = KafkaTestResource.class, restrictToAnnotatedClass = true)
class KafkaApiIT {

    @Inject
    TopicService topicService;

    @Test
    void topicLifecycleRoundTripsThroughTheAdminClient() {
        String topic = ItSupport.uniqueTopic("kafka-api-it");

        given().contentType(ContentType.JSON)
                .body(Map.of("name", topic, "partitions", 2, "replicationFactor", 1))
                .when()
                .post("/api/kafka/topics")
                .then()
                .statusCode(201)
                .body("name", equalTo(topic))
                .body("partitions", is(2))
                .body("replicationFactor", is(1))
                .body("partitionInfo", hasSize(2));

        // listTopics() memoises for 30s and createTopic does not evict, so a
        // listing taken earlier in this class would not contain the new topic.
        topicService.evictCache();
        given().when().get("/api/kafka/topics").then().statusCode(200).body("name", hasItem(topic));

        // Leader and ISR are only populated by a live cluster; a mocked
        // describe would leave them null. The leader id is whatever the
        // container's single node happens to be, so assert the invariant
        // (there is one, and it is in sync) rather than the number.
        given().when()
                .get("/api/kafka/topics/" + topic)
                .then()
                .statusCode(200)
                .body("partitionInfo[0].leader", notNullValue())
                .body("partitionInfo[0].isr", hasSize(1))
                .body("partitionInfo[0].underReplicated", is(false));

        // A config change has to survive a re-describe, which is the whole
        // point of PATCH and is untested today.
        given().contentType(ContentType.JSON)
                .body(Map.of("configs", Map.of("retention.ms", "60000")))
                .when()
                .patch("/api/kafka/topics/" + topic)
                .then()
                .statusCode(200)
                .body("configs.'retention.ms'", equalTo("60000"));

        given().when()
                .get("/api/kafka/topics/" + topic)
                .then()
                .statusCode(200)
                .body("configs.'retention.ms'", equalTo("60000"));

        given().when().delete("/api/kafka/topics/" + topic).then().statusCode(204);

        // The topic is really gone — asserted against the listing, which is the
        // reliable signal. The detail endpoint is deliberately NOT asserted to
        // be 404 here: TopicService.describeTopicDetail rewraps its own "Topic
        // not found" throw as "Failed to describe topic: <name>", and
        // KafkaClientResource only maps 404 when the message contains "not
        // found" — so the 404 branch is currently unreachable and a deleted
        // topic answers 500. Pinning that would enshrine the bug; asserting the
        // listing states the intent without doing so.
        topicService.evictCache();
        assertTrue(
                ItSupport.waitUntil(Duration.ofSeconds(15), () -> {
                    topicService.evictCache();
                    return !given().when()
                            .get("/api/kafka/topics")
                            .then()
                            .statusCode(200)
                            .extract()
                            .jsonPath()
                            .getList("name", String.class)
                            .contains(topic);
                }),
                "a deleted topic must disappear from the listing");
    }

    @Test
    void producedRecordIsReadableBackThroughTheConsumeEndpoint() {
        String topic = ItSupport.uniqueTopic("produce-consume-it");
        given().contentType(ContentType.JSON)
                .body(Map.of("name", topic, "partitions", 1, "replicationFactor", 1))
                .when()
                .post("/api/kafka/topics")
                .then()
                .statusCode(201);

        given().contentType(ContentType.JSON)
                .body(Map.of("key", "k1", "value", "hello-from-it"))
                .when()
                .post("/api/kafka/produce/" + topic)
                .then()
                .statusCode(201)
                .body("topic", equalTo(topic))
                .body("partition", is(0))
                .body("offset", is(0))
                .body("timestamp", notNullValue());

        // 'earliest' matters: the default is 'latest', which would return the
        // record only by accident of timing.
        given().when()
                .get("/api/kafka/consume/" + topic + "?offset=earliest&limit=10")
                .then()
                .statusCode(200)
                .body("$", hasSize(1))
                .body("[0].key", equalTo("k1"))
                .body("[0].value", equalTo("hello-from-it"))
                .body("[0].offset", is(0));
    }

    @Test
    void produceRejectsARequestWithoutAValue() {
        given().contentType(ContentType.JSON)
                .body(Map.of("key", "k1"))
                .when()
                .post("/api/kafka/produce/anything")
                .then()
                .statusCode(400);
    }

    @Test
    void clusterEndpointsDescribeTheContainerBroker() {
        given().when()
                .get("/api/kafka/brokers")
                .then()
                .statusCode(200)
                .body("clusterId", not(emptyOrNullString()))
                .body("brokerCount", is(1))
                .body("brokers", hasSize(1));

        given().when()
                .get("/api/cluster/info")
                .then()
                .statusCode(200)
                .body("brokerCount", is(1))
                .body("controller.id", notNullValue());

        // The health report walks partitions and the KRaft quorum — none of
        // which exists without a real controller.
        given().when()
                .get("/api/cluster/check")
                .then()
                .statusCode(200)
                .body("brokers", is(1))
                .body("status", notNullValue())
                .body("kraftQuorum.hasLeader", is(true));

        given().when()
                .get("/api/cluster/topics?size=10")
                .then()
                .statusCode(200)
                .body("page", is(0))
                .body("size", is(10))
                .body("total", greaterThanOrEqualTo(0));
    }

    @Test
    void brokerConfigsAreReadableAndCarryTheirSource() {
        // Ask the cluster which broker exists rather than assuming the
        // container numbers its only node 1.
        int brokerId = given().when()
                .get("/api/cluster/info")
                .then()
                .statusCode(200)
                .extract()
                .path("brokers[0].id");

        List<Map<String, Object>> configs = given().when()
                .get("/api/cluster/brokers/" + brokerId + "/configs")
                .then()
                .statusCode(200)
                .extract()
                .jsonPath()
                .getList("$");

        assertFalse(configs.isEmpty(), "a live broker reports its configuration");
        Map<String, Object> first = configs.get(0);
        assertTrue(
                first.containsKey("name") && first.containsKey("value") && first.containsKey("source"),
                "each config entry carries name/value/source, got " + first.keySet());
    }

    @Test
    void consumerGroupEndpointsAgreeWithTheBroker() {
        // Listing must succeed against a real cluster rather than surface an
        // AdminClient failure as a 500. How many groups exist depends on which
        // other tests in this class have already run, so the shape is what is
        // asserted.
        given().when().get("/api/kafka/groups").then().statusCode(200).body("$", instanceOf(List.class));

        given().when()
                .get("/api/cluster/groups")
                .then()
                .statusCode(200)
                .body("total", greaterThanOrEqualTo(0))
                .body("page", is(0));

        // Deliberately not asserted as 404. kafka-clients answers DescribeGroups
        // for an unknown group with state DEAD rather than an error, so the
        // resource's not-found branch never fires and this returns 200.
        // Asserting the current 200 would pin behaviour the resource clearly
        // does not intend, so only "did not blow up" is checked.
        given().when().get("/api/cluster/groups/no-such-group").then().statusCode(lessThan(500));
    }

    @Test
    void healthReportsUpOnlyWhenTheBrokerIsActuallyReachable() {
        // The existing HealthResourceTest mocks ClusterHealthService, so it
        // proves the JSON shape but never that reachability is real.
        given().when()
                .get("/api/health")
                .then()
                .statusCode(200)
                .body("status", equalTo("UP"))
                .body("kafka.status", equalTo("UP"))
                .body("kafka.message", equalTo("Kafka cluster is reachable"))
                .body("engine.activeBackend", equalTo("native"));
    }

    @Test
    void deadLetterQueueStatsStartEmptyAgainstAFreshBroker() {
        given().when()
                .get("/api/dlq/stats")
                .then()
                .statusCode(200)
                .body("totalMessages", is(0))
                .body("lastMessageAt", nullValue())
                .body("messagesBySource.size()", is(0));
    }

    @Test
    void shareGroupConsumerReportsItsOwnState() {
        given().when()
                .get("/api/share-groups/status")
                .then()
                .statusCode(200)
                .body("running", is(false))
                .body("processedCount", is(0))
                .body("failedCount", is(0))
                .body("recentResults", hasSize(0));

        // Stopping something that never started is a conflict, not a silent
        // success — the two endpoints have to agree about the state machine.
        given().when().post("/api/share-groups/stop").then().statusCode(409).body("status", equalTo("not_running"));
    }
}
