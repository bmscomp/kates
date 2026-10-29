package com.bmscomp.kates.disruption;

import java.time.Instant;
import java.time.ZoneOffset;
import java.time.ZonedDateTime;
import java.util.List;
import java.util.UUID;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;
import jakarta.transaction.Transactional;

import com.fasterxml.jackson.databind.ObjectMapper;
import io.quarkus.scheduler.Scheduled;
import org.jboss.logging.Logger;

import com.bmscomp.kates.schedule.TestScheduler;

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
    com.bmscomp.kates.service.AuditService auditService;

    @Inject
    DisruptionReportRepository reportRepository;

    @Inject
    ObjectMapper objectMapper;

    @jakarta.inject.Inject
    com.bmscomp.kates.service.SchedulerLeaseService leases;

    @Scheduled(every = "60s", identity = "kates-disruption-schedule-evaluator")
    void evaluateSchedules() {
        // Only the lease holder may trigger disruptions — with replicas > 1
        // each schedule would otherwise inject faults once per replica.
        if (!leases.tryAcquire("disruption-scheduler", java.time.Duration.ofSeconds(55))) {
            return;
        }
        List<DisruptionScheduleEntity> schedules = em.createQuery(
                        "SELECT s FROM DisruptionScheduleEntity s WHERE s.enabled = true",
                        DisruptionScheduleEntity.class)
                .getResultList();

        if (schedules.isEmpty()) return;

        ZonedDateTime now = ZonedDateTime.now(ZoneOffset.UTC);
        LOG.debugf("Evaluating %d disruption schedules at %s", schedules.size(), now);

        for (DisruptionScheduleEntity schedule : schedules) {
            try {
                if (TestScheduler.matchesCron(schedule.getCronExpression(), now)) {
                    LOG.info("Disruption schedule '" + schedule.getName() + "' triggered at " + now);
                    executeSchedule(schedule);
                }
            } catch (Exception e) {
                LOG.warn("Failed to evaluate disruption schedule '" + schedule.getName() + "'", e);
            }
        }
    }

    private void executeSchedule(DisruptionScheduleEntity schedule) {
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

            DisruptionReport report = orchestrator.execute(plan);
            String runId = UUID.randomUUID().toString().substring(0, 8);

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
