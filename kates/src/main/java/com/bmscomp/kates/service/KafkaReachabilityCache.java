package com.bmscomp.kates.service;

import java.util.concurrent.atomic.AtomicInteger;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import io.quarkus.scheduler.Scheduled;
import org.jboss.logging.Logger;

/**
 * Periodically refreshed view of Kafka reachability, kept OFF the kubelet probe
 * path.
 *
 * <p>The readiness check used to call {@link ClusterHealthService#isReachable()}
 * synchronously on every probe — an uncached AdminClient round-trip with a 5s
 * blocking get. Two consequences: every kubelet probe paid broker latency, and a
 * transient Kafka blip flipped readiness, pulling the whole API (including the
 * many Kafka-independent endpoints) out of the Service.
 *
 * <p>This bean moves that call onto a scheduler thread and exposes the last
 * known result plus a consecutive-failure count, so readiness can distinguish a
 * blip from a sustained outage.
 */
@ApplicationScoped
public class KafkaReachabilityCache {

    private static final Logger LOG = Logger.getLogger(KafkaReachabilityCache.class);

    private final ClusterHealthService clusterHealthService;

    /**
     * Optimistic at startup: the first refresh has not run yet, and reporting
     * NOT-ready here would stall a rollout behind the refresh interval.
     */
    private volatile boolean reachable = true;

    private volatile long lastCheckedEpochMs = 0L;
    private final AtomicInteger consecutiveFailures = new AtomicInteger(0);

    @Inject
    public KafkaReachabilityCache(ClusterHealthService clusterHealthService) {
        this.clusterHealthService = clusterHealthService;
    }

    @Scheduled(
            every = "{kates.health.kafka-refresh-interval:10s}",
            identity = "kafka-reachability-refresh",
            concurrentExecution = Scheduled.ConcurrentExecution.SKIP)
    void refresh() {
        boolean ok;
        try {
            ok = clusterHealthService.isReachable();
        } catch (Exception e) {
            // A probe refresh must never propagate — it would only poison the
            // scheduler, and "unreachable" is already the meaningful signal.
            LOG.debugf("Kafka reachability refresh failed: %s", e.getMessage());
            ok = false;
        }
        reachable = ok;
        lastCheckedEpochMs = System.currentTimeMillis();
        if (ok) {
            consecutiveFailures.set(0);
        } else {
            int failures = consecutiveFailures.incrementAndGet();
            LOG.warnf("Kafka unreachable (%d consecutive check(s))", failures);
        }
    }

    /** Last known reachability. Never blocks. */
    public boolean isReachable() {
        return reachable;
    }

    /** Consecutive failed refreshes; 0 when the last check succeeded. */
    public int consecutiveFailures() {
        return consecutiveFailures.get();
    }

    /** Epoch millis of the last refresh, or 0 if none has run yet. */
    public long lastCheckedEpochMs() {
        return lastCheckedEpochMs;
    }
}
