package com.klster.kates.disruption;

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import jakarta.enterprise.context.ApplicationScoped;

/**
 * Computes a composite impact score (0-100) for disruption reports.
 * Quantifies severity across five dimensions: availability, latency,
 * throughput, replication health, and consumer lag.
 */
@ApplicationScoped
public class DisruptionImpactScorer {

    public record ImpactScore(
            int overall,
            String severity,
            int availabilityScore,
            int latencyScore,
            int throughputScore,
            int replicationScore,
            int lagScore,
            List<String> factors) {}

    /**
     * Scores a completed disruption report.
     */
    public ImpactScore score(DisruptionReport report) {
        List<String> factors = new ArrayList<>();
        int availability = 0, latency = 0, throughput = 0, replication = 0, lag = 0;

        if (report.getSummary() != null) {
            DisruptionReport.DisruptionSummary s = report.getSummary();

            availability = scoreAvailability(s, factors);
            latency = scoreLatency(s, factors);
            throughput = scoreThroughput(s, factors);
        }

        for (DisruptionReport.StepReport step : report.getStepReports()) {
            replication = Math.max(replication, scoreIsr(step, factors));
            lag = Math.max(lag, scoreLag(step, factors));
        }

        int overall = weightedAverage(availability, latency, throughput, replication, lag);
        String severity = classifySeverity(overall);

        return new ImpactScore(overall, severity, availability, latency, throughput, replication, lag, factors);
    }

    /**
     * Produces a human-readable breakdown map for API responses.
     */
    public Map<String, Object> toMap(ImpactScore score) {
        Map<String, Object> m = new LinkedHashMap<>();
        m.put("overall", score.overall());
        m.put("severity", score.severity());

        Map<String, Integer> dimensions = new LinkedHashMap<>();
        dimensions.put("availability", score.availabilityScore());
        dimensions.put("latency", score.latencyScore());
        dimensions.put("throughput", score.throughputScore());
        dimensions.put("replication", score.replicationScore());
        dimensions.put("consumerLag", score.lagScore());
        m.put("dimensions", dimensions);
        m.put("factors", score.factors());
        return m;
    }

    private int scoreAvailability(DisruptionReport.DisruptionSummary s, List<String> factors) {
        int failed = s.totalSteps() - s.passedSteps();
        if (failed == 0) return 0;

        int score = Math.min(100, failed * 30);
        factors.add(failed + "/" + s.totalSteps() + " steps failed");

        if (s.worstRecovery() != null) {
            long recoverySec = s.worstRecovery().toSeconds();
            if (recoverySec > 300) {
                score = Math.min(100, score + 30);
                factors.add("Worst recovery: " + recoverySec + "s (>5min)");
            } else if (recoverySec > 120) {
                score = Math.min(100, score + 15);
                factors.add("Worst recovery: " + recoverySec + "s (>2min)");
            }
        }
        return score;
    }

    private int scoreLatency(DisruptionReport.DisruptionSummary s, List<String> factors) {
        double spike = s.maxP99LatencySpike();
        if (spike <= 0) return 0;

        int score;
        if (spike > 500) {
            score = 100;
            factors.add("P99 latency spike: +" + round(spike) + "ms (extreme)");
        } else if (spike > 200) {
            score = 70;
            factors.add("P99 latency spike: +" + round(spike) + "ms (severe)");
        } else if (spike > 50) {
            score = 40;
            factors.add("P99 latency spike: +" + round(spike) + "ms (moderate)");
        } else {
            score = (int) (spike / 50.0 * 20);
        }
        return score;
    }

    private int scoreThroughput(DisruptionReport.DisruptionSummary s, List<String> factors) {
        double degradation = s.avgThroughputDegradation();
        if (degradation <= 0) return 0;

        int score;
        if (degradation > 80) {
            score = 100;
            factors.add("Throughput degraded " + round(degradation) + "% (near-total loss)");
        } else if (degradation > 50) {
            score = 70;
            factors.add("Throughput degraded " + round(degradation) + "%");
        } else if (degradation > 20) {
            score = 40;
            factors.add("Throughput degraded " + round(degradation) + "% (moderate)");
        } else {
            score = (int) (degradation / 20.0 * 20);
        }
        return score;
    }

    private int scoreIsr(DisruptionReport.StepReport step, List<String> factors) {
        if (step.isrMetrics() == null) return 0;
        IsrSnapshot.Metrics isr = step.isrMetrics();

        int score = 0;
        if (isr.minIsrDepth() <= 1) {
            score = 80;
            factors.add("Step '" + step.stepName() + "': ISR dropped to " + isr.minIsrDepth());
        } else if (isr.underReplicatedPeakCount() > 0) {
            score = Math.min(80, isr.underReplicatedPeakCount() * 10);
            factors.add("Step '" + step.stepName() + "': " + isr.underReplicatedPeakCount()
                    + " under-replicated partitions");
        }

        if (isr.timeToFullIsr() != null && isr.timeToFullIsr().toSeconds() > 120) {
            score = Math.min(100, score + 20);
            factors.add("Step '" + step.stepName() + "': ISR recovery took "
                    + isr.timeToFullIsr().toSeconds() + "s");
        }

        if (!isr.recoveredFully()) {
            score = 100;
            factors.add("Step '" + step.stepName() + "': ISR never fully recovered");
        }
        return score;
    }

    private int scoreLag(DisruptionReport.StepReport step, List<String> factors) {
        if (step.lagMetrics() == null) return 0;
        LagSnapshot.Metrics lag = step.lagMetrics();

        long spike = lag.lagSpike();
        if (spike <= 0) return 0;

        int score;
        if (spike > 1_000_000) {
            score = 100;
            factors.add("Step '" + step.stepName() + "': consumer lag spiked by " + spike + " (>1M)");
        } else if (spike > 100_000) {
            score = 70;
            factors.add("Step '" + step.stepName() + "': consumer lag spiked by " + spike);
        } else if (spike > 10_000) {
            score = 40;
            factors.add("Step '" + step.stepName() + "': consumer lag spiked by " + spike);
        } else {
            score = (int) (spike / 10_000.0 * 20);
        }

        if (!lag.recoveredFully()) {
            score = Math.min(100, score + 30);
            factors.add("Step '" + step.stepName() + "': consumer lag never recovered");
        }
        return score;
    }

    private static int weightedAverage(int availability, int latency, int throughput, int replication, int lag) {
        return (int) (availability * 0.30 + latency * 0.20 + throughput * 0.20 + replication * 0.20 + lag * 0.10);
    }

    private static String classifySeverity(int score) {
        if (score >= 80) return "CRITICAL";
        if (score >= 60) return "HIGH";
        if (score >= 40) return "MEDIUM";
        if (score >= 20) return "LOW";
        return "MINIMAL";
    }

    private static double round(double v) {
        return Math.round(v * 100.0) / 100.0;
    }
}
