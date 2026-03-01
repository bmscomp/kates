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
    void errorResultHasFailureElement() {
        TestRun run = new TestRun();
        TestResult result = new TestResult()
            .withTaskId("produce-1")
            .withError("Connection refused");
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
        TestResult result = new TestResult()
            .withTaskId("task-1")
            .withError("value < threshold & \"quoted\"");
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
    void emptyReportProducesValidXml() {
        TestReport report = emptyReport();
        String xml = exporter.export(report);
        assertTrue(xml.contains("<testsuite"));
        assertTrue(xml.contains("</testsuite>"));
        assertTrue(xml.contains("tests=\"0\""));
    }

    private TestReport emptyReport() {
        TestReport report = new TestReport();
        report.setRun(new TestRun());
        report.setMetadata(Map.of("testType", "kates"));
        return report;
    }
}
