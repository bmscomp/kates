package com.bmscomp.kates.grpc;

import java.util.List;
import java.util.stream.Collectors;

import io.grpc.Status;
import io.quarkus.grpc.GrpcService;
import io.smallrye.common.annotation.Blocking;
import io.smallrye.mutiny.Uni;
import jakarta.inject.Inject;

import com.bmscomp.kates.domain.CreateTestRequest;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestSpec;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.engine.TestOrchestrator;
import com.bmscomp.kates.service.TestRunRepository;
import com.bmscomp.kates.util.Result;

import com.bmscomp.kates.grpc.proto.*;

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

    @Override
    public Uni<com.bmscomp.kates.grpc.proto.TestRun> createTest(
            com.bmscomp.kates.grpc.proto.CreateTestRequest request) {
        return Uni.createFrom().item(() -> {
            TestType type = ProtoMapper.toDomainType(request.getType());
            if (type == null) {
                throw Status.INVALID_ARGUMENT.withDescription("Test type is required").asRuntimeException();
            }

            CreateTestRequest domainReq = new CreateTestRequest();
            domainReq.setType(type);

            TestSpec spec = new TestSpec();
            if (request.getNumRecords() > 0) spec.setNumRecords((int) request.getNumRecords());
            if (request.getRecordSize() > 0) spec.setRecordSize(request.getRecordSize());
            if (request.getPartitions() > 0) spec.setPartitions(request.getPartitions());
            if (request.getReplicationFactor() > 0) spec.setReplicationFactor(request.getReplicationFactor());
            if (!request.getCompressionType().isEmpty()) spec.setCompressionType(request.getCompressionType());
            domainReq.setSpec(spec);

            Result<TestRun, Exception> result = orchestrator.executeTest(domainReq);
            TestRun run = result.orElseThrow(e ->
                    Status.INTERNAL.withDescription(e.getMessage()).withCause(e).asRuntimeException());
            return ProtoMapper.toProto(run);
        });
    }

    @Override
    public Uni<com.bmscomp.kates.grpc.proto.TestRun> getTest(GetTestRequest request) {
        return Uni.createFrom().item(() -> {
            TestRun run = repository.findById(request.getId())
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
            TestRun run = repository.findById(request.getId())
                    .orElseThrow(() -> Status.NOT_FOUND
                            .withDescription("Test not found: " + request.getId())
                            .asRuntimeException());

            orchestrator.stopTest(request.getId());
            TestRun updated = repository.findById(request.getId()).orElse(run);
            return ProtoMapper.toProto(updated);
        });
    }

    @Override
    public Uni<com.google.protobuf.Empty> deleteTest(DeleteTestRequest request) {
        return Uni.createFrom().item(() -> {
            repository.findById(request.getId())
                    .orElseThrow(() -> Status.NOT_FOUND
                            .withDescription("Test not found: " + request.getId())
                            .asRuntimeException());
            repository.delete(request.getId());
            return com.google.protobuf.Empty.getDefaultInstance();
        });
    }
}
