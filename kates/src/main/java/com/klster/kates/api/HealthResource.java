package com.klster.kates.api;

import com.klster.kates.config.TestTypeDefaults;
import com.klster.kates.engine.TestOrchestrator;
import com.klster.kates.service.KafkaAdminService;
import jakarta.inject.Inject;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.core.MediaType;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.eclipse.microprofile.openapi.annotations.Operation;
import org.eclipse.microprofile.openapi.annotations.tags.Tag;

import java.util.LinkedHashMap;
import java.util.Map;

@Path("/api/health")
@Produces(MediaType.APPLICATION_JSON)
@Tag(name = "Health")
public class HealthResource {

    private final KafkaAdminService kafkaAdmin;
    private final TestOrchestrator orchestrator;
    private final TestTypeDefaults typeDefaults;
    private final String defaultBackend;
    private final String bootstrapServers;

    @Inject
    public HealthResource(
            KafkaAdminService kafkaAdmin,
            TestOrchestrator orchestrator,
            TestTypeDefaults typeDefaults,
            @ConfigProperty(name = "kates.engine.default-backend", defaultValue = "native") String defaultBackend,
            @ConfigProperty(name = "kates.kafka.bootstrap-servers") String bootstrapServers) {
        this.kafkaAdmin = kafkaAdmin;
        this.orchestrator = orchestrator;
        this.typeDefaults = typeDefaults;
        this.defaultBackend = defaultBackend;
        this.bootstrapServers = bootstrapServers;
    }

    @GET
    @Operation(summary = "Application health", description = "Returns engine status, Kafka connectivity, and test type configurations")
    public Map<String, Object> health() {
        boolean kafkaReachable = kafkaAdmin.isReachable();

        Map<String, Object> response = new LinkedHashMap<>();
        response.put("status", kafkaReachable ? "UP" : "DEGRADED");

        response.put("engine", Map.of(
                "activeBackend", defaultBackend,
                "availableBackends", orchestrator.availableBackends()
        ));

        response.put("kafka", Map.of(
                "status", kafkaReachable ? "UP" : "DOWN",
                "bootstrapServers", bootstrapServers,
                "message", kafkaReachable
                        ? "Kafka cluster is reachable"
                        : "Cannot connect to Kafka cluster"
        ));

        Map<String, Object> testConfigs = new LinkedHashMap<>();
        typeDefaults.allConfigs().forEach((name, cfg) -> testConfigs.put(name, cfg.toMap()));
        response.put("tests", testConfigs);

        return response;
    }
}
