package com.klster.kates.engine;

import java.time.Duration;
import java.time.Instant;
import java.util.List;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import io.quarkus.scheduler.Scheduled;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

import com.klster.kates.domain.TestResult;
import com.klster.kates.domain.TestRun;
import com.klster.kates.service.TestRunRepository;

/**
 * Periodically checks for stuck tests that have exceeded the maximum allowed
 * duration and marks them as FAILED with a timeout error.
 */
@ApplicationScoped
public class TestTimeoutReaper {

    private static final Logger LOG = Logger.getLogger(TestTimeoutReaper.class);

    @Inject
    TestRunRepository repository;

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
                    run.setStatus(TestResult.TaskStatus.FAILED);
                    for (TestResult result : run.getResults()) {
                        if (result.getStatus() == TestResult.TaskStatus.RUNNING
                                || result.getStatus() == TestResult.TaskStatus.PENDING) {
                            result.setStatus(TestResult.TaskStatus.FAILED);
                            result.setError("Timeout: exceeded max duration of " + maxDurationMs + "ms");
                            result.setEndTime(Instant.now().toString());
                        }
                    }
                    repository.save(run);
                }
            } catch (Exception e) {
                LOG.debugf("Could not parse createdAt for run %s: %s", run.getId(), e.getMessage());
            }
        }
    }
}
