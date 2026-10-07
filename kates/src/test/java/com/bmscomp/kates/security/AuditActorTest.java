package com.bmscomp.kates.security;

import static io.restassured.RestAssured.given;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.junit.jupiter.api.Assertions.fail;

import java.net.URI;
import java.util.List;
import java.util.Map;
import java.util.concurrent.TimeUnit;
import java.util.function.Predicate;

import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.grpc.Metadata;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.grpc.stub.MetadataUtils;
import io.quarkus.test.InjectMock;
import io.quarkus.test.common.http.TestHTTPResource;
import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.junit.TestProfile;
import io.restassured.http.ContentType;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.disruption.DisruptionLauncher;
import com.bmscomp.kates.disruption.DisruptionSafetyGuard;
import com.bmscomp.kates.grpc.proto.DeleteTestRequest;
import com.bmscomp.kates.grpc.proto.TestServiceGrpc;

/**
 * Every call that changes something leaves an audit row naming who made it,
 * over REST and gRPC, refused and failed calls included; a call without a
 * valid key, and a dry run, leave none.
 */
@QuarkusTest
@TestProfile(ScopesTest.NamedKeysProfile.class)
class AuditActorTest {

    @InjectMock
    DisruptionSafetyGuard safetyGuard;

    @InjectMock
    DisruptionLauncher launcher;

    @TestHTTPResource("/")
    URI baseUri;

    private static ManagedChannel channel;

    @SuppressWarnings("unchecked")
    private static List<Map<String, Object>> rows() {
        return given()
                .header("X-API-Key", TestKeys.LEGACY)
                .queryParam("size", 200)
                .get("/api/audit")
                .then()
                .statusCode(200)
                .extract()
                .jsonPath()
                .getList("items", Map.class)
                .stream()
                .map(m -> (Map<String, Object>) m)
                .toList();
    }

    /** The audit row that matches, waiting for it: a row can be written after the response. */
    private static Map<String, Object> awaitRow(String what, Predicate<Map<String, Object>> match) {
        long deadline = System.currentTimeMillis() + 10_000;
        while (System.currentTimeMillis() < deadline) {
            for (Map<String, Object> row : rows()) {
                if (match.test(row)) {
                    return row;
                }
            }
            try {
                Thread.sleep(100);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                break;
            }
        }
        fail("no audit row for " + what + " in " + rows());
        return null;
    }

    private static Predicate<Map<String, Object>> target(String target) {
        return row -> target.equals(row.get("target"));
    }

    @Test
    void aRefusedChangeNamesWhoTried() {
        given().header("X-API-Key", TestKeys.AGENT)
                .contentType(ContentType.JSON)
                .body("{}")
                .post("/api/webhooks")
                .then()
                .statusCode(403);
        var row = awaitRow(
                "the agent's webhook", target("/api/webhooks").and(r -> "claude-on-lab".equals(r.get("actor"))));
        assertEquals("CREATE", row.get("action"));
        assertEquals("webhook", row.get("eventType"));
        assertEquals("agent", row.get("principalType"));
        assertEquals("HTTP 403", row.get("details"));
    }

    @Test
    void aStartedDisruptionsRowNamesItsReport() {
        org.mockito.Mockito.when(launcher.launch(org.mockito.ArgumentMatchers.any()))
                .thenReturn(
                        new DisruptionLauncher.LaunchResult(DisruptionLauncher.Status.ACCEPTED, "feedf00d", List.of()));
        given().header("X-API-Key", TestKeys.LEGACY)
                .contentType(ContentType.JSON)
                .body("{\"name\":\"p\",\"steps\":[{\"name\":\"s\"}]}")
                .post("/api/disruptions")
                .then()
                .statusCode(202);
        var row = awaitRow("the disruption run", target("feedf00d"));
        assertEquals("RUN", row.get("action"));
        assertEquals("disruption", row.get("eventType"));
        assertEquals("legacy", row.get("actor"));
        assertEquals("HTTP 202", row.get("details"));
    }

    @Test
    void aRefusedPruneNamesTheAgentThatTriedIt() {
        given().header("X-API-Key", TestKeys.AGENT)
                .delete("/api/tests?createdBefore=2001-06-01T00:00:00Z")
                .then()
                .statusCode(403);
        var row = awaitRow("the agent's prune", target("/api/tests").and(r -> "claude-on-lab".equals(r.get("actor"))));
        assertEquals("DELETE", row.get("action"));
        assertEquals("test", row.get("eventType"));
        assertEquals("HTTP 403", row.get("details"));
    }

    @Test
    void aFailedChangeIsAuditedWithItsStatus() {
        given().header("X-API-Key", TestKeys.LEGACY)
                .delete("/api/tests/0bad0bad")
                .then()
                .statusCode(404);
        var row = awaitRow("the legacy delete", target("/api/tests/0bad0bad"));
        assertEquals("DELETE", row.get("action"));
        assertEquals("test", row.get("eventType"));
        assertEquals("legacy", row.get("actor"));
        assertEquals("human", row.get("principalType"));
        assertEquals("HTTP 404", row.get("details"));
        // The actor filter finds it.
        List<?> legacyRows = given().header("X-API-Key", TestKeys.LEGACY)
                .queryParam("actor", "legacy")
                .get("/api/audit")
                .then()
                .extract()
                .jsonPath()
                .getList("items.actor");
        assertTrue(!legacyRows.isEmpty() && legacyRows.stream().allMatch("legacy"::equals), legacyRows.toString());
    }

    @Test
    void noValidKeyNoRowAndADryRunNoRow() {
        given().header("X-API-Key", "not-a-key")
                .delete("/api/profiles/nobody-1")
                .then()
                .statusCode(403);
        String plan = "{\"name\":\"p\",\"steps\":[{\"name\":\"s\"}]}";
        given().header("X-API-Key", TestKeys.AGENT)
                .contentType(ContentType.JSON)
                .body(plan)
                .post("/api/disruptions?dryRun=true")
                .then()
                .statusCode(org.hamcrest.Matchers.lessThan(300));
        // A later audited call, written after the two above.
        given().header("X-API-Key", TestKeys.LEGACY)
                .delete("/api/profiles/nobody-2")
                .then();
        awaitRow("the marker", target("/api/profiles/nobody-2"));
        for (Map<String, Object> row : rows()) {
            assertTrue(!"/api/profiles/nobody-1".equals(row.get("target")), "a refused key left a row: " + row);
            assertTrue(
                    !("/api/disruptions".equals(row.get("target")) && "HTTP 200".equals(row.get("details"))),
                    "a dry run left a row: " + row);
        }
    }

    private TestServiceGrpc.TestServiceBlockingStub stub(String key) {
        if (channel == null) {
            channel = ManagedChannelBuilder.forAddress(baseUri.getHost(), baseUri.getPort())
                    .usePlaintext()
                    .build();
        }
        Metadata metadata = new Metadata();
        metadata.put(Metadata.Key.of("x-api-key", Metadata.ASCII_STRING_MARSHALLER), key);
        return TestServiceGrpc.newBlockingStub(channel)
                .withInterceptors(MetadataUtils.newAttachHeadersInterceptor(metadata));
    }

    @Test
    void gRpcChangesAreAuditedRefusedOrNot() {
        var request = DeleteTestRequest.newBuilder().setId("0dead0ff").build();
        assertEquals(
                Status.Code.PERMISSION_DENIED,
                assertThrows(
                                StatusRuntimeException.class,
                                () -> stub(TestKeys.RUNNER).deleteTest(request))
                        .getStatus()
                        .getCode());
        var refused = awaitRow(
                "the runner's gRPC delete",
                r -> "perf-team".equals(r.get("actor")) && "gRPC PERMISSION_DENIED".equals(r.get("details")));
        assertEquals("DELETE", refused.get("action"));
        assertEquals("kates.TestService/DeleteTest", refused.get("target"));

        assertEquals(
                Status.Code.NOT_FOUND,
                assertThrows(
                                StatusRuntimeException.class,
                                () -> stub(TestKeys.LEGACY).deleteTest(request))
                        .getStatus()
                        .getCode());
        var failed = awaitRow("the legacy gRPC delete", target("0dead0ff"));
        assertEquals("legacy", failed.get("actor"));
        assertEquals("gRPC NOT_FOUND", failed.get("details"));
    }

    @AfterAll
    static void closeChannel() throws InterruptedException {
        if (channel != null) {
            channel.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
            channel = null;
        }
    }
}
