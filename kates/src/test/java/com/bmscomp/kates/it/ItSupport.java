package com.bmscomp.kates.it;

import java.time.Duration;
import java.util.List;
import java.util.UUID;
import java.util.function.BooleanSupplier;
import jakarta.persistence.EntityManager;

import io.quarkus.narayana.jta.QuarkusTransaction;

import com.bmscomp.kates.domain.CreateTestRequest;
import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestSpec;
import com.bmscomp.kates.domain.TestType;

/**
 * Shared fixtures for the {@code *IT} suite.
 *
 * <p>Extracted because {@code waitUntil} had already been copy-pasted into two
 * IT classes with two different tick intervals, and because every IT that needs
 * a persisted run was about to re-derive the same "single-broker safe" spec
 * (replication factor 1, min-ISR 1, unique topic) by hand.
 */
final class ItSupport {

    /**
     * Long enough to absorb a cold container and a loaded CI box, short enough
     * that a genuinely stuck condition fails the test rather than the job.
     */
    static final Duration DEFAULT_TIMEOUT = Duration.ofSeconds(30);

    private static final Duration TICK = Duration.ofMillis(200);

    private ItSupport() {}

    /** Polls {@code condition} until it holds or {@code timeout} elapses. */
    static boolean waitUntil(Duration timeout, BooleanSupplier condition) {
        long deadline = System.nanoTime() + timeout.toNanos();
        while (System.nanoTime() < deadline) {
            if (condition.getAsBoolean()) {
                return true;
            }
            try {
                Thread.sleep(TICK.toMillis());
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                return false;
            }
        }
        // One last read: the condition may have flipped inside the final sleep.
        return condition.getAsBoolean();
    }

    static boolean waitUntil(BooleanSupplier condition) {
        return waitUntil(DEFAULT_TIMEOUT, condition);
    }

    /**
     * A topic name unique per call. Container brokers are reused across the
     * methods of a class, so a fixed name would leak offsets and partition
     * state from one test into the next.
     *
     * <p>Deliberately hex, not {@code System.nanoTime()}: a nanosecond counter
     * renders as 13-16 digits for most machine uptimes, and the security secret
     * scanner treats any 13-16 digit run as a candidate credit-card number. Topic
     * names built from nanoTime therefore made {@link SecurityApiIT} report
     * findings against the test suite's own fixtures.
     */
    static String uniqueTopic(String prefix) {
        return prefix + "-" + UUID.randomUUID().toString().substring(0, 8);
    }

    /**
     * A spec that a single-broker test container can actually satisfy: the
     * production defaults are replication factor 3 / min-ISR 2, which a
     * one-node cluster rejects.
     *
     * <p>{@code topic} may be null. Report generation captures a cluster
     * snapshot whenever the spec names a topic, which costs a 15-second
     * AdminClient timeout per run in the ITs that have no broker attached — so
     * those pass null.
     */
    static TestSpec singleBrokerSpec(String topic) {
        TestSpec spec = new TestSpec();
        if (topic != null) {
            spec.setTopic(topic);
        }
        spec.setNumRecords(25);
        spec.setRecordSize(512);
        spec.setPartitions(1);
        spec.setReplicationFactor(1);
        spec.setMinInsyncReplicas(1);
        spec.setAcks("all");
        spec.setDurationMs(60_000);
        return spec;
    }

    static CreateTestRequest createRequest(TestType type, TestSpec spec) {
        CreateTestRequest request = new CreateTestRequest();
        request.setType(type);
        request.setSpec(spec);
        request.setBackend("native");
        return request;
    }

    /**
     * A finished run carrying one result, for the read-side endpoints (reports,
     * trends, profiles, baselines) that need history but not a live broker.
     *
     * <p>Pass a null {@code topic} when no broker is attached — see
     * {@link #singleBrokerSpec(String)}.
     */
    static TestRun finishedRun(TestType type, String topic, long records, double p50Ms, double p99Ms) {
        TestResult result = new TestResult()
                .withTaskId("task-" + System.nanoTime())
                .withTestType(type)
                .withStatus(TestResult.TaskStatus.DONE)
                .withRecordsSent(records)
                .withThroughputRecordsPerSec(records / 10.0)
                .withThroughputMBPerSec(records * 512 / 10.0 / (1024 * 1024))
                .withAvgLatencyMs(p50Ms)
                .withP50LatencyMs(p50Ms)
                .withP95LatencyMs(p99Ms * 0.9)
                .withP99LatencyMs(p99Ms)
                .withMaxLatencyMs(p99Ms * 1.5)
                .withStartTime("2026-01-01T00:00:00Z")
                .withEndTime("2026-01-01T00:00:10Z");

        return new TestRun(type, singleBrokerSpec(topic))
                .withStatus(TestResult.TaskStatus.DONE)
                .withBackend("native")
                .withResults(List.of(result));
    }

    /**
     * Empties the given tables in a fresh transaction.
     *
     * <p>The Postgres container is per test class, not per method, so anything
     * asserting an absolute row count has to start from a known state. Uses
     * TRUNCATE ... CASCADE rather than DELETE so child rows go with the parent
     * without the caller having to know the FK graph.
     */
    static void truncate(EntityManager em, String... tables) {
        QuarkusTransaction.requiringNew()
                .run(() -> em.createNativeQuery("TRUNCATE TABLE " + String.join(", ", tables) + " CASCADE")
                        .executeUpdate());
    }
}
