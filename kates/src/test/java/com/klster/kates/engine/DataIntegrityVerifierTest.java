package com.klster.kates.engine;

import static org.junit.jupiter.api.Assertions.*;

import org.junit.jupiter.api.Test;

import com.klster.kates.domain.IntegrityResult;

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
}
