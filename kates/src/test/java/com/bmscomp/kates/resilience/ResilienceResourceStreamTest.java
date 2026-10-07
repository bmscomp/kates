package com.bmscomp.kates.resilience;

import static io.restassured.RestAssured.given;
import static org.hamcrest.Matchers.*;
import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.when;

import java.util.List;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.TimeUnit;
import java.util.logging.Handler;
import java.util.logging.Level;
import java.util.logging.LogRecord;
import java.util.logging.SimpleFormatter;

import io.quarkus.test.InjectMock;
import io.quarkus.test.junit.QuarkusTest;
import org.jboss.logmanager.ExtLogRecord;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/**
 * The answer to a resilience run that gets through every check: a space every
 * 10 s while the run goes on, then its report. The orchestrator is a mock, so
 * nothing runs and no fault goes in.
 */
@QuarkusTest
class ResilienceResourceStreamTest {

    private static final String BODY = "{\"testRequest\":{\"type\":\"LOAD\"},\"chaosSpec\":{\"experimentName\":"
            + "\"kafka-pod-kill\",\"disruptionType\":\"POD_KILL\",\"chaosDurationSec\":30}}";

    @InjectMock
    ResilienceOrchestrator orchestrator;

    /** Held so the logger, and the handler on it, outlive the test's own references. */
    private final java.util.logging.Logger resourceLog =
            java.util.logging.Logger.getLogger(ResilienceResource.class.getName());

    private final CapturingHandler captured = new CapturingHandler();

    @BeforeEach
    void captureLogs() {
        resourceLog.addHandler(captured);
    }

    @AfterEach
    void releaseLogs() {
        resourceLog.removeHandler(captured);
    }

    /**
     * A run that sends its report logs no error. writeValue closes the stream,
     * which ends the answer, and the flush after it threw "Stream is closed",
     * so every run logged "Failed to execute resilience test" although its
     * report had gone out whole.
     */
    @Test
    void aRunThatSendsItsReportLogsNoError() throws InterruptedException {
        when(orchestrator.execute(any())).thenReturn(completed());

        given().contentType("application/json")
                .body(BODY)
                .when()
                .post("/api/resilience")
                .then()
                .statusCode(200)
                .body("status", is("COMPLETED"));

        // That error was logged after the report had gone out, so allow it a moment.
        Thread.sleep(500);
        assertEquals(List.of(), errors());
    }

    /**
     * The report goes out as soon as the run ends. The answer slept 10 s after
     * each keep-alive space and looked at the run only then, so a run that
     * ended just after a space had its report held back for up to 10 s.
     */
    @Test
    void theReportGoesOutAsSoonAsTheRunEnds() {
        // The run takes a moment, so the answer has sent a space and is
        // waiting when it ends.
        when(orchestrator.execute(any())).thenAnswer(invocation -> {
            Thread.sleep(200);
            return completed();
        });

        String answer = given().contentType("application/json")
                .body(BODY)
                .when()
                .post("/api/resilience")
                .then()
                .statusCode(200)
                .time(lessThan(5L), TimeUnit.SECONDS)
                .body("status", is("COMPLETED"))
                .extract()
                .asString();

        assertTrue(answer.startsWith(" {"), "a keep-alive space, then the report: " + answer);
    }

    /** A run that throws still logs the error, with its cause, and the answer holds no report. */
    @Test
    void aRunThatThrowsLogsTheError() {
        when(orchestrator.execute(any())).thenThrow(new IllegalStateException("the run broke"));

        String answer = given().contentType("application/json")
                .body(BODY)
                .when()
                .post("/api/resilience")
                .then()
                .extract()
                .asString();

        assertTrue(answer.isBlank(), "no report: " + answer);
        assertEquals(List.of("Failed to execute resilience test: the run broke"), errors());
    }

    private static ResilienceReport completed() {
        ResilienceReport report = new ResilienceReport();
        report.setStatus("COMPLETED");
        return report;
    }

    /** Each error the resource logged, and after a colon what caused it. */
    private List<String> errors() {
        return captured.records.stream()
                .filter(r -> r.getLevel().intValue() >= Level.SEVERE.intValue())
                .map(r -> r.getThrown() != null ? text(r) + ": " + rootMessage(r.getThrown()) : text(r))
                .toList();
    }

    private static String rootMessage(Throwable thrown) {
        while (thrown.getCause() != null) {
            thrown = thrown.getCause();
        }
        return thrown.getMessage();
    }

    private static String text(LogRecord record) {
        return record instanceof ExtLogRecord ext
                ? ext.getFormattedMessage()
                : new SimpleFormatter().formatMessage(record);
    }

    private static final class CapturingHandler extends Handler {
        final List<LogRecord> records = new CopyOnWriteArrayList<>();

        @Override
        public void publish(LogRecord record) {
            records.add(record);
        }

        @Override
        public void flush() {}

        @Override
        public void close() {}
    }
}
