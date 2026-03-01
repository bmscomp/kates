package com.bmscomp.kates.export;

import static org.junit.jupiter.api.Assertions.*;

import java.util.Map;

import org.junit.jupiter.api.Test;

import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.domain.TestRun;
import com.bmscomp.kates.report.ReportSummary;
import com.bmscomp.kates.report.TestReport;

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
        TestResult result = new TestResult()
            .withTaskId("task-1")
            .withRecordsSent(1000)
            .withThroughputRecordsPerSec(500.0)
            .withAvgLatencyMs(5.0)
            .withP50LatencyMs(3.0)
            .withP95LatencyMs(10.0)
            .withP99LatencyMs(20.0)
            .withMaxLatencyMs(50.0);
        run = run.withAddedResult(result);

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
        TestResult result = new TestResult()
            .withTaskId("task-1")
            .withError("Connection failed, retrying \"now\"");
        run = run.withAddedResult(result);

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
