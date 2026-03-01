package com.klster.kates.service;

import java.time.Duration;
import java.time.Instant;
import java.util.List;

import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.transaction.Transactional;

import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

import io.quarkus.scheduler.Scheduled;

import com.klster.kates.domain.TestResult;
import com.klster.kates.domain.TestRun;

/**
 * Scheduled job that archives (deletes) test runs older than the configured retention period.
 */
@ApplicationScoped
public class TestCleanupScheduler {

    private static final Logger LOG = Logger.getLogger(TestCleanupScheduler.class);

    @Inject
    TestRunRepository repository;

    @ConfigProperty(name = "kates.cleanup.retention-days", defaultValue = "90")
    int retentionDays;

    @Scheduled(every = "24h", identity = "test-data-cleanup")
    @Transactional
    void cleanupOldTests() {
        Instant cutoff = Instant.now().minus(Duration.ofDays(retentionDays));
        List<TestRun> all = repository.findAll();
        int deleted = 0;

        for (TestRun run : all) {
            if (run.getStatus() != TestResult.TaskStatus.RUNNING
                    && run.getStatus() != TestResult.TaskStatus.PENDING
                    && run.getCreatedAt() != null) {
                try {
                    Instant created = Instant.parse(run.getCreatedAt());
                    if (created.isBefore(cutoff)) {
                        repository.delete(run.getId());
                        deleted++;
                    }
                } catch (Exception ignored) {}
            }
        }

        if (deleted > 0) {
            LOG.infof("Cleaned up %d test runs older than %d days", deleted, retentionDays);
        }
    }
}
