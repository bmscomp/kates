package com.bmscomp.kates.schedule;

import java.time.ZoneOffset;
import java.time.ZonedDateTime;
import java.util.Comparator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.validation.Validator;

import com.fasterxml.jackson.databind.ObjectMapper;
import io.quarkus.scheduler.Scheduled;
import org.jboss.logging.Logger;

import com.bmscomp.kates.domain.CreateTestRequest;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestSpec;
import com.bmscomp.kates.engine.InvalidTestSpecException;
import com.bmscomp.kates.engine.TestOrchestrator;
import com.bmscomp.kates.util.Result;

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

    @Inject
    com.bmscomp.kates.service.SchedulerLeaseService leases;

    @Inject
    Validator validator;

    @Scheduled(every = "60s", identity = "kates-schedule-evaluator")
    void evaluateSchedules() {
        // With replicas > 1 every instance fires this — only the lease holder
        // may trigger runs, or each schedule would execute once per replica.
        if (!leases.tryAcquire("test-scheduler", java.time.Duration.ofSeconds(55))) {
            return;
        }
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

    /**
     * Starts a run of the schedule's request, or logs why the Kates API
     * refuses it and starts none. Package-private: a test fires a schedule
     * without waiting for its minute.
     *
     * <p>The request's spec is held first to the limits TestSpec sets, as POST
     * /api/tests holds it by bean validation, which executeTest doesn't run.
     * A stored spec can break them, if a PUT saved it without bean validation
     * or it was saved before TestSpec had limits. Its run went ahead anyway: a
     * numProducers of 1000 started 1000 Trogdor tasks at each firing. The spec
     * is validated on its own: validating the whole request would also require
     * the request's own type, which a scenario with a type of its own goes
     * without. A schedule saved before the Kates API kept only the fields a
     * request sets holds the old Java defaults, which are all within the
     * limits, so it still fires.
     */
    void executeSchedule(ScheduledTestRun schedule) {
        try {
            CreateTestRequest request = JSON.readValue(schedule.getRequestJson(), CreateTestRequest.class);
            Map<String, String> outsideLimits = outsideLimits(request.getSpec());
            Result<TestRun, Exception> result = outsideLimits.isEmpty()
                    ? orchestrator.executeTest(request)
                    : Result.failure(new InvalidTestSpecException(outsideLimits));
            if (result.isSuccess()) {
                var run = result.asSuccess().orElseThrow();
                repository.updateLastRun(schedule.getId(), run.getId());
                LOG.info("Schedule '" + schedule.getName() + "' started run " + run.getId());
            } else {
                LOG.error("Failed to execute schedule '" + schedule.getName() + "': "
                        + result.asFailure().orElseThrow().getMessage());
            }
        } catch (Exception e) {
            LOG.error("Failed to execute schedule '" + schedule.getName() + "'", e);
        }
    }

    /**
     * Each value of the spec outside its limits, keyed by field name, with the
     * reason; empty when all are within them, or there is no spec. Sorted, so
     * that the log names them in the same order each time.
     */
    private Map<String, String> outsideLimits(TestSpec spec) {
        Map<String, String> found = new LinkedHashMap<>();
        if (spec == null) {
            return found;
        }
        validator.validate(spec).stream()
                .sorted(Comparator.comparing(v -> v.getPropertyPath() + ": " + v.getMessage()))
                .forEach(v -> found.putIfAbsent(v.getPropertyPath().toString(), v.getMessage()));
        return found;
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
