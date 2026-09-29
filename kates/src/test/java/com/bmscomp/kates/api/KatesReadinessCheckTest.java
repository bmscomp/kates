package com.bmscomp.kates.api;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import jakarta.persistence.EntityManager;
import jakarta.persistence.Query;

import org.eclipse.microprofile.health.HealthCheckResponse;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.service.KafkaReachabilityCache;

/**
 * Readiness deliberately does NOT gate on Kafka by default — a chaos tool whose
 * target cluster is down is still perfectly able to serve its API. These tests
 * exist so that decision cannot be reverted by accident.
 */
class KatesReadinessCheckTest {

    private KatesReadinessCheck check;
    private KafkaReachabilityCache kafka;
    private EntityManager em;

    @BeforeEach
    void setUp() {
        check = new KatesReadinessCheck();
        kafka = mock(KafkaReachabilityCache.class);
        em = mock(EntityManager.class);
        check.kafkaReachability = kafka;
        check.em = em;
        check.requireKafka = false;
        check.kafkaFailureThreshold = 3;
        databaseUp();
    }

    private void databaseUp() {
        Query query = mock(Query.class);
        when(query.getSingleResult()).thenReturn(1);
        when(em.createNativeQuery("SELECT 1")).thenReturn(query);
    }

    private void databaseDown() {
        when(em.createNativeQuery("SELECT 1")).thenThrow(new IllegalStateException("no connection"));
    }

    private void kafka(boolean reachable, int consecutiveFailures) {
        when(kafka.isReachable()).thenReturn(reachable);
        when(kafka.consecutiveFailures()).thenReturn(consecutiveFailures);
    }

    @Test
    @DisplayName("ready while Kafka is down, by default")
    void kafkaDownDoesNotGateReadiness() {
        kafka(false, 25);

        HealthCheckResponse response = check.call();

        assertEquals(HealthCheckResponse.Status.UP, response.getStatus());
        assertEquals("DOWN", response.getData().orElseThrow().get("kafka"));
        assertEquals(false, response.getData().orElseThrow().get("kafkaGatesReadiness"));
    }

    @Test
    @DisplayName("the database is a hard gate")
    void databaseDownFailsReadiness() {
        kafka(true, 0);
        databaseDown();

        HealthCheckResponse response = check.call();

        assertEquals(HealthCheckResponse.Status.DOWN, response.getStatus());
        assertEquals("DOWN", response.getData().orElseThrow().get("database"));
    }

    @Test
    @DisplayName("with require-kafka, a blip below the threshold stays ready")
    void requireKafkaToleratesBlips() {
        check.requireKafka = true;
        kafka(false, 2);

        assertEquals(HealthCheckResponse.Status.UP, check.call().getStatus());
    }

    @Test
    @DisplayName("with require-kafka, a sustained outage fails readiness")
    void requireKafkaFailsAtThreshold() {
        check.requireKafka = true;
        kafka(false, 3);

        HealthCheckResponse response = check.call();

        assertEquals(HealthCheckResponse.Status.DOWN, response.getStatus());
        assertEquals(3L, ((Number) response.getData().orElseThrow().get("kafkaConsecutiveFailures")).longValue());
    }
}
