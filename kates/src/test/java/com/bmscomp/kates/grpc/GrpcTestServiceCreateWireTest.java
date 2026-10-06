package com.bmscomp.kates.grpc;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;

import java.net.URI;
import java.util.concurrent.TimeUnit;
import jakarta.inject.Inject;
import jakarta.validation.Validator;

import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.quarkus.test.common.http.TestHTTPResource;
import io.quarkus.test.junit.QuarkusTest;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.TestSpec;
import com.bmscomp.kates.grpc.proto.CreateTestRequest;
import com.bmscomp.kates.grpc.proto.TestServiceGrpc;
import com.bmscomp.kates.grpc.proto.TestType;

/**
 * CreateTest's limits as a client meets them: over a channel to the running
 * Kates API, with the Validator CDI injects into the service, which
 * GrpcTestServiceCreateTest sets by hand.
 */
@QuarkusTest
class GrpcTestServiceCreateWireTest {

    private static ManagedChannel channel;

    @TestHTTPResource("/")
    URI baseUri;

    @Inject
    Validator validator;

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
    void eachFieldOutsideItsLimitsIsNamedInAnInvalidArgument() {
        var stub = TestServiceGrpc.newBlockingStub(channel);
        CreateTestRequest request = CreateTestRequest.newBuilder()
                .setType(TestType.LOAD)
                .setNumRecords(5_000_000_000L)
                .setPartitions(20_000)
                .setCompressionType("bogus")
                .build();

        var e = assertThrows(StatusRuntimeException.class, () -> stub.createTest(request));

        assertEquals(Status.Code.INVALID_ARGUMENT, e.getStatus().getCode());
        TestSpec partitions = new TestSpec();
        partitions.setPartitions(20_000);
        assertEquals(
                "compression_type: compressionType must be one of: none, gzip, snappy, lz4, zstd;"
                        + " num_records: a run's record count is an int, and 5000000000 does not fit in one;"
                        + " partitions: "
                        + validator.validate(partitions).iterator().next().getMessage(),
                e.getStatus().getDescription());
    }
}
