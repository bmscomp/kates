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
 * SLA violations map to {@code <failure>} elements, a FAILED run's status
 * among them: the report's verdict names it first.
 *
 * <p>The suite always holds a test case, and its {@code tests} and
 * {@code failures} count every case and every failure. A FAILED run with no
 * task rows exported {@code tests="0" failures="0"}, an empty suite that CI
 * reads as a pass, and a task's error was a failure the counts left out.
 */
@ApplicationScoped
public class JunitXmlExporter {

    public String export(TestReport report) {
        StringBuilder sb = new StringBuilder();
        sb.append("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n");

        String suiteName =
                report.getMetadata() != null ? report.getMetadata().getOrDefault("testType", "kates") : "kates";
        List<TestResult> results = report.getRun() != null && report.getRun().getResults() != null
                ? report.getRun().getResults()
                : List.of();
        List<SlaViolation> violations = report.getOverallSlaVerdict() != null
                ? report.getOverallSlaVerdict().violations()
                : List.of();
        // No task row and nothing violated, which a finished run has only when
        // it ran nothing and failed nothing: the run itself is then the test
        // case, so the suite is never empty.
        boolean runCase = results.isEmpty() && violations.isEmpty();
        long failures = results.stream().filter(r -> r.getError() != null).count() + violations.size();

        sb.append("<testsuite name=\"")
                .append(xmlEscape(suiteName))
                .append("\" tests=\"")
                .append(results.size() + violations.size() + (runCase ? 1 : 0))
                .append("\" failures=\"")
                .append(failures)
                .append("\" errors=\"0\">\n");

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

        // Emit SLA violations as system-level failures
        for (SlaViolation v : violations) {
            sb.append("  <testcase name=\"SLA-").append(xmlEscape(v.metric())).append("\" classname=\"kates.sla\">\n");
            sb.append("    <failure message=\"")
                    .append(xmlEscape(failureMessage(v)))
                    .append("\" type=\"SlaViolation\"/>\n");
            sb.append("  </testcase>\n");
        }

        if (runCase) {
            String runName = report.getRun() != null && report.getRun().getId() != null
                    ? report.getRun().getId()
                    : suiteName;
            sb.append("  <testcase name=\"")
                    .append(xmlEscape(runName))
                    .append("\" classname=\"kates.")
                    .append(xmlEscape(suiteName))
                    .append("\"/>\n");
        }

        sb.append("</testsuite>\n");
        return sb.toString();
    }

    /**
     * A violation as one line. A reason replaces the numbers it stands in for:
     * "p99LatencyMs not measured (threshold=50.00)", "status FAILED: ...".
     */
    private static String failureMessage(SlaViolation v) {
        if (v.reason() == null) {
            return v.metric() + " threshold=" + String.format("%.2f", v.threshold()) + " actual="
                    + String.format("%.2f", v.actual());
        }
        String message = v.metric() + " " + v.reason();
        return v.threshold() >= 0 ? message + " (threshold=" + String.format("%.2f", v.threshold()) + ")" : message;
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
