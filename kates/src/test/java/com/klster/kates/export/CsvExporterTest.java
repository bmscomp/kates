package com.klster.kates.export;

import static org.junit.jupiter.api.Assertions.*;

import java.util.Map;

import org.junit.jupiter.api.Test;

import com.klster.kates.domain.TestResult;
import com.klster.kates.domain.TestRun;
import com.klster.kates.report.ReportSummary;
import com.klster.kates.report.TestReport;

class CsvExporterTest {

    private final CsvExporter exporter = new CsvExporter();

    @Test
    void headerRowPresent() {
        TestReport report = emptyReport();
        String csv = exporter.export(report);
        String firstLine = csv.lines().findFirst().orElse("");
        assertTrue(firstLine.startsWith("runId,testType,backend,phase,recordsSent"));
    }

    @Test
    void resultRowContainsAllFields() {
        TestRun run = new TestRun();
        TestResult result = new TestResult();
        result.setTaskId("task-1");
        result.setRecordsSent(1000);
        result.setThroughputRecordsPerSec(500.0);
        result.setAvgLatencyMs(5.0);
        result.setP50LatencyMs(3.0);
        result.setP95LatencyMs(10.0);
        result.setP99LatencyMs(20.0);
        result.setMaxLatencyMs(50.0);
        run.addResult(result);

        TestReport report = new TestReport();
        report.setRun(run);
        report.setMetadata(Map.of("testType", "LOAD", "backend", "native"));

        String csv = exporter.export(report);
        long dataLines = csv.lines()
                .filter(l -> !l.startsWith("#") && !l.isBlank() && !l.startsWith("runId"))
                .count();
        assertEquals(1, dataLines);
        assertTrue(csv.contains("1000"));
    }

    @Test
    void summaryAppended() {
        TestReport report = new TestReport();
        report.setRun(new TestRun());
        report.setSummary(new ReportSummary(1000, 500.0, 600.0, 5.0, 3.0, 2.0, 8.0, 15.0, 0, 50.0, 0, 0.0, 0));

        String csv = exporter.export(report);
        assertTrue(csv.contains("# Summary"));
        assertTrue(csv.contains("totalRecords,1000"));
    }

    @Test
    void emptyReportProducesHeaderOnly() {
        TestReport report = emptyReport();
        String csv = exporter.export(report);
        long nonEmptyLines = csv.lines().filter(l -> !l.isBlank()).count();
        assertEquals(1, nonEmptyLines, "Only header line expected");
    }

    @Test
    void csvEscapingHandlesCommasAndQuotes() {
        TestRun run = new TestRun();
        TestResult result = new TestResult();
        result.setTaskId("task-1");
        result.setError("Connection failed, retrying \"now\"");
        run.addResult(result);

        TestReport report = new TestReport();
        report.setRun(run);
        report.setMetadata(Map.of("testType", "LOAD", "backend", "native"));

        String csv = exporter.export(report);
        assertTrue(
                csv.contains("\"Connection failed, retrying \"\"now\"\"\""),
                "Commas and quotes in error field should be escaped");
    }

    private TestReport emptyReport() {
        TestReport report = new TestReport();
        report.setRun(new TestRun());
        return report;
    }
}
