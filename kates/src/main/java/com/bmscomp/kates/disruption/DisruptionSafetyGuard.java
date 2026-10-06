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
import com.bmscomp.kates.chaos.FaultLimits;
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

    @Inject
    FaultLimits limits;

    @ConfigProperty(name = "kates.chaos.kafka.namespace", defaultValue = "kafka")
    String kafkaNamespace;

    /**
     * Selects the cluster's Kafka pods. The default matches the KRaft
     * controllers as well, so only the brokers among them are counted
     * ({@link PodTargets#isBroker}).
     */
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

    /**
     * What a step's fault hits: every pod, for the dry run to list, and the
     * brokers among them, which are what the blast radius counts.
     */
    record Impact(List<String> pods, List<String> brokers) {
        static final Impact NONE = new Impact(List.of(), List.of());
    }

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
     * Validates a disruption plan against safety constraints before execution,
     * each step's fault parameters against the chaos limits ({@link FaultLimits})
     * among them.
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

        // Whatever the cluster holds. A NETWORK_PARTITION without a duration
        // used to be only a warning here, and the kubernetes provider then
        // never removed it.
        for (TargetedStep t : targeted) {
            limits.violations(t.spec())
                    .forEach((field, why) -> errors.add("Step '" + t.step().name() + "': " + field + " " + why));
        }

        List<Pod> kafkaPods = listKafkaPods();
        int totalBrokers = brokerCount(kafkaPods);

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
                List<String> hit = impact(spec, kafkaPods).brokers();
                // A rolling restart takes its brokers down one at a time.
                affectedBrokers.addAll(
                        spec.disruptionType() == DisruptionType.ROLLING_RESTART && !hit.isEmpty()
                                ? hit.subList(0, 1)
                                : hit);
            } catch (IllegalArgumentException | io.fabric8.kubernetes.client.KubernetesClientException e) {
                errors.add("Step '" + step.name() + "': " + e.getMessage());
            }

            if (spec.disruptionType() == DisruptionType.SCALE_DOWN) {
                warnings.add("Step '" + step.name() + "': SCALE_DOWN removes a broker from each KafkaNodePool or"
                        + " StatefulSet it selects and leaves it removed — autoRollback recommended, which restores"
                        + " it when the step fails its recovery check");
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

        List<Pod> kafkaPods = listKafkaPods();
        int totalBrokers = brokerCount(kafkaPods);

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
                Drain drain = drainsANode(spec) ? drain(spec, kafkaPods) : null;
                List<String> hit = drain != null
                        ? drain.impact().pods()
                        : impact(spec, kafkaPods).pods();
                if (drain != null) {
                    targetPod = drain.target();
                    affected.addAll(hit);
                    stepWarnings.addAll(drain.notes());
                } else if (hit.isEmpty() && spec.disruptionType() == DisruptionType.SCALE_DOWN) {
                    stepWarnings.add("SCALE_DOWN removes no broker in namespace '" + kafkaNamespace + "'");
                } else if (hit.isEmpty()) {
                    stepWarnings.add("targetLabel '" + spec.targetLabel() + "' matches no broker pod in namespace '"
                            + kafkaNamespace + "' — this step disrupts no broker");
                } else {
                    targetPod = String.join(",", hit);
                    affected.addAll(hit);
                }
                if (drain == null
                        && spec.disruptionType() != DisruptionType.SCALE_DOWN
                        && PodTargets.mode(spec) == PodTargets.Mode.BROKER_ID
                        && !hit.isEmpty()
                        && !hit.getFirst().endsWith("-" + spec.targetBrokerId())) {
                    stepWarnings.add(brokerIdFallback(spec, hit.getFirst()));
                }
            } catch (IllegalArgumentException | io.fabric8.kubernetes.client.KubernetesClientException e) {
                stepWarnings.add(e.getMessage());
            }

            if (spec.disruptionType() == DisruptionType.SCALE_DOWN) {
                stepWarnings.addAll(scaleDownWarnings(spec, kafkaPods, affected));
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
     * Verifies the cluster state by checking if all Kafka pods, KRaft
     * controllers included, are running and ready.
     * Use before/after chaos injection to ensure a stable baseline.
     */
    @Retry(maxRetries = 3, delay = 2000)
    @Timeout(10000)
    public boolean verifyClusterState() {
        List<Pod> pods = listKafkaPods();
        if (pods.isEmpty()) {
            LOG.warn("Cluster state verification failed: no Kafka pods found");
            return false;
        }

        for (Pod pod : pods) {
            if (pod.getStatus() == null || !"Running".equals(pod.getStatus().getPhase())) {
                LOG.warn("Cluster state verification failed: Kafka pod "
                        + pod.getMetadata().getName() + " is not Running");
                return false;
            }
            boolean ready = pod.getStatus().getConditions().stream()
                    .anyMatch(c -> "Ready".equals(c.getType()) && "True".equals(c.getStatus()));
            if (!ready) {
                LOG.warn("Cluster state verification failed: Kafka pod "
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

    /**
     * Every pod {@code kates.chaos.kafka.label} matches: the brokers, and with
     * the default label the KRaft controllers too.
     */
    @Retry(maxRetries = 3, delay = 2000)
    List<Pod> listKafkaPods() {
        try {
            return kubeClient
                    .pods()
                    .inNamespace(kafkaNamespace)
                    .withLabelSelector(ParsedLabelSelector.parse(kafkaLabel).toString())
                    .list()
                    .getItems();
        } catch (Exception e) {
            LOG.warn("Failed to list Kafka pods", e);
            return List.of();
        }
    }

    private static int brokerCount(List<Pod> kafkaPods) {
        return (int) kafkaPods.stream().filter(PodTargets::isBroker).count();
    }

    /**
     * What a step's fault would hit, chosen by the same rules the chaos
     * backends use ({@link PodTargets}) among the Kafka pods its selector
     * matches, so a {@code targetAll} step hits every one of them. Only the
     * brokers among them count: a KRaft controller the step hits is listed,
     * not counted. A random pick is one pod, marked since the actual pod is
     * not known yet, and counts as a broker if the selector matches one. A
     * {@code SCALE_DOWN} step hits the broker each KafkaNodePool or
     * StatefulSet it selects will lose ({@link ScaleDownTargets}). A drain hits
     * every Kafka pod on the node it drains ({@link #drain}).
     *
     * @throws IllegalArgumentException when {@code targetLabel} is not a valid selector
     */
    Impact impact(FaultSpec spec, List<Pod> kafkaPods) {
        if (drainsANode(spec)) {
            return drain(spec, kafkaPods).impact();
        }
        boolean inKafkaNamespace =
                spec.targetNamespace() == null || spec.targetNamespace().equals(kafkaNamespace);
        if (spec.disruptionType() == DisruptionType.SCALE_DOWN) {
            if (!inKafkaNamespace) {
                return Impact.NONE;
            }
            List<String> removed = ScaleDownTargets.preview(spec, kafkaPods).removedPods();
            return new Impact(removed, removed);
        }
        PodTargets.Mode mode = PodTargets.mode(spec);
        if (mode == PodTargets.Mode.NAMED_POD) {
            boolean controller = inKafkaNamespace
                    && kafkaPods.stream()
                            .anyMatch(
                                    p -> p.getMetadata().getName().equals(spec.targetPod()) && !PodTargets.isBroker(p));
            return new Impact(List.of(spec.targetPod()), controller ? List.of() : List.of(spec.targetPod()));
        }
        if (!inKafkaNamespace) {
            return Impact.NONE;
        }
        List<Pod> matching = kafkaPods;
        if (spec.targetLabel() != null && !spec.targetLabel().isBlank()) {
            ParsedLabelSelector selector = ParsedLabelSelector.parse(spec.targetLabel());
            matching = kafkaPods.stream()
                    .filter(p -> selector.matches(p.getMetadata().getLabels()))
                    .toList();
        }
        if (mode == PodTargets.Mode.ONE_RANDOM) {
            if (matching.isEmpty()) {
                return Impact.NONE;
            }
            Optional<Pod> broker =
                    matching.stream().filter(PodTargets::isBroker).findFirst();
            List<String> pick =
                    List.of(broker.orElse(matching.getFirst()).getMetadata().getName() + " (random selection)");
            return new Impact(pick, broker.isPresent() ? pick : List.of());
        }
        List<String> pods = PodTargets.select(spec, matching);
        Set<String> brokers = new HashSet<>(matching.stream()
                .filter(PodTargets::isBroker)
                .map(p -> p.getMetadata().getName())
                .toList());
        return new Impact(pods, pods.stream().filter(brokers::contains).toList());
    }

    /** What the dry run warns when targetBrokerId names no broker, and the pick falls back to another. */
    private static String brokerIdFallback(FaultSpec spec, String fallback) {
        return "targetBrokerId " + spec.targetBrokerId() + " matches no broker pod, so the step falls back to "
                + fallback + ", the first broker targetLabel matches. KRaft controllers are never picked by ID";
    }

    /**
     * What a drain hits, and for the dry run the pods it picks and what it
     * says about the node: the impact counts every Kafka pod on the node.
     */
    record Drain(Impact impact, String target, List<String> notes) {}

    /** The Litmus settings that pick the node a node-drain drains. */
    private static final String TARGET_NODE = "TARGET_NODE";

    private static final String NODE_LABEL = "NODE_LABEL";

    /**
     * Whether a step drains a node: a NODE_DRAIN, or a step without a type
     * that names Litmus's node-drain, which the litmus-crd provider aims the
     * same way.
     */
    static boolean drainsANode(FaultSpec spec) {
        return spec.disruptionType() == DisruptionType.NODE_DRAIN
                || (spec.disruptionType() == null && "node-drain".equals(spec.experimentName()));
    }

    /**
     * What a drain hits: every Kafka pod on the node it drains, whatever the
     * namespace of the pod that picks it. Unless an override picks the node,
     * the litmus-crd provider drains the node of the pod {@link PodTargets}
     * picks, looked up as the provider does. A random pick, or a node Litmus
     * picks by envOverrides.NODE_LABEL, counts the node it may land on that
     * runs the most brokers. A drain that would drain nothing, such as one
     * whose pod is on no node yet, counts none and says why: its step fails
     * without draining.
     *
     * @throws IllegalArgumentException when targetLabel or NODE_LABEL is not a
     *     valid selector, or the pods a targetAll drain picks run on more than
     *     one node, which node-drain cannot drain at once
     */
    Drain drain(FaultSpec spec, List<Pod> kafkaPods) {
        Map<String, String> overrides = spec.envOverrides() != null ? spec.envOverrides() : Map.of();
        String targetNode = overrides.get(TARGET_NODE);
        String nodeLabel = overrides.get(NODE_LABEL);
        if (targetNode != null && !targetNode.isBlank()) {
            return onNode(
                    targetNode,
                    null,
                    kafkaPods,
                    "NODE_DRAIN drains node " + targetNode + ", which envOverrides.TARGET_NODE names");
        }
        if (nodeLabel != null && !nodeLabel.isBlank()) {
            ParsedLabelSelector selector = ParsedLabelSelector.parse(nodeLabel);
            List<String> nodes = kubeClient.nodes().withLabelSelector(selector.toString()).list().getItems().stream()
                    .map(n -> n.getMetadata().getName())
                    .toList();
            return nodes.isEmpty()
                    ? nothing("envOverrides.NODE_LABEL '" + nodeLabel + "' matches no node, so the step drains nothing")
                    : worstOf(
                            nodes,
                            kafkaPods,
                            "NODE_DRAIN drains a node Litmus picks by envOverrides.NODE_LABEL '" + nodeLabel + "'");
        }
        if (overrides.containsKey(TARGET_NODE) || overrides.containsKey(NODE_LABEL)) {
            return worstOf(
                    kafkaPods.stream()
                            .map(DisruptionSafetyGuard::nodeOf)
                            .filter(Objects::nonNull)
                            .distinct()
                            .toList(),
                    kafkaPods,
                    "envOverrides sets TARGET_NODE or NODE_LABEL empty, so node-drain drains the node of a random pod"
                            + " in any namespace");
        }

        String namespace = spec.targetNamespace() != null ? spec.targetNamespace() : kafkaNamespace;
        PodTargets.Mode mode = PodTargets.mode(spec);
        List<Pod> candidates;
        if (mode == PodTargets.Mode.NAMED_POD) {
            Pod pod = kubeClient
                    .pods()
                    .inNamespace(namespace)
                    .withName(spec.targetPod())
                    .get();
            if (pod == null) {
                return nothing("pod " + spec.targetPod() + " was not found in namespace '" + namespace
                        + "', so the step drains nothing");
            }
            candidates = List.of(pod);
        } else {
            ParsedLabelSelector selector = ParsedLabelSelector.parse(spec.targetLabel());
            candidates = kubeClient
                    .pods()
                    .inNamespace(namespace)
                    .withLabelSelector(selector.toString())
                    .list()
                    .getItems();
            if (candidates.isEmpty()) {
                return nothing("targetLabel '" + spec.targetLabel() + "' matches no pod in namespace '" + namespace
                        + "', so the step drains nothing");
            }
        }
        if (mode == PodTargets.Mode.ONE_RANDOM) {
            List<String> nodes = candidates.stream()
                    .map(DisruptionSafetyGuard::nodeOf)
                    .filter(Objects::nonNull)
                    .distinct()
                    .toList();
            return nodes.isEmpty()
                    ? nothing("no pod targetLabel matches is on a node yet, so the step drains nothing")
                    : worstOf(nodes, kafkaPods, "NODE_DRAIN drains the node of a pod picked at random");
        }

        List<String> picked = PodTargets.select(spec, candidates);
        if (picked.isEmpty()) {
            return nothing("targetBrokerId picks brokers only, and targetLabel matches no broker pod in namespace '"
                    + namespace + "', so the step drains nothing");
        }
        Map<String, String> nodeOfPod = new HashMap<>();
        candidates.forEach(p -> nodeOfPod.put(p.getMetadata().getName(), nodeOf(p)));
        Set<String> nodes = new TreeSet<>();
        List<String> notOnANode = new ArrayList<>();
        for (String name : picked) {
            String node = nodeOfPod.get(name);
            if (node != null) {
                nodes.add(node);
            } else {
                notOnANode.add(name);
            }
        }
        if (nodes.size() > 1) {
            throw new IllegalArgumentException("NODE_DRAIN with targetAll picks pods on " + nodes.size() + " nodes ("
                    + String.join(", ", nodes) + "), and node-drain drains one. Narrow targetLabel to the pods of one"
                    + " node, or name the node in envOverrides.TARGET_NODE");
        }
        List<String> notes = new ArrayList<>();
        if (mode == PodTargets.Mode.BROKER_ID && !picked.getFirst().endsWith("-" + spec.targetBrokerId())) {
            notes.add(brokerIdFallback(spec, picked.getFirst()));
        }
        if (!notOnANode.isEmpty()) {
            notes.add("pod " + String.join(", ", notOnANode) + " is not on a node yet, and the step fails without"
                    + " draining if it still isn't when the step runs");
        }
        if (nodes.isEmpty()) {
            return new Drain(Impact.NONE, String.join(",", picked), notes);
        }
        String node = nodes.iterator().next();
        Drain drain = onNode(
                node,
                String.join(",", picked),
                kafkaPods,
                "NODE_DRAIN drains node " + node + ", the node of " + String.join(", ", picked));
        notes.addAll(drain.notes());
        return new Drain(drain.impact(), drain.target(), notes);
    }

    /** Every Kafka pod on {@code node}, with a note naming it, which {@code lead} begins. */
    private static Drain onNode(String node, String target, List<Pod> kafkaPods, String lead) {
        List<Pod> onIt = kafkaPods.stream()
                .filter(p -> node.equals(nodeOf(p)))
                .sorted(Comparator.comparing(p -> p.getMetadata().getName()))
                .toList();
        List<String> pods = onIt.stream().map(p -> p.getMetadata().getName()).toList();
        List<String> brokers = onIt.stream()
                .filter(PodTargets::isBroker)
                .map(p -> p.getMetadata().getName())
                .toList();
        String note = lead + (onIt.isEmpty() ? ", which runs no Kafka pod" : ", and evicts every pod on it");
        return new Drain(new Impact(pods, brokers), target, List.of(note));
    }

    /**
     * The worst case among the nodes a drain may land on: the one that runs
     * the most brokers, and of those the first by name.
     */
    private static Drain worstOf(List<String> nodes, List<Pod> kafkaPods, String lead) {
        if (nodes.isEmpty()) {
            return nothing(lead + ", and no Kafka pod is on a node, so the count takes none");
        }
        String worst = nodes.stream()
                .sorted()
                .max(Comparator.comparingLong(n -> kafkaPods.stream()
                        .filter(p -> n.equals(nodeOf(p)) && PodTargets.isBroker(p))
                        .count()))
                .orElseThrow();
        Drain drain = onNode(worst, null, kafkaPods, lead);
        int brokers = drain.impact().brokers().size();
        return new Drain(
                drain.impact(),
                null,
                List.of(lead + "; the count takes the worst case, node " + worst + ", which runs " + brokers
                        + (brokers == 1 ? " broker" : " brokers")));
    }

    private static Drain nothing(String why) {
        return new Drain(Impact.NONE, null, List.of(why));
    }

    /** The node a pod runs on, or null when it is on none yet. */
    private static String nodeOf(Pod pod) {
        String node = pod.getSpec() != null ? pod.getSpec().getNodeName() : null;
        return node == null || node.isBlank() ? null : node;
    }

    /** What the dry run says about a SCALE_DOWN step, beyond the brokers it removes. */
    private List<String> scaleDownWarnings(FaultSpec spec, List<Pod> kafkaPods, List<String> removed) {
        List<String> warnings = new ArrayList<>();
        boolean nodePools = false;
        if (spec.targetNamespace() == null || spec.targetNamespace().equals(kafkaNamespace)) {
            try {
                ScaleDownTargets.Preview preview = ScaleDownTargets.preview(spec, kafkaPods);
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
