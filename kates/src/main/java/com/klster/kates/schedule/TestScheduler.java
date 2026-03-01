package com.klster.kates.schedule;

import java.time.ZoneOffset;
import java.time.ZonedDateTime;
import java.util.List;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import com.fasterxml.jackson.databind.ObjectMapper;
import io.quarkus.scheduler.Scheduled;
import org.jboss.logging.Logger;

import com.klster.kates.domain.CreateTestRequest;
import com.klster.kates.engine.TestOrchestrator;

/**
 * Evaluates all enabled schedules every 60 seconds.
 * When a cron expression matches the current minute, the associated test is executed.
 */
@ApplicationScoped
public class TestScheduler {

    private static final Logger LOG = Logger.getLogger(TestScheduler.class);
    private static final ObjectMapper JSON = new ObjectMapper();

    @Inject
    ScheduledTestRunRepository repository;

    @Inject
    TestOrchestrator orchestrator;

    @Scheduled(every = "60s", identity = "kates-schedule-evaluator")
    void evaluateSchedules() {
        List<ScheduledTestRun> schedules = repository.findAllEnabled();
        if (schedules.isEmpty()) {
            return;
        }

        ZonedDateTime now = ZonedDateTime.now(ZoneOffset.UTC);
        LOG.debugf("Evaluating %d schedules at %s", schedules.size(), now);

        for (ScheduledTestRun schedule : schedules) {
            try {
                if (matchesCron(schedule.getCronExpression(), now)) {
                    LOG.info("Schedule '" + schedule.getName() + "' triggered at " + now);
                    executeSchedule(schedule);
                }
            } catch (Exception e) {
                LOG.warn("Failed to evaluate schedule '" + schedule.getName() + "'", e);
            }
        }
    }

    private void executeSchedule(ScheduledTestRun schedule) {
        try {
            CreateTestRequest request = JSON.readValue(schedule.getRequestJson(), CreateTestRequest.class);
            var result = orchestrator.executeTest(request);
            if (result.isSuccess()) {
                var run = result.asSuccess().orElseThrow();
                repository.updateLastRun(schedule.getId(), run.getId());
                LOG.info("Schedule '" + schedule.getName() + "' started run " + run.getId());
            } else {
                LOG.error("Failed to execute schedule '" + schedule.getName() + "': " + result.asFailure().orElseThrow().getMessage());
            }
        } catch (Exception e) {
            LOG.error("Failed to execute schedule '" + schedule.getName() + "'", e);
        }
    }

    /**
     * Evaluates a simplified cron expression (minute hour dayOfMonth month dayOfWeek)
     * against the current time. Supports '*' (any) and fixed values.
     */
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

        // Handle step values like */5
        if (field.startsWith("*/")) {
            try {
                int step = Integer.parseInt(field.substring(2));
                return step > 0 && value % step == 0;
            } catch (NumberFormatException e) {
                return false;
            }
        }

        // Handle comma-separated values like 0,15,30,45
        if (field.contains(",")) {
            for (String part : field.split(",")) {
                try {
                    if (Integer.parseInt(part.trim()) == value) return true;
                } catch (NumberFormatException e) {
                    // skip
                }
            }
            return false;
        }

        // Handle range like 9-17
        if (field.contains("-")) {
            String[] range = field.split("-");
            try {
                int low = Integer.parseInt(range[0].trim());
                int high = Integer.parseInt(range[1].trim());
                return value >= low && value <= high;
            } catch (Exception e) {
                return false;
            }
        }

        // Fixed value
        try {
            return Integer.parseInt(field) == value;
        } catch (NumberFormatException e) {
            return false;
        }
    }
}
