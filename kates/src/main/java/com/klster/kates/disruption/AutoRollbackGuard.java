package com.klster.kates.disruption;

import java.time.Duration;
import java.time.Instant;

import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

import com.klster.kates.chaos.FaultSpec;

/**
 * Monitors running disruption experiments and triggers automatic rollback
 * when safety thresholds are breached: ISR depth, consumer lag, or time-to-recover.
 */
@ApplicationScoped
public class AutoRollbackGuard {

    private static final Logger LOG = Logger.getLogger(AutoRollbackGuard.class);

    @Inject
    DisruptionSafetyGuard safetyGuard;

    @ConfigProperty(name = "kates.chaos.rollback.max-recovery-sec", defaultValue = "300")
    int maxRecoverySec;

    @ConfigProperty(name = "kates.chaos.rollback.min-isr-depth", defaultValue = "1")
    int minIsrDepth;

    @ConfigProperty(name = "kates.chaos.rollback.max-lag-spike", defaultValue = "1000000")
    long maxLagSpike;

    public record RollbackDecision(
            boolean shouldRollback,
            String reason,
            String dimension,
            String threshold,
            String observed) {}

    /**
     * Evaluates whether a step's live metrics have breached safety thresholds.
     * Called periodically during a step execution's observation window.
     */
    public RollbackDecision evaluate(
            DisruptionReport.StepReport step,
            FaultSpec spec,
            Instant stepStarted) {

        long elapsedSec = Duration.between(stepStarted, Instant.now()).toSeconds();
        if (elapsedSec > maxRecoverySec) {
            return new RollbackDecision(true,
                    "Recovery time exceeded " + maxRecoverySec + "s",
                    "availability",
                    maxRecoverySec + "s",
                    elapsedSec + "s");
        }

        if (step.isrMetrics() != null) {
            int minDepth = step.isrMetrics().minIsrDepth();
            if (minDepth < minIsrDepth) {
                return new RollbackDecision(true,
                        "ISR depth dropped below minimum threshold (" + minIsrDepth + ")",
                        "replication",
                        String.valueOf(minIsrDepth),
                        String.valueOf(minDepth));
            }
        }

        if (step.lagMetrics() != null) {
            long spike = step.lagMetrics().lagSpike();
            if (spike > maxLagSpike) {
                return new RollbackDecision(true,
                        "Consumer lag spike exceeded threshold (" + maxLagSpike + ")",
                        "consumer-lag",
                        String.valueOf(maxLagSpike),
                        String.valueOf(spike));
            }
        }

        return new RollbackDecision(false, null, null, null, null);
    }

    /**
     * Executes rollback via the safety guard and logs the event.
     */
    public void executeRollback(FaultSpec spec, String engineName, String reason) {
        LOG.warnf("AUTO-ROLLBACK triggered for experiment '%s': %s", spec.experimentName(), reason);
        try {
            safetyGuard.rollback(spec, engineName);
            LOG.infof("AUTO-ROLLBACK completed for '%s'", spec.experimentName());
        } catch (Exception e) {
            LOG.errorf("AUTO-ROLLBACK failed for '%s': %s", spec.experimentName(), e.getMessage());
        }
    }
}
