package com.bmscomp.kates.disruption;

import java.util.*;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

import io.fabric8.kubernetes.api.model.Pod;
import io.fabric8.kubernetes.client.KubernetesClient;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.eclipse.microprofile.faulttolerance.Retry;
import org.eclipse.microprofile.faulttolerance.Timeout;
import org.jboss.logging.Logger;

import com.bmscomp.kates.chaos.DisruptionType;
import com.bmscomp.kates.chaos.FaultSpec;
import com.bmscomp.kates.chaos.ParsedLabelSelector;
import com.bmscomp.kates.chaos.PodTargets;

/**
 * Safety layer for disruption tests. Validates blast radius, performs dry-run
 * simulations, and handles automatic rollback of faults on timeout.
 */
@ApplicationScoped
public class DisruptionSafetyGuard {

    private static final Logger LOG = Logger.getLogger(DisruptionSafetyGuard.class);

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
            List<String> warnings) {}

    public record DryRunResult(
            boolean wouldSucceed,
            int totalBrokers,
            List<StepPreview> steps,
            List<String> warnings,
            List<String> errors) {}

    /**
     * A step's fault as the orchestrator will run it. A leader-aware step
     * ({@code targetTopic} set) hits the partition's leader: the orchestrator
     * looks it up when the step starts and sets {@code targetBrokerId} to it.
     * Checking the spec as posted counted, and previewed, the broker of
     * whatever {@code targetBrokerId} it carried instead. The leader can move
     * before the step runs, so this is the best estimate available now. A
     * failed lookup leaves the spec as posted, as it does in the orchestrator.
     */
    private record TargetedStep(
            DisruptionPlan.DisruptionStep step, FaultSpec spec, Integer leader, boolean lookupFailed) {}

    private List<TargetedStep> targeted(DisruptionPlan plan) {
        List<TargetedStep> targeted = new ArrayList<>();
        for (DisruptionPlan.DisruptionStep step : plan.getSteps()) {
            FaultSpec spec = step.faultSpec();
            if (spec.targetTopic() == null || spec.targetTopic().isEmpty()) {
                targeted.add(new TargetedStep(step, spec, null, false));
                continue;
            }
            int leaderId = intelligence.resolveLeaderBrokerId(spec.targetTopic(), spec.targetPartition());
            targeted.add(
                    leaderId >= 0
                            ? new TargetedStep(
                                    step,
                                    spec.toBuilder().targetBrokerId(leaderId).build(),
                                    leaderId,
                                    false)
                            : new TargetedStep(step, spec, null, true));
        }
        return targeted;
    }

    /**
     * Validates a disruption plan against safety constraints before execution.
     */
    public ValidationResult validatePlan(DisruptionPlan plan) {
        return validatePlan(plan, targeted(plan));
    }

    private ValidationResult validatePlan(DisruptionPlan plan, List<TargetedStep> targeted) {
        List<String> warnings = new ArrayList<>();
        List<String> errors = new ArrayList<>();

        // Not a safety issue, but this runs before any fault (and on dry runs),
        // which is when a gate that can never fire is worth pointing out.
        SlaGrader.unevaluableConstraints(plan.getSla())
                .forEach(c -> warnings.add("SLA " + c + ". It will be reported as not evaluated, not as passed."));

        List<Pod> brokerPods = listBrokerPods();
        int totalBrokers = brokerPods.size();

        if (totalBrokers == 0) {
            errors.add(
                    "No broker pods found matching label '" + kafkaLabel + "' in namespace '" + kafkaNamespace + "'");
            return ValidationResult.rejected(warnings, errors);
        }

        Set<String> affectedBrokers = new HashSet<>();

        for (TargetedStep t : targeted) {
            DisruptionPlan.DisruptionStep step = t.step();
            FaultSpec spec = t.spec();

            try {
                List<String> hit = affectedBrokers(spec, brokerPods);
                // A rolling restart takes its brokers down one at a time.
                affectedBrokers.addAll(
                        spec.disruptionType() == DisruptionType.ROLLING_RESTART && !hit.isEmpty()
                                ? hit.subList(0, 1)
                                : hit);
            } catch (IllegalArgumentException e) {
                errors.add("Step '" + step.name() + "': " + e.getMessage());
            }

            if (spec.disruptionType() == DisruptionType.SCALE_DOWN) {
                warnings.add("Step '" + step.name()
                        + "': SCALE_DOWN reduces StatefulSet replicas — autoRollback recommended");
            }

            if (spec.disruptionType() == DisruptionType.NETWORK_PARTITION && spec.chaosDurationSec() <= 0) {
                warnings.add("Step '" + step.name()
                        + "': NETWORK_PARTITION without duration — NetworkPolicy will persist until cleanup");
            }
        }

        if (plan.getMaxAffectedBrokers() > 0 && affectedBrokers.size() > plan.getMaxAffectedBrokers()) {
            errors.add("Plan would affect " + affectedBrokers.size() + " brokers but maxAffectedBrokers="
                    + plan.getMaxAffectedBrokers());
        }

        int remainingBrokers = totalBrokers - affectedBrokers.size();
        if (remainingBrokers < 1) {
            errors.add("Plan would affect ALL " + totalBrokers + " brokers — cluster would lose availability");
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

        List<TargetedStep> targeted = targeted(plan);
        ValidationResult validation = validatePlan(plan, targeted);
        warnings.addAll(validation.warnings());
        errors.addAll(validation.errors());

        for (TargetedStep t : targeted) {
            DisruptionPlan.DisruptionStep step = t.step();
            FaultSpec spec = t.spec();
            List<String> stepWarnings = new ArrayList<>();
            List<String> affected = new ArrayList<>();
            Integer resolvedLeader = t.leader();

            if (t.lookupFailed()) {
                stepWarnings.add("Could not resolve leader for " + spec.targetTopic() + "-" + spec.targetPartition());
            }

            String targetPod = null;
            try {
                List<String> hit = affectedBrokers(spec, brokerPods);
                if (hit.isEmpty()) {
                    stepWarnings.add("targetLabel '" + spec.targetLabel() + "' matches no broker pod in namespace '"
                            + kafkaNamespace + "' — this step disrupts no broker");
                } else {
                    targetPod = String.join(",", hit);
                    affected.addAll(hit);
                }
            } catch (IllegalArgumentException e) {
                stepWarnings.add(e.getMessage());
            }

            if (spec.disruptionType() == DisruptionType.SCALE_DOWN) {
                brokerPods.forEach(p -> affected.add(p.getMetadata().getName()));
            }

            if (spec.disruptionType() == DisruptionType.ROLLING_RESTART && spec.chaosDurationSec() <= 0) {
                stepWarnings.add("chaosDurationSec is 0 — the step does not wait for the Cluster Operator to"
                        + " finish the roll, so the observation window overlaps it");
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

        return new DryRunResult(errors.isEmpty(), totalBrokers, stepPreviews, warnings, errors);
    }

    /**
     * Verifies the cluster state by checking if all broker pods are running and ready.
     * Use before/after chaos injection to ensure a stable baseline.
     */
    @Retry(maxRetries = 3, delay = 2000)
    @Timeout(10000)
    public boolean verifyClusterState() {
        List<Pod> brokers = listBrokerPods();
        if (brokers.isEmpty()) {
            LOG.warn("Cluster state verification failed: no brokers found");
            return false;
        }

        for (Pod pod : brokers) {
            if (pod.getStatus() == null || !"Running".equals(pod.getStatus().getPhase())) {
                LOG.warn("Cluster state verification failed: broker "
                        + pod.getMetadata().getName() + " is not Running");
                return false;
            }
            boolean ready = pod.getStatus().getConditions().stream()
                    .anyMatch(c -> "Ready".equals(c.getType()) && "True".equals(c.getStatus()));
            if (!ready) {
                LOG.warn("Cluster state verification failed: broker "
                        + pod.getMetadata().getName() + " is not Ready");
                return false;
            }
        }
        return true;
    }

    /**
     * Rolls back a fault injection. Called when recovery times out.
     */
    @Retry(maxRetries = 3, delay = 2000)
    public void rollback(FaultSpec spec, String engineName) {
        if (spec.disruptionType() == null) return;

        try {
            switch (spec.disruptionType()) {
                case NETWORK_PARTITION -> {
                    LOG.info("ROLLBACK: removing Kates-managed NetworkPolicies");
                    kubeClient
                            .network()
                            .networkPolicies()
                            .inNamespace(spec.targetNamespace())
                            .withLabel("managed-by", "kates")
                            .delete();
                }
                case SCALE_DOWN -> {
                    LOG.info("ROLLBACK: restoring StatefulSet replica count");
                    restoreReplicaCount(spec);
                }
                default ->
                    LOG.info("ROLLBACK: no explicit rollback needed for " + spec.disruptionType()
                            + " — StatefulSet controller handles recovery");
            }
        } catch (Exception e) {
            LOG.error("Rollback failed for " + spec.disruptionType(), e);
        }
    }

    @Retry(maxRetries = 3, delay = 2000)
    void restoreReplicaCount(FaultSpec spec) {
        kubeClient
                .apps()
                .statefulSets()
                .inNamespace(spec.targetNamespace())
                .withLabelSelector(ParsedLabelSelector.parse(spec.targetLabel()).toString())
                .list()
                .getItems()
                .forEach(ss -> {
                    String name = ss.getMetadata().getName();
                    Map<String, String> annotations = ss.getMetadata().getAnnotations();
                    String snapshot = annotations != null
                            ? annotations.get(
                                    com.bmscomp.kates.chaos.KubernetesChaosProvider.ORIGINAL_REPLICAS_ANNOTATION)
                            : null;
                    int current = ss.getSpec().getReplicas();
                    // Prefer the snapshot stamped at scale-down. status/spec.replicas
                    // both reflect the reduced count at rollback time, so falling back
                    // to them can only restore to the reduced value — the original bug.
                    int desired;
                    if (snapshot != null) {
                        try {
                            desired = Integer.parseInt(snapshot);
                        } catch (NumberFormatException e) {
                            desired = current;
                        }
                    } else {
                        desired = ss.getStatus() != null && ss.getStatus().getReplicas() != null
                                ? ss.getStatus().getReplicas()
                                : current;
                    }
                    if (current < desired) {
                        LOG.info("Restoring " + name + " from " + current + " → " + desired);
                        kubeClient
                                .apps()
                                .statefulSets()
                                .inNamespace(spec.targetNamespace())
                                .withName(name)
                                .scale(desired);
                    }
                    // Clear the snapshot so a later scale-down re-captures a fresh
                    // baseline instead of restoring to a stale count.
                    if (snapshot != null) {
                        kubeClient
                                .apps()
                                .statefulSets()
                                .inNamespace(spec.targetNamespace())
                                .withName(name)
                                .edit(s -> new io.fabric8.kubernetes.api.model.apps.StatefulSetBuilder(s)
                                        .editMetadata()
                                        .removeFromAnnotations(
                                                com.bmscomp.kates.chaos.KubernetesChaosProvider
                                                        .ORIGINAL_REPLICAS_ANNOTATION)
                                        .removeFromAnnotations(
                                                com.bmscomp.kates.chaos.KubernetesChaosProvider
                                                        .SCALED_DOWN_AT_ANNOTATION)
                                        .endMetadata()
                                        .build());
                    }
                });
    }

    @Retry(maxRetries = 3, delay = 2000)
    List<Pod> listBrokerPods() {
        try {
            return kubeClient
                    .pods()
                    .inNamespace(kafkaNamespace)
                    .withLabelSelector(ParsedLabelSelector.parse(kafkaLabel).toString())
                    .list()
                    .getItems();
        } catch (Exception e) {
            LOG.warn("Failed to list broker pods", e);
            return List.of();
        }
    }

    /**
     * The broker pods a step's fault would hit, chosen by the same rules the
     * chaos backends use ({@link PodTargets}) among the brokers its selector
     * matches, so a {@code targetAll} step counts every one of them. A random
     * pick is counted as one broker, marked since the actual pod is not known yet.
     *
     * @throws IllegalArgumentException when {@code targetLabel} is not a valid selector
     */
    List<String> affectedBrokers(FaultSpec spec, List<Pod> brokerPods) {
        PodTargets.Mode mode = PodTargets.mode(spec);
        if (mode == PodTargets.Mode.NAMED_POD) {
            return List.of(spec.targetPod());
        }
        if (spec.targetNamespace() != null && !spec.targetNamespace().equals(kafkaNamespace)) {
            return List.of();
        }
        List<Pod> matching = brokerPods;
        if (spec.targetLabel() != null && !spec.targetLabel().isBlank()) {
            ParsedLabelSelector selector = ParsedLabelSelector.parse(spec.targetLabel());
            matching = brokerPods.stream()
                    .filter(p -> selector.matches(p.getMetadata().getLabels()))
                    .toList();
        }
        if (mode == PodTargets.Mode.ONE_RANDOM) {
            return matching.isEmpty()
                    ? List.of()
                    : List.of(matching.getFirst().getMetadata().getName() + " (random selection)");
        }
        return PodTargets.select(spec, matching);
    }

    @Retry(maxRetries = 2, delay = 1000)
    boolean checkRbacPermissions(FaultSpec spec) {
        if (spec.disruptionType() == null) return true;

        try {
            return switch (spec.disruptionType()) {
                case POD_KILL, POD_DELETE, LEADER_ELECTION ->
                    kubeClient
                            .authorization()
                            .v1()
                            .selfSubjectAccessReview()
                            .create(
                                    new io.fabric8.kubernetes.api.model.authorization.v1
                                                    .SelfSubjectAccessReviewBuilder()
                                            .withNewSpec()
                                            .withNewResourceAttributes()
                                            .withNamespace(spec.targetNamespace())
                                            .withVerb("delete")
                                            .withResource("pods")
                                            .endResourceAttributes()
                                            .endSpec()
                                            .build())
                            .getStatus()
                            .getAllowed();
                case NETWORK_PARTITION, NETWORK_LATENCY ->
                    kubeClient
                            .authorization()
                            .v1()
                            .selfSubjectAccessReview()
                            .create(
                                    new io.fabric8.kubernetes.api.model.authorization.v1
                                                    .SelfSubjectAccessReviewBuilder()
                                            .withNewSpec()
                                            .withNewResourceAttributes()
                                            .withNamespace(spec.targetNamespace())
                                            .withVerb("create")
                                            .withGroup("networking.k8s.io")
                                            .withResource("networkpolicies")
                                            .endResourceAttributes()
                                            .endSpec()
                                            .build())
                            .getStatus()
                            .getAllowed();
                // Annotates the pods for the Strimzi Cluster Operator to roll.
                case ROLLING_RESTART ->
                    kubeClient
                            .authorization()
                            .v1()
                            .selfSubjectAccessReview()
                            .create(
                                    new io.fabric8.kubernetes.api.model.authorization.v1
                                                    .SelfSubjectAccessReviewBuilder()
                                            .withNewSpec()
                                            .withNewResourceAttributes()
                                            .withNamespace(spec.targetNamespace())
                                            .withVerb("patch")
                                            .withResource("pods")
                                            .endResourceAttributes()
                                            .endSpec()
                                            .build())
                            .getStatus()
                            .getAllowed();
                case SCALE_DOWN ->
                    kubeClient
                            .authorization()
                            .v1()
                            .selfSubjectAccessReview()
                            .create(
                                    new io.fabric8.kubernetes.api.model.authorization.v1
                                                    .SelfSubjectAccessReviewBuilder()
                                            .withNewSpec()
                                            .withNewResourceAttributes()
                                            .withNamespace(spec.targetNamespace())
                                            // edit() and rolling().restart() both PATCH
                                            .withVerb("patch")
                                            .withGroup("apps")
                                            .withResource("statefulsets")
                                            .endResourceAttributes()
                                            .endSpec()
                                            .build())
                            .getStatus()
                            .getAllowed();
                default -> true;
            };
        } catch (Exception e) {
            LOG.debug("RBAC check failed, assuming permitted", e);
            return true;
        }
    }
}
