package com.bmscomp.kates.export;

import static org.junit.jupiter.api.Assertions.*;

import java.util.List;
import java.util.Map;

import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.SlaVerdict;
import com.bmscomp.kates.domain.SlaViolation;
import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.report.TestReport;

class JunitXmlExporterTest {

    private final JunitXmlExporter exporter = new JunitXmlExporter();

    @Test
    void outputStartsWithXmlDeclaration() {
        TestReport report = emptyReport();
        String xml = exporter.export(report);
        assertTrue(xml.startsWith("<?xml version=\"1.0\" encoding=\"UTF-8\"?>"));
    }

    @Test
    void testcasePerResult() {
        TestRun run = new TestRun();
        TestResult r1 = new TestResult().withTaskId("produce-1");
        TestResult r2 = new TestResult().withTaskId("consume-1");
        run = run.withAddedResult(r1);
        run = run.withAddedResult(r2);

        TestReport report = new TestReport();
        report.setRun(run);
        report.setMetadata(Map.of("testType", "LOAD"));

        String xml = exporter.export(report);
        long testcaseCount =
                xml.lines().filter(l -> l.trim().startsWith("<testcase name=")).count();
        assertEquals(2, testcaseCount);
    }

    @Test
    void aPhaseCancelledBeforeItsTurnTookNoTime() {
        // A scenario's later phase has the startTime it was due to start at,
        // which comes after the time a cancel ended it.
        TestRun run = new TestRun()
                .withAddedResult(new TestResult()
                        .withTaskId("a1b2c3d4-ramp-ramp-3")
                        .withPhaseName("ramp")
                        .withStartTime("2026-10-05T12:10:00Z")
                        .withEndTime("2026-10-05T12:01:00Z")
                        .withError("Cancelled by user"));
        TestReport report = new TestReport();
        report.setRun(run);
        report.setMetadata(Map.of("testType", "LOAD"));

        String xml = exporter.export(report);
        assertTrue(xml.matches("(?s).*<testcase name=\"ramp\"[^>]*time=\"0[.,]000\".*"), xml);
    }

    @Test
    void errorResultHasFailureElement() {
        TestRun run = new TestRun();
        TestResult result = new TestResult().withTaskId("produce-1").withError("Connection refused");
        run = run.withAddedResult(result);

        TestReport report = new TestReport();
        report.setRun(run);
        report.setMetadata(Map.of("testType", "LOAD"));

        String xml = exporter.export(report);
        assertTrue(xml.contains("<failure message="));
        assertTrue(xml.contains("Connection refused"));
    }

    @Test
    void slaViolationsEmittedAsTestcases() {
        TestReport report = emptyReport();
        SlaViolation v1 = SlaViolation.critical("p99LatencyMs", 50.0, 100.0);
        SlaViolation v2 = SlaViolation.warning("avgLatencyMs", 10.0, 20.0);
        report.setOverallSlaVerdict(SlaVerdict.fail(List.of(v1, v2)));

        String xml = exporter.export(report);
        assertTrue(xml.contains("SLA-p99LatencyMs"));
        assertTrue(xml.contains("SLA-avgLatencyMs"));
        assertTrue(xml.contains("type=\"SlaViolation\""));
    }

    @Test
    void xmlEscapingHandlesSpecialChars() {
        TestRun run = new TestRun();
        TestResult result = new TestResult().withTaskId("task-1").withError("value < threshold & \"quoted\"");
        run = run.withAddedResult(result);

        TestReport report = new TestReport();
        report.setRun(run);
        report.setMetadata(Map.of("testType", "LOAD"));

        String xml = exporter.export(report);
        assertTrue(xml.contains("&lt;"));
        assertTrue(xml.contains("&amp;"));
        assertTrue(xml.contains("&quot;"));
    }

    @Test
    void emptyReportStillHoldsATestCase() {
        // An empty suite reads as a pass to CI whatever the run did, so the
        // run itself is the test case when it has no row and no violation.
        TestReport report = emptyReport();
        String xml = exporter.export(report);
        assertTrue(xml.contains("<testsuite"));
        assertTrue(xml.contains("</testsuite>"));
        assertTrue(xml.contains("tests=\"1\" failures=\"0\""), xml);
        assertEquals(1, testcases(xml));
        assertTrue(xml.contains("<testcase name=\"" + report.getRun().getId() + "\""), xml);
    }

    @Test
    void failedRunWithNoRowsHasOneFailingTestCase() {
        // A run that failed creating its topic: no task row, and the status
        // violation the report's verdict carries for a FAILED run.
        TestReport report = emptyReport();
        report.setRun(new TestRun().withStatus(TestResult.TaskStatus.FAILED));
        report.setOverallSlaVerdict(SlaVerdict.fail(List.of(SlaViolation.runFailed("FAILED before any task ran"))));

        String xml = exporter.export(report);

        assertTrue(xml.contains("tests=\"1\" failures=\"1\""), xml);
        assertEquals(1, testcases(xml));
        assertTrue(xml.contains("<failure message=\"status FAILED before any task ran\""), xml);
    }

    @Test
    void countsEveryTestCaseAndEveryFailure() {
        TestRun run = new TestRun()
                .withAddedResult(new TestResult().withTaskId("produce-1").withError("NOT_ENOUGH_REPLICAS"))
                .withAddedResult(new TestResult().withTaskId("consume-1"));
        TestReport report = new TestReport();
        report.setRun(run);
        report.setMetadata(Map.of("testType", "LOAD"));
        report.setOverallSlaVerdict(SlaVerdict.fail(List.of(SlaViolation.critical("p99LatencyMs", 50.0, 100.0))));

        String xml = exporter.export(report);

        // Two rows and one violation; the row's error is a failure too.
        assertTrue(xml.contains("tests=\"3\" failures=\"2\""), xml);
        assertEquals(3, testcases(xml));
    }

    @Test
    void notMeasuredGateSaysSoInsteadOfAnActualValue() {
        TestReport report = emptyReport();
        report.setOverallSlaVerdict(SlaVerdict.fail(
                List.of(SlaViolation.notMeasured("p99LatencyMs", 50.0, SlaViolation.Severity.CRITICAL))));

        String xml = exporter.export(report);

        assertTrue(xml.contains("message=\"p99LatencyMs not measured (threshold=50.00)\""), xml);
        assertFalse(xml.contains("actual="), xml);
    }

    private static long testcases(String xml) {
        return xml.lines().filter(l -> l.trim().startsWith("<testcase ")).count();
    }

    private TestReport emptyReport() {
        TestReport report = new TestReport();
        report.setRun(new TestRun());
        report.setMetadata(Map.of("testType", "kates"));
        return report;
    }
}
