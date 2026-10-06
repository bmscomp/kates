package com.bmscomp.kates.grpc;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.junit.jupiter.api.Assertions.fail;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.when;

import java.util.Arrays;
import java.util.List;
import java.util.Map;
import java.util.function.ObjIntConsumer;
import java.util.stream.Stream;
import jakarta.validation.ConstraintViolation;
import jakarta.validation.Validation;
import jakarta.validation.Validator;

import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.Arguments;
import org.junit.jupiter.params.provider.MethodSource;
import org.junit.jupiter.params.provider.ValueSource;

import com.bmscomp.kates.domain.TestSpec;
import com.bmscomp.kates.engine.InMemoryEngine;
import com.bmscomp.kates.engine.InvalidTestSpecException;
import com.bmscomp.kates.engine.TestOrchestrator;
import com.bmscomp.kates.grpc.proto.CreateTestRequest;
import com.bmscomp.kates.grpc.proto.CreateTestRequest.Builder;
import com.bmscomp.kates.grpc.proto.TestRun;
import com.bmscomp.kates.grpc.proto.TestType;
import com.bmscomp.kates.util.Result;

/**
 * CreateTest holds the fields it sets to the limits POST /api/tests holds a
 * spec to, and answers a request the run could not take as the caller's
 * error, naming each field by its name in kates.proto. It used to check
 * neither. num_records, an int64, was cast to an int, so 5000000000 ran as
 * 705032704 records and 3000000000 as a negative count; a record size,
 * partition count, replication factor or compression type outside TestSpec's
 * constraints reached the run; a negative value was dropped for the type's
 * default; and a refusal from the orchestrator came back INTERNAL.
 */
class GrpcTestServiceCreateTest {

    private static final Validator VALIDATOR =
            Validation.buildDefaultValidatorFactory().getValidator();

    private final InMemoryEngine engine = new InMemoryEngine();
    private final GrpcTestService service = new GrpcTestService();

    @BeforeEach
    void setUp() {
        service.orchestrator = engine.orchestrator;
        service.repository = engine.repository;
        service.validator = VALIDATOR;
    }

    @ParameterizedTest
    @ValueSource(longs = {3_000_000_000L, 5_000_000_000L, -3_000_000_000L, Long.MAX_VALUE})
    void aNumRecordsPastTheIntRangeIsRefusedRatherThanWrapped(long numRecords) {
        var e = refused(load().setNumRecords(numRecords));

        assertEquals(Status.Code.INVALID_ARGUMENT, e.getStatus().getCode());
        assertEquals(
                "num_records: a run's record count is an int, and " + numRecords + " does not fit in one",
                e.getStatus().getDescription());
        assertNothingStarted();
    }

    /**
     * Each number CreateTest sets, past one of its limits: the field, the
     * value, a request setting it, and a TestSpec holding it, which POST
     * /api/tests refuses.
     */
    static Stream<Arguments> outsideTheLimits() {
        return Stream.of(
                outside("num_records", 1_000_000_001, Builder::setNumRecords, TestSpec::setNumRecords),
                outside("num_records", -1, Builder::setNumRecords, TestSpec::setNumRecords),
                outside("record_size", 104_857_601, Builder::setRecordSize, TestSpec::setRecordSize),
                outside("record_size", -1, Builder::setRecordSize, TestSpec::setRecordSize),
                outside("partitions", 10_001, Builder::setPartitions, TestSpec::setPartitions),
                outside("partitions", -1, Builder::setPartitions, TestSpec::setPartitions),
                outside("replication_factor", 11, Builder::setReplicationFactor, TestSpec::setReplicationFactor),
                outside("replication_factor", -1, Builder::setReplicationFactor, TestSpec::setReplicationFactor));
    }

    @ParameterizedTest(name = "{0} {1}")
    @MethodSource("outsideTheLimits")
    void aValueOutsideItsLimitsIsRefusedByName(String field, int value, Builder request, TestSpec same) {
        var e = refused(request);

        assertEquals(Status.Code.INVALID_ARGUMENT, e.getStatus().getCode());
        // The reason POST /api/tests gives the same value, under the field's
        // name in kates.proto.
        assertEquals(field + ": " + reasonFor(same), e.getStatus().getDescription());
        assertNothingStarted();
    }

    @Test
    void aCompressionTypeKafkaDoesNotKnowIsRefusedByName() {
        var e = refused(load().setCompressionType("bogus"));

        assertEquals(Status.Code.INVALID_ARGUMENT, e.getStatus().getCode());
        assertEquals(
                "compression_type: compressionType must be one of: none, gzip, snappy, lz4, zstd",
                e.getStatus().getDescription());
        assertNothingStarted();
    }

    @Test
    void everyFieldOutsideItsLimitsIsNamedInTheSameOrderEachTime() {
        var e = refused(load().setNumRecords(5_000_000_000L)
                .setRecordSize(104_857_601)
                .setPartitions(10_001)
                .setReplicationFactor(11)
                .setCompressionType("bogus"));

        assertEquals(Status.Code.INVALID_ARGUMENT, e.getStatus().getCode());
        List<String> named = Arrays.stream(e.getStatus().getDescription().split("; "))
                .map(reason -> reason.substring(0, reason.indexOf(": ")))
                .toList();
        assertEquals(
                List.of("compression_type", "num_records", "partitions", "record_size", "replication_factor"), named);
        assertNothingStarted();
    }

    @Test
    void valuesAtTheLimitsRunAsSent() {
        TestRun highest = started(load().setNumRecords(1_000_000_000L)
                .setRecordSize(104_857_600)
                .setPartitions(10_000)
                .setReplicationFactor(10)
                .setCompressionType("zstd"));
        assertEquals(1_000_000_000L, highest.getSpec().getNumRecords());
        assertEquals(104_857_600, highest.getSpec().getRecordSize());
        assertEquals(10_000, highest.getSpec().getPartitions());
        assertEquals(10, highest.getSpec().getReplicationFactor());
        assertEquals("zstd", highest.getSpec().getCompressionType());

        TestRun lowest = started(load().setNumRecords(1)
                .setRecordSize(1)
                .setPartitions(1)
                .setReplicationFactor(1)
                .setCompressionType("none"));
        assertEquals(1, lowest.getSpec().getNumRecords());
        assertEquals(1, lowest.getSpec().getRecordSize());
        assertEquals(1, lowest.getSpec().getPartitions());
        assertEquals(1, lowest.getSpec().getReplicationFactor());
        assertEquals("none", lowest.getSpec().getCompressionType());
    }

    /**
     * proto3 sends no presence for a scalar: a 0, or an empty string, is a
     * field the request left unset, not a value below the limits.
     */
    @Test
    void aFieldLeftUnsetIsNotChecked() {
        TestRun run = started(load());

        assertEquals(Map.of(), engine.rows.get(run.getId()).getRequestedSpec(), "the request set no spec field");
    }

    /**
     * A request the orchestrator refuses, here an ENDURANCE run whose default
     * duration is past the longest the Kates API allows, is the caller's to
     * change, as POST /api/tests answers it with a 400.
     */
    @Test
    void aRequestTheRunCouldNotHonourIsInvalidArgumentNamingTheField() {
        String why = "the run is set to last 3600000 ms (the type's default); the Kates API allows a run at most"
                + " 1800000 ms (kates.engine.max-duration-ms)";
        TestOrchestrator orchestrator = mock(TestOrchestrator.class);
        when(orchestrator.executeTest(any()))
                .thenReturn(Result.failure(new InvalidTestSpecException(Map.of("durationMs", why))));
        service.orchestrator = orchestrator;

        var e = refused(CreateTestRequest.newBuilder().setType(TestType.ENDURANCE));

        assertEquals(Status.Code.INVALID_ARGUMENT, e.getStatus().getCode());
        assertEquals("duration_ms: " + why, e.getStatus().getDescription());
    }

    /** A full engine isn't the caller's error, and is answered as before. */
    @Test
    void aFullEngineIsNotAnInvalidArgument() {
        engine.fill();

        var e = refused(load());

        assertEquals(Status.Code.INTERNAL, e.getStatus().getCode());
        assertTrue(
                e.getStatus().getDescription().startsWith("Concurrency limit reached"),
                e.getStatus().getDescription());
    }

    /** A row of outsideTheLimits, with the value set on both. */
    private static Arguments outside(
            String field, int value, ObjIntConsumer<Builder> setOnRequest, ObjIntConsumer<TestSpec> setOnSpec) {
        Builder request = load();
        setOnRequest.accept(request, value);
        TestSpec spec = new TestSpec();
        setOnSpec.accept(spec, value);
        return Arguments.of(field, value, request, spec);
    }

    private static Builder load() {
        return CreateTestRequest.newBuilder().setType(TestType.LOAD);
    }

    /** The one reason bean validation gives for the spec. */
    private static String reasonFor(TestSpec spec) {
        List<String> reasons = VALIDATOR.validate(spec).stream()
                .map(ConstraintViolation::getMessage)
                .toList();
        assertEquals(1, reasons.size(), "one reason, got " + reasons);
        return reasons.get(0);
    }

    /** The status CreateTest answers the request with; fails if it starts a run instead. */
    private StatusRuntimeException refused(Builder request) {
        try {
            TestRun run = service.createTest(request.build()).await().indefinitely();
            return fail("CreateTest started run " + run.getId() + " with spec {"
                    + run.getSpec().toString().strip().replace("\n", ", ") + "}");
        } catch (StatusRuntimeException e) {
            return e;
        }
    }

    private TestRun started(Builder request) {
        return service.createTest(request.build()).await().indefinitely();
    }

    private void assertNothingStarted() {
        assertTrue(engine.rows.isEmpty(), "no run is stored");
        assertTrue(engine.backend.submitted.isEmpty(), "no task is submitted");
    }
}
