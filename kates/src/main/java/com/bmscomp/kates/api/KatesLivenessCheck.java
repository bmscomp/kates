package com.bmscomp.kates.api;

import java.lang.management.ManagementFactory;
import java.lang.management.ThreadMXBean;
import jakarta.inject.Inject;

import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.eclipse.microprofile.health.HealthCheck;
import org.eclipse.microprofile.health.HealthCheckResponse;
import org.eclipse.microprofile.health.Liveness;

/**
 * Liveness probe: answers only "is this JVM unrecoverably stuck?".
 *
 * <p>Deadlocked threads are the one condition a restart actually fixes, so that
 * is the sole failure signal. Heap pressure is reported as data but NEVER fails
 * the probe: a large benchmark legitimately drives heap toward the limit, and
 * failing liveness there had Kubernetes kill the pod MID-TEST — the probe caused
 * exactly the outage it was meant to detect, in a restart loop that got worse
 * the more load the pod carried. Genuine exhaustion still surfaces as an
 * OOMKill, which the kubelet handles on its own.
 */
@Liveness
public class KatesLivenessCheck implements HealthCheck {

    @Inject
    com.bmscomp.kates.engine.TestOrchestrator orchestrator;

    @ConfigProperty(name = "kates.health.min-free-heap-mb", defaultValue = "64")
    int minFreeHeapMb;

    @Override
    public HealthCheckResponse call() {
        Runtime rt = Runtime.getRuntime();
        long freeHeapMb = (rt.maxMemory() - rt.totalMemory() + rt.freeMemory()) / (1024 * 1024);
        boolean heapLow = freeHeapMb < minFreeHeapMb;

        ThreadMXBean threads = ManagementFactory.getThreadMXBean();
        long[] deadlocked = threads.findDeadlockedThreads();
        boolean noDeadlocks = deadlocked == null || deadlocked.length == 0;

        int activeTests = 0;
        int maxTests = 0;
        try {
            activeTests = orchestrator.activeTestCount();
            maxTests = orchestrator.maxConcurrentTests();
        } catch (Exception ignored) {
        }

        var builder = HealthCheckResponse.named("kates-liveness")
                .withData("freeHeapMb", freeHeapMb)
                .withData("minFreeHeapMb", minFreeHeapMb)
                // Advisory only — see the class javadoc. Surfaced so operators
                // and alerts can see heap pressure without the kubelet acting
                // on it.
                .withData("heapLow", heapLow)
                .withData("deadlockedThreads", deadlocked != null ? deadlocked.length : 0)
                .withData("activeTests", activeTests + "/" + maxTests);

        if (noDeadlocks) {
            return builder.up().build();
        }
        return builder.down().build();
    }
}
