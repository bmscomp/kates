package com.bmscomp.kates.chaos;

import static org.junit.jupiter.api.Assertions.*;

import java.util.concurrent.TimeUnit;

import io.fabric8.kubernetes.api.model.Pod;
import io.fabric8.kubernetes.api.model.PodBuilder;
import org.junit.jupiter.api.Test;

/**
 * The recovery timings the SLA grader reads. Time-to-all-ready used to count
 * only pods that reported Ready after the disruption started; a broker the
 * fault left alone sends no event, so after a single-pod fault the count never
 * reached the total and the recovery time was never set.
 */
class K8sPodWatcherTest {

    /** Three Ready brokers, the disruption just started. */
    private static K8sPodWatcher.WatchSession threeBrokersDisrupted() {
        K8sPodWatcher.WatchSession session = new K8sPodWatcher.WatchSession();
        session.expectPods(3);
        session.recordInitial("krafter-brokers-0", true);
        session.recordInitial("krafter-brokers-1", true);
        session.recordInitial("krafter-brokers-2", true);
        session.markDisruptionStart();
        return session;
    }

    @Test
    void theOnePodAFaultTookDownComingBackSetsTimeToAllReady() {
        K8sPodWatcher.WatchSession session = threeBrokersDisrupted();

        session.recordNotReady("krafter-brokers-0");
        session.recordReady("krafter-brokers-0");

        K8sPodWatcher.RecoveryMetrics recovery = session.computeRecovery();
        assertNotNull(recovery.timeToAllReady());
        assertNotNull(recovery.timeToFirstReady());
        assertTrue(recovery.podWentDown());
    }

    @Test
    void aPodStillDownLeavesTimeToAllReadyUnsetAndSaysSo() {
        K8sPodWatcher.WatchSession session = threeBrokersDisrupted();

        session.recordNotReady("krafter-brokers-0");

        K8sPodWatcher.RecoveryMetrics recovery = session.computeRecovery();
        assertNull(recovery.timeToAllReady());
        assertTrue(recovery.podWentDown());
    }

    @Test
    void aFaultThatTookNoPodDownHasNoRecoveryTime() {
        K8sPodWatcher.WatchSession session = threeBrokersDisrupted();

        // A status update on a broker that stayed Ready.
        session.recordReady("krafter-brokers-1");

        K8sPodWatcher.RecoveryMetrics recovery = session.computeRecovery();
        assertNull(recovery.timeToAllReady());
        assertFalse(recovery.podWentDown());
    }

    @Test
    void aPodThatDropsAgainClearsTimeToAllReady() {
        K8sPodWatcher.WatchSession session = threeBrokersDisrupted();

        session.recordNotReady("krafter-brokers-0");
        session.recordReady("krafter-brokers-0");
        session.recordNotReady("krafter-brokers-2");

        assertNull(session.computeRecovery().timeToAllReady());
    }

    @Test
    void recoveryWaitsForEveryPodNotJustTheFirst() throws Exception {
        K8sPodWatcher.WatchSession session = threeBrokersDisrupted();
        session.recordNotReady("krafter-brokers-0");
        session.recordNotReady("krafter-brokers-1");
        session.recordReady("krafter-brokers-0");

        // The first pod back used to open the gate.
        assertFalse(session.awaitRecovery(50, TimeUnit.MILLISECONDS));

        Thread.ofVirtual().start(() -> {
            try {
                Thread.sleep(50);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
            }
            session.recordReady("krafter-brokers-1");
        });
        assertTrue(session.awaitRecovery(5, TimeUnit.SECONDS));
        assertNotNull(session.computeRecovery().timeToAllReady());
    }

    @Test
    void recoveryTimesOutWhenNoPodComesBack() throws Exception {
        K8sPodWatcher.WatchSession session = threeBrokersDisrupted();
        session.recordNotReady("krafter-brokers-0");

        assertFalse(session.awaitRecovery(50, TimeUnit.MILLISECONDS));
    }

    private static Pod readyPod(String deletionTimestamp) {
        return new PodBuilder()
                .withNewMetadata()
                .withName("krafter-brokers-0")
                .withDeletionTimestamp(deletionTimestamp)
                .endMetadata()
                .withNewStatus()
                .addNewCondition()
                .withType("Ready")
                .withStatus("True")
                .endCondition()
                .endStatus()
                .build();
    }

    @Test
    void aTerminatingPodIsNotReady() {
        // It keeps its Ready condition until its containers stop, so a
        // graceful delete looked recovered the moment it started.
        assertTrue(K8sPodWatcher.isPodReady(readyPod(null)));
        assertFalse(K8sPodWatcher.isPodReady(readyPod("2026-09-23T12:00:00Z")));
    }
}
