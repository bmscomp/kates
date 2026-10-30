package com.bmscomp.kates.it;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;

import io.quarkus.narayana.jta.QuarkusTransaction;
import io.quarkus.test.common.QuarkusTestResource;
import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.junit.TestProfile;
import org.junit.jupiter.api.MethodOrderer;
import org.junit.jupiter.api.Order;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.TestMethodOrder;

import com.bmscomp.kates.audit.AuditChain;
import com.bmscomp.kates.service.AuditService;

/**
 * The audit chain on real PostgreSQL: V28's head row as Flyway inserts it,
 * and its row lock under concurrent writers, which AuditVerifyTest only
 * runs on H2, whose locking differs.
 */
@QuarkusTest
@TestProfile(NoSchedulersTestProfile.class)
@QuarkusTestResource(value = PostgresTestResource.class, restrictToAnnotatedClass = true)
@TestMethodOrder(MethodOrderer.OrderAnnotation.class)
class AuditChainIT {

    @Inject
    AuditService auditService;

    @Inject
    AuditChain chain;

    @Inject
    EntityManager em;

    @Test
    @Order(1)
    void concurrentWritersKeepOneChain() throws Exception {
        int writers = 16;
        ExecutorService pool = Executors.newFixedThreadPool(writers);
        CountDownLatch start = new CountDownLatch(1);
        try {
            List<Future<?>> writes = new ArrayList<>();
            for (int i = 0; i < 200; i++) {
                String target = "run-" + i;
                writes.add(pool.submit(() -> {
                    start.await();
                    auditService.record("RUN", "test", target, "HTTP 200", null);
                    return null;
                }));
            }
            start.countDown();
            for (Future<?> write : writes) {
                write.get();
            }
        } finally {
            pool.shutdown();
        }

        AuditChain.Verification v = chain.verify();
        assertTrue(v.intact(), v.problem());
        assertEquals(count(), v.chained() + v.unchained());
        assertTrue(v.chained() >= 200, v.chained() + " chained");
    }

    @Test
    @Order(2)
    void aRolledBackCallerTakesItsRowAndTheHeadWithIt() {
        AuditChain.Verification before = chain.verify();
        assertThrows(
                IllegalStateException.class,
                () -> QuarkusTransaction.requiringNew().run(() -> {
                    auditService.record("DELETE", "test", "rolled-back", "HTTP 500", null);
                    throw new IllegalStateException("the caller fails after its audit row");
                }));

        AuditChain.Verification after = chain.verify();
        assertTrue(after.intact(), after.problem());
        assertEquals(before.headHash(), after.headHash());
    }

    @Test
    @Order(3)
    void aRowEditedInTheDatabaseIsNamed() {
        Number id = (Number) em.createNativeQuery("SELECT MIN(id) FROM audit_events WHERE target = 'run-7'")
                .getSingleResult();
        QuarkusTransaction.requiringNew()
                .run(() -> em.createNativeQuery("UPDATE audit_events SET details = 'HTTP 403' WHERE id = :id")
                        .setParameter("id", id.longValue())
                        .executeUpdate());

        AuditChain.Verification v = chain.verify();
        assertFalse(v.intact());
        assertEquals(id.longValue(), v.brokenAt());
    }

    private long count() {
        return ((Number) em.createNativeQuery("SELECT COUNT(*) FROM audit_events")
                        .getSingleResult())
                .longValue();
    }
}
