package com.klster.kates.disruption;

import com.klster.kates.chaos.DisruptionType;
import com.klster.kates.chaos.FaultSpec;
import io.fabric8.kubernetes.api.model.Pod;
import io.fabric8.kubernetes.client.KubernetesClient;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import org.eclipse.microprofile.config.inject.ConfigProperty;

import java.util.*;
import java.util.logging.Level;
import java.util.logging.Logger;

/**
 * Safety layer for disruption tests. Validates blast radius, performs dry-run
 * simulations, and handles automatic rollback of faults on timeout.
 */
@ApplicationScoped
public class DisruptionSafetyGuard {

    private static final Logger LOG = Logger.getLogger(DisruptionSafetyGuard.class.getName());

    @Inject
    KubernetesClient kubeClient;

    @Inject
    KafkaIntelligenceService intelligence;

    @ConfigProperty(name = "kates.chaos.kafka.namespace", defaultValue = "kafka")
    String kafkaNamespace;

    @ConfigProperty(name = "kates.chaos.kafka.label", defaultValue = "strimzi.io/component-type=kafka")
    String kafkaLabel;

    public record ValidationResult(boolean safe, List<String> warnings, List<String> errors) {
        public static ValidationResult ok(List<String> warnings) {
            return new ValidationResult(true, warnings, List.of());
        }

        public static ValidationResult rejected(List<String> warnings, List<String> errors) {
            return new ValidationResult(false, warnings, errors);
        }
    }

    public record StepPreview(
            String name,
            String disruptionType,
            String targetPod,
            Integer resolvedLeaderId,
            List<String> affectedPods,
            List<String> warnings
    ) {}

    public record DryRunResult(
            boolean wouldSucceed,
            int totalBrokers,
            List<StepPreview> steps,
            List<String> warnings,
            List<String> errors
    ) {}

    /**
     * Validates a disruption plan against safety constraints before execution.
     */
    public ValidationResult validatePlan(DisruptionPlan plan) {
        List<String> warnings = new ArrayList<>();
        List<String> errors = new ArrayList<>();

        List<Pod> brokerPods = listBrokerPods();
        int totalBrokers = brokerPods.size();

        if (totalBrokers == 0) {
            errors.add("No broker pods found matching label '" + kafkaLabel
                    + "' in namespace '" + kafkaNamespace + "'");
            return ValidationResult.rejected(warnings, errors);
        }

        Set<String> affectedBrokers = new HashSet<>();

        for (DisruptionPlan.DisruptionStep step : plan.getSteps()) {
            FaultSpec spec = step.faultSpec();

            String resolved = resolveTargetPod(spec, brokerPods);
            if (resolved != null) {
                affectedBrokers.add(resolved);
            }

            if (spec.disruptionType() == DisruptionType.SCALE_DOWN) {
                warnings.add("Step '" + step.name()
                        + "': SCALE_DOWN reduces StatefulSet replicas — autoRollback recommended");
            }

            if (spec.disruptionType() == DisruptionType.NETWORK_PARTITION
                    && spec.chaosDurationSec() <= 0) {
                warnings.add("Step '" + step.name()
                        + "': NETWORK_PARTITION without duration — NetworkPolicy will persist until cleanup");
            }
        }

        if (plan.getMaxAffectedBrokers() > 0
                && affectedBrokers.size() > plan.getMaxAffectedBrokers()) {
            errors.add("Plan would affect " + affectedBrokers.size()
                    + " brokers but maxAffectedBrokers=" + plan.getMaxAffectedBrokers());
        }

        int remainingBrokers = totalBrokers - affectedBrokers.size();
        if (remainingBrokers < 1) {
            errors.add("Plan would affect ALL " + totalBrokers
                    + " brokers — cluster would lose availability");
        } else if (remainingBrokers == 1) {
            warnings.add("Only 1 broker would remain after disruption — high risk of data loss");
        }

        if (!errors.isEmpty()) {
            return ValidationResult.rejected(warnings, errors);
        }

        return ValidationResult.ok(warnings);
    }

    /**
     * Simulates a disruption plan without injecting any faults.
     */
    public DryRunResult dryRun(DisruptionPlan plan) {
        List<String> warnings = new ArrayList<>();
        List<String> errors = new ArrayList<>();
        List<StepPreview> stepPreviews = new ArrayList<>();

        List<Pod> brokerPods = listBrokerPods();
        int totalBrokers = brokerPods.size();

        if (totalBrokers == 0) {
            errors.add("No broker pods found");
            return new DryRunResult(false, 0, List.of(), warnings, errors);
        }

        ValidationResult validation = validatePlan(plan);
        warnings.addAll(validation.warnings());
        errors.addAll(validation.errors());

        for (DisruptionPlan.DisruptionStep step : plan.getSteps()) {
            FaultSpec spec = step.faultSpec();
            List<String> stepWarnings = new ArrayList<>();
            List<String> affected = new ArrayList<>();
            Integer resolvedLeader = null;

            if (spec.targetTopic() != null && !spec.targetTopic().isEmpty()) {
                int leaderId = intelligence.resolveLeaderBrokerId(
                        spec.targetTopic(), spec.targetPartition());
                if (leaderId >= 0) {
                    resolvedLeader = leaderId;
                } else {
                    stepWarnings.add("Could not resolve leader for "
                            + spec.targetTopic() + "-" + spec.targetPartition());
                }
            }

            String targetPod = resolveTargetPod(spec, brokerPods);
            if (targetPod != null) {
                affected.add(targetPod);
            }

            if (spec.disruptionType() == DisruptionType.ROLLING_RESTART
                    || spec.disruptionType() == DisruptionType.SCALE_DOWN) {
                brokerPods.forEach(p -> affected.add(p.getMetadata().getName()));
            }

            boolean canExecute = checkRbacPermissions(spec);
            if (!canExecute) {
                stepWarnings.add("Insufficient RBAC permissions for " + spec.disruptionType());
            }

            stepPreviews.add(new StepPreview(
                    step.name(),
                    spec.disruptionType() != null ? spec.disruptionType().name() : "unknown",
                    targetPod,
                    resolvedLeader,
                    affected,
                    stepWarnings));
        }

        return new DryRunResult(
                errors.isEmpty(),
                totalBrokers,
                stepPreviews,
                warnings,
                errors);
    }

    /**
     * Rolls back a fault injection. Called when recovery times out.
     */
    public void rollback(FaultSpec spec, String engineName) {
        if (spec.disruptionType() == null) return;

        try {
            switch (spec.disruptionType()) {
                case NETWORK_PARTITION -> {
                    LOG.info("ROLLBACK: removing Kates-managed NetworkPolicies");
                    kubeClient.network().networkPolicies()
                            .inNamespace(spec.targetNamespace())
                            .withLabel("managed-by", "kates")
                            .delete();
                }
                case SCALE_DOWN -> {
                    LOG.info("ROLLBACK: restoring StatefulSet replica count");
                    restoreReplicaCount(spec);
                }
                default -> LOG.info("ROLLBACK: no explicit rollback needed for "
                        + spec.disruptionType() + " — StatefulSet controller handles recovery");
            }
        } catch (Exception e) {
            LOG.log(Level.SEVERE, "Rollback failed for " + spec.disruptionType(), e);
        }
    }

    private void restoreReplicaCount(FaultSpec spec) {
        String[] parts = spec.targetLabel().split("=", 2);
        String labelKey = parts[0];
        String labelValue = parts.length > 1 ? parts[1] : "";

        kubeClient.apps().statefulSets()
                .inNamespace(spec.targetNamespace())
                .withLabel(labelKey, labelValue)
                .list()
                .getItems()
                .forEach(ss -> {
                    int desired = ss.getStatus() != null && ss.getStatus().getReplicas() != null
                            ? ss.getStatus().getReplicas()
                            : ss.getSpec().getReplicas();
                    int current = ss.getSpec().getReplicas();
                    if (current < desired) {
                        LOG.info("Restoring " + ss.getMetadata().getName()
                                + " from " + current + " → " + desired);
                        kubeClient.apps().statefulSets()
                                .inNamespace(spec.targetNamespace())
                                .withName(ss.getMetadata().getName())
                                .scale(desired);
                    }
                });
    }

    private List<Pod> listBrokerPods() {
        try {
            String[] parts = kafkaLabel.split("=", 2);
            String labelKey = parts[0];
            String labelValue = parts.length > 1 ? parts[1] : "";

            return kubeClient.pods()
                    .inNamespace(kafkaNamespace)
                    .withLabel(labelKey, labelValue)
                    .list()
                    .getItems();
        } catch (Exception e) {
            LOG.log(Level.WARNING, "Failed to list broker pods", e);
            return List.of();
        }
    }

    private String resolveTargetPod(FaultSpec spec, List<Pod> brokerPods) {
        if (spec.targetPod() != null && !spec.targetPod().isEmpty()) {
            return spec.targetPod();
        }
        if (spec.targetBrokerId() >= 0) {
            return brokerPods.stream()
                    .filter(p -> p.getMetadata().getName().endsWith("-" + spec.targetBrokerId()))
                    .map(p -> p.getMetadata().getName())
                    .findFirst()
                    .orElse(null);
        }
        if (!brokerPods.isEmpty()) {
            return brokerPods.getFirst().getMetadata().getName() + " (random selection)";
        }
        return null;
    }

    private boolean checkRbacPermissions(FaultSpec spec) {
        if (spec.disruptionType() == null) return true;

        try {
            return switch (spec.disruptionType()) {
                case POD_KILL, POD_DELETE, LEADER_ELECTION ->
                        kubeClient.authorization().v1().selfSubjectAccessReview()
                                .create(new io.fabric8.kubernetes.api.model.authorization.v1.SelfSubjectAccessReviewBuilder()
                                        .withNewSpec()
                                        .withNewResourceAttributes()
                                        .withNamespace(spec.targetNamespace())
                                        .withVerb("delete")
                                        .withResource("pods")
                                        .endResourceAttributes()
                                        .endSpec()
                                        .build())
                                .getStatus().getAllowed();
                case NETWORK_PARTITION, NETWORK_LATENCY ->
                        kubeClient.authorization().v1().selfSubjectAccessReview()
                                .create(new io.fabric8.kubernetes.api.model.authorization.v1.SelfSubjectAccessReviewBuilder()
                                        .withNewSpec()
                                        .withNewResourceAttributes()
                                        .withNamespace(spec.targetNamespace())
                                        .withVerb("create")
                                        .withResource("networkpolicies")
                                        .endResourceAttributes()
                                        .endSpec()
                                        .build())
                                .getStatus().getAllowed();
                case SCALE_DOWN, ROLLING_RESTART ->
                        kubeClient.authorization().v1().selfSubjectAccessReview()
                                .create(new io.fabric8.kubernetes.api.model.authorization.v1.SelfSubjectAccessReviewBuilder()
                                        .withNewSpec()
                                        .withNewResourceAttributes()
                                        .withNamespace(spec.targetNamespace())
                                        .withVerb("update")
                                        .withResource("statefulsets")
                                        .endResourceAttributes()
                                        .endSpec()
                                        .build())
                                .getStatus().getAllowed();
                default -> true;
            };
        } catch (Exception e) {
            LOG.log(Level.FINE, "RBAC check failed, assuming permitted", e);
            return true;
        }
    }
}
