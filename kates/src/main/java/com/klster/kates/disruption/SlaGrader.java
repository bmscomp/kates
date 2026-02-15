package com.klster.kates.disruption;

import com.klster.kates.domain.SlaDefinition;
import com.klster.kates.report.ReportSummary;
import jakarta.enterprise.context.ApplicationScoped;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.logging.Logger;

/**
 * Evaluates disruption test results against SLA thresholds.
 * Produces a letter grade (A/B/C/F) and per-metric violation details.
 */
@ApplicationScoped
public class SlaGrader {

    private static final Logger LOG = Logger.getLogger(SlaGrader.class.getName());

    public record SlaViolation(
            String metricName,
            String constraint,
            double threshold,
            double actual,
            String severity
    ) {}

    public record SlaVerdict(
            String grade,
            boolean violated,
            List<SlaViolation> violations,
            int totalChecks,
            int passedChecks
    ) {
        public static SlaVerdict pass(int totalChecks) {
            return new SlaVerdict("A", false, List.of(), totalChecks, totalChecks);
        }
    }

    /**
     * Grades a disruption report against SLA thresholds.
     */
    public SlaVerdict grade(DisruptionReport report, SlaDefinition sla) {
        if (sla == null || !sla.hasConstraints()) {
            return SlaVerdict.pass(0);
        }

        List<SlaViolation> violations = new ArrayList<>();
        int totalChecks = 0;

        for (DisruptionReport.StepReport step : report.getStepReports()) {
            ReportSummary post = step.postDisruptionMetrics();
            if (post == null) continue;

            if (sla.getMaxP99LatencyMs() != null) {
                totalChecks++;
                if (post.p99LatencyMs() > sla.getMaxP99LatencyMs()) {
                    violations.add(new SlaViolation(
                            "p99LatencyMs", "max",
                            sla.getMaxP99LatencyMs(), post.p99LatencyMs(),
                            post.p99LatencyMs() > sla.getMaxP99LatencyMs() * 2 ? "CRITICAL" : "WARNING"));
                }
            }

            if (sla.getMaxP999LatencyMs() != null) {
                totalChecks++;
                if (post.p999LatencyMs() > sla.getMaxP999LatencyMs()) {
                    violations.add(new SlaViolation(
                            "p999LatencyMs", "max",
                            sla.getMaxP999LatencyMs(), post.p999LatencyMs(),
                            "WARNING"));
                }
            }

            if (sla.getMaxAvgLatencyMs() != null) {
                totalChecks++;
                if (post.avgLatencyMs() > sla.getMaxAvgLatencyMs()) {
                    violations.add(new SlaViolation(
                            "avgLatencyMs", "max",
                            sla.getMaxAvgLatencyMs(), post.avgLatencyMs(),
                            "WARNING"));
                }
            }

            if (sla.getMinThroughputRecPerSec() != null) {
                totalChecks++;
                if (post.avgThroughputRecPerSec() < sla.getMinThroughputRecPerSec()) {
                    violations.add(new SlaViolation(
                            "throughputRecPerSec", "min",
                            sla.getMinThroughputRecPerSec(), post.avgThroughputRecPerSec(),
                            post.avgThroughputRecPerSec() < sla.getMinThroughputRecPerSec() * 0.5
                                    ? "CRITICAL" : "WARNING"));
                }
            }

            if (sla.getMaxErrorRate() != null) {
                totalChecks++;
                if (post.errorRate() > sla.getMaxErrorRate()) {
                    violations.add(new SlaViolation(
                            "errorRate", "max",
                            sla.getMaxErrorRate(), post.errorRate(),
                            post.errorRate() > sla.getMaxErrorRate() * 5 ? "CRITICAL" : "WARNING"));
                }
            }

            if (sla.getMaxRtoMs() != null && step.timeToAllReady() != null) {
                totalChecks++;
                long rtoMs = step.timeToAllReady().toMillis();
                if (rtoMs > sla.getMaxRtoMs()) {
                    violations.add(new SlaViolation(
                            "rtoMs", "max",
                            sla.getMaxRtoMs().doubleValue(), rtoMs,
                            rtoMs > sla.getMaxRtoMs() * 2 ? "CRITICAL" : "WARNING"));
                }
            }
        }

        Map<String, Double> deltas = computeWorstDeltas(report);
        if (sla.getMaxDataLossPercent() != null && deltas.containsKey("dataLossPercent")) {
            totalChecks++;
            double actual = deltas.get("dataLossPercent");
            if (actual > sla.getMaxDataLossPercent()) {
                violations.add(new SlaViolation(
                        "dataLossPercent", "max",
                        sla.getMaxDataLossPercent(), actual,
                        "CRITICAL"));
            }
        }

        int passedChecks = totalChecks - violations.size();
        String grade = computeGrade(totalChecks, violations);

        LOG.info("SLA verdict: " + grade + " (" + passedChecks + "/" + totalChecks
                + " passed, " + violations.size() + " violations)");

        return new SlaVerdict(grade, !violations.isEmpty(), violations, totalChecks, passedChecks);
    }

    /**
     * Grades a single step's disruption impact against SLA.
     */
    public SlaVerdict gradeStep(DisruptionReport.StepReport step, SlaDefinition sla) {
        if (sla == null || !sla.hasConstraints()) {
            return SlaVerdict.pass(0);
        }

        DisruptionReport tempReport = new DisruptionReport();
        tempReport.setStepReports(List.of(step));
        return grade(tempReport, sla);
    }

    private String computeGrade(int totalChecks, List<SlaViolation> violations) {
        if (violations.isEmpty()) return "A";

        long criticalCount = violations.stream()
                .filter(v -> "CRITICAL".equals(v.severity())).count();

        if (criticalCount > 0) return "F";

        double failRate = (double) violations.size() / totalChecks;
        if (failRate > 0.5) return "D";
        if (failRate > 0.25) return "C";
        return "B";
    }

    private Map<String, Double> computeWorstDeltas(DisruptionReport report) {
        Map<String, Double> worst = new java.util.LinkedHashMap<>();
        for (DisruptionReport.StepReport step : report.getStepReports()) {
            if (step.impactDeltas() != null) {
                step.impactDeltas().forEach((k, v) ->
                        worst.merge(k, v, (a, b) -> Math.abs(a) > Math.abs(b) ? a : b));
            }
        }
        return worst;
    }
}
