package com.bmscomp.kates.api;

import java.lang.management.ManagementFactory;
import java.lang.management.ThreadMXBean;

import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.eclipse.microprofile.health.HealthCheck;
import org.eclipse.microprofile.health.HealthCheckResponse;
import org.eclipse.microprofile.health.Liveness;

import jakarta.inject.Inject;

/**
 * Liveness probe: verifies JVM health beyond just "running".
 * Checks heap usage, deadlock state, and reports active test count.
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
        boolean heapOk = freeHeapMb >= minFreeHeapMb;

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
                .withData("deadlockedThreads", deadlocked != null ? deadlocked.length : 0)
                .withData("activeTests", activeTests + "/" + maxTests);

        if (heapOk && noDeadlocks) {
            return builder.up().build();
        }
        return builder.down().build();
    }
}
