package com.klster.kates.api;

import com.klster.kates.service.KafkaAdminService;
import jakarta.inject.Inject;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.core.MediaType;

import java.util.Map;

@Path("/api/health")
@Produces(MediaType.APPLICATION_JSON)
public class HealthResource {

    @Inject
    KafkaAdminService kafkaAdmin;

    @GET
    public Map<String, Object> health() {
        boolean kafkaReachable = kafkaAdmin.isReachable();

        return Map.of(
                "status", kafkaReachable ? "UP" : "DEGRADED",
                "kafka", Map.of(
                        "status", kafkaReachable ? "UP" : "DOWN",
                        "message", kafkaReachable
                                ? "Kafka cluster is reachable"
                                : "Cannot connect to Kafka cluster"
                ),
                "trogdor", Map.of(
                        "status", "UNKNOWN",
                        "message", "Trogdor health check requires coordinator deployment"
                )
        );
    }
}
