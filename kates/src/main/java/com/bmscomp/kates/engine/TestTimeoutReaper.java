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
 * Periodically fails runs that are still RUNNING past their deadline: the
 * run's planned duration, counted from its creation, plus a grace.
 *
 * <p>The orchestrator stores the planned duration with the run when it
 * creates it (TestOrchestrator.plannedDurationMs): the run's durationMs, twice
 * that for INTEGRITY, the sum of the phases for a scenario.
 * {@code kates.engine.max-duration-ms} caps it, and stands in for it when a
 * run has none. Every run used to get that cap alone, 30 minutes, whatever it
 * was set to last, so a default ENDURANCE run, an hour long, always ended
 * FAILED.
 */
@ApplicationScoped
public class TestTimeoutReaper {

    private static final Logger LOG = Logger.getLogger(TestTimeoutReaper.class);

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
        List<TestRun> running = repository.findByStatus(TestResult.TaskStatus.RUNNING);
        if (running.isEmpty()) {
            return;
        }

        Instant now = Instant.now();
        for (TestRun summary : running) {
            if (summary.getCreatedAt() == null) continue;
            try {
                if (!now.isAfter(deadlineOf(summary))) {
                    continue;
                }
                // findByStatus reads runs without their task results. Read the
                // run whole, so that each task that had not finished is failed
                // with the reason and every task is written back with what it
                // measured.
                TestRun run = repository.findById(summary.getId()).orElse(null);
                if (run == null) {
                    continue;
                }
                String error = timeoutError(run);
                LOG.warnf("Test %s is past its deadline — marking as FAILED: %s", run.getId(), error);
                run = run.withStatus(TestResult.TaskStatus.FAILED);
                List<TestResult> newResults = new java.util.ArrayList<>();
                for (TestResult result : run.getResults()) {
                    if (result.getStatus() == TestResult.TaskStatus.RUNNING
                            || result.getStatus() == TestResult.TaskStatus.PENDING) {
                        result = result.withStatus(TestResult.TaskStatus.FAILED)
                                .withError(error)
                                .withEndTime(Instant.now().toString());
                    }
                    newResults.add(result);
                }
                run = run.withResults(newResults);
                // Stop the live producer/consumer virtual threads BEFORE
                // persisting FAILED. Marking the DB row failed without this
                // left the backend workers running — a "timed out" run kept
                // producing to Kafka and skewing concurrent runs' latency.
                // Safe to do before the CAS below: if the run turns out to
                // have finished already, its workers are finished too and
                // this is a no-op.
                orchestrator.abortWorkers(run);

                // Compare-and-set on the status. A run can complete between
                // the query above and this write, and an unconditional save
                // would overwrite that real completion with a bogus timeout.
                if (!repository.saveIfStatus(run, TestResult.TaskStatus.RUNNING)) {
                    LOG.infof("Run %s changed state while being reaped — leaving it alone", run.getId());
                }
            } catch (Exception e) {
                // Covers an unparseable createdAt AND an optimistic-lock
                // conflict — the latter means a real completion landed while we
                // were deciding this run had timed out, so leaving it alone is
                // exactly right. Either way the next sweep re-evaluates.
                LOG.debugf("Skipping run %s this sweep: %s", summary.getId(), e.getMessage());
            }
        }
    }

    /** The instant after which the reaper fails the run if it is still RUNNING. */
    Instant deadlineOf(TestRun run) {
        return Instant.parse(run.getCreatedAt()).plusMillis(allowedMs(run));
    }

    /** How long after its creation the run may still be RUNNING. */
    private long allowedMs(TestRun run) {
        Long planned = run.getPlannedDurationMs();
        long bound = planned != null ? Math.min(planned, maxDurationMs) : maxDurationMs;
        return Math.max(0, bound) + graceMs;
    }

    /** The error each unfinished task is failed with: the deadline, and what it is made of. */
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
        return "Timeout: still running " + allowedMs(run) + "ms after it was created, " + bound + " plus a " + graceMs
                + "ms grace";
    }
}
