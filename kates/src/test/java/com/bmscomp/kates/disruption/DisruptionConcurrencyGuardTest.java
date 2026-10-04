package com.bmscomp.kates.disruption;

import static org.junit.jupiter.api.Assertions.*;

import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;

import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

class DisruptionConcurrencyGuardTest {

    private DisruptionConcurrencyGuard guard;

    @BeforeEach
    void setUp() {
        guard = new DisruptionConcurrencyGuard();
        guard.kafkaNamespace = "kafka";
        guard.kafkaCluster = "krafter";
    }

    @Test
    @DisplayName("target is namespace/cluster")
    void targetCombinesNamespaceAndCluster() {
        assertEquals("kafka/krafter", guard.currentTarget());
    }

    @Test
    @DisplayName("second plan cannot acquire while the first holds the lease")
    void secondAcquireIsRefused() {
        String target = guard.currentTarget();

        assertTrue(guard.tryAcquire(target, "plan-a#1"));
        assertFalse(guard.tryAcquire(target, "plan-b#2"));
        assertTrue(guard.isBusy(target));
        assertEquals("plan-a#1", guard.activeToken(target).orElseThrow());
    }

    @Test
    @DisplayName("release frees the lease for the next plan")
    void releaseAllowsNextPlan() {
        String target = guard.currentTarget();
        guard.tryAcquire(target, "plan-a#1");

        guard.release(target, "plan-a#1");

        assertFalse(guard.isBusy(target));
        assertTrue(guard.tryAcquire(target, "plan-b#2"));
    }

    @Test
    @DisplayName("a non-holder cannot release someone else's lease")
    void releaseIsHolderScoped() {
        String target = guard.currentTarget();
        guard.tryAcquire(target, "plan-a#1");

        // Same plan name, different run: must not steal the lease.
        guard.release(target, "plan-a#2");

        assertTrue(guard.isBusy(target));
        assertEquals("plan-a#1", guard.activeToken(target).orElseThrow());
    }

    @Test
    @DisplayName("different targets are independent")
    void targetsAreIndependent() {
        assertTrue(guard.tryAcquire("kafka/a", "plan#1"));
        assertTrue(guard.tryAcquire("kafka/b", "plan#2"));
    }

    @Test
    @DisplayName("exactly one of many concurrent acquires wins")
    void onlyOneConcurrentAcquireWins() throws Exception {
        String target = guard.currentTarget();
        int threads = 32;
        CountDownLatch start = new CountDownLatch(1);
        CountDownLatch done = new CountDownLatch(threads);
        AtomicInteger winners = new AtomicInteger();

        for (int i = 0; i < threads; i++) {
            final int idx = i;
            new Thread(() -> {
                        try {
                            start.await();
                            if (guard.tryAcquire(target, "plan#" + idx)) {
                                winners.incrementAndGet();
                            }
                        } catch (InterruptedException e) {
                            Thread.currentThread().interrupt();
                        } finally {
                            done.countDown();
                        }
                    })
                    .start();
        }

        start.countDown();
        assertTrue(done.await(10, TimeUnit.SECONDS));
        assertEquals(1, winners.get());
    }
}
