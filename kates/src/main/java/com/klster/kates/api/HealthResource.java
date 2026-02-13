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

import java.util.LinkedHashMap;
import java.util.Map;

@Path("/api/health")
@Produces(MediaType.APPLICATION_JSON)
public class HealthResource {

    @Inject
    KafkaAdminService kafkaAdmin;

    @Inject
    TestOrchestrator orchestrator;

    @Inject
    TestTypeDefaults typeDefaults;

    @ConfigProperty(name = "kates.engine.default-backend", defaultValue = "native")
    String defaultBackend;

    @ConfigProperty(name = "kates.kafka.bootstrap-servers")
    String bootstrapServers;

    @GET
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
