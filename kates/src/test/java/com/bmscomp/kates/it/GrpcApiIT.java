package com.bmscomp.kates.it;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.net.URI;
import java.util.concurrent.TimeUnit;
import jakarta.inject.Inject;

import com.google.protobuf.Empty;
import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.quarkus.test.common.QuarkusTestResource;
import io.quarkus.test.common.http.TestHTTPResource;
import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.junit.TestProfile;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.grpc.proto.ClusterServiceGrpc;
import com.bmscomp.kates.grpc.proto.GetTestRequest;
import com.bmscomp.kates.grpc.proto.GetTopicRequest;
import com.bmscomp.kates.grpc.proto.HealthServiceGrpc;
import com.bmscomp.kates.grpc.proto.ListTestsRequest;
import com.bmscomp.kates.grpc.proto.ListTopicsRequest;
import com.bmscomp.kates.grpc.proto.TestServiceGrpc;
import com.bmscomp.kates.grpc.proto.TestStatus;
import com.bmscomp.kates.service.TestRunRepository;
import com.bmscomp.kates.service.TopicService;

/**
 * The gRPC surface, which had no client of any kind.
 *
 * <p>Three services are exposed on the same port as REST
 * ({@code quarkus.grpc.server.use-separate-server=false}) and nothing verified
 * that they were reachable, that {@code ProtoMapper} produced sane messages, or
 * that the error paths surfaced as gRPC statuses rather than as exceptions on
 * the wire. The proto status enum is not a copy of the domain one — {@code DONE}
 * becomes {@code COMPLETED} — so the mapping is asserted explicitly.
 *
 * <p>The channel is built by hand against the injected test URI rather than via
 * {@code @GrpcClient}, so the test does not depend on a client host/port being
 * configured anywhere.
 */
@QuarkusTest
@TestProfile(NoSchedulersTestProfile.class)
@QuarkusTestResource(value = PostgresTestResource.class, restrictToAnnotatedClass = true)
@QuarkusTestResource(value = KafkaTestResource.class, restrictToAnnotatedClass = true)
class GrpcApiIT {

    private static ManagedChannel channel;

    @TestHTTPResource("/")
    URI baseUri;

    @Inject
    TestRunRepository repository;

    @Inject
    TopicService topicService;

    @BeforeEach
    void openChannel() {
        if (channel == null) {
            channel = ManagedChannelBuilder.forAddress(baseUri.getHost(), baseUri.getPort())
                    .usePlaintext()
                    .build();
        }
    }

    @AfterAll
    static void closeChannel() throws InterruptedException {
        if (channel != null) {
            channel.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
            channel = null;
        }
    }

    @Test
    void healthServiceReportsTheSameStateAsTheRestEndpoint() {
        var response = HealthServiceGrpc.newBlockingStub(channel).check(Empty.getDefaultInstance());

        assertEquals("UP", response.getStatus(), "a reachable broker means UP");
        assertEquals("UP", response.getKafka().getStatus());
        assertEquals("Kafka cluster is reachable", response.getKafka().getMessage());
        assertEquals("native", response.getEngine().getActiveBackend());
        assertFalse(
                response.getKafka().getBootstrapServers().isBlank(), "the health report names the broker it probed");
    }

    @Test
    void getTestMapsTheDomainRunOntoTheProtoMessage() {
        TestRun run = ItSupport.finishedRun(TestType.LOAD, "grpc-it", 5_000, 3.0, 12.0);
        repository.save(run);

        var stub = TestServiceGrpc.newBlockingStub(channel);
        var proto = stub.getTest(GetTestRequest.newBuilder().setId(run.getId()).build());

        assertEquals(run.getId(), proto.getId());
        assertEquals(com.bmscomp.kates.grpc.proto.TestType.LOAD, proto.getTestType());
        // The lossy half of ProtoMapper: the domain has DONE, the wire has
        // COMPLETED. Nothing else in the suite covers this translation.
        assertEquals(TestStatus.COMPLETED, proto.getStatus());
        assertEquals(1, proto.getResultsCount(), "results travel with the run");
        assertEquals(5_000L, proto.getResults(0).getRecordsSent());
        assertEquals(12.0, proto.getResults(0).getP99LatencyMs(), 0.001);
        assertEquals(1, proto.getSpec().getPartitions(), "the spec is carried across too");
        assertFalse(proto.getCreatedAt().isBlank());
    }

    @Test
    void listTestsPagesAndClampsLikeTheRestEndpoint() {
        for (int i = 0; i < 3; i++) {
            repository.save(ItSupport.finishedRun(TestType.STRESS, "grpc-list-" + i, 100, 1.0, 2.0));
        }

        var stub = TestServiceGrpc.newBlockingStub(channel);
        var page = stub.listTests(ListTestsRequest.newBuilder()
                .setType("STRESS")
                .setPage(0)
                .setSize(2)
                .build());

        assertEquals(2, page.getItemsCount(), "the page carries what was asked for");
        assertEquals(2, page.getSize());
        assertTrue(page.getTotal() >= 3, "total counts every matching row, got " + page.getTotal());

        // size=0 means "unset" on the wire, so the server substitutes its
        // default rather than returning an empty page.
        var defaulted =
                stub.listTests(ListTestsRequest.newBuilder().setType("STRESS").build());
        assertEquals(50, defaulted.getSize(), "an unset size falls back to the documented default");

        var clamped = stub.listTests(
                ListTestsRequest.newBuilder().setType("STRESS").setSize(10_000).build());
        assertEquals(200, clamped.getSize(), "an oversized page is capped");
    }

    @Test
    void unknownRunIsNotFoundAndAnUnsetTypeIsInvalidArgument() {
        var stub = TestServiceGrpc.newBlockingStub(channel);

        StatusRuntimeException missing = assertThrows(
                StatusRuntimeException.class,
                () -> stub.getTest(
                        GetTestRequest.newBuilder().setId("no-such-run").build()));
        assertEquals(Status.Code.NOT_FOUND, missing.getStatus().getCode());

        StatusRuntimeException invalid = assertThrows(
                StatusRuntimeException.class,
                () -> stub.createTest(com.bmscomp.kates.grpc.proto.CreateTestRequest.newBuilder()
                        .build()));
        assertEquals(
                Status.Code.INVALID_ARGUMENT,
                invalid.getStatus().getCode(),
                "an unspecified test type must be rejected as a bad request, not a 500");
    }

    @Test
    void deleteRemovesTheRunItNames() {
        TestRun run = ItSupport.finishedRun(TestType.VOLUME, "grpc-delete", 10, 1.0, 2.0);
        repository.save(run);
        var stub = TestServiceGrpc.newBlockingStub(channel);

        stub.deleteTest(com.bmscomp.kates.grpc.proto.DeleteTestRequest.newBuilder()
                .setId(run.getId())
                .build());

        assertTrue(repository.findById(run.getId()).isEmpty(), "the row is gone from the database, not just the cache");
        assertThrows(
                StatusRuntimeException.class,
                () -> stub.getTest(
                        GetTestRequest.newBuilder().setId(run.getId()).build()));
    }

    @Test
    void clusterServiceDescribesTheContainerBroker() {
        String topic = ItSupport.uniqueTopic("grpc-cluster-it");
        topicService.createTopic(topic, 2, 1, null);
        topicService.evictCache();

        var stub = ClusterServiceGrpc.newBlockingStub(channel);

        var info = stub.getClusterInfo(Empty.getDefaultInstance());
        assertFalse(info.getClusterId().isBlank(), "a real cluster has an id");
        assertEquals(1, info.getBrokersCount());
        assertFalse(info.getBrokers(0).getHost().isBlank());

        var topics = stub.listTopics(ListTopicsRequest.newBuilder().setSize(200).build());
        assertTrue(
                topics.getItemsList().stream().anyMatch(t -> topic.equals(t.getName())),
                "the topic just created must appear in the listing");

        var detail =
                stub.getTopicDetail(GetTopicRequest.newBuilder().setName(topic).build());
        assertEquals(topic, detail.getName());
        assertEquals(2, detail.getPartitions());
        assertNotNull(detail.getConfigsMap());
    }

    @Test
    void consumerGroupListingSucceedsAgainstARealCluster() {
        var stub = ClusterServiceGrpc.newBlockingStub(channel);
        var groups = stub.listConsumerGroups(
                com.bmscomp.kates.grpc.proto.ListGroupsRequest.newBuilder().build());

        // The assertion is that the call completes rather than surfacing an
        // AdminClient failure as an UNKNOWN status; how many groups exist
        // depends on what else has run against this broker, so the count itself
        // is not asserted.
        assertEquals(
                groups.getItemsCount(),
                groups.getItemsList().size(),
                "the response is well-formed rather than an error status");
    }
}
