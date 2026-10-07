package com.bmscomp.kates.security;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;

import java.net.URI;
import java.util.concurrent.TimeUnit;

import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.grpc.Metadata;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.grpc.stub.MetadataUtils;
import io.quarkus.test.common.http.TestHTTPResource;
import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.junit.TestProfile;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.grpc.proto.GetTestRequest;
import com.bmscomp.kates.grpc.proto.TestServiceGrpc;

/**
 * gRPC calls with security on: the interceptor reads the key from either
 * metadata key, and refuses a call without one or with a key no principal
 * holds. A call with the key reaches the service, which answers NOT_FOUND
 * for a run that does not exist.
 */
@QuarkusTest
@TestProfile(ApiKeyAuthenticationTest.SecurityEnabledProfile.class)
class GrpcApiKeyInterceptorTest {

    private static final String KEY = "test-secret-key-for-filter-test";

    private static ManagedChannel channel;

    @TestHTTPResource("/")
    URI baseUri;

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

    private Status.Code getTest(String header, String value) {
        var stub = TestServiceGrpc.newBlockingStub(channel);
        if (header != null) {
            Metadata metadata = new Metadata();
            metadata.put(Metadata.Key.of(header, Metadata.ASCII_STRING_MARSHALLER), value);
            stub = stub.withInterceptors(MetadataUtils.newAttachHeadersInterceptor(metadata));
        }
        var request = GetTestRequest.newBuilder().setId("deadbeef").build();
        var finalStub = stub;
        return assertThrows(StatusRuntimeException.class, () -> finalStub.getTest(request))
                .getStatus()
                .getCode();
    }

    @Test
    void aCallWithoutTheKeyIsRefused() {
        assertEquals(Status.Code.UNAUTHENTICATED, getTest(null, null));
        assertEquals(Status.Code.UNAUTHENTICATED, getTest("x-api-key", "not-the-key"));
        assertEquals(Status.Code.UNAUTHENTICATED, getTest("authorization", "Bearer not-the-key"));
    }

    @Test
    void aCallWithTheKeyReachesTheService() {
        assertEquals(Status.Code.NOT_FOUND, getTest("x-api-key", KEY));
        assertEquals(Status.Code.NOT_FOUND, getTest("authorization", "Bearer " + KEY));
    }
}
