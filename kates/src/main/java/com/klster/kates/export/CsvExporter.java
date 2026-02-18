package com.klster.kates.export;

import jakarta.enterprise.context.ApplicationScoped;

import com.klster.kates.domain.TestResult;
import com.klster.kates.report.ReportSummary;
import com.klster.kates.report.TestReport;

/**
 * Exports a {@link TestReport} as CSV.
 * One header row + one data row per test result.
 */
@ApplicationScoped
public class CsvExporter {

    private static final String HEADER =
            "runId,testType,backend,phase,recordsSent,throughputRecPerSec,throughputMBPerSec,"
                    + "avgLatencyMs,p50LatencyMs,p95LatencyMs,p99LatencyMs,maxLatencyMs,error";

    public String export(TestReport report) {
        StringBuilder sb = new StringBuilder();
        sb.append(HEADER).append("\n");

        String runId = report.getRun() != null ? report.getRun().getId() : "";
        String type = report.getMetadata() != null ? report.getMetadata().getOrDefault("testType", "") : "";
        String backend = report.getMetadata() != null ? report.getMetadata().getOrDefault("backend", "") : "";

        if (report.getRun() != null && report.getRun().getResults() != null) {
            for (TestResult r : report.getRun().getResults()) {
                sb.append(csvEscape(runId)).append(",");
                sb.append(csvEscape(type)).append(",");
                sb.append(csvEscape(backend)).append(",");
                sb.append(csvEscape(r.getPhaseName() != null ? r.getPhaseName() : ""))
                        .append(",");
                sb.append(r.getRecordsSent()).append(",");
                sb.append(fmt(r.getThroughputRecordsPerSec())).append(",");
                sb.append(fmt(r.getThroughputMBPerSec())).append(",");
                sb.append(fmt(r.getAvgLatencyMs())).append(",");
                sb.append(fmt(r.getP50LatencyMs())).append(",");
                sb.append(fmt(r.getP95LatencyMs())).append(",");
                sb.append(fmt(r.getP99LatencyMs())).append(",");
                sb.append(fmt(r.getMaxLatencyMs())).append(",");
                sb.append(csvEscape(r.getError() != null ? r.getError() : ""));
                sb.append("\n");
            }
        }

        // Append summary row
        ReportSummary s = report.getSummary();
        if (s != null) {
            sb.append("\n# Summary\n");
            sb.append("totalRecords,").append(s.totalRecords()).append("\n");
            sb.append("avgThroughputRecPerSec,")
                    .append(fmt(s.avgThroughputRecPerSec()))
                    .append("\n");
            sb.append("peakThroughputRecPerSec,")
                    .append(fmt(s.peakThroughputRecPerSec()))
                    .append("\n");
            sb.append("avgLatencyMs,").append(fmt(s.avgLatencyMs())).append("\n");
            sb.append("p99LatencyMs,").append(fmt(s.p99LatencyMs())).append("\n");
            sb.append("errorRate,").append(fmt(s.errorRate())).append("\n");
        }

        return sb.toString();
    }

    private String csvEscape(String value) {
        if (value.contains(",") || value.contains("\"") || value.contains("\n")) {
            return "\"" + value.replace("\"", "\"\"") + "\"";
        }
        return value;
    }

    private String fmt(double v) {
        return String.format("%.4f", v);
    }
}
