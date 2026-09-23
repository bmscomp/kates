package com.bmscomp.kates.disruption;

import java.util.ArrayList;
import java.util.List;
import jakarta.enterprise.context.ApplicationScoped;

import org.jboss.logging.Logger;

import com.bmscomp.kates.domain.SlaDefinition;
import com.bmscomp.kates.report.ReportSummary;

/**
 * Evaluates disruption test results against SLA thresholds.
 * Produces a letter grade (A/B/C/F) and per-metric violation details.
 *
 * <p>A declared constraint that nothing could be compared against is listed in
 * {@link SlaVerdict#unevaluated()} rather than counted as passed, and an SLA
 * with no evaluable constraint at all is graded {@value #NOT_GRADED}, not A.
 */
@ApplicationScoped
public class SlaGrader {

    private static final Logger LOG = Logger.getLogger(SlaGrader.class);

    /** The grade when no declared constraint could be evaluated. Fits the two-character grade column. */
    static final String NOT_GRADED = "-";

    public record SlaViolation(
            String metricName, String constraint, double threshold, double actual, String severity) {}

    /**
     * @param unevaluated declared constraints with nothing to compare against,
     *     each as {@code "<field>: <reason>"}. Not counted in the checks.
     */
    public record SlaVerdict(
            String grade,
            boolean violated,
            List<SlaViolation> violations,
            int totalChecks,
            int passedChecks,
            List<String> unevaluated) {
        public static SlaVerdict pass(int totalChecks) {
            return new SlaVerdict("A", false, List.of(), totalChecks, totalChecks, List.of());
        }
    }

    /**
     * Declared constraints a disruption report never has a measurement for.
     *
     * <p>A plan injects faults and reads broker metrics from Prometheus; it runs
     * no workload of its own. So there is no record count, no data-loss or RPO
     * figure, and the Prometheus capture has no p99.9 or error rate — it fills
     * both with a hard-coded 0. Each of these used to be skipped or compared
     * against that 0, so a plan whose SLA declared only these passed with an A.
     */
    public static List<String> unevaluableConstraints(SlaDefinition sla) {
        List<String> unevaluable = new ArrayList<>();
        if (sla == null) {
            return unevaluable;
        }
        if (sla.getMaxDataLossPercent() != null) {
            unevaluable.add("maxDataLossPercent: a disruption plan runs no workload, so no data loss is measured");
        }
        if (sla.getMaxRpoMs() != null) {
            unevaluable.add("maxRpoMs: a disruption plan runs no workload, so no RPO is measured");
        }
        if (sla.getMinRecordsProcessed() != null) {
            unevaluable.add("minRecordsProcessed: a disruption plan runs no workload, so no records are counted");
        }
        if (sla.getMaxP999LatencyMs() != null) {
            unevaluable.add("maxP999LatencyMs: the Prometheus capture has no p99.9 latency");
        }
        if (sla.getMaxErrorRate() != null) {
            unevaluable.add("maxErrorRate: the Prometheus capture has no error rate");
        }
        return unevaluable;
    }

    /**
     * Grades a disruption report against SLA thresholds.
     */
    public SlaVerdict grade(DisruptionReport report, SlaDefinition sla) {
        if (sla == null || !sla.hasConstraints()) {
            return SlaVerdict.pass(0);
        }

        List<SlaViolation> violations = new ArrayList<>();
        List<String> unevaluated = unevaluableConstraints(sla);
        int totalChecks = 0;
        boolean anyPostMetrics = false;
        boolean anyRecovery = false;

        for (DisruptionReport.StepReport step : report.getStepReports()) {
            // Recovery time comes from the pod watcher, not Prometheus, so it is
            // checked whether or not the step captured metrics.
            if (sla.getMaxRtoMs() != null && step.timeToAllReady() != null) {
                anyRecovery = true;
                totalChecks++;
                long rtoMs = step.timeToAllReady().toMillis();
                if (rtoMs > sla.getMaxRtoMs()) {
                    violations.add(new SlaViolation(
                            "rtoMs",
                            "max",
                            sla.getMaxRtoMs().doubleValue(),
                            rtoMs,
                            rtoMs > sla.getMaxRtoMs() * 2 ? "CRITICAL" : "WARNING"));
                }
            }

            ReportSummary post = step.postDisruptionMetrics();
            if (post == null) continue;
            anyPostMetrics = true;

            if (sla.getMaxP99LatencyMs() != null) {
                totalChecks++;
                if (post.p99LatencyMs() > sla.getMaxP99LatencyMs()) {
                    violations.add(new SlaViolation(
                            "p99LatencyMs",
                            "max",
                            sla.getMaxP99LatencyMs(),
                            post.p99LatencyMs(),
                            post.p99LatencyMs() > sla.getMaxP99LatencyMs() * 2 ? "CRITICAL" : "WARNING"));
                }
            }

            if (sla.getMaxAvgLatencyMs() != null) {
                totalChecks++;
                if (post.avgLatencyMs() > sla.getMaxAvgLatencyMs()) {
                    violations.add(new SlaViolation(
                            "avgLatencyMs", "max", sla.getMaxAvgLatencyMs(), post.avgLatencyMs(), "WARNING"));
                }
            }

            if (sla.getMinThroughputRecPerSec() != null) {
                totalChecks++;
                if (post.avgThroughputRecPerSec() < sla.getMinThroughputRecPerSec()) {
                    violations.add(new SlaViolation(
                            "throughputRecPerSec",
                            "min",
                            sla.getMinThroughputRecPerSec(),
                            post.avgThroughputRecPerSec(),
                            post.avgThroughputRecPerSec() < sla.getMinThroughputRecPerSec() * 0.5
                                    ? "CRITICAL"
                                    : "WARNING"));
                }
            }
        }

        if (!anyPostMetrics) {
            String noMetrics = ": no step captured post-disruption metrics"
                    + " (Prometheus unreachable, or observationWindowSec is 0)";
            if (sla.getMaxP99LatencyMs() != null) unevaluated.add("maxP99LatencyMs" + noMetrics);
            if (sla.getMaxAvgLatencyMs() != null) unevaluated.add("maxAvgLatencyMs" + noMetrics);
            if (sla.getMinThroughputRecPerSec() != null) unevaluated.add("minThroughputRecPerSec" + noMetrics);
        }
        if (sla.getMaxRtoMs() != null && !anyRecovery) {
            unevaluated.add("maxRtoMs: no step measured a recovery time (requireRecovery is off, or the step failed)");
        }

        int passedChecks = totalChecks - violations.size();
        String grade = totalChecks == 0 ? NOT_GRADED : computeGrade(totalChecks, violations);

        LOG.info("SLA verdict: " + grade + " (" + passedChecks + "/" + totalChecks + " passed, " + violations.size()
                + " violations)");
        if (!unevaluated.isEmpty()) {
            LOG.warn("SLA constraints not evaluated, so neither passed nor failed: " + unevaluated);
        }

        return new SlaVerdict(
                grade, !violations.isEmpty(), violations, totalChecks, passedChecks, List.copyOf(unevaluated));
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

        long criticalCount =
                violations.stream().filter(v -> "CRITICAL".equals(v.severity())).count();

        if (criticalCount > 0) return "F";

        double failRate = (double) violations.size() / totalChecks;
        if (failRate > 0.5) return "D";
        if (failRate > 0.25) return "C";
        return "B";
    }
}
