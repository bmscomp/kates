package com.bmscomp.kates.service;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

/**
 * The cache readiness reads instead of calling Kafka on the probe path. Its
 * failure counting is what lets readiness tell a blip from an outage, so the
 * counter transitions are the thing worth pinning down.
 */
class KafkaReachabilityCacheTest {

    @Test
    @DisplayName("optimistic before the first refresh")
    void startsReachable() {
        KafkaReachabilityCache cache = new KafkaReachabilityCache(mock(ClusterHealthService.class));

        assertTrue(cache.isReachable(), "reporting DOWN before any check would stall a rollout");
        assertEquals(0, cache.consecutiveFailures());
        assertEquals(0L, cache.lastCheckedEpochMs());
    }

    @Test
    @DisplayName("failures accumulate, a success resets them")
    void countsConsecutiveFailures() {
        ClusterHealthService health = mock(ClusterHealthService.class);
        KafkaReachabilityCache cache = new KafkaReachabilityCache(health);

        when(health.isReachable()).thenReturn(false);
        cache.refresh();
        cache.refresh();
        cache.refresh();

        assertFalse(cache.isReachable());
        assertEquals(3, cache.consecutiveFailures());

        when(health.isReachable()).thenReturn(true);
        cache.refresh();

        assertTrue(cache.isReachable());
        assertEquals(0, cache.consecutiveFailures(), "a single success clears the streak");
    }

    @Test
    @DisplayName("a thrown refresh counts as unreachable, never propagates")
    void swallowsRefreshExceptions() {
        ClusterHealthService health = mock(ClusterHealthService.class);
        when(health.isReachable()).thenThrow(new RuntimeException("broker gone"));
        KafkaReachabilityCache cache = new KafkaReachabilityCache(health);

        assertDoesNotThrow(cache::refresh);
        assertFalse(cache.isReachable());
        assertEquals(1, cache.consecutiveFailures());
    }

    @Test
    @DisplayName("every refresh stamps the check time")
    void recordsLastCheckedTime() {
        ClusterHealthService health = mock(ClusterHealthService.class);
        when(health.isReachable()).thenReturn(true);
        KafkaReachabilityCache cache = new KafkaReachabilityCache(health);

        long before = System.currentTimeMillis();
        cache.refresh();

        assertTrue(cache.lastCheckedEpochMs() >= before, "staleness is only detectable if this is stamped");
    }
}
