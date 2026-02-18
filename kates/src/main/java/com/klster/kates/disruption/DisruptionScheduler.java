package com.klster.kates.disruption;

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
    DisruptionReportRepository reportRepository;

    @Inject
    ObjectMapper objectMapper;

    @Scheduled(every = "60s", identity = "kates-disruption-schedule-evaluator")
    void evaluateSchedules() {
        List<DisruptionScheduleEntity> schedules = em.createQuery(
                        "SELECT s FROM DisruptionScheduleEntity s WHERE s.enabled = true",
                        DisruptionScheduleEntity.class)
                .getResultList();

        if (schedules.isEmpty()) return;

        ZonedDateTime now = ZonedDateTime.now(ZoneOffset.UTC);
        LOG.debugf("Evaluating %d disruption schedules at %s", schedules.size(), now);

        for (DisruptionScheduleEntity schedule : schedules) {
            try {
                if (matchesCron(schedule.getCronExpression(), now)) {
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

    static boolean matchesCron(String cronExpr, ZonedDateTime now) {
        String[] parts = cronExpr.trim().split("\\s+");
        if (parts.length < 5) {
            LOG.warn("Invalid cron expression (need 5 fields): " + cronExpr);
            return false;
        }
        return matchesField(parts[0], now.getMinute())
                && matchesField(parts[1], now.getHour())
                && matchesField(parts[2], now.getDayOfMonth())
                && matchesField(parts[3], now.getMonthValue())
                && matchesField(parts[4], now.getDayOfWeek().getValue() % 7);
    }

    private static boolean matchesField(String field, int value) {
        if ("*".equals(field)) return true;
        if (field.startsWith("*/")) {
            try {
                int step = Integer.parseInt(field.substring(2));
                return step > 0 && value % step == 0;
            } catch (NumberFormatException e) {
                return false;
            }
        }
        if (field.contains(",")) {
            for (String part : field.split(",")) {
                try {
                    if (Integer.parseInt(part.trim()) == value) return true;
                } catch (NumberFormatException e) {
                    /* skip */
                }
            }
            return false;
        }
        if (field.contains("-")) {
            String[] range = field.split("-");
            try {
                return value >= Integer.parseInt(range[0].trim()) && value <= Integer.parseInt(range[1].trim());
            } catch (Exception e) {
                return false;
            }
        }
        try {
            return Integer.parseInt(field) == value;
        } catch (NumberFormatException e) {
            return false;
        }
    }
}
