package com.bmscomp.kates.it;

import static org.junit.jupiter.api.Assertions.assertTrue;

import jakarta.inject.Inject;

import io.quarkus.test.common.QuarkusTestResource;
import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.junit.TestProfile;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.service.KafkaAdminService;

/**
 * Exercises the shared AdminClient against a real broker (KRaft, PLAINTEXT).
 * Unit tests point at localhost:9092 with no broker — the connection path,
 * circuit breaker wiring, and PLAINTEXT config were never actually executed.
 */
@QuarkusTest
@TestProfile(IntegrationTestProfile.class)
@QuarkusTestResource(value = PostgresTestResource.class, restrictToAnnotatedClass = true)
@QuarkusTestResource(value = KafkaTestResource.class, restrictToAnnotatedClass = true)
class KafkaAdminIT {

    @Inject
    KafkaAdminService adminService;

    @Test
    void pingReachesRealBroker() {
        assertTrue(adminService.ping(), "AdminClient should reach the containerized broker");
    }
}
