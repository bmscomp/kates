package com.bmscomp.kates.it;

import static org.junit.jupiter.api.Assertions.*;

import java.time.Duration;
import java.time.Instant;
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
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.disruption.DisruptionScheduleEntity;
import com.bmscomp.kates.schedule.ScheduleFirings;
import com.bmscomp.kates.schedule.ScheduleFirings.Kind;
import com.bmscomp.kates.schedule.ScheduledTestRun;
import com.bmscomp.kates.schedule.ScheduledTestRunRepository;

/**
 * A schedule's due time is claimed by one conditional update of last_due, on
 * the Flyway table: two members that both believe they lead fire it once.
 */
@QuarkusTest
@TestProfile(NoSchedulersTestProfile.class)
@QuarkusTestResource(value = PostgresTestResource.class, restrictToAnnotatedClass = true)
class ScheduleFiringsIT {

    private static final Instant DUE = Instant.parse("2026-11-15T10:37:00Z");

    @Inject
    EntityManager em;

    @Inject
    ScheduleFirings firings;

    @Inject
    ScheduledTestRunRepository repository;

    @BeforeEach
    void resetSchedules() {
        ItSupport.truncate(em, "scheduled_test_runs", "disruption_schedules");
    }

    @Test
    void concurrentClaimsOfOneDueTimeSucceedOnce() throws Exception {
        String id = testSchedule();
        assertEquals(1, claimsWon(Kind.TESTS, id));
    }

    @Test
    void concurrentClaimsOfADisruptionDueTimeSucceedOnce() throws Exception {
        DisruptionScheduleEntity schedule = new DisruptionScheduleEntity();
        schedule.setId("dsched01");
        schedule.setName("every-minute");
        schedule.setCronExpression("* * * * *");
        schedule.setPlaybookName("az-failure");
        QuarkusTransaction.requiringNew().run(() -> em.persist(schedule));

        assertEquals(1, claimsWon(Kind.DISRUPTIONS, "dsched01"));
    }

    @Test
    void anEarlierDueTimeIsNotClaimedAfterALaterOne() {
        String id = testSchedule();
        assertTrue(firings.claim(Kind.TESTS, id, DUE));
        assertFalse(firings.claim(Kind.TESTS, id, DUE.minus(Duration.ofMinutes(1))));
        assertFalse(firings.claim(Kind.TESTS, id, DUE));
        assertTrue(firings.claim(Kind.TESTS, id, DUE.plus(Duration.ofMinutes(1))));
    }

    /**
     * A PUT that loaded the schedule before a tick's claim, and commits after
     * it, must not write its stale last_due back, or the due time fires again.
     */
    @Test
    void aSaveAfterAClaimKeepsTheClaim() {
        String id = testSchedule();

        QuarkusTransaction.requiringNew().run(() -> {
            ScheduledTestRun loaded = em.find(ScheduledTestRun.class, id);
            assertTrue(firings.claim(Kind.TESTS, id, DUE));
            loaded.setName("renamed");
        });

        ScheduledTestRun reread = QuarkusTransaction.requiringNew()
                .call(() -> repository.findById(id).orElseThrow());
        assertEquals("renamed", reread.getName());
        assertEquals(DUE, reread.getLastDue());
    }

    private String testSchedule() {
        ScheduledTestRun schedule = new ScheduledTestRun();
        schedule.setId("sched001");
        schedule.setName("every-minute");
        schedule.setCronExpression("* * * * *");
        schedule.setEnabled(false);
        schedule.setRequestJson("{\"type\":\"LOAD\"}");
        repository.save(schedule);
        return schedule.getId();
    }

    /** How many of 8 callers, released together, win the claim of DUE. */
    private int claimsWon(Kind kind, String id) throws Exception {
        int callers = 8;
        CountDownLatch go = new CountDownLatch(1);
        ExecutorService pool = Executors.newFixedThreadPool(callers);
        try {
            List<Future<Boolean>> claims = new ArrayList<>();
            for (int i = 0; i < callers; i++) {
                claims.add(pool.submit(() -> {
                    go.await();
                    return firings.claim(kind, id, DUE);
                }));
            }
            go.countDown();
            int won = 0;
            for (Future<Boolean> claim : claims) {
                won += claim.get() ? 1 : 0;
            }
            return won;
        } finally {
            pool.shutdownNow();
        }
    }
}
