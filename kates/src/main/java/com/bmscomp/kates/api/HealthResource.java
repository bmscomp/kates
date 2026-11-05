package com.bmscomp.kates.api;

import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import jakarta.annotation.security.PermitAll;
import jakarta.inject.Inject;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.core.MediaType;

import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.eclipse.microprofile.openapi.annotations.Operation;
import org.eclipse.microprofile.openapi.annotations.tags.Tag;

import com.bmscomp.kates.config.TestTypeDefaults;
import com.bmscomp.kates.engine.TestOrchestrator;
import com.bmscomp.kates.service.ClusterHealthService;

// Kubernetes probes and anyone checking the API call this without a key.
@PermitAll
@Path("/api/health")
@Produces(MediaType.APPLICATION_JSON)
@Tag(name = "Health")
public class HealthResource {

    private final ClusterHealthService clusterHealthService;
    private final TestOrchestrator orchestrator;
    private final TestTypeDefaults typeDefaults;
    private final String defaultBackend;
    private final String bootstrapServers;

    @Inject
    public HealthResource(
            ClusterHealthService clusterHealthService,
            TestOrchestrator orchestrator,
            TestTypeDefaults typeDefaults,
            @ConfigProperty(name = "kates.engine.default-backend", defaultValue = "native") String defaultBackend,
            @ConfigProperty(name = "kates.kafka.bootstrap-servers") String bootstrapServers) {
        this.clusterHealthService = clusterHealthService;
        this.orchestrator = orchestrator;
        this.typeDefaults = typeDefaults;
        this.defaultBackend = defaultBackend;
        this.bootstrapServers = bootstrapServers;
    }

    @GET
    @Operation(
            summary = "Application health",
            description = "Returns engine status, Kafka connectivity, test type configurations, and the"
                    + " capabilities a client can check before relying on them")
    public Map<String, Object> health() {
        boolean kafkaReachable = clusterHealthService.isReachable();

        Map<String, Object> response = new LinkedHashMap<>();
        response.put("status", kafkaReachable ? "UP" : "DEGRADED");
        response.put("capabilities", List.of("clientKey"));

        response.put(
                "engine",
                Map.of("activeBackend", defaultBackend, "availableBackends", orchestrator.availableBackends()));

        response.put(
                "kafka",
                Map.of(
                        "status", kafkaReachable ? "UP" : "DOWN",
                        "bootstrapServers", bootstrapServers,
                        "message", kafkaReachable ? "Kafka cluster is reachable" : "Cannot connect to Kafka cluster"));

        Map<String, Object> testConfigs = new LinkedHashMap<>();
        typeDefaults.allConfigs().forEach((name, cfg) -> testConfigs.put(name, cfg.toMap()));
        response.put("tests", testConfigs);

        return response;
    }
}
