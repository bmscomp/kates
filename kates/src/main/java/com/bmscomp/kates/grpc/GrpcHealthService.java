package com.bmscomp.kates.grpc;

import io.quarkus.grpc.GrpcService;
import io.smallrye.common.annotation.Blocking;
import io.smallrye.mutiny.Uni;
import jakarta.inject.Inject;

import org.eclipse.microprofile.config.inject.ConfigProperty;

import com.bmscomp.kates.engine.TestOrchestrator;
import com.bmscomp.kates.service.ClusterHealthService;

import com.bmscomp.kates.grpc.proto.*;

/**
 * gRPC implementation of the HealthService — reports engine and Kafka status.
 */
@GrpcService
@Blocking
public class GrpcHealthService extends MutinyHealthServiceGrpc.HealthServiceImplBase {

    @Inject
    ClusterHealthService clusterHealthService;

    @Inject
    TestOrchestrator orchestrator;

    @ConfigProperty(name = "kates.engine.default-backend", defaultValue = "native")
    String defaultBackend;

    @ConfigProperty(name = "kates.kafka.bootstrap-servers")
    String bootstrapServers;

    @Override
    public Uni<HealthResponse> check(com.google.protobuf.Empty request) {
        return Uni.createFrom().item(() -> {
            boolean reachable = clusterHealthService.isReachable();

            return HealthResponse.newBuilder()
                    .setStatus(reachable ? "UP" : "DEGRADED")
                    .setEngine(EngineInfo.newBuilder()
                            .setActiveBackend(defaultBackend)
                            .addAllAvailableBackends(orchestrator.availableBackends())
                            .build())
                    .setKafka(KafkaHealth.newBuilder()
                            .setStatus(reachable ? "UP" : "DOWN")
                            .setBootstrapServers(bootstrapServers)
                            .setMessage(reachable
                                    ? "Kafka cluster is reachable"
                                    : "Cannot connect to Kafka cluster")
                            .build())
                    .build();
        });
    }
}
