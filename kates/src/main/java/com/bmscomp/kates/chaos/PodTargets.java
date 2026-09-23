package com.bmscomp.kates.chaos;

import java.util.List;
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
 * matching pod. {@code targetAll} outranks {@code targetBrokerId} because a
 * FaultSpec posted as JSON without {@code targetBrokerId} carries 0, not -1.
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
        if (spec.targetAll()) {
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
            throw new IllegalStateException("No pods found matching label selector '" + selector + "' in namespace '"
                    + spec.targetNamespace() + "'");
        }
        return targets;
    }

    /** The pods among {@code matching} that the fault hits; empty only when {@code matching} is. */
    public static List<String> select(FaultSpec spec, List<Pod> matching) {
        return switch (mode(spec)) {
            case NAMED_POD -> List.of(spec.targetPod());
            case ALL -> matching.stream().map(PodTargets::name).toList();
            // Falls back to the first match when no pod name ends in -<id>:
            // a JSON FaultSpec that never set targetBrokerId relies on it.
            case BROKER_ID ->
                matching.isEmpty()
                        ? List.of()
                        : List.of(matching.stream()
                                .map(PodTargets::name)
                                .filter(n -> n.endsWith("-" + spec.targetBrokerId()))
                                .findFirst()
                                .orElse(name(matching.getFirst())));
            case ONE_RANDOM ->
                matching.isEmpty()
                        ? List.of()
                        : List.of(name(matching.get(ThreadLocalRandom.current().nextInt(matching.size()))));
        };
    }

    private static String name(Pod pod) {
        return pod.getMetadata().getName();
    }
}
