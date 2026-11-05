package com.bmscomp.kates.it;

import static io.restassured.RestAssured.given;
import static org.hamcrest.Matchers.is;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyInt;
import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.Mockito.doThrow;

import java.util.List;
import java.util.UUID;
import java.util.concurrent.CompletableFuture;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;

import io.quarkus.narayana.jta.QuarkusTransaction;
import io.quarkus.test.InjectMock;
import io.quarkus.test.common.QuarkusTestResource;
import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.junit.TestProfile;
import io.restassured.http.ContentType;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.CreateTestRequest;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.engine.ConcurrencyLimitException;
import com.bmscomp.kates.engine.TestOrchestrator;
import com.bmscomp.kates.service.TestRunRepository;
import com.bmscomp.kates.service.TopicService;
import com.bmscomp.kates.util.Result;

/**
 * The unique index V29 builds, on the path only a second pod or a replay after
 * a restart can reach: past the resource's lookup and its per-key lock.
 *
 * <p>H2 cannot hold this: the insert's failure surfaces at commit on Postgres,
 * as a different exception through the transaction interceptor.
 */
@QuarkusTest
@TestProfile(NoSchedulersTestProfile.class)
@QuarkusTestResource(value = PostgresTestResource.class, restrictToAnnotatedClass = true)
class IdempotentCreateIT {

    @InjectMock
    TopicService topicService;

    @Inject
    TestOrchestrator orchestrator;

    @Inject
    TestRunRepository repository;

    @Inject
    EntityManager em;

    /**
     * EXCLUSIVE mode refuses writers and lets readers through, so both creates
     * pass every read and queue on the same insert, which is the race itself.
     */
    @Test
    void theLoserOfTheIndexRaceGivesItsPermitBack() {
        doThrow(new IllegalStateException("no broker here"))
                .when(topicService)
                .createTopic(anyString(), anyInt(), anyInt(), any());
        String key = UUID.randomUUID().toString();
        CreateTestRequest request = ItSupport.createRequest(TestType.LOAD, ItSupport.singleBrokerSpec(null));
        // The bean's first use reads the database, which needs this thread's request context.
        assertEquals(0, orchestrator.activeTestCount());

        List<Result<TestRun, Exception>> results = QuarkusTransaction.requiringNew()
                .call(() -> {
                    em.createNativeQuery("LOCK TABLE test_runs IN EXCLUSIVE MODE")
                            .executeUpdate();
                    var first = CompletableFuture.supplyAsync(() -> orchestrator.executeTest(request, key));
                    var second = CompletableFuture.supplyAsync(() -> orchestrator.executeTest(request, key));
                    assertTrue(ItSupport.waitUntil(() -> waitingWriters() == 2), "both creates reached the insert");
                    return List.of(first, second);
                })
                .stream()
                .map(CompletableFuture::join)
                .toList();

        assertEquals(1, results.stream().filter(Result::isSuccess).count());
        Exception lost = results.stream()
                .filter(Result::isFailure)
                .findFirst()
                .orElseThrow()
                .asFailure()
                .orElseThrow();
        assertTrue(!(lost instanceof ConcurrencyLimitException), lost.toString());

        assertTrue(
                ItSupport.waitUntil(() -> orchestrator.activeTestCount() == 0),
                "permits still held: " + orchestrator.activeTestCount());

        // The key stays with the winner; the loser's failed row holds none.
        String id = results.stream()
                .filter(Result::isSuccess)
                .findFirst()
                .orElseThrow()
                .asSuccess()
                .orElseThrow()
                .getId();
        assertEquals(id, repository.findByClientKey(key).orElseThrow().getId());

        // The loser's replay finds the run, and a new key still starts one.
        post(key).then().statusCode(200);
        post(UUID.randomUUID().toString()).then().statusCode(202);
    }

    /** Postgres refuses NUL in a text parameter, which H2 does not. */
    @Test
    void aClientKeyNoRunCanHoldIsFoundOnNoRun() {
        given().queryParam("clientKey", "a" + (char) 0 + "b")
                .when()
                .get("/api/tests")
                .then()
                .statusCode(200)
                .body("total", is(0));
    }

    private long waitingWriters() {
        return ((Number) em.createNativeQuery("SELECT COUNT(*) FROM pg_locks l WHERE l.relation = 'test_runs'::regclass"
                                + " AND NOT l.granted")
                        .getSingleResult())
                .longValue();
    }

    private static io.restassured.response.Response post(String key) {
        return given().contentType(ContentType.JSON)
                .header("Idempotency-Key", key)
                .body("{\"type\":\"LOAD\",\"backend\":\"native\",\"spec\":{\"numRecords\":25,\"partitions\":1,"
                        + "\"replicationFactor\":1,\"minInsyncReplicas\":1}}")
                .when()
                .post("/api/tests");
    }
}
