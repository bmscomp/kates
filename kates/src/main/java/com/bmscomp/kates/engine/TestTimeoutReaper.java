package com.bmscomp.kates.engine;

import java.time.Duration;
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
 * Periodically checks for stuck tests that have exceeded the maximum allowed
 * duration and marks them as FAILED with a timeout error.
 */
@ApplicationScoped
public class TestTimeoutReaper {

    private static final Logger LOG = Logger.getLogger(TestTimeoutReaper.class);

    @Inject
    TestRunRepository repository;

    @Inject
    TestOrchestrator orchestrator;

    @ConfigProperty(name = "kates.engine.max-duration-ms", defaultValue = "1800000")
    long maxDurationMs;

    @Scheduled(every = "60s", identity = "test-timeout-reaper")
    void reapStuckTests() {
        List<TestRun> running = repository.findByStatus(TestResult.TaskStatus.RUNNING);
        if (running.isEmpty()) {
            return;
        }

        Instant cutoff = Instant.now().minus(Duration.ofMillis(maxDurationMs));

        for (TestRun run : running) {
            if (run.getCreatedAt() == null) continue;
            try {
                Instant created = Instant.parse(run.getCreatedAt());
                if (created.isBefore(cutoff)) {
                    LOG.warnf("Test %s exceeded max duration (%dms) — marking as FAILED", run.getId(), maxDurationMs);
                    run = run.withStatus(TestResult.TaskStatus.FAILED);
                    List<TestResult> newResults = new java.util.ArrayList<>();
                    for (TestResult result : run.getResults()) {
                        if (result.getStatus() == TestResult.TaskStatus.RUNNING
                                || result.getStatus() == TestResult.TaskStatus.PENDING) {
                            result = result.withStatus(TestResult.TaskStatus.FAILED)
                                    .withError("Timeout: exceeded max duration of " + maxDurationMs + "ms")
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
                }
            } catch (Exception e) {
                // Covers an unparseable createdAt AND an optimistic-lock
                // conflict — the latter means a real completion landed while we
                // were deciding this run had timed out, so leaving it alone is
                // exactly right. Either way the next sweep re-evaluates.
                LOG.debugf("Skipping run %s this sweep: %s", run.getId(), e.getMessage());
            }
        }
    }
}
