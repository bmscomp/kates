package com.klster.kates.engine;

import java.time.Duration;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import io.micrometer.core.instrument.Counter;
import io.micrometer.core.instrument.DistributionSummary;
import io.micrometer.core.instrument.MeterRegistry;
import io.micrometer.core.instrument.Timer;

/**
 * Platform-level Prometheus metrics for Kates.
 * Exposes aggregate counters and distributions at /q/metrics that persist
 * across individual benchmark runs — suitable for Grafana dashboards.
 */
@ApplicationScoped
public class KatesMetrics {

    private final MeterRegistry registry;

    @Inject
    public KatesMetrics(MeterRegistry registry) {
        this.registry = registry;
    }

    public void recordTestCompleted(String testType, String outcome) {
        Counter.builder("kates.tests.completed.total")
                .tags("test_type", testType, "outcome", outcome)
                .description("Total tests completed")
                .register(registry)
                .increment();
    }

    public void recordTestDuration(String testType, Duration duration) {
        Timer.builder("kates.tests.duration.seconds")
                .tags("test_type", testType)
                .description("Test execution duration")
                .publishPercentiles(0.5, 0.95, 0.99)
                .register(registry)
                .record(duration);
    }

    public void recordFinalThroughput(String testType, double recPerSec, double mbPerSec) {
        DistributionSummary.builder("kates.tests.throughput.rec.sec")
                .tags("test_type", testType)
                .description("Final throughput per completed test (records/sec)")
                .register(registry)
                .record(recPerSec);

        DistributionSummary.builder("kates.tests.throughput.mb.sec")
                .tags("test_type", testType)
                .description("Final throughput per completed test (MB/sec)")
                .register(registry)
                .record(mbPerSec);
    }

    public void recordSlaEvaluation(String testType, boolean passed) {
        Counter.builder("kates.sla.evaluations.total")
                .tags("test_type", testType, "result", passed ? "pass" : "fail")
                .description("SLA evaluation outcomes")
                .register(registry)
                .increment();
    }

    public void recordDisruptionCompleted(String disruptionType, String outcome) {
        Counter.builder("kates.disruptions.completed.total")
                .tags("disruption_type", disruptionType, "outcome", outcome)
                .description("Total disruption executions completed")
                .register(registry)
                .increment();
    }

    public void recordDisruptionDuration(String disruptionType, Duration duration) {
        Timer.builder("kates.disruptions.duration.seconds")
                .tags("disruption_type", disruptionType)
                .description("Disruption execution duration")
                .publishPercentiles(0.5, 0.95)
                .register(registry)
                .record(duration);
    }

    public void recordRecordsProcessed(String testType, long records) {
        Counter.builder("kates.records.processed.total")
                .tags("test_type", testType)
                .description("Cumulative records processed across all tests")
                .register(registry)
                .increment(records);
    }
}
