package com.bmscomp.kates.engine;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.util.BitSet;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;

import org.junit.jupiter.api.Test;

/**
 * Covers the bitset-backed tracker that replaced two per-sequence skip-list
 * maps (~20M boxed entries on a 10M-record run).
 */
class AckTrackerTest {

    @Test
    void ackedSetReflectsAcknowledgedSequences() {
        AckTracker tracker = new AckTracker(100);
        for (long seq = 0; seq < 10; seq++) {
            tracker.recordSent(seq, System.nanoTime());
        }
        tracker.recordAcked(0, 111L);
        tracker.recordAcked(5, 222L);
        tracker.recordFailed(7);

        BitSet acked = tracker.getAckedSet();
        assertTrue(acked.get(0));
        assertTrue(acked.get(5));
        assertFalse(acked.get(7), "a failed sequence is not acked");
        assertFalse(acked.get(1));
        assertEquals(2, acked.cardinality());

        assertEquals(10, tracker.getTotalSent());
        assertEquals(2, tracker.getTotalAcked());
        assertEquals(1, tracker.getTotalFailed());
    }

    @Test
    void ackUsesTheSendTimestampHandedBackByTheCallback() {
        AckTracker tracker = new AckTracker(10);
        tracker.recordSent(0, 999L);
        tracker.recordAcked(0, 999L);
        // The tracker no longer stores a timestamp per sequence; the callback
        // supplies the one it already holds.
        assertEquals(999L, tracker.getLastAckedSendNanos());
    }

    @Test
    void sequencesBeyondCapacityDoNotThrow() {
        AckTracker tracker = new AckTracker(4);
        tracker.recordSent(99, System.nanoTime());
        tracker.recordAcked(99, 1L);

        // Counters still see it; only per-sequence tracking is bounded.
        assertEquals(1, tracker.getTotalAcked());
        assertEquals(0, tracker.getAckedSet().cardinality());
        assertEquals(4, tracker.trackedCapacity());
    }

    @Test
    void failureWindowClosesOnRecovery() {
        AckTracker tracker = new AckTracker(10);
        tracker.recordSent(0, System.nanoTime());
        tracker.recordFailed(0);
        assertTrue(tracker.isInFailure());

        tracker.recordAcked(1, System.nanoTime());
        assertFalse(tracker.isInFailure(), "an ack closes the open failure window");
        assertEquals(1, tracker.getCompletedWindows().size());
        assertTrue(tracker.maxRtoNanos() >= 0);
    }

    @Test
    void concurrentAcksAreAllRecorded() throws InterruptedException {
        int threads = 8;
        int perThread = 2_000;
        AckTracker tracker = new AckTracker(threads * perThread);
        CountDownLatch start = new CountDownLatch(1);
        CountDownLatch done = new CountDownLatch(threads);

        for (int t = 0; t < threads; t++) {
            final int offset = t * perThread;
            Thread.ofVirtual().start(() -> {
                try {
                    start.await();
                    for (int i = 0; i < perThread; i++) {
                        tracker.recordAcked(offset + i, 1L);
                    }
                } catch (InterruptedException e) {
                    Thread.currentThread().interrupt();
                } finally {
                    done.countDown();
                }
            });
        }

        start.countDown();
        assertTrue(done.await(30, TimeUnit.SECONDS), "workers finished");

        // The CAS bit-set must not lose updates when producer callbacks from
        // different threads land in the same 64-sequence word.
        assertEquals(threads * perThread, tracker.getAckedSet().cardinality());
        assertEquals(threads * perThread, tracker.getTotalAcked());
    }
}
