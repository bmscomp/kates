package com.bmscomp.kates.api;

import jakarta.inject.Inject;

import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.eclipse.microprofile.health.HealthCheck;
import org.eclipse.microprofile.health.HealthCheckResponse;
import org.eclipse.microprofile.health.Readiness;

import com.bmscomp.kates.service.KafkaReachabilityCache;

/**
 * Readiness probe: gates traffic to the pod.
 *
 * <p>The database is a hard dependency — every request path touches it. Kafka is
 * NOT a readiness gate by default: most endpoints (runs, reports, schedules,
 * trends) serve fine without a broker, and failing readiness on a transient
 * Kafka blip pulled the entire API out of the Service for no benefit. The Kafka
 * signal is reported as data, read from a periodically refreshed cache so the
 * probe never blocks on a broker round-trip.
 *
 * <p>Set {@code kates.health.readiness.require-kafka=true} to gate on Kafka
 * anyway; even then it only fails after
 * {@code kates.health.readiness.kafka-failure-threshold} consecutive failed
 * refreshes, so a blip cannot flap the pod out of the Service.
 */
@Readiness
public class KatesReadinessCheck implements HealthCheck {

    @Inject
    KafkaReachabilityCache kafkaReachability;

    @Inject
    jakarta.persistence.EntityManager em;

    @ConfigProperty(name = "kates.health.readiness.require-kafka", defaultValue = "false")
    boolean requireKafka;

    @ConfigProperty(name = "kates.health.readiness.kafka-failure-threshold", defaultValue = "3")
    int kafkaFailureThreshold;

    @Override
    public HealthCheckResponse call() {
        boolean kafkaOk = kafkaReachability.isReachable();
        int kafkaFailures = kafkaReachability.consecutiveFailures();
        boolean dbOk = checkDatabase();

        var builder = HealthCheckResponse.named("kates-readiness");
        builder.withData("kafka", kafkaOk ? "UP" : "DOWN");
        builder.withData("kafkaConsecutiveFailures", kafkaFailures);
        builder.withData("kafkaGatesReadiness", requireKafka);
        builder.withData("database", dbOk ? "UP" : "DOWN");

        boolean kafkaBlocks = requireKafka && kafkaFailures >= kafkaFailureThreshold;
        if (dbOk && !kafkaBlocks) {
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
