package com.bmscomp.kates.chaos;

import static org.junit.jupiter.api.Assertions.*;

import java.time.Duration;
import java.time.Instant;
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

    /** Three Ready brokers, watched before the fault goes in. */
    private static K8sPodWatcher.WatchSession threeBrokers() {
        K8sPodWatcher.WatchSession session = new K8sPodWatcher.WatchSession();
        session.expectPods(3);
        session.recordInitial("krafter-brokers-0", true);
        session.recordInitial("krafter-brokers-1", true);
        session.recordInitial("krafter-brokers-2", true);
        return session;
    }

    /** Three Ready brokers, the disruption just started. */
    private static K8sPodWatcher.WatchSession threeBrokersDisrupted() {
        K8sPodWatcher.WatchSession session = threeBrokers();
        session.markDisruptionStart(Instant.now());
        return session;
    }

    @Test
    void recoveryCountsFromTheMomentTheFaultWentIn() {
        // The provider's report can reach the watch a little after the moment
        // it reports: litmus-crd reports the start it took before picking pods.
        K8sPodWatcher.WatchSession session = threeBrokers();
        session.markDisruptionStart(Instant.now().minusSeconds(5));

        session.recordNotReady("krafter-brokers-0");
        session.recordReady("krafter-brokers-0");

        K8sPodWatcher.RecoveryMetrics recovery = session.computeRecovery();
        assertTrue(recovery.timeToFirstReady().compareTo(Duration.ofSeconds(5)) >= 0);
        assertTrue(recovery.timeToAllReady().compareTo(Duration.ofSeconds(5)) >= 0);
    }

    @Test
    void aPodThatCameBackBeforeTheFaultWentInIsNotTheFaults() {
        // A restart during the fault's delayBeforeSec, before the provider
        // reported the fault going in.
        K8sPodWatcher.WatchSession session = threeBrokers();
        session.recordNotReady("krafter-brokers-0");
        session.recordReady("krafter-brokers-0");

        session.markDisruptionStart(Instant.now());

        K8sPodWatcher.RecoveryMetrics recovery = session.computeRecovery();
        assertFalse(recovery.podWentDown());
        assertNull(recovery.timeToFirstReady());
        assertNull(recovery.timeToAllReady());
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
