package com.bmscomp.kates.disruption;

import static io.restassured.RestAssured.given;
import static org.hamcrest.Matchers.equalTo;
import static org.hamcrest.Matchers.hasItem;
import static org.hamcrest.Matchers.hasSize;
import static org.hamcrest.Matchers.is;
import static org.hamcrest.Matchers.startsWith;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.junit.jupiter.api.Assertions.fail;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.when;

import java.time.Duration;
import java.time.Instant;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import jakarta.inject.Inject;

import com.fasterxml.jackson.databind.ObjectMapper;
import io.quarkus.test.InjectMock;
import io.quarkus.test.junit.QuarkusTest;
import io.restassured.http.ContentType;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.chaos.DisruptionType;
import com.bmscomp.kates.chaos.FaultSpec;

/**
 * How each way of running a disruption stores its report, read back the way
 * the CLI reads it.
 *
 * <p>A launched plan is saved twice under one id: a RUNNING placeholder when
 * it starts and its outcome when it ends. The repository used to persist both,
 * and the id is assigned rather than generated, so the second save was an
 * insert that failed on the primary key: the report stayed RUNNING with no
 * steps, and {@code kates disruption run} polled until it timed out. The
 * startup reconciler that marks abandoned RUNNING reports INTERRUPTED never
 * managed to either.
 *
 * <p>The cluster is out of the picture: the orchestrator and the safety guard
 * are mocks, so a plan "runs" in however long the mock takes.
 */
@QuarkusTest
class DisruptionReportPersistenceTest {

    private static final Duration WAIT = Duration.ofSeconds(10);

    @InjectMock
    DisruptionOrchestrator orchestrator;

    @InjectMock
    DisruptionSafetyGuard safetyGuard;

    @Inject
    DisruptionLauncher launcher;

    @Inject
    DisruptionReportRepository repository;

    @Inject
    DisruptionOrphanReconciler reconciler;

    @Inject
    DisruptionScheduler scheduler;

    @Inject
    ObjectMapper objectMapper;

    @BeforeEach
    void everyPlanIsSafe() {
        when(safetyGuard.validatePlan(any()))
                .thenReturn(DisruptionSafetyGuard.ValidationResult.ok(List.of("a validation warning")));
    }

    // ---------------------------------------------------------------- launcher

    @Test
    void aLaunchedPlanReplacesItsRunningPlaceholderWithItsOutcome() throws Exception {
        String planName = unique("launched");
        CountDownLatch finish = new CountDownLatch(1);
        when(orchestrator.execute(any())).thenAnswer(inv -> {
            assertTrue(finish.await(WAIT.toSeconds(), TimeUnit.SECONDS), "the test never let the plan finish");
            return completed(planName);
        });

        DisruptionLauncher.LaunchResult result = launcher.launch(plan(planName));
        assertEquals(DisruptionLauncher.Status.ACCEPTED, result.status());
        String id = result.id();

        // While the plan runs, the placeholder answers.
        given().when()
                .get("/api/disruptions/" + id)
                .then()
                .statusCode(200)
                .body("status", equalTo("RUNNING"))
                .body("stepReports", hasSize(0));
        String startedAt = listed(planName).get("createdAt");

        finish.countDown();

        assertEquals("COMPLETED", awaitFinalStatus(id));
        given().when()
                .get("/api/disruptions/" + id)
                .then()
                .statusCode(200)
                .body("planName", equalTo(planName))
                .body("stepReports", hasSize(1))
                .body("stepReports[0].stepName", equalTo("kill-leader"))
                .body("summary.passedSteps", is(1))
                .body("slaVerdict.grade", equalTo("A"));

        // One row, now saying what happened, still dated when the plan started.
        given().when()
                .get("/api/disruptions?planName=" + planName)
                .then()
                .statusCode(200)
                .body("count", is(1));
        Map<String, String> row = listed(planName);
        assertEquals(id, row.get("id"));
        assertEquals("COMPLETED", row.get("status"));
        assertEquals("A", row.get("slaGrade"));
        assertEquals(startedAt, row.get("createdAt"));
    }

    @Test
    void aPlanThatThrowsIsStoredAsFailed() {
        String planName = unique("throws");
        when(orchestrator.execute(any())).thenThrow(new IllegalStateException("provider exploded"));

        String id = launcher.launch(plan(planName)).id();

        assertEquals("FAILED", awaitFinalStatus(id));
        given().when()
                .get("/api/disruptions/" + id)
                .then()
                .statusCode(200)
                .body("validationWarnings", hasItem("Execution error: provider exploded"));
        assertEquals("FAILED", listed(planName).get("status"));
    }

    // -------------------------------------------------------------- reconciler

    @Test
    void theReconcilerMarksAReportLeftRunningByAnEarlierProcessInterrupted() throws Exception {
        String orphanPlan = unique("orphan");
        String orphan = seedRunning(orphanPlan);
        Instant processStart = Instant.now();
        // A plan this process launched after it started is not an orphan, even
        // if it is still running when the reconciler gets to the reports.
        String livePlan = unique("live");
        String live = seedRunning(livePlan);

        // On its own thread, as at startup: no transaction, no request context.
        AtomicInteger marked = new AtomicInteger(-1);
        Thread thread = Thread.ofVirtual().start(() -> marked.set(reconciler.markInterruptedReports(processStart)));
        thread.join(WAIT.toMillis());

        assertTrue(marked.get() >= 1, "marked " + marked.get());
        given().when()
                .get("/api/disruptions/" + orphan)
                .then()
                .statusCode(200)
                .body("status", equalTo("INTERRUPTED"))
                .body("planName", equalTo(orphanPlan))
                .body("validationWarnings", hasItem("a validation warning"))
                .body("validationWarnings", hasItem(startsWith("Interrupted: ")));
        assertEquals("INTERRUPTED", listed(orphanPlan).get("status"));

        given().when().get("/api/disruptions/" + live).then().body("status", equalTo("RUNNING"));
        assertEquals("RUNNING", listed(livePlan).get("status"));
    }

    @Test
    void markingAReportThatFinishedMeanwhileLeavesItsOutcome() throws Exception {
        String planName = unique("finished");
        String id = seedRunning(planName);
        // The reconciler read the row while it said RUNNING; the plan finished
        // before the reconciler wrote.
        DisruptionReportEntity stale = repository.findByStatusCreatedBefore("RUNNING", Instant.now()).stream()
                .filter(e -> e.getId().equals(id))
                .findFirst()
                .orElseThrow();
        DisruptionPersistence.persistReport(id, completed(planName), repository, objectMapper);

        DisruptionReport interrupted = DisruptionPersistence.readReport(stale, objectMapper);
        interrupted.setStatus("INTERRUPTED");
        assertFalse(repository.saveIfStatus(DisruptionPersistence.toEntity(id, interrupted, objectMapper), "RUNNING"));

        given().when().get("/api/disruptions/" + id).then().body("status", equalTo("COMPLETED"));
        assertEquals("COMPLETED", listed(planName).get("status"));
    }

    // ------------------------------------------------ the paths that insert once

    @Test
    void aTemplateRunStoresItsReportOnce() {
        String planName = unique("template");
        when(orchestrator.execute(any())).thenReturn(completed(planName));

        String id = given().contentType(ContentType.JSON)
                .body(Map.of())
                .when()
                .post("/api/disruptions/templates/broker-kill-recovery")
                .then()
                .statusCode(200)
                .extract()
                .path("id");

        given().when()
                .get("/api/disruptions/" + id)
                .then()
                .statusCode(200)
                .body("status", equalTo("COMPLETED"))
                .body("stepReports", hasSize(1));
        assertEquals(id, listed(planName).get("id"));
    }

    @Test
    void aScheduledRunStoresItsReportOnce() {
        String planName = unique("scheduled");
        when(orchestrator.execute(any())).thenReturn(completed(planName));
        String scheduleName = unique("every-minute");
        String scheduleId = given().contentType(ContentType.JSON)
                .body(Map.of("name", scheduleName, "cronExpression", "* * * * *", "playbookName", "az-failure"))
                .when()
                .post("/api/disruptions/schedules")
                .then()
                .statusCode(201)
                .extract()
                .path("id");
        try {
            scheduler.evaluateSchedules();

            String runId = given().when()
                    .get("/api/disruptions/schedules")
                    .then()
                    .statusCode(200)
                    .extract()
                    .path("items.find { it.name == '" + scheduleName + "' }.lastRunId");
            given().when()
                    .get("/api/disruptions/" + runId)
                    .then()
                    .statusCode(200)
                    .body("status", equalTo("COMPLETED"))
                    .body("planName", equalTo(planName));
        } finally {
            given().when()
                    .delete("/api/disruptions/schedules/" + scheduleId)
                    .then()
                    .statusCode(204);
        }
    }

    // ----------------------------------------------------------------- fixture

    private static String unique(String prefix) {
        return "persist-" + prefix + "-" + UUID.randomUUID().toString().substring(0, 8);
    }

    private static DisruptionPlan plan(String name) {
        DisruptionPlan plan = new DisruptionPlan();
        plan.setName(name);
        plan.setSteps(List.of(new DisruptionPlan.DisruptionStep(
                "kill-leader",
                FaultSpec.builder("kill-leader")
                        .disruptionType(DisruptionType.POD_KILL)
                        .build(),
                0,
                0,
                false)));
        return plan;
    }

    private static DisruptionReport completed(String planName) {
        DisruptionReport report = new DisruptionReport();
        report.setPlanName(planName);
        report.setStatus("COMPLETED");
        report.setStepReports(List.of(new DisruptionReport.StepReport(
                "kill-leader",
                DisruptionType.POD_KILL,
                null,
                List.of(),
                Duration.ofSeconds(4),
                Duration.ofSeconds(9),
                null,
                null,
                null,
                Map.of(),
                null,
                null,
                null,
                false,
                null,
                List.of(),
                null)));
        report.setSummary(new DisruptionReport.DisruptionSummary(
                1, 1, Duration.ofSeconds(9), 0.0, 0.0, false, Duration.ZERO, 0L));
        report.setSlaVerdict(new SlaGrader.SlaVerdict("A", false, List.of(), 1, 1, List.of()));
        return report;
    }

    /** A RUNNING row as the launcher writes it when a plan starts. */
    private String seedRunning(String planName) {
        String id = UUID.randomUUID().toString().substring(0, 8);
        DisruptionReport pending = new DisruptionReport();
        pending.setPlanName(planName);
        pending.setStatus("RUNNING");
        pending.setValidationWarnings(List.of("a validation warning"));
        DisruptionPersistence.persistReport(id, pending, repository, objectMapper);
        return id;
    }

    /** Polls the report as {@code kates disruption run} does, until it is no longer RUNNING. */
    private static String awaitFinalStatus(String id) {
        long deadline = System.nanoTime() + WAIT.toNanos();
        String status = null;
        while (System.nanoTime() < deadline) {
            status = given().when()
                    .get("/api/disruptions/" + id)
                    .then()
                    .statusCode(200)
                    .extract()
                    .path("status");
            if (!"RUNNING".equals(status)) {
                return status;
            }
            try {
                Thread.sleep(50);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                break;
            }
        }
        fail("report " + id + " still says " + status + " after " + WAIT.toSeconds() + "s");
        return status;
    }

    /** The plan's one row in the disruption list, which reads the status column rather than the report. */
    private static Map<String, String> listed(String planName) {
        List<Map<String, String>> items = given().when()
                .get("/api/disruptions?planName=" + planName)
                .then()
                .statusCode(200)
                .extract()
                .path("items");
        assertEquals(1, items.size(), "rows for " + planName + ": " + items);
        return items.get(0);
    }
}
