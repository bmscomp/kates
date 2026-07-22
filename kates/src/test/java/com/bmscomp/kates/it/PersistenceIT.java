package com.bmscomp.kates.it;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.time.Duration;
import java.util.List;
import java.util.UUID;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;

import io.quarkus.narayana.jta.QuarkusTransaction;
import io.quarkus.test.common.QuarkusTestResource;
import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.junit.TestProfile;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.service.SchedulerLeaseService;
import com.bmscomp.kates.service.TestRunRepository;
import com.bmscomp.kates.webhook.WebhookService;

/**
 * Runs against real PostgreSQL (Testcontainers): Flyway migrations, the
 * jsonb containment query (H2 silently falls back to LIKE), webhook
 * registration persistence, and scheduler-lease claim semantics — none of
 * which the H2 unit-test path can exercise.
 */
@QuarkusTest
@TestProfile(IntegrationTestProfile.class)
@QuarkusTestResource(value = PostgresTestResource.class, restrictToAnnotatedClass = true)
class PersistenceIT {

    @Inject
    EntityManager em;

    @Inject
    TestRunRepository repository;

    @Inject
    WebhookService webhookService;

    @Inject
    SchedulerLeaseService leases;

    @Test
    void flywayMigrationsAllApplied() {
        Number applied =
                (Number) em.createNativeQuery("SELECT COUNT(*) FROM flyway_schema_history WHERE success = true")
                        .getSingleResult();
        // V1..V18 at time of writing — never falls below the current set.
        assertTrue(applied.intValue() >= 18, "expected >= 18 successful migrations, got " + applied);
    }

    @Test
    void jsonbLabelQueryWorksOnPostgres() {
        String runId = UUID.randomUUID().toString();
        QuarkusTransaction.requiringNew()
                .run(() -> em.createNativeQuery("INSERT INTO test_runs (id, test_type, status, created_at, labels_json)"
                                + " VALUES (:id, 'LOAD', 'DONE', NOW(), CAST(:labels AS jsonb))")
                        .setParameter("id", runId)
                        .setParameter("labels", "{\"team\":\"alpha\",\"env\":\"it\"}")
                        .executeUpdate());

        // Direct native containment query — proves the @> / CAST syntax and
        // the V11 jsonb column are valid on the real engine (no fallback).
        Number direct = (Number)
                em.createNativeQuery("SELECT COUNT(*) FROM test_runs WHERE labels_json @> CAST(:pattern AS jsonb)")
                        .setParameter("pattern", "{\"team\":\"alpha\"}")
                        .getSingleResult();
        assertEquals(1, direct.intValue());

        // Repository path (would silently degrade to LIKE on H2).
        List<TestRun> found = repository.findByLabelJsonb("team", "alpha");
        assertEquals(1, found.size());
        assertEquals(runId, found.get(0).getId());
    }

    @Test
    void webhookRegistrationsArePersisted() {
        var reg = new WebhookService.WebhookRegistration("it-hook", "http://example.com/hook", "DONE");
        webhookService.register(reg);
        assertTrue(webhookService.list().stream().anyMatch(r -> r.name().equals("it-hook")));

        // Upsert semantics: same name, new URL.
        webhookService.register(new WebhookService.WebhookRegistration("it-hook", "http://example.com/v2", "DONE"));
        // Drop the first-level cache so the next query reads the committed row
        // from the DB rather than the stale entity loaded by the list() above.
        // (In production, register/list are separate requests with separate
        // persistence contexts, so this staleness cannot occur.)
        em.clear();
        assertEquals(
                "http://example.com/v2",
                webhookService.list().stream()
                        .filter(r -> r.name().equals("it-hook"))
                        .findFirst()
                        .orElseThrow()
                        .url());

        webhookService.unregister("it-hook");
        em.clear();
        assertFalse(webhookService.list().stream().anyMatch(r -> r.name().equals("it-hook")));
    }

    @Test
    void schedulerLeaseClaimSemantics() {
        String lease = "it-lease-" + UUID.randomUUID();

        // First claim and same-holder renewal both succeed.
        assertTrue(leases.tryAcquire(lease, Duration.ofSeconds(30)));
        assertTrue(leases.tryAcquire(lease, Duration.ofSeconds(30)));

        // A live lease held by another replica cannot be stolen...
        QuarkusTransaction.requiringNew()
                .run(() -> em.createNativeQuery("UPDATE scheduler_leases SET holder = 'other-replica',"
                                + " expires_at = NOW() + INTERVAL '60 seconds' WHERE name = :name")
                        .setParameter("name", lease)
                        .executeUpdate());
        assertFalse(leases.tryAcquire(lease, Duration.ofSeconds(30)));

        // ...but an expired one can.
        QuarkusTransaction.requiringNew()
                .run(() -> em.createNativeQuery(
                                "UPDATE scheduler_leases SET expires_at = NOW() - INTERVAL '1 second' WHERE name = :name")
                        .setParameter("name", lease)
                        .executeUpdate());
        assertTrue(leases.tryAcquire(lease, Duration.ofSeconds(30)));
    }
}
