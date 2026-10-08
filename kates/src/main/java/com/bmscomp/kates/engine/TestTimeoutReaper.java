package com.bmscomp.kates.engine;

import java.time.Instant;
import java.util.List;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import io.quarkus.scheduler.Scheduled;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.service.TestRunRepository;

/**
 * Periodically fails runs that have not ended past their deadline: the run's
 * planned duration, counted from its creation, plus a grace.
 *
 * <p>The orchestrator stores the planned duration with the run when it
 * creates it (TestOrchestrator.plannedDurationMs): the run's durationMs, twice
 * that for INTEGRITY, the sum of the phases for a scenario.
 * {@code kates.engine.max-duration-ms} caps it, and stands in for it when a
 * run has none. Every run used to get that cap alone, 30 minutes, whatever it
 * was set to last, so a default ENDURANCE run, an hour long, always ended
 * FAILED.
 *
 * <p>A PENDING or STOPPING run is held to the same deadline as a RUNNING one,
 * as nothing else ends it once the process that had it is gone: orphan
 * recovery reads RUNNING runs only, and the reconciler only this process's
 * handles. A run stays PENDING when its submission never stored it RUNNING,
 * as when the Kates API stops while it starts the run's tasks. It stays
 * STOPPING when a Kates API before 1.25.0, which stored a run STOPPING on the
 * way to ending it, stopped before it did; no later version stores it. Such a
 * run used to stay so for good, and the daily sweep, which deletes only ended
 * runs, never took it.
 *
 * <p>The deadline is the one rule that holds whichever replica runs the run.
 * Which process runs it is not stored, and its handles and its concurrency
 * permit are in that process's memory, so a run with no handle here may be
 * another replica's, alive; a PENDING run has no handle even in its own
 * process, as they are published once it is stored RUNNING. Orphan recovery
 * does not tell the two apart either: it fails every RUNNING run it finds,
 * which is why the chart runs one replica. The deadline is stored with the
 * run and bounds its whole life, whoever runs it: a RUNNING run past it is
 * failed here already, so failing a PENDING or STOPPING one past it ends no
 * run sooner than that.
 */
@ApplicationScoped
public class TestTimeoutReaper {

    private static final Logger LOG = Logger.getLogger(TestTimeoutReaper.class);

    /** The statuses of a run that has not ended: each is held to the run's deadline. */
    private static final List<TestResult.TaskStatus> NOT_ENDED =
            List.of(TestResult.TaskStatus.RUNNING, TestResult.TaskStatus.PENDING, TestResult.TaskStatus.STOPPING);

    @Inject
    TestRunRepository repository;

    @Inject
    TestOrchestrator orchestrator;

    @ConfigProperty(name = "kates.engine.max-duration-ms", defaultValue = "7200000")
    long maxDurationMs;

    /**
     * What a planned duration leaves out: creating the topic and starting the
     * clients before the tasks' clocks start, a producer's close waiting out
     * its in-flight sends, and the reconciler's tick that marks the run DONE.
     */
    @ConfigProperty(name = "kates.engine.reaper-grace-ms", defaultValue = "300000")
    long graceMs;

    @Scheduled(every = "60s", identity = "test-timeout-reaper")
    void reapStuckTests() {
        Instant now = Instant.now();
        for (TestResult.TaskStatus status : NOT_ENDED) {
            for (TestRun summary : repository.findByStatus(status)) {
                reapIfPastDeadline(summary, now);
            }
        }
    }

    private void reapIfPastDeadline(TestRun summary, Instant now) {
        if (summary.getCreatedAt() == null) return;
        try {
            if (!now.isAfter(deadlineOf(summary))) {
                return;
            }
            // findByStatus reads runs without their task results. Read the
            // run whole, so that each task that had not finished is failed
            // with the reason and every task is written back with what it
            // measured. Its status is the one read here: a PENDING run may
            // have been stored RUNNING since.
            TestRun run = repository.findById(summary.getId()).orElse(null);
            if (run == null || !NOT_ENDED.contains(run.getStatus())) {
                return;
            }
            TestResult.TaskStatus status = run.getStatus();
            String error = timeoutError(run);
            LOG.warnf("Test %s is past its deadline — marking as FAILED: %s", run.getId(), error);
            TestRun failed = withTasksFailed(run.withStatus(TestResult.TaskStatus.FAILED), error);

            // A PENDING run may be one whose submission is still under way in
            // this process, and that submission can store it RUNNING before
            // the write below. Settling it first would hand back the permit
            // of a run that then goes on, so it is settled once the write has
            // landed. Its submission's own write then finds it FAILED, and
            // stops the tasks it started.
            boolean pending = status == TestResult.TaskStatus.PENDING;
            if (!pending) {
                // Stop the live producer/consumer virtual threads BEFORE
                // persisting FAILED. Marking the DB row failed without this
                // left the backend workers running — a "timed out" run kept
                // producing to Kafka and skewing concurrent runs' latency.
                // Safe to do before the CAS below: a RUNNING or STOPPING run
                // can only end, and if it has ended already, its workers are
                // finished too and this is a no-op.
                orchestrator.settle(run.getId());
            }

            // Compare-and-set on the status. A run can complete between
            // the query above and this write, and an unconditional save
            // would overwrite that real completion with a bogus timeout.
            if (!repository.saveIfStatus(failed, status)) {
                LOG.infof("Run %s changed state while being reaped — leaving it alone", run.getId());
            } else if (pending) {
                orchestrator.settle(run.getId());
            }
        } catch (Exception e) {
            // Covers an unparseable createdAt AND an optimistic-lock
            // conflict — the latter means a real completion landed while we
            // were deciding this run had timed out, so leaving it alone is
            // exactly right. Either way the next sweep re-evaluates.
            LOG.debugf("Skipping run %s this sweep: %s", summary.getId(), e.getMessage());
        }
    }

    /**
     * The run with each task that had not finished failed with the error. A
     * run with no task gets one that carries it, as a run its submission never
     * stored RUNNING has none, and would otherwise say nowhere why it failed.
     */
    private static TestRun withTasksFailed(TestRun run, String error) {
        String now = Instant.now().toString();
        List<TestResult> newResults = new java.util.ArrayList<>();
        for (TestResult result : run.getResults()) {
            if (result.getStatus() != TestResult.TaskStatus.DONE
                    && result.getStatus() != TestResult.TaskStatus.FAILED) {
                result = result.withStatus(TestResult.TaskStatus.FAILED)
                        .withError(error)
                        .withEndTime(now);
            }
            newResults.add(result);
        }
        if (newResults.isEmpty()) {
            newResults.add(new TestResult()
                    .withTaskId(run.getId() + "-submission")
                    .withTestType(run.getTestType())
                    .withStatus(TestResult.TaskStatus.FAILED)
                    .withError(error)
                    .withEndTime(now));
        }
        return run.withResults(newResults);
    }

    /** The instant after which the reaper fails the run if it has not ended. */
    Instant deadlineOf(TestRun run) {
        return Instant.parse(run.getCreatedAt()).plusMillis(allowedMs(run));
    }

    /** How long after its creation the run may still be going. */
    private long allowedMs(TestRun run) {
        Long planned = run.getPlannedDurationMs();
        long bound = planned != null ? Math.min(planned, maxDurationMs) : maxDurationMs;
        return Math.max(0, bound) + graceMs;
    }

    /**
     * The error each unfinished task is failed with: the status the run was
     * still in, the deadline and what it is made of, and for a run that never
     * got going, why it can still be in that status.
     */
    private String timeoutError(TestRun run) {
        Long planned = run.getPlannedDurationMs();
        String bound;
        if (planned == null) {
            bound = "kates.engine.max-duration-ms (" + maxDurationMs + "ms) for a run no duration bounds,";
        } else if (planned > maxDurationMs) {
            bound = "kates.engine.max-duration-ms (" + maxDurationMs + "ms)";
        } else {
            bound = "its planned " + planned + "ms";
        }
        String error = "Timeout: still " + run.getStatus().name().toLowerCase(java.util.Locale.ROOT) + " "
                + allowedMs(run) + "ms after it was created, " + bound + " plus a " + graceMs + "ms grace";
        return switch (run.getStatus()) {
            case PENDING ->
                error + "; its tasks were never stored as started, as when the Kates API stops"
                        + " while it starts them";
            case STOPPING ->
                error + "; only a Kates API before 1.25.0 stores a run STOPPING, and nothing ended this one";
            default -> error;
        };
    }
}
