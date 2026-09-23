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
import com.bmscomp.kates.chaos.ScaleDownSnapshots;
import com.bmscomp.kates.chaos.ScaleDownTargets;

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
                warnings.add("Step '" + step.name() + "': SCALE_DOWN removes a broker from each KafkaNodePool or"
                        + " StatefulSet it selects and leaves it removed — autoRollback recommended, which restores"
                        + " it when the step fails its recovery check");
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
                if (hit.isEmpty() && spec.disruptionType() == DisruptionType.SCALE_DOWN) {
                    stepWarnings.add("SCALE_DOWN removes no broker in namespace '" + kafkaNamespace + "'");
                } else if (hit.isEmpty()) {
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
                stepWarnings.addAll(scaleDownWarnings(spec, brokerPods, affected));
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
                    LOG.info("ROLLBACK: restoring scaled-down KafkaNodePools and StatefulSets");
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

    /**
     * Gives every KafkaNodePool and StatefulSet in the step's namespace that
     * still carries a scale-down snapshot its original replica count back, and
     * clears the snapshot so a later scale-down captures a fresh baseline. The
     * snapshot is what makes this work: {@code spec.replicas} only holds the
     * reduced count by now. It also finds a node pool scaled down to zero,
     * which has no pod left for the step's selector to match.
     */
    @Retry(maxRetries = 3, delay = 2000)
    void restoreReplicaCount(FaultSpec spec) {
        ScaleDownSnapshots.restore(kubeClient, spec.targetNamespace(), meta -> true);
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
     * A {@code SCALE_DOWN} step counts the broker each KafkaNodePool or
     * StatefulSet it selects will lose ({@link ScaleDownTargets}).
     *
     * @throws IllegalArgumentException when {@code targetLabel} is not a valid selector
     */
    List<String> affectedBrokers(FaultSpec spec, List<Pod> brokerPods) {
        if (spec.disruptionType() == DisruptionType.SCALE_DOWN) {
            if (spec.targetNamespace() != null && !spec.targetNamespace().equals(kafkaNamespace)) {
                return List.of();
            }
            return ScaleDownTargets.preview(spec, brokerPods).removedPods();
        }
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

    /** What the dry run says about a SCALE_DOWN step, beyond the brokers it removes. */
    private List<String> scaleDownWarnings(FaultSpec spec, List<Pod> brokerPods, List<String> removed) {
        List<String> warnings = new ArrayList<>();
        boolean nodePools = false;
        if (spec.targetNamespace() == null || spec.targetNamespace().equals(kafkaNamespace)) {
            try {
                ScaleDownTargets.Preview preview = ScaleDownTargets.preview(spec, brokerPods);
                warnings.addAll(preview.notes());
                nodePools = !preview.targets().pools().isEmpty();
            } catch (IllegalArgumentException e) {
                // A malformed selector: the step already carries the parse error.
            }
        }
        boolean namedPod = spec.targetPod() != null && !spec.targetPod().isEmpty();
        if (namedPod && !removed.isEmpty() && !removed.contains(spec.targetPod())) {
            warnings.add("targetPod " + spec.targetPod() + " only picks the node pool or StatefulSet that runs it,"
                    + " which loses its highest-numbered broker: " + String.join(",", removed) + ", not "
                    + spec.targetPod());
        }
        if (nodePools && spec.chaosDurationSec() <= 0) {
            warnings.add("chaosDurationSec is 0 — the step does not wait for the Cluster Operator, so it cannot"
                    + " tell a removed broker from a scale-down Strimzi holds back, which then stays pending"
                    + " until rollback");
        }
        return warnings;
    }

    /**
     * SCALE_DOWN patches the KafkaNodePools it selects. On StatefulSets it
     * patches the snapshot on, and sets the replicas through the scale
     * subresource.
     */
    private boolean canScaleDown(FaultSpec spec) {
        ScaleDownTargets.Targets targets;
        try {
            targets = ScaleDownTargets.resolve(kubeClient, spec);
        } catch (RuntimeException e) {
            return true; // Selects nothing to scale: the step's other warnings say so.
        }
        String namespace = spec.targetNamespace();
        return (targets.pools().isEmpty() || allowed(namespace, "patch", "kafka.strimzi.io", "kafkanodepools", null))
                && (targets.statefulSets().isEmpty()
                        || (allowed(namespace, "patch", "apps", "statefulsets", null)
                                && allowed(namespace, "update", "apps", "statefulsets", "scale")));
    }

    private boolean allowed(String namespace, String verb, String group, String resource, String subresource) {
        return kubeClient
                .authorization()
                .v1()
                .selfSubjectAccessReview()
                .create(new io.fabric8.kubernetes.api.model.authorization.v1.SelfSubjectAccessReviewBuilder()
                        .withNewSpec()
                        .withNewResourceAttributes()
                        .withNamespace(namespace)
                        .withVerb(verb)
                        .withGroup(group)
                        .withResource(resource)
                        .withSubresource(subresource)
                        .endResourceAttributes()
                        .endSpec()
                        .build())
                .getStatus()
                .getAllowed();
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
                case SCALE_DOWN -> canScaleDown(spec);
                default -> true;
            };
        } catch (Exception e) {
            LOG.debug("RBAC check failed, assuming permitted", e);
            return true;
        }
    }
}
