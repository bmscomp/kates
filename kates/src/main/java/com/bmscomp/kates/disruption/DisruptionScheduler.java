package com.bmscomp.kates.disruption;

import java.time.Instant;
import java.util.List;
import java.util.UUID;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;
import jakarta.transaction.Transactional;

import com.fasterxml.jackson.databind.ObjectMapper;
import io.quarkus.scheduler.Scheduled;
import org.jboss.logging.Logger;

/**
 * Cron-based scheduler for recurring disruption tests.
 * Evaluates enabled schedules every 60 seconds, executing matching plans
 * via DisruptionOrchestrator or from the playbook catalog.
 */
@ApplicationScoped
public class DisruptionScheduler {

    private static final Logger LOG = Logger.getLogger(DisruptionScheduler.class);

    @Inject
    EntityManager em;

    @Inject
    DisruptionOrchestrator orchestrator;

    @Inject
    DisruptionPlaybookCatalog playbookCatalog;

    @Inject
    DisruptionConcurrencyGuard concurrencyGuard;

    @Inject
    com.bmscomp.kates.service.AuditService auditService;

    @Inject
    DisruptionReportRepository reportRepository;

    @Inject
    ObjectMapper objectMapper;

    @jakarta.inject.Inject
    com.bmscomp.kates.schedule.ScheduleFirings firings;

    @jakarta.inject.Inject
    com.bmscomp.kates.quorum.Quorum quorum;

    @Scheduled(every = "10s", identity = "kates-disruption-schedule-evaluator")
    void evaluateSchedules() {
        evaluate(Instant.now());
    }

    /**
     * One tick at {@code now}; package-private, so a test ticks at the time it
     * needs. Each due time runs once, claimed as TestScheduler claims a test's.
     */
    void evaluate(Instant now) {
        // A draining instance fires nothing; nor does a member that does not lead its quorum.
        if (concurrencyGuard.isDraining() || !quorum.acceptsWrites()) {
            return;
        }
        List<DisruptionScheduleEntity> schedules = em.createQuery(
                        "SELECT s FROM DisruptionScheduleEntity s WHERE s.enabled = true",
                        DisruptionScheduleEntity.class)
                .getResultList();
        LOG.debugf("Evaluating %d disruption schedules at %s", schedules.size(), now);

        for (DisruptionScheduleEntity schedule : schedules) {
            try {
                Instant since = schedule.getLastDue() != null ? schedule.getLastDue() : schedule.getCreatedAt();
                com.bmscomp.kates.schedule.DueTimes.toFire(schedule.getCronExpression(), since, now)
                        .filter(due -> firings.claim(
                                com.bmscomp.kates.schedule.ScheduleFirings.Kind.DISRUPTIONS, schedule.getId(), due))
                        .ifPresent(due -> {
                            LOG.info("Disruption schedule '" + schedule.getName() + "' triggered for " + due);
                            executeSchedule(schedule);
                        });
            } catch (Exception e) {
                LOG.warn("Failed to evaluate disruption schedule '" + schedule.getName() + "'", e);
            }
        }
    }

    private void executeSchedule(DisruptionScheduleEntity schedule) {
        // A draining instance injects nothing, and saves no report of a firing it skipped.
        if (!concurrencyGuard.enter()) {
            return;
        }
        try {
            DisruptionPlan plan;

            if (schedule.getPlaybookName() != null
                    && !schedule.getPlaybookName().isBlank()) {
                plan = playbookCatalog
                        .findByName(schedule.getPlaybookName())
                        .map(playbookCatalog::toPlan)
                        .orElseThrow(
                                () -> new IllegalStateException("Playbook not found: " + schedule.getPlaybookName()));
            } else if (schedule.getPlanJson() != null) {
                plan = objectMapper.readValue(schedule.getPlanJson(), DisruptionPlan.class);
            } else {
                LOG.warn("Schedule '" + schedule.getName() + "' has no playbook or plan");
                return;
            }

            String runId = UUID.randomUUID().toString().substring(0, 8);
            DisruptionReport report = orchestrator.execute(runId, plan);

            String grade =
                    report.getSlaVerdict() != null ? report.getSlaVerdict().grade() : null;
            DisruptionReportEntity entity = new DisruptionReportEntity(
                    runId,
                    report.getPlanName(),
                    report.getStatus(),
                    grade,
                    objectMapper.writeValueAsString(report),
                    null);
            reportRepository.save(entity);
            auditService.record(
                    "RUN",
                    "disruption",
                    runId,
                    "schedule '" + schedule.getName() + "'",
                    com.bmscomp.kates.audit.Actor.SCHEDULER);

            updateLastRun(schedule.getId(), runId);
            LOG.info("Disruption schedule '" + schedule.getName() + "' completed: " + runId);
        } catch (Exception e) {
            LOG.error("Failed to execute disruption schedule '" + schedule.getName() + "'", e);
        } finally {
            concurrencyGuard.exit();
        }
    }

    @Transactional
    void updateLastRun(String scheduleId, String runId) {
        DisruptionScheduleEntity entity = em.find(DisruptionScheduleEntity.class, scheduleId);
        if (entity != null) {
            entity.setLastRunId(runId);
            entity.setLastRunAt(Instant.now());
            em.merge(entity);
        }
    }
}
