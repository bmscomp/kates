package com.bmscomp.kates.schedule;

import java.time.ZoneOffset;
import java.time.ZonedDateTime;
import java.time.temporal.ChronoUnit;
import java.util.List;
import java.util.Map;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import com.cronutils.model.CronType;
import com.cronutils.model.definition.CronDefinitionBuilder;
import com.cronutils.model.time.ExecutionTime;
import com.cronutils.parser.CronParser;
import com.fasterxml.jackson.databind.ObjectMapper;
import io.quarkus.scheduler.Scheduled;
import org.jboss.logging.Logger;

import com.bmscomp.kates.domain.CreateTestRequest;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.engine.InvalidTestSpecException;
import com.bmscomp.kates.engine.SpecLimits;
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
    SpecLimits specLimits;

    @Inject
    com.bmscomp.kates.service.AuditService auditService;

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
     * <p>The request's own spec is held first to the limits TestSpec sets
     * (SpecLimits), as POST /api/tests holds it by bean validation, which
     * executeTest doesn't run; refusal(), which executeTest asks, holds a
     * scenario's specs to them. A stored spec can break them, if a PUT saved
     * it without bean validation or it was saved before TestSpec had limits.
     * Its run went ahead anyway: a numProducers of 1000 started 1000 Trogdor
     * tasks at each firing. The spec is validated on its own: validating the
     * whole request would also require the request's own type, which a
     * scenario with a type of its own goes without. A schedule saved before
     * the Kates API kept only the fields a request sets holds the old Java
     * defaults, which are all within the limits, so it still fires.
     */
    void executeSchedule(ScheduledTestRun schedule) {
        try {
            CreateTestRequest request = JSON.readValue(schedule.getRequestJson(), CreateTestRequest.class);
            Map<String, String> outsideLimits = specLimits.violations(request.getSpec());
            Result<TestRun, Exception> result = outsideLimits.isEmpty()
                    ? orchestrator.executeTest(request)
                    : Result.failure(new InvalidTestSpecException(outsideLimits));
            if (result.isSuccess()) {
                var run = result.asSuccess().orElseThrow();
                repository.updateLastRun(schedule.getId(), run.getId());
                auditService.record(
                        "CREATE",
                        "test",
                        run.getId(),
                        "schedule '" + schedule.getName() + "'",
                        com.bmscomp.kates.audit.Actor.SCHEDULER);
                LOG.info("Schedule '" + schedule.getName() + "' started run " + run.getId());
            } else {
                LOG.error("Failed to execute schedule '" + schedule.getName() + "': "
                        + result.asFailure().orElseThrow().getMessage());
            }
        } catch (Exception e) {
            LOG.error("Failed to execute schedule '" + schedule.getName() + "'", e);
        }
    }

    /** Standard 5-field UNIX cron; schedules are validated with it on POST and PUT. */
    public static final CronParser CRON = new CronParser(CronDefinitionBuilder.instanceDefinitionFor(CronType.UNIX));

    /** Whether the expression fires in the minute of {@code now}; throws when it doesn't parse. */
    public static boolean matchesCron(String cronExpr, ZonedDateTime now) {
        return ExecutionTime.forCron(CRON.parse(cronExpr)).isMatch(now.truncatedTo(ChronoUnit.MINUTES));
    }
}
