package com.bmscomp.kates.service;

import static java.util.stream.Collectors.toMap;
import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyInt;
import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.ArgumentMatchers.eq;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.never;
import static org.mockito.Mockito.times;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.when;

import java.time.Duration;
import java.time.Instant;
import java.time.temporal.ChronoUnit;
import java.util.ArrayList;
import java.util.EnumSet;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.function.Function;
import java.util.logging.Handler;
import java.util.logging.Level;
import java.util.logging.LogRecord;
import java.util.logging.SimpleFormatter;
import jakarta.inject.Inject;

import io.quarkus.arc.ClientProxy;
import io.quarkus.narayana.jta.QuarkusTransaction;
import io.quarkus.test.junit.QuarkusTest;
import org.jboss.logmanager.ExtLogRecord;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.mockito.ArgumentCaptor;
import org.mockito.stubbing.Answer;

import com.bmscomp.kates.audit.Actor;
import com.bmscomp.kates.domain.TestResult.TaskStatus;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.engine.TestOrchestrator;

/**
 * The Kates API's own daily retention sweep. It read every run, results and
 * all, each day; deleted STOPPING runs, whose tasks may still produce; and
 * deleted each run by its row alone, so it neither settled the run nor wrote
 * an audit row.
 *
 * <p>The runs here are created in 2001, as TestResourcePruneTest's are, and
 * each test deletes its runs after it. The test profile's retention keeps
 * the scheduled sweep off runs that old, so only the sweeps a test calls,
 * with a cutoff of its own, reach them. The runs that have not finished are
 * PENDING and STOPPING: the timeout reaper fails a RUNNING run past its
 * deadline, as one from 2001 is, at whatever moment it runs.
 */
@QuarkusTest
class TestCleanupSchedulerTest {

    private static final Instant ERA = Instant.parse("2001-01-01T00:00:00Z");
    private static final Instant CUTOFF = Instant.parse("2002-01-01T00:00:00Z");
    private static final Set<TaskStatus> FINISHED = EnumSet.of(TaskStatus.DONE, TaskStatus.FAILED);
    private static final String DETAILS_30_DAYS =
            "retention sweep: created before 2002-01-01T00:00:00Z (kates.cleanup.retention-days=30)";

    @Inject
    TestCleanupScheduler bean;

    @Inject
    TestRunRepository repository;

    @Inject
    AuditService auditService;

    private TestCleanupScheduler scheduler;
    private final List<String> stored = new ArrayList<>();
    private Instant started;

    /** Held so the logger, and the handler on it, outlive the test's own references. */
    private final java.util.logging.Logger schedulerLog =
            java.util.logging.Logger.getLogger(TestCleanupScheduler.class.getName());

    private final CapturingHandler captured = new CapturingHandler();
    private Level priorLevel;

    @BeforeEach
    void start() {
        started = Instant.now();
        // The bean behind the client proxy: a field set through the proxy is
        // the proxy's own, which the bean never reads.
        scheduler = ClientProxy.unwrap(bean);
        scheduler.batchSize = 2;
    }

    @AfterEach
    void deleteTheRuns() {
        scheduler.batchSize = 1000;
        stored.forEach(repository::delete);
        stored.clear();
    }

    /** The test profile logs at WARN, which would drop the sweep's INFO line. */
    @BeforeEach
    void captureLogs() {
        priorLevel = schedulerLog.getLevel();
        schedulerLog.setLevel(Level.INFO);
        schedulerLog.addHandler(captured);
    }

    @AfterEach
    void releaseLogs() {
        schedulerLog.removeHandler(captured);
        schedulerLog.setLevel(priorLevel);
    }

    /**
     * Five old finished runs, at two a pass, take three passes. The runs that
     * have not finished stay however old, as do the runs created at the
     * cutoff or after it, and a second sweep finds nothing to delete.
     */
    @Test
    void sweepsTheFinishedRunsCreatedBeforeTheCutoffAndAuditsEach() {
        List<String> finished = List.of(
                store(TaskStatus.DONE, day(40)),
                store(TaskStatus.FAILED, day(3)),
                store(TaskStatus.DONE, day(1)),
                store(TaskStatus.FAILED, day(200)),
                store(TaskStatus.DONE, day(364)));
        List<String> survivors = List.of(
                store(TaskStatus.STOPPING, day(1)),
                store(TaskStatus.PENDING, day(2)),
                store(TaskStatus.DONE, CUTOFF),
                store(TaskStatus.FAILED, CUTOFF.plus(1, ChronoUnit.DAYS)));

        assertEquals(5, scheduler.sweep(CUTOFF));
        // Each row names the sweep, as a schedule's firing is named.
        var bySweep = auditService.list(500, "test", started.toString(), Actor.SCHEDULER.name()).stream()
                .map(row -> (String) row.get("target"))
                .toList();
        assertTrue(bySweep.containsAll(finished), "rows by system:scheduler: " + bySweep);

        finished.forEach(this::assertGone);
        survivors.forEach(this::assertStored);
        // 36500 is kates.cleanup.retention-days in the test profile.
        String row = "DELETE: retention sweep: created before 2002-01-01T00:00:00Z"
                + " (kates.cleanup.retention-days=36500)";
        Map<String, String> rows = finished.stream().collect(toMap(Function.identity(), id -> row));
        assertEquals(rows, auditRows());

        assertEquals(0, scheduler.sweep(CUTOFF));

        survivors.forEach(this::assertStored);
        assertEquals(rows, auditRows());
    }

    /**
     * The passes below run against a RunRetention of their own, so each can
     * fail or stall on cue; no run is stored.
     *
     * <p>Here the database goes after the first pass, so counting what is
     * left fails too, and the total is logged without it.
     */
    @Test
    void aPassThatThrowsEndsTheSweepWithoutThrowing() {
        TestCleanupScheduler sweeper = sweeper();
        when(sweeper.retention.prune(any(), any(), anyInt(), anyString(), any(), any()))
                .thenAnswer(pass(5, 2, 3))
                .thenThrow(new IllegalStateException("the database is gone"));
        when(sweeper.retention.count(any(), any())).thenThrow(new IllegalStateException("the database is gone"));

        assertEquals(2, sweeper.sweep(CUTOFF));

        verify(sweeper.retention, times(2))
                .prune(eq(FINISHED), eq(CUTOFF), eq(1000), eq(DETAILS_30_DAYS), eq(Actor.SCHEDULER), any());
        assertEquals(
                List.of("Retention sweep of the runs created before 2002-01-01T00:00:00Z stopped after deleting 2:"
                        + " the database is gone"),
                logged(Level.SEVERE));
        assertEquals(
                List.of("Retention sweep deleted 2 DONE or FAILED run(s) created before 2002-01-01T00:00:00Z"
                        + " (kates.cleanup.retention-days=30)"),
                logged(Level.INFO));
    }

    /**
     * A pass that throws part-way returns nothing, but the runs it deleted
     * before the throw are gone, each with its audit row. The sweep counted
     * neither them nor what was left: after deleting 3 here it returned 2,
     * and logged "stopped after deleting 2" and the first pass's "4 still
     * match".
     */
    @Test
    void aPassThatThrowsPartWayCountsTheRunsItDeleted() {
        TestOrchestrator orchestrator = mock(TestOrchestrator.class);
        TestRunRepository runs = mock(TestRunRepository.class);
        AuditService audit = mock(AuditService.class);
        TestCleanupScheduler sweeper = sweeper();
        sweeper.retention = new RunRetention(orchestrator, runs, audit);
        sweeper.batchSize = 2;
        when(runs.countByStatusCreatedBefore(FINISHED, CUTOFF)).thenReturn(6L, 4L, 4L, 3L);
        when(runs.findIdsByStatusCreatedBefore(FINISHED, CUTOFF, 2)).thenReturn(List.of("a", "b"), List.of("c", "d"));
        when(orchestrator.deleteTest(anyString())).thenReturn(true);
        when(orchestrator.deleteTest("d")).thenThrow(new IllegalStateException("lock timeout"));

        assertEquals(3, sweeper.sweep(CUTOFF));

        List.of("a", "b", "c")
                .forEach(id -> verify(audit).record("DELETE", "test", id, DETAILS_30_DAYS, Actor.SCHEDULER));
        verify(audit, never()).record(anyString(), anyString(), eq("d"), anyString(), any());
        assertEquals(
                List.of("Retention sweep of the runs created before 2002-01-01T00:00:00Z stopped after deleting 3:"
                        + " lock timeout"),
                logged(Level.SEVERE));
        assertEquals(
                List.of("Retention sweep deleted 3 DONE or FAILED run(s) created before 2002-01-01T00:00:00Z"
                        + " (kates.cleanup.retention-days=30); 3 still match"),
                logged(Level.INFO));
    }

    /** The scheduled method sweeps from retention-days ago, and a failure stays in it. */
    @Test
    void theScheduledSweepCutsOffAtTheRetentionAndThrowsNothing() {
        TestCleanupScheduler sweeper = sweeper();
        when(sweeper.retention.prune(any(), any(), anyInt(), anyString(), any(), any()))
                .thenThrow(new IllegalStateException("the database is gone"));

        Instant from = Instant.now().minus(Duration.ofDays(30));
        assertDoesNotThrow(sweeper::cleanupOldTests);
        Instant to = Instant.now().minus(Duration.ofDays(30));

        ArgumentCaptor<Instant> cutoff = ArgumentCaptor.forClass(Instant.class);
        verify(sweeper.retention)
                .prune(eq(FINISHED), cutoff.capture(), eq(1000), anyString(), eq(Actor.SCHEDULER), any());
        assertFalse(cutoff.getValue().isBefore(from), cutoff.getValue() + " is at or after " + from);
        assertFalse(cutoff.getValue().isAfter(to), cutoff.getValue() + " is at or before " + to);
    }

    /**
     * A pass that deletes none found its runs gone, taken by another delete,
     * and the sweep stops rather than read the same runs again.
     */
    @Test
    void aPassThatDeletesNoneEndsTheSweep() {
        TestCleanupScheduler sweeper = sweeper();
        when(sweeper.retention.prune(any(), any(), anyInt(), anyString(), any(), any()))
                .thenAnswer(pass(5, 0, 5));

        assertEquals(0, sweeper.sweep(CUTOFF));

        verify(sweeper.retention).prune(any(), any(), anyInt(), anyString(), any(), any());
        assertEquals(List.of(), logged(Level.INFO));
    }

    @Test
    void aSweepStopsAfterAThousandPasses() {
        TestCleanupScheduler sweeper = sweeper();
        when(sweeper.retention.prune(any(), any(), anyInt(), anyString(), any(), any()))
                .thenAnswer(pass(2, 1, 1));

        assertEquals(1000, sweeper.sweep(CUTOFF));

        verify(sweeper.retention, times(1000)).prune(any(), any(), anyInt(), anyString(), any(), any());
    }

    /** A scheduler of its own, with a mocked RunRetention and 30 days' retention. */
    private static TestCleanupScheduler sweeper() {
        TestCleanupScheduler sweeper = new TestCleanupScheduler();
        sweeper.retention = mock(RunRetention.class);
        sweeper.retentionDays = 30;
        return sweeper;
    }

    /**
     * A pass that reports each of its {@code deleted} runs as it deletes it,
     * as RunRetention does, and returns the counts given.
     */
    private static Answer<RunRetention.Pass> pass(long matched, int deleted, long remaining) {
        return invocation -> {
            Runnable onDeleted = invocation.getArgument(5);
            for (int i = 0; i < deleted; i++) {
                onDeleted.run();
            }
            return new RunRetention.Pass(matched, deleted, remaining);
        };
    }

    /** The scheduler's log lines at {@code level}, formatted. */
    private List<String> logged(Level level) {
        return captured.records.stream()
                .filter(r -> r.getLevel().intValue() == level.intValue())
                .map(r -> r instanceof ExtLogRecord ext
                        ? ext.getFormattedMessage()
                        : new SimpleFormatter().formatMessage(r))
                .toList();
    }

    private static Instant day(int n) {
        return ERA.plus(n, ChronoUnit.DAYS);
    }

    /** Stores a LOAD run with this status, created at {@code createdAt}, for deletion after the test. */
    private String store(TaskStatus status, Instant createdAt) {
        TestRun run = new TestRun(TestType.LOAD, null)
                .withBackend("native")
                .withStatus(status)
                .withCreatedAt(createdAt.toString());
        repository.save(run);
        stored.add(run.getId());
        return run.getId();
    }

    private void assertStored(String id) {
        assertTrue(isStored(id), "run " + id + " is still stored");
    }

    private void assertGone(String id) {
        assertFalse(isStored(id), "run " + id + " is deleted");
    }

    /**
     * Read in a transaction of its own. Outside one, a test's reads share one
     * persistence context for the whole test, which keeps a run it read even
     * after the sweep deleted it.
     */
    private boolean isStored(String id) {
        return QuarkusTransaction.requiringNew()
                .call(() -> repository.findById(id).isPresent());
    }

    /**
     * The audit rows this test wrote of its runs, as each run's id to the
     * row's action and details. A run with two rows fails here, on the
     * duplicate key.
     */
    private Map<String, String> auditRows() {
        return auditService.list(500, "test", started.toString(), null).stream()
                .filter(row -> stored.contains((String) row.get("target")))
                .collect(
                        toMap(row -> (String) row.get("target"), row -> row.get("action") + ": " + row.get("details")));
    }

    private static final class CapturingHandler extends Handler {
        final List<LogRecord> records = new CopyOnWriteArrayList<>();

        @Override
        public void publish(LogRecord record) {
            records.add(record);
        }

        @Override
        public void flush() {}

        @Override
        public void close() {}
    }
}
