package com.bmscomp.kates.chaos;

import java.util.List;
import java.util.Map;
import java.util.concurrent.ThreadLocalRandom;

import io.fabric8.kubernetes.api.model.Pod;
import io.fabric8.kubernetes.client.KubernetesClient;

/**
 * Decides which pods a pod-scoped fault hits. Both chaos backends and the
 * safety guard go through here, so a plan hits — and is counted against
 * {@code maxAffectedBrokers} as — the same pods whichever backend runs it.
 *
 * <p>Precedence: {@code targetPod}; then {@code targetAll}, every pod
 * {@code targetLabel} matches; then {@code targetBrokerId}; then one random
 * matching pod. {@code targetAll} outranks {@code targetBrokerId}, so a broker
 * ID the spec also carries never narrows it to one pod.
 *
 * <p>A {@code ROLLING_RESTART} without {@code targetPod} always takes every
 * matching pod, as if {@code targetAll} were set: it restarts them one at a
 * time, so the safety guard counts only one of them against
 * {@code maxAffectedBrokers}.
 *
 * <p>{@code targetBrokerId} picks among the brokers the selector matches
 * ({@link #isBroker}). {@code strimzi.io/component-type=kafka} matches KRaft
 * controllers too, and a dedicated controller's node ID used to pick it.
 */
public final class PodTargets {

    public enum Mode {
        NAMED_POD,
        ALL,
        BROKER_ID,
        ONE_RANDOM
    }

    private PodTargets() {}

    public static Mode mode(FaultSpec spec) {
        if (spec.targetPod() != null && !spec.targetPod().isEmpty()) {
            return Mode.NAMED_POD;
        }
        if (spec.targetAll() || spec.disruptionType() == DisruptionType.ROLLING_RESTART) {
            return Mode.ALL;
        }
        if (spec.targetBrokerId() >= 0) {
            return Mode.BROKER_ID;
        }
        return Mode.ONE_RANDOM;
    }

    /**
     * The pods the fault hits, looking {@code targetLabel} up in
     * {@code targetNamespace}. Never empty: a selector that matches nothing is
     * an error, not a no-op.
     */
    public static List<String> resolve(KubernetesClient client, FaultSpec spec) {
        if (mode(spec) == Mode.NAMED_POD) {
            return List.of(spec.targetPod());
        }
        ParsedLabelSelector selector = ParsedLabelSelector.parse(spec.targetLabel());
        List<Pod> pods = client.pods()
                .inNamespace(spec.targetNamespace())
                .withLabelSelector(selector.toString())
                .list()
                .getItems();
        List<String> targets = select(spec, pods);
        if (targets.isEmpty()) {
            throw new IllegalStateException((pods.isEmpty()
                            ? "No pods found"
                            : "targetBrokerId picks brokers only, and no broker pod was found")
                    + " matching label selector '" + selector + "' in namespace '" + spec.targetNamespace()
                    + "'");
        }
        return targets;
    }

    /**
     * The pods among {@code matching} that the fault hits; empty only when
     * {@code matching} is, or holds no broker for {@code targetBrokerId}.
     */
    public static List<String> select(FaultSpec spec, List<Pod> matching) {
        return switch (mode(spec)) {
            case NAMED_POD -> List.of(spec.targetPod());
            case ALL -> matching.stream().map(PodTargets::name).toList();
            // Falls back to the first broker when no broker the selector
            // matches has a name ending in -<id>.
            case BROKER_ID -> {
                List<String> brokers = matching.stream()
                        .filter(PodTargets::isBroker)
                        .map(PodTargets::name)
                        .toList();
                yield brokers.isEmpty()
                        ? List.of()
                        : List.of(brokers.stream()
                                .filter(n -> n.endsWith("-" + spec.targetBrokerId()))
                                .findFirst()
                                .orElse(brokers.getFirst()));
            }
            case ONE_RANDOM ->
                matching.isEmpty()
                        ? List.of()
                        : List.of(name(matching.get(ThreadLocalRandom.current().nextInt(matching.size()))));
        };
    }

    /**
     * Whether a Kafka pod runs a broker. Strimzi labels every pod of a node
     * pool with its roles: a dedicated KRaft controller has
     * {@code strimzi.io/broker-role=false}, and a node with both roles counts
     * as a broker. A pod without the label, such as Kafka not run by Strimzi,
     * is taken for a broker, since nothing says it is not one.
     */
    public static boolean isBroker(Pod pod) {
        Map<String, String> labels = pod.getMetadata().getLabels();
        String brokerRole = labels != null ? labels.get(ScaleDownTargets.BROKER_ROLE_LABEL) : null;
        return brokerRole == null || "true".equals(brokerRole);
    }

    private static String name(Pod pod) {
        return pod.getMetadata().getName();
    }
}
