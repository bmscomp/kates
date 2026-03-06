package com.bmscomp.kates.api;

import jakarta.inject.Inject;

import org.eclipse.microprofile.health.HealthCheck;
import org.eclipse.microprofile.health.HealthCheckResponse;
import org.eclipse.microprofile.health.Readiness;

import com.bmscomp.kates.service.ClusterHealthService;

/**
 * Readiness probe: checks Kafka and database connectivity.
 * Used by Kubernetes to gate traffic to the pod.
 */
@Readiness
public class KatesReadinessCheck implements HealthCheck {

    @Inject
    ClusterHealthService clusterHealthService;

    @Inject
    jakarta.persistence.EntityManager em;

    @Override
    public HealthCheckResponse call() {
        boolean kafkaOk = clusterHealthService.isReachable();
        boolean dbOk = checkDatabase();

        var builder = HealthCheckResponse.named("kates-readiness");
        builder.withData("kafka", kafkaOk ? "UP" : "DOWN");
        builder.withData("database", dbOk ? "UP" : "DOWN");

        if (kafkaOk && dbOk) {
            return builder.up().build();
        }
        return builder.down().build();
    }

    private boolean checkDatabase() {
        try {
            em.createNativeQuery("SELECT 1").getSingleResult();
            return true;
        } catch (Exception e) {
            return false;
        }
    }
}
