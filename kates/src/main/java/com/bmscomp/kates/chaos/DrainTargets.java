package com.bmscomp.kates.chaos;

import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.Set;
import java.util.TreeSet;
import java.util.concurrent.ThreadLocalRandom;
import java.util.function.Predicate;

import io.fabric8.kubernetes.api.model.Node;
import io.fabric8.kubernetes.client.KubernetesClient;
import org.jboss.logging.Logger;

/**
 * The node a NODE_DRAIN drains, picked the same way on both chaos providers:
 * the node of the pods {@link PodTargets} picks, or the one envOverrides
 * names (TARGET_NODE) or matches (NODE_LABEL). Never the node the Kates API
 * runs on ({@link KatesNode}).
 */
final class DrainTargets {

    private static final Logger LOG = Logger.getLogger(DrainTargets.class);

    private DrainTargets() {}

    /**
     * The node the kubernetes provider drains. TARGET_NODE names it; NODE_LABEL
     * has Kates pick one of the nodes it matches at random, as Litmus would;
     * otherwise it is the node of the pods the step picks.
     *
     * @throws IllegalStateException when the drain can take no node but the
     *     Kates API's, NODE_LABEL matches no node, or {@link #nodeOfPods} fails
     */
    static String node(KubernetesClient client, FaultSpec spec, Optional<String> kates) {
        Map<String, String> overrides = spec.envOverrides() != null ? spec.envOverrides() : Map.of();
        String targetNode = overrides.get("TARGET_NODE");
        if (targetNode != null && !targetNode.isBlank()) {
            if (kates.isPresent() && targetNode.equals(kates.get())) {
                throw new IllegalStateException(KatesNode.refusal("envOverrides.TARGET_NODE names", targetNode));
            }
            return targetNode;
        }
        String nodeLabel = overrides.get("NODE_LABEL");
        if (nodeLabel != null && !nodeLabel.isBlank()) {
            List<String> matching = client.nodes().list().getItems().stream()
                    .filter(drainedByOverride(spec))
                    .map(n -> n.getMetadata().getName())
                    .sorted()
                    .toList();
            List<String> others = matching.stream()
                    .filter(n -> kates.isEmpty() || !n.equals(kates.get()))
                    .toList();
            if (others.isEmpty()) {
                throw new IllegalStateException(
                        matching.isEmpty()
                                ? "NODE_DRAIN: envOverrides.NODE_LABEL '" + nodeLabel + "' matches no node"
                                : KatesNode.refusal(
                                        "envOverrides.NODE_LABEL '" + nodeLabel + "' matches only", kates.get()));
            }
            String node = others.get(ThreadLocalRandom.current().nextInt(others.size()));
            LOG.info("NODE_DRAIN: node " + node + ", one envOverrides.NODE_LABEL matches");
            return node;
        }
        return nodeOfPods(client, spec, kates);
    }

    /**
     * The node a drain drains: the one the pods it picks run on
     * ({@link PodTargets}), so it hits the pod its targetPod, targetBrokerId
     * or selector picks, as every other fault does. node-drain, given no
     * TARGET_NODE, drains the node of a random pod in any namespace, the
     * control plane's among them, and Kates used to give it none. It drains
     * one node, so the pods a targetAll drain picks must all run on the same
     * one. It never drains {@code kates}, the node the Kates API runs on: a
     * random pick chooses among the pods on other nodes.
     *
     * @throws IllegalStateException when a pod is gone or not on a node yet,
     *     the pods run on more than one node, or their node is the Kates
     *     API's; the fault fails before anything is drained
     */
    static String nodeOfPods(KubernetesClient client, FaultSpec spec, Optional<String> kates) {
        List<String> pods = kates.isPresent() && PodTargets.mode(spec) == PodTargets.Mode.ONE_RANDOM
                ? onePodAwayFrom(client, spec, kates.get())
                : PodTargets.resolve(client, spec);
        Set<String> nodes = new TreeSet<>();
        for (String name : pods) {
            var pod = client.pods()
                    .inNamespace(spec.targetNamespace())
                    .withName(name)
                    .get();
            if (pod == null) {
                throw new IllegalStateException("NODE_DRAIN: pod " + name + " was not found in namespace '"
                        + spec.targetNamespace() + "', so there is no node to drain");
            }
            String node = pod.getSpec() != null ? pod.getSpec().getNodeName() : null;
            if (node == null || node.isBlank()) {
                throw new IllegalStateException(
                        "NODE_DRAIN: pod " + name + " is not on a node yet, so there is no node to drain");
            }
            nodes.add(node);
        }
        if (nodes.size() > 1) {
            throw new IllegalStateException("NODE_DRAIN: the pods targetAll picks run on " + nodes.size() + " nodes ("
                    + String.join(", ", nodes) + "), and node-drain drains one. Narrow targetLabel to the pods of"
                    + " one node, or name the node in envOverrides.TARGET_NODE");
        }
        String node = nodes.iterator().next();
        if (kates.isPresent() && node.equals(kates.get())) {
            throw new IllegalStateException(
                    KatesNode.refusal(KatesNode.picked(pods), node) + ". Aim the drain at a pod on another node");
        }
        LOG.info("NODE_DRAIN: TARGET_NODE " + node + ", the node of " + String.join(", ", pods));
        return node;
    }

    /**
     * The pod a random drain takes: one of those targetLabel matches that
     * don't run on {@code kates}, the node the Kates API runs on.
     *
     * @throws IllegalStateException when every pod it matches runs there, or
     *     it matches none
     */
    private static List<String> onePodAwayFrom(KubernetesClient client, FaultSpec spec, String kates) {
        ParsedLabelSelector selector = ParsedLabelSelector.parse(spec.targetLabel());
        var matching = client.pods()
                .inNamespace(spec.targetNamespace())
                .withLabelSelector(selector.toString())
                .list()
                .getItems();
        if (matching.isEmpty()) {
            return PodTargets.resolve(client, spec);
        }
        var elsewhere = matching.stream()
                .filter(p -> p.getSpec() == null || !kates.equals(p.getSpec().getNodeName()))
                .toList();
        if (elsewhere.isEmpty()) {
            throw new IllegalStateException(KatesNode.refusal("every pod targetLabel matches runs on", kates));
        }
        return PodTargets.select(spec, elsewhere);
    }

    /**
     * The nodes an override may have node-drain drain: the one TARGET_NODE
     * names, or else those NODE_LABEL matches. Null when both are empty, and
     * node-drain then drains the node of a random pod in any namespace, which
     * no node is safe from.
     */
    static Predicate<Node> drainedByOverride(FaultSpec spec) {
        String targetNode = spec.envOverrides().get("TARGET_NODE");
        if (targetNode != null && !targetNode.isBlank()) {
            return n -> targetNode.equals(n.getMetadata().getName());
        }
        String nodeLabel = spec.envOverrides().get("NODE_LABEL");
        if (nodeLabel != null && !nodeLabel.isBlank()) {
            ParsedLabelSelector selector = ParsedLabelSelector.parse(nodeLabel);
            return n -> selector.matches(
                    n.getMetadata().getLabels() != null ? n.getMetadata().getLabels() : Map.of());
        }
        return null;
    }
}
