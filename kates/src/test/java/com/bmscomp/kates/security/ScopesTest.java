package com.bmscomp.kates.security;

import static io.restassured.RestAssured.given;
import static org.hamcrest.Matchers.contains;
import static org.hamcrest.Matchers.containsString;
import static org.hamcrest.Matchers.equalTo;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.never;
import static org.mockito.Mockito.verify;

import java.net.URI;
import java.util.List;
import java.util.Map;
import java.util.concurrent.TimeUnit;

import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.grpc.Metadata;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.grpc.stub.MetadataUtils;
import io.quarkus.test.InjectMock;
import io.quarkus.test.common.http.TestHTTPResource;
import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.junit.QuarkusTestProfile;
import io.quarkus.test.junit.TestProfile;
import io.restassured.http.ContentType;
import io.restassured.specification.RequestSpecification;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.disruption.DisruptionLauncher;
import com.bmscomp.kates.disruption.DisruptionSafetyGuard;
import com.bmscomp.kates.grpc.proto.CancelTestRequest;
import com.bmscomp.kates.grpc.proto.CreateTestRequest;
import com.bmscomp.kates.grpc.proto.DeleteTestRequest;
import com.bmscomp.kates.grpc.proto.GetTestRequest;
import com.bmscomp.kates.grpc.proto.TestServiceGrpc;
import com.bmscomp.kates.grpc.proto.TestType;

/**
 * Scopes over HTTP and gRPC, with named keys from a keys file beside the
 * legacy key: an agent that may only read changes nothing, a person who may
 * run tests deletes none, and the legacy key keeps every right.
 */
@QuarkusTest
@TestProfile(ScopesTest.NamedKeysProfile.class)
class ScopesTest {

    public static class NamedKeysProfile implements QuarkusTestProfile {
        @Override
        public Map<String, String> getConfigOverrides() {
            return Map.of(
                    "kates.api.security-enabled",
                    "true",
                    "kates.api.key",
                    TestKeys.LEGACY,
                    "kates.api.keys-file",
                    TestKeys.write(TestKeys.file()).toString());
        }
    }

    // The dry run lists broker pods and there is no cluster here; the launcher
    // is replaced so that a run that got past the scope check would show.
    @InjectMock
    DisruptionSafetyGuard safetyGuard;

    @InjectMock
    DisruptionLauncher launcher;

    @TestHTTPResource("/")
    URI baseUri;

    private static ManagedChannel channel;

    private static RequestSpecification as(String key) {
        return given().header("X-API-Key", key).contentType(ContentType.JSON);
    }

    private static int status(RequestSpecification request, String method, String path) {
        return request.request(method, path).then().extract().statusCode();
    }

    @Test
    void anAgentReads() {
        as(TestKeys.AGENT).get("/api/tests").then().statusCode(200);
        as(TestKeys.AGENT).get("/api/disruptions/types").then().statusCode(200);
    }

    @Test
    void anAgentChangesNothingAndReadsNothingSensitive() {
        List<String[]> refused = List.of(
                new String[] {"POST", "/api/tests", "{\"type\":\"LOAD\"}"},
                new String[] {"POST", "/api/tests/bulk", "[]"},
                new String[] {"POST", "/api/tests/deadbeef/cancel", "{}"},
                new String[] {"DELETE", "/api/tests/deadbeef", null},
                new String[] {"DELETE", "/api/tests?createdBefore=2026-01-01T00:00:00Z", null},
                new String[] {"POST", "/api/kafka/topics", "{\"name\":\"t\"}"},
                new String[] {"POST", "/api/kafka/produce/t", "{}"},
                new String[] {"GET", "/api/kafka/consume/t", null},
                new String[] {"POST", "/api/webhooks", "{}"},
                new String[] {"POST", "/api/schedules", "{}"},
                new String[] {"POST", "/api/disruptions/schedules", "{}"},
                new String[] {"POST", "/api/disruptions/playbooks/leader-cascade", "{}"},
                new String[] {"POST", "/api/disruptions/compound", "{}"},
                new String[] {"POST", "/api/disruptions/templates/x", "{}"},
                new String[] {"POST", "/api/resilience", "{}"},
                new String[] {"POST", "/api/security/baseline", "{}"},
                new String[] {"GET", "/api/security/secrets", null},
                new String[] {"GET", "/api/security/acl-map", null});
        for (String[] r : refused) {
            RequestSpecification request = as(TestKeys.AGENT);
            if (r[2] != null) {
                request = request.body(r[2]);
            }
            request.request(r[0], r[1])
                    .then()
                    .statusCode(403)
                    .body("error", equalTo("Forbidden"))
                    .body("message", containsString("GET /api/whoami lists its scopes"));
        }
    }

    @Test
    void anAgentPreviewsADisruptionButDoesNotRunOne() {
        String plan = "{\"name\":\"p\",\"steps\":[{\"name\":\"s\"}]}";
        as(TestKeys.AGENT)
                .body(plan)
                .post("/api/disruptions")
                .then()
                .statusCode(403)
                .body("message", containsString("needs the chaos:run scope"));
        verify(launcher, never()).launch(any());

        int dryRun = status(as(TestKeys.AGENT).body(plan), "POST", "/api/disruptions?dryRun=true");
        assertTrue(dryRun / 100 == 2, "the dry run is a read, got " + dryRun);
        verify(safetyGuard).dryRun(any());
    }

    @Test
    void aRunnerStartsTestsButDeletesNone() {
        int create = status(as(TestKeys.RUNNER).body("{}"), "POST", "/api/tests");
        assertTrue(create != 401 && create != 403, "test:run may create a test, got " + create);
        as(TestKeys.RUNNER).delete("/api/tests/deadbeef").then().statusCode(403);
    }

    @Test
    void theLegacyKeyKeepsEveryRight() {
        as(TestKeys.LEGACY).delete("/api/tests/deadbeef").then().statusCode(404);
        // Past the chaos:run check to the (mocked) launcher.
        int run = status(
                as(TestKeys.LEGACY).body("{\"name\":\"p\",\"steps\":[{\"name\":\"s\"}]}"), "POST", "/api/disruptions");
        assertTrue(run != 401 && run != 403, "the legacy key may run a disruption, got " + run);
        verify(launcher).launch(any());
    }

    @Test
    void aDisabledKeyIsAWrongKey() {
        as(TestKeys.DISABLED).get("/api/tests").then().statusCode(403).body("error", equalTo("Invalid API key"));
    }

    @Test
    void whoAmINamesTheAgent() {
        as(TestKeys.AGENT)
                .get("/api/whoami")
                .then()
                .statusCode(200)
                .body("principal", equalTo("claude-on-lab"))
                .body("principalType", equalTo("agent"))
                .body("scopes", contains("read"))
                .body("allowedClusterIds", contains("lab-cluster"));
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

    private static Status.Code code(Runnable call) {
        return assertThrows(StatusRuntimeException.class, call::run).getStatus().getCode();
    }

    @Test
    void overGrpcEachMethodNeedsItsScope() {
        var agent = stub(TestKeys.AGENT);
        assertEquals(
                Status.Code.PERMISSION_DENIED,
                code(() -> agent.createTest(
                        CreateTestRequest.newBuilder().setType(TestType.LOAD).build())));
        assertEquals(
                Status.Code.PERMISSION_DENIED,
                code(() -> agent.cancelTest(
                        CancelTestRequest.newBuilder().setId("deadbeef").build())));
        assertEquals(
                Status.Code.NOT_FOUND,
                code(() -> agent.getTest(
                        GetTestRequest.newBuilder().setId("deadbeef").build())));

        var runner = stub(TestKeys.RUNNER);
        assertEquals(
                Status.Code.PERMISSION_DENIED,
                code(() -> runner.deleteTest(
                        DeleteTestRequest.newBuilder().setId("deadbeef").build())));
        assertEquals(
                Status.Code.NOT_FOUND,
                code(() -> stub(TestKeys.LEGACY)
                        .deleteTest(
                                DeleteTestRequest.newBuilder().setId("deadbeef").build())));
    }

    @AfterAll
    static void closeChannel() throws InterruptedException {
        if (channel != null) {
            channel.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
            channel = null;
        }
    }
}
