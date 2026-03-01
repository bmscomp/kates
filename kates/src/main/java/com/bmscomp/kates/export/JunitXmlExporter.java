package com.bmscomp.kates.export;

import java.time.Instant;
import java.util.List;
import jakarta.enterprise.context.ApplicationScoped;

import com.bmscomp.kates.domain.SlaViolation;
import com.bmscomp.kates.domain.TestResult;
import com.bmscomp.kates.report.TestReport;

/**
 * Exports a {@link TestReport} as JUnit XML format.
 * Each test result maps to a {@code <testcase>} element.
 * SLA violations map to {@code <failure>} elements.
 */
@ApplicationScoped
public class JunitXmlExporter {

    public String export(TestReport report) {
        StringBuilder sb = new StringBuilder();
        sb.append("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n");

        String suiteName =
                report.getMetadata() != null ? report.getMetadata().getOrDefault("testType", "kates") : "kates";
        List<TestResult> results = report.getRun() != null ? report.getRun().getResults() : List.of();
        int failures = report.getOverallSlaVerdict() != null
                        && !report.getOverallSlaVerdict().passed()
                ? report.getOverallSlaVerdict().violations().size()
                : 0;

        sb.append("<testsuite name=\"")
                .append(xmlEscape(suiteName))
                .append("\" tests=\"")
                .append(results != null ? results.size() : 0)
                .append("\" failures=\"")
                .append(failures)
                .append("\" errors=\"0\">\n");

        if (results != null) {
            for (TestResult r : results) {
                String caseName = r.getPhaseName() != null ? r.getPhaseName() : r.getTaskId();
                sb.append("  <testcase name=\"")
                        .append(xmlEscape(caseName))
                        .append("\" classname=\"kates.")
                        .append(xmlEscape(suiteName))
                        .append("\" time=\"")
                        .append(String.format("%.3f", computeDurationSec(r)))
                        .append("\"");

                if (r.getError() != null) {
                    sb.append(">\n");
                    sb.append("    <failure message=\"")
                            .append(xmlEscape(r.getError()))
                            .append("\" type=\"Error\"/>\n");
                    sb.append("  </testcase>\n");
                } else {
                    sb.append("/>\n");
                }
            }
        }

        // Emit SLA violations as system-level failures
        if (report.getOverallSlaVerdict() != null
                && !report.getOverallSlaVerdict().violations().isEmpty()) {
            for (SlaViolation v : report.getOverallSlaVerdict().violations()) {
                sb.append("  <testcase name=\"SLA-")
                        .append(xmlEscape(v.metric()))
                        .append("\" classname=\"kates.sla\">\n");
                sb.append("    <failure message=\"")
                        .append(xmlEscape(v.metric()))
                        .append(" threshold=")
                        .append(String.format("%.2f", v.threshold()))
                        .append(" actual=")
                        .append(String.format("%.2f", v.actual()))
                        .append("\" type=\"SlaViolation\"/>\n");
                sb.append("  </testcase>\n");
            }
        }

        sb.append("</testsuite>\n");
        return sb.toString();
    }

    private String xmlEscape(String value) {
        if (value == null) return "";
        return value.replace("&", "&amp;")
                .replace("<", "&lt;")
                .replace(">", "&gt;")
                .replace("\"", "&quot;")
                .replace("'", "&apos;");
    }

    private double computeDurationSec(TestResult r) {
        try {
            if (r.getStartTime() != null && r.getEndTime() != null) {
                Instant start = Instant.parse(r.getStartTime());
                Instant end = Instant.parse(r.getEndTime());
                return java.time.Duration.between(start, end).toMillis() / 1000.0;
            }
        } catch (Exception ignored) {
        }
        return 0;
    }
}
