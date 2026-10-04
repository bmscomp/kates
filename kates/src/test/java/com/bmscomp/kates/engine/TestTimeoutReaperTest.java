package com.bmscomp.kates.engine;

import static org.junit.jupiter.api.Assertions.*;

import java.time.Duration;
import java.time.Instant;
import java.util.List;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;
import jakarta.transaction.Transactional;

import io.quarkus.test.junit.QuarkusTest;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestResult.TaskStatus;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestSpec;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.service.TestRunRepository;

/**
 * What the timeout reaper and orphan recovery leave of a run they end: the run
 * FAILED, every task row it had, and an error on each task that had not
 * finished.
 *
 * <p>Both read the RUNNING runs through findByStatus, whose runs carry no task
 * results, and saved them back that way. The save took the empty list for the
 * run's whole set of results and, under orphanRemoval, deleted every task row,
 * so the run ended FAILED with nothing it had measured: its report, its JUnit
 * file and its trend point had nothing left to read.
 *
 * <p>The reaper reads each run's deadline from what is stored with it, so a
 * run still inside its own is left alone. TestOrchestratorTest.RunDeadlines
 * covers the deadline each kind of run gets.
 */
@QuarkusTest
class TestTimeoutReaperTest {

    @Inject
    TestRunRepository repository;

    @Inject
    TestOrchestrator orchestrator;

    @Inject
    TestTimeoutReaper reaper;

    @Inject
    EntityManager em;

    @BeforeEach
    @Transactional
    void setUp() {
        // The orchestrator's startup recovery runs when the bean is first used.
        // Use it now, so that it finds an empty table rather than a run a test
        // has just stored for the reaper.
        orchestrator.activeTestCount();
        em.createQuery("DELETE FROM TestResultEntity").executeUpdate();
        em.createQuery("DELETE FROM TestRunEntity").executeUpdate();
    }

    @Test
    void theReaperKeepsEveryTaskRowOfARunItFails() {
        // Ten minutes long, and still RUNNING twenty minutes after it began:
        // past its ten minutes and the five-minute grace.
        TestRun run = storeRunning(Instant.now().minus(Duration.ofMinutes(20)));

        reaper.reapStuckTests();

        TestRun stored = reread(run);
        assertEquals(TaskStatus.FAILED, stored.getStatus());
        assertEquals(3, taskRows(run.getId()), "no task row is deleted");

        TestResult produced = task(stored, "-produce-0");
        assertEquals(TaskStatus.DONE, produced.getStatus(), "a task that finished is left as it was");
        assertEquals(50_000, produced.getRecordsSent());
        assertEquals(12.5, produced.getP99LatencyMs());
        assertNull(produced.getError());

        for (String suffix : List.of("-consume-0", "-verify-0")) {
            TestResult unfinished = task(stored, suffix);
            assertEquals(TaskStatus.FAILED, unfinished.getStatus(), suffix);
            assertEquals(
                    "Timeout: still running 900000ms after it was created, its planned 600000ms plus a 300000ms grace",
                    unfinished.getError(),
                    suffix);
            assertNotNull(unfinished.getEndTime(), suffix);
        }
        assertEquals(42_000, task(stored, "-consume-0").getRecordsSent(), "what the task measured is kept");
    }

    @Test
    void theReaperLeavesARunInsideItsDeadlineRunning() {
        // Fourteen minutes into a ten-minute run: inside the grace.
        TestRun run = storeRunning(Instant.now().minus(Duration.ofMinutes(14)));

        reaper.reapStuckTests();

        TestRun stored = reread(run);
        assertEquals(TaskStatus.RUNNING, stored.getStatus());
        assertEquals(600_000L, stored.getPlannedDurationMs());
        assertEquals(TaskStatus.RUNNING, task(stored, "-consume-0").getStatus());
        assertNull(task(stored, "-consume-0").getError());
    }

    @Test
    void recoveryKeepsEveryTaskRowOfARunItFails() {
        TestRun run = storeRunning(Instant.now());

        orchestrator.recoverOrphans();

        TestRun stored = reread(run);
        assertEquals(TaskStatus.FAILED, stored.getStatus());
        assertEquals(3, taskRows(run.getId()), "no task row is deleted");

        TestResult produced = task(stored, "-produce-0");
        assertEquals(TaskStatus.DONE, produced.getStatus(), "a task that finished is left as it was");
        assertEquals(50_000, produced.getRecordsSent());
        assertNull(produced.getError());

        for (String suffix : List.of("-consume-0", "-verify-0")) {
            TestResult unfinished = task(stored, suffix);
            assertEquals(TaskStatus.FAILED, unfinished.getStatus(), suffix);
            assertEquals("Recovered: test was orphaned after server restart", unfinished.getError(), suffix);
        }
        assertEquals(42_000, task(stored, "-consume-0").getRecordsSent(), "what the task measured is kept");
    }

    /**
     * A ten-minute RUNNING LOAD run as the orchestrator stores it once its
     * tasks are under way: a producer that has finished, a consumer still
     * reading, and a task that has not started.
     */
    private TestRun storeRunning(Instant createdAt) {
        TestSpec spec = new TestSpec();
        spec.setDurationMs(600_000);
        TestRun run = new TestRun(TestType.LOAD, spec)
                .withBackend("native")
                .withCreatedAt(createdAt.toString())
                .withPlannedDurationMs(600_000L)
                .withStatus(TaskStatus.RUNNING);
        String started = createdAt.plusSeconds(2).toString();
        run = run.withResults(List.of(
                new TestResult()
                        .withTaskId(run.getId() + "-produce-0")
                        .withTestType(TestType.LOAD)
                        .withPhaseName("produce")
                        .withStatus(TaskStatus.DONE)
                        .withRecordsSent(50_000)
                        .withThroughputRecordsPerSec(5_000)
                        .withP99LatencyMs(12.5)
                        .withStartTime(started)
                        .withEndTime(createdAt.plusSeconds(12).toString()),
                new TestResult()
                        .withTaskId(run.getId() + "-consume-0")
                        .withTestType(TestType.LOAD)
                        .withPhaseName("consume")
                        .withStatus(TaskStatus.RUNNING)
                        .withRecordsSent(42_000)
                        .withStartTime(started),
                new TestResult()
                        .withTaskId(run.getId() + "-verify-0")
                        .withTestType(TestType.LOAD)
                        .withPhaseName("verify")
                        .withStatus(TaskStatus.PENDING)));
        repository.save(run);
        return run;
    }

    /**
     * The run as stored now. Outside a transaction the test's reads share one
     * session, which still holds the run as it was first read; clear it first.
     */
    private TestRun reread(TestRun run) {
        em.clear();
        return repository.findById(run.getId()).orElseThrow();
    }

    private long taskRows(String runId) {
        return ((Number) em.createNativeQuery("SELECT COUNT(*) FROM test_results WHERE test_run_id = :id")
                        .setParameter("id", runId)
                        .getSingleResult())
                .longValue();
    }

    private static TestResult task(TestRun run, String suffix) {
        return run.getResults().stream()
                .filter(r -> r.getTaskId().endsWith(suffix))
                .findFirst()
                .orElseThrow(() -> new AssertionError("no task " + suffix + " in " + run.getResults()));
    }
}
