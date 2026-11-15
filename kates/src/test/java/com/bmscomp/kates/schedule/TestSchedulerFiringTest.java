package com.bmscomp.kates.schedule;

import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.ArgumentMatchers.eq;
import static org.mockito.Mockito.*;

import java.time.Duration;
import java.time.Instant;
import java.time.temporal.ChronoUnit;
import java.util.UUID;
import jakarta.inject.Inject;

import io.quarkus.test.InjectMock;
import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.junit.TestProfile;
import io.restassured.RestAssured;
import io.restassured.http.ContentType;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import com.bmscomp.kates.SchedulerOffTestProfile;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.domain.TestType;
import com.bmscomp.kates.engine.ConcurrencyLimitException;
import com.bmscomp.kates.engine.TestOrchestrator;
import com.bmscomp.kates.util.Result;

/**
 * Each due time of a schedule starts one run, however many ticks see it, and
 * a due time a late tick missed still fires. Every job is off, so only the
 * test's own ticks fire anything.
 */
@QuarkusTest
@TestProfile(SchedulerOffTestProfile.class)
class TestSchedulerFiringTest {

    private static final Instant T0 = Instant.parse("2026-11-15T10:36:30Z");

    @InjectMock
    TestOrchestrator orchestrator;

    @Inject
    TestScheduler scheduler;

    @Inject
    ScheduledTestRunRepository repository;

    private String id;

    @BeforeEach
    void runsStart() {
        id = UUID.randomUUID().toString().substring(0, 8);
        when(orchestrator.executeTest(any(), anyString()))
                .thenReturn(Result.success(new TestRun(TestType.LOAD, null).withId("run-" + id)));
    }

    @AfterEach
    void deleteSchedule() {
        repository.delete(id);
    }

    @Test
    void aDueTimeStartsOneRunAcrossTicks() {
        save(true, T0);

        scheduler.evaluate(T0.plusSeconds(35));
        scheduler.evaluate(T0.plusSeconds(45));

        verify(orchestrator, times(1)).executeTest(any(), eq(key("2026-11-15T10:37:00Z")));
        verify(orchestrator, times(1)).executeTest(any(), anyString());
    }

    @Test
    void aRefusedStartStillCountsAsTheFiring() {
        when(orchestrator.executeTest(any(), anyString())).thenReturn(Result.failure(new ConcurrencyLimitException(1)));
        save(true, T0);

        scheduler.evaluate(T0.plusSeconds(35));
        scheduler.evaluate(T0.plusSeconds(45));

        verify(orchestrator, times(1)).executeTest(any(), anyString());
    }

    /** A takeover that left two minutes without a tick still fires, once, the latest due time. */
    @Test
    void aMissedMinuteFiresAfterATakeover() {
        save(true, T0);

        scheduler.evaluate(T0.plusSeconds(35));
        scheduler.evaluate(T0.plusSeconds(155));

        verify(orchestrator).executeTest(any(), eq(key("2026-11-15T10:37:00Z")));
        verify(orchestrator).executeTest(any(), eq(key("2026-11-15T10:39:00Z")));
        verify(orchestrator, times(2)).executeTest(any(), anyString());
    }

    @Test
    void aReEnabledScheduleWaitsForItsNextDueTime() {
        Instant now = Instant.now();
        save(false, now.minus(Duration.ofMinutes(10)));

        RestAssured.given()
                .contentType(ContentType.JSON)
                .body("{\"enabled\":true}")
                .when()
                .put("/api/schedules/" + id)
                .then()
                .statusCode(200);

        scheduler.evaluate(Instant.now());
        verify(orchestrator, never()).executeTest(any(), anyString());

        Instant next = Instant.now().plusSeconds(60);
        scheduler.evaluate(next);
        verify(orchestrator, times(1)).executeTest(any(), anyString());
        verify(orchestrator)
                .executeTest(any(), eq(key(next.truncatedTo(ChronoUnit.MINUTES).toString())));
    }

    /** A PUT that leaves an enabled schedule enabled, as a rename does, keeps its pending due time. */
    @Test
    void aPutThatKeepsAScheduleOnKeepsItsDueTime() {
        save(true, Instant.now().minus(Duration.ofMinutes(10)));

        RestAssured.given()
                .contentType(ContentType.JSON)
                .body("{\"enabled\":true,\"name\":\"renamed-" + id + "\"}")
                .when()
                .put("/api/schedules/" + id)
                .then()
                .statusCode(200);

        scheduler.evaluate(Instant.now());
        verify(orchestrator, times(1)).executeTest(any(), anyString());
    }

    private void save(boolean enabled, Instant createdAt) {
        ScheduledTestRun schedule = new ScheduledTestRun();
        schedule.setId(id);
        schedule.setName("every-minute-" + id);
        schedule.setCronExpression("* * * * *");
        schedule.setEnabled(enabled);
        schedule.setRequestJson("{\"type\":\"LOAD\",\"spec\":{\"numRecords\":1000}}");
        schedule.setCreatedAt(createdAt);
        repository.save(schedule);
    }

    private String key(String due) {
        return "schedule:" + id + ":" + due;
    }
}
