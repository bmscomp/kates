package com.klster.kates.disruption;

import com.klster.kates.chaos.*;
import com.klster.kates.report.ReportSummary;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import org.eclipse.microprofile.config.inject.ConfigProperty;

import java.time.Duration;
import java.time.Instant;
import java.util.*;
import java.util.concurrent.TimeUnit;
import java.util.logging.Level;
import java.util.logging.Logger;

/**
 * Orchestrates multi-step disruption plans with:
 * - Kafka-aware intelligence (leader targeting, ISR/lag tracking)
 * - Safety guardrails (validation, auto-rollback)
 * - Prometheus metrics capture (before/after snapshots)
 * - SLA grading (per-step and overall verdict)
 */
@ApplicationScoped
public class DisruptionOrchestrator {

    private static final Logger LOG = Logger.getLogger(DisruptionOrchestrator.class.getName());

    @Inject
    ChaosCoordinator chaosCoordinator;

    @Inject
    K8sPodWatcher podWatcher;

    @Inject
    StrimziStateTracker strimziTracker;

    @Inject
    KafkaIntelligenceService intelligence;

    @Inject
    DisruptionSafetyGuard safetyGuard;

    @Inject
    PrometheusMetricsCapture prometheusCapture;

    @Inject
    SlaGrader slaGrader;

    @Inject
    DisruptionEventBus eventBus;

    @ConfigProperty(name = "kates.chaos.kafka.namespace", defaultValue = "kafka")
    String kafkaNamespace;

    @ConfigProperty(name = "kates.chaos.kafka.cluster", defaultValue = "krafter")
    String kafkaCluster;

    @ConfigProperty(name = "kates.chaos.kafka.label", defaultValue = "strimzi.io/component-type=kafka")
    String kafkaLabel;

    @ConfigProperty(name = "kates.chaos.recovery.timeout-sec", defaultValue = "300")
    int recoveryTimeoutSec;

    public DisruptionReport execute(DisruptionPlan plan) {
        String disruptionId = plan.getName();
        DisruptionReport report = new DisruptionReport();
        report.setPlanName(plan.getName());
        eventBus.emit(disruptionId, DisruptionEventBus.EventType.STARTED, null,
                "Disruption plan '" + plan.getName() + "' started");

        DisruptionSafetyGuard.ValidationResult validation = safetyGuard.validatePlan(plan);
        if (!validation.warnings().isEmpty()) {
            report.setValidationWarnings(validation.warnings());
            validation.warnings().forEach(w -> LOG.warning("Safety warning: " + w));
        }

        if (!validation.safe()) {
            LOG.severe("Plan REJECTED by safety guard: " + validation.errors());
            report.setStatus("REJECTED");
            List<String> combined = new ArrayList<>(validation.warnings());
            combined.addAll(validation.errors().stream().map(e -> "ERROR: " + e).toList());
            report.setValidationWarnings(combined);
            return report;
        }

        boolean prometheusAvailable = prometheusCapture.isAvailable();
        if (!prometheusAvailable) {
            LOG.warning("Prometheus not available — metrics capture disabled");
        }

        List<DisruptionReport.StepReport> stepReports = new ArrayList<>();
        int passedSteps = 0;
        Duration worstRecovery = Duration.ZERO;
        Duration worstIsrRecovery = Duration.ZERO;
        long peakConsumerLag = 0;
        double totalThroughputDelta = 0;
        double maxP99Spike = 0;

        try {
            for (DisruptionPlan.DisruptionStep step : plan.getSteps()) {
                LOG.info("Executing disruption step: " + step.name());
                eventBus.emit(disruptionId, DisruptionEventBus.EventType.STEP_STARTED,
                        step.name(), "Step started: " + step.name());

                DisruptionReport.StepReport stepReport = executeStep(step, plan, prometheusAvailable);
                stepReports.add(stepReport);

                if (stepReport.chaosOutcome() != null && stepReport.chaosOutcome().isPass()) {
                    passedSteps++;
                }
                eventBus.emit(disruptionId, DisruptionEventBus.EventType.STEP_COMPLETED,
                        step.name(), "Step completed: " + step.name());

                if (stepReport.timeToAllReady() != null
                        && stepReport.timeToAllReady().compareTo(worstRecovery) > 0) {
                    worstRecovery = stepReport.timeToAllReady();
                }

                if (stepReport.isrMetrics() != null && stepReport.isrMetrics().timeToFullIsr() != null
                        && stepReport.isrMetrics().timeToFullIsr().compareTo(worstIsrRecovery) > 0) {
                    worstIsrRecovery = stepReport.isrMetrics().timeToFullIsr();
                }

                if (stepReport.lagMetrics() != null && stepReport.lagMetrics().peakLag() > peakConsumerLag) {
                    peakConsumerLag = stepReport.lagMetrics().peakLag();
                }

                if (stepReport.impactDeltas() != null) {
                    Double tpDelta = stepReport.impactDeltas().get("throughputRecPerSec");
                    if (tpDelta != null) totalThroughputDelta += tpDelta;
                    Double p99Delta = stepReport.impactDeltas().get("p99LatencyMs");
                    if (p99Delta != null && p99Delta > maxP99Spike) maxP99Spike = p99Delta;
                }
            }

            int totalSteps = plan.getSteps().size();
            double avgThroughputDeg = totalSteps > 0 ? totalThroughputDelta / totalSteps : 0;

            report.setStepReports(stepReports);

            boolean slaViolated = false;
            if (plan.getSla() != null && plan.getSla().hasConstraints()) {
                SlaGrader.SlaVerdict verdict = slaGrader.grade(report, plan.getSla());
                report.setSlaVerdict(verdict);
                slaViolated = verdict.violated();
                eventBus.emit(disruptionId, DisruptionEventBus.EventType.SLA_GRADED, null,
                        "SLA grade: " + verdict.grade(), verdict);
            }

            report.setSummary(new DisruptionReport.DisruptionSummary(
                    totalSteps, passedSteps, worstRecovery,
                    avgThroughputDeg, maxP99Spike, slaViolated,
                    worstIsrRecovery, peakConsumerLag));
            report.setStatus(passedSteps == totalSteps ? "COMPLETED" : "PARTIAL");
            eventBus.emit(disruptionId, DisruptionEventBus.EventType.COMPLETED, null,
                    "Plan completed: " + report.getStatus());

        } catch (Exception e) {
            LOG.log(Level.SEVERE, "Disruption plan failed", e);
            report.setStepReports(stepReports);
            report.setStatus("FAILED");
            eventBus.emit(disruptionId, DisruptionEventBus.EventType.FAILED, null,
                    "Plan failed: " + e.getMessage());
        }

        return report;
    }

    private DisruptionReport.StepReport executeStep(
            DisruptionPlan.DisruptionStep step, DisruptionPlan plan, boolean prometheusAvailable) {

        K8sPodWatcher.WatchSession session = null;
        KafkaIntelligenceService.IsrTracker isrTracker = null;
        KafkaIntelligenceService.LagTracker lagTracker = null;
        boolean rolledBack = false;
        String rollbackReason = null;

        try {
            FaultSpec spec = step.faultSpec();
            Integer targetedLeader = null;

            if (spec.targetTopic() != null && !spec.targetTopic().isEmpty()) {
                int leaderId = intelligence.resolveLeaderBrokerId(
                        spec.targetTopic(), spec.targetPartition());
                if (leaderId >= 0) {
                    targetedLeader = leaderId;
                    LOG.info("  Auto-targeting leader broker " + leaderId
                            + " for " + spec.targetTopic() + "-" + spec.targetPartition());
                    spec = FaultSpec.builder(spec.experimentName())
                            .targetNamespace(spec.targetNamespace())
                            .targetLabel(spec.targetLabel())
                            .targetPod(spec.targetPod())
                            .chaosDurationSec(spec.chaosDurationSec())
                            .delayBeforeSec(spec.delayBeforeSec())
                            .envOverrides(spec.envOverrides())
                            .disruptionType(spec.disruptionType())
                            .targetBrokerId(leaderId)
                            .networkLatencyMs(spec.networkLatencyMs())
                            .fillPercentage(spec.fillPercentage())
                            .cpuCores(spec.cpuCores())
                            .gracePeriodSec(spec.gracePeriodSec())
                            .targetTopic(spec.targetTopic())
                            .targetPartition(spec.targetPartition())
                            .build();
                }
            }

            if (plan.getIsrTrackingTopic() != null && !plan.getIsrTrackingTopic().isEmpty()) {
                isrTracker = intelligence.startIsrTracking(
                        plan.getIsrTrackingTopic(), plan.getIsrPollIntervalMs());
            }
            if (plan.getLagTrackingGroupId() != null && !plan.getLagTrackingGroupId().isEmpty()) {
                lagTracker = intelligence.startLagTracking(
                        plan.getLagTrackingGroupId(), plan.getLagPollIntervalMs());
            }

            session = podWatcher.startWatching(kafkaNamespace, kafkaLabel);

            if (step.steadyStateSec() > 0) {
                LOG.info("  Waiting " + step.steadyStateSec() + "s for steady state");
                Thread.sleep(step.steadyStateSec() * 1000L);
            }

            ReportSummary preMetrics = null;
            PrometheusMetricsCapture.MetricsSnapshot baseline = null;
            if (prometheusAvailable && step.steadyStateSec() > 0) {
                baseline = prometheusCapture.capture(Duration.ofSeconds(step.steadyStateSec()));
                preMetrics = prometheusCapture.toReportSummary(baseline,
                        Duration.ofSeconds(step.steadyStateSec()));
                LOG.info("  Captured baseline Prometheus snapshot");
                eventBus.emit(plan.getName(), DisruptionEventBus.EventType.METRICS_BASELINE,
                        step.name(), "Baseline metrics captured");
            }

            session.markDisruptionStart();
            if (isrTracker != null) isrTracker.markDisruptionStart();
            if (lagTracker != null) lagTracker.markDisruptionStart();
            Instant disruptionStart = Instant.now();

            LOG.info("  Injecting fault: " + spec.experimentName());
            eventBus.emit(plan.getName(), DisruptionEventBus.EventType.FAULT_INJECTED,
                    step.name(), "Fault injected: " + spec.experimentName());
            ChaosOutcome outcome = chaosCoordinator.triggerFault(spec)
                    .get(spec.chaosDurationSec() + 120, TimeUnit.SECONDS);

            if (step.observationWindowSec() > 0) {
                LOG.info("  Observing for " + step.observationWindowSec() + "s");
                Thread.sleep(step.observationWindowSec() * 1000L);
            }

            ReportSummary postMetrics = null;
            Map<String, Double> deltas = new LinkedHashMap<>();
            if (prometheusAvailable && step.observationWindowSec() > 0) {
                PrometheusMetricsCapture.MetricsSnapshot impact =
                        prometheusCapture.capture(Duration.ofSeconds(step.observationWindowSec()));
                postMetrics = prometheusCapture.toReportSummary(impact,
                        Duration.ofSeconds(step.observationWindowSec()));
                if (baseline != null) {
                    deltas = prometheusCapture.computeDeltas(baseline, impact);
                }
                LOG.info("  Captured post-disruption Prometheus snapshot");
                eventBus.emit(plan.getName(), DisruptionEventBus.EventType.METRICS_CAPTURED,
                        step.name(), "Post-disruption metrics captured");
            }

            Duration tfr = null;
            Duration tar = null;
            if (step.requireRecovery()) {
                LOG.info("  Waiting for recovery (timeout=" + recoveryTimeoutSec + "s)");
                eventBus.emit(plan.getName(), DisruptionEventBus.EventType.RECOVERY_WAITING,
                        step.name(), "Waiting for recovery (timeout=" + recoveryTimeoutSec + "s)");
                boolean recovered = session.awaitFirstReady(recoveryTimeoutSec, TimeUnit.SECONDS);

                if (!recovered && plan.isAutoRollback()) {
                    LOG.warning("  Recovery timeout — triggering auto-rollback for " + step.name());
                    safetyGuard.rollback(spec, outcome.engineName());
                    eventBus.emit(plan.getName(), DisruptionEventBus.EventType.ROLLBACK,
                            step.name(), "Auto-rollback triggered for " + step.name());
                    rolledBack = true;
                    rollbackReason = "Recovery timeout exceeded " + recoveryTimeoutSec + "s";
                    session.awaitFirstReady(60, TimeUnit.SECONDS);
                }

                K8sPodWatcher.RecoveryMetrics recovery = session.computeRecovery();
                tfr = recovery.timeToFirstReady();
                tar = recovery.timeToAllReady();
            }

            Duration strimziRecovery = strimziTracker.measureRecoveryTime(
                    kafkaNamespace, kafkaCluster, disruptionStart,
                    Duration.ofSeconds(recoveryTimeoutSec));

            IsrSnapshot.Metrics isrMetrics = isrTracker != null ? isrTracker.stop() : null;
            LagSnapshot.Metrics lagMetrics = lagTracker != null ? lagTracker.stop() : null;

            return new DisruptionReport.StepReport(
                    step.name(), spec.disruptionType(), outcome,
                    session.getEvents(), tfr, tar, strimziRecovery,
                    preMetrics, postMetrics, deltas,
                    targetedLeader, isrMetrics, lagMetrics,
                    rolledBack, rollbackReason);

        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            return failedStep(step, "Interrupted", rolledBack, rollbackReason);
        } catch (Exception e) {
            LOG.log(Level.SEVERE, "Step '" + step.name() + "' failed", e);

            if (plan.isAutoRollback()) {
                try {
                    LOG.warning("  Auto-rollback on exception for step " + step.name());
                    safetyGuard.rollback(step.faultSpec(), "");
                    rolledBack = true;
                    rollbackReason = "Exception: " + e.getMessage();
                } catch (Exception re) {
                    LOG.log(Level.SEVERE, "Rollback also failed", re);
                }
            }

            return failedStep(step, e.getMessage(), rolledBack, rollbackReason);
        } finally {
            if (session != null) session.close();
            if (isrTracker != null) isrTracker.stop();
            if (lagTracker != null) lagTracker.stop();
        }
    }

    private DisruptionReport.StepReport failedStep(
            DisruptionPlan.DisruptionStep step, String reason,
            boolean rolledBack, String rollbackReason) {
        Instant now = Instant.now();
        return new DisruptionReport.StepReport(
                step.name(),
                step.faultSpec().disruptionType(),
                ChaosOutcome.failure("none", step.faultSpec().experimentName(),
                        now, now, System.nanoTime(), reason),
                List.of(), null, null, null, null, null, Map.of(),
                null, null, null,
                rolledBack, rollbackReason);
    }
}
