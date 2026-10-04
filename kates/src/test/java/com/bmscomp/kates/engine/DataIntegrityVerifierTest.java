package com.bmscomp.kates.engine;

import static org.junit.jupiter.api.Assertions.*;

import java.time.Duration;
import java.util.Arrays;
import java.util.Set;
import java.util.stream.Collectors;

import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.IntegrityResult;

class DataIntegrityVerifierTest {

    private AckTracker trackerWithAcked(int count) {
        AckTracker tracker = new AckTracker();
        for (int i = 0; i < count; i++) {
            tracker.recordSent(i, System.nanoTime());
            tracker.recordAcked(i);
        }
        return tracker;
    }

    @Test
    void perfectDeliveryShowsZeroLoss() {
        AckTracker tracker = trackerWithAcked(100);
        DataIntegrityVerifier verifier = new DataIntegrityVerifier(tracker);

        for (int i = 0; i < 100; i++) {
            verifier.recordConsumed(i, true, 0);
        }

        IntegrityResult result = verifier.verify(-1, true, false, false, false);
        assertEquals(100, result.totalSent());
        assertEquals(100, result.totalConsumed());
        assertEquals(0, result.lostRecords());
        assertEquals(0.0, result.dataLossPercent(), 0.001);
        assertEquals("PASS", result.verdict());
    }

    @Test
    void missingRecordsDetected() {
        AckTracker tracker = trackerWithAcked(10);
        DataIntegrityVerifier verifier = new DataIntegrityVerifier(tracker);

        for (int i = 0; i < 10; i++) {
            if (i != 3 && i != 7) {
                verifier.recordConsumed(i, true, 0);
            }
        }

        IntegrityResult result = verifier.verify(-1, true, false, false, false);
        assertEquals(10, result.totalSent());
        assertEquals(8, result.totalConsumed());
        assertEquals(2, result.lostRecords());
        assertTrue(result.dataLossPercent() > 0);
        assertEquals("DATA_LOSS", result.verdict());
    }

    @Test
    void duplicatesDetected() {
        AckTracker tracker = trackerWithAcked(5);
        DataIntegrityVerifier verifier = new DataIntegrityVerifier(tracker);

        for (int i = 0; i < 5; i++) {
            verifier.recordConsumed(i, true, 0);
        }
        verifier.recordConsumed(2, true, 0);
        verifier.recordConsumed(4, true, 0);

        IntegrityResult result = verifier.verify(-1, true, false, false, false);
        assertEquals(2, result.duplicateRecords());
    }

    @Test
    void lostRangesComputedCorrectly() {
        AckTracker tracker = trackerWithAcked(10);
        DataIntegrityVerifier verifier = new DataIntegrityVerifier(tracker);

        verifier.recordConsumed(0, true, 0);
        verifier.recordConsumed(1, true, 0);
        // gap: 2, 3, 4
        verifier.recordConsumed(5, true, 0);
        verifier.recordConsumed(6, true, 0);
        // gap: 7
        verifier.recordConsumed(8, true, 0);
        verifier.recordConsumed(9, true, 0);

        IntegrityResult result = verifier.verify(-1, true, false, false, false);
        assertEquals(4, result.lostRecords());
        assertNotNull(result.lostRanges());
        assertFalse(result.lostRanges().isEmpty());
    }

    @Test
    void crcFailureTracked() {
        AckTracker tracker = trackerWithAcked(5);
        DataIntegrityVerifier verifier = new DataIntegrityVerifier(tracker);

        verifier.recordConsumed(0, true, 0);
        verifier.recordConsumed(1, false, 0);
        verifier.recordConsumed(2, true, 0);
        verifier.recordConsumed(3, false, 0);
        verifier.recordConsumed(4, true, 0);

        IntegrityResult result = verifier.verify(-1, true, false, false, false);
        assertEquals(2, result.crcFailures());
    }

    @Test
    void legacyVerifyOverloadWorks() {
        AckTracker tracker = trackerWithAcked(3);
        DataIntegrityVerifier verifier = new DataIntegrityVerifier(tracker);

        verifier.recordConsumed(0);
        verifier.recordConsumed(1);
        verifier.recordConsumed(2);

        IntegrityResult result = verifier.verify(-1);
        assertEquals(3, result.totalSent());
        assertEquals(3, result.totalConsumed());
        assertEquals(0, result.lostRecords());
    }

    // ── RPO ─────────────────────────────────────────────────────────────

    private static final long MS = 1_000_000L;

    /** Record {@code seq} is sent at 1s + seq ms, as the producer loop would stamp it. */
    private static long sentAt(long seq) {
        return 1_000 * MS + seq * MS;
    }

    /**
     * 400 records sent 1 ms apart and every one acknowledged, including the
     * 199 sent after a fault at record 200: the producer recovered.
     */
    private static IntegrityResult verifyWithLost(long chaosStartNanos, int... lost) {
        AckTracker tracker = new AckTracker(400);
        for (int seq = 0; seq < 400; seq++) {
            tracker.recordSent(seq, sentAt(seq));
            tracker.recordAcked(seq, sentAt(seq));
        }
        DataIntegrityVerifier verifier = new DataIntegrityVerifier(tracker);
        Set<Integer> missing = Arrays.stream(lost).boxed().collect(Collectors.toSet());
        for (int seq = 0; seq < 400; seq++) {
            if (!missing.contains(seq)) {
                verifier.recordConsumed(seq, true, 0);
            }
        }
        return verifier.verify(chaosStartNanos, true, true, false, false);
    }

    @Test
    void rpoReachesBackToTheOldestLostWriteAfterTheProducerRecovered() {
        // The old formula subtracted the newest acknowledged send of the run
        // (record 399, after the fault) and clamped the negative to zero, so
        // this loss of 72 ms of acknowledged writes read as RPO 0.
        IntegrityResult result = verifyWithLost(sentAt(200), 128, 129, 130);

        assertEquals(Duration.ofMillis(72), result.rpo());
        assertEquals(72.0, result.rpoMs(), 0.001);
    }

    @Test
    void rpoIsRoundedUpToTheSampleBelowTheOldestLoss() {
        // Record 150's own send was 50 ms before the fault; the tracker keeps
        // one timestamp per 64 sends, so it reads record 128's.
        IntegrityResult result = verifyWithLost(sentAt(200), 150);

        assertTrue(result.rpoMs() >= 50.0, "never understated: " + result.rpoMs());
        assertTrue(result.rpoMs() < 50.0 + AckTracker.SEND_TIME_STRIDE, "overstated by under one stride");
    }

    @Test
    void rpoIsZeroWhenTheFaultLostNothing() {
        IntegrityResult result = verifyWithLost(sentAt(200));

        assertEquals(Duration.ZERO, result.rpo());
        assertEquals("PASS", result.verdict());
    }

    @Test
    void lossThatBeganAfterTheFaultIsDataLossNotRpo() {
        IntegrityResult result = verifyWithLost(sentAt(200), 320, 321);

        assertEquals(Duration.ZERO, result.rpo());
        assertEquals("DATA_LOSS", result.verdict());
    }

    @Test
    void rpoIsNotMeasuredWithoutAFault() {
        // No fault time means nothing to measure back from. This used to read
        // 0 ms, which a maxRpoMs gate took as a pass.
        IntegrityResult result = verifyWithLost(-1, 128);

        assertNull(result.rpo());
        assertEquals(-1.0, result.rpoMs(), 0.001);
        assertEquals("DATA_LOSS", result.verdict());
    }
}
