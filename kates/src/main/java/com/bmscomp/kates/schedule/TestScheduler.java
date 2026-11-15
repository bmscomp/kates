package com.bmscomp.kates.schedule;

import java.time.Instant;
import java.util.List;
import java.util.Map;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import com.cronutils.model.CronType;
import com.cronutils.model.definition.CronDefinitionBuilder;
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
 * Evaluates all enabled schedules every 10 seconds, and starts the test of
 * each due time once: DueTimes picks it, up to 5 minutes late, and
 * ScheduleFirings claims it in the database, so a second member that believes
 * it leads, or the next tick, finds it taken.
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
    com.bmscomp.kates.disruption.DisruptionConcurrencyGuard drain;

    @Inject
    com.bmscomp.kates.quorum.Quorum quorum;

    @Inject
    ScheduleFirings firings;

    @Inject
    SpecLimits specLimits;

    @Inject
    com.bmscomp.kates.service.AuditService auditService;

    @Scheduled(every = "10s", identity = "kates-schedule-evaluator")
    void evaluateSchedules() {
        evaluate(Instant.now());
    }

    /** One tick at {@code now}; package-private, so a test ticks at the time it needs. */
    void evaluate(Instant now) {
        // A draining instance fires nothing. The guard's flag, set with the
        // orchestrator's by PUT /api/drain: asking the orchestrator would create
        // it on the first tick, and its startup recovery with it.
        // A member that does not lead fires nothing either.
        if (drain.isDraining() || !quorum.acceptsWrites()) {
            return;
        }
        List<ScheduledTestRun> schedules = repository.findAllEnabled();
        LOG.debugf("Evaluating %d schedules at %s", schedules.size(), now);

        for (ScheduledTestRun schedule : schedules) {
            try {
                Instant since = schedule.getLastDue() != null ? schedule.getLastDue() : schedule.getCreatedAt();
                DueTimes.toFire(schedule.getCronExpression(), since, now)
                        .filter(due -> firings.claim(ScheduleFirings.Kind.TESTS, schedule.getId(), due))
                        .ifPresent(due -> {
                            LOG.info("Schedule '" + schedule.getName() + "' triggered for " + due);
                            executeSchedule(schedule, due);
                        });
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
     *
     * <p>The run carries the Idempotency-Key {@code schedule:<id>:<due>}, so it
     * names the schedule and due time that started it.
     */
    void executeSchedule(ScheduledTestRun schedule, Instant due) {
        try {
            CreateTestRequest request = JSON.readValue(schedule.getRequestJson(), CreateTestRequest.class);
            Map<String, String> outsideLimits = specLimits.violations(request.getSpec());
            Result<TestRun, Exception> result = outsideLimits.isEmpty()
                    ? orchestrator.executeTest(request, "schedule:" + schedule.getId() + ":" + due)
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
}
