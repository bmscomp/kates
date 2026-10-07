package com.bmscomp.kates.grpc;

import java.util.Comparator;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.TreeMap;
import java.util.stream.Collectors;
import jakarta.inject.Inject;
import jakarta.validation.Validator;

import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.quarkus.grpc.GrpcService;
import io.smallrye.common.annotation.Blocking;
import io.smallrye.mutiny.Uni;

import com.bmscomp.kates.domain.CreateTestRequest;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestSpec;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.engine.ConcurrencyLimitException;
import com.bmscomp.kates.engine.InvalidTestSpecException;
import com.bmscomp.kates.engine.TestOrchestrator;
import com.bmscomp.kates.grpc.proto.*;
import com.bmscomp.kates.service.TestRunRepository;
import com.bmscomp.kates.util.Result;

/**
 * gRPC implementation of the TestService — delegates to the same
 * orchestrator and repository as the REST API.
 */
@GrpcService
@Blocking
public class GrpcTestService extends MutinyTestServiceGrpc.TestServiceImplBase {

    @Inject
    TestOrchestrator orchestrator;

    @Inject
    TestRunRepository repository;

    @Inject
    Validator validator;

    @Override
    public Uni<com.bmscomp.kates.grpc.proto.TestRun> createTest(
            com.bmscomp.kates.grpc.proto.CreateTestRequest request) {
        return Uni.createFrom().item(() -> {
            TestType type = ProtoMapper.toDomainType(request.getType());
            if (type == null) {
                throw Status.INVALID_ARGUMENT
                        .withDescription("Test type is required")
                        .asRuntimeException();
            }

            CreateTestRequest domainReq = new CreateTestRequest();
            domainReq.setType(type);
            domainReq.setSpec(specWithinLimits(request));

            Result<TestRun, Exception> result = orchestrator.executeTest(domainReq);
            TestRun run = result.orElseThrow(GrpcTestService::notStarted);
            return ProtoMapper.toProto(run);
        });
    }

    /**
     * The status for a run the orchestrator did not start. A request the run
     * could not honour as written is INVALID_ARGUMENT, the caller's to change,
     * as POST /api/tests answers it with a 400. A full engine is
     * RESOURCE_EXHAUSTED: a temporary condition, so the caller can send the
     * same request again later, as POST /api/tests answers it with a 429.
     * Any other failure is a fault in the Kates API, INTERNAL. The first two
     * came back INTERNAL as well, so a client couldn't tell them from a fault.
     */
    private static StatusRuntimeException notStarted(Exception e) {
        if (e instanceof InvalidTestSpecException refused) {
            return invalidArgument(refused.getFieldErrors());
        }
        if (e instanceof ConcurrencyLimitException) {
            return Status.RESOURCE_EXHAUSTED.withDescription(e.getMessage()).asRuntimeException();
        }
        return Status.INTERNAL.withDescription(e.getMessage()).withCause(e).asRuntimeException();
    }

    /**
     * The spec the request asks for, once each field it sets is within the
     * limits POST /api/tests holds a spec to by bean validation; otherwise
     * INVALID_ARGUMENT naming each field outside them. Checked before the
     * orchestrator is asked, as on POST /api/tests. The RPC used to check no
     * limit, so a value outside them reached the run.
     */
    private TestSpec specWithinLimits(com.bmscomp.kates.grpc.proto.CreateTestRequest request) {
        // The reason for each field outside its limits, by its TestSpec name,
        // as refusal() names a field too.
        Map<String, String> outside = new TreeMap<>();
        // proto3 sends no presence for a scalar: 0, or "", is a field left
        // unset, and any other value is the request's. A negative one used to
        // be dropped for the type's default, where POST /api/tests refuses it.
        TestSpec spec = new TestSpec();
        long numRecords = request.getNumRecords();
        if (numRecords != (int) numRecords) {
            // An int64 here, an int in the spec. A cast wrapped such a value
            // into the int range: 5000000000 ran as 705032704 records, and
            // 3000000000 as a negative count.
            outside.put("numRecords", "a run's record count is an int, and " + numRecords + " does not fit in one");
        } else if (numRecords != 0) {
            spec.setNumRecords((int) numRecords);
        }
        if (request.getRecordSize() != 0) spec.setRecordSize(request.getRecordSize());
        if (request.getPartitions() != 0) spec.setPartitions(request.getPartitions());
        if (request.getReplicationFactor() != 0) spec.setReplicationFactor(request.getReplicationFactor());
        if (!request.getCompressionType().isEmpty()) spec.setCompressionType(request.getCompressionType());

        // Sorted, so that a field with two reasons gives the same one each time.
        validator.validate(spec).stream()
                .sorted(Comparator.comparing(v -> v.getPropertyPath() + ": " + v.getMessage()))
                .forEach(v -> outside.putIfAbsent(v.getPropertyPath().toString(), v.getMessage()));
        if (!outside.isEmpty()) {
            throw invalidArgument(outside);
        }
        return spec;
    }

    /**
     * INVALID_ARGUMENT naming each field, by its name in kates.proto, with its
     * reason: "partitions: must be less than or equal to 10000". The fields
     * come keyed by their TestSpec names.
     */
    private static StatusRuntimeException invalidArgument(Map<String, String> reasons) {
        String description = reasons.entrySet().stream()
                .map(e -> protoName(e.getKey()) + ": " + e.getValue())
                .collect(Collectors.joining("; "));
        return Status.INVALID_ARGUMENT.withDescription(description).asRuntimeException();
    }

    /** The name kates.proto gives a TestSpec field: num_records for numRecords. */
    private static String protoName(String field) {
        return field.replaceAll("([A-Z])", "_$1").toLowerCase(Locale.ROOT);
    }

    @Override
    public Uni<com.bmscomp.kates.grpc.proto.TestRun> getTest(GetTestRequest request) {
        return Uni.createFrom().item(() -> {
            TestRun run = repository
                    .findById(request.getId())
                    .orElseThrow(() -> Status.NOT_FOUND
                            .withDescription("Test not found: " + request.getId())
                            .asRuntimeException());
            return ProtoMapper.toProto(run);
        });
    }

    @Override
    public Uni<ListTestsResponse> listTests(ListTestsRequest request) {
        return Uni.createFrom().item(() -> {
            int page = Math.max(0, request.getPage());
            int size = Math.max(1, Math.min(request.getSize() > 0 ? request.getSize() : 50, 200));

            List<TestRun> runs;
            long total;

            if (!request.getType().isEmpty()) {
                try {
                    TestType type = TestType.valueOf(request.getType().toUpperCase());
                    runs = repository.findByTypePaged(type, page, size);
                    total = repository.countByType(type);
                } catch (IllegalArgumentException e) {
                    throw Status.INVALID_ARGUMENT
                            .withDescription("Invalid test type: " + request.getType())
                            .asRuntimeException();
                }
            } else {
                runs = repository.findAllPaged(page, size);
                total = repository.countAll();
            }

            return ListTestsResponse.newBuilder()
                    .addAllItems(runs.stream().map(ProtoMapper::toProto).collect(Collectors.toList()))
                    .setPage(page)
                    .setSize(size)
                    .setTotal(total)
                    .build();
        });
    }

    @Override
    public Uni<com.bmscomp.kates.grpc.proto.TestRun> cancelTest(CancelTestRequest request) {
        return Uni.createFrom().item(() -> {
            // The same cancel as POST /api/tests/{id}/cancel: the run is
            // stored as FAILED and this answers with it as stored. It used to
            // only stop the tasks, which left the run STOPPING (answered as
            // CANCELLED) until a poll settled it, and marked a run that had
            // already finished as STOPPING too.
            try {
                return orchestrator
                        .cancelTest(request.getId())
                        .map(ProtoMapper::toProto)
                        .orElseThrow(() -> Status.NOT_FOUND
                                .withDescription("Test not found: " + request.getId())
                                .asRuntimeException());
            } catch (com.bmscomp.kates.engine.RunNotCancellableException e) {
                throw Status.FAILED_PRECONDITION.withDescription(e.getMessage()).asRuntimeException();
            }
        });
    }

    @Override
    public Uni<com.google.protobuf.Empty> deleteTest(DeleteTestRequest request) {
        return Uni.createFrom().item(() -> {
            // The same delete as DELETE /api/tests/{id}. Removing only the row
            // left a running run's workers producing and its concurrency slot
            // taken, so new runs answered 429 until a restart.
            if (!orchestrator.deleteTest(request.getId())) {
                throw Status.NOT_FOUND
                        .withDescription("Test not found: " + request.getId())
                        .asRuntimeException();
            }
            return com.google.protobuf.Empty.getDefaultInstance();
        });
    }
}
