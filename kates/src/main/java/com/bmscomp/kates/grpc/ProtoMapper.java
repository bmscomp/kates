package com.bmscomp.kates.grpc;

import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestSpec;
import com.bmscomp.kates.domain.TestType;

import com.bmscomp.kates.grpc.proto.*;

/**
 * Converts between Kates domain objects and protobuf messages.
 * Keeps proto concerns out of domain and service layers.
 */
public final class ProtoMapper {

    private ProtoMapper() {}

    public static com.bmscomp.kates.grpc.proto.TestRun toProto(TestRun run) {
        var builder = com.bmscomp.kates.grpc.proto.TestRun.newBuilder()
                .setId(safe(run.getId()))
                .setTestType(toProtoType(run.getTestType()))
                .setStatus(toProtoStatus(run.getStatus()))
                .setCreatedAt(safe(run.getCreatedAt()))
                .setBackend(safe(run.getBackend()))
                .setScenarioName(safe(run.getScenarioName()));

        if (run.getSpec() != null) {
            builder.setSpec(toProto(run.getSpec()));
        }
        if (run.getResults() != null) {
            run.getResults().forEach(r -> builder.addResults(toProto(r)));
        }
        if (run.getLabels() != null) {
            builder.putAllLabels(run.getLabels());
        }
        return builder.build();
    }

    public static com.bmscomp.kates.grpc.proto.TestSpec toProto(TestSpec spec) {
        return com.bmscomp.kates.grpc.proto.TestSpec.newBuilder()
                .setNumRecords(spec.getNumRecords())
                .setRecordSize(spec.getRecordSize())
                .setThroughput(spec.getThroughput())
                .setAcks(safe(spec.getAcks()))
                .setBatchSize(spec.getBatchSize())
                .setLingerMs(spec.getLingerMs())
                .setCompressionType(safe(spec.getCompressionType()))
                .setNumProducers(spec.getNumProducers())
                .setNumConsumers(spec.getNumConsumers())
                .setDurationMs(spec.getDurationMs())
                .setReplicationFactor(spec.getReplicationFactor())
                .setPartitions(spec.getPartitions())
                .setMinInsyncReplicas(spec.getMinInsyncReplicas())
                .build();
    }

    public static com.bmscomp.kates.grpc.proto.TestResult toProto(TestResult r) {
        return com.bmscomp.kates.grpc.proto.TestResult.newBuilder()
                .setTaskId(safe(r.getTaskId()))
                .setTestType(toProtoType(r.getTestType()))
                .setStatus(toProtoResultStatus(r.getStatus()))
                .setRecordsSent(r.getRecordsSent())
                .setThroughputRecordsPerSec(r.getThroughputRecordsPerSec())
                .setThroughputMbPerSec(r.getThroughputMBPerSec())
                .setAvgLatencyMs(r.getAvgLatencyMs())
                .setP50LatencyMs(r.getP50LatencyMs())
                .setP95LatencyMs(r.getP95LatencyMs())
                .setP99LatencyMs(r.getP99LatencyMs())
                .setMaxLatencyMs(r.getMaxLatencyMs())
                .setStartTime(safe(r.getStartTime()))
                .setEndTime(safe(r.getEndTime()))
                .setError(safe(r.getError()))
                .setPhaseName(safe(r.getPhaseName()))
                .build();
    }

    public static com.bmscomp.kates.grpc.proto.TestType toProtoType(TestType type) {
        if (type == null) return com.bmscomp.kates.grpc.proto.TestType.TEST_TYPE_UNSPECIFIED;
        return switch (type) {
            case LOAD -> com.bmscomp.kates.grpc.proto.TestType.LOAD;
            case STRESS -> com.bmscomp.kates.grpc.proto.TestType.STRESS;
            case SPIKE -> com.bmscomp.kates.grpc.proto.TestType.SPIKE;
            case ENDURANCE -> com.bmscomp.kates.grpc.proto.TestType.ENDURANCE;
            case VOLUME -> com.bmscomp.kates.grpc.proto.TestType.VOLUME;
            case CAPACITY -> com.bmscomp.kates.grpc.proto.TestType.CAPACITY;
            case ROUND_TRIP -> com.bmscomp.kates.grpc.proto.TestType.ROUND_TRIP;
            case INTEGRITY -> com.bmscomp.kates.grpc.proto.TestType.INTEGRITY;
            case TUNE_REPLICATION -> com.bmscomp.kates.grpc.proto.TestType.TUNE_REPLICATION;
            case TUNE_ACKS -> com.bmscomp.kates.grpc.proto.TestType.TUNE_ACKS;
            case TUNE_BATCHING -> com.bmscomp.kates.grpc.proto.TestType.TUNE_BATCHING;
            case TUNE_COMPRESSION -> com.bmscomp.kates.grpc.proto.TestType.TUNE_COMPRESSION;
            case TUNE_PARTITIONS -> com.bmscomp.kates.grpc.proto.TestType.TUNE_PARTITIONS;
        };
    }

    public static TestType toDomainType(com.bmscomp.kates.grpc.proto.TestType type) {
        return switch (type) {
            case LOAD -> TestType.LOAD;
            case STRESS -> TestType.STRESS;
            case SPIKE -> TestType.SPIKE;
            case ENDURANCE -> TestType.ENDURANCE;
            case VOLUME -> TestType.VOLUME;
            case CAPACITY -> TestType.CAPACITY;
            case ROUND_TRIP -> TestType.ROUND_TRIP;
            case INTEGRITY -> TestType.INTEGRITY;
            case TUNE_REPLICATION -> TestType.TUNE_REPLICATION;
            case TUNE_ACKS -> TestType.TUNE_ACKS;
            case TUNE_BATCHING -> TestType.TUNE_BATCHING;
            case TUNE_COMPRESSION -> TestType.TUNE_COMPRESSION;
            case TUNE_PARTITIONS -> TestType.TUNE_PARTITIONS;
            default -> null;
        };
    }

    public static com.bmscomp.kates.grpc.proto.TestStatus toProtoStatus(TestResult.TaskStatus status) {
        if (status == null) return com.bmscomp.kates.grpc.proto.TestStatus.TEST_STATUS_UNSPECIFIED;
        return switch (status) {
            case PENDING -> com.bmscomp.kates.grpc.proto.TestStatus.PENDING;
            case RUNNING -> com.bmscomp.kates.grpc.proto.TestStatus.RUNNING;
            case DONE -> com.bmscomp.kates.grpc.proto.TestStatus.COMPLETED;
            case FAILED -> com.bmscomp.kates.grpc.proto.TestStatus.FAILED;
            case STOPPING -> com.bmscomp.kates.grpc.proto.TestStatus.CANCELLED;
        };
    }

    private static com.bmscomp.kates.grpc.proto.TestStatus toProtoResultStatus(TestResult.TaskStatus status) {
        return toProtoStatus(status);
    }

    private static String safe(String s) {
        return s != null ? s : "";
    }
}
