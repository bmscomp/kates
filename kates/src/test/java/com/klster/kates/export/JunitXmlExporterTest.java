package com.klster.kates.export;

import com.klster.kates.domain.SlaVerdict;
import com.klster.kates.domain.SlaViolation;
import com.klster.kates.domain.TestResult;
import com.klster.kates.domain.TestRun;
import com.klster.kates.report.TestReport;
import org.junit.jupiter.api.Test;

import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.*;

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
        TestResult r1 = new TestResult();
        r1.setTaskId("produce-1");
        TestResult r2 = new TestResult();
        r2.setTaskId("consume-1");
        run.addResult(r1);
        run.addResult(r2);

        TestReport report = new TestReport();
        report.setRun(run);
        report.setMetadata(Map.of("testType", "LOAD"));

        String xml = exporter.export(report);
        long testcaseCount = xml.lines()
                .filter(l -> l.trim().startsWith("<testcase name="))
                .count();
        assertEquals(2, testcaseCount);
    }

    @Test
    void errorResultHasFailureElement() {
        TestRun run = new TestRun();
        TestResult result = new TestResult();
        result.setTaskId("produce-1");
        result.setError("Connection refused");
        run.addResult(result);

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
        TestResult result = new TestResult();
        result.setTaskId("task-1");
        result.setError("value < threshold & \"quoted\"");
        run.addResult(result);

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
